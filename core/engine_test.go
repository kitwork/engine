package core

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kitwork/engine/capabilities"
	requestscope "github.com/kitwork/engine/request"
	"github.com/kitwork/engine/utilities/cache"
	"github.com/kitwork/engine/value"
)

func TestEngineAuthorizerRunsAtResolvedAppBoundary(t *testing.T) {
	root := t.TempDir()
	writeTreeTenant(t, root, "private")
	engine := New(root, 0, false, "")
	t.Cleanup(engine.Close)

	var called atomic.Bool
	engine.SetAuthorizer(func(
		request *http.Request,
		appID string,
		domain string,
	) (requestscope.Authorization, error) {
		called.Store(true)
		if request == nil || appID != "localhost" || domain != "localhost" {
			t.Errorf("authorizer boundary = app %q domain %q", appID, domain)
		}
		return requestscope.Authorization{}, errors.New("missing credentials")
	})

	recorder := httptest.NewRecorder()
	engine.ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodGet, "http://localhost/", nil),
	)
	if !called.Load() {
		t.Fatal("engine did not invoke the configured authorizer")
	}
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", recorder.Code)
	}
}

// writeTreeTenant lays a minimal FILESYSTEM-ROUTED tenant on disk (root/test/localhost) whose root
// router answers GET / with the given body. Returns the router file path (the tenant marker hot
// reload watches). The flat app.kitwork.js model is gone — every engine test drives the tree.
func writeTreeTenant(t *testing.T, tmpDir, body string) string {
	t.Helper()
	dir := filepath.Join(tmpDir, "test", "localhost")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	routerFile := filepath.Join(dir, "router.kitwork.js")
	writeRouterBody(t, routerFile, body)
	return routerFile
}

