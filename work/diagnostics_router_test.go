package work

import (
	"net/http"
	"strings"
	"testing"
)

// router.fonts reads two roles, each a typeface's name, and a { light, dark } pair of router.colors two
// keys: anything else used to be dropped without a word ("Chữ gõ sai đang biến mất im lặng",
// docs/KITWORK_UI_DIAGNOSTICS.md), the typeface or the dark value never applied. Now the boot stops and
// says what was written. CONTROL: the words they read reach the CSS.
func TestRouterFontsAndColorsRefuseWhatTheyDoNotRead(t *testing.T) {
	for written, want := range map[string]string{
		`router.fonts({ bdy: "Inter" });`:                                  `router.fonts({ bdy: … }): không có vai trò "bdy"; router.fonts nhận body (chữ của trang) và code (mã và con số)`,
		`router.fonts({ heading: "Inter" });`:                              `router.fonts({ heading: … }): không có vai trò "heading"`,
		`router.fonts({ code: 1 });`:                                       `router.fonts({ code: … }): code là tên một kiểu chữ, viết trong dấu nháy: router.fonts({ code: "Outfit" })`,
		`router.colors({ brand: { light: "#c8742c", drak: "#000000" } });`: `router.colors({ brand: { drak: … } }): một cặp màu chỉ nhận light và dark, không có "drak"`,
	} {
		files := withRootRouter(t, sitePalette+"\n"+written, nil)
		if _, err := bootUISite(t, files, true); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: want the boot to stop saying %q, got %v", written, want, err)
		}
	}
	// CONTROL
	files := withRootRouter(t, `router.colors({ brand: { light: "#c8742c", dark: "#0a0b0c" } });`+"\n"+`router.fonts({ code: "Fira Code" });`, nil)
	tenant, err := bootUISite(t, files, true)
	if err != nil {
		t.Fatalf("the words router.colors and router.fonts read must boot, got %v", err)
	}
	body := pageWithStylesheet(t, tenant, "http://localhost/")
	for _, want := range []string{"#0a0b0c", "Fira Code"} {
		if !strings.Contains(body, want) {
			t.Errorf("CONTROL: the CSS carries %q", want)
		}
	}
}

// A router.view that fails answers the page with its error, the way a compile error does (respondUI):
// 500, and while developing what failed in the view; in production nothing of it. It used to fall
// through to the template path and answer 200 with "[Error reading index …]". CONTROL: the same view
// without the failing call serves the page.
func TestUIViewThatFailsAnswersItsError(t *testing.T) {
	files := withFiles(nil)
	files["localhost/router.work"] = strings.Replace(files["localhost/router.work"], `ctx.title("Thực đơn");`, `ctx.title("Thực đơn");
  twice();`, 1)
	rec := serveUI(t, buildUISite(t, files), "http://localhost/")
	if body := rec.Body.String(); rec.Code != http.StatusInternalServerError || !strings.Contains(body, "at twice (router.work:11:") || strings.Contains(body, "Error reading index") {
		t.Errorf("while developing: want 500 with what failed in the view, got %d:\n%s", rec.Code, body)
	}
	rec = serveUI(t, buildUISiteMode(t, files, false), "http://localhost/")
	if body := rec.Body.String(); rec.Code != http.StatusInternalServerError || body != "Internal Server Error\nRequest ID: "+rec.Header().Get("X-Request-ID")+"\n" {
		t.Errorf("in production: want 500 saying nothing of the view but its request id, got %d:\n%s", rec.Code, body)
	}
	// CONTROL
	if rec := serveUI(t, buildUISite(t, uiSiteFiles), "http://localhost/"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "<p>2 món, gấp đôi 4</p>") {
		t.Errorf("CONTROL: the view that runs serves the page, got %d", rec.Code)
	}
}
