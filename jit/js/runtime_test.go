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
// `dropdown` went too, and stays gone: opening a panel is a scope flag and a show. What replaced
// it is `menu`, which does the part a flag cannot — where focus lands, and how the arrows walk —
// standing to it as `tabs` stands to the deleted `tab`.
//
// `theme` was deleted on 23/09, when markup called kit.theme directly, and returned on 03/10 as the
// page's view of kit.theme, once markup stopped calling kit. `progressbar` is the view of kit.progress.
//
// `dropdown` returned on 11/10 for Kitwork UI, where it is a built-in component the engine writes
// from <box component="dropdown" as="$account"> (docs/components/dropdown.md): the page reaches it through its alias,
// and the kernel registers an alias only on a component host. A hand-written page still opens a panel
// with a scope flag and a show. `appearance` is the theme contract under the name Kitwork UI gives it.
func TestHasComponent(t *testing.T) {
	for _, n := range []string{"clipboard", "dialog", "shortcut", "sidebar", "toast", "menu", "scrolled", "theme", "progressbar", "appearance", "dropdown"} {
		if !HasComponent(n) {
			t.Errorf("expected component module %q", n)
		}
	}
	for _, gone := range []string{"toggle", "tab", "dismiss", "get", "more", "submit", "copy"} {
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

func TestModulesOnlyUsedPlusCore(t *testing.T) {
	js := ModulesJS([]string{"component:clipboard", "component:clipboard", "nope"})
	if strings.Count(js, `component("clipboard"`) != 1 {
		t.Errorf("clipboard should ship once, however often it is named: %s", js)
	}
	if strings.Contains(js, `component("dialog"`) {
		t.Errorf("dialog should NOT be included (unused): %s", js)
	}
	if ModulesJS([]string{"nope"}) != "" {
		t.Error("only-unknown names should yield no module")
	}
	kernel := string(KernelAsset().Body)
	if !strings.Contains(kernel, "window.kit = window.kit") || !strings.Contains(kernel, "window.kitwork = kit") {
		t.Errorf("core dispatcher missing from the kernel's file")
	}
	if strings.Contains(kernel+js, `components.action(`) || strings.Contains(kernel+js, "kit.fire") {
		t.Error("the verb registry must not ship any more")
	}
}

func TestDeliverBringsOnlyUsed(t *testing.T) {
	html := `<html><head><title>x</title></head><body>` +
		`<div data-kit-component="toast"><button data-kit-click="show('saved')">One</button></div>` +
		`<dialog data-kit-component="dialog" data-kit-alias="$m"></dialog></body></html>`
	out := deliver(html)

	// count the OPEN TAG precisely — the kernel source itself mentions the marker (mergeHead).
	if strings.Count(out, `<script data-kitwork-generated="runtime"`) != 1 {
		t.Errorf("expected exactly one runtime script: %s", out)
	}
	si := strings.Index(out, `<script data-kitwork-generated="runtime"`)
	if hi := strings.Index(out, "</head>"); si < 0 || si > hi {
		t.Errorf("the runtime should be delivered before </head>: %s", out)
	}
	if !strings.Contains(out, `/_kitwork/components/dialog.`) || !strings.Contains(out, `/_kitwork/components/toast.`) {
		t.Errorf("both used components expected: %s", out)
	}
	if strings.Contains(out, `clipboard`) {
		t.Errorf("clipboard is unused and must not ship: %s", out)
	}
	if !strings.Contains(out, `<div data-kit-component="toast">`) {
		t.Errorf("author markup should be preserved: %s", out)
	}
}

func TestDeliverNothingWithoutGrammar(t *testing.T) {
	in := `<head></head><body><p>nothing here</p></body>`
	if out := deliver(in); out != in {
		t.Errorf("expected unchanged output, got: %s", out)
	}
	// A component nobody has is still a host the kernel reports by name, so the page gets the kernel
	// — and no component file.
	unknown := `<head></head><body><div data-kit-component="zzz"></div></body>`
	if out := deliver(unknown); strings.Contains(out, "/_kitwork/components/") || !strings.Contains(out, "/_kitwork/kernel.") {
		t.Errorf("an unknown component brings the kernel and no component file, got: %s", out)
	}
	// The retired verb attribute is just an attribute now: it brings nothing.
	verb := `<head></head><body><button data-kit-action="copy" data-kitwork-action="copy"></button></body>`
	if out := deliver(verb); out != verb {
		t.Errorf("data-kit-action is not a directive any more and must not inject, got: %s", out)
	}
}

// A bare name resolves to the latest version of that component, and only the catalogue's own
// names resolve: `copy` was clipboard's second name until 22/09 — one name, one meaning.
func TestDeliverBringsComponents(t *testing.T) {
	htmlComp := `<html><head></head><body><div data-kit-component="clipboard"></div></body></html>`
	outComp := deliver(htmlComp)
	if !strings.Contains(outComp, `/_kitwork/components/clipboard.`) {
		t.Errorf("expected the clipboard module to be injected, got: %s", outComp)
	}
	if !strings.Contains(ModulesJS([]string{"component:clipboard"}), "copyResetTimer") { // v2.0.0's, not v1.0.0's
		t.Error("a bare name must resolve to the latest version (v2.0.0)")
	}
	if out := deliver(`<html><head></head><body><div data-kit-component="copy"></div></body></html>`); strings.Contains(out, "/_kitwork/components/") {
		t.Errorf("copy is not a name any more, nothing may be injected for it: %s", out)
	}
	if ModulesJS([]string{"component:copy"}) != "" {
		t.Error("copy must resolve to no module")
	}
	if ModulesJS([]string{"action:copy"}) != "" {
		t.Error("an action: key is not a module kind any more")
	}
}

func TestDeliverBringsVersionedComponents(t *testing.T) {
	html := `<html><head></head><body><div data-kit-component="clipboard@v1.0.0"></div></body></html>`
	out := deliver(html)
	if v1 := componentAsset("clipboard@v1.0.0", nil); !strings.Contains(out, `src="`+v1.Path+`"`) || strings.Contains(out, `/components/clipboard@`) {
		t.Errorf("expected clipboard v1.0.0's file, under the component's name alone, got: %s", out)
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
