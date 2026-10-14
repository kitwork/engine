package runtime

import (
	"context"
	"fmt"
	"hash/maphash"
	"sync"
	"sync/atomic"

	"github.com/kitwork/engine/value"
)

// A VM can run inside a host call of another VM: the query builder runs a predicate to read it,
// db.transaction runs its callback, and either can start another one. That VM is nested. It has no
// budget of its own; it draws on the VM it runs inside, so a request is one budget however deep its
// callbacks go.
//
//   - Energy: a nested VM may spend what the VM it runs inside has left, and what it spent is added
//     to that VM's own energy when the host call returns.
//   - Call depth: the frames held by the VMs it runs inside count against the same 64-frame ceiling,
//     its own root frame included. Each nesting level is Go stack as well, so nesting without a
//     bound would end the process.
//   - Cancellation: a nested VM without a context of its own takes the outer VM's.
//   - A failure that ends a run (energy, cancellation, stack overflow, a Go panic: every failure but
//     a runtime error the host may handle) ends the outer VM too, as the host call returns, whatever
//     the host did with the nested result. A query builder that ignores a predicate's result cannot
//     hide it, and safe(() => …) cannot catch it, exactly as in the outer VM itself.
//
// The host finds the outer VM with LambdaCaller: a host call records, while it runs, which VM passed
// each lambda among its arguments. A capability object knows no VM (one imported at module level is
// built before any request), but the lambda it runs is the one it was handed.
//
// The outer VM usually waits in its host call on the same goroutine, but a host object can reach
// another goroutine (a background task holding the request's database), so the VMs exchange nothing
// but atomics, and every exchange names the outer VM's lease: a VM back in the pool never receives
// what was meant for its previous owner.

const (
	// shownEnergyBits is the width of the remaining energy in nesting.shown; the call depth is above it.
	shownEnergyBits = 56
	// shownEnergyMask is both the mask of the remaining energy and its value for a VM without a ceiling.
	shownEnergyMask = uint64(1)<<shownEnergyBits - 1
	// shownDepthLimit is the largest call depth nesting.shown can carry.
	shownDepthLimit = 1<<(64-shownEnergyBits) - 1
	// nestingWalkLimit bounds the walk to the innermost VM. The call depth bounds real nesting well below it.
	nestingWalkLimit = defaultMaxCallDepth + 1
	// nestingLinkAttempts bounds how often NestIn retries when another goroutine links first.
	nestingLinkAttempts = 8
)

// nesting is the side of a VM that VMs nested in its host calls see. Only atomics: see above.
type nesting struct {
	// lease changes every time the VM goes back to the pool.
	lease atomic.Uint64
	// shown is what the VM had as of its latest host call: the energy it has left in the low
	// shownEnergyBits (shownEnergyMask when it has no ceiling) and above them the frames in use, its
	// own and those of the VMs it runs inside. Zero is nothing shown yet in this lease (a VM that
	// shows has at least its root frame in use).
	shown atomic.Uint64
	// returned is what VMs nested in its host calls handed back that the VM has not taken yet.
	returned atomic.Pointer[handBack]
	// inner is the VM running inside the VM's current host call.
	inner atomic.Pointer[nestLink]
	// context is the VM's Context as of its latest host call that passed a lambda, for a nested VM
	// without one (VM.contextShown keeps it, so a new one is shown only when it changes).
	context atomic.Pointer[shownContext]
}

type shownContext struct{ context context.Context }

// nestLink records that inner runs inside a host call of outer, each named with its lease.
type nestLink struct {
	outer       *VM
	outerLease  uint64
	inner       *VM
	innerLease  uint64
	startEnergy uint64
}

// handBack is what nested VMs return to the VM they ran inside: the energy they spent and the first
// failure that ends a run, for one lease of that VM.
type handBack struct {
	lease  uint64
	energy uint64
	fault  *Diagnostic
}

