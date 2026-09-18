package javascript

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The code editor browser proof: the gutter counts the lines; Tab indents at
// the caret and across selected lines, Shift+Tab outdents them; Enter keeps
// the indent and deepens it after an opening bracket, and opens a block
// between a pair; a typed bracket closes itself and wraps a selection;
// Backspace inside an empty pair removes both; line and column follow the
// caret; goTo() and set() answer a directive.
func TestBrowserCodeEditorIndentsAndPairs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping code editor component browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := composer.ComposeStandalone([]ComponentRef{{Name: "code-editor", Version: "1.0.0"}}, false)
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/code-editor.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(bundle.JavaScript)
		case "/code-editor.html":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(codeEditorComponentDocument))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	runVanillaBrowser(t, browser, server.URL+"/code-editor.html")
}

var codeEditorComponentDocument = fmt.Sprintf(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>KitJS code editor component</title><script>
window.__errs = [];
window.addEventListener("error", function (e) { window.__errs.push(String(e.error && e.error.message || e.message)); });
var __ce = console.error; console.error = function () { window.__errs.push(Array.prototype.map.call(arguments, String).join(" ")); return __ce.apply(this, arguments); };
</script></head><body>
  <div id="host" data-kit-component="code-editor@1.0.0" data-kit-scope="value: 'function a() {\n  return 1;\n}'">
    <div id="gutter" data-code-gutter><template data-kit-for="n of lines()" data-kit-key="n"><span data-kit-text="n"></span></template></div>
    <textarea id="field" data-code-input data-kit-model="value" rows="8" cols="40"></textarea>
    <button id="jump" type="button" data-kit-click="goTo(2)">line 2</button>
    <button id="replace" type="button" data-kit-click="set('x')">set</button>
    <output id="state" data-kit-text="count() + '|' + line + ':' + column + '|' + length()"></output>
  </div>
  <script src="/code-editor.js"></script><script>
%s
__runStandaloneKitTest(async function () {
  var waitFor = __kitTestWaitFor;
  var assert = __kitTestAssert;
  var field = document.getElementById("field");
  function state() { return document.getElementById("state").textContent.trim(); }
  function gutter() { return document.querySelectorAll("#gutter span").length; }
  function key(name, init) {
    var options = { key: name, bubbles: true, cancelable: true };
    if (init) for (var k in init) options[k] = init[k];
    field.dispatchEvent(new KeyboardEvent("keydown", options));
  }
  function caretAt(position, end) { field.focus(); field.setSelectionRange(position, end === undefined ? position : end); field.dispatchEvent(new Event("click", { bubbles: true })); }

  await waitFor(function () { return field.value === "function a() {\n  return 1;\n}" && gutter() === 3; }, "the seeded value did not reach the field and the gutter: " + JSON.stringify(field.value) + " " + gutter() + " " + window.__errs.join(";"));

  // Enter after the opening brace of line 1 keeps the indent and deepens it.
  caretAt(14);
  await waitFor(function () { return state() === "3|1:15|28"; }, "the caret was not tracked: " + state() + " sel=" + field.selectionStart + " active=" + (document.activeElement === field));
  key("Enter");
  await waitFor(function () { return field.value === "function a() {\n  \n  return 1;\n}" && gutter() === 4 && state() === "4|2:3|31"; }, "Enter did not deepen the indent: " + JSON.stringify(field.value) + " " + state());

  // Tab at the caret inserts two spaces; Shift+Tab takes them back.
  key("Tab");
  await waitFor(function () { return field.value === "function a() {\n    \n  return 1;\n}"; }, "Tab did not indent: " + JSON.stringify(field.value));
  key("Tab", { shiftKey: true });
  await waitFor(function () { return field.value === "function a() {\n  \n  return 1;\n}"; }, "Shift+Tab did not outdent: " + JSON.stringify(field.value));

  // Tab over a multi-line selection indents every line.
  caretAt(15, 29);
  key("Tab");
  await waitFor(function () { return field.value === "function a() {\n    \n    return 1;\n}"; }, "Tab over lines did not indent them all: " + JSON.stringify(field.value));
  assert(field.selectionStart === 17 && field.selectionEnd === 33, "the selection did not follow the indent: " + field.selectionStart + "-" + field.selectionEnd);
  key("Tab", { shiftKey: true });
  await waitFor(function () { return field.value === "function a() {\n  \n  return 1;\n}"; }, "Shift+Tab over lines did not outdent them all: " + JSON.stringify(field.value));

  // A typed bracket closes itself; Enter between the pair opens a block; Backspace inside an empty pair removes both.
  caretAt(17);
  key("(");
  await waitFor(function () { return field.value === "function a() {\n  ()\n  return 1;\n}" && field.selectionStart === 18; }, "( did not pair: " + JSON.stringify(field.value));
  key("Backspace");
  await waitFor(function () { return field.value === "function a() {\n  \n  return 1;\n}"; }, "Backspace did not remove the empty pair: " + JSON.stringify(field.value));
  key("{");
  await waitFor(function () { return field.value === "function a() {\n  {}\n  return 1;\n}"; }, "{ did not pair: " + JSON.stringify(field.value));
  key("Enter");
  await waitFor(function () { return field.value === "function a() {\n  {\n    \n  }\n  return 1;\n}" && field.selectionStart === 23; }, "Enter inside a pair did not open a block: " + JSON.stringify(field.value) + " @" + field.selectionStart);

  // A quote wraps a selection.
  var at = field.value.indexOf("return");
  caretAt(at, at + 6);
  key("'");
  await waitFor(function () { return field.value.indexOf("'return'") >= 0 && field.selectionStart === at + 1 && field.selectionEnd === at + 7; }, "a quote did not wrap the selection: " + JSON.stringify(field.value) + " " + field.selectionStart + "-" + field.selectionEnd);

  document.getElementById("jump").click();
  await waitFor(function () { return state().indexOf("|2:1|") > 0; }, "goTo(2) did not move the caret: " + state());
  document.getElementById("replace").click();
  await waitFor(function () { return field.value === "x" && gutter() === 1 && state() === "1|1:2|1"; }, "set() did not replace the value: " + JSON.stringify(field.value) + " " + state());
});
  </script>
</body></html>`, browserHarness)
