package work

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

var servedScript = regexp.MustCompile(`src="?(/kit/[^"\s>]+)`)

func homeOf(t *testing.T, tenant *Tenant) string {
	t.Helper()
	recorder := httptest.NewRecorder()
	tenant.Serve(recorder, httptest.NewRequest(http.MethodGet, "http://localhost/", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET / = %d", recorder.Code)
	}
	return recorder.Body.String()
}

// router.javascript() configures the kernel's delivery: drive and component pins are honoured.
func TestJavascriptOptionsShapeTheKernelsFiles(t *testing.T) {
	page := `<main><div data-kit-component="clipboard"></div><b data-kit-text="'x'">x</b></main>`
	tenant, _ := writeTestSite(t, `router.javascript({ drive: true, components: { clipboard: "1.0.0" } });`, page)
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	snapshot := tenant.SiteGeneration().Presentation().Snapshot()
	if !snapshot.Drive || snapshot.ComponentVersions["clipboard"] != "1.0.0" {
		t.Fatalf("snapshot: Drive=%t versions=%v", snapshot.Drive, snapshot.ComponentVersions)
	}
	out := homeOf(t, tenant)
	for _, want := range []string{"data-kitwork-drive", "/kit/kernel.", "/kit/drive.", "/kit/components/clipboard@1.0.0."} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in:\n%s", want, out)
		}
	}

	// CONTROL: the same page with nothing said gets the latest clipboard and no Drive.
	plain, _ := writeTestSite(t, ``, page)
	if err := plain.Run(); err != nil {
		t.Fatal(err)
	}
	out = homeOf(t, plain)
	if !strings.Contains(out, "/kit/components/clipboard@2.0.0.") || strings.Contains(out, "/kit/drive.") {
		t.Errorf("defaults changed:\n%s", out)
	}
}

// Every mistake is named at boot, with what would have been right.
func TestJavascriptRefusesWhatItCannotMean(t *testing.T) {
	for _, test := range []struct{ call, says string }{
		{`router.javascript(false);`, "nothing to switch off"},
		{`router.javascript({ components: { clipboard: "9.0.0" } });`, "the catalogue has 1.0.0, 2.0.0"},
		{`router.javascript({ components: { nonesuch: "1.0.0" } });`, "no catalogue component by that name"},
		{`router.javascript({ components: { counter: { version: "1.0.0", source: "./c.js" } } });`, "_components/"},
		{`router.javascript({ drive: "yes" });`, "drive must be true or false"},
		{`router.javascript({ minify: true });`, `no option "minify"`},
	} {
		tenant, _ := writeTestSite(t, test.call, `<main></main>`)
		err := tenant.Run()
		if err == nil || !strings.Contains(err.Error(), test.says) {
			t.Errorf("%s: want an error saying %q, got %v", test.call, test.says, err)
		}
	}
	// CONTROL: the accepted shapes boot.
	for _, call := range []string{`router.javascript();`, `router.javascript(true);`, `router.javascript({ components: { dialog: "v2.0.0" } });`} {
		tenant, _ := writeTestSite(t, call, `<main></main>`)
		if err := tenant.Run(); err != nil {
			t.Errorf("%s should boot: %v", call, err)
		}
	}
}

// Each file the page names is served, cached for good; an old hash still gets today's file, not kept.
func TestKitFilesAreServedImmutable(t *testing.T) {
	files := componentPage(`<div data-kit-component="bid-stepper"></div><div data-kit-component="menu"></div>`)
	files["_components/bid-stepper.js"] = sharedComponentSource
	tenant := componentFixture(t, files)
	out := homeOf(t, tenant)
	paths := servedScript.FindAllStringSubmatch(out, -1)
	if len(paths) != 3 {
		t.Fatalf("want kernel, bid-stepper and menu, got %v in:\n%s", paths, out)
	}
	for _, m := range paths {
		recorder := httptest.NewRecorder()
		tenant.Serve(recorder, httptest.NewRequest(http.MethodGet, "http://localhost"+m[1], nil))
		if recorder.Code != http.StatusOK || !strings.Contains(recorder.Header().Get("Cache-Control"), "immutable") || recorder.Body.Len() == 0 {
			t.Errorf("%s: status %d, cache %q, %d bytes", m[1], recorder.Code, recorder.Header().Get("Cache-Control"), recorder.Body.Len())
		}
		again := httptest.NewRequest(http.MethodGet, "http://localhost"+m[1], nil)
		again.Header.Set("If-None-Match", recorder.Header().Get("ETag"))
		revalidated := httptest.NewRecorder()
		tenant.Serve(revalidated, again)
		if revalidated.Code != http.StatusNotModified {
			t.Errorf("%s: a matching ETag is a 304, got %d", m[1], revalidated.Code)
		}
	}
	// The site's file belongs to this site.
	site := ""
	for _, m := range paths {
		if strings.Contains(m[1], "bid-stepper") {
			site = m[1]
		}
	}
	other := componentFixture(t, componentPage(`<p>x</p>`))
	recorder := httptest.NewRecorder()
	other.Serve(recorder, httptest.NewRequest(http.MethodGet, "http://localhost"+site, nil))
	if recorder.Code != http.StatusNotFound {
		t.Errorf("another site answered %s with %d", site, recorder.Code)
	}
	// An old hash — HTML cached before a deploy — keeps its JavaScript, but the file is not kept
	// under a name that is not its own.
	wrong := paths[0][1][:strings.LastIndex(paths[0][1], ".")-16] + "0000000000000000.js"
	recorder = httptest.NewRecorder()
	tenant.Serve(recorder, httptest.NewRequest(http.MethodGet, "http://localhost"+wrong, nil))
	if recorder.Code != http.StatusOK || recorder.Header().Get("Cache-Control") != "no-cache" || recorder.Body.Len() == 0 {
		t.Errorf("%s: want today's file, no-cache; got %d %q", wrong, recorder.Code, recorder.Header().Get("Cache-Control"))
	}
	// A name that means nothing is a 404.
	recorder = httptest.NewRecorder()
	tenant.Serve(recorder, httptest.NewRequest(http.MethodGet, "http://localhost/kit/components/nonesuch.0000000000000000.js", nil))
	if recorder.Code != http.StatusNotFound {
		t.Errorf("an unknown component: want 404, got %d", recorder.Code)
	}
}
