package stylesheet

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Colour operators (decision of 13/10/2026): a colour value takes three operators, one job each.
//
//	X-N  lighter: N% of the way from X toward the mode's canvas, mixed in OKLab exactly as
//	     color-mix(in oklab, X (100-N)%, var(--canvas)) is. ink-30 is the old ink.70, byte for byte.
//	X+N  stronger: N% further from the mode's canvas (darker in light, lighter in dark), in OKLab,
//	     brought into sRGB by its chroma alone, its lightness and hue kept. Its value is one custom
//	     property per mode, --kitwork-strong-N-X (strongVariable), that the page's stylesheet sets.
//	X/N  the alpha, as before: color-mix(in srgb, X N%, transparent).
//
// A value takes at most one of - and +, then the alpha: brand+15/40, ink-30/50. Canvas is always the
// mode's canvas token, never a page backdrop. A fixed colour (white, black, a named colour) takes the
// alpha only: it is the same in light and dark, and lighter or stronger means a mode. The old
// spellings, a shade .N and the rungs .soft, .wash and .deep that accents no longer grow, are errors
// that name the new spelling (oldColour). No name a site or the engine declares ends in -<digits>
// (stepName), and + is no name character, so the last + or the last - followed by digits is always the
// operator: burnt-orange-30 is burnt-orange made lighter by 30.

// The steps the engine writes for the old rungs and its own text. Tests derive from them.
const (
	// TintStep is a tinted fill, the old X.soft: a badge's fill, the glow and tint gradients (X-85).
	// ink-30 still reads 4.5:1 on brand-85 on every palette measured; on brand-83 it does not.
	TintStep = 85
	// FaintStep is a faint zone, the old X.wash (X-95).
	FaintStep = 95
	// StrongStep is the old X.deep's nearest in both modes on Kit Design's brand (X+15): what an old
	// .deep is told to write, and where a badge's text starts in light (engine_colour.go). Text that
	// must read takes what the contrast check suggests.
	StrongStep = 15
	// ErrorStep is where the words of .error start in a mode whose canvas is lighter than danger:
	// danger+25, 4.5:1 on canvas, paper and panel on every palette measured; in dark they start from
	// danger itself (kitwork-error, engine_colour.go, which Colors writes and reboot.css reads).
	ErrorStep = 25
)

// ColourValue is a colour value as the table reads it (table.go, colour): a base, a token or a fixed
// colour (white, black, a named colour), with at most one operator, - lighter or + stronger, and its
// amount, then an optional alpha /N. Alpha is 100 when not written; Translucent says it was.
type ColourValue struct {
	Base        string
	Op          byte // 0 for none, '-' lighter, '+' stronger
	Amount      int  // the N of -N or +N
	Alpha       int
	Translucent bool
}

// SplitColour reads a colour value as written, without the palette: ok is false for a value the
// grammar refuses (two operators, an amount or an alpha that is no whole number from 0 to 100, a
// colon). Whether the base names a colour is the palette's to say (Paint).
func SplitColour(v string) (ColourValue, bool) {
	v = strings.TrimSpace(v)
	if v == "" || strings.Contains(v, ":") {
		return ColourValue{}, false
	}
	left, alpha, hasAlpha := strings.Cut(v, "/")
	c := ColourValue{Alpha: 100}
	if hasAlpha {
		n, ok := whole(alpha, 0, 100)
		if !ok {
			return ColourValue{}, false
		}
		c.Alpha, c.Translucent = n, true
	}
	base, op, amount := splitOperator(left)
	if op != 0 {
		n, ok := whole(amount, 0, 100)
		if !ok {
			return ColourValue{}, false
		}
		c.Op, c.Amount = op, n
	}
	if base == "" || twoOperators(base) {
		return ColourValue{}, false
	}
	c.Base = base
	return c, true
}

// splitOperator cuts the operator off a value without its alpha: the last +, or else the last -
// followed by digits or by nothing (ink-, for the message). op is 0 when there is none.
func splitOperator(left string) (base string, op byte, amount string) {
	if i := strings.LastIndexByte(left, '+'); i >= 0 {
		return left[:i], '+', left[i+1:]
	}
	if i := strings.LastIndexByte(left, '-'); i >= 0 && (i == len(left)-1 || isDigits(left[i+1:])) {
		return left[:i], '-', left[i+1:]
	}
	return left, 0, ""
}

// twoOperators reports whether what is left of a value once its operator is cut still carries one:
// ink-30+10 is ink-30 made stronger.
func twoOperators(base string) bool {
	_, op, _ := splitOperator(base)
	return op != 0
}

