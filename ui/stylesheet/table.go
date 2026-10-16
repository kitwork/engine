package stylesheet

import (
	"math"
	"regexp"
	"strconv"
	"strings"
)

// Intent is one presentation attribute as the .ui source writes it, with the condition it holds
// under. Width and State are the width and state words of its prefix, as written: "medium",
// "large,wide", "hover", "hover,focus" (target words are decided at build and never reach a
// stylesheet). Tag is the .ui tag the attribute sits on (badge, text, image…): it adds the rules an
// element of that kind needs and never changes the class name. Name and Value are the attribute.
type Intent struct {
	Width, State string
	Tag          string
	Name, Value  string
}

// Problem is a value the table refuses, or one it reads and tells the author of (decision D,
// 12/10/2026). Its message, in the .ui author's language, names the word as written and the words the
// attribute takes; the compiler reports it at the attribute's file:line:col. Suggestion, for an
// editor's fix, replaces what is wrong when one replacement says it: the value when the value is wrong
// (the nearest step "3", the new word "small", "white/50"), the attribute's name when the name is wrong
// ("padding-start"), and name="value" when both change (space="4" is gap="4").
//
// Warning says the page renders as written and the author is only told: a number outside its range
// that is drawn as the number written (columns="13"), a length off the spacing scale drawn as written
// (padding="17px"), a .ui word that does nothing where it is written (fit on a box), a value that
// writes the default (width="auto"), a state written twice.
// Table.Read hands such problems back beside the class; Table.Class, for a caller with no way to tell
// the author, refuses with them. A word the table does not understand is never a warning: a typo never
// vanishes.
type Problem struct {
	Name, Value string
	Suggestion  string
	Message     string
	Warning     bool
}

func (p *Problem) Error() string { return p.Message }

func problem(in Intent, message string) *Problem {
	return &Problem{Name: in.Name, Value: in.Value, Message: message}
}

// warning is a problem the page renders through: the value is read, the author is told.
func warning(in Intent, message string) *Problem {
	p := problem(in, message)
	p.Warning = true
	return p
}

// Table turns intents into classes and rules for one colour chain: the palette it validates colours
// against. Immutable; safe across goroutines (each page records into its own Rules).
type Table struct {
	palette *Palette
}

// NewTable is the table of a colour chain. A nil palette is Kit Design's default palette alone.
func NewTable(p *Palette) *Table {
	if p == nil {
		p, _ = NewPalette(false)
	}
	return &Table{palette: p}
}

// Palette is the colour chain the table validates colours against.
func (t *Table) Palette() *Palette { return t.palette }

// reading is what the table makes of one valid value.
type reading struct {
	declarations string       // the class's own rule
	scoped       []scopedRule // rules for an element of one kind, written right after the class's own
	reboot       bool         // the class is one reboot.css styles (flex-row, flex-column): no page rule
	none         bool         // no class at all: the HTML element says it (strikethrough is <s>)
	// inert is a value that does nothing where it is written (fit on a box, align="top" on a text,
	// width="auto"): no class and no rule under any condition, and a warning says so
	inert bool
	// transition is what a state change of the class animates, in a rule that holds under no
	// condition (Rules.record): scale and rotate animate together, whichever the class sets
	transition []string
	weight     int // the font weight the rule sets, for fonts.<hash>.css
	// handover is what a background class also writes on a page that has a material (Rules.CSS): its
	// colour as written in --kitwork-background, for a glass's fallback and for none, and that colour
	// without its /N in --kitwork-glass, for the glass to be made of (material.go, handOver)
	handover string
	// gradient is a background or text painted with a gradient (gradients.go): it animates nothing, and
	// on its page the colour classes of the same attribute give way to it and take it back (Rules.CSS)
	gradient bool
	// pairs are the custom properties the gradient's { light, dark } stops are written in, for the
	// page's stylesheet to set in each mode (Rules.CSS)
	pairs []pairValue
}

// scopedRule is a rule for an element of one kind. width, when set, is the width it starts at on top
// of the class's condition: a heading's size grows with the screen (heading="1" size="huge" is 48px,
// then 60px from small, 72px from large) under one class. wrap, when set, is the at-rule the rule
// holds under inside the class's condition: the fallback of a glass (@supports not …, its only one
// since 13/10/2026: the glass always shows, also where the reader asked for less transparency).
type scopedRule struct{ selector, declarations, width, wrap string }

// attribute is one row of the table: its name, its place in the fixed order, its layer, how it reads
// a value and what a value from data may be.
type attribute struct {
	name   string
	show   bool // the show layer
	read   func(t *Table, in Intent, value string) (reading, *Problem)
	domain func(t *Table, in Intent) []string
}

var table []attribute

var attributeRank = map[string]int{}

