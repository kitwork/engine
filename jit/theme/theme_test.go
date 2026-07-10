package theme

import (
	"strings"
	"testing"
)

func TestRenderMarker(t *testing.T) {
	out := Render(`<head><script data-kitwork-jit="theme"></script><title>x</title></head>`)
	if strings.Contains(out, `-jit="theme"`) {
		t.Error("marker was not replaced")
	}
	if !strings.Contains(out, `add("dark")`) || !strings.Contains(out, `remove("dark")`) {
		t.Error("pre-paint script not injected (add/remove dark)")
	}
	if !strings.Contains(out, `localStorage.getItem("theme")`) {
		t.Error("pre-paint should read the shared \"theme\" key")
	}

	// data-kit-jit alias + inner whitespace.
	if v := Render(`<script data-kit-jit="theme">   </script>`); strings.Contains(v, `-jit="theme"`) {
		t.Error("data-kit-jit alias / whitespace not handled")
	}
}

func TestRenderAutoScan(t *testing.T) {
	// A page that uses the kernel API (no marker) gets the pre-paint injected at the top of <head>.
	in := `<html><head><link rel="stylesheet" href="a.css"></head>` +
		`<body><button data-kit-click="$app.toggleTheme()">t</button></body></html>`
	out := Render(in)
	if !strings.Contains(out, `getItem("theme")`) {
		t.Fatal("pre-paint not auto-injected for $app.toggleTheme() page")
	}
	// It must land BEFORE the stylesheet (earliest point wins the anti-flash race).
	if strings.Index(out, `getItem("theme")`) > strings.Index(out, "a.css") {
		t.Error("pre-paint must be injected before the first stylesheet")
	}

	// The other recognised forms also trigger it.
	for _, use := range []string{
		`<head></head><body><i data-kit-text="$app.theme"></i></body>`,
		`<head></head><body><button data-kitwork-action="theme"></button></body>`,
		`<head></head><body><div data-kit-component="theme"></div></body>`,
	} {
		if !strings.Contains(Render(use), `getItem("theme")`) {
			t.Errorf("theme system not detected in: %s", use)
		}
	}
}

func TestRenderNoop(t *testing.T) {
	// No marker and no theme usage → unchanged.
	plain := `<head><title>x</title></head><body><p>hi</p></body>`
	if Render(plain) != plain {
		t.Error("should be a no-op without marker or theme usage")
	}
}

func TestForce(t *testing.T) {
	// No marker, no theme usage — Force still injects at the top of <head>.
	in := `<html><head><link rel="stylesheet" href="a.css"></head><body><p>plain</p></body></html>`
	out := Force(in)
	if !strings.Contains(out, `getItem("theme")`) {
		t.Fatal("Force must inject without any usage")
	}
	if strings.Index(out, `getItem("theme")`) > strings.Index(out, "a.css") {
		t.Error("forced pre-paint must land before the first stylesheet")
	}
	// A marker still pins the position.
	pinned := Force(`<head><title>x</title><script data-kitwork-jit="theme"></script></head>`)
	if strings.Contains(pinned, `-jit="theme"`) || !strings.Contains(pinned, `getItem("theme")`) {
		t.Error("marker should be replaced in place under Force")
	}
}
