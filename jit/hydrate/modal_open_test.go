package hydrate

import "testing"

// This is the test the DEMO earned — it composes data-kit-if + data-kit-away + a component method
// exactly as a real "open a modal" flow does, and pins the TWO bugs the node shim previously masked:
//
//  1. SCOPE via a comment anchor: renderIf resolves its condition with scopeFor(anchor), and an
//     anchor is a comment (no .closest()). Before the fix scopeFor fell back to the PAGE scope, so a
//     component-scoped condition (`adding`) read as missing → the modal never opened. (The realistic
//     shim — comments have no .closest — is what surfaces this.)
//  2. The opening click's own away: the click that runs open() mounts the modal (which carries
//     data-kit-away); the SAME click then reaches the away listener, and the trigger is "outside" the
//     just-mounted panel — so the modal closed itself instantly. markFresh/isFresh fix that.
//
// A later, separate outside click must still close it (away is not broken, just click-scoped).
func TestModalOpensAndStaysOnClick(t *testing.T) {
	const assertions = `
var kit = window.kit;

var app = el("div", { "data-kit-component": "modaldemo" });
var trigger = el("button", { "data-kit-click": "open()" });
app.appendChild(trigger);
var modal = el("div", { "data-kit-if": "adding" });
var panel = el("div", { "data-kit-away": "adding = false", "data-kit-escape": "adding = false" });
panel.appendChild(el("b", { "data-kit-text": "'panel'" }));
modal.appendChild(panel);
app.appendChild(modal);
document.body.appendChild(app);

kit.component("modaldemo", { adding: false, open: function () { this.adding = true; } });
kit.render();

function mounted() { return document.querySelectorAll("[data-kit-away]").length > 0; }

if (mounted()) throw new Error("initial: modal must be closed");

// The opening click: runs open() → mounts the modal, and must NOT be closed by its own away check.
document.dispatchEvent({ type: "click", target: trigger });
// The modal must be MOUNTED and STAYING after the click. It fails to mount if bug 1 (if-anchor scope
// fell to the page scope, so adding read missing); it mounts then self-closes if bug 2 (the opening
// click's own data-kit-away fired). Either regression leaves the modal absent here.
if (!mounted()) throw new Error("modal absent after the opening click (bug 1 scope, or bug 2 self-close)");
if (kit.scopeFor(app).adding !== true) throw new Error("adding should be true after open()");

// A genuinely separate later outside click DOES close it (away still works). freshMounts clears on a
// microtask, so a macrotask (setTimeout) is safely after the opening click's whole dispatch.
setTimeout(function () {
  document.dispatchEvent({ type: "click", target: document.body });
  if (mounted()) throw new Error("away: a separate outside click should close the modal");
  console.log("modal open flow: if(component scope) mounts on click, survives its own away, closes on a later outside click");
}, 0);
`
	runNodeDOMScript(t, "modal_open.test.js", assertions)
}
