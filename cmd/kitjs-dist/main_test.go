package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	kitjavascript "github.com/kitwork/engine/jit/javascript"
)

func TestDistributionMatchesNativeComposerGraph(t *testing.T) {
	config, err := parseDistArgs([]string{
		"1.0.0", t.TempDir(), "--drive", "--component", "theme@v1.0.0", "--component=dialog",
	})
	if err != nil {
		t.Fatal(err)
	}
	bundle, source, minified, err := composeDistribution(config)
	if err != nil {
		t.Fatal(err)
	}
	composer, err := kitjavascript.NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	native, err := composer.ComposeHTML([]byte(
		`<html data-kit-app="standalone"><main data-kit-component="theme@1.0.0"></main>` +
			`<aside data-kit-component="dialog"></aside></html>`,
	))
	if err != nil {
		t.Fatal(err)
	}
	if bundle.ContentHash != native.ContentHash || !bytes.Equal(bundle.JavaScript, native.JavaScript) {
		t.Fatal("standalone distribution graph drifted from Kitwork-native composition")
	}
	if !bytes.HasSuffix(source, bundle.JavaScript) || !bytes.Contains(source, []byte(bundle.ContentHash)) {
		t.Fatal("readable distribution omitted the exact composed graph or fingerprint")
	}
	if len(minified) == 0 || !bytes.Contains(minified, []byte(bundle.ContentHash)) {
		t.Fatalf("invalid minified distribution: source=%d minified=%d", len(source), len(minified))
	}
	outputs, err := writeDistribution(config.outdir, bundle, source, minified)
	if err != nil {
		t.Fatal(err)
	}
	if len(outputs) != 5 {
		t.Fatalf("outputs=%d, want aliases, canonical artifacts, and snippet", len(outputs))
	}
	sourceHash := fmt.Sprintf("%x", sha256.Sum256(source))
	minifiedHash := fmt.Sprintf("%x", sha256.Sum256(minified))
	canonicalSourceName := "kitjs." + sourceHash + ".js"
	canonicalMinifiedName := "kitjs." + minifiedHash + ".min.js"
	for _, name := range []string{
		"kitjs.js",
		"kitjs.min.js",
		canonicalSourceName,
		canonicalMinifiedName,
		"kitjs.snippet.html",
	} {
		if _, err := os.Stat(filepath.Join(config.outdir, name)); err != nil {
			t.Fatalf("output %s: %v", name, err)
		}
	}
	canonicalSource, err := os.ReadFile(filepath.Join(config.outdir, canonicalSourceName))
	if err != nil {
		t.Fatal(err)
	}
	canonicalMinified, err := os.ReadFile(filepath.Join(config.outdir, canonicalMinifiedName))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(canonicalSource, source) || !bytes.Equal(canonicalMinified, minified) {
		t.Fatal("canonical artifacts drifted from their convenience aliases")
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(canonicalSource)); got != sourceHash {
		t.Fatalf("readable filename hash=%s, artifact hash=%s", sourceHash, got)
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(canonicalMinified)); got != minifiedHash {
		t.Fatalf("minified filename hash=%s, artifact hash=%s", minifiedHash, got)
	}
	snippet, err := os.ReadFile(filepath.Join(config.outdir, "kitjs.snippet.html"))
	if err != nil {
		t.Fatal(err)
	}
	wantSnippet := "<script data-kitwork-runtime data-kitwork-plan=\"" + bundle.ContentHash +
		"\" src=\"./" + canonicalMinifiedName + "\" defer></script>\n"
	if string(snippet) != wantSnippet {
		t.Fatalf("snippet=%q, want %q", snippet, wantSnippet)
	}
}

func TestDistributionCoreOnlyAndArgumentValidation(t *testing.T) {
	config, err := parseDistArgs([]string{"1.0.0", t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	bundle, source, minified, err := composeDistribution(config)
	if err != nil {
		t.Fatal(err)
	}
	if bundle.Empty() || len(bundle.Modules) == 0 {
		t.Fatal("standalone script opt-in must emit the core without components")
	}
	for _, module := range bundle.Modules {
		if module.Kind == kitjavascript.ComponentModule {
			t.Fatalf("core-only distribution unexpectedly selected %s", module)
		}
	}
	outputs := distributionOutputs(bundle, source, minified)
	var snippet []byte
	for _, output := range outputs {
		if output.name == "kitjs.snippet.html" {
			snippet = output.body
			break
		}
	}
	if len(snippet) == 0 {
		t.Fatal("core-only distribution omitted its script snippet")
	}
	if bytes.Contains(snippet, []byte("data-kitwork-")) || bytes.Contains(snippet, []byte("data-kit-app")) || bytes.Contains(snippet, []byte("data-kit-hydrate")) {
		t.Fatalf("basic standalone snippet leaked Kitwork/Drive metadata: %s", snippet)
	}
	wantBasicSnippet := "<script src=\"./kitjs." + fmt.Sprintf("%x", sha256.Sum256(minified)) + ".min.js\" defer></script>\n"
	if string(snippet) != wantBasicSnippet {
		t.Fatalf("basic snippet=%q, want %q", snippet, wantBasicSnippet)
	}

	for _, arguments := range [][]string{
		{},
		{"1.0.0"},
		{"", t.TempDir()},
		{"v1.0.0", t.TempDir()},
		{"01.0.0", t.TempDir()},
		{"1.0", t.TempDir()},
		{" 1.0.0", t.TempDir()},
		{"1.0.0 ", t.TempDir()},
		{"1.0.0\nalert(1)", t.TempDir()},
		{"1.0.0*/\nalert(1)\n/*", t.TempDir()},
		{"1.0.0", t.TempDir(), "--unknown"},
		{"1.0.0", t.TempDir(), "--component"},
		{"1.0.0", t.TempDir(), "--component=@1.0.0"},
	} {
		if _, err := parseDistArgs(arguments); err == nil {
			t.Fatalf("parseDistArgs(%q) unexpectedly succeeded", arguments)
		}
	}
	valid, err := parseDistArgs([]string{"1.2.3-rc.1+build.7", t.TempDir()})
	if err != nil || valid.version != "1.2.3-rc.1+build.7" {
		t.Fatalf("valid exact SemVer rejected: config=%+v err=%v", valid, err)
	}
	if _, _, _, err := composeDistribution(distConfig{version: "1.0.0\nalert(1)"}); err == nil {
		t.Fatal("composeDistribution accepted an unsafe banner version")
	}

	invalid, err := parseDistArgs([]string{"1.0.0", t.TempDir(), "--component=missing@1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := composeDistribution(invalid); err == nil || !strings.Contains(err.Error(), "module not found") {
		t.Fatalf("unknown graph error=%v", err)
	}
}
