package work

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	requestscope "github.com/kitwork/engine/request"
	"github.com/kitwork/engine/utilities/query"
	"github.com/kitwork/engine/value"
)

const (
	kitDBRemoteSQLBytes  = 1 << 20
	kitDBRemoteSQLTokens = 4096
)

type kitSQLTokenKind uint8

const (
	kitSQLTokenEOF kitSQLTokenKind = iota
	kitSQLTokenIdentifier
	kitSQLTokenString
	kitSQLTokenNumber
	kitSQLTokenPlaceholder
	kitSQLTokenSymbol
)

type kitSQLToken struct {
	kind kitSQLTokenKind
	text string
}

type kitSQLBindings struct {
	positional []value.Value
	named      map[string]value.Value
	next       int
}

type kitSQLProjection struct {
	field string
	alias string
	all   bool
	count bool
}

type kitSQLCondition struct {
	column   string
	operator string
	value    value.Value
}

type kitSQLOrder struct {
	column    string
	direction string
}

type kitSQLScalar struct {
	name  string
	kind  string
	value value.Value
}

type kitSQLStatement struct {
	kind        string
	table       string
	projections []kitSQLProjection
	scalars     []kitSQLScalar
	values      map[string]value.Value
	conditions  []kitSQLCondition
	orders      []kitSQLOrder
	limit       int
	offset      int
	hasLimit    bool
	returning   []kitSQLProjection
	pragma      string
}

type kitSQLParser struct {
	tokens   []kitSQLToken
	position int
	bindings kitSQLBindings
}

type kitDBRemoteColumn struct {
	name string
	kind string
}

type kitDBRemoteResult struct {
	columns  []kitDBRemoteColumn
	rows     [][]value.Value
	affected int64
}

func tokenizeKitSQL(source string) ([]kitSQLToken, error) {
	if len(source) == 0 {
		return nil, fmt.Errorf("kitdb SQL: statement is empty")
	}
	if len(source) > kitDBRemoteSQLBytes {
		return nil, fmt.Errorf("kitdb SQL: statement exceeds %d bytes", kitDBRemoteSQLBytes)
	}
	tokens := make([]kitSQLToken, 0, 32)
	appendToken := func(kind kitSQLTokenKind, text string) error {
		if len(tokens) >= kitDBRemoteSQLTokens {
			return fmt.Errorf("kitdb SQL: statement exceeds %d tokens", kitDBRemoteSQLTokens)
		}
		tokens = append(tokens, kitSQLToken{kind: kind, text: text})
		return nil
	}
	for offset := 0; offset < len(source); {
		r, size := utf8.DecodeRuneInString(source[offset:])
		if r == utf8.RuneError && size == 1 {
			return nil, fmt.Errorf("kitdb SQL: invalid UTF-8 at byte %d", offset)
		}
		if unicode.IsSpace(r) {
			offset += size
			continue
		}
		if strings.HasPrefix(source[offset:], "--") || strings.HasPrefix(source[offset:], "/*") {
			return nil, fmt.Errorf("kitdb SQL: comments are not supported")
		}
		if r == '\'' {
			start := offset
			offset++
			var text strings.Builder
			closed := false
			for offset < len(source) {
				if source[offset] == '\'' {
					if offset+1 < len(source) && source[offset+1] == '\'' {
						text.WriteByte('\'')
						offset += 2
						continue
					}
					offset++
					closed = true
					break
				}
				text.WriteByte(source[offset])
				offset++
			}
			if !closed {
				return nil, fmt.Errorf("kitdb SQL: unterminated string at byte %d", start)
			}
			if err := appendToken(kitSQLTokenString, text.String()); err != nil {
				return nil, err
			}
			continue
		}
		if r == '"' || r == '`' || r == '[' {
			start := offset
			closeByte := byte(r)
			if r == '[' {
				closeByte = ']'
			}
			offset++
			var text strings.Builder
			closed := false
			for offset < len(source) {
				if source[offset] == closeByte {
					if closeByte != ']' && offset+1 < len(source) && source[offset+1] == closeByte {
						text.WriteByte(closeByte)
						offset += 2
						continue
					}
					offset++
					closed = true
					break
				}
				text.WriteByte(source[offset])
				offset++
			}
			if !closed || text.Len() == 0 {
				return nil, fmt.Errorf("kitdb SQL: invalid quoted identifier at byte %d", start)
			}
			if err := appendToken(kitSQLTokenIdentifier, text.String()); err != nil {
				return nil, err
			}
			continue
		}
		if r == '?' || r == ':' || r == '@' || r == '$' {
			start := offset
			offset += size
			for offset < len(source) {
				next, nextSize := utf8.DecodeRuneInString(source[offset:])
				if !unicode.IsLetter(next) && !unicode.IsDigit(next) && next != '_' {
					break
				}
				offset += nextSize
			}
			if r != '?' && offset == start+size {
				return nil, fmt.Errorf("kitdb SQL: empty named parameter at byte %d", start)
			}
			if err := appendToken(kitSQLTokenPlaceholder, source[start:offset]); err != nil {
				return nil, err
			}
			continue
		}
		if unicode.IsLetter(r) || r == '_' {
			start := offset
			offset += size
			for offset < len(source) {
				next, nextSize := utf8.DecodeRuneInString(source[offset:])
				if !unicode.IsLetter(next) && !unicode.IsDigit(next) && next != '_' {
					break
				}
				offset += nextSize
			}
			if err := appendToken(kitSQLTokenIdentifier, source[start:offset]); err != nil {
				return nil, err
			}
			continue
		}
		if unicode.IsDigit(r) {
			start := offset
			dot := false
			offset += size
			for offset < len(source) {
				next := source[offset]
				if next == '.' && !dot {
					dot = true
					offset++
					continue
				}
				if next < '0' || next > '9' {
					break
				}
				offset++
			}
			if err := appendToken(kitSQLTokenNumber, source[start:offset]); err != nil {
				return nil, err
			}
			continue
		}
		if strings.ContainsRune("(),.*;+/-", r) {
			if err := appendToken(kitSQLTokenSymbol, string(r)); err != nil {
				return nil, err
			}
			offset += size
			continue
		}
		if strings.ContainsRune("=!<>", r) {
			operator := string(r)
			offset += size
			if offset < len(source) {
				next := source[offset]
				if next == '=' || (r == '<' && next == '>') {
					operator += string(next)
					offset++
				}
			}
			if err := appendToken(kitSQLTokenSymbol, operator); err != nil {
				return nil, err
			}
			continue
		}
		return nil, fmt.Errorf("kitdb SQL: unsupported character %q at byte %d", r, offset)
	}
	tokens = append(tokens, kitSQLToken{kind: kitSQLTokenEOF})
	return tokens, nil
}

