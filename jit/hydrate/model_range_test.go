package hydrate

import "testing"

// A two-way `value:` in data-kitwork-bind on a type=range (or type=number) input must bind a NUMBER,
// not a string — otherwise n + step string-concatenates ("0" + "3" = "03") instead of adding. Caught
// by the full showcase demo (a range-driven counter produced "611" instead of 6). (The server's twin
// went with the template pages' pre-render, 27/10/2026.)
func TestModelRangeCoercesToNumber(t *testing.T) {
	const assertions = `
var kit = window.kit;
var box = el("div", { "data-kitwork-scope": "n: 0; step: 1" });
var range = el("input", { "type": "range", "data-kitwork-bind": "value: step" });
var sum = el("b", { "data-kitwork-bind": "text: n + step" });
box.appendChild(range);
box.appendChild(sum);
document.body.appendChild(box);
kit.render(); // initial: step = 1

// a user drags the slider to 3 — the input carries the new value, then fires input
range.value = "3";
document.dispatchEvent({ type: "input", target: range });

var s = kit.scopeFor(box);
if (typeof s.step !== "number") throw new Error("range model must be a number, got " + typeof s.step + " (" + s.step + ")");
if (s.step !== 3) throw new Error("step should be 3, got " + s.step);

// n(0) + step(3) must render 3 (numeric add), NOT "03" (string concat)
var out = sum.textContent;
if (out !== "3") throw new Error("n + step should be 3, got " + JSON.stringify(out) + " (string concat = the bug)");

console.log("range model coerces to number: n + step = 3, not string concat");
`
	runNodeDOMScript(t, "model_range.test.js", assertions)
}
