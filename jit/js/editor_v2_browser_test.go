package js

import (
	"encoding/json"
	"html"
	"strings"
	"testing"
	"unicode"

	"github.com/kitwork/engine/utilities/richtext"
)

// The editor's commands of grammar v2 (20/10/2026: underline, strikethrough, code in a line and in a
// block, headings 4 to 6, alignment, a rule) in a real browser, on a page written by hand the way the
// .ui engine writes <editor> with the canonical toolbar of docs/components/editor.md: groups
// (role="group", aria-label) inside one toolbar, and a <dropdown> in the headings group whose popover
// holds H4 to H6, which call the editor through its alias.

// editorWireV2 is one editor with the canonical toolbar of grammar v2, holding value (already in the
// grammar), its field carrying extra, wired as ui/editor.go wires toolbar v2 (21/10/2026): a group
// role="group" class editor-group, an alignment data-kitwork-command="align-…" with aria-pressed, the
// dropdown's trigger one stop of the toolbar's tab order and the buttons in its popover none (no
// tabindex). The dropdown is the dropdown component's own host, its popover hidden and shown with its
// open, as ui/compile.go writes one.
func editorWireV2(value, scope, extra string) string {
	tab := "0"
	button := func(id, kind, name, call, more string) string {
		attr := `data-kitwork-command="` + name + `"`
		if kind == "format" {
			attr = `data-kitwork-format="` + name + `" aria-pressed="false"`
		}
		out := `<button type="button" id="` + id + `" tabindex="` + tab + `" ` + attr + more + ` data-kitwork-event="click: ` + call + `">` + id + `</button>`
		tab = "-1"
		return out
	}
	// the popover's buttons are not stops of the toolbar: they keep no tabindex of it
	inPopover := func(id, name, call string) string {
		return `<button type="button" id="` + id + `" data-kitwork-format="` + name + `" aria-pressed="false" data-kitwork-event="click: ` + call + `">` + id + `</button>`
	}
	group := func(label string, buttons ...string) string {
		return `<div class="flex-row gap-1 editor-group" role="group" aria-label="` + label + `">` + strings.Join(buttons, "") + `</div>`
	}
	toolbar := `<div id="toolbar" class="flex-row wrap-true gap-3 editor-toolbar" role="toolbar" aria-label="Định dạng" aria-controls="area">` +
		group("Kiểu chữ",
			button("bold", "format", "bold", "toggle('bold')", ` aria-keyshortcuts="Control+B"`),
			button("italic", "format", "italic", "toggle('italic')", ` aria-keyshortcuts="Control+I"`),
			button("underline", "format", "underline", "toggle('underline')", ` aria-keyshortcuts="Control+U"`),
			button("strike", "format", "strikethrough", "toggle('strikethrough')", ""),
			button("code", "format", "code", "toggle('code')", "")) +
		group("Tiêu đề",
			button("h2", "format", "heading-2", "toggle('heading', 2)", ""),
			button("h3", "format", "heading-3", "toggle('heading', 3)", ""),
			`<div id="more" class="flex-column place-anchor" data-kitwork-component="dropdown">`+
				`<button type="button" id="more-button" tabindex="-1" aria-label="Tiêu đề nhỏ hơn" data-kitwork-event="click: toggle()" data-kitwork-bind="aria-expanded: open">v</button>`+
				`<div id="more-popover" class="popover flex-column" hidden data-kitwork-bind="show: open">`+
				inPopover("h4", "heading-4", "$body.toggle('heading', 4)")+
				inPopover("h5", "heading-5", "$body.toggle('heading', 5)")+
				inPopover("h6", "heading-6", "$body.toggle('heading', 6)")+`</div></div>`) +
		group("Khối",
			button("quote", "format", "quote", "toggle('quote')", ""),
			button("codeblock", "format", "code-block", "toggle('code', 'block')", "")) +
		group("Danh sách",
			button("list", "format", "list", "toggle('list')", ""),
			button("ordered", "format", "list-ordered", "toggle('list', 'ordered')", "")) +
		group("Căn lề",
			button("left", "command", "align-left", "align('left')", ` aria-pressed="false"`),
			button("center", "command", "align-center", "align('center')", ` aria-pressed="false"`),
			button("right", "command", "align-right", "align('right')", ` aria-pressed="false"`)) +
		group("Chèn",
			button("link", "command", "link", "link()", ` aria-keyshortcuts="Control+K" aria-controls="pop" aria-haspopup="dialog" data-kitwork-bind="aria-expanded: open" aria-disabled="true"`),
			button("rule", "command", "rule", "rule()", "")) +
		group("Lịch sử",
			button("undo", "command", "undo", "undo()", ` aria-keyshortcuts="Control+Z" aria-disabled="true"`),
			button("redo", "command", "redo", "redo()", ` aria-keyshortcuts="Control+Shift+Z" aria-disabled="true"`)) +
		`</div>`
	tab = "-1"
	return `<label id="label">Nội dung</label>
<div class="editor" id="host" data-kitwork-component="editor: $body" data-kitwork-scope="` + scope + `">
` + toolbar + `
<div id="pop" class="popover editor-popover" role="dialog" aria-label="Địa chỉ liên kết" hidden data-kitwork-bind="show: open">
<input id="address" class="editor-address" type="text" inputmode="url" autocomplete="off" autocapitalize="none" spellcheck="false" aria-label="Địa chỉ liên kết" data-kitwork-bind="value: address">` +
		button("apply", "command", "link", "link(address)", "") +
		button("unlink", "command", "unlink", "unlink()", "") +
		button("cancel", "command", "close", "close()", "") + `
<p id="error" class="error editor-address-error" hidden>Chỉ nhận https://, http://, mailto:, tel: hay đường dẫn bắt đầu bằng /.</p>
</div>
<div id="area" class="richtext editor-area" role="textbox" aria-multiline="true" aria-labelledby="label" aria-placeholder="Viết bài ở đây…" aria-required="true" aria-readonly="true" translate="no" spellcheck="true" data-kitwork-name="body">` + value + `</div>
<textarea id="field" class="editor-field" name="body"` + extra + `>` + html.EscapeString(value) + `</textarea>
</div>`
}