func parseKitSQL(source string, bindings kitSQLBindings) (kitSQLStatement, error) {
	tokens, err := tokenizeKitSQL(strings.TrimSpace(source))
	if err != nil {
		return kitSQLStatement{}, err
	}
	parser := &kitSQLParser{tokens: tokens, bindings: bindings}
	var statement kitSQLStatement
	switch {
	case parser.acceptKeyword("select"):
		statement, err = parser.parseSelect()
	case parser.acceptKeyword("insert"):
		statement, err = parser.parseInsert()
	case parser.acceptKeyword("update"):
		statement, err = parser.parseUpdate()
	case parser.acceptKeyword("delete"):
		statement, err = parser.parseDelete()
	case parser.acceptKeyword("pragma"):
		statement, err = parser.parsePragma()
	default:
		err = fmt.Errorf("kitdb SQL: only SELECT, INSERT, UPDATE, DELETE and supported PRAGMA statements are accepted")
	}
	if err != nil {
		return kitSQLStatement{}, err
	}
	if parser.acceptSymbol(";") && parser.peek().kind != kitSQLTokenEOF {
		return kitSQLStatement{}, fmt.Errorf("kitdb SQL: multiple statements are not supported")
	}
	if parser.peek().kind != kitSQLTokenEOF {
		return kitSQLStatement{}, fmt.Errorf("kitdb SQL: unexpected token %q", parser.peek().text)
	}
	return statement, nil
}

func (parser *kitSQLParser) peek() kitSQLToken {
	if parser.position >= len(parser.tokens) {
		return kitSQLToken{kind: kitSQLTokenEOF}
	}
	return parser.tokens[parser.position]
}

func (parser *kitSQLParser) take() kitSQLToken {
	token := parser.peek()
	if parser.position < len(parser.tokens) {
		parser.position++
	}
	return token
}

func (parser *kitSQLParser) acceptKeyword(keyword string) bool {
	token := parser.peek()
	if token.kind == kitSQLTokenIdentifier && strings.EqualFold(token.text, keyword) {
		parser.position++
		return true
	}
	return false
}

func (parser *kitSQLParser) expectKeyword(keyword string) error {
	if !parser.acceptKeyword(keyword) {
		return fmt.Errorf("kitdb SQL: expected %s, got %q", strings.ToUpper(keyword), parser.peek().text)
	}
	return nil
}

func (parser *kitSQLParser) acceptSymbol(symbol string) bool {
	token := parser.peek()
	if token.kind == kitSQLTokenSymbol && token.text == symbol {
		parser.position++
		return true
	}
	return false
}

func (parser *kitSQLParser) expectSymbol(symbol string) error {
	if !parser.acceptSymbol(symbol) {
		return fmt.Errorf("kitdb SQL: expected %q, got %q", symbol, parser.peek().text)
	}
	return nil
}

func (parser *kitSQLParser) identifier() (string, error) {
	token := parser.take()
	if token.kind != kitSQLTokenIdentifier || token.text == "" {
		return "", fmt.Errorf("kitdb SQL: expected identifier, got %q", token.text)
	}
	name := token.text
	for parser.acceptSymbol(".") {
		next := parser.take()
		if next.kind != kitSQLTokenIdentifier || next.text == "" {
			return "", fmt.Errorf("kitdb SQL: expected identifier after dot")
		}
		name = next.text
	}
	return name, nil
}

func (parser *kitSQLParser) operand() (value.Value, error) {
	negative := parser.acceptSymbol("-")
	token := parser.take()
	switch token.kind {
	case kitSQLTokenPlaceholder:
		if negative {
			return value.Value{}, fmt.Errorf("kitdb SQL: a bound parameter cannot have a unary minus")
		}
		return parser.resolvePlaceholder(token.text)
	case kitSQLTokenString:
		if negative {
			return value.Value{}, fmt.Errorf("kitdb SQL: a string cannot have a unary minus")
		}
		return value.New(token.text), nil
	case kitSQLTokenNumber:
		number, err := strconv.ParseFloat(token.text, 64)
		if err != nil || math.IsNaN(number) || math.IsInf(number, 0) {
			return value.Value{}, fmt.Errorf("kitdb SQL: invalid number %q", token.text)
		}
		if negative {
			number = -number
		}
		return value.New(number), nil
	case kitSQLTokenIdentifier:
		if negative {
			return value.Value{}, fmt.Errorf("kitdb SQL: invalid negative operand %q", token.text)
		}
		switch strings.ToLower(token.text) {
		case "null":
			return value.NewNil(), nil
		case "true":
			return value.New(true), nil
		case "false":
			return value.New(false), nil
		}
	}
	return value.Value{}, fmt.Errorf("kitdb SQL: expected a literal or bound parameter, got %q", token.text)
}

func (parser *kitSQLParser) resolvePlaceholder(placeholder string) (value.Value, error) {
	if placeholder == "?" {
		if parser.bindings.next >= len(parser.bindings.positional) {
			return value.Value{}, fmt.Errorf("kitdb SQL: missing positional parameter %d", parser.bindings.next+1)
		}
		item := parser.bindings.positional[parser.bindings.next]
		parser.bindings.next++
		return item, nil
	}
	if strings.HasPrefix(placeholder, "?") {
		index, err := strconv.Atoi(strings.TrimPrefix(placeholder, "?"))
		if err != nil || index < 1 || index > len(parser.bindings.positional) {
			return value.Value{}, fmt.Errorf("kitdb SQL: positional parameter %q is unavailable", placeholder)
		}
		return parser.bindings.positional[index-1], nil
	}
	name := strings.TrimLeft(placeholder, ":@$")
	item, found := parser.bindings.named[name]
	if !found {
		item, found = parser.bindings.named[placeholder]
	}
	if !found {
		return value.Value{}, fmt.Errorf("kitdb SQL: named parameter %q is unavailable", placeholder)
	}
	return item, nil
}

func (parser *kitSQLParser) parseSelect() (kitSQLStatement, error) {
	if parser.scalarSelectStart() {
		return parser.parseScalarSelect()
	}
	projections, err := parser.projectionList()
	if err != nil {
		return kitSQLStatement{}, err
	}
	if err := parser.expectKeyword("from"); err != nil {
		return kitSQLStatement{}, err
	}
	table, err := parser.identifier()
	if err != nil {
		return kitSQLStatement{}, err
	}
	statement := kitSQLStatement{kind: "select", table: table, projections: projections}
	if err := parser.parseTail(&statement, true); err != nil {
		return kitSQLStatement{}, err
	}
	return statement, nil
}

func (parser *kitSQLParser) scalarSelectStart() bool {
	token := parser.peek()
	switch token.kind {
	case kitSQLTokenString, kitSQLTokenNumber, kitSQLTokenPlaceholder:
		return true
	case kitSQLTokenSymbol:
		return token.text == "-"
	case kitSQLTokenIdentifier:
		switch strings.ToLower(token.text) {
		case "null", "true", "false":
			return true
		}
	}
	return false
}

