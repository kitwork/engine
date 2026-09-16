package work

import (
	"fmt"
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

// AppSiteSource is one domain-scoped site owned by a root app.
type AppSiteSource struct {
	Domain    string
	Directory string
}

// DiscoverAppSites walks only the declared app/<domain> level. Hidden and
// app-infrastructure directories can never become domain sites.
func DiscoverAppSites(root string) ([]AppSiteSource, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var sites []AppSiteSource
	for _, entry := range entries {
		if !entry.IsDir() || reservedRootEntry(entry.Name()) {
			continue
		}
		directory := filepath.Join(root, entry.Name())
		marker, statErr := os.Stat(filepath.Join(directory, RouterFileName))
		if os.IsNotExist(statErr) {
			continue
		}
		if statErr != nil {
			return nil, fmt.Errorf("inspect app site %q: %w", entry.Name(), statErr)
		}
		if marker.IsDir() {
			continue
		}
		sites = append(sites, AppSiteSource{
			Domain:    entry.Name(),
			Directory: directory,
		})
	}
	return sites, nil
}

// DiscoverFlatSites is the compatibility list form used by TLS and prewarm.
// Call DiscoverAppSites when discovery errors must be reported.
func DiscoverFlatSites(root string) []string {
	sites, err := DiscoverAppSites(root)
	if err != nil {
		return nil
	}
	domains := make([]string, 0, len(sites))
	for _, site := range sites {
		domains = append(domains, site.Domain)
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
	if misplaced, inspectErr := regularRouterMarker(root); inspectErr != nil {
		return nil, inspectErr
	} else if misplaced {
		return nil, fmt.Errorf(
			"multi-tenant router %q is two levels too shallow; expected apps/<identity>/<domain>/%s",
			filepath.Join(root, RouterFileName),
			RouterFileName,
		)
	}
	var sites []TenantSiteSource
	for _, identity := range identities {
		if !identity.IsDir() || reservedRootEntry(identity.Name()) {
			continue
		}
		identityRoot := filepath.Join(root, identity.Name())
		if misplaced, inspectErr := regularRouterMarker(identityRoot); inspectErr != nil {
			return nil, inspectErr
		} else if misplaced {
			return nil, fmt.Errorf(
				"multi-tenant router %q is one level too shallow; expected apps/<identity>/<domain>/%s",
				filepath.Join(identityRoot, RouterFileName),
				RouterFileName,
			)
		}
		domains, readErr := os.ReadDir(identityRoot)
		if readErr != nil {
			return nil, fmt.Errorf("read app identity %q: %w", identity.Name(), readErr)
		}
		for _, domain := range domains {
			if !domain.IsDir() || reservedRootEntry(domain.Name()) {
				continue
			}
			directory := filepath.Join(identityRoot, domain.Name())
			marker, statErr := os.Stat(filepath.Join(directory, RouterFileName))
			if os.IsNotExist(statErr) {
				continue
			}
			if statErr != nil {
				return nil, fmt.Errorf(
					"inspect site %q for app identity %q: %w",
					domain.Name(),
					identity.Name(),
					statErr,
				)
			}
			if marker.IsDir() {
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

func regularRouterMarker(root string) (bool, error) {
	marker := filepath.Join(root, RouterFileName)
	info, err := os.Stat(marker)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect router marker %q: %w", marker, err)
	}
	return !info.IsDir(), nil
}
