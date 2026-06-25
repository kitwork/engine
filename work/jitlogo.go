package work

import (
	"net/http"
	"sync"

	logo "github.com/kitwork/engine/jit/logo"
)

// LogoStylesheetPath is the DEFAULT route the site-wide JIT logo CSS is served at when a tenant
// declares router.logo(). Mirrors /jiticons; the same-site form of a future jitlogo CDN.
const LogoStylesheetPath = "/jitlogo"

// serveLogoCSS writes the tenant's site-wide JIT logo CSS with an ETag + Cache-Control (304-aware).
// No VM. Handler for the route registered by router.logo(); mirrors serveIconCSS.
func serveLogoCSS(t *Tenant, w http.ResponseWriter, r *http.Request) {
	css, etag := tenantLogoCSS(t)
	w.Header().Set("ETag", `"`+etag+`"`)
	w.Header().Set("Cache-Control", "public, max-age=300")
	if r.Header.Get("If-None-Match") == `"`+etag+`"` {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	_, _ = w.Write([]byte(css))
}

var logoStyleCache sync.Map // tenant base dir -> *jitEntry

// tenantLogoCSS returns the tenant's site-wide JIT logo CSS plus an ETag, regenerating only when a
// template changes (same mtime-signature as jitcss). Reuses jitEntry/templateSignature/readTemplates.
func tenantLogoCSS(t *Tenant) (css, etag string) {
	root := t.resolve()
	sig := templateSignature(root)
	if v, ok := logoStyleCache.Load(root); ok {
		if e := v.(*jitEntry); e.sig == sig {
			return e.css, sig
		}
	}
	out := logo.SiteCSS(readTemplates(root)...)
	logoStyleCache.Store(root, &jitEntry{css: out, sig: sig})
	return out, sig
}
