package stylesheet

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// router.highlight() for .ui pages (decision of 13/10/2026, "vâng làm đi"): a site's router.work may
// choose the colour of each kind of code token, router.highlight({ keyword: "violet", comment: "ink-40",
// "function": { light: "#0550ae", dark: "#79c0ff" } }). The keys are the closed set of kinds (HighlightKinds,
// every kind of jit/highlight but plain); a value is what color= takes (a token, -N, +N, /N, white, black,
// a named colour, never a gradient) or the kind's own colour in each mode, { light, dark }, each side
// "#rgb", "#rrggbb" or a named colour, as a token of router.colors and a stop of router.gradients are
// written (decision D2). inserted and deleted take an accent: the line's fill is the accent 85% lighter,
// its text the badge's engine colour on it (BadgeTint). A kind left out keeps its engine colour
// (codeLooks, --kitwork-code-<role>). Only the colour changes: a comment stays italic, a heading bold, the
// prompt out of a selection. A kind the author colours is measured on canvas, paper and panel in each
// mode once per route (HighlightShortfalls), and told at the call in router.work, one line a call
// (HighlightWarning); under 4.5:1 is a warning, an error under router.contrast("strict").
//
// router.kitwork.js keeps the template's router.highlight (a named theme and per-role classes): which
// grammar a call reads is the folder's router file's (work/router_ui_highlight.go).

// highlightTag marks a colour read as a kind's colour of router.highlight, not an attribute: like a
// gradient's stop, it is a colour only, so its message names no gradient (colourOnly).
const highlightTag = "router.highlight"

// highlightPrefix starts the engine's name for a kind's { light, dark } pair (HighlightVariable): no
// token starts with kitwork- (checkToken), so no author's value names it.
const highlightPrefix = "kitwork-highlight-"

// Source is where a call of router.highlight is written: the router file relative to the site root, and
// the line and column the VM gives the call.
type Source struct {
	File         string
	Line, Column int
}

// String is file:line:col, or the file alone when no line is known.
func (s Source) String() string {
	if s.Line <= 0 {
		return s.File
	}
	return s.File + ":" + strconv.Itoa(s.Line) + ":" + strconv.Itoa(s.Column)
}

// HighlightColour is one kind's colour as router.highlight writes it: Color, a colour as color= takes it,
// or Pair, its own colour in light and in dark; Source is the call that set it.
type HighlightColour struct {
	Color  string
	Pair   Color
	Source Source
}

func (h HighlightColour) paired() bool { return h.Pair != (Color{}) }

// written is the colour as the author wrote it: "violet", { light: "#0550ae", dark: "#79c0ff" }.
func (h HighlightColour) written() string {
	if h.paired() {
		return `{ light: "` + strings.TrimSpace(h.Pair.Light) + `", dark: "` + strings.TrimSpace(h.Pair.Dark) + `" }`
	}
	return `"` + strings.TrimSpace(h.Color) + `"`
}

// highlightKinds are the kinds router.highlight takes, in the order its messages list them.
var highlightKinds = []string{"keyword", "type", "string", "regular-expression", "number", "constant", "attribute",
	"function", "property", "variable", "tag", "comment", "operator", "punctuation", "heading", "inserted", "deleted",
	"prompt"}

// HighlightKinds lists the kinds router.highlight takes, in the order its messages list them: every kind
// of jit/highlight but plain, the kinds that have a look (CodeKinds).
func HighlightKinds() []string { return append([]string(nil), highlightKinds...) }

// HighlightVariable is the engine colour a kind's { light, dark } pair is painted as, the name of its
// custom property without --: kitwork-highlight-function.
func HighlightVariable(kind string) string { return highlightPrefix + kind }

// accentKind reports whether a kind takes an accent, its fill and text made from it: inserted, deleted.
func accentKind(kind string) bool { return kind == "inserted" || kind == "deleted" }

// defaultAccent is the accent an accent kind's engine look is made from: success for inserted, danger
// for deleted.
func defaultAccent(kind string) string {
	return strings.TrimSuffix(codeLooks[kind].Background, "-"+strconv.Itoa(TintStep))
}

// HighlightKey is a kind as a key of router.highlight's object is written: in quotes when it holds a
// hyphen ("regular-expression": bare, it is a subtraction), and "function" as the docs write it (the
// parser takes a word of the language before ':' as a property name too, compiler/parser.go, so a bare
// function: reads the same; no message says it must be quoted). Also the opener of a key the author wrote.
func HighlightKey(kind string) string {
	if strings.Contains(kind, "-") || kind == "function" {
		return `"` + kind + `"`
	}
	return kind
}

