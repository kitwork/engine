package javascript

import (
	"fmt"
	"unicode/utf8"
)

type javascriptSlashContext uint8

const (
	javascriptSlashRegexp javascriptSlashContext = iota
	javascriptSlashDivision
	javascriptSlashAmbiguous
)

type javascriptDelimiter struct {
	open               byte
	controlParenthesis bool
	templateExpression bool
}

// validateStagedComponentSource proves that source is lexically contained as
// one function body before stagedComponentSource inserts it into an installer.
// It intentionally does not sandbox trusted installer behavior: registration-
// only remains the tenant package contract. The validator prevents authored
// bytes from closing the installer and running while a speculative handoff
// script is merely being loaded.
func validateStagedComponentSource(name string, source []byte) error {
	if !utf8.Valid(source) {
		return fmt.Errorf("kitjs: component %q source must be valid UTF-8", name)
	}
	scanner := javascriptContainmentScanner{
		source: source,
		slash:  javascriptSlashRegexp,
	}
	if err := scanner.scanCode(false); err != nil {
		return fmt.Errorf("kitjs: component %q source is not lexically contained: %w", name, err)
	}
	if len(scanner.delimiters) != 0 {
		return fmt.Errorf("kitjs: component %q source is not lexically contained: unclosed %q at byte %d",
			name, scanner.delimiters[len(scanner.delimiters)-1].open, scanner.index)
	}
	return nil
}

type javascriptContainmentScanner struct {
	source          []byte
	index           int
	delimiters      []javascriptDelimiter
	slash           javascriptSlashContext
	pendingControl  bool
	memberAccess    bool
	restrictedLabel bool
}

func (scanner *javascriptContainmentScanner) scanCode(templateExpression bool) error {
	for scanner.index < len(scanner.source) {
		character := scanner.source[scanner.index]
		switch character {
		case ' ', '\t', '\v', '\f':
			scanner.index++
			continue
		case '\n', '\r':
			scanner.endRestrictedLine()
			scanner.index++
			continue
		case '\'', '"':
			scanner.pendingControl = false
			scanner.memberAccess = false
			scanner.restrictedLabel = false
			if err := scanner.scanString(character); err != nil {
				return err
			}
			scanner.slash = javascriptSlashDivision
			continue
		case '`':
			scanner.pendingControl = false
			scanner.memberAccess = false
			scanner.restrictedLabel = false
			if err := scanner.scanTemplate(); err != nil {
				return err
			}
			scanner.slash = javascriptSlashDivision
			continue
		case '\\':
			return scanner.errorf("identifier escapes are not accepted in component packages")
		case '/':
			if scanner.peek(1) == '/' {
				scanner.scanLineComment()
				continue
			}
			if scanner.peek(1) == '*' {
				if err := scanner.scanBlockComment(); err != nil {
					return err
				}
				continue
			}
			scanner.pendingControl = false
			scanner.memberAccess = false
			switch scanner.slash {
			case javascriptSlashRegexp:
				if err := scanner.scanRegexp(); err != nil {
					return err
				}
				scanner.slash = javascriptSlashDivision
			case javascriptSlashDivision:
				scanner.index++
				if scanner.index < len(scanner.source) && scanner.source[scanner.index] == '=' {
					scanner.index++
				}
				scanner.slash = javascriptSlashRegexp
			default:
				return scanner.errorf("ambiguous '/' must be parenthesized or separated by an operator")
			}
			continue
		case '(':
			control := scanner.pendingControl
			scanner.pendingControl = false
			scanner.memberAccess = false
			scanner.restrictedLabel = false
			scanner.delimiters = append(scanner.delimiters, javascriptDelimiter{open: '(', controlParenthesis: control})
			scanner.index++
			scanner.slash = javascriptSlashRegexp
			continue
		case '[':
			scanner.pendingControl = false
			scanner.memberAccess = false
			scanner.restrictedLabel = false
			scanner.delimiters = append(scanner.delimiters, javascriptDelimiter{open: '['})
			scanner.index++
			scanner.slash = javascriptSlashRegexp
			continue
		case '{':
			scanner.pendingControl = false
			scanner.memberAccess = false
			scanner.restrictedLabel = false
			scanner.delimiters = append(scanner.delimiters, javascriptDelimiter{open: '{'})
			scanner.index++
			scanner.slash = javascriptSlashRegexp
			continue
		case ')', ']', '}':
			closedTemplate, err := scanner.closeDelimiter(character)
			if err != nil {
				return err
			}
			if closedTemplate {
				if !templateExpression {
					return scanner.errorf("unexpected template-expression close")
				}
				return nil
			}
			continue
		case '<':
			if scanner.hasPrefix("<!--") {
				return scanner.errorf("HTML-like comments are not accepted in component packages")
			}
		case '-':
			if scanner.hasPrefix("-->") {
				return scanner.errorf("HTML-like comments are not accepted in component packages")
			}
		}

		if javascriptIdentifierStart(character) {
			scanner.scanIdentifier()
			continue
		}
		if character >= '0' && character <= '9' {
			scanner.scanNumber()
			continue
		}
		if character >= utf8.RuneSelf {
			if scanner.unicodeLineTerminator() {
				scanner.endRestrictedLine()
				scanner.index += 3
				continue
			}
			if width := scanner.javascriptWhitespaceWidth(); width != 0 {
				scanner.index += width
				continue
			}
			restrictedLabel := scanner.restrictedLabel
			scanner.restrictedLabel = false
			_, width := utf8.DecodeRune(scanner.source[scanner.index:])
			if width == 0 {
				return scanner.errorf("invalid UTF-8 token")
			}
			scanner.index += width
			for scanner.index < len(scanner.source) {
				character = scanner.source[scanner.index]
				if character < utf8.RuneSelf && javascriptIdentifierContinue(character) {
					scanner.index++
					continue
				}
				if character < utf8.RuneSelf {
					break
				}
				if scanner.javascriptWhitespaceWidth() != 0 {
					break
				}
				_, width = utf8.DecodeRune(scanner.source[scanner.index:])
				scanner.index += width
			}
			scanner.pendingControl = false
			scanner.memberAccess = false
			if restrictedLabel {
				scanner.slash = javascriptSlashAmbiguous
			} else {
				scanner.slash = javascriptSlashDivision
			}
			continue
		}

		scanner.scanOperator()
	}
	if templateExpression {
		return scanner.errorf("unterminated template expression")
	}
	return nil
}

