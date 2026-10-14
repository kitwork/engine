package runtime_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
)

// Nested execution (nesting.go): a VM that runs inside a host call of another draws on that VM's
// energy and call depth, and a failure that ends a run ends the outer VM too. These tests play the
// host as work/compute.go does: a host function runs its callback in a VM of its own, nested in the
// VM that runs the script.

const nestingBurn = `
const burn = (n) => {
	let total = 0;
	for (let i = 0; i < n; i++) {
		total = total + i;
	}
	return total;
};
`

type nestHost struct {
	program  *runtime.Program
	globals  map[string]value.Value
	root     *runtime.VM
	ceiling  uint64 // the MaxEnergy a nested VM starts with, before NestIn narrows it
	detached bool   // run each callback on a budget of its own, as before nesting existed
	swallow  bool   // drop the nested result, as the query builder drops a predicate's
	spent    uint64 // energy every nested VM spent, summed
	nested   int    // how many nested runs started
	orphans  int    // nested runs whose caller LambdaCaller did not know
}

func newNestHost(t *testing.T, source string, maxEnergy uint64) *nestHost {
	t.Helper()
	bytecode, err := compiler.CompileSource(source)
	if err != nil {
		t.Fatal(err)
	}
	host := &nestHost{program: bytecode.Program, ceiling: maxEnergy}
	host.globals = map[string]value.Value{"nest": value.NewFunc(host.call)}
	host.root = runtime.New(host.program)
	host.root.FastReset(host.program, host.globals)
	host.root.MaxEnergy = maxEnergy
	return host
}

// call runs its callback as work/compute.go does: in a VM of its own, nested in the VM that passed the
// callback (LambdaCaller), whose context it takes.
func (host *nestHost) call(args ...value.Value) value.Value {
	lambda, _ := args[0].V.(*value.Lambda)
	host.nested++
	child := runtime.New(host.program)
	child.FastReset(host.program, host.globals)
	child.MaxEnergy = host.ceiling
	if !host.detached {
		outer := runtime.LambdaCaller(lambda)
		if outer == nil {
			host.orphans++
			outer = host.root
		}
		child.NestIn(outer)
	}
	result := child.ExecuteLambda(lambda, args[1:])
	if !host.detached {
		child.Unnest(result)
	}
	host.spent += child.Stats().Energy
	if host.swallow {
		return value.Value{K: value.Nil}
	}
	return result
}

func (host *nestHost) number(t *testing.T, name string) float64 {
	t.Helper()
	item, ok := host.root.Vars[name]
	if !ok || item.K != value.Number {
		t.Fatalf("%s is not a number after the run: %#v", name, item)
	}
	return item.N
}

func requireDiagnostic(t *testing.T, result value.Value, code runtime.DiagnosticCode) *runtime.Diagnostic {
	t.Helper()
	diagnostic, ok := runtime.DiagnosticFrom(result)
	if !ok {
		t.Fatalf("want a %s failure, the run gave %#v", code, result)
	}
	if diagnostic.Code != code {
		t.Fatalf("want %s, got %s: %s", code, diagnostic.Code, diagnostic.Error())
	}
	return diagnostic
}

// What a nested run spends is the outer VM's own energy: the outer VM ends with exactly its own
// instructions plus everything its nested runs spent. Detached (the control), the outer VM's energy
// is only its own, so the difference is the nested spending, to the unit.
func TestNestedRunSpendsTheOuterBudget(t *testing.T) {
	source := nestingBurn + `var result = nest(() => burn(50));`

	detached := newNestHost(t, source, 10_000_000)
	detached.detached = true
	if result := detached.root.Run(); result.K == value.Invalid {
		t.Fatal(result.Text())
	}
	own := detached.root.Stats().Energy

	nested := newNestHost(t, source, 10_000_000)
	if result := nested.root.Run(); result.K == value.Invalid {
		t.Fatal(result.Text())
	}
	if nested.spent == 0 || nested.spent != detached.spent {
		t.Fatalf("the nested run must spend what the detached one did: %d and %d", nested.spent, detached.spent)
	}
	if got, want := nested.root.Stats().Energy, own+nested.spent; got != want {
		t.Fatalf("outer energy = %d, want its own %d + nested %d = %d", got, own, nested.spent, want)
	}
	if nested.orphans != 0 {
		t.Fatalf("LambdaCaller did not know the caller of %d nested runs", nested.orphans)
	}
}

