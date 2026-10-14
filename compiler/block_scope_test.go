package compiler

import (
	"encoding/hex"
	"strings"
	"testing"

	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
)

// Block scope (14/10/2026): const and let live in the block that declares them, as in JavaScript. The
// compiler resolves every name and gives each variable a slot of its own (scope.go); the VM is
// unchanged. Each case says what JavaScript gives and what Kitwork gave before.

type scopeCase struct {
	name   string
	source string
	want   string
}

func runScopeCases(t *testing.T, cases []scopeCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := grammarResult(t, tc.source); got != tc.want {
				t.Fatalf("%s\n got %s, want %s", tc.source, got, tc.want)
			}
		})
	}
}

// A const or let of a block is that block's: it never changes a variable of the same name outside,
// and is not seen after its block. Until 14/10/2026 they shared one variable of the function.
func TestConstAndLetLiveInTheirBlock(t *testing.T) {
	runScopeCases(t, []scopeCase{
		// was [2, 2]: the inner const overwrote the outer let
		{"inner-const-keeps-outer", `let x = 1; let seen = 0; if (true) { const x = 2; seen = x; } var result = [x, seen];`, `[1,2]`},
		// was "inner"
		{"read-after-block-is-outer", `const x = "outer"; if (true) { const x = "inner"; } var result = x;`, `"outer"`},
		// was 1: the block's const outlived it; a name nothing declares around is null
		{"not-seen-after-block", `if (true) { const hidden = 1; } var result = hidden;`, `null`},
		// was 3: the counter outlived its loop
		{"counter-not-seen-after-loop", `for (let i = 0; i < 3; i++) {} var result = i;`, `null`},
		// was "b": two branches shared one variable
		{"else-has-its-own", `let out = []; if (false) { const v = "a"; } else { const v = "b"; out.push(v); } const v = "top"; out.push(v); var result = out;`, `["b","top"]`},
		{"for-of-body-own-block", `const out = []; for (const x of [1, 2]) { const x = 10; out.push(x); } var result = out;`, `[10,10]`},
		{"switch-cases-are-one-block", `let out = []; switch (1) { case 1: let x = 1; case 2: out.push(x); } var result = out;`, `[1]`},
		// a function may call one declared after it, at any level: names are known before code runs
		{"later-declaration-is-callable", `const a = () => b(); const b = () => 7; var result = a();`, `7`},
		{"later-in-a-block", `let r = 0; if (true) { const a = () => b(); const b = () => 8; r = a(); } var result = r;`, `8`},
		// a destructuring in a block declares its names there
		{"pattern-in-block", `const a = 1; if (true) { const { a, b: c } = { a: 2, b: 3 }; } var result = a;`, `1`},
	})
}

// A declaration in a nested function is that function's, even when a function around it, or the file,
// has a variable of the same name. Until 14/10/2026 STORE wrote the first variable of that name it found
// around (the design's trap: const { error } = safe(…) in a helper overwrote the view's error).
func TestNestedDeclarationNeverWritesThrough(t *testing.T) {
	runScopeCases(t, []scopeCase{
		// was "inner"
		{"design-example", `
const outer = () => {
  let error = "outer";
  const f = () => { const { error } = { error: "inner" }; return error; };
  f();
  return error;
};
var result = outer();`, `"outer"`},
		// was "f": a function wrote the file's top-level variable
		{"top-level-kept", `let error = "top"; const f = () => { const error = "f"; return error; }; var got = f(); var result = [error, got];`, `["top","f"]`},
		{"let-assigned-inside", `let n = 1; const f = () => { let n = 5; n = n + 1; return n; }; var got = f(); var result = [n, got];`, `[1,6]`},
		// was [11, 10]: assigning a parameter wrote the outer variable of its name, and the parameter kept 10
		{"parameter-assignment-stays", `
const outer = (x) => {
  const g = (x) => { x = x + 1; return x; };
  const r = g(10);
  return [x, r];
};
var result = outer(1);`, `[1,11]`},
		{"parameter-of-a-callback", `let item = "outer"; const out = [1, 2].map((item) => { item = item * 10; return item; }); var result = [item, out];`, `["outer",[10,20]]`},
	})
}