// shortKinds are the short names of kinds people type out of habit, each with the kind written in full.
var shortKinds = map[string]string{
	"regex": "regular-expression", "regexp": "regular-expression", "kw": "keyword", "str": "string",
	"num": "number", "const": "constant", "attr": "attribute", "fn": "function", "func": "function",
	"prop": "property", "var": "variable", "op": "operator", "punct": "punctuation", "added": "inserted",
	"addition": "inserted", "removed": "deleted", "deletion": "deleted", "hunk": "heading",
}

// kindLetters is every kind by its letters (letters): regularExpression and regular_expression are
// regular-expression.
var kindLetters = func() map[string]string {
	out := map[string]string{}
	for _, kind := range highlightKinds {
		out[letters(kind)] = kind
	}
	return out
}()

// plainKey is what router.highlight says of plain, the one kind it does not take.
const plainKey = "plain là chữ thường của khối mã, không có màu riêng: nó theo màu chữ của <code>; viết color= trên <code> (hay trên thẻ chủ của terminal)"

// CheckHighlightKey reads a key of router.highlight. problem is "" for a kind it takes; otherwise what is
// wrong, in the author's words, without the call's opener: plain (and Plain), a kind in other letters
// (regularExpression), a short name (regex), the nearest kinds of a typo (keywrod), each as a key is
// written (HighlightKey), or every kind.
// written is the value as the author wrote it, for the example to write ("success"; … for any other).
// meant is the kind the key stands for when one does.
func CheckHighlightKey(key, written string) (meant, problem string) {
	if _, ok := codeLooks[key]; ok {
		return key, ""
	}
	word := letters(key)
	if word == "plain" {
		return "", plainKey
	}
	if kind, ok := kindLetters[word]; ok {
		say := "tên loại chữ là " + kind + ", chữ thường"
		if strings.Contains(kind, "-") {
			say += " nối bằng gạch, viết trong dấu nháy"
		}
		return kind, say + ": router.highlight({ " + HighlightKey(kind) + ": " + written + " })"
	}
	if kind, ok := shortKinds[word]; ok {
		return kind, "tên loại chữ viết đủ chữ: router.highlight({ " + HighlightKey(kind) + ": " + written + " })"
	}
	if near := nearestKinds(key); len(near) > 0 {
		for i, kind := range near {
			near[i] = HighlightKey(kind) // copied as written: "regular-expression" bare is a subtraction
		}
		return "", "không có loại chữ \"" + key + "\"; gần nhất: " + strings.Join(near, ", ")
	}
	return "", "không có loại chữ \"" + key + "\". Loại chữ: " + strings.Join(highlightKinds, ", ")
}

// nearestKinds are the kinds fewest edits from a key that is none, within a third of its length (two at
// least), at most three, in the order of HighlightKinds.
func nearestKinds(key string) []string {
	word := strings.ToLower(key)
	limit := max(len(word)/3, 2)
	best := limit + 1
	var near []string
	for _, kind := range highlightKinds {
		switch d := editDistance(word, kind, best); {
		case d < best:
			best, near = d, []string{kind}
		case d == best && d <= limit:
			near = append(near, kind)
		}
	}
	if len(near) > 3 {
		near = near[:3]
	}
	return near
}

// CheckHighlight checks what of a kind's colour can be read without a palette, as router.work runs:
// a colour that is written (an empty one is no colour), no #hex alone (a hex is one mode's: a pair; a #
// that is no hex said so at once), a pair with both sides, each "#rgb", "#rrggbb" or a named colour, and
// no pair and no #hex for inserted or deleted, which take an accent. Whether a colour is one the route's
// palette names is WithHighlight's.
func CheckHighlight(kind string, c HighlightColour) error {
	key := HighlightKey(kind)
	if c.paired() {
		at := "router.highlight({ " + key + ": … }): "
		if accentKind(kind) {
			return fmt.Errorf("%s%s", at, accentPairProblem(kind, c.Pair, "cặp"))
		}
		light, dark := strings.TrimSpace(c.Pair.Light), strings.TrimSpace(c.Pair.Dark)
		for _, side := range []struct{ mode, value string }{{"light", light}, {"dark", dark}} {
			if side.value == "" {
				shown := map[string]string{"light": light, "dark": dark}
				for mode, v := range shown {
					if v == "" {
						shown[mode] = "#…"
					}
				}
				return fmt.Errorf(`%scặp màu thiếu %s: viết cả hai, { light: "%s", dark: "%s" }, như một token của router.colors`, at, side.mode, shown["light"], shown["dark"])
			}
			if problem := highlightSideProblem(kind, side.value); problem != "" {
				return fmt.Errorf(`%s%s là "%s": %s`, at, side.mode, side.value, problem)
			}
		}
		return nil
	}
	v := strings.TrimSpace(c.Color)
	at := "router.highlight({ " + key + ": \"" + v + "\" }): "
	switch {
	case v == "":
		return fmt.Errorf("%smàu trống; viết một màu như color= nhận (brand, ink-30, violet), hay bỏ %s để giữ màu engine chọn", at, kind)
	case strings.HasPrefix(v, "#") && accentKind(kind):
		var light string // a # that is no colour is not put into router.colors
		if hexColor.MatchString(v) {
			light = v
		}
		return fmt.Errorf("%s%s", at, accentPairProblem(kind, Color{Light: light}, "#hex"))
	case strings.HasPrefix(v, "#") && !hexColor.MatchString(v):
		return fmt.Errorf(`%s"%s" không phải màu: màu #hex là "#rgb" hay "#rrggbb", màu của một chế độ, viết trong một cặp, %s: { light: "#7c3aed", dark: "#bb9af7" }; một màu cho cả hai chế độ là một token (router.colors) hay một màu có tên`, at, v, key)
	case strings.HasPrefix(v, "#"):
		return fmt.Errorf(`%smàu #hex là màu của một chế độ: viết một cặp cho sáng và tối, %s: { light: "%s", dark: "#…" } (hai bên "%s" khi tối cũng là màu ấy); một màu cho cả hai chế độ là một token (router.colors) hay một màu có tên`, at, key, v, v)
	}
	return nil
}