func writeRouterBody(t *testing.T, routerFile, body string) {
	t.Helper()
	code := "import { router } from \"kitwork\";\n" +
		"router.get().handle((ctx) => ctx.text(\"" + body + "\"));\n"
	if err := os.WriteFile(routerFile, []byte(code), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestEngineHotReloadAndFallback(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "kitwork-engine-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	routerFile := writeTreeTenant(t, tmpDir, "v1")

	// Initialize Engine with HotReload = true
	engine := New(tmpDir, 0, true, "")
	t.Cleanup(engine.Close)

	req1 := httptest.NewRequest("GET", "http://localhost/", nil)
	rr1 := httptest.NewRecorder()
	engine.ServeHTTP(rr1, req1)
	if rr1.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d. Body: %s", rr1.Code, rr1.Body.String())
	}
	if !strings.Contains(rr1.Body.String(), "v1") {
		t.Errorf("expected body to contain v1, got %s", rr1.Body.String())
	}
	oldTenant := engine.cache["localhost"].tenant
	oldAppRuntime := oldTenant.AppRuntime()
	oldSiteRuntime := oldTenant.SiteRuntime()
	oldGeneration := oldTenant.SiteGeneration()
	oldRouteGraph := oldGeneration.RouteGraph()
	oldBroker := oldTenant.SSEBroker()
	const reloadAppCapability = "__core_test_reload_app_capability"
	const reloadSiteCapability = "__core_test_reload_site_capability"
	capabilities.DefaultRegistry.RegisterWithLifetime(
		reloadAppCapability,
		capabilities.LifetimeApp,
		func(capabilities.Scope) value.Value {
			return value.New(&struct{ ID string }{ID: "reload-app"})
		},
	)
	capabilities.DefaultRegistry.Register(
		reloadSiteCapability,
		func(capabilities.Scope) value.Value {
			return value.New(&struct{ ID string }{ID: "reload-site"})
		},
	)
	oldAppCapability := oldTenant.Kitwork().Capability(reloadAppCapability)
	oldSiteCapability := oldTenant.Kitwork().Capability(reloadSiteCapability)

	// 2. Rewrite the root router as v2. The source digest detects content;
	// the wait only crosses the engine's one-second check throttle.
	writeRouterBody(t, routerFile, "v2")
	futureTime := time.Now().Add(5 * time.Second)
	if err := os.Chtimes(routerFile, futureTime, futureTime); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)

	req2 := httptest.NewRequest("GET", "http://localhost/", nil)
	rr2 := httptest.NewRecorder()
	engine.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d. Body: %s", rr2.Code, rr2.Body.String())
	}
	if !strings.Contains(rr2.Body.String(), "v2") {
		t.Errorf("expected body to contain v2, got %s", rr2.Body.String())
	}
	reloadedTenant := engine.cache["localhost"].tenant
	if reloadedTenant.AppRuntime() != oldAppRuntime {
		t.Fatal("hot reload replaced the app runtime")
	}
	if reloadedTenant.SiteRuntime() != oldSiteRuntime {
		t.Fatal("hot reload replaced the site runtime")
	}
	if reloadedTenant.SiteGeneration() == oldGeneration {
		t.Fatal("hot reload reused the old site generation")
	}
	if oldRouteGraph == nil || reloadedTenant.SiteGeneration().RouteGraph() == oldRouteGraph {
		t.Fatal("hot reload reused the old generation route graph")
	}
	if reloadedTenant.SSEBroker() != oldBroker {
		t.Fatal("hot reload replaced the site SSE broker")
	}
	if oldSiteRuntime.CurrentGeneration() != reloadedTenant.SiteGeneration() {
		t.Fatal("hot reload did not publish the new site generation")
	}
	if !oldGeneration.Retired() {
		t.Fatal("hot reload did not retire the replaced site generation")
	}
	if oldGeneration.RouteGraph() != nil {
		t.Fatal("retired generation retained its route graph")
	}
	if reloadedTenant.Kitwork().Capability(reloadAppCapability).V != oldAppCapability.V {
		t.Fatal("hot reload replaced an app-scoped capability")
	}
	if reloadedTenant.Kitwork().Capability(reloadSiteCapability).V == oldSiteCapability.V {
		t.Fatal("hot reload retained a site capability bound to the old tenant generation")
	}
	oldRecorder := httptest.NewRecorder()
	oldTenant.Serve(oldRecorder, httptest.NewRequest("GET", "http://localhost/", nil))
	if oldRecorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("hot reload did not close the replaced tenant, got status %d", oldRecorder.Code)
	}

	// 3. Broken syntax cannot publish a partial generation. The previous
	// generation remains current and continues serving.
	if err := os.WriteFile(routerFile, []byte("import { router } from \"kitwork\";\nrouter.get().handle((ctx => {\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	futureTime2 := futureTime.Add(5 * time.Second)
	if err := os.Chtimes(routerFile, futureTime2, futureTime2); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)

	req3 := httptest.NewRequest("GET", "http://localhost/", nil)
	rr3 := httptest.NewRecorder()
	engine.ServeHTTP(rr3, req3)
	if rr3.Code != http.StatusOK || !strings.Contains(rr3.Body.String(), "v2") {
		t.Errorf("broken generation replaced the current one: %d %s", rr3.Code, rr3.Body.String())
	}
	if engine.cache["localhost"].current() != reloadedTenant {
		t.Fatal("compile failure replaced the cached tenant")
	}

	// 4. Deleting the root router (the tenant marker) evicts the tenant from the cache → 404.
	if err := os.Remove(routerFile); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)

	req4 := httptest.NewRequest("GET", "http://localhost/", nil)
	rr4 := httptest.NewRecorder()
	engine.ServeHTTP(rr4, req4)
	if rr4.Code != http.StatusNotFound {
		t.Errorf("expected status 404 after deletion, got %d", rr4.Code)
	}
	if !oldSiteRuntime.Closed() {
		t.Error("deleting the site marker did not close its site runtime")
	}
	if !reloadedTenant.SiteGeneration().Retired() {
		t.Error("deleting the site did not retire its current generation")
	}
}

