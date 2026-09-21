package javascript

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The editor browser proof: html / empty / words mirror the region; a toolbar
// button wraps the selection without stealing it and reports pressed at the
// caret; a block command changes the paragraph; run() answers a directive;
// set() and clear() replace the content and the flags follow.
func TestBrowserEditorFormatsTheSelection(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping editor component browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := composer.ComposeStandalone([]ComponentRef{{Name: "editor", Version: "1.0.0"}}, false)
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/editor.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(bundle.JavaScript)
		case "/editor.html":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(editorComponentDocument))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	runVanillaBrowser(t, browser, server.URL+"/editor.html")
}

var editorComponentDocument = fmt.Sprintf(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>KitJS editor component</title><script>
window.__errs = [];
window.addEventListener("error", function (e) { window.__errs.push(String(e.error && e.error.message || e.message)); });
var __ce = console.error; console.error = function () { window.__errs.push(Array.prototype.map.call(arguments, String).join(" ")); return __ce.apply(this, arguments); };
</script></head><body>
  <div id="host" data-kit-component="editor@1.0.0">
    <button id="bold" type="button" data-editor-command="bold" data-kit-bind:aria-pressed="isActive('bold')">B</button>
    <button id="heading" type="button" data-editor-command="heading" data-kit-bind:aria-pressed="isActive('heading')">H</button>
    <button id="bullets" type="button" data-editor-command="bullets">•</button>
    <button id="quote" type="button" data-kit-click="run('quote')">Q</button>
    <button id="replace" type="button" data-kit-click="set('<p>Replaced text here</p>')">set</button>
    <button id="empty" type="button" data-kit-click="clear()">clear</button>
    <div id="area" data-editor-area contenteditable="true" style="min-height:40px"><p>Hello world</p></div>
    <p id="placeholder" data-kit-show="empty" hidden>Write something</p>
    <output id="state" data-kit-text="words + '|' + (empty ? 'empty' : 'full')"></output>
  </div>
  <script src="/editor.js"></script><script>
%s
__runStandaloneKitTest(async function () {
  var waitFor = __kitTestWaitFor;
  var assert = __kitTestAssert;
  var area = document.getElementById("area");
  function state() { return document.getElementById("state").textContent.trim(); }
  function selectWord(node, from, to) {
    var range = document.createRange();
    range.setStart(node, from);
    range.setEnd(node, to);
    var selection = window.getSelection();
    selection.removeAllRanges();
    selection.addRange(range);
  }

  await waitFor(function () { return state() === "2|full"; }, "the region was not read into the scope: " + state() + " " + window.__errs.join(";"));
  assert(document.getElementById("placeholder").hidden === true, "the placeholder showed over content");

  area.focus();
  selectWord(area.querySelector("p").firstChild, 0, 5);
  var bold = document.getElementById("bold");
  bold.dispatchEvent(new MouseEvent("mousedown", { bubbles: true, cancelable: true }));
  bold.click();
  await waitFor(function () { return /<(b|strong)>Hello<\/(b|strong)>/.test(area.innerHTML); }, "bold did not wrap the selection: " + area.innerHTML + " " + window.__errs.join(";"));
  await waitFor(function () { return bold.getAttribute("aria-pressed") === "true"; }, "the bold button did not report pressed at the caret");
  assert(state() === "2|full", "formatting changed the word count: " + state());

  document.getElementById("heading").click();
  await waitFor(function () { return /<h2>/.test(area.innerHTML) && document.getElementById("heading").getAttribute("aria-pressed") === "true"; }, "heading did not change the block: " + area.innerHTML);
  document.getElementById("quote").click();
  await waitFor(function () { return /<blockquote>/.test(area.innerHTML); }, "run('quote') from a directive did not apply: " + area.innerHTML);
  document.getElementById("bullets").click();
  await waitFor(function () { return /<ul>/.test(area.innerHTML) || /<li>/.test(area.innerHTML); }, "bullets did not make a list: " + area.innerHTML);

  document.getElementById("replace").click();
  await waitFor(function () { return area.innerHTML === "<p>Replaced text here</p>" && state() === "3|full"; }, "set() did not replace the content: " + area.innerHTML + " " + state());
  document.getElementById("empty").click();
  await waitFor(function () { return state() === "0|empty" && document.getElementById("placeholder").hidden === false; }, "clear() did not empty the region and show the placeholder: " + state());

  // Typing brings it back.
  area.focus();
  area.innerHTML = "<p>Typed</p>";
  area.dispatchEvent(new Event("input", { bubbles: true }));
  await waitFor(function () { return state() === "1|full"; }, "input did not resync: " + state());
});
  </script>
</body></html>`, browserHarness)
