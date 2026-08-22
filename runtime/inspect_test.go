package runtime_test

import (
	"encoding/json"
	"testing"
	"unicode/utf8"

	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/runtime"
)

func TestInspectProgramExplainsVerifiedControlFlow(t *testing.T) {
	bytecode, err := compiler.CompileSource(`
function choose(number) {
    switch (number) {
    case 1:
        return "one";
    case 2:
        return "two";
    default:
        return "other";
    }
}
let result = choose(2);
`)
	if err != nil {
		t.Fatal(err)
	}
	report, err := runtime.InspectProgram(bytecode.Program)
	if err != nil {
		t.Fatal(err)
	}
	if report.BytecodeVersion != runtime.BytecodeVersion ||
		report.ProgramEncodingVersion != runtime.ProgramEncodingVersion ||
		report.InstructionSetChecksum != runtime.InstructionSetChecksum() ||
		report.Limits != runtime.Limits() ||
		report.Checksum != bytecode.Program.Checksum() {
		t.Fatalf("inspection identity = %+v", report)
	}
	if len(report.Instructions) != report.Profile.Instructions ||
		len(report.Constants) != report.Profile.Constants ||
		len(report.EntryPoints) != report.Profile.EntryPoints {
		t.Fatalf(
			"inspection counts: instructions=%d/%d constants=%d/%d entries=%d/%d",
			len(report.Instructions),
			report.Profile.Instructions,
			len(report.Constants),
			report.Profile.Constants,
			len(report.EntryPoints),
			report.Profile.EntryPoints,
		)
	}
	if len(report.EntryPoints) < 2 || report.EntryPoints[0].Name != "<main>" {
		t.Fatalf("entry points = %+v", report.EntryPoints)
	}

	wantOpcodes := map[string]bool{
		"DUP":     false,
		"COMPARE": false,
		"TRUE":    false,
		"JUMP":    false,
		"POP":     false,
	}
	var operandInstruction int = -1
	reachableCount := 0
	unreachableCount := 0
	for index, instruction := range report.Instructions {
		if _, tracked := wantOpcodes[instruction.Name]; tracked {
			wantOpcodes[instruction.Name] = true
		}
		if instruction.Reachable {
			reachableCount++
			if instruction.StackBefore < instruction.StackIn {
				t.Fatalf("instruction %d stack before=%d, needs=%d", instruction.IP, instruction.StackBefore, instruction.StackIn)
			}
			if instruction.StackAfter != instruction.StackBefore-instruction.StackIn+instruction.StackOut {
				t.Fatalf("instruction %d stack transition = %+v", instruction.IP, instruction)
			}
		} else {
			unreachableCount++
		}
		if instruction.Target != nil && (*instruction.Target < 0 || *instruction.Target > bytecode.Program.Len()) {
			t.Fatalf("instruction %d target = %d", instruction.IP, *instruction.Target)
		}
		if len(instruction.Operands) > 0 && operandInstruction < 0 {
			operandInstruction = index
		}
	}
	for opcode, found := range wantOpcodes {
		if !found {
			t.Fatalf("switch lowering omitted %s: %+v", opcode, report.Instructions)
		}
	}
	if reachableCount == 0 || unreachableCount == 0 {
		t.Fatalf("reachable=%d unreachable=%d, want both for exhaustive-return fixture", reachableCount, unreachableCount)
	}
	for _, constant := range report.Constants {
		if utf8.RuneCountInString(constant.Preview) > 160 {
			t.Fatalf("constant preview is unbounded: %d runes", utf8.RuneCountInString(constant.Preview))
		}
	}
	if _, err := json.Marshal(report); err != nil {
		t.Fatalf("inspection JSON: %v", err)
	}

	if operandInstruction < 0 {
		t.Fatal("fixture produced no instruction operands")
	}
	report.Instructions[operandInstruction].Operands[0] = 0xffff
	if len(report.Constants) > 0 {
		report.Constants[0].Preview = "mutated"
	}
	again, err := runtime.InspectProgram(bytecode.Program)
	if err != nil {
		t.Fatal(err)
	}
	if again.Instructions[operandInstruction].Operands[0] == 0xffff {
		t.Fatal("inspection operands exposed reusable report storage")
	}
	if len(again.Constants) > 0 && again.Constants[0].Preview == "mutated" {
		t.Fatal("inspection constants exposed reusable report storage")
	}
}

func TestInspectProgramHandlesEmptyAndNilPrograms(t *testing.T) {
	empty, err := runtime.InspectProgram(runtime.EmptyProgram())
	if err != nil {
		t.Fatal(err)
	}
	if len(empty.Instructions) != 0 || len(empty.EntryPoints) != 0 || empty.Profile.Instructions != 0 {
		t.Fatalf("empty inspection = %+v", empty)
	}
	if _, err := runtime.InspectProgram(nil); err == nil {
		t.Fatal("nil Program was accepted")
	}
}
