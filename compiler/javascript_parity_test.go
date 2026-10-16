package compiler

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kitwork/engine/builtins"
	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
)

// The language decisions of 14/10/2026 ("đồng ý hết"), each compared with JavaScript itself: a negative
// number is true, x++ gives the old value and ++x the new one, a[i] = v past the end pads with null,
// continue, and typeof. Every row is a program that is JavaScript too; want is the result Kitwork gives,
// and node, when it is on PATH, runs the same program and must give the same, or js where a row differs
// on purpose (why says why). Without node only the Kitwork column is checked.
//
// Since 15/10/2026 (the review of these decisions) node runs each row as strict code, as a module is,
// and both sides write NaN, Infinity, -Infinity and -0 as {"#number": "NaN"} and so on, and node writes
// undefined as {"#undefined": true}: JSON alone made them null or 0, so a row could agree with a value
// JavaScript does not give. The rows marked 15/10/2026 and the tables objectKeyCases, arrayReadCases and
// droppedWriteCases list what that review found: the object and key of an update run once, a line break
// before ++ ends the expression, a number key of an object reads its text, a fractional index reads
// nothing, and the differences that stay, each with its reason.
//
// The owner's decisions of 15/10/2026 evening ("đồng ý hết, làm luôn đi"), each as in JavaScript, have
// their own tables: a write through a value that cannot hold properties is the runtime error TYPE
// (nonHolderWriteCases), a division or a remainder by zero follows IEEE and Infinity, NaN, isNaN and
// isFinite are there (numberCases), and ++ and -- make a number of what they step (stepCases). The rows
// of the tables above that they changed say so.
//
// The owner's E1 and E2 (15/10/2026, built 16/10/2026), each as in JavaScript too: == and === compare
// numbers exactly (exactEqualityCases), and Number(), unary +, parseFloat and parseInt read as JavaScript
// reads (conversionCases). The rows of the tables above that they changed say so.

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
	{"minus-zero", `const result = [-0, 0 * -1, -0 === 0, -0 || "falsy"];`, `[{"#number":"-0"},{"#number":"-0"},true,"falsy"]`, "", ""},
	{"infinity-and-not-a-number", `const v = 1e308 * 10; const result = [v, v ? 1 : 0, -v || "or", Math.sqrt(-1), Math.sqrt(-1) || "or"];`,
		`[{"#number":"Infinity"},1,{"#number":"-Infinity"},{"#number":"NaN"},"or"]`, "", ""},
	// 15/10/2026 (L2): a division by zero is Infinity, which is true, as in JavaScript (it was null, false)
	{"division-by-zero", `const v = 1 / 0; const result = [v, v ? 1 : 0, !v, v || "or", v && "and", Boolean(v), v ?? "nn", [5 % 0, 0 / 0, -1 / 0]];`,
		`[{"#number":"Infinity"},1,false,{"#number":"Infinity"},"and",true,{"#number":"Infinity"},[{"#number":"NaN"},{"#number":"NaN"},{"#number":"-Infinity"}]]`, "", ""},
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
	// 15/10/2026 (L3): ++ makes a number of the old value, as JavaScript does (they gave null and "51")
	{"null-operand", `let n = null; const a = n++; const result = [a, n];`, `[0,1]`, "", ""},
	{"string-operand", `let s = "5"; const a = s++; const result = [a, s];`, `[5,6]`, "", ""},
	// 15/10/2026: the object and key of an update or a compound assignment run once, as in JavaScript
	{"key-with-an-update", `const a = [1, 2, 3]; let i = 0; a[i++]++; const result = [a, i];`, `[[2,2,3],1]`, "", ""},
	{"key-with-an-update-as-a-value", `const a = [1, 2, 3]; let i = 0; const v = a[i++]++; const w = ++a[i++]; const x = a[i++]--; const result = [v, w, x, a, i];`,
		`[1,3,3,[2,3,2],3]`, "", ""},
	{"compound-key-with-an-update", `const a = [1, 2, 3]; let i = 0; a[i++] += 10; a[i++] -= 1; const result = [a, i];`, `[[11,1,3],2]`, "", ""},
	{"object-from-a-call", `let calls = 0; const o = { n: 0 }; const get = () => { calls++; return o; }; get().n++; const v = get().n++; const w = ++get().n; get().n += 5; const result = [v, w, calls, o.n];`,
		`[1,3,4,8]`, "", ""},
	{"object-from-a-call-is-the-one-written", `const os = [{ n: 0 }, { n: 10 }]; let k = 0; const pick = () => os[k++]; const v = pick().n++; const result = [v, os, k];`,
		`[0,[{"n":1},{"n":10}],1]`, "", ""},
	{"object-before-key", `let first = [1]; const second = [5]; let o = first; const key = () => { o = second; return 0; }; o[key()]++; const result = [first, second];`,
		`[[2],[5]]`, "", ""},
	{"nested", `const a = [0, 0]; const b = [0, 0]; let j = 0; a[b[j++]++]++; const result = [a, b, j];`, `[[1,0],[1,0],1]`, "", ""},
	// 15/10/2026: a line break before ++ or -- ends the expression, as JavaScript inserts the semicolon there
	{"a-line-break-before-increment", "let x = 1;\nlet y = 1;\nx\n++y\nconst result = [x, y];", `[1,2]`, "", ""},
	{"a-line-break-before-decrement", "let x = 3;\nlet y = 3;\nx\n--y\nconst result = [x, y];", `[3,2]`, "", ""},
	{"a-line-break-after-a-member", "const o = { n: 1 };\nlet count = 0;\nconst t = o.n\n++count\nconst result = [t, o.n, count];", `[1,1,1]`, "", ""},
	{"a-line-break-after-an-item", "const a = [5];\nlet count = 0;\nconst t = a[0]\n++count\nconst result = [t, a, count];", `[5,[5],1]`, "", ""},
	{"a-line-break-after-a-call", "let count = 0;\nconst f = () => 1;\nconst t = f()\n++count\nconst result = [t, count];", `[1,1]`, "", ""},
	{"a-line-break-after-return", "let x = 1;\nconst f = () => {\n  return x\n  ++x\n};\nconst result = [f(), x];", `[1,1]`, "", ""},
	{"a-line-break-around-the-operator", "let x = 1;\nlet y = x\n++\nx\nconst result = [x, y];", `[2,1]`, "", ""},
	{"postfix-then-a-line-break", "let x = 1;\nlet y = 5;\nx++\ny\nconst result = [x, y];", `[2,5]`, "", ""},
	// 15/10/2026 (L3): ++ and -- make a number of what they step, the old value x++ gives too (stepCases);
	// these rows failed with TYPE or gave the value unchanged until then. What stays different: a missing
	// item or key is null in Kitwork, which ++ makes 0 as JavaScript makes null; JavaScript's undefined is NaN.
	{"old-value-is-made-a-number", `const a = []; const result = [a[2]++, typeof a[2]];`, `[0,"number"]`, `[{"#number":"NaN"},"number"]`,
		"a missing item is null in Kitwork, which ++ makes 0 (the old value) and steps to 1; JavaScript's undefined makes NaN"},
	{"decrement-of-null", `let n = null; const failed = safe(() => { n--; }); const result = [failed.error?.code ?? null, n];`, `[null,-1]`, "", ""},
	{"decrement-of-a-missing-key", `const o = {}; const failed = safe(() => { o.count--; }); const result = [failed.error?.code ?? null, o.count ?? "missing"];`,
		`[null,-1]`, `[null,{"#number":"NaN"}]`, "a missing key is null, which -- makes 0 and steps to -1; JavaScript makes NaN of undefined"},
	{"decrement-of-a-string", `let s = "5"; const failed = safe(() => { s--; }); const result = [failed.error?.code ?? null, s];`, `[null,4]`, "", ""},
	{"update-of-a-boolean-an-array-an-object", `let b = true; let a = [2]; let o = {}; const codes = [safe(() => { b++; }), safe(() => { a++; }), safe(() => { o--; })].map((r) => r.error?.code ?? null); const result = [codes, b, o];`,
		`[[null,null,null],2,{"#number":"NaN"}]`, "", ""},
}

var arrayWriteCases = []parityCase{
	{"in-range", `const a = [1, 2, 3]; a[0] = 9; const result = a;`, `[9,2,3]`, "", ""},
	{"at-the-length", `const a = [1]; a[1] = 2; a[a.length] = 3; const result = a;`, `[1,2,3]`, "", ""},
	{"past-the-end", `const a = [1, 2]; a[4] = 5; const result = [a, a.length];`, `[[1,2,null,null,5],5]`,
		`[[1,2,{"#undefined":true},{"#undefined":true},5],5]`,
		"the gap holds null, Kitwork's one value for null and undefined; JavaScript leaves holes, which read undefined (JSON wrote them null, so the row agreed until 15/10/2026)"},
	{"empty", `const a = []; a[2] = "x"; const result = [a.length, a];`, `[3,[null,null,"x"]]`, `[3,[{"#undefined":true},{"#undefined":true},"x"]]`,
		"holes, as above"},
	{"gap-reads", `const a = [1]; a[3] = 4; const result = [a[1] ?? "hole", a[2] === undefined, a.length];`, `["hole",true,4]`, "", ""},
	{"for-of-over-the-gap", `const a = []; a[2] = 1; const seen = []; for (const x of a) { seen.push(x ?? "hole"); } const result = seen;`, `["hole","hole",1]`, "", ""},
	{"compound-in-range", `const a = [1, 2]; a[1] += 5; a[0]++; const result = a;`, `[2,7]`, "", ""},
	{"map-over-the-gap", `const a = []; a[2] = 1; const result = [a.map((x) => x ?? 0), a.filter((x) => true)];`, `[[0,0,1],[null,null,1]]`,
		`[[{"#undefined":true},{"#undefined":true},1],[1]]`,
		"the gap holds null, a value every array method visits; JavaScript's holes are skipped by map, filter and forEach (map keeps them as holes)"},
	{"join-of-the-gap", `const a = [1]; a[3] = 4; const result = [a.join("-"), [1, null, 4].join("-"), String([1, null, 4])];`,
		`["1-null-null-4","1-null-4","[1, null, 4]"]`, `["1---4","1--4","1,,4"]`,
		"join and String write null as the text null in Kitwork (String writes the array as [a, b]); JavaScript writes nothing for null and undefined"},
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
	{"no-index-however-far", `const a = [1, 2]; const results = [safe(() => { a[1048578.5] = 9; }), safe(() => { a[1e308 * 10] = 9; }), safe(() => { a[-1e308 * 10] = 9; })]; const result = [results.map((r) => r.error?.code ?? null), results.map((r) => r.error?.message.split("ở đây là ")[1].split(":")[0] ?? null), a];`,
		`[["UNSUPPORTED","UNSUPPORTED","UNSUPPORTED"],["số không nguyên","vô cực","số âm"],[1,2]]`, `[[null,null,null],[null,null,null],[1,2]]`,
		"a fraction or an infinity is no index however far past the end (15/10/2026: past the gap limit it was INVALID_ARGUMENT); a property in JavaScript, as above"},
	{"length", `const a = [1, 2, 3]; const failed = safe(() => { a.length = 0; }); const result = [failed.error?.code ?? null, a, a.slice(0, 1)];`,
		`["UNSUPPORTED",[1,2,3],[1]]`, `[null,[],[]]`,
		"the length of a Kitwork array is read, never written: a.slice(0, n) gives the first n items; JavaScript cuts or grows the array"},
}

