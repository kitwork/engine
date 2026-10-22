package js

import (
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/kitwork/engine/utilities/richtext"
)

// The editor's formats (21/10/2026, anh Quốc: "tạo cho tôi một số nút giống segment hoặc dropdown để có
// thể tuỳ người dùng viết bằng rich / code hoặc markdown"): the post seen and typed as rich text, as
// Markdown or as HTML, one value on the wire either way (the cleaned HTML of grammar v2). The browser
// side: parts/editor-format.js (the views, the switch, the toolbar in a text surface, the posted
// value, dropped) and parts/editor-markdown.js (Markdown both ways), loaded by the editor when another
// format than rich is first asked for, on a page written by hand the way the compiler is to write it:
// the canonical segment first in the toolbar, its buttons data-kitwork-command="format" with
// data-kitwork-format-value and aria-pressed bound to format == '…' (the kernel's binding, its one
// writer: the parts write none), aria-disabled in an editor that takes no writing, a click that is one
// assignment (format = 'markdown'), the host's scope with format and dropped.

// editorFormatWire is editorWireV2 holding value (its first state counted as richtext counts it) with
// the formats' segment first in its toolbar (<group alt="Viết bằng">, the canonical segment), the
// toolbar's one tab stop its first button, the scope starting in start with dropped empty; extra is
// the field's (required, readonly…).
func editorFormatWire(value, start, extra string) string {
	c := richtext.Clean(value)
	wire := editorWireV2(value, startScope(c.Empty(), c.Words, c.Characters)+"; format: '"+start+"'; dropped: []", extra)
	tab, off := "0", ""
	if strings.Contains(extra, "readonly") || strings.Contains(extra, "disabled") {
		off = ` aria-disabled="true"` // as ui/editor.go writes a switch of an editor that keeps its format
	}
	button := func(format, label string) string {
		out := `<button type="button" id="f-` + format + `" tabindex="` + tab + `" class="active:background-brand/15" data-kitwork-command="format" data-kitwork-format-value="` + format + `"` + off +
			` data-kitwork-bind="aria-pressed: format == &#39;` + format + `&#39;" aria-pressed="` + boolWord(format == start) + `" data-kitwork-event="click: format = &#39;` + format + `&#39;">` + label + `</button>`
		tab = "-1"
		return out
	}
	group := `<div class="flex-row gap-1 editor-group" role="group" aria-label="Viết bằng">` + button("rich", "Rich") + button("markdown", "Markdown") + button("html", "HTML") + `</div>`
	wire = strings.Replace(wire, `id="bold" tabindex="0"`, `id="bold" tabindex="-1"`, 1)
	return strings.Replace(wire, `aria-controls="area">`, `aria-controls="area">`+group, 1)
}

// editorFormatPage is a form holding that editor, its area drawn with presentation classes and
// described by a hint, and what the page shows of it outside the host: its words, unsaved, and dropped
// (Đã bỏ …, shown while it names something).
func editorFormatPage(value, start, extra string) string {
	wire := strings.Replace(editorFormatWire(value, start, extra), `class="richtext editor-area"`, `class="richtext editor-area padding-4 radius-medium border-line editor-rows-12" aria-describedby="hint"`, 1)
	return `<form id="form" method="post">` + wire + `<p id="hint">Gợi ý</p><b id="words" data-kitwork-bind="text: $body.words"></b>` +
		`<b id="unsaved" data-kitwork-bind="show: $body.unsaved" hidden>Chưa lưu</b>` +
		`<b id="dropped" data-kitwork-bind="show: $body.dropped.length" hidden>Đã bỏ <i id="dropped-words" data-kitwork-bind="text: $body.dropped.join(', ')"></i></b></form>`
}

// editorFormatScript is the hands the format proofs add.
const editorFormatScript = `
Object.defineProperty(window, "body", { get: function () { return window.kit.scope.$body; } }); // read when used: the alias is there once the kernel has booted
var errors = [];
window.addEventListener("error", function (event) { errors.push(String(event.message)); });
function surface() { return document.querySelector("#host .editor-text"); }
async function until(test) { for (var i = 0; i < 250 && !test(); i++) await wait(20); return test(); }
// (what shows, not only what is asked: format and its switch's aria-pressed say a click at once; the
// surface is spellchecked for Markdown alone)
function showing(format) {
  var s = surface();
  return (format === "rich" ? !area.hidden && (!s || s.hidden) : !!s && !s.hidden && area.hidden && s.spellcheck === (format === "markdown")) &&
    body.format === format && $("f-" + format).getAttribute("aria-pressed") === "true";
}
async function to(format) {
  press($("f-" + format));
  assert(await until(function () { return showing(format); }), "the editor shows " + format + ", got format " + body.format + ", the area hidden " + area.hidden + ", a surface " + !!surface() + ", errors " + errors.join(" | "));
}
function typeIn(words) { var s = surface(); s.focus(); document.execCommand("insertText", false, words); }
function within(word) { var s = surface(), at = s.value.indexOf(word); assert(at >= 0, "no " + JSON.stringify(word) + " in " + JSON.stringify(s.value)); s.focus(); s.setSelectionRange(at, at + word.length); }
function caretIn(at) { var s = surface(); s.focus(); s.setSelectionRange(at, at); }
function chosen() { var s = surface(); return s.value.slice(s.selectionStart, s.selectionEnd); }
function partFetched(name) { return performance.getEntriesByType("resource").filter(function (e) { return e.name.indexOf("/_kitwork/parts/" + name + ".") >= 0; }).length; }
async function settled() { await wait(360); }
`

// runEditorFormatProof is runEditorV2Proof with the format hands.
func runEditorFormatProof(t *testing.T, label, body string) {
	t.Helper()
	runEditorV2Proof(t, label, strings.Replace(body, "<script>", "<script>"+editorFormatScript, 1))
}

