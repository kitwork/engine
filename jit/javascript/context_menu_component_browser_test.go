package javascript

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The context menu browser proof: a contextmenu event on the target opens the
// menu at the pointer, kept inside the viewport, with the first usable item
// focused; arrows move over usable items; Escape closes and restores the
// target; an outside pointerdown closes; a chosen item closes; Shift+F10 opens
// from the keyboard.
func TestBrowserContextMenuOpensAtThePointer(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping context-menu component browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := composer.ComposeStandalone([]ComponentRef{{Name: "context-menu", Version: "1.0.0"}}, false)
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/context.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(bundle.JavaScript)
		case "/context.html":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(contextMenuComponentDocument))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	runVanillaBrowser(t, browser, server.URL+"/context.html")
}

var contextMenuComponentDocument = fmt.Sprintf(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>KitJS context-menu component</title>
<style>#menu { width: 160px; height: 120px; }</style></head><body style="margin:0">
  <div id="host" data-kit-component="context-menu@1.0.0">
    <div id="target" data-context-target tabindex="0" style="width:400px;height:200px">Right-click here</div>
    <output id="state" data-kit-text="open ? 'open' : 'closed'"></output>
    <div id="menu" role="menu" data-context-menu hidden>
      <button id="item-rename" type="button" role="menuitem" data-context-item>Rename</button>
      <button id="item-off" type="button" role="menuitem" data-context-item disabled>Move</button>
      <button id="item-delete" type="button" role="menuitem" data-context-item>Delete</button>
    </div>
    <p id="outside">outside</p>
  </div>
  <script src="/context.js"></script><script>
%s
__runStandaloneKitTest(async function () {
  var waitFor = __kitTestWaitFor;
  var assert = __kitTestAssert;
  var state = function () { return document.getElementById("state").textContent.trim(); };
  var menu = document.getElementById("menu");
  var target = document.getElementById("target");
  function key(el, name, init) {
    var options = { key: name, bubbles: true, cancelable: true };
    for (var k in (init || {})) options[k] = init[k];
    el.dispatchEvent(new KeyboardEvent("keydown", options));
  }

  await waitFor(function () { return state() === "closed" && menu.hidden === true; }, "menu did not start closed");

  var contextEvent = new MouseEvent("contextmenu", { bubbles: true, cancelable: true, clientX: 50, clientY: 40 });
  target.dispatchEvent(contextEvent);
  await waitFor(function () { return state() === "open" && menu.hidden === false; }, "contextmenu did not open the menu");
  assert(contextEvent.defaultPrevented, "the browser's own menu was not suppressed");
  assert(menu.style.left === "50px" && menu.style.top === "40px", "menu did not open at the pointer: " + menu.style.left + "," + menu.style.top);
  assert(document.activeElement === document.getElementById("item-rename"), "the first usable item was not focused");

  key(document.activeElement, "ArrowDown");
  await waitFor(function () { return document.activeElement === document.getElementById("item-delete"); }, "ArrowDown did not skip the disabled item");
  key(document.activeElement, "ArrowDown");
  await waitFor(function () { return document.activeElement === document.getElementById("item-rename"); }, "ArrowDown did not wrap");

  key(document.activeElement, "Escape");
  await waitFor(function () { return menu.hidden === true && document.activeElement === target; }, "Escape did not close and restore the target");

  // Near the viewport's edge the menu is pulled back inside.
  target.dispatchEvent(new MouseEvent("contextmenu", { bubbles: true, cancelable: true, clientX: innerWidth - 10, clientY: innerHeight - 10 }));
  await waitFor(function () { return menu.hidden === false; }, "second open failed");
  var box = menu.getBoundingClientRect();
  assert(box.right <= innerWidth && box.bottom <= innerHeight, "menu opened off-screen: " + JSON.stringify({ right: box.right, bottom: box.bottom, w: innerWidth, h: innerHeight }));

  document.getElementById("outside").dispatchEvent(new PointerEvent("pointerdown", { bubbles: true }));
  await waitFor(function () { return menu.hidden === true; }, "an outside pointerdown did not close");

  // Shift+F10 on the focused target opens from the keyboard; choosing closes.
  target.focus();
  key(target, "F10", { shiftKey: true });
  await waitFor(function () { return menu.hidden === false; }, "Shift+F10 did not open the menu");
  document.getElementById("item-delete").click();
  await waitFor(function () { return menu.hidden === true; }, "choosing an item did not close the menu");
});
  </script>
</body></html>`, browserHarness)
