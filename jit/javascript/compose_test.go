package javascript

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestComposeMigratedComponentsIncludeExactDependencies(t *testing.T) {
	t.Parallel()
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}

	service := func(name string) ModuleID {
		return ModuleID{Kind: ServiceModule, Name: name, Version: "1.0.0"}
	}
	component := func(name string) ModuleID {
		return ModuleID{Kind: ComponentModule, Name: name, Version: "1.0.0"}
	}

	tests := []struct {
		name string
		want []ModuleID
	}{
		{name: "accordion", want: runtimeModuleIDs(component("accordion"))},
		{name: "announce", want: runtimeModuleIDs(service("announce"), component("announce"))},
		{name: "clipboard", want: runtimeModuleIDs(service("clipboard"), component("clipboard"))},
		{name: "combobox", want: runtimeModuleIDs(component("combobox"))},
		{name: "command-palette", want: runtimeModuleIDs(component("command-palette"))},
		{name: "counter", want: runtimeModuleIDs(component("counter"))},
		{name: "dialog", want: runtimeModuleIDs(component("dialog"))},
		{name: "drawer", want: runtimeModuleIDs(component("drawer"))},
		{name: "dropdown", want: runtimeModuleIDs(component("dropdown"))},
		{name: "menu", want: runtimeModuleIDs(component("menu"))},
		{name: "popover", want: runtimeModuleIDs(component("popover"))},
		{name: "progress-bar", want: runtimeModuleIDs(component("progress-bar"))},
		{name: "tabs", want: runtimeModuleIDs(component("tabs"))},
		{name: "theme", want: runtimeModuleIDs(service("storage"), component("theme"))},
		{name: "toast", want: runtimeModuleIDs(service("announce"), component("toast"))},
		{name: "tooltip", want: runtimeModuleIDs(component("tooltip"))},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			bundle, err := composer.ComposeHTML([]byte(`<div data-kit-component="` + test.name + `" data-kit-version="1.0.0"></div>`))
			if err != nil {
				t.Fatal(err)
			}
			assertBundle(t, composer.registry, bundle, test.want)
		})
	}
}

func TestComposeAllMigratedComponentsDeduplicatesSharedGraph(t *testing.T) {
	t.Parallel()
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := composer.ComposeHTML([]byte(`
<div data-kit-component="tooltip"></div>
<div data-kit-component="dialog"></div>
<div data-kit-component="theme"></div>
<div data-kit-component="accordion"></div>
<div data-kit-component="clipboard" data-kit-version="1.0.0"></div>
<div data-kit-component="announce"></div>
<div data-kit-component="combobox"></div>
<div data-kit-component="command-palette"></div>
<div data-kit-component="counter"></div>
<div data-kit-component="drawer"></div>
<div data-kit-component="dropdown"></div>
<div data-kit-component="menu"></div>
<div data-kit-component="popover"></div>
<div data-kit-component="progress-bar"></div>
<div data-kit-component="tabs"></div>
<div data-kit-component="toast"></div>
<div data-kit-component="theme" data-kit-version="1.0.0"></div>`))
	if err != nil {
		t.Fatal(err)
	}

	want := runtimeModuleIDs(
		ModuleID{Kind: ServiceModule, Name: "announce", Version: "1.0.0"},
		ModuleID{Kind: ServiceModule, Name: "clipboard", Version: "1.0.0"},
		ModuleID{Kind: ServiceModule, Name: "storage", Version: "1.0.0"},
		ModuleID{Kind: ComponentModule, Name: "accordion", Version: "1.0.0"},
		ModuleID{Kind: ComponentModule, Name: "announce", Version: "1.0.0"},
		ModuleID{Kind: ComponentModule, Name: "clipboard", Version: "1.0.0"},
		ModuleID{Kind: ComponentModule, Name: "combobox", Version: "1.0.0"},
		ModuleID{Kind: ComponentModule, Name: "command-palette", Version: "1.0.0"},
		ModuleID{Kind: ComponentModule, Name: "counter", Version: "1.0.0"},
		ModuleID{Kind: ComponentModule, Name: "dialog", Version: "1.0.0"},
		ModuleID{Kind: ComponentModule, Name: "drawer", Version: "1.0.0"},
		ModuleID{Kind: ComponentModule, Name: "dropdown", Version: "1.0.0"},
		ModuleID{Kind: ComponentModule, Name: "menu", Version: "1.0.0"},
		ModuleID{Kind: ComponentModule, Name: "popover", Version: "1.0.0"},
		ModuleID{Kind: ComponentModule, Name: "progress-bar", Version: "1.0.0"},
		ModuleID{Kind: ComponentModule, Name: "tabs", Version: "1.0.0"},
		ModuleID{Kind: ComponentModule, Name: "theme", Version: "1.0.0"},
		ModuleID{Kind: ComponentModule, Name: "toast", Version: "1.0.0"},
		ModuleID{Kind: ComponentModule, Name: "tooltip", Version: "1.0.0"},
	)
	assertBundle(t, composer.registry, bundle, want)
}

