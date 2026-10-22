package work

import (
	"encoding/json"
	"html"
	"net/http"
	"net/url"
	"strconv"
	"testing"

	"github.com/kitwork/engine/utilities/richtext"
)

// richtextAnswer is the object richtext(…) gives, as the JSON a handler answers with it.
type richtextAnswer struct {
	Value      string `json:"value"`
	Text       string `json:"text"`
	Words      int    `json:"words"`
	Characters int    `json:"characters"`
	Empty      bool   `json:"empty"`
}

func readRichtext(t *testing.T, name, body string) []richtextAnswer {
	t.Helper()
	var answers []richtextAnswer
	if err := json.Unmarshal([]byte(body), &answers); err != nil {
		t.Fatalf("%s: %v in %q", name, err, body)
	}
	return answers
}

func wantRichtext(source string) richtextAnswer {
	document := richtext.Clean(source)
	return richtextAnswer{document.HTML, document.Text, document.Words, document.Characters, document.Empty()}
}

// The edit page of docs/components/richtext.md ("Lưu một bài") gives the textarea the saved value, the
// cleaned HTML, and saves what comes back through richtext(): a post opened and saved without a change
// is the same post. value re-cleans to itself (Clean(Clean(x)) == Clean(x)); text does not, and is not
// the value to edit: it holds a word written "<script>" or "<b>" as those characters, which a source
// read again takes for tags. The saved post here holds such words, an ampersand and a list.
//
// CONTROL: with the textarea given post.text (as the page was first documented), saving it unchanged
// kept "<p>Dùng</p>" and lost the rest of the post, and this test failed.
func TestRichtextValueEditsBackUnchanged(t *testing.T) {
	const saved = "<p>Dùng &lt;script&gt; trong trang &amp; ngoài trang</p>\n<p>Viết thẻ &lt;b&gt; để in đậm, rồi &lt;/b&gt; để thôi.</p>\n<ul>\n<li>một</li>\n<li>hai</li>\n</ul>"
	if again := richtext.Clean(saved).HTML; again != saved {
		t.Fatalf("the saved post is not a value richtext() gives: %q", again)
	}
	tn := NewTenant(writeSite(t, map[string]string{
		"router.work": `import * as router from "kitwork/router";`,
		"page.ui":     `<page><content /></page>`,
		"bai/router.work": "import * as router from \"kitwork/router\";\n" +
			"import { richtext } from \"kitwork\";\n" +
			"const saved = " + strconv.Quote(saved) + ";\n" +
			"router.get((context) => context.view({ post: { body: saved, text: richtext(saved).text } }));\n" +
			"router.post((context) => context.json([richtext(context.request.form(\"body\")).value]));\n",
		"bai/content.ui": `<content gap="4" padding="6" max-width="640px">
  <form method="post" gap="3">
    <field>
      <label>Bài viết</label>
      <textarea name="body" rows="12" value="{{ post.body }}" required="true" />
    </field>
    <button type="submit">Lưu</button>
  </form>
  <richtext value="{{ post.body }}" />
</content>`,
	}), "localhost")
	if err := tn.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	page := sendRequest(tn, http.MethodGet, "/bai", "", "", nil)
	m := exampleTextareaRe.FindStringSubmatch(page.Body.String())
	if page.Code != http.StatusOK || m == nil {
		t.Fatalf("GET /bai: %d, no textarea:\n%.2000s", page.Code, page.Body.String())
	}
	written := html.UnescapeString(m[1])
	browser := map[string]string{"Origin": "http://localhost", "Sec-Fetch-Site": "same-origin"}
	rec := sendRequest(tn, http.MethodPost, "/bai", urlencoded, "body="+url.QueryEscape(written), browser)
	var answer []string
	if err := json.Unmarshal(rec.Body.Bytes(), &answer); err != nil || rec.Code != http.StatusOK || len(answer) != 1 {
		t.Fatalf("POST /bai: %d %q (%v)", rec.Code, rec.Body.String(), err)
	}
	if answer[0] != saved {
		t.Errorf("the post opened and saved unchanged:\n got  %q\n want %q\n(the textarea held %q)", answer[0], saved, written)
	}
}