// The editor names its formats' part with its hash where its source names it without one, the part names
// the Markdown part the same way, both are answered by their exact current names only, and a page links
// neither: the editor loads the one, the one the other.
// The page's check of names reads format and dropped as the editor's (the setter's slot, scope[FORMAT],
// a symbol the reader knows only from a declaration of its own).
// CONTROL (run 21/10/2026, copies through -overlay, controls.py in the scratchpad's editor-formats):
// with PartAsset building a part's file from its source as written (delivery.go, no partsNamed), "the
// formats' part names the Markdown part with its hash" failed; with FORMAT declared beside VIEWS in one
// var (v2.0.0.js), "the editor's names do not hold format and dropped" failed (the names unread).
func TestEditorFormatPartsAreDeliveredByName(t *testing.T) {
	format, markdown := PartAsset("editor-format"), PartAsset("editor-markdown")
	for _, part := range []*Asset{format, markdown} {
		if part == nil || part.Role != "part" || !regexp.MustCompile(`^/_kitwork/parts/editor-(format|markdown)\.[0-9a-f]{16}\.js$`).MatchString(part.Path) {
			t.Fatalf("the part: %+v", part)
		}
		if asset, exact := AssetAt(part.Path, nil); asset != part || !exact {
			t.Errorf("%s is not answered by its exact name", part.Path)
		}
		if asset, _ := AssetAt(part.Path[:len(part.Path)-20]+".0123456789abcdef.js", nil); asset != nil {
			t.Errorf("a name of another build is answered with %s", asset.Path)
		}
	}
	if body := string(format.Body); !strings.Contains(body, `"`+markdown.Path+`"`) || strings.Contains(body, `"/_kitwork/parts/editor-markdown.js"`) {
		t.Errorf("the formats' part names the Markdown part with its hash: want %s in it", markdown.Path)
	}
	if !strings.Contains(string(markdown.Body), `Symbol.for("kitwork:editor-markdown")`) || !strings.Contains(string(format.Body), `Symbol.for("kitwork:editor-format")`) {
		t.Errorf("a part does not define its function under its symbol")
	}
	editor := componentAsset("editor", nil)
	if body := string(editor.Body); !strings.Contains(body, `"`+format.Path+`"`) || strings.Contains(body, `"/_kitwork/parts/editor-format.js"`) {
		t.Errorf("the editor's file names the formats' part with its hash: want %s in it", format.Path)
	}
	page := Deliver(`<html><head></head><body><div data-kitwork-component="editor: $body" data-kitwork-scope="format: 'markdown'"></div></body></html>`, nil, Options{})
	if strings.Contains(page, "/parts/") {
		t.Errorf("a page links a part:\n%s", page)
	}
	// the page's check of names reads format and dropped as the editor's
	if names, ok := ComponentNames(nil)("editor"); !ok || !containsString(names, "format") || !containsString(names, "dropped") {
		t.Errorf("the editor's names do not hold format and dropped: %v (read: %v)", names, ok)
	}
	// a part naming itself keeps its name as written (its own hash would be its own bytes)
	if got := partsNamed(`x("/_kitwork/parts/editor-format.js", "/_kitwork/parts/editor-markdown.js")`, "editor-format"); got != `x("/_kitwork/parts/editor-format.js", "`+markdown.Path+`")` {
		t.Errorf("partsNamed: %s", got)
	}
}

// The views and the switch: nothing of the formats before the first switch; Markdown and HTML in a
// plain-text surface right after the area, wearing its presentation, named as it is, the toolbar
// controlling it, the area hidden, the focus moved to it with the caret near the same words; the
// toolbar's state by format; back to rich, the area focused with the caret near the same words and the
// editor's own state again; the format buttons are stops of the toolbar's row; each part fetched once.
// A selection put in the hidden area (Escape from the toolbar sends the editor's focus there; here the
// page's own hand too) leaves the buttons Markdown's.
// CONTROL (run 21/10/2026, copies of editor-format.js through -overlay, controls.py in the scratchpad's
// editor-formats): with apply() leaving the editor's buttons to its update (no parking of mine.formats
// and mine.commands), "a selection in the hidden area says nothing" failed (bold true, left on; Escape
// alone did not show it: the surface takes the selection back before the editor reads it); with the
// focus left where it was (moving false), "the focus moves to it" failed (BODY).
// CONTROL (run 21/10/2026, the integration's copies of this file through -overlay, c25 and c27 in the
// scratchpad's editor-formats/integrate): with the switches written without their aria-pressed binding
// (the wire before the two sides agreed, the part no longer writing it), "the editor shows markdown"
// failed here and in five other proofs (never pressed); with showing() reading what is asked and not
// what shows (no spellcheck), "HTML: the canonical value" failed (read before the switch was drawn), and
// five other proofs with it.
func TestBrowserEditorFormatViews(t *testing.T) {
	runEditorFormatProof(t, "editor format views", editorFormatPage("<p>Xin <strong>chào</strong> thế giới</p><h2>Tựa</h2>", "rich", " required")+editorProof(`
  assert(!surface() && !partFetched("editor-format") && !partFetched("editor-markdown"), "nothing of the formats before a switch");
  caretAt("chào", true);
  assert(pressed("bold") === "true" && pressed("f-rich") === "true", "rich at the start, bold at the caret");
  await to("markdown");
  var s = surface();
  assert(s.tagName === "TEXTAREA" && s.previousElementSibling === area && s.nextElementSibling === field, "right after the area, got " + s.outerHTML);
  assert(s.className === "editor-text padding-4 radius-medium border-line", "the area's presentation, got " + s.className);
  assert(s.style.minBlockSize === getComputedStyle(area).minBlockSize, "at least as high as the area, got " + s.style.minBlockSize);
  assert(s.id === "text" && $("toolbar").getAttribute("aria-controls") === "text", "the toolbar controls it, got " + $("toolbar").getAttribute("aria-controls"));
  assert(s.getAttribute("aria-labelledby") === "label" && s.getAttribute("aria-describedby") === "hint" && s.getAttribute("aria-required") === "true" && s.placeholder === "Viết bài ở đây…" && !s.name, "named as the area is, posted by none, got " + s.outerHTML);
  assert(s.spellcheck === true && s.value === "Xin **chào** thế giới\n\n## Tựa", "Markdown, spellchecked, got " + JSON.stringify(s.value));
  assert(area.hidden && !s.hidden, "the area hidden");
  assert(document.activeElement === s && s.value.slice(0, s.selectionStart) === "Xin **chào", "the focus moves to it, the caret near the same words, got " + active() + " at " + s.selectionStart);
  assert(pressed("f-markdown") === "true" && pressed("f-rich") === "false" && pressed("f-html") === "false", "aria-pressed says markdown");
  assert(pressed("bold") === "false" && !off("bold") && !off("h4") && off("left") && off("center") && off("right") && pressed("left") === "false" && !off("link") && !off("rule") && !off("undo") && !off("redo"), "markdown's toolbar");
  assert(partFetched("editor-format") === 1 && partFetched("editor-markdown") === 1, "its parts, once each");
  // Escape from the toolbar: the editor puts the selection back in its (hidden) area, whose caret is
  // bold; the buttons stay Markdown's
  assert(key(s, "F10", { altKey: true }).defaultPrevented && active() === "f-rich", "Alt+F10 from the text, got " + active());
  assert(key($("f-rich"), "Escape").defaultPrevented, "Escape on the toolbar");
  await wait(80);
  assert(document.activeElement === s && pressed("bold") === "false" && off("left") && !off("bold"), "back in the text, the hidden area's caret says nothing, got " + active() + ", bold " + pressed("bold") + ", left off " + off("left"));
  // whatever puts the selection in the hidden area (here the page's own hand), the buttons stay Markdown's
  var kept = s.selectionStart;
  select(find("chào")[0], 2);
  await wait(40);
  assert(pressed("bold") === "false" && off("left"), "a selection in the hidden area says nothing, got bold " + pressed("bold") + ", left off " + off("left"));
  caretIn(kept);
  await to("html");
  assert(s.spellcheck === false && s.value === "<p>Xin <strong>chào</strong> thế giới</p>\n<h2>Tựa</h2>" && document.activeElement === s, "HTML: the canonical value, not spellchecked, got " + JSON.stringify(s.value));
  assert(s.value.slice(0, s.selectionStart).slice(-4) === "chào", "the caret near the same words, got " + s.selectionStart);
  assert(off("bold") && off("h2") && off("h5") && off("quote") && off("codeblock") && off("list") && off("link") && off("rule") && off("left") && !off("undo") && !off("redo") && !off("f-rich") && !off("f-markdown"), "HTML's toolbar: every formatting button off, the history and the formats on");
  await to("rich");
  assert(!area.hidden && s.hidden && document.activeElement === area && $("toolbar").getAttribute("aria-controls") === "area", "rich: the area again, focused, controlled");
  var at = getSelection();
  assert(at.anchorNode.data === "chào" && at.anchorOffset === 4, "the caret near the same words, got " + (at.anchorNode && at.anchorNode.textContent) + " " + at.anchorOffset);
  assert(pressed("bold") === "true" && pressed("f-rich") === "true" && pressed("f-html") === "false" && !off("left") && off("link"), "the editor's own state again (a caret outside a link: no link), got bold " + pressed("bold") + " rich " + pressed("f-rich") + " html " + pressed("f-html") + " left off " + off("left") + " link off " + off("link"));
  assert(field.value === "<p>Xin <strong>chào</strong> thế giới</p>\n<h2>Tựa</h2>" && body.dropped.length === 0, "the value as it was, nothing left out");
  // the format buttons are stops of the toolbar's row
  focusIn($("f-rich"));
  key($("f-rich"), "ArrowRight");
  assert(active() === "f-markdown" && $("f-markdown").tabIndex === 0 && $("f-rich").tabIndex === -1, "the arrows go along them, got " + active());
  key($("f-markdown"), "ArrowLeft"); key($("f-rich"), "ArrowLeft");
  assert(active() === "redo", "the row goes round, got " + active());
  assert(partFetched("editor-format") === 1 && partFetched("editor-markdown") === 1 && errors.length === 0, "still once each, no error: " + errors.join(" | "));
`))
}

