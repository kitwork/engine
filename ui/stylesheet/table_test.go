package stylesheet

import (
	"errors"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// examplePalette is the palette of the quancaphe conformance fixture's site root (the theme of
// engine/conformance/testdata/ui/quancaphe/cases.json); its /quan-ly branch writes brand #2563eb.
func examplePalette(t *testing.T, chain ...map[string]Color) *Palette {
	t.Helper()
	root := map[string]Color{
		"brand":   {Light: "#c8742c"},
		"canvas":  {Light: "#f6f3ee", Dark: "#141110"},
		"panel":   {Light: "#efe9e1", Dark: "#1a1613"},
		"paper":   {Light: "#ffffff", Dark: "#1f1a17"},
		"ink":     {Light: "#2b211b", Dark: "#f3ece6"},
		"line":    {Light: "#e6ded5", Dark: "#3a302a"},
		"success": {Light: "#2f9e63"},
		"warning": {Light: "#c98a1e"},
		"danger":  {Light: "#e5484d"},
	}
	p, err := NewPalette(false, append([]map[string]Color{root}, chain...)...)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func classes(t *testing.T, table *Table, r *Rules, intents ...Intent) string {
	t.Helper()
	var out []string
	for _, in := range intents {
		class, err := table.Class(r, in)
		if err != nil {
			t.Fatalf("%+v: %v", in, err)
		}
		if class != "" {
			out = append(out, class)
		}
	}
	return strings.Join(out, " ")
}

// The header of the synthesis, inside <page container="wide">:
// <header flex="row" align="center" space="between" gap="4" padding="4" medium:padding="6">
// One class per written attribute, in the source's words, and its page.css exactly.
func TestHeaderExample(t *testing.T) {
	table := NewTable(examplePalette(t))
	var r Rules
	got := classes(t, table, &r,
		Intent{Tag: "header", Name: "flex", Value: "row"},
		Intent{Tag: "header", Name: "align", Value: "center"},
		Intent{Tag: "header", Name: "space", Value: "between"},
		Intent{Tag: "header", Name: "gap", Value: "4"},
		Intent{Tag: "header", Name: "padding", Value: "4"},
		Intent{Tag: "header", Width: "medium", Name: "padding", Value: "6"},
		Intent{Tag: "header", Name: "container", Value: "wide"}, // passed down from <page>
	)
	if want := "flex-row align-center space-between gap-4 padding-4 medium:padding-6 container-wide"; got != want {
		t.Fatalf("class\n got %q\nwant %q", got, want)
	}
	want := `@layer reboot,elements,page,show;
@layer page{
.align-center{--kitwork-vertical:center;--kitwork-horizontal:center}
.space-between{--kitwork-space:space-between}
.gap-4{gap:1rem}
.container-wide{inline-size:100%;max-inline-size:1280px;margin-inline:auto}
.padding-4{padding:1rem}
@media (min-width:768px){
.medium\:padding-6{padding:1.5rem}
}
}
`
	if css := string(r.CSS()); css != want {
		t.Fatalf("page.css\n got:\n%s\nwant:\n%s", css, want)
	}
	if f := r.File(); f.Role != "page" || !strings.HasPrefix(f.Name, "page.") || len(f.Name) != len("page.")+16 {
		t.Fatalf("file %q %q", f.Role, f.Name)
	}
}

// specAttributes are the presentation attributes of KITWORK_UI §7 and §8, with K's additions: the
// padding axes and sides, raw sizes, aspect-ratio and fit; and M's material.
var specAttributes = []string{
	"flex", "wrap", "align", "space", "gap", "columns", "span", "place", "pin", "container",
	"width", "min-width", "max-width", "height", "min-height", "max-height", "aspect-ratio", "fit",
	"padding", "padding-x", "padding-y", "padding-top", "padding-bottom", "padding-left", "padding-right",
	"padding-start", "padding-end", "scroll", "lines", "background", "material", "border", "radius",
	"radius-top", "radius-bottom", "radius-left", "radius-right", "radius-start", "radius-end",
	"radius-top-left", "radius-top-right", "radius-bottom-left", "radius-bottom-right",
	"radius-top-start", "radius-top-end", "radius-bottom-start", "radius-bottom-end",
	"shadow", "opacity", "scale", "rotate",
	"font", "size", "weight", "italic", "case", "underline", "strikethrough", "color", "thickness", "show",
}

// tailwindWord matches the Tailwind dialect a .ui class never speaks: its width prefixes, its
// abbreviations, its arbitrary values.
var tailwindWord = regexp.MustCompile(`(^|:)(sm|md|lg|xl|2xl):|(^|:)(p|px|py|pt|pb|pl|pr|m|mx|my|w|h|bg|text|justify|items|rounded|max-w|max-h|min-w|min-h|grid-cols|col-span|line-clamp|overflow|leading|tracking|inset|top|left|right|bottom|z|translate)-|\[`)

// valuesToTry lists values of an attribute: every word or step a value from data may take, and the
// forms only a written value takes (units, ratios, lighter and stronger colours, alphas, fixed colours).
func valuesToTry(table *Table, name string) []string {
	a := table.attribute(name)
	var out []string
	if a.domain != nil {
		out = append(out, a.domain(table, Intent{Name: name})...)
	}
	switch name {
	case "width", "min-width", "max-width", "height", "min-height", "max-height":
		out = append(out, "640px", "50%", "68ch", "12.5rem", "2em", "100vw", "100vh", "100dvh", "0px")
	case "aspect-ratio":
		out = append(out, "1/1", "5/4", "1200/630")
	case "color", "background", "border":
		out = append(out, "ink-30", "paper/80", "ink-30/50", "black/50", "white", "brand+15", "success-85", "brand+20/40", "brand.on-20")
	case "opacity":
		out = append(out, "37", "100")
	case "scale":
		out = append(out, "200", "33")
	case "rotate":
		out = append(out, "360", "15")
	case "show":
		out = append(out, "true", "false")
	}
	return out
}

func (t *Table) attribute(name string) attribute { return table[attributeRank[name]] }

// rulesOf counts the rules of a class in page.css: lines whose selector is the class, alone or with
// its state or scope.
func rulesOf(css, class string) int {
	selector := "." + escape(class)
	n := 0
	for _, line := range strings.Split(css, "\n") {
		if strings.HasPrefix(line, selector+"{") || strings.HasPrefix(line, selector+":where(") {
			n++
		}
	}
	return n
}

// Every attribute of the spec, every value: one class, named as the source writes it, and one rule
// behind it — unprefixed and under a width. The exceptions are the engine's own: flex without a prefix
// is a direction class reboot.css styles; strikethrough without a prefix is the <s> element.
func TestEveryAttributeValueMakesOneClassAndOneRule(t *testing.T) {
	table := NewTable(examplePalette(t))
	got := Attributes()
	sort.Strings(got)
	want := append([]string(nil), specAttributes...)
	sort.Strings(want)
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("the table's attributes\n got %v\nwant %v", got, want)
	}
	tried := 0
	for _, name := range specAttributes {
		values := valuesToTry(table, name)
		if len(values) == 0 {
			t.Fatalf("no value to try for %s", name)
		}
		for _, value := range values {
			for _, width := range []string{"", "medium"} {
				var r Rules
				in := Intent{Width: width, Name: name, Value: value}
				class, err := table.Class(&r, in)
				if err != nil {
					t.Errorf("%+v: %v", in, err)
					continue
				}
				tried++
				written := name + "-" + value
				if width != "" {
					written = width + ":" + written
				}
				css := string(r.CSS())
				switch {
				case width == "" && name == "flex":
					if class != written || css != "" || !contains(ElementWords(), class) {
						t.Errorf("flex=%q: %q is a direction class of reboot.css, no page rule: %q", value, class, css)
					}
					continue
				case width == "" && name == "strikethrough":
					if class != "" || css != "" {
						t.Errorf("strikethrough=%q without a prefix is the <s> element: got class %q, css %q", value, class, css)
					}
					continue
				}
				if class != written {
					t.Errorf("%+v: class %q, want %q", in, class, written)
				}
				if tailwindWord.MatchString(class) {
					t.Errorf("%q speaks Tailwind", class)
				}
				if !r.Has(class) || strings.Join(r.Classes(), " ") != class {
					t.Errorf("%q: recorded %v", class, r.Classes())
				}
				if n := rulesOf(css, class); n != 1 {
					t.Errorf("%q has %d rules, want 1:\n%s", class, n, css)
				}
				if width != "" && !strings.Contains(css, "@media (min-width:768px){\n."+escape(class)+"{") {
					t.Errorf("%q is not under min-width:768px:\n%s", class, css)
				}
			}
		}
	}
	if tried < 400 {
		t.Fatalf("only %d intents tried", tried)
	}
	// a state on scale or rotate adds the transition, outside the state, so the change animates back
	for _, name := range []string{"scale", "rotate"} {
		var r Rules
		class, err := table.Class(&r, Intent{State: "pressed", Name: name, Value: "90"})
		if err != nil {
			t.Fatal(err)
		}
		css := string(r.CSS())
		if !strings.Contains(css, "\n."+escape(class)+"{transition:scale .15s,rotate .15s}\n") || rulesOf(css, class) != 2 {
			t.Errorf("%s with a state:\n%s", name, css)
		}
	}
}

func contains(list []string, word string) bool { return indexOf(list, word) >= 0 }

// The fixed order: within a layer the unprefixed rules, then the widths ascending; then each state
// (open < active < hover < focus < pressed < disabled) with its widths ascending; within one condition
// the table's order (a side of padding after the axis after padding). Every page rule weighs one class,
// so this order is the whole precedence.
func TestRulesKeepTheFixedOrder(t *testing.T) {
	table := NewTable(examplePalette(t))
	var r Rules
	written := []Intent{ // in no particular order
		{State: "disabled", Name: "opacity", Value: "50"},
		{Name: "padding-top", Value: "4"},
		{Width: "large", State: "hover", Name: "background", Value: "brand"},
		{State: "hover", Name: "opacity", Value: "80"},
		{Width: "medium", Name: "padding", Value: "6"},
		{State: "focus", Name: "background", Value: "paper"},
		{State: "active", Name: "color", Value: "ink"},
		{Name: "padding-y", Value: "2"},
		{State: "pressed", Name: "scale", Value: "95"},
		{Width: "small", Name: "gap", Value: "2"},
		{State: "open", Name: "rotate", Value: "180"},
		{Name: "padding", Value: "4"},
		{State: "hover", Name: "background", Value: "panel"},
		{Name: "show", Value: "false"},
		{Width: "medium", Name: "show", Value: "true"},
	}
	for _, in := range written {
		if _, err := table.Class(&r, in); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{
		`.padding-4{`,
		`.padding-y-2{`,
		`.padding-top-4{`,
		`.pressed\:scale-95{`, // the transitions hold under no condition
		`.open\:rotate-180{`,
		`.small\:gap-2{`,
		`.medium\:padding-6{`,
		`.open\:rotate-180:where(:is(`,
		`.active\:color-ink:where(`,
		`.hover\:background-panel:where(:hover)`,
		`.hover\:opacity-80:where(:hover)`,
		`.large\+hover\:background-brand:where(:hover)`,
		`.focus\:background-paper:where(:focus-visible)`,
		`.pressed\:scale-95:where(:active)`,
		`.disabled\:opacity-50:where(:disabled`,
		`.show-false{`,
		`.medium\:show-true{`,
	}
	var got []string
	for _, line := range strings.Split(string(r.CSS()), "\n") {
		if strings.HasPrefix(line, ".") {
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
	css := string(r.CSS())
	if strings.Index(css, "@layer show{") < strings.Index(css, ".disabled\\:opacity-50") {
		t.Fatal("the show layer comes after the page layer")
	}
	// Control: sorted as text, these classes would put a narrower width after a wider one, disabled
	// before hover, open after active and padding-top before padding-y, so the order above is the
	// writer's and not an accident of the names.
	names := []string{"small:gap-2", "medium:padding-6", "disabled:opacity-50", "hover:opacity-80", "open:rotate-180", "active:color-ink", "padding-top-4", "padding-y-2"}
	sort.Strings(names)
	before := func(a, b string) bool { return indexOf(names, a) < indexOf(names, b) }
	if !before("medium:padding-6", "small:gap-2") || !before("disabled:opacity-50", "hover:opacity-80") ||
		!before("active:color-ink", "open:rotate-180") || !before("padding-top-4", "padding-y-2") {
		t.Fatalf("control: alphabetical order no longer differs from the fixed order: %v", names)
	}
}

// hover holds only where a pointer hovers; open follows the nearest element that opens; active is
// aria-current; disabled covers aria-disabled. A width and a state together are one block.
func TestStateSelectors(t *testing.T) {
	table := NewTable(nil)
	want := map[string]string{
		"open":     `.open\:color-ink:where(:is(details[open],[aria-expanded=true]) *):not(:where(details:not([open]),[aria-expanded=false]) *){color:var(--ink)}`,
		"active":   `.active\:color-ink:where([aria-current]:not([aria-current=false])){color:var(--ink)}`,
		"scrolled": `.scrolled\:color-ink:where(:root[data-kitwork-scrolled] *){color:var(--ink)}`,
		"hover":    "@media (hover:hover){\n" + `.hover\:color-ink:where(:hover){color:var(--ink)}`,
		"focus":    `.focus\:color-ink:where(:focus-visible){color:var(--ink)}`,
		"pressed":  `.pressed\:color-ink:where(:active){color:var(--ink)}`,
		"disabled": `.disabled\:color-ink:where(:disabled,[aria-disabled=true]){color:var(--ink)}`,
	}
	for state, rule := range want {
		var r Rules
		if _, err := table.Class(&r, Intent{State: state, Name: "color", Value: "ink"}); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(r.CSS()), rule) {
			t.Errorf("%s:\n%s\nwant %s", state, r.CSS(), rule)
		}
	}
	var r Rules
	class, err := table.Class(&r, Intent{Width: "large", State: "hover,focus", Name: "background", Value: "brand+15"})
	if err != nil {
		t.Fatal(err)
	}
	if class != "large+hover,focus:background-brand+15" {
		t.Fatalf("class %q", class)
	}
	css := string(r.CSS())
	for _, rule := range []string{
		"@media (min-width:1024px) and (hover:hover){\n" + `.large\+hover\,focus\:background-brand\+15:where(:hover){background-color:var(--kitwork-strong-15-brand)}`,
		"@media (min-width:1024px){\n" + `.large\+hover\,focus\:background-brand\+15:where(:focus-visible){background-color:var(--kitwork-strong-15-brand)}`,
	} {
		if !strings.Contains(css, rule) {
			t.Errorf("one class in each state's slot:\n%s\nwant %s", css, rule)
		}
	}
	// , between widths: the narrowest, because a width holds from where it starts
	class, err = table.Class(&r, Intent{Width: "wide,large", Name: "columns", Value: "3"})
	if err != nil || class != "large:columns-3" {
		t.Fatalf("large,wide: %q %v", class, err)
	}
}

// Only the CSS escapes: the six characters a .ui class keeps from its source and CSS reads as syntax.
// Unescaped by CSS's own rule (a backslash and a character that is not hex is that character), the
// selector gives back the class exactly.
func TestEscapeRoundTrip(t *testing.T) {
	for _, class := range []string{
		"medium:padding-6", "large+hover:background-brand+15", "hover,focus:color-ink/80",
		"width-50%", "aspect-ratio-16/9", "gap-0.5", "color-ink-30/50", "large,wide+open:rotate-180",
		"padding-x-4", "max-width-68ch", "color-brand+15", "medium+hover:background-brand+15", "color-brand.on-20",
		"background-cirrus+20", "material-glass/60",
	} {
		escaped := escape(class)
		for i := 0; i < len(escaped); i++ {
			if strings.IndexByte(":+,./%", escaped[i]) >= 0 && (i == 0 || escaped[i-1] != '\\') {
				t.Errorf("%q: %q left unescaped at %d", escaped, escaped[i], i)
			}
		}
		if back := unescapeCSS(escaped); back != class {
			t.Errorf("round trip %q → %q → %q", class, escaped, back)
		}
	}
	if escape("padding-x-4") != "padding-x-4" {
		t.Error("letters, digits and - are not escaped")
	}
	var r Rules
	class, _ := NewTable(nil).Class(&r, Intent{Width: "small", State: "hover", Name: "width", Value: "50%"})
	if !strings.Contains(string(r.CSS()), "."+`small\+hover\:width-50\%`+":where(:hover){inline-size:50%}") || class != "small+hover:width-50%" {
		t.Fatalf("%q:\n%s", class, r.CSS())
	}
}

// unescapeCSS reads CSS's escapes: a backslash and a hex digit start a code point, any other character
// after a backslash is itself.
func unescapeCSS(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			if strings.IndexByte("0123456789abcdefABCDEF", s[i+1]) >= 0 {
				return "<hex escape: not expected>"
			}
			i++
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// A value the table refuses names the word as written and the words the attribute takes; where there
// is one, the suggestion is the word to write instead (the new name of an old word, the nearest step).
func TestValidationNamesTheWordAndTheValidOnes(t *testing.T) {
	table := NewTable(examplePalette(t))
	cases := []struct {
		in         Intent
		contains   []string
		suggestion string
	}{
		{Intent{Name: "color", Value: "slate-500"}, []string{`"slate"`, "brand", "success.on", "white, black", "ink-30", "brand+15", "ink/80"}, ""},
		{Intent{Name: "color", Value: "blue-500"}, []string{`"blue-500"`, "Tailwind", "accent-30"}, ""},
		{Intent{Name: "background", Value: "pinkish"}, []string{`"pinkish"`, "canvas", "paper"}, ""},
		// deeppink is a named colour now; written with a hyphen it is told its name
		{Intent{Name: "background", Value: "deep-pink"}, []string{`background="deep-pink"`, "tên là deeppink (#ff1493)"}, "deeppink"},
		{Intent{Name: "background", Value: "transparent"}, []string{"transparent", "paper/0"}, ""},
		{Intent{Name: "color", Value: "white.80"}, []string{"white.80", "cố định", "white/80"}, "white/80"},
		{Intent{Name: "color", Value: "white-20"}, []string{"white-20", "cố định", "white/N"}, ""},
		{Intent{Name: "background", Value: "white:50"}, []string{"white:50", "dấu :", "white/50"}, "white/50"},
		{Intent{Name: "color", Value: "ink.150"}, []string{"ink.150", "0 đến 100", "ink-N"}, ""},
		{Intent{Name: "color", Value: "ink-150"}, []string{"ink-150", "0 đến 100"}, ""},
		{Intent{Name: "border", Value: "brand.deep"}, []string{"border nhận none hoặc một màu", "brand+15"}, "brand+15"},
		{Intent{Name: "color", Value: "ink/x"}, []string{"ink/x", "0 đến 100"}, ""},
		{Intent{Name: "border", Value: "thick"}, []string{"none", `"thick"`, "brand"}, ""},
		{Intent{Name: "radius", Value: "md"}, []string{`radius="md"`, `radius="small"`, "none, small, medium, large, full"}, "small"},
		{Intent{Name: "radius", Value: "lg"}, []string{`radius="medium"`}, "medium"},
		{Intent{Name: "radius", Value: "xl"}, []string{`radius="large"`}, "large"},
		{Intent{Name: "radius", Value: "round"}, []string{`"round"`, "none, small, medium, large hoặc full"}, ""},
		{Intent{Name: "shadow", Value: "lg"}, []string{`shadow="lg"`, `shadow="large"`, "small, medium, large"}, "large"},
		{Intent{Name: "shadow", Value: "md"}, []string{`shadow="medium"`}, "medium"},
		{Intent{Name: "padding", Value: "13px"}, []string{`padding="13px"`, `padding="3"`, ".75rem", "0.5, 1"}, "3"},
		{Intent{Name: "gap", Value: "13"}, []string{`gap="13"`, `gap="12"`}, "12"},
		{Intent{Name: "padding-x", Value: "2rem"}, []string{`padding-x="2rem"`, `padding-x="8"`}, "8"},
		{Intent{Name: "padding", Value: "50%"}, []string{`padding="50%"`, "không theo %"}, ""},
		{Intent{Name: "gap", Value: "calc(1rem + 2px)"}, []string{"calc()", "0.5"}, ""},
		{Intent{Name: "width", Value: "2xl"}, []string{"2xl", "Tailwind", "small (640px)", "px, rem, em, %, ch, vw, vh, dvh"}, ""},
		{Intent{Name: "max-width", Value: "full"}, []string{"full", "bề ngang", "100%"}, "100%"},
		{Intent{Name: "width", Value: "stretch"}, []string{`"stretch"`, "fit (ôm nội dung) hoặc full"}, ""},
		{Intent{Tag: "text", Name: "wrap", Value: "tight"}, []string{`"tight"`, "true, false, balance hoặc pretty"}, ""},
		{Intent{Tag: "box", Name: "wrap", Value: "balance"}, []string{`"balance"`, "chỉ có nghĩa trên chữ"}, ""},
		{Intent{Name: "min-height", Value: "full"}, []string{"full", "chiều cao", "100%"}, "100%"},
		{Intent{Name: "width", Value: "screen"}, []string{"screen", "cửa sổ", "100vw"}, "100vw"},
		{Intent{Name: "min-height", Value: "screen"}, []string{"screen", "cửa sổ", "100dvh"}, "100dvh"},
		{Intent{Name: "container", Value: "full"}, []string{"full", "Tailwind", "không viết container"}, ""},
		{Intent{Name: "max-width", Value: "calc(100% - 2rem)"}, []string{"calc()", "var()"}, ""},
		{Intent{Name: "max-width", Value: "mini"}, []string{"mini", "cỡ chữ"}, ""},
		{Intent{Name: "height", Value: "small"}, []string{"small", "chỉ cho bề ngang"}, ""},
		{Intent{Name: "min-height", Value: "13pt"}, []string{`"13pt"`, "dvh"}, ""},
		{Intent{Name: "container", Value: "7xl"}, []string{"7xl", "Tailwind", "huge (1536px)"}, ""},
		{Intent{Tag: "box", Name: "fit", Value: "cover"}, []string{"fit", "image", "box"}, ""},
		{Intent{Tag: "image", Name: "fit", Value: "fill"}, []string{`"fill"`, "cover", "contain"}, ""},
		{Intent{Name: "aspect-ratio", Value: "16:9"}, []string{`"16:9"`, "16/9", "square", "auto"}, "16/9"},
		{Intent{Name: "aspect-ratio", Value: "1.5"}, []string{`"1.5"`, "16/9"}, ""},
		{Intent{Name: "size", Value: "sm"}, []string{"sm", "small", "mini, small, medium, large, huge"}, "small"},
		{Intent{Name: "size", Value: "3xl"}, []string{"3xl", "heading"}, ""},
		{Intent{Name: "weight", Value: "700"}, []string{`weight="700"`, `weight="bold"`, "semibold"}, "bold"},
		{Intent{Name: "weight", Value: "fat"}, []string{`"fat"`, "thin", "black"}, ""},
		{Intent{Name: "font", Value: "mono"}, []string{"mono", "code"}, "code"},
		{Intent{Name: "space", Value: "4"}, []string{"between", `gap="4"`}, `gap="4"`},
		{Intent{Name: "align", Value: "between"}, []string{"between", `space="between"`}, `space="between"`},
		{Intent{Name: "align", Value: "left"}, []string{`"left"`, "top-start", "bottom-end"}, ""},
		{Intent{Tag: "text", Name: "align", Value: "top"}, []string{`"top"`, "start, center hoặc end"}, ""},
		{Intent{Name: "flex", Value: "grid"}, []string{`"grid"`, "row", "column"}, ""},
		{Intent{Name: "place", Value: "before"}, []string{"before", "chưa được hỗ trợ"}, ""},
		{Intent{Name: "place", Value: "middle"}, []string{`"middle"`, "below-end", "fill"}, ""},
		{Intent{Name: "opacity", Value: "150"}, []string{`"150"`, "0 đến 100"}, ""},
		{Intent{Name: "scale", Value: "0.95"}, []string{`"0.95"`, "95"}, ""},
		{Intent{Name: "rotate", Value: "-90"}, []string{`"-90"`, "0 đến 360"}, ""},
		{Intent{Name: "columns", Value: "13"}, []string{`"13"`, "1 đến 12"}, ""},
		{Intent{Name: "lines", Value: "0"}, []string{`"0"`, "1 đến 10"}, ""},
		{Intent{Name: "thickness", Value: "9"}, []string{`"9"`, "1 đến 8"}, ""},
		{Intent{Name: "scroll", Value: "both"}, []string{`"both"`, "x hoặc y"}, ""},
		{Intent{Name: "italic", Value: "yes"}, []string{`"yes"`, "true hoặc false"}, ""},
		{Intent{Name: "case", Value: "title"}, []string{`"title"`, "upper", "lower"}, ""},
		{Intent{Width: "tablet", Name: "gap", Value: "4"}, []string{"tablet", "small, medium, large, wide, huge"}, ""},
		{Intent{State: "checked", Name: "gap", Value: "4"}, []string{"checked", "open, active, scrolled, hover, focus, pressed, disabled"}, ""},
		{Intent{State: "hover,hover", Name: "gap", Value: "4"}, []string{"hover", "hai lần"}, ""},
		{Intent{State: "hover", Name: "show", Value: "true"}, []string{"hover", "trạng thái", "medium:show"}, ""},
		{Intent{Name: "show", Value: "unread > 0"}, []string{`"unread > 0"`, "true hoặc false"}, ""},
		{Intent{Name: "width", Value: "auto"}, []string{`"auto"`, "theo nội dung", "68ch"}, ""},
		{Intent{Name: "cursor", Value: "pointer"}, []string{`cursor="pointer"`, "không phải thuộc tính trình bày"}, ""},
		{Intent{Name: "display", Value: "flex"}, []string{`display="flex"`, "flex", "grid", "show"}, ""},
		{Intent{Name: "pl", Value: "4"}, []string{`pl="4"`, "Tailwind", "padding-left"}, "padding-left"},
		{Intent{Name: "padding-inline", Value: "4"}, []string{"padding-x"}, "padding-x"},
		{Intent{Name: "margin-top", Value: "4"}, []string{`margin-top="4"`, "gap", "padding"}, ""},
		{Intent{Name: "position", Value: "absolute"}, []string{"place"}, "place"},
		{Intent{Name: "z-index", Value: "50"}, []string{"thứ tự viết"}, ""},
		{Intent{Name: "justify", Value: "between"}, []string{"align", "space"}, ""},
		{Intent{Name: "font-size", Value: "14px"}, []string{`font-size="14px"`, "size", "mini, small"}, "size"},
		{Intent{Name: "border-radius", Value: "8px"}, []string{"radius", "none, small"}, "radius"},
		{Intent{Name: "text-align", Value: "center"}, []string{"align", "start, center, end"}, "align"},
		{Intent{Name: "overflow", Value: "auto"}, []string{"scroll", "x, y"}, "scroll"},
	}
	for _, c := range cases {
		var r Rules
		class, err := table.Class(&r, c.in)
		var p *Problem
		if !errors.As(err, &p) {
			t.Errorf("%+v: got class %q, error %v; want a Problem", c.in, class, err)
			continue
		}
		if p.Name != c.in.Name || p.Value != strings.TrimSpace(c.in.Value) {
			t.Errorf("%+v: the problem names %q=%q", c.in, p.Name, p.Value)
		}
		if !strings.Contains(p.Message, c.in.Value) && !strings.Contains(p.Message, c.in.Width) && !strings.Contains(p.Message, c.in.State) {
			t.Errorf("%+v: the message does not name the word: %s", c.in, p.Message)
		}
		for _, want := range c.contains {
			if !strings.Contains(p.Message, want) {
				t.Errorf("%+v: message lacks %q:\n%s", c.in, want, p.Message)
			}
		}
		if p.Suggestion != c.suggestion {
			t.Errorf("%+v: suggestion %q, want %q", c.in, p.Suggestion, c.suggestion)
		}
		if class != "" || len(r.Classes()) != 0 {
			t.Errorf("%+v: a refused value recorded %q %v", c.in, class, r.Classes())
		}
	}
}

// A colour is a token of the route's own chain: the table of /quan-ly knows the token its branch
// declares, the site's does not.
func TestColoursAreTheChainsTokens(t *testing.T) {
	branch := map[string]Color{"brand": {Light: "#2563eb"}, "chart": {Light: "#7c3aed"}}
	site, quanLy := NewTable(examplePalette(t)), NewTable(examplePalette(t, branch))
	if _, err := site.Class(nil, Intent{Name: "background", Value: "chart.on"}); err == nil {
		t.Fatal("the site knows a token only the branch declares")
	}
	var r Rules
	if _, err := quanLy.Class(&r, Intent{Name: "background", Value: "chart.on"}); err != nil {
		t.Fatalf("the branch's grown rung: %v", err)
	}
	if _, err := quanLy.Class(&r, Intent{Name: "background", Value: "chart-85"}); err != nil {
		t.Fatalf("the branch's token made lighter: %v", err)
	}
	for _, value := range []string{"ink-30", "paper/80", "ink-30/50", "black/50", "white"} {
		if _, err := site.Class(&r, Intent{Name: "color", Value: value}); err != nil {
			t.Errorf("%s: %v", value, err)
		}
	}
	css := string(r.CSS())
	for _, want := range []string{
		`.background-chart\.on{background-color:var(--chart-on)}`,
		`.background-chart-85{background-color:color-mix(in oklab,var(--chart) 15%,var(--canvas))}`,
		`.color-ink-30{color:color-mix(in oklab,var(--ink) 70%,var(--canvas))}`,
		`.color-paper\/80{color:color-mix(in srgb,var(--paper) 80%,transparent)}`,
		`.color-ink-30\/50{color:color-mix(in srgb,color-mix(in oklab,var(--ink) 70%,var(--canvas)) 50%,transparent)}`,
		`.color-black\/50{color:color-mix(in srgb,#000 50%,transparent)}`,
		`.color-white{color:#fff}`,
	} {
		if !strings.Contains(css, want) {
			t.Errorf("lacks %s\n%s", want, css)
		}
	}
}

// A badge's colour sets the tint its own fill reads, its accent made lighter (X-85), and its text the
// engine colour kitwork-badge-X (engine_colour.go): the accent made stronger as far as it must to read
// 4.5:1 on the fill in each mode, from 15 where stronger darkens (light: the step it always had, so a
// pale accent takes more, warning +55 on Kit Design) and from 0 where it lightens (dark: the accent
// itself on these palettes, its hue kept; warning+55 was #ffffff there). A colour with no family sits on
// the panel. A written background wins (it is the page layer, the fill is elements). CONTROL: the step
// before n falls short, and a fixed +15 would leave warning unreadable in light.
func TestBadgeColour(t *testing.T) {
	// the light steps the badges had before the per-mode rule (13/10/2026), on each palette
	lightSteps := map[string]map[string]int{
		"Kit Design":        {"brand": 15, "success": 35, "warning": 55, "danger": 25, "info": 20},
		"kitwork":           {"brand": 25, "success": 35, "warning": 55, "danger": 30, "info": 30},
		"quancaphe":         {"brand": 40, "success": 40, "warning": 60, "danger": 30, "info": 20},
		"quancaphe/quan-ly": {"brand": 15, "success": 40, "warning": 60, "danger": 30, "info": 20},
	}
	for _, pp := range operatorPalettes(t) {
		table := NewTable(pp.palette)
		for _, accent := range []string{"brand", "success", "warning", "danger", "info"} {
			if !pp.palette.Has(accent) {
				t.Fatalf("%s has no %s", pp.name, accent)
			}
			fill, text, family := table.BadgeTint(accent)
			if !family || fill != accent+"-85" || text != BadgeColour(accent) {
				t.Errorf("%s %s badge: %s on %s (family %v), want %s on %s-85", pp.name, accent, text, fill, family, BadgeColour(accent), accent)
				continue
			}
			for _, m := range pp.palette.Modes() {
				value, _ := pp.palette.Engine(text, m)
				fg, _ := pp.palette.Paint(text, m)
				bg, _ := pp.palette.Paint(fill, m)
				if r := Ratio(fg.Painted(), bg.Painted()); r < NormalTextRatio {
					t.Errorf("%s %s badge in %s: %s on %s is %.2f:1", pp.name, accent, m.Word(), value, fill, r)
				}
				c, _ := SplitColour(value)
				floor := pp.palette.floorIn(badgeRole(accent), m)
				if c.Amount > floor && pp.palette.readsIn(spell(accent, c.Amount-5), []string{fill}, NormalTextRatio, m) {
					t.Errorf("%s %s badge in %s: %s reads already, %s is not the smallest", pp.name, accent, m.Word(), spell(accent, c.Amount-5), value)
				}
			}
			light, _ := pp.palette.Engine(text, LightMode)
			dark, _ := pp.palette.Engine(text, DarkMode)
			if want := spell(accent, lightSteps[pp.name][accent]); light != want || dark != accent {
				t.Errorf("%s %s badge: %s in light and %s in dark, want %s and %s", pp.name, accent, light, dark, want, accent)
			}
		}
	}
	kit, _ := NewPalette(true)
	if _, text, _ := NewTable(kit).BadgeTint("warning"); hexes(t, kit, text) != "#8c5c00 / #daa965" {
		t.Errorf("warning's badge text on Kit Design is %s, want #8c5c00 / #daa965", hexes(t, kit, text))
	}
	if kit.readsIn("warning+15", []string{"warning-85"}, NormalTextRatio, LightMode) {
		t.Error("CONTROL: warning+15 reads on warning-85 in light: a fixed step would do")
	}

	table := NewTable(examplePalette(t))
	var r Rules
	classes(t, table, &r,
		Intent{Tag: "badge", Name: "color", Value: "success"},
		Intent{Tag: "badge", Name: "color", Value: "ink-30"},
		Intent{Tag: "text", Name: "color", Value: "danger"},
	)
	css := string(r.CSS())
	pair, _ := table.Palette().enginePair(BadgeColour("success"))
	for _, want := range []string{
		":root{--kitwork-badge-success:" + pair.light + "}",
		"@media screen{:root[data-theme=dark]{--kitwork-badge-success:" + pair.dark + "}}",
		`.color-success{color:var(--success)}` + "\n" + `.color-success:where(.badge){--kitwork-tint:color-mix(in oklab,var(--success) 15%,var(--canvas));color:var(--kitwork-badge-success)}`,
		`.color-ink-30:where(.badge){--kitwork-tint:var(--panel)}`,
		`.color-danger{color:var(--danger)}`,
	} {
		if !strings.Contains(css, want) {
			t.Errorf("lacks %s\n%s", want, css)
		}
	}
	if pair.light == pair.dark || pair.light != hexes(t, table.Palette(), "success+40")[:7] {
		t.Errorf("--kitwork-badge-success is %s / %s, want success+40 in light and another colour in dark", pair.light, pair.dark)
	}
	if strings.Contains(css, ".color-danger:where(.badge)") || strings.Contains(css, "--kitwork-strong-") {
		t.Errorf("a text's colour got a badge's tint, or a badge an author's stronger colour:\n%s", css)
	}
}

// align on text aligns its lines, on any element that is not a container; the class's own rule is the
// same on every tag, so one class serves a box and a text.
func TestAlignOnText(t *testing.T) {
	table := NewTable(nil)
	var boxRules, textRules Rules
	box, _ := table.Class(&boxRules, Intent{Tag: "box", Name: "align", Value: "end"})
	text, _ := table.Class(&textRules, Intent{Tag: "text", Name: "align", Value: "end"})
	if box != text || box != "align-end" {
		t.Fatalf("%q %q", box, text)
	}
	own := ".align-end{--kitwork-vertical:center;--kitwork-horizontal:flex-end}"
	if !strings.Contains(string(boxRules.CSS()), own) || !strings.Contains(string(textRules.CSS()), own) {
		t.Fatalf("the own rule differs:\n%s\n%s", boxRules.CSS(), textRules.CSS())
	}
	if !strings.Contains(string(textRules.CSS()), `.align-end:where(:not(.flex-row,.flex-column,.grid)){text-align:end}`) ||
		strings.Contains(string(boxRules.CSS()), "text-align") {
		t.Fatalf("text-align:\n%s\n%s", boxRules.CSS(), textRules.CSS())
	}
}

// A value from data becomes a class only if it is in the attribute's domain, compiled ahead into the
// page: for a colour every declared token (not a grown rung, not an operator, not an alpha).
func TestDomain(t *testing.T) {
	p := examplePalette(t)
	table := NewTable(p)
	var r Rules
	domain, err := table.Domain(&r, Intent{Tag: "badge", Name: "color"})
	if err != nil {
		t.Fatal(err)
	}
	declared := p.Declared()
	if len(domain) != len(declared) || len(declared) != 10 {
		t.Fatalf("domain %v, declared %v", domain, declared)
	}
	css := string(r.CSS())
	for _, token := range declared {
		class := domain[token]
		if class != "color-"+token || rulesOf(css, class) != 2 {
			t.Errorf("%s → %q, %d rules", token, class, rulesOf(css, class))
		}
	}
	for _, refused := range []string{"brand.on", "ink-30", "brand+15", "red-500", "transparent", "white"} {
		if _, ok := domain[refused]; ok {
			t.Errorf("%s from data got a class", refused)
		}
	}
	if !strings.Contains(css, `.color-warning:where(.badge){--kitwork-tint:color-mix(in oklab,var(--warning) 15%,var(--canvas));color:var(--kitwork-badge-warning)}`) ||
		!regexp.MustCompile(`:root\{[^}]*--kitwork-badge-warning:#[0-9a-f]{6}`).MatchString(css) {
		t.Errorf("the badge tint of a data value:\n%s", css)
	}
	if _, err := table.Domain(&r, Intent{Width: "medium", Name: "color"}); err == nil || !strings.Contains(err.Error(), "{{ }}") {
		t.Errorf("a prefix with {{ }}: %v", err)
	}
	if _, err := table.Domain(&r, Intent{Name: "show"}); err == nil {
		t.Error("show from data is a live expression, not a class")
	}
	sizes, _ := table.Domain(&r, Intent{Name: "size"})
	if len(sizes) != 5 || sizes["small"] != "size-small" {
		t.Errorf("size domain %v", sizes)
	}
	strike, _ := table.Domain(&r, Intent{Name: "strikethrough"})
	if strike["true"] != "strikethrough-true" || !r.Has("strikethrough-true") {
		t.Errorf("from data the element cannot become <s>: it needs the class (%v)", strike)
	}
	text, _ := table.Domain(&r, Intent{Tag: "text", Name: "align"})
	if len(text) != 3 {
		t.Errorf("align of text from data: %v", text)
	}
	// A tag that takes no value of the attribute is refused as a written value is, not given an empty
	// domain; the image takes them all.
	var refused *Problem
	if fit, err := table.Domain(&r, Intent{Tag: "box", Name: "fit"}); !errors.As(err, &refused) || !strings.Contains(refused.Message, "image") || fit != nil {
		t.Errorf("fit from data on a box: %v %v", fit, err)
	}
	if fit, err := table.Domain(&r, Intent{Tag: "image", Name: "fit"}); err != nil || fit["contain"] != "fit-contain" || len(fit) != 2 {
		t.Errorf("fit from data on an image: %v %v", fit, err)
	}
	if _, err := table.Domain(&r, Intent{Name: "pl"}); !errors.As(err, &refused) || refused.Suggestion != "padding-left" {
		t.Errorf("a misnamed attribute from data: %v", err)
	}
}

// show is its own layer, after page: show-false hides whatever display a page class sets, and a width
// shows it again with revert-layer, which hands back flex, grid or the -webkit-box of lines.
func TestShowLayer(t *testing.T) {
	var r Rules
	got := classes(t, NewTable(nil), &r,
		Intent{Name: "lines", Value: "2"},
		Intent{Name: "show", Value: "false"},
		Intent{Width: "medium", Name: "show", Value: "true"},
	)
	if got != "lines-2 show-false medium:show-true" {
		t.Fatalf("%q", got)
	}
	css := string(r.CSS())
	want := "@layer show{\n.show-false{display:none}\n@media (min-width:768px){\n.medium\\:show-true{display:revert-layer}\n}\n}\n"
	if !strings.HasSuffix(css, want) {
		t.Errorf("show is not its own layer, last, with revert-layer:\n%s", css)
	}
	if !strings.Contains(css, "@layer page{\n.lines-2{display:-webkit-box;") || strings.Contains(css[strings.Index(css, "@layer page{"):strings.Index(css+"@layer show{", "@layer show{")], "show-") {
		t.Errorf("lines belongs to the page layer, show to its own:\n%s", css)
	}
}

// A page that records nothing has no stylesheet: no name, no link.
func TestEmptyRulesHaveNoFile(t *testing.T) {
	var r Rules
	if r.CSS() != nil || r.File().Name != "" || r.File().Role != "page" {
		t.Fatalf("a page with no rule has a stylesheet: %q %+v", r.CSS(), r.File())
	}
	NewTable(nil).Class(&r, Intent{Name: "flex", Value: "column"}) // reboot.css styles it
	NewTable(nil).Class(&r, Intent{Name: "strikethrough", Value: "true"})
	if r.CSS() != nil {
		t.Fatalf("flex-column and <s> need no page rule, yet the page has a stylesheet:\n%s", r.CSS())
	}
}

// The weights a page sets, for fonts.<hash>.css to ship exactly those.
func TestWeights(t *testing.T) {
	var r Rules
	classes(t, NewTable(nil), &r,
		Intent{Name: "weight", Value: "bold"},
		Intent{State: "hover", Name: "weight", Value: "semibold"},
		Intent{Name: "weight", Value: "bold"},
		Intent{Name: "size", Value: "small"},
	)
	if got := r.Weights(); len(got) != 2 || got[0] != 600 || got[1] != 700 {
		t.Fatalf("the weights the page sets: %v, want [600 700]", got)
	}
}

// The same intents give the same bytes whatever order they are recorded in: the page's name is its
// content, not the order its branches compiled.
func TestCSSDoesNotDependOnRecordingOrder(t *testing.T) {
	table := NewTable(examplePalette(t))
	intents := []Intent{
		{Name: "gap", Value: "10"}, {Name: "gap", Value: "2"}, {Name: "gap", Value: "0.5"},
		{Tag: "badge", Name: "color", Value: "success"}, {Name: "color", Value: "success"},
		{Width: "large", Name: "columns", Value: "2"}, {Width: "small", Name: "columns", Value: "2"},
		{State: "hover,focus", Name: "color", Value: "ink"}, {Name: "place", Value: "below-end"},
	}
	var forward, backward Rules
	for i := range intents {
		classes(t, table, &forward, intents[i])
		classes(t, table, &backward, intents[len(intents)-1-i])
	}
	if string(forward.CSS()) != string(backward.CSS()) {
		t.Fatalf("order of recording shows:\n%s\n%s", forward.CSS(), backward.CSS())
	}
	css := string(forward.CSS())
	if !(strings.Index(css, ".gap-0\\.5{") < strings.Index(css, ".gap-2{") && strings.Index(css, ".gap-2{") < strings.Index(css, ".gap-10{")) {
		t.Fatalf("steps not in numeric order:\n%s", css)
	}
}
