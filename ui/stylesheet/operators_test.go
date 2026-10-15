package stylesheet

import (
	"errors"
	"math"
	"strconv"
	"strings"
	"testing"
)

// operatorPalettes are the palettes the operators are measured on: Kit Design's default (dark look
// derived), the kitwork example's (docs/examples/kitwork/kitwork.localhost/router.work, 13/10/2026),
// the quancaphe conformance fixture's site root (examplePalette) and its /quan-ly branch.
func operatorPalettes(t *testing.T) []struct {
	name    string
	palette *Palette
} {
	t.Helper()
	kit, err := NewPalette(true)
	if err != nil {
		t.Fatal(err)
	}
	kitwork, err := NewPalette(true, map[string]Color{
		"brand": {Light: "#f5225f", Dark: "#ff6b6b"}, "canvas": {Light: "#f6f9fc", Dark: "#16161e"},
		"paper": {Light: "#ffffff", Dark: "#1e2030"}, "panel": {Light: "#eff3f8", Dark: "#181a26"},
		"line": {Light: "#e6ebf1", Dark: "#2c3145"}, "ink": {Light: "#242442", Dark: "#c0caf5"},
		"success": {Light: "#0e9f6e"}, "warning": {Light: "#c98a1e"}, "danger": {Light: "#ef4444"}, "info": {Light: "#3b82f6"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return []struct {
		name    string
		palette *Palette
	}{
		{"Kit Design", kit},
		{"kitwork", kitwork},
		{"quancaphe", examplePalette(t)},
		{"quancaphe/quan-ly", examplePalette(t, map[string]Color{"brand": {Light: "#2563eb"}})},
	}
}

// hexes is a colour value painted in light and dark, as #rrggbb / #rrggbb.
func hexes(t *testing.T, p *Palette, value string) string {
	t.Helper()
	light, ok1 := p.Paint(value, LightMode)
	dark, ok2 := p.Paint(value, DarkMode)
	if !ok1 || !ok2 {
		t.Fatalf("%s is no colour of the palette", value)
	}
	return light.Hex() + " / " + dark.Hex()
}

// ruleOf is the declarations of a class's own rule in page.css ("" when it has none).
func ruleOf(css, class string) string {
	selector := "." + escape(class) + "{"
	for _, line := range strings.Split(css, "\n") {
		if rest, ok := strings.CutPrefix(line, selector); ok {
			return strings.TrimSuffix(rest, "}")
		}
	}
	return ""
}

// X-N is the old shade .(100-N) exactly: the same oklab mix in Go (Paint is MixOklab toward the mode's
// canvas with 100-N) and the same bytes in page.css (the string the table wrote for X.(100-N)), on four
// palettes, both modes, every fifth step from 0 to 100, for ink and every accent. ink-30 is ink.70:
// #4a5f75 in light and #7594b5 in dark on Kit Design. CONTROL: X-N and X-(100-N) differ for N other than
// 50, so a mix written the wrong way round is caught.
func TestLighterIsTheOldShade(t *testing.T) {
	for _, pp := range operatorPalettes(t) {
		table := NewTable(pp.palette)
		for _, token := range []string{"ink", "brand", "success", "warning", "danger", "paper"} {
			if !pp.palette.Has(token) {
				continue
			}
			for n := 0; n <= 100; n += 5 {
				value := token + "-" + strconv.Itoa(n)
				for _, m := range pp.palette.Modes() {
					x, _ := pp.palette.Paint(token, m)
					canvas, _ := pp.palette.Paint("canvas", m)
					got, ok := pp.palette.Paint(value, m)
					if want := MixOklab(x, canvas, float64(100-n)); !ok || got != want {
						t.Errorf("%s %s %s: Paint %s, want the old .%d %s", pp.name, m.Word(), value, got.Hex(), 100-n, want.Hex())
					}
				}
				var r Rules
				class, err := table.Class(&r, Intent{Name: "color", Value: value})
				if err != nil {
					t.Fatalf("%s %s: %v", pp.name, value, err)
				}
				old := "color:color-mix(in oklab,var(" + variable(token) + ") " + strconv.Itoa(100-n) + "%,var(--canvas))"
				if got := ruleOf(string(r.CSS()), class); got != old || class != "color-"+value {
					t.Errorf("%s %s: class %q rule %q, want the old .%d's %q", pp.name, value, class, got, 100-n, old)
				}
			}
		}
	}
	kit, _ := NewPalette(true)
	if got := hexes(t, kit, "ink-30"); got != "#4a5f75 / #7594b5" {
		t.Errorf("ink-30 on Kit Design is %s, want #4a5f75 / #7594b5 (the old ink.70)", got)
	}
	if hexes(t, kit, "ink-30") == hexes(t, kit, "ink-70") {
		t.Fatal("CONTROL: ink-30 and ink-70 paint the same: the test cannot tell the direction of the mix")
	}
}

// X+N moves away from the mode's canvas: the further N, the further its lightness from the canvas's,
// darker than X in light and lighter in dark; a point outside sRGB keeps its hue, giving up chroma
// alone (the hue of X+N is the extrapolated point's within 0.02 rad). Goldens on Kit Design: brand+15
// #5537fc / #969eff, brand+40 #4000d1 / #c2c8ff, danger+25 #ce0029 / #ffb3ad; on kitwork brand+15
// #df0051 / #ff9994. CONTROL: clipping each channel instead of the chroma turns the hue of some value
// (the check below can tell the two apart).
func TestStrongerMovesAwayFromCanvas(t *testing.T) {
	clipped := false
	for _, pp := range operatorPalettes(t) {
		for _, token := range []string{"ink", "brand", "success", "warning", "danger", "info", "line"} {
			if !pp.palette.Has(token) {
				continue
			}
			for _, m := range pp.palette.Modes() {
				x, _ := pp.palette.Paint(token, m)
				canvas, _ := pp.palette.Paint("canvas", m)
				a, c := toOklab(x), toOklab(canvas)
				previous := -1.0
				for n := 0; n <= 100; n += 5 {
					value := token + "+" + strconv.Itoa(n)
					painted, ok := pp.palette.Paint(value, m)
					if !ok {
						t.Fatalf("%s: no colour", value)
					}
					// Stronger itself: Paint rounds it to the 8 bits of its custom property, below which
					// a hue is noise
					got := Stronger(x, canvas, n)
					if painted != got.Painted() {
						t.Errorf("%s %s %s: painted %s, Stronger is %s", pp.name, m.Word(), value, painted.Hex(), got.Hex())
					}
					o := toOklab(got)
					// as a screen shows it: below 1/255 the extrapolation and the chroma's bisection are noise
					distance := math.Abs(toOklab(got.Painted()).L - c.L)
					if distance < previous-1e-9 {
						t.Errorf("%s %s %s: %.4f from the canvas's lightness, nearer than at the step before (%.4f)", pp.name, m.Word(), value, distance, previous)
					}
					previous = distance
					if light := c.L > a.L; n > 0 && (light && o.L > a.L+1e-9 || !light && o.L < a.L-1e-9) {
						t.Errorf("%s %s %s: lightness %.4f moved toward the canvas from %s's %.4f", pp.name, m.Word(), value, o.L, token, a.L)
					}
					f := float64(n) / 100
					want := oklab{L: a.L + f*(a.L-c.L), A: a.A + f*(a.A-c.A), B: a.B + f*(a.B-c.B)}
					if math.Hypot(o.A, o.B) > 0.02 && math.Hypot(want.A, want.B) > 0.02 {
						if d := hueDistance(math.Atan2(o.B, o.A), math.Atan2(want.B, want.A)); d > 0.02 {
							t.Errorf("%s %s %s: hue %.3f rad from the extrapolated point's", pp.name, m.Word(), value, d)
						}
					}
					// the control: each channel clipped instead
					naive := fromOklab(oklab{L: math.Max(0, math.Min(1, want.L)), A: want.A, B: want.B})
					if no := toOklab(naive); math.Hypot(no.A, no.B) > 0.02 && hueDistance(math.Atan2(no.B, no.A), math.Atan2(want.B, want.A)) > 0.02 {
						clipped = true
					}
				}
			}
		}
	}
	if !clipped {
		t.Error("CONTROL: clipping each channel keeps every hue: the hue check cannot tell clipping from the chroma mapping")
	}
	kit, _ := NewPalette(true)
	kitwork := operatorPalettes(t)[1].palette
	for _, c := range []struct {
		palette *Palette
		value   string
		want    string
	}{
		{kit, "brand+15", "#5537fc / #969eff"},
		{kit, "brand+40", "#4000d1 / #c2c8ff"},
		{kit, "danger+25", "#ce0029 / #ffb3ad"},
		{kitwork, "brand+15", "#df0051 / #ff9994"},
		{kit, "brand+0", hexes(t, kit, "brand")},
	} {
		if got := hexes(t, c.palette, c.value); got != c.want {
			t.Errorf("%s is %s, want %s", c.value, got, c.want)
		}
	}
	// the alpha is the colour's own, and kept
	if half, _ := kit.Paint("brand+15/40", DarkMode); half.A != 0.4 || half.Hex() != "#969eff" {
		t.Errorf("brand+15/40 in dark is %s at %v", half.Hex(), half.A)
	}
}

func hueDistance(a, b float64) float64 {
	d := math.Abs(a - b)
	if d > math.Pi {
		d = 2*math.Pi - d
	}
	return d
}

// X+N is one custom property per mode, which the page's stylesheet sets: the class reads
// var(--kitwork-strong-N-X), :root sets its light colour, the dark block its dark one. A background in it
// hands the variable to a glass as its tint, an alpha mixes it toward transparent, a border reads it
// too. Two classes of one colour write the property once.
func TestStrongerIsAPerModeProperty(t *testing.T) {
	kit, _ := NewPalette(true)
	table := NewTable(kit)
	var r Rules
	got := classes(t, table, &r,
		Intent{Name: "background", Value: "brand+15"},
		Intent{Name: "material", Value: "glass"},
		Intent{State: "hover", Name: "color", Value: "brand+15/40"},
		Intent{Name: "border", Value: "brand+15"},
	)
	if got != "background-brand+15 material-glass hover:color-brand+15/40 border-brand+15" {
		t.Fatalf("classes %q", got)
	}
	css := string(r.CSS())
	for _, want := range []string{
		LayerStatement + "\n:root{--kitwork-strong-15-brand:#5537fc}\n@media screen{:root[data-theme=dark]{--kitwork-strong-15-brand:#969eff}}\n",
		`.background-brand\+15{background-color:var(--kitwork-strong-15-brand);--kitwork-background:var(--kitwork-strong-15-brand);--kitwork-glass:var(--kitwork-strong-15-brand)}`,
		`.hover\:color-brand\+15\/40:where(:hover){color:color-mix(in srgb,var(--kitwork-strong-15-brand) 40%,transparent)}`,
		`.border-brand\+15{border-width:1px;border-color:var(--kitwork-strong-15-brand)}`,
	} {
		if !strings.Contains(css, want) {
			t.Errorf("page.css lacks %s\n%s", want, css)
		}
	}
	if n := strings.Count(css, "--kitwork-strong-15-brand:"); n != 2 {
		t.Errorf("the property is set %d times, want once a mode:\n%s", n, css)
	}
	// each attribute brings the property it reads, alone on its page
	for _, in := range []Intent{{Name: "background", Value: "brand+20"}, {Name: "color", Value: "brand+20/50"}, {Name: "border", Value: "brand+20"}, {Tag: "badge", Name: "color", Value: "brand+20"}} {
		var alone Rules
		classes(t, table, &alone, in)
		light, dark := hexes(t, kit, "brand+20")[:7], hexes(t, kit, "brand+20")[10:]
		if css := string(alone.CSS()); !strings.Contains(css, "\n:root{--kitwork-strong-20-brand:"+light+"}\n@media screen{:root[data-theme=dark]{--kitwork-strong-20-brand:"+dark+"}}\n") {
			t.Errorf("%s=%q alone does not set its property:\n%s", in.Name, in.Value, css)
		}
	}
	// without a dark look, the light value alone
	light, _ := NewPalette(false)
	var plain Rules
	classes(t, NewTable(light), &plain, Intent{Name: "color", Value: "brand.on+10"})
	if css := string(plain.CSS()); !strings.Contains(css, ":root{--kitwork-strong-10-brand-on:") || strings.Contains(css, "data-theme=dark") {
		t.Errorf("a palette without a dark look:\n%s", css)
	}
}

// Every old spelling is an error that names the new one exactly, with the suggestion an editor applies
// (Problem.Suggestion); what the grammar refuses of the new spellings says why. CONTROL: each suggestion
// compiles clean.
func TestOldColourFormsAreErrors(t *testing.T) {
	kit, _ := NewPalette(true)
	table := NewTable(kit)
	cases := []struct {
		name, value, says, suggestion string
	}{
		// E1, E1b, E2: a token's shade .N
		{"color", "ink.70", `color="ink.70": cách viết .N đã bỏ (13/10/2026): nhạt hơn giờ là ink-30, đi 30% từ ink về phía canvas, đúng màu ink.70 cũ`, "ink-30"},
		{"color", "ink.70/50", "nhạt hơn giờ là ink-30", "ink-30/50"},
		{"border", "line.40", "nhạt hơn giờ là line-60", "line-60"},
		{"color", "brand.on.70", "nhạt hơn giờ là brand.on-30", "brand.on-30"},
		{"color", "ink.100", "cách viết .N đã bỏ (13/10/2026): ink.100 là chính ink, viết ink", "ink"},
		{"color", "ink.0", "cách viết .N đã bỏ (13/10/2026): ink.0 là canvas, viết ink-100", "ink-100"},
		{"color", "ink.150", "cách viết .N đã bỏ (13/10/2026): nhạt hơn là ink-N, N là số nguyên từ 0 đến 100 (ink-30)", ""},
		{"color", "ink.07", "nhạt hơn là ink-N, N là số nguyên từ 0 đến 100", ""},
		// E3: a gradient's shade .N, and a renamed WebGradient's
		{"background", "cirrus.30", `background="cirrus.30": cách viết .N đã bỏ (13/10/2026): gradient nhạt hơn giờ là cirrus-70, mỗi điểm dừng đi 70% về phía canvas, đúng cirrus.30 cũ`, "cirrus-70"},
		{"color", "glow.100", "glow.100 là chính gradient glow, viết glow", "glow"},
		{"background", "lady-lips.40", "gradient nhạt hơn giờ là pink-blush-60, mỗi điểm dừng đi 60% về phía canvas, đúng lady-lips.40 cũ; lady-lips là tên gốc trong bộ WebGradients", "pink-blush-60"},
		// E4: a fixed colour's shade .N
		{"color", "white.50", `color="white.50": white là màu cố định, không nhạt hay đậm theo trang: độ hiện viết white/50`, "white/50"},
		{"color", "white.50/80", "độ hiện viết white/50", ""},
		{"color", "tomato.70", `color="tomato.70": tomato là màu có tên, cố định ở sáng và tối, không có -N, +N: độ hiện viết tomato/70; cần nhạt hơn hay đậm hơn thì cho một token màu ấy: router.colors({ brand: "tomato" }) rồi viết brand-30, brand+15`, "tomato/70"},
		// E5: a rung
		{"background", "brand.soft", `background="brand.soft": bậc .soft đã bỏ (13/10/2026): nền nhạt của brand giờ là brand-85 (đi 85% về phía canvas)`, "brand-85"},
		{"background", "brand.soft/0", "brand-85", "brand-85/0"},
		{"background", "success.wash", "bậc .wash đã bỏ (13/10/2026): nền rất nhạt của success giờ là success-95", "success-95"},
		// +15 is the old deep's nearest in both modes on Kit Design's brand only, so no likeness is claimed
		{"color", "brand.deep", `color="brand.deep": bậc .deep đã bỏ (13/10/2026): đậm hơn giờ là brand+15 (ra xa canvas thêm 15%); chữ cần tương phản thì kiểm tương phản gợi số lớn hơn`, "brand+15"},
		{"color", "danger.deep", `color="danger.deep": bậc .deep đã bỏ (13/10/2026): đậm hơn giờ là danger+15 (ra xa canvas thêm 15%); chữ cần tương phản thì kiểm tương phản gợi số lớn hơn`, "danger+15"},
		{"color", "ink.soft", `color="ink.soft": ink.soft không còn: chữ phụ là ink-30`, "ink-30"},
		// E5c: a rung's shade, legal before 13/10/2026: a lighter rung made lighter is one step, a
		// stronger one made lighter is two
		{"background", "brand.soft.50", `background="brand.soft.50": bậc .soft đã bỏ (13/10/2026): nền nhạt của brand giờ là brand-85 (đi 85% về phía canvas); brand.soft.50 là brand-85 còn 50% (phần còn lại là canvas): một phép là brand-93`, "brand-93"},
		{"background", "brand.soft.50/40", "một phép là brand-93", "brand-93/40"},
		{"background", "success.wash.40", "bậc .wash đã bỏ (13/10/2026): nền rất nhạt của success giờ là success-95; success.wash.40 là success-95 còn 40% (phần còn lại là canvas): một phép là success-98", "success-98"},
		{"color", "ink.soft.70", "ink.soft không còn: chữ phụ là ink-30; ink.soft.70 là ink-30 còn 70% (phần còn lại là canvas): một phép là ink-51", "ink-51"},
		{"color", "brand.deep.70", `color="brand.deep.70": bậc .deep đã bỏ (13/10/2026): đậm hơn giờ là brand+15 (ra xa canvas thêm 15%); chữ cần tương phản thì kiểm tương phản gợi số lớn hơn; brand.deep.70 là brand.deep rồi nhạt đi (.N cũ), hai phép, mà một màu nhận một phép (-N hoặc +N, rồi /N): viết brand+15 (đậm hơn) hoặc brand-N (nhạt hơn)`, ""},
		{"color", "brand.soft.150", "bậc .soft đã bỏ (13/10/2026): nền nhạt của brand giờ là brand-85 (đi 85% về phía canvas); số sau dấu . của brand.soft.150 không phải số nguyên từ 0 đến 100, và cách viết .N cũng đã bỏ: nhạt hơn là brand-N", ""},
		// a decimal after a step is an amount that is no whole number, not the old shade of a stepped name
		{"background", "glow-30.5", `background="glow-30.5": số sau dấu - của gradient glow là số nguyên từ 0 đến 100 (glow-20: mỗi điểm dừng đi 20% về phía canvas)`, ""},
		{"background", "cirrus+20.5", `background="cirrus+20.5": số sau dấu + của gradient cirrus là số nguyên từ 0 đến 100 (cirrus+20: mỗi điểm dừng ra xa canvas thêm 20%)`, ""},
		{"color", "headline-50.5", "số sau dấu - của gradient headline là số nguyên từ 0 đến 100", ""},
		{"background", "lady-lips-30.5", "số sau dấu - của gradient pink-blush là số nguyên từ 0 đến 100", ""},
		{"color", "ink-30.5", `color="ink-30.5": số sau dấu - là số nguyên từ 0 đến 100 (ink-30: đi 30% từ ink về phía canvas)`, ""},
		{"color", "brand+12.5", `color="brand+12.5": số sau dấu + là số nguyên từ 0 đến 100 (brand+15: ra xa canvas thêm 15%)`, ""},
		{"color", "white-30.5", "white là màu cố định, không có -N hay +N", ""},
		{"color", "chart-30.5", `"chart" không có trong bảng màu của trang`, ""},
		// E6: -N or +N on a fixed colour
		{"color", "white-20", `color="white-20": white là màu cố định, không có -N hay +N (chỉ màu của bảng màu mới nhạt, đậm theo trang); độ hiện viết white/N`, ""},
		{"background", "black-5", "black là màu cố định, không có -N hay +N", ""},
		{"color", "tomato+10", `tomato là màu có tên, cố định ở sáng và tối, không có -N hay +N (chỉ màu của bảng màu mới nhạt, đậm theo trang); độ hiện viết tomato/N; cần nhạt hơn hay đậm hơn thì cho một token màu ấy: router.colors({ accent: "tomato" }) rồi viết accent-30, accent+15`, ""},
		{"color", "burnt-orange-30", "burnt-orange là màu có tên, cố định ở sáng và tối, không có -N hay +N", ""},
		{"color", "blue-500", `color="blue-500": blue-500 là cách viết của Tailwind; blue là một màu có tên, cố định: cần nhạt hơn hay đậm hơn thì cho một token màu ấy: router.colors({ accent: "blue" }) rồi viết accent-30, accent+15`, ""},
		{"background", "red-600/50", "red-600 là cách viết của Tailwind", ""},
		// E7: the amount
		{"color", "ink-150", `color="ink-150": số sau dấu - là số nguyên từ 0 đến 100 (ink-30: đi 30% từ ink về phía canvas)`, ""},
		{"color", "ink-", "số sau dấu - là số nguyên từ 0 đến 100", ""},
		{"color", "ink-07", "số sau dấu - là số nguyên từ 0 đến 100", ""},
		{"color", "brand+150", `color="brand+150": số sau dấu + là số nguyên từ 0 đến 100 (brand+15: ra xa canvas thêm 15%)`, ""},
		{"color", "brand+", "số sau dấu + là số nguyên từ 0 đến 100", ""},
		// E8, E9: one operator, before the alpha
		{"color", "ink-30+10", `color="ink-30+10": một màu nhận một phép: -N (nhạt hơn) hoặc +N (đậm hơn), rồi /N (độ hiện) nếu cần`, ""},
		{"color", "brand+10-5", "một màu nhận một phép", ""},
		{"color", "ink/50-30", `color="ink/50-30": -N hay +N viết trước dấu /: ink-30/50`, "ink-30/50"},
		// an unknown base is told as the word it is
		{"color", "chart-30", `"chart" không có trong bảng màu của trang`, ""},
		{"color", "chart.30", `"chart" không có trong bảng màu của trang`, ""},
	}
	for _, c := range cases {
		t.Run(c.value, func(t *testing.T) {
			var r Rules
			class, err := table.Class(&r, Intent{Name: c.name, Value: c.value})
			var p *Problem
			if !errors.As(err, &p) || p.Warning {
				t.Fatalf("%s=%q: class %q, %v; want an error", c.name, c.value, class, err)
			}
			if !strings.Contains(p.Message, c.says) {
				t.Errorf("%s=%q says\n  %s\nwant it to say\n  %s", c.name, c.value, p.Message, c.says)
			}
			if p.Suggestion != c.suggestion {
				t.Errorf("%s=%q: suggestion %q, want %q", c.name, c.value, p.Suggestion, c.suggestion)
			}
			if len(r.Classes()) != 0 {
				t.Errorf("a refused value recorded %v", r.Classes())
			}
			if c.suggestion == "" {
				return
			}
			if _, err := table.Class(&r, Intent{Name: c.name, Value: c.suggestion}); err != nil {
				t.Errorf("CONTROL: the suggestion %s=%q does not compile: %v", c.name, c.suggestion, err)
			}
		})
	}
}

// The grammar: base, then at most one operator, the last + or the last - followed by digits, then the
// alpha. A hyphen inside a name stays the name's (burnt-orange), a + before the attribute's : is the
// prefix's join (medium+hover:), never the operator. CONTROL: the values the grammar refuses.
func TestOperatorGrammar(t *testing.T) {
	for value, want := range map[string]ColourValue{
		"ink-0":           {Base: "ink", Op: '-', Amount: 0, Alpha: 100},
		"ink-100":         {Base: "ink", Op: '-', Amount: 100, Alpha: 100},
		"brand+0":         {Base: "brand", Op: '+', Amount: 0, Alpha: 100},
		"brand.on-10":     {Base: "brand.on", Op: '-', Amount: 10, Alpha: 100},
		"brand-30/50":     {Base: "brand", Op: '-', Amount: 30, Alpha: 50, Translucent: true},
		"brand+20/40":     {Base: "brand", Op: '+', Amount: 20, Alpha: 40, Translucent: true},
		"burnt-orange/40": {Base: "burnt-orange", Alpha: 40, Translucent: true},
		"burnt-orange-30": {Base: "burnt-orange", Op: '-', Amount: 30, Alpha: 100}, // syntax: the table refuses a fixed colour's -N
		" paper/0 ":       {Base: "paper", Alpha: 0, Translucent: true},
		"ink":             {Base: "ink", Alpha: 100},
	} {
		got, ok := SplitColour(value)
		if !ok || got != want {
			t.Errorf("SplitColour(%q) = %+v %v, want %+v", value, got, ok, want)
		}
		if ok && got.String() != strings.TrimSpace(value) {
			t.Errorf("%q writes back as %q", value, got.String())
		}
	}
	for _, bad := range []string{"", "ink:80", "ink-150", "ink-", "ink-07", "brand+", "brand+150", "ink-30+10", "brand+10-5", "ink/50-30", "ink/x", "+15", "-30"} {
		if c, ok := SplitColour(bad); ok {
			t.Errorf("SplitColour(%q) = %+v: the grammar refuses it", bad, c)
		}
	}
	if c, _ := SplitColour("brand+15/40"); c.Plain() || !c.Translucent {
		t.Error("brand+15/40 is not plain")
	}
	if c, _ := SplitColour("ink-0"); c.Plain() {
		t.Error("ink-0 is written with an operator: not plain")
	}
	// gradient names cut the same way
	for v, want := range map[string]string{"cirrus-70": "cirrus - 70", "cirrus+20": "cirrus + 20", "fade-top": "fade-top  ", "warm-flame-60": "warm-flame - 60", "glow-": "glow - "} {
		base, op, amount, has := SplitStep(v)
		got := base + " " + strings.TrimRight(string(op), "\x00") + " " + amount
		if got != want || has != (op != 0) {
			t.Errorf("SplitStep(%q) = %q %v, want %q", v, got, has, want)
		}
	}
	// a prefix and the operator in one class, each where it belongs
	table := NewTable(examplePalette(t))
	var r Rules
	class, err := table.Class(&r, Intent{Width: "medium", State: "hover", Name: "background", Value: "brand+15"})
	if err != nil || class != "medium+hover:background-brand+15" {
		t.Fatalf("%q %v", class, err)
	}
	if css := string(r.CSS()); !strings.Contains(css, "@media (min-width:768px) and (hover:hover){\n"+`.medium\+hover\:background-brand\+15:where(:hover){background-color:var(--kitwork-strong-15-brand)}`) {
		t.Errorf("the class and its rule:\n%s", css)
	}
}

// No name ends in -<digits> or +<digits>, which the markup would read as a colour made lighter or
// stronger: router.colors refuses one with the name to write (the hyphens before the digits dropped),
// and no name the engine has does (named colours, the standard gradients, the backdrops, the
// WebGradients, the default tokens). CONTROL: the check finds x-2 and x+2 planted in the list.
func TestNamesMayNotEndInDigits(t *testing.T) {
	for name, want := range map[string]string{
		"chart-2":        `router.colors: "chart-2" kết thúc bằng -2, mà trong giao diện X-N là X nhạt hơn (chart-2 sẽ đọc là chart nhạt 2%): viết "chart2"`,
		"chart.series-2": `viết "chart.series2"`,
		"chart-2-3":      `viết "chart23"`,
		"accent-10":      `(accent-10 sẽ đọc là accent nhạt 10%): viết "accent10"`,
	} {
		_, err := NewPalette(false, map[string]Color{name: {Light: "#7c3aed"}})
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("router.colors %q: %v, want %s", name, err, want)
		}
	}
	for _, name := range []string{"chart2", "chart-two", "chart.series2", "h2o"} {
		if _, err := NewPalette(false, map[string]Color{name: {Light: "#7c3aed"}}); err != nil {
			t.Errorf("CONTROL: %q is a name: %v", name, err)
		}
	}
	if err := stepNameError("router.gradients", "hero-2"); err == nil || err.Error() != `router.gradients: "hero-2" kết thúc bằng -2, mà trong giao diện X-N là X nhạt hơn (hero-2 sẽ đọc là hero nhạt 2%): viết "hero2"` {
		t.Errorf("router.gradients: %v", err)
	}
	kit, _ := NewPalette(true)
	lists := map[string][]string{"named colours": NamedColours(), "standard gradients": StandardGradients(), "backdrops": Backdrops(),
		"WebGradients": WebGradients(), "default tokens": kit.Tokens()}
	counted := 0
	for list, names := range lists {
		for _, name := range names {
			counted++
			if step, _, ends := stepName(name); ends {
				t.Errorf("%s: %q ends in %s", list, name, step)
			}
		}
	}
	if counted < 500 {
		t.Fatalf("only %d names checked", counted)
	}
	for _, planted := range []string{"x-2", "x+2", "chart.series-12"} {
		if _, _, ends := stepName(planted); !ends {
			t.Errorf("CONTROL: %q is not found", planted)
		}
	}
	if _, instead, _ := stepName("x+2"); instead != "x2" {
		t.Errorf("x+2 → %q", instead)
	}
}

// Accents grow .on alone: the palette names no deep, soft or wash, colors.css writes none, and a site
// may not declare one (E15), nor a name the engine's own properties start with (E16). Kit Design's
// default palette is fifteen tokens: the surfaces and ink, the five accents and their labels.
func TestRungsAreGone(t *testing.T) {
	kit, _ := NewPalette(true)
	want := "brand brand.on canvas danger danger.on info info.on ink line panel paper success success.on warning warning.on"
	if got := strings.Join(kit.Tokens(), " "); got != want {
		t.Errorf("default tokens\n got %s\nwant %s", got, want)
	}
	css := string(Colors(kit).CSS)
	for _, gone := range []string{"--brand-soft:", "--brand-deep:", "--brand-wash:", "--info-soft:", "--danger-deep:"} {
		if strings.Contains(css, gone) {
			t.Errorf("colors.css writes %s", gone)
		}
	}
	if !strings.Contains(css, ";--brand-on:") {
		t.Errorf("colors.css lacks --brand-on:\n%s", css)
	}
	for name, says := range map[string]string{
		"brand.soft":    `router.colors: "brand.soft": bậc .soft đã bỏ (13/10/2026), màu nhấn chỉ còn mọc .on; nền nhạt viết brand-85 trong giao diện; một vai trò riêng thì đặt tên khác (brand-tint, brand.muted)`,
		"brand.wash":    `bậc .wash đã bỏ (13/10/2026), màu nhấn chỉ còn mọc .on; nền rất nhạt viết brand-95 trong giao diện`,
		"chart.deep":    `bậc .deep đã bỏ (13/10/2026), màu nhấn chỉ còn mọc .on; đậm hơn viết chart+15 trong giao diện; một vai trò riêng thì đặt tên khác (chart-strong, chart.muted)`,
		"ink.soft":      `router.colors: "ink.soft": bậc .soft đã bỏ (13/10/2026), màu nhấn chỉ còn mọc .on; chữ phụ viết ink-30 trong giao diện`,
		"kitwork-focus": `router.colors: "kitwork-focus": tên bắt đầu bằng kitwork- là của engine (--kitwork-focus, --kitwork-strong-…): đặt tên khác`,
		"kitwork.brand": `tên bắt đầu bằng kitwork- là của engine`,
	} {
		_, err := NewPalette(false, map[string]Color{name: {Light: "#555555"}})
		if err == nil || !strings.Contains(err.Error(), says) {
			t.Errorf("router.colors %q: %v\nwant %s", name, err, says)
		}
	}
	// CONTROL: the label rung, other dotted roles and a hyphenated tint are declarable
	p, err := NewPalette(false, map[string]Color{"brand.on": {Light: "#000000"}, "chart": {Light: "#7c3aed"}, "chart.muted": {Light: "#a78bfa"}, "brand-tint": {Light: "#eeeeff"}, "kitwork": {Light: "#123456"}})
	if err != nil {
		t.Fatalf("control: %v", err)
	}
	if l, _, _ := p.Value("brand.on"); l != "#000000" || !p.Has("chart.on") || !p.Has("chart.muted") {
		t.Errorf("control: %v", p.Tokens())
	}
}

// colors.<hash>.css writes the words of .error, the engine colour kitwork-error (engine_colour.go), per
// mode in --kitwork-error, the variable reboot.css reads: on Kit Design danger+25 #ce0029 in light, as
// before, and danger itself #ef7976 in dark, where danger+25 was a pale #ffb3ad. The value is what
// Palette.Engine names and Paint paints. Without a dark look, the light value alone. The author's
// danger+25 property is not written there: an author's X+N and the engine's colours are two names.
// The words read 4.5:1 on canvas, paper and panel on every palette measured, in each mode.
func TestColorsWritesTheErrorColour(t *testing.T) {
	kit, _ := NewPalette(true)
	css := string(Colors(kit).CSS)
	light, dark := modes(t, Colors(kit).CSS)
	if light["--kitwork-error"] != "#ce0029" || dark["--kitwork-error"] != "#ef7976" {
		t.Errorf("--kitwork-error: light %q, dark %q; want #ce0029, #ef7976", light["--kitwork-error"], dark["--kitwork-error"])
	}
	if strings.Contains(css, "--kitwork-strong-") {
		t.Errorf("colors.css writes an author's stronger colour:\n%s", css)
	}
	if want := hexes(t, kit, ErrorColour); light["--kitwork-error"]+" / "+dark["--kitwork-error"] != want {
		t.Errorf("colors.css and Paint disagree: %s", want)
	}
	lightValue, _ := kit.Engine(ErrorColour, LightMode)
	darkValue, _ := kit.Engine(ErrorColour, DarkMode)
	lightHex, _ := kit.Paint("danger+"+strconv.Itoa(ErrorStep), LightMode)
	darkHex, _ := kit.Paint("danger", DarkMode)
	if lightValue != "danger+"+strconv.Itoa(ErrorStep) || darkValue != "danger" ||
		lightHex.Hex() != light["--kitwork-error"] || darkHex.Hex() != dark["--kitwork-error"] {
		t.Errorf("kitwork-error is %s in light and %s in dark, want danger+25 and danger", lightValue, darkValue)
	}
	plain, _ := NewPalette(false)
	light, dark = modes(t, Colors(plain).CSS)
	if light["--kitwork-error"] != "#ce0029" || dark != nil {
		t.Errorf("without a dark look: %v %v", light, dark)
	}
	if reboot := string(Reboot().CSS); !strings.Contains(reboot, "color:var(--kitwork-error)") || strings.Contains(reboot, "--kitwork-strong-") {
		t.Error("reboot.css's .error does not read the engine colour")
	}
	// the words of .error read on every palette measured, on canvas, paper and panel, in each mode
	for _, pp := range operatorPalettes(t) {
		for _, m := range pp.palette.Modes() {
			if !pp.palette.readsIn(ErrorColour, pageGrounds, NormalTextRatio, m) {
				v, _ := pp.palette.Engine(ErrorColour, m)
				t.Errorf("%s: kitwork-error (%s) does not read 4.5:1 on canvas, paper and panel in %s", pp.name, v, m.Word())
			}
		}
	}
}

// readableIn is the smallest multiple of 5 from the floor whose X+N reads on every ground in one mode;
// 0 is the token itself. When nothing up to +100 reads, the floor stays (decision of 13/10/2026: the
// role's own step, and the contrast check tells it), not +100. CONTROL: the step before it does not
// read; a floor of 12 starts at 15.
func TestReadableIn(t *testing.T) {
	kit, _ := NewPalette(true)
	warningFill := []string{"warning-85"}
	n, ok := kit.readableIn("warning", warningFill, NormalTextRatio, 15, LightMode)
	if !ok || n != 55 {
		t.Fatalf("warning on warning-85 in light: +%d %v, want +55", n, ok)
	}
	if kit.readsIn("warning+50", warningFill, NormalTextRatio, LightMode) {
		t.Error("CONTROL: warning+50 reads too: +55 is not the smallest")
	}
	if n, ok := kit.readableIn("warning", warningFill, NormalTextRatio, 0, DarkMode); !ok || n != 0 {
		t.Errorf("warning on warning-85 in dark from 0: +%d %v, want warning itself", n, ok)
	}
	if !kit.readsIn("warning", warningFill, NormalTextRatio, DarkMode) || kit.readsIn("warning", warningFill, NormalTextRatio, LightMode) {
		t.Error("CONTROL: warning itself reads on its fill in dark only")
	}
	if n, _ := kit.readableIn("brand", []string{"brand-85"}, NormalTextRatio, 12, LightMode); n != 15 {
		t.Errorf("a floor of 12 starts at 15: +%d", n)
	}
	if n, ok := kit.readableIn("line", []string{"paper"}, 21, 25, LightMode); ok || n != 25 {
		t.Errorf("nothing reads 21:1 but black on white: +%d %v, want the floor +25 kept", n, ok)
	}
	if _, ok := kit.readableIn("white", []string{"ink"}, 3, 5, LightMode); ok {
		t.Error("a fixed colour has no +N")
	}
	if spell("brand", 0) != "brand" || spell("brand", 35) != "brand+35" {
		t.Errorf("spell: %s %s", spell("brand", 0), spell("brand", 35))
	}
}
