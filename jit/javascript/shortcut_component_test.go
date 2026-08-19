package javascript

import (
	"bytes"
	"testing"
)

const shortcutComponent100SHA256 = "afe02b233676eb7598d569bdba1e884be296521d8aa4cf5a9487c4a9298059ce"

func TestShortcutComponentCatalogAndFrozenSource(t *testing.T) {
	source := readVanillaFile(t, "component", "shortcut", "1.0.0.js")
	if got := ContentHash(source); got != shortcutComponent100SHA256 {
		t.Fatalf("shortcut@1.0.0 bytes changed: %s", got)
	}
	for _, marker := range [][]byte{
		[]byte(`kit.component("shortcut"`),
		[]byte(`host.getAttribute("data-shortcut") !== "mod+k"`),
		[]byte(`context.listen(host.ownerDocument, "keydown"`),
		[]byte(`event.preventDefault()`),
		[]byte(`host.click()`),
	} {
		if !bytes.Contains(source, marker) {
			t.Fatalf("shortcut@1.0.0 lost contract %q", marker)
		}
	}
	for _, forbidden := range [][]byte{
		[]byte(`kit.service(`), []byte(`querySelector(`), []byte(`location.`),
		[]byte(`history.`), []byte(`data-search`),
	} {
		if bytes.Contains(source, forbidden) {
			t.Fatalf("shortcut@1.0.0 contains out-of-contract ownership %q", forbidden)
		}
	}

	catalog, err := loadDeliveryCatalog()
	if err != nil {
		t.Fatal(err)
	}
	component, err := catalog.component("shortcut", "")
	if err != nil {
		t.Fatal(err)
	}
	if component.identity != (ComponentVersion{Name: "shortcut", Version: "1.0.0"}) {
		t.Fatalf("shortcut default identity = %#v", component.identity)
	}
	if len(component.requires) != 0 || !bytes.Equal(component.source, source) {
		t.Fatal("shortcut catalog changed its dependency-free frozen source")
	}

	composer := &Composer{catalog: catalog}
	fromHTML, err := composer.ComposeHTML([]byte(
		`<a href="/search" data-kit-component="shortcut@1.0.0" data-shortcut="mod+k">Search</a>`,
	))
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := composer.ComposeStandalone([]ComponentRef{{Name: "shortcut", Version: "1.0.0"}}, false)
	if err != nil {
		t.Fatal(err)
	}
	if fromHTML.ContentHash != explicit.ContentHash || !bytes.Equal(fromHTML.JavaScript, explicit.JavaScript) {
		t.Fatal("authored and explicit shortcut selections produced different artifacts")
	}
	if got := bytes.Count(fromHTML.JavaScript, []byte(`kit.component("shortcut"`)); got != 1 {
		t.Fatalf("composed shortcut registration count = %d, want 1", got)
	}
	if bytes.Contains(fromHTML.JavaScript, []byte(`kit.service(`)) {
		t.Fatal("shortcut artifact unexpectedly sealed a service")
	}
}
