package stylesheet

import (
	"errors"
	"sort"
	"strings"
	"testing"
)

// The sides and corners decided 13/10/2026 ("start với end nó hợp với flex hơn", and padding-left is
// a word of the web worth keeping): padding-left and padding-right pad the screen's side and never
// flip; radius has its sides (top, bottom, left, right: two corners each) and its corners (top-left…),
// physical, and their logical twins (start, end; top-start…), which flip right to left. Each makes the
// class as written and exactly its CSS.
func TestSidesAndCornersMakeTheirClassAndExactCSS(t *testing.T) {
	table := NewTable(nil)
	for _, c := range []struct{ name, value, rule string }{
		{"padding-left", "4", `.padding-left-4{padding-left:1rem}`},
		{"padding-right", "2", `.padding-right-2{padding-right:.5rem}`},
		{"padding-left", "0.5", `.padding-left-0\.5{padding-left:.125rem}`},
		{"radius-top", "large", `.radius-top-large{border-top-left-radius:.75rem;border-top-right-radius:.75rem}`},
		{"radius-bottom", "none", `.radius-bottom-none{border-bottom-left-radius:0;border-bottom-right-radius:0}`},
		{"radius-left", "small", `.radius-left-small{border-top-left-radius:.375rem;border-bottom-left-radius:.375rem}`},
		{"radius-right", "medium", `.radius-right-medium{border-top-right-radius:.5rem;border-bottom-right-radius:.5rem}`},
		{"radius-start", "full", `.radius-start-full{border-start-start-radius:999px;border-end-start-radius:999px}`},
		{"radius-end", "large", `.radius-end-large{border-start-end-radius:.75rem;border-end-end-radius:.75rem}`},
		{"radius-top-left", "large", `.radius-top-left-large{border-top-left-radius:.75rem}`},
		{"radius-top-right", "small", `.radius-top-right-small{border-top-right-radius:.375rem}`},
		{"radius-bottom-left", "full", `.radius-bottom-left-full{border-bottom-left-radius:999px}`},
		{"radius-bottom-right", "none", `.radius-bottom-right-none{border-bottom-right-radius:0}`},
		{"radius-top-start", "large", `.radius-top-start-large{border-start-start-radius:.75rem}`},
		{"radius-top-end", "medium", `.radius-top-end-medium{border-start-end-radius:.5rem}`},
		{"radius-bottom-start", "small", `.radius-bottom-start-small{border-end-start-radius:.375rem}`},
		{"radius-bottom-end", "full", `.radius-bottom-end-full{border-end-end-radius:999px}`},
	} {
		var r Rules
		class, err := table.Class(&r, Intent{Name: c.name, Value: c.value})
		if err != nil {
			t.Errorf("%s=%q: %v", c.name, c.value, err)
			continue
		}
		if class != c.name+"-"+c.value {
			t.Errorf("%s=%q: class %q", c.name, c.value, class)
		}
		want := "@layer reboot,elements,page,show;\n@layer page{\n" + c.rule + "\n}\n"
		if css := string(r.CSS()); css != want {
			t.Errorf("%s=%q:\n got  %q\n want %q", c.name, c.value, css, want)
		}
	}
	// a side takes the five words of radius and refuses another, naming itself; Tailwind's word is told
	// its .ui word as radius's is
	if _, err := table.Class(nil, Intent{Name: "radius-top", Value: "round"}); err == nil ||
		!strings.Contains(err.Error(), `radius-top nhận none, small, medium, large hoặc full, không có "round"`) {
		t.Errorf("radius-top=\"round\": %v", err)
	}
	var p *Problem
	if _, err := table.Class(nil, Intent{Name: "radius-top-left", Value: "lg"}); !errors.As(err, &p) || p.Suggestion != "medium" ||
		!strings.Contains(p.Message, `radius-top-left="lg" là chữ cũ của Tailwind: viết radius-top-left="medium" (.5rem)`) {
		t.Errorf("radius-top-left=\"lg\": %v", err)
	}
	// from data: the five words, like radius
	domain, err := table.Domain(nil, Intent{Name: "radius-bottom"})
	if err != nil || len(domain) != 5 || domain["none"] != "radius-bottom-none" {
		t.Errorf("radius-bottom from data: %v %v", domain, err)
	}
}

