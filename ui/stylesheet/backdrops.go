package stylesheet

import "strings"

// Page backdrops (decision D1 of 13/10/2026). Twelve gradients made for <page background="…">: the
// soft, deep ground of a desktop interface, one family of hue each, round the wheel. They are gradients
// like the standard set — the same namespace, usable anywhere a gradient is (background="dusk" on a
// band, color="cirrus" on a headline) — but measured for a page: ink reads on every point of each (at
// least 4.5:1 in light and in dark, on Kit Design's palette, the kitwork example's and the quancaphe
// conformance fixture's), and a glass card (paper at 75%) with ink-30 on it reads over every point too.
// Secondary text belongs on glass: ink-30 straight on a backdrop falls short (2.5–3.2:1 in light).
//
// Each is a linear ramp to the bottom end, the geometry of the reference it was fitted to: a coloured
// corner at 0, a middle at 34 on the same family, a quiet neutral at 67 held to the far end. Light
// mode thins from a corner of lightness .70–.775 into a light neutral of about .88; dark mode holds its
// chroma through the middle (so a dark glass card stays told apart from the page where the page
// passes through the dark paper's lightness) and falls into a soft black of about .17. Every stop is a
// { light, dark } pair (decision D2), the same in every palette. A page wanting a quieter ground writes
// it lighter, background="cirrus-30" (decision D3, its operators of 13/10/2026): in dark it pulls the
// page toward the canvas, so a glass dashboard keeps the full gradient.
//
// On a page (decision D4) the backdrop is painted on a layer fixed to the screen, behind everything:
// the element the compiler writes first in <body>, <div class="page-backdrop background-cirrus"
// aria-hidden="true" data-kitwork-backdrop>, positioned by Backdrop's rule (a fixed element, not
// background-attachment:fixed, which iOS does not draw). The gradient spans the screen and stays while
// the document scrolls, never stretched over the document's height; <html> takes the gradient's first
// stop for its ground (overscroll, the strip under a mobile browser's bars), and the document's
// <meta name="theme-color"> the same colour per mode (ui, Page.Document) — for a gradient every stop of
// which paints solid, as each of the twelve does; one that fades to transparent leaves both to the
// canvas and the browser. Where the brand, the palette's focus ring, falls short of 3:1 on the backdrop
// (every one of the twelve in light, on Kit Design's palette), the page's ring is ink. Drive keeps the
// layer on a move between pages of one page.ui (jit/hydrate/drive.js); another backdrop is another
// page.ui, reached by a whole load.

// backdrop is a page backdrop: a pair at 0, at 34 and at 67, to the bottom end.
func backdrop(light0, dark0, light34, dark34, light67, dark67 string) Gradient {
	return linear("to-bottom-end",
		Stop{Pair: Color{Light: light0, Dark: dark0}, At: 0},
		Stop{Pair: Color{Light: light34, Dark: dark34}, At: 34},
		Stop{Pair: Color{Light: light67, Dark: dark67}, At: 67})
}

// backdropGradients are the twelve, round the hue wheel from blue: designed and measured 13/10/2026.
var backdropGradients = []standardGradient{
	// a periwinkle morning sky thinning into cool grey cloud; at night a deep periwinkle easing toward indigo
	{"cirrus", backdrop("#849cdd", "#202f5e", "#a9bbe0", "#181948", "#cdd9e2", "#0d0f15")},
	// clear sky blue over still water, the everyday calm blue; at night deep harbour blue
	{"harbor", backdrop("#72acd5", "#00355a", "#a1c3dc", "#001e4a", "#cfdae2", "#0b1014")},
	// the blue-teal of a quiet lake easing into a grey mist
	{"still-water", backdrop("#6cb3c3", "#003a44", "#9dc7d1", "#002627", "#ccdbdf", "#091113")},
	// pale aqua spray settling onto a cool grey shore
	{"sea-mist", backdrop("#71b8b5", "#003b3a", "#9fcac8", "#002120", "#ccdcdb", "#091111")},
	// jade leaves after a spring shower, fading into a pale green-grey
	{"spring-rain", backdrop("#80b9a1", "#003d2b", "#a8cbbb", "#002217", "#cedcd5", "#0a110e")},
	// dusty sage in soft rain, the quietest of the greens
	{"rain-garden", backdrop("#97b78c", "#203b16", "#b5c9af", "#082300", "#d3dbd0", "#0d110c")},
	// warm sand in late light, fading into unbleached linen; at night a warm umber
	{"dune", backdrop("#ceb183", "#452d0a", "#d6c4a8", "#2a1200", "#ded7cc", "#130f0a")},
	// the soft apricot of the last hour of sun, fading into cream; at night an ember
	{"golden-hour", backdrop("#d6a47e", "#4f2700", "#dcbda6", "#311100", "#e1d6cd", "#130e0a")},
	// dusty rose fading into a warm blush-grey; at night a deep wine
	{"rose-haze", backdrop("#d7979e", "#541e28", "#ddb6ba", "#380512", "#e3d4d5", "#140d0e")},
	// mauve evening haze fading into a soft lilac-grey; at night plum
	{"dusk", backdrop("#ca92b9", "#4c2041", "#d5b3cb", "#310828", "#e0d4dc", "#130d11")},
	// lilac evening light, quiet and velvety, settling into pearl-grey
	{"evensong", backdrop("#b894ce", "#412452", "#cab5d7", "#300f3a", "#dcd5e0", "#110e13")},
	// the indigo-lilac of the sky just after sunset, settling into lavender-grey; at night indigo deepening to violet
	{"twilight", backdrop("#a097d9", "#33295a", "#bcb7de", "#2d113d", "#d8d6e3", "#0f0f14")},
}

