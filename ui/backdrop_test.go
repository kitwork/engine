package ui

import (
	"strings"
	"testing"

	"github.com/kitwork/engine/ui/stylesheet"
)

// backdropSite is a site whose page.ui writes the page's background, with a header pinned to the top
// and a glass one, and a dark look.
func backdropSite(t *testing.T, page string, palette *stylesheet.Palette) *Page {
	t.Helper()
	s := memorySite("", map[string]string{
		"/site/page.ui":    page,
		"/site/content.ui": `<content><text>Trà</text></content>`,
	})
	s.Reload = true
	s.Palette = palette
	return compiled(t, s)
}

// <page background="cirrus"> (decision D4 of 13/10/2026) paints the gradient on a layer fixed to the
// screen: an element first in <body> carrying <page>'s background classes — a colour written for a
// width with them, the classes cascading on the layer as they would on <body> — and <body> keeps none
// (its own background would cover the layer). The page's stylesheet positions the layer (fixed, inset 0,
// at least the large viewport tall, under everything, taking no pointer) and gives <html> the
// gradient's first stop, the pair's custom property, so overscroll and the strip under a mobile
// browser's bars match; cirrus lighter (cirrus-80) is the same layer with the mixed stops. A pinned
// header with no background takes the canvas (a gradient on its own box is not the screen's), a
// pinned glass none, so the backdrop shows through it. CONTROL: <page background="paper"> keeps its
// class on <body>, with no layer and no rule for one.
func TestPageBackdropIsALayerFixedToTheScreen(t *testing.T) {
	palette := darkPalette(t)
	page := backdropSite(t, `<page background="cirrus" medium:background="paper" padding="4">`+
		`<header pin="top"><text>Quán</text></header><header pin="top" material="glass"><text>Kính</text></header><content /></page>`, palette)
	body := page.Render(Request{Path: "/"})
	css := string(page.Stylesheet().CSS)
	mustContain(t, body,
		`<body class="flex-column padding-4" data-kitwork-layout="page.ui"><div class="page-backdrop background-cirrus medium:background-paper" aria-hidden="true" data-kitwork-backdrop></div>`,
		`<header class="flex-column background-canvas pin-top">`,
		`<header class="flex-column pin-top material-glass">`)
	mustContain(t, css,
		":root{--kitwork-gradient-cirrus-1:#849cdd;--kitwork-gradient-cirrus-2:#a9bbe0;--kitwork-gradient-cirrus-3:#cdd9e2}\n",
		"@media screen{:root[data-theme=dark]{--kitwork-gradient-cirrus-1:#202f5e;--kitwork-gradient-cirrus-2:#181948;--kitwork-gradient-cirrus-3:#0d0f15}}\n",
		"@layer page{\n:where(html){background-color:var(--kitwork-gradient-cirrus-1)}\n:where(.page-backdrop){position:fixed;inset:0;min-block-size:100lvh;z-index:-1;pointer-events:none}\n",
		".background-cirrus{background-image:linear-gradient(to bottom right,var(--kitwork-gradient-cirrus-1) 0%,var(--kitwork-gradient-cirrus-2) 34%,var(--kitwork-gradient-cirrus-3) 67%)}",
		// the colour written for medium takes the image away on the layer that carries both (TakeBack)
		`.medium\:background-paper:where(.background-cirrus){background-image:none}`)
	if strings.Count(body, "background-cirrus") != 1 {
		t.Errorf("a pinned frame never takes the backdrop's gradient on its own box, a glass takes nothing:\n%s", body)
	}

	shaded := backdropSite(t, `<page background="cirrus-80"><content /></page>`, palette)
	mustContain(t, shaded.Render(Request{Path: "/"}), `<div class="page-backdrop background-cirrus-80" aria-hidden="true" data-kitwork-backdrop></div>`)
	mustContain(t, string(shaded.Stylesheet().CSS),
		":where(html){background-color:color-mix(in oklab,var(--kitwork-gradient-cirrus-1) 20%,var(--canvas))}")

	control := backdropSite(t, `<page background="paper"><header pin="top"><text>Quán</text></header><content /></page>`, palette)
	controlBody, controlCSS := control.Render(Request{Path: "/"}), string(control.Stylesheet().CSS)
	mustContain(t, controlBody, `<body class="flex-column background-paper" data-kitwork-layout="page.ui">`, `<header class="flex-column background-paper pin-top">`)
	mustNotContain(t, controlBody, "page-backdrop", "data-kitwork-backdrop")
	mustNotContain(t, controlCSS, "page-backdrop", ":where(html)", "--kitwork-gradient-")
}

