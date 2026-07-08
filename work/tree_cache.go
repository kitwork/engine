package work

// Response-cache glue: this is the HTTP-aware layer that wires the pure cache/persist/ratelimit
// packages into the tree lifecycle. The packages store opaque bytes by key; here we extract the
// key from the request, serialize the finalized Response, and replay it on a hit.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"

	"github.com/kitwork/engine/helpers/cache"
	"github.com/kitwork/engine/helpers/persist"
)

// cacheKey identifies a cached response by method + path + query.
func cacheKey(r *http.Request) string {
	if r.URL.RawQuery != "" {
		return r.Method + " " + r.URL.Path + "?" + r.URL.RawQuery
	}
	return r.Method + " " + r.URL.Path
}

// hashKey turns a key into a filesystem-safe name for the disk (.persist) store.
func hashKey(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// responseBytes serializes a finalized Response to (body, content-type, status) for caching. Only
// value-bearing responses cache; streams / redirects / files return ok=false.
func responseBytes(resp *Response) (body []byte, ct string, status int, ok bool) {
	status = resp.Code()
	if status == 0 {
		status = 200
	}
	switch resp.Kind() {
	case "html", "":
		return []byte(resp.Data().String()), "text/html; charset=utf-8", status, true
	case "text":
		return []byte(resp.Data().String()), "text/plain; charset=utf-8", status, true
	case "css":
		return []byte(resp.Data().String()), "text/css; charset=utf-8", status, true
	case "svg":
		return []byte(resp.Data().String()), "image/svg+xml; charset=utf-8", status, true
	case "json":
		b, err := json.Marshal(resp.Data())
		if err != nil {
			return nil, "", 0, false
		}
		return b, "application/json; charset=utf-8", status, true
	case "image":
		return resp.Data().Bytes(), "image/png", status, true
	case "bytes":
		return resp.Data().Bytes(), "application/octet-stream", status, true
	default:
		return nil, "", 0, false // sse / redirect / file / directory / error — never cached
	}
}

// serveCached writes a cache/persist hit straight to the wire — no VM, no render.
func serveCached(w http.ResponseWriter, body []byte, ct string, status int) {
	if status == 0 {
		status = 200
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("X-Kitwork-Cache", "hit")
	w.WriteHeader(status)
	w.Write(body)
}

// cachedResponse returns a hit for the method's cache/persist config, if any.
func (t *Tenant) cachedResponse(method *FolderMethod, key string) (body []byte, ct string, status int, ok bool) {
	if method.cacheExpiry != nil {
		if e, hit := t.respCache.Get(key); hit {
			return e.Body, e.ContentType, e.Status, true
		}
	}
	if method.persistExpiry != nil {
		if rec, hit := t.persistStore.Get(hashKey(key)); hit {
			return rec.Body, rec.ContentType, rec.Status, true
		}
	}
	return nil, "", 0, false
}

// saveResponse stores a finalized 2xx response in RAM (.cache) and/or on disk (.persist). The
// expiry resolvers are evaluated NOW, so a boundary spec ("nextday 03:00") pins the expiry to the
// next wall-clock boundary rather than a rolling window.
func (t *Tenant) saveResponse(method *FolderMethod, key string, resp *Response) {
	body, ct, status, ok := responseBytes(resp)
	if !ok || status < 200 || status >= 300 {
		return
	}
	now := time.Now()
	if method.cacheExpiry != nil {
		t.respCache.Set(key, cache.Entry{Body: body, ContentType: ct, Status: status}, method.cacheExpiry(now))
	}
	if method.persistExpiry != nil {
		_ = t.persistStore.Set(hashKey(key), persist.Record{Body: body, ContentType: ct, Status: status}, method.persistExpiry(now))
	}
}
