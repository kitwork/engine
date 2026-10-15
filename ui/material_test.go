package ui

import (
	"strings"
	"testing"
)

// material="none|glass|glass/N" (decision M; glass/N of 13/10/2026, N the tint's coverage from 10 to
// 95, glass is glass/75) goes on the frames that take a background:
// box, card, section, grid, list, form, the regions, popover. The class is the attribute as written,
// with width and state prefixes; the glass is made of the frame's own background, which the background
// classes hand over on a page with a material — brand-85 glass is background="brand-85"
// material="glass", the colour written once (brand glass itself carries no normal text that reads on
// Kit Design's brand: the contrast check measures it solid too). Glass first, background second
// (decision of 13/10/2026): the glass reads the background without its /N (--kitwork-glass), and only
// where the browser cannot blur the backdrop does the frame show its background as written
// (--kitwork-background); it shows also to a reader who asked for less transparency (decision of
// 13/10/2026).
func TestMaterialOnFrames(t *testing.T) {
	files := map[string]string{
		"/site/page.ui":   `<page><header /><content /></page>`,
		"/site/header.ui": `<header pin="top" scrolled:material="glass" padding="4"><text>Quán</text></header>`,
		"/site/content.ui": `<content><box background="brand-85" material="glass"><text>a</text></box>` +
			`<card medium:material="glass/60" large:material="none"><text>b</text></card>` +
			`<section material="glass/88" hover:background="paper"><text>c</text></section>` +
			`<box component="dropdown"><button click="toggle()">Mở</button><popover material="glass"><text>d</text></popover></box></content>`,
	}
	s := memorySite("", files)
	s.Reload = true
	html, css := renderWithCSS(t, s, ".", "/", nil)
	mustContain(t, html,
		// a pinned frame that writes no background gets the page's: the tint of its glass once scrolled
		`<header class="flex-column background-canvas pin-top scrolled:material-glass padding-4">`,
		`<div class="flex-column background-brand-85 material-glass">`,
		`<div class="card flex-column medium:material-glass/60 large:material-none">`,
		`<section class="flex-column material-glass/88 hover:background-paper">`,
		`<div class="popover flex-column material-glass" hidden`)
	mustContain(t, css,
		`.background-brand-85{background-color:color-mix(in oklab,var(--brand) 15%,var(--canvas));--kitwork-background:color-mix(in oklab,var(--brand) 15%,var(--canvas));--kitwork-glass:color-mix(in oklab,var(--brand) 15%,var(--canvas))}`,
		`.background-canvas{background-color:var(--canvas);--kitwork-background:var(--canvas);--kitwork-glass:var(--canvas)}`,
		`.material-glass{background-color:color-mix(in srgb,var(--kitwork-glass,var(--paper)) 75%,transparent);-webkit-backdrop-filter:blur(12px) saturate(180%);backdrop-filter:blur(12px) saturate(180%)}`,
		"@media (min-width:768px){\n"+`.medium\:material-glass\/60{background-color:color-mix(in srgb,var(--kitwork-glass,var(--paper)) 60%,transparent);`,
		"@media (min-width:1024px){\n"+`.large\:material-none{background-color:var(--kitwork-background,transparent);`,
		`.material-glass\/88{background-color:color-mix(in srgb,var(--kitwork-glass,var(--paper)) 88%,transparent);-webkit-backdrop-filter:blur(20px) saturate(180%);`,
		`.scrolled\:material-glass:where(:root[data-kitwork-scrolled] *){background-color:color-mix(in srgb,var(--kitwork-glass,var(--paper)) 75%,transparent);`,
		`.scrolled\:material-glass{transition:background-color .15s,-webkit-backdrop-filter .15s,backdrop-filter .15s}`,
		`@supports not ((backdrop-filter:blur(1px)) or (-webkit-backdrop-filter:blur(1px))){.material-glass{background-color:var(--kitwork-background,var(--paper))}}`)
	// the glass always shows (decision of 13/10/2026): its one fallback is a browser that cannot blur the
	// backdrop, none gives way to the reader's prefers-reduced-transparency
	mustNotContain(t, css, "prefers-reduced-transparency")
	// the hover background still wins over the base glass while the pointer is on it
	if strings.Index(css, `.material-glass\/88{`) > strings.Index(css, `.hover\:background-paper:where(:hover)`) {
		t.Errorf("hover comes after the base material:\n%s", css)
	}
	if !compiled(t, s).UsesScrolled() {
		t.Error("scrolled:material is a scrolled: class: the page needs the flag")
	}
	// control: the same page without a material has no variable in its backgrounds
	files["/site/header.ui"] = `<header pin="top" padding="4"><text>Quán</text></header>`
	files["/site/content.ui"] = `<content><box background="brand-85"><text>a</text></box></content>`
	_, css = renderWithCSS(t, s, ".", "/", nil)
	mustContain(t, css, ".background-brand-85{background-color:color-mix(in oklab,var(--brand) 15%,var(--canvas))}\n", ".background-canvas{background-color:var(--canvas)}\n")
	mustNotContain(t, css, "--kitwork-background", "--kitwork-glass", "backdrop-filter")
}

// material from data compiles every word ahead, like any presentation attribute from data.
func TestMaterialFromData(t *testing.T) {
	html, css := renderCSS(t, `<content><box material="{{ look }}"><text>a</text></box></content>`, map[string]any{"look": "glass/60"})
	mustContain(t, html, `class="flex-column material-glass/60"`)
	mustContain(t, css, `.material-none{`, `.material-glass\/60{`, `.material-glass{`, `.material-glass\/88{`)
}

