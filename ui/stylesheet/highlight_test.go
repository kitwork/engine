package stylesheet

import (
	"sort"
	"strings"
	"testing"
)

// highlightCall is the owner's example of router.highlight (13/10/2026) at one place of router.work.
func highlightCall(at Source) map[string]HighlightColour {
	return map[string]HighlightColour{
		"keyword":  {Color: "violet", Source: at},
		"string":   {Color: "success", Source: at},
		"comment":  {Color: "ink-40", Source: at},
		"function": {Pair: Color{Light: "#0550ae", Dark: "#79c0ff"}, Source: at},
	}
}

var highlightAt = Source{File: "router.work", Line: 20, Column: 7}

// codeCSS records the rules of kinds on a palette and returns the page's stylesheet.
func codeCSS(t *testing.T, p *Palette, kinds ...string) string {
	t.Helper()
	r := &Rules{}
	table := NewTable(p)
	for _, kind := range kinds {
		if _, err := table.Code(r, kind); err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
	}
	return string(r.CSS())
}

// The keys of router.highlight are the kinds that have a look, every kind of jit/highlight but plain:
// eighteen, in the order its messages list them. A key that is none says what to write: plain (however
// its letters are written) is the code's own text; a kind in other letters, its name; a short name, the
// name in full; a typo, the nearest kinds, each written as a key is (gần nhất: "regular-expression": a
// hyphenated key copied bare is a subtraction); anything else, every kind. A kind with a hyphen is written
// in quotes, and function too, as the docs write it (the parser takes it bare as well: no message says it
// must be quoted). CONTROL: every kind reads, with no message.
func TestHighlightKeysAreTheKinds(t *testing.T) {
	kinds := HighlightKinds()
	sorted := append([]string(nil), kinds...)
	sort.Strings(sorted)
	if strings.Join(sorted, " ") != strings.Join(CodeKinds(), " ") || len(kinds) != 18 {
		t.Errorf("HighlightKinds is not the kinds that have a look:\n got  %v\n want %v", sorted, CodeKinds())
	}
	for _, kind := range kinds { // CONTROL
		if meant, problem := CheckHighlightKey(kind, `"brand"`); problem != "" || meant != kind {
			t.Errorf("%s is a kind: %q, %q", kind, meant, problem)
		}
	}
	for key, want := range map[string]string{
		"plain":             `plain là chữ thường của khối mã, không có màu riêng: nó theo màu chữ của <code>; viết color= trên <code> (hay trên thẻ chủ của terminal)`,
		"Plain":             `plain là chữ thường của khối mã, không có màu riêng`,
		"PLAIN":             `plain là chữ thường của khối mã, không có màu riêng`,
		"keywrod":           `không có loại chữ "keywrod"; gần nhất: keyword`,
		"regular-expresion": `không có loại chữ "regular-expresion"; gần nhất: "regular-expression"`,
		"fucntion":          `không có loại chữ "fucntion"; gần nhất: "function"`,
		"regex":             `tên loại chữ viết đủ chữ: router.highlight({ "regular-expression": "success" })`,
		"fn":                `tên loại chữ viết đủ chữ: router.highlight({ "function": "success" })`,
		"regularExpression": `tên loại chữ là regular-expression, chữ thường nối bằng gạch, viết trong dấu nháy: router.highlight({ "regular-expression": "success" })`,
		"Function":          `tên loại chữ là function, chữ thường: router.highlight({ "function": "success" })`,
		"banana":            `không có loại chữ "banana". Loại chữ: keyword, type, string, regular-expression, number, constant, attribute, function, property, variable, tag, comment, operator, punctuation, heading, inserted, deleted, prompt`,
	} {
		if _, problem := CheckHighlightKey(key, `"success"`); problem != want && !(strings.HasPrefix(want, "plain là") && strings.HasPrefix(problem, want)) {
			t.Errorf("%s:\n got  %s\n want %s", key, problem, want)
		}
	}
	if HighlightKey("keyword") != "keyword" || HighlightKey("regular-expression") != `"regular-expression"` || HighlightKey("function") != `"function"` {
		t.Errorf("a key with a hyphen, and function as the docs write it, are written in quotes")
	}
}