func (scanner *javascriptContainmentScanner) closeDelimiter(close byte) (bool, error) {
	if len(scanner.delimiters) == 0 {
		return false, scanner.errorf("closing %q escapes the installer body", close)
	}
	last := len(scanner.delimiters) - 1
	delimiter := scanner.delimiters[last]
	want := byte(')')
	if delimiter.open == '[' {
		want = ']'
	} else if delimiter.open == '{' {
		want = '}'
	}
	if close != want {
		return false, scanner.errorf("closing %q does not match %q", close, delimiter.open)
	}
	scanner.delimiters = scanner.delimiters[:last]
	scanner.index++
	scanner.pendingControl = false
	scanner.memberAccess = false
	scanner.restrictedLabel = false
	if delimiter.templateExpression {
		scanner.slash = javascriptSlashDivision
		return true, nil
	}
	switch close {
	case ')':
		if delimiter.controlParenthesis {
			scanner.slash = javascriptSlashRegexp
		} else {
			scanner.slash = javascriptSlashDivision
		}
	case ']':
		scanner.slash = javascriptSlashDivision
	case '}':
		// A closing brace may finish a block, object, class, or function. Slash
		// grammar differs among those contexts, so reject a directly following
		// slash rather than risk treating division as a regular expression.
		scanner.slash = javascriptSlashAmbiguous
	}
	return false, nil
}

func (scanner *javascriptContainmentScanner) scanString(quote byte) error {
	start := scanner.index
	scanner.index++
	for scanner.index < len(scanner.source) {
		character := scanner.source[scanner.index]
		if character == quote {
			scanner.index++
			return nil
		}
		if character == '\n' || character == '\r' || scanner.unicodeLineTerminator() {
			return fmt.Errorf("unterminated string starting at byte %d", start)
		}
		if character == '\\' {
			scanner.index++
			if scanner.index >= len(scanner.source) {
				return fmt.Errorf("unterminated string escape starting at byte %d", start)
			}
			if scanner.source[scanner.index] == '\r' {
				scanner.index++
				if scanner.index < len(scanner.source) && scanner.source[scanner.index] == '\n' {
					scanner.index++
				}
			} else if scanner.unicodeLineTerminator() {
				scanner.index += 3
			} else {
				scanner.index++
			}
			continue
		}
		scanner.index++
	}
	return fmt.Errorf("unterminated string starting at byte %d", start)
}

