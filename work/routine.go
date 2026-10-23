package work

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/kitwork/engine/app"
	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
)

// ── kitwork/routine (KITWORK_ROUTINE.md) ──────────────────────────────────────────────────────────
//
//	import * as routine from "kitwork/routine";
//	const [users, products] = routine.wait(() => loadUsers(), () => loadProducts());   // at once, in input order
//	routine.go(() => sendWelcome(email));                                              // answer first, work after
//	const report = routine.go(() => buildReport(day));
//	const result = routine.wait(report);                                               // a started one, never again
//
// Each function runs in a pooled VM of its own, with the app's MaxEnergy and frame ceiling of its own, on a
// copy of what it reads from outside (routine_snapshot.go). Both live in memory only: work that must
// survive a restart is a job of kitwork/queue.
//
//   - go(fn…) starts each function and gives a handle at once (an array of them for several). The routine
//     belongs to the tenant that started it, the site generation (or the app's own tenant, for a job): it
//     outlives the request, and stops when the generation is replaced or the site removed, or the app
//     closes; a site is not evicted from memory while one of its routines runs. A failure no wait reads
//     goes to the log, with its place, once the call that started the routine is over.
//   - wait(fn…) starts every function, then waits for all of them, and gives their results in input order;
//     the functions belong to the caller, whose context stops them. wait(handle…) waits for what go
//     started, never running it again: the same handle gives the same result.
//   - retry(n) is n attempts more, timeout("…") bounds each attempt (TIMEOUT), as the queue's, the
//     schedules' and HTTP's. cancel(handle…) asks the routine to stop: a wait of it then fails CANCELLED,
//     which safe catches.
//
// The module object is built where the file imports it, once, by the VM that loads the file; a call of it
// is handed the VM that calls it (runtime.CallerMethods, value.CallerFunction), for the context the call
// runs under, where it is written, and whether the file is still loading (a file only declares).

var (
	// errRoutineCancelled is the cause a routine stops with when routine.cancel asks it to.
	errRoutineCancelled = errors.New("routine.cancel")
	// errRoutineTimeout is the cause an attempt stops with past its .timeout().
	errRoutineTimeout = errors.New("routine: hết giờ của lần thử")
	// errRoutineStopped is the cause a routine of go stops with when its app stops or is rebuilt.
	errRoutineStopped = errors.New("routine: app dừng hay dựng lại")
)

// routineLimit is how many routines one app runs at once, those of go and the functions of wait together
// (mục 9): past it go() and wait() fail, and nothing waits in line. The spec leaves the number open (mục
// 10.4). A var so tests can lower it.
var routineLimit = 1024

const (
	routineRetryMaximum   = 100
	routineTimeoutMaximum = 24 * time.Hour
)

// routineWords are the words of the module.
var routineWords = map[string]bool{"go": true, "wait": true, "cancel": true, "retry": true, "timeout": true}

// Routine is kitwork/routine: the module, or a builder its modifiers made (routine.retry(2).timeout("5s")).
// A builder never changes: each modifier makes another, so the module stays as it was for every request.
type Routine struct {
	tenant      *Tenant
	retry       int
	retrySet    bool
	timeout     time.Duration
	timeoutText string
	timeoutSet  bool
}

// Routine is the module: import * as routine from "kitwork/routine".
func (w *KitWork) Routine() value.Value {
	return value.Value{K: value.Proxy, V: &Routine{tenant: w.tenant}}
}

func (r *Routine) OnGet(key string) value.Value {
	if !routineWords[key] {
		return value.InvalidFailure(value.InvalidArgumentCode, compiler.RoutineUnknownWord(key))
	}
	// read by name (import { go } from "kitwork/routine"): called alone it is still handed its caller
	return value.NewCallerFunction(func(caller any, args []value.Value) value.Value {
		vm, _ := caller.(*runtime.VM)
		return r.call(vm, key, args)
	})
}

func (r *Routine) OnCompare(_ string, _ value.Value) value.Value { return value.NewNil() }

