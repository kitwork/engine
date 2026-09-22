package hydrate

import "testing"

// data-kit-element + `$element` in the kernel (ideaship-final §6): a ref belongs to the boundary that owns
// it, so `$element.field` inside a scope is that scope's field, on the page it is the page's, a nested
// boundary's ref is invisible from outside and an outer one invisible from inside, and a missing
// name is nullish. The shim has no focus(), so the proof reads through the element.
func TestKernelNamedElementsAreScopedToTheActingBoundary(t *testing.T) {
	runNodeDOMScript(t, "element.js", `
var kit = window.kit;
var pageField = el("input", { "data-kit-element": "field", "value": "page" });
var pageRead = el("button", { "data-kit-click": "hit = $element.field.getAttribute('value')" });
var pageOut = el("output", { "data-kit-text": "hit" });
document.body.appendChild(pageField); document.body.appendChild(pageRead); document.body.appendChild(pageOut);

var outer = el("section", { "data-kit-scope": "found: '', missing: 'unset'" });
var outerField = el("input", { "data-kit-element": "field", "value": "outer" });
var outerRead = el("button", { "data-kit-click": "found = $element.field.getAttribute('value'); missing = $element.nothing == null ? 'null' : 'element'" });
var outerInner = el("button", { "data-kit-click": "missing = $element.inner == null ? 'null' : 'element'" });
var found = el("output", { "data-kit-text": "found" });
var missing = el("output", { "data-kit-text": "missing" });
var nested = el("div", { "data-kit-scope": "seen: 'unset'" });
var innerField = el("input", { "data-kit-element": "inner", "value": "inner" });
var innerOuter = el("button", { "data-kit-click": "seen = $element.field == null ? 'null' : 'element'" });
var innerOwn = el("button", { "data-kit-click": "seen = $element.inner.getAttribute('value')" });
var seen = el("output", { "data-kit-text": "seen" });
[outerField, outerRead, outerInner, found, missing, nested].forEach(function (n) { outer.appendChild(n); });
[innerField, innerOuter, innerOwn, seen].forEach(function (n) { nested.appendChild(n); });
document.body.appendChild(outer);
kit.render();

function click(target) { document.dispatchEvent({ type: "click", target: target, defaultPrevented: false, preventDefault: function () {}, stopPropagation: function () {} }); kit.render(); }

click(outerRead);
if (found.textContent !== "outer") throw new Error("$element.field inside the scope should be the scope's own field, got " + JSON.stringify(found.textContent));
if (missing.textContent !== "null") throw new Error("a missing name should be nullish, got " + missing.textContent);
click(pageRead);
if (pageOut.textContent !== "page") throw new Error("$element.field on the page should be the page-level field, got " + JSON.stringify(pageOut.textContent));
click(outerInner);
if (missing.textContent !== "null") throw new Error("a nested boundary's ref must not be visible from the outer boundary");
click(innerOuter);
if (seen.textContent !== "null") throw new Error("an outer ref must not be visible from inside a nested boundary");
click(innerOwn);
if (seen.textContent !== "inner") throw new Error("a nested boundary reaches its own ref, got " + JSON.stringify(seen.textContent));
console.log("kernel $element ok");
`)
}
