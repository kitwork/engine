package work

import (
	"encoding/json"
	"fmt"
	"math"
	"time"
	"unicode/utf8"

	"github.com/kitwork/engine/database/statement"
	"github.com/kitwork/engine/utilities/query"
	"github.com/kitwork/engine/value"
)

const (
	// statementDefaultLimit is how many rows list() gives without a limit (KITWORK_DATABASE.md mục 2).
	statementDefaultLimit = 1000
	// statementRowCeiling is the most rows one list(n) gives; past it a list is read by pages.
	statementRowCeiling = 10000
)

// bindTruth is what a condition is before it reaches the storage: open (it depends on the row), or
// true or false whatever the row (a value from outside in the place of a condition, one([])).
type bindTruth int8

const (
	bindOpen bindTruth = iota
	bindTrue
	bindFalse
)

// run binds a checked statement's holes and runs it on a fresh query of its table.
func (m *ManagedDatabase) run(prepared *preparedStatement, holes []value.Value) value.Value {
	s := prepared.statement
	if len(holes) != len(s.Holes) {
		return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("câu database nhận %d giá trị, có %d: dịch lại", len(s.Holes), len(holes)))
	}
	binder := &statementBinder{s: s, holes: holes, columns: prepared.columns}
	switch s.Action {
	case statement.ActionUpdate, statement.ActionDelete, statement.ActionUpsert:
		return m.runWrite(prepared, binder)
	}
	if s.Action == statement.ActionInsert {
		row, failure := binder.row()
		if failure.Failed() {
			return failure
		}
		table, failure := m.proxy.table(s.Table)
		if table == nil {
			return failure
		}
		return table.Create(value.New(row))
	}
	if positive, negative := s.Where.Matches(); len(positive)+len(negative) > 0 {
		return m.runMatches(prepared, binder, positive, negative)
	}
	predicate, truth, failure := binder.predicate(s.Where)
	if failure.Failed() {
		return failure
	}
	limit, offset := statementDefaultLimit, 0
	if s.Action == statement.ActionList {
		limit, offset, failure = binder.bounds()
		if failure.Failed() {
			return failure
		}
	}
	if truth == bindFalse || limit == 0 {
		// no row can match: the storage is not opened
		switch s.Action {
		case statement.ActionFirst:
			return value.NewNil()
		case statement.ActionCount:
			return value.New(0)
		}
		return value.New([]value.Value{})
	}
	table, failure := m.proxy.table(s.Table)
	if table == nil {
		return failure
	}
	if truth == bindOpen && predicate != nil {
		if m.proxy.engine != "kitdb" {
			// a decimal on SQLite is a scaled integer: what it is compared with is too (db.sqlite_decimal.go)
			if message := sqliteWritePredicate(s.Table, prepared.columns, predicate); message != "" {
				return value.InvalidFailure(value.InvalidArgumentCode, message)
			}
		}
		table.builder().WherePredicate(*predicate)
	}
	for _, key := range s.Order {
		direction := "ASC"
		if key.Descending {
			direction = "DESC"
		}
		table.OrderBy(key.Column, direction)
	}
	switch s.Action {
	case statement.ActionFirst:
		return table.First()
	case statement.ActionCount:
		return table.Count()
	}
	table.Limit(limit)
	table.builder().Limited(limit)
	if offset > 0 {
		table.Offset(offset)
	}
	// list is an array, [] when nothing matches (KITWORK_DATABASE.md mục 2): SQLite's query gives no rows as an
	// array holding no slice, which JSON writes null
	if rows := table.List(); rows.Failed() || rows.K == value.Array && len(rows.Array()) > 0 {
		return rows
	}
	return value.New([]value.Value{})
}

// statementBinder binds one run of a statement: its holes, by the kind of the column each meets.
type statementBinder struct {
	s       *statement.Statement
	holes   []value.Value
	columns map[string]*ColumnSpec
}

// value is a value node's value and where it is written: a literal, a hole, null.
func (b *statementBinder) value(node *statement.Node) (value.Value, string) {
	switch node.Op {
	case statement.OpHole:
		return b.holes[*node.Hole], b.s.Holes[*node.Hole].Site
	case statement.OpLiteral:
		return literalValue(node), "trong lambda"
	}
	return value.NewNil(), "trong lambda"
}

