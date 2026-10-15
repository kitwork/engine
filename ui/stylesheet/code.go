package stylesheet

import (
	"fmt"
	"strings"
)

// Code colours (decision of 12/10/2026): <code language="…"> is coloured on the server, one
// <span class="code-<kind>"> per token that is not plain text, each kind a name of jit/highlight's
// closed set (keyword, string, regular-expression…). By default a kind's colour is a ROLE of the
// palette, never a hex of its own, so code follows the site's router.colors and its dark look like every
// other text, and the contrast check measures it like any text (ui/contrast.go). A site's
// router.highlight in router.work may set a kind's colour of its own (highlight.go, 13/10/2026): a colour
// as color= takes it, or a { light, dark } pair; that kind then writes the author's colour and never its
// engine colour. The rule of a kind is in the page's own stylesheet, page.<hash>.css, only when the page
// can show that kind (Table.Code).
//
// What reboot.css paints behind code: nothing. :where(code, kbd, samp, pre) sets the code face and the
// size around it, :where(pre) scrolls sideways; a block shows the ground of what holds it — the page's
// canvas, a card's paper, a box written background="panel". So every colour below reaches 4.5:1 (WCAG
// 2.2 AA, normal text) on canvas, paper and panel, in light and in dark, on Kit Design's default palette,
// the kitwork example's and the quancaphe conformance fixture's (engine/conformance/testdata/ui/quancaphe,
// cases.json; code_test.go measures them all and fails on the first that falls short). inserted and
// deleted paint their own fill and are measured on it.

// CodeLook is how one kind of token looks: the colour of its text and, for a line a diff added or
// removed, the fill behind it (colour values of the palette, as an attribute writes them), its weight
// (0: the code's own), whether it is italic, and whether a reader can select it (the terminal's prompt
// is never selected nor copied). Share, with no Color, is the colour of the text around the token at
// that percent (color-mix with transparent): the terminal's prompt follows what the author writes on
// the terminal (color="paper" on a dark one), muted, and is measured so where it stands. Source is the
// call of router.highlight whose colour the kind takes (Table.TokenLook); nil for the engine's.
type CodeLook struct {
	Color, Background string
	Weight            int
	Italic            bool
	Unselectable      bool
	Share             int
	Source            *Source
}

// codeLooks is every kind but plain (plain text stays bare text, in the code's own colour). The same
// role serves kinds that never meet in one language (a tag and a variable), and kinds that read alike
// share one (number and constant): a few clear colours, not a rainbow.
//
// Each accent's colour is an engine colour (engine_colour.go), the accent moved further from the canvas
// (+N, operators of 13/10/2026) by the smallest multiple of 5 that reads 4.5:1 on canvas, paper and
// panel in each mode: in light from the step that read in both modes on every palette measured — Kit
// Design's, the kitwork example's and the conformance fixture's — close to the deep rung it replaces
// (brand +35, success +35, warning +55, info +25, danger +25), in dark from 0, so code keeps its hues
// (the accent itself on those palettes; decision of 13/10/2026). A diff line's fill is its accent 85% of
// the way to the canvas (the tinted fill of a badge, glow and tint), its text its own engine colour, read
// on that fill (light from +45 and +40).
var codeLooks = map[string]CodeLook{
	"keyword":            {Color: CodeColour("brand")},
	"type":               {Color: CodeColour("brand")},
	"string":             {Color: CodeColour("success")},
	"regular-expression": {Color: CodeColour("success")},
	"number":             {Color: CodeColour("warning")},
	"constant":           {Color: CodeColour("warning")},
	"attribute":          {Color: CodeColour("warning")},
	"function":           {Color: CodeColour("info")},
	"property":           {Color: CodeColour("info")},
	"variable":           {Color: CodeColour("danger")},
	"tag":                {Color: CodeColour("danger")},
	"comment":            {Color: "ink-30", Italic: true},
	"operator":           {Color: "ink-30"},
	"punctuation":        {Color: "ink-30"},
	"heading":            {Color: CodeColour("brand"), Weight: 700},
	"inserted":           {Color: CodeColour("inserted"), Background: "success-85"},
	"deleted":            {Color: CodeColour("deleted"), Background: "danger-85"},
	// the terminal's "$ " and "PS> ": the text around it at 70% (a fixed ink-30 was 1.83:1 on a dark
	// terminal, whatever the author wrote), and left out of a selection, so a copy is only commands
	"prompt": {Share: 70, Unselectable: true},
}

// CodeKinds lists the kinds that have a look, sorted: every kind of jit/highlight but plain.
func CodeKinds() []string { return sortedKeys(codeLooks) }

// CodeLookOf is a kind's look as the engine colours it; ok is false for plain and for a name that is not
// a kind.
func CodeLookOf(kind string) (CodeLook, bool) {
	look, ok := codeLooks[kind]
	return look, ok
}

