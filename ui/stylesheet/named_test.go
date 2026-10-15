package stylesheet

import (
	"strconv"
	"strings"
	"testing"
)

// The dictionary is the two lists as written (named_source_test.go): CSS Color 4's 148 names, each
// its CSS value, and the 207 names only Figma's colour list has, each its Figma value, but for the five
// that only rename a CSS colour, which are refused with the CSS name. Every name is read where a
// colour is (color, background, border) as its hex, the same class and rule as the table writes for
// white. 148 + 207 = 355 words: 350 colours, 5 refusals.
func TestNamedColoursAreTheTwoLists(t *testing.T) {
	if len(sourceCSS) != 148 || len(sourceFigmaOnly) != 207 || len(sourceShared) != 48 {
		t.Fatalf("the source lists hold %d CSS names, %d Figma-only names, %d shared: want 148, 207, 48", len(sourceCSS), len(sourceFigmaOnly), len(sourceShared))
	}
	table := NewTable(nil)
	var r Rules
	var rules []string // every rule page.css must hold, checked once at the end
	resolved, refused := 0, 0
	read := func(name, want string) {
		t.Helper()
		if got, ok := NamedColour(name); !ok || got != want {
			t.Errorf("NamedColour(%q) = %q %v, want %s", name, got, ok, want)
		}
		if fixed, ok := fixedColors[name]; ok {
			want = fixed // white and black keep the spelling the table has always written: the same colour
		}
		for _, attribute := range []string{"color", "background", "border"} {
			class, warnings, err := table.Read(&r, Intent{Name: attribute, Value: name})
			if err != nil || len(warnings) > 0 || class != attribute+"-"+name {
				t.Errorf("%s=%q: class %q, warnings %v, error %v", attribute, name, class, warnings, err)
			}
		}
		rules = append(rules,
			"."+escape("color-"+name)+"{color:"+want+"}",
			"."+escape("background-"+name)+"{background-color:"+want+"}",
			"."+escape("border-"+name)+"{border-width:1px;border-color:"+want+"}")
		resolved++
	}
	for name, hex := range sourceCSS {
		read(name, hex)
	}
	for name, hex := range sourceFigmaOnly {
		if css, ok := namedAliases[name]; ok {
			_, _, err := table.Read(nil, Intent{Name: "color", Value: name})
			var p *Problem
			if err == nil || !asProblem(err, &p) || p.Suggestion != css || sourceCSS[css] != hex {
				t.Errorf("color=%q: %v, want it refused with %s (%s)", name, err, css, hex)
			}
			refused++
			continue
		}
		read(name, hex)
	}
	if resolved != 350 || refused != 5 || len(NamedColours()) != 350 {
		t.Errorf("%d names read, %d refused, %d in the dictionary: want 350, 5, 350", resolved, refused, len(NamedColours()))
	}
	css := "\n" + string(r.CSS())
	for _, rule := range rules {
		if !strings.Contains(css, "\n"+rule+"\n") {
			t.Errorf("page.css has no %s", rule)
		}
	}
	// grey and gray, aqua and cyan: CSS has both names, so the dictionary does
	for _, pair := range [][2]string{{"grey", "gray"}, {"aqua", "cyan"}, {"darkslategrey", "darkslategray"}} {
		a, _ := NamedColour(pair[0])
		b, _ := NamedColour(pair[1])
		if a == "" || a != b {
			t.Errorf("%s %q and %s %q: CSS writes both, one colour", pair[0], a, pair[1], b)
		}
	}
}

func asProblem(err error, p **Problem) bool {
	problem, ok := err.(*Problem)
	*p = problem
	return ok
}

// Where Figma and CSS give one name two colours, the name is CSS's colour (48 names): violet is
// #ee82ee, not Figma's #7f00ff.
func TestNamedColoursCSSWinsWhereFigmaSharesTheName(t *testing.T) {
	for _, row := range sourceShared {
		display, figma, css := row[0], row[1], row[2]
		name := strings.ReplaceAll(strings.ToLower(display), " ", "")
		name = strings.ReplaceAll(name, "-", "")
		got, ok := NamedColour(name)
		if !ok || got != css || got == figma {
			t.Errorf("%s (Figma %q): %q is %q, want CSS's %s", name, display, name, got, css)
		}
	}
	for name, want := range map[string]string{"violet": "#ee82ee", "gray": "#808080", "teal": "#008080", "gold": "#ffd700", "coral": "#ff7f50"} {
		if got, _ := NamedColour(name); got != want {
			t.Errorf("%s is %q, want CSS's %s", name, got, want)
		}
	}
}

