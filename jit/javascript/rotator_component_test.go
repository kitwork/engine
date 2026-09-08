package javascript

import (
	"bytes"
	"testing"
)

// rotator@1.0.0 is carousel plus a timer. `carousel` owns no lifecycle at all, so a region that
// advances on its own had to be hand-written on the page; this package owns the interval and, just
// as importantly, every reason to stop it. It reaches the document only through the boundary it was
// given (context.host / ownerDocument) and depends on no service.
func TestRotatorComponentCatalogIsSelfContained(t *testing.T) {
	source := readVanillaFile(t, "component", "rotator", "1.0.0.js")
	if len(source) == 0 || source[0] != ';' || source[len(source)-1] != '\n' || bytes.Contains(source, []byte{'\r'}) {
		t.Fatal("rotator@1.0.0 is not a sealable LF-only classic script")
	}
	if got := bytes.Count(source, []byte(`kit.component("rotator"`)); got != 1 {
		t.Fatalf("rotator@1.0.0 registration count = %d, want 1", got)
	}
	for _, marker := range [][]byte{
		// The four independent hold reasons. Losing any one of them turns the rotator into content
		// that keeps moving while it is being read, hidden, or explicitly unwanted.
		[]byte(`data.hovered || data.focused || data.hidden || data.reduced`),
		[]byte(`context.listen(context.host, "pointerenter"`),
		[]byte(`context.listen(context.host, "focusin"`),
		[]byte(`"visibilitychange"`),
		[]byte(`"(prefers-reduced-motion: reduce)"`),
		// The timer must be owned by the boundary, or a removed rotator keeps ticking.
		[]byte(`context.cleanup(`),
		[]byte(`clearTimeout(data.timer)`),
		// Item count comes from bound data OR authored markup.
		[]byte(`context.owned("[data-rotator-item]")`),
		// The hold reasons are private to the instance, so the fact that the rotator is stopped has to
		// reach bindings as reactive state; a method reading those flags would answer correctly and
		// never re-render the control showing it.
		[]byte(`if (scope.running !== running) scope.running = running;`),
	} {
		if !bytes.Contains(source, marker) {
			t.Fatalf("rotator@1.0.0 lost contract %q", marker)
		}
	}
	for _, forbidden := range [][]byte{
		[]byte(`kit.service(`), []byte(`document.`), []byte(`window.`),
		[]byte(`navigator.`), []byte(`innerHTML`), []byte(`createElement(`),
		// setInterval would keep firing while a tab is throttled and drift against the work it
		// queues; the package re-arms a setTimeout after each advance instead.
		[]byte(`setInterval(`),
	} {
		if bytes.Contains(source, forbidden) {
			t.Fatalf("rotator@1.0.0 contains out-of-contract ownership %q", forbidden)
		}
	}

	catalog, err := loadDeliveryCatalog()
	if err != nil {
		t.Fatal(err)
	}
	component, err := catalog.component("rotator", "")
	if err != nil {
		t.Fatal(err)
	}
	if component.identity != (ComponentVersion{Name: "rotator", Version: "1.0.0"}) {
		t.Fatalf("rotator default identity = %#v", component.identity)
	}
	if len(component.requires) != 0 {
		t.Fatalf("rotator dependencies = %#v, want none", component.requires)
	}

	composer := &Composer{catalog: catalog}
	fromHTML, err := composer.ComposeHTML([]byte(
		`<section data-kit-component="rotator@1.0.0"></section>`,
	))
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := composer.ComposeStandalone([]ComponentRef{{Name: "rotator", Version: "1.0.0"}}, false)
	if err != nil {
		t.Fatal(err)
	}
	if fromHTML.ContentHash != explicit.ContentHash || !bytes.Equal(fromHTML.JavaScript, explicit.JavaScript) {
		t.Fatal("authored and explicit rotator selections produced different artifacts")
	}
	if got := bytes.Count(fromHTML.JavaScript, []byte(`kit.component("rotator"`)); got != 1 {
		t.Fatalf("composed rotator registration count = %d, want 1", got)
	}
	// A page that never writes the name must not pay for it.
	other, err := composer.ComposeHTML([]byte(`<section data-kit-component="carousel@1.0.0"></section>`))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(other.JavaScript, []byte(`kit.component("rotator"`)) {
		t.Fatal("a carousel-only document shipped the rotator package")
	}
}