// OnInvoke is a call by a host, which lends no VM.
func (r *Routine) OnInvoke(method string, args ...value.Value) value.Value {
	return r.call(nil, method, args)
}

// InvokeFrom is routine.<method>(…) written in a program: caller is the VM that runs it.
func (r *Routine) InvokeFrom(caller *runtime.VM, method string, args []value.Value) (value.Value, bool) {
	return r.call(caller, method, args), true
}

func (r *Routine) call(caller *runtime.VM, method string, args []value.Value) value.Value {
	switch method {
	case "retry":
		return r.withRetry(args)
	case "timeout":
		return r.withTimeout(args)
	case "go":
		return r.start(caller, args)
	case "wait":
		return r.wait(caller, args)
	case "cancel":
		return r.cancel(args)
	}
	return value.InvalidFailure(value.InvalidArgumentCode, compiler.RoutineUnknownWord(method))
}

// withRetry is .retry(n): n attempts more after the first, .retry(2) three in all (mục 5).
func (r *Routine) withRetry(args []value.Value) value.Value {
	if r.retrySet {
		return value.InvalidFailure(value.InvalidArgumentCode, ".retry nối hai lần: một lời gọi một luật, routine.retry(2)")
	}
	if len(args) != 1 || args[0].K != value.Number || args[0].N < 0 || args[0].N != math.Trunc(args[0].N) || args[0].N > routineRetryMaximum {
		return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf(".retry(n) nhận số lần thử thêm, số nguyên từ 0 tới %d: .retry(2) là ba lần tổng cộng", routineRetryMaximum))
	}
	next := *r
	next.retry, next.retrySet = int(args[0].N), true
	return value.Value{K: value.Proxy, V: &next}
}

// withTimeout is .timeout(hạn): the longest one attempt may run, "5s", "500ms".
func (r *Routine) withTimeout(args []value.Value) value.Value {
	if r.timeoutSet {
		return value.InvalidFailure(value.InvalidArgumentCode, ".timeout nối hai lần: một lời gọi một luật, routine.timeout(\"5s\")")
	}
	if len(args) != 1 || args[0].K != value.String {
		return value.InvalidFailure(value.InvalidArgumentCode, ".timeout(hạn) nhận một khoảng viết bằng chuỗi: .timeout(\"5s\"), \"500ms\", \"1m\"")
	}
	text := strings.TrimSpace(args[0].Text())
	duration, err := ParseDuration(text)
	if err != nil || duration < time.Millisecond || duration > routineTimeoutMaximum {
		return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf(".timeout(%q): hạn của một lần thử là một khoảng từ 1ms tới 24h: \"5s\", \"500ms\", \"1m\"", text))
	}
	next := *r
	next.timeout, next.timeoutText, next.timeoutSet = duration, text, true
	return value.Value{K: value.Proxy, V: &next}
}

// routineRules are what retry and timeout set for the routines one call starts.
type routineRules struct {
	retry       int
	timeout     time.Duration
	timeoutText string
}

func (r *Routine) rules() routineRules {
	return routineRules{retry: r.retry, timeout: r.timeout, timeoutText: r.timeoutText}
}

// ── go ─────────────────────────────────────────────────────────────────────────────────────────────

