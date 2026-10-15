package compiler

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
)

// The language decisions of 14/10/2026 ("đồng ý hết"), each compared with JavaScript itself: a negative
// number is true, x++ gives the old value and ++x the new one, a[i] = v past the end pads with null,
// continue, and typeof. Every row is a program that is JavaScript too; want is the result Kitwork gives,
// and node, when it is on PATH, runs the same program and must give the same, or js where a row differs
// on purpose (why says why). Without node only the Kitwork column is checked.

type parityCase struct {
	name   string
	source string // sets result; safe(() => …) is given to node too
	want   string // result as JSON, from Kitwork
	js     string // result as JSON from node when it differs from want on purpose
	why    string // why it differs
}

// syntaxCase is a program JavaScript refuses with a SyntaxError and Kitwork with a compile error.
type syntaxCase struct {
	name    string
	source  string
	kitwork string // what the compile error holds
}

var truthinessCases = []parityCase{
	{"or", `const result = -1 || 5;`, `-1`, "", ""},
	{"not", `const result = [!(-1), !-0.5, !!-3];`, `[false,false,true]`, "", ""},
	{"and", `const result = -2 && "and";`, `"and"`, "", ""},
	{"if", `let result = "no"; if (-1) { result = "yes"; }`, `"yes"`, "", ""},
	{"index-of", `const result = "kitwork".indexOf("z") ? "not first, or missing" : "first";`, `"not first, or missing"`, "", ""},
	{"difference", `const a = 3; const b = 5; const result = (a - b) ? "differ" : "same";`, `"differ"`, "", ""},
	{"zeros", `const result = [0 || "zero", -0 || "minus zero", (5 - 5) || "difference zero"];`, `["zero","minus zero","difference zero"]`, "", ""},
	{"not-a-number", `const result = Math.sqrt(-1) || "not a number";`, `"not a number"`, "", ""},
	{"boolean", `const result = [Boolean(-1), Boolean(0), Boolean(-0.1)];`, `[true,false,true]`, "", ""},
	{"filter", `const result = [3, 1, 2].filter((x) => x - 2);`, `[3,1]`, "", ""},
	{"find-index", `const result = [5, 6].findIndex((x) => x - 9);`, `0`, "", ""},
	{"find", `const result = [9, 2].find((x) => x - 9);`, `2`, "", ""},
	{"some-every", `const result = [[-1, 0].some((x) => x), [-1, -2].every((x) => x), [0, -1].every((x) => x)];`, `[true,true,false]`, "", ""},
	{"condition-chain", `let result = "no"; const n = -1; if (n && n < 0) { result = "negative"; }`, `"negative"`, "", ""},
	{"right-side-not-run", `let calls = 0; const touch = () => { calls = calls + 1; return calls; }; const v = -1 || touch(); const result = [v, calls];`, `[-1,0]`, "", ""},
	{"nullish-unchanged", `const result = [-1 ?? 5, 0 ?? 5];`, `[-1,0]`, "", ""},
}

