package stylesheet

import (
	"regexp"
	"strings"
	"testing"

	jitcss "github.com/kitwork/engine/jit/css"
)

// modes reads colors.css into its two blocks: custom property → value, for light (:root) and dark
// (:root[data-theme=dark] inside @media screen).
func modes(t *testing.T, css []byte) (light, dark map[string]string) {
	t.Helper()
	text := string(css)
	read := func(block string) map[string]string {
		out := map[string]string{}
		for _, declaration := range strings.Split(block, ";") {
			name, value, ok := strings.Cut(declaration, ":")
			if ok && strings.HasPrefix(name, "--") {
				out[name] = value
			}
		}
		return out
	}
	const lightOpen, darkOpen = ":root{", "@media screen{:root[data-theme=dark]{"
	if !strings.HasPrefix(text, lightOpen) {
		t.Fatalf("colors.css does not open with :root: %.60q", text)
	}
	end := strings.Index(text, "}\n")
	light = read(text[len(lightOpen):end])
	rest := text[end+2:]
	if rest == "" {
		return light, nil
	}
	if !strings.HasPrefix(rest, darkOpen) || !strings.HasSuffix(rest, "}}\n") {
		t.Fatalf("the dark block is not :root[data-theme=dark] for screens: %q", rest)
	}
	return light, read(strings.TrimSuffix(rest[len(darkOpen):], "}}\n"))
}

// A site that writes no router.colors still has its ground: canvas, paper, panel, ink, line, the brand
// and the status colours of Kit Design, the label each grows, and the words of .error. jit/css's own
// defaults, which a .ui site got before, have none of the surfaces or the text.
func TestColorsWithoutRouterColorsHasTheGround(t *testing.T) {
	p, err := NewPalette(false)
	if err != nil {
		t.Fatal(err)
	}
	light, dark := modes(t, Colors(p).CSS)
	for _, token := range []string{"canvas", "paper", "panel", "ink", "line", "brand", "brand-on", "success", "warning", "danger", "info", "info-on", "kitwork-error"} {
		if !regexp.MustCompile(`^#[0-9a-f]{6}$`).MatchString(light["--"+token]) {
			t.Errorf("--%s is %q", token, light["--"+token])
		}
	}
	if light["--canvas"] != "#f6f9fc" || light["--ink"] != "#0a2540" || light["--line"] != "#e6ebf1" {
		t.Errorf("not Kit Design's default skin: %v", light)
	}
	if dark != nil {
		t.Errorf("no dark value written, no dark block: %v", dark)
	}
	if !strings.Contains(string(Colors(p).CSS), ":root{color-scheme:light;") {
		t.Error("the light block names its scheme")
	}
	// Control: the palette a .ui site had without router.colors (jit/css's defaults) had no ground.
	for _, token := range []string{"canvas", "ink", "line"} {
		if _, ok := jitcss.DefaultConfig.Colors[token]; ok {
			t.Fatalf("control: jit/css's defaults now declare %s; this test no longer shows the difference", token)
		}
	}
}

// A branch's colors.css differs from the site's only in the tokens the branch re-values: the brand it
// writes, the label that grows from it, and the focus ring that may follow — in both modes.
func TestBranchFileDiffersOnlyInItsOwnTokens(t *testing.T) {
	site := examplePalette(t)
	branch := examplePalette(t, map[string]Color{"brand": {Light: "#2563eb"}})
	family := map[string]bool{"--brand": true, "--brand-on": true, "--kitwork-focus": true}
	siteFile, branchFile := Colors(site), Colors(branch)
	if siteFile.Name == branchFile.Name {
		t.Fatal("two palettes, one name")
	}
	if differing := outside(t, siteFile.CSS, branchFile.CSS, family); len(differing) > 0 {
		t.Fatalf("the branch changed tokens it does not write: %v", differing)
	}
	light, _ := modes(t, branchFile.CSS)
	if light["--brand"] != "#2563eb" || light["--brand-on"] != jitcss.Palette("#2563eb")["on"] {
		t.Fatalf("the branch's brand did not regrow its family: %v", light)
	}
	// Control: a branch that re-values ink moves the dark surfaces too (they take the hue of ink), and
	// the comparison reports them.
	inked := examplePalette(t, map[string]Color{"ink": {Light: "#1e3a8a"}})
	if differing := outside(t, siteFile.CSS, Colors(inked).CSS, family); len(differing) == 0 {
		t.Fatal("control: the comparison reports nothing for a branch that changes more than its brand")
	}
	// The same chain gives the same bytes, whatever order its tokens were declared in.
	if again := Colors(examplePalette(t, map[string]Color{"brand": {Light: "#2563EB"}})); again.Name != branchFile.Name {
		t.Fatal("the same palette gives another name")
	}
}

