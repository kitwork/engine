package ui

import (
	"strings"
	"testing"

	"github.com/kitwork/engine/ui/stylesheet"
)

// Text on a gradient background is measured against its eleven samples, each composited over what lies
// beneath it, in light and in dark, a warning by default like all contrast (the same line is the error
// under "strict", contrastCompile). White on gilded falls short at the pale jasmine stop, 40% along it,
// with the fixed colour that reads on all of it; ink-30 on tint inside a card reads (the wash over the
// card's paper), and so does a gradient text headline on the page. Gradient text is measured sample by
// sample against its ground: the fixed nebula is large text only, so as normal text it falls short, at
// its worst sample, mediumorchid halfway (#ba55d3 on the canvas #f6f9fc), and as a heading it reads.
// The ratios were checked by hand from the hex values (jasmine #f8de7e under white: 1.33:1). CONTROL:
// white on night, the dark fixed band, reads.
func TestContrastMeasuresAGradientSampleBySample(t *testing.T) {
	contrastFails(t, nil, content(`<content><box background="gilded" padding="4"><text color="white">Gói Pro</text></box></content>`), `color=`,
		`color="white" trên nền gilded tại 40% chỉ 1.33:1 ở chế độ sáng; chữ thường cần 4.5:1 (chữ lớn 3:1). Dùng color="black" (6.45:1 sáng).`)
	contrastFails(t, darkPalette(t), content(`<content><text color="nebula">Trà</text></content>`), `color=`,
		`color="nebula" tại 50% trên nền canvas chỉ 3.73:1 ở chế độ sáng; chữ thường cần 4.5:1 (chữ lớn 3:1). Dùng color="ink" (14.70:1 sáng, 11.26:1 tối).`)
	contrastPasses(t, darkPalette(t), content(`<content><text heading="1" color="nebula">Trà</text></content>`))
	contrastPasses(t, darkPalette(t), content(`<content><card background="tint" padding="4"><text color="ink-30">Gói được chọn</text></card><text heading="1" color="headline">Kitwork</text></content>`))
	contrastPasses(t, darkPalette(t), content(`<content><box background="night" padding="4"><text color="white">Chân trang</text></box></content>`))
}

