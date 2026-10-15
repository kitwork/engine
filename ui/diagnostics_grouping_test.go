package ui

import (
	"errors"
	"strings"
	"testing"

	"github.com/kitwork/engine/ui/stylesheet"
)

// groupedCompile compiles a site with a palette and returns its warnings, one a line, and the lines of
// the same site's compile error under router.contrast("strict"): grouping is the default mode's, so
// unlike contrastCompile the two may differ.
func groupedCompile(t *testing.T, palette *stylesheet.Palette, files map[string]string) (warned, strict []string) {
	t.Helper()
	s := memorySite("", files)
	s.Reload = true
	s.Palette = palette
	page, err := s.Compile("/site")
	if err != nil {
		t.Fatalf("by default a pair short of contrast is a warning, the page compiles; got the error:\n%v", err)
	}
	for _, w := range page.Warnings() {
		warned = append(warned, w.String())
	}
	s.Contrast = ContrastStrict
	_, err = s.Compile("/site")
	var all Errors
	var one *Error
	switch {
	case errors.As(err, &all):
		for _, e := range all {
			strict = append(strict, e.Error())
		}
	case errors.As(err, &one):
		strict = []string{one.Error()}
	}
	return warned, strict
}

// whitePage is a page written background="white" — white in the dark too — under text that follows
// the mode: the page's own colour (ink, inherited by a text of the content), and ink-30 written in a
// region and twice in the content. Each falls short in the dark only, and each reads once the white is
// left out (on the canvas).
var whitePage = map[string]string{
	"/site/page.ui":    `<page background="white" color="ink"><header /><content /></page>`,
	"/site/header.ui":  `<header><text color="ink-30">Quán</text></header>`,
	"/site/content.ui": "<content><text>Cà phê</text>\n<text color=\"ink-30\">Trà</text>\n<text color=\"ink-30\">Bánh</text></content>",
}

// Pairs that fall short because of one background are one warning at that background, by default: the
// background as written and that it stays white in the dark, at how many places, each text colour with
// its worst ratio, the first places, and what reads for every one of them (the surface that follows the
// mode, or black text: never a token to declare). "strict" keeps each pair its own error, with its own
// suggestion. CONTROLS: the same page on the canvas tells nothing; one place alone on the white is told
// alone, as strict tells it.
func TestContrastGroupsPairsByTheirBackground(t *testing.T) {
	palette := darkPalette(t)
	warned, strict := groupedCompile(t, palette, whitePage)
	want := `page.ui:1:7: background="white" giữ màu trắng ở chế độ tối; chữ ở 4 chỗ trên nó khó đọc (ink-30 3.15:1, ink 1.59:1; chữ thường cần 4.5:1, chữ lớn 3:1): ` +
		`header.ui:1:15, page.ui:1:26, content.ui:2:7, 3:7. Viết background="canvas" (6.24:1 sáng, 5.67:1 tối), hoặc giữ nền trắng và viết color="black" cho chữ trên nó (21.00:1 sáng, 21.00:1 tối). ` +
		`router.contrast("strict") nêu từng chỗ với gợi ý riêng.`
	if strings.Join(warned, "\n") != want {
		t.Errorf("one line at the background:\n got  %q\n want %q", warned, want)
	}
	advice := ` Nền white là màu cố định, không đổi theo chế độ như chữ: viết background="canvas" ở page.ui:1:7 (6.24:1 sáng, 5.67:1 tối), hoặc giữ nền trắng và viết color="black" (21.00:1 sáng, 21.00:1 tối).`
	wantStrict := []string{
		`header.ui:1:15: color="ink-30" trên nền white chỉ 3.15:1 ở chế độ tối; chữ thường cần 4.5:1 (chữ lớn 3:1).` + advice,
		`page.ui:1:26: color="ink" trên nền white chỉ 1.59:1 ở chế độ tối; chữ thường cần 4.5:1 (chữ lớn 3:1). Nền white là màu cố định, không đổi theo chế độ như chữ: viết background="canvas" ở page.ui:1:7 (14.70:1 sáng, 11.26:1 tối), hoặc giữ nền trắng và viết color="black" (21.00:1 sáng, 21.00:1 tối).`,
		`content.ui:2:7: color="ink-30" trên nền white chỉ 3.15:1 ở chế độ tối; chữ thường cần 4.5:1 (chữ lớn 3:1).` + advice,
		`content.ui:3:7: color="ink-30" trên nền white chỉ 3.15:1 ở chế độ tối; chữ thường cần 4.5:1 (chữ lớn 3:1).` + advice,
	}
	if strings.Join(strict, "\n") != strings.Join(wantStrict, "\n") {
		t.Errorf("strict: each pair its own error:\n got  %q\n want %q", strict, wantStrict)
	}

	// CONTROL: the canvas follows the mode; nothing to tell
	canvas := copyFiles(whitePage)
	canvas["/site/page.ui"] = `<page background="canvas" color="ink"><header /><content /></page>`
	if warned, strict := groupedCompile(t, palette, canvas); len(warned) != 0 || len(strict) != 0 {
		t.Errorf("CONTROL: on the canvas nothing falls short, got %q / %q", warned, strict)
	}
	// CONTROL: one place alone on the white is told alone, at its colour, as strict tells it
	alone := map[string]string{
		"/site/page.ui":    `<page background="white"><content /></page>`,
		"/site/content.ui": `<content><text color="ink-30">Trà</text></content>`,
	}
	warned, strict = groupedCompile(t, palette, alone)
	if len(warned) != 1 || warned[0] != `content.ui:1:16: color="ink-30" trên nền white chỉ 3.15:1 ở chế độ tối; chữ thường cần 4.5:1 (chữ lớn 3:1).`+advice ||
		strings.Join(warned, "\n") != strings.Join(strict, "\n") {
		t.Errorf("CONTROL: one place is told alone, as strict tells it:\n warned %q\n strict %q", warned, strict)
	}
}

