package work

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/kitwork/engine/utilities/richtext"
)

// The editor's three formats by a person's hands (21/10/2026, anh Quốc: "tạo cho tôi một số nút giống
// segment hoặc dropdown để có thể tuỳ người dùng viết bằng rich / code hoặc markdown", then "vâng thử
// xem"): the canonical segment first in the toolbar of v2, compiled by the engine (ui/editor.go), run
// by the editor and its parts (jit/js/components/editor/v2.0.0.js, parts/editor-format.js,
// parts/editor-markdown.js, parts/editor-paste.js), on a page the engine serves, by the keyboard and
// the mouse alone, the form posted by the browser three times, once from each format.

// editorFormatsLeftOut is what the page says the last switch or paste left out, read bare inside the
// editor's host, after its writing area.
const editorFormatsLeftOut = `<text id="left-out" show="dropped.length">Đã bỏ {{ dropped.join(', ') }}</text>`

// editorFormatsFiles is editorV2Files with the canonical segment first in the toolbar
// (editorFormatSegment, as docs/components/editor.md writes it) and editorFormatsLeftOut inside the
// editor, after its writing area.
var editorFormatsFiles = func() map[string]string {
	files := map[string]string{}
	for name, source := range editorV2Files {
		source = strings.Replace(source, `<box alt="Định dạng" flex="row" wrap="true" align="left" gap="3">`+"\n",
			`<box alt="Định dạng" flex="row" wrap="true" align="left" gap="3">`+"\n"+editorFormatSegment, 1)
		files[name] = strings.Replace(source, "border=\"line\" />\n    </editor>", "border=\"line\" />\n      "+editorFormatsLeftOut+"\n    </editor>", 1)
	}
	return files
}()

// editorFormatsLook is what the proof reads once the format want is drawn: what shows (the area for
// rich; the text surface, spellchecked for Markdown and not for HTML) and the switch pressed, both
// want's (or what they are after five seconds), every part the editor asked for loaded, two frames
// drawn. It reads the surface's words, the area's HTML, the field, the focus (the surface, the area, a
// switch by its format, a button by its format or command), the words the page says were left out
// (the element bound to dropped.length, "" while hidden), the alignments disabled and the formats pressed in the toolbar, and what ran.
const editorFormatsLook = `(function (want) {
  return new Promise(function (done) {
    var tries = 0;
    (function look() {
      var text = document.querySelector(".editor-text"), area = document.querySelector(".editor-area");
      var on = document.querySelectorAll('[data-kitwork-command="format"][aria-pressed="true"]');
      var pressed = on.length === 1 ? on[0].getAttribute("data-kitwork-format-value") : on.length + " pressed";
      var shown = text && !text.hidden ? (text.spellcheck ? "markdown" : "html") : !area.hidden ? "rich" : "none";
      var parts = Array.prototype.every.call(document.querySelectorAll('script[src*="/_kitwork/parts/"]'), function (tag) {
        return window[Symbol.for("kitwork:" + /parts\/([a-z-]+)\./.exec(tag.getAttribute("src"))[1])];
      });
      if ((shown !== want || pressed !== want || !parts) && tries++ < 250) return setTimeout(look, 20);
      requestAnimationFrame(function () { requestAnimationFrame(function () {
        var left = document.querySelector('[data-kitwork-bind*="dropped.length"]');
        var names = function (selector) {
          return Array.prototype.map.call(document.querySelectorAll(selector), function (b) { return b.getAttribute("data-kitwork-format") || b.getAttribute("data-kitwork-command"); }).join(" ") || "none";
        };
        done(JSON.stringify({
          shown: shown + " shown, " + pressed + " pressed", text: text && !text.hidden ? text.value : "", area: area.innerHTML,
          field: document.querySelector(".editor-field").value, focus: window.__formats.focus(), leftOut: !left || left.hidden ? "" : left.textContent.trim(),
          bar: "pressed " + names('.editor-toolbar [data-kitwork-format][aria-pressed="true"]') + ", off " + names('.editor-toolbar [data-kitwork-command^="align"][aria-disabled="true"]'),
          ran: window.__ran === undefined ? "nothing" : String(window.__ran)
        }));
      }); });
    })();
  });
})`

