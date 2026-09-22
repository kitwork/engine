package js

import (
	"strings"
	"testing"
)

// The verb system is gone (22/09): behaviour is a component or an expression, transport is Drive.
// What ships is a kernel component, by name or by exact version, and nothing else.
//
// The catalogue only holds what the grammar cannot say and the platform does not do: toggle, tab
// and dismiss WERE the grammar (a scope flag plus show/bind), get, more and submit were Drive's
// work, and all six went unused — deleted 22/09. `copy` went with them: one name, one meaning.
func TestHasComponent(t *testing.T) {
	for _, n := range []string{"clipboard", "dialog", "shortcut", "sidebar", "theme", "toast"} {
		if !HasComponent(n) {
			t.Errorf("expected component module %q", n)
		}
	}
	for _, gone := range []string{"toggle", "tab", "dismiss", "get", "more", "submit", "copy", "dropdown"} {
		if HasComponent(gone) {
			t.Errorf("%q was deleted: the grammar or the platform covers it", gone)
		}
	}
	if HasComponent("core") {
		t.Error("core is reserved, must not be a component")
	}
	if HasComponent("definitely-not-a-component") {
		t.Error("unknown component reported present")
	}
}

func TestRuntimeJSOnlyUsedPlusCore(t *testing.T) {
	js := RuntimeJS([]string{"component:clipboard", "component:clipboard", "nope"})
	if js == "" {
		t.Fatal("expected runtime for a known component")
	}
	if !strings.Contains(js, "window.kit = kit") || !strings.Contains(js, "window.kitwork = kit") {
		t.Errorf("core dispatcher missing: %s", js)
	}
	if !strings.Contains(js, `component("clipboard"`) {
		t.Errorf("clipboard module missing: %s", js)
	}
	if strings.Contains(js, `component("dialog"`) {
		t.Errorf("dialog should NOT be included (unused): %s", js)
	}
	if RuntimeJS([]string{"nope"}) != "" {
		t.Error("only-unknown names should yield no runtime")
	}
	if strings.Contains(js, `components.action(`) || strings.Contains(js, "kit.fire") {
		t.Errorf("the verb registry must not ship any more: %s", js)
	}
}

func TestRenderInjectsOnlyUsed(t *testing.T) {
	html := `<html><head><title>x</title></head><body>` +
		`<div data-kit-component="toast"><button data-kit-click="show('saved')">One</button></div>` +
		`<dialog data-kit-component="dialog" data-kit-alias="$m"></dialog></body></html>`
	out := Render(html)

	// count the OPEN TAG precisely — the kernel source itself mentions the marker (mergeHead).
	if strings.Count(out, `<script data-kitwork-jit="runtime"`) != 1 {
		t.Errorf("expected exactly one runtime script: %s", out)
	}
	si := strings.Index(out, `<script data-kitwork-jit="runtime"`)
	if hi := strings.Index(out, "</head>"); si < 0 || si > hi {
		t.Errorf("runtime should be injected before </head>: %s", out)
	}
	if !strings.Contains(out, `components=component%3Adialog%2Ccomponent%3Atoast`) {
		t.Errorf("both used components expected: %s", out)
	}
	if strings.Contains(out, `clipboard`) {
		t.Errorf("clipboard is unused and must not ship: %s", out)
	}
	if !strings.Contains(out, `<div data-kit-component="toast">`) {
		t.Errorf("author markup should be preserved: %s", out)
	}
}

func TestRenderNoOpWithoutComponents(t *testing.T) {
	in := `<head></head><body><p>nothing here</p></body>`
	if out := Render(in); out != in {
		t.Errorf("expected unchanged output, got: %s", out)
	}
	unknown := `<head></head><body><div data-kit-component="zzz"></div></body>`
	if out := Render(unknown); out != unknown {
		t.Errorf("unknown component should inject nothing, got: %s", out)
	}
	// The retired verb attribute is just an attribute now: it brings nothing.
	verb := `<head></head><body><button data-kit-action="copy" data-kitwork-action="copy"></button></body>`
	if out := Render(verb); out != verb {
		t.Errorf("data-kit-action is not a directive any more and must not inject, got: %s", out)
	}
}

// A bare name resolves to the latest version of that component, and only the catalogue's own
// names resolve: `copy` was clipboard's second name until 22/09 — one name, one meaning.
func TestRenderInjectsComponents(t *testing.T) {
	htmlComp := `<html><head></head><body><div data-kit-component="clipboard"></div></body></html>`
	outComp := Render(htmlComp)
	if !strings.Contains(outComp, `components=component%3Aclipboard`) {
		t.Errorf("expected the clipboard module to be injected, got: %s", outComp)
	}
	if !strings.Contains(ModulesJS([]string{"component:clipboard"}), `component("clipboard@v2.0.0"`) {
		t.Error("a bare name must resolve to the latest version (v2.0.0)")
	}
	if out := Render(`<html><head></head><body><div data-kit-component="copy"></div></body></html>`); strings.Contains(out, "components=") {
		t.Errorf("copy is not a name any more, nothing may be injected for it: %s", out)
	}
	if ModulesJS([]string{"component:copy"}) != "" {
		t.Error("copy must resolve to no module")
	}
	if ModulesJS([]string{"action:copy"}) != "" {
		t.Error("an action: key is not a module kind any more")
	}
}

func TestRenderInjectsVersionedComponents(t *testing.T) {
	html := `<html><head></head><body><div data-kit-component="clipboard@v1.0.0"></div></body></html>`
	out := Render(html)
	if !strings.Contains(out, `components=component%3Aclipboard%40v1.0.0`) {
		t.Errorf("expected versioned clipboard component (v1.0.0) to be injected, got: %s", out)
	}
}

func TestClipboardLatestHasPermissionFallback(t *testing.T) {
	out := ModulesJS([]string{"component:clipboard@v2.0.0"})
	for _, want := range []string{
		`navigator.clipboard.writeText(text).then(copied).catch(fallback)`,
		`document.execCommand("copy")`,
		`active.focus({ preventScroll: true })`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("latest clipboard component is missing %q: %s", want, out)
		}
	}
}
