package ui

import (
	"strings"
	"testing"
)

// The stylesheet's kinds on a page (decision D, 12/10/2026; docs/KITWORK_UI_DIAGNOSTICS.md): a value
// the table draws as written, or a .ui word that does nothing where it is written, compiles, renders
// and warns once at the attribute (classes.go, class); a word the table does not understand stops the
// compile at the attribute.

// stylesheetDiagnose compiles a site that must compile and returns "/" rendered with no data, the
// page's own stylesheet and its warnings, one file:line:col: message a line.
func stylesheetDiagnose(t *testing.T, files map[string]string, scope map[string]any) (html, css string, warned []string) {
	t.Helper()
	s := memorySite("", files)
	s.Reload = true
	page, err := s.Compile("/site")
	if err != nil {
		t.Fatalf("the page must compile, got the error:\n%v", err)
	}
	for _, w := range page.Warnings() {
		warned = append(warned, w.String())
	}
	return page.Render(Request{Path: "/", Scope: scope}), string(page.Stylesheet().CSS), warned
}

// stylesheetSameLines fails unless got holds exactly the lines want, in order.
func stylesheetSameLines(t *testing.T, what string, got, want []string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("%s:\n got\n  %s\n want\n  %s", what, strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

// Numbers and lengths off their range or scale are drawn exactly as written, and each is told once at
// its attribute; a state written twice holds once. CONTROL: the same page with the values on the scale
// compiles with no warning.
func TestStylesheetDiagnosticsDrawAsWritten(t *testing.T) {
	content := "<content>\n" +
		`  <grid columns="13" gap="17px"><box span="13" padding="13"><text lines="11">a</text></box></grid>` + "\n" +
		`  <box hover,hover:gap="2" rotate="400" scale="300" width="13" padding-x="2vh"><text>b</text></box>` + "\n" +
		"</content>"
	html, css, warned := stylesheetDiagnose(t, map[string]string{"/site/content.ui": content}, nil)
	mustContain(t, html,
		`class="grid columns-13 gap-17px"`,
		`span-13 padding-13`,
		`class="lines-11"`,
		`hover:gap-2 rotate-400 scale-300 width-13 padding-x-2vh"`)
	mustNotContain(t, html, "hover,hover")
	mustContain(t, css,
		`.columns-13{grid-template-columns:repeat(13,minmax(0,1fr))}`,
		`.gap-17px{gap:17px}`,
		`.span-13{grid-column:span 13/span 13}`,
		`.padding-13{padding:3.25rem}`,
		`-webkit-line-clamp:11;`,
		`.rotate-400{rotate:400deg}`,
		`.scale-300{scale:3}`,
		`.width-13{inline-size:3.25rem}`,
		`.padding-x-2vh{padding-inline:2vh}`,
		`.hover\:gap-2:where(:hover){gap:.5rem}`)
	stylesheetSameLines(t, "one warning per value, at its attribute", warned, []string{
		`content.ui:2:9: columns là số cột, số nguyên từ 1 đến 12; "13" ở ngoài khoảng đó`,
		`content.ui:2:22: gap="17px" không có trong thang khoảng cách; gần nhất là gap="4" (1rem). Thang: ` + spacingScaleForTest,
		`content.ui:2:38: span là số cột thẻ trải qua, số nguyên từ 1 đến 12; "13" ở ngoài khoảng đó`,
		`content.ui:2:48: padding="13" không có trong thang khoảng cách; gần nhất là padding="12" (3rem). Thang: ` + spacingScaleForTest,
		`content.ui:2:67: lines là số dòng tối đa, số nguyên từ 1 đến 10; "11" ở ngoài khoảng đó`,
		`content.ui:3:8: trạng thái hover: viết hai lần trong hover,hover:`,
		`content.ui:3:28: rotate là số độ quay, số nguyên từ 0 đến 360; "400" ở ngoài khoảng đó`,
		`content.ui:3:41: scale là phần trăm cỡ, số nguyên từ 0 đến 200 (95 là 95%); "300" ở ngoài khoảng đó`,
		`content.ui:3:53: width="13" không có trong thang khoảng cách; gần nhất là width="12" (3rem). Thang: ` + spacingScaleForTest,
		`content.ui:3:64: padding-x="2vh": khoảng cách theo thang, không theo vh: ` + spacingScaleForTest,
	})
	// CONTROL: on the scale, nothing to tell
	onScale := strings.NewReplacer(`"13"`, `"12"`, `"17px"`, `"4"`, `"11"`, `"10"`, `hover,hover:`, `hover:`, `"400"`, `"270"`,
		`"300"`, `"150"`, `"2vh"`, `"2"`).Replace(content)
	if _, _, warned := stylesheetDiagnose(t, map[string]string{"/site/content.ui": onScale}, nil); len(warned) != 0 {
		t.Errorf("CONTROL: values on the scale warn of nothing, got %q", warned)
	}

	// a step of the scale written as a length is told it is that step, not that it is off the scale
	// (review 12/10/2026): drawn as written
	step := `<content><box gap="1.5rem" padding="24px"><text>c</text></box></content>`
	html, css, warned = stylesheetDiagnose(t, map[string]string{"/site/content.ui": step}, nil)
	mustContain(t, html, `class="flex-column gap-1.5rem padding-24px"`)
	mustContain(t, css, `.gap-1\.5rem{gap:1.5rem}`, `.padding-24px{padding:24px}`)
	stylesheetSameLines(t, "a step written as a length", warned, []string{
		`content.ui:1:15: gap="1.5rem" là bậc 6 của thang khoảng cách (1.5rem): viết gap="6"`,
		`content.ui:1:28: padding="24px" là bậc 6 của thang khoảng cách (1.5rem): viết padding="6"`,
	})
}

// spacingScaleForTest is the spacing scale as a message names it.
const spacingScaleForTest = "0, 0.5, 1, 1.5, 2, 2.5, 3, 3.5, 4, 5, 6, 7, 8, 9, 10, 11, 12, 14, 16, 20, 24, 28, 32, 36, 40, 44, 48, 52, 56, 60, 64, 72, 80, 96 (bội của 0.25rem: 4 là 1rem)"

// A .ui word that does nothing where it is written compiles, draws nothing and warns once: wrap="balance"
// on a frame, fit on a box, align="top" on a text, width="auto" (the default written out). CONTROL: each
// word where it acts is a class with its rule and no warning.
func TestStylesheetDiagnosticsWordsThatDoNothingHere(t *testing.T) {
	content := "<content>\n" +
		`  <box wrap="balance" fit="cover"><text align="top" width="auto">a</text></box>` + "\n" +
		"</content>"
	html, css, warned := stylesheetDiagnose(t, map[string]string{"/site/content.ui": content}, nil)
	mustNotContain(t, html, "wrap-", "fit-", "align-", "width-")
	mustNotContain(t, css, "wrap-", "fit-", "align-", "width-", "object-fit", "text-wrap", "inline-size")
	stylesheetSameLines(t, "one warning per word, at its attribute", warned, []string{
		`content.ui:2:8: wrap="balance" chỉ có nghĩa trên chữ (<text>); trên khung, wrap nhận true hoặc false`,
		`content.ui:2:23: fit chỉ đặt trên image (ảnh lấp khung thế nào), không đặt trên box`,
		`content.ui:2:41: align="top" là vị trí các con của một khung, trên chữ không có tác dụng: align của text nhận start, center hoặc end`,
		`content.ui:2:53: width="auto" là mặc định: không viết width là theo nội dung. width nhận fit (ôm nội dung) hoặc full (hết bề ngang của khung cha); hoặc một chữ của thang độ rộng (small (640px), medium (768px), large (1024px), wide (1280px) hoặc huge (1536px)), nhận một số của thang (` + spacingScaleForTest + `) hoặc một số có đơn vị px, rem, em, %, ch, vw, vh, dvh (640px, 50%, 68ch)`,
	})
	// CONTROL: where each word acts
	control := "<content>\n" +
		`  <text wrap="balance" align="center">a</text><image value="/a.png" alt="" fit="cover" /><box align="top" width="fit"><text>b</text></box>` + "\n" +
		"</content>"
	html, css, warned = stylesheetDiagnose(t, map[string]string{"/site/content.ui": control}, nil)
	if len(warned) != 0 {
		t.Errorf("CONTROL: no warning where the words act, got %q", warned)
	}
	mustContain(t, html, "wrap-balance", "fit-cover", "align-top", "width-fit")
	mustContain(t, css, "text-wrap:balance", "object-fit:cover", ".width-fit{inline-size:fit-content}")
}

// A word the table does not understand stops the compile at the attribute, and so does a number the
// engine cannot draw: the value is read before the place is judged (fit="cove" on a box is a typo), and
// the web's and Tailwind's words keep their error with the .ui word (decision D, question 2).
// CONTROL: the same attribute with a word the table reads compiles.
func TestStylesheetDiagnosticsTyposStayErrors(t *testing.T) {
	cases := []struct {
		attribute, says, control string
	}{
		{`fit="cove"`, `fit nhận cover (lấp đầy khung, cắt phần thừa) hoặc contain (thấy trọn ảnh), không có "cove"`, `fit="cover"`},
		{`gap="big"`, `gap theo thang khoảng cách, không có "big"`, `gap="13"`},
		{`columns="0"`, `columns là số cột, số nguyên từ 1 đến 12, không có "0"`, `columns="13"`},
		{`opacity="150"`, `opacity là độ hiện của cả phần tử, số nguyên từ 0 đến 100, không có "150"`, `opacity="100"`},
		{`rotate="45.5"`, `rotate là số độ quay, số nguyên từ 0 đến 360, không có "45.5"`, `rotate="-45"`},
		{`medium:width="auto"`, `không có "auto"`, `medium:width="fit"`},
		{`radius="xl"`, `radius="xl" là chữ cũ của Tailwind: viết radius="large"`, `radius="large"`},
		{`container="full"`, `full là chữ của Tailwind, không phải của .ui`, `container="large"`},
		{`hover,hoverr:gap="2"`, `tiền tố không tồn tại: hoverr:`, `hover,hover:gap="2"`},
	}
	for _, c := range cases {
		source := `<content><box ` + c.attribute + `><text>a</text></box></content>`
		mustFail(t, map[string]string{"/site/content.ui": source}, "content.ui:1:15:", c.says)
		control := `<content><box ` + c.control + `><text>a</text></box></content>`
		stylesheetDiagnose(t, map[string]string{"/site/content.ui": control}, nil)
	}
}

// A value from data: what its expression spells out is read like a written value (13 is drawn and
// told; balance on a frame does nothing and is told), and fit from data on a box, where no value of fit
// does anything, is told once and makes no class, at render too: no second word from the renderer.
// CONTROL: the other arm of the wrap is its class, and on an image the same fit from data is its class.
func TestStylesheetDiagnosticsFromData(t *testing.T) {
	files := map[string]string{"/site/content.ui": `<content><box gap="{{ wide ? '13' : '4' }}" fit="{{ f }}" wrap="{{ wide ? 'balance' : 'true' }}"><text>a</text></box></content>`}
	s := memorySite("", files)
	s.Reload = true
	page, err := s.Compile("/site")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	var warned []string
	for _, w := range page.Warnings() {
		warned = append(warned, w.String())
	}
	stylesheetSameLines(t, "the spelled-out value and the fit from data, each once", warned, []string{
		`content.ui:1:15: gap="13" không có trong thang khoảng cách; gần nhất là gap="12" (3rem). Thang: ` + spacingScaleForTest,
		`content.ui:1:45: fit chỉ đặt trên image (ảnh lấp khung thế nào), không đặt trên box`,
		`content.ui:1:59: wrap="balance" chỉ có nghĩa trên chữ (<text>); trên khung, wrap nhận true hoặc false`,
	})
	var rendered []string
	html := page.Render(Request{Path: "/", Scope: map[string]any{"wide": true, "f": "cover"}, Warn: func(m string) { rendered = append(rendered, m) }})
	mustContain(t, html, `class="flex-column gap-13"`)
	mustNotContain(t, html, "fit-", "wrap-")
	mustContain(t, string(page.Stylesheet().CSS), `.gap-13{gap:3.25rem}`)
	if len(rendered) != 0 {
		t.Errorf("what does nothing here was told when the page compiled: the renderer says nothing more, got %q", rendered)
	}
	// CONTROL: the other arm of the wrap is its class
	mustContain(t, page.Render(Request{Path: "/", Scope: map[string]any{"wide": false, "f": "cover"}}), `class="flex-column gap-4 wrap-true"`)
	// CONTROL: on an image, fit from data is its class
	html, _, warned = stylesheetDiagnose(t, map[string]string{"/site/content.ui": `<content><image value="/a.png" alt="" fit="{{ f }}" /></content>`},
		map[string]any{"f": "contain"})
	mustContain(t, html, "fit-contain")
	if len(warned) != 0 {
		t.Errorf("CONTROL: fit from data on an image warns of nothing, got %q", warned)
	}
}
