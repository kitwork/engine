package ui

import (
	"strings"
	"testing"
)

// sideCompile compiles content.ui on a site whose router.language is language and returns the page
// with every warning, one line each.
func sideCompile(t *testing.T, language, source string) (*Page, []string) {
	t.Helper()
	s := memorySite("", content(source))
	s.Reload = true
	s.Language = language
	return compileDiagnose(t, s)
}

// start and end, the words of the line, were replaced by left and right everywhere (Quốc, 19/10/2026),
// and left and right mirror on a page written right to left. A side or a corner named the old way
// (padding-start, radius-top-end) is an error at the attribute naming the attribute of today, the
// prefix kept (Có tiền tố thì viết medium:padding-right); a corner written the other way round in the
// old words besides (radius-start-top) is told both fixes in one; a value with start or end (place of a
// part, align of a box or of a text, under a prefix too) is told its left or right word. Each error is
// whole. The old word beside its new twin (radius-top-start with radius-top-left), which warned until
// then that the two set one corner, is the old word's error: a side can no longer be written two ways.
// CONTROL: the words of today, together and under any condition, compile without a warning.
func TestStartAndEndAreNowLeftAndRight(t *testing.T) {
	const flips = " (tự lật khi trang viết từ phải sang trái)"
	for _, c := range []struct{ source, needle, says string }{
		{`<content><box padding-start="4"><text>x</text></box></content>`, "padding-start=", "padding-start nay là padding-left: start và end nay là left và right" + flips},
		{`<content><box padding-end="4"><text>x</text></box></content>`, "padding-end=", "padding-end nay là padding-right: start và end nay là left và right" + flips},
		{`<content><box radius-start="large"><text>x</text></box></content>`, "radius-start=", "radius-start nay là radius-left: start và end nay là left và right" + flips},
		{`<content><box radius-end="large"><text>x</text></box></content>`, "radius-end=", "radius-end nay là radius-right: start và end nay là left và right" + flips},
		{`<content><box radius-top-start="large"><text>x</text></box></content>`, "radius-top-start=", "radius-top-start nay là radius-top-left: start và end nay là left và right" + flips},
		{`<content><box radius-top-end="large"><text>x</text></box></content>`, "radius-top-end=", "radius-top-end nay là radius-top-right: start và end nay là left và right" + flips},
		{`<content><box radius-bottom-start="large"><text>x</text></box></content>`, "radius-bottom-start=", "radius-bottom-start nay là radius-bottom-left: start và end nay là left và right" + flips},
		{`<content><box radius-bottom-end="large"><text>x</text></box></content>`, "radius-bottom-end=", "radius-bottom-end nay là radius-bottom-right: start và end nay là left và right" + flips},
		{`<content><box medium:padding-end="2"><text>x</text></box></content>`, "medium:padding-end=",
			"medium:padding-end nay là padding-right: start và end nay là left và right" + flips + ". Có tiền tố thì viết medium:padding-right"},
		{`<content><box hover,focus:radius-start="none"><text>x</text></box></content>`, "hover,focus:radius-start=",
			"hover,focus:radius-start nay là radius-left: start và end nay là left và right" + flips + ". Có tiền tố thì viết hover,focus:radius-left"},
		{`<content><box radius-start-top="large"><text>x</text></box></content>`, "radius-start-top=",
			`radius-start-top="large": góc viết cạnh trên hay dưới trước rồi mới đến left hay right; start và end nay là left và right: radius-top-left` + flips},
		{`<content><box radius-end-bottom="large">x</box></content>`, "radius-end-bottom=",
			`radius-end-bottom="large": góc viết cạnh trên hay dưới trước rồi mới đến left hay right; start và end nay là left và right: radius-bottom-right` + flips},
		// the twin of before: the old word is the error, wherever it stands
		{`<content><box radius-top-left="large" radius-top-start="none"><text>x</text></box></content>`, "radius-top-start=",
			"radius-top-start nay là radius-top-left: start và end nay là left và right" + flips},
		{`<content><box padding-start="4" padding-left="2"><text>x</text></box></content>`, "padding-start=",
			"padding-start nay là padding-left: start và end nay là left và right" + flips},
		// values
		{`<content><box><box place="start"><text>x</text></box></box></content>`, "place=", `place="start": start và end nay là left và right, viết place="left"` + flips},
		{`<content><box><box place="end"><text>x</text></box></box></content>`, "place=", `place="end": start và end nay là left và right, viết place="right"` + flips},
		{`<content><box><box place="top-start"><text>x</text></box></box></content>`, "place=", `place="top-start": start và end nay là left và right, viết place="top-left"` + flips},
		{`<content><box><box place="below-end"><text>x</text></box></box></content>`, "place=", `place="below-end": start và end nay là left và right, viết place="below-right"` + flips},
		{`<content><box><box place="above-start"><text>x</text></box></box></content>`, "place=", `place="above-start": start và end nay là left và right, viết place="above-left"` + flips},
		{`<content><box><box medium:place="top-end"><text>x</text></box></box></content>`, "medium:place=", `place="top-end": start và end nay là left và right, viết place="top-right"` + flips},
		{`<content><box align="start"><text>x</text></box></content>`, "align=", `align="start": start và end nay là left và right, viết align="left"` + flips},
		{`<content><box align="top-end"><text>x</text></box></content>`, "align=", `align="top-end": start và end nay là left và right, viết align="top-right"` + flips},
		{`<content><box align="bottom-start"><text>x</text></box></content>`, "align=", `align="bottom-start": start và end nay là left và right, viết align="bottom-left"` + flips},
		{`<content><box medium:align="start"><text>x</text></box></content>`, "medium:align=", `align="start": start và end nay là left và right, viết align="left"` + flips},
		{`<content><text align="end">x</text></content>`, "align=", `align="end": start và end nay là left và right, viết align="right"` + flips},
		{`<content><text align="start">x</text></content>`, "align=", `align="start": start và end nay là left và right, viết align="left"` + flips},
		{`<content><text hover:align="end">x</text></content>`, "hover:align=", `align="end": start và end nay là left và right, viết align="right"` + flips},
	} {
		mustFailExactly(t, content(c.source), where("content.ui", c.source, c.needle)+c.says)
	}
	// CONTROL
	compileQuiet(t, content(`<content><box padding-left="4" medium:padding-left="6" hover:padding-right="2" focus:padding-left="1" `+
		`radius-left="large" radius-top-left="none" medium:radius-bottom-right="full" padding-x="2" align="bottom-left"><text align="right">x</text></box>`+
		`<box><box place="below-right"><text>y</text></box><box place="top-left"><text>z</text></box></box></content>`))
}

