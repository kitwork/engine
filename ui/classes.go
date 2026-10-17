package ui

import (
	"fmt"
	"strings"

	"github.com/kitwork/engine/ui/stylesheet"
)

// Presentation attributes become classes named in the page's own words (KITWORK_UI §11, "Đầu ra web:
// class và CSS"): the attribute as written, [condition ":"] name "-" value — gap-4, medium:padding-6,
// large+hover:background-brand+15, color-ink-30, show-false. engine/ui/stylesheet reads each
// value against the route's colour chain and the .ui scales, refuses a word that is not .ui's at the
// attribute's line and column, warns there of a value it still draws as written (decision D), and
// records the rule of every class the page can carry in the page's own stylesheet (page.<hash>.css):
// a class exists only because its rule does.

// prefix is the "when" of an attribute name: target, width and state axes, from `ios,android+large:`.
type prefix struct {
	keep     bool     // the target axis allows the current build target
	widths   []string // width words as written: small, medium, large, wide, huge
	states   []string // state words as written: hover, pressed, open…
	explicit bool     // a target axis was written
}

var targetWords = map[string]bool{
	"website": true, "desktop": true, "mobile": true,
	"ios": true, "android": true, "windows": true, "macos": true, "linux": true,
}

// targetFamily maps a platform to its surface, so ios: also belongs to mobile.
var targetFamily = map[string]string{
	"ios": "mobile", "android": "mobile", "windows": "desktop", "macos": "desktop", "linux": "desktop",
}

// widthWords are the width prefixes, narrowest first (KITWORK_UI §8): where each starts is the
// stylesheet's (min-width 640, 768, 1024, 1280, 1536px).
var widthWords = map[string]bool{"small": true, "medium": true, "large": true, "wide": true, "huge": true}

// stateWords are the state prefixes. pressed is CSS :active (not Kitwork's active:, which is the link
// of the page being viewed); open follows the nearest element that opens; selected is the tab chosen
// in a tabs (aria-selected, which the engine binds, so a tab's look follows the choice with no
// expression, before the kernel too); scrolled holds while the page is scrolled away from its top (a
// header that floats up: scrolled:shadow="small").
var stateWords = map[string]bool{
	"open": true, "active": true, "selected": true, "scrolled": true, "hover": true, "focus": true, "pressed": true, "disabled": true,
}

// widthScaleWords names the width scale in order, for the error of a word outside it.
const widthScaleWords = "small (640px), medium (768px), large (1024px), wide (1280px) hoặc huge (1536px)"

// parsePrefix reads "ios,android+large" against a build target. It reports which axis each word is on
// and refuses mixing axes inside one ',' group or repeating an axis across '+'.
func parsePrefix(chain, target string) (prefix, string) {
	p := prefix{keep: true}
	seen := map[string]bool{}
	order := map[string]int{"target": 0, "width": 1, "state": 2}
	last := -1
	for _, group := range strings.Split(chain, "+") {
		words := strings.Split(group, ",")
		axis := ""
		for _, w := range words {
			a := ""
			switch {
			case targetWords[w]:
				a = "target"
			case widthWords[w]:
				a = "width"
			case stateWords[w]:
				a = "state"
			default:
				return p, "tiền tố không tồn tại: " + w + ":"
			}
			if axis == "" {
				axis = a
			} else if axis != a {
				return p, "dấu , chỉ nối tiền tố cùng một trục; khác trục thì nối bằng + (" + group + ")"
			}
		}
		if seen[axis] {
			return p, "một trục chỉ viết một lần; " + group + " lặp lại trục đã có"
		}
		seen[axis] = true
		if order[axis] < last {
			return p, "thứ tự tiền tố là đích → độ rộng → trạng thái (" + chain + ")"
		}
		last = order[axis]
		switch axis {
		case "target":
			p.explicit = true
			match := false
			for _, w := range words {
				if w == target || targetFamily[target] == w {
					match = true
				}
			}
			p.keep = match
		case "width":
			p.widths = append(p.widths, words...)
		case "state":
			p.states = append(p.states, words...)
		}
	}
	return p, ""
}

// intent is one presentation attribute as the stylesheet reads it: the prefix's width and state words
// as written (target words are decided here and never reach a class), the tag it sits on, the attribute.
func (p prefix) intent(tag, name, value string) stylesheet.Intent {
	return stylesheet.Intent{
		Width: strings.Join(p.widths, ","), State: strings.Join(p.states, ","),
		Tag: tag, Name: name, Value: value,
	}
}

// presentationName reports whether an attribute is presentation: one the stylesheet reads. show is
// read apart (show="{{ … }}" is live, the hidden attribute); thickness is the progressbar's own input.
func presentationName(name string) bool {
	return name != "show" && name != "thickness" && stylesheet.IsPresentation(name)
}

