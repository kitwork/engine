package work

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kitwork/engine/storage"
	"github.com/kitwork/engine/value"
)

// context.sse (KITWORK_SSE.md) driven through a real server: a browser's GET held open as the stream, a
// POST that sends into it, every frame read off the wire as EventSource reads it.

// streamServer serves files (each a folder's router.work, under storageHeader) over HTTP.
func streamServer(t *testing.T, files map[string]string) (*Tenant, *httptest.Server) {
	t.Helper()
	tn := storageSite(t, files)
	server := httptest.NewServer(http.HandlerFunc(tn.Serve))
	t.Cleanup(server.Close)
	return tn, server
}

// openStream GETs path and gives the stream's reader; the connection closes when the test ends or cancel
// is called.
func openStream(t *testing.T, server *httptest.Server, path string) (*http.Response, *bufio.Reader, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+path, nil)
	response, err := server.Client().Do(request)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); response.Body.Close() })
	return response, bufio.NewReader(response.Body), cancel
}

// readFrame reads one frame (up to its blank line), or fails the test after two seconds; "" at the end of
// the stream.
func readFrame(t *testing.T, reader *bufio.Reader) string {
	t.Helper()
	read := make(chan string, 1)
	go func() {
		var frame strings.Builder
		for {
			line, err := reader.ReadString('\n')
			frame.WriteString(line)
			if err != nil || line == "\n" {
				read <- frame.String()
				return
			}
		}
	}()
	select {
	case frame := <-read:
		return frame
	case <-time.After(2 * time.Second):
		t.Fatal("no frame within 2s")
		return ""
	}
}