func init() {
	table = []attribute{
		{name: "flex", read: readFlex, domain: words("row", "column")},
		{name: "wrap", read: readWrap, domain: wrapDomain},
		{name: "align", read: readAlign, domain: alignDomain},
		{name: "space", read: readSpace, domain: words("between", "around", "evenly")},
		{name: "gap", read: readSpacing("gap"), domain: list(spacingSteps)},
		{name: "columns", read: readWhole(1, 12, "columns là số cột, số nguyên từ 1 đến 12", func(n string) string { return "grid-template-columns:repeat(" + n + ",minmax(0,1fr))" }), domain: list(wholeRange(1, 12))},
		{name: "span", read: readWhole(1, 12, "span là số cột thẻ trải qua, số nguyên từ 1 đến 12", func(n string) string { return "grid-column:span " + n + "/span " + n }), domain: list(wholeRange(1, 12))},
		{name: "place", read: readPlace, domain: list(placeWords)},
		{name: "pin", read: readPin, domain: words("top", "bottom", "none")},
		{name: "lazy", read: readLazy, domain: words("true", "false")},
		{name: "container", read: readContainer, domain: list(widthWords)},
		{name: "width", read: readSize("inline-size", true), domain: sizeDomain(true)},
		{name: "min-width", read: readSize("min-inline-size", true), domain: sizeDomain(true)},
		{name: "max-width", read: readSize("max-inline-size", true), domain: sizeDomain(true)},
		{name: "height", read: readSize("block-size", false), domain: sizeDomain(false)},
		{name: "min-height", read: readSize("min-block-size", false), domain: sizeDomain(false)},
		{name: "max-height", read: readSize("max-block-size", false), domain: sizeDomain(false)},
		{name: "aspect-ratio", read: readRatio, domain: list(ratioDomain)},
		{name: "fit", read: readFit, domain: words("cover", "contain")},
		{name: "padding", read: readSpacing("padding"), domain: list(spacingSteps)},
		{name: "padding-x", read: readSpacing("padding-inline"), domain: list(spacingSteps)},
		{name: "padding-y", read: readSpacing("padding-block"), domain: list(spacingSteps)},
		{name: "padding-top", read: readSpacing("padding-block-start"), domain: list(spacingSteps)},
		{name: "padding-bottom", read: readSpacing("padding-block-end"), domain: list(spacingSteps)},
		// left and right are the screen's sides and never flip; start and end are the line's and flip
		// right to left. A side wins over an axis over padding; where a side and its twin are both
		// written (padding-left with padding-start), the twin, later here, wins and the author is told
		// (ui/classes.go, checkSides)
		{name: "padding-left", read: readSpacing("padding-left"), domain: list(spacingSteps)},
		{name: "padding-right", read: readSpacing("padding-right"), domain: list(spacingSteps)},
		{name: "padding-start", read: readSpacing("padding-inline-start"), domain: list(spacingSteps)},
		{name: "padding-end", read: readSpacing("padding-inline-end"), domain: list(spacingSteps)},
		{name: "scroll", read: readScroll, domain: words("x", "y")},
		{name: "lines", read: readWhole(1, 10, "lines là số dòng tối đa, số nguyên từ 1 đến 10", func(n string) string {
			return "display:-webkit-box;-webkit-box-orient:vertical;-webkit-line-clamp:" + n + ";overflow:hidden"
		}), domain: list(wholeRange(1, 10))},
		{name: "background", read: readBackground, domain: colourDomain}, // a colour or a gradient (gradients.go)
		// right after background: at one condition the glass wins over the plain colour (material.go)
		{name: "material", read: readMaterial, domain: list(materialWords)},
		{name: "border", read: readBorder, domain: borderDomain},
		{name: "radius", read: readRadius("border-radius"), domain: list(radiusWords)},
		// the sides (two corners each), then the corners: within one condition a corner wins over a side
		// and a side over radius, whatever order they are written in. top, bottom, left and right are
		// the screen's and never flip; start and end are the line's and flip right to left. Each
		// physical word comes before its logical twin, so the twin wins where both are written
		// (ui/classes.go, checkSides)
		{name: "radius-top", read: readRadius("border-top-left-radius", "border-top-right-radius"), domain: list(radiusWords)},
		{name: "radius-bottom", read: readRadius("border-bottom-left-radius", "border-bottom-right-radius"), domain: list(radiusWords)},
		{name: "radius-left", read: readRadius("border-top-left-radius", "border-bottom-left-radius"), domain: list(radiusWords)},
		{name: "radius-right", read: readRadius("border-top-right-radius", "border-bottom-right-radius"), domain: list(radiusWords)},
		{name: "radius-start", read: readRadius("border-start-start-radius", "border-end-start-radius"), domain: list(radiusWords)},
		{name: "radius-end", read: readRadius("border-start-end-radius", "border-end-end-radius"), domain: list(radiusWords)},
		{name: "radius-top-left", read: readRadius("border-top-left-radius"), domain: list(radiusWords)},
		{name: "radius-top-right", read: readRadius("border-top-right-radius"), domain: list(radiusWords)},
		{name: "radius-bottom-left", read: readRadius("border-bottom-left-radius"), domain: list(radiusWords)},
		{name: "radius-bottom-right", read: readRadius("border-bottom-right-radius"), domain: list(radiusWords)},
		{name: "radius-top-start", read: readRadius("border-start-start-radius"), domain: list(radiusWords)},
		{name: "radius-top-end", read: readRadius("border-start-end-radius"), domain: list(radiusWords)},
		{name: "radius-bottom-start", read: readRadius("border-end-start-radius"), domain: list(radiusWords)},
		{name: "radius-bottom-end", read: readRadius("border-end-end-radius"), domain: list(radiusWords)},
		{name: "shadow", read: readShadow, domain: list(shadowWords)},
		{name: "opacity", read: readPercent("opacity", 0, 100, "opacity là độ hiện của cả phần tử, số nguyên từ 0 đến 100", false), domain: list(stepsOf(0, 100, 5))},
		{name: "scale", read: readPercent("scale", 0, 200, "scale là phần trăm cỡ, số nguyên từ 0 đến 200 (95 là 95%)", true), domain: words("0", "50", "75", "90", "95", "100", "105", "110", "125", "150")},
		{name: "rotate", read: readRotate, domain: words("0", "45", "90", "180", "270")},
		{name: "font", read: readFont, domain: words("body", "code")},
		{name: "size", read: readTextSize, domain: list(textSizeWords)},
		{name: "weight", read: readWeight, domain: list(weightWords)},
		{name: "italic", read: readBoolean("font-style:italic", "font-style:normal"), domain: words("true", "false")},
		{name: "case", read: readCase, domain: words("upper", "lower")},
		{name: "underline", read: readBoolean("text-decoration-line:underline", "text-decoration-line:none"), domain: words("true", "false")},
		{name: "strikethrough", read: readStrikethrough, domain: words("true", "false")},
		{name: "color", read: readTextColour, domain: colourDomain}, // a colour or gradient text (gradients.go)
		{name: "thickness", read: readWhole(1, 8, "thickness là độ dày vạch tính bằng px, số nguyên từ 1 đến 8", func(n string) string { return "block-size:" + n + "px" }), domain: list(wholeRange(1, 8))},
		{name: "show", show: true, read: readShow},
	}
	for i, a := range table {
		attributeRank[a.name] = i
	}
}

// Attributes lists the presentation attributes in the fixed order of the stylesheet: within one
// condition, a later attribute wins over an earlier one (padding-top over padding-y over padding).
func Attributes() []string {
	out := make([]string, len(table))
	for i, a := range table {
		out[i] = a.name
	}
	return out
}

// IsPresentation reports whether an attribute name is one the table reads.
func IsPresentation(name string) bool { _, ok := attributeRank[name]; return ok }

// Rank is an attribute's place in the fixed order (Attributes): within one condition the rule of the
// higher rank wins, whatever order the attributes are written in. -1 for a name the table does not read.
func Rank(name string) int {
	if rank, ok := attributeRank[name]; ok {
		return rank
	}
	return -1
}

// Logical is the logical twin of a physical left or right attribute: the one that names the same side
// or corner in a line written left to right (rightToLeft false) or right to left. padding-left is
// padding-start left to right and padding-end right to left; radius-top-right is radius-top-end, then
// radius-top-start. "" for any other name: top and bottom are the same in both directions, start and
// end are the line's words already.
func Logical(name string, rightToLeft bool) string {
	if !IsPresentation(name) {
		return ""
	}
	start, end := "start", "end"
	if rightToLeft {
		start, end = end, start
	}
	switch {
	case strings.HasSuffix(name, "-left"):
		return strings.TrimSuffix(name, "left") + start
	case strings.HasSuffix(name, "-right"):
		return strings.TrimSuffix(name, "right") + end
	}
	return ""
}

func words(w ...string) func(*Table, Intent) []string {
	return func(*Table, Intent) []string { return w }
}

func list(w []string) func(*Table, Intent) []string {
	return func(*Table, Intent) []string { return w }
}

func stepsOf(low, high, step int) []string {
	var out []string
	for n := low; n <= high; n += step {
		out = append(out, strconv.Itoa(n))
	}
	return out
}

// condition is a parsed prefix: the narrowest width (widths are min-width, so large,wide holds from
// large) and the states, in the source's order, each once.
type condition struct {
	width  string
	states []string
	label  string // as the class writes it: "medium", "large+hover", "hover,focus"
	// repeated is a state the prefix writes twice (hover,hover:): it holds as written once, the class
	// names it once, and the author is told (Table.Read)
	repeated string
}

func parseCondition(in Intent) (condition, *Problem) {
	var c condition
	if w := strings.TrimSpace(in.Width); w != "" {
		rank := len(widthWords)
		for _, word := range strings.Split(w, ",") {
			i := indexOf(widthWords, word)
			if i < 0 {
				return c, problem(in, "tiền tố độ rộng không tồn tại: "+word+": (small, medium, large, wide, huge)")
			}
			if i < rank {
				rank = i
			}
		}
		c.width = widthWords[rank]
	}
	if s := strings.TrimSpace(in.State); s != "" {
		seen := map[string]bool{}
		for _, word := range strings.Split(s, ",") {
			if indexOf(stateWords, word) < 0 {
				return c, problem(in, "tiền tố trạng thái không tồn tại: "+word+": ("+strings.Join(stateWords, ", ")+")")
			}
			if seen[word] {
				if c.repeated == "" {
					c.repeated = word
				}
				continue
			}
			seen[word] = true
			c.states = append(c.states, word)
		}
	}
	c.label = c.width
	if len(c.states) > 0 {
		if c.label != "" {
			c.label += "+"
		}
		c.label += strings.Join(c.states, ",")
	}
	return c, nil
}

func indexOf(list []string, word string) int {
	for i, w := range list {
		if w == word {
			return i
		}
	}
	return -1
}

// Read validates one intent, records its rules in r and returns its class: the attribute as the
// source writes it, [condition ":"] name "-" value — padding-4, medium:padding-6,
// large+hover:background-brand+15, show-false. The HTML carries it as written; only the CSS escapes
// it. "" with no error and no warning means the attribute needs no class: strikethrough="true" without
// a prefix is the <s> element. flex="row" without a prefix returns flex-row, the direction class
// reboot.css styles, and records no page rule. A nil r validates only.
//
// err is a value the table refuses: the compile stops (decision D). warnings are what the page renders
// through, each a Problem with Warning set, for the caller to tell the author at the attribute: the
// class is drawn as written (columns="13" is repeat(13,…), padding="17px" is 17px), or, for a value
// that does nothing where it is written (fit on a box, align="top" on a text, width="auto"), "" comes
// back with its warning: no class, no rule, as if it were not written.
func (t *Table) Read(r *Rules, in Intent) (class string, warnings []*Problem, err error) {
	class, _, warnings, err = t.class(r, in, false, false)
	return class, warnings, err
}