// A closure made in a loop sees the variables of its own iteration, as in JavaScript. Until 14/10/2026
// every closure of a loop saw the loop's last values.
func TestClosuresInLoopsKeepTheirIteration(t *testing.T) {
	runScopeCases(t, []scopeCase{
		// was [3, 3, 3]
		{"for-of-item", `const fns = []; for (const item of [1, 2, 3]) { fns.push(() => item); } var result = fns.map((f) => f());`, `[1,2,3]`},
		// was [3, 3, 3]
		{"counter", `const fns = []; for (let i = 0; i < 3; i++) { fns.push(() => i); } var result = fns.map((f) => f());`, `[0,1,2]`},
		// JavaScript copies the counter into the next iteration before the update: the body's i++ stays
		{"counter-changed-by-body", `const fns = []; for (let i = 0; i < 5; i++) { fns.push(() => i); i++; } var result = fns.map((f) => f());`, `[1,3,5]`},
		{"body-const", `const fns = []; for (const v of [1, 2]) { const doubled = v * 2; fns.push(() => doubled); } var result = fns.map((f) => f());`, `[2,4]`},
		// was [3, 3]: two closures of one iteration share its let, and each iteration has its own
		{"let-shared-in-iteration", `
const counters = [];
for (const name of ["a", "b"]) {
  let count = 0;
  counters.push({ inc: () => { count = count + 1; return count; }, read: () => count });
}
counters[0].inc(); counters[0].inc(); counters[1].inc();
var result = [counters[0].read(), counters[1].read()];`, `[2,1]`},
		// a callback that adds into the iteration's let, as forEach does, still adds into it
		{"accumulator", `let total = 0; for (const v of [1, 2, 3]) { let sum = 0; [v, v].forEach((x) => { sum = sum + x; }); total = total + sum; } var result = total;`, `12`},
		{"nested-loops", `const fns = []; for (const row of [1, 2]) { for (const cell of [10, 20]) { fns.push(() => row + cell); } } var result = fns.map((f) => f());`, `[11,21,12,22]`},
		{"break", `const fns = []; for (const v of [1, 2, 3]) { if (v == 2) { break; } fns.push(() => v); } var result = fns.map((f) => f());`, `[1]`},
		{"pattern-in-iteration", `const fns = []; for (const v of [1, 2]) { const { a, b: c } = { a: v, b: v * 10 }; fns.push(() => [a, c]); } var result = fns.map((f) => f());`, `[[1,10],[2,20]]`},
		{"closure-assigns-pattern", `const fns = []; for (const v of [1, 2]) { let p = 0; let q = 0; fns.push(() => { [p, q] = [v, v * 2]; return p + q; }); } var result = fns.map((f) => f());`, `[3,6]`},
		{"closure-made-before-declaration", `const fns = []; for (const v of [1, 2]) { const read = () => later; const later = v * 100; fns.push(read); } var result = fns.map((f) => f());`, `[100,200]`},
		{"nested-closure", `const fns = []; for (const v of [1, 2]) { fns.push(() => () => v); } var result = fns.map((f) => f()());`, `[1,2]`},
		// a closure that reads no variable of the iteration is made as before
		{"closure-of-outer-variable", `let base = 1; const fns = []; for (const v of [1, 2]) { fns.push(() => base); } base = 5; var result = fns.map((f) => f());`, `[5,5]`},
		{"in-a-function", `const make = () => { const fns = []; for (let i = 0; i < 2; i++) { fns.push(() => i * 10); } return fns; }; var result = make().map((f) => f());`, `[0,10]`},
		// a closure's own const is its call's, not the loop's iteration's
		{"closure-declares-its-own", `const fns = []; for (const v of [1, 2]) { fns.push((k) => { const w = v * k; return () => w; }); } const a = fns[0](1); const b = fns[0](2); var result = [a(), b()];`, `[1,2]`},
	})
}

// A closure writes a variable of its iteration in that iteration's object, through a slot of its own
// call: the map of the function that ran the loop (for a loop at the top of a file, the map every
// request's handlers share) is never written by the closure.
func TestClosureWriteNeverTouchesTheLoopsFunction(t *testing.T) {
	bytecode, err := CompileSource(`const fns = []; for (const v of [1, 2]) { let p = 0; fns.push(() => { [p] = [v * 5]; p = p + 1; return p; }); } var result = fns.map((f) => f());`)
	if err != nil {
		t.Fatal(err)
	}
	vm := runtime.New(bytecode.Program)
	vm.MaxEnergy = 10_000_000
	if returned := vm.Run(); returned.IsInvalid() {
		t.Fatal(returned.Text())
	}
	if got, _ := vm.Vars["result"].MarshalJSON(); string(got) != `[6,11]` {
		t.Fatalf("result %s, want [6,11]", got)
	}
	for name, item := range vm.Vars {
		if runtime.SlotName(name) == "p" && item.N != 0 {
			t.Fatalf("the closure wrote %s = %v in the loop's map", name, item.V)
		}
	}
}

