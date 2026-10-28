package work

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// A tab listening to its page (28/10/2026, view_listen.go) is told the stamp of the data a job of the page left,
// and nothing else: a job that changes only vars tells nothing; a tab that connects is told the last stamp at
// once; a native app's shell is answered 204; the listener passes the guards of the page.

// listenTo opens a listener on a running server and hands each stamp it is told.
func listenTo(t *testing.T, url string, header http.Header) (<-chan string, *http.Response, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	for name, values := range header {
		req.Header[name] = values
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	stamps := make(chan string, 16)
	go func() {
		defer close(stamps)
		lines := bufio.NewScanner(res.Body)
		for lines.Scan() {
			if stamp, ok := strings.CutPrefix(lines.Text(), "data: "); ok {
				stamps <- stamp
			}
		}
	}()
	return stamps, res, func() { cancel(); res.Body.Close() }
}

func told(stamps <-chan string, within time.Duration) (string, bool) {
	select {
	case stamp, ok := <-stamps:
		return stamp, ok
	case <-time.After(within):
		return "", false
	}
}

func TestAJobTellsTheTabsListeningToItsPage(t *testing.T) {
	tn := patchTodoSite(t)
	server := httptest.NewServer(http.HandlerFunc(tn.Serve))
	defer server.Close()

	stamps, res, stop := listenTo(t, server.URL+"/todo?kitwork-listen=1", nil)
	defer stop()
	if res.StatusCode != http.StatusOK || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("a listener should get a stream: %d %s", res.StatusCode, res.Header.Get("Content-Type"))
	}
	if stamp, ok := told(stamps, 200*time.Millisecond); ok {
		t.Fatalf("nothing changed yet, the listener was told %s", stamp)
	}

	call, err := http.Post(server.URL+"/todo?kitwork-call=add", "application/json", strings.NewReader(`{"args":[],"vars":{"title":"Mua sữa"},"seq":1}`))
	if err != nil {
		t.Fatal(err)
	}
	var answer struct {
		Stamp int64 `json:"stamp"`
	}
	json.NewDecoder(call.Body).Decode(&answer)
	call.Body.Close()
	if answer.Stamp == 0 {
		t.Fatalf("the add answered no stamp")
	}
	want := strconv.FormatInt(answer.Stamp, 10)
	if stamp, _ := told(stamps, 3*time.Second); stamp != want {
		t.Fatalf("a job that changed the page's data should tell the listener its stamp %s, told %q", want, stamp)
	}

	// a tab that connects now is told the last stamp at once
	later, _, stopLater := listenTo(t, server.URL+"/todo?kitwork-listen=1", nil)
	defer stopLater()
	if stamp, _ := told(later, 3*time.Second); stamp != want {
		t.Fatalf("a tab that connects should be told the last stamp %s at once, told %q", want, stamp)
	}
}

func TestAJobThatChangesOnlyVarsTellsNoTab(t *testing.T) {
	tn := urlViewSite(t)
	server := httptest.NewServer(http.HandlerFunc(tn.Serve))
	defer server.Close()
	stamps, _, stop := listenTo(t, server.URL+"/board?kitwork-listen=1", nil)
	defer stop()
	call, err := http.Post(server.URL+"/board?kitwork-call=look", "application/json", strings.NewReader(`{"args":[],"vars":{"seen":"","tab":""},"seq":1}`))
	if err != nil {
		t.Fatal(err)
	}
	call.Body.Close()
	if call.StatusCode != http.StatusOK {
		t.Fatalf("look: %d", call.StatusCode)
	}
	if stamp, ok := told(stamps, 400*time.Millisecond); ok {
		t.Fatalf("a job that changed only the page's vars should tell no tab, told %s", stamp)
	}
}

func TestANativeShellIsNotHeldListening(t *testing.T) {
	tn := patchTodoSite(t)
	req := httptest.NewRequest(http.MethodGet, "http://localhost/todo?kitwork-listen=1", nil)
	req.Header.Set("X-Kitwork-Native", "windows")
	rec := httptest.NewRecorder()
	tn.Serve(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("a native app's shell holds no stream, its listener should be answered 204: %d %q", rec.Code, rec.Body.String())
	}
}

func TestAListenerPassesTheGuards(t *testing.T) {
	tn := guardedViewSite(t)
	// a stream the guard would wrongly let through ends with the request, not the test
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "http://localhost/desk?kitwork-listen=1", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	tn.Serve(rec, req)
	if strings.HasPrefix(rec.Header().Get("Content-Type"), "text/event-stream") || rec.Header().Get("Location") != "/login" {
		t.Fatalf("a listener the guard does not let through should go to the guard's /login: %d %v", rec.Code, rec.Header())
	}

	server := httptest.NewServer(http.HandlerFunc(tn.Serve))
	defer server.Close()
	_, res, stop := listenTo(t, server.URL+"/desk?kitwork-listen=1", http.Header{"Cookie": {"session=in"}})
	defer stop()
	if res.StatusCode != http.StatusOK || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("a listener the guard lets through should get a stream: %d %s", res.StatusCode, res.Header.Get("Content-Type"))
	}
}