// editorFormatsHands is put in a document once (it says "ready" when it already is): the name of the
// focus (focus), and a selection of the first words of the text surface that read so (select: a drag of
// the mouse would make it).
const editorFormatsHands = `(function () {
  if (window.__formats) return "ready";
  window.__formats = {
    focus: function () {
      var a = document.activeElement;
      if (!a || a === document.body) return "body";
      if (a.classList.contains("editor-text")) return "text";
      if (a.classList.contains("editor-area")) return "area";
      return a.getAttribute("data-kitwork-format-value") ? "format " + a.getAttribute("data-kitwork-format-value") : a.getAttribute("data-kitwork-format") || a.getAttribute("data-kitwork-command") || a.tagName.toLowerCase();
    },
    select: function (words) {
      var s = document.querySelector(".editor-text"), at = s.value.indexOf(words);
      if (at < 0) return "no " + JSON.stringify(words) + " in " + JSON.stringify(s.value);
      s.setSelectionRange(at, at + words.length);
      return "selected";
    }
  };
  return "installed";
})()`

// editorFormatsRead is one look (editorFormatsLook).
type editorFormatsRead struct{ Shown, Text, Area, Field, Focus, LeftOut, Bar, Ran string }

// editorFormatsAt reads the editor once the format want is drawn (editorFormatsLook, its hands put in
// the page if they are not there), held to the identity: in rich the area cleaned by Go is the field,
// and the field is its own Clean whatever the format.
func editorFormatsAt(t *testing.T, d *devtools, want, what string) editorFormatsRead {
	t.Helper()
	d.evaluate(editorFormatsHands)
	var got editorFormatsRead
	if raw := d.evaluate(editorFormatsLook + `(` + jsString(want) + `)`); json.Unmarshal([]byte(raw), &got) != nil {
		t.Fatalf("%s: the look read %s", what, raw)
	}
	if want == "rich" {
		if cleaned := richtext.Clean(got.Area).HTML; cleaned != got.Field {
			t.Errorf("%s: what the area holds, cleaned, is not what the form posts:\n area  %s\n clean %q\n field %q", what, got.Area, cleaned, got.Field)
		}
	}
	if again := richtext.Clean(got.Field).HTML; again != got.Field {
		t.Errorf("%s: the field is not its own Clean:\n field %q\n clean %q", what, got.Field, again)
	}
	return got
}

// editorFormatsSays is a look in one line: what shows and is pressed, the surface's words, the field, the
// focus, the words left out, the toolbar, what ran.
func editorFormatsSays(r editorFormatsRead) string {
	return strings.Join([]string{r.Shown, "text " + jsString(r.Text), "field " + jsString(r.Field), "focus " + r.Focus, "left out " + jsString(r.LeftOut), r.Bar, "ran " + r.Ran}, " | ")
}