// highlightSideProblem is what is wrong with one side of a kind's pair, in the author's words: "" for
// "#rgb", "#rrggbb" or a named colour.
func highlightSideProblem(kind, side string) string {
	if _, ok := NamedColour(side); ok {
		return ""
	}
	if _, why, ok := namedOtherwise(side); ok {
		return why // navy-blue is navy
	}
	if hexColor.MatchString(side) {
		return ""
	}
	return `một bên của cặp là "#rgb", "#rrggbb" hay một màu có tên (tomato, cerulean…); một token (brand, ink-30) đã có màu sáng và tối của nó, nên viết thẳng ` +
		HighlightKey(kind) + `: "brand" thay cho cặp`
}

// accentPairProblem refuses a pair or a #hex (refused names which) for inserted or deleted: each takes an
// accent, and an accent of the author's own in each mode is declared in router.colors, its pair as
// written there (a side not written is #…).
func accentPairProblem(kind string, pair Color, refused string) string {
	name, accent := "added", defaultAccent(kind)
	if kind == "deleted" {
		name = "removed"
	}
	light, dark := strings.TrimSpace(pair.Light), strings.TrimSpace(pair.Dark)
	if light == "" {
		light = "#…"
	}
	if dark == "" {
		dark = "#…"
	}
	return kind + " nhận một màu nhấn, không nhận " + refused + ": nền của dòng là màu ấy nhạt " + strconv.Itoa(TintStep) + " (" + accent + "-" + strconv.Itoa(TintStep) +
		"), chữ là màu ấy engine làm đậm tới khi đọc được trên nền đó ở mỗi chế độ. Muốn màu riêng mỗi chế độ thì khai một màu nhấn trong router.colors (router.colors({ " +
		name + `: { light: "` + light + `", dark: "` + dark + `" } })) rồi viết ` + kind + `: "` + name + `"`
}

// ---- the palette's code colours ----

// WithHighlight is the palette with a site's router.highlight: chain holds the calls of each folder from
// the site root down to the route, each kind with the call that set it, nearest last, and the nearest
// folder wins per kind. Every key is a kind (CheckHighlightKey) and every colour reads (CheckHighlight);
// then, on this palette: a colour is no gradient, it is one color= takes (Table.colour, its message after
// the call's place), and inserted or deleted name an accent that grows a label (BadgeTint), or the
// default one (success, danger), which keeps the engine's colours. The shortfalls of contrast on the
// page's grounds are worked out here, once a route (HighlightShortfalls), for every colour each call of
// the chain writes, so a call's line is its own (highlightWarnings).
func (p *Palette) WithHighlight(chain ...map[string]HighlightColour) (*Palette, error) {
	merged := map[string]HighlightColour{}
	for _, layer := range chain {
		for kind, c := range layer {
			merged[kind] = c
		}
	}
	if len(merged) == 0 && len(p.highlight) == 0 {
		return p, nil
	}
	q := *p
	q.highlight = merged
	q.highlightLines = nil
	q.shortfalls = nil
	for _, kind := range sortedKeys(merged) {
		if _, problem := CheckHighlightKey(kind, "…"); problem != "" {
			return nil, fmt.Errorf("%srouter.highlight({ %s: … }): %s", opener(merged[kind].Source), HighlightKey(kind), problem)
		}
	}
	table := NewTable(&q)
	for _, kind := range highlightKinds {
		if c, ok := merged[kind]; ok {
			if err := q.readHighlight(table, kind, c); err != nil {
				return nil, err
			}
		}
	}
	q.shortfalls = q.highlightShortfalls(table, chain)
	q.highlightLines = q.highlightWarnings()
	return &q, nil
}

