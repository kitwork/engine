package javascript

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The tree browser proof: authored branches open from the expanded seed and
// hide their groups otherwise; a toggle click opens and selects; the arrow
// keys walk visible items only, ArrowRight opens then descends, ArrowLeft
// closes then climbs; Enter selects; the roving tabindex follows focus; the
// methods expand and collapse from a directive.
func TestBrowserTreeOpensBranchesAndWalksWithArrows(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping tree component browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := composer.ComposeStandalone([]ComponentRef{{Name: "tree", Version: "1.0.0"}}, false)
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/tree.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(bundle.JavaScript)
		case "/tree.html":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(treeComponentDocument))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	runVanillaBrowser(t, browser, server.URL+"/tree.html")
}

var treeComponentDocument = fmt.Sprintf(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>KitJS tree component</title></head><body>
  <ul role="tree" data-kit-component="tree@1.0.0" data-kit-scope="expanded: ['src']">
    <li role="treeitem" data-tree-item data-tree-id="src" id="src">
      <button id="src-toggle" type="button" data-tree-toggle>src</button>
      <ul role="group" data-tree-group id="src-group">
        <li role="treeitem" data-tree-item data-tree-id="app" id="app"><button id="app-toggle" type="button" data-tree-toggle>app.kitwork.js</button></li>
        <li role="treeitem" data-tree-item data-tree-id="views" id="views">
          <button id="views-toggle" type="button" data-tree-toggle>views</button>
          <ul role="group" data-tree-group id="views-group">
            <li role="treeitem" data-tree-item data-tree-id="home" id="home"><button id="home-toggle" type="button" data-tree-toggle>home.html</button></li>
          </ul>
        </li>
      </ul>
    </li>
    <li role="treeitem" data-tree-item data-tree-id="readme" id="readme"><button id="readme-toggle" type="button" data-tree-toggle>README</button></li>
    <li><button id="open-all" type="button" data-kit-click="expandAll()">all</button><button id="close-all" type="button" data-kit-click="collapseAll()">none</button></li>
    <output id="state" data-kit-text="expanded.join(',') + '|' + selected"></output>
  </ul>
  <script src="/tree.js"></script><script>
%s
__runStandaloneKitTest(async function () {
  var waitFor = __kitTestWaitFor;
  var assert = __kitTestAssert;
  function state() { return document.getElementById("state").textContent.trim(); }
  function key(el, name) { el.dispatchEvent(new KeyboardEvent("keydown", { key: name, bubbles: true, cancelable: true })); }
  var src = document.getElementById("src"), views = document.getElementById("views");

  await waitFor(function () { return src.getAttribute("aria-expanded") === "true" && document.getElementById("src-group").hidden === false; }, "the seeded branch did not open");
  assert(views.getAttribute("aria-expanded") === "false" && document.getElementById("views-group").hidden === true, "a closed branch showed its group");
  assert(document.getElementById("app").hasAttribute("aria-expanded") === false, "a leaf carried aria-expanded");
  assert(document.getElementById("src-toggle").getAttribute("tabindex") === "0" && document.getElementById("app-toggle").getAttribute("tabindex") === "-1", "the roving tabindex did not start on the first item");

  document.getElementById("views-toggle").click();
  await waitFor(function () { return views.getAttribute("aria-expanded") === "true" && state() === "src,views|views"; }, "clicking a branch did not open and select it: " + state());

  // Arrows walk the visible items only.
  document.getElementById("views-toggle").focus();
  key(views, "ArrowDown");
  await waitFor(function () { return document.activeElement === document.getElementById("home-toggle"); }, "ArrowDown did not descend into the open branch");
  key(document.getElementById("home"), "ArrowDown");
  await waitFor(function () { return document.activeElement === document.getElementById("readme-toggle"); }, "ArrowDown did not reach the next visible top-level item");
  key(document.getElementById("readme"), "ArrowUp");
  await waitFor(function () { return document.activeElement === document.getElementById("home-toggle"); }, "ArrowUp did not go back");
  key(document.getElementById("home"), "ArrowLeft");
  await waitFor(function () { return document.activeElement === document.getElementById("views-toggle"); }, "ArrowLeft on a leaf did not climb to its parent");
  key(views, "ArrowLeft");
  await waitFor(function () { return views.getAttribute("aria-expanded") === "false"; }, "ArrowLeft on an open branch did not close it");
  key(views, "ArrowRight");
  await waitFor(function () { return views.getAttribute("aria-expanded") === "true"; }, "ArrowRight on a closed branch did not open it");
  key(views, "Enter");
  await waitFor(function () { return views.getAttribute("aria-expanded") === "false" && views.getAttribute("aria-selected") === "true"; }, "Enter did not toggle and select");

  document.getElementById("close-all").click();
  await waitFor(function () { return state() === "|views" && document.getElementById("src-group").hidden === true; }, "collapseAll from a directive did not close everything: " + state());
  document.getElementById("open-all").click();
  await waitFor(function () { return state() === "src,views|views" && document.getElementById("views-group").hidden === false; }, "expandAll from a directive did not open every branch: " + state());
});
  </script>
</body></html>`, browserHarness)
