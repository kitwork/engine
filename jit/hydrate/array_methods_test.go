package hydrate

import (
	"errors"
	"testing"
)

// Array methods with a callback came into the grammar on 29/09, the list the standalone KitJS 1.0.0
// always had: join includes indexOf slice map filter find some every. Before that the kernel PARSED
// items.filter((x) => x.done) and then threw "object is not a function" — a lambda is a compiled
// tree, not a function — while the Go twin had no array methods at all and answered null. What
// each engine returns is pinned by the shared corpus (array-*). This file pins the one property the
// corpus cannot: a callback does not buy its way out of the evaluation budget.

func bigList(n int) []any {
	out := make([]any, n)
	for i := range out {
		out[i] = float64(i)
	}
	return out
}

// The server: a callback over 20,000 items spends the expression's budget, it does not get its own.
func TestServerCallbackCountsAgainstTheBudget(t *testing.T) {
	ir, err := Compile("big.filter((x) => x > 0).length")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Eval(ir, map[string]any{"big": bigList(20000)}); !errors.Is(err, errBudget) {
		t.Fatalf("a callback over 20,000 items finished inside the budget (err=%v) — it escaped the limit", err)
	}
	// CONTROL: the same expression over a short list is fine, so the refusal above is about size.
	v, err := Eval(ir, map[string]any{"big": bigList(10)})
	if err != nil || v != float64(9) {
		t.Fatalf("a short list should filter normally, got %v, %v", v, err)
	}
}

// The kernel: the callback's steps run inside the same run(), so they draw on the same 10,000.
func TestKernelCallbackCountsAgainstTheBudget(t *testing.T) {
	runNodeDOMScript(t, "array_budget.test.js", `
var kit = window.kit;
var big = []; for (var i = 0; i < 20000; i++) big.push(i);
var ir = kit.compile("big.filter((x) => x > 0).length");
var refused = false;
try { kit.run(ir, { big: big }); } catch (e) { refused = /budget/.test(e.message); }
if (!refused) throw new Error("a callback over 20,000 items finished inside the budget — it escaped the limit");

// CONTROL: a short list filters normally.
if (kit.run(ir, { big: [0, 1, 2, 3] }) !== 3) throw new Error("a short list should filter normally");
console.log("a callback spends the expression's budget, it does not get its own");
`)
}
