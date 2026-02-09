package value

import (
	"fmt"
	"strings"
)

// --- Date Formatting Helper ---

// Strftime to Go Layout Converter
func convertStrftime(format string) string {
	// Common replacements
	r := strings.NewReplacer(
		"%Y", "2006",
		"%y", "06",
		"%m", "01",
		"%d", "02",
		"%H", "15",
		"%I", "03",
		"%M", "04",
		"%S", "05",
		"%p", "PM",
		"%a", "Mon",
		"%A", "Monday",
		"%b", "Jan",
		"%B", "January",
	)
	return r.Replace(format)
}

// --- Numeric Formatting Helper ---

// Standard Numeric Formatting (SQL-Like Pattern)
// Pattern Examples:
// "?,??0.00"  -> 1,234.56
// "?.??0,00"  -> 1.234,56 (European)
// "? ???"     -> 1 234
func formatNumericPattern(n float64, pattern string) string {
	// 1. Analyze Decimal Part
	// Detect based on suffix: .00 or .0 or .?? -> Dot Decimal
	// ,00 or ,0 or ,?? -> Comma Decimal

	decSep := ""
	groupSep := ""
	decimals := 0

	var intPat, decPat string
	hasDotDec := false
	hasCommaDec := false
	decIdx := -1

	// Check for Dot Decimal
	if idx := strings.LastIndex(pattern, "."); idx != -1 && idx < len(pattern)-1 {
		suffix := pattern[idx+1:]
		if strings.ContainsAny(suffix, "0?") && !strings.ContainsAny(suffix, ",.") {
			hasDotDec = true
			decIdx = idx
			decSep = "."
		}
	}

	// Check for Comma Decimal
	if !hasDotDec {
		if idx := strings.LastIndex(pattern, ","); idx != -1 && idx < len(pattern)-1 {
			suffix := pattern[idx+1:]
			if strings.ContainsAny(suffix, "0?") && !strings.ContainsAny(suffix, ",.") {
				hasCommaDec = true
				decIdx = idx
				decSep = ","
			}
		}
	}

	if hasDotDec || hasCommaDec {
		intPat = pattern[:decIdx]
		decPat = pattern[decIdx+1:]
	} else {
		intPat = pattern
	}

	// Count decimals
	decimals = len(decPat)

	// Determine Group Separator from Int Part
	if strings.Contains(intPat, ",") && decSep != "," {
		groupSep = ","
	} else if strings.Contains(intPat, ".") && decSep != "." {
		groupSep = "."
	} else if strings.Contains(intPat, " ") {
		groupSep = " "
	} else if strings.Contains(intPat, "'") {
		groupSep = "'"
	}

	// 2. Format Value
	// Round and stringify absolute value
	// Handle Sign
	isNeg := n < 0
	if isNeg {
		n = -n
	}

	// Format basic fixed point using Go's rounding
	baseFormat := fmt.Sprintf("%%.%df", decimals)
	s := fmt.Sprintf(baseFormat, n) // Always dot separated: "1234.56"

	// Split Go result
	parts := strings.Split(s, ".")
	integer := parts[0]
	fraction := ""
	if len(parts) > 1 {
		fraction = parts[1]
	}

	// 3. Apply Grouping to Integer part
	if groupSep != "" {
		var b strings.Builder
		count := 0
		// Loop backwards
		for i := len(integer) - 1; i >= 0; i-- {
			if count == 3 && i >= 0 {
				b.WriteString(groupSep)
				count = 0
			}
			b.WriteByte(integer[i])
			count++
		}

		// Reverse builder result
		grouped := b.String()
		runes := []rune(grouped)
		for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
			runes[i], runes[j] = runes[j], runes[i]
		}
		integer = string(runes)
	}

	// 4. Construct Final Result
	res := integer
	if decimals > 0 {
		res += decSep + fraction
	}

	if isNeg {
		res = "-" + res
	}

	return res
}
