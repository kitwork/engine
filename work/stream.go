package work

import (
	"bytes"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/storage"
	"github.com/kitwork/engine/value"
)

// ── context.sse (KITWORK_SSE.md) ───────────────────────────────────────────────────────────────────
//
//	router.get((context) => {
//	  const stream = context.sse.reconnect("3s");
//	  memory("events").segment(userId).observe((message) => stream.send(message));
//	  return stream;
//	});
//	router.get((context) => context.sse.subscribe(memory("rooms").segment("lobby")));
//
// Reading context.sse in a GET handler chooses the answer: the handler returns the stream, and the request
// stays open as the stream. Messages come in by send() of kitwork/storage, and go out by stream.send({ event,
// id, data }) (or stream.event(…).id(…).data(…)), data written as JSON. What is sent before the handler
// returns is kept and goes out as the stream opens.
//
// The request holds its VM while the stream is open: every function an observe() of the request was given
// runs on it, one message at a time on the request's own goroutine, each run with the app's MaxEnergy of its
// own. A function that fails closes the stream, logged as the handler's failure. The stream closes when the
// browser goes, stream.close(), a function fails, an observer's queue fills (a reader too slow), a write is
// not read within streamWriteTimeout (a reader that stopped), the generation is replaced (hot reload: the
// browser connects again to the new one) or the server stops, neither of which waits for a write held by
// such a reader; every observer of the request closes with it. Each app has a ceiling of open streams (503 past it), and a site
// with an open stream is not idle.

// The numbers of streams (KITWORK_SSE.md mục 10.4 leaves them open). Vars so tests can lower them.
var (
	// streamLimit is how many streams one app may hold open at once; one more is 503 (mục 8).
	streamLimit = 1_000
	// streamHeartbeat is how often an open stream writes a comment, so a proxy or the browser keeps it.
	streamHeartbeat = 15 * time.Second
	// streamBufferLimit and streamBufferBytes bound what a handler may send before it returns: a backlog sent
	// on connecting (mục 6) fits, a loop without end does not.
	streamBufferLimit = 10_000
	streamBufferBytes = 4 << 20
	// streamWriteTimeout is how long one write of an open stream may wait for the browser to read: a reader
	// that stops reading has its stream closed, never holding the request, its VM and its generation.
	streamWriteTimeout = 15 * time.Second
)

// streamSlots counts the open streams of one app.
type streamSlots struct {
	mu   sync.Mutex
	open int
}

func (s *streamSlots) enter() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.open >= streamLimit {
		return false
	}
	s.open++
	return true
}

func (s *streamSlots) leave() {
	s.mu.Lock()
	s.open--
	s.mu.Unlock()
}

// requestVMs is the request each leased VM runs, while it runs it (serveTree): observe() binds its observer
// to the request of the VM that calls it.
var requestVMs sync.Map // *runtime.VM → *Router

// requestRouterOf is the request vm runs, or nil (a job, a schedule, a routine, a file loading).
func requestRouterOf(vm *runtime.VM) *Router {
	if vm == nil {
		return nil
	}
	router, _ := requestVMs.Load(vm)
	found, _ := router.(*Router)
	return found
}

// requestEvents is what one request observes: its observers, in the order they began, and its stream once
// context.sse is read. It ends with the request.
type requestEvents struct {
	router *Router
	// wake is told, never blocking, that an observer has a message or was closed
	wake chan struct{}

	mu        sync.Mutex
	observers []*eventObserver
	stream    *eventStream
	closed    bool
}

// eventsOf is the request's events, made on first use and closed with the request.
func (r *Router) eventsOf() *requestEvents {
	if r.events == nil {
		events := &requestEvents{router: r, wake: make(chan struct{}, 1)}
		if r.requestScope != nil && !r.requestScope.AddCleanup(events.close) {
			events.closed = true
		}
		r.events = events
	}
	return r.events
}

func (e *requestEvents) signal() {
	select {
	case e.wake <- struct{}{}:
	default:
	}
}

// observe begins an observer of key on hub for the request: fn runs for each message, or, nil, the message
// goes to the stream as it is (subscribe).
func (e *requestEvents) observe(hub *storage.Hub, key string, fn *value.Lambda) (*eventObserver, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil, fmt.Errorf("request đã xong")
	}
	observer, err := hub.Observe(key, e.signal)
	if err != nil {
		return nil, err
	}
	watching := &eventObserver{observer: observer, callback: fn}
	e.observers = append(e.observers, watching)
	return watching, nil
}

