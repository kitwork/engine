package javascript

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func commonNativeServicePackage(t *testing.T, name string) Service {
	t.Helper()
	return Service{
		Name: name, Version: "1.0.0",
		Requires: []ServiceVersion{{Name: "capabilities", Version: "1.0.0"}},
		Source:   readVanillaFile(t, "service", name, "1.0.0.js"),
	}
}

func TestCommonNativeServicesNodeContract(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js is not installed")
	}
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("Cannot locate Node contract")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, node, "--test", filepath.Join(filepath.Dir(source), "testdata", "common_native_services.test.cjs"))
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("Native services Node contract: %v\n%s", err, output)
	}
}

func TestCommonNativeApp1160ClosesExactSealedGraph(t *testing.T) {
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := composer.catalog.component("app", "1.15.0")
	if err != nil {
		t.Fatal(err)
	}
	current, err := composer.catalog.component("app", "1.16.0")
	if err != nil {
		t.Fatal(err)
	}
	oldPins, newPins := serviceVersionMap(legacy.requires), serviceVersionMap(current.requires)
	for _, name := range []string{"biometric", "geolocation", "nfc"} {
		if newPins[name] != "1.0.0" || oldPins[name] != "" {
			t.Fatalf("Unexpected %s version pins", name)
		}
		delete(newPins, name)
		service, err := composer.catalog.service(ServiceVersion{Name: name, Version: "1.0.0"})
		if err != nil || len(service.actions) != 0 {
			t.Fatalf("%s is not sealed: %v", name, err)
		}
		if appGrantsAuthoredService("1.16.0", name) || validAuthoredServiceAction(name, "status") {
			t.Fatalf("%s escaped into authored expressions", name)
		}
		source := readVanillaFile(t, "service", name, "1.0.0.js")
		if bytes.ContainsRune(source, '\r') || len(source) == 0 || source[0] != ';' || source[len(source)-1] != '\n' {
			t.Fatalf("%s is not a sealable LF-only classic script", name)
		}
	}
	if newPins["notifications"] != "1.2.0" || oldPins["notifications"] != "1.1.0" {
		t.Fatal("Notification version upgrade drifted")
	}
	newPins["notifications"] = "1.1.0"
	if !reflect.DeepEqual(oldPins, newPins) {
		t.Fatal("app@1.16.0 changed unrelated service versions")
	}
	if !bytes.Equal(readVanillaFile(t, "component", "app", "1.15.0.js"), readVanillaFile(t, "component", "app", "1.16.0.js")) {
		t.Fatal("app@1.16.0 changed component behavior, not just the service graph")
	}
	artifact, err := composer.ComposeHTML([]byte("<html data-kit-component=\"app@1.16.0\" data-kit-as=\"$app\"></html>"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"biometric", "geolocation", "nfc", "notifications"} {
		if bytes.Contains(artifact.JavaScript, []byte("actions[\""+name+"\"][\"")) || appGrantsAuthoredService("1.16.0", name) {
			t.Fatalf("Native-only %s has authored actions", name)
		}
	}
	for relative, expected := range map[string]string{
		"service/notifications/1.1.0.js": "0994495431dc311b71cc01cc2e86681626dfe8125d7c61c1dfabdcabc44ef122",
		"service/qr/1.0.0.js":            "98c5e0cbcb340b53cce43a9af7ba9ee63007c853000b789a7faf6e57c4098d42",
		"component/app/1.15.0.js":        "281f44c44bfc85f929ff3ba0d92baf416314e8fa483d96f2340fb6e35b79f1dc",
	} {
		if got := fmt.Sprintf("%x", sha256.Sum256(readVanillaFile(t, strings.Split(relative, "/")...))); got != expected {
			t.Fatalf("Frozen predecessor %s changed: %s", relative, got)
		}
	}
}

func TestCommonNativeApp1160CapabilityLab140VersionCompatibility(t *testing.T) {
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	oldLab, err := composer.catalog.component("capability-lab", "1.3.0")
	if err != nil {
		t.Fatal(err)
	}
	newLab, err := composer.catalog.component("capability-lab", "1.4.0")
	if err != nil {
		t.Fatal(err)
	}
	oldPins, newPins := serviceVersionMap(oldLab.requires), serviceVersionMap(newLab.requires)
	if oldPins["notifications"] != "1.1.0" || newPins["notifications"] != "1.2.0" {
		t.Fatalf("Capability lab notification pins drifted: old=%#v new=%#v", oldPins, newPins)
	}
	newPins["notifications"] = "1.1.0"
	if !reflect.DeepEqual(oldPins, newPins) {
		t.Fatal("capability-lab@1.4.0 changed unrelated service versions")
	}
	oldSource := readVanillaFile(t, "component", "capability-lab", "1.3.0.js")
	if got := ContentHash(oldSource); got != "ad4abc5a4d71c1495562bd5a4b8d6c72f9ed5c3f83d4faece3aa1f24cdf38ef8" {
		t.Fatalf("Frozen capability-lab@1.3.0 bytes changed: %s", got)
	}
	if !bytes.Equal(oldSource, readVanillaFile(t, "component", "capability-lab", "1.4.0.js")) {
		t.Fatal("capability-lab@1.4.0 changed component behavior instead of upgrading its service graph")
	}
	page := func(app, lab string) []byte {
		return []byte("<html data-kit-component=\"app@" + app + "\" data-kit-as=\"$app\">" +
			"<main data-kit-component=\"capability-lab@" + lab + "\" data-kit-as=\"$lab\"></main></html>")
	}
	compatible, err := composer.ComposeHTML(page("1.16.0", "1.4.0"))
	if err != nil {
		t.Fatalf("Current app and capability lab must compose: %v", err)
	}
	if !bytes.Contains(compatible.JavaScript, []byte("services[\"notifications\"] = \"1.2.0\"")) ||
		bytes.Contains(compatible.JavaScript, []byte("services[\"notifications\"] = \"1.1.0\"")) {
		t.Fatal("Current combined graph must contain only notifications@1.2.0")
	}
	if _, err := composer.ComposeHTML(page("1.15.0", "1.3.0")); err != nil {
		t.Fatalf("Legacy app/lab compatibility must remain unchanged: %v", err)
	}
	if _, err := composer.ComposeHTML(page("1.16.0", "1.3.0")); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("Mixed notification versions must fail closed, got %v", err)
	}
	options, err := composer.stagedBuildOptions(ScanResult{
		Components:   []ComponentRef{{Name: "app", Version: "1.16.0"}, {Name: "capability-lab", Version: "1.4.0"}},
		NeedsRuntime: true,
	}, ProfileKit, nil)
	if err != nil {
		t.Fatalf("Current staged graph must prepare: %v", err)
	}
	if _, err := BuildStaged(options); err != nil {
		t.Fatalf("Current staged graph must assemble: %v", err)
	}
}
