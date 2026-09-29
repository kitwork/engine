package hydrate

import (
	"strings"
	"testing"
)

// What each global returns is pinned by the shared corpus (global-*). This file pins what the
// corpus cannot: that exposing them opened no door, and that they resolve inside real directives —
// the corpus runs through kit.run, which is a different scope path from a boundary's.

// A constructor reached through a global must not become a way to Function — the one object that
// would turn a string into code. Every route is refused by the member/index blocklist that was
// already there; this test makes sure the globals did not add a route around it.
func TestKernelGlobalsOpenNoWayToFunction(t *testing.T) {
	runNodeDOMScript(t, "globals_sandbox.test.js", `
var kit = window.kit;
function run(src) { return kit.run(kit.compile(src)); }
var escapes = [
  "Array.constructor", "Math.max.constructor", "Number['constructor']", "parseInt.constructor",
  "Array.prototype", "Date.prototype", "Object.defineProperty", "Object.assign", "Object.constructor",
  "JSON.parse.constructor", "String.fromCharCode.constructor"
];
var leaked = escapes.filter(function (src) { var v = run(src); return v !== undefined && v !== 0; });
if (leaked.length) throw new Error("a global leaked a route out of the sandbox: " + leaked.join(", "));

// CONTROL: the globals are really there, so the list above is about the routes, not an empty table.
if (run("Math.max(2, 9)") !== 9) throw new Error("precondition: Math should be reachable");
if (kit.run(kit.compile("Object.keys(o).length"), { o: { a: 1, b: 2 } }) !== 2) throw new Error("precondition: Object.keys should be reachable");
if (run("Array.isArray([1])") !== true) throw new Error("precondition: Array.isArray should be reachable");
console.log("the globals are reachable and open no route to Function");
`)
}

// Inside a directive, through a boundary's scope — the path a page actually takes.
func TestKernelGlobalsResolveInsideABoundary(t *testing.T) {
	runNodeDOMScript(t, "globals_directive.test.js", `
var kit = window.kit;
var box = el("div", { "data-kit-scope": "{ total: 7, size: 4, Date: 'mine' }" });
var pages = el("b", { "data-kit-text": "Math.ceil(total / size) + ' pages'" });
var own = el("i", { "data-kit-text": "Date" });
box.appendChild(pages); box.appendChild(own);
document.body.appendChild(box);
kit.render();
if (pages.textContent !== "2 pages") throw new Error("Math did not resolve inside a boundary: " + pages.textContent);
// A name the page declares wins over the global of the same name.
if (own.textContent !== "mine") throw new Error("a declared name must shadow the global: " + own.textContent);
console.log("globals resolve inside a boundary, and a declared name wins");
`)
}

// The verify pass must not call a global a name nothing declares.
func TestVerifyDoesNotReportAGlobal(t *testing.T) {
	report := strings.Join(authoringMistakes(`<html><body><div data-kit-scope="{ n: 7 }"><b data-kit-text="Math.ceil(n / 2) + parseInt('3')"></b></div></body></html>`), "\n")
	if report != "" {
		t.Errorf("a global was reported as undeclared:\n%s", report)
	}
	// CONTROL: a real undeclared name next to it is still reported.
	report = strings.Join(authoringMistakes(`<html><body><div data-kit-scope="{ n: 7 }"><b data-kit-text="Math.ceil(nn / 2)"></b></div></body></html>`), "\n")
	if !strings.Contains(report, `"nn"`) {
		t.Errorf("an undeclared name beside a global should still be reported:\n%s", report)
	}
}
