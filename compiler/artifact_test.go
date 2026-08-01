package compiler

import (
	"strings"
	"testing"

	"github.com/kitwork/engine/runtime"
)

func TestBytecodeArtifactRoundTrip(t *testing.T) {
	bytecode, err := CompileSource(`
const answer = (input) => input + 2;
const result = answer(40);
`)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := bytecode.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := UnmarshalBytecode(encoded, bytecode.SourceFingerprint())
	if err != nil {
		t.Fatal(err)
	}

	if restored.Program == bytecode.Program {
		t.Fatal("artifact reused original Program owner")
	}
	if restored.CompilerFingerprint() != Fingerprint() ||
		restored.SourceFingerprint() != bytecode.SourceFingerprint() ||
		restored.CacheKey() != bytecode.CacheKey() ||
		restored.Checksum() != bytecode.Checksum() {
		t.Fatalf(
			"artifact metadata differs: compiler=%q source=%q key=%q",
			restored.CompilerFingerprint(),
			restored.SourceFingerprint(),
			restored.CacheKey(),
		)
	}
	vm := runtime.New(restored.Program)
	if result := vm.Run(); result.K == 0 {
		t.Fatalf("restored execution failed = %#v", result)
	}
	if got := vm.Vars["result"].Int(); got != 42 {
		t.Fatalf("restored result variable = %d", got)
	}
}

func TestBytecodeCacheIdentityChangesWithSource(t *testing.T) {
	first, err := CompileSource(`const answer = 41 + 1;`)
	if err != nil {
		t.Fatal(err)
	}
	same, err := CompileSource(`const answer = 41 + 1;`)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := CompileSource(`const answer = 40 + 3;`)
	if err != nil {
		t.Fatal(err)
	}

	if first.CompilerFingerprint() == "" ||
		first.CompilerFingerprint() != same.CompilerFingerprint() {
		t.Fatal("compiler fingerprint is empty or unstable")
	}
	if first.SourceFingerprint() != same.SourceFingerprint() ||
		first.CacheKey() != same.CacheKey() {
		t.Fatal("equivalent sources produced unstable cache identity")
	}
	if first.SourceFingerprint() == changed.SourceFingerprint() ||
		first.CacheKey() == changed.CacheKey() {
		t.Fatal("source change did not invalidate cache identity")
	}
}

func TestBytecodeArtifactRejectsStaleCompilerAndSource(t *testing.T) {
	bytecode, err := CompileSource(`const answer = 42;`)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := bytecode.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}

	staleCompiler := append([]byte(nil), encoded...)
	staleCompiler[6] ^= 0xff
	if _, err := UnmarshalBytecode(staleCompiler, bytecode.SourceFingerprint()); err == nil ||
		!strings.Contains(err.Error(), "compiler fingerprint mismatch") {
		t.Fatalf("stale compiler error = %v", err)
	}

	changed, err := CompileSource(`const answer = 43;`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := UnmarshalBytecode(encoded, changed.SourceFingerprint()); err == nil ||
		!strings.Contains(err.Error(), "source fingerprint mismatch") {
		t.Fatalf("stale source error = %v", err)
	}
	if _, err := UnmarshalBytecode(encoded, ""); err == nil ||
		!strings.Contains(err.Error(), "source fingerprint is required") {
		t.Fatalf("missing source fingerprint error = %v", err)
	}
}

func FuzzUnmarshalBytecode(f *testing.F) {
	bytecode, err := CompileSource(`const answer = 42;`)
	if err != nil {
		f.Fatal(err)
	}
	encoded, err := bytecode.MarshalBinary()
	if err != nil {
		f.Fatal(err)
	}
	f.Add(encoded, bytecode.SourceFingerprint())
	f.Add([]byte("KWBC"), bytecode.SourceFingerprint())

	f.Fuzz(func(t *testing.T, data []byte, sourceFingerprint string) {
		restored, err := UnmarshalBytecode(data, sourceFingerprint)
		if err != nil {
			return
		}
		if restored == nil ||
			restored.Program == nil ||
			restored.CompilerFingerprint() != Fingerprint() ||
			!strings.EqualFold(restored.SourceFingerprint(), sourceFingerprint) {
			t.Fatalf("accepted invalid artifact: %#v", restored)
		}
	})
}