// Within one condition a corner beats a side and a side beats radius, and padding-left beats
// padding-x beats padding, whatever order they are written in: the intents below are recorded in the
// reverse order (the most specific first), and the rules still come out general first, so the later,
// more specific one wins. Each physical word comes before its logical twin. CONTROL: sorted as text
// the same classes would put radius after radius-bottom and padding-x after padding-left, so the order
// is the table's and not an accident of the names.
func TestCornerBeatsSideBeatsRadiusWhateverTheOrderWritten(t *testing.T) {
	table := NewTable(nil)
	var r Rules
	written := []Intent{ // most specific first
		{Name: "radius-top-start", Value: "small"},
		{Name: "radius-top-left", Value: "full"},
		{Name: "radius-start", Value: "small"},
		{Name: "radius-bottom", Value: "none"},
		{Name: "radius", Value: "large"},
		{Name: "padding-start", Value: "3"},
		{Name: "padding-left", Value: "1"},
		{Name: "padding-x", Value: "4"},
		{Name: "padding", Value: "8"},
	}
	for _, in := range written {
		if _, err := table.Class(&r, in); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{
		`.padding-8{`,
		`.padding-x-4{`,
		`.padding-left-1{`,
		`.padding-start-3{`,
		`.radius-large{`,
		`.radius-bottom-none{`,
		`.radius-start-small{`,
		`.radius-top-left-full{`,
		`.radius-top-start-small{`,
	}
	var got []string
	for _, line := range strings.Split(string(r.CSS()), "\n") {
		if strings.HasPrefix(line, ".") {
			got = append(got, line)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("%d rules, want %d:\n%s", len(got), len(want), r.CSS())
	}
	for i := range want {
		if !strings.HasPrefix(got[i], want[i]) {
			t.Fatalf("rule %d is %q, want %s…\n%s", i, got[i], want[i], r.CSS())
		}
	}
	// Rank says the same order to the compiler
	for _, pair := range [][2]string{
		{"padding", "padding-x"}, {"padding-x", "padding-left"}, {"padding-left", "padding-start"},
		{"padding-right", "padding-end"}, {"radius", "radius-bottom"}, {"radius-left", "radius-start"},
		{"radius-end", "radius-top-left"}, {"radius-top-left", "radius-top-start"}, {"radius-bottom-right", "radius-bottom-end"},
	} {
		if Rank(pair[0]) >= Rank(pair[1]) || Rank(pair[0]) < 0 {
			t.Errorf("%s ranks %d, %s %d: the second must win", pair[0], Rank(pair[0]), pair[1], Rank(pair[1]))
		}
	}
	if Rank("pl") != -1 {
		t.Error("a name the table does not read has no rank")
	}
	// CONTROL
	names := []string{"radius-large", "radius-bottom-none", "padding-x-4", "padding-left-1"}
	sort.Strings(names)
	before := func(a, b string) bool { return indexOf(names, a) < indexOf(names, b) }
	if !before("radius-bottom-none", "radius-large") || !before("padding-left-1", "padding-x-4") {
		t.Fatalf("control: alphabetical order no longer differs from the fixed order: %v", names)
	}
}

// A prefix works for each new attribute as for every presentation attribute: medium:radius-bottom is
// min-width 768px, hover:padding-left holds where a pointer hovers.
func TestSidesAndCornersTakePrefixes(t *testing.T) {
	table := NewTable(nil)
	var r Rules
	got := classes(t, table, &r,
		Intent{Width: "medium", Name: "radius-bottom", Value: "large"},
		Intent{State: "hover", Name: "padding-left", Value: "4"},
		Intent{Width: "large", State: "focus", Name: "radius-top-start", Value: "none"},
	)
	if got != "medium:radius-bottom-large hover:padding-left-4 large+focus:radius-top-start-none" {
		t.Fatalf("classes %q", got)
	}
	css := string(r.CSS())
	for _, rule := range []string{
		"@media (min-width:768px){\n" + `.medium\:radius-bottom-large{border-bottom-left-radius:.75rem;border-bottom-right-radius:.75rem}` + "\n}",
		"@media (hover:hover){\n" + `.hover\:padding-left-4:where(:hover){padding-left:1rem}` + "\n}",
		"@media (min-width:1024px){\n" + `.large\+focus\:radius-top-start-none:where(:focus-visible){border-start-start-radius:0}` + "\n}",
	} {
		if !strings.Contains(css, rule) {
			t.Errorf("lacks\n%s\nin\n%s", rule, css)
		}
	}
}

// Logical is the twin of a physical left or right: the logical word for the same side in a line
// written left to right, and the other one right to left, where left is the end of the line. Top,
// bottom and the logical words have none.
func TestLogicalTwinOfEachPhysicalSide(t *testing.T) {
	for name, want := range map[string][2]string{
		"padding-left":        {"padding-start", "padding-end"},
		"padding-right":       {"padding-end", "padding-start"},
		"radius-left":         {"radius-start", "radius-end"},
		"radius-right":        {"radius-end", "radius-start"},
		"radius-top-left":     {"radius-top-start", "radius-top-end"},
		"radius-top-right":    {"radius-top-end", "radius-top-start"},
		"radius-bottom-left":  {"radius-bottom-start", "radius-bottom-end"},
		"radius-bottom-right": {"radius-bottom-end", "radius-bottom-start"},
	} {
		if got := [2]string{Logical(name, false), Logical(name, true)}; got != want {
			t.Errorf("%s: twins %v, want %v", name, got, want)
		}
		for _, twin := range want {
			if !IsPresentation(twin) {
				t.Errorf("%s's twin %s is not an attribute", name, twin)
			}
		}
	}
	for _, name := range []string{"padding", "padding-x", "padding-top", "padding-start", "radius", "radius-top", "radius-bottom",
		"radius-start", "radius-top-start", "radius-bottom-end", "align", "pl", "text-left"} {
		if got := Logical(name, false) + Logical(name, true); got != "" {
			t.Errorf("%s has no twin, got %q", name, got)
		}
	}
	// every physical left or right of the table has its twin, so none is left out of the warnings
	for _, name := range Attributes() {
		physical := strings.HasSuffix(name, "-left") || strings.HasSuffix(name, "-right")
		if physical != (Logical(name, false) != "") {
			t.Errorf("%s: physical %v, twin %q", name, physical, Logical(name, false))
		}
	}
}

// Words for a side or a corner from elsewhere are typos with the .ui word to write (decision 13/10/2026):
// Tailwind's padding names and classes (pl, pl-4), Tailwind's rounded sides and corners, CSS's corner
// properties and a corner named the other way round. Tailwind's sizes are read by their size, as
// radius="lg" is: rounded-t-lg is .5rem, so radius-top="medium" (the decision's sketch said "large";
// the measured size and radius="lg" say medium). CONTROL: a name like none of them is only "not a
// presentation attribute", with no suggestion.
func TestSidesFromElsewhereAreSuggested(t *testing.T) {
	table := NewTable(nil)
	for _, c := range []struct {
		in         Intent
		says       string
		suggestion string
	}{
		{Intent{Name: "rounded-t-lg"}, `rounded-t-lg là chữ của Tailwind; .ui viết thuộc tính: radius-top="medium" (.5rem)`, `radius-top="medium"`},
		{Intent{Name: "rounded-tl", Value: "xl"}, `radius-top-left="large"`, `radius-top-left="large"`},
		{Intent{Name: "rounded-b"}, `radius-bottom="small"`, `radius-bottom="small"`},
		{Intent{Name: "rounded-ee-2xl"}, `radius-bottom-end="large"`, `radius-bottom-end="large"`},
		{Intent{Name: "rounded-s-full"}, `radius-start="full"`, `radius-start="full"`},
		{Intent{Name: "rounded-lg"}, `radius="medium"`, `radius="medium"`},
		{Intent{Name: "rounded-tr", Value: "8px"}, `.ui viết radius-top-right (none, small, medium, large, full)`, "radius-top-right"},
		{Intent{Name: "border-top-left-radius", Value: "8px"}, `góc trên bên trái là radius-top-left`, "radius-top-left"},
		{Intent{Name: "border-bottom-right-radius", Value: "8px"}, "radius-bottom-right", "radius-bottom-right"},
		{Intent{Name: "border-end-start-radius", Value: "8px"}, `góc dưới đầu dòng là radius-bottom-start`, "radius-bottom-start"},
		{Intent{Name: "radius-left-top", Value: "large"}, `radius-left-top="large": góc viết cạnh trên hay dưới trước rồi mới đến left: radius-top-left`, "radius-top-left"},
		{Intent{Name: "radius-end-bottom", Value: "small"}, "radius-bottom-end", "radius-bottom-end"},
		{Intent{Name: "pl", Value: "4"}, `pl="4": pl là chữ viết tắt của Tailwind; .ui viết đủ chữ: padding-left`, "padding-left"},
		{Intent{Name: "pr", Value: "2"}, "padding-right", "padding-right"},
		{Intent{Name: "ps", Value: "2"}, "padding-start", "padding-start"},
		{Intent{Name: "px", Value: "2"}, "padding-x", "padding-x"},
		{Intent{Name: "pl-4"}, `pl-4 là lớp của Tailwind; .ui viết thuộc tính: padding-left="4"`, `padding-left="4"`},
		{Intent{Name: "pr-0.5"}, `padding-right="0.5"`, `padding-right="0.5"`},
		{Intent{Name: "padding-inline-start", Value: "4"}, "cạnh đầu dòng là padding-start", "padding-start"},
		{Intent{Name: "padding-block-end", Value: "4"}, "padding-bottom", "padding-bottom"},
		// CONTROL: not Tailwind's, not CSS's, not a corner turned round
		{Intent{Name: "rounded-x"}, "rounded-x không phải thuộc tính trình bày", ""},
		{Intent{Name: "rounded-t-huge"}, "rounded-t-huge không phải thuộc tính trình bày", ""},
		{Intent{Name: "pl-wide", Value: "4"}, "pl-wide không phải thuộc tính trình bày", ""},
		{Intent{Name: "radius-middle", Value: "large"}, "radius-middle không phải thuộc tính trình bày", ""},
		{Intent{Name: "radius-top-middle", Value: "large"}, "radius-top-middle không phải thuộc tính trình bày", ""},
	} {
		var r Rules
		class, err := table.Class(&r, c.in)
		var p *Problem
		if !errors.As(err, &p) || p.Warning {
			t.Errorf("%+v: got class %q, error %v; want an error", c.in, class, err)
			continue
		}
		if !strings.Contains(p.Message, c.says) {
			t.Errorf("%+v: the error\n %s\ndoes not say\n %s", c.in, p.Message, c.says)
		}
		if p.Suggestion != c.suggestion {
			t.Errorf("%+v: suggestion %q, want %q", c.in, p.Suggestion, c.suggestion)
		}
		if p.Name != c.in.Name || class != "" || len(r.Classes()) != 0 {
			t.Errorf("%+v: names %q, drew %q, recorded %v", c.in, p.Name, class, r.Classes())
		}
	}
}
