package javascript

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The terminal browser proof: one host with two files and one with a single
// file. The component shows the active panel and hides the rest, moves between
// files by click and by arrow keys with focus following, copies the file on
// screen — never a hidden one — and reports copied until its timer resets.
func TestBrowserTerminalComponentShowsOneFileAndCopiesIt(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping terminal component browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := composer.ComposeStandalone([]ComponentRef{{Name: "terminal", Version: "1.0.0"}}, false)
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/terminal.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(bundle.JavaScript)
		case "/terminal.html":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(terminalComponentDocument))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	runVanillaBrowser(t, browser, server.URL+"/terminal.html")
}

var terminalComponentDocument = fmt.Sprintf(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>KitJS terminal component</title></head><body>
  <figure id="files" data-kit-component="terminal@1.0.0" data-kit-scope="delay: 40">
    <div role="tablist">
      <button id="tab-html" type="button" role="tab" data-terminal-tab="html">page.html</button>
      <button id="tab-js" type="button" role="tab" data-terminal-tab="js">page.js</button>
      <button id="copy-files" type="button" data-terminal-copy>Copy</button>
      <output id="files-state" data-kit-text="copied ? 'copied' : 'idle'"></output>
      <output id="files-active" data-kit-text="active"></output>
    </div>
    <pre id="panel-html" data-terminal-panel="html">html-source</pre>
    <pre id="panel-js" data-terminal-panel="js">js-source</pre>
  </figure>
  <figure id="single" data-kit-component="terminal@1.0.0" data-kit-scope="delay: 40">
    <button id="copy-single" type="button" data-terminal-copy>Copy</button>
    <pre data-terminal-panel="shell">go run .</pre>
  </figure>
  <script>
  (function () {
    "use strict";
    globalThis.__clipboardCalls = [];
    var adapter = {
      writeText: function (value) { globalThis.__clipboardCalls.push(value); return Promise.resolve(); },
      readText: function () { return Promise.resolve(""); }
    };
    function expose(getter) {
      try { Object.defineProperty(navigator, "clipboard", { configurable: true, get: getter }); }
      catch (_) { Object.defineProperty(Object.getPrototypeOf(navigator), "clipboard", { configurable: true, get: getter }); }
    }
    expose(function () { return adapter; });
  })();
  </script>
  <script src="/terminal.js"></script><script>
%s
__runStandaloneKitTest(async function () {
  var waitFor = __kitTestWaitFor;
  var assert = __kitTestAssert;
  var html = document.getElementById("panel-html");
  var js = document.getElementById("panel-js");
  var tabHTML = document.getElementById("tab-html");
  var tabJS = document.getElementById("tab-js");
  function state() { return document.getElementById("files-state").textContent.trim(); }
  function last() { return globalThis.__clipboardCalls[globalThis.__clipboardCalls.length - 1]; }

  // The first panel is the active one when nothing was authored.
  await waitFor(function () { return html.hidden === false && js.hidden === true; }, "terminal did not show its first file only");
  assert(tabHTML.getAttribute("aria-selected") === "true" && tabJS.getAttribute("aria-selected") === "false", "tabs did not reflect the active file");
  assert(tabHTML.getAttribute("data-state") === "active" && html.getAttribute("data-state") === "active", "data-state did not mark the active parts");
  assert(document.getElementById("files-active").textContent.trim() === "html", "active was not published to bindings");

  // Click the other file: it shows, the first hides.
  tabJS.click();
  await waitFor(function () { return js.hidden === false && html.hidden === true; }, "clicking a tab did not switch the panel");
  assert(tabJS.getAttribute("tabindex") === "0" && tabHTML.getAttribute("tabindex") === "-1", "roving tabindex did not follow the active tab");

  // Copy copies the file on screen, not the hidden one.
  document.getElementById("copy-files").click();
  await waitFor(function () { return state() === "copied"; }, "copy did not flip to copied");
  assert(last() === "js-source", "copy took the hidden file instead of the one on screen: " + last());
  await waitFor(function () { return state() === "idle"; }, "copied did not reset after its delay");

  // Arrow keys move between files and focus follows; the ends wrap.
  tabJS.focus();
  tabJS.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowRight", bubbles: true, cancelable: true }));
  await waitFor(function () { return html.hidden === false && document.activeElement === tabHTML; }, "ArrowRight did not wrap to the first file with focus");
  tabHTML.dispatchEvent(new KeyboardEvent("keydown", { key: "End", bubbles: true, cancelable: true }));
  await waitFor(function () { return js.hidden === false && document.activeElement === tabJS; }, "End did not jump to the last file with focus");

  // A single-file terminal has no tabs: the one panel stays visible and copies.
  var single = document.querySelector("#single pre");
  assert(single.hidden === false, "a single file was hidden");
  document.getElementById("copy-single").click();
  await waitFor(function () { return last() === "go run ."; }, "single-file copy did not copy its panel");
});
  </script>
</body></html>`, browserHarness)