// start is go(fn…): each function starts now, and the call gives its handle (an array for several).
func (r *Routine) start(caller *runtime.VM, args []value.Value) value.Value {
	if failure := routineLoading(caller, "go"); failure.IsError {
		return failure
	}
	functions, failure := routineFunctions("go", args)
	if failure.IsError {
		return failure
	}
	tenant := r.tenant
	if tenant == nil || tenant.appRuntime == nil {
		return value.InvalidFailure(value.UnavailableCode, "routine.go: không có app để chạy routine")
	}
	group := tenant.routineGroup()
	tasks, done, err := tenant.appRuntime.Tasks().StartWithin(len(functions), routineLimit)
	if err != nil {
		return routineRefused("go", err)
	}
	if !group.enter(len(functions)) {
		for range functions {
			done()
		}
		return routineRefused("go", app.ErrTasksClosing)
	}
	place := routinePlace(caller)
	// the call that started the routines: a failure no wait has read when it is over goes to the log
	var scope context.Context
	if caller != nil {
		scope = caller.Context
	}
	rules := r.rules()
	detached := &routineRequestRules{detached: true}
	handles := make([]value.Value, len(functions))
	for index, fn := range functions {
		copied := copyRoutineLambda(fn, detached)
		ctx, stop := context.WithCancelCause(group.ctx)
		// the app's close stops it too, wherever the tenant is
		unlink := context.AfterFunc(tasks, func() { stop(errRoutineStopped) })
		handle := &routineHandle{tenant: tenant, place: place, scope: scope, done: make(chan struct{}), stop: stop}
		go func() {
			defer done()
			defer group.leave()
			defer unlink()
			defer stop(nil)
			defer func() {
				if recovered := recover(); recovered != nil {
					handle.end(runtime.NativePanic("routine", recovered))
				}
			}()
			handle.end(tenant.runRoutine(ctx, copied, rules))
		}()
		handles[index] = value.Value{K: value.Proxy, V: handle}
	}
	if len(handles) == 1 {
		return handles[0]
	}
	return value.New(handles)
}

// ── wait ───────────────────────────────────────────────────────────────────────────────────────────

// wait is wait(fn…), which starts every function then waits for all of them, or wait(handle…), which waits
// for what go started. Not both in one call (mục 4).
func (r *Routine) wait(caller *runtime.VM, args []value.Value) value.Value {
	if failure := routineLoading(caller, "wait"); failure.IsError {
		return failure
	}
	if len(args) == 0 {
		return value.InvalidFailure(value.InvalidArgumentCode, "routine.wait nhận ít nhất một hàm hay một handle: routine.wait(() => …), routine.wait(handle)")
	}
	var functions []*value.Lambda
	var handles []*routineHandle
	for index, argument := range args {
		if handle, ok := argument.V.(*routineHandle); ok && argument.K == value.Proxy {
			handles = append(handles, handle)
			continue
		}
		if lambda, ok := argument.V.(*value.Lambda); ok && lambda != nil && argument.K == value.Func {
			functions = append(functions, lambda)
			continue
		}
		if argument.K == value.Array {
			return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("routine.wait nhận từng handle, không nhận mảng (đối số thứ %d): const [a, b] = routine.go(f, g); routine.wait(a, b)", index+1))
		}
		return value.InvalidFailure(value.InvalidArgumentCode, compiler.RoutineArgumentNotAFunction("wait", value.KindName(argument), index+1))
	}
	if len(functions) > 0 && len(handles) > 0 {
		return value.InvalidFailure(value.InvalidArgumentCode, compiler.RoutineWaitMixed)
	}
	if len(handles) > 0 {
		if r.retrySet || r.timeoutSet {
			return value.InvalidFailure(value.InvalidArgumentCode, compiler.RoutineRulesOnHandle)
		}
		return waitHandles(caller, handles)
	}
	return r.waitFunctions(caller, functions)
}

// waitFunctions starts every function, each on a copy of what it reads, under the caller's context, then
// waits for all of them: never fewer, so nothing of the request is read once wait has answered.
func (r *Routine) waitFunctions(caller *runtime.VM, functions []*value.Lambda) value.Value {
	tenant := r.tenant
	if tenant == nil || tenant.appRuntime == nil {
		return value.InvalidFailure(value.UnavailableCode, "routine.wait: không có app để chạy routine")
	}
	var parent context.Context
	if caller != nil {
		parent = caller.Context
	}
	if parent == nil {
		parent = tenant.routineGroup().ctx
	}
	_, done, err := tenant.appRuntime.Tasks().StartWithin(len(functions), routineLimit)
	if err != nil {
		return routineRefused("wait", err)
	}
	rules := r.rules()
	attached := &routineRequestRules{lock: &sync.Mutex{}}
	copies := make([]*value.Lambda, len(functions))
	for index, fn := range functions {
		copies[index] = copyRoutineLambda(fn, attached)
	}
	results := make([]value.Value, len(copies))
	var running sync.WaitGroup
	running.Add(len(copies))
	for index := range copies {
		ctx, stop := context.WithCancelCause(parent)
		go func() {
			defer running.Done()
			defer done()
			defer stop(nil)
			defer func() {
				if recovered := recover(); recovered != nil {
					results[index] = runtime.NativePanic("routine", recovered)
				}
			}()
			results[index] = tenant.runRoutine(ctx, copies[index], rules)
		}()
	}
	running.Wait()
	return routineOutcome(caller, "hàm", results)
}