// A word that means a named colour but is not its name is refused with the name: the five Figma names
// that only rename a CSS colour, a CSS name written with a hyphen, in capitals, a Figma name run
// together. A typo is told the nearest names. CONTROL: each name as written is read.
func TestNamedColourWrittenOtherwiseIsAnError(t *testing.T) {
	table := NewTable(nil)
	for written, want := range map[string]string{
		"navy-blue":    "navy",
		"crimson-red":  "firebrick",
		"grass-green":  "lawngreen",
		"purple-blue":  "slateblue",
		"red-violet":   "mediumvioletred",
		"Navy-Blue":    "navy",
		"slate-gray":   "slategray",
		"SlateGray":    "slategray",
		"dark-blue":    "darkblue",
		"burntorange":  "burnt-orange",
		"Burnt-Orange": "burnt-orange",
		"White":        "white",
	} {
		for _, form := range []struct{ value, suggestion string }{{written, want}, {written + "/50", want + "/50"}} {
			_, _, err := table.Read(nil, Intent{Name: "color", Value: form.value})
			var p *Problem
			if err == nil || !asProblem(err, &p) || p.Warning || p.Suggestion != form.suggestion {
				t.Errorf("color=%q: %v, want an error suggesting %q", form.value, err, form.suggestion)
				continue
			}
			hex, _ := fixedColour(want)
			if named, _ := NamedColour(want); named != "" {
				hex = named
			}
			if !strings.Contains(p.Message, `color="`+form.value+`": `+written+" không phải tên màu") || !strings.Contains(p.Message, want+" ("+hex+")") || !strings.HasSuffix(p.Message, "viết "+want) {
				t.Errorf("color=%q says %q", form.value, p.Message)
			}
		}
		if _, _, err := table.Read(nil, Intent{Name: "color", Value: want}); err != nil {
			t.Errorf("control: color=%q: %v", want, err)
		}
	}
	if !strings.Contains(table.mustRefuse(t, "navy-blue"), "tên Figma đặt cho navy (#000080) của CSS") {
		t.Error("an alias says it is Figma's name of the CSS colour")
	}
	// a typo of a name, of a token: the nearest words; no suggestion, the author picks
	for typo, near := range map[string]string{"tomatoe": "; gần nhất: tomato.", "cool-grey": "; gần nhất: cool-gray.", "papr": "; gần nhất: paper."} {
		_, _, err := table.Read(nil, Intent{Name: "background", Value: typo})
		var p *Problem
		if err == nil || !asProblem(err, &p) || p.Suggestion != "" || !strings.Contains(p.Message, `"`+typo+`" không có trong bảng màu của trang`+near) {
			t.Errorf("background=%q: %v, want it told %q", typo, err, near)
		}
	}
	if message := table.mustRefuse(t, "qwerty-500"); strings.Contains(message, "gần nhất") || !strings.Contains(message, `"qwerty" không có trong bảng màu`) || !strings.Contains(message, "white, black, tomato, cerulean… (350 tên màu của CSS và Figma)") {
		t.Errorf("a word near no name names none, and the message says what a named colour is: %s", message)
	}
}

func (t *Table) mustRefuse(test *testing.T, value string) string {
	test.Helper()
	_, _, err := t.Read(nil, Intent{Name: "color", Value: value})
	if err == nil {
		test.Fatalf("color=%q is read", value)
	}
	return err.Error()
}

