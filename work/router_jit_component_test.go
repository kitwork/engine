package work

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kitwork/engine/app"
	kitjavascript "github.com/kitwork/engine/jit/javascript"
	"github.com/kitwork/engine/site"
)

func TestRouterJITComponentLoadsTenantSourceWithInlineAndLegacyExactVersion(t *testing.T) {
	tenant, directory := writeKitJSTestSite(t,
		`router.jitjs(true); router.jitComponent("tenant-counter", "1.2.3", "components/tenant-counter.js");`,
		`<main data-kit-component="tenant-counter@1.2.3"></main><aside data-kit-component="tenant-counter" data-kit-version="1.2.3"></aside>`)
	raw := "kit.component(\"tenant-counter\", {\r\n  count: 0\r\n});"
	writeKitJSFile(t, directory, "components/tenant-counter.js", raw)
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	presentation := tenant.SiteGeneration().Presentation().Snapshot()
	if len(presentation.JITComponents) != 1 {
		t.Fatalf("tenant catalog = %#v", presentation.JITComponents)
	}
	canonical := presentation.JITComponents[0].JavaScript
	if canonical[0] != ';' || canonical[len(canonical)-1] != '\n' || bytes.Contains(canonical, []byte{'\r'}) ||
		!bytes.Contains(canonical, []byte("kit.component(\"tenant-counter\"")) {
		t.Fatalf("canonical tenant JavaScript = %q", canonical)
	}

	tags, body := serveKitJSPage(t, tenant, "/")
	if got := stagedJITRoles(tags); got != "runtime,hydrate,graph,component" {
		t.Fatalf("delivery roles=%s body=%s", got, body)
	}
	component, ok := findStagedJITTag(tags, "component", "tenant-counter")
	if !ok {
		t.Fatalf("tenant component tag missing: %s", body)
	}
	asset, ok := tenant.renderPlan().kitJSAsset(component.hash)
	if !ok || asset.Version != "1.2.3" || !bytes.Contains(asset.JavaScript, canonical) {
		t.Fatalf("tenant component asset = %+v", asset)
	}
}

func TestRouterJITComponentIsNotEmittedWhenNoTemplateUsesIt(t *testing.T) {
	tenant, directory := writeKitJSTestSite(t,
		`router.jitjs(true); router.jitComponent("tenant-counter", "1.0.0", "counter.js");`,
		`<main data-kit-scope="ready: true"></main>`)
	writeKitJSFile(t, directory, "counter.js", `kit.component("tenant-counter", {});`)
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	tags, body := serveKitJSPage(t, tenant, "/")
	if got := stagedJITRoles(tags); got != "runtime,hydrate,graph" {
		t.Fatalf("unused tenant component delivery=%s body=%s", got, body)
	}
	if tenant.renderPlan().kitJSAssets.Len() != 3 {
		t.Fatalf("unused catalog source created assets: %d", tenant.renderPlan().kitJSAssets.Len())
	}
}

func TestRouterJITComponentSourcePathIsRelativeToDeclaringFolder(t *testing.T) {
	tenant, directory := writeKitJSTestSite(t, `router.jitjs(true);`,
		`<main data-kit-component="docs-widget@1.0.0"></main>`)
	writeKitJSFile(t, directory, "docs/router.kitwork.js", `import { router } from "kitwork";
router.jitComponent("docs-widget", "1.0.0", "widget.js");`)
	writeKitJSFile(t, directory, "docs/widget.js", `kit.component("docs-widget", { owner: "docs" });`)
	writeKitJSFile(t, directory, "docs/page.kitwork.html", `<main>Docs</main>`)
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	tags, _ := serveKitJSPage(t, tenant, "/")
	component, ok := findStagedJITTag(tags, "component", "docs-widget")
	if !ok {
		t.Fatal("component declared by descendant router was not available generation-wide")
	}
	asset, ok := tenant.renderPlan().kitJSAsset(component.hash)
	if !ok || !bytes.Contains(asset.JavaScript, []byte(`owner: "docs"`)) {
		t.Fatal("component source was not resolved from the declaring folder")
	}
}

