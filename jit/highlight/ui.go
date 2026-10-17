package highlight

import "strings"

// tokenizeUI reads Kitwork's .ui markup the way engine/ui reads it: html's
// reader with .ui's differences.
//
//   - {{ … }} in text and inside attribute values is a javascript expression:
//     the braces are punctuation, what sits between them is handed to the
//     javascript tokenizer. The closing }} is the first one outside a quoted
//     string, as engine/ui finds it, so {{ "}}" }} is one expression. \{{ writes
//     {{ itself. An expression never closed runs to the end of its value, or of
//     the source in text.
//   - Element names are letters, digits, '-' and '.' (<product-card>).
//   - An attribute name says when and what: conditions before ':' are keywords,
//     the attribute itself is an attribute, and what follows an event after ':'
//     is a keyword (with '+' an operator and '.', ',', '(', ')' punctuation):
//     medium+hover:color is keyword, operator, keyword, punctuation, attribute;
//     keydown:ctrl+k.prevent is attribute, punctuation, keyword, operator,
//     keyword, punctuation, keyword; input:debounce(300) ends in a number in
//     parentheses. A condition is one of the words of KITWORK_UI §8 (website …
//     linux, small … huge, open … disabled), which engine/ui never lets an event
//     name share, so the words before ':' always say which side is which. A
//     name that is only a condition word (open="…") is the attribute itself.
//   - An attribute that says what an element does is an expression as a whole,
//     with no {{ }} (KITWORK_UI §9): if, show and for, and an event or a list
//     of events (click, keydown:escape, pointer-enter,focus-in). Its quotes are
//     string and what sits between them is javascript. Any other value is text
//     with {{ }} parts.
//   - There are no character references (.ui prints &amp; as written), no
//     doctype, no <script> or <style>: the only declaration is <!-- … -->.
//
// The content of <code language="…"> is .ui text like any other (engine/ui
// interpolates {{ }} in it), so it is not handed to that language here.
func tokenizeUI(scan *scanner) { uiDialect.read(scan) }

var uiDialect = &markupDialect{
	tagNameByte:   uiTagNameByte,
	textStops:     "<{",
	valueStops:    "{",
	expression:    "javascript",
	attributeName: uiAttributeName,
	codeValue:     uiCodeValue,
}

// uiEvents are the event names engine/ui accepts before an attribute's ':'
// (engine/ui/catalog.go, events).
var uiEvents = newWordSet(
	"click", "double-click", "context-menu", "click-outside",
	"keydown", "keyup",
	"pointer-enter", "pointer-leave", "pointer-down", "pointer-up",
	"focus-in", "focus-out",
	"intersect-enter", "intersect-leave",
	"submit", "input", "change", "scroll",
	"load", "error", "toggle", "ready",
)

// uiCodeValue reports whether the value of the attribute named name is an
// expression as a whole, reading the name as uiAttributeName does: past its
// condition groups, the attribute is if, show or for, or names events only.
func uiCodeValue(name string) bool {
	for {
		colon := strings.IndexByte(name, ':')
		if colon < 0 || !uiIsConditionGroup(name[:colon]) {
			break
		}
		name = name[colon+1:]
	}
	if colon := strings.IndexByte(name, ':'); colon >= 0 {
		name = name[:colon]
	}
	if name == "if" || name == "show" || name == "for" {
		return true
	}
	for {
		comma := strings.IndexByte(name, ',')
		if comma < 0 {
			return uiEvents.has(name)
		}
		if !uiEvents.has(name[:comma]) {
			return false
		}
		name = name[comma+1:]
	}
}

// uiTagNameByte is what engine/ui reads as a tag name after its first letter.
func uiTagNameByte(character byte) bool {
	return isASCIILetter(character) || isDecimalDigit(character) || character == '-' || character == '.'
}

// uiConditionWords are the prefixes of KITWORK_UI §8: targets, widths, states.
var uiConditionWords = newWordSet(
	"website", "desktop", "mobile", "ios", "android", "windows", "macos", "linux",
	"small", "medium", "large", "wide", "huge",
	"open", "active", "selected", "scrolled", "hover", "focus", "pressed", "disabled",
)

