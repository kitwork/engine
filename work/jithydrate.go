package work

import (
	"hash/fnv"
	"net/http"
	"strconv"
	"sync"

	hydrate "github.com/kitwork/engine/jit/hydrate"
)

// serveHydrateIf serves the hydrate client interpreter — the "frontend bytecode VM" runtime that
// walks the IR compiled by jit/hydrate — at hydrate.RuntimePath. Like jitfonts, it is a built-in,
// always-on route checked before tenant routing: the <script src> that render injects points here,
// and the bytes are identical for every tenant (embedded at build), so one browser-cached file
// serves the whole host. A cheap no-op (returns false) for any other path.
func serveHydrateIf(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path != hydrate.RuntimePath {
		return false
	}
	etag := hydrateETag()
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "public, max-age=300")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return true
	}
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	_, _ = w.Write([]byte(hydrate.Runtime()))
	return true
}

// hydrateETag is a content hash of the embedded runtime, computed once. The URL is fixed but the
// bytes change per build, so we pair a short max-age with revalidation (mirrors serveJitjsJS) rather
// than an immutable cache that would pin a stale runtime.
var (
	hydrateETagOnce sync.Once
	hydrateETagVal  string
)

func hydrateETag() string {
	hydrateETagOnce.Do(func() {
		h := fnv.New64a()
		_, _ = h.Write([]byte(hydrate.Runtime()))
		hydrateETagVal = `"` + strconv.FormatUint(h.Sum64(), 16) + `"`
	})
	return hydrateETagVal
}