// editorV2Script is the hands the v2 proofs add: the dropdown opened from its own button, whether it
// is open, the disabled state of a button.
const editorV2Script = `
function dropdownOpen() { return !$("more-popover").hidden && window.kit.scopeFor($("more")).open === true; }
function openMore() { $("more-button").click(); }
function off(id) { return $(id).getAttribute("aria-disabled") === "true"; }
`

// runEditorV2Proof is runEditorProof with the v2 hands.
func runEditorV2Proof(t *testing.T, label, body string) {
	t.Helper()
	runEditorProof(t, label, strings.Replace(body, "<script>", "<script>"+editorV2Script, 1))
}

// A code block: toggled on over two lines it is one block, their marks and links gone; in it Enter and
// Shift+Enter write a newline (two at the end, so the new line shows), Enter on an empty last line
// leaves it with a paragraph after, a paste is its words as written, the marks, the link and the
// alignments are off; Backspace at its start and the button again make it a paragraph whose lines are
// line breaks.
// CONTROL, against copies of v2.0.0.js through -overlay (run 21/10/2026): with enter() taking a code
// block for a paragraph (no newline()), "Enter at its end writes a newline and one more" failed (the
// block split in two, the new one empty); with uncode() leaving a kept caret in the text it split,
// "made a paragraph, the caret stays after its words, on its line" failed (the ! went to the end of the
// line before).
func TestBrowserEditorCodeBlock(t *testing.T) {
	runEditorV2Proof(t, "editor code block", editorWireV2(`<p>một <strong>hai</strong> <a href="/x">ba</a></p><p>bốn</p><p>năm</p>`, startScope(false, 5, 18), "")+editorProof(`
  var a = find("một"), b = find("bốn"); select(a[0], 1, b[0], 1); press($("codeblock"));
  assert(field.value === "<pre><code>một hai ba\nbốn</code></pre>\n<p>năm</p>", "two lines are one code block, their marks and link gone, got " + field.value);
  assert(pressed("codeblock") === "true" && off("bold") && off("italic") && off("underline") && off("strike") && off("code") && off("link") && off("center"), "in it the marks, the link and the alignments are off");
  assert(!off("h2") && !off("quote") && !off("rule"), "the blocks and the rule are not");
  caretAt("bốn", true);
  assert(beforeinput("insertParagraph").defaultPrevented && field.value === "<pre><code>một hai ba\nbốn\n\n</code></pre>\n<p>năm</p>", "Enter at its end writes a newline and one more, so the new line shows, got " + JSON.stringify(field.value));
  type("sáu");
  assert(field.value.indexOf("<pre><code>một hai ba\nbốn\nsáu") === 0, "typing goes on the new line, got " + JSON.stringify(field.value));
  caretAt("hai", true); beforeinput("insertParagraph");
  assert(field.value.indexOf("<pre><code>một hai\n ba\nbốn") === 0, "Enter in a code block writes a newline, got " + JSON.stringify(field.value));
  caretAt("bốn", true); beforeinput("insertLineBreak");
  assert(field.value.indexOf("<pre><code>một hai\n ba\nbốn\n\nsáu") === 0, "and so does Shift+Enter, got " + JSON.stringify(field.value));
  caretAt("sáu", true); beforeinput("insertParagraph"); beforeinput("insertParagraph");
  assert(/^<pre><code>một hai\n ba\nbốn\n\nsáu<\/code><\/pre>\n<p>năm<\/p>$/.test(field.value), "Enter on an empty last line leaves the block, got " + JSON.stringify(field.value));
  type("bảy");
  assert(/<\/code><\/pre>\n<p>bảy<\/p>\n<p>năm<\/p>$/.test(field.value), "a paragraph after it, got " + JSON.stringify(field.value));
  caretAt("ba", true);
  pasted({ "text/html": "<b>đậm</b><script>window.ran = 1<\/script>", "text/plain": "x  <y>\r\nz" });
  assert(field.value.indexOf("<pre><code>một hai\n bax  &lt;y&gt;\nz\nbốn\n\nsáu</code></pre>") === 0, "a paste in it is its words as written, got " + JSON.stringify(field.value));
  assert(!area.querySelector("pre b, pre strong, script") && !window.ran, "and nothing else");
  caretAt("hai", true); pasted({ "text/plain": "  " });
  assert(field.value.indexOf("<pre><code>một hai  \n bax") === 0, "white space alone pasted in it stays, got " + JSON.stringify(field.value));
  caretAt("z", true); press($("codeblock")); type("!");
  assert(field.value.indexOf("<p>một hai<br>bax &lt;y&gt;<br>z!<br>bốn") === 0, "made a paragraph, the caret stays after its words, on its line, got " + JSON.stringify(field.value));
  press($("codeblock"));
  assert(field.value.indexOf("<pre><code>một hai  \n bax  &lt;y&gt;\nz!\nbốn") === 0, "and a code block again, got " + JSON.stringify(field.value));
  caretAt("một");
  assert(backspace().defaultPrevented && area.querySelector("pre") === null, "Backspace at its start makes it a paragraph, got " + area.innerHTML);
  assert(field.value.indexOf("<p>một hai<br>bax &lt;y&gt;<br>z") === 0, "its lines are line breaks, got " + JSON.stringify(field.value));
  caretAt("một"); press($("codeblock")); press($("codeblock"));
  assert(field.value.indexOf("<p>một hai<br>bax &lt;y&gt;<br>z") === 0 && pressed("codeblock") === "false", "and so is the button again, got " + JSON.stringify(field.value));
  caretAt("bảy"); press($("codeblock")); beforeinput("insertParagraph");
  assert(field.value.endsWith("</p>\n<pre><code>\nbảy</code></pre>\n<p>năm</p>"), "Enter at the start of its line writes a newline before it, got " + JSON.stringify(field.value));
  caretAt("năm", true); beforeinput("insertParagraph"); press($("codeblock"));
  assert(area.lastElementChild.outerHTML === "<pre><code><br></code></pre>", "an empty line made a code block holds the caret's place, got " + area.innerHTML);
  beforeinput("insertParagraph");
  assert(area.lastElementChild.outerHTML === "<p><br></p>" && area.querySelectorAll("pre").length === 1, "Enter in an empty code block makes it a paragraph again, got " + area.innerHTML);
`))
}