// pending are the observers that are open, and the messages each has queued; overflowed is true when one
// was closed because its queue was full.
func (e *requestEvents) pending() (taken []observedMessages, overflowed bool) {
	e.mu.Lock()
	observers := append([]*eventObserver(nil), e.observers...)
	e.mu.Unlock()
	for _, watching := range observers {
		messages, full := watching.observer.Take()
		overflowed = overflowed || full
		if len(messages) > 0 {
			taken = append(taken, observedMessages{observer: watching, messages: messages})
		}
	}
	return taken, overflowed
}

type observedMessages struct {
	observer *eventObserver
	messages []*storage.Message
}

// close ends every observer of the request, and its stream.
func (e *requestEvents) close() {
	e.mu.Lock()
	e.closed = true
	observers := e.observers
	e.observers = nil
	stream := e.stream
	e.mu.Unlock()
	for _, watching := range observers {
		watching.observer.Close()
	}
	if stream != nil {
		stream.close()
	}
}

// eventObserver is what observe() gives: close() ends it early (mục 7); otherwise it ends with the request.
type eventObserver struct {
	observer *storage.Observer
	callback *value.Lambda
}

func (o *eventObserver) OnGet(key string) value.Value {
	if key == "close" {
		return value.NewFunc(func(...value.Value) value.Value { return o.OnInvoke("close") })
	}
	return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("observer.%s không có: observer có close() để dừng sớm; không gọi thì nó đóng khi request kết thúc (KITWORK_STORAGE.md mục 7)", key))
}

func (o *eventObserver) OnCompare(_ string, _ value.Value) value.Value { return value.NewNil() }

func (o *eventObserver) OnInvoke(method string, args ...value.Value) value.Value {
	if method != "close" {
		return o.OnGet(method)
	}
	if len(args) != 0 {
		return value.InvalidFailure(value.InvalidArgumentCode, "observer.close() không nhận đối số")
	}
	o.observer.Close()
	return value.Value{K: value.Nil}
}

// observe(fn) is the storage handle's observe: an observer of its key for the request the caller runs.
func (h *storageHandle) observe(caller *runtime.VM, args []value.Value) value.Value {
	var fn *value.Lambda
	if len(args) == 1 {
		fn, _ = args[0].V.(*value.Lambda)
	}
	if fn == nil || args[0].K != value.Func {
		return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("%s.observe nhận một hàm: observe((message) => stream.send(message))", h.spelling()))
	}
	router := requestRouterOf(caller)
	if router == nil {
		return value.InvalidFailure(value.UnsupportedCode, fmt.Sprintf("%s.observe chỉ dùng trong một route ở engine hôm nay (route mở luồng bằng context.sse); trong job, lịch, routine chưa có (KITWORK_STORAGE.md mục 11)", h.spelling()))
	}
	app, err := h.tenant.storage()
	if err != nil {
		return value.InvalidFailure(value.UnavailableCode, fmt.Sprintf("%s.observe: %v", h.spelling(), err))
	}
	watching, err := router.eventsOf().observe(app.hub, h.key.Encoded(), fn)
	if err != nil {
		return observeRefused(h.spelling(), err)
	}
	return value.Value{K: value.Proxy, V: watching}
}

// observeRefused is the failure of an observe() the app does not begin.
func observeRefused(where string, err error) value.Value {
	if err == storage.ErrObserverLimit {
		return value.InvalidFailure(value.TooLargeCode, fmt.Sprintf("%s.observe: app đã có %d observer, trần của một app (KITWORK_STORAGE.md mục 7)", where, storageObserverLimit))
	}
	return value.InvalidFailure(value.UnavailableCode, fmt.Sprintf("%s.observe: %v", where, err))
}

// ── the stream ─────────────────────────────────────────────────────────────────────────────────────

// eventStream is context.sse: the answer of its request, written on the request's goroutine once the
// handler returned. Two locks: writeMu orders the writes on the connection, so a send from anywhere never
// meets another; mu guards the state and is never held across the network, so close() never waits for a
// browser that does not read. Each write has a deadline, and close() ends a write in progress at once.
type eventStream struct {
	events *requestEvents

	mu        sync.Mutex
	writer    http.ResponseWriter
	control   *http.ResponseController
	opened    bool
	closed    bool
	writing   bool // a write is on the connection now (writeMu held)
	buffer    [][]byte
	buffered  int // the bytes of buffer
	reconnect time.Duration

	writeMu sync.Mutex
	done    chan struct{}
}

