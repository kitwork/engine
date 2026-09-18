package javascript

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The command palette browser proof: the chord and the trigger open it with
// focus in the field, typing filters the authored rows by label and keywords,
// arrows move the highlight over the visible rows only, Enter runs the row as a
// click, Escape closes and hands focus back to the trigger.
func TestBrowserCommandPaletteFiltersAndRunsRows(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping command component browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := composer.ComposeStandalone([]ComponentRef{{Name: "command", Version: "1.0.0"}}, false)
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/command.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(bundle.JavaScript)
		case "/command.html":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(commandComponentDocument))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	runVanillaBrowser(t, browser, server.URL+"/command.html")
}

var commandComponentDocument = fmt.Sprintf(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>KitJS command component</title></head><body>
  <div id="palette" data-kit-component="command@1.0.0">
    <button id="trigger" type="button" data-command-trigger>Search</button>
    <output id="state" data-kit-text="(open ? 'open' : 'closed') + ':' + count"></output>
    <div id="panel" data-command-panel hidden>
      <input id="field" type="text" data-command-input>
      <button id="row-deploy" type="button" data-command-item data-command-keywords="ship release">Deploy site</button>
      <button id="row-logs" type="button" data-command-item>Open logs</button>
      <button id="row-off" type="button" data-command-item disabled>Disabled</button>
      <button id="row-theme" type="button" data-command-item>Toggle theme</button>
      <p id="empty" data-command-empty hidden>No match</p>
    </div>
  </div>
  <script src="/command.js"></script><script>
%s
__runStandaloneKitTest(async function () {
  var waitFor = __kitTestWaitFor;
  var assert = __kitTestAssert;
  var state = function () { return document.getElementById("state").textContent.trim(); };
  var field = document.getElementById("field");
  var panel = document.getElementById("panel");
  var visible = function () {
    return Array.prototype.filter.call(document.querySelectorAll("[data-command-item]"), function (row) { return !row.hidden; })
      .map(function (row) { return row.id.replace("row-", ""); }).join(",");
  };
  var active = function () {
    var row = document.querySelector('[data-command-item][data-state="active"]');
    return row ? row.id.replace("row-", "") : "";
  };
  function key(target, name, init) {
    var options = { key: name, bubbles: true, cancelable: true };
    for (var k in (init || {})) options[k] = init[k];
    target.dispatchEvent(new KeyboardEvent("keydown", options));
  }

  globalThis.__ran = ""; globalThis.__clicks = 0;
  ["deploy", "logs", "theme"].forEach(function (name) {
    document.getElementById("row-" + name).addEventListener("click", function () { globalThis.__ran = name; globalThis.__clicks++; });
  });

  await waitFor(function () { return state() === "closed:3"; }, "palette did not start closed with three usable rows counted");
  assert(panel.hidden === true, "the panel was not hidden at rest");

  // The chord opens it, focus lands in the field, the first usable row is active.
  key(document.body, "k", { ctrlKey: true });
  await waitFor(function () { return state() === "open:3" && document.activeElement === field; }, "Ctrl+K did not open with focus in the field");
  assert(active() === "deploy", "the first usable row was not active on open: " + active());
  assert(visible() === "deploy,logs,theme", "a disabled row was shown: " + visible());

  // Typing filters by label and keywords; the highlight follows the visible rows.
  field.value = "shi"; field.dispatchEvent(new Event("input", { bubbles: true }));
  await waitFor(function () { return visible() === "deploy"; }, "keywords did not match: " + visible());
  field.value = "o"; field.dispatchEvent(new Event("input", { bubbles: true }));
  await waitFor(function () { return visible() === "deploy,logs,theme"; }, "a common letter did not match every row: " + visible());
  key(field, "ArrowDown");
  await waitFor(function () { return active() === "logs"; }, "ArrowDown did not move the highlight");
  key(field, "ArrowDown"); key(field, "ArrowDown");
  await waitFor(function () { return active() === "deploy"; }, "the highlight did not wrap past the last visible row");
  field.value = "zzz"; field.dispatchEvent(new Event("input", { bubbles: true }));
  await waitFor(function () { return document.getElementById("empty").hidden === false && state() === "open:0"; }, "no match did not show the empty row");

  // Enter runs the highlighted row as a click and closes.
  field.value = "log"; field.dispatchEvent(new Event("input", { bubbles: true }));
  await waitFor(function () { return active() === "logs"; }, "the highlight did not land on the only match");
  key(field, "Enter");
  await waitFor(function () { return globalThis.__ran === "logs" && panel.hidden === true; }, "Enter did not run the row and close");
  assert(field.value === "", "the field kept its query after closing");

  // The trigger opens; Escape closes and returns focus to it.
  document.getElementById("trigger").click();
  await waitFor(function () { return panel.hidden === false && document.activeElement === field; }, "the trigger did not open with focus in the field");
  key(field, "Escape");
  await waitFor(function () { return panel.hidden === true && document.activeElement === document.getElementById("trigger"); }, "Escape did not close and restore the trigger");

  // A click on a row runs it once.
  globalThis.__ran = ""; globalThis.__clicks = 0;
  document.getElementById("trigger").click();
  await waitFor(function () { return panel.hidden === false; }, "reopen failed");
  document.getElementById("row-theme").click();
  await waitFor(function () { return globalThis.__ran === "theme" && panel.hidden === true; }, "a clicked row did not run and close");
  assert(globalThis.__clicks === 1, "a clicked row ran " + globalThis.__clicks + " times");
});
  </script>
</body></html>`, browserHarness)