// readHighlight checks a kind's colour on this palette, its message after the call's place: it reads
// without a palette (CheckHighlight); a colour is no gradient and is one color= takes (Table.colour);
// inserted or deleted name an accent that grows a label (BadgeTint), or their default one.
func (p *Palette) readHighlight(table *Table, kind string, c HighlightColour) error {
	if err := CheckHighlight(kind, c); err != nil {
		return fmt.Errorf("%s%v", opener(c.Source), err)
	}
	if c.paired() {
		return nil
	}
	v := strings.TrimSpace(c.Color)
	at := opener(c.Source) + "router.highlight({ " + HighlightKey(kind) + ": \"" + v + "\" }): "
	if base, _, _ := strings.Cut(v, "/"); p.isGradient(base) || p.isGradient(v) {
		return fmt.Errorf("%s%s là một gradient; màu của một loại chữ là một màu (token, nhạt hơn token-N, đậm hơn token+N, độ hiện token/N, white, black hay màu có tên)", at, v)
	}
	if _, _, _, _, problem := table.colour(Intent{Tag: highlightTag, Name: "color", Value: v}, v); problem != nil {
		return fmt.Errorf("%s%s", at, strings.TrimPrefix(problem.Message, said(Intent{Name: "color"}, v)))
	}
	if accentKind(kind) && v != defaultAccent(kind) {
		if _, _, family := table.BadgeTint(v); !family {
			return fmt.Errorf("%s%s", at, p.accentProblem(kind, v))
		}
	}
	return nil
}

// opener is a message's opening place, file:line:col: , or nothing when the call has no place.
func opener(s Source) string {
	if s.File == "" {
		return ""
	}
	return s.String() + ": "
}

// accentProblem is what inserted or deleted are told of a value that is no accent that grows a label:
// a fixed colour has no tinted step, an accent takes no operator here (its fill and text are the
// engine's), and ink, a surface or a label is no accent. The accents listed are the route's palette's.
func (p *Palette) accentProblem(kind, v string) string {
	line := "dòng thêm"
	if kind == "deleted" {
		line = "dòng xoá"
	}
	accents := ". Màu nhấn của trang: " + strings.Join(p.labelledAccents(), ", ")
	c, ok := SplitColour(v)
	switch {
	case ok && fixedColors[c.Base] != "":
		return kind + " là màu nhấn của " + line + ": nền là màu ấy nhạt " + strconv.Itoa(TintStep) + ", chữ là màu ấy đậm lên tới khi đọc được; " + c.Base +
			" là màu cố định ở sáng và tối, không có bậc nhạt" + accents
	case ok && c.Fixed():
		return kind + " là màu nhấn của " + line + ": nền là màu ấy nhạt " + strconv.Itoa(TintStep) + ", chữ là màu ấy đậm lên tới khi đọc được; " + c.Base +
			" là màu có tên, cố định ở sáng và tối, không có bậc nhạt" + accents
	case ok && accent(c.Base) && p.Has(c.Base+".on"):
		return kind + ` nhận màu nhấn như đã khai, không phép: viết ` + kind + `: "` + c.Base + `"; nền là ` + c.Base + "-" + strconv.Itoa(TintStep) +
			", chữ engine làm đậm từ " + c.Base + " tới khi đọc được"
	}
	base := v
	if ok {
		base = c.Base
	}
	return kind + " là màu nhấn của " + line + "; " + base + " không phải màu nhấn (ink, canvas, paper, panel, line là chữ và nền, X.on là chữ trên nền X)" + accents
}

// labelledAccents are the accents of the palette that grow a label (.on), sorted: what inserted and
// deleted take.
func (p *Palette) labelledAccents() []string {
	var out []string
	for _, name := range p.names {
		if accent(name) && p.Has(name+".on") {
			out = append(out, name)
		}
	}
	return out
}

// Highlight is a kind's colour as router.highlight sets it on this palette's route; false when it does
// not, and for inserted or deleted written their default accent, which keeps the engine's colours.
func (p *Palette) Highlight(kind string) (HighlightColour, bool) {
	c, ok := p.highlight[kind]
	if !ok || !ownColour(kind, c) {
		return HighlightColour{}, false
	}
	return c, true
}

// ownColour reports whether a kind's colour is the author's: false for inserted or deleted written their
// default accent, which keeps the engine's colours.
func ownColour(kind string, c HighlightColour) bool {
	return !accentKind(kind) || c.paired() || strings.TrimSpace(c.Color) != defaultAccent(kind)
}

