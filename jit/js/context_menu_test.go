package js

import (
	"strings"
	"testing"
)

func TestContextMenuIsShippedWhenUsed(t *testing.T) {
	out := Render(`<div data-kit-component="context-menu"><div data-kit-element="target">t</div></div>`)
	if !strings.Contains(out, `components=component%3Acontext-menu`) {
		t.Fatalf("context-menu was not injected for markup that uses it:\n%s", out)
	}
	if !strings.Contains(ModulesJS([]string{"context-menu"}), `component("context-menu@v1.0.0"`) {
		t.Error("versioned registration missing")
	}
}

// Three things only a browser can show: the panel opening at the pointer, staying on screen when
// the pointer is at the edge, and focus walking the items. The keyboard path opens it with no mouse.
func TestBrowserContextMenuOpensWhereAskedAndStaysOnScreen(t *testing.T) {
	item := func(id, label, extra string) string {
		return `<button type="button" id="` + id + `" role="menuitem" data-kit-element="item" ` + extra +
			` data-kit-click="picked = '` + label + `'; hide()" style="display:block;width:180px;height:32px">` + label + `</button>`
	}
	runBrowserProof(t, "context-menu", []string{"context-menu"}, `
<div data-kit-scope="picked: ''">
  <div id="host" data-kit-component="context-menu" data-kit-click:outside="hide()">
    <div id="target" data-kit-element="target" tabindex="0" style="width:300px;height:120px">Right-click here</div>
    <div id="panel" role="menu" aria-label="File" data-kit-show="open" hidden
         style="position:fixed;width:200px" data-kit-style:left="x + 'px'" data-kit-style:top="y + 'px'">
      `+item("rename", "Rename", "")+item("share", "Share", "disabled")+item("remove", "Delete", "")+`
    </div>
  </div>
  <output id="picked" data-kit-text="picked"></output>
</div>
<button id="elsewhere">elsewhere</button>
<script>
(async function () {
  await wait(80);
  var target = document.getElementById("target"), panel = document.getElementById("panel");
  function rightClick(x, y) { target.dispatchEvent(new MouseEvent("contextmenu", { clientX: x, clientY: y, bubbles: true, cancelable: true })); }

  // A right-click opens the panel AT the pointer, and focus lands on the first choosable item.
  rightClick(40, 50); await wait(80);
  assert(!panel.hidden, "a right-click opens the menu");
  assert(panel.style.left === "40px" && panel.style.top === "50px", "the panel sits at the pointer, got " + panel.style.left + "," + panel.style.top);
  assert(active() === "rename", "focus lands on the first item, it is on " + active());

  // ↓ skips the disabled item and wraps.
  key(document.activeElement, "ArrowDown"); await wait(30);
  assert(active() === "remove", "ArrowDown skips the disabled Share, focus is on " + active());
  key(document.activeElement, "ArrowDown"); await wait(30);
  assert(active() === "rename", "and wraps, focus is on " + active());

  // Escape closes and gives focus back to the target.
  key(document.activeElement, "Escape"); await wait(50);
  assert(panel.hidden && active() === "target", "Escape closes and returns focus to the target, focus is on " + active());

  // A right-click at the bottom-right corner is pulled back on screen.
  rightClick(innerWidth - 5, innerHeight - 5); await wait(120);
  var box = panel.getBoundingClientRect();
  assert(box.right <= innerWidth - 7 && box.bottom <= innerHeight - 7, "the panel is pulled back inside the view, its box ends at " + Math.round(box.right) + "," + Math.round(box.bottom) + " of " + innerWidth + "," + innerHeight);
  key(document.activeElement, "Escape"); await wait(40);

  // No mouse at all: Shift+F10 on the focused target opens it under the target.
  focusIn(target);
  var open = key(target, "F10", { shiftKey: true }); await wait(80);
  assert(open.defaultPrevented && !panel.hidden && active() === "rename", "Shift+F10 opens the menu from the keyboard, focus is on " + active());

  // Choosing runs the item's own expression, which also closes the menu.
  document.getElementById("remove").click(); await wait(50);
  assert(document.getElementById("picked").textContent === "Delete" && panel.hidden, "choosing runs the item's expression and closes");

  // CONTROL: an outside click closes it through the grammar, and focus is not dragged back.
  rightClick(60, 60); await wait(80);
  document.getElementById("elsewhere").focus(); document.getElementById("elsewhere").click(); await wait(50);
  assert(panel.hidden, "an outside click closes it through data-kit-click:outside");

  root.setAttribute("data-kit-test", "passed");
})().catch(function (error) { if (!root.hasAttribute("data-kit-test")) { root.setAttribute("data-kit-test", "failed"); root.setAttribute("data-kit-test-error", String(error && error.message || error)); } });
</script>`)
}

// A fixed panel is placed against the viewport only when no ancestor has a transform, a filter or a
// backdrop-filter; any of those becomes its containing block. The catalogue's demo frame is such an
// ancestor (backdrop-blur), and there the panel used to open far from the pointer and run off the
// bottom of the screen. The panel must land at the pointer in the VIEWPORT, whatever box it is
// positioned against.
func TestBrowserContextMenuLandsAtThePointerInsideATransformedFrame(t *testing.T) {
	runBrowserProof(t, "context-menu frame", []string{"context-menu"}, `
<div style="margin:120px 0 0 90px;transform:translateZ(0);width:320px;height:160px">
  <div data-kit-component="context-menu" data-kit-click:outside="hide()">
    <div id="target" data-kit-element="target" tabindex="0" style="width:300px;height:120px">Right-click here</div>
    <div id="panel" role="menu" aria-label="File" data-kit-show="open" hidden
         style="position:fixed;width:200px" data-kit-style:left="x + 'px'" data-kit-style:top="y + 'px'">
      <button type="button" role="menuitem" data-kit-element="item" style="display:block;width:180px;height:32px">Rename</button>
      <button type="button" role="menuitem" data-kit-element="item" style="display:block;width:180px;height:32px">Delete</button>
    </div>
  </div>
</div>
<script>
(async function () {
  await wait(80);
  var target = document.getElementById("target"), panel = document.getElementById("panel");
  function rightClick(x, y) { target.dispatchEvent(new MouseEvent("contextmenu", { clientX: x, clientY: y, bubbles: true, cancelable: true })); }

  rightClick(150, 170); await wait(120);
  var box = panel.getBoundingClientRect();
  assert(Math.round(box.left) === 150 && Math.round(box.top) === 170, "the panel lands at the pointer in the viewport, got " + Math.round(box.left) + "," + Math.round(box.top));
  key(document.activeElement, "Escape"); await wait(40);

  rightClick(innerWidth - 5, innerHeight - 5); await wait(120);
  box = panel.getBoundingClientRect();
  assert(box.right <= innerWidth - 7 && box.bottom <= innerHeight - 7, "and is kept inside the view, its box ends at " + Math.round(box.right) + "," + Math.round(box.bottom) + " of " + innerWidth + "," + innerHeight);

  root.setAttribute("data-kit-test", "passed");
})().catch(function (error) { if (!root.hasAttribute("data-kit-test")) { root.setAttribute("data-kit-test", "failed"); root.setAttribute("data-kit-test-error", String(error && error.message || error)); } });
</script>`)
}
