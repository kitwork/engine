package css

import (
	"testing"
)

// stubPalette installs a two-theme resolver and removes it afterwards.
func stubPalette(t *testing.T) {
	t.Helper()
	RegisterHighlightPalette(func(theme, role string) (string, bool) {
		tables := map[string]map[string]string{
			"tokyonight": {"DEFAULT": "#1a1b26", "keyword": "#bb9af7"},
			"classic":    {"DEFAULT": "#1e1e1e", "keyword": "#c586c0"},
		}
		table, ok := tables[theme]
		if !ok {
			table = tables["tokyonight"]
		}
		hex, ok := table[role]
		return hex, ok
	})
	t.Cleanup(func() { RegisterHighlightPalette(nil) })
}

// A site gets the code-surface colours without declaring anything.
func TestTerminalTokensResolveWithNoSiteConfiguration(t *testing.T) {
	stubPalette(t)
	if got := twColor("terminal", "", nil); got != Hex("#1a1b26").String() {
		t.Fatalf("bare `terminal` resolved to %q, want the default theme's ground", got)
	}
	if got := twColor("terminal-keyword", "", nil); got != Hex("#bb9af7").String() {
		t.Fatalf("`terminal-keyword` resolved to %q, want the default theme's keyword", got)
	}
}

// router.highlight("classic") has to reach colour resolution, or the theme name
// would style the syntax and leave the frame on the old palette.
func TestThemeNameSelectsThePalette(t *testing.T) {
	stubPalette(t)
	cfg := &Config{HighlightTheme: "classic"}
	if got := twColor("terminal-keyword", "", cfg); got != Hex("#c586c0").String() {
		t.Fatalf("the configured theme was ignored: got %q, want the classic keyword", got)
	}
}

// router.highlight({ keyword: … }) overrides one role of the chosen theme.
func TestPerRoleOverrideBeatsTheTheme(t *testing.T) {
	stubPalette(t)
	cfg := &Config{HighlightTheme: "classic", HighlightPalette: map[string]string{"keyword": "#ff0000"}}
	if got := twColor("terminal-keyword", "", cfg); got != Hex("#ff0000").String() {
		t.Fatalf("the per-role override lost to the theme: %q", got)
	}
	if got := twColor("terminal", "", cfg); got != Hex("#1e1e1e").String() {
		t.Fatalf("overriding one role changed an unrelated one: %q", got)
	}
}

// router.css() is the last word, as it is for every other token.
func TestSiteConfigurationBeatsBothThemeAndOverride(t *testing.T) {
	stubPalette(t)
	cfg := &Config{
		Colors:           map[string]Color{"terminal-keyword": Hex("#00ff00")},
		HighlightTheme:   "classic",
		HighlightPalette: map[string]string{"keyword": "#ff0000"},
	}
	if got := twColor("terminal-keyword", "", cfg); got != Hex("#00ff00").String() {
		t.Fatalf("router.css() lost to router.highlight(): %q", got)
	}
}


// Unwired, the token must not resolve — the same failure as any unknown colour,
// rather than a plausible-looking wrong one.
func TestTerminalTokensDoNotResolveWhenUnwired(t *testing.T) {
	RegisterHighlightPalette(nil)
	if got := twColor("terminal-keyword", "", nil); got != "" {
		t.Fatalf("an unwired terminal token resolved to %q, want no colour", got)
	}
}

// The prefix must be exact, or an unrelated token would be answered here.
func TestOnlyTerminalPrefixedNamesReachTheHighlightPalette(t *testing.T) {
	RegisterHighlightPalette(func(theme, role string) (string, bool) { return "#ff0000", true })
	t.Cleanup(func() { RegisterHighlightPalette(nil) })

	for _, name := range []string{"terminalish", "terminal-", "determinal"} {
		if got := twColor(name, "", nil); got == Hex("#ff0000").String() {
			t.Fatalf("%q was answered by the code-surface palette", name)
		}
	}
}

// An override value may name a token or spell a class, not only a colour. Those
// forms belong to class resolution; running them through Hex here produced
// Color{0,0,0}, so a site asking for its brand colour silently got black.
func TestNonHexOverrideDoesNotResolveAsAColour(t *testing.T) {
	stubPalette(t)
	for _, value := range []string{"brand", "text-pink-400", "font-bold", ""} {
		cfg := &Config{HighlightPalette: map[string]string{"keyword": value}}
		got := twColor("terminal-keyword", "", cfg)
		if got == Hex("#000000").String() {
			t.Fatalf("override %q resolved to black instead of falling through to the theme", value)
		}
		if got != Hex("#bb9af7").String() {
			t.Fatalf("override %q gave %q, want the theme's keyword colour", value, got)
		}
	}
}

// A real colour must still work on this path — the control that keeps the guard
// from passing by rejecting everything.
func TestHexOverrideStillResolvesAsAColour(t *testing.T) {
	stubPalette(t)
	cfg := &Config{HighlightPalette: map[string]string{"keyword": "#ff0000"}}
	if got := twColor("terminal-keyword", "", cfg); got != Hex("#ff0000").String() {
		t.Fatalf("a hex override stopped working: %q", got)
	}
}