// A <select value="{{ format }}"> in the editor (two-way: the kernel writes format from it, and it from
// format): a press on it is not kept from it (it opens), a change switches, and the focus stays on it
// (a change of a select moves nothing); the buttons switch it too.
// CONTROL (run 21/10/2026, a copy of v2.0.0.js through -overlay): with the editor's press kept from
// every [data-kitwork-command] (a select too), "a press on the select opens it" failed.
func TestBrowserEditorFormatSelect(t *testing.T) {
	selectBox := `<select id="choice" data-kitwork-command="format" data-kitwork-bind="value: format"><option value="rich">Rich</option><option value="markdown">Markdown</option><option value="html">HTML</option></select>`
	runEditorFormatProof(t, "editor format select", strings.Replace(editorFormatPage("<p>Một <em>hai</em></p>", "rich", ""), `<div id="area"`, selectBox+`<div id="area"`, 1)+editorProof(`
  var choice = $("choice"), down = new PointerEvent("pointerdown", { bubbles: true, cancelable: true }), mouse = new MouseEvent("mousedown", { bubbles: true, cancelable: true });
  choice.dispatchEvent(down); choice.dispatchEvent(mouse);
  assert(!down.defaultPrevented && !mouse.defaultPrevented, "a press on the select opens it");
  assert(choice.value === "rich", "the select reads the format, got " + choice.value);
  choice.focus();
  choice.value = "markdown";
  choice.dispatchEvent(new Event("input", { bubbles: true }));
  choice.dispatchEvent(new Event("change", { bubbles: true }));
  assert(await until(function () { return showing("markdown"); }), "a change switches, got " + body.format);
  assert(document.activeElement === choice && surface().value === "Một *hai*", "the focus stays on the select, got " + active());
  await to("html");
  assert(await until(function () { return choice.value === "html"; }), "the buttons switch it too, got " + choice.value);
  choice.value = "rich";
  choice.dispatchEvent(new Event("input", { bubbles: true }));
  assert(await until(function () { return showing("rich"); }) && document.activeElement === choice, "back to rich from the select, the focus on it");
`))
}

// The toolbar in Markdown: Markdown written around the selection (each again takes it off), whole lines
// for the blocks, a rule line, a link through the same popover (refused addresses refused there, the link
// at the caret changed or taken off), Control+B, the surface's own history for undo and redo, Alt+F10 to
// the toolbar and Escape back; no button pressed. Then HTML: the formatting buttons do nothing.
// CONTROL (run 21/10/2026, copies of editor-format.js through -overlay): with wrap() reading any star
// outside the selection as italic (no count), "italic inside bold" failed (the bold's stars taken for
// italic: Xin *chào*); with put() writing by setRangeText always (not the browser's typing), "undo is
// the surface's own" failed (the bold stayed).
func TestBrowserEditorFormatToolbar(t *testing.T) {
	runEditorFormatProof(t, "editor format toolbar", editorFormatPage("<p>Xin chào thế giới</p><p>hai</p>", "rich", "")+editorProof(`
  await to("markdown");
  var s = surface();
  within("chào"); press($("bold"));
  assert(s.value === "Xin **chào** thế giới\n\nhai" && chosen() === "chào", "bold around the selection, still selected, got " + JSON.stringify(s.value));
  press($("italic"));
  assert(s.value === "Xin ***chào*** thế giới\n\nhai", "italic inside bold, got " + JSON.stringify(s.value));
  press($("bold"));
  assert(s.value === "Xin *chào* thế giới\n\nhai" && chosen() === "chào", "bold off, italic stays, got " + JSON.stringify(s.value));
  press($("italic"));
  press($("underline"));
  assert(s.value === "Xin <u>chào</u> thế giới\n\nhai", "underline, got " + JSON.stringify(s.value));
  press($("underline")); press($("strike"));
  assert(s.value === "Xin ~~chào~~ thế giới\n\nhai", "strikethrough, got " + JSON.stringify(s.value));
  press($("strike")); press($("code"));
  assert(s.value === "Xin `+"`chào`"+` thế giới\n\nhai", "code, got " + JSON.stringify(s.value));
  press($("code"));
  assert(s.value === "Xin chào thế giới\n\nhai" && pressed("bold") === "false" && pressed("code") === "false", "all off again; nothing pressed in plain text");
  caretIn(s.value.indexOf("hai") + 1); press($("h2"));
  assert(s.value === "Xin chào thế giới\n\n## hai", "a heading at the line's start, got " + JSON.stringify(s.value));
  press($("h3"));
  assert(s.value === "Xin chào thế giới\n\n### hai", "another level in its place, got " + JSON.stringify(s.value));
  openMore(); press($("h5"));
  assert(s.value === "Xin chào thế giới\n\n##### hai" && !dropdownOpen(), "from the dropdown, which closes, got " + JSON.stringify(s.value));
  openMore(); press($("h5"));
  assert(s.value === "Xin chào thế giới\n\nhai", "and off, got " + JSON.stringify(s.value));
  s.setSelectionRange(0, s.value.length); press($("quote"));
  assert(s.value === "> Xin chào thế giới\n>\n> hai", "a quote over the lines, got " + JSON.stringify(s.value));
  press($("quote")); press($("list"));
  assert(s.value === "- Xin chào thế giới\n\n- hai", "a list, got " + JSON.stringify(s.value));
  press($("ordered"));
  assert(s.value === "1. Xin chào thế giới\n\n2. hai", "an ordered one in its place, got " + JSON.stringify(s.value));
  press($("ordered")); press($("codeblock"));
  assert(s.value === "`+"```"+`\nXin chào thế giới\n\nhai\n`+"```"+`", "a code block, got " + JSON.stringify(s.value));
  press($("codeblock"));
  assert(s.value === "Xin chào thế giới\n\nhai", "and off, got " + JSON.stringify(s.value));
  caretIn(5); press($("rule"));
  assert(s.value === "Xin chào thế giới\n\n---\n\n\nhai" && s.selectionStart === 23, "a rule after the caret's line, got " + JSON.stringify(s.value) + " at " + s.selectionStart);
  // the link: the same popover, [selection](address)
  within("thế"); press($("link"));
  assert(!$("pop").hidden && body.open && document.activeElement === $("address") && $("address").value === "", "the popover opens, its input focused");
  $("address").value = "javascript:alert(1)"; $("address").dispatchEvent(new Event("input", { bubbles: true }));
  press($("apply"));
  assert(!$("pop").hidden && !$("error").hidden && $("address").getAttribute("aria-invalid") === "true" && s.value.indexOf("](") < 0, "a refused address is refused there");
  $("address").value = "kitwork.io/a"; $("address").dispatchEvent(new Event("input", { bubbles: true }));
  press($("apply"));
  assert($("pop").hidden && s.value === "Xin chào [thế](https://kitwork.io/a) giới\n\n---\n\n\nhai" && document.activeElement === s, "linked, the popover shut, back in the text, got " + JSON.stringify(s.value));
  caretIn(s.value.indexOf("thế") + 1); press($("link"));
  assert($("address").value === "https://kitwork.io/a", "the link at the caret, its address, got " + $("address").value);
  press($("unlink"));
  assert(s.value === "Xin chào thế giới\n\n---\n\n\nhai" && $("pop").hidden, "unlinked, got " + JSON.stringify(s.value));
  within("hai"); press($("link")); press($("cancel"));
  assert($("pop").hidden && document.activeElement === s && chosen() === "hai", "close: the selection back in the text");
  // the keyboard
  within("giới");
  assert(key(s, "b", { ctrlKey: true }).defaultPrevented && s.value === "Xin chào thế **giới**\n\n---\n\n\nhai", "Control+B, got " + JSON.stringify(s.value));
  $("undo").click();
  assert(s.value === "Xin chào thế giới\n\n---\n\n\nhai", "undo is the surface's own, got " + JSON.stringify(s.value));
  $("redo").click();
  assert(s.value === "Xin chào thế **giới**\n\n---\n\n\nhai", "and redo, got " + JSON.stringify(s.value));
  assert(key(s, "F10", { altKey: true }).defaultPrevented && active() === "f-rich", "Alt+F10 to the toolbar, got " + active());
  assert(key($("f-rich"), "Escape").defaultPrevented && document.activeElement === s, "Escape back to the text, got " + active());
  await settled();
  assert(field.value === "<p>Xin chào thế <strong>giới</strong></p>\n<hr>\n<p>hai</p>", "the field follows, got " + JSON.stringify(field.value));
  // HTML: the formatting buttons do nothing; undo and redo are the surface's
  await to("html");
  var before = s.value;
  caretIn(3);
  ["bold", "h2", "quote", "list", "codeblock", "left", "rule", "link"].forEach(function (id) { press($(id)); });
  assert(s.value === before && $("pop").hidden, "HTML: nothing written, no popover, got " + JSON.stringify(s.value));
  caretIn(s.value.length); typeIn("\n<p>x</p>");
  $("undo").click();
  assert(s.value === before, "undo in HTML, got " + JSON.stringify(s.value));
  $("redo").click();
  assert(s.value === before + "\n<p>x</p>", "redo in HTML, got " + JSON.stringify(s.value));
`))
}

