package work

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// A search page (29/10/2026): the field holds the var query, what it finds is read through it, and search
// changes nothing but query, so the compiler calls it read only (compiler/view_read_only.go); add writes the
// words.
func readOnlySite(t *testing.T) *Tenant {
	t.Helper()
	tn := NewTenant(writeSite(t, map[string]string{
		"router.work":      `import * as router from "kitwork/router";`,
		"view.ui":          "<view><content /></view>",
		"find/content.ui":  `<content><input value="{{ query }}" /><text>{{ found }}</text><text>{{ count }}</text></content>`,
		"find/router.work": readOnlyPage,
	}), "localhost")
	if err := tn.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	return tn
}

const readOnlyPage = `import * as router from "kitwork/router";
import { datastore } from "kitwork/storage";

const list = datastore("words").segment("list");

router.view((context) => {
  var query = "";
  const words = list.get() ?? [];
  const found = words.filter((word) => word.includes(query)).length;
  const count = words.length;
  const search = (text) => { query = text.trim(); };
  const add = (word) => { list.set(words.concat([word])); };
  return context.bind({ query, found, count, search, add });
});
`

// answered calls in the background and says whether it answered within a time.
func answered(call func() *httptest.ResponseRecorder) <-chan *httptest.ResponseRecorder {
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- call() }()
	return done
}

