package ui

import (
	"strings"
	"testing"
)

// sideWarnings compiles content.ui on a site whose router.language is language and returns the page
// rendered at "/" with every warning, one line each.
func sideWarnings(t *testing.T, language, source string) (string, []string) {
	t.Helper()
	s := memorySite("", content(source))
	s.Reload = true
	s.Language = language
	page, warned := compileDiagnose(t, s)
	return page.Render(Request{Path: "/"}), warned
}

// A physical side or corner and its logical twin on one element under one condition set the same
// side of a line written left to right (decision 13/10/2026): the page renders both classes, the twin,
// later in the fixed order, wins whichever is written first, and while developing the author hears it
// at the physical one, with where the twin is: keep one. CONTROL: another condition (a width, another
// state), or two different sides, is no twin and warns of nothing.
func TestPhysicalAndLogicalTwinsWarn(t *testing.T) {
	for _, c := range []struct{ source, says string }{
		{"<content>\n  <box padding-start=\"6\" padding-left=\"4\"><text>x</text></box>\n</content>",
			`content.ui:2:26: padding-left="4" và padding-start="6" (content.ui:2:8) cùng là cạnh trái khi dòng viết từ trái sang phải: padding-start thắng vì đứng sau padding-left trong thứ tự cố định, viết trước hay sau cũng vậy. Giữ một: padding-start lật theo hướng viết, padding-left thì không`},
		{"<content>\n  <box padding-left=\"4\" padding-start=\"6\"><text>x</text></box>\n</content>",
			`content.ui:2:8: padding-left="4" và padding-start="6" (content.ui:2:25) cùng là cạnh trái khi dòng viết từ trái sang phải: padding-start thắng`},
		{"<content>\n  <box radius-top-start=\"small\" radius-top-left=\"large\"><text>x</text></box>\n</content>",
			`content.ui:2:33: radius-top-left="large" và radius-top-start="small" (content.ui:2:8) cùng là góc trên bên trái khi dòng viết từ trái sang phải: radius-top-start thắng`},
		{"<content>\n  <box radius-bottom-right=\"none\" radius-bottom-end=\"full\"><text>x</text></box>\n</content>",
			`content.ui:2:8: radius-bottom-right="none" và radius-bottom-end="full" (content.ui:2:35) cùng là góc dưới bên phải`},
		{"<content>\n  <box radius-end=\"none\" radius-right=\"full\"><text>x</text></box>\n</content>",
			`content.ui:2:26: radius-right="full" và radius-end="none" (content.ui:2:8) cùng là hai góc bên phải`},
		{"<content>\n  <box medium:padding-end=\"2\" medium:padding-right=\"4\"><text>x</text></box>\n</content>",
			`content.ui:2:31: medium:padding-right="4" và medium:padding-end="2" (content.ui:2:8) cùng là cạnh phải`},
		{"<content>\n  <box hover,focus:radius-left=\"large\" hover:radius-start=\"none\"><text>x</text></box>\n</content>",
			`content.ui:2:8: hover,focus:radius-left="large" và hover:radius-start="none" (content.ui:2:40) cùng là hai góc bên trái`},
	} {
		html, warned := sideWarnings(t, "", c.source)
		if len(warned) != 1 || warned[0] != c.says && !strings.HasPrefix(warned[0], c.says) {
			t.Errorf("%s\nwant one warning\n %s\ngot\n %s", c.source, c.says, strings.Join(warned, "\n "))
		}
		if strings.Count(html, "padding-")+strings.Count(html, "radius-") != 2 {
			t.Errorf("both classes render as written:\n%s", html)
		}
	}
	// CONTROL
	for _, source := range []string{
		`<content><box padding-left="4" medium:padding-start="6"><text>x</text></box></content>`,
		`<content><box padding-left="4" padding-end="6"><text>x</text></box></content>`,
		`<content><box padding-right="4" padding-start="6"><text>x</text></box></content>`,
		`<content><box hover:padding-left="4" focus:padding-start="6"><text>x</text></box></content>`,
		`<content><box radius-top-left="large" radius-top-end="none"><text>x</text></box></content>`,
		`<content><box radius-left="large" radius-top-start="none" radius="small" padding-x="2" padding-left="4"><text>x</text></box></content>`,
		`<content><box padding-left="4"><text>x</text></box><box padding-start="6"><text>y</text></box></content>`,
	} {
		if _, warned := sideWarnings(t, "", source); len(warned) != 0 {
			t.Errorf("CONTROL %s\nwarns of nothing, got\n %s", source, strings.Join(warned, "\n "))
		}
	}
}

