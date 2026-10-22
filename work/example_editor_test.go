package work

import (
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/kitwork/engine/utilities/richtext"
)

// exampleEditorAreaRe finds the writing area of an editor on a page the engine wrote and what it holds
// (the grammar it holds has no <div>, so the first </div> is its own); exampleEditorFieldRe its field
// and what that holds; exampleEditorScopeRe the editor's first state.
var (
	exampleEditorAreaRe  = regexp.MustCompile(`<div class="richtext editor-area[^"]*"[^>]*>([\s\S]*?)</div>`)
	exampleEditorFieldRe = regexp.MustCompile(`<textarea class="editor-field[^"]*"[^>]*name="body"[^>]*>([\s\S]*?)</textarea>`)
	exampleEditorScopeRe = regexp.MustCompile(`data-kitwork-component="editor: \$body" data-kitwork-scope="([^"]*)"`)
)

// exampleEditorSample is the post the editor of /components/editor starts with (its router.view): a
// centred heading, an underline, a code block of three lines.
const exampleEditorSample = "<h2 class=\"align-center\">Opening hours</h2>\n" +
	"<p>Open <strong>every day</strong> from 7 am to 10 pm, <u>the kitchen until 9</u>. Select a few words and try the buttons.</p>\n" +
	"<ul><li>Espresso</li><li>Latte with oat milk</li></ul>\n" +
	"<p>The board by the door is drawn with Kitwork UI:</p>\n" +
	"<pre><code>&lt;text heading=\"2\" align=\"center\"&gt;\n  Opening hours\n&lt;/text&gt;</code></pre>\n" +
	"<p>Read more in <a href=\"/docs\">the docs</a>.</p>"

// exampleEditorToolbarV2 is what the toolbar of v2 writes on a page of the example, its editor named
// alias: its seven groups, named; the dropdown of smaller headings, its trigger controlling its popover,
// closing on a click outside it and on Escape, its buttons calling the editor through alias; the new
// marks, the code block, the alignments and the rule.
func exampleEditorToolbarV2(alias string) []string {
	out := []string{`data-kitwork-component="dropdown" data-kitwork-event="click-outside: close(); keydown.escape: close()"`,
		`aria-controls="kitwork-editor-1-menu-1" data-kitwork-bind="aria-expanded: open" data-kitwork-event="click: toggle()"`}
	// the formats (21/10/2026): the canonical segment first, Rich the toolbar's one stop and pressed
	for i, format := range []string{"rich", "markdown", "html"} {
		tab := map[bool]string{true: "0", false: "-1"}[i == 0]
		out = append(out, `tabindex="`+tab+`" data-kitwork-command="format" data-kitwork-format-value="`+format+`" data-kitwork-bind="aria-pressed: format == &#39;`+format+
			`&#39;" data-kitwork-event="click: format = &#39;`+format+`&#39;" aria-pressed="`+fmt.Sprint(i == 0)+`">`+map[string]string{"rich": "Rich", "markdown": "Markdown", "html": "HTML"}[format]+`</button>`)
	}
	out = append(out, `tabindex="-1" data-kitwork-format="bold"`)
	for _, group := range []string{"Write in", "Text", "Headings", "Blocks", "Lists", "Alignment", "Insert", "History"} {
		out = append(out, `editor-group" aria-label="`+group+`" role="group">`)
	}
	for level := 4; level <= 6; level++ {
		out = append(out, fmt.Sprintf(`data-kitwork-format="heading-%d" aria-pressed="false" data-kitwork-event="click: %s.toggle(&#39;heading&#39;, %d)"`, level, alias, level))
	}
	for _, format := range []string{"underline", "strikethrough", "code", "code-block"} {
		out = append(out, `data-kitwork-format="`+format+`" aria-pressed="false"`)
	}
	return append(out, `data-kitwork-command="align-left" aria-pressed="false"`, `data-kitwork-command="align-center" aria-pressed="false"`,
		`data-kitwork-command="align-right" aria-pressed="false"`, `data-kitwork-command="rule"`)
}

// exampleEditorFormats holds a demo's editor (page, named what) to the formats: its group of formats is
// the toolbar's first ("Write in", before "Text"), and the words that say what a switch or a paste left
// out (leftOut, the line as the engine writes it, hidden while nothing was) are on it.
func exampleEditorFormats(t *testing.T, what, page, leftOut string) {
	t.Helper()
	formats, text := strings.Index(page, `aria-label="Write in" role="group"`), strings.Index(page, `aria-label="Text" role="group"`)
	if formats < 0 || text < 0 || formats > text {
		t.Errorf("%s: the group of formats is not the toolbar's first (Write in at %d, Text at %d)", what, formats, text)
	}
	if !strings.Contains(page, leftOut) {
		t.Errorf("%s has no line of what was left out, %s", what, leftOut)
	}
}

