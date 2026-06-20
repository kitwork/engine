package work

import (
	"os"
	"path/filepath"
	"testing"
)

// Proves NATIVE `import … from "kitwork"` works end to end: full pipeline
// (lexer → parser → compiler → VM) plus actually USING the imported binding
// at runtime. Same path the real multi-tenant server uses.
func TestNativeKitworkImportRuntime(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "kitwork-import-native-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	tenantDir := filepath.Join(tmpDir, "test", "localhost")
	if err := os.MkdirAll(tenantDir, 0755); err != nil {
		t.Fatal(err)
	}

	// `log` is bound via the native import (lowered to `const { log } = kitwork()`),
	// then actually invoked — if the binding were broken this would fail at runtime.
	script := `
import { log } from "kitwork";
log.Print("E2E: native kitwork import works");
`
	if err := os.WriteFile(filepath.Join(tenantDir, "app.kitwork.js"), []byte(script), 0644); err != nil {
		t.Fatal(err)
	}

	if err := NewTenant(tmpDir, "localhost").Run(); err != nil {
		t.Fatalf("native kitwork import failed at runtime: %v", err)
	}
}

// Proves importing ANOTHER file works end to end: helper.kitwork.js exports,
// app.kitwork.js imports it (relative → esbuild fallback today) and uses it.
func TestRelativeFileImportRuntime(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "kitwork-import-relative-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	tenantDir := filepath.Join(tmpDir, "test", "localhost")
	if err := os.MkdirAll(tenantDir, 0755); err != nil {
		t.Fatal(err)
	}

	helper := `export const getHello = () => "Hello from modular helper!";`
	if err := os.WriteFile(filepath.Join(tenantDir, "helper.kitwork.js"), []byte(helper), 0644); err != nil {
		t.Fatal(err)
	}

	app := `
import { getHello } from "./helper.kitwork.js";
import { log } from "kitwork";
log.Print(getHello());
`
	if err := os.WriteFile(filepath.Join(tenantDir, "app.kitwork.js"), []byte(app), 0644); err != nil {
		t.Fatal(err)
	}

	if err := NewTenant(tmpDir, "localhost").Run(); err != nil {
		t.Fatalf("relative file import failed at runtime: %v", err)
	}
}
