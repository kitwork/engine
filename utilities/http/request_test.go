package http

import (
	stdhttp "net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kitwork/engine/value"
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
	if req.Status().Int() != 200 {
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
	if req.Status().Int() != 404 {
		t.Fatalf("status = %d, want 404", req.res.Status)
	}
	if n := atomic.LoadInt32(&hits); n != 1 {
		t.Fatalf("server hit %d times, want 1 (no retry on 4xx)", n)
	}
}

func TestRequestModifiersBeforeAndAfterVerbAreEquivalent(t *testing.T) {
	base := NewClient(nil, nil)

	before := base.
		Retry(3).
		Timeout(5000).
		MaxBytes(4096).
		Header("X-Test", "before").
		Cache(value.New("5m")).
		Persist(value.New("1h")).
		Get("https://example.test").
		V.(*Request)

	after := base.
		Get("https://example.test").
		V.(*Request).
		Retry(3).
		Timeout(5000).
		MaxBytes(4096).
		Header("X-Test", "before").
		Cache(value.New("5m")).
		Persist(value.New("1h"))

	if before.h.retry != after.h.retry ||
		before.h.timeout != after.h.timeout ||
		before.h.maxBytes != after.h.maxBytes ||
		before.h.headers["X-Test"] != after.h.headers["X-Test"] ||
		before.h.cacheOn != after.h.cacheOn ||
		before.h.cacheTTL != after.h.cacheTTL ||
		before.h.persistOn != after.h.persistOn ||
		before.h.persistTTL != after.h.persistTTL {
		t.Fatalf("request plans differ: before=%+v after=%+v", before.h, after.h)
	}
	if base.retry != 0 || base.timeout != 0 || base.maxBytes != 0 || len(base.headers) != 0 ||
		base.cacheOn || base.persistOn {
		t.Fatalf("builder was mutated by a derived chain: %+v", base)
	}
}

func TestRequestMaxBytesRejectsOversizedResponse(t *testing.T) {
	prev := IsLocalAllowed
	IsLocalAllowed = func() bool { return true }
	defer func() { IsLocalAllowed = prev }()

	srv := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
		_, _ = w.Write([]byte("0123456789"))
	}))
	defer srv.Close()

	// an answer past the limit the program set is a failure, INVALID_INPUT, at the first read; its
	// message says so once, the words .error read until 14/10/2026 (sites compare them)
	request := NewClient(nil, nil).MaxBytes(5).Get(srv.URL).V.(*Request)
	status := request.Status()
	failure, _ := value.FailureFrom(status)
	if status.K != value.Invalid || failure.Code != value.InvalidInputCode ||
		failure.Message != "http: response body exceeds max bytes" {
		t.Fatalf("status = %s %+v, want the INVALID_INPUT failure", status.K, failure)
	}

	// fetch(url, { maxBytes }) is the same limit, spelled as an option
	limited := FetchWith(NewClient(nil, nil), value.New(srv.URL), value.New(map[string]any{"maxBytes": 5})).V.(*Request)
	status = limited.Status()
	failure, _ = value.FailureFrom(status)
	if status.K != value.Invalid || failure.Code != value.InvalidInputCode {
		t.Fatalf("fetch(url, { maxBytes: 5 }).status = %s %+v, want the INVALID_INPUT failure", status.Text(), failure)
	}
	// control: the same fetch with room for the body reads it
	roomy := FetchWith(NewClient(nil, nil), value.New(srv.URL), value.New(map[string]any{"maxBytes": 64})).V.(*Request)
	if text := roomy.Text(); text.Text() != "0123456789" {
		t.Fatalf("fetch(url, { maxBytes: 64 }).text = %s, want the body", text.Text())
	}
}

func TestRequestCommitFiresExactlyOnce(t *testing.T) {
	prev := IsLocalAllowed
	IsLocalAllowed = func() bool { return true }
	defer func() { IsLocalAllowed = prev }()

	var hits int32
	srv := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(stdhttp.StatusNoContent)
	}))
	defer srv.Close()

	request := NewClient(nil, nil).Get(srv.URL).V.(*Request)
	first := request.Commit()
	second := request.Commit()
	_ = request.Status()

	if n := atomic.LoadInt32(&hits); n != 1 {
		t.Fatalf("server hit %d times, want exactly 1", n)
	}
	// an answer, of any status, is no failure and asks the VM for nothing
	if first.Failure.IsError || first.Handler != nil || second.Failure.IsError || second.Handler != nil {
		t.Fatalf("commits = %+v, %+v, want nothing for the VM", first, second)
	}
}

