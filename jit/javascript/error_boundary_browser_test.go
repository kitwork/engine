package javascript

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// data-kit-error is an error boundary (ideaship-final §2): the nearest ancestor carrying it runs its
// action with $error — cause, message, directive, element — when an action or binding inside it
// fails. An error does not travel past the first boundary; an error nowhere near a boundary still
// reaches the console; a boundary whose own handler throws does not loop.
func TestBrowserErrorBoundaryCatchesTheNearestFailure(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping error boundary browser contract in short mode")
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
		case "/error.html":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(errorBoundaryDocument))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	runVanillaBrowser(t, browser, server.URL+"/error.html")
}

var errorBoundaryDocument = fmt.Sprintf(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>KitJS error boundary</title></head><body>
  <script>
  window.__consoleErrors = [];
  var __consoleError = console.error;
  console.error = function () { window.__consoleErrors.push(Array.prototype.map.call(arguments, function (a) { return a && a.message || String(a); }).join(" ")); return __consoleError.apply(this, arguments); };
  </script>
  <section id="outer" data-kit-scope="caught: '', where: '', on: '', inner: '', phase: 'boot', hits: 0"
           data-kit-error="caught = $error.message; where = $error.directive; on = $error.element.id; phase = 'caught'; hits = hits + 1">
    <button id="bad-action" type="button" data-kit-click="missingField = 1">bad action</button>
    <button id="poke" type="button" data-kit-click="phase = 'later'">poke</button>
    <output id="bad-binding" data-kit-text="phase === 'boot' ? explode() : 'fine'"></output>
    <div id="nested" data-kit-scope="innerCaught: ''" data-kit-error="innerCaught = $error.message">
      <button id="nested-bad" type="button" data-kit-click="alsoMissing = 1">nested bad</button>
      <output id="inner-out" data-kit-text="innerCaught"></output>
    </div>
    <div id="throwing" data-kit-scope="looped: 0, seen: 0" data-kit-error="seen = seen + 1; stillMissing = 1">
      <button id="throwing-bad" type="button" data-kit-click="gone = 1">handler throws</button>
      <output id="looped" data-kit-text="seen"></output>
    </div>
    <output id="caught" data-kit-text="caught"></output>
    <output id="where" data-kit-text="where"></output>
    <output id="on" data-kit-text="on"></output>
    <output id="phase" data-kit-text="phase"></output>
    <output id="hits" data-kit-text="hits"></output>
  </section>
  <section id="free" data-kit-scope="y: 0">
    <button id="free-bad" type="button" data-kit-click="nowhere = 1">no boundary</button>
  </section>
  <script src="/kit.js"></script><script>
%s
__runStandaloneKitTest(async function () {
  var waitFor = __kitTestWaitFor;
  var assert = __kitTestAssert;
  var byId = function (id) { return document.getElementById(id); };
  var text = function (id) { return byId(id).textContent; };

  // A binding that fails while rendering reaches the boundary first (bad-binding renders at boot).
  await waitFor(function () { return text("where") === "data-kit-text"; }, "a failing binding should reach the boundary, where = " + JSON.stringify(text("where")) + " (console: " + window.__consoleErrors.join(" | ") + "; bad-binding text: " + JSON.stringify(text("bad-binding")) + "; caught: " + JSON.stringify(text("caught")) + "; outer scope: " + (window.kit && kit.scopeFor ? "?" : "n/a") + ")");
  assert(text("on") === "bad-binding", "$error.element should be the failing element, got " + text("on"));
  assert(/callable|explode/.test(text("caught")), "$error.message should carry the cause, got " + text("caught"));
  await waitFor(function () { return text("bad-binding") === "fine"; }, "the boundary's own writes during the boot render must schedule the re-render that lets the binding recover, got " + JSON.stringify(text("bad-binding")));
  var settled = text("bad-binding");
  byId("poke").click();
  await waitFor(function () { return text("bad-binding") === "fine"; }, "after a later state change the binding renders again (phase=" + text("phase") + ")");
  assert(settled === "fine", "the binding should have recovered before any later change");

  byId("bad-action").click();
  await waitFor(function () { return text("where") === "data-kit-click" && text("on") === "bad-action"; }, "a failing action should reach the boundary with its directive and element, got " + text("where") + "/" + text("on"));
  assert(/missingField/.test(text("caught")), "$error.message should name the missing field, got " + text("caught"));

  var outerBefore = text("caught");
  byId("nested-bad").click();
  await waitFor(function () { return /alsoMissing/.test(text("inner-out")); }, "the nearest boundary should catch, got inner = " + JSON.stringify(text("inner-out")));
  assert(text("caught") === outerBefore, "an error must not travel past the first boundary (outer caught = " + text("caught") + ")");

  // A boundary whose own handler throws: the handler's failure goes to the console (its writes roll
  // back with the failed action), it is not re-entered, and the outer boundary does not see it.
  var consoleBefore = window.__consoleErrors.length;
  outerBefore = text("caught");
  byId("throwing-bad").click();
  await waitFor(function () { return window.__consoleErrors.length > consoleBefore; }, "the handler's own failure should reach the console");
  var since = window.__consoleErrors.slice(consoleBefore).join(" | ");
  assert(/stillMissing/.test(since) && /gone/.test(since), "the console should carry the handler's own error and the original one, got " + since);
  assert(text("caught") === outerBefore, "a failing handler must not hand its error to the outer boundary");

  consoleBefore = window.__consoleErrors.length;
  byId("free-bad").click();
  await waitFor(function () { return window.__consoleErrors.length > consoleBefore; }, "an error with no boundary above it still reaches the console");
});
  </script>
</body></html>`, browserHarness)
