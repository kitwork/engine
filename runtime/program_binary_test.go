package runtime

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/kitwork/engine/value"
)

func TestProgramBinaryGolden(t *testing.T) {
	program, err := NewProgram(
		[]byte{byte(PUSH), 0, 0, byte(RETURN)},
		[]value.Value{value.New(42)},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := program.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	// Bytecode 3 (14/10/2026): only the version (0002 → 0003) and the checksum after it moved; the
	// envelope, the code and the constant are those bytecode 2 wrote. Bytecode 4 (15/10/2026) the same
	// again: the version (0003 → 0004) and the checksum.
	const expected = "4b5750420001000493dd074bb273e70d745438f926533c2af190725163dc4bdb3ff9035e8bf005280000000400000001000000000000001b02004045000000000000"
	if got := hex.EncodeToString(encoded); got != expected {
		t.Fatalf("encoded Program changed:\n got %s\nwant %s", got, expected)
	}
}

func TestProgramBinaryRoundTrip(t *testing.T) {
	program, err := NewProgramWithDebug(
		[]byte{
			byte(JUMP), 0, 7,
			byte(PUSH), 0, 0,
			byte(RETURN),
			byte(PUSH), 0, 1,
			byte(RETURN),
		},
		[]value.Value{
			value.New(&value.Lambda{
				Address:      3,
				Name:         "answer",
				SourceFile:   "router.kitwork.js",
				SourceLine:   2,
				SourceColumn: 7,
				Params:       []string{"input"},
			}),
			value.New(42),
		},
		[]DebugEntry{
			{
				IP: 0,
				SourceLocation: SourceLocation{
					File:   "router.kitwork.js",
					Line:   1,
					Column: 1,
				},
			},
			{
				IP: 7,
				SourceLocation: SourceLocation{
					File:   "router.kitwork.js",
					Line:   5,
					Column: 3,
				},
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	encoded, err := program.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := UnmarshalProgram(encoded)
	if err != nil {
		t.Fatal(err)
	}

	if restored == program {
		t.Fatal("decode reused the original Program owner")
	}
	if restored.ProgramVersion() != BytecodeVersion ||
		restored.Checksum() != program.Checksum() ||
		!bytes.Equal(restored.Instructions(), program.Instructions()) {
		t.Fatalf(
			"restored program differs: version=%d checksum=%q",
			restored.ProgramVersion(),
			restored.Checksum(),
		)
	}
	if got := restored.SourceAt(8); got.Line != 5 || got.Column != 3 {
		t.Fatalf("restored source location = %#v", got)
	}
	result := New(restored).Run()
	if result.Int() != 42 {
		t.Fatalf("restored execution result = %#v", result)
	}
}

func TestProgramBinaryRejectsIncompatibleOrCorruptData(t *testing.T) {
	program := mustProgram(
		t,
		[]byte{byte(PUSH), 0, 0, byte(RETURN)},
		[]value.Value{value.New(42)},
	)
	encoded, err := program.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		mutate  func([]byte) []byte
		message string
	}{
		{
			name: "encoding version",
			mutate: func(data []byte) []byte {
				binary.BigEndian.PutUint16(data[4:6], ProgramEncodingVersion+1)
				return data
			},
			message: "encoding version",
		},
		{
			name: "bytecode version",
			mutate: func(data []byte) []byte {
				binary.BigEndian.PutUint16(data[6:8], BytecodeVersion+1)
				return data
			},
			message: "bytecode version",
		},
		{
			name: "checksum",
			mutate: func(data []byte) []byte {
				data[8] ^= 0xff
				return data
			},
			message: "checksum mismatch",
		},
		{
			name: "truncated",
			mutate: func(data []byte) []byte {
				return data[:len(data)-1]
			},
			message: "truncated",
		},
		{
			name: "trailing",
			mutate: func(data []byte) []byte {
				return append(data, 1)
			},
			message: "trailing",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := test.mutate(append([]byte(nil), encoded...))
			if _, err := UnmarshalProgram(candidate); err == nil ||
				!strings.Contains(err.Error(), test.message) {
				t.Fatalf("decode error = %v, want %q", err, test.message)
			}
		})
	}
}

func FuzzUnmarshalProgram(f *testing.F) {
	program, err := NewProgram(
		[]byte{byte(PUSH), 0, 0, byte(RETURN)},
		[]value.Value{value.New("seed")},
		nil,
	)
	if err != nil {
		f.Fatal(err)
	}
	encoded, err := program.MarshalBinary()
	if err != nil {
		f.Fatal(err)
	}
	f.Add(encoded)
	f.Add([]byte("KWPB"))
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, data []byte) {
		restored, err := UnmarshalProgram(data)
		if err != nil {
			return
		}
		if restored == nil || restored.ProgramVersion() != BytecodeVersion {
			t.Fatalf("accepted invalid restored program: %#v", restored)
		}
		if _, verifyErr := verifyAndProfile(restored.code, restored.constants); verifyErr != nil {
			t.Fatalf("accepted unverifiable program: %v", verifyErr)
		}
	})
}

// EncodedSize counts what MarshalBinary writes, without writing it (15/10/2026): the compiler refuses a
// program whose envelope is past MaxProgramBinarySize where it crosses. Its debug table writes each
// entry's file name, so 140 entries in a file named with 1 MiB pass 64 MiB with 280 bytes of code, and the
// encoder's error names the envelope, not a string. CONTROL: the same program in a file without a name.
func TestEncodedSizeIsWhatMarshalBinaryWrites(t *testing.T) {
	program := func(file string) *Program {
		t.Helper()
		var code []byte
		var entries []DebugEntry
		for index := 0; index < 70; index++ {
			entries = append(entries,
				DebugEntry{IP: len(code), SourceLocation: SourceLocation{File: file, Line: int32(index + 1), Column: 1}},
				DebugEntry{IP: len(code) + 3, SourceLocation: SourceLocation{File: file, Line: int32(index + 1), Column: 5}})
			code = append(code, byte(PUSH), 0, 1, byte(POP))
		}
		code = append(code, byte(PUSH), 0, 0, byte(RETURN))
		lambda := value.New(&value.Lambda{Address: 0, Name: "answer", SourceFile: "router.kitwork.js", SourceLine: 2, Params: []string{"input", "limit"}})
		built, err := NewProgramWithDebug(code, []value.Value{value.New(42), value.New("text"), lambda}, entries)
		if err != nil {
			t.Fatal(err)
		}
		return built
	}

	small := program("")
	data, err := small.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if size, crossing := small.EncodedSize(); size != len(data) || crossing != len(small.DebugEntries()) {
		t.Fatalf("CONTROL: EncodedSize = %d crossing %d, MarshalBinary wrote %d bytes for %d entries",
			size, crossing, len(data), len(small.DebugEntries()))
	}

	name := strings.Repeat("d", 1<<20)
	large := program(name)
	entries := len(large.DebugEntries())
	fixed := len(data) - entries*16
	want := fixed + entries*(16+len(name))
	crossing := 0
	for fixed+(crossing+1)*(16+len(name)) <= MaxProgramBinarySize {
		crossing++
	}
	if size, at := large.EncodedSize(); size != want || at != crossing {
		t.Fatalf("EncodedSize = %d crossing %d, want %d crossing %d", size, at, want, crossing)
	}
	if _, err := large.MarshalBinary(); err == nil || err.Error() != "encode program: envelope exceeds 67108864 bytes" {
		t.Fatalf("encode error = %v, want the envelope's limit", err)
	}
}
