package javascript

import (
	"bytes"
	"reflect"
	"testing"
)

const appComponent1100SHA256 = "281f44c44bfc85f929ff3ba0d92baf416314e8fa483d96f2340fb6e35b79f1dc"

func TestApp1100SelectsDeepLinks110WithoutChangingApp190(t *testing.T) {
	if got := ContentHash(readVanillaFile(t, "component", "app", "1.10.0.js")); got != appComponent1100SHA256 {
		t.Fatalf("app@1.10.0 source bytes changed: %s", got)
	}
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := composer.ComposeHTML([]byte(`<html data-kit-component="app@1.9.0" data-kit-alias="$app"></html>`))
	if err != nil {
		t.Fatal(err)
	}
	current, err := composer.ComposeHTML([]byte(`<html data-kit-component="app@1.10.0" data-kit-alias="$app"></html>`))
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range [][]byte{
		[]byte(`components["app"] = "1.10.0"`),
		[]byte(`services["notifications"] = "1.1.0"`),
		[]byte(`services["deepLinks"] = "1.1.0"`),
		[]byte(`grants["app"]["notifications"] = "1.1.0"`),
		[]byte(`grants["app"]["deepLinks"] = "1.1.0"`),
	} {
		if !bytes.Contains(current.JavaScript, marker) {
			t.Fatalf("app@1.10.0 artifact omitted %q", marker)
		}
	}
	if bytes.Contains(current.JavaScript, []byte(`services["deepLinks"] = "1.0.0"`)) ||
		!bytes.Contains(legacy.JavaScript, []byte(`services["deepLinks"] = "1.0.0"`)) ||
		bytes.Contains(legacy.JavaScript, []byte(`services["deepLinks"] = "1.1.0"`)) {
		t.Fatal("app@1.9.0 or app@1.10.0 deep-link service pins drifted")
	}
	if current.ContentHash == legacy.ContentHash || bytes.Equal(current.JavaScript, legacy.JavaScript) {
		t.Fatal("app@1.9.0 and app@1.10.0 shared an artifact identity")
	}
	for _, name := range []string{"deepLinks", "lifecycle", "notifications"} {
		if bytes.Contains(current.JavaScript, []byte(`actions["`+name+`"]["`)) ||
			appGrantsAuthoredService("1.10.0", name) {
			t.Fatalf("app@1.10.0 exposed %s through authored actions", name)
		}
	}

	app190, err := composer.catalog.component("app", "1.9.0")
	if err != nil {
		t.Fatal(err)
	}
	app1100, err := composer.catalog.component("app", "1.10.0")
	if err != nil {
		t.Fatal(err)
	}
	versions190 := serviceVersionMap(app190.requires)
	versions1100 := serviceVersionMap(app1100.requires)
	if versions190["deepLinks"] != "1.0.0" || versions1100["deepLinks"] != "1.1.0" ||
		versions190["notifications"] != "1.1.0" || versions1100["notifications"] != "1.1.0" {
		t.Fatalf("app service pins: 1.9=%#v 1.10=%#v", versions190, versions1100)
	}
	delete(versions190, "deepLinks")
	delete(versions1100, "deepLinks")
	if !reflect.DeepEqual(versions1100, versions190) {
		t.Fatalf("app@1.10.0 changed unrelated service pins: 1.9=%#v 1.10=%#v", versions190, versions1100)
	}
}
