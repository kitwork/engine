package work

import (
	"net/http"
	"strings"
	"testing"
)

// A Kitwork UI site with no notfound.ui on the way up answers a 404 with the engine's plain "Not Found",
// while developing as in production, for a path with no folder and for ctx.notFound() in a handler
// alike: a .ui site never reaches the template pages' renderer, which while developing wrote the path
// of an index.kitwork.html on the server as the page (until 27/10/2026). CONTROL: the same site with its
// notfound.ui draws it.
func TestUISiteWithoutNotFoundPageAnswersPlainNotFound(t *testing.T) {
	files := withFiles(nil)
	delete(files, "localhost/notfound.ui")
	for _, local := range []bool{true, false} {
		tenant := buildUISiteMode(t, files, local)
		for _, target := range []string{"http://localhost/mon/tra", "http://localhost/khong-co", "http://localhost/khong-co/sau"} {
			rec := serveUI(t, tenant, target)
			if rec.Code != http.StatusNotFound || rec.Body.String() != "Not Found" ||
				!strings.HasPrefix(rec.Header().Get("Content-Type"), "text/plain") {
				t.Errorf("developing=%v GET %s = %d %q (%s), want 404 Not Found as plain text",
					local, target, rec.Code, rec.Body.String(), rec.Header().Get("Content-Type"))
			}
			if strings.Contains(rec.Body.String(), "kitwork.html") {
				t.Errorf("developing=%v GET %s names a template file: %s", local, target, rec.Body.String())
			}
		}
	}

	control := serveUI(t, buildUISite(t, uiSiteFiles), "http://localhost/khong-co")
	if control.Code != http.StatusNotFound || !strings.Contains(control.Body.String(), "<p>Không thấy trang</p>") {
		t.Errorf("CONTROL: with its notfound.ui the site draws it at 404, got %d\n%s", control.Code, control.Body.String())
	}
}
