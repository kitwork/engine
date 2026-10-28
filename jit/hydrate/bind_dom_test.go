package hydrate

import "testing"

// The pairs of data-kitwork-bind in the kernel: the three groups of ideaship-final §5, driven
// through the node DOM shim. A reflected boolean writes property + attribute; a live property
// writes the property only; aria-* is spelled out; a data-* boolean is present or absent. (The
// checkbox's value is an expression, not a bare name: `checked: open` on a field would be the
// two-way form, which grouped_dom_test.go covers, and would not reach the live-property write.)
func TestKernelBindingGroups(t *testing.T) {
	runNodeDOMScript(t, "bind_groups.js", `
var kitwork = window.kitwork;
var section = el("section", { "data-kitwork-component": "panel" });
var button = el("button", { "data-kitwork-bind": "disabled: busy; aria-expanded: open; data-lit: open; title: label" });
var check = el("input", { "type": "checkbox", "data-kitwork-bind": "checked: !busy" });
section.appendChild(button); section.appendChild(check);
document.body.appendChild(section);

kitwork.component("panel", { open: true, busy: false, label: "Save", flip: function () { this.open = !this.open; this.busy = !this.busy; } });
kitwork.render();

if (button.disabled !== false || button.hasAttribute("disabled")) throw new Error("disabled=false: property/attribute " + button.disabled + "/" + button.hasAttribute("disabled"));
if (button.getAttribute("aria-expanded") !== "true") throw new Error("aria-expanded should be the word true, got " + button.getAttribute("aria-expanded"));
if (button.getAttribute("data-lit") !== "") throw new Error("a true data-* binding is a bare attribute");
if (button.getAttribute("title") !== "Save") throw new Error("title should be the string, got " + button.getAttribute("title"));
if (check.checked !== true || check.hasAttribute("checked")) throw new Error("checked is a live property, not an attribute: " + check.checked + "/" + check.hasAttribute("checked"));

kitwork.scopeFor(section).flip();
kitwork.render();
if (button.disabled !== true || !button.hasAttribute("disabled")) throw new Error("disabled=true should set both");
if (button.getAttribute("aria-expanded") !== "false") throw new Error("aria-expanded should flip to the word false");
if (button.hasAttribute("data-lit")) throw new Error("a false data-* binding is removed");
if (check.checked !== false || check.hasAttribute("checked")) throw new Error("checked=false clears the property only");
console.log("kernel binding groups ok");
`)
}
