package work

import (
	"net/http"
	"strings"

	fonts "github.com/kitwork/engine/jit/fonts"
)

// serveFontIf serves a vendored woff2 from the embedded jit/fonts FS when the path is under
// fonts.RoutePrefix, returning true once it has handled the request. Always-on (the injected
// @font-face `src` + preload links point here), no VM, no tenant lookup. The bytes are pinned at
// build time, so they are hard-cached + immutable; crossorigin so the preload/`format('woff2')`
// fetch (which fonts always make anonymously) is accepted.
func serveFontIf(w http.ResponseWriter, r *http.Request) bool {
	if !strings.HasPrefix(r.URL.Path, fonts.RoutePrefix) {
		return false
	}
	rel := strings.TrimPrefix(r.URL.Path, fonts.RoutePrefix) // e.g. "outfit/400-latin.woff2"
	if rel == "" || strings.Contains(rel, "..") || !strings.HasSuffix(rel, ".woff2") {
		http.NotFound(w, r)
		return true
	}
	data, err := fonts.FS.ReadFile("families/" + rel)
	if err != nil {
		http.NotFound(w, r)
		return true
	}
	w.Header().Set("Content-Type", "font/woff2")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	_, _ = w.Write(data)
	return true
}
