package http

import (
	"fmt"
	"io"
	"net"
	stdhttp "net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kitwork/engine/internal/loopback"
	"github.com/kitwork/engine/value"
)

// These tests go through value.Invoke and value.Get, the path the VM takes for http.get(url).send(),
// so what they read is what a program reads.

// openLoopback lets the test's httptest server (127.0.0.1) through the SSRF guard.
func openLoopback(t *testing.T) {
	t.Helper()
	t.Cleanup(loopback.Open())
}

// invoke is receiver.name(args…) as the VM calls it.
func invoke(receiver value.Value, name string, args ...any) value.Value {
	converted := make([]value.Value, len(args))
	for i, arg := range args {
		converted[i] = value.New(arg)
	}
	return receiver.Invoke(name, converted...)
}

// describe is http.<method>(url) on a module with no cache tiers.
func describe(method, url string) value.Value {
	return invoke(value.New(NewModule(nil, nil, "")), method, url)
}

// failureOf is the failure v carries, or the test's end when v is none.
func failureOf(t *testing.T, label string, v value.Value) value.Failure {
	t.Helper()
	failure, ok := value.FailureFrom(v)
	if v.K != value.Invalid || !ok {
		t.Fatalf("%s = %s %s, want a failure", label, v.K, v.Text())
	}
	return failure
}

// wantFailure checks a failure's code and that its message says what to write.
func wantFailure(t *testing.T, label string, v value.Value, code string, says ...string) {
	t.Helper()
	failure := failureOf(t, label, v)
	if failure.Code != code {
		t.Errorf("%s: code %s (%q), want %s", label, failure.Code, failure.Message, code)
	}
	for _, said := range says {
		if !strings.Contains(failure.Message, said) {
			t.Errorf("%s: message %q does not say %q", label, failure.Message, said)
		}
	}
}

// counting is an httptest server that counts its requests and answers with handle.
func counting(t *testing.T, handle func(stdhttp.ResponseWriter, *stdhttp.Request)) (*httptest.Server, *int32) {
	t.Helper()
	var hits int32
	server := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		atomic.AddInt32(&hits, 1)
		handle(w, r)
	}))
	t.Cleanup(server.Close)
	return server, &hits
}