// Class is Read for a caller with no way to tell the author: a value Read would only warn of is
// refused with that warning, and records nothing, so nothing is drawn that nobody was told of.
func (t *Table) Class(r *Rules, in Intent) (string, error) {
	class, _, _, err := t.class(r, in, false, true)
	return class, err
}

func (t *Table) class(r *Rules, in Intent, fromData, strict bool) (string, reading, []*Problem, error) {
	rank, ok := attributeRank[in.Name]
	if !ok {
		return "", reading{}, nil, notPresentation(in)
	}
	a := table[rank]
	c, p := parseCondition(in)
	if p != nil {
		return "", reading{}, nil, p
	}
	value := strings.TrimSpace(in.Value)
	in.Value = value
	var warnings []*Problem
	if c.repeated != "" {
		// hover,hover holds as hover: the class names it once
		warnings = append(warnings, warning(in, "trạng thái "+c.repeated+": viết hai lần trong "+strings.TrimSpace(in.State)+":"))
	}
	read, p := a.read(t, in, value)
	if p != nil && !p.Warning {
		return "", reading{}, nil, p
	}
	if p != nil {
		warnings = append(warnings, p)
	}
	if a.show && len(c.states) > 0 {
		return "", reading{}, nil, problem(in, in.State+":show: ẩn hiện theo trạng thái chưa được hỗ trợ; ẩn hiện theo độ rộng thì viết medium:show=\"…\"")
	}
	if strict && len(warnings) > 0 {
		return "", reading{}, nil, warnings[0]
	}
	if read.inert || (read.none && c.label == "" && !fromData) {
		return "", read, warnings, nil
	}
	class := in.Name + "-" + value
	if c.label != "" {
		class = c.label + ":" + class
	}
	if read.reboot && c.label == "" {
		return class, read, warnings, nil
	}
	if r != nil {
		r.record(a, rank, c, value, class, read)
	}
	return class, read, warnings, nil
}

// ReadDomain is the classes a value from data ({{ }}) may become, compiled ahead into r: for a colour
// every token the palette declares (not a grown .on, not an operator -N/+N or an alpha), for the other
// attributes every word or step of their scale. It returns value → class; a value the map does not
// hold gets no class (the renderer warns in development). A prefix does not go with {{ }} in the
// first version (KITWORK_UI §8). Like Read, an attribute that does nothing where it is written (fit
// from data on a box) is no error: each of its values maps to "" (no class) and the warning, once,
// comes back for the caller to tell the author.
func (t *Table) ReadDomain(r *Rules, in Intent) (map[string]string, []*Problem, error) {
	return t.domain(r, in, false)
}

// Domain is ReadDomain for a caller with no way to tell the author: a value it would only warn of is
// refused, so an attribute none of whose values does anything here is an error (fit on a box).
func (t *Table) Domain(r *Rules, in Intent) (map[string]string, error) {
	out, _, err := t.domain(r, in, true)
	return out, err
}

func (t *Table) domain(r *Rules, in Intent, strict bool) (map[string]string, []*Problem, error) {
	rank, ok := attributeRank[in.Name]
	if !ok {
		return nil, nil, notPresentation(in)
	}
	a := table[rank]
	if strings.TrimSpace(in.Width) != "" || strings.TrimSpace(in.State) != "" {
		return nil, nil, problem(in, "bản đầu chưa cho tiền tố đi cùng {{ }}: "+in.Name+" từ dữ liệu không có tiền tố")
	}
	if a.domain == nil {
		return nil, nil, problem(in, in.Name+" từ dữ liệu là biểu thức sống: viết "+in.Name+"=\"điều kiện\" (không có {{ }})")
	}
	out := map[string]string{}
	var refused error
	var warnings []*Problem
	told := map[string]bool{}
	for _, value := range a.domain(t, in) {
		v := in
		v.Value = value
		class, _, warned, err := t.class(r, v, true, strict)
		if err != nil {
			if refused == nil {
				refused = err
			}
			continue // a word the tag does not take (align of text is start, center or end)
		}
		for _, w := range warned {
			if !told[w.Message] {
				told[w.Message] = true
				warnings = append(warnings, w)
			}
		}
		out[value] = class
	}
	if len(out) == 0 && refused != nil {
		return nil, nil, refused // the tag takes no value of the attribute
	}
	return out, warnings, nil
}

// misnamed are the names a hand used to CSS or Tailwind writes where .ui has its own word, or none
// (KITWORK_UI §7): what to write instead, and the word when one word does it.
var misnamed = map[string]struct{ suggestion, message string }{
	"padding-inline":       {"padding-x", "trục đầu và cuối dòng là padding-x"},
	"padding-block":        {"padding-y", "trục trên và dưới là padding-y"},
	"padding-inline-start": {"padding-start", "cạnh đầu dòng là padding-start, tự lật khi viết từ phải sang trái"},
	"padding-inline-end":   {"padding-end", "cạnh cuối dòng là padding-end, tự lật khi viết từ phải sang trái"},
	"padding-block-start":  {"padding-top", "cạnh trên là padding-top"},
	"padding-block-end":    {"padding-bottom", "cạnh dưới là padding-bottom"},
	// CSS's corner properties: the .ui corner says the side first (top, bottom), then left, right,
	// start or end
	"border-top-left-radius":     {"radius-top-left", "góc trên bên trái là radius-top-left (none, small, medium, large, full)"},
	"border-top-right-radius":    {"radius-top-right", "góc trên bên phải là radius-top-right (none, small, medium, large, full)"},
	"border-bottom-left-radius":  {"radius-bottom-left", "góc dưới bên trái là radius-bottom-left (none, small, medium, large, full)"},
	"border-bottom-right-radius": {"radius-bottom-right", "góc dưới bên phải là radius-bottom-right (none, small, medium, large, full)"},
	"border-start-start-radius":  {"radius-top-start", "góc trên đầu dòng là radius-top-start (none, small, medium, large, full), tự lật khi viết từ phải sang trái"},
	"border-start-end-radius":    {"radius-top-end", "góc trên cuối dòng là radius-top-end (none, small, medium, large, full), tự lật khi viết từ phải sang trái"},
	"border-end-start-radius":    {"radius-bottom-start", "góc dưới đầu dòng là radius-bottom-start (none, small, medium, large, full), tự lật khi viết từ phải sang trái"},
	"border-end-end-radius":      {"radius-bottom-end", "góc dưới cuối dòng là radius-bottom-end (none, small, medium, large, full), tự lật khi viết từ phải sang trái"},
	// what .ui has no word for, or says another way
	"margin":   {"", "không có margin: khoảng cách giữa các phần là gap của khung cha, khoảng đệm là padding của chính khung"},
	"display":  {"", "không có display: hàng hay cột là flex, lưới là thẻ grid, ẩn hiện là show hoặc if"},
	"position": {"place", "không có position: thẻ nằm đâu so với cha là place"},
	"z-index":  {"", "không có z-index: lớp là thứ tự viết, thẻ viết trước nằm dưới"},
	"justify":  {"", "không có justify: các con dồn về đâu là align, khoảng trống chia thế nào là space"},
	// the CSS property a .ui attribute says in its own word
	"font-size":             {"size", "cỡ chữ là size (mini, small, medium, large, huge)"},
	"font-weight":           {"weight", "độ đậm của chữ là weight (normal, semibold, bold…)"},
	"font-family":           {"font", "họ chữ là font, theo vai trò khai trong router.fonts (body, code)"},
	"text-align":            {"align", "căn chữ là align (start, center, end)"},
	"text-transform":        {"case", "chữ hoa, chữ thường là case (upper, lower)"},
	"line-clamp":            {"lines", "số dòng tối đa là lines"},
	"background-color":      {"background", "màu nền là background, một token của bảng màu"},
	"border-radius":         {"radius", "bo góc là radius (none, small, medium, large, full)"},
	"rounded":               {"radius", "bo góc là radius (none, small, medium, large, full)"},
	"box-shadow":            {"shadow", "bóng là shadow (small, medium, large)"},
	"flex-direction":        {"flex", "hướng xếp các con là flex (row, column)"},
	"flex-wrap":             {"wrap", "xuống dòng khi hết chỗ là wrap (true, false)"},
	"overflow":              {"scroll", "vùng cuộn là scroll (x, y)"},
	"grid-template-columns": {"columns", "số cột của lưới là columns"},
	"content-visibility":    {"lazy", "bỏ qua phần ở xa màn hình là lazy (true, hoặc chiều cao ước lượng: 400px)"},
}

