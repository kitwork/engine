//go:build !stdminify

package main

import "testing"

func TestDefaultMinifierShrinksDistribution(t *testing.T) {
	config, err := parseDistArgs([]string{"1.0.0", t.TempDir(), "--component=dropdown@1.0.0"})
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
