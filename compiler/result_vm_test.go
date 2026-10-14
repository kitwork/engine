package compiler

import (
	"strings"
	"testing"

	"github.com/kitwork/engine/builtins"
	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
)

// safe(() => x) is how a program reads a failure (14/10/2026): a global function the VM runs, giving
// a plain object { value, error }, error null or { message, code, at } (error.progress too for
// PARTIAL). It replaced x.safe(), a lowering whose result was a Go struct with ok/isError/value/
// error/code getters, which JSON wrote as null.

// runSafe compiles source and runs it with the global safe and globals, and gives the run's own
// result and its `result` variable.
func runSafe(t *testing.T, source string, globals map[string]value.Value) (value.Value, value.Value) {
	t.Helper()
	bytecode, err := CompileSource(source)
	if err != nil {
		t.Fatalf("compile %q: %v", source, err)
	}
	vm := runtime.New(bytecode.Program)
	vm.MaxEnergy = 10_000_000
	builtins.InjectJSCompat(vm.Globals) // safe among them
	for name, item := range globals {
		vm.Globals[name] = item
	}
	returned := vm.Run()
	return returned, vm.Vars["result"]
}

func failingHost(code string) map[string]value.Value {
	return map[string]value.Value{"failHost": value.NewFunc(func(...value.Value) value.Value {
		if code == "" {
			return value.Value{K: value.Invalid, V: "boom"}
		}
		return value.InvalidFailure(code, "host failed")
	})}
}

func TestSafeGivesThePlainObjectOfASuccess(t *testing.T) {
	returned, result := runSafe(t, `
const u = { id: 7, name: "ann" }
const r = safe(() => u)
const empty = safe(() => null)
var result = [r.value.id, r.error, JSON.stringify(r.value), empty.value, empty.error, r.ok, r.isError, r.code]
`, nil)
	if returned.IsInvalid() {
		t.Fatal(returned.Text())
	}
	// ok, isError and code are not keys of the result: one way to check, if (r.error)
	if got := result.Text(); got != `[7, null, {"id":7,"name":"ann"}, null, null, null, null, null]` {
		t.Fatalf("result = %s", got)
	}
	_, written := runSafe(t, `var result = JSON.stringify(safe(() => 1 + 2))`, nil)
	if got := written.Text(); got != `{"error":null,"value":3}` {
		t.Fatalf("JSON of a result = %s, want {\"error\":null,\"value\":3}", got)
	}
}

func TestSafeKeepsAReturnedClosure(t *testing.T) {
	_, result := runSafe(t, `
const base = 40
const check = safe(() => () => base + 2)
const fn = check.value
var result = fn()
`, nil)
	wantNum(t, result, 42, "safe keeps a returned closure's scope")
}

// A failure of the host the program called is caught where it is, with its code; a failure without a
// code is ERROR.
func TestSafeRescuesAFailureAndKeepsItsCode(t *testing.T) {
	for code, want := range map[string]string{"": value.DefaultFailureCode, value.ConflictCode: value.ConflictCode,
		"KITDB_TRANSACTION_CONFLICT": value.DefaultFailureCode} {
		returned, result := runSafe(t, `
const check = safe(() => failHost())
var result = [check.value, check.error.code, check.error.at]
`, failingHost(code))
		if returned.IsInvalid() {
			t.Fatal(returned.Text())
		}
		if got := result.Text(); got != "[null, "+want+", <source>:2:26]" {
			t.Errorf("host failing with %q: result = %s, want code %s at <source>:2:26", code, got, want)
		}
	}
}

// at is where the failure is: the innermost frame at the failing instruction, inside a function the
// protected one called, not the line of safe(.
func TestSafeAtIsWhereTheFailureIs(t *testing.T) {
	_, result := runSafe(t, `
const findUser = (id) => {
  const rows = [1, 2]
  return failHost(id)
}
const check = safe(() => findUser(3))
var result = [check.error.message, check.error.at]
`, failingHost(value.DatabaseCode))
	if got := result.Text(); got != "[host failed, <source>:4:10]" {
		t.Fatalf("result = %s, want the failure at findUser's line", got)
	}
	// columns count characters: Vietnamese text before the place does not move it
	_, result = runSafe(t, "const name = \"Quốc\"; const check = safe(() => failHost())\nvar result = check.error.at", failingHost(value.DatabaseCode))
	if got := result.Text(); got != "<source>:1:47" {
		t.Fatalf("at = %s, want <source>:1:47 (characters, not bytes: 49)", got)
	}
}

