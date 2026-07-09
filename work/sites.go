package work

import (
	"os"
	"path/filepath"
)

// SitesDirName is the single-tenant convention folder: <root>/sites/<domain>/router.kitwork.js.
// Each subfolder IS a domain — no identity layer, no DB registration. Dropping a folder here is
// enough for the engine to serve it and for AutoSSL to obtain its certificate.
const SitesDirName = "sites"

// DiscoverSites lists the domains under <root>/sites/. A subfolder counts as a site only if it
// contains the tenant marker (RouterFileName), so half-created or unrelated folders are ignored.
// Returns nil when there is no sites/ directory (e.g. standalone or pure multi-tenant layouts).
func DiscoverSites(root string) []string {
	switch root {
	case "", "./", "../", "/", ".", "..":
		return nil
	}
	sitesDir := filepath.Join(root, SitesDirName)
	entries, err := os.ReadDir(sitesDir)
	if err != nil {
		return nil
	}
	var domains []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(sitesDir, e.Name(), RouterFileName)); err == nil {
			domains = append(domains, e.Name())
		}
	}
	return domains
}
