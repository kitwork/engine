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
// JIT modules used by the page into the same external, cacheable response.
func serveHydrateIf(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path != hydrate.RuntimePath {
		return false
	}
	keys := runtimeComponentKeys(r.URL.Query().Get("components"))
	body, etag := hydrateAsset(keys)
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

func runtimeComponentKeys(raw string) []string {
	if len(raw) > 1024 {
		return nil
	}
	parts := strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' })
	if len(parts) > 32 {
		parts = parts[:32]
	}
	keys := jitjs.ModuleKeys(parts)
	sort.Strings(keys)
	return keys
}

func hydrateAsset(keys []string) ([]byte, string) {
	mode := "min:"
	if AllowLocal {
		mode = "raw:"
	}
	key := mode + strings.Join(keys, ",")
	if cached, ok := runtimeAssetCache.Load(key); ok {
		asset := cached.(runtimeAsset)
		return asset.body, asset.etag
	}

	source := hydrate.Runtime()
	if modules := jitjs.ModulesJS(keys); modules != "" {
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
