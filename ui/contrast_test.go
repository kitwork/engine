package ui

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/kitwork/engine/ui/stylesheet"
)

// contrastCompile compiles a site with a palette (nil: Kit Design's light palette alone) and returns
// what its contrast check told, one a line, "" when nothing. By default (router.contrast "warning") a
// pair short of contrast is a warning: the page compiles and the pairs are its warnings. The same site
// is compiled again under "strict", where the same pairs, word for word and in the same order, are its
// compile error: every measurement below holds in both modes.
func contrastCompile(t *testing.T, palette *stylesheet.Palette, files map[string]string) string {
	t.Helper()
	s := memorySite("", files)
	s.Reload = true
	s.Palette = palette
	page, err := s.Compile("/site")
	if err != nil {
		t.Fatalf("by default a pair short of contrast is a warning, the page compiles; got the error:\n%v", err)
	}
	lines := make([]string, 0, len(page.Warnings()))
	for _, w := range page.Warnings() {
		lines = append(lines, w.String())
	}
	warned := strings.Join(lines, "\n")

	s.Contrast = ContrastStrict
	strict := ""
	if _, err := s.Compile("/site"); err != nil {
		strict = err.Error()
	}
	if strict != warned {
		t.Errorf("router.contrast(\"strict\") makes the same pairs the compile error:\n warnings %q\n strict   %q", warned, strict)
	}
	return warned
}

func content(source string) map[string]string { return map[string]string{"/site/content.ui": source} }

// contrastFails asserts the one warning of a site, whole (and under "strict" its one error, the same
// text): at the first needle of content.ui (where), the message.
func contrastFails(t *testing.T, palette *stylesheet.Palette, files map[string]string, needle, message string) {
	t.Helper()
	want := where("content.ui", files["/site/content.ui"], needle) + message
	if got := contrastCompile(t, palette, files); got != want {
		t.Errorf("\n got  %s\n want %s", got, want)
	}
}

// contrastPasses asserts a site compiles with nothing told, in both modes.
func contrastPasses(t *testing.T, palette *stylesheet.Palette, files map[string]string) {
	t.Helper()
	if got := contrastCompile(t, palette, files); got != "" {
		t.Errorf("want it to compile, got:\n%s", got)
	}
}

