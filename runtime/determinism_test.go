package runtime_test

import (
	"context"
	"encoding/json"
	"reflect"
	"sort"
	"testing"

	"github.com/kitwork/engine/app"
	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
)

type executionFingerprint struct {
	Program        string
	Result         valueFingerprint
	Variables      []variableFingerprint
	Diagnostic     *diagnosticFingerprint
	Instructions   uint64
	Energy         uint64
	StackDepth     int
	FrameDepth     int
	PeakFrameDepth int
}

type valueFingerprint struct {
	Kind    value.Kind
	IsError bool
	JSON    string
}

type variableFingerprint struct {
	Name  string
	Value valueFingerprint
}

type diagnosticFingerprint struct {
	Code       runtime.DiagnosticCode
	Message    string
	IP         int
	File       string
	Line       int32
	Column     int32
	Function   string
	Stack      []runtime.StackFrame
	Suppressed []*diagnosticFingerprint
}

type determinismFixture struct {
	name       string
	source     string
	globals    func() map[string]value.Value
	context    func() context.Context
	maxEnergy  uint64
	diagnostic runtime.DiagnosticCode
}

func compileDeterminismProgram(t testing.TB, source string) *runtime.Program {
	t.Helper()
	bytecode, err := compiler.CompileSource(source)
	if err != nil {
		t.Fatalf("compile determinism fixture: %v", err)
	}
	return bytecode.Program
}

func runFingerprint(
	vm *runtime.VM,
	program *runtime.Program,
	globals map[string]value.Value,
	executionContext context.Context,
	maxEnergy uint64,
) executionFingerprint {
	vm.FastReset(program, globals)
	vm.Context = executionContext
	vm.Builtins = nil
	vm.Spawner = nil
	vm.MaxEnergy = maxEnergy

	result := vm.Run()
	stats := vm.Stats()
	fingerprint := executionFingerprint{
		Program:        program.Checksum(),
		Result:         fingerprintValue(result),
		Variables:      fingerprintVariables(vm.Vars),
		Instructions:   stats.Instructions,
		Energy:         stats.Energy,
		StackDepth:     stats.StackDepth,
		FrameDepth:     stats.FrameDepth,
		PeakFrameDepth: stats.PeakFrameDepth,
	}
	if diagnostic, ok := runtime.DiagnosticFrom(result); ok {
		fingerprint.Diagnostic = fingerprintDiagnostic(diagnostic)
	}
	return fingerprint
}

func fingerprintValue(item value.Value) valueFingerprint {
	data, err := json.Marshal(item)
	if err != nil {
		data = []byte("<json-error:" + err.Error() + ">")
	}
	return valueFingerprint{
		Kind:    item.K,
		IsError: item.IsError,
		JSON:    string(data),
	}
}

func fingerprintVariables(variables map[string]value.Value) []variableFingerprint {
	names := make([]string, 0, len(variables))
	for name := range variables {
		names = append(names, name)
	}
	sort.Strings(names)

	fingerprint := make([]variableFingerprint, 0, len(names))
	for _, name := range names {
		fingerprint = append(fingerprint, variableFingerprint{
			Name:  name,
			Value: fingerprintValue(variables[name]),
		})
	}
	return fingerprint
}

func fingerprintDiagnostic(diagnostic *runtime.Diagnostic) *diagnosticFingerprint {
	if diagnostic == nil {
		return nil
	}
	fingerprint := &diagnosticFingerprint{
		Code:     diagnostic.Code,
		Message:  diagnostic.Message,
		IP:       diagnostic.IP,
		File:     diagnostic.File,
		Line:     diagnostic.Line,
		Column:   diagnostic.Column,
		Function: diagnostic.Function,
		Stack:    append([]runtime.StackFrame(nil), diagnostic.Stack...),
	}
	for _, suppressed := range diagnostic.Suppressed {
		fingerprint.Suppressed = append(
			fingerprint.Suppressed,
			fingerprintDiagnostic(suppressed),
		)
	}
	return fingerprint
}

func dirtyVM(vm *runtime.VM, program *runtime.Program) {
	vm.FastReset(
		program,
		map[string]value.Value{"previousOwner": value.NewString("dirty")},
	)
	vm.Context = context.Background()
	vm.Builtins = []value.Value{
		value.NewFunc(func(...value.Value) value.Value { return value.NewNil() }),
	}
	vm.Spawner = func(*value.Lambda) {}
	vm.MaxEnergy = 1_000_000
	_ = vm.Run()
}

