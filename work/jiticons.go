package work

import (
	"net/http"
	"sync"

	icons "github.com/kitwork/engine/jit/icons"
)

// IconStylesheetPath is the DEFAULT route the site-wide JIT icon CSS is served at when a tenant
// declares router.icons(). Pages <link rel="stylesheet" href="/jiticons"> (auto-injected) and the
// browser caches it once for the whole site instead of inlining the masks per render. It is the
// same-site stepping stone to a cross-origin icon CDN (jiticons.kitwork.io) — both serve plain CSS.
const IconStylesheetPath = "/jiticons"

// serveIconCSS writes the tenant's site-wide JIT icon CSS with an ETag + Cache-Control, honoring
// If-None-Match (304). No VM, no allocation beyond the cached string. This is the handler for the
// route registered by router.icons(); it mirrors serveJITCSS.
func serveIconCSS(t *Tenant, w http.ResponseWriter, r *http.Request) {
	css, etag := tenantIconCSS(t)
	w.Header().Set("ETag", `"`+etag+`"`)
	w.Header().Set("Cache-Control", "public, max-age=300")
	if r.Header.Get("If-None-Match") == `"`+etag+`"` {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	_, _ = w.Write([]byte(css))
}

var iconStyleCache sync.Map // tenant base dir -> *jitEntry

// tenantIconCSS returns the tenant's site-wide JIT icon CSS plus an ETag. It scans every
// *.kitwork.html under the tenant for used icon-<name> classes, generates once, and regenerates
// only when a template changes (the same mtime/size signature jitcss uses) — free after the first
// hit. Reuses jitEntry/templateSignature/readTemplates from jitcss.go.
func tenantIconCSS(t *Tenant) (css, etag string) {
	root := t.resolve()
	sig := templateSignature(root)
	if v, ok := iconStyleCache.Load(root); ok {
		if e := v.(*jitEntry); e.sig == sig {
			return e.css, sig
		}
	}
	out := icons.SiteCSS(readTemplates(root)...)
	iconStyleCache.Store(root, &jitEntry{css: out, sig: sig})
	return out, sig
}