// notPresentation refuses a name the table does not read, and says what to write when it is the
// name of another system.
func notPresentation(in Intent) *Problem {
	written := in.Name + "=\"" + strings.TrimSpace(in.Value) + "\": "
	key := in.Name
	if strings.HasPrefix(key, "margin-") {
		key = "margin"
	}
	if m, ok := misnamed[key]; ok {
		p := problem(in, written+m.message)
		p.Suggestion = m.suggestion
		return p
	}
	if p := misnamedSide(in); p != nil {
		return p
	}
	return problem(in, written+in.Name+" không phải thuộc tính trình bày")
}

// tailwindPadding are Tailwind's padding names, each with the .ui attribute it is.
var tailwindPadding = map[string]string{
	"p": "padding", "px": "padding-x", "py": "padding-y", "pt": "padding-top", "pb": "padding-bottom",
	"pl": "padding-left", "pr": "padding-right", "ps": "padding-start", "pe": "padding-end",
}

// tailwindCorners are the sides and corners of Tailwind's rounded-…, each with the .ui attribute it is.
var tailwindCorners = map[string]string{
	"t": "radius-top", "b": "radius-bottom", "l": "radius-left", "r": "radius-right", "s": "radius-start", "e": "radius-end",
	"tl": "radius-top-left", "tr": "radius-top-right", "bl": "radius-bottom-left", "br": "radius-bottom-right",
	"ss": "radius-top-start", "se": "radius-top-end", "es": "radius-bottom-start", "ee": "radius-bottom-end",
}

// radiusWordOf is the .ui word of a radius written in .ui's words or Tailwind's (lg is .5rem, medium).
func radiusWordOf(word string) (string, bool) {
	if _, ok := radii[word]; ok {
		return word, true
	}
	word, ok := radiusReplaced[word]
	return word, ok
}

// misnamedSide answers a side or a corner written in another system's words, nil for any other name:
// Tailwind's padding names, as an attribute (pl="4") or a class written as one (pl-4), Tailwind's
// rounded sides and corners (rounded-t-lg, rounded-tl="xl"), and a .ui corner named the other way
// round (radius-left-top). Its suggestion is the .ui name, or name="value" when the value is in the
// name or is a word of Tailwind's too: Tailwind's sizes are read by their size, so rounded-t-lg (.5rem)
// is radius-top="medium", as radius="lg" is radius="medium".
func misnamedSide(in Intent) *Problem {
	name, value := in.Name, strings.TrimSpace(in.Value)
	written := name
	if value != "" {
		written += "=\"" + value + "\""
	}
	if short, step, _ := strings.Cut(name, "-"); tailwindPadding[short] != "" {
		word := tailwindPadding[short]
		switch {
		case step == "":
			p := problem(in, written+": "+short+" là chữ viết tắt của Tailwind; .ui viết đủ chữ: "+word+" (theo thang khoảng cách)")
			p.Suggestion = word
			return p
		case spacingIndex[step]:
			p := problem(in, written+": "+name+" là lớp của Tailwind; .ui viết thuộc tính: "+word+"=\""+step+"\"")
			p.Suggestion = word + "=\"" + step + "\""
			return p
		}
		return nil
	}
	if rest, ok := strings.CutPrefix(name, "rounded-"); ok {
		corner, size, _ := strings.Cut(rest, "-")
		attribute, isCorner := tailwindCorners[corner]
		if !isCorner {
			attribute, size = "radius", rest // rounded-lg: every corner
		}
		word, known := radiusWordOf(size)
		switch {
		case size == "" && value == "":
			word, known = "small", true // rounded-t: Tailwind's default corner, .25rem
		case size == "" && value != "":
			word, known = radiusWordOf(value)
		case !known:
			return nil // rounded-t-huge, rounded-x: not Tailwind's
		}
		if !known {
			p := problem(in, written+": "+name+" là chữ của Tailwind; .ui viết "+attribute+" (none, small, medium, large, full)")
			p.Suggestion = attribute
			return p
		}
		p := problem(in, written+": "+name+" là chữ của Tailwind; .ui viết thuộc tính: "+attribute+"=\""+word+"\" ("+radii[word]+"). Các chữ: none, small, medium, large, full")
		p.Suggestion = attribute + "=\"" + word + "\""
		return p
	}
	if rest, ok := strings.CutPrefix(name, "radius-"); ok {
		across, along, _ := strings.Cut(rest, "-")
		switch across {
		case "left", "right", "start", "end":
			if along == "top" || along == "bottom" {
				corner := "radius-" + along + "-" + across
				p := problem(in, written+": góc viết cạnh trên hay dưới trước rồi mới đến "+across+": "+corner)
				p.Suggestion = corner
				return p
			}
		}
	}
	return nil
}

// ---- readers ----

func readFlex(_ *Table, in Intent, v string) (reading, *Problem) {
	switch v {
	case "row":
		return reading{reboot: true, declarations: "flex-direction:row;justify-content:var(--kitwork-space,var(--kitwork-horizontal,normal));align-items:var(--kitwork-vertical,normal)"}, nil
	case "column":
		return reading{reboot: true, declarations: "flex-direction:column;justify-content:var(--kitwork-space,var(--kitwork-vertical,normal));align-items:var(--kitwork-horizontal,normal)"}, nil
	}
	return reading{}, problem(in, "flex nhận row hoặc column, không có \""+v+"\"")
}

func readBoolean(yes, no string) func(*Table, Intent, string) (reading, *Problem) {
	return func(_ *Table, in Intent, v string) (reading, *Problem) {
		switch v {
		case "true":
			return reading{declarations: yes}, nil
		case "false":
			return reading{declarations: no}, nil
		}
		return reading{}, problem(in, in.Name+" nhận true hoặc false, không có \""+v+"\"")
	}
}

// textual reports whether a tag is a text (<text>, a heading among them): it lays out no children,
// its words are what align and wrap act on.
func textual(tag string) bool { return tag == "text" || tag == "heading" }

// readWrap reads wrap. On a frame the children go to the next line when the row is full (true) or
// stay on one (false). On a text it is how the words break into lines (KITWORK_UI §7, "wrap trên
// chữ"): true as usual, false keeps a phrase in a sentence together (inline-block: it moves to the
// next line whole and still breaks inside when longer than a line), balance evens the lines, pretty
// avoids one lonely last word. The class's own rule is the frame's on every tag, and the text's is
// scoped to an element that lays out no children, so one class serves both.
func readWrap(_ *Table, in Intent, v string) (reading, *Problem) {
	frame := map[string]string{"true": "flex-wrap:wrap", "false": "flex-wrap:nowrap"}
	if textual(in.Tag) {
		text := map[string]string{"true": "text-wrap:wrap", "false": "display:inline-block", "balance": "text-wrap:balance", "pretty": "text-wrap:pretty"}
		d, ok := text[v]
		if !ok {
			return reading{}, problem(in, "wrap trên chữ nhận true, false, balance hoặc pretty, không có \""+v+"\"")
		}
		if own, isFrame := frame[v]; isFrame {
			return reading{declarations: own, scoped: []scopedRule{{selector: textNotContainer, declarations: d}}}, nil
		}
		return reading{declarations: d}, nil
	}
	if d, ok := frame[v]; ok {
		return reading{declarations: d}, nil
	}
	if v == "balance" || v == "pretty" {
		// a word of wrap, where it does nothing: the frame's children are not lines of words
		return reading{inert: true}, warning(in, "wrap=\""+v+"\" chỉ có nghĩa trên chữ (<text>); trên khung, wrap nhận true hoặc false")
	}
	return reading{}, problem(in, "wrap nhận true hoặc false, không có \""+v+"\"")
}

func wrapDomain(_ *Table, in Intent) []string {
	if textual(in.Tag) {
		return []string{"true", "false", "balance", "pretty"}
	}
	return []string{"true", "false"}
}

func readStrikethrough(t *Table, in Intent, v string) (reading, *Problem) {
	read, p := readBoolean("text-decoration-line:line-through", "text-decoration-line:none")(t, in, v)
	read.none = true // without a prefix the element is <s> (or not): the tag says it
	return read, p
}

