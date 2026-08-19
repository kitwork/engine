package render

import (
	"regexp"
	"strings"
	"testing"

	kitjavascript "github.com/kitwork/engine/jit/javascript"
	"github.com/kitwork/engine/value"
)

var preparedKitJSTagPattern = regexp.MustCompile(`data-kitwork-jit="?([a-z]+)"? data-kitwork-hash="?([0-9a-f]{64})"? src="?/jit/([0-9a-f]{64})\.([A-Za-z0-9._-]+)\.js"?`)

func TestKitJSScopePreparesRuntimeWithoutActivationMarker(t *testing.T) {
	root := t.TempDir()
	mkfile(t, root, "index.kitwork.html", `<html><head><title>Counter</title></head><body>{{ @page }}</body></html>`)
	mkfile(t, root, "page.kitwork.html", `<section data-kit-scope="count: 0"><button data-kit-click="count = count + 1">Add</button><output data-kit-text="count">0</output></section>`)
	assets, err := kitjavascript.NewDefaultAssetStore()
	if err != nil {
		t.Fatal(err)
	}
	defer assets.Close()

	prepared := New(Config{Base: root, Directory: ".", KitJSAssets: assets}).Prepare()
	if err := prepared.PreparationError(); err != nil {
		t.Fatal(err)
	}
	html := prepared.Bind(value.New(map[string]any{})).String()
	matches := preparedKitJSTagPattern.FindAllStringSubmatch(html, -1)
	if len(matches) != 2 || matches[0][1] != "runtime" || matches[1][1] != "graph" {
		t.Fatalf("staged runtime/graph tags missing or out of order:\n%s", html)
	}
	for _, match := range matches {
		if match[2] != match[3] || match[1] != match[4] {
			t.Fatalf("content-addressed staged tag is inconsistent: %v", match)
		}
	}
	if strings.Contains(html, "data-kit-app") || strings.Contains(html, "data-kit-hydrate") ||
		strings.Contains(html, "data-kitwork-plan") || strings.Contains(html, "data-kitwork-runtime") {
		t.Fatalf("ordinary component render invented an activation marker:\n%s", html)
	}
	runtimeAsset, ok := assets.Lookup(matches[0][2])
	if !ok {
		t.Fatal("prepared runtime is missing from the generation asset store")
	}
	graphAsset, ok := assets.Lookup(matches[1][2])
	if !ok {
		t.Fatal("prepared graph is missing from the generation asset store")
	}
	if !strings.Contains(string(runtimeAsset.JavaScript), "data-kit-scope") ||
		!strings.Contains(string(graphAsset.JavaScript), "KitJS: boot loaded out of order") {
		t.Fatal("staged delivery omitted scope support or automatic boot")
	}
	if strings.Contains(string(runtimeAsset.JavaScript), "KitJS: Drive fragment loaded out of order") ||
		strings.Contains(string(graphAsset.JavaScript), "KitJS: Drive fragment loaded out of order") {
		t.Fatal("plain render composition unexpectedly selected Hydrate/Drive")
	}
}

func TestKitJSTemplateTokenPreparation(t *testing.T) {
	for _, source := range []string{
		`<body {{ if section == "docs" }}aria-current="page" {{ end }}></body>`,
		`<div {{ if enabled }} data-kit-component="dialog@1.0.0" {{ end }}></div>`,
		`<div data-kit-component="dialog@1.0.0"{{ if enabled }} data-ready="yes"{{ end }}></div>`,
		`<div {{ if enabled }}data-kit-click="run()"{{ else }}data-ready="no"{{ end }}></div>`,
		`<meta name="description" content="{{ description }}">`,
		`<script>window.__label = "{{ label }}";</script>`,
		`<code>&lt;div data-kit-{{ example }}&gt;</code>`,
	} {
		if hasDynamicKitJSAttribute(source) {
			t.Fatalf("static component/control-token source flagged dynamic: %s", source)
		}
		scanSource, err := kitJSStaticScanSource(source)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := kitjavascript.ScanHTML(scanSource); err != nil {
			t.Fatalf("semantic scan rejected sanitized template %q: %v", source, err)
		}
	}

	conditional, err := kitJSStaticScanSource(`<div {{ if enabled }} data-kit-component="dialog@1.0.0" {{ end }}></div>`)
	if err != nil {
		t.Fatal(err)
	}
	result, err := kitjavascript.ScanHTML(conditional)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Components) != 1 || result.Components[0].Name != "dialog" {
		t.Fatalf("conditional static component scan = %#v, want safe-superset dialog selection", result.Components)
	}

	for _, source := range []string{
		`<main data-kit-component="{{ component }}"></main>`,
		`<span data-kit-text={{ expression }}></span>`,
		`<button data-kit-click="run({{ id }})"></button>`,
		`<div data-kit-{{ directive }}="run()"></div>`,
		`<div data-kit-{{ if enabled }}click{{ end }}="run()"></div>`,
		`<{{ tag }} data-kit-click="run()">Run</{{ tag }}>`,
		`<div {{ attributes }}></div>`,
		`<div {{ raw(attributes) }}></div>`,
		`<div {{ raw (attributes) }}></div>`,
		`<div title={{ title }}></div>`,
		`{{ raw(fragment) }}`,
		`{{ raw (fragment) }}`,
		`<script>const close = "</scr{{ suffix }}>";</script>`,
	} {
		if !hasDynamicKitJSAttribute(source) {
			t.Fatalf("dynamic KitJS attribute was not rejected: %s", source)
		}
		if _, err := kitJSStaticScanSource(source); err == nil {
			t.Fatalf("unsafe template source was sanitized instead of rejected: %s", source)
		}
	}
}

func TestKitJSOptInPreparationRejectsUnscannableMarkupAndReservedAttributes(t *testing.T) {
	tests := []struct {
		name string
		page string
		want string
	}{
		{name: "dynamic attribute name", page: `<div data-kit-{{ directive }}="run()"></div>`, want: "statically scannable HTML"},
		{name: "raw attribute fragment", page: `<div {{ raw(attributes) }}></div>`, want: "statically scannable HTML"},
		{name: "raw element fragment", page: `{{ raw(fragment) }}`, want: "statically scannable HTML"},
		{name: "unsupported legacy directive", page: `<div data-kit-if="open"></div>`, want: "unsupported reserved attribute"},
		{name: "unknown reserved directive", page: `<div data-kit-surprise="value"></div>`, want: "unsupported reserved attribute"},
		{name: "unknown event modifier", page: `<button data-kit-click:typo="run()"></button>`, want: "unsupported event modifier"},
		{name: "authored engine namespace", page: `<div data-kitwork-action="toggle"></div>`, want: "engine-emitted namespace"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			mkfile(t, root, "index.kitwork.html", `<html><head></head><body>{{ @page }}</body></html>`)
			mkfile(t, root, "page.kitwork.html", test.page)
			assets, err := kitjavascript.NewDefaultAssetStore()
			if err != nil {
				t.Fatal(err)
			}
			defer assets.Close()

			prepared := New(Config{Base: root, Directory: ".", KitJSAssets: assets}).Prepare()
			err = prepared.PreparationError()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("PreparationError() = %v, want substring %q", err, test.want)
			}
		})
	}
}
