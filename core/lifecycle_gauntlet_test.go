package core

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kitwork/engine/work"
)

func writeLifecycleRoute(
	t testing.TB,
	root string,
	relativePath string,
	source string,
) string {
	t.Helper()
	directory := filepath.Join(root, "acme", "localhost", relativePath)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	routerFile := filepath.Join(directory, work.RouterFileName)
	if err := os.WriteFile(routerFile, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	return routerFile
}

func waitLifecycleCondition(
	t testing.TB,
	description string,
	condition func() bool,
) {
	t.Helper()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		if condition() {
			return
		}
		select {
		case <-deadline.C:
			t.Fatalf("timed out waiting for %s", description)
		case <-ticker.C:
		}
	}
}

func readLifecycleSSEBlock(
	t testing.TB,
	reader *bufio.Reader,
) string {
	t.Helper()
	result := make(chan string, 1)
	errors := make(chan error, 1)
	go func() {
		var block strings.Builder
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				errors <- err
				return
			}
			block.WriteString(line)
			if line == "\n" || line == "\r\n" {
				result <- block.String()
				return
			}
		}
	}()

	select {
	case block := <-result:
		return block
	case err := <-errors:
		t.Fatalf("read SSE block: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for SSE block")
	}
	return ""
}

func waitLifecycleSSEClosed(t testing.TB, reader *bufio.Reader) {
	t.Helper()
	closed := make(chan error, 1)
	go func() {
		for {
			_, err := reader.ReadString('\n')
			if err != nil {
				closed <- err
				return
			}
		}
	}()
	select {
	case err := <-closed:
		if err != io.EOF && !strings.Contains(err.Error(), "closed") {
			t.Fatalf("SSE stream closed with unexpected error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("site shutdown did not close the SSE stream")
	}
}

// TestEngineLifecycleGauntlet exercises the complete production ownership
// hierarchy with work accepted at three different lifetimes:
//
//   - a request holds the old generation and its primary VM in native I/O;
//   - an SSE connection is handed from request/generation ownership to the
//     site broker and must survive hot reload;
//   - an app task observes cancellation but controls when app shutdown drains.
//
// The barriers are channels and observable lifecycle state. Sleeps do not
// decide correctness, so the test remains meaningful under the race detector.
func TestEngineLifecycleGauntlet(t *testing.T) {
	savedAllowLocal := work.AllowLocal
	work.AllowLocal = true
	t.Cleanup(func() { work.AllowLocal = savedAllowLocal })

	requestEntered := make(chan struct{})
	releaseRequest := make(chan struct{})
	var requestEnteredOnce sync.Once
	var releaseRequestOnce sync.Once
	upstream := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		requestEnteredOnce.Do(func() { close(requestEntered) })
		select {
		case <-releaseRequest:
			_, _ = writer.Write([]byte("released"))
		case <-request.Context().Done():
		}
	}))

	root := t.TempDir()
	writeLifecycleRoute(t, root, "", `
import { router } from "kitwork";
router.get((ctx) => ctx.text("v1"));
`)
	writeLifecycleRoute(t, root, "hold", fmt.Sprintf(`
import { router, http } from "kitwork";
router.get((ctx) => {
	const response = http.get(%q);
	return ctx.text(response.text());
});
`, upstream.URL))
	writeLifecycleRoute(t, root, "events", `
import { router } from "kitwork";
router.get((ctx, sse) => {
	sse.connect({ channel: "updates" });
});
`)

	engine := New(root, 1_000_000, true, "localhost")
	server := httptest.NewServer(engine)
	var stream *http.Response
	taskRelease := make(chan struct{})
	var taskReleaseOnce sync.Once
	t.Cleanup(func() {
		releaseRequestOnce.Do(func() { close(releaseRequest) })
		taskReleaseOnce.Do(func() { close(taskRelease) })
		if stream != nil {
			_ = stream.Body.Close()
		}
		engine.Close()
		server.Close()
		upstream.Close()
	})

	initial := httptest.NewRecorder()
	engine.ServeHTTP(
		initial,
		httptest.NewRequest(http.MethodGet, "http://localhost/", nil),
	)
	if initial.Code != http.StatusOK || initial.Body.String() != "v1" {
		t.Fatalf("initial response = %d %q", initial.Code, initial.Body.String())
	}

	engine.mu.RLock()
	cached := engine.cache["localhost"]
	engine.mu.RUnlock()
	if cached == nil {
		t.Fatal("initial request did not publish a cached tenant")
	}
	oldTenant := cached.current()
	oldGeneration := oldTenant.SiteGeneration()
	appRuntime := oldTenant.AppRuntime()
	siteRuntime := oldTenant.SiteRuntime()
	broker := oldTenant.SSEBroker()
	tree, ok := oldGeneration.RouteGraph().(*work.RouteTree)
	if !ok || !tree.Resolve("/events").Found || !tree.Resolve("/hold").Found {
		t.Fatal("prepared generation did not include lifecycle child routes")
	}

	stream, err := server.Client().Get(server.URL + "/events")
	if err != nil {
		t.Fatal(err)
	}
	if stream.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(stream.Body)
		t.Fatalf("SSE status = %d: %s", stream.StatusCode, body)
	}
	streamReader := bufio.NewReader(stream.Body)
	if block := readLifecycleSSEBlock(t, streamReader); !strings.Contains(block, "event: init") {
		t.Fatalf("SSE init block = %q", block)
	}
	waitLifecycleCondition(t, "SSE broker registration", func() bool {
		return broker.ClientCount() == 1
	})

	holdDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		response := httptest.NewRecorder()
		engine.ServeHTTP(
			response,
			httptest.NewRequest(http.MethodGet, "http://localhost/hold", nil),
		)
		holdDone <- response
	}()
	select {
	case <-requestEntered:
	case <-time.After(3 * time.Second):
		t.Fatal("request VM did not enter the blocking native call")
	}
	if oldGeneration.Active() != 1 {
		t.Fatalf(
			"old generation active leases = %d, want only the blocking request",
			oldGeneration.Active(),
		)
	}
	if active := engine.Health().VMPool.Active; active != 1 {
		t.Fatalf("active VM leases = %d, want 1 during native I/O", active)
	}

	taskContext, taskDone, ok := appRuntime.Tasks().Start()
	if !ok {
		t.Fatal("open app runtime rejected lifecycle task")
	}
	taskStarted := make(chan struct{})
	taskCancelled := make(chan struct{})
	go func() {
		defer taskDone()
		close(taskStarted)
		<-taskContext.Done()
		close(taskCancelled)
		<-taskRelease
	}()
	<-taskStarted

	writeLifecycleRoute(t, root, "", `
import { router } from "kitwork";
router.get((ctx) => ctx.text("v2"));
`)
	cached.mu.Lock()
	cached.lastChecked = time.Time{}
	cached.mu.Unlock()

	reloadDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		response := httptest.NewRecorder()
		engine.ServeHTTP(
			response,
			httptest.NewRequest(http.MethodGet, "http://localhost/", nil),
		)
		reloadDone <- response
	}()
	waitLifecycleCondition(t, "replacement generation publication", func() bool {
		return cached.current() != oldTenant
	})
	select {
	case response := <-reloadDone:
		t.Fatalf(
			"hot reload returned before its accepted old request drained: %d %q",
			response.Code,
			response.Body.String(),
		)
	default:
	}

	releaseRequestOnce.Do(func() { close(releaseRequest) })
	select {
	case response := <-holdDone:
		if response.Code != http.StatusOK || response.Body.String() != "released" {
			t.Fatalf("blocking response = %d %q", response.Code, response.Body.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("blocking request did not finish after release")
	}

	select {
	case response := <-reloadDone:
		if response.Code != http.StatusOK || response.Body.String() != "v2" {
			t.Fatalf("reload response = %d %q", response.Code, response.Body.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("SSE stream prevented the old generation from draining")
	}

	currentTenant := cached.current()
	currentGeneration := currentTenant.SiteGeneration()
	if currentTenant == oldTenant || currentGeneration == oldGeneration {
		t.Fatal("hot reload did not replace the tenant generation")
	}
	if !oldGeneration.Retired() || oldGeneration.Active() != 0 {
		t.Fatalf(
			"old generation did not drain: retired=%v active=%d",
			oldGeneration.Retired(),
			oldGeneration.Active(),
		)
	}
	if active := engine.Health().VMPool.Active; active != 0 {
		t.Fatalf("VM pool retained %d active leases after reload drain", active)
	}
	if siteRuntime.SSEBroker() != broker || broker.ClientCount() != 1 {
		t.Fatal("hot reload replaced or disconnected the site SSE broker")
	}
	broker.Publish("updates", "reload-1", []byte("event: reload\ndata: v2\n\n"))
	if block := readLifecycleSSEBlock(t, streamReader); !strings.Contains(block, "data: v2") {
		t.Fatalf("SSE block after reload = %q", block)
	}

	engineClosed := make(chan struct{})
	go func() {
		engine.Close()
		close(engineClosed)
	}()
	select {
	case <-taskCancelled:
	case <-time.After(3 * time.Second):
		t.Fatal("engine shutdown did not cancel accepted app work")
	}
	select {
	case <-engineClosed:
		t.Fatal("engine closed before its accepted app task drained")
	default:
	}

	rejected := httptest.NewRecorder()
	engine.ServeHTTP(
		rejected,
		httptest.NewRequest(http.MethodGet, "http://localhost/", nil),
	)
	if rejected.Code < http.StatusBadRequest {
		t.Fatalf("closing engine accepted a new request: %d", rejected.Code)
	}
	waitLifecycleSSEClosed(t, streamReader)

	taskReleaseOnce.Do(func() { close(taskRelease) })
	select {
	case <-engineClosed:
	case <-time.After(3 * time.Second):
		t.Fatal("engine shutdown did not finish after app task drain")
	}

	if !appRuntime.Closed() || !siteRuntime.Closed() {
		t.Fatal("engine shutdown did not close the app/site hierarchy")
	}
	if !currentGeneration.Retired() || currentGeneration.Active() != 0 {
		t.Fatalf(
			"current generation did not retire: retired=%v active=%d",
			currentGeneration.Retired(),
			currentGeneration.Active(),
		)
	}
	if oldGeneration.RouteGraph() != nil || currentGeneration.RouteGraph() != nil {
		t.Fatal("shutdown retained a retired generation route graph")
	}
	if _, _, accepted := appRuntime.Tasks().Start(); accepted {
		t.Fatal("closed app runtime accepted new work")
	}

	waitLifecycleCondition(t, "runtime health request drain", func() bool {
		return engine.Health().Requests.Inflight == 0
	})
	health := engine.Health()
	if health.LoadedApps != 0 ||
		health.LoadedSites != 0 ||
		health.ActiveGenerations != 0 ||
		health.ActiveGenerationLeases != 0 ||
		health.VMPool.Active != 0 ||
		health.Requests.Completed != health.Requests.Started {
		t.Fatalf("runtime health after shutdown = %+v", health)
	}

	engine.Close()
}
