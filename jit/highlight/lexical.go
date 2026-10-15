package highlight

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// The scan* helpers are pure: they take the source and a start offset and
// return where the thing they read ends, never reading before start or past the
// end, and never failing. Nothing found means the end is start. Each one is a
// single forward pass, so a tokenizer built from them stays linear as long as it
// emits up to what a helper returned instead of scanning the same text again.

// byteAt is source[index], or 0 outside the source.
func byteAt(source string, index int) byte {
	if index < 0 || index >= len(source) {
		return 0
	}
	return source[index]
}

// isWhitespace is the whitespace the contract means: space, tab, line feed,
// carriage return, form feed, vertical tab. Not NUL, not no-break space.
func isWhitespace(character byte) bool {
	switch character {
	case ' ', '\t', '\n', '\r', '\f', '\v':
		return true
	}
	return false
}

// isAllWhitespace reports whether text is non-empty and only whitespace.
func isAllWhitespace(text string) bool {
	if text == "" {
		return false
	}
	for index := 0; index < len(text); index++ {
		if !isWhitespace(text[index]) {
			return false
		}
	}
	return true
}

func isDecimalDigit(character byte) bool { return character >= '0' && character <= '9' }
func isBinaryDigit(character byte) bool  { return character == '0' || character == '1' }
func isOctalDigit(character byte) bool   { return character >= '0' && character <= '7' }

func isHexadecimalDigit(character byte) bool {
	return isDecimalDigit(character) || (character >= 'a' && character <= 'f') || (character >= 'A' && character <= 'F')
}