func TestEngineReloadsWholeGenerationForRouteGraphChanges(t *testing.T) {
	tmpDir := t.TempDir()
	dir := filepath.Join(tmpDir, "test", "localhost")
	write := func(relative, content string) {
		t.Helper()
		filename := filepath.Join(dir, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	request := func(engine *Engine, route string) *httptest.ResponseRecorder {
		t.Helper()
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "http://localhost"+route, nil)
		engine.ServeHTTP(recorder, req)
		return recorder
	}
	waitForCheck := func() {
		time.Sleep(1100 * time.Millisecond)
	}

	write("router.kitwork.js", `import { router } from "kitwork";`)
	write("_core/service.kitwork.js", `export const answer = () => ("service-v1");`)
	write(
		"api/router.kitwork.js",
		`import { router } from "kitwork";`+"\n"+
			`import { answer } from "../_core/service.kitwork.js";`+"\n"+
			`router.get().handle((ctx) => ctx.text("api-v1 " + answer()));`,
	)

	engine := New(tmpDir, 0, true, "")
	t.Cleanup(engine.Close)
	if response := request(engine, "/api"); response.Code != http.StatusOK ||
		!strings.Contains(response.Body.String(), "api-v1 service-v1") {
		t.Fatalf("baseline: %d %s", response.Code, response.Body.String())
	}
	first := engine.cache["localhost"].current()

	write(
		"api/router.kitwork.js",
		`import { router } from "kitwork";`+"\n"+
			`import { answer } from "../_core/service.kitwork.js";`+"\n"+
			`router.get().handle((ctx) => ctx.text("api-v2 " + answer()));`,
	)
	waitForCheck()
	if response := request(engine, "/api"); response.Code != http.StatusOK ||
		!strings.Contains(response.Body.String(), "api-v2 service-v1") {
		t.Fatalf("subfolder reload: %d %s", response.Code, response.Body.String())
	}
	second := engine.cache["localhost"].current()
	if second == first || !first.SiteGeneration().Retired() {
		t.Fatal("subfolder edit did not replace and retire the generation")
	}

	write("_core/service.kitwork.js", `export const answer = () => ("service-v2");`)
	waitForCheck()
	if response := request(engine, "/api"); response.Code != http.StatusOK ||
		!strings.Contains(response.Body.String(), "api-v2 service-v2") {
		t.Fatalf("import reload: %d %s", response.Code, response.Body.String())
	}
	third := engine.cache["localhost"].current()
	if third == second || !second.SiteGeneration().Retired() {
		t.Fatal("import edit did not replace and retire the generation")
	}

	write(
		"fresh/router.kitwork.js",
		`import { router } from "kitwork";`+"\n"+
			`router.get().handle((ctx) => ctx.text("fresh-alive"));`,
	)
	waitForCheck()
	if response := request(engine, "/fresh"); response.Code != http.StatusOK ||
		!strings.Contains(response.Body.String(), "fresh-alive") {
		t.Fatalf("route graph reload: %d %s", response.Code, response.Body.String())
	}
	if current := engine.cache["localhost"].current(); current == third || !third.SiteGeneration().Retired() {
		t.Fatal("new route folder did not replace and retire the generation")
	}
}

func TestEngineReloadsEnvironmentAsGenerationState(t *testing.T) {
	tmpDir := t.TempDir()
	dir := filepath.Join(tmpDir, "test", "localhost")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	routerFile := filepath.Join(dir, "router.kitwork.js")
	router := `import { router, env } from "kitwork";
router.get().handle((ctx) => ctx.text(env.MESSAGE));`
	if err := os.WriteFile(routerFile, []byte(router), 0o644); err != nil {
		t.Fatal(err)
	}
	envFile := filepath.Join(dir, ".env")
	if err := os.WriteFile(envFile, []byte("MESSAGE=env-v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	request := func(engine *Engine) *httptest.ResponseRecorder {
		t.Helper()
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "http://localhost/", nil))
		return recorder
	}

	engine := New(tmpDir, 0, true, "")
	t.Cleanup(engine.Close)
	if response := request(engine); response.Code != http.StatusOK || response.Body.String() != "env-v1" {
		t.Fatalf("baseline environment: %d %q", response.Code, response.Body.String())
	}
	first := engine.cache["localhost"].current()

	if err := os.WriteFile(envFile, []byte("MESSAGE=env-v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)
	if response := request(engine); response.Code != http.StatusOK || response.Body.String() != "env-v2" {
		t.Fatalf("reloaded environment: %d %q", response.Code, response.Body.String())
	}
	second := engine.cache["localhost"].current()
	if second == first || second.SiteGeneration() == first.SiteGeneration() {
		t.Fatal(".env edit did not replace the site generation")
	}
	if !first.SiteGeneration().Retired() {
		t.Fatal(".env edit did not retire the previous generation")
	}
}

func TestEngineReloadsTemplateSnapshotAndKeepsValidFallback(t *testing.T) {
	tmpDir := t.TempDir()
	dir := filepath.Join(tmpDir, "test", "localhost")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(dir, "router.kitwork.js"),
		[]byte(`import { router } from "kitwork";`),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	indexFile := filepath.Join(dir, "index.kitwork.html")
	pageFile := filepath.Join(dir, "page.kitwork.html")
	if err := os.WriteFile(indexFile, []byte(`<html><body>{{ @page }}</body></html>`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pageFile, []byte(`<main>template-v1</main>`), 0o644); err != nil {
		t.Fatal(err)
	}

	request := func(engine *Engine) *httptest.ResponseRecorder {
		t.Helper()
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "http://localhost/", nil))
		return recorder
	}
	engine := New(tmpDir, 0, true, "")
	t.Cleanup(engine.Close)
	if response := request(engine); response.Code != http.StatusOK ||
		!strings.Contains(response.Body.String(), "template-v1") {
		t.Fatalf("template baseline: %d %q", response.Code, response.Body.String())
	}
	first := engine.cache["localhost"].current()
	firstPlan := first.SiteGeneration().RenderPlan()
	if firstPlan == nil {
		t.Fatal("generation has no render plan")
	}

	if err := os.WriteFile(pageFile, []byte(`<main>template-v2</main>`), 0o644); err != nil {
		t.Fatal(err)
	}
	if response := request(engine); response.Code != http.StatusOK ||
		!strings.Contains(response.Body.String(), "template-v1") {
		t.Fatalf("active snapshot observed disk edit before reload: %d %q", response.Code, response.Body.String())
	}
	time.Sleep(1100 * time.Millisecond)
	if response := request(engine); response.Code != http.StatusOK ||
		!strings.Contains(response.Body.String(), "template-v2") {
		t.Fatalf("template reload: %d %q", response.Code, response.Body.String())
	}
	second := engine.cache["localhost"].current()
	if second == first || second.SiteGeneration().RenderPlan() == firstPlan {
		t.Fatal("template edit did not replace the generation render plan")
	}
	if first.SiteGeneration().RenderPlan() != nil {
		t.Fatal("retired generation retained its render plan")
	}

	if err := os.WriteFile(pageFile, []byte(`{{ if }}`), 0o644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)
	if response := request(engine); response.Code != http.StatusOK ||
		!strings.Contains(response.Body.String(), "template-v2") {
		t.Fatalf("broken template replaced valid generation: %d %q", response.Code, response.Body.String())
	}
	if engine.cache["localhost"].current() != second {
		t.Fatal("broken template candidate was published")
	}
}

func TestEnginePreservesSiteStateAndReplacesGenerationCache(t *testing.T) {
	tmpDir := t.TempDir()
	dir := filepath.Join(tmpDir, "test", "localhost")
	write := func(relative, content string) {
		t.Helper()
		filename := filepath.Join(dir, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	request := func(engine *Engine, route string) *httptest.ResponseRecorder {
		t.Helper()
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(
			recorder,
			httptest.NewRequest(http.MethodGet, "http://localhost"+route, nil),
		)
		return recorder
	}
	route := func(body, policy string) string {
		return `import { router } from "kitwork";` + "\n" +
			`router.get((ctx) => ctx.text("` + body + `")).` + policy + `("1h");`
	}

	write("router.kitwork.js", `import { router } from "kitwork";`)
	write("ram/router.kitwork.js", route("ram-v1", "cache"))
	write("disk/router.kitwork.js", route("disk-v1", "persist"))

	engine := New(tmpDir, 0, true, "")
	t.Cleanup(engine.Close)
	if response := request(engine, "/ram"); response.Code != http.StatusOK || response.Body.String() != "ram-v1" {
		t.Fatalf("RAM baseline: %d %q", response.Code, response.Body.String())
	}
	if response := request(engine, "/disk"); response.Code != http.StatusOK || response.Body.String() != "disk-v1" {
		t.Fatalf("persist baseline: %d %q", response.Code, response.Body.String())
	}
	first := engine.cache["localhost"].current()
	firstCache := first.SiteGeneration().ResponseCache()
	firstCache.Set("lifetime-probe", cache.Entry{Body: []byte("alive")}, time.Hour)
	persistStore := first.SiteRuntime().PersistStore()

	write("ram/router.kitwork.js", route("ram-v2", "cache"))
	write("disk/router.kitwork.js", route("disk-v2", "persist"))
	time.Sleep(1100 * time.Millisecond)

	if response := request(engine, "/ram"); response.Code != http.StatusOK || response.Body.String() != "ram-v2" {
		t.Fatalf("generation RAM cache survived reload: %d %q", response.Code, response.Body.String())
	}
	if response := request(engine, "/disk"); response.Code != http.StatusOK ||
		response.Body.String() != "disk-v1" ||
		response.Header().Get("X-Kitwork-Cache") != "hit" {
		t.Fatalf(
			"site persistent cache did not survive reload: code=%d body=%q cache=%q",
			response.Code,
			response.Body.String(),
			response.Header().Get("X-Kitwork-Cache"),
		)
	}
	second := engine.cache["localhost"].current()
	if second.SiteGeneration().ResponseCache() == firstCache {
		t.Fatal("hot reload reused the generation RAM cache")
	}
	if second.SiteRuntime().PersistStore() != persistStore {
		t.Fatal("hot reload replaced the site persistent store")
	}
	if _, ok := firstCache.Get("lifetime-probe"); ok {
		t.Fatal("retired generation retained its RAM response cache")
	}
}

func TestEnginePreservesRateLimitStateAcrossGenerationReload(t *testing.T) {
	tmpDir := t.TempDir()
	dir := filepath.Join(tmpDir, "test", "localhost")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	routerFile := filepath.Join(dir, "router.kitwork.js")
	write := func(body string) {
		t.Helper()
		source := `import { router } from "kitwork";` + "\n" +
			`router.ratelimit({ ip: 1, period: "1m" });` + "\n" +
			`router.get().handle((ctx) => ctx.text("` + body + `"));`
		if err := os.WriteFile(routerFile, []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	request := func(engine *Engine) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "http://localhost/", nil)
		req.RemoteAddr = "8.8.4.4:1234"
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, req)
		return recorder
	}

	write("v1")
	engine := New(tmpDir, 0, true, "")
	t.Cleanup(engine.Close)
	if response := request(engine); response.Code != http.StatusOK {
		t.Fatalf("first limited request: %d %q", response.Code, response.Body.String())
	}
	first := engine.cache["localhost"].current()
	limiter := first.SiteRuntime().Limiter()

	write("v2")
	time.Sleep(1100 * time.Millisecond)
	second, err := engine.run("localhost")
	if err != nil {
		t.Fatal(err)
	}
	if second == first || second.SiteGeneration() == first.SiteGeneration() {
		t.Fatal("router edit did not replace the generation")
	}
	if second.SiteRuntime().Limiter() != limiter {
		t.Fatal("hot reload replaced the site limiter")
	}
	if response := request(engine); response.Code != http.StatusTooManyRequests {
		t.Fatalf("reload reset the rate-limit budget: %d %q", response.Code, response.Body.String())
	}
}

func TestEngineSharesAppRuntimeAndIsolatesSiteRuntimes(t *testing.T) {
	tmpDir := t.TempDir()
	identity := "identity-a"
	for _, domain := range []string{"first.example", "second.example"} {
		dir := filepath.Join(tmpDir, identity, domain)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		writeRouterBody(t, filepath.Join(dir, "router.kitwork.js"), domain)
	}

	engine := New(tmpDir, 0, true, "")
	t.Cleanup(engine.Close)

	first, err := engine.run("first.example")
	if err != nil {
		t.Fatal(err)
	}
	second, err := engine.run("second.example")
	if err != nil {
		t.Fatal(err)
	}

	var appFactories atomic.Int32
	var siteFactories atomic.Int32
	const appCapability = "__core_test_app_runtime_capability"
	const siteCapability = "__core_test_site_runtime_capability"
	capabilities.DefaultRegistry.RegisterWithLifetime(
		appCapability,
		capabilities.LifetimeApp,
		func(capabilities.Scope) value.Value {
			appFactories.Add(1)
			return value.New(&struct{ ID string }{ID: "app"})
		},
	)
	capabilities.DefaultRegistry.Register(
		siteCapability,
		func(capabilities.Scope) value.Value {
			siteFactories.Add(1)
			return value.New(&struct{ ID string }{ID: "site"})
		},
	)

	firstAppCapability := first.Kitwork().Capability(appCapability)
	secondAppCapability := second.Kitwork().Capability(appCapability)
	if firstAppCapability.V != secondAppCapability.V || appFactories.Load() != 1 {
		t.Fatal("sibling sites did not share one app-scoped capability")
	}
	firstSiteCapability := first.Kitwork().Capability(siteCapability)
	firstSiteCapabilityAgain := first.Kitwork().Capability(siteCapability)
	secondSiteCapability := second.Kitwork().Capability(siteCapability)
	if firstSiteCapability.V != firstSiteCapabilityAgain.V {
		t.Fatal("one site did not reuse its site-scoped capability")
	}
	if firstSiteCapability.V == secondSiteCapability.V || siteFactories.Load() != 2 {
		t.Fatal("sibling sites shared a site-scoped capability")
	}

	if first.AppRuntime() == nil || first.AppRuntime() != second.AppRuntime() {
		t.Fatal("domains under one identity do not share one app runtime")
	}
	if first.SiteRuntime() == nil || second.SiteRuntime() == nil {
		t.Fatal("site runtime was not attached to a tenant")
	}
	if first.SiteRuntime() == second.SiteRuntime() {
		t.Fatal("sibling domains share a site runtime")
	}
	if first.SiteGeneration() == nil || second.SiteGeneration() == nil {
		t.Fatal("site generation was not attached")
	}
	if first.SiteGeneration() == second.SiteGeneration() {
		t.Fatal("sibling domains share a site generation")
	}
	if first.AppRuntime().SiteCount() != 2 {
		t.Fatalf("expected two registered sites, got %d", first.AppRuntime().SiteCount())
	}

	appRuntime := first.AppRuntime()
	firstSite := first.SiteRuntime()
	secondSite := second.SiteRuntime()
	if err := os.Remove(first.RouterFile()); err != nil {
		t.Fatal(err)
	}
	engine.cache[first.Domain()].mu.Lock()
	engine.cache[first.Domain()].lastChecked = time.Time{}
	engine.cache[first.Domain()].mu.Unlock()
	if _, err := engine.run(first.Domain()); err == nil {
		t.Fatal("expected the removed site to be evicted")
	}
	if !firstSite.Closed() {
		t.Fatal("evicting one site did not close it")
	}
	if secondSite.Closed() {
		t.Fatal("evicting one site closed its sibling")
	}
	if appRuntime.SiteCount() != 1 {
		t.Fatalf("expected one remaining site, got %d", appRuntime.SiteCount())
	}

	engine.Close()
	if !appRuntime.Closed() || !secondSite.Closed() {
		t.Fatal("engine shutdown did not close the app runtime hierarchy")
	}
}

func TestAppSchedulerAndSitesShareOneAppRuntime(t *testing.T) {
	tmpDir := t.TempDir()
	identity := "identity-a"
	domain := "app.example"
	siteDir := filepath.Join(tmpDir, identity, domain)
	cronDir := filepath.Join(tmpDir, identity, "_cron")
	if err := os.MkdirAll(siteDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cronDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeRouterBody(t, filepath.Join(siteDir, "router.kitwork.js"), "ok")
	cron := `import { cron } from "kitwork";
cron.every("1h").handle(() => {});`
	if err := os.WriteFile(filepath.Join(cronDir, "heartbeat.kitwork.js"), []byte(cron), 0o644); err != nil {
		t.Fatal(err)
	}

	engine := New(tmpDir, 0, false, "")
	t.Cleanup(engine.Close)

	const workers = 8
	started := make(chan int, workers)
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			started <- engine.StartAppSchedulers()
		}()
	}
	wg.Wait()
	close(started)

	total := 0
	for count := range started {
		total += count
	}
	if total != 1 {
		t.Fatalf("concurrent scheduler startup started %d app tenants, want 1", total)
	}

	tenant, err := engine.run(domain)
	if err != nil {
		t.Fatal(err)
	}
	appTenant := engine.appTenants[identity]
	if appTenant == nil {
		t.Fatal("app scheduler tenant was not registered")
	}
	if tenant.AppRuntime() != appTenant.AppRuntime() {
		t.Fatal("scheduler and site do not share the identity app runtime")
	}
}

func TestEngineHotReloadDisabled(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "kitwork-engine-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	routerFile := writeTreeTenant(t, tmpDir, "v1")

	// Initialize Engine with HotReload = false
	engine := New(tmpDir, 0, false, "")
	t.Cleanup(engine.Close)

	req1 := httptest.NewRequest("GET", "http://localhost/", nil)
	rr1 := httptest.NewRecorder()
	engine.ServeHTTP(rr1, req1)
	if !strings.Contains(rr1.Body.String(), "v1") {
		t.Fatalf("expected v1, got %s", rr1.Body.String())
	}

	// Rewrite as v2 — with hot reload off, the cached tenant (and its compiled folder) must keep
	// serving v1.
	writeRouterBody(t, routerFile, "v2")
	futureTime := time.Now().Add(5 * time.Second)
	if err := os.Chtimes(routerFile, futureTime, futureTime); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)

	req2 := httptest.NewRequest("GET", "http://localhost/", nil)
	rr2 := httptest.NewRecorder()
	engine.ServeHTTP(rr2, req2)
	if !strings.Contains(rr2.Body.String(), "v1") {
		t.Errorf("expected v1 (cached), got %s", rr2.Body.String())
	}
}

