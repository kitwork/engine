package css

import (
	"fmt"
	"strconv"
	"strings"
)

// --- UTILITIES ---

func transformUnit(s string) string {
	if s == "none" || s == "0" {
		return "0"
	}
	if strings.HasSuffix(s, "pct") {
		return strings.TrimSuffix(s, "pct") + "%"
	}
	if strings.HasSuffix(s, "px") || strings.HasSuffix(s, "rem") || strings.HasSuffix(s, "vh") || strings.HasSuffix(s, "vw") || strings.HasSuffix(s, "em") || strings.HasSuffix(s, "%") {
		return s
	}
	if isNumeric(s) {
		return s + ExplicitUnit
	}
	return s
}

func isNumeric(s string) bool { _, err := strconv.Atoi(s); return err == nil }
func mustInt(s string) int    { i, _ := strconv.Atoi(s); return i }

// rgbWrap turns a twColor result into a usable CSS color: "r, g, b" → "rgb(r, g, b)";
// hex / transparent / currentColor pass through; empty stays empty.
func rgbWrap(col string) string {
	if col == "" || col == "transparent" || col == "currentColor" {
		return col
	}
	if strings.HasPrefix(col, "#") {
		return col
	}
	return "rgb(" + col + ")"
}

// gradientStop emits the Tailwind gradient CSS-var declarations for from/via/to.
func gradientStop(pos, color string) string {
	switch pos {
	case "from":
		return fmt.Sprintf("--kitwork-gradient-from: %s; --kitwork-gradient-stops: var(--kitwork-gradient-from), var(--kitwork-gradient-to, transparent);", color)
	case "via":
		return fmt.Sprintf("--kitwork-gradient-stops: var(--kitwork-gradient-from), %s, var(--kitwork-gradient-to, transparent);", color)
	case "to":
		return fmt.Sprintf("--kitwork-gradient-to: %s;", color)
	}
	return ""
}

// unarb unwraps a Tailwind arbitrary value: "[1fr_2fr]" -> "1fr 2fr" (underscores become
// spaces, per Tailwind). Non-arbitrary input is returned unchanged.
func unarb(s string) string {
	if strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]") {
		return normalizeMath(strings.ReplaceAll(s[1:len(s)-1], "_", " "))
	}
	return s
}

// normalizeMath puts the spaces CSS math needs around + - * / inside calc(), min(), max()
// and clamp(): a class cannot hold spaces, so `calc(100%+8px)` is what gets written and
// `calc(100% + 8px)` is what the browser accepts. A `-` is an operator only after an
// operand — a number with its unit, or a closing paren — so `safe-area-inset-top` and
// `var(--gap)` keep their hyphens, and `*-1` keeps its sign.
func normalizeMath(s string) string {
	if !strings.Contains(s, "(") {
		return s
	}
	var out strings.Builder
	var stack []string // the open functions, innermost last
	mathDepth, varDepth := 0, 0
	word := ""
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '(':
			stack = append(stack, word)
			switch word {
			case "calc", "min", "max", "clamp":
				mathDepth++
			case "var":
				varDepth++
			}
			word = ""
			out.WriteByte(c)
			continue
		case c == ')':
			if n := len(stack); n > 0 {
				switch stack[n-1] {
				case "calc", "min", "max", "clamp":
					mathDepth--
				case "var":
					varDepth--
				}
				stack = stack[:n-1]
			}
			word = ""
			out.WriteByte(c)
			continue
		case (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '-' && word != "" && varDepth > 0:
			word += string(c)
		default:
			if c != '-' {
				word = ""
			}
		}
		isOp := c == '+' || c == '*' || c == '/' || (c == '-' && operandBefore(s, i))
		if isOp && mathDepth > 0 && varDepth == 0 {
			prevSpace := i > 0 && s[i-1] == ' '
			nextSpace := i+1 < len(s) && s[i+1] == ' '
			if !prevSpace && i > 0 && s[i-1] != '(' {
				out.WriteByte(' ')
			}
			out.WriteByte(c)
			if !nextSpace && i+1 < len(s) {
				out.WriteByte(' ')
			}
			word = ""
			continue
		}
		out.WriteByte(c)
	}
	return out.String()
}

// operandBefore says whether the text before position i ends in an operand: a number with an
// optional unit or percent sign, or a closing paren.
func operandBefore(s string, i int) bool {
	j := i - 1
	for j >= 0 && s[j] == ' ' {
		j--
	}
	if j < 0 {
		return false
	}
	if s[j] == ')' || s[j] == '%' {
		return true
	}
	for j >= 0 && ((s[j] >= 'a' && s[j] <= 'z') || (s[j] >= 'A' && s[j] <= 'Z')) {
		j--
	}
	return j >= 0 && (s[j] >= '0' && s[j] <= '9' || s[j] == '.')
}