// highlightSide is the colour of one side of a kind's pair in a mode, #rrggbb (pairSide). ok is false for
// a kind with no pair.
func (p *Palette) highlightSide(kind string, m Mode) (string, bool) {
	c, ok := p.highlight[kind]
	if !ok || !c.paired() {
		return "", false
	}
	return p.pairSide(c, m)
}

// pairSide is the colour of one side of a pair in a mode, #rrggbb: the dark side in dark when the palette
// has a dark look, else the light one.
func (p *Palette) pairSide(c HighlightColour, m Mode) (string, bool) {
	side := c.Pair.Light
	if m == DarkMode && p.hasDark {
		side = c.Pair.Dark
	}
	hex, problem := pairHex(side)
	return hex, problem == ""
}

// highlightPair is a kind's pair as the custom property the page's stylesheet sets (Rules.CSS),
// --kitwork-highlight-<kind>: its light colour, and its dark one when the palette has a dark look.
func (p *Palette) highlightPair(kind string) (pairValue, bool) {
	light, ok := p.highlightSide(kind, LightMode)
	if !ok {
		return pairValue{}, false
	}
	v := pairValue{name: "--" + HighlightVariable(kind), light: light}
	if p.hasDark {
		v.dark, _ = p.highlightSide(kind, DarkMode)
	}
	return v, true
}

// highlightColours is the CSS of a kind's text and fill ("" for none) as router.highlight sets them, and
// the custom properties they read: a colour as color= writes it (a token's var(), a lighter colour's
// color-mix(), a stronger one's custom property, a named colour's hex); a pair as its custom property, or
// its hex when both sides are one colour; an accent of inserted or deleted as a badge is: the badge's
// engine colour on the accent 85% lighter.
func (t *Table) highlightColours(kind string, h HighlightColour) (colour, fill string, custom []pairValue, err error) {
	switch {
	case accentKind(kind) && !h.paired():
		fillValue, text, family := t.BadgeTint(strings.TrimSpace(h.Color))
		css, textCustom, ok := t.engineColour(text)
		if !family || !ok {
			return "", "", nil, fmt.Errorf("router.highlight({ %s: %q }): không phải màu nhấn", kind, h.Color)
		}
		fillCSS, _, _, fillCustom, p := t.colour(Intent{Name: "background", Value: fillValue}, fillValue)
		if p != nil {
			return "", "", nil, p
		}
		return css, fillCSS, append(textCustom, fillCustom...), nil
	case h.paired():
		v, ok := t.palette.highlightPair(kind)
		if !ok {
			return "", "", nil, fmt.Errorf("router.highlight({ %s: … }): cặp màu không đọc được", HighlightKey(kind))
		}
		if dark, _ := pairHex(h.Pair.Dark); dark == v.light {
			return v.light, "", nil, nil // one colour in both modes: written as it is
		}
		return "var(" + v.name + ")", "", []pairValue{v}, nil
	}
	v := strings.TrimSpace(h.Color)
	css, _, _, c, p := t.colour(Intent{Tag: highlightTag, Name: "color", Value: v}, v)
	if p != nil {
		return "", "", nil, p
	}
	return css, "", c, nil
}

// ---- contrast on the page's grounds ----

// HighlightShortfall is one colour of router.highlight short of 4.5:1 on one ground of the page in one
// mode: the kind, the call that set it, the colour as written and as the mode paints it (#rrggbb), the
// ground (canvas, paper, panel; an accent kind's own fill) and the ratio.
type HighlightShortfall struct {
	Kind    string
	Source  Source
	Written string
	Mode    Mode
	Shown   string
	Ground  string
	Ratio   float64
	// colour is the call's colour of the kind; beneath, the colour a farther folder's call sets for it,
	// which the page follows where the call leaves the kind out (nil: the engine's)
	colour  HighlightColour
	beneath *HighlightColour
}

// HighlightShortfalls are the colours of router.highlight on this palette that fall short of 4.5:1 (WCAG
// 2.2 AA, normal text: code is) on the page's grounds, canvas, paper and panel, in each mode, as the
// pixel a screen paints (an alpha composited over the ground); inserted and deleted on their own fill.
// Every colour each call down the route writes, a kind a nearer folder's call colours too still its
// call's. In the order of the chain, then HighlightKinds, then mode, then ground. Worked out once, in
// WithHighlight.
func (p *Palette) HighlightShortfalls() []HighlightShortfall {
	return append([]HighlightShortfall(nil), p.shortfalls...)
}

// highlightText is what a kind's text is painted as in a mode (a colour value Paint reads; a pair's side,
// #rrggbb, its own, not the kind's on the route) and the grounds it is measured on.
func (p *Palette) highlightText(kind string, c HighlightColour, m Mode) (string, []string) {
	switch {
	case accentKind(kind) && !c.paired():
		fill, text, _ := NewTable(p).BadgeTint(strings.TrimSpace(c.Color))
		return text, []string{fill}
	case c.paired():
		side, _ := p.pairSide(c, m)
		return side, pageGrounds
	}
	return strings.TrimSpace(c.Color), pageGrounds
}

