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

var stagedJITTagPattern = regexp.MustCompile(`data-kitwork-jit="?([a-z]+)"?[[:space:]]+data-kitwork-hash="?([0-9a-f]{64})"?[[:space:]]+src="?(/jit/([0-9a-f]{64})\.([A-Za-z0-9._-]+)\.js)"?[[:space:]]+integrity="(sha256-[A-Za-z0-9+/=]+)"[[:space:]]+crossorigin="?anonymous"?`)

type stagedJITTag struct {
	role      string
	hash      string
	suffix    string
	path      string
	integrity string
}

func writeKitJSTestSite(t *testing.T, router, page string) (*Tenant, string) {
	t.Helper()
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	writeKitJSFile(t, directory, "router.kitwork.js", `import { router } from "kitwork";`+"\n"+router)
	writeKitJSFile(t, directory, "index.kitwork.html", `<!doctype html><html><head><title>KitJS</title></head><body {{ if section == "docs" }}data-ready="yes" {{ end }}>{{ @page }}</body></html>`)
	writeKitJSFile(t, directory, "page.kitwork.html", page)
	writeKitJSFile(t, directory, "notfound.kitwork.html", `<main>Not found</main>`)

	tenant := NewTenant(root, "localhost")
	t.Cleanup(tenant.Close)
	return tenant, directory
}

func writeKitJSRouteGraphSite(t *testing.T, rootIndex, docsIndex string) *Tenant {
	t.Helper()
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	writeKitJSFile(t, directory, "router.kitwork.js", `import { router } from "kitwork"; router.jitjs(true); router.get((ctx) => ctx.bind({ section: "home" }));`)
	writeKitJSFile(t, directory, "docs/router.kitwork.js", `import { router } from "kitwork"; router.get((ctx) => ctx.bind({ section: "docs" }));`)
	writeKitJSFile(t, directory, "index.kitwork.html", rootIndex)
	if docsIndex != "" {
		writeKitJSFile(t, directory, "docs/index.kitwork.html", docsIndex)
	}
	writeKitJSFile(t, directory, "page.kitwork.html", `<main data-kit-scope="count: 0"><a id="docs-link" href="/docs">Docs</a><output data-kit-text="count">0</output></main>`)
	writeKitJSFile(t, directory, "docs/page.kitwork.html", `<main data-kit-component="progress-bar" data-kit-version="2.0.0"><a id="home-link" href="/">Home</a></main>`)
	writeKitJSFile(t, directory, "notfound.kitwork.html", `<main>Not found</main>`)

	tenant := NewTenant(root, "localhost")
	t.Cleanup(tenant.Close)
	return tenant
}