// 15/10/2026: a number key of an object reads the key o[k] = v writes, its text, as in JavaScript; it read
// null, so counting by a number lost the count.
var objectKeyCases = []parityCase{
	{"literal-and-name", `const o = { 1: "a" }; const k = 1; const result = [o[1], o[k], o["1"]];`, `["a","a","a"]`, "", ""},
	{"written-and-read", `const o = {}; o[1] = "x"; o[1.5] = "y"; const result = [o[1], o["1"], o[1.5], o["1.5"]];`, `["x","x","y","y"]`, "", ""},
	{"minus-zero", `const o = { 0: "zero" }; const result = [o[-0], o[0]];`, `["zero","zero"]`, "", ""},
	{"counting", `const c = {}; for (const n of [1, 1, 2, 1]) { c[n] = (c[n] ?? 0) + 1; } const result = c;`, `{"1":3,"2":1}`, "", ""},
	{"counting-by-year", `const byYear = {}; for (const y of [2024, 2025, 2024]) { byYear[y] = (byYear[y] || 0) + 1; } const result = byYear;`, `{"2024":2,"2025":1}`, "", ""},
	{"update", `const c = { 7: 10 }; const k = 7; const v = ++c[k]; c[k]++; c[k] += 5; const result = [v, c];`, `[11,{"7":17}]`, "", ""},
	{"parsed", `const o = JSON.parse('{"5":"five"}'); const result = o[5];`, `"five"`, "", ""},
}

// 15/10/2026: an index that is no whole number reads nothing, as JavaScript reads undefined, and as the
// write refuses it; it read the item of the whole part (a[1.5] was a[1]).
var arrayReadCases = []parityCase{
	{"fraction", `const a = [10, 20, 30]; const result = [a[0.5] === undefined, a[1.9] === undefined, a[-0.5] === undefined, a[3 / 2] === undefined, a[a.length / 2] === undefined];`,
		`[true,true,true,true,true]`, "", ""},
	{"fraction-of-a-string", `const s = "abc"; const result = [s[0.5] === undefined, s[1.5] === undefined, s[1]];`, `[true,true,"b"]`, "", ""},
	{"whole-numbers-and-the-rest", `const a = [10, 20, 30]; const result = [a[2 / 2], a[-1] === undefined, a[0 / 0] === undefined, a[1e308 * 10] === undefined];`,
		`[20,true,true,true]`, "", ""},
	{"what-the-write-refuses", `const a = [10, 20, 30]; const failed = safe(() => { a[1.5] = 99; }); const result = [failed.error?.code ?? null, a[1.5] === undefined, a];`,
		`["UNSUPPORTED",true,[10,20,30]]`, `[null,false,[10,20,30]]`, "a property in JavaScript, which an array of Kitwork does not have (the write is refused)"},
}

// A write through a value that cannot hold properties fails, as JavaScript's TypeError (15/10/2026, the
// owner's L1): through null (a missing row, a missing object), and, as strict code, which a module is,
// through a string, a number and a boolean. Until then Kitwork dropped each without a word. A row says
// "failed" or "written": Kitwork's code is TYPE (TestAWriteThroughANonHolderSaysWhatAndWhere), node's an
// exception.
var nonHolderWriteCases = []parityCase{
	{"through-null", `const n = null; const results = [safe(() => { n.x = 1; }), safe(() => { n[0] = 1; })]; const result = results.map((r) => r.error ? "failed" : "written");`,
		`["failed","failed"]`, "", ""},
	{"a-missing-row", `const grid = []; const r = safe(() => { grid[1][2] = "x"; }); const result = [r.error ? "failed" : "written", grid];`,
		`["failed",[]]`, "", ""},
	{"a-missing-object", `const o = {}; const r = safe(() => { o.a.b = 1; }); const result = [r.error ? "failed" : "written", o];`,
		`["failed",{}]`, "", ""},
	{"an-update-through-null", `const n = null; const r = safe(() => n.x++); const result = [r.error ? "failed" : "written", r.value];`,
		`["failed",null]`, "", ""},
	{"a-compound-assignment-through-null", `const n = null; const r = safe(() => { n.total += 5; }); const result = r.error ? "failed" : "written";`,
		`"failed"`, "", ""},
	{"strings-numbers-and-booleans", `const s = "abc"; const n = 5; const b = true; const results = [safe(() => { s[0] = "x"; }), safe(() => { s.x = 1; }), safe(() => { n.x = 1; }), safe(() => { n[0] = 1; }), safe(() => { b.x = 1; })]; const result = [results.map((r) => r.error ? "failed" : "written"), s];`,
		`[["failed","failed","failed","failed","failed"],"abc"]`, "", ""},
	{"a-member-that-is-a-string", `const o = { name: "kit" }; const r = safe(() => { o.name.x = 1; }); const result = [r.error ? "failed" : "written", o];`,
		`["failed",{"name":"kit"}]`, "", ""},
	{"the-rest-of-the-function-does-not-run", `const o = { name: "kit" }; let after = "not reached"; const r = safe(() => { o.name.size = 3; after = "reached"; }); const result = [r.error ? "failed" : "written", after];`,
		`["failed","not reached"]`, "", ""},
	{"CONTROL-objects-arrays-and-dates", `const o = {}; const a = []; const d = new Date(0); o.x = 1; a[0] = 2; d.label = "x"; const result = [o, a, d.label];`,
		`[{"x":1},[2],"x"]`, "", ""},
	{"a-member-of-a-function", `const f = () => 1; const r = safe(() => { f.extra = 1; }); const result = [r.error ? "failed" : "written", f.extra ?? "missing"];`,
		`["failed","missing"]`, `["written",1]`,
		"Kitwork keeps no member a program writes on a function, an arrow function or one of the engine's (engine-functions), so the write fails rather than being lost; JavaScript keeps it, as a function is an object there"},
	{"engine-functions", `const results = [safe(() => { Number.extra = 1; }), safe(() => { Math.max.x = 1; }), safe(() => { String.y = 1; })]; const result = [results.map((r) => r.error ? "failed" : "written"), Number.extra ?? "missing", typeof Number.MAX_SAFE_INTEGER];`,
		`[["failed","failed","failed"],"missing","number"]`, `[["written","written","written"],1,"number"]`,
		"the engine's functions (Number, String, Math.max) are functions of Kitwork, which keep no member a program writes, though some have members of the engine's own (Number.MAX_SAFE_INTEGER); JavaScript keeps the write"},
}

func TestObjectNumberKeysAsInJavaScript(t *testing.T) { checkParity(t, objectKeyCases, nil) }

func TestArrayReadsAsInJavaScript(t *testing.T) { checkParity(t, arrayReadCases, nil) }

func TestWriteThroughANonHolderFailsAsInJavaScript(t *testing.T) {
	checkParity(t, nonHolderWriteCases, nil)
}

