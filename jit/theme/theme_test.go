package theme

import (
	"strings"
	"testing"
)

// injected reports whether a rendered page carries the engine's pre-paint exactly as it ships:
// today's marker around today's body. What the body does is pinned by
// TestPrepaintResolvesSystemBeforeFirstPaint and run in a browser by TestBrowserPrepaint….
func injected(out string) bool {
	return strings.Contains(out, `<script data-kitwork-generated="appearance">`+prepaintBody+`</script>`)
}

func TestRenderMarker(t *testing.T) {
	out := Render(`<head><script data-kitwork-generated="appearance"></script><title>x</title></head>`)
	if strings.Count(out, `data-kitwork-generated="appearance"`) != 1 {
		t.Error("pre-paint must retain exactly one canonical engine marker")
	}
	if !strings.Contains(out, `add("dark")`) || !strings.Contains(out, `remove("dark")`) {
		t.Error("pre-paint script not injected (add/remove dark)")
	}
	if !strings.Contains(out, `getItem("appearance")`) || !strings.Contains(out, `getItem("theme")`) {
		t.Error("pre-paint should read the \"appearance\" key and fall back to the older \"theme\" key")
	}

	// data-kit-jit alias + inner whitespace.
	if v := Render(`<script data-kit-jit="theme">   </script>`); strings.Count(v, `data-kitwork-generated="appearance"`) != 1 {
		t.Error("data-kit-jit alias / whitespace was not canonicalized")
	}
	for _, marker := range []string{
		`<SCRIPT DATA-KITWORK-JIT='theme'> </SCRIPT>`,
		`<script data-kit-jit = theme></script>`,
		`<script data-kitwork-generated="theme">globalThis.forgedThemePrepaint = true</script>`,
		`<script data-kitwork-generated="appearance">globalThis.forgedThemePrepaint = true</script>`,
		`<SCRIPT DATA-KITWORK-GENERATED='appearance'> </SCRIPT>`,
		`<script data-kitwork-generated = appearance></script>`,
	} {
		if rendered := Render(marker); !injected(rendered) ||
			strings.Contains(rendered, `forgedThemePrepaint`) ||
			strings.Count(rendered, `data-kitwork-generated="appearance"`) != 1 ||
			strings.Contains(rendered, `"theme">`) {
			t.Errorf("valid marker spelling was not replaced by today's: %s\n  → %s", marker, rendered)
		}
	}
	// control: a script named for something else is not the pre-paint's slot
	for _, html := range []string{
		`<head><script data-kitwork-generated="appearances"></script></head>`,
		`<head><script data-kitwork-generated="js">x</script></head>`,
	} {
		if Render(html) != html {
			t.Errorf("not the pre-paint's marker, yet filled: %s", html)
		}
	}
}

