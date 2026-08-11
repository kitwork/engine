package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/kitwork/engine/jit/javascript/vanilla"
)

func TestParseOptionsSelectsCanonicalClosedGraph(t *testing.T) {
	got, err := parseOptions([]string{
		"-profile", "hydrate",
		"-service", `storage=1.0.0=D:\packages\storage.js`,
		"-service", "share=1.0.0=share.js",
		"-service-require", "share=storage=1.0.0",
		"-component", "dialog=1.0.0",
		"-script", "dialog=dialog.js",
		"-canonical-dir", "dist",
	}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if got.profile != vanilla.ProfileHydrate || got.output != "" || got.canonicalDir != "dist" {
		t.Fatalf("options = %#v", got)
	}
	if len(got.services) != 2 || got.services[0].name != "storage" || got.services[0].version != "1.0.0" ||
		got.services[0].path != `D:\packages\storage.js` || len(got.services[0].requires) != 0 {
		t.Fatalf("services = %#v", got.services)
	}
	if got.services[1].name != "share" || got.services[1].version != "1.0.0" || got.services[1].path != "share.js" ||
		len(got.services[1].requires) != 1 || got.services[1].requires[0] != (vanilla.ServiceVersion{Name: "storage", Version: "1.0.0"}) {
		t.Fatalf("share service = %#v", got.services[1])
	}
	if len(got.components) != 1 || got.components[0] != (vanilla.ComponentVersion{Name: "dialog", Version: "1.0.0"}) {
		t.Fatalf("components = %#v", got.components)
	}
	if len(got.scripts) != 1 || got.scripts[0] != (scriptSpec{name: "dialog", path: "dialog.js"}) {
		t.Fatalf("scripts = %#v", got.scripts)
	}
}

func TestParseOptionsRejectsMalformedServices(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "missing path", args: []string{"-service", "storage=1.0.0", "out.js"}},
		{name: "missing version", args: []string{"-service", "storage==storage.js", "out.js"}},
		{name: "duplicate", args: []string{"-service", "storage=1.0.0=a.js", "-service", "storage=2.0.0=b.js", "out.js"}},
		{name: "malformed dependency", args: []string{"-service", "share=1.0.0=share.js", "-service-require", "share=clipboard", "out.js"}},
		{name: "unknown dependency owner", args: []string{"-service-require", "share=clipboard=1.0.0", "out.js"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := parseOptions(test.args, &bytes.Buffer{}); err == nil {
				t.Fatal("parseOptions accepted malformed service flags")
			}
		})
	}
}

func TestWriteArtifactNeverReplacesCanonicalBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kit.0.7.0.hash.js")
	if err := writeArtifact(path, []byte("first"), true); err != nil {
		t.Fatal(err)
	}
	if err := writeArtifact(path, []byte("first"), true); err != nil {
		t.Fatalf("identical immutable write failed: %v", err)
	}
	if err := writeArtifact(path, []byte("second"), true); err == nil {
		t.Fatal("different bytes replaced an immutable artifact")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "first" {
		t.Fatalf("immutable artifact = %q, want first", got)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(path) {
		t.Fatalf("artifact directory entries = %v, want only %s", entries, filepath.Base(path))
	}
}