func TestEngineRateLimit(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "kitwork-engine-rl-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	writeTreeTenant(t, tmpDir, "ok")

	// Global budget of 5 per window, per-IP budget of 2. Window = 1 minute so nothing refills
	// mid-test. Public test IPs — loopback/private would bypass the limiter.
	engine := New(tmpDir, 0, false, "")
	t.Cleanup(engine.Close)
	engine.SetRateLimit(&RateLimiter{Rate: 5, IPRate: 2, Period: time.Minute})

	send := func(ip string) int {
		r := httptest.NewRequest("GET", "http://localhost/", nil)
		r.RemoteAddr = ip + ":1234"
		rr := httptest.NewRecorder()
		engine.ServeHTTP(rr, r)
		return rr.Code
	}

	// IP 1: two allowed, third blocked by the per-IP budget — and the global token it took must
	// be ROLLED BACK (a blocked request never burns global budget).
	if c := send("1.1.1.1"); c != http.StatusOK {
		t.Errorf("ip1 #1: expected 200, got %d", c)
	}
	if c := send("1.1.1.1"); c != http.StatusOK {
		t.Errorf("ip1 #2: expected 200, got %d", c)
	}
	if c := send("1.1.1.1"); c != http.StatusTooManyRequests {
		t.Errorf("ip1 #3: expected 429 (per-IP), got %d", c)
	}

	// IP 2 has its own budget: two more allowed (global now 4/5).
	if c := send("2.2.2.2"); c != http.StatusOK {
		t.Errorf("ip2 #1: expected 200, got %d", c)
	}
	if c := send("2.2.2.2"); c != http.StatusOK {
		t.Errorf("ip2 #2: expected 200, got %d", c)
	}

	// IP 3 takes the 5th and last global token — proof the rollback above worked.
	if c := send("3.3.3.3"); c != http.StatusOK {
		t.Errorf("ip3 #1: expected 200, got %d", c)
	}

	// IP 4 is refused by the exhausted GLOBAL bucket despite a fresh per-IP budget.
	if c := send("4.4.4.4"); c != http.StatusTooManyRequests {
		t.Errorf("ip4 #1: expected 429 (global), got %d", c)
	}
}