// streamPost sends body as JSON to path and gives the status and answer.
func streamPost(t *testing.T, server *httptest.Server, path, body string) (int, string) {
	t.Helper()
	response, err := server.Client().Post(server.URL+path, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	answer, _ := io.ReadAll(response.Body)
	return response.StatusCode, string(answer)
}

// streamWaitFor polls condition until it holds, or fails the test after two seconds.
func streamWaitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("%s: not within 2s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// The spec's example end to end (mục 1–3, 5, 6): reconnect("3s") is retry: 3000 once; a message sent before
// the handler returned goes out as the stream opens; a POST's send reaches the observer, whose function
// sends it on as it was (event, id, data); data is always JSON, a string included; subscribe is the short
// form; the chain form sends at .data and its event and id stick to nothing after. The stream's headers are
// the engine's.
//
// CONTROL: with sseFrame writing a string's data without its quotes, "the message sent before return =
// \"event: hello\ndata: hi\n\n\"". With open() not writing the buffer, the hello frame never came ("no frame
// within 2s").
func TestStreamCarriesSendsToTheBrowser(t *testing.T) {
	_, server := streamServer(t, map[string]string{
		"events/router.work": `router.get((context) => {
  const stream = context.sse.reconnect("3s");
  stream.send({ event: "hello", data: "hi" });
  memory("rooms").segment("lobby").observe((message) => stream.send(message));
  return stream;
});
router.post((context) => {
  const { text } = context.request.json();
  if (typeof text !== "string") { return context.status(400).json({ message: "Tin không hợp lệ." }); }
  memory("rooms").segment("lobby").send({ event: "chat", id: "msg-1", data: { text } });
  return context.json({ ok: true });
});`,
		"short/router.work":  `router.get((context) => context.sse.subscribe(memory("rooms").segment("lobby")));`,
		"headed/router.work": `router.get((context) => context.header("X-Room", "lobby").sse.subscribe(memory("rooms").segment("lobby")));`,
		"chain/router.work": `router.get((context) => {
  const stream = context.sse;
  stream.event("chat").id("7").data({ n: 1 });
  stream.data(2);
  return stream;
});`,
	})
	response, reader, _ := openStream(t, server, "/events")
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "text/event-stream" || response.Header.Get("Cache-Control") != "no-cache" {
		t.Fatalf("GET /events = %d %v", response.StatusCode, response.Header)
	}
	if frame := readFrame(t, reader); frame != "retry: 3000\n\n" {
		t.Fatalf("first frame = %q, want retry: 3000", frame)
	}
	if frame := readFrame(t, reader); frame != "event: hello\ndata: \"hi\"\n\n" {
		t.Fatalf("the message sent before return = %q", frame)
	}
	_, short, _ := openStream(t, server, "/short")
	headed, _, _ := openStream(t, server, "/headed")
	if headed.Header.Get("X-Room") != "lobby" || headed.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("an edit before context.sse: %v", headed.Header)
	}
	if status, body := streamPost(t, server, "/events", `{"text":"Xin chào"}`); status != http.StatusOK || body != `{"ok":true}` {
		t.Fatalf("POST = %d %s", status, body)
	}
	want := "event: chat\nid: msg-1\ndata: {\"text\":\"Xin chào\"}\n\n"
	if frame := readFrame(t, reader); frame != want {
		t.Fatalf("the observer's frame = %q, want %q", frame, want)
	}
	if frame := readFrame(t, short); frame != want {
		t.Fatalf("subscribe's frame = %q, want %q", frame, want)
	}
	if status, _ := streamPost(t, server, "/events", `{"text":5}`); status != http.StatusBadRequest {
		t.Fatalf("POST of a number = %d, want 400", status)
	}
	_, chain, _ := openStream(t, server, "/chain")
	if frame := readFrame(t, chain); frame != "event: chat\nid: 7\ndata: {\"n\":1}\n\n" {
		t.Fatalf("chain = %q", frame)
	}
	if frame := readFrame(t, chain); frame != "data: 2\n\n" {
		t.Fatalf("after the chain = %q, want no event or id", frame)
	}
}

// send always takes { event, id, data } (STORAGE mục 7, SSE mục 2): data alone, an unknown key, data
// missing, a line break in event, are INVALID_ARGUMENT with the spec's words; so is stream.send of bare
// data.
//
// CONTROL: with storageEnvelope taking a map without data as the data (the old sse.publish guess), send({
// text }) and send({ event }) went through: "checked frame lacks … khoá \"text\" không có trong phong bì", "…
// thiếu data".
func TestStreamSendTakesTheEnvelope(t *testing.T) {
	_, server := streamServer(t, map[string]string{
		"send/router.work": `const outcome = (r) => r.ok ? "ok" : r.error.code + ": " + r.error.message;
router.get((context) => {
  const stream = context.sse;
  const room = memory("rooms").segment(1);
  stream.send({ event: "checked", data: [
    outcome(safe(() => room.send({ text: "Xin chào" }))),
    outcome(safe(() => room.send({ event: "chat" }))),
    outcome(safe(() => room.send({ event: "a\nb", data: 1 }))),
    outcome(safe(() => room.send({ id: 5, data: 1 }))),
    outcome(safe(() => stream.send("x"))),
    outcome(safe(() => room.send({ data: { text: "x" }, id: "1" })))
  ] });
  return stream;
});`,
	})
	_, reader, _ := openStream(t, server, "/send")
	frame := readFrame(t, reader)
	for _, want := range []string{
		`INVALID_ARGUMENT: send nhận { event, id, data }; dữ liệu đặt trong data: khoá \"text\" không có trong phong bì`,
		`INVALID_ARGUMENT: send nhận { event, id, data }; dữ liệu đặt trong data: thiếu data`,
		`INVALID_ARGUMENT: send nhận { event, id, data }; dữ liệu đặt trong data: event không chứa xuống dòng`,
		`INVALID_ARGUMENT: send nhận { event, id, data }; dữ liệu đặt trong data: id là chuỗi, ở đây là số`,
		`INVALID_ARGUMENT: stream.send nhận { event, id, data }; dữ liệu đặt trong data: ở đây là chuỗi, stream.send({ data: … })`,
		`"ok"]`,
	} {
		if !strings.Contains(frame, want) {
			t.Errorf("checked frame lacks %s:\n%s", want, frame)
		}
	}
}

// context.sse chooses the answer of a GET handler (mục 1): in a POST it is an error, as is a handler that
// read it and returns anything but the stream, and a route that caches; none of them opens a stream. Each
// is the request's failure, logged with its message.
//
// CONTROL: with the return check removed from serveTree, GET /number opened a stream: "/number = 200
// text/event-stream, want the failure, no stream".
func TestStreamOnlyFromAGetHandlerThatReturnsIt(t *testing.T) {
	records := captureSlog(t)
	tn, server := streamServer(t, map[string]string{
		"post/router.work":   `router.post((context) => context.sse);`,
		"other/router.work":  `router.get((context) => { const stream = context.sse; return context.json({ ok: true }); });`,
		"cached/router.work": `router.get((context) => context.sse).cache("1m");`,
		"titled/router.work": `router.get((context) => context.title("Luồng").sse);`,
		"number/router.work": `router.get((context) => { const stream = context.sse; return 42; });`,
	})
	_ = tn
	for path, want := range map[string]string{
		"/post":   "context.sse chỉ dùng trong router.get: EventSource của trình duyệt chỉ gửi GET, route này trả lời POST; tin đi vào bằng router.post gửi send({ event, id, data }) (KITWORK_SSE.md mục 1)",
		"/other":  "`.json()` sau context.sse: route đã mở luồng SSE thì trả lời bằng luồng, return stream; không trả lời gì khác (KITWORK_SSE.md mục 1)",
		"/cached": "route mở luồng SSE không cache: bỏ .cache() hay .persist() trên route này; mỗi kết nối là một luồng của riêng nó (KITWORK_SSE.md mục 1)",
		"/titled": "`.title()` là chữ của trang, không có nghĩa với luồng SSE: bỏ `.title()` trước context.sse; chỉnh sửa của luồng là header, headers, cookie (KITWORK_SSE.md mục 1)",
		"/number": "route này đã mở luồng SSE (context.sse); trả về stream: return stream, hay return context.sse.subscribe(…) (KITWORK_SSE.md mục 1)",
	} {
		var response *http.Response
		var err error
		if path == "/post" {
			response, err = server.Client().Post(server.URL+path, "application/json", strings.NewReader("{}"))
		} else {
			response, err = server.Client().Get(server.URL + path)
		}
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusInternalServerError || response.Header.Get("Content-Type") == "text/event-stream" {
			t.Errorf("%s = %d %s, want the failure, no stream", path, response.StatusCode, response.Header.Get("Content-Type"))
		}
		found := false
		for _, record := range records.named("Kitwork runtime execution failed") {
			found = found || (record["path"] == path && strings.Contains(record["message"], want))
		}
		if !found {
			t.Errorf("%s: no failure logged with %q: %v", path, want, records.named("Kitwork runtime execution failed"))
		}
	}
}

// An observer lives as long as the connection that registered it (mục 4): while the stream is open the
// request holds its VM and its observer; once the browser goes, both are released, with nothing to clean by
// hand.
//
// CONTROL: with requestEvents.close not closing its observers, the lobby kept its observer after the client
// went: "the observer closed with the connection: not within 2s".
func TestStreamObserversEndWithTheConnection(t *testing.T) {
	tn, server := streamServer(t, map[string]string{
		"events/router.work": `router.get((context) => context.sse.subscribe(memory("rooms").segment("lobby")));`,
	})
	app, err := tn.storage()
	if err != nil {
		t.Fatal(err)
	}
	key := storage.Key{Place: storage.Memory, Name: "rooms", Segments: []string{"lobby"}}.Encoded()
	before := VMPoolHealth().Active
	_, reader, cancel := openStream(t, server, "/events")
	go io.Copy(io.Discard, reader)
	streamWaitFor(t, "the stream's observer", func() bool { return app.hub.Observers(key) == 1 && tn.OpenStreams() == 1 })
	if held := VMPoolHealth().Active; held != before+1 {
		t.Fatalf("active VMs while the stream is open = %d, want %d: the request holds its VM", held, before+1)
	}
	cancel()
	streamWaitFor(t, "the observer closed with the connection", func() bool { return app.hub.Observers(key) == 0 && tn.OpenStreams() == 0 })
	streamWaitFor(t, "the VM released", func() bool { return VMPoolHealth().Active == before })
}

// Each run of an observer's function has the app's MaxEnergy of its own (mục 4): a function that spends
// most of the budget runs for every message, not until the request's budget is gone.
//
// CONTROL: with runObserver not resetting the VM (FastResetPrepared) before each run, the second run hit
// ENERGY_LIMIT and closed the stream: "round 2 = \"\"", ENERGY_LIMIT in the log.
func TestStreamEachCallbackHasItsOwnBudget(t *testing.T) {
	tn, server := streamServer(t, map[string]string{
		"events/router.work": `router.get((context) => {
  const stream = context.sse;
  memory("work").observe((message) => {
    let x = 0;
    for (let i = 0; i < message.data; i++) { x = x + i; }
    stream.send({ event: "done", data: x });
  });
  return stream;
});
router.post((context) => { memory("work").send({ data: context.request.json().n }); return context.json(true); });`,
	})
	tn.MaxEnergy = 600_000
	records := captureSlog(t)
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("log: %v", records.named("Kitwork runtime execution failed"))
		}
	})
	_, reader, _ := openStream(t, server, "/events")
	streamWaitFor(t, "the observer", func() bool { app, _ := tn.storage(); return app.hub.Count() == 1 })
	for round := 1; round <= 3; round++ {
		streamPost(t, server, "/events", `{"n":10000}`)
		if frame := readFrame(t, reader); frame != "event: done\ndata: 49995000\n\n" {
			t.Fatalf("round %d = %q", round, frame)
		}
	}
}

