package stylesheet

import (
	"errors"
	"strings"
	"testing"
)

// The table's kinds (decision D, 12/10/2026; docs/KITWORK_UI_DIAGNOSTICS.md): a word the table does
// not understand is an error, a value it understands and draws as written is a warning, and a .ui word
// that does nothing where it is written is a warning with no class. Read hands the warnings back beside
// the class; Class, for a caller that cannot tell the author, refuses with them.

// diagnosticCSS is the page stylesheet a set of rules writes, "" when nothing was recorded.
func diagnosticCSS(r *Rules) string { return string(r.CSS()) }

// A number or a length off its range or scale is drawn exactly as written, and the author is told
// once: the class names the value as written, its rule says it, and the warning names the value and,
// for spacing, the nearest step. A state written twice holds once, under a class that names it once.
// CONTROL: Class, which has no way to tell anyone, still refuses each one and records nothing.
func TestStylesheetWarningsDrawAsWritten(t *testing.T) {
	table := NewTable(examplePalette(t))
	cases := []struct {
		in       Intent
		class    string
		rule     string
		warnings []string
	}{
		{Intent{State: "hover,hover", Name: "gap", Value: "4"}, "hover:gap-4", `.hover\:gap-4:where(:hover){gap:1rem}`,
			[]string{"trạng thái hover: viết hai lần trong hover,hover:"}},
		{Intent{Name: "padding", Value: "13"}, "padding-13", `.padding-13{padding:3.25rem}`,
			[]string{`padding="13" không có trong thang khoảng cách; gần nhất là padding="12" (3rem)`}},
		{Intent{Name: "gap", Value: "17px"}, "gap-17px", `.gap-17px{gap:17px}`,
			[]string{`gap="17px" không có trong thang khoảng cách; gần nhất là gap="4" (1rem)`}},
		{Intent{Name: "gap", Value: "2vh"}, "gap-2vh", `.gap-2vh{gap:2vh}`,
			[]string{`gap="2vh": khoảng cách theo thang, không theo vh`}},
		{Intent{Name: "padding-x", Value: "1.5rem"}, "padding-x-1.5rem", `.padding-x-1\.5rem{padding-inline:1.5rem}`,
			[]string{`padding-x="1.5rem" là bậc 6 của thang khoảng cách (1.5rem): viết padding-x="6"`}},
		{Intent{Name: "width", Value: "13"}, "width-13", `.width-13{inline-size:3.25rem}`,
			[]string{`width="13" không có trong thang khoảng cách; gần nhất là width="12" (3rem)`}},
		{Intent{Name: "columns", Value: "13"}, "columns-13", `.columns-13{grid-template-columns:repeat(13,minmax(0,1fr))}`,
			[]string{`columns là số cột, số nguyên từ 1 đến 12; "13" ở ngoài khoảng đó`}},
		{Intent{Name: "span", Value: "13"}, "span-13", `.span-13{grid-column:span 13/span 13}`,
			[]string{`span là số cột thẻ trải qua, số nguyên từ 1 đến 12; "13" ở ngoài khoảng đó`}},
		{Intent{Name: "lines", Value: "11"}, "lines-11", `-webkit-line-clamp:11;overflow:hidden}`,
			[]string{`lines là số dòng tối đa, số nguyên từ 1 đến 10; "11" ở ngoài khoảng đó`}},
		{Intent{Name: "thickness", Value: "9"}, "thickness-9", `.thickness-9{block-size:9px}`,
			[]string{`"9" ở ngoài khoảng đó`}},
		{Intent{Name: "scale", Value: "300"}, "scale-300", `.scale-300{scale:3}`,
			[]string{`scale là phần trăm cỡ, số nguyên từ 0 đến 200 (95 là 95%); "300" ở ngoài khoảng đó`}},
		{Intent{Name: "scale", Value: "-100"}, "scale--100", `.scale--100{scale:-1}`,
			[]string{`"-100" ở ngoài khoảng đó`}},
		{Intent{Name: "rotate", Value: "400"}, "rotate-400", `.rotate-400{rotate:400deg}`,
			[]string{`rotate là số độ quay, số nguyên từ 0 đến 360; "400" ở ngoài khoảng đó`}},
		{Intent{Name: "rotate", Value: "-45"}, "rotate--45", `.rotate--45{rotate:-45deg}`,
			[]string{`"-45" ở ngoài khoảng đó`}},
	}
	for _, c := range cases {
		var r Rules
		class, warnings, err := table.Read(&r, c.in)
		if err != nil {
			t.Errorf("%+v: refused, want a warning: %v", c.in, err)
			continue
		}
		if class != c.class {
			t.Errorf("%+v: class %q, want %q", c.in, class, c.class)
		}
		if css := diagnosticCSS(&r); !strings.Contains(css, c.rule) {
			t.Errorf("%+v: the rule %q is not in\n%s", c.in, c.rule, css)
		}
		if len(warnings) != len(c.warnings) {
			t.Errorf("%+v: %d warnings, want %d: %v", c.in, len(warnings), len(c.warnings), warnings)
			continue
		}
		for i, w := range warnings {
			if !w.Warning || !strings.Contains(w.Message, c.warnings[i]) {
				t.Errorf("%+v: warning %q (Warning=%v), want one saying %q", c.in, w.Message, w.Warning, c.warnings[i])
			}
		}
		// CONTROL: the strict door refuses the same value, with the same words, and draws nothing
		var strict Rules
		got, err := table.Class(&strict, c.in)
		var p *Problem
		if !errors.As(err, &p) || !p.Warning || p.Message != warnings[0].Message || got != "" || len(strict.Classes()) != 0 {
			t.Errorf("%+v: Class gave %q, %v, recorded %v; want the first warning as its refusal and nothing recorded", c.in, got, err, strict.Classes())
		}
	}
}

