package work

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	jitcss "github.com/kitwork/engine/jit/css"
)

func themesFixture(t *testing.T, router string) *Tenant {
	t.Helper()
	root := t.TempDir()
	appDir := filepath.Join(root, "acme", "localhost")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "router.kitwork.js"), []byte(router), 0o644); err != nil {
		t.Fatal(err)
	}
	tenant := NewTenant(root, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	return tenant
}

const themesTokens = `theme: { extend: { colors: {
    canvas: "#ffeaec", paper: "#ffffff", panel: "#efeff1", line: "#e5e3e5",
    ink: pigment("#242442"), brand: palette("#f5225f"),
} } }`

// router.themes({ dark: true }) after router.css(): the served stylesheet carries a derived .dark
// block, bg-canvas is unchanged, and the pre-paint is forced on.
func TestRouterThemesDerivesDark(t *testing.T) {
	tenant := themesFixture(t, `
import { router, palette, pigment } from "kitwork";
router.css({ `+themesTokens+` }).themes({ dark: true });
router.get((ctx) => ctx.html("<div class='bg-canvas text-ink'>ok</div>"));
`)
	view := tenant.presentation().View()
	if view.ThemeMode != "force" {
		t.Fatalf("declaring a theme must turn the pre-paint on, got mode %q", view.ThemeMode)
	}
	if got := view.JITConfig.Themes["dark"]["canvas"]; got == (jitcss.Color{}) {
		t.Fatalf("dark canvas was not derived: %+v", view.JITConfig.Themes)
	}
	css := jitcss.GenerateJIT(`<div class="bg-canvas text-ink"></div>`, view.JITConfig)
	if !strings.Contains(css, ".dark { --color-brand") || !strings.Contains(css, "--color-canvas: ") {
		t.Fatalf("no derived .dark block in the stylesheet:\n%s", css)
	}
	if !strings.Contains(css, ".bg-canvas { background-color: rgb(var(--color-canvas") {
		t.Fatalf("bg-canvas must still read the variable, unchanged:\n%s", css)
	}
	if strings.Contains(css, ".bg-canvas-dark") {
		t.Fatal("a derived mode must not mint utilities")
	}
}

// Order does not matter: themes() before css() derives from the same tokens.
func TestRouterThemesBeforeCss(t *testing.T) {
	tenant := themesFixture(t, `
import { router, palette, pigment } from "kitwork";
router.themes({ dark: true }).css({ `+themesTokens+` });
router.get((ctx) => ctx.html("ok"));
`)
	dark := tenant.presentation().View().JITConfig.Themes["dark"]
	if _, ok := dark["paper"]; !ok {
		t.Fatalf("themes() before css() lost the tokens: %+v", dark)
	}
}

// CONTROL: a hand-written dark rung in css() wins over the derived one.
func TestRouterThemesHandRungWins(t *testing.T) {
	tenant := themesFixture(t, `
import { router, palette, pigment } from "kitwork";
router.css({ theme: { extend: { colors: { canvas: { DEFAULT: "#ffeaec", dark: "#101010" }, ink: pigment("#242442") } } } }).themes({ dark: true });
router.get((ctx) => ctx.html("ok"));
`)
	css := jitcss.GenerateJIT(`<div class="bg-canvas"></div>`, tenant.presentation().View().JITConfig)
	block := css[strings.Index(css, ".dark {"):]
	block = block[:strings.Index(block, "}")]
	if !strings.Contains(block, "--color-canvas: 16, 16, 16;") {
		t.Fatalf("hand-written canvas-dark must be the emitted value:\n%s", block)
	}
}

// CONTROL: without themes(), nothing is derived and the pre-paint stays on auto-scan.
func TestRouterWithoutThemesDerivesNothing(t *testing.T) {
	tenant := themesFixture(t, `
import { router, palette, pigment } from "kitwork";
router.css({ `+themesTokens+` });
router.get((ctx) => ctx.html("ok"));
`)
	view := tenant.presentation().View()
	if len(view.JITConfig.Themes) != 0 || view.ThemeMode != "" {
		t.Fatalf("no themes() must mean no derivation: themes=%v mode=%q", view.JITConfig.Themes, view.ThemeMode)
	}
}