// An observer that falls behind is closed, not given memory without end, and the sender never waits for it
// (mục 7, 8): its function is held on the first message while more are sent than its queue holds; every send
// returns at once, and once the function is let go the stream closes, with a line in the log.
//
// CONTROL: with Observer.deliver queueing past the limit, the stream stayed open after the function was let
// go: "after the queue filled = \"data: 1\n\n\", want the stream closed".
func TestStreamSlowObserverClosesTheStream(t *testing.T) {
	records := captureSlog(t)
	saved := storageQueueLimit
	storageQueueLimit = 2
	t.Cleanup(func() { storageQueueLimit = saved })
	held := newGate()
	var calls atomic.Int32
	notifyHook(t, func(name string, _ ...value.Value) {
		if name == "slow" && calls.Add(1) == 1 {
			held.wait(t, "the test lets the slow function go")
		}
	})
	tn, server := streamServer(t, map[string]string{
		"events/router.work": `router.get((context) => {
  const stream = context.sse;
  memory("feed").observe((message) => { testNotify("slow"); stream.send(message); });
  return stream;
});
router.post((context) => { memory("feed").send({ data: 1 }); return context.json(true); });`,
	})
	_, reader, _ := openStream(t, server, "/events")
	streamWaitFor(t, "the observer", func() bool { app, _ := tn.storage(); return app.hub.Count() == 1 })
	streamPost(t, server, "/events", "{}")
	streamWaitFor(t, "the function is held", func() bool { return calls.Load() == 1 })
	started := time.Now()
	for range 4 {
		streamPost(t, server, "/events", "{}")
	}
	if took := time.Since(started); took > time.Second {
		t.Fatalf("four sends took %s: the sender waited for the slow observer", took)
	}
	held.open()
	if frame := readFrame(t, reader); frame != "data: 1\n\n" {
		t.Fatalf("the held message = %q", frame)
	}
	if frame := readFrame(t, reader); frame != "" {
		t.Fatalf("after the queue filled = %q, want the stream closed", frame)
	}
	if len(records.named("Kitwork stream closed: an observer's queue was full")) != 1 {
		t.Fatalf("no line in the log for the stream closed")
	}
}

