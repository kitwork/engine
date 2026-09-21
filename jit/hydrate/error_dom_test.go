package hydrate

import "testing"

// data-kit-error is an error boundary (ideaship-final §2): the nearest ancestor carrying it runs
// its expression with $error — cause, message, directive, element — when an action or binding
// inside it fails; a failing element no longer aborts the rest of the pass; the error does not
// travel past the first boundary; a boundary whose own handler throws is not re-entered.
func TestKernelErrorBoundary(t *testing.T) {
	runNodeDOMScript(t, "error_boundary.js", `
var kit = window.kit;
var logged = [];
var originalError = console.error;
console.error = function (e) { logged.push(String(e && e.message || e)); };

// The kernel's walker is forgiving — a missing name reads as 0, a call on a non-function is
// undefined — so what a boundary catches is what real component code throws.
kit.component("boom", { caught: "", where: "", on: "", after: "unset", phase: "boot",
  explode: function () { throw new Error("kaboom in a binding"); },
  kaboom: function () { throw new Error("kaboom in an action"); } });
kit.component("innerboom", { inner: "", nope: function () { throw new Error("nope inside"); } });
kit.component("throwingboom", { seen: 0, gone: function () { throw new Error("gone"); }, alsoBroken: function () { throw new Error("alsoBroken"); } });
kit.component("freeboom", { nowhere: function () { throw new Error("nowhere"); } });
var outer = el("section", { "data-kit-component": "boom", "data-kit-error": "caught = $error.message; where = $error.directive; on = $error.element.getAttribute('id'); phase = 'caught'" });
var badBinding = el("output", { "id": "bad-binding", "data-kit-text": "phase === 'boot' ? explode() : 'fine'" });
var afterBinding = el("output", { "id": "after", "data-kit-text": "after" });
var badAction = el("button", { "id": "bad-action", "data-kit-click": "kaboom()" });
var nested = el("div", { "data-kit-component": "innerboom", "data-kit-error": "inner = $error.message" });
var nestedBad = el("button", { "id": "nested-bad", "data-kit-click": "nope()" });
var innerOut = el("output", { "data-kit-text": "inner" });
nested.appendChild(nestedBad); nested.appendChild(innerOut);
var throwing = el("div", { "data-kit-component": "throwingboom", "data-kit-error": "seen = seen + 1; alsoBroken()" });
var throwingBad = el("button", { "id": "throwing-bad", "data-kit-click": "gone()" });
throwing.appendChild(throwingBad);
[badBinding, afterBinding, badAction, nested, throwing].forEach(function (n) { outer.appendChild(n); });
document.body.appendChild(outer);
var freeHost = el("div", { "data-kit-component": "freeboom" });
var free = el("button", { "id": "free-bad", "data-kit-click": "nowhere()" });
freeHost.appendChild(free);
document.body.appendChild(freeHost);
kit.render();

function click(target) { document.dispatchEvent({ type: "click", target: target, defaultPrevented: false, preventDefault: function () {}, stopPropagation: function () {} }); kit.render(); }
var scope = kit.scopeFor(outer);

// A failing binding reaches the boundary, and the binding after it still renders.
if (scope.where !== "data-kit-text" || scope.on !== "bad-binding") throw new Error("a failing binding should reach the boundary with its directive and element, got " + scope.where + "/" + scope.on);
if (afterBinding.textContent !== "unset") throw new Error("a failing binding must not abort the rest of the pass, after = " + JSON.stringify(afterBinding.textContent));
kit.render();
if (badBinding.textContent !== "fine") throw new Error("after the boundary changed the state the binding renders again, got " + JSON.stringify(badBinding.textContent));

click(badAction);
if (scope.where !== "data-kit-click" || scope.on !== "bad-action") throw new Error("a failing action should reach the boundary, got " + scope.where + "/" + scope.on);
if (!/kaboom in an action/.test(scope.caught)) throw new Error("$error.message should carry the cause, got " + scope.caught);

var outerBefore = scope.caught;
click(nestedBad);
if (!/nope/.test(kit.scopeFor(nested).inner)) throw new Error("the nearest boundary should catch, inner = " + kit.scopeFor(nested).inner);
if (scope.caught !== outerBefore) throw new Error("an error must not travel past the first boundary");

var loggedBefore = logged.length;
click(throwingBad);
if (kit.scopeFor(throwing).seen !== 1) throw new Error("a boundary whose handler throws runs once, seen = " + kit.scopeFor(throwing).seen);
if (!logged.slice(loggedBefore).some(function (m) { return /alsoBroken/.test(m); })) throw new Error("the handler's own failure should reach the console, logged: " + logged.slice(loggedBefore).join(" | "));
if (scope.caught !== outerBefore) throw new Error("a failing handler must not hand its error to the outer boundary");

loggedBefore = logged.length;
click(free);
if (!logged.slice(loggedBefore).some(function (m) { return /nowhere/.test(m); })) throw new Error("an error with no boundary above it still reaches the console");
console.error = originalError;
console.log("kernel error boundary ok");
`)
}
