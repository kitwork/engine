package hydrate

import (
	"math"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

// maxStringLength is the longest string V8 makes (2^29 - 24 code units): past it a method throws
// RangeError: Invalid string length, on both ends.
const maxStringLength = 1<<29 - 24

// serverStringLimit is the longest string the server builds for one expression (16M code units): what
// would be longer is refused, where the browser could go on to maxStringLength — a page's first paint
// is not where a string of hundreds of megabytes belongs.
const serverStringLimit = 1 << 24

var errTooLong = rangeError("Invalid string length")

// thisString is RequireObjectCoercible(this) then ToString(this), as every String.prototype method
// begins.
func (m *machine) thisString(f *frame, this any, name string) (string, error) {
	if nullish(this) {
		return "", typeError("String.prototype." + name + " called on null or undefined")
	}
	return m.toString(f, this)
}

// stringMethod is a method of String.prototype, as V8 runs it.
func (m *machine) stringMethod(f *frame, name string, this any, args []any) (any, error) {
	if name == "toString" || name == "valueOf" {
		s, ok := this.(string)
		if !ok {
			return nil, typeError("String.prototype." + name + " requires that 'this' be a String")
		}
		return s, nil
	}
	s, err := m.thisString(f, this, name)
	if err != nil {
		return nil, err
	}
	textArg := func(i int) (string, error) { return m.toString(f, arg(args, i)) }
	switch name {
	case "anchor", "fontcolor", "fontsize", "link":
		attribute := map[string]string{"anchor": "name", "fontcolor": "color", "fontsize": "size", "link": "href"}[name]
		tag := map[string]string{"anchor": "a", "fontcolor": "font", "fontsize": "font", "link": "a"}[name]
		v, err := textArg(0)
		if err != nil {
			return nil, err
		}
		return "<" + tag + " " + attribute + "=\"" + strings.ReplaceAll(v, "\"", "&quot;") + "\">" + s + "</" + tag + ">", nil
	case "big", "blink", "bold", "fixed", "italics", "small", "strike", "sub", "sup":
		tag := map[string]string{"big": "big", "blink": "blink", "bold": "b", "fixed": "tt", "italics": "i",
			"small": "small", "strike": "strike", "sub": "sub", "sup": "sup"}[name]
		return "<" + tag + ">" + s + "</" + tag + ">", nil
	case "at", "charAt", "charCodeAt", "codePointAt":
		pos, err := m.toIntegerOrInfinity(f, arg(args, 0))
		if err != nil {
			return nil, err
		}
		u := units(s)
		if name == "at" && pos < 0 {
			pos += float64(len(u))
		}
		if pos < 0 || pos >= float64(len(u)) {
			switch name {
			case "charAt":
				return "", nil
			case "charCodeAt":
				return math.NaN(), nil
			}
			return Undefined, nil
		}
		i := int(pos)
		switch name {
		case "charCodeAt":
			return float64(u[i]), nil
		case "codePointAt":
			if u[i] >= 0xD800 && u[i] <= 0xDBFF && i+1 < len(u) && u[i+1] >= 0xDC00 && u[i+1] <= 0xDFFF {
				return float64(utf16.DecodeRune(rune(u[i]), rune(u[i+1]))), nil
			}
			return float64(u[i]), nil
		}
		return unitString(u[i]), nil
	case "concat":
		out := s
		for i := range args {
			v, err := textArg(i)
			if err != nil {
				return nil, err
			}
			out = concat16(out, v)
			if len(out) > serverStringLimit {
				return nil, errTooLong
			}
		}
		return out, nil
	case "endsWith", "startsWith", "includes":
		search, err := textArg(0)
		if err != nil {
			return nil, err
		}
		u, su := units(s), units(search)
		if name == "endsWith" {
			end := len(u)
			if !isUndefined(arg(args, 1)) {
				n, err := m.toIntegerOrInfinity(f, arg(args, 1))
				if err != nil {
					return nil, err
				}
				end = int(math.Max(0, math.Min(n, float64(len(u)))))
			}
			start := end - len(su)
			return start >= 0 && equalUnits(u[start:end], su), nil
		}
		n, err := m.toIntegerOrInfinity(f, arg(args, 1))
		if err != nil {
			return nil, err
		}
		start := int(math.Max(0, math.Min(n, float64(len(u)))))
		if name == "startsWith" {
			return start+len(su) <= len(u) && equalUnits(u[start:start+len(su)], su), nil
		}
		return indexOf16(u, su, start) >= 0, nil
	case "indexOf":
		search, err := textArg(0)
		if err != nil {
			return nil, err
		}
		n, err := m.toIntegerOrInfinity(f, arg(args, 1))
		if err != nil {
			return nil, err
		}
		u := units(s)
		start := int(math.Max(0, math.Min(n, float64(len(u)))))
		return float64(indexOf16(u, units(search), start)), nil
	case "lastIndexOf":
		search, err := textArg(0)
		if err != nil {
			return nil, err
		}
		n, err := m.toNumber(f, arg(args, 1))
		if err != nil {
			return nil, err
		}
		pos := math.Inf(1)
		if n == n {
			pos = value0(n)
		}
		u, su := units(s), units(search)
		start := int(math.Max(0, math.Min(pos, float64(len(u)))))
		return float64(lastIndexOf16(u, su, start)), nil
	case "isWellFormed":
		return !strings.Contains(s, "\xED") || wellFormed(s), nil
	case "toWellFormed":
		if wellFormed(s) {
			return s, nil
		}
		u := units(s)
		for i := 0; i < len(u); i++ {
			if u[i] >= 0xD800 && u[i] <= 0xDBFF && i+1 < len(u) && u[i+1] >= 0xDC00 && u[i+1] <= 0xDFFF {
				i++
				continue
			}
			if u[i] >= 0xD800 && u[i] <= 0xDFFF {
				u[i] = 0xFFFD
			}
		}
		return fromUnits(u), nil
	case "localeCompare", "toLocaleUpperCase", "toLocaleLowerCase":
		// the visitor's language decides these (localeCompare's order, a Turkish dotless i): the
		// browser answers them
		for i := range args {
			if _, err := textArg(i); err != nil {
				return nil, err
			}
		}
		return Undefined, nil
	case "match", "matchAll", "search":
		return m.regexpMethod(f, name, s, args)
	case "normalize":
		form := "NFC"
		if !isUndefined(arg(args, 0)) {
			v, err := textArg(0)
			if err != nil {
				return nil, err
			}
			form = v
		}
		switch form {
		case "NFC", "NFD", "NFKC", "NFKD":
		default:
			return nil, rangeError("The normalization form should be one of NFC, NFD, NFKC, NFKD.")
		}
		return normalize(s, form), nil
	case "padEnd", "padStart":
		n, err := m.toLength(f, arg(args, 0))
		if err != nil {
			return nil, err
		}
		u := units(s)
		if n <= float64(len(u)) {
			return s, nil
		}
		filler := " "
		if !isUndefined(arg(args, 1)) {
			filler, err = textArg(1)
			if err != nil {
				return nil, err
			}
		}
		if filler == "" {
			return s, nil
		}
		if n > maxStringLength {
			return nil, errTooLong
		}
		if n > serverStringLimit {
			return nil, errTooLong
		}
		fu := units(filler)
		fill := make([]uint16, int(n)-len(u))
		for i := range fill {
			fill[i] = fu[i%len(fu)]
		}
		if name == "padStart" {
			return fromUnits(append(fill, u...)), nil
		}
		return fromUnits(append(u, fill...)), nil
	case "repeat":
		n, err := m.toIntegerOrInfinity(f, arg(args, 0))
		if err != nil {
			return nil, err
		}
		if n < 0 || math.IsInf(n, 1) {
			return nil, rangeError("Invalid count value: " + primitiveText(n))
		}
		if n == 0 || s == "" {
			return "", nil
		}
		total := n * float64(length16(s))
		if total > maxStringLength || total > serverStringLimit {
			return nil, errTooLong
		}
		if strings.Contains(s, "\xED") {
			// a lone half at each end may meet its other half across a repeat
			u := units(s)
			out := make([]uint16, 0, len(u)*int(n))
			for i := 0; i < int(n); i++ {
				out = append(out, u...)
			}
			return fromUnits(out), nil
		}
		return strings.Repeat(s, int(n)), nil
	case "replace", "replaceAll":
		return m.replace(f, name == "replaceAll", s, args)
	case "slice":
		u := units(s)
		start, err := m.relativeIndex(f, arg(args, 0), len(u), 0)
		if err != nil {
			return nil, err
		}
		end, err := m.relativeIndex(f, arg(args, 1), len(u), len(u))
		if err != nil {
			return nil, err
		}
		if start >= end {
			return "", nil
		}
		return fromUnits(u[start:end]), nil
	case "split":
		return m.split(f, s, args)
	case "substr":
		u := units(s)
		start, err := m.toIntegerOrInfinity(f, arg(args, 0))
		if err != nil {
			return nil, err
		}
		size := float64(len(u))
		switch {
		case math.IsInf(start, -1):
			start = 0
		case start < 0:
			start = math.Max(size+start, 0)
		default:
			start = math.Min(start, size)
		}
		length := size
		if !isUndefined(arg(args, 1)) {
			length, err = m.toIntegerOrInfinity(f, arg(args, 1))
			if err != nil {
				return nil, err
			}
		}
		length = math.Min(math.Max(length, 0), size)
		end := math.Min(start+length, size)
		if start >= end {
			return "", nil
		}
		return fromUnits(u[int(start):int(end)]), nil
	case "substring":
		u := units(s)
		a, err := m.toIntegerOrInfinity(f, arg(args, 0))
		if err != nil {
			return nil, err
		}
		b := float64(len(u))
		if !isUndefined(arg(args, 1)) {
			b, err = m.toIntegerOrInfinity(f, arg(args, 1))
			if err != nil {
				return nil, err
			}
		}
		start := math.Min(math.Max(a, 0), float64(len(u)))
		end := math.Min(math.Max(b, 0), float64(len(u)))
		if start > end {
			start, end = end, start
		}
		return fromUnits(u[int(start):int(end)]), nil
	case "toLowerCase":
		return lowerCase(s), nil
	case "toUpperCase":
		return upperCase(s), nil
	case "trim":
		return trim16(s, true, true), nil
	case "trimStart":
		return trim16(s, true, false), nil
	case "trimEnd":
		return trim16(s, false, true), nil
	}
	return nil, typeError("String.prototype." + name + " is not a function")
}

// value0 is n without its fraction, -0 as 0.
func value0(n float64) float64 { return math.Trunc(n) + 0 }

// toLength is ToLength(v): an integer from 0 to 2^53 - 1.
func (m *machine) toLength(f *frame, v any) (float64, error) {
	n, err := m.toIntegerOrInfinity(f, v)
	if err != nil {
		return 0, err
	}
	return math.Min(math.Max(n, 0), 9007199254740991), nil
}

// wellFormed reports a string with no lone half of a UTF-16 pair.
func wellFormed(s string) bool {
	for i := 0; i < len(s); i++ {
		if _, lone := loneHalf(s[i:]); lone {
			return false
		}
	}
	return true
}

// split is String.prototype.split with a separator that is text, as the grammar has no other.
func (m *machine) split(f *frame, s string, args []any) (any, error) {
	limit := uint32(math.MaxUint32)
	if !isUndefined(arg(args, 1)) {
		n, err := m.toNumber(f, arg(args, 1))
		if err != nil {
			return nil, err
		}
		limit = toUint32(n)
	}
	var sep string
	separator := arg(args, 0)
	if !isUndefined(separator) {
		var err error
		sep, err = m.toString(f, separator)
		if err != nil {
			return nil, err
		}
	}
	if limit == 0 {
		return newList(0), nil
	}
	if isUndefined(separator) {
		out := newList(1)
		out[0] = s
		return out, nil
	}
	u, su := units(s), units(sep)
	if len(su) == 0 {
		n := len(u)
		if uint32(n) > limit {
			n = int(limit)
		}
		out := newList(n)
		for i := 0; i < n; i++ {
			out[i] = unitString(u[i])
		}
		return out, nil
	}
	if len(u) == 0 {
		out := newList(1)
		out[0] = s
		return out, nil
	}
	out := newList(0)
	p := 0
	for q := 0; q+len(su) <= len(u); {
		if equalUnits(u[q:q+len(su)], su) {
			out = append(out, fromUnits(u[p:q]))
			if uint32(len(out)) == limit {
				return out, nil
			}
			q += len(su)
			p = q
			continue
		}
		q++
	}
	return append(out, fromUnits(u[p:])), nil
}

// toUint32 is ToUint32 of a number.
func toUint32(n float64) uint32 {
	if n != n || math.IsInf(n, 0) {
		return 0
	}
	n = math.Mod(math.Trunc(n), 1<<32)
	if n < 0 {
		n += 1 << 32
	}
	return uint32(n)
}

// replace is replace and replaceAll with a pattern that is text: the first or every place it is, each
// replaced by the replacement's $ patterns or by what a function given there returns.
func (m *machine) replace(f *frame, all bool, s string, args []any) (any, error) {
	search, err := m.toString(f, arg(args, 0))
	if err != nil {
		return nil, err
	}
	replacement := arg(args, 1)
	functional := callable(replacement)
	var template string
	if !functional {
		template, err = m.toString(f, replacement)
		if err != nil {
			return nil, err
		}
	}
	u, su := units(s), units(search)
	var positions []int
	advance := max(1, len(su))
	for pos := indexOf16(u, su, 0); pos >= 0; pos = indexOf16(u, su, pos+advance) {
		positions = append(positions, pos)
		if !all {
			break
		}
		if pos+advance > len(u) {
			break
		}
	}
	if len(positions) == 0 {
		return s, nil
	}
	var b []uint16
	end := 0
	for _, pos := range positions {
		var text string
		if functional {
			out, err := m.callValue(f, replacement, Undefined, []any{search, float64(pos), s})
			if err != nil {
				return nil, err
			}
			text, err = m.toString(f, out)
			if err != nil {
				return nil, err
			}
		} else {
			text = fromUnits(substitution(su, u, pos, units(template)))
		}
		b = append(b, u[end:pos]...)
		b = append(b, units(text)...)
		end = pos + len(su)
		if len(b) > serverStringLimit {
			return nil, errTooLong
		}
	}
	b = append(b, u[end:]...)
	return fromUnits(b), nil
}

// substitution is GetSubstitution with no captures: $$ is $, $& the match, $` what comes before it,
// $' what comes after; anything else as written.
func substitution(matched, str []uint16, position int, template []uint16) []uint16 {
	var out []uint16
	tail := position + len(matched)
	for i := 0; i < len(template); i++ {
		c := template[i]
		if c != '$' || i+1 >= len(template) {
			out = append(out, c)
			continue
		}
		switch template[i+1] {
		case '$':
			out = append(out, '$')
			i++
		case '&':
			out = append(out, matched...)
			i++
		case '`':
			out = append(out, str[:position]...)
			i++
		case '\'':
			if tail < len(str) {
				out = append(out, str[tail:]...)
			}
			i++
		default:
			out = append(out, c)
		}
	}
	return out
}

// upperCase and lowerCase are JavaScript's toUpperCase and toLowerCase: the full mapping of each
// character (jscase.go), Σ at the end of a word ς.
func upperCase(s string) string {
	if ascii(s) {
		return strings.ToUpper(s)
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		if _, lone := loneHalf(s[i:]); lone {
			b.WriteString(s[i : i+3])
			i += 3
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if mapped, ok := caseUpper[r]; ok {
			b.WriteString(mapped)
		} else {
			b.WriteString(s[i : i+size])
		}
		i += size
	}
	return b.String()
}

func lowerCase(s string) string {
	if ascii(s) {
		return strings.ToLower(s)
	}
	runes := []rune{}
	lone := map[int]string{}
	for i := 0; i < len(s); {
		if _, ok := loneHalf(s[i:]); ok {
			lone[len(runes)] = s[i : i+3]
			runes = append(runes, -1)
			i += 3
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		runes = append(runes, r)
		i += size
	}
	var b strings.Builder
	for i, r := range runes {
		switch {
		case r == -1:
			b.WriteString(lone[i])
		case r == 'Σ' && finalSigma(runes, i):
			b.WriteRune('ς')
		default:
			if mapped, ok := caseLower[r]; ok {
				b.WriteString(mapped)
			} else {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}

// finalSigma is Unicode's Final_Sigma: a cased letter before, skipping case-ignorable ones, and none
// after.
func finalSigma(runes []rune, at int) bool {
	before := false
	for i := at - 1; i >= 0; i-- {
		if caseIgnorable(runes[i]) {
			continue
		}
		before = cased(runes[i])
		break
	}
	if !before {
		return false
	}
	for i := at + 1; i < len(runes); i++ {
		if caseIgnorable(runes[i]) {
			continue
		}
		return !cased(runes[i])
	}
	return true
}

func cased(r rune) bool {
	return r >= 0 && (unicode.IsUpper(r) || unicode.IsLower(r) || unicode.IsTitle(r) ||
		unicode.Is(unicode.Other_Lowercase, r) || unicode.Is(unicode.Other_Uppercase, r))
}

func caseIgnorable(r rune) bool {
	if r < 0 {
		return false
	}
	switch r {
	case '\'', '.', ':', '^', '`', 0x00A8, 0x00AD, 0x00AF, 0x00B4, 0x00B7, 0x00B8, 0x0387, 0x055F, 0x05F4,
		0x2018, 0x2019, 0x2024, 0x2027, 0xFE13, 0xFE52, 0xFE55, 0xFF07, 0xFF0E, 0xFF1A:
		return true
	}
	return unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf, unicode.Lm, unicode.Sk)
}

// stringStatic is String.fromCharCode, String.fromCodePoint and String.raw.
func (m *machine) stringStatic(f *frame, name string, args []any) (any, error) {
	switch name {
	case "fromCharCode":
		u := make([]uint16, len(args))
		for i, a := range args {
			n, err := m.toNumber(f, a)
			if err != nil {
				return nil, err
			}
			u[i] = uint16(toUint32(n))
		}
		return fromUnits(u), nil
	case "fromCodePoint":
		var u []uint16
		for _, a := range args {
			n, err := m.toNumber(f, a)
			if err != nil {
				return nil, err
			}
			if n != math.Trunc(n) || n < 0 || n > 0x10FFFF || math.IsInf(n, 0) {
				text, _ := m.toString(f, a)
				return nil, rangeError("Invalid code point " + text)
			}
			r := rune(n)
			if r >= 0x10000 {
				a, b := utf16.EncodeRune(r)
				u = append(u, uint16(a), uint16(b))
			} else {
				u = append(u, uint16(r))
			}
		}
		return fromUnits(u), nil
	case "raw":
		cooked := arg(args, 0)
		if nullish(cooked) {
			return nil, typeError("Cannot convert undefined or null to object")
		}
		raw, err := m.get(f, cooked, "raw")
		if err != nil {
			return nil, err
		}
		if nullish(raw) {
			return nil, typeError("Cannot convert undefined or null to object")
		}
		lengthValue, err := m.get(f, raw, "length")
		if err != nil {
			return nil, err
		}
		count, err := m.toLength(f, lengthValue)
		if err != nil {
			return nil, err
		}
		out := ""
		for i := 0; float64(i) < count; i++ {
			part, err := m.get(f, raw, primitiveText(float64(i)))
			if err != nil {
				return nil, err
			}
			text, err := m.toString(f, part)
			if err != nil {
				return nil, err
			}
			out = concat16(out, text)
			if float64(i+1) == count {
				break
			}
			if i+1 < len(args) {
				sub, err := m.toString(f, args[i+1])
				if err != nil {
					return nil, err
				}
				out = concat16(out, sub)
			}
			if len(out) > serverStringLimit {
				return nil, errTooLong
			}
		}
		return out, nil
	}
	return nil, typeError("String." + name + " is not a function")
}
