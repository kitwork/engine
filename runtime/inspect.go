package runtime

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/kitwork/engine/value"
)

const maxConstantPreviewRunes = 160

// ProgramInspection is a detached, read-only explanation of one verified
// Program. It is tooling data, never an executable representation.
type ProgramInspection struct {
	BytecodeVersion        uint16                 `json:"bytecode_version"`
	ProgramEncodingVersion uint16                 `json:"program_encoding_version"`
	InstructionSetChecksum string                 `json:"instruction_set_checksum"`
	Limits                 LimitsSnapshot         `json:"limits"`
	Checksum               string                 `json:"checksum"`
	Profile                ProgramProfile         `json:"profile"`
	EntryPoints            []InspectedEntryPoint  `json:"entry_points"`
	Constants              []InspectedConstant    `json:"constants"`
	Instructions           []InspectedInstruction `json:"instructions"`
}

// InspectedEntryPoint identifies the root or a compiler-created lambda body.
type InspectedEntryPoint struct {
	Address int             `json:"address"`
	Name    string          `json:"name"`
	Params  []string        `json:"params,omitempty"`
	Source  InspectedSource `json:"source"`
}

// InspectedConstant exposes only a bounded textual preview of immutable pool
// data. Large literals cannot make inspector reports unbounded.
type InspectedConstant struct {
	Index   int    `json:"index"`
	Kind    string `json:"kind"`
	Preview string `json:"preview"`
}

// InspectedSource is the source range active at one bytecode address.
type InspectedSource struct {
	File   string `json:"file,omitempty"`
	Line   int32  `json:"line,omitempty"`
	Column int32  `json:"column,omitempty"`
}

// InspectedInstruction combines canonical instruction metadata with the
// absolute stack depth proven by the verifier for that control-flow address.
type InspectedInstruction struct {
	IP              int             `json:"ip"`
	NextIP          int             `json:"next_ip"`
	Opcode          Opcode          `json:"opcode"`
	Name            string          `json:"name"`
	Operands        []uint16        `json:"operands,omitempty"`
	Energy          uint64          `json:"energy"`
	StackIn         int             `json:"stack_in"`
	StackOut        int             `json:"stack_out"`
	Reachable       bool            `json:"reachable"`
	StackBefore     int             `json:"stack_before"`
	StackAfter      int             `json:"stack_after"`
	Target          *int            `json:"target,omitempty"`
	TargetStack     *int            `json:"target_stack,omitempty"`
	Constant        *int            `json:"constant,omitempty"`
	ConstantPreview string          `json:"constant_preview,omitempty"`
	Source          InspectedSource `json:"source"`
}

// InspectProgram decodes a verified Program through the same instruction and
// stack-analysis machinery used for publication. Returned slices own their
// storage and can be safely changed by tooling callers.
func InspectProgram(program *Program) (ProgramInspection, error) {
	if program == nil {
		return ProgramInspection{}, fmt.Errorf("inspect program: nil program")
	}
	code := program.Instructions()
	constants := program.Constants()
	report := ProgramInspection{
		BytecodeVersion:        program.ProgramVersion(),
		ProgramEncodingVersion: ProgramEncodingVersion,
		InstructionSetChecksum: InstructionSetChecksum(),
		Limits:                 Limits(),
		Checksum:               program.Checksum(),
		Profile:                program.Profile(),
		Constants:              inspectConstants(constants),
	}
	if len(code) == 0 {
		return report, nil
	}

	instructions, byIP, boundaries, err := decodeProgram(code)
	if err != nil {
		return ProgramInspection{}, fmt.Errorf("inspect program: %w", err)
	}
	if err := validateOperands(instructions, constants, boundaries, len(code)); err != nil {
		return ProgramInspection{}, fmt.Errorf("inspect program: %w", err)
	}
	entries := inspectEntryPoints(program, constants)
	entryAddresses := make([]int, len(entries))
	for index := range entries {
		entryAddresses[index] = entries[index].Address
	}
	_, depths, err := analyzeStack(entryAddresses, instructions, byIP, len(code))
	if err != nil {
		return ProgramInspection{}, fmt.Errorf("inspect program: %w", err)
	}

	report.EntryPoints = entries
	report.Instructions = make([]InspectedInstruction, 0, len(instructions))
	for _, instruction := range instructions {
		stackIn, stackOut := instruction.spec.StackEffect(instruction.operands)
		depth, reachable := depths[instruction.ip]
		item := InspectedInstruction{
			IP:          instruction.ip,
			NextIP:      instruction.next,
			Opcode:      instruction.op,
			Name:        instruction.spec.Name,
			Operands:    append([]uint16(nil), instruction.operands...),
			Energy:      uint64(instruction.spec.Energy),
			StackIn:     stackIn,
			StackOut:    stackOut,
			Reachable:   reachable,
			StackBefore: depth,
			Source:      inspectSource(program.SourceAt(instruction.ip)),
		}
		if reachable {
			item.StackAfter = depth - stackIn + stackOut
		}
		if isJumpInstruction(instruction.op) {
			target := int(instruction.operands[0])
			item.Target = &target
			if targetDepth, ok := depths[target]; ok {
				item.TargetStack = intPointer(targetDepth)
			}
		}
		if isConstantInstruction(instruction.op) {
			index := int(instruction.operands[0])
			item.Constant = &index
			item.ConstantPreview = report.Constants[index].Preview
		}
		report.Instructions = append(report.Instructions, item)
	}
	return report, nil
}