// literalValue is a literal node's value.
func literalValue(node *statement.Node) value.Value {
	var item any
	if err := json.Unmarshal(node.Value, &item); err != nil {
		return value.NewNil()
	}
	return value.New(item)
}

// predicate binds a condition: nil and open for every row.
func (b *statementBinder) predicate(node *statement.Node) (*query.Predicate, bindTruth, value.Value) {
	if node == nil {
		return nil, bindTrue, value.Value{}
	}
	switch node.Op {
	case statement.OpTable:
		return nil, bindTrue, value.Value{}
	case statement.OpTruth:
		item, _ := b.value(node.Args[0])
		if item.Truthy() {
			return nil, bindTrue, value.Value{}
		}
		return nil, bindFalse, value.Value{}
	case statement.OpNot:
		child, truth, failure := b.predicate(node.Args[0])
		switch {
		case failure.Failed():
			return nil, bindOpen, failure
		case truth == bindTrue:
			return nil, bindFalse, value.Value{}
		case truth == bindFalse:
			return nil, bindTrue, value.Value{}
		}
		return &query.Predicate{Kind: query.PredicateNot, Children: []query.Predicate{*child}}, bindOpen, value.Value{}
	case statement.OpAnd, statement.OpOr:
		left, leftTruth, failure := b.predicate(node.Args[0])
		if failure.Failed() {
			return nil, bindOpen, failure
		}
		right, rightTruth, failure := b.predicate(node.Args[1])
		if failure.Failed() {
			return nil, bindOpen, failure
		}
		// a side true or false whatever the row decides, or drops out (KitDB wants two children)
		absorbing, neutral, kind := bindFalse, bindTrue, query.PredicateAnd
		if node.Op == statement.OpOr {
			absorbing, neutral, kind = bindTrue, bindFalse, query.PredicateOr
		}
		switch {
		case leftTruth == absorbing || rightTruth == absorbing:
			return nil, absorbing, value.Value{}
		case leftTruth == neutral && rightTruth == neutral:
			return nil, neutral, value.Value{}
		case leftTruth == neutral:
			return right, bindOpen, value.Value{}
		case rightTruth == neutral:
			return left, bindOpen, value.Value{}
		}
		return &query.Predicate{Kind: kind, Children: []query.Predicate{*left, *right}}, bindOpen, value.Value{}
	}
	return b.comparison(node)
}

// comparison binds column OP value.
func (b *statementBinder) comparison(node *statement.Node) (*query.Predicate, bindTruth, value.Value) {
	column := node.Args[0].Column
	spec := b.columns[column]
	right := node.Args[1]
	condition := query.Condition{Column: column, Logic: "AND"}
	switch right.Op {
	case statement.OpNull:
		condition.Operator = "is null"
		if node.Op == statement.OpUnequal {
			condition.Operator = "is not null"
		}
	case statement.OpOne:
		items, site := b.value(right.Args[0])
		if items.K != value.Array {
			return nil, bindOpen, value.InvalidFailure(value.InvalidArgumentCode,
				fmt.Sprintf("one(…) ở %s nhận một mảng: %s.%s === one([…])", site, b.s.Table, column))
		}
		list := items.Array()
		if len(list) == 0 {
			// in nothing is false, not in nothing is true (KITWORK_DATABASE.md mục 2)
			if node.Op == statement.OpEqual {
				return nil, bindFalse, value.Value{}
			}
			return nil, bindTrue, value.Value{}
		}
		bound := make([]value.Value, 0, len(list))
		for _, item := range list {
			if item.IsNil() {
				bound = append(bound, item)
				continue
			}
			coerced, failure := bindValue(column, spec, item, site)
			if failure.Failed() {
				return nil, bindOpen, failure
			}
			bound = append(bound, coerced)
		}
		condition.Operator = "in"
		if node.Op == statement.OpUnequal {
			condition.Operator = "not in"
		}
		condition.Value = value.New(bound)
	default:
		item, site := b.value(right)
		condition.Operator = comparisonOperator(node.Op)
		// a null from outside makes the comparison unknown: it matches no row, as SQL (only null written
		// in the lambda means IS NULL)
		if !item.IsNil() {
			coerced, failure := bindValue(column, spec, item, site)
			if failure.Failed() {
				return nil, bindOpen, failure
			}
			condition.Value = coerced
		} else {
			condition.Value = value.NewNil()
		}
	}
	return &query.Predicate{Kind: query.PredicateCondition, Condition: condition}, bindOpen, value.Value{}
}

