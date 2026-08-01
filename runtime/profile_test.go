package runtime

import (
	"reflect"
	"testing"

	"github.com/kitwork/engine/value"
)

func TestProgramProfileUsesVerifiedInstructionGraph(t *testing.T) {
	program := mustProgram(
		t,
		[]byte{
			byte(PUSH), 0, 0,
			byte(PUSH), 0, 1,
			byte(ADD),
			byte(RETURN),
		},
		[]value.Value{value.New(20), value.New(22)},
	)

	profile := program.Profile()
	if profile.BytecodeBytes != 8 ||
		profile.Constants != 2 ||
		profile.Instructions != 4 ||
		profile.EntryPoints != 1 ||
		profile.MaxStackDepth != 2 {
		t.Fatalf("profile = %#v", profile)
	}
	if profile.EncodedEnergy != 9 {
		t.Fatalf("encoded energy = %d, want 9", profile.EncodedEnergy)
	}
	wantOpcodes := []OpcodeProfile{
		{Opcode: PUSH, Name: "PUSH", Count: 2, EncodedEnergy: 2},
		{Opcode: ADD, Name: "ADD", Count: 1, EncodedEnergy: 2},
		{Opcode: RETURN, Name: "RETURN", Count: 1, EncodedEnergy: 5},
	}
	if !reflect.DeepEqual(profile.Opcodes, wantOpcodes) {
		t.Fatalf("opcodes = %#v, want %#v", profile.Opcodes, wantOpcodes)
	}
}

func TestProgramProfileIsDetached(t *testing.T) {
	program := mustProgram(
		t,
		[]byte{byte(PUSH), 0, 0, byte(RETURN)},
		[]value.Value{value.New(42)},
	)
	first := program.Profile()
	first.Opcodes[0].Name = "MUTATED"
	first.Opcodes = nil

	second := program.Profile()
	if len(second.Opcodes) == 0 || second.Opcodes[0].Name != "PUSH" {
		t.Fatalf("Program profile storage was mutable: %#v", second)
	}
}

func TestEmptyProgramProfile(t *testing.T) {
	profile := EmptyProgram().Profile()
	if profile.BytecodeBytes != 0 ||
		profile.Constants != 0 ||
		profile.Instructions != 0 ||
		profile.EntryPoints != 0 ||
		profile.MaxStackDepth != 0 ||
		len(profile.Opcodes) != 0 {
		t.Fatalf("empty profile = %#v", profile)
	}
}
