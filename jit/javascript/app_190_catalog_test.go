package javascript

import (
	"bytes"
	"reflect"
	"testing"
)

func TestApp190SelectsOnlyNotificationService110(t *testing.T) {
	if got := ContentHash(readVanillaFile(t, "component", "app", "1.9.0.js")); got != appComponent110SHA256 {
		t.Fatalf("app@1.9.0 source bytes changed from the canonical loader: %s", got)
	}
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := composer.ComposeHTML([]byte(`<html data-kit-component="app@1.8.0" data-kit-alias="$app"></html>`))
	if err != nil {
		t.Fatal(err)
	}
	current, err := composer.ComposeHTML([]byte(`<html data-kit-component="app@1.9.0" data-kit-alias="$app"></html>`))
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range [][]byte{
		[]byte(`components["app"] = "1.9.0"`),
		[]byte(`services["notifications"] = "1.1.0"`),
		[]byte(`grants["app"]["notifications"] = "1.1.0"`),
		[]byte(`services["deepLinks"] = "1.0.0"`),
		[]byte(`services["lifecycle"] = "1.0.0"`),
		[]byte(`services["files"] = "1.3.0"`),
	} {
		if !bytes.Contains(current.JavaScript, marker) {
			t.Fatalf("app@1.9.0 artifact omitted %q", marker)
		}
	}
	if bytes.Contains(current.JavaScript, []byte(`services["notifications"] = "1.0.0"`)) {
		t.Fatal("app@1.9.0 selected both notification service versions")
	}
	if !bytes.Contains(legacy.JavaScript, []byte(`services["notifications"] = "1.0.0"`)) ||
		bytes.Contains(legacy.JavaScript, []byte(`services["notifications"] = "1.1.0"`)) {
		t.Fatal("app@1.8.0 notification service pin changed")
	}
	if current.ContentHash == legacy.ContentHash || bytes.Equal(current.JavaScript, legacy.JavaScript) {
		t.Fatal("app@1.8.0 and app@1.9.0 shared an artifact identity")
	}
	for _, name := range []string{"deepLinks", "lifecycle", "notifications"} {
		if bytes.Contains(current.JavaScript, []byte(`actions["`+name+`"]["`)) ||
			appGrantsAuthoredService("1.9.0", name) {
			t.Fatalf("app@1.9.0 exposed %s through authored actions", name)
		}
	}

	app180, err := composer.catalog.component("app", "1.8.0")
	if err != nil {
		t.Fatal(err)
	}
	app190, err := composer.catalog.component("app", "1.9.0")
	if err != nil {
		t.Fatal(err)
	}
	versions180 := serviceVersionMap(app180.requires)
	versions190 := serviceVersionMap(app190.requires)
	if versions180["notifications"] != "1.0.0" || versions190["notifications"] != "1.1.0" {
		t.Fatalf("notification pins: app@1.8.0=%q app@1.9.0=%q",
			versions180["notifications"], versions190["notifications"])
	}
	delete(versions180, "notifications")
	delete(versions190, "notifications")
	if !reflect.DeepEqual(versions190, versions180) {
		t.Fatalf("app@1.9.0 changed unrelated service pins: 1.8=%#v 1.9=%#v", versions180, versions190)
	}
}

func serviceVersionMap(services []ServiceVersion) map[string]string {
	versions := make(map[string]string, len(services))
	for _, service := range services {
		versions[service.Name] = service.Version
	}
	return versions
}
