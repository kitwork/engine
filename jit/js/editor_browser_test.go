package js

import (
	"bytes"
	"context"
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kitwork/engine/utilities/richtext"
)

// The editor of Kitwork UI (components/editor/v2.0.0.js) in a real browser, on a page written by hand
// the way the .ui engine writes <editor> (ui/editor.go): the host with its first state, a toolbar of
// format and command buttons, the link's popover, the writing area painted with the cleaned value and
// no contenteditable, and the field the form posts right after it.
//
// The browser's own typing is played by the page: a beforeinput the component may cancel, and only
// when it does not, document.execCommand doing what the browser would (the test's hand, never the
// component's), which fires the input event the browser fires.

// editorWire is one editor as ui/editor.go writes it, holding value (already in the grammar) and
// carrying the attributes extra on its field (required, readonly…). scope is its first state.
func editorWire(value, scope, extra string) string {
	button := func(id, tab, kind, name, call, more string) string {
		attr := `data-kitwork-command="` + name + `"`
		if kind == "format" {
			attr = `data-kitwork-format="` + name + `" aria-pressed="false"`
		}
		return `<button type="button" id="` + id + `" tabindex="` + tab + `" ` + attr + more + ` data-kitwork-event="click: ` + call + `">` + id + `</button>`
	}
	return `<label id="label">Nội dung</label>
<div class="editor" id="host" data-kitwork-component="editor: $body" data-kitwork-scope="` + scope + `">
<div id="toolbar" class="editor-toolbar" role="toolbar" aria-label="Định dạng" aria-controls="area">` +
		button("bold", "0", "format", "bold", "toggle('bold')", ` aria-keyshortcuts="Control+B"`) +
		button("italic", "-1", "format", "italic", "toggle('italic')", ` aria-keyshortcuts="Control+I"`) +
		button("h2", "-1", "format", "heading-2", "toggle('heading', 2)", "") +
		button("h3", "-1", "format", "heading-3", "toggle('heading', 3)", "") +
		button("quote", "-1", "format", "quote", "toggle('quote')", "") +
		button("list", "-1", "format", "list", "toggle('list')", "") +
		button("ordered", "-1", "format", "list-ordered", "toggle('list', 'ordered')", "") +
		button("link", "-1", "command", "link", "link()", ` aria-keyshortcuts="Control+K" aria-controls="pop" aria-haspopup="dialog" data-kitwork-bind="aria-expanded: open" aria-disabled="true"`) +
		button("undo", "-1", "command", "undo", "undo()", ` aria-keyshortcuts="Control+Z" aria-disabled="true"`) +
		button("redo", "-1", "command", "redo", "redo()", ` aria-keyshortcuts="Control+Shift+Z" aria-disabled="true"`) + `
</div>
<div id="pop" class="popover editor-popover" role="dialog" aria-label="Địa chỉ liên kết" hidden data-kitwork-bind="show: open">
<input id="address" class="editor-address" type="text" inputmode="url" autocomplete="off" autocapitalize="none" spellcheck="false" aria-label="Địa chỉ liên kết" data-kitwork-bind="value: address">` +
		button("apply", "-1", "command", "link", "link(address)", "") +
		button("unlink", "-1", "command", "unlink", "unlink()", "") +
		button("cancel", "-1", "command", "close", "close()", "") + `
<p id="error" class="error editor-address-error" hidden>Chỉ nhận https://, http://, mailto:, tel: hay đường dẫn bắt đầu bằng /.</p>
</div>
<div id="area" class="richtext editor-area" role="textbox" aria-multiline="true" aria-labelledby="label" aria-placeholder="Viết bài ở đây…" aria-required="true" aria-readonly="true" translate="no" spellcheck="true" data-kitwork-name="body">` + value + `</div>
<textarea id="field" class="editor-field" name="body"` + extra + `>` + html.EscapeString(value) + `</textarea>
</div>`
}

// editorStyle is the part of ui/stylesheet/editor.go the proofs need: the field drawn as nothing under
// the area, never display:none.
const editorStyle = `<style>.editor{position:relative}.editor-area{min-height:3em}
.editor-field{position:absolute;inset-block-end:0;inset-inline-start:0;inline-size:1px;block-size:1px;overflow:hidden;clip-path:inset(50%);resize:none}</style>`

// editorScript is what every editor proof starts with: the page's hands on the editor.
const editorScript = `
var area = document.getElementById("area"), field = document.getElementById("field");
function $(id) { return document.getElementById(id); }
function select(a, from, b, to) { getSelection().setBaseAndExtent(a, from, b === undefined ? a : b, to === undefined ? from : to); document.dispatchEvent(new Event("selectionchange")); }
function text(node) { while (node && node.nodeType !== 3) node = node.firstChild; return node; }
function find(word) {
  var walker = document.createTreeWalker(area, NodeFilter.SHOW_TEXT);
  while (walker.nextNode()) { var at = walker.currentNode.data.indexOf(word); if (at >= 0) return [walker.currentNode, at]; }
  throw new Error("no " + JSON.stringify(word) + " in " + area.innerHTML);
}
function selectWord(word) { var at = find(word); select(at[0], at[1], at[0], at[1] + word.length); }
function caretAt(word, after) { var at = find(word); select(at[0], at[1] + (after ? word.length : 0)); }
function beforeinput(type, extra) {
  var init = { inputType: type, bubbles: true, cancelable: true };
  for (var k in (extra || {})) init[k] = extra[k];
  var event = new InputEvent("beforeinput", init);
  area.dispatchEvent(event);
  return event;
}
// type is the browser typing: the component may cancel it; otherwise the browser's own insertText
function type(words) {
  var event = beforeinput("insertText", { data: words });
  if (!event.defaultPrevented) document.execCommand("insertText", false, words);
  return event;
}
function backspace() {
  var event = beforeinput("deleteContentBackward");
  if (!event.defaultPrevented) document.execCommand("delete", false);
  return event;
}
function press(button) {
  var down = new PointerEvent("pointerdown", { bubbles: true, cancelable: true }), mouse = new MouseEvent("mousedown", { bubbles: true, cancelable: true });
  button.dispatchEvent(down); button.dispatchEvent(mouse);
  assert(down.defaultPrevented && mouse.defaultPrevented, "a press on " + button.id + " keeps the focus in the area");
  button.click();
}
function pasted(data) {
  var transfer = new DataTransfer();
  for (var k in data) transfer.setData(k, data[k]);
  var event = new ClipboardEvent("paste", { clipboardData: transfer, bubbles: true, cancelable: true });
  area.dispatchEvent(event);
  return event;
}
function pressed(id) { return $(id).getAttribute("aria-pressed"); }
`

// runEditorProof is runBrowserProof with the editor's style and hands, and the file a paste is read by
// already loaded (editorPastePart): a paste is done as it happens, as on a page that pasted before.
func runEditorProof(t *testing.T, label, body string) {
	t.Helper()
	body = strings.Replace(body, "<script>", "<script>"+editorScript, 1)
	runBrowserProof(t, label, nil, editorStyle+editorPastePart()+body)
}

// editorPastePart is the tag of the file the editor reads a paste and a drop with (parts/editor-paste.js),
// which the editor itself loads at a page's first paste or drop (TestBrowserEditorLoadsItsPastePart).
func editorPastePart() string {
	return `<script src="` + PartAsset("editor-paste").Path + `"></script>`
}

// runBrowserDump serves a page as runBrowserProof does and returns the browser's DOM once its script
// has set data-kit-test: a proof whose results the Go side reads (richtext.Clean computed here).
func runBrowserDump(t *testing.T, label, body string) string {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping " + label + " browser proof in short mode")
	}
	browser := findHeadlessBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	body = strings.Replace(body, "<script>", "<script>"+editorScript, 1)
	page := "<!doctype html><html><head></head><body>" + editorStyle + editorPastePart() + body + "</body></html>"
	page = strings.Replace(page, "<script>", "<script>"+proofScriptHead, 1)
	delivered := deliverTestPage(t, page, nil)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if delivered.serveFiles(response, request) {
			return
		}
		if request.URL.Path != "/" {
			http.NotFound(response, request)
			return
		}
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = response.Write([]byte(delivered.html))
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	args := []string{"--headless=new", "--disable-gpu", "--no-sandbox", "--disable-dev-shm-usage", "--disable-background-networking",
		"--disable-default-apps", "--disable-extensions", "--disable-sync", "--metrics-recording-only", "--no-first-run",
		"--window-size=1000,800", "--run-all-compositor-stages-before-draw", "--user-data-dir=" + filepath.Join(t.TempDir(), "p"),
		"--virtual-time-budget=20000", "--dump-dom", server.URL + "/"}
	if runtime.GOOS == "windows" {
		args = append([]string{"--disable-background-mode", "--disable-breakpad", "--disable-crashpad-for-testing"}, args...)
	}
	output, err := exec.CommandContext(ctx, browser, args...).CombinedOutput()
	if i := bytes.Index(output, []byte("data-kit-test-error=")); i >= 0 {
		end := min(i+400, len(output))
		t.Fatalf("%s proof failed: %s", label, output[i:end])
	}
	if !bytes.Contains(output, []byte(`data-kit-test="passed"`)) {
		t.Fatalf("%s proof did not pass (err=%v):\n%.2000s", label, err, output)
	}
	return string(output)
}