func (parser *kitSQLParser) parseScalarSelect() (kitSQLStatement, error) {
	scalars := make([]kitSQLScalar, 0, 2)
	for {
		start := parser.position
		item, err := parser.operand()
		if err != nil {
			return kitSQLStatement{}, err
		}
		name := kitSQLScalarName(parser.tokens[start:parser.position], len(scalars)+1)
		if parser.acceptKeyword("as") {
			name, err = parser.identifier()
			if err != nil {
				return kitSQLStatement{}, err
			}
		} else if parser.peek().kind == kitSQLTokenIdentifier && !kitSQLClauseKeyword(parser.peek().text) {
			name = parser.take().text
		}
		scalars = append(scalars, kitSQLScalar{name: name, kind: kitSQLScalarKind(item), value: item})
		if !parser.acceptSymbol(",") {
			break
		}
		if !parser.scalarSelectStart() {
			return kitSQLStatement{}, fmt.Errorf("kitdb SQL: scalar SELECT expects a literal or bound parameter")
		}
	}
	return kitSQLStatement{kind: "select_scalar", scalars: scalars}, nil
}

func kitSQLScalarName(tokens []kitSQLToken, position int) string {
	var name strings.Builder
	for _, token := range tokens {
		name.WriteString(token.text)
	}
	if name.Len() == 0 {
		return fmt.Sprintf("column%d", position)
	}
	return name.String()
}

func kitSQLScalarKind(item value.Value) string {
	if item.IsNil() {
		return "text"
	}
	switch item.K {
	case value.Bool:
		return "bool"
	case value.Number:
		if item.N == math.Trunc(item.N) {
			return "integer"
		}
		return "float"
	case value.Bytes:
		return "blob"
	default:
		return "text"
	}
}

func (parser *kitSQLParser) parseInsert() (kitSQLStatement, error) {
	if err := parser.expectKeyword("into"); err != nil {
		return kitSQLStatement{}, err
	}
	table, err := parser.identifier()
	if err != nil {
		return kitSQLStatement{}, err
	}
	statement := kitSQLStatement{kind: "insert", table: table, values: map[string]value.Value{}}
	if parser.acceptKeyword("default") {
		if err := parser.expectKeyword("values"); err != nil {
			return kitSQLStatement{}, err
		}
	} else {
		if err := parser.expectSymbol("("); err != nil {
			return kitSQLStatement{}, err
		}
		columns, err := parser.identifierList(")")
		if err != nil {
			return kitSQLStatement{}, err
		}
		if err := parser.expectKeyword("values"); err != nil {
			return kitSQLStatement{}, err
		}
		if err := parser.expectSymbol("("); err != nil {
			return kitSQLStatement{}, err
		}
		items := make([]value.Value, 0, len(columns))
		for {
			item, err := parser.operand()
			if err != nil {
				return kitSQLStatement{}, err
			}
			items = append(items, item)
			if parser.acceptSymbol(")") {
				break
			}
			if err := parser.expectSymbol(","); err != nil {
				return kitSQLStatement{}, err
			}
		}
		if len(items) != len(columns) {
			return kitSQLStatement{}, fmt.Errorf("kitdb SQL: INSERT has %d columns but %d values", len(columns), len(items))
		}
		for index, column := range columns {
			if _, duplicate := statement.values[column]; duplicate {
				return kitSQLStatement{}, fmt.Errorf("kitdb SQL: duplicate INSERT column %q", column)
			}
			statement.values[column] = items[index]
		}
	}
	if parser.acceptKeyword("returning") {
		statement.returning, err = parser.projectionList()
		if err != nil {
			return kitSQLStatement{}, err
		}
	}
	return statement, nil
}

func (parser *kitSQLParser) parseUpdate() (kitSQLStatement, error) {
	table, err := parser.identifier()
	if err != nil {
		return kitSQLStatement{}, err
	}
	if err := parser.expectKeyword("set"); err != nil {
		return kitSQLStatement{}, err
	}
	statement := kitSQLStatement{kind: "update", table: table, values: map[string]value.Value{}}
	for {
		column, err := parser.identifier()
		if err != nil {
			return kitSQLStatement{}, err
		}
		if _, duplicate := statement.values[column]; duplicate {
			return kitSQLStatement{}, fmt.Errorf("kitdb SQL: duplicate UPDATE column %q", column)
		}
		if err := parser.expectSymbol("="); err != nil {
			return kitSQLStatement{}, err
		}
		item, err := parser.operand()
		if err != nil {
			return kitSQLStatement{}, err
		}
		statement.values[column] = item
		if !parser.acceptSymbol(",") {
			break
		}
	}
	if err := parser.parseTail(&statement, false); err != nil {
		return kitSQLStatement{}, err
	}
	if parser.acceptKeyword("returning") {
		statement.returning, err = parser.projectionList()
		if err != nil {
			return kitSQLStatement{}, err
		}
	}
	return statement, nil
}

func (parser *kitSQLParser) parseDelete() (kitSQLStatement, error) {
	if err := parser.expectKeyword("from"); err != nil {
		return kitSQLStatement{}, err
	}
	table, err := parser.identifier()
	if err != nil {
		return kitSQLStatement{}, err
	}
	statement := kitSQLStatement{kind: "delete", table: table}
	if err := parser.parseTail(&statement, false); err != nil {
		return kitSQLStatement{}, err
	}
	if parser.acceptKeyword("returning") {
		statement.returning, err = parser.projectionList()
		if err != nil {
			return kitSQLStatement{}, err
		}
	}
	return statement, nil
}

func (parser *kitSQLParser) parsePragma() (kitSQLStatement, error) {
	name, err := parser.identifier()
	if err != nil {
		return kitSQLStatement{}, err
	}
	name = strings.ToLower(name)
	if name != "table_info" && name != "index_list" && name != "foreign_key_list" {
		return kitSQLStatement{}, fmt.Errorf("kitdb SQL: PRAGMA %s is not supported", name)
	}
	if err := parser.expectSymbol("("); err != nil {
		return kitSQLStatement{}, err
	}
	table, err := parser.identifierOrString()
	if err != nil {
		return kitSQLStatement{}, err
	}
	if err := parser.expectSymbol(")"); err != nil {
		return kitSQLStatement{}, err
	}
	return kitSQLStatement{kind: "pragma", pragma: name, table: table}, nil
}

func (parser *kitSQLParser) identifierOrString() (string, error) {
	if parser.peek().kind == kitSQLTokenString {
		return parser.take().text, nil
	}
	return parser.identifier()
}

