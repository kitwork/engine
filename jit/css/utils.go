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
		return fmt.Sprintf("--tw-gradient-from: %s; --tw-gradient-stops: var(--tw-gradient-from), var(--tw-gradient-to, transparent);", color)
	case "via":
		return fmt.Sprintf("--tw-gradient-stops: var(--tw-gradient-from), %s, var(--tw-gradient-to, transparent);", color)
	case "to":
		return fmt.Sprintf("--tw-gradient-to: %s;", color)
	}
	return ""
}

// unarb unwraps a Tailwind arbitrary value: "[1fr_2fr]" -> "1fr 2fr" (underscores become
// spaces, per Tailwind). Non-arbitrary input is returned unchanged.
func unarb(s string) string {
	if strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]") {
		return strings.ReplaceAll(s[1:len(s)-1], "_", " ")
	}
	return s
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

// twColor resolves a Tailwind color to an "R, G, B" string (or hex for arbitrary /
// transparent passthrough). Lookup order: arbitrary [..] → base keywords → the
// Tailwind v3 palette (family-shade, see twpalette.go) → the custom Colors map.
func twColor(colorName, shade string) string {
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

	// Custom design-system colors (brand, kitwork, primary, …) — shade ignored.
	if rgb, ok := Colors[colorName]; ok {
		return rgb.String()
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