// streamWords are the words of a stream.
var streamWords = map[string]bool{
	"send": true, "event": true, "id": true, "data": true, "reconnect": true, "subscribe": true, "close": true,
}

// openStream is context.sse: the stream this request answers with, the same one however often it is read.
func (r *Router) openStream() (*eventStream, value.Value) {
	if stream := r.openedStream(); stream != nil {
		return stream, value.Value{K: value.Nil}
	}
	refuse := func(message string) (*eventStream, value.Value) {
		return nil, value.InvalidFailure(value.InvalidArgumentCode, message)
	}
	if r.request == nil || (r.request.Method != http.MethodGet && r.request.Method != http.MethodHead) {
		method := ""
		if r.request != nil {
			method = strings.ToLower(r.request.Method)
		}
		return refuse(fmt.Sprintf("context.sse chỉ dùng trong router.get: EventSource của trình duyệt chỉ gửi GET, route này trả lời %s; tin đi vào bằng router.%s gửi send({ event, id, data }) (KITWORK_SSE.md mục 1)", strings.ToUpper(method), method))
	}
	if !r.handling {
		return refuse("context.sse chỉ dùng trong handler của router.get, nơi trả lời bằng luồng: router.get((context) => { const stream = context.sse; …; return stream; }) (KITWORK_SSE.md mục 1)")
	}
	if r.method != nil && (r.method.cacheExpiry != nil || r.method.persistExpiry != nil) {
		return refuse("route mở luồng SSE không cache: bỏ .cache() hay .persist() trên route này; mỗi kết nối là một luồng của riêng nó (KITWORK_SSE.md mục 1)")
	}
	response := r.response
	switch {
	case response.IsSend():
		return refuse(fmt.Sprintf("context.sse sau câu trả lời .%s(): route mở luồng trả lời bằng luồng, không gì khác (KITWORK_SSE.md mục 1)", response.Kind()))
	case response.ordered() && response.order.pageText != "":
		return refuse(fmt.Sprintf("`.%s()` là chữ của trang, không có nghĩa với luồng SSE: bỏ `.%s()` trước context.sse; chỉnh sửa của luồng là header, headers, cookie (KITWORK_SSE.md mục 1)", response.order.pageText, response.order.pageText))
	case response.ordered() && response.order.typed:
		return refuse("`.type()` không có nghĩa với luồng SSE: luồng luôn là text/event-stream, bỏ `.type()` trước context.sse (KITWORK_SSE.md mục 1)")
	}
	if err := response.answering("sse"); err != nil {
		return refuse(err.Error())
	}
	if response.code != 0 && response.code != http.StatusOK {
		return refuse(fmt.Sprintf("luồng SSE luôn trả 200: bỏ .status(%d) trước context.sse (KITWORK_SSE.md mục 1)", response.code))
	}
	events := r.eventsOf()
	stream := &eventStream{events: events, done: make(chan struct{})}
	events.mu.Lock()
	events.stream = stream
	closed := events.closed
	events.mu.Unlock()
	if closed {
		stream.close()
	}
	response.Return(value.Value{K: value.Proxy, V: stream}, "sse", http.StatusOK)
	return stream, value.Value{K: value.Nil}
}

// Sse is context.sse (KITWORK_SSE.md mục 1), read without a call: the stream the handler returns.
func (c *Context) Sse() value.Value {
	stream, failure := c.router().openStream()
	if failure.IsError {
		return failure
	}
	return value.Value{K: value.Proxy, V: stream}
}

// is reports whether v is this stream: what a handler that read context.sse returns.
func (s *eventStream) is(v value.Value) bool {
	stream, ok := v.V.(*eventStream)
	return ok && v.K == value.Proxy && stream == s
}

func (s *eventStream) OnGet(key string) value.Value {
	switch {
	case key == "closed":
		return value.New(s.isClosed())
	case streamWords[key]:
		return value.NewCallerFunction(func(caller any, args []value.Value) value.Value {
			vm, _ := caller.(*runtime.VM)
			return s.call(vm, key, args)
		})
	}
	return s.unknown(key)
}