var updateCases = []parityCase{
	{"postfix-increment", `let x = 5; const a = x++; const result = [a, x];`, `[5,6]`, "", ""},
	{"postfix-decrement", `let x = 5; const a = x--; const result = [a, x];`, `[5,4]`, "", ""},
	{"prefix-increment", `let x = 5; const a = ++x; const result = [a, x];`, `[6,6]`, "", ""},
	{"prefix-decrement", `let x = 5; const a = --x; const result = [a, x];`, `[4,4]`, "", ""},
	{"in-a-sum", `let n = 3; const result = n++ + n;`, `7`, "", ""},
	{"in-an-array", `let i = 0; const result = [i++, i++, ++i, i--, i];`, `[0,1,3,3,2]`, "", ""},
	{"member", `const o = { n: 1 }; const a = o.n++; const b = ++o.n; const c = o.n--; const result = [a, b, c, o.n];`, `[1,3,3,2]`, "", ""},
	{"index", `const list = [10, 20]; const a = list[1]++; const b = --list[0]; const result = [a, b, list];`, `[20,9,[9,21]]`, "", ""},
	{"returned-by-an-arrow", `let count = 0; const next = () => count++; const result = [next(), next(), count];`, `[0,1,2]`, "", ""},
	{"returned", `let count = 10; const take = () => { return count--; }; const result = [take(), take(), count];`, `[10,9,8]`, "", ""},
	{"closure-of-an-iteration", `const fns = []; for (let i = 0; i < 2; i++) { fns.push(() => i++); } const result = [fns[0](), fns[0](), fns[1]()];`, `[0,1,1]`, "", ""},
	{"statements", `let x = 1; x++; ++x; x--; let total = 0; for (let i = 0; i < 3; i++) { total += i; } const result = [x, total];`, `[2,3]`, "", ""},
	{"condition", `let n = 0; let result = "no"; if (n++) { result = "was true"; } else { result = "was zero, now " + n; }`, `"was zero, now 1"`, "", ""},
	{"template", "let n = 1; const result = `${n++}-${n}`;", `"1-2"`, "", ""},
	{"null-operand", `let n = null; const a = n++; const result = [a, n];`, `[null,1]`, `[0,1]`,
		"Kitwork has one value for null and undefined and gives it back as the old value; JavaScript turns null into 0 (undefined into NaN)"},
	{"string-operand", `let s = "5"; const a = s++; const result = [a, s];`, `["5","51"]`, `[5,6]`,
		"x++ is x = x + 1, and + joins a string in Kitwork; JavaScript's ++ makes a number of it first (not part of this change)"},
}

var arrayWriteCases = []parityCase{
	{"in-range", `const a = [1, 2, 3]; a[0] = 9; const result = a;`, `[9,2,3]`, "", ""},
	{"at-the-length", `const a = [1]; a[1] = 2; a[a.length] = 3; const result = a;`, `[1,2,3]`, "", ""},
	{"past-the-end", `const a = [1, 2]; a[4] = 5; const result = [a, a.length];`, `[[1,2,null,null,5],5]`, "", ""},
	{"empty", `const a = []; a[2] = "x"; const result = [a.length, a];`, `[3,[null,null,"x"]]`, "", ""},
	{"gap-reads", `const a = [1]; a[3] = 4; const result = [a[1] ?? "hole", a[2] === undefined, a.length];`, `["hole",true,4]`, "", ""},
	{"for-of-over-the-gap", `const a = []; a[2] = 1; const seen = []; for (const x of a) { seen.push(x ?? "hole"); } const result = seen;`, `["hole","hole",1]`, "", ""},
	{"compound-in-range", `const a = [1, 2]; a[1] += 5; a[0]++; const result = a;`, `[2,7]`, "", ""},
	{"map-over-the-gap", `const a = []; a[2] = 1; const result = [a.map((x) => x ?? 0), a.filter((x) => true)];`, `[[0,0,1],[null,null,1]]`, `[[null,null,1],[1]]`,
		"the gap holds null, a value every array method visits; JavaScript's holes are skipped by map, filter and forEach"},
	{"negative-key", `const a = [1, 2]; const failed = safe(() => { a[-1] = 0; }); const result = [failed.error?.code ?? null, a];`, `["UNSUPPORTED",[1,2]]`, `[null,[1,2]]`,
		"JavaScript keeps the value as a property of the array, which an array of Kitwork does not have: the write fails rather than being lost"},
	{"fractional-key", `const a = [1, 2]; const failed = safe(() => { a[1.5] = 0; }); const result = [failed.error?.code ?? null, a];`, `["UNSUPPORTED",[1,2]]`, `[null,[1,2]]`,
		"a property in JavaScript, as above"},
	{"string-key", `const a = [1, 2]; const failed = safe(() => { a["1"] = 0; }); const result = [failed.error?.code ?? null, a];`, `["UNSUPPORTED",[1,2]]`, `[null,[1,0]]`,
		"JavaScript reads \"1\" as the index 1; Kitwork reads a[\"1\"] as no item (null), so it does not write one either"},
	{"named-key", `const a = [1, 2]; const failed = safe(() => { a.total = 3; }); const result = [failed.error?.code ?? null, a];`, `["UNSUPPORTED",[1,2]]`, `[null,[1,2]]`,
		"a property in JavaScript, as above"},
	{"too-far", `const a = []; const failed = safe(() => { a[2000000] = 1; }); const result = [failed.error?.code ?? null, a.length];`, `["INVALID_ARGUMENT",0]`, `[null,2000001]`,
		"a gap of more than runtime.MaxArrayGap (1048576) nulls is refused: an array of Kitwork holds each null, JavaScript's sparse array does not"},
}

