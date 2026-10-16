package render

import (
	"math"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/kitwork/engine/value"
)

// A {{ if … }} reads its whole condition as one expression and compares the values of both sides with
// the VM's operators (16/10/2026, the owner's E3). Until then it read the first word as the value and
// the raw text after an operator as the other side: {{ if guide.file.slug == slug }} compared the slug
// with the text "slug", {{ if !x }} looked up a key named "!x" (always false), a || b and a && b counted
// only a, === was not an operator, and "two words" was cut at the space.
//
// The grammar, lowest first, ordered as JavaScript orders it:
//
//	a ? b : c
//	a ?? b
//	a || b
//	a && b
//	a == b   a != b   a === b   a !== b
//	a < b    a > b    a <= b    a >= b
//	a + b    a - b
//	a * b    a / b    a % b
//	!a   -a   +a
//	a.b   a.b(…)   ( … )
//	"text"  'text'  12  1.5e3  true  false  null  undefined  NaN  Infinity  name  .name  .
//
// The operators are the VM's: == and === are one operator (value.Equal: no conversion, numbers
// exactly), so "1" == 1 is false and 0.1 + 0.2 === 0.3 is false; != and !== are its negation; < > <= >=
// are value.Less and its kin, which order numbers with numbers and texts with texts (null counts as 0,
// NaN is ordered with nothing); && and || give the value that decided, as the VM's AND and OR; ! is
// NOT; -a is a * -1 and +a the number JavaScript's ToNumber makes of a, as the VM compiles them. A name
// is looked up as every template expression looks one up (resolvePath): the scope of for and let first,
// then the data, then the data of $name; .name is the current data's. A call is a method called after a
// dot, as everywhere in a template. A failure anywhere in the condition (a method that is not a
// function, "a" - 1) is the condition's failure, as it ends a script's run.

// conditionFailure is a condition that cannot be read: where in the condition (a byte offset) and why,
// in the author's words.
type conditionFailure struct {
	at      int
	message string
}

// conditionNestingLimit is how deep a condition may nest (parentheses, ternaries, arguments, !, - and
// +), so a condition written to be deep is unreadable instead of growing the stack.
const conditionNestingLimit = 64

// conditionOperators are the operators a condition knows, longest first, so == is never read as = =.
// = & | are no operators of the grammar: they are here to be named when they are written by mistake.
var conditionOperators = [...]string{
	"===", "!==",
	"==", "!=", "<=", ">=", "&&", "||", "??",
	"<", ">", "+", "-", "*", "/", "%", "?", ":", "!", "=", "&", "|",
}

type conditionParser struct {
	text  string
	at    int
	depth int
	// last is the operator or the bracket read last, to say what a missing value was missing after;
	// "" while the first value is read
	last string
}

// compileCondition reads the whole text after {{ if as one expression.
func compileCondition(text string) (compiled *expression, failure *conditionFailure) {
	parser := &conditionParser{text: text}
	defer func() {
		if recovered := recover(); recovered != nil {
			stopped, ok := recovered.(*conditionFailure)
			if !ok {
				panic(recovered)
			}
			compiled, failure = nil, stopped
		}
	}()
	parser.space()
	if parser.at == len(text) {
		parser.fail(0, "thiếu điều kiện: viết {{ if tên }} hoặc {{ if a == b }}")
	}
	compiled = parser.conditional()
	parser.space()
	if parser.at < len(text) {
		parser.leftover(true)
	}
	return compiled, nil
}

// describe is the message of a condition that cannot be read, naming the tag as it was written.
func (failure *conditionFailure) describe(condition string) string {
	if condition == "" {
		return "{{ if }}: " + failure.message
	}
	return "{{ if " + condition + " }}: " + failure.message
}

func (p *conditionParser) fail(at int, message string) {
	panic(&conditionFailure{at: at, message: message})
}

func (p *conditionParser) enter() {
	p.depth++
	if p.depth > conditionNestingLimit {
		p.fail(p.at, "điều kiện lồng quá sâu (quá "+strconv.Itoa(conditionNestingLimit)+" tầng)")
	}
}

func (p *conditionParser) leave() { p.depth-- }

func (p *conditionParser) space() {
	for p.at < len(p.text) {
		r, size := utf8.DecodeRuneInString(p.text[p.at:])
		if !unicode.IsSpace(r) {
			return
		}
		p.at += size
	}
}

// peek is the operator at the next character, or "".
func (p *conditionParser) peek() string {
	p.space()
	rest := p.text[p.at:]
	for _, operator := range conditionOperators {
		if strings.HasPrefix(rest, operator) {
			return operator
		}
	}
	return ""
}

