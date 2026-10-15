package render

import (
	"strings"
	"testing"

	jittheme "github.com/kitwork/engine/jit/theme"
	"github.com/kitwork/engine/value"
)

func TestPreparePreservesCanonicalThemePrepaintBody(t *testing.T) {
	root := t.TempDir()
	mkfile(t, root, "index.kitwork.html", `<html><head><meta charset="utf-8"></head><body>{{ @page }}</body></html>`)
	mkfile(t, root, "page.kitwork.html", `<main>Theme prepaint</main>`)

	prepared := New(Config{
		Base:          root,
		Directory:     ".",
		DefaultMinify: true,
		ThemeMode:     "force",
	}).Prepare()
	if err := prepared.PreparationError(); err != nil {
		t.Fatal(err)
	}
	output := prepared.Bind(value.New(map[string]any{})).String()
	want, ok := themePrepaintBodyForTest(jittheme.Force(`<html><head></head><body></body></html>`))
	if !ok {
		t.Fatal("canonical theme renderer omitted its marked prepaint")
	}
	got, ok := themePrepaintBodyForTest(output)
	if !ok {
		t.Fatalf("prepared render omitted its marked prepaint: %s", output)
	}
	if got != want {
		t.Fatalf("presentation minification changed the canonical theme prepaint\n got: %q\nwant: %q", got, want)
	}
}

// themePrepaintBodyForTest returns the engine's marked pre-paint, its opening tag included, so the
// comparison covers the marker as well as the body. The marker is whichever one the canonical script
// carries (appearance, or theme before it), and there must be exactly one marked script.
func themePrepaintBodyForTest(source string) (string, bool) {
	found := ""
	for _, open := range []string{`<script data-kitwork-generated="appearance">`, `<script data-kitwork-generated="theme">`} {
		switch strings.Count(source, open) {
		case 0:
			continue
		case 1:
			if found != "" {
				return "", false
			}
			found = open
		default:
			return "", false
		}
	}
	if found == "" {
		return "", false
	}
	start := strings.Index(source, found)
	end := strings.Index(source[start:], `</script>`)
	if end < 0 {
		return "", false
	}
	return source[start : start+end], true
}
