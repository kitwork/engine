package work

import (
	"hash/fnv"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"

	hydrate "github.com/kitwork/engine/jit/hydrate"
	jitjs "github.com/kitwork/engine/jit/js"
	"github.com/kitwork/engine/utilities/minifier"
)

// serveHydrateIf serves the shared client runtime at /kit.js. A components query composes only the
// JIT modules used by the page into the same external, cacheable response — the embedded catalogue
// plus, for this tenant, whatever it owns in _components (sitecomponents.go).
func (t *Tenant) serveHydrateIf(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path != hydrate.RuntimePath {
		return false
	}
	site := t.renderPlan().SiteComponents()
	keys := runtimeComponentKeys(r.URL.Query().Get("components"), site)
	body, etag := hydrateAsset(keys, site)
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

type runtimeAsset struct {
	body []byte
	etag string
}

var runtimeAssetCache sync.Map

func runtimeComponentKeys(raw string, site *siteComponents) []string {
	if len(raw) > 1024 {
		return nil
	}
	parts := strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' })
	if len(parts) > 32 {
		parts = parts[:32]
	}
	keys := jitjs.ModuleKeysFor(parts, siteOrNil(site))
	sort.Strings(keys)
	return keys
}

// siteOrNil keeps a nil *siteComponents from becoming a non-nil jitjs.Site interface value.
func siteOrNil(site *siteComponents) jitjs.Site {
	if site == nil {
		return nil
	}
	return site
}

func hydrateAsset(keys []string, site *siteComponents) ([]byte, string) {
	mode := "min:"
	if AllowLocal {
		mode = "raw:"
	}
	// The site's content signature joins the cache key: one tenant's component can never be served
	// from another tenant's entry, and editing a component invalidates only what contains it.
	key := mode + site.Tag() + "|" + strings.Join(keys, ",")
	if cached, ok := runtimeAssetCache.Load(key); ok {
		asset := cached.(runtimeAsset)
		return asset.body, asset.etag
	}

	source := hydrate.Runtime()
	if modules := jitjs.ModulesJSFor(keys, siteOrNil(site)); modules != "" {
		source += "\n" + modules
	}
	if !AllowLocal {
		// The minifier returns the readable input unchanged on a parse error.
		source = minifier.JS(source)
	}
	asset := runtimeAsset{body: []byte(source)}
	asset.etag = contentTag(asset.body)
	actual, _ := runtimeAssetCache.LoadOrStore(key, asset)
	stored := actual.(runtimeAsset)
	return stored.body, stored.etag
}

func contentTag(b []byte) string {
	h := fnv.New64a()
	_, _ = h.Write(b)
	return `"` + strconv.FormatUint(h.Sum64(), 16) + `"`
}
