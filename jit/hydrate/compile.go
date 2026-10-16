// Package hydrate is the server half of the "frontend bytecode VM": it compiles a small,
// deliberately-constrained expression subset (the same one authors write in data-* attributes)
// into a compact IR — a nested JSON array the tiny client interpreter walks. The wire carries SOURCE,
// never IR: the client keeps a matching parser and reads the same expressions from the hand-written
// data-kit-* attributes and from the values of the eight grouped data-kitwork-* attributes the engine
// writes (pairs.go). The IR is the server's own form — verification, eval, tests.
//
// IR shapes (each is a JSON array):
//
//	["#", literal]            literal (number | string | bool | null)
//	["$", "name"]            variable read from scope ("$" itself = the PAGE scope object)
//	[op, left, right]        binary: + - * / % > < >= <= == != === !== && || ??
//	                         (== and != coerce like JavaScript; === and !== are strict; ?? takes
//	                         the right side only when the left is null or undefined)
//	                         a?.b, a?.[k] and f?.() compile to the plain ".", "idx" and "call"
//	                         (o.m?.() to "()"): every access is already null-safe, so the ? adds
//	                         nothing to keep
//	["u!", e] / ["u-", e]    unary not / negate; ["u+", e] is Number(e), as +e in JavaScript
//	                         (16/10/2026)
//	["?", c, a, b]           ternary c ? a : b
//	["=", "name", value]     assignment to a scope variable (lexical: owner scope, else nearest)
//	["=$", "name", value]    assignment to the PAGE scope: $.name = value — the same $ the
//	                         server template language uses for its root data
//	["++", "name", prefix]   update: count++ / ++count, and ["--", …] for the decrement; the page-
//	["++$", "name", prefix]  scope form is $.count++. prefix is true for ++count. The value is
//	                         coerced to a NUMBER first, exactly like JavaScript — so "5"++ is 6,
//	                         where count = count + 1 would have made it "51". That difference is
//	                         why this is its own op rather than sugar for the assignment. Postfix
//	                         yields the old number, prefix the new one. Targets are the ones `=`
//	                         accepts: a name, or $.name.
//	[".", obj, "name"]       member access obj.name
//	["idx", obj, key]        index access obj[key] — key is an expression (number or string)
//	["()", obj, "name", []]  method call obj.name(args...); ["()", obj, key, []] with key an
//	                         expression is obj[key](args...) (16/10/2026)
//	["{}", [[k, v], …]]      object literal { count: 5, open: false } — a BLUEPRINT, parsed not
//	                         eval'd (objects allow a trailing comma, matching the server language)
//	["[]", [e, …]]           array literal [1, 2, 3] (arrays REJECT a trailing comma, ditto)
//	[";", e1, e2, …]         sequence: count = 5; open = true — evaluates left to right,
//	                         yields the last value
//	["=>", [params], body, source]
//	                         lambda: () => count = count + 1 — a NAMED IR TREE, i.e. code-as-data.
//	                         The body is this same grammar; parens are required around params.
//	                         source is the lambda as written, its text as JavaScript gives a
//	                         function's (16/10/2026)
//	["call", callee, [args]] bare call: inc() — callee must evaluate to a lambda value
//
// The line the grammar never crosses is ARBITRARY CODE: a lambda here is not JavaScript — it is a
// compiled IR tree walked by the same budgeted walker as everything else. Nothing is ever handed
// to eval/new Function, there are no loops, and the op budget stops runaway recursion.
package hydrate

import (
	"bytes"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/kitwork/engine/value"
)

type tok struct {
	t string // "num" | "str" | "id" | "op" | "eof"
	v string
	// at is where the token starts in the source, so a lambda keeps its source as written (16/10/2026,
	// as the kernel's tokens keep p)
	at int
}

func isIDStart(c byte) bool {
	return c == '_' || c == '$' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
}
func isIDPart(c byte) bool { return isIDStart(c) || (c >= '0' && c <= '9') }
func isDigit(c byte) bool  { return c >= '0' && c <= '9' }

// lex turns the source into tokens. It is byte-oriented for the ASCII structure; string-literal
// contents are sliced from the source so UTF-8 (e.g. Vietnamese, emoji) is preserved verbatim.
// strMark leads a string token's value, so no string can be mistaken for the operator it spells:
// the parser compares a token's value with "!", "(", "[" and the rest, and before 03/10 the string
// '!' WAS the not operator — data-kit-text="'!'" failed to parse, as did '(' or '-'. No operator
// starts with a NUL byte; the parser strips the mark where it reads a string's value.
const strMark = "\x00"