// SplitStep cuts a step -N or +N off the end of a gradient's name, as a colour's operator is cut:
// cirrus-70 is cirrus and "70", cirrus+20 cirrus and "20". amount is as written, for the caller to
// read (whole); has is false for a name with none.
func SplitStep(v string) (base string, op byte, amount string, has bool) {
	base, op, amount = splitOperator(v)
	return base, op, amount, op != 0
}

// String writes the value back as the source writes it: ink-30, brand+15/40, paper/80, brand.
func (c ColourValue) String() string {
	s := c.Base
	if c.Op != 0 {
		s += string(c.Op) + strconv.Itoa(c.Amount)
	}
	if c.Translucent {
		s += "/" + strconv.Itoa(c.Alpha)
	}
	return s
}

// Plain reports whether the value is its base alone: no operator, no alpha.
func (c ColourValue) Plain() bool { return c.Op == 0 && !c.Translucent }

// Fixed reports whether the base is a fixed colour, the same in every mode (white, black, a named
// colour: tomato).
func (c ColourValue) Fixed() bool { _, fixed := fixedColour(c.Base); return fixed }

// Lighter is x made lighter by n: n% of the way toward canvas, mixed in OKLab, exactly as
// color-mix(in oklab, x (100-n)%, canvas) is. Lighter(x, canvas, 30) is the old shade .70.
func Lighter(x, canvas RGBA, n int) RGBA { return MixOklab(x, canvas, float64(100-n)) }

// Stronger is x made stronger by n: n% further from canvas than x is, in OKLab, x + n/100·(x − canvas)
// on L, a and b. Its lightness is held between 0 and 1; a point outside sRGB keeps its lightness and
// hue and gives up chroma, the most that stays in sRGB (bisected 40 times), so the colour never turns
// to another hue as clipping each channel would. Its alpha is x's.
func Stronger(x, canvas RGBA, n int) RGBA {
	a, c := toOklab(x), toOklab(canvas)
	f := float64(n) / 100
	o := oklab{L: a.L + f*(a.L-c.L), A: a.A + f*(a.A-c.A), B: a.B + f*(a.B-c.B)}
	o.L = math.Max(0, math.Min(1, o.L))
	if !inGamut(o) {
		chroma := math.Hypot(o.A, o.B)
		hue := math.Atan2(o.B, o.A)
		low, high := 0.0, chroma
		for i := 0; i < 40; i++ {
			mid := (low + high) / 2
			if inGamut(oklab{L: o.L, A: mid * math.Cos(hue), B: mid * math.Sin(hue)}) {
				low = mid
			} else {
				high = mid
			}
		}
		o = oklab{L: o.L, A: low * math.Cos(hue), B: low * math.Sin(hue)}
	}
	out := fromOklab(o)
	out.A = x.A
	return out
}

// linearOf is the linear sRGB of an OKLab colour, unclipped.
func linearOf(c oklab) (r, g, b float64) {
	l := c.L + 0.3963377774*c.A + 0.2158037573*c.B
	m := c.L - 0.1055613458*c.A - 0.0638541728*c.B
	s := c.L - 0.0894841775*c.A - 1.2914855480*c.B
	l, m, s = l*l*l, m*m*m, s*s*s
	return 4.0767416621*l - 3.3077115913*m + 0.2309699292*s,
		-1.2684380046*l + 2.6097574011*m - 0.3413193965*s,
		-0.0041960863*l - 0.7034186147*m + 1.7076147010*s
}

// inGamut reports whether an OKLab colour is inside sRGB, within a millionth on each channel.
func inGamut(c oklab) bool {
	const e = 1e-6
	r, g, b := linearOf(c)
	return r >= -e && r <= 1+e && g >= -e && g <= 1+e && b >= -e && b <= 1+e
}

// strongVariable is the custom property X+N is written in, one value per mode: brand+15 is
// --kitwork-strong-15-brand, brand.on+10 --kitwork-strong-10-brand-on. A token never starts with
// kitwork- (checkToken), so it is no token's; --kitwork-gradient-… is a gradient pair's.
func strongVariable(token string, n int) string {
	return "--kitwork-strong-" + strconv.Itoa(n) + "-" + strings.TrimPrefix(variable(token), "--")
}

// strongValue is the custom property of token+n with its colour in light, and in dark when the palette
// has a dark look: what the page's stylesheet sets (Rules.CSS) for an author's X+N, one step in both
// modes. An engine colour has its own (enginePair).
func (p *Palette) strongValue(token string, n int) pairValue {
	v := pairValue{name: strongVariable(token, n)}
	plain := ColourValue{Base: token, Op: '+', Amount: n, Alpha: 100}.String()
	if c, ok := p.Paint(plain, LightMode); ok {
		v.light = c.Hex()
	}
	if p.hasDark {
		if c, ok := p.Paint(plain, DarkMode); ok {
			v.dark = c.Hex()
		}
	}
	return v
}

