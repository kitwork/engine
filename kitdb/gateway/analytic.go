package gateway

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/kitwork/engine/kitdb/analytics"
	"github.com/kitwork/engine/kitdb/pgwire"
)

// A read statement the column copy answers (KITWORK_KITDB.md mục 6 and 7, kitdb/analytics): one table, its rows
// filtered, grouped and aggregated, where SQLite would scan the whole table. The statement runs as any other up to its
// last step: its read transaction, the grants checked on its program, its columns described by SQLite; then, when its
// shape is one the copy knows, SQLite's plan scans the table and the copy holds its columns, the copy answers it in
// the same read transaction, and its values go through the same rendering as SQLite's, so a client sees the same
// columns, types and values. Anything else, and anything the copy refuses, runs on SQLite. A table SQLite scans gets
// a copy of the columns its statements ask for, built in the background (analytics.Registry).

// analyticAnswers counts the statements the copy answered (tests read it).
var analyticAnswers atomic.Int64

// analyticBudget is how long SQLite has to answer a statement its plan searches through an index for, before the copy
// answers it: a search of a few rows takes SQLite well under a millisecond, while a search that reads most of the
// table (a skip-scan over the second column of a key) takes it seconds. Zero gives SQLite no try.
var analyticBudget = 100 * time.Millisecond

// Plans of a statement, as EXPLAIN QUERY PLAN tells them.
const (
	planOther  = iota // not one table read once: SQLite's
	planScan          // the table read whole, in rowid order or an index's: the copy's
	planSearch        // the table searched through an index: SQLite's while it is quick
)

// analyticItem is one item of the select list: a grouped column (group its position in the query's Group) or an
// aggregate (aggregate its position in the query's Aggregates), and the name a client may order by.
type analyticItem struct {
	group, aggregate int
	name             string // the alias, or the column's name, in lower case
	function, column string // an aggregate's, to find it again in ORDER BY
}

type analyticOrder struct {
	item       int
	descending bool
}

// analyticShape is a statement the copy may answer.
type analyticShape struct {
	schema, table, alias string
	items                []analyticItem
	query                analytics.Query
	order                []analyticOrder
	limit, offset        int64
	hasLimit             bool
	columns              map[string]bool // every column named, in lower case, to be read from the table's
}

// analyticCursor reads a statement's tokens.
type analyticCursor struct {
	tokens    []token
	at        int
	arguments []any
	next      int // the parameter a bare ? stands for
}

func (c *analyticCursor) done() bool { return c.at >= len(c.tokens) }

func (c *analyticCursor) peek() token {
	if c.done() {
		return token{}
	}
	return c.tokens[c.at]
}

func (c *analyticCursor) word() string { return c.peek().word() }

func (c *analyticCursor) take(word string) bool {
	if c.word() == word {
		c.at++
		return true
	}
	return false
}

func (c *analyticCursor) punctuation(text string) bool {
	if c.done() || c.peek().kind != tokenPunctuation || c.peek().text != text {
		return false
	}
	c.at++
	return true
}

// keywords the shape reader stops an alias or a column at.
var analyticKeywords = map[string]bool{
	"FROM": true, "WHERE": true, "GROUP": true, "ORDER": true, "LIMIT": true, "OFFSET": true, "AND": true, "OR": true,
	"NOT": true, "IN": true, "IS": true, "NULL": true, "BETWEEN": true, "AS": true, "BY": true, "ASC": true, "DESC": true,
	"HAVING": true, "JOIN": true, "ON": true, "UNION": true, "DISTINCT": true, "ALL": true, "LIKE": true, "SELECT": true,
}

// name reads an identifier.
func (c *analyticCursor) name() (string, bool) {
	current := c.peek()
	if !isIdentifier(current) || (current.kind == tokenWord && analyticKeywords[current.word()]) {
		return "", false
	}
	c.at++
	return unquoted(current), true
}

// column reads a column, maybe qualified by the table or its alias, checked once FROM is read.
func (c *analyticCursor) column(qualifiers *[]string) (string, bool) {
	first, ok := c.name()
	if !ok {
		return "", false
	}
	if c.punctuation(".") {
		second, ok := c.name()
		if !ok {
			return "", false
		}
		*qualifiers = append(*qualifiers, strings.ToLower(first))
		return strings.ToLower(second), true
	}
	return strings.ToLower(first), true
}