func lex(s string) ([]tok, error) {
	var out []tok
	i, n := 0, len(s)
	for i < n {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case isDigit(c) || (c == '.' && i+1 < n && isDigit(s[i+1])):
			// A number as JavaScript writes one — 12, 1.5, .5, 5., 1e-13, 2E3, 0x1f, 0o17, 0b101 — and
			// read as Number reads it (16/10/2026: 1e-13 and 0x1f were refused, on both ends). The kernel's
			// lex reads the same with one regular expression.
			j := numberEnd(s, i)
			out = append(out, tok{"num", s[i:j], i})
			i = j
		case c == '"' || c == '\'':
			// A string reads its escapes the way JavaScript does: \n \t \r \b \f \v \0, \' \" \\,
			// \xHH, \uXXXX, \u{X…}, and any other escaped character stands for itself. Before 30/09 there
			// were none — 'a\nb' was five characters, value.split('\n') split on a backslash, and
			// 'it\'s' ended at the escaped quote and failed to parse. \xHH and \u{X…} came 16/10/2026,
			// with a string as JavaScript's UTF-16 units: '\ud83d\ude00' is '😀', and '\ud800' keeps its
			// lone half (jstext.go).
			q := c
			j := i + 1
			var u []uint16
			closed := false
			for j < n {
				ch := s[j]
				if ch == q {
					closed = true
					break
				}
				if ch != '\\' || j+1 >= n {
					r, size := utf8.DecodeRuneInString(s[j:])
					if r >= 0x10000 {
						hi, lo := utf16.EncodeRune(r)
						u = append(u, uint16(hi), uint16(lo))
					} else {
						u = append(u, uint16(r))
					}
					j += size
					continue
				}
				j++
				if code, width, ok := hexEscape(s[j:]); ok {
					if code > 0x10FFFF {
						return nil, errors.New("hydrate: undefined Unicode code-point")
					}
					if code >= 0x10000 {
						hi, lo := utf16.EncodeRune(rune(code))
						u = append(u, uint16(hi), uint16(lo))
					} else {
						u = append(u, uint16(code))
					}
					j += width
					continue
				}
				switch e := s[j]; e {
				case '\n':
					// a backslash at the end of a line continues the string on the next, as in JavaScript
				case 'n':
					u = append(u, '\n')
				case 't':
					u = append(u, '\t')
				case 'r':
					u = append(u, '\r')
				case 'b':
					u = append(u, '\b')
				case 'f':
					u = append(u, '\f')
				case 'v':
					u = append(u, '\v')
				case '0':
					u = append(u, 0)
				default:
					// \' \" \\ \/ and anything else: the character itself
					r, size := utf8.DecodeRuneInString(s[j:])
					if r >= 0x10000 {
						hi, lo := utf16.EncodeRune(r)
						u = append(u, uint16(hi), uint16(lo))
					} else {
						u = append(u, uint16(r))
					}
					j += size - 1
				}
				j++
			}
			if !closed {
				return nil, errors.New("hydrate: unterminated string")
			}
			out = append(out, tok{"str", strMark + fromUnits(u), i})
			i = j + 1
		case isIDStart(c):
			j := i
			for j < n && isIDPart(s[j]) {
				j++
			}
			out = append(out, tok{"id", s[i:j], i})
			i = j
		default:
			// Longest match first: `===` must not lex as `==` + `=`.
			if i+2 < n {
				if three := s[i : i+3]; three == "===" || three == "!==" {
					out = append(out, tok{"op", three, i})
					i += 3
					continue
				}
			}
			if i+1 < n {
				if two := s[i : i+2]; two == "==" || two == "!=" || two == ">=" || two == "<=" || two == "&&" || two == "||" || two == "=>" || two == "++" || two == "--" || two == "??" ||
					// ?. is optional chaining — unless a digit follows: a ? .5 : 1 is a ternary.
					(two == "?." && !(i+2 < n && s[i+2] >= '0' && s[i+2] <= '9')) {
					out = append(out, tok{"op", two, i})
					i += 2
					continue
				}
			}
			if strings.IndexByte("+-*/%<>!?:().,={}[];", c) >= 0 {
				out = append(out, tok{"op", string(c), i})
				i++
				continue
			}
			return nil, errors.New("hydrate: unexpected character '" + string(c) + "'")
		}
	}
	return append(out, tok{"eof", "", n}), nil
}

