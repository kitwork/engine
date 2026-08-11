package javascript

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var canonicalComponentNames = []string{
	"accordion",
	"announce",
	"clipboard",
	"combobox",
	"command-palette",
	"counter",
	"dialog",
	"drawer",
	"dropdown",
	"menu",
	"popover",
	"progress-bar",
	"tabs",
	"theme",
	"toast",
	"tooltip",
}

func TestDefaultRegistryPublishesCanonicalModules(t *testing.T) {
	t.Parallel()
	registry, err := DefaultRegistry()
	if err != nil {
		t.Fatal(err)
	}

	want := map[ModuleID]string{
		{Kind: CoreModule, Name: "global", Version: "0.1.0-preview.1"}:     "core/global.js",
		{Kind: CoreModule, Name: "expression", Version: "0.1.0-preview.1"}: "core/expression.js",
		{Kind: CoreModule, Name: "component", Version: "0.1.0-preview.1"}:  "core/component.js",
		{Kind: CoreModule, Name: "dom", Version: "0.1.0-preview.1"}:        "core/dom.js",
		{Kind: CoreModule, Name: "lifecycle", Version: "0.1.0-preview.1"}:  "core/lifecycle.js",
		{Kind: CoreModule, Name: "morph", Version: "0.1.0-preview.1"}:      "core/morph.js",
		{Kind: CoreModule, Name: "drive", Version: "0.1.0-preview.1"}:      "core/drive.js",
		{Kind: CoreModule, Name: "boot", Version: "0.1.0-preview.1"}:       "core/boot.js",
		{Kind: ServiceModule, Name: "announce", Version: "1.0.0"}:          "service/announce/1.0.0.js",
		{Kind: ServiceModule, Name: "clipboard", Version: "1.0.0"}:         "service/clipboard/1.0.0.js",
		{Kind: ServiceModule, Name: "cookie", Version: "1.0.0"}:            "service/cookie/1.0.0.js",
		{Kind: ServiceModule, Name: "fullscreen", Version: "1.0.0"}:        "service/fullscreen/1.0.0.js",
		{Kind: ServiceModule, Name: "navigation", Version: "1.0.0"}:        "service/navigation/1.0.0.js",
		{Kind: ServiceModule, Name: "network", Version: "1.0.0"}:           "service/network/1.0.0.js",
		{Kind: ServiceModule, Name: "request", Version: "1.0.0"}:           "service/request/1.0.0.js",
		{Kind: ServiceModule, Name: "share", Version: "1.0.0"}:             "service/share/1.0.0.js",
		{Kind: ServiceModule, Name: "storage", Version: "1.0.0"}:           "service/storage/1.0.0.js",
		{Kind: ComponentModule, Name: "accordion", Version: "1.0.0"}:       "component/accordion/1.0.0.js",
		{Kind: ComponentModule, Name: "announce", Version: "1.0.0"}:        "component/announce/1.0.0.js",
		{Kind: ComponentModule, Name: "clipboard", Version: "1.0.0"}:       "component/clipboard/1.0.0.js",
		{Kind: ComponentModule, Name: "combobox", Version: "1.0.0"}:        "component/combobox/1.0.0.js",
		{Kind: ComponentModule, Name: "command-palette", Version: "1.0.0"}: "component/command-palette/1.0.0.js",
		{Kind: ComponentModule, Name: "counter", Version: "1.0.0"}:         "component/counter/1.0.0.js",
		{Kind: ComponentModule, Name: "dialog", Version: "1.0.0"}:          "component/dialog/1.0.0.js",
		{Kind: ComponentModule, Name: "drawer", Version: "1.0.0"}:          "component/drawer/1.0.0.js",
		{Kind: ComponentModule, Name: "dropdown", Version: "1.0.0"}:        "component/dropdown/1.0.0.js",
		{Kind: ComponentModule, Name: "menu", Version: "1.0.0"}:            "component/menu/1.0.0.js",
		{Kind: ComponentModule, Name: "popover", Version: "1.0.0"}:         "component/popover/1.0.0.js",
		{Kind: ComponentModule, Name: "progress-bar", Version: "1.0.0"}:    "component/progress-bar/1.0.0.js",
		{Kind: ComponentModule, Name: "tabs", Version: "1.0.0"}:            "component/tabs/1.0.0.js",
		{Kind: ComponentModule, Name: "theme", Version: "1.0.0"}:           "component/theme/1.0.0.js",
		{Kind: ComponentModule, Name: "toast", Version: "1.0.0"}:           "component/toast/1.0.0.js",
		{Kind: ComponentModule, Name: "tooltip", Version: "1.0.0"}:         "component/tooltip/1.0.0.js",
	}
	if len(registry.modules) != len(want) {
		t.Fatalf("default registry publishes %d modules, want %d", len(registry.modules), len(want))
	}
	for id, path := range want {
		module, exists := registry.modules[id]
		if !exists {
			t.Errorf("default registry is missing %s", id)
			continue
		}
		if module.Path != path {
			t.Errorf("%s path = %q, want %q", id, module.Path, path)
		}
	}
}

