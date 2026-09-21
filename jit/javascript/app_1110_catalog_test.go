package javascript

import (
	"bytes"
	"errors"
	"reflect"
	"testing"
)

const appComponent1110SHA256 = "281f44c44bfc85f929ff3ba0d92baf416314e8fa483d96f2340fb6e35b79f1dc"

func TestApp1110AddsOnlySealedStudioSQLite(t *testing.T) {
	if got := ContentHash(readVanillaFile(t, "component", "app", "1.11.0.js")); got != appComponent1110SHA256 {
		t.Fatalf("app@1.11.0 source bytes changed: %s", got)
	}
	if !bytes.Equal(readVanillaFile(t, "component", "app", "1.10.0.js"),
		readVanillaFile(t, "component", "app", "1.11.0.js")) {
		t.Fatal("app@1.11.0 changed component behavior instead of only closing the SQLite service graph")
	}

	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := composer.ComposeHTML([]byte(`<html data-kit-component="app@1.10.0" data-kit-alias="$app"></html>`))
	if err != nil {
		t.Fatal(err)
	}
	current, err := composer.ComposeHTML([]byte(`<html data-kit-component="app@1.11.0" data-kit-alias="$app"></html>`))
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range [][]byte{
		[]byte(`components["app"] = "1.11.0"`),
		[]byte(`services["studioSqlite"] = "1.0.0"`),
		[]byte(`grants["app"]["studioSqlite"] = "1.0.0"`),
		[]byte(`services["files"] = "1.3.0"`),
		[]byte(`services["notifications"] = "1.1.0"`),
		[]byte(`services["deepLinks"] = "1.1.0"`),
	} {
		if !bytes.Contains(current.JavaScript, marker) {
			t.Fatalf("app@1.11.0 artifact omitted %q", marker)
		}
	}
	if bytes.Contains(legacy.JavaScript, []byte(`services["studioSqlite"]`)) ||
		bytes.Contains(legacy.JavaScript, []byte(`kit.service("studioSqlite"`)) {
		t.Fatal("app@1.10.0 changed when app@1.11.0 introduced studioSqlite")
	}
	if bytes.Contains(current.JavaScript, []byte(`actions["studioSqlite"]["`)) ||
		appGrantsAuthoredService("1.11.0", "studioSqlite") ||
		validAuthoredServiceAction("studioSqlite", "open") {
		t.Fatal("studioSqlite escaped into authored HTML actions")
	}

	app1100, err := composer.catalog.component("app", "1.10.0")
	if err != nil {
		t.Fatal(err)
	}
	app1110, err := composer.catalog.component("app", "1.11.0")
	if err != nil {
		t.Fatal(err)
	}
	versions1100 := serviceVersionMap(app1100.requires)
	versions1110 := serviceVersionMap(app1110.requires)
	if versions1110["studioSqlite"] != "1.0.0" {
		t.Fatalf("app@1.11.0 studioSqlite pin = %q", versions1110["studioSqlite"])
	}
	delete(versions1110, "studioSqlite")
	if !reflect.DeepEqual(versions1110, versions1100) {
		t.Fatalf("app@1.11.0 changed unrelated pins: 1.10=%#v 1.11=%#v", versions1100, versions1110)
	}

	service, err := composer.catalog.service(ServiceVersion{Name: "studioSqlite", Version: "1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if len(service.actions) != 0 || len(service.requires) != 0 {
		t.Fatalf("studioSqlite catalog definition = %#v", service)
	}
	if _, err := composer.catalog.service(ServiceVersion{Name: "studioSqlite", Version: "1.0.1"}); !errors.Is(err, ErrModuleNotFound) {
		t.Fatalf("unknown studioSqlite version error = %v", err)
	}
}
