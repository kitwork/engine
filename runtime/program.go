package runtime

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash"
	"math"

	"github.com/kitwork/engine/value"
)

// BytecodeVersion identifies the structural format understood by this runtime.
// Increment it when instruction encoding or constant semantics become incompatible.
const BytecodeVersion uint16 = 2

// Program is an immutable, verified unit of Kitwork execution.
//
// Its slices are private and copied at construction. Public accessors return
// snapshots; only the runtime hot path reads the owned storage directly.
type Program struct {
	code      []byte
	constants []value.Value
	debug     debugTable
	version   uint16
	checksum  [sha256.Size]byte
	profile   ProgramProfile
}

// NewProgram copies and verifies a complete program before publishing it.
func NewProgram(code []byte, constants []value.Value, sourceMap []int32) (*Program, error) {
	if len(sourceMap) != 0 && len(sourceMap) != len(code) {
		return nil, fmt.Errorf(
			"program source map has %d entries for %d bytecode bytes",
			len(sourceMap),
			len(code),
		)
	}
	return NewProgramWithDebug(code, constants, debugEntriesFromSourceMap(sourceMap))
}

// NewProgramWithDebug copies and verifies code, constants, and a compressed
// source-location table before publishing the Program.
func NewProgramWithDebug(
	code []byte,
	constants []value.Value,
	debugEntries []DebugEntry,
) (*Program, error) {
	ownedCode := append([]byte(nil), code...)
	ownedConstants := cloneConstants(constants)

	if err := validateProgramConstants(ownedConstants); err != nil {
		return nil, err
	}
	profile, err := verifyAndProfile(ownedCode, ownedConstants)
	if err != nil {
		return nil, err
	}
	debug, err := newDebugTable(len(ownedCode), debugEntries)
	if err != nil {
		return nil, err
	}

	program := &Program{
		code:      ownedCode,
		constants: ownedConstants,
		debug:     debug,
		version:   BytecodeVersion,
		profile:   profile,
	}
	program.checksum = programChecksum(program)
	return program, nil
}

// EmptyProgram returns a verified empty program for VM pools and uninitialized tenants.
func EmptyProgram() *Program {
	program, err := NewProgram(nil, nil, nil)
	if err != nil {
		panic(err)
	}
	return program
}

// ProgramVersion implements value.ProgramRef without exposing program storage.
func (p *Program) ProgramVersion() uint16 {
	if p == nil {
		return 0
	}
	return p.version
}

func (p *Program) Len() int {
	if p == nil {
		return 0
	}
	return len(p.code)
}

func (p *Program) Checksum() string {
	if p == nil {
		return ""
	}
	return hex.EncodeToString(p.checksum[:])
}

// ChecksumDigest returns the fixed-size Program identity without allocating or
// exposing internal storage. Runtime telemetry can use it as a map key without
// retaining the Program or its generation.
func (p *Program) ChecksumDigest() [sha256.Size]byte {
	if p == nil {
		return [sha256.Size]byte{}
	}
	return p.checksum
}

// Profile returns a detached static profile produced during bytecode
// verification. It performs no decoding and exposes no Program storage.
func (p *Program) Profile() ProgramProfile {
	if p == nil {
		return ProgramProfile{}
	}
	profile := p.profile
	profile.Opcodes = append([]OpcodeProfile(nil), p.profile.Opcodes...)
	return profile
}

// Instructions returns a copy suitable for diagnostics and serialization.
func (p *Program) Instructions() []byte {
	if p == nil {
		return nil
	}
	return append([]byte(nil), p.code...)
}

// Constants returns a detached copy suitable for diagnostics and serialization.
func (p *Program) Constants() []value.Value {
	if p == nil {
		return nil
	}
	return cloneConstants(p.constants)
}

// SourceMap returns a copy of the byte-offset to source-line mapping.
func (p *Program) SourceMap() []int32 {
	if p == nil {
		return nil
	}
	sourceMap := make([]int32, len(p.code))
	for ip := range sourceMap {
		sourceMap[ip] = p.debug.location(ip).Line
	}
	return sourceMap
}

// DebugEntries returns a detached snapshot of the compressed source table.
func (p *Program) DebugEntries() []DebugEntry {
	if p == nil {
		return nil
	}
	return p.debug.snapshot()
}

// SourceAt resolves the original source location for a bytecode offset.
func (p *Program) SourceAt(ip int) SourceLocation {
	if p == nil {
		return SourceLocation{}
	}
	return p.debug.location(ip)
}

// ProgramFromRef resolves the opaque handle stored by a detached lambda.
func ProgramFromRef(ref value.ProgramRef) (*Program, bool) {
	program, ok := ref.(*Program)
	return program, ok && program != nil
}

func cloneConstants(constants []value.Value) []value.Value {
	if constants == nil {
		return nil
	}
	cloned := make([]value.Value, len(constants))
	for i, constant := range constants {
		cloned[i] = cloneConstant(constant)
	}
	return cloned
}

