package ui

import (
	"strings"

	"github.com/kitwork/engine/jit/hydrate"
)

// Expressions in .ui use the same grammar and the same IR as the client kernel (jit/hydrate): one
// language, two ends. The server walks the IR to render the first paint; the browser walks the same
// IR to keep the page live.

// expr is one compiled expression.
type expr struct {
	source string
	ir     any
}

func compileExpr(source string, at *Node, attr *Attr) (*expr, error) {
	if attr != nil && strings.Contains(source, "{{") {
		// for, if, show and events are expressions already: {{ }} belongs to values, not to them. The
		// whole value wrapped once reads as what the braces hold (house style: the compiler's lint warns);
		// braces anywhere else do not say one expression, an error
		bare, ok := wrappedExpression(source)
		if !ok {
			// what is written without any of its braces (if="{{ a }} && b" is if="a && b"), offered only
			// when that reads as one expression: show="{{ a }} {{ b }}" is no "a b"
			bare = strings.Join(strings.Fields(strings.NewReplacer("{{", " ", "}}", " ").Replace(source)), " ")
			message := attr.Name + " đã là biểu thức, không bọc {{ }}"
			if _, err := hydrate.Compile(bare); bare != "" && err == nil {
				message += ": viết " + attr.Name + "=\"" + bare + "\""
			}
			return nil, &Error{File: at.File, Line: attr.Line, Column: attr.Column, Message: message}
		}
		source = bare
	}
	ir, err := hydrate.Compile(strings.TrimSpace(source))
	if err != nil {
		line, column := at.Line, at.Column
		if attr != nil {
			line, column = attr.Line, attr.Column
		}
		return nil, &Error{File: at.File, Line: line, Column: column, Message: "biểu thức không hợp lệ: " + strings.TrimSpace(source) + " (" + err.Error() + ")"}
	}
	if callsSafe(ir) {
		line, column := at.Line, at.Column
		if attr != nil {
			line, column = attr.Line, attr.Column
		}
		return nil, &Error{File: at.File, Line: line, Column: column, Message: safeInExpression}
	}
	return &expr{source: strings.TrimSpace(source), ir: ir}, nil
}

// safeInExpression is the compile error of an expression that calls safe (14/10/2026). An expression
// of .ui has nothing to catch: it never fails as it runs, on either end (a name that is not there reads
// empty, a call of what is not a function gives empty, past its budget it is empty), safe would have to
// ship in the browser's kernel for a function that never has work, and logic belongs in .work. The
// view reads the failure (const { value, error } = safe(() => …)) and the page shows that data.
const safeInExpression = "biểu thức .ui không có lỗi để bắt: gọi safe(() => …) trong router.view rồi đọc kết quả"

// callsSafe reports whether an expression's IR calls the bare name safe: ["call", ["$", "safe"], …],
// outside a lambda that names a parameter so.
func callsSafe(ir any) bool {
	node, ok := ir.([]any)
	if !ok || len(node) == 0 {
		return false
	}
	if _, op := node[0].(string); !op {
		// a list of nodes (arguments, elements, pairs): every one of them
		for _, child := range node {
			if callsSafe(child) {
				return true
			}
		}
		return false
	}
	switch node[0] {
	case "#":
		return false // a literal: its text is data
	case "call":
		if callee, ok := node[1].([]any); ok && len(callee) == 2 && callee[0] == "$" && callee[1] == "safe" {
			return true
		}
	case "=>":
		if len(node) > 1 && namesSafe(node[1]) {
			return false
		}
	}
	for _, child := range node[1:] {
		if callsSafe(child) {
			return true
		}
	}
	return false
}

// namesSafe reports whether a lambda's parameter list names safe.
func namesSafe(params any) bool {
	switch list := params.(type) {
	case []string:
		for _, name := range list {
			if name == "safe" {
				return true
			}
		}
	case []any:
		for _, name := range list {
			if name == "safe" {
				return true
			}
		}
	}
	return false
}

// wrappedExpression is what one {{ }} around a whole value holds ({{ ready }} → ready), and whether the
// value is that: one pair of braces, from the first character to the last but for spaces, holding
// something and no braces of its own.
func wrappedExpression(source string) (string, bool) {
	v := strings.TrimSpace(source)
	if !strings.HasPrefix(v, "{{") || !strings.HasSuffix(v, "}}") {
		return "", false
	}
	end := closingBraces(v[2:])
	if end < 0 || 2+end+2 != len(v) {
		return "", false // the braces close before the end: more than one {{ }}, or text after them
	}
	inner := strings.TrimSpace(v[2 : 2+end])
	if inner == "" || strings.Contains(inner, "{{") {
		return "", false
	}
	return inner, true
}

func (e *expr) eval(scope map[string]any) any {
	v, err := hydrate.Eval(e.ir, scope)
	if err != nil {
		return nil
	}
	return v
}

