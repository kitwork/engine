package work

import (
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/kitwork/engine/utilities/richtext"
)

// exampleRichtextRe finds the richtext of a page the engine wrote, its HTML as written (the cleaner's
// grammar has no <div>, so the first </div> is its own).
var exampleRichtextRe = regexp.MustCompile(`<div class="richtext">([\s\S]*?)</div>`)

// exampleHTMLCodeRe finds a block of HTML code on a page the engine wrote.
var exampleHTMLCodeRe = regexp.MustCompile(`<code class="language-html">([\s\S]*?)</code>`)

// exampleTextareaRe finds the textarea of a page the engine wrote and what it holds.
var exampleTextareaRe = regexp.MustCompile(`<textarea[^>]*name="body"[^>]*>([\s\S]*?)</textarea>`)

// exampleRichtextOrigin is the origin the example is served on in these tests: what the richtext's
// links are measured against.
const exampleRichtextOrigin = "http://" + exampleHost

// exampleSend sends one request to the example with a form body, as a browser on the site's own page
// sends it unless header says otherwise, and returns the answer, failing when it links warnings.
func exampleSend(t *testing.T, tenant *Tenant, method, path, body string, header map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, exampleRichtextOrigin+path, strings.NewReader(body))
	if method == http.MethodPost {
		request.Header.Set("Content-Type", urlencoded)
		request.Header.Set("Origin", exampleRichtextOrigin)
		request.Header.Set("Sec-Fetch-Site", "same-origin")
	}
	for name, value := range header {
		request.Header.Set(name, value)
	}
	rec := httptest.NewRecorder()
	tenant.Serve(rec, request)
	if link := warningsScriptRe.FindStringSubmatch(rec.Body.String()); link != nil {
		r := httptest.NewRecorder()
		tenant.Serve(r, httptest.NewRequest(http.MethodGet, exampleRichtextOrigin+link[1], nil))
		t.Errorf("%s %s links warnings:\n%.2000s", method, path, r.Body.String())
	}
	return rec
}

// exampleShows reads what a page of the example shows of a post: the richtext's HTML, the text of each
// block of HTML code, and what the textarea holds ("no textarea" when there is none).
func exampleShows(page string) (shown string, codes []string, written string) {
	shown = "no richtext"
	if m := exampleRichtextRe.FindStringSubmatch(page); m != nil {
		shown = m[1]
	}
	for _, m := range exampleHTMLCodeRe.FindAllStringSubmatch(page, -1) {
		codes = append(codes, textContent(m[1]))
	}
	written = "no textarea"
	if m := exampleTextareaRe.FindStringSubmatch(page); m != nil {
		written = html.UnescapeString(m[1])
	}
	return shown, codes, written
}

// exampleRuns lists the markup of a post that would run on a page, written as markup (never as the
// words of a code block or a textarea, whose brackets and quotes are escaped).
func exampleRuns(page string) []string {
	var found []string
	for _, attack := range []string{"<script>alert", "<script>window", `onclick="`, `onmouseover="`, `onerror="`, `onload="`, `ontoggle="`, `srcdoc="`,
		`href="javascript:`, `href=" JaVaScRiPt:`, `<img src="/nothing.png"`, "<svg", "<iframe", "<details", "<style>body"} {
		if strings.Contains(page, attack) {
			found = append(found, attack)
		}
	}
	return found
}

