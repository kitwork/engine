package icons

import (
	"strings"
	"testing"
)

// The same gap, for icons: an icon named inside a template branch had no mask CSS at all, so the
// element rendered as an empty box on whichever branch the page took.
func TestIconsInsideATemplateBranchAreEmitted(t *testing.T) {
	out := Render(`<head></head><body><i class="{{ if dark }}icon-moon{{ else }}icon-sun{{ end }}"></i></body>`)
	style := out
	if i := strings.Index(out, "<style"); i >= 0 {
		if j := strings.Index(out[i:], "</style>"); j >= 0 {
			style = out[i : i+j]
		}
	}
	for _, want := range []string{".icon-moon", ".icon-sun"} {
		if !strings.Contains(style, want) {
			t.Errorf("missing %q — both branches must ship their mask:\n%s", want, style)
		}
	}

	// CONTROL: without the template both are emitted, so a failure above is about the delimiters.
	plain := Render(`<head></head><body><i class="icon-moon"></i><i class="icon-sun"></i></body>`)
	if !strings.Contains(plain, ".icon-moon") || !strings.Contains(plain, ".icon-sun") {
		t.Fatal("control lost an icon — the failure is not about templates at all")
	}
}
