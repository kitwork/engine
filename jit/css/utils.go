package css

import (
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
