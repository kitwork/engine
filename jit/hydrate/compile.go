// Package hydrate is the server half of the "frontend bytecode VM": it compiles a small,
// deliberately-constrained expression subset (the same one authors write in data-* attributes)
// into a compact IR — a nested JSON array the tiny client interpreter walks. The client ships NO
// lexer/parser; the parser lives here, on the server, so pages ship bytecode + a ~30-line runner.
//
// IR shapes (each is a JSON array):
//
//	["#", literal]            literal (number | string | bool | null)
//	["$", "name"]            variable read from scope ("$" itself = the PAGE scope object)
//	[op, left, right]        binary: + - * / % > < >= <= == != && ||
//	["u!", e] / ["u-", e]    unary not / negate
//	["?", c, a, b]           ternary c ? a : b
//	["=", "name", value]     assignment to a scope variable (lexical: owner scope, else nearest)
//	["=$", "name", value]    assignment to the PAGE scope: $.name = value — the same $ the
//	                         server template language uses for its root data
//	[".", obj, "name"]       member access obj.name
//	["()", obj, "name", []]  method call obj.name(args...)
//	["{}", [[k, v], …]]      object literal { count: 5, open: false } — a BLUEPRINT, parsed not
//	                         eval'd (objects allow a trailing comma, matching the server language)
//	["[]", [e, …]]           array literal [1, 2, 3] (arrays REJECT a trailing comma, ditto)
//	[";", e1, e2, …]         sequence: count = 5; open = true — evaluates left to right,
//	                         yields the last value
//	["=>", [params], body]   lambda: () => count = count + 1 — a NAMED IR TREE, i.e. code-as-data.
//	                         The body is this same grammar; parens are required around params.
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
)

type tok struct {
	t string // "num" | "str" | "id" | "op" | "eof"
	v string
}

func isIDStart(c byte) bool {
	return c == '_' || c == '$' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
}
func isIDPart(c byte) bool { return isIDStart(c) || (c >= '0' && c <= '9') }
func isDigit(c byte) bool  { return c >= '0' && c <= '9' }

// lex turns the source into tokens. It is byte-oriented for the ASCII structure; string-literal
// contents are sliced from the source so UTF-8 (e.g. Vietnamese, emoji) is preserved verbatim.
func lex(s string) []tok {
	var out []tok
	i, n := 0, len(s)
	for i < n {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case isDigit(c) || (c == '.' && i+1 < n && isDigit(s[i+1])):
			j := i
			for j < n && (isDigit(s[j]) || s[j] == '.') {
				j++
			}
			out = append(out, tok{"num", s[i:j]})
			i = j
		case c == '"' || c == '\'':
			q := c
			j := i + 1
			for j < n && s[j] != q {
				j++
			}
			out = append(out, tok{"str", s[i+1 : j]})
			i = j + 1
		case isIDStart(c):
			j := i
			for j < n && isIDPart(s[j]) {
				j++
			}
			out = append(out, tok{"id", s[i:j]})
			i = j
		default:
			if i+1 < n {
				if two := s[i : i+2]; two == "==" || two == "!=" || two == ">=" || two == "<=" || two == "&&" || two == "||" || two == "=>" {
					out = append(out, tok{"op", two})
					i += 2
					continue
				}
			}
			if strings.IndexByte("+-*/%<>!?:().,={}[];", c) >= 0 {
				out = append(out, tok{"op", string(c)})
			}
			i++
		}
	}
	return append(out, tok{"eof", ""})
}

var precedence = map[string]int{
	"||": 1, "&&": 2, "==": 3, "!=": 3, ">": 4, "<": 4, ">=": 4, "<=": 4, "+": 5, "-": 5, "*": 6, "/": 6, "%": 6,
}

type parser struct {
	toks []tok
	pos  int
}

func (p *parser) peek() tok {
	if p.pos >= len(p.toks) {
		return tok{"eof", ""}
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
	if v := p.peek().v; v == "!" || v == "-" {
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
		if p.peek().v == "." {
			p.next()
			name := p.next().v
			if p.peek().v == "(" {
				p.next()
				args, err := p.callArgs()
				if err != nil {
					return nil, err
				}
				e = []any{"()", e, name, args}
			} else {
				e = []any{".", e, name}
			}
			continue
		}
		// bare call: inc(), add(1, 2) — the callee is whatever expression came before.
		if p.peek().v == "(" {
			p.next()
			args, err := p.callArgs()
			if err != nil {
				return nil, err
			}
			e = []any{"call", e, args}
			continue
		}
		break
	}
	return e, nil
}

func (p *parser) primary() (any, error) {
	t := p.peek()
	switch t.t {
	case "num":
		p.next()
		f, err := strconv.ParseFloat(t.v, 64)
		if err != nil {
			return nil, errors.New("hydrate: bad number '" + t.v + "'")
		}
		return []any{"#", f}, nil
	case "str":
		p.next()
		return []any{"#", t.v}, nil
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
				return []any{"=>", params, body}, nil
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
				pairs = append(pairs, []any{kt.v, v})
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
func Compile(expr string) (any, error) {
	p := &parser{toks: lex(expr)}
	node, err := p.sequence()
	if err != nil {
		return nil, err
	}
	if p.peek().t != "eof" {
		return nil, errors.New("hydrate: unexpected trailing token '" + p.peek().v + "'")
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