// value reads a literal or a parameter: an integer, a real, a text, TRUE or FALSE (1 and 0 in SQLite).
func (c *analyticCursor) value() (any, bool) {
	negative := c.punctuation("-")
	current := c.peek()
	switch current.kind {
	case tokenNumber:
		c.at++
		text := current.text
		if negative {
			text = "-" + text
		}
		if integer, err := strconv.ParseInt(text, 10, 64); err == nil {
			return integer, true
		}
		if strings.ContainsAny(text, "xX_") {
			return nil, false
		}
		real, err := strconv.ParseFloat(text, 64)
		return real, err == nil
	case tokenString:
		if negative || !strings.HasPrefix(current.text, "'") {
			return nil, false // a blob x'…'
		}
		c.at++
		return strings.ReplaceAll(current.text[1:len(current.text)-1], "''", "'"), true
	case tokenVariable:
		if negative {
			return nil, false
		}
		c.at++
		index := c.next
		if current.text != "?" {
			if !strings.HasPrefix(current.text, "?") {
				return nil, false
			}
			number, err := strconv.Atoi(current.text[1:])
			if err != nil {
				return nil, false
			}
			index = number - 1
		}
		c.next = index + 1
		if index < 0 || index >= len(c.arguments) {
			return nil, false
		}
		switch argument := c.arguments[index].(type) {
		case int64, float64, string:
			return argument, true
		}
		return nil, false
	case tokenWord:
		if negative {
			return nil, false
		}
		switch current.word() {
		case "TRUE":
			c.at++
			return int64(1), true
		case "FALSE":
			c.at++
			return int64(0), true
		}
	}
	return nil, false
}

// condition reads a WHERE: OR of ANDs of NOT or comparisons, in SQLite's precedence.
func (c *analyticCursor) condition(qualifiers *[]string, columns map[string]bool) (*analytics.Condition, bool) {
	left, ok := c.conjunction(qualifiers, columns)
	if !ok {
		return nil, false
	}
	for c.take("OR") {
		right, ok := c.conjunction(qualifiers, columns)
		if !ok {
			return nil, false
		}
		left = &analytics.Condition{Op: analytics.OpOr, Args: []*analytics.Condition{left, right}}
	}
	return left, true
}

func (c *analyticCursor) conjunction(qualifiers *[]string, columns map[string]bool) (*analytics.Condition, bool) {
	left, ok := c.negation(qualifiers, columns)
	if !ok {
		return nil, false
	}
	for c.take("AND") {
		right, ok := c.negation(qualifiers, columns)
		if !ok {
			return nil, false
		}
		left = &analytics.Condition{Op: analytics.OpAnd, Args: []*analytics.Condition{left, right}}
	}
	return left, true
}

