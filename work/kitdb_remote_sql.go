package work

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	kitdbsql "github.com/kitwork/engine/kitdb/sql"
	requestscope "github.com/kitwork/engine/request"
	"github.com/kitwork/engine/utilities/query"
	"github.com/kitwork/engine/value"
)

const (
	kitDBRemoteSQLBytes                  = kitdbsql.MaximumStatementBytes
	kitDBRemoteSQLTokens                 = kitdbsql.MaximumStatementTokens
	kitDBRemoteSelectRowLimit            = 1_000
	kitDBRemoteInsertRowLimit            = 256
	kitDBRemoteTransactionStatementLimit = 256
	kitDBRemoteGroupLimit                = 10_000
	kitDBRemoteGroupFieldLimit           = 8
	kitDBRemoteGroupAggregateLimit       = 16
	kitDBRemoteGroupProjectionLimit      = 32
	kitDBRemoteJoinLimit                 = 1
	kitDBRemoteJoinInputLimit            = 10_000
	kitDBRemoteJoinPairLimit             = 20_000
	kitDBRemoteJoinProjectionLimit       = 64
	kitDBRemoteJoinOrderLimit            = 8
)

const kitSQLAggregateReferencePrefix = "\x00kitdb-aggregate\x00"

type kitSQLTokenKind = kitdbsql.TokenKind

const (
	kitSQLTokenEOF         = kitdbsql.TokenEOF
	kitSQLTokenIdentifier  = kitdbsql.TokenIdentifier
	kitSQLTokenString      = kitdbsql.TokenString
	kitSQLTokenNumber      = kitdbsql.TokenNumber
	kitSQLTokenPlaceholder = kitdbsql.TokenPlaceholder
	kitSQLTokenSymbol      = kitdbsql.TokenSymbol
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
	field      string
	alias      string
	label      string
	all        bool
	aggregate  string
	expression *kitSQLExpression
}

type kitSQLCondition struct {
	column   string
	operator string
	value    value.Value
}

type kitSQLOrder struct {
	column     string
	direction  string
	expression *kitSQLExpression
}

type kitSQLJoin struct {
	kind  string
	table string
	alias string
	left  string
	right string
}

type kitSQLScalar struct {
	name  string
	kind  string
	value value.Value
}

type kitSQLConflict struct {
	columns     []string
	action      string
	assignments map[string]kitSQLAssignment
}

type kitSQLAssignment struct {
	value    value.Value
	excluded string
}

type kitSQLStatement struct {
	kind              string
	table             string
	tableAlias        string
	distinct          bool
	search            *kitSQLSearch
	joins             []kitSQLJoin
	projections       []kitSQLProjection
	scalars           []kitSQLScalar
	values            map[string]value.Value
	updateExpressions map[string]*kitSQLExpression
	updateColumns     []string
	insertCols        []string
	insertRows        [][]value.Value
	conflict          *kitSQLConflict
	predicate         *query.Predicate
	whereExpression   *kitSQLExpression
	groups            []string
	having            *query.Predicate
	havingExpression  *kitSQLExpression
	conditions        []kitSQLCondition
	orders            []kitSQLOrder
	limit             int
	offset            int
	hasLimit          bool
	returning         []kitSQLProjection
	pragma            string
	createDatabase    *kitSQLCreateDatabase
	dropDatabase      *kitSQLDropDatabase
	renameDatabase    *kitSQLRenameDatabase
	createTable       *kitSQLCreateTable
	createIndex       *kitSQLCreateIndex
	dropTable         *kitSQLDropTable
	dropIndex         *kitSQLDropIndex
	renameTable       *kitSQLRenameTable
	renameIndex       *kitSQLRenameIndex
	alterColumn       *kitSQLAlterColumn
	alterConstraint   *kitSQLAlterConstraint
	alterPrimaryKey   *kitSQLAlterPrimaryKey
}

type kitSQLParser struct {
	cursor          *kitdbsql.Cursor
	expressionDepth int
	bindings        kitSQLBindings
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
	lexed, err := kitdbsql.Lex(source)
	if err != nil {
		return nil, err
	}
	tokens := make([]kitSQLToken, len(lexed))
	for index, token := range lexed {
		tokens[index] = kitSQLToken{kind: token.Kind, text: token.Text}
	}
	return tokens, nil
}