// LambdaCaller is the VM that passed the lambda to the host call in progress, whether the lambda went
// as a function's argument (CALL) or a method's (INVOKE), and nobody once the call has returned. A
// lambda that reached the host some other way has no caller.
func TestLambdaCallerIsTheVMThatPassedTheLambda(t *testing.T) {
	source := `
const callback = () => 1;
const unpassed = () => 2;
var byFunction = probe(callback);
var byMethod = object.probe(callback);
`
	host := newNestHost(t, source, 10_000_000)
	var seen []string
	var callback *value.Lambda
	probe := value.NewFunc(func(args ...value.Value) value.Value {
		callback, _ = args[0].V.(*value.Lambda)
		unpassed, _ := host.root.Vars["unpassed"].V.(*value.Lambda)
		switch {
		case runtime.LambdaCaller(callback) != host.root:
			seen = append(seen, "the passed lambda has another caller")
		case runtime.LambdaCaller(unpassed) == nil:
			seen = append(seen, "ok")
		default:
			seen = append(seen, "a lambda not passed has a caller")
		}
		return value.Value{K: value.Nil}
	})
	host.globals["probe"] = probe
	host.globals["object"] = value.New(map[string]value.Value{"probe": probe})

	if result := host.root.Run(); result.K == value.Invalid {
		t.Fatal(result.Text())
	}
	if len(seen) != 2 || seen[0] != "ok" || seen[1] != "ok" {
		t.Fatalf("by function, by method: %v", seen)
	}
	if runtime.LambdaCaller(callback) != nil {
		t.Fatal("the host call returned, yet the lambda still has a caller")
	}
}

// One lambda value can be passed by several VMs at once (a module-level lambda, two requests).
// LambdaCaller then answers the VM with the most frames in use: here the nested VM that passes the
// lambda again, inside the outer VM's call, not the outer VM.
func TestLambdaCallerPrefersTheDeepestCaller(t *testing.T) {
	source := `
const shared = () => 1;
var answer = nest((callback) => again(callback), shared);
`
	host := newNestHost(t, source, 10_000_000)
	var caller, root *runtime.VM
	host.globals["again"] = value.NewFunc(func(args ...value.Value) value.Value {
		lambda, _ := args[0].V.(*value.Lambda)
		caller = runtime.LambdaCaller(lambda)
		return value.Value{K: value.Nil}
	})
	root = host.root
	if result := host.root.Run(); result.K == value.Invalid {
		t.Fatal(result.Text())
	}
	switch caller {
	case nil:
		t.Fatal("both VMs passed the lambda, yet it has no caller")
	case root:
		t.Fatal("both VMs passed the lambda; the nested one is the deeper, not the outer one")
	}
}

// Siblings share one budget: each nested run fits the ceiling on its own, the outer loop fits it on
// its own (the control, detached, completes all ten rounds), but together they do not. The fourth run
// exhausts what is left, and the outer VM fails on energy as that host call returns, although the host
// dropped the nested result.
func TestNestedSiblingsExhaustOneBudget(t *testing.T) {
	source := nestingBurn + `
var rounds = 0;
for (let k = 0; k < 10; k++) {
	nest(() => burn(100));
	rounds = rounds + 1;
}
`
	measure := newNestHost(t, source, 10_000_000)
	measure.detached = true
	if result := measure.root.Run(); result.K == value.Invalid {
		t.Fatal(result.Text())
	}
	perRun := measure.spent / 10
	ceiling := measure.root.Stats().Energy + perRun*7/2

	control := newNestHost(t, source, ceiling)
	control.detached, control.swallow = true, true
	if result := control.root.Run(); result.K == value.Invalid {
		t.Fatalf("control: every run fits the ceiling alone: %s", result.Text())
	}
	if rounds := control.number(t, "rounds"); rounds != 10 {
		t.Fatalf("control: all ten rounds must complete, got %v", rounds)
	}

	shared := newNestHost(t, source, ceiling)
	shared.swallow = true
	requireDiagnostic(t, shared.root.Run(), runtime.DiagnosticEnergyLimit)
	if rounds := shared.number(t, "rounds"); rounds != 3 {
		t.Fatalf("three runs fit the shared budget and the fourth exhausts it; rounds = %v", rounds)
	}
	if spent := shared.root.Stats().Energy; spent > ceiling+runtime.Limits().CleanupEnergyReserve {
		t.Fatalf("the request spent %d, above its ceiling %d", spent, ceiling)
	}
}

