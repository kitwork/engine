package sql

import (
	"fmt"
	"strings"
)

const MaximumTriggerColumns = 32

// The first trigger profile has one inline INSERT action, not a procedural
// language or a PostgreSQL EXECUTE FUNCTION compatibility shim.
type CreateTriggerStatement struct {
	Name    string
	Table   string
	Event   string
	When    *ExpressionPlan
	Target  string
	Columns []string
	Values  []ExpressionPlan
}

type DropTriggerStatement struct {
	Name     string
	Table    string
	IfExists bool
}

func (parser *statementParser) parseCreateTrigger() (*CreateTriggerStatement, error) {
	plan := &CreateTriggerStatement{}
	var err error
	plan.Name, err = parser.identifier()
	if err != nil {
		return nil, err
	}
	plan.Name = strings.ToLower(plan.Name)
	if err := parser.cursor.ExpectKeyword("after"); err != nil {
		return nil, fmt.Errorf("kitdb SQL: triggers currently require AFTER: %w", err)
	}
	plan.Event = strings.ToLower(parser.cursor.Take().Text)
	switch plan.Event {
	case "insert", "update", "delete":
	default:
		return nil, fmt.Errorf("kitdb SQL: trigger event must be INSERT, UPDATE or DELETE")
	}
	if err := parser.cursor.ExpectKeyword("on"); err != nil {
		return nil, err
	}
	plan.Table, err = parser.qualifiedIdentifier()
	if err != nil {
		return nil, err
	}
	for _, word := range []string{"for", "each", "row"} {
		if err := parser.cursor.ExpectKeyword(word); err != nil {
			return nil, err
		}
	}
	if parser.cursor.AcceptKeyword("when") {
		if err := parser.cursor.ExpectSymbol("("); err != nil {
			return nil, err
		}
		plan.When, err = parser.parseExpression()
		if err != nil {
			return nil, err
		}
		if err := parser.cursor.ExpectSymbol(")"); err != nil {
			return nil, err
		}
	}
	for _, word := range []string{"insert", "into"} {
		if err := parser.cursor.ExpectKeyword(word); err != nil {
			return nil, fmt.Errorf("kitdb SQL: trigger body requires one inline INSERT INTO: %w", err)
		}
	}
	plan.Target, err = parser.qualifiedIdentifier()
	if err != nil {
		return nil, err
	}
	plan.Columns, err = parser.identifierList()
	if err != nil {
		return nil, err
	}
	if len(plan.Columns) > MaximumTriggerColumns {
		return nil, fmt.Errorf("kitdb SQL: trigger exceeds %d target columns", MaximumTriggerColumns)
	}
	if err := parser.cursor.ExpectKeyword("values"); err != nil {
		return nil, err
	}
	if err := parser.cursor.ExpectSymbol("("); err != nil {
		return nil, err
	}
	for {
		value := ExpressionPlan{Kind: "literal", Literal: Literal{Kind: LiteralDefault}}
		if !parser.cursor.AcceptKeyword("default") {
			parsed, err := parser.parseExpression()
			if err != nil {
				return nil, err
			}
			value = *parsed
		}
		plan.Values = append(plan.Values, value)
		if len(plan.Values) > MaximumTriggerColumns {
			return nil, fmt.Errorf("kitdb SQL: trigger exceeds %d values", MaximumTriggerColumns)
		}
		if parser.cursor.AcceptSymbol(")") {
			break
		}
		if err := parser.cursor.ExpectSymbol(","); err != nil {
			return nil, err
		}
	}
	if len(plan.Columns) != len(plan.Values) {
		return nil, fmt.Errorf("kitdb SQL: trigger INSERT columns and values do not match")
	}
	return plan, nil
}

func (parser *statementParser) parseDropTrigger() (*DropTriggerStatement, error) {
	plan := &DropTriggerStatement{}
	if parser.cursor.AcceptKeyword("if") {
		if err := parser.cursor.ExpectKeyword("exists"); err != nil {
			return nil, err
		}
		plan.IfExists = true
	}
	var err error
	plan.Name, err = parser.identifier()
	if err != nil {
		return nil, err
	}
	plan.Name = strings.ToLower(plan.Name)
	if err := parser.cursor.ExpectKeyword("on"); err != nil {
		return nil, err
	}
	plan.Table, err = parser.qualifiedIdentifier()
	_ = parser.cursor.AcceptKeyword("restrict")
	return plan, err
}
