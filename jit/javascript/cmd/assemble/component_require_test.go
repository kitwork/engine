package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/kitwork/engine/jit/javascript"
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
	want := javascript.ComponentServiceRequirement{
		Component: "progress-bar",
		Service:   javascript.ServiceVersion{Name: "progress", Version: "1.0.0"},
	}
	if len(got.componentReq) != 1 || got.componentReq[0] != want {
		t.Fatalf("component requirements = %#v, want %#v", got.componentReq, want)
	}
	if len(got.components) != 1 || got.components[0] != (javascript.ComponentVersion{Name: "progress-bar", Version: "1.2.0"}) {
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

func TestParseOptionsAcceptsServiceAuthoredActions(t *testing.T) {
	got, err := parseOptions([]string{
		"-service", "storage=1.0.0=service/storage/1.0.0.js",
		"-service-action", "storage=set",
		"-service-action", "storage=remove",
		"out.js",
	}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.services) != 1 || strings.Join(got.services[0].actions, ",") != "set,remove" {
		t.Fatalf("service authored actions = %#v", got.services)
	}
}

func TestParseOptionsRejectsMalformedOrOwnerlessServiceAction(t *testing.T) {
	for _, args := range [][]string{
		{"-service", "storage=1.0.0=storage.js", "-service-action", "storage", "out.js"},
		{"-service-action", "storage=set", "out.js"},
	} {
		if _, err := parseOptions(args, &bytes.Buffer{}); err == nil {
			t.Fatalf("parseOptions(%q) succeeded", args)
		}
	}
}
