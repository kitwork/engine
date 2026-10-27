package compiler

import (
	"os"
	"path/filepath"
	"testing"
)

func mustWrite(tb testing.TB, path, content string) {
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		tb.Fatal(err)
	}
}

// Baseline: a single-file script with no imports (native, no esbuild).
func BenchmarkCompile_NoImports(b *testing.B) {
	dir := b.TempDir()
	entry := filepath.Join(dir, "main.work")
	mustWrite(b, entry, `const x = 1 + 2; const y = x * 10; const z = y + x;`)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := CompileFile(entry); err != nil {
			b.Fatal(err)
		}
	}
}

// Native path: `import … from "kitwork"` lowered to kitwork() — NO esbuild.
func BenchmarkCompile_NativeKitworkImport(b *testing.B) {
	dir := b.TempDir()
	entry := filepath.Join(dir, "main.work")
	mustWrite(b, entry, `import { richtext, log, http, database } from "kitwork";`+"\n"+
		`const a = richtext; const b = log; const c = http; const d = database;`)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := CompileFile(entry); err != nil {
			b.Fatal(err)
		}
	}
}

// Native path: a relative import is resolved by the native IIFE bundler — NO
// esbuild. (Was ~3,200µs via esbuild before; now ~177µs.)
func BenchmarkCompile_NativeRelativeImport(b *testing.B) {
	dir := b.TempDir()
	helper := filepath.Join(dir, "helper.work")
	entry := filepath.Join(dir, "main.work")
	mustWrite(b, helper, `export const getHello = () => "Hello from modular helper!";`)
	mustWrite(b, entry, `import { getHello } from "./helper.work";`+"\n"+
		`const greeting = getHello();`)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := CompileFile(entry); err != nil {
			b.Fatal(err)
		}
	}
}

// Native path: `as` aliases lower to member bindings (was esbuild-only before).
func BenchmarkCompile_NativeAliasImport(b *testing.B) {
	dir := b.TempDir()
	entry := filepath.Join(dir, "main.work")
	mustWrite(b, entry, `import { http as r, log as l } from "kitwork";`+"\n"+
		`const a = r; const b = l;`)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := CompileFile(entry); err != nil {
			b.Fatal(err)
		}
	}
}
