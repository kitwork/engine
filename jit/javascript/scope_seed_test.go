package javascript

import (
	"errors"
	"strings"
	"testing"
)

func TestScopeInitializerFieldsReturnsOnlyTopLevelKeys(t *testing.T) {
	t.Parallel()
	fields, err := scopeInitializerFields(`{count: 1, profile: {storage: true}, items: [{request: false}]}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"count", "profile", "items"} {
		if _, exists := fields[name]; !exists {
			t.Errorf("missing top-level field %q in %#v", name, fields)
		}
	}
	for _, name := range []string{"storage", "request"} {
		if _, exists := fields[name]; exists {
			t.Errorf("nested field %q leaked into top-level fields %#v", name, fields)
		}
	}

	fields, err = scopeInitializerFields(`count: 1; storageKey: "safe"`)
	if err != nil {
		t.Fatal(err)
	}
	if len(fields) != 2 {
		t.Fatalf("shorthand fields = %#v, want two", fields)
	}
}

func TestScanHTMLValidatesScopeSeedContract(t *testing.T) {
	valid := []string{
		`<section data-kit-scope="count: 3; open: true;"></section>`,
		`<section data-kit-scope='{"count": 3, map: {"": 1, "$nested": 2, "kebab-key": 3, "window": 4, "true": 5}, list: [null, false, +.5, -2e3]}'></section>`,
		`<section data-kit-scope="toString: 1; valueOf: 2; hasOwnProperty: 3"></section>`,
		`<section data-kit-component="profile" data-kit-version="1.0.0" data-kit-scope='name: "Ada"'></section>`,
		`<template><section data-kit-scope="count: 1"></section></template>`,
		`<div data-kit-ignore><section data-kit-scope></section></div>`,
	}
	for _, source := range valid {
		result, err := ScanHTML([]byte(source))
		if err != nil {
			t.Errorf("ScanHTML(%q): %v", source, err)
			continue
		}
		if !strings.Contains(source, "data-kit-ignore") && !result.NeedsRuntime {
			t.Errorf("ScanHTML(%q) did not select the runtime", source)
		}
	}

	invalid := []string{
		`<section data-kit-scope></section>`,
		`<section data-kit-scope=""></section>`,
		`<section data-kit-scope="true: 1"></section>`,
		`<section data-kit-scope="window: 1"></section>`,
		`<section data-kit-scope="a$b: 1"></section>`,
		`<section data-kit-scope="count: other"></section>`,
		`<section data-kit-scope="count: [1,]"></section>`,
		`<section data-kit-scope="count: 1; count: 2"></section>`,
		`<section data-kit-scope="map: {&quot;__proto__&quot;: 1}"></section>`,
		`<section data-kit-scope="count: &quot;\uD800&quot;"></section>`,
		`<section data-kit-scope=" count: 1"></section>`,
		`<template data-kit-scope="count: 1"><p></p></template>`,
	}
	for _, source := range invalid {
		if _, err := ScanHTML([]byte(source)); !errors.Is(err, ErrInvalidScopeUse) {
			t.Errorf("ScanHTML(%q) error = %v, want ErrInvalidScopeUse", source, err)
		}
	}
}

func TestScanHTMLScopeSeedBudgetsUseDecodedUTF16(t *testing.T) {
	tooLong := `<section data-kit-scope="value: &quot;` + strings.Repeat("a", scopeSourceLimit) + `&quot;"></section>`
	if _, err := ScanHTML([]byte(tooLong)); !errors.Is(err, ErrInvalidScopeUse) {
		t.Fatalf("overlong decoded scope error = %v, want ErrInvalidScopeUse", err)
	}

	deep := "value: " + strings.Repeat("[", scopeDepthLimit) + "0" + strings.Repeat("]", scopeDepthLimit)
	if _, err := ScanHTML([]byte(`<section data-kit-scope="` + deep + `"></section>`)); !errors.Is(err, ErrInvalidScopeUse) {
		t.Fatalf("over-deep scope error = %v, want ErrInvalidScopeUse", err)
	}

	nodes := `values: [` + strings.TrimSuffix(strings.Repeat("0,", scopeNodeLimit), ",") + `]`
	if _, err := ScanHTML([]byte(`<section data-kit-scope="` + nodes + `"></section>`)); !errors.Is(err, ErrInvalidScopeUse) {
		t.Fatalf("over-node scope error = %v, want ErrInvalidScopeUse", err)
	}
}

func TestScanHTMLRejectsTemplateComponentBoundary(t *testing.T) {
	_, err := ScanHTML([]byte(`<template data-kit-component="row"><p></p></template>`))
	if !errors.Is(err, ErrInvalidComponentUse) {
		t.Fatalf("template component error = %v, want ErrInvalidComponentUse", err)
	}
}