// A message reaches each observer as a value of its own: two streams whose functions change message.data
// never see each other's change (run with -race: a shared object would be a data race).
//
// CONTROL: with messageValue handing every observer the one value decoded first, a stream read the other's
// mark too: "a = \"data: [\\\"b\\\",\\\"a\\\"]\n\n\"".
func TestStreamObserversGetCopies(t *testing.T) {
	tn, server := streamServer(t, map[string]string{
		"events/router.work": `router.get((context) => {
  const stream = context.sse;
  const mark = context.query("mark");
  memory("shared").observe((message) => { message.data.marks.push(mark); stream.send({ data: message.data.marks }); });
  return stream;
});
router.post((context) => { memory("shared").send({ data: { marks: [] } }); return context.json(true); });`,
	})
	_, a, _ := openStream(t, server, "/events?mark=a")
	_, b, _ := openStream(t, server, "/events?mark=b")
	streamWaitFor(t, "two observers", func() bool { app, _ := tn.storage(); return app.hub.Count() == 2 })
	streamPost(t, server, "/events", "{}")
	if frame := readFrame(t, a); frame != "data: [\"a\"]\n\n" {
		t.Errorf("a = %q", frame)
	}
	if frame := readFrame(t, b); frame != "data: [\"b\"]\n\n" {
		t.Errorf("b = %q", frame)
	}
}

// Each app has a ceiling of open streams: one more is 503 as it connects, and the streams open stay open
// (mục 8).
//
// CONTROL: with serveStream not entering the app's slots, the second stream opened: 200.
func TestStreamCeilingAnswers503(t *testing.T) {
	saved := streamLimit
	streamLimit = 1
	t.Cleanup(func() { streamLimit = saved })
	tn, server := streamServer(t, map[string]string{
		"events/router.work": `router.get((context) => context.sse.subscribe(memory("x")));`,
	})
	first, reader, _ := openStream(t, server, "/events")
	go io.Copy(io.Discard, reader)
	streamWaitFor(t, "the first stream", func() bool { return tn.OpenStreams() == 1 })
	second, err := server.Client().Get(server.URL + "/events")
	if err != nil {
		t.Fatal(err)
	}
	second.Body.Close()
	if first.StatusCode != http.StatusOK || second.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("first %d, second %d: want 200 then 503", first.StatusCode, second.StatusCode)
	}
	if tn.OpenStreams() != 1 {
		t.Fatalf("the open stream was closed by the refused one")
	}
}