func TestEngineBrowserRateLimit(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "kitwork-engine-rl-b-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	writeTreeTenant(t, tmpDir, "ok")

	// Browser fingerprint budget of 2 — catches one client rotating proxy IPs.
	engine := New(tmpDir, 0, false, "")
	t.Cleanup(engine.Close)
	engine.SetRateLimit(&RateLimiter{BrowserRate: 2, Period: time.Minute})

	send := func(ip string) int {
		r := httptest.NewRequest("GET", "http://localhost/", nil)
		r.RemoteAddr = ip + ":1234"
		r.Header.Set("User-Agent", "MaliciousBrowser")
		r.Header.Set("Accept-Language", "en")
		rr := httptest.NewRecorder()
		engine.ServeHTTP(rr, r)
		return rr.Code
	}

	if c := send("1.1.1.1"); c != http.StatusOK {
		t.Errorf("proxy A: expected 200, got %d", c)
	}
	if c := send("2.2.2.2"); c != http.StatusOK {
		t.Errorf("proxy B: expected 200, got %d", c)
	}
	// Third request: new IP, SAME browser fingerprint → blocked.
	if c := send("3.3.3.3"); c != http.StatusTooManyRequests {
		t.Errorf("proxy C: expected 429 (browser fingerprint), got %d", c)
	}
}

