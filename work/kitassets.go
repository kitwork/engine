package work

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"

	fonts "github.com/kitwork/engine/jit/fonts"
	jitjs "github.com/kitwork/engine/jit/js"
	"github.com/kitwork/engine/ui/stylesheet"
)

// siteOrNil keeps a nil *siteComponents from becoming a non-nil jitjs.Site interface value.
func siteOrNil(site *siteComponents) jitjs.Site {
	if site == nil {
		return nil
	}
	return site
}

// enginePathSegment is the first segment of everything the engine serves for itself: /_kitwork/.
// Under it, grouped by role and never by file type (the extension says that), each file named
// <role>.<hash>.<ext> — one-of-a-kind files at the top, kinds with many files in a folder:
//
//	/_kitwork/kernel.<hash>.js   /_kitwork/drive.<hash>.js   /_kitwork/scrolled.<hash>.js
//	/_kitwork/warnings.<hash>.js (a .ui page's warnings, while developing only)
//	/_kitwork/bundle.<hash>.js   (a .ui page's JavaScript parts as one file, router.bundle "split")
//	/_kitwork/site.<hash>.css    (a .ui page's reboot, colours and fonts as one file, router.bundle)
//	/_kitwork/page.<hash>.css    /_kitwork/icons.<hash>.css  /_kitwork/logo.<hash>.css  /_kitwork/fonts.<hash>.css
//	/_kitwork/components/<name>.<hash>.js   /_kitwork/services/<name>.<hash>.js
//	/_kitwork/directives/<name>.<hash>.js   /_kitwork/fonts/<family>-<weight>-<subset>.<hash>.woff2
const enginePathSegment = "_kitwork"

