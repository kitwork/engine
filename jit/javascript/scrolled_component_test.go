package javascript

import (
	"bytes"
	"testing"
)

// scrolled@1.0.0 answers one question — has the page moved under this element? — and keeps the
// answer two ways: as reactive state for bindings and as a host attribute for plain CSS. It owns
// its window listener and its pending frame through the boundary and depends on no service.
func TestScrolledComponentCatalogIsSelfContained(t *testing.T) {
	source := readVanillaFile(t, "component", "scrolled", "1.0.0.js")
	if len(source) == 0 || source[0] != ';' || source[len(source)-1] != '\n' || bytes.Contains(source, []byte{'\r'}) {
		t.Fatal("scrolled@1.0.0 is not a sealable LF-only classic script")
	}
	if got := bytes.Count(source, []byte(`kit.component("scrolled"`)); got != 1 {
		t.Fatalf("scrolled@1.0.0 registration count = %d, want 1", got)
	}
	for _, marker := range [][]byte{
		// The listener is the boundary's, on the window the host lives in — never the global one.
		[]byte(`context.listen(data.view, "scroll", function () { measure(scope, data); }, { passive: true })`),
		// The instance table is cleared with the boundary.
		[]byte(`context.cleanup(function () { instances.delete(scope); });`),
		// Both faces of the answer: the attribute CSS reads and the state bindings read — and the DOM
		// is only written when the answer changes, since scroll fires every frame.
		[]byte(`if (data.context.host.getAttribute("data-scrolled") !== flag) {`),
		[]byte(`if (scope.scrolled !== next) scope.scrolled = next;`),
		// The resting state is measured at mount, not assumed — a page opened mid-scroll is right.
		[]byte("    measure(scope, data);\n  },"),
	} {
		if !bytes.Contains(source, marker) {
			t.Fatalf("scrolled@1.0.0 lost contract %q", marker)
		}
	}
	for _, forbidden := range [][]byte{
		[]byte(`window.addEventListener`),
		[]byte(`document.addEventListener`),
		[]byte(`kit.request`),
		[]byte(`kit.clipboard`),
		[]byte(`requestAnimationFrame`),
	} {
		if bytes.Contains(source, forbidden) {
			t.Fatalf("scrolled@1.0.0 must not reach past its boundary: %q", forbidden)
		}
	}
}

func TestScrolledComponentIsInTheCatalog(t *testing.T) {
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatalf("NewDefaultComposer: %v", err)
	}
	if !composer.HasManagedComponent("scrolled") {
		t.Fatal("scrolled is not a managed component")
	}
	source, err := ComponentSource("scrolled", "1.0.0")
	if err != nil || len(source) == 0 {
		t.Fatalf("ComponentSource(scrolled@1.0.0) = %d bytes, %v", len(source), err)
	}
}