// A generation that closes (hot reload, the site removed) closes its streams before it waits for its
// requests: Close returns, the browser reads the end and connects again to the new generation (mục 4).
//
// CONTROL: with Tenant.Close not closing its streams, Close waited for the open request: "Close did not
// return within 2s".
func TestStreamClosesWithItsGeneration(t *testing.T) {
	tn, server := streamServer(t, map[string]string{
		"events/router.work": `router.get((context) => context.sse.subscribe(memory("x")));`,
	})
	_, reader, _ := openStream(t, server, "/events")
	ended := make(chan string, 1)
	go func() {
		_, err := reader.ReadString('\n')
		if err == io.EOF {
			ended <- "eof"
			return
		}
		ended <- "read"
	}()
	streamWaitFor(t, "the stream", func() bool { return tn.OpenStreams() == 1 })
	closed := make(chan struct{})
	go func() { tn.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not return within 2s")
	}
	select {
	case how := <-ended:
		if how != "eof" {
			t.Fatalf("the reader got %s, want the end of the stream", how)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the browser never read the end")
	}
}

// sse of the old spellings: a handler that takes sse after its context fails to load, naming context.sse;
// connect and publish read off the stream name what replaced them.
//
// CONTROL: with removedParameterError never refusing, the site loaded: "run = <nil>, want the sse parameter
// refused".
func TestStreamOldSpellingsAreErrors(t *testing.T) {
	root := writeSite(t, map[string]string{
		"router.work":        `import * as router from "kitwork/router";`,
		"events/router.work": storageHeader + `router.get((context, sse) => sse.connect({ channel: "x" }));`,
	})
	tn := NewTenant(root, "localhost")
	err := tn.Run()
	tn.Close()
	if err == nil || !strings.Contains(err.Error(), "tham số sse đã bỏ: luồng là context.sse trong router.get") {
		t.Fatalf("run = %v, want the sse parameter refused", err)
	}
	_, server := streamServer(t, map[string]string{
		"events/router.work": `router.get((context) => {
  const stream = context.sse;
  const outcome = (r) => r.error.message;
  stream.send({ data: [outcome(safe(() => stream.connect({ channel: "x" }))), outcome(safe(() => stream.publish("x", 1)))] });
  return stream;
});`,
	})
	_, reader, _ := openStream(t, server, "/events")
	frame := readFrame(t, reader)
	for _, want := range []string{
		`sse.connect đã bỏ: context.sse đã là luồng; nghe một khoá bằng context.sse.subscribe(memory(\"rooms\").segment(1))`,
		`sse.publish đã bỏ: memory(\"rooms\").segment(1).send({ event, id, data })`,
	} {
		if !strings.Contains(frame, want) {
			t.Errorf("frame lacks %s:\n%s", want, frame)
		}
	}
}

// An open stream writes a comment now and then, so a proxy or the browser keeps the connection (mục 4); a
// HEAD of the route answers the stream's headers and holds nothing open.
//
// CONTROL: with the heartbeat case taken out of serveStream's loop, "no frame within 2s".
func TestStreamHeartbeatAndHead(t *testing.T) {
	saved := streamHeartbeat
	streamHeartbeat = 20 * time.Millisecond
	t.Cleanup(func() { streamHeartbeat = saved })
	tn, server := streamServer(t, map[string]string{
		"events/router.work": `router.get((context) => context.sse.subscribe(memory("quiet")));`,
	})
	_, reader, _ := openStream(t, server, "/events")
	if frame := readFrame(t, reader); frame != ": heartbeat\n\n" {
		t.Fatalf("a quiet stream wrote %q, want a heartbeat", frame)
	}
	request, _ := http.NewRequest(http.MethodHead, server.URL+"/events", nil)
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("HEAD = %d %v", response.StatusCode, response.Header)
	}
	streamWaitFor(t, "the HEAD holds nothing open", func() bool { return tn.OpenStreams() == 1 })
}