// outside lists the custom properties, in either mode, that differ between two colors.css and are not
// in allowed.
func outside(t *testing.T, a, b []byte, allowed map[string]bool) []string {
	t.Helper()
	aLight, aDark := modes(t, a)
	bLight, bDark := modes(t, b)
	var out []string
	compare := func(mode string, x, y map[string]string) {
		for name := range union(x, y) {
			if x[name] != y[name] && !allowed[name] {
				out = append(out, mode+" "+name+": "+x[name]+" → "+y[name])
			}
		}
	}
	compare("light", aLight, bLight)
	compare("dark", aDark, bDark)
	return out
}

func union(a, b map[string]string) map[string]bool {
	out := map[string]bool{}
	for k := range a {
		out[k] = true
	}
	for k := range b {
		out[k] = true
	}
	return out
}

// The focus ring is the brand where it reaches 3:1 on canvas and paper, else ink, decided per mode:
// a pale brand would draw a ring nobody sees.
func TestPaleBrandFocusFallsBackToInk(t *testing.T) {
	pale, err := NewPalette(true, map[string]Color{"brand": {Light: "#ffd166"}})
	if err != nil {
		t.Fatal(err)
	}
	light, _ := modes(t, Colors(pale).CSS)
	if jitcss.Contrast("#ffd166", "#f6f9fc") >= 3 {
		t.Fatal("the pale brand is not pale")
	}
	if light["--kitwork-focus"] != "var(--ink)" {
		t.Errorf("pale brand: the ring is %s, want var(--ink)", light["--kitwork-focus"])
	}
	// Control: a brand that reaches 3:1 is the ring.
	strong, _ := NewPalette(true, map[string]Color{"brand": {Light: "#2563eb"}})
	light, dark := modes(t, Colors(strong).CSS)
	if jitcss.Contrast("#2563eb", "#f6f9fc") < 3 || jitcss.Contrast("#2563eb", "#ffffff") < 3 {
		t.Fatal("control: the strong brand does not reach 3:1")
	}
	if light["--kitwork-focus"] != "var(--brand)" {
		t.Errorf("strong brand: the ring is %s, want var(--brand)", light["--kitwork-focus"])
	}
	// Per mode: a near-black brand reaches 3:1 on the light ground, never on the dark one.
	night, _ := NewPalette(false, map[string]Color{
		"brand":  {Light: "#1f2937", Dark: "#1f2937"},
		"canvas": {Light: "#ffffff", Dark: "#0b0b0c"},
		"paper":  {Light: "#ffffff", Dark: "#141416"},
	})
	light, dark = modes(t, Colors(night).CSS)
	if light["--kitwork-focus"] != "var(--brand)" || dark["--kitwork-focus"] != "var(--ink)" {
		t.Errorf("per mode: light %s, dark %s", light["--kitwork-focus"], dark["--kitwork-focus"])
	}
	if jitcss.Contrast("#000000", "#ffffff") < 20.9 || jitcss.Contrast("#ffffff", "#ffffff") != 1 {
		t.Fatal("control: the contrast formula is wrong")
	}
}