// A call of a read-only job, and a refresh, wait for no other call of the page: while a call that writes holds
// the page's lock, a search is answered, with what it finds; a call that writes waits for the lock.
//
// CONTROL (by hand, 29/10/2026): with router_serve.go taking the lock for every call, the search below was not
// answered while the lock was held (red); with it taking none, the add was.
func TestAReadOnlyJobWaitsForNoOtherCall(t *testing.T) {
	tn := readOnlySite(t)
	for _, word := range []string{"apple", "apricot", "banana"} {
		if r := postCall(tn, "/find", "add", `{"args":["`+word+`"],"vars":{"query":""},"seq":1}`); r.Code != http.StatusOK {
			t.Fatalf("add %s: %d %s", word, r.Code, r.Body.String())
		}
	}
	held, _ := tn.viewCalls.LoadOrStore("find", &sync.Mutex{}) // the page's lock, by its folder (viewPageKey)
	lock := held.(*sync.Mutex)
	lock.Lock() // a call that writes is out

	search := answered(func() *httptest.ResponseRecorder {
		return postCall(tn, "/find", "search", `{"args":[" ap "],"vars":{"query":""},"seq":2}`)
	})
	select {
	case r := <-search:
		var answer struct {
			Patch map[string]any `json:"patch"`
		}
		if json.Unmarshal(r.Body.Bytes(), &answer) != nil || answer.Patch["query"] != "ap" || answer.Patch["found"] != float64(2) {
			t.Fatalf("the search should answer what it finds: %d %s", r.Code, r.Body.String())
		}
	case <-time.After(3 * time.Second):
		lock.Unlock()
		t.Fatal("a read-only job should not wait for the call that holds the page's lock")
	}

	refresh := answered(func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "http://localhost/find?"+ViewRefreshParameter+"=1", strings.NewReader(`{"vars":{"query":"b"},"seq":3,"revs":{}}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		tn.Serve(rec, req)
		return rec
	})
	select {
	case r := <-refresh:
		if r.Code != http.StatusOK || !strings.Contains(r.Body.String(), `"found":1`) {
			t.Fatalf("the refresh should answer the page as its vars find it: %d %s", r.Code, r.Body.String())
		}
	case <-time.After(3 * time.Second):
		lock.Unlock()
		t.Fatal("a refresh should not wait for the call that holds the page's lock")
	}

	add := answered(func() *httptest.ResponseRecorder {
		return postCall(tn, "/find", "add", `{"args":["cherry"],"vars":{"query":""},"seq":4}`)
	})
	select {
	case r := <-add:
		lock.Unlock()
		t.Fatalf("a job that writes should wait for the page's lock, answered %d %s", r.Code, r.Body.String())
	case <-time.After(300 * time.Millisecond):
	}
	lock.Unlock()
	select {
	case r := <-add:
		if r.Code != http.StatusOK || !strings.Contains(r.Body.String(), `"count":4`) {
			t.Fatalf("the add should go once the lock is let go: %d %s", r.Code, r.Body.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the add should be answered once the lock is let go")
	}
}

// A read-only job tells no tab: what it changed on its page (the search's results) is its tab's, not data
// another tab shows. A job that writes still tells.
//
// CONTROL (by hand, 29/10/2026): with serveViewCall telling whenever the page's data differs, the search below
// told the tab (red).
func TestAReadOnlyJobTellsNoTab(t *testing.T) {
	tn := readOnlySite(t)
	postCall(tn, "/find", "add", `{"args":["apple"],"vars":{"query":""},"seq":1}`)
	server := httptest.NewServer(http.HandlerFunc(tn.Serve))
	defer server.Close()
	id, frames, stop := openChanges(t, server)
	defer stop()
	if answer, code := holdOn(t, server, id, pageTickets(t, serveGet(tn, "/find", nil).Body.String()), nil); code != http.StatusOK || answer["taken"] == 0 {
		t.Fatalf("the channel should take the page's tickets: %d %v", code, answer)
	}
	if r := postCall(tn, "/find", "search", `{"args":["zz"],"vars":{"query":""},"seq":2}`); !strings.Contains(r.Body.String(), `"found":0`) {
		t.Fatalf("the search should change what the page finds: %s", r.Body.String())
	}
	if frame, ok := frameWithin(frames, 400*time.Millisecond); ok {
		t.Fatalf("a read-only job should tell no tab, told %s", frame)
	}
	postCall(tn, "/find", "add", `{"args":["banana"],"vars":{"query":""},"seq":3}`)
	if _, ok := frameWithin(frames, 3*time.Second); !ok {
		t.Fatal("a job that writes should still tell the tab")
	}
}

// A component that searches (29/10/2026): a select with a field to type in. search changes nothing but the
// instance's var query; add writes the words. The page, compiled, read the component's logic and knows which is
// which before the lock is taken (ui.Page.ReadOnlyJob).
const wordPicker = `import * as component from "kitwork/component";
import { datastore } from "kitwork/storage";

const words = datastore("words").segment("list");

component.define("word-picker", (context) => {
  var query = "";
  const all = words.get() ?? [];
  const found = all.filter((word) => word.includes(query)).length;
  const search = (text) => { query = text.trim(); };
  const add = (word) => { words.set(all.concat([word])); };
  return context.bind({ query, found, search, add });
});
`

func pickerSite(t *testing.T) *Tenant {
	t.Helper()
	tn := NewTenant(writeSite(t, map[string]string{
		"router.work":      `import * as router from "kitwork/router";`,
		"view.ui":          "<view><content /></view>",
		"components.work":  wordPicker,
		"pick/content.ui":  `<content><text>{{ title }}</text><word-picker><input value="{{ query }}" /><text>{{ found }}</text></word-picker></content>`,
		"pick/router.work": "import * as router from \"kitwork/router\";\nrouter.view((context) => {\n  const title = \"Chọn\";\n  return context.bind({ title });\n});\n",
	}), "localhost")
	if err := tn.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	return tn
}

// A read-only job of a component waits for no other call of the page: while a call that writes holds the page's
// lock, the picker's search is answered with what it finds; its add waits for the lock.
//
// CONTROL (by hand, 29/10/2026): with router_serve.go taking the lock for every job of a component, and with
// the page not reading the read-only jobs off the component's logic (ui/component.go), the search was not
// answered while the lock was held (red); with Page.ReadOnlyJob saying yes to every job of the component, the
// add was.
func TestAReadOnlyJobOfAComponentWaitsForNoOtherCall(t *testing.T) {
	tn := pickerSite(t)
	if r := serveGet(tn, "/pick", nil); r.Code != http.StatusOK || !strings.Contains(r.Body.String(), `data-kitwork-at="c1"`) {
		t.Fatalf("/pick should draw the picker for the bridge: %d %.1500s", r.Code, r.Body.String())
	}
	for _, word := range []string{"apple", "apricot", "banana"} {
		if r := postCall(tn, "/pick", "word-picker.add", `{"args":["`+word+`"],"vars":{},"seq":1,"at":"c1","scope":{"query":""}}`); r.Code != http.StatusOK {
			t.Fatalf("add %s: %d %s", word, r.Code, r.Body.String())
		}
	}
	held, _ := tn.viewCalls.LoadOrStore("pick", &sync.Mutex{}) // the page's lock, by its folder (viewPageKey)
	lock := held.(*sync.Mutex)
	lock.Lock() // a call that writes is out

	search := answered(func() *httptest.ResponseRecorder {
		return postCall(tn, "/pick", "word-picker.search", `{"args":[" ap "],"vars":{},"seq":2,"at":"c1","scope":{"query":""}}`)
	})
	select {
	case r := <-search:
		var answer struct {
			Components map[string]struct {
				Patch map[string]any `json:"patch"`
			} `json:"components"`
		}
		if json.Unmarshal(r.Body.Bytes(), &answer) != nil || answer.Components["c1"].Patch["query"] != "ap" || answer.Components["c1"].Patch["found"] != float64(2) {
			t.Fatalf("the search should answer what it finds: %d %s", r.Code, r.Body.String())
		}
	case <-time.After(3 * time.Second):
		lock.Unlock()
		t.Fatal("a read-only job of a component should not wait for the call that holds the page's lock")
	}

	add := answered(func() *httptest.ResponseRecorder {
		return postCall(tn, "/pick", "word-picker.add", `{"args":["cherry"],"vars":{},"seq":3,"at":"c1","scope":{"query":""}}`)
	})
	select {
	case r := <-add:
		lock.Unlock()
		t.Fatalf("a job of a component that writes should wait for the page's lock, answered %d %s", r.Code, r.Body.String())
	case <-time.After(300 * time.Millisecond):
	}
	lock.Unlock()
	select {
	case r := <-add:
		if r.Code != http.StatusOK || !strings.Contains(r.Body.String(), `"found":4`) {
			t.Fatalf("the add should go once the lock is let go: %d %s", r.Code, r.Body.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the add should be answered once the lock is let go")
	}
}
