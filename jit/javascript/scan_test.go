package javascript

import (
	"errors"
	"testing"
)

func TestScanComponentsReadsAliasesAndSeparateExactVersions(t *testing.T) {
	t.Parallel()
	source := []byte(`<!doctype html>
<html data-kit-component="app" data-kit-as="$app">
  <div DATA-KIT-AS='$theme' DATA-KIT-COMPONENT='theme' DATA-KIT-VERSION='1.0.0'></div>
  <div data-kit-component="progress-bar" data-kit-version="1.2.3-rc.1+build.7"></div>
  <x-panel data-kit-component=dialog></x-panel>
</html>`)

	got, err := ScanComponents(source)
	if err != nil {
		t.Fatal(err)
	}
	want := []ComponentRef{
		{Name: "app", Alias: "$app"},
		{Name: "theme", Version: "1.0.0", Alias: "$theme"},
		{Name: "progress-bar", Version: "1.2.3-rc.1+build.7"},
		{Name: "dialog"},
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
}

func TestScanComponentsIgnoresCommentsAndRawText(t *testing.T) {
	t.Parallel()
	source := []byte(`
<!-- <div data-kit-component="comment"></div> -->
<script>const example = '<div data-kit-component="script"></div>';</script>
<style>.x::after { content: '<i data-kit-component="style">'; }</style>
<textarea><div data-kit-component="textarea"></div></textarea>
<template><div data-kit-component="template-child"></div></template>
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
		`<div data-kit-style="opacity: alpha;"></div>`,
		`<input data-kit-model="query">`,
		`<div data-kit-cloak></div>`,
		`<section data-kit-scope="count: 0;"></section>`,
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
		if !result.NeedsRuntime || result.Drive || result.HasApp || len(result.Components) != 0 {
			t.Errorf("ScanHTML(%q) = %#v, want runtime-only use", source, result)
		}
	}
}

func TestScanHTMLDoesNotPromoteSupportedMetadata(t *testing.T) {
	t.Parallel()
	for _, source := range []string{
		`<a data-kit-drive="false" href="/next">Next</a>`,
		`<div data-kit-ref="field"></div>`,
		`<div data-kit-key="stable-row"></div>`,
	} {
		result, err := ScanHTML([]byte(source))
		if err != nil {
			t.Fatalf("ScanHTML(%q): %v", source, err)
		}
		if result.NeedsRuntime || result.Drive || result.HasApp || len(result.Components) != 0 {
			t.Errorf("ScanHTML(%q) = %#v, want no runtime use", source, result)
		}
	}
}

func TestScanHTMLRejectsUnsupportedReservedAttributes(t *testing.T) {
	t.Parallel()
	for _, source := range []string{
		`<div data-kit-guard="prevent stop"></div>`,
		`<div data-kit-debounce="300"></div>`,
		`<div data-kit-if="open"></div>`,
		`<div data-kit-for="item of items"></div>`,
		`<button data-kit-action="toggle"></button>`,
		`<div data-kit-unknown="value"></div>`,
		`<button data-kit-click:mystery="run()"></button>`,
		`<button data-kit-click:debounce(0)="run()"></button>`,
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
	if result.NeedsRuntime || result.Drive || result.HasApp || len(result.Components) != 0 {
		t.Fatalf("opaque reserved examples changed selection: %#v", result)
	}
}

func TestScanHTMLSelectsOnePositiveAppMarker(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		source string
		app    string
	}{
		{name: "valueless app", source: `<main data-kit-app></main>`},
		{name: "opaque app identity", source: `<main data-kit-app="dashboard@canary"></main>`, app: "dashboard@canary"},
		{name: "opaque whitespace identity", source: `<main data-kit-app=" false "></main>`, app: " false "},
		{name: "hydrate compatibility alias", source: `<main data-kit-hydrate="legacy-v1"></main>`, app: "legacy-v1"},
		{name: "disabled marker before positive", source: `<aside data-kit-app="false"></aside><main data-kit-hydrate="main"></main>`, app: "main"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			result, err := ScanHTML([]byte(test.source))
			if err != nil {
				t.Fatal(err)
			}
			if !result.HasApp || !result.NeedsRuntime || !result.Drive || result.App != test.app {
				t.Fatalf("ScanHTML() = %#v, want positive app %q with Drive", result, test.app)
			}
		})
	}
}

func TestScanHTMLDisabledAppMarkerIsNotRuntimeUse(t *testing.T) {
	t.Parallel()
	result, err := ScanHTML([]byte(`<main data-kit-app="false"><a data-kit-drive="false">Native</a></main>`))
	if err != nil {
		t.Fatal(err)
	}
	if result.NeedsRuntime || result.Drive || result.HasApp {
		t.Fatalf("disabled app marker selected runtime: %#v", result)
	}
}

func TestScanHTMLRejectsMultiplePositiveAppMarkers(t *testing.T) {
	t.Parallel()
	for _, source := range []string{
		`<main data-kit-app="one"></main><aside data-kit-app="two"></aside>`,
		`<main data-kit-app data-kit-hydrate></main>`,
		`<main data-kit-app></main><aside data-kit-hydrate="compat"></aside>`,
	} {
		if _, err := ScanHTML([]byte(source)); !errors.Is(err, ErrInvalidAppUse) {
			t.Errorf("ScanHTML(%q) error = %v, want ErrInvalidAppUse", source, err)
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
<div data-kit-component="theme" data-kit-as="$duplicate"></div>`)
	result, err := ScanHTML(source)
	if err != nil {
		t.Fatal(err)
	}
	if result.HasApp || result.Drive {
		t.Fatalf("ignored app marker selected Drive: %#v", result)
	}
	if !result.NeedsRuntime || len(result.Components) != 1 || result.Components[0].Name != "theme" {
		t.Fatalf("ignored subtree leaked into scan: %#v", result)
	}
}

func TestScanHTMLDoesNotValidateKitMetadataOnIgnoredHost(t *testing.T) {
	t.Parallel()
	result, err := ScanHTML([]byte(`<section data-kit-component data-kit-component="bad" data-kit-version data-kit-as data-kit-app data-kit-app data-kit-ignore></section>`))
	if err != nil {
		t.Fatal(err)
	}
	if result.NeedsRuntime || result.Drive || result.HasApp || len(result.Components) != 0 {
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
	if result.NeedsRuntime || result.Drive || result.HasApp || len(result.Components) != 0 {
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

func TestScanComponentsRejectsInlineOrInvalidVersionAndInvalidAlias(t *testing.T) {
	t.Parallel()
	tests := []string{
		`<div data-kit-version="1.0.0"></div>`,
		`<div data-kit-component="theme@1.0.0"></div>`,
		`<div data-kit-component="theme@1.0.0" data-kit-version="1.0.0"></div>`,
		`<div data-kit-component="theme@v1.0.0" data-kit-version="2.0.0"></div>`,
		`<div data-kit-component="theme" data-kit-version="v1.0.0"></div>`,
		`<div data-kit-component="theme" data-kit-version="1.0.0" data-kit-version="1.0.0"></div>`,
		`<div data-kit-component="theme" data-kit-as="theme"></div>`,
		`<div data-kit-component="theme" data-kit-as="$theme.value"></div>`,
		`<div data-kit-component="theme" data-kit-as="$_theme"></div>`,
		`<div data-kit-component="theme" data-kit-as="$element"></div>`,
		`<div data-kit-as="$theme"></div>`,
		`<div data-kit-component="_theme"></div>`,
		`<div data-kit-component="theme" data-kit-scope="mode: 'dark'"></div>`,
		`<div data-kit-component="theme" data-kit-component="dialog"></div>`,
	}
	for _, source := range tests {
		if _, err := ScanComponents([]byte(source)); !errors.Is(err, ErrInvalidComponentUse) {
			t.Errorf("ScanComponents(%q) error = %v, want ErrInvalidComponentUse", source, err)
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
