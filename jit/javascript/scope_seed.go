package javascript

import (
	"errors"
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// ErrInvalidScopeUse reports authored scope metadata that cannot be decoded
// into the browser runtime's closed initial-state grammar.
var ErrInvalidScopeUse = errors.New("kitjs: invalid scope use")

const (
	scopeSourceLimit = 16384
	scopeDepthLimit  = 32
	scopeNodeLimit   = 1024
)

var scopeNumberPattern = regexp.MustCompile(`^[+-]?(?:(?:0|[1-9][0-9]*)(?:\.[0-9]+)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?`)

var scopeValueWords = map[string]bool{
	"true": true, "false": true, "null": true,
}

var scopePrototypeKeys = map[string]bool{
	"constructor": true, "prototype": true, "__proto__": true,
	"__defineGetter__": true, "__defineSetter__": true,
	"__lookupGetter__": true, "__lookupSetter__": true,
}

var scopeBlockedNames = map[string]bool{
	"constructor": true, "prototype": true, "__proto__": true,
	"__defineGetter__": true, "__defineSetter__": true,
	"__lookupGetter__": true, "__lookupSetter__": true,
	"ownerDocument": true, "defaultView": true, "contentWindow": true,
	"window": true, "globalThis": true, "top": true, "parent": true,
	"self": true, "caller": true, "callee": true, "arguments": true,
}

var scopeForbiddenNames = map[string]bool{
	"var": true, "let": true, "const": true, "function": true, "class": true,
	"return": true, "if": true, "else": true, "for": true, "while": true,
	"do": true, "switch": true, "case": true, "new": true, "delete": true,
	"void": true, "typeof": true, "instanceof": true, "in": true,
	"await": true, "yield": true, "throw": true, "try": true, "catch": true,
	"finally": true, "import": true, "export": true, "this": true,
	"super": true, "with": true, "debugger": true, "of": true, "async": true,
	"document": true, "location": true, "navigator": true, "Function": true,
	"eval": true, "undefined": true, "NaN": true, "Infinity": true,
}

// validateScopeInitializer applies the same closed, bounded data grammar as
// the browser scope fragment. Authored HTML is entity-decoded exactly once
// before validation, matching Element.getAttribute().
func validateScopeInitializer(authored string) error {
	_, err := scopeInitializerFields(authored)
	return err
}

// scopeInitializerFields validates a scope initializer and returns its exact
// top-level field names. Callers use the result to reject metadata-owned names
// without maintaining a second, less precise scope parser.
func scopeInitializerFields(authored string) (map[string]struct{}, error) {
	decoded := html.UnescapeString(authored)
	parser := scopeSeedParser{source: decoded, topLevelFields: make(map[string]struct{})}
	if parser.utf16Length() > scopeSourceLimit {
		return nil, fmt.Errorf("data-kit-scope exceeds %d UTF-16 code units", scopeSourceLimit)
	}
	parser.skip()
	if parser.done() {
		return nil, parser.syntax("empty data-kit-scope")
	}
	if parser.peek() == '{' {
		if err := parser.object(1, true, true); err != nil {
			return nil, err
		}
	} else if err := parser.shorthand(); err != nil {
		return nil, err
	}
	parser.skip()
	if !parser.done() {
		return nil, parser.syntax(fmt.Sprintf("unexpected token %q", parser.peek()))
	}
	return parser.topLevelFields, nil
}

type scopeSeedParser struct {
	source         string
	index          int
	nodes          int
	topLevelFields map[string]struct{}
}

func (parser *scopeSeedParser) utf16Length() int {
	total := 0
	for _, character := range parser.source {
		total += utf16.RuneLen(character)
	}
	return total
}

func (parser *scopeSeedParser) syntax(message string) error {
	return fmt.Errorf("%s at byte %d", message, parser.index)
}

func (parser *scopeSeedParser) done() bool { return parser.index >= len(parser.source) }

func (parser *scopeSeedParser) peek() byte {
	if parser.done() {
		return 0
	}
	return parser.source[parser.index]
}

func (parser *scopeSeedParser) skip() {
	for !parser.done() {
		switch parser.peek() {
		case ' ', '\t', '\n', '\r', '\f':
			parser.index++
		default:
			return
		}
	}
}

func (parser *scopeSeedParser) count() error {
	parser.nodes++
	if parser.nodes > scopeNodeLimit {
		return fmt.Errorf("data-kit-scope exceeds %d data nodes", scopeNodeLimit)
	}
	return nil
}

func (parser *scopeSeedParser) depth(level int) error {
	if level > scopeDepthLimit {
		return fmt.Errorf("data-kit-scope exceeds %d data levels", scopeDepthLimit)
	}
	return nil
}

func (parser *scopeSeedParser) shorthand() error {
	if err := parser.depth(1); err != nil {
		return err
	}
	if err := parser.count(); err != nil {
		return err
	}
	seen := make(map[string]struct{})
	for !parser.done() {
		keyAt := parser.index
		key, err := parser.name()
		if err != nil {
			return err
		}
		if _, exists := seen[key]; exists {
			parser.index = keyAt
			return parser.syntax(fmt.Sprintf("duplicate scope field %q", key))
		}
		seen[key] = struct{}{}
		parser.topLevelFields[key] = struct{}{}
		parser.skip()
		if parser.peek() != ':' {
			return parser.syntax(`expected ":"`)
		}
		parser.index++
		if err := parser.value(1); err != nil {
			return err
		}
		parser.skip()
		if parser.done() {
			return nil
		}
		if parser.peek() != ';' {
			return parser.syntax(`expected ";"`)
		}
		parser.index++
		parser.skip()
		if parser.done() {
			return nil
		}
	}
	return nil
}

func (parser *scopeSeedParser) value(parentLevel int) error {
	parser.skip()
	if parser.done() {
		return parser.syntax("expected a scope value")
	}
	switch parser.peek() {
	case '{':
		return parser.object(parentLevel+1, false, false)
	case '[':
		return parser.array(parentLevel + 1)
	case '\'', '"':
		if _, err := parser.stringValue(); err != nil {
			return err
		}
		return parser.count()
	case '+', '-', '.':
		return parser.number()
	}
	if parser.peek() >= '0' && parser.peek() <= '9' {
		return parser.number()
	}
	if scopeIdentifierStart(parser.peek()) {
		start := parser.index
		parser.index++
		for !parser.done() && scopeIdentifierPart(parser.peek()) {
			parser.index++
		}
		word := parser.source[start:parser.index]
		switch word {
		case "true", "false", "null":
			return parser.count()
		default:
			parser.index = start
			return parser.syntax(fmt.Sprintf("scope values must be pure data; found identifier %q", word))
		}
	}
	return parser.syntax("expected a scope value")
}

func (parser *scopeSeedParser) number() error {
	start := parser.index
	match := scopeNumberPattern.FindString(parser.source[start:])
	if match == "" {
		return parser.syntax("invalid number")
	}
	value, err := strconv.ParseFloat(match, 64)
	if err != nil || value != value || value > 1.7976931348623157e308 || value < -1.7976931348623157e308 {
		return parser.syntax("number is outside the supported range")
	}
	parser.index += len(match)
	return parser.count()
}

func (parser *scopeSeedParser) array(level int) error {
	if err := parser.depth(level); err != nil {
		return err
	}
	if err := parser.count(); err != nil {
		return err
	}
	parser.index++
	parser.skip()
	if parser.peek() == ']' {
		parser.index++
		return nil
	}
	for !parser.done() {
		if err := parser.value(level); err != nil {
			return err
		}
		parser.skip()
		switch parser.peek() {
		case ']':
			parser.index++
			return nil
		case ',':
			parser.index++
			parser.skip()
			if parser.peek() == ']' {
				return parser.syntax("arrays reject a trailing comma")
			}
		default:
			return parser.syntax(`expected "," or "]"`)
		}
	}
	return parser.syntax(`expected "]"`)
}

func (parser *scopeSeedParser) object(level int, requireEntry, topLevel bool) error {
	if err := parser.depth(level); err != nil {
		return err
	}
	if err := parser.count(); err != nil {
		return err
	}
	parser.index++
	parser.skip()
	if parser.peek() == '}' {
		if requireEntry {
			return parser.syntax("scope objects cannot be empty")
		}
		parser.index++
		return nil
	}
	seen := make(map[string]struct{})
	for !parser.done() {
		keyAt := parser.index
		key, err := parser.objectKey(topLevel)
		if err != nil {
			return err
		}
		if _, exists := seen[key]; exists {
			parser.index = keyAt
			return parser.syntax(fmt.Sprintf("duplicate scope field %q", key))
		}
		seen[key] = struct{}{}
		if topLevel {
			parser.topLevelFields[key] = struct{}{}
		}
		parser.skip()
		if parser.peek() != ':' {
			return parser.syntax(`expected ":"`)
		}
		parser.index++
		if err := parser.value(level); err != nil {
			return err
		}
		parser.skip()
		switch parser.peek() {
		case '}':
			parser.index++
			return nil
		case ',':
			parser.index++
			parser.skip()
			if parser.peek() == '}' {
				parser.index++
				return nil
			}
		default:
			return parser.syntax(`expected "," or "}"`)
		}
	}
	return parser.syntax(`expected "}"`)
}

func (parser *scopeSeedParser) objectKey(topLevel bool) (string, error) {
	parser.skip()
	start := parser.index
	if !topLevel && (parser.peek() == '\'' || parser.peek() == '"') {
		key, err := parser.stringValue()
		if err != nil {
			return "", err
		}
		if scopePrototypeKeys[key] {
			parser.index = start
			return "", parser.syntax(fmt.Sprintf("blocked object key %q", key))
		}
		return key, nil
	}
	return parser.name()
}

func (parser *scopeSeedParser) name() (string, error) {
	parser.skip()
	start := parser.index
	var output string
	var err error
	if parser.peek() == '\'' || parser.peek() == '"' {
		output, err = parser.stringValue()
		if err != nil {
			return "", err
		}
	} else {
		if parser.done() || !scopeIdentifierStart(parser.peek()) {
			return "", parser.syntax("expected a scope field name")
		}
		parser.index++
		for !parser.done() && scopeIdentifierPart(parser.peek()) {
			parser.index++
		}
		output = parser.source[start:parser.index]
	}
	if !validScopeIdentifier(output) || scopeValueWords[output] || scopeForbiddenNames[output] || scopeBlockedNames[output] {
		parser.index = start
		return "", parser.syntax(fmt.Sprintf("invalid scope field %q", output))
	}
	return output, nil
}

func (parser *scopeSeedParser) stringValue() (string, error) {
	start := parser.index
	quote := parser.peek()
	parser.index++
	var output strings.Builder
	for !parser.done() {
		character := parser.peek()
		parser.index++
		if character == quote {
			return output.String(), nil
		}
		if character == '\\' {
			if parser.done() {
				parser.index = start
				return "", parser.syntax("unfinished string")
			}
			escaped := parser.peek()
			parser.index++
			switch escaped {
			case 'u':
				unitAt := parser.index - 2
				unit, err := parser.unicodeUnit()
				if err != nil {
					return "", err
				}
				if unit >= 0xdc00 && unit <= 0xdfff {
					parser.index = unitAt
					return "", parser.syntax("lone UTF-16 low surrogate")
				}
				if unit >= 0xd800 && unit <= 0xdbff {
					if parser.index+2 > len(parser.source) || parser.source[parser.index] != '\\' || parser.source[parser.index+1] != 'u' {
						parser.index = unitAt
						return "", parser.syntax("invalid UTF-16 surrogate pair")
					}
					parser.index += 2
					low, lowErr := parser.unicodeUnit()
					if lowErr != nil || low < 0xdc00 || low > 0xdfff {
						parser.index = unitAt
						return "", parser.syntax("invalid UTF-16 surrogate pair")
					}
					output.WriteRune(utf16.DecodeRune(rune(unit), rune(low)))
				} else {
					output.WriteRune(rune(unit))
				}
			case 'n':
				output.WriteByte('\n')
			case 'r':
				output.WriteByte('\r')
			case 't':
				output.WriteByte('\t')
			case 'b':
				output.WriteByte('\b')
			case 'f':
				output.WriteByte('\f')
			case '\\', '/', '\'', '"':
				output.WriteByte(escaped)
			default:
				return "", parser.syntax(fmt.Sprintf("unsupported string escape \\%c", escaped))
			}
			continue
		}
		if character < utf8.RuneSelf {
			if character < 32 {
				return "", parser.syntax("unescaped control character in string")
			}
			output.WriteByte(character)
			continue
		}
		parser.index--
		decoded, size := utf8.DecodeRuneInString(parser.source[parser.index:])
		if decoded == utf8.RuneError && size == 1 {
			return "", parser.syntax("invalid UTF-8 in string")
		}
		parser.index += size
		output.WriteRune(decoded)
	}
	parser.index = start
	return "", parser.syntax("unfinished string")
}

func (parser *scopeSeedParser) unicodeUnit() (uint16, error) {
	if parser.index+4 > len(parser.source) {
		return 0, parser.syntax("invalid unicode string escape")
	}
	value, err := strconv.ParseUint(parser.source[parser.index:parser.index+4], 16, 16)
	if err != nil {
		return 0, parser.syntax("invalid unicode string escape")
	}
	parser.index += 4
	return uint16(value), nil
}

func validScopeIdentifier(value string) bool {
	if value == "" || !scopeIdentifierStart(value[0]) {
		return false
	}
	for index := 1; index < len(value); index++ {
		character := value[index]
		if !scopeIdentifierStart(character) && (character < '0' || character > '9') {
			return false
		}
	}
	return true
}

func scopeIdentifierStart(character byte) bool {
	return character >= 'A' && character <= 'Z' || character >= 'a' && character <= 'z' || character == '_'
}

func scopeIdentifierPart(character byte) bool {
	return scopeIdentifierStart(character) || character >= '0' && character <= '9' || character == '$'
}