// A group is one background, in one mode, under one condition: the texts of one white box that fall
// short on every screen are one line, those that fall short from medium only a second line at the same
// background, saying when; a second white box is a line of its own. Where no surface reads for every
// pair of a group (black text on the paper in the dark), only the text colour is told, in the attribute
// the texts write it in.
func TestContrastGroupsByModeAndCondition(t *testing.T) {
	files := content("<content><box background=\"white\"><text color=\"ink-30\">a</text>\n<text color=\"ink-30\">b</text>\n" +
		"<text medium:color=\"ink-30\" color=\"black\">c</text>\n<text medium:color=\"ink-30\" color=\"black\">d</text></box>\n" +
		"<box background=\"white\"><text color=\"ink-30\">e</text>\n<text color=\"ink-30\">f</text></box></content>")
	warned, strict := groupedCompile(t, darkPalette(t), files)
	const tail = ` router.contrast("strict") nêu từng chỗ với gợi ý riêng.`
	want := []string{
		`content.ui:1:15: background="white" giữ màu trắng ở chế độ tối; chữ ở 2 chỗ trên nó khó đọc (ink-30 3.15:1; chữ thường cần 4.5:1, chữ lớn 3:1): content.ui:1:40, 2:7. ` +
			`Viết background="paper" (6.59:1 sáng, 5.06:1 tối), hoặc giữ nền trắng và viết color="black" cho chữ trên nó (21.00:1 sáng, 21.00:1 tối).` + tail,
		`content.ui:1:15: background="white" giữ màu trắng ở chế độ tối khi medium:; chữ ở 2 chỗ trên nó khó đọc (ink-30 3.15:1; chữ thường cần 4.5:1, chữ lớn 3:1): content.ui:3:7, 4:7. ` +
			`Giữ nền trắng và viết medium:color="black" cho chữ trên nó (21.00:1 sáng, 21.00:1 tối).` + tail,
		`content.ui:5:6: background="white" giữ màu trắng ở chế độ tối; chữ ở 2 chỗ trên nó khó đọc (ink-30 3.15:1; chữ thường cần 4.5:1, chữ lớn 3:1): content.ui:5:31, 6:7. ` +
			`Viết background="paper" (6.59:1 sáng, 5.06:1 tối), hoặc giữ nền trắng và viết color="black" cho chữ trên nó (21.00:1 sáng, 21.00:1 tối).` + tail,
	}
	if strings.Join(warned, "\n") != strings.Join(want, "\n") {
		t.Errorf("a line a background, mode and condition:\n got  %q\n want %q", warned, want)
	}
	if len(strict) != 6 {
		t.Errorf("strict: the six pairs, each its own error, got %q", strict)
	}
}

