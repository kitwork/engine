package js

import (
	"strings"
	"testing"
)

func TestSplitIsShippedWhenUsed(t *testing.T) {
	out := deliver(`<div data-kit-component="split"><div role="separator" data-kit-element="handle"></div></div>`)
	if !strings.Contains(out, `/kit/components/split@`) {
		t.Fatalf("split was not injected for markup that uses it:\n%s", out)
	}
	if !strings.Contains(ModulesJS([]string{"split"}), `component("split@v1.0.0"`) {
		t.Error("versioned registration missing")
	}
}

// The divider is the component: a drag measured against the container, and the window-splitter
// keys. The pane's width is the markup's own style binding, so what the proof reads back is the
// rendered page, not the component's state.
func TestBrowserSplitMovesByKeyAndByDrag(t *testing.T) {
	runBrowserProof(t, "split", []string{"split"}, `
<div id="host" data-kit-component="split" data-kit-alias="$split" style="display:flex;width:800px;height:200px">
  <aside id="pane" style="flex-shrink:0" data-kit-style:flex-basis="collapsed ? '0%' : size + '%'">Files</aside>
  <div id="bar" role="separator" data-kit-element="handle" tabindex="0" aria-label="Resize"
       aria-orientation="vertical" aria-valuemin="20" aria-valuemax="60" aria-valuenow="34" data-step="5"
       data-kit-bind:aria-valuenow="collapsed ? 0 : size" style="width:6px"></div>
  <main style="flex:1">body</main>
  <button id="reset" data-kit-click="reset()">Reset</button>
</div>
<script>
(async function () {
  await wait(80);
  var bar = document.getElementById("bar"), pane = document.getElementById("pane");
  var now = function () { return bar.getAttribute("aria-valuenow"); };
  var basis = function () { return pane.style.flexBasis; };

  // It starts where the markup's aria-valuenow says, and the pane's binding follows.
  assert(now() === "34" && basis() === "34%", "starts at aria-valuenow 34, got " + now() + " / " + basis());

  // The keys: a step each way, the limits from aria-valuemin/max, and they hold at the edge.
  focusIn(bar);
  key(bar, "ArrowRight"); await wait(30);
  assert(now() === "39" && basis() === "39%", "ArrowRight moves one step of 5, got " + now());
  key(bar, "ArrowLeft"); key(bar, "ArrowLeft"); await wait(30);
  assert(now() === "29", "ArrowLeft twice moves back two steps, got " + now());
  key(bar, "Home"); await wait(30);
  assert(now() === "20", "Home goes to aria-valuemin, got " + now());
  key(bar, "End"); await wait(30);
  assert(now() === "60", "End goes to aria-valuemax, got " + now());
  key(bar, "ArrowRight"); await wait(30);
  assert(now() === "60", "it holds at the maximum, got " + now());

  // Enter collapses the pane and restores it where it was.
  key(bar, "Enter"); await wait(30);
  assert(now() === "0" && basis() === "0%", "Enter collapses, got " + now() + " / " + basis());
  key(bar, "Enter"); await wait(30);
  assert(now() === "60" && basis() === "60%", "Enter again restores the size it had, got " + now());

  // The drag: the pointer's position against the container becomes the size.
  var host = document.getElementById("host").getBoundingClientRect();
  bar.dispatchEvent(new PointerEvent("pointerdown", { pointerId: 7, button: 0, clientX: host.left + 480, clientY: host.top + 10, bubbles: true, cancelable: true }));
  bar.dispatchEvent(new PointerEvent("pointermove", { pointerId: 7, clientX: host.left + 360, clientY: host.top + 10, bubbles: true }));
  await wait(30);
  assert(now() === "45" && basis() === "45%", "dragging to 360 of 800px makes 45%, got " + now());
  bar.dispatchEvent(new PointerEvent("pointermove", { pointerId: 7, clientX: host.left + 20, clientY: host.top + 10, bubbles: true }));
  await wait(30);
  assert(now() === "20", "a drag past the minimum stops at it, got " + now());
  bar.dispatchEvent(new PointerEvent("pointerup", { pointerId: 7, bubbles: true }));
  bar.dispatchEvent(new PointerEvent("pointermove", { pointerId: 7, clientX: host.left + 400, clientY: host.top + 10, bubbles: true }));
  await wait(30);
  assert(now() === "20", "after pointerup a move changes nothing, got " + now());

  // reset() returns to the markup's starting size.
  document.getElementById("reset").click(); await wait(30);
  assert(now() === "34", "reset returns to aria-valuenow's 34, got " + now());

  root.setAttribute("data-kit-test", "passed");
})().catch(function (error) { if (!root.hasAttribute("data-kit-test")) { root.setAttribute("data-kit-test", "failed"); root.setAttribute("data-kit-test-error", String(error && error.message || error)); } });
</script>`)
}
