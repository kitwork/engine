package javascript

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The data table browser proof: rows render through visible(), sorting flips
// direction on a repeated key and compares numbers as numbers, filtering
// matches every word across fields and resets the page, paging clamps at the
// ends and reports from/to/total, and aria-sort follows the sorted column.
func TestBrowserDataTableSortsFiltersAndPages(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping data-table component browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := composer.ComposeStandalone([]ComponentRef{{Name: "data-table", Version: "1.0.0"}}, false)
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/table.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(bundle.JavaScript)
		case "/table.html":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(dataTableComponentDocument))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	runVanillaBrowser(t, browser, server.URL+"/table.html")
}

var dataTableComponentDocument = fmt.Sprintf(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>KitJS data-table component</title></head><body>
  <div data-kit-component="data-table@1.0.0" data-kit-scope="rows: [{ name: 'Mango', qty: 12, region: 'south' }, { name: 'apple', qty: 3, region: 'north' }, { name: 'Banana', qty: 100, region: 'south' }, { name: 'cherry', qty: 7, region: 'north' }, { name: 'Kiwi', qty: 25, region: 'west' }]; pageSize: 2">
    <input id="search" type="search" data-kit-model="query" data-kit-input="search(query)">
    <table>
      <thead><tr>
        <th id="head-name" data-kit-bind="aria-sort: direction('name');"><button id="sort-name" type="button" data-kit-click="sortBy('name')">Name</button></th>
        <th id="head-qty" data-kit-bind="aria-sort: direction('qty');"><button id="sort-qty" type="button" data-kit-click="sortBy('qty')">Qty</button></th>
      </tr></thead>
      <tbody id="body"><template data-kit-for="row of visible()"><tr><td data-kit-text="row.name"></td><td data-kit-text="row.qty"></td></tr></template></tbody>
    </table>
    <output id="range" data-kit-text="from() + '-' + to() + '/' + total() + ' p' + current() + '/' + pages()"></output>
    <button id="prev" type="button" data-kit-click="previous()">Prev</button>
    <button id="next" type="button" data-kit-click="next()">Next</button>
  </div>
  <script src="/table.js"></script><script>
%s
__runStandaloneKitTest(async function () {
  var waitFor = __kitTestWaitFor;
  var assert = __kitTestAssert;
  function names() { return Array.prototype.map.call(document.querySelectorAll("#body tr td:first-child"), function (td) { return td.textContent; }).join(","); }
  function range() { return document.getElementById("range").textContent.trim(); }

  await waitFor(function () { return names() === "Mango,apple" && range() === "1-2/5 p1/3"; }, "first page did not render in authored order: " + names() + " " + range());

  document.getElementById("next").click();
  await waitFor(function () { return names() === "Banana,cherry" && range() === "3-4/5 p2/3"; }, "next did not page: " + names());
  document.getElementById("next").click(); document.getElementById("next").click();
  await waitFor(function () { return names() === "Kiwi" && range() === "5-5/5 p3/3"; }, "paging did not clamp at the last page: " + range());

  document.getElementById("sort-name").click();
  await waitFor(function () { return names() === "apple,Banana"; }, "name sort was not case-insensitive from page one: " + names() + " " + range());
  assert(document.getElementById("head-name").getAttribute("aria-sort") === "ascending", "aria-sort did not follow the sorted column");
  document.getElementById("sort-name").click();
  await waitFor(function () { return names() === "Mango,Kiwi" && document.getElementById("head-name").getAttribute("aria-sort") === "descending"; }, "a second click did not flip to descending: " + names());

  document.getElementById("sort-qty").click();
  await waitFor(function () { return names() === "apple,cherry" && document.getElementById("head-name").getAttribute("aria-sort") === "none"; }, "numeric sort did not order by value: " + names());
  document.getElementById("sort-qty").click();
  await waitFor(function () { return names() === "Banana,Kiwi"; }, "descending numeric sort was wrong: " + names());

  var search = document.getElementById("search");
  search.value = "south"; search.dispatchEvent(new Event("input", { bubbles: true }));
  await waitFor(function () { return names() === "Banana,Mango" && range() === "1-2/2 p1/1"; }, "filter did not match across fields and reset the page: " + names() + " " + range());
  search.value = "sou an"; search.dispatchEvent(new Event("input", { bubbles: true }));
  await waitFor(function () { return names() === "Banana,Mango"; }, "every-word filter failed: " + names());
  search.value = "zzz"; search.dispatchEvent(new Event("input", { bubbles: true }));
  await waitFor(function () { return names() === "" && range() === "0-0/0 p1/1"; }, "an empty result did not report zero: " + range());
});
  </script>
</body></html>`, browserHarness)