// A nested run gets only what the outer VM has left, even when its own ceiling is far higher (the
// outer VM spent most of the request before the call): it stops there, and the request as a whole
// stays within its ceiling. The control measures the outer VM's own spending, detached.
func TestNestedRunGetsOnlyWhatIsLeft(t *testing.T) {
	const margin = 3_000
	measure := newNestHost(t, nestingBurn+`var own = burn(2000);`, 100_000_000)
	if result := measure.root.Run(); result.K == value.Invalid {
		t.Fatal(result.Text())
	}
	ceiling := measure.root.Stats().Energy + margin

	host := newNestHost(t, nestingBurn+`
var own = burn(2000);
var after = 0;
nest(() => burn(100000));
after = 1;
`, ceiling)
	host.ceiling = 100_000_000
	host.swallow = true
	requireDiagnostic(t, host.root.Run(), runtime.DiagnosticEnergyLimit)
	if host.spent == 0 || host.spent > margin {
		t.Fatalf("the nested run spent %d; only what was left (under %d) was its to spend", host.spent, margin)
	}
	if spent := host.root.Stats().Energy; spent > ceiling+runtime.Limits().CleanupEnergyReserve {
		t.Fatalf("the request spent %d, above its ceiling %d", spent, ceiling)
	}
	if host.number(t, "after") != 0 {
		t.Fatal("the outer VM ran on after the request's energy was gone")
	}
}

// Call depth is one ceiling for the whole way down: frames held by the outer VMs count, and so does
// each nested VM's root frame. dig recurses through nest, three frames a level (the nested root frame,
// the callback, dig); with the 64-frame ceiling the 21st level is the last to run. Detached (the
// control), every level had 64 frames of its own and the recursion reaches 40.
func TestNestedDepthSharesTheFrameCeiling(t *testing.T) {
	source := `
var deepest = 0;
const dig = (level, limit) => {
	deepest = level;
	if (level < limit) {
		nest(() => dig(level + 1, limit));
	}
	return level;
};
var reached = dig(1, LIMIT);
`
	detached := newNestHost(t, strings.Replace(source, "LIMIT", "40", 1), 10_000_000)
	detached.detached, detached.swallow = true, true
	if result := detached.root.Run(); result.K == value.Invalid {
		t.Fatal(result.Text())
	}
	if deepest := detached.number(t, "deepest"); deepest != 40 {
		t.Fatalf("control: detached levels reach 40, got %v", deepest)
	}

	shallow := newNestHost(t, strings.Replace(source, "LIMIT", "10", 1), 10_000_000)
	shallow.swallow = true
	if result := shallow.root.Run(); result.K == value.Invalid {
		t.Fatalf("control: ten nested levels fit the ceiling: %s", result.Text())
	}

	deep := newNestHost(t, strings.Replace(source, "LIMIT", "40", 1), 10_000_000)
	deep.swallow = true
	requireDiagnostic(t, deep.root.Run(), runtime.DiagnosticStackOverflow)
	if deepest := deep.number(t, "deepest"); deepest != 21 {
		t.Fatalf("frames are shared (3 a level, 64 in all): the 21st level is the last, got %v", deepest)
	}
	if deep.orphans != 0 {
		t.Fatalf("LambdaCaller did not know the caller of %d levels", deep.orphans)
	}
}

// Cancellation reaches a nested run (it shares the request's context) and ends the outer VM as the
// host call returns, not instructions later: mark() after the nested run never runs, although the
// host dropped the nested result.
func TestNestedCancellationEndsTheOuterRun(t *testing.T) {
	source := nestingBurn + `
var after = 0;
nest(() => {
	cancel();
	return burn(100000);
});
after = 1;
mark();
`
	host := newNestHost(t, source, 100_000_000)
	host.swallow = true
	requestContext, cancel := context.WithCancel(context.Background())
	defer cancel()
	host.root.Context = requestContext
	marked := false
	host.globals["cancel"] = value.NewFunc(func(...value.Value) value.Value {
		cancel()
		return value.Value{K: value.Nil}
	})
	host.globals["mark"] = value.NewFunc(func(...value.Value) value.Value {
		marked = true
		return value.Value{K: value.Nil}
	})

	requireDiagnostic(t, host.root.Run(), runtime.DiagnosticCancelled)
	if marked || host.number(t, "after") != 0 {
		t.Fatal("the outer VM ran on after its nested run was cancelled")
	}
	if host.spent > 64*100 {
		t.Fatalf("the nested run did not stop within one check interval: it spent %d", host.spent)
	}
}

