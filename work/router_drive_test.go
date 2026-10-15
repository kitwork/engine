package work

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// driveFile is Drive's own file on a page, under either prefix the engine has served it from
// (/kit/drive.<hash>.js, /_kitwork/drive.<hash>.js); production drops the quotes around src.
var driveFile = regexp.MustCompile(`/(?:kit|_kitwork)/drive\.[0-9a-f]+\.js`)

// templateHome boots a template site (router.kitwork.js) whose root router is the given lines and
// returns its home page, what the boot printed and whether the generation it activated has Drive on.
func templateHome(t *testing.T, router, page string) (body, printed string, drive bool) {
	t.Helper()
	tenant, _ := writeTestSite(t, router, page)
	var err error
	printed = captureBootOutput(t, func() { err = tenant.Run() })
	if err != nil {
		t.Fatalf("%s: %v", router, err)
	}
	recorder := httptest.NewRecorder()
	tenant.Serve(recorder, httptest.NewRequest(http.MethodGet, "http://localhost/", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("%s: GET / = %d", router, recorder.Code)
	}
	return recorder.Body.String(), printed, tenant.SiteGeneration().Presentation().Snapshot().Drive
}

// router.reload is the one switch for moving between pages, on a template site as on a Kitwork UI
// site: reload(false) turns Drive on (its file on every page, once, and no flag written for it),
// reload(true) loads every page whole, and the last word in the file is the one meant. Without it a
// template site reloads, as it always has.
func TestReloadFalseTurnsDriveOnForATemplateSite(t *testing.T) {
	const page = `<main><a href="/next">next</a><b data-kit-text="'x'">x</b></main>`
	for _, test := range []struct {
		router string
		drive  bool
	}{
		{``, false}, // control: the default of a template site is a whole reload
		{`router.reload(false);`, true},
		{`router.reload(true);`, false},
		{`router.reload(true);` + "\n" + `router.reload(false);`, true},
		{`router.reload(false);` + "\n" + `router.reload(true);`, false},
		{`router.title("Quán").reload(false).description("chained");`, true}, // reload returns the router
	} {
		body, _, drive := templateHome(t, test.router, page)
		if drive != test.drive {
			t.Errorf("%q: the site's Drive is %t, want %t", test.router, drive, test.drive)
		}
		files := len(driveFile.FindAllString(body, -1))
		if test.drive && files != 1 {
			t.Errorf("%q: Drive's file should be on the page once, found %d:\n%s", test.router, files, body)
		}
		if !test.drive && files != 0 {
			t.Errorf("%q: a site that reloads must not get Drive's file:\n%s", test.router, body)
		}
		if strings.Contains(body, "data-kitwork-drive") {
			t.Errorf("%q: no flag is written for Drive; its file is the answer:\n%s", test.router, body)
		}
		if !strings.Contains(body, "kernel.") {
			t.Errorf("%q: the page uses the grammar, so it keeps the kernel either way:\n%s", test.router, body)
		}
	}
	// A root marker used to switch Drive on; it buys nothing.
	if body, _, drive := templateHome(t, ``, `<main data-kit-hydrate="v1"><b data-kit-text="'x'">x</b></main>`); drive || driveFile.MatchString(body) {
		t.Errorf("a marker must not turn Drive on:\n%s", body)
	}
}

// Each kind of site keeps the default it had: a Kitwork UI site (router.work) moves without a
// reload until it writes router.reload(true); a template site reloads until it writes
// router.reload(false).
func TestReloadDefaultFollowsTheKindOfSite(t *testing.T) {
	ui := buildUISite(t, uiSiteFiles)
	if !ui.SiteGeneration().Presentation().Snapshot().Drive {
		t.Error("a Kitwork UI site moves between pages without a reload by default")
	}
	if body := serveUI(t, ui, "http://localhost/tinh").Body.String(); !driveFile.MatchString(body) {
		t.Errorf("a Kitwork UI page should carry Drive by default:\n%s", body)
	}
	whole := buildUISite(t, withFiles(map[string]string{
		"localhost/router.work": strings.Replace(uiSiteFiles["localhost/router.work"], "router.meta(", "router.reload(true);\nrouter.meta(", 1),
	}))
	if whole.SiteGeneration().Presentation().Snapshot().Drive {
		t.Error("router.reload(true) on a Kitwork UI site loads every page whole")
	}
	if _, _, drive := templateHome(t, ``, `<main></main>`); drive {
		t.Error("a template site reloads by default")
	}
}

// router.drive() is gone: a router that calls it still boots and keeps its chain, but the call
// applies nothing (on/off and a wrong argument alike), and the boot says so ONCE for the file, naming
// what to write instead. A file that does not call it says nothing.
func TestRouterDriveIsGone(t *testing.T) {
	const said = "router.drive() is gone — write router.reload(false)"
	const page = `<main><a href="/next">next</a><b data-kit-text="'x'">x</b></main>`
	line := "[router] router.kitwork.js: " + said + "\n"

	for _, router := range []string{
		`router.drive();`,
		`router.drive(true);`,
		`router.drive("yes");`, // refused before; now nothing at all
		`router.drive(); router.drive(true); router.drive(false);`,
		`router.title("Quán").drive().description("chained");`,
	} {
		body, printed, drive := templateHome(t, router, page)
		if drive || driveFile.MatchString(body) {
			t.Errorf("%q must apply nothing, yet the site has Drive:\n%s", router, body)
		}
		if got := strings.Count(printed, line); got != 1 {
			t.Errorf("%q: want the line %q once at boot, got %d:\n%s", router, strings.TrimSpace(line), got, printed)
		}
		if got := strings.Count(printed, "router.drive()"); got != 1 {
			t.Errorf("%q: router.drive() is named once, got %d:\n%s", router, got, printed)
		}
		if strings.Contains(printed, "not a method") {
			t.Errorf("%q: the engine knows router.drive(); it must not be reported as an unknown method:\n%s", router, printed)
		}
	}
	// the chain survives: what comes after .drive() still lands
	tenant, _ := writeTestSite(t, `router.drive().description("after drive");`, page)
	captureBootOutput(t, func() {
		if err := tenant.Run(); err != nil {
			t.Fatal(err)
		}
	})
	if got := tenant.routeTree().root.ensureFolder(tenant).meta["description"].Text(); got != "after drive" {
		t.Errorf("a declaration after .drive() was lost: %q", got)
	}

	// control: the same site written with router.reload(false) has Drive and says nothing
	body, printed, drive := templateHome(t, `router.reload(false);`, page)
	if !drive || !driveFile.MatchString(body) || strings.Contains(printed, "router.drive()") {
		t.Errorf("control: router.reload(false) turns Drive on with no boot line (Drive %t):\n%s", drive, printed)
	}

	// a branch's router names its own file
	tenant, site := writeTestSite(t, ``, page)
	writeSiteFile(t, site, "docs/router.kitwork.js", `import { router } from "kitwork";`+"\nrouter.drive();\nrouter.drive();")
	writeSiteFile(t, site, "docs/page.kitwork.html", `<main>docs</main>`)
	printed = captureBootOutput(t, func() {
		if err := tenant.Run(); err != nil {
			t.Fatal(err)
		}
	})
	branch := "[router] " + filepath.Join("docs", "router.kitwork.js") + ": " + said + "\n"
	if got := strings.Count(printed, branch); got != 1 {
		t.Errorf("the branch file calls router.drive() twice: want %q once, got %d:\n%s", strings.TrimSpace(branch), got, printed)
	}
	if strings.Contains(printed, line) {
		t.Errorf("the root router does not call router.drive(), so it gets no line:\n%s", printed)
	}
}

// A misspelt argument to router.reload is named at boot rather than shrugged off.
func TestReloadRejectsANonBoolean(t *testing.T) {
	for _, call := range []string{`router.reload("yes");`, `router.reload();`, `router.reload(false, true);`} {
		tenant, _ := writeTestSite(t, call, `<main></main>`)
		var err error
		captureBootOutput(t, func() { err = tenant.Run() })
		if err == nil || !strings.Contains(err.Error(), "router.reload expects true or false") {
			t.Errorf("%s: want a boot error naming true or false, got %v", call, err)
		}
	}
	for _, call := range []string{`router.reload(true);`, `router.reload(false);`} {
		tenant, _ := writeTestSite(t, call, `<main></main>`)
		var err error
		captureBootOutput(t, func() { err = tenant.Run() })
		if err != nil {
			t.Errorf("control: %s must boot: %v", call, err)
		}
	}
}

// The bar between pages is the progressbar component on every page of a Drive site, template pages
// included: the engine puts it first in <body>, delivers its file and styles its classes. A site
// without Drive gets none.
func TestDriveSitePagesGetTheProgressbar(t *testing.T) {
	serve := func(router string) string {
		t.Helper()
		body, _, _ := templateHome(t, router, `<main><a href="/next">next</a></main>`)
		return body
	}
	out := serve(`router.reload(false);`)
	flat := strings.ReplaceAll(strings.ReplaceAll(out, `"`, ""), " ", "")
	// the CSS rule itself, not the class name (which also spells --kitwork-progress)
	for _, want := range []string{"data-kitwork-component=progressbar", "/components/progressbar", "{background-color:var(--kitwork-progress,var(--kitwork-brand))"} {
		if !strings.Contains(flat, want) {
			t.Errorf("a Drive page is missing %s\n%.700s", want, out)
		}
	}
	if strings.Index(out, "progressbar") > strings.Index(out, "<main>") {
		t.Errorf("the progressbar goes before the page's content:\n%.700s", out)
	}
	// control: no Drive, no bar between pages
	if off := serve(""); strings.Contains(off, "progressbar") {
		t.Errorf("a site without Drive must not get a progressbar:\n%.700s", off)
	}
}
