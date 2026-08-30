package runtime_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kitwork/engine/app"
	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
)

const faultManifestSchemaVersion uint16 = 3

const (
	programCodeLengthOffset    = 40
	programConstantCountOffset = 44
	programDebugCountOffset    = 48
	programPayloadOffset       = 52
)

type faultManifest struct {
	SchemaVersion uint16           `json:"schema_version"`
	Decoder       []decoderFault   `json:"decoder"`
	Verifier      []verifierFault  `json:"verifier"`
	Execution     []executionFault `json:"execution"`
}

type decoderFault struct {
	Name          string `json:"name"`
	Mutation      string `json:"mutation"`
	ErrorContains string `json:"error_contains"`
}

type verifierFault struct {
	Name     string             `json:"name"`
	Scenario string             `json:"scenario"`
	Code     runtime.VerifyCode `json:"code"`
}

type executionFault struct {
	Name                string                 `json:"name"`
	Scenario            string                 `json:"scenario"`
	Diagnostic          runtime.DiagnosticCode `json:"diagnostic"`
	CauseCode           string                 `json:"cause_code,omitempty"`
	MinimumStackFrames  int                    `json:"minimum_stack_frames"`
	ExpectedStackFrames int                    `json:"expected_stack_frames,omitempty"`
}

type faultExecutionSetup struct {
	program   *runtime.Program
	globals   map[string]value.Value
	context   context.Context
	maxEnergy uint64
	stopped   bool
}

type faultExecutionOutcome struct {
	Diagnostic     faultDiagnosticFingerprint
	Instructions   uint64
	Energy         uint64
	StackDepth     int
	PeakStackDepth int
	FrameDepth     int
	PeakFrameDepth int
	FrameIndex     int
}

type faultDiagnosticFingerprint struct {
	Code       runtime.DiagnosticCode
	CauseCode  string
	Message    string
	IP         int
	File       string
	Line       int32
	Column     int32
	Function   string
	Stack      []runtime.StackFrame
	Suppressed []faultDiagnosticFingerprint
}

func TestVMFaultGauntletManifest(t *testing.T) {
	_ = readFaultManifest(t)
}

func TestVMFaultGauntletDecoder(t *testing.T) {
	manifest := readFaultManifest(t)
	for _, fixture := range manifest.Decoder {
		fixture := fixture
		t.Run(fixture.Name, func(t *testing.T) {
			encoded := mutatedProgramBinary(t, fixture.Mutation)
			first := decodeFaultError(encoded)
			second := decodeFaultError(encoded)
			if first == "" {
				t.Fatal("mutated Program decoded successfully")
			}
			if !strings.Contains(first, fixture.ErrorContains) {
				t.Fatalf("decode error = %q, want substring %q", first, fixture.ErrorContains)
			}
			if second != first {
				t.Fatalf("decode failure is nondeterministic\nfirst: %q\nsecond: %q", first, second)
			}
		})
	}
}

func TestVMFaultGauntletVerifier(t *testing.T) {
	manifest := readFaultManifest(t)
	for _, fixture := range manifest.Verifier {
		fixture := fixture
		t.Run(fixture.Name, func(t *testing.T) {
			code, constants := verifierFaultInput(t, fixture.Scenario)
			first := verifyFault(t, code, constants)
			second := verifyFault(t, code, constants)
			if first.Code != fixture.Code {
				t.Fatalf("verify code = %s, want %s (%v)", first.Code, fixture.Code, first)
			}
			if !reflect.DeepEqual(first, second) {
				t.Fatalf("verification failure is nondeterministic\nfirst: %#v\nsecond: %#v", first, second)
			}
		})
	}
}