func readShow(_ *Table, in Intent, v string) (reading, *Problem) {
	switch v {
	case "false":
		return reading{declarations: "display:none"}, nil
	case "true":
		// revert-layer hands back whatever the page and elements layers set: flex, inline-flex, grid,
		// the -webkit-box of lines
		return reading{declarations: "display:revert-layer"}, nil
	}
	return reading{}, problem(in, "show có tiền tố nhận true hoặc false, không có \""+v+"\"; ẩn hiện theo dữ liệu là show=\"điều kiện\" không tiền tố")
}

// textNotContainer scopes text-align to an element that lays out no children: a container always
// carries a direction class, a text element never does.
const textNotContainer = ":where(:not(.flex-row,.flex-column,.grid))"

// readAlign sets the variables the direction classes read (reboot.css), so the same class places the
// children whichever way medium:flex="row" turns the container. On text it also aligns the lines;
// the class's own rule stays the same on every tag, so one class serves both.
func readAlign(_ *Table, in Intent, v string) (reading, *Problem) {
	pos, ok := positions[v]
	if textual(in.Tag) {
		switch v {
		case "start", "center", "end":
			return reading{
				declarations: "--kitwork-vertical:" + flexValue[pos[0]] + ";--kitwork-horizontal:" + flexValue[pos[1]],
				scoped:       []scopedRule{{selector: textNotContainer, declarations: "text-align:" + v}},
			}, nil
		}
		if ok {
			// a position of a frame's children, where it does nothing: a text lays out no children
			return reading{inert: true}, warning(in, "align=\""+v+"\" là vị trí các con của một khung, trên chữ không có tác dụng: align của text nhận start, center hoặc end")
		}
		return reading{}, problem(in, "align của text nhận start, center hoặc end, không có \""+v+"\"")
	}
	if !ok {
		if v == "between" || v == "around" || v == "evenly" {
			p := problem(in, "align là vị trí của các con; giãn cách là space=\""+v+"\"")
			p.Suggestion = "space=\"" + v + "\""
			return reading{}, p
		}
		return reading{}, problem(in, "align nhận top-start, top, top-end, start, center, end, bottom-start, bottom hoặc bottom-end, không có \""+v+"\"")
	}
	return reading{declarations: "--kitwork-vertical:" + flexValue[pos[0]] + ";--kitwork-horizontal:" + flexValue[pos[1]]}, nil
}

func alignDomain(_ *Table, in Intent) []string {
	if textual(in.Tag) {
		return []string{"start", "center", "end"}
	}
	return positionWords
}

func readSpace(_ *Table, in Intent, v string) (reading, *Problem) {
	switch v {
	case "between", "around", "evenly":
		return reading{declarations: "--kitwork-space:space-" + v}, nil
	}
	if _, isStep := spacingIndex[v]; isStep {
		p := problem(in, "space nhận between, around hoặc evenly; khoảng cách cố định là gap=\""+v+"\"")
		p.Suggestion = "gap=\"" + v + "\""
		return reading{}, p
	}
	return reading{}, problem(in, "space nhận between, around hoặc evenly, không có \""+v+"\"")
}

func readSpacing(property string) func(*Table, Intent, string) (reading, *Problem) {
	return func(_ *Table, in Intent, v string) (reading, *Problem) {
		if spacingIndex[v] {
			return reading{declarations: property + ":" + remOf(v)}, nil
		}
		length, p := offScale(in, v)
		if length == "" {
			return reading{}, p
		}
		return reading{declarations: property + ":" + length}, p
	}
}

// offScale reads a spacing value off the scale. A length the engine can draw exactly as written — a
// plain number, in steps of .25rem (13 is 3.25rem), or a number with a unit of the closed set (17px,
// 2vh) — comes back with its warning: the scale is the house's, the author decides (decision D,
// question 3). A length in px, rem or em names the step nearest to it (16px a rem). Anything else is
// refused: calc() and var(), a sign, a word.
func offScale(in Intent, v string) (string, *Problem) {
	if strings.Contains(v, "calc(") || strings.Contains(v, "var(") {
		return "", problem(in, in.Name+" theo thang khoảng cách, không nhận calc() hay var(): "+spacingWords)
	}
	if m := unitSize.FindStringSubmatch(v); m != nil {
		f, _ := strconv.ParseFloat(m[1], 64)
		switch m[2] {
		case "px":
			return v, nearestStepWarning(in, v, f/16)
		case "rem", "em":
			return v, nearestStepWarning(in, v, f)
		}
		return v, warning(in, in.Name+"=\""+v+"\": khoảng cách theo thang, không theo "+m[2]+": "+spacingWords)
	}
	if plainNumber.MatchString(v) {
		f, _ := strconv.ParseFloat(v, 64)
		return remOf(v), nearestStepWarning(in, v, f*0.25)
	}
	return "", problem(in, in.Name+" theo thang khoảng cách, không có \""+v+"\": "+spacingWords)
}

// nearestStepWarning tells a length off the spacing scale, drawn as written, with the step nearest it;
// a length that is a step written another way (1.5rem, 24px, 6.0) is told it is that step.
func nearestStepWarning(in Intent, v string, rem float64) *Problem {
	step := nearestStep(rem)
	if f, _ := strconv.ParseFloat(step, 64); math.Abs(f*0.25-rem) < 1e-9 {
		p := warning(in, in.Name+"=\""+v+"\" là bậc "+step+" của thang khoảng cách ("+remOf(step)+"): viết "+in.Name+"=\""+step+"\"")
		p.Suggestion = step
		return p
	}
	p := warning(in, in.Name+"=\""+v+"\" không có trong thang khoảng cách; gần nhất là "+in.Name+"=\""+step+"\" ("+remOf(step)+"). Thang: "+spacingWords)
	p.Suggestion = step
	return p
}

// plainNumber is a number written plainly: digits, at most one decimal part, no sign, no exponent.
var plainNumber = regexp.MustCompile(`^\d+(?:\.\d+)?$`)

// readWhole reads a whole number from low. One above high is still drawn as the number written (13
// columns is repeat(13, …)): the range is the house's, so it is a warning (decision D, question 3).
// One below low has nothing to draw (repeat(0, …), span 0 and a clamp of 0 lines are not CSS) and a
// value that is not a whole number is not understood: both are refused.
func readWhole(low, high int, message string, declarations func(string) string) func(*Table, Intent, string) (reading, *Problem) {
	return func(_ *Table, in Intent, v string) (reading, *Problem) {
		n, ok := whole(v, low, math.MaxInt32)
		if !ok {
			return reading{}, problem(in, message+", không có \""+v+"\"")
		}
		read := reading{declarations: declarations(v)}
		if n > high {
			return read, warning(in, message+"; \""+v+"\" ở ngoài khoảng đó")
		}
		return read, nil
	}
}

// readPercent reads a whole percentage within [low, high]. drawn says a value outside it still means
// something the engine draws as written (scale="300" is three times the size, scale="-100" a mirror):
// a warning; otherwise it is refused (opacity above 100 means nothing).
func readPercent(property string, low, high int, message string, drawn bool) func(*Table, Intent, string) (reading, *Problem) {
	return func(_ *Table, in Intent, v string) (reading, *Problem) {
		n, ok := integer(v)
		if !ok || (!drawn && (n < low || n > high)) {
			return reading{}, problem(in, message+", không có \""+v+"\"")
		}
		read := reading{declarations: property + ":" + number(float64(n)/100)}
		if property == "scale" {
			read.transition = []string{"scale", "rotate"}
		}
		if n < low || n > high {
			return read, warning(in, message+"; \""+v+"\" ở ngoài khoảng đó")
		}
		return read, nil
	}
}

// readRotate reads rotate in degrees. A whole number outside 0 to 360 still turns the element as
// written (400 is a turn and 40 degrees, -45 is a quarter turn back): a warning.
func readRotate(_ *Table, in Intent, v string) (reading, *Problem) {
	n, ok := integer(v)
	if !ok {
		return reading{}, problem(in, "rotate là số độ quay, số nguyên từ 0 đến 360, không có \""+v+"\"")
	}
	read := reading{declarations: "rotate:" + v + "deg", transition: []string{"scale", "rotate"}}
	if n < 0 || n > 360 {
		return read, warning(in, "rotate là số độ quay, số nguyên từ 0 đến 360; \""+v+"\" ở ngoài khoảng đó")
	}
	return read, nil
}