// A pair is grouped at its background only when that background is why it falls short: two texts
// whose own colour falls short on the background and on the canvas behind it alike are each told at
// their colour. CONTROL: two texts that read on the canvas and fall short on the brand fill are one line
// at the fill, told the label colour that reads for both.
func TestContrastGroupsOnlyWhatTheBackgroundCauses(t *testing.T) {
	own := content("<content><box background=\"paper\"><text color=\"ink-80\">a</text>\n<text color=\"ink-70\">b</text></box></content>")
	warned, strict := groupedCompile(t, nil, own)
	wantOwn := []string{
		`content.ui:1:40: color="ink-80" trên nền paper chỉ 1.65:1 ở chế độ sáng; chữ thường cần 4.5:1 (chữ lớn 3:1). Dùng color="ink-40" (4.87:1 sáng).`,
		`content.ui:2:7: color="ink-70" trên nền paper chỉ 2.10:1 ở chế độ sáng; chữ thường cần 4.5:1 (chữ lớn 3:1). Dùng color="ink-40" (4.87:1 sáng).`,
	}
	if strings.Join(warned, "\n") != strings.Join(wantOwn, "\n") || strings.Join(strict, "\n") != strings.Join(wantOwn, "\n") {
		t.Errorf("each text's own colour is told at it:\n warned %q\n strict %q\n want   %q", warned, strict, wantOwn)
	}
	fill := content("<content><section background=\"brand\"><text>a</text>\n<text color=\"ink\">b</text></section></content>")
	warned, strict = groupedCompile(t, nil, fill)
	want := `content.ui:1:19: background="brand" ở chế độ sáng; chữ ở 2 chỗ trên nó khó đọc (ink 3.31:1; chữ thường cần 4.5:1, chữ lớn 3:1): content.ui:1:19, 2:7. ` +
		`Dùng color="brand.on" cho chữ trên nó (4.70:1 sáng). router.contrast("strict") nêu từng chỗ với gợi ý riêng.`
	if len(warned) != 1 || warned[0] != want || len(strict) != 2 {
		t.Errorf("CONTROL: the fill is why, one line at it:\n got  %q\n want %q\n strict %q", warned, want, strict)
	}
}