// A request that got no answer is a failure of the program (14/10/2026), as fetch rejects in
// JavaScript: the COMMIT fails with it, and so does every read. Its code says why: NETWORK when nothing
// answered, TIMEOUT past its limit, DENIED when the SSRF guard refused the address. A 4xx or 5xx is an
// answer, and stays one.
func TestRequestWithoutAnAnswerIsAFailure(t *testing.T) {
	prev := IsLocalAllowed
	defer func() { IsLocalAllowed = prev }()

	slow := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
		time.Sleep(300 * time.Millisecond)
		w.WriteHeader(200)
	}))
	defer slow.Close()
	broken := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
		w.WriteHeader(503)
	}))
	defer broken.Close()
	closed := httptest.NewServer(stdhttp.HandlerFunc(func(stdhttp.ResponseWriter, *stdhttp.Request) {}))
	closedURL := closed.URL
	closed.Close()
	// what a URL may carry that a message must never show: its user information and its query
	const secret = "/x?api_key=SECRET123#frag"
	withUser := strings.Replace(closedURL, "://", "://user:hunter2@", 1)

	for _, c := range []struct {
		label, url string
		local      bool
		timeout    int
		code       string
	}{
		{"nothing answers", withUser + secret, true, 0, value.NetworkCode},
		{"past its time limit", slow.URL + secret, true, 50, value.TimeoutCode},
		{"refused by the SSRF guard", slow.URL + secret, false, 0, value.DeniedCode},
		{"not a URL", "http://user:hunter2@[::1]:namedport" + secret, true, 0, value.InvalidArgumentCode},
	} {
		IsLocalAllowed = func() bool { return c.local }
		client := NewClient(nil, nil)
		if c.timeout > 0 {
			client = client.Timeout(c.timeout)
		}
		request := client.Get(c.url).V.(*Request)
		committed := request.Commit()
		failure, _ := value.FailureFrom(committed.Failure)
		if !committed.Failure.IsError || failure.Code != c.code {
			t.Errorf("%s: commit = %+v, want the %s failure", c.label, failure, c.code)
			continue
		}
		// the message names the request, with no user information, query or fragment, and once
		if strings.Contains(failure.Message, "SECRET123") || strings.Contains(failure.Message, "hunter2") ||
			strings.Contains(failure.Message, "user") || strings.Contains(failure.Message, "frag") ||
			strings.HasPrefix(failure.Message, "http: http:") {
			t.Errorf("%s: message %q shows what the URL carried", c.label, failure.Message)
		}
		if c.code != value.InvalidArgumentCode && !strings.Contains(failure.Message, "/x\"") {
			t.Errorf("%s: message %q does not name the request", c.label, failure.Message)
		}
		for name, read := range map[string]func() value.Value{
			"status": request.Status, "ok": request.Ok, "json": request.JSON, "text": request.Text, "body": request.Body,
		} {
			if got := read(); got.K != value.Invalid {
				t.Errorf("%s: .%s = %s, want the failure", c.label, name, got.Text())
			}
		}
	}

	// control: a 503 is an answer, read as one
	IsLocalAllowed = func() bool { return true }
	request := NewClient(nil, nil).Get(broken.URL).V.(*Request)
	if committed := request.Commit(); committed.Failure.IsError {
		t.Fatalf("a 503 failed the commit: %s", committed.Failure.Text())
	}
	if status, ok := request.Status(), request.Ok(); status.Int() != 503 || ok.Truthy() {
		t.Fatalf("a 503 reads status %s ok %s, want 503 and false", status.Text(), ok.Text())
	}
}

// .then and .catch are gone (14/10/2026): a request has no method of those names.
func TestRequestHasNoThenOrCatch(t *testing.T) {
	request := NewClient(nil, nil).Get("https://example.test")
	for _, name := range []string{"then", "catch", "error"} {
		if got := request.Get(name); got.K != value.Nil {
			t.Errorf("request.%s = %s, want nothing", name, got.K)
		}
	}
	if request.V.(*Request).fired {
		t.Fatal("looking for .then fired the request")
	}
}
