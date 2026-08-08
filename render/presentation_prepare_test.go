package render

import (
	"strings"
	"testing"

	"github.com/kitwork/engine/value"
)

func TestPreparePrecomputesStaticPresentation(t *testing.T) {
	root := t.TempDir()
	mkfile(t, root, "index.kitwork.html", `<html><head></head><body>{{ @page }}</body></html>`)
	mkfile(t, root, "page.kitwork.html", `<main class="p-4 button">Hello {{ name }}</main>`)

	config := Config{
		Base:      root,
		Directory: ".",
		JitCSS:    true,
	}
	prepared := New(config).Prepare()
	if err := prepared.PreparationError(); err != nil {
		t.Fatal(err)
	}
	if !prepared.presentationPrepared {
		t.Fatal("static template did not prepare presentation with the generation")
	}

	data := value.New(map[string]any{"name": "Kitwork"})
	preparedOutput := prepared.Bind(data).String()
	liveOutput := New(config).Bind(data).String()
	if preparedOutput != liveOutput {
		t.Fatalf("prepared presentation changed output\nprepared:\n%s\nlive:\n%s", preparedOutput, liveOutput)
	}
	for _, marker := range []string{
		`data-kitwork-jit="css"`,
		`data-kitwork-jit="material"`,
		".p-4",
		"Hello Kitwork",
	} {
		if !strings.Contains(preparedOutput, marker) {
			t.Fatalf("prepared output missing %q\n%s", marker, preparedOutput)
		}
	}
}

func TestPrepareKeepsDynamicPresentationOnRequestPath(t *testing.T) {
	root := t.TempDir()
	mkfile(t, root, "index.kitwork.html", `<html><head></head><body>{{ @page }}</body></html>`)
	mkfile(t, root, "page.kitwork.html", `<main class="{{ tone }}">dynamic</main>`)

	prepared := New(Config{
		Base:      root,
		Directory: ".",
		JitCSS:    true,
	}).Prepare()
	if err := prepared.PreparationError(); err != nil {
		t.Fatal(err)
	}
	if prepared.presentationPrepared {
		t.Fatal("dynamic class was incorrectly frozen into generation presentation")
	}

	red := prepared.Bind(value.New(map[string]any{"tone": "text-red-500"})).String()
	green := prepared.Bind(value.New(map[string]any{"tone": "text-green-500"})).String()
	if !strings.Contains(red, ".text-red-500") {
		t.Fatalf("red request did not generate its dynamic class\n%s", red)
	}
	if !strings.Contains(green, ".text-green-500") {
		t.Fatalf("green request did not generate its dynamic class\n%s", green)
	}
}

func TestDynamicPresentationDetection(t *testing.T) {
	cases := []struct {
		name     string
		template string
		dynamic  bool
	}{
		{name: "text", template: `<p>{{ message }}</p>`, dynamic: false},
		{name: "class", template: `<p class="p-4 {{ tone }}">x</p>`, dynamic: true},
		{name: "link attribute", template: `<a href="{{ url }}">x</a>`, dynamic: false},
		{name: "meta attribute", template: `<meta content="{{ description }}">`, dynamic: false},
		{name: "kit attribute", template: `<button data-kit-action="{{ action }}">x</button>`, dynamic: true},
		{name: "style block", template: `<style>body{font-family:{{ font }}}</style>`, dynamic: true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := hasDynamicPresentation(test.template); got != test.dynamic {
				t.Fatalf("hasDynamicPresentation() = %v, want %v", got, test.dynamic)
			}
		})
	}
}
