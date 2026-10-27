package work

import (
	"regexp"
	"strings"
	"testing"
)

// driveFile is Drive's own file on a page, /_kitwork/drive.<hash>.js (/kit/drive.<hash>.js is
// answered by nothing since 26/10/2026); production drops the quotes around src.
var driveFile = regexp.MustCompile(`/_kitwork/drive\.[0-9a-f]+\.js`)

// reloadingSite is the shared site that loads every page whole: router.reload(true).
func reloadingSite() map[string]string {
	return withFiles(map[string]string{
		"localhost/router.work": strings.Replace(uiSiteFiles["localhost/router.work"], "router.meta(", "router.reload(true);\nrouter.meta(", 1),
	})
}

// A Kitwork UI site moves between pages without a reload until it writes router.reload(true). (A
// template site reloaded by default until the template pages went, 27/10/2026.)
func TestReloadDefaultIsDriveOnAKitworkUISite(t *testing.T) {
	ui := buildUISite(t, uiSiteFiles)
	if !ui.SiteGeneration().Presentation().Snapshot().Drive {
		t.Error("a Kitwork UI site moves between pages without a reload by default")
	}
	if body := serveUI(t, ui, "http://localhost/tinh").Body.String(); !driveFile.MatchString(body) {
		t.Errorf("a Kitwork UI page should carry Drive by default:\n%s", body)
	}
	whole := buildUISite(t, reloadingSite())
	if whole.SiteGeneration().Presentation().Snapshot().Drive {
		t.Error("router.reload(true) on a Kitwork UI site loads every page whole")
	}
	if body := serveUI(t, whole, "http://localhost/tinh").Body.String(); driveFile.MatchString(body) {
		t.Errorf("a site that reloads must not get Drive's file:\n%s", body)
	}
}

// A misspelt argument to router.reload is named at boot rather than shrugged off.
func TestReloadRejectsANonBoolean(t *testing.T) {
	boot := func(call string) error {
		var err error
		captureBootOutput(t, func() {
			var tenant *Tenant
			tenant, err = bootUISite(t, map[string]string{
				"localhost/router.work": "import * as router from \"kitwork/router\";\n" + call + "\n",
				"localhost/view.ui":     `<view><content /></view>`,
				"localhost/content.ui":  `<content><text>x</text></content>`,
			}, true)
			t.Cleanup(tenant.Close)
		})
		return err
	}
	for _, call := range []string{`router.reload("yes");`, `router.reload();`, `router.reload(false, true);`} {
		if err := boot(call); err == nil || !strings.Contains(err.Error(), "router.reload expects true or false") {
			t.Errorf("%s: want a boot error naming true or false, got %v", call, err)
		}
	}
	for _, call := range []string{`router.reload(true);`, `router.reload(false);`} {
		if err := boot(call); err != nil {
			t.Errorf("control: %s must boot: %v", call, err)
		}
	}
}

// The bar between pages is the progressbar component on every page of a Drive site: the engine puts
// it first in <body> and delivers its file, and the site's stylesheet styles it. A site that loads
// every page whole (router.reload(true)) gets none. (Template pages got it too until 27/10/2026.)
func TestDriveSitePagesGetTheProgressbar(t *testing.T) {
	const bar = `data-kitwork-component="progressbar"`
	tenant := buildUISite(t, uiSiteFiles)
	out := serveUI(t, tenant, "http://localhost/tinh").Body.String()
	for _, want := range []string{bar, "/_kitwork/components/progressbar."} {
		if !strings.Contains(out, want) {
			t.Errorf("a Drive page is missing %s\n%.700s", want, out)
		}
	}
	if body := strings.Index(out, "<body"); body < 0 || strings.Index(out[body:], bar) > strings.Index(out[body:], "Trang tĩnh") {
		t.Errorf("the progressbar goes first in <body>, before the page's content:\n%.700s", out)
	}
	if !strings.Contains(pageWithStylesheet(t, tenant, "http://localhost/tinh"), ":where(.progressbar){position:fixed") {
		t.Error("the site's stylesheet does not style the progressbar")
	}
	// control: no Drive, no bar between pages
	if off := serveUI(t, buildUISite(t, reloadingSite()), "http://localhost/tinh").Body.String(); strings.Contains(off, bar) || strings.Contains(off, "/components/progressbar") {
		t.Errorf("a site without Drive must not get a progressbar:\n%.700s", off)
	}
}