// A kind's colour is read as color= reads a colour (operators of 13/10/2026), but where only a colour
// goes: what the palette refuses is told after the call's place as a gradient's stop is told, with no
// word of gradients — no list of them, none suggested (lady-lips is no pink-blush here, glwo no glow). A
// gradient itself is refused: a kind's colour is a colour. CONTROL: the colours color= takes are taken,
// and a value whose message names no gradient reads the same as through color=.
func TestHighlightValuesReadAsColorDoes(t *testing.T) {
	kit, _ := NewPalette(true)
	table := NewTable(kit)
	at := func(v string) map[string]HighlightColour {
		return map[string]HighlightColour{"keyword": {Color: v, Source: highlightAt}}
	}
	for _, v := range []string{"violet", "ink-40", "brand+15", "brand/80", "white", "brand.on"} { // CONTROL
		if _, err := kit.WithHighlight(at(v)); err != nil {
			t.Errorf("%s is a colour color= takes: %v", v, err)
		}
	}
	body := func(in Intent, v string) string {
		_, _, _, _, p := table.colour(in, v)
		if p == nil {
			t.Fatalf("%s reads through %+v", v, in)
		}
		return strings.TrimPrefix(p.Message, said(in, v))
	}
	opening := func(v string) string { return `router.work:20:7: router.highlight({ keyword: "` + v + `" }): ` }
	for _, v := range []string{"brnad", "violet+10", "ink.70", "lady-lips", "glwo"} {
		_, err := kit.WithHighlight(at(v))
		if err == nil {
			t.Errorf("%s is refused", v)
			continue
		}
		got := err.Error()
		want := opening(v) + body(Intent{Tag: stopTag, Name: "color", Value: v}, v)
		if got != want {
			t.Errorf("%s:\n got  %s\n want %s", v, got, want)
		}
		if strings.Contains(got, "gradient") || strings.Contains(got, "pink-blush") || strings.Contains(got, "glow") {
			t.Errorf("%s: a kind's colour is told no gradient: %s", v, got)
		}
	}
	if _, err := kit.WithHighlight(at("brnad")); err == nil || !strings.Contains(err.Error(), `"brnad" không có trong bảng màu của trang; gần nhất: brand.`) {
		t.Errorf("brnad: %v", err)
	}
	// CONTROL: what names no gradient through color= is the same words
	for _, v := range []string{"violet+10", "ink.70"} {
		_, err := kit.WithHighlight(at(v))
		if want := opening(v) + body(Intent{Name: "color", Value: v}, v); err == nil || err.Error() != want {
			t.Errorf("%s through color= says:\n %s\n here: %v", v, want, err)
		}
	}
	for _, v := range []string{"glow", "glow-30", "cirrus"} {
		_, err := kit.WithHighlight(at(v))
		if want := opening(v) + v + " là một gradient; màu của một loại chữ là một màu"; err == nil || !strings.HasPrefix(err.Error(), want) {
			t.Errorf("%s:\n got  %v\n want %s…", v, err, want)
		}
	}
	if _, err := kit.WithHighlight(map[string]HighlightColour{"keywrod": {Color: "brand"}}); err == nil || !strings.Contains(err.Error(), "gần nhất: keyword") {
		t.Errorf("a key that is no kind is refused: %v", err)
	}
}