// The posted value in Markdown and HTML: the field holds the canonical HTML of the text, at once at the
// first change since the post was saved (unsaved, and the question on leaving, as in rich), then, in a
// long text (2 000 characters or more), 300 ms after the typing stops, when the surface loses the focus,
// on the form's formdata (before the typing stops too); the counts and unsaved follow; an empty post's
// required check marks the surface invalid and gives it the focus; HTML without a tag is read as
// richtext reads such a source. (A short text is read at each input: TestBrowserEditorFormatReadsAsTyped.)
// CONTROL (run 21/10/2026, a copy of editor-format.js through -overlay): with the formdata listener
// gone, "formdata reads the text as it is now" failed (the field's old value posted); with the input
// listener waiting at the empty edge too, "empty at once" failed. Since the review of 21/10/2026 (copies
// through -overlay, fix/controls.py in the scratchpad's editor-formats): with the first change waiting as
// every other (no !scope.unsaved in changed()), "the first change of a long post since it was saved"
// failed; with the focusout listener gone, "the surface losing the focus: at once" failed (<p>a</p>);
// with the formdata listener gone, "formdata reads the text as it is now" failed on the long text.
func TestBrowserEditorFormatPostedValue(t *testing.T) {
	long := strings.TrimSpace(strings.Repeat("Một câu dài. ", 170))
	start := richtext.Clean("<p>" + long + "</p>")
	runEditorFormatProof(t, "editor format posted value", editorFormatPage(start.HTML, "rich", " required")+editorProof(`
  var long = `+jsString(long)+`, words = `+itoa(start.Words)+`;
  await to("markdown");
  var s = surface(), form = $("form"), first = field.value;
  caretIn(s.value.length); typeIn("\n\nhai");
  assert(field.value === first + "\n<p>hai</p>" && body.unsaved, "the first change of a long post since it was saved: at once (unsaved), got " + JSON.stringify(field.value.slice(-40)));
  typeIn(" *ba*");
  assert(field.value === first + "\n<p>hai</p>", "then the field waits for the typing to stop, got " + JSON.stringify(field.value.slice(-40)));
  var data = new FormData(form);
  assert(data.get("body") === first + "\n<p>hai <em>ba</em></p>" && field.value === data.get("body"), "formdata reads the text as it is now, got " + JSON.stringify(String(data.get("body")).slice(-40)));
  typeIn(" bốn");
  await settled();
  assert(field.value === first + "\n<p>hai <em>ba</em> bốn</p>" && $("words").textContent === String(words + 3) && !$("unsaved").hidden, "300 ms after: the field, the counts, unsaved, got " + JSON.stringify(field.value.slice(-40)) + " " + $("words").textContent);
  s.setSelectionRange(0, s.value.length); document.execCommand("delete");
  assert(field.value === "" && body.empty, "empty at once (required reads it), got " + JSON.stringify(field.value));
  form.requestSubmit();
  assert(s.getAttribute("aria-invalid") === "true" && document.activeElement === s, "required: the surface invalid and focused, got " + active());
  typeIn("a");
  assert(field.value === "<p>a</p>" && !s.hasAttribute("aria-invalid"), "not empty at once, valid again, got " + JSON.stringify(field.value));
  await to("html");
  s.setSelectionRange(0, s.value.length); typeIn("<h3>Mới</h3><p>" + long + "</p>");
  assert(field.value === "<p>a</p>", "a long text waits, got " + JSON.stringify(field.value.slice(0, 40)));
  s.dispatchEvent(new FocusEvent("focusout"));
  assert(field.value === "<h3>Mới</h3>\n<p>" + long + "</p>", "the surface losing the focus: at once, got " + JSON.stringify(field.value.slice(0, 40)));
  s.setSelectionRange(0, s.value.length); typeIn("một\nhai\n\nba & <");
  await settled();
  assert(field.value === "<p>một<br>hai</p>\n<p>ba &amp; &lt;</p>", "HTML without a tag: as richtext reads such a source, got " + JSON.stringify(field.value));
  await to("rich");
  assert(field.value === "<p>một<br>hai</p>\n<p>ba &amp; &lt;</p>" && area.innerHTML === "<p>một<br>hai</p><p>ba &amp; &lt;</p>", "rich: the area holds it, got " + area.innerHTML);
`))
}