func TestRenderAutoScan(t *testing.T) {
	// A page that uses the kernel API (no marker) gets the pre-paint injected at the top of <head>.
	in := `<html><head><link rel="stylesheet" href="a.css"></head>` +
		`<body><button data-kit-click="kit.theme.toggle()">t</button></body></html>`
	out := Render(in)
	if !injected(out) {
		t.Fatal("pre-paint not auto-injected for kit.theme.toggle() page")
	}
	// It must land BEFORE the stylesheet (earliest point wins the anti-flash race).
	if strings.Index(out, `data-kitwork-generated="appearance"`) > strings.Index(out, "a.css") {
		t.Error("pre-paint must be injected before the first stylesheet")
	}

	// The other recognised forms also trigger it.
	for _, use := range []string{
		`<head></head><body><button data-kit-click="kit.appearance.toggle()"></button></body>`,
		`<head></head><body><button data-kit-click="$app.appearance.system()"></button></body>`,
		`<head></head><body><i data-kit-text="kit.theme.mode"></i></body>`,
		`<head></head><body><div data-kit-component="theme"></div></body>`,
		`<HEAD></HEAD><body><div DATA-KIT-COMPONENT='theme'></div></body>`,
		`<head></head><body><main data-kit-component='app' data-kit-alias='$app'></main></body>`,
		`<head></head><body><main data-kit-component=" app " data-kit-alias="$app"></main></body>`,
		`<head></head><body><main data-kit-component="&#97;pp" data-kit-alias="$app"></main></body>`,
		`<head></head><body><div data-kit-component=' theme '></div></body>`,
		`<head></head><body><main data-kit-component="app@1.1.0" data-kit-alias="$app"></main></body>`,
		`<head></head><body><div data-kit-component='theme@3.0.0'></div></body>`,
		`<head></head><body><div data-kit-component='th&#101;me@3.0.0'></div></body>`,
		`<head></head><body><main data-kit-component="&#xfeff;app@1.1.0" data-kit-alias="$app"></main></body>`,
		`<head></head><body><div data-kit-component='&#x1680;theme&#x3000;'></div></body>`,
		`<head></head><body><button data-kit-click="$app . appearance . toggle()"></button></body>`,
		`<head></head><body><button data-kit-click="kit . appearance . system()"></button></body>`,
	} {
		if !injected(Render(use)) {
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
		`<head></head><body><button data-kitwork-action="theme"></button></body>`,
		`<head></head><body><button data-kit-action="theme"></button></body>`,
		`<head></head><body><p>kit&#46;theme</p></body>`,
		`<head></head><body><script>kit&#46;theme</script></body>`,
		`<head></head><body><div data-kit-component="th&notit;eme"></div></body>`,
		`<head></head><body><button data-kit-action="th&notit;eme"></button></body>`,
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
	if !injected(out) {
		t.Fatal("Force must inject without any usage")
	}
	if strings.Index(out, `data-kitwork-generated="appearance"`) > strings.Index(out, "a.css") {
		t.Error("forced pre-paint must land before the first stylesheet")
	}
	// A marker still pins the position — today's, and the one before the rename.
	for _, marker := range []string{`data-kitwork-generated="appearance"`, `data-kitwork-generated="theme"`} {
		pinned := Force(`<head><title>x</title><script ` + marker + `></script></head>`)
		if strings.Count(pinned, `data-kitwork-generated=`) != 1 || !injected(pinned) ||
			strings.Index(pinned, "<title>") > strings.Index(pinned, `data-kitwork-generated="appearance"`) {
			t.Errorf("marker %s should be filled in place and canonicalized under Force:\n%s", marker, pinned)
		}
	}
}

func TestPrepaintResolvesSystemBeforeFirstPaint(t *testing.T) {
	for _, fragment := range []string{
		`t=t&&t.toLowerCase()`,
		`t==="light"||t==="dark"||t==="system"`,
		`m=v(s.getItem("appearance"))||v(s.getItem("theme"))`,
		`m=m||v(r.getAttribute("data-kitwork-appearance"))||"system"`,
		`matchMedia("(prefers-color-scheme: dark)").matches`,
		`if(m==="dark")c.add("dark");else c.remove("dark")`,
		`r.style.colorScheme=m`,
	} {
		if !strings.Contains(prepaint, fragment) {
			t.Fatalf("prepaint is missing system-resolution contract %q", fragment)
		}
	}
	// The order is the contract: the stored choice, then the site's default, then the device. A
	// storage failure (the catch) must still fall through to the default and to matchMedia.
	storageCatch := strings.Index(prepaint, `catch(e){}`)
	siteDefault := strings.Index(prepaint, `data-kitwork-appearance`)
	mediaResolution := strings.Index(prepaint, `if(m==="system")`)
	if storageCatch < 0 || siteDefault < 0 || mediaResolution < 0 || storageCatch > siteDefault || siteDefault > mediaResolution {
		t.Fatal("storage failure must continue into the site default, then into system media resolution")
	}
}

// The site's default mode (router.appearance → <html data-kitwork-appearance>) is read off the page by
// the script, never written into it: the pre-paint a dark-default site gets is byte for byte the one
// every other site gets, so a single CSP hash covers them all.
func TestPrepaintIsOneScriptWhateverTheSiteDefault(t *testing.T) {
	plain := Force(`<html><head></head><body></body></html>`)
	for _, page := range []string{
		`<html data-kitwork-appearance="dark"><head></head><body></body></html>`,
		`<html data-kitwork-appearance="light"><head></head><body></body></html>`,
	} {
		out := Force(page)
		if !strings.Contains(out, `<script data-kitwork-generated="appearance">`) {
			t.Fatalf("no pre-paint on %s:\n%s", page, out)
		}
		if !injected(out) || strings.Replace(out, page[:strings.Index(page, "<head>")], "<html>", 1) != plain {
			t.Errorf("the pre-paint must not depend on the site's default — it must be the one constant script:\n got  %s\n want %s", out, plain)
		}
	}
}