func (parser *kitSQLParser) projectionList() ([]kitSQLProjection, error) {
	projections := make([]kitSQLProjection, 0, 4)
	for {
		projection := kitSQLProjection{}
		if parser.acceptSymbol("*") {
			projection.all = true
			projection.field = "*"
		} else if parser.acceptKeyword("count") {
			if err := parser.expectSymbol("("); err != nil {
				return nil, err
			}
			if err := parser.expectSymbol("*"); err != nil {
				return nil, fmt.Errorf("kitdb SQL: only COUNT(*) is supported")
			}
			if err := parser.expectSymbol(")"); err != nil {
				return nil, err
			}
			projection.count = true
			projection.field = "count(*)"
		} else {
			field, err := parser.identifier()
			if err != nil {
				return nil, err
			}
			projection.field = field
		}
		if parser.acceptKeyword("as") {
			alias, err := parser.identifier()
			if err != nil {
				return nil, err
			}
			projection.alias = alias
		} else if parser.peek().kind == kitSQLTokenIdentifier && !kitSQLClauseKeyword(parser.peek().text) {
			projection.alias = parser.take().text
		}
		projections = append(projections, projection)
		if !parser.acceptSymbol(",") {
			break
		}
	}
	if len(projections) == 0 {
		return nil, fmt.Errorf("kitdb SQL: projection list is empty")
	}
	return projections, nil
}

func kitSQLClauseKeyword(text string) bool {
	switch strings.ToLower(text) {
	case "from", "where", "order", "limit", "offset", "returning", "and", "or", "asc", "desc":
		return true
	default:
		return false
	}
}

func (parser *kitSQLParser) identifierList(close string) ([]string, error) {
	columns := make([]string, 0, 4)
	for {
		column, err := parser.identifier()
		if err != nil {
			return nil, err
		}
		columns = append(columns, column)
		if parser.acceptSymbol(close) {
			break
		}
		if err := parser.expectSymbol(","); err != nil {
			return nil, err
		}
	}
	return columns, nil
}

func (parser *kitSQLParser) parseTail(statement *kitSQLStatement, allowOrderLimit bool) error {
	if parser.acceptKeyword("where") {
		conditions, err := parser.conditionList()
		if err != nil {
			return err
		}
		statement.conditions = conditions
	}
	if allowOrderLimit && parser.acceptKeyword("order") {
		if err := parser.expectKeyword("by"); err != nil {
			return err
		}
		for {
			column, err := parser.identifier()
			if err != nil {
				return err
			}
			direction := "asc"
			if parser.acceptKeyword("asc") {
				direction = "asc"
			} else if parser.acceptKeyword("desc") {
				direction = "desc"
			}
			statement.orders = append(statement.orders, kitSQLOrder{column: column, direction: direction})
			if !parser.acceptSymbol(",") {
				break
			}
		}
	}
	if allowOrderLimit && parser.acceptKeyword("limit") {
		limit, err := parser.nonNegativeInteger("LIMIT")
		if err != nil {
			return err
		}
		statement.limit, statement.hasLimit = limit, true
		if parser.acceptKeyword("offset") {
			offset, err := parser.nonNegativeInteger("OFFSET")
			if err != nil {
				return err
			}
			statement.offset = offset
		}
	}
	return nil
}

func (parser *kitSQLParser) conditionList() ([]kitSQLCondition, error) {
	conditions := make([]kitSQLCondition, 0, 2)
	for {
		column, err := parser.identifier()
		if err != nil {
			return nil, err
		}
		condition := kitSQLCondition{column: column}
		negated := parser.acceptKeyword("not")
		if parser.acceptKeyword("is") {
			if negated {
				return nil, fmt.Errorf("kitdb SQL: NOT IS is invalid; use IS NOT NULL")
			}
			condition.operator = "="
			if parser.acceptKeyword("not") {
				condition.operator = "!="
			}
			if err := parser.expectKeyword("null"); err != nil {
				return nil, err
			}
			condition.value = value.NewNil()
		} else if parser.acceptKeyword("in") {
			condition.operator = "in"
			if negated {
				condition.operator = "not in"
			}
			if err := parser.expectSymbol("("); err != nil {
				return nil, err
			}
			items := make([]value.Value, 0, 4)
			for {
				item, err := parser.operand()
				if err != nil {
					return nil, err
				}
				items = append(items, item)
				if parser.acceptSymbol(")") {
					break
				}
				if err := parser.expectSymbol(","); err != nil {
					return nil, err
				}
			}
			condition.value = value.New(items)
		} else if parser.acceptKeyword("like") {
			condition.operator = "like"
			if negated {
				condition.operator = "not like"
			}
			item, err := parser.operand()
			if err != nil {
				return nil, err
			}
			condition.value = item
		} else {
			if negated {
				return nil, fmt.Errorf("kitdb SQL: NOT is supported only with IN or LIKE")
			}
			operator := parser.take()
			if operator.kind != kitSQLTokenSymbol || !kitSQLComparisonOperator(operator.text) {
				return nil, fmt.Errorf("kitdb SQL: unsupported comparison operator %q", operator.text)
			}
			condition.operator = operator.text
			item, err := parser.operand()
			if err != nil {
				return nil, err
			}
			condition.value = item
		}
		conditions = append(conditions, condition)
		if parser.acceptKeyword("and") {
			continue
		}
		if parser.acceptKeyword("or") {
			return nil, fmt.Errorf("kitdb SQL: OR is not supported in the first remote profile")
		}
		break
	}
	return conditions, nil
}

func kitSQLComparisonOperator(operator string) bool {
	switch operator {
	case "=", "==", "!=", "<>", ">", ">=", "<", "<=":
		return true
	default:
		return false
	}
}

func (parser *kitSQLParser) nonNegativeInteger(label string) (int, error) {
	item, err := parser.operand()
	if err != nil {
		return 0, err
	}
	if item.K != value.Number || item.N < 0 || item.N != math.Trunc(item.N) || item.N > math.MaxInt32 {
		return 0, fmt.Errorf("kitdb SQL: %s must be a non-negative integer", label)
	}
	return int(item.N), nil
}

func kitSQLBindingsFromHrana(stmt hranaStmt) (kitSQLBindings, error) {
	bindings := kitSQLBindings{named: make(map[string]value.Value, len(stmt.NamedArgs))}
	for _, encoded := range stmt.Args {
		decoded, err := hranaDecodeValue(encoded)
		if err != nil {
			return kitSQLBindings{}, err
		}
		bindings.positional = append(bindings.positional, value.New(decoded))
	}
	for _, argument := range stmt.NamedArgs {
		decoded, err := hranaDecodeValue(argument.Value)
		if err != nil {
			return kitSQLBindings{}, err
		}
		name := strings.TrimLeft(argument.Name, ":@$")
		if name == "" {
			return kitSQLBindings{}, fmt.Errorf("kitdb SQL: named parameter cannot be empty")
		}
		item := value.New(decoded)
		bindings.named[name] = item
		bindings.named[argument.Name] = item
	}
	return bindings, nil
}

func kitSQLBindingsFromAny(arguments []any) kitSQLBindings {
	bindings := kitSQLBindings{named: map[string]value.Value{}}
	for _, argument := range arguments {
		bindings.positional = append(bindings.positional, value.New(argument))
	}
	return bindings
}

