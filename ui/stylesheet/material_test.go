package stylesheet

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// glassRule is the rule of one glass class, its fallback right after it: the tint (the background
// without its /N, --kitwork-glass) at the glass's opacity over the blurred, saturated backdrop
// (-webkit- first, for Safari 15.4 to 17), and the background as written (--kitwork-background, its /N
// the fallback's) where the browser cannot blur the backdrop: glass first, background second (decision
// of 13/10/2026). That is the one fallback: the glass always shows, also where the reader asked for
// less transparency (decision of 13/10/2026: no prefers-reduced-transparency rule, as on the sites
// written with Tailwind's backdrop-blur).
func glassRule(selector, opacity, blur string) string {
	return selector + "{background-color:color-mix(in srgb,var(--kitwork-glass,var(--paper)) " + opacity + "%,transparent);" +
		"-webkit-backdrop-filter:blur(" + blur + "px) saturate(180%);backdrop-filter:blur(" + blur + "px) saturate(180%)}\n" +
		"@supports not ((backdrop-filter:blur(1px)) or (-webkit-backdrop-filter:blur(1px))){" + selector + "{background-color:var(--kitwork-background,var(--paper))}}\n"
}

// material (decision M) is a presentation attribute like any other: the class is the attribute as
// written, with width and state prefixes, and every value has its rule — glass/N the tint at N% over a
// blur that grows with it (glass/60 8px, glass 12px, glass/88 20px, between them along a line: glass/70
// 11px, glass/80 15px; 8px below, 20px above), all saturate(180%); none gives back the background the
// frame has without a material and no filter. Each glass carries its one fallback inside its own
// condition, the @supports one, and nothing after it: no rule gives way to the reader's
// prefers-reduced-transparency (decision of 13/10/2026). CONTROL: none has no fallback (nothing to fall
// back from).
func TestMaterialClassesAndRules(t *testing.T) {
	table := NewTable(examplePalette(t))
	glass := map[string][2]string{"glass/60": {"60", "8"}, "glass": {"75", "12"}, "glass/75": {"75", "12"}, "glass/88": {"88", "20"},
		"glass/70": {"70", "11"}, "glass/80": {"80", "15"}, "glass/65": {"65", "9"}, "glass/85": {"85", "18"}, "glass/10": {"10", "8"}, "glass/95": {"95", "20"}}
	for value, g := range glass {
		for _, c := range []struct {
			in     Intent
			class  string
			before string // what opens the rule's block, or "" for the page layer itself
			state  string
		}{
			{Intent{Name: "material", Value: value}, "material-" + value, "@layer page{\n", ""},
			{Intent{Width: "medium", Name: "material", Value: value}, "medium:material-" + value, "@media (min-width:768px){\n", ""},
			{Intent{State: "scrolled", Name: "material", Value: value}, "scrolled:material-" + value, "", ":where(:root[data-kitwork-scrolled] *)"},
			{Intent{Width: "medium", State: "hover", Name: "material", Value: value}, "medium+hover:material-" + value, "@media (min-width:768px) and (hover:hover){\n", ":where(:hover)"},
		} {
			var r Rules
			class, err := table.Class(&r, c.in)
			if err != nil {
				t.Fatalf("%+v: %v", c.in, err)
			}
			if class != c.class {
				t.Errorf("%+v: class %q, want %q", c.in, class, c.class)
			}
			css := string(r.CSS())
			// the glass and its fallback, then the end of its condition: nothing else is written for it
			if want := c.before + glassRule("."+escape(class)+c.state, g[0], g[1]) + "}\n"; !strings.Contains(css, want) {
				t.Errorf("%s:\n%s\nwant\n%s", class, css, want)
			}
			if strings.Contains(css, "prefers-reduced-transparency") {
				t.Errorf("%s: the glass always shows (13/10/2026), no rule gives way to reduced transparency:\n%s", class, css)
			}
			if !r.Has(class) {
				t.Errorf("%s is not recorded", class)
			}
		}
		if got, ok := GlassOf(value); !ok || got.Saturate != 180 || itoa(got.Opacity) != g[0] || itoa(got.Blur) != g[1] {
			t.Errorf("GlassOf(%q) = %+v %v", value, got, ok)
		}
	}
	var r Rules
	class, err := table.Class(&r, Intent{Width: "large", Name: "material", Value: "none"})
	if err != nil || class != "large:material-none" {
		t.Fatalf("none: %q %v", class, err)
	}
	css := string(r.CSS())
	if want := "@media (min-width:1024px){\n" + `.large\:material-none{background-color:var(--kitwork-background,transparent);-webkit-backdrop-filter:none;backdrop-filter:none}` + "\n}"; !strings.Contains(css, want) {
		t.Errorf("none:\n%s\nwant %s", css, want)
	}
	if strings.Contains(css, "@supports") {
		t.Errorf("control: none has nothing to fall back from:\n%s", css)
	}
	if _, ok := GlassOf("none"); ok {
		t.Error("none is no glass")
	}
	for _, word := range []string{"glass.thin", "glass/5", "glass/100", "glass/070", "glass/", "Glass"} {
		if _, ok := GlassOf(word); ok {
			t.Errorf("%q is no glass", word)
		}
	}
}