// The first paint and the start: the area turned on, the field out of the way, the toolbar one stop
// for Tab, the counts the server painted kept.
// CONTROL: against a copy of v2.0.0.js that never set contenteditable, "the area is turned on at
// ready" failed.
func TestBrowserEditorStarts(t *testing.T) {
	runEditorProof(t, "editor start", `<form id="form" method="post">`+editorWire("<p>hello world</p>", "empty: false; words: 2; characters: 11; unsaved: false; open: false; address: ''", " required")+`
<b id="words" data-kitwork-bind="text: $body.words"></b><b id="unsaved" data-kitwork-bind="show: $body.unsaved" hidden>Chưa lưu</b></form>
<script>
(async function () {
  await wait(80);
  assert(area.getAttribute("contenteditable") === "true" && !area.hasAttribute("aria-readonly"), "the area is turned on at ready, got " + area.outerHTML.slice(0, 300));
  assert(field.tabIndex === -1 && field.getAttribute("aria-hidden") === "true", "the field is out of the way");
  assert($("words").textContent === "2" && $("unsaved").hidden, "the server's counts stand, got " + $("words").textContent);
  assert(field.value === "<p>hello world</p>", "the field holds the value, got " + field.value);
  root.setAttribute("data-kit-test", "passed");
})().catch(function (error) { var root = document.documentElement; if (!root.hasAttribute("data-kit-test")) { root.setAttribute("data-kit-test", "failed"); root.setAttribute("data-kit-test-error", String(error && error.stack || error)); } });
</script>`)
}

// editorProof is the shape every editor proof's script takes: its steps, then passed, or the failure
// with its reason on <html>.
func editorProof(steps string) string {
	return `<script>
(async function () {
  await wait(80);
` + steps + `
  root.setAttribute("data-kit-test", "passed");
})().catch(function (error) { var root = document.documentElement; if (!root.hasAttribute("data-kit-test")) { root.setAttribute("data-kit-test", "failed"); root.setAttribute("data-kit-test-error", String(error && error.stack || error)); } });
</script>`
}

// startScope is the first state of an editor holding value, as the server writes it.
func startScope(empty bool, words, characters int) string {
	return "empty: " + boolWord(empty) + "; words: " + itoa(words) + "; characters: " + itoa(characters) + "; unsaved: false; open: false; address: ''"
}

func boolWord(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for ; n > 0; n /= 10 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
	}
	return string(digits)
}

// Bold and italic from the toolbar: on a selection, off again when all of it has it, across lines, on
// part of a run; with a caret, for the next words typed; aria-pressed follows the caret.
// CONTROL: against a copy of v2.0.0.js whose mark() wrapped nothing, "bold on a selection" failed.
func TestBrowserEditorMarks(t *testing.T) {
	runEditorProof(t, "editor marks", editorWire("<p>hello world</p><p>second line</p>", startScope(false, 4, 23), "")+editorProof(`
  selectWord("hello"); press($("bold"));
  assert(area.innerHTML === "<p><strong>hello</strong> world</p><p>second line</p>", "bold on a selection, got " + area.innerHTML);
  assert(getSelection().toString() === "hello" && pressed("bold") === "true", "the selection stays and bold is pressed, got " + getSelection().toString() + " / " + pressed("bold"));
  assert(field.value === "<p><strong>hello</strong> world</p>\n<p>second line</p>", "the field follows, got " + field.value);
  press($("bold"));
  assert(area.innerHTML === "<p>hello world</p><p>second line</p>" && pressed("bold") === "false", "bold again on all-bold takes it off, got " + area.innerHTML);

  var a = find("world"), b = find("second"); select(a[0], a[1], b[0], b[1] + 6); press($("bold"));
  assert(field.value === "<p>hello <strong>world</strong></p>\n<p><strong>second</strong> line</p>", "bold across two lines, got " + field.value);
  selectWord("orl"); press($("bold"));
  assert(field.value === "<p>hello <strong>w</strong>orl<strong>d</strong></p>\n<p><strong>second</strong> line</p>", "off on part of a run splits it, got " + field.value);
  selectWord("hello"); press($("italic"));
  assert(field.value === "<p><em>hello</em> <strong>w</strong>orl<strong>d</strong></p>\n<p><strong>second</strong> line</p>", "italic, got " + field.value);
  var s = find("second"); select(s[0], 2); 
  assert(pressed("bold") === "true" && pressed("italic") === "false", "aria-pressed follows the caret into bold, got " + pressed("bold") + pressed("italic"));
  caretAt("line", true);
  assert(pressed("bold") === "false", "and out of it");

  // a caret: the mark is for what is typed next
  press($("bold")); type("X");
  assert(field.value.endsWith("<p><strong>second</strong> line<strong>X</strong></p>"), "bold at a caret marks what is typed next, got " + field.value);
  assert(pressed("bold") === "true", "bold is pressed while typing in it");
  press($("bold")); type("Y");
  assert(field.value.endsWith("<p><strong>second</strong> line<strong>X</strong>Y</p>"), "bold off at a caret: what is typed next is plain, got " + field.value);
  press($("italic")); press($("bold")); type("Z");
  assert(field.value.endsWith("Y<strong><em>Z</em></strong></p>"), "two marks at a caret, got " + field.value);
`))
}

// The blocks: a heading on and off, a list made of two lines and switched to numbers, an item taken out
// into a quote, a quote joined by the line after it, a heading out of a quote; the caret stays.
// CONTROL: against a copy of v2.0.0.js whose block() joined no container, "a quote beside a quote is
// one" failed (two blockquotes).
func TestBrowserEditorBlocks(t *testing.T) {
	runEditorProof(t, "editor blocks", editorWire("<p>one</p><p>two</p><p>three</p>", startScope(false, 3, 11), "")+editorProof(`
  caretAt("one"); press($("h2"));
  assert(area.innerHTML === "<h2>one</h2><p>two</p><p>three</p>" && pressed("h2") === "true", "a heading, got " + area.innerHTML);
  assert(getSelection().anchorNode === find("one")[0], "the caret stays in its words");
  press($("h2"));
  assert(area.innerHTML === "<p>one</p><p>two</p><p>three</p>", "the heading again is a paragraph, got " + area.innerHTML);
  var a = find("one"), b = find("two"); select(a[0], 1, b[0], 1); press($("list"));
  assert(area.innerHTML === "<ul><li>one</li><li>two</li></ul><p>three</p>" && pressed("list") === "true", "a list of two lines, got " + area.innerHTML);
  press($("ordered"));
  assert(area.innerHTML === "<ol><li>one</li><li>two</li></ol><p>three</p>" && pressed("ordered") === "true" && pressed("list") === "false", "numbered, got " + area.innerHTML);
  caretAt("two"); press($("quote"));
  assert(area.innerHTML === "<ol><li>one</li></ol><blockquote><p>two</p></blockquote><p>three</p>", "an item taken out into a quote, got " + area.innerHTML);
  caretAt("three"); press($("quote"));
  assert(area.innerHTML === "<ol><li>one</li></ol><blockquote><p>two</p><p>three</p></blockquote>", "a quote beside a quote is one, got " + area.innerHTML);
  caretAt("two"); press($("h3"));
  assert(area.innerHTML === "<ol><li>one</li></ol><h3>two</h3><blockquote><p>three</p></blockquote>", "a heading out of a quote, got " + area.innerHTML);
  caretAt("three"); press($("quote"));
  assert(area.innerHTML === "<ol><li>one</li></ol><h3>two</h3><p>three</p>", "quote off: a paragraph, got " + area.innerHTML);
  assert(field.value === "<ol>\n<li>one</li>\n</ol>\n<h3>two</h3>\n<p>three</p>", "the field holds the grammar, got " + field.value);
`))
}

// Enter, Shift+Enter and Backspace arrive as beforeinput (an Android keyboard sends key 229, never
// Enter): a new line split at the caret, a paragraph after a heading, an empty item or quote paragraph
// left, a break; Backspace at the start of a heading, an item or a quote makes a paragraph first.
// CONTROL: against a copy of v2.0.0.js whose enter() never left a list, "Enter on an empty item leaves
// the list" failed.
func TestBrowserEditorEnterAndBackspace(t *testing.T) {
	runEditorProof(t, "editor enter", editorWire("<p>hello world</p><h2>Title</h2><ul><li>item</li></ul><blockquote><p>said</p></blockquote>", startScope(false, 5, 25), "")+editorProof(`
  caretAt("world");
  assert(beforeinput("insertParagraph").defaultPrevented, "Enter is the editor's");
  assert(area.innerHTML.indexOf("<p>hello </p><p>world</p>") === 0, "a line split at the caret, got " + area.innerHTML);
  type("X");
  assert(area.innerHTML.indexOf("<p>hello </p><p>Xworld</p>") === 0, "the caret is at the start of the new line, got " + area.innerHTML);

  caretAt("Title", true); beforeinput("insertParagraph"); type("after");
  assert(area.innerHTML.indexOf("<h2>Title</h2><p>after</p>") > 0, "after a heading, a paragraph, got " + area.innerHTML);

  caretAt("item", true); beforeinput("insertParagraph");
  assert(area.innerHTML.indexOf("<ul><li>item</li><li><br></li></ul>") > 0, "Enter in a list makes an item, got " + area.innerHTML);
  beforeinput("insertParagraph");
  assert(area.innerHTML.indexOf("<ul><li>item</li></ul><p><br></p>") > 0, "Enter on an empty item leaves the list, got " + area.innerHTML);
  type("out");
  assert(area.innerHTML.indexOf("</ul><p>out</p>") > 0, "typing goes on there, got " + area.innerHTML);

  caretAt("said", true); beforeinput("insertParagraph"); beforeinput("insertParagraph");
  assert(area.innerHTML.endsWith("<blockquote><p>said</p></blockquote><p><br></p>"), "Enter twice leaves the quote, got " + area.innerHTML);

  caretAt("hello", true);
  assert(beforeinput("insertLineBreak").defaultPrevented, "Shift+Enter is the editor's");
  type("Z");
  assert(field.value.indexOf("<p>hello<br>Z</p>") === 0, "a line break, got " + field.value + " / " + area.innerHTML);

  caretAt("Title");
  assert(backspace().defaultPrevented && area.innerHTML.indexOf("<p>Title</p>") > 0, "Backspace at the start of a heading makes it a paragraph, got " + area.innerHTML);
  caretAt("item");
  assert(backspace().defaultPrevented && area.innerHTML.indexOf("<p>item</p>") > 0 && area.innerHTML.indexOf("<ul>") < 0, "and of an item, out of its list, got " + area.innerHTML);
  caretAt("said");
  assert(backspace().defaultPrevented && area.innerHTML.indexOf("<blockquote>") < 0, "and of a quote, got " + area.innerHTML);
  caretAt("said", true);
  assert(!backspace().defaultPrevented && field.value.indexOf("<p>sai</p>") > 0, "anywhere else Backspace is the browser's, got " + field.value);

  // Enter over a selection across two lines: what is selected goes, the line split there
  var a = find("Xworld"), b = find("after"); select(a[0], 3, b[0], 2); beforeinput("insertParagraph");
  assert(field.value.indexOf("<p>Xwo</p>\n<p>ter</p>") >= 0, "Enter over two lines, got " + field.value);
`))
}