// TokenLook is a kind's look on this table's route: the engine's (CodeLookOf), or, when router.highlight
// sets the kind (Palette.Highlight), its colour as the author wrote it — a colour value, the name of its
// pair (kitwork-highlight-<kind>, painted per mode), or for inserted and deleted the badge's engine colour
// on the accent 85% lighter — with Source set. Weight, italic and selection stay the kind's; the prompt's
// share of the text around it goes, its colour is the author's. ok is false for plain and for a name that
// is not a kind.
func (t *Table) TokenLook(kind string) (CodeLook, bool) {
	look, ok := codeLooks[kind]
	if !ok {
		return look, false
	}
	h, set := t.palette.Highlight(kind)
	if !set {
		return look, true
	}
	source := h.Source
	look.Source, look.Share = &source, 0
	switch {
	case accentKind(kind) && !h.paired():
		look.Background, look.Color, _ = t.BadgeTint(strings.TrimSpace(h.Color))
	case h.paired():
		look.Color = HighlightVariable(kind)
	default:
		look.Color = strings.TrimSpace(h.Color)
	}
	return look, true
}

// CodeClass is the class a token of a kind carries: code-keyword, code-regular-expression.
func CodeClass(kind string) string { return "code-" + kind }

// Code records the rule of a kind of token in r and returns its class (CodeClass): its colour — the
// author's when router.highlight sets the kind (highlightColours: as color= writes it, a pair's custom
// property, an accent's badge colour and fill), else through the palette's roles (an engine colour's
// custom property, kitwork-code-brand, set per mode on the page; ink lighter, mixed toward canvas, as
// color="ink-30" writes it) or a share of the text around it (the prompt) —, its fill, weight, italic and
// selection. A kind the author colours never reads its engine colour, so that custom property is set
// only when another kind of the page needs it. The weight goes to Rules.Weights, for the fonts file. A
// nil r only names the class. An error is a kind with no look, or a palette that cannot name its colour
// (every palette can: the roles are Kit Design's, which router.colors only overrides; an author's colour
// is checked in WithHighlight).
func (t *Table) Code(r *Rules, kind string) (string, error) {
	look, ok := codeLooks[kind]
	if !ok {
		return "", fmt.Errorf("không có màu cho loại chữ %q của khối mã (có: %s)", kind, strings.Join(CodeKinds(), ", "))
	}
	colour, fill, custom, err := t.codeColours(kind, look)
	if err != nil {
		return "", err
	}
	declarations := []string{"color:" + colour}
	if fill != "" {
		declarations = append(declarations, "background-color:"+fill)
	}
	read := reading{pairs: custom}
	if look.Italic {
		declarations = append(declarations, "font-style:italic")
	}
	if look.Weight != 0 {
		declarations = append(declarations, fmt.Sprintf("font-weight:%d", look.Weight))
		read.weight = look.Weight
	}
	if look.Unselectable {
		declarations = append(declarations, "-webkit-user-select:none", "user-select:none")
	}
	read.declarations = strings.Join(declarations, ";")
	class := CodeClass(kind)
	if r != nil {
		// after every attribute's rules in the page layer: a token's span carries its class alone, so no
		// other class of the page meets it on one element, and the place only keeps the file in one order
		r.record(attribute{name: "code"}, len(table), condition{}, kind, class, read)
	}
	return class, nil
}

// codeColours is the CSS of a kind's text and fill ("" for none) and the custom properties they read:
// the author's when router.highlight sets the kind (highlightColours), else the engine's — the text
// through the palette's roles, or the share of the text around it, and the kind's fill.
func (t *Table) codeColours(kind string, look CodeLook) (colour, fill string, custom []pairValue, err error) {
	if h, set := t.palette.Highlight(kind); set {
		return t.highlightColours(kind, h)
	}
	if look.Color == "" && look.Share > 0 {
		// the colour of the text around, at Share percent: currentColor in color is the inherited one
		colour = fmt.Sprintf("color-mix(in oklab,currentColor %d%%,transparent)", look.Share)
	} else {
		css, own, engine := t.engineColour(look.Color)
		if !engine {
			in := Intent{Name: "color", Value: look.Color}
			var p *Problem
			if css, _, _, own, p = t.colour(in, look.Color); p != nil {
				return "", "", nil, p
			}
		}
		colour = css
		custom = append(custom, own...)
	}
	if look.Background != "" {
		in := Intent{Name: "background", Value: look.Background}
		css, _, _, own, p := t.colour(in, look.Background)
		if p != nil {
			return "", "", nil, p
		}
		fill = css
		custom = append(custom, own...)
	}
	return colour, fill, custom, nil
}