func TestRouterJITComponentRequiresExplicitJITAndUniqueNonReservedName(t *testing.T) {
	for _, test := range []struct {
		name   string
		router string
		want   string
	}{
		{
			name:   "jitjs remains explicit",
			router: `router.jitComponent("tenant-counter", "1.0.0", "counter.js");`,
			want:   "requires router.jitjs(true)",
		},
		{
			name: "duplicate name",
			router: `router.jitjs(true); router.jitComponent("tenant-counter", "1.0.0", "counter.js"); ` +
				`router.jitComponent("tenant-counter", "2.0.0", "counter.js");`,
			want: "duplicate JIT component declaration",
		},
		{
			name:   "embedded shadow",
			router: `router.jitjs(true); router.jitComponent("dialog", "9.0.0", "counter.js");`,
			want:   "shadows managed component",
		},
		{
			name:   "reserved staged role",
			router: `router.jitjs(true); router.jitComponent("runtime", "1.0.0", "counter.js");`,
			want:   "cannot be represented by a staged asset suffix",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			tenant, directory := writeKitJSTestSite(t, test.router, `<main></main>`)
			writeKitJSFile(t, directory, "counter.js", `kit.component("tenant-counter", {});`)
			err := tenant.Run()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Run error=%v, want %q", err, test.want)
			}
		})
	}
}

func TestRouterJITComponentNameBoundaryMatchesStagedDelivery(t *testing.T) {
	maximumName := "a" + strings.Repeat("b", kitjavascript.MaxStagedPackageSuffixBytes-1)
	tenant, directory := writeKitJSTestSite(t,
		`router.jitjs(true); router.jitComponent("`+maximumName+`", "1.0.0", "component.js");`,
		`<main data-kit-component="`+maximumName+`@1.0.0"></main>`)
	writeKitJSFile(t, directory, "component.js", `kit.component("`+maximumName+`", {});`)
	if err := tenant.Run(); err != nil {
		t.Fatalf("%d-byte component name Run error: %v", len(maximumName), err)
	}
	if err := tenant.ActivateGeneration(); err != nil {
		t.Fatalf("%d-byte component name activation error: %v", len(maximumName), err)
	}
	tags, body := serveKitJSPage(t, tenant, "/")
	component, ok := findStagedJITTag(tags, "component", maximumName)
	if !ok || component.suffix != maximumName {
		t.Fatalf("%d-byte component was not delivered with its exact suffix: tag=%+v body=%s", len(maximumName), component, body)
	}

	overlongName := maximumName + "c"
	overlong, overlongDirectory := writeKitJSTestSite(t,
		`router.jitjs(true); router.jitComponent("`+overlongName+`", "1.0.0", "component.js");`,
		`<main data-kit-component="`+overlongName+`@1.0.0"></main>`)
	writeKitJSFile(t, overlongDirectory, "component.js", `kit.component("`+overlongName+`", {});`)
	err := overlong.Run()
	if err == nil || !strings.Contains(err.Error(), "maximum 128 bytes") {
		t.Fatalf("%d-byte component Run error=%v, want early maximum rejection", len(overlongName), err)
	}
}

func TestRouterJITComponentRejectsInvalidArgumentsAndSourceFiles(t *testing.T) {
	for _, test := range []struct {
		name   string
		router string
		setup  func(*testing.T, string)
		want   string
	}{
		{name: "too few arguments", router: `router.jitComponent("x", "1.0.0");`, want: "exactly three string arguments"},
		{name: "non-string name", router: `router.jitComponent(1, "1.0.0", "counter.js");`, want: "argument 1 must be a string"},
		{name: "non-exact version", router: `router.jitComponent("x", "latest", "counter.js");`, want: "must be an exact SemVer"},
		{name: "missing source", router: `router.jitComponent("x", "1.0.0", "missing.js");`, want: "resolve router.jitComponent source"},
		{
			name: "directory source", router: `router.jitComponent("x", "1.0.0", "directory.js");`, want: "must be a regular file",
			setup: func(t *testing.T, directory string) {
				if err := os.Mkdir(filepath.Join(directory, "directory.js"), 0o755); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "oversize source", router: `router.jitComponent("x", "1.0.0", "large.js");`, want: "limit is",
			setup: func(t *testing.T, directory string) {
				writeKitJSFile(t, directory, "large.js", strings.Repeat("x", site.MaxJITComponentSourceBytes+1))
			},
		},
		{
			name: "utf8 bom", router: `router.jitComponent("x", "1.0.0", "bom.js");`, want: "UTF-8 without BOM",
			setup: func(t *testing.T, directory string) {
				writeKitJSFile(t, directory, "bom.js", "\ufeffkit.component(\"x\", {});")
			},
		},
		{
			name: "bare carriage return", router: `router.jitComponent("x", "1.0.0", "cr.js");`, want: "LF or CRLF",
			setup: func(t *testing.T, directory string) {
				writeKitJSFile(t, directory, "cr.js", "kit.component(\"x\", {});\rnext")
			},
		},
		{
			name: "installer wrapper escape", router: `router.jitjs(true); router.jitComponent("x", "1.0.0", "escape.js");`, want: "escapes the installer body",
			setup: func(t *testing.T, directory string) {
				writeKitJSFile(t, directory, "escape.js", "kit.component(\"x\", {});});")
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			tenant, directory := writeKitJSTestSite(t, test.router, `<main></main>`)
			if test.setup != nil {
				test.setup(t, directory)
			}
			err := tenant.Run()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Run error=%v, want %q", err, test.want)
			}
		})
	}
}