func TestComposeAcceptsSeparateVersionAndRejectsInlineVersion(t *testing.T) {
	t.Parallel()
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}

	separate, err := composer.ComposeHTML([]byte(`<div data-kit-component="theme" data-kit-version="1.0.0"></div>`))
	if err != nil {
		t.Fatal(err)
	}
	assertBundle(t, composer.registry, separate, runtimeModuleIDs(
		ModuleID{Kind: ServiceModule, Name: "storage", Version: "1.0.0"},
		ModuleID{Kind: ComponentModule, Name: "theme", Version: "1.0.0"},
	))
	if _, err := composer.ComposeHTML([]byte(`<div data-kit-component="theme@1.0.0"></div>`)); !errors.Is(err, ErrInvalidComponentUse) {
		t.Fatalf("inline version error=%v, want ErrInvalidComponentUse", err)
	}
}

func TestComposeGraphCanOrderAllDefaultServices(t *testing.T) {
	t.Parallel()
	defaults, err := DefaultRegistry()
	if err != nil {
		t.Fatal(err)
	}

	modules := make([]Module, 0, len(defaults.modules)+1)
	for _, module := range defaults.modules {
		modules = append(modules, module)
	}
	allServices := []ModuleID{
		{Kind: ServiceModule, Name: "announce", Version: "1.0.0"},
		{Kind: ServiceModule, Name: "clipboard", Version: "1.0.0"},
		{Kind: ServiceModule, Name: "cookie", Version: "1.0.0"},
		{Kind: ServiceModule, Name: "fullscreen", Version: "1.0.0"},
		{Kind: ServiceModule, Name: "navigation", Version: "1.0.0"},
		{Kind: ServiceModule, Name: "network", Version: "1.0.0"},
		{Kind: ServiceModule, Name: "request", Version: "1.0.0"},
		{Kind: ServiceModule, Name: "share", Version: "1.0.0"},
		{Kind: ServiceModule, Name: "storage", Version: "1.0.0"},
	}
	harness := ModuleID{Kind: ComponentModule, Name: "all-services-contract", Version: "1.0.0"}
	modules = append(modules, Module{
		ID:       harness,
		Path:     "component/all-services-contract/1.0.0.js",
		Requires: allServices,
		Source:   []byte(`kit.component("all-services-contract", {})`),
		Default:  true,
	})
	registry, err := NewRegistry(modules)
	if err != nil {
		t.Fatal(err)
	}
	composer, err := NewComposer(registry)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := composer.Compose([]ComponentRef{{Name: harness.Name}})
	if err != nil {
		t.Fatal(err)
	}

	want := make([]ModuleID, 0, len(registry.baseCore)+len(allServices)+2)
	want = append(want, registry.baseCore...)
	want = append(want, allServices...)
	want = append(want, harness, registry.boot)
	assertBundle(t, registry, bundle, want)
}

func TestComposeIsDeterministicAcrossDocumentOrderAndDuplicates(t *testing.T) {
	t.Parallel()
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	left, err := composer.ComposeHTML([]byte(`
<div data-kit-component="theme"></div>
<div data-kit-component="theme" data-kit-version="1.0.0"></div>
<div data-kit-component="theme"></div>`))
	if err != nil {
		t.Fatal(err)
	}
	right, err := composer.ComposeHTML([]byte(`
<div data-kit-component="theme" data-kit-version="1.0.0"></div>`))
	if err != nil {
		t.Fatal(err)
	}
	if left.ContentHash != right.ContentHash || !bytes.Equal(left.JavaScript, right.JavaScript) || !reflect.DeepEqual(left.Modules, right.Modules) {
		t.Fatalf("same graph was not deterministic:\nleft  %s %#v\nright %s %#v", left.ContentHash, left.Modules, right.ContentHash, right.Modules)
	}
}