// A kind's { light, dark } pair is its own colour in each mode, each side "#rgb", "#rrggbb" or a named
// colour, as a token of router.colors and a stop of router.gradients: its custom property
// --kitwork-highlight-<kind> set per mode in the page's stylesheet, the rule reading it. Paint reads the
// name per mode for the contrast check, kept apart from the engine's own colours (p.engine, which the
// palette it came from keeps as it was). What can be read without a palette is checked at once
// (CheckHighlight). CONTROL: a palette with no dark look sets the light side only, as a gradient's pair; a
// pair of one colour is written as that hex, no property.
func TestHighlightPairs(t *testing.T) {
	kit, _ := NewPalette(true)
	engines := len(kit.engine)
	q, err := kit.WithHighlight(map[string]HighlightColour{
		"function": {Pair: Color{Light: "#0550ae", Dark: "#79c0ff"}, Source: highlightAt},
		"property": {Pair: Color{Light: "navy", Dark: "#79c0ff"}, Source: highlightAt},
	})
	if err != nil {
		t.Fatal(err)
	}
	css := codeCSS(t, q, "function", "property")
	for _, want := range []string{
		":root{--kitwork-highlight-function:#0550ae;--kitwork-highlight-property:#000080}\n",
		"@media screen{:root[data-theme=dark]{--kitwork-highlight-function:#79c0ff;--kitwork-highlight-property:#79c0ff}}\n",
		".code-function{color:var(--kitwork-highlight-function)}",
		".code-property{color:var(--kitwork-highlight-property)}",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("missing %q in\n%s", want, css)
		}
	}
	if strings.Contains(css, "--kitwork-code-info") {
		t.Errorf("a kind the author colours never reads its engine colour:\n%s", css)
	}
	for m, want := range map[Mode]string{LightMode: "#0550ae", DarkMode: "#79c0ff"} {
		if got, ok := q.Paint(HighlightVariable("function"), m); !ok || got.Hex() != want {
			t.Errorf("Paint kitwork-highlight-function in %s: %s, want %s", m.Word(), got.Hex(), want)
		}
	}
	if len(q.engine) != engines || len(kit.engine) != engines {
		t.Errorf("the pair stays out of the engine's colours: %d and %d, want %d", len(q.engine), len(kit.engine), engines)
	}
	if _, ok := kit.Paint(HighlightVariable("function"), LightMode); ok {
		t.Error("the palette WithHighlight came from has no pair")
	}

	light, _ := NewPalette(false) // CONTROL
	q, err = light.WithHighlight(map[string]HighlightColour{
		"function": {Pair: Color{Light: "#0550ae", Dark: "#79c0ff"}, Source: highlightAt},
		"keyword":  {Pair: Color{Light: "#7c3aed", Dark: "#7C3AED"}, Source: highlightAt},
	})
	if err != nil {
		t.Fatal(err)
	}
	css = codeCSS(t, q, "function", "keyword")
	if !strings.Contains(css, ":root{--kitwork-highlight-function:#0550ae}\n") || strings.Contains(css, "data-theme=dark") ||
		!strings.Contains(css, ".code-keyword{color:#7c3aed}") || strings.Contains(css, "--kitwork-highlight-keyword") {
		t.Errorf("CONTROL: no dark look, the light side only; one colour, its hex:\n%s", css)
	}

	for kind, c := range map[string]HighlightColour{
		`keyword: ""`:   {Color: ""},
		`keyword: "#…"`: {Color: "#7c3aed"},
		"light only":    {Pair: Color{Light: "#0550ae"}},
		"dark info":     {Pair: Color{Light: "#0550ae", Dark: "info"}},
		"navy-blue":     {Pair: Color{Light: "#0550ae", Dark: "navy-blue"}},
		"inserted":      {Pair: Color{Light: "#1a7f37", Dark: "#3fb950"}},
	} {
		name := "keyword"
		switch kind {
		case "light only", "dark info", "navy-blue":
			name = "function"
		case "inserted":
			name = "inserted"
		}
		want := map[string]string{
			`keyword: ""`:   `router.highlight({ keyword: "" }): màu trống; viết một màu như color= nhận (brand, ink-30, violet), hay bỏ keyword để giữ màu engine chọn`,
			`keyword: "#…"`: `router.highlight({ keyword: "#7c3aed" }): màu #hex là màu của một chế độ: viết một cặp cho sáng và tối, keyword: { light: "#7c3aed", dark: "#…" } (hai bên "#7c3aed" khi tối cũng là màu ấy); một màu cho cả hai chế độ là một token (router.colors) hay một màu có tên`,
			"light only":    `router.highlight({ "function": … }): cặp màu thiếu dark: viết cả hai, { light: "#0550ae", dark: "#…" }, như một token của router.colors`,
			"dark info":     `router.highlight({ "function": … }): dark là "info": một bên của cặp là "#rgb", "#rrggbb" hay một màu có tên (tomato, cerulean…); một token (brand, ink-30) đã có màu sáng và tối của nó, nên viết thẳng "function": "brand" thay cho cặp`,
			"navy-blue":     `router.highlight({ "function": … }): dark là "navy-blue": navy-blue không phải tên màu: đó là tên Figma đặt cho navy (#000080) của CSS; viết navy`,
			"inserted":      `router.highlight({ inserted: … }): inserted nhận một màu nhấn, không nhận cặp: nền của dòng là màu ấy nhạt 85 (success-85), chữ là màu ấy engine làm đậm tới khi đọc được trên nền đó ở mỗi chế độ. Muốn màu riêng mỗi chế độ thì khai một màu nhấn trong router.colors (router.colors({ added: { light: "#1a7f37", dark: "#3fb950" } })) rồi viết inserted: "added"`,
		}[kind]
		if err := CheckHighlight(name, c); err == nil || err.Error() != want {
			t.Errorf("%s:\n got  %v\n want %s", kind, err, want)
		}
	}
	// A # that is no colour is told so at once, not sent to a pair that the next boot refuses; a #hex for
	// inserted or deleted is told what they take, an accent, not a pair that the next boot refuses too.
	for _, c := range []struct{ kind, v, want string }{
		{"keyword", "#zzz", `router.highlight({ keyword: "#zzz" }): "#zzz" không phải màu: màu #hex là "#rgb" hay "#rrggbb", màu của một chế độ, viết trong một cặp, keyword: { light: "#7c3aed", dark: "#bb9af7" }; một màu cho cả hai chế độ là một token (router.colors) hay một màu có tên`},
		{"inserted", "#1a7f37", `router.highlight({ inserted: "#1a7f37" }): inserted nhận một màu nhấn, không nhận #hex: nền của dòng là màu ấy nhạt 85 (success-85), chữ là màu ấy engine làm đậm tới khi đọc được trên nền đó ở mỗi chế độ. Muốn màu riêng mỗi chế độ thì khai một màu nhấn trong router.colors (router.colors({ added: { light: "#1a7f37", dark: "#…" } })) rồi viết inserted: "added"`},
		{"deleted", "#zzz", `router.highlight({ deleted: "#zzz" }): deleted nhận một màu nhấn, không nhận #hex: nền của dòng là màu ấy nhạt 85 (danger-85), chữ là màu ấy engine làm đậm tới khi đọc được trên nền đó ở mỗi chế độ. Muốn màu riêng mỗi chế độ thì khai một màu nhấn trong router.colors (router.colors({ removed: { light: "#…", dark: "#…" } })) rồi viết deleted: "removed"`},
	} {
		if err := CheckHighlight(c.kind, HighlightColour{Color: c.v}); err == nil || err.Error() != c.want {
			t.Errorf("%s: %s:\n got  %v\n want %s", c.kind, c.v, err, c.want)
		}
	}
}