// intentTag is the tag the stylesheet reads an attribute against: a text that is a heading is
// "heading" (size is a step of the heading scale), every other element its .ui tag.
func (el *element) intentTag() string {
	if el.tag == "text" && len(el.html) == 2 && el.html[0] == 'h' {
		return "heading"
	}
	return el.tag
}

// class turns one presentation attribute into its class, through the route's table, and records its
// rule in the page's stylesheet. A value the stylesheet refuses is an error at the attribute; what it
// only warns of (decision D: columns="13", padding="17px", fit on a box) is a warning there, and the
// page renders as written. "" with no error means the attribute needs no class (strikethrough="true"
// is the <s> element, fit on a box does nothing).
func (c *compiler) class(n *Node, a *Attr, el *element, pre prefix, name, value string) (string, error) {
	class, _, err := c.readClass(n, a, el, pre, name, value)
	return class, err
}

// readClass is class that also says whether the stylesheet warned of the value: "" with a warning is a
// value that does nothing where it is written, "" without one a value its element says.
func (c *compiler) readClass(n *Node, a *Attr, el *element, pre prefix, name, value string) (string, bool, error) {
	class, warnings, err := c.table.Read(c.rules, pre.intent(el.intentTag(), name, strings.TrimSpace(value)))
	if err != nil {
		return "", false, errAttr(n, a, "%s", err.Error())
	}
	for _, w := range warnings {
		c.warn(n.File, a.Line, a.Column, w.Message)
	}
	if len(pre.widths) > 1 && a != nil && strings.Contains(class, ":") {
		// widths are min-width: small,large holds from small, so large says nothing (a warning: it works)
		// from is the narrowest width word; write is the class's whole prefix (small+hover), what to write
		from := stylesheet.Widths()[narrowest(pre.widths)-1]
		write := class[:strings.IndexByte(class, ':')]
		var wider []string
		seen := map[string]bool{from: true}
		for _, w := range pre.widths {
			if !seen[w] {
				seen[w] = true
				wider = append(wider, w)
			}
		}
		if len(wider) == 0 { // small,small: the same width twice
			c.warn(n.File, a.Line, a.Column, fmt.Sprintf("%s — độ rộng %s viết lặp lại: viết %s:%s",
				a.Name, from, write, name))
		} else {
			c.warn(n.File, a.Line, a.Column, fmt.Sprintf("%s — độ rộng là từ đó trở lên, nên %s đã gồm cả %s: viết %s:%s",
				a.Name, from, strings.Join(wider, ", "), write, name))
		}
	}
	return class, len(warnings) > 0, nil
}

// dynamicClass reads a presentation attribute whose value has {{ }} (render.go dynClass). Every value
// its expression spells out ({{ open ? 'brand' : 'paper' }}) is checked now, at the attribute, like a
// written value. A value from data ({{ order.tone }}) cannot be checked before it arrives, so the
// attribute's whole domain goes into the page's stylesheet — every token the palette declares for a
// colour, every step of a scale — and at render a value outside it makes no class, never CSS of its
// own (KITWORK_UI §8: no prefix goes with {{ }} from data yet).
func (c *compiler) dynamicClass(n *Node, a *Attr, el *element, pre prefix, name string, in *interpolation) (dynClass, error) {
	d := dynClass{name: name, in: in, classes: map[string]string{},
		where: fmt.Sprintf("%s:%d:%d", n.File, a.Line, a.Column), written: a.Name + `="` + a.Value + `"`}
	values := interpolationCandidates(in)
	fromData := values == nil
	for _, value := range values {
		if value == "" {
			continue
		}
		class, warned, err := c.readClass(n, a, el, pre, name, value)
		if err != nil {
			return d, err
		}
		if class == "" && warned {
			d.classes[value] = "" // does nothing here (fit on a box): no class, and the author was told
			continue
		}
		if class == "" {
			fromData = true // a value whose element says it unprefixed (strikethrough): its class from the domain
			continue
		}
		d.classes[value] = class
	}
	if fromData {
		domain, warnings, err := c.table.ReadDomain(c.rules, pre.intent(el.intentTag(), name, ""))
		if err != nil {
			return d, errAttr(n, a, "%s", err.Error())
		}
		for _, w := range warnings {
			c.warn(n.File, a.Line, a.Column, w.Message) // fit from data on a box: no value does anything here
		}
		for value, class := range domain {
			if _, written := d.classes[value]; !written {
				d.classes[value] = class
			}
		}
	}
	return d, nil
}

// engineClass is a class the engine writes without an attribute, through the same table (the
// background of a pinned element): its rule is in the page's stylesheet like any other.
func (c *compiler) engineClass(name, value string) string {
	return c.engineStateClass("", name, value)
}

// engineStateClass is engineClass in a state ("" none): the fill of a neutral button under the
// pointer, while pressed, while disabled (neutralSteps).
func (c *compiler) engineStateClass(state, name, value string) string {
	class, err := c.table.Class(c.rules, stylesheet.Intent{State: state, Name: name, Value: value})
	if err != nil {
		return "" // a word of the engine's own: never refused
	}
	return class
}

