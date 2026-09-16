package work

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kitwork/engine/app"
	"github.com/kitwork/engine/database"
)

func TestTenantRootLayoutsResolveOnlyTheirDeclaredShape(t *testing.T) {
	root := t.TempDir()
	domain := "example.com"

	tests := []struct {
		name     string
		layout   RootLayout
		identity string
		want     string
	}{
		{name: "single", layout: RootLayoutSingle, want: root},
		{name: "multi-domain", layout: RootLayoutMultiDomain, identity: "ignored", want: filepath.Join(root, domain)},
		{name: "multi-tenant", layout: RootLayoutMultiTenant, identity: "app-id", want: filepath.Join(root, "app-id", domain)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			appRuntime := app.NewRuntime(test.identity)
			defer appRuntime.Close()
			siteRuntime, err := appRuntime.Site(root, domain)
			if err != nil {
				t.Fatal(err)
			}
			tenant := NewTenantWithRuntimeLayout(
				root,
				domain,
				test.layout,
				appRuntime,
				siteRuntime,
			)
			if got := tenant.resolve(); got != test.want {
				t.Fatalf("resolved root = %q, want %q", got, test.want)
			}
		})
	}
}

func TestResolveIdentityForLayoutSeparatesSiteAndTenantAuthority(t *testing.T) {
	root := t.TempDir()
	domain := "example.com"
	previousSystem := database.System
	database.System = nil
	t.Cleanup(func() { database.System = previousSystem })

	for _, layout := range []RootLayout{RootLayoutSingle, RootLayoutMultiDomain} {
		identity, err := ResolveIdentityForLayout(root, domain, layout)
		if err != nil {
			t.Fatalf("layout %s returned an error: %v", layout, err)
		}
		if identity != RootAppIdentity {
			t.Fatalf("layout %s identity = %q, want %q", layout, identity, RootAppIdentity)
		}
	}

	marker := filepath.Join(root, "app-id", domain, RouterFileName)
	if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte("// root"), 0o644); err != nil {
		t.Fatal(err)
	}
	identity, err := ResolveIdentityForLayout(root, domain, RootLayoutMultiTenant)
	if err != nil {
		t.Fatal(err)
	}
	if identity != "app-id" {
		t.Fatalf("identity = %q, want app-id", identity)
	}

	if _, err := ResolveIdentityForLayout(root, "missing.example", RootLayoutMultiTenant); err == nil ||
		!strings.Contains(err.Error(), "not found") {
		t.Fatalf("missing domain error = %v", err)
	}
}

func TestResolveIdentityForLayoutRejectsDuplicateDomainOwners(t *testing.T) {
	root := t.TempDir()
	previousSystem := database.System
	database.System = nil
	t.Cleanup(func() { database.System = previousSystem })
	for _, identity := range []string{"first", "second"} {
		marker := filepath.Join(root, identity, "example.com", RouterFileName)
		if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(marker, []byte("// root"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := ResolveIdentityForLayout(root, "example.com", RootLayoutMultiTenant); err == nil ||
		!strings.Contains(err.Error(), "multiple app identities") {
		t.Fatalf("duplicate domain error = %v", err)
	}
}

func TestResolveIdentityForLayoutRejectsUnsafeSystemIdentity(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE domain (hostname TEXT PRIMARY KEY, identity TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		`INSERT INTO domain (hostname, identity) VALUES (?, ?)`,
		"example.com",
		"../outside",
	); err != nil {
		t.Fatal(err)
	}

	previousSystem := database.System
	database.System = db
	t.Cleanup(func() { database.System = previousSystem })
	if _, err := ResolveIdentityForLayout("apps", "example.com", RootLayoutMultiTenant); err == nil || !strings.Contains(err.Error(), "invalid system identity") {
		t.Fatalf("unsafe identity error = %v", err)
	}
}

func TestSingleAppLayoutsUseOneLogicalAppID(t *testing.T) {
	root := t.TempDir()
	for _, layout := range []RootLayout{RootLayoutSingle, RootLayoutMultiDomain} {
		appRuntime := app.NewRuntime("")
		siteRuntime, err := appRuntime.Site(root, "example.com")
		if err != nil {
			t.Fatal(err)
		}
		tenant := NewTenantWithRuntimeLayout(root, "example.com", layout, appRuntime, siteRuntime)
		if tenant.AppID() != "app" {
			t.Fatalf("layout %s app ID = %q, want app", layout, tenant.AppID())
		}
		appRuntime.Close()
	}
}

func TestDiscoverTenantSitesUsesOnlyIdentityDomainShape(t *testing.T) {
	root := t.TempDir()
	writeTenantLayoutMarker(t, filepath.Join(root, "first", "one.example"))
	writeTenantLayoutMarker(t, filepath.Join(root, "second", "two.example"))
	writeTenantLayoutMarker(t, filepath.Join(root, "_core", "ignored.example"))
	writeTenantLayoutMarker(t, filepath.Join(root, "first", "_queue"))

	sites, err := DiscoverTenantSites(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(sites) != 2 {
		t.Fatalf("discovered sites = %+v, want 2", sites)
	}
	if sites[0].Identity != "first" || sites[0].Domain != "one.example" ||
		sites[1].Identity != "second" || sites[1].Domain != "two.example" {
		t.Fatalf("discovered sites = %+v", sites)
	}
	domains := DiscoverTenantDomains(root)
	if len(domains) != 2 || domains[0] != "one.example" || domains[1] != "two.example" {
		t.Fatalf("discovered domains = %v", domains)
	}
}

func TestDiscoverAppSitesIgnoresInfrastructureAndNestedIdentities(t *testing.T) {
	root := t.TempDir()
	writeTenantLayoutMarker(t, filepath.Join(root, "one.example"))
	writeTenantLayoutMarker(t, filepath.Join(root, "_core"))
	writeTenantLayoutMarker(t, filepath.Join(root, "identity", "nested.example"))

	sites, err := DiscoverAppSites(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(sites) != 1 || sites[0].Domain != "one.example" ||
		sites[0].Directory != filepath.Join(root, "one.example") {
		t.Fatalf("discovered app sites = %+v", sites)
	}
	domains := DiscoverFlatSites(root)
	if len(domains) != 1 || domains[0] != "one.example" {
		t.Fatalf("discovered app domains = %v", domains)
	}
}

func TestDiscoverTenantSitesRejectsRoutersAboveTheDomainLevel(t *testing.T) {
	for _, test := range []struct {
		name      string
		directory func(string) string
		want      string
	}{
		{
			name:      "root",
			directory: func(root string) string { return root },
			want:      "two levels too shallow",
		},
		{
			name:      "identity",
			directory: func(root string) string { return filepath.Join(root, "identity") },
			want:      "one level too shallow",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeTenantLayoutMarker(t, test.directory(root))
			if _, err := DiscoverTenantSites(root); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("misplaced router error = %v", err)
			}
		})
	}
}

func writeTenantLayoutMarker(t testing.TB, directory string) {
	t.Helper()
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, RouterFileName), []byte("// root"), 0o644); err != nil {
		t.Fatal(err)
	}
}