// material goes on a frame only: on text, an inline element, a control, an image or an icon it is a
// warning at the attribute, in the author's language, naming the frames it goes on; on <page> it says
// nothing lies behind the page. A word it does not take names what it takes (an error); an old word
// of the glass (glass.thin, 13/10/2026) and a coverage outside 10 to 95 are errors that name the
// spelling to write.
func TestMaterialErrors(t *testing.T) {
	const frames = "material là chất liệu của một khung (box, card, section, header, footer, popover…)"
	cases := []struct{ name, content, at, says string }{
		{"a word it does not take", `<content><box material="frosted"><text>a</text></box></content>`, "content.ui:1:15", `material="frosted": viết material="glass"`},
		{"an old word of the glass", `<content><box material="glass.thin"><text>a</text></box></content>`, "content.ui:1:15", "kính mỏng giờ là glass/60"},
		{"a coverage outside 10 to 95", `<content><box material="glass/100"><text>a</text></box></content>`, "content.ui:1:15", "N sau glass/ là độ phủ của màu kính, số nguyên từ 10 đến 95"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mustFail(t, map[string]string{"/site/content.ui": c.content}, c.at, c.says)
		})
	}
	// on <page> (and on what is no frame: diagnostics_compile_test.go) it is drawn as written and warned of
	compileWarns(t, map[string]string{"/site/page.ui": `<page material="glass"><content /></page>`, "/site/content.ui": `<content><text>a</text></content>`},
		"page.ui:1:7: ", frames+"; <page> là nền của cả trang, phía sau không có gì để nhìn qua")
	// control: the same words on frames compile
	mustCompile(t, map[string]string{"/site/content.ui": `<content><box material="glass/80"><text>a</text></box><grid columns="2" material="glass/60"><box /></grid>` +
		`<list material="glass/88"><item material="none">x</item></list><form material="glass"><box /></form></content>`})
}

// A background with /N next to a material says nothing (decision of 13/10/2026, glass first,
// background second): its /N is the opacity of the fallback, where the browser cannot blur, a design
// choice; the glass is its colour without the /N at the material's coverage. Every case the warning
// "material quyết độ trong" once stood at compiles without a word: the same condition, scrolled, the
// base background under a prefixed glass, one a {{ }} spells out, a material from data, a wider width,
// an earlier state, a width and a state. CONTROL: a frame that writes no background stays paper glass,
// and the page carries the two variables a /N background hands over; a value the table refuses is
// still an error.
func TestMaterialAlphaBackgroundIsSilent(t *testing.T) {
	for _, source := range []string{
		`<content><box background="paper/80" material="glass"><text>a</text></box></content>`,
		`<content><box pin="top" scrolled:material="glass/30" scrolled:background="paper/80"><text>a</text></box></content>`,
		`<content><box background="paper/80" scrolled:material="glass/60"><text>a</text></box></content>`,
		`<content><box background="{{ open ? 'paper/80' : 'brand-85' }}" material="glass"><text>a</text></box></content>`,
		`<content><box background="paper/80" material="{{ look }}"><text>a</text></box></content>`,
		`<content><box scrolled:material="glass" medium:background="paper/80"><text>a</text></box></content>`,
		`<content><box hover:material="glass" scrolled:background="paper/80"><text>a</text></box></content>`,
		`<content><box medium+hover:material="glass" large:background="paper/80"><text>a</text></box></content>`,
		`<content><box background="brand-85/80" material="glass/70"><text>a</text></box></content>`,
	} {
		compileQuiet(t, map[string]string{"/site/content.ui": source})
	}
	html, css := renderCSS(t, `<content><box background="paper/80" material="glass/30"><text>a</text></box><box material="glass"><text>b</text></box></content>`, nil)
	mustContain(t, html, `class="flex-column background-paper/80 material-glass/30"`, `class="flex-column material-glass"`)
	mustContain(t, css,
		`.background-paper\/80{background-color:color-mix(in srgb,var(--paper) 80%,transparent);--kitwork-background:color-mix(in srgb,var(--paper) 80%,transparent);--kitwork-glass:var(--paper)}`,
		`.material-glass\/30{background-color:color-mix(in srgb,var(--kitwork-glass,var(--paper)) 30%,transparent);-webkit-backdrop-filter:blur(8px) saturate(180%);`,
		`{.material-glass\/30{background-color:var(--kitwork-background,var(--paper))}}`)
	mustFail(t, map[string]string{"/site/content.ui": `<content><box background="paper/180" material="glass"><text>a</text></box></content>`},
		"content.ui:1:15", "paper/180")
}

// A pinned frame that writes no background takes the page's, solid: on <page background="paper/80">
// it is paper, so the content scrolling under it does not show through, and its glass is paper at the
// glass's own opacity, not paper/80 read by a glass. CONTROL: the page keeps the alpha it writes.
func TestMaterialPinnedOnATranslucentPage(t *testing.T) {
	s := memorySite("", map[string]string{
		"/site/page.ui":    `<page background="paper/80"><header /><content /></page>`,
		"/site/header.ui":  `<header pin="top" scrolled:material="glass" padding="4"><text>Quán</text></header>`,
		"/site/content.ui": `<content><text>a</text></content>`,
	})
	s.Reload = true
	html, _ := renderWithCSS(t, s, ".", "/", nil)
	mustContain(t, html, `<header class="flex-column background-paper pin-top scrolled:material-glass padding-4">`, `background-paper/80`)
	mustNotContain(t, html, `background-paper/80 pin-top`)
}
