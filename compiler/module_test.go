package compiler

import (
	"os"
	"path/filepath"
	"testing"
)

// Proves a file can import another file that exports (app imports helper).
// Today this resolves via the esbuild fallback (relative module).
func TestRelativeImportAnotherFile(t *testing.T) {
	dir := t.TempDir()
	helper := filepath.Join(dir, "helper.kitwork.js")
	entry := filepath.Join(dir, "app.kitwork.js")

	if err := os.WriteFile(helper, []byte(
		`export const getHello = () => "Hello from modular helper!";`,
	), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entry, []byte(
		`import { getHello } from "./helper.kitwork.js";`+"\n"+
			`const greeting = getHello();`,
	), 0644); err != nil {
		t.Fatal(err)
	}

	bc, err := CompileFile(entry)
	if err != nil {
		t.Fatalf("importing another file failed: %v", err)
	}
	if bc == nil {
		t.Fatalf("nil bytecode")
	}
}