func (c *analyticCursor) negation(qualifiers *[]string, columns map[string]bool) (*analytics.Condition, bool) {
	if c.take("NOT") {
		inner, ok := c.negation(qualifiers, columns)
		if !ok {
			return nil, false
		}
		return &analytics.Condition{Op: analytics.OpNot, Args: []*analytics.Condition{inner}}, true
	}
	if c.punctuation("(") {
		inner, ok := c.condition(qualifiers, columns)
		if !ok || !c.punctuation(")") {
			return nil, false
		}
		return inner, true
	}
	column, ok := c.column(qualifiers)
	if !ok {
		return nil, false
	}
	columns[column] = true
	not := func(condition *analytics.Condition, negated bool) *analytics.Condition {
		if negated {
			return &analytics.Condition{Op: analytics.OpNot, Args: []*analytics.Condition{condition}}
		}
		return condition
	}
	if c.take("IS") {
		negated := c.take("NOT")
		if !c.take("NULL") {
			return nil, false
		}
		if negated {
			return &analytics.Condition{Op: analytics.OpNotNull, Column: column}, true
		}
		return &analytics.Condition{Op: analytics.OpNull, Column: column}, true
	}
	negated := c.take("NOT")
	if c.take("IN") {
		if !c.punctuation("(") {
			return nil, false
		}
		var values []any
		for {
			value, ok := c.value()
			if !ok {
				return nil, false
			}
			values = append(values, value)
			if c.punctuation(")") {
				break
			}
			if !c.punctuation(",") {
				return nil, false
			}
		}
		return not(&analytics.Condition{Op: analytics.OpIn, Column: column, Values: values}, negated), true
	}
	if c.take("BETWEEN") {
		low, ok := c.value()
		if !ok || !c.take("AND") {
			return nil, false
		}
		high, ok := c.value()
		if !ok {
			return nil, false
		}
		return not(&analytics.Condition{Op: analytics.OpAnd, Args: []*analytics.Condition{
			{Op: analytics.OpAtLeast, Column: column, Value: low}, {Op: analytics.OpAtMost, Column: column, Value: high},
		}}, negated), true
	}
	if negated {
		return nil, false
	}
	var op string
	switch {
	case c.punctuation("="):
		c.punctuation("=") // ==
		op = analytics.OpEqual
	case c.punctuation("!"):
		if !c.punctuation("=") {
			return nil, false
		}
		op = analytics.OpUnequal
	case c.punctuation("<"):
		switch {
		case c.punctuation("="):
			op = analytics.OpAtMost
		case c.punctuation(">"):
			op = analytics.OpUnequal
		default:
			op = analytics.OpLess
		}
	case c.punctuation(">"):
		op = analytics.OpMore
		if c.punctuation("=") {
			op = analytics.OpAtLeast
		}
	default:
		return nil, false
	}
	value, ok := c.value()
	if !ok {
		return nil, false
	}
	return &analytics.Condition{Op: op, Column: column, Value: value}, true
}

var analyticFunctions = map[string]bool{"COUNT": true, "SUM": true, "AVG": true, "MIN": true, "MAX": true}

