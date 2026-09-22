package hydrate

import "testing"

// A component method that writes state later — from a timer, a callback, a .then it did not
// return — still paints: a scope write outside a pass schedules one coalesced repaint. Inside a
// pass nothing is scheduled (the click below paints once, synchronously, and the timer paints
// once more). This is what lets clipboard's `copied` reset itself two seconds later.
func TestScopeWriteOutsideAPassRepaints(t *testing.T) {
	const assertions = `
var kit = window.kit;
var paints = 0;
kit.component("flash", {
  copied: false,
  copy: function () {
    var self = this;
    setTimeout(function () { self.copied = true; }, 5);
    setTimeout(function () { self.copied = false; }, 15);
  }
});

var host = el("section", { "data-kit-component": "flash" });
var button = el("button", { "data-kit-click": "copy()" });
var label = el("b", { "data-kit-text": "copied ? 'yes' : 'no'" });
host.appendChild(button);
host.appendChild(label);
document.body.appendChild(host);
kit.render();
if (label.textContent !== "no") throw new Error("boot paint: " + label.textContent);

document.dispatchEvent({ type: "click", target: button });
if (label.textContent !== "no") throw new Error("a method that writes nothing synchronously must not change the paint");

setTimeout(function () {
  if (label.textContent !== "yes") throw new Error("timer write did not repaint: " + label.textContent);
  setTimeout(function () {
    if (label.textContent !== "no") throw new Error("reset write did not repaint: " + label.textContent);
    console.log("scope write outside a pass: repaint on set, repaint on reset");
  }, 15);
}, 10);
`
	runNodeDOMScript(t, "write_repaint.test.js", assertions)
}

// The counterpart: a write inside a pass (an event handler) paints once, synchronously — the
// write must not queue a second pass behind the one the handler already runs.
func TestScopeWriteInsideAPassDoesNotQueueAnotherPaint(t *testing.T) {
	const assertions = `
var kit = window.kit;
var host = el("section", { "data-kit-scope": "count: 0, paints: 0" });
var button = el("button", { "data-kit-click": "count = count + 1" });
var label = el("b", { "data-kit-text": "count" });
host.appendChild(button);
host.appendChild(label);
document.body.appendChild(host);
kit.render();

var paints = 0;
var probe = el("i", { "data-kit-text": "tick()" });
kit.scope.$.tick = function () { paints++; return ""; }; // raw page scope: the probe itself must not schedule a paint
document.body.appendChild(probe);
kit.render();
var before = paints;

document.dispatchEvent({ type: "click", target: button });
if (label.textContent !== "1") throw new Error("handler write did not paint synchronously: " + label.textContent);
var afterClick = paints;
if (afterClick !== before + 1) throw new Error("expected exactly one synchronous paint for the click, got " + (afterClick - before));

setTimeout(function () {
  if (paints !== afterClick) throw new Error("a write inside the handler queued " + (paints - afterClick) + " extra paint(s)");
  console.log("scope write inside a pass: one synchronous paint, nothing queued");
}, 10);
`
	runNodeDOMScript(t, "write_repaint_inside.test.js", assertions)
}
