package javascript

import (
	"bytes"
	"testing"
)

const appComponent100SHA256 = "f58f0b4f661aa160e1ae4534646441c8613ef14f7a8c920d9bb9ba033b99ca2c"

func TestAppLoaderCatalogPins110AndPreserves100(t *testing.T) {
	if got := ContentHash(readVanillaFile(t, "component", "app", "1.0.0.js")); got != appComponent100SHA256 {
		t.Fatalf("app@1.0.0 bytes changed: %s", got)
	}

	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	canonical110, err := composer.ComposeHTML([]byte(`<html data-kit-component="app@1.1.0" data-kit-as="$app"></html>`))
	if err != nil {
		t.Fatal(err)
	}
	legacy110, err := composer.ComposeHTML([]byte(`<html data-kit-component="app" data-kit-version="1.1.0" data-kit-as="$app"></html>`))
	if err != nil {
		t.Fatal(err)
	}
	if canonical110.ContentHash != legacy110.ContentHash || !bytes.Equal(canonical110.JavaScript, legacy110.JavaScript) {
		t.Fatal("canonical and legacy app@1.1.0 pins resolved differently")
	}
	for _, marker := range [][]byte{
		[]byte(`kit.service("progress"`),
		[]byte(`kit.component("app"`),
		[]byte(`components["app"] = "1.1.0"`),
		[]byte(`grants["app"]["progress"] = "1.0.0"`),
		[]byte(`actions["progress"]["start"] = true`),
		[]byte(`actions["progress"]["update"] = true`),
		[]byte(`actions["progress"]["finish"] = true`),
	} {
		if !bytes.Contains(canonical110.JavaScript, marker) {
			t.Fatalf("app@1.1.0 artifact omitted %q", marker)
		}
	}

	explicit100, err := composer.ComposeHTML([]byte(`<html data-kit-component="app@1.0.0" data-kit-as="$app"></html>`))
	if err != nil {
		t.Fatal(err)
	}
	if explicit100.ContentHash == canonical110.ContentHash || bytes.Equal(explicit100.JavaScript, canonical110.JavaScript) {
		t.Fatal("app@1.0.0 and app@1.1.0 shared an artifact identity")
	}
	for _, marker := range [][]byte{
		[]byte(`services["progress"] =`),
		[]byte(`grants["app"]["progress"] =`),
		[]byte(`components["app"] = "1.1.0"`),
	} {
		if bytes.Contains(explicit100.JavaScript, marker) {
			t.Fatalf("app@1.0.0 artifact unexpectedly contains %q", marker)
		}
	}
}