func executeKitDBRemoteSQL(
	ctx context.Context,
	scope *requestscope.Scope,
	database *dbProxy,
	source string,
	bindings kitSQLBindings,
	readonly bool,
) (kitDBRemoteResult, error) {
	if database == nil || database.engine != "kitdb" {
		return kitDBRemoteResult{}, fmt.Errorf("kitdb remote: database is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return kitDBRemoteResult{}, err
	}
	statement, err := parseKitSQL(source, bindings)
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	if readonly && (statement.kind == "insert" || statement.kind == "update" || statement.kind == "delete") {
		return kitDBRemoteResult{}, fmt.Errorf("database is read-only (serve access: readonly); writes are refused")
	}
	switch statement.kind {
	case "select_scalar":
		return executeKitDBRemoteScalar(statement), nil
	case "select":
		return executeKitDBRemoteSelect(ctx, scope, database, statement)
	case "insert":
		return executeKitDBRemoteInsert(ctx, scope, database, statement)
	case "update":
		return executeKitDBRemoteUpdate(ctx, scope, database, statement)
	case "delete":
		return executeKitDBRemoteDelete(ctx, scope, database, statement)
	case "pragma":
		return executeKitDBRemotePragma(database, statement)
	default:
		return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: unsupported statement")
	}
}

func executeKitDBRemoteScalar(statement kitSQLStatement) kitDBRemoteResult {
	result := kitDBRemoteResult{
		columns: make([]kitDBRemoteColumn, len(statement.scalars)),
		rows:    make([][]value.Value, 1),
	}
	result.rows[0] = make([]value.Value, len(statement.scalars))
	for index, scalar := range statement.scalars {
		result.columns[index] = kitDBRemoteColumn{name: scalar.name, kind: scalar.kind}
		result.rows[0][index] = scalar.value
	}
	return result
}

func kitDBRemoteTable(database *dbProxy, scope *requestscope.Scope, requested string) (*SchemaTable, error) {
	name, definition, err := kitDBRemoteDefinition(database, requested)
	if err != nil {
		return nil, err
	}
	return &SchemaTable{
		tenant: database.tenant, scope: scope, engine: "kitdb", dbName: database.dbName,
		table: name, columns: definition.columns, siblings: database.tables,
		definition: definition, definitions: database.structs,
	}, nil
}

func kitDBRemoteDefinition(database *dbProxy, requested string) (string, *StructDef, error) {
	if definition := database.structs[requested]; definition != nil {
		return requested, definition, nil
	}
	match := ""
	for name := range database.structs {
		if strings.EqualFold(name, requested) {
			if match != "" {
				return "", nil, fmt.Errorf("kitdb SQL: ambiguous struct %q", requested)
			}
			match = name
		}
	}
	if match == "" {
		return "", nil, fmt.Errorf("kitdb SQL: no struct %q is declared", requested)
	}
	return match, database.structs[match], nil
}

func kitDBRemoteField(definition *StructDef, requested string) (string, StructFieldDef, error) {
	if definition == nil {
		return "", StructFieldDef{}, fmt.Errorf("kitdb SQL: struct is unavailable")
	}
	match := ""
	var field StructFieldDef
	for _, candidate := range definition.Fields {
		if candidate.Name == requested {
			return candidate.Name, candidate, nil
		}
		if strings.EqualFold(candidate.Name, requested) {
			if match != "" {
				return "", StructFieldDef{}, fmt.Errorf("kitdb SQL: ambiguous field %q", requested)
			}
			match, field = candidate.Name, candidate
		}
	}
	if match == "" {
		return "", StructFieldDef{}, fmt.Errorf("kitdb SQL: struct %q has no field %q", definition.Name, requested)
	}
	return match, field, nil
}

func applyKitDBRemoteNarrowing(table *SchemaTable, statement kitSQLStatement) error {
	for _, condition := range statement.conditions {
		column, _, err := kitDBRemoteField(table.definition, condition.column)
		if err != nil {
			return err
		}
		table.Where(value.New(column), value.New(condition.operator), condition.value)
	}
	for _, order := range statement.orders {
		column, _, err := kitDBRemoteField(table.definition, order.column)
		if err != nil {
			return err
		}
		table.OrderBy(column, order.direction)
	}
	if statement.hasLimit {
		table.Limit(statement.limit)
	}
	if statement.offset > 0 {
		table.builder().Skip(statement.offset)
	}
	if table.failed {
		return fmt.Errorf("%s", table.failMsg)
	}
	return nil
}

func executeKitDBRemoteSelect(
	ctx context.Context,
	scope *requestscope.Scope,
	database *dbProxy,
	statement kitSQLStatement,
) (kitDBRemoteResult, error) {
	if strings.EqualFold(statement.table, "sqlite_master") || strings.EqualFold(statement.table, "sqlite_schema") {
		return executeKitDBRemoteCatalog(database, statement)
	}
	table, err := kitDBRemoteTable(database, scope, statement.table)
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	if err := applyKitDBRemoteNarrowing(table, statement); err != nil {
		return kitDBRemoteResult{}, err
	}
	if len(statement.projections) == 1 && statement.projections[0].count {
		count := table.Count()
		if err := kitDBRemoteValueError(count); err != nil {
			return kitDBRemoteResult{}, err
		}
		name := statement.projections[0].alias
		if name == "" {
			name = "count(*)"
		}
		return kitDBRemoteResult{
			columns: []kitDBRemoteColumn{{name: name, kind: "integer"}},
			rows:    [][]value.Value{{count}},
		}, nil
	}
	for _, projection := range statement.projections {
		if projection.count {
			return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: COUNT(*) cannot be mixed with ordinary fields")
		}
	}
	if statement.hasLimit && statement.limit == 0 {
		columns, err := kitDBRemoteProjectionColumns(table.definition, statement.projections)
		return kitDBRemoteResult{columns: columns, rows: [][]value.Value{}}, err
	}
	rowsValue := table.List()
	if err := kitDBRemoteValueError(rowsValue); err != nil {
		return kitDBRemoteResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return kitDBRemoteResult{}, err
	}
	return projectKitDBRemoteRows(table.definition, statement.projections, rowsValue.Array())
}

func executeKitDBRemoteInsert(
	ctx context.Context,
	scope *requestscope.Scope,
	database *dbProxy,
	statement kitSQLStatement,
) (kitDBRemoteResult, error) {
	table, err := kitDBRemoteTable(database, scope, statement.table)
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	row := make(map[string]value.Value, len(statement.values))
	for requested, item := range statement.values {
		field, _, err := kitDBRemoteField(table.definition, requested)
		if err != nil {
			return kitDBRemoteResult{}, err
		}
		row[field] = item
	}
	created := table.Create(value.New(row))
	if err := kitDBRemoteValueError(created); err != nil {
		return kitDBRemoteResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return kitDBRemoteResult{}, err
	}
	result := kitDBRemoteResult{affected: 1}
	if len(statement.returning) != 0 {
		projected, err := projectKitDBRemoteRows(table.definition, statement.returning, []value.Value{created})
		if err != nil {
			return kitDBRemoteResult{}, err
		}
		result.columns, result.rows = projected.columns, projected.rows
	}
	return result, nil
}

func executeKitDBRemoteUpdate(
	ctx context.Context,
	scope *requestscope.Scope,
	database *dbProxy,
	statement kitSQLStatement,
) (kitDBRemoteResult, error) {
	if len(statement.conditions) == 0 {
		return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: UPDATE requires WHERE")
	}
	table, err := kitDBRemoteTable(database, scope, statement.table)
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	if err := applyKitDBRemoteNarrowing(table, statement); err != nil {
		return kitDBRemoteResult{}, err
	}
	countValue := table.Count()
	if err := kitDBRemoteValueError(countValue); err != nil {
		return kitDBRemoteResult{}, err
	}
	count := int(countValue.N)
	if len(statement.returning) != 0 && count > query.DefaultDBMaxLimit {
		return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: UPDATE RETURNING is bounded to %d rows", query.DefaultDBMaxLimit)
	}
	primary, _ := table.definition.primaryField()
	var keys []value.Value
	if len(statement.returning) != 0 && count != 0 {
		table.Limit(count)
		before := table.List()
		if err := kitDBRemoteValueError(before); err != nil {
			return kitDBRemoteResult{}, err
		}
		for _, row := range before.Array() {
			if row.K == value.Map {
				keys = append(keys, row.Map()[primary.Name])
			}
		}
	}
	changes := make(map[string]value.Value, len(statement.values))
	for requested, item := range statement.values {
		field, _, err := kitDBRemoteField(table.definition, requested)
		if err != nil {
			return kitDBRemoteResult{}, err
		}
		changes[field] = item
	}
	updated := table.Update(value.New(changes))
	if err := kitDBRemoteValueError(updated); err != nil {
		return kitDBRemoteResult{}, err
	}
	result := kitDBRemoteResult{affected: int64(count)}
	if len(statement.returning) != 0 && len(keys) != 0 {
		fresh, err := kitDBRemoteTable(database, scope, statement.table)
		if err != nil {
			return kitDBRemoteResult{}, err
		}
		fresh.Where(value.New(primary.Name), value.New("in"), value.New(keys)).Limit(len(keys))
		rows := fresh.List()
		if err := kitDBRemoteValueError(rows); err != nil {
			return kitDBRemoteResult{}, err
		}
		projected, err := projectKitDBRemoteRows(fresh.definition, statement.returning, rows.Array())
		if err != nil {
			return kitDBRemoteResult{}, err
		}
		result.columns, result.rows = projected.columns, projected.rows
	}
	if err := ctx.Err(); err != nil {
		return kitDBRemoteResult{}, err
	}
	return result, nil
}

func executeKitDBRemoteDelete(
	ctx context.Context,
	scope *requestscope.Scope,
	database *dbProxy,
	statement kitSQLStatement,
) (kitDBRemoteResult, error) {
	if len(statement.conditions) == 0 {
		return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: DELETE requires WHERE")
	}
	table, err := kitDBRemoteTable(database, scope, statement.table)
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	if err := applyKitDBRemoteNarrowing(table, statement); err != nil {
		return kitDBRemoteResult{}, err
	}
	countValue := table.Count()
	if err := kitDBRemoteValueError(countValue); err != nil {
		return kitDBRemoteResult{}, err
	}
	count := int(countValue.N)
	var projected kitDBRemoteResult
	if len(statement.returning) != 0 {
		if count > query.DefaultDBMaxLimit {
			return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: DELETE RETURNING is bounded to %d rows", query.DefaultDBMaxLimit)
		}
		table.Limit(count)
		rows := table.List()
		if err := kitDBRemoteValueError(rows); err != nil {
			return kitDBRemoteResult{}, err
		}
		projected, err = projectKitDBRemoteRows(table.definition, statement.returning, rows.Array())
		if err != nil {
			return kitDBRemoteResult{}, err
		}
	}
	deleted := table.Delete()
	if err := kitDBRemoteValueError(deleted); err != nil {
		return kitDBRemoteResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return kitDBRemoteResult{}, err
	}
	projected.affected = int64(count)
	return projected, nil
}

func kitDBRemoteValueError(item value.Value) error {
	if item.K != value.Invalid {
		return nil
	}
	message := item.Text()
	if message == "" {
		message = "KitDB operation failed"
	}
	return fmt.Errorf("%s", message)
}

func kitDBRemoteProjectionColumns(definition *StructDef, projections []kitSQLProjection) ([]kitDBRemoteColumn, error) {
	if len(projections) == 1 && projections[0].all {
		columns := make([]kitDBRemoteColumn, 0, len(definition.Fields))
		for _, field := range definition.Fields {
			columns = append(columns, kitDBRemoteColumn{name: field.Name, kind: field.Kind})
		}
		return columns, nil
	}
	columns := make([]kitDBRemoteColumn, 0, len(projections))
	for _, projection := range projections {
		if projection.all {
			return nil, fmt.Errorf("kitdb SQL: * cannot be mixed with named fields")
		}
		fieldName, field, err := kitDBRemoteField(definition, projection.field)
		if err != nil {
			return nil, err
		}
		name := projection.alias
		if name == "" {
			name = fieldName
		}
		columns = append(columns, kitDBRemoteColumn{name: name, kind: field.Kind})
	}
	return columns, nil
}

func projectKitDBRemoteRows(
	definition *StructDef,
	projections []kitSQLProjection,
	rows []value.Value,
) (kitDBRemoteResult, error) {
	columns, err := kitDBRemoteProjectionColumns(definition, projections)
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	fieldNames := make([]string, len(columns))
	if len(projections) == 1 && projections[0].all {
		for index, field := range definition.Fields {
			fieldNames[index] = field.Name
		}
	} else {
		for index, projection := range projections {
			field, _, err := kitDBRemoteField(definition, projection.field)
			if err != nil {
				return kitDBRemoteResult{}, err
			}
			fieldNames[index] = field
		}
	}
	result := kitDBRemoteResult{columns: columns, rows: make([][]value.Value, 0, len(rows))}
	for _, encoded := range rows {
		if encoded.K != value.Map {
			return kitDBRemoteResult{}, fmt.Errorf("kitdb remote: row is not an object")
		}
		row := encoded.Map()
		projected := make([]value.Value, len(fieldNames))
		for index, field := range fieldNames {
			item, found := row[field]
			if !found {
				item = value.NewNil()
			}
			projected[index] = item
		}
		result.rows = append(result.rows, projected)
	}
	return result, nil
}

func executeKitDBRemoteCatalog(database *dbProxy, statement kitSQLStatement) (kitDBRemoteResult, error) {
	virtual := make([]map[string]value.Value, 0, len(database.structs))
	names := make([]string, 0, len(database.structs))
	for name := range database.structs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		definition := database.structs[name]
		virtual = append(virtual, map[string]value.Value{
			"type": value.New("table"), "name": value.New(name), "tbl_name": value.New(name),
			"rootpage": value.New(0), "sql": value.New(schemaDDL(name, definition.columns)),
		})
		for _, index := range collectIndexes(name, definition.columns) {
			virtual = append(virtual, map[string]value.Value{
				"type": value.New("index"), "name": value.New(index.name), "tbl_name": value.New(name),
				"rootpage": value.New(0), "sql": value.New(indexSQL(name, index)),
			})
		}
	}
	filtered, err := filterKitDBVirtualRows(virtual, statement.conditions)
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	if len(statement.projections) == 1 && statement.projections[0].count {
		name := statement.projections[0].alias
		if name == "" {
			name = "count(*)"
		}
		return kitDBRemoteResult{
			columns: []kitDBRemoteColumn{{name: name, kind: "integer"}},
			rows:    [][]value.Value{{value.New(len(filtered))}},
		}, nil
	}
	orderKitDBVirtualRows(filtered, statement.orders)
	filtered = boundKitDBVirtualRows(filtered, statement)
	fields := []kitDBRemoteColumn{
		{name: "type", kind: "text"}, {name: "name", kind: "text"},
		{name: "tbl_name", kind: "text"}, {name: "rootpage", kind: "integer"},
		{name: "sql", kind: "text"},
	}
	return projectKitDBVirtualRows(fields, statement.projections, filtered)
}