// ---- what the table says of a colour written another way ----

// said opens a message: the attribute as written, color="ink.70": .
func said(in Intent, v string) string { return in.Name + "=\"" + v + "\": " }

// oldColour refuses a spelling the operators replaced (13/10/2026), with the new one: a shade .N of a
// token (ink.70 is ink-30, ink.100 ink, ink.0 ink-100), of a gradient (cirrus.30 is cirrus-70), of a
// fixed colour (white.50: the alpha white/50 is meant); a rung .soft, .wash or .deep of a token (brand-85,
// brand-95, brand+15; ink.soft is ink-30), and a rung's shade (brand.soft.50, rungShade). An alpha
// written after it is kept in the suggestion, but a gradient's, which it does not take. A shade of a
// word the palette does not name is told as that word unknown; a decimal after a step (glow-30.5) is an
// amount that is no whole number (stepAmountProblem). nil for any other value.
func (t *Table) oldColour(in Intent, v string) *Problem {
	left, alpha, hasAlpha := strings.Cut(v, "/")
	tail := ""
	if hasAlpha {
		tail = "/" + alpha
	}
	i := strings.LastIndexByte(left, '.')
	if i < 0 {
		return nil
	}
	base, word := left[:i], left[i+1:]
	refuse := func(message, suggestion string) *Problem {
		p := problem(in, said(in, v)+message)
		p.Suggestion = suggestion
		return p
	}
	const dropped = "cách viết .N đã bỏ (13/10/2026): "
	switch {
	case isDigits(word):
		if stem, op, amount, stepped := SplitStep(base); stepped && isDigits(amount) {
			return t.stepAmountProblem(in, v, stem, op) // glow-30.5: a decimal amount, not glow-30's shade
		}
		n, valid := whole(word, 0, 100)
		lighter := strconv.Itoa(100 - n)
		switch _, fixed := fixedColour(base); {
		case fixedColors[base] != "":
			alphaWord := base + "/N"
			if valid {
				alphaWord = base + "/" + word
			}
			p := refuse(base+" là màu cố định, không nhạt hay đậm theo trang: độ hiện viết "+alphaWord, "")
			if valid && !hasAlpha {
				p.Suggestion = alphaWord
			}
			return p
		case fixed:
			return namedShadeProblem(in, v, base, word, valid, hasAlpha)
		case t.palette.Has(base):
			switch {
			case !valid:
				return refuse(dropped+"nhạt hơn là "+base+"-N, N là số nguyên từ 0 đến 100 ("+base+"-30)", "")
			case n == 100:
				return refuse(dropped+base+".100 là chính "+base+", viết "+base, base+tail)
			case n == 0:
				return refuse(dropped+base+".0 là canvas, viết "+base+"-100", base+"-100"+tail)
			}
			return refuse(dropped+"nhạt hơn giờ là "+base+"-"+lighter+", đi "+lighter+"% từ "+base+" về phía canvas, đúng màu "+left+" cũ", base+"-"+lighter+tail)
		}
		if p := t.rungShade(in, v, left, base, word, tail); p != nil {
			return p // brand.deep.70, brand.soft.50
		}
		if colourOnly(in) || (in.Name != "background" && in.Name != "color") {
			return t.unknownColour(in, v, base) // a gradient in border is told so there (gradientMisused)
		}
		name, original := base, ""
		if renamed, from, ok := renamedWebGradient(base); ok {
			name, original = renamed, from
		} else if _, ok := t.palette.Gradient(base); !ok {
			return t.unknownColour(in, v, base)
		}
		var p *Problem
		switch {
		case !valid:
			p = refuse(dropped+"gradient nhạt hơn là "+name+"-N, N là số nguyên từ 0 đến 100 ("+name+"-20: mỗi điểm dừng đi 20% về phía canvas)", "")
		case n == 100:
			p = refuse(dropped+base+".100 là chính gradient "+name+", viết "+name, name)
		default:
			p = refuse(dropped+"gradient nhạt hơn giờ là "+name+"-"+lighter+", mỗi điểm dừng đi "+lighter+"% về phía canvas, đúng "+left+" cũ", name+"-"+lighter)
		}
		if original != "" {
			p.Message += "; " + base + " là tên gốc trong bộ WebGradients, trong Kitwork gradient ấy tên là " + name
		}
		return p
	case word == "soft" || word == "wash" || word == "deep":
		message, step, _, ok := t.rung(base, word)
		if !ok {
			return nil // a word the palette does not name, told as one
		}
		return refuse(message, step+tail)
	}
	return nil
}