// glass/N replaced the glass words (decision of 13/10/2026) with the same CSS: glass/60, glass and
// glass/88 write exactly what glass.thin, glass and glass.thick wrote, the page's stylesheet byte for
// byte but the class's name (the stylesheets below were written down from the engine before the
// change), the rule that gave way to the reader's prefers-reduced-transparency, gone since the
// decision of 13/10/2026 that the glass always shows, and the variable the glass reads its tint in:
// --kitwork-glass, the background without its /N, since the decision of 13/10/2026 that the glass
// comes first and the background second (the fallback still reads --kitwork-background, as written).
// glass/75 is glass under another class. The blur never shrinks as the glass thickens. The values a
// material from data may take are the same three glasses and none, in that order.
func TestGlassN(t *testing.T) {
	before := map[string]string{
		"glass.thin":  "@layer reboot,elements,page,show;\n@layer page{\n.material-glass\\.thin{background-color:color-mix(in srgb,var(--kitwork-background,var(--paper)) 60%,transparent);-webkit-backdrop-filter:blur(8px) saturate(180%);backdrop-filter:blur(8px) saturate(180%)}\n@supports not ((backdrop-filter:blur(1px)) or (-webkit-backdrop-filter:blur(1px))){.material-glass\\.thin{background-color:var(--kitwork-background,var(--paper))}}\n@media (prefers-reduced-transparency:reduce){.material-glass\\.thin{background-color:var(--kitwork-background,var(--paper));-webkit-backdrop-filter:none;backdrop-filter:none}}\n}\n",
		"glass":       "@layer reboot,elements,page,show;\n@layer page{\n.material-glass{background-color:color-mix(in srgb,var(--kitwork-background,var(--paper)) 75%,transparent);-webkit-backdrop-filter:blur(12px) saturate(180%);backdrop-filter:blur(12px) saturate(180%)}\n@supports not ((backdrop-filter:blur(1px)) or (-webkit-backdrop-filter:blur(1px))){.material-glass{background-color:var(--kitwork-background,var(--paper))}}\n@media (prefers-reduced-transparency:reduce){.material-glass{background-color:var(--kitwork-background,var(--paper));-webkit-backdrop-filter:none;backdrop-filter:none}}\n}\n",
		"glass.thick": "@layer reboot,elements,page,show;\n@layer page{\n.material-glass\\.thick{background-color:color-mix(in srgb,var(--kitwork-background,var(--paper)) 88%,transparent);-webkit-backdrop-filter:blur(20px) saturate(180%);backdrop-filter:blur(20px) saturate(180%)}\n@supports not ((backdrop-filter:blur(1px)) or (-webkit-backdrop-filter:blur(1px))){.material-glass\\.thick{background-color:var(--kitwork-background,var(--paper))}}\n@media (prefers-reduced-transparency:reduce){.material-glass\\.thick{background-color:var(--kitwork-background,var(--paper));-webkit-backdrop-filter:none;backdrop-filter:none}}\n}\n",
	}
	// what was written less the line of the reduced-transparency rule, which the glass no longer writes
	// (13/10/2026), and how many lines that took out
	withoutReduced := func(stylesheet string) (string, int) {
		var kept []string
		taken := 0
		for _, line := range strings.SplitAfter(stylesheet, "\n") {
			if strings.HasPrefix(line, "@media (prefers-reduced-transparency:reduce){") {
				taken++
				continue
			}
			kept = append(kept, line)
		}
		return strings.Join(kept, ""), taken
	}
	// the glass's own tint, which it reads in --kitwork-glass since glass first, background second
	// (13/10/2026); the fallback's var(--kitwork-background,var(--paper)) stays as it was
	const tintBefore, tintNow = "color-mix(in srgb,var(--kitwork-background,var(--paper))", "color-mix(in srgb,var(--kitwork-glass,var(--paper))"
	table := NewTable(mustPalette(t))
	for old, now := range map[string]string{"glass.thin": "glass/60", "glass": "glass", "glass.thick": "glass/88"} {
		css := pageCSS(t, table, Intent{Name: "material", Value: now})
		kept, taken := withoutReduced(before[old])
		if taken != 1 || !strings.Contains(kept, "@supports not") || strings.Count(kept, tintBefore) != 1 ||
			!strings.Contains(kept, "{background-color:var(--kitwork-background,var(--paper))}}") {
			t.Fatalf("control: what %q wrote has %d reduced-transparency rules to take out, want one, keeps its @supports fallback on the background, and reads its tint once:\n%s", old, taken, kept)
		}
		kept = strings.Replace(kept, tintBefore, tintNow, 1)
		want := strings.ReplaceAll(kept, "."+escape("material-"+old)+"{", "."+escape("material-"+now)+"{")
		if css != want {
			t.Errorf("material=%q is not what material=%q wrote:\n got %s\nwant %s", now, old, css, want)
		}
	}
	if plain, same := pageCSS(t, table, Intent{Name: "material", Value: "glass"}), pageCSS(t, table, Intent{Name: "material", Value: "glass/75"}); strings.ReplaceAll(plain, `.material-glass{`, `.material-glass\/75{`) != same {
		t.Errorf("glass/75 is glass under its own class:\n%s\n%s", plain, same)
	}
	last := 0
	for n := leastGlass; n <= mostGlass; n++ {
		g, ok := GlassOf("glass/" + itoa(n))
		if !ok || g.Opacity != n || g.Blur < last || g.Blur < 8 || g.Blur > 20 {
			t.Fatalf("glass/%d: %+v %v, the blur after %dpx", n, g, ok, last)
		}
		last = g.Blur
	}
	if got := strings.Join(Materials(), " "); got != "none glass/60 glass glass/88" {
		t.Errorf("a material from data: %s, want none glass/60 glass glass/88", got)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for ; n > 0; n /= 10 {
		s = string(rune('0'+n%10)) + s
	}
	return s
}

// The tint follows the background (decision M): on a page with a material every background class
// hands its colour over twice in its own rule — a token, one made lighter or stronger, a fixed colour,
// the engine's own background of a pinned frame. Glass first, background second (decision of
// 13/10/2026): --kitwork-glass is the colour without its /N, the glass's tint (an operator keeps its
// operator: brand+15/80 is brand+15 glass), and --kitwork-background is the colour exactly as written,
// /N included, what the fallback and none read. The glass reads --kitwork-glass at its coverage, paper
// when there is none. background="brand" material="glass" is brand glass, the colour written once.
// CONTROL: a page without a material keeps the bytes it always had, no variable; a colour with no /N
// hands the same colour over twice.
func TestMaterialTintFollowsTheBackground(t *testing.T) {
	table := NewTable(examplePalette(t))
	var r Rules
	got := classes(t, table, &r,
		Intent{Tag: "box", Name: "background", Value: "brand"},
		Intent{Tag: "box", Name: "material", Value: "glass"},
		Intent{Tag: "card", State: "scrolled", Name: "background", Value: "ink-30"},
		Intent{Tag: "card", Name: "background", Value: "white"},
		Intent{Tag: "box", Name: "background", Value: "paper/80"},
		Intent{Tag: "box", Name: "background", Value: "brand+15/80"},
		Intent{Tag: "box", Width: "medium", Name: "background", Value: "ink-30/50"},
		Intent{Tag: "box", Name: "background", Value: "white/50"},
	)
	if got != "background-brand material-glass scrolled:background-ink-30 background-white background-paper/80 background-brand+15/80 medium:background-ink-30/50 background-white/50" {
		t.Fatalf("classes %q", got)
	}
	css := string(r.CSS())
	mustHave := []string{
		`.background-brand{background-color:var(--brand);--kitwork-background:var(--brand);--kitwork-glass:var(--brand)}`,
		`.background-white{background-color:#fff;--kitwork-background:#fff;--kitwork-glass:#fff}`,
		`.scrolled\:background-ink-30:where(:root[data-kitwork-scrolled] *){background-color:color-mix(in oklab,var(--ink) 70%,var(--canvas));--kitwork-background:color-mix(in oklab,var(--ink) 70%,var(--canvas));--kitwork-glass:color-mix(in oklab,var(--ink) 70%,var(--canvas))}`,
		// with /N: the background and the fallback as written, the glass without it
		`.background-paper\/80{background-color:color-mix(in srgb,var(--paper) 80%,transparent);--kitwork-background:color-mix(in srgb,var(--paper) 80%,transparent);--kitwork-glass:var(--paper)}`,
		`.background-brand\+15\/80{background-color:color-mix(in srgb,var(--kitwork-strong-15-brand) 80%,transparent);--kitwork-background:color-mix(in srgb,var(--kitwork-strong-15-brand) 80%,transparent);--kitwork-glass:var(--kitwork-strong-15-brand)}`,
		`.medium\:background-ink-30\/50{background-color:color-mix(in srgb,color-mix(in oklab,var(--ink) 70%,var(--canvas)) 50%,transparent);--kitwork-background:color-mix(in srgb,color-mix(in oklab,var(--ink) 70%,var(--canvas)) 50%,transparent);--kitwork-glass:color-mix(in oklab,var(--ink) 70%,var(--canvas))}`,
		`.background-white\/50{background-color:color-mix(in srgb,#fff 50%,transparent);--kitwork-background:color-mix(in srgb,#fff 50%,transparent);--kitwork-glass:#fff}`,
		// the glass reads the tint, and paper when the frame has none; its fallback the background as written
		`.material-glass{background-color:color-mix(in srgb,var(--kitwork-glass,var(--paper)) 75%,transparent);`,
		`{.material-glass{background-color:var(--kitwork-background,var(--paper))}}`,
	}
	for _, rule := range mustHave {
		if !strings.Contains(css, rule) {
			t.Errorf("missing %s\n%s", rule, css)
		}
	}
	// a background class keeps one rule: the variables ride in it
	if n := rulesOf(css, "background-brand"); n != 1 {
		t.Errorf("background-brand has %d rules:\n%s", n, css)
	}
	// the variables are not something that animates: a scrolled background moves its colour only
	if strings.Contains(css, "--kitwork-background .15s") || strings.Contains(css, "--kitwork-glass .15s") {
		t.Errorf("a variable does not animate:\n%s", css)
	}
	// GlassColour is the value without its /N, the operator kept; anything else as it is
	for value, want := range map[string]string{"paper/80": "paper", "brand+15/80": "brand+15", "ink-30/50": "ink-30", "white/0": "white",
		"brand": "brand", "ink-30": "ink-30", "glow": "glow", " paper/80 ": "paper", "{{ tone }}": "{{ tone }}"} {
		if got := GlassColour(value); got != want {
			t.Errorf("GlassColour(%q) = %q, want %q", value, got, want)
		}
	}

	var plain Rules
	classes(t, table, &plain,
		Intent{Name: "background", Value: "brand"},
		Intent{Name: "background", Value: "paper/80"},
		Intent{State: "scrolled", Name: "background", Value: "ink-30"})
	if css := string(plain.CSS()); strings.Contains(css, "--kitwork-background") || strings.Contains(css, "--kitwork-glass") ||
		!strings.Contains(css, ".background-brand{background-color:var(--brand)}\n") ||
		!strings.Contains(css, `.background-paper\/80{background-color:color-mix(in srgb,var(--paper) 80%,transparent)}`+"\n") {
		t.Errorf("control: a page without a material has no variable:\n%s", css)
	}
	// whichever order the page records them in: the material may come after the background
	var later Rules
	classes(t, table, &later, Intent{Name: "background", Value: "brand"})
	before := string(later.CSS())
	classes(t, table, &later, Intent{State: "hover", Name: "material", Value: "glass/60"})
	if strings.Contains(before, "--kitwork-background") || !strings.Contains(string(later.CSS()), `.background-brand{background-color:var(--brand);--kitwork-background:var(--brand);--kitwork-glass:var(--brand)}`) {
		t.Errorf("a material recorded after the background still gets its tint:\n%s", later.CSS())
	}
}

// Every frame with a material reads only its own background: reboot.css starts it from nothing, both
// variables, the background as written and the glass's tint, so it never takes the colour of a frame
// around it (custom properties inherit), and a card or a popover from its paper, which material="none"
// also gives back. Both rules weigh nothing, in the elements layer, the card's after the reset; they
// match a material class written with or without a prefix.
func TestRebootStartsEveryMaterialFromItsOwnBackground(t *testing.T) {
	css := string(Reboot().CSS)
	elements := strings.Index(css, "@layer elements{")
	reset := strings.Index(css, `:where([class^="material-"],[class*=" material-"],[class*=":material-"]){--kitwork-background:initial;--kitwork-glass:initial}`)
	paper := strings.Index(css, `:where(.card,.popover):where([class^="material-"],[class*=" material-"],[class*=":material-"]){--kitwork-background:var(--paper);--kitwork-glass:var(--paper)}`)
	card := strings.Index(css, ":where(.card){background:var(--paper)")
	if elements < 0 || reset < elements || paper < reset || card < 0 {
		t.Fatalf("reboot.css: elements %d, reset %d, card and popover %d, card look %d\n%s", elements, reset, paper, card, css)
	}
}

// In the page layer the material comes right after background in the table's order: at one
// condition the glass wins over the plain background (both set background-color), and the fixed state
// order holds around it — a hover background still beats a base material while the pointer is on it,
// a scrolled material beats the base background and loses to hover, a width background beats a base
// material from that width. CONTROL: alphabetical order would put border before material; the table
// keeps border after it, so the order is the table's.
func TestMaterialWinsOverBackgroundAtOneConditionAndKeepsTheStateOrder(t *testing.T) {
	table := NewTable(examplePalette(t))
	var r Rules
	classes(t, table, &r, // in no particular order
		Intent{State: "hover", Name: "background", Value: "brand"},
		Intent{Name: "material", Value: "glass"},
		Intent{Width: "medium", Name: "background", Value: "panel"},
		Intent{State: "scrolled", Name: "material", Value: "glass/88"},
		Intent{Name: "border", Value: "line"},
		Intent{Name: "background", Value: "paper"},
		Intent{State: "scrolled", Name: "background", Value: "canvas"},
	)
	want := []string{
		`.background-paper{`,
		`.material-glass{`,
		`.border-line{`,
		`.medium\:background-panel{`,
		`.scrolled\:background-canvas:where(`,
		`.scrolled\:material-glass\/88:where(`,
		`.hover\:background-brand:where(:hover)`,
	}
	var got []string
	for _, line := range strings.Split(string(r.CSS()), "\n") {
		if strings.HasPrefix(line, ".") && !strings.Contains(line, "{transition:") {
			got = append(got, line)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("%d rules, want %d:\n%s", len(got), len(want), r.CSS())
	}
	for i := range want {
		if !strings.HasPrefix(got[i], want[i]) {
			t.Fatalf("rule %d is %q, want %s…\n%s", i, got[i], want[i], r.CSS())
		}
	}
	// the fallback of a glass stands right after it, inside its condition, before what comes after it,
	// and no other at-rule follows it there: the @supports one is its only fallback, none for reduced
	// transparency (13/10/2026)
	css := string(r.CSS())
	const supports = "\n@supports not ((backdrop-filter:blur(1px)) or (-webkit-backdrop-filter:blur(1px))){.material-glass{background-color:var(--kitwork-background,var(--paper))}}\n"
	own := strings.Index(css, ".material-glass{")
	fallback := strings.Index(css, supports)
	border := strings.Index(css, ".border-line{")
	if own < 0 || fallback < own || strings.Contains(css[own:fallback], "\n") || border < fallback ||
		strings.Contains(css[fallback+len(supports):border], "@") {
		t.Errorf("the fallback belongs right after its glass, alone, before what comes after it: own %d, @supports %d, next %d\n%s", own, fallback, border, css)
	}
	if !(attributeRank["background"] < attributeRank["material"] && attributeRank["material"] < attributeRank["border"]) {
		t.Fatal("material sits between background and border in the table")
	}
	if !("border" < "material") {
		t.Fatal("control: alphabetical order no longer differs from the table's")
	}
}

// A scrolled: material animates in as the header floats up: its transition lists exactly what it sets
// — background-color and the backdrop filter, both spellings — and with another scrolled: class on the
// element, the element's own transition lists them all. Reduced motion makes it instant (reboot.css).
func TestMaterialScrolledTransition(t *testing.T) {
	table := NewTable(examplePalette(t))
	var r Rules
	got := classes(t, table, &r,
		Intent{Name: "background", Value: "canvas"},
		Intent{Name: "pin", Value: "top"},
		Intent{State: "scrolled", Name: "material", Value: "glass"},
		Intent{State: "scrolled", Name: "shadow", Value: "small"})
	r.Transition(strings.Fields(got))
	css := string(r.CSS())
	for _, rule := range []string{
		`.scrolled\:material-glass{transition:background-color .15s,-webkit-backdrop-filter .15s,backdrop-filter .15s}`,
		`.scrolled\:material-glass:where(.scrolled\:shadow-small){transition:background-color .15s,-webkit-backdrop-filter .15s,backdrop-filter .15s,box-shadow .15s}`,
	} {
		if !strings.Contains(css, rule) {
			t.Errorf("missing %s\n%s", rule, css)
		}
	}
	if strings.Contains(css, "transition:all") || strings.Contains(css, "--kitwork-background .15s") {
		t.Errorf("the transition lists what moves, nothing else:\n%s", css)
	}
	if !strings.Contains(string(Reboot().CSS), "transition-duration:0.01ms!important") {
		t.Error("reduced motion no longer makes every transition instant")
	}
	// control: a material without a state does not animate
	var still Rules
	classes(t, table, &still, Intent{Name: "material", Value: "glass"})
	if strings.Contains(string(still.CSS()), "transition") {
		t.Errorf("control: a base material has no transition:\n%s", still.CSS())
	}
}

// A word material does not take is an error in the author's language, naming what it takes, and the
// one meant when a word says it: the glass words of before are known words with a replacement
// (glass.thin is glass/60, glass.thick glass/88: decision of 13/10/2026), a coverage after a dot is
// written after a slash, one outside 10 to 95 is brought into it; a value from data compiles every
// glass ahead. CONTROL: the replacements read.
func TestMaterialValidation(t *testing.T) {
	table := NewTable(examplePalette(t))
	const thin, thick = "bậc chữ của kính đã bỏ (13/10/2026): kính mỏng giờ là glass/60 (màu kính phủ 60%)", "bậc chữ của kính đã bỏ (13/10/2026): kính dày giờ là glass/88"
	const coverage = "N sau glass/ là độ phủ của màu kính, số nguyên từ 10 đến 95 (glass/60 mỏng, glass/75 vừa, glass/88 dày)"
	for value, want := range map[string][2]string{
		"glass.thin":  {"glass/60", `material="glass.thin": ` + thin},
		"glass-thin":  {"glass/60", `material="glass-thin": ` + thin},
		"thin":        {"glass/60", `material="thin": ` + thin},
		"glass.thick": {"glass/88", `material="glass.thick": ` + thick},
		"glass-thick": {"glass/88", `material="glass-thick": ` + thick},
		"thick":       {"glass/88", `material="thick": ` + thick},
		"glass.70":    {"glass/70", `material="glass.70": độ phủ của kính viết sau dấu /: glass/70`},
		"glass.90":    {"glass/90", `material="glass.90": độ phủ của kính viết sau dấu /: glass/90`},
		"glass.5":     {"glass/10", `material="glass.5": độ phủ của kính viết sau dấu /: glass/10 (N từ 10 đến 95)`},
		"glass/5":     {"glass/10", `material="glass/5": ` + coverage},
		"glass/100":   {"glass/95", `material="glass/100": ` + coverage},
		"glass/070":   {"glass/70", `material="glass/070": ` + coverage},
		"glass/":      {"", `material="glass/": ` + coverage},
		"glass/half":  {"", `material="glass/half": ` + coverage},
		"frosted":     {"glass", `material="frosted": viết material="glass". material nhận none, glass hoặc glass/N (N từ 10 đến 95; glass là glass/75)`},
		"acrylic":     {"glass", `material="acrylic": acrylic là chất liệu cửa sổ của Windows, chưa có; khung thì viết glass (glass/60 mỏng, glass/75 vừa, glass/88 dày)`},
		"false":       {"none", `viết material="none"`},
		"metal":       {"", `material nhận none, glass hoặc glass/N (N từ 10 đến 95; glass là glass/75), không có "metal"`},
		"glass.soft":  {"", `không có "glass.soft"`},
	} {
		_, err := table.Class(nil, Intent{Name: "material", Value: value})
		var p *Problem
		if !errors.As(err, &p) {
			t.Errorf("material=%q: %v", value, err)
			continue
		}
		if p.Suggestion != want[0] || !strings.Contains(p.Message, want[1]) || p.Name != "material" || p.Value != value {
			t.Errorf("material=%q:\n got %+v\nwant suggestion %q, message with %q", value, p, want[0], want[1])
		}
		if p.Suggestion != "" {
			if _, err := table.Class(nil, Intent{Name: "material", Value: p.Suggestion}); err != nil {
				t.Errorf("CONTROL: material=%q, the replacement of %q, does not read: %v", p.Suggestion, value, err)
			}
		}
	}
	var r Rules
	domain, err := table.Domain(&r, Intent{Tag: "box", Name: "material"})
	if err != nil {
		t.Fatal(err)
	}
	if len(domain) != 4 || domain["glass/60"] != "material-glass/60" || domain["glass"] != "material-glass" ||
		domain["glass/88"] != "material-glass/88" || domain["none"] != "material-none" {
		t.Errorf("material from data: %v", domain)
	}
}

// TintOf finds the backgrounds a material at its condition reads: the one written for the condition
// that holds whenever the material's does and comes last in the fixed order.
func TestTintOf(t *testing.T) {
	backgrounds := []Intent{
		{Name: "background", Value: "paper"},
		{State: "hover", Name: "background", Value: "brand"},
		{Width: "medium", Name: "background", Value: "panel"},
		{State: "scrolled", Name: "background", Value: "canvas"},
		{Width: "large", State: "scrolled", Name: "background", Value: "ink"},
		{State: "hover,focus", Name: "background", Value: "line"},
	}
	cases := []struct {
		material Intent
		want     string
	}{
		{Intent{Name: "material", Value: "glass"}, "paper"},
		{Intent{Width: "small", Name: "material", Value: "glass"}, "paper"},     // medium starts wider
		{Intent{Width: "wide", Name: "material", Value: "glass"}, "panel"},      // medium holds from wide on
		{Intent{State: "scrolled", Name: "material", Value: "glass"}, "canvas"}, // large+scrolled starts wider
		{Intent{Width: "large", State: "scrolled", Name: "material", Value: "glass"}, "ink"},
		{Intent{State: "hover", Name: "material", Value: "glass"}, "brand line"}, // two written for hover
		{Intent{State: "focus", Name: "material", Value: "glass"}, "line"},
		{Intent{State: "pressed", Name: "material", Value: "glass"}, "paper"},
		{Intent{State: "hover,focus", Name: "material", Value: "glass"}, ""}, // a group: ask per state
	}
	for _, c := range cases {
		var got []string
		for _, b := range TintOf(c.material, backgrounds) {
			got = append(got, b.Value)
		}
		if strings.Join(got, " ") != c.want {
			t.Errorf("%+v: tint %q, want %q", c.material, strings.Join(got, " "), c.want)
		}
	}
	if len(TintOf(Intent{Name: "material", Value: "glass"}, nil)) != 0 {
		t.Error("no background: no tint (paper)")
	}
}

// readsOf names every background a glass reads at some moment, its tint and its fallback (glass first,
// background second: decision of 13/10/2026, the /N of such a background is the fallback's opacity and
// says nothing). These are the cases the warning about a /N under a material once stood at
// (CheckMaterial, removed that day): the background of the glass's own condition, the base one under a
// prefixed glass, one a {{ }} spells out, a wider width under a scrolled: glass, an earlier state under
// a hover: one, each state of a group. CONTROLS: a background that beats the glass whenever both hold
// (a hover or a later state, a wider width over a base glass) and one a background of the glass's own
// condition always hides are not read.
func TestReadsOf(t *testing.T) {
	reads := func(material Intent, backgrounds []Intent) []int {
		states := []string{""}
		if s := strings.TrimSpace(material.State); s != "" {
			states = strings.Split(s, ",")
		}
		var out []int
		for _, state := range states {
			one := material
			one.State = state
			for _, i := range readsOf(one, states, backgrounds) {
				if indexOfInt(out, i) < 0 {
					out = append(out, i)
				}
			}
		}
		return out
	}
	cases := []struct {
		material    Intent
		backgrounds []Intent
		want        []int
	}{
		{Intent{Name: "material", Value: "glass"}, []Intent{{Name: "background", Value: "paper/80"}}, []int{0}},
		{Intent{Name: "material", Value: "glass/60"}, []Intent{{Name: "background", Value: "paper/80"}}, []int{0}},
		{Intent{State: "scrolled", Name: "material", Value: "glass/60"},
			[]Intent{{Name: "background", Value: "canvas"}, {State: "scrolled", Name: "background", Value: "white/80"}}, []int{1}},
		{Intent{Width: "medium", Name: "material", Value: "glass/88"}, []Intent{{Width: "medium", Name: "background", Value: "ink-30/50"}}, []int{0}},
		{Intent{Name: "material", Value: "glass/70"}, []Intent{{Name: "background", Value: "brand+15/80"}}, []int{0}},
		{Intent{State: "scrolled", Name: "material", Value: "glass"}, []Intent{{Name: "background", Value: "paper/80"}}, []int{0}},
		{Intent{State: "hover,focus", Name: "material", Value: "glass"},
			[]Intent{{Name: "background", Value: "paper"}, {State: "focus", Name: "background", Value: "brand/50"}}, []int{0, 1}},
		{Intent{State: "scrolled", Name: "material", Value: "glass"}, []Intent{{Width: "medium", Name: "background", Value: "paper/80"}}, []int{0}},
		{Intent{State: "hover", Name: "material", Value: "glass"}, []Intent{{State: "scrolled", Name: "background", Value: "paper/80"}}, []int{0}},
		{Intent{Width: "medium", State: "hover", Name: "material", Value: "glass"},
			[]Intent{{Name: "background", Value: "paper"}, {Width: "large", Name: "background", Value: "paper/80"}}, []int{0, 1}},
		// CONTROLS: not read
		{Intent{Name: "material", Value: "glass"}, []Intent{{Name: "background", Value: "paper"}, {State: "hover", Name: "background", Value: "brand/80"}}, []int{0}},
		{Intent{State: "scrolled", Name: "material", Value: "glass"}, []Intent{{Name: "background", Value: "paper/80"}, {State: "scrolled", Name: "background", Value: "paper"}}, []int{1}},
		{Intent{Name: "material", Value: "glass"}, []Intent{{Width: "medium", Name: "background", Value: "paper/80"}}, nil},
		{Intent{State: "scrolled", Name: "material", Value: "glass"}, []Intent{{Width: "medium", Name: "background", Value: "paper/80"}, {State: "scrolled", Name: "background", Value: "paper"}}, []int{1}},
		{Intent{State: "hover", Name: "material", Value: "glass"}, []Intent{{State: "focus", Name: "background", Value: "paper/80"}}, nil},
	}
	for _, c := range cases {
		if got := reads(c.material, c.backgrounds); fmt.Sprint(got) != fmt.Sprint(c.want) {
			t.Errorf("%+v with %+v: reads %v, want %v", c.material, c.backgrounds, got, c.want)
		}
	}
}
