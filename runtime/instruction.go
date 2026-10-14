package runtime

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
)

const DynamicStack = -1

// InstructionSpec is the canonical structural contract for one executable opcode.
// OperandWidths contains big-endian operand widths in bytes. StackIn and StackOut
// describe the fixed stack effect; dynamic instructions expose it through StackEffect.
type InstructionSpec struct {
	Name          string
	OperandWidths []uint8
	StackIn       int
	StackOut      int
	Energy        Cost

	operandSize uint8
	stackEffect func([]uint32) (int, int)
}

func instruction(name string, widths []uint8, stackIn, stackOut int, energy Cost) InstructionSpec {
	var operandSize uint8
	for _, width := range widths {
		operandSize += width
	}
	return InstructionSpec{
		Name:          name,
		OperandWidths: widths,
		StackIn:       stackIn,
		StackOut:      stackOut,
		Energy:        energy,
		operandSize:   operandSize,
	}
}

func dynamicInstruction(
	name string,
	widths []uint8,
	energy Cost,
	effect func([]uint32) (int, int),
) InstructionSpec {
	spec := instruction(name, widths, DynamicStack, DynamicStack, energy)
	spec.stackEffect = effect
	return spec
}

var instructionTable = [256]InstructionSpec{
	PUSH:    instruction("PUSH", []uint8{2}, 0, 1, 1),
	POP:     instruction("POP", nil, 1, 0, 1),
	LOAD:    instruction("LOAD", []uint8{2}, 0, 1, 2),
	STORE:   instruction("STORE", []uint8{2}, 1, 1, 2),
	GET:     instruction("GET", nil, 2, 1, 12),
	DUP:     instruction("DUP", nil, 1, 2, 1),
	BUILTIN: instruction("BUILTIN", []uint8{1}, 0, 1, 2),

	ADD: instruction("ADD", nil, 2, 1, 2),
	SUB: instruction("SUB", nil, 2, 1, 2),
	MUL: instruction("MUL", nil, 2, 1, 6),
	DIV: instruction("DIV", nil, 2, 1, 25),
	MOD: instruction("MOD", nil, 2, 1, 25),

	AND: instruction("AND", nil, 2, 1, 1),
	OR:  instruction("OR", nil, 2, 1, 1),
	NOT: instruction("NOT", nil, 1, 1, 1),

	COMPARE: instruction("COMPARE", []uint8{1}, 2, 1, 4),
	JUMP:    instruction("JUMP", []uint8{2}, 0, 0, 3),
	TRUE:    instruction("TRUE", []uint8{2}, 1, 0, 3),
	FALSE:   instruction("FALSE", []uint8{2}, 1, 0, 3),
	// ITER has branch-specific effects. StackIn/StackOut describes the successful
	// iteration path; Verify handles the exhausted path separately.
	ITER: instruction("ITER", []uint8{2}, 2, 3, 15),
	HALT: instruction("HALT", nil, 0, 0, 1),

	MAKE:  instruction("MAKE", []uint8{1}, 0, 1, 80),
	SET:   instruction("SET", nil, 3, 1, 10),
	MERGE: instruction("MERGE", nil, 2, 1, 20),

	CALL: dynamicInstruction("CALL", []uint8{1}, 150, func(operands []uint32) (int, int) {
		return int(operands[0]) + 1, 1
	}),
	INVOKE: dynamicInstruction("INVOKE", []uint8{1}, 150, func(operands []uint32) (int, int) {
		return int(operands[0]) + 2, 1
	}),
	RETURN: instruction("RETURN", nil, 0, 0, 5),
	DEFER:  instruction("DEFER", nil, 1, 0, 10),
	SPAWN:  instruction("SPAWN", nil, 1, 0, 200),
	COMMIT: instruction("COMMIT", nil, 1, 1, 1),

	// Each wide form is its instruction with a three-byte operand: same stack effect, same energy.
	PUSH_WIDE:  instruction("PUSH_WIDE", []uint8{3}, 0, 1, 1),
	LOAD_WIDE:  instruction("LOAD_WIDE", []uint8{3}, 0, 1, 2),
	STORE_WIDE: instruction("STORE_WIDE", []uint8{3}, 1, 1, 2),
	JUMP_WIDE:  instruction("JUMP_WIDE", []uint8{3}, 0, 0, 3),
	TRUE_WIDE:  instruction("TRUE_WIDE", []uint8{3}, 1, 0, 3),
	FALSE_WIDE: instruction("FALSE_WIDE", []uint8{3}, 1, 0, 3),
	ITER_WIDE:  instruction("ITER_WIDE", []uint8{3}, 2, 3, 15),
}

