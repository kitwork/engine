package javascript

import (
	"errors"
	"strconv"
	"strings"
	"testing"
)

func TestScanComponentsReadsInlineAndLegacyExactVersions(t *testing.T) {
	t.Parallel()
	source := []byte(`<!doctype html>
<html data-kit-component="app@1.1.0" data-kit-as="$app">
  <div DATA-KIT-AS='$theme' DATA-KIT-COMPONENT='theme' DATA-KIT-VERSION='2.0.0'></div>
  <div data-kit-component="progress-bar@1.2.3-rc.1+build.7"></div>
  <x-panel data-kit-component=dialog></x-panel>
</html>`)

	scan, err := ScanHTML(source)
	if err != nil {
		t.Fatal(err)
	}
	got := scan.Components
	want := []ComponentRef{
		{Name: "app", Version: "1.1.0", Alias: "$app"},
		{Name: "theme", Version: "2.0.0", Alias: "$theme"},
		{Name: "progress-bar", Version: "1.2.3-rc.1+build.7"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d components, want %d: %#v", len(got), len(want), got)
	}
	for index := range want {
		if got[index].Name != want[index].Name || got[index].Version != want[index].Version || got[index].Alias != want[index].Alias {
			t.Errorf("component %d = %#v, want %#v", index, got[index], want[index])
		}
		if got[index].Offset <= 0 {
			t.Errorf("component %d has invalid source offset %d", index, got[index].Offset)
		}
	}
	if len(scan.LocalComponents) != 1 || scan.LocalComponents[0].Name != "dialog" || scan.LocalComponents[0].Version != "" {
		t.Fatalf("client components = %#v, want one unversioned dialog", scan.LocalComponents)
	}
}

func TestScanComponentsIgnoresCommentsAndRawText(t *testing.T) {
	t.Parallel()
	source := []byte(`
<!-- <div data-kit-component="comment"></div> -->
<script>const example = '<div data-kit-component="script"></div>';</script>
<style>.x::after { content: '<i data-kit-component="style">'; }</style>
<textarea><div data-kit-component="textarea"></div></textarea>
<template><div data-kit-component="template-child@1.0.0"></div></template>
`)

	got, err := ScanComponents(source)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "template-child" {
		t.Fatalf("got %#v, want only template-child", got)
	}
}

func TestScanHTMLDetectsImplementedRuntimeDirectivesAndEvents(t *testing.T) {
	t.Parallel()
	tests := []string{
		`<b data-kit-text="label"></b>`,
		`<div data-kit-show="open"></div>`,
		`<div data-kit-class="tone"></div>`,
		`<div data-kit-bind="aria-expanded: open;"></div>`,
		`<div data-kit-style="width: progress + '%'; opacity: open ? 1 : 0;"></div>`,
		`<input data-kit-model="query">`,
		`<section data-kit-scope="count: 0;"></section>`,
		`<template data-kit-if="open"><p data-kit-text="label"></p></template>`,
		`<template data-kit-for="item, index of items" data-kit-key="item.id"><p data-kit-text="item.name"></p></template>`,
		`<button data-kit-click="run()"></button>`,
		`<button data-kit-dblclick="run()"></button>`,
		`<form data-kit-submit:prevent="save()"></form>`,
		`<input data-kit-input:debounce(250)="search()">`,
		`<input data-kit-change="change()">`,
		`<input data-kit-keydown:escape="close()">`,
		`<input data-kit-keyup="release()">`,
		`<button data-kit-pointerdown="down()"></button>`,
		`<button data-kit-pointerup="up()"></button>`,
		`<input data-kit-focusin="enter()">`,
		`<input data-kit-focusout="leave()">`,
	}
	for _, source := range tests {
		result, err := ScanHTML([]byte(source))
		if err != nil {
			t.Fatalf("ScanHTML(%q): %v", source, err)
		}
		if !result.NeedsRuntime || len(result.Components) != 0 {
			t.Errorf("ScanHTML(%q) = %#v, want runtime-only use", source, result)
		}
	}
}

func TestScanHTMLResolvesAuthoredServiceCommandsThroughAppAliasGrants(t *testing.T) {
	t.Parallel()
	source := []byte(`<button data-kit-click="$app.clipboard.writeText(code); $app.appearance.toggle(); $app.navigation.back()">Run</button>
<main data-kit-component="app" data-kit-version="1.0.0" data-kit-as="$app"></main>`)
	result, err := ScanHTML(source)
	if err != nil {
		t.Fatal(err)
	}
	if !result.NeedsRuntime || len(result.Components) != 1 || result.Components[0].Name != "app" {
		t.Fatalf("ScanHTML() = %#v, want the granted app component", result)
	}
}

func TestScanHTMLVersionsAppProgressCommandsAndLoaderBindings(t *testing.T) {
	t.Parallel()
	for _, source := range []string{
		`<html data-kit-component="app" data-kit-version="1.1.0" data-kit-as="$app"><button data-kit-click="$app.progress.start('load')"></button><div data-kit-show="$app.loader.visible"></div></html>`,
		`<html data-kit-component="app@1.1.0" data-kit-as="$app"><div data-kit-style="width: $app.loader.value === null ? '12%' : $app.loader.value + '%';"></div></html>`,
	} {
		result, err := ScanHTML([]byte(source))
		if err != nil {
			t.Errorf("ScanHTML(%q) = %v", source, err)
			continue
		}
		if len(result.Components) != 1 || result.Components[0].Name != "app" {
			t.Errorf("ScanHTML(%q) = %#v", source, result)
		}
	}

	for _, source := range []string{
		`<html data-kit-component="app" data-kit-version="1.0.0" data-kit-as="$app"><button data-kit-click="$app.progress.start('load')"></button></html>`,
		`<html data-kit-component="app" data-kit-version="1.0.0" data-kit-as="$app"><div data-kit-show="$app.loader.visible"></div></html>`,
		`<html data-kit-component="app" data-kit-version="1.2.0" data-kit-as="$app"><div data-kit-show="$app.loader.visible"></div></html>`,
	} {
		if _, err := ScanHTML([]byte(source)); !errors.Is(err, ErrInvalidExpressionUse) {
			t.Errorf("ScanHTML(%q) error = %v, want ErrInvalidExpressionUse", source, err)
		}
	}
}

func TestScanHTMLRejectsMissingAuthoredServiceAliasOrGrant(t *testing.T) {
	t.Parallel()
	for _, source := range []string{
		`<button data-kit-click="$missing.clipboard.writeText(code)"></button>`,
		`<button data-kit-click="$dialog.clipboard.writeText(code)"></button><section data-kit-component="dialog@1.0.0" data-kit-as="$dialog"></section>`,
		`<button data-kit-click="$app.clipboard.writeText(code)"></button><section data-kit-component="dialog@1.0.0" data-kit-as="$app"></section>`,
		`<button data-kit-click="$other.clipboard.writeText(code)"></button><main data-kit-component="app@1.0.0" data-kit-as="$app"></main>`,
		`<button data-kit-click="$app.request.get('/api')"></button><main data-kit-component="app@1.0.0" data-kit-as="$app"></main>`,
	} {
		if _, err := ScanHTML([]byte(source)); !errors.Is(err, ErrInvalidExpressionUse) {
			t.Errorf("ScanHTML(%q) error = %v, want ErrInvalidExpressionUse", source, err)
		}
	}
}

func TestScanHTMLRejectsAppScopeServiceNamespaceCollisions(t *testing.T) {
	t.Parallel()
	for _, field := range []string{
		"announce", "appearance", "clipboard", "cookie", "fullscreen", "navigation",
		"progress", "share", "storage",
	} {
		source := `<main data-kit-component="app@1.1.0" data-kit-as="$app" data-kit-scope="` + field + `: null"></main>`
		if _, err := ScanHTML([]byte(source)); !errors.Is(err, ErrInvalidScopeUse) {
			t.Errorf("ScanHTML(app scope field %q) error = %v, want ErrInvalidScopeUse", field, err)
		}
	}

	for _, source := range []string{
		`<main data-kit-component="app@1.0.0" data-kit-as="$app" data-kit-scope="storageKey: null; profile: {storage: true}"></main>`,
		`<main data-kit-component="app@1.0.0" data-kit-as="$other" data-kit-scope="storage: null"></main>`,
		`<main data-kit-component="dialog@1.0.0" data-kit-as="$app" data-kit-scope="storage: null"></main>`,
	} {
		if _, err := ScanHTML([]byte(source)); err != nil {
			t.Errorf("ScanHTML(non-projected scope) = %v", err)
		}
	}
}

func TestScanHTMLKeepsIgnoredServiceCommandsOpaque(t *testing.T) {
	t.Parallel()
	result, err := ScanHTML([]byte(`<section data-kit-ignore>
  <button data-kit-click="$app.request.get('/private').then(done)"></button>
  <main data-kit-component="app" data-kit-as="$app" data-kit-scope="storage: null"></main>
</section>`))
	if err != nil {
		t.Fatal(err)
	}
	if result.NeedsRuntime || len(result.Components) != 0 {
		t.Fatalf("ignored service command leaked into scan: %#v", result)
	}
}

func TestScanHTMLServiceCommandDiagnosticUsesAttributeByteOffset(t *testing.T) {
	t.Parallel()
	source := `<p>prefix</p><button data-kit-click="$app.clipboard.writeText(code)"></button>`
	want := strings.Index(source, "data-kit-click")
	_, err := ScanHTML([]byte(source))
	if !errors.Is(err, ErrInvalidExpressionUse) {
		t.Fatalf("ScanHTML() error = %v, want ErrInvalidExpressionUse", err)
	}
	if marker := "at byte " + strconv.Itoa(want); !strings.Contains(err.Error(), marker) {
		t.Fatalf("ScanHTML() error = %q, want %q", err, marker)
	}
}

func TestScanHTMLDoesNotPromoteSupportedMetadata(t *testing.T) {
	t.Parallel()
	for _, source := range []string{
		`<a data-kit-drive="false" href="/next">Next</a>`,
		`<script defer src="/assets/site.js" data-kit-drive="stable"></script>`,
	} {
		result, err := ScanHTML([]byte(source))
		if err != nil {
			t.Fatalf("ScanHTML(%q): %v", source, err)
		}
		if result.NeedsRuntime || len(result.Components) != 0 {
			t.Errorf("ScanHTML(%q) = %#v, want no runtime use", source, result)
		}
	}
}

func TestScanHTMLRejectsUnsupportedReservedAttributes(t *testing.T) {
	t.Parallel()
	for _, source := range []string{
		`<div data-kit-guard="prevent stop"></div>`,
		`<div data-kit-debounce="300"></div>`,
		`<div data-kit-cloak></div>`,
		`<div data-kit-ref="field"></div>`,
		`<div data-kit-if="open"></div>`,
		`<div data-kit-for="item of items"></div>`,
		`<div data-kit-key="stable-row"></div>`,
		`<template data-kit-if="open" data-kit-for="item of items"></template>`,
		`<template data-kit-key="item.id"></template>`,
		`<button data-kit-action="toggle"></button>`,
		`<div data-kit-unknown="value"></div>`,
		`<button data-kit-click:mystery="run()"></button>`,
		`<button data-kit-click:debounce(0)="run()"></button>`,
		`<button data-kit-click:enter="run()"></button>`,
		`<input data-kit-input:escape="run()">`,
		`<input data-kit-keydown:enter:escape="run()">`,
		`<button data-kit-click:self:outside="run()"></button>`,
		`<input data-kit-input:outside="run()">`,
		`<button data-kit-click:once:once="run()"></button>`,
		`<input data-kit-input:debounce(10):debounce(20)="run()">`,
		`<template data-kit-if="open"><script>globalThis.bad = true;</script></template>`,
		`<div data-kit-component:lazy="dialog"></div>`,
		`<div data-kitwork-action="toggle"></div>`,
		`<script data-kitwork-plan="authored"></script>`,
	} {
		if _, err := ScanHTML([]byte(source)); !errors.Is(err, ErrUnsupportedAttribute) {
			t.Errorf("ScanHTML(%q) error = %v, want ErrUnsupportedAttribute", source, err)
		}
	}
}

func TestScanHTMLOpaqueReservedExamplesRemainOpaque(t *testing.T) {
	t.Parallel()
	source := []byte(`
<!-- <div data-kit-unknown="comment"></div> -->
<script>const sample = '<div data-kit-if="script"></div>';</script>
<code>&lt;button data-kit-action="toggle"&gt;Example&lt;/button&gt;</code>
<section data-kit-ignore data-kit-unknown="ignored">
  <div data-kitwork-action="ignored"></div>
</section>`)
	result, err := ScanHTML(source)
	if err != nil {
		t.Fatal(err)
	}
	if result.NeedsRuntime || len(result.Components) != 0 {
		t.Fatalf("opaque reserved examples changed selection: %#v", result)
	}
}

func TestScanHTMLRejectsAuthoredRuntimeProfileMarkers(t *testing.T) {
	t.Parallel()
	for _, source := range []string{
		`<main data-kit-app></main>`,
		`<main data-kit-app="false"></main>`,
		`<main data-kit-hydrate="legacy-v1"></main>`,
	} {
		if _, err := ScanHTML([]byte(source)); !errors.Is(err, ErrUnsupportedAttribute) {
			t.Errorf("ScanHTML(%q) error = %v, want ErrUnsupportedAttribute", source, err)
		}
	}
}

func TestScanHTMLSkipsEntireIgnoredSubtree(t *testing.T) {
	t.Parallel()
	source := []byte(`
<section data-kit-ignore data-kit-app="ignored-root" data-kit-component="missing">
  <div data-kit-text="ignored">
    <div data-kit-component="theme" data-kit-as="$duplicate"></div>
  </div>
  <script>var fake = '</section><main data-kit-app="fake"></main>';</script>
</section>
<img data-kit-ignore data-kit-component="missing">
<div data-kit-component="theme@3.0.0" data-kit-as="$duplicate"></div>`)
	result, err := ScanHTML(source)
	if err != nil {
		t.Fatal(err)
	}
	if !result.NeedsRuntime || len(result.Components) != 1 || result.Components[0].Name != "theme" {
		t.Fatalf("ignored subtree leaked into scan: %#v", result)
	}
}

func TestScanHTMLStopsIgnoreAtImpliedHTMLClosures(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		source string
	}{
		{
			name:   "paragraph",
			source: `<p data-kit-ignore>ignored<div data-kit-component="progress-bar" data-kit-version="2.0.0"></div>`,
		},
		{
			name:   "list item",
			source: `<ul><li data-kit-ignore>ignored<li data-kit-component="progress-bar" data-kit-version="2.0.0">live</ul>`,
		},
		{
			name:   "option",
			source: `<select><option data-kit-ignore>ignored<option data-kit-component="progress-bar" data-kit-version="2.0.0">live</select>`,
		},
		{
			name:   "table cell",
			source: `<table><tbody><tr><td data-kit-ignore>ignored<td data-kit-component="progress-bar" data-kit-version="2.0.0">live</tr></tbody></table>`,
		},
		{
			name:   "table row",
			source: `<table><tbody><tr data-kit-ignore><td>ignored<tr data-kit-component="progress-bar" data-kit-version="2.0.0"><td>live</tbody></table>`,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			result, err := ScanHTML([]byte(test.source))
			if err != nil {
				t.Fatal(err)
			}
			if !result.NeedsRuntime || len(result.Components) != 1 ||
				result.Components[0].Name != "progress-bar" || result.Components[0].Version != "2.0.0" {
				t.Fatalf("implied closure kept live component opaque: %#v", result)
			}
		})
	}
}

