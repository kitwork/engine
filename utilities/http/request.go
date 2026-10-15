package http

import (
	"time"

	"github.com/kitwork/engine/value"
)

// Request is a deferred, single-fire HTTP plan produced by http.get(url) / http.post(url, body).
// Builder modifiers may appear before the verb and request modifiers may appear after it. COMMIT
// executes the normalized plan after the expression ends; observing a response field also executes
// it. The response is memoized, so every plan reaches the network at most once.
//
// A request that got no answer (no connection, its time limit, the SSRF guard, a body past its
// limit) is a failure of the program (14/10/2026), as fetch rejects in JavaScript: the COMMIT or the
// read that fired it fails with NETWORK, TIMEOUT, DENIED, INVALID_INPUT…, and safe(() => fetch(url))
// reads it. Until then it was a response of status 0 carrying .error, which a program had to know to
// check. Any answer, 4xx and 5xx included, is a response. .then() and .catch() are gone with it: one
// way to read a failure.
type Request struct {
	h       *HTTP // the cloned, configured client (owns cache/persist/timeout/headers/retry)
	method  string
	url     string
	reqBody value.Value

	fired     bool
	committed bool
	res       Response
}

func newRequest(h *HTTP, method, url string, body value.Value) value.Value {
	return value.New(&Request{h: h, method: method, url: url, reqBody: body})
}

// ---- request modifiers: HTTP exposes the same configuration before .get()/.post() ----

func (r *Request) Retry(n int) *Request              { r.h = r.h.Retry(n); return r }
func (r *Request) Timeout(ms int) *Request           { r.h = r.h.Timeout(ms); return r }
func (r *Request) MaxBytes(n int) *Request           { r.h = r.h.MaxBytes(n); return r }
func (r *Request) Header(k, v string) *Request       { r.h = r.h.Header(k, v); return r }
func (r *Request) Cache(a ...value.Value) *Request   { r.h = r.h.Cache(a...); return r }
func (r *Request) Persist(a ...value.Value) *Request { r.h = r.h.Persist(a...); return r }

// Commit executes the normalized plan; a request that got no answer fails the COMMIT.
func (r *Request) Commit() value.CommitResult {
	if r.committed {
		return value.CommitResult{}
	}
	r.committed = true
	if failure, failed := r.failure(); failed {
		return value.CommitResult{Failure: failure}
	}
	return value.CommitResult{}
}

// failure fires the request and reports the failure it ended in when it got no answer (Status 0).
func (r *Request) failure() (value.Value, bool) {
	r.ensure()
	if r.res.Status != 0 {
		return value.Value{}, false
	}
	code := r.res.Code
	if code == "" {
		code = value.NetworkCode
	}
	message := r.res.Error
	if message == "" {
		message = "http: no answer"
	}
	// the message as the client said it, once ("Get \"…\": dial tcp …", "http: response body exceeds
	// max bytes"): until 14/10/2026 it was what .error read, and sites compare it
	return value.InvalidFailure(code, message), true
}

// ensure fires the request once, applying the retry policy, and memoises the result.
func (r *Request) ensure() {
	if r.fired {
		return
	}
	r.fired = true

	attempts := r.h.retry + 1
	if attempts < 1 {
		attempts = 1
	}
	// Retry is for transient failures on IDEMPOTENT reads only — a POST retry would double-write.
	if r.method != "GET" && r.method != "HEAD" {
		attempts = 1
	}

	for i := 0; i < attempts; i++ {
		v := r.h.do(r.method, r.url, r.reqBody)
		resp, _ := v.V.(Response)
		r.res = resp
		if !isTransient(resp) {
			break // 2xx/3xx/4xx is a definite answer — retrying a 404 just wastes time
		}
		if i < attempts-1 {
			time.Sleep(backoff(i))
		}
	}
}

// ---- observation triggers ----
//
// Each fires the request, and is the request's failure when it got no answer. There is no .error: a
// request that reached a field got an answer.

func (r *Request) Status() value.Value {
	return r.observe(func() value.Value { return value.New(r.res.Status) })
}
func (r *Request) Ok() value.Value {
	return r.observe(func() value.Value { return value.New(r.res.Ok()) })
}
func (r *Request) Body() value.Value { return r.observe(func() value.Value { return r.res.Body }) }
func (r *Request) JSON() value.Value { return r.observe(func() value.Value { return r.res.JSON() }) }
func (r *Request) Text() value.Value {
	return r.observe(func() value.Value { return value.New(r.res.Text()) })
}
func (r *Request) Base64() value.Value {
	return r.observe(func() value.Value { return value.New(r.res.Base64()) })
}
func (r *Request) ContentType() value.Value {
	return r.observe(func() value.Value { return value.New(r.res.ContentType) })
}
func (r *Request) Cached() value.Value {
	return r.observe(func() value.Value { return value.New(r.res.Cached) })
}
func (r *Request) Stale() value.Value {
	return r.observe(func() value.Value { return value.New(r.res.Stale) })
}

// observe is read() of a fired request, or the request's failure.
func (r *Request) observe(read func() value.Value) value.Value {
	if failure, failed := r.failure(); failed {
		return failure
	}
	return read()
}

// Fire is the Go-side accessor (e.g. router.proxy()): ensure + hand back the concrete Response.
func (r *Request) Fire() Response { r.ensure(); return r.res }

// isTransient reports a failure worth retrying: a network error (Status 0) or a 5xx. A 4xx is a
// definite answer and is never retried.
func isTransient(resp Response) bool {
	return resp.Status == 0 || resp.Status >= 500
}

// backoff grows 100ms, 200ms, 400ms, … between attempts.
func backoff(attempt int) time.Duration {
	return time.Duration(100<<uint(attempt)) * time.Millisecond
}
