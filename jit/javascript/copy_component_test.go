package javascript

import (
	"bytes"
	"testing"
)

// copy@1.0.0 is the first common component that consumes a sealed service. It
// owns only a transient "copied" flag and delegates the actual write to the
// clipboard service; the reset timer is disposed with the boundary.
func TestCopyComponentCatalogSealsClipboardService(t *testing.T) {
	source := readVanillaFile(t, "component", "copy", "1.0.0.js")
	if len(source) == 0 || source[0] != ';' || source[len(source)-1] != '\n' || bytes.Contains(source, []byte{'\r'}) {
		t.Fatal("copy@1.0.0 is not a sealable LF-only classic script")
	}
	if got := bytes.Count(source, []byte(`kit.component("copy"`)); got != 1 {
		t.Fatalf("copy@1.0.0 registration count = %d, want 1", got)
	}
	for _, marker := range [][]byte{
		[]byte(`copied: false`),
		[]byte(`kit.clipboard.writeText(payload)`),
		[]byte(`schedule(scope)`),
		[]byte(`context.listen(context.host, "click"`),
		[]byte(`context.owned("[data-copy-trigger]")`),
		[]byte(`context.owned("[data-copy-source]")`),
	} {
		if !bytes.Contains(source, marker) {
			t.Fatalf("copy@1.0.0 lost contract %q", marker)
		}
	}
	for _, forbidden := range [][]byte{
		[]byte(`kit.service(`), []byte(`document.`), []byte(`window.`),
		[]byte(`navigator.`), []byte(`innerHTML`), []byte(`createElement(`),
	} {
		if bytes.Contains(source, forbidden) {
			t.Fatalf("copy@1.0.0 contains out-of-contract ownership %q", forbidden)
		}
	}

	catalog, err := loadDeliveryCatalog()
	if err != nil {
		t.Fatal(err)
	}
	component, err := catalog.component("copy", "")
	if err != nil {
		t.Fatal(err)
	}
	if component.identity != (ComponentVersion{Name: "copy", Version: "1.0.0"}) {
		t.Fatalf("copy default identity = %#v", component.identity)
	}
	if len(component.requires) != 1 || component.requires[0] != (ServiceVersion{Name: "clipboard", Version: "1.0.0"}) {
		t.Fatalf("copy dependencies = %#v, want clipboard@1.0.0", component.requires)
	}

	composer := &Composer{catalog: catalog}
	fromHTML, err := composer.ComposeHTML([]byte(
		`<section data-kit-component="copy@1.0.0"></section>`,
	))
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := composer.ComposeStandalone([]ComponentRef{{Name: "copy", Version: "1.0.0"}}, false)
	if err != nil {
		t.Fatal(err)
	}
	if fromHTML.ContentHash != explicit.ContentHash || !bytes.Equal(fromHTML.JavaScript, explicit.JavaScript) {
		t.Fatal("authored and explicit copy selections produced different artifacts")
	}
	if got := bytes.Count(fromHTML.JavaScript, []byte(`kit.component("copy"`)); got != 1 {
		t.Fatalf("composed copy registration count = %d, want 1", got)
	}
	if got := bytes.Count(fromHTML.JavaScript, []byte(`kit.service("clipboard"`)); got != 1 {
		t.Fatalf("copy artifact clipboard registration count = %d, want 1", got)
	}
	if !bytes.Contains(fromHTML.JavaScript, []byte(`services["clipboard"] = "1.0.0"`)) {
		t.Fatal("composed copy graph lost its exact clipboard version")
	}
}