// What is typed in Markdown or HTML is read as it is typed while the text is short (under 2 000
// characters; the review of 21/10/2026 found that the 300 ms wait let a page's own script send a post
// older than what showed, and a tab closed at once lose its first words without a question): the field,
// the counts, unsaved and the question on leaving at once; a text that reads as no post (an image with
// no words) leaves required empty at once, so the form does not go; what the reading left out is said
// at once, with no switch; a long text that turns to white space is empty at once too (by its letters).
// What is read is read as richtext.Clean reads it: HTML typed, or written in Markdown, with
// <b style="font-weight:normal"> is not bold (Clean's one reading of style; a paste reads more), a span
// is no mark; a tag named in Markdown's prose (the <textarea> tag) is words and takes nothing after it;
// a script closed in its paragraph is left out and said.
// CONTROL (run 21/10/2026, copies of editor-format.js through -overlay, fix/controls.py in the
// scratchpad's editor-formats): with dropped written by no reading (flush() without it), "what the
// reading left out, said at once" failed ([]); with the text read without Clean's style rule
// (editor.read(doc.body)), "Markdown's HTML reads as Clean" failed (<strong>a</strong> <strong>b</strong>);
// with a short text waiting as a long one (no SHORT in changed()), "a text that reads as no post:
// required reads it at once" failed (<p>một hai</p> still); with the empty edge waiting too, "a long text
// turning to white space: empty at once" failed.
func TestBrowserEditorFormatReadsAsTyped(t *testing.T) {
	styled := `<p><b style="font-weight:normal">a</b> <b style="font-weight:400">b</b> <span style="font-weight:bold">c</span> <i style="font-style:normal">d</i> <strong style="font-weight:700">e</strong></p>`
	cleaned := richtext.Clean(styled).HTML
	if cleaned != "<p>a b c <em>d</em> <strong>e</strong></p>" {
		t.Fatalf("Clean of the styled HTML: %q", cleaned)
	}
	prose := "Ô dùng thẻ <textarea> của HTML.\n\nĐoạn hai.\n\n## Phần ba"
	runEditorFormatProof(t, "editor format reads as typed", editorFormatPage("<p>một</p>", "rich", " required")+editorProof(`
  var styled = `+jsString(styled)+`, cleaned = `+jsString(cleaned)+`;
  await to("markdown");
  var s = surface(), form = $("form");
  caretIn(s.value.length); typeIn(" hai");
  assert(field.value === "<p>một hai</p>" && body.unsaved && body.words === 2, "a short text: the field, the counts, unsaved at once, got " + JSON.stringify(field.value) + " " + body.words + " " + body.unsaved);
  var leaving = new Event("beforeunload", { cancelable: true });
  window.dispatchEvent(leaving);
  assert(leaving.defaultPrevented, "the question on leaving at once");
  s.setSelectionRange(0, s.value.length); typeIn("![](x.png)");
  assert(field.value === "" && body.empty && !form.checkValidity() && new FormData(form).get("body") === "", "a text that reads as no post: required reads it at once, got " + JSON.stringify(field.value));
  assert(JSON.stringify(body.dropped) === '["img"]', "what the reading left out, said at once, got " + JSON.stringify(body.dropped));
  await wait(20);
  assert(!$("dropped").hidden && !$("unsaved").hidden, "and the page says it");
  s.setSelectionRange(0, s.value.length); typeIn(`+jsString(prose)+`);
  assert(field.value === "<p>Ô dùng thẻ &lt;textarea&gt; của HTML.</p>\n<p>Đoạn hai.</p>\n<h2>Phần ba</h2>" && body.dropped.length === 0, "a tag named in prose is words and takes nothing after it, got " + JSON.stringify(field.value));
  s.setSelectionRange(0, s.value.length); typeIn("a<script>x()<\/script>b");
  assert(field.value === "<p>ab</p>" && JSON.stringify(body.dropped) === '["script"]', "a script closed in its paragraph: left out and said, got " + JSON.stringify(field.value) + " " + JSON.stringify(body.dropped));
  s.setSelectionRange(0, s.value.length); typeIn(styled);
  assert(field.value === cleaned, "Markdown's HTML reads as Clean, got " + JSON.stringify(field.value));
  await to("html");
  s.setSelectionRange(0, s.value.length); typeIn(styled);
  assert(field.value === cleaned, "HTML typed reads as Clean, got " + JSON.stringify(field.value));
  s.setSelectionRange(0, s.value.length); typeIn(new Array(2501).join(" "));
  assert(field.value === "" && body.empty, "a long text turning to white space: empty at once, got " + JSON.stringify(field.value));
`))
}

// Two quotes or two lists of a kind side by side, typed in Markdown or HTML, are one, as the area holds
// them (the editor's normalise joins them after every input in rich): what is posted from the text is
// what is posted after a switch to rich with nothing typed (the review of 21/10/2026: the switch joined
// them, the text did not, and the same words posted two values).
// CONTROL (run 21/10/2026, a copy of editor-format.js through -overlay, fix/controls.py in the
// scratchpad's editor-formats): with flush() joining nothing, "posted from Markdown as the area holds it"
// failed (two quotes and two lists posted).
func TestBrowserEditorFormatJoinsAsTheAreaHolds(t *testing.T) {
	runEditorFormatProof(t, "editor format joins as the area holds", editorFormatPage("<p>một</p>", "rich", "")+editorProof(`
  await to("markdown");
  var s = surface(), joined = "<blockquote>\n<p>a</p>\n<p>b</p>\n</blockquote>\n<ul>\n<li>c</li>\n<li>d</li>\n</ul>";
  s.setSelectionRange(0, s.value.length); typeIn("> a\n\n> b\n\n- c\n\n* d");
  assert(field.value === joined && new FormData($("form")).get("body") === joined, "posted from Markdown as the area holds it, got " + JSON.stringify(field.value));
  await to("rich");
  assert(field.value === joined && area.querySelectorAll("blockquote,ul").length === 2, "the same after a switch to rich, got " + JSON.stringify(field.value));
  await to("markdown");
  assert(s.value === "> a\n>\n> b\n\n- c\n- d", "written back as one quote and one list, got " + JSON.stringify(s.value));
  await to("html");
  s.setSelectionRange(0, s.value.length); typeIn("<ol><li>x</li></ol><ol><li>y</li></ol>");
  assert(field.value === "<ol>\n<li>x</li>\n<li>y</li>\n</ol>", "and from HTML, got " + JSON.stringify(field.value));
`))
}