// A kind the author colours writes the author's colour and never its engine colour, so the engine's
// custom property of a role (--kitwork-code-brand) is set only while a kind of the page still reads it:
// keyword set, type not, the property stays for type; keyword, type and heading set, it is gone. CONTROL:
// with no router.highlight the page sets it.
func TestHighlightWinsOverTheEngineColour(t *testing.T) {
	kit, _ := NewPalette(true)
	q, err := kit.WithHighlight(map[string]HighlightColour{"keyword": {Color: "violet", Source: highlightAt}})
	if err != nil {
		t.Fatal(err)
	}
	css := codeCSS(t, q, "keyword", "type")
	if !strings.Contains(css, ".code-keyword{color:#ee82ee}") || !strings.Contains(css, ".code-type{color:var(--kitwork-code-brand)}") ||
		!strings.Contains(css, "--kitwork-code-brand:#4500de") {
		t.Errorf("keyword is the author's, type the engine's:\n%s", css)
	}
	q, err = kit.WithHighlight(map[string]HighlightColour{
		"keyword": {Color: "violet", Source: highlightAt}, "type": {Color: "brand+40", Source: highlightAt},
		"heading": {Color: "ink", Source: highlightAt},
	})
	if err != nil {
		t.Fatal(err)
	}
	if css := codeCSS(t, q, "keyword", "type", "heading"); strings.Contains(css, "--kitwork-code-brand") {
		t.Errorf("no kind reads the engine's brand, so the page does not set it:\n%s", css)
	}
	if css := codeCSS(t, kit, "keyword", "type", "heading"); !strings.Contains(css, "--kitwork-code-brand") { // CONTROL
		t.Errorf("CONTROL: the engine's brand is set when a kind reads it:\n%s", css)
	}
}

// A colour router.highlight sets is written exactly as color= writes it on the same palette — a token's
// var(), a lighter colour's color-mix(), a stronger one's custom property (set per mode with it), an
// alpha's color-mix(), a named colour's hex — so the two never drift. CONTROL: the .color-<value> rule
// of the same page.
func TestHighlightOperatorsTakeTheColorPath(t *testing.T) {
	kit, _ := NewPalette(true)
	for _, v := range []string{"ink-40", "success+20", "brand", "violet", "brand/80"} {
		q, err := kit.WithHighlight(map[string]HighlightColour{"keyword": {Color: v, Source: highlightAt}})
		if err != nil {
			t.Fatal(err)
		}
		r := &Rules{}
		table := NewTable(q)
		class, err := table.Class(r, Intent{Name: "color", Value: v})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := table.Code(r, "keyword"); err != nil {
			t.Fatal(err)
		}
		css := string(r.CSS())
		start := strings.Index(css, "."+escape(class)+"{")
		if start < 0 {
			t.Fatalf("CONTROL: color=%q has no rule:\n%s", v, css)
		}
		rule := css[start+len("."+escape(class)+"{"):]
		rule = rule[:strings.IndexByte(rule, '}')]
		if want := ".code-keyword{" + rule + "}"; !strings.Contains(css, want) {
			t.Errorf("%s: want %s, as color= writes it, in\n%s", v, want, css)
		}
	}
	q, _ := kit.WithHighlight(map[string]HighlightColour{"keyword": {Color: "success+20", Source: highlightAt}})
	if css := codeCSS(t, q, "keyword"); !strings.Contains(css, "--kitwork-strong-20-success:#00884d") || !strings.Contains(css, "--kitwork-strong-20-success:#7ed89a") {
		t.Errorf("success+20's custom property is set per mode with the rule:\n%s", css)
	}
}

