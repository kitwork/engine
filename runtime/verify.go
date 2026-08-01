package runtime

import (
	"fmt"

	"github.com/kitwork/engine/value"
)

const (
	MaxBytecodeSize = 1<<16 - 1
	MaxConstants    = 1 << 16
	MaxStackDepth   = 1<<16 - 1
)

type VerifyCode string

const (
	VerifyProgramTooLarge     VerifyCode = "PROGRAM_TOO_LARGE"
	VerifyConstantsTooLarge   VerifyCode = "CONSTANTS_TOO_LARGE"
	VerifyUnknownOpcode       VerifyCode = "UNKNOWN_OPCODE"
	VerifyTruncatedOperand    VerifyCode = "TRUNCATED_OPERAND"
	VerifyInvalidOperand      VerifyCode = "INVALID_OPERAND"
	VerifyConstantOutOfBounds VerifyCode = "CONSTANT_OUT_OF_BOUNDS"
	VerifyInvalidConstant     VerifyCode = "INVALID_CONSTANT"
	VerifyInvalidJump         VerifyCode = "INVALID_JUMP"
	VerifyInvalidLambda       VerifyCode = "INVALID_LAMBDA"
	VerifyStackUnderflow      VerifyCode = "STACK_UNDERFLOW"
	VerifyStackMismatch       VerifyCode = "STACK_MISMATCH"
	VerifyStackTooDeep        VerifyCode = "STACK_TOO_DEEP"
)

// VerifyError reports a deterministic bytecode rejection without executing the program.
type VerifyError struct {
	Code   VerifyCode
	IP     int
	Opcode Opcode
	Detail string
}

func (e *VerifyError) Error() string {
	location := ""
	if e.IP >= 0 {
		location = fmt.Sprintf(" at byte %d", e.IP)
	}
	if e.Detail == "" {
		return fmt.Sprintf("bytecode verify %s%s", e.Code, location)
	}
	return fmt.Sprintf("bytecode verify %s%s: %s", e.Code, location, e.Detail)
}

type decodedInstruction struct {
	ip       int
	next     int
	op       Opcode
	spec     InstructionSpec
	operands []uint16
}

// OpcodeProfile describes one opcode's static presence in an immutable Program.
// EncodedEnergy is Count multiplied by the instruction's canonical energy cost;
// it does not predict loop iterations or runtime branch frequency.
type OpcodeProfile struct {
	Opcode        Opcode `json:"opcode"`
	Name          string `json:"name"`
	Count         int    `json:"count"`
	EncodedEnergy uint64 `json:"encoded_energy"`
}

// ProgramProfile is a static summary produced by the same pass that verifies
// bytecode. It describes encoded structure, not dynamic execution frequency.
type ProgramProfile struct {
	BytecodeBytes int             `json:"bytecode_bytes"`
	Constants     int             `json:"constants"`
	Instructions  int             `json:"instructions"`
	EntryPoints   int             `json:"entry_points"`
	MaxStackDepth int             `json:"max_stack_depth"`
	EncodedEnergy uint64          `json:"encoded_energy"`
	Opcodes       []OpcodeProfile `json:"opcodes"`
}

// Verify rejects structurally invalid bytecode before it reaches a VM.
func Verify(code []byte, constants []value.Value) error {
	_, err := verifyAndProfile(code, constants)
	return err
}