// LookupInstruction returns metadata only for opcodes implemented by the VM.
// Reserved or historical numeric slots deliberately return false.
func LookupInstruction(op Opcode) (InstructionSpec, bool) {
	spec, ok := lookupInstructionSpec(op)
	if !ok {
		return InstructionSpec{}, false
	}
	return *spec, true
}

func lookupInstructionSpec(op Opcode) (*InstructionSpec, bool) {
	spec := &instructionTable[uint8(op)]
	return spec, spec.Name != ""
}

// InstructionSetChecksum fingerprints the encoded instruction contract.
// BytecodeVersion remains the explicit compatibility decision; this checksum
// lets compiler caches also notice accidental metadata drift.
func InstructionSetChecksum() string {
	return instructionSetChecksum(BytecodeVersion, len(instructionTable))
}

// instructionSetChecksum hashes a version and the instructions in the slots below limit: bytecode 2's
// checksum is instructionSetChecksum(2, int(PUSH_WIDE)), as the slots bytecode 3 kept are unchanged.
func instructionSetChecksum(version uint16, limit int) string {
	hash := sha256.New()
	var number [8]byte
	binary.BigEndian.PutUint16(number[:2], version)
	hash.Write(number[:2])
	for raw := 0; raw < limit; raw++ {
		spec := instructionTable[raw]
		if spec.Name == "" {
			continue
		}
		hash.Write([]byte{byte(raw)})
		binary.BigEndian.PutUint16(number[:2], uint16(len(spec.Name)))
		hash.Write(number[:2])
		hash.Write([]byte(spec.Name))
		hash.Write([]byte{byte(len(spec.OperandWidths))})
		hash.Write(spec.OperandWidths)
		binary.BigEndian.PutUint32(number[:4], uint32(int32(spec.StackIn)))
		hash.Write(number[:4])
		binary.BigEndian.PutUint32(number[:4], uint32(int32(spec.StackOut)))
		hash.Write(number[:4])
		binary.BigEndian.PutUint64(number[:], uint64(spec.Energy))
		hash.Write(number[:])
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func (s InstructionSpec) OperandSize() int {
	return int(s.operandSize)
}

func (s InstructionSpec) StackEffect(operands []uint32) (int, int) {
	if s.stackEffect != nil {
		return s.stackEffect(operands)
	}
	return s.StackIn, s.StackOut
}

// DecodeOperands decodes one instruction's operands from the bytes immediately
// following its opcode. An operand is one, two or three bytes (three in a wide form).
func DecodeOperands(spec InstructionSpec, data []byte) ([]uint32, int, error) {
	need := spec.OperandSize()
	if len(data) < need {
		return nil, 0, fmt.Errorf("need %d operand bytes, have %d", need, len(data))
	}

	operands := make([]uint32, len(spec.OperandWidths))
	offset := 0
	for i, width := range spec.OperandWidths {
		switch width {
		case 1:
			operands[i] = uint32(data[offset])
		case 2:
			operands[i] = uint32(data[offset])<<8 | uint32(data[offset+1])
		case 3:
			operands[i] = uint32(data[offset])<<16 | uint32(data[offset+1])<<8 | uint32(data[offset+2])
		default:
			return nil, 0, fmt.Errorf("unsupported operand width %d", width)
		}
		offset += int(width)
	}
	return operands, offset, nil
}
