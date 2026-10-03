package hydrate

import (
	"os"
	"path/filepath"
	"testing"
)

// dragDirective is jit/js/directives/drag.js — the file the engine delivers to a page that writes
// data-kit-drag. Read from disk because jit/js imports this package, not the other way round.
func dragDirective(t *testing.T) string {
	t.Helper()
	source, err := os.ReadFile(filepath.Join("..", "js", "directives", "drag.js"))
	if err != nil {
		t.Fatal(err)
	}
	return string(source)
}

// A desktop app opened with app.desktop({ chrome: "html" }) gets a window with no frame of its own,
// so the page draws its own title bar. data-kit-drag="true" marks the region the OS may drag the
// window by; a control inside opts out with data-kit-drag="false", so pressing a button is a press
// and not the start of a drag.
//
// ONE attribute, two values. It used to be a pair — data-kit-drag beside a separate
// data-kit-no-drag — which made the opt-out look like a different feature; the repo had three
// spellings of it (39 attributes) before this.
//
// The machinery is a directive file of its own since 02/10 (jit/js/directives/drag.js): the kernel
// alone does not drag — the control below — and a page that writes only data-kit-drag still gets
// the runtime, so the file has a kernel to stand on.
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

// Double-click TOGGLES, which is what a title bar does: the second one puts the window back. The
// shell is asked first, so the calls arrive in that order.
(async function () {
  var maximized = false;
  kit.bridge = {
    call: function (action) {
      calls.push(action);
      if (action === "window.maximize") maximized = true;
      if (action === "window.restore") maximized = false;
      return Promise.resolve(action === "window.isMaximized" ? maximized : true);
    }
  };

  calls = [];
  press(bar, "dblclick");
  await new Promise(function (r) { setTimeout(r, 0); });
  if (calls.join(",") !== "window.isMaximized,window.maximize") {
    throw new Error("first double-click should ask then maximise, got " + calls.join(","));
  }

  calls = [];
  press(bar, "dblclick");
  await new Promise(function (r) { setTimeout(r, 0); });
  if (calls.join(",") !== "window.isMaximized,window.restore") {
    throw new Error("second double-click should ask then restore, got " + calls.join(","));
  }

  calls = [];
  press(button, "dblclick");
  await new Promise(function (r) { setTimeout(r, 0); });
  if (calls.length !== 0) throw new Error("double-click on an opted-out control must do nothing");

  // A shell that cannot answer still gets a window maximised rather than nothing happening.
  kit.bridge = { call: function (action) { calls.push(action); return action === "window.isMaximized" ? Promise.reject(new Error("no")) : Promise.resolve(true); } };
  calls = [];
  press(bar, "dblclick");
  await new Promise(function (r) { setTimeout(r, 0); });
  if (calls.join(",") !== "window.isMaximized,window.maximize") {
    throw new Error("an unanswerable shell should still maximise, got " + calls.join(","));
  }

  kit.bridge = null;
  console.log("drag region: one attribute, true drags, false opts out, double-click toggles");
})();
`
	runNodeDOMScript(t, "drag_region.test.js", dragDirective(t)+"\n"+assertions)
}

func TestKernelAloneDoesNotDrag(t *testing.T) {
	const assertions = `
var calls = [];
window.kit.bridge = { call: function (action) { calls.push(action); return Promise.resolve(true); } };
var bar = el("div", { "data-kit-drag": "true" });
document.body.appendChild(bar);
document.dispatchEvent({ type: "mousedown", button: 0, target: bar });
document.dispatchEvent({ type: "dblclick", target: bar });
setTimeout(function () {
  if (calls.length !== 0) throw new Error("the kernel still drags by itself: " + calls.join(","));
  console.log("kernel alone: no window command");
}, 0);
`
	runNodeDOMScript(t, "kernel_alone_does_not_drag.test.js", assertions)

	page := `<head></head><body><header data-kit-drag="true"></header></body></html>`
	if !UsesGrammar(page) {
		t.Error("a page whose only grammar is data-kit-drag needs the runtime under its drag file")
	}
	if UsesGrammar(`<head></head><body><pre><code>&lt;header data-kit-drag="true"&gt;</code></pre></body></html>`) {
		t.Error("CONTROL: a page that only prints data-kit-drag in an example gets no runtime")
	}
}