// Underline, strikethrough and code in a line are marks like bold: on a selection, off when all of it
// has them, waiting at a caret; written nested as richtext nests them (a > strong > em > u > s > code),
// whatever order they were given in; Control+U is underline (it was refused before grammar v2), and so
// is the browser's formatUnderline.
// CONTROL, against copies of v2.0.0.js through -overlay (run 21/10/2026): with KEYS refusing u again
// (u: []), "Control+U underlines" failed; with MARKS nesting code outside s, "nested as richtext nests
// them" failed.
func TestBrowserEditorUnderlineStrikeCode(t *testing.T) {
	runEditorV2Proof(t, "editor marks v2", editorWireV2(`<p>một hai ba bốn</p>`, startScope(false, 4, 15), "")+editorProof(`
  selectWord("hai"); press($("code")); press($("strike")); press($("underline")); press($("bold"));
  assert(field.value === "<p>một <strong><u><s><code>hai</code></s></u></strong> ba bốn</p>", "nested as richtext nests them, got " + field.value);
  assert(pressed("code") === "true" && pressed("strike") === "true" && pressed("underline") === "true" && pressed("italic") === "false", "and pressed, got " + pressed("code") + pressed("strike") + pressed("underline"));
  press($("strike"));
  assert(field.value === "<p>một <strong><u><code>hai</code></u></strong> ba bốn</p>" && pressed("strike") === "false", "off again, got " + field.value);
  selectWord("ba");
  assert(key(area, "u", { ctrlKey: true }).defaultPrevented && field.value === "<p>một <strong><u><code>hai</code></u></strong> <u>ba</u> bốn</p>", "Control+U underlines, got " + field.value);
  selectWord("bốn");
  assert(beforeinput("formatUnderline").defaultPrevented && beforeinput("formatStrikeThrough").defaultPrevented && field.value.endsWith(" <u><s>bốn</s></u></p>"), "and so do the browser's formatUnderline and formatStrikeThrough, got " + field.value);
  caretAt("bốn", true); press($("code")); press($("underline")); press($("strike")); type("x");
  assert(field.value.endsWith(" <u><s>bốn</s></u><code>x</code></p>") && pressed("code") === "true", "at a caret code waits for what is typed, the two marks it was in toggled off, got " + field.value);
`))
}

// Headings 4 to 6 from a dropdown's popover in the toolbar, called through the alias: a press there
// keeps the selection, its aria-pressed follows the caret, and the dropdown closes once the command is
// done. align() sets the paragraphs and headings of the selection, its buttons pressed by the caret's
// block and off in a list item; rule() puts a rule after the caret's block, splitting a list, with an
// empty paragraph after it when it is the last, for the caret: the value keeps that rule (21/10/2026: a
// rule that ends the post is kept) and leaves the empty paragraph out.
// CONTROL (21/10/2026, a copy of v2.0.0.js through -overlay, controls.py in the scratchpad's
// final-rule): with read() dropping a rule at the end again (its kept.pop() put back), "a rule that ends
// the post is in the value at once" failed.
// CONTROL, against copies of v2.0.0.js through -overlay (run 21/10/2026): with the format buttons found
// by context.owned (a nested component's left out), "H4's aria-pressed follows the caret" failed;
// without the click listener on window, "the dropdown closes once the command is done" failed; with
// update() never turning an alignment off, "an item takes no alignment: its buttons are off" failed;
// with block() putting every line in one quote (as it did before grammar v2), "a quote over rules: a
// rule between two lines keeps its place" failed.
func TestBrowserEditorHeadingsAlignRule(t *testing.T) {
	runEditorV2Proof(t, "editor headings align rule", editorWireV2(`<p>một</p><p>hai</p><ul><li>ba</li><li>bốn</li></ul>`, startScope(false, 4, 12), "")+editorProof(`
  caretAt("một"); openMore(); await wait(20);
  assert(dropdownOpen(), "the dropdown opens from its own button");
  press($("h4")); await wait(20);
  assert(field.value.indexOf("<h4>một</h4>") === 0, "H4 from the dropdown, through the alias, got " + field.value);
  assert(pressed("h4") === "true" && pressed("h5") === "false", "H4's aria-pressed follows the caret, got " + pressed("h4") + pressed("h5"));
  assert(!dropdownOpen(), "the dropdown closes once the command is done");
  assert(getSelection().anchorNode === find("một")[0], "the caret stays in its words");
  openMore(); await wait(20); press($("h6")); await wait(20);
  assert(field.value.indexOf("<h6>một</h6>") === 0 && pressed("h6") === "true" && pressed("h4") === "false", "H6, got " + field.value);
  caretAt("hai");
  assert(pressed("h6") === "false" && pressed("left") === "true" && pressed("center") === "false", "the caret in a paragraph, got " + pressed("h6") + pressed("left"));
  press($("center"));
  assert(field.value === '<h6>một</h6>\n<p class="align-center">hai</p>\n<ul>\n<li>ba</li>\n<li>bốn</li>\n</ul>' && pressed("center") === "true" && pressed("left") === "false", "align center, got " + field.value);
  var a = find("một"), b = find("hai"); select(a[0], 0, b[0], 2); press($("right"));
  assert(field.value.indexOf('<h6 class="align-right">một</h6>\n<p class="align-right">hai</p>') === 0 && pressed("right") === "true", "align right over a heading and a paragraph, got " + field.value);
  caretAt("hai", true); beforeinput("insertParagraph"); type("mới");
  assert(field.value.indexOf('<p class="align-right">hai</p>\n<p class="align-right">mới</p>') > 0, "a new line keeps the alignment, got " + field.value);
  press($("left"));
  assert(field.value.indexOf('<p class="align-right">hai</p>\n<p>mới</p>') > 0, "align left is no class, got " + field.value);
  caretAt("ba");
  assert(off("left") && off("center") && off("right") && pressed("center") === "false", "an item takes no alignment: its buttons are off");
  press($("center"));
  assert(!area.querySelector("li[class]"), "an item takes no alignment, got " + area.innerHTML);
  caretAt("ba"); press($("rule"));
  assert(field.value.endsWith("<ul>\n<li>ba</li>\n</ul>\n<hr>\n<ul>\n<li>bốn</li>\n</ul>"), "a rule after the caret's item splits its list, got " + field.value);
  assert(getSelection().anchorNode === find("bốn")[0].parentNode && getSelection().anchorOffset === 0, "the caret goes after the rule, got " + getSelection().anchorNode.nodeName);
  caretAt("bốn"); press($("rule"));
  assert(area.lastElementChild.outerHTML === "<p><br></p>" && area.lastElementChild.previousElementSibling.tagName === "HR", "a rule after the last block, an empty paragraph after it, got " + area.innerHTML);
  assert(field.value.endsWith("<li>bốn</li>\n</ul>\n<hr>"), "a rule that ends the post is in the value at once, the empty paragraph after it not, got " + field.value);
  type("cuối");
  assert(field.value.endsWith("<li>bốn</li>\n</ul>\n<hr>\n<p>cuối</p>"), "words after it, got " + field.value);
  assert(beforeinput("insertHorizontalRule").defaultPrevented && area.querySelectorAll("hr").length === 3 && field.value.endsWith("<p>cuối</p>\n<hr>"), "the browser's insertHorizontalRule is rule(), got " + field.value);
  caretAt("một");
  assert(beforeinput("formatJustifyCenter").defaultPrevented && field.value.indexOf('<h6 class="align-center">một</h6>') === 0, "and its formatJustifyCenter align('center'), got " + field.value);
  a = find("mới"); b = find("cuối"); select(a[0], 0, b[0], 2); press($("quote"));
  assert(field.value.endsWith("<blockquote>\n<p>mới</p>\n<p>ba</p>\n</blockquote>\n<hr>\n<blockquote>\n<p>bốn</p>\n</blockquote>\n<hr>\n<blockquote>\n<p>cuối</p>\n</blockquote>\n<hr>"), "a quote over rules: a rule between two lines keeps its place, got " + field.value);
  press($("codeblock"));
  assert(field.value.endsWith("<pre><code>mới\nba</code></pre>\n<hr>\n<pre><code>bốn</code></pre>\n<hr>\n<pre><code>cuối</code></pre>\n<hr>"), "and so does a code block, got " + JSON.stringify(field.value));
`))
}