// The normaliser, after an input the browser made: a <b> it typed into is <strong>, a span with a style
// is unwrapped, a div is a paragraph, text left on the area goes into a paragraph, attributes go — and
// the caret stays in the very text node it was in. During a composition it does nothing.
// CONTROL: against a copy of v2.0.0.js whose shape() renamed no mark, "back in the grammar" failed
// with the browser's <b> still there.
func TestBrowserEditorNormaliser(t *testing.T) {
	runEditorProof(t, "editor normaliser", editorWire("<p>one</p>", startScope(false, 1, 3), "")+editorProof(`
  var p = area.firstChild, made = function (html) { var holder = document.createElement("div"); holder.innerHTML = html; return holder.firstChild; };
  // what a browser may leave after its own typing and deleting
  p.appendChild(made('<b style="font-weight:bold">bold</b>'));
  p.appendChild(made('<span style="font-size:24px">span</span>'));
  p.appendChild(made('<font color="red">font</font>'));
  area.appendChild(made("<div>block</div>"));
  area.appendChild(document.createTextNode("loose"));
  var typed = find("span")[0]; select(typed, 2);
  area.dispatchEvent(new InputEvent("input", { inputType: "insertText", data: "x", bubbles: true }));
  assert(area.innerHTML === "<p>one<strong>bold</strong>spanfont</p><p>block</p><p>loose</p>", "back in the grammar, got " + area.innerHTML);
  assert(getSelection().anchorNode === typed && getSelection().anchorOffset === 2, "the caret is where it was, in the same text node");
  assert(field.value === "<p>one<strong>bold</strong>spanfont</p>\n<p>block</p>\n<p>loose</p>", "the field, got " + field.value);
  // a list holds items, a quote paragraphs, a line no block
  area.appendChild(made("<ul><p>para</p>stray</ul>"));
  area.appendChild(made("<blockquote>quoted<h2>head</h2></blockquote>"));
  area.firstChild.appendChild(made("<p>inner</p>"));
  area.appendChild(made('<p><a href="javascript:alert(1)">bad</a> <a href="https://kitwork.io" onclick="x()">good</a></p>'));
  area.dispatchEvent(new InputEvent("input", { inputType: "deleteContentBackward", bubbles: true }));
  assert(field.value === '<p>one<strong>bold</strong>spanfont<br>inner</p>\n<p>block</p>\n<p>loose</p>\n<ul>\n<li>para</li>\n<li>stray</li>\n</ul>\n<blockquote>\n<p>quoted</p>\n<p>head</p>\n</blockquote>\n<p>bad <a href="https://kitwork.io">good</a></p>', "structure, got " + field.value);
  assert(!area.querySelector("[onclick],[style]") && area.querySelectorAll("a").length === 1, "attributes and a javascript: link gone, got " + area.innerHTML);

  // during a composition nothing is touched
  area.dispatchEvent(new CompositionEvent("compositionstart", { data: "" }));
  area.firstChild.appendChild(made("<span>ime</span>"));
  var during = area.innerHTML, value = field.value;
  area.dispatchEvent(new InputEvent("input", { inputType: "insertCompositionText", data: "ime", isComposing: true, bubbles: true }));
  assert(area.innerHTML === during && field.value === value, "during a composition the DOM and the field stay, got " + area.innerHTML);
  area.dispatchEvent(new CompositionEvent("compositionend", { data: "ime" }));
  assert(area.innerHTML.indexOf("<span>") < 0 && field.value.indexOf("inner</p>") < 0 && field.value.indexOf("innerime</p>") > 0, "at its end it is read, got " + field.value);

  // everything deleted: the area is empty again, for its placeholder (:empty)
  area.innerHTML = "<p><br></p>"; select(area.firstChild, 0);
  area.dispatchEvent(new InputEvent("input", { inputType: "deleteContentBackward", bubbles: true }));
  assert(area.childNodes.length === 0 && field.value === "", "emptied, got " + area.innerHTML);
  type("again");
  assert(area.innerHTML === "<p>again</p>" && field.value === "<p>again</p>", "typing into an empty area makes a paragraph, got " + area.innerHTML);
`))
}

// jsString is a Go string as a JavaScript string literal.
func jsString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// Pastes are read onto the grammar (DOMParser, inert; built with createElement): a Google Docs paste
// keeps its bold and italic from style and drops its wrapper, Word's list markers go, a web page
// loses its scripts, styles, images, forms and javascript: links the way richtext.Clean does, plain
// text makes paragraphs and breaks, an address pasted over words links them, files alone are refused,
// and each paste is one step of undo.
// CONTROL: against a copy of v2.0.0.js that read no font-weight from style, "Google Docs" failed (its
// bold, carried by style alone, was lost). The reading of style is in parts/editor-paste.js since
// 21/10/2026: against a copy of it with weight never read (through -overlay), "Google Docs" failed again.
func TestBrowserEditorPaste(t *testing.T) {
	const docs = `<meta charset="utf-8"><b style="font-weight:normal;" id="docs-internal-guid-5f3c2a1e-7fff-1234-abcd-0123456789ab"><p dir="ltr" style="line-height:1.38;margin-top:0pt;margin-bottom:0pt;"><span style="font-size:11pt;font-family:Arial,sans-serif;color:#000000;font-weight:700;font-style:normal;">Đậm</span><span style="font-size:11pt;font-family:Arial,sans-serif;font-weight:400;font-style:normal;"> thường </span><span style="font-size:11pt;font-weight:400;font-style:italic;">nghiêng</span></p><br><ul style="margin-top:0;margin-bottom:0;"><li dir="ltr" style="list-style-type:disc;font-weight:400;" aria-level="1"><p dir="ltr" role="presentation"><span style="font-weight:400;">mục một</span></p></li></ul></b>`
	const word = `<html xmlns:o="urn:schemas-microsoft-com:office:office" xmlns:w="urn:schemas-microsoft-com:office:word"><head><style>p.MsoNormal{margin:0in;font-size:11.0pt}</style></head><body lang=EN-US><!--StartFragment--><p class=MsoNormal><b><span style='font-size:12.0pt'>Word đậm</span></b><span style='font-size:12.0pt'> và <i>nghiêng</i><o:p></o:p></span></p><p class=MsoListParagraphCxSpFirst style='text-indent:-.25in;mso-list:l0 level1 lfo1'><![if !supportLists]><span style='font-family:Symbol'><span style='mso-list:Ignore'>·<span style='font:7.0pt "Times New Roman"'>&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp; </span></span></span><![endif]>Ý một<o:p></o:p></p><!--EndFragment--></body></html>`
	const web = `<div class="post"><h1>Tiêu đề</h1><script>window.leaked = 1</script><style>p{color:red}</style><p>Đoạn có <a href="javascript:alert(1)">xấu</a> và <a href="https://kitwork.io/x?a=1&amp;b=2" target="_blank">tốt</a> <img src="x" onerror="window.leaked = 2"></p><ul><li>một<ul><li>lồng</li></ul></li></ul><table><tr><td>ô</td></tr></table><form><input value="x"><button>nút</button></form><blockquote>trích</blockquote></div>`
	const text = "dòng một\ndòng hai\n\nđoạn mới\r\n  "
	runEditorProof(t, "editor paste", editorWire("<p>start</p>", startScope(false, 1, 5), "")+editorProof(`
  var docs = `+jsString(docs)+`, word = `+jsString(word)+`, web = `+jsString(web)+`, text = `+jsString(text)+`;
  caretAt("start", true);
  assert(pasted({ "text/html": docs, "text/plain": "Đậm thường nghiêng" }).defaultPrevented, "a paste is the editor's");
  assert(field.value === "<p>start</p>\n<p><strong>Đậm</strong> thường <em>nghiêng</em></p>\n<ul>\n<li>mục một</li>\n</ul>", "Google Docs, got " + field.value);
  $("undo").click();
  assert(field.value === "<p>start</p>", "a paste is one step of undo, got " + field.value);

  caretAt("start", true); pasted({ "text/html": word });
  assert(field.value === "<p>start</p>\n<p><strong>Word đậm</strong> và <em>nghiêng</em></p>\n<p>Ý một</p>", "Word, got " + field.value);
  $("undo").click();

  caretAt("start", true); pasted({ "text/html": web, "text/plain": "x" });
  assert(field.value === "<p>start</p>\n" + `+jsString(richtext.Clean(web).HTML)+`, "a web page as richtext.Clean reads it, got " + field.value);
  assert(!window.leaked && !area.querySelector("img,script,style,form,input,button,[target]"), "nothing of the page ran or stayed, got " + window.leaked + " " + area.innerHTML);
  $("undo").click();

  caretAt("start", true); pasted({ "text/plain": text });
  assert(field.value === "<p>start</p>\n" + `+jsString(richtext.Clean(text).HTML)+`, "plain text as richtext.Clean reads it, got " + field.value);
  $("undo").click();

  caretAt("start", true); type(" one two");
  caretAt("one", true); pasted({ "text/plain": " và" });
  assert(field.value === "<p>start one và two</p>", "words go into the line at the caret, got " + field.value);
  selectWord("two"); pasted({ "text/plain": " https://kitwork.io/docs " });
  assert(field.value === '<p>start one và <a href="https://kitwork.io/docs">two</a></p>', "an address over words links them, got " + field.value);
  var before = area.innerHTML, files = new DataTransfer();
  files.items.add(new File(["png"], "a.png", { type: "image/png" }));
  assert(area.dispatchEvent(new ClipboardEvent("paste", { clipboardData: files, bubbles: true, cancelable: true })) === false && area.innerHTML === before, "files alone are refused, got " + area.innerHTML);
  // a drop is read the same way, at the place it lands
  var drop = new DataTransfer(); drop.setData("text/plain", "thả");
  caretAt("start", true);
  assert(beforeinput("insertFromDrop", { dataTransfer: drop }).defaultPrevented && field.value.indexOf("<p>startthả one") === 0, "a drop, got " + field.value);
`))
}

