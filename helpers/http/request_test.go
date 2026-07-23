package http

import (
	stdhttp "net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// The lazy Request must (1) NOT fire until a field is read, (2) fire exactly once no matter how many
// fields are read, and (3) return the same values the old eager Response did — so the 7 tenant files
// that do `const res = http.cache().get(url); if (res.status != 200) …; res.json()` keep working.
func TestLazyRequestFiresOnceOnRead(t *testing.T) {
	prev := IsLocalAllowed
	IsLocalAllowed = func() bool { return true } // httptest is 127.0.0.1 — skip the SSRF backstop
	defer func() { IsLocalAllowed = prev }()

	var hits int32
	srv := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"n":42}`))
	}))
	defer srv.Close()

	reqVal := NewClient(nil, nil).Get(srv.URL)
	r, ok := reqVal.V.(*Request)
	if !ok {
		t.Fatalf("Get() should return *Request, got %T", reqVal.V)
	}
	if r.fired {
		t.Fatal("request fired before any field was read")
	}
	if atomic.LoadInt32(&hits) != 0 {
		t.Fatal("network hit before read — not lazy")
	}

	// The VM reads `.status` as a 0-arg getter (navigation.reflect auto-calls Status()).
	if got := reqVal.Get("status"); got.Int() != 200 {
		t.Fatalf(".status = %v, want 200", got.Int())
	}
	if !r.fired {
		t.Fatal("reading .status should have fired the request")
	}

	// `.json` and `.body` read the SAME fired result — no second network hit.
	if got := reqVal.Get("json"); got.Get("n").Int() != 42 {
		t.Fatalf(".json().n = %v, want 42", got.Get("n").Int())
	}
	if got := reqVal.Get("ok"); got.N != 1 {
		t.Fatalf(".ok = %v, want true", got.N)
	}
	if n := atomic.LoadInt32(&hits); n != 1 {
		t.Fatalf("request fired %d times, want exactly 1 (memoised)", n)
	}
}

// .retry(n) re-attempts a 5xx on a GET, then gives up with the last response.
func TestLazyRequestRetryTransient(t *testing.T) {
	prev := IsLocalAllowed
	IsLocalAllowed = func() bool { return true }
	defer func() { IsLocalAllowed = prev }()

	var hits int32
	srv := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		n := atomic.AddInt32(&hits, 1)
		if n < 3 {
			w.WriteHeader(503) // transient — should be retried
			return
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	req := NewClient(nil, nil).Get(srv.URL).V.(*Request).Retry(3)
	if req.Status() != 200 {
		t.Fatalf("status = %d, want 200 after retries", req.res.Status)
	}
	if n := atomic.LoadInt32(&hits); n != 3 {
		t.Fatalf("server hit %d times, want 3 (2 failures + 1 success)", n)
	}
}

// A 4xx is a definite answer and must NOT be retried.
func TestLazyRequestNoRetryOn4xx(t *testing.T) {
	prev := IsLocalAllowed
	IsLocalAllowed = func() bool { return true }
	defer func() { IsLocalAllowed = prev }()

	var hits int32
	srv := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(404)
	}))
	defer srv.Close()

	req := NewClient(nil, nil).Get(srv.URL).V.(*Request).Retry(3)
	if req.Status() != 404 {
		t.Fatalf("status = %d, want 404", req.res.Status)
	}
	if n := atomic.LoadInt32(&hits); n != 1 {
		t.Fatalf("server hit %d times, want 1 (no retry on 4xx)", n)
	}
}
