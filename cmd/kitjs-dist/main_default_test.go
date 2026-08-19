//go:build !stdminify

package main

import (
	"testing"

	kitjavascript "github.com/kitwork/engine/jit/javascript"
)

func TestDefaultMinifierShrinksDistribution(t *testing.T) {
	config, err := parseDistArgs([]string{kitjavascript.ReleaseVersion, t.TempDir(), "--component=progress-bar@2.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	_, source, minified, err := composeDistribution(config)
	if err != nil {
		t.Fatal(err)
	}
	if len(minified) >= len(source) {
		t.Fatalf("default minifier did not shrink distribution: source=%d minified=%d", len(source), len(minified))
	}
}