// rung is what a rung the accents no longer grow is written as now (13/10/2026): base.soft is base-85,
// base.wash base-95, base.deep base+15, ink.soft ink-30. lighter is the step's amount when the rung is
// a lighter colour (85, 95, 30), 0 for .deep. ok is false when the palette does not name base.
func (t *Table) rung(base, word string) (message, step string, lighter int, ok bool) {
	if !t.palette.Has(base) {
		return "", "", 0, false
	}
	switch {
	case base == "ink" && word == "soft":
		return "ink.soft không còn: chữ phụ là ink-30", "ink-30", 30, true
	case word == "soft":
		step := base + "-" + strconv.Itoa(TintStep)
		return "bậc .soft đã bỏ (13/10/2026): nền nhạt của " + base + " giờ là " + step + " (đi " + strconv.Itoa(TintStep) + "% về phía canvas)", step, TintStep, true
	case word == "wash":
		step := base + "-" + strconv.Itoa(FaintStep)
		return "bậc .wash đã bỏ (13/10/2026): nền rất nhạt của " + base + " giờ là " + step, step, FaintStep, true
	case word == "deep":
		// +15 is the old deep's nearest in both modes on Kit Design's brand only: on other accents and
		// palettes the nearest step differs, so the message claims no likeness
		step := base + "+" + strconv.Itoa(StrongStep)
		return "bậc .deep đã bỏ (13/10/2026): đậm hơn giờ là " + step + " (ra xa canvas thêm " + strconv.Itoa(StrongStep) +
			"%); chữ cần tương phản thì kiểm tương phản gợi số lớn hơn", step, 0, true
	}
	return "", "", 0, false
}

// rungShade refuses the old shade .N of a rung (brand.soft.50, brand.deep.70, ink.soft.80), legal before
// 13/10/2026: the rung's message, and what the shade is in one operator. A lighter rung made lighter
// still is one step: brand.soft.50 is brand-85 at 50%, brand-92.5, so brand-93 (a value takes one
// operator). A stronger rung made lighter is two, stronger then lighter, which no value writes. nil when
// base is no rung of a token.
func (t *Table) rungShade(in Intent, v, left, base, word, tail string) *Problem {
	i := strings.LastIndexByte(base, '.')
	if i < 0 {
		return nil
	}
	token := base[:i]
	message, step, lighter, ok := t.rung(token, base[i+1:])
	if !ok {
		return nil
	}
	p := problem(in, said(in, v)+message)
	n, valid := whole(word, 0, 100)
	switch {
	case !valid:
		p.Message += "; số sau dấu . của " + left + " không phải số nguyên từ 0 đến 100, và cách viết .N cũng đã bỏ: nhạt hơn là " + token + "-N"
	case lighter == 0:
		p.Message += "; " + left + " là " + base + " rồi nhạt đi (.N cũ), hai phép, mà một màu nhận một phép (-N hoặc +N, rồi /N): viết " + step + " (đậm hơn) hoặc " + token + "-N (nhạt hơn)"
	default:
		one := token + "-" + strconv.Itoa(int(math.Round(100-float64((100-lighter)*n)/100)))
		p.Message += "; " + left + " là " + step + " còn " + word + "% (phần còn lại là canvas): một phép là " + one
		p.Suggestion = one + tail
	}
	return p
}

// stepAmountProblem refuses a step whose amount is written with a decimal point (ink-30.5, glow-30.5,
// brand+12.5): no name ends in -<digits> (stepName), so it is no old shade .N of a name but an amount
// that is no whole number. No suggestion: which whole number is the author's to say.
func (t *Table) stepAmountProblem(in Intent, v, stem string, op byte) *Problem {
	if _, fixed := fixedColour(stem); fixed {
		return fixedStepProblem(in, v, stem, "")
	}
	if t.palette.Has(stem) {
		return amountProblem(in, v, stem, op)
	}
	if !colourOnly(in) && (in.Name == "background" || in.Name == "color") {
		name := stem
		if renamed, _, ok := renamedWebGradient(stem); ok {
			name = renamed
		}
		if g, ok := t.palette.Gradient(name); ok && g.Op == 0 {
			return gradientAmountProblem(in, v, name, op)
		}
	}
	return t.unknownColour(in, v, stem)
}

