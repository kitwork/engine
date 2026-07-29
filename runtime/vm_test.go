package runtime

import (
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

func TestVMFastReset(t *testing.T) {
	program := mustProgram(t, []byte{byte(RETURN)}, nil)
	vm := New(program)
	vm.Globals["x"] = value.New(123)

	vm.FastReset(program, nil)
	if len(vm.Globals) != 0 {
		t.Fatalf("Expected FastReset to clear globals, got len=%d", len(vm.Globals))
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