// The document's <meta name="theme-color"> is the backdrop's first stop, solid, per mode: one for each
// of the device's modes, media="(prefers-color-scheme: …)", when the page follows the device; one, in
// that mode, when router.appearance fixes it, or when the palette has no dark look. With a dark look each
// carries both modes' colours too (data-kitwork-light, data-kitwork-dark), for the mode the page shows to
// decide the bar (TestBrowserPageBackdropBarFollowsTheMode). Written lighter, the
// mixed colour: cirrus-80 is 20% of #849cdd and 80% of the canvas #f6f9fc in light, #dee6f7, and 20% of
// #202f5e and 80% of the dark canvas #0f1822 in dark, #121d2d (both mixed in oklab by an implementation
// of CSS Color 4's sample code, through XYZ, apart from the engine's). CONTROL: a page without a
// backdrop, or with a colour background, has none.
func TestPageBackdropThemeColor(t *testing.T) {
	palette := darkPalette(t)
	page := backdropSite(t, `<page background="cirrus"><content /></page>`, palette)
	const modes = ` data-kitwork-light="#849cdd" data-kitwork-dark="#202f5e">` + "\n"
	mustContain(t, page.Document(Request{Path: "/"}),
		"<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\n"+
			"<meta name=\"theme-color\" media=\"(prefers-color-scheme: light)\" content=\"#849cdd\""+modes+
			"<meta name=\"theme-color\" media=\"(prefers-color-scheme: dark)\" content=\"#202f5e\""+modes)
	mustContain(t, page.Document(Request{Path: "/", Appearance: "dark"}), "<meta name=\"theme-color\" content=\"#202f5e\""+modes)
	mustContain(t, page.Document(Request{Path: "/", Appearance: "light"}), "<meta name=\"theme-color\" content=\"#849cdd\""+modes)
	if doc := page.Document(Request{Path: "/", Appearance: "dark"}); strings.Count(doc, "theme-color") != 1 {
		t.Errorf("a fixed appearance has one theme-color:\n%s", doc)
	}
	light := backdropSite(t, `<page background="cirrus"><content /></page>`, nil)
	if doc := light.Document(Request{Path: "/"}); !strings.Contains(doc, "<meta name=\"theme-color\" content=\"#849cdd\">\n") || strings.Count(doc, "theme-color") != 1 {
		t.Errorf("a palette with no dark look has one theme-color, its light:\n%s", doc)
	}
	if light, dark, _ := palette.Value("canvas"); light != "#f6f9fc" || dark != "#0f1822" {
		t.Fatalf("the canvas the mixes were done with is %s and %s", light, dark)
	}
	shaded := backdropSite(t, `<page background="cirrus-80"><content /></page>`, palette)
	mustContain(t, shaded.Document(Request{Path: "/"}),
		`<meta name="theme-color" media="(prefers-color-scheme: light)" content="#dee6f7" data-kitwork-light="#dee6f7" data-kitwork-dark="#121d2d">`,
		`<meta name="theme-color" media="(prefers-color-scheme: dark)" content="#121d2d" data-kitwork-light="#dee6f7" data-kitwork-dark="#121d2d">`)
	for _, source := range []string{`<page><content /></page>`, `<page background="paper"><content /></page>`} {
		if doc := backdropSite(t, source, palette).Document(Request{Path: "/"}); strings.Contains(doc, "theme-color") {
			t.Errorf("control: %s has no theme-color:\n%s", source, doc)
		}
	}
}