func TestEngineRateLimitIgnoresSpoofedForwardedFor(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "kitwork-engine-rl-xff-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	writeTreeTenant(t, tmpDir, "ok")

	engine := New(tmpDir, 0, false, "")
	t.Cleanup(engine.Close)
	engine.SetRateLimit(&RateLimiter{IPRate: 2, Period: time.Minute})

	// Same real connection, rotating FAKE X-Forwarded-For each time. Kitwork is the edge server:
	// the header is client-supplied and must be IGNORED (work.TrustProxyHeaders default false) —
	// otherwise the per-IP budget resets on every spoofed value.
	send := func(fakeIP string) int {
		r := httptest.NewRequest("GET", "http://localhost/", nil)
		r.RemoteAddr = "9.9.9.9:1234"
		r.Header.Set("X-Forwarded-For", fakeIP)
		rr := httptest.NewRecorder()
		engine.ServeHTTP(rr, r)
		return rr.Code
	}

	if c := send("1.2.3.4"); c != http.StatusOK {
		t.Errorf("#1: expected 200, got %d", c)
	}
	if c := send("5.6.7.8"); c != http.StatusOK {
		t.Errorf("#2: expected 200, got %d", c)
	}
	if c := send("10.11.12.13"); c != http.StatusTooManyRequests {
		t.Errorf("#3: spoofed X-Forwarded-For must NOT bypass the per-IP limit, got %d", c)
	}
}