func TestSafeAcrossCallsAndCallbacks(t *testing.T) {
	for name, source := range map[string]string{
		"nested call": `
const run = (task) => task()
const check = safe(() => run(() => failHost()))
var result = [check.value, check.error.code, check.error.message]
`,
		"collection callback": `
const checks = [1].map(() => safe(() => failHost()))
const check = checks[0]
var result = [check.value, check.error.code, check.error.message]
`,
		"a function by its name": `
const load = () => failHost()
const check = safe(load)
var result = [check.value, check.error.code, check.error.message]
`,
		"a host function itself": `
const check = safe(failHost)
var result = [check.value, check.error.code, check.error.message]
`,
	} {
		returned, result := runSafe(t, source, failingHost(value.ConflictCode))
		if returned.IsInvalid() {
			t.Fatalf("%s: %s", name, returned.Text())
		}
		if got := result.Text(); got != "[null, CONFLICT, host failed]" {
			t.Errorf("%s: result = %s", name, got)
		}
	}
}

// The nearest safe catches; the outer sees what the inner function returned. fail is FAIL, whatever
// else it is given.
func TestSafeNests(t *testing.T) {
	_, result := runSafe(t, `
const fail = (message) => failHost(message)
const outer = safe(() => {
  const inner = safe(() => fail("hết hàng"))
  if (inner.error) return "dự phòng"
  return inner.value
})
var result = [outer.value, outer.error]
`, failingHost(value.FailCode))
	if got := result.Text(); got != "[dự phòng, null]" {
		t.Fatalf("result = %s", got)
	}
}

// What the failing function did before it failed stays done, and the program goes on after safe.
func TestSafeGoesOnAfterAFailure(t *testing.T) {
	returned, result := runSafe(t, `
var log = []
const work = () => {
  log.push("trước")
  failHost()
  log.push("sau")
}
const check = safe(work)
log.push("tiếp")
var result = [log, check.error.code]
`, failingHost(value.DatabaseCode))
	if returned.IsInvalid() {
		t.Fatal(returned.Text())
	}
	if got := result.Text(); got != "[[trước, tiếp], DATABASE]" {
		t.Fatalf("result = %s", got)
	}
}

// A limit the host puts on the program is never caught: the run ends, safe or not.
func TestSafeNeverCatchesALimit(t *testing.T) {
	energy := `
var after = 0
const spin = () => { let n = 0; for (let i = 0; i < 10000000; i++) { n = n + 1 } return n }
const check = safe(spin)
after = 1
`
	bytecode, err := CompileSource(energy)
	if err != nil {
		t.Fatal(err)
	}
	vm := runtime.New(bytecode.Program)
	vm.MaxEnergy = 100_000
	vm.Globals["safe"] = value.NewSafeFunction()
	if diagnostic, ok := runtime.DiagnosticFrom(vm.Run()); !ok || diagnostic.Code != runtime.DiagnosticEnergyLimit {
		t.Fatalf("energy inside safe = %v, want %s", diagnostic, runtime.DiagnosticEnergyLimit)
	}
	if after := vm.Vars["after"]; after.N != 0 {
		t.Fatal("the run went on after the energy limit inside safe")
	}

	returned, _ := runSafe(t, `
const deep = (n) => deep(n + 1)
const check = safe(() => deep(0))
var result = 1
`, nil)
	if diagnostic, ok := runtime.DiagnosticFrom(returned); !ok || diagnostic.Code != runtime.DiagnosticStackOverflow {
		t.Fatalf("stack overflow inside safe = %s, want %s", returned.Text(), runtime.DiagnosticStackOverflow)
	}

	returned, _ = runSafe(t, `const result = safe(() => explode())`, map[string]value.Value{
		"explode": value.NewFunc(func(...value.Value) value.Value { panic("boom") }),
	})
	diagnostic, ok := runtime.DiagnosticFrom(returned)
	if !ok || diagnostic.Code != runtime.DiagnosticNativePanic {
		t.Fatalf("native panic inside safe = %s, want %s", returned.Text(), runtime.DiagnosticNativePanic)
	}
	if last := diagnostic.Stack[len(diagnostic.Stack)-1]; last.Function != "<main>" {
		t.Fatalf("the panic's stack does not reach <main>: %s", diagnostic.Error())
	}
}