func comparisonOperator(op string) string {
	switch op {
	case statement.OpEqual:
		return "="
	case statement.OpUnequal:
		return "<>"
	}
	return op
}

// bindValue checks a value against its column's kind, by the rule a write follows, and gives it in its
// stored form. The error names the column and where the value is written, never the value.
func bindValue(column string, spec *ColumnSpec, item value.Value, site string) (value.Value, value.Value) {
	if spec == nil {
		return value.Value{}, value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("không có cột %s", column))
	}
	kind := spec.builder
	if kind == "" {
		kind = spec.kind
	}
	wrong := func(want string) value.Value {
		return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("cột %s là %s: giá trị ở %s không phải %s", column, kindSpelling(spec, kind), site, want))
	}
	// a text the storage cannot keep is no value of any column: a form sent in another encoding (Git Bash's
	// curl on Windows sends "cà phê" in the ANSI code page, c%E0+ph%EA) reached KitDB's row encoder, which
	// refused it as DATABASE in its own words, and a condition with one matched nothing without a word
	// (22/10/2026)
	if item.K == value.String && !utf8.ValidString(item.String()) {
		return value.Value{}, wrong("chuỗi UTF-8")
	}
	switch kind {
	case "id", "text", "kitid", "uuid", "varchar", "char":
		if item.K != value.String {
			return value.Value{}, wrong("chuỗi")
		}
	case "date":
		if item.K != value.String {
			return value.Value{}, wrong("ngày YYYY-MM-DD")
		}
		if _, err := time.Parse("2006-01-02", item.String()); err != nil {
			return value.Value{}, wrong("ngày YYYY-MM-DD")
		}
	case "integer":
		if item.K != value.Number || math.IsNaN(item.N) || math.IsInf(item.N, 0) || item.N != math.Trunc(item.N) ||
			math.Abs(item.N) > 1<<53-1 {
			return value.Value{}, wrong("số nguyên")
		}
	case "float":
		if item.K != value.Number || math.IsNaN(item.N) || math.IsInf(item.N, 0) {
			return value.Value{}, wrong("số")
		}
	case "decimal":
		switch {
		case item.K == value.Number && !math.IsNaN(item.N) && !math.IsInf(item.N, 0):
		case item.K == value.String && validKitDBDecimal(item.String()):
		default:
			return value.Value{}, wrong("số hay chuỗi thập phân")
		}
	case "boolean", "bool":
		if item.K != value.Bool {
			return value.Value{}, wrong("true hay false")
		}
	case "timestamp", "datetime":
		if item.K != value.String && item.K != value.Time {
			return value.Value{}, wrong("thời điểm")
		}
	}
	return coerceWrite(spec.kind, item), value.Value{}
}

// kindSpelling is a column's kind as the schema writes it.
func kindSpelling(spec *ColumnSpec, kind string) string {
	if kind == "decimal" && spec.precision > 0 {
		return fmt.Sprintf("decimal(%d, %d)", spec.precision, spec.scale)
	}
	return kind + "()"
}

// row binds an insert's data. null for a column that refuses it is an error naming the column.
func (b *statementBinder) row() (map[string]value.Value, value.Value) {
	row := make(map[string]value.Value, len(b.s.Values))
	for _, field := range b.s.Values {
		spec := b.columns[field.Column]
		item, site := b.value(field.Value)
		if item.IsNil() {
			if spec != nil && spec.notNull {
				return nil, value.InvalidFailure(value.InvalidArgumentCode,
					fmt.Sprintf("cột %s của %s không nhận null (giá trị ở %s); cột được bỏ trống khai .nullable()", field.Column, b.s.Table, site))
			}
			row[field.Column] = value.NewNil()
			continue
		}
		coerced, failure := bindValue(field.Column, spec, item, site)
		if failure.Failed() {
			return nil, failure
		}
		row[field.Column] = coerced
	}
	return row, value.Value{}
}