// The toolbar of groups is one toolbar: one stop for Tab, the arrows, Home and End along all its
// groups; the dropdown's own button is one of its stops, the buttons in its popover are not (they keep
// the tabindex the page gave them, for Tab once the dropdown is open).
// CONTROL: against a copy of v2.0.0.js whose toolbar took every button in it for a stop (through
// -overlay, run 21/10/2026), "ArrowRight from the dropdown's button skips its popover" failed: the next
// stop was H4, hidden in the closed popover, and the focus stayed on the dropdown's button; against one
// whose Alt+F10 took the first button with tabindex 0, "Alt+F10 goes to the toolbar's stop, never a
// popover's button" failed (the focus stayed in the area).
func TestBrowserEditorGroupedToolbarKeyboard(t *testing.T) {
	runEditorV2Proof(t, "editor grouped toolbar", editorWireV2("<p>hello world</p>", startScope(false, 2, 11), "")+editorProof(`
  var bar = $("toolbar"), stops = function () { return Array.prototype.filter.call(bar.querySelectorAll("button"), function (b) { return b.tabIndex === 0; }).map(function (b) { return b.id; }).join(); };
  assert(stops() === "bold,h4,h5,h6", "the first button is the toolbar's stop, the popover's keep theirs, got " + stops());
  selectWord("world"); area.focus();
  key(area, "F10", { altKey: true });
  assert(active() === "bold", "Alt+F10, got " + active());
  key($("code"), "ArrowRight");
  assert(active() === "h2", "ArrowRight goes on into the next group, got " + active());
  key($("h3"), "ArrowRight");
  assert(active() === "more-button", "the dropdown's button is a stop, got " + active());
  key($("more-button"), "ArrowRight");
  assert(active() === "quote", "ArrowRight from the dropdown's button skips its popover, got " + active());
  focusIn($("quote"));
  assert(stops() === "h4,h5,h6,quote", "the stop goes with it, got " + stops());
  key($("quote"), "End");
  assert(active() === "redo", "End, got " + active());
  key($("redo"), "ArrowRight");
  assert(active() === "bold", "round, got " + active());
  key($("h4"), "ArrowRight");
  assert(active() === "bold", "an arrow in the popover moves nothing of the toolbar");
  assert(key($("bold"), "Escape").defaultPrevented && active() === "area" && getSelection().toString() === "world", "Escape back to the area, got " + active());
  $("h4").setAttribute("tabindex", "0"); focusIn($("quote")); key($("quote"), "Escape");
  assert(key(area, "F10", { altKey: true }).defaultPrevented && active() === "quote", "Alt+F10 goes to the toolbar's stop, never a popover's button, got " + active());
`))
}