// The kitwork example shows richtext in two places, every page with no warning.
//   - /components, #richtext: the sample post of .shared/site.work (it holds a script, an onclick and a
//     javascript: link) as it was saved, in a block of HTML code; through <richtext value="{{ sample }}" />,
//     exactly what richtext.Display writes for it on the site's own origin; and the HTML richtext() of
//     "kitwork" kept, exactly richtext.Clean's. None of the attack is markup on the page, and the
//     catalogue lists richtext as a built element. A link goes to the page that tries it.
//   - /components/richtext: the same sample in the form's textarea, shown cleaned. The form posts to the
//     page itself; router.post reads the field from the body and answers the page for what was sent:
//     the textarea given back what was written, as words, the richtext of it and the HTML it kept. An
//     empty post says nothing was kept and shows no richtext. A post from another site is refused 403
//     before router.post.
//
// CONTROL, against a copy served through -overlay (the tree untouched, run 20/10/2026): with <richtext>
// writing its value as it came (ui/richtext.go, no richtext.Display), /components showed the sample as
// it was saved, its script, onclick and javascript: link markup on the page, and the answer to the post
// lost the map link's rel="external noopener"; this test failed on both pages.
func TestExampleRichtext(t *testing.T) {
	tenant, page := examplePage(t, "/components")
	start := strings.Index(page, `id="richtext"`)
	end := strings.Index(page[max(start, 0):], "</section>")
	if start < 0 || end < 0 {
		t.Fatalf("/components has no section id=\"richtext\":\n%.2000s", page)
	}
	section := page[start : start+end]
	shown, codes, _ := exampleShows(section)
	if len(codes) != 2 {
		t.Fatalf("the richtext section has %d blocks of HTML code, want the saved post and the HTML kept:\n%s", len(codes), section)
	}
	sample, kept := codes[0], codes[1]
	for _, want := range []string{"<script>alert('hi')</script>", `onclick="alert('hi')"`, `href="javascript:alert('hi')"`, "<li>Latte<ul><li>with oat milk</li></ul>"} {
		if !strings.Contains(sample, want) {
			t.Errorf("the saved post shown has no %s: %s", want, sample)
		}
	}
	if want := richtext.Clean(sample).HTML; kept != want {
		t.Errorf("the HTML kept:\n got  %s\n want %s", kept, want)
	}
	if want := richtext.Display(sample, exampleRichtextOrigin); shown != want {
		t.Errorf("/components shows:\n got  %s\n want %s", shown, want)
	}
	for _, want := range []string{`<a href="/docs">the docs</a>`, "<p>Book a table or read", "<li>with oat milk</li>", `href="/components/richtext"`} {
		if !strings.Contains(section, want) {
			t.Errorf("the richtext section has no %s", want)
		}
	}
	if found := exampleRuns(page); len(found) > 0 {
		t.Errorf("/components carries the attack as markup: %s", strings.Join(found, ", "))
	}
	if !strings.Contains(page, `>richtext</p>`) || !strings.Contains(page, "A post someone wrote or pasted, cleaned on the server") {
		t.Error("the catalogue of /components does not list richtext")
	}

	// the page that tries it starts with the sample
	rec := exampleSend(t, tenant, http.MethodGet, "/components/richtext", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /components/richtext = %d:\n%.1500s", rec.Code, rec.Body.String())
	}
	shown, codes, written := exampleShows(rec.Body.String())
	if written != sample || shown != richtext.Display(sample, exampleRichtextOrigin) || len(codes) != 1 || codes[0] != kept {
		t.Errorf("GET /components/richtext shows\n textarea %q\n richtext %s\n code %q\nwant the sample, cleaned", written, shown, codes)
	}
	if !strings.Contains(rec.Body.String(), `<form class="flex-column gap-3 max-width-large" method="post">`) {
		t.Errorf("the page's form does not post:\n%.3000s", rec.Body.String())
	}

	// a post of the form, same-origin: the page for what was sent
	rec = exampleSend(t, tenant, http.MethodPost, "/components/richtext", "body="+url.QueryEscape(richtextAttack), nil)
	answer := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /components/richtext = %d:\n%.1500s", rec.Code, answer)
	}
	shown, codes, written = exampleShows(answer)
	if written != richtextAttack {
		t.Errorf("the textarea is given back\n %q\nwant\n %q", written, richtextAttack)
	}
	if want := richtext.Display(richtextAttack, exampleRichtextOrigin); shown != want {
		t.Errorf("the answer shows:\n got  %s\n want %s", shown, want)
	}
	if want := richtext.Clean(richtextAttack).HTML; len(codes) != 1 || codes[0] != want {
		t.Errorf("the answer's HTML kept:\n got  %q\n want %q", codes, want)
	}
	if found := exampleRuns(answer); len(found) > 0 {
		t.Errorf("the answer to the post carries the attack as markup: %s", strings.Join(found, ", "))
	}
	if !strings.Contains(answer, "<title>Try richtext — Kitwork</title>") || !strings.Contains(answer, `href="/docs"`) {
		t.Error("the answer to the post is not the whole page (its title, the header's navigation)")
	}

	// an empty post keeps nothing
	rec = exampleSend(t, tenant, http.MethodPost, "/components/richtext", "body=", nil)
	if shown, _, _ := exampleShows(rec.Body.String()); rec.Code != http.StatusOK || shown != "no richtext" || !strings.Contains(rec.Body.String(), "Nothing was kept") {
		t.Errorf("an empty post: %d, richtext %s:\n%.2000s", rec.Code, shown, rec.Body.String())
	}

	// a page of another site posting to it is refused before router.post
	rec = exampleSend(t, tenant, http.MethodPost, "/components/richtext", "body=x", map[string]string{"Origin": "https://evil.example", "Sec-Fetch-Site": "cross-site"})
	if rec.Code != http.StatusForbidden || strings.Contains(rec.Body.String(), "richtext") && strings.Contains(rec.Body.String(), "<html") {
		t.Errorf("a post from another site: %d\n%.500s", rec.Code, rec.Body.String())
	}
}