func determinismFixtures() []determinismFixture {
	return []determinismFixture{
		{
			name: "arithmetic-and-globals",
			source: `
var total = input;
for (let i = 0; i < 20; i++) {
	total = total + i * 2;
}
var result = { total: total, stable: true };
`,
			globals: func() map[string]value.Value {
				return map[string]value.Value{"input": value.New(42)}
			},
			maxEnergy: 100_000,
		},
		{
			name: "closures-and-callbacks",
			source: `
const make = (factor) => (number) => number * factor;
const triple = make(3);
var result = [1, 2, 3, 4]
	.map((number) => triple(number))
	.filter((number) => number > 5)
	.reduce((total, number) => total + number, 0);
`,
			maxEnergy: 100_000,
		},
		{
			name:   "native-panic",
			source: `var result = explode();`,
			globals: func() map[string]value.Value {
				return map[string]value.Value{
					"explode": value.NewFunc(func(...value.Value) value.Value {
						panic("deterministic host panic")
					}),
				}
			},
			maxEnergy:  100_000,
			diagnostic: runtime.DiagnosticNativePanic,
		},
		{
			name: "energy-limit",
			source: `
var total = 0;
for (let i = 0; i < 10000; i++) {
	total = total + i;
}
`,
			maxEnergy:  128,
			diagnostic: runtime.DiagnosticEnergyLimit,
		},
		{
			name: "cancelled",
			source: `
var total = 0;
for (let i = 0; i < 10000; i++) {
	total = total + i;
}
`,
			context: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			},
			maxEnergy:  1_000_000,
			diagnostic: runtime.DiagnosticCancelled,
		},
	}
}

func fixtureGlobals(fixture determinismFixture) map[string]value.Value {
	if fixture.globals == nil {
		return nil
	}
	return fixture.globals()
}

func fixtureContext(fixture determinismFixture) context.Context {
	if fixture.context == nil {
		return context.Background()
	}
	return fixture.context()
}

func assertFingerprintEqual(
	t testing.TB,
	want executionFingerprint,
	got executionFingerprint,
) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("execution fingerprint changed\nwant: %#v\n got: %#v", want, got)
	}
}

func TestVMDeterminismAcrossFreshDirtyAndPooledExecution(t *testing.T) {
	dirtyProgram := compileDeterminismProgram(t, `
const make = (base) => (number) => base + number;
const add = make(40);
var previousResult = [1, 2].map((number) => add(number));
`)
	pool := app.NewPool()

	for _, fixture := range determinismFixtures() {
		t.Run(fixture.name, func(t *testing.T) {
			program := compileDeterminismProgram(t, fixture.source)
			baseline := runFingerprint(
				runtime.New(program),
				program,
				fixtureGlobals(fixture),
				fixtureContext(fixture),
				fixture.maxEnergy,
			)
			if fixture.diagnostic != "" {
				if baseline.Diagnostic == nil ||
					baseline.Diagnostic.Code != fixture.diagnostic {
					t.Fatalf(
						"diagnostic = %#v, want %s",
						baseline.Diagnostic,
						fixture.diagnostic,
					)
				}
			}

			reused := runtime.New(dirtyProgram)
			for iteration := 0; iteration < 20; iteration++ {
				dirtyVM(reused, dirtyProgram)
				got := runFingerprint(
					reused,
					program,
					fixtureGlobals(fixture),
					fixtureContext(fixture),
					fixture.maxEnergy,
				)
				assertFingerprintEqual(t, baseline, got)
			}

			for iteration := 0; iteration < 20; iteration++ {
				vm := pool.Acquire()
				dirtyVM(vm, dirtyProgram)
				pool.Release(vm)

				vm = pool.Acquire()
				got := runFingerprint(
					vm,
					program,
					fixtureGlobals(fixture),
					fixtureContext(fixture),
					fixture.maxEnergy,
				)
				assertFingerprintEqual(t, baseline, got)
				pool.Release(vm)
			}
		})
	}

	if active := pool.Active(); active != 0 {
		t.Fatalf("pool retained %d active VM leases", active)
	}
}

func FuzzVMDeterminism(f *testing.F) {
	seeds := []string{
		`var result = 40 + 2;`,
		`const add = (a, b) => a + b; var result = add(20, 22);`,
		`var result = [1, 2, 3].map((item) => item * 2);`,
		`const make = (base) => (number) => base + number; var result = make(40)(2);`,
		`var result = { answer: 42, nested: [1, 2, 3] };`,
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	dirtyProgram := compileDeterminismProgram(f, `
const make = (base) => (number) => base + number;
var previousResult = make(40)(2);
`)

	f.Fuzz(func(t *testing.T, source string) {
		if len(source) > 32*1024 {
			t.Skip()
		}
		bytecode, err := compiler.CompileSource(source)
		if err != nil {
			return
		}

		baseline := runFingerprint(
			runtime.New(bytecode.Program),
			bytecode.Program,
			nil,
			context.Background(),
			20_000,
		)
		reused := runtime.New(dirtyProgram)
		dirtyVM(reused, dirtyProgram)
		got := runFingerprint(
			reused,
			bytecode.Program,
			nil,
			context.Background(),
			20_000,
		)
		assertFingerprintEqual(t, baseline, got)
	})
}