func TestVMFaultGauntletExecutionRecovery(t *testing.T) {
	manifest := readFaultManifest(t)
	recovery := compileFaultProgram(t, `const result = 40 + 2;`)

	for _, fixture := range manifest.Execution {
		fixture := fixture
		t.Run(fixture.Name, func(t *testing.T) {
			vm := runtime.New(nil)
			baseline := runExecutionFault(t, vm, executionFaultSetup(t, fixture.Scenario), fixture)
			assertVMRecovers(t, vm, recovery)

			repeated := runExecutionFault(
				t,
				runtime.New(nil),
				executionFaultSetup(t, fixture.Scenario),
				fixture,
			)
			if !reflect.DeepEqual(repeated, baseline) {
				t.Fatalf("fresh execution failure is nondeterministic\nwant: %#v\n got: %#v", baseline, repeated)
			}

			pool := app.NewPool()
			lease := pool.Acquire()
			pooled := runExecutionFault(t, lease, executionFaultSetup(t, fixture.Scenario), fixture)
			if !reflect.DeepEqual(pooled, baseline) {
				t.Fatalf("pooled execution failure changed\nwant: %#v\n got: %#v", baseline, pooled)
			}
			pool.Release(lease)
			if active := pool.Active(); active != 0 {
				t.Fatalf("pool retained %d active leases after failure", active)
			}

			lease = pool.Acquire()
			assertAcquiredVMIsClean(t, lease)
			assertVMRecovers(t, lease, recovery)
			pool.Release(lease)
			stats := pool.Stats()
			if stats.Active != 0 || stats.Acquired != 2 || stats.Released != 2 {
				t.Fatalf("pool accounting after failure recovery = %+v", stats)
			}
		})
	}
}

func readFaultManifest(t testing.TB) faultManifest {
	t.Helper()
	path := filepath.Join("testdata", "faults", "manifest.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var manifest faultManifest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		t.Fatalf("decode VM fault manifest: %v", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			t.Fatal("VM fault manifest contains a trailing JSON value")
		}
		t.Fatalf("decode VM fault manifest trailing data: %v", err)
	}
	validateFaultManifest(t, manifest)
	return manifest
}

func validateFaultManifest(t testing.TB, manifest faultManifest) {
	t.Helper()
	if manifest.SchemaVersion != faultManifestSchemaVersion {
		t.Fatalf("VM fault schema = %d, want %d", manifest.SchemaVersion, faultManifestSchemaVersion)
	}
	if len(manifest.Decoder) < 6 || len(manifest.Verifier) < 6 || len(manifest.Execution) < 6 {
		t.Fatalf(
			"VM fault corpus is incomplete: decoder=%d verifier=%d execution=%d",
			len(manifest.Decoder),
			len(manifest.Verifier),
			len(manifest.Execution),
		)
	}
	seen := make(map[string]struct{}, len(manifest.Decoder)+len(manifest.Verifier)+len(manifest.Execution))
	add := func(category, name string) {
		t.Helper()
		if strings.TrimSpace(name) == "" {
			t.Fatalf("%s fault has an empty name", category)
		}
		key := category + "/" + name
		if _, exists := seen[key]; exists {
			t.Fatalf("duplicate VM fault %q", key)
		}
		seen[key] = struct{}{}
	}
	for _, fixture := range manifest.Decoder {
		add("decoder", fixture.Name)
		if fixture.Mutation == "" || fixture.ErrorContains == "" {
			t.Fatalf("decoder fault %q is incomplete", fixture.Name)
		}
	}
	for _, fixture := range manifest.Verifier {
		add("verifier", fixture.Name)
		if fixture.Scenario == "" || fixture.Code == "" {
			t.Fatalf("verifier fault %q is incomplete", fixture.Name)
		}
	}
	for _, fixture := range manifest.Execution {
		add("execution", fixture.Name)
		if fixture.Scenario == "" ||
			fixture.Diagnostic == "" ||
			fixture.MinimumStackFrames < 0 ||
			fixture.ExpectedStackFrames < 0 ||
			(fixture.ExpectedStackFrames > 0 &&
				fixture.ExpectedStackFrames < fixture.MinimumStackFrames) {
			t.Fatalf("execution fault %q is incomplete", fixture.Name)
		}
	}
}

func decodeFaultError(encoded []byte) string {
	_, err := runtime.UnmarshalProgram(encoded)
	if err == nil {
		return ""
	}
	return err.Error()
}