// numberCases (15/10/2026, the owner's L2): a division or a remainder by zero is Infinity, -Infinity or
// NaN, as IEEE 754 and JavaScript say (it was null); they print, compare and go to JSON as in JavaScript,
// and Infinity, NaN, isNaN, isFinite, Number.POSITIVE_INFINITY, Number.NEGATIVE_INFINITY, Number.NaN and
// Number.isNaN are there.
var numberCases = []parityCase{
	{"division-and-remainder", `const z = 0; const result = [1 / z, -1 / z, 0 / z, 5 % z, -5 % z, 5 % -0, 1 / -0, 5 % (1 / z)];`,
		`[{"#number":"Infinity"},{"#number":"-Infinity"},{"#number":"NaN"},{"#number":"NaN"},{"#number":"NaN"},{"#number":"NaN"},{"#number":"-Infinity"},5]`, "", ""},
	{"or-and-nullish", `const z = 0; const result = [(1 / z) || "or", (0 / z) || "or", (1 / z) ?? "nn", (0 / z) ?? "nn"];`,
		`[{"#number":"Infinity"},"or",{"#number":"Infinity"},{"#number":"NaN"}]`, "", ""},
	{"arithmetic-goes-on", `const z = 0; const r = safe(() => (1 / z) * 100); const result = [r.value, r.error?.code ?? null, (1 / z) + 1, (1 / z) - (1 / z), 1 / (1 / z)];`,
		`[{"#number":"Infinity"},null,{"#number":"Infinity"},{"#number":"NaN"},0]`, "", ""},
	{"compound", `let x = 6; x /= 0; let y = -6; y /= 0; const result = [x, y];`, `[{"#number":"Infinity"},{"#number":"-Infinity"}]`, "", ""},
	{"text", "const z = 0; const result = [String(1 / z), `${1 / z}`, `${0 / z}`, (1 / z) + \"\", String(-1 / z), \"x\" + (0 / z), [1 / z, 0 / z].join(\",\")];",
		`["Infinity","Infinity","NaN","Infinity","-Infinity","xNaN","Infinity,NaN"]`, "", ""},
	{"to-fixed", `const z = 0; const result = [(1 / z).toFixed(2), (0 / z).toFixed(1), (-1 / z).toFixed(0), (2.5).toFixed(1)];`,
		`["Infinity","NaN","-Infinity","2.5"]`, "", ""},
	{"json", `const z = 0; const result = [JSON.stringify({ a: 1 / z, b: 0 / z, c: [-1 / z, 1] }), JSON.stringify(0 / z)];`,
		`["{\"a\":null,\"b\":null,\"c\":[null,1]}","null"]`, "", ""},
	{"typeof", `const z = 0; const result = [typeof (1 / z), typeof (0 / z), typeof Infinity, typeof NaN];`,
		`["number","number","number","number"]`, "", ""},
	{"equality", `const big = 1e308 * 10; const n = 0 / 0; const result = [big === big, big == big, -big === -big, big === -big, big === 1e308, n === n, n == n, n !== n, n == null];`,
		`[true,true,true,false,false,false,false,true,false]`, "", ""},
	{"order", `const n = 0 / 0; const result = [n < 1, n <= 1, n > 1, n >= 1, 1 <= n, 1 >= n, n <= n, 1 / 0 >= 1 / 0, -1 / 0 < 1];`,
		`[false,false,false,false,false,false,false,true,true]`, "", ""},
	{"object-keys", `const o = {}; o[1 / 0] = "inf"; o[0 / 0] = "nan"; o[-1 / 0] = "minus"; const result = [o.Infinity, o.NaN, o["-Infinity"]];`,
		`["inf","nan","minus"]`, "", ""},
	{"math", `const z = 0; const result = [Math.round(1 / z), Math.floor(0 / z), Math.max(1 / z, 3), Math.min(0, -1 / z), Math.max(1, 0 / z), Math.min(0 / z, 1), Math.sign(0 / z), Math.abs(-1 / z)];`,
		`[{"#number":"Infinity"},{"#number":"NaN"},{"#number":"Infinity"},{"#number":"-Infinity"},{"#number":"NaN"},{"#number":"NaN"},{"#number":"NaN"},{"#number":"Infinity"}]`, "", ""},
	{"globals", `const result = [Infinity, -Infinity, NaN, Infinity > 1e308, 1 / Infinity, Infinity === 1 / 0];`,
		`[{"#number":"Infinity"},{"#number":"-Infinity"},{"#number":"NaN"},true,0,true]`, "", ""},
	{"is-nan", `const result = [isNaN("abc"), isNaN(""), isNaN(" 12 "), isNaN(null), isNaN(true), isNaN(NaN), isNaN(0 / 0), isNaN(1 / 0), isNaN({}), isNaN("0x1f"), isNaN("1e3"), isNaN("Infinity"), isNaN("5px"), isNaN(() => 1)];`,
		`[true,false,false,false,false,true,true,false,true,false,false,false,true,true]`, "", ""},
	{"is-finite", `const result = [isFinite("12"), isFinite(""), isFinite("abc"), isFinite(1 / 0), isFinite(null), isFinite(-1 / 0), isFinite(0 / 0), isFinite(false), Number.isFinite("12"), Number.isFinite(12)];`,
		`[true,true,false,false,true,false,false,true,false,true]`, "", ""},
	{"number-constants", `const result = [Number.POSITIVE_INFINITY, Number.NEGATIVE_INFINITY, Number.NaN, Number.POSITIVE_INFINITY === Infinity, Number.NEGATIVE_INFINITY === -Infinity];`,
		`[{"#number":"Infinity"},{"#number":"-Infinity"},{"#number":"NaN"},true,true]`, "", ""},
	{"number-is-nan", `const result = [Number.isNaN(0 / 0), Number.isNaN(NaN), Number.isNaN("abc"), Number.isNaN(null), Number.isNaN(1), Number.isNaN()];`,
		`[true,true,false,false,false,false]`, "", ""},
	// 16/10/2026 (the owner's E2): an array is a number through its text, and Number("abc") is NaN, as in
	// JavaScript (every array was NaN, and Number("abc") null, so isNaN(Number(x)) was never true)
	{"is-nan-of-an-array", `const result = [isNaN([]), isNaN([5]), isNaN([1, 2]), isFinite([5])];`, `[false,false,true,true]`, "", ""},
	{"number-of-text-that-is-no-number", `const val = "abc"; const result = [isNaN(val), isNaN(Number(val)), Number(val) ?? "null"];`, `[true,true,{"#number":"NaN"}]`, "", ""},
	// what stays different
	{"is-nan-of-undefined", `const o = {}; const result = [isNaN(o.missing), isNaN(undefined), isNaN()];`, `[false,false,true]`, `[true,true,true]`,
		"Kitwork has one value for null and undefined, which isNaN makes 0 as JavaScript makes null; JavaScript's undefined is NaN (no argument is NaN in both)"},
}

func TestDivisionByZeroAndNonFiniteNumbersAsInJavaScript(t *testing.T) {
	checkParity(t, numberCases, nil)
}