func TestScanHTMLRejectsFosterParentedContentFromIgnoredTables(t *testing.T) {
	t.Parallel()
	for _, source := range []string{
		`<table data-kit-ignore><div data-kit-component="progress-bar" data-kit-version="2.0.0"></div></table>`,
		`<table data-kit-ignore><div data-kit-component="progress-bar" data-kit-version="2.0.0"></div><tbody><tr><td data-kit-unknown></td></tr></tbody></table>`,
	} {
		if _, err := ScanHTML([]byte(source)); !errors.Is(err, ErrUnsupportedAttribute) {
			t.Fatalf("ScanHTML(%q) error = %v, want stable ErrUnsupportedAttribute", source, err)
		}
	}
}

func TestScanHTMLFosterParentingWithinBroaderIgnoreRemainsOpaque(t *testing.T) {
	t.Parallel()
	result, err := ScanHTML([]byte(`<section data-kit-ignore>
  <table><div data-kit-component="hidden-fostered"></div><tbody><tr><td data-kit-unknown></td></tr></tbody></table>
</section>`))
	if err != nil {
		t.Fatal(err)
	}
	if result.NeedsRuntime || len(result.Components) != 0 {
		t.Fatalf("foster-parented content escaped its broader ignored ancestor: %#v", result)
	}
}

