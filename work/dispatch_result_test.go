package work

import (
	"encoding/json"
	"testing"

	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
)

// queue.dispatch answers with a plain object { queued, id, error? }: the engine attaches no error to
// it, the message is an ordinary key. So the idiom queue.md documents,
// if (!sent.queued && sent.error) return ctx.status(503).json({ message: sent.error }),
// reads the message, because on a plain object an own error is data (13/10/2026, "sửa: khoá riêng
// thắng"). Before that day sent.error read null there and the refusal was never reported.
func TestDispatchRefusalReadsItsErrorInTheVM(t *testing.T) {
	bc, err := compiler.CompileSource(`const result = [!sent.queued && sent.error, sent.error, accepted.error || "none", accepted.id]`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	vm := runtime.New(bc.Program)
	vm.Globals = map[string]value.Value{
		"sent":     dispatchResult(0, false, "queue full"),
		"accepted": dispatchResult(7, true, ""), // control: a dispatch that went through has no error
	}
	if returned := vm.Run(); returned.IsInvalid() {
		t.Fatalf("run: %s", returned.Text())
	}
	data, err := json.Marshal(vm.Vars["result"].Interface())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(data), `["queue full","queue full","none",7]`; got != want {
		t.Fatalf("reading a dispatch result = %s, want %s", got, want)
	}
}