// waitHandles waits for each handle, in input order, until each has ended or the caller's context is done.
func waitHandles(caller *runtime.VM, handles []*routineHandle) value.Value {
	var stop <-chan struct{}
	if caller != nil && caller.Context != nil {
		stop = caller.Context.Done()
	}
	// every handle is waited for from now: a failure that ends meanwhile is this call's to report
	for _, handle := range handles {
		handle.attach()
	}
	results := make([]value.Value, len(handles))
	for index, handle := range handles {
		result, ended := handle.wait(stop)
		if !ended {
			for _, rest := range handles[index+1:] {
				rest.abandon()
			}
			return value.InvalidFailure(value.CancelledCode, "routine.wait: request đã xong trong lúc đợi routine")
		}
		results[index] = routineAt(caller, result)
	}
	return routineOutcome(caller, "handle", results)
}

// routineOutcome is what a wait gives: the result of its one function or handle, failure included as it
// is; for several, their results in input order, or the failure of the first that failed in input order,
// wrapped (its cause). A failure nothing catches (a limit of the host, the request over) ends the call
// whichever function had it.
func routineOutcome(caller *runtime.VM, noun string, results []value.Value) value.Value {
	for _, result := range results {
		if routineUncatchable(result) {
			return result
		}
	}
	if len(results) == 1 {
		return results[0]
	}
	for index, result := range results {
		if result.K == value.Invalid {
			_, message, _ := routineFailureParts(result)
			return runtime.WrapFailure(caller, fmt.Sprintf("routine.wait: %s thứ %d trong %d hỏng: %s", noun, index+1, len(results), message), result)
		}
	}
	return value.New(results)
}

// routineUncatchable reports a failure no safe catches.
func routineUncatchable(result value.Value) bool {
	if result.K != value.Invalid {
		return false
	}
	if diagnostic, ok := runtime.DiagnosticFrom(result); ok {
		return diagnostic.Code != runtime.DiagnosticRuntimeError
	}
	failure, ok := value.FailureFrom(result)
	return ok && failure.Code == value.CancelledCode
}

// routineAt gives a failure that has no place of its own (a routine cancelled before its VM ran) the
// place of the wait that reads it.
func routineAt(caller *runtime.VM, result value.Value) value.Value {
	diagnostic, ok := runtime.DiagnosticFrom(result)
	if !ok || diagnostic.Line > 0 || caller == nil {
		return result
	}
	at := caller.CallSite()
	placed := *diagnostic
	placed.File, placed.Line, placed.Column = at.File, at.Line, at.Column
	return placed.Failure()
}

// ── cancel ─────────────────────────────────────────────────────────────────────────────────────────

// cancel asks each routine to stop (mục 7): a request, not a guarantee it has stopped. A routine that has
// ended keeps its result. What the call gives is open (mục 10.2): null.
func (r *Routine) cancel(args []value.Value) value.Value {
	if r.retrySet || r.timeoutSet {
		return value.InvalidFailure(value.InvalidArgumentCode, "routine.cancel không đi sau retry hay timeout: cancel chỉ xin dừng, routine.cancel(handle)")
	}
	if len(args) == 0 {
		return value.InvalidFailure(value.InvalidArgumentCode, "routine.cancel nhận handle của routine.go: const task = routine.go(fn); routine.cancel(task)")
	}
	handles := make([]*routineHandle, len(args))
	for index, argument := range args {
		handle, ok := argument.V.(*routineHandle)
		if !ok || argument.K != value.Proxy {
			return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("routine.cancel nhận handle của routine.go, không nhận %s (đối số thứ %d): const task = routine.go(fn); routine.cancel(task)", value.KindName(argument), index+1))
		}
		handles[index] = handle
	}
	for _, handle := range handles {
		handle.stop(errRoutineCancelled)
	}
	return value.Value{K: value.Nil}
}