// router.highlight changes a kind's colour only: an author's comment is still italic, a heading still
// bold (its weight in the fonts file), a prompt still out of a selection — its colour the author's, no
// longer a share of the text around it, and measured so (TokenLook: no share). CONTROL: with no
// router.highlight each look is the engine's (codeLooks).
func TestHighlightKeepsItalicWeightAndSelection(t *testing.T) {
	kit, _ := NewPalette(true)
	q, err := kit.WithHighlight(map[string]HighlightColour{
		"comment": {Color: "ink-20", Source: highlightAt}, "heading": {Color: "info+20", Source: highlightAt},
		"prompt": {Color: "ink-30", Source: highlightAt},
	})
	if err != nil {
		t.Fatal(err)
	}
	r := &Rules{}
	table := NewTable(q)
	for _, kind := range []string{"comment", "heading", "prompt"} {
		if _, err := table.Code(r, kind); err != nil {
			t.Fatal(err)
		}
	}
	css := string(r.CSS())
	for _, want := range []string{
		".code-comment{color:color-mix(in oklab,var(--ink) 80%,var(--canvas));font-style:italic}",
		".code-heading{color:var(--kitwork-strong-20-info);font-weight:700}",
		".code-prompt{color:color-mix(in oklab,var(--ink) 70%,var(--canvas));-webkit-user-select:none;user-select:none}",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("missing %s in\n%s", want, css)
		}
	}
	if strings.Contains(css, "currentColor") {
		t.Errorf("the prompt the author colours is no share of the text around it:\n%s", css)
	}
	if got := r.Weights(); len(got) != 1 || got[0] != 700 {
		t.Errorf("the heading ships the weight 700, got %v", got)
	}
	look, _ := table.TokenLook("prompt")
	if look.Share != 0 || !look.Unselectable || look.Color != "ink-30" || look.Source == nil || *look.Source != highlightAt {
		t.Errorf("the prompt's look is the author's colour, no share, still unselectable: %+v", look)
	}
	for _, kind := range []string{"comment", "heading", "prompt"} { // CONTROL
		engine, _ := NewTable(kit).TokenLook(kind)
		want, _ := CodeLookOf(kind)
		if engine != want {
			t.Errorf("CONTROL: %s with no router.highlight is %+v, want %+v", kind, engine, want)
		}
	}
}