// The marks of the toolbar in Markdown go around words (the review of 21/10/2026): a word selected with
// the space after it (a double click on Windows) or before it is marked without the space, around
// which Markdown would close nothing; over two paragraphs, list items or a heading each line is marked on
// its own, after its start (- , ## ), the rule, the fences and the code between them left as they are;
// again, where every line has the mark, it comes off. Each posts the mark, as rich does, never a
// literal ** or ~~.
// CONTROL (run 21/10/2026, a copy of editor-format.js through -overlay, fix/controls.py in the
// scratchpad's editor-formats): with wrap() putting the delimiters around the selection as it is (the
// code before the review), "a word with the space after it" failed (Xin **chào **thế giới, posted so);
// with a selection of no words marked as it is, "a selection of no words (a rule)" failed (**---**).
func TestBrowserEditorFormatMarksTheWords(t *testing.T) {
	const fence = "```"
	runEditorFormatProof(t, "editor format marks the words", editorFormatPage("<p>Xin chào thế giới</p>", "rich", "")+editorProof(`
  await to("markdown");
  var s = surface(), fence = "`+fence+`";
  function write(text) { s.focus(); s.setSelectionRange(0, s.value.length); document.execCommand("insertText", false, text); }
  function all() { s.focus(); s.setSelectionRange(0, s.value.length); }
  var at = s.value.indexOf("chào");
  s.setSelectionRange(at, at + 5); press($("bold"));
  assert(s.value === "Xin **chào** thế giới" && chosen() === "chào" && field.value === "<p>Xin <strong>chào</strong> thế giới</p>", "a word with the space after it: bold without the space, got " + JSON.stringify(s.value) + " " + JSON.stringify(field.value));
  press($("bold"));
  assert(s.value === "Xin chào thế giới", "and off again, got " + JSON.stringify(s.value));
  at = s.value.indexOf(" thế");
  s.setSelectionRange(at, at + 4); press($("italic"));
  assert(s.value === "Xin chào *thế* giới" && field.value === "<p>Xin chào <em>thế</em> giới</p>", "a word with the space before it, got " + JSON.stringify(s.value));
  write("Đoạn một.\n\nĐoạn hai."); all(); press($("bold"));
  assert(s.value === "**Đoạn một.**\n\n**Đoạn hai.**" && field.value === "<p><strong>Đoạn một.</strong></p>\n<p><strong>Đoạn hai.</strong></p>", "two paragraphs: each its own, got " + JSON.stringify(s.value));
  press($("bold"));
  assert(s.value === "Đoạn một.\n\nĐoạn hai.", "and off again where every line has it, got " + JSON.stringify(s.value));
  write("- một\n- hai"); all(); press($("strike"));
  assert(s.value === "- ~~một~~\n- ~~hai~~" && field.value === "<ul>\n<li><s>một</s></li>\n<li><s>hai</s></li>\n</ul>", "list items: after the marker, got " + JSON.stringify(s.value));
  write("## Tựa\n\na\n\n---\n\n" + fence + "\nx\n" + fence); all(); press($("italic"));
  assert(s.value === "## *Tựa*\n\n*a*\n\n---\n\n" + fence + "\nx\n" + fence, "a heading after its #, the rule, the fences and the code left, got " + JSON.stringify(s.value));
  assert(field.value === "<h2><em>Tựa</em></h2>\n<p><em>a</em></p>\n<hr>\n<pre><code>x</code></pre>", "posted, got " + JSON.stringify(field.value));
  write("- một"); all(); press($("bold"));
  assert(s.value === "- **một**", "one line from its start: after its marker, got " + JSON.stringify(s.value));
  write("a\n\n---\n\nb"); at = s.value.indexOf("---"); s.setSelectionRange(at, at + 3); press($("bold"));
  assert(s.value === "a\n\n---\n\nb" && field.value === "<p>a</p>\n<hr>\n<p>b</p>", "a selection of no words (a rule): nothing marked, got " + JSON.stringify(s.value));
`))
}

// Back to rich with the caret at the very start of the text (Control+Home, the review of 21/10/2026):
// the caret at the start of the first words, the switch one step of the history (Undo on, and it gives
// back the post before the switch), no error.
// CONTROL (run 21/10/2026, a copy of editor-format.js through -overlay, fix/controls.py in the
// scratchpad's editor-formats): with caretInArea() taking the last text for a place at the start (the
// code before the review), "no error" failed (IndexSizeError: the offset 4294967288 is larger than the
// node's length).
func TestBrowserEditorFormatCaretAtTheStart(t *testing.T) {
	runEditorFormatProof(t, "editor format caret at the start", editorFormatPage("<p>Xin <strong>chào</strong> thế giới</p>", "rich", "")+editorProof(`
  await to("markdown");
  var s = surface();
  caretIn(s.value.length); typeIn(" nhé");
  caretIn(0);
  await to("rich");
  await wait(40);
  assert(errors.length === 0, "no error, got " + errors.join(" | "));
  var at = getSelection();
  assert(document.activeElement === area && at.anchorNode && at.anchorNode.data === "Xin " && at.anchorOffset === 0, "the caret at the start of the first words, got " + (at.anchorNode && at.anchorNode.textContent) + " " + at.anchorOffset);
  assert(field.value === "<p>Xin <strong>chào</strong> thế giới nhé</p>" && !off("undo"), "the switch is a step of the history, undo on, got " + JSON.stringify(field.value) + " undo off " + off("undo"));
  $("undo").click();
  assert(field.value === "<p>Xin <strong>chào</strong> thế giới</p>", "undo gives back the post before the switch, got " + JSON.stringify(field.value));
`))
}

// A composition in the surface (an input method writing a word: Vietnamese, CJK, a phone's keyboard;
// the review of 21/10/2026) is left alone, as the area's is: a command asked during it (a click on the
// toolbar, Control+B) is done at its end at the caret it ends with, never in the middle of the word; a
// switch asked during one, in the surface or in the area, waits for its end; the text is read at its end.
// CONTROL (run 21/10/2026, copies of editor-format.js through -overlay, fix/controls.py in the
// scratchpad's editor-formats): with a command run at once in the surface (no wait for view.composing),
// "a command during a composition waits for its end" failed (một vie****); with ask() switching at once
// (no later()), "a switch during a composition in the surface waits for its end" failed; with the input
// read during a composition, "the text is read at its end" failed (<p>một vie</p>).
func TestBrowserEditorFormatWaitsForAComposition(t *testing.T) {
	runEditorFormatProof(t, "editor format waits for a composition", editorFormatPage("<p>một</p>", "rich", "")+editorProof(`
  await to("markdown");
  var s = surface();
  function compose(target, type, data) { target.dispatchEvent(new CompositionEvent(type, { bubbles: true, data: data || "" })); }
  caretIn(s.value.length);
  compose(s, "compositionstart");
  typeIn(" vie");
  press($("bold"));
  assert(s.value === "một vie", "a command during a composition waits for its end, got " + JSON.stringify(s.value));
  assert(field.value === "<p>một</p>", "the text is read at its end, got " + JSON.stringify(field.value));
  s.setSelectionRange(3, 7); typeIn(" việt");
  compose(s, "compositionend", " việt");
  assert(s.value === "một việt****" && s.selectionStart === 10 && s.selectionEnd === 10, "done at its end, at the caret it ends with, got " + JSON.stringify(s.value) + " at " + s.selectionStart);
  assert(field.value === "<p>một việt****</p>", "read at its end, got " + JSON.stringify(field.value));
  s.setSelectionRange(4, 8);
  compose(s, "compositionstart");
  assert(key(s, "b", { ctrlKey: true, isComposing: true }).defaultPrevented && s.value === "một việt****", "Control+B during a composition waits too, got " + JSON.stringify(s.value));
  compose(s, "compositionend", "");
  assert(s.value === "một **việt******", "and is done at its end, got " + JSON.stringify(s.value));
  compose(s, "compositionstart");
  press($("f-html"));
  await wait(200);
  assert(!s.hidden && s.spellcheck && s.value === "một **việt******", "a switch during a composition in the surface waits for its end");
  compose(s, "compositionend", "");
  assert(await until(function () { return showing("html"); }), "then it switches");
  await to("rich");
  compose(area, "compositionstart");
  press($("f-markdown"));
  await wait(200);
  assert(!area.hidden && s.hidden, "a switch during a composition in the area waits for its end");
  compose(area, "compositionend", "");
  assert(await until(function () { return showing("markdown"); }) && errors.length === 0, "then it switches, no error: " + errors.join(" | "));
`))
}

