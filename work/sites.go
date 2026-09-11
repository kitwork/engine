package work

import (
	"os"
	"path/filepath"
	"strings"
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

// DiscoverFlatSites lists a single app's domain folders:
// app/<domain>/router.kitwork.js.
func DiscoverFlatSites(root string) []string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var domains []string
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") ||
			strings.HasPrefix(entry.Name(), "_") {
			continue
		}
		if info, err := os.Stat(filepath.Join(root, entry.Name(), RouterFileName)); err == nil && !info.IsDir() {
			domains = append(domains, entry.Name())
		}
	}
	return domains
}

// TenantSiteSource is one site discovered under the strict multi-tenant
// contract apps/<identity>/<domain>/router.kitwork.js.
type TenantSiteSource struct {
	Identity  string
	Domain    string
	Directory string
}

// DiscoverTenantSites walks only the two declared multi-tenant levels. It
// ignores app infrastructure and hidden directories so discovery, prewarm,
// checks, and request ownership all observe the same source shape.
func DiscoverTenantSites(root string) ([]TenantSiteSource, error) {
	identities, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var sites []TenantSiteSource
	for _, identity := range identities {
		if !identity.IsDir() || reservedRootEntry(identity.Name()) {
			continue
		}
		identityRoot := filepath.Join(root, identity.Name())
		domains, readErr := os.ReadDir(identityRoot)
		if readErr != nil {
			continue
		}
		for _, domain := range domains {
			if !domain.IsDir() || reservedRootEntry(domain.Name()) {
				continue
			}
			directory := filepath.Join(identityRoot, domain.Name())
			marker, statErr := os.Stat(filepath.Join(directory, RouterFileName))
			if statErr != nil || marker.IsDir() {
				continue
			}
			sites = append(sites, TenantSiteSource{
				Identity:  identity.Name(),
				Domain:    domain.Name(),
				Directory: directory,
			})
		}
	}
	return sites, nil
}

// DiscoverTenantDomains returns each discovered multi-tenant domain once.
// Duplicate owners are rejected later by ResolveIdentityForLayout.
func DiscoverTenantDomains(root string) []string {
	sites, err := DiscoverTenantSites(root)
	if err != nil {
		return nil
	}
	seen := make(map[string]struct{}, len(sites))
	domains := make([]string, 0, len(sites))
	for _, site := range sites {
		if _, exists := seen[site.Domain]; exists {
			continue
		}
		seen[site.Domain] = struct{}{}
		domains = append(domains, site.Domain)
	}
	return domains
}

func reservedRootEntry(name string) bool {
	return name == SitesDirName || name == "test" ||
		strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")
}