// On a site whose router.language is written right to left (ar, he, fa, ur and their regional forms),
// left is the end of the line and right its start, and neither flips: a physical attribute is told,
// while developing, the twin that keeps its place and flips, and the one for the start or end of the
// line. Its twins are the other way round there: padding-left goes with padding-end. CONTROL: a site
// written left to right (vi, en, none written, a language whose code only starts like ar) hears
// nothing of left and right, and start and end warn nowhere.
func TestRightToLeftSiteWarnsOfLeftAndRight(t *testing.T) {
	source := "<content>\n  <box padding-left=\"4\" radius-top-right=\"large\" medium:radius-bottom-left=\"none\"><text>x</text></box>\n</content>"
	for _, language := range []string{"ar", "ar-EG", "AR", "he", "he_IL", "fa-IR", "ur"} {
		html, warned := sideWarnings(t, language, source)
		quoted := `(router.language "` + language + `")`
		want := []string{
			`content.ui:2:8: padding-left="4": trang viết từ phải sang trái ` + quoted + ` mà left và right không lật theo hướng viết, nên cạnh trái ở đây là phía cuối dòng. Viết padding-end để giữ đúng chỗ này mà vẫn lật theo hướng viết (padding-start nếu ý là phía đầu dòng)`,
			`content.ui:2:25: radius-top-right="large": trang viết từ phải sang trái ` + quoted + ` mà left và right không lật theo hướng viết, nên góc trên bên phải ở đây là phía đầu dòng. Viết radius-top-start để giữ đúng chỗ này mà vẫn lật theo hướng viết (radius-top-end nếu ý là phía cuối dòng)`,
			`content.ui:2:50: medium:radius-bottom-left="none": trang viết từ phải sang trái ` + quoted + ` mà left và right không lật theo hướng viết, nên góc dưới bên trái ở đây là phía cuối dòng. Viết radius-bottom-end`,
		}
		if len(warned) != len(want) {
			t.Errorf("%s: want %d warnings, got\n %s", language, len(want), strings.Join(warned, "\n "))
			continue
		}
		for i := range want {
			if !strings.HasPrefix(warned[i], want[i]) {
				t.Errorf("%s: warning %d\n got  %s\n want %s…", language, i, warned[i], want[i])
			}
		}
		if !strings.Contains(html, `class="flex-column padding-left-4 radius-top-right-large medium:radius-bottom-left-none"`) {
			t.Errorf("%s: the page renders as written:\n%s", language, html)
		}
	}
	// right to left, padding-left's twin is padding-end: both set the left side
	_, warned := sideWarnings(t, "ar", `<content><box padding-end="2" padding-left="4" padding-start="6"><text>x</text></box></content>`)
	if len(warned) != 2 || !strings.Contains(warned[0], "phía cuối dòng") ||
		!strings.HasPrefix(warned[1], `content.ui:1:31: padding-left="4" và padding-end="2" (content.ui:1:15) cùng là cạnh trái khi dòng viết từ phải sang trái (router.language "ar"): padding-end thắng`) {
		t.Errorf("right to left twins:\n %s", strings.Join(warned, "\n "))
	}
	// CONTROL
	for _, language := range []string{"", "vi", "en", "en-US", "arn", "hebrew"} {
		if _, warned := sideWarnings(t, language, source); len(warned) != 0 {
			t.Errorf("CONTROL %q is written left to right, got\n %s", language, strings.Join(warned, "\n "))
		}
	}
	if _, warned := sideWarnings(t, "ar", `<content><box padding-start="4" radius-top-end="large" radius-bottom="none"><text>x</text></box></content>`); len(warned) != 0 {
		t.Errorf("CONTROL start, end, top and bottom warn nowhere, got\n %s", strings.Join(warned, "\n "))
	}
}

// Words for a side or a corner from elsewhere reach the author at the attribute, as errors with the
// .ui word (decision 13/10/2026), a Tailwind class written as an attribute among them. CONTROL: the
// .ui words compile without a warning.
func TestSidesFromElsewhereReachTheAuthor(t *testing.T) {
	for _, c := range []struct{ source, at, says string }{
		{`<content><box rounded-t-lg>x</box></content>`, "content.ui:1:15", `rounded-t-lg là chữ của Tailwind; .ui viết thuộc tính: radius-top="medium"`},
		{`<content><box border-top-left-radius="8px">x</box></content>`, "content.ui:1:15", "góc trên bên trái là radius-top-left"},
		{`<content><box radius-left-top="large">x</box></content>`, "content.ui:1:15", "radius-top-left"},
		{`<content><box pr="4">x</box></content>`, "content.ui:1:15", "padding-right"},
		{`<content><box pl-4>x</box></content>`, "content.ui:1:15", `padding-left="4"`},
	} {
		mustFail(t, content(c.source), c.at, c.says)
	}
	compileQuiet(t, content(`<content><box radius-top="medium" radius-top-left="large" padding-left="4" padding-right="2"><text>x</text></box></content>`))
}