// inserted and deleted take an accent and are made from it as a badge is: the line's fill is the accent
// 85% lighter, its text the badge's engine colour, made stronger as far as it must to read on that fill
// in each mode — on the five palettes the code colours are measured on, 4.5:1 in light and in dark.
// Written their default accent (success, danger), they keep the engine's colours byte for byte. What is
// no accent that grows a label is refused with the route's accents. CONTROL: <badge color="info"> on the
// same palette sets the same --kitwork-badge-info.
func TestHighlightInsertedTakesAnAccent(t *testing.T) {
	for name, p := range codePalettes(t) {
		q, err := p.WithHighlight(map[string]HighlightColour{"inserted": {Color: "info", Source: highlightAt}, "deleted": {Color: "warning", Source: highlightAt}})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, kind := range []string{"inserted", "deleted"} {
			look, _ := NewTable(q).TokenLook(kind)
			for _, m := range q.Modes() {
				if !q.readsIn(look.Color, []string{look.Background}, NormalTextRatio, m) {
					x, _ := q.Paint(look.Color, m)
					t.Errorf("%s: %s (%s %s) does not read on %s in %s", name, kind, look.Color, x.Hex(), look.Background, m.Word())
				}
			}
		}
		if lines := q.HighlightShortfalls(); len(lines) != 0 {
			t.Errorf("%s: an accent's line reads on its fill: %+v", name, lines)
		}
	}
	kit, _ := NewPalette(true)
	q, _ := kit.WithHighlight(map[string]HighlightColour{"inserted": {Color: "info", Source: highlightAt}})
	css := codeCSS(t, q, "inserted")
	if !strings.Contains(css, ".code-inserted{color:var(--kitwork-badge-info);background-color:color-mix(in oklab,var(--info) 15%,var(--canvas))}") {
		t.Errorf("inserted: info is the badge's text on info-85:\n%s", css)
	}
	r := &Rules{} // CONTROL
	if _, err := NewTable(kit).Class(r, Intent{Tag: "badge", Name: "color", Value: "info"}); err != nil {
		t.Fatal(err)
	}
	badge := string(r.CSS())
	for _, side := range []string{":root{--kitwork-badge-info:", "{--kitwork-badge-info:"} {
		at := strings.Index(badge, side)
		if at < 0 {
			t.Fatalf("CONTROL: the badge sets %s:\n%s", side, badge)
		}
		value := badge[at+len(side):]
		value = value[:7]
		if !strings.Contains(css, side+value) {
			t.Errorf("inserted and <badge color=\"info\"> set one --kitwork-badge-info (%s), got\n%s", value, css)
		}
	}
	plain, _ := kit.WithHighlight(map[string]HighlightColour{"inserted": {Color: "success", Source: highlightAt}, "deleted": {Color: "danger", Source: highlightAt}})
	if got, want := codeCSS(t, plain, "inserted", "deleted"), codeCSS(t, kit, "inserted", "deleted"); got != want {
		t.Errorf("the default accents keep the engine's colours:\n got  %s\n want %s", got, want)
	}
	opening := func(kind, v string) string {
		return `router.work:20:7: router.highlight({ ` + kind + `: "` + v + `" }): `
	}
	accents := ". Màu nhấn của trang: brand, danger, info, success, warning"
	for v, want := range map[string]string{
		"violet":     opening("inserted", "violet") + "inserted là màu nhấn của dòng thêm: nền là màu ấy nhạt 85, chữ là màu ấy đậm lên tới khi đọc được; violet là màu có tên, cố định ở sáng và tối, không có bậc nhạt" + accents,
		"success+10": opening("inserted", "success+10") + `inserted nhận màu nhấn như đã khai, không phép: viết inserted: "success"; nền là success-85, chữ engine làm đậm từ success tới khi đọc được`,
		"success/80": opening("inserted", "success/80") + `inserted nhận màu nhấn như đã khai, không phép: viết inserted: "success"`,
		"ink":        opening("inserted", "ink") + "inserted là màu nhấn của dòng thêm; ink không phải màu nhấn (ink, canvas, paper, panel, line là chữ và nền, X.on là chữ trên nền X)" + accents,
		"brand.on":   opening("inserted", "brand.on") + "inserted là màu nhấn của dòng thêm; brand.on không phải màu nhấn",
	} {
		if _, err := kit.WithHighlight(map[string]HighlightColour{"inserted": {Color: v, Source: highlightAt}}); err == nil || !strings.HasPrefix(err.Error(), want) {
			t.Errorf("%s:\n got  %v\n want %s", v, err, want)
		}
	}
	// the route's own accents are listed: a branch's added
	added, _ := NewPalette(true, map[string]Color{"added": {Light: "#1a7f37", Dark: "#3fb950"}})
	if _, err := added.WithHighlight(map[string]HighlightColour{"deleted": {Color: "ink", Source: highlightAt}}); err == nil ||
		!strings.HasSuffix(err.Error(), ". Màu nhấn của trang: added, brand, danger, info, success, warning") {
		t.Errorf("the accents are the route's: %v", err)
	}
	if _, err := added.WithHighlight(map[string]HighlightColour{"inserted": {Color: "added", Source: highlightAt}}); err != nil {
		t.Errorf("an accent the site declares is one: %v", err)
	}
}