var continueCases = []parityCase{
	{"for-of", `const out = []; for (const x of [1, 2, 3, 4, 5]) { if (x % 2 == 0) { continue; } out.push(x); } const result = out;`, `[1,3,5]`, "", ""},
	{"counting-for-runs-its-update", `const out = []; for (let i = 0; i < 5; i++) { if (i == 2) { continue; } out.push(i); } const result = out;`, `[0,1,3,4]`, "", ""},
	{"without-braces", `const out = []; for (const x of [1, 2, 3]) { if (x == 2) continue; out.push(x); } const result = out;`, `[1,3]`, "", ""},
	{"in-a-switch", `const out = []; for (let i = 0; i < 6; i++) { if (i == 1) continue; switch (i) { case 3: continue; default: out.push(i); } } const result = out;`, `[0,2,4,5]`, "", ""},
	{"switch-in-for-of", `const out = []; for (const x of ["a", "b", "c"]) { switch (x) { case "b": continue; } out.push(x); } const result = out;`, `["a","c"]`, "", ""},
	{"inner-loop", `const out = []; for (const row of [[1, 2], [3, 4]]) { for (const cell of row) { if (cell % 2 == 0) { continue; } out.push(cell); } out.push("/"); } const result = out;`, `[1,"/",3,"/"]`, "", ""},
	{"outer-loop", `const out = []; for (let i = 0; i < 3; i++) { for (const x of [7]) { out.push(x); } if (i == 1) { continue; } out.push(i); } const result = out;`, `[7,0,7,7,2]`, "", ""},
	{"closures", `const read = []; for (let i = 0; i < 4; i++) { if (i == 2) { continue; } read.push(() => i); } const result = read.map((f) => f());`, `[0,1,3]`, "", ""},
	{"with-break", `const out = []; for (let i = 0; i < 10; i++) { if (i % 2 == 1) { continue; } if (i > 6) { break; } out.push(i); } const result = out;`, `[0,2,4,6]`, "", ""},
	{"in-a-function", `const evens = (list) => { const out = []; for (const x of list) { if (x % 2) { continue; } out.push(x); } return out; }; const result = evens([1, 2, 3, 4]);`, `[2,4]`, "", ""},
	{"then-return", `const first = (list) => { for (const x of list) { if (x < 0) { continue; } return x; } return null; }; const result = [first([-1, -2, 3]), first([-1])];`, `[3,null]`, "", ""},
	{"last-statement", `let n = 0; for (let i = 0; i < 3; i++) { n = n + i; continue; } const result = n;`, `3`, "", ""},
	{"a-line-break-ends-it", "const out = []\nfor (const x of [1, 2, 3, 4]) {\n  if (x == 2) continue\n  if (x == 3) continue // skip\n  out.push(x)\n}\nconst result = out", `[1,4]`, "", ""},
}

var continueSyntaxCases = []syntaxCase{
	{"outside-a-loop", "const x = 1;\ncontinue;", "<source>:2:1: 'continue' chỉ hợp lệ bên trong vòng for"},
	{"in-a-switch-alone", "switch (1) {\ncase 1:\n  continue;\n}", "<source>:3:3: 'continue' chỉ hợp lệ bên trong vòng for"},
	{"in-a-function-made-in-a-loop", "for (const x of [1]) {\n  const f = () => { continue; };\n}", "<source>:2:21: 'continue' chỉ hợp lệ bên trong vòng for"},
}

