package compiler

import (
	"strconv"
	"strings"
	"testing"

	"github.com/kitwork/engine/builtins"
	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
)

// safeCallPrefix is what runNotAFunction writes before the call it is given.
const safeCallPrefix = "const r = safe(() => "

// runNotAFunction runs setup, then const r = safe(() => call), and gives r.error's message, code and
// at as the VM left them ("" for each when nothing failed).
func runNotAFunction(t *testing.T, setup, call string, globals map[string]value.Value) (string, string, string) {
	t.Helper()
	source := safeCallPrefix + call + ")\nconst result = [r.error.message, r.error.code, r.error.at]"
	if setup != "" {
		source = setup + "\n" + source
	}
	bytecode, err := CompileSource(source)
	if err != nil {
		t.Fatalf("compile %q: %v", source, err)
	}
	vm := runtime.New(bytecode.Program)
	builtins.InjectJSCompat(vm.Globals)
	for name, item := range globals {
		vm.Globals[name] = item
	}
	if returned := vm.Run(); returned.IsInvalid() {
		t.Fatalf("run %q: %s", source, returned.Text())
	}
	result := vm.Vars["result"]
	return result.At(0).Text(), result.At(1).Text(), result.At(2).Text()
}

// notAFunctionHost is a host value, as a capability is: a getter (a Go method without arguments),
// a method with an argument, and an exported field.
type notAFunctionHost struct{ Count int }

func (*notAFunctionHost) Name() string               { return "host" }
func (*notAFunctionHost) Greet(who string) string    { return "chào " + who }
func (*notAFunctionHost) unexported() string         { return "hidden" }
func (*notAFunctionHost) Join(a, b string) string    { return a + b }
func (*notAFunctionHost) Values() map[string]any     { return map[string]any{"a": 1} }
func (*notAFunctionHost) Missing() *notAFunctionHost { return nil }

// anyMethodProxy answers every method with its name, as the query builder's proxy answers its verbs.
type anyMethodProxy struct{}

func (anyMethodProxy) OnGet(key string) value.Value              { return value.Value{K: value.Nil} }
func (anyMethodProxy) OnCompare(string, value.Value) value.Value { return value.ToBool(false) }
func (anyMethodProxy) OnInvoke(method string, _ ...value.Value) value.Value {
	return value.NewString(method)
}

// Calling a value that is not a function is a runtime error (14/10/2026, "vâng làm cả hai đi"), as in
// JavaScript ("x is not a function"). The message names what was called as the source wrote it and says
// what kind of value it is; where the call starts is the error's at: inside safe(() => …) it is the
// error (message, code NOT_A_FUNCTION, at). Until that day the call was silent: the value itself after a
// dot ({ x: 3 }.x() was 3), null otherwise, and a bare log(…) named nothing and did nothing. Neither a
// string's text nor a number is shown (a number since 14/10/2026, the safe run), so a secret called by
// mistake stays out of the message; the message holds no place either, at does.
func TestCallingAValueThatIsNotAFunctionIsARuntimeError(t *testing.T) {
	host := map[string]value.Value{"host": value.New(&notAFunctionHost{Count: 2})}
	cases := []struct {
		setup, call, want string
		// place is where the call starts in call (its callee); at, when set, is the whole place instead
		place int
		at    string
	}{
		{"const o = { x: 31337 }", `o.x("k")`, `o.x không phải hàm (là số): không gọi được`, 0, ""},
		{"const o = { x: 3 }", `o["x"]("k")`, `o["x"] không phải hàm (là số): không gọi được`, 0, ""},
		{"const k = \"x\"\nconst o = { x: 3 }", `o[k]()`, `o[k] không phải hàm (là số): không gọi được`, 0, ""},
		{"const items = [1]", `items[0]()`, `items[0] không phải hàm (là số): không gọi được`, 0, ""},
		{"const f = \"log\"", `f("sk-secret")`, `f không phải hàm (là chuỗi): không gọi được`, 0, ""},
		{"", `nothing(1)`, `nothing không phải hàm (là null): không gọi được`, 0, ""},
		{"", `log("x")`, `log không phải hàm (là null): không gọi được`, 0, ""},
		{"", `print("x")`, `print không phải hàm (là null): không gọi được`, 0, ""},
		{"", `nothing(() => 1, 2)`, `nothing không phải hàm (là null): không gọi được`, 0, ""},
		{"const o = { a: { b: { flag: true } } }", `o.a.b.flag()`, `o.a.b.flag không phải hàm (là boolean true): không gọi được`, 0, ""},
		{"const o = { f: () => ({ n: 1 }) }", `o.f().n()`, `o.f().n không phải hàm (là số): không gọi được`, 0, ""},
		{"", `"abc".nope()`, `"abc".nope không phải hàm (là null): không gọi được`, 0, ""},
		// an array literal's place is its opening bracket (14/10/2026; it was the closing one)
		{"", `[1, 2].nope()`, `[…].nope không phải hàm (là null): không gọi được`, 0, ""},
		{"", `({ x: 1 }).x()`, `{…}.x không phải hàm (là số): không gọi được`, 1, ""},
		{"", `(3)(1)`, `3 không phải hàm (là số): không gọi được`, 1, ""},
		{"const a = [1]", `a()`, `a không phải hàm (là mảng): không gọi được`, 0, ""},
		{"const o = {}", `o()`, `o không phải hàm (là object): không gọi được`, 0, ""},
		// the failure is inside run: at is there, the innermost frame, not at the call of run
		{"const o = { x: 3 }\nconst run = () => o.x()", `run()`, `o.x không phải hàm (là số): không gọi được`, 0, "<source>:2:19"},
		{"", `host.count()`, `host.count không phải hàm (là số): không gọi được`, 0, ""},
		{"", `host.unexported()`, `host.unexported không phải hàm (là null): không gọi được`, 0, ""},
		{"", `host()`, `host không phải hàm (là object của engine): không gọi được`, 0, ""},
		// a value a branch chose has no single name: the call is named by what it is, at where its
		// callee starts (c), not its last argument
		{"const f = 1\nconst g = 2\nconst c = true", `(c ? f : g)(1)`, `giá trị được gọi không phải hàm (là số): không gọi được`, 1, ""},
	}
	for _, c := range cases {
		message, code, at := runNotAFunction(t, c.setup, c.call, host)
		want := c.at
		if want == "" {
			line := 1
			if c.setup != "" {
				line = strings.Count(c.setup, "\n") + 2
			}
			want = "<source>:" + strconv.Itoa(line) + ":" + strconv.Itoa(len(safeCallPrefix)+c.place+1)
		}
		if message != c.want || code != value.NotAFunctionCode || at != want {
			t.Errorf("%q:\n got message=%q code=%q at=%q\nwant message=%q code=%q at=%q", c.call, message, code, at, c.want, value.NotAFunctionCode, want)
		}
		if strings.Contains(message, "sk-secret") || strings.Contains(message, "31337") {
			t.Errorf("%q: the message shows a value: %q", c.call, message)
		}
	}
}