// The button of the toolbar's dropdown (smaller headings) in HTML (the review of 21/10/2026): every
// button its popover holds is off there, so it is off too and opens nothing; in Markdown and in rich it
// is on and opens.
// CONTROL (run 21/10/2026, a copy of editor-format.js through -overlay, fix/controls.py in the
// scratchpad's editor-formats): with no dropdown's button found (triggers empty), "in HTML the
// dropdown's button is off" failed; with its click not stopped, "it opens nothing" failed.
func TestBrowserEditorFormatDropdownOffInHTML(t *testing.T) {
	runEditorFormatProof(t, "editor format dropdown off in HTML", editorFormatPage("<p>một</p>", "rich", "")+editorProof(`
  await to("html");
  assert(off("more-button") && off("h4") && off("h6"), "in HTML the dropdown's button is off, as all it holds");
  openMore();
  await wait(40);
  assert(!dropdownOpen(), "it opens nothing");
  await to("markdown");
  assert(!off("more-button") && !off("h4"), "in Markdown it is on");
  openMore();
  await wait(40);
  assert(dropdownOpen(), "and opens");
  openMore();
  await to("rich");
  assert(!off("more-button"), "in rich it is on");
`))
}

// A part that cannot load once (offline a moment, a deploy; the review of 21/10/2026) is asked for
// again at the next switch: the formats' part (v2.0.0.js, viewing) and the Markdown part
// (editor-format.js, markdownFile), each refused the first time it is asked for, then shown.
// CONTROL (run 21/10/2026, copies through -overlay, fix/controls.py in the scratchpad's editor-formats):
// with markdownFile() keeping the promise of a load that failed (editor-format.js), and with viewing()
// keeping the one of the formats' part (v2.0.0.js, no viewFile = null), "the editor shows markdown"
// failed at the second switch (rich again, no new request).
func TestBrowserEditorRetriesItsFormatParts(t *testing.T) {
	failOnce := func(name string) func(string) bool {
		var mu sync.Mutex
		failed := false
		return func(path string) bool {
			mu.Lock()
			defer mu.Unlock()
			if failed || !strings.HasPrefix(path, AssetPrefix+"parts/"+name+".") {
				return false
			}
			failed = true
			return true
		}
	}
	steps := func(more string) string {
		return strings.Replace(editorProof(more), "<script>", "<script>"+editorFormatScript, 1)
	}
	runEditorPartServed(t, "editor format part fails once", failOnce("editor-format"), editorFormatPage("<p>một</p>", "rich", "")+steps(`
  press($("f-markdown"));
  assert(await until(function () { return partFetched("editor-format") === 1 && body.format === "rich" && pressed("f-rich") === "true"; }), "refused the first time: rich again, got " + body.format);
  assert(!surface() && !area.hidden, "the area stays");
  await to("markdown");
  assert(surface().value === "một" && partFetched("editor-format") === 2, "asked again at the next switch, it shows Markdown, got " + partFetched("editor-format"));
`))
	runEditorPartServed(t, "editor markdown part fails once", failOnce("editor-markdown"), editorFormatPage("<p>một</p>", "rich", "")+steps(`
  press($("f-markdown"));
  assert(await until(function () { return partFetched("editor-markdown") === 1 && body.format === "rich" && pressed("f-rich") === "true"; }), "refused the first time: rich again, got " + body.format);
  assert(!area.hidden && (!surface() || surface().hidden), "the area stays");
  await to("markdown");
  assert(surface().value === "một" && partFetched("editor-markdown") === 2, "asked again at the next switch, it shows Markdown, got " + partFetched("editor-markdown"));
  await to("html");
`))
}

// dropped: what the last switch from HTML or Markdown, or a paste, left out, in the order met, each once
// (a switch from rich: nothing); the page reads it outside the host ($body.dropped).
// CONTROL (run 21/10/2026, copies through -overlay): with the formats' part handing leftOut the body
// only (editor-format.js), "the head's script first" failed (no script: the parser puts a leading script
// in the head); with paste() no longer writing dropped (editor-paste.js), "a paste names what it left
// out" failed ([]).
func TestBrowserEditorFormatDropped(t *testing.T) {
	runEditorFormatProof(t, "editor format dropped", editorFormatPage("<p>start</p>", "rich", "")+editorProof(`
  await to("html");
  var s = surface();
  s.setSelectionRange(0, s.value.length);
  typeIn('<script>x()<\/script><p>a <span>b</span> <b>c</b></p><img src="x.png"><iframe></iframe><picture><img src="y.png"></picture><table><tr><td>d</td></tr></table><svg><circle r="1"/></svg><img src="z.png">');
  await to("rich");
  assert(JSON.stringify(body.dropped) === '["script","img","iframe","picture","table","svg"]', "the head's script first, then in the order met, each once, got " + JSON.stringify(body.dropped));
  assert(!$("dropped").hidden && $("dropped-words").textContent === "script, img, iframe, picture, table, svg", "the page says it, got " + $("dropped").textContent);
  assert(field.value === "<p>a b <strong>c</strong></p>\n<p>d</p>", "the value: the grammar's, got " + JSON.stringify(field.value));
  await to("markdown");
  assert(body.dropped.length === 0 && $("dropped").hidden, "a switch from rich leaves nothing out");
  caretIn(s.value.length); typeIn("\n\n![ảnh](x.png) <video src=v></video> <u>gạch</u>\n\n| a |\n|---|\n| 1 |");
  await to("html");
  assert(JSON.stringify(body.dropped) === '["img","video","table"]', "Markdown's image, raw HTML, table, got " + JSON.stringify(body.dropped));
  assert(s.value === "<p>a b <strong>c</strong></p>\n<p>d</p>\n<p>ảnh <u>gạch</u></p>\n<p>a</p>\n<p>1</p>", "their words, got " + JSON.stringify(s.value));
  await to("rich");
  assert(body.dropped.length === 0, "HTML that left nothing out, got " + JSON.stringify(body.dropped));
  caretAt("gạch", true);
  pasted({ "text/html": "<p>x<img src=y><b>z</b><form><input></form></p>", "text/plain": "xz" });
  assert(JSON.stringify(body.dropped) === '["img","form"]', "a paste names what it left out, got " + JSON.stringify(body.dropped));
  pasted({ "text/plain": " w" });
  assert(body.dropped.length === 0 && $("dropped").hidden, "a paste that left nothing out");
`))
}

