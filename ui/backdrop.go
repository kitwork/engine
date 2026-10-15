package ui

import (
	"strings"

	"github.com/kitwork/engine/ui/stylesheet"
)

// Page backdrops (decision D4 of 13/10/2026): <page background="cirrus"> paints the gradient on a layer
// fixed to the screen, behind all content, never stretched over the document's height. The layer is an
// element the compiler writes first in <body>, <div class="page-backdrop background-cirrus"
// aria-hidden="true" data-kitwork-backdrop>, carrying every background class <page> writes (a colour
// written for a width paints the same layer: the classes cascade on it as they would on <body>), and
// <body> keeps none, so nothing of its own covers the layer. A real element, not a pseudo-element: Drive
// sees it and keeps it on a move between pages of one page.ui (the same node, no repaint;
// jit/hydrate/drive.js). Another backdrop is another page.ui, and a move between two layouts is a whole
// load, as it always was: the browser keeps the old frame until the new page paints, so nothing
// flashes. Drive's swapBackdrop also replaces or removes the layer, for a move that keeps the shell
// with another backdrop; no .ui page makes one today, since a backdrop comes from page.ui alone, never
// from data. The layer's rule, the keyboard focus ring where the brand's falls short on the backdrop,
// and, for a backdrop that paints solid everywhere, <html>'s ground in the gradient's first stop
// (overscroll, the strip under a mobile browser's bars), are the page's stylesheet's
// (stylesheet.Table.Backdrop); the document's <meta name="theme-color"> is the same colour per mode,
// the mode shown deciding it (themeColor). The contrast check reads the page's background as it reads
// any gradient: text and glass over the page are measured against every sample of it, since the fixed
// layer shows each part of the gradient behind some part of the content.
//
// A backdrop is written plainly: a background of <page> from data ({{ }}) beside a gradient, or one that
// spells a gradient out ({{ night ? 'dusk' : 'cirrus' }}), is refused, since the layer is written once
// with the page's classes and a value from data would change only <body>'s. A frame pinned to an edge
// with no background of its own takes the canvas on a backdrop page (a gradient on a header box is not
// the screen's); a pinned glass takes nothing, so the backdrop shows through the glass.

// pageBackdrop moves <page>'s background classes onto the backdrop layer when one of them paints a
// gradient, records the layer's rules (and the ground of <html> when the background written without a
// prefix is the gradient), and returns the layer, the first child of <body>. nil for any other element,
// and for a page whose backgrounds are colours: they stay on <body>.
func (c *compiler) pageBackdrop(n *Node, p *attrPlan) (op, error) {
	if n.Tag != "page" {
		return nil, nil
	}
	gradient, ground := false, ""
	var fromData *Attr
	for i := range n.Attrs {
		a := &n.Attrs[i]
		if a.Name[strings.LastIndexByte(a.Name, ':')+1:] != "background" {
			continue
		}
		v := strings.TrimSpace(a.Value)
		if strings.Contains(v, "{{") {
			fromData = a
			// a gradient it spells out ({{ night ? 'dusk' : 'cirrus' }}) would be a backdrop from data:
			// refused as the rest is, never left on <body> stretched over the document
			if in, err := parseInterpolation(a.Value, n, a); err == nil {
				literals, _ := interpolationLiterals(in)
				for _, l := range literals {
					gradient = gradient || c.isGradient(l)
				}
			}
			continue
		}
		if c.isGradient(v) {
			gradient = true
			if a.Name == "background" {
				ground = v
			}
		}
	}
	if !gradient {
		return nil, nil
	}
	if fromData != nil {
		return nil, errAttr(n, fromData, "%s=\"%s\": <page> có nền gradient, vẽ trên một lớp cố định theo màn hình (nền trang); nền của trang viết thẳng, không lấy từ dữ liệu: lớp nền viết một lần với các class của trang, giá trị từ {{ }} chỉ đổi <body>",
			fromData.Name, strings.TrimSpace(fromData.Value))
	}
	var layer, rest []string
	for _, class := range p.classes {
		if strings.HasPrefix(class[strings.LastIndexByte(class, ':')+1:], "background-") {
			layer = append(layer, class)
			continue
		}
		rest = append(rest, class)
	}
	p.classes = rest
	c.table.Backdrop(c.rules, ground)
	var b strings.Builder
	b.WriteString("<div")
	writeAttr(&b, "class", strings.Join(append([]string{stylesheet.BackdropClass}, layer...), " "))
	b.WriteString(` aria-hidden="true" data-kitwork-backdrop></div>`)
	return &staticOp{s: b.String()}, nil
}

// pinnedGround is the background the engine gives a frame pinned to an edge that writes none: the
// page's colour (<page background="paper/80"> pins paper), else the canvas. On a page whose background
// is a gradient — a backdrop, fixed to the screen — the canvas, since a gradient on the frame's own box
// is not the screen's; and nothing for a glass, which shows the backdrop through it (ok false).
func (c *compiler) pinnedGround(n *Node, cx ctx) (background string, ok bool) {
	background, _, _ = strings.Cut(cx.pageBackground, "/") // solid: <page background="paper/80"> pins paper
	switch {
	case background == "":
		return "canvas", true
	case !c.isGradient(background):
		return background, true
	case writesAttribute(n, "material"):
		return "", false
	}
	return "canvas", true
}

// themeColor is the document's <meta name="theme-color">, the colour a mobile browser paints its bar
// and the strip around the page in, when the page's backdrop is a gradient that paints solid: its first
// stop, as <html>'s ground (stylesheet.Palette.GradientGround). In the page's appearance when it fixes
// one (router.appearance "light" or "dark"), or when the palette has no dark look; otherwise one per
// mode, for the device's (media="(prefers-color-scheme: …)"). With a dark look each also carries the
// colour of both modes, data-kitwork-light and data-kitwork-dark: the mode the page shows decides the
// bar, a visitor's choice of the other mode than the device's too — the pre-paint (jit/theme) and
// kit.appearance (jit/hydrate/modules/web.js) set the content of that mode and drop the media, and a
// move by Drive keeps the mode shown. Without a script the device decides, as before. "" for a page
// without such a backdrop.
func (p *Page) themeColor(appearance string) string {
	if p.rules == nil {
		return ""
	}
	ground := p.rules.Backdrop()
	if ground == "" {
		return ""
	}
	palette := p.palette
	if palette == nil {
		palette, _ = stylesheet.NewPalette(false)
	}
	hex := func(m stylesheet.Mode) string {
		c, _ := palette.GradientGround(ground, m)
		return c.Painted().Hex()
	}
	light, dark, modes := hex(stylesheet.LightMode), "", ""
	if palette.Dark() {
		dark = hex(stylesheet.DarkMode)
		modes = ` data-kitwork-light="` + light + `" data-kitwork-dark="` + dark + `"`
	}
	meta := func(content, media string) string {
		out := `<meta name="theme-color"`
		if media != "" {
			out += ` media="` + media + `"`
		}
		return out + ` content="` + content + `"` + modes + ">\n"
	}
	switch {
	case !palette.Dark() || appearance == "light":
		return meta(light, "")
	case appearance == "dark":
		return meta(dark, "")
	}
	return meta(light, "(prefers-color-scheme: light)") + meta(dark, "(prefers-color-scheme: dark)")
}
