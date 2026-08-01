package compiler

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"sort"

	"github.com/kitwork/engine/runtime"
)

// CompilerSchemaVersion is the explicit compiler-cache contract. Increment it
// whenever lowering or constant semantics change without an incompatible
// opcode encoding change.
const CompilerSchemaVersion uint16 = 1

var currentCompilerFingerprint = buildCompilerFingerprint()

// Fingerprint identifies the compiler and runtime contract that produced an
// artifact. It is stable across processes running the same engine build.
func Fingerprint() string {
	return hex.EncodeToString(currentCompilerFingerprint[:])
}

// CompilerFingerprint reports the producer contract recorded on this
// Bytecode value.
func (b *Bytecode) CompilerFingerprint() string {
	if b == nil {
		return ""
	}
	return b.compilerFingerprint
}

// SourceFingerprint reports a deterministic hash of every bundled source name
// and byte used by the compiler.
func (b *Bytecode) SourceFingerprint() string {
	if b == nil {
		return ""
	}
	return b.sourceFingerprint
}

// CacheKey combines source and compiler identity. A bytecode version,
// instruction contract, compiler schema, or source change produces a new key.
func (b *Bytecode) CacheKey() string {
	if b == nil || b.sourceFingerprint == "" {
		return ""
	}
	return cacheKeyForSource(b.sourceFingerprint)
}

func cacheKeyForSource(sourceFingerprint string) string {
	hash := sha256.New()
	hash.Write([]byte("kitwork-bytecode-cache"))
	hash.Write([]byte{0})
	hash.Write([]byte(Fingerprint()))
	hash.Write([]byte{0})
	hash.Write([]byte(sourceFingerprint))
	return hex.EncodeToString(hash.Sum(nil))
}

func buildCompilerFingerprint() [sha256.Size]byte {
	hash := sha256.New()
	hash.Write([]byte("kitwork-compiler"))
	var version [2]byte
	binary.BigEndian.PutUint16(version[:], CompilerSchemaVersion)
	hash.Write(version[:])
	binary.BigEndian.PutUint16(version[:], runtime.BytecodeVersion)
	hash.Write(version[:])
	binary.BigEndian.PutUint16(version[:], runtime.ProgramEncodingVersion)
	hash.Write(version[:])
	hash.Write([]byte(runtime.InstructionSetChecksum()))

	var fingerprint [sha256.Size]byte
	copy(fingerprint[:], hash.Sum(nil))
	return fingerprint
}

func fingerprintSources(sources map[string]string) string {
	names := make([]string, 0, len(sources))
	for name := range sources {
		names = append(names, name)
	}
	sort.Strings(names)

	hash := sha256.New()
	var length [8]byte
	for _, name := range names {
		binary.BigEndian.PutUint64(length[:], uint64(len(name)))
		hash.Write(length[:])
		hash.Write([]byte(name))
		source := sources[name]
		binary.BigEndian.PutUint64(length[:], uint64(len(source)))
		hash.Write(length[:])
		hash.Write([]byte(source))
	}
	return hex.EncodeToString(hash.Sum(nil))
}