// negate applies Tailwind's negative prefix: a plain length just gets the sign; a value that
// is an expression — calc(), var(), a nested function — becomes calc(<value> * -1), because
// `-calc(…)` is not CSS and the whole declaration would be dropped.
func negate(val string) string {
	if strings.Contains(val, "(") || strings.HasPrefix(val, "-") {
		return "calc(" + val + " * -1)"
	}
	return "-" + val
}

// scaleVal turns a Tailwind scale number (105) into a CSS scale factor (1.05); arbitrary
// values pass through. Honors the negative-prefix flag.
func scaleVal(s string, neg bool) string {
	if strings.HasPrefix(s, "[") {
		return unarb(s)
	}
	f, _ := strconv.ParseFloat(s, 64)
	v := fmt.Sprintf("%g", f/100)
	if neg {
		v = "-" + v
	}
	return v
}

// twUnit converts Tailwind syntax to CSS values (e.g. 4 -> 1rem, [120px] -> 120px)
func twUnit(s string) string {
	if strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]") {
		return unarb(s)
	}
	if s == "px" {
		return "1px"
	}
	if s == "full" {
		return "100%"
	}
	if s == "screen" {
		return "100vw" // Context-dependent (vw or vh), handled in engine
	}
	if s == "auto" || s == "none" || s == "min-content" || s == "max-content" || s == "fit-content" {
		return s
	}
	if s == "fit" {
		return "fit-content"
	}
	if s == "min" {
		return "min-content"
	}
	if s == "max" {
		return "max-content"
	}
	// Fractions: 1/2 → 50%, 2/3 → 66.6667%
	if strings.Contains(s, "/") {
		parts := strings.SplitN(s, "/", 2)
		if n, e1 := strconv.ParseFloat(parts[0], 64); e1 == nil {
			if d, e2 := strconv.ParseFloat(parts[1], 64); e2 == nil && d != 0 {
				return fmt.Sprintf("%g%%", n/d*100)
			}
		}
	}
	// Numeric (incl. decimals like 0.5, 1.5): 1 tw unit = 0.25rem.
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		if f == 0 {
			return "0px"
		}
		return fmt.Sprintf("%grem", f*0.25)
	}
	return s
}

// derivedRungs are the role suffixes a flat design token can grow on demand. They mirror the
// vocabulary palette() produces, so `sage: "#def7ec"` alone still answers bg-sage-soft.
var derivedRungs = map[string]bool{"deep": true, "soft": true, "wash": true, "on": true}

// deriveRung grows a role rung from a token declared as a single flat colour, using the same OKLCH
// maths as palette(). It deliberately refuses Tailwind's own families: `blue` already ships eleven
// hand-tuned shades, so a derived `blue-soft` would be a second way to say nearly the same thing.
// A rung the site spells out explicitly is found earlier and always wins over this.
func deriveRung(colorName string, colors map[string]Color) (string, bool) {
	i := strings.LastIndexByte(colorName, '-')
	if i <= 0 {
		return "", false
	}
	base, rung := colorName[:i], colorName[i+1:]
	if !derivedRungs[rung] {
		return "", false
	}
	if _, isPalette := TwPalette[base]; isPalette {
		return "", false
	}
	seed, ok := colors[base]
	if !ok {
		return "", false
	}
	hex, ok := Palette(seed.HexString())[rung]
	if !ok {
		return "", false
	}
	r, g, b, ok := parseHexRGB(hex)
	if !ok {
		return "", false
	}
	return fmt.Sprintf("%d, %d, %d", int(r), int(g), int(b)), true
}