// An IME composition (Vietnamese Telex, a phone's keyboard) is the browser's alone: between
// compositionstart and compositionend nothing touches the area, its field or its state, a command
// waits, and at the end the words are read and the command done.
// CONTROL: against a copy of v2.0.0.js whose input listener ran during a composition, "nothing is
// touched during the composition" failed (the field took the half-typed word).
func TestBrowserEditorComposition(t *testing.T) {
	runEditorProof(t, "editor composition", editorWire("<p>xin chào</p>", startScope(false, 2, 8), "")+`<b id="words" data-kitwork-bind="text: $body.words"></b>`+editorProof(`
  caretAt("chào", true);
  area.dispatchEvent(new CompositionEvent("compositionstart", { data: "" }));
  assert(!beforeinput("insertCompositionText", { data: "v", isComposing: true }).defaultPrevented, "composition input is the browser's");
  // the IME writes: "vieet" read as "việt", as Telex does, in a span the browser made
  var t = find("chào")[0]; t.appendData(" vieet");
  var span = document.createElement("span"); span.style.color = "red"; span.textContent = "x"; t.parentNode.appendChild(span);
  var during = area.innerHTML;
  area.dispatchEvent(new InputEvent("input", { inputType: "insertCompositionText", data: "vieet", isComposing: true, bubbles: true }));
  press($("bold"));
  assert(area.innerHTML === during && field.value === "<p>xin chào</p>" && $("words").textContent === "2", "nothing is touched during the composition, got " + area.innerHTML + " / " + field.value);
  t.replaceData(t.length - 5, 5, "việt"); span.remove();
  select(t, t.length);
  area.dispatchEvent(new CompositionEvent("compositionend", { data: "việt" }));
  await wait(20);
  assert(field.value === "<p>xin chào việt</p>" && $("words").textContent === "3", "the words are read at the end, got " + field.value + " / " + $("words").textContent);
  assert(pressed("bold") === "true", "the bold pressed during it is done after: it waits at the caret, got " + pressed("bold") + " " + area.innerHTML);
  type("nam");
  assert(field.value === "<p>xin chào việt<strong>nam</strong></p>", "and what is typed next is bold, got " + field.value);
`))
}

// Undo and redo are the editor's own: typing is one step until a pause of 500ms or a caret moved in
// between; a command is a step; Ctrl+Z, Ctrl+Shift+Z, Ctrl+Y and the browser's historyUndo and
// historyRedo all reach it; the buttons say when there is nothing to undo or redo.
// CONTROL: against a copy of v2.0.0.js that never grouped typing, "b and c typed together are one
// step" failed; against one whose TYPED left formatJustifyFull to the browser (through -overlay, run
// 21/10/2026), "superscript and full justification are no part of the grammar" failed.
func TestBrowserEditorHistory(t *testing.T) {
	runEditorProof(t, "editor history", editorWire("<p>a</p>", startScope(false, 1, 1), "")+editorProof(`
  var undo = $("undo"), redo = $("redo"), off = function (b) { return b.getAttribute("aria-disabled") === "true"; };
  assert(off(undo) && off(redo), "nothing to undo or redo at first");
  caretAt("a", true); type("b"); type("c");
  assert(!off(undo) && off(redo), "something to undo");
  await wait(600); type("d");
  caretAt("a"); type("x");
  assert(field.value === "<p>xabcd</p>", "typed, got " + field.value);
  undo.click();
  assert(field.value === "<p>abcd</p>", "a caret moved in between: x alone, got " + field.value);
  undo.click();
  assert(field.value === "<p>abc</p>", "after a pause, d alone, got " + field.value);
  undo.click();
  assert(field.value === "<p>a</p>" && off(undo) && !off(redo), "b and c typed together are one step, got " + field.value);
  redo.click();
  assert(field.value === "<p>abc</p>", "redo, got " + field.value);
  assert(key(area, "y", { ctrlKey: true }).defaultPrevented && field.value === "<p>abcd</p>", "Ctrl+Y redoes, got " + field.value);
  assert(key(area, "z", { ctrlKey: true }).defaultPrevented && field.value === "<p>abc</p>", "Ctrl+Z undoes, got " + field.value);
  assert(key(area, "Z", { ctrlKey: true, shiftKey: true }).defaultPrevented && field.value === "<p>abcd</p>", "Ctrl+Shift+Z redoes, got " + field.value);
  selectWord("bc"); press($("bold"));
  assert(field.value === "<p>a<strong>bc</strong>d</p>", "bold, got " + field.value);
  assert(beforeinput("historyUndo").defaultPrevented && field.value === "<p>abcd</p>", "the browser's undo reaches the editor's: the command was one step, got " + field.value);
  assert(beforeinput("historyRedo").defaultPrevented && field.value === "<p>a<strong>bc</strong>d</p>", "and its redo, got " + field.value);
  type("new");
  assert(off(redo), "a new step drops what could be redone");
  var before = field.value;
  assert(beforeinput("formatSuperscript").defaultPrevented && beforeinput("formatJustifyFull").defaultPrevented && field.value === before, "superscript and full justification are no part of the grammar (underline is, since grammar v2: TestBrowserEditorUnderlineStrikeCode), got " + field.value);
`))
}

// A link: the button is off with a caret outside a link; link() keeps the selection and opens the
// popover with its input focused; a javascript: address is refused (the popover stays, the input
// aria-invalid, the error shown and named); a bare host becomes https://, Enter in the input links and
// never submits; the link at the caret fills the address and takes a new one; unlink() and Escape.
// CONTROL: against a copy of v2.0.0.js whose link(address) never marked a refusal, "javascript: is
// refused, the popover stays" failed (no aria-invalid, the error hidden).
func TestBrowserEditorLink(t *testing.T) {
	runEditorProof(t, "editor link", `<form id="form" method="post" action="/never">`+editorWire("<p>hello world</p>", startScope(false, 2, 11), "")+`</form>`+editorProof(`
  var link = $("link"), pop = $("pop"), input = $("address"), error = $("error"), submitted = false;
  $("form").addEventListener("submit", function (event) { submitted = true; event.preventDefault(); });
  caretAt("hello");
  assert(link.getAttribute("aria-disabled") === "true", "a caret outside a link: the button is off");
  press(link);
  assert(pop.hidden, "and it opens nothing");
  selectWord("world");
  assert(!link.hasAttribute("aria-disabled"), "with words selected it is on");
  press(link);
  assert(!pop.hidden && link.getAttribute("aria-expanded") === "true" && document.activeElement === input && input.value === "", "the popover opens, its input focused, got hidden=" + pop.hidden + " expanded=" + link.getAttribute("aria-expanded") + " focus=" + active());
  input.value = "javascript:alert(1)"; input.dispatchEvent(new Event("input", { bubbles: true }));
  press($("apply"));
  assert(!pop.hidden && input.getAttribute("aria-invalid") === "true" && !error.hidden && input.getAttribute("aria-describedby") === "error" && !area.querySelector("a"), "javascript: is refused, the popover stays, got " + pop.hidden + " " + input.outerHTML + " " + area.innerHTML);
  input.value = "kitwork.io/x"; input.dispatchEvent(new Event("input", { bubbles: true }));
  var enter = key(input, "Enter");
  assert(enter.defaultPrevented && !submitted, "Enter in the address never submits the form");
  assert(field.value === '<p>hello <a href="https://kitwork.io/x">world</a></p>', "a bare host is a web page, got " + field.value);
  assert(pop.hidden && error.hidden && !input.hasAttribute("aria-invalid") && !input.hasAttribute("aria-describedby") && document.activeElement === area, "closed, the error gone, the focus back, got " + active());
  assert(getSelection().toString() === "world", "the linked words stay selected, got " + getSelection().toString());

  caretAt("world");
  assert(!link.hasAttribute("aria-disabled"), "a caret in a link: on");
  key(area, "k", { ctrlKey: true });
  assert(!pop.hidden && input.value === "https://kitwork.io/x", "Ctrl+K opens it with the link's address, got " + input.value);
  input.value = "/docs"; input.dispatchEvent(new Event("input", { bubbles: true }));
  press($("apply"));
  assert(field.value === '<p>hello <a href="/docs">world</a></p>', "the link at the caret takes the new address, got " + field.value);
  caretAt("world"); press(link); press($("unlink"));
  assert(field.value === "<p>hello world</p>" && pop.hidden, "unlink, got " + field.value);

  selectWord("hello"); press(link);
  key(input, "Escape");
  assert(pop.hidden && document.activeElement === area && getSelection().toString() === "hello", "Escape closes, the selection back, got " + getSelection().toString());
  selectWord("hello"); press(link); press($("cancel"));
  assert(pop.hidden && getSelection().toString() === "hello" && field.value === "<p>hello world</p>", "close() too");
`))
}