func (s *eventStream) OnCompare(_ string, _ value.Value) value.Value { return value.NewNil() }

func (s *eventStream) OnInvoke(method string, args ...value.Value) value.Value {
	return s.call(nil, method, args)
}

// InvokeFrom is stream.<method>(…) written in a program: caller is the VM that runs it.
func (s *eventStream) InvokeFrom(caller *runtime.VM, method string, args []value.Value) (value.Value, bool) {
	return s.call(caller, method, args), true
}

func (s *eventStream) unknown(word string) value.Value {
	switch word {
	case "connect":
		return value.InvalidFailure(value.InvalidArgumentCode, `sse.connect đã bỏ: context.sse đã là luồng; nghe một khoá bằng context.sse.subscribe(memory("rooms").segment(1)) (KITWORK_SSE.md mục 11)`)
	case "publish":
		return value.InvalidFailure(value.InvalidArgumentCode, `sse.publish đã bỏ: memory("rooms").segment(1).send({ event, id, data }) gửi tới mọi luồng đang nghe khoá đó (KITWORK_SSE.md mục 11)`)
	}
	return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("stream.%s không có: luồng có send, event, id, data, reconnect, subscribe, close và closed (KITWORK_SSE.md mục 2)", word))
}

func (s *eventStream) call(caller *runtime.VM, method string, args []value.Value) value.Value {
	switch method {
	case "send":
		message, failure := storageEnvelope("stream.send", args)
		if failure.IsError {
			return failure
		}
		return s.sendMessage(message)
	case "event", "id":
		return (&streamDraft{stream: s}).call(method, args)
	case "data":
		return (&streamDraft{stream: s}).call(method, args)
	case "reconnect":
		return s.withReconnect(args)
	case "subscribe":
		return s.subscribe(args)
	case "close":
		if len(args) != 0 {
			return value.InvalidFailure(value.InvalidArgumentCode, "stream.close() không nhận đối số: luồng đóng ngay; observer của request đóng theo (KITWORK_SSE.md mục 4)")
		}
		s.close()
		return value.Value{K: value.Nil}
	}
	return s.unknown(method)
}

// withReconnect is reconnect("3s"): the browser waits that long before it connects again, written once as
// retry: when the stream opens (mục 6). It gives the stream.
func (s *eventStream) withReconnect(args []value.Value) value.Value {
	usage := `reconnect(hạn) nhận một khoảng viết bằng chuỗi: reconnect("3s"), "500ms"`
	if len(args) != 1 || args[0].K != value.String {
		return value.InvalidFailure(value.InvalidArgumentCode, usage)
	}
	span, err := ParseDuration(strings.TrimSpace(args[0].Text()))
	if err != nil || span < time.Millisecond || span > 24*time.Hour {
		return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("reconnect(%q): một khoảng từ 1ms tới 24h; %s", args[0].Text(), usage))
	}
	s.mu.Lock()
	s.reconnect = span
	opened, closed := s.opened, s.closed
	s.mu.Unlock()
	if opened && !closed {
		s.write(retryFrame(span))
	}
	return value.Value{K: value.Proxy, V: s}
}

// retryFrame is the retry: field of reconnect(span).
func retryFrame(span time.Duration) []byte {
	return []byte("retry: " + strconv.FormatInt(span.Milliseconds(), 10) + "\n\n")
}

// subscribe(source) observes source and sends on each message as it is, with no function to run: the short
// form of observe((message) => stream.send(message)). It gives the stream, so subscribes chain (mục 3).
func (s *eventStream) subscribe(args []value.Value) value.Value {
	var source *storageHandle
	if len(args) == 1 && args[0].K == value.Proxy {
		source, _ = args[0].V.(*storageHandle)
	}
	if source == nil {
		return value.InvalidFailure(value.InvalidArgumentCode, `stream.subscribe nhận một handle của kitwork/storage: context.sse.subscribe(memory("rooms").segment(1)) (KITWORK_SSE.md mục 3)`)
	}
	app, err := source.tenant.storage()
	if err != nil {
		return value.InvalidFailure(value.UnavailableCode, fmt.Sprintf("stream.subscribe: %v", err))
	}
	if _, err := s.events.observe(app.hub, source.key.Encoded(), nil); err != nil {
		return observeRefused(source.spelling(), err)
	}
	return value.Value{K: value.Proxy, V: s}
}