func mutatedProgramBinary(t testing.TB, mutation string) []byte {
	t.Helper()
	var program *runtime.Program
	var err error
	switch mutation {
	case "truncated-string-payload":
		program, err = runtime.NewProgram(
			[]byte{byte(runtime.PUSH), 0, 0, byte(runtime.RETURN)},
			[]value.Value{value.NewString("fault")},
			nil,
		)
	case "debug-ip-outside-program":
		program, err = runtime.NewProgramWithDebug(
			[]byte{byte(runtime.RETURN)},
			nil,
			[]runtime.DebugEntry{{
				IP: 0,
				SourceLocation: runtime.SourceLocation{
					File:   "fault.kitwork.js",
					Line:   1,
					Column: 1,
				},
			}},
		)
	default:
		program, err = runtime.NewProgram(
			[]byte{byte(runtime.PUSH), 0, 0, byte(runtime.RETURN)},
			[]value.Value{value.New(42)},
			nil,
		)
	}
	if err != nil {
		t.Fatalf("build decoder fault seed: %v", err)
	}
	encoded, err := program.MarshalBinary()
	if err != nil {
		t.Fatalf("encode decoder fault seed: %v", err)
	}
	codeLength := int(binary.BigEndian.Uint32(encoded[programCodeLengthOffset:programConstantCountOffset]))
	constantOffset := programPayloadOffset + codeLength

	switch mutation {
	case "invalid-magic":
		encoded[0] ^= 0xff
	case "oversized-code-length":
		binary.BigEndian.PutUint32(
			encoded[programCodeLengthOffset:programConstantCountOffset],
			uint32(runtime.MaxBytecodeSize+1),
		)
	case "oversized-constant-count":
		binary.BigEndian.PutUint32(
			encoded[programConstantCountOffset:programDebugCountOffset],
			uint32(runtime.MaxConstants+1),
		)
	case "debug-count-exceeds-code":
		binary.BigEndian.PutUint32(
			encoded[programDebugCountOffset:programPayloadOffset],
			uint32(codeLength+1),
		)
	case "unsupported-constant-flags":
		encoded[constantOffset+1] = 2
	case "unsupported-constant-kind":
		encoded[constantOffset] = 0xff
	case "truncated-string-payload":
		binary.BigEndian.PutUint32(encoded[constantOffset+10:constantOffset+14], uint32(len(encoded)+1))
	case "debug-ip-outside-program":
		binary.BigEndian.PutUint32(
			encoded[programPayloadOffset+codeLength:programPayloadOffset+codeLength+4],
			uint32(codeLength),
		)
	default:
		t.Fatalf("unknown decoder mutation %q", mutation)
	}
	return encoded
}

func verifierFaultInput(t testing.TB, scenario string) ([]byte, []value.Value) {
	t.Helper()
	switch scenario {
	case "oversized-program":
		return make([]byte, runtime.MaxBytecodeSize+1), nil
	case "oversized-constant-pool":
		return nil, make([]value.Value, runtime.MaxConstants+1)
	case "store-requires-string":
		return []byte{
			byte(runtime.PUSH), 0, 0,
			byte(runtime.STORE), 0, 1,
			byte(runtime.RETURN),
		}, []value.Value{value.New(1), value.New(2)}
	case "store-rejects-malformed-string":
		return []byte{
				byte(runtime.PUSH), 0, 0,
				byte(runtime.STORE), 0, 1,
				byte(runtime.RETURN),
			}, []value.Value{
				value.New(1),
				{K: value.String, V: 42},
			}
	case "jump-past-program":
		return []byte{byte(runtime.JUMP), 0xff, 0xff}, nil
	case "iter-target-inside-operand":
		return []byte{byte(runtime.ITER), 0, 1, byte(runtime.RETURN)}, nil
	case "lambda-target-inside-operand":
		return []byte{
			byte(runtime.PUSH), 0, 0,
			byte(runtime.RETURN),
		}, []value.Value{value.New(&value.Lambda{Address: 1})}
	case "unsupported-assigned-opcode":
		return []byte{byte(runtime.YIELD)}, nil
	default:
		t.Fatalf("unknown verifier scenario %q", scenario)
		return nil, nil
	}
}