// ── one routine: its attempts ──────────────────────────────────────────────────────────────────────

// runRoutine runs fn under ctx until it succeeds, or has no attempt left. Each attempt runs on a copy of
// fn (when there may be another), so it starts from what go() or wait() saw, never from what the attempt
// before it left. A runtime error and a timeout are tried again; a limit of the host and a stop are not.
func (t *Tenant) runRoutine(ctx context.Context, fn *value.Lambda, rules routineRules) value.Value {
	for attempt := 1; ; attempt++ {
		if ctx.Err() != nil {
			return routineStopped(ctx, nil)
		}
		run := fn
		if rules.retry > 0 {
			run = copyRoutineLambda(fn, nil)
		}
		attemptContext, cancel := ctx, context.CancelFunc(func() {})
		if rules.timeout > 0 {
			attemptContext, cancel = context.WithTimeoutCause(ctx, rules.timeout, errRoutineTimeout)
		}
		result := t.routineAttempt(attemptContext, run)
		timedOut := errors.Is(context.Cause(attemptContext), errRoutineTimeout)
		cancel()
		if result.K != value.Invalid {
			return result
		}
		diagnostic, structured := runtime.DiagnosticFrom(result)
		switch {
		case routineUncatchable(result) && ctx.Err() != nil:
			return routineStopped(ctx, diagnostic)
		case routineUncatchable(result) && timedOut && structured && diagnostic.Code == runtime.DiagnosticCancelled:
			result = routineFailure(diagnostic, value.TimeoutCode, fmt.Sprintf("hết giờ: lần thử %d chạy quá %s (.timeout(%q))", attempt, rules.timeout, rules.timeoutText))
		case !structured || diagnostic.Code != runtime.DiagnosticRuntimeError:
			return result
		}
		if attempt > rules.retry {
			return result
		}
	}
}

// routineStopped is the failure of a routine whose context ended: CANCELLED when routine.cancel asked for
// it, which safe catches; the stop of the request or of the app as it is otherwise, which nothing catches.
func routineStopped(ctx context.Context, diagnostic *runtime.Diagnostic) value.Value {
	if errors.Is(context.Cause(ctx), errRoutineCancelled) {
		return routineFailure(diagnostic, value.RoutineCancelledCode, "routine đã bị huỷ bằng routine.cancel(handle)")
	}
	if diagnostic != nil {
		return diagnostic.Failure()
	}
	return value.InvalidFailure(value.CancelledCode, fmt.Sprintf("routine dừng trước khi chạy: %v", context.Cause(ctx)))
}

// routineFailure is a runtime error with code and message, where diagnostic is (where the routine was
// stopped), or with no place when it had not run.
func routineFailure(diagnostic *runtime.Diagnostic, code, message string) value.Value {
	failure := runtime.Diagnostic{IP: -1}
	if diagnostic != nil {
		failure = *diagnostic
		failure.Suppressed = nil
	}
	failure.Code, failure.CauseCode, failure.Message, failure.Cause = runtime.DiagnosticRuntimeError, code, message, nil
	return failure.Failure()
}

// routineAttempt runs one attempt of fn in a pooled VM of its own: the tenant's globals, the app's
// MaxEnergy, ctx. A Go panic in it is NATIVE_PANIC, as in any VM's host call.
func (t *Tenant) routineAttempt(ctx context.Context, fn *value.Lambda) (result value.Value) {
	program, _ := runtime.ProgramFromRef(fn.Program)
	if program == nil {
		return value.InvalidFailure(value.InvalidArgumentCode, "routine: hàm không có bytecode")
	}
	vm := enginePool.Acquire()
	started := time.Now()
	defer func() {
		if recovered := recover(); recovered != nil {
			result = runtime.NativePanic("routine", recovered)
		}
		t.recordVMExecution(program, vm, result, time.Since(started))
		enginePool.Release(vm)
	}()
	var globals map[string]value.Value
	var builtins []value.Value
	if t.vm != nil {
		globals, builtins = t.vm.Globals, t.vm.Builtins
	}
	t.prepareExecutionVM(vm, globals, builtins)
	vm.FastResetPrepared(program)
	vm.MaxEnergy = t.MaxEnergy
	vm.Context = ctx
	if t.vm != nil {
		for name, item := range t.vm.Vars {
			vm.Vars[name] = item
		}
	}
	return vm.ExecuteLambda(fn, nil)
}