// sideWritten is a presentation attribute of one element as written, for what its left, right, start
// and end attributes say together (checkSides): the attribute, its name without the prefix, and the
// condition it holds under, the narrowest width (widths are min-width) and the states.
type sideWritten struct {
	attr   *Attr
	name   string
	width  int
	states []string
}

// sameCondition reports whether two attributes of one element hold under one condition: the same
// width, and no state on either or a state they share (hover,focus: and hover: both hold while
// hovered). A wider width is another condition: medium:padding-start over padding-left is the page
// growing, not two words for one side.
func sameCondition(a, b sideWritten) bool {
	if a.width != b.width || (len(a.states) == 0) != (len(b.states) == 0) {
		return false
	}
	if len(a.states) == 0 {
		return true
	}
	for _, s := range a.states {
		for _, t := range b.states {
			if s == t {
				return true
			}
		}
	}
	return false
}

// rightToLeftLanguages are the languages written right to left that router.language names, by their
// primary subtag: ar-EG, he-IL, fa-IR and ur-PK are theirs.
var rightToLeftLanguages = map[string]bool{"ar": true, "he": true, "fa": true, "ur": true}

// rightToLeft reports whether a language (router.language: "ar", "ar-EG", "he_IL") is written right to
// left; "" and every other language are left to right.
func rightToLeft(language string) bool {
	primary := strings.ToLower(strings.TrimSpace(language))
	if i := strings.IndexAny(primary, "-_"); i >= 0 {
		primary = primary[:i]
	}
	return rightToLeftLanguages[primary]
}

// sidePlace names in words the side or corners a physical left or right attribute sets.
func sidePlace(name string) string {
	side := "trái"
	if strings.HasSuffix(name, "-right") {
		side = "phải"
	}
	switch {
	case strings.HasPrefix(name, "padding-"):
		return "cạnh " + side
	case strings.HasPrefix(name, "radius-top-"):
		return "góc trên bên " + side
	case strings.HasPrefix(name, "radius-bottom-"):
		return "góc dưới bên " + side
	}
	return "hai góc bên " + side
}

// checkSides tells the author, while developing, what an element's left and right attributes say
// (decision 13/10/2026). left and right are the screen's and never flip; start and end are the line's
// and flip right to left. The page renders as written in both cases:
//   - a physical attribute and its logical twin under one condition (padding-left with padding-start,
//     radius-top-left with radius-top-start, or with -end on a site written right to left) set the same
//     side: the one later in the fixed order wins, whatever order they are written in, so the other
//     does nothing there. Keep one;
//   - on a site whose router.language is written right to left (ar, he, fa, ur), left is the end of the
//     line and right its start: a physical attribute is told the twin that keeps its place and flips.
//
// A site written left to right hears only of the twins.
func (c *compiler) checkSides(n *Node, written []sideWritten) {
	language := c.site.Language
	rtl := rightToLeft(language)
	direction := "từ trái sang phải"
	if rtl {
		direction = fmt.Sprintf("từ phải sang trái (router.language %q)", language)
	}
	for _, physical := range written {
		twin := stylesheet.Logical(physical.name, rtl)
		if twin == "" {
			continue // not a physical left or right
		}
		a := physical.attr
		if rtl {
			at, other, otherAt := "cuối", stylesheet.Logical(physical.name, false), "đầu"
			if strings.HasSuffix(physical.name, "-right") {
				at, otherAt = "đầu", "cuối"
			}
			c.warn(n.File, a.Line, a.Column, fmt.Sprintf("%s=%q: trang viết %s mà left và right không lật theo hướng viết, nên %s ở đây là phía %s dòng. Viết %s để giữ đúng chỗ này mà vẫn lật theo hướng viết (%s nếu ý là phía %s dòng)",
				a.Name, a.Value, direction, sidePlace(physical.name), at, twin, other, otherAt))
		}
		for _, logical := range written {
			if logical.name != twin || !sameCondition(physical, logical) {
				continue
			}
			winner, loser := twin, physical.name
			if stylesheet.Rank(physical.name) > stylesheet.Rank(twin) {
				winner, loser = physical.name, twin
			}
			c.warn(n.File, a.Line, a.Column, fmt.Sprintf("%s=%q và %s=%q (%s:%d:%d) cùng là %s khi dòng viết %s: %s thắng vì đứng sau %s trong thứ tự cố định, viết trước hay sau cũng vậy. Giữ một: %s lật theo hướng viết, %s thì không",
				a.Name, a.Value, logical.attr.Name, logical.attr.Value, n.File, logical.attr.Line, logical.attr.Column,
				sidePlace(physical.name), direction, winner, loser, twin, physical.name))
			break
		}
	}
}
