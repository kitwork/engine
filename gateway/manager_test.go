package gateway

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestManagerRejectsInvalidReplacementWithoutChangingSnapshot(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusAccepted)
	}))
	defer upstream.Close()

	manager, err := NewManager([]Target{{Target: upstream.URL}})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()

	if err := manager.Replace([]Target{{Target: "file:///not-an-upstream"}}); err == nil {
		t.Fatal("expected invalid replacement to fail")
	}
	stats := manager.Snapshot()
	if len(stats) != 1 || stats[0].Target != upstream.URL {
		t.Fatalf("published snapshot changed after rejection: %#v", stats)
	}

	response := httptest.NewRecorder()
	if !manager.ServeDefault(response, httptest.NewRequest(http.MethodGet, "http://example.com/", nil)) {
		t.Fatal("expected original default target to remain published")
	}
	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusAccepted)
	}
}

func TestManagerPublishesReplacementBeforeDrainingActiveRequest(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	oldUpstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		writer.Header().Set("X-Gateway", "old")
		writer.WriteHeader(http.StatusAccepted)
	}))
	defer oldUpstream.Close()
	newUpstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("X-Gateway", "new")
		writer.WriteHeader(http.StatusCreated)
	}))
	defer newUpstream.Close()

	manager, err := NewManager([]Target{{Target: oldUpstream.URL}})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()

	oldResponse := httptest.NewRecorder()
	oldDone := make(chan struct{})
	go func() {
		defer close(oldDone)
		manager.ServeDefault(oldResponse, httptest.NewRequest(http.MethodGet, "http://example.com/old", nil))
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("old request did not reach upstream")
	}

	replaced := make(chan error, 1)
	go func() {
		replaced <- manager.Replace([]Target{{Target: newUpstream.URL}})
	}()
	waitForManagerTarget(t, manager, newUpstream.URL, time.Second)

	newResponse := httptest.NewRecorder()
	if !manager.ServeDefault(newResponse, httptest.NewRequest(http.MethodGet, "http://example.com/new", nil)) {
		t.Fatal("expected replacement target to be published")
	}
	if newResponse.Code != http.StatusCreated || newResponse.Header().Get("X-Gateway") != "new" {
		t.Fatalf("new response = %d %q", newResponse.Code, newResponse.Header().Get("X-Gateway"))
	}
	select {
	case err := <-replaced:
		t.Fatalf("replacement returned before old request drained: %v", err)
	default:
	}

	close(release)
	select {
	case <-oldDone:
	case <-time.After(time.Second):
		t.Fatal("old request did not complete")
	}
	if oldResponse.Code != http.StatusAccepted || oldResponse.Header().Get("X-Gateway") != "old" {
		t.Fatalf("old response = %d %q", oldResponse.Code, oldResponse.Header().Get("X-Gateway"))
	}
	select {
	case err := <-replaced:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("replacement did not finish after drain")
	}
}

func TestManagerServesDuringRepeatedReplacements(t *testing.T) {
	server := func(label string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.Header().Set("X-Gateway", label)
			writer.WriteHeader(http.StatusNoContent)
		}))
	}
	first := server("first")
	defer first.Close()
	second := server("second")
	defer second.Close()

	manager, err := NewManager([]Target{{Target: first.URL}})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()

	var failed atomic.Bool
	var workers sync.WaitGroup
	for range 8 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for range 100 {
				response := httptest.NewRecorder()
				if !manager.ServeDefault(response, httptest.NewRequest(http.MethodGet, "http://example.com/", nil)) ||
					response.Code != http.StatusNoContent {
					failed.Store(true)
					return
				}
				label := response.Header().Get("X-Gateway")
				if label != "first" && label != "second" {
					failed.Store(true)
					return
				}
			}
		}()
	}
	for index := range 20 {
		target := first.URL
		if index%2 == 0 {
			target = second.URL
		}
		if err := manager.Replace([]Target{{Target: target}}); err != nil {
			t.Fatal(err)
		}
	}
	workers.Wait()
	if failed.Load() {
		t.Fatal("request failed while gateway snapshots were replaced")
	}
}

func TestManagerReplacementStopsSupersededHealthWorkers(t *testing.T) {
	var healthChecks atomic.Int64
	oldUpstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/health" {
			healthChecks.Add(1)
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer oldUpstream.Close()
	newUpstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer newUpstream.Close()

	manager, err := newManager(
		[]Target{{Target: oldUpstream.URL, Health: "/health"}},
		handlerOptions{healthInterval: 5 * time.Millisecond, healthTimeout: time.Second},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()

	deadline := time.Now().Add(time.Second)
	for healthChecks.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if healthChecks.Load() < 2 {
		t.Fatal("old health worker did not start")
	}
	if err := manager.Replace([]Target{{Target: newUpstream.URL}}); err != nil {
		t.Fatal(err)
	}
	stoppedAt := healthChecks.Load()
	time.Sleep(25 * time.Millisecond)
	if got := healthChecks.Load(); got != stoppedAt {
		t.Fatalf("superseded health worker continued: checks %d -> %d", stoppedAt, got)
	}
}

func TestManagerCloseDrainsAndRejectsReplacement(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	manager, err := NewManager([]Target{{Target: upstream.URL}})
	if err != nil {
		t.Fatal(err)
	}

	manager.Close()
	manager.Close()
	if manager.ServeDefault(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "http://example.com/", nil)) {
		t.Fatal("closed manager served a request")
	}
	if err := manager.Replace([]Target{{Target: upstream.URL}}); !errors.Is(err, ErrManagerClosed) {
		t.Fatalf("replacement error = %v, want %v", err, ErrManagerClosed)
	}
}

func waitForManagerTarget(t *testing.T, manager *Manager, target string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		stats := manager.Snapshot()
		if len(stats) == 1 && stats[0].Target == target {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("gateway target %q was not published before timeout", target)
}