// A paste of grammar v2: Google Docs and Word say underline, line-through and a monospace face in a
// span's style (u, s, code: the editor's mapping, richtext reads no span), and alignment in a
// paragraph's (the align class, as richtext reads it); a link's own underline is the link's. A web page
// with the elements of v2 (h4–h6, center, ins, del, kbd, tt, samp, pre, hr, code in and around a link)
// is read exactly as richtext.Clean reads it. One paragraph pasted into an empty line gives the line the
// alignment it says, as two pasted would; pasted among words it is words, the line keeping its own.
// A code block's carriage returns written as references are newlines over its whole text, as
// richtext.Clean makes them (the review of 21/10/2026: the editor made each text's apart, so a CR and
// the LF in the next text were two).
// CONTROL, against copies of v2.0.0.js through -overlay (run 21/10/2026): reading no text-decoration,
// "Google Docs" failed (its underline and line-through lost); reading the align attribute on any element
// (a table's too), "a web page as richtext.Clean reads it" failed; with paste() leaving the line's class
// as it was (the file before the integration of 21/10/2026), "one centred paragraph pasted into an empty
// line" failed (<p>giữa</p>); with the line given the alignment whether empty or not, "and into a line of
// words" failed (align-right). Since 21/10/2026 the reading of style and paste() are in
// parts/editor-paste.js, moved there as they were: against a copy of it reading no text-decoration,
// "Google Docs" failed again. With the editor's read() making each text of a code block LF apart again
// (its whole-text replace gone), "a carriage return written as a reference in a code block" failed
// ("a\n\nb\nc\nd").
func TestBrowserEditorPasteGrammarTwo(t *testing.T) {
	const docs = `<meta charset="utf-8"><b style="font-weight:normal;" id="docs-internal-guid-2f1c"><p dir="ltr" style="line-height:1.38;text-align: center;margin-top:0pt;"><span style="font-size:11pt;font-weight:400;text-decoration:underline;-webkit-text-decoration-skip:none;text-decoration-skip-ink:none;">gạch chân</span><span style="font-weight:400;"> và </span><span style="font-weight:400;text-decoration:line-through;">gạch ngang</span><span style="font-weight:400;"> và </span><span style="font-family:'Courier New',monospace;font-weight:400;">mã</span><span> </span><a href="https://kitwork.io/docs" style="text-decoration:none;"><span style="color:#1155cc;font-weight:400;text-decoration:underline;-webkit-text-decoration-skip:none;">liên kết</span></a></p><p dir="ltr" style="text-align: right;"><span style="font-weight:400;">phải</span></p></b>`
	const word = `<html><body><!--StartFragment--><p class=MsoNormal align=center style='text-align:center'><u><span style='font-size:12.0pt'>Word gạch</span></u><span style='font-size:12.0pt'> <s>xoá</s> <span style='font-family:Consolas'>mã</span><o:p></o:p></span></p><p class=MsoNormal>Dòng hai</p><!--EndFragment--></body></html>`
	const web = "<article><h4>Bốn</h4><h5 style=\"text-align:right\">Năm</h5><h6 align=\"center\">Sáu</h6><center>giữa</center>" +
		"<p>a <ins>thêm</ins> <del>bớt</del> <kbd>Ctrl</kbd> <tt>tt</tt> <samp>s</samp> <strike>x</strike></p><hr class=\"x\" style=\"color:red\">" +
		"<pre class=\"language-go\">func main() {\n\t<b>x</b> := 1 &lt; 2\n}</pre><hr><blockquote style=\"text-align:center\"><p>trích</p><p style=\"text-align:left\">trái</p></blockquote>" +
		"<ul><li>một<pre>mã\nhai</pre></li></ul><table align=\"center\"><tr><td>ô</td><td align=\"right\">phải</td></tr></table><p><a href=\"/x\"><code>mã</code></a> <code><a href=\"/y\">y</a></code></p></article>"
	runEditorV2Proof(t, "editor paste v2", editorWireV2("<p>start</p>", startScope(false, 1, 5), "")+editorProof(`
  caretAt("start", true); pasted({ "text/html": `+jsString(docs)+`, "text/plain": "x" });
  assert(field.value === '<p>start</p>\n<p class="align-center"><u>gạch chân</u> và <s>gạch ngang</s> và <code>mã</code> <a href="https://kitwork.io/docs">liên kết</a></p>\n<p class="align-right">phải</p>', "Google Docs, got " + field.value);
  $("undo").click();
  caretAt("start", true); pasted({ "text/html": `+jsString(word)+` });
  assert(field.value === '<p>start</p>\n<p class="align-center"><u>Word gạch</u> <s>xoá</s> <code>mã</code></p>\n<p>Dòng hai</p>', "Word, got " + field.value);
  $("undo").click();
  caretAt("start", true); pasted({ "text/html": `+jsString(web)+`, "text/plain": "x" });
  assert(field.value === "<p>start</p>\n" + `+jsString(richtext.Clean(web).HTML)+`, "a web page as richtext.Clean reads it, got " + field.value);
  assert(!area.querySelector("[style], hr[class], pre[class], pre b, table, center, ins, del, kbd, tt, samp, strike"), "nothing of its markup stays but the grammar, got " + area.innerHTML);
  $("undo").click();
  caretAt("start"); pasted({ "text/html": "<pre>một\n  hai</pre>" });
  assert(field.value === "<pre><code>một\n  hai</code></pre>\n<p>start</p>", "a code block pasted before a line, got " + JSON.stringify(field.value));
  $("undo").click();
  caretAt("start", true); beforeinput("insertParagraph"); pasted({ "text/html": '<p style="text-align:center">giữa</p>', "text/plain": "giữa" });
  assert(field.value === '<p>start</p>\n<p class="align-center">giữa</p>', "one centred paragraph pasted into an empty line, got " + field.value);
  caretAt("giữa", true); pasted({ "text/html": '<p style="text-align:right">x</p>', "text/plain": "x" });
  assert(field.value === '<p>start</p>\n<p class="align-center">giữax</p>', "and into a line of words: the words, the line's alignment kept, got " + field.value);
  area.innerHTML = "<p>start</p>"; caretAt("start", true); beforeinput("insertParagraph");
  pasted({ "text/html": `+jsString(carriageReturns)+`, "text/plain": "x" });
  assert(field.value === "<p>start</p>\n" + `+jsString(richtext.Clean(carriageReturns).HTML)+`, "a carriage return written as a reference in a code block, as richtext.Clean reads it, got " + JSON.stringify(field.value));
`))
}

// carriageReturns is a code block whose carriage returns are references, one of them with its line
// feed in the next text: richtext.Clean makes each a newline over the whole text (tree.go).
const carriageReturns = "<pre>a&#13;<b>&#10;b</b>&#13;c&#13;&#10;d</pre>"