// sendMessage writes one message to the stream, or keeps it until the stream opens. Once the stream has
// closed it does nothing: a closed connection is the network's, not the program's (mục 2).
func (s *eventStream) sendMessage(message *storage.Message) value.Value {
	frame, err := sseFrame(message)
	if err != nil {
		return valueFailure("stream.send (data)", err)
	}
	s.mu.Lock()
	if !s.opened && !s.closed {
		if len(s.buffer) >= streamBufferLimit || s.buffered+len(frame) > streamBufferBytes {
			held, size := len(s.buffer), s.buffered
			s.mu.Unlock()
			return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("stream.send: tin gửi trước khi handler trả về luồng đã tới trần %d tin hay %d byte (đang giữ %d tin, %d byte); gửi bù ít hơn mỗi lần kết nối, chẳng hạn chỉ những tin sau Last-Event-ID và có giới hạn (KITWORK_SSE.md mục 6)",
				streamBufferLimit, streamBufferBytes, held, size))
		}
		s.buffer = append(s.buffer, frame)
		s.buffered += len(frame)
		s.mu.Unlock()
		return value.Value{K: value.Nil}
	}
	s.mu.Unlock()
	s.write(frame)
	return value.Value{K: value.Nil}
}

// sseFrame is a message as the stream writes it: event and id when it has them, data always as JSON on one
// line (mục 2: the browser always reads JSON.parse(event.data)).
func sseFrame(message *storage.Message) ([]byte, error) {
	data, err := message.JSON()
	if err != nil {
		return nil, err
	}
	var frame bytes.Buffer
	if message.HasEvent && message.Event != "" {
		frame.WriteString("event: " + message.Event + "\n")
	}
	if message.HasID {
		frame.WriteString("id: " + message.ID + "\n")
	}
	frame.WriteString("data: ")
	frame.Write(data)
	frame.WriteString("\n\n")
	return frame.Bytes(), nil
}

// write writes bytes on the open stream and flushes them; a write the connection refuses, or one the browser
// does not read within streamWriteTimeout, closes the stream. It reports whether the stream is still open.
func (s *eventStream) write(frame []byte) bool {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.writeFrames(frame)
}

// writeFrames writes frames then flushes, with writeMu held: under a deadline set for this write and cleared
// after it, and marked as writing, so a close() meanwhile ends it at once.
func (s *eventStream) writeFrames(frames ...[]byte) bool {
	s.mu.Lock()
	if s.closed || !s.opened {
		s.mu.Unlock()
		return !s.closed
	}
	s.writing = true
	_ = s.control.SetWriteDeadline(time.Now().Add(streamWriteTimeout))
	s.mu.Unlock()
	failed := false
	for _, frame := range frames {
		if _, err := s.writer.Write(frame); err != nil {
			failed = true
			break
		}
	}
	if !failed && s.control.Flush() != nil {
		failed = true
	}
	s.mu.Lock()
	s.writing = false
	if !failed && !s.closed {
		// the deadline is the write's alone: an idle stream has none (under HTTP/2 a deadline is a timer that
		// resets the stream when it fires), and none is left for the connection's next request
		_ = s.control.SetWriteDeadline(time.Time{})
	}
	s.mu.Unlock()
	if failed {
		s.close()
		return false
	}
	return !s.isClosed()
}

// open makes the stream write to w: the retry: field when reconnect was set, then what was sent before the
// handler returned. It holds writeMu throughout, so a send meanwhile goes out after them.
func (s *eventStream) open(w http.ResponseWriter) bool {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return false
	}
	s.writer, s.control, s.opened = w, http.NewResponseController(w), true
	frames := s.buffer
	if s.reconnect > 0 {
		frames = append([][]byte{retryFrame(s.reconnect)}, frames...)
	}
	s.buffer, s.buffered = nil, 0
	s.mu.Unlock()
	return s.writeFrames(frames...)
}

// close closes the stream, at once: a write waiting on a browser that does not read is given a deadline in
// the past, so it returns now.
func (s *eventStream) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	s.buffer, s.buffered = nil, 0
	if s.writing {
		_ = s.control.SetWriteDeadline(time.Now())
	}
	close(s.done)
}

