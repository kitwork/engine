package ui

import (
	"strings"
	"testing"

	"github.com/kitwork/engine/ui/stylesheet"
)

// Text on a see-through fill of its own colour (decision F1: a neutral button's ink/14 pressed, a
// field's ink/8 with focus, under ink text) falls short because the colour is too light for the ground
// that fill makes with what lies behind it: the advice is the same colour stronger (13/10/2026: ink+5,
// the fill staying ink/8), or to declare that colour anew, on the ground as it is shown (ink/8 phủ
// trên canvas), never paper, the other end of the palette, "trên nền ink" — a ground no reader sees.
// Measured as before: ink #717171 on ink/8 over a white canvas is #f4f4f4, 4.44:1; ink #6a6a6a on
// ink/14 over white is #eaeaea, 4.49:1 (both by hand). The same for the ink a <page> writes over a
// backdrop (the kitwork example's ink on ink/14 over cirrus, 4.48:1). An ink already black, on a
// canvas and a paper of mid grey #767676, has no stronger step that reads (ink+N is black too), nor
// does paper: then the advice is to declare ink anew on the ground as shown. CONTROL: words in a
// section painted solid ink are told to take paper.
func TestAdviceOnTheTextsOwnFill(t *testing.T) {
	grey := func(ink string) *stylesheet.Palette {
		p, err := stylesheet.NewPalette(false, map[string]stylesheet.Color{"canvas": {Light: "#ffffff"}, "ink": {Light: ink}})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	// black ink on a mid-grey canvas and paper: ink+N is black, and neither it nor paper reads on the
	// ink over that grey
	black, err := stylesheet.NewPalette(false, map[string]stylesheet.Color{"canvas": {Light: "#767676"}, "paper": {Light: "#767676"}, "ink": {Light: "#000000"}})
	if err != nil {
		t.Fatal(err)
	}
	declare := "khai lại ink trong router.colors bằng một cặp { light: \"#…\", dark: \"#…\" } đủ tương phản trên nền "
	for _, c := range []struct {
		name    string
		palette *stylesheet.Palette
		files   map[string]string
		want    []string
	}{
		{"a field's words with focus, the page's ink", grey("#717171"),
			content(`<content><field><label>Tên</label><input></input></field></content>`),
			[]string{"trên nền ink/8 phủ trên canvas chỉ 4.44:1", "khi focus:", `Chữ này engine tô theo router.colors: viết color="ink+5" (4.85:1 sáng), hoặc khai lại ink trong router.colors cho đủ tương phản.`}},
		{"a neutral button pressed, ink written on a box", grey("#6a6a6a"),
			content(`<content><box color="ink"><button>Gửi</button></box></content>`),
			[]string{`color="ink" trên nền ink/14 phủ trên canvas chỉ 4.49:1`, "khi pressed:", `Dùng color="ink+5" (4.99:1 sáng).`}},
		{"a neutral button pressed on cirrus, ink written on <page>", kitworkPalette(t),
			map[string]string{"/site/page.ui": `<page background="cirrus" color="ink"><content /></page>`, "/site/content.ui": `<content><button>Gửi</button></content>`},
			[]string{`page.ui:1:27: color="ink" trên nền ink/14 phủ trên cirrus tại 0% chỉ 4.48:1`, "khi pressed:", `Dùng color="ink+5" (4.95:1 sáng, 6.37:1 tối).`}},
		// on the grey even the fills at rest fall short (a field's ink/5, a button's ink/6)
		{"a field's words, a black ink on grey", black,
			content(`<content><field><label>Tên</label><input></input></field></content>`),
			[]string{"trên nền ink/5 phủ trên canvas chỉ 4.24:1", "Chữ này engine tô theo router.colors: " + declare + "ink/5 phủ trên canvas."}},
		{"a neutral button, a black ink written on a box", black,
			content(`<content><box color="ink"><button>Gửi</button></box></content>`),
			[]string{`color="ink" trên nền ink/6 phủ trên canvas chỉ 4.18:1`, "Không màu gợi ý nào đạt ở mọi chế độ và điều kiện: " + declare + "ink/6 phủ trên canvas."}},
	} {
		got := contrastCompile(t, c.palette, c.files)
		for _, want := range c.want {
			if !strings.Contains(got, want) {
				t.Errorf("%s: want %q in\n%s", c.name, want, got)
			}
		}
		if strings.Contains(got, "khai paper") || strings.Contains(got, "trên nền ink.") || strings.Contains(got, "khai lại ink hoặc ink") || strings.Contains(got, `color="paper"`) {
			t.Errorf("%s: told to declare the other end of the palette, on a ground no reader sees:\n%s", c.name, got)
		}
	}

	// CONTROL: on a solid ink section the words take paper
	got := contrastCompile(t, nil, content(`<content><section background="ink"><text>Câu</text></section></content>`))
	if !strings.Contains(got, `trên nền ink chỉ`) || !strings.Contains(got, `color="paper"`) || strings.Contains(got, "khai lại ink") {
		t.Errorf("CONTROL: words on a solid ink section are told to take paper, got %q", got)
	}
}

// kitworkPalette is the kitwork example's router.colors (docs/examples/kitwork, router.work).
func kitworkPalette(t *testing.T) *stylesheet.Palette {
	t.Helper()
	return darkPalette(t, map[string]stylesheet.Color{
		"brand": {Light: "#f5225f", Dark: "#ff6b6b"}, "canvas": {Light: "#ffeaec", Dark: "#16161e"},
		"paper": {Light: "#ffffff", Dark: "#1e2030"}, "panel": {Light: "#f7eff1", Dark: "#181a26"},
		"line": {Light: "#e5e3e5", Dark: "#2c3145"}, "ink": {Light: "#242442", Dark: "#c0caf5"},
	})
}
