package javascript

import (
	"bytes"
	"errors"
	"testing"
)

const appComponent100SHA256 = "f58f0b4f661aa160e1ae4534646441c8613ef14f7a8c920d9bb9ba033b99ca2c"
const appComponent110SHA256 = "281f44c44bfc85f929ff3ba0d92baf416314e8fa483d96f2340fb6e35b79f1dc"

func TestAppLoaderCatalogPins180AndPreservesEarlierVersions(t *testing.T) {
	if got := ContentHash(readVanillaFile(t, "component", "app", "1.0.0.js")); got != appComponent100SHA256 {
		t.Fatalf("app@1.0.0 bytes changed: %s", got)
	}
	for _, version := range []string{"1.1.0", "1.2.0", "1.3.0", "1.4.0", "1.5.0", "1.6.0", "1.7.0", "1.8.0"} {
		if got := ContentHash(readVanillaFile(t, "component", "app", version+".js")); got != appComponent110SHA256 {
			t.Fatalf("app@%s source bytes changed: %s", version, got)
		}
	}

	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	defaultApp, err := composer.catalog.component("app", "")
	if err != nil {
		t.Fatal(err)
	}
	if defaultApp.identity.Version != "1.1.0" {
		t.Fatalf("default app version = %s, want unchanged 1.1.0", defaultApp.identity.Version)
	}
	canonical110, err := composer.ComposeHTML([]byte(`<html data-kit-component="app@1.1.0" data-kit-alias="$app"></html>`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := composer.ComposeHTML([]byte(`<html data-kit-component="app" data-kit-version="1.1.0" data-kit-alias="$app"></html>`)); !errors.Is(err, ErrUnsupportedAttribute) {
		t.Fatalf("removed split app pin error = %v", err)
	}
	for _, marker := range [][]byte{
		[]byte(`kit.service("progress"`),
		[]byte(`kit.service("capabilities"`),
		[]byte(`kit.service("device"`),
		[]byte(`kit.service("secureStorage"`),
		[]byte(`kit.service("shell"`),
		[]byte(`kit.component("app"`),
		[]byte(`components["app"] = "1.1.0"`),
		[]byte(`grants["app"]["progress"] = "1.0.0"`),
		[]byte(`grants["app"]["capabilities"] = "1.0.0"`),
		[]byte(`grants["app"]["device"] = "1.0.0"`),
		[]byte(`grants["app"]["network"] = "1.0.0"`),
		[]byte(`grants["app"]["secureStorage"] = "1.0.0"`),
		[]byte(`grants["app"]["shell"] = "1.0.0"`),
		[]byte(`services["files"] = "1.0.0"`),
		[]byte(`grants["app"]["files"] = "1.0.0"`),
		[]byte(`actions["progress"]["start"] = true`),
		[]byte(`actions["progress"]["update"] = true`),
		[]byte(`actions["progress"]["finish"] = true`),
	} {
		if !bytes.Contains(canonical110.JavaScript, marker) {
			t.Fatalf("app@1.1.0 artifact omitted %q", marker)
		}
	}

	canonical120, err := composer.ComposeHTML([]byte(`<html data-kit-component="app@1.2.0" data-kit-alias="$app"></html>`))
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range [][]byte{
		[]byte(`kit.service("files"`),
		[]byte(`kit.component("app"`),
		[]byte(`components["app"] = "1.2.0"`),
		[]byte(`services["files"] = "1.1.0"`),
		[]byte(`grants["app"]["files"] = "1.1.0"`),
		[]byte(`services["progress"] = "1.0.0"`),
	} {
		if !bytes.Contains(canonical120.JavaScript, marker) {
			t.Fatalf("app@1.2.0 artifact omitted %q", marker)
		}
	}
	if canonical120.ContentHash == canonical110.ContentHash || bytes.Equal(canonical120.JavaScript, canonical110.JavaScript) {
		t.Fatal("app@1.1.0 and app@1.2.0 shared an artifact identity")
	}
	if bytes.Contains(canonical120.JavaScript, []byte(`export: exportFile`)) {
		t.Fatal("app@1.2.0 changed after files@1.2.0 introduced export")
	}

	canonical130, err := composer.ComposeHTML([]byte(`<html data-kit-component="app@1.3.0" data-kit-alias="$app"></html>`))
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range [][]byte{
		[]byte(`kit.service("files"`),
		[]byte(`kit.component("app"`),
		[]byte(`components["app"] = "1.3.0"`),
		[]byte(`services["files"] = "1.2.0"`),
		[]byte(`grants["app"]["files"] = "1.2.0"`),
		[]byte(`export: exportFile`),
	} {
		if !bytes.Contains(canonical130.JavaScript, marker) {
			t.Fatalf("app@1.3.0 artifact omitted %q", marker)
		}
	}
	if canonical130.ContentHash == canonical120.ContentHash || bytes.Equal(canonical130.JavaScript, canonical120.JavaScript) {
		t.Fatal("app@1.2.0 and app@1.3.0 shared an artifact identity")
	}

	canonical140, err := composer.ComposeHTML([]byte(`<html data-kit-component="app@1.4.0" data-kit-alias="$app"></html>`))
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range [][]byte{
		[]byte(`kit.service("camera"`),
		[]byte(`kit.service("files"`),
		[]byte(`kit.component("app"`),
		[]byte(`components["app"] = "1.4.0"`),
		[]byte(`services["camera"] = "1.0.0"`),
		[]byte(`services["files"] = "1.3.0"`),
		[]byte(`grants["app"]["camera"] = "1.0.0"`),
		[]byte(`grants["app"]["files"] = "1.3.0"`),
	} {
		if !bytes.Contains(canonical140.JavaScript, marker) {
			t.Fatalf("app@1.4.0 artifact omitted %q", marker)
		}
	}
	if canonical140.ContentHash == canonical130.ContentHash || bytes.Equal(canonical140.JavaScript, canonical130.JavaScript) {
		t.Fatal("app@1.3.0 and app@1.4.0 shared an artifact identity")
	}
	if bytes.Contains(canonical140.JavaScript, []byte(`actions["camera"]["`)) {
		t.Fatal("app@1.4.0 exposed camera through authored actions")
	}

	canonical150, err := composer.ComposeHTML([]byte(`<html data-kit-component="app@1.5.0" data-kit-alias="$app"></html>`))
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range [][]byte{
		[]byte(`kit.service("media"`),
		[]byte(`kit.service("qr"`),
		[]byte(`components["app"] = "1.5.0"`),
		[]byte(`services["camera"] = "1.0.0"`),
		[]byte(`services["files"] = "1.3.0"`),
		[]byte(`services["media"] = "1.0.0"`),
		[]byte(`services["qr"] = "1.0.0"`),
		[]byte(`grants["app"]["media"] = "1.0.0"`),
		[]byte(`grants["app"]["qr"] = "1.0.0"`),
	} {
		if !bytes.Contains(canonical150.JavaScript, marker) {
			t.Fatalf("app@1.5.0 artifact omitted %q", marker)
		}
	}
	if canonical150.ContentHash == canonical140.ContentHash || bytes.Equal(canonical150.JavaScript, canonical140.JavaScript) {
		t.Fatal("app@1.4.0 and app@1.5.0 shared an artifact identity")
	}
	if bytes.Contains(canonical150.JavaScript, []byte(`actions["media"]["`)) ||
		bytes.Contains(canonical150.JavaScript, []byte(`actions["qr"]["`)) {
		t.Fatal("app@1.5.0 exposed media or QR through authored actions")
	}

	canonical160, err := composer.ComposeHTML([]byte(`<html data-kit-component="app@1.6.0" data-kit-alias="$app"></html>`))
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range [][]byte{
		[]byte(`kit.service("wakeLock"`),
		[]byte(`components["app"] = "1.6.0"`),
		[]byte(`services["camera"] = "1.0.0"`),
		[]byte(`services["files"] = "1.3.0"`),
		[]byte(`services["media"] = "1.0.0"`),
		[]byte(`services["qr"] = "1.0.0"`),
		[]byte(`services["wakeLock"] = "1.0.0"`),
		[]byte(`grants["app"]["wakeLock"] = "1.0.0"`),
	} {
		if !bytes.Contains(canonical160.JavaScript, marker) {
			t.Fatalf("app@1.6.0 artifact omitted %q", marker)
		}
	}
	if canonical160.ContentHash == canonical150.ContentHash || bytes.Equal(canonical160.JavaScript, canonical150.JavaScript) {
		t.Fatal("app@1.5.0 and app@1.6.0 shared an artifact identity")
	}
	if bytes.Contains(canonical160.JavaScript, []byte(`actions["wakeLock"]["`)) {
		t.Fatal("app@1.6.0 exposed wake lock through authored actions")
	}

	canonical170, err := composer.ComposeHTML([]byte(`<html data-kit-component="app@1.7.0" data-kit-alias="$app"></html>`))
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range [][]byte{
		[]byte(`kit.service("notifications"`),
		[]byte(`components["app"] = "1.7.0"`),
		[]byte(`services["notifications"] = "1.0.0"`),
		[]byte(`services["wakeLock"] = "1.0.0"`),
		[]byte(`grants["app"]["notifications"] = "1.0.0"`),
		[]byte(`grants["app"]["wakeLock"] = "1.0.0"`),
	} {
		if !bytes.Contains(canonical170.JavaScript, marker) {
			t.Fatalf("app@1.7.0 artifact omitted %q", marker)
		}
	}
	if canonical170.ContentHash == canonical160.ContentHash || bytes.Equal(canonical170.JavaScript, canonical160.JavaScript) {
		t.Fatal("app@1.6.0 and app@1.7.0 shared an artifact identity")
	}
	if bytes.Contains(canonical170.JavaScript, []byte(`actions["notifications"]["`)) {
		t.Fatal("app@1.7.0 exposed notifications through authored actions")
	}

	canonical180, err := composer.ComposeHTML([]byte(`<html data-kit-component="app@1.8.0" data-kit-alias="$app"></html>`))
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range [][]byte{
		[]byte(`kit.service("deepLinks"`),
		[]byte(`kit.service("lifecycle"`),
		[]byte(`components["app"] = "1.8.0"`),
		[]byte(`services["deepLinks"] = "1.0.0"`),
		[]byte(`services["lifecycle"] = "1.0.0"`),
		[]byte(`services["notifications"] = "1.0.0"`),
		[]byte(`grants["app"]["deepLinks"] = "1.0.0"`),
		[]byte(`grants["app"]["lifecycle"] = "1.0.0"`),
	} {
		if !bytes.Contains(canonical180.JavaScript, marker) {
			t.Fatalf("app@1.8.0 artifact omitted %q", marker)
		}
	}
	if canonical180.ContentHash == canonical170.ContentHash || bytes.Equal(canonical180.JavaScript, canonical170.JavaScript) {
		t.Fatal("app@1.7.0 and app@1.8.0 shared an artifact identity")
	}
	for _, name := range []string{"deepLinks", "lifecycle"} {
		if bytes.Contains(canonical180.JavaScript, []byte(`actions["`+name+`"]["`)) {
			t.Fatalf("app@1.8.0 exposed %s through authored actions", name)
		}
	}

	files100, err := composer.catalog.service(ServiceVersion{Name: "files", Version: "1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	files110, err := composer.catalog.service(ServiceVersion{Name: "files", Version: "1.1.0"})
	if err != nil {
		t.Fatal(err)
	}
	files120, err := composer.catalog.service(ServiceVersion{Name: "files", Version: "1.2.0"})
	if err != nil {
		t.Fatal(err)
	}
	files130, err := composer.catalog.service(ServiceVersion{Name: "files", Version: "1.3.0"})
	if err != nil {
		t.Fatal(err)
	}
	if files100.identity.Version != "1.0.0" || files110.identity.Version != "1.1.0" ||
		files120.identity.Version != "1.2.0" || files130.identity.Version != "1.3.0" ||
		bytes.Equal(files100.source, files110.source) || bytes.Equal(files110.source, files120.source) ||
		bytes.Equal(files120.source, files130.source) {
		t.Fatal("files service versions are not distinct exact catalog packages")
	}
	if _, err := composer.catalog.service(ServiceVersion{Name: "files", Version: "1.5.0"}); !errors.Is(err, ErrModuleNotFound) {
		t.Fatalf("unknown files service version error = %v", err)
	}
	app110, err := composer.catalog.component("app", "1.1.0")
	if err != nil {
		t.Fatal(err)
	}
	app120, err := composer.catalog.component("app", "1.2.0")
	if err != nil {
		t.Fatal(err)
	}
	app130, err := composer.catalog.component("app", "1.3.0")
	if err != nil {
		t.Fatal(err)
	}
	app140, err := composer.catalog.component("app", "1.4.0")
	if err != nil {
		t.Fatal(err)
	}
	app150, err := composer.catalog.component("app", "1.5.0")
	if err != nil {
		t.Fatal(err)
	}
	app160, err := composer.catalog.component("app", "1.6.0")
	if err != nil {
		t.Fatal(err)
	}
	app170, err := composer.catalog.component("app", "1.7.0")
	if err != nil {
		t.Fatal(err)
	}
	app180, err := composer.catalog.component("app", "1.8.0")
	if err != nil {
		t.Fatal(err)
	}
	versions110 := make(map[string]string, len(app110.requires))
	for _, service := range app110.requires {
		versions110[service.Name] = service.Version
	}
	versions120 := make(map[string]string, len(app120.requires))
	for _, service := range app120.requires {
		versions120[service.Name] = service.Version
	}
	versions130 := make(map[string]string, len(app130.requires))
	for _, service := range app130.requires {
		versions130[service.Name] = service.Version
	}
	versions140 := make(map[string]string, len(app140.requires))
	for _, service := range app140.requires {
		versions140[service.Name] = service.Version
	}
	versions150 := make(map[string]string, len(app150.requires))
	for _, service := range app150.requires {
		versions150[service.Name] = service.Version
	}
	versions160 := make(map[string]string, len(app160.requires))
	for _, service := range app160.requires {
		versions160[service.Name] = service.Version
	}
	versions170 := make(map[string]string, len(app170.requires))
	for _, service := range app170.requires {
		versions170[service.Name] = service.Version
	}
	versions180 := make(map[string]string, len(app180.requires))
	for _, service := range app180.requires {
		versions180[service.Name] = service.Version
	}
	if versions110["files"] != "1.0.0" || versions120["files"] != "1.1.0" ||
		versions130["files"] != "1.2.0" || versions140["files"] != "1.3.0" ||
		versions150["files"] != "1.3.0" || versions160["files"] != "1.3.0" || versions170["files"] != "1.3.0" || versions180["files"] != "1.3.0" {
		t.Fatalf("app files requirements = 1.1:%q 1.2:%q 1.3:%q 1.4:%q 1.5:%q 1.6:%q 1.7:%q 1.8:%q",
			versions110["files"], versions120["files"], versions130["files"], versions140["files"], versions150["files"], versions160["files"], versions170["files"], versions180["files"])
	}
	if versions140["camera"] != "1.0.0" || versions150["camera"] != "1.0.0" || versions160["camera"] != "1.0.0" || versions170["camera"] != "1.0.0" || versions110["camera"] != "" ||
		versions120["camera"] != "" || versions130["camera"] != "" {
		t.Fatalf("app camera requirements = 1.1:%q 1.2:%q 1.3:%q 1.4:%q 1.5:%q 1.6:%q",
			versions110["camera"], versions120["camera"], versions130["camera"], versions140["camera"], versions150["camera"], versions160["camera"])
	}
	if versions150["media"] != "1.0.0" || versions150["qr"] != "1.0.0" ||
		versions160["media"] != "1.0.0" || versions160["qr"] != "1.0.0" || versions170["media"] != "1.0.0" || versions170["qr"] != "1.0.0" ||
		versions140["media"] != "" || versions140["qr"] != "" {
		t.Fatalf("app media/QR requirements = 1.5 media:%q qr:%q; 1.6 media:%q qr:%q",
			versions150["media"], versions150["qr"], versions160["media"], versions160["qr"])
	}
	if versions160["wakeLock"] != "1.0.0" || versions170["wakeLock"] != "1.0.0" || versions150["wakeLock"] != "" {
		t.Fatalf("app wake-lock requirements = 1.5:%q 1.6:%q 1.7:%q", versions150["wakeLock"], versions160["wakeLock"], versions170["wakeLock"])
	}
	if versions170["notifications"] != "1.0.0" || versions160["notifications"] != "" {
		t.Fatalf("app notification requirements = 1.6:%q 1.7:%q", versions160["notifications"], versions170["notifications"])
	}
	if versions180["notifications"] != "1.0.0" || versions180["deepLinks"] != "1.0.0" || versions180["lifecycle"] != "1.0.0" ||
		versions170["deepLinks"] != "" || versions170["lifecycle"] != "" {
		t.Fatalf("app 1.8 service delta notifications=%q deepLinks=%q lifecycle=%q", versions180["notifications"], versions180["deepLinks"], versions180["lifecycle"])
	}
	for name, version := range versions110 {
		if name == "files" {
			continue
		}
		if versions120[name] != version {
			t.Fatalf("app@1.2.0 changed unrelated service %s from %s to %s", name, version, versions120[name])
		}
		if versions130[name] != version {
			t.Fatalf("app@1.3.0 changed unrelated service %s from %s to %s", name, version, versions130[name])
		}
		if versions140[name] != version {
			t.Fatalf("app@1.4.0 changed unrelated service %s from %s to %s", name, version, versions140[name])
		}
		if versions150[name] != version {
			t.Fatalf("app@1.5.0 changed unrelated service %s from %s to %s", name, version, versions150[name])
		}
		if versions160[name] != version {
			t.Fatalf("app@1.6.0 changed unrelated service %s from %s to %s", name, version, versions160[name])
		}
		if versions170[name] != version {
			t.Fatalf("app@1.7.0 changed unrelated service %s from %s to %s", name, version, versions170[name])
		}
		if versions180[name] != version {
			t.Fatalf("app@1.8.0 changed unrelated service %s from %s to %s", name, version, versions180[name])
		}
	}

	explicit100, err := composer.ComposeHTML([]byte(`<html data-kit-component="app@1.0.0" data-kit-alias="$app"></html>`))
	if err != nil {
		t.Fatal(err)
	}
	if explicit100.ContentHash == canonical110.ContentHash || bytes.Equal(explicit100.JavaScript, canonical110.JavaScript) {
		t.Fatal("app@1.0.0 and app@1.1.0 shared an artifact identity")
	}
	for _, marker := range [][]byte{
		[]byte(`services["progress"] =`),
		[]byte(`services["capabilities"] =`),
		[]byte(`services["device"] =`),
		[]byte(`services["network"] =`),
		[]byte(`services["secureStorage"] =`),
		[]byte(`services["shell"] =`),
		[]byte(`grants["app"]["progress"] =`),
		[]byte(`components["app"] = "1.1.0"`),
	} {
		if bytes.Contains(explicit100.JavaScript, marker) {
			t.Fatalf("app@1.0.0 artifact unexpectedly contains %q", marker)
		}
	}
}