// The value is richtext's own in grammar v2 too: for every state the commands of v2 reach — the new
// marks across a link and nested, code at a caret, headings from the dropdown, alignment on paragraphs,
// headings and a quote's paragraphs, a code block made, written in, left, pasted into and made a
// paragraph again, rules after a quote and at the end (since 21/10/2026 a rule that ends the post is in
// the value, the empty paragraph after it for the caret is not), the browser's Backspace in that
// paragraph taking the rule and undo bringing it back, the browser's Backspace taking a rule, a web page
// of v2 pasted, the browser's delete across a code block, undo and redo — the field holds exactly
// richtext.Clean of the area as it stands, Clean of the value is the value, and the counts are Clean's
// (checkEditorCorpus, computed here in Go on what the browser dumped).
// CONTROL (21/10/2026, copies of v2.0.0.js through -overlay, controls.py in the scratchpad's
// final-rule): with read() dropping a rule at the end again (its kept.pop() put back), the states "a
// rule at the end: an empty paragraph after it", "undo: the rule back" and "a rule after everything
// typed over" failed the first check. Not a control: with serialize() writing a newline into the text
// for a rule that ends the post (its blocks[i + 1] test gone), this test and the proofs of a final rule
// (TestBrowserEditorHeadingsAlignRule, work's TestBrowserEditorFinalRule*) passed: the
// editor reads its text only for the counts, which leave newlines out; the text itself is held by
// richtext's TestCounts and checkClean, and the editor's is kept the same for parity.
// CONTROL, against copies of v2.0.0.js through -overlay (run 21/10/2026): with MARKS nesting code
// outside s, every state from "code and strikethrough on bold" on (all held both) failed the first two
// checks; with serialize() writing a quote's paragraphs without their class, every state whose value
// held an aligned paragraph of a quote, from the first paint on, failed the first check.
// The one place the counts differ is pinned (checkNewerLetters): Garay letters, which the browser's
// Unicode tables have and Go's do not, make a word for the editor only.
// CONTROL (run 21/10/2026, a copy of this file through -overlay): with that difference not pinned (want
// left as richtext's), checkNewerLetters failed: 2 words for the editor, 1 for richtext.
func TestBrowserEditorValueIsRichtextsOwnV2(t *testing.T) {
	const web = "<div><h5>Năm</h5><p style=\"text-align:center\">giữa <ins>thêm</ins> <kbd>Ctrl</kbd></p><hr><pre>a  b\n\tc &amp; d</pre>" +
		"<blockquote><p align=\"right\">phải</p></blockquote><hr><hr><ul><li>một<pre>x\ny</pre></li></ul><center>tâm</center></div>"
	start := richtext.Clean("<h4 class=\"align-center\">Đầu đề bốn</h4><p>Một <strong>đậm</strong> và <a href=\"https://kitwork.io/a\">liên hệ</a> nữa.</p>" +
		"<pre><code>func main() {\n  x := 1\n}</code></pre><hr><blockquote><p class=\"align-right\">trích dẫn</p></blockquote><ul><li>mục một</li><li>mục hai</li></ul><p>kết bài</p>")
	out := runBrowserDump(t, "editor value v2", editorWireV2(strings.ReplaceAll(start.HTML, ">\n<", "><"), startScope(false, start.Words, start.Characters), "")+`<pre id="corpus"></pre><pre id="newer"></pre>`+editorProof(editorV2Script+`
  var states = [], body = window.kit.scope.$body;
  function record(label) {
    states.push({ label: label, html: area.innerHTML, value: field.value, words: body.words, characters: body.characters, empty: body.empty });
  }
  // all is what Control+A selects: from the first text to the end of the last
  function all() { var texts = [], walker = document.createTreeWalker(area, NodeFilter.SHOW_TEXT); while (walker.nextNode()) texts.push(walker.currentNode); select(texts[0], 0, texts[texts.length - 1], texts[texts.length - 1].length); }
  function browserDelete(forward) { var event = beforeinput(forward ? "deleteContentForward" : "deleteContentBackward"); if (!event.defaultPrevented) document.execCommand(forward ? "forwardDelete" : "delete", false); }
  record("first paint");
  var a = find("Một"), b = find("liên"); select(a[0], 1, b[0], 3); press($("underline")); record("underline across a link");
  selectWord("đậm"); press($("code")); press($("strike")); record("code and strikethrough on bold");
  caretAt("bài", true); press($("code")); type(" x = 1"); record("code at a caret, typed");
  caretAt("Đầu"); openMore(); press($("h5")); record("a heading 5 from the dropdown");
  press($("h5")); record("the heading off: a paragraph, its alignment kept");
  caretAt("trích"); press($("center")); record("a quote's paragraph centred");
  a = find("Đầu"); b = find("trích"); select(a[0], 0, b[0], 2); press($("right")); record("right over two paragraphs, a code block, a rule and a quote");
  press($("left")); record("and left");
  caretAt("x := 1", true); beforeinput("insertParagraph"); record("Enter in a code block");
  type("  y := 2"); record("typed in a code block");
  caretAt("}", true); beforeinput("insertParagraph"); record("Enter at the end of a code block");
  beforeinput("insertParagraph"); record("Enter on its empty last line: out of it");
  type("sau mã"); record("typed after it");
  caretAt("func"); beforeinput("insertLineBreak"); record("Shift+Enter at its start");
  caretAt("main", true); pasted({ "text/html": "<b>đậm</b>", "text/plain": "(a,\r\n  b)" }); record("a paste in a code block");
  a = find("mục một"); b = find("mục hai"); select(a[0], 0, b[0], 3); press($("codeblock")); record("a code block over two items");
  press($("codeblock")); record("and off: a paragraph of line breaks");
  caretAt("trích", true); press($("rule")); record("a rule after a quote's paragraph");
  caretAt("bài", true); press($("rule")); record("a rule at the end: an empty paragraph after it");
  backspace(); record("the browser's Backspace in that paragraph takes the rule");
  $("undo").click(); record("undo: the rule back");
  type("cuối cùng"); record("written after the rule");
  caretAt("cuối cùng"); backspace(); record("the browser's Backspace takes the rule");
  caretAt("nữa", true); pasted({ "text/html": `+jsString(web)+`, "text/plain": "x" }); record("a web page of grammar v2 pasted");
  a = find("y := 2"); b = find("sau mã"); select(a[0], a[1] + 2, b[0], b[1] + 3); browserDelete(); record("the browser's delete across a code block");
  caretAt("trích"); press($("h3")); record("a heading out of a quote");
  caretAt("phải"); openMore(); press($("h6")); record("and another, from the dropdown");
  a = find("func"); b = find("mục một"); select(a[0], a[1], b[0], b[1] + 3); press($("list")); record("a list over a code block, a rule and a heading");
  caretAt("tâm"); press($("codeblock")); caretAt("tâm", true); pasted({ "text/plain": "\t<b>&amp;</b>\n" }); record("text with markup pasted in a code block");
  for (var i = 0; i < 6; i++) { $("undo").click(); record("undo " + (i + 1)); }
  for (i = 0; i < 3; i++) { $("redo").click(); record("redo " + (i + 1)); }
  all(); press($("codeblock")); record("everything a code block");
  press($("codeblock")); record("and a paragraph again");
  all(); type("fresh"); press($("rule")); record("a rule after everything typed over");
  $("corpus").textContent = JSON.stringify(states);
  // letters newer than Go's tables (Garay, Unicode 16): the editor counts what its browser's tables say
  area.innerHTML = "<p>abc</p>"; caretAt("abc", true); type(" \u{10D50}\u{10D51}\u{10D52}");
  $("newer").textContent = JSON.stringify({ value: field.value, words: body.words, characters: body.characters, letter: /\p{L}/u.test("\u{10D50}") });
`))
	checkEditorCorpus(t, out, 35, 28)
	checkNewerLetters(t, out)
}

