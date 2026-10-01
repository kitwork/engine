package work

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	hydrate "github.com/kitwork/engine/jit/hydrate"
)

// componentFixture builds apps/<identity>/<domain>; a path in files is relative to the IDENTITY, so
// "_components/x.js" is shared by every domain and "localhost/_components/x.js" belongs to one.
func componentFixture(t *testing.T, files map[string]string) *Tenant {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "acme", "localhost"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		path := filepath.Join(root, "acme", filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tenant := NewTenant(root, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tenant.Close() })
	return tenant
}

// componentPage is the smallest site that renders one page through the JIT pipeline.
func componentPage(markup string) map[string]string {
	return map[string]string{
		"localhost/filesystem.kitwork": "",
		"localhost/index.kitwork.html": `<!doctype html><html><head></head><body data-kit-app="acme">{{ @page }}</body></html>`,
		"localhost/page.kitwork.html":  markup,
		"localhost/router.kitwork.js":  `import { router } from "kitwork";`,
	}
}

const sharedComponentSource = `kit.component("bid-stepper", { amount: 0 });`
const ownComponentSource = `kit.component("search", { open: false });`

// A site's own component: dropped in _components, named with no version, composed into the one
// cacheable /kit.js response beside the embedded catalogue.
func TestSiteComponentsShipThroughTheRuntimeRoute(t *testing.T) {
	files := componentPage(`<div data-kit-component="bid-stepper"></div>`)
	files["_components/bid-stepper.js"] = sharedComponentSource
	files["localhost/_components/search.js"] = ownComponentSource
	files["localhost/_components/READ-ME.js"] = `kit.component("ignored", {});`
	files["localhost/_components/not-a-module.txt"] = "ignored"
	tenant := componentFixture(t, files)

	set := tenant.renderPlan().SiteComponents()
	if set == nil {
		t.Fatal("the tenant owns components: the plan must carry them")
	}
	if got := strings.Join(set.Names(), ","); got != "bid-stepper,search" {
		t.Fatalf("component names = %q, want the identity's and the domain's (a capitalised file name is refused)", got)
	}

	// The page asks for all three by name; the response carries exactly those modules. The body is
	// minified here (tests run with AllowLocal false), so assert on what survives minification.
	request := httptest.NewRequest("GET", hydrate.RuntimePath+"?components=component:bid-stepper,component:search,component:dialog", nil)
	recorder := httptest.NewRecorder()
	if !tenant.serveHydrateIf(recorder, request) {
		t.Fatal("the runtime route must handle its own path")
	}
	body := recorder.Body.String()
	for _, want := range []string{`kit.component("bid-stepper"`, `kit.component("search"`, `kit.component("dialog"`} {
		if !strings.Contains(body, want) {
			t.Errorf("the composed runtime is missing %s", want)
		}
	}
	if !strings.Contains(body, "kitwork:ready") {
		t.Error("the kernel must still be the body of the response")
	}

	// A name the site does not own composes nothing — a query cannot mint asset variants.
	other := httptest.NewRequest("GET", hydrate.RuntimePath+"?components=component:nonesuch", nil)
	otherRecorder := httptest.NewRecorder()
	tenant.serveHydrateIf(otherRecorder, other)
	if strings.Contains(otherRecorder.Body.String(), "nonesuch") {
		t.Error("an unknown component name must resolve to nothing")
	}

	// A version suffix is the engine catalogue's spelling only: a site component has no version.
	versioned := httptest.NewRequest("GET", hydrate.RuntimePath+"?components=component:search@1.0.0", nil)
	versionedRecorder := httptest.NewRecorder()
	tenant.serveHydrateIf(versionedRecorder, versioned)
	if strings.Contains(versionedRecorder.Body.String(), `kit.component("search"`) {
		t.Error("search@1.0.0 must not resolve to the site's unversioned component")
	}
}