func readPlace(_ *Table, in Intent, v string) (reading, *Problem) {
	if d, ok := places[v]; ok {
		return reading{declarations: d}, nil
	}
	if v == "before" || v == "after" {
		return reading{}, problem(in, "place \""+v+"\" chưa được hỗ trợ ở bản này")
	}
	return reading{}, problem(in, "place nhận top-start, top, top-end, start, center, end, bottom-start, bottom, bottom-end, fill (bên trong cha) hoặc below-start, below, below-end, above-start, above, above-end (bên ngoài), không có \""+v+"\"")
}

// readPin reads pin: the element keeps its place in the page and sticks to the top or bottom edge of
// the screen while the page scrolls under it (sticky, never fixed: nothing is covered, nothing needs a
// gap), above the content and below every floating layer (popover, toast, menus: z-index 50). none
// gives back the position the element had (medium:pin="none" undoes pin="top"). A width prefix moves
// it; a state does not.
func readPin(_ *Table, in Intent, v string) (reading, *Problem) {
	if strings.TrimSpace(in.State) != "" {
		return reading{}, problem(in, "pin không đổi theo trạng thái; theo độ rộng thì viết medium:pin=\""+v+"\"")
	}
	switch v {
	case "top":
		return reading{declarations: "position:sticky;inset-block-start:0;z-index:40"}, nil
	case "bottom":
		return reading{declarations: "position:sticky;inset-block-end:0;z-index:40"}, nil
	case "none":
		return reading{declarations: "position:revert-layer;inset-block:revert-layer;z-index:revert-layer"}, nil
	}
	p := problem(in, "pin nhận top, bottom hoặc none, không có \""+v+"\"")
	switch v {
	case "sticky", "fixed", "true", "start":
		p.Suggestion = "top"
		p.Message = "pin=\"" + v + "\": pin nhận top, bottom hoặc none (pin=\"top\" ghim vào mép trên khi cuộn, phần tử vẫn giữ chỗ của nó)"
	case "end":
		p.Suggestion = "bottom"
		p.Message = "pin=\"end\": pin nhận top, bottom hoặc none (mép dưới là bottom)"
	case "false":
		p.Suggestion = "none"
		p.Message = "pin=\"false\": pin nhận top, bottom hoặc none (không ghim là none)"
	}
	return reading{}, p
}

// lazyEstimate is the height lazy="true" keeps for an element the browser has not drawn yet: a block of
// a page, a little over half a phone's screen. A height written (lazy="2800px") replaces it.
const lazyEstimate = "500px"

// lazyHeight is a height lazy estimates: a number above 0 with a unit of the element's own or of the
// screen. Not %: an estimate is the element's height, not a part of its parent's.
var lazyHeight = regexp.MustCompile(`^(\d+(?:\.\d+)?)(px|rem|em|vw|vh|dvh)$`)

// readLazy reads lazy: while the element is far from the screen, the browser does not lay out or paint
// what it holds (content-visibility:auto) and keeps its room with an estimated height, then, once it
// has drawn it, with the size it last had (contain-intrinsic-size: auto <estimate>). What it holds stays
// in the page: find in page, a jump to an anchor inside, a screen reader and print all reach it. true
// estimates lazyEstimate; a height estimates that height; false draws it as always, to undo lazy from a
// width on (lazy="true" large:lazy="false"). The estimate is the height only: in a column the element
// takes its width from its parent as always; where its width comes from what it holds (a centred column,
// a row), it is 0 until first drawn, never an estimate that could overflow a phone, then the width it
// last had. Print draws everything (@media print), whatever a browser does with content-visibility
// there. A width prefix moves it; a state does not.
func readLazy(_ *Table, in Intent, v string) (reading, *Problem) {
	if strings.TrimSpace(in.State) != "" {
		return reading{}, problem(in, "lazy không đổi theo trạng thái; theo độ rộng thì viết medium:lazy=\""+v+"\"")
	}
	estimate := ""
	switch m := lazyHeight.FindStringSubmatch(v); {
	case v == "true":
		estimate = lazyEstimate
	case v == "false":
		return reading{declarations: "content-visibility:visible;contain-intrinsic-size:none"}, nil
	case m != nil:
		if f, _ := strconv.ParseFloat(m[1], 64); f == 0 {
			return reading{}, problem(in, "lazy=\""+v+"\": chiều cao ước lượng phải lớn hơn 0 (400px, 25rem, 100vh); không ước lượng thì viết lazy=\"true\"")
		}
		estimate = v
	}
	if estimate != "" {
		return reading{declarations: "content-visibility:auto;contain-intrinsic-inline-size:auto none;contain-intrinsic-block-size:auto " + estimate,
			scoped: []scopedRule{{wrap: "@media print", declarations: "content-visibility:visible"}}}, nil
	}
	p := problem(in, "lazy nhận true, false hoặc chiều cao ước lượng có đơn vị (px, rem, em, vw, vh, dvh: 400px, 25rem), không có \""+v+"\"")
	switch {
	case plainNumber.MatchString(v):
		p.Suggestion = v + "px"
		p.Message = "lazy=\"" + v + "\": chiều cao ước lượng cần đơn vị: lazy=\"" + v + "px\" (px, rem, em, vw, vh, dvh)"
	case strings.HasSuffix(v, "%") && plainNumber.MatchString(strings.TrimSuffix(v, "%")):
		p.Message = "lazy=\"" + v + "\": chiều cao ước lượng là của chính phần tử, không theo khung cha: viết px, rem, em, vw, vh hoặc dvh (400px, 25rem, 100vh)"
	case v == "auto":
		p.Suggestion = "true"
		p.Message = "lazy=\"auto\": lazy nhận true, false hoặc chiều cao ước lượng (400px); bỏ qua phần ở xa màn hình là lazy=\"true\""
	case v == "visible":
		p.Suggestion = "false"
		p.Message = "lazy=\"visible\": lazy nhận true, false hoặc chiều cao ước lượng (400px); luôn vẽ là lazy=\"false\""
	}
	return reading{}, p
}

// widthProblem refuses a word outside the width scale and says what it is when it is a known word. A
// size (width, height and their min-/max- forms) is told the length that says what Tailwind's full
// and screen meant; container is not, it takes the five words only.
func widthProblem(in Intent, v, accepts string, size, horizontal bool) *Problem {
	p := problem(in, "")
	switch {
	case v == "mini":
		p.Message = in.Name + " " + accepts + "; mini là cỡ chữ, không phải độ rộng"
	case tailwindWidth[v] || strings.HasPrefix(v, "screen-"):
		p.Message = in.Name + " " + accepts + "; " + v + " là chữ của Tailwind, không phải của .ui"
		switch {
		case !size && v == "full":
			p.Message += " (không viết container là rộng hết chỗ cha)"
		case size && v == "full" && horizontal:
			p.Suggestion = "100%"
			p.Message += " (cả bề ngang của khung cha là 100%)"
		case size && v == "full":
			p.Suggestion = "100%"
			p.Message += " (cả chiều cao của khung cha là 100%)"
		case size && v == "screen" && horizontal:
			p.Suggestion = "100vw"
			p.Message += " (cả bề ngang cửa sổ là 100vw)"
		case size && v == "screen":
			p.Suggestion = "100dvh"
			p.Message += " (cả chiều cao cửa sổ là 100dvh)"
		}
	default:
		p.Message = in.Name + " " + accepts + "; không có \"" + v + "\""
	}
	return p
}

func readContainer(_ *Table, in Intent, v string) (reading, *Problem) {
	if px, ok := widthPixels[v]; ok {
		return reading{declarations: "inline-size:100%;max-inline-size:" + px + ";margin-inline:auto"}, nil
	}
	return reading{}, widthProblem(in, v, "theo thang độ rộng: "+widthScaleWords, false, true)
}

