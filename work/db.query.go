package work

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
)

// ==========================================
// CORE DATA STRUCTURES (AST)
// ==========================================

// Condition represents a single logical filter in a WHERE or HAVING clause.
type Condition struct {
	Column   string
	Operator string
	Value    any
	Logic    string // "AND" or "OR"
}

// OrderQuery represents a sort direction for a column.
type OrderQuery struct {
	Column    string
	Direction string // "ASC" or "DESC"
}

// JoinQuery represents a SQL JOIN operation.
type JoinQuery struct {
	Type  string // e.g., "JOIN", "LEFT JOIN"
	Table string
	On    string // The JOIN condition string (e.g. "users.id = orders.user_id")
}

// Query is the main builder object that holds the state of the query (AST).
type Query struct {
	vm *runtime.Runtime
	db *sql.DB

	table      string
	fields     []string
	conditions []Condition
	joins      []JoinQuery
	orders     []OrderQuery
	groups     []string
	havings    []Condition

	limit  int
	offset int
}

// (SQLProxyHandler is defined in query.go)

// ==========================================
// AST BUILDER METHODS (Fluent API)
// ==========================================

func (q *Query) Table(name string) *Query {
	q.table = name
	return q
}

func (q *Query) Select(fields ...string) *Query {
	q.fields = fields
	return q
}

func (q *Query) Where(args ...value.Value) *Query {
	return q.addCondition("AND", args...)
}

func (q *Query) Or(args ...value.Value) *Query {
	return q.addCondition("OR", args...)
}

func (q *Query) In(column string, vals any) *Query {
	return q.add("AND", column, "IN", vals)
}

func (q *Query) add(logic, column, operator string, value any) *Query {
	q.conditions = append(q.conditions, Condition{
		Column:   column,
		Operator: operator,
		Value:    value,
		Logic:    logic,
	})
	return q
}

func (q *Query) isNull(column string) *Query {
	return q.add("AND", column, "IS NULL", nil)
}

func (q *Query) notNull(column string) *Query {
	return q.add("AND", column, "IS NOT NULL", nil)
}

func (q *Query) Limit(n int) *Query {
	q.limit = n
	return q
}

func (q *Query) Offset(n int) *Query {
	q.offset = n
	return q
}

func (q *Query) OrderBy(column string, direction ...string) *Query {
	dir := "ASC"
	if len(direction) > 0 {
		dir = strings.ToUpper(direction[0])
	}

	// Handle "id desc" string format
	parts := strings.Split(strings.TrimSpace(column), " ")
	if len(parts) > 1 {
		column = parts[0]
		dir = strings.ToUpper(parts[1])
	}

	q.orders = append(q.orders, OrderQuery{Column: column, Direction: dir})
	return q
}

func (q *Query) GroupBy(columns ...string) *Query {
	q.groups = append(q.groups, columns...)
	return q
}

func (q *Query) Group(columns ...string) *Query {
	return q.GroupBy(columns...)
}

func (q *Query) Join(table string, on string) *Query {
	q.joins = append(q.joins, JoinQuery{Type: "JOIN", Table: table, On: on})
	return q
}

func (q *Query) LeftJoin(table string, on string) *Query {
	q.joins = append(q.joins, JoinQuery{Type: "LEFT JOIN", Table: table, On: on})
	return q
}

// ==========================================
// INTERNAL HELPERS
// ==========================================