func verifyAndProfile(code []byte, constants []value.Value) (ProgramProfile, error) {
	profile := ProgramProfile{
		BytecodeBytes: len(code),
		Constants:     len(constants),
	}
	if len(code) > MaxBytecodeSize {
		return ProgramProfile{}, verifyError(
			VerifyProgramTooLarge,
			-1,
			0,
			fmt.Sprintf("%d bytes exceeds %d", len(code), MaxBytecodeSize),
		)
	}
	if len(constants) > MaxConstants {
		return ProgramProfile{}, verifyError(
			VerifyConstantsTooLarge,
			-1,
			0,
			fmt.Sprintf("%d constants exceeds %d", len(constants), MaxConstants),
		)
	}
	if len(code) == 0 {
		return profile, nil
	}

	instructions, byIP, boundaries, err := decodeProgram(code)
	if err != nil {
		return ProgramProfile{}, err
	}
	if err := validateOperands(instructions, constants, boundaries, len(code)); err != nil {
		return ProgramProfile{}, err
	}

	entries := []int{0}
	for i, constant := range constants {
		lambda, ok := constant.V.(*value.Lambda)
		if !ok {
			continue
		}
		if lambda == nil || lambda.Address < 0 || lambda.Address >= len(code) || !boundaries[lambda.Address] {
			address := -1
			if lambda != nil {
				address = lambda.Address
			}
			return ProgramProfile{}, verifyError(
				VerifyInvalidLambda,
				-1,
				PUSH,
				fmt.Sprintf("constant %d points to non-instruction address %d", i, address),
			)
		}
		entries = append(entries, lambda.Address)
	}

	maxStackDepth, err := verifyStack(entries, instructions, byIP, len(code))
	if err != nil {
		return ProgramProfile{}, err
	}

	profile.Instructions = len(instructions)
	profile.EntryPoints = len(entries)
	profile.MaxStackDepth = maxStackDepth
	var counts [256]int
	for _, instruction := range instructions {
		counts[uint8(instruction.op)]++
		profile.EncodedEnergy += uint64(instruction.spec.Energy)
	}
	for rawOpcode, count := range counts {
		if count == 0 {
			continue
		}
		opcode := Opcode(rawOpcode)
		spec, _ := LookupInstruction(opcode)
		profile.Opcodes = append(profile.Opcodes, OpcodeProfile{
			Opcode:        opcode,
			Name:          spec.Name,
			Count:         count,
			EncodedEnergy: uint64(spec.Energy) * uint64(count),
		})
	}
	return profile, nil
}

func decodeProgram(code []byte) ([]decodedInstruction, map[int]int, []bool, error) {
	instructions := make([]decodedInstruction, 0, len(code)/2)
	byIP := make(map[int]int, len(code)/2)
	boundaries := make([]bool, len(code)+1)

	for ip := 0; ip < len(code); {
		start := ip
		op := Opcode(code[ip])
		spec, ok := LookupInstruction(op)
		if !ok {
			return nil, nil, nil, verifyError(
				VerifyUnknownOpcode,
				start,
				op,
				fmt.Sprintf("opcode %d is unknown, reserved, or unsupported", op),
			)
		}

		ip++
		operands, size, err := DecodeOperands(spec, code[ip:])
		if err != nil {
			return nil, nil, nil, verifyError(VerifyTruncatedOperand, start, op, err.Error())
		}
		ip += size

		boundaries[start] = true
		byIP[start] = len(instructions)
		instructions = append(instructions, decodedInstruction{
			ip:       start,
			next:     ip,
			op:       op,
			spec:     spec,
			operands: operands,
		})
	}
	boundaries[len(code)] = true
	return instructions, byIP, boundaries, nil
}

func validateOperands(
	instructions []decodedInstruction,
	constants []value.Value,
	boundaries []bool,
	codeLen int,
) error {
	for _, ins := range instructions {
		switch ins.op {
		case PUSH, LOAD, STORE:
			index := int(ins.operands[0])
			if index >= len(constants) {
				return verifyError(
					VerifyConstantOutOfBounds,
					ins.ip,
					ins.op,
					fmt.Sprintf("constant %d with pool size %d", index, len(constants)),
				)
			}
			if ins.op == LOAD || ins.op == STORE {
				if constants[index].K != value.String {
					return verifyError(
						VerifyInvalidConstant,
						ins.ip,
						ins.op,
						fmt.Sprintf("constant %d must be a variable-name string", index),
					)
				}
				if _, ok := constants[index].V.(string); !ok {
					return verifyError(
						VerifyInvalidConstant,
						ins.ip,
						ins.op,
						fmt.Sprintf("constant %d has string kind with non-string payload", index),
					)
				}
			}

		case JUMP, TRUE, FALSE, ITER:
			target := int(ins.operands[0])
			if target < 0 || target > codeLen || !boundaries[target] {
				return verifyError(
					VerifyInvalidJump,
					ins.ip,
					ins.op,
					fmt.Sprintf("target %d is not an instruction boundary", target),
				)
			}

		case COMPARE:
			if ins.operands[0] > 5 {
				return verifyError(
					VerifyInvalidOperand,
					ins.ip,
					ins.op,
					fmt.Sprintf("comparison mode %d is not supported", ins.operands[0]),
				)
			}

		case MAKE:
			if ins.operands[0] > 1 {
				return verifyError(
					VerifyInvalidOperand,
					ins.ip,
					ins.op,
					fmt.Sprintf("collection kind %d is not supported", ins.operands[0]),
				)
			}
		}
	}
	return nil
}

