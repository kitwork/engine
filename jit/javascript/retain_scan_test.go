package javascript

import (
	"errors"
	"strings"
	"testing"
)

func TestScanHTMLAcceptsExactRetainKeys(t *testing.T) {
	t.Parallel()
	keys := []string{
		"a",
		"App-Progress",
		"app.progress_v1:primary",
		"a" + strings.Repeat("z", 127),
	}
	for _, key := range keys {
		key := key
		t.Run(key[:1], func(t *testing.T) {
			t.Parallel()
			result, err := ScanHTML([]byte(`<section data-kit-component="progress-bar@1.0.0" data-kit-retain="` + key + `"></section>`))
			if err != nil {
				t.Fatal(err)
			}
			if !result.NeedsRuntime || len(result.Components) != 1 || result.Components[0].Name != "progress-bar" || result.Components[0].Retain != key {
				t.Fatalf("ScanHTML() = %#v, want one progress-bar component", result)
			}
		})
	}

	result, err := ScanHTML([]byte(`
<section data-kit-component="one@1.0.0" data-kit-retain="Shell"></section>
<section data-kit-component="two@1.0.0" data-kit-retain="shell"></section>`))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Components) != 2 {
		t.Fatalf("case-distinct retain keys produced %#v, want two components", result.Components)
	}
}

func TestScanHTMLRejectsInvalidOrNonExactRetainKeys(t *testing.T) {
	t.Parallel()
	invalid := []string{
		`<section data-kit-component="probe@1.0.0" data-kit-retain></section>`,
		`<section data-kit-component="probe@1.0.0" data-kit-retain=""></section>`,
		`<section data-kit-component="probe@1.0.0" data-kit-retain=" shell"></section>`,
		`<section data-kit-component="probe@1.0.0" data-kit-retain="shell "></section>`,
		`<section data-kit-component="probe@1.0.0" data-kit-retain="two words"></section>`,
		`<section data-kit-component="probe@1.0.0" data-kit-retain="0shell"></section>`,
		`<section data-kit-component="probe@1.0.0" data-kit-retain="_shell"></section>`,
		`<section data-kit-component="probe@1.0.0" data-kit-retain=":shell"></section>`,
		`<section data-kit-component="probe@1.0.0" data-kit-retain="shell/path"></section>`,
		`<section data-kit-component="probe@1.0.0" data-kit-retain="shell@next"></section>`,
		`<section data-kit-component="probe" data-kit-retain="tiến-trình"></section>`,
		`<section data-kit-component="probe" data-kit-retain="a` + strings.Repeat("z", 128) + `"></section>`,
		`<section data-kit-component="probe" data-kit-retain="one" data-kit-retain="two"></section>`,
	}
	for _, source := range invalid {
		if _, err := ScanHTML([]byte(source)); !errors.Is(err, ErrInvalidComponentUse) {
			t.Errorf("ScanHTML(%q) error = %v, want ErrInvalidComponentUse", source, err)
		}
	}
}

func TestScanHTMLRejectsRetainOutsideAnUnnestedComponentHost(t *testing.T) {
	t.Parallel()
	invalid := []string{
		`<div data-kit-retain="orphan"></div>`,
		`<section data-kit-component="owner@1.0.0"><div data-kit-retain="descendant"></div></section>`,
		`<template><section data-kit-component="probe@1.0.0" data-kit-retain="template-child"></section></template>`,
		`<template data-kit-if="open"><section data-kit-component="probe@1.0.0" data-kit-retain="if-child"></section></template>`,
		`<template data-kit-for="item of items"><section data-kit-component="probe@1.0.0" data-kit-retain="for-child"></section></template>`,
		`<section data-kit-component="outer@1.0.0" data-kit-retain="outer"><section data-kit-component="inner@1.0.0" data-kit-retain="inner"></section></section>`,
	}
	for _, source := range invalid {
		if _, err := ScanHTML([]byte(source)); !errors.Is(err, ErrInvalidComponentUse) {
			t.Errorf("ScanHTML(%q) error = %v, want ErrInvalidComponentUse", source, err)
		}
	}
}

func TestScanHTMLRejectsDuplicateRetainKeysAcrossTheDocument(t *testing.T) {
	t.Parallel()
	source := []byte(`
<section data-kit-component="one@1.0.0" data-kit-retain="app-progress"></section>
<main><section data-kit-component="two@1.0.0" data-kit-retain="app-progress"></section></main>`)
	if _, err := ScanHTML(source); !errors.Is(err, ErrInvalidComponentUse) {
		t.Fatalf("ScanHTML() error = %v, want ErrInvalidComponentUse", err)
	}
}

func TestScanHTMLRetainValidationIgnoresOpaqueContent(t *testing.T) {
	t.Parallel()
	source := []byte(`
<!-- <div data-kit-retain="app-shell"></div> -->
<script>const example = '<section data-kit-component="fake" data-kit-retain="app-shell"></section>';</script>
<section data-kit-ignore data-kit-retain="invalid ignored key" data-kit-component="missing"></section>
<section data-kit-component="real@1.0.0" data-kit-retain="app-shell"></section>`)
	result, err := ScanHTML(source)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Components) != 1 || result.Components[0].Name != "real" {
		t.Fatalf("opaque retain metadata leaked into scan: %#v", result.Components)
	}
}

func TestScanHTMLRetainAncestryMatchesHTMLOptionalEndTags(t *testing.T) {
	t.Parallel()
	for _, source := range []string{
		`<ul><li data-kit-component="row@1.0.0" data-kit-retain="a"><li data-kit-component="row@1.0.0" data-kit-retain="b"></ul>`,
		`<p data-kit-component="notice@1.0.0" data-kit-retain="a"><div data-kit-component="panel@1.0.0" data-kit-retain="b"></div>`,
		`<table><colgroup data-kit-component="columns@1.0.0" data-kit-retain="a"><col><tbody data-kit-component="rows@1.0.0" data-kit-retain="b"><tr><td>row</table>`,
		`<ruby><rb data-kit-component="base@1.0.0" data-kit-retain="a">&#28450;<rt data-kit-component="reading@1.0.0" data-kit-retain="b">kan</ruby>`,
	} {
		result, err := ScanHTML([]byte(source))
		if err != nil {
			t.Fatalf("ScanHTML(%q): %v", source, err)
		}
		if len(result.Components) != 2 {
			t.Fatalf("ScanHTML(%q) components = %#v, want two browser-sibling hosts", source, result.Components)
		}
	}
}

func TestScanHTMLDoesNotTreatNonVoidHTMLElementSlashAsSelfClosing(t *testing.T) {
	t.Parallel()
	source := []byte(`<div data-kit-component="outer@1.0.0" data-kit-retain="outer"/><section data-kit-component="inner@1.0.0" data-kit-retain="inner"></section></div>`)
	if _, err := ScanHTML(source); !errors.Is(err, ErrInvalidComponentUse) {
		t.Fatalf("ScanHTML() error = %v, want nested retain rejection matching the HTML parser", err)
	}

	foreign := []byte(`<svg><g data-kit-component="one@1.0.0" data-kit-retain="one"/><g data-kit-component="two@1.0.0" data-kit-retain="two"/></svg>`)
	result, err := ScanHTML(foreign)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Components) != 2 {
		t.Fatalf("foreign self-closing hosts = %#v, want two", result.Components)
	}
}
