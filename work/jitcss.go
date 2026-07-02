package work

import (
	"hash/fnv"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	jitcss "github.com/kitwork/engine/jit/css"
	"github.com/kitwork/engine/modules/minifier"
)

// JITStylesheetPath is the DEFAULT route the site-wide JIT CSS is served at when a tenant
// declares router.jit(). Pages <link rel="stylesheet" href="/jitcss"> (auto-injected) and
// the browser caches it once for the whole site instead of inlining per render.
const JITStylesheetPath = "/jitcss"

// serveJITCSS writes the tenant's minified site-wide JIT CSS with an ETag + Cache-Control,
// honoring If-None-Match (304). No VM, no allocation beyond the cached string. This is the
// handler for the route registered by router.jit().
func serveJITCSS(t *Tenant, w http.ResponseWriter, r *http.Request) {
	css, etag := tenantJITCSS(t)
	// Validators/freshness go out on BOTH 200 and 304 (RFC 7232 §4.1) so caches keep the
	// stylesheet fresh on revalidation.
	w.Header().Set("ETag", `"`+etag+`"`)
	w.Header().Set("Cache-Control", "public, max-age=300")
	if r.Header.Get("If-None-Match") == `"`+etag+`"` {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	_, _ = w.Write([]byte(css))
}

type jitEntry struct{ css, sig string }

var jitStyleCache sync.Map // tenant base dir -> *jitEntry

// tenantJITCSS returns the tenant's MINIFIED site-wide JIT CSS plus an ETag. It scans every
// *.kitwork.html under the tenant for the classes used, generates once, and regenerates
// only when a template changes (mtime/size signature) — so it is free after the first hit.
func tenantJITCSS(t *Tenant) (css, etag string) {
	root := t.resolve()
	sig := templateSignature(root)
	if v, ok := jitStyleCache.Load(root); ok {
		if e := v.(*jitEntry); e.sig == sig {
			return e.css, sig
		}
	}
	out := minifier.CSS(jitcss.GenerateSiteCSS(t.jitcssConfig, readTemplates(root)...))
	jitStyleCache.Store(root, &jitEntry{css: out, sig: sig})
	return out, sig
}

// templateSignature hashes (path, mtime, size) of every template — cheap (stat only).
func templateSignature(root string) string {
	h := fnv.New64a()
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".kitwork.html") {
			return nil
		}
		if info, e := d.Info(); e == nil {
			_, _ = h.Write([]byte(p))
			_, _ = h.Write([]byte(strconv.FormatInt(info.ModTime().UnixNano(), 10)))
			_, _ = h.Write([]byte(strconv.FormatInt(info.Size(), 10)))
		}
		return nil
	})
	return strconv.FormatUint(h.Sum64(), 16)
}

func readTemplates(root string) []string {
	var out []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".kitwork.html") {
			return nil
		}
		if b, e := os.ReadFile(p); e == nil {
			out = append(out, string(b))
		}
		return nil
	})
	return out
}