func TestEngineRepairsCorruptGenerationBytecodeCache(t *testing.T) {
	root := t.TempDir()
	cacheDirectory := filepath.Join(root, ".cache", "bytecode")
	writeTreeTenant(t, root, "cache-alive")

	request := func(engine *Engine) *httptest.ResponseRecorder {
		t.Helper()
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(
			recorder,
			httptest.NewRequest(http.MethodGet, "http://localhost/", nil),
		)
		return recorder
	}

	first := New(root, 0, false, "")
	first.SetBytecodeCache(cacheDirectory)
	response := request(first)
	if response.Code != http.StatusOK || response.Body.String() != "cache-alive" {
		t.Fatalf("first generation: %d %q", response.Code, response.Body.String())
	}
	health := first.Health()
	if health.Executions == 0 || health.Successes == 0 || health.Programs == 0 {
		t.Fatalf("runtime health did not observe request execution: %+v", health)
	}
	first.Close()

	artifacts, err := filepath.Glob(filepath.Join(cacheDirectory, "*.kwbc"))
	if err != nil || len(artifacts) == 0 {
		t.Fatalf("generation did not publish a cache artifact: %v", err)
	}
	if err := os.WriteFile(artifacts[0], []byte("fault-injected"), 0o644); err != nil {
		t.Fatal(err)
	}

	second := New(root, 0, false, "")
	t.Cleanup(second.Close)
	second.SetBytecodeCache(cacheDirectory)
	response = request(second)
	if response.Code != http.StatusOK || response.Body.String() != "cache-alive" {
		t.Fatalf("repaired generation: %d %q", response.Code, response.Body.String())
	}
	repaired, err := os.ReadFile(artifacts[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(repaired) == "fault-injected" {
		t.Fatal("corrupt bytecode artifact was not repaired")
	}
}