// In markup a named colour is fixed like white and black: its hex in the rule (no custom property, the
// palette does not re-value it), an alpha /N mixed toward transparent, a prefix as any colour; the old
// shade .N is refused with the alpha to write and the role that takes -N and +N, and -N and +N are
// refused the same way. Values from {{ }} keep the palette's tokens: no named colour among them. A
// badge in a named colour sits on the quiet panel.
func TestNamedColourInMarkupIsFixed(t *testing.T) {
	table := NewTable(examplePalette(t))
	var r Rules
	got := classes(t, table, &r,
		Intent{Name: "color", Value: "tomato"},
		Intent{Name: "background", Value: "burnt-orange/40"},
		Intent{Name: "border", Value: "cerulean/0"},
		Intent{State: "hover", Name: "background", Value: "rose-gold"},
	)
	if want := "color-tomato background-burnt-orange/40 border-cerulean/0 hover:background-rose-gold"; got != want {
		t.Fatalf("classes %q, want %q", got, want)
	}
	css := string(r.CSS())
	for _, rule := range []string{
		`.color-tomato{color:#ff6347}`,
		`.background-burnt-orange\/40{background-color:color-mix(in srgb,#be5103 40%,transparent)}`,
		`.border-cerulean\/0{border-width:1px;border-color:color-mix(in srgb,#007ba7 0%,transparent)}`,
		`background-color:#dea193`,
	} {
		if !strings.Contains(css, rule) {
			t.Errorf("page.css has no %s:\n%s", rule, css)
		}
	}
	if strings.Contains(css, "var(--tomato") || strings.Contains(css, "var(--burnt") || strings.Contains(string(Colors(table.Palette()).CSS), "tomato") {
		t.Errorf("a named colour is no token of the palette:\n%s", css)
	}
	if !strings.Contains(css, `.hover\:background-rose-gold`) {
		t.Errorf("hover: holds the named colour as any colour:\n%s", css)
	}

	for value, suggestion := range map[string]string{"tomato.70": "tomato/70", "cerulean.50/80": ""} {
		_, _, err := table.Read(nil, Intent{Name: "color", Value: value})
		var p *Problem
		if err == nil || !asProblem(err, &p) || p.Warning || p.Suggestion != suggestion {
			t.Errorf("color=%q: %v, want it refused, suggesting %q", value, err, suggestion)
			continue
		}
		base, shade, _ := strings.Cut(strings.Split(value, "/")[0], ".")
		n, _ := strconv.Atoi(shade)
		for _, says := range []string{`color="` + value + `": ` + base + " là màu có tên, cố định ở sáng và tối, không có -N, +N",
			"độ hiện viết " + base + "/" + shade, `router.colors({ brand: "` + base + `" }) rồi viết brand-` + strconv.Itoa(100-n) + ", brand+15"} {
			if !strings.Contains(p.Message, says) {
				t.Errorf("color=%q says %q, not %q", value, p.Message, says)
			}
		}
	}
	for _, value := range []string{"tomato-30", "cerulean+10/80", "rose-gold-20"} {
		base := strings.TrimRight(strings.Split(value, "/")[0], "+-0123456789")
		if message := table.mustRefuse(t, value); !strings.Contains(message, base+" là màu có tên, cố định ở sáng và tối, không có -N hay +N") ||
			!strings.Contains(message, `router.colors({ accent: "`+base+`" })`) {
			t.Errorf("color=%q: %s", value, message)
		}
	}
	// CONTROL: white's shade keeps its own message
	if message := table.mustRefuse(t, "white.80"); !strings.Contains(message, "white là màu cố định, không nhạt hay đậm theo trang") {
		t.Errorf("white.80: %s", message)
	}

	domain, err := table.Domain(nil, Intent{Name: "background"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := domain["tomato"]; ok || domain["canvas"] == "" {
		t.Errorf("a value from data takes the palette's declared tokens, no named colour: %v", domain)
	}
	if fill, text, family := table.BadgeTint("tomato"); fill != "panel" || text != "tomato" || family {
		t.Errorf("a badge in tomato is %s under %s (family %v), want the panel under tomato", fill, text, family)
	}
}

// A named colour paints the same in light and in dark: it is fixed, the contrast check measures its hex
// in both modes, and an alpha is its alpha. CONTROL: brand, a token, is re-valued in the dark.
func TestNamedColourPaintsTheSameInBothModes(t *testing.T) {
	p, err := NewPalette(true)
	if err != nil {
		t.Fatal(err)
	}
	for value, hex := range map[string]string{"gold": "#ffd700", "navy": "#000080", "cerulean": "#007ba7"} {
		light, okLight := p.Paint(value, LightMode)
		dark, okDark := p.Paint(value, DarkMode)
		if !okLight || !okDark || light != dark || light.Hex() != hex || !light.Opaque() {
			t.Errorf("%s paints %s light, %s dark; want %s in both", value, light.Hex(), dark.Hex(), hex)
		}
		if c, _ := SplitColour(value); !c.Fixed() {
			t.Errorf("%s is not fixed", value)
		}
		if half, _ := p.Paint(value+"/50", DarkMode); half.A != 0.5 || half.Hex() != hex {
			t.Errorf("%s/50 paints %s at %v", value, half.Hex(), half.A)
		}
	}
	light, _ := p.Paint("brand", LightMode)
	dark, _ := p.Paint("brand", DarkMode)
	if light == dark {
		t.Fatal("control: brand is the same in both modes; the test would not tell a fixed colour from a token")
	}
	if _, ok := p.Paint("navy-blue", LightMode); ok {
		t.Error("navy-blue is no name: nothing to paint")
	}
}

// router.colors({ brand: "cerulean" }) is brand: "#007ba7" exactly: the same label, the same dark, the
// same colors.css byte for byte, and the role takes -N and +N as any (brand+15, brand-85, brand-95,
// cerulean's own in both modes). A { light, dark } pair takes names on both sides. A name written
// otherwise is refused with the name. CONTROL: the default brand is another colour.
func TestNamedColourSeedsARole(t *testing.T) {
	seeded, err := NewPalette(true, map[string]Color{"brand": {Light: "cerulean"}, "success": {Light: "forestgreen"}})
	if err != nil {
		t.Fatal(err)
	}
	hexed, err := NewPalette(true, map[string]Color{"brand": {Light: "#007ba7"}, "success": {Light: "#228b22"}})
	if err != nil {
		t.Fatal(err)
	}
	if a, b := string(Colors(seeded).CSS), string(Colors(hexed).CSS); a != b {
		t.Errorf("a named seed must be its hex:\n%s\n%s", a, b)
	}
	defaults, _ := NewPalette(true)
	for _, token := range []string{"brand", "brand.on"} {
		light, dark, ok := seeded.Value(token)
		was, _, _ := defaults.Value(token)
		// the label rung may be white on both hues; brand is cerulean's own
		if !ok || dark == "" || light == was && token != "brand.on" {
			t.Errorf("%s: %s / %s (Kit Design's %s): the seed must grow the family and its dark", token, light, dark, was)
		}
	}
	for _, value := range []string{"brand+15", "brand-85", "brand-95"} {
		got, from, was := hexes(t, seeded, value), hexes(t, hexed, value), hexes(t, defaults, value)
		if got != from || got == was {
			t.Errorf("%s: %s, from the hex %s, Kit Design's %s: the seed must be its hex, and its own", value, got, from, was)
		}
	}
	if light, _, _ := seeded.Value("brand"); light != "#007ba7" {
		t.Errorf("brand is %s, want cerulean's #007ba7", light)
	}

	pair, err := NewPalette(false, map[string]Color{"canvas": {Light: "ivory", Dark: "midnightblue"}, "paper": {Light: "white"}})
	if err != nil {
		t.Fatal(err)
	}
	if light, dark, _ := pair.Value("canvas"); light != "#fffff0" || dark != "#191970" {
		t.Errorf("canvas { light: ivory, dark: midnightblue } is %s / %s", light, dark)
	}
	if light, _, _ := pair.Value("paper"); light != "#ffffff" {
		t.Errorf("paper: white is %s", light)
	}

	for _, c := range []struct {
		color Color
		says  string
	}{
		{Color{Light: "navy-blue"}, `router.colors: "brand" là "navy-blue": navy-blue không phải tên màu: đó là tên Figma đặt cho navy (#000080) của CSS; viết navy`},
		{Color{Light: "red-violet"}, `viết mediumvioletred`},
		{Color{Light: "#fff", Dark: "slate-gray"}, `router.colors: "brand" (dark) là "slate-gray": slate-gray không phải tên màu: tên là slategray (#708090)`},
		{Color{Light: "Cerulean"}, `tên là cerulean (#007ba7)`},
		{Color{Light: "ceruleen"}, `"brand" is "ceruleen": a colour is "#rgb", "#rrggbb" or a named colour (tomato, cerulean…)`},
	} {
		_, err := NewPalette(false, map[string]Color{"brand": c.color})
		if err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("brand %+v: %v, want %q", c.color, err, c.says)
		}
	}
}

// A token may not take a named colour's name, a CSS one or one only Figma has: the name already means
// that colour, the same in light and dark. CONTROL: the same words in a longer token name are tokens.
func TestTokenNamedLikeANamedColourIsAnError(t *testing.T) {
	for name, hex := range map[string]string{"tomato": "#ff6347", "cerulean": "#007ba7", "gray": "#808080"} {
		_, err := NewPalette(false, map[string]Color{name: {Light: "#123456"}})
		want := `router.colors: "` + name + `" đã là tên một màu (` + hex + `), cùng một màu ở sáng và tối: đặt tên token theo vai trò (brand, accent…) và cho nó màu ấy: router.colors({ brand: "` + name + `" })`
		if err == nil || err.Error() != want {
			t.Errorf("token %q: %v\nwant %s", name, err, want)
		}
	}
	p, err := NewPalette(false, map[string]Color{"tomato-sauce": {Light: "#c0392b"}, "chart.cerulean": {Light: "#007ba7"}})
	if err != nil {
		t.Fatalf("control: %v", err)
	}
	if !p.Has("tomato-sauce") || !p.Has("tomato-sauce.on") || !p.Has("chart.cerulean") {
		t.Errorf("control: tokens %v", p.Tokens())
	}
}
