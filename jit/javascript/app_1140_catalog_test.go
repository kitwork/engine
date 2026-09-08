package javascript

import (
	"bytes"
	"errors"
	"reflect"
	"testing"
)

func TestApp1140UpgradesOnlySealedStudioDatabase(t *testing.T) {
	if !bytes.Equal(readVanillaFile(t, "component", "app", "1.13.0.js"),
		readVanillaFile(t, "component", "app", "1.14.0.js")) {
		t.Fatal("app@1.14.0 changed component behavior instead of only upgrading the Studio database service graph")
	}

	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := composer.ComposeHTML([]byte(`<html data-kit-component="app@1.13.0" data-kit-as="$app"></html>`))
	if err != nil {
		t.Fatal(err)
	}
	current, err := composer.ComposeHTML([]byte(`<html data-kit-component="app@1.14.0" data-kit-as="$app"></html>`))
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range [][]byte{
		[]byte(`components["app"] = "1.14.0"`),
		[]byte(`services["studioState"] = "1.0.0"`),
		[]byte(`services["studioDatabase"] = "1.1.0"`),
		[]byte(`grants["app"]["studioDatabase"] = "1.1.0"`),
	} {
		if !bytes.Contains(current.JavaScript, marker) {
			t.Fatalf("app@1.14.0 artifact omitted %q", marker)
		}
	}
	if !bytes.Contains(legacy.JavaScript, []byte(`services["studioDatabase"] = "1.0.0"`)) ||
		bytes.Contains(legacy.JavaScript, []byte(`services["studioDatabase"] = "1.1.0"`)) {
		t.Fatal("app@1.13.0 changed when app@1.14.0 upgraded studioDatabase")
	}
	if bytes.Contains(current.JavaScript, []byte(`actions["studioDatabase"]["`)) ||
		appGrantsAuthoredService("1.14.0", "studioDatabase") ||
		validAuthoredServiceAction("studioDatabase", "structure") {
		t.Fatal("studioDatabase escaped into authored HTML actions")
	}

	app1130, err := composer.catalog.component("app", "1.13.0")
	if err != nil {
		t.Fatal(err)
	}
	app1140, err := composer.catalog.component("app", "1.14.0")
	if err != nil {
		t.Fatal(err)
	}
	versions1130 := serviceVersionMap(app1130.requires)
	versions1140 := serviceVersionMap(app1140.requires)
	if versions1130["studioDatabase"] != "1.0.0" || versions1140["studioDatabase"] != "1.1.0" {
		t.Fatalf("Studio database pins drifted: 1.13=%q 1.14=%q", versions1130["studioDatabase"], versions1140["studioDatabase"])
	}
	versions1140["studioDatabase"] = "1.0.0"
	if !reflect.DeepEqual(versions1140, versions1130) {
		t.Fatalf("app@1.14.0 changed unrelated pins: 1.13=%#v 1.14=%#v", versions1130, versions1140)
	}

	service, err := composer.catalog.service(ServiceVersion{Name: "studioDatabase", Version: "1.1.0"})
	if err != nil {
		t.Fatal(err)
	}
	if len(service.actions) != 0 || len(service.requires) != 0 {
		t.Fatalf("studioDatabase@1.1.0 catalog definition = %#v", service)
	}
	if _, err := composer.catalog.service(ServiceVersion{Name: "studioDatabase", Version: "1.1.1"}); !errors.Is(err, ErrModuleNotFound) {
		t.Fatalf("unknown studioDatabase version error = %v", err)
	}
}