var typeofCases = []parityCase{
	{"kinds", `const result = [typeof 1, typeof -2.5, typeof "s", typeof "", typeof true, typeof false, typeof {}, typeof [], typeof (() => 1), typeof function () { return 1; }];`,
		`["number","number","string","string","boolean","boolean","object","object","function","function"]`, "", ""},
	{"a-name-nothing-declares", `const result = [typeof missingName, typeof missingName === "undefined"];`, `["undefined",true]`, "", ""},
	{"missing-key-and-let", `const o = {}; let y; const result = [typeof o.nothing, typeof y, typeof [1][5]];`, `["undefined","undefined","undefined"]`, "", ""},
	{"precedence", `const o = { a: { b: "x" } }; const f = () => 2; const result = [typeof o.a.b, typeof f(), typeof 1 + 1, typeof (1 + "a"), typeof !0];`,
		`["string","number","number1","string","boolean"]`, "", ""},
	{"typeof-typeof", `const result = typeof typeof 1;`, `"string"`, "", ""},
	{"compared", `const v = "x"; let result = "other"; if (typeof v === "string") { result = "string"; }`, `"string"`, "", ""},
	{"template", "const result = `${typeof 1}`;", `"number"`, "", ""},
	{"globals", `const result = [typeof JSON, typeof Math, typeof Math.max, typeof parseInt, typeof Date, typeof new Date(), typeof console.log, typeof Array, typeof Object];`,
		`["object","object","function","function","function","object","function","function","function"]`, "", ""},
	{"own-key-cannot-answer", `const result = typeof { "#typeof": () => "no" };`, `"object"`, "", ""},
	{"key-named-typeof", `const o = { typeof: 5 }; const result = [o.typeof, typeof o.typeof];`, `[5,"number"]`, "", ""},
	{"object-test", `const isObject = (x) => typeof x === "object" && x !== null; const result = [isObject({}), isObject(null), isObject([])];`, `[true,false,true]`, "", ""},
	{"null", `const result = [typeof null, typeof null === "object"];`, `["undefined",false]`, `["object",true]`,
		"Kitwork has one value for null and undefined (a missing name, a missing key, let y;), and typeof gives it \"undefined\""},
}

func TestNegativeNumbersAreTrueAsInJavaScript(t *testing.T) { checkParity(t, truthinessCases, nil) }

func TestUpdateGivesTheOldOrNewValueAsInJavaScript(t *testing.T) { checkParity(t, updateCases, nil) }

func TestArrayWritePadsWithNullAsInJavaScript(t *testing.T) { checkParity(t, arrayWriteCases, nil) }

func TestContinueAsInJavaScript(t *testing.T) { checkParity(t, continueCases, continueSyntaxCases) }

var typeofSyntaxCases = []syntaxCase{
	{"without-an-operand", "const r = typeof;", "<source>:1:11: typeof là từ khoá của ngôn ngữ, không làm tên biến được"},
	{"before-a-colon", "const r = true ? typeof : 1;", "<source>:1:18: typeof là từ khoá của ngôn ngữ, không làm tên biến được"},
	{"as-a-parameter", "const f = (typeof) => 1;", "<source>:1:12: typeof là từ khoá của ngôn ngữ, không làm tên biến được"},
}

func TestTypeofAsInJavaScript(t *testing.T) { checkParity(t, typeofCases, typeofSyntaxCases) }

// checkParity runs every row on Kitwork, then all of them in one node process.
func checkParity(t *testing.T, cases []parityCase, syntax []syntaxCase) {
	t.Helper()
	for _, c := range cases {
		if (c.js == "") != (c.why == "") {
			t.Fatalf("%s: a row that differs from JavaScript says why, and only such a row", c.name)
		}
		got, failure := grammarRun(t, c.source, nil)
		if failure != nil {
			t.Errorf("%s: %s: %v", c.name, c.source, failure)
			continue
		}
		if !sameJSON(t, got, c.want) {
			t.Errorf("%s: %s\nKitwork = %s\nwant      %s", c.name, c.source, got, c.want)
		}
	}
	for _, c := range syntax {
		if got := compileError(c.source); !strings.Contains(got, c.kitwork) {
			t.Errorf("%s: %q = %q\nwant %s", c.name, c.source, got, c.kitwork)
		}
	}

	results, ok := runNode(t, cases, syntax)
	if !ok {
		return
	}
	for index, c := range cases {
		want := c.want
		if c.js != "" {
			want = c.js
		}
		if !sameJSON(t, results[index], want) {
			t.Errorf("%s: %s\nnode = %s\nwant   %s", c.name, c.source, results[index], want)
		}
	}
	for index, c := range syntax {
		if got := results[len(cases)+index]; got != "SyntaxError" {
			t.Errorf("%s: node compiled %q (%s), want a SyntaxError", c.name, c.source, got)
		}
	}
}

