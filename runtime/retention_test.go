package runtime

import (
	"context"
	"fmt"
	"testing"

	"github.com/kitwork/engine/value"
)

func TestVMResetForPoolReleasesExceptionalState(t *testing.T) {
	program := mustProgram(t, []byte{byte(RETURN)}, nil)
	vm := New(program)
	payload := value.New(map[string]value.Value{
		"owner": value.NewString("previous-request"),
	})
	deferred := &value.Lambda{
		Address: 0,
		Scope:   map[string]value.Value{"payload": payload},
		Program: program,
	}

	vm.Context = context.WithValue(
		context.Background(),
		struct{ name string }{"request"},
		payload,
	)
	vm.Stack = make([]value.Value, maxPooledStackCapacity*2)
	for index := range vm.Stack {
		vm.Stack[index] = payload
	}
	for index := 0; index <= maxReusableVariables; index++ {
		vm.Vars[fmt.Sprintf("root_%d", index)] = payload
	}
	vm.Globals = map[string]value.Value{"payload": payload}
	vm.Builtins = []value.Value{payload}
	vm.Spawner = func(*value.Lambda) {}
	vm.MaxEnergy = 1_000_000
	vm.Energy = 42
	vm.instructions = 84
	vm.frameHighWater = len(vm.Frames) - 1
	vm.Frames[0].Defers = make([]*value.Lambda, maxReusableDefers+1)
	for index := range vm.Frames[0].Defers {
		vm.Frames[0].Defers[index] = deferred
	}

	for index := 1; index < len(vm.Frames); index++ {
		frame := &vm.Frames[index]
		frame.IP = index
		frame.LastIP = index
		frame.Fn = deferred
		frame.StackBase = index
		frame.Vars = map[string]value.Value{"payload": payload}
		frame.Defers = []*value.Lambda{deferred}

		switch index {
		case 1:
			frame.captured = true
		case 2:
			frame.Defers = make([]*value.Lambda, maxReusableDefers+1)
			for deferIndex := range frame.Defers {
				frame.Defers[deferIndex] = deferred
			}
		case 3:
			for variable := 0; variable <= maxReusableVariables; variable++ {
				frame.Vars[fmt.Sprintf("local_%d", variable)] = payload
			}
		}
	}

	vm.ResetForPool()

	if vm.Program() != nil {
		t.Fatal("pool reset retained the previous Program")
	}
	if vm.Context != nil || vm.Globals != nil || vm.Builtins != nil || vm.Spawner != nil {
		t.Fatal("pool reset retained request or host-owned references")
	}
	if vm.MaxEnergy != 0 || vm.Energy != 0 || vm.instructions != 0 {
		t.Fatal("pool reset retained execution policy or counters")
	}
	if len(vm.Stack) != 0 || cap(vm.Stack) != initialStackCapacity {
		t.Fatalf(
			"pool reset retained exceptional stack: len=%d cap=%d",
			len(vm.Stack),
			cap(vm.Stack),
		)
	}
	if len(vm.Vars) != 0 || vm.Frames[0].Vars == nil {
		t.Fatal("pool reset did not install a clean root scope")
	}
	vm.Vars["scope_probe"] = value.NewBool(true)
	if !vm.Frames[0].Vars["scope_probe"].Truthy() {
		t.Fatal("root frame and VM variables no longer share one scope")
	}
	delete(vm.Vars, "scope_probe")
	if vm.Frames[0].Defers != nil {
		t.Fatal("pool reset retained oversized root defer storage")
	}
	if vm.FrameIdx != 0 || vm.frameHighWater != 0 {
		t.Fatalf(
			"pool reset retained frame state: current=%d high-water=%d",
			vm.FrameIdx,
			vm.frameHighWater,
		)
	}

	for index := 1; index < len(vm.Frames); index++ {
		frame := &vm.Frames[index]
		if frame.IP != 0 ||
			frame.LastIP != -1 ||
			frame.Fn != nil ||
			frame.StackBase != 0 ||
			frame.captured {
			t.Fatalf("frame %d retained execution state: %#v", index, frame)
		}
		if len(frame.Vars) != 0 || len(frame.Defers) != 0 {
			t.Fatalf("frame %d retained request references", index)
		}
		if index == 2 && frame.Defers != nil {
			t.Fatal("oversized child defer storage was not released")
		}
		if index == 3 && frame.Vars != nil {
			t.Fatal("oversized child variable storage was not released")
		}
	}
}

func TestVMResetClearsReusableBackingStorage(t *testing.T) {
	program := mustProgram(t, []byte{byte(RETURN)}, nil)
	vm := New(program)
	payload := value.NewString("previous-request")
	deferred := &value.Lambda{Address: 0, Program: program}

	vm.Stack = append(vm.Stack, payload)
	vm.Frames[0].Defers = append(vm.Frames[0].Defers, deferred)
	vm.Frames[1].Vars = map[string]value.Value{"payload": payload}
	vm.Frames[1].Defers = append(vm.Frames[1].Defers, deferred)
	vm.frameHighWater = 1

	stackStorage := vm.Stack[:cap(vm.Stack)]
	rootDeferStorage := vm.Frames[0].Defers[:cap(vm.Frames[0].Defers)]
	childDeferStorage := vm.Frames[1].Defers[:cap(vm.Frames[1].Defers)]

	vm.FastReset(program, nil)

	if stackStorage[0].K != value.Invalid || stackStorage[0].V != nil {
		t.Fatal("reusable stack backing storage retained a Value reference")
	}
	if rootDeferStorage[0] != nil || childDeferStorage[0] != nil {
		t.Fatal("reusable defer backing storage retained a lambda reference")
	}
	if len(vm.Frames[1].Vars) != 0 {
		t.Fatal("reusable frame map retained a variable reference")
	}
}
