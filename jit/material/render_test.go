package material

import (
	"strings"
	"testing"
)

func TestRenderEmitsOnlyUsedFamilies(t *testing.T) {
	html := `<html><head><title>x</title></head><body>` +
		`<button class="button button-brand">Go</button></body></html>`
	out := Render(html)

	if strings.Count(out, `data-kitwork-jit="material"`) != 1 {
		t.Fatalf("expected exactly one material style block: %s", out)
	}
	si := strings.Index(out, `<style data-kitwork-jit="material">`)
	if hi := strings.Index(out, "</head>"); si < 0 || si > hi {
		t.Errorf("material style should be injected before </head>: %s", out)
	}
	if !strings.Contains(out, ".button,.btn{") {
		t.Errorf("button family missing or not aliased: %s", out)
	}
	if strings.Contains(out, ".card{") {
		t.Errorf("card family should NOT ship (unused): %s", out)
	}
}

func TestRenderAliasTriggersFamily(t *testing.T) {
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

func TestRenderNoOpWithoutComponents(t *testing.T) {
	in := `<head></head><body><p class="text-sm">no components</p></body>`
	if out := Render(in); out != in {
		t.Errorf("expected unchanged output, got: %s", out)
	}
}

func TestRenderNewFamilies(t *testing.T) {
	cases := []struct{ markup, want, notWant string }{
		{`<button class="button button-brand">Save</button>`, ".button,.btn{", ".card{"},
		{`<span class="badge badge-success">New</span>`, ".badge{", ".alert{"},
		{`<div class="alert alert-warning">!</div>`, ".alert{", ".badge{"},
		{`<input class="input input-large">`, ".input,.textarea,.select{", ".table{"},
		{`<table class="table table-zebra">`, ".table{", ".badge{"},
		{`<textarea class="textarea"></textarea>`, ".input,.textarea,.select{", ".badge{"},
		{`<div class="stat"><div class="stat-value">$1,200</div></div>`, ".stat{", ".badge{"},
		{`<nav class="navbar"><a class="navbar-brand">Logo</a></nav>`, ".navbar{", ".badge{"},
		{`<ul class="timeline"><li class="timeline-item">Event</li></ul>`, ".timeline{", ".badge{"},
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

func TestRenderProseFamily(t *testing.T) {
	out := Render(`<head></head><body><article class="prose"><h1>Title</h1><p>Body</p></article></body>`)
	if !strings.Contains(out, ".prose{") {
		t.Errorf("prose family expected: %s", out)
	}
}