// NestIn makes vm run inside the current host call of outer, or of the VM nested deepest inside
// outer: a callback can start another. Call it once vm is prepared for its run (reset, MaxEnergy set)
// and before the run, then Unnest when the run ends. vm then spends at most what that VM has left and
// holds frames only below the ceiling that VM leaves. NestIn does nothing when outer is nil or vm is
// nested already.
func (vm *VM) NestIn(outer *VM) {
	if vm == nil || outer == nil || outer == vm || vm.outer != nil {
		return
	}

	innerLease := vm.nested.lease.Load()
	var link *nestLink
	for attempt := 0; attempt < nestingLinkAttempts; attempt++ {
		host, hostLease, current := innermost(outer)
		link = &nestLink{
			outer:       host,
			outerLease:  hostLease,
			inner:       vm,
			innerLease:  innerLease,
			startEnergy: vm.Energy,
		}
		if host.nested.inner.CompareAndSwap(current, link) {
			break
		}
		// Another goroutine nested a VM in host meanwhile; with the last attempt this VM still draws on
		// host, it is only not found by VMs nested deeper (they draw on host as well).
	}
	vm.outer = link
	if vm.Context == nil {
		if shown := link.outer.nested.context.Load(); shown != nil {
			vm.Context = shown.context
		}
	}

	shown := link.outer.nested.shown.Load()
	if shown == 0 {
		// The outer VM has shown nothing in this lease (no host call yet, so this VM cannot be running
		// inside one of them): nothing to narrow by. What this VM spends still goes back to it.
		vm.showHost()
		return
	}
	vm.callFloor = int(shown >> shownEnergyBits)
	if remaining := shown & shownEnergyMask; remaining != shownEnergyMask {
		ceiling := vm.Energy + remaining
		if ceiling < vm.Energy {
			ceiling = ^uint64(0)
		}
		if vm.MaxEnergy == 0 || ceiling < vm.MaxEnergy {
			vm.MaxEnergy = ceiling
		}
		if vm.MaxEnergy == 0 {
			// Nothing is left to lend, and zero would mean no ceiling at all: the first instruction
			// fails on energy, as it would in the outer VM.
			vm.MaxEnergy, vm.Energy = 1, 1
		}
	}
	vm.showHost()
}

// Unnest ends what NestIn began: the energy vm spent goes back to the VM it ran inside, and result
// with it when result is a failure that ends a run, so that VM fails with it as its host call returns.
func (vm *VM) Unnest(result value.Value) {
	if vm == nil || vm.outer == nil {
		return
	}
	link := vm.outer
	vm.outer = nil
	vm.callFloor = 0

	// A VM nested in this one may have handed back after this one's latest host call returned.
	if vm.nested.returned.Load() != nil {
		result = vm.takeBack(result)
	}
	var fault *Diagnostic
	if diagnostic, ok := DiagnosticFrom(result); ok && diagnostic.Code != DiagnosticRuntimeError {
		fault = diagnostic
	}
	link.outer.nested.inner.CompareAndSwap(link, nil)

	spent := uint64(0)
	if vm.Energy > link.startEnergy {
		spent = vm.Energy - link.startEnergy
	}
	link.outer.receive(link.outerLease, spent, fault)
}

// checkpoint runs every cancellationCheckInterval instructions: a cancelled context ends the run; what
// nested VMs handed back outside a host call (one started by a property read, or on another
// goroutine) is taken; and the VM shows again what it has left, so that such a VM never sees
// figures older than one interval.
func (vm *VM) checkpoint(ip int) (value.Value, bool) {
	if vm.Context != nil {
		if err := vm.Context.Err(); err != nil {
			return vm.diagnosticValue(
				DiagnosticCancelled,
				fmt.Sprintf("Execution Cancelled: %v", err),
				ip,
			), true
		}
	}
	if vm.nested.returned.Load() != nil {
		if failure := vm.takeBack(value.Value{K: value.Nil}); failure.K == value.Invalid {
			return failure, true
		}
	}
	vm.showHost()
	return value.Value{}, false
}

