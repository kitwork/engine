package compiler

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"

	"github.com/kitwork/engine/runtime"
)

const (
	BytecodeArtifactVersion uint16 = 1
	MaxBytecodeArtifactSize        = runtime.MaxProgramBinarySize + 128
)

var bytecodeArtifactMagic = [4]byte{'K', 'W', 'B', 'C'}

// MarshalBinary serializes bytecode for a trusted local cache. Files are
// intentionally not persisted: the cache owner must validate its source
// manifest and attach the current dependency list before publication.
func (b *Bytecode) MarshalBinary() ([]byte, error) {
	if b == nil || b.Program == nil {
		return nil, fmt.Errorf("encode bytecode artifact: nil program")
	}
	compilerDigest, err := decodeFingerprint(b.compilerFingerprint)
	if err != nil {
		return nil, fmt.Errorf("encode bytecode artifact: compiler fingerprint: %w", err)
	}
	sourceDigest, err := decodeFingerprint(b.sourceFingerprint)
	if err != nil {
		return nil, fmt.Errorf("encode bytecode artifact: source fingerprint: %w", err)
	}
	programData, err := b.Program.MarshalBinary()
	if err != nil {
		return nil, err
	}

	data := make([]byte, 0, 4+2+sha256.Size*2+4+len(programData))
	data = append(data, bytecodeArtifactMagic[:]...)
	data = binary.BigEndian.AppendUint16(data, BytecodeArtifactVersion)
	data = append(data, compilerDigest[:]...)
	data = append(data, sourceDigest[:]...)
	data = binary.BigEndian.AppendUint32(data, uint32(len(programData)))
	data = append(data, programData...)
	return data, nil
}

// UnmarshalBytecode restores an artifact only when both the current compiler
// contract and the caller's expected source fingerprint match.
func UnmarshalBytecode(data []byte, expectedSourceFingerprint string) (*Bytecode, error) {
	if expectedSourceFingerprint == "" {
		return nil, fmt.Errorf("decode bytecode artifact: expected source fingerprint is required")
	}
	if len(data) > MaxBytecodeArtifactSize {
		return nil, fmt.Errorf(
			"decode bytecode artifact: %d bytes exceeds %d",
			len(data),
			MaxBytecodeArtifactSize,
		)
	}
	const headerSize = 4 + 2 + sha256.Size + sha256.Size + 4
	if len(data) < headerSize {
		return nil, fmt.Errorf("decode bytecode artifact: truncated header")
	}
	if !bytes.Equal(data[:4], bytecodeArtifactMagic[:]) {
		return nil, fmt.Errorf("decode bytecode artifact: invalid magic")
	}
	if version := binary.BigEndian.Uint16(data[4:6]); version != BytecodeArtifactVersion {
		return nil, fmt.Errorf(
			"decode bytecode artifact: version %d is incompatible with %d",
			version,
			BytecodeArtifactVersion,
		)
	}

	offset := 6
	var compilerDigest [sha256.Size]byte
	copy(compilerDigest[:], data[offset:offset+sha256.Size])
	offset += sha256.Size
	if compilerDigest != currentCompilerFingerprint {
		return nil, fmt.Errorf("decode bytecode artifact: compiler fingerprint mismatch")
	}
	var sourceDigest [sha256.Size]byte
	copy(sourceDigest[:], data[offset:offset+sha256.Size])
	offset += sha256.Size
	if expectedSourceFingerprint != "" {
		expectedDigest, err := decodeFingerprint(expectedSourceFingerprint)
		if err != nil {
			return nil, fmt.Errorf("decode bytecode artifact: expected source fingerprint: %w", err)
		}
		if sourceDigest != expectedDigest {
			return nil, fmt.Errorf("decode bytecode artifact: source fingerprint mismatch")
		}
	}

	programLength := int(binary.BigEndian.Uint32(data[offset : offset+4]))
	offset += 4
	if programLength != len(data)-offset {
		return nil, fmt.Errorf(
			"decode bytecode artifact: program length %d does not match payload %d",
			programLength,
			len(data)-offset,
		)
	}
	program, err := runtime.UnmarshalProgram(data[offset:])
	if err != nil {
		return nil, err
	}
	return &Bytecode{
		Program:             program,
		compilerFingerprint: Fingerprint(),
		sourceFingerprint:   hex.EncodeToString(sourceDigest[:]),
	}, nil
}

// ValidateArtifact proves that bytecode produced by the current compiler can
// survive the complete local-cache boundary and retain its executable
// identity. Release and deployment checks use this compatibility gate.
func ValidateArtifact(bytecode *Bytecode) error {
	if bytecode == nil || bytecode.Program == nil {
		return fmt.Errorf("validate bytecode artifact: nil program")
	}
	data, err := bytecode.MarshalBinary()
	if err != nil {
		return err
	}
	restored, err := UnmarshalBytecode(data, bytecode.SourceFingerprint())
	if err != nil {
		return err
	}
	reencoded, err := restored.MarshalBinary()
	if err != nil {
		return err
	}
	if !bytes.Equal(data, reencoded) {
		return fmt.Errorf("validate bytecode artifact: round-trip is not deterministic")
	}
	if restored.Checksum() != bytecode.Checksum() {
		return fmt.Errorf("validate bytecode artifact: program checksum changed")
	}
	if restored.CompilerFingerprint() != bytecode.CompilerFingerprint() ||
		restored.SourceFingerprint() != bytecode.SourceFingerprint() ||
		restored.CacheKey() != bytecode.CacheKey() {
		return fmt.Errorf("validate bytecode artifact: artifact identity changed")
	}
	return nil
}

func decodeFingerprint(fingerprint string) ([sha256.Size]byte, error) {
	var digest [sha256.Size]byte
	decoded, err := hex.DecodeString(fingerprint)
	if err != nil {
		return digest, err
	}
	if len(decoded) != sha256.Size {
		return digest, fmt.Errorf("got %d bytes, want %d", len(decoded), sha256.Size)
	}
	copy(digest[:], decoded)
	return digest, nil
}