// ratioOn is a text colour's ratio on a ground in a mode, as the pixel a screen paints; ok is false when
// the palette cannot paint either.
func (p *Palette) ratioOn(text RGBA, ground string, m Mode) (float64, bool) {
	g, ok := p.Paint(ground, m)
	if !ok {
		return 0, false
	}
	return Ratio(text.Over(g.Painted()).Painted(), g.Painted()), true
}

// highlightShortfalls measures every colour each call of the chain (the farthest folder first) writes,
// whether a nearer folder's call colours the kind too or not: a call's line then depends on the call and
// the palette alone, the same on its pages and a branch's. Each remembers the colour a farther folder's
// call sets for the kind. A colour this palette does not read is measured nowhere: where it wins, the
// route's error says it (readHighlight).
func (p *Palette) highlightShortfalls(table *Table, chain []map[string]HighlightColour) []HighlightShortfall {
	type written struct {
		source Source
		kind   string
	}
	var out []HighlightShortfall
	seen := map[written]bool{}
	farther := map[string]HighlightColour{}
	for _, layer := range chain {
		for _, kind := range highlightKinds {
			c, ok := layer[kind]
			if !ok || seen[written{c.Source, kind}] || !ownColour(kind, c) || p.readHighlight(table, kind, c) != nil {
				continue
			}
			seen[written{c.Source, kind}] = true
			var beneath *HighlightColour
			if b, ok := farther[kind]; ok && ownColour(kind, b) {
				beneath = &b
			}
			for _, m := range p.Modes() {
				text, grounds := p.highlightText(kind, c, m)
				x, ok := p.Paint(text, m)
				if !ok {
					continue
				}
				for _, ground := range grounds {
					if r, ok := p.ratioOn(x, ground, m); ok && r < NormalTextRatio {
						out = append(out, HighlightShortfall{Kind: kind, Source: c.Source, Written: c.written(), Mode: m,
							Shown: x.Hex(), Ground: ground, Ratio: r, colour: c, beneath: beneath})
					}
				}
			}
		}
		for kind, c := range layer {
			farther[kind] = c
		}
	}
	return out
}

// HighlightWarning is the line a call of router.highlight is told, at the call, when a colour it sets
// falls short of contrast on the page's grounds (HighlightShortfalls): every such colour the call writes,
// whether a nearer folder's call colours the kind too or not, so the line is the same on every page of
// one palette, then what reads. "" when none of its colours falls short.
func (p *Palette) HighlightWarning(s Source) string { return p.highlightLines[s] }

// highlightWarnings writes the line of each call with a shortfall: each kind as written, the mode, its
// colour there and each ground with its ratio, then the colour to write for each kind (highlightAdvice).
func (p *Palette) highlightWarnings() map[Source]string {
	if len(p.shortfalls) == 0 {
		return nil
	}
	type kindShort struct {
		kind    string
		colour  HighlightColour
		beneath *HighlightColour
		byMode  map[Mode][]HighlightShortfall
		modes   []Mode
	}
	calls := map[Source][]*kindShort{}
	var order []Source
	for _, s := range p.shortfalls {
		list, seen := calls[s.Source]
		if !seen {
			order = append(order, s.Source)
		}
		if len(list) == 0 || list[len(list)-1].kind != s.Kind {
			list = append(list, &kindShort{kind: s.Kind, colour: s.colour, beneath: s.beneath, byMode: map[Mode][]HighlightShortfall{}})
			calls[s.Source] = list
		}
		k := list[len(list)-1]
		if len(k.byMode[s.Mode]) == 0 {
			k.modes = append(k.modes, s.Mode)
		}
		k.byMode[s.Mode] = append(k.byMode[s.Mode], s)
	}
	out := map[Source]string{}
	for _, source := range order {
		var said, use, own []string
		for _, k := range calls[source] {
			c := k.colour
			var modes []string
			for _, m := range k.modes {
				list := k.byMode[m]
				var grounds []string
				for _, s := range list {
					grounds = append(grounds, s.Ground+" "+RatioText(s.Ratio, NormalTextRatio)+":1")
				}
				modes = append(modes, "ở chế độ "+m.Word()+" ("+list[0].Shown+"): "+strings.Join(grounds, ", "))
			}
			said = append(said, k.kind+" "+c.written()+" "+strings.Join(modes, ", "))
			if write, ok := p.highlightAdvice(k.kind, c, k.beneath); ok {
				use = append(use, write)
			} else {
				own = append(own, p.highlightNoAdvice(k.kind, c))
			}
		}
		line := "router.highlight: màu chưa đủ tương phản trên nền của trang (chữ thường cần 4.5:1): " + strings.Join(said, "; ") + "."
		if len(use) > 0 {
			line += " Dùng " + strings.Join(use, ", ") + "."
		}
		for _, o := range own {
			line += " " + o
		}
		out[source] = line
	}
	return out
}