func verifyFault(t testing.TB, code []byte, constants []value.Value) runtime.VerifyError {
	t.Helper()
	err := runtime.Verify(code, constants)
	if err == nil {
		t.Fatal("malformed bytecode verified successfully")
	}
	var verifyError *runtime.VerifyError
	if !errors.As(err, &verifyError) {
		t.Fatalf("verify error type = %T, want *runtime.VerifyError", err)
	}
	return *verifyError
}

func executionFaultSetup(t testing.TB, scenario string) faultExecutionSetup {
	t.Helper()
	setup := faultExecutionSetup{
		context:   context.Background(),
		maxEnergy: 1_000_000,
	}
	switch scenario {
	case "no-program":
		return setup
	case "stopped-vm":
		setup.program = compileFaultProgram(t, `const result = 1;`)
		setup.stopped = true
	case "native-panic":
		setup.program = compileFaultProgram(t, `const result = explode();`)
		setup.globals = map[string]value.Value{
			"explode": value.NewFunc(func(...value.Value) value.Value {
				panic("fault gauntlet native panic")
			}),
		}
	case "runtime-error":
		setup.program = compileFaultProgram(t, `const result = fail();`)
		setup.globals = map[string]value.Value{
			"fail": value.NewFunc(func(...value.Value) value.Value {
				return value.Value{K: value.Invalid, V: "fault gauntlet runtime error"}
			}),
		}
	case "runtime-error-with-cause":
		setup.program = compileFaultProgram(t, `const result = fail();`)
		setup.globals = map[string]value.Value{
			"fail": value.NewFunc(func(...value.Value) value.Value {
				return value.InvalidFailure("FAULT_CONFLICT", "fault gauntlet conflict")
			}),
		}
	case "energy-limit":
		setup.program = compileFaultProgram(t, faultLoopSource())
		setup.maxEnergy = 25
	case "cancelled":
		setup.program = compileFaultProgram(t, faultLoopSource())
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		setup.context = ctx
	case "stack-overflow":
		setup.program = compileFaultProgram(t, `
const recurse = (self) => self(self);
const result = recurse(recurse);
`)
	case "program-mismatch":
		foreign := mustFaultProgram(t,
			[]byte{byte(runtime.PUSH), 0, 0, byte(runtime.RETURN)},
			[]value.Value{value.New(42)},
		)
		setup.program = mustFaultProgram(t,
			[]byte{
				byte(runtime.LOAD), 0, 0,
				byte(runtime.CALL), 0,
				byte(runtime.RETURN),
			},
			[]value.Value{value.NewString("task")},
		)
		setup.globals = map[string]value.Value{
			"task": value.New(&value.Lambda{Address: 0, Program: foreign}),
		}
	default:
		t.Fatalf("unknown execution scenario %q", scenario)
	}
	return setup
}

func faultLoopSource() string {
	return `
let total = 0;
for (let index = 0; index < 1000; index++) {
  total = total + index;
}
const result = total;
`
}

func runExecutionFault(
	t testing.TB,
	vm *runtime.VM,
	setup faultExecutionSetup,
	expected executionFault,
) faultExecutionOutcome {
	t.Helper()
	vm.FastReset(setup.program, setup.globals)
	vm.Context = setup.context
	vm.Builtins = nil
	vm.Spawner = nil
	vm.MaxEnergy = setup.maxEnergy
	if setup.stopped {
		vm.Stop()
	}

	result := vm.Run()
	diagnostic, ok := runtime.DiagnosticFrom(result)
	if !ok {
		t.Fatalf("fault %q returned no structured diagnostic: %#v", expected.Name, result)
	}
	if diagnostic.Code != expected.Diagnostic {
		t.Fatalf("fault %q diagnostic = %s, want %s", expected.Name, diagnostic.Code, expected.Diagnostic)
	}
	if diagnostic.CauseCode != expected.CauseCode {
		t.Fatalf(
			"fault %q cause code = %q, want %q",
			expected.Name,
			diagnostic.CauseCode,
			expected.CauseCode,
		)
	}
	if diagnostic.Message == "" {
		t.Fatalf("fault %q returned an empty diagnostic message", expected.Name)
	}
	if len(diagnostic.Stack) < expected.MinimumStackFrames {
		t.Fatalf(
			"fault %q stack frames = %d, want at least %d",
			expected.Name,
			len(diagnostic.Stack),
			expected.MinimumStackFrames,
		)
	}
	if expected.ExpectedStackFrames > 0 && len(diagnostic.Stack) != expected.ExpectedStackFrames {
		t.Fatalf(
			"fault %q stack frames = %d, want exactly %d",
			expected.Name,
			len(diagnostic.Stack),
			expected.ExpectedStackFrames,
		)
	}
	stats := vm.Stats()
	if stats.StackDepth != 0 {
		t.Fatalf("fault %q retained stack depth %d", expected.Name, stats.StackDepth)
	}
	return faultExecutionOutcome{
		Diagnostic:     fingerprintFaultDiagnostic(diagnostic),
		Instructions:   stats.Instructions,
		Energy:         stats.Energy,
		StackDepth:     stats.StackDepth,
		PeakStackDepth: stats.PeakStackDepth,
		FrameDepth:     stats.FrameDepth,
		PeakFrameDepth: stats.PeakFrameDepth,
		FrameIndex:     vm.FrameIdx,
	}
}

