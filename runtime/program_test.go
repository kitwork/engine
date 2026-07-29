package runtime

import (
	"strings"
	"testing"

	"github.com/kitwork/engine/value"
)

func TestProgramCopiesOwnedStorage(t *testing.T) {
	template := &value.Lambda{
		Address:      0,
		Name:         "greet",
		SourceFile:   "router.kitwork.js",
		SourceLine:   7,
		SourceColumn: 3,
		Params:       []string{"name"},
	}
	code := []byte{byte(PUSH), 0, 0, byte(RETURN)}
	constants := []value.Value{value.New(template)}
	sourceMap := []int32{1, 1, 1, 1}

	program, err := NewProgram(code, constants, sourceMap)
	if err != nil {
		t.Fatal(err)
	}
	checksum := program.Checksum()

	code[0] = byte(HALT)
	template.Address = 3
	template.Name = "mutated"
	template.SourceFile = "mutated.kitwork.js"
	template.SourceLine = 99
	template.SourceColumn = 99
	template.Params[0] = "mutated"
	sourceMap[0] = 99

	if got := program.Instructions()[0]; got != byte(PUSH) {
		t.Fatalf("constructor retained caller code: opcode = %d", got)
	}
	ownedTemplate := program.Constants()[0].V.(*value.Lambda)
	if ownedTemplate.Address != 0 ||
		ownedTemplate.Name != "greet" ||
		ownedTemplate.SourceFile != "router.kitwork.js" ||
		ownedTemplate.SourceLine != 7 ||
		ownedTemplate.SourceColumn != 3 ||
		ownedTemplate.Params[0] != "name" {
		t.Fatalf("constructor retained caller lambda: %#v", ownedTemplate)
	}
	if got := program.SourceMap()[0]; got != 1 {
		t.Fatalf("constructor retained caller source map: line = %d", got)
	}
	if program.Checksum() != checksum {
		t.Fatal("caller mutation changed program checksum")
	}
}

func TestProgramCompressesAndCopiesDebugTable(t *testing.T) {
	program, err := NewProgramWithDebug(
		[]byte{byte(PUSH), 0, 0, byte(RETURN)},
		[]value.Value{value.New(1)},
		[]DebugEntry{
			{
				IP: 0,
				SourceLocation: SourceLocation{
					File:   "router.kitwork.js",
					Line:   2,
					Column: 1,
				},
			},
			{
				IP: 3,
				SourceLocation: SourceLocation{
					File:   "router.kitwork.js",
					Line:   3,
					Column: 4,
				},
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	if got := program.SourceAt(2); got != (SourceLocation{
		File:   "router.kitwork.js",
		Line:   2,
		Column: 1,
	}) {
		t.Fatalf("source at operand = %#v", got)
	}
	if got := program.SourceAt(3); got.Line != 3 || got.Column != 4 {
		t.Fatalf("source at return = %#v", got)
	}

	entries := program.DebugEntries()
	if len(entries) != 2 {
		t.Fatalf("debug entries = %d, want 2", len(entries))
	}
	entries[0].File = "mutated.kitwork.js"
	if got := program.DebugEntries()[0].File; got != "router.kitwork.js" {
		t.Fatalf("debug accessor exposed program storage: %q", got)
	}
}

func TestProgramRejectsInvalidDebugTable(t *testing.T) {
	_, err := NewProgramWithDebug(
		[]byte{byte(RETURN)},
		nil,
		[]DebugEntry{{IP: 1}},
	)
	if err == nil || !strings.Contains(err.Error(), "outside program length") {
		t.Fatalf("invalid debug table error = %v", err)
	}
}

func TestProgramAccessorsReturnCopies(t *testing.T) {
	program, err := NewProgram(
		[]byte{byte(PUSH), 0, 0, byte(RETURN)},
		[]value.Value{value.New(&value.Lambda{Address: 0, Params: []string{"value"}})},
		[]int32{4, 4, 4, 4},
	)
	if err != nil {
		t.Fatal(err)
	}

	code := program.Instructions()
	constants := program.Constants()
	sourceMap := program.SourceMap()
	code[0] = byte(HALT)
	constants[0].V.(*value.Lambda).Params[0] = "mutated"
	sourceMap[0] = 99

	if program.Instructions()[0] != byte(PUSH) {
		t.Fatal("instruction accessor exposed program storage")
	}
	if got := program.Constants()[0].V.(*value.Lambda).Params[0]; got != "value" {
		t.Fatalf("constant accessor exposed program storage: %q", got)
	}
	if program.SourceMap()[0] != 4 {
		t.Fatal("source-map accessor exposed program storage")
	}
}

func TestProgramRejectsInvalidInput(t *testing.T) {
	if _, err := NewProgram([]byte{byte(ADD)}, nil, nil); err == nil {
		t.Fatal("malformed bytecode was published")
	}
	if _, err := NewProgram([]byte{byte(RETURN)}, nil, []int32{1, 2}); err == nil ||
		!strings.Contains(err.Error(), "source map") {
		t.Fatalf("source-map mismatch error = %v", err)
	}
	if _, err := NewProgram(
		[]byte{byte(PUSH), 0, 0, byte(RETURN)},
		[]value.Value{value.New(map[string]value.Value{"mutable": value.New(1)})},
		nil,
	); err == nil || !strings.Contains(err.Error(), "mutable or unsupported") {
		t.Fatalf("mutable constant error = %v", err)
	}
}

func TestProgramVersionAndChecksum(t *testing.T) {
	code := []byte{byte(PUSH), 0, 0, byte(RETURN)}
	first, err := NewProgram(code, []value.Value{value.New(1)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewProgram(code, []value.Value{value.New(1)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := NewProgram(code, []value.Value{value.New(2)}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if first.ProgramVersion() != BytecodeVersion {
		t.Fatalf("version = %d, want %d", first.ProgramVersion(), BytecodeVersion)
	}
	if first.Checksum() == "" || first.Checksum() != second.Checksum() {
		t.Fatal("equivalent programs must have a stable checksum")
	}
	if first.Checksum() == changed.Checksum() {
		t.Fatal("constant change did not change the checksum")
	}
}

func TestVMRejectsLambdaFromDifferentProgram(t *testing.T) {
	owner, err := NewProgram([]byte{byte(RETURN)}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	other, err := NewProgram(
		[]byte{byte(PUSH), 0, 0, byte(RETURN)},
		[]value.Value{value.New(1)},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}

	vm := New(other)
	result := vm.ExecuteLambda(&value.Lambda{Address: 0, Program: owner}, nil)
	if result.K != value.Invalid || !strings.Contains(result.Text(), "different program") {
		t.Fatalf("cross-program lambda result = %#v", result)
	}
}