// finish closes the stream as its request ends, and waits for a write in progress to return (close gave it a
// deadline in the past): nothing touches the connection once the handler has returned.
func (s *eventStream) finish() {
	s.close()
	// taken only to wait for the write that holds it
	s.writeMu.Lock()
	s.writeMu.Unlock()
}

func (s *eventStream) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// streamDraft is stream.event(…).id(…) before its .data(…): the edits of one message, which go with it and
// never stick to the next (mục 2).
type streamDraft struct {
	stream   *eventStream
	event    string
	id       string
	hasEvent bool
	hasID    bool
}

func (d *streamDraft) OnGet(key string) value.Value {
	switch key {
	case "event", "id", "data":
		return value.NewFunc(func(args ...value.Value) value.Value { return d.call(key, args) })
	}
	return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("stream.event(…).%s không có: sau event, id là .data(giá_trị), gửi ngay (KITWORK_SSE.md mục 2)", key))
}

func (d *streamDraft) OnCompare(_ string, _ value.Value) value.Value { return value.NewNil() }

func (d *streamDraft) OnInvoke(method string, args ...value.Value) value.Value {
	return d.call(method, args)
}

func (d *streamDraft) call(method string, args []value.Value) value.Value {
	switch method {
	case "event", "id":
		if len(args) != 1 || args[0].K != value.String || strings.ContainsAny(args[0].Text(), "\r\n") {
			return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf(`stream.%s(…) nhận một chuỗi không chứa xuống dòng: stream.event("chat").id("msg-123").data({ text })`, method))
		}
		if (method == "event" && d.hasEvent) || (method == "id" && d.hasID) {
			return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf(".%s nối hai lần: một tin một %s", method, method))
		}
		next := *d
		if method == "event" {
			next.event, next.hasEvent = args[0].Text(), true
		} else {
			next.id, next.hasID = args[0].Text(), true
		}
		return value.Value{K: value.Proxy, V: &next}
	case "data":
		if len(args) != 1 {
			return value.InvalidFailure(value.InvalidArgumentCode, "stream.data(giá_trị) nhận một giá trị, gửi ngay: stream.event(\"chat\").data({ text })")
		}
		encoded, err := storage.Encode(args[0], storageValueLimit)
		if err != nil {
			return valueFailure("stream.data", err)
		}
		return d.stream.sendMessage(&storage.Message{Event: d.event, HasEvent: d.hasEvent, ID: d.id, HasID: d.hasID, Data: encoded})
	}
	return d.OnGet(method)
}

// ── the loop ───────────────────────────────────────────────────────────────────────────────────────

// serveStream answers the request with its stream, until it closes: the handler returned it, and nothing
// failed. It runs on the request's goroutine, holding the request's VM.
func (t *Tenant) serveStream(w http.ResponseWriter, router *Router) {
	events := router.events
	stream := events.stream
	defer stream.finish()
	if router.request.Method == http.MethodHead {
		streamHeaders(w, router)
		w.WriteHeader(http.StatusOK)
		return
	}
	app, err := t.storage()
	if err != nil {
		http.Error(w, "Service Unavailable", http.StatusServiceUnavailable)
		return
	}
	if !app.streams.enter() {
		w.Header().Set("Retry-After", "5")
		http.Error(w, "Service Unavailable: too many open streams", http.StatusServiceUnavailable)
		return
	}
	defer app.streams.leave()
	if !t.addStream(stream) {
		http.Error(w, "Service Unavailable", http.StatusServiceUnavailable)
		return
	}
	defer t.removeStream(stream)
	if _, ok := w.(http.Flusher); !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	vm := requestVM(router.requestScope)
	streamHeaders(w, router)
	// an open stream has no end in sight: no write deadline of the server's cuts it; each write of the stream
	// sets its own (streamWriteTimeout)
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
	w.WriteHeader(http.StatusOK)
	if !stream.open(w) {
		return
	}

	heartbeat := time.NewTicker(streamHeartbeat)
	defer heartbeat.Stop()
	gone := router.requestScope.Context().Done()
	for {
		select {
		case <-gone:
			return
		case <-stream.done:
			return
		case <-events.wake:
			if !t.deliverStream(router, vm) {
				return
			}
		case <-heartbeat.C:
			if !stream.write([]byte(": heartbeat\n\n")) {
				return
			}
		}
	}
}