func writeKitJSFile(t *testing.T, directory, relative, source string) {
	t.Helper()
	filename := filepath.Join(directory, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
}

func serveKitJSPage(t *testing.T, tenant *Tenant, route string) ([]stagedJITTag, string) {
	t.Helper()
	recorder := httptest.NewRecorder()
	tenant.Serve(recorder, httptest.NewRequest(http.MethodGet, "http://localhost"+route, nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("%s status=%d body=%s", route, recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	matches := stagedJITTagPattern.FindAllStringSubmatch(body, -1)
	if len(matches) == 0 {
		t.Fatalf("%s staged JIT tags missing: %s", route, body)
	}
	tags := make([]stagedJITTag, len(matches))
	for index, match := range matches {
		if match[2] != match[4] {
			t.Fatalf("%s tag hash does not match URL: %v", route, match)
		}
		tags[index] = stagedJITTag{role: match[1], hash: match[2], path: match[3], suffix: match[5], integrity: match[6]}
	}
	return tags, body
}

func findStagedJITTag(tags []stagedJITTag, role, suffix string) (stagedJITTag, bool) {
	for _, tag := range tags {
		if tag.role == role && (suffix == "" || tag.suffix == suffix) {
			return tag, true
		}
	}
	return stagedJITTag{}, false
}

func stagedJITRoles(tags []stagedJITTag) string {
	roles := make([]string, len(tags))
	for index, tag := range tags {
		roles[index] = tag.role
	}
	return strings.Join(roles, ",")
}

func TestJitjsCanonicalContractAndDeprecatedAliases(t *testing.T) {
	for _, test := range []struct {
		name    string
		call    string
		enabled bool
	}{
		{name: "canonical bare enables", call: `router.jitjs();`, enabled: true},
		{name: "canonical true enables", call: `router.jitjs(true);`, enabled: true},
		{name: "canonical false disables", call: `router.jitjs(false);`, enabled: false},
		{name: "canonical object enables", call: `router.jitjs({});`, enabled: true},
		{name: "empty component manifest enables", call: `router.jitjs({ components: {} });`, enabled: true},
		{name: "deprecated kitjs enables", call: `router.kitjs(true);`, enabled: true},
		{name: "deprecated KitJS disables", call: `router.kitJS(false);`, enabled: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			tenant, _ := writeKitJSTestSite(t, test.call, `<main></main>`)
			if err := tenant.Run(); err != nil {
				t.Fatal(err)
			}
			if got := tenant.SiteGeneration().Presentation().Snapshot().KitJS; got != test.enabled {
				t.Fatalf("KitJS enabled=%t, want %t", got, test.enabled)
			}
		})
	}
}

func TestJitjsRejectsInvalidArguments(t *testing.T) {
	for _, test := range []struct {
		name string
		call string
		want string
	}{
		{name: "string", call: `router.jitjs("true");`, want: "expects zero arguments or one boolean; got string"},
		{name: "number", call: `router.jitjs(1);`, want: "expects zero arguments or one boolean; got number"},
		{name: "object option", call: `router.jitjs({ enabled: true });`, want: `unsupported field "enabled"`},
		{name: "null", call: `router.jitjs(null);`, want: "expects zero arguments or one boolean; got nil"},
		{name: "undefined", call: `router.jitjs(undefined);`, want: "expects zero arguments or one boolean; got nil"},
		{name: "too many", call: `router.jitjs(true, false);`, want: "expects zero arguments or one boolean"},
		{name: "deprecated alias stays strict", call: `router.kitjs("true");`, want: "expects zero arguments or one boolean; got string"},
	} {
		t.Run(test.name, func(t *testing.T) {
			tenant, _ := writeKitJSTestSite(t, test.call, `<main></main>`)
			err := tenant.Run()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Run error=%v, want %q", err, test.want)
			}
		})
	}
}

func TestKitJSRoutesUseSpecificGraphsAndReuseStableChunks(t *testing.T) {
	tenant := writeKitJSRouteGraphSite(t,
		`<!doctype html><html><head><title>Shared</title></head><body>{{ @page }}</body></html>`, "")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	home, homeBody := serveKitJSPage(t, tenant, "/")
	docs, docsBody := serveKitJSPage(t, tenant, "/docs")
	if got := stagedJITRoles(home); got != "runtime,hydrate,graph" {
		t.Fatalf("home delivery roles=%s", got)
	}
	if got := stagedJITRoles(docs); got != "runtime,hydrate,graph,service,component" {
		t.Fatalf("docs delivery roles=%s", got)
	}
	for _, role := range []string{"runtime", "hydrate"} {
		homeTag, _ := findStagedJITTag(home, role, "")
		docsTag, _ := findStagedJITTag(docs, role, "")
		if homeTag.hash == "" || homeTag.hash != docsTag.hash {
			t.Fatalf("stable %s chunk mismatch: home=%+v docs=%+v", role, homeTag, docsTag)
		}
	}
	homeGraph, _ := findStagedJITTag(home, "graph", "graph")
	docsGraph, _ := findStagedJITTag(docs, "graph", "graph")
	if homeGraph.hash == docsGraph.hash {
		t.Fatal("component-only route change did not change the exact graph script hash")
	}
	if strings.Contains(homeBody, "progress-bar.js") || strings.Contains(homeBody, ".progress.js") {
		t.Fatal("home route received docs-only packages")
	}
	if _, ok := findStagedJITTag(docs, "service", "progress"); !ok {
		t.Fatal("docs route omitted progress service")
	}
	if _, ok := findStagedJITTag(docs, "component", "progress-bar"); !ok {
		t.Fatal("docs route omitted progress-bar component")
	}
	for _, body := range []string{homeBody, docsBody} {
		if strings.Contains(body, "data-kitwork-plan") || strings.Contains(body, "data-kitwork-runtime") || strings.Contains(body, "/kit.js/") {
			t.Fatalf("legacy plan delivery leaked into staged HTML: %s", body)
		}
	}
	if assets := tenant.renderPlan().kitJSAssets.Len(); assets != 6 {
		t.Fatalf("generation prepared %d assets, want stable base + two graphs + docs packages", assets)
	}
}

func TestKitJSStagedAssetsServeCanonicalImmutableURLs(t *testing.T) {
	tenant, _ := writeKitJSTestSite(t, `router.jitjs(true);`,
		`<main data-kit-component="progress-bar" data-kit-version="2.0.0"></main>`)
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	if !tenant.SiteGeneration().Presentation().Snapshot().KitJS {
		t.Fatal("router.jitjs(true) did not reach the generation presentation snapshot")
	}
	tags, body := serveKitJSPage(t, tenant, "/")
	if got := stagedJITRoles(tags); got != "runtime,hydrate,graph,service,component" {
		t.Fatalf("delivery roles=%s body=%s", got, body)
	}
	for _, tag := range tags {
		stored, ok := tenant.renderPlan().kitJSAsset(tag.hash)
		if !ok || string(stored.Role) != tag.role || stored.Suffix != tag.suffix || stored.Name != strings.TrimPrefix(tag.path, "/jit/") ||
			stored.Integrity != tag.integrity {
			t.Fatalf("stored asset does not match tag: stored=%+v tag=%+v", stored, tag)
		}

		get := httptest.NewRecorder()
		tenant.Serve(get, httptest.NewRequest(http.MethodGet, "http://localhost"+tag.path, nil))
		if get.Code != http.StatusOK || get.Body.String() != string(stored.JavaScript) {
			t.Fatalf("GET %s status=%d bytes=%d", tag.path, get.Code, get.Body.Len())
		}
		if got := get.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
			t.Fatalf("Cache-Control=%q", got)
		}
		if got := get.Header().Get("ETag"); got != `"`+tag.hash+`"` {
			t.Fatalf("ETag=%q", got)
		}
		if got := get.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Fatalf("X-Content-Type-Options=%q", got)
		}

		conditional := httptest.NewRequest(http.MethodGet, "http://localhost"+tag.path, nil)
		conditional.Header.Set("If-None-Match", `"`+tag.hash+`"`)
		conditionalRecorder := httptest.NewRecorder()
		tenant.Serve(conditionalRecorder, conditional)
		if conditionalRecorder.Code != http.StatusNotModified || conditionalRecorder.Body.Len() != 0 {
			t.Fatalf("conditional %s status=%d bytes=%d", tag.path, conditionalRecorder.Code, conditionalRecorder.Body.Len())
		}

		head := httptest.NewRecorder()
		tenant.Serve(head, httptest.NewRequest(http.MethodHead, "http://localhost"+tag.path, nil))
		if head.Code != http.StatusOK || head.Body.Len() != 0 || head.Header().Get("Content-Length") == "" {
			t.Fatalf("HEAD %s status=%d bytes=%d headers=%v", tag.path, head.Code, head.Body.Len(), head.Header())
		}
	}

	runtimeTag, _ := findStagedJITTag(tags, "runtime", "runtime")
	invalid := []string{
		"/jit/not-a-hash.runtime.js",
		"/jit/" + strings.Repeat("A", 64) + ".runtime.js",
		"/jit/../secret.js",
		"/jit/" + strings.Repeat("0", 64) + ".runtime.js",
		"/jit/" + runtimeTag.hash + ".dialog.js",
		runtimeTag.path + "?v=1",
		runtimeTag.path + "/extra",
		"/kit.js/" + runtimeTag.hash + ".js",
	}
	for _, requestPath := range invalid {
		recorder := httptest.NewRecorder()
		tenant.Serve(recorder, httptest.NewRequest(http.MethodGet, "http://localhost"+requestPath, nil))
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("%s status=%d, want 404", requestPath, recorder.Code)
		}
	}
	post := httptest.NewRecorder()
	tenant.Serve(post, httptest.NewRequest(http.MethodPost, "http://localhost"+runtimeTag.path, nil))
	if post.Code != http.StatusNotFound {
		t.Fatalf("POST staged asset status=%d", post.Code)
	}

	legacy := httptest.NewRecorder()
	tenant.Serve(legacy, httptest.NewRequest(http.MethodGet, "http://localhost/kit.js", nil))
	if legacy.Code != http.StatusOK || !strings.Contains(legacy.Body.String(), "kitwork:ready") {
		t.Fatal("exact legacy /kit.js endpoint was not preserved")
	}
}

