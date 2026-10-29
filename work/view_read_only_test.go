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
