package work

// Response-cache glue for tree routes. Cache packages store opaque HTTP records; this layer
// extracts and replays body, content type, status, and validator headers without entering the VM.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"

	"github.com/kitwork/engine/utilities/cache"
	"github.com/kitwork/engine/utilities/persist"
)

func cacheKey(r *http.Request) string {
	method := r.Method
	if method == http.MethodHead {
		method = http.MethodGet
	}
	if r.URL.RawQuery != "" {
		return method + " " + r.URL.Path + "?" + r.URL.RawQuery
	}
	return method + " " + r.URL.Path
}

func hashKey(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func responseBytes(resp *Response) (body []byte, contentType string, status int, headers map[string]string, ok bool) {
	status = resp.Code()
	if status == 0 {
		status = http.StatusOK
	}
	headers = resp.Headers()
	switch resp.Kind() {
	case "html", "":
		return []byte(resp.Data().String()), "text/html; charset=utf-8", status, headers, true
	case "text":
		return []byte(resp.Data().String()), "text/plain; charset=utf-8", status, headers, true
	case "css":
		return []byte(resp.Data().String()), "text/css; charset=utf-8", status, headers, true
	case "svg":
		return []byte(resp.Data().String()), "image/svg+xml; charset=utf-8", status, headers, true
	case "typed":
		return []byte(resp.Data().String()), resp.ContentType(), status, headers, true
	case "json":
		data, err := json.Marshal(resp.Data())
		if err != nil {
			return nil, "", 0, nil, false
		}
		return data, "application/json; charset=utf-8", status, headers, true
	case "image":
		return resp.Data().Bytes(), "image/png", status, headers, true
	case "bytes":
		return resp.Data().Bytes(), "application/octet-stream", status, headers, true
	default:
		return nil, "", 0, nil, false
	}
}

func serveCached(
	w http.ResponseWriter,
	request *http.Request,
	body []byte,
	contentType string,
	status int,
	headers map[string]string,
) {
	if status == 0 {
		status = http.StatusOK
	}
	w.Header().Set("Content-Type", contentType)
	for name, data := range headers {
		w.Header().Set(name, data)
	}
	w.Header().Set("X-Kitwork-Cache", "hit")
	if requestNotModified(request, w.Header()) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.WriteHeader(status)
	if request.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

func (t *Tenant) cachedResponse(
	method *FolderMethod,
	key string,
) (body []byte, contentType string, status int, headers map[string]string, ok bool) {
	if method.cacheExpiry != nil {
		if entry, hit := t.respCache.Get(key); hit {
			return entry.Body, entry.ContentType, entry.Status, entry.Headers, true
		}
	}
	if method.persistExpiry != nil {
		if record, hit := t.persistStore.Get(hashKey(key)); hit {
			return record.Body, record.ContentType, record.Status, record.Headers, true
		}
	}
	return nil, "", 0, nil, false
}

func (t *Tenant) saveResponse(method *FolderMethod, key string, response *Response) {
	body, contentType, status, headers, ok := responseBytes(response)
	if !ok || status < 200 || status >= 300 {
		return
	}
	now := time.Now()
	if method.cacheExpiry != nil {
		t.respCache.Set(key, cache.Entry{
			Body: body, ContentType: contentType, Status: status, Headers: headers,
		}, method.cacheExpiry(now))
	}
	if method.persistExpiry != nil {
		_ = t.persistStore.Set(hashKey(key), persist.Record{
			Body: body, ContentType: contentType, Status: status, Headers: headers,
		}, method.persistExpiry(now))
	}
}