// numberEnd is where the number that starts at i ends, as the kernel's
// /^(0x[\da-f]+|0o[0-7]+|0b[01]+|(\d+\.?\d*|\.\d+)(e[+-]?\d+)?)/i reads it.
func numberEnd(s string, i int) int {
	n := len(s)
	if i+2 < n && s[i] == '0' {
		var digit func(byte) bool
		switch s[i+1] {
		case 'x', 'X':
			digit = func(c byte) bool { return isDigit(c) || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F' }
		case 'o', 'O':
			digit = func(c byte) bool { return c >= '0' && c <= '7' }
		case 'b', 'B':
			digit = func(c byte) bool { return c == '0' || c == '1' }
		}
		if digit != nil && digit(s[i+2]) {
			j := i + 2
			for j < n && digit(s[j]) {
				j++
			}
			return j
		}
	}
	j := i
	for j < n && isDigit(s[j]) {
		j++
	}
	if j < n && s[j] == '.' {
		j++
		for j < n && isDigit(s[j]) {
			j++
		}
	}
	if j < n && (s[j] == 'e' || s[j] == 'E') {
		k := j + 1
		if k < n && (s[k] == '+' || s[k] == '-') {
			k++
		}
		if k < n && isDigit(s[k]) {
			for k < n && isDigit(s[k]) {
				k++
			}
			j = k
		}
	}
	return j
}

// hexEscape reads the escape after a backslash that names a code point: xHH, uXXXX or u{X…}, and how
// many bytes it took, as the kernel's /^(?:x([\da-f]{2})|u([\da-f]{4})|u\{([\da-f]+)\})/i does.
func hexEscape(s string) (uint64, int, bool) {
	hex := func(t string) bool {
		for i := 0; i < len(t); i++ {
			c := t[i]
			if !(isDigit(c) || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
				return false
			}
		}
		return t != ""
	}
	switch {
	case len(s) >= 3 && s[0] == 'x' && hex(s[1:3]):
		code, _ := strconv.ParseUint(s[1:3], 16, 64)
		return code, 3, true
	case len(s) >= 5 && s[0] == 'u' && hex(s[1:5]):
		code, _ := strconv.ParseUint(s[1:5], 16, 64)
		return code, 5, true
	case len(s) >= 3 && s[0] == 'u' && s[1] == '{':
		end := strings.IndexByte(s, '}')
		if end > 2 && hex(s[2:end]) {
			code, err := strconv.ParseUint(s[2:end], 16, 64)
			if err != nil {
				code = 0x110000 // past every code point: an error, as String.fromCodePoint throws
			}
			return code, end + 1, true
		}
	}
	return 0, 0, false
}

var precedence = map[string]int{
	"||": 1, "??": 1, "&&": 2, "==": 3, "!=": 3, "===": 3, "!==": 3, ">": 4, "<": 4, ">=": 4, "<=": 4, "+": 5, "-": 5, "*": 6, "/": 6, "%": 6,
}

type parser struct {
	src  string
	toks []tok
	pos  int
	// depth is how deep the parser recurses now (nestingLimit).
	depth int
}

// nestingLimit bounds how deep an expression may nest, as the server language's compiler does
// (14/10/2026): the parser and every walker of the IR (the render of a .ui page, the check for safe, the
// JSON a page ships) recurse as deep as it nests, so an expression nested as deep as it is long would
// overflow the Go stack of the shared host while a page compiles. Past it the expression is an error.
const nestingLimit = 1000

var errNested = errors.New("hydrate: biểu thức lồng quá sâu (hơn 1000 tầng ngoặc, toán tử hay mắt xích a.b.c): tách bớt")

// enter counts one level of the parser's recursion; leave undoes it.
func (p *parser) enter() error {
	if p.depth >= nestingLimit {
		return errNested
	}
	p.depth++
	return nil
}

func (p *parser) leave() { p.depth-- }

// tooDeep reports an IR nested deeper than nestingLimit, walked without recursion: a chain a.b.c… or
// 1 + 1 + … nests the IR without the parser recursing.
func tooDeep(node any) bool {
	type pending struct {
		node  any
		depth int
	}
	stack := []pending{{node, 1}}
	for len(stack) > 0 {
		top := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if top.depth > nestingLimit {
			return true
		}
		if list, ok := top.node.([]any); ok {
			for _, child := range list {
				stack = append(stack, pending{child, top.depth + 1})
			}
		}
	}
	return false
}

func (p *parser) peek() tok {
	if p.pos >= len(p.toks) {
		return tok{"eof", "", 0}
	}
	return p.toks[p.pos]
}
func (p *parser) next() tok {
	t := p.peek()
	if p.pos < len(p.toks) {
		p.pos++
	}
	return t
}
func (p *parser) eat(v string) error {
	if p.peek().v != v {
		return errors.New("hydrate: expected '" + v + "', got '" + p.peek().v + "'")
	}
	p.next()
	return nil
}

// The grammar (precedence low→high): sequence → assign → ternary → binary → unary → postfix →
// primary. Each stage returns the IR directly, so parse and codegen are one pass.

// sequence parses `expr; expr; …` (a trailing ; is fine). A single expression compiles exactly as
// before — the [";"] wrapper only appears when there really is a sequence.
func (p *parser) sequence() (any, error) {
	first, err := p.assign()
	if err != nil {
		return nil, err
	}
	if p.peek().v != ";" {
		return first, nil
	}
	exprs := []any{";", first}
	for p.peek().v == ";" {
		p.next()
		if p.peek().t == "eof" {
			break
		}
		e, err := p.assign()
		if err != nil {
			return nil, err
		}
		exprs = append(exprs, e)
	}
	if len(exprs) == 2 {
		return first, nil
	}
	return exprs, nil
}

func (p *parser) assign() (any, error) {
	if err := p.enter(); err != nil {
		return nil, err
	}
	defer p.leave()
	left, err := p.ternary()
	if err != nil {
		return nil, err
	}
	if p.peek().v == "=" {
		p.next()
		val, err := p.assign()
		if err != nil {
			return nil, err
		}
		if arr, ok := left.([]any); ok {
			if len(arr) == 2 && arr[0] == "$" {
				if arr[1] == "$" {
					return nil, errors.New("hydrate: cannot assign to $ itself")
				}
				return []any{"=", arr[1], val}, nil
			}
			// $.name = value — the explicit page-scope address. The ONLY member target that is
			// assignable; general obj.prop assignment stays out of the grammar.
			if len(arr) == 3 && arr[0] == "." {
				if inner, ok := arr[1].([]any); ok && len(inner) == 2 && inner[0] == "$" && inner[1] == "$" {
					return []any{"=$", arr[2], val}, nil
				}
			}
		}
		return nil, errors.New("hydrate: invalid assignment target")
	}
	return left, nil
}

func (p *parser) ternary() (any, error) {
	c, err := p.binary(0)
	if err != nil {
		return nil, err
	}
	if p.peek().v == "?" {
		p.next()
		a, err := p.assign()
		if err != nil {
			return nil, err
		}
		if err := p.eat(":"); err != nil {
			return nil, err
		}
		b, err := p.assign()
		if err != nil {
			return nil, err
		}
		return []any{"?", c, a, b}, nil
	}
	return c, nil
}

func (p *parser) binary(min int) (any, error) {
	left, err := p.unary()
	if err != nil {
		return nil, err
	}
	for {
		t := p.peek()
		if t.t != "op" {
			break
		}
		pr, ok := precedence[t.v]
		if !ok || pr < min {
			break
		}
		op := p.next().v
		right, err := p.binary(pr + 1)
		if err != nil {
			return nil, err
		}
		left = []any{op, left, right}
	}
	return left, nil
}

func (p *parser) unary() (any, error) {
	if v := p.peek().v; v == "++" || v == "--" || v == "!" || v == "-" || v == "+" {
		// an operator before an operand nests one level, as a parenthesis does (assign)
		if err := p.enter(); err != nil {
			return nil, err
		}
		defer p.leave()
	}
	if v := p.peek().v; v == "++" || v == "--" {
		p.next()
		target, err := p.unary()
		if err != nil {
			return nil, err
		}
		return updateNode(v, target, true)
	}
	if v := p.peek().v; v == "!" || v == "-" || v == "+" {
		// +x is Number(x), as in JavaScript (16/10/2026, the owner's E2)
		p.next()
		e, err := p.unary()
		if err != nil {
			return nil, err
		}
		return []any{"u" + v, e}, nil
	}
	return p.postfix()
}

// callArgs parses `(a, b, …)` — the opening paren already consumed by the caller.
func (p *parser) callArgs() ([]any, error) {
	args := []any{}
	if p.peek().v != ")" {
		a, err := p.assign()
		if err != nil {
			return nil, err
		}
		args = append(args, a)
		for p.peek().v == "," {
			p.next()
			a, err := p.assign()
			if err != nil {
				return nil, err
			}
			args = append(args, a)
		}
	}
	if err := p.eat(")"); err != nil {
		return nil, err
	}
	return args, nil
}

func (p *parser) postfix() (any, error) {
	e, err := p.primary()
	if err != nil {
		return nil, err
	}
	for {
		// a?.b, a?.[k], f?.() — the same reads as a.b, a[k], f(): every access is null-safe already.
		if p.peek().v == "?." {
			p.next()
			if v := p.peek().v; v == "(" || v == "[" {
				continue
			}
			if p.peek().t != "id" {
				return nil, errors.New("hydrate: ?. needs a name, [ or ( after it")
			}
			p.toks[p.pos-1] = tok{"op", ".", p.toks[p.pos-1].at}
			p.pos--
		}
		if p.peek().v == "." {
			p.next()
			// a name follows a dot, as in JavaScript: o., o.) and o.'a' are refused (16/10/2026: o. read
			// member "" here and threw in the kernel, so {{ user. }} compiled and printed nothing)
			if p.peek().t != "id" {
				return nil, errors.New("hydrate: . needs a name after it")
			}
			e = []any{".", e, p.next().v}
			continue
		}
		// a call: of a member it keeps what the member was read from as this, however it was read —
		// o.m(), o[k](), o.m?.() and (o.m)() call m on o, as JavaScript does (16/10/2026: o[k]() and
		// o.m?.() called it bare, on the scope); a computed key rides as its expression. Anything else
		// is a bare call: inc(), add(1, 2) — the callee is whatever expression came before.
		if p.peek().v == "(" {
			p.next()
			args, err := p.callArgs()
			if err != nil {
				return nil, err
			}
			if node, ok := e.([]any); ok && len(node) == 3 && (node[0] == "." || node[0] == "idx") {
				e = []any{"()", node[1], node[2], args}
			} else {
				e = []any{"call", e, args}
			}
			continue
		}
		// a[b]: the key is any expression — items[1], items[i], map[key] — the same read as a.name once
		// the key is known (the kernel's postfix() mirrors this).
		if p.peek().v == "[" {
			p.next()
			key, err := p.assign()
			if err != nil {
				return nil, err
			}
			if err := p.eat("]"); err != nil {
				return nil, err
			}
			e = []any{"idx", e, key}
			continue
		}
		break
	}
	// count++ / count-- binds to the whole left-hand side just read, once: count++++ is an error,
	// as it is in JavaScript.
	if v := p.peek().v; v == "++" || v == "--" {
		p.next()
		return updateNode(v, e, false)
	}
	return e, nil
}

// updateNode builds ++/-- over a target, which must be one `=` could assign to: a scope name, or
// the page-scope address $.name. obj.prop++ stays out for the same reason obj.prop = v does —
// general member assignment is not in the grammar.
func updateNode(op string, target any, prefix bool) (any, error) {
	if arr, ok := target.([]any); ok {
		if len(arr) == 2 && arr[0] == "$" {
			if arr[1] == "$" {
				return nil, errors.New("hydrate: cannot " + describeUpdate(op) + " $ itself")
			}
			return []any{op, arr[1], prefix}, nil
		}
		if len(arr) == 3 && arr[0] == "." {
			if inner, ok := arr[1].([]any); ok && len(inner) == 2 && inner[0] == "$" && inner[1] == "$" {
				return []any{op + "$", arr[2], prefix}, nil
			}
		}
	}
	return nil, errors.New("hydrate: " + op + " needs a name to " + describeUpdate(op) +
		" — count" + op + " or $.count" + op + ", not a member of an object")
}

func describeUpdate(op string) string {
	if op == "++" {
		return "increment"
	}
	return "decrement"
}

func (p *parser) primary() (any, error) {
	t := p.peek()
	switch t.t {
	case "num":
		p.next()
		// as Number reads the text: past the largest number Infinity, as the kernel's Number(t.v)
		return []any{"#", value.StringToNumber(t.v)}, nil
	case "str":
		p.next()
		return []any{"#", strings.TrimPrefix(t.v, strMark)}, nil
	case "id":
		p.next()
		switch t.v {
		case "true":
			return []any{"#", true}, nil
		case "false":
			return []any{"#", false}, nil
		case "null":
			return []any{"#", nil}, nil
		}
		return []any{"$", t.v}, nil
	case "op":
		if t.v == "(" {
			// Arrow lookahead first: `() => body` / `(x, y) => body`. Parens are required.
			if params, ok := p.tryArrowParams(); ok {
				body, err := p.assign()
				if err != nil {
					return nil, err
				}
				// the lambda as written, from its ( to the end of its body: its text, as JavaScript's
				source := strings.TrimSpace(p.src[t.at:p.peek().at])
				return []any{"=>", params, body, source}, nil
			}
			p.next()
			e, err := p.assign()
			if err != nil {
				return nil, err
			}
			if err := p.eat(")"); err != nil {
				return nil, err
			}
			return e, nil
		}
		if t.v == "{" {
			p.next()
			pairs := []any{}
			for p.peek().v != "}" {
				kt := p.next()
				if kt.t != "id" && kt.t != "str" {
					return nil, errors.New("hydrate: bad object key '" + kt.v + "'")
				}
				if err := p.eat(":"); err != nil {
					return nil, err
				}
				v, err := p.assign()
				if err != nil {
					return nil, err
				}
				pairs = append(pairs, []any{strings.TrimPrefix(kt.v, strMark), v})
				if p.peek().v == "," {
					p.next() // objects allow a trailing comma (server-language convention)
					continue
				}
				break
			}
			if err := p.eat("}"); err != nil {
				return nil, err
			}
			return []any{"{}", pairs}, nil
		}
		if t.v == "[" {
			p.next()
			items := []any{}
			if p.peek().v != "]" {
				for {
					v, err := p.assign()
					if err != nil {
						return nil, err
					}
					items = append(items, v)
					if p.peek().v == "," {
						p.next()
						if p.peek().v == "]" {
							return nil, errors.New("hydrate: arrays reject a trailing comma")
						}
						continue
					}
					break
				}
			}
			if err := p.eat("]"); err != nil {
				return nil, err
			}
			return []any{"[]", items}, nil
		}
	}
	return nil, errors.New("hydrate: unexpected token '" + t.v + "'")
}

// tryArrowParams speculatively parses `(ident, …) =>` from the current position. On success the
// tokens are consumed and the parameter names are returned; on any mismatch the position is
// restored and the caller falls through to a parenthesized expression.
func (p *parser) tryArrowParams() ([]any, bool) {
	save := p.pos
	p.next() // consume (
	params := []any{}
	if p.peek().v == ")" {
		p.next()
	} else {
		for {
			if p.peek().t != "id" {
				p.pos = save
				return nil, false
			}
			params = append(params, p.next().v)
			if p.peek().v == "," {
				p.next()
				continue
			}
			break
		}
		if p.peek().v != ")" {
			p.pos = save
			return nil, false
		}
		p.next()
	}
	if p.peek().v != "=>" {
		p.pos = save
		return nil, false
	}
	p.next()
	return params, true
}

// Compile parses a hydrate expression and returns its IR tree (marshalable to a compact JSON array).
// A carriage return reads as a line feed, as the browser's HTML parser hands the kernel an attribute
// (16/10/2026: a .ui file saved with CRLF gave a string, and a lambda's text, a \r the browser has not).
func Compile(expr string) (any, error) {
	if strings.Contains(expr, "\r") {
		expr = strings.ReplaceAll(strings.ReplaceAll(expr, "\r\n", "\n"), "\r", "\n")
	}
	toks, err := lex(expr)
	if err != nil {
		return nil, err
	}
	p := &parser{src: expr, toks: toks}
	node, err := p.sequence()
	if err != nil {
		return nil, err
	}
	if p.peek().t != "eof" {
		return nil, errors.New("hydrate: unexpected trailing token '" + p.peek().v + "'")
	}
	if tooDeep(node) {
		return nil, errNested
	}
	return node, nil
}

// CompileJSON compiles an expression to its IR and returns the compact JSON string that a page ships
// (e.g. in data-text-ir="…"). HTML escaping is disabled so operators like > stay literal.
func CompileJSON(expr string) (string, error) {
	node, err := Compile(expr)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(node); err != nil {
		return "", err
	}
	return strings.TrimRight(buf.String(), "\n"), nil
}
