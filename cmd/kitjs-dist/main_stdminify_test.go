//go:build stdminify

package main

import (
	"bytes"
	"testing"

	kitjavascript "github.com/kitwork/engine/jit/javascript"
)

func TestStandardLibraryBuildEmitsValidDistribution(t *testing.T) {
	config, err := parseDistArgs([]string{
		kitjavascript.ReleaseVersion, t.TempDir(), "--component=progress-bar@2.0.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, source, minified, err := composeDistribution(config)
	if err != nil {
		t.Fatal(err)
	}
	if len(minified) == 0 || !bytes.Equal(minified, source) {
		t.Fatalf("stdminify must emit the valid unminified program: source=%d output=%d", len(source), len(minified))
	}
}
