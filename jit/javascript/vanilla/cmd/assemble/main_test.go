package main

import (
	"bytes"
	"testing"

	"github.com/kitwork/engine/jit/javascript/vanilla"
)

func TestParseOptionsPreservesLegacyKitCommand(t *testing.T) {
	got, err := parseOptions([]string{"kit.js"}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if got.profile != vanilla.ProfileKit || got.output != "kit.js" {
		t.Fatalf("options = %#v, want kit profile and legacy positional output", got)
	}
}

func TestParseOptionsSelectsHydrateProfileAndNamedOutput(t *testing.T) {
	got, err := parseOptions([]string{
		"-profile", "hydrate",
		"-output", "hydrate.kit.js",
	}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if got.profile != vanilla.ProfileHydrate || got.output != "hydrate.kit.js" {
		t.Fatalf("options = %#v, want hydrate profile and named output", got)
	}
}

func TestParseOptionsRejectsUnknownProfileAndAmbiguousOutput(t *testing.T) {
	tests := [][]string{
		{"-profile", "unknown", "artifact.js"},
		{"-output", "one.js", "two.js"},
		{},
	}
	for _, args := range tests {
		if _, err := parseOptions(args, &bytes.Buffer{}); err == nil {
			t.Fatalf("parseOptions(%q) succeeded, want error", args)
		}
	}
}