// A Go panic in a nested run's host call is NATIVE_PANIC for the outer VM too: it ends the run though
// the host dropped the result, safe(() => …) does not catch it, and the diagnostic shows the nested
// frame above the outer one.
func TestNestedPanicIsNativePanicForTheOuterRun(t *testing.T) {
	source := `
var after = 0;
const attempt = safe(() => nest(() => explode()));
after = 1;
`
	host := newNestHost(t, source, 10_000_000)
	host.globals["safe"] = value.NewSafeFunction()
	host.swallow = true
	host.globals["explode"] = value.NewFunc(func(...value.Value) value.Value {
		panic("explode")
	})

	diagnostic := requireDiagnostic(t, host.root.Run(), runtime.DiagnosticNativePanic)
	if !strings.Contains(diagnostic.Message, "explode") {
		t.Fatalf("the panic value is lost: %s", diagnostic.Message)
	}
	if host.number(t, "after") != 0 {
		t.Fatal("safe() caught a nested panic; the outer VM ran on")
	}
	if len(diagnostic.Stack) < 2 || diagnostic.Stack[len(diagnostic.Stack)-1].Function != "<main>" {
		t.Fatalf("want the nested frames, then the outer ones down to <main>: %s", diagnostic.Error())
	}
}

// A panic outside any host call of the nested VM (work/compute.go recovers it around the whole run)
// is runtime.NativePanic: handed back, it ends the outer VM as NATIVE_PANIC at the host call.
func TestNativePanicHandedBackEndsTheOuterRun(t *testing.T) {
	source := `
var after = 0;
const attempt = safe(() => fail());
after = 1;
`
	host := newNestHost(t, source, 10_000_000)
	host.globals["safe"] = value.NewSafeFunction()
	host.globals["fail"] = value.NewFunc(func(...value.Value) value.Value {
		child := runtime.New(host.program)
		child.FastReset(host.program, host.globals)
		child.MaxEnergy = host.ceiling
		child.NestIn(host.root)
		child.Unnest(runtime.NativePanic("nested execution", "boom"))
		return value.Value{K: value.Nil}
	})

	diagnostic := requireDiagnostic(t, host.root.Run(), runtime.DiagnosticNativePanic)
	if diagnostic.Message != "Native panic in nested execution: boom" {
		t.Fatalf("message = %q", diagnostic.Message)
	}
	if diagnostic.Line == 0 {
		t.Fatalf("a panic with no nested frame is placed at the outer call: %s", diagnostic.Error())
	}
	if host.number(t, "after") != 0 {
		t.Fatal("the outer VM ran on after the handed-back panic")
	}
}

// handBackPanic nests a VM in outer and ends it at once with a Go panic, as work/compute.go does with a
// panic it recovers.
func (host *nestHost) handBackPanic(outer *runtime.VM, recovered string) {
	child := runtime.New(host.program)
	child.FastReset(host.program, host.globals)
	child.MaxEnergy = host.ceiling
	child.NestIn(outer)
	child.Unnest(runtime.NativePanic("nested execution", recovered))
}

// A hand-back can arrive while the outer VM makes no host call (a nested run on another goroutine): the
// checkpoint takes it within one interval. The control, with nothing handed back, runs the loop out.
func TestCheckpointTakesBackOutsideHostCalls(t *testing.T) {
	source := nestingBurn + `var total = burn(100);`

	control := newNestHost(t, source, 10_000_000)
	if result := control.root.Run(); result.K == value.Invalid {
		t.Fatalf("control: the loop runs out: %s", result.Text())
	}

	host := newNestHost(t, source, 10_000_000)
	host.handBackPanic(host.root, "late")
	diagnostic := requireDiagnostic(t, host.root.Run(), runtime.DiagnosticNativePanic)
	if !strings.Contains(diagnostic.Message, "late") {
		t.Fatalf("message = %q", diagnostic.Message)
	}
	if _, ran := host.root.Vars["total"]; ran {
		t.Fatal("the loop ran out: the checkpoint did not take the hand-back")
	}
}

// nestingProxy is a host object whose property read runs a nested VM.
type nestingProxy struct{ read func(key string) value.Value }

func (proxy nestingProxy) OnGet(key string) value.Value { return proxy.read(key) }
func (nestingProxy) OnCompare(string, value.Value) value.Value {
	return value.Value{K: value.Nil}
}
func (nestingProxy) OnInvoke(string, ...value.Value) value.Value {
	return value.Value{K: value.Nil}
}

