package compiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTenant lays out module files in a temp dir and returns the entry path, main.work.
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
	return filepath.Join(dir, "main.work")
}

// nativeBundle must resolve relative imports by IIFE-wrapping each module —
// no ImportStatement left, no esbuild — and the result must compile to bytecode.
func TestNativeBundleResolvesRelative(t *testing.T) {
	entry := writeTenant(t, map[string]string{
		"main.work": `import http from "kitwork/http";
import "./routes/hello.work";
import { greet } from "./lib/greet.work";
const request = http.get("/x").header("X-Greeting", greet("x"));`,
		"routes/hello.work": `import http from "kitwork/http";
import { greet } from "../lib/greet.work";
const request = http.get("/hello").header("X-Greeting", greet("world"));`,
		"lib/greet.work": `export const greet = (name) => "Hello, " + name + "!";`,
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
	for _, want := range []string{"__kw_mod_", "= kitwork().http", "greet"} {
		if !strings.Contains(out, want) {
			t.Errorf("combined program missing %q\n---\n%s", want, out)
		}
	}

	// And it must compile to real bytecode natively (no esbuild involved).
	c := NewCompiler(string(content))
	if err := c.Compile(combined); err != nil {
		t.Fatalf("compile of natively-bundled program failed: %v", err)
	}
	if _, err := c.ByteCodeResult(); err != nil {
		t.Fatalf("program: %v", err)
	}
}

// `as` aliases (kitwork + relative) must compile through the whole native path.
func TestNativeAliasImportsCompile(t *testing.T) {
	entry := writeTenant(t, map[string]string{
		"main.work": `import { http as r, log as l } from "kitwork";
import { greet as g } from "./lib/greet.work";
const request = r.get("/x").header("X-Greeting", g("x"));
l.print("hi");`,
		"lib/greet.work": `export const greet = (name) => "Hi " + name;`,
	})
	bc, err := CompileFile(entry)
	if err != nil {
		t.Fatalf("Bytecode error: %v", err)
	}
	if bc == nil || bc.Program == nil || bc.Len() == 0 {
		t.Fatal("expected non-empty bytecode")
	}
}

// CompileFile() must drive the whole native path for a modular tenant.
func TestBytecodeNativeForModularTenant(t *testing.T) {
	entry := writeTenant(t, map[string]string{
		"main.work":      `import { greet } from "./lib/greet.work"; const msg = greet("kit");`,
		"lib/greet.work": `export const greet = (name) => "Hi " + name;`,
	})
	bc, err := CompileFile(entry)
	if err != nil {
		t.Fatalf("Bytecode error: %v", err)
	}
	if bc == nil || bc.Program == nil || bc.Len() == 0 {
		t.Fatal("expected non-empty bytecode")
	}
}

func TestNativeBundleDirectoryImport(t *testing.T) {
	entry := writeTenant(t, map[string]string{
		"main.work":     `import "./routes/";`,
		"routes/a.work": `import { log } from "kitwork"; log.print("route a loaded");`,
		"routes/b.work": `import { log } from "kitwork"; log.print("route b loaded");`,
		"routes/c.js":   `import { log } from "kitwork"; log.print("route c should not load");`,
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
	// Should contain messages from a.work and b.work
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
		"main.work":     `import { something } from "./routes/";`,
		"routes/a.work": `export const something = 123;`,
	})
	contentFail, _ := os.ReadFile(entryFail)
	progFail, _ := parseProgram(string(contentFail))
	_, _, errFail := nativeBundle(entryFail, progFail)
	if errFail == nil {
		t.Error("expected directory import with bindings to fail, but it succeeded")
	} else if !strings.Contains(errFail.Error(), `import "./routes/" là một thư mục`) {
		t.Errorf("expected side-effect error, got: %v", errFail)
	}
}

// import "./dir/" runs every .work file of the folder, as its error message says it does, and an import
// written without its .work finds the file (27/10/2026: only .kitwork.js members were read, and an
// import without a suffix tried .kitwork.js and .js, so both ran nothing of a .work app). A file of
// another kind in the folder is not run. CONTROL: the same folder read as text has all three files.
func TestNativeBundleReadsWorkModules(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"main.work": `import "./routes/";
import { greet } from "./lib/greet";
const said = greet("x");`,
		"routes/a.work":  `import { log } from "kitwork"; log.print("route a loaded");`,
		"routes/b.work":  `import { log } from "kitwork"; log.print("route b loaded");`,
		"routes/c.js":    `import { log } from "kitwork"; log.print("route c should not load");`,
		"lib/greet.work": `export const greet = (name) => "Hello, " + name + "!";`,
	}
	for rel, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	entry := filepath.Join(dir, "main.work")
	prog, err := parseProgram(files["main.work"])
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	combined, moduleFiles, err := nativeBundle(entry, prog)
	if err != nil {
		t.Fatalf("nativeBundle error: %v", err)
	}
	out := combined.String()
	for _, want := range []string{"route a loaded", "route b loaded", "Hello, "} {
		if !strings.Contains(out, want) {
			t.Errorf("the bundle misses %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "route c should not load") {
		t.Errorf("a .js file of the folder is not a module of the app:\n%s", out)
	}
	watched := strings.Join(moduleFiles, "\n")
	for _, want := range []string{"a.work", "b.work", "greet.work"} {
		if !strings.Contains(watched, want) {
			t.Errorf("hot reload must watch %s, it watches:\n%s", want, watched)
		}
	}
	if entries, _ := os.ReadDir(filepath.Join(dir, "routes")); len(entries) != 3 {
		t.Errorf("CONTROL: the folder holds three files, got %d", len(entries))
	}
}