func inspectEntryPoints(program *Program, constants []value.Value) []InspectedEntryPoint {
	entries := []InspectedEntryPoint{{
		Address: 0,
		Name:    "<main>",
		Source:  inspectSource(program.SourceAt(0)),
	}}
	for _, constant := range constants {
		lambda, ok := constant.V.(*value.Lambda)
		if !ok || lambda == nil {
			continue
		}
		name := lambda.Name
		if name == "" {
			name = "<anonymous>"
		}
		entries = append(entries, InspectedEntryPoint{
			Address: lambda.Address,
			Name:    name,
			Params:  append([]string(nil), lambda.Params...),
			Source: InspectedSource{
				File:   lambda.SourceFile,
				Line:   lambda.SourceLine,
				Column: lambda.SourceColumn,
			},
		})
	}
	lambdas := entries[1:]
	sort.Slice(lambdas, func(left, right int) bool {
		if lambdas[left].Address != lambdas[right].Address {
			return lambdas[left].Address < lambdas[right].Address
		}
		return lambdas[left].Name < lambdas[right].Name
	})
	return entries
}

func inspectConstants(constants []value.Value) []InspectedConstant {
	result := make([]InspectedConstant, len(constants))
	for index, constant := range constants {
		result[index] = InspectedConstant{
			Index:   index,
			Kind:    constant.K.String(),
			Preview: constantPreview(constant),
		}
	}
	return result
}

func constantPreview(constant value.Value) string {
	if lambda, ok := constant.V.(*value.Lambda); ok && lambda != nil {
		name := lambda.Name
		if name == "" {
			name = "<anonymous>"
		}
		return boundedPreview(fmt.Sprintf(
			"%s(%s) @%d",
			name,
			strings.Join(lambda.Params, ", "),
			lambda.Address,
		))
	}
	encoded, err := json.Marshal(constant)
	if err != nil {
		return boundedPreview(constant.Text())
	}
	return boundedPreview(string(encoded))
}

func boundedPreview(input string) string {
	if utf8.RuneCountInString(input) <= maxConstantPreviewRunes {
		return input
	}
	runes := []rune(input)
	return string(runes[:maxConstantPreviewRunes-3]) + "..."
}

func inspectSource(location SourceLocation) InspectedSource {
	return InspectedSource{
		File:   location.File,
		Line:   location.Line,
		Column: location.Column,
	}
}

func isJumpInstruction(op Opcode) bool {
	switch op {
	case JUMP, TRUE, FALSE, ITER:
		return true
	default:
		return false
	}
}

func isConstantInstruction(op Opcode) bool {
	switch op {
	case PUSH, LOAD, STORE:
		return true
	default:
		return false
	}
}

func intPointer(value int) *int {
	return &value
}
