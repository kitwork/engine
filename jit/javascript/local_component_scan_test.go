package javascript

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestScanHTMLSeparatesUnversionedClientComponentsFromManagedGraph(t *testing.T) {
	source := []byte(`
<main data-kit-component="app@1.1.0" data-kit-as="$app"></main>
<section data-kit-component="test" data-kit-as="$test" data-kit-scope="{ count: 1 }">
  <button data-kit-click="count = count + 1"></button>
</section>
<aside data-kit-component="notice" data-kit-local=""></aside>`)

	got, err := ScanHTML(source)
	if err != nil {
		t.Fatal(err)
	}
	if !got.NeedsRuntime {
		t.Fatal("explicit local component did not select the runtime")
	}
	wantManaged := []ComponentRef{{Name: "app", Version: "1.1.0", Alias: "$app", Offset: 7}}
	if !reflect.DeepEqual(got.Components, wantManaged) {
		t.Fatalf("managed components = %#v, want %#v", got.Components, wantManaged)
	}
	if len(got.LocalComponents) != 2 {
		t.Fatalf("local components = %#v, want two", got.LocalComponents)
	}
	if first := got.LocalComponents[0]; first.Name != "test" || first.Version != "" || first.Alias != "$test" || first.Retain != "" {
		t.Fatalf("first local component = %#v", first)
	}
	if second := got.LocalComponents[1]; second.Name != "notice" || second.Version != "" || second.Alias != "" || second.Retain != "" {
		t.Fatalf("second local component = %#v", second)
	}

	managedOnly, err := ScanComponents(source)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(managedOnly, wantManaged) {
		t.Fatalf("ScanComponents() = %#v, want managed references only", managedOnly)
	}
}

func TestScanHTMLRejectsAmbiguousLocalComponentMetadata(t *testing.T) {
	tests := []struct {
		name    string
		source  string
		message string
	}{
		{name: "missing host", source: `<section data-kit-local></section>`, message: "requires data-kit-component"},
		{name: "nonempty marker", source: `<section data-kit-component="test" data-kit-local="false"></section>`, message: "empty presence marker"},
		{name: "whitespace marker", source: `<section data-kit-component="test" data-kit-local=" "></section>`, message: "empty presence marker"},
		{name: "duplicate marker", source: `<section data-kit-component="test" data-kit-local data-kit-local></section>`, message: "duplicate data-kit-local"},
		{name: "version", source: `<section data-kit-component="test" data-kit-local data-kit-version="1.0.0"></section>`, message: "cannot use data-kit-version"},
		{name: "inline version", source: `<section data-kit-component="test@1.0.0" data-kit-local></section>`, message: "cannot mark a versioned component"},
		{name: "retain", source: `<section data-kit-component="test" data-kit-local data-kit-retain="test"></section>`, message: "cannot use data-kit-retain"},
		{name: "modifier", source: `<section data-kit-component="test" data-kit-local:once></section>`, message: "only permits modifiers on event attributes"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ScanHTML([]byte(test.source))
			if !errors.Is(err, ErrInvalidComponentUse) && !errors.Is(err, ErrUnsupportedAttribute) {
				t.Fatalf("ScanHTML() error = %v, want component/reserved-attribute rejection", err)
			}
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("ScanHTML() error = %v, want message containing %q", err, test.message)
			}
		})
	}
}

func TestScanHTMLKeepsAliasesStrictAcrossManagedAndLocalComponents(t *testing.T) {
	_, err := ScanHTML([]byte(`
<section data-kit-component="dialog@1.0.0" data-kit-as="$shared"></section>
<section data-kit-component="test" data-kit-as="$shared"></section>`))
	if !errors.Is(err, ErrInvalidComponentUse) || !strings.Contains(err.Error(), "duplicate alias") {
		t.Fatalf("ScanHTML() error = %v, want duplicate alias rejection", err)
	}

	_, err = ScanHTML([]byte(`
<section data-kit-component="app" data-kit-as="$app"></section>
<section data-kit-scope="{ ready: true }" data-kit-click="$app.progress.start()"></section>`))
	if !errors.Is(err, ErrInvalidExpressionUse) || !strings.Contains(err.Error(), "authored service commands require") {
		t.Fatalf("local $app service error = %v, want managed app grant rejection", err)
	}
}

func TestScanHTMLKeepsIgnoredLocalMetadataOpaque(t *testing.T) {
	got, err := ScanHTML([]byte(`
<section data-kit-ignore>
  <div data-kit-local="false" data-kit-version="latest" data-kit-retain="bad key"></div>
</section>
<section data-kit-component="test"></section>`))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Components) != 0 || len(got.LocalComponents) != 1 || got.LocalComponents[0].Name != "test" {
		t.Fatalf("opaque local metadata leaked into scan: %#v", got)
	}
}