func parseKitSQL(source string, bindings kitSQLBindings) (kitSQLStatement, error) {
	envelope, err := kitdbsql.ParseEnvelope(source)
	if err != nil {
		return kitSQLStatement{}, err
	}
	cursor, err := kitdbsql.NewCursor(envelope.Tokens, envelope.Body)
	if err != nil {
		return kitSQLStatement{}, err
	}
	parser := &kitSQLParser{cursor: cursor, bindings: bindings}
	var statement kitSQLStatement
	switch envelope.Kind {
	case kitdbsql.StatementExplain:
		if envelope.ExplainAnalyze {
			return kitSQLStatement{}, fmt.Errorf("kitdb SQL: EXPLAIN ANALYZE requires the standalone KitDB executor")
		}
		statement, err = parser.parseSelect()
		if err == nil {
			if statement.kind != "select" {
				return kitSQLStatement{}, fmt.Errorf("kitdb SQL: EXPLAIN requires SELECT ... FROM a struct")
			}
			statement.kind = "explain"
		}
	case kitdbsql.StatementSelect:
		statement, err = parser.parseSelect()
	case kitdbsql.StatementInsert:
		statement, err = parser.parseInsert()
	case kitdbsql.StatementUpdate:
		statement, err = parser.parseUpdate()
	case kitdbsql.StatementDelete:
		statement, err = parser.parseDelete()
	case kitdbsql.StatementCreate:
		statement, err = parser.parseCreate()
	case kitdbsql.StatementAlter:
		statement, err = parser.parseAlter()
	case kitdbsql.StatementDrop:
		statement, err = parser.parseDrop()
	case kitdbsql.StatementAnalyze:
		statement, err = parser.parseAnalyze()
	case kitdbsql.StatementPragma:
		statement, err = parser.parsePragma()
	default:
		err = fmt.Errorf("kitdb SQL: unsupported statement")
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
	if (statement.kind == "select" || statement.kind == "explain") &&
		statement.hasLimit && statement.limit > kitDBRemoteSelectRowLimit {
		return kitSQLStatement{}, fmt.Errorf(
			"kitdb SQL: LIMIT exceeds the remote result limit of %d rows",
			kitDBRemoteSelectRowLimit,
		)
	}
	return statement, nil
}

func (parser *kitSQLParser) peek() kitSQLToken {
	return kitSQLTokenFromContract(parser.cursor.Peek())
}

func (parser *kitSQLParser) take() kitSQLToken {
	return kitSQLTokenFromContract(parser.cursor.Take())
}

func (parser *kitSQLParser) acceptKeyword(keyword string) bool {
	return parser.cursor.AcceptKeyword(keyword)
}

func (parser *kitSQLParser) expectKeyword(keyword string) error {
	return parser.cursor.ExpectKeyword(keyword)
}

func (parser *kitSQLParser) acceptSymbol(symbol string) bool {
	return parser.cursor.AcceptSymbol(symbol)
}

func (parser *kitSQLParser) expectSymbol(symbol string) error {
	return parser.cursor.ExpectSymbol(symbol)
}

func kitSQLTokenFromContract(token kitdbsql.Token) kitSQLToken {
	return kitSQLToken{kind: token.Kind, text: token.Text}
}

func (parser *kitSQLParser) tokenPosition() int {
	return parser.cursor.Position()
}

func (parser *kitSQLParser) tokenAt(position int) kitSQLToken {
	token, _ := parser.cursor.At(position)
	return kitSQLTokenFromContract(token)
}

func (parser *kitSQLParser) tokenRange(start, end int) []kitSQLToken {
	tokens, found := parser.cursor.Slice(start, end)
	if !found {
		return nil
	}
	result := make([]kitSQLToken, len(tokens))
	for index, token := range tokens {
		result[index] = kitSQLTokenFromContract(token)
	}
	return result
}

func (parser *kitSQLParser) advanceTokens(count int) bool {
	return parser.cursor.Advance(count)
}

func (parser *kitSQLParser) restoreTokens(position int) bool {
	return parser.cursor.Restore(position)
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

func (parser *kitSQLParser) columnReference(allowStar bool) (string, error) {
	token := parser.take()
	if token.kind != kitSQLTokenIdentifier || token.text == "" {
		return "", fmt.Errorf("kitdb SQL: expected column reference, got %q", token.text)
	}
	parts := []string{token.text}
	for parser.acceptSymbol(".") {
		if allowStar && parser.acceptSymbol("*") {
			parts = append(parts, "*")
			return strings.Join(parts, "."), nil
		}
		next := parser.take()
		if next.kind != kitSQLTokenIdentifier || next.text == "" {
			return "", fmt.Errorf("kitdb SQL: expected identifier after dot")
		}
		parts = append(parts, next.text)
	}
	return strings.Join(parts, "."), nil
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
	if strings.HasPrefix(placeholder, "?") || strings.HasPrefix(placeholder, "$") {
		index, err := strconv.Atoi(placeholder[1:])
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
	distinct := parser.acceptKeyword("distinct")
	projections, err := parser.projectionList(true)
	if err != nil {
		return kitSQLStatement{}, err
	}
	if !parser.acceptKeyword("from") {
		return kitSQLScalarStatement(projections, distinct)
	}
	table, err := parser.identifier()
	if err != nil {
		return kitSQLStatement{}, err
	}
	alias, err := parser.optionalTableAlias(table)
	if err != nil {
		return kitSQLStatement{}, err
	}
	statement := kitSQLStatement{
		kind: "select", table: table, tableAlias: alias, distinct: distinct, projections: projections,
	}
	for parser.joinStart() {
		if len(statement.joins) >= kitDBRemoteJoinLimit {
			return kitSQLStatement{}, fmt.Errorf("kitdb SQL: SELECT supports at most %d JOIN", kitDBRemoteJoinLimit)
		}
		join, err := parser.parseJoin()
		if err != nil {
			return kitSQLStatement{}, err
		}
		statement.joins = append(statement.joins, join)
	}
	if err := parser.parseTail(&statement, true); err != nil {
		return kitSQLStatement{}, err
	}
	return statement, nil
}

func (parser *kitSQLParser) optionalTableAlias(fallback string) (string, error) {
	if parser.acceptKeyword("as") {
		return parser.identifier()
	}
	if parser.peek().kind == kitSQLTokenIdentifier && !kitSQLClauseKeyword(parser.peek().text) {
		return parser.take().text, nil
	}
	return fallback, nil
}

func (parser *kitSQLParser) joinStart() bool {
	if parser.peek().kind != kitSQLTokenIdentifier {
		return false
	}
	switch strings.ToLower(parser.peek().text) {
	case "join", "inner", "left":
		return true
	default:
		return false
	}
}

func (parser *kitSQLParser) parseJoin() (kitSQLJoin, error) {
	join := kitSQLJoin{kind: "inner"}
	switch {
	case parser.acceptKeyword("join"):
	case parser.acceptKeyword("inner"):
		if err := parser.expectKeyword("join"); err != nil {
			return kitSQLJoin{}, err
		}
	case parser.acceptKeyword("left"):
		join.kind = "left"
		parser.acceptKeyword("outer")
		if err := parser.expectKeyword("join"); err != nil {
			return kitSQLJoin{}, err
		}
	default:
		return kitSQLJoin{}, fmt.Errorf("kitdb SQL: expected INNER or LEFT JOIN")
	}
	var err error
	join.table, err = parser.identifier()
	if err != nil {
		return kitSQLJoin{}, err
	}
	join.alias, err = parser.optionalTableAlias(join.table)
	if err != nil {
		return kitSQLJoin{}, err
	}
	if err := parser.expectKeyword("on"); err != nil {
		return kitSQLJoin{}, err
	}
	join.left, err = parser.columnReference(false)
	if err != nil {
		return kitSQLJoin{}, err
	}
	if err := parser.expectSymbol("="); err != nil {
		return kitSQLJoin{}, fmt.Errorf("kitdb SQL: JOIN ON supports one equality comparison")
	}
	join.right, err = parser.columnReference(false)
	if err != nil {
		return kitSQLJoin{}, err
	}
	return join, nil
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
		start := parser.tokenPosition()
		item, err := parser.operand()
		if err != nil {
			return kitSQLStatement{}, err
		}
		name := kitSQLScalarName(parser.tokenRange(start, parser.tokenPosition()), len(scalars)+1)
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
	statement := kitSQLStatement{kind: "insert", table: table}
	if parser.acceptKeyword("default") {
		if err := parser.expectKeyword("values"); err != nil {
			return kitSQLStatement{}, err
		}
		statement.insertRows = [][]value.Value{{}}
	} else {
		if parser.acceptSymbol("(") {
			statement.insertCols, err = parser.identifierList(")")
			if err != nil {
				return kitSQLStatement{}, err
			}
			seen := make(map[string]struct{}, len(statement.insertCols))
			for _, column := range statement.insertCols {
				key := strings.ToLower(column)
				if _, duplicate := seen[key]; duplicate {
					return kitSQLStatement{}, fmt.Errorf("kitdb SQL: duplicate INSERT column %q", column)
				}
				seen[key] = struct{}{}
			}
		}
		if err := parser.expectKeyword("values"); err != nil {
			return kitSQLStatement{}, err
		}
		for {
			if len(statement.insertRows) >= kitDBRemoteInsertRowLimit {
				return kitSQLStatement{}, fmt.Errorf("kitdb SQL: INSERT exceeds %d rows", kitDBRemoteInsertRowLimit)
			}
			if err := parser.expectSymbol("("); err != nil {
				return kitSQLStatement{}, err
			}
			items := make([]value.Value, 0, len(statement.insertCols))
			if parser.acceptSymbol(")") {
				return kitSQLStatement{}, fmt.Errorf("kitdb SQL: INSERT row cannot be empty")
			}
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
			if len(statement.insertCols) != 0 && len(items) != len(statement.insertCols) {
				return kitSQLStatement{}, fmt.Errorf(
					"kitdb SQL: INSERT has %d columns but row %d has %d values",
					len(statement.insertCols), len(statement.insertRows)+1, len(items),
				)
			}
			if len(statement.insertRows) != 0 && len(items) != len(statement.insertRows[0]) {
				return kitSQLStatement{}, fmt.Errorf(
					"kitdb SQL: INSERT row %d has %d values; row 1 has %d",
					len(statement.insertRows)+1, len(items), len(statement.insertRows[0]),
				)
			}
			statement.insertRows = append(statement.insertRows, items)
			if !parser.acceptSymbol(",") {
				break
			}
		}
	}
	if parser.acceptKeyword("on") {
		statement.conflict, err = parser.parseInsertConflict()
		if err != nil {
			return kitSQLStatement{}, err
		}
	}
	if parser.acceptKeyword("returning") {
		statement.returning, err = parser.projectionList(false)
		if err != nil {
			return kitSQLStatement{}, err
		}
	}
	return statement, nil
}

func (parser *kitSQLParser) parseInsertConflict() (*kitSQLConflict, error) {
	if err := parser.expectKeyword("conflict"); err != nil {
		return nil, err
	}
	conflict := &kitSQLConflict{}
	if parser.acceptSymbol("(") {
		seen := make(map[string]struct{})
		for {
			column, err := parser.identifier()
			if err != nil {
				return nil, err
			}
			key := strings.ToLower(column)
			if _, duplicate := seen[key]; duplicate {
				return nil, fmt.Errorf("kitdb SQL: ON CONFLICT repeats target column %q", column)
			}
			seen[key] = struct{}{}
			conflict.columns = append(conflict.columns, column)
			if parser.acceptSymbol(")") {
				break
			}
			if err := parser.expectSymbol(","); err != nil {
				return nil, err
			}
		}
	}
	if err := parser.expectKeyword("do"); err != nil {
		return nil, err
	}
	if parser.acceptKeyword("nothing") {
		conflict.action = "nothing"
		return conflict, nil
	}
	if err := parser.expectKeyword("update"); err != nil {
		return nil, fmt.Errorf("kitdb SQL: ON CONFLICT expects DO NOTHING or DO UPDATE")
	}
	if len(conflict.columns) == 0 {
		return nil, fmt.Errorf("kitdb SQL: ON CONFLICT DO UPDATE requires a conflict target")
	}
	if err := parser.expectKeyword("set"); err != nil {
		return nil, err
	}
	conflict.action = "update"
	conflict.assignments = map[string]kitSQLAssignment{}
	seen := map[string]struct{}{}
	for {
		column, err := parser.identifier()
		if err != nil {
			return nil, err
		}
		key := strings.ToLower(column)
		if _, duplicate := seen[key]; duplicate {
			return nil, fmt.Errorf("kitdb SQL: duplicate ON CONFLICT assignment %q", column)
		}
		seen[key] = struct{}{}
		if err := parser.expectSymbol("="); err != nil {
			return nil, err
		}
		assignment, err := parser.insertConflictAssignment()
		if err != nil {
			return nil, err
		}
		conflict.assignments[column] = assignment
		if !parser.acceptSymbol(",") {
			break
		}
	}
	return conflict, nil
}

func (parser *kitSQLParser) insertConflictAssignment() (kitSQLAssignment, error) {
	if parser.peek().kind == kitSQLTokenIdentifier && strings.EqualFold(parser.peek().text, "excluded") {
		parser.take()
		if err := parser.expectSymbol("."); err != nil {
			return kitSQLAssignment{}, fmt.Errorf("kitdb SQL: EXCLUDED must reference a column")
		}
		column, err := parser.identifier()
		if err != nil {
			return kitSQLAssignment{}, err
		}
		return kitSQLAssignment{excluded: column}, nil
	}
	item, err := parser.operand()
	if err != nil {
		return kitSQLAssignment{}, err
	}
	return kitSQLAssignment{value: item}, nil
}

func (parser *kitSQLParser) parseUpdate() (kitSQLStatement, error) {
	table, err := parser.identifier()
	if err != nil {
		return kitSQLStatement{}, err
	}
	if err := parser.expectKeyword("set"); err != nil {
		return kitSQLStatement{}, err
	}
	statement := kitSQLStatement{
		kind: "update", table: table, values: map[string]value.Value{},
		updateExpressions: map[string]*kitSQLExpression{},
	}
	seen := make(map[string]struct{})
	for {
		column, err := parser.identifier()
		if err != nil {
			return kitSQLStatement{}, err
		}
		key := strings.ToLower(column)
		if _, duplicate := seen[key]; duplicate {
			return kitSQLStatement{}, fmt.Errorf("kitdb SQL: duplicate UPDATE column %q", column)
		}
		seen[key] = struct{}{}
		statement.updateColumns = append(statement.updateColumns, column)
		if err := parser.expectSymbol("="); err != nil {
			return kitSQLStatement{}, err
		}
		expression, err := parser.expression()
		if err != nil {
			return kitSQLStatement{}, err
		}
		if expression.kind == kitSQLExpressionLiteral {
			statement.values[column] = expression.literal
		} else {
			statement.updateExpressions[column] = expression
		}
		if !parser.acceptSymbol(",") {
			break
		}
	}
	if err := parser.parseTail(&statement, false); err != nil {
		return kitSQLStatement{}, err
	}
	if parser.acceptKeyword("returning") {
		statement.returning, err = parser.projectionList(false)
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
		statement.returning, err = parser.projectionList(false)
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
	if name != "table_info" && name != "index_list" && name != "index_info" &&
		name != "foreign_key_list" && name != "migration_status" && name != "index_status" &&
		name != "statistics" && name != "import_status" && name != "import_cancel" &&
		name != "import_forget" {
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
	kind := "pragma"
	if name == "import_cancel" || name == "import_forget" {
		kind = name
	}
	return kitSQLStatement{kind: kind, pragma: name, table: table}, nil
}

func (parser *kitSQLParser) parseAnalyze() (kitSQLStatement, error) {
	statement := kitSQLStatement{kind: "analyze"}
	if parser.peek().kind == kitSQLTokenEOF ||
		(parser.peek().kind == kitSQLTokenSymbol && parser.peek().text == ";") {
		return statement, nil
	}
	table, err := parser.identifier()
	if err != nil {
		return kitSQLStatement{}, err
	}
	statement.table = table
	return statement, nil
}

func (parser *kitSQLParser) identifierOrString() (string, error) {
	if parser.peek().kind == kitSQLTokenString {
		return parser.take().text, nil
	}
	return parser.identifier()
}

func (parser *kitSQLParser) projectionList(allowAggregates bool) ([]kitSQLProjection, error) {
	projections := make([]kitSQLProjection, 0, 4)
	for {
		projection := kitSQLProjection{}
		if parser.acceptSymbol("*") {
			projection.all = true
			projection.field = "*"
		} else if position := parser.tokenPosition(); parser.tokenAt(position).kind == kitSQLTokenIdentifier &&
			parser.tokenAt(position+1).kind == kitSQLTokenSymbol && parser.tokenAt(position+1).text == "." &&
			parser.tokenAt(position+2).kind == kitSQLTokenSymbol && parser.tokenAt(position+2).text == "*" {
			projection.all = true
			projection.field = parser.tokenAt(position).text + ".*"
			parser.advanceTokens(3)
		} else {
			start := parser.tokenPosition()
			expression, err := parser.expression()
			if err != nil {
				return nil, err
			}
			projection.label = kitSQLScalarName(parser.tokenRange(start, parser.tokenPosition()), len(projections)+1)
			switch {
			case expression.kind == kitSQLExpressionReference:
				projection.field = expression.reference
			case expression.kind == kitSQLExpressionFunction && kitSQLAggregateName(expression.operator):
				if !allowAggregates {
					return nil, fmt.Errorf("kitdb SQL: aggregates are not allowed in RETURNING")
				}
				if len(expression.arguments) != 1 {
					return nil, fmt.Errorf("kitdb SQL: %s expects one field", strings.ToUpper(expression.operator))
				}
				argument := expression.arguments[0]
				switch argument.kind {
				case kitSQLExpressionStar:
					if expression.operator != "count" {
						return nil, fmt.Errorf("kitdb SQL: %s(*) is not supported", strings.ToUpper(expression.operator))
					}
					projection.field = "*"
				case kitSQLExpressionReference:
					projection.field = argument.reference
				default:
					return nil, fmt.Errorf("kitdb SQL: %s expects one field", strings.ToUpper(expression.operator))
				}
				projection.aggregate = expression.operator
			default:
				if !allowAggregates {
					if kitSQLExpressionContainsAggregate(expression) {
						return nil, fmt.Errorf("kitdb SQL: aggregates are not allowed in RETURNING")
					}
					return nil, fmt.Errorf("kitdb SQL: expressions are not allowed in RETURNING")
				}
				projection.expression = expression
			}
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

func (parser *kitSQLParser) aggregateFunction() (string, bool) {
	token := parser.peek()
	if token.kind != kitSQLTokenIdentifier {
		return "", false
	}
	next := parser.tokenAt(parser.tokenPosition() + 1)
	if next.kind != kitSQLTokenSymbol || next.text != "(" {
		return "", false
	}
	operation := strings.ToLower(token.text)
	switch operation {
	case "count", "sum", "avg", "min", "max":
		parser.advanceTokens(1)
		return operation, true
	default:
		return "", false
	}
}

func kitSQLClauseKeyword(text string) bool {
	switch strings.ToLower(text) {
	case "from", "where", "group", "by", "having", "order", "limit", "offset", "returning", "and", "or", "not",
		"in", "between", "like", "search", "is", "null", "on", "conflict", "do", "nothing",
		"update", "set", "excluded", "asc", "desc", "join", "inner", "left", "outer",
		"right", "full", "cross", "distinct", "case", "when", "then", "else", "end":
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
		expression, err := parser.expression()
		if err != nil {
			return err
		}
		search, residual, err := extractKitSQLSearch(expression)
		if err != nil {
			return err
		}
		if search != nil {
			if statement.kind != "select" {
				return fmt.Errorf("kitdb SQL: SEARCH is supported only in SELECT WHERE")
			}
			statement.search = search
			expression = residual
		}
		if expression != nil {
			if predicate, exact := kitSQLPredicateFromExpression(expression); exact {
				statement.predicate = predicate
			} else {
				statement.whereExpression = expression
				statement.predicate = kitSQLPlannerPredicateFromExpression(expression)
			}
		}
		if conditions, ok := query.ConjunctiveConditions(statement.predicate); ok {
			statement.conditions = make([]kitSQLCondition, len(conditions))
			for index, condition := range conditions {
				statement.conditions[index] = kitSQLCondition{
					column: condition.Column, operator: condition.Operator,
					value: kitDBAnyValue(condition.Value),
				}
			}
		}
	}
	if allowOrderLimit && parser.acceptKeyword("group") {
		if err := parser.expectKeyword("by"); err != nil {
			return err
		}
		for {
			column, err := parser.columnReference(false)
			if err != nil {
				return err
			}
			statement.groups = append(statement.groups, column)
			if !parser.acceptSymbol(",") {
				break
			}
		}
	}
	if allowOrderLimit && parser.acceptKeyword("having") {
		expression, err := parser.expression()
		if err != nil {
			return err
		}
		if predicate, exact := kitSQLPredicateFromExpression(expression); exact {
			statement.having = predicate
		} else {
			statement.havingExpression = expression
		}
	}
	if allowOrderLimit && parser.acceptKeyword("order") {
		if err := parser.expectKeyword("by"); err != nil {
			return err
		}
		for {
			expression, err := parser.expression()
			if err != nil {
				return err
			}
			order := kitSQLOrder{expression: expression}
			if expression.kind == kitSQLExpressionReference {
				order.column = expression.reference
				order.expression = nil
			}
			direction := "asc"
			if parser.acceptKeyword("asc") {
				direction = "asc"
			} else if parser.acceptKeyword("desc") {
				direction = "desc"
			}
			order.direction = direction
			statement.orders = append(statement.orders, order)
			if !parser.acceptSymbol(",") {
				break
			}
		}
	}
	if allowOrderLimit && parser.acceptKeyword("limit") {
		first, err := parser.nonNegativeInteger("LIMIT")
		if err != nil {
			return err
		}
		statement.limit, statement.hasLimit = first, true
		if parser.acceptSymbol(",") {
			limit, err := parser.nonNegativeInteger("LIMIT")
			if err != nil {
				return err
			}
			statement.offset, statement.limit = first, limit
		} else if parser.acceptKeyword("offset") {
			offset, err := parser.nonNegativeInteger("OFFSET")
			if err != nil {
				return err
			}
			statement.offset = offset
		}
	}
	return nil
}

func (parser *kitSQLParser) conditionExpression(allowAggregates bool) (*query.Predicate, error) {
	predicate, err := parser.conditionOr(allowAggregates)
	if err != nil {
		return nil, err
	}
	return &predicate, nil
}

func (parser *kitSQLParser) conditionOr(allowAggregates bool) (query.Predicate, error) {
	left, err := parser.conditionAnd(allowAggregates)
	if err != nil {
		return query.Predicate{}, err
	}
	for parser.acceptKeyword("or") {
		right, err := parser.conditionAnd(allowAggregates)
		if err != nil {
			return query.Predicate{}, err
		}
		left = kitSQLLogicalPredicate(query.PredicateOr, left, right)
	}
	return left, nil
}

func (parser *kitSQLParser) conditionAnd(allowAggregates bool) (query.Predicate, error) {
	left, err := parser.conditionNot(allowAggregates)
	if err != nil {
		return query.Predicate{}, err
	}
	for parser.acceptKeyword("and") {
		right, err := parser.conditionNot(allowAggregates)
		if err != nil {
			return query.Predicate{}, err
		}
		left = kitSQLLogicalPredicate(query.PredicateAnd, left, right)
	}
	return left, nil
}

func (parser *kitSQLParser) conditionNot(allowAggregates bool) (query.Predicate, error) {
	if parser.acceptKeyword("not") {
		child, err := parser.conditionNot(allowAggregates)
		if err != nil {
			return query.Predicate{}, err
		}
		return query.Predicate{Kind: query.PredicateNot, Children: []query.Predicate{child}}, nil
	}
	if parser.acceptSymbol("(") {
		nested, err := parser.conditionOr(allowAggregates)
		if err != nil {
			return query.Predicate{}, err
		}
		if err := parser.expectSymbol(")"); err != nil {
			return query.Predicate{}, err
		}
		return nested, nil
	}
	return parser.conditionComparison(allowAggregates)
}

func (parser *kitSQLParser) conditionComparison(allowAggregates bool) (query.Predicate, error) {
	column, err := parser.conditionReference(allowAggregates)
	if err != nil {
		return query.Predicate{}, err
	}
	condition := query.Condition{Column: column, Logic: "AND"}
	if parser.acceptKeyword("is") {
		condition.Operator = "is null"
		if parser.acceptKeyword("not") {
			condition.Operator = "is not null"
		}
		if err := parser.expectKeyword("null"); err != nil {
			return query.Predicate{}, err
		}
		condition.Value = value.NewNil()
		return kitSQLConditionPredicate(condition), nil
	}

	negated := parser.acceptKeyword("not")
	switch {
	case parser.acceptKeyword("in"):
		condition.Operator = "in"
		if negated {
			condition.Operator = "not in"
		}
		if err := parser.expectSymbol("("); err != nil {
			return query.Predicate{}, err
		}
		if parser.acceptSymbol(")") {
			return query.Predicate{}, fmt.Errorf("kitdb SQL: IN requires at least one value")
		}
		items := make([]value.Value, 0, 4)
		for {
			item, err := parser.operand()
			if err != nil {
				return query.Predicate{}, err
			}
			items = append(items, item)
			if parser.acceptSymbol(")") {
				break
			}
			if err := parser.expectSymbol(","); err != nil {
				return query.Predicate{}, err
			}
		}
		condition.Value = value.New(items)
	case parser.acceptKeyword("between"):
		condition.Operator = "between"
		if negated {
			condition.Operator = "not between"
		}
		lower, err := parser.operand()
		if err != nil {
			return query.Predicate{}, err
		}
		if err := parser.expectKeyword("and"); err != nil {
			return query.Predicate{}, err
		}
		upper, err := parser.operand()
		if err != nil {
			return query.Predicate{}, err
		}
		condition.Value = value.New([]value.Value{lower, upper})
	case parser.acceptKeyword("like"):
		condition.Operator = "like"
		if negated {
			condition.Operator = "not like"
		}
		item, err := parser.operand()
		if err != nil {
			return query.Predicate{}, err
		}
		condition.Value = item
	default:
		if negated {
			return query.Predicate{}, fmt.Errorf("kitdb SQL: expected IN, BETWEEN, or LIKE after NOT")
		}
		operator := parser.take()
		if operator.kind != kitSQLTokenSymbol || !kitSQLComparisonOperator(operator.text) {
			return query.Predicate{}, fmt.Errorf("kitdb SQL: unsupported comparison operator %q", operator.text)
		}
		condition.Operator = operator.text
		item, err := parser.operand()
		if err != nil {
			return query.Predicate{}, err
		}
		condition.Value = item
	}
	return kitSQLConditionPredicate(condition), nil
}

func (parser *kitSQLParser) conditionReference(allowAggregates bool) (string, error) {
	if allowAggregates {
		if operation, ok := parser.aggregateFunction(); ok {
			if err := parser.expectSymbol("("); err != nil {
				return "", err
			}
			field := ""
			if operation == "count" && parser.acceptSymbol("*") {
				field = "*"
			} else {
				var err error
				field, err = parser.identifier()
				if err != nil {
					return "", fmt.Errorf("kitdb SQL: %s expects one field", strings.ToUpper(operation))
				}
			}
			if err := parser.expectSymbol(")"); err != nil {
				return "", err
			}
			return kitSQLAggregateReference(operation, field), nil
		}
	}
	return parser.columnReference(false)
}

func kitSQLAggregateReference(operation, field string) string {
	return kitSQLAggregateReferencePrefix + strings.ToLower(operation) + "\x00" + field
}

func parseKitSQLAggregateReference(reference string) (string, string, bool) {
	encoded, found := strings.CutPrefix(reference, kitSQLAggregateReferencePrefix)
	if !found {
		return "", "", false
	}
	operation, field, found := strings.Cut(encoded, "\x00")
	return operation, field, found && operation != "" && field != ""
}

func kitSQLConditionPredicate(condition query.Condition) query.Predicate {
	return query.Predicate{Kind: query.PredicateCondition, Condition: condition}
}

func kitSQLLogicalPredicate(kind query.PredicateKind, left, right query.Predicate) query.Predicate {
	children := make([]query.Predicate, 0, 2)
	if left.Kind == kind {
		children = append(children, left.Children...)
	} else {
		children = append(children, left)
	}
	if right.Kind == kind {
		children = append(children, right.Children...)
	} else {
		children = append(children, right)
	}
	return query.Predicate{Kind: kind, Children: children}
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
	if readonly && kitSQLStatementWrites(statement.kind) {
		return kitDBRemoteResult{}, fmt.Errorf("database is read-only (serve access: readonly); writes are refused")
	}
	if database.transaction != nil {
		switch statement.kind {
		case "create_table", "create_index", "alter_add_column", "alter_rename_column",
			"alter_drop_column", "alter_rename_table", "alter_rename_index", "alter_add_constraint", "alter_drop_constraint", "alter_column_type", "alter_primary_key", "alter_cancel_migration", "drop_table", "drop_index":
			return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: schema changes are not allowed inside a record transaction")
		case "import_cancel", "import_forget":
			return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: import lifecycle changes are not allowed inside a record transaction")
		}
	}
	result, executeErr := executeKitDBRemoteStatement(ctx, scope, database, statement)
	if (!errors.Is(executeErr, errKitDBRowMigrationPending) &&
		!errors.Is(executeErr, errKitDBStaleSchema) &&
		!errors.Is(executeErr, errKitDBLayoutAdvanced)) ||
		!kitDBRemoteStatementRetriesAfterCatalogAdvance(statement.kind) {
		return result, executeErr
	}
	database.markKitDBCatalogPending()
	if refreshErr := database.refreshKitDBCatalogDefinitions(); refreshErr != nil {
		return kitDBRemoteResult{}, errors.Join(executeErr, refreshErr)
	}
	return executeKitDBRemoteStatement(ctx, scope, database, statement)
}

func executeKitDBRemoteStatement(
	ctx context.Context,
	scope *requestscope.Scope,
	database *dbProxy,
	statement kitSQLStatement,
) (kitDBRemoteResult, error) {
	switch statement.kind {
	case "select_scalar":
		return executeKitDBRemoteScalar(statement), nil
	case "explain":
		return executeKitDBRemoteExplain(ctx, scope, database, statement)
	case "select":
		return executeKitDBRemoteSelect(ctx, scope, database, statement)
	case "insert":
		return executeKitDBRemoteInsert(ctx, scope, database, statement)
	case "update":
		return executeKitDBRemoteUpdate(ctx, scope, database, statement)
	case "delete":
		return executeKitDBRemoteDelete(ctx, scope, database, statement)
	case "analyze":
		return executeKitDBRemoteAnalyze(ctx, scope, database, statement)
	case "create_table":
		return executeKitDBRemoteCreateTable(ctx, database, statement)
	case "create_database":
		return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: CREATE DATABASE is available only through the PostgreSQL node gateway")
	case "drop_database":
		return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: DROP DATABASE is available only through the PostgreSQL node gateway")
	case "alter_rename_database":
		return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: ALTER DATABASE is available only through the PostgreSQL node gateway")
	case "create_index":
		return executeKitDBRemoteCreateIndex(ctx, scope, database, statement)
	case "drop_table":
		return executeKitDBRemoteDropTable(ctx, scope, database, statement)
	case "drop_index":
		return executeKitDBRemoteDropIndex(ctx, scope, database, statement)
	case "alter_rename_table":
		return executeKitDBRemoteRenameTable(ctx, scope, database, statement)
	case "alter_rename_index":
		return executeKitDBRemoteRenameIndex(ctx, scope, database, statement)
	case "alter_add_column":
		return executeKitDBRemoteAlterAddColumn(ctx, scope, database, statement)
	case "alter_rename_column", "alter_drop_column", "alter_column_type":
		return executeKitDBRemoteAlterColumn(ctx, scope, database, statement)
	case "alter_add_constraint":
		return executeKitDBRemoteAddConstraint(ctx, scope, database, statement)
	case "alter_drop_constraint":
		return executeKitDBRemoteDropConstraint(ctx, scope, database, statement)
	case "alter_primary_key":
		return executeKitDBRemoteAlterPrimaryKey(ctx, scope, database, statement)
	case "alter_cancel_migration":
		return executeKitDBRemoteCancelMigration(ctx, scope, database, statement)
	case "pragma":
		return executeKitDBRemotePragma(ctx, scope, database, statement)
	case "import_cancel":
		return executeKitDBRemoteCancelImport(ctx, scope, database, statement.table)
	case "import_forget":
		return executeKitDBRemoteForgetImport(ctx, scope, database, statement.table)
	default:
		return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: unsupported statement")
	}
}

func kitDBRemoteStatementRetriesAfterCatalogAdvance(kind string) bool {
	switch kind {
	case "select", "insert", "update", "delete", "explain":
		return true
	default:
		return false
	}
}

func kitSQLStatementWrites(kind string) bool {
	switch kind {
	case "insert", "update", "delete", "analyze", "create_database", "drop_database", "create_table", "create_index", "alter_add_column",
		"alter_rename_column", "alter_drop_column", "alter_rename_table", "alter_rename_index", "alter_add_constraint", "alter_drop_constraint", "alter_column_type", "alter_primary_key", "alter_cancel_migration",
		"drop_table", "drop_index", "import_cancel", "import_forget":
		return true
	default:
		return false
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

func kitSQLScalarStatement(projections []kitSQLProjection, distinct bool) (kitSQLStatement, error) {
	scalars := make([]kitSQLScalar, len(projections))
	for index, projection := range projections {
		if projection.all || projection.aggregate != "" || projection.expression == nil {
			return kitSQLStatement{}, fmt.Errorf("kitdb SQL: SELECT without FROM accepts scalar expressions only")
		}
		if _, err := resolveKitSQLExpression(projection.expression, nil); err != nil {
			return kitSQLStatement{}, err
		}
		item, err := evaluateKitSQLExpression(projection.expression, nil)
		if err != nil {
			return kitSQLStatement{}, err
		}
		name := projection.alias
		if name == "" {
			name = projection.label
		}
		scalars[index] = kitSQLScalar{name: name, kind: kitSQLScalarKind(item), value: item}
	}
	return kitSQLStatement{kind: "select_scalar", distinct: distinct, scalars: scalars}, nil
}

func kitDBRemoteTable(database *dbProxy, scope *requestscope.Scope, requested string) (*SchemaTable, error) {
	name, definition, err := kitDBRemoteDefinition(database, requested)
	if err != nil {
		return nil, err
	}
	tables, definitions := database.schemaSnapshot()
	return &SchemaTable{
		tenant: database.tenant, scope: scope, engine: "kitdb", dbName: database.dbName,
		migrate: database.migrate, table: name, columns: definition.columns, siblings: tables,
		definition: definition, definitions: definitions, transaction: database.transaction,
		catalogRequired: !database.kitDBStructIsSourceDeclared(name),
	}, nil
}

func kitDBRemoteDefinition(database *dbProxy, requested string) (string, *StructDef, error) {
	if err := database.refreshKitDBCatalogDefinitions(); err != nil {
		return "", nil, err
	}
	_, definitions := database.schemaSnapshot()
	if definition := definitions[requested]; definition != nil {
		return requested, definition, nil
	}
	match := ""
	for name := range definitions {
		if strings.EqualFold(name, requested) {
			if match != "" {
				return "", nil, fmt.Errorf("kitdb SQL: ambiguous struct %q", requested)
			}
			match = name
		}
	}
	if match == "" {
		return "", nil, fmt.Errorf("kitdb SQL: no such table: %s (no matching struct is declared)", requested)
	}
	return match, definitions[match], nil
}

func kitDBRemoteField(definition *StructDef, requested string) (string, StructFieldDef, error) {
	if definition == nil {
		return "", StructFieldDef{}, fmt.Errorf("kitdb SQL: struct is unavailable")
	}
	_, requested = kitSQLReferenceParts(requested)
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
		return "", StructFieldDef{}, fmt.Errorf("kitdb SQL: no such column: %s (struct %q)", requested, definition.Name)
	}
	return match, field, nil
}

func kitSQLReferenceParts(reference string) (string, string) {
	reference = strings.TrimSpace(reference)
	if split := strings.LastIndexByte(reference, '.'); split >= 0 {
		return reference[:split], reference[split+1:]
	}
	return "", reference
}

func kitDBRemoteProjectionAlias(
	projections []kitSQLProjection,
	requested string,
) (kitSQLProjection, bool, error) {
	var match kitSQLProjection
	found := false
	for _, projection := range projections {
		if projection.alias == "" || !strings.EqualFold(projection.alias, requested) {
			continue
		}
		if found && (match.field != projection.field || match.aggregate != projection.aggregate ||
			match.all != projection.all || match.expression != projection.expression) {
			return kitSQLProjection{}, false, fmt.Errorf("kitdb SQL: ORDER BY alias %q is ambiguous", requested)
		}
		match, found = projection, true
	}
	return match, found, nil
}

func applyKitDBRemoteNarrowing(table *SchemaTable, statement kitSQLStatement) error {
	if statement.predicate != nil {
		predicate, err := canonicalKitSQLPredicate(table.definition, *statement.predicate)
		if err != nil {
			return err
		}
		table.builder().WherePredicate(predicate)
	}
	if kitDBRemoteGrouped(statement) {
		if table.failed {
			return fmt.Errorf("%s", table.failMsg)
		}
		return nil
	}
	for _, order := range statement.orders {
		requested := order.column
		projection, found, err := kitDBRemoteProjectionAlias(statement.projections, requested)
		if err != nil {
			return err
		}
		if found {
			if projection.aggregate != "" {
				continue
			}
			requested = projection.field
		}
		column, _, err := kitDBRemoteField(table.definition, requested)
		if err != nil {
			return err
		}
		table.OrderBy(column, order.direction)
	}
	if statement.hasLimit {
		table.Limit(statement.limit)
		// Remote SQL has its own explicit, bounded result contract. Reusing the
		// fluent ORM ceiling silently changed LIMIT 300 into LIMIT 120.
		table.builder().Limited(kitDBRemoteSelectRowLimit)
	}
	if statement.offset > 0 {
		table.builder().Skip(statement.offset)
	}
	if table.failed {
		return fmt.Errorf("%s", table.failMsg)
	}
	return nil
}

func canonicalKitSQLPredicate(definition *StructDef, predicate query.Predicate) (query.Predicate, error) {
	canonical := predicate
	canonical.Children = make([]query.Predicate, len(predicate.Children))
	if predicate.Kind == query.PredicateCondition {
		column, field, err := kitDBRemoteField(definition, predicate.Condition.Column)
		if err != nil {
			return query.Predicate{}, err
		}
		canonical.Condition.Column = column
		if predicate.Condition.IsColumn {
			right, _, err := kitDBRemoteField(definition, fmt.Sprint(predicate.Condition.Value))
			if err != nil {
				return query.Predicate{}, err
			}
			canonical.Condition.Value = right
		} else {
			item, err := coerceKitDBSQLConditionValue(
				field, predicate.Condition.Operator, kitDBAnyValue(predicate.Condition.Value),
			)
			if err != nil {
				return query.Predicate{}, err
			}
			canonical.Condition.Value = item
		}
	}
	for index, child := range predicate.Children {
		resolved, err := canonicalKitSQLPredicate(definition, child)
		if err != nil {
			return query.Predicate{}, err
		}
		canonical.Children[index] = resolved
	}
	return canonical, nil
}

func coerceKitDBSQLConditionValue(
	field StructFieldDef,
	operator string,
	item value.Value,
) (value.Value, error) {
	switch strings.ToLower(strings.TrimSpace(operator)) {
	case "in", "not in", "between", "not between":
		if item.K == value.Array {
			items := item.Array()
			coerced := make([]value.Value, len(items))
			for index, candidate := range items {
				converted, err := coerceKitDBSQLScalar(field, candidate)
				if err != nil {
					return value.Value{}, err
				}
				coerced[index] = converted
			}
			return value.New(coerced), nil
		}
	}
	return coerceKitDBSQLScalar(field, item)
}

// PostgreSQL clients may bind an unknown parameter (OID 0) in text format.
// Resolve it from the declared field kind instead of guessing from its bytes,
// so numeric-looking text fields remain text.
func coerceKitDBSQLScalar(field StructFieldDef, item value.Value) (value.Value, error) {
	if item.IsNil() || item.K != value.String {
		return coerceWrite(field.Kind, item), nil
	}
	text := item.String()
	switch field.Kind {
	case "integer", "smallint", "int32", "serial", "year", "month", "day":
		bits := 64
		if field.Kind == "smallint" {
			bits = 16
		} else if field.Kind == "int32" {
			bits = 32
		}
		parsed, err := strconv.ParseInt(text, 10, bits)
		minimum, maximum, _ := kitDBVMIntegerBounds(field.Kind)
		if err != nil || float64(parsed) < minimum || float64(parsed) > maximum {
			return value.Value{}, fmt.Errorf(
				"kitdb SQL: field %q expects an integer, got %q", field.Name, text,
			)
		}
		return value.New(parsed), nil
	case "float":
		parsed, err := strconv.ParseFloat(text, 64)
		if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
			return value.Value{}, fmt.Errorf(
				"kitdb SQL: field %q expects a finite number, got %q", field.Name, text,
			)
		}
		return value.New(parsed), nil
	case "bool":
		switch strings.ToLower(strings.TrimSpace(text)) {
		case "true", "t", "1", "yes", "y", "on":
			return value.New(1), nil
		case "false", "f", "0", "no", "n", "off":
			return value.New(0), nil
		default:
			return value.Value{}, fmt.Errorf(
				"kitdb SQL: field %q expects a boolean, got %q", field.Name, text,
			)
		}
	case "decimal":
		if !validKitDBDecimal(text) {
			return value.Value{}, fmt.Errorf(
				"kitdb SQL: field %q expects decimal text, got %q", field.Name, text,
			)
		}
	}
	return coerceWrite(field.Kind, item), nil
}

func executeKitDBRemoteSelect(
	ctx context.Context,
	scope *requestscope.Scope,
	database *dbProxy,
	statement kitSQLStatement,
) (kitDBRemoteResult, error) {
	if statement.search != nil {
		return executeKitDBRemoteSearchSelect(ctx, scope, database, statement)
	}
	if len(statement.joins) != 0 {
		if strings.EqualFold(statement.table, "sqlite_master") ||
			strings.EqualFold(statement.table, "sqlite_schema") ||
			strings.EqualFold(statement.joins[0].table, "sqlite_master") ||
			strings.EqualFold(statement.joins[0].table, "sqlite_schema") {
			return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: JOIN is not supported on catalog metadata")
		}
		return executeKitDBRemoteJoin(ctx, scope, database, statement)
	}
	if strings.EqualFold(statement.table, "sqlite_master") || strings.EqualFold(statement.table, "sqlite_schema") {
		if kitDBRemoteGrouped(statement) {
			return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: GROUP BY and HAVING are not supported on catalog metadata")
		}
		if kitDBRemoteUsesExpressionEngine(statement) {
			return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: expressions are not supported on catalog metadata")
		}
		return executeKitDBRemoteCatalog(database, statement)
	}
	table, err := kitDBRemoteTable(database, scope, statement.table)
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	statement, err = lowerKitDBRemoteTableDistinct(table, statement)
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	if kitDBRemoteGrouped(statement) {
		if err := applyKitDBRemoteNarrowing(table, statement); err != nil {
			return kitDBRemoteResult{}, err
		}
		return executeKitDBRemoteGroups(ctx, table, statement)
	}
	if kitDBRemoteUsesExpressionEngine(statement) {
		return executeKitDBRemoteExpressionSelect(ctx, table, statement)
	}
	if err := applyKitDBRemoteNarrowing(table, statement); err != nil {
		return kitDBRemoteResult{}, err
	}
	aggregate, err := kitDBRemoteAggregateMode(statement.projections)
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	if aggregate {
		result, err := executeKitDBRemoteAggregates(table, statement)
		if err != nil {
			return kitDBRemoteResult{}, err
		}
		if err := ctx.Err(); err != nil {
			return kitDBRemoteResult{}, err
		}
		return result, nil
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

func executeKitDBRemoteExplain(
	ctx context.Context,
	scope *requestscope.Scope,
	database *dbProxy,
	statement kitSQLStatement,
) (kitDBRemoteResult, error) {
	if statement.search != nil {
		return executeKitDBRemoteSearchExplain(ctx, scope, database, statement)
	}
	if len(statement.joins) != 0 {
		if strings.EqualFold(statement.table, "sqlite_master") ||
			strings.EqualFold(statement.table, "sqlite_schema") ||
			strings.EqualFold(statement.joins[0].table, "sqlite_master") ||
			strings.EqualFold(statement.joins[0].table, "sqlite_schema") {
			return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: JOIN is not supported on catalog metadata")
		}
		return executeKitDBRemoteJoinExplain(ctx, scope, database, statement)
	}
	columns := []kitDBRemoteColumn{
		{name: "id", kind: "integer"},
		{name: "parent", kind: "integer"},
		{name: "notused", kind: "integer"},
		{name: "detail", kind: "text"},
	}
	if strings.EqualFold(statement.table, "sqlite_master") || strings.EqualFold(statement.table, "sqlite_schema") {
		if kitDBRemoteGrouped(statement) {
			return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: GROUP BY and HAVING are not supported on catalog metadata")
		}
		if kitDBRemoteUsesExpressionEngine(statement) {
			return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: expressions are not supported on catalog metadata")
		}
		detail := "KITDB SCAN catalog"
		if statement.distinct {
			detail += "; HASH DISTINCT"
		}
		return kitDBRemoteResult{
			columns: columns,
			rows:    [][]value.Value{{value.New(0), value.New(0), value.New(0), value.New(detail)}},
		}, nil
	}
	table, err := kitDBRemoteTable(database, scope, statement.table)
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	statement, err = lowerKitDBRemoteTableDistinct(table, statement)
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	if kitDBRemoteUsesExpressionEngine(statement) && !kitDBRemoteGrouped(statement) {
		return explainKitDBRemoteExpressionSelect(ctx, table, statement, columns)
	}
	if err := applyKitDBRemoteNarrowing(table, statement); err != nil {
		return kitDBRemoteResult{}, err
	}
	if kitDBRemoteGrouped(statement) {
		groupPlan, err := prepareKitDBRemoteGroupPlan(table, statement)
		if err != nil {
			return kitDBRemoteResult{}, err
		}
		if err := ensureKitDBRemoteTableReady(table); err != nil {
			return kitDBRemoteResult{}, err
		}
		sourcePlan := kitDBAggregateExecutionPlan(table.builder().ExecutionPlan())
		access, err := table.planKitDBAccess(sourcePlan)
		if err != nil {
			return kitDBRemoteResult{}, err
		}
		if err := ctx.Err(); err != nil {
			return kitDBRemoteResult{}, err
		}
		return kitDBRemoteResult{
			columns: columns,
			rows: [][]value.Value{{
				value.New(0), value.New(0), value.New(0),
				value.New(kitDBExplainGroupDetail(table.table, access, sourcePlan, statement, groupPlan)),
			}},
		}, nil
	}
	aggregate, err := kitDBRemoteAggregateMode(statement.projections)
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	if err := validateKitDBRemoteProjections(table.definition, statement.projections); err != nil {
		return kitDBRemoteResult{}, err
	}
	if err := ensureKitDBRemoteTableReady(table); err != nil {
		return kitDBRemoteResult{}, err
	}
	plan := table.builder().ExecutionPlan()
	if aggregate {
		plan = kitDBAggregateExecutionPlan(plan)
	}
	access, err := table.planKitDBAccess(plan)
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return kitDBRemoteResult{}, err
	}
	detail := kitDBExplainDetail(table.table, access, plan)
	if aggregate {
		detail = kitDBExplainAggregateDetail(table.table, access, plan)
	}
	return kitDBRemoteResult{
		columns: columns,
		rows: [][]value.Value{{
			value.New(0), value.New(0), value.New(0),
			value.New(detail),
		}},
	}, nil
}

func executeKitDBRemoteAggregates(table *SchemaTable, statement kitSQLStatement) (kitDBRemoteResult, error) {
	requests := make([]kitDBAggregateRequest, len(statement.projections))
	columns := make([]kitDBRemoteColumn, len(statement.projections))
	for index, projection := range statement.projections {
		field := projection.field
		var definition StructFieldDef
		if field != "*" {
			canonical, resolved, err := kitDBRemoteField(table.definition, field)
			if err != nil {
				return kitDBRemoteResult{}, err
			}
			field, definition = canonical, resolved
		}
		requests[index] = kitDBAggregateRequest{operation: projection.aggregate, field: field}
		name := projection.alias
		if name == "" {
			name = projection.aggregate + "(" + field + ")"
		}
		columns[index] = kitDBRemoteColumn{
			name: name,
			kind: kitDBRemoteAggregateKind(projection.aggregate, definition),
		}
	}
	if err := ensureKitDBRemoteTableReady(table); err != nil {
		return kitDBRemoteResult{}, err
	}
	values, err := table.kitDBAggregates(requests)
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	result := kitDBRemoteResult{columns: columns}
	if (statement.hasLimit && statement.limit == 0) || statement.offset > 0 {
		result.rows = [][]value.Value{}
		return result, nil
	}
	result.rows = [][]value.Value{values}
	return result, nil
}

func kitDBRemoteAggregateMode(projections []kitSQLProjection) (bool, error) {
	hasAggregate := false
	hasOrdinary := false
	for _, projection := range projections {
		if projection.aggregate != "" {
			hasAggregate = true
		} else {
			hasOrdinary = true
		}
	}
	if hasAggregate && hasOrdinary {
		return false, fmt.Errorf("kitdb SQL: aggregate projections cannot be mixed with ordinary fields without GROUP BY")
	}
	return hasAggregate, nil
}

func validateKitDBRemoteProjections(definition *StructDef, projections []kitSQLProjection) error {
	for _, projection := range projections {
		if projection.expression != nil {
			return fmt.Errorf("kitdb SQL: expression projection requires expression planning")
		}
		if projection.all || (projection.aggregate == "count" && projection.field == "*") {
			continue
		}
		_, field, err := kitDBRemoteField(definition, projection.field)
		if err != nil {
			return err
		}
		if (projection.aggregate == "sum" || projection.aggregate == "avg") && !kitDBNumericAggregateKind(field.Kind) {
			return fmt.Errorf(
				"kitdb SQL: %s(%s) requires a numeric field, got %s",
				strings.ToUpper(projection.aggregate), field.Name, field.Kind,
			)
		}
		if (projection.aggregate == "min" || projection.aggregate == "max") && !kitDBComparableAggregateKind(field.Kind) {
			return fmt.Errorf(
				"kitdb SQL: %s(%s) does not support field kind %s",
				strings.ToUpper(projection.aggregate), field.Name, field.Kind,
			)
		}
	}
	return nil
}

func kitDBRemoteAggregateKind(operation string, field StructFieldDef) string {
	switch operation {
	case "count":
		return "integer"
	case "avg":
		return "float"
	case "sum":
		if kitSQLIntegerKind(field.Kind) {
			return "integer"
		}
		return "float"
	default:
		return field.Kind
	}
}

func executeKitDBRemoteInsert(
	ctx context.Context,
	scope *requestscope.Scope,
	database *dbProxy,
	statement kitSQLStatement,
) (kitDBRemoteResult, error) {
	if len(statement.insertRows) == 0 {
		return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: INSERT has no rows")
	}
	if len(statement.returning) != 0 && len(statement.insertRows) > query.DefaultDBMaxLimit {
		return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: INSERT RETURNING is bounded to %d rows", query.DefaultDBMaxLimit)
	}

	target := database
	transaction := database.transaction
	ownedTransaction := false
	if (len(statement.insertRows) > 1 || statement.conflict != nil) && transaction == nil {
		var err error
		target, transaction, err = database.beginKitDBRecordTransaction(scope)
		if err != nil {
			return kitDBRemoteResult{}, err
		}
		ownedTransaction = true
	}
	var savepoint kitDBRecordSavepoint
	if transaction != nil {
		savepoint = transaction.Savepoint()
	}
	rollback := func() {
		if transaction == nil {
			return
		}
		if ownedTransaction {
			_ = transaction.Rollback()
			return
		}
		transaction.RollbackTo(savepoint)
	}

	table, err := kitDBRemoteTable(target, scope, statement.table)
	if err != nil {
		rollback()
		return kitDBRemoteResult{}, err
	}
	if len(statement.returning) != 0 {
		if _, err := kitDBRemoteProjectionColumns(table.definition, statement.returning); err != nil {
			rollback()
			return kitDBRemoteResult{}, err
		}
	}
	if err := ensureKitDBRemoteTableReady(table); err != nil {
		rollback()
		return kitDBRemoteResult{}, err
	}
	conflict, err := canonicalKitSQLConflict(table.definition, statement.conflict)
	if err != nil {
		rollback()
		return kitDBRemoteResult{}, err
	}
	createdRows := make([]value.Value, 0, len(statement.insertRows))
	affected := int64(0)
	for index, items := range statement.insertRows {
		row, err := kitDBRemoteInsertRow(table.definition, statement.insertCols, items, index)
		if err != nil {
			rollback()
			return kitDBRemoteResult{}, err
		}
		created := value.Value{}
		changed := true
		if conflict == nil {
			filled, message := fillRow(table.columns, row)
			if message != "" {
				rollback()
				return kitDBRemoteResult{}, fmt.Errorf(
					"kitdb SQL: INSERT row %d: table %q %s", index+1, table.table, message,
				)
			}
			created, err = table.createKitDBRow(filled)
			if err != nil {
				rollback()
				return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: INSERT row %d: %w", index+1, err)
			}
		} else {
			created, changed, err = executeKitDBRemoteConflictRow(table, transaction, row, conflict)
			if err != nil {
				rollback()
				return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: INSERT row %d: %w", index+1, err)
			}
		}
		if changed {
			if err := kitDBRemoteValueError(created); err != nil {
				rollback()
				return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: INSERT row %d: %w", index+1, err)
			}
			createdRows = append(createdRows, created)
			affected++
		}
		if err := ctx.Err(); err != nil {
			rollback()
			return kitDBRemoteResult{}, err
		}
	}
	if ownedTransaction {
		if _, err := transaction.Commit(); err != nil {
			return kitDBRemoteResult{}, err
		}
	}
	result := kitDBRemoteResult{affected: affected}
	if len(statement.returning) != 0 {
		projected, err := projectKitDBRemoteRows(table.definition, statement.returning, createdRows)
		if err != nil {
			return kitDBRemoteResult{}, err
		}
		result.columns, result.rows = projected.columns, projected.rows
	}
	return result, nil
}

func kitDBRemoteInsertRow(
	definition *StructDef,
	requestedColumns []string,
	items []value.Value,
	rowIndex int,
) (map[string]value.Value, error) {
	columns := requestedColumns
	if len(columns) == 0 && len(items) != 0 {
		if definition == nil || len(items) != len(definition.Fields) {
			available := 0
			if definition != nil {
				available = len(definition.Fields)
			}
			return nil, fmt.Errorf(
				"kitdb SQL: INSERT row %d has %d values but struct has %d fields",
				rowIndex+1, len(items), available,
			)
		}
		columns = make([]string, len(definition.Fields))
		for index, field := range definition.Fields {
			columns[index] = field.Name
		}
	}
	if len(columns) != len(items) {
		return nil, fmt.Errorf(
			"kitdb SQL: INSERT row %d has %d values for %d columns",
			rowIndex+1, len(items), len(columns),
		)
	}
	row := make(map[string]value.Value, len(columns))
	for index, requested := range columns {
		field, definitionField, err := kitDBRemoteField(definition, requested)
		if err != nil {
			return nil, err
		}
		item, err := coerceKitDBSQLScalar(definitionField, items[index])
		if err != nil {
			return nil, err
		}
		row[field] = item
	}
	return row, nil
}

func canonicalKitSQLConflict(definition *StructDef, conflict *kitSQLConflict) (*kitSQLConflict, error) {
	if conflict == nil {
		return nil, nil
	}
	canonical := &kitSQLConflict{action: conflict.action}
	for _, requested := range conflict.columns {
		column, _, err := kitDBRemoteField(definition, requested)
		if err != nil {
			return nil, err
		}
		canonical.columns = append(canonical.columns, column)
	}
	primary := definition.primaryFields()
	if len(canonical.columns) != 0 && kitDBFieldNamesMatch(canonical.columns, primary) {
		canonical.columns = kitDBStructFieldNamesInOrder(primary)
	} else if len(canonical.columns) == 1 {
		_, field, _ := kitDBRemoteField(definition, canonical.columns[0])
		if !field.Unique {
			return nil, fmt.Errorf("kitdb SQL: ON CONFLICT target %q is not primary or unique", canonical.columns[0])
		}
	} else if len(canonical.columns) > 1 {
		constraint, found, err := kitDBUniqueConstraintForColumns(definition, canonical.columns)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, fmt.Errorf(
				"kitdb SQL: ON CONFLICT target (%s) is not a unique constraint",
				strings.Join(canonical.columns, ", "),
			)
		}
		fields, err := structUniqueConstraintFields(definition, constraint)
		if err != nil {
			return nil, err
		}
		canonical.columns = canonical.columns[:0]
		for _, field := range fields {
			canonical.columns = append(canonical.columns, field.Name)
		}
	}
	if conflict.action == "update" {
		canonical.assignments = make(map[string]kitSQLAssignment, len(conflict.assignments))
		for requested, assignment := range conflict.assignments {
			column, field, err := kitDBRemoteField(definition, requested)
			if err != nil {
				return nil, err
			}
			if assignment.excluded != "" {
				excluded, _, err := kitDBRemoteField(definition, assignment.excluded)
				if err != nil {
					return nil, err
				}
				assignment.excluded = excluded
			} else {
				assignment.value, err = coerceKitDBSQLScalar(field, assignment.value)
				if err != nil {
					return nil, err
				}
			}
			canonical.assignments[column] = assignment
		}
	}
	return canonical, nil
}

func executeKitDBRemoteConflictRow(
	table *SchemaTable,
	transaction *kitDBRecordTransaction,
	provided map[string]value.Value,
	conflict *kitSQLConflict,
) (value.Value, bool, error) {
	if transaction == nil {
		return value.Value{}, false, fmt.Errorf("kitdb SQL: conflict handling requires a transaction")
	}
	row, message := fillRow(table.columns, provided)
	if message != "" {
		return value.Value{}, false, fmt.Errorf("table %q %s", table.table, message)
	}
	existing, found, err := findKitDBRemoteConflict(table, transaction, row, conflict.columns)
	if err != nil {
		return value.Value{}, false, err
	}
	if !found {
		created, err := table.createKitDBRow(row)
		return created, true, err
	}
	if conflict.action == "nothing" {
		return value.NewNil(), false, nil
	}

	changes := make(map[string]value.Value, len(conflict.assignments))
	for column, assignment := range conflict.assignments {
		if assignment.excluded == "" {
			changes[column] = assignment.value
			continue
		}
		item, found := row[assignment.excluded]
		if !found {
			item = value.NewNil()
		}
		changes[column] = item
	}
	primary := table.definition.primaryFields()
	if len(primary) == 0 {
		return value.Value{}, false, fmt.Errorf("kitdb SQL: struct %q has no primary key", table.table)
	}
	updateTable := *table
	updateTable.q = nil
	for _, field := range primary {
		updateTable.Where(value.New(field.Name), value.New("="), existing[field.Name])
	}
	updated := updateTable.Update(value.New(changes))
	if err := kitDBRemoteValueError(updated); err != nil {
		return value.Value{}, false, err
	}
	if updated.IsNil() {
		return value.Value{}, false, fmt.Errorf("kitdb SQL: updated conflict row disappeared")
	}
	return updated, true, nil
}

func findKitDBRemoteConflict(
	table *SchemaTable,
	reader kitDBReader,
	row map[string]value.Value,
	target []string,
) (map[string]value.Value, bool, error) {
	primary := table.definition.primaryFields()
	primaryTarget := len(target) == 0 || kitDBFieldNamesEqual(target, primary)
	if primaryTarget {
		complete := len(primary) != 0
		for _, field := range primary {
			item, found := row[field.Name]
			if !found || item.IsNil() {
				complete = false
				break
			}
		}
		if complete {
			rowKey, err := kitDBRowKeyForRow(table.definition, row)
			if err != nil {
				return nil, false, err
			}
			conflicting, found, err := loadKitDBRemoteConflictRow(table, reader, rowKey)
			if err != nil || found || len(target) != 0 {
				return conflicting, found, err
			}
		} else if len(target) != 0 {
			return nil, false, nil
		}
	}
	if len(target) > 1 {
		constraint, found, err := kitDBUniqueConstraintForColumns(table.definition, target)
		if err != nil {
			return nil, false, err
		}
		if !found {
			return nil, false, fmt.Errorf("kitdb SQL: conflict target (%s) disappeared", strings.Join(target, ", "))
		}
		uniqueKey, applicable, err := kitDBCompositeUniqueKey(table.definition, constraint, row)
		if err != nil || !applicable {
			return nil, false, err
		}
		rowKey, found, err := reader.Get(uniqueKey)
		if err != nil || !found {
			return nil, false, err
		}
		conflicting, found, err := loadKitDBRemoteConflictRow(table, reader, rowKey)
		if err != nil {
			return nil, false, err
		}
		if found {
			return conflicting, true, nil
		}
		return nil, false, nil
	}
	fields := table.definition.Fields
	if len(target) == 1 {
		field, found := kitDBField(table.definition, target[0])
		if !found {
			return nil, false, fmt.Errorf("kitdb SQL: conflict target %q disappeared", target[0])
		}
		fields = []StructFieldDef{field}
	}
	for _, field := range fields {
		if field.Primary || !field.Unique {
			continue
		}
		item, found := row[field.Name]
		if !found || item.IsNil() {
			continue
		}
		var rowKey []byte
		var err error
		uniqueKey, keyErr := kitDBUniqueKey(table.definition, field, item)
		if keyErr != nil {
			return nil, false, keyErr
		}
		rowKey, found, err = reader.Get(uniqueKey)
		if err != nil || !found {
			continue
		}
		if err != nil {
			return nil, false, err
		}
		conflicting, found, err := loadKitDBRemoteConflictRow(table, reader, rowKey)
		if err != nil {
			return nil, false, err
		}
		if found {
			return conflicting, true, nil
		}
	}
	if len(target) == 0 {
		for _, constraint := range table.definition.UniqueConstraints {
			uniqueKey, applicable, err := kitDBCompositeUniqueKey(table.definition, constraint, row)
			if err != nil {
				return nil, false, err
			}
			if !applicable {
				continue
			}
			rowKey, found, err := reader.Get(uniqueKey)
			if err != nil {
				return nil, false, err
			}
			if found {
				return loadKitDBRemoteConflictRow(table, reader, rowKey)
			}
		}
	}
	return nil, false, nil
}

func kitDBFieldNamesEqual(names []string, fields []StructFieldDef) bool {
	if len(names) == 0 || len(names) != len(fields) {
		return false
	}
	for index := range names {
		if names[index] != fields[index].Name {
			return false
		}
	}
	return true
}

func kitDBFieldNamesMatch(names []string, fields []StructFieldDef) bool {
	if len(names) == 0 || len(names) != len(fields) {
		return false
	}
	remaining := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		remaining[field.Name] = struct{}{}
	}
	for _, name := range names {
		if _, found := remaining[name]; !found {
			return false
		}
		delete(remaining, name)
	}
	return len(remaining) == 0
}

func kitDBStructFieldNamesInOrder(fields []StructFieldDef) []string {
	names := make([]string, len(fields))
	for index, field := range fields {
		names[index] = field.Name
	}
	return names
}

func kitDBUniqueConstraintForColumns(
	definition *StructDef,
	columns []string,
) (StructUniqueConstraint, bool, error) {
	requested := make(map[string]struct{}, len(columns))
	for _, column := range columns {
		requested[column] = struct{}{}
	}
	for _, constraint := range definition.UniqueConstraints {
		if len(constraint.Fields) != len(columns) {
			continue
		}
		fields, err := structUniqueConstraintFields(definition, constraint)
		if err != nil {
			return StructUniqueConstraint{}, false, err
		}
		matches := true
		for _, field := range fields {
			if _, found := requested[field.Name]; !found {
				matches = false
				break
			}
		}
		if matches {
			return constraint, true, nil
		}
	}
	return StructUniqueConstraint{}, false, nil
}

func loadKitDBRemoteConflictRow(
	table *SchemaTable,
	reader kitDBReader,
	rowKey []byte,
) (map[string]value.Value, bool, error) {
	encoded, found, err := table.getKitDBRow(reader, rowKey)
	if err != nil || !found {
		return nil, false, err
	}
	decoded, err := decodeKitDBRow(table.definition, encoded)
	if err != nil {
		return nil, false, err
	}
	return decoded.values, true, nil
}

func executeKitDBRemoteUpdate(
	ctx context.Context,
	scope *requestscope.Scope,
	database *dbProxy,
	statement kitSQLStatement,
) (kitDBRemoteResult, error) {
	if statement.predicate == nil && statement.whereExpression == nil {
		return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: UPDATE requires WHERE")
	}
	table, err := kitDBRemoteTable(database, scope, statement.table)
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	if err := ensureKitDBRemoteTableReady(table); err != nil {
		return kitDBRemoteResult{}, err
	}
	if len(statement.returning) != 0 {
		if _, err := kitDBRemoteProjectionColumns(table.definition, statement.returning); err != nil {
			return kitDBRemoteResult{}, err
		}
	}
	filter, err := prepareKitDBRemoteMutationFilter(table, statement)
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	updatePlan, err := prepareKitDBRemoteUpdateExpressionPlan(table, statement)
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	if err := applyKitDBRemoteNarrowing(table, statement); err != nil {
		return kitDBRemoteResult{}, err
	}
	rows, err := table.kitDBUpdateMatching(
		table.builder().ExecutionPlan(),
		kitDBRemoteMutationMatcher(ctx, table, filter, "UPDATE", len(statement.returning) != 0),
		updatePlan.updater(ctx, table),
	)
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	result := kitDBRemoteResult{affected: int64(len(rows))}
	if len(statement.returning) != 0 {
		projected, err := projectKitDBRemoteRows(
			table.definition,
			statement.returning,
			kitDBRemoteMutationResultRows(table, rows),
		)
		if err != nil {
			return kitDBRemoteResult{}, err
		}
		result.columns, result.rows = projected.columns, projected.rows
	}
	return result, nil
}

func executeKitDBRemoteDelete(
	ctx context.Context,
	scope *requestscope.Scope,
	database *dbProxy,
	statement kitSQLStatement,
) (kitDBRemoteResult, error) {
	if statement.predicate == nil && statement.whereExpression == nil {
		return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: DELETE requires WHERE")
	}
	table, err := kitDBRemoteTable(database, scope, statement.table)
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	if err := ensureKitDBRemoteTableReady(table); err != nil {
		return kitDBRemoteResult{}, err
	}
	if len(statement.returning) != 0 {
		if _, err := kitDBRemoteProjectionColumns(table.definition, statement.returning); err != nil {
			return kitDBRemoteResult{}, err
		}
	}
	filter, err := prepareKitDBRemoteMutationFilter(table, statement)
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	if err := applyKitDBRemoteNarrowing(table, statement); err != nil {
		return kitDBRemoteResult{}, err
	}
	rows, err := table.kitDBDeleteMatching(
		table.builder().ExecutionPlan(),
		kitDBRemoteMutationMatcher(ctx, table, filter, "DELETE", len(statement.returning) != 0),
	)
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	result := kitDBRemoteResult{affected: int64(len(rows))}
	if len(statement.returning) != 0 {
		projected, err := projectKitDBRemoteRows(
			table.definition,
			statement.returning,
			kitDBRemoteMutationResultRows(table, rows),
		)
		if err != nil {
			return kitDBRemoteResult{}, err
		}
		result.columns, result.rows = projected.columns, projected.rows
	}
	return result, nil
}

func kitDBRemoteMutationResultRows(table *SchemaTable, rows []kitDBStoredRow) []value.Value {
	result := make([]value.Value, len(rows))
	for index, row := range rows {
		result[index] = coerceResult(table.columns, value.New(cloneKitDBRow(row.values)))
	}
	return result
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

// ensureKitDBRemoteTableReady mirrors SchemaTable.ready while preserving the
// wrapped migration error. Hrana can then distinguish a harmless catalog
// cutover race from a validation failure instead of retrying string messages.
func ensureKitDBRemoteTableReady(table *SchemaTable) error {
	if table == nil {
		return fmt.Errorf("kitdb remote: table is unavailable")
	}
	if table.failed {
		return kitDBRemoteValueError(table.failVal())
	}
	if err := table.ensureTable(); err != nil {
		return fmt.Errorf("db: table %q migration failed: %w", table.table, err)
	}
	return nil
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
		if projection.expression != nil {
			return nil, fmt.Errorf("kitdb SQL: expression projection requires expression planning")
		}
		if projection.aggregate != "" {
			return nil, fmt.Errorf("kitdb SQL: aggregates are valid only in SELECT projections")
		}
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
	if err := database.refreshKitDBCatalogDefinitions(); err != nil {
		return kitDBRemoteResult{}, err
	}
	_, definitions := database.schemaSnapshot()
	virtual := make([]map[string]value.Value, 0, len(definitions))
	names := make([]string, 0, len(definitions))
	for name := range definitions {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		definition := definitions[name]
		virtual = append(virtual, map[string]value.Value{
			"type": value.New("table"), "name": value.New(name), "tbl_name": value.New(name),
			"rootpage": value.New(0), "sql": value.New(kitDBCatalogTableSQL(definition, definitions)),
		})
		for _, constraint := range definition.UniqueConstraints {
			virtual = append(virtual, map[string]value.Value{
				"type": value.New("index"), "name": value.New(constraint.Name), "tbl_name": value.New(name),
				"rootpage": value.New(0), "sql": value.New(kitDBUniqueConstraintSQL(definition, constraint)),
			})
		}
		for _, index := range collectIndexes(name, definition.columns) {
			virtual = append(virtual, map[string]value.Value{
				"type": value.New("index"), "name": value.New(index.name), "tbl_name": value.New(name),
				"rootpage": value.New(0), "sql": value.New(indexSQL(name, index)),
			})
		}
	}
	filtered, err := filterKitDBVirtualRows(virtual, statement.predicate)
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	aggregate, err := kitDBRemoteAggregateMode(statement.projections)
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	if aggregate {
		if len(statement.projections) != 1 || statement.projections[0].aggregate != "count" {
			return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: catalog metadata supports COUNT only")
		}
		projection := statement.projections[0]
		count := 0
		if projection.field == "*" {
			count = len(filtered)
		} else {
			for _, row := range filtered {
				item, found := virtualKitDBValue(row, projection.field)
				if !found {
					return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: metadata has no field %q", projection.field)
				}
				if !item.IsNil() {
					count++
				}
			}
		}
		name := projection.alias
		if name == "" {
			name = "count(" + projection.field + ")"
		}
		return kitDBRemoteResult{
			columns: []kitDBRemoteColumn{{name: name, kind: "integer"}},
			rows:    [][]value.Value{{value.New(count)}},
		}, nil
	}
	fields := []kitDBRemoteColumn{
		{name: "type", kind: "text"}, {name: "name", kind: "text"},
		{name: "tbl_name", kind: "text"}, {name: "rootpage", kind: "integer"},
		{name: "sql", kind: "text"},
	}
	orders := append([]kitSQLOrder(nil), statement.orders...)
	for index := range orders {
		projection, found, err := kitDBRemoteProjectionAlias(statement.projections, orders[index].column)
		if err != nil {
			return kitDBRemoteResult{}, err
		}
		if found {
			orders[index].column = projection.field
		}
	}
	orderKitDBVirtualRows(filtered, orders)
	if statement.distinct {
		projected, err := projectKitDBVirtualRows(fields, statement.projections, filtered)
		if err != nil {
			return kitDBRemoteResult{}, err
		}
		return distinctKitDBRemoteResult(projected, statement)
	}
	filtered = boundKitDBVirtualRows(filtered, statement)
	return projectKitDBVirtualRows(fields, statement.projections, filtered)
}

func kitDBCatalogTableSQL(definition *StructDef, definitions map[string]*StructDef) string {
	parts := make([]string, 0, len(definition.Fields)+len(definition.UniqueConstraints)+len(definition.ForeignConstraints)+len(definition.CheckConstraints))
	for _, field := range definition.Fields {
		spec := *definition.columns[field.Name]
		spec.checks = nil
		parts = append(parts, columnSQL(field.Name, &spec))
	}
	for _, constraint := range definition.UniqueConstraints {
		fields, err := structUniqueConstraintFields(definition, constraint)
		if err != nil {
			continue
		}
		columns := make([]string, len(fields))
		for index, field := range fields {
			columns[index] = fmt.Sprintf("%q", field.Name)
		}
		parts = append(parts, fmt.Sprintf("CONSTRAINT %q UNIQUE (%s)", constraint.Name, strings.Join(columns, ", ")))
	}
	for _, constraint := range definition.CheckConstraints {
		expression, err := renderStructCheckExpression(definition, constraint.Expression)
		if err != nil {
			continue
		}
		parts = append(parts, fmt.Sprintf("CONSTRAINT %q CHECK (%s)", constraint.Name, expression))
	}
	for _, constraint := range definition.ForeignConstraints {
		target := structDefinitionByIdentity(definitions, constraint.TargetStructID, constraint.TargetStruct)
		if target == nil {
			continue
		}
		localFields, targetFields, err := structForeignConstraintFields(definition, target, constraint)
		if err != nil {
			continue
		}
		local := make([]string, len(localFields))
		referenced := make([]string, len(targetFields))
		for index := range localFields {
			local[index] = fmt.Sprintf("%q", localFields[index].Name)
			referenced[index] = fmt.Sprintf("%q", targetFields[index].Name)
		}
		foreign := fmt.Sprintf(
			"CONSTRAINT %q FOREIGN KEY (%s) REFERENCES %q (%s)",
			constraint.Name, strings.Join(local, ", "), target.Name, strings.Join(referenced, ", "),
		)
		if strings.TrimSpace(constraint.OnDelete) != "" {
			foreign += " ON DELETE " + fkAction(constraint.OnDelete)
		}
		if strings.TrimSpace(constraint.OnUpdate) != "" {
			foreign += " ON UPDATE " + fkAction(constraint.OnUpdate)
		}
		parts = append(parts, foreign)
	}
	return fmt.Sprintf("CREATE TABLE IF NOT EXISTS %q (%s)", definition.Name, strings.Join(parts, ", "))
}

func kitDBUniqueConstraintSQL(definition *StructDef, constraint StructUniqueConstraint) string {
	fields, err := structUniqueConstraintFields(definition, constraint)
	if err != nil {
		return ""
	}
	columns := make([]string, len(fields))
	for index, field := range fields {
		columns[index] = fmt.Sprintf("%q", field.Name)
	}
	return fmt.Sprintf(
		"CREATE UNIQUE INDEX IF NOT EXISTS %q ON %q (%s)",
		constraint.Name, definition.Name, strings.Join(columns, ", "),
	)
}

func executeKitDBRemotePragma(
	ctx context.Context,
	scope *requestscope.Scope,
	database *dbProxy,
	statement kitSQLStatement,
) (kitDBRemoteResult, error) {
	if statement.pragma == "index_info" {
		return executeKitDBRemoteIndexInfo(database, statement.table)
	}
	if statement.pragma == "statistics" {
		return executeKitDBRemoteStatistics(scope, database, statement.table)
	}
	if statement.pragma == "import_status" {
		return executeKitDBRemoteImportStatus(ctx, scope, database, statement.table)
	}
	_, definition, err := kitDBRemoteDefinition(database, statement.table)
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	if statement.pragma == "migration_status" {
		return executeKitDBRemoteMigrationStatus(ctx, scope, database, definition)
	}
	if statement.pragma == "index_status" {
		return executeKitDBRemoteIndexStatus(ctx, scope, database, definition)
	}
	_, definitions := database.schemaSnapshot()
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
			primaryOrder := field.PrimaryOrder
			if field.Primary && primaryOrder == 0 {
				primaryOrder = 1
			}
			rows = append(rows, []value.Value{
				value.New(field.Position), value.New(field.Name), value.New(storageClass(field.Kind)),
				value.New(boolInt(field.NotNull)), defaultValue, value.New(primaryOrder),
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
		for _, constraint := range definition.UniqueConstraints {
			rows = append(rows, []value.Value{
				value.New(sequence), value.New(constraint.Name), value.New(1), value.New("u"), value.New(0),
			})
			sequence++
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
		referenceID := 0
		for _, field := range definition.Fields {
			if field.Reference == nil {
				continue
			}
			targetName := field.Reference.Field
			if target := definitions[field.Reference.Struct]; target != nil {
				if targetField, found := kitDBField(target, targetName); found {
					targetName = targetField.Name
				}
			}
			rows = append(rows, []value.Value{
				value.New(referenceID), value.New(0), value.New(field.Reference.Struct), value.New(field.Name),
				value.New(targetName), value.New(remoteFKAction(field.Reference.OnUpdate)),
				value.New(remoteFKAction(field.Reference.OnDelete)), value.New("NONE"),
			})
			referenceID++
		}
		for _, constraint := range definition.ForeignConstraints {
			target := structDefinitionByIdentity(definitions, constraint.TargetStructID, constraint.TargetStruct)
			if target == nil {
				return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: foreign key %q target is unavailable", constraint.Name)
			}
			localFields, targetFields, err := structForeignConstraintFields(definition, target, constraint)
			if err != nil {
				return kitDBRemoteResult{}, err
			}
			for sequence := range localFields {
				rows = append(rows, []value.Value{
					value.New(referenceID), value.New(sequence), value.New(target.Name), value.New(localFields[sequence].Name),
					value.New(targetFields[sequence].Name), value.New(remoteFKAction(constraint.OnUpdate)),
					value.New(remoteFKAction(constraint.OnDelete)), value.New("NONE"),
				})
			}
			referenceID++
		}
		return kitDBRemoteResult{columns: columns, rows: rows}, nil
	default:
		return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: unsupported PRAGMA %q", statement.pragma)
	}
}

func executeKitDBRemoteImportStatus(
	ctx context.Context,
	scope *requestscope.Scope,
	database *dbProxy,
	id string,
) (kitDBRemoteResult, error) {
	columns := kitDBRemoteImportStatusColumns()
	if err := validateKitDBImportLabel("id", id, kitDBImportIDLimit); err != nil {
		return kitDBRemoteResult{}, err
	}
	managed, err := kitDBForRequest(database.tenant, database.dbName, scope).database()
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	defer managed.Release()
	managed.writeMu.RLock()
	defer managed.writeMu.RUnlock()
	if err := ctx.Err(); err != nil {
		return kitDBRemoteResult{}, err
	}
	state, found, err := loadKitDBImportState(managed.database, id)
	if err != nil || !found {
		return kitDBRemoteResult{columns: columns, rows: [][]value.Value{}}, err
	}
	row, err := kitDBRemoteImportStatusRow(state)
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	return kitDBRemoteResult{columns: columns, rows: [][]value.Value{row}}, nil
}

func executeKitDBRemoteCancelImport(
	ctx context.Context,
	scope *requestscope.Scope,
	database *dbProxy,
	id string,
) (kitDBRemoteResult, error) {
	if err := validateKitDBImportLabel("id", id, kitDBImportIDLimit); err != nil {
		return kitDBRemoteResult{}, err
	}
	_, transaction, err := database.beginKitDBRecordTransaction(scope)
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	defer transaction.Rollback()
	state, found, err := loadKitDBImportState(transaction, id)
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	if !found {
		return kitDBRemoteResult{}, fmt.Errorf("kitdb import %q does not exist", id)
	}
	if state.Complete {
		return kitDBRemoteResult{}, fmt.Errorf("kitdb import %q is already complete", id)
	}
	if !state.Cancelled {
		if transaction.base == math.MaxUint64 {
			return kitDBRemoteResult{}, fmt.Errorf("kitdb import transaction watermark overflow")
		}
		state.Cancelled = true
		state.Transaction = transaction.base + 1
		if err := putKitDBImportState(transaction, &state); err != nil {
			return kitDBRemoteResult{}, err
		}
		committed, err := transaction.CommitContext(ctx)
		if err != nil {
			return kitDBRemoteResult{}, err
		}
		if committed != state.Transaction {
			return kitDBRemoteResult{}, fmt.Errorf(
				"kitdb import %q committed at transaction %d, expected %d",
				id, committed, state.Transaction,
			)
		}
	}
	row, err := kitDBRemoteImportStatusRow(state)
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	return kitDBRemoteResult{
		columns: kitDBRemoteImportStatusColumns(), rows: [][]value.Value{row}, affected: 1,
	}, nil
}

func executeKitDBRemoteForgetImport(
	ctx context.Context,
	scope *requestscope.Scope,
	database *dbProxy,
	id string,
) (kitDBRemoteResult, error) {
	columns := []kitDBRemoteColumn{{name: "forgotten", kind: "integer"}}
	if err := validateKitDBImportLabel("id", id, kitDBImportIDLimit); err != nil {
		return kitDBRemoteResult{}, err
	}
	_, transaction, err := database.beginKitDBRecordTransaction(scope)
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	defer transaction.Rollback()
	state, found, err := loadKitDBImportState(transaction, id)
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	if !found {
		return kitDBRemoteResult{columns: columns, rows: [][]value.Value{{value.New(0)}}}, nil
	}
	if !state.Complete && !state.Cancelled {
		return kitDBRemoteResult{}, fmt.Errorf(
			"kitdb import %q is active; cancel or complete it before forgetting its checkpoint", id,
		)
	}
	key, err := kitDBImportStateKey(id)
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	if err := transaction.Delete(key); err != nil {
		return kitDBRemoteResult{}, err
	}
	if _, err := transaction.CommitContext(ctx); err != nil {
		return kitDBRemoteResult{}, err
	}
	return kitDBRemoteResult{
		columns: columns, rows: [][]value.Value{{value.New(1)}}, affected: 1,
	}, nil
}

func kitDBRemoteImportStatusColumns() []kitDBRemoteColumn {
	return []kitDBRemoteColumn{
		{name: "import_id", kind: "text"},
		{name: "source", kind: "text"},
		{name: "source_format", kind: "text"},
		{name: "table_name", kind: "text"},
		{name: "columns", kind: "text"},
		{name: "seed_checksum", kind: "text"},
		{name: "checksum", kind: "text"},
		{name: "chunk", kind: "integer"},
		{name: "source_rows", kind: "integer"},
		{name: "source_offset", kind: "integer"},
		{name: "complete", kind: "integer"},
		{name: "cancelled", kind: "integer"},
		{name: "transaction", kind: "integer"},
	}
}

func kitDBRemoteImportStatusRow(state kitDBImportState) ([]value.Value, error) {
	encodedColumns, err := json.Marshal(state.Columns)
	if err != nil {
		return nil, err
	}
	return []value.Value{
		value.New(state.ID),
		value.New(state.Source),
		value.New(state.SourceFormat),
		value.New(state.Table),
		value.New(string(encodedColumns)),
		value.New(state.Seed),
		value.New(state.Checksum),
		value.New(state.Chunk),
		value.New(state.Rows),
		value.New(state.Offset),
		value.New(boolInt(state.Complete)),
		value.New(boolInt(state.Cancelled)),
		value.New(state.Transaction),
	}, nil
}

func executeKitDBRemoteMigrationStatus(
	ctx context.Context,
	scope *requestscope.Scope,
	database *dbProxy,
	definition *StructDef,
) (kitDBRemoteResult, error) {
	columns := []kitDBRemoteColumn{
		{name: "phase", kind: "text"},
		{name: "processed_rows", kind: "integer"},
		{name: "cleaned_rows", kind: "integer"},
		{name: "admission_rows", kind: "integer"},
		{name: "source_hash", kind: "text"},
		{name: "target_hash", kind: "text"},
		{name: "source_generation", kind: "integer"},
		{name: "target_generation", kind: "integer"},
		{name: "started_transaction", kind: "integer"},
		{name: "started_at", kind: "text"},
		{name: "can_cancel", kind: "integer"},
		{name: "target_published", kind: "integer"},
		{name: "mode", kind: "text"},
		{name: "writes_paused", kind: "integer"},
		{name: "cleanup_prefix", kind: "integer"},
		{name: "cleanup_total", kind: "integer"},
		{name: "verified_rows", kind: "integer"},
	}
	managed, err := kitDBForRequest(database.tenant, database.dbName, scope).database()
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	defer managed.Release()
	managed.writeMu.RLock()
	defer managed.writeMu.RUnlock()
	if err := ctx.Err(); err != nil {
		return kitDBRemoteResult{}, err
	}
	state, found, err := loadKitDBRowMigrationState(managed.database, definition)
	if err != nil || !found {
		return kitDBRemoteResult{columns: columns, rows: [][]value.Value{}}, err
	}
	phase := state.Phase
	canCancel := 0
	targetPublished := 0
	mode := "row"
	writesPaused := 0
	cleanupTotal := 1
	if state.PrimaryKeyChange {
		mode = "rekey"
		writesPaused = 1
		cleanupTotal += len(state.RetiredIndexes)
		if state.Phase == kitDBRowMigrationPhaseCancel {
			cleanupTotal = len(state.TargetIndexes) + 1
		}
	}
	switch state.Phase {
	case "":
		phase = "legacy"
	case kitDBRowMigrationPhaseRows:
		phase = "building"
		canCancel = 1
	case kitDBRowMigrationPhaseVerify:
		phase = "validating"
		canCancel = 1
	case kitDBRowMigrationPhaseCancel:
		phase = "cancelling"
		writesPaused = 0
	case kitDBRowMigrationPhaseCleanup:
		phase = "cleanup"
		targetPublished = 1
		writesPaused = 0
	}
	row := []value.Value{
		value.New(phase),
		value.New(state.Rows),
		value.New(state.CleanupRows),
		value.New(state.TotalRows),
		value.New(state.SourceHash),
		value.New(state.TargetHash),
		value.New(state.SourceGeneration),
		value.New(state.TargetGeneration),
		value.New(state.StartedAt),
		value.New(time.Unix(0, state.MigrationUnixNano).UTC().Format(time.RFC3339Nano)),
		value.New(canCancel),
		value.New(targetPublished),
		value.New(mode),
		value.New(writesPaused),
		value.New(state.CleanupIndex),
		value.New(cleanupTotal),
		value.New(state.VerifiedRows),
	}
	return kitDBRemoteResult{columns: columns, rows: [][]value.Value{row}}, nil
}

func executeKitDBRemoteIndexStatus(
	ctx context.Context,
	scope *requestscope.Scope,
	database *dbProxy,
	definition *StructDef,
) (kitDBRemoteResult, error) {
	columns := []kitDBRemoteColumn{
		{name: "mode", kind: "text"},
		{name: "phase", kind: "text"},
		{name: "processed_rows", kind: "integer"},
		{name: "index_count", kind: "integer"},
		{name: "has_cursor", kind: "integer"},
		{name: "cleanup_index", kind: "integer"},
		{name: "cleanup_total", kind: "integer"},
		{name: "started_transaction", kind: "integer"},
		{name: "source_hash", kind: "text"},
		{name: "target_hash", kind: "text"},
		{name: "target_published", kind: "integer"},
	}
	managed, err := kitDBForRequest(database.tenant, database.dbName, scope).database()
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	defer managed.Release()
	managed.writeMu.RLock()
	defer managed.writeMu.RUnlock()
	if err := ctx.Err(); err != nil {
		return kitDBRemoteResult{}, err
	}
	statuses, err := loadKitDBIndexBuildStatuses(managed.database, definition)
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	rows := make([][]value.Value, 0, len(statuses))
	for _, status := range statuses {
		rows = append(rows, []value.Value{
			value.New(status.Mode),
			value.New(status.Phase),
			value.New(status.ProcessedRows),
			value.New(status.IndexCount),
			value.New(boolInt(status.HasCursor)),
			value.New(status.CleanupIndex),
			value.New(status.CleanupTotal),
			value.New(status.StartedTransaction),
			value.New(status.SourceHash),
			value.New(status.TargetHash),
			value.New(boolInt(status.TargetPublished)),
		})
	}
	return kitDBRemoteResult{columns: columns, rows: rows}, nil
}

func executeKitDBRemoteIndexInfo(database *dbProxy, requested string) (kitDBRemoteResult, error) {
	columns := []kitDBRemoteColumn{
		{name: "seqno", kind: "integer"}, {name: "cid", kind: "integer"}, {name: "name", kind: "text"},
	}
	if err := database.refreshKitDBCatalogDefinitions(); err != nil {
		return kitDBRemoteResult{}, err
	}
	_, definitions := database.schemaSnapshot()
	names := make([]string, 0, len(definitions))
	for name := range definitions {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		definition := definitions[name]
		fields, found, err := kitDBDefinitionIndexFields(definition, requested)
		if err != nil {
			return kitDBRemoteResult{}, err
		}
		if !found {
			continue
		}
		rows := make([][]value.Value, len(fields))
		for index, field := range fields {
			rows[index] = []value.Value{value.New(index), value.New(field.Position), value.New(field.Name)}
		}
		return kitDBRemoteResult{columns: columns, rows: rows}, nil
	}
	return kitDBRemoteResult{columns: columns, rows: [][]value.Value{}}, nil
}

func kitDBDefinitionIndexFields(
	definition *StructDef,
	requested string,
) ([]StructFieldDef, bool, error) {
	for _, field := range definition.Fields {
		if field.Unique && !field.Primary &&
			strings.EqualFold("unique_"+definition.Name+"_"+field.Name, requested) {
			return []StructFieldDef{field}, true, nil
		}
	}
	for _, constraint := range definition.UniqueConstraints {
		if strings.EqualFold(constraint.Name, requested) {
			fields, err := structUniqueConstraintFields(definition, constraint)
			return fields, err == nil, err
		}
	}
	for _, index := range collectIndexes(definition.Name, definition.columns) {
		if !strings.EqualFold(index.name, requested) {
			continue
		}
		fields := make([]StructFieldDef, len(index.columns))
		for position, column := range index.columns {
			field, found := kitDBField(definition, column)
			if !found {
				return nil, false, fmt.Errorf("kitdb SQL: index %q references missing field %q", index.name, column)
			}
			fields[position] = field
		}
		return fields, true, nil
	}
	return nil, false, nil
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

func filterKitDBVirtualRows(rows []map[string]value.Value, predicate *query.Predicate) ([]map[string]value.Value, error) {
	filtered := make([]map[string]value.Value, 0, len(rows))
	for _, row := range rows {
		truth, err := kitDBEvaluatePredicate(predicate, func(column string) (value.Value, string, error) {
			left, found := virtualKitDBValue(row, column)
			if !found {
				return value.Value{}, "", fmt.Errorf("kitdb SQL: metadata has no field %q", column)
			}
			return left, "", nil
		})
		if err != nil {
			return nil, err
		}
		if truth == kitDBTrue {
			filtered = append(filtered, row)
		}
	}
	return filtered, nil
}

func virtualKitDBValue(row map[string]value.Value, requested string) (value.Value, bool) {
	_, requested = kitSQLReferenceParts(requested)
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

func distinctKitDBRemoteResult(
	result kitDBRemoteResult,
	statement kitSQLStatement,
) (kitDBRemoteResult, error) {
	seen := make(map[string]struct{}, len(result.rows))
	rows := make([][]value.Value, 0, len(result.rows))
	for _, row := range result.rows {
		key := make([]byte, 0, len(row)*12)
		for _, item := range row {
			component, err := kitDBScalarComponent(item)
			if err != nil {
				return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: DISTINCT: %w", err)
			}
			key = append(key, component...)
		}
		encoded := string(key)
		if _, duplicate := seen[encoded]; duplicate {
			continue
		}
		if err := ensureKitDBRemoteGroupCapacity(len(seen)); err != nil {
			return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: DISTINCT exceeds %d intermediate rows", kitDBRemoteGroupLimit)
		}
		seen[encoded] = struct{}{}
		rows = append(rows, row)
	}
	offset := statement.offset
	if offset > len(rows) {
		offset = len(rows)
	}
	rows = rows[offset:]
	limit := query.DefaultDBLimit
	if statement.hasLimit {
		limit = statement.limit
	}
	if limit > kitDBRemoteSelectRowLimit {
		limit = kitDBRemoteSelectRowLimit
	}
	if len(rows) > limit {
		rows = rows[:limit]
	}
	result.rows = rows
	return result, nil
}

func boundKitDBVirtualRows(rows []map[string]value.Value, statement kitSQLStatement) []map[string]value.Value {
	offset := statement.offset
	if offset > len(rows) {
		offset = len(rows)
	}
	rows = rows[offset:]
	limit := kitDBRemoteSelectRowLimit
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
			if projection.all || projection.aggregate != "" {
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
			return hranaErrCode(err.Error(), kitDBErrorCode(err))
		}
		return hranaOK(map[string]any{"type": "execute", "result": result})
	case "batch":
		if request.Batch == nil {
			return hranaErr("batch: missing batch")
		}
		stepResults, stepErrors := runKitDBHranaBatch(
			ctx, scope, database, request.Batch, store, readonly,
		)
		return hranaOK(map[string]any{
			"type":   "batch",
			"result": map[string]any{"step_results": stepResults, "step_errors": stepErrors},
		})
	default:
		return hranaErr("unsupported request type in KitDB profile: " + request.Type)
	}
}

func runKitDBHranaBatch(
	ctx context.Context,
	scope *requestscope.Scope,
	database *dbProxy,
	batch *hranaBatch,
	store map[int32]string,
	readonly bool,
) ([]any, []any) {
	count := len(batch.Steps)
	stepResults := make([]any, count)
	stepErrors := make([]any, count)
	if count > kitDBRemoteTransactionStatementLimit {
		stepErrors[0] = map[string]any{"message": fmt.Sprintf(
			"kitdb SQL: batch exceeds %d statements",
			kitDBRemoteTransactionStatementLimit,
		)}
		return stepResults, stepErrors
	}

	controls := make([]string, count)
	hasControl := false
	for index, step := range batch.Steps {
		source, err := stmtSQL(step.Stmt, store)
		if err != nil {
			stepErrors[index] = kitDBRemoteErrorPayload(err)
			return stepResults, stepErrors
		}
		controls[index] = kitDBTransactionControl(source)
		hasControl = hasControl || controls[index] != ""
	}
	if !hasControl {
		for index, step := range batch.Steps {
			result, err := runKitDBHranaStmt(ctx, scope, database, step.Stmt, store, readonly)
			if err != nil {
				stepErrors[index] = kitDBRemoteErrorPayload(err)
				break
			}
			stepResults[index] = result
		}
		return stepResults, stepErrors
	}

	if count < 2 || controls[0] != "begin" || (controls[count-1] != "commit" && controls[count-1] != "rollback") {
		stepErrors[0] = map[string]any{"message": "kitdb SQL: transaction controls must be one BEGIN ... COMMIT or BEGIN ... ROLLBACK batch"}
		return stepResults, stepErrors
	}
	for index := 1; index < count-1; index++ {
		if controls[index] != "" {
			stepErrors[index] = map[string]any{"message": "kitdb SQL: nested transaction controls are not supported"}
			return stepResults, stepErrors
		}
	}

	transactionProxy, transaction, err := database.beginKitDBRecordTransaction(scope)
	if err != nil {
		stepErrors[0] = kitDBRemoteErrorPayload(err)
		return stepResults, stepErrors
	}
	stepResults[0] = kitDBRemoteHranaResult(kitDBRemoteResult{}, batch.Steps[0].Stmt.WantRows, 0)
	for index := 1; index < count-1; index++ {
		result, err := runKitDBHranaStmt(ctx, scope, transactionProxy, batch.Steps[index].Stmt, store, readonly)
		if err != nil {
			_ = transaction.Rollback()
			stepErrors[index] = kitDBRemoteErrorPayload(err)
			return stepResults, stepErrors
		}
		stepResults[index] = result
	}
	if controls[count-1] == "rollback" {
		_ = transaction.Rollback()
		stepResults[count-1] = kitDBRemoteHranaResult(kitDBRemoteResult{}, batch.Steps[count-1].Stmt.WantRows, 0)
		return stepResults, stepErrors
	}
	if _, err := transaction.Commit(); err != nil {
		stepErrors[count-1] = kitDBRemoteErrorPayload(err)
		return stepResults, stepErrors
	}
	stepResults[count-1] = kitDBRemoteHranaResult(kitDBRemoteResult{}, batch.Steps[count-1].Stmt.WantRows, 0)
	return stepResults, stepErrors
}

func kitDBRemoteErrorPayload(err error) map[string]any {
	payload := map[string]any{"message": err.Error()}
	if code := kitDBErrorCode(err); code != "" {
		payload["code"] = code
	}
	return payload
}

func kitDBTransactionControl(source string) string {
	normalized := strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(source)), " "))
	switch normalized {
	case "begin", "begin transaction", "begin deferred", "begin deferred transaction", "begin immediate", "begin immediate transaction":
		return "begin"
	case "commit", "end", "end transaction":
		return "commit"
	case "rollback", "rollback transaction":
		return "rollback"
	default:
		return ""
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