// The rendered page asks for the site's component by itself: delivery scans data-kit-component and
// gives the component a file of its own under /kit/components/.
func TestRenderRequestsTheSiteComponentsThePageNames(t *testing.T) {
	files := componentPage(`<div data-kit-component="bid-stepper"></div>`)
	files["_components/bid-stepper.js"] = sharedComponentSource
	tenant := componentFixture(t, files)

	request := httptest.NewRequest("GET", "http://localhost/", nil)
	recorder := httptest.NewRecorder()
	tenant.Serve(recorder, request)
	if recorder.Result().StatusCode != http.StatusOK {
		t.Fatalf("status = %d", recorder.Result().StatusCode)
	}
	if body := recorder.Body.String(); !strings.Contains(body, `src="/kit/components/bid-stepper.`) {
		t.Fatalf("the page must request its own component:\n%s", body)
	}
}

// A tenant that owns nothing renders exactly as before: no site, no request, no change.
func TestNoSiteComponentsIsUntouched(t *testing.T) {
	tenant := componentFixture(t, componentPage(`<div data-kit-component="bid-stepper"></div>`))
	if set := tenant.renderPlan().SiteComponents(); set != nil {
		t.Fatalf("no _components anywhere, yet the plan carries %v", set.Names())
	}
	request := httptest.NewRequest("GET", "http://localhost/", nil)
	recorder := httptest.NewRecorder()
	tenant.Serve(recorder, request)
	if strings.Contains(recorder.Body.String(), "components=") {
		t.Error("a component name nothing owns must not bring a runtime request")
	}
}

// The domain's own component wins over the identity's when both use one name — the nearer
// declaration is the one the site meant, like an import walking up to _core.
func TestDomainComponentOverridesTheIdentityOne(t *testing.T) {
	files := componentPage(`<div data-kit-component="search"></div>`)
	files["_components/search.js"] = `kit.component("search", { scope: "identity" });`
	files["localhost/_components/search.js"] = `kit.component("search", { scope: "domain" });`
	tenant := componentFixture(t, files)

	if source := tenant.renderPlan().SiteComponents().Component("search"); !strings.Contains(source, `"domain"`) {
		t.Fatalf("the domain's component must win, got %q", source)
	}
}

// The size limit exists to stop a site shipping a bundle through the runtime route. Studio — the
// first real application to run on the kernel — arrived at 429 KB in one component and was refused
// by a 256 KiB limit, so the limit is what a real workbench is measured against now.
//
// The rule it protects is narrower than the old comment claimed: a site's component does NOT ride
// the shared /kit.js. The page asks for what it names (/kit.js?components=component:<name>), so a
// page that names nothing still gets the kernel alone. What the limit really bounds is ONE
// response for the page that does name it.
func TestASiteComponentMayBeAsLargeAsARealApplication(t *testing.T) {
	// Big enough that the old 256 KiB limit would refuse it, small enough to stay under this one.
	body := `kit.component("workbench", { ready: true, filler: "` + strings.Repeat("x", 300*1024) + `" });`
	files := componentPage(`<div data-kit-component="workbench"></div>`)
	files["localhost/_components/workbench.js"] = body
	tenant := componentFixture(t, files)

	names := tenant.renderPlan().SiteComponents().Names()
	found := false
	for _, n := range names {
		if n == "workbench" {
			found = true
		}
	}
	if !found {
		t.Fatalf("a %d-byte component was refused; a real application's workbench is this size (names: %v)", len(body), names)
	}

	// The control: the limit still refuses something, so the assertion above is about the number
	// being right and not about the check having been deleted.
	over := `kit.component("bundle", { filler: "` + strings.Repeat("x", maxSiteComponentBytes) + `" });`
	files2 := componentPage(`<div data-kit-component="bundle"></div>`)
	files2["localhost/_components/bundle.js"] = over
	for _, n := range componentFixture(t, files2).renderPlan().SiteComponents().Names() {
		if n == "bundle" {
			t.Errorf("a %d-byte file passed a %d-byte limit — the bound is not being applied", len(over), maxSiteComponentBytes)
		}
	}
}