// safe given what is not a function, at run time (a name, which the compiler cannot check), fails
// outside what it protects: the run ends, or an outer safe catches it as INVALID_ARGUMENT.
func TestSafeOfWhatIsNotAFunctionFailsOutsideItself(t *testing.T) {
	returned, _ := runSafe(t, "const load = 3\nconst check = safe(load)\nvar result = 1", nil)
	if diagnostic, ok := runtime.DiagnosticFrom(returned); !ok || diagnostic.CauseCode != value.InvalidArgumentCode ||
		diagnostic.Message != "safe nhận một hàm, không nhận số: viết safe(() => …)" {
		t.Fatalf("safe(3) = %s", returned.Text())
	}
	_, result := runSafe(t, "const load = 3\nconst check = safe(() => safe(load))\nvar result = [check.error.code, check.error.at]", nil)
	if got := result.Text(); got != "[INVALID_ARGUMENT, <source>:2:26]" {
		t.Fatalf("an outer safe = %s", got)
	}
	// a call of safe the compiler cannot see (through another name) with the wrong count
	_, result = runSafe(t, "const s = safe\nconst check = safe(() => s(() => 1, 2))\nvar result = check.error.message", nil)
	if got := result.Text(); got != "safe nhận đúng một hàm, không nhận 2 đối số: viết safe(() => …)" {
		t.Fatalf("s(f, 2) = %s", got)
	}
	// safe reached as a member is the same global: the VM runs it after a dot too
	_, result = runSafe(t, "const k = { safe: safe }\nconst check = k.safe(() => 7)\nvar result = [check.value, check.error]", nil)
	if got := result.Text(); got != "[7, null]" {
		t.Fatalf("k.safe(f) = %s", got)
	}
	// an array method calls safe as it calls any function of the engine, with the element and its index
	_, result = runSafe(t, "const check = safe(() => [() => 1].map(safe))\nvar result = check.error.message", nil)
	if got := result.Text(); got != "safe nhận đúng một hàm, không nhận 2 đối số: viết safe(() => …)" {
		t.Fatalf("[f].map(safe) = %s", got)
	}
}

// const { value, error } takes the result apart, with renaming.
func TestSafeDestructures(t *testing.T) {
	_, result := runSafe(t, `
const { value: user, error: userError } = safe(() => ({ id: 1 }))
const { value: orders = [], error } = safe(() => failHost())
var result = [user.id, userError, orders, error.code]
`, failingHost(value.TimeoutCode))
	if got := result.Text(); got != "[1, null, [], TIMEOUT]" {
		t.Fatalf("result = %s", got)
	}
}

func TestSafeSurvivesArtifactRoundTrip(t *testing.T) {
	compiled, err := CompileSource(`const result = safe(() => failHost()).error.message`)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := compiled.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := UnmarshalBytecode(encoded, compiled.SourceFingerprint())
	if err != nil {
		t.Fatal(err)
	}
	report, err := runtime.InspectProgram(restored.Program)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range report.EntryPoints {
		if strings.Contains(entry.Name, "safe") {
			t.Fatalf("a program still holds a protected evaluator: %q", entry.Name)
		}
	}

	vm := runtime.New(restored.Program)
	vm.Globals["safe"] = value.NewSafeFunction()
	vm.Globals["failHost"] = failingHost(value.DatabaseCode)["failHost"]
	if result := vm.Run(); result.K == value.Invalid {
		t.Fatalf("round-trip runtime result = %s", result.Text())
	}
	if got := vm.Vars["result"].Text(); got != "host failed" {
		t.Fatalf("round-trip safe error = %q", got)
	}
}
