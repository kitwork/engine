package javascript

import (
	"bytes"
	"errors"
	"reflect"
	"testing"
)

func TestApp1130AddsOnlySealedStudioState(t *testing.T) {
	if !bytes.Equal(readVanillaFile(t, "component", "app", "1.12.0.js"),
		readVanillaFile(t, "component", "app", "1.13.0.js")) {
		t.Fatal("app@1.13.0 changed component behavior instead of only closing the Studio state service graph")
	}

	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := composer.ComposeHTML([]byte(`<html data-kit-component="app@1.12.0" data-kit-alias="$app"></html>`))
	if err != nil {
		t.Fatal(err)
	}
	current, err := composer.ComposeHTML([]byte(`<html data-kit-component="app@1.13.0" data-kit-alias="$app"></html>`))
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range [][]byte{
		[]byte(`components["app"] = "1.13.0"`),
		[]byte(`services["studioState"] = "1.0.0"`),
		[]byte(`grants["app"]["studioState"] = "1.0.0"`),
		[]byte(`services["studioDatabase"] = "1.0.0"`),
	} {
		if !bytes.Contains(current.JavaScript, marker) {
			t.Fatalf("app@1.13.0 artifact omitted %q", marker)
		}
	}
	if bytes.Contains(legacy.JavaScript, []byte(`services["studioState"]`)) ||
		bytes.Contains(legacy.JavaScript, []byte(`kit.service("studioState"`)) {
		t.Fatal("app@1.12.0 changed when app@1.13.0 introduced studioState")
	}
	if bytes.Contains(current.JavaScript, []byte(`actions["studioState"]["`)) ||
		appGrantsAuthoredService("1.13.0", "studioState") ||
		validAuthoredServiceAction("studioState", "loadSnapshot") {
		t.Fatal("studioState escaped into authored HTML actions")
	}

	app1120, err := composer.catalog.component("app", "1.12.0")
	if err != nil {
		t.Fatal(err)
	}
	app1130, err := composer.catalog.component("app", "1.13.0")
	if err != nil {
		t.Fatal(err)
	}
	versions1120 := serviceVersionMap(app1120.requires)
	versions1130 := serviceVersionMap(app1130.requires)
	if versions1130["studioState"] != "1.0.0" {
		t.Fatalf("app@1.13.0 studioState pin = %q", versions1130["studioState"])
	}
	delete(versions1130, "studioState")
	if !reflect.DeepEqual(versions1130, versions1120) {
		t.Fatalf("app@1.13.0 changed unrelated pins: 1.12=%#v 1.13=%#v", versions1120, versions1130)
	}

	service, err := composer.catalog.service(ServiceVersion{Name: "studioState", Version: "1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if len(service.actions) != 0 || len(service.requires) != 0 {
		t.Fatalf("studioState catalog definition = %#v", service)
	}
	if _, err := composer.catalog.service(ServiceVersion{Name: "studioState", Version: "1.0.1"}); !errors.Is(err, ErrModuleNotFound) {
		t.Fatalf("unknown studioState version error = %v", err)
	}
}