func TestComposeUnknownComponentFailsClosed(t *testing.T) {
	t.Parallel()
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	_, err = composer.ComposeHTML([]byte(`<div data-kit-component="legacy-unmigrated"></div>`))
	if !errors.Is(err, ErrModuleNotFound) {
		t.Fatalf("got %v, want ErrModuleNotFound", err)
	}
}

func TestComposeNoComponentsProducesNoRuntime(t *testing.T) {
	t.Parallel()
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := composer.ComposeHTML([]byte(`<main class="p-6">Static</main>`))
	if err != nil {
		t.Fatal(err)
	}
	if !bundle.Empty() || bundle.ContentHash != "" || len(bundle.Modules) != 0 {
		t.Fatalf("static document produced bundle %#v", bundle)
	}
}

func TestComposeImplementedDirectiveProducesBaseRuntime(t *testing.T) {
	t.Parallel()
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{
		`<b data-kit-text="label">fallback</b>`,
		`<button data-kit-click:once="run()">Run</button>`,
		`<section data-kit-scope="count: 0;"></section>`,
	} {
		bundle, err := composer.ComposeHTML([]byte(source))
		if err != nil {
			t.Fatalf("ComposeHTML(%q): %v", source, err)
		}
		assertBundle(t, composer.registry, bundle, runtimeModuleIDs())
	}
}

func TestComposePositiveAppSelectsMorphAndDrive(t *testing.T) {
	t.Parallel()
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	app, err := composer.ComposeHTML([]byte(`<main data-kit-app="opaque@identity"></main>`))
	if err != nil {
		t.Fatal(err)
	}
	hydrate, err := composer.ComposeHTML([]byte(`<main data-kit-hydrate="compat"></main>`))
	if err != nil {
		t.Fatal(err)
	}
	want := driveModuleIDs()
	assertBundle(t, composer.registry, app, want)
	assertBundle(t, composer.registry, hydrate, want)
	if app.ContentHash != hydrate.ContentHash {
		t.Fatalf("app and hydrate compatibility markers selected different bundles: %s != %s", app.ContentHash, hydrate.ContentHash)
	}
}

func TestComposeAppScansUnionsRoutesDeterministically(t *testing.T) {
	t.Parallel()
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	left, err := ScanHTML([]byte(`<html data-kit-app="docs"><main data-kit-component="counter"></main></html>`))
	if err != nil {
		t.Fatal(err)
	}
	right, err := ScanHTML([]byte(`<html data-kit-app="docs"><main data-kit-component="theme" data-kit-version="1.0.0"></main></html>`))
	if err != nil {
		t.Fatal(err)
	}

	bundle, err := composer.ComposeAppScans([]ScanResult{left, right})
	if err != nil {
		t.Fatal(err)
	}
	reversed, err := composer.ComposeAppScans([]ScanResult{right, left})
	if err != nil {
		t.Fatal(err)
	}
	want := driveModuleIDs(
		ModuleID{Kind: ServiceModule, Name: "storage", Version: "1.0.0"},
		ModuleID{Kind: ComponentModule, Name: "counter", Version: "1.0.0"},
		ModuleID{Kind: ComponentModule, Name: "theme", Version: "1.0.0"},
	)
	assertBundle(t, composer.registry, bundle, want)
	if bundle.ContentHash != reversed.ContentHash || !bytes.Equal(bundle.JavaScript, reversed.JavaScript) || !reflect.DeepEqual(bundle.Modules, reversed.Modules) {
		t.Fatal("application union graph depends on route scan order")
	}
}