// innermost follows the inner links from outer to the VM nested deepest inside it, the one whose host
// call runs now, and returns it with its lease and its current inner link (nil, or one to replace).
// A link is not followed once either of its VMs has gone back to the pool.
func innermost(outer *VM) (*VM, uint64, *nestLink) {
	host, lease := outer, outer.nested.lease.Load()
	for range nestingWalkLimit {
		link := host.nested.inner.Load()
		if link == nil ||
			link.outerLease != lease ||
			link.inner.nested.lease.Load() != link.innerLease {
			return host, lease, link
		}
		host, lease = link.inner, link.innerLease
	}
	return host, lease, host.nested.inner.Load()
}

// showHost tells the VMs that may run inside the coming host call what this VM has left.
func (vm *VM) showHost() {
	remaining := shownEnergyMask
	if vm.MaxEnergy > 0 {
		remaining = 0
		if vm.Energy < vm.MaxEnergy {
			remaining = min(vm.MaxEnergy-vm.Energy, shownEnergyMask-1)
		}
	}
	depth := min(max(vm.callFloor+vm.FrameIdx+1, 1), shownDepthLimit) // never 0: see nesting.shown
	vm.nested.shown.Store(uint64(depth)<<shownEnergyBits | remaining)
}

// receive keeps what a nested VM handed back to lease of vm until vm takes it.
func (vm *VM) receive(lease, energy uint64, fault *Diagnostic) {
	if energy == 0 && fault == nil {
		return
	}
	for {
		current := vm.nested.returned.Load()
		next := &handBack{lease: lease, energy: energy, fault: fault}
		if current != nil && current.lease == lease {
			next.energy = current.energy + energy
			if next.energy < current.energy {
				next.energy = ^uint64(0)
			}
			if current.fault != nil {
				next.fault = current.fault
			}
		}
		if vm.nested.returned.CompareAndSwap(current, next) {
			return
		}
	}
}

// takeBack adds to vm what the VMs nested in its host calls handed back. result is what the host
// call returned; takeBack returns it, or the failure that replaces it: a nested VM's failure that ends
// a run, or the energy limit when what they spent leaves vm past its own.
func (vm *VM) takeBack(result value.Value) value.Value {
	back := vm.nested.returned.Swap(nil)
	if back == nil || back.lease != vm.nested.lease.Load() {
		return result
	}
	if ^uint64(0)-vm.Energy < back.energy {
		vm.Energy = ^uint64(0)
	} else {
		vm.Energy += back.energy
	}
	switch {
	case back.fault != nil:
		failure := vm.nestedFailure(back.fault)
		if diagnostic, ok := DiagnosticFrom(result); ok && diagnostic != back.fault {
			failure = mergeFailures(failure, result)
		}
		return failure
	case vm.MaxEnergy > 0 && vm.Energy > vm.MaxEnergy:
		return vm.diagnosticValue(
			DiagnosticEnergyLimit,
			"Energy Limit Exceeded: Execution halted",
			vm.currentInstruction(),
		)
	}
	return result
}

// nestedFailure is fault, the failure that ended a VM nested in vm's current host call, as vm's own:
// the same code and message, the nested frames first and vm's below them, so the whole way down shows.
func (vm *VM) nestedFailure(fault *Diagnostic) value.Value {
	own := vm.buildDiagnostic(fault.Code, fault.Message, vm.currentInstruction())
	merged := *fault
	merged.Stack = append(append([]StackFrame(nil), fault.Stack...), own.Stack...)
	merged.Suppressed = append([]*Diagnostic(nil), fault.Suppressed...)
	if len(fault.Stack) == 0 {
		merged.IP = own.IP
		merged.File = own.File
		merged.Line = own.Line
		merged.Column = own.Column
		merged.Function = own.Function
	}
	return diagnosticResult(&merged)
}

// resetNesting drops every tie of a VM going back to the pool. The new lease makes whatever still
// names the old one (a link, a hand-back) inert.
func (vm *VM) resetNesting() {
	vm.withdrawLambdas(0) // none after a normal run; an interpreter panic can leave some
	if cap(vm.passing) > maxReusableDefers {
		vm.passing = nil
	}
	vm.nested.lease.Add(1)
	vm.nested.shown.Store(0)
	vm.nested.returned.Store(nil)
	vm.nested.inner.Store(nil)
	vm.nested.context.Store(nil)
	vm.contextShown = nil
	vm.outer = nil
	vm.callFloor = 0
}

