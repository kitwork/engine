package work

import (
	"net/http"
	"strconv"
	"strings"

	kitjavascript "github.com/kitwork/engine/jit/javascript"
	"github.com/kitwork/engine/site"
)

const (
	jitAssetPrefix   = "/jit/"
	kitJSAssetPrefix = "/kit.js/"
)

// serveJITAssetIf exposes the bounded site-lifetime staged JavaScript CAS.
// Both the SHA-256 and the canonical suffix are authoritative: a valid hash
// requested as the wrong package/role filename is not served.
func (t *Tenant) serveJITAssetIf(w http.ResponseWriter, r *http.Request) bool {
	if r == nil || !strings.HasPrefix(r.URL.Path, jitAssetPrefix) {
		return false
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.NotFound(w, r)
		return true
	}
	if r.URL.RawQuery != "" {
		http.NotFound(w, r)
		return true
	}
	contentHash, suffix, valid := parseJITAssetPath(r.URL.Path)
	if !valid || t.siteRuntime == nil {
		http.NotFound(w, r)
		return true
	}
	asset, ok := t.siteRuntime.ContentAsset(contentHash)
	if !ok || asset.Suffix != suffix || !validJITAssetRoleSuffix(asset.Role, asset.Suffix) {
		http.NotFound(w, r)
		return true
	}
	serveImmutableJavaScript(w, r, asset)
	return true
}

func parseJITAssetPath(requestPath string) (string, string, bool) {
	filename := strings.TrimPrefix(requestPath, jitAssetPrefix)
	if len(filename) <= 64+len("..js") || !strings.HasSuffix(filename, ".js") || filename[64] != '.' {
		return "", "", false
	}
	contentHash := filename[:64]
	suffix := filename[65 : len(filename)-len(".js")]
	if !kitjavascript.ValidContentHash(contentHash) || !validJITAssetSuffix(suffix) {
		return "", "", false
	}
	return contentHash, suffix, true
}

func validJITAssetSuffix(suffix string) bool {
	if suffix == "" || len(suffix) > kitjavascript.MaxStagedPackageSuffixBytes {
		return false
	}
	for index := range len(suffix) {
		char := suffix[index]
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' {
			continue
		}
		if index > 0 && (char == '-' || char == '.' || char == '_') {
			continue
		}
		return false
	}
	return true
}

func validJITAssetRoleSuffix(role, suffix string) bool {
	switch kitjavascript.JITRole(role) {
	case kitjavascript.JITRoleRuntime,
		kitjavascript.JITRoleHydrate,
		kitjavascript.JITRoleGraph,
		kitjavascript.JITRoleComponents:
		return suffix == role
	case kitjavascript.JITRoleService, kitjavascript.JITRoleComponent:
		return validJITAssetSuffix(suffix)
	default:
		return false
	}
}

// serveKitJSAssetIf preserves the historical hashed endpoint only for legacy
// CAS entries that carry no staged Role/Suffix metadata. New generations never
// publish these entries and emit only /jit URLs.
func (t *Tenant) serveKitJSAssetIf(w http.ResponseWriter, r *http.Request) bool {
	if r == nil || !strings.HasPrefix(r.URL.Path, kitJSAssetPrefix) {
		return false
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.NotFound(w, r)
		return true
	}
	filename := strings.TrimPrefix(r.URL.Path, kitJSAssetPrefix)
	if len(filename) != 64+len(".js") || !strings.HasSuffix(filename, ".js") {
		http.NotFound(w, r)
		return true
	}
	contentHash := strings.TrimSuffix(filename, ".js")
	if !kitjavascript.ValidContentHash(contentHash) || t.siteRuntime == nil {
		http.NotFound(w, r)
		return true
	}
	asset, ok := t.siteRuntime.ContentAsset(contentHash)
	if !ok || asset.Role != "" || asset.Suffix != "" {
		http.NotFound(w, r)
		return true
	}
	serveImmutableJavaScript(w, r, asset)
	return true
}

func serveImmutableJavaScript(w http.ResponseWriter, r *http.Request, asset site.ContentAsset) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	etag := `"` + asset.ContentHash + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Length", strconv.Itoa(len(asset.Body)))
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	_, _ = w.Write(asset.Body)
}