func TestComposeAppScansFailClosedAcrossIdentityAndVersionBoundaries(t *testing.T) {
	t.Parallel()
	registry := testRegistry(t, []Module{
		testModule(ComponentModule, "theme", "1.0.0", true),
		testModule(ComponentModule, "theme", "2.0.0", false),
	})
	composer, err := NewComposer(registry)
	if err != nil {
		t.Fatal(err)
	}
	appV1, err := ScanHTML([]byte(`<main data-kit-app="docs" data-kit-component="theme" data-kit-version="1.0.0"></main>`))
	if err != nil {
		t.Fatal(err)
	}
	appV2, err := ScanHTML([]byte(`<main data-kit-app="docs" data-kit-component="theme" data-kit-version="2.0.0"></main>`))
	if err != nil {
		t.Fatal(err)
	}
	otherApp, err := ScanHTML([]byte(`<main data-kit-app="admin" data-kit-component="theme" data-kit-version="1.0.0"></main>`))
	if err != nil {
		t.Fatal(err)
	}
	noApp, err := ScanHTML([]byte(`<main data-kit-component="theme" data-kit-version="1.0.0"></main>`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := composer.ComposeAppScans([]ScanResult{appV1, appV2}); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("version-conflicting app graph error=%v, want ErrVersionConflict", err)
	}
	if _, err := composer.ComposeAppScans([]ScanResult{appV1, otherApp}); !errors.Is(err, ErrInvalidAppUse) {
		t.Fatalf("mixed app graph error=%v, want ErrInvalidAppUse", err)
	}
	if _, err := composer.ComposeAppScans([]ScanResult{appV1, noApp}); !errors.Is(err, ErrInvalidAppUse) {
		t.Fatalf("missing app marker error=%v, want ErrInvalidAppUse", err)
	}
}

func TestComposeAppAndComponentPreserveExactDependencyGraph(t *testing.T) {
	t.Parallel()
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := composer.ComposeHTML([]byte(`<main data-kit-app="shop"><section data-kit-component="theme" data-kit-version="1.0.0"></section></main>`))
	if err != nil {
		t.Fatal(err)
	}
	assertBundle(t, composer.registry, bundle, driveModuleIDs(
		ModuleID{Kind: ServiceModule, Name: "storage", Version: "1.0.0"},
		ModuleID{Kind: ComponentModule, Name: "theme", Version: "1.0.0"},
	))
}

func TestComposeDisabledAppAndDriveOptOutAreStatic(t *testing.T) {
	t.Parallel()
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := composer.ComposeHTML([]byte(`<main data-kit-app="false"><a href="/native" data-kit-drive="false">Native</a></main>`))
	if err != nil {
		t.Fatal(err)
	}
	if !bundle.Empty() || len(bundle.Modules) != 0 || bundle.ContentHash != "" {
		t.Fatalf("disabled app/Drive opt-out produced bundle %#v", bundle)
	}
}

func TestComposeIgnoredSubtreeCannotSelectRuntimeOrUnknownComponent(t *testing.T) {
	t.Parallel()
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := composer.ComposeHTML([]byte(`<main data-kit-ignore data-kit-app><div data-kit-component="missing"><b data-kit-text="fake"></b></div></main>`))
	if err != nil {
		t.Fatal(err)
	}
	if !bundle.Empty() {
		t.Fatalf("ignored subtree produced bundle %#v", bundle)
	}
}

func TestComposeRejectsMultiplePositiveApps(t *testing.T) {
	t.Parallel()
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	_, err = composer.ComposeHTML([]byte(`<main data-kit-app="one"></main><aside data-kit-hydrate="two"></aside>`))
	if !errors.Is(err, ErrInvalidAppUse) {
		t.Fatalf("got %v, want ErrInvalidAppUse", err)
	}
}

func TestComposeUnknownExactComponentFailsClosed(t *testing.T) {
	t.Parallel()
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	_, err = composer.ComposeHTML([]byte(`<div data-kit-component="theme" data-kit-version="2.0.0"></div>`))
	if !errors.Is(err, ErrModuleNotFound) {
		t.Fatalf("got %v, want ErrModuleNotFound", err)
	}
}

func TestResolveRejectsTwoVersionsOfOneName(t *testing.T) {
	t.Parallel()
	registry := testRegistry(t, []Module{
		testModule(ComponentModule, "theme", "1.0.0", true),
		testModule(ComponentModule, "theme", "2.0.0", false),
	})
	_, err := registry.Resolve([]ComponentRef{
		{Name: "theme", Version: "1.0.0"},
		{Name: "theme", Version: "2.0.0"},
	})
	if !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("got %v, want ErrVersionConflict", err)
	}
}