// The toolbar is one stop for Tab: the arrows move along it (round at either end), Home and End go to
// its ends, Alt+F10 moves there from the area and Escape back with the selection as it was. The
// shortcuts it names are Control's away from Apple's devices.
// CONTROL: against a copy of v2.0.0.js whose toolbar moved no Tab stop, "ArrowRight moves on, the stop
// with it" failed.
func TestBrowserEditorToolbarKeyboard(t *testing.T) {
	runEditorProof(t, "editor toolbar", editorWire("<p>hello world</p>", startScope(false, 2, 11), "")+editorProof(`
  var bar = $("toolbar"), tabs = function () { return Array.prototype.map.call(bar.querySelectorAll("button"), function (b) { return b.tabIndex; }).join(""); };
  assert(tabs() === "0-1-1-1-1-1-1-1-1-1", "the first button alone is a Tab stop, got " + tabs());
  selectWord("world"); area.focus();
  assert(key(area, "F10", { altKey: true }).defaultPrevented && active() === "bold", "Alt+F10 goes to the toolbar, got " + active());
  key($("bold"), "ArrowRight"); focusIn($("italic"));
  assert(active() === "italic" && $("italic").tabIndex === 0 && $("bold").tabIndex === -1, "ArrowRight moves on, the stop with it, got " + active() + " " + tabs());
  key($("italic"), "End");
  assert(active() === "redo", "End, got " + active());
  key($("redo"), "ArrowRight");
  assert(active() === "bold", "round from the last to the first, got " + active());
  key($("bold"), "ArrowLeft");
  assert(active() === "redo", "and back, got " + active());
  key($("redo"), "Home");
  assert(active() === "bold", "Home, got " + active());
  assert(key($("bold"), "Escape").defaultPrevented && active() === "area" && getSelection().toString() === "world", "Escape goes back to the area, the selection kept, got " + active() + " " + getSelection().toString());
  assert($("bold").getAttribute("aria-keyshortcuts") === "Control+B", "Control away from Apple");
  assert(!key(area, "b", { metaKey: true }).defaultPrevented && key(area, "b", { ctrlKey: true }).defaultPrevented && field.value === "<p>hello <strong>world</strong></p>", "Ctrl+B is bold, got " + field.value);
`))
}

// On an Apple device the shortcuts are Command's, and say so.
// CONTROL: against a copy of v2.0.0.js that never took a device for Apple's, "Meta on Apple" failed
// (Control+B).
func TestBrowserEditorAppleKeys(t *testing.T) {
	runEditorProof(t, "editor apple", editorWire("<p>hello world</p>", startScope(false, 2, 11), "")+
		`<script>Object.defineProperty(navigator, "platform", { get: function () { return "MacIntel"; } });</script><script src="/kit.js"></script>`+editorProof(`
  assert($("bold").getAttribute("aria-keyshortcuts") === "Meta+B" && $("redo").getAttribute("aria-keyshortcuts") === "Meta+Shift+Z", "Meta on Apple, got " + $("bold").getAttribute("aria-keyshortcuts"));
  selectWord("world");
  assert(!key(area, "b", { ctrlKey: true }).defaultPrevented && field.value === "<p>hello world</p>", "Control+B is not bold there");
  assert(key(area, "b", { metaKey: true }).defaultPrevented && field.value === "<p>hello <strong>world</strong></p>", "Command+B is, got " + field.value);
`))
}

// The field the form posts follows the area on every input, at a composition's end and on the form's
// formdata; required holds through it (an empty editor stops the form, the area takes the focus and
// says aria-invalid); a readonly editor takes no writing and its buttons say so.
// CONTROL: against a copy of v2.0.0.js whose formdata listener set no entry, "formdata takes the
// area's value" failed (the form posted the stale field).
func TestBrowserEditorField(t *testing.T) {
	runEditorProof(t, "editor field", `<form id="form" method="post">`+editorWire("", startScope(true, 0, 0), " required")+`</form>
<b id="empty" data-kitwork-bind="text: $body.empty ? 'empty' : 'written'"></b>`+editorProof(`
  assert(area.childNodes.length === 0 && $("empty").textContent === "empty", "an empty editor is an empty area, for its placeholder");
  var form = $("form");
  assert(!form.reportValidity(), "required: an empty editor stops the form");
  assert(area.getAttribute("aria-invalid") === "true" && active() === "area", "the area takes the focus and says why, got " + active());
  select(area, 0); type("viết"); await wait(10);
  assert(field.value === "<p>viết</p>" && !area.hasAttribute("aria-invalid") && $("empty").textContent === "written", "the field follows typing, got " + field.value);
  assert(form.checkValidity(), "and the form may go");
  field.value = "stale";
  assert(new FormData(form).get("body") === "<p>viết</p>" && field.value === "<p>viết</p>", "formdata takes the area's value, got " + field.value);
`))
	runEditorProof(t, "editor readonly", editorWire("<p>kept</p>", startScope(false, 1, 4), " readonly")+editorProof(`
  assert(!area.hasAttribute("contenteditable") && area.getAttribute("aria-readonly") === "true" && area.tabIndex === 0, "readonly: no writing, still read, got " + area.outerHTML.slice(0, 200));
  var off = Array.prototype.every.call(document.querySelectorAll("#toolbar button"), function (b) { return b.getAttribute("aria-disabled") === "true"; });
  assert(off, "every command is off");
  select(text(area), 0, text(area), 4); $("bold").click();
  assert(area.innerHTML === "<p>kept</p>", "and does nothing, got " + area.innerHTML);
`))
}

// unsaved: the value differs from the one at ready. While it does, leaving asks first (beforeunload)
// and Drive is held (it loads the next page whole, so that question is asked); the form's own submit
// lets go. A page that answers a refused save shows it unsaved from the start.
// CONTROL: against a copy of v2.0.0.js whose beforeunload listener held nothing, "unsaved: leaving
// asks" failed.
func TestBrowserEditorUnsaved(t *testing.T) {
	runEditorProof(t, "editor unsaved", `<form id="form" method="post" action="/never">`+editorWire("<p>draft</p>", startScope(false, 1, 5), "")+`</form>
<b id="unsaved" data-kitwork-bind="show: $body.unsaved" hidden>Chưa lưu</b>`+editorProof(`
  var leave = function () { var event = new Event("beforeunload", { cancelable: true }); window.dispatchEvent(event); return event.defaultPrevented; };
  var holds = [];
  window.kit.drive.hold = function (key, on) { holds.push(on); };
  assert(!leave() && $("unsaved").hidden, "saved: leaving asks nothing");
  caretAt("draft", true); type("!"); await wait(10);
  assert(!$("unsaved").hidden && leave() && holds.join() === "true", "unsaved: leaving asks, Drive is held, got " + holds.join());
  backspace(); await wait(10);
  assert($("unsaved").hidden && !leave() && holds.join() === "true,false", "back as it was: saved again, got " + holds.join());
  type("?");
  var form = $("form"), stop = function (event) { event.preventDefault(); };
  form.addEventListener("submit", stop);
  form.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
  assert(leave() && holds.join() === "true,false,true", "a submit the page stopped keeps the hold, got " + holds.join());
  form.removeEventListener("submit", stop);
  form.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
  assert(!leave() && holds.join() === "true,false,true,false", "the form's own submit lets go, got " + holds.join());
  type("more");
  assert(!leave(), "and the words have gone with it: no hold again");
`))
	runEditorProof(t, "editor refused save", editorWire("<p>refused</p>", "empty: false; words: 1; characters: 7; unsaved: true; open: false; address: ''", "")+`
<b id="unsaved" data-kitwork-bind="show: $body.unsaved" hidden>Chưa lưu</b>`+editorProof(`
  var event = new Event("beforeunload", { cancelable: true }); window.dispatchEvent(event);
  assert(!$("unsaved").hidden && event.defaultPrevented, "a refused save shown again is unsaved from the start");
`))
}

// While the words are unsaved Drive is held: a link, a GET form and Back each load the next page whole
// (the browser's own move, so it asks before the words go), Back with the editor's address put back
// first, so the page it leaves is the one its form posts to. Each in a browser of its own, a CONTROL
// in the run: before anything is typed, the same move is Drive's swap (the window and its marker stay).
// CONTROL: against drive.js as it was before the hold (go test -overlay with that copy), all three runs
// failed: Drive swapped /away, /found and the Back to / in (every request carried X-Kitwork-Hydrate).
func TestBrowserEditorHoldsDrive(t *testing.T) {
	// the control and the held move, on the editor's page reached by Drive's swap from /
	t.Run("link", func(t *testing.T) {
		runDriveHold(t, `
    $("away").click(); await arrive("here");
    assert(window.marker && location.pathname === "/away", "CONTROL: nothing typed, a link is Drive's swap");
    history.back(); await arrive("area");
    assert(window.marker && location.pathname === "/editor", "CONTROL: and so is Back");
    caretAt("draft", true); type("!"); await wait(20);
    $("away").click();`, `
    assert(here === "/away" && !window.marker, "unsaved: the link loaded its page whole, on " + here);`)
	})
	t.Run("form", func(t *testing.T) {
		runDriveHold(t, `
    $("search").requestSubmit(); await arrive("here");
    assert(window.marker && location.pathname === "/found", "CONTROL: nothing typed, a GET form is Drive's swap");
    history.back(); await arrive("area");
    caretAt("draft", true); type("?"); await wait(20);
    $("search").requestSubmit();`, `
    assert(here === "/found" && location.search === "?q=x" && !window.marker, "unsaved: the GET form loaded its page whole, on " + here + location.search);`)
	})
	t.Run("back", func(t *testing.T) {
		runDriveHold(t, `
    caretAt("draft", true); type("…"); await wait(20);
    history.back();`, `
    assert(here === "/" && !window.marker, "unsaved: Back loaded its page whole, on " + here);
    assert(sessionStorage.getItem("left") === "/editor", "and left from the editor's own address, got " + sessionStorage.getItem("left"));`)
	})
}