// An observer belongs to the request that registered it (STORAGE mục 7): one of a route that answers at
// once ends with its answer, close() ends one early, and where no request runs it (a function of
// routine.wait here, a job or a schedule) observe is refused for now, naming the gap.
//
// CONTROL: with eventObserver.OnInvoke("close") not closing the observer, "after close() the hub had 1".
func TestStreamObserveBelongsToARequest(t *testing.T) {
	tn, _ := streamServer(t, map[string]string{
		"plain/router.work": `router.get((context) => { memory("x").observe((message) => 1); return context.json("answered"); });`,
		"early/router.work": `router.get((context) => {
  const stream = context.sse;
  const observer = memory("y").observe((message) => stream.send(message));
  observer.close();
  return stream;
});`,
		"routine/router.work": `import * as routine from "kitwork/routine";
router.get((context) => {
  const r = safe(() => routine.wait(() => memory("z").observe((message) => 1)));
  return context.json({ code: r.error.code, message: r.error.message });
});`,
	})
	app, err := tn.storage()
	if err != nil {
		t.Fatal(err)
	}
	if code, body := storageGet(t, tn, "/plain"); code != http.StatusOK || body != `"answered"` || app.hub.Count() != 0 {
		t.Fatalf("/plain = %d %s, hub %d: the observer outlived its answer", code, body, app.hub.Count())
	}
	server := httptest.NewServer(http.HandlerFunc(tn.Serve))
	t.Cleanup(server.Close)
	_, reader, _ := openStream(t, server, "/early")
	go io.Copy(io.Discard, reader)
	streamWaitFor(t, "the stream", func() bool { return tn.OpenStreams() == 1 })
	if app.hub.Count() != 0 {
		t.Fatalf("after close() the hub had %d", app.hub.Count())
	}
	_, body := storageGet(t, tn, "/routine")
	want := `{"code":"UNSUPPORTED","message":"memory(\"z\").observe chỉ dùng trong một route ở engine hôm nay (route mở luồng bằng context.sse); trong job, lịch, routine chưa có (KITWORK_STORAGE.md mục 11)"}`
	if body != want {
		t.Fatalf("/routine = %s\nwant %s", body, want)
	}
}

// stalledReader opens path on server as a browser that never reads: a raw connection with a small receive
// buffer. It is closed when the test ends.
func stalledReader(t *testing.T, server *httptest.Server, path string) net.Conn {
	t.Helper()
	conn, err := net.Dial("tcp", server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetReadBuffer(4096)
	}
	t.Cleanup(func() { conn.Close() })
	if _, err := fmt.Fprintf(conn, "GET %s HTTP/1.1\r\nHost: %s\r\n\r\n", path, server.Listener.Addr()); err != nil {
		t.Fatal(err)
	}
	return conn
}

// floodStream sends what no reader keeps up with to the key subscribed at /events: 64 messages of 512 KiB.
func floodStream(t *testing.T, tn *Tenant) {
	t.Helper()
	app, err := tn.storage()
	if err != nil {
		t.Fatal(err)
	}
	key := storage.Key{Place: storage.Memory, Name: "rooms", Segments: []string{"lobby"}}.Encoded()
	streamWaitFor(t, "the stream", func() bool { return tn.OpenStreams() == 1 && app.hub.Observers(key) == 1 })
	data, err := storage.Encode(value.New(strings.Repeat("x", 512<<10)), storageValueLimit)
	if err != nil {
		t.Fatal(err)
	}
	for range 64 {
		app.hub.Send(key, &storage.Message{Data: data})
	}
}

// writingNow reports whether a stream of tn has a write on its connection.
func writingNow(tn *Tenant) bool {
	tn.streamsMu.Lock()
	defer tn.streamsMu.Unlock()
	for stream := range tn.streams {
		stream.mu.Lock()
		writing := stream.writing
		stream.mu.Unlock()
		if writing {
			return true
		}
	}
	return false
}

// A browser that stops reading never holds its stream for good (mục 8): a write it does not read within
// streamWriteTimeout closes the stream and frees its place, and a generation closing (hot reload, server
// stop) does not wait for such a write: close() ends it at once.
//
// CONTROL: with close() not giving a write in progress a deadline in the past, Close waited for the write
// held by the reader: "Close did not return within 3s while a write was held"; with no deadline on each
// write, the stream of the reader that stopped stayed open: "the stream of a reader that stopped closed
// on its own: not within 2s".
func TestStreamReaderThatDoesNotReadIsCut(t *testing.T) {
	saved := streamWriteTimeout
	t.Cleanup(func() { streamWriteTimeout = saved })
	route := map[string]string{"events/router.work": `router.get((context) => context.sse.subscribe(memory("rooms").segment("lobby")));`}

	streamWriteTimeout = time.Minute
	tn, server := streamServer(t, route)
	stalledReader(t, server, "/events")
	floodStream(t, tn)
	// the same write still on the connection after a while: the reader holds it
	streamWaitFor(t, "a write held by the reader", func() bool {
		if !writingNow(tn) {
			return false
		}
		time.Sleep(300 * time.Millisecond)
		return writingNow(tn)
	})
	closed := make(chan struct{})
	go func() { tn.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("Close did not return within 3s while a write was held")
	}
	if tn.OpenStreams() != 0 {
		t.Fatalf("%d streams open after Close", tn.OpenStreams())
	}

	streamWriteTimeout = 200 * time.Millisecond
	tn, server = streamServer(t, route)
	stalledReader(t, server, "/events")
	floodStream(t, tn)
	app, _ := tn.storage()
	streamWaitFor(t, "the stream of a reader that stopped closed on its own", func() bool {
		return tn.OpenStreams() == 0 && app.hub.Count() == 0
	})
}