func TestRegistryRejectsDependencyCycle(t *testing.T) {
	t.Parallel()
	a := ModuleID{Kind: ServiceModule, Name: "a", Version: "1.0.0"}
	b := ModuleID{Kind: ServiceModule, Name: "b", Version: "1.0.0"}
	modules := append(testCoreModules(),
		Module{ID: a, Path: "service/a/1.0.0.js", Source: []byte("a()"), Default: true, Requires: []ModuleID{b}},
		Module{ID: b, Path: "service/b/1.0.0.js", Source: []byte("b()"), Default: true, Requires: []ModuleID{a}},
	)
	_, err := NewRegistry(modules)
	if !errors.Is(err, ErrDependencyCycle) {
		t.Fatalf("got %v, want ErrDependencyCycle", err)
	}
}

func TestRegistryRejectsDuplicateModuleID(t *testing.T) {
	t.Parallel()
	module := testModule(ComponentModule, "dropdown", "1.0.0", true)
	modules := append(testCoreModules(), module, module)
	_, err := NewRegistry(modules)
	if !errors.Is(err, ErrInvalidModule) {
		t.Fatalf("got %v, want ErrInvalidModule", err)
	}
}

func TestRegistryRequiresExplicitDefaultVersion(t *testing.T) {
	t.Parallel()
	modules := append(testCoreModules(), testModule(ComponentModule, "dialog", "1.0.0", false))
	_, err := NewRegistry(modules)
	if !errors.Is(err, ErrInvalidModule) {
		t.Fatalf("got %v, want ErrInvalidModule", err)
	}
}

func TestRegistryRequiresEverySplitCoreFragment(t *testing.T) {
	t.Parallel()
	modules := testCoreModules()
	for index := range modules {
		if modules[index].ID.Name == "lifecycle" {
			modules = append(modules[:index], modules[index+1:]...)
			break
		}
	}
	_, err := NewRegistry(modules)
	if !errors.Is(err, ErrInvalidModule) {
		t.Fatalf("got %v, want ErrInvalidModule", err)
	}
}

func TestRegistryComposesBaseGraphWithoutNavigationCore(t *testing.T) {
	t.Parallel()
	counter := testModule(ComponentModule, "counter", "1.0.0", true)
	registry, err := NewRegistry(append(testCoreModulesWithoutNavigation(), counter))
	if err != nil {
		t.Fatal(err)
	}
	composer, err := NewComposer(registry)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := composer.Compose([]ComponentRef{{Name: counter.ID.Name}})
	if err != nil {
		t.Fatal(err)
	}

	want := make([]ModuleID, 0, len(baseCoreNames)+2)
	for _, name := range baseCoreNames {
		want = append(want, ModuleID{Kind: CoreModule, Name: name, Version: "1.0.0"})
	}
	want = append(want, counter.ID, ModuleID{Kind: CoreModule, Name: "boot", Version: "1.0.0"})
	assertBundle(t, registry, bundle, want)
}

func TestComposeDriveRequiresOptionalNavigationCore(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		modules  []Module
		wantCore string
	}{
		{
			name:     "missing morph",
			modules:  testCoreModulesWithoutNavigation(),
			wantCore: "core:morph",
		},
		{
			name: "missing drive",
			modules: append(testCoreModulesWithoutNavigation(), Module{
				ID:      ModuleID{Kind: CoreModule, Name: "morph", Version: "1.0.0"},
				Path:    "core/morph.js",
				Source:  []byte("morph()"),
				Default: true,
			}),
			wantCore: "core:drive",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			counter := testModule(ComponentModule, "counter", "1.0.0", true)
			registry, err := NewRegistry(append(test.modules, counter))
			if err != nil {
				t.Fatal(err)
			}
			composer, err := NewComposer(registry)
			if err != nil {
				t.Fatal(err)
			}
			_, err = composer.ComposeStandalone([]ComponentRef{{Name: counter.ID.Name}}, true)
			if !errors.Is(err, ErrInvalidModule) {
				t.Fatalf("got %v, want ErrInvalidModule", err)
			}
			if message := err.Error(); !strings.Contains(message, "compose Drive") || !strings.Contains(message, test.wantCore) {
				t.Fatalf("error %q does not identify the missing Drive core %q", message, test.wantCore)
			}
		})
	}
}

