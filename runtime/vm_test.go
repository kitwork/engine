package runtime

import (
	"testing"

	"github.com/kitwork/engine/value"
)

func TestVMEnergyExhaustion(t *testing.T) {
	vm := New([]byte{byte(ADD), byte(ADD)}, []value.Value{})
	vm.MaxEnergy = 1

	res := vm.Run()
	if res.K != value.Invalid {
		t.Fatalf("Expected invalid value on energy exhaustion, got %v", res)
	}
}

func TestVMFastReset(t *testing.T) {
	vm := New([]byte{byte(ADD)}, []value.Value{})
	vm.Globals["x"] = value.New(123)

	vm.FastReset([]byte{byte(ADD)}, []value.Value{}, nil, nil)
	if len(vm.Globals) != 0 {
		t.Fatalf("Expected FastReset to clear globals, got len=%d", len(vm.Globals))
	}
}

func TestVMFastResetDetachesCapturedRootScope(t *testing.T) {
	template := &value.Lambda{Address: 0}
	vm := New(
		[]byte{byte(PUSH), 0, 0, byte(RETURN)},
		[]value.Value{value.New(template)},
	)
	vm.Vars["secret"] = value.NewString("tenant-a")

	result := vm.Run()
	closure, ok := result.V.(*value.Lambda)
	if !ok {
		t.Fatalf("expected closure, got %T", result.V)
	}

	vm.FastReset(nil, nil, nil, nil)
	vm.Vars["secret"] = value.NewString("tenant-b")

	if got := closure.Scope["secret"].Text(); got != "tenant-a" {
		t.Fatalf("captured root scope was cleared during reset: got %q", got)
	}
	if got := vm.Vars["secret"].Text(); got != "tenant-b" {
		t.Fatalf("reset VM did not receive a fresh root scope: got %q", got)
	}
}

func TestVMResetForPoolDropsTenantReferences(t *testing.T) {
	vm := New([]byte{byte(RETURN)}, []value.Value{value.NewString("constant")})
	vm.Globals["tenant"] = value.NewString("a")
	vm.Vars["request"] = value.NewString("a")
	vm.Builtins = []value.Value{value.NewString("builtin")}
	vm.MaxEnergy = 42
	vm.Spawner = func(*value.Lambda) {}

	vm.ResetForPool()

	if vm.Bytecode != nil || vm.Constants != nil || vm.Globals != nil || vm.Builtins != nil {
		t.Fatal("pooled VM retained tenant-owned runtime state")
	}
	if len(vm.Vars) != 0 || vm.MaxEnergy != 0 || vm.Spawner != nil {
		t.Fatal("pooled VM retained request state or execution hooks")
	}
}
