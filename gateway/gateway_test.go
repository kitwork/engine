package gateway

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestHandlerRoutesExactDomainAndRewritesTargetPath(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		fmt.Fprintf(
			writer,
			"%s|%s|%s|%s|%s",
			request.URL.EscapedPath(),
			request.URL.RawQuery,
			request.Host,
			request.Header.Get("X-Forwarded-For"),
			request.Header.Get("X-Forwarded-Host"),
		)
	}))
	defer upstream.Close()

	handler, err := New([]Target{{
		Domain: "API.Example.COM.",
		Target: upstream.URL + "/base",
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer handler.Close()

	request := httptest.NewRequest(http.MethodGet, "http://api.example.com/items?q=kitwork", nil)
	request.Host = "api.example.com"
	request.RemoteAddr = "203.0.113.7:4321"
	request.Header.Set("X-Forwarded-For", "198.51.100.9")
	response := httptest.NewRecorder()
	if !handler.ServeExact(response, request, "api.example.com") {
		t.Fatal("expected exact gateway match")
	}

	want := "/base/items|q=kitwork|api.example.com|203.0.113.7|api.example.com"
	if got := response.Body.String(); got != want {
		t.Fatalf("upstream request = %q, want %q", got, want)
	}
	if handler.ServeExact(httptest.NewRecorder(), request, "other.example.com") {
		t.Fatal("unexpected gateway match for undeclared domain")
	}
}

func TestHandlerUsesSmoothWeightedRoundRobin(t *testing.T) {
	var first atomic.Int64
	var second atomic.Int64
	server := func(counter *atomic.Int64) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			counter.Add(1)
			writer.WriteHeader(http.StatusNoContent)
		}))
	}
	firstServer := server(&first)
	defer firstServer.Close()
	secondServer := server(&second)
	defer secondServer.Close()

	handler, err := New([]Target{
		{Domain: "api.example.com", Target: firstServer.URL, Weight: 2},
		{Domain: "api.example.com", Target: secondServer.URL, Weight: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer handler.Close()

	for range 6 {
		request := httptest.NewRequest(http.MethodGet, "http://api.example.com/", nil)
		request.Host = "api.example.com"
		if !handler.ServeExact(httptest.NewRecorder(), request, "api.example.com") {
			t.Fatal("expected exact gateway match")
		}
	}
	if first.Load() != 4 || second.Load() != 2 {
		t.Fatalf("weighted requests = %d:%d, want 4:2", first.Load(), second.Load())
	}
}

func TestHandlerDefaultPoolAndValidation(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusAccepted)
	}))
	defer upstream.Close()

	handler, err := New([]Target{{Target: upstream.URL}})
	if err != nil {
		t.Fatal(err)
	}
	defer handler.Close()
	if !handler.HasDefault() {
		t.Fatal("expected default gateway pool")
	}
	response := httptest.NewRecorder()
	if !handler.ServeDefault(response, httptest.NewRequest(http.MethodGet, "http://anything.example/", nil)) {
		t.Fatal("expected default gateway match")
	}
	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusAccepted)
	}

	invalid := []Target{{Domain: "bad:8080", Target: upstream.URL}}
	if _, err := New(invalid); err == nil {
		t.Fatal("expected invalid domain error")
	}
	invalid = []Target{{Target: "file:///tmp/backend"}}
	if _, err := New(invalid); err == nil {
		t.Fatal("expected invalid target scheme error")
	}
	invalid = []Target{{Target: upstream.URL, Health: "health"}}
	if _, err := New(invalid); err == nil {
		t.Fatal("expected relative health path error")
	}
}