// A backdrop is written plainly: a background of <page> from data beside a gradient is an error at it,
// since the layer is written once with the page's classes; so is a {{ }} that spells a gradient out (a
// ternary of two backdrops), which would otherwise stay on <body>, stretched over the document, with no
// layer, while the contrast check measured it as a backdrop. CONTROL: a background from data on a page
// with no gradient, and a ternary of two colours, stay on <body> as before.
func TestPageBackdropFromDataIsAnError(t *testing.T) {
	files := map[string]string{
		"/site/page.ui":    `<page background="{{ tone }}" medium:background="cirrus"><content /></page>`,
		"/site/content.ui": `<content><text>Trà</text></content>`,
	}
	want := `page.ui:1:7: background="{{ tone }}": <page> có nền gradient, vẽ trên một lớp cố định theo màn hình (nền trang); nền của trang viết thẳng, không lấy từ dữ liệu`
	if got := compileError(t, files); !strings.Contains(got, want) {
		t.Errorf("\n got  %s\n want %s…", got, want)
	}
	files["/site/page.ui"] = `<page background="{{ night ? 'dusk' : 'cirrus' }}"><content /></page>`
	want = `page.ui:1:7: background="{{ night ? 'dusk' : 'cirrus' }}": <page> có nền gradient, vẽ trên một lớp cố định theo màn hình (nền trang); nền của trang viết thẳng, không lấy từ dữ liệu`
	if got := compileError(t, files); !strings.Contains(got, want) {
		t.Errorf("a ternary of backdrops:\n got  %s\n want %s…", got, want)
	}
	files["/site/page.ui"] = `<page background="{{ tone }}"><content /></page>`
	s := memorySite("", files)
	s.Reload = true
	page := compiled(t, s)
	mustContain(t, page.Render(Request{Path: "/", Scope: map[string]any{"tone": "paper"}}), `<body class="flex-column background-paper" data-kitwork-layout="page.ui">`)
	files["/site/page.ui"] = `<page background="{{ night ? 'ink' : 'paper' }}"><content /></page>`
	s = memorySite("", files)
	s.Reload = true
	mustContain(t, compiled(t, s).Render(Request{Path: "/", Scope: map[string]any{"night": true}}), `<body class="flex-column background-ink" data-kitwork-layout="page.ui">`)
}

// Text and glass over a page backdrop are measured against the backdrop's samples (decision D4), each a
// part of the screen the fixed layer shows behind some part of the content: ink reads on cirrus in light
// and in dark, and so does ink-30 on a glass card (paper at 75% over every sample); ink-30 straight on
// the page falls short at the periwinkle corner, #4a5f75 on #849cdd, 2.44:1 (by hand). A lighter one is
// measured as mixed (decision D3): on cirrus-50 the corner is #bbcaee (an oklab mix by hand), 4.02:1.
func TestContrastOverAPageBackdrop(t *testing.T) {
	site := func(page, source string) map[string]string {
		files := content(source)
		files["/site/page.ui"] = page
		return files
	}
	contrastPasses(t, darkPalette(t), site(`<page background="cirrus"><content /></page>`,
		`<content><text>Trà</text><card material="glass" padding="4"><text color="ink-30">Mô tả</text></card></content>`))
	contrastFails(t, darkPalette(t), site(`<page background="cirrus"><content /></page>`, `<content><text color="ink-30">Mô tả</text></content>`), `color=`,
		`color="ink-30" trên nền cirrus tại 0% chỉ 2.44:1 ở chế độ sáng; chữ thường cần 4.5:1 (chữ lớn 3:1). Dùng color="ink-5" (5.12:1 sáng, 7.26:1 tối).`)
	contrastFails(t, darkPalette(t), site(`<page background="cirrus-50"><content /></page>`, `<content><text color="ink-30">Mô tả</text></content>`), `color=`,
		`color="ink-30" trên nền cirrus-50 tại 0% chỉ 4.02:1 ở chế độ sáng; chữ thường cần 4.5:1 (chữ lớn 3:1). Dùng color="ink-25" (4.69:1 sáng, 5.54:1 tối).`)
}