func (q *Query) addCondition(logic string, args ...value.Value) *Query {
	if len(args) == 0 {
		return q
	}

	// 1. Magic Lambda Support (u) => u.id == 1
	if args[0].K == value.Func && q.vm != nil {
		if sFn, ok := args[0].V.(*value.Lambda); ok {
			// Auto-infer table if not set
			if q.table == "" && len(sFn.Params) > 0 {
				q.table = sFn.Params[0]
			}

			proxy := value.Value{K: value.Proxy, V: &SQLProxyHandler{TableName: q.table}}
			res := q.vm.ExecuteLambda(sFn, []value.Value{proxy})

			if res.K == value.Proxy {
				if filter, ok := res.V.(*SQLProxyHandler); ok {
					op := q.mapOperator(filter.Operator)
					val := filter.Value

					// Smart Detection (Auto-LIKE, Auto-IN)
					if op == "=" && val.K == value.String && strings.Contains(val.Text(), "%") {
						op = "LIKE"
					}
					if op == "=" && val.K == value.Array {
						op = "IN"
					}

					q.conditions = append(q.conditions, Condition{
						Column:   filter.Column,
						Operator: op,
						Value:    val.Interface(),
						Logic:    logic,
					})
					return q
				}
			}
		}
	}

	// 2. Traditional Args: .where("id", 1) or .where("id", ">", 1)
	if len(args) == 2 {
		q.conditions = append(q.conditions, Condition{Column: args[0].Text(), Operator: "=", Value: args[1].Interface(), Logic: logic})
	} else if len(args) == 3 {
		q.conditions = append(q.conditions, Condition{Column: args[0].Text(), Operator: args[1].Text(), Value: args[2].Interface(), Logic: logic})
	}
	return q
}

func (q *Query) mapOperator(op string) string {
	switch strings.ToLower(op) {
	case "==", "===", "":
		return "="
	case "!=", "!==":
		return "<>"
	case "gt":
		return ">"
	case "lt":
		return "<"
	case "gte":
		return ">="
	case "lte":
		return "<="
	default:
		return strings.ToUpper(op)
	}
}

// ==========================================
// EXECUTORS
// ==========================================

func (q *Query) list(limit int) value.Value {
	q.limit = limit
	return q.run()
}

func (q *Query) first() value.Value {
	q.limit = 1
	return q.run()
}

func (q *Query) Find(idOrFn any) value.Value {
	// SMART FIND: If it's a Lambda, treat it as a Where condition
	if v, ok := idOrFn.(value.Value); ok && v.K == value.Func {
		return q.Where(v).first()
	}

	// TRADITIONAL FIND: Primary Key lookup
	q.conditions = append(q.conditions, Condition{
		Column:   "id",
		Operator: "=",
		Value:    idOrFn,
		Logic:    "AND",
	})
	q.limit = 1
	return q.run()
}

func (q *Query) find(col, op string, val any) value.Value {
	q.conditions = append(q.conditions, Condition{
		Column:   col,
		Operator: op,
		Value:    val,
		Logic:    "AND",
	})
	return q.run()
}

func (q *Query) Count(column ...string) value.Value {
	col := "*"
	if len(column) > 0 {
		col = column[0]
	}
	q.fields = []string{fmt.Sprintf("COUNT(%s)", col)}
	return q.run()
}

func (q *Query) Sum(column string) value.Value {
	q.fields = []string{fmt.Sprintf("SUM(\"%s\")", column)}
	return q.run()
}

func (q *Query) Avg(column string) value.Value {
	q.fields = []string{fmt.Sprintf("AVG(\"%s\")", column)}
	return q.run()
}

func (q *Query) Min(column string) value.Value {
	q.fields = []string{fmt.Sprintf("MIN(\"%s\")", column)}
	return q.run()
}

func (q *Query) Max(column string) value.Value {
	q.fields = []string{fmt.Sprintf("MAX(\"%s\")", column)}
	return q.run()
}

// ==========================================
// SQL GENERATION & EXECUTION
// ==========================================

func (q *Query) run() value.Value {
	// For now, let's keep it in "Preview Mode" as requested earlier,
	// but this can easily call .execute() to run for real.
	return q.toQuery().Raw()
}

func (q *Query) RawQuery() value.Value {
	return q.toQuery().Raw()
}

