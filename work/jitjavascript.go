package work

import (
	"net/http"
	"strconv"
	"strings"

	kitjavascript "github.com/kitwork/engine/jit/javascript"
)

const kitJSAssetPrefix = "/kit.js/"

// serveKitJSAssetIf exposes the bounded site-lifetime content-addressed store.
// An exact retained hash is authoritative across generation handoffs, including
// when the current generation has disabled KitJS. A site that never enabled
// KitJS has an empty store. The legacy /kit.js route remains a separate exact
// path and cannot reach this store.
func (t *Tenant) serveKitJSAssetIf(w http.ResponseWriter, r *http.Request) bool {
	if r == nil || !strings.HasPrefix(r.URL.Path, kitJSAssetPrefix) {
		return false
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.NotFound(w, r)
		return true
	}
	filename := strings.TrimPrefix(r.URL.Path, kitJSAssetPrefix)
	if len(filename) != 64+len(".js") || !strings.HasSuffix(filename, ".js") {
		http.NotFound(w, r)
		return true
	}
	contentHash := strings.TrimSuffix(filename, ".js")
	if !kitjavascript.ValidContentHash(contentHash) {
		http.NotFound(w, r)
		return true
	}
	if t.siteRuntime == nil {
		http.NotFound(w, r)
		return true
	}
	asset, ok := t.siteRuntime.ContentAsset(contentHash)
	if !ok {
		http.NotFound(w, r)
		return true
	}

	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	etag := `"` + contentHash + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Length", strconv.Itoa(len(asset.Body)))
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return true
	}
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return true
	}
	_, _ = w.Write(asset.Body)
	return true
}
