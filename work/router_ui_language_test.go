package work

import (
	"net/http"
	"strings"
	"testing"
)

// router.language reaches the .ui compiler (decision 13/10/2026): a site whose root says "ar" is
// written right to left, so a left or right attribute is a warning while developing, the terminal once
// a compile, at the attribute, naming the language; the page renders 200 as written. The nearest folder
// that says a language decides: a branch that says "vi" is written left to right. CONTROL: a root that
// says "vi" (or nothing) hears nothing of left and right.
func TestRouterLanguageReachesTheCompiler(t *testing.T) {
	sides := `<content><box padding-left="4"><text>x</text></box></content>`
	told := `cảnh báo: content.ui:1:15: padding-left="4": trang viết từ phải sang trái (router.language "ar") mà left và right không lật theo hướng viết`
	files := withRootRouter(t, sitePalette+"\nrouter.language(\"ar\");", map[string]string{
		"localhost/content.ui":      sides,
		"localhost/tinh/content.ui": sides,
		"localhost/tinh/router.work": `import { router } from "kitwork";
router.language("vi");`,
	})
	var home, branch string
	printed := captureBootOutput(t, func() {
		tenant := buildUISite(t, files)
		rec := serveUI(t, tenant, "http://localhost/")
		if rec.Code != http.StatusOK {
			t.Fatalf("GET / = %d, want the page as written:\n%s", rec.Code, rec.Body.String())
		}
		home = rec.Body.String()
		branch = serveUI(t, tenant, "http://localhost/tinh").Body.String()
	})
	lines := warningLines(printed)
	if len(lines) != 1 || !strings.HasPrefix(lines[0], told) {
		t.Errorf("the right-to-left root warns once at the attribute:\n got  %q\n want %s…", lines, told)
	}
	if !strings.Contains(home, `<html lang="ar"`) || !strings.Contains(home, `class="flex-column padding-left-4"`) {
		t.Errorf("the page renders as written, in its language:\n%s", linesWith(home, "padding-left"))
	}
	if !strings.Contains(branch, `<html lang="vi"`) || !strings.Contains(branch, `class="flex-column padding-left-4"`) {
		t.Errorf("the branch renders as written, in vi:\n%s", linesWith(branch, "padding-left"))
	}

	// CONTROL: a root written left to right
	for _, root := range []string{sitePalette + "\nrouter.language(\"vi\");", sitePalette} {
		files = withRootRouter(t, root, map[string]string{"localhost/content.ui": sides})
		printed = captureBootOutput(t, func() {
			serveUI(t, buildUISite(t, files), "http://localhost/")
		})
		if lines := warningLines(printed); len(lines) != 0 {
			t.Errorf("CONTROL %q: a site written left to right hears nothing of left and right, got %q", root, lines)
		}
	}
}