// An observer's function that fails closes the stream (mục 4): the failure is logged as the handler's, at
// the stage observe, and nothing of it goes to the browser, which reads the end of the stream; the observer
// and the stream's place are released.
//
// CONTROL: with runObserver going on after a failure, the stream stayed open: "no frame within 2s".
func TestStreamFailingCallbackClosesTheStream(t *testing.T) {
	records := captureSlog(t)
	tn, server := streamServer(t, map[string]string{
		"events/router.work": `router.get((context) => {
  const stream = context.sse;
  memory("feed").observe((message) => {
    if (message.data === "boom") { fail("callback broke"); }
    stream.send(message);
  });
  return stream;
});
router.post((context) => { memory("feed").send({ data: context.request.json().say }); return context.json(true); });`,
	})
	_, reader, _ := openStream(t, server, "/events")
	app, _ := tn.storage()
	streamWaitFor(t, "the observer", func() bool { return app.hub.Count() == 1 })
	streamPost(t, server, "/events", `{"say":"ok"}`)
	if frame := readFrame(t, reader); frame != "data: \"ok\"\n\n" {
		t.Fatalf("before the failure = %q", frame)
	}
	streamPost(t, server, "/events", `{"say":"boom"}`)
	if rest := readFrame(t, reader); rest != "" {
		t.Fatalf("after the failure the browser read %q, want the end of the stream and nothing of the failure", rest)
	}
	found := false
	for _, record := range records.named("Kitwork runtime execution failed") {
		found = found || (record["stage"] == "observe" && record["message"] == "callback broke" && record["path"] == "/events")
	}
	if !found {
		t.Fatalf("no failure logged at the stage observe: %v", records.named("Kitwork runtime execution failed"))
	}
	streamWaitFor(t, "the observer and the stream released", func() bool { return app.hub.Count() == 0 && tn.OpenStreams() == 0 })
}

// The words of the stream that change it after it opened (mục 2, 3, 6): subscribe gives the stream, so two
// chain and one stream hears both keys; closed reads false while it is open and true after close(); close()
// ends the stream and its observers; reconnect() once open writes retry: at once.
//
// CONTROL: with subscribe giving null instead of the stream, the chained route failed: "GET /chain = 500";
// with the stream's close() doing nothing, "no frame within 2s" after close; with reconnect() writing
// nothing once open, "no frame within 2s" after retry.
func TestStreamCloseClosedSubscribeChainAndReconnect(t *testing.T) {
	tn, server := streamServer(t, map[string]string{
		"chain/router.work": `router.get((context) => context.sse.subscribe(memory("a")).subscribe(memory("b")));`,
		"control/router.work": `router.get((context) => {
  const stream = context.sse;
  memory("control").observe((message) => {
    if (message.data === "retry") {
      stream.reconnect("2s");
    } else if (message.data === "close") {
      stream.close();
      memory("after").set(stream.closed);
    } else {
      stream.send({ data: stream.closed });
    }
  });
  return stream;
});`,
		"say/router.work":   `router.post((context) => { const body = context.request.json(); memory(body.key).send({ data: body.data }); return context.json(true); });`,
		"after/router.work": `router.get((context) => context.json(memory("after").get()));`,
	})
	app, _ := tn.storage()
	chained, chain, _ := openStream(t, server, "/chain")
	if chained.StatusCode != http.StatusOK {
		t.Fatalf("GET /chain = %d", chained.StatusCode)
	}
	streamWaitFor(t, "both keys heard", func() bool { return app.hub.Count() == 2 })
	streamPost(t, server, "/say", `{"key":"a","data":1}`)
	streamPost(t, server, "/say", `{"key":"b","data":2}`)
	for _, want := range []string{"data: 1\n\n", "data: 2\n\n"} {
		if frame := readFrame(t, chain); frame != want {
			t.Fatalf("chained = %q, want %q", frame, want)
		}
	}

	_, control, _ := openStream(t, server, "/control")
	streamWaitFor(t, "the control observer", func() bool { return app.hub.Count() == 3 })
	streamPost(t, server, "/say", `{"key":"control","data":"open"}`)
	if frame := readFrame(t, control); frame != "data: false\n\n" {
		t.Fatalf("closed while open = %q, want false", frame)
	}
	streamPost(t, server, "/say", `{"key":"control","data":"retry"}`)
	if frame := readFrame(t, control); frame != "retry: 2000\n\n" {
		t.Fatalf("reconnect once open = %q, want retry: 2000", frame)
	}
	streamPost(t, server, "/say", `{"key":"control","data":"close"}`)
	if rest := readFrame(t, control); rest != "" {
		t.Fatalf("after close() = %q, want the end of the stream", rest)
	}
	streamWaitFor(t, "the closed stream's observer released", func() bool { return app.hub.Count() == 2 })
	if _, body := storageGet(t, tn, "/after"); body != "true" {
		t.Fatalf("closed after close() = %s, want true", body)
	}
}