type stackState struct {
	ip    int
	depth int
}

func verifyStack(
	entries []int,
	instructions []decodedInstruction,
	byIP map[int]int,
	codeLen int,
) (int, error) {
	depthAt := make(map[int]int, len(instructions))
	queue := make([]stackState, 0, len(instructions))
	maxDepth := 0

	enqueue := func(ip, depth int) error {
		if depth < 0 {
			return verifyError(VerifyStackUnderflow, ip, 0, "negative stack depth")
		}
		if depth > MaxStackDepth {
			return verifyError(
				VerifyStackTooDeep,
				ip,
				0,
				fmt.Sprintf("depth %d exceeds %d", depth, MaxStackDepth),
			)
		}
		if depth > maxDepth {
			maxDepth = depth
		}
		if ip == codeLen {
			if depth > 1 {
				return verifyError(
					VerifyStackMismatch,
					ip,
					0,
					fmt.Sprintf("program exit retains %d stack values", depth),
				)
			}
			return nil
		}
		if existing, ok := depthAt[ip]; ok {
			if existing != depth {
				return verifyError(
					VerifyStackMismatch,
					ip,
					instructions[byIP[ip]].op,
					fmt.Sprintf("control-flow join has depths %d and %d", existing, depth),
				)
			}
			return nil
		}
		depthAt[ip] = depth
		queue = append(queue, stackState{ip: ip, depth: depth})
		return nil
	}

	for _, entry := range entries {
		if err := enqueue(entry, 0); err != nil {
			return 0, err
		}
	}

	for len(queue) > 0 {
		state := queue[0]
		queue = queue[1:]
		ins := instructions[byIP[state.ip]]

		stackIn, stackOut := ins.spec.StackEffect(ins.operands)
		if state.depth < stackIn {
			return 0, verifyError(
				VerifyStackUnderflow,
				ins.ip,
				ins.op,
				fmt.Sprintf("%s needs %d values, stack has %d", ins.spec.Name, stackIn, state.depth),
			)
		}

		nextDepth := state.depth - stackIn + stackOut
		switch ins.op {
		case RETURN, HALT:
			if state.depth > 1 {
				return 0, verifyError(
					VerifyStackMismatch,
					ins.ip,
					ins.op,
					fmt.Sprintf("%s retains %d stack values", ins.spec.Name, state.depth),
				)
			}
			continue

		case JUMP:
			if err := enqueue(int(ins.operands[0]), nextDepth); err != nil {
				return 0, err
			}

		case TRUE, FALSE:
			if err := enqueue(ins.next, nextDepth); err != nil {
				return 0, err
			}
			if err := enqueue(int(ins.operands[0]), nextDepth); err != nil {
				return 0, err
			}

		case ITER:
			if err := enqueue(ins.next, nextDepth); err != nil {
				return 0, err
			}
			if err := enqueue(int(ins.operands[0]), state.depth-2); err != nil {
				return 0, err
			}

		default:
			if err := enqueue(ins.next, nextDepth); err != nil {
				return 0, err
			}
		}
	}
	return maxDepth, nil
}

func verifyError(code VerifyCode, ip int, op Opcode, detail string) error {
	return &VerifyError{Code: code, IP: ip, Opcode: op, Detail: detail}
}