// The colours of router.highlight are measured on the page's grounds (canvas, paper, panel) in each mode,
// as the pixel a screen paints, once a route. On the kitwork example's palette the owner's call: violet
// falls short on all three in light and reads in dark; success on all three in light; ink-40 on canvas
// and panel in light and on all three in dark (paper 3.87:1); the pair of function reads. The line of the
// call names each, then what reads in both modes: violet made stronger in light only, success+30,
// ink-30 (the comment's default). CONTROL: each kind written as its own engine colour's hexes, light and
// dark, falls short nowhere on the five palettes; brand itself does on Kit Design in light.
func TestHighlightShortfalls(t *testing.T) {
	palettes := codePalettes(t)
	q, err := palettes["kitwork"].WithHighlight(highlightCall(highlightAt))
	if err != nil {
		t.Fatal(err)
	}
	type short struct {
		kind, ground string
		m            Mode
	}
	got := map[short]string{}
	for _, s := range q.HighlightShortfalls() {
		got[short{s.Kind, s.Ground, s.Mode}] = RatioText(s.Ratio, NormalTextRatio)
		if s.Source != highlightAt {
			t.Errorf("%s: the call's place is %v", s.Kind, s.Source)
		}
	}
	want := map[short]string{
		{"keyword", "canvas", LightMode}: "2.19", {"keyword", "paper", LightMode}: "2.32", {"keyword", "panel", LightMode}: "2.08",
		{"string", "canvas", LightMode}: "3.20", {"string", "paper", LightMode}: "3.39", {"string", "panel", LightMode}: "3.04",
		{"comment", "canvas", LightMode}: "4.46", {"comment", "panel", LightMode}: "4.23",
		{"comment", "canvas", DarkMode}: "4.33", {"comment", "paper", DarkMode}: "3.87", {"comment", "panel", DarkMode}: "4.16",
	}
	for k, r := range want {
		if got[k] != r {
			t.Errorf("%s on %s in %s: %q, want %s", k.kind, k.ground, k.m.Word(), got[k], r)
		}
	}
	for k, r := range got {
		if _, ok := want[k]; !ok {
			t.Errorf("%s on %s in %s falls short (%s), the plan measured it reads", k.kind, k.ground, k.m.Word(), r)
		}
	}
	line := `router.highlight: màu chưa đủ tương phản trên nền của trang (chữ thường cần 4.5:1): ` +
		`keyword "violet" ở chế độ sáng (#ee82ee): canvas 2.19:1, paper 2.32:1, panel 2.08:1; ` +
		`string "success" ở chế độ sáng (#0e9f6e): canvas 3.20:1, paper 3.39:1, panel 3.04:1; ` +
		`comment "ink-40" ở chế độ sáng (#707288): canvas 4.46:1, panel 4.23:1, ở chế độ tối (#767b97): canvas 4.33:1, paper 3.87:1, panel 4.16:1. ` +
		`Dùng keyword: { light: "#c400c5", dark: "violet" } (4.54:1 sáng, 6.95:1 tối), ` +
		`string: "success+30" (4.76:1 sáng, 10.97:1 tối; success là màu mặc định của string: bỏ string khỏi router.highlight thì engine chọn màu từng chế độ), ` +
		`comment: "ink-30" (5.76:1 sáng, 5.00:1 tối; màu mặc định của comment: bỏ comment khỏi router.highlight là có).`
	if got := q.HighlightWarning(highlightAt); got != line {
		t.Errorf("the line of the call:\n got  %s\n want %s", got, line)
	}
	// the colours suggested read: the call written with them falls short nowhere
	fixed, err := palettes["kitwork"].WithHighlight(map[string]HighlightColour{
		"keyword": {Pair: Color{Light: "#c400c5", Dark: "violet"}, Source: highlightAt},
		"string":  {Color: "success+30", Source: highlightAt}, "comment": {Color: "ink-30", Source: highlightAt},
	})
	if err != nil {
		t.Fatal(err)
	}
	if lines := fixed.HighlightShortfalls(); len(lines) != 0 || fixed.HighlightWarning(highlightAt) != "" {
		t.Errorf("the suggestions read: %+v", lines)
	}

	// CONTROL: the engine's own colours, written as pairs of their hexes, read everywhere
	for name, p := range palettes {
		call := map[string]HighlightColour{}
		for _, kind := range HighlightKinds() {
			look, _ := CodeLookOf(kind)
			if look.Share > 0 || accentKind(kind) {
				continue
			}
			light, _ := p.Paint(look.Color, LightMode)
			dark, _ := p.Paint(look.Color, DarkMode)
			call[kind] = HighlightColour{Pair: Color{Light: light.Hex(), Dark: dark.Hex()}, Source: highlightAt}
		}
		if len(call) != 15 {
			t.Fatalf("CONTROL: %d kinds written, want the 15 with a colour of their own", len(call))
		}
		q, err := p.WithHighlight(call)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if lines := q.HighlightShortfalls(); len(lines) != 0 {
			t.Errorf("CONTROL: %s: the engine's colours read everywhere, got %+v", name, lines)
		}
	}
	brand, _ := palettes["Kit Design"].WithHighlight(map[string]HighlightColour{"keyword": {Color: "brand", Source: highlightAt}})
	if lines := brand.HighlightShortfalls(); len(lines) == 0 || lines[0].Mode != LightMode {
		t.Errorf("CONTROL: brand itself falls short on Kit Design in light, got %+v", lines)
	}
}