// A .ui word that does nothing where it is written is a warning with no class and no rule: the page
// renders as if it were not written, which is what it does there. CONTROL: on the tag it belongs to,
// the same word is a class with its rule and no warning.
func TestStylesheetWordsThatDoNothingHere(t *testing.T) {
	table := NewTable(examplePalette(t))
	cases := []struct {
		in      Intent
		says    []string
		control Intent // the same word where it acts
	}{
		{Intent{Tag: "box", Name: "wrap", Value: "balance"}, []string{`wrap="balance" chỉ có nghĩa trên chữ (<text>)`},
			Intent{Tag: "text", Name: "wrap", Value: "balance"}},
		{Intent{Tag: "box", Width: "medium", Name: "wrap", Value: "pretty"}, []string{`wrap="pretty" chỉ có nghĩa trên chữ`},
			Intent{Tag: "text", Width: "medium", Name: "wrap", Value: "pretty"}},
		{Intent{Tag: "text", Name: "align", Value: "top"}, []string{`align="top" là vị trí các con của một khung, trên chữ không có tác dụng`, "start, center hoặc end"},
			Intent{Tag: "box", Name: "align", Value: "top"}},
		{Intent{Tag: "box", Name: "fit", Value: "cover"}, []string{"fit chỉ đặt trên image", "không đặt trên box"},
			Intent{Tag: "image", Name: "fit", Value: "cover"}},
		{Intent{Tag: "card", State: "hover", Name: "fit", Value: "contain"}, []string{"không đặt trên card"},
			Intent{Tag: "image", State: "hover", Name: "fit", Value: "contain"}},
		{Intent{Tag: "box", Name: "width", Value: "auto"}, []string{`width="auto" là mặc định: không viết width là theo nội dung`},
			Intent{Tag: "box", Name: "width", Value: "fit"}},
		{Intent{Tag: "box", Name: "max-height", Value: "auto"}, []string{`max-height="auto" là mặc định: không viết max-height là không giới hạn`},
			Intent{Tag: "box", Name: "max-height", Value: "80vh"}},
	}
	for _, c := range cases {
		var r Rules
		class, warnings, err := table.Read(&r, c.in)
		if err != nil {
			t.Errorf("%+v: refused, want a warning: %v", c.in, err)
			continue
		}
		if class != "" || len(r.Classes()) != 0 || diagnosticCSS(&r) != "" {
			t.Errorf("%+v: drew class %q, recorded %v; a word that does nothing here draws nothing", c.in, class, r.Classes())
		}
		if len(warnings) != 1 || !warnings[0].Warning {
			t.Errorf("%+v: want one warning, got %v", c.in, warnings)
			continue
		}
		for _, s := range c.says {
			if !strings.Contains(warnings[0].Message, s) {
				t.Errorf("%+v: the warning %q does not say %q", c.in, warnings[0].Message, s)
			}
		}
		var acts Rules
		class, warnings, err = table.Read(&acts, c.control)
		if err != nil || class == "" || len(warnings) != 0 || diagnosticCSS(&acts) == "" {
			t.Errorf("CONTROL %+v: class %q, warnings %v, error %v, css %q", c.control, class, warnings, err, diagnosticCSS(&acts))
		}
	}
}