// readOperators reads a colour value with the operators as the table does: the base, the operator and
// its amount as written, and the alpha. A problem is the grammar's, whatever the base names: two
// operators (ink-30+10), an operator after the alpha (ink/50-30).
func readOperators(in Intent, v string) (c ColourValue, amount string, p *Problem) {
	left, alpha, hasAlpha := strings.Cut(v, "/")
	if hasAlpha {
		if a, op, n := splitOperator(alpha); op != 0 && isDigits(a) && isDigits(n) {
			p := problem(in, said(in, v)+"-N hay +N viết trước dấu /: "+left+string(op)+n+"/"+a)
			if _, inner, _ := splitOperator(left); inner == 0 {
				p.Suggestion = left + string(op) + n + "/" + a
			}
			return ColourValue{}, "", p
		}
	}
	base, op, amount := splitOperator(left)
	if op != 0 && (twoOperators(base) || strings.ContainsAny(amount, "+-")) {
		return ColourValue{}, "", problem(in, said(in, v)+"một màu nhận một phép: -N (nhạt hơn) hoặc +N (đậm hơn), rồi /N (độ hiện) nếu cần")
	}
	c = ColourValue{Base: base, Op: op, Alpha: 100}
	if hasAlpha {
		c.Translucent = true
	}
	return c, amount, nil
}

// amountProblem refuses an amount that is no whole number from 0 to 100 after a token's operator.
func amountProblem(in Intent, v, base string, op byte) *Problem {
	if op == '+' {
		return problem(in, said(in, v)+"số sau dấu + là số nguyên từ 0 đến 100 ("+base+"+15: ra xa canvas thêm 15%)")
	}
	return problem(in, said(in, v)+"số sau dấu - là số nguyên từ 0 đến 100 ("+base+"-30: đi 30% từ "+base+" về phía canvas)")
}

// fixedStepProblem refuses -N or +N on a fixed colour: white and black, and a named colour, are the
// same in light and dark, and lighter or stronger is toward or away from a mode's canvas. A named colour
// written as Tailwind writes its scale (blue-500) is told so.
func fixedStepProblem(in Intent, v, base, amount string) *Problem {
	if fixedColors[base] != "" {
		return problem(in, said(in, v)+base+" là màu cố định, không có -N hay +N (chỉ màu của bảng màu mới nhạt, đậm theo trang); độ hiện viết "+base+"/N")
	}
	role := `router.colors({ accent: "` + base + `" }) rồi viết accent-30, accent+15`
	left, _, _ := strings.Cut(v, "/")
	if n, err := strconv.Atoi(amount); err == nil && isDigits(amount) && n > 100 && strings.HasPrefix(left, base+"-") {
		return problem(in, said(in, v)+left+" là cách viết của Tailwind; "+base+" là một màu có tên, cố định: cần nhạt hơn hay đậm hơn thì cho một token màu ấy: "+role)
	}
	return problem(in, said(in, v)+base+" là màu có tên, cố định ở sáng và tối, không có -N hay +N (chỉ màu của bảng màu mới nhạt, đậm theo trang); độ hiện viết "+base+
		"/N; cần nhạt hơn hay đậm hơn thì cho một token màu ấy: "+role)
}

// ---- names ----

// stepName reports whether a name ends in -<digits> or +<digits>, which the markup would read as a
// colour or a gradient made lighter or stronger (chart-2 is chart, lighter by 2), with the step as
// written and the name to write instead: the hyphens before the trailing digits dropped (chart2,
// chart.series2).
func stepName(name string) (step, instead string, ends bool) {
	base, op, amount := splitOperator(name)
	if op == 0 || !isDigits(amount) {
		return "", "", false
	}
	step = name[len(base):]
	instead = base + amount
	for {
		b, o, a := splitOperator(instead)
		if o == 0 || !isDigits(a) {
			break
		}
		instead = b + a
	}
	return step, instead, true
}

// stepNameError refuses a name router.colors or router.gradients declares that ends in -<digits>
// (stepName): registry is router.colors or router.gradients. nil for any other name.
func stepNameError(registry, name string) error {
	step, instead, ends := stepName(name)
	if !ends {
		return nil
	}
	base, rule, word := strings.TrimSuffix(name, step), "X-N là X nhạt hơn", "nhạt"
	if step[0] == '+' {
		rule, word = "X+N là X đậm hơn", "đậm"
	}
	return fmt.Errorf("%s: %q kết thúc bằng %s, mà trong giao diện %s (%s sẽ đọc là %s %s %s%%): viết %q",
		registry, name, step, rule, name, base, word, step[1:], instead)
}