// streamHeaders are the headers of a stream: its own, then what the handler set before context.sse.
func streamHeaders(w http.ResponseWriter, router *Router) {
	router.response.writeHeaders(w)
	router.response.writeCookies(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
}

// deliverStream hands each observer of the request what it has queued, oldest first: a function runs on the
// request's VM, a subscribe writes the message as it is. It reports whether the stream stays open.
func (t *Tenant) deliverStream(router *Router, vm *runtime.VM) bool {
	events, stream := router.events, router.events.stream
	taken, overflowed := events.pending()
	for _, batch := range taken {
		for _, message := range batch.messages {
			if stream.isClosed() {
				return false
			}
			if batch.observer.callback == nil {
				frame, err := sseFrame(message)
				if err == nil && !stream.write(frame) {
					return false
				}
				continue
			}
			if !t.runObserver(router, vm, batch.observer.callback, message) {
				return false
			}
		}
	}
	if overflowed {
		slog.WarnContext(router.request.Context(), "Kitwork stream closed: an observer's queue was full",
			"request_id", router.requestID, "app", t.AppID(), "site", t.Domain(), "path", router.request.URL.Path,
			"queue", storageQueueLimit)
		return false
	}
	return !stream.isClosed()
}

// runObserver runs an observer's function for one message on the request's VM, with a budget of its own; a
// failure is logged as the handler's and closes the stream (mục 4: no detail goes to the browser).
func (t *Tenant) runObserver(router *Router, vm *runtime.VM, fn *value.Lambda, message *storage.Message) bool {
	program, _ := runtime.ProgramFromRef(fn.Program)
	if vm == nil || program == nil {
		return false
	}
	vm.FastResetPrepared(program)
	vm.MaxEnergy = t.MaxEnergy
	started := time.Now()
	var result value.Value
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				result = runtime.NativePanic("observe", recovered)
			}
		}()
		result = vm.ExecuteLambda(fn, []value.Value{messageValue(message)})
	}()
	t.recordVMExecution(program, vm, result, time.Since(started))
	if result.K == value.Invalid {
		t.recordRuntimeFailure(router, nil, "observe", result)
		return false
	}
	return true
}

// ── the streams of a generation ────────────────────────────────────────────────────────────────────

// addStream counts an open stream of t, unless t is closing.
func (t *Tenant) addStream(stream *eventStream) bool {
	t.streamsMu.Lock()
	defer t.streamsMu.Unlock()
	if t.streamsClosing {
		return false
	}
	if t.streams == nil {
		t.streams = map[*eventStream]struct{}{}
	}
	t.streams[stream] = struct{}{}
	return true
}

func (t *Tenant) removeStream(stream *eventStream) {
	t.streamsMu.Lock()
	delete(t.streams, stream)
	t.streamsMu.Unlock()
}

// closeStreams closes every open stream of t and refuses new ones: the generation is replaced or the site
// stops (Tenant.Close, before it waits for its requests). A browser connects again, to the new generation.
func (t *Tenant) closeStreams() {
	t.streamsMu.Lock()
	t.streamsClosing = true
	streams := make([]*eventStream, 0, len(t.streams))
	for stream := range t.streams {
		streams = append(streams, stream)
	}
	t.streamsMu.Unlock()
	for _, stream := range streams {
		stream.close()
	}
}

// OpenStreams is how many streams of t are open: a site with one is not idle, and the engine does not evict
// it from memory (core.Engine's cleanup), which would cut them.
func (t *Tenant) OpenStreams() int {
	if t == nil {
		return 0
	}
	t.streamsMu.Lock()
	defer t.streamsMu.Unlock()
	return len(t.streams)
}

// openedStream is the stream the request's handler opened with context.sse, or nil.
func (r *Router) openedStream() *eventStream {
	if r.events == nil {
		return nil
	}
	r.events.mu.Lock()
	defer r.events.mu.Unlock()
	return r.events.stream
}

// Sse is context.sse read off the response an edit gives (context.header("X-Room", "1").sse): the same
// stream, with the edits before it.
func (r *Response) Sse() value.Value {
	if r == nil || r.ctx == nil {
		return value.InvalidFailure(value.InvalidArgumentCode, "context.sse chỉ dùng trong handler của router.get (KITWORK_SSE.md mục 1)")
	}
	return r.ctx.Sse()
}