// checkNewerLetters holds the one place the editor's counts are not richtext's: a letter the browser's
// Unicode tables have and Go's do not (Go 1.26 reads Unicode 15.0.0; Chrome 155 knows Garay, added in
// 16.0) makes a word for the editor and none for richtext. The server's counts (Clean's) are the ones a
// rule on the server reads; the editor's are what the writer sees while writing (docs/components/
// editor.md). The difference is exactly that word, and goes when Go's tables have the letter.
func checkNewerLetters(t *testing.T, out string) {
	t.Helper()
	i := strings.Index(out, `<pre id="newer">`)
	j := strings.Index(out[i+1:], "</pre>")
	var newer struct {
		Value      string `json:"value"`
		Words      int    `json:"words"`
		Characters int    `json:"characters"`
		Letter     bool   `json:"letter"`
	}
	if i < 0 || j < 0 || json.Unmarshal([]byte(html.UnescapeString(out[i+len(`<pre id="newer">`):i+1+j])), &newer) != nil {
		t.Fatalf("the proof wrote no count of newer letters:\n%.2000s", out)
	}
	clean := richtext.Clean(newer.Value)
	want := clean.Words
	if newer.Letter && !unicode.IsLetter(0x10D50) {
		want++ // the browser's tables have Garay, Go's do not
	}
	t.Logf("Garay letters typed: %d words for the editor, %d for richtext (the browser has the letter: %v, Go: %v)", newer.Words, clean.Words, newer.Letter, unicode.IsLetter(0x10D50))
	if newer.Value != "<p>abc \U00010D50\U00010D51\U00010D52</p>" || newer.Words != want || newer.Characters != clean.Characters {
		t.Errorf("Garay letters typed: field %q, counts %d words %d characters (the browser has the letter: %v), richtext's %d %d; want %d words",
			newer.Value, newer.Words, newer.Characters, newer.Letter, clean.Words, clean.Characters, want)
	}
}

// editorV2Start is editorWireV2 holding value, its first state counted as richtext counts it.
func editorV2Start(value, extra string) string {
	c := richtext.Clean(value)
	return editorWireV2(value, startScope(c.Empty(), c.Words, c.Characters), extra)
}

// A caret of the area itself, beside a rule (where Chrome's arrows and a click on the rule leave it: the
// review of 21/10/2026 found every command then acted at the start of the post's first line), is the end
// of the line before the rule for every command, Enter, a paste and a mark waiting for what is typed; a
// selection from there starts at the line after it; the toolbar says what the line before holds. Set
// here by hand (select(area, 3)); TestBrowserEditorRuleCaretByTheKeys takes the same place with real
// keys.
// CONTROL, against copies of v2.0.0.js through -overlay (run 21/10/2026, fix in the scratchpad): with
// within() handing back every range as it is, "the toolbar says what the line before the rule holds"
// failed (left not pressed, center off); with focus() alone no longer moving the caret into a line, the
// toolbar row held and "H2 beside the rule is the line before it" failed (<h2>Mở đầu bài</h2>).
func TestBrowserEditorCaretBesideARule(t *testing.T) {
	const start = "<p>Mở đầu bài</p><p>đoạn hai</p><p>đoạn ba</p><hr><p>đoạn bốn</p>"
	runEditorV2Proof(t, "editor caret beside a rule", editorV2Start(start, "")+editorProof(`
  var start = `+jsString(start)+`, rest = "\n<hr>\n<p>đoạn bốn</p>";
  function beside() { area.innerHTML = start; select(area, 3); }
  beside();
  assert(pressed("left") === "true" && !off("center") && !off("h2"), "the toolbar says what the line before the rule holds, got left " + pressed("left") + ", center off " + off("center"));
  press($("h2"));
  assert(field.value === "<p>Mở đầu bài</p>\n<p>đoạn hai</p>\n<h2>đoạn ba</h2>" + rest, "H2 beside the rule is the line before it, got " + JSON.stringify(field.value));
  beside(); press($("center"));
  assert(field.value === '<p>Mở đầu bài</p>\n<p>đoạn hai</p>\n<p class="align-center">đoạn ba</p>' + rest, "and so is align('center'), got " + JSON.stringify(field.value));
  beside(); beforeinput("insertParagraph"); type("x");
  assert(field.value === "<p>Mở đầu bài</p>\n<p>đoạn hai</p>\n<p>đoạn ba</p>\n<p>x</p>" + rest, "Enter beside the rule: a new line after the line before it, got " + JSON.stringify(field.value));
  beside(); beforeinput("insertLineBreak"); type("y");
  assert(field.value === "<p>Mở đầu bài</p>\n<p>đoạn hai</p>\n<p>đoạn ba<br>y</p>" + rest, "Shift+Enter beside the rule, got " + JSON.stringify(field.value));
  beside(); pasted({ "text/html": "<p>dán vào</p>", "text/plain": "dán vào" });
  assert(field.value === "<p>Mở đầu bài</p>\n<p>đoạn hai</p>\n<p>đoạn badán vào</p>" + rest, "a paste beside the rule, got " + JSON.stringify(field.value));
  beside(); key(area, "u", { ctrlKey: true }); type("z");
  assert(field.value === "<p>Mở đầu bài</p>\n<p>đoạn hai</p>\n<p>đoạn ba<u>z</u></p>" + rest, "Control+U then typing beside the rule, got " + JSON.stringify(field.value));
  beside(); press($("rule"));
  assert(area.innerHTML === "<p>Mở đầu bài</p><p>đoạn hai</p><p>đoạn ba</p><hr><p><br></p><hr><p>đoạn bốn</p>", "rule() beside the rule goes after the line before it, got " + area.innerHTML);
  area.innerHTML = start; var b = find("đoạn bốn"); select(area, 3, b[0], b[1] + 8); press($("bold"));
  assert(field.value === "<p>Mở đầu bài</p>\n<p>đoạn hai</p>\n<p>đoạn ba</p>\n<hr>\n<p><strong>đoạn bốn</strong></p>", "a selection from beside the rule starts at the line after it, got " + JSON.stringify(field.value));
`))
}