// isEnginePath reports whether a request path is the engine's own, under /_kitwork/. It is read the
// way a disk would read it, not only as written. A tenant's folders starting with "_" are private
// in the route tree, but a FILE under one is still a static file to serveTreeStatic, so every
// spelling that reaches <tenant>/_kitwork on disk — "//_kitwork/x", "/_kitwork\x", and "/_KITWORK/x"
// or "/_kitwork./x" on a filesystem that ignores case or trailing dots — must be answered by the
// engine, or the tenant's file would be served in its place.
func isEnginePath(p string) bool {
	clean := path.Clean("/" + strings.ReplaceAll(strings.TrimPrefix(p, "/"), `\`, "/"))
	first, _, _ := strings.Cut(strings.TrimPrefix(clean, "/"), "/")
	first, _, _ = strings.Cut(first, ":") // a Windows stream name: _kitwork::$DATA
	return strings.EqualFold(strings.TrimRight(first, ". "), enginePathSegment)
}

// serveEngineIf answers what the engine serves for itself, before the folder tree is walked. A path
// under /_kitwork/ that names nothing is a 404 from the engine (engineNotFound) — never the tenant's
// disk, never a route — so a folder named _kitwork in a site is never served, however the request
// spells it. The names used before 11/10/2026 keep answering, for pages cached or left open across a
// deploy: /kit/… (the JavaScript) and /jitfonts/… (the fonts), each with today's file, not kept.
func (t *Tenant) serveEngineIf(w http.ResponseWriter, r *http.Request) bool {
	if r == nil {
		return false
	}
	switch p := r.URL.Path; {
	case isEnginePath(p):
		name, stylesheet := pageStylesheetName(p)
		switch {
		case strings.HasPrefix(p, fonts.RoutePrefix): // the woff2 folder, /_kitwork/fonts/…
			serveFont(w, r)
		case stylesheet: // a page's stylesheet, /_kitwork/<role>.<hash>.css — fonts.<hash>.css among them
			t.servePageStylesheet(w, r, name)
		default:
			t.serveKitAsset(w, r)
		}
		return true
	case strings.HasPrefix(p, jitjs.LegacyAssetPrefix):
		t.serveKitAsset(w, r)
		return true
	case strings.HasPrefix(p, fonts.LegacyRoutePrefix):
		serveFont(w, r)
		return true
	}
	return false
}

// serveKitAsset serves one page JavaScript file — the kernel, Drive, and one file per component,
// service and directive (jit/js delivery.go), and a Kitwork UI page's scrolled.<hash>.js
// (scrolledScript) and, while developing, its warnings.<hash>.js (router_ui.go warningsScript: a name
// the generation never made is the engine's 404). An old name that still means something — a page
// cached before a deploy, the /kit/ prefix, a component or service name with its version — gets
// today's file, marked no-cache, so the page keeps working without that file being kept under a name
// that is not its own.
//
// A site's own component is looked up in THIS tenant's _components, so one site's file is never
// answered from another's — though, being content-addressed, the same bytes would be the same name.
func (t *Tenant) serveKitAsset(w http.ResponseWriter, r *http.Request) {
	if exact, ok := scrolledScriptAt(r.URL.Path); ok {
		script := scrolledScript()
		serveEngineFile(w, r, "text/javascript; charset=utf-8", script.body, script.digest, exact)
		return
	}
	if name, ok := generationScriptName(r.URL.Path); ok {
		// a page's warnings while developing (router_ui.go warningsScript) or its JavaScript bundle
		// (router.bundle javascript "split", router_ui_bundle.go): only this generation's, no warnings
		// in production; a bundle name it did not make loads the parts its tag names (staleBundle)
		var script engineScript
		if plan := t.renderPlan(); plan != nil {
			script, ok = plan.ui.script(name)
		}
		if (!ok || script.body == nil) && strings.HasPrefix(name, bundleRole+".") {
			stale := staleBundle()
			serveEngineFile(w, r, "text/javascript; charset=utf-8", stale.body, stale.digest, false)
			return
		}
		if !ok || script.body == nil {
			engineNotFound(w, r)
			return
		}
		serveEngineFile(w, r, "text/javascript; charset=utf-8", script.body, script.digest, true)
		return
	}
	asset, exact := jitjs.AssetAt(r.URL.Path, siteOrNil(t.renderPlan().SiteComponents()))
	if asset == nil {
		engineNotFound(w, r)
		return
	}
	serveEngineFile(w, r, "text/javascript; charset=utf-8", asset.Body, asset.Digest, exact)
}

// engineScript is a script the engine serves for itself from its own bytes: its path under /_kitwork/,
// its bytes and their whole SHA-256 (the ETag).
type engineScript struct {
	path   string
	body   []byte
	digest string
}

// scrolledScript is scrolled.<hash>.js (ui/stylesheet ScrolledScript): the few lines that keep the
// flag a Kitwork UI page's scrolled: rules read. The same bytes for every site, so one file the
// browser keeps for good, linked only by a page that can carry a scrolled: class (buildHead).
var scrolledScript = sync.OnceValue(func() engineScript {
	script := stylesheet.ScrolledScript()
	sum := sha256.Sum256(script.JavaScript)
	return engineScript{path: jitjs.AssetPrefix + script.Name + ".js", body: script.JavaScript, digest: hex.EncodeToString(sum[:])}
})

// scrolledScriptAt reports whether a request path names scrolled.<hash>.js, /_kitwork/scrolled.<16
// hex>.js, and whether it is the current name. A name an older engine wrote (another hash, from a page
// cached or left open across a deploy) is answered with today's file, not kept, as the engine's other
// scripts are.
func scrolledScriptAt(p string) (exact, ok bool) {
	rest, found := strings.CutPrefix(p, jitjs.AssetPrefix+"scrolled.")
	hash, isScript := strings.CutSuffix(rest, ".js")
	if !found || !isScript || len(hash) != 16 {
		return false, false
	}
	for _, c := range hash {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false, false
		}
	}
	return p == scrolledScript().path, true
}

// servePageStylesheet serves one of a Kitwork UI page's stylesheets, /_kitwork/<role>.<hash>.css
// (page, icons, logo, fonts), from the store of the generation that compiled the page (router_ui.go):
// what one generator made for the page, named by its role and its bytes, so kept for good. A name this
// generation never made — an unknown hash, a hash under another role — is the engine's 404, not kept:
// never the tenant's disk, and never an older stylesheet under a name that is not its own, as a page
// compiled since may name it.
func (t *Tenant) servePageStylesheet(w http.ResponseWriter, r *http.Request, name string) {
	plan := t.renderPlan()
	if plan == nil {
		engineNotFound(w, r)
		return
	}
	sheet, ok := plan.ui.stylesheet(name)
	if !ok {
		engineNotFound(w, r)
		return
	}
	serveEngineFile(w, r, "text/css; charset=utf-8", sheet.body, sheet.digest, true)
}

// serveEngineFile writes one file the engine serves for itself. Each current name carries a hash of
// the file's bytes, so an exact name is cached for a year and never revalidated: a different byte is
// a different name. A name that is not exact (an old hash, an old prefix, an old spelling) still
// gets the file, marked no-cache, so nothing keeps it under a name that is not its own. The ETag is
// the whole digest either way.
func serveEngineFile(w http.ResponseWriter, r *http.Request, contentType string, body []byte, digest string, exact bool) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		engineNotFound(w, r)
		return
	}
	header := w.Header()
	header.Set("Content-Type", contentType)
	if exact {
		header.Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		header.Set("Cache-Control", "no-cache")
	}
	header.Set("X-Content-Type-Options", "nosniff")
	etag := `"` + digest + `"`
	header.Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	header.Set("Content-Length", strconv.Itoa(len(body)))
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	_, _ = w.Write(body)
}

// engineNotFound is the engine's 404 for a name under its prefix that names nothing. It is not
// kept: the same name may mean a file after the next deploy, and a page compiled since may name it.
func engineNotFound(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	http.NotFound(w, r)
}
