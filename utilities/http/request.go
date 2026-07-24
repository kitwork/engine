package http

import (
	"time"

	"github.com/kitwork/engine/value"
)

// Request is a LAZY, single-fire HTTP request produced by http.get(url) / http.post(url, body). It
// collects modifiers in ANY order — http.get(url).retry(3).cache("5m") — and fires exactly ONCE, the
// first time its result is read (.status / .json() / .body / …) or .send() is called. Firing is
// memoised, so reading several fields runs the request only once.
//
// This is what makes the chain flat and order-free: because .get() no longer fires, every modifier
// after it is still just configuration. do() (client.go) remains the eager engine — Request.ensure()
// calls it, and so does fetch(). Kitwork has no promises, so this laziness is invisible: existing code
// (const res = http.cache().get(url); if (res.status ...)) fires on the very next line, unchanged.
type Request struct {
	h       *HTTP // the cloned, configured client (owns cache/persist/timeout/headers/retry)
	method  string
	url     string
	reqBody value.Value

	onOk  *value.Lambda // .then() — final success (a first attempt or any retry succeeded)
	onErr *value.Lambda // .catch() — final failure (every attempt, retries included, failed)

	fired bool
	res   Response
}

func newRequest(h *HTTP, method, url string, body value.Value) value.Value {
	return value.New(&Request{h: h, method: method, url: url, reqBody: body})
}

// ---- modifiers: return *Request so they chain in any order, BEFORE or AFTER .get() ----

func (r *Request) Retry(n int) *Request             { r.h.retry = n; return r }
func (r *Request) Timeout(ms int) *Request           { r.h.Timeout(ms); return r }
func (r *Request) Header(k, v string) *Request       { r.h.Header(k, v); return r }
func (r *Request) Cache(a ...value.Value) *Request   { r.h.Cache(a...); return r }
func (r *Request) Persist(a ...value.Value) *Request { r.h.Persist(a...); return r }

// Then / Catch record handlers that run when the statement ends (see FinalizeStatement). They may be
// placed ANYWHERE in the chain — before or after retry/cache — because nothing fires until the end.
func (r *Request) Then(cb value.Value) *Request {
	if l, ok := cb.V.(*value.Lambda); ok {
		r.onOk = l
	}
	return r
}
func (r *Request) Catch(cb value.Value) *Request {
	if l, ok := cb.V.(*value.Lambda); ok {
		r.onErr = l
	}
	return r
}

// FinalizeStatement (value.StatementFinalizer) fires the request once at the end of a bare statement,
// then hands the current VM the handler to run: .then() on final success, .catch() on final failure.
// A request with no handler still fires (fire-and-forget) and returns run == false.
func (r *Request) FinalizeStatement(soft bool) (*value.Lambda, value.Value, bool) {
	// Assigned/returned (soft) + no handler → a plain lazy request; leave it to fire on read.
	if soft && r.onOk == nil && r.onErr == nil {
		return nil, value.Value{}, false
	}
	r.ensure()
	if r.res.Ok() {
		if r.onOk != nil {
			return r.onOk, value.New(r), true
		}
	} else if r.onErr != nil {
		return r.onErr, value.New(r), true
	}
	return nil, value.Value{}, false
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

// ---- fire triggers: read a field / call .send() ----

func (r *Request) Status() int         { r.ensure(); return r.res.Status }
func (r *Request) Ok() bool            { r.ensure(); return r.res.Ok() }
func (r *Request) Body() value.Value   { r.ensure(); return r.res.Body }
func (r *Request) JSON() value.Value   { r.ensure(); return r.res.JSON() }
func (r *Request) Text() string        { r.ensure(); return r.res.Text() }
func (r *Request) Base64() string      { r.ensure(); return r.res.Base64() }
func (r *Request) ContentType() string { r.ensure(); return r.res.ContentType }
func (r *Request) Error() string       { r.ensure(); return r.res.Error }
func (r *Request) Cached() bool        { r.ensure(); return r.res.Cached }
func (r *Request) Stale() bool         { r.ensure(); return r.res.Stale }

// Send fires the request for its side effect and returns the Request (fire-and-forget that reads no
// field): http.post(webhook, body).send().
func (r *Request) Send() *Request { r.ensure(); return r }

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