// A group's line suggests one X+N for every text on its background, and when that X+N goes 20 or more
// past what one mode needs for all of them, the line carries the note a place told alone has (option B),
// each mode's step the least that reads for every pair of the group, the role written as the line writes
// its colour ("cho chữ trên nó"): the kitwork example's two brand+15 on <page background="cirrus-70">.
// strict tells each place with its own note. CONTROL: on Kit Design's band #3c3c3c, brand+15 for both is
// 15 past light's brand, and the line has no note.
func TestContrastGroupLineCarriesTheOvershootNote(t *testing.T) {
	files := map[string]string{
		"/site/page.ui":    `<page background="cirrus-70" color="ink"><content /></page>`,
		"/site/content.ui": "<content><text color=\"brand+15\">a</text>\n<text color=\"brand+15\">b</text></content>",
	}
	warned, strict := groupedCompile(t, kitworkExampleColors(t), files)
	note := `brand+35 là một bậc cho cả hai chế độ: ở chế độ tối nó là #ffccc8, gần trắng, chỉ còn 32% sắc của brand, mà tối chỉ cần brand (5.96:1). ` +
		`Muốn giữ sắc ở cả hai chế độ thì khai một vai trò riêng: router.colors({ "brand.text": { light: "#bb0042", dark: "#ff6b6b" } }) rồi viết color="brand.text"`
	want := `page.ui:1:7: background="cirrus-70" ở chế độ sáng; chữ ở 2 chỗ trên nó khó đọc (brand+15 3.62:1; chữ thường cần 4.5:1, chữ lớn 3:1): content.ui:1:16, 2:7. ` +
		`Dùng color="brand+35" cho chữ trên nó (4.83:1 sáng, 11.60:1 tối). ` + note + ` cho chữ trên nó. router.contrast("strict") nêu từng chỗ với gợi ý riêng.`
	if strings.Join(warned, "\n") != want {
		t.Errorf("the group line carries the note:\n got  %q\n want %q", warned, want)
	}
	one := `color="brand+15" trên nền cirrus-70 tại 0% chỉ 3.62:1 ở chế độ sáng; chữ thường cần 4.5:1 (chữ lớn 3:1). Dùng color="brand+35" (4.83:1 sáng, 11.60:1 tối). ` + note + `.`
	if wantStrict := "content.ui:1:16: " + one + "\ncontent.ui:2:7: " + one; strings.Join(strict, "\n") != wantStrict {
		t.Errorf("strict: each place with its own note:\n got  %q\n want %q", strict, wantStrict)
	}

	// CONTROL: 15 past light's brand, no note on the line
	band := darkPalette(t, map[string]stylesheet.Color{"page.band": {Light: "#ffffff", Dark: "#3c3c3c"}})
	warned, _ = groupedCompile(t, band, content("<content><box background=\"page.band\"><text color=\"brand\">a</text>\n<text color=\"brand\">b</text></box></content>"))
	wantControl := `content.ui:1:15: background="page.band" ở chế độ tối; chữ ở 2 chỗ trên nó khó đọc (brand 3.43:1; chữ thường cần 4.5:1, chữ lớn 3:1): content.ui:1:44, 2:7. ` +
		`Dùng color="brand+15" cho chữ trên nó (6.29:1 sáng, 4.53:1 tối). router.contrast("strict") nêu từng chỗ với gợi ý riêng.`
	if strings.Join(warned, "\n") != wantControl {
		t.Errorf("CONTROL: 15 past, no note:\n got  %q\n want %q", warned, wantControl)
	}
}

// A fixed colour (white, black) written as a background under text that follows the mode, told alone:
// the surface that follows the mode too, or the fixed colour for the text, each with its ratios — never
// a token to declare in router.colors, which white and black are not. Text the engine colours, told at
// the background, the same. CONTROL: a token fill keeps its own advice (brand.on).
func TestContrastFixedSurfaceAdvice(t *testing.T) {
	palette := darkPalette(t)
	for _, c := range []struct{ source, needle, want string }{
		{`<content><box background="white"><text color="ink-30">Trà</text></box></content>`, `color=`,
			`color="ink-30" trên nền white chỉ 3.15:1 ở chế độ tối; chữ thường cần 4.5:1 (chữ lớn 3:1). Nền white là màu cố định, không đổi theo chế độ như chữ: ` +
				`viết background="paper" ở content.ui:1:15 (6.59:1 sáng, 5.06:1 tối), hoặc giữ nền trắng và viết color="black" (21.00:1 sáng, 21.00:1 tối).`},
		{`<content><box background="white"><text>Trà</text></box></content>`, `background=`,
			`chữ ink mặc định của trang trên nền white chỉ 1.59:1 ở chế độ tối; chữ thường cần 4.5:1 (chữ lớn 3:1). Nền white là màu cố định, không đổi theo chế độ như chữ: ` +
				`viết background="paper" (15.54:1 sáng, 10.04:1 tối), hoặc giữ nền trắng và viết color="black" (21.00:1 sáng, 21.00:1 tối).`},
		{`<content><box background="black"><text color="ink">Trà</text></box></content>`, `color=`,
			`color="ink" trên nền black chỉ 1.35:1 ở chế độ sáng; chữ thường cần 4.5:1 (chữ lớn 3:1). Nền black là màu cố định, không đổi theo chế độ như chữ: ` +
				`viết background="paper" ở content.ui:1:15 (15.54:1 sáng, 10.04:1 tối), hoặc giữ nền đen và viết color="white" (21.00:1 sáng, 21.00:1 tối).`},
	} {
		got := contrastCompile(t, palette, content(c.source))
		if want := where("content.ui", c.source, c.needle) + c.want; got != want {
			t.Errorf("%s\n got  %s\n want %s", c.source, got, want)
		}
		if strings.Contains(got, "router.colors") {
			t.Errorf("%s: white and black are not tokens to declare: %s", c.source, got)
		}
	}
	contrastFails(t, palette, content(`<content><button background="brand" color="ink">Gửi</button></content>`), `color=`,
		`color="ink" trên nền brand chỉ 3.31:1 ở chế độ sáng; chữ thường cần 4.5:1 (chữ lớn 3:1). Dùng color="brand.on" (4.70:1 sáng, 5.66:1 tối).`)
}

