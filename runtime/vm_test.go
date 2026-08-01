package runtime

import (
	"math"
	"testing"

	"github.com/kitwork/engine/value"
)

func mustProgram(t *testing.T, code []byte, constants []value.Value) *Program {
	t.Helper()
	program, err := NewProgram(code, constants, nil)
	if err != nil {
		t.Fatalf("program: %v", err)
	}
	return program
}

func TestVMEnergyExhaustion(t *testing.T) {
	program := mustProgram(t,
		[]byte{byte(PUSH), 0, 0, byte(PUSH), 0, 0, byte(ADD), byte(RETURN)},
		[]value.Value{value.New(1)},
	)
	vm := New(program)
	vm.MaxEnergy = 1

	res := vm.Run()
	if res.K != value.Invalid {
		t.Fatalf("Expected invalid value on energy exhaustion, got %v", res)
	}
}

func TestVMEnergyAccountingDoesNotOverflow(t *testing.T) {
	program := mustProgram(t,
		[]byte{byte(PUSH), 0, 0, byte(RETURN)},
		[]value.Value{value.New(1)},
	)
	vm := New(program)
	vm.Energy = math.MaxUint64 - 1
	vm.MaxEnergy = math.MaxUint64

	result := vm.Run()
	diagnostic, ok := DiagnosticFrom(result)
	if !ok || diagnostic.Code != DiagnosticEnergyLimit {
		t.Fatalf("energy overflow bypassed the limit: %#v", result)
	}
	if vm.Energy != math.MaxUint64 {
		t.Fatalf("energy wrapped to %d", vm.Energy)
	}
}

func TestVMFastReset(t *testing.T) {
	program := mustProgram(t, []byte{byte(RETURN)}, nil)
	vm := New(program)
	vm.Globals["x"] = value.New(123)

	vm.FastReset(program, nil)
	if len(vm.Globals) != 0 {
		t.Fatalf("Expected FastReset to clear globals, got len=%d", len(vm.Globals))
	}
}

func TestVMStatsDescribeAndResetExecution(t *testing.T) {
	program := mustProgram(t,
		[]byte{byte(PUSH), 0, 0, byte(RETURN)},
		[]value.Value{value.New(42)},
	)
	vm := New(program)
	if result := vm.Run(); result.Int() != 42 {
		t.Fatalf("result = %v, want 42", result.Interface())
	}

	stats := vm.Stats()
	if stats.Instructions != 2 || stats.Energy == 0 {
		t.Fatalf("execution stats = %#v", stats)
	}
	if stats.FrameDepth != 1 || stats.PeakFrameDepth != 1 {
		t.Fatalf("frame stats = %#v", stats)
	}

	vm.FastReset(program, nil)
	stats = vm.Stats()
	if stats.Instructions != 0 || stats.Energy != 0 || stats.StackDepth != 0 {
		t.Fatalf("reset stats = %#v", stats)
	}
}

func TestVMFastResetDetachesCapturedRootScope(t *testing.T) {
	template := &value.Lambda{Address: 0}
	program := mustProgram(t,
		[]byte{byte(PUSH), 0, 0, byte(RETURN)},
		[]value.Value{value.New(template)},
	)
	vm := New(program)
	vm.Vars["secret"] = value.NewString("tenant-a")

	result := vm.Run()
	closure, ok := result.V.(*value.Lambda)
	if !ok {
		t.Fatalf("expected closure, got %T", result.V)
	}

	vm.FastReset(nil, nil)
	vm.Vars["secret"] = value.NewString("tenant-b")

	if got := closure.Scope["secret"].Text(); got != "tenant-a" {
		t.Fatalf("captured root scope was cleared during reset: got %q", got)
	}
	if got := vm.Vars["secret"].Text(); got != "tenant-b" {
		t.Fatalf("reset VM did not receive a fresh root scope: got %q", got)
	}
}

func TestVMResetForPoolDropsTenantReferences(t *testing.T) {
	vm := New(mustProgram(t, []byte{byte(RETURN)}, []value.Value{value.NewString("constant")}))
	vm.Globals["tenant"] = value.NewString("a")
	vm.Vars["request"] = value.NewString("a")
	vm.Builtins = []value.Value{value.NewString("builtin")}
	vm.MaxEnergy = 42
	vm.Spawner = func(*value.Lambda) {}

	vm.ResetForPool()

	if vm.Program() != nil || vm.Globals != nil || vm.Builtins != nil {
		t.Fatal("pooled VM retained tenant-owned runtime state")
	}
	if len(vm.Vars) != 0 || vm.MaxEnergy != 0 || vm.Spawner != nil {
		t.Fatal("pooled VM retained request state or execution hooks")
	}
}