// highlightNoAdvice is what a kind is told when no colour the engine tries reads on every ground in
// every mode: for inserted or deleted, whose text the engine already made as strong as +100 on the fill,
// another accent or the accent declared anew; for any other kind, a colour of the author's own each mode.
func (p *Palette) highlightNoAdvice(kind string, c HighlightColour) string {
	if accentKind(kind) && !c.paired() {
		accent, _, _ := strings.Cut(strings.TrimSpace(c.Color), "/")
		return kind + ": chữ engine đã thử làm đậm " + accent + " tới " + accent + "+100 mà chưa đọc được trên " + accent + "-" + strconv.Itoa(TintStep) +
			": khai lại " + accent + " trong router.colors hay chọn màu nhấn khác."
	}
	return kind + ": không bậc đậm hơn nào tới +100 đọc được ở mọi chế độ: chọn một cặp { light, dark } đủ tương phản trên canvas, paper và panel."
}

// highlightAdvice is the colour to write for a kind short of contrast, with its worst ratio in each mode:
//   - a pair, or a fixed colour (white, black, a named colour): the mode that reads keeps its side, the
//     other is that side made stronger (Stronger, rounded to 8 bits a channel, as an X+N's property is)
//     by the smallest multiple of 5 that reads: { light: "#c400c5", dark: "violet" };
//   - a token: lighter X-N steps back toward X by fives, then X itself; then X+M, the smallest multiple
//     of 5 above N that reads in every mode, one value for both as the author's own +N is; when none up to
//     +100 does, a pair, each mode the hex of its own least step;
//   - an alpha /N is dropped first: the colour solid when that reads.
//
// A token that is the kind's default role says so: left out, the engine chooses its colour per mode, or
// the page follows beneath, the colour a farther folder's call sets for the kind (defaultNote).
// false for inserted and deleted, and when nothing reads.
func (p *Palette) highlightAdvice(kind string, c HighlightColour, beneath *HighlightColour) (string, bool) {
	if accentKind(kind) && !c.paired() {
		return "", false
	}
	key := HighlightKey(kind)
	if c.paired() {
		write, worst, ok := p.strongerPair(c.Pair.Light, c.Pair.Dark)
		if !ok {
			return "", false
		}
		return key + ": " + write + " (" + p.worstText(worst) + ")", true
	}
	v := strings.TrimSpace(c.Color)
	cv, ok := SplitColour(v)
	if !ok {
		return "", false
	}
	if cv.Translucent {
		solid := cv
		solid.Alpha, solid.Translucent = 100, false
		if worst, ok := p.readsEverywhere(solid.String()); ok {
			return key + ": \"" + solid.String() + "\" (" + p.worstText(worst) + p.defaultNote(kind, solid, beneath) + ")", true
		}
		cv = solid
	}
	if cv.Fixed() {
		write, worst, ok := p.strongerPair(cv.Base, cv.Base)
		if !ok {
			return "", false
		}
		return key + ": " + write + " (" + p.worstText(worst) + ")", true
	}
	var tries []ColourValue
	if cv.Op == '-' {
		for n := (cv.Amount - 1) / 5 * 5; n >= 5; n -= 5 {
			tries = append(tries, ColourValue{Base: cv.Base, Op: '-', Amount: n, Alpha: 100})
		}
		tries = append(tries, ColourValue{Base: cv.Base, Alpha: 100})
	}
	from := 5
	if cv.Op == '+' {
		from = (cv.Amount/5 + 1) * 5
	}
	for n := from; n <= 100; n += 5 {
		tries = append(tries, ColourValue{Base: cv.Base, Op: '+', Amount: n, Alpha: 100})
	}
	for _, try := range tries {
		if worst, ok := p.readsEverywhere(try.String()); ok {
			return key + ": \"" + try.String() + "\" (" + p.worstText(worst) + p.defaultNote(kind, try, beneath) + ")", true
		}
	}
	// no one step reads in both modes: each mode its own least step, as a pair of hexes
	sides := map[Mode]string{}
	for _, m := range p.Modes() {
		n, ok := p.readableIn(cv.Base, pageGrounds, NormalTextRatio, 0, m)
		if !ok {
			return "", false
		}
		x, _ := p.Paint(spell(cv.Base, n), m)
		sides[m] = x.Hex()
	}
	dark := sides[DarkMode]
	if dark == "" {
		dark = sides[LightMode]
	}
	write, worst, ok := p.strongerPair(sides[LightMode], dark)
	if !ok {
		return "", false
	}
	return key + ": " + write + " (" + p.worstText(worst) + p.defaultNote(kind, cv, beneath) + ")", true
}