func isASCIILetter(character byte) bool {
	return (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z')
}

// scanWhile reads bytes while accept says yes.
func scanWhile(source string, start int, accept func(byte) bool) int {
	index := clampStart(source, start)
	for index < len(source) && accept(source[index]) {
		index++
	}
	return index
}

// scanWhitespace reads a run of whitespace, line breaks included.
func scanWhitespace(source string, start int) int { return scanWhile(source, start, isWhitespace) }

// scanToLineEnd reads up to, not including, the next line break ("\n" or "\r").
// A line comment ends here, so "\r\n" stays outside the comment: a lone "\r"
// inside a coloured span would render as a line break of its own.
func scanToLineEnd(source string, start int) int {
	index := clampStart(source, start)
	if found := strings.IndexAny(source[index:], "\r\n"); found >= 0 {
		return index + found
	}
	return len(source)
}

// scanNewline reads one line break: "\r\n", "\n" or "\r".
func scanNewline(source string, start int) int {
	index := clampStart(source, start)
	if strings.HasPrefix(source[index:], "\r\n") {
		return index + 2
	}
	if index < len(source) && (source[index] == '\n' || source[index] == '\r') {
		return index + 1
	}
	return index
}

// scanUntil reads up to and including the first closer, or to the end of the
// source when there is none: an unterminated comment or string keeps its kind to
// the end rather than dropping the tail.
func scanUntil(source string, start int, closer string) int {
	index := clampStart(source, start)
	if closer == "" {
		return index
	}
	if found := strings.Index(source[index:], closer); found >= 0 {
		return index + found + len(closer)
	}
	return len(source)
}

// scanBlockComment reads a comment that opens at start with opener ("/*",
// "<!--", "<#") and runs to closer or to the end of the source. It does not nest.
// It returns start when the source does not open a comment there.
func scanBlockComment(source string, start int, opener, closer string) int {
	start = clampStart(source, start)
	if opener == "" || !strings.HasPrefix(source[start:], opener) {
		return start
	}
	return scanUntil(source, start+len(opener), closer)
}

// quoted describes how one kind of string ends.
type quoted struct {
	closer    string // what ends it; usually the opening quote, `"""` for Python
	escape    byte   // the escape character ('\\', PowerShell '`'), 0 for none
	doubled   bool   // a doubled closer is a literal one: SQL 'it''s', PowerShell 'it''s'
	multiline bool   // false: an unterminated string stops before the line break
}

// The strings most languages share.
var (
	doubleQuoted = quoted{closer: `"`, escape: '\\'}
	singleQuoted = quoted{closer: `'`, escape: '\\'}
)

// scanQuoted reads the body of a string whose opening delimiter ends just before
// start, through its closer. An escape character takes the character after it
// with it, a line break included (a line continuation). A single-line string
// with no closer stops before the line break, so one stray quote colours one
// line, not the rest of the file; a multi-line one runs to the end.
func scanQuoted(source string, start int, rules quoted) int {
	index := clampStart(source, start)
	length := len(source)
	if rules.closer == "" {
		return index
	}
	for index < length {
		character := source[index]
		if rules.escape != 0 && character == rules.escape {
			index++
			if strings.HasPrefix(source[index:], "\r\n") {
				index += 2
			} else if index < length {
				index++
			}
			continue
		}
		if strings.HasPrefix(source[index:], rules.closer) {
			index += len(rules.closer)
			if rules.doubled && strings.HasPrefix(source[index:], rules.closer) {
				index += len(rules.closer)
				continue
			}
			return index
		}
		if !rules.multiline && (character == '\n' || character == '\r') {
			return index
		}
		index++
	}
	return length
}

// scanNumber reads a numeric literal: decimal with fraction and exponent (7,
// 1.5, .5, 1e9, 2.5E-3), 0x hexadecimal (with a p exponent: 0x1p-3), 0b binary,
// 0o octal, underscores between digits (1_000_000), then any letters and digits
// glued to it as a suffix whose meaning the language owns (10n, 2i, 1.5f, 10px,
// 100L). A dot is part of the number only when a digit follows it, so 1.toString
// keeps its dot. It returns start when no number begins there: the first byte
// must be a digit, or a dot followed by a digit.
func scanNumber(source string, start int) int {
	index := clampStart(source, start)
	length := len(source)
	if index >= length {
		return index
	}
	if !isDecimalDigit(source[index]) && !(source[index] == '.' && isDecimalDigit(byteAt(source, index+1))) {
		return index
	}

	digit, exponent := isDecimalDigit, "eE"
	if source[index] == '0' {
		next, after := byteAt(source, index+1), byteAt(source, index+2)
		switch {
		case (next == 'x' || next == 'X') && (isHexadecimalDigit(after) || after == '.'):
			digit, exponent, index = isHexadecimalDigit, "pP", index+2
		case (next == 'b' || next == 'B') && isBinaryDigit(after):
			digit, exponent, index = isBinaryDigit, "", index+2
		case (next == 'o' || next == 'O') && isOctalDigit(after):
			digit, exponent, index = isOctalDigit, "", index+2
		}
	}
	digits := func(from int) int {
		return scanWhile(source, from, func(character byte) bool { return digit(character) || character == '_' })
	}

	index = digits(index)
	if byteAt(source, index) == '.' && digit(byteAt(source, index+1)) {
		index = digits(index + 1)
	}
	if exponent != "" && index < length && strings.IndexByte(exponent, source[index]) >= 0 {
		after := index + 1
		if sign := byteAt(source, after); sign == '+' || sign == '-' {
			after++
		}
		if isDecimalDigit(byteAt(source, after)) {
			index = scanWhile(source, after, func(character byte) bool { return isDecimalDigit(character) || character == '_' })
		}
	}
	return scanWhile(source, index, func(character byte) bool {
		return isASCIILetter(character) || isDecimalDigit(character) || character == '_'
	})
}

// scanIdentifier reads a name: a letter (any script) or '_' first, then letters,
// digits, combining marks and connector punctuation. extra adds ASCII bytes the
// language allows anywhere in a name, first byte included: "$" for JavaScript,
// "-" for CSS and tag names. Bytes that are not valid UTF-8 end the name. It
// returns start when no name begins there.
func scanIdentifier(source string, start int, extra string) int {
	index := clampStart(source, start)
	begin := index
	for index < len(source) {
		character := source[index]
		if character < utf8.RuneSelf {
			if isASCIILetter(character) || character == '_' ||
				(index > begin && isDecimalDigit(character)) ||
				strings.IndexByte(extra, character) >= 0 {
				index++
				continue
			}
			return index
		}
		letter, size := utf8.DecodeRuneInString(source[index:])
		if letter == utf8.RuneError {
			return index
		}
		if unicode.IsLetter(letter) ||
			(index > begin && (unicode.IsDigit(letter) || unicode.In(letter, unicode.Mn, unicode.Mc, unicode.Pc))) {
			index += size
			continue
		}
		return index
	}
	return index
}

// matchLongest returns the end of the longest candidate that the source holds at
// start ("===" before "=="), or start when none does.
func matchLongest(source string, start int, candidates []string) int {
	index := clampStart(source, start)
	best := index
	for _, candidate := range candidates {
		if candidate != "" && index+len(candidate) > best && strings.HasPrefix(source[index:], candidate) {
			best = index + len(candidate)
		}
	}
	return best
}

// wordSet is a set of keywords, types or constants.
type wordSet map[string]struct{}

func newWordSet(words ...string) wordSet {
	set := make(wordSet, len(words))
	for _, word := range words {
		set[word] = struct{}{}
	}
	return set
}

func (set wordSet) has(word string) bool {
	_, found := set[word]
	return found
}

// clampStart keeps a start offset inside [0, len(source)].
func clampStart(source string, start int) int {
	if start < 0 {
		return 0
	}
	if start > len(source) {
		return len(source)
	}
	return start
}