// twColor resolves a Tailwind color to an "R, G, B" string (or hex for arbitrary /
// transparent passthrough). Lookup order: arbitrary [..] → base keywords → the
// Tailwind v3 palette (family-shade, see twpalette.go) → the custom Colors map.
func twColor(colorName, shade string, cfg *Config) string {
	if strings.HasPrefix(colorName, "[") && strings.HasSuffix(colorName, "]") {
		return colorName[1 : len(colorName)-1] // e.g. [#fcfcfd]
	}

	// Base keywords (no shade).
	switch colorName {
	case "white":
		return "255, 255, 255"
	case "black":
		return "0, 0, 0"
	case "transparent":
		return "transparent"
	case "current":
		return "currentColor"
	}

	// Tailwind palette: family + shade (e.g. slate-800, gray-400, emerald-500).
	if shade != "" {
		if fam, ok := TwPalette[colorName]; ok {
			if rgb, ok := fam[shade]; ok {
				return rgb
			}
		}
	}

	// Custom design-system tokens are named by ROLE (brand, brand-soft, ink), never numbered, so a
	// numeric shade on one is always a mistake. This used to ignore the shade and fall through to the
	// plain token, which meant bg-brand-100, bg-brand-500 and bg-brand-900 all silently painted the
	// same colour — and lost the themeable var() form on the way, since only the unshaded path emits
	// it. Tailwind generates nothing for a shade you never defined; do the same, so a wrong class
	// shows up as "no style" instead of a plausible-looking wrong colour.
	if shade == "" {
		colors := Colors
		if cfg != nil {
			colors = cfg.Colors
		}
		if rgb, ok := colors[colorName]; ok {
			return rgb.String()
		}
		// Not spelled out — grow the rung from the flat token it belongs to.
		if rgb, ok := deriveRung(colorName, colors); ok {
			return rgb
		}
	}

	// Brand-logo colours: brand-<slug> (text-brand-github, bg-brand-stripe, …) resolve to the
	// official hex registered by jit/logo. Returns a #hex; the callers handle the '#' path.
	if hex, ok := brandHex(colorName); ok {
		return hex
	}

	// Code-surface colours: terminal, terminal-bar, terminal-keyword … resolve
	// through router.highlight(). Returned as a triplet, not a hex, so
	// colorCSSValue can wrap it in var() like any configured token — otherwise a
	// [data-theme] block could re-skin every colour on the site except these.
	if color, ok := highlightColor(colorName, cfg); ok {
		return color.String()
	}

	// Tailwind family without an explicit shade → default to the 500 shade (Tailwind's
	// behavior for `bg-blue` etc., though v3 usually requires a shade).
	if fam, ok := TwPalette[colorName]; ok {
		if rgb, ok := fam["500"]; ok {
			return rgb
		}
	}

	return ""
}

// colorCSSValue resolves a Tailwind-style color to a finished CSS color value, mirroring the
// tw-color-base / tw-color-shade handlers so utilities beyond bg/text/border/ring (e.g. divide-*)
// share one behavior: a config design token (no shade) resolves through var(--color-<token>,
// <triplet>) so a [data-theme] block can re-skin it; palette family+shade stays baked; an optional
// alpha ("40" or "[.4]") yields rgba(); a hex passes through (8-digit when alpha given). Returns ""
// for an unknown color so callers can bail and let other patterns try.
func colorCSSValue(name, shade, alpha string, cfg *Config) string {
	color := twColor(name, shade, cfg)
	if color == "" {
		return ""
	}
	if color == "transparent" || color == "currentColor" {
		return color
	}
	if color[0] == '#' {
		if alpha != "" && isNumeric(alpha) {
			return fmt.Sprintf("%s%02x", color, mustInt(alpha)*255/100)
		}
		return color
	}
	// "r, g, b" triplet. A named design token (no shade) resolves through its CSS var so themes can
	// override it; the literal triplet stays as the var() fallback (default render is unchanged).
	expr := color
	if shade == "" {
		colors := Colors
		if cfg != nil && cfg.Colors != nil {
			colors = cfg.Colors
		}
		_, configured := colors[name]
		if configured || highlightRegistered(name, cfg) {
			expr = "var(--color-" + name + ", " + color + ")"
		}
	}
	if alpha != "" {
		if strings.HasPrefix(alpha, "[") && strings.HasSuffix(alpha, "]") {
			return fmt.Sprintf("rgba(%s, %s)", expr, alpha[1:len(alpha)-1])
		}
		return fmt.Sprintf("rgba(%s, %.2f)", expr, float64(mustInt(alpha))/100.0)
	}
	return fmt.Sprintf("rgb(%s)", expr)
}

// unescapeArbitrary applies Tailwind's spacing rule to the INSIDE of an
// arbitrary value: an underscore stands for a space, and `\_` for a literal
// underscore — which a url() path may well contain.
func unescapeArbitrary(value string) string {
	var b strings.Builder
	for i := 0; i < len(value); i++ {
		switch {
		case value[i] == '\\' && i+1 < len(value) && value[i+1] == '_':
			b.WriteByte('_')
			i++
		case value[i] == '_':
			b.WriteByte(' ')
		default:
			b.WriteByte(value[i])
		}
	}
	return b.String()
}

// isGradientValue reports whether an arbitrary background value opens with one
// of the CSS gradient functions, including a multi-layer list — the first layer
// decides, since every layer of a background-image list is an image.
func isGradientValue(value string) bool {
	for _, fn := range [...]string{"linear-gradient(", "radial-gradient(", "conic-gradient(",
		"repeating-linear-gradient(", "repeating-radial-gradient(", "repeating-conic-gradient("} {
		if strings.HasPrefix(value, fn) {
			return true
		}
	}
	return false
}
