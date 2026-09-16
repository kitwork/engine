package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kitwork/engine/work"
)

func resolveRootConfig(cfg *Config) error {
	return resolveRootConfigAt(cfg, ".")
}

func resolveRootConfigAt(cfg *Config, base string) error {
	if cfg == nil {
		return fmt.Errorf("configuration is nil")
	}
	if cfg.RootExplicit {
		root := cfg.Root
		if !filepath.IsAbs(root) {
			root = filepath.Join(base, root)
		}
		root = filepath.Clean(root)

		// Keep the pre-apps rename operational for explicitly configured old
		// hosts. Auto mode retains their historical mixed folder resolver.
		if strings.EqualFold(filepath.Base(root), "apps") && !directoryExists(root) {
			legacy := filepath.Join(filepath.Dir(root), "tenants")
			if directoryExists(legacy) {
				cfg.Root = cleanConfiguredRoot(legacy)
				cfg.RootLayout = work.RootLayoutAuto
				return nil
			}
		}
		if !directoryExists(root) {
			return fmt.Errorf("root directory %q does not exist", cfg.Root)
		}
		cfg.Root = cleanConfiguredRoot(root)
		cfg.RootLayout = rootLayoutForPath(root)
		return validateResolvedRoot(cfg.Root, cfg.RootLayout)
	}

	appRoot := filepath.Join(base, "app")
	appsRoot := filepath.Join(base, "apps")
	hasApp := directoryExists(appRoot)
	hasApps := directoryExists(appsRoot)
	switch {
	case hasApp && hasApps:
		return fmt.Errorf("root is ambiguous: both app/ and apps/ exist; declare app.root(\"app\") or app.root(\"apps\")")
	case hasApp:
		cfg.Root = cleanConfiguredRoot(appRoot)
		cfg.RootLayout = appRootLayout(appRoot)
	case hasApps:
		cfg.Root = cleanConfiguredRoot(appsRoot)
		cfg.RootLayout = work.RootLayoutMultiTenant
	default:
		legacyRoot := filepath.Join(base, "tenants")
		if directoryExists(legacyRoot) {
			cfg.Root = cleanConfiguredRoot(legacyRoot)
			cfg.RootLayout = work.RootLayoutAuto
			return nil
		}
		return fmt.Errorf("no app root found: create app/ for one app, create apps/ for multiple tenants, or declare app.root(...)")
	}
	return validateResolvedRoot(cfg.Root, cfg.RootLayout)
}

func rootLayoutForPath(root string) work.RootLayout {
	clean := filepath.Clean(root)
	switch strings.ToLower(filepath.Base(clean)) {
	case "app":
		return appRootLayout(clean)
	case "apps":
		return work.RootLayoutMultiTenant
	case ".", string(filepath.Separator):
		return work.RootLayoutSingle
	default:
		return work.RootLayoutAuto
	}
}

func appRootLayout(root string) work.RootLayout {
	if regularFileExists(filepath.Join(root, work.RouterFileName)) {
		return work.RootLayoutSingle
	}
	if len(work.DiscoverFlatSites(root)) > 0 {
		return work.RootLayoutMultiDomain
	}
	// An empty app/ is a direct app under construction. Once domain folders
	// with root routers are added, the next boot selects multi-domain mode.
	return work.RootLayoutSingle
}

func regularFileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func cleanConfiguredRoot(root string) string {
	return filepath.Clean(root)
}

func directoryExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func validateResolvedRoot(root string, layout work.RootLayout) error {
	return work.ValidateRootLayout(root, layout)
}
