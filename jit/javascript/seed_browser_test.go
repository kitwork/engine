package javascript

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// data-kit-seed — DOM → state, once (the mirror of data-kit-bind): the text the server rendered
// becomes state, a property or attribute becomes state by the bind groups read in reverse, a
// dotted path creates its objects, list[] collects one entry per element in document order, and a
// <script type="application/json"> hands over structured data. The DOM wins over a scope literal;
// an undeclared field on a component host is an error, not a silent new field; seeding does not
// run again on the next render.
func TestBrowserSeedHandsTheDOMToState(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping seed browser contract in short mode")
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
		case "/seed.html":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(seedDocument))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	runVanillaBrowser(t, browser, server.URL+"/seed.html")
}

var seedDocument = fmt.Sprintf(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>KitJS seed</title></head><body>
  <script>
  window.__consoleErrors = [];
  var __consoleError = console.error;
  console.error = function () { window.__consoleErrors.push(Array.prototype.map.call(arguments, function (a) { return a && a.message || String(a); }).join(" ")); return __consoleError.apply(this, arguments); };
  </script>
  <section id="page" data-kit-scope="title: 'from the literal', tags: [], user: null, count: 0, expanded: false, items: [], busy: 'unset'">
    <h1 id="title" data-kit-seed="title" data-kit-text="title">
      Rendered by the server
    </h1>
    <input id="email" value="ada@example.com" data-kit-seed:value="user.email">
    <input id="age" type="number" value="36" data-kit-seed:value="count">
    <details id="details" open data-kit-seed:open="expanded"><summary>more</summary></details>
    <button id="busy" aria-busy="true" data-kit-seed:aria-busy="busy" data-kit-seed:data-missing="user.missing">go</button>
    <ul id="tags"><li data-kit-seed="tags[]">go</li><li data-kit-seed="tags[]">sqlite</li><li data-kit-seed="tags[]">html</li></ul>
    <script type="application/json" data-kit-seed="items">[{"id": 1, "name": "Ada"}, {"id": 2, "name": "Bob"}]</script>
    <output id="out" data-kit-text="title + '|' + user.email + '|' + count + '|' + expanded + '|' + busy + '|' + user.missing + '|' + tags.join(',') + '|' + items.length + ':' + items[1].name"></output>
    <button id="retitle" type="button" data-kit-click="title = 'changed by an action'; count = 1">retitle</button>
    <div id="host" data-kit-component="switch@1.0.0" data-kit-seed:data-nope="nope" data-nope="x"></div>
  </section>
  <script src="/kit.js"></script><script>
%s
__runStandaloneKitTest(async function () {
  var waitFor = __kitTestWaitFor;
  var assert = __kitTestAssert;
  var byId = function (id) { return document.getElementById(id); };
  await waitFor(function () { return byId("out").textContent.indexOf("Rendered by the server") === 0; }, "the server text should have become state (DOM wins over the scope literal), out = " + JSON.stringify(byId("out").textContent) + " errors: " + window.__consoleErrors.join(" | "));
  assert(byId("out").textContent === "Rendered by the server|ada@example.com|36|true|true|null|go,sqlite,html|2:Bob", "seeded state, got " + byId("out").textContent);
  assert(byId("title").textContent === "Rendered by the server", "the text binding re-renders the same trimmed value, got " + JSON.stringify(byId("title").textContent));

  // Seeding is once: a later state change is not undone by a re-render reading the DOM again.
  byId("retitle").click();
  await waitFor(function () { return byId("title").textContent === "changed by an action"; }, "the action should win after boot");
  assert(byId("out").textContent.indexOf("changed by an action|ada@example.com|1|") === 0, "state should keep the action's values — the number input still says 36 in the DOM, and must not be seeded again, got " + byId("out").textContent);

  // An undeclared field on a component host is an error, not a silent new field.
  assert(window.__consoleErrors.some(function (m) { return /nope/.test(m) && /not declared/.test(m); }), "seeding an undeclared component field should be reported, errors: " + window.__consoleErrors.join(" | "));
});
  </script>
</body></html>`, browserHarness)