// take reads operator when it is the next one, as a whole: take("<") does not read the < of <=.
func (p *conditionParser) take(operator string) bool {
	if p.peek() != operator {
		return false
	}
	p.at += len(operator)
	p.last = operator
	return true
}

// next is the character at p.at after any space, or 0 at the end.
func (p *conditionParser) next() byte {
	p.space()
	if p.at >= len(p.text) {
		return 0
	}
	return p.text[p.at]
}

func (p *conditionParser) conditional() *expression {
	p.enter()
	defer p.leave()
	test := p.nullish()
	if p.peek() != "?" {
		return test
	}
	question := p.at
	p.take("?")
	then := p.conditional()
	if !p.take(":") {
		p.fail(question, "a ? b : c thiếu phần : c")
	}
	alt := p.conditional()
	return &expression{kind: expressionTernary, left: test, right: then, alt: alt}
}

// binary reads the operands of next joined left to right by the operators of one level.
func (p *conditionParser) binary(next func() *expression, operators map[string]expressionKind) *expression {
	left := next()
	for {
		operator := p.peek()
		kind, ok := operators[operator]
		if !ok {
			return left
		}
		p.take(operator)
		left = &expression{kind: kind, left: left, right: next()}
	}
}

var (
	conditionNullish        = map[string]expressionKind{"??": expressionNullish}
	conditionOr             = map[string]expressionKind{"||": expressionOr}
	conditionAnd            = map[string]expressionKind{"&&": expressionAnd}
	conditionEquality       = map[string]expressionKind{"==": expressionEqual, "===": expressionEqual, "!=": expressionNotEqual, "!==": expressionNotEqual}
	conditionRelational     = map[string]expressionKind{"<": expressionLess, ">": expressionGreater, "<=": expressionLessEqual, ">=": expressionGreaterEqual}
	conditionAdditive       = map[string]expressionKind{"+": expressionAdd, "-": expressionSubtract}
	conditionMultiplicative = map[string]expressionKind{"*": expressionMultiply, "/": expressionDivide, "%": expressionModulo}
)

func (p *conditionParser) nullish() *expression    { return p.binary(p.or, conditionNullish) }
func (p *conditionParser) or() *expression         { return p.binary(p.and, conditionOr) }
func (p *conditionParser) and() *expression        { return p.binary(p.equality, conditionAnd) }
func (p *conditionParser) equality() *expression   { return p.binary(p.relational, conditionEquality) }
func (p *conditionParser) relational() *expression { return p.binary(p.additive, conditionRelational) }
func (p *conditionParser) additive() *expression {
	return p.binary(p.multiplicative, conditionAdditive)
}
func (p *conditionParser) multiplicative() *expression {
	return p.binary(p.unary, conditionMultiplicative)
}

func (p *conditionParser) unary() *expression {
	var kind expressionKind
	switch {
	case p.take("!"):
		kind = expressionNot
	case p.take("-"):
		kind = expressionNegate
	case p.take("+"):
		kind = expressionNumber
	default:
		return p.postfix()
	}
	p.enter()
	defer p.leave()
	return &expression{kind: kind, left: p.unary()}
}

func (p *conditionParser) postfix() *expression {
	start := p.at
	current := p.primary()
	for {
		switch p.next() {
		case '.':
			p.at++
			p.space()
			name := p.name()
			if name == "" {
				p.fail(p.at, "thiếu tên sau dấu chấm")
			}
			if p.next() == '(' {
				open := p.at
				p.at++
				p.last = "("
				current = &expression{kind: expressionMethod, left: current, parts: []string{name}, args: p.arguments(open)}
				continue
			}
			current = member(current, name)
		case '(':
			p.fail(p.at, "template gọi một hàm sau dấu chấm, như x.has(\"a\"): "+strings.TrimSpace(p.text[start:p.at])+"(…) không gọi được")
		case '?':
			// ?. is an optional chain unless a digit follows (a ?.5 : 1 is a ternary), as the VM's lexer
			// reads it; a template has none, and it was read as a ternary that lacks its : c (16/10/2026)
			if p.at+2 >= len(p.text) || p.text[p.at+1] != '.' || p.text[p.at+2] >= '0' && p.text[p.at+2] <= '9' {
				return current
			}
			p.fail(p.at, "template chưa có ?.: viết a.khoá, khoá của null đã là null")
		default:
			return current
		}
	}
}

