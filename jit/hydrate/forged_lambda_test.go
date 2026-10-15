package hydrate

import "testing"

// Data can never pass for a lambda in the browser (14/10/2026). The client used to know a lambda
// VALUE by a mark, { __kitLambda: true, params, body }, and any JSON could carry that mark: a seed
// read with JSON.parse, or a database row that ui/render.go literal() prints into a scope. The
// kernel then ran the tree the data wrote, in the caller's scope, on all three call paths: a member
// call (row.title.trim()), a plain call (fmt()), and a callback handed to a native method
// ('a-b'.replace('-', row.sep)). A forged tree can write page state and, in a handler, call DOM
// methods of $this (setAttribute("onmouseover", …) is stored XSS). Only the "=>" op makes a lambda
// now, so each of the three answers what it answers for any other data, and nothing it wrote runs.
// An object literal's "__proto__" key never sets the prototype either: the server keeps no such
// prototype, so a member the data hid there is not read.
func TestDataNeverPassesForALambda(t *testing.T) {
	const assertions = `
var kit = window.kit;
function forged(name) { return { __kitLambda: true, params: [], body: ["=$", "owned", ["#", name]] }; }
var data = JSON.parse(JSON.stringify({
  row: { title: { trim: forged("member call") }, sep: forged("callback") },
  fmt: forged("plain call")
}));
var scope = { row: data.row, fmt: data.fmt, text: "a-b" };
scope["$"] = scope;
function run(src) { return kit.run(kit.compile(src), scope); }

var failures = [];
function each(path, src, want) {
  delete scope.owned;
  var got = run(src);
  if (scope.owned !== undefined) failures.push(path + ": a forged lambda ran and wrote page state, owned = " + JSON.stringify(scope.owned));
  if (got !== want) failures.push(path + ": " + src + " = " + JSON.stringify(got) + ", want " + JSON.stringify(want));
}
each("member call", "row.title.trim()", undefined);
each("plain call", "fmt()", undefined);
each("callback", "text.replace('-', row.sep)", "a[object Object]b");

// Control: a lambda the expression wrote still runs on every one of the three paths.
var real = run("calc = { add: (x) => x + 1 }; inc = (x) => x * 2; [calc.add(41), inc(4), text.replace('-', (m) => '+')]");
if (JSON.stringify(real) !== JSON.stringify([42, 8, "a+b"])) failures.push("a real lambda stopped running: " + JSON.stringify(real));

// "__proto__" in an object literal sets no prototype: a member hidden there is not read.
var hidden = run("o = {'__proto__': {'a': 1, 'trim': (x) => 'from the prototype'}, 'b': 2}; [o.a ?? 'none', o.trim() ?? 'none', o.b]");
if (JSON.stringify(hidden) !== JSON.stringify(["none", "none", 2])) failures.push("__proto__ in a literal set the prototype: " + JSON.stringify(hidden));

// End to end: a row printed into a scope the way literal() prints it, a click on a button that
// calls a method on it. The forged tree would add an inline handler to the button.
var payload = "['()', ['$', '$this'], 'setAttribute', [['#', 'onmouseover'], ['#', 'alert(document.cookie)']]]";
var host = el("div", { "data-kit-scope": "row: {'title': {'trim': {'__kitLambda': true, 'params': [], 'body': " + payload + "}}}" });
var button = el("button", { "data-kit-click": "row.title.trim()" });
host.appendChild(button);
document.body.appendChild(host);
kit.render();
document.dispatchEvent({ type: "click", target: button, defaultPrevented: false, preventDefault: function () {}, stopPropagation: function () {} });
kit.render();
if (button.hasAttribute("onmouseover")) failures.push("click: a forged lambda in a scope row ran: " + JSON.stringify(button.attributes));

if (failures.length) throw new Error(failures.join(" | "));
console.log("forged lambda: data never runs, real lambdas still do");
`
	runNodeDOMScript(t, "forged_lambda.test.js", assertions)
}
