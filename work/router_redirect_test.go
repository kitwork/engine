package work

import (
	"net/http"
	"testing"
)

// ctx.redirect takes a status and must send it: a moved page says 301, a
// form's post-redirect says 303. The status is the only thing a crawler or a
// cache reads off a redirect, so a silently substituted one is a wrong answer.
func TestRedirectHonoursTheStatusItIsGiven(t *testing.T) {
	serve := scaffold(t, map[string]string{
		"filesystem.kitwork": "",
		"index.kitwork.html": shorthandShell,
		"page.kitwork.html":  `<main>home</main>`,
		"router.kitwork.js":  `import { router } from "kitwork";`,
		"moved/router.kitwork.js": `import { router } from "kitwork";
router.get((ctx) => ctx.redirect("https://kitwork.io/components", 301));`,
		"gone/router.kitwork.js": `import { router } from "kitwork";
router.get((ctx) => ctx.redirect("/elsewhere", 308));`,
		"plain/router.kitwork.js": `import { router } from "kitwork";
router.get((ctx) => ctx.redirect("/elsewhere"));`,
		"odd/router.kitwork.js": `import { router } from "kitwork";
router.get((ctx) => ctx.redirect("/elsewhere", 200));`,
	})
	for _, want := range []struct {
		path     string
		code     int
		location string
	}{
		{"/moved", http.StatusMovedPermanently, "https://kitwork.io/components"},
		{"/gone", http.StatusPermanentRedirect, "/elsewhere"},
		{"/plain", http.StatusSeeOther, "/elsewhere"},
		{"/odd", http.StatusSeeOther, "/elsewhere"},
	} {
		rec := serve(want.path)
		if rec.Code != want.code || rec.Header().Get("Location") != want.location {
			t.Errorf("%s: got %d → %q, want %d → %q", want.path, rec.Code, rec.Header().Get("Location"), want.code, want.location)
		}
	}
}
