package sql

import (
	"fmt"
	"strings"
)

type CreateDomainStatement struct {
	Name   string
	Column ColumnDefinition
}

type DropDomainStatement struct {
	Name     string
	IfExists bool
}

func (parser *statementParser) domainIdentifier() (string, error) {
	name, err := parser.identifier()
	if err != nil {
		return "", err
	}
	if parser.cursor.Peek().Text == "." {
		return "", fmt.Errorf("kitdb SQL: qualified domain names are not supported yet")
	}
	return strings.ToLower(name), nil
}

func (parser *statementParser) parseCreateDomain() (*CreateDomainStatement, error) {
	name, err := parser.domainIdentifier()
	if err != nil {
		return nil, err
	}
	_ = parser.cursor.AcceptKeyword("as")
	typeInfo, choices, modifiers, err := parser.columnType()
	if err != nil {
		return nil, err
	}
	column := ColumnDefinition{Name: "value", Type: typeInfo, Choices: choices,
		Precision: modifiers.Precision, Scale: modifiers.Scale, TimePrecision: modifiers.TimePrecision, TextLength: modifiers.TextLength}
	nullSeen := false
	for {
		switch {
		case parser.cursor.AcceptKeyword("default"):
			if column.HasDefault {
				return nil, fmt.Errorf("kitdb SQL: DOMAIN repeats DEFAULT")
			}
			column.HasDefault = true
			column.Default, err = parser.literal(false)
			if err != nil {
				return nil, err
			}
		case parser.cursor.AcceptKeyword("not"):
			if nullSeen {
				return nil, fmt.Errorf("kitdb SQL: DOMAIN repeats nullability")
			}
			if err := parser.cursor.ExpectKeyword("null"); err != nil {
				return nil, err
			}
			column.NotNull, nullSeen = true, true
		case parser.cursor.AcceptKeyword("null"):
			if nullSeen {
				return nil, fmt.Errorf("kitdb SQL: DOMAIN repeats nullability")
			}
			nullSeen = true
		case parser.cursor.AcceptKeyword("check"):
			check, err := parser.parseCheck("", "value")
			if err != nil {
				return nil, err
			}
			column.Checks = append(column.Checks, check)
		case parser.cursor.AcceptKeyword("constraint"):
			constraint, err := parser.identifier()
			if err != nil {
				return nil, err
			}
			if err := parser.cursor.ExpectKeyword("check"); err != nil {
				return nil, err
			}
			check, err := parser.parseCheck(constraint, "value")
			if err != nil {
				return nil, err
			}
			column.Checks = append(column.Checks, check)
		default:
			return &CreateDomainStatement{Name: name, Column: column}, nil
		}
		if len(column.Checks) > 16 {
			return nil, fmt.Errorf("kitdb SQL: DOMAIN exceeds 16 CHECK constraints")
		}
	}
}

func (parser *statementParser) parseDropDomain() (*DropDomainStatement, error) {
	plan := &DropDomainStatement{}
	if parser.cursor.AcceptKeyword("if") {
		if err := parser.cursor.ExpectKeyword("exists"); err != nil {
			return nil, err
		}
		plan.IfExists = true
	}
	var err error
	plan.Name, err = parser.domainIdentifier()
	_ = parser.cursor.AcceptKeyword("restrict")
	return plan, err
}