// runNode runs the rows in one node process: each as a function body that returns result as JSON, each
// syntax row through new Function, which must throw. ok is false when node is not on PATH.
func runNode(t *testing.T, cases []parityCase, syntax []syntaxCase) ([]string, bool) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Logf("node is not on PATH: the rows are checked against Kitwork only")
		return nil, false
	}
	type row struct {
		Source string `json:"source"`
		Syntax bool   `json:"syntax"`
	}
	var rows []row
	for _, c := range cases {
		rows = append(rows, row{Source: c.source})
	}
	for _, c := range syntax {
		rows = append(rows, row{Source: c.source, Syntax: true})
	}
	encoded, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	script := `globalThis.safe = (f) => {
  try { const value = f(); return { value: value === undefined ? null : value, error: null }; }
  catch (e) { return { value: null, error: { message: String(e && e.message), code: "THROWN" } }; }
};
const rows = ` + string(encoded) + `;
const out = rows.map((row) => {
  if (row.syntax) {
    try { new Function(row.source); return "compiled"; } catch (e) { return e.name; }
  }
  try { return JSON.stringify(new Function(row.source + "\n;return result;")()); }
  catch (e) { return "THROWN " + e.name + ": " + e.message; }
});
process.stdout.write(JSON.stringify(out));
`
	path := filepath.Join(t.TempDir(), "parity.js")
	if err := os.WriteFile(path, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	output, err := exec.CommandContext(ctx, node, path).Output()
	if err != nil {
		t.Fatalf("node: %v", err)
	}
	var results []string
	if err := json.Unmarshal(output, &results); err != nil || len(results) != len(rows) {
		t.Fatalf("node gave %q (%v)", output, err)
	}
	return results, true
}

// sameJSON compares two JSON texts as values: key order and number spelling aside.
func sameJSON(t *testing.T, got, want string) bool {
	t.Helper()
	var gotValue, wantValue any
	if json.Unmarshal([]byte(got), &gotValue) != nil {
		return false
	}
	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		t.Fatalf("the row's JSON %q: %v", want, err)
	}
	return reflect.DeepEqual(gotValue, wantValue)
}

// x++ and ++x whose value is dropped (a statement, a for's update) compile to the instructions and
// constants of the assignment they always were: no program grew. CONTROL: x++ used as a value is longer
// (read, update, POP).
func TestADroppedUpdateCompilesAsBefore(t *testing.T) {
	for _, pair := range [][2]string{
		{"let x = 1;\nx++;", "let x = 1;\nx = x + 1;"},
		{"let x = 1;\n--x;", "let x = 1;\nx = x - 1;"},
		{"const o = { n: 1 };\no.n++;", "const o = { n: 1 };\no.n = o.n + 1;"},
		{"const a = [1];\na[0]--;", "const a = [1];\na[0] = a[0] - 1;"},
		{"let t = 0;\nfor (let i = 0; i < 3; i++) { t += i; }", "let t = 0;\nfor (let i = 0; i < 3; i = i + 1) { t += i; }"},
		// padded, so the closure starts at the same column in both (a function constant records it)
		{"const f = [];\nfor (let i = 0; i < 3; i++      ) { f.push(() => i); }", "const f = [];\nfor (let i = 0; i < 3; i = i + 1) { f.push(() => i); }"},
	} {
		update, assignment := compileBoth(t, pair[0], pair[1])
		assertSameCode(t, update, assignment, pair[0], pair[1])
	}
	used, assignment := compileBoth(t, "let x = 1;\nconst y = x++;", "let x = 1;\nconst y = x = x + 1;")
	if len(used.Instructions()) <= len(assignment.Instructions()) {
		t.Fatalf("CONTROL: x++ as a value is %d bytes, the assignment %d", len(used.Instructions()), len(assignment.Instructions()))
	}
}

