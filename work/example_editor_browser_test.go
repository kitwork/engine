package work

import (
	"strings"
	"testing"

	"github.com/kitwork/engine/utilities/richtext"
)

// exampleEditorHint is the hint under the editor of /components/editor after its count of words.
const exampleEditorHint = "up to 20 000 characters. A paste from a document keeps its bold, italic, underline, strikethrough, code, headings, lists, alignment and links."

// exampleEditorMarkdown is the post TestBrowserExampleEditor writes on the demo, as its Markdown
// shows it: the centred heading a line of HTML (Markdown has no alignment), bold over italic three
// stars, the underline <u>, the code block fenced, the links [words](address).
const exampleEditorMarkdown = "<h4 class=\"align-center\">Opening hours</h4>\n\n" +
	"Open ***every day*** from 7 am to 10 pm, <u>the kitchen until 9</u>. Select a few words and try the buttons.\n\n" +
	"- Espresso\n- Latte with oat milk\n\n" +
	"The board by the door is drawn with Kitwork UI:\n\n" +
	"```\n<text heading=\"2\" align=\"center\">\n  Opening hours\n</text>\n```\n\n" +
	"Read more in [the docs](/docs).\n\n" +
	"Closed on [public holidays](/docs)."

// TestBrowserExampleEditor proves the kitwork example's /components/editor in a real browser, as a
// visitor reaches and uses it, with the hands and eyes of TestBrowserEditorWritesAPost (every look held
// to the identity: the area cleaned by Go is the field, its own Clean):
//   - from /components, the #editor section's link is a Drive move (X-Kitwork-Hydrate), and the editor
//     Drive brought starts: the area editable, nothing pressed, the alignments disabled (no caret yet),
//     the count of the sample (its centred heading, underline and code block);
//   - "every day" (bold in the sample) put in italic too with the toolbar's Italic, a line typed after
//     the last one and its last word linked to /docs through the popover (Enter in the address): the
//     count follows and the unsaved words show;
//   - at a phone's width (390px), the popover open, the page has no sideways scroll;
//   - toolbar v2: the caret in the centred heading (H2 and centre pressed), the dropdown of smaller
//     headings opened from its trigger; at 390px open, it stands inside the screen; its Heading 4 makes
//     the heading an h4 through $body, its alignment kept, and the dropdown closes, the focus back in the
//     area; opened again, a click in the area closes it (click-outside on <dropdown>);
//   - the formats (21/10/2026; the switch to rich pressed in every look of the toolbar above,
//     format=rich): Markdown by the mouse shows the post as Markdown (exampleEditorMarkdown), the focus
//     in it, the alignments off; Rich again, the same post, drawn as before, nothing said left out;
//   - Save is the browser's own post, which asks nothing, and the answer shows exactly what the field
//     held: through <richtext> as richtext.Display writes it, drawn as the editor's area drew it
//     (editorV2Probe's look: the same runs of words, alike), and in the block of the HTML it kept; the
//     page's scripts complain of nothing, on either page.
//
// CONTROL, against a copy served through -overlay (the tree untouched, run 20/10/2026): the editor's
// field written only at the start (jit/js/components/editor/v2.0.0.js, sync() and the formdata listener
// leaving it) kept the sample while the visitor wrote ("written": the area cleaned is not the field),
// and the answer showed the sample ("the answer's editor"). Toolbar v2 (run 21/10/2026, controls.py in
// the scratchpad's editor-more/integrate): the editor without its click listener on window that closes
// the dropdown after a command failed at "H4 from the dropdown" (the dropdown open); with the demo as it
// was before toolbar v2 (its toolbar, sample and words; a copy of the examples tree, examplesRoot pointed
// at it through an overlaid example_sites_test.go) at "as it starts" and on, no dropdown on the page;
// with the demo's <dropdown> written without click-outside and keydown:escape at "a click outside the
// open dropdown" (open still). The formats (run 21/10/2026, c23 in the scratchpad's
// editor-formats/integrate, the same way): with the demo without its segment, "the dropdown opened" (no
// format=rich pressed) and on, and the click on Markdown found nothing ("Markdown on the demo").
func TestBrowserExampleEditor(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the example editor browser proof in short mode")
	}
	browser := findHeadlessBrowserForGate()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	tenant, _ := examplePage(t, "/components/editor")
	site := newEditorSite(tenant, exampleHost)
	defer site.Close()
	check := func(what, got, want string) {
		t.Helper()
		if got != want {
			t.Errorf("%s:\n got  %s\n want %s", what, got, want)
		}
	}
	d := openDevtools(t, browser, site.URL+"/components", 1280, 1000, "")
	d.loaded(exampleSettle)
	d.call("Page.enable", map[string]any{})
	d.call("Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": editorBrowserHeard})
	networkReload(d)
	from := site.heard()
	d.evaluate(`document.querySelector('#editor a[href="/components/editor"]').click()`)
	// the move is over once its fade has finished (data-kitwork-moving gone): until then the transition
	// stands over the page and takes the clicks (/components holds an editor of its own, in #editor)
	site.waitFor(t, "Drive's move to /components/editor", func() bool {
		return d.evaluate(`String(location.pathname === "/components/editor" && !!document.querySelector(".editor-area") && !document.documentElement.hasAttribute("data-kitwork-moving"))`) == "true"
	})
	check("the move to the editor", "loaded "+site.pages(from)+"; Drive "+site.drive(from), "loaded ; Drive GET /components/editor 200")
	d.evaluate(`window.__before = true`)
	sample := richtext.Clean(exampleEditorSample).HTML
	start := editorStep(t, d, "as it starts")
	check("as it starts", editorSays(start)+" | "+d.evaluate(`document.querySelector(".editor-area").contentEditable`),
		sample+" | "+strings.Join([]string{`empty false, words 49, characters 259, unsaved false, open false, address ""`,
			"49 words, " + exampleEditorHint + " | nothing",
			"pressed none | disabled align-left align-center align-right link undo redo | closed | focus main | ran nothing | true"}, " | "))

	// italic over the bold, a line after the last, two of its words linked
	d.evaluate(`window.__editor.select("every day")`)
	editorClick(d, `[data-kitwork-format="italic"]`)
	editorPress(d, "Control+End")
	editorPress(d, "Enter")
	editorType(d, "Closed on public holidays.")
	d.evaluate(`window.__editor.select("public holidays")`)
	editorClick(d, `.editor-toolbar [data-kitwork-command="link"]`)
	d.call("Emulation.setDeviceMetricsOverride", map[string]any{"width": 390, "height": 844, "deviceScaleFactor": 1, "mobile": true})
	check("at 390px with the popover open", d.evaluate(`window.__editor.frames().then(function () {
  return document.documentElement.scrollWidth + " wide, popover " + (document.querySelector(".editor-popover").hidden ? "closed" : "open"); })`), "390 wide, popover open")
	d.call("Emulation.clearDeviceMetricsOverride", map[string]any{})
	editorType(d, "/docs")
	editorPress(d, "Enter")
	written := editorStep(t, d, "written")
	want := strings.Replace(sample, "<strong>every day</strong>", "<strong><em>every day</em></strong>", 1) +
		"\n<p>Closed on <a href=\"/docs\">public holidays</a>.</p>"
	check("written", editorSays(written), want+" | "+strings.Join([]string{`empty false, words 53, characters 285, unsaved true, open false, address "/docs"`,
		"53 words, " + exampleEditorHint + " | Not saved",
		"pressed none | disabled redo | closed | focus area | ran nothing"}, " | "))

	// the centred heading made H4 from the toolbar's dropdown, its alignment kept; at a phone's width the
	// dropdown open makes no sideways scroll; a click outside it closes it (click-outside on <dropdown>)
	d.evaluate(editorV2Probe)
	menu := func() string {
		return d.evaluate(`window.__editorV2.read().then(function (raw) { var r = JSON.parse(raw); return r.menu + " | pressed " + r.pressed + " | focus " + r.focus; })`)
	}
	editorClick(d, ".editor-area h2")
	editorCaretIn(d, "Opening hours")
	editorClick(d, `.editor-toolbar [data-kitwork-component="dropdown"] > button`)
	check("the dropdown opened", menu(), "open, expanded true, open true | pressed format=rich heading-2 align-center | focus trigger")
	d.call("Emulation.setDeviceMetricsOverride", map[string]any{"width": 390, "height": 844, "deviceScaleFactor": 1, "mobile": true})
	check("at 390px with the dropdown open", d.evaluate(`window.__editor.frames().then(function () {
  var pop = document.querySelector('.editor-toolbar [data-kitwork-component="dropdown"] .popover').getBoundingClientRect();
  return document.documentElement.scrollWidth + " wide, the dropdown " + (pop.left >= 0 && pop.right <= 390 ? "inside the screen" : "from " + pop.left + " to " + pop.right); })`), "390 wide, the dropdown inside the screen")
	d.call("Emulation.clearDeviceMetricsOverride", map[string]any{})
	editorClick(d, `[data-kitwork-format="heading-4"]`)
	want = strings.Replace(want, `<h2 class="align-center">Opening hours</h2>`, `<h4 class="align-center">Opening hours</h4>`, 1)
	written = editorStep(t, d, "H4")
	check("H4 from the dropdown", written.Field+" | "+menu(), want+" | closed, expanded false, open false | pressed format=rich heading-4 align-center | focus area")
	editorClick(d, `.editor-toolbar [data-kitwork-component="dropdown"] > button`)
	editorClick(d, ".editor-area p")
	check("a click outside the open dropdown", menu(), "closed, expanded false, open false | pressed format=rich align-left | focus area")
	drawn := d.evaluate(`window.__editorV2.look(".editor-area")`)

	// the formats: Markdown by the mouse, the post as Markdown (its centred heading a line of HTML, its
	// underline <u>, its code block fenced, its links [words](address)); Rich again, the same post drawn
	// the same, nothing said left out
	editorClick(d, `[data-kitwork-format-value="markdown"]`)
	check("Markdown on the demo", editorFormatsSays(editorFormatsAt(t, d, "markdown", "Markdown on the demo")), editorFormatsSays(editorFormatsRead{
		Shown: "markdown shown, markdown pressed", Text: exampleEditorMarkdown, Field: written.Field, Focus: "text",
		Bar: "pressed none, off align-left align-center align-right", Ran: "nothing"}))
	editorClick(d, `[data-kitwork-format-value="rich"]`)
	again := editorFormatsAt(t, d, "rich", "Rich again on the demo")
	check("Rich again on the demo", again.Field+" | left out "+jsString(again.LeftOut)+" | focus "+again.Focus, written.Field+` | left out "" | focus area`)
	d.evaluate(editorV2Probe)
	check("drawn after Markdown as before", d.evaluate(`window.__editorV2.look(".editor-area")`), drawn)

	// Save
	from = site.heard()
	editorClickAway(d, `button[type="submit"]`)
	check("Save clicked", editorDialog(d), "no question")
	site.waitFor(t, "the post", func() bool { return strings.Contains(site.pages(from), "POST") })
	d.loaded(`(window.__before ? "wait of 0" : ` + exampleSettle + `)`)
	check("what the browser sent", site.pages(from), "POST /components/editor 200 site=same-origin application/x-www-form-urlencoded")
	check("the post shown", d.evaluate(`document.querySelector("div.richtext:not(.editor-area)").innerHTML`), richtext.Display(written.Field, exampleRichtextOrigin))
	check("the HTML it kept", d.evaluate(`document.querySelector("pre code.language-html").textContent`), written.Field)
	d.evaluate(editorV2Probe)
	check("the post drawn by <richtext>, as the editor drew it", d.evaluate(`window.__editorV2.look("div.richtext:not(.editor-area)")`), drawn)
	answer := editorStep(t, d, "the answer")
	check("the answer's editor", answer.Field+" | "+answer.State, written.Field+` | empty false, words 53, characters 285, unsaved true, open false, address ""`)
	check("complaints", d.evaluate(`JSON.stringify(window.__complaints)`), "[]")
}