// A property read is a host call too: what a VM nested in it hands back is taken as the read returns.
func TestPropertyReadTakesBack(t *testing.T) {
	source := `
var after = 0;
var read = probe.anything;
after = 1;
`
	run := func(t *testing.T, fail bool) (*nestHost, value.Value) {
		host := newNestHost(t, source, 10_000_000)
		host.globals["probe"] = value.Value{K: value.Proxy, V: nestingProxy{read: func(string) value.Value {
			if fail {
				host.handBackPanic(host.root, "read")
			}
			return value.New(1)
		}}}
		return host, host.root.Run()
	}

	if control, result := run(t, false); result.K == value.Invalid || control.number(t, "after") != 1 {
		t.Fatalf("control: the read succeeds and the run goes on: %s", result.Text())
	}
	host, result := run(t, true)
	requireDiagnostic(t, result, runtime.DiagnosticNativePanic)
	if host.number(t, "after") != 0 {
		t.Fatal("the outer VM ran on after the read whose nested run panicked")
	}
}

// What a nested VM hands back is for one lease of the outer VM: once the outer VM is back in the pool,
// its next owner never receives it. The control proves the hand-back is live while the lease holds.
func TestHandBackNeverReachesTheNextLease(t *testing.T) {
	handBack := func(t *testing.T, recycle bool) value.Value {
		t.Helper()
		host := newNestHost(t, `var x = park();`, 10_000)
		var parked *runtime.VM
		host.globals["park"] = value.NewFunc(func(...value.Value) value.Value {
			parked = runtime.New(host.program)
			parked.FastReset(host.program, host.globals)
			parked.MaxEnergy = host.ceiling
			parked.NestIn(host.root)
			return value.Value{K: value.Nil}
		})
		if result := host.root.Run(); result.K == value.Invalid {
			t.Fatal(result.Text())
		}
		if recycle {
			host.root.ResetForPool()
		}
		host.root.FastReset(host.program, host.globals)
		host.root.MaxEnergy = 10_000
		parked.Energy = 1_000_000
		parked.Unnest(runtime.NativePanic("nested execution", "stale"))
		host.globals["park"] = value.NewFunc(func(...value.Value) value.Value { return value.Value{K: value.Nil} })
		return host.root.Run()
	}

	requireDiagnostic(t, handBack(t, false), runtime.DiagnosticNativePanic)
	if result := handBack(t, true); result.K == value.Invalid {
		t.Fatalf("the next lease received a stale hand-back: %s", result.Text())
	}
}

// The outer VM and a nested one may be on different goroutines (a host object shared with a
// background task). Under -race this proves they exchange only through atomics; the energy proves
// every hand-back is taken exactly once, whether at a checkpoint while the outer VM runs or at its
// last host call: the outer VM ends with its own energy plus all the nested spending.
func TestNestingAcrossGoroutines(t *testing.T) {
	source := `
const work = () => {
	let ticks = 0;
	for (let k = 0; k < 3000; k++) {
		ticks = ticks + tick();
	}
	return ticks + settle();
};
var ticks = work();
`
	burnBytecode, err := compiler.CompileSource(nestingBurn)
	if err != nil {
		t.Fatal(err)
	}
	setup := runtime.New(burnBytecode.Program)
	if result := setup.Run(); result.K == value.Invalid {
		t.Fatal(result.Text())
	}
	burn, _ := setup.Vars["burn"].V.(*value.Lambda)
	if burn == nil {
		t.Fatal("burn is not a function")
	}

	run := func(t *testing.T, children int) (*runtime.VM, uint64) {
		t.Helper()
		host := newNestHost(t, source, 100_000_000)
		started := make(chan struct{})
		done := make(chan struct{})
		var spent uint64
		var once sync.Once
		host.globals["tick"] = value.NewFunc(func(...value.Value) value.Value {
			once.Do(func() { close(started) })
			return value.New(1)
		})
		host.globals["settle"] = value.NewFunc(func(...value.Value) value.Value {
			<-done
			return value.New(0)
		})
		go func() {
			defer close(done)
			<-started
			for range children {
				child := runtime.New(burnBytecode.Program)
				child.MaxEnergy = 1_000_000
				child.NestIn(host.root)
				result := child.ExecuteLambda(burn, []value.Value{value.New(20)})
				child.Unnest(result)
				spent += child.Stats().Energy
			}
		}()
		if result := host.root.Run(); result.K == value.Invalid {
			t.Fatal(result.Text())
		}
		return host.root, spent
	}

	alone, none := run(t, 0)
	if none != 0 {
		t.Fatal("control: no nested runs, nothing spent")
	}
	shared, spent := run(t, 200)
	if spent == 0 {
		t.Fatal("the nested runs spent nothing")
	}
	if got, want := shared.Stats().Energy, alone.Stats().Energy+spent; got != want {
		t.Fatalf("outer energy = %d, want its own %d + nested %d = %d",
			got, alone.Stats().Energy, spent, want)
	}
}
