package javascript

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The editor 2.0.0 browser proof: markdown as you type turns "# " into a
// heading, "- " into a list and **bold** into bold on the space after it; the
// keyboard shortcuts run the block commands; link() keeps the selection while
// the URL is typed elsewhere and applyLink() puts it on, normalised; pasted
// HTML comes in clean; selecting and bubbleX/Y follow a selection; dirty
// follows the baseline; markdown() writes the document as a file would.
func TestBrowserEditorV2WritesLikeAnEditor(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping editor 2.0.0 browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := composer.ComposeStandalone([]ComponentRef{{Name: "editor", Version: "2.0.0"}}, false)
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
			_, _ = response.Write([]byte(editorV2ComponentDocument))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	runVanillaBrowser(t, browser, server.URL+"/editor.html")
}

var editorV2ComponentDocument = fmt.Sprintf(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>KitJS editor 2.0.0</title><script>
window.__errs = [];
window.addEventListener("error", function (e) { window.__errs.push(String(e.error && e.error.message || e.message)); });
var __ce = console.error; console.error = function () { window.__errs.push(Array.prototype.map.call(arguments, String).join(" ")); return __ce.apply(this, arguments); };
</script></head><body>
  <div id="host" data-kit-component="editor@2.0.0" style="position:relative;width:500px">
    <button id="bold" type="button" data-editor-command="bold" data-kit-bind="aria-pressed: isActive('bold');">B</button>
    <button id="apply" type="button" data-kit-click="applyLink('kitwork.io/docs')">apply</button>
    <button id="cancel" type="button" data-kit-click="cancelLink()">cancel</button>
    <button id="unlink" type="button" data-kit-click="unlink()">unlink</button>
    <button id="replace" type="button" data-kit-click="set('<h2>Title</h2><p>Some <strong>bold</strong> and <a href=&quot;https://x.y/&quot;>link</a></p><ul><li>one</li><li>two</li></ul><blockquote><p>quoted</p></blockquote>')">set</button>
    <div id="area" data-editor-area contenteditable="true" style="min-height:60px;padding:8px"><p>Hello world</p></div>
    <output id="state" data-kit-text="words + '|' + characters + '|' + (dirty ? 'dirty' : 'clean') + '|' + (selecting ? 'sel' : 'nosel') + '|' + (linking ? 'linking:' + href : 'idle') + '|' + linkHref()"></output>
    <output id="bubble" data-kit-text="bubbleX + ',' + bubbleY"></output>
    <pre id="md" data-kit-text="markdown()"></pre>
  </div>
  <script src="/editor.js"></script><script>
%s
__runStandaloneKitTest(async function () {
  var waitFor = __kitTestWaitFor;
  var assert = __kitTestAssert;
  var area = document.getElementById("area");
  function state() { return document.getElementById("state").textContent.trim(); }
  function html() { return area.innerHTML; }
  function caretEnd(node) {
    var range = document.createRange();
    range.selectNodeContents(node);
    range.collapse(false);
    var selection = window.getSelection();
    selection.removeAllRanges();
    selection.addRange(range);
  }
  function selectText(node, from, to) {
    var range = document.createRange();
    range.setStart(node, from);
    range.setEnd(node, to);
    var selection = window.getSelection();
    selection.removeAllRanges();
    selection.addRange(range);
  }
  function key(name, init) {
    var options = { key: name, bubbles: true, cancelable: true };
    if (init) for (var k in init) options[k] = init[k];
    var event = new KeyboardEvent("keydown", options);
    area.dispatchEvent(event);
    return event.defaultPrevented;
  }

  await waitFor(function () { return state() === "2|11|clean|nosel|idle|"; }, "the region was not read into the scope: " + state() + " " + window.__errs.join(";"));

  // Markdown as you type: "# " becomes a heading, "- " a list.
  area.focus();
  area.innerHTML = "<p>#</p>";
  caretEnd(area.firstChild);
  assert(key(" ") === true, "space after # was not taken by the rule");
  await waitFor(function () { return !!area.querySelector("h2") && area.textContent.indexOf("#") < 0; }, "# + space did not make a heading: " + html());
  area.innerHTML = "<p>-</p>";
  caretEnd(area.firstChild);
  key(" ");
  await waitFor(function () { return !!area.querySelector("ul li") && area.textContent.indexOf("-") < 0; }, "- + space did not make a list: " + html());

  // **bold** closes itself on the space after it, and the format is off again for what follows.
  area.innerHTML = "<p>say **hi**</p>";
  caretEnd(area.firstChild.firstChild);
  key(" ");
  await waitFor(function () { return /<(strong|b)>hi<\/(strong|b)>/.test(html()) && html().indexOf("*") < 0; }, "**hi** + space did not bold: " + html());
  assert(/say (<(strong|b)>hi<\/(strong|b)>)(&nbsp;| )/.test(html()), "the space after the bold went missing: " + html());
  await waitFor(function () { return state().indexOf("|dirty|") > 0; }, "editing did not mark the content dirty: " + state());

  // Shortcuts: Ctrl+Shift+8 is a bulleted list.
  area.innerHTML = "<p>item</p>";
  caretEnd(area.firstChild.firstChild);
  key("8", { ctrlKey: true, shiftKey: true });
  await waitFor(function () { return !!area.querySelector("ul li"); }, "Ctrl+Shift+8 did not make a list: " + html());

  // A link: select a word, Ctrl+K keeps it while the URL is typed, applyLink puts it on.
  area.innerHTML = "<p>read the docs today</p>";
  var textNode = area.firstChild.firstChild;
  selectText(textNode, 9, 13);
  assert(key("k", { ctrlKey: true }) === true, "Ctrl+K was not taken");
  await waitFor(function () { return state().indexOf("|linking:|") > 0; }, "Ctrl+K did not open the link flow: " + state());
  document.getElementById("apply").click();
  await waitFor(function () { return /<a href="https:\/\/kitwork\.io\/docs">docs<\/a>/.test(html()) && state().indexOf("|idle|") > 0; }, "applyLink did not link the kept selection with a normalised href: " + html() + " " + state());
  var link = area.querySelector("a");
  selectText(link.firstChild, 1, 1);
  await waitFor(function () { return state().slice(-"https://kitwork.io/docs".length) === "https://kitwork.io/docs"; }, "linkHref() did not read the link under the caret: " + state());
  document.getElementById("unlink").click();
  await waitFor(function () { return !area.querySelector("a") && area.textContent === "read the docs today"; }, "unlink did not remove the link: " + html());

  // Paste: styles, spans and scripts go; the semantics stay.
  area.innerHTML = "<p>before </p>";
  caretEnd(area.firstChild.firstChild);
  var transfer = new DataTransfer();
  transfer.setData("text/html", '<div style="color:red"><span class="x">Hello <b>there</b></span><script>alert(1)<\/script><a href="javascript:alert(2)">bad</a> <a href="kitwork.io">good</a></div>');
  transfer.setData("text/plain", "Hello there");
  area.dispatchEvent(new ClipboardEvent("paste", { clipboardData: transfer, bubbles: true, cancelable: true }));
  await waitFor(function () { return html().indexOf("Hello") >= 0 && /<strong>there<\/strong>/.test(html()); }, "paste did not insert clean HTML: " + html());
  assert(html().indexOf("style=") < 0 && html().indexOf("<span") < 0 && html().indexOf("<script") < 0 && html().indexOf("javascript:") < 0, "paste let something through: " + html());
  assert(/<a href="https:\/\/kitwork\.io">good<\/a>/.test(html()) && html().indexOf("bad") >= 0 && !/<a[^>]*>bad/.test(html()), "paste did not keep the good link and unwrap the bad one: " + html());

  // The bubble: a selection turns selecting on and places it; collapsing turns it off.
  area.innerHTML = "<p>select me please</p>";
  selectText(area.firstChild.firstChild, 7, 9);
  await waitFor(function () { return state().indexOf("|sel|") > 0; }, "a selection did not turn selecting on: " + state());
  var parts = document.getElementById("bubble").textContent.split(",");
  assert(Number(parts[0]) > 0 && Number(parts[1]) >= 0, "the bubble has no position: " + parts.join(","));
  selectText(area.firstChild.firstChild, 9, 9);
  await waitFor(function () { return state().indexOf("|nosel|") > 0; }, "collapsing did not turn selecting off: " + state());

  // set() is a clean baseline; markdown() writes it as a file would.
  document.getElementById("replace").click();
  await waitFor(function () { return state().indexOf("|clean|") > 0 && document.getElementById("md").textContent === "## Title\n\nSome **bold** and [link](https://x.y/)\n\n- one\n- two\n\n> quoted\n"; }, "set()/markdown() are off: " + state() + " " + JSON.stringify(document.getElementById("md").textContent));
});
  </script>
</body></html>`, browserHarness)