func executeKitDBRemotePragma(database *dbProxy, statement kitSQLStatement) (kitDBRemoteResult, error) {
	_, definition, err := kitDBRemoteDefinition(database, statement.table)
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	switch statement.pragma {
	case "table_info":
		columns := []kitDBRemoteColumn{
			{name: "cid", kind: "integer"}, {name: "name", kind: "text"},
			{name: "type", kind: "text"}, {name: "notnull", kind: "integer"},
			{name: "dflt_value", kind: "text"}, {name: "pk", kind: "integer"},
		}
		rows := make([][]value.Value, 0, len(definition.Fields))
		for _, field := range definition.Fields {
			defaultValue := value.NewNil()
			if field.HasDefault {
				defaultValue = value.New(field.Default.Text())
			} else if field.DefaultNow {
				defaultValue = value.New("CURRENT_TIMESTAMP")
			}
			rows = append(rows, []value.Value{
				value.New(field.Position), value.New(field.Name), value.New(storageClass(field.Kind)),
				value.New(boolInt(field.NotNull)), defaultValue, value.New(boolInt(field.Primary)),
			})
		}
		return kitDBRemoteResult{columns: columns, rows: rows}, nil
	case "index_list":
		columns := []kitDBRemoteColumn{
			{name: "seq", kind: "integer"}, {name: "name", kind: "text"},
			{name: "unique", kind: "integer"}, {name: "origin", kind: "text"},
			{name: "partial", kind: "integer"},
		}
		rows := make([][]value.Value, 0)
		sequence := 0
		for _, field := range definition.Fields {
			if field.Unique && !field.Primary {
				rows = append(rows, []value.Value{
					value.New(sequence), value.New("unique_" + definition.Name + "_" + field.Name),
					value.New(1), value.New("u"), value.New(0),
				})
				sequence++
			}
		}
		for _, index := range collectIndexes(definition.Name, definition.columns) {
			rows = append(rows, []value.Value{
				value.New(sequence), value.New(index.name), value.New(0), value.New("c"),
				value.New(boolInt(len(index.filter) != 0)),
			})
			sequence++
		}
		return kitDBRemoteResult{columns: columns, rows: rows}, nil
	case "foreign_key_list":
		columns := []kitDBRemoteColumn{
			{name: "id", kind: "integer"}, {name: "seq", kind: "integer"},
			{name: "table", kind: "text"}, {name: "from", kind: "text"},
			{name: "to", kind: "text"}, {name: "on_update", kind: "text"},
			{name: "on_delete", kind: "text"}, {name: "match", kind: "text"},
		}
		rows := make([][]value.Value, 0)
		for _, field := range definition.Fields {
			if field.Reference == nil {
				continue
			}
			rows = append(rows, []value.Value{
				value.New(len(rows)), value.New(0), value.New(field.Reference.Struct), value.New(field.Name),
				value.New(field.Reference.Field), value.New(remoteFKAction(field.Reference.OnUpdate)),
				value.New(remoteFKAction(field.Reference.OnDelete)), value.New("NONE"),
			})
		}
		return kitDBRemoteResult{columns: columns, rows: rows}, nil
	default:
		return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: unsupported PRAGMA %q", statement.pragma)
	}
}

