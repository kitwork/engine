package javascript

import (
	"bytes"
	"testing"
)

// otp@1.0.0 owns segmented code inputs: auto-advance, paste distribution, and
// backspace navigation over the boundary's [data-otp-slot] children. It is
// dependency-free and disposes its owned listeners with the boundary.
func TestOTPComponentCatalogAndOwnershipContract(t *testing.T) {
	source := readVanillaFile(t, "component", "otp", "1.0.0.js")
	if len(source) == 0 || source[0] != ';' || source[len(source)-1] != '\n' || bytes.Contains(source, []byte{'\r'}) {
		t.Fatal("otp@1.0.0 is not a sealable LF-only classic script")
	}
	if got := bytes.Count(source, []byte(`kit.component("otp"`)); got != 1 {
		t.Fatalf("otp@1.0.0 registration count = %d, want 1", got)
	}
	for _, marker := range [][]byte{
		[]byte(`context.owned("[data-otp-slot]")`),
		[]byte(`context.listen(context.host, "input"`),
		[]byte(`context.listen(context.host, "keydown"`),
		[]byte(`context.listen(context.host, "paste"`),
		[]byte(`context.cleanup(`),
		[]byte(`isComplete: function`),
	} {
		if !bytes.Contains(source, marker) {
			t.Fatalf("otp@1.0.0 lost contract %q", marker)
		}
	}
	for _, forbidden := range [][]byte{
		[]byte(`kit.service(`), []byte(`document.querySelector`), []byte(`window.`),
		[]byte(`innerHTML`), []byte(`createElement(`),
	} {
		if bytes.Contains(source, forbidden) {
			t.Fatalf("otp@1.0.0 contains out-of-contract ownership %q", forbidden)
		}
	}

	catalog, err := loadDeliveryCatalog()
	if err != nil {
		t.Fatal(err)
	}
	component, err := catalog.component("otp", "")
	if err != nil {
		t.Fatal(err)
	}
	if component.identity != (ComponentVersion{Name: "otp", Version: "1.0.0"}) {
		t.Fatalf("otp default identity = %#v", component.identity)
	}
	if len(component.requires) != 0 || !bytes.Equal(component.source, source) {
		t.Fatal("otp catalog changed its dependency-free frozen source")
	}

	composer := &Composer{catalog: catalog}
	fromHTML, err := composer.ComposeHTML([]byte(
		`<section data-kit-component="otp@1.0.0"></section>`,
	))
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := composer.ComposeStandalone([]ComponentRef{{Name: "otp", Version: "1.0.0"}}, false)
	if err != nil {
		t.Fatal(err)
	}
	if fromHTML.ContentHash != explicit.ContentHash || !bytes.Equal(fromHTML.JavaScript, explicit.JavaScript) {
		t.Fatal("authored and explicit otp selections produced different artifacts")
	}
	if bytes.Contains(fromHTML.JavaScript, []byte(`kit.service(`)) {
		t.Fatal("otp artifact unexpectedly sealed a service")
	}
}
