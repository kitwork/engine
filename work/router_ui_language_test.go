package work

import (
	"net/http"
	"strings"
	"testing"
)

// router.language reaches the .ui compiler (decision 13/10/2026): a site whose root says "ar" is
// written right to left, so the document of its page carries dir="rtl" beside lang="ar" (the compile's
// language, not the request's), and the left and right it writes mirror there (19/10/2026: the
// stylesheet writes them as logical CSS; TestBrowserRightToLeftSiteRunsRightToLeft measures them). The
// page renders 200 as written and nothing is said of left and right. The nearest folder that says a
// language decides: a branch that says "vi" is written left to right, its document with no dir.
// CONTROL: a root that says "vi" (or nothing) writes no dir either and hears nothing.
func TestRouterLanguageReachesTheCompiler(t *testing.T) {
	sides := `<content><box padding-left="4"><text>x</text></box></content>`
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
	if lines := warningLines(printed); len(lines) != 0 {
		t.Errorf("left and right mirror on a right-to-left page: nothing is said of them, got %q", lines)
	}
	if !strings.Contains(home, `<html lang="ar" dir="rtl">`) || !strings.Contains(home, `class="flex-column padding-left-4"`) {
		t.Errorf("the page renders as written, in its language, right to left:\n%s\n%s", linesWith(home, "<html"), linesWith(home, "padding-left"))
	}
	if !strings.Contains(branch, `<html lang="vi">`) || !strings.Contains(branch, `class="flex-column padding-left-4"`) {
		t.Errorf("the branch renders as written, in vi, left to right:\n%s\n%s", linesWith(branch, "<html"), linesWith(branch, "padding-left"))
	}

	// CONTROL: a root written left to right
	for _, root := range []string{sitePalette + "\nrouter.language(\"vi\");", sitePalette} {
		files = withRootRouter(t, root, map[string]string{"localhost/content.ui": sides})
		var page string
		printed = captureBootOutput(t, func() {
			page = serveUI(t, buildUISite(t, files), "http://localhost/").Body.String()
		})
		if lines := warningLines(printed); len(lines) != 0 {
			t.Errorf("CONTROL %q: a site written left to right hears nothing of left and right, got %q", root, lines)
		}
		if !strings.Contains(page, `<html lang="vi">`) {
			t.Errorf("CONTROL %q: a site written left to right has no dir:\n%s", root, linesWith(page, "<html"))
		}
	}
}

// start and end name no side any more (Quốc, 19/10/2026): left and right replaced them and mirror on a
// page written right to left, so each side and corner a right-to-left site wrote with them before
// (padding-start, padding-end, radius-top-end, radius-bottom-start) is a compile error at the attribute
// naming the attribute that replaced it: while developing the page answers 500 with that line alone.
// CONTROL: the attribute named, written instead on the same site, renders 200 with nothing said.
func TestUISiteStartAndEndSidesAreErrors(t *testing.T) {
	for _, run := range []struct{ old, renamed, told string }{
		{`padding-start="4"`, `padding-left="4"`, `content.ui:1:15: padding-start nay là padding-left: start và end nay là left và right (tự lật khi trang viết từ phải sang trái)`},
		{`padding-end="3"`, `padding-right="3"`, `content.ui:1:15: padding-end nay là padding-right: start và end nay là left và right (tự lật khi trang viết từ phải sang trái)`},
		{`radius-top-end="large"`, `radius-top-right="large"`, `content.ui:1:15: radius-top-end nay là radius-top-right: start và end nay là left và right (tự lật khi trang viết từ phải sang trái)`},
		{`radius-bottom-start="large"`, `radius-bottom-left="large"`, `content.ui:1:15: radius-bottom-start nay là radius-bottom-left: start và end nay là left và right (tự lật khi trang viết từ phải sang trái)`},
	} {
		for _, written := range []string{run.old, run.renamed} {
			files := withRootRouter(t, sitePalette+"\nrouter.language(\"ar\");", map[string]string{
				"localhost/content.ui": `<content><box ` + written + `><text>x</text></box></content>`,
			})
			var rec *http.Response
			var body string
			printed := captureBootOutput(t, func() {
				recorded := serveUI(t, buildUISite(t, files), "http://localhost/")
				rec, body = recorded.Result(), recorded.Body.String()
			})
			if lines := warningLines(printed); len(lines) != 0 {
				t.Errorf("%s: nothing is said of left and right, got %q", written, lines)
			}
			if written == run.old {
				if rec.StatusCode != http.StatusInternalServerError || body != run.told {
					t.Errorf("%s:\n got  %d %s\n want 500 %s", written, rec.StatusCode, body, run.told)
				}
				continue
			}
			// CONTROL: the attribute the error names
			if rec.StatusCode != http.StatusOK {
				t.Errorf("CONTROL %s: %d, want the page as written:\n%s", written, rec.StatusCode, body)
			}
		}
	}
}
