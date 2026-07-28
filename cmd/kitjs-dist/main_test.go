package main

import (
	"testing"

	"github.com/kitwork/engine/jit/hydrate"
	"github.com/kitwork/engine/utilities/minifier"
)

func TestComposedRuntimeMinifies(t *testing.T) {
	source := hydrate.Runtime()
	minified := minifier.JS(source)
	if minified == "" {
		t.Fatal("composed runtime minified to an empty artifact")
	}
	if len(minified) >= len(source) {
		t.Fatalf("minified runtime is not smaller: source=%d minified=%d", len(source), len(minified))
	}
	t.Logf("composed runtime: source=%d bytes, minified=%d bytes", len(source), len(minified))
}