// runDriveHold opens / (Drive on), moves to /editor by Drive's swap, runs held there (which ends by
// leaving), and then arrived on the page that was loaded whole: once on a full load of each page.
func runDriveHold(t *testing.T, held, arrived string) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping Drive browser proof in short mode")
	}
	browser := findHeadlessBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	// one script on every page, run on a full load only (a page Drive swaps in keeps the window, and
	// DOMContentLoaded has long passed)
	script := `<script>document.addEventListener("DOMContentLoaded", function () { (async function () {` + proofScriptHead + editorScript + `
  await wait(100); // the kernel and Drive start on DOMContentLoaded too, after this listener
  var here = location.pathname;
  var arrive = async function (id) { for (var i = 0; i < 60 && !$(id); i++) await wait(50); assert($(id), "Drive brought #" + id); await wait(150); area = $("area"); field = $("field"); };
  // the browser's question is not shown to a page no person has touched (Chrome: no user gesture), so
  // each move goes on; TestBrowserEditorUnsaved proves it is asked
  addEventListener("beforeunload", function () { sessionStorage.setItem("left", location.pathname); });
  if (!sessionStorage.getItem("started")) {
    sessionStorage.setItem("started", "1");
    window.marker = 1;
    $("to-editor").click(); await arrive("area");
` + held + `
    return;
  }
` + arrived + `
  root.setAttribute("data-kit-test", "passed");
})().catch(function (error) { var root = document.documentElement; if (!root.hasAttribute("data-kit-test")) { root.setAttribute("data-kit-test", "failed"); root.setAttribute("data-kit-test-error", String(error && error.stack || error)); } }); });
</script>`
	page := func(main string) string {
		return Deliver(`<!doctype html><html><head></head><body>`+editorStyle+`<main>`+main+`</main>`+script+`</body></html>`, Options{Drive: true})
	}
	pages := map[string]string{
		"/": page(`<a id="to-editor" href="/editor">editor</a>`),
		"/editor": page(`<form id="post" method="post" action="/editor">` + editorWire("<p>draft</p>", startScope(false, 1, 5), "") + `</form>
<a id="away" href="/away">away</a><form id="search" method="get" action="/found"><input name="q" value="x"></form>`),
		"/away":  page(`<p id="here">away</p>`),
		"/found": page(`<p id="here">found</p>`),
	}
	var visits []string // what the browser asked for, in order (1: Drive's fetch): where a run that failed stopped
	var visitsMu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if asset, _ := AssetAt(request.URL.Path); asset != nil {
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(asset.Body)
			return
		}
		visitsMu.Lock()
		visits = append(visits, request.Method+" "+request.URL.RequestURI()+request.Header.Get("X-Kitwork-Hydrate"))
		visitsMu.Unlock()
		body, ok := pages[request.URL.Path]
		if !ok {
			http.NotFound(response, request)
			return
		}
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = response.Write([]byte(body))
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// the fewest switches: with runBrowserProof's set, a page loaded whole after a Drive swap may never
	// reach DOMContentLoaded under --virtual-time-budget (20/10/2026, Chrome; no one switch alone)
	args := []string{"--headless=new", "--disable-gpu", "--no-sandbox", "--no-first-run", "--user-data-dir=" + filepath.Join(t.TempDir(), "p"),
		"--virtual-time-budget=20000", "--dump-dom", server.URL + "/"}
	if runtime.GOOS == "windows" {
		args = append([]string{"--disable-background-mode", "--disable-breakpad", "--disable-crashpad-for-testing"}, args...)
	}
	output, err := exec.CommandContext(ctx, browser, args...).CombinedOutput()
	if bytes.Contains(output, []byte(`data-kit-test="passed"`)) {
		return
	}
	visitsMu.Lock()
	defer visitsMu.Unlock()
	if i := bytes.Index(output, []byte("data-kit-test-error=")); i >= 0 {
		t.Fatalf("editor Drive hold proof failed after %s: %s", strings.Join(visits, ", "), output[i:min(i+400, len(output))])
	}
	t.Fatalf("editor Drive hold proof did not pass (err=%v) after %s:\n%.2000s", err, strings.Join(visits, ", "), output)
}

// editorState is one state the editor reached in the corpus proof, as the browser saw it.
type editorState struct {
	Label      string `json:"label"`
	HTML       string `json:"html"`  // the area's innerHTML, as it stands (no NFC: richtext has none)
	Value      string `json:"value"` // the field the form posts
	Words      int    `json:"words"`
	Characters int    `json:"characters"`
	Empty      bool   `json:"empty"`
}

// The value is richtext's own: for every state the editor reaches — typing (the browser's, with its
// no-break spaces), every command, Enter, breaks, pastes, links, the browser's own deletes, undo and
// redo — the field holds exactly richtext.Clean of the area as it stands, Clean of the value is the
// value, and the counts are Clean's. Clean is computed here, in Go, on what the browser dumped.
// The area is cleaned as it stands: the editor reads text as richtext does, with no NFC (Go's stdlib
// has none), so letters typed decomposed stay decomposed in the value and the counts are Clean's.
// CONTROL: against a copy of v2.0.0.js whose tidy() kept a space at the end of a line, every state
// whose last line ended in a typed space failed all three checks (the field, Clean of the value, the
// characters), from "spaces typed" on. Against the file as it was before the review of 20/10/2026
// (read() normalising to NFC), every state from "decomposed letters and an emoji" until "everything
// typed over" failed the first check: the field held NFC, Clean of the area the letters as typed.
func TestBrowserEditorValueIsRichtextsOwn(t *testing.T) {
	const web = `<article><h1>Web &amp; page</h1><p>Text with <b>bold <i>both</i></b> and <a href="/x?a=1&amp;b=2">a link</a>.</p><ol><li>one</li><li>two<br>lines</li></ol><blockquote><p>quote</p><p>more</p></blockquote><pre>pre
formatted</pre></article>`
	const docs = `<b style="font-weight:normal;" id="docs-internal-guid-1"><p dir="ltr"><span style="font-weight:700;">Docs</span><span style="font-weight:400;"> then </span><span style="font-style:italic;">slant</span></p></b>`
	start := richtext.Clean(`<h2>Đầu đề</h2><p>Một đoạn <strong>đậm</strong> và <a href="https://kitwork.io/a">liên kết</a>.</p><ul><li>mục một</li><li>mục hai</li></ul><blockquote><p>trích dẫn</p></blockquote><p>cuối</p>`)
	out := runBrowserDump(t, "editor value", editorWire(strings.ReplaceAll(start.HTML, "\n", ""), startScope(false, start.Words, start.Characters), "")+`<pre id="corpus"></pre>`+editorProof(`
  var states = [], body = window.kit.scope.$body;
  function record(label) {
    states.push({ label: label, html: area.innerHTML, value: field.value, words: body.words, characters: body.characters, empty: body.empty });
  }
  function all() { select(area, 0, area, area.childNodes.length); }
  function browserDelete(forward) { var event = beforeinput(forward ? "deleteContentForward" : "deleteContentBackward"); if (!event.defaultPrevented) document.execCommand(forward ? "forwardDelete" : "delete", false); }
  record("first paint");
  caretAt("cuối", true); type("  hai  dấu cách  "); record("spaces typed, the browser's no-break spaces");
  type("a & b < c > d \"e\""); record("characters that are escaped");
  type("Vie\u0302\u0323t \ud83d\ude00"); record("decomposed letters and an emoji");
  type("\u00a0nb\u00a0sp\u200bzw\ufeff"); record("no-break and zero-width spaces typed");
  var a = find("đoạn"), b = find("liên"); select(a[0], 1, b[0], 3); press($("bold")); record("bold across a link");
  selectWord("trích"); press($("italic")); record("italic in a quote");
  caretAt("đậm"); beforeinput("insertParagraph"); record("Enter inside bold");
  caretAt("mục hai", true); beforeinput("insertParagraph"); record("Enter in a list");
  beforeinput("insertParagraph"); record("Enter on an empty item");
  type("sau danh sách"); beforeinput("insertLineBreak"); type("dòng mới"); record("a line break");
  beforeinput("insertLineBreak"); record("a break at the end of a line");
  caretAt("mục một"); press($("h3")); record("an item made a heading");
  caretAt("mục hai"); backspace(); record("Backspace at the start of an item");
  a = find("Đầu"); b = find("trích"); select(a[0], 0, b[0], 2); press($("quote")); record("a quote over several blocks");
  press($("quote")); record("and off again");
  a = find("Đầu"); b = find("cuối"); select(a[0], 0, b[0], 2); press($("ordered")); record("numbered over everything");
  press($("list")); record("bulleted");
  selectWord("kết"); press($("link")); $("address").value = '/tìm?q=a&b="c"<d>'; key($("address"), "Enter"); record("a link whose address must be escaped");
  selectWord("kết"); press($("link")); press($("unlink")); record("unlinked");
  caretAt("cuối", true); press($("bold")); record("bold at a caret, nothing typed");
  caretAt("Đầu"); type("x"); record("typing elsewhere after it");
  a = find("mục"); b = find("cuối"); select(a[0], 2, b[0], 1); browserDelete(); record("the browser's delete across blocks");
  caretAt("xĐ"); browserDelete(true); record("the browser's forward delete");
  var c = find("hai")[0]; select(c, c.length); pasted({ "text/html": `+jsString(web)+` }); record("a web page pasted");
  caretAt("quote", true); pasted({ "text/html": `+jsString(docs)+` }); record("Google Docs pasted into a quote");
  caretAt("one"); pasted({ "text/plain": "plain\tpasted\r\nwith lines\n\n\nand a paragraph" }); record("plain text pasted into an item");
  a = find("two"); b = find("more"); select(a[0], 1, b[0], 2); pasted({ "text/plain": "over" }); record("pasted over a list and a quote");
  for (var i = 0; i < 6; i++) { $("undo").click(); record("undo " + (i + 1)); }
  for (i = 0; i < 3; i++) { $("redo").click(); record("redo " + (i + 1)); }
  all(); type("fresh"); record("everything typed over");
  caretAt("fresh", true); beforeinput("insertParagraph"); press($("h2")); type("Đầu đề mới"); record("a heading made on a new line");
  caretAt("Đầu", false); beforeinput("insertParagraph"); record("Enter at the start of a heading");
  all(); browserDelete(); record("everything deleted");
  type("again"); record("typed into the empty area");
  $("corpus").textContent = JSON.stringify(states);
`))
	checkEditorCorpus(t, out, 40, 25)
}