func TestPassiveFailureSkipsUnavailableTargetAndPublishesMetrics(t *testing.T) {
	first := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer second.Close()

	handler, err := newHandler([]Target{
		{Domain: "api.example.com", Target: first.URL},
		{Domain: "api.example.com", Target: second.URL},
	}, handlerOptions{failureThreshold: 1, failureCooldown: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	defer handler.Close()

	request := func() *http.Request {
		return httptest.NewRequest(http.MethodGet, "http://api.example.com/", nil)
	}
	failed := httptest.NewRecorder()
	handler.ServeExact(failed, request(), "api.example.com")
	if failed.Code != http.StatusServiceUnavailable {
		t.Fatalf("first status = %d, want upstream 503", failed.Code)
	}
	success := httptest.NewRecorder()
	handler.ServeExact(success, request(), "api.example.com")
	if success.Code != http.StatusNoContent {
		t.Fatalf("failover status = %d, want 204", success.Code)
	}

	stats := handler.Snapshot()
	if len(stats) != 2 {
		t.Fatalf("stats targets = %d, want 2", len(stats))
	}
	var failedTarget, healthyTarget TargetStats
	for _, target := range stats {
		if target.Target == first.URL {
			failedTarget = target
		} else if target.Target == second.URL {
			healthyTarget = target
		}
	}
	if failedTarget.Available || failedTarget.Failures != 1 || failedTarget.Requests != 1 {
		t.Fatalf("failed target stats = %#v", failedTarget)
	}
	if !healthyTarget.Available || healthyTarget.Responses != 1 || healthyTarget.Requests != 1 {
		t.Fatalf("healthy target stats = %#v", healthyTarget)
	}
}

func TestPassiveFailureAllowsProbeAfterCooldown(t *testing.T) {
	var status atomic.Int64
	var calls atomic.Int64
	status.Store(http.StatusServiceUnavailable)
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		writer.WriteHeader(int(status.Load()))
	}))
	defer upstream.Close()

	handler, err := newHandler(
		[]Target{{Target: upstream.URL}},
		handlerOptions{failureThreshold: 1, failureCooldown: 20 * time.Millisecond},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer handler.Close()

	handler.ServeDefault(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "http://example.com/", nil))
	blocked := httptest.NewRecorder()
	handler.ServeDefault(blocked, httptest.NewRequest(http.MethodGet, "http://example.com/", nil))
	if blocked.Code != http.StatusServiceUnavailable {
		t.Fatalf("blocked status = %d, want 503", blocked.Code)
	}
	if calls.Load() != 1 {
		t.Fatalf("upstream calls during cooldown = %d, want 1", calls.Load())
	}

	status.Store(http.StatusNoContent)
	time.Sleep(30 * time.Millisecond)
	recovered := httptest.NewRecorder()
	handler.ServeDefault(recovered, httptest.NewRequest(http.MethodGet, "http://example.com/", nil))
	if recovered.Code != http.StatusNoContent || calls.Load() != 2 || !handler.Snapshot()[0].Available {
		t.Fatalf("recovered response = %d stats=%#v", recovered.Code, handler.Snapshot()[0])
	}
}

func TestActiveHealthCheckRemovesAndRestoresTarget(t *testing.T) {
	var health atomic.Int64
	health.Store(http.StatusServiceUnavailable)
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/health" {
			writer.WriteHeader(int(health.Load()))
			return
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	handler, err := newHandler(
		[]Target{{Target: upstream.URL, Health: "/health"}},
		handlerOptions{
			failureThreshold: 1,
			failureCooldown:  time.Second,
			healthInterval:   10 * time.Millisecond,
			healthTimeout:    time.Second,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer handler.Close()

	waitForGatewayState(t, handler, time.Second, func(stats TargetStats) bool {
		return stats.HealthChecks > 0 && stats.HealthFailures > 0 && !stats.Available
	})
	health.Store(http.StatusNoContent)
	waitForGatewayState(t, handler, time.Second, func(stats TargetStats) bool {
		return stats.HealthChecks > 1 && stats.Available && stats.ConsecutiveFailures == 0
	})

	response := httptest.NewRecorder()
	handler.ServeDefault(response, httptest.NewRequest(http.MethodGet, "http://example.com/", nil))
	if response.Code != http.StatusNoContent {
		t.Fatalf("recovered health target status = %d, want 204", response.Code)
	}
}

func waitForGatewayState(t *testing.T, handler *Handler, timeout time.Duration, match func(TargetStats) bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		all := handler.Snapshot()
		if len(all) != 1 {
			t.Fatalf("stats targets = %d, want 1", len(all))
		}
		stats := all[0]
		if match(stats) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("gateway state did not converge before timeout")
}