func TestRegistryRejectsLegacyMonolithicKernel(t *testing.T) {
	t.Parallel()
	modules := append(testCoreModules(), Module{
		ID:      ModuleID{Kind: CoreModule, Name: "kernel", Version: "1.0.0"},
		Path:    "core/kernel.js",
		Source:  []byte("kernel()"),
		Default: true,
	})
	_, err := NewRegistry(modules)
	if !errors.Is(err, ErrInvalidModule) {
		t.Fatalf("got %v, want ErrInvalidModule", err)
	}
}

func assertBundle(t *testing.T, registry *Registry, bundle Bundle, want []ModuleID) {
	t.Helper()
	if !reflect.DeepEqual(bundle.Modules, want) {
		t.Fatalf("bundle modules = %#v, want %#v", bundle.Modules, want)
	}
	if bundle.Empty() {
		t.Fatal("bundle unexpectedly reports empty")
	}

	parts := make([][]byte, 0, len(want))
	for _, id := range want {
		module, exists := registry.modules[id]
		if !exists {
			t.Fatalf("registry is missing expected module %s", id)
		}
		parts = append(parts, module.Source)
	}
	wantSource := bytes.Join(parts, []byte(";\n"))
	if !bytes.Equal(bundle.JavaScript, wantSource) {
		t.Fatal("bundle source does not match the ordered registry sources")
	}

	digest := sha256.Sum256(bundle.JavaScript)
	wantHash := hex.EncodeToString(digest[:])
	if bundle.ContentHash != wantHash {
		t.Fatalf("bundle content hash = %q, want %q", bundle.ContentHash, wantHash)
	}
}

func testRegistry(t *testing.T, modules []Module) *Registry {
	t.Helper()
	registry, err := NewRegistry(append(testCoreModules(), modules...))
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

func testCoreModules() []Module {
	modules := make([]Module, 0, len(baseCoreNames)+3)
	for _, name := range append(append([]string(nil), baseCoreNames[:]...), "morph", "drive", "boot") {
		modules = append(modules, Module{
			ID:      ModuleID{Kind: CoreModule, Name: name, Version: "1.0.0"},
			Path:    "core/" + name + ".js",
			Source:  []byte(name + "()"),
			Default: true,
		})
	}
	return modules
}

func testCoreModulesWithoutNavigation() []Module {
	modules := make([]Module, 0, len(baseCoreNames)+1)
	for _, name := range append(append([]string(nil), baseCoreNames[:]...), "boot") {
		modules = append(modules, Module{
			ID:      ModuleID{Kind: CoreModule, Name: name, Version: "1.0.0"},
			Path:    "core/" + name + ".js",
			Source:  []byte(name + "()"),
			Default: true,
		})
	}
	return modules
}

func runtimeModuleIDs(selected ...ModuleID) []ModuleID {
	ids := make([]ModuleID, 0, len(baseCoreNames)+len(selected)+1)
	for _, name := range baseCoreNames {
		ids = append(ids, ModuleID{Kind: CoreModule, Name: name, Version: embeddedCoreVersion})
	}
	ids = append(ids, selected...)
	ids = append(ids, ModuleID{Kind: CoreModule, Name: "boot", Version: embeddedCoreVersion})
	return ids
}

func driveModuleIDs(selected ...ModuleID) []ModuleID {
	ids := make([]ModuleID, 0, len(baseCoreNames)+len(selected)+3)
	for _, name := range baseCoreNames {
		ids = append(ids, ModuleID{Kind: CoreModule, Name: name, Version: embeddedCoreVersion})
	}
	ids = append(ids,
		ModuleID{Kind: CoreModule, Name: "morph", Version: embeddedCoreVersion},
		ModuleID{Kind: CoreModule, Name: "drive", Version: embeddedCoreVersion},
	)
	ids = append(ids, selected...)
	ids = append(ids, ModuleID{Kind: CoreModule, Name: "boot", Version: embeddedCoreVersion})
	return ids
}

func testModule(kind ModuleKind, name, version string, isDefault bool) Module {
	return Module{
		ID:      ModuleID{Kind: kind, Name: name, Version: version},
		Path:    string(kind) + "/" + name + "/" + version + ".js",
		Source:  []byte(name + "_" + version + "()"),
		Default: isDefault,
	}
}