// A word the table does not understand stays an error, never a warning: the value is read before the
// place is judged (fit="cove" on a box is a typo, not a misplaced fit), a number the engine cannot draw
// is refused (columns="0", opacity above 100), and the web and Tailwind words keep their error with the
// .ui word to write (decision D, question 2). An auto written under a prefix would undo a size, which
// drawing it as not written would not: it stays refused.
func TestStylesheetTyposAndWebWordsStayErrors(t *testing.T) {
	table := NewTable(examplePalette(t))
	cases := []struct {
		in   Intent
		says string
	}{
		{Intent{Tag: "box", Name: "fit", Value: "cove"}, `fit nhận cover`},
		{Intent{Tag: "text", Name: "align", Value: "middle"}, `align của text nhận start, center hoặc end, không có "middle"`},
		{Intent{Tag: "box", Name: "wrap", Value: "nowrap"}, `wrap nhận true hoặc false, không có "nowrap"`},
		{Intent{Name: "gap", Value: "big"}, `gap theo thang khoảng cách, không có "big"`},
		{Intent{Name: "padding", Value: "-4"}, `không có "-4"`},
		{Intent{Name: "padding", Value: "1e3"}, `không có "1e3"`},
		{Intent{Name: "gap", Value: "calc(1rem)"}, "không nhận calc() hay var()"},
		{Intent{Name: "width", Value: "giant"}, `không có "giant"`},
		{Intent{Name: "columns", Value: "0"}, `số nguyên từ 1 đến 12, không có "0"`},
		{Intent{Name: "columns", Value: "abc"}, `không có "abc"`},
		{Intent{Name: "span", Value: "1.5"}, `không có "1.5"`},
		{Intent{Name: "lines", Value: "0"}, `không có "0"`},
		{Intent{Name: "opacity", Value: "150"}, `số nguyên từ 0 đến 100, không có "150"`},
		{Intent{Name: "opacity", Value: "-5"}, `không có "-5"`},
		{Intent{Name: "scale", Value: "0.95"}, `không có "0.95"`},
		{Intent{Name: "rotate", Value: "45.5"}, `không có "45.5"`},
		{Intent{Name: "rotate", Value: "+45"}, `không có "+45"`},
		{Intent{Name: "rotate", Value: "-0"}, `không có "-0"`},
		{Intent{Width: "medium", Name: "width", Value: "auto"}, `không có "auto"`},
		{Intent{State: "hover,hoverr", Name: "gap", Value: "4"}, "tiền tố trạng thái không tồn tại: hoverr"},
		// the web's and Tailwind's words: an error that names the .ui word
		{Intent{Name: "radius", Value: "xl"}, `viết radius="large"`},
		{Intent{Name: "shadow", Value: "xl"}, `viết shadow="large"`},
		{Intent{Name: "container", Value: "full"}, "full là chữ của Tailwind"},
		{Intent{Name: "width", Value: "screen"}, "100vw"},
		{Intent{Name: "pin", Value: "sticky"}, `pin="top"`},
		{Intent{Name: "font", Value: "mono"}, "code (không viết mono)"},
		{Intent{Name: "size", Value: "sm"}, "size viết đủ chữ: small"},
		{Intent{Name: "weight", Value: "700"}, `viết weight="bold"`},
		{Intent{Name: "background", Value: "transparent"}, "paper/0"},
		{Intent{Name: "align", Value: "between"}, `space="between"`},
		{Intent{Name: "space", Value: "4"}, `gap="4"`},
		{Intent{Tag: "box", Name: "material", Value: "frosted"}, `viết material="glass"`},
		{Intent{Tag: "box", Name: "material", Value: "acrylic"}, "chất liệu cửa sổ của Windows"},
		{Intent{Name: "margin", Value: "4"}, "không có margin"},
		{Intent{Name: "pl", Value: "4"}, "padding-left"},
	}
	for _, c := range cases {
		var r Rules
		class, warnings, err := table.Read(&r, c.in)
		var p *Problem
		if !errors.As(err, &p) || p.Warning {
			t.Errorf("%+v: got class %q, warnings %v, error %v; want an error", c.in, class, warnings, err)
			continue
		}
		if !strings.Contains(p.Message, c.says) {
			t.Errorf("%+v: the error %q does not say %q", c.in, p.Message, c.says)
		}
		if class != "" || len(warnings) != 0 || len(r.Classes()) != 0 {
			t.Errorf("%+v: a refused value drew %q, warned %v, recorded %v", c.in, class, warnings, r.Classes())
		}
	}
}

