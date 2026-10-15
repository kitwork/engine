package ui

import (
	"math"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/kitwork/engine/jit/hydrate"
)

// Format helpers an expression can call by name: {{ money(product.price) }}. Each has one meaning on
// every target; the client kernel carries the same functions so a live value prints the same way the
// server printed it (no flash, no locale drift between ends).

func formatHelpers() map[string]any {
	return map[string]any{
		"money":    hydrate.Func(func(args []any) any { return money(arg(args, 0), stringify(arg(args, 1))) }),
		"date":     hydrate.Func(func(args []any) any { return formatDate(arg(args, 0)) }),
		"initials": hydrate.Func(func(args []any) any { return initials(stringify(arg(args, 0))) }),
	}
}

func arg(args []any, i int) any {
	if i < len(args) {
		return args[i]
	}
	return nil
}

func number(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, !math.IsNaN(t)
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
		return f, err == nil
	}
	return 0, false
}

// money prints an amount: VND (default) as "29.000đ", USD as "$12.50".
func money(v any, currency string) string {
	amount, ok := number(v)
	if !ok {
		return ""
	}
	switch strings.ToUpper(currency) {
	case "USD":
		return "$" + groupThousands(strconv.FormatFloat(math.Abs(amount), 'f', 2, 64), ",", ".", amount < 0)
	default:
		return groupThousands(strconv.FormatFloat(math.Round(math.Abs(amount)), 'f', 0, 64), ".", ",", amount < 0) + "đ"
	}
}

func groupThousands(digits, thousands, decimal string, negative bool) string {
	whole, fraction := digits, ""
	if i := strings.IndexByte(digits, '.'); i >= 0 {
		whole, fraction = digits[:i], digits[i+1:]
	}
	var b strings.Builder
	if negative {
		b.WriteByte('-')
	}
	for i, c := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteString(thousands)
		}
		b.WriteRune(c)
	}
	if fraction != "" {
		b.WriteString(decimal)
		b.WriteString(fraction)
	}
	return b.String()
}

// formatDate prints a day as dd/mm/yyyy from an ISO-8601 string or a Unix time in milliseconds.
func formatDate(v any) string {
	var t time.Time
	switch x := v.(type) {
	case string:
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05", "2006-01-02"} {
			if parsed, err := time.Parse(layout, x); err == nil {
				t = parsed
				break
			}
		}
		if t.IsZero() {
			return x
		}
	default:
		ms, ok := number(v)
		if !ok {
			return ""
		}
		t = time.UnixMilli(int64(ms)).UTC()
	}
	return t.Format("02/01/2006")
}

// initials takes the first letter of the first and last words: "Huỳnh Nhân Quốc" → "HQ".
func initials(name string) string {
	words := strings.Fields(name)
	if len(words) == 0 {
		return ""
	}
	first := firstLetter(words[0])
	if len(words) == 1 {
		return first
	}
	return first + firstLetter(words[len(words)-1])
}

func firstLetter(word string) string {
	r, _ := utf8.DecodeRuneInString(word)
	if r == utf8.RuneError {
		return ""
	}
	return string(unicode.ToUpper(r))
}
