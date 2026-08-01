package compiler

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFileCacheLoadsAndRepairsArtifacts(t *testing.T) {
	root := t.TempDir()
	entry := filepath.Join(root, "router.kitwork.js")
	if err := os.WriteFile(entry, []byte(`const answer = 42;`), 0o644); err != nil {
		t.Fatal(err)
	}
	cache := NewFileCache(filepath.Join(root, "cache"))

	first, err := cache.CompileFile(entry)
	if err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(root, "cache", first.CacheKey()+".kwbc")
	if _, err := os.Stat(filename); err != nil {
		t.Fatalf("cache artifact was not written: %v", err)
	}

	second, err := cache.CompileFile(entry)
	if err != nil {
		t.Fatal(err)
	}
	if second.Checksum() != first.Checksum() ||
		second.SourceFingerprint() != first.SourceFingerprint() {
		t.Fatal("cache hit changed executable identity")
	}
	if len(second.Files) != 1 || second.Files[0] != entry {
		t.Fatalf("cache hit dependency files = %#v", second.Files)
	}

	if err := os.WriteFile(filename, []byte("corrupt"), 0o644); err != nil {
		t.Fatal(err)
	}
	repaired, err := cache.CompileFile(entry)
	if err != nil {
		t.Fatal(err)
	}
	if repaired.Checksum() != first.Checksum() {
		t.Fatal("repair changed executable identity")
	}
	if data, err := os.ReadFile(filename); err != nil || string(data) == "corrupt" {
		t.Fatalf("corrupt artifact was not replaced: %v", err)
	}
}

func TestFileCacheInvalidatesOnSourceChange(t *testing.T) {
	root := t.TempDir()
	entry := filepath.Join(root, "router.kitwork.js")
	cacheDirectory := filepath.Join(root, "cache")
	cache := NewFileCache(cacheDirectory)

	if err := os.WriteFile(entry, []byte(`const answer = 42;`), 0o644); err != nil {
		t.Fatal(err)
	}
	first, err := cache.CompileFile(entry)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entry, []byte(`const answer = 43;`), 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := cache.CompileFile(entry)
	if err != nil {
		t.Fatal(err)
	}

	if first.SourceFingerprint() == second.SourceFingerprint() ||
		first.CacheKey() == second.CacheKey() ||
		first.Checksum() == second.Checksum() {
		t.Fatal("source edit did not invalidate bytecode artifact")
	}
	entries, err := os.ReadDir(cacheDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("cache artifact count = %d, want 2", len(entries))
	}
}

func TestFileCacheInvalidatesOnNativeImportChange(t *testing.T) {
	root := t.TempDir()
	entry := filepath.Join(root, "router.kitwork.js")
	module := filepath.Join(root, "answer.kitwork.js")
	cache := NewFileCache(filepath.Join(root, "cache"))

	if err := os.WriteFile(
		entry,
		[]byte(`import { answer } from "./answer.kitwork.js"; const result = answer;`),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(module, []byte(`export const answer = 42;`), 0o644); err != nil {
		t.Fatal(err)
	}
	first, err := cache.CompileFile(entry)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(module, []byte(`export const answer = 43;`), 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := cache.CompileFile(entry)
	if err != nil {
		t.Fatal(err)
	}

	if first.SourceFingerprint() == second.SourceFingerprint() ||
		first.CacheKey() == second.CacheKey() {
		t.Fatal("native import edit did not invalidate bytecode artifact")
	}
}

func TestValidateArtifactRoundTrip(t *testing.T) {
	bytecode, err := CompileSource(`const answer = 6 * 7;`)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateArtifact(bytecode); err != nil {
		t.Fatalf("compatible artifact rejected: %v", err)
	}
	if err := ValidateArtifact(nil); err == nil {
		t.Fatal("nil artifact passed compatibility validation")
	}
}