func TestKitJSStagedScriptsPrecedeCrossOriginBase(t *testing.T) {
	tenant, directory := writeKitJSTestSite(t, `router.jitjs(true);`, `<main data-kit-scope="count: 0"></main>`)
	writeKitJSFile(t, directory, "index.kitwork.html", `<!doctype html><html><head><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="script-src 'self'"><base href="https://assets.invalid/"><title>Base fence</title></head><body>{{ @page }}</body></html>`)
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	tags, body := serveKitJSPage(t, tenant, "/")
	runtimeTag, ok := findStagedJITTag(tags, "runtime", "runtime")
	if !ok {
		t.Fatal("runtime tag missing")
	}
	firstScript := strings.Index(body, "data-kitwork-jit=runtime")
	if firstScript < 0 {
		firstScript = strings.Index(body, `data-kitwork-jit="runtime"`)
	}
	base := strings.Index(body, "assets.invalid")
	charset := strings.Index(body, `charset=utf-8`)
	if charset < 0 {
		charset = strings.Index(body, `charset="utf-8"`)
	}
	csp := strings.Index(strings.ToLower(body), "content-security-policy")
	if charset < 0 || csp < 0 || firstScript < 0 || base < 0 || !(charset < csp && csp < firstScript && firstScript < base) {
		t.Fatalf("want charset < CSP < staged scripts < cross-origin base:\n%s", body)
	}
	if charset >= 1024 {
		t.Fatalf("charset offset=%d, want declaration in the first 1024 bytes", charset)
	}
	if !strings.Contains(body, `integrity="`+runtimeTag.integrity+`"`) ||
		!strings.Contains(body, `crossorigin=anonymous`) && !strings.Contains(body, `crossorigin="anonymous"`) {
		t.Fatalf("runtime SRI/CORS metadata missing:\n%s", body)
	}
	asset := httptest.NewRecorder()
	tenant.Serve(asset, httptest.NewRequest(http.MethodGet, "http://localhost"+runtimeTag.path, nil))
	if asset.Code != http.StatusOK || asset.Body.Len() == 0 {
		t.Fatalf("same-origin raw JIT path status=%d bytes=%d", asset.Code, asset.Body.Len())
	}
}