// TestBrowserEditorFormatsByTheKeysAndTheMouse proves the three formats end to end, in headless Chrome
// or Edge, every step a key, a click or the keyboard's text, every look of rich held to the identity
// (the area cleaned by Go is the field, its own Clean):
//   - the page as the server writes it: the segment's switches (aria-pressed bound to format), Rich
//     pressed and the toolbar's one stop for Tab, Bold now -1; the scope holding format and dropped;
//     the words left out, hidden;
//   - the keys reach the segment: Tab from the page lands on Rich, the arrows go along the switches and
//     on to Bold and back, Enter on Markdown switches: the post as Markdown, the focus in it;
//   - Markdown typed (a heading, bold, a list with a link, a fenced code block): the field the grammar
//     of it 300 ms after the typing stops; the toolbar's Bold, clicked on a selection, wraps it in **,
//     no format pressed, the alignments disabled;
//   - Rich by the mouse: the area holds the grammar, focused, nothing left out;
//   - Alt+F10, Home and the arrows to HTML, Enter: the field's own HTML in the surface; an <iframe> and
//     a <p> typed; Rich by the mouse: the paragraph kept, the iframe left out and the page says so
//     ("Đã bỏ iframe", read bare inside the host), and the iframe was never fetched;
//   - Save from rich, from Markdown and from HTML, each the browser's own post with no question: what
//     was sent is the same post each time, and the <richtext> that shows it the same, richtext.Display's;
//   - Markdown typed and Save clicked at once, before its 300 ms (the field still waiting: a long
//     paragraph typed after the first change, which is read at once): what was typed is sent (the
//     surface losing the focus to Save reads it, the form's formdata too).
//
// CONTROL (run 21/10/2026, copies through -overlay, c17 to c22 in the scratchpad's
// editor-formats/integrate, e2e-controls.txt): parts/editor-format.js without its focusout and its
// formdata listeners (c17c) failed "Save from Markdown at once" (the words typed last not sent; either
// listener alone keeps it green, c17 and c17b: a click on Save takes the focus first); writing no
// dropped on a switch (c18) failed "Rich after the HTML" (nothing said left out); reading the HTML typed
// in the live page beside the DOMParser (c19) failed "the iframe typed in HTML was fetched"; the focus
// left where it was on a switch (c20) failed "Enter on Markdown" (focus on the switch); ui/editor.go
// leaving the switches out of the toolbar's buttons (c21) failed "the page as the server writes it" (no
// tabindex on them, Bold 0); v2.0.0.js's arrows skipping the switches (c22) failed "the arrows along
// the segment and on".
func TestBrowserEditorFormatsByTheKeysAndTheMouse(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the editor browser proof in short mode")
	}
	browser := findHeadlessBrowserForGate()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	tenant := buildUISite(t, editorFormatsFiles)
	first := serveUI(t, tenant, "http://localhost/")
	served := first.Body.String()
	if first.Code != http.StatusOK || warningsScriptRe.MatchString(served) {
		t.Fatalf("/ = %d, or it links warnings:\n%.3000s", first.Code, served)
	}
	switchOf := func(tab, format, label string, pressed bool) string {
		return `tabindex="` + tab + `" data-kitwork-command="format" data-kitwork-format-value="` + format + `" data-kitwork-bind="aria-pressed: format == &#39;` + format +
			`&#39;" data-kitwork-event="click: format = &#39;` + format + `&#39;" aria-pressed="` + map[bool]string{true: "true", false: "false"}[pressed] + `">` + label + `</button>`
	}
	for _, want := range []string{
		`<div class="flex-row gap-1 editor-group" aria-label="Viết bằng" role="group">`,
		switchOf("0", "rich", "Rich", true), switchOf("-1", "markdown", "Markdown", false), switchOf("-1", "html", "HTML", false),
		`tabindex="-1" data-kitwork-format="bold"`, `format: &#39;rich&#39;; dropped: []`,
		`id="left-out" data-kitwork-bind="show: dropped.length; text: [&#39;Đã bỏ &#39;, (dropped.join(&#39;, &#39;))].join(&#39;&#39;)" hidden>`,
	} {
		if !strings.Contains(served, want) {
			t.Errorf("the page as the server writes it has no %s", want)
		}
	}
	if n := strings.Count(served, `tabindex="0"`); n != 1 {
		t.Errorf("tabindex=\"0\" written %d times, want once (Rich, the toolbar's one stop)", n)
	}

	site := newEditorSite(tenant, "localhost")
	defer site.Close()
	check := func(what, got, want string) {
		t.Helper()
		if got != want {
			t.Errorf("%s:\n got  %s\n want %s", what, got, want)
		}
	}
	d := openDevtools(t, browser, site.URL+"/", 1000, 1400, "")
	d.loaded(exampleSettle)
	d.call("Page.enable", map[string]any{})
	d.call("Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": editorBrowserHeard + ";\n" + editorFormatsHands})
	networkReload(d)
	// the page has the focus, as a person's window has it
	d.call("Emulation.setFocusEmulationEnabled", map[string]any{"enabled": true})
	look := func(want, what string) editorFormatsRead {
		t.Helper()
		return editorFormatsAt(t, d, want, what)
	}
	says := editorFormatsSays
	wants := func(shown, text, field, focus, leftOut, bar string) string {
		return says(editorFormatsRead{Shown: shown, Text: text, Field: field, Focus: focus, LeftOut: leftOut, Bar: bar, Ran: "nothing"})
	}
	focus := func() string { return d.evaluate(`window.__formats.focus()`) }
	key := func(name, code string, windows, modifiers int) { editorReviewKey(d, name, code, windows, modifiers) }
	wait := func() {
		d.evaluate(`new Promise(function (done) { setTimeout(function () { done("waited"); }, 400); })`)
	}
	saved := richtext.Clean(editorV2Saved).HTML
	// no caret yet in rich: the alignments off; in a text surface, no format pressed, the alignments off
	const richBar = "pressed none, off align-left align-center align-right"
	const textBar = richBar
	check("as it starts", says(look("rich", "as it starts")), wants("rich shown, rich pressed", "", saved, "body", "", richBar))

	// the keys reach the segment: Tab, the arrows along it and on, Enter on Markdown
	key("Tab", "Tab", 9, 0)
	check("Tab from the page", focus(), "format rich")
	var walked []string
	for _, arrow := range []string{"ArrowRight", "ArrowRight", "ArrowRight", "ArrowLeft", "ArrowLeft"} {
		key(arrow, arrow, map[string]int{"ArrowLeft": 37, "ArrowRight": 39}[arrow], 0)
		walked = append(walked, focus())
	}
	check("the arrows along the segment and on", strings.Join(walked, ", "), "format markdown, format html, bold, format html, format markdown")
	editorPress(d, "Enter")
	check("Enter on Markdown", says(look("markdown", "Enter on Markdown")), wants("markdown shown, markdown pressed", "Họp nhóm lúc chín giờ mười giờ sáng nay", saved, "text", "", textBar))

	// Markdown typed: a heading, bold, a list with a link, a fenced code block
	editorPress(d, "Control+End")
	typed := "\n\n# Kế hoạch\n\n**Quan trọng**: đọc kỹ\n\n- một\n- hai [tài liệu](https://kitwork.io/docs)\n\n```\ngo test ./...\n```"
	editorType(d, typed)
	wait()
	markdown := "Họp nhóm lúc chín giờ mười giờ sáng nay" + typed
	post := saved + "\n<h2>Kế hoạch</h2>\n<p><strong>Quan trọng</strong>: đọc kỹ</p>\n<ul>\n<li>một</li>\n<li>hai <a href=\"https://kitwork.io/docs\">tài liệu</a></li>\n</ul>\n<pre><code>go test ./...</code></pre>"
	check("the Markdown typed", says(look("markdown", "the Markdown typed")), wants("markdown shown, markdown pressed", markdown, post, "text", "", textBar))

	// the toolbar's Bold on a selection: ** around it
	check("đọc kỹ selected", d.evaluate(`window.__formats.select("đọc kỹ")`), "selected")
	editorClick(d, `.editor-toolbar [data-kitwork-format="bold"]`)
	wait()
	markdown = strings.Replace(markdown, ": đọc kỹ", ": **đọc kỹ**", 1)
	post = strings.Replace(post, ": đọc kỹ</p>", ": <strong>đọc kỹ</strong></p>", 1)
	check("Bold in Markdown", says(look("markdown", "Bold in Markdown")), wants("markdown shown, markdown pressed", markdown, post, "text", "", textBar))

	// Rich by the mouse: the grammar in the area
	editorClick(d, `[data-kitwork-format-value="rich"]`)
	// (the caret near the same words: after "đọc kỹ", in its bold; in a paragraph: no alignment off)
	check("Rich again", says(look("rich", "Rich again")), wants("rich shown, rich pressed", "", post, "area", "", "pressed bold, off none"))

	// HTML by the keys: Alt+F10 to the toolbar, Home, the arrows, Enter; an iframe and a paragraph typed
	key("F10", "F10", 121, 1)
	key("Home", "Home", 36, 0)
	check("Alt+F10 and Home", focus(), "format rich")
	key("ArrowRight", "ArrowRight", 39, 0)
	key("ArrowRight", "ArrowRight", 39, 0)
	editorPress(d, "Enter")
	check("Enter on HTML", says(look("html", "Enter on HTML")), wants("html shown, html pressed", post, post, "text", "", textBar))
	editorPress(d, "Control+End")
	editorType(d, "\n<iframe src=\"/frame\"></iframe>\n<p>Thêm <b>một</b> đoạn.</p>")
	editorClick(d, `[data-kitwork-format-value="rich"]`)
	post += "\n<p>Thêm <strong>một</strong> đoạn.</p>"
	check("Rich after the HTML", says(look("rich", "Rich after the HTML")), wants("rich shown, rich pressed", "", post, "area", "Đã bỏ iframe", "pressed none, off none"))
	if site.fetched("/frame") {
		t.Error("the iframe typed in HTML was fetched: the HTML was read where it runs")
	}

	// Save from rich, from Markdown, from HTML: the same post sent, the same <richtext>
	shown := richtext.Display(post, "http://localhost")
	save := func(from string) {
		t.Helper()
		heard := site.heard()
		d.evaluate(`window.__before = true`)
		editorClickAway(d, `button[type="submit"]`)
		check("Save from "+from+": a question", editorDialog(d), "no question")
		site.waitFor(t, "the post from "+from, func() bool { return strings.Contains(site.pages(heard), "POST") })
		d.loaded(`(window.__before ? "wait of 0" : ` + exampleSettle + `)`)
		check("Save from "+from+": what the browser sent", site.pages(heard), "POST / 200 site=same-origin application/x-www-form-urlencoded")
		check("Save from "+from+": what was sent", d.evaluate(`document.querySelector("pre code.language-html").textContent`), post)
		check("Save from "+from+": the post shown by <richtext>", d.evaluate(`document.querySelector("#shown .richtext").innerHTML`), shown)
	}
	save("rich")
	editorClick(d, `[data-kitwork-format-value="markdown"]`)
	// the post written as Markdown: the # typed is ## (an h2: h1 is the page's title), the rest as typed
	markdown = strings.Replace(markdown, "\n# Kế hoạch", "\n## Kế hoạch", 1) + "\n\nThêm **một** đoạn."
	check("Markdown on the answer", says(look("markdown", "Markdown on the answer")), wants("markdown shown, markdown pressed", markdown, post, "text", "", textBar))
	save("Markdown")
	editorClick(d, `[data-kitwork-format-value="html"]`)
	check("HTML on the answer", says(look("html", "HTML on the answer")), wants("html shown, html pressed", post, post, "text", "", textBar))
	save("HTML")

	// Markdown typed and saved at once, before its 300 ms: the surface losing the focus to Save, or the
	// form's formdata, reads the text as it is. (Since the review of 21/10/2026 the first change since
	// the post was saved, and a text under 2 000 characters, are read at once: a long paragraph typed
	// after the first change waits.)
	editorClick(d, `[data-kitwork-format-value="markdown"]`)
	look("markdown", "Markdown on the last answer")
	editorPress(d, "Control+End")
	editorType(d, "\n\nXong *rồi*.")
	check("the first change since the post was saved", d.evaluate(`document.querySelector(".editor-field").value.indexOf("Xong") < 0 ? "waiting" : "written"`), "written")
	long := strings.TrimSpace(strings.Repeat("Một câu dài. ", 160))
	editorType(d, "\n\n"+long)
	check("the field before Save", d.evaluate(`document.querySelector(".editor-field").value.indexOf("câu dài") < 0 ? "waiting" : "written"`), "waiting")
	post += "\n<p>Xong <em>rồi</em>.</p>\n<p>" + long + "</p>"
	shown = richtext.Display(post, "http://localhost")
	save("Markdown at once")
	check("complaints", d.evaluate(`JSON.stringify(window.__complaints)`), "[]")
}