// A call's line is its own: every kind the call writes, measured on the route's palette, whether a nearer
// folder's call colours the kind too or not, so a branch's pages tell the site's call the line the site's
// pages tell, and the terminal says it once. A colour to write that is the kind's default says what
// leaving the kind out gives: the engine's colour, or, when a farther folder's call sets the kind, that
// call's colour. CONTROL: the site's call alone has the same line; the branch's call alone is told the
// engine chooses.
func TestHighlightLineIsTheCallsOwn(t *testing.T) {
	kit := codePalettes(t)["Kit Design"]
	site := Source{File: "router.work", Line: 6, Column: 48}
	branch := Source{File: "kho/router.work", Line: 2, Column: 47}
	for _, c := range []struct {
		site, branch map[string]HighlightColour
		shows        string
	}{
		{ // the branch colours keyword: the site's keyword is still the site's call's
			map[string]HighlightColour{"keyword": {Color: "violet", Source: site}, "comment": {Color: "ink-40", Source: site}},
			map[string]HighlightColour{"keyword": {Color: "brand+40", Source: branch}},
			`keyword "violet" ở chế độ sáng`,
		},
		{ // the branch colours both: the site's call shows on no kind of the branch, its line stays
			map[string]HighlightColour{"keyword": {Color: "violet", Source: site}, "comment": {Color: "ink-20", Source: site}},
			map[string]HighlightColour{"keyword": {Color: "brand", Source: branch}, "comment": {Color: "ink-40", Source: branch}},
			`keyword "violet" ở chế độ sáng`,
		},
		{ // a pair is measured as the call writes it, not as the branch's pair that wins there
			map[string]HighlightColour{"function": {Pair: Color{Light: "#ee82ee", Dark: "#79c0ff"}, Source: site}},
			map[string]HighlightColour{"function": {Pair: Color{Light: "#0550ae", Dark: "#79c0ff"}, Source: branch}},
			`function { light: "#ee82ee", dark: "#79c0ff" } ở chế độ sáng (#ee82ee)`,
		},
	} {
		alone, err := kit.WithHighlight(c.site) // CONTROL
		if err != nil {
			t.Fatal(err)
		}
		want := alone.HighlightWarning(site)
		if !strings.Contains(want, c.shows) {
			t.Fatalf("CONTROL: the site's call alone names %s: %q", c.shows, want)
		}
		q, err := kit.WithHighlight(c.site, c.branch)
		if err != nil {
			t.Fatal(err)
		}
		if got := q.HighlightWarning(site); got != want {
			t.Errorf("the site's call has one line on its pages and the branch's:\n got  %s\n want %s", got, want)
		}
	}

	q, err := kit.WithHighlight(
		map[string]HighlightColour{"keyword": {Color: "violet", Source: site}, "comment": {Color: "ink-20", Source: site}},
		map[string]HighlightColour{"keyword": {Color: "brand", Source: branch}, "comment": {Color: "ink-40", Source: branch}})
	if err != nil {
		t.Fatal(err)
	}
	line := q.HighlightWarning(branch)
	for _, want := range []string{
		`keyword: "brand+5" (4.65:1 sáng, 5.47:1 tối; brand là màu mặc định của keyword, nhưng bỏ keyword ở đây thì trang theo keyword "violet" của router.work:6:48)`,
		`comment: "ink-30" (5.92:1 sáng, 5.06:1 tối; màu mặc định của comment, nhưng bỏ comment ở đây thì trang theo comment "ink-20" của router.work:6:48).`,
	} {
		if !strings.Contains(line, want) {
			t.Errorf("leaving a kind out of the branch's call gives the site's colour:\n want %s\n in   %s", want, line)
		}
	}
	if strings.Contains(line, "khỏi router.highlight") {
		t.Errorf("the site sets keyword and comment: leaving them out of the branch never gives the engine's colour:\n%s", line)
	}
	own, _ := kit.WithHighlight(map[string]HighlightColour{"keyword": {Color: "brand", Source: branch}, "comment": {Color: "ink-40", Source: branch}}) // CONTROL
	for _, want := range []string{
		`keyword: "brand+5" (4.65:1 sáng, 5.47:1 tối; brand là màu mặc định của keyword: bỏ keyword khỏi router.highlight thì engine chọn màu từng chế độ)`,
		`comment: "ink-30" (5.92:1 sáng, 5.06:1 tối; màu mặc định của comment: bỏ comment khỏi router.highlight là có).`,
	} {
		if !strings.Contains(own.HighlightWarning(branch), want) {
			t.Errorf("CONTROL: with no call above, leaving a kind out gives the engine's colour:\n want %s\n in   %s", want, own.HighlightWarning(branch))
		}
	}
}

// colors.<hash>.css is the colour chain's own: router.highlight writes nothing into it (its pairs are the
// page's, Rules.CSS), so its name stays. CONTROL: another router.colors is another file.
func TestHighlightLeavesColorsFileAlone(t *testing.T) {
	for name, p := range codePalettes(t) {
		q, err := p.WithHighlight(highlightCall(highlightAt))
		if err != nil {
			t.Fatal(err)
		}
		if got, want := string(Colors(q).CSS), string(Colors(p).CSS); got != want {
			t.Errorf("%s: colors.css changed with router.highlight:\n got  %s\n want %s", name, got, want)
		}
	}
	other, _ := NewPalette(true, map[string]Color{"brand": {Light: "#2563eb"}}) // CONTROL
	kit, _ := NewPalette(true)
	if string(Colors(other).CSS) == string(Colors(kit).CSS) {
		t.Error("CONTROL: another router.colors is another colors.css")
	}
}
