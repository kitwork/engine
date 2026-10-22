package work

import (
	"net/http"
	"strings"
	"testing"

	"github.com/kitwork/engine/utilities/richtext"
)

// The editor's formats end to end (21/10/2026, anh Quốc: "tạo cho tôi một số nút giống segment hoặc
// dropdown để có thể tuỳ người dùng viết bằng rich / code hoặc markdown"): the canonical segment first in
// the toolbar of v2, compiled by the engine (ui/editor.go), run by the editor and its parts
// (jit/js/components/editor/v2.0.0.js, parts/editor-format.js, parts/editor-markdown.js), driven by the
// mouse and the keyboard, the form posted by the browser.

// editorFormatSegment is the canonical segment, as docs/components/editor.md writes it.
const editorFormatSegment = `        <group alt="Viết bằng" flex="row" gap="1">
          <button click="format = 'rich'" active:background="brand/15">Rich</button>
          <button click="format = 'markdown'" active:background="brand/15">Markdown</button>
          <button click="format = 'html'" active:background="brand/15">HTML</button>
        </group>
`

// editorFormatFiles is editorV2Files with the segment first in the toolbar and, under the editor, what
// the last switch or paste left out (Đã bỏ …).
var editorFormatFiles = func() map[string]string {
	files := map[string]string{}
	for name, source := range editorV2Files {
		source = strings.Replace(source, `<box alt="Định dạng" flex="row" wrap="true" align="left" gap="3">`+"\n", `<box alt="Định dạng" flex="row" wrap="true" align="left" gap="3">`+"\n"+editorFormatSegment, 1)
		files[name] = strings.Replace(source, `<text show="$body.unsaved">Chưa lưu</text>`,
			`<text show="$body.unsaved">Chưa lưu</text>`+"\n  "+`<text id="dropped" show="$body.dropped.length">Đã bỏ {{ $body.dropped.join(', ') }}</text>`, 1)
	}
	return files
}()

// editorFormatState is what the page shows of the formats once the switch asked for is drawn: the
// format pressed, what shows (the text surface or the area), the surface's words, the field, the focus,
// what the page says was left out, and what ran.
const editorFormatState = `new Promise(function (done) {
  var tries = 0;
  (function look() {
    var text = document.querySelector(".editor-text"), area = document.querySelector(".editor-area"), on = document.querySelector('[data-kitwork-command="format"][aria-pressed="true"]');
    var shown = text && !text.hidden ? "text" : !area.hidden ? "area" : "none", format = on ? on.getAttribute("data-kitwork-format-value") : "none";
    if ((format === "rich") !== (shown === "area") && tries++ < 150) return setTimeout(look, 20);
    requestAnimationFrame(function () { requestAnimationFrame(function () {
      var a = document.activeElement, dropped = document.getElementById("dropped");
      done(JSON.stringify({ format: format + " in the " + shown, words: shown === "text" ? text.value : "", field: document.querySelector(".editor-field").value,
        focus: a === text ? "text" : a === area ? "area" : a.tagName.toLowerCase(), dropped: dropped.hidden ? "" : dropped.textContent, ran: String(window.__ran || "nothing") }));
    }); });
  })();
})`

