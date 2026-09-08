package javascript

import (
	"bytes"
	"errors"
	"reflect"
	"testing"
)

func TestApp1120AddsOnlySealedStudioDatabase(t *testing.T) {
	if !bytes.Equal(readVanillaFile(t, "component", "app", "1.11.0.js"),
		readVanillaFile(t, "component", "app", "1.12.0.js")) {
		t.Fatal("app@1.12.0 changed component behavior instead of only closing the database service graph")
	}

	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := composer.ComposeHTML([]byte(`<html data-kit-component="app@1.11.0" data-kit-as="$app"></html>`))
	if err != nil {
		t.Fatal(err)
	}
	current, err := composer.ComposeHTML([]byte(`<html data-kit-component="app@1.12.0" data-kit-as="$app"></html>`))
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range [][]byte{
		[]byte(`components["app"] = "1.12.0"`),
		[]byte(`services["studioDatabase"] = "1.0.0"`),
		[]byte(`grants["app"]["studioDatabase"] = "1.0.0"`),
		[]byte(`services["studioSqlite"] = "1.0.0"`),
	} {
		if !bytes.Contains(current.JavaScript, marker) {
			t.Fatalf("app@1.12.0 artifact omitted %q", marker)
		}
	}
	if bytes.Contains(legacy.JavaScript, []byte(`services["studioDatabase"]`)) ||
		bytes.Contains(legacy.JavaScript, []byte(`kit.service("studioDatabase"`)) {
		t.Fatal("app@1.11.0 changed when app@1.12.0 introduced studioDatabase")
	}
	if bytes.Contains(current.JavaScript, []byte(`actions["studioDatabase"]["`)) ||
		appGrantsAuthoredService("1.12.0", "studioDatabase") ||
		validAuthoredServiceAction("studioDatabase", "open") {
		t.Fatal("studioDatabase escaped into authored HTML actions")
	}

	app1110, err := composer.catalog.component("app", "1.11.0")
	if err != nil {
		t.Fatal(err)
	}
	app1120, err := composer.catalog.component("app", "1.12.0")
	if err != nil {
		t.Fatal(err)
	}
	versions1110 := serviceVersionMap(app1110.requires)
	versions1120 := serviceVersionMap(app1120.requires)
	if versions1120["studioDatabase"] != "1.0.0" {
		t.Fatalf("app@1.12.0 studioDatabase pin = %q", versions1120["studioDatabase"])
	}
	delete(versions1120, "studioDatabase")
	if !reflect.DeepEqual(versions1120, versions1110) {
		t.Fatalf("app@1.12.0 changed unrelated pins: 1.11=%#v 1.12=%#v", versions1110, versions1120)
	}

	service, err := composer.catalog.service(ServiceVersion{Name: "studioDatabase", Version: "1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if len(service.actions) != 0 || len(service.requires) != 0 {
		t.Fatalf("studioDatabase catalog definition = %#v", service)
	}
	if _, err := composer.catalog.service(ServiceVersion{Name: "studioDatabase", Version: "1.0.1"}); !errors.Is(err, ErrModuleNotFound) {
		t.Fatalf("unknown studioDatabase version error = %v", err)
	}
}