// import { richtext } from "kitwork": richtext(source) cleans rich text into the grammar of
// utilities/richtext and gives { value, text, words, characters, empty }, the five and no other. It
// never fails: null, undefined and no source are the empty document, and so are an object, a list and
// a function; a number is its text. A template site (router.kitwork.js) imports it the same way.
//
// CONTROL: with Richtext taken off KitWork, the import gave null and every call was a NOT_A_FUNCTION
// failure, and this test failed; with richtextSource reading args[0] without looking, richtext() with
// no source was a NATIVE_PANIC failure (index out of range), GET /note answered 500 and this test
// failed.
func TestRichtextBuiltin(t *testing.T) {
	tn := NewTenant(writeSite(t, map[string]string{
		"router.work": `import * as router from "kitwork/router";`,
		"note/router.work": "import * as router from \"kitwork/router\";\n" +
			"import { richtext } from \"kitwork\";\n" +
			"router.post(({ request, response }) => response.json([richtext(request.form(\"note\"))]));\n" +
			"router.get(({ response }) => response.json([richtext(null), richtext(undefined), richtext(), richtext({ a: 1 }), richtext([\"<b>x</b>\"]), richtext(() => 1), richtext(42), richtext(\"<script>alert(1)</script>\")]));\n",
	}), "localhost")
	if err := tn.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}

	note := "<h1>Chào</h1><p style=\"color:red\" onclick=\"x()\">Xin <b>chào</b> <a href=\"javascript:alert(1)\">bạn</a> <a href=\"https://kitwork.io/a?b=1&c=2\">đây</a></p>"
	rec := sendRequest(tn, http.MethodPost, "/note", urlencoded, "note="+url.QueryEscape(note), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /note: %d %q", rec.Code, rec.Body.String())
	}
	var fields []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &fields); err != nil || len(fields) != 1 {
		t.Fatalf("POST /note: %v %q", err, rec.Body.String())
	}
	if len(fields[0]) != 5 {
		t.Errorf("the object has %d names, want value, text, words, characters, empty: %v", len(fields[0]), fields[0])
	}
	got := readRichtext(t, "POST /note", rec.Body.String())[0]
	want := richtextAnswer{
		Value:      "<h2>Chào</h2>\n<p>Xin <strong>chào</strong> bạn <a href=\"https://kitwork.io/a?b=1&amp;c=2\">đây</a></p>",
		Text:       "Chào\n\nXin chào bạn đây",
		Words:      5,
		Characters: 20,
	}
	if got != want || got != wantRichtext(note) {
		t.Errorf("richtext(note) = %+v\nwant %+v", got, want)
	}

	rec = sendRequest(tn, http.MethodGet, "/note", "", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /note: %d %q", rec.Code, rec.Body.String())
	}
	empty := richtextAnswer{Empty: true}
	answers := readRichtext(t, "GET /note", rec.Body.String())
	for i, want := range []richtextAnswer{empty, empty, empty, empty, empty, empty, wantRichtext("42"), empty} {
		if i >= len(answers) || answers[i] != want {
			t.Errorf("GET /note answer %d: %+v, want %+v (all: %s)", i, answers, want, rec.Body.String())
			break
		}
	}
	if answers[6].Value != "<p>42</p>" {
		t.Errorf("richtext(42) = %q, want <p>42</p>", answers[6].Value)
	}

	template := NewTenant(writeSite(t, map[string]string{
		"filesystem.kitwork": "",
		"router.kitwork.js":  `import { router } from "kitwork";`,
		"note/router.kitwork.js": "import { router, richtext } from \"kitwork\";\n" +
			"router.get((ctx) => ctx.json([richtext(\"<b>x</b>\")]));\n",
	}), "localhost")
	if err := template.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	rec = sendRequest(template, http.MethodGet, "/note", "", "", nil)
	if answers := readRichtext(t, "template GET /note", rec.Body.String()); len(answers) != 1 || answers[0] != wantRichtext("<b>x</b>") || answers[0].Value != "<p><strong>x</strong></p>" {
		t.Errorf("template site: %d %s", rec.Code, rec.Body.String())
	}
}