func TestScanHTMLIgnoredFramesPreserveRawTextTemplateAndForeignBoundaries(t *testing.T) {
	t.Parallel()
	source := []byte(`<section data-kit-ignore>
  <script>const fake = '<div data-kit-component="script-fake"></div>';</script>
  <template><div data-kit-component="template-fake"></div></template>
  <svg><path data-kit-component="svg-fake"/><foreignObject><div data-kit-component="foreign-fake"></div></foreignObject></svg>
</section>
<div data-kit-component="progress-bar" data-kit-version="2.0.0"></div>`)
	result, err := ScanHTML(source)
	if err != nil {
		t.Fatal(err)
	}
	if !result.NeedsRuntime || len(result.Components) != 1 ||
		result.Components[0].Name != "progress-bar" || result.Components[0].Version != "2.0.0" {
		t.Fatalf("opaque parser lost an HTML boundary: %#v", result)
	}
}

func TestScanHTMLIgnoredTableCellsAndCaptionsRemainOpaque(t *testing.T) {
	t.Parallel()
	for _, source := range []string{
		`<table><caption data-kit-ignore><div data-kit-component="hidden-caption"></div></caption></table>`,
		`<table data-kit-ignore><tbody><tr><td><div data-kit-component="hidden-cell"></div></td></tr></tbody></table>`,
	} {
		result, err := ScanHTML([]byte(source))
		if err != nil {
			t.Fatal(err)
		}
		if result.NeedsRuntime || len(result.Components) != 0 {
			t.Fatalf("ordinary table-region content escaped ignore: %#v", result)
		}
	}
}