func fingerprintFaultDiagnostic(diagnostic *runtime.Diagnostic) faultDiagnosticFingerprint {
	result := faultDiagnosticFingerprint{
		Code:      diagnostic.Code,
		CauseCode: diagnostic.CauseCode,
		Message:   diagnostic.Message,
		IP:        diagnostic.IP,
		File:      diagnostic.File,
		Line:      diagnostic.Line,
		Column:    diagnostic.Column,
		Function:  diagnostic.Function,
		Stack:     append([]runtime.StackFrame(nil), diagnostic.Stack...),
	}
	for _, suppressed := range diagnostic.Suppressed {
		result.Suppressed = append(result.Suppressed, fingerprintFaultDiagnostic(suppressed))
	}
	return result
}

func assertVMRecovers(t testing.TB, vm *runtime.VM, recovery *runtime.Program) {
	t.Helper()
	vm.FastReset(recovery, nil)
	vm.Context = context.Background()
	vm.Builtins = nil
	vm.Spawner = nil
	vm.MaxEnergy = 1_000_000
	result := vm.Run()
	if diagnostic, ok := runtime.DiagnosticFrom(result); ok {
		t.Fatalf("recovery returned diagnostic: %#v", diagnostic)
	}
	answer, ok := vm.Vars["result"]
	if !ok || answer.Int() != 42 {
		t.Fatalf("recovery result = %#v, want 42", answer)
	}
	stats := vm.Stats()
	if stats.StackDepth != 0 || vm.FrameIdx != 0 {
		t.Fatalf("recovery left execution state: stats=%+v frame=%d", stats, vm.FrameIdx)
	}
}

func assertAcquiredVMIsClean(t testing.TB, vm *runtime.VM) {
	t.Helper()
	stats := vm.Stats()
	if vm.Program() != nil ||
		vm.Context != nil ||
		vm.MaxEnergy != 0 ||
		vm.Energy != 0 ||
		vm.Spawner != nil ||
		len(vm.Stack) != 0 ||
		len(vm.Vars) != 0 ||
		len(vm.Globals) != 0 ||
		len(vm.Builtins) != 0 ||
		vm.FrameIdx != 0 ||
		stats.Instructions != 0 ||
		stats.StackDepth != 0 ||
		stats.PeakStackDepth != 0 {
		t.Fatalf("acquired VM retained fault state: vm=%+v stats=%+v", vm, stats)
	}
}

func compileFaultProgram(t testing.TB, source string) *runtime.Program {
	t.Helper()
	bytecode, err := compiler.CompileSource(source)
	if err != nil {
		t.Fatalf("compile VM fault fixture: %v", err)
	}
	return bytecode.Program
}

func mustFaultProgram(t testing.TB, code []byte, constants []value.Value) *runtime.Program {
	t.Helper()
	program, err := runtime.NewProgram(code, constants, nil)
	if err != nil {
		t.Fatalf("build VM fault Program: %v", err)
	}
	return program
}