func (scanner *javascriptContainmentScanner) scanTemplate() error {
	start := scanner.index
	scanner.index++
	for scanner.index < len(scanner.source) {
		character := scanner.source[scanner.index]
		switch character {
		case '`':
			scanner.index++
			return nil
		case '\\':
			scanner.index++
			if scanner.index >= len(scanner.source) {
				return fmt.Errorf("unterminated template escape starting at byte %d", start)
			}
			if scanner.source[scanner.index] == '\r' {
				scanner.index++
				if scanner.index < len(scanner.source) && scanner.source[scanner.index] == '\n' {
					scanner.index++
				}
			} else if scanner.unicodeLineTerminator() {
				scanner.index += 3
			} else {
				scanner.index++
			}
			continue
		case '$':
			if scanner.peek(1) == '{' {
				scanner.delimiters = append(scanner.delimiters, javascriptDelimiter{open: '{', templateExpression: true})
				scanner.index += 2
				scanner.pendingControl = false
				scanner.memberAccess = false
				scanner.slash = javascriptSlashRegexp
				if err := scanner.scanCode(true); err != nil {
					return err
				}
				continue
			}
		}
		scanner.index++
	}
	return fmt.Errorf("unterminated template starting at byte %d", start)
}

func (scanner *javascriptContainmentScanner) scanRegexp() error {
	start := scanner.index
	scanner.index++
	inClass := false
	for scanner.index < len(scanner.source) {
		character := scanner.source[scanner.index]
		if character == '\n' || character == '\r' || scanner.unicodeLineTerminator() {
			return fmt.Errorf("unterminated regular expression starting at byte %d", start)
		}
		if character == '\\' {
			scanner.index++
			if scanner.index >= len(scanner.source) || scanner.source[scanner.index] == '\n' ||
				scanner.source[scanner.index] == '\r' || scanner.unicodeLineTerminator() {
				return fmt.Errorf("unterminated regular expression escape starting at byte %d", start)
			}
			scanner.index++
			continue
		}
		if character == '[' {
			inClass = true
			scanner.index++
			continue
		}
		if character == ']' && inClass {
			inClass = false
			scanner.index++
			continue
		}
		if character == '/' && !inClass {
			scanner.index++
			for scanner.index < len(scanner.source) {
				character = scanner.source[scanner.index]
				if character < utf8.RuneSelf {
					if !javascriptIdentifierContinue(character) {
						break
					}
					scanner.index++
					continue
				}
				if scanner.javascriptWhitespaceWidth() != 0 {
					break
				}
				_, width := utf8.DecodeRune(scanner.source[scanner.index:])
				scanner.index += width
			}
			return nil
		}
		scanner.index++
	}
	return fmt.Errorf("unterminated regular expression starting at byte %d", start)
}

func (scanner *javascriptContainmentScanner) scanLineComment() {
	scanner.index += 2
	for scanner.index < len(scanner.source) && scanner.source[scanner.index] != '\n' &&
		scanner.source[scanner.index] != '\r' && !scanner.unicodeLineTerminator() {
		scanner.index++
	}
}

func (scanner *javascriptContainmentScanner) scanBlockComment() error {
	start := scanner.index
	scanner.index += 2
	for scanner.index+1 < len(scanner.source) {
		if scanner.source[scanner.index] == '\n' || scanner.source[scanner.index] == '\r' ||
			scanner.unicodeLineTerminator() {
			scanner.endRestrictedLine()
		}
		if scanner.source[scanner.index] == '*' && scanner.source[scanner.index+1] == '/' {
			scanner.index += 2
			return nil
		}
		scanner.index++
	}
	return fmt.Errorf("unterminated block comment starting at byte %d", start)
}

func (scanner *javascriptContainmentScanner) scanIdentifier() {
	start := scanner.index
	scanner.index++
	for scanner.index < len(scanner.source) {
		character := scanner.source[scanner.index]
		if character < utf8.RuneSelf {
			if !javascriptIdentifierContinue(character) {
				break
			}
			scanner.index++
			continue
		}
		if scanner.javascriptWhitespaceWidth() != 0 {
			break
		}
		_, width := utf8.DecodeRune(scanner.source[scanner.index:])
		scanner.index += width
	}
	word := string(scanner.source[start:scanner.index])
	member := scanner.memberAccess
	controlPending := scanner.pendingControl
	restrictedLabel := scanner.restrictedLabel
	scanner.memberAccess = false
	scanner.pendingControl = false
	scanner.restrictedLabel = false
	if restrictedLabel && !member {
		// break and continue may carry one label before ASI terminates the
		// statement. Without tracking line terminators through comments, a
		// slash after that label cannot be proven as division or RegExp.
		scanner.slash = javascriptSlashAmbiguous
		return
	}
	if !member {
		switch word {
		case "if", "for", "while", "with", "switch", "catch":
			scanner.pendingControl = true
			scanner.slash = javascriptSlashRegexp
			return
		case "await":
			if controlPending {
				// Preserve the control-parenthesis state of `for await (`. The
				// parenthesis closes a statement header, so a following slash has
				// the RegExp lexical goal.
				scanner.pendingControl = true
				scanner.slash = javascriptSlashRegexp
				return
			}
			// In a classic ordinary function, await may be an identifier rather
			// than the async-function operator. A following slash can therefore
			// be either division or a regular expression. Reject that boundary
			// instead of letting the containment scanner disagree with the
			// browser parser about authored closing delimiters.
			scanner.slash = javascriptSlashAmbiguous
			return
		case "break", "continue":
			scanner.restrictedLabel = true
			scanner.slash = javascriptSlashAmbiguous
			return
		case "of", "debugger":
			// These words are contextual or affected by automatic semicolon
			// insertion. A directly following slash does not have one lexical
			// goal that can be proven without a full parser.
			scanner.slash = javascriptSlashAmbiguous
			return
		case "extends":
			// ClassHeritage begins an expression, and extends is reserved in
			// this strict wrapper when it is not a property name.
			scanner.slash = javascriptSlashRegexp
			return
		case "return", "throw", "case", "delete", "void", "typeof", "new", "yield", "in", "instanceof", "else", "do":
			scanner.slash = javascriptSlashRegexp
			return
		case "true", "false", "null", "this", "super":
			scanner.slash = javascriptSlashDivision
			return
		}
	}
	scanner.slash = javascriptSlashDivision
}