// nativeNumberCases (15/10/2026, the review of L2): once a division by zero gives Infinity and NaN, they
// reach every function of the engine that takes a number. Each reads it as JavaScript does: a position,
// a length or a count of NaN is 0 and of Infinity past the end (value.Value.Int; Go's int() made both
// -9223372036854775808, and "7".padStart(1 / 0) panicked past safe()); a date of NaN is invalid; a date is
// its time value to isNaN, Number() and Math; numbers print, round and compare as in JavaScript.
var nativeNumberCases = []parityCase{
	{"positions-and-counts-of-nan-and-infinity", `const z = 0; const inf = 1 / z; const nan = 0 / z; const result = ["abc".slice(0, inf), "abc".slice(nan, 2), [1, 2, 3].slice(0, inf), "abc".substring(0, inf), "abc".substring(nan), "abc".charAt(nan), "abc".charAt(inf), "abc".at(nan), [1, 2].at(nan), [1, 2].at(inf) ?? "none", "abc".charCodeAt(nan), [1, 2, 1].indexOf(1, inf), [1, 2, 1].includes(1, inf), "abcabc".indexOf("a", inf), "abcabc".indexOf("a", nan), "7".padStart(nan, "0"), "7".padEnd(nan, "x"), "ab".repeat(nan)];`,
		`["abc","ab",[1,2,3],"abc","abc","a","","a",1,"none",97,-1,false,-1,0,"7","7",""]`, "", ""},
	{"index-of-and-includes-from-an-index", `const a = [1, 2, 1]; const result = [a.indexOf(1, 1), a.indexOf(1, -1), a.indexOf(1, -10), a.indexOf(2, 2), a.includes(2, 2), a.includes(1, -1)];`,
		`[2,2,0,-1,false,true]`, "", ""},
	{"a-length-it-cannot-make", `const z = 0; const results = [safe(() => "7".padStart(2 / z, "0")), safe(() => "ab".padEnd(1 / z, "x")), safe(() => "ab".repeat(1 / z)), safe(() => Array(0 / z)), safe(() => new Array(1 / z)), safe(() => Array(-1))]; const result = [results.map((r) => r.error ? "failed" : r.value), Array(2).length, "7".padStart(3, "0"), "ab".repeat(2)];`,
		`[["failed","failed","failed","failed","failed","failed"],2,"007","abab"]`, "", ""},
	{"parse-int-of-infinity-and-nan", `const z = 0; const result = [Number.parseInt(1 / z), Number.parseInt(0 / z), Number.parseInt("Infinity"), Number.parseInt(12.7), Number.parseInt(-12.7), Number.parseInt("0x10")];`,
		`[{"#number":"NaN"},{"#number":"NaN"},{"#number":"NaN"},12,-12,16]`, "", ""},
	{"dates-of-nan-and-infinity", `const z = 0; const d = new Date(0 / z); const e = new Date(1 / z); let f = new Date(0 / z); f++; const result = [d.getTime(), e.getTime(), d.getFullYear(), f, new Date(8.64e15 + 1).getTime(), new Date(8.64e15).getTime(), Date.UTC(0 / z, 0)];`,
		`[{"#number":"NaN"},{"#number":"NaN"},{"#number":"NaN"},{"#number":"NaN"},{"#number":"NaN"},8640000000000000,{"#number":"NaN"}]`, "", ""},
	{"is-nan-of-a-date", `const good = "2024-01-15"; const bad = "not a date"; const result = [isNaN(new Date(good)), isNaN(new Date(bad)), isNaN(Date.parse(good)), isNaN(Date.parse(bad)), isNaN(new Date(good).getTime()), isNaN(new Date(bad).getTime()), Date.parse(bad), isFinite(new Date(5)), Number(new Date(5)), new Date(bad).getFullYear(), new Date(bad).toString(), new Date(bad).toJSON(), safe(() => new Date(bad).toISOString()).error ? "failed" : "written", new Date(5).toISOString()];`,
		`[false,true,false,true,false,true,{"#number":"NaN"},true,5,{"#number":"NaN"},"Invalid Date",null,"failed","1970-01-01T00:00:00.005Z"]`, "", ""},
	{"a-date-steps-and-a-copy-is-no-date", `let d = new Date(5); d++; let bad = new Date("garbage"); bad++; let spread = { ...new Date(5) }; spread++; const result = [d, bad, spread, isNaN({ ...new Date(5) })];`,
		`[6,{"#number":"NaN"},{"#number":"NaN"},true]`, "", ""},
	{"number-text", "const result = [String(1234567.5), `${1000000.25}`, \"\" + -2500000.75, String(99999999.99), String(0.00001), String(1e-7), String(1e20), String(1e21), String(-0), [1234567.5, 1e-7].join(\",\"), String(1152921504606846976), String(123e-20), String(1.5e300), String(0.1 + 0.2)];",
		`["1234567.5","1000000.25","-2500000.75","99999999.99","0.00001","1e-7","100000000000000000000","1e+21","0","1234567.5,1e-7","1152921504606847000","1.23e-18","1.5e+300","0.30000000000000004"]`, "", ""},
	{"number-keys-and-json", `const o = {}; o[1234567.5] = 1; o[1e21] = 2; o[1e-7] = 3; const result = [Object.keys(o).sort(), o["1234567.5"], o["1e+21"], JSON.stringify([1234567.5, 1e21, 1e-7, -0, 1152921504606846976, { a: 0.00001 }])];`,
		`[["1234567.5","1e+21","1e-7"],1,2,"[1234567.5,1e+21,1e-7,0,1152921504606847000,{\"a\":0.00001}]"]`, "", ""},
	{"to-fixed", `const result = [(1.25).toFixed(1), (2.5).toFixed(0), (0.5).toFixed(0), (1.125).toFixed(2), (-0).toFixed(2), (1e21).toFixed(2), (1.005).toFixed(2), (-1.25).toFixed(1), (-0.001).toFixed(2), (123.456).toFixed(0), (0).toFixed(2), (1234.5678).toFixed(2), (0.000001).toFixed(7)];`,
		`["1.3","3","1","1.13","0.00","1e+21","1.00","-1.3","-0.00","123","0.00","1234.57","0.0000010"]`, "", ""},
	{"number-reads-text-as-javascript", "const result = [Number(\"1e400\"), Number(\"0x10\"), Number(\"\\uFEFF5\"), Number(\"  12  \"), Number(\"\"), Number(\"1e3\"), Number(new Date(5)), Number(true), Number(null), parseFloat(\"1e400\"), parseFloat(\" 2.5 \"), parseFloat(\"-.5e1\"), Number.parseFloat(\"1e400\"), Number.parseFloat(\"\\uFEFF5\"), isNaN(\"0x10\"), Number(\"-Infinity\")];",
		`[{"#number":"Infinity"},16,5,12,0,1000,5,1,0,{"#number":"Infinity"},2.5,-5,{"#number":"Infinity"},5,false,{"#number":"-Infinity"}]`, "", ""},
	{"math-reads-numbers-as-javascript", `const result = [Math.round("2.5"), Math.floor("7.9"), Math.abs("-5"), Math.max("5", 2), Math.min("1", 2), Math.sqrt("16"), Math.abs("x"), Math.round(new Date(1500)), Math.max([1, 2]), Math.round(null), Math.abs(true), Math.pow("2", "3")];`,
		`[3,7,5,5,1,4,{"#number":"NaN"},1500,{"#number":"NaN"},0,1,8]`, "", ""},
	{"math-edges", `const result = [Math.pow(1, Infinity), Math.pow(-1, Infinity), Math.pow(-1, -Infinity), Math.pow(1, NaN), Math.pow(2, 10), Math.pow(NaN, 0), Math.round(4503599627370497), Math.round(0.49999999999999994), Math.round(-0.4), Math.round(-0.5), Math.round(-2.5), Math.round(2.5), Math.round(-2.6), Math.max(-0, 0), Math.min(0, -0), Math.max(0, -0), Math.min(-0, 0), Math.max(), Math.min()];`,
		`[{"#number":"NaN"},{"#number":"NaN"},{"#number":"NaN"},{"#number":"NaN"},1024,1,4503599627370497,0,{"#number":"-0"},{"#number":"-0"},-2,3,-3,0,{"#number":"-0"},0,{"#number":"-0"},{"#number":"-Infinity"},{"#number":"Infinity"}]`, "", ""},
	{"number-constants-and-integers", `const result = [Number.isInteger(1e300), Number.isInteger(9223372036854775808), Number.isInteger(1.5), Number.isInteger(1 / 0), Number.MAX_VALUE, Number.MIN_VALUE, Number.isSafeInteger(9007199254740991), Number.isSafeInteger(9007199254740992), Number.isSafeInteger(1.5), Number.MAX_VALUE * 2, Number.MIN_VALUE / 2];`,
		`[true,true,false,false,1.7976931348623157e+308,5e-324,true,false,false,{"#number":"Infinity"},0]`, "", ""},
	{"includes-finds-nan", `const z = 0; const result = [[0 / z].includes(0 / z), [1, NaN].includes(NaN), [NaN].indexOf(NaN), [-0].includes(0), [0].includes(-0), [NaN].includes(0)];`,
		`[true,true,-1,true,true,false]`, "", ""},
	// 16/10/2026 (the owner's E1 and E2): what is no number is NaN to Number() and parseFloat, parseFloat
	// reads the number the text begins with, parseInt reads 0x without a radix, an array is a number
	// through its text, and == compares numbers exactly (they were null, 0, the whole text, 0x only with
	// the radix 16, NaN, and equal when less than 1e-12 apart)
	{"what-is-no-number", "const result = [\"inf\", \"infinity\", \"1_000\", \"0x1p3\", \"\\u00855\", \"abc\", \"\"].map((s) => [Number(s) ?? \"null\", parseFloat(s), Number.parseFloat(s) ?? \"null\"]);",
		`[[{"#number":"NaN"},{"#number":"NaN"},{"#number":"NaN"}],[{"#number":"NaN"},{"#number":"NaN"},{"#number":"NaN"}],[{"#number":"NaN"},1,1],[{"#number":"NaN"},0,0],[{"#number":"NaN"},{"#number":"NaN"},{"#number":"NaN"}],[{"#number":"NaN"},{"#number":"NaN"},{"#number":"NaN"}],[0,{"#number":"NaN"},{"#number":"NaN"}]]`, "", ""},
	{"parse-float-reads-the-number-the-text-begins-with", `const result = [parseFloat("3.5px"), parseFloat("1e3x"), parseFloat("0x10"), parseInt("0x1f"), Number.parseFloat("0x10") ?? "null", parseInt("12px")];`,
		`[3.5,1000,0,31,0,12]`, "", ""},
	{"math-of-an-array", `const result = [Math.abs([-3]), Math.round([]), Number([5]) ?? "null"];`, `[3,0,5]`, "", ""},
	{"no-tolerance", `const result = [0.1 + 0.2 === 0.3, 1e-13 === 0, 1e-13 > 0, 1e-13 <= 0, [1e-13].includes(0), [0].indexOf(1e-13), 1e-13 !== 0];`,
		`[false,false,true,false,false,-1,true]`, "", ""},
	// what stays different
	{"a-copied-value-of-is-no-date", `const o = { valueOf: new Date(5).valueOf }; const r = safe(() => { let x = o; x++; return x; }); const result = r.error ? "failed" : r.value;`,
		`{"#number":"NaN"}`, `"failed"`,
		"++ calls no valueOf: an object whose valueOf was copied from a date is no date, and NaN; JavaScript calls the copied valueOf, which refuses an object that is not a date (TypeError)"},
	// 16/10/2026: docs/KITWORK_LANG.md lists each with its reason
	{"a-string-searches-from-an-index", `const result = ["abcabc".lastIndexOf("a", 2), "abcabc".lastIndexOf("c", 0), "abcabc".indexOf("a", "1"), "abcabc".indexOf("b", "2"), "abcabc".indexOf("a", 1), "abcabc".lastIndexOf("a"), "abcabc".indexOf("a")];`,
		`[3,5,0,1,3,3,0]`, `[0,-1,3,4,3,3,0]`,
		"a string's lastIndexOf reads no fromIndex and its indexOf reads one only when it is a number (value.LastIndexOf, value.IndexOf); an array's read both as JavaScript does (search-from)"},
	{"math-with-a-missing-argument", `const result = [Math.abs() ?? "null", Math.round() ?? "null", Math.pow(2) ?? "null", Math.max(), Math.min(), Math.abs(-2), Math.pow(2, 3)];`,
		`["null","null","null",{"#number":"-Infinity"},{"#number":"Infinity"},2,8]`, `[{"#number":"NaN"},{"#number":"NaN"},{"#number":"NaN"},{"#number":"-Infinity"},{"#number":"Infinity"},2,8]`,
		"a function of Math given fewer arguments than it reads gives null (mathFn1, mathFn2 of builtins); JavaScript reads a missing one as undefined, which is NaN. Math.max() and Math.min() read any count and agree"},
	{"char-code-at-past-the-end", `const result = ["ab".charCodeAt(5) ?? "null", "ab".charCodeAt(-1) ?? "null", "ab".charCodeAt(1), "ab".charCodeAt()];`,
		`["null","null",98,97]`, `[{"#number":"NaN"},{"#number":"NaN"},98,97]`,
		"charCodeAt outside the string gives null (value.CharCodeAt), as at() does in both; JavaScript's charCodeAt gives NaN there"},
	{"an-array-of-a-fractional-length", `const r = safe(() => Array(1.5)); const s = safe(() => new Array(2.5)); const result = [r.error ? "failed" : r.value.length, s.error ? "failed" : s.value.length, Array(2).length];`,
		`[1,2,2]`, `["failed","failed",2]`,
		"Array(n) takes the whole part of a fractional n (builtins.Array); JavaScript refuses a length that is no whole number with a RangeError"},
	{"repeat-infinity-of-nothing", `const r = safe(() => "".repeat(Infinity)); const s = safe(() => "ab".repeat(Infinity)); const result = [r.error ? "failed" : r.value, s.error ? "failed" : s.value, "ab".repeat(2)];`,
		`["","failed","abab"]`, `["failed","failed","abab"]`,
		"repeat refuses a count only when the text it would make is too large, and nothing repeated is nothing (value.Repeat); JavaScript refuses an infinite count first, with a RangeError"},
}

func TestNativesReadNumbersAsInJavaScript(t *testing.T) { checkParity(t, nativeNumberCases, nil) }