func darkPalette(t *testing.T, chain ...map[string]stylesheet.Color) *stylesheet.Palette {
	t.Helper()
	p, err := stylesheet.NewPalette(true, chain...)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// kitworkExampleColors is the kitwork example's router.colors as it is today
// (docs/examples/kitwork/kitwork.localhost/router.work, canvas #f6f9fc), with its four accents.
func kitworkExampleColors(t *testing.T) *stylesheet.Palette {
	t.Helper()
	return darkPalette(t, map[string]stylesheet.Color{
		"brand": {Light: "#f5225f", Dark: "#ff6b6b"}, "canvas": {Light: "#f6f9fc", Dark: "#16161e"},
		"paper": {Light: "#ffffff", Dark: "#1e2030"}, "panel": {Light: "#eff3f8", Dark: "#181a26"},
		"line": {Light: "#e6ebf1", Dark: "#2c3145"}, "ink": {Light: "#242442", Dark: "#c0caf5"},
		"success": {Light: "#0e9f6e"}, "warning": {Light: "#c98a1e"}, "danger": {Light: "#ef4444"}, "info": {Light: "#3b82f6"},
	})
}

// A colour that reads passes; one that falls short is a warning at its attribute: the pair as
// written, the ratio to two decimals, the mode, what text needs, and the first colour that passes in
// every mode — with a dark look, its ratio in each.
func TestContrastPassAndFail(t *testing.T) {
	contrastPasses(t, nil, content(`<content><text color="brand+15">Đặt món</text></content>`))
	contrastFails(t, nil, content(`<content><text color="brand">Đặt món</text></content>`), `color=`,
		`color="brand" trên nền canvas chỉ 4.45:1 ở chế độ sáng; chữ thường cần 4.5:1 (chữ lớn 3:1). Dùng color="brand+5" (4.90:1 sáng).`)
	contrastFails(t, darkPalette(t), content(`<content><text color="brand">Đặt món</text></content>`), `color=`,
		`color="brand" trên nền canvas chỉ 4.45:1 ở chế độ sáng; chữ thường cần 4.5:1 (chữ lớn 3:1). Dùng color="brand+5" (4.90:1 sáng, 6.13:1 tối).`)
	// ink lighter: the next step back toward ink, by fives, that passes
	contrastFails(t, nil, content(`<content><card><text color="ink-50">Ghi chú</text></card></content>`), `color=`,
		`color="ink-50" trên nền paper chỉ 3.63:1 ở chế độ sáng; chữ thường cần 4.5:1 (chữ lớn 3:1). Dùng color="ink-40" (4.87:1 sáng).`)
}

// What is not measured: text over content the page cannot know (place over a photo) with nothing solid
// between, a background from data, a disabled control and every disabled: colour, an icon without alt.
// Each beside its CONTROL, the same pair where it is measured, which fails. A scrim between the text
// and the photo is measured over white and over black.
func TestContrastSkips(t *testing.T) {
	const photo = `<image value="/quay.png" alt="Quầy pha chế" />`
	contrastPasses(t, nil, content(`<content><section>`+photo+`<box place="fill"><text color="white">Đặt trước</text></box></section></content>`))
	got := contrastCompile(t, nil, content(`<content><section>`+photo+`<box><text color="white">Đặt trước</text></box></section></content>`))
	if !strings.Contains(got, `color="white" trên nền canvas chỉ 1.06:1`) {
		t.Errorf("CONTROL: white text in the flow is on the canvas, got %q", got)
	}
	got = contrastCompile(t, nil, content(`<content><section>`+photo+`<box place="fill" background="black/50"><text color="white">Đặt trước</text></box></section></content>`))
	if want := `color="white" trên nền black/50 phủ trên nội dung bên dưới (xét cả nền trắng và nền đen) chỉ 3.95:1 ở chế độ sáng`; !strings.Contains(got, want) {
		t.Errorf("a scrim over a photo is measured over white:\n got  %s\n want %s", got, want)
	}
	contrastPasses(t, nil, content(`<content><section>`+photo+`<box place="fill" background="black/70"><text color="white">Đặt trước</text></box></section></content>`))
	// a place written at a layout's call puts its root over the photo too
	files := content(`<content><section>` + photo + `<hero-caption place="fill" /></section></content>`)
	files["/site/hero-caption.ui"] = `<box><text color="white">Đặt trước</text></box>`
	contrastPasses(t, nil, files)
	files["/site/content.ui"] = `<content><section>` + photo + `<hero-caption /></section></content>`
	if got := contrastCompile(t, nil, files); !strings.HasPrefix(got, "hero-caption.ui:1:12: ") {
		t.Errorf("CONTROL: the same layout without place is on the canvas, got %q", got)
	}

	// a background from data: unknown; its literal candidates are checked like written values
	contrastPasses(t, nil, content(`<content><box background="{{ tone }}"><text color="white">Mới</text></box></content>`))
	contrastFails(t, nil, content(`<content><box background="{{ open ? 'paper' : 'canvas' }}"><text color="white">Mới</text></box></content>`), `color=`,
		`color="white" trên nền paper chỉ 1.00:1 ở chế độ sáng; chữ thường cần 4.5:1 (chữ lớn 3:1). Dùng color="black" (19.87:1 sáng).`)
	got = contrastCompile(t, nil, content(`<content><text color="{{ done ? 'success' : 'ink-30' }}">Đã giao</text></content>`))
	if !strings.HasPrefix(got, `content.ui:1:16: color="success" trên nền canvas chỉ 3.21:1`) {
		t.Errorf("each value a {{ }} spells out is measured, got %q", got)
	}

	// a disabled control, and disabled: colours
	contrastPasses(t, nil, content(`<content><button disabled="true" background="brand">Gửi</button><button disabled:color="line">Gửi</button></content>`))
	if got := contrastCompile(t, nil, content(`<content><button background="brand">Gửi</button></content>`)); !strings.Contains(got, "trên nền brand chỉ 3.31:1") {
		t.Errorf("CONTROL: the same button enabled is measured, got %q", got)
	}
	// (a neutral button: under the pointer its fill is the engine's step deeper, ink/10 over the canvas)
	if got := contrastCompile(t, nil, content(`<content><button hover:color="line">Gửi</button></content>`)); !strings.Contains(got, `hover:color="line" trên nền ink/10 phủ trên canvas chỉ 1.07:1 ở chế độ sáng khi hover:`) {
		t.Errorf("CONTROL: the same colour under hover: is measured, got %q", got)
	}

	// an icon without alt is decoration; with alt it says something and needs 3:1
	contrastPasses(t, nil, content(`<content><icon value="coffee" color="line"></icon></content>`))
	contrastFails(t, nil, content(`<content><icon value="coffee" color="line" alt="Cà phê"></icon></content>`), `color=`,
		`icon màu color="line" trên nền canvas chỉ 1.13:1 ở chế độ sáng; icon có alt cần 3:1 như mọi thứ không phải chữ mà vẫn nói một điều. Dùng color="ink" (14.70:1 sáng).`)
}

// Large text — 24px, or 18.66px and bold, read from the stylesheet's scale — needs 3:1: a heading of
// level 2 (24px) and huge text pass at 3.21:1, normal text fails. 18px bold (size="large") is not large,
// nor a level-3 heading (18px).
func TestContrastLargeText(t *testing.T) {
	contrastPasses(t, nil, content(`<content><text heading="2" color="success">Thực đơn</text><text size="huge" color="success">Hôm nay</text></content>`))
	contrastFails(t, nil, content(`<content><text color="success">Thực đơn</text></content>`), `color=`,
		`color="success" trên nền canvas chỉ 3.21:1 ở chế độ sáng; chữ thường cần 4.5:1 (chữ lớn 3:1). Dùng color="success+25" (4.63:1 sáng).`)
	for _, source := range []string{
		`<content><text size="large" weight="bold" color="success">Thực đơn</text></content>`,
		`<content><text heading="3" color="success">Thực đơn</text></content>`,
	} {
		if got := contrastCompile(t, nil, content(source)); !strings.Contains(got, "chỉ 3.21:1 ở chế độ sáng; chữ thường cần 4.5:1") {
			t.Errorf("%s: 18px is not large, got %q", source, got)
		}
	}
	contrastFails(t, nil, content(`<content><text heading="1" color="warning">Thực đơn</text></content>`), `color=`,
		`color="warning" trên nền canvas chỉ 2.78:1 ở chế độ sáng; chữ lớn cần 3:1. Dùng color="warning+10" (3.14:1 sáng).`)
}

// A pair that falls short only under a state, or only from a width, says so; the suggestion is written
// with the same prefix. A state written on the element around the text counts: a button's label on its
// hover fill.
func TestContrastConditions(t *testing.T) {
	contrastFails(t, nil, content(`<content><link value="/" hover:color="brand">Trang chủ</link></content>`), `hover:color=`,
		`hover:color="brand" trên nền canvas chỉ 4.45:1 ở chế độ sáng khi hover:; chữ thường cần 4.5:1 (chữ lớn 3:1). Dùng hover:color="brand+5" (4.90:1 sáng).`)
	contrastFails(t, nil, content(`<content><text medium:color="brand">Trang chủ</text></content>`), `medium:color=`,
		`medium:color="brand" trên nền canvas chỉ 4.45:1 ở chế độ sáng khi medium:; chữ thường cần 4.5:1 (chữ lớn 3:1). Dùng medium:color="brand+5" (4.90:1 sáng).`)
	contrastFails(t, nil, content(`<content><box medium:background="brand"><text>Trang chủ</text></box></content>`), `medium:background=`,
		`chữ ink mặc định của trang trên nền brand chỉ 3.31:1 ở chế độ sáng khi medium:; chữ thường cần 4.5:1 (chữ lớn 3:1). Chữ này engine tô theo router.colors: viết medium:color="brand.on" (4.70:1 sáng), hoặc khai lại ink trong router.colors cho đủ tương phản.`)
	contrastFails(t, nil, content(`<content><button color="paper" background="ink" hover:background="success"><text>Thêm</text></button></content>`), `color=`,
		`color="paper" trên nền success chỉ 3.39:1 ở chế độ sáng khi hover:; chữ thường cần 4.5:1 (chữ lớn 3:1). Dùng hover:color="success.on" trên <button> (content.ui:1:10) (5.30:1 sáng).`)
	// control: base and states that read pass
	contrastPasses(t, nil, content(`<content><link value="/" hover:color="brand+15">Trang chủ</link><box medium:background="ink" medium:color="paper"><text>x</text></box></content>`))
}

// A pair that reads in light and not in dark fails in dark, and says so. CONTROL: the same page with no
// dark look compiles.
func TestContrastDarkOnly(t *testing.T) {
	indigo := map[string]stylesheet.Color{"brand": {Light: "#3730a3", Dark: "#3730a3"}}
	got := contrastCompile(t, darkPalette(t, indigo), content(`<content><text color="brand">Quản lý</text></content>`))
	if want := `content.ui:1:16: color="brand" trên nền canvas chỉ 1.80:1 ở chế độ tối;`; !strings.HasPrefix(got, want) {
		t.Errorf("\n got  %s\n want %s…", got, want)
	}
	light, err := stylesheet.NewPalette(false, map[string]stylesheet.Color{"brand": {Light: "#3730a3"}})
	if err != nil {
		t.Fatal(err)
	}
	contrastPasses(t, light, content(`<content><text color="brand">Quản lý</text></content>`))
}

// A glass is its tint at the glass's opacity over what lies behind it, and the background as written
// (here paper, solid) where the browser cannot blur the backdrop (its one fallback: since 13/10/2026 the
// glass shows also to a reader who asked for less transparency). A pinned frame sticks over the page while it scrolls: behind its
// glass is the page's background, not the frame around it. CONTROL: the same frame unpinned is over the
// paper section, and reads.
func TestContrastGlassOverThePage(t *testing.T) {
	files := map[string]string{
		"/site/page.ui":    `<page background="ink"><content /></page>`,
		"/site/content.ui": `<content><section background="paper"><box pin="top" background="paper" material="glass/60"><text color="ink-30">Thanh ghim</text></box></section></content>`,
	}
	contrastFails(t, nil, files, `color=`,
		`color="ink-30" trên nền kính paper (material="glass/60") phủ trên ink chỉ 2.73:1 ở chế độ sáng; chữ thường cần 4.5:1 (chữ lớn 3:1). Dùng color="ink-10" (4.98:1 sáng).`)
	files["/site/content.ui"] = `<content><section background="paper"><box background="paper" material="glass/60"><text color="ink-30">Thanh ghim</text></box></section></content>`
	contrastPasses(t, nil, files)
}

// Glass first, background second (decision of 13/10/2026): text on a glass is measured on the
// background's colour without its /N at the material's coverage, and on the background as written
// where the browser cannot blur. Over the ink page, background="paper/80" material="glass/30" is the
// glass of background="paper" material="glass/30", word for word: paper at 30% (ink 2.63:1), not the
// paper/80 the glass once read (paper at 24%, 2.15:1). background="paper/20" material="glass/90": the
// glass, paper at 90%, reads (12.77:1); the fallback, paper/20 over ink, does not (1.89:1), and the
// message names it as the fallback. CONTROL: with background="paper" that fallback is solid paper and
// the same text reads.
func TestContrastGlassFirstBackgroundSecond(t *testing.T) {
	glass := func(background, material string) map[string]string {
		return map[string]string{
			"/site/page.ui":    `<page background="ink"><content /></page>`,
			"/site/content.ui": `<content><box pin="top" background="` + background + `" material="` + material + `"><text color="ink">Thanh ghim</text></box></content>`,
		}
	}
	const onGlass = `color="ink" trên nền kính paper (material="glass/30") phủ trên ink chỉ 2.63:1 ở chế độ sáng; chữ thường cần 4.5:1 (chữ lớn 3:1). ` +
		`Không màu chữ nào, kể cả trắng hay đen, đọc được trên nền này: đổi nền paper (background, material) hay khai lại paper trong router.colors.`
	contrastFails(t, nil, glass("paper/80", "glass/30"), `color=`, onGlass)
	contrastFails(t, nil, glass("paper", "glass/30"), `color=`, onGlass)
	contrastFails(t, nil, glass("paper/20", "glass/90"), `color=`,
		`color="ink" trên nền paper/20 (lối lùi của material="glass/90" khi trình duyệt không nhoè được) phủ trên ink chỉ 1.89:1 ở chế độ sáng; chữ thường cần 4.5:1 (chữ lớn 3:1). `+
			`Không màu chữ nào, kể cả trắng hay đen, đọc được trên nền này: đổi nền paper (background, material) hay khai lại paper trong router.colors.`)
	contrastPasses(t, nil, glass("paper", "glass/90"))
}

// The fallback is named the fallback whether or not its background writes a /N: background="brand"
// material="glass/30" on the canvas page is brand at 30% where the browser blurs (the page's ink
// reads) and solid brand where it cannot (ink 3.31:1, the ratio of ink on background="brand" alone),
// so the warning names the fallback, as it does for background="brand/100", the same ground written
// with its /N (no text colour reads on both the brand glass and solid brand, so no colour is
// suggested). CONTROL: a glass that falls short where it blurs keeps its name, "kính … phủ trên".
func TestContrastFallbackIsNamedWithoutAnAlpha(t *testing.T) {
	fallback := func(background string) string {
		return `chữ ink mặc định của trang trên nền ` + background + ` (lối lùi của material="glass/30" khi trình duyệt không nhoè được) chỉ 3.31:1 ở chế độ sáng; chữ thường cần 4.5:1 (chữ lớn 3:1). ` +
			`Không màu chữ nào, kể cả trắng hay đen, đọc được trên nền này: đổi nền brand (background, material) hay khai lại brand trong router.colors.`
	}
	for _, background := range []string{"brand", "brand/100"} {
		contrastFails(t, nil, content(`<content><box background="`+background+`" material="glass/30"><text>Thanh ghim</text></box></content>`), `material=`, fallback(background))
	}
	files := map[string]string{
		"/site/page.ui":    `<page background="ink"><content /></page>`,
		"/site/content.ui": `<content><box pin="top" background="paper" material="glass/30"><text color="ink">Thanh ghim</text></box></content>`,
	}
	contrastFails(t, nil, files, `color=`,
		`color="ink" trên nền kính paper (material="glass/30") phủ trên ink chỉ 2.63:1 ở chế độ sáng; chữ thường cần 4.5:1 (chữ lớn 3:1). `+
			`Không màu chữ nào, kể cả trắng hay đen, đọc được trên nền này: đổi nền paper (background, material) hay khai lại paper trong router.colors.`)
}

// A badge's colour is a pill: the accent's stronger text on its fill 85% lighter, which reads (the
// step is the smallest that does, success+35 on Kit Design's success-85). A written background
// replaces the fill and the text stays stronger: on the solid fill it falls short, and the label rung
// of that fill is the suggestion. The text is an engine colour per mode (13/10/2026), and a message says
// the one of its mode: warning in dark on Kit Design. On a badge, warning alone is the badge's own pill
// (its text warning+55 in light), so the note after a suggestion steps from warning+5 there: light needs
// all of warning+40 on white, and only dark is told it needs less.
func TestContrastBadge(t *testing.T) {
	contrastPasses(t, nil, content(`<content><badge color="warning"><text>Đang pha</text></badge><badge color="ink-30"><text>Tạm hết</text></badge></content>`))
	contrastFails(t, nil, content(`<content><badge color="success" background="success"><text>Đã giao</text></badge></content>`), `color=`,
		`color="success" (chữ success+35 của <badge>) trên nền success chỉ 1.69:1 ở chế độ sáng; chữ thường cần 4.5:1 (chữ lớn 3:1). Dùng color="success.on" (5.30:1 sáng).`)
	slate := darkPalette(t, map[string]stylesheet.Color{"page.band": {Light: "#ffffff", Dark: "#4a5260"}})
	contrastFails(t, slate, content(`<content><badge color="warning" background="page.band"><text>Đang pha</text></badge></content>`), `color=`,
		`color="warning" (chữ warning của <badge>) trên nền page.band chỉ 3.69:1 ở chế độ tối; chữ thường cần 4.5:1 (chữ lớn 3:1). Dùng color="warning+40" (4.76:1 sáng, 7.64:1 tối). `+
			`warning+40 là một bậc cho cả hai chế độ: ở chế độ tối nó là #fffbf6, gần trắng, chỉ còn 8% sắc của warning, mà tối chỉ cần warning+15 (4.88:1). `+
			`Muốn giữ sắc ở cả hai chế độ thì khai một vai trò riêng: router.colors({ "warning.text": { light: "#9d6800", dark: "#fdc16d" } }) rồi viết color="warning.text".`)
}

// A label on a fill takes the fill's on rung. Written, the error stands at the colour; left to the
// engine (ink), at the background, and it points at router.colors.
func TestContrastLabelOnFill(t *testing.T) {
	contrastFails(t, nil, content(`<content><button background="brand">Gửi</button></content>`), `background=`,
		`chữ ink mặc định của trang trên nền brand chỉ 3.31:1 ở chế độ sáng; chữ thường cần 4.5:1 (chữ lớn 3:1). Chữ này engine tô theo router.colors: viết color="brand.on" (4.70:1 sáng), hoặc khai lại ink trong router.colors cho đủ tương phản.`)
	contrastFails(t, nil, content(`<content><button background="brand" color="ink">Gửi</button></content>`), `color=`,
		`color="ink" trên nền brand chỉ 3.31:1 ở chế độ sáng; chữ thường cần 4.5:1 (chữ lớn 3:1). Dùng color="brand.on" (4.70:1 sáng).`)
	contrastPasses(t, nil, content(`<content><button background="brand" color="brand.on">Gửi</button></content>`))
}

// A layout, a component and a region are measured where they are called: the same file reads on the
// page's canvas and falls short inside an ink section, and the error stands in the file.
func TestContrastAtTheCallSite(t *testing.T) {
	layout := content(`<content><section background="ink"><price-tag /></section></content>`)
	layout["/site/price-tag.ui"] = `<box><text color="ink-30">29.000đ</text></box>`
	if got := contrastCompile(t, nil, layout); !strings.HasPrefix(got, `price-tag.ui:1:12: color="ink-30" trên nền ink chỉ 2.36:1 ở chế độ sáng`) {
		t.Errorf("a layout inside an ink section: %q", got)
	}
	layout["/site/content.ui"] = `<content><section><price-tag /></section></content>`
	contrastPasses(t, nil, layout)

	component := content(`<content><section background="ink"><box component="cart" /></section></content>`)
	component["/site/cart.ui"] = `<box><text color="ink-30">Giỏ</text></box>`
	component["/site/cart.work"] = cartLogic
	if got := contrastCompile(t, nil, component); !strings.HasPrefix(got, `cart.ui:1:12: color="ink-30" trên nền ink chỉ 2.36:1`) {
		t.Errorf("a component inside an ink section: %q", got)
	}
	component["/site/content.ui"] = `<content><section><box component="cart" /></section></content>`
	contrastPasses(t, nil, component)

	region := map[string]string{
		"/site/page.ui":    `<page background="ink"><header /><content /></page>`,
		"/site/header.ui":  `<header><text color="ink-30">Quán</text></header>`,
		"/site/content.ui": `<content><box background="paper"><text>a</text></box></content>`,
	}
	if got := contrastCompile(t, nil, region); !strings.HasPrefix(got, `header.ui:1:15: color="ink-30" trên nền ink chỉ 2.36:1`) {
		t.Errorf("a region on an ink page: %q", got)
	}
	region["/site/page.ui"] = `<page><header /><content /></page>`
	contrastPasses(t, nil, region)
}

// The words the engine shows itself are measured even when the page writes none: an empty <error> a
// validation fills, on an ink card (its words paper: what is typed stands on the field's translucent
// fill over the ink). With no colour written, the error stands at the background and points at
// router.colors. A site palette whose ink-30 is too light fails the hint the same way. The words of
// <error> are an engine colour per mode (13/10/2026): danger+25 in light, danger in dark on Kit Design,
// and a message says the one of its mode, the suggestions stepping from it — on a site ground that is
// white in light and a slate in dark, the error falls short in dark only and is told "chữ danger".
func TestContrastEngineText(t *testing.T) {
	contrastFails(t, nil, content(`<content><card background="ink" color="paper"><field><input type="tel"></input><error /></field></card></content>`), `background=`,
		`chữ danger+25 mặc định của <error> trên nền ink chỉ 2.71:1 ở chế độ sáng; chữ thường cần 4.5:1 (chữ lớn 3:1). Chữ này engine tô theo router.colors: viết color="paper" (15.54:1 sáng), hoặc khai lại danger trong router.colors cho đủ tương phản.`)
	// an ink that reads on the field's fill over the white canvas, at rest (4.80:1) and with focus
	// (4.6:1): what is typed reads, and only the hint falls short
	grey, err := stylesheet.NewPalette(false, map[string]stylesheet.Color{"canvas": {Light: "#ffffff"}, "ink": {Light: "#6e6e6e"}})
	if err != nil {
		t.Fatal(err)
	}
	contrastFails(t, grey, content("<content><field><label>Số điện thoại</label><input type=\"tel\"></input>\n<hint>Quán gọi lại.</hint></field></content>"), `<hint>`,
		`chữ ink-30 mặc định của <hint> trên nền canvas chỉ 2.92:1 ở chế độ sáng; chữ thường cần 4.5:1 (chữ lớn 3:1). Chữ này engine tô theo router.colors: viết color="ink-5" (4.61:1 sáng), hoặc khai lại ink trong router.colors cho đủ tương phản.`)
	// in dark: the engine colour as written in that mode, never its name
	slate := darkPalette(t, map[string]stylesheet.Color{"page.band": {Light: "#ffffff", Dark: "#4a5260"}})
	contrastFails(t, slate, content(`<content><box background="page.band"><error>Sai</error></box></content>`), `background=`,
		`chữ danger mặc định của <error> trên nền page.band chỉ 2.87:1 ở chế độ tối; chữ thường cần 4.5:1 (chữ lớn 3:1). Chữ này engine tô theo router.colors: viết color="danger+25" (5.74:1 sáng, 4.61:1 tối), hoặc khai lại danger trong router.colors cho đủ tương phản.`)
	// CONTROL: the same error on Kit Design's ink card with a dark look is told in light as before
	if got := contrastCompile(t, darkPalette(t), content(`<content><card background="ink" color="paper"><field><input type="tel"></input><error /></field></card></content>`)); !strings.Contains(got, "chữ danger+25 mặc định của <error> trên nền ink chỉ 2.71:1 ở chế độ sáng") || strings.Contains(got, "kitwork-") {
		t.Errorf("CONTROL: in light the error is danger+25, its name never said:\n%s", got)
	}
}

// A suggestion X+N is one step for both modes (the grammar); when it goes 20 or more past what one mode
// needs alone, a note follows it (decision of 13/10/2026, option B): what X+N is in that mode — its hex,
// and "gần trắng" (OKLab lightness 0.85 or more), "gần đen" (0.25 or less), "trắng hẳn" (#ffffff), "đen
// hẳn" (#000000), "chỉ còn N% sắc của X" (chroma under 40% of the token's) when it shows —, the step that
// mode needs with its ratio, then a role X.text of the site, its { light, dark } the least step of each
// mode, and the attribute as the suggestion writes it (hover:color too) with that role, "đặt tên khác"
// when the site has the name. What the role is for is worded by what the colours show: "giữ sắc ở cả hai
// chế độ" only when X+N lost its hue in the mode told and the role keeps it in both (the kitwork example's
// manifest, content.ui:98, brand+15 on cirrus-70: the plan's example; ink+35 black in light); else "mỗi
// chế độ chỉ lấy bậc nó cần" — light's X+N only darker, its hue kept (a site ground that is a slate in
// dark), or the role itself pale in a mode, said with that mode's step (<error> on ink-60, danger+35 near
// white in dark). The boundary is 20 exactly: brand+20 on a band #3e3e3e is 20 past light's brand and has
// the note, brand+15 on #3c3c3c is 15 past and has none; success+20 on #303030 and on #3c3c3c the same
// in dark. CONTROL: brand+5 on Kit Design is 5 past dark's brand and has no note; a palette with one mode
// has none.
func TestContrastOvershootNote(t *testing.T) {
	kitwork := kitworkExampleColors(t)
	contrastFails(t, kitwork, content(`<content><box background="cirrus-70"><text font="code" weight="bold" color="brand+15">a</text></box></content>`), `color=`,
		`color="brand+15" trên nền cirrus-70 tại 0% chỉ 3.62:1 ở chế độ sáng; chữ thường cần 4.5:1 (chữ lớn 3:1). Dùng color="brand+35" (4.83:1 sáng, 11.60:1 tối). `+
			`brand+35 là một bậc cho cả hai chế độ: ở chế độ tối nó là #ffccc8, gần trắng, chỉ còn 32% sắc của brand, mà tối chỉ cần brand (5.96:1). `+
			`Muốn giữ sắc ở cả hai chế độ thì khai một vai trò riêng: router.colors({ "brand.text": { light: "#bb0042", dark: "#ff6b6b" } }) rồi viết color="brand.text".`)
	kit := darkPalette(t)
	// the engine's own text: the note after "khai lại"; pure white is said so, and the role's dark,
	// danger+35, is near white too: no "giữ sắc", the role's own look said with the step dark needs
	contrastFails(t, kit, content(`<content><box background="ink-60"><error>Sai số</error></box></content>`), `background=`,
		`chữ danger+25 mặc định của <error> trên nền ink-60 chỉ 2.10:1 ở chế độ sáng; chữ thường cần 4.5:1 (chữ lớn 3:1). Chữ này engine tô theo router.colors: viết color="danger+80" (4.56:1 sáng, 7.01:1 tối), hoặc khai lại danger trong router.colors cho đủ tương phản. `+
			`danger+80 là một bậc cho cả hai chế độ: ở chế độ tối nó là #ffffff, trắng hẳn, mà tối chỉ cần danger+35 (4.91:1). `+
			`Muốn mỗi chế độ chỉ lấy bậc nó cần thì khai một vai trò riêng: router.colors({ "danger.text": { light: "#6f0010", dark: "#ffccc7" } }) rồi viết color="danger.text"; `+
			`ở chế độ tối danger.text vẫn gần trắng: tối cần tới danger+35.`)
	// light is the mode that needs less: a site ground white in light, a slate in dark. Light's danger+40
	// is only darker, its hue kept (no look word), and the role's dark is danger+40 itself, near white
	slate := darkPalette(t, map[string]stylesheet.Color{"page.band": {Light: "#ffffff", Dark: "#5a6270"}})
	contrastFails(t, slate, content(`<content><box background="page.band"><text color="danger+15">a</text></box></content>`), `color=`,
		`color="danger+15" trên nền page.band chỉ 2.99:1 ở chế độ tối; chữ thường cần 4.5:1 (chữ lớn 3:1). Dùng color="danger+40" (7.14:1 sáng, 4.66:1 tối). `+
			`danger+40 là một bậc cho cả hai chế độ: ở chế độ sáng nó là #b30022, mà sáng chỉ cần danger+10 (4.57:1). `+
			`Muốn mỗi chế độ chỉ lấy bậc nó cần thì khai một vai trò riêng: router.colors({ "danger.text": { light: "#e02c3b", dark: "#ffd7d3" } }) rồi viết color="danger.text"; `+
			`ở chế độ tối danger.text vẫn gần trắng, chỉ còn 31% sắc của danger: tối cần tới danger+40.`)
	// the boundary, 20 exactly, on the light side: brand+20 on a band #3e3e3e in dark is 20 past light's
	// brand, its hue kept and the role's too: "mỗi chế độ chỉ lấy bậc nó cần", no tail. CONTROL: on
	// #3c3c3c brand+15 is 15 past, no note
	band := func(dark string) *stylesheet.Palette {
		return darkPalette(t, map[string]stylesheet.Color{"page.band": {Light: "#ffffff", Dark: dark}})
	}
	contrastFails(t, band("#3e3e3e"), content(`<content><box background="page.band"><text color="brand">a</text></box></content>`), `color=`,
		`color="brand" trên nền page.band chỉ 3.33:1 ở chế độ tối; chữ thường cần 4.5:1 (chữ lớn 3:1). Dùng color="brand+20" (6.98:1 sáng, 4.76:1 tối). `+
			`brand+20 là một bậc cho cả hai chế độ: ở chế độ sáng nó là #5125fb, mà sáng chỉ cần brand (4.70:1). `+
			`Muốn mỗi chế độ chỉ lấy bậc nó cần thì khai một vai trò riêng: router.colors({ "brand.text": { light: "#635bff", dark: "#9ea6ff" } }) rồi viết color="brand.text".`)
	contrastFails(t, band("#3c3c3c"), content(`<content><box background="page.band"><text color="brand">a</text></box></content>`), `color=`,
		`color="brand" trên nền page.band chỉ 3.43:1 ở chế độ tối; chữ thường cần 4.5:1 (chữ lớn 3:1). Dùng color="brand+15" (6.29:1 sáng, 4.53:1 tối).`)
	// the same boundary on the dark side: success+20 is 20 past dark's success on #303030, 15 past on #3c3c3c
	contrastFails(t, band("#303030"), content(`<content><box background="page.band"><text color="success">a</text></box></content>`), `color=`,
		`color="success" trên nền page.band chỉ 3.39:1 ở chế độ sáng; chữ thường cần 4.5:1 (chữ lớn 3:1). Dùng color="success+20" (4.54:1 sáng, 7.65:1 tối). `+
			`success+20 là một bậc cho cả hai chế độ: ở chế độ tối nó là #7ed89a, mà tối chỉ cần success (5.32:1). `+
			`Muốn mỗi chế độ chỉ lấy bậc nó cần thì khai một vai trò riêng: router.colors({ "success.text": { light: "#00884d", dark: "#69b485" } }) rồi viết color="success.text".`)
	contrastFails(t, band("#3c3c3c"), content(`<content><box background="page.band"><text color="success">a</text></box></content>`), `color=`,
		`color="success" trên nền page.band chỉ 3.39:1 ở chế độ sáng; chữ thường cần 4.5:1 (chữ lớn 3:1). Dùng color="success+20" (4.54:1 sáng, 6.40:1 tối).`)
	// "gần đen": ink+20 in light on a band #6c6c6c in dark; the role's dark is ink+20 itself, near white
	contrastFails(t, band("#6c6c6c"), content(`<content><box background="page.band"><text color="ink">a</text></box></content>`), `color=`,
		`color="ink" trên nền page.band chỉ 3.30:1 ở chế độ tối; chữ thường cần 4.5:1 (chữ lớn 3:1). Dùng color="ink+20" (20.39:1 sáng, 4.90:1 tối). `+
			`ink+20 là một bậc cho cả hai chế độ: ở chế độ sáng nó là #000511, gần đen, mà sáng chỉ cần ink (15.54:1). `+
			`Muốn mỗi chế độ chỉ lấy bậc nó cần thì khai một vai trò riêng: router.colors({ "ink.text": { light: "#0a2540", dark: "#f1f8ff" } }) rồi viết color="ink.text"; `+
			`ở chế độ tối ink.text vẫn gần trắng, chỉ còn 16% sắc của ink: tối cần tới ink+20.`)
	// "đen hẳn": ink+35 in light is #000000; the role keeps ink's navy in light and a slate in dark, so
	// here the hue is kept in both: "giữ sắc ở cả hai chế độ"
	navy := darkPalette(t, map[string]stylesheet.Color{"ink": {Light: "#0a2540", Dark: "#5a6a7a"}, "page.band": {Light: "#ffffff", Dark: "#202020"}})
	contrastFails(t, navy, content(`<content><box background="page.band"><text color="ink">a</text></box></content>`), `color=`,
		`color="ink" trên nền page.band chỉ 2.93:1 ở chế độ tối; chữ thường cần 4.5:1 (chữ lớn 3:1). Dùng color="ink+35" (21.00:1 sáng, 4.59:1 tối). `+
			`ink+35 là một bậc cho cả hai chế độ: ở chế độ sáng nó là #000000, đen hẳn, mà sáng chỉ cần ink (15.54:1). `+
			`Muốn giữ sắc ở cả hai chế độ thì khai một vai trò riêng: router.colors({ "ink.text": { light: "#0a2540", dark: "#788a9c" } }) rồi viết color="ink.text".`)
	// written for a condition, and a name the site has already
	contrastFails(t, kit, content(`<content><text hover:color="warning">a</text></content>`), `hover:color=`,
		`hover:color="warning" trên nền canvas chỉ 2.78:1 ở chế độ sáng khi hover:; chữ thường cần 4.5:1 (chữ lớn 3:1). Dùng hover:color="warning+40" (4.50:1 sáng, 11.26:1 tối). `+
			`warning+40 là một bậc cho cả hai chế độ: ở chế độ tối nó là #fffbf6, gần trắng, chỉ còn 8% sắc của warning, mà tối chỉ cần warning (8.38:1). `+
			`Muốn giữ sắc ở cả hai chế độ thì khai một vai trò riêng: router.colors({ "warning.text": { light: "#9d6800", dark: "#daa965" } }) rồi viết hover:color="warning.text".`)
	named := darkPalette(t, map[string]stylesheet.Color{"warning.text": {Light: "#000000"}})
	if got := contrastCompile(t, named, content(`<content><text color="warning">a</text></content>`)); !strings.HasSuffix(got, `rồi viết color="warning.text" (warning.text đã có trong bảng màu: đặt tên khác).`) {
		t.Errorf("a role the site has already: want %q, got\n%s", "đặt tên khác", got)
	}
	// CONTROLS: 5 past dark's brand; one mode only
	contrastFails(t, kit, content(`<content><text color="brand">a</text></content>`), `color=`,
		`color="brand" trên nền canvas chỉ 4.45:1 ở chế độ sáng; chữ thường cần 4.5:1 (chữ lớn 3:1). Dùng color="brand+5" (4.90:1 sáng, 6.13:1 tối).`)
	contrastFails(t, nil, content(`<content><text color="warning">a</text></content>`), `color=`,
		`color="warning" trên nền canvas chỉ 2.78:1 ở chế độ sáng; chữ thường cần 4.5:1 (chữ lớn 3:1). Dùng color="warning+40" (4.50:1 sáng).`)
}

// The note's threshold is 20 steps exactly (decision of 13/10/2026, option B): 20 past a mode's need is
// told, 19 is not. A suggestion steps by fives, so a compile never shows 19 (TestContrastOvershootNote
// pins 20 against 15); here the rule itself, 19 the CONTROL.
func TestContrastOvershootThreshold(t *testing.T) {
	for _, c := range []struct {
		amount, need int
		want         bool
	}{{40, 20, true}, {39, 20, false}, {20, 0, true}, {19, 0, false}, {25, 0, true}, {35, 20, false}} {
		if got := overshoots(c.amount, c.need); got != c.want {
			t.Errorf("X+%d past a mode's X+%d (%d steps): told %v, want %v", c.amount, c.need, c.amount-c.need, got, c.want)
		}
	}
}

// Every pair of a page that falls short is told at once, each once, at its attribute: by default as the
// page's warnings, in the order compiled, the page rendering as written; under "strict" as one compile
// error, one a line, each an *Error.
func TestContrastReportsEveryPair(t *testing.T) {
	s := memorySite("", content("<content><text color=\"brand\">a</text>\n<text color=\"danger\">b</text></content>"))
	page, err := s.Compile("/site")
	if err != nil {
		t.Fatalf("by default the page compiles, got %v", err)
	}
	warned := page.Warnings()
	if len(warned) != 2 || !strings.HasPrefix(warned[0].String(), "content.ui:1:16: color=\"brand\"") || !strings.HasPrefix(warned[1].String(), "content.ui:2:7: color=\"danger\"") {
		t.Fatalf("want both pairs as warnings, got %v", warned)
	}
	if warned[0].Line != 1 || warned[0].Column != 16 || warned[0].File != "content.ui" {
		t.Errorf("a warning knows where it stands, got %+v", warned[0])
	}
	mustContain(t, page.Render(Request{Path: "/"}), `class="color-brand"`, `class="color-danger"`)

	s.Contrast = ContrastStrict
	_, err = s.Compile("/site")
	var all Errors
	if !errors.As(err, &all) || len(all) != 2 {
		t.Fatalf("want both pairs, got %v", err)
	}
	if !strings.HasPrefix(all[0].Error(), "content.ui:1:16: color=\"brand\"") || !strings.HasPrefix(all[1].Error(), "content.ui:2:7: color=\"danger\"") {
		t.Errorf("got:\n%v", err)
	}
	var one *Error
	if !errors.As(err, &one) || one.Line != 1 {
		t.Errorf("errors.As finds the first *Error, got %v", one)
	}
	if strings.Count(err.Error(), "\n") != 1 {
		t.Errorf("one line each:\n%v", err)
	}
}

// A ratio is written with two decimals, never rounded up to the ratio it misses: 4.4999 against 4.5
// is 4.49, so a failing pair never reads as passing. CONTROL: with nothing to miss it rounds as usual.
func TestRatioText(t *testing.T) {
	for _, c := range []struct {
		r, need float64
		want    string
	}{
		{4.4999, 4.5, "4.49"}, {4.4781, 4.5, "4.48"}, {2.996, 3, "2.99"}, {21, 4.5, "21.00"}, {4.4999, 0, "4.50"},
	} {
		if got := ratioText(c.r, c.need); got != c.want {
			t.Errorf("ratioText(%v, %v) = %s, want %s", c.r, c.need, got, c.want)
		}
	}
}

// A pin written with a width prefix pins from that width only: below it the frame is in the flow, over
// the frame around it, and a wider pin="none" unpins. medium:pin over an ink section is measured over
// the page's canvas from medium, over the ink section below it; the suggestion holds at both.
// CONTROL: the same frame without a pin is over the ink section at every width, and reads.
func TestContrastPinFromItsWidth(t *testing.T) {
	contrastFails(t, nil, content(`<content><section background="ink"><box medium:pin="top" background="white/10"><text color="paper">x</text></box></section></content>`), `color=`,
		`color="paper" trên nền white/10 phủ trên canvas chỉ 1.05:1 ở chế độ sáng khi medium:; chữ thường cần 4.5:1 (chữ lớn 3:1). Dùng medium:color="ink" (11.53:1 sáng).`)
	contrastPasses(t, nil, content(`<content><section background="ink"><box background="white/10"><text color="paper">x</text></box></section></content>`))
	// pinned on small screens only: from medium it is over the ink section, where paper reads
	contrastPasses(t, nil, content(`<content><section background="ink"><box pin="top" medium:pin="none" background="white/10"><text color="ink" medium:color="paper">x</text></box></section></content>`))
}

// A {{ }} that spells out some values and reads data for another: the values spelled out are measured
// like written ones, the one from data is not; a size it may read from data counts as the smallest.
// CONTROLS: a value from data alone is not measured; a large size written reads at 3:1.
func TestContrastLiteralArmBesideData(t *testing.T) {
	contrastFails(t, nil, content(`<content><text color="{{ done ? 'line' : tone }}">Đã giao</text></content>`), `color=`,
		`color="line" trên nền canvas chỉ 1.13:1 ở chế độ sáng; chữ thường cần 4.5:1 (chữ lớn 3:1). Dùng color="ink" (14.70:1 sáng).`)
	contrastFails(t, nil, content(`<content><box background="{{ hot ? 'ink' : tone }}"><text>x</text></box></content>`), `background=`,
		`chữ ink mặc định của trang trên nền ink chỉ 1.00:1 ở chế độ sáng; chữ thường cần 4.5:1 (chữ lớn 3:1). Chữ này engine tô theo router.colors: viết color="paper" (15.54:1 sáng).`)
	if got := contrastCompile(t, nil, content(`<content><text size="{{ big ? 'huge' : size }}" color="success">Hôm nay</text></content>`)); !strings.Contains(got, `color="success" trên nền canvas chỉ 3.21:1 ở chế độ sáng; chữ thường cần 4.5:1`) {
		t.Errorf("a size from data besides huge is normal text, got %q", got)
	}
	contrastPasses(t, nil, content(`<content><text color="{{ tone }}">a</text><box background="{{ tone }}"><text>b</text></box><text size="huge" color="success">c</text></content>`))
}

// An input that shows its value as a label (submit, reset, button) is measured like a button's label.
// CONTROLS: a checkbox on the same fill shows no text; the same label on the field's paper reads.
func TestContrastInputLabels(t *testing.T) {
	for _, kind := range []string{"submit", "reset", "button"} {
		source := `<content><form><input type="` + kind + `" value="Gửi" background="brand" /></form></content>`
		if got := contrastCompile(t, nil, content(source)); !strings.Contains(got, `chữ ink mặc định của trang trên nền brand chỉ 3.31:1`) {
			t.Errorf("%s: %q", kind, got)
		}
	}
	contrastPasses(t, nil, content(`<content><form><input type="checkbox" background="brand" /><input type="submit" value="Gửi" /></form></content>`))
}

// Two ternaries of one element that test the same show their arms together: a selected chip is
// brand.on on brand or ink on paper, never brand.on on paper; with the other arm from data, ink is
// never on brand. CONTROL: ternaries that test different things cross, and brand.on on paper is
// measured.
func TestContrastTernaryArmsPairTogether(t *testing.T) {
	contrastPasses(t, nil, content(`<content><button background="{{ selected ? 'brand' : 'paper' }}" color="{{ selected ? 'brand.on' : 'ink' }}">Size M</button>`+
		`<box background="{{ on ? 'ink' : 'paper' }}" color="{{ on ? 'paper' : 'ink' }}"><text>x</text></box>`+
		`<button background="{{ selected ? 'brand' : tone }}" color="{{ selected ? 'brand.on' : 'ink' }}">Size L</button></content>`))
	if got := contrastCompile(t, nil, content(`<content><button background="{{ selected ? 'brand' : 'paper' }}" color="{{ chosen ? 'brand.on' : 'ink' }}">Size M</button></content>`)); !strings.Contains(got, `color="brand.on" trên nền paper chỉ 1.00:1`) {
		t.Errorf("CONTROL: two different tests cross, got %q", got)
	}
}

// An icon placed over its parent's content (beside a photo) is over what the page cannot know, like
// text placed there: skipped. CONTROL: the same icon in the flow is on the canvas and falls short.
func TestContrastPlacedIcon(t *testing.T) {
	contrastPasses(t, nil, content(`<content><box><image value="/a.png" alt="a" /><icon value="heart" alt="Yêu thích" color="white" place="top-end" /></box></content>`))
	if got := contrastCompile(t, nil, content(`<content><box><image value="/a.png" alt="a" /><icon value="heart" alt="Yêu thích" color="white" /></box></content>`)); !strings.Contains(got, `icon màu color="white" trên nền canvas chỉ 1.06:1`) {
		t.Errorf("CONTROL: an icon in the flow is measured, got %q", got)
	}
}

// Text a show written "false" hides at a width, on it or around it, is not measured at that width: it
// cannot render there. CONTROL: shown at every width, its pair below medium falls short.
func TestContrastHiddenWidths(t *testing.T) {
	const frame = `<box background="ink" medium:background="canvas" color="paper" medium:color="ink">`
	contrastPasses(t, nil, content(`<content>`+frame+`<text show="false" medium:show="true" color="ink">Chỉ màn rộng</text></box></content>`))
	contrastPasses(t, nil, content(`<content><box show="false" medium:show="true">`+frame+`<text color="ink">Chỉ màn rộng</text></box></box></content>`))
	if got := contrastCompile(t, nil, content(`<content>`+frame+`<text color="ink">Mọi màn</text></box></content>`)); !strings.Contains(got, `color="ink" trên nền ink chỉ 1.00:1 ở chế độ sáng;`) {
		t.Errorf("CONTROL: shown at every width it is measured below medium, got %q", got)
	}
}

// When no colour suggested passes, the advice is one the author can follow. Text the engine coloured
// on a ground of its own token is not told to declare that token twice; ink lighter on ink is
// told paper, the other end of the palette; a label whose grounds no one colour reads on (brand and,
// on hover, brand+15: black reads on one, white on the other) is told to write a colour for each
// condition or change the ground, never to declare a rung; brand glass, on which neither black nor
// white reads, is told to change the ground. CONTROL: the colour for each condition compiles.
func TestContrastAdviceItCanFollow(t *testing.T) {
	contrastFails(t, nil, content(`<content><section background="ink"><box><text>Câu</text></box></section></content>`), `background=`,
		`chữ ink mặc định của trang trên nền ink chỉ 1.00:1 ở chế độ sáng; chữ thường cần 4.5:1 (chữ lớn 3:1). Chữ này engine tô theo router.colors: viết color="paper" (15.54:1 sáng).`)
	contrastFails(t, nil, content(`<content><section background="ink"><text color="ink-30">x</text></section></content>`), `color=`,
		`color="ink-30" trên nền ink chỉ 2.36:1 ở chế độ sáng; chữ thường cần 4.5:1 (chữ lớn 3:1). Dùng color="paper" (15.54:1 sáng).`)
	kitwork := darkPalette(t, map[string]stylesheet.Color{ // docs/examples/kitwork/kitwork.localhost/router.work
		"brand": {Light: "#f5225f", Dark: "#ff6b6b"}, "canvas": {Light: "#ffeaec", Dark: "#16161e"},
		"paper": {Light: "#ffffff", Dark: "#1e2030"}, "panel": {Light: "#f7eff1", Dark: "#181a26"},
		"line": {Light: "#e5e3e5", Dark: "#2c3145"}, "ink": {Light: "#242442", Dark: "#c0caf5"},
	})
	contrastFails(t, kitwork, content(`<content><link value="/" background="brand" color="paper" hover:background="brand+15">Bắt đầu</link></content>`), `color=`,
		`color="paper" trên nền brand chỉ 3.98:1 ở chế độ sáng; chữ thường cần 4.5:1 (chữ lớn 3:1). Không một màu chữ nào, kể cả trắng hay đen, đọc được trên mọi nền của nó ở mọi điều kiện: viết màu chữ riêng cho từng điều kiện (như hover:color), hoặc đổi nền brand (background, material) hay khai lại brand trong router.colors.`)
	contrastPasses(t, kitwork, content(`<content><link value="/" background="brand" color="brand.on" hover:color="paper" hover:background="brand+15">Bắt đầu</link></content>`))
	contrastFails(t, darkPalette(t), content(`<content><box background="brand" material="glass" color="brand.on"><text>a</text></box></content>`), `color=`,
		`color="brand.on" trên nền kính brand (material="glass") phủ trên canvas chỉ 3.12:1 ở chế độ sáng; chữ thường cần 4.5:1 (chữ lớn 3:1). Không màu chữ nào, kể cả trắng hay đen, đọc được trên nền này: đổi nền brand (background, material) hay khai lại brand trong router.colors.`)
}

// open: follows the openers around an element (an accordion, a button written expanded): an open:
// colour on the accordion itself is never painted (its CSS needs an open element around it), one
// inside it is, and an inner accordion's own open: is, while the outer one is open.
// CONTROLS: the last two fall short when open.
func TestContrastOpenFollowsTheOpener(t *testing.T) {
	contrastPasses(t, nil, content(`<content><accordions><accordion open:background="ink"><text click="toggle()">Hỏi</text><text>Đáp</text></accordion></accordions></content>`))
	for _, source := range []string{
		`<content><accordions><accordion><text click="toggle()">Hỏi</text><box open:background="ink"><text>Đáp</text></box></accordion></accordions></content>`,
		`<content><accordions><accordion><text click="toggle()">Hỏi</text><accordions><accordion open:background="ink"><text click="toggle()">Hỏi</text><text>Đáp</text></accordion></accordions></accordion></accordions></content>`,
	} {
		if got := contrastCompile(t, nil, content(source)); !strings.Contains(got, `trên nền ink chỉ 1.00:1 ở chế độ sáng khi open:`) {
			t.Errorf("CONTROL: %s\n got %q", source, got)
		}
	}
}

// The pair is measured as the pixel a screen paints, 8 bits a channel: brand+7 at 90% over paper is
// #6c5efe, 4.49:1, though the colour in floats is 4.51:1. CONTROL: at 95% it reads.
func TestContrastMeasuresThePixel(t *testing.T) {
	contrastFails(t, nil, content(`<content><card><text color="brand+7/90">Đặt món</text></card></content>`), `color=`,
		`color="brand+7/90" trên nền paper chỉ 4.49:1 ở chế độ sáng; chữ thường cần 4.5:1 (chữ lớn 3:1). Dùng color="brand+7" (5.36:1 sáng).`)
	contrastPasses(t, nil, content(`<content><card><text color="brand+7/95">Đặt món</text></card></content>`))
	// X+N with an alpha composites the #rrggbb of its custom property, as the page does: danger+12 is
	// #df2437, at 95% over paper #e12f41, 4.48:1 (the unrounded colour would be #e02f41, 4.51:1)
	contrastFails(t, nil, content(`<content><card><text color="danger+12/95">Đặt món</text></card></content>`), `color=`,
		`color="danger+12/95" trên nền paper chỉ 4.48:1 ở chế độ sáng; chữ thường cần 4.5:1 (chữ lớn 3:1). Dùng color="danger+12" (4.72:1 sáng).`)
}

// The engine's own colours pass on Kit Design's default palette, light and dark, in both modes of
// router.contrast (decision D): a page that writes no colour tells nothing — no warning by default, no
// error under "strict" — so a site that leaves colour to the engine never gets unreadable text. Every
// element the engine paints is here on every surface it sits on: page text, headings, links, a hint and
// an error in a field, a badge of every accent, a button, a card, a panel section. CONTROL: one colour
// written short of contrast on the same page is told, once.
func TestEngineDefaultsTellNothing(t *testing.T) {
	var badges strings.Builder
	for _, accent := range []string{"brand", "success", "warning", "danger", "info", "ink-30"} {
		badges.WriteString(`<badge color="` + accent + `"><text>` + accent + `</text></badge>`)
	}
	surface := func(open, close string) string {
		return open + `<text heading="1">Thực đơn</text><text heading="3">Hôm nay</text><text>Cà phê sữa</text>` +
			`<text size="small">Ghi chú</text><link value="/menu">Xem thêm</link>` + badges.String() +
			`<form><field><label>Số điện thoại</label><input type="tel"></input><hint>Quán gọi lại.</hint><error /></field>` +
			`<button>Gửi</button><input type="submit" value="Đặt" /></form>` + close
	}
	page := `<content>` + surface(`<box>`, `</box>`) + surface(`<card>`, `</card>`) + surface(`<section background="panel">`, `</section>`) +
		surface(`<section background="paper">`, `</section>`) + `</content>`
	for _, palette := range []*stylesheet.Palette{nil, darkPalette(t)} {
		contrastPasses(t, palette, content(page))
	}
	// CONTROL: the same page with one colour written short of contrast is told it, once
	short := strings.Replace(page, `<text>Cà phê sữa</text>`, `<text color="ink-60">Cà phê sữa</text>`, 1)
	if got := contrastCompile(t, nil, content(short)); strings.Count(got, `color="ink-60" trên nền canvas`) != 1 || strings.Contains(got, "\n") {
		t.Errorf("CONTROL: one pair written short is told once, got:\n%s", got)
	}
}

// A colour that falls short is told the step of its own colour that reads, by fives (operators of
// 13/10/2026): a token written plain its smallest stronger step — brand on the canvas of Kit Design, of
// the kitwork example and of the quancaphe conformance fixture is told brand+N, N the first multiple of
// 5 at which brand+N reads 4.5:1 on that canvas in every mode, with its ratios; a stronger step the next
// stronger ones (brand+5: brand+10 …); a lighter one steps back toward the colour itself (ink-30: ink-25,
// ink-20 … ink-5, then ink), the first that reads. The steps and ratios are measured here from the
// palette, not read from the check. When that one step is 20 or more past what one mode needs alone
// (decision of 13/10/2026, option B), a note follows: what brand+N is in that mode, and the role of the
// site that keeps the hue in both, its pair the least step of each mode. CONTROL: the step told is the
// first that reads: the one before it falls short in some mode; Kit Design's brand+5 is 5 past dark's
// brand and gets no note.
func TestContrastSuggestsStronger(t *testing.T) {
	// reads measures text on ground in every mode of a palette, each as a screen paints it
	reads := func(p *stylesheet.Palette, text, ground string) (string, bool) {
		var ratios []string
		ok := true
		for _, m := range p.Modes() {
			fg, known := p.Paint(text, m)
			bg, under := p.Paint(ground, m)
			if !known || !under {
				t.Fatalf("%s on %s: the palette paints neither", text, ground)
			}
			r := stylesheet.Ratio(fg.Painted(), bg.Painted())
			ok = ok && r >= stylesheet.NormalTextRatio
			ratios = append(ratios, ratioText(r, 0)+":1 "+m.Word())
		}
		return " (" + strings.Join(ratios, ", ") + ")", ok
	}
	// least is the first step from 0 at which brand+N reads on ground in one mode alone, and its colour
	least := func(p *stylesheet.Palette, ground string, m stylesheet.Mode) (int, string) {
		for n := 0; n <= 100; n += 5 {
			text := "brand"
			if n > 0 {
				text += "+" + strconv.Itoa(n)
			}
			fg, _ := p.Paint(text, m)
			bg, _ := p.Paint(ground, m)
			if stylesheet.Ratio(fg.Painted(), bg.Painted()) >= stylesheet.NormalTextRatio {
				return n, fg.Hex()
			}
		}
		t.Fatalf("no brand+N reads on %s in %s", ground, m.Word())
		return 0, ""
	}
	notes := 0
	palettes := codeExamplePalettes(t)
	for _, name := range []string{"Kit Design", "kitwork", "quancaphe"} {
		palette := palettes[name]
		if _, ok := reads(palette, "brand", "canvas"); ok {
			t.Fatalf("%s: brand reads on canvas, so nothing is told and this proves nothing", name)
		}
		for _, written := range []string{"brand", "brand+5"} {
			from := 5
			if written == "brand+5" {
				from = 10
				if _, ok := reads(palette, written, "canvas"); ok {
					continue // reads already: nothing to tell
				}
			}
			step, ratios := 0, ""
			for n := from; n <= 100 && step == 0; n += 5 {
				if r, ok := reads(palette, "brand+"+strconv.Itoa(n), "canvas"); ok {
					step, ratios = n, r
				}
			}
			if step == 0 {
				t.Fatalf("%s: no brand+N reads on canvas", name)
			}
			if step > from {
				if _, ok := reads(palette, "brand+"+strconv.Itoa(step-5), "canvas"); ok {
					t.Fatalf("%s: brand+%d reads too: the step measured is not the first", name, step-5)
				}
			}
			got := contrastCompile(t, palette, content(`<content><text color="`+written+`">Đặt món</text></content>`))
			want := `. Dùng color="brand+` + strconv.Itoa(step) + `"` + ratios + `.`
			_, tail, told := strings.Cut(got, want)
			if !told || strings.Contains(got, "\n") {
				t.Errorf("%s: color=%q is told its first stronger step that reads:\n got  %s\n want …%s", name, written, got, want)
				continue
			}
			lightStep, lightHex := least(palette, "canvas", stylesheet.LightMode)
			darkStep, darkHex := least(palette, "canvas", stylesheet.DarkMode)
			if step-lightStep < 20 && step-darkStep < 20 {
				if tail != "" {
					t.Errorf("%s: color=%q: brand+%d is under 20 past each mode's brand+%d, brand+%d, yet a note follows:%s", name, written, step, lightStep, darkStep, tail)
				}
				continue
			}
			notes++
			strong, _ := palette.Paint("brand+"+strconv.Itoa(step), stylesheet.DarkMode)
			for _, part := range []string{
				" brand+" + strconv.Itoa(step) + " là một bậc cho cả hai chế độ: ở chế độ tối nó là " + strong.Hex() + ",",
				`router.colors({ "brand.text": { light: "` + lightHex + `", dark: "` + darkHex + `" } }) rồi viết color="brand.text".`,
			} {
				if !strings.Contains(tail, part) || !strings.HasSuffix(tail, ".") {
					t.Errorf("%s: color=%q: brand+%d is %d past dark's brand+%d: want %q in the note:%s", name, written, step, step-darkStep, darkStep, part, tail)
				}
			}
		}
	}

	if notes == 0 {
		t.Error("CONTROL: no palette is told the note: the kitwork example's brand+35 is 35 past dark's brand")
	}

	// a lighter colour steps back toward the colour itself: on a grey ink, ink-5 is the first that
	// reads; on a lighter grey none of ink-25 … ink-5 does, and ink itself is told
	for _, c := range []struct{ ink, want string }{{"#6e6e6e", "ink-5"}, {"#767676", "ink"}} {
		palette, err := stylesheet.NewPalette(false, map[string]stylesheet.Color{"canvas": {Light: "#ffffff"}, "ink": {Light: c.ink}})
		if err != nil {
			t.Fatal(err)
		}
		first, ratios := "", ""
		for _, v := range []string{"ink-25", "ink-20", "ink-15", "ink-10", "ink-5", "ink"} {
			if r, ok := reads(palette, v, "canvas"); ok {
				first, ratios = v, r
				break
			}
		}
		if first != c.want {
			t.Fatalf("ink %s: measured %q first, the case wants %q: this proves nothing", c.ink, first, c.want)
		}
		got := contrastCompile(t, palette, content(`<content><text color="ink-30">Ghi chú</text></content>`))
		if want := `. Dùng color="` + first + `"` + ratios + `.`; !strings.HasSuffix(got, want) {
			t.Errorf("ink %s: color=\"ink-30\" steps back toward ink:\n got  %s\n want …%s", c.ink, got, want)
		}
	}
}