// A selection from a line into a code block, taken away by Enter, Shift+Enter or a paste, joins the
// words of the code's first line selected into to the line, as text, and leaves its other lines the code
// block, as the browser's own delete does (the review of 21/10/2026 found the whole block joined the
// line as code in a line, its newlines gone); selected to its end, the block goes.
// CONTROL, against a copy of v2.0.0.js through -overlay (run 21/10/2026, fix in the scratchpad): with
// removeSelection moving every child of the code block into the line again (its code case off), "Enter:
// the rest of the code's first line joins the line" failed ("<p>một</p>\n<p><code>:= 1 bốn := 2</code>
// </p>\n<p>năm sáu</p>").
func TestBrowserEditorSelectionIntoACodeBlock(t *testing.T) {
	const start = "<p>một hai</p><pre><code>ba := 1\nbốn := 2</code></pre><p>năm sáu</p>"
	runEditorV2Proof(t, "editor selection into a code block", editorV2Start(start, "")+editorProof(`
  var start = `+jsString(start)+`;
  function over(to) { area.innerHTML = start; var a = find("một hai"), b = find("ba := 1"); select(a[0], a[1] + 4, b[0], to < 0 ? b[0].length : b[1] + to); }
  over(2); beforeinput("insertParagraph");
  assert(field.value === "<p>một</p>\n<p>:= 1</p>\n<pre><code>bốn := 2</code></pre>\n<p>năm sáu</p>", "Enter: the rest of the code's first line joins the line, its other lines stay the block, got " + JSON.stringify(field.value));
  over(2); beforeinput("insertLineBreak");
  assert(field.value === "<p>một<br>:= 1</p>\n<pre><code>bốn := 2</code></pre>\n<p>năm sáu</p>", "Shift+Enter, got " + JSON.stringify(field.value));
  over(2); pasted({ "text/html": "<p><b>đậm</b></p><p>hai đoạn</p>", "text/plain": "đậm\n\nhai đoạn" });
  assert(field.value === "<p>một</p>\n<p><strong>đậm</strong></p>\n<p>hai đoạn</p>\n<p>:= 1</p>\n<pre><code>bốn := 2</code></pre>\n<p>năm sáu</p>", "a paste of two blocks, got " + JSON.stringify(field.value));
  over(-1); beforeinput("insertParagraph");
  assert(field.value === "<p>một</p>\n<p>năm sáu</p>" && !area.querySelector("pre"), "selected to its end, the block goes, got " + JSON.stringify(field.value));
`))
}

// rule() on an empty line after a block puts the rule in the line's place: the area shows no blank line
// above the rule that the value does not have (the review of 21/10/2026). On the area's only line, or a
// line right after a rule, it goes after the line as before (no two rules side by side).
// CONTROL, against a copy of v2.0.0.js through -overlay (run 21/10/2026, fix in the scratchpad): with
// ruling() leaving the empty line where it is, "the rule takes the empty line's place" failed
// (<p>một</p><p>hai</p><p><br></p><hr><p><br></p>).
func TestBrowserEditorRuleOnAnEmptyLine(t *testing.T) {
	runEditorV2Proof(t, "editor rule on an empty line", editorV2Start("<p>một</p><p>hai</p>", "")+editorProof(`
  caretAt("hai", true); beforeinput("insertParagraph"); press($("rule"));
  assert(area.innerHTML === "<p>một</p><p>hai</p><hr><p><br></p>", "the rule takes the empty line's place, got " + area.innerHTML);
  type("ba");
  assert(area.innerHTML === "<p>một</p><p>hai</p><hr><p>ba</p>" && field.value === "<p>một</p>\n<p>hai</p>\n<hr>\n<p>ba</p>", "typed after it, got " + area.innerHTML);
  area.innerHTML = "<p>một</p><hr><p><br></p>"; select(area.lastChild, 0); press($("rule"));
  assert(area.innerHTML === "<p>một</p><hr><p><br></p><hr><p><br></p>", "an empty line right after a rule stays, got " + area.innerHTML);
  area.innerHTML = "<p><br></p>"; select(area.firstChild, 0); press($("rule"));
  assert(area.innerHTML === "<p><br></p><hr><p><br></p>", "and so does the area's only line, got " + area.innerHTML);
`))
}

// The alignment buttons say there is no line to align while the area has had no caret (aria-disabled,
// from the page's first paint), and a press then aligns nothing, by the mouse or the keyboard (the
// review of 21/10/2026: it aligned the first paragraph). Once there is a caret they work.
// CONTROL, against a copy of v2.0.0.js through -overlay (run 21/10/2026, fix in the scratchpad): with
// align() no longer looking for a caret first, "a press aligns nothing" failed (<p class="align-right">
// một</p>: the first paragraph, aligned by each press).
func TestBrowserEditorAlignWaitsForACaret(t *testing.T) {
	runEditorV2Proof(t, "editor align waits for a caret", editorV2Start("<p>một</p><p>hai</p>", "")+editorProof(`
  assert(off("left") && off("center") && off("right"), "no caret yet: the alignments are off");
  press($("center")); $("right").click();
  assert(field.value === "<p>một</p>\n<p>hai</p>" && !area.querySelector("[class]"), "a press aligns nothing, got " + JSON.stringify(field.value));
  caretAt("hai");
  assert(!off("center"), "a caret in a paragraph: the alignments are on");
  press($("center"));
  assert(field.value === '<p>một</p>\n<p class="align-center">hai</p>', "and they align, got " + JSON.stringify(field.value));
`))
}

// Alt+F10 goes to the toolbar's stop in an editor written inside a popover too (a reply in a dropdown):
// the buttons of a dropdown's popover in the toolbar are not stops, the toolbar's own are (the review of
// 21/10/2026: any popover around the editor took them all away).
// CONTROL, against a copy of v2.0.0.js through -overlay (run 21/10/2026, fix in the scratchpad): with
// Alt+F10 looking for 'button[tabindex="0"]:not(.popover button)' again, the focus stayed in the area
// ("got area") and this test failed.
func TestBrowserEditorAltF10InAPopover(t *testing.T) {
	runEditorV2Proof(t, "editor in a popover, Alt+F10", `<div class="popover" style="position:static">`+editorV2Start("<p>hello world</p>", "")+`</div>`+editorProof(`
  selectWord("world"); area.focus();
  assert(key(area, "F10", { altKey: true }).defaultPrevented && active() === "bold", "Alt+F10 in an editor inside a popover, got " + active());
`))
}
