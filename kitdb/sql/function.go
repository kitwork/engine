package sql

import (
	"fmt"
	"strings"
)

type FunctionParameter struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
}

type FunctionDefinition struct {
	Parameters []FunctionParameter `json:"parameters"`
	ReturnKind string              `json:"returnKind"`
	Body       ExpressionPlan      `json:"body"`
}

type CreateFunctionStatement struct {
	Name    string
	Replace bool
	FunctionDefinition
}

type DropFunctionStatement struct {
	Name          string
	IfExists      bool
	ArgumentKinds []string
	HasSignature  bool
}

func (parser *statementParser) functionIdentifier() (string, error) {
	name, err := parser.identifier()
	if err != nil {
		return "", err
	}
	if parser.cursor.Peek().Text == "." {
		return "", fmt.Errorf("kitdb SQL: qualified function names are not supported yet")
	}
	return strings.ToLower(name), nil
}

func (parser *statementParser) functionType() (string, error) {
	token := parser.cursor.Take()
	name := strings.ToLower(token.Text)
	if name == "double" && parser.cursor.AcceptKeyword("precision") {
		name = "double precision"
	}
	info, found := ResolveName(name)
	if token.Kind != TokenIdentifier || !found || !SupportedFunctionKind(info.Kind) {
		return "", fmt.Errorf("kitdb SQL: function type %q is not supported; use TEXT, UUID, SMALLINT/INTEGER/BIGINT, NUMERIC, DOUBLE PRECISION or BOOLEAN", name)
	}
	return info.Kind, nil
}

func SupportedFunctionKind(kind string) bool {
	switch kind {
	case "text", "uuid", "integer", "smallint", "int32", "bigint", "float", "decimal", "bool":
		return true
	}
	return false
}

func (parser *statementParser) parseCreateFunction(replace bool) (*CreateFunctionStatement, error) {
	plan := &CreateFunctionStatement{Replace: replace}
	var err error
	plan.Name, err = parser.functionIdentifier()
	if err != nil {
		return nil, err
	}
	if err := parser.cursor.ExpectSymbol("("); err != nil {
		return nil, err
	}
	if !parser.cursor.AcceptSymbol(")") {
		for {
			name, err := parser.functionIdentifier()
			if err != nil {
				return nil, err
			}
			kind, err := parser.functionType()
			if err != nil {
				return nil, err
			}
			for _, parameter := range plan.Parameters {
				if parameter.Name == name {
					return nil, fmt.Errorf("kitdb SQL: duplicate function parameter %q", name)
				}
			}
			plan.Parameters = append(plan.Parameters, FunctionParameter{Name: name, Kind: kind})
			if len(plan.Parameters) > 16 {
				return nil, fmt.Errorf("kitdb SQL: function exceeds 16 parameters")
			}
			if parser.cursor.AcceptSymbol(")") {
				break
			}
			if err := parser.cursor.ExpectSymbol(","); err != nil {
				return nil, err
			}
		}
	}
	if err := parser.cursor.ExpectKeyword("returns"); err != nil {
		return nil, err
	}
	plan.ReturnKind, err = parser.functionType()
	if err != nil {
		return nil, err
	}
	if err := parser.cursor.ExpectKeyword("language"); err != nil {
		return nil, err
	}
	if err := parser.cursor.ExpectKeyword("sql"); err != nil {
		return nil, err
	}
	if err := parser.cursor.ExpectKeyword("return"); err != nil {
		return nil, err
	}
	body, err := parser.parseExpression()
	if err != nil {
		return nil, err
	}
	plan.Body = *body
	return plan, nil
}

func (parser *statementParser) parseDropFunction() (*DropFunctionStatement, error) {
	plan := &DropFunctionStatement{}
	if parser.cursor.AcceptKeyword("if") {
		if err := parser.cursor.ExpectKeyword("exists"); err != nil {
			return nil, err
		}
		plan.IfExists = true
	}
	var err error
	plan.Name, err = parser.functionIdentifier()
	if err != nil {
		return nil, err
	}
	if parser.cursor.AcceptSymbol("(") {
		plan.HasSignature = true
		if !parser.cursor.AcceptSymbol(")") {
			for {
				kind, err := parser.functionType()
				if err != nil {
					return nil, err
				}
				plan.ArgumentKinds = append(plan.ArgumentKinds, kind)
				if len(plan.ArgumentKinds) > 16 {
					return nil, fmt.Errorf("kitdb SQL: function exceeds 16 parameters")
				}
				if parser.cursor.AcceptSymbol(")") {
					break
				}
				if err := parser.cursor.ExpectSymbol(","); err != nil {
					return nil, err
				}
			}
		}
	}
	_ = parser.cursor.AcceptKeyword("restrict")
	return plan, nil
}

func ValidateExpression(expression ExpressionPlan) error { return validateExpressionPlan(expression) }