// stepCases (15/10/2026, the owner's L3): ++ and -- make a number of what they step as JavaScript's
// ToNumber does, then step it (runtime.STEP); x++ gives the old value made a number. "5"++ is 6 (it was
// "51"), null-- is -1 (it was TYPE), true++ is 2, a date steps from its time value in milliseconds.
var stepCases = []parityCase{
	{"strings", `let a = "5"; a++; let b = "5"; b--; let c = ""; c++; let d = "a"; d++; let e = " 12 "; e++; let f = "0x10"; f++; let g = "1e3"; g--; let h = "Infinity"; h++; let i = "-0.5"; i++; const result = [a, b, c, d, e, f, g, h, i];`,
		`[6,4,1,{"#number":"NaN"},13,17,999,{"#number":"Infinity"},0.5]`, "", ""},
	{"the-old-value-made-a-number", `let s = "5"; const a = s++; let t = "x"; const b = t--; let n = null; const c = n++; let u = true; const d = u--; const result = [a, s, b, t, c, n, d, u];`,
		`[5,6,{"#number":"NaN"},{"#number":"NaN"},0,1,1,0]`, "", ""},
	{"the-new-value", `let s = "5"; const a = ++s; let n = null; const b = --n; let f = false; const c = ++f; const result = [a, s, b, n, c, f];`,
		`[6,6,-1,-1,1,1]`, "", ""},
	{"booleans", `let t = true; t++; let f = false; f--; const result = [t, f, typeof t];`, `[2,-1,"number"]`, "", ""},
	{"null", `let n = null; n--; const p = { count: null }; p.count--; let q = null; q++; const result = [n, p.count, q];`, `[-1,-1,1]`, "", ""},
	{"objects-and-functions", `let o = {}; o--; let f = () => 1; f++; let g = { a: 1 }; const v = g++; const result = [o, f, v, g];`,
		`[{"#number":"NaN"},{"#number":"NaN"},{"#number":"NaN"},{"#number":"NaN"}]`, "", ""},
	{"dates", `let d = new Date(1000); d++; let e = new Date(5000); const v = e--; const result = [d, typeof d, v, e];`, `[1001,"number",5000,4999]`, "", ""},
	{"infinity-nan-and-minus-zero", `let i = Infinity; i++; let n = NaN; n--; let m = -0; const v = m++; const result = [i, n, v, m];`,
		`[{"#number":"Infinity"},{"#number":"NaN"},{"#number":"-0"},1]`, "", ""},
	{"members-and-items", `const o = { n: "5" }; o.n++; const a = ["9", true]; a[0]--; a[1]++; const c = { k: null }; c["k"]++; const result = [o, a, c];`,
		`[{"n":6},[8,2],{"k":1}]`, "", ""},
	{"counting-loops", `let total = 0; for (let i = 0; i < 5; i++) { total += i; } for (let j = 3; j > 0; j--) { total += j; } const result = total;`, `16`, "", ""},
	{"CONTROL-plus-still-joins", `let s = "5"; s += 1; let t = "5"; t = t + 1; const result = [s, t];`, `["51","51"]`, "", ""},
	// 16/10/2026 (the owner's E2): an array steps from the number its text is, as in JavaScript (it was NaN)
	{"arrays", `let a = []; a++; let b = [5]; b--; let c = [" 7 "]; c++; let d = [1, 2]; d++; const result = [a, b, c, d];`, `[1,4,8,{"#number":"NaN"}]`, "", ""},
	// what stays different
	{"a-missing-key-counts-from-zero", `const o = {}; o.count++; const p = {}; p.count--; const result = [o.count, p.count];`, `[1,-1]`,
		`[{"#number":"NaN"},{"#number":"NaN"}]`,
		"a missing key is null in Kitwork, which ++ and -- make 0 as JavaScript makes null, so o.count++ on a fresh object counts from 0; JavaScript's undefined makes NaN"},
	{"a-value-of-the-program", `const o = { valueOf: () => 7 }; let x = o; x++; const result = x;`, `{"#number":"NaN"}`, `8`,
		"++ calls no code of the program: an object is NaN unless its valueOf is the engine's own (a date's); JavaScript calls the program's valueOf"},
}

// stepSyntaxCases: ++ or -- of an update is told in the words the source wrote (15/10/2026: it was told
// of the lowering, "không gán được vào (x = (x + 1)): vế trái của = …").
var stepSyntaxCases = []syntaxCase{
	{"an-update-of-an-update", `let x = 1; ++x++;`, "1:12: không tăng được x++: ++ và -- chỉ dùng trên một tên, o.x hay o[k]"},
	{"a-postfix-update-of-an-update", `let x = 1; (x++)++;`, "1:17: không tăng được x++: ++ và -- chỉ dùng trên một tên"},
	{"a-decrement-of-a-decrement", `let x = 1; --x--;`, "1:12: không giảm được x--: ++ và -- chỉ dùng trên một tên"},
	{"of-a-member", `const o = { n: 1 }; ++o.n--;`, "không tăng được o.n--"},
}

func TestStepMakesANumberAsInJavaScript(t *testing.T) { checkParity(t, stepCases, stepSyntaxCases) }

// exactEqualityCases (16/10/2026, the owner's E1): == and === compare two numbers exactly, as JavaScript
// does (IEEE 754), and switch, includes, indexOf, lastIndexOf, find and filter follow them. Two numbers
// less than 1e-12 apart were equal (value.equalScalar), so 0.1 + 0.2 === 0.3 and 1e-13 === 0 were true.
// CONTROL: numbers that are equal stay equal, a sum included.
var exactEqualityCases = []parityCase{
	{"sums", `const result = [0.1 + 0.2 === 0.3, 0.1 + 0.2 == 0.3, 0.1 + 0.2 !== 0.3, 0.1 + 0.2 != 0.3, 0.3 - 0.1 === 0.2, 1e-13 === 0, 1e-13 == 0, 0.1 * 3 === 0.3, 1.1 + 2.2 === 3.3];`,
		`[false,false,true,true,false,false,false,false,false]`, "", ""},
	{"CONTROL-equal-numbers", `const result = [0.5 + 0.25 === 0.75, 1 + 1e-16 === 1, 9007199254740992 === 9007199254740993, -0 === 0, 0.1 + 0.2 === 0.30000000000000004, 1 / 0 === Infinity, 2.5 * 4 == 10];`,
		`[true,true,true,true,true,true,true]`, "", ""},
	{"nan", `const n = 0 / 0; const result = [n === n, n == n, n !== n, n != n, [n].indexOf(n), [n].lastIndexOf(n), [n].includes(n)];`,
		`[false,false,true,true,-1,-1,true]`, "", ""},
	{"switch", `const pick = (x) => { switch (x) { case 0.3: return "0.3"; case 0: return "zero"; default: return "other"; } }; const result = [pick(0.1 + 0.2), pick(1e-13), pick(0.3), pick(0), pick(-0)];`,
		`["other","other","0.3","zero","zero"]`, "", ""},
	{"searches", `const sum = 0.1 + 0.2; const list = [0.3, 1e-13, sum]; const result = [list.includes(sum), list.indexOf(sum), list.lastIndexOf(0.3), list.indexOf(0), list.includes(0), [0.3].includes(sum), [0.3].indexOf(sum), [0.3].lastIndexOf(sum), [1e-13].indexOf(0)];`,
		`[true,2,0,-1,false,false,-1,-1,-1]`, "", ""},
	{"callbacks", `const sum = 0.1 + 0.2; const result = [[0.3].findIndex((x) => x === sum), [sum, 0.3].filter((x) => x == 0.3).length, [0.3, sum].find((x) => x !== 0.3) === sum, [1e-13, 0].some((x) => x === 0), [1e-13].every((x) => x == 0)];`,
		`[-1,1,true,true,false]`, "", ""},
	// 16/10/2026: lastIndexOf read no fromIndex ([1, 2, 1].lastIndexOf(1, 1) was 2), and indexOf and
	// includes read none that was not a number ([1, 2, 1].indexOf(1, "1") was 0). CONTROL: the rows with
	// no fromIndex, a number one and one past either end.
	{"search-from", `const list = [1, 2, 1]; const result = [list.lastIndexOf(1, 1), list.lastIndexOf(1, -2), list.lastIndexOf(1, 0), list.lastIndexOf(1, -5), list.lastIndexOf(1, NaN), list.lastIndexOf(1, 5), list.lastIndexOf(1, -Infinity), list.lastIndexOf(1, Infinity), list.lastIndexOf(1, "1"), list.lastIndexOf(1, null), list.lastIndexOf(1, 1.9), list.lastIndexOf(1, -1), [].lastIndexOf(1, 0), list.indexOf(1, "1"), list.indexOf(1, "x"), list.includes(1, "2"), list.indexOf(1, null), list.indexOf(1, 1.5), list.includes(2, "2")];`,
		`[0,0,0,-1,0,2,-1,2,0,0,0,2,-1,2,0,true,0,2,false]`, "", ""},
	{"CONTROL-search", `const list = [1, 2, 1]; const result = [list.lastIndexOf(1), list.lastIndexOf(2), list.lastIndexOf(3), list.indexOf(1, 1), list.indexOf(1, -1), list.indexOf(1, 9), list.includes(1, -1), list.indexOf(1)];`,
		`[2,1,-1,2,2,-1,true,0]`, "", ""},
	// what stays different (16/10/2026: docs/KITWORK_LANG.md lists each with its reason)
	{"equality-converts-nothing-and-compares-content", `const a = [1, 2]; const o = { a: 1 }; const result = ["1" == 1, 0 == "", true == 1, "1" != 1, null == 0, null == undefined, [1, 2] === [1, 2], [1, 2] == [1, 2], { a: 1 } === { a: 1 }, a === a, o === o, [1, 2] !== [1, 2]];`,
		`[false,false,false,true,false,true,true,true,true,true,true,false]`, `[true,true,true,false,false,true,false,false,false,true,true,true]`,
		"== is === (value.Equal): it converts no type, so \"1\" == 1 is false, and two arrays or two objects are equal when what they hold is; JavaScript's == converts (\"1\" == 1 is true), and both == and === compare an array or an object by reference"},
	{"an-engine-function-is-not-equal-to-itself", `const f = () => 1; const g = f; const result = [parseInt === parseInt, Math.max === Math.max, Number.parseInt === parseInt, parseInt !== parseInt, f === f, f === g, f === (() => 1)];`,
		`[false,false,false,true,true,true,false]`, `[true,true,true,false,true,true,false]`,
		"a function of the engine holds a Go function, and a Go function is equal to nothing, itself included (reflect.DeepEqual in value.equalScalar); a function the program writes compares by reference, as in JavaScript"},
}

