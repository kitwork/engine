// Package htmlattr decodes authored HTML attribute values exactly as a browser
// exposes them through getAttribute. The standard library's html.UnescapeString
// uses the HTML text-context rules, whose handling of legacy named references
// differs when an ambiguous ampersand appears in an attribute.
package htmlattr

import (
	"html"
	"strings"
	"unicode/utf8"
)

const (
	longestLegacyName = 6
	longestNamedRef   = 32 // includes the optional semicolon
)

// These are the case-sensitive HTML named references that browsers may consume
// without a trailing semicolon. Attribute context leaves such a reference
// literal when the following byte is ASCII alphanumeric or '='.
var legacyNames = func() map[string]struct{} {
	names := strings.Fields(`
		AElig AMP Aacute Acirc Agrave Aring Atilde Auml COPY Ccedil ETH Eacute
		Ecirc Egrave Euml GT Iacute Icirc Igrave Iuml LT Ntilde Oacute Ocirc
		Ograve Oslash Otilde Ouml QUOT REG THORN Uacute Ucirc Ugrave Uuml Yacute
		aacute acirc acute aelig agrave amp aring atilde auml brvbar ccedil cedil
		cent copy curren deg divide eacute ecirc egrave eth euml frac12 frac14
		frac34 gt iacute icirc iexcl igrave iquest iuml laquo lt macr micro middot
		nbsp not ntilde oacute ocirc ograve ordf ordm oslash otilde ouml para
		plusmn pound quot raquo reg sect shy sup1 sup2 sup3 szlig thorn times uacute
		ucirc ugrave uml uuml yacute yen yuml
	`)
	set := make(map[string]struct{}, len(names))
	for _, name := range names {
		set[name] = struct{}{}
	}
	return set
}()

var numericReplacements = [...]rune{
	'\u20AC', '\u0081', '\u201A', '\u0192', '\u201E', '\u2026', '\u2020', '\u2021',
	'\u02C6', '\u2030', '\u0160', '\u2039', '\u0152', '\u008D', '\u017D', '\u008F',
	'\u0090', '\u2018', '\u2019', '\u201C', '\u201D', '\u2022', '\u2013', '\u2014',
	'\u02DC', '\u2122', '\u0161', '\u203A', '\u0153', '\u009D', '\u017E', '\u0178',
}

// Decode applies HTML input-stream newline/NUL preprocessing and character
// reference decoding for an attribute value. It intentionally accepts the raw
// bytes between the attribute quotes, not a complete tag.
func Decode(raw string) string {
	source := preprocess(raw)
	changed := false
	var decoded strings.Builder
	outputStart := 0
	searchFrom := 0
	for searchFrom < len(source) {
		next := strings.IndexByte(source[searchFrom:], '&')
		if next < 0 {
			break
		}
		amp := searchFrom + next
		if amp+1 < len(source) && source[amp+1] == '#' {
			value, end, matched := numericReference(source, amp)
			if !changed {
				changed = true
				decoded.Grow(len(source))
			}
			decoded.WriteString(html.UnescapeString(source[outputStart:amp]))
			if matched {
				decoded.WriteRune(value)
				outputStart = end
				searchFrom = end
			} else {
				decoded.WriteByte('&')
				outputStart = amp + 1
				searchFrom = amp + 1
			}
			continue
		}
		if ambiguousAmpersand(source, amp) {
			if !changed {
				changed = true
				decoded.Grow(len(source))
			}
			decoded.WriteString(html.UnescapeString(source[outputStart:amp]))
			decoded.WriteByte('&')
			outputStart = amp + 1
		}
		searchFrom = amp + 1
	}
	if !changed {
		return html.UnescapeString(source)
	}
	decoded.WriteString(html.UnescapeString(source[outputStart:]))
	return decoded.String()
}

func numericReference(source string, amp int) (rune, int, bool) {
	index := amp + 2
	if index > len(source) || amp+1 >= len(source) || source[amp+1] != '#' {
		return 0, amp, false
	}
	base := uint32(10)
	if index < len(source) && (source[index] == 'x' || source[index] == 'X') {
		base = 16
		index++
	}
	digits := index
	value := uint32(0)
	for index < len(source) {
		digit, ok := numericDigit(source[index], base)
		if !ok {
			break
		}
		// Once the value is outside Unicode, cap it instead of allowing an
		// integer overflow while still consuming the complete reference.
		if value <= utf8.MaxRune {
			value = value*base + digit
			if value > utf8.MaxRune {
				value = utf8.MaxRune + 1
			}
		}
		index++
	}
	if index == digits {
		return 0, amp, false
	}
	if index < len(source) && source[index] == ';' {
		index++
	}
	if value >= 0x80 && value <= 0x9f {
		value = uint32(numericReplacements[value-0x80])
	} else if value == 0 || value > utf8.MaxRune || value >= 0xd800 && value <= 0xdfff {
		value = utf8.RuneError
	}
	return rune(value), index, true
}

func numericDigit(character byte, base uint32) (uint32, bool) {
	if character >= '0' && character <= '9' {
		return uint32(character - '0'), true
	}
	if base == 16 && character >= 'a' && character <= 'f' {
		return uint32(character-'a') + 10, true
	}
	if base == 16 && character >= 'A' && character <= 'F' {
		return uint32(character-'A') + 10, true
	}
	return 0, false
}

