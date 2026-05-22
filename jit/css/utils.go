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

// twUnit converts Tailwind syntax to CSS values (e.g. 4 -> 1rem, [120px] -> 120px)
func twUnit(s string) string {
	if strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]") {
		return s[1 : len(s)-1]
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
	if isNumeric(s) {
		val, _ := strconv.ParseFloat(s, 64)
		if val == 0 {
			return "0px"
		}
		// 1 tw unit = 0.25rem
		return fmt.Sprintf("%grem", val*0.25)
	}
	return s
}

// twColor resolves Tailwind colors including arbitrary hex values
func twColor(colorName, shade string) string {
	if strings.HasPrefix(colorName, "[") && strings.HasSuffix(colorName, "]") {
		return colorName[1 : len(colorName)-1] // e.g. [#fcfcfd]
	}
	
	key := colorName
	if shade != "" {
		if colorName == "gray" && shade == "900" { key = "dark" }
		if colorName == "gray" && shade == "800" { key = "dark-lighter" }
		if colorName == "gray" && shade == "100" { key = "light" }
		if colorName == "gray" && shade == "200" { key = "light-darker" }
		// Fallback for demo
		if rgb, ok := Colors[key]; ok {
			return rgb.String()
		}
	}
	
	if rgb, ok := Colors[key]; ok {
		return rgb.String()
	}
	
	// Default fallbacks for common colors to avoid breaking compilation
	if colorName == "white" { return "255, 255, 255" }
	if colorName == "black" { return "0, 0, 0" }
	if colorName == "transparent" { return "transparent" }
	
	return ""
}