// parseAnalytic reads a statement of the shape the copy answers; false for any other.
func parseAnalytic(parsed statement, arguments []any) (*analyticShape, bool) {
	tokens := parsed.tokens
	for len(tokens) > 0 && tokens[len(tokens)-1].kind == tokenSemicolon {
		tokens = tokens[:len(tokens)-1]
	}
	c := &analyticCursor{tokens: tokens, arguments: arguments}
	if !c.take("SELECT") || c.word() == "DISTINCT" || c.word() == "ALL" {
		return nil, false
	}
	shape := &analyticShape{columns: map[string]bool{}}
	var qualifiers []string
	type pendingItem struct {
		function, column string // function "" for a column
		name             string
	}
	var pending []pendingItem
	for {
		var item pendingItem
		if analyticFunctions[c.word()] && c.at+1 < len(c.tokens) && c.tokens[c.at+1].text == "(" {
			item.function = strings.ToLower(c.word())
			c.at += 2
			if c.word() == "DISTINCT" {
				return nil, false
			}
			if c.punctuation("*") {
				if item.function != "count" {
					return nil, false
				}
			} else {
				column, ok := c.column(&qualifiers)
				if !ok {
					return nil, false
				}
				item.column = column
				shape.columns[column] = true
			}
			if !c.punctuation(")") {
				return nil, false
			}
		} else {
			column, ok := c.column(&qualifiers)
			if !ok {
				return nil, false
			}
			item.column, item.name = column, column
			shape.columns[column] = true
		}
		if c.take("AS") {
			alias, ok := c.name()
			if !ok {
				return nil, false
			}
			item.name = strings.ToLower(alias)
		} else if alias, ok := c.name(); ok {
			item.name = strings.ToLower(alias)
		}
		pending = append(pending, item)
		if !c.punctuation(",") {
			break
		}
	}
	if !c.take("FROM") {
		return nil, false
	}
	first, ok := c.name()
	if !ok {
		return nil, false
	}
	shape.table = first
	if c.punctuation(".") {
		second, ok := c.name()
		if !ok {
			return nil, false
		}
		shape.schema, shape.table = first, second
	}
	if c.take("AS") {
		alias, ok := c.name()
		if !ok {
			return nil, false
		}
		shape.alias = alias
	} else if alias, ok := c.name(); ok {
		shape.alias = alias
	}
	if c.take("WHERE") {
		filter, ok := c.condition(&qualifiers, shape.columns)
		if !ok {
			return nil, false
		}
		shape.query.Filter = filter
	}
	groupOf := map[string]int{}
	if c.take("GROUP") {
		if !c.take("BY") {
			return nil, false
		}
		for {
			column, ok := c.column(&qualifiers)
			if !ok {
				return nil, false
			}
			if _, twice := groupOf[column]; !twice {
				groupOf[column] = len(shape.query.Group)
				shape.query.Group = append(shape.query.Group, column)
				shape.columns[column] = true
			}
			if !c.punctuation(",") {
				break
			}
		}
	}
	for _, item := range pending {
		current := analyticItem{group: -1, aggregate: -1, name: item.name, function: item.function, column: item.column}
		if item.function == "" {
			position, grouped := groupOf[item.column]
			if !grouped {
				return nil, false // a bare column beside aggregates: SQLite takes it from some row
			}
			current.group = position
		} else {
			current.aggregate = len(shape.query.Aggregates)
			shape.query.Aggregates = append(shape.query.Aggregates, analytics.Aggregate{Function: item.function, Column: item.column})
		}
		shape.items = append(shape.items, current)
	}
	if len(shape.query.Aggregates) == 0 {
		return nil, false // a list of rows, not an aggregate: SQLite's
	}
	if c.take("ORDER") {
		if !c.take("BY") {
			return nil, false
		}
		for {
			item := -1
			if current := c.peek(); current.kind == tokenNumber {
				position, err := strconv.Atoi(current.text)
				if err != nil || position < 1 || position > len(shape.items) {
					return nil, false
				}
				item = position - 1
				c.at++
			} else if analyticFunctions[c.word()] {
				function := strings.ToLower(c.word())
				c.at++
				if !c.punctuation("(") {
					return nil, false
				}
				column := ""
				if !c.punctuation("*") {
					var ok bool
					if column, ok = c.column(&qualifiers); !ok {
						return nil, false
					}
				}
				if !c.punctuation(")") {
					return nil, false
				}
				for position, candidate := range shape.items {
					if candidate.function == function && candidate.column == column {
						item = position
						break
					}
				}
			} else {
				column, ok := c.column(&qualifiers)
				if !ok {
					return nil, false
				}
				for position, candidate := range shape.items {
					if candidate.name == column {
						item = position
						break
					}
				}
			}
			if item < 0 {
				return nil, false
			}
			order := analyticOrder{item: item}
			if c.take("DESC") {
				order.descending = true
			} else {
				c.take("ASC")
			}
			if c.word() == "NULLS" || c.word() == "COLLATE" {
				return nil, false
			}
			shape.order = append(shape.order, order)
			if !c.punctuation(",") {
				break
			}
		}
	}
	if c.take("LIMIT") {
		limit, ok := c.value()
		count, isInteger := limit.(int64)
		if !ok || !isInteger {
			return nil, false
		}
		shape.limit, shape.hasLimit = count, true
		if c.take("OFFSET") {
			offset, ok := c.value()
			skip, isInteger := offset.(int64)
			if !ok || !isInteger || skip < 0 {
				return nil, false
			}
			shape.offset = skip
		}
	}
	if !c.done() {
		return nil, false
	}
	for _, qualifier := range qualifiers {
		if qualifier != strings.ToLower(shape.table) && qualifier != strings.ToLower(shape.alias) {
			return nil, false
		}
	}
	return shape, true
}