func preprocess(raw string) string {
	if !strings.ContainsAny(raw, "\r\x00") && utf8.ValidString(raw) {
		return raw
	}
	var normalized strings.Builder
	normalized.Grow(len(raw))
	lastWasCR := false
	writeCharacter := func(character rune) {
		if character == '\n' && lastWasCR {
			lastWasCR = false
			return
		}
		lastWasCR = false
		switch character {
		case '\r':
			normalized.WriteByte('\n')
			lastWasCR = true
		case 0:
			normalized.WriteRune(utf8.RuneError)
		default:
			normalized.WriteRune(character)
		}
	}

	// Decode malformed UTF-8 with the Encoding Standard's replacement and
	// reconsume rules. In particular, a truncated sequence such as E2 82 emits
	// one replacement, while an invalid sequence such as E0 80 80 emits three.
	// Repeated utf8.DecodeRune calls would emit two replacements for the former
	// and would therefore disagree with the browser before HTML tokenization.
	var codePoint uint32
	bytesSeen := 0
	bytesNeeded := 0
	lowerBoundary := byte(0x80)
	upperBoundary := byte(0xbf)
	resetDecoder := func() {
		codePoint = 0
		bytesSeen = 0
		bytesNeeded = 0
		lowerBoundary = 0x80
		upperBoundary = 0xbf
	}
	for index := 0; index < len(raw); index++ {
		current := raw[index]
		if bytesNeeded == 0 {
			switch {
			case current <= 0x7f:
				writeCharacter(rune(current))
			case current >= 0xc2 && current <= 0xdf:
				bytesNeeded = 1
				codePoint = uint32(current & 0x1f)
			case current >= 0xe0 && current <= 0xef:
				bytesNeeded = 2
				codePoint = uint32(current & 0x0f)
				if current == 0xe0 {
					lowerBoundary = 0xa0
				} else if current == 0xed {
					upperBoundary = 0x9f
				}
			case current >= 0xf0 && current <= 0xf4:
				bytesNeeded = 3
				codePoint = uint32(current & 0x07)
				if current == 0xf0 {
					lowerBoundary = 0x90
				} else if current == 0xf4 {
					upperBoundary = 0x8f
				}
			default:
				writeCharacter(utf8.RuneError)
			}
			continue
		}

		if current < lowerBoundary || current > upperBoundary {
			writeCharacter(utf8.RuneError)
			resetDecoder()
			index-- // Reconsume the non-continuation byte as a new sequence.
			continue
		}
		lowerBoundary = 0x80
		upperBoundary = 0xbf
		codePoint = codePoint<<6 | uint32(current&0x3f)
		bytesSeen++
		if bytesSeen != bytesNeeded {
			continue
		}
		writeCharacter(rune(codePoint))
		resetDecoder()
	}
	if bytesNeeded != 0 {
		writeCharacter(utf8.RuneError)
	}
	return normalized.String()
}

func ambiguousAmpersand(source string, amp int) bool {
	nameStart := amp + 1
	if nameStart >= len(source) || source[nameStart] == '#' {
		return false
	}
	remaining := len(source) - nameStart
	if remaining > longestLegacyName {
		remaining = longestLegacyName
	}
	legacyEnd := -1
	for length := remaining; length > 0; length-- {
		if _, exists := legacyNames[source[nameStart:nameStart+length]]; exists {
			legacyEnd = nameStart + length
			break
		}
	}
	if legacyEnd < 0 || legacyEnd >= len(source) {
		return false
	}
	next := source[legacyEnd]
	if next == '=' {
		return true
	}
	if !asciiAlphaNumeric(next) {
		return false
	}

	// A longer, semicolon-terminated reference such as &notin; wins over the
	// legacy &not prefix. Ask the standard decoder only about that bounded
	// candidate; every named HTML replacement contains at most two code points.
	end := legacyEnd
	limit := nameStart + longestNamedRef
	if limit > len(source) {
		limit = len(source)
	}
	for end < limit && asciiAlphaNumeric(source[end]) {
		end++
	}
	if end < limit && source[end] == ';' {
		candidate := source[amp : end+1]
		value := html.UnescapeString(candidate)
		if value != candidate && utf8.RuneCountInString(value) <= 2 {
			return false
		}
	}
	return true
}

func asciiAlphaNumeric(character byte) bool {
	return character >= 'a' && character <= 'z' ||
		character >= 'A' && character <= 'Z' ||
		character >= '0' && character <= '9'
}

// ClassTokens splits a class attribute into the names a JIT engine should emit for.
//
// A template branch inside the attribute is ordinary authoring —
//
//	class="fixed left-0 {{ if macos }}right-0{{ else }}right-36{{ end }}"
//
// — but splitting on whitespace alone yields the token "}}right-0{{", which matches no utility and
// no icon. The class is in the markup, the page renders it, and the stylesheet simply does not have
// it: the styling disappears with no error anywhere. Worse, only the names TOUCHING a delimiter are
// lost, so the attribute keeps working for every other class and the gap reads as a design mistake
// rather than a missing rule.
//
// Both branches are collected, deliberately — the same rule data-kit-class already follows: a
// stylesheet must hold every branch, not whichever one this request happened to take. The template
// keywords left behind (if, else, end, and the tested value) match nothing, as they already did.
func ClassTokens(value string) []string {
	return strings.Fields(templateDelimiters.Replace(value))
}

var templateDelimiters = strings.NewReplacer("{{", " ", "}}", " ")