func TestDefaultServiceContracts(t *testing.T) {
	t.Parallel()
	registry, err := DefaultRegistry()
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		required []string
	}{
		{name: "announce", required: []string{"say: say", "polite:", "assertive:", "clear: clear"}},
		{name: "clipboard", required: []string{"writeText: writeText", "readText: readText"}},
		{name: "cookie", required: []string{"get: get", "set: set", "remove: remove", "has: has"}},
		{name: "fullscreen", required: []string{"request: request", "exit: exit", "active: active"}},
		{name: "navigation", required: []string{"back: back", "forward: forward", "reload: reload"}},
		{name: "network", required: []string{"get online()", "snapshot: snapshot", "subscribe: subscribe"}},
		{name: "request", required: []string{"request: request", "get: get", "post: post", "submit: submit", "abort: abort"}},
		{name: "share", required: []string{"open: open", "canShare: canShare", "kit.clipboard.writeText"}},
		{name: "storage", required: []string{"get: get", "set: set", "remove: remove", "has: has", "clear: clear"}},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			id := ModuleID{Kind: ServiceModule, Name: test.name, Version: "1.0.0"}
			module, exists := registry.modules[id]
			if !exists {
				t.Fatalf("default registry is missing %s", id)
			}
			source := string(module.Source)
			common := []string{
				"KitJS service: " + test.name + "@1.0.0",
				`var version = "1.0.0"`,
				`OWN.call(kit, "component")`,
				`OWN.call(kit, "` + test.name + `")`,
				"Object.freeze(",
				`Object.defineProperty(kit, "` + test.name + `"`,
				"configurable: false",
				"writable: false",
			}
			for _, required := range append(common, test.required...) {
				if !strings.Contains(source, required) {
					t.Errorf("%s is missing contract marker %q", id, required)
				}
			}
			for _, forbidden := range []string{"kit.component(", "data-kit-", "innerHTML"} {
				if strings.Contains(source, forbidden) {
					t.Errorf("%s contains component/markup concern %q", id, forbidden)
				}
			}
		})
	}
}

func TestDefaultComponentContracts(t *testing.T) {
	t.Parallel()
	registry, err := DefaultRegistry()
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		required  []string
		forbidden []string
	}{
		{
			name:      "counter",
			required:  []string{`kit.component("counter"`, `count: 0`},
			forbidden: []string{"init()", "addEventListener", "document.", "this.$host", "this.$refs", "innerHTML"},
		},
		{
			name: "clipboard",
			required: []string{
				`kit.component("clipboard"`, `copied: false`, `error: ""`, `async copy(value)`,
				"kit.clipboard.writeText", "nextRevision", "reset()",
			},
			forbidden: []string{"kit.clipboard =", "document.", "innerHTML"},
		},
		{
			name: "progress-bar",
			required: []string{
				`kit.component("progress-bar"`, `status: "idle"`, "get hidden()", "get width()",
				"start()", "set(value)", "inc(amount)", "done()", "reset()",
			},
			forbidden: []string{"kit.progress", "document.", "innerHTML"},
		},
		{
			name:      "dropdown",
			required:  []string{`kit.component("dropdown"`, `open: false`},
			forbidden: []string{"init()", "addEventListener", "document.", "this.$host", "this.$refs", "queueMicrotask", "toggle()", "close()", "innerHTML"},
		},
		{
			name:      "popover",
			required:  []string{`kit.component("popover"`, `open: false`},
			forbidden: []string{"init()", "addEventListener", "document.", "this.$host", "this.$refs", "queueMicrotask", "toggle()", "show()", "close()", "innerHTML"},
		},
		{
			name: "theme",
			required: []string{
				`kit.component("theme"`, `mode: "system"`, "get resolved()", "async init()", "kit.storage.get", "set(mode)", "toggle()", "kit.storage.set",
			},
			forbidden: []string{"kit.theme", "mount", "unmount", "WeakMap", "document.", "innerHTML"},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			id := ModuleID{Kind: ComponentModule, Name: test.name, Version: "1.0.0"}
			module, exists := registry.modules[id]
			if !exists {
				t.Fatalf("default registry is missing %s", id)
			}
			source := string(module.Source)
			for _, required := range append([]string{"KitJS component: " + test.name + "@1.0.0"}, test.required...) {
				if !strings.Contains(source, required) {
					t.Errorf("%s is missing contract marker %q", id, required)
				}
			}
			for _, forbidden := range append(test.forbidden, "data-kit-") {
				if strings.Contains(source, forbidden) {
					t.Errorf("%s contains service/markup concern %q", id, forbidden)
				}
			}
		})
	}
}