// lambdaCallers records, for each lambda passed to a host call in progress, the VMs that passed it.
// An entry lives exactly as long as its host call; shards keep unrelated calls off one lock.
var (
	lambdaCallers    [lambdaCallerShards]lambdaCallerShard
	lambdaCallerSeed = maphash.MakeSeed()
)

const lambdaCallerShards = 64

type lambdaCallerShard struct {
	mu      sync.Mutex
	callers map[*value.Lambda][]*VM
}

func lambdaShard(lambda *value.Lambda) *lambdaCallerShard {
	return &lambdaCallers[maphash.Comparable(lambdaCallerSeed, lambda)%lambdaCallerShards]
}

// LambdaCaller is the VM that passed lambda to a host call in progress, the VM whose budget a host
// running lambda in a VM of its own nests it in (NestIn); nil when no host call in progress was
// passed lambda as an argument. When several VMs were (one lambda value used by several requests at
// once), it is the one with the most frames in use: nested there, a VM has no more room than in its
// real caller, so nesting stays bounded either way.
func LambdaCaller(lambda *value.Lambda) *VM {
	if lambda == nil {
		return nil
	}
	shard := lambdaShard(lambda)
	shard.mu.Lock()
	defer shard.mu.Unlock()
	var deepest *VM
	var depth uint64
	for _, caller := range shard.callers[lambda] {
		if callerDepth := caller.nested.shown.Load() >> shownEnergyBits; deepest == nil || callerDepth > depth {
			deepest, depth = caller, callerDepth
		}
	}
	return deepest
}

// passLambdas records vm as the caller of the lambdas among args for the host call it is starting,
// and shows its Context for the VMs that may run them. It returns the mark withdrawLambdas takes the
// records back to when the call returns.
func (vm *VM) passLambdas(args []value.Value) int {
	mark := len(vm.passing)
	for _, argument := range args {
		lambda, ok := argument.V.(*value.Lambda)
		if !ok || lambda == nil {
			continue
		}
		shard := lambdaShard(lambda)
		shard.mu.Lock()
		if shard.callers == nil {
			shard.callers = make(map[*value.Lambda][]*VM)
		}
		shard.callers[lambda] = append(shard.callers[lambda], vm)
		shard.mu.Unlock()
		vm.passing = append(vm.passing, lambda)
	}
	if len(vm.passing) > mark && vm.contextShown != vm.Context {
		vm.contextShown = vm.Context
		vm.nested.context.Store(&shownContext{context: vm.Context})
	}
	return mark
}

// withdrawLambdas removes the records passLambdas made since mark.
func (vm *VM) withdrawLambdas(mark int) {
	for index := len(vm.passing) - 1; index >= mark; index-- {
		lambda := vm.passing[index]
		vm.passing[index] = nil
		shard := lambdaShard(lambda)
		shard.mu.Lock()
		callers := shard.callers[lambda]
		for position := len(callers) - 1; position >= 0; position-- {
			if callers[position] == vm {
				callers = append(callers[:position], callers[position+1:]...)
				break
			}
		}
		if len(callers) == 0 {
			delete(shard.callers, lambda)
		} else {
			shard.callers[lambda] = callers
		}
		shard.mu.Unlock()
	}
	if len(vm.passing) > mark {
		vm.passing = vm.passing[:mark]
	}
}

// NativePanic is the failure of a Go panic that escaped a nested run outside any host call of its
// VM: NATIVE_PANIC, the failure a panic in a host call is, which ends the run and safe() cannot catch.
func NativePanic(label string, recovered any) value.Value {
	return diagnosticResult(&Diagnostic{
		Code:    DiagnosticNativePanic,
		Message: nativePanicMessage(label, recovered),
		IP:      -1,
	})
}

func nativePanicMessage(label string, recovered any) string {
	return fmt.Sprintf("Native panic in %s: %v", label, recovered)
}
