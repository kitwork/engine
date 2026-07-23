package http

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	stdhttp "net/http"
	"strings"
	"time"

	"github.com/kitwork/engine/value"
)

type HTTP struct {
	timeout time.Duration
	headers map[string]string

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
	h.timeout = time.Duration(ms) * time.Millisecond
	return h
}

func (h *HTTP) Header(key, val string) *HTTP {
	if h.headers == nil {
		h.headers = make(map[string]string)
	}
	h.headers[key] = val
	return h
}

// Retry sets how many extra attempts a transient failure (network error / 5xx) gets on a GET/HEAD.
func (h *HTTP) Retry(n int) *HTTP {
	h.retry = n
	return h
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

// Get / Post are now LAZY: they return a *Request (see request.go) that fires exactly once when its
// result is first read (.status/.json()/.body/…) or .send() is called. This is what lets the chain be
// written flat in any order — http.get(url).retry(3).cache("5m").  do() stays the eager engine used by
// fetch() and by Request.ensure().
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
		return value.New(Response{Status: 0, Error: err.Error()})
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
		return value.New(Response{Status: 0, Error: err.Error()})
	}
	defer resp.Body.Close()

	resBody, _ := io.ReadAll(resp.Body)
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

// staleFallback returns an expired-but-present disk copy when the live request failed.
func staleFallback(key string, wantPersist bool, store ResponseStore) (Snapshot, bool) {
	if key == "" || !wantPersist {
		return Snapshot{}, false
	}
	return store.LoadStale(key)
}
