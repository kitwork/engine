package work

import (
	"os"
	"path/filepath"
	"testing"
)

// The filesystem is the source of truth for tenant layout: with no DB row (database.System is nil
// in tests), NewTenant must still find tenants/<identity>/<domain> by scanning the root.
func TestNewTenantIdentityFromFilesystem(t *testing.T) {
	root := t.TempDir()
	mk := func(parts ...string) {
		p := filepath.Join(append([]string{root}, parts...)...)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("// x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// sites/ is a convention folder, never an identity — even when it holds the same domain name.
	mk(SitesDirName, "example.com", "router"+extension+".js")
	// The real tenant, nested under an identity folder.
	mk("029identity", "example.com", "router"+extension+".js")

	tn := NewTenant(root, "example.com")
	if got, want := tn.resolve(), filepath.Join(root, "029identity", "example.com"); got != want {
		t.Errorf("base = %q, want identity-nested %q", got, want)
	}

	// A flat tenant (directly under root) still resolves flat: no identity folder claims it.
	mk("flat.com", "router"+extension+".js")
	flat := NewTenant(root, "flat.com")
	if got, want := flat.resolve(), filepath.Join(root, "flat.com"); got != want {
		t.Errorf("flat base = %q, want %q", got, want)
	}

	// An unknown domain falls back to the flat path (pre-existing behaviour for new tenants).
	missing := NewTenant(root, "nowhere.dev")
	if got, want := missing.resolve(), filepath.Join(root, "nowhere.dev"); got != want {
		t.Errorf("missing base = %q, want %q", got, want)
	}
}