// analyticRead answers a read statement from the copy; answered false when the statement runs on SQLite. It runs in
// the statement's read transaction, after its grants were checked and its columns described. A statement whose plan
// searches an index runs on SQLite for analyticBudget first; SQLite's answer within it is the answer.
func (session *Session) analyticRead(ctx context.Context, parsed statement, arguments []any, columns []resultColumn) (pgwire.Result, bool) {
	registry := session.options.Analytics
	if registry == nil || parsed.kind != statementRead {
		return pgwire.Result{}, false
	}
	shape, ok := parseAnalytic(parsed, arguments)
	if !ok || len(columns) != len(shape.items) {
		return pgwire.Result{}, false
	}
	schema, path, declared, ok := session.analyticTable(ctx, shape)
	if !ok {
		return pgwire.Result{}, false
	}
	// the copy's names are the table's own: SQLite's names are not case sensitive
	canonical := func(name string) (string, bool) {
		found, ok := declared[name]
		return found, ok
	}
	var needed []string
	for name := range shape.columns {
		found, ok := canonical(name)
		if !ok {
			return pgwire.Result{}, false
		}
		needed = append(needed, found)
	}
	slices.Sort(needed)
	query := shape.query
	query.Group = make([]string, len(shape.query.Group))
	for position, name := range shape.query.Group {
		query.Group[position], _ = canonical(name)
	}
	query.Aggregates = make([]analytics.Aggregate, len(shape.query.Aggregates))
	for position, aggregate := range shape.query.Aggregates {
		query.Aggregates[position] = aggregate
		if aggregate.Column != "" {
			query.Aggregates[position].Column, _ = canonical(aggregate.Column)
		}
	}
	query.Filter = renameColumns(shape.query.Filter, canonical)
	switch session.analyticPlan(ctx, parsed, arguments, shape) {
	case planOther:
		return pgwire.Result{}, false
	case planSearch:
		if analyticBudget <= 0 {
			break // no budget: the copy answers a search too
		}
		budget, cancel := context.WithTimeout(ctx, analyticBudget)
		result, err := session.run(budget, parsed, arguments, columns)
		expired := budget.Err() == context.DeadlineExceeded && ctx.Err() == nil
		cancel()
		if err == nil {
			return result, true // SQLite answered within the budget
		}
		if !expired {
			return pgwire.Result{}, false // SQLite's own error: the statement runs again on it and says it
		}
	}
	table, release := registry.Acquire(path, shape.table, needed)
	defer release()
	if table == nil {
		return pgwire.Result{}, false // built in the background; SQLite answers meanwhile
	}
	view, err := table.View(ctx, session.connection, schema)
	if err != nil {
		if errors.Is(err, analytics.ErrStale) {
			registry.Stale(path, shape.table) // built again in the background; an older snapshot (ErrBehind) is not
		}
		return pgwire.Result{}, false
	}
	registry.Changed(path, shape.table, view) // many changed rows: folded in the background
	answer, err := view.Execute(ctx, query)
	if err != nil {
		return pgwire.Result{}, false
	}
	rows := make([][]any, len(answer.Rows))
	for index, row := range answer.Rows {
		values := make([]any, len(shape.items))
		for position, item := range shape.items {
			if item.group >= 0 {
				values[position] = row[item.group]
			} else {
				values[position] = row[len(query.Group)+item.aggregate]
			}
		}
		rows[index] = values
	}
	if len(shape.order) > 0 {
		slices.SortStableFunc(rows, func(a, b []any) int {
			for _, order := range shape.order {
				comparison := compareOrder(a[order.item], b[order.item])
				if order.descending {
					comparison = -comparison
				}
				if comparison != 0 {
					return comparison
				}
			}
			return 0
		})
	}
	if shape.offset > 0 {
		rows = rows[min(int(shape.offset), len(rows)):]
	}
	if shape.hasLimit && shape.limit >= 0 && int(shape.limit) < len(rows) {
		rows = rows[:shape.limit]
	}
	result, err := session.renderRows(rows, columns, parsed)
	if err != nil {
		return pgwire.Result{}, false
	}
	analyticAnswers.Add(1)
	return result, true
}

// renameColumns is condition with each column's name as the table declares it.
func renameColumns(condition *analytics.Condition, canonical func(string) (string, bool)) *analytics.Condition {
	if condition == nil {
		return nil
	}
	renamed := *condition
	if renamed.Column != "" {
		renamed.Column, _ = canonical(renamed.Column)
	}
	renamed.Args = make([]*analytics.Condition, len(condition.Args))
	for position, argument := range condition.Args {
		renamed.Args[position] = renameColumns(argument, canonical)
	}
	return &renamed
}