// A value from data ({{ }}) on a tag where no value of the attribute does anything (fit on a box) is
// a warning, told once, and every value maps to no class. CONTROL: Domain, the strict door, still
// refuses it; on an image the same domain is two classes and no warning.
func TestStylesheetDomainWarnsOfWhatDoesNothing(t *testing.T) {
	table := NewTable(examplePalette(t))
	var r Rules
	domain, warnings, err := table.ReadDomain(&r, Intent{Tag: "box", Name: "fit"})
	if err != nil {
		t.Fatalf("fit from data on a box: refused, want a warning: %v", err)
	}
	if len(domain) != 2 || domain["cover"] != "" || domain["contain"] != "" {
		t.Errorf("each value maps to no class, got %v", domain)
	}
	if _, ok := domain["cover"]; !ok {
		t.Errorf("cover is a value the attribute takes: it must be in the map (no class), got %v", domain)
	}
	if len(warnings) != 1 || !warnings[0].Warning || !strings.Contains(warnings[0].Message, "không đặt trên box") {
		t.Errorf("one warning, got %v", warnings)
	}
	if len(r.Classes()) != 0 {
		t.Errorf("nothing recorded, got %v", r.Classes())
	}
	if _, err := table.Domain(nil, Intent{Tag: "box", Name: "fit"}); err == nil {
		t.Error("CONTROL: Domain has no way to tell the author: it refuses")
	}
	image, warnings, err := table.ReadDomain(&r, Intent{Tag: "image", Name: "fit"})
	if err != nil || len(warnings) != 0 || image["cover"] != "fit-cover" {
		t.Errorf("CONTROL: on an image, %v %v %v", image, warnings, err)
	}
}

// A background with /N next to a material says nothing (decision of 13/10/2026, glass first,
// background second: the /N is the fallback's opacity, a design choice; the warning that the material
// decides the transparency is gone): the table reads background="paper/80" and material="glass/30" with
// no problem, and the background hands the glass paper and the fallback paper/80. CONTROL: a value the
// table refuses still comes back as a problem.
func TestStylesheetMaterialAlphaIsSilent(t *testing.T) {
	table := NewTable(examplePalette(t))
	var r Rules
	for _, in := range []Intent{
		{Tag: "box", Name: "background", Value: "paper/80"},
		{Tag: "box", Name: "material", Value: "glass/30"},
		{Tag: "box", State: "scrolled", Name: "background", Value: "white/80"},
		{Tag: "box", State: "scrolled", Name: "material", Value: "glass"},
	} {
		if _, problems, err := table.Read(&r, in); err != nil || len(problems) != 0 {
			t.Errorf("%+v: %v %v", in, problems, err)
		}
	}
	if css := string(r.CSS()); !strings.Contains(css, `--kitwork-background:color-mix(in srgb,var(--paper) 80%,transparent);--kitwork-glass:var(--paper)}`) {
		t.Errorf("paper/80 hands the glass paper and the fallback paper/80:\n%s", css)
	}
	if _, _, err := table.Read(&r, Intent{Tag: "box", Name: "background", Value: "paper/180"}); err == nil {
		t.Error("CONTROL: an alpha past 100 is still refused")
	}
}