// member is current.name: a name joins the path it follows, so the path resolves as a template path
// does (scope, data, $name); after any other value it is a key of that value.
func member(current *expression, name string) *expression {
	if (current.kind == expressionPath && !(len(current.parts) == 1 && current.parts[0] == ".")) ||
		current.kind == expressionMember {
		parts := make([]string, len(current.parts), len(current.parts)+1)
		copy(parts, current.parts)
		return &expression{kind: current.kind, left: current.left, parts: append(parts, name)}
	}
	return &expression{kind: expressionMember, left: current, parts: []string{name}}
}

func (p *conditionParser) arguments(open int) []*expression {
	if p.next() == ')' {
		p.at++
		return nil
	}
	var args []*expression
	for {
		args = append(args, p.conditional())
		switch p.next() {
		case ')':
			p.at++
			return args
		case ',':
			p.at++
			p.last = ","
			if p.next() == ')' {
				p.fail(p.at, "thừa dấu phẩy trước )")
			}
		case 0:
			p.fail(open, "lời gọi mở bằng ( chưa có ) đóng")
		default:
			p.leftover(true)
		}
	}
}

func (p *conditionParser) primary() *expression {
	char := p.next()
	switch {
	case char == '(':
		open := p.at
		p.at++
		p.last = "("
		inner := p.conditional()
		switch p.next() {
		case ')':
			p.at++
			return inner
		case 0:
			p.fail(open, "dấu ( chưa có dấu ) đóng")
		}
		p.leftover(true)
	case char == '"' || char == '\'':
		return &expression{kind: expressionLiteral, value: value.New(p.quoted())}
	case char >= '0' && char <= '9',
		char == '.' && p.at+1 < len(p.text) && p.text[p.at+1] >= '0' && p.text[p.at+1] <= '9':
		// .5 is a number, as in JavaScript and the VM's lexer (16/10/2026: it was the key "5" of the
		// current data)
		return &expression{kind: expressionLiteral, value: value.New(p.number())}
	case char == '.':
		p.at++
		if name := p.name(); name != "" {
			return &expression{kind: expressionPath, parts: []string{"", name}}
		}
		return &expression{kind: expressionPath, parts: []string{"."}}
	}
	if word := p.name(); word != "" {
		switch word {
		case "true":
			return &expression{kind: expressionLiteral, value: value.New(true)}
		case "false":
			return &expression{kind: expressionLiteral, value: value.New(false)}
		case "null", "undefined":
			return &expression{kind: expressionLiteral, value: value.Value{K: value.Nil}}
		case "NaN":
			return &expression{kind: expressionLiteral, value: value.New(math.NaN())}
		case "Infinity":
			return &expression{kind: expressionLiteral, value: value.New(math.Inf(1))}
		}
		return &expression{kind: expressionPath, parts: []string{word}}
	}
	operator := p.peek()
	switch {
	case p.last != "" && (char == 0 || char == ')' || char == ',' || (operator != "" && operator != "=")):
		// after an operator or a bracket, where its value should be
		p.fail(p.at, "thiếu giá trị sau "+p.last)
	case p.last == "" && operator != "" && operator != "=" && operator != "&" && operator != "|":
		p.fail(p.at, "thiếu giá trị trước "+operator)
	}
	p.leftover(false)
	return nil
}

// leftover is text where it cannot stand: after a value, where an operator, a closing bracket or the
// end of the condition should be (afterValue), or where a value should begin.
func (p *conditionParser) leftover(afterValue bool) {
	p.space()
	rest := p.text[p.at:]
	switch {
	case strings.HasPrefix(rest, "=") && !strings.HasPrefix(rest, "=="):
		p.fail(p.at, "một dấu = là phép gán, điều kiện không gán: so sánh viết == hoặc ===")
	case strings.HasPrefix(rest, "&") && !strings.HasPrefix(rest, "&&"):
		p.fail(p.at, "một dấu & không phải toán tử: và viết &&")
	case strings.HasPrefix(rest, "|") && !strings.HasPrefix(rest, "||"):
		p.fail(p.at, "một dấu | không phải toán tử: hoặc viết ||")
	case strings.HasPrefix(rest, ")"):
		p.fail(p.at, "dấu ) không có dấu ( mở")
	case strings.HasPrefix(rest, ":"):
		p.fail(p.at, "dấu : không có dấu ? trước nó (a ? b : c)")
	case strings.HasPrefix(rest, "["):
		p.fail(p.at, "template chưa đọc a[…]: một khoá viết a.khoá")
	case strings.HasPrefix(rest, "`"):
		p.fail(p.at, "template chưa có chuỗi `…`: viết trong nháy đơn hoặc nháy kép")
	}
	word := rest
	if end := strings.IndexFunc(rest, unicode.IsSpace); end > 0 {
		word = rest[:end]
	}
	if afterValue {
		p.fail(p.at, "thừa "+strconv.Quote(word)+": hai giá trị cần một toán tử giữa chúng (==, ===, !=, <, >, &&, ||, …)")
	}
	p.fail(p.at, "không đọc được "+strconv.Quote(word))
}

