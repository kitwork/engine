package work

import (
	"net/http"
	"testing"
)

// context.redirect sends the status written before it: a moved page says 301, a form's
// post-redirect says 303, and with no .status() it is 302 (KITWORK_ROUTER §6). The status is the only
// thing a crawler or a cache reads off a redirect, so a silently substituted one is a wrong answer.
// (A status that is not 3xx is an error: TestUIRedirectStatus. Until 27/10/2026 this tested a template
// site, where the status was redirect's second argument and the default 303.)
func TestRedirectHonoursTheStatusItIsGiven(t *testing.T) {
	serve := scaffold(t, map[string]string{
		"router.work": `import * as router from "kitwork/router";`,
		"moved/router.work": `import * as router from "kitwork/router";
router.get((context) => context.status(301).redirect("https://kitwork.io/components"));`,
		"gone/router.work": `import * as router from "kitwork/router";
router.get((context) => context.status(308).redirect("/elsewhere"));`,
		"posted/router.work": `import * as router from "kitwork/router";
router.get((context) => context.status(303).redirect("/elsewhere"));`,
		"plain/router.work": `import * as router from "kitwork/router";
router.get((context) => context.redirect("/elsewhere"));`,
	})
	for _, want := range []struct {
		path     string
		code     int
		location string
	}{
		{"/moved", http.StatusMovedPermanently, "https://kitwork.io/components"},
		{"/gone", http.StatusPermanentRedirect, "/elsewhere"},
		{"/posted", http.StatusSeeOther, "/elsewhere"},
		{"/plain", http.StatusFound, "/elsewhere"},
	} {
		rec := serve(want.path)
		if rec.Code != want.code || rec.Header().Get("Location") != want.location {
			t.Errorf("%s: got %d → %q, want %d → %q", want.path, rec.Code, rec.Header().Get("Location"), want.code, want.location)
		}
	}
}