// checkEditorCorpus reads the states a corpus proof dumped (no fewer than least, holding no fewer than
// distinct values) and holds each to richtext: the field is Clean of the area as it stands, Clean of
// the value is the value, and the counts are Clean's.
func checkEditorCorpus(t *testing.T, out string, least, distinct int) {
	t.Helper()
	i := strings.Index(out, `<pre id="corpus">`)
	j := strings.Index(out[i:], "</pre>")
	if i < 0 || j < 0 {
		t.Fatalf("the proof wrote no corpus:\n%.2000s", out)
	}
	var states []editorState
	if err := json.Unmarshal([]byte(html.UnescapeString(out[i+len(`<pre id="corpus">`):i+j])), &states); err != nil {
		t.Fatalf("the corpus does not read: %v", err)
	}
	if len(states) < least {
		t.Fatalf("only %d states recorded", len(states))
	}
	for _, s := range states {
		clean := richtext.Clean(s.HTML)
		if s.Value != clean.HTML {
			t.Errorf("%s: the field is not richtext.Clean of the area\n area  %q\n field %q\n Clean %q", s.Label, s.HTML, s.Value, clean.HTML)
		}
		if again := richtext.Clean(s.Value); again.HTML != s.Value {
			t.Errorf("%s: Clean(value) is not the value\n value %q\n Clean %q", s.Label, s.Value, again.HTML)
		}
		if again := richtext.Clean(s.Value); again.Words != s.Words || again.Characters != s.Characters || again.Empty() != s.Empty {
			t.Errorf("%s: counts %d words %d characters empty %v, richtext's %d %d %v", s.Label, s.Words, s.Characters, s.Empty, again.Words, again.Characters, again.Empty())
		}
	}
	seen := map[string]bool{}
	for _, s := range states {
		seen[s.Value] = true
	}
	if len(seen) < distinct {
		t.Errorf("the corpus reached only %d distinct values", len(seen))
	}
}

// The editor's link rule is richtext.Link's: an address pasted in an <a href> (as HTML writes it, its
// character references decoded once) is kept exactly when Link keeps it, as Link returns it; an
// address typed in the popover is trimmed, a bare host is https://, then the same rule.
// CONTROL: against a copy of v2.0.0.js whose safeLink kept a backslash as it is (no "\\" read as
// "/"), the pasted "/\\evil.example" was kept where Link refuses it, and that row alone failed.
func TestBrowserEditorLinkRuleIsRichtexts(t *testing.T) {
	addresses := []string{
		"https://kitwork.io/x", "HTTP://kitwork.io", "mailto:ai@kitwork.io", "tel:+84123", "/trang", "/trang?a=1&b=2",
		"//evil.example", `/\evil.example`, "/\t/evil.example", "java\tscript:alert(1)", "javascript:alert(1)",
		"  https://kitwork.io", "\x01https://kitwork.io", "#top", "?q=1", "trang/con", "data:text/html,x", "vbscript:x",
		`https://kitwork.io/"q"`, "https://kitwork.io/<y>", "https:kitwork.io", "ftp://kitwork.io", "kitwork.io/x",
		"https://kitwork.io/a b", "&#106;avascript:alert(1)", "/a?lang=vi&region=vn",
	}
	typed := []string{"kitwork.io/x", " https://kitwork.io ", "www.kitwork.io", "localhost:8080/x", "a@b.c", "/trang", "javascript:x", "kitwork.io:8443/a?b#c"}
	var pastes, inputs []string
	for _, address := range addresses {
		pastes = append(pastes, `<p><a href="`+strings.ReplaceAll(strings.ReplaceAll(address, `"`, "&quot;"), "<", "&lt;")+`">w</a></p>`)
	}
	inputs = append(inputs, typed...)
	out := runBrowserDump(t, "editor link rule", editorWire("<p>start</p>", startScope(false, 1, 5), "")+`<pre id="corpus"></pre>`+editorProof(`
  var pastes = `+jsonOf(pastes)+`, inputs = `+jsonOf(inputs)+`, found = { pasted: [], typed: [] };
  var hrefs = function () { return Array.prototype.map.call(area.querySelectorAll("a"), function (a) { return a.getAttribute("href"); }); };
  pastes.forEach(function (html) {
    area.innerHTML = "<p>start</p>"; caretAt("start", true);
    pasted({ "text/html": html });
    found.pasted.push(hrefs()[0] || "");
  });
  inputs.forEach(function (address) {
    area.innerHTML = "<p>start</p>"; selectWord("start");
    press($("link")); $("address").value = address; key($("address"), "Enter");
    found.typed.push(hrefs()[0] || (!$("pop").hidden && $("address").getAttribute("aria-invalid") === "true" ? "" : "?"));
    if (!$("pop").hidden) key($("address"), "Escape");
  });
  $("corpus").textContent = JSON.stringify(found);
`))
	i := strings.Index(out, `<pre id="corpus">`)
	j := strings.Index(out[i:], "</pre>")
	var found struct{ Pasted, Typed []string }
	if err := json.Unmarshal([]byte(html.UnescapeString(out[i+len(`<pre id="corpus">`):i+j])), &found); err != nil || len(found.Pasted) != len(addresses) || len(found.Typed) != len(typed) {
		t.Fatalf("the corpus does not read (%v): %+v", err, found)
	}
	for k, address := range addresses {
		written := strings.ReplaceAll(strings.ReplaceAll(address, `"`, "&quot;"), "<", "&lt;")
		want, _ := richtext.Link(written)
		if found.Pasted[k] != want {
			t.Errorf("pasted %q: the editor keeps %q, richtext.Link %q", address, found.Pasted[k], want)
		}
	}
	for k, address := range typed {
		address = strings.TrimSpace(address)
		if regexp.MustCompile(`^[\w-]+(\.[\w-]+)+(:\d+)?([/?#]|$)`).MatchString(address) {
			address = "https://" + address
		}
		want, _ := richtext.Link(html.EscapeString(address))
		if found.Typed[k] != want {
			t.Errorf("typed %q: the editor links %q, richtext.Link %q", typed[k], found.Typed[k], want)
		}
	}
}

