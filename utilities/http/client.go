package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	stdhttp "net/http"
	neturl "net/url"
	"strings"
	"time"

	"github.com/kitwork/engine/value"
)

type HTTP struct {
	timeout  time.Duration
	headers  map[string]string
	maxBytes int64

	// Outbound caching (see store.go): tiers injected per tenant; flags set by .cache()/.persist().
	cacheStore   ResponseStore
	persistStore ResponseStore
	cacheOn      bool
	persistOn    bool
	cacheTTL     time.Duration
	persistTTL   time.Duration
	retry        int // .retry(n): re-attempt transient failures on idempotent reads (see request.go)
}

func (h *HTTP) Timeout(ms int) *HTTP {
	next := h.clone()
	next.timeout = time.Duration(ms) * time.Millisecond
	return next
}

func (h *HTTP) Header(key, val string) *HTTP {
	next := h.clone()
	if next.headers == nil {
		next.headers = make(map[string]string)
	}
	next.headers[key] = val
	return next
}

// MaxBytes bounds the decoded response body. A zero value keeps the historical
// unbounded behavior for callers that have not opted into a limit.
func (h *HTTP) MaxBytes(n int) *HTTP {
	next := h.clone()
	if n < 0 {
		n = 0
	}
	const hardMax = 64 << 20
	if n > hardMax {
		n = hardMax
	}
	next.maxBytes = int64(n)
	return next
}

// Retry sets how many extra attempts a transient failure (network error / 5xx) gets on a GET/HEAD.
func (h *HTTP) Retry(n int) *HTTP {
	next := h.clone()
	next.retry = n
	return next
}

// clone copies the configured template so a fired Request never mutates the shared builder.
func (h *HTTP) clone() *HTTP {
	c := *h
	if h.headers != nil {
		c.headers = make(map[string]string, len(h.headers))
		for k, v := range h.headers {
			c.headers[k] = v
		}
	}
	return &c
}

// Get / Post create a deferred Request. Builder options are copied into the request, whose own
// modifiers may continue configuring the same plan after the verb. COMMIT or response observation
// executes the plan exactly once.
func (h *HTTP) Get(url string) value.Value {
	return newRequest(h.clone(), "GET", url, value.New(nil))
}

func (h *HTTP) Post(url string, body value.Value) value.Value {
	return newRequest(h.clone(), "POST", url, body)
}

