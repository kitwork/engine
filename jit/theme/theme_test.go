package theme

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestRenderMarker(t *testing.T) {
	out := Render(`<head><script data-kitwork-jit="theme"></script><title>x</title></head>`)
	if strings.Count(out, `data-kitwork-jit="theme"`) != 1 {
		t.Error("pre-paint must retain exactly one canonical engine marker")
	}
	if !strings.Contains(out, `add("dark")`) || !strings.Contains(out, `remove("dark")`) {
		t.Error("pre-paint script not injected (add/remove dark)")
	}
	if !strings.Contains(out, `localStorage.getItem("theme")`) {
		t.Error("pre-paint should read the shared \"theme\" key")
	}

	// data-kit-jit alias + inner whitespace.
	if v := Render(`<script data-kit-jit="theme">   </script>`); strings.Count(v, `data-kitwork-jit="theme"`) != 1 {
		t.Error("data-kit-jit alias / whitespace was not canonicalized")
	}
	for _, marker := range []string{
		`<SCRIPT DATA-KITWORK-JIT='theme'> </SCRIPT>`,
		`<script data-kit-jit = theme></script>`,
		`<script data-kitwork-jit="theme">globalThis.forgedThemePrepaint = true</script>`,
	} {
		if rendered := Render(marker); !strings.Contains(rendered, `getItem("theme")`) ||
			strings.Contains(rendered, `forgedThemePrepaint`) ||
			strings.Count(rendered, `data-kitwork-jit="theme"`) != 1 {
			t.Errorf("valid marker spelling was not replaced: %s", marker)
		}
	}
}

func TestRenderAutoScan(t *testing.T) {
	// A page that uses the kernel API (no marker) gets the pre-paint injected at the top of <head>.
	in := `<html><head><link rel="stylesheet" href="a.css"></head>` +
		`<body><button data-kit-click="kit.theme.toggle()">t</button></body></html>`
	out := Render(in)
	if !strings.Contains(out, `getItem("theme")`) {
		t.Fatal("pre-paint not auto-injected for kit.theme.toggle() page")
	}
	// It must land BEFORE the stylesheet (earliest point wins the anti-flash race).
	if strings.Index(out, `getItem("theme")`) > strings.Index(out, "a.css") {
		t.Error("pre-paint must be injected before the first stylesheet")
	}

	// The other recognised forms also trigger it.
	for _, use := range []string{
		`<head></head><body><button data-kit-click="kit.appearance.toggle()"></button></body>`,
		`<head></head><body><button data-kit-click="$app.appearance.system()"></button></body>`,
		`<head></head><body><i data-kit-text="kit.theme.mode"></i></body>`,
		`<head></head><body><button data-kitwork-action="theme"></button></body>`,
		`<head></head><body><div data-kit-component="theme"></div></body>`,
		`<HEAD></HEAD><body><div DATA-KIT-COMPONENT='theme'></div></body>`,
		`<head></head><body><main data-kit-component='app' data-kit-as='$app'></main></body>`,
		`<head></head><body><main data-kit-component=" app " data-kit-as="$app"></main></body>`,
		`<head></head><body><main data-kit-component="&#97;pp" data-kit-as="$app"></main></body>`,
		`<head></head><body><div data-kit-component=' theme '></div></body>`,
		`<head></head><body><main data-kit-component="app@1.1.0" data-kit-as="$app"></main></body>`,
		`<head></head><body><div data-kit-component='theme@3.0.0'></div></body>`,
		`<head></head><body><main data-kit-component="&#xfeff;app@1.1.0" data-kit-as="$app"></main></body>`,
		`<head></head><body><div data-kit-component='&#x1680;theme&#x3000;'></div></body>`,
		`<head></head><body><button data-kit-click="$app . appearance . toggle()"></button></body>`,
		`<head></head><body><button data-kit-click="kit . appearance . system()"></button></body>`,
	} {
		if !strings.Contains(Render(use), `getItem("theme")`) {
			t.Errorf("theme system not detected in: %s", use)
		}
	}
}

func TestRenderDoesNotConfuseAppearanceLookalikes(t *testing.T) {
	for _, html := range []string{
		`<head></head><body><div data-kit-component="application"></div></body>`,
		`<head></head><body><div data-kit-component="&#x85;theme&#x85;"></div></body>`,
		`<head></head><body><code>$application.appearance.toggle()</code></body>`,
		`<head></head><body><div data-kit-action="themed"></div></body>`,
	} {
		if Render(html) != html {
			t.Errorf("appearance lookalike triggered pre-paint: %s", html)
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
	if strings.Count(pinned, `data-kitwork-jit="theme"`) != 1 || !strings.Contains(pinned, `getItem("theme")`) {
		t.Error("marker should be filled in place and canonicalized under Force")
	}
}

func TestPrepaintResolvesSystemBeforeFirstPaint(t *testing.T) {
	for _, fragment := range []string{
		`m="system"`,
		`t=t&&t.toLowerCase()`,
		`t==="light"||t==="dark"||t==="system"`,
		`matchMedia("(prefers-color-scheme: dark)").matches`,
		`if(m==="dark")c.add("dark");else c.remove("dark")`,
		`r.style.colorScheme=m`,
	} {
		if !strings.Contains(prepaint, fragment) {
			t.Fatalf("prepaint is missing system-resolution contract %q", fragment)
		}
	}
	storageCatch := strings.Index(prepaint, `catch(e){}`)
	mediaResolution := strings.Index(prepaint, `if(m==="system")`)
	if storageCatch < 0 || mediaResolution < 0 || storageCatch > mediaResolution {
		t.Fatal("storage failure must continue into system media resolution")
	}
}

func TestPrepaintBodyMatchesDriveCanonicalSource(t *testing.T) {
	source, err := os.ReadFile("../javascript/src/drive.js")
	if err != nil {
		t.Fatal(err)
	}
	contract := []byte("var THEME_PREPAINT_SOURCE = '" + prepaintBody + "';")
	if !bytes.Contains(source, contract) {
		t.Fatal("Drive canonical theme prepaint source drifted from the Go renderer")
	}
}