// Outside safe() the error ends the run like any other runtime error of the VM: a structured
// RUNTIME_ERROR whose cause is NOT_A_FUNCTION, at the place its printed text leads with, and nothing
// after the call runs.
func TestNotAFunctionEndsTheRunLikeEveryRuntimeError(t *testing.T) {
	source := "const o = { x: 3 }\nconst before = 1\n  o.x(\"k\")\nconst after = 2"
	bytecode, err := CompileSource(source)
	if err != nil {
		t.Fatal(err)
	}
	vm := runtime.New(bytecode.Program)
	returned := vm.Run()
	diagnostic, ok := runtime.DiagnosticFrom(returned)
	if !returned.IsInvalid() || !ok {
		t.Fatalf("run = %s, want a runtime error", returned.Text())
	}
	want := "o.x không phải hàm (là số): không gọi được"
	if diagnostic.Code != runtime.DiagnosticRuntimeError || diagnostic.CauseCode != value.NotAFunctionCode || diagnostic.Message != want {
		t.Fatalf("diagnostic = %s/%s %q, want %s/%s %q", diagnostic.Code, diagnostic.CauseCode, diagnostic.Message, runtime.DiagnosticRuntimeError, value.NotAFunctionCode, want)
	}
	if !strings.HasPrefix(returned.Text(), "<source>:3:3: "+want) {
		t.Fatalf("printed = %q, want it led by <source>:3:3", returned.Text())
	}
	if diagnostic.Line != 3 || diagnostic.Column != 3 {
		t.Fatalf("diagnostic at %d:%d, want 3:3 (where the message says)", diagnostic.Line, diagnostic.Column)
	}
	if _, ran := vm.Vars["after"]; ran {
		t.Fatal("the run went on after the failed call")
	}
	if before := vm.Vars["before"]; before.Int() != 1 {
		t.Fatalf("before = %s, want 1: the run failed before reaching the call", before.Text())
	}
}

// What IS a function is called as before: a function an object owns (after a dot and through a
// bracket), a builtin method (data with the same name included), a Go function, a Go method (a getter
// without arguments too), a proxy that answers any name, a host function key (new Date().getTime()),
// an array's callback method. A method of null is null, as every member of null is: the receiver
// chain never fails.
func TestWhatIsAFunctionIsStillCalled(t *testing.T) {
	globals := map[string]value.Value{}
	builtins.InjectJSCompat(globals)
	globals["host"] = value.New(&notAFunctionHost{Count: 2})
	globals["proxy"] = value.Value{K: value.Proxy, V: anyMethodProxy{}}
	source := `
const o = { add: (x) => x + 1, pick: 3, a: 1 }
const n = null
const result = [
  o.add(1), o["add"](2), o.pick("a"), parseInt("42"), host.name(), host.greet("Quốc"), host.join("a", "b"),
  host.values().a, proxy.anything(1), new Date(0).getTime(), [1, 2].map((x) => x * 2), "ab".toUpperCase(),
  n.trim(), n.a.b(), safe(() => o.add(1)).value, host.missing()
]`
	got := runOwnKey(t, source, globals)
	want := `[2,3,{"a":1},42,"host","chào Quốc","ab",1,"anything",0,[2,4],"AB",null,null,2,null]`
	if got != want {
		t.Fatalf("calls of functions = %s\nwant %s", got, want)
	}
}