func TestScanHTMLDoesNotValidateKitMetadataOnIgnoredHost(t *testing.T) {
	t.Parallel()
	result, err := ScanHTML([]byte(`<section data-kit-component data-kit-component="bad" data-kit-version data-kit-as data-kit-app data-kit-app data-kit-ignore></section>`))
	if err != nil {
		t.Fatal(err)
	}
	if result.NeedsRuntime || len(result.Components) != 0 {
		t.Fatalf("ignored host leaked into scan: %#v", result)
	}
}

func TestScanHTMLCommentsAndRawTextCannotSelectRuntimeOrDrive(t *testing.T) {
	t.Parallel()
	source := []byte(`
<!-- <main data-kit-app><b data-kit-text="fake"></b></main> -->
<script>var sample = '<main data-kit-hydrate data-kit-click="fake()">';</script>
<style>[data-kit-app] { content: 'data-kit-text'; }</style>`)
	result, err := ScanHTML(source)
	if err != nil {
		t.Fatal(err)
	}
	if result.NeedsRuntime || len(result.Components) != 0 {
		t.Fatalf("opaque content selected runtime: %#v", result)
	}
}

func TestScanComponentsRejectsLegacyInlineAlias(t *testing.T) {
	t.Parallel()
	_, err := ScanComponents([]byte(`<div data-kit-component="theme=$theme"></div>`))
	if !errors.Is(err, ErrInvalidComponentUse) {
		t.Fatalf("got %v, want ErrInvalidComponentUse", err)
	}
}