// On a site whose router.language is written right to left (ar, he, fa, ur and their regional forms),
// left and right mirror (Quốc, 19/10/2026): the page's stylesheet writes each side and corner as logical
// CSS — padding-left is padding-inline-start, radius-top-right border-start-end-radius, align="right" on
// a text text-align:end, align="top-left" of a box flex-start — so under the dir="rtl" the document
// carries (TestRightToLeftSiteWritesItsDirection) padding-left pads the right side, the side the line
// starts on. Until then left and right were the screen's, and a site written right to left was warned
// of every one of them (checkSides, removed): the page now compiles with no warning, the classes as
// written, and the stylesheet is the very one of a site written left to right, with no rule of its own
// for the direction: the mirroring is the browser's, by dir alone. CONTROL: the same page on a site
// written left to right (none written, vi, en, en-US, and arn and hebrew, which only start like ar and
// he) has the same classes and the same stylesheet and no dir; neither writes a physical side or corner
// (padding-left:, border-top-left-radius, text-align:left).
func TestLeftAndRightMirrorRightToLeft(t *testing.T) {
	source := `<content><box padding-left="4" padding-right="2" medium:padding-left="6" radius-top-left="large" radius-top-right="small" ` +
		`radius-bottom-left="none" radius-bottom-right="full" radius-left="medium" radius-right="large" align="top-left">` +
		`<text align="right">x</text><text align="left">y</text></box></content>`
	classes := []string{
		`<div class="flex-column padding-left-4 padding-right-2 medium:padding-left-6 radius-top-left-large radius-top-right-small radius-bottom-left-none radius-bottom-right-full radius-left-medium radius-right-large align-top-left">`,
		`<p class="align-right">x</p><p class="align-left">y</p>`,
	}
	rules := []string{
		`.padding-left-4{padding-inline-start:1rem}`,
		`.padding-right-2{padding-inline-end:.5rem}`,
		`.medium\:padding-left-6{padding-inline-start:1.5rem}`,
		`.radius-top-left-large{border-start-start-radius:.75rem}`,
		`.radius-top-right-small{border-start-end-radius:.375rem}`,
		`.radius-bottom-left-none{border-end-start-radius:0}`,
		`.radius-bottom-right-full{border-end-end-radius:999px}`,
		`.radius-left-medium{border-start-start-radius:.5rem;border-end-start-radius:.5rem}`,
		`.radius-right-large{border-start-end-radius:.75rem;border-end-end-radius:.75rem}`,
		`.align-top-left{--kitwork-vertical:flex-start;--kitwork-horizontal:flex-start}`,
		`.align-left:where(:not(.flex-row,.flex-column,.grid)){text-align:start}`,
		`.align-right:where(:not(.flex-row,.flex-column,.grid)){text-align:end}`,
	}
	physical := []string{"padding-left:", "padding-right:", "border-top-left-radius", "border-top-right-radius",
		"border-bottom-left-radius", "border-bottom-right-radius", "text-align:left", "text-align:right", ":dir("}
	check := func(language string, rightToLeft bool) string {
		t.Helper()
		page, warned := sideCompile(t, language, source)
		if len(warned) != 0 {
			t.Errorf("%q: left and right warn of nothing, got\n %s", language, strings.Join(warned, "\n "))
		}
		mustContain(t, page.Render(Request{Path: "/"}), classes...)
		css := string(page.Stylesheet().CSS)
		lines := map[string]bool{}
		for _, line := range strings.Split(css, "\n") {
			lines[line] = true
		}
		for _, rule := range rules {
			if !lines[rule] {
				t.Errorf("%q: the stylesheet misses the line %s:\n%s", language, rule, css)
			}
		}
		mustNotContain(t, css, physical...)
		if got := strings.Contains(page.Document(Request{Path: "/"}), ` dir="rtl"`); got != rightToLeft {
			t.Errorf("%q: dir=\"rtl\" written %v, want %v", language, got, rightToLeft)
		}
		return css
	}
	leftToRight := check("vi", false)
	for _, language := range []string{"ar", "ar-EG", "AR", "he", "he_IL", "fa-IR", "ur"} {
		if css := check(language, true); css != leftToRight {
			t.Errorf("%q: the stylesheet is the one of a site written left to right:\n got  %s\n want %s", language, css, leftToRight)
		}
	}
	// CONTROL
	for _, language := range []string{"", "en", "en-US", "arn", "hebrew"} {
		if css := check(language, false); css != leftToRight {
			t.Errorf("CONTROL %q: one stylesheet for every site written left to right:\n got  %s\n want %s", language, css, leftToRight)
		}
	}
}