// ── handles ────────────────────────────────────────────────────────────────────────────────────────

// routineHandle is what go() gives for one routine: a reference the VM keeps, never an object a program
// builds or writes (mục 8). Its result is written once, when the routine ends.
type routineHandle struct {
	tenant *Tenant
	place  string
	// scope is the context of the call that started the routine (its request, its job, the routine around
	// it); nil when no VM called go
	scope context.Context
	stop  context.CancelCauseFunc
	done  chan struct{}

	mu       sync.Mutex
	ended    bool
	result   value.Value
	waiters  int
	read     bool // a wait has read the result
	reported bool // the result is in the log
}

func (h *routineHandle) OnGet(key string) value.Value {
	return value.InvalidFailure(value.UnsupportedCode, fmt.Sprintf("handle.%s: handle của routine chưa đọc được trạng thái (KITWORK_ROUTINE.md mục 10); đợi kết quả bằng routine.wait(handle), dừng bằng routine.cancel(handle)", key))
}

func (h *routineHandle) OnCompare(_ string, _ value.Value) value.Value { return value.NewNil() }

func (h *routineHandle) OnInvoke(method string, _ ...value.Value) value.Value { return h.OnGet(method) }

// attach counts a waiter: a failure that ends while one waits is the waiter's to read.
func (h *routineHandle) attach() {
	h.mu.Lock()
	h.waiters++
	h.mu.Unlock()
}

// end writes the routine's result, once. A failure no wait is there for is left for one to read until the
// call that started the routine is over (reportUnread): const task = routine.go(f); …; routine.wait(task)
// in one handler never logs what it reads, however soon f fails.
func (h *routineHandle) end(result value.Value) {
	h.mu.Lock()
	if h.ended {
		h.mu.Unlock()
		return
	}
	h.ended, h.result = true, result
	close(h.done)
	waiting := h.waiters > 0
	h.mu.Unlock()
	if !waiting {
		h.reportUnread()
	}
}

// reportUnread puts the routine's failure in the log once the call that started it is over, unless a
// wait reads it before: now when the call is over already (or no VM called go), else when it ends.
func (h *routineHandle) reportUnread() {
	if h.scope == nil || h.scope.Err() != nil {
		h.report()
		return
	}
	context.AfterFunc(h.scope, h.report)
}

// report writes the result to the log, once, unless a wait has read it.
func (h *routineHandle) report() {
	h.mu.Lock()
	if h.read || h.reported {
		h.mu.Unlock()
		return
	}
	h.reported = true
	result := h.result
	h.mu.Unlock()
	h.tenant.reportRoutine(result, h.place)
}

