package work

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	jitjs "github.com/kitwork/engine/jit/js"
	"github.com/kitwork/engine/render"
	"github.com/kitwork/engine/site"
)

// servedScript is a script file the engine delivers, under either prefix it has served from.
var servedScript = regexp.MustCompile(`src="?(/(?:kit|_kitwork)/[^"\s>]+)`)

func homeOf(t *testing.T, tenant *Tenant) string {
	t.Helper()
	recorder := httptest.NewRecorder()
	tenant.Serve(recorder, httptest.NewRequest(http.MethodGet, "http://localhost/", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET / = %d", recorder.Code)
	}
	return recorder.Body.String()
}

// router.javascript() and its old name router.jitjs() are gone with all they took — drive, prerender
// and the component version pins. A router that still calls them boots, and the call applies
// NOTHING: the page is byte for byte the page of the same site that never called them (no Drive, no
// pre-rendered answer, the latest clipboard rather than the pinned one). The boot says so once for the
// file, naming what replaced them.
func TestRouterJavascriptIsGone(t *testing.T) {
	const said = "router.javascript() is gone — moving between pages is router.reload(false), components are found by name"
	const page = `<main><div data-kit-component="clipboard"></div><input data-kit-model="qty" value="3"><b data-kit-text="qty * 2">pending</b></main>`
	line := "[router] router.kitwork.js: " + said + "\n"

	plain, printed, drive := templateHome(t, ``, page)
	if drive || strings.Contains(printed, "router.javascript()") {
		t.Fatalf("control: a site that calls nothing has no Drive and no boot line (Drive %t):\n%s", drive, printed)
	}
	if !strings.Contains(plain, ">pending<") || strings.Contains(plain, ">6<") {
		t.Fatalf("control: the server sends the author's content, it does not pre-render:\n%s", plain)
	}

	for _, router := range []string{
		`router.javascript({ drive: true, prerender: true, components: { clipboard: "1.0.0" } });`,
		`router.jitjs({ drive: true, prerender: true, components: { clipboard: "1.0.0" } });`,
		`router.javascript();`,
		`router.javascript(true);`,
		`router.javascript(false);`,            // refused before; now nothing at all
		`router.javascript({ minify: true });`, // refused before: an option it never had
		`router.jitjs(true);`,                  // the oldest spelling
		`router.javascript({ drive: true }); router.jitjs({ prerender: true }); router.javascript();`, // one file, one line
		`router.title("Quán").javascript({ drive: true }).description("chained");`,
	} {
		body, printed, drive := templateHome(t, router, page)
		if drive {
			t.Errorf("%q must not turn Drive on", router)
		}
		if body != plain {
			t.Errorf("%q must apply nothing: its page differs from the page of a site that never called it\n got: %s\nwant: %s", router, body, plain)
		}
		if got := strings.Count(printed, line); got != 1 {
			t.Errorf("%q: want the line %q once at boot, got %d:\n%s", router, strings.TrimSpace(line), got, printed)
		}
		if strings.Contains(printed, "router.jitjs()") || strings.Contains(printed, "not a method") {
			t.Errorf("%q: router.jitjs() shares router.javascript()'s line, and neither is an unknown method:\n%s", router, printed)
		}
	}

	// control: the page comparison above can see what a pin or Drive would change
	if driven, _, _ := templateHome(t, `router.reload(false);`, page); driven == plain {
		t.Error("control: a site with Drive must serve a different page, or the comparison proves nothing")
	}
	if pinned, _, _ := templateHome(t, ``, strings.Replace(page, `"clipboard"`, `"clipboard@1.0.0"`, 1)); pinned == plain {
		t.Error("control: clipboard 1.0.0 named in the markup must serve a different page than the latest, or the comparison proves nothing")
	}

	// the chain survives: what comes after .javascript() still lands
	tenant, _ := writeTestSite(t, `router.javascript({ drive: true }).description("after javascript");`, page)
	captureBootOutput(t, func() {
		if err := tenant.Run(); err != nil {
			t.Fatal(err)
		}
	})
	if got := tenant.routeTree().root.ensureFolder(tenant).meta["description"].Text(); got != "after javascript" {
		t.Errorf("a declaration after .javascript() was lost: %q", got)
	}

	// a branch's router names its own file, once for both spellings
	tenant, directory := writeTestSite(t, ``, page)
	writeSiteFile(t, directory, "docs/router.kitwork.js", `import { router } from "kitwork";`+"\nrouter.jitjs(true);\nrouter.javascript({ drive: true });")
	writeSiteFile(t, directory, "docs/page.kitwork.html", `<main>docs</main>`)
	printed = captureBootOutput(t, func() {
		if err := tenant.Run(); err != nil {
			t.Fatal(err)
		}
	})
	branch := "[router] " + filepath.Join("docs", "router.kitwork.js") + ": " + said + "\n"
	if got := strings.Count(printed, branch); got != 1 {
		t.Errorf("want %q once, got %d:\n%s", strings.TrimSpace(branch), got, printed)
	}
	if strings.Contains(printed, line) {
		t.Errorf("the root router calls neither, so it gets no line:\n%s", printed)
	}
}

// Nothing can carry a version pin or a pre-render switch any more: the site's presentation, the
// render configuration and the JavaScript delivery options have no field for one. A version written
// in a template page's own markup is the only way to name one.
func TestNothingCarriesAVersionPinOrPrerender(t *testing.T) {
	for _, carrier := range []any{site.PresentationSnapshot{}, render.Config{}, jitjs.Options{}} {
		kind := reflect.TypeOf(carrier)
		for i := 0; i < kind.NumField(); i++ {
			field := kind.Field(i)
			name := strings.ToLower(field.Name)
			if strings.Contains(name, "version") || strings.Contains(name, "prerender") || strings.Contains(name, "pin") {
				t.Errorf("%s still has %s: router.javascript() and its pins are gone", kind, field.Name)
			}
		}
		if kind.NumField() == 0 {
			t.Errorf("control: %s has no fields, the check proves nothing", kind)
		}
	}
	// control: the check does find a field that carries one
	type pinned struct{ ComponentVersions map[string]string }
	if field, ok := reflect.TypeOf(pinned{}).FieldByName("ComponentVersions"); !ok || !strings.Contains(strings.ToLower(field.Name), "version") {
		t.Error("control: the check must recognise a version field")
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
