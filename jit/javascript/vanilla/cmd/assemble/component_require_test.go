package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/kitwork/engine/jit/javascript/vanilla"
)

func TestParseOptionsAcceptsExactComponentServiceRequirement(t *testing.T) {
	got, err := parseOptions([]string{
		"-profile", "hydrate",
		"-service", "progress=1.0.0=service/progress/1.0.0.js",
		"-component", "progress-bar=1.2.0",
		"-component-require", "progress-bar=progress=1.0.0",
		"-script", "progress-bar=component/progress-bar/1.2.0.js",
		"out.js",
	}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	want := vanilla.ComponentServiceRequirement{
		Component: "progress-bar",
		Service:   vanilla.ServiceVersion{Name: "progress", Version: "1.0.0"},
	}
	if len(got.componentReq) != 1 || got.componentReq[0] != want {
		t.Fatalf("component requirements = %#v, want %#v", got.componentReq, want)
	}
	if len(got.components) != 1 || got.components[0] != (vanilla.ComponentVersion{Name: "progress-bar", Version: "1.2.0"}) {
		t.Fatalf("component pins = %#v", got.components)
	}
}

func TestParseOptionsRejectsMalformedOrOwnerlessComponentRequirement(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		contains string
	}{
		{
			name:     "missing version",
			args:     []string{"-component", "progress-bar=1.2.0", "-component-require", "progress-bar=progress", "out.js"},
			contains: "want owner=service=version",
		},
		{
			name:     "missing owner pin",
			args:     []string{"-component-require", "progress-bar=progress=1.0.0", "out.js"},
			contains: "has no -component pin",
		},
		{
			name:     "duplicate component pin",
			args:     []string{"-component", "progress-bar=1.2.0", "-component", "progress-bar=1.2.0", "out.js"},
			contains: "duplicate component",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := parseOptions(test.args, &bytes.Buffer{})
			if err == nil || !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("parseOptions error = %v, want containing %q", err, test.contains)
			}
		})
	}
}
