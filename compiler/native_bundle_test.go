package compiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	
)

// writeTenant lays out module files in a temp dir and returns the entry path.
func writeTenant(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, "app.kitwork.js")
}

// nativeBundle must resolve relative imports by IIFE-wrapping each module —
// no ImportStatement left, no esbuild — and the result must compile to bytecode.
func TestNativeBundleResolvesRelative(t *testing.T) {
	entry := writeTenant(t, map[string]string{
		"app.kitwork.js": `import router from "kitwork/router";
import "./routes/hello.kitwork.js";
import { greet } from "./lib/greet.kitwork.js";
router.get("/x").handle((res) => { return res.text(greet("x")); });`,
		"routes/hello.kitwork.js": `import router from "kitwork/router";
import { greet } from "../lib/greet.kitwork.js";
router.get("/hello").handle((res) => { return res.text(greet("world")); });`,
		"lib/greet.kitwork.js": `export const greet = (name) => "Hello, " + name + "!";`,
	})

	content, err := os.ReadFile(entry)
	if err != nil {
		t.Fatal(err)
	}
	prog, perr := parseProgram(string(content))
	if perr != nil {
		t.Fatalf("native parse failed: %v", perr)
	}
	if !hasRelativeImports(prog) {
		t.Fatal("expected entry to carry relative ImportStatement nodes")
	}

	combined, moduleFiles, err := nativeBundle(entry, prog)
	if err != nil {
		t.Fatalf("nativeBundle error: %v", err)
	}
	if len(moduleFiles) == 0 {
		t.Fatal("nativeBundle should report the bundled module files (hot reload watches them)")
	}

	// No unresolved imports should remain.
	for _, s := range combined.Statements {
		if _, ok := s.(*ImportStatement); ok {
			t.Fatalf("ImportStatement survived bundling: %s", s.String())
		}
	}

	// The wrapped module + binding must be present (proves IIFE strategy).
	out := combined.String()
	for _, want := range []string{"__kw_mod_", "= kitwork().router", "greet"} {
		if !strings.Contains(out, want) {
			t.Errorf("combined program missing %q\n---\n%s", want, out)
		}
	}

	// And it must compile to real bytecode natively (no esbuild involved).
	c := NewCompiler(string(content))
	if err := c.Compile(combined); err != nil {
		t.Fatalf("compile of natively-bundled program failed: %v", err)
	}
	if c.ByteCodeResult() == nil {
		t.Fatal("nil bytecode")
	}
}

// `as` aliases (kitwork + relative) must compile through the whole native path.
func TestNativeAliasImportsCompile(t *testing.T) {
	entry := writeTenant(t, map[string]string{
		"app.kitwork.js": `import { router as r, log as l } from "kitwork";
import { greet as g } from "./lib/greet.kitwork.js";
r.get("/x").handle((res) => { l("hi"); return res.text(g("x")); });`,
		"lib/greet.kitwork.js": `export const greet = (name) => "Hi " + name;`,
	})
	bc, err := CompileFile(entry)
	if err != nil {
		t.Fatalf("Bytecode error: %v", err)
	}
	if bc == nil || len(bc.Instructions) == 0 {
		t.Fatal("expected non-empty bytecode")
	}
}

// CompileFile() must drive the whole native path for a modular tenant.
func TestBytecodeNativeForModularTenant(t *testing.T) {
	entry := writeTenant(t, map[string]string{
		"app.kitwork.js":       `import { greet } from "./lib/greet.kitwork.js"; const msg = greet("kit");`,
		"lib/greet.kitwork.js": `export const greet = (name) => "Hi " + name;`,
	})
	bc, err := CompileFile(entry)
	if err != nil {
		t.Fatalf("Bytecode error: %v", err)
	}
	if bc == nil || len(bc.Instructions) == 0 {
		t.Fatal("expected non-empty bytecode")
	}
}

func TestNativeBundleDirectoryImport(t *testing.T) {
	entry := writeTenant(t, map[string]string{
		"app.kitwork.js": `import "./routes/";`,
		"routes/a.kitwork.js": `import { log } from "kitwork"; log.print("route a loaded");`,
		"routes/b.kitwork.js": `import { log } from "kitwork"; log.print("route b loaded");`,
		"routes/c.js": `import { log } from "kitwork"; log.print("route c should not load");`,
	})

	content, err := os.ReadFile(entry)
	if err != nil {
		t.Fatal(err)
	}
	prog, perr := parseProgram(string(content))
	if perr != nil {
		t.Fatalf("parse failed: %v", perr)
	}

	combined, moduleFiles, err := nativeBundle(entry, prog)
	if err != nil {
		t.Fatalf("nativeBundle error: %v", err)
	}
	if len(moduleFiles) == 0 {
		t.Fatal("nativeBundle should report the bundled module files (hot reload watches them)")
	}

	out := combined.String()
	// Should contain messages from a.kitwork.js and b.kitwork.js
	if !strings.Contains(out, "route a loaded") {
		t.Errorf("expected route a to be bundled, got: %s", out)
	}
	if !strings.Contains(out, "route b loaded") {
		t.Errorf("expected route b to be bundled, got: %s", out)
	}
	// Should NOT contain message from c.js
	if strings.Contains(out, "route c should not load") {
		t.Errorf("expected route c (c.js) to be ignored, got: %s", out)
	}

	// Verify that named imports from directory fail compilation
	entryFail := writeTenant(t, map[string]string{
		"app.kitwork.js": `import { something } from "./routes/";`,
		"routes/a.kitwork.js": `export const something = 123;`,
	})
	contentFail, _ := os.ReadFile(entryFail)
	progFail, _ := parseProgram(string(contentFail))
	_, _, errFail := nativeBundle(entryFail, progFail)
	if errFail == nil {
		t.Error("expected directory import with bindings to fail, but it succeeded")
	} else if !strings.Contains(errFail.Error(), "must be a side-effect import") {
		t.Errorf("expected side-effect error, got: %v", errFail)
	}
}

