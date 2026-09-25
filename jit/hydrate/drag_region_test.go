package hydrate

import "testing"

// A desktop app opened with app.desktop({ chrome: "html" }) gets a window with no frame of its own,
// so the page draws its own title bar. data-kit-drag="true" marks the region the OS may drag the
// window by; a control inside opts out with data-kit-drag="false", so pressing a button is a press
// and not the start of a drag.
//
// ONE attribute, two values. It used to be a pair — data-kit-drag beside a separate
// data-kit-no-drag — which made the opt-out look like a different feature; the repo had three
// spellings of it (39 attributes) before this.
func TestDragRegionIsOneAttributeWithTwoValues(t *testing.T) {
	const assertions = `
var kit = window.kit;
var calls = [];
kit.bridge = { call: function (action) { calls.push(action); return Promise.resolve(true); } };

var bar = el("div", { "data-kit-drag": "true" });
var button = el("button", { "data-kit-drag": "false" });
var label = el("span", {});
var outside = el("div", {});
bar.appendChild(button); bar.appendChild(label);
document.body.appendChild(bar); document.body.appendChild(outside);

function press(target, type) {
  document.dispatchEvent({ type: type || "mousedown", button: 0, target: target });
}

press(bar);
if (calls.join(",") !== "window.drag") throw new Error("the bar itself should drag, got " + calls.join(","));

calls = [];
press(label);
if (calls.join(",") !== "window.drag") throw new Error("plain content inside the bar drags too, got " + calls.join(","));

calls = [];
press(button);
if (calls.length !== 0) throw new Error('data-kit-drag="false" must not drag, got ' + calls.join(","));

calls = [];
press(outside);
if (calls.length !== 0) throw new Error("outside the bar must not drag, got " + calls.join(","));

// Only the exact word counts. A value nobody defined is not a quiet yes — otherwise the attribute
// would be "present or not" again, which is the shape we just left behind.
calls = [];
var unclear = el("div", { "data-kit-drag": "maybe" });
document.body.appendChild(unclear);
press(unclear);
if (calls.length !== 0) throw new Error('data-kit-drag="maybe" must not drag, got ' + calls.join(","));

calls = [];
var empty = el("div", { "data-kit-drag": "" });
document.body.appendChild(empty);
press(empty);
if (calls.length !== 0) throw new Error("an empty value must not drag, got " + calls.join(","));

// A secondary button is not a drag either.
calls = [];
document.dispatchEvent({ type: "mousedown", button: 2, target: bar });
if (calls.length !== 0) throw new Error("right-click must not drag, got " + calls.join(","));

// Double-click on the bar maximises — what a title bar does everywhere — and the opt-out holds.
calls = [];
press(bar, "dblclick");
if (calls.join(",") !== "window.maximize") throw new Error("double-click should maximise, got " + calls.join(","));
calls = [];
press(button, "dblclick");
if (calls.length !== 0) throw new Error("double-click on an opted-out control must do nothing");

kit.bridge = null;
console.log("drag region: one attribute, true drags, false opts out");
`
	runNodeDOMScript(t, "drag_region.test.js", assertions)
}
