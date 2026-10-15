package work

import (
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/kitwork/engine/ui/stylesheet"
)

// scrolledFiles is a site whose / carries a scrolled: class (a pinned bar that floats up once the page
// scrolls) and whose /khong carries none: the same layout, other content.
var scrolledFiles = map[string]string{
	"localhost/router.work": `import { router } from "kitwork";

router.colors({ brand: "#c8742c", canvas: "#f6f3ee", paper: "#ffffff", ink: "#2b211b" });
`,
	"localhost/page.ui":          `<page><content /></page>`,
	"localhost/content.ui":       `<content><box pin="top" scrolled:shadow="small" scrolled:background="paper"><text>Thanh</text></box></content>`,
	"localhost/khong/content.ui": `<content><box pin="top" shadow="small"><text>Không cuộn</text></box></content>`,
}

// scrolledTagRe is the script a page that can carry a scrolled: class links: scrolled.<hash>.js under
// /_kitwork/, marked by its role, deferred. The minifier may drop the quotes.
var scrolledTagRe = regexp.MustCompile(`<script data-kitwork-generated="?scrolled"? src="?(/_kitwork/scrolled\.[0-9a-f]{16}\.js)"? defer>\s*</script>`)

// A page whose HTML can carry a scrolled: class links scrolled.<hash>.js once, right after its
// stylesheets and before the engine's other scripts, in development and in production; the file is
// the engine's, the same bytes for every site, kept for good under its name. CONTROL: a page of the
// same site without scrolled: links no script of that role and names no such file.
func TestUIPageLinksTheScrolledScriptOnlyWhenItCanCarryTheClass(t *testing.T) {
	want := stylesheet.ScrolledScript()
	for _, local := range []bool{true, false} {
		tenant := buildUISiteMode(t, scrolledFiles, local)
		page := serveUI(t, tenant, "http://localhost/").Body.String()
		tags := scrolledTagRe.FindAllStringSubmatch(page, -1)
		if len(tags) != 1 {
			t.Fatalf("local=%v: the page links scrolled.<hash>.js once, found %d:\n%.2000s", local, len(tags), page)
		}
		if tags[0][1] != "/_kitwork/"+want.Name+".js" {
			t.Errorf("local=%v: the script is %s, want /_kitwork/%s.js", local, tags[0][1], want.Name)
		}
		sheets := stylesheetsOf(t, tenant, page)
		last := strings.Index(page, sheets[len(sheets)-1].href)
		at := strings.Index(page, tags[0][0])
		if last < 0 || at < last {
			t.Errorf("local=%v: the script comes after the stylesheets:\n%.2000s", local, page)
		}
		if runtime := strings.Index(page, `data-kitwork-generated="runtime"`); runtime >= 0 && runtime < at {
			t.Errorf("local=%v: the script comes before the engine's other scripts", local)
		}
		if strings.Contains(page, "<script>") {
			t.Errorf("local=%v: nothing is inlined, the head links the file:\n%.2000s", local, page)
		}

		answer := serveUI(t, tenant, "http://localhost"+tags[0][1])
		if answer.Code != http.StatusOK || answer.Body.String() != string(want.JavaScript) {
			t.Fatalf("local=%v: %s = %d, want the script:\n%.300s", local, tags[0][1], answer.Code, answer.Body.String())
		}
		if got := answer.Header().Get("Content-Type"); got != "text/javascript; charset=utf-8" {
			t.Errorf("local=%v: Content-Type %q", local, got)
		}
		if got := answer.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
			t.Errorf("local=%v: the current name is kept for good, Cache-Control %q", local, got)
		}

		other := serveUI(t, tenant, "http://localhost/khong").Body.String()
		if strings.Contains(other, "scrolled.") || strings.Contains(other, `"scrolled"`) {
			t.Errorf("local=%v: control: a page without scrolled: links no script:\n%.2000s", local, other)
		}
		if len(stylesheetsOf(t, tenant, other)) == 0 {
			t.Fatalf("local=%v: control: /khong is a .ui page with its stylesheets", local)
		}
	}
}

// A name of scrolled.<hash>.js an older engine wrote (another hash: a page left open across a deploy)
// gets today's file, not kept under that name. What is not that name's spelling is the engine's 404.
func TestScrolledScriptOldAndWrongNames(t *testing.T) {
	tenant := buildUISite(t, scrolledFiles)
	want := string(stylesheet.ScrolledScript().JavaScript)
	old := serveUI(t, tenant, "http://localhost/_kitwork/scrolled.0123456789abcdef.js")
	if old.Code != http.StatusOK || old.Body.String() != want || old.Header().Get("Cache-Control") != "no-cache" {
		t.Errorf("an old hash = %d %q, want today's file, no-cache", old.Code, old.Header().Get("Cache-Control"))
	}
	for _, name := range []string{
		"/_kitwork/scrolled.js", "/_kitwork/scrolled.0123456789ABCDEF.js", "/_kitwork/scrolled.0123456789abcde.js",
		"/_kitwork/scrolled.0123456789abcdefa.js", "/_kitwork/scrolled.0123456789abcdef.css", "/_kitwork/scrolled/x.0123456789abcdef.js",
	} {
		if got := serveUI(t, tenant, "http://localhost"+name); got.Code != http.StatusNotFound {
			t.Errorf("%s = %d, want the engine's 404", name, got.Code)
		}
	}
}
