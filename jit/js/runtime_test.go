package js

import (
	"strings"
	"testing"
)

func TestHasVerb(t *testing.T) {
	for _, n := range []string{"copy", "toggle", "dismiss", "tab", "theme", "dialog", "get", "more", "submit"} {
		if !HasVerb(n) {
			t.Errorf("expected verb module %q", n)
		}
	}
	if HasVerb("core") {
		t.Error("core is reserved, must not be a verb")
	}
	if HasVerb("definitely-not-a-verb") {
		t.Error("unknown verb reported present")
	}
}

func TestRuntimeJSOnlyUsedPlusCore(t *testing.T) {
	js := RuntimeJS([]string{"copy", "copy", "nope"})
	if js == "" {
		t.Fatal("expected runtime for a known verb")
	}
	if !strings.Contains(js, "window.kitwork = window.kitwork") {
		t.Errorf("core dispatcher missing: %s", js)
	}
	if !strings.Contains(js, `action("copy"`) {
		t.Errorf("copy module missing: %s", js)
	}
	if strings.Contains(js, `action("toggle"`) {
		t.Errorf("toggle should NOT be included (unused): %s", js)
	}
	if RuntimeJS([]string{"nope"}) != "" {
		t.Error("only-unknown verbs should yield no runtime")
	}
}

func TestRenderInjectsOnlyUsed(t *testing.T) {
	html := `<html><head><title>x</title></head><body>` +
		`<button data-kitwork-action="tab" data-kitwork-target="#one">One</button>` +
		`<button data-kitwork-action="dialog" data-kitwork-target="#m">Open</button></body></html>`
	out := Render(html)

	// count the OPEN TAG precisely — the kernel source itself mentions the marker (mergeHead).
	if strings.Count(out, `<script data-kitwork-jit="js">`) != 1 {
		t.Errorf("expected exactly one runtime script: %s", out)
	}
	si := strings.Index(out, `<script data-kitwork-jit="js">`)
	if hi := strings.Index(out, "</head>"); si < 0 || si > hi {
		t.Errorf("runtime should be injected before </head>: %s", out)
	}
	if !strings.Contains(out, `action("tab"`) || !strings.Contains(out, `action("dialog"`) {
		t.Errorf("both used verbs expected: %s", out)
	}
	if strings.Contains(out, `action("copy"`) {
		t.Errorf("copy is unused and must not ship: %s", out)
	}
	if !strings.Contains(out, `<button data-kitwork-action="tab"`) {
		t.Errorf("author markup should be preserved: %s", out)
	}
}

func TestRenderNoOpWithoutVerbs(t *testing.T) {
	in := `<head></head><body><p>no actions here</p></body>`
	if out := Render(in); out != in {
		t.Errorf("expected unchanged output, got: %s", out)
	}
	unknown := `<head></head><body><button data-kitwork-action="zzz"></button></body>`
	if out := Render(unknown); out != unknown {
		t.Errorf("unknown verb should inject nothing, got: %s", out)
	}
}

func TestRenderInjectsComponents(t *testing.T) {
	// 1. Only component should be injected if only component is declared
	htmlComp := `<html><head></head><body><div data-kit-component="copy"></div></body></html>`
	outComp := Render(htmlComp)
	if !strings.Contains(outComp, `component("copy"`) {
		t.Errorf("expected component copy module to be injected, got: %s", outComp)
	}
	if strings.Contains(outComp, `action("copy"`) {
		t.Errorf("expected copy action verb to NOT be injected, got: %s", outComp)
	}
	if !strings.Contains(outComp, `component("copy@v2.0.0"`) {
		t.Errorf("expected latest copy v2.0.0 to be resolved, got: %s", outComp)
	}

	// 2. Only action should be injected if only action is declared
	htmlAct := `<html><head></head><body><button data-kitwork-action="copy"></button></body></html>`
	outAct := Render(htmlAct)
	if !strings.Contains(outAct, `action("copy"`) {
		t.Errorf("expected copy action verb to be injected, got: %s", outAct)
	}
	if strings.Contains(outAct, `component("copy"`) {
		t.Errorf("expected copy component to NOT be injected, got: %s", outAct)
	}
}

func TestRenderInjectsVersionedComponents(t *testing.T) {
	html := `<html><head></head><body><div data-kit-component="copy@v1.0.0"></div></body></html>`
	out := Render(html)
	if !strings.Contains(out, `component("copy@v1.0.0"`) {
		t.Errorf("expected versioned copy component (v1.0.0) to be injected, got: %s", out)
	}
	if strings.Contains(out, `component("copy@v2.0.0"`) {
		t.Errorf("expected copy@v1.0.0 specifically, v2.0.0 should not be injected, got: %s", out)
	}
}