// braces reads at a '{' before limit: {{ expression }}, or \{{ and a lone '{' as
// literal text of kind literal (plain in text, string in a value).
func (dialect *markupDialect) braces(scan *scanner, limit int, literal Kind) {
	source, at := scan.source, scan.position
	if at+1 >= limit || source[at+1] != '{' {
		scan.emitLength(literal, 1)
		return
	}
	if at > 0 && source[at-1] == '\\' {
		scan.emitLength(literal, 2)
		return
	}
	scan.emitLength(Punctuation, 2)
	expression := lookUpTokenizer(dialect.expression)
	closing := uiClosingBraces(source, scan.position, limit)
	if closing < 0 {
		embedOrPlain(scan, expression, limit)
		return
	}
	embedOrPlain(scan, expression, closing)
	scan.emitLength(Punctuation, 2)
}

// embedOrPlain hands source[position:end] to tokenize, or emits it as plain
// text when there is no tokenizer.
func embedOrPlain(scan *scanner, tokenize tokenizer, end int) {
	if tokenize == nil {
		scan.emit(Plain, end)
		return
	}
	scan.embed(tokenize, end)
}

// uiClosingBraces is the offset of the "}}" that closes an expression whose text
// starts at from, looking no further than limit, or -1. Like engine/ui's
// closingBraces it skips '…', "…" and `…` strings, a backslash taking the byte
// after it, and it counts no brace depth.
func uiClosingBraces(source string, from, limit int) int {
	var quote byte
	for index := from; index < limit; index++ {
		character := source[index]
		if quote != 0 {
			if character == '\\' {
				index++
			} else if character == quote {
				quote = 0
			}
			continue
		}
		switch character {
		case '"', '\'', '`':
			quote = character
		case '}':
			if index+1 < limit && source[index+1] == '}' {
				return index
			}
		}
	}
	return -1
}

// uiAttributeName emits a .ui attribute name ending at end: the leading
// ':'-separated groups made only of condition words, then the attribute, then
// the modifiers of an event.
func uiAttributeName(scan *scanner, end int) {
	source := scan.source
	for scan.position < end {
		colon := strings.IndexByte(source[scan.position:end], ':')
		if colon < 0 || !uiIsConditionGroup(source[scan.position:scan.position+colon]) {
			break
		}
		uiEmitNameParts(scan, scan.position+colon, Keyword)
		scan.emitLength(Punctuation, 1)
	}
	nameEnd := end
	if colon := strings.IndexByte(source[scan.position:end], ':'); colon >= 0 {
		nameEnd = scan.position + colon
	}
	uiEmitNameParts(scan, nameEnd, Attribute)
	uiEmitNameParts(scan, end, Keyword)
}

// uiIsConditionGroup reports whether text is condition words joined by '+'
// (and) and ',' (or): ios,android+large.
func uiIsConditionGroup(text string) bool {
	if text == "" {
		return false
	}
	start := 0
	for index := 0; index <= len(text); index++ {
		if index < len(text) && text[index] != '+' && text[index] != ',' {
			continue
		}
		if !uiConditionWords.has(text[start:index]) {
			return false
		}
		start = index + 1
	}
	return true
}

// uiEmitNameParts emits up to end: words as kind, '+' as an operator, ':', '.',
// ',', '(' and ')' as punctuation, and a number (300, -20%) as a number.
func uiEmitNameParts(scan *scanner, end int, kind Kind) {
	for scan.position < end {
		source, at := scan.source, scan.position
		character := source[at]
		switch {
		case character == '+':
			scan.emitLength(Operator, 1)
		case strings.IndexByte(":.,()", character) >= 0:
			scan.emitLength(Punctuation, 1)
		case isDecimalDigit(character) || (character == '-' && isDecimalDigit(byteAt(source[:end], at+1))):
			digits := at
			if character == '-' {
				digits++
			}
			number := scanNumber(source[:end], digits)
			if byteAt(source[:end], number) == '%' {
				number++
			}
			scan.emit(Number, number)
		default:
			scan.emit(kind, scanWhile(source[:end], at, func(character byte) bool {
				return strings.IndexByte("+:.,()", character) < 0
			}))
		}
	}
}
