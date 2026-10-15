package ui

import "testing"

// A named colour is measured as a fixed colour: its own hex in light and in dark, like white and black
// (the ratios below were checked by hand from #ffd700, #000080 and the palette's values). On a dark
// look, ink text on a gold box reads in light (11.08:1) and falls short in dark, where ink turns light
// and gold stays gold (1.13:1); the advice is a fixed ground's: a surface that follows the mode, or
// black text, 14.97:1 in both modes because gold is the same in both. Gold text on the canvas falls
// short in light (1.33:1) and reads in dark. An alpha is composited over what is below in each mode:
// white on navy/50 is 3.80:1 over the light canvas and 17.77:1 over the dark one; navy solid is
// 16.01:1 in both. CONTROL: on a light-only palette the gold box passes, nothing to measure in dark.
func TestContrastMeasuresANamedColourAsFixed(t *testing.T) {
	palette := darkPalette(t)
	contrastFails(t, palette, content(`<content><box background="gold"><text>Trà</text></box></content>`), `background=`,
		`chữ ink mặc định của trang trên nền gold chỉ 1.13:1 ở chế độ tối; chữ thường cần 4.5:1 (chữ lớn 3:1). `+
			`Nền gold là màu cố định, không đổi theo chế độ như chữ: viết background="paper" (15.54:1 sáng, 10.04:1 tối), `+
			`hoặc giữ nền gold và viết color="black" (14.97:1 sáng, 14.97:1 tối).`)
	contrastFails(t, palette, content(`<content><text color="gold">Trà</text></content>`), `color=`,
		`color="gold" trên nền canvas chỉ 1.33:1 ở chế độ sáng; chữ thường cần 4.5:1 (chữ lớn 3:1). Dùng color="ink" (14.70:1 sáng, 11.26:1 tối).`)
	contrastFails(t, palette, content(`<content><box background="navy/50"><text color="white">Trà</text></box></content>`), `color=`,
		`color="white" trên nền navy/50 phủ trên canvas chỉ 3.80:1 ở chế độ sáng; chữ thường cần 4.5:1 (chữ lớn 3:1). `+
			`Dùng background="navy" trên <box> (content.ui:1:10) (16.01:1 sáng, 16.01:1 tối).`)
	contrastPasses(t, nil, content(`<content><box background="gold"><text>Trà</text></box></content>`))
}