// A frame pinned to an edge with no background of its own takes the page's (the content scrolls under
// it): its texts on the white stand on <page background="white">, and are grouped there with the
// content's; told alone, a pinned text is told to change that background, never to declare a token.
func TestContrastPinnedFrameStandsOnThePageBackground(t *testing.T) {
	files := map[string]string{
		"/site/page.ui":    `<page background="white" color="ink"><header /><content /></page>`,
		"/site/header.ui":  `<header pin="top"><text color="ink-30">Quán</text></header>`,
		"/site/content.ui": `<content><text color="ink-30">Trà</text></content>`,
	}
	warned, strict := groupedCompile(t, darkPalette(t), files)
	want := `page.ui:1:7: background="white" giữ màu trắng ở chế độ tối; chữ ở 2 chỗ trên nó khó đọc (ink-30 3.15:1; chữ thường cần 4.5:1, chữ lớn 3:1): header.ui:1:25, content.ui:1:16. ` +
		`Viết background="canvas" (6.24:1 sáng, 5.67:1 tối), hoặc giữ nền trắng và viết color="black" cho chữ trên nó (21.00:1 sáng, 21.00:1 tối). router.contrast("strict") nêu từng chỗ với gợi ý riêng.`
	if strings.Join(warned, "\n") != want {
		t.Errorf("the pinned text is grouped at the page's background:\n got  %q\n want %q", warned, want)
	}
	wantStrict := `header.ui:1:25: color="ink-30" trên nền white chỉ 3.15:1 ở chế độ tối; chữ thường cần 4.5:1 (chữ lớn 3:1). Nền white là màu cố định, không đổi theo chế độ như chữ: ` +
		`viết background="canvas" ở page.ui:1:7 (6.24:1 sáng, 5.67:1 tối), hoặc giữ nền trắng và viết color="black" (21.00:1 sáng, 21.00:1 tối).`
	if len(strict) == 0 || strict[0] != wantStrict || strings.Contains(strings.Join(strict, "\n"), "router.colors") {
		t.Errorf("strict: the pinned text, told alone:\n got  %q\n want %q", strict, wantStrict)
	}
}

// The places of a group: the first four, a place in the file of the one before it as line:col, then
// how many more.
func TestContrastGroupPlaces(t *testing.T) {
	var places []Warning
	for _, p := range []struct {
		file         string
		line, column int
	}{{"page.ui", 1, 26}, {"content.ui", 5, 12}, {"content.ui", 9, 4}, {"header.ui", 2, 3}, {"content.ui", 32, 8}, {"content.ui", 40, 1}} {
		places = append(places, Warning{File: p.file, Line: p.line, Column: p.column})
	}
	if got, want := placesText(places), "page.ui:1:26, content.ui:5:12, 9:4, header.ui:2:3 và 2 chỗ khác"; got != want {
		t.Errorf("\n got  %s\n want %s", got, want)
	}
	if got, want := placesText(places[:2]), "page.ui:1:26, content.ui:5:12"; got != want {
		t.Errorf("\n got  %s\n want %s", got, want)
	}
}

func copyFiles(files map[string]string) map[string]string {
	out := make(map[string]string, len(files))
	for name, source := range files {
		out[name] = source
	}
	return out
}
