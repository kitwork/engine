package javascript

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The copy component browser proof stubs navigator.clipboard so the sealed
// clipboard service resolves deterministically, then verifies the transient
// "copied" flag flips on success, resets on its own timer, forwards an explicit
// value, and stays idle when the write is denied.
func TestBrowserCopyComponentFlipsAndResets(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping copy component browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := composer.ComposeStandalone([]ComponentRef{{Name: "copy", Version: "1.0.0"}}, false)
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/copy.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(bundle.JavaScript)
		case "/copy.html":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(copyComponentDocument))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	runVanillaBrowser(t, browser, server.URL+"/copy.html")
}

var copyComponentDocument = fmt.Sprintf(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>KitJS copy component</title></head><body>
  <section data-kit-component="copy@1.0.0" data-kit-scope="text: 'npm i kitwork', delay: 40">
    <button id="copy-default" data-kit-click="copy()">Copy</button>
    <button id="copy-explicit" data-kit-click="copy('explicit-value')">Copy value</button>
    <output id="copy-state" data-kit-text="copied ? 'copied' : 'idle'"></output>
  </section>
  <section data-kit-component="copy@1.0.0" data-kit-scope="delay: 40">
    <button id="copy-source" type="button" data-copy-trigger>Copy source</button>
    <pre data-copy-source>alpha-source-block</pre>
  </section>
  <script>
  (function () {
    "use strict";
    globalThis.__clipboardCalls = [];
    globalThis.__clipboardMode = "success";
    var adapter = {
      writeText: function (value) {
        globalThis.__clipboardCalls.push(value);
        if (globalThis.__clipboardMode === "denied") {
          return Promise.reject(new DOMException("denied", "NotAllowedError"));
        }
        return Promise.resolve();
      },
      readText: function () { return Promise.resolve(""); }
    };
    function expose(getter) {
      try { Object.defineProperty(navigator, "clipboard", { configurable: true, get: getter }); }
      catch (_) { Object.defineProperty(Object.getPrototypeOf(navigator), "clipboard", { configurable: true, get: getter }); }
    }
    expose(function () { return adapter; });
  })();
  </script>
  <script src="/copy.js"></script><script>
%s
__runStandaloneKitTest(async function () {
  var waitFor = __kitTestWaitFor;
  var nextTurn = __kitTestNextTurn;
  var assert = __kitTestAssert;
  function state() { return document.getElementById("copy-state").textContent.trim(); }
  function last() { return globalThis.__clipboardCalls[globalThis.__clipboardCalls.length - 1]; }

  await waitFor(function () { return state() === "idle"; }, "copy did not start idle");

  document.getElementById("copy-default").click();
  await waitFor(function () { return state() === "copied"; }, "copy did not flip to copied");
  assert(last() === "npm i kitwork", "copy forwarded the wrong default text");
  await waitFor(function () { return state() === "idle"; }, "copy did not reset after its delay");

  document.getElementById("copy-explicit").click();
  await waitFor(function () { return state() === "copied"; }, "explicit copy did not flip");
  assert(last() === "explicit-value", "copy forwarded the wrong explicit text");
  await waitFor(function () { return state() === "idle"; }, "explicit copy did not reset");

  // copy() with no value/text copies the boundary's [data-copy-source] element.
  document.getElementById("copy-source").click();
  await waitFor(function () { return last() === "alpha-source-block"; }, "copy trigger did not copy the [data-copy-source] element");

  var before = globalThis.__clipboardCalls.length;
  globalThis.__clipboardMode = "denied";
  document.getElementById("copy-default").click();
  await waitFor(function () { return globalThis.__clipboardCalls.length === before + 1; }, "denied copy did not attempt a write");
  await nextTurn();
  await nextTurn();
  assert(state() === "idle", "denied copy still reported success");
});
  </script>
</body></html>`, browserHarness)
