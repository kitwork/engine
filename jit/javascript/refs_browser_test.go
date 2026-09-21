package javascript

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// data-kit-ref names a DOM element; `$refs.<name>` reads it from an action (ideaship-final §6).
// The registry is the acting boundary's own — the nearest component host or data-kit-scope, else
// the page — so a nested boundary's ref is not visible from outside, an outer ref is not visible
// from inside, and a missing name is nullish.
func TestBrowserRefsAreScopedToTheActingBoundary(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping $refs browser contract in short mode")
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
		case "/refs.html":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(refsDocument))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	runVanillaBrowser(t, browser, server.URL+"/refs.html")
}

var refsDocument = fmt.Sprintf(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>KitJS $refs</title></head><body>
  <input id="page-field" data-kit-ref="field" value="page">
  <button id="page-focus" type="button" data-kit-click="$refs.field.focus()">page focus</button>
  <section id="outer" data-kit-scope="found: '', missing: 'unset'">
    <input id="outer-field" data-kit-ref="field" value="outer">
    <button id="outer-focus" type="button" data-kit-click="$refs.field.focus()">outer focus</button>
    <button id="outer-read" type="button" data-kit-click="found = $refs.field.value; missing = $refs.nothing == null ? 'null' : 'element'">read</button>
    <button id="outer-inner" type="button" data-kit-click="missing = $refs.inner == null ? 'null' : 'element'">inner from outer</button>
    <output id="found" data-kit-text="found"></output>
    <output id="missing" data-kit-text="missing"></output>
    <div id="nested" data-kit-scope="seen: 'unset'">
      <input id="inner-field" data-kit-ref="inner" value="inner">
      <button id="inner-outer" type="button" data-kit-click="seen = $refs.field == null ? 'null' : 'element'">outer from inner</button>
      <button id="inner-focus" type="button" data-kit-click="$refs.inner.focus()">inner focus</button>
      <output id="seen" data-kit-text="seen"></output>
    </div>
  </section>
  <script>
  window.__kitErrors = [];
  var __consoleError = console.error;
  console.error = function () { window.__kitErrors.push(Array.prototype.map.call(arguments, function (a) { return a && a.message || String(a); }).join(" ")); return __consoleError.apply(this, arguments); };
  </script>
  <script src="/kit.js"></script><script>
%s
__runStandaloneKitTest(async function () {
  var waitFor = __kitTestWaitFor;
  var assert = __kitTestAssert;
  var byId = function (id) { return document.getElementById(id); };
  var errors = function () { return window.__kitErrors.length ? " (runtime errors: " + window.__kitErrors.join(" | ") + ")" : ""; };
  await waitFor(function () { return byId("missing").textContent === "unset"; }, "scope did not render");

  byId("outer-focus").click();
  await waitFor(function () { return document.activeElement === byId("outer-field"); }, "$refs.field inside the scope should be the scope's own field, not the page's" + errors());

  byId("page-focus").click();
  await waitFor(function () { return document.activeElement === byId("page-field"); }, "$refs.field on the page should be the page-level field");

  byId("inner-focus").click();
  await waitFor(function () { return document.activeElement === byId("inner-field"); }, "a nested boundary reaches its own ref");

  byId("outer-read").click();
  await waitFor(function () { return byId("found").textContent === "outer"; }, "$refs.field.value should read the owned input");
  assert(byId("missing").textContent === "null", "a missing name is nullish, got " + byId("missing").textContent);

  byId("outer-inner").click();
  await waitFor(function () { return byId("missing").textContent === "null"; }, "a ref inside a nested boundary must not be visible from the outer one");

  byId("inner-outer").click();
  await waitFor(function () { return byId("seen").textContent === "null"; }, "an outer ref must not be visible from inside a nested boundary");
});
  </script>
</body></html>`, browserHarness)