// jsonOf is a Go value as a JavaScript literal.
func jsonOf(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// A mark toggled with nothing selected writes nothing yet: the area is as it was, there is nothing to
// undo, its button says it is on. What is typed there takes it, and so does a letter typed, deleted and
// typed again, as Unikey writes á (a, s: Backspace then á); pressed again, what is typed next is plain;
// a Backspace deletes the letter before the caret and the mark waits on, for what is typed after it;
// toggled twice it changes nothing; a caret moved elsewhere ends the wait.
// CONTROL, against copies of v2.0.0.js through -overlay (run 20/10/2026): with mark() at a caret writing
// the invisible place of its own (U+200B in a <strong>) again, as the file did before the review of
// 20/10/2026, "bold at a caret writes nothing yet" failed, and with that row skipped (a copy of this
// file through -overlay too), "nothing to undo" (the invisible place was a step of its own); with the
// wait ended at the first input after it (settle clearing it, as this fix was first written), "a
// Backspace deletes the letter before the caret, the italic waits on" failed (the wait over); with the
// wait's start left where it was when a Backspace went back past it, "the italic waits on, for what is
// typed after it" failed (the d went into the bold); with the wait ended on selectionchange alone (not
// also at the start of each input), "a caret moved and typed at once ends the wait too" failed (the Z
// went in italic). Not a control: "a letter typed again keeps it"
// holds in Chrome with the wait ended too (the browser types the á bold again after deleting the a).
func TestBrowserEditorMarkWaitsAtTheCaret(t *testing.T) {
	runEditorProof(t, "editor waiting mark", editorWire("<p>một hai ba</p>", startScope(false, 3, 10), "")+editorProof(`
  var undo = $("undo"), before = area.innerHTML;
  caretAt("ba", true); press($("bold"));
  assert(area.innerHTML === before && pressed("bold") === "true", "bold at a caret writes nothing yet and is pressed, got " + area.innerHTML.replace(/\u200b/g, "[ZW]") + " / " + pressed("bold"));
  assert(undo.getAttribute("aria-disabled") === "true", "nothing to undo: nothing changed");
  type("a");
  assert(field.value === "<p>một hai ba<strong>a</strong></p>", "what is typed there takes it, got " + field.value);
  backspace(); type("á");
  assert(field.value === "<p>một hai ba<strong>á</strong></p>", "a letter typed again keeps it, got " + field.value);
  press($("bold"));
  assert(pressed("bold") === "false", "pressed again, it is off");
  type("c");
  assert(field.value === "<p>một hai ba<strong>á</strong>c</p>", "and what is typed next is plain, got " + field.value);
  press($("italic")); backspace();
  assert(field.value === "<p>một hai ba<strong>á</strong></p>" && pressed("italic") === "true", "a Backspace deletes the letter before the caret, the italic waits on, got " + field.value + " / " + pressed("italic"));
  type("d");
  assert(field.value === "<p>một hai ba<strong>á</strong><em>d</em></p>", "the italic waits on, for what is typed after it, got " + field.value);
  press($("bold")); press($("bold"));
  assert(pressed("bold") === "false", "toggled twice, it changes nothing");
  caretAt("hai", true);
  assert(pressed("italic") === "false", "a caret moved elsewhere ends the wait");
  type("X");
  assert(field.value === "<p>một haiX ba<strong>á</strong><em>d</em></p>", "and what is typed there is plain, got " + field.value);
  // the caret moved and typed into at once, before the browser says selectionchange
  press($("italic")); getSelection().collapse(find("một")[0], 3); type("Y"); type("Z");
  assert(field.value === "<p>mộtYZ haiX ba<strong>á</strong><em>d</em></p>", "a caret moved and typed at once ends the wait too, got " + field.value);
`))
}

// Undo back to the state the editor started in puts the caret where the writer was: that state, taken
// at ready, has no caret of its own, so the step undone gives it; what is typed next goes there, not to
// the top of the post.
// CONTROL: against a copy of v2.0.0.js whose travel() placed only the restored step's own caret (the
// file before the review of 20/10/2026), "the caret is where the writer was" failed (the caret at the
// area's start).
func TestBrowserEditorUndoToTheStartKeepsTheCaret(t *testing.T) {
	runEditorProof(t, "editor undo caret", editorWire("<p>một</p><p>hai</p>", startScope(false, 2, 6), "")+editorProof(`
  caretAt("hai", true); type("abc");
  assert(field.value === "<p>một</p>\n<p>haiabc</p>", "typed, got " + field.value);
  assert(key(area, "z", { ctrlKey: true }).defaultPrevented && field.value === "<p>một</p>\n<p>hai</p>", "Ctrl+Z, got " + field.value);
  var s = getSelection();
  assert(s.anchorNode === find("hai")[0] && s.anchorOffset === 3, "the caret is where the writer was, at the end of hai, got " + (s.anchorNode && s.anchorNode.nodeName) + " at " + s.anchorOffset);
  type("x");
  assert(field.value === "<p>một</p>\n<p>haix</p>", "and what is typed goes there, got " + field.value);
`))
}

// In an empty editor, a heading, an item or a quote whose only letter is deleted stays what it is, so
// the next letter goes into it: Unikey writes đ (typed d, d) as Backspace then đ. A lone paragraph
// emptied is the empty area again, for its placeholder (TestBrowserEditorNormaliser).
// CONTROL: against a copy of v2.0.0.js whose normalise() emptied any lone line with no words (the file
// before the review of 20/10/2026), all three rows failed: the đ went into a paragraph.
func TestBrowserEditorKeepsALoneBlock(t *testing.T) {
	runEditorProof(t, "editor lone block", editorWire("", startScope(true, 0, 0), "")+editorProof(`
  var rows = [["h2", "<h2>đi</h2>"], ["list", "<ul>\n<li>đi</li>\n</ul>"], ["quote", "<blockquote>\n<p>đi</p>\n</blockquote>"]], failed = [];
  for (var i = 0; i < rows.length; i++) {
    area.innerHTML = "";
    area.dispatchEvent(new InputEvent("input", { inputType: "deleteContentBackward", bubbles: true }));
    press($(rows[i][0])); type("d"); backspace();
    var kept = area.innerHTML;
    type("đi");
    if (field.value !== rows[i][1]) failed.push(rows[i][0] + ": " + JSON.stringify(field.value) + " (after the Backspace " + JSON.stringify(kept) + ")");
  }
  assert(!failed.length, "a lone block whose only letter is deleted stays, the next letter in it; got " + failed.join("; "));
`))
}

// Two lists of a kind, or two quotes, that the browser's own Backspace leaves side by side (a paragraph
// joined to the item before it, the list after it left beside) are one, the caret where the browser
// put it: a numbered list goes on counting, and the post holds one list.
// CONTROL: against a copy of v2.0.0.js whose normalise() joined nothing (the file before the review of
// 20/10/2026), "the two lists are one" failed (two <ol>).
func TestBrowserEditorJoinsBlocksSideBySide(t *testing.T) {
	runEditorProof(t, "editor side by side", editorWire("<ol><li>một</li></ol><p>hai</p><ol><li>ba</li></ol><blockquote><p>bốn</p></blockquote><p>năm</p><blockquote><p>sáu</p></blockquote>", startScope(false, 6, 17), "")+editorProof(`
  caretAt("hai"); backspace();
  assert(area.innerHTML.indexOf("<ol><li>mộthai</li><li>ba</li></ol>") === 0, "the browser's Backspace joined hai to the item before: the two lists are one, got " + area.innerHTML);
  type("X");
  assert(field.value.indexOf("<ol>\n<li>mộtXhai</li>\n<li>ba</li>\n</ol>") === 0, "the caret stays where the browser put it, got " + field.value);
  caretAt("năm"); backspace();
  assert(area.innerHTML.endsWith("<blockquote><p>bốnnăm</p><p>sáu</p></blockquote>"), "and two quotes, got " + area.innerHTML);
`))
}

// During a composition the editor cancels what it does itself and does it once the composition ends,
// in order: Control+B (as the toolbar's Bold waits), an Enter a keyboard sent without isComposing, a
// paste, a drop. Nothing touches the area meanwhile, and nothing is left to the browser to put in it.
// CONTROL: against a copy of v2.0.0.js whose beforeinput, keydown and paste listeners returned during a
// composition (the file before the review of 20/10/2026), "Control+B during a composition is the
// editor's" failed; with that row and "and it waits" skipped (a copy of this file through -overlay
// too), "at its end the bold waits at the caret" failed (the Control+B was lost); with the four rows of
// the bold skipped, "an Enter during a composition is the editor's" failed, and with that one skipped
// as well, "a paste during a composition is the editor's".
func TestBrowserEditorWaitsForTheComposition(t *testing.T) {
	runEditorProof(t, "editor waits for a composition", editorWire("<p>xin chào</p>", startScope(false, 2, 8), "")+editorProof(`
  caretAt("chào", true);
  area.dispatchEvent(new CompositionEvent("compositionstart", { data: "" }));
  var t = find("chào")[0]; t.appendData(" việt");
  var during = area.innerHTML;
  assert(key(area, "b", { ctrlKey: true, isComposing: true }).defaultPrevented, "Control+B during a composition is the editor's");
  assert(area.innerHTML === during && pressed("bold") === "false", "and it waits, got " + area.innerHTML + " / " + pressed("bold"));
  select(t, t.length);
  area.dispatchEvent(new CompositionEvent("compositionend", { data: "việt" }));
  assert(pressed("bold") === "true", "at its end the bold waits at the caret, got " + pressed("bold"));
  type("nam");
  assert(field.value === "<p>xin chào việt<strong>nam</strong></p>", "and what is typed next is bold, got " + field.value);

  area.dispatchEvent(new CompositionEvent("compositionstart", { data: "" }));
  t = find("nam")[0]; t.appendData(" ơi");
  during = area.innerHTML;
  assert(beforeinput("insertParagraph").defaultPrevented, "an Enter during a composition is the editor's");
  assert(pasted({ "text/html": '<p>dán<input name="injected"></p>', "text/plain": "dán" }).defaultPrevented, "a paste during a composition is the editor's");
  var drop = new DataTransfer(); drop.setData("text/plain", "thả");
  assert(beforeinput("insertFromDrop", { dataTransfer: drop }).defaultPrevented, "a drop during a composition is the editor's");
  assert(area.innerHTML === during && field.value === "<p>xin chào việt<strong>nam</strong></p>", "nothing is touched meanwhile, got " + area.innerHTML + " / " + field.value);
  select(t, t.length);
  area.dispatchEvent(new CompositionEvent("compositionend", { data: "ơi" }));
  assert(field.value === "<p>xin chào việt<strong>nam ơi</strong></p>\n<p>dánthả</p>", "at its end: the words read, then the Enter, the paste and the drop, got " + field.value);
  assert(!area.querySelector("input"), "the paste's input never reached the area, got " + area.innerHTML);
`))
}

// The Enter that ends a composition in the address (an input method's commit) only ends it: no link,
// the popover open, and the key still cancelled, so it never submits the form. The next Enter links.
// CONTROL: against a copy of v2.0.0.js whose address keydown took every Enter as link(address) (the
// file before the review of 20/10/2026), "the composing Enter only ends the composition" failed (the
// link made, the popover closed); against one that returned early on a composing Enter without
// cancelling it, the same row failed on the Enter left uncancelled.
func TestBrowserEditorAddressEnterEndsAComposition(t *testing.T) {
	runEditorProof(t, "editor address composition", editorWire("<p>hello world</p>", startScope(false, 2, 11), "")+editorProof(`
  var input = $("address"), pop = $("pop");
  selectWord("world"); press($("link"));
  input.value = "/tin-tuc"; input.dispatchEvent(new Event("input", { bubbles: true }));
  var enter = key(input, "Enter", { isComposing: true });
  assert(enter.defaultPrevented && !pop.hidden && !area.querySelector("a"), "the composing Enter only ends the composition, got cancelled " + enter.defaultPrevented + ", hidden " + pop.hidden + ", " + area.innerHTML);
  enter = key(input, "Enter");
  assert(enter.defaultPrevented && pop.hidden && field.value === '<p>hello <a href="/tin-tuc">world</a></p>', "the next Enter links, got " + field.value);
`))
}

// What an SVG or a MathML element holds is pasted as richtext.Clean reads it: not at all. The tag of a
// foreign element is lower case (svg, math), so it is told apart by its namespace, and goes whole: its
// style sheet and its script are no words, a link in a MathML text no link.
// CONTROL: against a copy of v2.0.0.js that looked an element up by its tag alone (the file before the
// review of 20/10/2026), every row failed: the svg's style and text, its script, the MathML link and the
// svg's desc and title were pasted as words.
func TestBrowserEditorPasteDropsForeignElements(t *testing.T) {
	rows := []string{
		`<p>keep</p><svg><style>p{color:red}</style><text>svgtext</text></svg>`,
		`<p>keep</p><svg><script>window.ran = 1</script>scripttext</svg>`,
		`<p>keep</p><math><mtext><a href="https://kitwork.io/y">mathlink</a></mtext></math>`,
		`<p>keep</p><svg><desc><b>descbold</b></desc><title>titletext</title><foreignObject><p>foreign</p></foreignObject></svg>`,
	}
	for _, row := range rows {
		if clean := richtext.Clean(row).HTML; clean != "<p>keep</p>" {
			t.Fatalf("richtext.Clean(%q) = %q: the rows are written for <p>keep</p>", row, clean)
		}
	}
	runEditorProof(t, "editor foreign paste", editorWire("<p>start</p>", startScope(false, 1, 5), "")+editorProof(`
  var rows = `+jsonOf(rows)+`, failed = [];
  for (var i = 0; i < rows.length; i++) {
    caretAt("start", true); pasted({ "text/html": rows[i] });
    if (field.value !== "<p>startkeep</p>") failed.push(i + " " + field.value.slice(25, 60));
    $("undo").click();
  }
  assert(!failed.length && !window.ran, "what richtext keeps of each, <p>keep</p>, into the line, and nothing ran; rows failed (with what followed <p>keep</p>): " + failed.join("; ") + ", ran " + window.ran);
`))
}