// readSize reads width, height and their min-/max- forms: a word of the width scale (the widths
// only), a step of the spacing scale, or a number with a unit of a closed set, written as it is.
// width alone also takes fit (as wide as what it holds, also in a column that stretches its children)
// and full (the whole width of its parent).
func readSize(property string, horizontal bool) func(*Table, Intent, string) (reading, *Problem) {
	return func(_ *Table, in Intent, v string) (reading, *Problem) {
		if in.Name == "width" {
			switch v {
			case "fit":
				return reading{declarations: property + ":fit-content"}, nil
			case "full":
				return reading{declarations: property + ":100%"}, nil
			}
		}
		if horizontal {
			if px, ok := widthPixels[v]; ok {
				return reading{declarations: property + ":" + px}, nil
			}
		}
		if spacingIndex[v] {
			return reading{declarations: property + ":" + remOf(v)}, nil
		}
		if unitSize.MatchString(v) {
			return reading{declarations: property + ":" + v}, nil
		}
		if plainNumber.MatchString(v) {
			// a number off the scale, drawn as written in steps of .25rem (13 is 3.25rem)
			f, _ := strconv.ParseFloat(v, 64)
			return reading{declarations: property + ":" + remOf(v)}, nearestStepWarning(in, v, f*0.25)
		}
		accepts := "nhận một số của thang (" + spacingWords + ") hoặc một số có đơn vị " + unitWords + " (640px, 50%, 68ch)"
		if horizontal {
			accepts = "nhận một chữ của thang độ rộng (" + widthScaleWords + "), " + accepts
		}
		if in.Name == "width" {
			accepts = "nhận fit (ôm nội dung) hoặc full (hết bề ngang của khung cha); hoặc" + strings.TrimPrefix(accepts, "nhận")
		}
		if strings.Contains(v, "calc(") || strings.Contains(v, "var(") {
			return reading{}, problem(in, in.Name+" "+accepts+"; không nhận calc() hay var()")
		}
		if !horizontal && widthPixels[v] != "" {
			return reading{}, problem(in, in.Name+" "+accepts+"; "+v+" là chữ của thang độ rộng, chỉ cho bề ngang")
		}
		if v == "auto" && strings.TrimSpace(in.Width) == "" && strings.TrimSpace(in.State) == "" {
			// the default written out: drawn as not written (decision D: a warning). With a prefix it
			// would undo a size written for a narrower screen, which not writing does not: still refused.
			unwritten := "theo nội dung"
			if strings.HasPrefix(in.Name, "min-") || strings.HasPrefix(in.Name, "max-") {
				unwritten = "không giới hạn"
			}
			return reading{inert: true}, warning(in, in.Name+"=\"auto\" là mặc định: không viết "+in.Name+" là "+unwritten+". "+in.Name+" "+accepts)
		}
		if v == "auto" {
			return reading{}, problem(in, in.Name+" "+accepts+"; không có \"auto\": không viết "+in.Name+" là theo nội dung")
		}
		return reading{}, widthProblem(in, v, accepts, true, horizontal)
	}
}

func sizeDomain(horizontal bool) func(*Table, Intent) []string {
	return func(_ *Table, in Intent) []string {
		if in.Name == "width" {
			return append(append([]string{"fit", "full"}, widthWords...), spacingSteps...)
		}
		if horizontal {
			return append(append([]string(nil), widthWords...), spacingSteps...)
		}
		return spacingSteps
	}
}

func readRatio(_ *Table, in Intent, v string) (reading, *Problem) {
	switch v {
	case "square":
		return reading{declarations: "aspect-ratio:1"}, nil
	case "auto":
		return reading{declarations: "aspect-ratio:auto"}, nil
	}
	if ratio.MatchString(v) {
		return reading{declarations: "aspect-ratio:" + v}, nil
	}
	p := problem(in, "aspect-ratio là tỉ lệ rộng/cao bằng số nguyên (16/9, 4/3, 3/2, 21/9), square hoặc auto, không có \""+v+"\"")
	if w, h, ok := strings.Cut(v, ":"); ok && ratio.MatchString(strings.TrimSpace(w)+"/"+strings.TrimSpace(h)) {
		p.Suggestion = strings.TrimSpace(w) + "/" + strings.TrimSpace(h)
	} else if w, h, ok := strings.Cut(v, "/"); ok && ratio.MatchString(strings.TrimSpace(w)+"/"+strings.TrimSpace(h)) {
		p.Suggestion = strings.TrimSpace(w) + "/" + strings.TrimSpace(h)
	}
	return reading{}, p
}

// readFit reads fit. The value is read first, on every tag: fit="cove" is a typo wherever it stands.
// A word of fit on anything but an image does nothing there: a warning, no class.
func readFit(_ *Table, in Intent, v string) (reading, *Problem) {
	if v != "cover" && v != "contain" {
		return reading{}, problem(in, "fit nhận cover (lấp đầy khung, cắt phần thừa) hoặc contain (thấy trọn ảnh), không có \""+v+"\"")
	}
	if in.Tag != "" && in.Tag != "image" {
		return reading{inert: true}, warning(in, "fit chỉ đặt trên image (ảnh lấp khung thế nào), không đặt trên "+in.Tag)
	}
	return reading{declarations: "object-fit:" + v}, nil
}

func readScroll(_ *Table, in Intent, v string) (reading, *Problem) {
	switch v {
	case "x", "y":
		return reading{declarations: "overflow-" + v + ":auto"}, nil
	}
	return reading{}, problem(in, "scroll nhận x hoặc y, không có \""+v+"\"")
}

// readRadius reads radius, or one of its sides or corners: the same five words, each corner the
// properties name set to the word's radius.
func readRadius(properties ...string) func(*Table, Intent, string) (reading, *Problem) {
	return func(_ *Table, in Intent, v string) (reading, *Problem) {
		if r, ok := radii[v]; ok {
			declarations := make([]string, len(properties))
			for i, property := range properties {
				declarations[i] = property + ":" + r
			}
			return reading{declarations: strings.Join(declarations, ";")}, nil
		}
		p := problem(in, in.Name+" nhận none, small, medium, large hoặc full, không có \""+v+"\"")
		if word, ok := radiusReplaced[v]; ok {
			p.Suggestion = word
			p.Message = in.Name + "=\"" + v + "\" là chữ cũ của Tailwind: viết " + in.Name + "=\"" + word + "\" (" + radii[word] + "). Các chữ: none, small, medium, large, full"
		}
		return reading{}, p
	}
}

func readShadow(_ *Table, in Intent, v string) (reading, *Problem) {
	if s, ok := shadows[v]; ok {
		return reading{declarations: "box-shadow:" + s}, nil
	}
	p := problem(in, "shadow nhận small, medium hoặc large, không có \""+v+"\"")
	if word, ok := shadowReplaced[v]; ok {
		p.Suggestion = word
		p.Message = "shadow=\"" + v + "\" là chữ cũ của Tailwind: viết shadow=\"" + word + "\". Các chữ: small, medium, large"
	}
	return reading{}, p
}

func readFont(_ *Table, in Intent, v string) (reading, *Problem) {
	switch v {
	case "body":
		return reading{declarations: "font-family:var(--font-body,system-ui,sans-serif)"}, nil
	case "code":
		return reading{declarations: "font-family:var(--font-code,ui-monospace,monospace)"}, nil
	case "mono", "monospace":
		p := problem(in, "font theo vai trò trong router.fonts: code (không viết "+v+")")
		p.Suggestion = "code"
		return reading{}, p
	case "sans", "serif":
		p := problem(in, "font theo vai trò trong router.fonts: body (không viết "+v+")")
		p.Suggestion = "body"
		return reading{}, p
	}
	return reading{}, problem(in, "font là body hoặc code: họ chữ router.fonts khai cho chữ thường và chữ đều nét, không có \""+v+"\"")
}

// readTextSize reads size. On text it is a step of the body scale (huge is 24px). On a heading it is
// a step of the heading scale, the same five words sized for a heading and growing with the screen
// (KITWORK_UI §7): the class's own rule stays the body step, so one class serves both, and the
// heading's steps are scoped to h1–h6. A width-prefixed size on a heading takes the step's largest
// size from that width on (large:size="huge" is 72px from large).
func readTextSize(_ *Table, in Intent, v string) (reading, *Problem) {
	if s, ok := textSizes[v]; ok {
		read := reading{declarations: "font-size:" + s}
		if in.Tag == "heading" {
			step := headingSteps[v]
			if strings.TrimSpace(in.Width) != "" {
				read.scoped = []scopedRule{{selector: headingSelector,
					declarations: "font-size:" + step.sizes[len(step.sizes)-1] + ";letter-spacing:" + step.tracking}}
				return read, nil
			}
			for i, size := range step.sizes {
				d := "font-size:" + size
				if i == 0 {
					d += ";letter-spacing:" + step.tracking
				}
				read.scoped = append(read.scoped, scopedRule{selector: headingSelector, declarations: d, width: step.widths[i]})
			}
		}
		return read, nil
	}
	if word, ok := textSizeSpelledOut[v]; ok {
		p := problem(in, "size viết đủ chữ: "+word+" (không viết tắt "+v+"). Các bậc: mini, small, medium, large, huge")
		p.Suggestion = word
		return reading{}, p
	}
	return reading{}, problem(in, "size là mini, small, medium, large hoặc huge, không có \""+v+"\"; chữ to hơn huge là tiêu đề (heading)")
}