// wait waits for the routine to end, or for stop: ended is false when stop came first. Either way the
// waiter leaves. The decision is taken under the handle's lock, so a failure always reaches someone: the
// waiter if the routine ended before it left, else the log (end, abandon).
func (h *routineHandle) wait(stop <-chan struct{}) (result value.Value, ended bool) {
	select {
	case <-h.done:
	case <-stop:
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.waiters--
	if !h.ended {
		return value.Value{}, false
	}
	h.read = true
	// every waiter gets its own copy: two requests never hold one object of a routine's result
	return copyRoutineValue(h.result), true
}

// abandon is a waiter attached and gone before it waited (the caller's context ended on another handle):
// a failure that ended while it was there, and no one read, is the log's.
func (h *routineHandle) abandon() {
	h.mu.Lock()
	h.waiters--
	unread := h.ended && h.waiters == 0 && !h.read
	h.mu.Unlock()
	if unread {
		h.reportUnread()
	}
}

// ── reports ────────────────────────────────────────────────────────────────────────────────────────

// reportRoutine writes a line for a routine whose result nobody read (mục 6: "lỗi không bao giờ mất im
// lặng"): an error for a failure, with its code, message and place; a warning for one stopped because its
// app stopped or was rebuilt. A success, and a routine routine.cancel stopped, say nothing.
func (t *Tenant) reportRoutine(result value.Value, place string) {
	if result.K != value.Invalid {
		return
	}
	code, message, at := routineFailureParts(result)
	if code == value.RoutineCancelledCode {
		return
	}
	appName, site := t.routineOwner()
	attributes := []any{"app", appName, "site", site, "code", code, "message", message, "at", at, "routine", place}
	if code == string(runtime.DiagnosticCancelled) {
		slog.Warn("Kitwork routine stopped", attributes...)
		return
	}
	if diagnostic, ok := runtime.DiagnosticFrom(result); ok {
		attributes = append(attributes, "function", diagnostic.Function, "diagnostic", result.Text())
	}
	slog.Error("Kitwork routine failed", attributes...)
}

// routineFailureParts are the code (one of the set for an error safe catches, the host's own for one it
// does not), the message and the place (file:line:column) of a failure.
func routineFailureParts(result value.Value) (code, message, at string) {
	if diagnostic, ok := runtime.DiagnosticFrom(result); ok {
		code = string(diagnostic.Code)
		if diagnostic.Code == runtime.DiagnosticRuntimeError {
			code = value.ClosedCode(diagnostic.CauseCode)
		}
		if diagnostic.Line > 0 {
			at = fmt.Sprintf("%s:%d:%d", diagnostic.File, diagnostic.Line, diagnostic.Column)
		}
		return code, diagnostic.Message, at
	}
	failure, _ := value.FailureFrom(result)
	if failure.Code == value.CancelledCode {
		return string(runtime.DiagnosticCancelled), failure.Message, ""
	}
	return value.ClosedCode(failure.Code), failure.Message, ""
}

// routineOwner is the app and the site a routine runs for, as the log names them.
func (t *Tenant) routineOwner() (string, string) {
	if t == nil || t.entity == nil {
		return "", ""
	}
	return t.appID(), t.entity.Domain
}

// routinePlace is where a call of the module is written, file:line:column; "" when no VM calls it.
func routinePlace(caller *runtime.VM) string {
	if caller == nil {
		return ""
	}
	at := caller.CallSite()
	if at.Line <= 0 {
		return ""
	}
	return fmt.Sprintf("%s:%d:%d", at.File, at.Line, at.Column)
}

// routineRefused is the failure of a go or a wait the app does not start: past the app's limit (mục 9: no
// line to wait in), or while it stops.
func routineRefused(word string, err error) value.Value {
	if errors.Is(err, app.ErrTaskLimit) {
		return value.InvalidFailure(value.DeniedCode, fmt.Sprintf("routine.%s: app đã chạy tới trần %d routine cùng lúc; không bắt đầu thêm, không xếp hàng (KITWORK_ROUTINE.md mục 9)", word, routineLimit))
	}
	return value.InvalidFailure(value.UnavailableCode, fmt.Sprintf("routine.%s: app đang dừng hay dựng lại, không bắt đầu routine mới", word))
}

// routineFunctions are the functions of go(fn…): one at least, each a function the program wrote.
func routineFunctions(word string, args []value.Value) ([]*value.Lambda, value.Value) {
	if len(args) == 0 {
		return nil, value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("routine.%s nhận ít nhất một hàm: routine.%s(() => …)", word, word))
	}
	functions := make([]*value.Lambda, len(args))
	for index, argument := range args {
		if _, isHandle := argument.V.(*routineHandle); isHandle {
			return nil, value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("routine.%s nhận hàm, không nhận handle: handle đã chạy, đợi nó bằng routine.wait(handle)", word))
		}
		lambda, ok := argument.V.(*value.Lambda)
		if !ok || lambda == nil || argument.K != value.Func {
			return nil, value.InvalidFailure(value.InvalidArgumentCode, compiler.RoutineArgumentNotAFunction(word, value.KindName(argument), index+1))
		}
		functions[index] = lambda
	}
	return functions, value.Value{K: value.Nil}
}