// The history across a switch: in a text surface undo and redo are the surface's own; back to rich, the
// switch is one step of the editor's history (undone, the post before it), the steps before it kept.
// CONTROL (run 21/10/2026, a copy of editor-format.js through -overlay): with apply() going back to rich
// without the editor's after() (no step recorded), "undo takes the switch back in one step" failed.
func TestBrowserEditorFormatHistory(t *testing.T) {
	runEditorFormatProof(t, "editor format history", editorFormatPage("<p>một</p>", "rich", "")+editorProof(`
  caretAt("một", true); type(" hai");
  assert(field.value === "<p>một hai</p>", "typed in rich, got " + field.value);
  await to("markdown");
  var s = surface();
  caretIn(s.value.length); typeIn(" ba");
  within("ba"); press($("bold"));
  assert(s.value === "một hai **ba**", "bold in the surface, got " + JSON.stringify(s.value));
  $("undo").click();
  assert(s.value === "một hai ba", "undo in the surface: the bold, a step of its own, got " + JSON.stringify(s.value));
  $("redo").click();
  assert(s.value === "một hai **ba**", "redo in the surface, got " + JSON.stringify(s.value));
  await to("rich");
  assert(field.value === "<p>một hai <strong>ba</strong></p>" && !off("undo"), "rich again");
  $("undo").click();
  assert(field.value === "<p>một hai</p>" && !off("redo"), "undo takes the switch back in one step, got " + field.value);
  $("redo").click();
  assert(field.value === "<p>một hai <strong>ba</strong></p>", "redo brings it, got " + field.value);
  $("undo").click(); $("undo").click();
  assert(field.value === "<p>một</p>", "and the steps before it, got " + field.value);
`))
}

// An editor that starts in Markdown (<editor format="markdown">: format: 'markdown' in its scope) shows
// it once its parts are here, taking no focus; a switch pressed then moves the focus to the surface
// wherever it stood (another input of the page here: the two-end proof found a click on Markdown with the
// focus on the page's body left it there, and the words typed next went nowhere); a readonly editor
// keeps the format it starts in.
// CONTROL (run 21/10/2026, copies through -overlay, controls.py in the scratchpad's editor-formats): with
// init() not asking for the start (v2.0.0.js), "it starts in Markdown" failed (format markdown, nothing
// shown); with apply() switching a readonly editor (editor-format.js, no start kept), "a readonly editor
// keeps the format it starts in" failed (rich); with the focus moved only when it stood in the editor
// (the rule before the two-end proof), "a switch pressed moves the focus wherever it stood" failed.
// Since the integration of 21/10/2026 the editor's setter of format writes nothing in an editor that
// takes no writing (v2.0.0.js), before the part is asked: the copies c28 to c30 in the scratchpad's
// editor-formats/integrate show the setter without that (c28) failing "a press does nothing at once"
// (markdown, its switch pressed by its binding), apply() without its check alone (c29, the control
// above) now green, and both gone (c30) "a readonly editor keeps the format it starts in" (rich).
func TestBrowserEditorFormatStart(t *testing.T) {
	runEditorFormatProof(t, "editor format start", `<input id="other">`+editorFormatPage("<p>Xin <strong>chào</strong></p>", "markdown", "")+editorProof(`
  assert(await until(function () { return showing("markdown"); }) && surface().value === "Xin **chào**", "it starts in Markdown, got " + body.format);
  assert(!$("host").contains(document.activeElement), "the start takes no focus, got " + active());
  assert(pressed("f-markdown") === "true" && pressed("f-rich") === "false" && pressed("bold") === "false" && off("left"), "the toolbar as Markdown's");
  focusIn($("other"));
  await to("html");
  assert(document.activeElement === surface() && surface().value === "<p>Xin <strong>chào</strong></p>", "a switch pressed moves the focus wherever it stood, got " + active());
`))
	runEditorFormatProof(t, "editor format start, readonly", editorFormatPage("<p>Xin <strong>chào</strong></p>", "markdown", " readonly")+editorProof(`
  assert(await until(function () { return showing("markdown"); }) && surface().readOnly, "it starts in Markdown, read only");
  press($("f-rich"));
  await wait(200);
  assert(showing("markdown") && body.format === "markdown" && off("f-rich") && off("bold"), "a readonly editor keeps the format it starts in, got " + body.format);
`))
	// readonly from rich: a switch pressed is nothing, at once (the format and its switch's aria-pressed,
	// a binding of it, never say another) and for good (no part of the formats loaded for it)
	runEditorFormatProof(t, "editor format start, readonly in rich", editorFormatPage("<p>Xin <strong>chào</strong></p>", "rich", " readonly")+editorProof(`
  press($("f-markdown"));
  assert(body.format === "rich" && pressed("f-rich") === "true" && pressed("f-markdown") === "false" && off("f-markdown"), "a press does nothing at once, got " + body.format + ", markdown pressed " + pressed("f-markdown"));
  await wait(200);
  assert(showing("rich") && !surface() && !partFetched("editor-format") && !partFetched("editor-markdown"), "nor later: no part loaded for it");
`))
}

// The formats' part is loaded at the first switch away from rich and never before; HTML needs no
// Markdown part; when the part cannot load, the format reads rich again and the area stays.
// CONTROL (run 21/10/2026, a copy of v2.0.0.js through -overlay): with viewing() not taking the format
// back when the part does not load, "the format reads rich again" failed (again on the code after the
// integration, c31 in the scratchpad's editor-formats/integrate: got markdown). With it taken back in its
// slot but not painted (c24 in the scratchpad's editor-formats/integrate: no kit.render after it; a slot
// of a symbol is written without a paint), "the area stays" failed: Rich's aria-pressed, a binding of
// format since the integration, still false.
func TestBrowserEditorLoadsItsFormatParts(t *testing.T) {
	steps := func(more string) string {
		return strings.Replace(editorProof(more), "<script>", "<script>"+editorV2Script+editorFormatScript, 1)
	}
	runEditorPartProof(t, "editor format parts", false, editorFormatPage("<p>một</p>", "rich", "")+steps(`
  caretAt("một", true); press($("bold")); type(" hai");
  assert(!partFetched("editor-format") && !partFetched("editor-markdown") && !window[Symbol.for("kitwork:editor-format")], "nothing of the formats before a switch");
  await to("html");
  assert(partFetched("editor-format") === 1 && partFetched("editor-paste") === 1 && !partFetched("editor-markdown"), "HTML: the formats' part and the paste part, no Markdown");
  await to("markdown");
  // (bold that starts with a space: Markdown cannot open it there, so it is written as its tag)
  assert(partFetched("editor-markdown") === 1 && surface().value === "một<strong> hai</strong>", "Markdown: its part, got " + JSON.stringify(surface().value));
`))
	runEditorPartProof(t, "editor format parts refused", true, editorFormatPage("<p>một</p>", "rich", "")+steps(`
  press($("f-markdown"));
  assert(await until(function () { return body.format === "rich" && partFetched("editor-format") === 1; }), "the format reads rich again, got " + body.format);
  assert(!surface() && !area.hidden && pressed("f-rich") === "true", "the area stays");
`))
}