// Backdrops lists the page backdrops by name, in their designed order, round the hue wheel from blue.
func Backdrops() []string {
	out := make([]string, len(backdropGradients))
	for i, s := range backdropGradients {
		out[i] = s.name
	}
	return out
}

// BackdropClass is the class of the layer a page backdrop is painted on: the element the compiler
// writes first in <body>, carrying <page>'s background classes, and that Backdrop positions.
const BackdropClass = "page-backdrop"

// Backdrop records in r what a page whose <page> paints a gradient needs besides the classes of its
// background (those go on the backdrop layer, BackdropClass): the layer fixed to the screen behind
// everything — inset 0 and at least the large viewport tall, so a mobile browser's bars retracting never
// uncover the bottom; under every element of the page (z-index -1 in the root stacking context, over
// the ground of <html> only); taking no pointer; no space in the flow, so nothing shifts — and, when
// ground (the background <page> writes without a prefix) is a gradient of the palette:
//   - when every stop of it paints solid in every mode (the twelve backdrops), <html>'s ground in its
//     first stop, per mode: what a reader sees when the page is pulled past its edge, and what
//     Page.Document gives the browser's bar (Rules.Backdrop says it). A gradient that fades to
//     transparent (glow, tint, the scrims) leaves <html> on the canvas reboot.css gives it and the bar
//     to the browser: the layer's see-through part shows the canvas, as the contrast check composites
//     it, not a flat first stop over the whole screen;
//   - the keyboard focus ring of each mode where the palette's, the brand, falls short of 3:1 on a point
//     of the backdrop (focusOver): a control on the page has its ring drawn over the backdrop.
//
// It reports whether ground is a gradient: "" or a colour records the layer alone.
func (t *Table) Backdrop(r *Rules, ground string) bool {
	ground = strings.TrimSpace(ground)
	g, ok := t.palette.Gradient(ground)
	if !ok {
		ground = ""
	}
	html := ""
	var pairs []pairValue
	var focus [2]string
	if ok {
		if t.palette.solidGradient(g) {
			html = "background-color:" + t.solidFirstStop(g)
			pairs = t.pairsOf(g)
		}
		focus = t.palette.focusOver(ground)
	}
	r.backdrop(ground, html, pairs, focus)
	return ok
}

// solidGradient reports whether every stop of a gradient paints solid in every mode of the palette,
// the page's step (-N, +N) taken: a backdrop the whole screen is made of, never one that fades to
// transparent.
func (p *Palette) solidGradient(g Gradient) bool {
	for _, m := range p.Modes() {
		for _, s := range g.Stops {
			if c, ok := p.paintStop(g, s, m); !ok || !c.Opaque() {
				return false
			}
		}
	}
	return true
}

// focusOver is the keyboard focus ring a page whose backdrop is the gradient name asks for, light then
// dark ("" keeps the palette's, colors.<hash>.css): ink where the palette's ring is the brand (focusOn,
// which reads it on the canvas and the paper) and the brand falls short of 3:1, WCAG's non-text
// contrast, on a point of the backdrop over the canvas — the eleven points the contrast check reads.
// The ring stands 2px out from a control, on what lies behind it: on such a page, the backdrop.
func (p *Palette) focusOver(name string) [2]string {
	var out [2]string
	for _, m := range p.Modes() {
		i := 0
		if m == DarkMode {
			i = 1
		}
		samples, ok := p.GradientSamples(name, m)
		if p.focus[i] != "brand" || !ok {
			continue
		}
		brand, _ := p.Paint("brand", m)
		canvas, _ := p.Paint("canvas", m)
		for _, s := range samples {
			if Ratio(brand.Painted(), s.Over(canvas.Painted()).Painted()) < NonTextRatio {
				out[i] = "ink"
				break
			}
		}
	}
	return out
}

// backdropLayer is the rule of the backdrop layer (Backdrop).
const backdropLayer = "position:fixed;inset:0;min-block-size:100lvh;z-index:-1;pointer-events:none"