// readsEverywhere reports whether a colour value reads 4.5:1 on canvas, paper and panel in every mode,
// with its worst ratio in each.
func (p *Palette) readsEverywhere(v string) (map[Mode]float64, bool) {
	worst := map[Mode]float64{}
	for _, m := range p.Modes() {
		x, ok := p.Paint(v, m)
		if !ok {
			return nil, false
		}
		w, reads := p.worstOn(x, m)
		if !reads {
			return nil, false
		}
		worst[m] = w
	}
	return worst, true
}

// worstOn is a colour's lowest ratio on canvas, paper and panel in a mode, and whether it reads 4.5:1 on
// all three.
func (p *Palette) worstOn(x RGBA, m Mode) (float64, bool) {
	worst := 21.0
	for _, ground := range pageGrounds {
		r, ok := p.ratioOn(x, ground, m)
		if !ok {
			return 0, false
		}
		worst = min(worst, r)
	}
	return worst, worst >= NormalTextRatio
}

// strongerPair is a pair of sides (each "#rrggbb" or a named colour) each mode reads with: a side that
// reads on canvas, paper and panel in its mode stays as written; one that does not is made stronger in
// that mode by the smallest multiple of 5 that does, as #rrggbb. On a palette with no dark look the dark
// side stays as written. ok is false when a side reads at no step up to +100.
func (p *Palette) strongerPair(light, dark string) (string, map[Mode]float64, bool) {
	sides := map[Mode]string{LightMode: light, DarkMode: dark}
	worst := map[Mode]float64{}
	for _, m := range p.Modes() {
		hex, problem := pairHex(sides[m])
		if problem != "" {
			return "", nil, false
		}
		x, _ := ParseHex(hex)
		if w, ok := p.worstOn(x, m); ok {
			worst[m] = w
			continue
		}
		canvas, _ := p.token("canvas", m)
		found := false
		for n := 5; n <= 100; n += 5 {
			y, _ := ParseHex(Stronger(x, canvas, n).Hex())
			if w, ok := p.worstOn(y, m); ok {
				sides[m], worst[m], found = y.Hex(), w, true
				break
			}
		}
		if !found {
			return "", nil, false
		}
	}
	return `{ light: "` + sides[LightMode] + `", dark: "` + sides[DarkMode] + `" }`, worst, true
}

// worstText is the worst ratio of a colour in each mode: 4.54:1 sáng, 6.95:1 tối.
func (p *Palette) worstText(worst map[Mode]float64) string {
	var out []string
	for _, m := range p.Modes() {
		out = append(out, RatioText(worst[m], NormalTextRatio)+":1 "+m.Word())
	}
	return strings.Join(out, ", ")
}

// defaultNote says when a colour to write is the kind's default: the engine's own value (comment's
// ink-30, nothing to write), or a token that is the kind's role (keyword's brand), which the engine
// makes stronger per mode on its own when the kind is left out. When a farther folder's call sets the
// kind (beneath), leaving it out gives that call's colour, not the engine's, and the note says so.
func (p *Palette) defaultNote(kind string, v ColourValue, beneath *HighlightColour) string {
	look := codeLooks[kind]
	var note, engine string
	if role, ok := engineRoles[look.Color]; v.String() == look.Color {
		note, engine = "; màu mặc định của "+kind, ": bỏ "+kind+" khỏi router.highlight là có"
	} else if ok && role.token == v.Base {
		note, engine = "; "+v.Base+" là màu mặc định của "+kind, ": bỏ "+kind+" khỏi router.highlight thì engine chọn màu từng chế độ"
	} else {
		return ""
	}
	if beneath == nil {
		return note + engine
	}
	note += ", nhưng bỏ " + kind + " ở đây thì trang theo " + kind + " " + beneath.written()
	if beneath.Source.File != "" {
		note += " của " + beneath.Source.String()
	}
	return note
}

// RatioText writes a ratio with two decimals, never rounded up to the ratio it misses (4.499 is 4.49).
func RatioText(r, need float64) string {
	s := strconv.FormatFloat(r, 'f', 2, 64)
	if f, _ := strconv.ParseFloat(s, 64); r < need && f >= need {
		s = strconv.FormatFloat(math.Floor(r*100)/100, 'f', 2, 64)
	}
	return s
}

// SortSources sorts places by file, line and column.
func SortSources(list []Source) {
	sort.Slice(list, func(i, j int) bool {
		a, b := list[i], list[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Column < b.Column
	})
}
