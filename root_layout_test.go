package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kitwork/engine/work"
)

func TestResolveRootConfigAutoDetectsAppAndApps(t *testing.T) {
	tests := []struct {
		name    string
		root    string
		markers [][]string
		want    work.RootLayout
	}{
		{
			name:    "single app",
			root:    "app",
			markers: [][]string{{"app"}},
			want:    work.RootLayoutSingle,
		},
		{
			name:    "single app with multiple domains",
			root:    "app",
			markers: [][]string{{"app", "one.example"}, {"app", "two.example"}},
			want:    work.RootLayoutMultiDomain,
		},
		{
			name:    "multiple tenants",
			root:    "apps",
			markers: [][]string{{"apps", "identity", "example.com"}},
			want:    work.RootLayoutMultiTenant,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			base := t.TempDir()
			if err := os.Mkdir(filepath.Join(base, test.root), 0o755); err != nil {
				t.Fatal(err)
			}
			for _, marker := range test.markers {
				writeRootMarker(t, filepath.Join(append([]string{base}, marker...)...))
			}

			cfg := &Config{}
			if err := resolveRootConfigAt(cfg, base); err != nil {
				t.Fatal(err)
			}
			if cfg.Root != filepath.Join(base, test.root) {
				t.Fatalf("root = %q, want %q", cfg.Root, filepath.Join(base, test.root))
			}
			if cfg.RootLayout != test.want {
				t.Fatalf("layout = %s, want %s", cfg.RootLayout, test.want)
			}
		})
	}
}

func TestResolveRootConfigRejectsAmbiguousOrMissingAutoRoot(t *testing.T) {
	base := t.TempDir()
	for _, name := range []string{"app", "apps"} {
		if err := os.Mkdir(filepath.Join(base, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := resolveRootConfigAt(&Config{}, base); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ambiguous roots error = %v", err)
	}

	if err := resolveRootConfigAt(&Config{}, t.TempDir()); err == nil || !strings.Contains(err.Error(), "no app root") {
		t.Fatalf("missing roots error = %v", err)
	}
}

func TestResolveRootConfigKeepsLegacyTenantsAsLastFallback(t *testing.T) {
	base := t.TempDir()
	legacy := filepath.Join(base, "tenants")
	if err := os.Mkdir(legacy, 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := &Config{}
	if err := resolveRootConfigAt(cfg, base); err != nil {
		t.Fatal(err)
	}
	if cfg.Root != legacy || cfg.RootLayout != work.RootLayoutAuto {
		t.Fatalf("legacy root = %q (%s), want %q (auto)", cfg.Root, cfg.RootLayout, legacy)
	}
}

func TestResolveExplicitRootUsesFolderContract(t *testing.T) {
	base := t.TempDir()
	appRoot := filepath.Join(base, "app")
	appsRoot := filepath.Join(base, "apps")
	for _, root := range []string{appRoot, appsRoot} {
		if err := os.Mkdir(root, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeRootMarker(t, filepath.Join(appRoot, "one.example"))

	multiDomain := &Config{Root: appRoot, RootExplicit: true}
	if err := resolveRootConfigAt(multiDomain, base); err != nil {
		t.Fatal(err)
	}
	if multiDomain.RootLayout != work.RootLayoutMultiDomain {
		t.Fatalf("app layout = %s", multiDomain.RootLayout)
	}

	multiple := &Config{Root: appsRoot, RootExplicit: true}
	if err := resolveRootConfigAt(multiple, base); err != nil {
		t.Fatal(err)
	}
	if multiple.RootLayout != work.RootLayoutMultiTenant {
		t.Fatalf("apps layout = %s", multiple.RootLayout)
	}
}

func TestAppRootRouterTakesPrecedenceOverNestedRouteFolders(t *testing.T) {
	root := filepath.Join(t.TempDir(), "app")
	writeRootMarker(t, root)
	writeRootMarker(t, filepath.Join(root, "about"))

	if layout := appRootLayout(root); layout != work.RootLayoutSingle {
		t.Fatalf("layout = %s, want single", layout)
	}
}

func TestAppRootIgnoresReservedInfrastructureFolders(t *testing.T) {
	root := filepath.Join(t.TempDir(), "app")
	writeRootMarker(t, filepath.Join(root, "_core"))
	writeRootMarker(t, filepath.Join(root, ".data"))

	if layout := appRootLayout(root); layout != work.RootLayoutSingle {
		t.Fatalf("layout = %s, want single", layout)
	}
}

func TestParseConfigTracksWhetherRootWasDeclared(t *testing.T) {
	auto, err := ParseConfig(map[string]interface{}{})
	if err != nil {
		t.Fatal(err)
	}
	if auto.RootExplicit {
		t.Fatal("omitted root was marked explicit")
	}

	explicit, err := ParseConfig(map[string]interface{}{"root": "apps"})
	if err != nil {
		t.Fatal(err)
	}
	if !explicit.RootExplicit {
		t.Fatal("declared root was not marked explicit")
	}
}

func writeRootMarker(t testing.TB, directory string) {
	t.Helper()
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, work.RouterFileName), []byte("// root"), 0o644); err != nil {
		t.Fatal(err)
	}
}