// A loop whose closures reach none of its variables makes no object per iteration; one whose closures
// do makes one each time, and the closure is made by a call that hands it that object.
func TestIterationObjectOnlyWhenAClosureReachesTheLoop(t *testing.T) {
	plain, err := CompileSource(`const out = []; for (const v of [1, 2]) { out.push(v * 2); [v].map((x) => x + 1); }`)
	if err != nil {
		t.Fatal(err)
	}
	if ops := countOps(t, plain); ops[runtime.MAKE] != 3 || ops[runtime.CALL] != 0 {
		t.Fatalf("a loop no closure reaches: MAKE=%d CALL=%d, want the three array literals and no call", ops[runtime.MAKE], ops[runtime.CALL])
	}
	reached, err := CompileSource(`const out = []; for (const v of [1, 2]) { out.push(() => v); }`)
	if err != nil {
		t.Fatal(err)
	}
	if ops := countOps(t, reached); ops[runtime.MAKE] != 3 || ops[runtime.CALL] != 1 {
		t.Fatalf("a loop a closure reaches: MAKE=%d CALL=%d, want an object per iteration and one call making the closure", ops[runtime.MAKE], ops[runtime.CALL])
	}
}

// An assignment to a name nothing declares keeps the VM's lookup, as before: var x = … (the language
// has no var keyword; it is an assignment) gives the function a variable a nested function can change.
func TestUndeclaredAssignmentKeepsItsLookup(t *testing.T) {
	runScopeCases(t, []scopeCase{
		{"var-changed-by-closure", `const f = () => { var n = 1; const g = () => { n = n + 1; }; g(); return n; }; var result = f();`, `2`},
		{"assignment-in-block-is-the-functions", `if (true) { z = 5; } var result = z;`, `5`},
		// a nested function's const of that name is its own, the var keeps its value
		{"var-not-overwritten", `const f = () => { var state = "kept"; const g = () => { const state = "inner"; return state; }; g(); return state; }; var result = f();`, `"kept"`},
	})
}

// Declaring a name twice in one block is a compile error, as in JavaScript, naming where the first one
// is; another block, a nested block and a nested function may declare it again.
func TestRedeclarationInOneBlockIsACompileError(t *testing.T) {
	cases := []struct {
		name, source string
		says         []string
	}{
		{"const-twice", "const a = 1;\nconst a = 2;", []string{"<source>:2:7: a đã được khai báo trong cùng khối ở <source>:1:7", "const { error: saveError }"}},
		{"pattern-twice", "const { value, error } = { value: 1 };\nconst { value: v, error } = { value: 2 };", []string{"<source>:2:19: error đã được khai báo trong cùng khối ở <source>:1:16"}},
		{"in-a-block", "if (true) {\n  let x = 1;\n  const x = 2;\n}", []string{"<source>:3:9: x đã được khai báo trong cùng khối ở <source>:2:7"}},
		{"function-statement", "function f() { return 1; }\nconst f = 2;", []string{"<source>:2:7: f đã được khai báo trong cùng khối ở <source>:1:10"}},
		{"switch-cases", "switch (1) { case 1: const a = 1; break; case 2: const a = 2; }", []string{"<source>:1:56: a đã được khai báo trong cùng khối ở <source>:1:28"}},
		{"parameter", "const f = (a) => {\n  const a = 1;\n};", []string{"<source>:2:9: a đã là tham số của hàm (<source>:1:12)"}},
		{"parameters", "const f = (a, a) => a;", []string{"<source>:1:15: hàm có hai tham số cùng tên a (tham số kia ở <source>:1:12)"}},
		{"import-twice", "import { router } from \"kitwork\";\nimport { router } from \"kitwork\";", []string{"đã được khai báo trong cùng khối ở <source>:1:1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			message := grammarCompileError(t, tc.source)
			for _, part := range tc.says {
				if !strings.Contains(message, part) {
					t.Fatalf("error %q\n does not say %q", message, part)
				}
			}
		})
	}
	for _, allowed := range []string{
		`if (true) { const a = 1; } if (true) { const a = 2; } var result = 1;`,
		`const a = 1; if (true) { const a = 2; } var result = a;`,
		`const a = 1; const f = () => { const a = 2; return a; }; var result = f();`,
		`const f = (a) => { if (a) { const a = 2; return a; } return a; }; var result = f(1);`,
		`for (const x of [1]) { const x = 2; } var result = 1;`,
	} {
		if _, err := CompileSource(allowed); err != nil {
			t.Fatalf("%s: %v", allowed, err)
		}
	}
}

