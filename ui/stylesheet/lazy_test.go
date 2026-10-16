package stylesheet

import (
	"strings"
	"testing"
)

// lazy (15/10/2026, KITWORK_UI §7 "Bỏ qua phần ở xa màn hình: lazy") is CSS's content-visibility:auto
// with an estimated height: true keeps 500px for a block the browser has not drawn, a height written
// keeps that height, false draws it as always (to undo lazy from a width on). The estimate is the height
// only, with auto: once drawn, the element keeps the size it last had; its width before that is 0
// where it comes from what it holds, never an estimate that would overflow a phone. Print draws
// everything, under the class's own condition. A width prefix moves it, as every presentation
// attribute.
func TestLazyClassesAndRules(t *testing.T) {
	table := NewTable(nil)
	var r Rules
	for _, c := range []struct{ in Intent }{
		{Intent{Name: "lazy", Value: "true"}},
		{Intent{Name: "lazy", Value: "2860px"}},
		{Intent{Width: "medium", Name: "lazy", Value: "1420px"}},
		{Intent{Width: "large", Name: "lazy", Value: "false"}},
		{Intent{Name: "lazy", Value: "25rem"}},
		{Intent{Name: "lazy", Value: "100vh"}},
	} {
		class, warnings, err := table.Read(&r, c.in)
		if err != nil || len(warnings) > 0 {
			t.Fatalf("%+v: %v %v", c.in, err, warnings)
		}
		want := c.in.Name + "-" + c.in.Value
		if c.in.Width != "" {
			want = c.in.Width + ":" + want
		}
		if class != want {
			t.Errorf("%+v: class %q, want %q", c.in, class, want)
		}
	}
	css := string(r.CSS())
	for _, line := range []string{
		".lazy-true{content-visibility:auto;contain-intrinsic-inline-size:auto none;contain-intrinsic-block-size:auto 500px}\n" +
			"@media print{.lazy-true{content-visibility:visible}}\n",
		".lazy-2860px{content-visibility:auto;contain-intrinsic-inline-size:auto none;contain-intrinsic-block-size:auto 2860px}\n" +
			"@media print{.lazy-2860px{content-visibility:visible}}\n",
		".lazy-25rem{content-visibility:auto;contain-intrinsic-inline-size:auto none;contain-intrinsic-block-size:auto 25rem}\n",
		".lazy-100vh{content-visibility:auto;contain-intrinsic-inline-size:auto none;contain-intrinsic-block-size:auto 100vh}\n",
		// under a width, the print rule holds under the same width
		"@media (min-width:768px){\n" +
			`.medium\:lazy-1420px{content-visibility:auto;contain-intrinsic-inline-size:auto none;contain-intrinsic-block-size:auto 1420px}` + "\n" +
			`@media print{.medium\:lazy-1420px{content-visibility:visible}}` + "\n}\n",
		// false draws as always: no skipping, no estimate, nothing to undo in print
		"@media (min-width:1024px){\n" + `.large\:lazy-false{content-visibility:visible;contain-intrinsic-size:none}` + "\n}\n",
	} {
		if !strings.Contains(css, line) {
			t.Errorf("page.css lacks\n%s\n---\n%s", line, css)
		}
	}
	if strings.Count(css, "@media print") != 5 {
		t.Errorf("one print rule per class that skips (5), got %d:\n%s", strings.Count(css, "@media print"), css)
	}
	// every estimate is the height alone: no class writes a width to keep
	if strings.Contains(css, "contain-intrinsic-size:auto") || strings.Contains(css, "contain-intrinsic-width") {
		t.Errorf("an estimate of the width:\n%s", css)
	}
	// from data: true or false
	domain, err := table.Domain(nil, Intent{Name: "lazy"})
	if err != nil || len(domain) != 2 || domain["true"] != "lazy-true" || domain["false"] != "lazy-false" {
		t.Errorf("lazy from data: %v %v", domain, err)
	}
}

// What lazy refuses, in the author's language, at the attribute: a state (it is no look of a moment), a
// number without its unit (told the px it meant), a part of the parent (%), 0, CSS's own words (auto
// is true, visible is false) and any other word. CSS's property is told the .ui word. CONTROL: true,
// false and a height read without a word.
func TestLazyRefusals(t *testing.T) {
	table := NewTable(nil)
	for _, c := range []struct {
		in         Intent
		says       string
		suggestion string
	}{
		{Intent{State: "hover", Name: "lazy", Value: "true"}, "lazy không đổi theo trạng thái", ""},
		{Intent{State: "scrolled", Name: "lazy", Value: "400px"}, "lazy không đổi theo trạng thái", ""},
		{Intent{Name: "lazy", Value: "400"}, `lazy="400": chiều cao ước lượng cần đơn vị: lazy="400px"`, "400px"},
		{Intent{Name: "lazy", Value: "50%"}, "chiều cao ước lượng là của chính phần tử, không theo khung cha", ""},
		{Intent{Name: "lazy", Value: "0px"}, "chiều cao ước lượng phải lớn hơn 0", ""},
		{Intent{Name: "lazy", Value: "auto"}, `bỏ qua phần ở xa màn hình là lazy="true"`, "true"},
		{Intent{Name: "lazy", Value: "visible"}, `luôn vẽ là lazy="false"`, "false"},
		{Intent{Name: "lazy", Value: "yes"}, `lazy nhận true, false hoặc chiều cao ước lượng có đơn vị (px, rem, em, vw, vh, dvh: 400px, 25rem), không có "yes"`, ""},
		{Intent{Name: "lazy", Value: "40ch"}, `không có "40ch"`, ""},
		{Intent{Name: "lazy", Value: "calc(100vh - 2rem)"}, `không có "calc(100vh - 2rem)"`, ""},
		{Intent{Name: "content-visibility", Value: "auto"}, "bỏ qua phần ở xa màn hình là lazy", "lazy"},
	} {
		_, err := table.Class(nil, c.in)
		p, ok := err.(*Problem)
		if !ok || p.Warning || !strings.Contains(p.Message, c.says) || p.Suggestion != c.suggestion {
			t.Errorf("%+v:\n got  %v (suggestion %q)\n want %q (suggestion %q)", c.in, err, suggestionOf(p), c.says, c.suggestion)
		}
	}
	for _, value := range []string{"true", "false", "400px", "1.5em", "80dvh", "150vw"} {
		if _, err := table.Class(nil, Intent{Name: "lazy", Value: value}); err != nil {
			t.Errorf("CONTROL lazy=%q: %v", value, err)
		}
	}
}
