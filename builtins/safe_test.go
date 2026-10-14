package builtins_test

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/kitwork/engine/value"
)

// safe is a global like fail and JSON (14/10/2026), and gives a plain object.
func TestSafeIsAGlobal(t *testing.T) {
	returned, vars := run(t, "const r = safe(() => 1 + 2)\nconst s = safe(() => JSON.parse(\"{bad\"))\nconst result = [r.value, r.error, s.value, s.error.code]")
	if returned.IsInvalid() {
		t.Fatal(returned.Text())
	}
	if got := vars["result"].Text(); got != "[3, null, null, INVALID_INPUT]" {
		t.Fatalf("result = %s", got)
	}
}

// fail is always FAIL: an application never sets a code of its own. The global Error, another name
// for fail that stopped the program where JavaScript makes a value, is gone.
func TestFailIsAlwaysFailAndErrorIsGone(t *testing.T) {
	_, vars := run(t, "const a = safe(() => fail(\"hết hàng\"))\nconst b = safe(() => fail(\"hết hàng\", \"OUT_OF_STOCK\"))\nconst result = [a.error.code, a.error.message, b.error.code, b.error.message]")
	if got := vars["result"].Text(); got != "[FAIL, hết hàng, FAIL, hết hàng]" {
		t.Fatalf("result = %s", got)
	}
	_, vars = run(t, "const e = safe(() => Error(\"x\"))\nconst result = [e.error.code, e.error.message]")
	if got := vars["result"].Text(); got != "[NOT_A_FUNCTION, Error không phải hàm (là null): không gọi được]" {
		t.Fatalf("Error(\"x\") = %s, want the NOT_A_FUNCTION error: the global is gone", got)
	}
}

// Text from outside that cannot be read is INVALID_INPUT; an operator on kinds it does not take TYPE.
func TestBuiltinFailuresCarryTheirCode(t *testing.T) {
	_, vars := run(t, `const a = safe(() => JSON.parse("{bad"))
const b = safe(() => BigInt("12x"))
const c = safe(() => BigInt(1.5))
const d = safe(() => "a" * 2)
const result = [a.error.code, b.error.code, c.error.code, d.error.code, d.error.message]`)
	if got := vars["result"].Text(); got != "[INVALID_INPUT, INVALID_INPUT, INVALID_INPUT, TYPE, phép * không dùng được giữa chuỗi và số]" {
		t.Fatalf("result = %s", got)
	}
}

// console has log, error, warn, info and debug (14/10/2026), each printing its arguments on one line led
// by its own name. Before, console.error() was the NOT_A_FUNCTION error.
func TestConsoleHasTheMethodsCodeForJavaScriptCalls(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stdout
	os.Stdout = writer
	returned, _ := run(t, `console.log("a", 1)
console.error("b", 2)
console.warn("c")
console.info("d")
console.debug("e", { k: true })`)
	os.Stdout = saved
	writer.Close()
	printed, _ := io.ReadAll(reader)
	reader.Close()
	if returned.K == value.Invalid {
		t.Fatalf("the run failed: %s", returned.Text())
	}
	want := "[console.log] a 1\n[console.error] b 2\n[console.warn] c\n[console.info] d\n[console.debug] e {k: true}\n"
	if got := strings.ReplaceAll(string(printed), "\r\n", "\n"); got != want {
		t.Fatalf("printed %q, want %q", got, want)
	}
}
