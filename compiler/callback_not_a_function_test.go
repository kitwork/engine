package compiler

import (
	"strconv"
	"testing"

	"github.com/kitwork/engine/builtins"
	"github.com/kitwork/engine/value"
)

// An array method that takes a callback (map, filter, forEach…) calls a function of the engine as it
// calls a script lambda, as JavaScript does: [1, 0, 2].some(Boolean), items.forEach(console.log). A
// callback that is not a function is the not-a-function error (14/10/2026), and the message names the
// argument, not the method, which the array has. Until this fix forEach, some, every, reduce and
// findIndex blamed the method ("[…].forEach không phải hàm (là null)") whatever the argument was, a
// function of the engine included, while map, filter, find, group, groupBy and sortBy gave null and
// sort(3) ignored its argument, all without a word. sort() and unique() without a callback keep their
// own order and key.
func TestAnArrayMethodCallsAFunctionAndRefusesAnythingElse(t *testing.T) {
	refused := []struct {
		setup, call, want string
	}{
		{"const items = [1, 2]", `items.forEach(3)`, `items.forEach(…): đối số 1 không phải hàm (là số): không gọi được`},
		{"const items = [1, 2]", `items.some(3)`, `items.some(…): đối số 1 không phải hàm (là số): không gọi được`},
		{"const items = [1, 2]", `items.every(3)`, `items.every(…): đối số 1 không phải hàm (là số): không gọi được`},
		{"const items = [1, 2]", `items.reduce(3, 0)`, `items.reduce(…): đối số 1 không phải hàm (là số): không gọi được`},
		{"const items = [1, 2]", `items.findIndex(3)`, `items.findIndex(…): đối số 1 không phải hàm (là số): không gọi được`},
		{"const items = [1, 2]", `items.map(3)`, `items.map(…): đối số 1 không phải hàm (là số): không gọi được`},
		{"const items = [1, 2]", `items.filter(null)`, `items.filter(…): đối số 1 không phải hàm (là null): không gọi được`},
		{"const items = [1, 2]", `items.find("log")`, `items.find(…): đối số 1 không phải hàm (là chuỗi): không gọi được`},
		{"const items = [3, 1, 2]", `items.sort(3)`, `items.sort(…): đối số 1 không phải hàm (là số): không gọi được`},
		{"const items = [1, 2]", `items.group({})`, `items.group(…): đối số 1 không phải hàm (là object): không gọi được`},
		{"const items = [1, 2]", `items.groupBy(3)`, `items.groupBy(…): đối số 1 không phải hàm (là số): không gọi được`},
		{"const items = [1, 2]", `items.sortBy([])`, `items.sortBy(…): đối số 1 không phải hàm (là mảng): không gọi được`},
		{"const items = [1, 1]", `items.unique("id")`, `items.unique(…): đối số 1 không phải hàm (là chuỗi): không gọi được`},
		{"const items = [1, 2]", `items.forEach()`, `items.forEach(): đối số 1 không phải hàm (là null): không gọi được`},
		{"const items = [1, 2]", `items.forEach("PRINT")`, `items.forEach(…): đối số 1 không phải hàm (là chuỗi): không gọi được`},
	}
	wantAt := "<source>:2:" + strconv.Itoa(len(safeCallPrefix)+1)
	for _, c := range refused {
		message, code, at := runNotAFunction(t, c.setup, c.call, nil)
		if message != c.want || code != value.NotAFunctionCode || at != wantAt {
			t.Errorf("%q:\n got message=%q code=%q at=%q\nwant message=%q code=%q at=%q", c.call, message, code, at, c.want, value.NotAFunctionCode, wantAt)
		}
	}

	var calls []string
	globals := map[string]value.Value{}
	builtins.InjectJSCompat(globals)
	globals["record"] = value.NewFunc(func(args ...value.Value) value.Value {
		line := ""
		for _, arg := range args {
			line += arg.Text() + " "
		}
		calls = append(calls, line)
		return value.Value{K: value.Nil}
	})
	globals["add"] = value.NewFunc(func(args ...value.Value) value.Value { return value.New(args[0].N + args[1].N) })
	globals["descending"] = value.NewFunc(func(args ...value.Value) value.Value { return value.New(args[1].N - args[0].N) })
	source := `
const items = [1, 2]
const each = items.forEach(record)
const result = [
  [1, 0, 2].some(Boolean), [0, 0].some(Boolean), [1, 2].every(Boolean), ["1", "2"].map(Number),
  [0, 1, 2].filter(Boolean), [0, 3].find(Boolean), [0, 3].findIndex(Boolean), [1, 2, 3].reduce(add, 0),
  [1, 3, 2].sort(descending), [3, 1, 2].sort(), [3, 1, 2].sort(null), [1, 1, 2].unique(), [1, 2].map((x) => x * 2), each
]`
	got := runOwnKey(t, source, globals)
	want := `[true,false,true,[1,2],[1,2],3,1,6,[3,2,1],[1,2,3],[1,2,3],[1,2],[2,4],null]`
	if got != want {
		t.Fatalf("array methods with a function of the engine = %s\nwant %s", got, want)
	}
	if len(calls) != 2 || calls[0] != "1 0 " || calls[1] != "2 1 " {
		t.Fatalf("forEach(record) called %q, want each element with its index", calls)
	}
}