// text is what the page writes of the expression, as the browser's kernel writes it (hydrate.EvalText):
// nothing for null and undefined, else String(v) — a list its items joined, an object "[object Object]".
// An expression that fails writes nothing, which the browser then leaves as it is.
func (e *expr) text(scope map[string]any) string {
	s, err := hydrate.EvalText(e.ir, scope)
	if err != nil {
		return ""
	}
	return s
}

// part is a piece of interpolated text: either literal text or an expression.
type part struct {
	text string
	e    *expr
}

// interpolation is a string that may hold {{ }}: "Giá cũ {{ money(p) }}".
type interpolation struct {
	parts []part
	// joined is the one expression a value of more than one part is, the way the kernel is handed it
	// and the server prints it: [(a), ' text', (b)].join(''), each part's text joined (nil for a single
	// {{ }} and for a value with none)
	joined *expr
}

// single returns the expression when the whole value is exactly one {{ }} (whitespace around it does
// not count): such a value keeps its own kind (number, boolean, object) instead of becoming text.
func (i *interpolation) single() *expr {
	var found *expr
	for _, p := range i.parts {
		if p.e != nil {
			if found != nil {
				return nil
			}
			found = p.e
			continue
		}
		if strings.TrimSpace(p.text) != "" {
			return nil
		}
	}
	return found
}

func (i *interpolation) static() bool {
	for _, p := range i.parts {
		if p.e != nil {
			return false
		}
	}
	return true
}

func (i *interpolation) literal() string {
	var b strings.Builder
	for _, p := range i.parts {
		b.WriteString(p.text)
	}
	return b.String()
}

// render joins the parts as text; null and undefined print as "". A value of more than one part is its
// joined expression, which the kernel runs as it is: every part read, then each written as
// Array.prototype.join writes an item (String(v), null and undefined ""), in one evaluation, as the
// browser does (16/10/2026: the kernel was handed (a) + (b), which added 1 and 2 to 3 and true and
// true to 2 where the server had printed "12" and "truetrue").
func (i *interpolation) render(scope map[string]any) string {
	if i.joined != nil {
		return i.joined.text(scope)
	}
	var b strings.Builder
	for _, p := range i.parts {
		if p.e == nil {
			b.WriteString(p.text)
			continue
		}
		b.WriteString(p.e.text(scope))
	}
	return b.String()
}

// value returns the raw value of a single-expression interpolation, or its text otherwise.
func (i *interpolation) value(scope map[string]any) any {
	if e := i.single(); e != nil {
		return e.eval(scope)
	}
	return i.render(scope)
}

func parseInterpolation(source string, at *Node, attr *Attr) (*interpolation, error) {
	out := &interpolation{}
	rest := source
	for {
		start := strings.Index(rest, "{{")
		if start < 0 {
			if rest != "" {
				out.parts = append(out.parts, part{text: unescapeBraces(rest)})
			}
			return out, out.join()
		}
		if start > 0 && rest[start-1] == '\\' { // \{{ writes a literal {{
			out.parts = append(out.parts, part{text: rest[:start-1] + "{{"})
			rest = rest[start+2:]
			continue
		}
		if start > 0 {
			out.parts = append(out.parts, part{text: rest[:start]})
		}
		end := closingBraces(rest[start+2:])
		if end < 0 {
			line, column := at.Line, at.Column
			if attr != nil {
				line, column = attr.Line, attr.Column
			}
			return nil, &Error{File: at.File, Line: line, Column: column, Message: "biểu thức {{ chưa được đóng bằng }}"}
		}
		e, err := compileExpr(rest[start+2:start+2+end], at, attr)
		if err != nil {
			return nil, err
		}
		out.parts = append(out.parts, part{e: e})
		rest = rest[start+2+end+2:]
	}
}

func unescapeBraces(s string) string { return s }

// join makes the joined expression of a value of more than one part (interpolation.joined).
func (i *interpolation) join() error {
	if i.static() || i.single() != nil {
		return nil
	}
	items := make([]string, 0, len(i.parts))
	for _, p := range i.parts {
		if p.e == nil {
			items = append(items, quoteString(p.text))
		} else {
			items = append(items, "("+p.e.source+")")
		}
	}
	source := "[" + strings.Join(items, ", ") + "].join('')"
	ir, err := hydrate.Compile(source)
	if err != nil {
		return err // each part compiled on its own, so the whole does
	}
	i.joined = &expr{source: source, ir: ir}
	return nil
}

// stringify prints a value the way the browser's kernel writes it into the page, which is JavaScript's
// String(v), except that null and undefined print as "" (a missing name never shows "undefined" on a
// page): a number as JavaScript writes it ("1e+21", -0 as "0"; 15/10/2026), a list its items joined by
// commas, an object "[object Object]", a lambda its source (hydrate.Text). 16/10/2026: a list or an
// object printed "" here and "1,2" or "[object Object]" in the browser, the text flashing as it hydrated.
func stringify(v any) string {
	return hydrate.Text(v)
}

func truthy(v any) bool { return hydrate.Truthy(v) }
