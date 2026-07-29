package runtime

import (
	"fmt"
	"strings"

	"github.com/kitwork/engine/value"
)

// DiagnosticCode identifies a runtime failure independently of its wording.
type DiagnosticCode string

const (
	DiagnosticRuntimeError      DiagnosticCode = "RUNTIME_ERROR"
	DiagnosticUnknownOpcode     DiagnosticCode = "UNKNOWN_OPCODE"
	DiagnosticTruncatedBytecode DiagnosticCode = "TRUNCATED_BYTECODE"
	DiagnosticEnergyLimit       DiagnosticCode = "ENERGY_LIMIT"
	DiagnosticCancelled         DiagnosticCode = "EXECUTION_CANCELLED"
	DiagnosticStackOverflow     DiagnosticCode = "STACK_OVERFLOW"
	DiagnosticProgramMismatch   DiagnosticCode = "PROGRAM_MISMATCH"
	DiagnosticNoProgram         DiagnosticCode = "NO_PROGRAM"
	DiagnosticVMStopped         DiagnosticCode = "VM_STOPPED"
	DiagnosticNativePanic       DiagnosticCode = "NATIVE_PANIC"
)

// StackFrame is a source-aware snapshot of one active VM frame.
type StackFrame struct {
	Function string
	IP       int
	File     string
	Line     int32
	Column   int32
}

// Diagnostic is the structured form of a runtime failure.
type Diagnostic struct {
	Code       DiagnosticCode
	Message    string
	IP         int
	File       string
	Line       int32
	Column     int32
	Function   string
	Stack      []StackFrame
	Suppressed []*Diagnostic
}

func (d *Diagnostic) Error() string {
	if d == nil {
		return ""
	}

	var out strings.Builder
	out.WriteString(d.Message)
	if d.Line > 0 && !strings.Contains(d.Message, "(at line") {
		fmt.Fprintf(&out, " (at line %d)", d.Line)
	}
	for _, frame := range d.Stack {
		out.WriteString("\n    at ")
		out.WriteString(frame.Function)
		switch {
		case frame.File != "" && frame.Line > 0 && frame.Column > 0 && frame.IP >= 0:
			fmt.Fprintf(
				&out,
				" (%s:%d:%d, byte %d)",
				frame.File,
				frame.Line,
				frame.Column,
				frame.IP,
			)
		case frame.File != "" && frame.Line > 0 && frame.IP >= 0:
			fmt.Fprintf(&out, " (%s:%d, byte %d)", frame.File, frame.Line, frame.IP)
		case frame.File != "" && frame.Line > 0:
			fmt.Fprintf(&out, " (%s:%d)", frame.File, frame.Line)
		case frame.Line > 0 && frame.IP >= 0:
			fmt.Fprintf(&out, " (line %d, byte %d)", frame.Line, frame.IP)
		case frame.Line > 0:
			fmt.Fprintf(&out, " (line %d)", frame.Line)
		case frame.IP >= 0:
			fmt.Fprintf(&out, " (byte %d)", frame.IP)
		}
	}
	for _, suppressed := range d.Suppressed {
		if suppressed == nil {
			continue
		}
		out.WriteString("\n    suppressed: ")
		text := suppressed.Error()
		text = strings.ReplaceAll(text, "\n", "\n        ")
		out.WriteString(text)
	}
	return out.String()
}

// DiagnosticFrom extracts structured runtime information while callers that
// only read Value.Text continue to receive the formatted compatibility string.
func DiagnosticFrom(result value.Value) (*Diagnostic, bool) {
	diagnostic, ok := result.ErrorVal.(*Diagnostic)
	return diagnostic, ok && diagnostic != nil
}

type runtimeFault struct {
	code    DiagnosticCode
	message string
}

func (f *runtimeFault) Error() string {
	if f == nil {
		return ""
	}
	return f.message
}

func (vm *VM) diagnosticValue(code DiagnosticCode, message string, ip int) value.Value {
	diagnostic := vm.buildDiagnostic(code, message, ip)
	return diagnosticResult(diagnostic)
}

func diagnosticResult(diagnostic *Diagnostic) value.Value {
	return value.Value{
		K:        value.Invalid,
		V:        diagnostic.Error(),
		IsError:  true,
		ErrorVal: diagnostic,
	}
}

func mergeFailures(primary, secondary value.Value) value.Value {
	if secondary.K != value.Invalid {
		return primary
	}
	if primary.K != value.Invalid {
		return secondary
	}

	primaryDiagnostic, primaryOK := DiagnosticFrom(primary)
	secondaryDiagnostic, secondaryOK := DiagnosticFrom(secondary)
	if !primaryOK || !secondaryOK {
		return primary
	}

	merged := *primaryDiagnostic
	merged.Stack = append([]StackFrame(nil), primaryDiagnostic.Stack...)
	merged.Suppressed = append(
		append([]*Diagnostic(nil), primaryDiagnostic.Suppressed...),
		secondaryDiagnostic,
	)
	return diagnosticResult(&merged)
}

func (vm *VM) buildDiagnostic(code DiagnosticCode, message string, ip int) *Diagnostic {
	diagnostic := &Diagnostic{
		Code:    code,
		Message: message,
		IP:      ip,
	}
	if vm == nil || vm.program == nil || vm.FrameIdx < 0 {
		return diagnostic
	}

	for index := vm.FrameIdx; index >= 0; index-- {
		frame := &vm.Frames[index]
		frameIP := frame.LastIP
		if index == vm.FrameIdx && ip >= 0 {
			frameIP = ip
		}
		if frameIP < 0 && frame.Fn == nil {
			continue
		}

		name := "<main>"
		location := vm.currentLocation(frameIP)
		if frame.Fn != nil {
			name = frame.Fn.Name
			if name == "" {
				name = "<anonymous>"
			}
			if location.File == "" {
				location.File = frame.Fn.SourceFile
			}
			if location.Line == 0 {
				location.Line = frame.Fn.SourceLine
			}
			if location.Column == 0 {
				location.Column = frame.Fn.SourceColumn
			}
		}
		diagnostic.Stack = append(diagnostic.Stack, StackFrame{
			Function: name,
			IP:       frameIP,
			File:     location.File,
			Line:     location.Line,
			Column:   location.Column,
		})
	}

	if len(diagnostic.Stack) > 0 {
		top := diagnostic.Stack[0]
		diagnostic.IP = top.IP
		diagnostic.File = top.File
		diagnostic.Line = top.Line
		diagnostic.Column = top.Column
		diagnostic.Function = top.Function
	}
	return diagnostic
}
