package runtime

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"

	"github.com/kitwork/engine/value"
)

const (
	// ProgramEncodingVersion identifies the binary envelope around a Program.
	// It is independent from BytecodeVersion so storage framing can evolve
	// without claiming that opcode semantics changed.
	ProgramEncodingVersion uint16 = 1
	MaxProgramBinarySize          = 16 << 20
)

var programBinaryMagic = [4]byte{'K', 'W', 'P', 'B'}

// MarshalBinary serializes a verified Program for a trusted local cache.
// Loading always verifies the envelope, bytecode version, checksum, constants,
// debug table, and instruction stream again before returning an executable.
func (p *Program) MarshalBinary() ([]byte, error) {
	if p == nil {
		return nil, fmt.Errorf("encode program: nil program")
	}

	data := make([]byte, 0, 64+len(p.code))
	data = append(data, programBinaryMagic[:]...)
	data = appendUint16(data, ProgramEncodingVersion)
	data = appendUint16(data, p.version)
	data = append(data, p.checksum[:]...)
	data = appendUint32(data, uint32(len(p.code)))
	data = appendUint32(data, uint32(len(p.constants)))
	data = appendUint32(data, uint32(len(p.debug.entries)))
	data = append(data, p.code...)

	var err error
	for _, constant := range p.constants {
		data, err = appendBinaryConstant(data, constant)
		if err != nil {
			return nil, err
		}
	}
	for _, entry := range p.DebugEntries() {
		data = appendUint32(data, uint32(entry.IP))
		data, err = appendBinaryString(data, entry.File)
		if err != nil {
			return nil, err
		}
		data = appendUint32(data, uint32(entry.Line))
		data = appendUint32(data, uint32(entry.Column))
	}

	if len(data) > MaxProgramBinarySize {
		return nil, fmt.Errorf(
			"encode program: %d bytes exceeds %d",
			len(data),
			MaxProgramBinarySize,
		)
	}
	return data, nil
}

// UnmarshalProgram restores a local cached Program. The returned Program is a
// new immutable owner; no slice or object points into data.
func UnmarshalProgram(data []byte) (*Program, error) {
	if len(data) > MaxProgramBinarySize {
		return nil, fmt.Errorf(
			"decode program: %d bytes exceeds %d",
			len(data),
			MaxProgramBinarySize,
		)
	}

	reader := programBinaryReader{data: data}
	magic, err := reader.bytes(len(programBinaryMagic))
	if err != nil || !bytes.Equal(magic, programBinaryMagic[:]) {
		return nil, fmt.Errorf("decode program: invalid magic")
	}
	encodingVersion, err := reader.uint16()
	if err != nil {
		return nil, err
	}
	if encodingVersion != ProgramEncodingVersion {
		return nil, fmt.Errorf(
			"decode program: encoding version %d is incompatible with %d",
			encodingVersion,
			ProgramEncodingVersion,
		)
	}
	bytecodeVersion, err := reader.uint16()
	if err != nil {
		return nil, err
	}
	if bytecodeVersion != BytecodeVersion {
		return nil, fmt.Errorf(
			"decode program: bytecode version %d is incompatible with %d",
			bytecodeVersion,
			BytecodeVersion,
		)
	}
	expectedChecksumBytes, err := reader.bytes(len([32]byte{}))
	if err != nil {
		return nil, err
	}
	var expectedChecksum [32]byte
	copy(expectedChecksum[:], expectedChecksumBytes)

	codeLength, err := reader.uint32()
	if err != nil {
		return nil, err
	}
	constantCount, err := reader.uint32()
	if err != nil {
		return nil, err
	}
	debugCount, err := reader.uint32()
	if err != nil {
		return nil, err
	}
	if codeLength > MaxBytecodeSize {
		return nil, fmt.Errorf("decode program: bytecode length %d exceeds %d", codeLength, MaxBytecodeSize)
	}
	if constantCount > MaxConstants {
		return nil, fmt.Errorf("decode program: constant count %d exceeds %d", constantCount, MaxConstants)
	}
	if uint64(debugCount) > uint64(codeLength) {
		return nil, fmt.Errorf(
			"decode program: debug entry count %d exceeds bytecode length %d",
			debugCount,
			codeLength,
		)
	}

	codeBytes, err := reader.bytes(int(codeLength))
	if err != nil {
		return nil, err
	}
	code := append([]byte(nil), codeBytes...)
	constants := make([]value.Value, int(constantCount))
	for index := range constants {
		constants[index], err = reader.constant()
		if err != nil {
			return nil, fmt.Errorf("decode program constant %d: %w", index, err)
		}
	}
	debugEntries := make([]DebugEntry, int(debugCount))
	for index := range debugEntries {
		ip, readErr := reader.uint32()
		if readErr != nil {
			return nil, fmt.Errorf("decode program debug entry %d: %w", index, readErr)
		}
		file, readErr := reader.string()
		if readErr != nil {
			return nil, fmt.Errorf("decode program debug entry %d: %w", index, readErr)
		}
		line, readErr := reader.uint32()
		if readErr != nil {
			return nil, fmt.Errorf("decode program debug entry %d: %w", index, readErr)
		}
		column, readErr := reader.uint32()
		if readErr != nil {
			return nil, fmt.Errorf("decode program debug entry %d: %w", index, readErr)
		}
		debugEntries[index] = DebugEntry{
			IP: int(ip),
			SourceLocation: SourceLocation{
				File:   file,
				Line:   int32(line),
				Column: int32(column),
			},
		}
	}
	if reader.remaining() != 0 {
		return nil, fmt.Errorf("decode program: %d trailing bytes", reader.remaining())
	}

	program, err := NewProgramWithDebug(code, constants, debugEntries)
	if err != nil {
		return nil, fmt.Errorf("decode program: %w", err)
	}
	if program.checksum != expectedChecksum {
		return nil, fmt.Errorf("decode program: checksum mismatch")
	}
	return program, nil
}