// bounds binds list's limit and page: list(n), list({ page, limit }), or a value that is one of the two.
func (b *statementBinder) bounds() (int, int, value.Value) {
	s := b.s
	switch {
	case s.Bounds != nil:
		item, site := b.value(s.Bounds)
		switch item.K {
		case value.Number:
			limit, failure := boundNumber("list(n)", item, site, 0)
			return limit, 0, failure
		case value.Map:
			bounds := item.Map()
			page, hasPage := bounds["page"]
			limit, hasLimit := bounds["limit"]
			if !hasPage || !hasLimit || len(bounds) != 2 {
				return 0, 0, value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("list(…) ở %s nhận { page, limit }", site))
			}
			return pageBounds(page, limit, site, site)
		case value.Func:
			return 0, 0, value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("list(…) ở %s là một hàm: lambda của câu viết ngay trong câu, db.list((bảng) => …)", site))
		}
		return 0, 0, value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("list(…) ở %s nhận số hay { page, limit }", site))
	case s.Page != nil:
		page, pageSite := b.value(s.Page)
		limit, limitSite := b.value(s.Limit)
		return pageBounds(page, limit, pageSite, limitSite)
	case s.Limit != nil:
		item, site := b.value(s.Limit)
		limit, failure := boundNumber("list(n)", item, site, 0)
		return limit, 0, failure
	}
	return statementDefaultLimit, 0, value.Value{}
}

func pageBounds(page, limit value.Value, pageSite, limitSite string) (int, int, value.Value) {
	pageNumber, failure := boundNumber("page", page, pageSite, 1)
	if failure.Failed() {
		return 0, 0, failure
	}
	size, failure := boundNumber("limit", limit, limitSite, 0)
	if failure.Failed() {
		return 0, 0, failure
	}
	// the rows before the page are read and skipped: past what an int32 counts, the page is refused
	if offset := int64(pageNumber-1) * int64(size); offset > math.MaxInt32 {
		return 0, 0, value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("page ở %s quá lớn: trang bắt đầu sau %d dòng", pageSite, math.MaxInt32))
	}
	return size, (pageNumber - 1) * size, value.Value{}
}

// boundNumber is a limit or a page: a whole number from least up, a limit at most statementRowCeiling.
func boundNumber(what string, item value.Value, site string, least int) (int, value.Value) {
	if item.K != value.Number || item.N != math.Trunc(item.N) || item.N < float64(least) || math.IsInf(item.N, 0) {
		return 0, value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("%s ở %s là số nguyên từ %d", what, site, least))
	}
	if what != "page" && item.N > statementRowCeiling {
		return 0, value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("%s ở %s quá %d dòng: đọc theo trang, list({ page, limit })", what, site, statementRowCeiling))
	}
	if item.N > math.MaxInt32 {
		return 0, value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("%s ở %s quá lớn", what, site))
	}
	return int(item.N), value.Value{}
}

// checkLiterals checks, once, every literal a statement writes against its column's kind.
func checkLiterals(s *statement.Statement, columns map[string]*ColumnSpec) value.Value {
	var failure value.Value
	var walk func(*statement.Node)
	walk = func(node *statement.Node) {
		if node == nil || failure.Failed() {
			return
		}
		switch node.Op {
		case statement.OpEqual, statement.OpUnequal, statement.OpLess, statement.OpAtMost, statement.OpMore, statement.OpAtLeast:
			if right := node.Args[1]; right.Op == statement.OpLiteral {
				column := node.Args[0].Column
				_, failure = bindValue(column, columns[column], literalValue(right), "trong lambda")
			}
			return
		}
		for _, argument := range node.Args {
			walk(argument)
		}
	}
	walk(s.Where)
	for _, field := range s.Values {
		if failure.Failed() {
			break
		}
		if field.Value.Op == statement.OpLiteral {
			_, failure = bindValue(field.Column, columns[field.Column], literalValue(field.Value), "trong lambda")
		}
	}
	for _, bound := range []*statement.Node{s.Limit, s.Page} {
		if bound != nil && bound.Op == statement.OpLiteral && !failure.Failed() {
			_, failure = boundNumber("list(n)", literalValue(bound), "trong câu", 0)
		}
	}
	return failure
}