// compareOrder orders two values of one output column as SQLite's ORDER BY does: NULL first, numbers by value, texts
// byte by byte.
func compareOrder(a, b any) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return -1
	case b == nil:
		return 1
	}
	number := func(value any) (float64, bool) {
		switch typed := value.(type) {
		case int64:
			return float64(typed), true
		case float64:
			return typed, true
		}
		return 0, false
	}
	if x, ok := a.(int64); ok {
		if y, ok := b.(int64); ok {
			return compareIntegers(x, y)
		}
	}
	x, xNumber := number(a)
	y, yNumber := number(b)
	switch {
	case xNumber && yNumber:
		switch {
		case x < y:
			return -1
		case x > y:
			return 1
		}
		return 0
	case xNumber:
		return -1
	case yNumber:
		return 1
	}
	return strings.Compare(fmt.Sprint(a), fmt.Sprint(b))
}

func compareIntegers(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// analyticTable finds the schema the statement's table is in (the first attached schema holding it when the statement
// names none, as SQLite looks), its file, and its columns by their names in lower case.
func (session *Session) analyticTable(ctx context.Context, shape *analyticShape) (string, string, map[string]string, bool) {
	candidates := session.order
	if shape.schema != "" {
		candidates = nil
		for _, name := range session.order {
			if strings.EqualFold(name, shape.schema) {
				candidates = []string{name}
			}
		}
	}
	for _, schema := range candidates {
		rows, err := session.connection.QueryContext(ctx, `SELECT name FROM pragma_table_xinfo(?, ?)`, shape.table, schema)
		if err != nil {
			return "", "", nil, false
		}
		declared := map[string]string{}
		for rows.Next() {
			var name string
			if rows.Scan(&name) == nil {
				declared[strings.ToLower(name)] = name
			}
		}
		rows.Close()
		if len(declared) == 0 {
			continue
		}
		path, err := filepath.Abs(session.target.Schemas[schema])
		if err != nil {
			return "", "", nil, false
		}
		return schema, path, declared, true
	}
	return "", "", nil, false
}

// analyticPlan reads SQLite's plan for the statement: planScan when it reads its table whole (SCAN, in rowid order or
// through an index for its order), the case the copy is for; planSearch when it searches an index (a value, a range,
// a skip-scan); planOther for anything else.
func (session *Session) analyticPlan(ctx context.Context, parsed statement, arguments []any, shape *analyticShape) int {
	rows, err := session.connection.QueryContext(ctx, "EXPLAIN QUERY PLAN "+parsed.text, arguments...)
	if err != nil {
		return planOther
	}
	defer rows.Close()
	plan, reads := planOther, 0
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if rows.Scan(&id, &parent, &unused, &detail) != nil {
			return planOther
		}
		fields := strings.Fields(detail)
		if len(fields) < 2 || (fields[0] != "SCAN" && fields[0] != "SEARCH") {
			continue // USE TEMP B-TREE FOR GROUP BY, and the like
		}
		reads++
		if !strings.EqualFold(fields[1], shape.table) && !strings.EqualFold(fields[1], shape.alias) {
			return planOther
		}
		plan = planScan
		if fields[0] == "SEARCH" {
			plan = planSearch
		}
	}
	if rows.Err() != nil || reads != 1 {
		return planOther
	}
	return plan
}

// renderRows renders values as run renders SQLite's rows: MaxRows and MaxBytes, each value by its column, each
// column's type decided from its values.
func (session *Session) renderRows(rows [][]any, columns []resultColumn, parsed statement) (pgwire.Result, error) {
	seen := make([][classBlob + 1]bool, len(columns))
	var result pgwire.Result
	var bytes int64
	for _, values := range rows {
		if len(result.Rows) >= session.options.MaxRows {
			return pgwire.Result{}, errors.New("rows past MaxRows") // SQLite's path says so to the client
		}
		row := make([]pgwire.Field, len(columns))
		for index, value := range values {
			class := classify(value)
			seen[index][class] = true
			row[index] = render(value, class, columns[index])
			bytes += int64(len(row[index].Data))
		}
		if bytes > session.options.MaxBytes {
			return pgwire.Result{}, errors.New("bytes past MaxBytes")
		}
		result.Rows = append(result.Rows, row)
	}
	result.Columns = make([]pgwire.Column, len(columns))
	for index, column := range columns {
		result.Columns[index] = finalColumn(column, seen[index])
	}
	result.CommandTag = fmt.Sprintf("SELECT %d", len(result.Rows))
	return result, nil
}
