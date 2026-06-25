package work

import (
	"net/http"
	"sync"

	jitjs "github.com/kitwork/engine/jit/js"
)

// JitjsPath is the DEFAULT route the site-wide jitjs runtime is served at when a tenant declares
// router.jitjs(). Pages carry <script src="/jitjs" defer> (auto-injected) and the browser caches
// the runtime once for the whole site instead of inlining the per-page verbs.
const JitjsPath = "/jitjs"

// serveJitjsJS writes the tenant's site-wide jitjs runtime with an ETag + Cache-Control, honoring
// If-None-Match (304). No VM. Handler for the route registered by router.jitjs(); mirrors serveIconCSS.
func serveJitjsJS(t *Tenant, w http.ResponseWriter, r *http.Request) {
	js, etag := tenantJitjsJS(t)
	w.Header().Set("ETag", `"`+etag+`"`)
	w.Header().Set("Cache-Control", "public, max-age=300")
	if r.Header.Get("If-None-Match") == `"`+etag+`"` {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	_, _ = w.Write([]byte(js))
}

var jitjsCache sync.Map // tenant base dir -> *jitEntry

// tenantJitjsJS returns the tenant's site-wide jitjs runtime (core dispatcher + the union of verbs
// used across every template) plus an ETag, regenerating only when a template changes (the same
// mtime/size signature jitcss uses). Reuses jitEntry/templateSignature/readTemplates from jitcss.go.
func tenantJitjsJS(t *Tenant) (js, etag string) {
	root := t.resolve()
	sig := templateSignature(root)
	if v, ok := jitjsCache.Load(root); ok {
		if e := v.(*jitEntry); e.sig == sig {
			return e.css, sig
		}
	}
	out := jitjs.SiteRuntimeJS(readTemplates(root)...)
	jitjsCache.Store(root, &jitEntry{css: out, sig: sig})
	return out, sig
}