func (h *HTTP) do(method, url string, body value.Value) value.Value {
	// Read-through caching: GET only. Check RAM first, then disk; on a live failure fall back to
	// an expired disk copy (stale-on-error) so a third-party outage never breaks the page.
	wantCache := h.cacheOn && h.cacheStore != nil
	wantPersist := h.persistOn && h.persistStore != nil
	key := ""
	if method == "GET" && (wantCache || wantPersist) {
		key = requestKey(url, h.headers)
		if wantCache {
			if snap, ok := h.cacheStore.Load(key); ok {
				return storedResponse(snap, false)
			}
		}
		if wantPersist {
			if snap, ok := h.persistStore.Load(key); ok {
				if wantCache { // promote a disk hit into RAM for the next reader
					h.cacheStore.Save(key, snap, h.cacheTTL)
				}
				return storedResponse(snap, false)
			}
		}
	}

	timeout := h.timeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}

	var bodyReader io.Reader
	if !body.IsBlank() {
		if body.K == value.String {
			bodyReader = strings.NewReader(body.String())
		} else {
			b, _ := json.Marshal(body.Interface())
			bodyReader = bytes.NewReader(b)
		}
	}

	serverPort := 0
	if GetServerPort != nil {
		serverPort = GetServerPort()
	}

	var isLocalRequest bool
	if strings.HasPrefix(url, "/") {
		isLocalRequest = true
		port := 8080
		if serverPort > 0 {
			port = serverPort
		}
		url = fmt.Sprintf("http://127.0.0.1:%d%s", port, url)
	}

	req, err := stdhttp.NewRequest(method, url, bodyReader)
	if err != nil {
		// a URL that is not one: the program's argument
		return value.New(Response{Status: 0, Error: shownError(err), Code: value.InvalidArgumentCode})
	}

	for k, v := range h.headers {
		req.Header.Set(k, v)
	}

	if method == "POST" && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}

	allowLocal := false
	if IsLocalAllowed != nil {
		allowLocal = IsLocalAllowed()
	}

	transport := sharedTransport
	if isLocalRequest || allowLocal {
		transport = localTransport
	}

	var client *stdhttp.Client
	if h.timeout > 0 {
		client = &stdhttp.Client{
			Transport: transport,
			Timeout:   h.timeout,
		}
	} else {
		if isLocalRequest || allowLocal {
			client = localClient
		} else {
			client = sharedClient
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		if snap, ok := staleFallback(key, wantPersist, h.persistStore); ok {
			return storedResponse(snap, true)
		}
		return value.New(Response{Status: 0, Error: shownError(err), Code: failureCode(err)})
	}
	defer resp.Body.Close()

	responseReader := io.Reader(resp.Body)
	if h.maxBytes > 0 {
		responseReader = io.LimitReader(resp.Body, h.maxBytes+1)
	}
	resBody, readErr := io.ReadAll(responseReader)
	if readErr != nil {
		return value.New(Response{Status: 0, Error: shownError(readErr), Code: failureCode(readErr)})
	}
	if h.maxBytes > 0 && int64(len(resBody)) > h.maxBytes {
		// an answer from outside past the limit the program set
		return value.New(Response{Status: 0, Error: "http: response body exceeds max bytes", Code: value.InvalidInputCode})
	}
	// Keep the upstream's media type: a cached/proxied copy must replay under the ORIGINAL type.
	// Fall back to sniffing the bytes when the server sent none (stdlib, no dependency).
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" && len(resBody) > 0 {
		contentType = stdhttp.DetectContentType(resBody)
	}

	if key != "" && resp.StatusCode >= 200 && resp.StatusCode < 300 {
		snap := Snapshot{Status: resp.StatusCode, Body: resBody, ContentType: contentType}
		if wantCache {
			h.cacheStore.Save(key, snap, h.cacheTTL)
		}
		if wantPersist {
			h.persistStore.Save(key, snap, h.persistTTL)
		}
	} else if resp.StatusCode >= 500 {
		if snap, ok := staleFallback(key, wantPersist, h.persistStore); ok {
			return storedResponse(snap, true)
		}
	}

	return value.New(Response{
		Status:      resp.StatusCode,
		Body:        value.New(resBody),
		ContentType: contentType,
	})
}

// failureCode is the code of a request that got no answer (14/10/2026): DENIED when the SSRF guard
// refused the address, EXECUTION_CANCELLED when the request was cancelled (nothing catches it),
// TIMEOUT past its time limit, NETWORK for any other.
func failureCode(err error) string {
	var denied *deniedAddress
	var timeout interface{ Timeout() bool }
	switch {
	case errors.As(err, &denied):
		return value.DeniedCode
	case errors.Is(err, context.Canceled):
		return value.CancelledCode
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &timeout) && timeout.Timeout():
		return value.TimeoutCode
	}
	return value.NetworkCode
}

// shownError is the text of a request's failure as the program reads it (error.message, which a site
// may hand a browser): Go names the URL in it, and a URL can carry a secret, so the URL loses its user
// information, query and fragment ("Get \"https://api.example.com/v1\": dial tcp …").
func shownError(err error) string {
	var failed *neturl.Error
	if errors.As(err, &failed) {
		shown := *failed
		shown.URL = shownURL(failed.URL)
		return shown.Error()
	}
	return err.Error()
}

// shownURL is raw without its user information, query and fragment. raw may not parse (the error of
// a URL that is not one), so it is cut as text.
func shownURL(raw string) string {
	if cut := strings.IndexAny(raw, "?#"); cut >= 0 {
		raw = raw[:cut]
	}
	if scheme := strings.Index(raw, "://"); scheme >= 0 {
		rest := raw[scheme+3:]
		authority := rest
		if slash := strings.IndexByte(rest, '/'); slash >= 0 {
			authority = rest[:slash]
		}
		if at := strings.LastIndexByte(authority, '@'); at >= 0 {
			raw = raw[:scheme+3] + rest[at+1:]
		}
	}
	return raw
}

// staleFallback returns an expired-but-present disk copy when the live request failed.
func staleFallback(key string, wantPersist bool, store ResponseStore) (Snapshot, bool) {
	if key == "" || !wantPersist {
		return Snapshot{}, false
	}
	return store.LoadStale(key)
}
