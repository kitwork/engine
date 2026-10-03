package js

import (
	"strings"
	"testing"
)

func TestEditorIsShippedWhenUsed(t *testing.T) {
	out := deliver(`<div data-kit-component="editor"><div data-kit-element="area" contenteditable="true"></div></div>`)
	if !strings.Contains(out, `/kit/components/editor@`) {
		t.Fatalf("editor was not injected for markup that uses it:\n%s", out)
	}
	if !strings.Contains(ModulesJS([]string{"editor"}), `component("editor@v1.0.0"`) {
		t.Error("versioned registration missing")
	}
}

// A real contenteditable in a real browser: commands from the markup's own buttons, the pressed
// state following the selection, a link that refuses javascript:, plain-text paste, and the
// markdown-style line shortcuts.
func TestBrowserEditorEditsFromTheMarkupsButtons(t *testing.T) {
	runBrowserProof(t, "editor", []string{"editor"}, `
<div data-kit-component="editor" data-kit-alias="$post">
  <div role="toolbar" aria-label="Formatting">
    <button id="bold" data-kit-click="run('bold')" data-kit-bind:aria-pressed="isActive('bold') ? 'true' : 'false'">B</button>
    <button id="heading" data-kit-click="run('heading')" data-kit-bind:aria-pressed="isActive('heading') ? 'true' : 'false'">H</button>
    <button id="link" data-kit-click="beginLink()">Link</button>
  </div>
  <div id="area" data-kit-element="area" contenteditable="true" role="textbox" aria-multiline="true" aria-label="Body"><p id="first">hello world</p></div>
  <form id="linkform" data-kit-show="linking" hidden data-kit-submit:prevent="applyLink()">
    <input id="href" data-kit-model="href">
  </form>
  <span id="count" data-kit-text="words + ' words'"></span>
  <span id="empty" data-kit-text="empty ? 'empty' : 'has text'"></span>
</div>
<script>
(async function () {
  await wait(80);
  var area = document.getElementById("area");
  function select(node, from, to) { var r = document.createRange(); r.setStart(node, from); r.setEnd(node, to); var s = getSelection(); s.removeAllRanges(); s.addRange(r); document.dispatchEvent(new Event("selectionchange")); }
  area.focus();

  assert(document.getElementById("count").textContent === "2 words", "counts come from the text, got " + document.getElementById("count").textContent);

  // A toolbar press must not take focus from the text.
  var press = new MouseEvent("mousedown", { bubbles: true, cancelable: true });
  document.getElementById("bold").dispatchEvent(press);
  assert(press.defaultPrevented, "pressing a toolbar button keeps the selection in the text");

  // Bold from the markup's own button, and aria-pressed follows.
  select(document.getElementById("first").firstChild, 0, 5);
  document.getElementById("bold").click(); await wait(60);
  assert(/<(b|strong)>hello<\/(b|strong)>/.test(area.innerHTML), "run('bold') bolds the selection, got " + area.innerHTML);
  select(area.querySelector("b,strong").firstChild, 1, 2); await wait(60);
  assert(document.getElementById("bold").getAttribute("aria-pressed") === "true", "aria-pressed says bold where the caret is bold");

  // A block command, and its pressed state.
  document.getElementById("heading").click(); await wait(60);
  assert(area.querySelector("h2") && document.getElementById("heading").getAttribute("aria-pressed") === "true", "run('heading') makes the line a heading, got " + area.innerHTML);

  // A link: the selection survives typing the address elsewhere; javascript: is refused.
  var text = area.querySelector("h2").lastChild;
  select(text, 1, text.textContent.length);
  document.getElementById("link").click(); await wait(40);
  assert(!document.getElementById("linkform").hidden, "beginLink opens the address form");
  var href = document.getElementById("href");
  href.focus(); href.value = "javascript:alert(1)"; href.dispatchEvent(new Event("input", { bubbles: true }));
  document.getElementById("linkform").dispatchEvent(new Event("submit", { bubbles: true, cancelable: true })); await wait(60);
  assert(!area.querySelector("a"), "a javascript: address never becomes a link, got " + area.innerHTML);
  select(text, 1, text.textContent.length);
  document.getElementById("link").click(); await wait(40);
  href.focus(); href.value = "https://kitwork.io"; href.dispatchEvent(new Event("input", { bubbles: true }));
  document.getElementById("linkform").dispatchEvent(new Event("submit", { bubbles: true, cancelable: true })); await wait(60);
  var a = area.querySelector("a");
  assert(a && a.getAttribute("href") === "https://kitwork.io" && document.getElementById("linkform").hidden, "the remembered selection becomes the link, got " + area.innerHTML);

  // Paste arrives as plain text: no styles, no spans, no scripts.
  var p = document.createElement("p"); p.id = "second"; p.textContent = "x"; area.appendChild(p);
  select(p.firstChild, 1, 1);
  var data = new DataTransfer(); data.setData("text/plain", " pasted"); data.setData("text/html", '<span style="color:red">pasted</span><script>alert(1)<\/script>');
  area.dispatchEvent(new ClipboardEvent("paste", { clipboardData: data, bubbles: true, cancelable: true })); await wait(60);
  assert(p.textContent === "x pasted" && !area.querySelector("span, script"), "paste arrives as plain text, got " + p.innerHTML);

  // "# " at the start of a line becomes a heading.
  var q = document.createElement("p"); q.appendChild(document.createTextNode("# ")); area.appendChild(q);
  select(q.firstChild, 2, 2);
  area.dispatchEvent(new InputEvent("input", { bubbles: true, inputType: "insertText", data: " " })); await wait(60);
  assert(area.querySelectorAll("h2").length === 2, "a line started with '# ' becomes a heading, got " + area.innerHTML);

  // clear() empties it, and the page hears so.
  window.kit.run(window.kit.compile("$post.clear()")); await wait(40);
  assert(document.getElementById("empty").textContent === "empty" && document.getElementById("count").textContent === "0 words", "clear empties the text and the counts follow");

  root.setAttribute("data-kit-test", "passed");
})().catch(function (error) { var root = document.documentElement; if (!root.hasAttribute("data-kit-test")) { root.setAttribute("data-kit-test", "failed"); root.setAttribute("data-kit-test-error", String(error && error.message || error)); } });
</script>`)
}