func TestRouterJITComponentConfinesSourceToTenantSite(t *testing.T) {
	t.Run("lexical traversal", func(t *testing.T) {
		tenant, directory := writeKitJSTestSite(t, "", `<main></main>`)
		outside := filepath.Join(filepath.Dir(filepath.Dir(directory)), "outside.js")
		if err := os.WriteFile(outside, []byte(`kit.component("outside", {});`), 0o644); err != nil {
			t.Fatal(err)
		}
		relative, err := filepath.Rel(directory, outside)
		if err != nil {
			t.Fatal(err)
		}
		writeKitJSFile(t, directory, "router.kitwork.js", `import { router } from "kitwork";
router.jitComponent("outside", "1.0.0", "`+filepath.ToSlash(relative)+`");`)
		err = tenant.Run()
		if err == nil || !strings.Contains(err.Error(), "escapes the tenant site") {
			t.Fatalf("Run error=%v, want tenant boundary rejection", err)
		}
	})

	t.Run("symlink traversal", func(t *testing.T) {
		tenant, directory := writeKitJSTestSite(t,
			`router.jitComponent("outside", "1.0.0", "outside-link.js");`, `<main></main>`)
		outside := filepath.Join(filepath.Dir(filepath.Dir(directory)), "outside.js")
		if err := os.WriteFile(outside, []byte(`kit.component("outside", {});`), 0o644); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(directory, "outside-link.js")
		if err := os.Symlink(outside, link); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		err := tenant.Run()
		if err == nil || !strings.Contains(err.Error(), "escapes the tenant site") {
			t.Fatalf("symlink Run error=%v, want tenant boundary rejection", err)
		}
	})
}

func TestRouterJITComponentSnapshotSurvivesSourceEditAndInvalidatesGeneration(t *testing.T) {
	tenant, directory := writeKitJSTestSite(t,
		`router.jitjs(true); router.jitComponent("tenant-counter", "1.0.0", "counter.js");`,
		`<main data-kit-component="tenant-counter@1.0.0"></main>`)
	filename := filepath.Join(directory, "counter.js")
	writeKitJSFile(t, directory, "counter.js", `kit.component("tenant-counter", { marker: "old" });`)
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	tags, _ := serveKitJSPage(t, tenant, "/")
	component, _ := findStagedJITTag(tags, "component", "tenant-counter")
	writeKitJSFile(t, directory, "counter.js", `kit.component("tenant-counter", { marker: "new" });`)
	changed, err := tenant.SourcesChanged()
	if err != nil || !changed {
		t.Fatalf("source edit: changed=%v err=%v", changed, err)
	}
	asset, ok := tenant.renderPlan().kitJSAsset(component.hash)
	if !ok || !bytes.Contains(asset.JavaScript, []byte(`marker: "old"`)) || bytes.Contains(asset.JavaScript, []byte(`marker: "new"`)) {
		t.Fatalf("published asset reread edited source %q: %+v", filename, asset)
	}
}