// Words for a side or a corner from elsewhere reach the author at the attribute, as errors with the
// .ui word (decision 13/10/2026), a Tailwind class written as an attribute among them. Tailwind's start
// and end (ps, pe, rounded-s, rounded-ee) and CSS's logical names (padding-inline-start,
// border-start-end-radius, text-align) are told .ui's left and right, which mirror the same way
// (19/10/2026). CONTROL: the .ui words compile without a warning.
func TestSidesFromElsewhereReachTheAuthor(t *testing.T) {
	for _, c := range []struct{ source, at, says string }{
		{`<content><box rounded-t-lg>x</box></content>`, "content.ui:1:15", `rounded-t-lg là chữ của Tailwind; .ui viết thuộc tính: radius-top="medium"`},
		{`<content><box border-top-left-radius="8px">x</box></content>`, "content.ui:1:15", "góc trên bên trái là radius-top-left"},
		{`<content><box radius-left-top="large">x</box></content>`, "content.ui:1:15", "radius-top-left"},
		{`<content><box pr="4">x</box></content>`, "content.ui:1:15", "padding-right"},
		{`<content><box pl-4>x</box></content>`, "content.ui:1:15", `padding-left="4"`},
		// the line's words of elsewhere, .ui's left and right
		{`<content><box ps="4">x</box></content>`, "content.ui:1:15", `ps="4": ps là chữ viết tắt của Tailwind; .ui viết đủ chữ: padding-left (theo thang khoảng cách)`},
		{`<content><box pe-2>x</box></content>`, "content.ui:1:15", `pe-2: pe-2 là lớp của Tailwind; .ui viết thuộc tính: padding-right="2"`},
		{`<content><box rounded-s-lg>x</box></content>`, "content.ui:1:15",
			`rounded-s-lg: rounded-s-lg là chữ của Tailwind; .ui viết thuộc tính: radius-left="medium" (.5rem). Các chữ: none, small, medium, large, full`},
		{`<content><box rounded-ee="xl">x</box></content>`, "content.ui:1:15",
			`rounded-ee="xl": rounded-ee là chữ của Tailwind; .ui viết thuộc tính: radius-bottom-right="large" (.75rem). Các chữ: none, small, medium, large, full`},
		{`<content><box padding-inline-start="4">x</box></content>`, "content.ui:1:15", `padding-inline-start="4": cạnh trái là padding-left (tự lật khi trang viết từ phải sang trái)`},
		{`<content><box padding-inline-end="2">x</box></content>`, "content.ui:1:15", `padding-inline-end="2": cạnh phải là padding-right (tự lật khi trang viết từ phải sang trái)`},
		{`<content><box border-start-end-radius="8px">x</box></content>`, "content.ui:1:15",
			`border-start-end-radius="8px": góc trên bên phải là radius-top-right (none, small, medium, large, full), tự lật khi trang viết từ phải sang trái`},
		{`<content><text text-align="left">x</text></content>`, "content.ui:1:16", `text-align="left": căn chữ là align (left, center, right; tự lật khi trang viết từ phải sang trái)`},
	} {
		mustFail(t, content(c.source), c.at, c.says)
	}
	compileQuiet(t, content(`<content><box radius-top="medium" radius-top-left="large" padding-left="4" padding-right="2"><text>x</text></box></content>`))
}

