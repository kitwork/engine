package work

import (
	"net/http"
	"strconv"
	"strings"

	jitjs "github.com/kitwork/engine/jit/js"
)

// siteOrNil keeps a nil *siteComponents from becoming a non-nil jitjs.Site interface value.
func siteOrNil(site *siteComponents) jitjs.Site {
	if site == nil {
		return nil
	}
	return site
}

// serveKitAssetIf serves the page JavaScript files under /kit/ — the kernel, Drive, and one file per
// component and capability (jit/js delivery.go). Each name carries a hash of its bytes, so a file is
// cached for a year and never revalidated: a different byte is a different name. An old name that
// still means something — a page cached before a deploy — gets today's file, marked no-cache, so the
// page keeps working without that file being kept under a name that is not its own.
//
// A site's own component is looked up in THIS tenant's _components, so one site's file is never
// answered from another's — though, being content-addressed, the same bytes would be the same name.
func (t *Tenant) serveKitAssetIf(w http.ResponseWriter, r *http.Request) bool {
	if r == nil || !strings.HasPrefix(r.URL.Path, jitjs.AssetPrefix) {
		return false
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.NotFound(w, r)
		return true
	}
	asset, exact := jitjs.AssetAt(r.URL.Path, siteOrNil(t.renderPlan().SiteComponents()))
	if asset == nil {
		http.NotFound(w, r)
		return true
	}
	header := w.Header()
	header.Set("Content-Type", "text/javascript; charset=utf-8")
	if exact {
		header.Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		header.Set("Cache-Control", "no-cache")
	}
	header.Set("X-Content-Type-Options", "nosniff")
	etag := `"` + asset.Digest + `"`
	header.Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return true
	}
	header.Set("Content-Length", strconv.Itoa(len(asset.Body)))
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return true
	}
	_, _ = w.Write(asset.Body)
	return true
}
