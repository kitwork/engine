package work

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The tabs told when the data they show changes (28/10/2026, view_changes.go): two pages read one key of
// storage; a job of one writes it, and a tab of the other, holding the tickets its page gave it, is told. A
// ticket holds for the cookies it was made with and only as the server made it; a job that changes only vars
// tells nothing; a native shell's channel is 204; a channel's tickets come only from the site's own pages; a
// page its guard refuses gives none.
func sharedKeySite(t *testing.T) *Tenant {
	t.Helper()
	tn := NewTenant(writeSite(t, map[string]string{
		"router.work":   `import * as router from "kitwork/router";`,
		"view.ui":       "<view><content /></view>",
		"a/content.ui":  `<content><text>{{ count }}</text><input value="{{ note }}" /></content>`,
		"b/content.ui":  `<content><text>{{ count }}</text></content>`,
		"a/router.work": sharedKeyPage("note = \"x\";"),
		"b/router.work": sharedKeyPage("list.set((list.get() ?? []).concat([1]));"),
	}), "localhost")
	if err := tn.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	return tn
}

func sharedKeyPage(job string) string {
	return `import * as router from "kitwork/router";
import { datastore } from "kitwork/storage";

const list = datastore("shared").segment("list");

router.view((context) => {
  var note = "";
  const count = (list.get() ?? []).length;
  const act = () => { ` + job + ` };
  return context.bind({ note, count, act });
});
`
}

// pageTickets reads the tickets of a page's data block.
func pageTickets(t *testing.T, body string) []string {
	t.Helper()
	m := regexp.MustCompile(`data-kitwork-page>(.*?)</script>`).FindStringSubmatch(body)
	var block struct {
		Bridge struct {
			Tickets []string `json:"tickets"`
		} `json:"bridge"`
	}
	if m == nil || json.Unmarshal([]byte(m[1]), &block) != nil {
		t.Fatalf("no data block:\n%.2000s", body)
	}
	return block.Bridge.Tickets
}

// openChanges opens a channel on a running server: its name, and each frame it is told after.
func openChanges(t *testing.T, server *httptest.Server) (string, <-chan string, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+changesPath, nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/event-stream") {
		cancel()
		t.Fatalf("a channel should be a stream: %d %s", res.StatusCode, res.Header.Get("Content-Type"))
	}
	frames := make(chan string, 16)
	name := make(chan string, 1)
	go func() {
		defer close(frames)
		lines := bufio.NewScanner(res.Body)
		event := ""
		for lines.Scan() {
			line := lines.Text()
			if e, ok := strings.CutPrefix(line, "event: "); ok {
				event = e
				continue
			}
			if data, ok := strings.CutPrefix(line, "data: "); ok {
				if event == "connection" {
					name <- data
				} else {
					frames <- event + ":" + data
				}
				event = ""
			}
		}
	}()
	select {
	case id := <-name:
		return id, frames, func() { cancel(); res.Body.Close() }
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("the channel said no name")
	}
	return "", nil, nil
}

