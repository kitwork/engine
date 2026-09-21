package javascript

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// data-kit-for is authored on the row itself (ideaship-final §7): <li data-kit-for="item, i of
// items" data-kit-key="item.id">. The row is the blueprint and leaves the document; each rendered
// row sees the overlay item / index / count / first / last / even / odd; a keyed row survives a
// reorder as the same node; a row whose key is gone is removed. The five overlay words are lexical
// to the row — a nested component host keeps its own `count` — while the authored item name flows in.
func TestBrowserForOnAPlainElement(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping direct for browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := composer.ComposeStandalone([]ComponentRef{{Name: "switch", Version: "1.0.0"}}, false)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/kit.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(bundle.JavaScript)
		case "/for.html":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(directForDocument))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	runVanillaBrowser(t, browser, server.URL+"/for.html")
}

var directForDocument = fmt.Sprintf(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>KitJS for on a plain element</title></head><body>
  <section id="list" data-kit-scope="items: [{ id: 'a', name: 'Ada' }, { id: 'b', name: 'Bob' }, { id: 'c', name: 'Cy' }]">
    <ul id="rows">
      <li data-kit-for="item, i of items" data-kit-key="item.id" data-kit-bind:data-id="item.id"
          data-kit-text="item.name + ' ' + i + '/' + count + (first ? ' first' : '') + (last ? ' last' : '') + (even ? ' even' : ' odd')"></li>
    </ul>
    <ul id="nested">
      <li data-kit-for="item of items" data-kit-key="item.id"><b class="own-count" data-kit-component="tally" data-kit-text="count"></b><i class="own-item" data-kit-component="tally" data-kit-text="item.name"></i></li>
    </ul>
    <button id="reverse" type="button" data-kit-click="items = [items[2], items[1], items[0]]">reverse</button>
    <button id="drop" type="button" data-kit-click="items = [items[0], items[1]]">drop</button>
  </section>
  <script src="/kit.js"></script><script>
kit.component("tally", { count: 0 });
%s
__runStandaloneKitTest(async function () {
  var waitFor = __kitTestWaitFor;
  var assert = __kitTestAssert;
  var rows = function () { return Array.prototype.slice.call(document.querySelectorAll("#rows > li")); };
  var texts = function () { return rows().map(function (li) { return li.textContent; }).join("|"); };

  await waitFor(function () { return rows().length === 3; }, "three rows should be materialised from the plain <li>");
  assert(!document.querySelector("#rows > li[data-kit-for]"), "the authored row is the blueprint and must leave the document");
  assert(texts() === "Ada 0/3 first even|Bob 1/3 odd|Cy 2/3 last even", "overlay item/index/count/first/last/even/odd, got " + texts());
  var ada = rows()[0], cy = rows()[2];
  // The overlay words are lexical to the row: a nested component host keeps its own count, while
  // the authored item name still reaches it.
  await waitFor(function () { return document.querySelectorAll("#nested .own-count").length === 3; }, "nested rows should render");
  var ownCounts = Array.prototype.map.call(document.querySelectorAll("#nested .own-count"), function (b) { return b.textContent; }).join(",");
  assert(ownCounts === "0,0,0", "a nested component's own count field must not be shadowed by the row overlay, got " + ownCounts);
  var ownItems = Array.prototype.map.call(document.querySelectorAll("#nested .own-item"), function (b) { return b.textContent; }).join(",");
  assert(ownItems === "Ada,Bob,Cy", "the authored item name still flows into a nested host, got " + ownItems);

  document.getElementById("reverse").click();
  await waitFor(function () { return texts() === "Cy 0/3 first even|Bob 1/3 odd|Ada 2/3 last even"; }, "reorder should re-render the overlay, got " + texts());
  assert(rows()[0] === cy && rows()[2] === ada, "keyed rows must survive a reorder as the same nodes");

  document.getElementById("drop").click();
  await waitFor(function () { return rows().length === 2; }, "a row whose key is gone is removed");
  assert(texts() === "Cy 0/2 first even|Bob 1/2 last odd", "count/last follow the shorter list, got " + texts());
});
  </script>
</body></html>`, browserHarness)