// A slot never reaches the author: a call of a variable that is not a function names it as written.
func TestNotAFunctionNamesTheVariableAsWritten(t *testing.T) {
	for _, tc := range []struct{ source, says string }{
		{`if (true) { const f = 3; f(); }`, "f không phải hàm (là số)"},
		{`const f = 1; const g = () => { const f = 2; f(); }; g();`, "f không phải hàm (là số)"},
		{`for (const h of [3]) { const read = () => h; h(); }`, "h không phải hàm (là số)"},
		{`for (const h of [3]) { const read = () => h(); read(); }`, "h không phải hàm (là số)"},
	} {
		_, failure := grammarRun(t, tc.source, nil)
		if failure == nil {
			t.Fatalf("%s ran", tc.source)
		}
		if !strings.Contains(failure.Message, tc.says) || strings.ContainsAny(failure.Message, "@#") {
			t.Fatalf("%s: %q, want it to say %q and no slot", tc.source, failure.Message, tc.says)
		}
	}
}

// Kitwork UI reads a view's names by name: a declaration of the view renamed because the file has the
// same name is read back under its own, and a block's is not the view's.
func TestViewScopeReadsTheViewsOwnNames(t *testing.T) {
	bytecode, err := CompileSource(`
const title = "file";
const view = (ctx) => {
  const title = "view";
  let count = 1;
  if (ctx) { const inner = 1; count = count + inner; }
  for (const item of [1]) { const doubled = item * 2; }
};`)
	if err != nil {
		t.Fatal(err)
	}
	vm := runtime.New(bytecode.Program)
	vm.MaxEnergy = 10_000_000
	if returned := vm.Run(); returned.IsInvalid() {
		t.Fatal(returned.Text())
	}
	result, names := vm.ExecuteLambdaScope(vm.Vars["view"].V.(*value.Lambda), []value.Value{value.New(true)})
	if result.IsInvalid() {
		t.Fatal(result.Text())
	}
	if names["title"].Text() != "view" || names["count"].N != 2 || names["ctx"].K != value.Bool {
		t.Fatalf("view names: %v", names)
	}
	for name := range names {
		if strings.ContainsAny(name, "@#") || name == "inner" || name == "doubled" || name == "item" {
			t.Fatalf("view names hold %q: %v", name, names)
		}
	}
	if vm.Vars["title"].Text() != "file" {
		t.Fatalf("the file's title is %q", vm.Vars["title"].Text())
	}
}

// A module's top-level name that the importing file also declares (it imports it) lives in a slot of the
// module; the module still exports it under its name.
func TestBundledModuleKeepsItsExportNames(t *testing.T) {
	entry := writeTenant(t, map[string]string{
		"app.kitwork.js": `import { label, count } from "./lib.kitwork.js";
const total = count();
var result = [label, total];`,
		"lib.kitwork.js": `export const label = "lib";
let calls = 0;
export const count = () => { const label = "inner"; calls = calls + 1; return label + calls; };`,
	})
	bytecode, err := CompileFile(entry)
	if err != nil {
		t.Fatal(err)
	}
	vm := runtime.New(bytecode.Program)
	vm.MaxEnergy = 10_000_000
	if returned := vm.Run(); returned.IsInvalid() {
		t.Fatal(returned.Text())
	}
	got, _ := vm.Vars["result"].MarshalJSON()
	if string(got) != `["lib","inner1"]` {
		t.Fatalf("result %s", got)
	}
}

// Schema 6 resolves names; an artifact a schema-5 compiler cached (no block scope) is never reused.
func TestSchema6RetiresSchema5Artifacts(t *testing.T) {
	const schema5 = "d15db8847ba6b0a15b54b23378998abf32c71b15c3633a0ac791dfcd2d039316"
	stamped := bytecode2FingerprintFor(5)
	if got := hex.EncodeToString(stamped[:]); got != schema5 {
		t.Fatalf("schema 5 on bytecode 2 stamps %s, want %s", got, schema5)
	}
	if Fingerprint() == schema5 {
		t.Fatal("the compiler still stamps the schema-5 fingerprint")
	}
}
