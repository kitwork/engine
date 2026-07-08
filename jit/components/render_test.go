package components

import (
	"strings"
	"testing"
)

func TestRenderEmitsOnlyUsedFamilies(t *testing.T) {
	html := `<html><head><title>x</title></head><body>` +
		`<button class="button button-brand">Go</button></body></html>`
	out := Render(html)

	if strings.Count(out, `data-kitwork-jit="components"`) != 1 {
		t.Fatalf("expected exactly one component style block: %s", out)
	}
	si := strings.Index(out, `<style data-kitwork-jit="components">`)
	if hi := strings.Index(out, "</head>"); si < 0 || si > hi {
		t.Errorf("component style should be injected before </head>: %s", out)
	}
	// Button family emitted (full word + alias share a rule); card family NOT (unused).
	if !strings.Contains(out, ".button,.btn{") {
		t.Errorf("button family missing or not aliased: %s", out)
	}
	if !strings.Contains(out, ".button-brand,.btn-brand{") {
		t.Errorf("button-brand variant missing: %s", out)
	}
	if strings.Contains(out, ".card{") {
		t.Errorf("card family should NOT ship (unused): %s", out)
	}
	// Markup untouched.
	if !strings.Contains(out, `<button class="button button-brand">`) {
		t.Errorf("author markup should be preserved: %s", out)
	}
}

func TestRenderAliasTriggersFamily(t *testing.T) {
	// The short alias `.btn` alone must also trigger the family.
	out := Render(`<head></head><body><a class="btn btn-outline">x</a></body>`)
	if !strings.Contains(out, ".button,.btn{") {
		t.Errorf("alias .btn should trigger the button family: %s", out)
	}
}

func TestRenderCardFamily(t *testing.T) {
	out := Render(`<head></head><body><div class="card"><div class="card-body">x</div></div></body>`)
	if !strings.Contains(out, ".card{") || !strings.Contains(out, ".card-body{") {
		t.Errorf("card family expected: %s", out)
	}
	if strings.Contains(out, ".button,.btn{") {
		t.Errorf("button family should NOT ship (unused): %s", out)
	}
}

func TestRenderProseFamilyIncludesDarkRules(t *testing.T) {
	out := Render(`<head></head><body><article class="prose prose-frame"><p>x</p></article></body>`)
	for _, want := range []string{".prose{", ".dark .prose{", ".prose-frame img{"} {
		if !strings.Contains(out, want) {
			t.Errorf("prose family missing %q: %s", want, out)
		}
	}
	if strings.Contains(out, ".card{") {
		t.Errorf("card family should NOT ship (unused): %s", out)
	}
}

func TestRenderNoOpWithoutComponents(t *testing.T) {
	in := `<head></head><body><p class="text-sm">no components</p></body>`
	if out := Render(in); out != in {
		t.Errorf("expected unchanged output, got: %s", out)
	}
}

func TestRenderNewFamilies(t *testing.T) {
	// Each new family emits only when used, and only itself.
	cases := []struct{ markup, want, notWant string }{
		{`<span class="badge badge-success">New</span>`, ".badge{", ".alert{"},
		{`<div class="alert alert-warning">!</div>`, ".alert{", ".badge{"},
		{`<input class="input input-large">`, ".input,.textarea,.select{", ".table{"},
		{`<table class="table table-zebra">`, ".table{", ".badge{"},
		{`<textarea class="textarea"></textarea>`, ".input,.textarea,.select{", ".badge{"}, // alias base triggers input
	}
	for _, c := range cases {
		out := Render(`<head></head><body>` + c.markup + `</body>`)
		if !strings.Contains(out, c.want) {
			t.Errorf("%q: expected %q in %s", c.markup, c.want, out)
		}
		if strings.Contains(out, c.notWant) {
			t.Errorf("%q: %q should NOT ship (unused): %s", c.markup, c.notWant, out)
		}
	}
}
