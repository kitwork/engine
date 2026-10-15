package render

import (
	"strings"
	"testing"

	"github.com/kitwork/engine/version"
)

// Every finished page names the engine and its release in <head>; an author's own generator wins,
// and a fragment without <head> is not given one.
func TestGeneratorNamesTheEngine(t *testing.T) {
	want := `<meta name="generator" content="Kitwork ` + version.Number + `">`
	page := `<!doctype html><html><head><title>x</title></head><body>x</body></html>`
	out := withGenerator(page)
	if !strings.Contains(out, want) {
		t.Fatalf("missing %s in\n%s", want, out)
	}
	if strings.Index(out, want) > strings.Index(out, "</head>") {
		t.Errorf("the generator belongs in <head>:\n%s", out)
	}
	if strings.Count(withGenerator(out), `name="generator"`) != 1 {
		t.Error("a page that already has the engine's generator must not get a second")
	}
	// control: the author's own generator stays, in any of the ways an attribute is written
	for _, own := range []string{`<meta name="generator" content="Hugo">`, `<meta name=generator content=Hugo>`, `<META NAME='Generator' CONTENT='Hugo'>`} {
		authored := strings.Replace(page, "<title>", own+"<title>", 1)
		if got := withGenerator(authored); got != authored {
			t.Errorf("an author's generator must be kept as written (%s):\n%s", own, got)
		}
	}
	fragment := `<p>no head here</p>`
	if withGenerator(fragment) != fragment {
		t.Error("a fragment with no <head> is left alone")
	}
}