// The anti-flash pre-paint is the theme's own job: a site that declares a class-switched dark in
// router.css() gets it on every page — no call, no switch — while nothing is derived (the
// stylesheet has no .dark block). "media" dark needs no pre-paint; a site that declared no dark at
// all keeps the per-page toggle scan.
func TestRouterCssDarkModeTurnsThePrepaintOnByItself(t *testing.T) {
	for _, c := range []struct{ darkMode, want string }{
		{`darkMode: ["class"], `, "force"},
		{`darkMode: "class", `, "force"},
		{`darkMode: ["selector", "[data-theme=dark]"], `, "force"},
		{`darkMode: "media", `, ""},
		{``, ""},
	} {
		tenant := themesFixture(t, `
import { router, palette, pigment } from "kitwork";
router.css({ `+c.darkMode+themesTokens+` });
router.get((ctx) => ctx.html("<div class='bg-canvas dark:bg-ink'>no toggle on this page</div>"));
`)
		view := tenant.presentation().View()
		if view.ThemeMode != c.want {
			t.Errorf("css({ %s… }): pre-paint mode = %q, want %q", c.darkMode, view.ThemeMode, c.want)
		}
		if len(view.JITConfig.Themes) != 0 {
			t.Errorf("css({ %s… }) must derive nothing, got %+v", c.darkMode, view.JITConfig.Themes)
		}
		if css := jitcss.GenerateJIT(`<div class="bg-canvas"></div>`, view.JITConfig); strings.Contains(css, ".dark { --color-") {
			t.Errorf("css({ %s… }) must not add a derived .dark block:\n%s", c.darkMode, css)
		}
	}
}

// CONTROL: router.themes({ prepaint: false }) is the one setting left — an opt-out that wins over
// both a declared darkMode and a derived mode, in either order.
func TestRouterThemesPrepaintFalseOptsOut(t *testing.T) {
	for _, router := range []string{
		`router.css({ darkMode: ["class"], ` + themesTokens + ` }).themes({ dark: true, prepaint: false });`,
		`router.themes({ prepaint: false }).css({ darkMode: ["class"], ` + themesTokens + ` });`,
	} {
		tenant := themesFixture(t, `
import { router, palette, pigment } from "kitwork";
`+router+`
router.get((ctx) => ctx.html("ok"));
`)
		if view := tenant.presentation().View(); view.ThemeMode != "off" {
			t.Errorf("%s\n  prepaint: false must turn the pre-paint off, got %q", router, view.ThemeMode)
		}
	}
}

// prepaint: true is not a setting (there is no opt-in — declare the dark you have): a named
// warning, and the mode stays whatever the declarations say. The deprecated router.jittheme(true)
// keeps forcing the pre-paint for existing sites.
func TestRouterThemesPrepaintTrueIsNotASettingAndJitthemeStillWorks(t *testing.T) {
	undeclared := themesFixture(t, `
import { router } from "kitwork";
router.css({}).themes({ prepaint: true });
router.get((ctx) => ctx.html("ok"));
`)
	if view := undeclared.presentation().View(); view.ThemeMode != "" || len(view.JITConfig.Themes) != 0 {
		t.Fatalf("prepaint: true must change nothing, got mode %q themes %+v", view.ThemeMode, view.JITConfig.Themes)
	}
	legacy := themesFixture(t, `
import { router } from "kitwork";
router.jittheme(true);
router.get((ctx) => ctx.html("ok"));
`)
	if view := legacy.presentation().View(); view.ThemeMode != "force" {
		t.Fatalf("router.jittheme(true) must still force the pre-paint, got %q", view.ThemeMode)
	}
}
