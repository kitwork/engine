package js

import (
	"strings"
	"testing"
)

// remember used to live in the always-shipped core kernel; it is now a CAPABILITY that rides this
// only-used channel. These tests pin the serving contract that makes that safe:
//
//   - a page carrying data-kit-remember (even with NO action/component) gets the runtime injected,
//     and the injected asset asks for capability:remember;
//   - ModulesJS(capability:remember) emits the module source (which installs kit.remember);
//   - a page that uses no capability/verb is still a no-op (unchanged);
//   - an unknown capability key is dropped, so a crafted ?components= can't pull an arbitrary file.
func TestRememberCapabilityEmitted(t *testing.T) {
	if !HasCapability("remember") {
		t.Fatal("remember capability module should be embedded")
	}
	if HasCapability("definitely-not-a-capability") {
		t.Error("unknown capability reported present")
	}

	// ModulesJS resolves the capability key to the module, and the module is the remember one.
	mod := ModulesJS([]string{"capability:remember"})
	if mod == "" {
		t.Fatal("expected the remember module for capability:remember")
	}
	for _, want := range []string{"kit.remember", "kitwork:remember:", "kit.internal.pageScope"} {
		if !strings.Contains(mod, want) {
			t.Errorf("remember module missing %q", want)
		}
	}

	// An unknown capability key must not resolve to anything (no arbitrary file read).
	if keys := ModuleKeys([]string{"capability:bogus"}); len(keys) != 0 {
		t.Errorf("unknown capability must be dropped, got %v", keys)
	}
	if ModulesJS([]string{"capability:bogus"}) != "" {
		t.Error("unknown capability must emit nothing")
	}
}

// api and live followed remember out of the core through the reconcile/destroy seam. Same contract:
// embedded, emitted for capability:<name>, and each module installs through kit.internal.
func TestApiLiveCapabilitiesEmitted(t *testing.T) {
	for _, name := range []string{"api", "live"} {
		if !HasCapability(name) {
			t.Fatalf("%s capability module should be embedded", name)
		}
	}
	api := ModulesJS([]string{"capability:api"})
	for _, want := range []string{"kit.syncApi", "kit.internal.boundaryScope", "kit.internal.onReconcile", `data-kit-api`} {
		if !strings.Contains(api, want) {
			t.Errorf("api module missing %q", want)
		}
	}
	live := ModulesJS([]string{"capability:live"})
	for _, want := range []string{"kit.sync", "new EventSource", "kit.internal.onDestroy", "kit.internal.scopeSelector"} {
		if !strings.Contains(live, want) {
			t.Errorf("live module missing %q", want)
		}
	}
}

// The injection gate for the api/live-only case: a page using just one of these directives (no action,
// no component) still gets the runtime, requesting the right capability. This is what the kitwork.io/
// org/vn hydrate demo pages rely on now.
func TestRenderInjectsForApiAndLive(t *testing.T) {
	cases := []struct{ attr, key string }{
		{`data-kit-api="/notifications"`, "capability%3Aapi"},
		{`data-kit-live="notifications"`, "capability%3Alive"},
	}
	for _, c := range cases {
		in := `<head></head><body><div ` + c.attr + `></div></body>`
		out := Render(in)
		if out == in {
			t.Fatalf("a %s page must get the runtime injected", c.attr)
		}
		if !strings.Contains(out, c.key) {
			t.Errorf("injected asset should request %s for %s: %s", c.key, c.attr, out)
		}
	}

	// A syntax-highlighted code sample (data-kit-live= followed by markup, not a quote) must NOT trip
	// the capability scan — that would inject a module the page never actually uses.
	sample := `<head></head><body><pre>data-kit-live=<span>"x"</span></pre></body>`
	if Render(sample) != sample {
		t.Error("an escaped code sample must not trigger capability injection")
	}
}

// The injection gate: data-kit-remember alone (no action, no component) must still inject the runtime,
// pointing at capability:remember. This is the case buildinpublic.guide is — before the extraction it
// relied on the core being injected by other directives; now the capability channel owns it.
func TestRenderInjectsForCapabilityOnly(t *testing.T) {
	// A page whose only jit/js trigger is the remember directive.
	in := `<head></head><body><html data-kit-remember="theme"></html><b data-kit-text="theme">x</b></body>`
	out := Render(in)
	if out == in {
		t.Fatal("a data-kit-remember page must get the runtime injected")
	}
	if !strings.Contains(out, runtimeMarker) {
		t.Errorf("injected script should carry the runtime marker: %s", out)
	}
	// The key is URL-encoded in the query (":" → "%3A"); the handler decodes it back to
	// capability:remember and resolves the module.
	if !strings.Contains(out, "capability%3Aremember") {
		t.Errorf("injected asset should request capability:remember: %s", out)
	}

	// Both authored prefixes trigger it.
	if Render(`<body><div data-kitwork-remember="x"></div></body>`) == `<body><div data-kitwork-remember="x"></div></body>` {
		t.Error("data-kitwork-remember should also inject the runtime")
	}

	// A page with no verb and no capability stays untouched.
	plain := `<head></head><body><p>hello</p></body>`
	if Render(plain) != plain {
		t.Error("a page with no verb/capability must be left unchanged")
	}
}
