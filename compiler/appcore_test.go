package compiler

import (
	"os"
	"path/filepath"
	"testing"
)

// An app-shared `_core/…` import resolves by walking UP from the importing file: the DOMAIN's own _core
// wins if present, otherwise it falls back to the IDENTITY-level _core one directory up. Existing
// relative `../_core` imports are unaffected.
func TestAppCoreWalkUp(t *testing.T) {
	root := t.TempDir()
	identity := filepath.Join(root, "acme")
	domain := filepath.Join(identity, "shop.local")
	blog := filepath.Join(domain, "blog")
	for _, d := range []string{
		filepath.Join(identity, "_core"),
		filepath.Join(domain, "_core"),
		blog,
	} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(p, s string) {
		if err := os.WriteFile(p, []byte(s), 0644); err != nil {
			t.Fatal(err)
		}
	}
	// identity-level shared core
	write(filepath.Join(identity, "_core", "shared.kitwork.js"), `export const who = () => "identity-core";`)
	// a module that exists at BOTH levels — domain copy must win
	write(filepath.Join(identity, "_core", "both.kitwork.js"), `export const tag = () => "identity";`)
	write(filepath.Join(domain, "_core", "both.kitwork.js"), `export const tag = () => "domain";`)

	fromDir := blog

	// only at identity level → walk up finds it
	got, err := resolveModulePath("_core/shared.kitwork.js", fromDir)
	if err != nil {
		t.Fatalf("identity-level _core not resolved: %v", err)
	}
	if want := filepath.Join(identity, "_core", "shared.kitwork.js"); got != want {
		t.Errorf("shared resolved to %s, want %s", got, want)
	}

	// exists at both → the DOMAIN copy overrides (nearest wins)
	got, err = resolveModulePath("_core/both.kitwork.js", fromDir)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(domain, "_core", "both.kitwork.js"); got != want {
		t.Errorf("override resolved to %s, want domain copy %s", got, want)
	}

	// missing everywhere → error (not a silent wrong match up the tree)
	if _, err := resolveModulePath("_core/nope.kitwork.js", fromDir); err == nil {
		t.Errorf("expected error for missing app-shared module")
	}

	// a relative import is unchanged: still resolves to the exact single location
	got, err = resolveModulePath("../_core/both.kitwork.js", blog)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(domain, "_core", "both.kitwork.js"); got != want {
		t.Errorf("relative ../_core resolved to %s, want %s", got, want)
	}
}