func holdOn(t *testing.T, server *httptest.Server, id string, tickets []string, header http.Header) (map[string]int64, int) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"tickets": tickets})
	req, _ := http.NewRequest(http.MethodPost, server.URL+changesPath+"?connection="+id, strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	for name, values := range header {
		req.Header[name] = values
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	answer := map[string]int64{}
	json.NewDecoder(res.Body).Decode(&answer)
	return answer, res.StatusCode
}

func frameWithin(frames <-chan string, within time.Duration) (string, bool) {
	select {
	case frame, ok := <-frames:
		return frame, ok
	case <-time.After(within):
		return "", false
	}
}

func TestAWriteTellsTheTabsOfEveryPageThatReadTheKey(t *testing.T) {
	tn := sharedKeySite(t)
	server := httptest.NewServer(http.HandlerFunc(tn.Serve))
	defer server.Close()
	tickets := pageTickets(t, serveGet(tn, "/a", nil).Body.String())
	if len(tickets) != 2 {
		t.Fatalf("page a should give a ticket for itself and one for the key it read, gave %v", tickets)
	}
	id, frames, stop := openChanges(t, server)
	defer stop()
	if answer, code := holdOn(t, server, id, tickets, nil); code != http.StatusOK || answer["taken"] != 2 {
		t.Fatalf("the channel should take page a's two tickets: %d %v", code, answer)
	}

	// a job of page b writes the key page a read
	call, err := http.Post(server.URL+"/b?kitwork-call=act", "application/json", strings.NewReader(`{"args":[],"vars":{"note":""},"seq":1}`))
	if err != nil {
		t.Fatal(err)
	}
	var answer struct {
		Stamp int64 `json:"stamp"`
	}
	json.NewDecoder(call.Body).Decode(&answer)
	call.Body.Close()
	frame, _ := frameWithin(frames, 3*time.Second)
	stamp, err := strconv.ParseInt(strings.TrimPrefix(frame, ":"), 10, 64)
	if err != nil || stamp == 0 || stamp >= answer.Stamp {
		t.Fatalf("a write of page b should tell the tab of page a a stamp before b's answer read the data (%d), told %q", answer.Stamp, frame)
	}

	// a channel opened later, holding the same tickets, is told the last change at once
	later, _, stopLater := openChanges(t, server)
	defer stopLater()
	if held, _ := holdOn(t, server, later, tickets, nil); held["last"] != stamp {
		t.Fatalf("a channel that takes the tickets should be told the last change %d, got %v", stamp, held)
	}
}

func TestAJobThatChangesOnlyVarsTellsNoTab(t *testing.T) {
	tn := sharedKeySite(t)
	server := httptest.NewServer(http.HandlerFunc(tn.Serve))
	defer server.Close()
	id, frames, stop := openChanges(t, server)
	defer stop()
	holdOn(t, server, id, pageTickets(t, serveGet(tn, "/a", nil).Body.String()), nil)
	call, err := http.Post(server.URL+"/a?kitwork-call=act", "application/json", strings.NewReader(`{"args":[],"vars":{"note":""},"seq":1}`))
	if err != nil {
		t.Fatal(err)
	}
	call.Body.Close()
	if frame, ok := frameWithin(frames, 400*time.Millisecond); ok {
		t.Fatalf("a job that changed only the page's vars should tell no tab, told %s", frame)
	}
}

func TestATicketIsTheServersAndHoldsForItsCookies(t *testing.T) {
	tn := sharedKeySite(t)
	server := httptest.NewServer(http.HandlerFunc(tn.Serve))
	defer server.Close()
	tickets := pageTickets(t, serveGet(tn, "/a", map[string]string{"Cookie": "session=An"}).Body.String())
	id, _, stop := openChanges(t, server)
	defer stop()
	if answer, _ := holdOn(t, server, id, tickets, http.Header{"Cookie": {"session=Binh"}}); answer["taken"] != 0 {
		t.Errorf("tickets made for other cookies should not be taken, took %d", answer["taken"])
	}
	forged := append([]string{}, tickets...)
	forged[0] = "A" + forged[0][1:]
	if answer, _ := holdOn(t, server, id, forged[:1], http.Header{"Cookie": {"session=An"}}); answer["taken"] != 0 {
		t.Errorf("a ticket the server did not make should not be taken, took %d", answer["taken"])
	}
	if answer, _ := holdOn(t, server, id, tickets, http.Header{"Cookie": {"session=An"}}); answer["taken"] != 2 {
		t.Errorf("the tickets, with the cookies they were made for, should be taken, took %d", answer["taken"])
	}
	if _, code := holdOn(t, server, "nothing", tickets, http.Header{"Cookie": {"session=An"}}); code != http.StatusNotFound {
		t.Errorf("a channel that is not open should be 404, got %d", code)
	}
	if _, code := holdOn(t, server, id, tickets, http.Header{"Sec-Fetch-Site": {"cross-site"}}); code != http.StatusForbidden {
		t.Errorf("tickets from another site's page should be refused, got %d", code)
	}
}

func TestANativeShellHoldsNoChannel(t *testing.T) {
	tn := sharedKeySite(t)
	req := httptest.NewRequest(http.MethodGet, "http://localhost"+changesPath, nil)
	req.Header.Set("X-Kitwork-Native", "windows")
	rec := httptest.NewRecorder()
	tn.Serve(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("a native app's shell holds no stream, its channel should be answered 204: %d", rec.Code)
	}
}

func TestAPageItsGuardRefusesGivesNoTickets(t *testing.T) {
	tn := guardedViewSite(t)
	refused := serveGet(tn, "/desk", nil)
	if strings.Contains(refused.Body.String(), `"tickets"`) || refused.Header().Get("Location") != "/login" {
		t.Fatalf("a page its guard refuses should go to /login with no tickets: %d %v", refused.Code, refused.Header())
	}
	if tickets := pageTickets(t, serveGet(tn, "/desk", map[string]string{"Cookie": "session=in"}).Body.String()); len(tickets) == 0 {
		t.Fatalf("the page its guard lets through should give tickets")
	}
}

// An answer brings tickets only when the page does not hold tickets, made lately, for the keys its run read: a
// call that sends the marks of its page's tickets gets none; one that sends none, or others, gets them.
//
// CONTROL (by hand, 28/10/2026): with answerTickets putting the tickets in every answer, the first call below
// went red; with it putting none, the second.
func TestAnAnswerBringsTicketsOnlyWhenThePageHoldsOthers(t *testing.T) {
	tn := patchTodoSite(t)
	marks := ticketMarks(pageTickets(t, serveGet(tn, "/todo", nil).Body.String()))
	if answer, body := callAnswer(t, tn, "add", `{"args":[],"vars":{"title":"a"},"seq":1,"marks":"`+marks+`"}`); answer["tickets"] != nil {
		t.Errorf("a page that holds tickets for the keys should get none again: %s", body)
	}
	for _, sent := range []string{``, `,"marks":"other"`} {
		if answer, body := callAnswer(t, tn, "add", `{"args":[],"vars":{"title":"b"},"seq":2`+sent+`}`); answer["tickets"] == nil {
			t.Errorf("a page that holds no tickets for those keys should get them (%s): %s", sent, body)
		}
	}
}

// What a component reads gives its page a ticket too (28/10/2026): a component runs on a VM of its own, which
// is the request's as the page's is. A badge reads the signed-in name; another page writes it; a tab of the
// badge's page, holding its tickets, is told.
//
// CONTROL (by hand, 28/10/2026): with loadComponents not making its VM the request's, the page gave one ticket
// (its own) and the tab was told nothing.
func TestWhatAComponentReadsGivesItsPageATicket(t *testing.T) {
	logic := "import * as component from \"kitwork/component\";\nimport { datastore } from \"kitwork/storage\";\n" +
		"const me = datastore(\"users\").segment(\"me\");\n" +
		"component.define(\"user-badge\", (context) => {\n" +
		"  var open = false;\n  const name = me.get() ?? \"khách\";\n  const toggle = () => { open = !open; };\n" +
		"  return context.bind({ open, name, toggle });\n});\n"
	tn := NewTenant(writeSite(t, map[string]string{
		"router.work":     `import * as router from "kitwork/router";`,
		"view.ui":         "<view><content /></view>",
		"components.work": logic,
		"p/content.ui":    `<content><input value="{{ note }}" /><user-badge><text>{{ name }}</text></user-badge></content>`,
		"p/router.work":   "import * as router from \"kitwork/router\";\nrouter.view((context) => {\n  var note = \"\";\n  return context.bind({ note });\n});\n",
		"w/content.ui":    `<content><input value="{{ note }}" /></content>`,
		"w/router.work": "import * as router from \"kitwork/router\";\nimport { datastore } from \"kitwork/storage\";\n" +
			"const me = datastore(\"users\").segment(\"me\");\n" +
			"router.view((context) => {\n  var note = \"\";\n  const rename = (name) => { me.set(name); };\n" +
			"  return context.bind({ note, rename });\n});\n",
	}), "localhost")
	if err := tn.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(tn.Serve))
	defer server.Close()
	tickets := pageTickets(t, serveGet(tn, "/p", nil).Body.String())
	if len(tickets) != 2 {
		t.Fatalf("the page should give a ticket for itself and one for what its badge read, gave %d", len(tickets))
	}
	id, frames, stop := openChanges(t, server)
	defer stop()
	holdOn(t, server, id, tickets, nil)
	call, err := http.Post(server.URL+"/w?kitwork-call=rename", "application/json", strings.NewReader(`{"args":["An"],"vars":{"note":""},"seq":1}`))
	if err != nil {
		t.Fatal(err)
	}
	call.Body.Close()
	if frame, ok := frameWithin(frames, 3*time.Second); !ok || frame == "" {
		t.Fatalf("a write of what the badge read should tell the tab of its page, told %q", frame)
	}
}

// A tab's channel holds no VM (28/10/2026): it is answered before the route tree, which is what leases one,
// so a thousand tabs seen hold a thousand streams and no VM; the per-page listener before it held the VM of
// its request for as long as it was open.
//
// CONTROL (by hand, 28/10/2026): with serveChanges holding a VM of the pool while open, five channels held
// five VMs and this test went red.
func TestAChannelHoldsNoVM(t *testing.T) {
	tn := sharedKeySite(t)
	server := httptest.NewServer(http.HandlerFunc(tn.Serve))
	defer server.Close()
	serveGet(tn, "/a", nil) // the pool warm, the site's storage made
	before := enginePool.Active()
	for i := 0; i < 5; i++ {
		_, _, stop := openChanges(t, server)
		defer stop()
	}
	if held := enginePool.Active() - before; held != 0 {
		t.Fatalf("five open channels hold %d VMs, want none", held)
	}
}