// nameRune is a character of a name: a letter, a digit, _ or $.
func nameRune(r rune) bool {
	return r == '_' || r == '$' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

// name reads a name. After a dot it may start with a digit (m.0, the key "0"); a value never does,
// since a digit there starts a number.
func (p *conditionParser) name() string {
	start := p.at
	for p.at < len(p.text) {
		r, size := utf8.DecodeRuneInString(p.text[p.at:])
		if !nameRune(r) {
			break
		}
		p.at += size
	}
	return p.text[start:p.at]
}

// number reads a decimal number: 12, 1.5, .5, 1., 1e3, 2.5E-2, as JavaScript writes one (16/10/2026:
// 1. could not be read).
func (p *conditionParser) number() float64 {
	start := p.at
	digits := func() {
		for p.at < len(p.text) && p.text[p.at] >= '0' && p.text[p.at] <= '9' {
			p.at++
		}
	}
	digits()
	if p.at < len(p.text) && p.text[p.at] == '.' {
		p.at++
		digits()
	}
	if p.at < len(p.text) && (p.text[p.at] == 'e' || p.text[p.at] == 'E') {
		exponent := p.at + 1
		if exponent < len(p.text) && (p.text[exponent] == '+' || p.text[exponent] == '-') {
			exponent++
		}
		if exponent < len(p.text) && p.text[exponent] >= '0' && p.text[exponent] <= '9' {
			p.at = exponent
			digits()
		}
	}
	end := p.at
	if r, _ := utf8.DecodeRuneInString(p.text[p.at:]); p.at < len(p.text) && nameRune(r) {
		p.name()
		p.fail(start, "không đọc được "+strconv.Quote(p.text[start:p.at])+": một tên không bắt đầu bằng chữ số")
	}
	// past float64's range ParseFloat gives ±Inf with its error, as JavaScript reads 1e400
	number, _ := strconv.ParseFloat(p.text[start:end], 64)
	return number
}

// quoted reads a quoted text, its escapes read as the VM's lexer reads them.
func (p *conditionParser) quoted() string {
	quote := p.text[p.at]
	open := p.at
	p.at++
	start := p.at
	for p.at < len(p.text) && p.text[p.at] != quote {
		if p.text[p.at] == '\\' && p.at+1 < len(p.text) {
			p.at++
		}
		p.at++
	}
	if p.at >= len(p.text) {
		p.fail(open, "chuỗi mở bằng "+string(quote)+" chưa đóng")
	}
	content := p.text[start:p.at]
	p.at++
	return unescapeConditionText(content, quote)
}

// unescapeConditionText is the compiler's unescapeJSString, so a template's text and a script's read
// the same escapes: an escape Go does not know stays as written.
func unescapeConditionText(text string, quote byte) string {
	if strings.IndexByte(text, '\\') < 0 {
		return text
	}
	var builder strings.Builder
	builder.Grow(len(text))
	for len(text) > 0 {
		char, _, tail, err := strconv.UnquoteChar(text, quote)
		if err != nil {
			builder.WriteByte(text[0])
			text = text[1:]
			continue
		}
		builder.WriteRune(char)
		text = tail
	}
	return builder.String()
}

// ifCondition is the text of a {{ if … }} tag after the word if, and where it starts in the tag.
func ifCondition(tag string) (string, int) {
	inner := tag[2 : len(tag)-2]
	start := 2 + len(inner) - len(strings.TrimLeftFunc(inner, unicode.IsSpace)) + len("if")
	rest := tag[start : len(tag)-2]
	start += len(rest) - len(strings.TrimLeftFunc(rest, unicode.IsSpace))
	return strings.TrimRightFunc(tag[start:len(tag)-2], unicode.IsSpace), start
}

// sourceLocation is line:column of offset in tokens[index], tokens being one source cut in pieces
// (specializeTokens), the column counted in characters from 1, as the compiler counts it.
func sourceLocation(tokens []string, index, offset int) string {
	line, column := 1, 1
	count := func(text string) {
		for _, r := range text {
			if r == '\n' {
				line++
				column = 1
			} else {
				column++
			}
		}
	}
	for _, token := range tokens[:index] {
		count(token)
	}
	count(tokens[index][:offset])
	return strconv.Itoa(line) + ":" + strconv.Itoa(column)
}