// exampleEditorShows reads what a page of the example shows of the editor: the area's HTML, the
// field's value, the editor's first state ("no editor" when there is none).
func exampleEditorShows(page string) (area, field, scope string) {
	area, field, scope = "no editor", "no editor", "no editor"
	if m := exampleEditorAreaRe.FindStringSubmatch(page); m != nil {
		area = m[1]
	}
	if m := exampleEditorFieldRe.FindStringSubmatch(page); m != nil {
		field = html.UnescapeString(m[1])
	}
	if m := exampleEditorScopeRe.FindStringSubmatch(page); m != nil {
		scope = html.UnescapeString(m[1])
	}
	return area, field, scope
}

// The kitwork example shows the editor in two places, every page with no warning, each with the toolbar
// of v2 (exampleEditorToolbarV2: its groups, the dropdown of smaller headings calling the editor through
// the page's alias and closing on a click outside it and on Escape, the new marks, the code block, the
// alignments, the rule).
//   - /components: the catalogue lists editor as a built component, and the #editor section ("Write a
//     post in place") says what it is, holds a live editor (its toolbar and writing area, the area and
//     its field holding the sample post of #richtext cleaned, in a form with no Save: this page answers
//     no post) and links to the page that tries it with Save.
//   - /components/editor: the editor of the decision with English words, in a post form: the writing
//     area and its field hold the sample post (a centred heading, an underline, a code block) cleaned
//     (richtext.Clean), its first state counts it, not unsaved, and nothing below the form yet; the page
//     carries the editor's script and the dropdown's. Saving posts the
//     form to the page itself: router.post cleans what was sent and answers with the page for it, the
//     editor holding it cleaned (unsaved: the decision's answer to a post), the post through <richtext>
//     exactly as richtext.Display writes it and the HTML kept exactly richtext.Clean's; none of an
//     attack is markup anywhere on the answer. A post over 20 000 characters is answered with an empty
//     editor and the word under it; one from another site is refused 403 before router.post.
//
// CONTROL, against copies served through -overlay (the tree untouched, run 20/10/2026): the editor's
// value painted as it came (ui/editor.go, editorValueOp without richtext.Clean) left the sample as
// written in the area and the field ("GET: the area", "GET: the field") and the attack as it came in
// the answer's area ("POST: the area"); the page rendered without the request's method
// (work/router_ui.go, no Method) painted the answer to the post saved ("POST: the editor's first state
// is not unsaved"). With the #editor section of /components as it was before the review of 20/10/2026
// (words and a link, no editor; a copy of the examples tree, examplesRoot pointed at it through an
// overlaid example_sites_test.go), "the section's editor" failed on every part. Toolbar v2 (run
// 21/10/2026, controls.py in the scratchpad's editor-more/integrate, the same way through a copy of the
// examples tree): with both demos as they were before it, "the section's editor has no …" and "GET
// /components/editor has no …" failed for every part of exampleEditorToolbarV2, and "GET: the area",
// "GET: the field" and "GET: the editor's first state" on the old sample; with the page's <dropdown> written without click-outside
// and keydown:escape, "GET /components/editor has no" its events.
//
// The formats (21/10/2026): both demos draw the canonical segment as their toolbar's first group ("Write
// in": Rich, Markdown, HTML, Rich the toolbar's one stop and pressed, Bold -1) and say what a switch or
// a paste left out (the section through $draft, the page bare inside its editor).
// CONTROL (run 21/10/2026, c23 in the scratchpad's editor-formats/integrate, the same way through a copy
// of the examples tree): with both demos without the segment and the line, "the section's editor has
// no", "GET /components/editor has no" (each switch, Bold's -1, the group), "the group of formats is
// not the toolbar's first" and "has no line of what was left out" failed for both.
func TestExampleEditor(t *testing.T) {
	tenant, page := examplePage(t, "/components")
	start := strings.Index(page, `id="editor"`)
	end := strings.Index(page[max(start, 0):], "</section>")
	if start < 0 || end < 0 {
		t.Fatalf("/components has no section id=\"editor\":\n%.2000s", page)
	}
	section := page[start : start+end]
	for _, want := range []string{"Write a post in place", `href="/components/editor"`, "Try the editor"} {
		if !strings.Contains(section, want) {
			t.Errorf("the editor section has no %s:\n%s", want, section)
		}
	}
	// the section's editor is live, in a form with no Save
	rich := page[strings.Index(page, `id="richtext"`):]
	_, richCodes, _ := exampleShows(rich[:strings.Index(rich, "</section>")])
	if len(richCodes) == 0 {
		t.Fatalf("the #richtext section shows no sample post")
	}
	drafted := richtext.Clean(richCodes[0]).HTML
	if area, field, _ := exampleEditorShows(section); area != drafted || field != drafted {
		t.Errorf("the section's editor holds\n area  %q\n field %q\nwant the sample of #richtext cleaned, %q", area, field, drafted)
	}
	for _, want := range append(exampleEditorToolbarV2("$draft"), `data-kitwork-component="editor: $draft"`, `role="toolbar"`, `role="textbox"`,
		`data-kitwork-format="bold"`, `data-kitwork-command="link"`, `role="dialog" aria-label="Link address"`) {
		if !strings.Contains(section, want) {
			t.Errorf("the section's editor has no %s", want)
		}
	}
	if strings.Contains(section, `type="submit"`) {
		t.Error("the section's form has a Save, which /components (no router.post) cannot answer")
	}
	// the formats: the segment first in the toolbar, what a switch left out said under the editor
	exampleEditorFormats(t, "the section's editor", section, `data-kitwork-bind="show: $draft.dropped.length; text: [&#39;Left out: &#39;, ($draft.dropped.join(&#39;, &#39;))].join(&#39;&#39;)" hidden>Left out: </p>`)
	catalogue := regexp.MustCompile(`>editor</p>\s*<[^>]*>[\s\S]{0,400}?Built[\s\S]{0,400}?Rich text written in place`)
	if !catalogue.MatchString(page) {
		t.Error("the catalogue of /components does not list editor as built")
	}

	check := func(what, got, want string) {
		t.Helper()
		if got != want {
			t.Errorf("%s:\n got  %s\n want %s", what, got, want)
		}
	}
	// the page that tries it starts with the sample, cleaned
	rec := exampleSend(t, tenant, http.MethodGet, "/components/editor", "", nil)
	first := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /components/editor = %d:\n%.1500s", rec.Code, first)
	}
	sample := richtext.Clean(exampleEditorSample)
	area, field, scope := exampleEditorShows(first)
	check("GET: the area", area, sample.HTML)
	check("GET: the field", field, sample.HTML)
	check("GET: the editor's first state", scope, "empty: false; words: 49; characters: 259; unsaved: false; open: false; address: ''; format: 'rich'; dropped: []")
	for _, want := range append(exampleEditorToolbarV2("$body"), `<form class="flex-column gap-3 max-width-large" method="post">`, `role="toolbar"`,
		`aria-label="Formatting"`, `data-kitwork-format="bold"`, `data-kitwork-command="link"`, `role="dialog" aria-label="Link address"`,
		`/_kitwork/components/editor.`, `/_kitwork/components/dropdown.`) {
		if !strings.Contains(first, want) {
			t.Errorf("GET /components/editor has no %s", want)
		}
	}
	if strings.Contains(first, "What the page shows") {
		t.Error("GET /components/editor shows a result before anything was sent")
	}
	exampleEditorFormats(t, "GET /components/editor", first, `data-kitwork-bind="show: dropped.length; text: [&#39;Left out: &#39;, (dropped.join(&#39;, &#39;))].join(&#39;&#39;)" hidden>Left out: </p>`)

	// Save, a post of the form, same-origin: the page for what was sent
	rec = exampleSend(t, tenant, http.MethodPost, "/components/editor", "body="+url.QueryEscape(richtextAttack), nil)
	answer := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /components/editor = %d:\n%.1500s", rec.Code, answer)
	}
	cleaned := richtext.Clean(richtextAttack)
	area, field, scope = exampleEditorShows(answer)
	check("POST: the area", area, cleaned.HTML)
	check("POST: the field", field, cleaned.HTML)
	if !strings.HasSuffix(scope, "; unsaved: true; open: false; address: ''; format: 'rich'; dropped: []") {
		t.Errorf("POST: the editor's first state is not unsaved: %s", scope)
	}
	if m := exampleRichtextRe.FindStringSubmatch(answer); m == nil || m[1] != richtext.Display(richtextAttack, exampleRichtextOrigin) {
		t.Errorf("POST: the post shown is not richtext.Display's:\n%v", m)
	}
	if _, codes, _ := exampleShows(answer); len(codes) != 1 || codes[0] != cleaned.HTML {
		t.Errorf("POST: the HTML kept:\n got  %q\n want %q", codes, cleaned.HTML)
	}
	if found := exampleRuns(answer); len(found) > 0 {
		t.Errorf("the answer to the post carries the attack as markup: %s", strings.Join(found, ", "))
	}
	if !strings.Contains(answer, "<title>Try the editor — Kitwork</title>") || !strings.Contains(answer, `href="/docs"`) {
		t.Error("the answer to the post is not the whole page (its title, the header's navigation)")
	}

	// a post too long for the demo: an empty editor, the word under it
	rec = exampleSend(t, tenant, http.MethodPost, "/components/editor", "body="+url.QueryEscape("<p>"+strings.Repeat("a", 20001)+"</p>"), nil)
	area, field, scope = exampleEditorShows(rec.Body.String())
	if rec.Code != http.StatusOK || area != "" || field != "" || !strings.HasPrefix(scope, "empty: true; words: 0;") ||
		!strings.Contains(rec.Body.String(), "This demo takes a post of up to 20 000 characters.") {
		t.Errorf("a post too long: %d, area %q, field %q, %s", rec.Code, area, field, scope)
	}

	// a page of another site posting to it is refused before router.post
	rec = exampleSend(t, tenant, http.MethodPost, "/components/editor", "body=x", map[string]string{"Origin": "https://evil.example", "Sec-Fetch-Site": "cross-site"})
	if rec.Code != http.StatusForbidden || strings.Contains(rec.Body.String(), "editor-area") {
		t.Errorf("a post from another site: %d\n%.500s", rec.Code, rec.Body.String())
	}
}