// The method and its modifiers only describe: nothing reaches the network until .send(), reading a
// result on a request never sent is an error naming .send() (it used to send then), .send() sends each
// time it is called, and reading the body again never sends again. A modifier gives a changed copy: a
// description sent with one more header and parameter is still itself, sent again.
func TestDescribingSendsNothingAndSendSendsOnce(t *testing.T) {
	openLoopback(t)
	var seen sync.Map // the n-th request → its query and X-Extra header
	var count int32
	server, hits := counting(t, func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		seen.Store(atomic.AddInt32(&count, 1), r.URL.RawQuery+" X-Extra:"+r.Header.Get("X-Extra"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"n":42}`)
	})

	feed := describe("get", server.URL)
	feed = invoke(feed, "header", "Accept", "application/json")
	feed = invoke(feed, "query", map[string]any{"page": 1})
	feed = invoke(feed, "timeout", "5s")
	if feed.K != value.Struct {
		t.Fatalf("the chain = %s %s, want a request", feed.K, feed.Text())
	}
	for _, name := range []string{"status", "ok", "json", "body"} {
		wantFailure(t, "unsent ."+name, feed.Get(name), value.InvalidArgumentCode, "chưa gửi", ".send()")
	}
	if n := atomic.LoadInt32(hits); n != 0 {
		t.Fatalf("describing reached the network %d times, want 0", n)
	}

	result := invoke(feed, "send")
	if n := atomic.LoadInt32(hits); n != 1 {
		t.Fatalf("send reached the network %d times, want 1", n)
	}
	for i := 0; i < 3; i++ {
		if got := invoke(result, "json").Get("n"); got.Int() != 42 {
			t.Fatalf("json().n = %s, want 42", got.Text())
		}
		if got := invoke(result, "text"); got.Text() != `{"n":42}` {
			t.Fatalf("text() = %s", got.Text())
		}
		if got := invoke(result, "bytes"); got.K != value.Bytes || string(got.Bytes()) != `{"n":42}` {
			t.Fatalf("bytes() = %s %s", got.K, got.Text())
		}
	}
	if n := atomic.LoadInt32(hits); n != 1 {
		t.Fatalf("reading the body sent again: %d hits, want 1", n)
	}
	// a modifier changes a copy: feed sent with a header and a parameter more…
	invoke(invoke(invoke(feed, "header", "X-Extra", "1"), "query", map[string]any{"extra": 1}), "send")
	// …and feed itself, sent again, is another request without them
	invoke(feed, "send")
	if n := atomic.LoadInt32(hits); n != 3 {
		t.Fatalf("two more .send() made %d hits in all, want 3", n)
	}
	for n, want := range map[int32]string{1: "page=1 X-Extra:", 2: "extra=1&page=1 X-Extra:1", 3: "page=1 X-Extra:"} {
		if got, _ := seen.Load(n); got != want {
			t.Errorf("request %d saw %q, want %q (a modifier must not change the description it is called on)", n, got, want)
		}
	}
}

// .query() merges (a later name wins), encodes, and goes after a query the URL already has. What its
// encoding is still open for (null, a list, a nested object) is an error, before any request.
func TestQueryMergesAndEncodes(t *testing.T) {
	openLoopback(t)
	var seen sync.Map
	server, hits := counting(t, func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		seen.Store(r.URL.Path, r.URL.RawQuery)
	})

	request := invoke(describe("get", server.URL+"/a"), "query", map[string]any{"page": 1, "search": "kit work", "limit": 20})
	request = invoke(request, "query", map[string]any{"page": 2, "flag": true})
	if result := invoke(request, "send"); !invoke(result, "ok").Truthy() && !result.Get("ok").Truthy() {
		t.Fatalf("send = %s", result.Text())
	}
	if got, _ := seen.Load("/a"); got != "flag=true&limit=20&page=2&search=kit+work" {
		t.Errorf("query = %v, want flag=true&limit=20&page=2&search=kit+work", got)
	}
	invoke(invoke(describe("get", server.URL+"/b?source=web#top"), "query", map[string]any{"page": 1}), "send")
	if got, _ := seen.Load("/b"); got != "source=web&page=1" {
		t.Errorf("query after the URL's = %v, want source=web&page=1", got)
	}

	before := atomic.LoadInt32(hits)
	for label, field := range map[string]any{"null": nil, "a list": []any{1, 2}, "an object": map[string]any{"a": 1}} {
		wantFailure(t, ".query() of "+label, invoke(describe("get", server.URL), "query", map[string]any{"x": field}),
			value.InvalidArgumentCode, `"x"`, "chưa có")
	}
	if n := atomic.LoadInt32(hits); n != before {
		t.Fatalf("a refused .query() sent %d requests", n-before)
	}
}

// .header() and .headers() merge; a name is one header whatever its case, the later value winning. A
// line break or NUL in a value is an error that never echoes the value (it may be a token), and no
// request goes out: until 22/10/2026 Go's transport refused it as a NETWORK failure.
func TestHeadersMergeAndRefuseInjection(t *testing.T) {
	openLoopback(t)
	var accept, count atomic.Value
	server, hits := counting(t, func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		accept.Store(strings.Join(r.Header.Values("Accept"), "|"))
		count.Store(r.Header.Get("X-Count"))
	})

	request := invoke(describe("get", server.URL), "header", "accept", "text/plain")
	request = invoke(request, "headers", map[string]any{"Accept": "application/json", "X-Count": 2})
	invoke(request, "send")
	if got := accept.Load(); got != "application/json" {
		t.Errorf("Accept = %v, want the later value alone", got)
	}
	if got := count.Load(); got != "2" {
		t.Errorf("X-Count = %v, want 2", got)
	}

	before := atomic.LoadInt32(hits)
	for label, v := range map[string]value.Value{
		"CR LF in .header()":  invoke(describe("get", server.URL), "header", "X-Token", "SECRET\r\nX-Injected: 1"),
		"LF in .headers()":    invoke(describe("get", server.URL), "headers", map[string]any{"X-Token": "SECRET\nX-Injected: 1"}),
		"NUL in .header()":    invoke(describe("get", server.URL), "header", "X-Token", "SECRET\x00"),
		"a name with a space": invoke(describe("get", server.URL), "header", "X Token", "x"),
	} {
		failure := failureOf(t, label, v)
		if failure.Code != value.InvalidArgumentCode || strings.Contains(failure.Message, "SECRET") {
			t.Errorf("%s = %s %q, want INVALID_ARGUMENT without the value", label, failure.Code, failure.Message)
		}
	}
	wantFailure(t, "CR LF", invoke(describe("get", server.URL), "header", "X-Token", "a\r\nb"),
		value.InvalidArgumentCode, `"X-Token"`, "chống chèn header")
	if n := atomic.LoadInt32(hits); n != before {
		t.Fatalf("a refused header sent %d requests", n-before)
	}
}

// .send(data): an object or a list as JSON with Content-Type application/json unless the program set
// one, a string as text, bytes as they are. GET and HEAD take no body: an error naming .query().
func TestSendBodyByKind(t *testing.T) {
	openLoopback(t)
	type seenRequest struct{ method, contentType, body string }
	var last atomic.Value
	server, hits := counting(t, func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		body, _ := io.ReadAll(r.Body)
		last.Store(seenRequest{r.Method, r.Header.Get("Content-Type"), string(body)})
	})

	for _, c := range []struct {
		label  string
		method string
		header []any
		data   any
		want   seenRequest
	}{
		{"an object", "post", nil, map[string]any{"name": "Kitwork", "active": true},
			seenRequest{"POST", "application/json", `{"active":true,"name":"Kitwork"}`}},
		{"a list", "put", nil, []any{"one", "two"}, seenRequest{"PUT", "application/json", `["one","two"]`}},
		{"a string", "post", nil, "Hello world", seenRequest{"POST", "", "Hello world"}},
		{"bytes", "put", nil, []byte{0, 1, 2, 255}, seenRequest{"PUT", "", "\x00\x01\x02\xff"}},
		{"its own Content-Type", "patch", []any{"Content-Type", "application/merge-patch+json"}, map[string]any{"a": 1},
			seenRequest{"PATCH", "application/merge-patch+json", `{"a":1}`}},
		{"nothing", "delete", nil, nil, seenRequest{"DELETE", "", ""}},
	} {
		request := describe(c.method, server.URL)
		if c.header != nil {
			request = invoke(request, "header", c.header...)
		}
		var result value.Value
		if c.data == nil {
			result = invoke(request, "send")
		} else {
			result = invoke(request, "send", c.data)
		}
		if !result.Get("ok").Truthy() {
			t.Errorf("%s: send = %s", c.label, result.Text())
			continue
		}
		if got, _ := last.Load().(seenRequest); got != c.want {
			t.Errorf("%s: upstream saw %+v, want %+v", c.label, got, c.want)
		}
	}

	before := atomic.LoadInt32(hits)
	wantFailure(t, "GET with a body", invoke(describe("get", server.URL), "send", map[string]any{"page": 1}),
		value.InvalidArgumentCode, "http.get(…).send() không nhận thân", ".query(")
	wantFailure(t, "HEAD with a body", invoke(describe("head", server.URL), "send", "x"),
		value.InvalidArgumentCode, ".query(")
	wantFailure(t, "a number as the body", invoke(describe("post", server.URL), "send", 42),
		value.InvalidArgumentCode, "object, mảng, chuỗi hay bytes")
	if n := atomic.LoadInt32(hits); n != before {
		t.Fatalf("a refused body sent %d requests", n-before)
	}
}

// outcome reads a result as a program does.
type outcome struct {
	ok           bool
	status, code value.Value
	error        value.Value
}

func readOutcome(result value.Value) outcome {
	return outcome{
		ok:     result.Get("ok").Truthy(),
		status: result.Get("status"),
		code:   result.Get("code"),
		error:  result.Get("error"),
	}
}

// same is a and b of one kind and equal; null only equals null.
func same(a, b value.Value) bool {
	return a.K == b.K && (a.K == value.Nil || a.Equal(b))
}

func (o outcome) String() string {
	return fmt.Sprintf("{ ok: %v, status: %s, code: %s, error: %s }", o.ok, written(o.status), written(o.code), written(o.error))
}

// The table of KITWORK_HTTP.md mục 8, one row per final outcome: every one is the same object, ok tells
// them apart, status is null without an answer, code is null with one, and error is Kitwork's sentence.
func TestOutcomesTable(t *testing.T) {
	openLoopback(t)
	server, _ := counting(t, func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		switch r.URL.Path {
		case "/slow":
			time.Sleep(300 * time.Millisecond)
		case "/size":
			n, _ := strconv.Atoi(r.URL.Query().Get("n"))
			_, _ = w.Write([]byte(strings.Repeat("x", n)))
			return
		}
		if status, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/")); status > 0 {
			w.WriteHeader(status)
		}
		_, _ = io.WriteString(w, `{"from":"upstream","error":"their own words"}`)
	})
	closed := httptest.NewServer(stdhttp.HandlerFunc(func(stdhttp.ResponseWriter, *stdhttp.Request) {}))
	closedURL := closed.URL
	closed.Close()
	// what a URL may carry that a message must never show: its user information, query and fragment
	withSecrets := strings.Replace(closedURL, "://", "://user:hunter2@", 1) + "/x?api_key=SECRET123#frag"

	null := value.Value{K: value.Nil}
	for _, c := range []struct {
		label   string
		result  value.Value
		ok      bool
		status  value.Value
		code    value.Value
		error   value.Value
		message string // a part of error when error is not fixed
	}{
		{"HTTP 200", invoke(describe("get", server.URL+"/200"), "send"), true, value.New(200), null, null, ""},
		{"HTTP 201", invoke(describe("post", server.URL+"/201"), "send", map[string]any{"a": 1}), true, value.New(201), null, null, ""},
		{"HTTP 404", invoke(describe("get", server.URL+"/404"), "send"), false, value.New(404), null, value.New("Not Found"), ""},
		{"HTTP 429", invoke(describe("get", server.URL+"/429"), "send"), false, value.New(429), null, value.New("Too Many Requests"), ""},
		{"HTTP 503 after the attempts", invoke(invoke(describe("get", server.URL+"/503"), "retry", 1), "send"),
			false, value.New(503), null, value.New("Service Unavailable"), ""},
		{"no answer", invoke(describe("get", withSecrets), "send"), false, null, value.New(value.NetworkCode), null, "/x\""},
		{"past its time", invoke(invoke(describe("get", server.URL+"/slow"), "timeout", "50ms"), "send"),
			false, null, value.New(value.TimeoutCode), value.New("Request timed out"), ""},
		{"a body past 1kb", invoke(invoke(describe("get", server.URL+"/size?n=1025"), "maxBytes", "1kb"), "send"),
			false, value.New(200), value.New(value.TooLargeCode), null, "1kb"},
		{"a body past 10 MiB by default", invoke(describe("get", server.URL+fmt.Sprintf("/size?n=%d", defaultMaxBytes+1)), "send"),
			false, value.New(200), value.New(value.TooLargeCode), null, "10mb"},
	} {
		got := readOutcome(c.result)
		if got.ok != c.ok || !same(got.status, c.status) || !same(got.code, c.code) {
			t.Errorf("%s: %s, want ok %v status %s code %s", c.label, got, c.ok, written(c.status), written(c.code))
			continue
		}
		if c.error.K != value.Nil || c.message == "" {
			if !same(got.error, c.error) {
				t.Errorf("%s: error %s, want %s", c.label, written(got.error), written(c.error))
			}
		} else if !strings.Contains(got.error.Text(), c.message) {
			t.Errorf("%s: error %s does not say %q", c.label, written(got.error), c.message)
		}
		for _, secret := range []string{"SECRET123", "hunter2", "user", "frag", "their own words"} {
			if strings.Contains(got.error.Text(), secret) {
				t.Errorf("%s: error %q shows %q", c.label, got.error.Text(), secret)
			}
		}
		// an answer has headers, even a 404 or a body past its cap; no answer has none
		if headers := c.result.Get("headers"); (c.status.K == value.Nil) != (headers.K == value.Nil) {
			t.Errorf("%s: headers = %s with status %s", c.label, headers.K, written(c.status))
		}
	}

	// the cap is the cap: exactly 1kb is read
	if exact := invoke(invoke(describe("get", server.URL+"/size?n=1024"), "maxBytes", 1024), "send"); !exact.Get("ok").Truthy() ||
		len(invoke(exact, "bytes").Bytes()) != 1024 {
		t.Fatalf("a body of exactly .maxBytes(1024) = %s", readOutcome(exact))
	}
}

// The SSRF guard is on for every absolute URL (ALLOW_LOCAL no longer turns it off, 22/10/2026): loopback,
// private, link-local, metadata, CGNAT, 0.0.0.0/8, multicast and broadcast end in DENIED, with no
// request sent.
func TestGuardRefusesPrivateSpace(t *testing.T) {
	server, hits := counting(t, func(stdhttp.ResponseWriter, *stdhttp.Request) {})
	result := invoke(describe("get", server.URL), "send") // loopback, not opened
	if got := readOutcome(result); got.ok || got.status.K != value.Nil || got.code.Text() != value.DeniedCode ||
		!strings.Contains(got.error.Text(), "SSRF") {
		t.Fatalf("a loopback server = %s, want DENIED", got)
	}
	if n := atomic.LoadInt32(hits); n != 0 {
		t.Fatalf("the guard let %d requests through", n)
	}
	result = invoke(describe("get", "http://169.254.169.254/latest/meta-data/"), "send")
	if got := readOutcome(result); got.code.Text() != value.DeniedCode {
		t.Fatalf("the metadata address = %s, want DENIED", got)
	}

	for _, address := range []string{
		"127.0.0.1", "127.8.9.10", "::1", "10.0.0.1", "172.16.0.1", "192.168.1.1", "169.254.169.254",
		"fd00:ec2::254", "fe80::1", "100.100.100.200", "100.64.0.1", "0.0.0.0", "0.1.2.3", "224.0.0.1",
		"ff02::1", "255.255.255.255", "::ffff:127.0.0.1", "::ffff:10.0.0.1", "64:ff9b::a9fe:a9fe", "::",
	} {
		if err := guardDial("tcp", net.JoinHostPort(address, "80"), nil); err == nil {
			t.Errorf("the guard let %s through", address)
		}
	}
	for _, address := range []string{"93.184.216.34", "8.8.8.8", "100.128.0.1", "2606:4700::1111", "64:ff9b::808:808"} {
		if err := guardDial("tcp", net.JoinHostPort(address, "443"), nil); err != nil {
			t.Errorf("the guard refused %s: %v", address, err)
		}
	}
	// a test opens loopback only
	restore := loopback.Open()
	defer restore()
	if err := guardDial("tcp", "127.0.0.1:80", nil); err != nil {
		t.Errorf("an opened loopback is refused: %v", err)
	}
	for _, address := range []string{"10.0.0.1", "169.254.169.254", "100.100.100.200"} {
		if err := guardDial("tcp", net.JoinHostPort(address, "80"), nil); err == nil {
			t.Errorf("opening loopback let %s through", address)
		}
	}
}

// .retry(n) is n attempts after the first, only when there was no answer or a 5xx. GET, HEAD, OPTIONS,
// PUT and DELETE retry; POST and PATCH only with an Idempotency-Key header, otherwise .send() is an error
// and nothing is sent.
func TestRetryRules(t *testing.T) {
	openLoopback(t)
	var hits sync.Map
	server := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		key := r.Method + " " + r.URL.Path
		previous, _ := hits.LoadOrStore(key, new(int32))
		atomic.AddInt32(previous.(*int32), 1)
		status, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/"))
		w.WriteHeader(status)
	}))
	defer server.Close()
	count := func(method, path string) int32 {
		if n, ok := hits.Load(method + " " + path); ok {
			return atomic.LoadInt32(n.(*int32))
		}
		return 0
	}

	for _, c := range []struct {
		method, path string
		retry        int
		header       []any
		want         int32
	}{
		{"get", "/503", 2, nil, 3},
		{"get", "/404", 3, nil, 1},
		{"get", "/200", 3, nil, 1},
		{"head", "/500", 1, nil, 2},
		{"options", "/502", 1, nil, 2},
		{"put", "/503", 1, nil, 2},
		{"delete", "/504", 1, nil, 2},
		{"post", "/503", 1, []any{"Idempotency-Key", "order-1"}, 2},
		{"patch", "/500", 1, []any{"Idempotency-Key", "order-2"}, 2},
	} {
		request := invoke(describe(c.method, server.URL+c.path), "retry", c.retry)
		if c.header != nil {
			request = invoke(request, "header", c.header...)
		}
		result := invoke(request, "send")
		if result.K == value.Invalid {
			t.Errorf("%s %s: %s", c.method, c.path, result.Text())
			continue
		}
		if got := count(strings.ToUpper(c.method), c.path); got != c.want {
			t.Errorf("%s %s .retry(%d): %d attempts, want %d", c.method, c.path, c.retry, got, c.want)
		}
	}

	for _, method := range []string{"post", "patch"} {
		path := "/599"
		result := invoke(invoke(describe(method, server.URL+path), "retry", 1), "send", map[string]any{"a": 1})
		wantFailure(t, method+" .retry(1) without a key", result, value.InvalidArgumentCode,
			strings.ToUpper(method)+" thử lại có thể ghi hai lần", "Idempotency-Key")
		if got := count(strings.ToUpper(method), path); got != 0 {
			t.Errorf("%s .retry(1) without a key sent %d requests", method, got)
		}
	}
	// control: without .retry() a POST needs no key
	if result := invoke(describe("post", server.URL+"/201"), "send", "x"); result.K == value.Invalid {
		t.Fatalf("a POST without .retry() = %s", result.Text())
	}
}

// .retry(n) also retries when there was no answer: a connection closed before one (NETWORK), an attempt
// past its .timeout() (TIMEOUT). The time limit is each attempt's, not the run's (mục 6):
// .timeout("50ms").retry(1) against an upstream that takes 300 ms is two attempts. A body past
// .maxBytes() and an address the guard refuses are definite: one attempt. Without .timeout() an attempt
// has 10 seconds.
func TestRetryWithoutAnAnswer(t *testing.T) {
	openLoopback(t)
	// a server per case, so no attempt goes on a kept connection, which Go's transport may retry itself
	dropped, droppedHits := counting(t, func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		if connection, _, err := w.(stdhttp.Hijacker).Hijack(); err == nil {
			_ = connection.Close()
		}
	})
	slow, slowHits := counting(t, func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(300 * time.Millisecond):
		}
	})
	large, largeHits := counting(t, func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		_, _ = io.WriteString(w, strings.Repeat("x", 2048))
	})

	for _, c := range []struct {
		label  string
		result value.Value
		hits   *int32
		code   string
		want   int32
	}{
		{"a connection closed before an answer, .retry(2)", invoke(invoke(describe("get", dropped.URL), "retry", 2), "send"),
			droppedHits, value.NetworkCode, 3},
		{`an upstream of 300 ms, .timeout("50ms").retry(1)`,
			invoke(invoke(invoke(describe("get", slow.URL), "timeout", "50ms"), "retry", 1), "send"), slowHits, value.TimeoutCode, 2},
		{`a body past .maxBytes("1kb"), .retry(2)`,
			invoke(invoke(invoke(describe("get", large.URL), "maxBytes", "1kb"), "retry", 2), "send"), largeHits, value.TooLargeCode, 1},
	} {
		if got := readOutcome(c.result); got.ok || got.code.Text() != c.code {
			t.Errorf("%s: %s, want code %s", c.label, got, c.code)
		}
		if n := atomic.LoadInt32(c.hits); n != c.want {
			t.Errorf("%s: %d attempts, want %d", c.label, n, c.want)
		}
	}
	// the guard refuses every attempt alike, and a body past the cap is the same body again
	for label, o := range map[string]Outcome{
		"DENIED":             {Code: value.DeniedCode, Message: "SSRF prevention"},
		"TOO_LARGE of a 503": {Status: 503, Code: value.TooLargeCode},
	} {
		if retryable(o) {
			t.Errorf("%s is retried, want it definite", label)
		}
	}
	request, _ := describe("get", "https://api.example.test").V.(*Request)
	if request == nil || request.timeout != 10*time.Second {
		t.Errorf("a request without .timeout() = %+v, want 10s per attempt", request)
	}
}

// The result is the last attempt's (mục 6): an upstream that answers 503 twice and then 200 gives ok, 200
// and the third body, after three of the four attempts .retry(3) allows.
func TestRetryGivesTheLastAttempt(t *testing.T) {
	openLoopback(t)
	var attempts int32
	server, hits := counting(t, func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		n := atomic.AddInt32(&attempts, 1)
		if n < 3 {
			w.WriteHeader(stdhttp.StatusServiceUnavailable)
		}
		_, _ = fmt.Fprintf(w, `{"attempt":%d}`, n)
	})
	result := invoke(invoke(describe("get", server.URL), "retry", 3), "send")
	if got := readOutcome(result); !got.ok || got.status.Int() != 200 || got.code.K != value.Nil || got.error.K != value.Nil {
		t.Fatalf("503, 503, 200 = %s, want the last attempt's { ok: true, status: 200, code: null, error: null }", got)
	}
	if n := atomic.LoadInt32(hits); n != 3 {
		t.Errorf("503, 503, 200 with .retry(3): %d attempts, want 3", n)
	}
	if got := invoke(result, "json").Get("attempt"); got.Int() != 3 {
		t.Errorf("json().attempt = %s, want 3 (the last body)", got.Text())
	}
}

// A result's fields are properties (mục 8): ok, status, code, error, headers, cached and stale read as data,
// and calling one is NOT_A_FUNCTION, as calling an object's key is; until 22/10/2026 upstream.error() gave
// the sentence. json(), text() and bytes() are the functions to call.
func TestResultFieldsAreProperties(t *testing.T) {
	result := value.New(&Result{outcome: Outcome{
		Status: 404, Header: stdhttp.Header{"Content-Type": {"text/plain"}}, Body: []byte("gone"), Message: "Not Found",
	}})
	for name, want := range map[string]string{"ok": "false", "status": "404", "error": "Not Found", "cached": "false", "stale": "false"} {
		if got := result.Get(name); got.Text() != want {
			t.Errorf("result.%s = %s, want %s", name, got.Text(), want)
		}
	}
	if got := result.Get("code"); got.K != value.Nil {
		t.Errorf("result.code of an answer = %s, want null", got.Text())
	}
	if got := result.Get("headers").Get("content-type"); got.Text() != "text/plain" {
		t.Errorf(`result.headers["content-type"] = %s`, got.Text())
	}
	for _, name := range []string{"ok", "status", "code", "error", "headers", "cached", "stale"} {
		wantFailure(t, "result."+name+"()", result.Invoke(name), value.NotAFunctionCode, name+" không phải hàm")
	}
	if got := result.Invoke("text"); got.Text() != "gone" {
		t.Errorf("result.text() = %s, want gone", got.Text())
	}
}

// json(), text() and bytes() are functions: read, each is the function and reads nothing. json() of a
// body that is not JSON, or of an empty one, is INVALID_INPUT; reading the body of a result without one
// fails with the result's own code, so safe(() => upstream.json()) reads NETWORK when the network is down.
func TestBodyReadersAreCalled(t *testing.T) {
	openLoopback(t)
	server, _ := counting(t, func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		switch r.URL.Path {
		case "/empty":
			w.WriteHeader(stdhttp.StatusNoContent)
		case "/large":
			_, _ = io.WriteString(w, strings.Repeat("x", 2048))
		default:
			_, _ = io.WriteString(w, "not json")
		}
	})

	result := invoke(describe("get", server.URL+"/text"), "send")
	for _, name := range []string{"json", "text", "bytes"} {
		if read := result.Get(name); read.K != value.Func {
			t.Errorf(".%s read without a call = %s, want the function", name, read.K)
		}
	}
	wantFailure(t, "json() of text", invoke(result, "json"), value.InvalidInputCode, "không phải JSON")
	if text := invoke(result, "text"); text.Text() != "not json" {
		t.Errorf("text() = %s", text.Text())
	}
	wantFailure(t, "json() of a 204", invoke(invoke(describe("get", server.URL+"/empty"), "send"), "json"),
		value.InvalidInputCode, "rỗng", "204")
	wantFailure(t, "json() of a HEAD", invoke(invoke(describe("head", server.URL+"/text"), "send"), "json"),
		value.InvalidInputCode, "rỗng")

	closed := httptest.NewServer(stdhttp.HandlerFunc(func(stdhttp.ResponseWriter, *stdhttp.Request) {}))
	closed.Close()
	wantFailure(t, "json() without an answer", invoke(invoke(describe("get", closed.URL), "send"), "json"),
		value.NetworkCode, "không có thân", "ok")
	wantFailure(t, "text() past maxBytes", invoke(invoke(invoke(describe("get", server.URL+"/large"), "maxBytes", "1kb"), "send"), "text"),
		value.TooLargeCode, "không có thân")
}

// A self-call ("/path") goes to this host's port on 127.0.0.1, past the guard, with the site's domain as
// its Host, so it reaches the site that made it (until 22/10/2026 it carried 127.0.0.1 and reached
// whichever site answers that). A redirect on the same port keeps the Host; one that leaves is the answer.
func TestSelfCallCarriesTheSitesHost(t *testing.T) {
	var hosts sync.Map
	server := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		hosts.Store(r.URL.Path, r.Host)
		switch r.URL.Path {
		case "/moved":
			stdhttp.Redirect(w, r, "/echo", stdhttp.StatusFound)
		case "/away":
			stdhttp.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", stdhttp.StatusFound)
		default:
			_, _ = io.WriteString(w, "self:"+r.URL.RawQuery)
		}
	}))
	defer server.Close()
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	previous := GetServerPort
	GetServerPort = func() int { n, _ := strconv.Atoi(port); return n }
	defer func() { GetServerPort = previous }()

	module := value.New(NewModule(nil, nil, "shop.example"))
	result := invoke(invoke(invoke(module, "get", "/echo"), "query", map[string]any{"n": 1}), "send")
	if got := readOutcome(result); !got.ok || invoke(result, "text").Text() != "self:n=1" {
		t.Fatalf("a self-call = %s %s", got, invoke(result, "text").Text())
	}
	if host, _ := hosts.Load("/echo"); host != "shop.example" {
		t.Fatalf("the self-call's Host = %v, want the site's domain", host)
	}

	hosts.Delete("/echo")
	if moved := invoke(invoke(module, "get", "/moved"), "send"); !moved.Get("ok").Truthy() {
		t.Fatalf("a redirect on the same port = %s", readOutcome(moved))
	}
	if host, _ := hosts.Load("/echo"); host != "shop.example" {
		t.Fatalf("after a redirect on the same port the Host = %v, want the site's domain", host)
	}
	away := invoke(invoke(module, "get", "/away"), "send")
	if got := readOutcome(away); got.ok || got.status.Int() != stdhttp.StatusFound {
		t.Fatalf("a redirect off the host = %s, want the 302 itself", got)
	}
}

// The ways that are gone are errors naming the new spelling: modifiers before the method, a body as the
// method's second argument, a number of milliseconds for .timeout(), the result's body, base64,
// contentType, data and message, and then/catch on a request.
func TestOldSpellingsNameTheNewOnes(t *testing.T) {
	module := value.New(NewModule(nil, nil, ""))
	for _, name := range []string{"cache", "persist", "retry", "timeout", "header", "maxBytes", "query"} {
		wantFailure(t, "http."+name, module.Get(name), value.InvalidArgumentCode,
			"http."+name+"(…) đã bỏ", "http.get(url)."+name+"(…).send()")
	}
	wantFailure(t, "http.send", module.Get("send"), value.InvalidArgumentCode, "http.get(url).send()")
	wantFailure(t, "http.fetch", module.Get("fetch"), value.InvalidArgumentCode, "http.get(url).send()")
	wantFailure(t, "http.post(url, body)", invoke(module, "post", "https://api.example.test", map[string]any{"a": 1}),
		value.InvalidArgumentCode, "thân đi trong .send(data)", "http.post(url).send(data)")
	wantFailure(t, "http.get(url, options)", invoke(module, "get", "https://api.example.test", map[string]any{"page": 1}),
		value.InvalidArgumentCode, ".query(")
	wantFailure(t, "http.get()", invoke(module, "get"), value.InvalidArgumentCode, "cần một URL")
	wantFailure(t, "http.get(not a URL)", invoke(module, "get", "ftp://user:pw@files.example.test/x?k=SECRET"),
		value.InvalidArgumentCode, `"ftp://files.example.test/x"`)

	request := invoke(module, "get", "https://api.example.test")
	wantFailure(t, ".timeout(5000)", invoke(request, "timeout", 5000), value.InvalidArgumentCode, `.timeout("5s")`, "số mili giây đã bỏ")
	wantFailure(t, ".timeout(\"soon\")", invoke(request, "timeout", "soon"), value.InvalidArgumentCode, `"500ms"`)
	wantFailure(t, ".retry(-1)", invoke(request, "retry", -1), value.InvalidArgumentCode, ".retry(3)")
	wantFailure(t, ".maxBytes(\"huge\")", invoke(request, "maxBytes", "huge"), value.InvalidArgumentCode, `"50mb"`)
	wantFailure(t, ".maxBytes(\"65mb\")", invoke(request, "maxBytes", "65mb"), value.InvalidArgumentCode, "trần của host, 64mb")
	for _, name := range []string{"then", "catch"} {
		wantFailure(t, "request."+name, request.Get(name), value.InvalidArgumentCode, ".send() là chỗ duy nhất gửi request")
	}
	// a result read on a request never sent (it was sent then): each name says how the result of .send()
	// spells it today, a field to read, a reader to call, or what replaced a field that is gone
	for name, says := range map[string]string{
		"status": "rồi đọc upstream.status", "json": "rồi đọc upstream.json()", "text": "rồi đọc upstream.text()",
		"body": "rồi đọc upstream.bytes(), upstream.text() hay upstream.json()", "data": "rồi đọc upstream.json()",
		"base64": "rồi đọc upstream.bytes()", "contentType": `rồi đọc upstream.headers["content-type"]`,
		"message": "rồi đọc upstream.error",
	} {
		wantFailure(t, "unsent ."+name, request.Get(name), value.InvalidArgumentCode,
			"http.get(…) chưa gửi nên chưa có "+name, "const upstream = http.get(url).send()", says)
	}
	unsent, isUnsent := UnsentFailure(request)
	if !isUnsent {
		t.Fatalf("UnsentFailure of a request = not one")
	}
	wantFailure(t, "a request returned unsent", unsent, value.InvalidArgumentCode,
		"trả về http.get(…) chưa gửi", "const upstream = http.get(url).send()")
	if _, isUnsent := UnsentFailure(value.New(&Result{})); isUnsent {
		t.Errorf("UnsentFailure of a result = one, want only a request")
	}

	result := value.New(&Result{outcome: Outcome{Status: 200, Header: stdhttp.Header{"Content-Type": {"text/plain"}}, Body: []byte("x")}})
	for name, says := range map[string]string{
		"body": ".bytes()", "data": ".json()", "base64": ".bytes()", "contentType": `.headers["content-type"]`, "message": ".error",
	} {
		wantFailure(t, "result."+name, result.Get(name), value.InvalidArgumentCode, says)
	}
	// control: what the result has reads, and a name it has nothing for is null
	if got := result.Get("headers").Get("content-type"); got.Text() != "text/plain" {
		t.Errorf(`headers["content-type"] = %s`, got.Text())
	}
	if got := result.Get("nothing"); got.K != value.Nil {
		t.Errorf("result.nothing = %s, want null", got.K)
	}
	// a destructured result reads its fields
	if got := result.Get("error"); got.K != value.Nil {
		t.Errorf("error of an ok result = %s, want null", got.Text())
	}
}
