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

func themePrepaintBodyForTest(source string) (string, bool) {
	const open = `<script data-kitwork-jit="theme">`
	start := strings.Index(source, open)
	if start < 0 || strings.Count(source, open) != 1 {
		return "", false
	}
	start += len(open)
	end := strings.Index(source[start:], `</script>`)
	if end < 0 {
		return "", false
	}
	return source[start : start+end], true
}
