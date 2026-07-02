// Package hydrate is the server half of the "frontend bytecode VM": it compiles a small,
// deliberately-constrained expression subset (the same one authors write in data-* attributes)
// into a compact IR — a nested JSON array the tiny client interpreter walks. The client ships NO
// lexer/parser; the parser lives here, on the server, so pages ship bytecode + a ~30-line runner.
//
// IR shapes (each is a JSON array):
//
//	["#", literal]            literal (number | string | bool | null)
//	["$", "name"]            variable read from scope
//	[op, left, right]        binary: + - * / % > < >= <= == != && ||
//	["u!", e] / ["u-", e]    unary not / negate
//	["?", c, a, b]           ternary c ? a : b
//	["=", "name", value]     assignment to a scope variable
//	[".", obj, "name"]       member access obj.name
//	["()", obj, "name", []]  method call obj.name(args...)
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
				if two := s[i : i+2]; two == "==" || two == "!=" || two == ">=" || two == "<=" || two == "&&" || two == "||" {
					out = append(out, tok{"op", two})
					i += 2
					continue
				}
			}
			if strings.IndexByte("+-*/%<>!?:().,=", c) >= 0 {
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

// The grammar (precedence low→high): assign → ternary → binary → unary → postfix → primary.
// Each stage returns the IR directly, so parse and codegen are one pass.

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
		arr, ok := left.([]any)
		if !ok || len(arr) != 2 || arr[0] != "$" {
			return nil, errors.New("hydrate: invalid assignment target")
		}
		return []any{"=", arr[1], val}, nil
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

func (p *parser) postfix() (any, error) {
	e, err := p.primary()
	if err != nil {
		return nil, err
	}
	for p.peek().v == "." {
		p.next()
		name := p.next().v
		if p.peek().v == "(" {
			p.next()
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
			e = []any{"()", e, name, args}
		} else {
			e = []any{".", e, name}
		}
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
	}
	return nil, errors.New("hydrate: unexpected token '" + t.v + "'")
}

// Compile parses a hydrate expression and returns its IR tree (marshalable to a compact JSON array).
func Compile(expr string) (any, error) {
	p := &parser{toks: lex(expr)}
	node, err := p.assign()
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