func TestNumbersCompareExactlyAsInJavaScript(t *testing.T) { checkParity(t, exactEqualityCases, nil) }

// conversionCases (16/10/2026, the owner's E2): Number(), unary +, parseFloat and parseInt read as
// JavaScript reads. Number of what is no number is NaN (it was null), an array is a number through its
// text ([5] is 5; every array was NaN), +x is Number(x) (it did not parse), parseFloat reads the number
// the text begins with ("3.5px" is 3.5; it read the whole text, and no number was 0), parseInt reads 0x
// without a radix, a radix outside 2 to 36 is NaN, no digit is NaN, and digits past the int64 range
// round as JavaScript rounds them (it read 0x only with the radix 16, made a radix outside 2 to 36 ten,
// and gave 0). Kitwork keeps one value for null and undefined, which Number makes 0 as JavaScript makes
// null (is-nan-of-undefined says where that differs).
var conversionCases = []parityCase{
	{"number-of-text", `const result = [Number("abc"), Number(""), Number("  "), Number(" 12 "), Number("0x1f"), Number("1e3"), Number("12px"), Number("-0x10"), Number("0b101"), Number("0o17"), Number("\t5\n"), Number("-"), Number(".")];`,
		`[{"#number":"NaN"},0,0,12,31,1000,{"#number":"NaN"},{"#number":"NaN"},5,15,5,{"#number":"NaN"},{"#number":"NaN"}]`, "", ""},
	{"number-of-other-values", `const result = [Number(null), Number(true), Number(false), Number([]), Number([5]), Number(["7"]), Number([[" 8 "]]), Number([1, 2]), Number([null]), Number({}), Number(), Number(new Date(5)), Number(() => 1), Number([new Date(5)])];`,
		`[0,1,0,0,5,7,8,{"#number":"NaN"},0,{"#number":"NaN"},0,5,{"#number":"NaN"},{"#number":"NaN"}]`, "", ""},
	{"no-number-is-nan-not-null", `const n = Number("abc"); const result = [n === null, n == null, n ?? "fallback", isNaN(n), Number.isNaN(n), typeof n, Number(" ") || "zero is false", n || "nan is false"];`,
		`[false,false,{"#number":"NaN"},true,true,"number","zero is false","nan is false"]`, "", ""},
	{"an-array-that-holds-itself", `const a = []; a.push(a); const b = [[]]; b[0].push(b); const result = [Number(a), Number(b), isNaN(a), parseInt(a), parseFloat(b)];`,
		`[0,0,false,{"#number":"NaN"},{"#number":"NaN"}]`, "", ""},
	{"unary-plus", `const o = { b: "4" }; const f = () => "6"; const result = [+"5", +"", +"abc", +null, +true, +false, +[], +[5], +{}, +"0x1f", +" 12 ", +new Date(5), +o.b, +f(), +"Infinity", +[1, 2], +"-0"];`,
		`[5,0,{"#number":"NaN"},0,1,0,0,5,{"#number":"NaN"},31,12,5,4,6,{"#number":"Infinity"},{"#number":"NaN"},{"#number":"-0"}]`, "", ""},
	{"unary-plus-in-an-expression", `const a = "2"; const result = [1 + +a, +a + +a, "x" + +a, +"5" * 2, - +"3", + +"5", +-1, +(-1), typeof +"5", +a === 2, !+"0", [+a, +a]];`,
		`[3,4,"x2",10,-3,5,-1,-1,"number",true,true,[2,2]]`, "", ""},
	{"a-line-break-before-plus-continues", "const x = 1\n+2\nconst y = \"4\"\nconst z = 1\n+y\nconst result = [x, z];", `[3,"14"]`, "", ""},
	{"parse-float", `const result = [parseFloat("3.5px"), parseFloat(".5"), parseFloat("abc"), parseFloat(""), parseFloat("  -2.5e2kg"), parseFloat("1e"), parseFloat("1e+"), parseFloat("1.2.3"), parseFloat("+.5"), parseFloat("-.5e1x"), parseFloat("Infinity1"), parseFloat("-Infinityx"), parseFloat("0x10"), parseFloat("1_000"), parseFloat("inf"), parseFloat("1e400"), parseFloat("-0"), parseFloat("5."), parseFloat(".e1"), parseFloat("\u00a0 7")];`,
		`[3.5,0.5,{"#number":"NaN"},{"#number":"NaN"},-250,1,1,1.2,0.5,-5,{"#number":"Infinity"},{"#number":"-Infinity"},0,1,{"#number":"NaN"},{"#number":"Infinity"},{"#number":"-0"},5,{"#number":"NaN"},7]`, "", ""},
	{"parse-float-of-other-values", `const result = [parseFloat(), parseFloat(null), parseFloat(3.25), parseFloat([" 2.5 ", 3]), parseFloat(true), parseFloat({}), parseFloat(1e21), parseFloat("1e-7"), parseFloat([[]]), Number.parseFloat("3.5px"), parseFloat([[1.5], 2])];`,
		`[{"#number":"NaN"},{"#number":"NaN"},3.25,2.5,{"#number":"NaN"},{"#number":"NaN"},1e+21,1e-7,{"#number":"NaN"},3.5,1.5]`, "", ""},
	{"parse-int", `const result = [parseInt("42"), parseInt("  -12"), parseInt("+7"), parseInt("12px"), parseInt("0x1f"), parseInt("0X1F"), parseInt("-0x1f"), parseInt("0x"), parseInt("abc"), parseInt(""), parseInt("1e3"), parseInt("99999999999999999999"), parseInt("-0"), parseInt("  0012"), parseInt("08"), parseInt("0b11"), parseInt("1.9"), parseInt(".5"), parseInt("\u00a0\ufeff 9"), parseInt("-")];`,
		`[42,-12,7,12,31,31,-31,{"#number":"NaN"},{"#number":"NaN"},{"#number":"NaN"},1,100000000000000000000,{"#number":"-0"},12,8,0,1,{"#number":"NaN"},9,{"#number":"NaN"}]`, "", ""},
	{"parse-int-radix", `const result = [parseInt("0x1f", 16), parseInt("0x1f", 10), parseInt("ff", 16), parseInt("11", 2), parseInt("7", 37), parseInt("7", 1), parseInt("7", 0), parseInt("z", 36), parseInt("Z", 36), parseInt("2", 2), parseInt("10", 16.9), parseInt("10", "16"), parseInt("10", NaN), parseInt("10", null), parseInt("10", 4294967312), parseInt("10", -4294967280), parseInt("1".repeat(30), 2), parseInt("zz", 37), parseInt("0x10", 0), parseInt("-ff", 16)];`,
		`[31,0,255,3,{"#number":"NaN"},{"#number":"NaN"},7,35,35,{"#number":"NaN"},16,16,10,10,16,16,1073741823,{"#number":"NaN"},16,-255]`, "", ""},
	{"parse-int-of-other-values", `const result = [parseInt(null), parseInt(), parseInt(12.7), parseInt(-12.7), parseInt(1e21), parseInt(0.0000005), parseInt([" 15 ", 3]), parseInt(true), parseInt({}), Number.parseInt("12px"), Number.parseInt(" 0x1f "), Number.parseFloat(".5e1x"), parseInt(1 / 0)];`,
		`[{"#number":"NaN"},{"#number":"NaN"},12,-12,1,5,15,{"#number":"NaN"},{"#number":"NaN"},12,31,5,{"#number":"NaN"}]`, "", ""},
	// the forms the apps write (a scan of every app, 16/10/2026): each guard still catches what is no
	// number, and text in hex reads as JavaScript reads it
	{"what-the-apps-write", `const query = (v) => v; const page = parseInt(query("") || "1"); const typed = parseInt(query("abc")) || 1; const hex = parseInt(query("0x2")) || 1; const requested = parseInt(query("abc") || "1"); const clamped = requested > 0 ? requested : 1; const dollars = Math.floor(Number("x")); const amount = Number(query(null)) || 0; const result = [page, typed, hex, clamped, dollars, !(dollars >= 1), amount, isNaN(Number("x"))];`,
		`[1,1,2,1,{"#number":"NaN"},true,0,true]`, "", ""},
	// what stays different (16/10/2026)
	{"parse-int-past-2-53-in-another-base", `const result = [parseInt("ffffffffffffffff", 36), parseInt("1".repeat(53), 36), parseInt("ffffffffffffffffffff", 16), parseInt("1".repeat(70), 2), parseInt("zz", 36)];`,
		`[3.4108547614056005e+24,8.708923913719803e+80,1.2089258196146292e+24,1.1805916207174113e+21,1295]`,
		`[3.410854761405601e+24,8.708923913719802e+80,1.2089258196146292e+24,1.1805916207174113e+21,1295]`,
		"past 2^53 Kitwork rounds the digits of every base to the nearest number (value.ParseIntText); JavaScript asks for that in the bases 10, 2, 4, 8, 16 and 32 only and lets an engine approximate in any other, and V8 adds the digits up in parts, which can differ in the last bit"},
	{"a-value-of-the-program-is-not-called", `const v = { valueOf: () => 7 }; const result = [Number(v), +v, isNaN(v), isFinite(v), Math.abs(v), Math.max(v, 1)];`,
		`[{"#number":"NaN"},{"#number":"NaN"},true,false,{"#number":"NaN"},{"#number":"NaN"}]`, `[7,7,false,true,7,7]`,
		"Number, unary +, isNaN, isFinite and Math call no code of the program (value.ToNumber), as ++ does not (a-value-of-the-program): an object is NaN unless its valueOf is the engine's own (a date's); JavaScript calls the program's valueOf"},
	{"a-missing-key-is-zero", `const o = {}; const result = [Number(o.missing), +o.missing, Math.abs(o.missing), isFinite(o.missing), Number(null), isFinite(null)];`,
		`[0,0,0,true,0,true]`, `[{"#number":"NaN"},{"#number":"NaN"},{"#number":"NaN"},false,0,true]`,
		"a missing key is null, Kitwork's one value for null and undefined, which Number, unary +, Math and isFinite make 0 as JavaScript makes null; JavaScript's undefined is NaN (is-nan-of-undefined, a-missing-key-counts-from-zero)"},
	{"binary-operators-convert-nothing", `const tried = [safe(() => "5" - 1), safe(() => "6" * 2), safe(() => "6" / 2), safe(() => -"3"), safe(() => { let s = "5"; s -= 1; return s; })]; const result = [tried.map((r) => r.error ? "failed" : r.value), "5" < 6, "5" > 4, +"5" - 1, "10" > "9"];`,
		`[["failed","failed","failed","failed","failed"],false,false,4,false]`, `[[4,12,3,-3,4],true,true,4,false]`,
		"-, *, / and -= on a text, and unary - of one, are the runtime error TYPE, and < and > between a text and a number are false (value.Sub, value.Less): only ++, --, unary + and Number() make a number of a text; JavaScript makes one for every operator"},
	{"a-date-is-no-number-to-an-operator", `const a = new Date(17280000005); const b = new Date(17280000003); const d = safe(() => a - b); const result = [d.error ? "failed" : d.value, a > b, b < a, a.getTime() > b.getTime(), new Date(a).getTime() === 17280000005, String(a).includes("1970"), isNaN(Date.parse("Jan 15, 2024")), isNaN(Date.parse("2024-01-15"))];`,
		`["failed",false,false,true,false,false,true,false]`, `[2,true,true,true,true,true,false,false]`,
		"a date is an object of the engine's whose number only isNaN, isFinite, Number(), Math and ++ read (its valueOf), so a - b of two dates is TYPE and < and > between them are false; new Date(date) is now, not a copy (builtins timeFromArgs), String(date) writes the object's methods, and Date.parse reads a few layouts (2024-01-15, RFC 3339, 2024-01-15 10:00:00); JavaScript reads a date's number for each, copies it, writes its time and reads more layouts"},
}