func boolInt(flag bool) int {
	if flag {
		return 1
	}
	return 0
}

func remoteFKAction(action string) string {
	if strings.TrimSpace(action) == "" {
		return "NO ACTION"
	}
	return fkAction(action)
}

func filterKitDBVirtualRows(rows []map[string]value.Value, conditions []kitSQLCondition) ([]map[string]value.Value, error) {
	filtered := make([]map[string]value.Value, 0, len(rows))
	for _, row := range rows {
		matched := true
		for _, condition := range conditions {
			left, found := virtualKitDBValue(row, condition.column)
			if !found {
				return nil, fmt.Errorf("kitdb SQL: metadata has no field %q", condition.column)
			}
			comparison := kitDBCompareValues(left, condition.value)
			switch strings.ToLower(condition.operator) {
			case "=", "==":
				matched = comparison == 0
			case "!=", "<>":
				matched = comparison != 0
			case "like":
				matched = kitDBLike(left.Text(), condition.value.Text())
			case "not like":
				matched = !kitDBLike(left.Text(), condition.value.Text())
			case "in":
				matched = kitDBContains(condition.value, left)
			case "not in":
				matched = !kitDBContains(condition.value, left)
			default:
				return nil, fmt.Errorf("kitdb SQL: metadata operator %q is not supported", condition.operator)
			}
			if !matched {
				break
			}
		}
		if matched {
			filtered = append(filtered, row)
		}
	}
	return filtered, nil
}

func virtualKitDBValue(row map[string]value.Value, requested string) (value.Value, bool) {
	if item, found := row[requested]; found {
		return item, true
	}
	for name, item := range row {
		if strings.EqualFold(name, requested) {
			return item, true
		}
	}
	return value.NewNil(), false
}

func orderKitDBVirtualRows(rows []map[string]value.Value, orders []kitSQLOrder) {
	sort.SliceStable(rows, func(left, right int) bool {
		for _, order := range orders {
			leftValue, _ := virtualKitDBValue(rows[left], order.column)
			rightValue, _ := virtualKitDBValue(rows[right], order.column)
			comparison := kitDBCompareValues(leftValue, rightValue)
			if comparison != 0 {
				if strings.EqualFold(order.direction, "desc") {
					return comparison > 0
				}
				return comparison < 0
			}
		}
		return false
	})
}

func boundKitDBVirtualRows(rows []map[string]value.Value, statement kitSQLStatement) []map[string]value.Value {
	offset := statement.offset
	if offset > len(rows) {
		offset = len(rows)
	}
	rows = rows[offset:]
	limit := query.DefaultDBMaxLimit
	if statement.hasLimit && statement.limit < limit {
		limit = statement.limit
	}
	if len(rows) > limit {
		rows = rows[:limit]
	}
	return rows
}