// Without JavaScript the field is the control (no JS, unchanged): a select of format written in the
// editor beside its toolbar, which the compiler takes as it takes one in the toolbar, is not drawn
// either, as the toolbar is not, so nothing shows that switches nothing (the review of 21/10/2026 found
// the select drawn alone above the field). Read in headless Chrome or Edge with its scripts off (blink's
// scriptEnabled=false): the select, the toolbar and the area not drawn, the field in the page's flow.
// CONTROL (run 21/10/2026, a copy of ui/stylesheet/editor.go through -overlay, fix/controls.py in the
// scratchpad's editor-formats): with the rule for scripting:none without .editor
// [data-kitwork-command], "without JavaScript" failed (the select display:block). The first copy of this
// proof read the blank document the browser lists first at times; it waits for the editor's page now.
func TestBrowserEditorFormatSelectWithoutJavaScript(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the editor browser proof in short mode")
	}
	browser := findHeadlessBrowserForGate()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	files := map[string]string{}
	for name, source := range editorV2Files {
		files[name] = strings.Replace(source, `<box alt="Định dạng" flex="row" wrap="true" align="left" gap="3">`,
			`<select value="{{ format }}" alt="Viết bằng"><option value="rich">Rich</option><option value="markdown">Markdown</option><option value="html">HTML</option></select>
      <box alt="Định dạng" flex="row" wrap="true" align="left" gap="3">`, 1)
	}
	tenant := buildUISite(t, files)
	first := serveUI(t, tenant, "http://localhost/")
	if served := first.Body.String(); first.Code != http.StatusOK || !strings.Contains(served, `<select aria-label="Viết bằng" data-kitwork-command="format"`) {
		t.Fatalf("/ = %d, or no select of format beside the toolbar:\n%.3000s", first.Code, served)
	}
	site := newEditorSite(tenant, "localhost")
	defer site.Close()
	d := openDevtools(t, browser, site.URL+"/", 1000, 1400, "scriptEnabled=false")
	// (the blank document the browser lists first is no page: "of 0" is tried again)
	d.loaded(`(document.querySelector(".editor-field") ? "the editor's page" : "a wait of 0")`)
	got := d.evaluate(`(function () {
  function shown(selector) { var el = document.querySelector(selector); return el ? getComputedStyle(el).display : "missing"; }
  return JSON.stringify({ select: shown("select"), toolbar: shown(".editor-toolbar"), area: shown(".editor-area"), field: getComputedStyle(document.querySelector(".editor-field")).position, scripts: typeof window.kitwork });
})()`)
	if want := `{"select":"none","toolbar":"none","area":"none","field":"static","scripts":"undefined"}`; got != want {
		t.Errorf("without JavaScript:\n got  %s\n want %s", got, want)
	}
}
