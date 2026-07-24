package work

import (
	"hash/fnv"
	"net/http"
	"strconv"
	"sync"

	hydrate "github.com/kitwork/engine/jit/hydrate"
	"github.com/kitwork/engine/utilities/minifier"
)

// serveHydrateIf serves the hydrate client interpreter — the "frontend bytecode VM" runtime that
// walks the IR compiled by jit/hydrate — at hydrate.RuntimePath. Like jitfonts, it is a built-in,
// always-on route checked before tenant routing: the <script src> that render injects points here,
// and the bytes are identical for every tenant (embedded at build), so one browser-cached file
// serves the whole host. A cheap no-op (returns false) for any other path.
//
// The kernel ships MINIFIED in production — the embedded source is comment-heavy by design (it is
// the reference implementation), roughly halving on minify. Local dev (ALLOW_LOCAL) serves the
// readable source instead, mirroring how HTML minify is keyed off !AllowLocal, so view-source
// debugging stays pleasant. Each variant is minified/hashed once and cached for the process.
func serveHydrateIf(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path != hydrate.RuntimePath {
		return false
	}
	body, etag := hydrateAsset()
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "public, max-age=300")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return true
	}
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	_, _ = w.Write(body)
	return true
}

// The two serve variants, each computed once. The URL is fixed but the bytes change per build (and
// per variant), so the ETag is a content hash and we pair a short max-age with revalidation rather
// than an immutable cache that would pin a stale runtime. Distinct ETags mean a dev↔prod switch
// revalidates cleanly.
var (
	hydrateMinOnce sync.Once
	hydrateMin     []byte
	hydrateMinTag  string
	hydrateRawOnce sync.Once
	hydrateRaw     []byte
	hydrateRawTag  string
)

func hydrateAsset() ([]byte, string) {
	if AllowLocal {
		hydrateRawOnce.Do(func() {
			hydrateRaw = []byte(hydrate.Runtime())
			hydrateRawTag = contentTag(hydrateRaw)
		})
		return hydrateRaw, hydrateRawTag
	}
	hydrateMinOnce.Do(func() {
		// minifier.JS returns the input unchanged on a parse error — worst case we serve readable.
		hydrateMin = []byte(minifier.JS(hydrate.Runtime()))
		hydrateMinTag = contentTag(hydrateMin)
	})
	return hydrateMin, hydrateMinTag
}

func contentTag(b []byte) string {
	h := fnv.New64a()
	_, _ = h.Write(b)
	return `"` + strconv.FormatUint(h.Sum64(), 16) + `"`
}