// colors.css carries only the chain's tokens, as full colours: none of jit/css's legacy palette, no
// *-dark copies, no rgb triplets, no --color- names, no --kitwork-brand.
func TestColorsHasNoLegacyNames(t *testing.T) {
	css := string(Colors(examplePalette(t)).CSS)
	for name := range jitcss.DefaultConfig.Colors {
		switch name {
		case "brand", "success", "warning", "danger", "info":
			continue
		}
		for _, found := range regexp.MustCompile(`--`+regexp.QuoteMeta(name)+`(-[a-z]+)?:`).FindAllString(css, -1) {
			if found != "--kitwork-focus:" && found != "--kitwork-error:" { // the engine's own ring and words of .error, not jit/css's kitwork colour
				t.Errorf("legacy jit/css colour %s", found)
			}
		}
	}
	for _, banned := range []string{"-dark:", "--color-", "--kitwork-brand", ", ", "rgb("} {
		if strings.Contains(css, banned) {
			t.Errorf("colors.css carries %q", banned)
		}
	}
	for _, line := range strings.Split(strings.TrimSpace(css), "\n") {
		for _, declaration := range strings.Split(line, ";") {
			if name, value, ok := strings.Cut(declaration, ":"); ok && strings.Contains(name, "--") && name != "--kitwork-focus" {
				if !regexp.MustCompile(`^#[0-9a-f]{6}`).MatchString(value) {
					t.Errorf("%s is %q: a full colour", name, value)
				}
			}
		}
	}
}

// A dark value anywhere gives the chain its dark look: written values win, every other token is
// derived (router.themes' formula), for screens only. dark=true asks for it without a written value.
func TestDarkLook(t *testing.T) {
	p := examplePalette(t)
	light, dark := modes(t, Colors(p).CSS)
	if dark["--canvas"] != "#141110" || dark["--ink"] != "#f3ece6" {
		t.Errorf("written dark values: %v", dark)
	}
	if dark["--brand"] == "" || dark["--brand"] == light["--brand"] || dark["--success-on"] == "" {
		t.Errorf("brand and the grown rungs derive a dark value: %v", dark)
	}
	if dark["--brand"] != "#d79565" || dark["--brand-on"] != "#1e130c" {
		t.Errorf("not router.themes' formula: brand %s on %s", dark["--brand"], dark["--brand-on"])
	}
	if _, d, _ := p.Value("brand"); d != dark["--brand"] {
		t.Errorf("Palette.Value and the file disagree: %s %s", d, dark["--brand"])
	}
	forced, _ := NewPalette(true, map[string]Color{"brand": {Light: "#c8742c"}})
	if _, d := modes(t, Colors(forced).CSS); d["--canvas"] == "" || d["--ink"] == "" {
		t.Errorf("router.appearance(\"dark\") without a written dark value: %v", d)
	}
	if !forced.Dark() || !p.Dark() {
		t.Error("Dark()")
	}
	plain, _ := NewPalette(false, map[string]Color{"brand": {Light: "#c8742c"}})
	if _, d := modes(t, Colors(plain).CSS); d != nil || plain.Dark() {
		t.Errorf("no dark value, no dark block: %v", d)
	}
}

// A token a folder writes is that folder's whole, and an accent it writes regrows its family: the
// site's hand-written brand.on does not survive a branch's new brand, unless the branch writes it.
func TestBranchRegrowsTheFamily(t *testing.T) {
	root := map[string]Color{"brand": {Light: "#c8742c"}, "brand.on": {Light: "#111111"}}
	site, _ := NewPalette(false, root)
	if l, _, _ := site.Value("brand.on"); l != "#111111" {
		t.Fatalf("a written rung is kept: %s", l)
	}
	branch, _ := NewPalette(false, root, map[string]Color{"brand": {Light: "#2563eb"}})
	grown := jitcss.Palette("#2563eb")
	for _, rung := range rungs {
		if l, _, _ := branch.Value("brand." + rung); l != grown[rung] {
			t.Errorf("brand.%s is %s, want %s grown from the branch's brand", rung, l, grown[rung])
		}
	}
	own, _ := NewPalette(false, root, map[string]Color{"brand": {Light: "#2563eb"}, "brand.on": {Light: "#222222"}})
	if l, _, _ := own.Value("brand.on"); l != "#222222" {
		t.Errorf("the branch's own rung: %s", l)
	}
	if branch.declared["brand.on"] || len(rungs) != 1 {
		t.Errorf("brand.on is grown, not declared; rungs %v", rungs)
	}
}