func TestCanonicalComponentSourcesRegisterOnceAndStayHeadless(t *testing.T) {
	t.Parallel()
	registry, err := DefaultRegistry()
	if err != nil {
		t.Fatal(err)
	}

	for _, name := range canonicalComponentNames {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			id := ModuleID{Kind: ComponentModule, Name: name, Version: "1.0.0"}
			module, exists := registry.modules[id]
			if !exists {
				t.Fatalf("default registry is missing %s", id)
			}
			source := string(module.Source)
			for _, required := range []string{
				"KitJS component: " + name + "@1.0.0",
			} {
				if !strings.Contains(source, required) {
					t.Errorf("%s is missing contract marker %q", id, required)
				}
			}
			registration := `kit.component("` + name + `",`
			if count := strings.Count(source, registration); count != 1 {
				t.Errorf("%s registers %d times with %q, want exactly one", id, count, registration)
			}
			for _, forbidden := range []string{
				"window.kit = window.kit ||",
				"global.kit = global.kit ||",
				"Object.prototype.hasOwnProperty",
				"OWN.call(kit",
				"KitJS core must be loaded before component:",
				`kit.component("` + name + `")) return`,
				"innerHTML",
				"insertAdjacentHTML",
				"document.write",
				"kit.render",
				"mount:",
				"unmount:",
				"showModal(",
				"showPopover(",
			} {
				if strings.Contains(source, forbidden) {
					t.Errorf("%s contains forbidden implementation concern %q", id, forbidden)
				}
			}
		})
	}
}

func TestCanonicalComponentExamplesAreCustomTailwindHTML(t *testing.T) {
	t.Parallel()
	registry, err := DefaultRegistry()
	if err != nil {
		t.Fatal(err)
	}
	composer, err := NewComposer(registry)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range canonicalComponentNames {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join("component", name, "example.html")
			source, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			html := strings.ToLower(string(source))
			if !strings.Contains(html, `data-kit-component="`+name) {
				t.Errorf("%s does not demonstrate component %q", path, name)
			}
			for _, script := range []string{
				`src="../../core/global.js"`,
				`src="../../core/expression.js"`,
				`src="../../core/component.js"`,
				`src="../../core/dom.js"`,
				`src="../../core/lifecycle.js"`,
				`src="./1.0.0.js"`,
				`src="../../core/boot.js"`,
			} {
				if !strings.Contains(html, script) {
					t.Errorf("%s is missing classic-script dependency %q", path, script)
				}
			}
			module := registry.modules[ModuleID{Kind: ComponentModule, Name: name, Version: "1.0.0"}]
			for _, dependency := range module.Requires {
				if dependency.Kind != ServiceModule {
					continue
				}
				script := `src="../../service/` + dependency.Name + `/` + dependency.Version + `.js"`
				if !strings.Contains(html, script) {
					t.Errorf("%s is missing exact dependency %s", path, dependency)
				}
			}
			for _, forbidden := range []string{
				"<dialog",
				"<details",
				"<style",
				"tailwindcss.com",
				" showpopover",
				`type="module"`,
			} {
				if strings.Contains(html, forbidden) {
					t.Errorf("%s contains forbidden browser/CSS primitive %q", path, forbidden)
				}
			}
			bundle, err := composer.ComposeHTML(source)
			if err != nil {
				t.Fatalf("compose %s: %v", path, err)
			}
			wantComponent := ModuleID{Kind: ComponentModule, Name: name, Version: "1.0.0"}
			found := false
			for _, id := range bundle.Modules {
				if id == wantComponent {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("%s did not select %s", path, wantComponent)
			}
		})
	}
}

func TestCanonicalBrowserSourcesNeedNoDynamicLoader(t *testing.T) {
	t.Parallel()
	registry, err := DefaultRegistry()
	if err != nil {
		t.Fatal(err)
	}

	for id, module := range registry.modules {
		if bytes.Contains(module.Source, []byte("\r\n")) {
			t.Errorf("%s contains CRLF; content hashes require canonical LF source", id)
		}
		source := string(module.Source)
		for _, forbidden := range []string{
			"eval(",
			"new Function",
			"module.exports",
			"require(",
			"KitworkRuntime",
		} {
			if strings.Contains(source, forbidden) {
				t.Errorf("%s contains forbidden browser source %q", id, forbidden)
			}
		}
	}
}

func TestCorePublicSurfaceKeepsLifecyclePrivate(t *testing.T) {
	t.Parallel()

	files := []string{
		"core/global.js",
		"core/expression.js",
		"core/component.js",
		"core/dom.js",
		"core/lifecycle.js",
		"core/morph.js",
		"core/drive.js",
		"core/boot.js",
	}
	var combined strings.Builder
	for _, path := range files {
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		combined.Write(source)
		combined.WriteByte('\n')
	}
	source := combined.String()
	for _, forbidden := range []string{
		"kit.start =",
		"kit.destroy =",
		"kit.use =",
		"kit.mount =",
		"kit.unmount =",
	} {
		if strings.Contains(source, forbidden) {
			t.Errorf("core exposes forbidden public control %q", forbidden)
		}
	}
	for _, required := range []string{
		"kit.component = function (name, definition)",
		"arguments.length !== 2",
		"must be a plain object",
		"core.startRuntime = startRuntime",
		"core.destroyRuntime = destroyRuntime",
		"startRuntime(document.documentElement)",
		"delete kit.__kitwork_core__",
	} {
		if !strings.Contains(source, required) {
			t.Errorf("core is missing private lifecycle/public surface marker %q", required)
		}
	}
}