func (q *Query) toQuery() *SQLQuery {
	var sql strings.Builder
	var args []any
	argCount := 1

	// 1. SELECT
	sql.WriteString("SELECT ")
	if len(q.fields) == 0 {
		sql.WriteString("*")
	} else {
		for i, f := range q.fields {
			if i > 0 {
				sql.WriteString(", ")
			}
			if strings.Contains(f, "(") || strings.Contains(f, " ") {
				sql.WriteString(f)
			} else {
				sql.WriteString(fmt.Sprintf("\"%s\"", f))
			}
		}
	}

	// 2. FROM
	sql.WriteString(fmt.Sprintf(" FROM \"%s\"", q.table))

	// 3. JOIN
	for _, j := range q.joins {
		sql.WriteString(fmt.Sprintf(" %s \"%s\" ON %s", j.Type, j.Table, j.On))
	}

	// 4. WHERE
	if len(q.conditions) > 0 {
		sql.WriteString(" WHERE ")
		for i, cond := range q.conditions {
			if i > 0 {
				sql.WriteString(fmt.Sprintf(" %s ", cond.Logic))
			}

			// 1. Handle Parameterless Operators (IS NULL, etc.)
			if strings.Contains(strings.ToUpper(cond.Operator), "NULL") {
				sql.WriteString(fmt.Sprintf("\"%s\" %s", cond.Column, cond.Operator))
				continue
			}

			// 2. Handle IN Operator Special Case
			if strings.ToUpper(cond.Operator) == "IN" {
				rv := reflect.ValueOf(cond.Value)
				if rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array {
					var placeholders []string
					for j := 0; j < rv.Len(); j++ {
						placeholders = append(placeholders, fmt.Sprintf("$%d", argCount))
						args = append(args, rv.Index(j).Interface())
						argCount++
					}
					sql.WriteString(fmt.Sprintf("\"%s\" IN (%s)", cond.Column, strings.Join(placeholders, ", ")))
					continue
				}
			}

			// 3. Standard Operator
			sql.WriteString(fmt.Sprintf("\"%s\" %s $%d", cond.Column, cond.Operator, argCount))
			args = append(args, cond.Value)
			argCount++
		}
	}

	// 5. GROUP BY
	if len(q.groups) > 0 {
		sql.WriteString(" GROUP BY ")
		for i, g := range q.groups {
			if i > 0 {
				sql.WriteString(", ")
			}
			sql.WriteString(fmt.Sprintf("\"%s\"", g))
		}
	}

	// 6. ORDER BY
	if len(q.orders) > 0 {
		sql.WriteString(" ORDER BY ")
		for i, o := range q.orders {
			if i > 0 {
				sql.WriteString(", ")
			}
			sql.WriteString(fmt.Sprintf("\"%s\" %s", o.Column, o.Direction))
		}
	}

	// 7. LIMIT & OFFSET
	if q.limit > 0 {
		sql.WriteString(fmt.Sprintf(" LIMIT %d", q.limit))
	}
	if q.offset > 0 {
		sql.WriteString(fmt.Sprintf(" OFFSET %d", q.offset))
	}

	return &SQLQuery{
		db:    q.db,
		Query: sql.String(),
		Args:  args,
	}
}

// SQLQuery is a helper to execute the generated SQL.
type SQLQuery struct {
	db    *sql.DB
	Query string
	Args  []any
}

func (s *SQLQuery) execute() value.Value {
	if s.db == nil {
		return value.Value{K: value.Nil}
	}

	rows, err := s.db.QueryContext(context.Background(), s.Query, s.Args...)
	if err != nil {
		fmt.Printf("[DB] Execution Error: %v\n", err)
		return value.Value{K: value.Nil}
	}
	defer rows.Close()

	columns, _ := rows.Columns()
	var results []value.Value

	for rows.Next() {
		values := make([]any, len(columns))
		ptrs := make([]any, len(columns))
		for i := range values {
			ptrs[i] = &values[i]
		}

		if err := rows.Scan(ptrs...); err != nil {
			continue
		}

		row := make(map[string]value.Value)
		for i, col := range columns {
			row[col] = value.New(values[i])
		}
		results = append(results, value.New(row))
	}

	return value.New(results)
}

func (s *SQLQuery) Raw() value.Value {
	raw := s.Query
	for i := len(s.Args); i >= 1; i-- {
		placeholder := fmt.Sprintf("$%d", i)
		val := s.Args[i-1]
		var valStr string

		switch v := val.(type) {
		case string:
			valStr = fmt.Sprintf("'%s'", strings.ReplaceAll(v, "'", "''"))
		case time.Time:
			valStr = fmt.Sprintf("'%s'", v.Format(time.RFC3339))
		default:
			valStr = fmt.Sprintf("%v", v)
		}
		raw = strings.ReplaceAll(raw, placeholder, valStr)
	}
	return value.New(raw)
}