func TestScanComponentsRejectsFloatingConflictingOrInvalidVersionAndInvalidAlias(t *testing.T) {
	t.Parallel()
	tests := []string{
		`<div data-kit-version="1.0.0"></div>`,
		`<div data-kit-component="theme@2.0.0" data-kit-version="2.0.0"></div>`,
		`<div data-kit-component="dialog@v1.0.0"></div>`,
		`<div data-kit-component="dialog@latest"></div>`,
		`<div data-kit-component="dialog@^1.0.0"></div>`,
		`<div data-kit-component="dialog@ 1.0.0"></div>`,
		`<div data-kit-component="dialog@1.0.0 "></div>`,
		`<div data-kit-component="dialog@@1.0.0"></div>`,
		`<div data-kit-component="theme@"></div>`,
		`<div data-kit-component="theme" data-kit-version="v2.0.0"></div>`,
		`<div data-kit-component="theme" data-kit-version="2.0.0" data-kit-version="2.0.0"></div>`,
		`<div data-kit-component="theme" data-kit-as="theme"></div>`,
		`<div data-kit-component="theme" data-kit-as="$theme.value"></div>`,
		`<div data-kit-component="theme" data-kit-as="$_theme"></div>`,
		`<div data-kit-component="theme" data-kit-as="$element"></div>`,
		`<div data-kit-as="$theme"></div>`,
		`<div data-kit-component="theme" data-kit-component="dialog"></div>`,
	}
	for _, source := range tests {
		if _, err := ScanComponents([]byte(source)); !errors.Is(err, ErrInvalidComponentUse) {
			t.Errorf("ScanComponents(%q) error = %v, want ErrInvalidComponentUse", source, err)
		}
	}
}