// What a handler sends before it returns is kept and goes out as the stream opens (mục 1), a backlog sent on
// connecting (mục 6) included: 300 messages all arrive, in order. Past the bound of what may wait (bytes or
// messages) a send is INVALID_ARGUMENT, which safe catches, naming the bound and mục 6.
//
// CONTROL: with the byte bound taken out of sendMessage, the flood was never refused: "refused =
// {\"refused\":null}";
// with the bound of 256 messages the engine had, the backlog route failed: "GET /backlog = 500".
func TestStreamBacklogBeforeReturn(t *testing.T) {
	saved := streamBufferBytes
	t.Cleanup(func() { streamBufferBytes = saved })
	streamBufferBytes = 64 << 10
	tn, server := streamServer(t, map[string]string{
		"backlog/router.work": `router.get((context) => {
  const stream = context.sse;
  for (let i = 0; i < 300; i++) { stream.send({ id: "" + i, data: i }); }
  return stream;
});`,
		"flood/router.work": `router.get((context) => {
  const stream = context.sse;
  let refused = null;
  for (let i = 0; i < 1000; i++) {
    const r = safe(() => stream.send({ data: "x".repeat(100) }));
    if (!r.ok && refused === null) { refused = { at: i, code: r.error.code, message: r.error.message }; }
  }
  memory("flood").set({ refused: refused });
  return stream;
});`,
		"refused/router.work": `router.get((context) => context.json(memory("flood").get()));`,
	})
	response, reader, _ := openStream(t, server, "/backlog")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET /backlog = %d", response.StatusCode)
	}
	for i := range 300 {
		if frame, want := readFrame(t, reader), fmt.Sprintf("id: %d\ndata: %d\n\n", i, i); frame != want {
			t.Fatalf("frame %d = %q, want %q", i, frame, want)
		}
	}
	_, flood, _ := openStream(t, server, "/flood")
	go io.Copy(io.Discard, flood)
	streamWaitFor(t, "the flood route ran", func() bool { _, body := storageGet(t, tn, "/refused"); return body != "null" })
	_, body := storageGet(t, tn, "/refused")
	want := `{"refused":{"at":595,"code":"INVALID_ARGUMENT","message":"stream.send: tin gửi trước khi handler trả về luồng đã tới trần 10000 tin hay 65536 byte (đang giữ 595 tin, 65450 byte); gửi bù ít hơn mỗi lần kết nối, chẳng hạn chỉ những tin sau Last-Event-ID và có giới hạn (KITWORK_SSE.md mục 6)"}}`
	if body != want {
		t.Fatalf("refused = %s\nwant %s", body, want)
	}
}

// An idle stream holds no deadline: a write's deadline is cleared once it is written, so a stream quiet for
// longer than streamWriteTimeout stays open, under HTTP/2 too, where a write deadline is a timer that resets
// the stream when it fires.
//
// CONTROL: with writeFrames leaving the write's deadline set, the quiet HTTP/2 stream was reset before its
// first heartbeat: "heartbeat 0 after a quiet spell longer than a write's deadline = \"\"".
func TestStreamQuietLongerThanAWriteDeadlineStaysOpen(t *testing.T) {
	savedTimeout, savedHeartbeat := streamWriteTimeout, streamHeartbeat
	streamWriteTimeout, streamHeartbeat = 100*time.Millisecond, 400*time.Millisecond
	t.Cleanup(func() { streamWriteTimeout, streamHeartbeat = savedTimeout, savedHeartbeat })
	tn := storageSite(t, map[string]string{
		"events/router.work": `router.get((context) => context.sse.reconnect("1s").subscribe(memory("quiet")));`,
	})
	server := httptest.NewUnstartedServer(http.HandlerFunc(tn.Serve))
	server.EnableHTTP2 = true
	server.StartTLS()
	t.Cleanup(server.Close)
	response, reader, _ := openStream(t, server, "/events")
	if response.ProtoMajor != 2 {
		t.Fatalf("served over %s, want HTTP/2", response.Proto)
	}
	if frame := readFrame(t, reader); frame != "retry: 1000\n\n" {
		t.Fatalf("first frame = %q", frame)
	}
	for i := range 2 {
		if frame := readFrame(t, reader); frame != ": heartbeat\n\n" {
			t.Fatalf("heartbeat %d after a quiet spell longer than a write's deadline = %q", i, frame)
		}
	}
}
