package work

// The files a Kitwork UI site (router.work) serves as they are (KITWORK_UI §5, 20/10/2026): assets/
// under its own path, the catalog of root files below, and .well-known/. No other file under the app is
// served, whatever its name. A template site (router.kitwork.js) keeps serving every safe file under
// the app, or the prefixes its .assets() declared.

import (
	"os"
	"path/filepath"
	"strings"
)

// uiRootFiles is the engine's catalog of files a site serves from its root.
var uiRootFiles = map[string]bool{
	"/robots.txt": true, "/llms.txt": true, "/llms-full.txt": true, "/sitemap.xml": true,
	"/favicon.ico": true, "/apple-touch-icon.png": true, "/site.webmanifest": true,
	"/ads.txt": true, "/app-ads.txt": true, "/humans.txt": true,
}

// uiStaticAllowed reports whether a cleaned URL path is one a Kitwork UI site serves from disk.
func uiStaticAllowed(clean string) bool {
	return strings.HasPrefix(clean, "/assets/") || strings.HasPrefix(clean, "/.well-known/") || uiRootFiles[clean]
}

// uiSite reports whether the tenant is a Kitwork UI site: its root's router file is router.work.
func (t *Tenant) uiSite() bool {
	if tree := t.routeTree(); tree != nil && tree.root != nil && tree.root.folder != nil {
		return tree.root.folder.ui
	}
	return filepath.Base(RouterMarker(t.resolve())) == UIRouterFileName && fileExists(filepath.Join(t.resolve(), UIRouterFileName))
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
