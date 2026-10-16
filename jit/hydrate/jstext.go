package hydrate

import (
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// Text as JavaScript counts it (16/10/2026, the owner's decision that a .ui expression answers as
// JavaScript does on both ends): a string is a run of UTF-16 code units, so '😀'.length is 2, '😀'[0]
// is the lone half '\ud83d', and two strings compare unit by unit. A Go string holds UTF-8, which has
// no lone half of a pair, so the server keeps one the way WTF-8 does: the three bytes UTF-8 would give
// the half if it were a character (ED A0 80 to ED BF BF). Joined back to its other half it is the
// character again; written to the page it is U+FFFD, as the browser shows it (pageText).

// units is s as UTF-16 code units, a lone half of a pair kept as itself.
func units(s string) []uint16 {
	out := make([]uint16, 0, len(s))
	for i := 0; i < len(s); {
		c := s[i]
		if c < utf8.RuneSelf {
			out = append(out, uint16(c))
			i++
			continue
		}
		if half, ok := loneHalf(s[i:]); ok {
			out = append(out, half)
			i += 3
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		i += size
		if r >= 0x10000 {
			a, b := utf16.EncodeRune(r)
			out = append(out, uint16(a), uint16(b))
			continue
		}
		out = append(out, uint16(r))
	}
	return out
}

// loneHalf reads the three bytes that keep a lone half of a pair at the start of s.
func loneHalf(s string) (uint16, bool) {
	if len(s) < 3 || s[0] != 0xED || s[1] < 0xA0 || s[1] > 0xBF || s[2] < 0x80 || s[2] > 0xBF {
		return 0, false
	}
	return 0xD000 | uint16(s[1]&0x3F)<<6 | uint16(s[2]&0x3F), true
}

// fromUnits is the string of UTF-16 code units: a pair joins into its character, a lone half is kept.
func fromUnits(u []uint16) string {
	var b strings.Builder
	b.Grow(len(u))
	for i := 0; i < len(u); i++ {
		c := u[i]
		switch {
		case c < utf8.RuneSelf:
			b.WriteByte(byte(c))
		case c >= 0xD800 && c <= 0xDBFF && i+1 < len(u) && u[i+1] >= 0xDC00 && u[i+1] <= 0xDFFF:
			b.WriteRune(utf16.DecodeRune(rune(c), rune(u[i+1])))
			i++
		case c >= 0xD800 && c <= 0xDFFF:
			b.WriteByte(0xED)
			b.WriteByte(byte(0x80 | (c>>6)&0x3F))
			b.WriteByte(byte(0x80 | c&0x3F))
		default:
			b.WriteRune(rune(c))
		}
	}
	return b.String()
}

// ascii reports whether s is all ASCII: one byte is one code unit.
func ascii(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// length16 is s.length: its UTF-16 code units.
func length16(s string) int {
	if ascii(s) {
		return len(s)
	}
	return len(units(s))
}

// unitString is the one code unit c as a string.
func unitString(c uint16) string { return fromUnits([]uint16{c}) }

// concat16 joins two strings; a lone high half at the end of a and a lone low half at the start of b
// become the one character they make, as JavaScript's strings simply sit side by side.
func concat16(a, b string) string {
	if len(a) >= 3 && len(b) >= 3 {
		if hi, ok := loneHalf(a[len(a)-3:]); ok && hi <= 0xDBFF {
			if lo, ok := loneHalf(b); ok && lo >= 0xDC00 {
				return a[:len(a)-3] + string(utf16.DecodeRune(rune(hi), rune(lo))) + b[3:]
			}
		}
	}
	return a + b
}

// join16 joins strings as concat16 does, pair by pair.
func join16(parts []string, sep string) string {
	if len(parts) == 0 {
		return ""
	}
	out := parts[0]
	for _, p := range parts[1:] {
		out = concat16(concat16(out, sep), p)
	}
	return out
}

// compare16 orders two strings by their UTF-16 code units, as JavaScript's < does: '😀' < 'ｚ' is
// true there (U+D83D before U+FF5A) where UTF-8's byte order puts the emoji last.
func compare16(a, b string) int {
	if ascii(a) && ascii(b) {
		return strings.Compare(a, b)
	}
	ua, ub := units(a), units(b)
	for i := 0; i < len(ua) && i < len(ub); i++ {
		if ua[i] != ub[i] {
			if ua[i] < ub[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(ua) < len(ub):
		return -1
	case len(ua) > len(ub):
		return 1
	}
	return 0
}

// pageText is s as the page shows it: a lone half of a pair is U+FFFD, one for each, as the browser
// draws a lone half; anything else as it is.
func pageText(s string) string {
	if !strings.Contains(s, "\xED") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		if _, ok := loneHalf(s[i:]); ok {
			b.WriteString("�")
			i += 3
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// PageText is s with each lone half of a UTF-16 pair as U+FFFD: the text a page writes of a string an
// expression made ('😀'[0] holds one), as the browser shows it.
func PageText(s string) string { return pageText(s) }

// isJSSpace reports JavaScript's WhiteSpace and LineTerminator, what trim drops and Number skips:
// tab, vertical tab, form feed, space, no-break space, the byte order mark, every space separator
// (Zs) and the four line terminators. U+0085 and U+180E are not among them, as in JavaScript.
func isJSSpace(c uint16) bool {
	switch c {
	case 0x09, 0x0A, 0x0B, 0x0C, 0x0D, 0x20, 0xA0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000, 0xFEFF:
		return true
	}
	return c >= 0x2000 && c <= 0x200A
}

// trim16 drops JavaScript's white space from the start, the end or both.
func trim16(s string, start, end bool) string {
	u := units(s)
	i, j := 0, len(u)
	if start {
		for i < j && isJSSpace(u[i]) {
			i++
		}
	}
	if end {
		for j > i && isJSSpace(u[j-1]) {
			j--
		}
	}
	if i == 0 && j == len(u) {
		return s
	}
	return fromUnits(u[i:j])
}

// indexOf16 is the first place at or after from where sub starts in s, in code units, or -1.
func indexOf16(s, sub []uint16, from int) int {
	if from < 0 {
		from = 0
	}
	for i := from; i+len(sub) <= len(s); i++ {
		if equalUnits(s[i:i+len(sub)], sub) {
			return i
		}
	}
	return -1
}

// lastIndexOf16 is the last place at or before from where sub starts in s, or -1.
func lastIndexOf16(s, sub []uint16, from int) int {
	if from > len(s)-len(sub) {
		from = len(s) - len(sub)
	}
	for i := from; i >= 0; i-- {
		if equalUnits(s[i:i+len(sub)], sub) {
			return i
		}
	}
	return -1
}

func equalUnits(a, b []uint16) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// QuoteText is s as the inside of a quoted string of the expression grammar, read back by either end
// as s: a backslash and a quote escaped, a carriage return and NUL escaped (a page's HTML would make
// them a line feed and U+FFFD), and a lone half of a UTF-16 pair as \uXXXX (UTF-8 has no form for it).
func QuoteText(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if half, lone := loneHalf(s[i:]); lone {
			b.WriteString(`\u`)
			b.WriteString(strings.ToUpper(strconv.FormatUint(uint64(half), 16)))
			i += 3
			continue
		}
		switch c := s[i]; c {
		case '\\':
			b.WriteString(`\\`)
		case '\'':
			b.WriteString(`\'`)
		case '\r':
			b.WriteString(`\r`)
		case 0:
			b.WriteString(`\0`)
		default:
			b.WriteByte(c)
		}
		i++
	}
	return b.String()
}
