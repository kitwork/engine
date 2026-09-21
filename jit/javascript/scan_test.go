package javascript

import (
	"errors"
	"strconv"
	"strings"
	"testing"
)

func TestScanComponentsReadsInlineExactVersions(t *testing.T) {
	t.Parallel()
	source := []byte(`<!doctype html>
<html data-kit-component="app@1.1.0" data-kit-alias="$app">
  <div DATA-KIT-ALIAS='$theme' DATA-KIT-COMPONENT='theme@2.0.0'></div>
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

func TestScanHTMLTagNameStateKeepsScriptLikeCustomElementsLive(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		suffix []byte
	}{
		{name: "underscore", suffix: []byte("_")},
		{name: "period", suffix: []byte(".")},
		{name: "NUL", suffix: []byte{0}},
		{name: "non-ASCII", suffix: []byte("é")},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var source []byte
			source = append(source, []byte("<script")...)
			source = append(source, test.suffix...)
			source = append(source, []byte(`><div data-kit-component="dialog@1.0.0"></div></script`)...)
			source = append(source, test.suffix...)
			source = append(source, []byte(`>
<script>var sample = '<div data-kit-component="theme@2.0.0"></div>';</script>
<DIV><span data-kit-component="toast@1.0.0"></span></DIV>`)...)

			result, err := ScanHTML(source)
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Components) != 2 || result.Components[0].Name != "dialog" ||
				result.Components[1].Name != "toast" {
				t.Fatalf("script-like custom tag scan = %#v, want live dialog and toast only", result.Components)
			}
		})
	}

	for _, suffix := range [][]byte{[]byte("_"), []byte("."), {0}, []byte("é")} {
		source := append([]byte("<script"), suffix...)
		source = append(source, []byte(` data-kitwork-jit="runtime"></script`)...)
		source = append(source, suffix...)
		source = append(source, '>')
		if hasRuntimeMarkerAttribute(source) {
			t.Errorf("script-like custom tag %q was mistaken for an engine runtime marker", source)
		}
	}
	if !hasRuntimeMarkerAttribute([]byte(`<SCRIPT data-kitwork-jit="runtime"></SCRIPT>`)) {
		t.Error("ordinary ASCII-case-insensitive script marker was not detected")
	}
}

func TestScanHTMLAttributeNamesUseASCIIOnlyCaseFolding(t *testing.T) {
	t.Parallel()
	result, err := ScanHTML([]byte(`<div data-Kit-component="dialog@1.0.0" data-Kit-text="label"></div>`))
	if err != nil {
		t.Fatal(err)
	}
	if result.NeedsRuntime || len(result.Components) != 0 || len(result.LocalComponents) != 0 {
		t.Fatalf("Unicode-lookalike attribute selected KitJS: %#v", result)
	}
	if hasRuntimeMarkerAttribute([]byte(`<script data-Kitwork-jit="runtime"></script>`)) {
		t.Error("Unicode-lookalike attribute was mistaken for an engine runtime marker")
	}
}

func TestScanHTMLRawTextEndTagAcceptsSlashDelimiter(t *testing.T) {
	t.Parallel()
	source := []byte(`<script>var sample = '<div data-kit-component="theme@2.0.0"></div>';</script/><div data-kit-component="dialog@1.0.0"></div>`)
	result, err := ScanHTML(source)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Components) != 1 || result.Components[0].Name != "dialog" {
		t.Fatalf("slash-delimited raw-text close scan = %#v, want live dialog only", result.Components)
	}

	closeStart, closeEnd, ok := deliveryRawTextClose(source, len(`<script>`), len(source), "script")
	if !ok || string(source[closeStart:closeEnd]) != `</script/>` {
		t.Fatalf("delivery raw-text close = (%d, %d, %t) %q, want </script/>",
			closeStart, closeEnd, ok, source[closeStart:closeEnd])
	}
}

func TestScanHTMLDetectsImplementedRuntimeDirectivesAndEvents(t *testing.T) {
	t.Parallel()
	tests := []string{
		`<b data-kit-text="label"></b>`,
		`<div data-kit-show="open"></div>`,
		`<div data-kit-class="tone"></div>`,
		`<div data-kit-bind:aria-expanded="open"></div>`,
		`<button data-kit-bind:disabled="!ready" data-kit-bind:title="label" data-kit-bind:data-state="tone"></button>`,
		`<input data-kit-ref="search"><button data-kit-click="$refs.search.focus()"></button>`,
		`<div data-kit-keydown:escape:window="open = false"></div>`,
		`<section data-kit-error="failed = $error.message; $error.element.focus()"><b data-kit-text="failed"></b></section>`,
		`<h1 data-kit-seed="title">Hello</h1><input value="a" data-kit-seed:value="user.email"><li data-kit-seed="tags[]">go</li><details open data-kit-seed:open="expanded"></details>`,
		`<script type="application/json" data-kit-seed="items">[1,2]</script>`,
		`<button data-kit-click:document:prevent="run()"></button>`,
		`<button data-kit-pointerdown:throttle(100):stop="run()"></button>`,
		`<input data-kit-bind:checked="on" data-kit-bind:value="text">`,
		`<div data-kit-style="width: progress + '%'; opacity: open ? 1 : 0;"></div>`,
		`<input data-kit-model="query">`,
		`<section data-kit-scope="count: 0"></section>`,
		`<div data-kit-if="open"><p data-kit-text="label"></p></div>`,
		`<template data-kit-if="open"><p data-kit-text="label"></p></template>`,
		`<template data-kit-for="item, index of items" data-kit-key="item.id"><p data-kit-text="item.name"></p></template>`,
		`<ul><li data-kit-for="item, index of items" data-kit-key="item.id" data-kit-text="item.name + count"></li></ul>`,
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

func TestScanHTMLDirectIfKeepsClosedGraphAndExpressionValidation(t *testing.T) {
	t.Parallel()
	result, err := ScanHTML([]byte(`<main data-kit-scope="open: true">
  <section data-kit-if="open" data-kit-component="dialog@1.0.0"></section>
</main>`))
	if err != nil {
		t.Fatal(err)
	}
	if !result.NeedsRuntime || len(result.Components) != 1 ||
		result.Components[0].Name != "dialog" || result.Components[0].Version != "1.0.0" {
		t.Fatalf("direct-if graph scan = %#v, want exact dialog component", result)
	}

	if _, err := ScanHTML([]byte(`<main data-kit-scope="open: true"><div data-kit-if="open(">fallback</div></main>`)); !errors.Is(err, ErrInvalidExpressionUse) {
		t.Fatalf("malformed direct-if error = %v, want ErrInvalidExpressionUse", err)
	}
}

func TestScanHTMLRejectsRetainInDirectIfBranch(t *testing.T) {
	t.Parallel()
	for _, source := range []string{
		`<main data-kit-scope="open: true"><section data-kit-if="open" data-kit-component="dialog@1.0.0" data-kit-retain="dialog"></section></main>`,
		`<main data-kit-scope="open: true"><div data-kit-if="open"><section data-kit-component="dialog@1.0.0" data-kit-retain="dialog"></section></div></main>`,
	} {
		if _, err := ScanHTML([]byte(source)); !errors.Is(err, ErrInvalidComponentUse) {
			t.Errorf("ScanHTML(%q) error = %v, want ErrInvalidComponentUse", source, err)
		}
	}
}

func TestScanHTMLResolvesAuthoredServiceCommandsThroughAppAliasGrants(t *testing.T) {
	t.Parallel()
	source := []byte(`<button data-kit-click="$app.clipboard.writeText(code); $app.appearance.toggle(); $app.navigation.back()">Run</button>
<main data-kit-component="app@1.0.0" data-kit-alias="$app"></main>`)
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
		`<html data-kit-component="app@1.1.0" data-kit-alias="$app"><button data-kit-click="$app.progress.start('load')"></button><div data-kit-show="$app.loader.visible"></div></html>`,
		`<html data-kit-component="app@1.1.0" data-kit-alias="$app"><div data-kit-style="width: $app.loader.value === null ? '12%' : $app.loader.value + '%';"></div></html>`,
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
		`<html data-kit-component="app@1.0.0" data-kit-alias="$app"><button data-kit-click="$app.progress.start('load')"></button></html>`,
		`<html data-kit-component="app@1.0.0" data-kit-alias="$app"><div data-kit-show="$app.loader.visible"></div></html>`,
		`<html data-kit-component="app@1.2.0" data-kit-alias="$app"><div data-kit-show="$app.loader.visible"></div></html>`,
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
		`<button data-kit-click="$dialog.clipboard.writeText(code)"></button><section data-kit-component="dialog@1.0.0" data-kit-alias="$dialog"></section>`,
		`<button data-kit-click="$app.clipboard.writeText(code)"></button><section data-kit-component="dialog@1.0.0" data-kit-alias="$app"></section>`,
		`<button data-kit-click="$other.clipboard.writeText(code)"></button><main data-kit-component="app@1.0.0" data-kit-alias="$app"></main>`,
		`<button data-kit-click="$app.request.get('/api')"></button><main data-kit-component="app@1.0.0" data-kit-alias="$app"></main>`,
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
		source := `<main data-kit-component="app@1.1.0" data-kit-alias="$app" data-kit-scope="` + field + `: null"></main>`
		if _, err := ScanHTML([]byte(source)); !errors.Is(err, ErrInvalidScopeUse) {
			t.Errorf("ScanHTML(app scope field %q) error = %v, want ErrInvalidScopeUse", field, err)
		}
	}

	for _, source := range []string{
		`<main data-kit-component="app@1.0.0" data-kit-alias="$app" data-kit-scope="storageKey: null, profile: {storage: true}"></main>`,
		`<main data-kit-component="app@1.0.0" data-kit-alias="$other" data-kit-scope="storage: null"></main>`,
		`<main data-kit-component="dialog@1.0.0" data-kit-alias="$app" data-kit-scope="storage: null"></main>`,
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
  <main data-kit-component="app" data-kit-alias="$app" data-kit-scope="storage: null"></main>
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
		// A binding names one target in the attribute; the list form is not authored any more.
		`<div data-kit-bind="aria-expanded: open;"></div>`,
		`<div data-kit-bind="{ disabled: !ok }"></div>`,
		`<div data-kit-bind:="open"></div>`,
		`<div data-kit-bind:onclick="run"></div>`,
		`<div data-kit-bind:innerhtml="body"></div>`,
		`<div data-kit-bind:data-kit-text="x"></div>`,
		`<div data-kit-bind:aria-expanded:prevent="open"></div>`,
		`<div data-kit-attr:aria-expanded="open"></div>`,
		`<div data-kit-guard="prevent stop"></div>`,
		`<div data-kit-debounce="300"></div>`,
		`<div data-kit-cloak></div>`,
		// data-kit-ref names an element for $refs.<name>: an identifier, one per element, no modifiers.
		`<div data-kit-ref></div>`,
		`<div data-kit-ref=""></div>`,
		`<div data-kit-ref="$field"></div>`,
		`<div data-kit-ref="search-box"></div>`,
		`<div data-kit-ref="constructor"></div>`,
		`<div data-kit-ref="document"></div>`,
		`<div data-kit-ref:once="field"></div>`,
		`<div data-kit-key="stable-row"></div>`,
		`<div data-kit-if="open" data-kit-for="item of items"></div>`,
		`<section data-kit-error:once="failed = $error.message"></section>`,
		// data-kit-seed: a state target on the right, a safe source on the left.
		`<b data-kit-seed></b>`,
		`<b data-kit-seed="items[1]"></b>`,
		`<b data-kit-seed="a b"></b>`,
		`<b data-kit-seed="$title"></b>`,
		`<b data-kit-seed="constructor"></b>`,
		`<b data-kit-seed="user.__proto__"></b>`,
		`<b data-kit-seed:="title"></b>`,
		`<b data-kit-seed:innerhtml="body"></b>`,
		`<b data-kit-seed:onclick="run"></b>`,
		`<b data-kit-seed:value:once="title"></b>`,
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
		`<input data-kit-input:debounce(10):throttle(20)="run()">`,
		`<input data-kit-input:throttle(0)="run()">`,
		`<button data-kit-click:window:document="run()"></button>`,
		`<button data-kit-click:self:window="run()"></button>`,
		`<button data-kit-click:outside:document="run()"></button>`,
		`<template data-kit-if="open"><script>globalThis.bad = true;</script></template>`,
		`<div data-kit-if="open"><script>globalThis.bad = true;</script></div>`,
		`<script data-kit-if="open">globalThis.bad = true;</script>`,
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
    <div data-kit-component="theme" data-kit-alias="$duplicate"></div>
  </div>
  <script>var fake = '</section><main data-kit-app="fake"></main>';</script>
</section>
<img data-kit-ignore data-kit-component="missing">
<div data-kit-component="theme@3.0.0" data-kit-alias="$duplicate"></div>`)
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
			source: `<p data-kit-ignore>ignored<div data-kit-component="progress-bar@2.0.0"></div>`,
		},
		{
			name:   "list item",
			source: `<ul><li data-kit-ignore>ignored<li data-kit-component="progress-bar@2.0.0">live</ul>`,
		},
		{
			name:   "option",
			source: `<select><option data-kit-ignore>ignored<option data-kit-component="progress-bar@2.0.0">live</select>`,
		},
		{
			name:   "table cell",
			source: `<table><tbody><tr><td data-kit-ignore>ignored<td data-kit-component="progress-bar@2.0.0">live</tr></tbody></table>`,
		},
		{
			name:   "table row",
			source: `<table><tbody><tr data-kit-ignore><td>ignored<tr data-kit-component="progress-bar@2.0.0"><td>live</tbody></table>`,
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
		`<table data-kit-ignore><div data-kit-component="progress-bar@2.0.0"></div></table>`,
		`<table data-kit-ignore><div data-kit-component="progress-bar@2.0.0"></div><tbody><tr><td data-kit-unknown></td></tr></tbody></table>`,
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
<div data-kit-component="progress-bar@2.0.0"></div>`)
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
	result, err := ScanHTML([]byte(`<section data-kit-component data-kit-component="bad" data-kit-version data-kit-alias data-kit-app data-kit-app data-kit-ignore></section>`))
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

func TestScanComponentsRejectsRemovedVersionInvalidIdentityAndAlias(t *testing.T) {
	t.Parallel()
	for _, source := range []string{
		`<div data-kit-version="1.0.0"></div>`,
		`<div data-kit-component="theme@2.0.0" data-kit-version="2.0.0"></div>`,
		`<div data-kit-component="theme@2.0.0" data-kit-version="2.0.0" data-kit-version="2.0.0"></div>`,
	} {
		if _, err := ScanComponents([]byte(source)); !errors.Is(err, ErrUnsupportedAttribute) ||
			!strings.Contains(err.Error(), "data-kit-version") {
			t.Errorf("ScanComponents(%q) error = %v, want removed data-kit-version rejection", source, err)
		}
	}

	tests := []string{
		`<div data-kit-component="dialog@v1.0.0"></div>`,
		`<div data-kit-component="dialog@latest"></div>`,
		`<div data-kit-component="dialog@^1.0.0"></div>`,
		`<div data-kit-component="dialog@ 1.0.0"></div>`,
		`<div data-kit-component="dialog@1.0.0 "></div>`,
		`<div data-kit-component="dialog@@1.0.0"></div>`,
		`<div data-kit-component="theme@"></div>`,
		`<div data-kit-component="theme@v2.0.0"></div>`,
		`<div data-kit-component="theme" data-kit-alias="theme"></div>`,
		`<div data-kit-component="theme" data-kit-alias="$theme.value"></div>`,
		`<div data-kit-component="theme" data-kit-alias="$_theme"></div>`,
		`<div data-kit-component="theme" data-kit-alias="$element"></div>`,
		`<div data-kit-component="theme" data-kit-alias="$this"></div>`,
		`<div data-kit-component="theme" data-kit-alias="$el"></div>`,
		`<div data-kit-component="theme" data-kit-alias="$host"></div>`,
		`<div data-kit-alias="$theme"></div>`,
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

	result, err := ScanHTML([]byte("<div data-kit-component=\"\ufeffdialog@1.0.0\" data-kit-alias=\"\ufeff$dialog\ufeff\"></div>"))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Components) != 1 || result.Components[0].Name != "dialog" ||
		result.Components[0].Version != "1.0.0" || result.Components[0].Alias != "$dialog" {
		t.Fatalf("ECMAScript-trimmed inline identity = %#v", result.Components)
	}

	result, err = ScanHTML([]byte("<div data-kit-component=\"\u1680dialog@1.0.0\" data-kit-alias=\"\u00a0$dialog\u3000\"></div>"))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Components) != 1 || result.Components[0].Name != "dialog" ||
		result.Components[0].Version != "1.0.0" || result.Components[0].Alias != "$dialog" {
		t.Fatalf("ECMAScript-trimmed inline identity = %#v", result.Components)
	}

	for _, source := range []string{
		"<div data-kit-component=\"\u0085dialog@1.0.0\"></div>",
		"<div data-kit-component=\"dialog@1.0.0\ufeff\"></div>",
		"<div data-kit-component=\"dialog@\u00851.0.0\"></div>",
		"<div data-kit-component=\"dialog@1.0.0\" data-kit-alias=\"\u0085$dialog\"></div>",
	} {
		if _, err := ScanHTML([]byte(source)); !errors.Is(err, ErrInvalidComponentUse) {
			t.Errorf("ScanHTML(%q) error = %v, want ErrInvalidComponentUse", source, err)
		}
	}
}

func TestScanComponentsRejectsDuplicateAliases(t *testing.T) {
	t.Parallel()
	_, err := ScanComponents([]byte(`
<div data-kit-component="theme" data-kit-alias="$theme"></div>
<div data-kit-component="theme" data-kit-alias="$theme"></div>`))
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