func TestVMPrepareHostStateReusesOnlyClearedContainers(t *testing.T) {
	program := mustProgram(t, []byte{byte(RETURN)}, nil)
	vm := New(program)
	firstObject := &struct{ Secret string }{Secret: "first"}
	vm.PrepareHostState(
		map[string]value.Value{"secret": value.New(firstObject)},
		[]value.Value{value.New(firstObject)},
	)
	vm.ResetForPool()

	if vm.Globals != nil || vm.Builtins != nil {
		t.Fatal("pooled VM exposed reusable host containers")
	}
	vm.PrepareHostState(
		map[string]value.Value{"public": value.New("second")},
		[]value.Value{value.New("builtin")},
	)
	if _, exists := vm.Globals["secret"]; exists {
		t.Fatal("reused globals retained the previous owner")
	}
	if len(vm.Globals) != 1 || vm.Globals["public"].String() != "second" {
		t.Fatalf("prepared globals = %#v", vm.Globals)
	}
	if len(vm.Builtins) != 1 || vm.Builtins[0].String() != "builtin" {
		t.Fatalf("prepared builtins = %#v", vm.Builtins)
	}
}

func TestVMResetForPoolDoesNotMutateCallerHostState(t *testing.T) {
	program := mustProgram(t, []byte{byte(RETURN)}, nil)
	globals := map[string]value.Value{"secret": value.New("caller-owned")}
	builtins := []value.Value{value.New("caller-owned")}
	vm := New(program)
	vm.FastReset(program, globals)
	vm.Builtins = builtins
	vm.ResetForPool()

	if globals["secret"].String() != "caller-owned" {
		t.Fatalf("caller globals were cleared: %#v", globals)
	}
	if builtins[0].String() != "caller-owned" {
		t.Fatalf("caller builtins were cleared: %#v", builtins)
	}
}

func TestVMFastResetDropsInactiveFrameReferences(t *testing.T) {
	program := mustProgram(t, []byte{byte(RETURN)}, nil)
	vm := New(program)
	lambda := &value.Lambda{
		Address: 0,
		Params:  []string{"payload"},
		Program: program,
	}
	payload := value.New(map[string]value.Value{
		"request": value.NewString("old"),
	})

	if result := vm.ExecuteLambda(lambda, []value.Value{payload}); result.K == value.Invalid {
		t.Fatalf("execute lambda: %s", result.Text())
	}
	if _, ok := vm.Frames[1].Vars["payload"]; !ok {
		t.Fatal("fixture did not leave payload in the inactive frame")
	}

	vm.FastReset(program, nil)

	frame := vm.Frames[1]
	if len(frame.Vars) != 0 || frame.Fn != nil || len(frame.Defers) != 0 {
		t.Fatal("FastReset retained references in an inactive frame")
	}
}

func TestVMFastResetDetachesCapturedChildFrame(t *testing.T) {
	program := mustProgram(t,
		[]byte{
			byte(JUMP), 0, 7,
			byte(LOAD), 0, 0,
			byte(RETURN),
			byte(PUSH), 0, 1,
			byte(RETURN),
		},
		[]value.Value{
			value.NewString("payload"),
			value.New(&value.Lambda{Address: 3}),
		},
	)
	outer := &value.Lambda{
		Address: 7,
		Params:  []string{"payload"},
		Program: program,
	}
	vm := New(program)

	result := vm.ExecuteLambda(
		outer,
		[]value.Value{value.NewString("captured")},
	)
	closure, ok := result.V.(*value.Lambda)
	if !ok {
		t.Fatalf("result payload = %T, want closure", result.V)
	}

	vm.FastReset(program, nil)

	if got := closure.Scope["payload"].Text(); got != "captured" {
		t.Fatalf("captured child scope was cleared: %q", got)
	}
	if vm.Frames[1].Vars != nil {
		t.Fatal("reset VM retained the captured child frame map")
	}
}

func TestVMResetForPoolBoundsRetainedStackCapacity(t *testing.T) {
	const pooledStackCapacityLimit = 4_096
	vm := New(mustProgram(t, []byte{byte(RETURN)}, nil))
	vm.Stack = make([]value.Value, 32_000)

	vm.ResetForPool()

	if capacity := cap(vm.Stack); capacity > pooledStackCapacityLimit {
		t.Fatalf(
			"pooled VM retained stack capacity %d, limit is %d",
			capacity,
			pooledStackCapacityLimit,
		)
	}
}
