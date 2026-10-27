package work

import (
	"strings"
	"testing"
)

// MINIFY=true (work.Minify) minifies what production minifies while developing (14/10/2026): the head
// of a .ui page comes out as production writes it, and nothing else that developing means changes: the
// page's warnings still reach the browser's console (warnings.<hash>.js). (A template page's HTML was
// minified the same way until the template pages went, 27/10/2026.)
//
// CONTROL: the same site while developing without MINIFY is written out.
func TestMinifyPreviewWhileDeveloping(t *testing.T) {
	for _, minify := range []bool{false, true} {
		saved := Minify
		Minify = minify
		t.Cleanup(func() { Minify = saved })

		// a .ui page with a warning (a pair of colours short of contrast)
		tenant := buildUISite(t, withRootRouter(t, sitePalette, map[string]string{"localhost/content.ui": lowContrastContent}))
		page := serveUI(t, tenant, "http://localhost/").Body.String()
		minified := strings.Contains(page, `<meta name=generator content=`)
		if minified != minify {
			t.Errorf("MINIFY %v: the .ui head is minified: %v\n%.900s", minify, minified, page)
		}
		if !strings.Contains(page, `data-kitwork-generated=warnings`) && !strings.Contains(page, `data-kitwork-generated="warnings"`) {
			t.Errorf("MINIFY %v: while developing the page's warnings still reach the console:\n%.900s", minify, page)
		}

	}
}