func projectKitDBVirtualRows(
	available []kitDBRemoteColumn,
	projections []kitSQLProjection,
	rows []map[string]value.Value,
) (kitDBRemoteResult, error) {
	lookup := func(requested string) (kitDBRemoteColumn, bool) {
		for _, column := range available {
			if strings.EqualFold(column.name, requested) {
				return column, true
			}
		}
		return kitDBRemoteColumn{}, false
	}
	selected := make([]kitDBRemoteColumn, 0)
	fields := make([]string, 0)
	if len(projections) == 1 && projections[0].all {
		selected = append(selected, available...)
		for _, column := range available {
			fields = append(fields, column.name)
		}
	} else {
		for _, projection := range projections {
			if projection.all || projection.count {
				return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: unsupported metadata projection")
			}
			column, found := lookup(projection.field)
			if !found {
				return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: metadata has no field %q", projection.field)
			}
			fields = append(fields, column.name)
			if projection.alias != "" {
				column.name = projection.alias
			}
			selected = append(selected, column)
		}
	}
	result := kitDBRemoteResult{columns: selected, rows: make([][]value.Value, 0, len(rows))}
	for _, row := range rows {
		encoded := make([]value.Value, len(fields))
		for index, field := range fields {
			encoded[index], _ = virtualKitDBValue(row, field)
		}
		result.rows = append(result.rows, encoded)
	}
	return result, nil
}

func kitDBRemoteResultMaps(result kitDBRemoteResult) []map[string]any {
	rows := make([]map[string]any, 0, len(result.rows))
	for _, encoded := range result.rows {
		row := make(map[string]any, len(result.columns))
		for index, column := range result.columns {
			if index < len(encoded) {
				row[column.name] = encoded[index].Interface()
			}
		}
		rows = append(rows, row)
	}
	return rows
}

func kitDBRemoteHranaResult(result kitDBRemoteResult, wantRows bool, durationMS float64) map[string]any {
	columns := make([]any, len(result.columns))
	for index, column := range result.columns {
		columns[index] = map[string]any{"name": column.name, "decltype": storageClass(column.kind)}
	}
	rows := []any{}
	if wantRows {
		rows = make([]any, 0, len(result.rows))
		for _, source := range result.rows {
			row := make([]any, len(source))
			for index, item := range source {
				kind := ""
				if index < len(result.columns) {
					kind = result.columns[index].kind
				}
				row[index] = hranaEncodeKitDBValue(item, kind)
			}
			rows = append(rows, row)
		}
	}
	return map[string]any{
		"cols": columns, "rows": rows, "affected_row_count": result.affected,
		"last_insert_rowid": nil, "rows_read": len(result.rows),
		"rows_written": result.affected, "query_duration_ms": durationMS,
	}
}

func hranaEncodeKitDBValue(item value.Value, kind string) map[string]any {
	if item.IsNil() {
		return map[string]any{"type": "null"}
	}
	switch item.K {
	case value.Bool:
		if item.N != 0 {
			return map[string]any{"type": "integer", "value": "1"}
		}
		return map[string]any{"type": "integer", "value": "0"}
	case value.Number:
		if storageClass(kind) == "INTEGER" && item.N == math.Trunc(item.N) && item.N >= math.MinInt64 && item.N <= math.MaxInt64 {
			return map[string]any{"type": "integer", "value": strconv.FormatInt(int64(item.N), 10)}
		}
		return map[string]any{"type": "float", "value": item.N}
	case value.String:
		return map[string]any{"type": "text", "value": item.String()}
	case value.Bytes:
		return hranaEncodeValue(item.Interface())
	default:
		encoded, _ := json.Marshal(item)
		return map[string]any{"type": "text", "value": string(encoded)}
	}
}

func runKitDBHranaRequest(
	ctx context.Context,
	scope *requestscope.Scope,
	database *dbProxy,
	request hranaStreamRequest,
	store map[int32]string,
	readonly bool,
) map[string]any {
	switch request.Type {
	case "close":
		return hranaOK(map[string]any{"type": "close"})
	case "get_autocommit":
		return hranaOK(map[string]any{"type": "get_autocommit", "is_autocommit": true})
	case "store_sql":
		if request.SQLID == nil || request.SQL == nil {
			return hranaErr("store_sql: missing sql_id or sql")
		}
		if _, exists := store[*request.SQLID]; exists {
			return hranaErr("store_sql: sql_id is already in use")
		}
		store[*request.SQLID] = *request.SQL
		return hranaOK(map[string]any{"type": "store_sql"})
	case "close_sql":
		if request.SQLID != nil {
			delete(store, *request.SQLID)
		}
		return hranaOK(map[string]any{"type": "close_sql"})
	case "execute":
		if request.Stmt == nil {
			return hranaErr("execute: missing stmt")
		}
		result, err := runKitDBHranaStmt(ctx, scope, database, *request.Stmt, store, readonly)
		if err != nil {
			return hranaErr(err.Error())
		}
		return hranaOK(map[string]any{"type": "execute", "result": result})
	case "batch":
		if request.Batch == nil {
			return hranaErr("batch: missing batch")
		}
		stepResults := make([]any, len(request.Batch.Steps))
		stepErrors := make([]any, len(request.Batch.Steps))
		for index, step := range request.Batch.Steps {
			result, err := runKitDBHranaStmt(ctx, scope, database, step.Stmt, store, readonly)
			if err != nil {
				stepErrors[index] = map[string]any{"message": err.Error()}
				break
			}
			stepResults[index] = result
		}
		return hranaOK(map[string]any{
			"type":   "batch",
			"result": map[string]any{"step_results": stepResults, "step_errors": stepErrors},
		})
	default:
		return hranaErr("unsupported request type in KitDB profile: " + request.Type)
	}
}

func runKitDBHranaStmt(
	ctx context.Context,
	scope *requestscope.Scope,
	database *dbProxy,
	statement hranaStmt,
	store map[int32]string,
	readonly bool,
) (map[string]any, error) {
	source, err := stmtSQL(statement, store)
	if err != nil {
		return nil, err
	}
	first := strings.ToLower(strings.TrimSpace(source))
	for _, control := range []string{"begin", "commit", "rollback", "savepoint", "release"} {
		if first == control || strings.HasPrefix(first, control+" ") {
			return nil, fmt.Errorf("kitdb SQL: interactive transactions are not supported by the first Hrana profile")
		}
	}
	bindings, err := kitSQLBindingsFromHrana(statement)
	if err != nil {
		return nil, err
	}
	started := time.Now()
	result, err := executeKitDBRemoteSQL(ctx, scope, database, source, bindings, readonly)
	if err != nil {
		return nil, err
	}
	return kitDBRemoteHranaResult(
		result,
		statement.WantRows,
		float64(time.Since(started).Microseconds())/1000,
	), nil
}