// Each null a[i] = v adds costs one unit of energy, so a program cannot make a long array for the cost of
// one instruction, and a write past what the run may spend is the energy limit, before anything is made.
// CONTROL: a write in range costs what it always did.
func TestArrayGapCostsEnergy(t *testing.T) {
	energy := func(source string, limit uint64) (uint64, *runtime.Diagnostic) {
		bytecode, err := CompileSource(source)
		if err != nil {
			t.Fatal(err)
		}
		vm := runtime.New(bytecode.Program)
		vm.MaxEnergy = limit
		returned := vm.Run()
		diagnostic, _ := runtime.DiagnosticFrom(returned)
		return vm.Stats().Energy, diagnostic
	}
	inRange, failure := energy("const a = [0];\na[0] = 1;", 1_000_000)
	if failure != nil {
		t.Fatal(failure)
	}
	padded, failure := energy("const a = [0];\na[1000] = 1;", 1_000_000)
	if failure != nil {
		t.Fatal(failure)
	}
	if padded-inRange != 999 {
		t.Fatalf("a[1000] = 1 on [0] cost %d energy, a[0] = 1 %d: want 999 more, one per null", padded, inRange)
	}
	_, failure = energy("const a = [];\na[100000] = 1;", 50_000)
	if failure == nil || failure.Code != runtime.DiagnosticEnergyLimit {
		t.Fatalf("a gap past the run's energy = %v, want %s", failure, runtime.DiagnosticEnergyLimit)
	}
}

// typeof is answered by the VM before the value: a proxy that answers every method (the query builder's
// row), an object of the host and an own key named as the method typeof compiles to are "object".
// CONTROL: the proxy answers a method of its own.
func TestTypeofIsNeverAnsweredByTheValue(t *testing.T) {
	globals := map[string]value.Value{
		"proxy": {K: value.Proxy, V: anyMethodProxy{}},
		"host":  value.New(&positionHost{}),
	}
	source := `const result = [typeof proxy, typeof host, typeof { "#typeof": () => "mine" }, proxy.anything()];`
	if got, want := runOwnKey(t, source, globals), `["object","object","object","anything"]`; got != want {
		t.Fatalf("result = %s, want %s", got, want)
	}
}

// .compact() and Boolean, which read truth the same way, keep a negative number (14/10/2026).
func TestCompactKeepsNegativeNumbers(t *testing.T) {
	if got, want := runOwnKey(t, `const result = [1, 0, -1, "", -0.5, null].compact();`, nil), `[1,-1,-0.5]`; got != want {
		t.Fatalf("compact = %s, want %s", got, want)
	}
}

// A write an array refuses fails at its bracket or its dot (14/10/2026), as a read does, not at the end
// of the value written; the message says what kind of key it was, never the key. CONTROL: an object
// takes both keys.
func TestAnArrayWriteFailsAtItsBracketOrDot(t *testing.T) {
	source := "const a = [1];\nconst bad = safe(() => {\n  a[-1] = {\n    x: 1\n  };\n});\nconst named = safe(() => {\n  a.total =\n    2;\n});\n" +
		"const o = {};\no[-1] = 1;\no.total = 2;\nconst result = [bad.error.at, bad.error.message, named.error.at, named.error.message, o];"
	want := `["<source>:3:4","mảng chỉ nhận chỉ số là số nguyên từ 0 trở lên, ở đây là số âm: mảng không có thuộc tính như object của JavaScript; cần khoá như vậy thì dùng object { }",` +
		`"<source>:8:4","mảng chỉ nhận chỉ số là số nguyên từ 0 trở lên, ở đây là chuỗi: mảng không có thuộc tính như object của JavaScript; cần khoá như vậy thì dùng object { }",` +
		`{"-1":1,"total":2}]`
	got, failure := grammarRun(t, source, nil)
	if failure != nil || !sameJSON(t, got, want) {
		t.Fatalf("result = %s (%v)\nwant     %s", got, failure, want)
	}
}