func readWeight(_ *Table, in Intent, v string) (reading, *Problem) {
	if w, ok := weights[v]; ok {
		return reading{declarations: "font-weight:" + strconv.Itoa(w), weight: w}, nil
	}
	accepts := "thin, extralight, light, normal, medium, semibold, bold, extrabold hoặc black"
	if word, ok := weightSpelledOut[v]; ok {
		p := problem(in, "weight=\""+v+"\": viết weight=\""+word+"\". Các chữ: "+accepts)
		p.Suggestion = word
		return reading{}, p
	}
	return reading{}, problem(in, "weight nhận "+accepts+", không có \""+v+"\"")
}

func readCase(_ *Table, in Intent, v string) (reading, *Problem) {
	switch v {
	case "upper":
		return reading{declarations: "text-transform:uppercase;letter-spacing:.14em"}, nil // capitals read better spaced apart
	case "lower":
		return reading{declarations: "text-transform:lowercase"}, nil
	}
	return reading{}, problem(in, "case nhận upper hoặc lower, không có \""+v+"\"")
}

// ---- colours ----

// colour reads a colour value (operators.go): a token of the palette (ink, brand, brand.on), a fixed
// colour (white, black, or a named colour: tomato, cerulean — named_colour.go), with at most one
// operator — lighter -N, N% toward the mode's canvas; stronger +N, N% away from it; a fixed colour has
// neither — then an optional alpha /N. It returns the CSS colour, the token, whether the value is the
// token alone, and the custom properties the CSS reads: a stronger colour is one per mode
// (--kitwork-strong-N-X), for the page's stylesheet to set (Rules.CSS). The old spellings, a shade .N
// and the rungs .soft, .wash and .deep, are refused with the new one before the palette is asked. An
// engine colour's name (kitwork-error, engine_colour.go) is no token, so no author's value: refused.
func (t *Table) colour(in Intent, v string) (css, token string, plain bool, custom []pairValue, p *Problem) {
	if before, after, ok := strings.Cut(v, ":"); ok {
		// ":" is the condition before an attribute's name, never part of a value (KITWORK_UI §7)
		p := problem(in, in.Name+"=\""+v+"\": dấu : không bao giờ nằm trong giá trị; độ hiện viết sau dấu /: "+before+"/"+after)
		p.Suggestion = before + "/" + after
		return "", "", false, nil, p
	}
	if p := t.oldColour(in, v); p != nil {
		return "", "", false, nil, p
	}
	c, amount, p := readOperators(in, v)
	if p != nil {
		return "", "", false, nil, p
	}
	if fixed, ok := fixedColour(c.Base); ok {
		if c.Op != 0 {
			return "", "", false, nil, fixedStepProblem(in, v, c.Base, amount)
		}
		css = fixed
	} else if t.palette.Has(c.Base) {
		css = "var(" + variable(c.Base) + ")"
	} else {
		return "", "", false, nil, t.unknownColour(in, v, c.Base)
	}
	if c.Op != 0 {
		n, ok := whole(amount, 0, 100)
		if !ok {
			return "", "", false, nil, amountProblem(in, v, c.Base, c.Op)
		}
		c.Amount = n
		if c.Op == '-' {
			css = "color-mix(in oklab," + css + " " + strconv.Itoa(100-n) + "%,var(--canvas))"
		} else {
			strong := t.palette.strongValue(c.Base, n)
			css, custom = "var("+strong.name+")", []pairValue{strong}
		}
	}
	if c.Translucent {
		_, alpha, _ := strings.Cut(v, "/")
		n, ok := whole(alpha, 0, 100)
		if !ok {
			return "", "", false, nil, problem(in, "độ hiện "+v+" là số nguyên từ 0 đến 100 sau dấu / (ink/80)")
		}
		css = "color-mix(in srgb," + css + " " + strconv.Itoa(n) + "%,transparent)"
	}
	return css, c.Base, c.Plain(), custom, nil
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func (t *Table) unknownColour(in Intent, v, base string) *Problem {
	if p := t.renamedGradient(in, v, base); p != nil {
		return p // lady-lips is pink-blush in background and color (gradients.go)
	}
	if p := misnamedColour(in, v, base); p != nil {
		return p // navy-blue is navy, slate-gray is slategray
	}
	if p := t.gradientMisused(in, v, base); p != nil {
		return p // border="glow", glow/50, Glow (gradients.go)
	}
	message := in.Name + "=\"" + v + "\": \"" + base + "\" không có trong bảng màu của trang" + nearestColours(base, t.colourNames(in)) + "."
	if base == "transparent" || base == "none" {
		message = in.Name + "=\"" + v + "\": không có màu \"" + base + "\"; trong suốt là token/0 (paper/0)."
	}
	return problem(in, message+" Token: "+strings.Join(t.palette.Tokens(), ", ")+"; "+namedColourWords()+t.gradientWords(in)+"; nhạt hơn token-N (ink-30), đậm hơn token+N (brand+15), độ hiện token/N (ink/80)")
}

func readColour(property string) func(*Table, Intent, string) (reading, *Problem) {
	return func(t *Table, in Intent, v string) (reading, *Problem) {
		css, _, _, custom, p := t.colour(in, v)
		if p != nil {
			return reading{}, p
		}
		// a stronger colour's custom property goes to the page's stylesheet, and a background hands a
		// glass its colour twice: as written, the fallback's, and without its /N, the glass's own
		// (glass first, background second: decision of 13/10/2026)
		read := reading{declarations: property + ":" + css, pairs: custom}
		if property == "background-color" {
			glass := css
			if tinted := GlassColour(v); tinted != v {
				glass, _, _, _, _ = t.colour(in, tinted) // read already, with its /N: no problem left
			}
			read.handover = handOver(css, glass)
		}
		if property == "color" && in.Tag == "badge" {
			// a badge is a tinted pill: an accent's fill made lighter (X-85) under its text, the engine
			// colour kitwork-badge-X, made stronger as far as it must to read in each mode (BadgeTint,
			// engine_colour.go): its custom property goes to the page's stylesheet per mode. Anything else
			// (ink, a surface, an operator, an alpha, a fixed colour) sits on the quiet panel. The fill goes
			// through --kitwork-tint, which the badge's own background reads (elements layer), so a written
			// background wins. BadgeTint decides it, and the contrast check reads the same pill.
			fill, text, family := t.BadgeTint(v)
			fillCSS, _, _, fillCustom, _ := t.colour(in, fill)
			tint := "--kitwork-tint:" + fillCSS
			read.pairs = append(read.pairs, fillCustom...)
			if family {
				textCSS, textCustom, _ := t.engineColour(text)
				tint += ";color:" + textCSS
				read.pairs = append(read.pairs, textCustom...)
			}
			read.scoped = []scopedRule{{selector: ":where(.badge)", declarations: tint}}
		}
		return read, nil
	}
}

func readBorder(t *Table, in Intent, v string) (reading, *Problem) {
	if v == "none" {
		// the 1px box stays: a button keeps its size, and forced colours still draw the edge
		return reading{declarations: "border-color:transparent"}, nil
	}
	css, _, _, custom, p := t.colour(in, v)
	if p != nil {
		p.Message = "border nhận none hoặc một màu. " + p.Message
		return reading{}, p
	}
	return reading{declarations: "border-width:1px;border-color:" + css, pairs: custom}, nil
}

func colourDomain(t *Table, _ Intent) []string { return t.palette.Declared() }

func borderDomain(t *Table, _ Intent) []string {
	return append([]string{"none"}, t.palette.Declared()...)
}