func TestKitJSStagedAssetConcurrentReads(t *testing.T) {
	tenant, _ := writeKitJSTestSite(t, `router.jitjs(true);`, `<main data-kit-scope="count: 0"></main>`)
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	tags, _ := serveKitJSPage(t, tenant, "/")
	runtimeTag, _ := findStagedJITTag(tags, "runtime", "runtime")

	const readers = 32
	var wait sync.WaitGroup
	errorsSeen := make(chan string, readers)
	for range readers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			recorder := httptest.NewRecorder()
			tenant.Serve(recorder, httptest.NewRequest(http.MethodGet, "http://localhost"+runtimeTag.path, nil))
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

func TestKitJSStagedAssetsReuseAcrossGenerationsAndOldGraphSurvivesRetirement(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	writeKitJSFile(t, directory, "router.kitwork.js", `import { router } from "kitwork"; router.jitjs(true);`)
	writeKitJSFile(t, directory, "index.kitwork.html", `<!doctype html><html><head><title>KitJS</title></head><body>{{ @page }}</body></html>`)
	writeKitJSFile(t, directory, "notfound.kitwork.html", `<main>Not found</main>`)
	writeKitJSFile(t, directory, "page.kitwork.html", `<main data-kit-component="progress-bar" data-kit-version="2.0.0"></main>`)

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
	firstTags, _ := serveKitJSPage(t, firstTenant, "/")

	writeKitJSFile(t, directory, "page.kitwork.html", `<main data-kit-component="progress-bar" data-kit-version="2.0.0"></main><aside data-kit-component="dialog" data-kit-version="2.0.0"></aside>`)
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
	secondTags, _ := serveKitJSPage(t, secondTenant, "/")

	for _, identity := range []struct{ role, suffix string }{
		{role: "runtime", suffix: "runtime"},
		{role: "hydrate", suffix: "hydrate"},
		{role: "service", suffix: "progress"},
		{role: "component", suffix: "progress-bar"},
	} {
		first, firstOK := findStagedJITTag(firstTags, identity.role, identity.suffix)
		second, secondOK := findStagedJITTag(secondTags, identity.role, identity.suffix)
		if !firstOK || !secondOK || first.hash != second.hash {
			t.Fatalf("%s.%s was not reused across generations: first=%+v second=%+v", identity.role, identity.suffix, first, second)
		}
	}
	firstGraph, _ := findStagedJITTag(firstTags, "graph", "graph")
	secondGraph, _ := findStagedJITTag(secondTags, "graph", "graph")
	if firstGraph.hash == secondGraph.hash {
		t.Fatal("changed component graph reused the prior graph script")
	}

	firstTenant.Close()
	if _, ok := firstPlan.kitJSAsset(firstGraph.hash); ok {
		t.Fatal("retired generation retained its private preparation graph")
	}
	servedAfterRetirement := httptest.NewRecorder()
	secondTenant.Serve(servedAfterRetirement, httptest.NewRequest(http.MethodGet, "http://localhost"+firstGraph.path, nil))
	if servedAfterRetirement.Code != http.StatusOK || servedAfterRetirement.Body.Len() == 0 {
		t.Fatalf("site CAS lost old graph after retirement: status=%d bytes=%d", servedAfterRetirement.Code, servedAfterRetirement.Body.Len())
	}

	writeKitJSFile(t, directory, "router.kitwork.js", `import { router } from "kitwork";`)
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
	thirdTenant.Serve(legacyRequest, httptest.NewRequest(http.MethodGet, "http://localhost"+firstGraph.path, nil))
	if legacyRequest.Code != http.StatusOK || legacyRequest.Body.Len() == 0 {
		t.Fatalf("disabled generation lost retained staged graph: status=%d bytes=%d", legacyRequest.Code, legacyRequest.Body.Len())
	}
	thirdTenant.Close()
}

func TestKitJSLegacyIsolationDefaultOff(t *testing.T) {
	tenant, _ := writeKitJSTestSite(t, ``, `<button data-kit-action="toggle" data-kit-target="#menu">Menu</button><div id="menu"></div>`)
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	if tenant.SiteGeneration().Presentation().Snapshot().KitJS {
		t.Fatal("KitJS preview must default off")
	}
	home := httptest.NewRecorder()
	tenant.Serve(home, httptest.NewRequest(http.MethodGet, "http://localhost/", nil))
	body := home.Body.String()
	if !strings.Contains(body, "data-kitwork-jit=runtime") || !strings.Contains(body, "/kit.js?") ||
		strings.Contains(body, "data-kitwork-hash") || strings.Contains(body, "/jit/") {
		t.Fatalf("legacy tenant delivery changed:\n%s", body)
	}
	recorder := httptest.NewRecorder()
	tenant.Serve(recorder, httptest.NewRequest(http.MethodGet,
		"http://localhost/jit/"+strings.Repeat("0", 64)+".runtime.js", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("legacy tenant served a staged asset: %d", recorder.Code)
	}
}

func TestKitJSPreparationFailsClosed(t *testing.T) {
	for _, test := range []struct {
		name string
		page string
		want string
	}{
		{name: "unknown exact component", page: `<main data-kit-component="legacy-unmigrated@1.0.0"></main>`, want: "component package not found"},
		{name: "dynamic attribute", page: `<main data-kit-component="{{ component }}"></main>`, want: "static data-kit-* attributes"},
		{name: "legacy runtime marker", page: `<script data-kitwork-runtime src="/user.js"></script>`, want: "engine-emitted namespace"},
		{name: "authored hash marker", page: `<script data-kitwork-hash="abc"></script>`, want: "engine-emitted namespace"},
		{name: "authored staged role", page: `<script data-kitwork-jit="runtime"></script>`, want: "engine-emitted namespace"},
	} {
		t.Run(test.name, func(t *testing.T) {
			tenant, _ := writeKitJSTestSite(t, `router.jitjs(true);`, test.page)
			err := tenant.Run()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Run error=%v, want %q", err, test.want)
			}
		})
	}
}

func TestKitJSUnversionedUnknownComponentIsClientOwned(t *testing.T) {
	tenant, _ := writeKitJSTestSite(t, `router.jitjs(true);`,
		`<main data-kit-component="legacy-unmigrated"></main>`)
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	tags, body := serveKitJSPage(t, tenant, "/")
	if got := stagedJITRoles(tags); got != "runtime,hydrate,graph" {
		t.Fatalf("unversioned client component roles=%s body=%s", got, body)
	}
	if _, exists := findStagedJITTag(tags, "component", "legacy-unmigrated"); exists {
		t.Fatalf("unversioned client component received a managed package: %s", body)
	}
}

func TestKitJSDynamicViewOverrideCannotCreateRequestAsset(t *testing.T) {
	tenant, directory := writeKitJSTestSite(t,
		`router.jitjs(true); router.get((ctx) => ctx.view("alternate"));`,
		`<main data-kit-scope="count: 0"></main>`)
	writeKitJSFile(t, directory, "alternate/page.kitwork.html", `<main data-kit-component="progress-bar" data-kit-version="2.0.0"></main>`)
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
		t.Fatalf("request created a staged asset: before=%d after=%d", before, after)
	}
}