func cloneConstant(constant value.Value) value.Value {
	cloned := constant
	switch payload := constant.V.(type) {
	case []byte:
		cloned.V = append([]byte(nil), payload...)
	case *value.Lambda:
		if payload == nil {
			cloned.V = (*value.Lambda)(nil)
			break
		}
		cloned.V = &value.Lambda{
			Address:      payload.Address,
			Name:         payload.Name,
			SourceFile:   payload.SourceFile,
			SourceLine:   payload.SourceLine,
			SourceColumn: payload.SourceColumn,
			Params:       append([]string(nil), payload.Params...),
		}
	case *[]value.Value:
		if payload == nil {
			cloned.V = (*[]value.Value)(nil)
			break
		}
		items := cloneConstants(*payload)
		cloned.V = &items
	case map[string]value.Value:
		items := make(map[string]value.Value, len(payload))
		for key, item := range payload {
			items[key] = cloneConstant(item)
		}
		cloned.V = items
	}
	return cloned
}

func validateProgramConstants(constants []value.Value) error {
	for index, constant := range constants {
		if constant.ErrorVal != nil {
			return fmt.Errorf(
				"program constant %d contains mutable error metadata",
				index,
			)
		}
		switch constant.K {
		case value.Nil, value.Number, value.Bool, value.Time, value.Duration:
			if constant.V != nil {
				return fmt.Errorf(
					"program constant %d has scalar kind %s with mutable payload %T",
					index,
					constant.K,
					constant.V,
				)
			}
		case value.String:
			if _, ok := constant.V.(string); !ok {
				return fmt.Errorf(
					"program constant %d has string kind with payload %T",
					index,
					constant.V,
				)
			}
		case value.Func:
			lambda, ok := constant.V.(*value.Lambda)
			if !ok || lambda == nil {
				return fmt.Errorf(
					"program constant %d must contain a lambda template, got %T",
					index,
					constant.V,
				)
			}
			if lambda.Scope != nil || lambda.Parent != nil || lambda.Program != nil {
				return fmt.Errorf("program constant %d contains a bound closure", index)
			}
		default:
			return fmt.Errorf(
				"program constant %d uses mutable or unsupported kind %s",
				index,
				constant.K,
			)
		}
	}
	return nil
}

func programChecksum(program *Program) [sha256.Size]byte {
	h := sha256.New()
	writeUint16(h, program.version)
	writeBytes(h, program.code)
	writeUint64(h, uint64(len(program.constants)))
	for _, constant := range program.constants {
		writeConstant(h, constant)
	}
	writeUint64(h, uint64(len(program.debug.files)))
	for _, file := range program.debug.files {
		writeBytes(h, []byte(file))
	}
	writeUint64(h, uint64(len(program.debug.entries)))
	for _, entry := range program.debug.entries {
		writeUint64(h, uint64(entry.ip))
		writeUint32(h, entry.fileID)
		writeUint32(h, uint32(entry.line))
		writeUint32(h, uint32(entry.column))
	}

	var checksum [sha256.Size]byte
	copy(checksum[:], h.Sum(nil))
	return checksum
}

func writeConstant(h hash.Hash, constant value.Value) {
	h.Write([]byte{byte(constant.K)})
	writeUint64(h, math.Float64bits(constant.N))
	if constant.IsError {
		h.Write([]byte{1})
	} else {
		h.Write([]byte{0})
	}

	switch payload := constant.V.(type) {
	case nil:
		h.Write([]byte{0})
	case string:
		h.Write([]byte{1})
		writeBytes(h, []byte(payload))
	case *value.Lambda:
		h.Write([]byte{2})
		if payload == nil {
			writeUint64(h, ^uint64(0))
			return
		}
		writeUint64(h, uint64(payload.Address))
		writeBytes(h, []byte(payload.Name))
		writeBytes(h, []byte(payload.SourceFile))
		writeUint32(h, uint32(payload.SourceLine))
		writeUint32(h, uint32(payload.SourceColumn))
		writeUint64(h, uint64(len(payload.Params)))
		for _, parameter := range payload.Params {
			writeBytes(h, []byte(parameter))
		}
	}
}

func writeBytes(h hash.Hash, data []byte) {
	writeUint64(h, uint64(len(data)))
	h.Write(data)
}

func writeUint16(h hash.Hash, number uint16) {
	var data [2]byte
	binary.BigEndian.PutUint16(data[:], number)
	h.Write(data[:])
}

func writeUint32(h hash.Hash, number uint32) {
	var data [4]byte
	binary.BigEndian.PutUint32(data[:], number)
	h.Write(data[:])
}

func writeUint64(h hash.Hash, number uint64) {
	var data [8]byte
	binary.BigEndian.PutUint64(data[:], number)
	h.Write(data[:])
}
