package fonts

import (
	"strings"
	"testing"
)

func TestHasAndNames(t *testing.T) {
	for _, slug := range []string{"outfit", "fira-code"} {
		if !Has(slug) {
			t.Errorf("expected vendored family %q", slug)
		}
	}
	if Has("definitely-not-a-font") {
		t.Error("unknown family reported present")
	}
	if len(Names()) < 2 {
		t.Errorf("expected the vendored families, got %d", len(Names()))
	}
}

// Every face in the catalog must point at a real embedded woff2.
func TestEmbeddedFilesExist(t *testing.T) {
	for slug, fam := range catalog {
		if len(fam.faces) == 0 {
			t.Errorf("family %q has no faces", slug)
		}
		for _, f := range fam.faces {
			if _, err := FS.ReadFile(f.file); err != nil {
				t.Errorf("missing embedded woff2 for %q: %s (%v)", slug, f.file, err)
			}
		}
	}
}

// A plain `font-family: Outfit` (no class) must trigger @font-face + preload, markup untouched.
func TestRenderDetectsByFontFamily(t *testing.T) {
	in := `<html><head><style>body{font-family: Outfit, sans-serif}</style></head><body>x</body></html>`
	out := Render(in)
	if strings.Count(out, `data-kitwork-jit="fonts"`) != 1 {
		t.Errorf("expected exactly one fonts style block: %s", out)
	}
	if !strings.Contains(out, "@font-face{font-family:'Outfit'") {
		t.Errorf("expected an Outfit @font-face: %s", out)
	}
	if !strings.Contains(out, "src:url("+RoutePrefix+"outfit/") || !strings.Contains(out, "format('woff2')") {
		t.Errorf("@font-face should point at the local /jitfonts woff2: %s", out)
	}
	if !strings.Contains(out, `<link rel="preload" as="font" type="font/woff2" href="`+RoutePrefix+`outfit/`) {
		t.Errorf("expected a preload link for Outfit: %s", out)
	}
	if !strings.Contains(out, "font-display:swap") {
		t.Errorf("expected font-display:swap: %s", out)
	}
	// No class form was used → no `.font-outfit{}` utility, only the @font-face.
	if strings.Contains(out, ".font-outfit{") {
		t.Errorf("utility class should not be emitted when only font-family is used: %s", out)
	}
	si := strings.Index(out, "<style "+styleMarker+">")
	if hi := strings.Index(out, "</head>"); si < 0 || si > hi {
		t.Errorf("fonts style should be injected before </head>: %s", out)
	}
}

// The `font-<slug>` class form additionally emits the `.font-<slug>` utility.
func TestRenderClassEmitsUtility(t *testing.T) {
	out := Render(`<head></head><body><code class="font-fira-code">x</code></body>`)
	if !strings.Contains(out, "@font-face{font-family:'Fira Code'") {
		t.Errorf("expected a Fira Code @font-face: %s", out)
	}
	if !strings.Contains(out, ".font-fira-code{font-family:'Fira Code',") {
		t.Errorf("expected the .font-fira-code utility with fallback: %s", out)
	}
}

func TestRenderNoOpWithoutKnownFont(t *testing.T) {
	in := `<head></head><body><p class="font-bold">no vendored font here</p></body>`
	if out := Render(in); out != in {
		t.Errorf("expected unchanged output (font-bold is not a family): %s", out)
	}
	none := `<head></head><body><p>plain</p></body>`
	if out := Render(none); out != none {
		t.Errorf("expected unchanged output for a page with no fonts: %s", out)
	}
}
