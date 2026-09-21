package hydrate

import "testing"

// data-kit-bind:<name> in the kernel: the three groups of ideaship-final §5, driven through the
// node DOM shim. A reflected boolean writes property + attribute; a live property writes the
// property only; aria-* is spelled out; a data-* boolean is present or absent.
func TestKernelBindingGroups(t *testing.T) {
	runNodeDOMScript(t, "bind_groups.js", `
var kit = window.kit;
var section = el("section", { "data-kit-component": "panel" });
var button = el("button", { "data-kit-bind:disabled": "busy", "data-kit-bind:aria-expanded": "open", "data-kit-bind:data-lit": "open", "data-kit-bind:title": "label" });
var check = el("input", { "type": "checkbox", "data-kit-bind:checked": "open" });
section.appendChild(button); section.appendChild(check);
document.body.appendChild(section);

kit.component("panel", { open: true, busy: false, label: "Save", flip: function () { this.open = !this.open; this.busy = !this.busy; } });
kit.render();

if (button.disabled !== false || button.hasAttribute("disabled")) throw new Error("disabled=false: property/attribute " + button.disabled + "/" + button.hasAttribute("disabled"));
if (button.getAttribute("aria-expanded") !== "true") throw new Error("aria-expanded should be the word true, got " + button.getAttribute("aria-expanded"));
if (button.getAttribute("data-lit") !== "") throw new Error("a true data-* binding is a bare attribute");
if (button.getAttribute("title") !== "Save") throw new Error("title should be the string, got " + button.getAttribute("title"));
if (check.checked !== true || check.hasAttribute("checked")) throw new Error("checked is a live property, not an attribute: " + check.checked + "/" + check.hasAttribute("checked"));

kit.scopeFor(section).flip();
kit.render();
if (button.disabled !== true || !button.hasAttribute("disabled")) throw new Error("disabled=true should set both");
if (button.getAttribute("aria-expanded") !== "false") throw new Error("aria-expanded should flip to the word false");
if (button.hasAttribute("data-lit")) throw new Error("a false data-* binding is removed");
if (check.checked !== false || check.hasAttribute("checked")) throw new Error("checked=false clears the property only");
console.log("kernel binding groups ok");
`)
}
