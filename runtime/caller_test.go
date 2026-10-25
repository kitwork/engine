package runtime_test

import (
	"testing"

	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
)

// callerHost is a module object whose methods are handed the VM that calls them (runtime.CallerMethods),
// and whose words read by name are value.CallerFunction: it records which VM each call came from, and
// where it is written.
type callerHost struct {
	callers []*runtime.VM
	places  []runtime.SourceLocation
}

func (h *callerHost) record(caller any) value.Value {
	vm, _ := caller.(*runtime.VM)
	h.callers = append(h.callers, vm)
	h.places = append(h.places, vm.CallSite())
	return value.New(len(h.callers))
}

func (h *callerHost) InvokeFrom(caller *runtime.VM, _ string, _ []value.Value) (value.Value, bool) {
	return h.record(caller), true
}

func (h *callerHost) OnGet(string) value.Value {
	return value.NewCallerFunction(func(caller any, _ []value.Value) value.Value { return h.record(caller) })
}

func (h *callerHost) OnCompare(string, value.Value) value.Value { return value.NewNil() }

func (h *callerHost) OnInvoke(string, ...value.Value) value.Value { return h.record(nil) }

// A host object built once (here before the VM exists, as a module imported at a file's top level is) is
// handed, on every call, the VM that runs the call: module.go(…) through INVOKE, a word read off it by
// name and called alone (const { go } = module; go(…)) through CALL, a word kept in an object and called
// after a dot ({ go: module.go }.go(…)), and a word given to an array as its callback. A host that calls a
// value.CallerFunction itself lends no VM.
//
// CONTROL: with nativeMethod not asking the target for CallerMethods, the first call went to OnInvoke and
// the host saw no VM ("call 1 came from 0x0"); with callHost not knowing value.CallerFunction, the second
// call gave null and was never recorded ("1 calls, want 4"). With INVOKE of an object's key leaving a
// CallerFunction to the host's Call, the third call came from no VM ("call 3 came from 0x0"); with an
// array's callback calling it through Call, the fourth ("call 4 came from 0x0").
func TestCallerMethodsReceiveCallingVM(t *testing.T) {
	bytecode, err := compiler.CompileSource(`
const first = module.go(1);
const { go } = module;
const second = go(2);
const words = { start: module.go };
const third = words.start(3);
const fourth = [4].map(module.go);
`)
	if err != nil {
		t.Fatal(err)
	}
	host := &callerHost{}
	vm := runtime.New(bytecode.Program)
	vm.FastReset(bytecode.Program, map[string]value.Value{"module": {K: value.Proxy, V: host}})
	if result := vm.Run(); result.K == value.Invalid {
		t.Fatalf("run: %v", result.V)
	}
	if len(host.callers) != 4 {
		t.Fatalf("%d calls, want 4", len(host.callers))
	}
	for index, caller := range host.callers {
		if caller != vm {
			t.Fatalf("call %d came from %p, want the VM that ran it (%p)", index+1, caller, vm)
		}
	}
	if host.places[0].Line != 2 || host.places[1].Line != 4 || host.places[2].Line != 6 || host.places[3].Line != 7 {
		t.Fatalf("call sites %+v, want lines 2, 4, 6 and 7", host.places)
	}
	if called := (value.NewCallerFunction(func(caller any, _ []value.Value) value.Value {
		if caller != nil {
			return value.New("a VM")
		}
		return value.New("none")
	})).Call("go"); called.Text() != "none" {
		t.Fatalf("a host calling a CallerFunction lent %s", called.Text())
	}
}

// WrapFailure is a runtime error at the host call in progress whose cause is the failure it wraps; safe
// gives the cause as error.cause, of the same shape. A failure nothing catches is not wrapped.
//
// CONTROL: with errorObject leaving cause null, error.cause was null: "cause = null".
func TestWrapFailureGivesTheCause(t *testing.T) {
	bytecode, err := compiler.CompileSource(`const caught = safe(() => group());`)
	if err != nil {
		t.Fatal(err)
	}
	vm := runtime.New(bytecode.Program)
	inner := (&runtime.Diagnostic{Code: runtime.DiagnosticRuntimeError, CauseCode: value.FailCode, Message: "boom", File: "a.work", Line: 3, Column: 7}).Failure()
	group := value.NewCallerFunction(func(caller any, _ []value.Value) value.Value {
		return runtime.WrapFailure(caller.(*runtime.VM), "nhóm hỏng: boom", inner)
	})
	vm.FastReset(bytecode.Program, map[string]value.Value{"group": group, "safe": value.NewSafeFunction()})
	if result := vm.Run(); result.K == value.Invalid {
		t.Fatalf("run: %v", result.V)
	}
	failure := vm.Vars["caught"].Get("error")
	if failure.Get("message").Text() != "nhóm hỏng: boom" || failure.Get("code").Text() != "FAIL" || failure.Get("at").Text() != "<source>:1:27" {
		t.Fatalf("error = %s", failure.Text())
	}
	cause := failure.Get("cause")
	if cause.K != value.Map || cause.Get("message").Text() != "boom" || cause.Get("code").Text() != "FAIL" ||
		cause.Get("at").Text() != "a.work:3:7" || cause.Get("cause").K != value.Nil {
		t.Fatalf("cause = %v", cause.Text())
	}
	limit := (&runtime.Diagnostic{Code: runtime.DiagnosticEnergyLimit, Message: "Energy Limit Exceeded"}).Failure()
	if wrapped := runtime.WrapFailure(nil, "nhóm", limit); wrapped.V != limit.V {
		t.Fatalf("a failure nothing catches was wrapped: %v", wrapped.Text())
	}
}
