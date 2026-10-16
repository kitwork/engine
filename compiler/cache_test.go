package compiler

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
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

// The fingerprint every artifact cached by a schema-3 engine carries. Pinned, not computed from the
// constant, so this keeps meaning "written before the bump" whatever the constant becomes.
const schema3CompilerFingerprint = "f0c6c9255af80f7d2109348a23f49281ca50935b9bb8e41cbcf4970d7fcd4c07"

// bytecode2InstructionSetChecksum is the instruction contract of bytecode 2, which every engine before
// bytecode 3 (14/10/2026) stamped into its compiler fingerprint.
const bytecode2InstructionSetChecksum = "10872c964d1c5c284b8ec4fd1acc429e31568151dc03d17a77d1da76774c7e91"

// bytecode2FingerprintFor is the fingerprint a compiler of this schema stamped on bytecode 2.
func bytecode2FingerprintFor(schema uint16) [sha256.Size]byte {
	return compilerFingerprintWith(schema, 2, 1, bytecode2InstructionSetChecksum)
}

// bytecode3InstructionSetChecksum is the instruction contract of bytecode 3, which every engine from
// 14/10/2026 to bytecode 4 (15/10/2026, STEP) stamped into its compiler fingerprint.
const bytecode3InstructionSetChecksum = "f98baae0b65716b886b0786a06a9316ae7baf4ff7ab93b8cd87149a344df1678"

// bytecode3FingerprintFor is the fingerprint a compiler of this schema stamped on bytecode 3.
func bytecode3FingerprintFor(schema uint16) [sha256.Size]byte {
	return compilerFingerprintWith(schema, 3, 1, bytecode3InstructionSetChecksum)
}

// Schema 4 changed what some source compiles to (reserved words as keys). A cache a schema-3 engine
// wrote must never run: the current engine looks for it under another name, and an artifact with the
// schema-3 stamp is refused and replaced even when it sits under the current name.
func TestFileCacheRefusesArtifactsOfSchema3(t *testing.T) {
	schema3 := bytecode2FingerprintFor(3)
	if got := hex.EncodeToString(schema3[:]); got != schema3CompilerFingerprint {
		t.Fatalf("schema 3 on bytecode 2 stamps %s, want %s", got, schema3CompilerFingerprint)
	}
	if Fingerprint() == schema3CompilerFingerprint {
		t.Fatal("the current compiler still stamps the schema-3 fingerprint")
	}

	root := t.TempDir()
	entry := filepath.Join(root, "router.kitwork.js")
	if err := os.WriteFile(entry, []byte(`const o = { undefined: 1 }; const result = o.undefined;`), 0o644); err != nil {
		t.Fatal(err)
	}
	fresh, err := CompileFile(entry)
	if err != nil {
		t.Fatal(err)
	}
	freshData, err := fresh.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}

	// An artifact for the same source whose program is something else, so a test that loads it can
	// tell. stamped(fingerprint) is that artifact as a compiler with that fingerprint wrote it.
	other, err := CompileSource(`const o = { "null": 1 }; const result = o["null"] + 1;`)
	if err != nil {
		t.Fatal(err)
	}
	otherData, err := other.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if other.Checksum() == fresh.Checksum() {
		t.Fatal("the planted program must differ from the fresh one")
	}
	const compilerAt, sourceAt = 6, 6 + sha256.Size
	stamped := func(compilerDigest []byte) []byte {
		artifact := append([]byte(nil), otherData...)
		copy(artifact[compilerAt:sourceAt], compilerDigest)
		copy(artifact[sourceAt:sourceAt+sha256.Size], freshData[sourceAt:sourceAt+sha256.Size])
		return artifact
	}
	current := currentCompilerFingerprint
	cacheDirectory := filepath.Join(root, "cache")
	currentName := filepath.Join(cacheDirectory, fresh.CacheKey()+".kwbc")
	schema3Name := filepath.Join(cacheDirectory, cacheKeyFor(schema3CompilerFingerprint, fresh.SourceFingerprint())+".kwbc")
	if currentName == schema3Name {
		t.Fatal("schema 3 and the current schema look under the same cache name")
	}
	plant := func(name string, artifact []byte) {
		t.Helper()
		if err := os.MkdirAll(cacheDirectory, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, artifact, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Control: the same planted program with the current stamp is trusted and runs. This is what
	// proves the cache would use the schema-3 artifact below if only its stamp let it.
	plant(currentName, stamped(current[:]))
	loaded, err := NewFileCache(cacheDirectory).CompileFile(entry)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Checksum() != other.Checksum() {
		t.Fatal("control: an artifact with the current stamp was not loaded, so this test cannot see a load")
	}

	// The schema-3 artifact, under the name schema 3 used and under the current name.
	stale := stamped(schema3[:])
	if _, err := UnmarshalBytecode(stale, fresh.SourceFingerprint()); err == nil {
		t.Fatal("an artifact stamped by schema 3 decoded")
	}
	plant(schema3Name, stale)
	plant(currentName, stale)
	rebuilt, err := NewFileCache(cacheDirectory).CompileFile(entry)
	if err != nil {
		t.Fatal(err)
	}
	if rebuilt.Checksum() != fresh.Checksum() {
		t.Fatalf("program checksum = %s, want the fresh compile %s (a schema-3 artifact ran)", rebuilt.Checksum(), fresh.Checksum())
	}
	replaced, err := os.ReadFile(currentName)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(replaced[compilerAt:sourceAt], current[:]) {
		t.Fatal("the schema-3 artifact under the current name was not replaced by a current one")
	}
	if left, err := os.ReadFile(schema3Name); err != nil || !bytes.Equal(left, stale) {
		t.Fatalf("the artifact under the schema-3 name was read or rewritten: %v", err)
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
