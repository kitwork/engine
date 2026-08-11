package work

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/kitwork/engine/app"
)

var kitJSRuntimeTagPattern = regexp.MustCompile(`data-kitwork-plan="?([0-9a-f]{64})"? src="?/kit\.js/([0-9a-f]{64})\.js"?`)

func writeKitJSTestSite(t *testing.T, router, page string) (*Tenant, string) {
	t.Helper()
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	write := func(relative, source string) {
		filename := filepath.Join(directory, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("router.kitwork.js", `import { router } from "kitwork";`+"\n"+router)
	write("index.kitwork.html", `<!doctype html><html data-kit-app="docs"><head><title>KitJS</title></head><body {{ if section == "docs" }}data-ready="yes" {{ end }}>{{ @page }}</body></html>`)
	write("page.kitwork.html", page)
	write("notfound.kitwork.html", `<main>Not found</main>`)

	tenant := NewTenant(root, "localhost")
	t.Cleanup(tenant.Close)
	return tenant, directory
}

func writeKitJSRouteGraphSite(t *testing.T, rootIndex, docsIndex string) *Tenant {
	t.Helper()
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	write := func(relative, source string) {
		filename := filepath.Join(directory, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("router.kitwork.js", `import { router } from "kitwork"; router.kitjs(true); router.get((ctx) => ctx.bind({ section: "home" }));`)
	write("docs/router.kitwork.js", `import { router } from "kitwork"; router.get((ctx) => ctx.bind({ section: "docs" }));`)
	write("index.kitwork.html", rootIndex)
	if docsIndex != "" {
		write("docs/index.kitwork.html", docsIndex)
	}
	write("page.kitwork.html", `<main data-kit-component="counter"><a id="docs-link" href="/docs">Docs</a><output data-kit-text="count">0</output></main>`)
	write("docs/page.kitwork.html", `<main data-kit-component="dropdown"><a id="home-link" href="/">Home</a><div data-kit-show="open" hidden>Open</div></main>`)
	write("notfound.kitwork.html", `<main>Not found</main>`)

	tenant := NewTenant(root, "localhost")
	t.Cleanup(tenant.Close)
	return tenant
}

func serveKitJSPlan(t *testing.T, tenant *Tenant, route string) (string, string) {
	t.Helper()
	recorder := httptest.NewRecorder()
	tenant.Serve(recorder, httptest.NewRequest(http.MethodGet, "http://localhost"+route, nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("%s status=%d body=%s", route, recorder.Code, recorder.Body.String())
	}
	match := kitJSRuntimeTagPattern.FindStringSubmatch(recorder.Body.String())
	if len(match) != 3 || match[1] != match[2] {
		t.Fatalf("%s runtime tag missing or inconsistent: %s", route, recorder.Body.String())
	}
	return match[1], recorder.Body.String()
}

func TestKitJSApplicationRoutesShareGenerationUnionGraph(t *testing.T) {
	tenant := writeKitJSRouteGraphSite(t,
		`<!doctype html><html data-kit-app="shared"><head><title>Shared</title></head><body>{{ @page }}</body></html>`, "")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	homeHash, home := serveKitJSPlan(t, tenant, "/")
	docsHash, docs := serveKitJSPlan(t, tenant, "/docs")
	if homeHash != docsHash {
		t.Fatalf("same application routes received different plans: home=%s docs=%s", homeHash, docsHash)
	}
	if !strings.Contains(home, `data-kit-app="shared"`) || !strings.Contains(docs, `data-kit-app="shared"`) {
		t.Fatal("shared application boundary missing from a prepared route")
	}
	asset, ok := tenant.renderPlan().kitJSAsset(homeHash)
	if !ok {
		t.Fatal("shared application asset missing")
	}
	source := string(asset.JavaScript)
	for _, marker := range []string{`kit.component("counter"`, `kit.component("dropdown"`, "KitJS same-plan Drive navigation"} {
		if !strings.Contains(source, marker) {
			t.Fatalf("shared application graph omitted %q", marker)
		}
	}
	if assets := tenant.renderPlan().kitJSAssets.Len(); assets != 1 {
		t.Fatalf("shared application prepared %d assets, want one union graph", assets)
	}
}

func TestKitJSRouteGraphGroupingPreservesNonAppOnlyUsedAndSeparatesIdentities(t *testing.T) {
	t.Run("no app marker stays per document", func(t *testing.T) {
		tenant := writeKitJSRouteGraphSite(t,
			`<!doctype html><html><head><title>Static</title></head><body>{{ @page }}</body></html>`, "")
		if err := tenant.Run(); err != nil {
			t.Fatal(err)
		}
		homeHash, _ := serveKitJSPlan(t, tenant, "/")
		docsHash, _ := serveKitJSPlan(t, tenant, "/docs")
		if homeHash == docsHash {
			t.Fatal("documents without an app marker were unexpectedly unioned")
		}
		for _, contentHash := range []string{homeHash, docsHash} {
			asset, ok := tenant.renderPlan().kitJSAsset(contentHash)
			if !ok {
				t.Fatalf("per-document asset %s missing", contentHash)
			}
			if strings.Contains(string(asset.JavaScript), "KitJS same-plan Drive navigation") {
				t.Fatal("document without data-kit-app selected Drive")
			}
		}
	})

	t.Run("different app identities stay separate", func(t *testing.T) {
		tenant := writeKitJSRouteGraphSite(t,
			`<!doctype html><html data-kit-app="public"><head><title>Public</title></head><body>{{ @page }}</body></html>`,
			`<!doctype html><html data-kit-app="docs"><head><title>Docs</title></head><body>{{ @page }}</body></html>`)
		if err := tenant.Run(); err != nil {
			t.Fatal(err)
		}
		homeHash, _ := serveKitJSPlan(t, tenant, "/")
		docsHash, _ := serveKitJSPlan(t, tenant, "/docs")
		if homeHash == docsHash {
			t.Fatal("different application identities were unioned")
		}
		if assets := tenant.renderPlan().kitJSAssets.Len(); assets != 2 {
			t.Fatalf("different identities prepared %d assets, want two", assets)
		}
	})
}

func TestKitJSOptInComposesAndServesGenerationAsset(t *testing.T) {
	tenant, _ := writeKitJSTestSite(t, `router.kitjs(true);`,
		`<main data-kit-component="theme@1.0.0"><button data-kit-click="toggle()">Theme</button></main>`)
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	if !tenant.SiteGeneration().Presentation().Snapshot().KitJS {
		t.Fatal("router.kitjs(true) did not reach the generation presentation snapshot")
	}

	home := httptest.NewRecorder()
	tenant.Serve(home, httptest.NewRequest(http.MethodGet, "http://localhost/", nil))
	if home.Code != http.StatusOK {
		t.Fatalf("home status=%d body=%s", home.Code, home.Body.String())
	}
	body := home.Body.String()
	if strings.Count(body, "data-kitwork-runtime") != 1 || strings.Contains(body, "data-kitwork-jit=runtime") || strings.Contains(body, "/kit.js?") {
		t.Fatalf("opted-in page did not isolate the new runtime:\n%s", body)
	}
	match := kitJSRuntimeTagPattern.FindStringSubmatch(body)
	if len(match) != 3 || match[1] != match[2] {
		t.Fatalf("invalid content-addressed runtime tag:\n%s", body)
	}
	contentHash := match[1]
	assetPath := "/kit.js/" + contentHash + ".js"

	assetRecorder := httptest.NewRecorder()
	tenant.Serve(assetRecorder, httptest.NewRequest(http.MethodGet, "http://localhost"+assetPath, nil))
	if assetRecorder.Code != http.StatusOK {
		t.Fatalf("asset status=%d body=%s", assetRecorder.Code, assetRecorder.Body.String())
	}
	if got := assetRecorder.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Fatalf("Cache-Control=%q", got)
	}
	if got := assetRecorder.Header().Get("ETag"); got != `"`+contentHash+`"` {
		t.Fatalf("ETag=%q", got)
	}
	asset := assetRecorder.Body.String()
	if !strings.Contains(asset, `kit.component("theme"`) || !strings.Contains(asset, "kit.storage") {
		t.Fatal("resolved component graph omitted theme or its storage service")
	}

	conditional := httptest.NewRequest(http.MethodGet, "http://localhost"+assetPath, nil)
	conditional.Header.Set("If-None-Match", `"`+contentHash+`"`)
	conditionalRecorder := httptest.NewRecorder()
	tenant.Serve(conditionalRecorder, conditional)
	if conditionalRecorder.Code != http.StatusNotModified || conditionalRecorder.Body.Len() != 0 {
		t.Fatalf("conditional status=%d bytes=%d", conditionalRecorder.Code, conditionalRecorder.Body.Len())
	}

	headRecorder := httptest.NewRecorder()
	tenant.Serve(headRecorder, httptest.NewRequest(http.MethodHead, "http://localhost"+assetPath, nil))
	if headRecorder.Code != http.StatusOK || headRecorder.Body.Len() != 0 || headRecorder.Header().Get("Content-Length") == "" {
		t.Fatalf("HEAD status=%d bytes=%d headers=%v", headRecorder.Code, headRecorder.Body.Len(), headRecorder.Header())
	}

	for _, invalid := range []string{
		"/kit.js/not-a-hash.js",
		"/kit.js/" + strings.Repeat("A", 64) + ".js",
		"/kit.js/../secret.js",
		"/kit.js/" + strings.Repeat("0", 64) + ".js",
	} {
		recorder := httptest.NewRecorder()
		tenant.Serve(recorder, httptest.NewRequest(http.MethodGet, "http://localhost"+invalid, nil))
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("%s status=%d, want 404", invalid, recorder.Code)
		}
	}

	// The exact legacy endpoint remains available for compatibility tooling;
	// opted-in HTML simply never references it.
	legacy := httptest.NewRecorder()
	tenant.Serve(legacy, httptest.NewRequest(http.MethodGet, "http://localhost/kit.js", nil))
	if legacy.Code != http.StatusOK || !strings.Contains(legacy.Body.String(), "kitwork:ready") {
		t.Fatal("legacy /kit.js endpoint was not preserved")
	}

	plan := tenant.renderPlan()
	tenant.Close()
	if _, ok := plan.kitJSAsset(contentHash); ok {
		t.Fatal("generation retirement retained its KitJS asset")
	}
}

func TestKitJSAssetConcurrentReads(t *testing.T) {
	tenant, _ := writeKitJSTestSite(t, `router.kitjs(true);`, `<main data-kit-component="dialog"></main>`)
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	home := httptest.NewRecorder()
	tenant.Serve(home, httptest.NewRequest(http.MethodGet, "http://localhost/", nil))
	match := kitJSRuntimeTagPattern.FindStringSubmatch(home.Body.String())
	if len(match) != 3 {
		t.Fatalf("runtime tag missing: %s", home.Body.String())
	}
	path := "http://localhost/kit.js/" + match[1] + ".js"

	const readers = 32
	var wait sync.WaitGroup
	errorsSeen := make(chan string, readers)
	for index := 0; index < readers; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			recorder := httptest.NewRecorder()
			tenant.Serve(recorder, httptest.NewRequest(http.MethodGet, path, nil))
			if recorder.Code != http.StatusOK || recorder.Body.Len() == 0 {
				errorsSeen <- recorder.Result().Status
			}
		}()
	}
	wait.Wait()
	close(errorsSeen)
	for failure := range errorsSeen {
		t.Fatal(failure)
	}
}

func TestKitJSAssetSurvivesOriginGenerationRetirement(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	write := func(relative, source string) {
		filename := filepath.Join(directory, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("router.kitwork.js", `import { router } from "kitwork"; router.kitjs(true);`)
	write("index.kitwork.html", `<!doctype html><html data-kit-app="handoff"><head><title>KitJS</title></head><body>{{ @page }}</body></html>`)
	write("notfound.kitwork.html", `<main>Not found</main>`)
	write("page.kitwork.html", `<main data-kit-component="dialog"></main>`)

	appRuntime := app.NewRuntime("test")
	t.Cleanup(appRuntime.Close)
	siteRuntime, err := appRuntime.Site(root, "localhost")
	if err != nil {
		t.Fatal(err)
	}
	firstGeneration, err := siteRuntime.PrepareGeneration()
	if err != nil {
		t.Fatal(err)
	}
	firstTenant := NewTenantWithRuntime(root, "localhost", appRuntime, siteRuntime, firstGeneration)
	if err := firstTenant.Run(); err != nil {
		t.Fatal(err)
	}
	if err := firstTenant.ActivateGeneration(); err != nil {
		t.Fatal(err)
	}
	firstPlan := firstTenant.renderPlan()

	firstPage := httptest.NewRecorder()
	firstTenant.Serve(firstPage, httptest.NewRequest(http.MethodGet, "http://localhost/", nil))
	match := kitJSRuntimeTagPattern.FindStringSubmatch(firstPage.Body.String())
	if len(match) != 3 {
		t.Fatalf("first generation runtime tag missing: %s", firstPage.Body.String())
	}
	oldHash := match[1]
	oldPath := "/kit.js/" + oldHash + ".js"
	oldAsset := httptest.NewRecorder()
	firstTenant.Serve(oldAsset, httptest.NewRequest(http.MethodGet, "http://localhost"+oldPath, nil))
	if oldAsset.Code != http.StatusOK {
		t.Fatalf("first generation asset status=%d", oldAsset.Code)
	}

	write("page.kitwork.html", `<main data-kit-component="drawer"></main>`)
	secondGeneration, err := siteRuntime.PrepareGeneration()
	if err != nil {
		t.Fatal(err)
	}
	secondTenant := NewTenantWithRuntime(root, "localhost", appRuntime, siteRuntime, secondGeneration)
	if err := secondTenant.Run(); err != nil {
		t.Fatal(err)
	}
	if err := secondTenant.ActivateGeneration(); err != nil {
		t.Fatal(err)
	}
	firstTenant.Close()
	if _, ok := firstPlan.kitJSAsset(oldHash); ok {
		t.Fatal("retired generation retained its private preparation asset")
	}

	servedAfterRetirement := httptest.NewRecorder()
	secondTenant.Serve(servedAfterRetirement, httptest.NewRequest(http.MethodGet, "http://localhost"+oldPath, nil))
	if servedAfterRetirement.Code != http.StatusOK || servedAfterRetirement.Body.String() != oldAsset.Body.String() {
		t.Fatalf("site CAS lost old hash after retirement: status=%d bytes=%d", servedAfterRetirement.Code, servedAfterRetirement.Body.Len())
	}

	write("router.kitwork.js", `import { router } from "kitwork";`)
	thirdGeneration, err := siteRuntime.PrepareGeneration()
	if err != nil {
		t.Fatal(err)
	}
	thirdTenant := NewTenantWithRuntime(root, "localhost", appRuntime, siteRuntime, thirdGeneration)
	if err := thirdTenant.Run(); err != nil {
		t.Fatal(err)
	}
	if thirdTenant.SiteGeneration().Presentation().Snapshot().KitJS {
		t.Fatal("third generation did not disable KitJS")
	}
	if err := thirdTenant.ActivateGeneration(); err != nil {
		t.Fatal(err)
	}
	secondTenant.Close()
	legacyRequest := httptest.NewRecorder()
	thirdTenant.Serve(legacyRequest, httptest.NewRequest(http.MethodGet, "http://localhost"+oldPath, nil))
	if legacyRequest.Code != http.StatusOK || legacyRequest.Body.String() != oldAsset.Body.String() {
		t.Fatalf("disabled generation lost retained KitJS CAS asset: status=%d bytes=%d", legacyRequest.Code, legacyRequest.Body.Len())
	}
	thirdTenant.Close()
}

func TestKitJSLegacyIsolationDefaultOff(t *testing.T) {
	tenant, _ := writeKitJSTestSite(t, ``, `<main data-kit-component="dropdown@v1.0.0"></main>`)
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	if tenant.SiteGeneration().Presentation().Snapshot().KitJS {
		t.Fatal("KitJS preview must default off")
	}
	home := httptest.NewRecorder()
	tenant.Serve(home, httptest.NewRequest(http.MethodGet, "http://localhost/", nil))
	body := home.Body.String()
	if strings.Contains(body, "data-kitwork-runtime") || !strings.Contains(body, "data-kitwork-jit=runtime") || !strings.Contains(body, "/kit.js?") {
		t.Fatalf("legacy tenant delivery changed:\n%s", body)
	}
	recorder := httptest.NewRecorder()
	tenant.Serve(recorder, httptest.NewRequest(http.MethodGet,
		"http://localhost/kit.js/"+strings.Repeat("0", 64)+".js", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("legacy tenant served a new-plan asset: %d", recorder.Code)
	}
}

func TestKitJSPreparationFailsClosed(t *testing.T) {
	for _, test := range []struct {
		name string
		page string
		want string
	}{
		{name: "unknown component", page: `<main data-kit-component="legacy-unmigrated"></main>`, want: "module not found"},
		{name: "dynamic attribute", page: `<main data-kit-component="{{ component }}"></main>`, want: "static data-kit-* attributes"},
		{name: "authored runtime marker", page: `<script data-kitwork-runtime src="/user.js"></script>`, want: "reserved"},
	} {
		t.Run(test.name, func(t *testing.T) {
			tenant, _ := writeKitJSTestSite(t, `router.kitjs(true);`, test.page)
			err := tenant.Run()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Run error=%v, want %q", err, test.want)
			}
		})
	}
}

func TestKitJSDynamicViewOverrideCannotCreateRequestAsset(t *testing.T) {
	tenant, directory := writeKitJSTestSite(t,
		`router.kitjs(true); router.get((ctx) => ctx.view("alternate"));`,
		`<main data-kit-component="dialog"></main>`)
	alternate := filepath.Join(directory, "alternate", "page.kitwork.html")
	if err := os.MkdirAll(filepath.Dir(alternate), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(alternate, []byte(`<main data-kit-component="drawer"></main>`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	plan := tenant.renderPlan()
	before := plan.kitJSAssets.Len()
	recorder := httptest.NewRecorder()
	tenant.Serve(recorder, httptest.NewRequest(http.MethodGet, "http://localhost/", nil))
	if !strings.Contains(recorder.Body.String(), "requires a generation-prepared template") {
		t.Fatalf("dynamic view override did not fail closed: %s", recorder.Body.String())
	}
	if after := plan.kitJSAssets.Len(); after != before {
		t.Fatalf("request created a KitJS asset: before=%d after=%d", before, after)
	}
}