var conversionSyntaxCases = []syntaxCase{
	{"plus-before-nothing", `const a = +;`, `không đọc được ";" ở đầu một biểu thức`},
}

func TestConversionsReadAsInJavaScript(t *testing.T) {
	checkParity(t, conversionCases, conversionSyntaxCases)
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
	// 15/10/2026: what else typeof gives differently, which the language guide said it did not
	{"a-missing-key-named-as-a-builtin", `const o = { other: 1 }; const result = [typeof o.type, typeof o.length, typeof o.text, typeof o.keys, typeof o.has, typeof o.other, typeof o.nothing];`,
		`["string","number","function","function","function","number","undefined"]`, `["undefined","undefined","undefined","undefined","undefined","number","undefined"]`,
		"a missing key named as a builtin (type, length, text, keys, has, json, merge…) reads the builtin; test a key with o.has(\"k\")"},
	{"bigint", `const result = typeof BigInt(5);`, `"number"`, `"bigint"`, "BigInt(x) gives a number in Kitwork"},
	// 15/10/2026 (L2): a division by zero is Infinity or NaN, numbers (it was null, "undefined")
	{"division-by-zero", `const result = [typeof (1 / 0), typeof (0 / 0), typeof (1e308 * 10)];`, `["number","number","number"]`, "", ""},
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
		got, failure := parityRun(t, c.source)
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

// parityRun runs source as grammarRun does and gives result as JSON with NaN, Infinity, -Infinity and -0
// written as {"#number": "NaN"} and so on, as node's side writes them (15/10/2026).
func parityRun(t *testing.T, source string) (string, *runtime.Diagnostic) {
	t.Helper()
	bytecode, err := CompileSource(source)
	if err != nil {
		t.Fatalf("compile %q: %v", source, err)
	}
	vm := runtime.New(bytecode.Program)
	vm.MaxEnergy = 10_000_000
	builtins.InjectJSCompat(vm.Globals)
	if returned := vm.Run(); returned.IsInvalid() {
		diagnostic, ok := runtime.DiagnosticFrom(returned)
		if !ok {
			t.Fatalf("run %q: %s", source, returned.Text())
		}
		return "", diagnostic
	}
	data, err := json.Marshal(parityValue(vm.Vars["result"]))
	if err != nil {
		t.Fatalf("encode result of %q: %v", source, err)
	}
	return string(data), nil
}

// parityValue is what JSON writes for a value, a number JSON has no spelling for marked.
func parityValue(v value.Value) any {
	switch v.K {
	case value.Number:
		switch {
		case v.N != v.N:
			return map[string]any{"#number": "NaN"}
		case math.IsInf(v.N, 1):
			return map[string]any{"#number": "Infinity"}
		case math.IsInf(v.N, -1):
			return map[string]any{"#number": "-Infinity"}
		case v.N == 0 && math.Signbit(v.N):
			return map[string]any{"#number": "-0"}
		}
		return v.N
	case value.Array:
		items := []any{}
		for _, item := range v.Array() {
			items = append(items, parityValue(item))
		}
		return items
	case value.Map:
		entries := map[string]any{}
		for key, item := range v.Map() {
			entries[key] = parityValue(item)
		}
		return entries
	}
	return v.Interface()
}

// runNode runs the rows in one node process: each as a strict function body that returns result as JSON
// (NaN, Infinity, -0 and undefined marked as parityValue marks them), each syntax row through new
// Function, which must throw. ok is false when node is not on PATH.
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
const mark = (key, v) => {
  if (v === undefined) return { "#undefined": true };
  if (typeof v === "number") {
    if (v !== v) return { "#number": "NaN" };
    if (v === Infinity) return { "#number": "Infinity" };
    if (v === -Infinity) return { "#number": "-Infinity" };
    if (Object.is(v, -0)) return { "#number": "-0" };
  }
  return v;
};
const rows = ` + string(encoded) + `;
const out = rows.map((row) => {
  if (row.syntax) {
    try { new Function("'use strict';\n" + row.source); return "compiled"; } catch (e) { return e.name; }
  }
  try { return JSON.stringify(new Function("'use strict';\n" + row.source + "\n;return result;")(), mark); }
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

// x++ and ++x whose value is dropped (a statement, a for's update) compile to the assignment they were,
// x = x ± 1, with its PUSH 1; ADD (or SUB) as one STEP (bytecode 4, 15/10/2026): two bytes shorter, the
// same energy, nothing else moved. CONTROL: x++ used as a value makes the old x a number too (STEP 0),
// which the dropped one never does.
func TestADroppedUpdateIsTheAssignmentWithOneStep(t *testing.T) {
	for _, pair := range [][2]string{
		{"let x = 1;\nx++;", "let x = 1;\nx = x + 1;"},
		{"let x = 1;\n--x;", "let x = 1;\nx = x - 1;"},
		{"const o = { n: 1 };\no.n++;", "const o = { n: 1 };\no.n = o.n + 1;"},
		{"const a = [1];\na[0]--;", "const a = [1];\na[0] = a[0] - 1;"},
		{"let t = 0;\nfor (let i = 0; i < 3; i++) { t += i; }", "let t = 0;\nfor (let i = 0; i < 3; i = i + 1) { t += i; }"},
		{"const f = [];\nfor (let i = 0; i < 3; i++) { f.push(() => i); }", "const f = [];\nfor (let i = 0; i < 3; i = i + 1) { f.push(() => i); }"},
	} {
		update, assignment := compileBoth(t, pair[0], pair[1])
		assertOneStep(t, update, assignment, pair[0], pair[1])
	}
	used, dropped := compileBoth(t, "let x = 1;\nconst y = x++;", "let x = 1;\nx++;")
	if !slices.Contains(listing(t, used), "STEP 0") || slices.Contains(listing(t, dropped), "STEP 0") {
		t.Fatalf("CONTROL: x++ as a value %v, dropped %v: want STEP 0 in the first only", listing(t, used), listing(t, dropped))
	}
}

// An update or a compound assignment whose object and key only read compiles as it did before its object
// and key ran once (15/10/2026): a chain of members, an index computed from names; an update with its
// PUSH 1; ADD as one STEP (bytecode 4). CONTROL: one whose key calls a function keeps the key in a slot,
// so it differs from the assignment written out.
func TestAnUpdateThatOnlyReadsCompilesAsBefore(t *testing.T) {
	for _, pair := range [][2]string{
		{"const o = { a: { b: 1 } };\no.a.b++;", "const o = { a: { b: 1 } };\no.a.b = o.a.b + 1;"},
		{"const m = [[1]];\nlet i = 0;\nm[i][i ? 1 : 0]--;", "const m = [[1]];\nlet i = 0;\nm[i][i ? 1 : 0] = m[i][i ? 1 : 0] - 1;"},
	} {
		update, assignment := compileBoth(t, pair[0], pair[1])
		assertOneStep(t, update, assignment, pair[0], pair[1])
	}
	for _, pair := range [][2]string{
		{"const a = [1, 2];\nlet i = 0;\na[i + 1] += 3;", "const a = [1, 2];\nlet i = 0;\na[i + 1] = a[i + 1] + 3;"},
		{"const c = {};\nconst k = \"x\";\nc[`${k}-1`] += 1;", "const c = {};\nconst k = \"x\";\nc[`${k}-1`] = c[`${k}-1`] + 1;"},
	} {
		compound, assignment := compileBoth(t, pair[0], pair[1])
		assertSameCode(t, compound, assignment, pair[0], pair[1])
	}
	once, twice := compileBoth(t, "const a = [1];\nconst f = () => 0;\na[f()]++;", "const a = [1];\nconst f = () => 0;\na[f()] = a[f()] + 1;")
	if got, want := listing(t, once), stepped(t, listing(t, twice)); slices.Equal(got, want) {
		t.Fatalf("CONTROL: a[f()]++ compiles as a[f()] = a[f()] + 1, which calls f twice")
	}
}

// assertOneStep checks that update compiles as assignment (x = x ± 1) with its last PUSH 1; ADD or SUB as
// STEP 1 or STEP 2, two bytes shorter and at the same energy.
func assertOneStep(t *testing.T, update, assignment *Bytecode, updateSource, assignmentSource string) {
	t.Helper()
	got, want := listing(t, update), stepped(t, listing(t, assignment))
	if !slices.Equal(got, want) {
		t.Fatalf("%q compiles to\n%v\nwant %q with one STEP:\n%v", updateSource, got, assignmentSource, want)
	}
	if len(update.Instructions()) != len(assignment.Instructions())-2 {
		t.Fatalf("%q is %d bytes, %q %d: want two fewer", updateSource, len(update.Instructions()), assignmentSource, len(assignment.Instructions()))
	}
	if got, want := update.Program.Profile().EncodedEnergy, assignment.Program.Profile().EncodedEnergy; got != want {
		t.Fatalf("%q costs %d energy as written, %q %d: want the same", updateSource, got, assignmentSource, want)
	}
}

// listing writes a program's instructions one per line, by what they do rather than how they are
// numbered: a constant as its kind and text ("PUSH number 1", "LOAD string x"), a function as where it
// starts, a jump as where it lands, both counted in instructions.
func listing(t *testing.T, bytecode *Bytecode) []string {
	t.Helper()
	code, constants := bytecode.Instructions(), bytecode.Constants()
	type decoded struct {
		name     string
		operands []uint32
	}
	var instructions []decoded
	at := map[int]int{}
	for ip := 0; ip < len(code); {
		spec, ok := runtime.LookupInstruction(runtime.Opcode(code[ip]))
		if !ok {
			t.Fatalf("opcode %d at byte %d", code[ip], ip)
		}
		operands, size, err := runtime.DecodeOperands(spec, code[ip+1:])
		if err != nil {
			t.Fatal(err)
		}
		at[ip] = len(instructions)
		instructions = append(instructions, decoded{strings.TrimSuffix(spec.Name, "_WIDE"), operands})
		ip += 1 + size
	}
	at[len(code)] = len(instructions)
	lines := make([]string, len(instructions))
	for index, instruction := range instructions {
		line := instruction.name
		switch instruction.name {
		case "PUSH", "LOAD", "STORE":
			constant := constants[instruction.operands[0]]
			if lambda, ok := constant.V.(*value.Lambda); ok {
				line += fmt.Sprintf(" function at %d (%s)", at[lambda.Address], strings.Join(lambda.Params, ", "))
			} else {
				line += " " + constant.K.String() + " " + constant.Text()
			}
		case "JUMP", "TRUE", "FALSE", "ITER":
			line += fmt.Sprintf(" to %d", at[int(instruction.operands[0])])
		default:
			for _, operand := range instruction.operands {
				line += fmt.Sprintf(" %d", operand)
			}
		}
		lines[index] = line
	}
	return lines
}

// stepped is the listing of x = x ± 1 with its last PUSH 1; ADD (or SUB) as STEP 1 (or 2): what x++ (or
// x--) compiles to since bytecode 4. Every jump and function start past the pair moves back one.
func stepped(t *testing.T, lines []string) []string {
	t.Helper()
	pair := -1
	for index := len(lines) - 2; index >= 0; index-- {
		if lines[index] == "PUSH number 1" && (lines[index+1] == "ADD" || lines[index+1] == "SUB") {
			pair = index
			break
		}
	}
	if pair < 0 {
		t.Fatalf("no PUSH 1; ADD in %v", lines)
	}
	step := "STEP 1"
	if lines[pair+1] == "SUB" {
		step = "STEP 2"
	}
	moved := regexp.MustCompile(`(to|function at) (\d+)`)
	out := make([]string, 0, len(lines)-1)
	for index, line := range lines {
		switch {
		case index == pair:
			out = append(out, step)
		case index == pair+1:
		default:
			out = append(out, moved.ReplaceAllStringFunc(line, func(found string) string {
				parts := strings.SplitN(found, " ", 3)
				target, _ := strconv.Atoi(parts[len(parts)-1])
				if target > pair+1 {
					target--
				}
				return strings.Join(parts[:len(parts)-1], " ") + " " + strconv.Itoa(target)
			}))
		}
	}
	return out
}

// A counting loop costs, with i++ and n++ (STEP), the energy it cost with i = i + 1 and n = n + 1: each
// STEP costs what PUSH 1; ADD did (bytecode 4, 15/10/2026). CONTROL: one more statement in the loop costs
// more.
func TestAnUpdateCostsTheEnergyItCost(t *testing.T) {
	energy := func(source string) uint64 {
		bytecode, err := CompileSource(source)
		if err != nil {
			t.Fatal(err)
		}
		vm := runtime.New(bytecode.Program)
		vm.MaxEnergy = 10_000_000
		if returned := vm.Run(); returned.IsInvalid() {
			t.Fatalf("run %q: %s", source, returned.Text())
		}
		return vm.Stats().Energy
	}
	stepping := energy("let n = 0;\nfor (let i = 0; i < 1000; i++) { n++; }\nconst result = n;")
	adding := energy("let n = 0;\nfor (let i = 0; i < 1000; i = i + 1) { n = n + 1; }\nconst result = n;")
	if stepping != adding {
		t.Fatalf("1000 iterations cost %d energy with ++, %d with + 1: want the same", stepping, adding)
	}
	if more := energy("let n = 0;\nfor (let i = 0; i < 1000; i++) { n++; n++; }\nconst result = n;"); more <= stepping {
		t.Fatalf("CONTROL: two updates per iteration cost %d, one %d", more, stepping)
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

// A write through a value that cannot hold properties says what it wrote and what it wrote through, as the
// source wrote them, and the kind of that value, never a value, at the write's dot or bracket, with the
// code TYPE (15/10/2026, the owner's L1). Uncaught, it ends the run with its file:line:column. CONTROL: an
// object and an array take the same writes.
func TestAWriteThroughANonHolderSaysWhatAndWhere(t *testing.T) {
	source := strings.Join([]string{
		`const o = { name: "kit", list: [1] };`,
		`const k = "x";`,
		`const results = [`,
		`  safe(() => { o.name.x = 1; }),`,
		`  safe(() => { o.missing[k] = 1; }),`,
		`  safe(() => { o.list[0].y = 2; }),`,
		`  safe(() => { o.name[0] = "K"; }),`,
		`  safe(() => { o.list[0][k + 1] = 3; }),`,
		`  safe(() => { const f = () => 1; f.extra = 1; }),`,
		`  safe(() => { let n = null; n.count++; })`,
		`];`,
		`const control = { name: {}, list: [{}] };`,
		`control.name.x = 1;`,
		`control.list[0].y = 2;`,
		`const result = [results.map((r) => [r.error.code, r.error.at, r.error.message]), control];`,
	}, "\n")
	said := func(write, through, kind string) string {
		return "không ghi được " + write + ": " + through + " là " + kind + ", không phải object hay mảng"
	}
	rows := [][3]string{
		{"TYPE", "<source>:4:22", said("o.name.x", "o.name", "chuỗi")},
		{"TYPE", "<source>:5:25", said("o.missing[k]", "o.missing", "null")},
		{"TYPE", "<source>:6:25", said("o.list[0].y", "o.list[0]", "số")},
		{"TYPE", "<source>:7:22", said("o.name[0]", "o.name", "chuỗi")},
		{"TYPE", "<source>:8:25", said("o.list[0][…]", "o.list[0]", "số")},
		{"TYPE", "<source>:9:36", said("f.extra", "f", "hàm")},
		{"TYPE", "<source>:10:31", said("n.count", "n", "null")},
	}
	encoded, err := json.Marshal([]any{rows, map[string]any{"name": map[string]any{"x": 1}, "list": []any{map[string]any{"y": 2}}}})
	if err != nil {
		t.Fatal(err)
	}
	got, failure := grammarRun(t, source, nil)
	if failure != nil || !sameJSON(t, got, string(encoded)) {
		t.Fatalf("result = %s (%v)\nwant     %s", got, failure, encoded)
	}

	_, failure = grammarRun(t, "const s = \"abc\";\ns[0] = \"x\";\nconst result = s;", nil)
	if failure == nil || failure.CauseCode != value.TypeCode || failure.Message != said("s[0]", "s", "chuỗi") ||
		!strings.HasPrefix(failure.Error(), "<source>:2:2: "+failure.Message) {
		t.Fatalf("uncaught: %v", failure)
	}
}

// A value of the host keeps the rule it had: a Struct (an object of the engine, as ctx), a Proxy (a
// query builder's row), and an engine time, duration or bytes (typeof "object"; the owner's list of
// 15/10/2026 is null, the primitives and functions, and the review took these three back out) take no
// write from a script, and the write is dropped without an error, as before. CONTROL: a write through
// null on the next line fails.
func TestAWriteThroughAHostValueIsStillDropped(t *testing.T) {
	globals := map[string]value.Value{
		"proxy": {K: value.Proxy, V: anyMethodProxy{}},
		"host":  value.New(&positionHost{}),
		"t":     value.New(time.UnixMilli(1500)),
		"d":     value.New(1500 * time.Millisecond),
		"b":     value.New([]byte("ab")),
	}
	got, failure := grammarRun(t, "proxy.x = 1;\nproxy[0] = 2;\nhost.extra = 3;\nt.label = \"x\";\nd.x = 1;\nb[0] = 1;\nconst result = [\"ran\", host.extra ?? \"missing\", typeof t, typeof d, typeof b];", globals)
	if failure != nil || got != `["ran","missing","object","object","object"]` {
		t.Fatalf("result = %s (%v), want the writes dropped", got, failure)
	}
	_, failure = grammarRun(t, "host.extra = 3;\nconst n = null;\nn.extra = 3;\nconst result = 1;", globals)
	if failure == nil || failure.CauseCode != value.TypeCode || !strings.HasPrefix(failure.Error(), "<source>:3:2: ") {
		t.Fatalf("CONTROL: a write through null = %v", failure)
	}
}