func (scanner *javascriptContainmentScanner) scanNumber() {
	scanner.pendingControl = false
	scanner.memberAccess = false
	scanner.restrictedLabel = false
	for scanner.index < len(scanner.source) {
		character := scanner.source[scanner.index]
		if character >= '0' && character <= '9' || character >= 'a' && character <= 'f' ||
			character >= 'A' && character <= 'F' || character == 'x' || character == 'X' ||
			character == 'o' || character == 'O' || character == 'b' || character == 'B' ||
			character == 'e' || character == 'E' || character == 'n' || character == '_' || character == '.' {
			scanner.index++
			continue
		}
		break
	}
	scanner.slash = javascriptSlashDivision
}

func (scanner *javascriptContainmentScanner) scanOperator() {
	character := scanner.source[scanner.index]
	scanner.pendingControl = false
	scanner.memberAccess = false
	scanner.restrictedLabel = false
	if character == '.' {
		if scanner.hasPrefix("...") {
			scanner.index += 3
			scanner.slash = javascriptSlashRegexp
			return
		}
		scanner.index++
		scanner.memberAccess = true
		scanner.slash = javascriptSlashAmbiguous
		return
	}
	if character == '?' && scanner.peek(1) == '.' {
		scanner.index += 2
		scanner.memberAccess = true
		scanner.slash = javascriptSlashAmbiguous
		return
	}
	if (character == '+' || character == '-') && scanner.peek(1) == character {
		scanner.index += 2
		scanner.slash = javascriptSlashDivision
		return
	}
	scanner.index++
	// Punctuation and every remaining operator require an expression or begin a
	// new statement; a following slash is therefore a regular expression.
	scanner.slash = javascriptSlashRegexp
}

func (scanner *javascriptContainmentScanner) peek(offset int) byte {
	index := scanner.index + offset
	if index < 0 || index >= len(scanner.source) {
		return 0
	}
	return scanner.source[index]
}

func (scanner *javascriptContainmentScanner) hasPrefix(prefix string) bool {
	if scanner.index+len(prefix) > len(scanner.source) {
		return false
	}
	return string(scanner.source[scanner.index:scanner.index+len(prefix)]) == prefix
}

func (scanner *javascriptContainmentScanner) unicodeLineTerminator() bool {
	return scanner.index+3 <= len(scanner.source) && scanner.source[scanner.index] == 0xe2 &&
		scanner.source[scanner.index+1] == 0x80 &&
		(scanner.source[scanner.index+2] == 0xa8 || scanner.source[scanner.index+2] == 0xa9)
}

func (scanner *javascriptContainmentScanner) endRestrictedLine() {
	if !scanner.restrictedLabel {
		return
	}
	scanner.restrictedLabel = false
	scanner.slash = javascriptSlashRegexp
}

func (scanner *javascriptContainmentScanner) javascriptWhitespaceWidth() int {
	runeValue, width := utf8.DecodeRune(scanner.source[scanner.index:])
	if runeValue >= 0x2000 && runeValue <= 0x200a {
		return width
	}
	switch runeValue {
	case 0x00a0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return width
	default:
		return 0
	}
}

func (scanner *javascriptContainmentScanner) errorf(format string, args ...any) error {
	return fmt.Errorf(format+" at byte %d", append(args, scanner.index)...)
}

func javascriptIdentifierStart(character byte) bool {
	return character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
		character == '_' || character == '$'
}

func javascriptIdentifierContinue(character byte) bool {
	return javascriptIdentifierStart(character) || character >= '0' && character <= '9' || character >= utf8.RuneSelf
}