func TestRouterJITComponentEditChangesOnlyUsingRouteGraphAndComponentHash(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	writeKitJSFile(t, directory, "router.kitwork.js", `import { router } from "kitwork";
router.jitjs(true);
router.jitComponent("tenant-counter", "1.0.0", "components/counter.js");`)
	writeKitJSFile(t, directory, "index.kitwork.html", `<!doctype html><html><head><title>Custom</title></head><body>{{ @page }}</body></html>`)
	writeKitJSFile(t, directory, "notfound.kitwork.html", `<main>Not found</main>`)
	writeKitJSFile(t, directory, "page.kitwork.html", `<main data-kit-scope="ready: true">Home</main>`)
	writeKitJSFile(t, directory, "docs/page.kitwork.html", `<main data-kit-component="tenant-counter@1.0.0">Docs</main>`)
	componentFile := filepath.Join(directory, "components", "counter.js")
	writeKitJSFile(t, directory, "components/counter.js", `kit.component("tenant-counter", { marker: "first" });`)

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
	t.Cleanup(firstTenant.Close)
	if err := firstTenant.Run(); err != nil {
		t.Fatal(err)
	}
	firstHome, _ := serveKitJSPage(t, firstTenant, "/")
	firstDocs, _ := serveKitJSPage(t, firstTenant, "/docs")
	if got := stagedJITRoles(firstHome); got != "runtime,hydrate,graph" {
		t.Fatalf("first home roles=%s", got)
	}
	if got := stagedJITRoles(firstDocs); got != "runtime,hydrate,graph,component" {
		t.Fatalf("first docs roles=%s", got)
	}

	writeKitJSFile(t, directory, "components/counter.js", `kit.component("tenant-counter", { marker: "second" });`)
	secondGeneration, err := siteRuntime.PrepareGeneration()
	if err != nil {
		t.Fatal(err)
	}
	secondTenant := NewTenantWithRuntime(root, "localhost", appRuntime, siteRuntime, secondGeneration)
	t.Cleanup(secondTenant.Close)
	if err := secondTenant.Run(); err != nil {
		t.Fatal(err)
	}
	if err := secondTenant.ActivateGeneration(); err != nil {
		t.Fatal(err)
	}
	secondHome, _ := serveKitJSPage(t, secondTenant, "/")
	secondDocs, _ := serveKitJSPage(t, secondTenant, "/docs")
	if got := stagedJITRoles(secondHome); got != "runtime,hydrate,graph" {
		t.Fatalf("second home roles=%s", got)
	}
	if got := stagedJITRoles(secondDocs); got != "runtime,hydrate,graph,component" {
		t.Fatalf("second docs roles=%s", got)
	}

	for _, role := range []string{"runtime", "hydrate"} {
		first, firstOK := findStagedJITTag(firstDocs, role, role)
		second, secondOK := findStagedJITTag(secondDocs, role, role)
		if !firstOK || !secondOK || first.hash != second.hash {
			t.Fatalf("%s hash changed with tenant source: first=%+v second=%+v", role, first, second)
		}
	}
	firstComponent, _ := findStagedJITTag(firstDocs, "component", "tenant-counter")
	secondComponent, _ := findStagedJITTag(secondDocs, "component", "tenant-counter")
	if firstComponent.hash == "" || secondComponent.hash == "" || firstComponent.hash == secondComponent.hash {
		t.Fatalf("component source edit reused hash: first=%+v second=%+v", firstComponent, secondComponent)
	}
	firstGraph, _ := findStagedJITTag(firstDocs, "graph", "graph")
	secondGraph, _ := findStagedJITTag(secondDocs, "graph", "graph")
	if firstGraph.hash == "" || secondGraph.hash == "" || firstGraph.hash == secondGraph.hash {
		t.Fatalf("using route graph did not change: first=%+v second=%+v", firstGraph, secondGraph)
	}
	firstHomeGraph, _ := findStagedJITTag(firstHome, "graph", "graph")
	secondHomeGraph, _ := findStagedJITTag(secondHome, "graph", "graph")
	if firstHomeGraph.hash != secondHomeGraph.hash {
		t.Fatalf("unused route graph changed with catalog-only bytes: first=%+v second=%+v", firstHomeGraph, secondHomeGraph)
	}
	t.Logf("stable runtime=%s hydrate=%s unused-graph=%s; component %s -> %s; using-graph %s -> %s",
		firstDocs[0].hash, firstDocs[1].hash, firstHomeGraph.hash,
		firstComponent.hash, secondComponent.hash, firstGraph.hash, secondGraph.hash)
	if changed, err := firstTenant.SourcesChanged(); err != nil || !changed {
		t.Fatalf("first generation did not observe %q edit: changed=%v err=%v", componentFile, changed, err)
	}
}