// ── where routines may start, and what stops them ──────────────────────────────────────────────────

// loadingVMs are the VMs running a file as it loads (a folder's router, a file of .shared/queue/ or of
// .schedule/, a config.work): a go or a wait they call is at a file's top level, where a file only
// declares (mục 9).
var loadingVMs sync.Map

// loadingFile marks vm as running a file as it loads, until the function it gives is called.
func loadingFile(vm *runtime.VM) func() {
	loadingVMs.Store(vm, struct{}{})
	return func() { loadingVMs.Delete(vm) }
}

func routineLoading(caller *runtime.VM, word string) value.Value {
	if caller != nil {
		if _, loading := loadingVMs.Load(caller); loading {
			return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("routine.%s ở cấp file: lúc nạp, file chỉ khai; gọi routine.%s trong một handler, một job, một lịch hay một hàm (KITWORK_ROUTINE.md mục 9)", word, word))
		}
	}
	return value.Value{K: value.Nil}
}

// routineGroup is the routines of go one tenant started: they outlive their request, and stop when the
// tenant closes (its site generation replaced, the site removed, the app closed).
type routineGroup struct {
	ctx     context.Context
	stop    context.CancelCauseFunc
	mu      sync.Mutex
	wg      sync.WaitGroup
	running int
	closed  bool
}

func (t *Tenant) routineGroup() *routineGroup {
	t.routinesMu.Lock()
	defer t.routinesMu.Unlock()
	if t.routines == nil {
		ctx, stop := context.WithCancelCause(context.Background())
		t.routines = &routineGroup{ctx: ctx, stop: stop}
	}
	return t.routines
}

// enter counts count routines in, unless the group is closed.
func (g *routineGroup) enter(count int) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return false
	}
	g.running += count
	g.wg.Add(count)
	return true
}

func (g *routineGroup) leave() {
	g.mu.Lock()
	g.running--
	g.mu.Unlock()
	g.wg.Done()
}

func (g *routineGroup) count() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.running
}

// RunningRoutines is how many routines of go this tenant started are running: a site with one is not
// idle, and the engine does not evict it from memory (core.Engine's cleanup), since go() lives as long as
// its app, not as long as the site is visited (KITWORK_ROUTINE.md mục 3).
func (t *Tenant) RunningRoutines() int {
	if t == nil {
		return 0
	}
	t.routinesMu.Lock()
	group := t.routines
	t.routinesMu.Unlock()
	if group == nil {
		return 0
	}
	return group.count()
}

// routineStopGrace is how long a tenant that closes waits for its routines to stop. A VM stops at its
// next checkpoint, at once; a host call blocked outside it (an HTTP request that does not hear the stop)
// only when it returns. Past the grace the tenant goes on closing without it, so a hot reload, which
// closes the generation it replaces on the request that saw the change, is held no longer: the routine
// stops when its host call returns, and its line goes to the log. A var so tests can lower it.
var routineStopGrace = 2 * time.Second

// stopRoutines stops the routines this tenant started with go and waits for them, for routineStopGrace at
// most (Tenant.Close, once its requests are over): a routine of a generation replaced runs no further
// against what the generation closes, but one blocked in a host call that does not hear the stop.
func (t *Tenant) stopRoutines() {
	group := t.routineGroup()
	group.mu.Lock()
	group.closed = true
	group.mu.Unlock()
	group.stop(errRoutineStopped)
	stopped := make(chan struct{})
	go func() {
		group.wg.Wait()
		close(stopped)
	}()
	timer := time.NewTimer(routineStopGrace)
	defer timer.Stop()
	select {
	case <-stopped:
	case <-timer.C:
		appName, site := t.routineOwner()
		slog.Warn("Kitwork routines still running after their generation closed",
			"app", appName, "site", site, "running", group.count(), "waited", routineStopGrace.String())
	}
}
