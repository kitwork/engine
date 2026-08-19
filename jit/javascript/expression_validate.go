package javascript

import (
	"errors"
	"fmt"
	"html"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"
)

// ErrInvalidExpressionUse reports authored directive source that the browser
// expression compiler would reject. Validation is syntax-only; expressions
// remain browser-owned and are never evaluated by the Go host.
var ErrInvalidExpressionUse = errors.New("kitjs: invalid expression use")

const (
	expressionNodeLimit  = 10000
	expressionDepthLimit = 64
	styleSourceLimit     = 16384
	styleEntryLimit      = 128
)

var expressionModelPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var expressionBindNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.:-]*$`)
var expressionStyleNamePattern = regexp.MustCompile(`^-?[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`)
var expressionStyleCustomNamePattern = regexp.MustCompile(`^--[A-Za-z_][A-Za-z0-9_-]*$`)
var expressionForPattern = regexp.MustCompile(`(?s)^[\t\n\x{000B}\f\r \p{Zs}\x{FEFF}\x{2028}\x{2029}]*([A-Za-z_$][A-Za-z0-9_$]*)[\t\n\x{000B}\f\r \p{Zs}\x{FEFF}\x{2028}\x{2029}]*(?:,[\t\n\x{000B}\f\r \p{Zs}\x{FEFF}\x{2028}\x{2029}]*([A-Za-z_$][A-Za-z0-9_$]*)[\t\n\x{000B}\f\r \p{Zs}\x{FEFF}\x{2028}\x{2029}]*)?[\t\n\x{000B}\f\r \p{Zs}\x{FEFF}\x{2028}\x{2029}]+of[\t\n\x{000B}\f\r \p{Zs}\x{FEFF}\x{2028}\x{2029}]+(.+?)[\t\n\x{000B}\f\r \p{Zs}\x{FEFF}\x{2028}\x{2029}]*$`)

var expressionBlockedNames = map[string]bool{
	"constructor": true, "prototype": true, "__proto__": true,
	"__defineGetter__": true, "__defineSetter__": true,
	"__lookupGetter__": true, "__lookupSetter__": true,
	"ownerDocument": true, "defaultView": true, "contentWindow": true,
	"window": true, "globalThis": true, "top": true, "parent": true,
	"self": true, "caller": true, "callee": true, "arguments": true,
}

var expressionForbiddenNames = map[string]bool{
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

func validateDirectiveExpression(attribute rawScannedAttribute) error {
	_, err := directiveExpressionServiceCalls(attribute)
	return err
}

// directiveExpressionServiceCalls validates one authored directive and returns
// the narrow service commands it contains. The scanner resolves each returned
// alias against the component catalog after the complete document has been
// read; expression validation itself remains independent of document order.
func directiveExpressionServiceCalls(attribute rawScannedAttribute) ([]expressionServiceCall, error) {
	if !strings.HasPrefix(attribute.name, "data-kit-") {
		return nil, nil
	}
	directive := strings.TrimPrefix(attribute.name, "data-kit-")
	if colon := strings.IndexByte(directive, ':'); colon >= 0 {
		directive = directive[:colon]
	}
	mode := ""
	switch directive {
	case "text", "show", "class", "if", "key":
		mode = "binding"
	case "click", "dblclick", "submit", "input", "change", "keydown", "keyup", "pointerdown", "pointerup", "focusin", "focusout":
		mode = "action"
	case "bind":
		if !attribute.hasValue {
			return nil, fmt.Errorf("bind requires a value")
		}
		return nil, validateBindExpression(attribute.value)
	case "style":
		if !attribute.hasValue {
			return nil, fmt.Errorf("style requires a value")
		}
		return nil, validateStyleExpression(attribute.value)
	case "model":
		if !attribute.hasValue {
			return nil, fmt.Errorf("model requires a value")
		}
		return nil, validateModelExpression(attribute.value)
	case "for":
		if !attribute.hasValue {
			return nil, fmt.Errorf("for requires a value")
		}
		return nil, validateForExpression(attribute.value)
	default:
		return nil, nil
	}
	if !attribute.hasValue {
		return nil, fmt.Errorf("%s requires a value", directive)
	}
	return expressionServiceCalls(attribute.value, mode)
}

type expressionTokenKind uint8

const (
	expressionEnd expressionTokenKind = iota
	expressionLiteral
	expressionIdentifier
	expressionOperator
)

type expressionToken struct {
	kind     expressionTokenKind
	value    string
	position int
	literal  any
}

type expressionServiceCall struct {
	Alias    string
	Service  string
	Method   string
	Position int
	Loader   bool
}

type expressionServiceReference struct {
	call     expressionServiceCall
	accepted bool
}

func expressionServiceName(name string) bool {
	if _, exists := authoredServiceActions[name]; exists {
		return true
	}
	switch name {
	case "network", "progress", "request":
		return true
	default:
		return false
	}
}

func validateExpression(authored, mode string) error {
	_, err := expressionServiceCalls(authored, mode)
	return err
}

func validateDecodedExpression(source, mode string) error {
	_, err := decodedExpressionServiceCalls(source, mode)
	return err
}

func expressionServiceCalls(authored, mode string) ([]expressionServiceCall, error) {
	source := trimECMAScriptSpace(html.UnescapeString(authored))
	return decodedExpressionServiceCalls(source, mode)
}

func decodedExpressionServiceCalls(source, mode string) ([]expressionServiceCall, error) {
	source = trimECMAScriptSpace(source)
	if source == "" {
		return nil, fmt.Errorf("empty expression")
	}
	tokens, err := lexExpression(source)
	if err != nil {
		return nil, err
	}
	loaderReads, err := normalizeAppLoaderBindingTokens(tokens, mode)
	if err != nil {
		return nil, err
	}
	parser := expressionParser{source: source, tokens: tokens, action: mode == "action"}
	_, err = parser.parse()
	if err != nil {
		return nil, err
	}
	if len(parser.serviceReferences) == 0 {
		return loaderReads, nil
	}
	if !parser.action {
		return nil, expressionSyntax("service commands are only allowed in actions", parser.serviceReferences[0].call.Position)
	}
	calls := make([]expressionServiceCall, 0, len(loaderReads)+len(parser.serviceReferences))
	calls = append(calls, loaderReads...)
	for _, reference := range parser.serviceReferences {
		if !reference.accepted {
			return nil, expressionSyntax("service namespaces may only be used as static command calls", reference.call.Position)
		}
		if !validAuthoredServiceAction(reference.call.Service, reference.call.Method) {
			return nil, expressionSyntax(fmt.Sprintf("service method %s.%s is not available to authored HTML", reference.call.Service, reference.call.Method), reference.call.Position)
		}
		calls = append(calls, reference.call)
	}
	return calls, nil
}

// normalizeAppLoaderBindingTokens admits exactly the two inert presentation
// leaves projected by app@1.1: $app.loader.visible and $app.loader.value. The
// Go parser still sees an ordinary safe identifier, while ScanHTML retains a
// synthetic progress reference so an app@1.0 or missing $app host fails closed.
// Service namespaces and every other alias remain action-only.
func normalizeAppLoaderBindingTokens(tokens []expressionToken, mode string) ([]expressionServiceCall, error) {
	if mode != "binding" {
		return nil, nil
	}
	reads := make([]expressionServiceCall, 0, 2)
	for index := 0; index < len(tokens); index++ {
		token := tokens[index]
		if token.kind != expressionIdentifier || !strings.HasPrefix(token.value, "$") {
			continue
		}
		if token.value != "$app" || index+4 >= len(tokens) ||
			tokens[index+1].kind != expressionOperator || tokens[index+1].value != "." ||
			tokens[index+2].kind != expressionIdentifier || tokens[index+2].value != "loader" ||
			tokens[index+3].kind != expressionOperator || tokens[index+3].value != "." ||
			tokens[index+4].kind != expressionIdentifier ||
			tokens[index+4].value != "visible" && tokens[index+4].value != "value" {
			return nil, expressionSyntax("the $ namespace is action-only", token.position)
		}
		if index+5 < len(tokens) && tokens[index+5].kind == expressionOperator &&
			expressionStringIn(tokens[index+5].value, ".", "?.", "[", "(", "++", "--") {
			return nil, expressionSyntax("app loader bindings end at visible or value", tokens[index+5].position)
		}
		tokens[index].value = "__kitAppLoader"
		reads = append(reads, expressionServiceCall{
			Alias: "$app", Service: "progress", Method: tokens[index+4].value,
			Position: token.position, Loader: true,
		})
		index += 4
	}
	if len(reads) == 0 {
		return nil, nil
	}
	// Loader leaves may participate in arithmetic, comparison, coalescing and
	// conditionals. They cannot be grouped, passed, captured or assembled into
	// another object/array value.
	for _, token := range tokens {
		if token.kind == expressionOperator && expressionStringIn(token.value,
			"(", ")", "[", "]", "{", "}", ",", "=>", ";", "=", "?.", "++", "--") {
			return nil, expressionSyntax("app loader bindings cannot be grouped, passed, captured, or assigned", token.position)
		}
	}
	return reads, nil
}

func validateModelExpression(authored string) error {
	source := trimECMAScriptSpace(html.UnescapeString(authored))
	if strings.HasPrefix(source, "$") {
		return fmt.Errorf("model cannot use the reserved $ namespace")
	}
	if !expressionModelPattern.MatchString(source) || expressionBlockedNames[source] {
		return fmt.Errorf("model must name one component field")
	}
	return nil
}

func validateForExpression(authored string) error {
	source := html.UnescapeString(authored)
	match := expressionForPattern.FindStringSubmatch(source)
	if match == nil || !validExpressionLocal(match[1]) || match[2] != "" && !validExpressionLocal(match[2]) || match[2] != "" && match[1] == match[2] {
		return fmt.Errorf("invalid for specification")
	}
	return validateDecodedExpression(match[3], "binding")
}

func validExpressionLocal(name string) bool {
	if name == "" || !scopeIdentifierStart(name[0]) || expressionBlockedNames[name] || expressionForbiddenNames[name] {
		return false
	}
	for index := 1; index < len(name); index++ {
		if !scopeIdentifierStart(name[index]) && !expressionDigit(name[index]) {
			return false
		}
	}
	return true
}

func validateBindExpression(authored string) error {
	source := trimECMAScriptSpace(html.UnescapeString(authored))
	if len(source) >= 2 && source[0] == '{' && source[len(source)-1] == '}' {
		source = source[1 : len(source)-1]
	}
	entries := 0
	for _, part := range splitExpressionTop(source, ",;") {
		if trimECMAScriptSpace(part) == "" {
			continue
		}
		pieces := splitExpressionTop(part, ":")
		if len(pieces) < 2 {
			return fmt.Errorf("invalid bind entry")
		}
		key := trimECMAScriptSpace(pieces[0])
		if unquoted, ok := unquoteBindName(key); ok {
			key = unquoted
		} else if !expressionBindNamePattern.MatchString(key) {
			return fmt.Errorf("invalid bind name")
		}
		lower := strings.ToLower(key)
		if strings.HasPrefix(lower, "on") || strings.HasPrefix(lower, "data-kit-") || expressionUnsafeBindNames[lower] {
			return fmt.Errorf("unsafe bind name %q", key)
		}
		if err := validateDecodedExpression(strings.Join(pieces[1:], ":"), "binding"); err != nil {
			return err
		}
		entries++
	}
	if entries == 0 {
		return fmt.Errorf("empty bind map")
	}
	return nil
}

func validateStyleExpression(authored string) error {
	source := html.UnescapeString(authored)
	if expressionUTF16Length(source) > styleSourceLimit {
		return fmt.Errorf("style source exceeds %d UTF-16 code units", styleSourceLimit)
	}
	source = trimECMAScriptSpace(source)
	if source == "" {
		return fmt.Errorf("empty style map")
	}
	if source[0] == '{' {
		return fmt.Errorf("style map cannot use outer braces")
	}

	parts := splitExpressionTop(source, ";")
	if trimECMAScriptSpace(parts[len(parts)-1]) == "" {
		parts = parts[:len(parts)-1]
	}
	if len(parts) == 0 {
		return fmt.Errorf("empty style map")
	}
	if len(parts) > styleEntryLimit {
		return fmt.Errorf("style map exceeds %d entries", styleEntryLimit)
	}

	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		part = trimECMAScriptSpace(part)
		if part == "" {
			return fmt.Errorf("empty style entry")
		}
		pieces := splitExpressionTop(part, ":")
		if len(pieces) < 2 {
			return fmt.Errorf("invalid style entry")
		}
		name := trimECMAScriptSpace(pieces[0])
		if err := validateStyleName(name); err != nil {
			return err
		}
		if _, duplicate := seen[name]; duplicate {
			return fmt.Errorf("duplicate style property %q", name)
		}
		seen[name] = struct{}{}

		expression := trimECMAScriptSpace(strings.Join(pieces[1:], ":"))
		if expression == "" {
			return fmt.Errorf("empty style expression for %q", name)
		}
		if err := validateDecodedExpression(expression, "binding"); err != nil {
			return err
		}
	}
	return nil
}

func validateStyleName(name string) error {
	if name == "" {
		return fmt.Errorf("empty style property name")
	}
	if strings.HasPrefix(name, "--") {
		if !expressionStyleCustomNamePattern.MatchString(name) {
			return fmt.Errorf("invalid style property name %q", name)
		}
		lower := strings.ToLower(name)
		if strings.HasPrefix(lower, "--kit-") || strings.HasPrefix(lower, "--kitwork-") {
			return fmt.Errorf("unsafe style property name %q", name)
		}
		return nil
	}
	if !expressionStyleNamePattern.MatchString(name) {
		return fmt.Errorf("invalid style property name %q", name)
	}
	if expressionUnsafeStyleNames[name] {
		return fmt.Errorf("unsafe style property name %q", name)
	}
	if expressionStyleShorthandNames[name] {
		return fmt.Errorf("shorthand style property %q is not supported", name)
	}
	return nil
}

func expressionUTF16Length(source string) int {
	total := 0
	for _, character := range source {
		total += utf16.RuneLen(character)
	}
	return total
}

func trimECMAScriptSpace(source string) string {
	return strings.TrimFunc(source, isECMAScriptSpace)
}

func trimLeftECMAScriptSpace(source string) string {
	return strings.TrimLeftFunc(source, isECMAScriptSpace)
}

func isECMAScriptSpace(character rune) bool {
	return character == '\t' || character == '\v' || character == '\f' || character == ' ' ||
		character == '\u00a0' || character == '\ufeff' || character == '\n' || character == '\r' ||
		character == '\u2028' || character == '\u2029' || unicode.Is(unicode.Zs, character)
}

var expressionUnsafeBindNames = map[string]bool{
	"srcdoc": true, "style": true, "innerhtml": true, "outerhtml": true,
	"insertadjacenthtml": true, "textcontent": true, "innertext": true, "outertext": true,
}

var expressionUnsafeStyleNames = map[string]bool{
	"css-text": true, "csstext": true, "behavior": true, "-moz-binding": true,
}

// Shorthands are rejected because one setProperty call can overwrite longhands
// the directive does not own and therefore cannot restore independently. Keep
// this list byte-for-byte aligned with src/style.js.
var expressionStyleShorthandNames = expressionNameSet(`
-webkit-animation -webkit-border-after -webkit-border-before -webkit-border-end -webkit-border-radius
-webkit-border-start -webkit-column-rule -webkit-columns -webkit-flex -webkit-flex-flow -webkit-mask
-webkit-mask-box-image -webkit-mask-position -webkit-text-emphasis -webkit-text-stroke -webkit-transition
all animation animation-range background background-position border border-block border-block-color
border-block-end border-block-start border-block-style border-block-width border-bottom border-color
border-image border-inline border-inline-color border-inline-end border-inline-start border-inline-style
border-inline-width border-left border-radius border-right border-spacing border-style border-top border-width
column-rule column-rule-inset column-rule-inset-cap column-rule-inset-end column-rule-inset-junction
column-rule-inset-start columns contain-intrinsic-size container corner-block-end-shape corner-block-start-shape
corner-bottom-shape corner-inline-end-shape corner-inline-start-shape corner-left-shape corner-right-shape
corner-shape corner-top-shape flex flex-flow font font-synthesis font-variant gap grid grid-area grid-column
grid-gap grid-row grid-template inset inset-block inset-inline interest-delay list-style margin margin-block
margin-inline marker mask mask-position offset outline overflow overscroll-behavior padding padding-block
padding-inline place-content place-items place-self position-try row-rule row-rule-inset row-rule-inset-cap
row-rule-inset-end row-rule-inset-junction row-rule-inset-start rule rule-break rule-color rule-inset
rule-inset-cap rule-inset-end rule-inset-junction rule-inset-start rule-style rule-visibility-items rule-width
scroll-margin scroll-margin-block scroll-margin-inline scroll-padding scroll-padding-block scroll-padding-inline
scroll-timeline text-box text-decoration text-emphasis text-wrap timeline-trigger
timeline-trigger-activation-range timeline-trigger-active-range transition view-timeline white-space
`)

func expressionNameSet(source string) map[string]bool {
	output := make(map[string]bool)
	for _, name := range strings.Fields(source) {
		output[name] = true
	}
	return output
}

func unquoteBindName(source string) (string, bool) {
	if len(source) < 2 || source[0] != source[len(source)-1] || source[0] != '\'' && source[0] != '"' {
		return "", false
	}
	name := source[1 : len(source)-1]
	return name, expressionBindNamePattern.MatchString(name)
}

func splitExpressionTop(source, separators string) []string {
	output := make([]string, 0, 4)
	start := 0
	depth := 0
	quote := byte(0)
	for index := 0; index < len(source); index++ {
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
		case '\'', '"':
			quote = character
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		default:
			if depth == 0 && strings.ContainsRune(separators, rune(character)) {
				output = append(output, source[start:index])
				start = index + 1
			}
		}
	}
	return append(output, source[start:])
}

func lexExpression(source string) ([]expressionToken, error) {
	tokens := make([]expressionToken, 0, len(source)/2+1)
	for index := 0; index < len(source); {
		character := source[index]
		if expressionSpace(character) {
			index++
			continue
		}
		start := index
		if expressionDigit(character) || character == '.' && index+1 < len(source) && expressionDigit(source[index+1]) {
			if character == '.' {
				index++
			}
			for index < len(source) && expressionDigit(source[index]) {
				index++
			}
			if index < len(source) && source[index] == '.' {
				index++
				for index < len(source) && expressionDigit(source[index]) {
					index++
				}
			}
			if index < len(source) && (source[index] == 'e' || source[index] == 'E') {
				exponent := index
				index++
				if index < len(source) && (source[index] == '+' || source[index] == '-') {
					index++
				}
				digits := index
				for index < len(source) && expressionDigit(source[index]) {
					index++
				}
				if digits == index {
					return nil, expressionSyntax("invalid number exponent", exponent)
				}
			}
			text := source[start:index]
			number, parseErr := strconv.ParseFloat(text, 64)
			if parseErr != nil || math.IsInf(number, 0) || math.IsNaN(number) {
				return nil, expressionSyntax("number is outside the supported range", start)
			}
			tokens = append(tokens, expressionToken{kind: expressionLiteral, value: text, literal: number, position: start})
			continue
		}
		if character == '\'' || character == '"' {
			quote := character
			index++
			var value strings.Builder
			closed := false
			for index < len(source) {
				character = source[index]
				index++
				if character == quote {
					closed = true
					break
				}
				if character != '\\' {
					value.WriteByte(character)
					continue
				}
				if index >= len(source) {
					return nil, expressionSyntax("unfinished string", start)
				}
				escaped := source[index]
				index++
				switch escaped {
				case 'n':
					value.WriteByte('\n')
				case 'r':
					value.WriteByte('\r')
				case 't':
					value.WriteByte('\t')
				case 'b':
					value.WriteByte('\b')
				case 'f':
					value.WriteByte('\f')
				case '\\', '\'', '"':
					value.WriteByte(escaped)
				default:
					return nil, expressionSyntax(fmt.Sprintf("unsupported string escape \\%c", escaped), index-2)
				}
			}
			if !closed {
				return nil, expressionSyntax("unfinished string", start)
			}
			tokens = append(tokens, expressionToken{kind: expressionLiteral, value: value.String(), literal: value.String(), position: start})
			continue
		}
		if expressionIdentifierStart(character) {
			index++
			for index < len(source) && expressionIdentifierPart(source[index]) {
				index++
			}
			identifier := source[start:index]
			switch identifier {
			case "true":
				tokens = append(tokens, expressionToken{kind: expressionLiteral, value: identifier, literal: true, position: start})
			case "false":
				tokens = append(tokens, expressionToken{kind: expressionLiteral, value: identifier, literal: false, position: start})
			case "null":
				tokens = append(tokens, expressionToken{kind: expressionLiteral, value: identifier, literal: nil, position: start})
			default:
				if expressionForbiddenNames[identifier] {
					return nil, expressionSyntax(fmt.Sprintf("forbidden keyword %q", identifier), start)
				}
				tokens = append(tokens, expressionToken{kind: expressionIdentifier, value: identifier, position: start})
			}
			continue
		}
		if index+3 <= len(source) {
			op := source[index : index+3]
			if op == "===" || op == "!==" {
				tokens = append(tokens, expressionToken{kind: expressionOperator, value: op, position: start})
				index += 3
				continue
			}
		}
		if index+2 <= len(source) {
			op := source[index : index+2]
			if expressionTwoCharacterOperator(op) {
				tokens = append(tokens, expressionToken{kind: expressionOperator, value: op, position: start})
				index += 2
				continue
			}
			if expressionUnsupportedOperator(op) {
				return nil, expressionSyntax(fmt.Sprintf("unsupported operator %q", op), start)
			}
		}
		if strings.ContainsRune("+-*/%!?:.,()[]{}=;<>", rune(character)) {
			tokens = append(tokens, expressionToken{kind: expressionOperator, value: string(character), position: start})
			index++
			continue
		}
		return nil, expressionSyntax(fmt.Sprintf("unexpected character %q", character), start)
	}
	tokens = append(tokens, expressionToken{kind: expressionEnd, position: len(source)})
	return tokens, nil
}

func expressionSpace(character byte) bool {
	return character == ' ' || character == '\t' || character == '\n' || character == '\r' || character == '\f'
}

func expressionDigit(character byte) bool { return character >= '0' && character <= '9' }

func expressionIdentifierStart(character byte) bool {
	return character == '$' || character == '_' || character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z'
}

func expressionIdentifierPart(character byte) bool {
	return expressionIdentifierStart(character) || expressionDigit(character)
}

func expressionTwoCharacterOperator(operator string) bool {
	switch operator {
	case "??", "&&", "||", "<=", ">=", "=>", "==", "!=", "?.", "++", "--":
		return true
	default:
		return false
	}
}

func expressionUnsupportedOperator(operator string) bool {
	switch operator {
	case "+=", "-=", "*=", "/=", "%=", "**", "<<", ">>":
		return true
	default:
		return false
	}
}

func expressionSyntax(message string, position int) error {
	return fmt.Errorf("%s at byte %d", message, position)
}

type expressionNode struct {
	kind             string
	name             string
	literal          any
	alias            string
	aliasDepth       int
	directAlias      bool
	serviceReference int
	serviceMethod    string
	serviceCommand   bool
}

type expressionParser struct {
	source            string
	tokens            []expressionToken
	position          int
	nodes             int
	nesting           int
	action            bool
	serviceReferences []expressionServiceReference
}

func (parser *expressionParser) current() expressionToken {
	if parser.position >= len(parser.tokens) {
		return expressionToken{kind: expressionEnd, position: len(parser.source)}
	}
	return parser.tokens[parser.position]
}

func (parser *expressionParser) is(value string) bool {
	current := parser.current()
	return current.kind == expressionOperator && current.value == value
}

func (parser *expressionParser) take(value string) bool {
	if !parser.is(value) {
		return false
	}
	parser.position++
	return true
}

func (parser *expressionParser) expect(value string) error {
	if parser.take(value) {
		return nil
	}
	return parser.syntax(fmt.Sprintf("expected %q", value))
}

func (parser *expressionParser) syntax(message string) error {
	return expressionSyntax(message, parser.current().position)
}

func (parser *expressionParser) make(kind string) (expressionNode, error) {
	parser.nodes++
	if parser.nodes > expressionNodeLimit {
		return expressionNode{}, parser.syntax("expression is too large")
	}
	return expressionNode{kind: kind}, nil
}

func (parser *expressionParser) nested(read func() (expressionNode, error)) (expressionNode, error) {
	parser.nesting++
	if parser.nesting > expressionDepthLimit {
		parser.nesting--
		return expressionNode{}, parser.syntax("expression nesting is too deep")
	}
	value, err := read()
	parser.nesting--
	return value, err
}

func (parser *expressionParser) safeName(name string, at int) error {
	if expressionBlockedNames[name] {
		return expressionSyntax(fmt.Sprintf("blocked name %q", name), at)
	}
	return nil
}

func (parser *expressionParser) parse() (expressionNode, error) {
	output, err := parser.assignment()
	if err != nil {
		return expressionNode{}, err
	}
	parser.acceptServiceCommand(output)
	if parser.take(";") {
		if !parser.action {
			return expressionNode{}, parser.syntax("sequences are only allowed in actions")
		}
		for parser.current().kind != expressionEnd {
			statement, err := parser.assignment()
			if err != nil {
				return expressionNode{}, err
			}
			parser.acceptServiceCommand(statement)
			if !parser.take(";") {
				break
			}
		}
		output, err = parser.make("sequence")
		if err != nil {
			return expressionNode{}, err
		}
	}
	if parser.current().kind != expressionEnd {
		return expressionNode{}, parser.syntax(fmt.Sprintf("unexpected token %q", parser.current().value))
	}
	return output, nil
}

func (parser *expressionParser) acceptServiceCommand(node expressionNode) {
	if !node.serviceCommand || node.serviceReference <= 0 || node.serviceReference > len(parser.serviceReferences) {
		return
	}
	parser.serviceReferences[node.serviceReference-1].accepted = true
}

func (parser *expressionParser) assignment() (expressionNode, error) {
	left, err := parser.conditional()
	if err != nil || !parser.take("=") {
		return left, err
	}
	if !parser.action {
		return expressionNode{}, parser.syntax("assignment is only allowed in actions")
	}
	if left.kind != "identifier" {
		return expressionNode{}, parser.syntax("assignment target must be a component field")
	}
	if strings.HasPrefix(left.name, "$") {
		return expressionNode{}, parser.syntax("the $ namespace is read-only")
	}
	if _, err := parser.nested(parser.assignment); err != nil {
		return expressionNode{}, err
	}
	return parser.make("assign")
}

func (parser *expressionParser) conditional() (expressionNode, error) {
	condition, err := parser.coalesce()
	if err != nil || !parser.take("?") {
		return condition, err
	}
	if _, err := parser.nested(parser.assignment); err != nil {
		return expressionNode{}, err
	}
	if err := parser.expect(":"); err != nil {
		return expressionNode{}, err
	}
	if _, err := parser.nested(parser.assignment); err != nil {
		return expressionNode{}, err
	}
	return parser.make("conditional")
}

func (parser *expressionParser) coalesce() (expressionNode, error) {
	left, err := parser.logicalOr()
	if err != nil {
		return expressionNode{}, err
	}
	for parser.take("??") {
		if left.kind == "logical" {
			return expressionNode{}, parser.syntax("parentheses are required when mixing ?? with && or ||")
		}
		right, err := parser.logicalOr()
		if err != nil {
			return expressionNode{}, err
		}
		if right.kind == "logical" {
			return expressionNode{}, parser.syntax("parentheses are required when mixing ?? with && or ||")
		}
		left, err = parser.make("coalesce")
		if err != nil {
			return expressionNode{}, err
		}
	}
	return left, nil
}

func (parser *expressionParser) logicalOr() (expressionNode, error) {
	left, err := parser.logicalAnd()
	if err != nil {
		return expressionNode{}, err
	}
	for parser.take("||") {
		if _, err := parser.logicalAnd(); err != nil {
			return expressionNode{}, err
		}
		left, err = parser.make("logical")
		if err != nil {
			return expressionNode{}, err
		}
	}
	return left, nil
}

func (parser *expressionParser) logicalAnd() (expressionNode, error) {
	left, err := parser.equality()
	if err != nil {
		return expressionNode{}, err
	}
	for parser.take("&&") {
		if _, err := parser.equality(); err != nil {
			return expressionNode{}, err
		}
		left, err = parser.make("logical")
		if err != nil {
			return expressionNode{}, err
		}
	}
	return left, nil
}

func (parser *expressionParser) equality() (expressionNode, error) {
	left, err := parser.relation()
	if err != nil {
		return expressionNode{}, err
	}
	for expressionStringIn(parser.current().value, "==", "!=", "===", "!==") {
		parser.position++
		if _, err := parser.relation(); err != nil {
			return expressionNode{}, err
		}
		left, err = parser.make("binary")
		if err != nil {
			return expressionNode{}, err
		}
	}
	return left, nil
}

func (parser *expressionParser) relation() (expressionNode, error) {
	left, err := parser.addition()
	if err != nil {
		return expressionNode{}, err
	}
	for expressionStringIn(parser.current().value, "<", "<=", ">", ">=") {
		parser.position++
		if _, err := parser.addition(); err != nil {
			return expressionNode{}, err
		}
		left, err = parser.make("binary")
		if err != nil {
			return expressionNode{}, err
		}
	}
	return left, nil
}

func (parser *expressionParser) addition() (expressionNode, error) {
	left, err := parser.multiplication()
	if err != nil {
		return expressionNode{}, err
	}
	for parser.is("+") || parser.is("-") {
		parser.position++
		if _, err := parser.multiplication(); err != nil {
			return expressionNode{}, err
		}
		left, err = parser.make("binary")
		if err != nil {
			return expressionNode{}, err
		}
	}
	return left, nil
}

func (parser *expressionParser) multiplication() (expressionNode, error) {
	left, err := parser.unary()
	if err != nil {
		return expressionNode{}, err
	}
	for parser.is("*") || parser.is("/") || parser.is("%") {
		parser.position++
		if _, err := parser.unary(); err != nil {
			return expressionNode{}, err
		}
		left, err = parser.make("binary")
		if err != nil {
			return expressionNode{}, err
		}
	}
	return left, nil
}

func (parser *expressionParser) unary() (expressionNode, error) {
	if parser.is("++") || parser.is("--") {
		operator := parser.current()
		parser.position++
		if !parser.action {
			return expressionNode{}, expressionSyntax("updates are only allowed in actions", operator.position)
		}
		target, err := parser.nested(parser.unary)
		if err != nil {
			return expressionNode{}, err
		}
		if err := parser.validateUpdateTarget(target, operator.position); err != nil {
			return expressionNode{}, err
		}
		return parser.make("update")
	}
	if parser.is("!") || parser.is("-") || parser.is("+") {
		parser.position++
		if _, err := parser.nested(parser.unary); err != nil {
			return expressionNode{}, err
		}
		return parser.make("unary")
	}
	return parser.postfix()
}

func expressionStringIn(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if value == candidate {
			return true
		}
	}
	return false
}

func (parser *expressionParser) postfix() (expressionNode, error) {
	value, err := parser.primary()
	if err != nil {
		return expressionNode{}, err
	}
	optionalChain := false
	for {
		switch {
		case parser.take("?."):
			optionalChain = true
			switch {
			case parser.current().kind == expressionIdentifier:
				current := parser.current()
				if err := parser.safeName(current.value, current.position); err != nil {
					return expressionNode{}, err
				}
				parser.position++
				value, err = parser.staticMember(value, current.value, current.position, true)
			case parser.take("["):
				key, keyErr := parser.nested(parser.assignment)
				if keyErr != nil {
					return expressionNode{}, keyErr
				}
				if err := parser.expect("]"); err != nil {
					return expressionNode{}, err
				}
				if key.kind == "literal" {
					if text, ok := key.literal.(string); ok && expressionBlockedNames[text] {
						return expressionNode{}, parser.syntax(fmt.Sprintf("blocked member %q", text))
					}
				}
				value, err = parser.computedMember(value, key, true)
			case parser.take("("):
				_, argsErr := parser.nested(parser.argumentsList)
				if argsErr != nil {
					return expressionNode{}, argsErr
				}
				value, err = parser.call(value, true)
			default:
				return expressionNode{}, parser.syntax("expected an optional member, computed member, or call")
			}
		case parser.take("."):
			current := parser.current()
			if current.kind != expressionIdentifier {
				return expressionNode{}, parser.syntax("expected a member name")
			}
			if err := parser.safeName(current.value, current.position); err != nil {
				return expressionNode{}, err
			}
			parser.position++
			value, err = parser.staticMember(value, current.value, current.position, false)
		case parser.take("["):
			key, keyErr := parser.nested(parser.assignment)
			if keyErr != nil {
				return expressionNode{}, keyErr
			}
			if err := parser.expect("]"); err != nil {
				return expressionNode{}, err
			}
			if key.kind == "literal" {
				if text, ok := key.literal.(string); ok && expressionBlockedNames[text] {
					return expressionNode{}, parser.syntax(fmt.Sprintf("blocked member %q", text))
				}
			}
			value, err = parser.computedMember(value, key, false)
		case parser.take("("):
			_, argsErr := parser.nested(parser.argumentsList)
			if argsErr != nil {
				return expressionNode{}, argsErr
			}
			value, err = parser.call(value, false)
		case parser.is("++") || parser.is("--"):
			operator := parser.current()
			parser.position++
			if !parser.action {
				return expressionNode{}, expressionSyntax("updates are only allowed in actions", operator.position)
			}
			if err := parser.validateUpdateTarget(value, operator.position); err != nil {
				return expressionNode{}, err
			}
			return parser.make("update")
		default:
			if optionalChain {
				return parser.make("chain")
			}
			return value, nil
		}
		if err != nil {
			return expressionNode{}, err
		}
	}
}

func (parser *expressionParser) addServiceReference(alias, service string, at int) int {
	parser.serviceReferences = append(parser.serviceReferences, expressionServiceReference{
		call: expressionServiceCall{Alias: alias, Service: service, Position: at},
	})
	return len(parser.serviceReferences)
}

func (parser *expressionParser) staticMember(base expressionNode, name string, at int, optional bool) (expressionNode, error) {
	node, err := parser.make("member")
	if err != nil {
		return expressionNode{}, err
	}
	node.alias = base.alias
	node.aliasDepth = base.aliasDepth + 1
	if parser.action && base.alias == "$app" && base.aliasDepth == 0 && name == "loader" {
		return expressionNode{}, expressionSyntax("app loader state is binding-only", at)
	}
	if base.alias != "" && base.aliasDepth == 0 && expressionServiceName(name) {
		if base.alias != "$app" {
			return expressionNode{}, expressionSyntax("authored service commands require the $app alias", at)
		}
		node.serviceReference = parser.addServiceReference(base.alias, name, at)
		node.directAlias = base.directAlias && !optional
		return node, nil
	}
	if base.serviceReference > 0 {
		node.serviceReference = base.serviceReference
		node.serviceMethod = base.serviceMethod
		if base.serviceMethod == "" {
			node.serviceMethod = name
			node.directAlias = base.directAlias && !optional
		}
		return node, nil
	}
	return node, nil
}

func (parser *expressionParser) computedMember(base, key expressionNode, optional bool) (expressionNode, error) {
	node, err := parser.make("member")
	if err != nil {
		return expressionNode{}, err
	}
	node.alias = base.alias
	node.aliasDepth = base.aliasDepth + 1
	if parser.action && base.alias == "$app" && base.aliasDepth == 0 {
		if text, ok := key.literal.(string); ok && text == "loader" {
			return expressionNode{}, expressionSyntax("app loader state is binding-only", parser.current().position)
		}
	}
	if base.alias == "$app" && base.aliasDepth == 0 {
		service := ""
		if text, ok := key.literal.(string); ok && expressionServiceName(text) {
			service = text
		}
		node.serviceReference = parser.addServiceReference(base.alias, service, parser.current().position)
		return node, nil
	}
	if base.serviceReference > 0 {
		node.serviceReference = base.serviceReference
		node.serviceMethod = base.serviceMethod
	}
	return node, nil
}

func (parser *expressionParser) call(callee expressionNode, optional bool) (expressionNode, error) {
	node, err := parser.make("call")
	if err != nil {
		return expressionNode{}, err
	}
	node.alias = callee.alias
	node.aliasDepth = callee.aliasDepth
	node.serviceReference = callee.serviceReference
	node.serviceMethod = callee.serviceMethod
	if callee.serviceReference > 0 && callee.serviceMethod != "" && callee.directAlias && !optional {
		node.serviceCommand = true
		parser.serviceReferences[callee.serviceReference-1].call.Method = callee.serviceMethod
	}
	return node, nil
}

func (parser *expressionParser) validateUpdateTarget(target expressionNode, at int) error {
	if target.kind != "identifier" {
		return expressionSyntax("update target must be a component field", at)
	}
	if strings.HasPrefix(target.name, "$") {
		return expressionSyntax("the $ namespace is read-only", at)
	}
	return nil
}

func (parser *expressionParser) argumentsList() (expressionNode, error) {
	if !parser.is(")") {
		for {
			if parser.is(")") {
				return expressionNode{}, parser.syntax("calls reject a trailing comma")
			}
			if _, err := parser.assignment(); err != nil {
				return expressionNode{}, err
			}
			if !parser.take(",") {
				break
			}
		}
	}
	if err := parser.expect(")"); err != nil {
		return expressionNode{}, err
	}
	return expressionNode{}, nil
}

func (parser *expressionParser) arrowParameters() ([]string, bool, error) {
	saved := parser.position
	if !parser.take("(") {
		return nil, false, nil
	}
	params := make([]string, 0, 2)
	seen := make(map[string]struct{})
	if !parser.take(")") {
		for {
			current := parser.current()
			if current.kind != expressionIdentifier {
				parser.position = saved
				return nil, false, nil
			}
			if err := parser.safeName(current.value, current.position); err != nil {
				return nil, false, err
			}
			if strings.HasPrefix(current.value, "$") {
				return nil, false, expressionSyntax("lambda parameters cannot use the $ namespace", current.position)
			}
			if _, duplicate := seen[current.value]; duplicate {
				return nil, false, expressionSyntax(fmt.Sprintf("duplicate lambda parameter %q", current.value), current.position)
			}
			seen[current.value] = struct{}{}
			params = append(params, current.value)
			parser.position++
			if !parser.take(",") {
				break
			}
			if parser.is(")") {
				return nil, false, parser.syntax("lambdas reject a trailing comma")
			}
		}
		if !parser.take(")") {
			parser.position = saved
			return nil, false, nil
		}
	}
	if !parser.take("=>") {
		parser.position = saved
		return nil, false, nil
	}
	return params, true, nil
}

func (parser *expressionParser) primary() (expressionNode, error) {
	current := parser.current()
	if current.kind == expressionLiteral {
		parser.position++
		node, err := parser.make("literal")
		node.literal = current.literal
		return node, err
	}
	if current.kind == expressionIdentifier {
		parser.position++
		if err := parser.safeName(current.value, current.position); err != nil {
			return expressionNode{}, err
		}
		if !parser.action && strings.HasPrefix(current.value, "$") {
			return expressionNode{}, expressionSyntax("the $ namespace is action-only", current.position)
		}
		node, err := parser.make("identifier")
		node.name = current.value
		if strings.HasPrefix(current.value, "$") {
			node.alias = current.value
			node.directAlias = true
		}
		return node, err
	}
	if parser.is("(") {
		_, arrow, err := parser.arrowParameters()
		if err != nil {
			return expressionNode{}, err
		}
		if arrow {
			if _, err := parser.nested(parser.assignment); err != nil {
				return expressionNode{}, err
			}
			return parser.make("lambda")
		}
		if err := parser.expect("("); err != nil {
			return expressionNode{}, err
		}
		grouped, err := parser.nested(parser.assignment)
		if err != nil {
			return expressionNode{}, err
		}
		if err := parser.expect(")"); err != nil {
			return expressionNode{}, err
		}
		node, err := parser.make("group")
		node.alias = grouped.alias
		node.aliasDepth = grouped.aliasDepth
		node.serviceReference = grouped.serviceReference
		node.serviceMethod = grouped.serviceMethod
		return node, err
	}
	if parser.take("[") {
		if !parser.is("]") {
			for {
				if _, err := parser.nested(parser.assignment); err != nil {
					return expressionNode{}, err
				}
				if !parser.take(",") {
					break
				}
				if parser.is("]") {
					return expressionNode{}, parser.syntax("arrays reject a trailing comma")
				}
			}
		}
		if err := parser.expect("]"); err != nil {
			return expressionNode{}, err
		}
		return parser.make("array")
	}
	if parser.take("{") {
		for !parser.is("}") {
			key := parser.current()
			if key.kind != expressionIdentifier && !(key.kind == expressionLiteral && expressionLiteralIsString(key.literal)) {
				return expressionNode{}, expressionSyntax("expected an object key", key.position)
			}
			parser.position++
			keyName := key.value
			if text, ok := key.literal.(string); ok {
				keyName = text
			}
			if err := parser.safeName(keyName, key.position); err != nil {
				return expressionNode{}, err
			}
			if err := parser.expect(":"); err != nil {
				return expressionNode{}, err
			}
			if _, err := parser.nested(parser.assignment); err != nil {
				return expressionNode{}, err
			}
			if !parser.take(",") {
				break
			}
			if parser.is("}") {
				break
			}
		}
		if err := parser.expect("}"); err != nil {
			return expressionNode{}, err
		}
		return parser.make("object")
	}
	return expressionNode{}, expressionSyntax("expected an expression", current.position)
}

func expressionLiteralIsString(value any) bool {
	_, ok := value.(string)
	return ok
}
