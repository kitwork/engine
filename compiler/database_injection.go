package compiler

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// SQL injection where it is written (KITWORK_DATABASE.md mục 2, "SQL thô", 28/10/2026). JavaScript joins a
// template or a + before query(…) runs, so query receives finished text and nothing can tell the SQL from
// what a person typed; the check therefore runs on the source. The SQL of query(…) may be constant text, or
// text the compiler cannot see built (a parameter, an import, a call: the build report's job); a value glued
// into it, by ${…}, by +, or by concat or join, is a compile error naming the value.

// sqlGlue is the first value a text glues in, as the compiler sees it written: a ${…} of a template (any,
// even a constant one, as the spec writes it), or a part of +, concat or join that is not constant text;
// through ?:, ||, ?? and && both sides, and through a const or a let, what its values glued (holdSQL,
// assignSQL). nil when the text glues nothing, and for a value it cannot see whole (query(sql) with sql a
// parameter).
func (r *resolver) sqlGlue(e Expression) Expression {
	switch node := e.(type) {
	case *TemplateLiteral:
		for _, part := range node.Parts {
			if literal, isText := part.(*Literal); isText && literal.Token.Kind == String {
				continue
			}
			return part
		}
	case *InfixExpression:
		switch node.Operator {
		case "+":
			return r.sqlGlueParts(node.Left, node.Right)
		case "||", "??", "&&":
			if glued := r.sqlGlue(node.Left); glued != nil {
				return glued
			}
			return r.sqlGlue(node.Right)
		}
	case *TernaryExpression:
		if glued := r.sqlGlue(node.Consequence); glued != nil {
			return glued
		}
		return r.sqlGlue(node.Alternative)
	case *MethodCallExpression:
		if node.Method == nil {
			return nil
		}
		switch node.Method.Value {
		case "concat":
			return r.sqlGlueParts(append([]Expression{node.Object}, node.Arguments...)...)
		case "join":
			if parts, isArray := node.Object.(*ArrayLiteral); isArray {
				return r.sqlGlueParts(append(append([]Expression{}, parts.Elements...), node.Arguments...)...)
			}
		}
	case *Identifier:
		if found := r.resolvedBinding(node); found != nil {
			return found.sqlGlued
		}
	}
	return nil
}

// sqlGlueParts is the first part of a join that glues a value: a part that glues one itself, or one that
// is not constant text.
func (r *resolver) sqlGlueParts(parts ...Expression) Expression {
	for _, part := range parts {
		if glued := r.sqlGlue(part); glued != nil {
			return glued
		}
		if !r.sqlConstant(part) {
			return part
		}
	}
	return nil
}

// sqlConstant reports text no value reaches: a literal (a string, a number), a template with no ${…}, a + of
// constants, a const or a let whose every value is one.
func (r *resolver) sqlConstant(e Expression) bool {
	switch node := e.(type) {
	case *Literal:
		return true
	case *TemplateLiteral:
		return r.sqlGlue(node) == nil
	case *InfixExpression:
		return node.Operator == "+" && r.sqlConstant(node.Left) && r.sqlConstant(node.Right)
	case *Identifier:
		found := r.resolvedBinding(node)
		return found != nil && found.sqlText
	}
	return false
}

// holdSQL records, once a declaration's value is resolved, whether its name holds constant text and the
// first value it glues: const where = "id = " + id glues id, and query("… where " + where) is refused at id.
func (r *resolver) holdSQL(node *VarStatement) {
	if r.err != nil || !holdsOne(node) {
		return
	}
	declared := r.result.slots[node.Names[0]]
	if declared == nil {
		return
	}
	declared.sqlText = r.sqlConstant(node.Value)
	declared.sqlGlued = r.sqlGlue(node.Value)
}

// assignSQL follows a write to a name the same way: sql += " where id = " + id glues id into sql. A name
// stays constant text only while every value written to it is.
func (r *resolver) assignSQL(node *AssignmentExpression) {
	name, isName := node.Name.(*Identifier)
	if r.err != nil || !isName {
		return
	}
	found := r.resolvedBinding(name)
	if found == nil {
		return
	}
	constant, glued := r.sqlConstant(node.Value), r.sqlGlue(node.Value)
	found.sqlText = found.sqlText && constant
	if found.sqlGlued == nil {
		found.sqlGlued = glued
	}
}

// sqlInjection is the error of a query(…) whose SQL glues a value: where the call is, what is glued and
// where, and the call with the value as a parameter. Today's engine binds the values of an array
// (query(sql, [id])); the spec's query(sql, id) is a slice of its own.
func (r *resolver) sqlInjection(root *Identifier, query *MethodCallExpression, glued Expression) error {
	name := gluedName(glued)
	return fmt.Errorf("%s: %s.query(…) dán %s vào chữ SQL (%s) trước khi gọi, thành SQL injection: đưa nó thành tham số, viết %s.query(\"… $1\", [%s])",
		r.where(query.Method.Token), root.Value, name, r.where(expressionToken(glued)), root.Value, name)
}

// gluedName is how an error names a glued value: as written, without the parentheses String puts around a
// +, or "một giá trị" when it is too long to read in a line.
func gluedName(e Expression) string {
	text := e.String()
	if strings.HasPrefix(text, "(") && strings.HasSuffix(text, ")") {
		text = text[1 : len(text)-1]
	}
	if text == "" || utf8.RuneCountInString(text) > 40 {
		return "một giá trị"
	}
	return text
}
