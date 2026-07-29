package http

import (
	"time"

	"github.com/kitwork/engine/value"
)

// Request is a deferred, single-fire HTTP plan produced by http.get(url) / http.post(url, body).
// Builder modifiers may appear before the verb and request modifiers may appear after it. COMMIT
// executes the normalized plan after the expression ends; observing a response field also executes
// it. The response is memoized, so every plan reaches the network at most once.
type Request struct {
	h       *HTTP // the cloned, configured client (owns cache/persist/timeout/headers/retry)
	method  string
	url     string
	reqBody value.Value

	onOk  *value.Lambda // .then() — final success (a first attempt or any retry succeeded)
	onErr *value.Lambda // .catch() — final failure (every attempt, retries included, failed)

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
func (r *Request) Header(k, v string) *Request       { r.h = r.h.Header(k, v); return r }
func (r *Request) Cache(a ...value.Value) *Request   { r.h = r.h.Cache(a...); return r }
func (r *Request) Persist(a ...value.Value) *Request { r.h = r.h.Persist(a...); return r }

// Then / Catch record handlers that run when the expression commits. They may be
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

// Commit executes the normalized plan and selects an optional continuation for the current VM.
func (r *Request) Commit() value.CommitResult {
	if r.committed {
		return value.CommitResult{}
	}
	r.committed = true
	r.ensure()
	if r.res.Ok() {
		if r.onOk != nil {
			return value.CommitResult{Handler: r.onOk, Argument: value.New(r)}
		}
	} else if r.onErr != nil {
		return value.CommitResult{Handler: r.onErr, Argument: value.New(r)}
	}
	return value.CommitResult{}
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