// A gradient whose stops let what is beneath show through is measured over it: the colour a background
// written for that condition or an earlier one leaves under the image (a gradient class sets only the
// image), else the surface the element's kind has, else what lies behind the element. A site's veil,
// paper/0 to paper, inside a brand box is brand at the top: ink-30 is 1.40:1 there (#4a5f75 on #635bff,
// by hand), and no one colour reads on all of it (white is 4.70:1 on the brand but not on the paper
// foot, black 4.47:1 on the brand), so the ground is to change: the gradient in router.gradients, not a
// token in router.colors. Written under hover over a canvas background on the same box, it is canvas at
// the top, where ink-30 reads: beneath it is the box's own canvas, not the brand around it.
func TestContrastMeasuresAGradientOverWhatIsBeneathIt(t *testing.T) {
	palette, err := stylesheet.NewPalette(false)
	if err != nil {
		t.Fatal(err)
	}
	palette, err = palette.WithGradients(map[string]stylesheet.Gradient{
		"veil": {Type: "linear", Direction: "to-bottom", Stops: []stylesheet.Stop{{Color: "paper/0", At: 0}, {Color: "paper", At: 100}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	contrastFails(t, palette, content(`<content><box background="brand" padding="4"><box background="veil" padding="4"><text color="ink-30">Mô tả</text></box></box></content>`), `color=`,
		`color="ink-30" trên nền veil tại 0% phủ trên brand chỉ 1.40:1 ở chế độ sáng; chữ thường cần 4.5:1 (chữ lớn 3:1). `+
			`Không màu chữ nào, kể cả trắng hay đen, đọc được trên nền này: đổi nền veil (background, material) hay sửa veil trong router.gradients.`)
	contrastPasses(t, palette, content(`<content><box background="brand" padding="4"><box background="canvas" hover:background="veil" padding="4"><text color="ink-30">Mô tả</text></box></box></content>`))
}

// A ground written lighter or stronger is changed where its gradient is (13/10/2026): the advice names
// the gradient without the step, hero for hero-80 and hero+20, in router.gradients; a standard gradient
// written with a step (cirrus-70) is changed only by writing another, so nothing more is said; a token,
// lighter or not, is declared again in router.colors. CONTROL end to end: text on veil+20 inside a
// brand box, where no colour reads, is told to change veil, never veil+20.
func TestRedeclareGroundStripsTheStep(t *testing.T) {
	palette, err := stylesheet.NewPalette(false)
	if err != nil {
		t.Fatal(err)
	}
	palette, err = palette.WithGradients(map[string]stylesheet.Gradient{
		"hero": {Type: "linear", Direction: "to-bottom", Stops: []stylesheet.Stop{{Color: "brand", At: 0}, {Color: "paper", At: 100}}},
		"veil": {Type: "linear", Direction: "to-bottom", Stops: []stylesheet.Stop{{Color: "paper/0", At: 0}, {Color: "paper", At: 100}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	c := &compiler{table: stylesheet.NewTable(palette)}
	for ground, want := range map[string]string{
		"hero":      " hay sửa hero trong router.gradients",
		"hero-80":   " hay sửa hero trong router.gradients",
		"hero+20":   " hay sửa hero trong router.gradients",
		"cirrus":    "",
		"cirrus-70": "",
		"cirrus+20": "",
		"brand":     " hay khai lại brand trong router.colors",
	} {
		if got := c.redeclareGround(ground); got != want {
			t.Errorf("redeclareGround(%q) = %q, want %q", ground, got, want)
		}
	}
	got := contrastCompile(t, palette, content(`<content><box background="brand" padding="4"><box background="veil+20" padding="4"><text color="ink-30">Mô tả</text></box></box></content>`))
	if want := `đổi nền veil (background, material) hay sửa veil trong router.gradients.`; !strings.HasSuffix(got, want) || !strings.Contains(got, `trên nền veil+20 tại 0% phủ trên brand`) {
		t.Errorf("text on veil+20 is told to change veil:\n got  %s\n want …%s", got, want)
	}
}

// A gradient the page would not draw as written is a compile error at the attribute to change (decision
// of 12/10/2026): gradient text on an element that also paints a background — one written on it at the
// same or any other condition, a glass, the background a pinned frame gets, the surface of a card or a
// badge — since the letters clip the background too: the text goes in a <text> of its own; gradient text
// on an icon, a mask its background paints; a glass over a gradient background. CONTROL: the same
// gradients each on an element of its own compile, and so does a glass that gives way to none where the
// gradient is written.
func TestGradientErrors(t *testing.T) {
	for _, c := range []struct{ source, needle, says string }{
		{`<content><box background="paper" color="headline"><text>Kitwork</text></box></content>`, `color=`,
			`color="headline" là chữ gradient: gradient được cắt theo nét chữ (background-clip:text), nên background="paper" trên cùng phần tử cũng bị cắt theo nét chữ, không còn là nền. ` +
				`Để chữ gradient trên một <text> riêng bên trong: <box background="paper"><text color="headline">…</text></box>`},
		{`<content><text color="headline" hover:background="panel">Kitwork</text></content>`, `color=`,
			`nên hover:background="panel" trên cùng phần tử cũng bị cắt theo nét chữ, không còn là nền. Để chữ gradient trên một <text> riêng bên trong: <text hover:background="panel"><text color="headline">…</text></text>`},
		{`<content><text medium:color="magma" background="glow">Giảm giá</text></content>`, `medium:color=`,
			`medium:color="magma" là chữ gradient: gradient được cắt theo nét chữ (background-clip:text), nên background="glow" trên cùng phần tử cũng bị cắt`},
		{`<content><box material="glass" color="headline"><text>Kitwork</text></box></content>`, `color=`,
			`nên kính (material="glass") trên cùng phần tử cũng bị cắt theo nét chữ`},
		{`<content><card color="headline"><text>Kitwork</text></card></content>`, `color=`,
			`nên nền paper của <card> cũng bị cắt theo nét chữ, không còn là nền. Để chữ gradient trên một <text> riêng bên trong: <card><text color="headline">…</text></card>`},
		{`<content><badge color="nebula">Mới</badge></content>`, `color=`, `nên nền của <badge> cũng bị cắt theo nét chữ`},
		{`<content><box pin="top" color="headline"><text>Kitwork</text></box></content>`, `color=`,
			`nên nền engine cho khung ghim vào mép (pin, để nội dung cuộn bên dưới không lộ qua) cũng bị cắt theo nét chữ`},
		{`<content><icon value="star" hover:color="nebula" alt="Yêu thích" /></content>`, `hover:color=`,
			`hover:color="nebula" trên <icon>: icon là một mặt nạ tô bằng nền của nó, chữ gradient không tô được icon (icon biến mất); tô icon bằng gradient là hover:background="nebula"`},
		{`<content><box material="glass" background="glow"><text>Kitwork</text></box></content>`, `material=`,
			`material="glass" đi cùng background="glow": kính làm từ màu nền của khung, gradient không làm kính được (trang sẽ vẽ gradient đè lên kính). ` +
				`Bỏ material, hoặc đặt gradient trên một khung riêng bên trong khung kính: <box background="glow">…</box>`},
		{`<content><box scrolled:material="glass" medium:background="tint"><text>Kitwork</text></box></content>`, `scrolled:material=`,
			`scrolled:material="glass" đi cùng medium:background="tint"`},
		// below medium, at rest, the thin glass and the glow hold together
		{`<content><box background="glow" medium:background="paper" material="glass/60" hover:material="none"><text>Kitwork</text></box></content>`, `material=`,
			`material="glass/60" đi cùng background="glow"`},
	} {
		files := content(c.source)
		mustFail(t, files, where("content.ui", c.source, c.needle), c.says)
	}
	for _, source := range []string{
		`<content><box background="paper"><text color="headline">Kitwork</text></box></content>`,
		`<content><card><text color="headline">Kitwork</text></card></content>`,
		`<content><icon value="star" background="nebula" alt="Yêu thích" /></content>`,
		`<content><box material="glass"><box background="glow"><text>Kitwork</text></box></box></content>`,
		// the glass gives way to none where the gradient is written
		`<content><box material="glass" medium:material="none" medium:background="glow"><text>Kitwork</text></box></content>`,
		// a colour written where the glass is takes the gradient away (background-image:none)
		`<content><box background="glow" medium:background="paper" medium:material="glass"><text>Kitwork</text></box></content>`,
	} {
		mustCompile(t, content(source))
	}
}

// A page with a gradient: its stylesheet has the gradient's rules, the classes are the attributes as
// written, and the right-to-left rule comes with a gradient that names start or end. The colour written
// for medium on the gradient's element takes the gradient back there, on that element only (the colour's
// selector with the gradient's class in :where()).
func TestGradientClassesOnAPage(t *testing.T) {
	s := memorySite("", content(`<content><box background="fade-end" medium:background="paper"><text heading="1" color="headline">Kitwork</text></box></content>`))
	s.Reload = true
	page, err := s.Compile("/site")
	if err != nil {
		t.Fatal(err)
	}
	html := page.Render(Request{Path: "/"})
	css := string(page.Stylesheet().CSS)
	for _, want := range []string{`background-fade-end medium:background-paper`, `color-headline`} {
		if !strings.Contains(html, want) {
			t.Errorf("the page misses the class %q:\n%s", want, html)
		}
	}
	for _, want := range []string{
		`.background-fade-end{background-image:linear-gradient(to right,`,
		`.background-fade-end:where(:dir(rtl)){background-image:linear-gradient(to left,`,
		".medium\\:background-paper{background-color:var(--paper)}\n.medium\\:background-paper:where(.background-fade-end){background-image:none}\n",
		`-webkit-background-clip:text;background-clip:text;-webkit-text-fill-color:transparent`,
	} {
		if !strings.Contains(css, want) {
			t.Errorf("the page's stylesheet misses %q:\n%s", want, css)
		}
	}
}

// The advice for a ground that is a site's gradient names router.gradients, never router.colors, which
// has no such token. Text the engine colours (the page's ink) on mist, a dimgray gradient, under a
// paper of light grey, reads in no colour the check suggests (ink 2.83:1; ink+35 and stronger are
// black, 3.83:1; paper #d0d0d0 3.56:1 on #696969, measured): the ink is to declare again for that
// ground, or the gradient changed. Two texts short on smoke (transparent white into black) are one
// line at the background, since they read on the canvas without it; no one colour reads on all of
// smoke, so the ground is to change, in router.gradients.
func TestContrastAdviceForAGradientGround(t *testing.T) {
	grey, err := stylesheet.NewPalette(false, map[string]stylesheet.Color{"paper": {Light: "#d0d0d0"}})
	if err != nil {
		t.Fatal(err)
	}
	grey, err = grey.WithGradients(map[string]stylesheet.Gradient{
		"mist": {Type: "linear", Direction: "to-end", Stops: []stylesheet.Stop{{Color: "dimgray", At: 0}, {Color: "dimgray", At: 100}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	contrastFails(t, grey, content(`<content><box background="mist" padding="4"><text>Trà</text></box></content>`), `background=`,
		`chữ ink mặc định của trang trên nền mist tại 0% chỉ 2.83:1 ở chế độ sáng; chữ thường cần 4.5:1 (chữ lớn 3:1). `+
			`Chữ này engine tô theo router.colors: khai lại ink trong router.colors bằng một cặp { light: "#…", dark: "#…" } đủ tương phản trên nền mist hay sửa mist trong router.gradients.`)
	palette, err := stylesheet.NewPalette(false)
	if err != nil {
		t.Fatal(err)
	}
	palette, err = palette.WithGradients(map[string]stylesheet.Gradient{
		"smoke": {Type: "linear", Direction: "to-bottom", Stops: []stylesheet.Stop{{Color: "white/0", At: 0}, {Color: "black", At: 100}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	source := `<content><box background="smoke" padding="4"><text color="ink-30">Một</text><text color="ink-30">Hai</text></box></content>`
	warned, _ := groupedCompile(t, palette, content(source))
	want := where("content.ui", source, `background=`) + `background="smoke" ở chế độ sáng; chữ ở 2 chỗ trên nó khó đọc`
	tail := `Không màu chữ gợi ý nào đọc được cho mọi chữ trên nó: đổi nền smoke (background, material) hay sửa smoke trong router.gradients. router.contrast("strict") nêu từng chỗ với gợi ý riêng.`
	if len(warned) != 1 || !strings.HasPrefix(warned[0], want) || !strings.HasSuffix(warned[0], tail) {
		t.Errorf("two texts on smoke, one line at it:\n got  %q\n want %s … %s", warned, want, tail)
	}
}
