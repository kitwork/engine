package compiler

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/kitwork/engine/builtins"
	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
)

// runCapturingStdout runs source in a fresh VM with the JavaScript globals (console, Math…) and gives
// what it wrote to the process's standard output, and the `result` it left.
func runCapturingStdout(t *testing.T, source string) (string, value.Value) {
	t.Helper()
	bytecode, err := CompileSource(source)
	if err != nil {
		t.Fatalf("compile %q: %v", source, err)
	}
	vm := runtime.New(bytecode.Program)
	builtins.InjectJSCompat(vm.Globals)

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	captured := make(chan string)
	go func() {
		data, _ := io.ReadAll(reader)
		captured <- string(data)
	}()
	saved := os.Stdout
	os.Stdout = writer
	returned := vm.Run()
	os.Stdout = saved
	writer.Close()
	output := <-captured
	reader.Close()
	if returned.IsInvalid() {
		t.Fatalf("run %q: %s", source, returned.Text())
	}
	return output, vm.Vars["result"]
}

// Calling a STRING never prints anything (14/10/2026). Until then the VM printed the arguments of a call
// to the server's standard output whenever the value called was the string "log" or "PRINT" — a branch
// left from the first VM, reached through a variable or a bracket, so `body["x"](secret)` with body.x =
// "log" (data a request can carry) wrote the secret into the server's log. The language's real logging
// never went through it: console.log is a native function, `log.print` a method of the log capability,
// and a bare log(…) or print(…) names nothing. CONTROL: console.log does reach the captured output, so
// an empty capture means the call printed nothing, not that nothing was captured.
func TestCallingAStringNeverPrints(t *testing.T) {
	const secret = "sk-live-4f2a-never-in-the-log"
	output, result := runCapturingStdout(t, `
const body = { x: "log", y: "PRINT" };
const secret = "`+secret+`";
const f = "log";
const g = "PRINT";
const result = [
  safe(() => body["x"](secret)),
  safe(() => body["y"](secret)),
  safe(() => f(secret)),
  safe(() => g(secret)),
  safe(() => "log"(secret))
];
console.log("control-marker");
`)
	if !strings.Contains(output, "[console.log] control-marker") {
		t.Fatalf("CONTROL: console.log did not reach the captured output %q", output)
	}
	if strings.Contains(output, secret) {
		t.Fatalf("calling a string printed its argument to standard output: %q", output)
	}
	if strings.TrimSpace(output) != "[console.log] control-marker" {
		t.Fatalf("calling a string printed something: %q", output)
	}
	if result.K != value.Array || result.Len() != 5 {
		t.Fatalf("the five calls did not all return: %s", result.Text())
	}
}
