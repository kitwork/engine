package javascript

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The split browser proof: the first pane takes the seeded size and the handle
// carries the separator's ARIA; the arrow keys move it a step and clamp at the
// bounds, Home and End jump to them; a pointer drag follows the pointer as a
// percentage of the host; Enter collapses and restores; the methods answer a
// directive; a "y" split reports the other orientation and reads the other
// arrow pair.
func TestBrowserSplitResizesByPointerAndKeyboard(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping split component browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := composer.ComposeStandalone([]ComponentRef{{Name: "split", Version: "1.0.0"}}, false)
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/split.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(bundle.JavaScript)
		case "/split.html":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(splitComponentDocument))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	runVanillaBrowser(t, browser, server.URL+"/split.html")
}

var splitComponentDocument = fmt.Sprintf(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>KitJS split component</title></head><body>
  <div id="host" data-kit-component="split@1.0.0" data-kit-scope="size: 30, min: 20, max: 70, step: 10" style="display:flex;width:600px;height:200px">
    <div id="panel" data-split-panel style="flex-basis:30%%;flex-shrink:0">first</div>
    <div id="handle" data-split-handle role="separator" tabindex="0" aria-label="Resize" style="width:6px;flex-shrink:0"></div>
    <div style="flex:1">second</div>
    <div style="display:none">
      <button id="set" type="button" data-kit-click="set(25)">25</button>
      <button id="reset" type="button" data-kit-click="reset()">reset</button>
      <button id="fold" type="button" data-kit-click="collapse()">fold</button>
      <output id="state" data-kit-text="size + '|' + (collapsed ? 'folded' : 'open') + '|' + (dragging ? 'drag' : 'still')"></output>
    </div>
  </div>
  <div id="stack" data-kit-component="split@1.0.0" data-kit-scope="size: 50, axis: 'y'" style="display:flex;flex-direction:column;width:300px;height:400px">
    <div id="stack-panel" data-split-panel style="flex-basis:50%%;flex-shrink:0">top</div>
    <div id="stack-handle" data-split-handle role="separator" tabindex="0" aria-label="Resize" style="height:6px;flex-shrink:0"></div>
    <div style="flex:1">bottom</div>
  </div>
  <script src="/split.js"></script><script>
%s
__runStandaloneKitTest(async function () {
  var waitFor = __kitTestWaitFor;
  var assert = __kitTestAssert;
  var host = document.getElementById("host"), panel = document.getElementById("panel"), handle = document.getElementById("handle");
  function state() { return document.getElementById("state").textContent.trim(); }
  function key(el, name) { el.dispatchEvent(new KeyboardEvent("keydown", { key: name, bubbles: true, cancelable: true })); }
  function pointer(type, el, x, y) {
    el.dispatchEvent(new PointerEvent(type, { bubbles: true, cancelable: true, pointerId: 7, button: 0, clientX: x, clientY: y }));
  }

  await waitFor(function () { return handle.getAttribute("aria-valuenow") === "30" && panel.style.flexBasis === "30%%"; }, "the seeded size did not reach the pane and the handle");
  assert(handle.getAttribute("aria-orientation") === "vertical" && handle.getAttribute("aria-valuemin") === "20" && handle.getAttribute("aria-valuemax") === "70", "the separator ARIA is wrong: " + handle.outerHTML);
  assert(state() === "30|open|still", "initial state: " + state());

  handle.focus();
  key(handle, "ArrowRight");
  await waitFor(function () { return state() === "40|open|still" && panel.style.flexBasis === "40%%"; }, "ArrowRight did not grow by a step: " + state());
  key(handle, "ArrowRight"); key(handle, "ArrowRight"); key(handle, "ArrowRight"); key(handle, "ArrowRight");
  await waitFor(function () { return state() === "70|open|still"; }, "growing did not clamp at max: " + state());
  key(handle, "Home");
  await waitFor(function () { return state() === "20|open|still"; }, "Home did not jump to min: " + state());
  key(handle, "End");
  await waitFor(function () { return state() === "70|open|still"; }, "End did not jump to max: " + state());
  key(handle, "ArrowLeft");
  await waitFor(function () { return state() === "60|open|still"; }, "ArrowLeft did not shrink by a step: " + state());

  // A drag: down on the handle, move to the middle of the host, up.
  var rect = host.getBoundingClientRect();
  pointer("pointerdown", handle, rect.left + rect.width * 0.6, rect.top + 10);
  await waitFor(function () { return handle.getAttribute("data-state") === "dragging" && state() === "60|open|drag"; }, "pointerdown did not start a drag: " + state());
  pointer("pointermove", handle, rect.left + rect.width * 0.5, rect.top + 10);
  await waitFor(function () { return state() === "50|open|drag" && panel.style.flexBasis === "50%%"; }, "pointermove did not follow the pointer: " + state());
  pointer("pointermove", handle, rect.left + rect.width * 0.05, rect.top + 10);
  await waitFor(function () { return state() === "20|open|drag"; }, "a drag past min did not clamp: " + state());
  pointer("pointerup", handle, rect.left + rect.width * 0.05, rect.top + 10);
  await waitFor(function () { return state() === "20|open|still" && handle.getAttribute("data-state") === "idle"; }, "pointerup did not end the drag: " + state());

  key(handle, "Enter");
  await waitFor(function () { return state() === "20|folded|still" && panel.style.flexBasis === "0%%" && panel.getAttribute("data-state") === "collapsed"; }, "Enter did not collapse: " + state());
  assert(handle.getAttribute("aria-valuenow") === "0", "a collapsed handle did not report 0");
  key(handle, "Enter");
  await waitFor(function () { return state() === "20|open|still" && panel.style.flexBasis === "20%%"; }, "Enter did not restore the size: " + state());

  document.getElementById("set").click();
  await waitFor(function () { return state() === "25|open|still"; }, "set() from a directive did not apply: " + state());
  document.getElementById("fold").click();
  await waitFor(function () { return state() === "25|folded|still"; }, "collapse() from a directive did not apply: " + state());
  document.getElementById("reset").click();
  await waitFor(function () { return state() === "30|open|still" && panel.style.flexBasis === "30%%"; }, "reset() did not return to the seeded size and expand: " + state());

  var stackHandle = document.getElementById("stack-handle"), stackPanel = document.getElementById("stack-panel");
  await waitFor(function () { return stackHandle.getAttribute("aria-orientation") === "horizontal" && stackHandle.getAttribute("aria-valuenow") === "50"; }, "a y split did not report a horizontal separator");
  stackHandle.focus();
  key(stackHandle, "ArrowDown");
  await waitFor(function () { return stackPanel.style.flexBasis === "55%%"; }, "ArrowDown did not grow a y split");
  key(stackHandle, "ArrowRight");
  await new Promise(function (resolve) { setTimeout(resolve, 60); });
  assert(stackPanel.style.flexBasis === "55%%", "ArrowRight moved a y split");
});
  </script>
</body></html>`, browserHarness)