func appendBinaryConstant(data []byte, constant value.Value) ([]byte, error) {
	data = append(data, byte(constant.K))
	var flags byte
	if constant.IsError {
		flags |= 1
	}
	data = append(data, flags)
	data = appendUint64(data, math.Float64bits(constant.N))

	switch constant.K {
	case value.Nil, value.Number, value.Bool, value.Time, value.Duration:
		return data, nil
	case value.String:
		text, ok := constant.V.(string)
		if !ok {
			return nil, fmt.Errorf("encode program: string constant has payload %T", constant.V)
		}
		return appendBinaryString(data, text)
	case value.Func:
		lambda, ok := constant.V.(*value.Lambda)
		if !ok || lambda == nil {
			return nil, fmt.Errorf("encode program: function constant has payload %T", constant.V)
		}
		data = appendUint32(data, uint32(int32(lambda.Address)))
		var err error
		data, err = appendBinaryString(data, lambda.Name)
		if err != nil {
			return nil, err
		}
		data, err = appendBinaryString(data, lambda.SourceFile)
		if err != nil {
			return nil, err
		}
		data = appendUint32(data, uint32(lambda.SourceLine))
		data = appendUint32(data, uint32(lambda.SourceColumn))
		if len(lambda.Params) > math.MaxUint16 {
			return nil, fmt.Errorf("encode program: lambda has %d parameters", len(lambda.Params))
		}
		data = appendUint16(data, uint16(len(lambda.Params)))
		for _, parameter := range lambda.Params {
			data, err = appendBinaryString(data, parameter)
			if err != nil {
				return nil, err
			}
		}
		return data, nil
	default:
		return nil, fmt.Errorf("encode program: unsupported constant kind %s", constant.K)
	}
}

func appendBinaryString(data []byte, text string) ([]byte, error) {
	if uint64(len(text)) > math.MaxUint32 ||
		len(text) > MaxProgramBinarySize ||
		len(data) > MaxProgramBinarySize-4-len(text) {
		return nil, fmt.Errorf("encode program: string is too large")
	}
	data = appendUint32(data, uint32(len(text)))
	data = append(data, text...)
	return data, nil
}

func appendUint16(data []byte, number uint16) []byte {
	return binary.BigEndian.AppendUint16(data, number)
}

func appendUint32(data []byte, number uint32) []byte {
	return binary.BigEndian.AppendUint32(data, number)
}

func appendUint64(data []byte, number uint64) []byte {
	return binary.BigEndian.AppendUint64(data, number)
}

type programBinaryReader struct {
	data   []byte
	offset int
}

func (r *programBinaryReader) remaining() int {
	return len(r.data) - r.offset
}

func (r *programBinaryReader) bytes(length int) ([]byte, error) {
	if length < 0 || length > r.remaining() {
		return nil, fmt.Errorf(
			"decode program: truncated payload at byte %d, need %d bytes",
			r.offset,
			length,
		)
	}
	start := r.offset
	r.offset += length
	return r.data[start:r.offset], nil
}

func (r *programBinaryReader) uint16() (uint16, error) {
	data, err := r.bytes(2)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint16(data), nil
}

func (r *programBinaryReader) uint32() (uint32, error) {
	data, err := r.bytes(4)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(data), nil
}

func (r *programBinaryReader) uint64() (uint64, error) {
	data, err := r.bytes(8)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint64(data), nil
}

func (r *programBinaryReader) string() (string, error) {
	length, err := r.uint32()
	if err != nil {
		return "", err
	}
	data, err := r.bytes(int(length))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (r *programBinaryReader) constant() (value.Value, error) {
	header, err := r.bytes(2)
	if err != nil {
		return value.Value{}, err
	}
	kind := value.Kind(header[0])
	if header[1]&^byte(1) != 0 {
		return value.Value{}, fmt.Errorf("unsupported flags 0x%x", header[1])
	}
	number, err := r.uint64()
	if err != nil {
		return value.Value{}, err
	}
	constant := value.Value{
		K:       kind,
		N:       math.Float64frombits(number),
		IsError: header[1]&1 != 0,
	}

	switch kind {
	case value.Nil, value.Number, value.Bool, value.Time, value.Duration:
		return constant, nil
	case value.String:
		constant.V, err = r.string()
		return constant, err
	case value.Func:
		address, readErr := r.uint32()
		if readErr != nil {
			return value.Value{}, readErr
		}
		name, readErr := r.string()
		if readErr != nil {
			return value.Value{}, readErr
		}
		sourceFile, readErr := r.string()
		if readErr != nil {
			return value.Value{}, readErr
		}
		sourceLine, readErr := r.uint32()
		if readErr != nil {
			return value.Value{}, readErr
		}
		sourceColumn, readErr := r.uint32()
		if readErr != nil {
			return value.Value{}, readErr
		}
		parameterCount, readErr := r.uint16()
		if readErr != nil {
			return value.Value{}, readErr
		}
		parameters := make([]string, int(parameterCount))
		for index := range parameters {
			parameters[index], readErr = r.string()
			if readErr != nil {
				return value.Value{}, readErr
			}
		}
		constant.V = &value.Lambda{
			Address:      int(int32(address)),
			Name:         name,
			SourceFile:   sourceFile,
			SourceLine:   int32(sourceLine),
			SourceColumn: int32(sourceColumn),
			Params:       parameters,
		}
		return constant, nil
	default:
		return value.Value{}, fmt.Errorf("unsupported constant kind %s", kind)
	}
}