func TestScanComponentsUsesECMAScriptWhitespaceForIdentity(t *testing.T) {
	t.Parallel()

	result, err := ScanHTML([]byte("<div data-kit-component=\"\ufeffdialog@1.0.0\" data-kit-as=\"\ufeff$dialog\ufeff\"></div>"))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Components) != 1 || result.Components[0].Name != "dialog" ||
		result.Components[0].Version != "1.0.0" || result.Components[0].Alias != "$dialog" {
		t.Fatalf("ECMAScript-trimmed inline identity = %#v", result.Components)
	}

	result, err = ScanHTML([]byte("<div data-kit-component=\"\u1680dialog\u2003\" data-kit-version=\"\ufeff1.0.0\u2029\" data-kit-as=\"\u00a0$dialog\u3000\"></div>"))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Components) != 1 || result.Components[0].Name != "dialog" ||
		result.Components[0].Version != "1.0.0" || result.Components[0].Alias != "$dialog" {
		t.Fatalf("ECMAScript-trimmed legacy identity = %#v", result.Components)
	}

	for _, source := range []string{
		"<div data-kit-component=\"\u0085dialog@1.0.0\"></div>",
		"<div data-kit-component=\"dialog@1.0.0\ufeff\"></div>",
		"<div data-kit-component=\"dialog\" data-kit-version=\"\u00851.0.0\"></div>",
		"<div data-kit-component=\"dialog@1.0.0\" data-kit-as=\"\u0085$dialog\"></div>",
	} {
		if _, err := ScanHTML([]byte(source)); !errors.Is(err, ErrInvalidComponentUse) {
			t.Errorf("ScanHTML(%q) error = %v, want ErrInvalidComponentUse", source, err)
		}
	}
}

func TestScanComponentsRejectsDuplicateAliases(t *testing.T) {
	t.Parallel()
	_, err := ScanComponents([]byte(`
<div data-kit-component="theme" data-kit-as="$theme"></div>
<div data-kit-component="theme" data-kit-as="$theme"></div>`))
	if !errors.Is(err, ErrInvalidComponentUse) {
		t.Fatalf("got %v, want ErrInvalidComponentUse", err)
	}
}

func TestValidExactSemVer(t *testing.T) {
	t.Parallel()
	valid := []string{"0.0.0", "1.0.0", "1.2.3-alpha.1", "1.2.3+build.7", "1.2.3-rc.1+sha-a"}
	invalid := []string{"", "v1.0.0", "1", "1.0", "01.0.0", "1.0.0-01", "1.0.0+", "1.0.0+bad_value"}
	for _, version := range valid {
		if !validExactSemVer(version) {
			t.Errorf("validExactSemVer(%q) = false, want true", version)
		}
	}
	for _, version := range invalid {
		if validExactSemVer(version) {
			t.Errorf("validExactSemVer(%q) = true, want false", version)
		}
	}
}