// An accent named like a Tailwind or a legacy jit/css colour (primary, sky) still gets its dark value.
// (blue, which this test once used, is a named colour now: no token may take its name.)
func TestAccentNamedLikeALegacyColourGetsItsDark(t *testing.T) {
	p, err := NewPalette(true, map[string]Color{"primary": {Light: "#4242ff"}, "sky": {Light: "#2563eb"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"primary", "sky"} {
		if light, dark, _ := p.Value(token); dark == "" || dark == light {
			t.Errorf("%s has no dark value of its own (%s / %s)", token, light, dark)
		}
	}
	for _, token := range []string{"primary.on", "sky.on"} {
		if _, dark, _ := p.Value(token); dark == "" {
			t.Errorf("%s has no dark value", token)
		}
	}
	// Control: given the names as they are, router.themes' formula skips them.
	config := &jitcss.Config{Colors: map[string]jitcss.Color{"ink": jitcss.Hex("#0a2540"), "primary": jitcss.Hex("#4242ff"), "sky": jitcss.Hex("#2563eb")}}
	jitcss.DeriveThemes(config, "dark")
	if _, ok := config.Themes["dark"]["primary"]; ok {
		t.Fatal("control: the formula now derives primary itself; the alias is no longer needed")
	}
}

// router.colors is checked when the chain resolves: a colour that is neither hex nor a named colour, a
// fixed colour or an old shade written as a token, a dark value written as a token, two names for one
// variable. (A dropped rung, a name ending in -<digits> and the engine's kitwork- names: operators_test.go.)
func TestPaletteRefusesWhatIsNotAPalette(t *testing.T) {
	cases := []struct {
		chain []map[string]Color
		want  string
	}{
		{[]map[string]Color{{"brand": {Light: "reddish"}}}, `"brand" is "reddish": a colour is "#rgb", "#rrggbb" or a named colour`},
		{[]map[string]Color{{"canvas": {Light: "#fff", Dark: "oklch(0.2 0 0)"}}}, `"canvas" (dark)`},
		{[]map[string]Color{{"white": {Light: "#fff"}}}, "fixed colour"},
		{[]map[string]Color{{"ink.70": {Light: "#555555"}}}, "ink-30 is ink made lighter in the markup, not a token"},
		{[]map[string]Color{{"Brand": {Light: "#555555"}}}, "not a token name"},
		{[]map[string]Color{{"brand.dark": {Light: "#555555"}}}, "dark value"},
		{[]map[string]Color{{"chart.muted": {Light: "#555555"}}, {"chart-muted": {Light: "#666666"}}}, "--chart-muted"},
	}
	for _, c := range cases {
		_, err := NewPalette(false, c.chain...)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v: %v, want %q", c.chain, err, c.want)
		}
	}
	p, err := NewPalette(false, map[string]Color{"brand": {Light: "#ABC"}})
	if err != nil {
		t.Fatal(err)
	}
	if l, _, _ := p.Value("brand"); l != "#aabbcc" {
		t.Errorf("#ABC reads as %s", l)
	}
}

// The grown rung (on) is jit/css's Palette of the hue, and Declared leaves it out.
func TestGrownRungsAreThePalettesOwn(t *testing.T) {
	p := examplePalette(t)
	grown := jitcss.Palette("#2f9e63")
	for _, rung := range rungs {
		if l, _, _ := p.Value("success." + rung); l != grown[rung] {
			t.Errorf("success.%s %s, want %s", rung, l, grown[rung])
		}
	}
	if strings.Join(p.Declared(), " ") != "brand canvas danger info ink line panel paper success warning" {
		t.Errorf("declared %v", p.Declared())
	}
	if !p.Has("info.on") || p.Has("ink.on") || p.Has("success.soft") || p.Has("success.deep") {
		t.Error("only accents grow a family, and the family is on alone")
	}
}
