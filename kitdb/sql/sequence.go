package sql

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

type CreateSequenceStatement struct {
	Name                               string
	DataType                           string
	IfNotExists                        bool
	Start, Increment, Minimum, Maximum int64
	Cache                              int64
	Cycle                              bool
}

type DropSequenceStatement struct {
	Name     string
	IfExists bool
}
type AlterSequenceStatement struct {
	Name     string
	IfExists bool
	Restart  *int64
}

// ParseSequenceName validates the text argument of nextval/currval/setval.
func ParseSequenceName(source string) (string, error) {
	tokens, err := Lex(source)
	if err != nil {
		return "", err
	}
	cursor, err := NewCursor(tokens, 0)
	if err != nil {
		return "", err
	}
	parser := statementParser{cursor: cursor}
	name, err := parser.sequenceName()
	if err != nil {
		return "", err
	}
	if cursor.Peek().Kind != TokenEOF {
		return "", fmt.Errorf("kitdb SQL: invalid sequence name")
	}
	return name, nil
}

func (parser *statementParser) sequenceName() (string, error) {
	name, err := parser.identifier()
	if err != nil {
		return "", err
	}
	if parser.cursor.AcceptSymbol(".") {
		if name != "public" {
			return "", fmt.Errorf("kitdb SQL: sequences currently use the public schema")
		}
		name, err = parser.identifier()
	}
	if name == "" || len(name) > 128 || strings.ContainsAny(name, ".\x00") {
		return "", fmt.Errorf("kitdb SQL: invalid sequence name")
	}
	return name, err
}

func (parser *statementParser) sequenceInteger() (int64, error) {
	parser.cursor.AcceptSymbol("+")
	literal, err := parser.literal(false)
	if err != nil {
		return 0, err
	}
	if literal.Kind != LiteralNumber {
		return 0, fmt.Errorf("kitdb SQL: sequence option requires an integer")
	}
	value, err := strconv.ParseInt(literal.Text, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("kitdb SQL: invalid sequence integer %q", literal.Text)
	}
	return value, nil
}

func (parser *statementParser) parseCreateSequence() (*CreateSequenceStatement, error) {
	plan := &CreateSequenceStatement{DataType: "bigint", Increment: 1, Cache: 1}
	if parser.cursor.AcceptKeyword("if") {
		if err := parser.cursor.ExpectKeyword("not"); err != nil {
			return nil, err
		}
		if err := parser.cursor.ExpectKeyword("exists"); err != nil {
			return nil, err
		}
		plan.IfNotExists = true
	}
	var err error
	plan.Name, err = parser.sequenceName()
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var start, minimum, maximum *int64
	for parser.cursor.Peek().Kind != TokenEOF && parser.cursor.Peek().Text != ";" {
		no := parser.cursor.AcceptKeyword("no")
		option := strings.ToLower(parser.cursor.Take().Text)
		if seen[option] {
			return nil, fmt.Errorf("kitdb SQL: duplicate sequence option %q", option)
		}
		seen[option] = true
		if no && option != "minvalue" && option != "maxvalue" && option != "cycle" {
			return nil, fmt.Errorf("kitdb SQL: unsupported NO sequence option")
		}
		switch option {
		case "as":
			token := parser.cursor.Take()
			name := strings.ToLower(token.Text)
			if token.Kind != TokenIdentifier || name != "smallint" && name != "integer" && name != "bigint" {
				return nil, fmt.Errorf("kitdb SQL: sequence AS requires SMALLINT, INTEGER or BIGINT")
			}
			kind, found := SequenceKindForDataType(name)
			if !found {
				return nil, fmt.Errorf("kitdb SQL: sequence AS requires SMALLINT, INTEGER or BIGINT")
			}
			plan.DataType, _ = SequenceDataTypeForKind(kind)
		case "start", "increment", "minvalue", "maxvalue", "cache":
			if no {
				continue
			}
			if option == "start" {
				parser.cursor.AcceptKeyword("with")
			}
			if option == "increment" {
				parser.cursor.AcceptKeyword("by")
			}
			value, err := parser.sequenceInteger()
			if err != nil {
				return nil, err
			}
			switch option {
			case "start":
				start = &value
			case "increment":
				plan.Increment = value
			case "minvalue":
				minimum = &value
			case "maxvalue":
				maximum = &value
			case "cache":
				if value < 1 || value > 4096 {
					return nil, fmt.Errorf("kitdb SQL: sequence CACHE must be between 1 and 4096")
				}
				plan.Cache = value
			}
		case "cycle":
			plan.Cycle = !no
		case "owned":
			if err := parser.cursor.ExpectKeyword("by"); err != nil {
				return nil, err
			}
			if !parser.cursor.AcceptKeyword("none") {
				return nil, fmt.Errorf("kitdb SQL: sequence column ownership is not supported yet")
			}
		default:
			return nil, fmt.Errorf("kitdb SQL: unsupported sequence option %q", option)
		}
	}
	typeMinimum, typeMaximum := int64(math.MinInt64), int64(math.MaxInt64)
	switch plan.DataType {
	case "smallint":
		typeMinimum, typeMaximum = math.MinInt16, math.MaxInt16
	case "integer":
		typeMinimum, typeMaximum = math.MinInt32, math.MaxInt32
	}
	plan.Minimum, plan.Maximum = 1, typeMaximum
	if plan.Increment < 0 {
		plan.Minimum, plan.Maximum = typeMinimum, -1
	}
	if minimum != nil {
		plan.Minimum = *minimum
	}
	if maximum != nil {
		plan.Maximum = *maximum
	}
	plan.Start = plan.Minimum
	if plan.Increment < 0 {
		plan.Start = plan.Maximum
	}
	if start != nil {
		plan.Start = *start
	}
	if plan.Increment == 0 || plan.Minimum < typeMinimum || plan.Maximum > typeMaximum ||
		plan.Minimum >= plan.Maximum || plan.Start < plan.Minimum || plan.Start > plan.Maximum {
		return nil, fmt.Errorf("kitdb SQL: invalid sequence increment, bounds or start")
	}
	return plan, nil
}

func (parser *statementParser) parseDropSequence() (*DropSequenceStatement, error) {
	plan := &DropSequenceStatement{}
	if parser.cursor.AcceptKeyword("if") {
		if err := parser.cursor.ExpectKeyword("exists"); err != nil {
			return nil, err
		}
		plan.IfExists = true
	}
	var err error
	plan.Name, err = parser.sequenceName()
	parser.cursor.AcceptKeyword("restrict")
	return plan, err
}

func (parser *statementParser) parseAlterSequence() (*AlterSequenceStatement, error) {
	plan := &AlterSequenceStatement{}
	if parser.cursor.AcceptKeyword("if") {
		if err := parser.cursor.ExpectKeyword("exists"); err != nil {
			return nil, err
		}
		plan.IfExists = true
	}
	var err error
	plan.Name, err = parser.sequenceName()
	if err != nil {
		return nil, err
	}
	if err := parser.cursor.ExpectKeyword("restart"); err != nil {
		return nil, err
	}
	with := parser.cursor.AcceptKeyword("with")
	if with || parser.cursor.Peek().Kind != TokenEOF && parser.cursor.Peek().Text != ";" {
		value, err := parser.sequenceInteger()
		if err != nil {
			return nil, err
		}
		plan.Restart = &value
	}
	return plan, nil
}