// TestBrowserEditorWritesInMarkdownAndHTML proves the formats end to end on a page the engine serves, in
// headless Chrome or Edge:
//   - the page as the server writes it: the segment's buttons data-kitwork-command="format" with
//     data-kitwork-format-value, the one it starts in pressed, the scope holding format and dropped;
//   - Markdown by the mouse: the post as Markdown in the text surface, focused; words typed by the
//     keyboard's text (a heading, a list with bold and italic, a quote) and italic from the toolbar's I
//     on a selection, the field the cleaned HTML of what is typed;
//   - HTML by the mouse: the field's own HTML in the surface; a script and a centred paragraph typed;
//   - back to rich: the area holds the post (the script left out and said so, nothing ran);
//   - Save, the browser's own post: what was sent is the field, to the letter, and richtext() keeps it.
//
// It found what the proofs of jit/js had not: a click on Markdown with the focus on the page's body
// (nothing in the editor focused yet) left the focus there, and the words typed next went nowhere. The
// focus now follows every switch but a select's change and the start.
// CONTROL (run 21/10/2026, copies of jit/js/parts/editor-format.js through -overlay, controls.py in the
// scratchpad's editor-formats): with the focus moved only when it stood in the editor (the rule before
// this proof), "Markdown" failed (focus body); with the text read as HTML in Markdown too (no Markdown
// reading), "the Markdown typed" failed.
func TestBrowserEditorWritesInMarkdownAndHTML(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the editor browser proof in short mode")
	}
	browser := findHeadlessBrowserForGate()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	tenant := buildUISite(t, editorFormatFiles)
	first := serveUI(t, tenant, "http://localhost/")
	served := first.Body.String()
	if first.Code != http.StatusOK || warningsScriptRe.MatchString(served) {
		t.Fatalf("/ = %d, or it links warnings:\n%.3000s", first.Code, served)
	}
	for _, want := range []string{
		`aria-label="Viết bằng" role="group"`, `data-kitwork-command="format"`, `data-kitwork-format-value="rich"`,
		`data-kitwork-format-value="markdown"`, `data-kitwork-format-value="html"`, `format: &#39;rich&#39;`, `dropped: []`,
	} {
		if !strings.Contains(served, want) {
			t.Errorf("the page as the server writes it has no %s", want)
		}
	}

	site := newEditorSite(tenant, "localhost")
	defer site.Close()
	check := func(what, got, want string) {
		t.Helper()
		if got != want {
			t.Errorf("%s:\n got  %s\n want %s", what, got, want)
		}
	}
	state := func(format, words, field, focus, dropped string) string {
		return `{"format":` + jsString(format) + `,"words":` + jsString(words) + `,"field":` + jsString(field) + `,"focus":` + jsString(focus) + `,"dropped":` + jsString(dropped) + `,"ran":"nothing"}`
	}
	d := openDevtools(t, browser, site.URL+"/", 1000, 1400, "")
	d.loaded(exampleSettle)
	d.call("Page.enable", map[string]any{})
	d.call("Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": editorBrowserHeard})
	networkReload(d)
	d.evaluate(`window.__before = true`)
	saved := richtext.Clean(editorV2Saved).HTML
	check("as it starts", d.evaluate(editorFormatState), state("rich in the area", "", saved, "body", ""))

	// Markdown: by the mouse, then the keyboard's text, then italic from the toolbar on a selection
	editorClick(d, `[data-kitwork-format-value="markdown"]`)
	check("Markdown", d.evaluate(editorFormatState), state("markdown in the text", "Họp nhóm lúc chín giờ mười giờ sáng nay", saved, "text", ""))
	editorPress(d, "Control+End")
	editorType(d, "\n\n## Việc cần làm\n\n- **một** việc\n- hai *việc*\n\n> Ghi chú")
	d.evaluate(`new Promise(function (done) { setTimeout(function () { done("waited"); }, 400); })`)
	markdown := "Họp nhóm lúc chín giờ mười giờ sáng nay\n\n## Việc cần làm\n\n- **một** việc\n- hai *việc*\n\n> Ghi chú"
	post := saved + "\n<h2>Việc cần làm</h2>\n<ul>\n<li><strong>một</strong> việc</li>\n<li>hai <em>việc</em></li>\n</ul>\n<blockquote>\n<p>Ghi chú</p>\n</blockquote>"
	check("the Markdown typed", d.evaluate(editorFormatState), state("markdown in the text", markdown, post, "text", ""))
	d.evaluate(`(function () { var s = document.querySelector(".editor-text"), at = s.value.indexOf("Ghi chú"); s.setSelectionRange(at, at + 7); return "selected"; })()`)
	editorClick(d, `[data-kitwork-format="italic"]`)
	d.evaluate(`new Promise(function (done) { setTimeout(function () { done("waited"); }, 400); })`)
	markdown = strings.Replace(markdown, "> Ghi chú", "> *Ghi chú*", 1)
	post = strings.Replace(post, "<p>Ghi chú</p>", "<p><em>Ghi chú</em></p>", 1)
	check("italic from the toolbar", d.evaluate(editorFormatState), state("markdown in the text", markdown, post, "text", ""))

	// HTML: the field's own; a script and a centred paragraph typed
	editorClick(d, `[data-kitwork-format-value="html"]`)
	check("HTML", d.evaluate(editorFormatState), state("html in the text", post, post, "text", ""))
	editorPress(d, "Control+End")
	editorType(d, "\n<script>window.__ran = 'html script'</script><p class=\"align-center\">Hẹn gặp lại.</p>")

	// rich again: the post in the area, the script left out and said so
	editorClick(d, `[data-kitwork-format-value="rich"]`)
	post += "\n<p class=\"align-center\">Hẹn gặp lại.</p>"
	check("rich again", d.evaluate(editorFormatState), state("rich in the area", "", post, "area", "Đã bỏ script"))
	check("the area cleaned is the field", richtext.Clean(d.evaluate(`document.querySelector(".editor-area").innerHTML`)).HTML, post)

	// Save: the browser posts the form, the answer a new document
	from := site.heard()
	editorClickAway(d, `button[type="submit"]`)
	check("Save clicked", editorDialog(d), "no question")
	site.waitFor(t, "the post", func() bool { return strings.Contains(site.pages(from), "POST") })
	d.loaded(`(window.__before ? "wait of 0" : ` + exampleSettle + `)`)
	check("what the browser sent", site.pages(from), "POST / 200 site=same-origin application/x-www-form-urlencoded")
	sent := d.evaluate(`document.querySelector("pre code.language-html").textContent`)
	check("what was sent", sent, post)
	check("what richtext() keeps of it", richtext.Clean(sent).HTML, sent)
	check("complaints", d.evaluate(`JSON.stringify(window.__complaints)`), "[]")
}