// A side or a corner named with start or end at a call of a layout or of a component is told its name
// of today, as on any element, the prefix kept: not taken for an input the file does not read
// (paddingStart), nor for the look the file holds.
// CONTROL: padding-left at the same calls is told that the look is the file's, as before; without the
// check at the call, padding-start was told "product-card.ui không đọc paddingStart" (run 19/10/2026).
func TestStartAndEndAtACallAreTheRenamedSide(t *testing.T) {
	const told = " nay là "
	files := func(call string) map[string]string {
		return map[string]string{
			"/site/content.ui":      "<content>" + call + "</content>",
			"/site/product-card.ui": "<card><text>card</text></card>",
			"/site/shop-cart.ui":    "<shop-cart><text>giỏ</text></shop-cart>",
			"/site/shop-cart.work":  "import { component } from \"kitwork\";\n\ncomponent.define(\"shop-cart\", () => {\n  var open = false;\n});\n",
		}
	}
	for _, c := range []struct{ call, needle, says string }{
		{`<product-card padding-start="4" />`, `padding-start=`, "padding-start" + told + "padding-left: start và end nay là left và right (tự lật khi trang viết từ phải sang trái)"},
		{`<product-card radius-top-end="large" />`, `radius-top-end=`, "radius-top-end" + told + "radius-top-right: start và end nay là left và right (tự lật khi trang viết từ phải sang trái)"},
		{`<product-card medium:padding-start="4" />`, `medium:padding-start=`, "medium:padding-start" + told + "padding-left: start và end nay là left và right (tự lật khi trang viết từ phải sang trái). Có tiền tố thì viết medium:padding-left"},
		{`<shop-cart padding-start="2" />`, `padding-start=`, "padding-start" + told + "padding-left: start và end nay là left và right (tự lật khi trang viết từ phải sang trái)"},
		{`<shop-cart medium:radius-end="large" />`, `medium:radius-end=`, "medium:radius-end" + told + "radius-right: start và end nay là left và right (tự lật khi trang viết từ phải sang trái). Có tiền tố thì viết medium:radius-right"},
	} {
		t.Run(c.call, func(t *testing.T) {
			source := "<content>" + c.call + "</content>"
			mustFailExactly(t, files(c.call), where("content.ui", source, c.needle)+c.says)
		})
	}
	for _, c := range []struct{ call, says string }{
		{`<product-card padding-left="4" />`, "trình bày của product-card nằm trong product-card.ui; chỗ gọi nhận if, show, for, span, place, source và dữ liệu file đọc"},
		{`<shop-cart padding-left="2" />`, "trình bày của shop-cart nằm trong shop-cart.ui; chỗ gọi nhận as, if, show, for, span, place và dữ liệu nó đọc"},
	} {
		t.Run("control "+c.call, func(t *testing.T) {
			source := "<content>" + c.call + "</content>"
			mustFailExactly(t, files(c.call), where("content.ui", source, "padding-left=")+c.says)
		})
	}
}
