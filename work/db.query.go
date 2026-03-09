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
// SQL PROXY HANDLER (For "Magic Where")
// ==========================================

type SQLProxy struct {
	TableName string
	Column    string
	Operator  string
	Value     value.Value
}

func (h *SQLProxy) OnGet(key string) value.Value {
	return value.Value{K: value.Proxy, V: &SQLProxy{TableName: h.TableName, Column: key}}
}

func (h *SQLProxy) OnCompare(op string, other value.Value) value.Value {
	return value.Value{K: value.Proxy, V: &SQLProxy{TableName: h.TableName, Column: h.Column, Operator: op, Value: other}}
}

func (h *SQLProxy) OnInvoke(method string, args ...value.Value) value.Value {
	if len(args) > 0 {
		return value.Value{K: value.Proxy, V: &SQLProxy{TableName: h.TableName, Column: h.Column, Operator: method, Value: args[0]}}
	}
	return value.Value{K: value.Nil}
}

// ==========================================
// CORE DATA STRUCTURES (AST)
// ==========================================

type Condition struct {
	Column   string
	Operator string
	Value    any
	Logic    string // "AND" or "OR"
	IsColumn bool   // TRUE if comparing column to column (e.g. user.id = order.id)
}

type OrderQuery struct {
	Column    string
	Direction string // "ASC" or "DESC"
}

type JoinQuery struct {
	Type  string // "JOIN", "LEFT JOIN", etc.
	Table string
	On    string
}

type Query struct {
	vm *runtime.Runtime
	db *sql.DB

	ctx *context.Context

	tables []string
	method string
	fields []string

	conditions []Condition
	joins      []JoinQuery
	orders     []OrderQuery

	groups  []string
	havings []Condition

	returning []string
	limit     int
	offset    int
	maxLimit  int

	debug bool
}

func NewQuery(vm *runtime.Runtime, db *sql.DB) *Query {
	return &Query{vm: vm, db: db}
}

// ==========================================
// PUBLIC FLUENT API
// ==========================================

func (q *Query) Method(name string) *Query { q.method = name; return q }
func (q *Query) Table(names ...string) *Query {
	for _, n := range names {
		parts := strings.Split(n, ",")
		for _, p := range parts {
			if trimmed := strings.TrimSpace(p); trimmed != "" {
				q.tables = append(q.tables, trimmed)
			}
		}
	}
	return q
}
func (q *Query) From(names ...string) *Query { return q.Table(names...) }

func (q *Query) Field(set string) *Query           { q.fields = []string{set}; return q }
func (q *Query) Select(fields ...string) *Query    { q.fields = fields; return q }
func (q *Query) Returning(fields ...string) *Query { q.returning = fields; return q }

func (q *Query) Limit(n int) *Query   { q.limit = n; return q }
func (q *Query) Limited(n int) *Query { q.maxLimit = n; return q }

func (q *Query) Offset(n int) *Query { q.offset = n; return q }
func (q *Query) Skip(n int) *Query   { return q.Offset(n) }

func (q *Query) Debug() *Query { q.debug = true; return q }

func (q *Query) Where(args ...value.Value) *Query {
	if len(args) == 0 {
		return q
	}

	// MAGIC WHERE: If first arg is a Lambda
	if args[0].K == value.Func && q.vm != nil {
		if sFn, ok := args[0].V.(*value.Lambda); ok {
			// Chuẩn bị proxies tương ứng với từng tham số truyền vào Lambda
			proxies := make([]value.Value, len(sFn.Params))
			var tables []string
			for i, p := range sFn.Params {
				tables = append(tables, p)
				proxies[i] = value.Value{K: value.Proxy, V: &SQLProxy{TableName: p}}
			}

			// Auto-table inference cho TẤT CẢ các tham số nếu bảng bị bỏ trống
			if len(q.tables) == 0 && len(tables) > 0 {
				q.tables = tables
			}

			res := q.vm.ExecuteLambda(sFn, proxies)

			if res.K == value.Proxy {
				if filter, ok := res.V.(*SQLProxy); ok {
					// Nếu Value bên phải cũng là một Proxy -> So sánh chéo hai cột
					if filter.Value.K == value.Proxy {
						if otherFilter, ok := filter.Value.V.(*SQLProxy); ok {
							leftCol := filter.Column
							if filter.TableName != "" {
								leftCol = filter.TableName + "." + leftCol
							}
							rightCol := otherFilter.Column
							if otherFilter.TableName != "" {
								rightCol = otherFilter.TableName + "." + rightCol
							}
							q.conditions = append(q.conditions, Condition{
								Column:   leftCol,
								Operator: filter.Operator,
								Value:    rightCol,
								Logic:    "AND",
								IsColumn: true,
							})
							return q
						}
					}

					// Ngược lại, so sánh với một giá trị cố định
					col := filter.Column
					if filter.TableName != "" {
						col = filter.TableName + "." + filter.Column
					}
					return q.and(col, filter.Operator, filter.Value)
				}
			}
		}
	}

	if len(args) == 1 {
		q.conditions = append(q.conditions, Condition{Column: args[0].Text(), Logic: "AND"})
	} else if len(args) == 2 {
		q.and(args[0].Text(), "=", args[1])
	} else if len(args) == 3 {
		q.and(args[0].Text(), args[1].Text(), args[2])
	}

	return q
}

func (q *Query) And(args ...value.Value) *Query {
	return q.Where(args...)
}

func (q *Query) Or(args ...value.Value) *Query {
	if len(args) == 0 {
		return q
	}

	// MAGIC OR: If first arg is a Lambda
	if args[0].K == value.Func && q.vm != nil {
		if sFn, ok := args[0].V.(*value.Lambda); ok {
			tableName := ""
			if len(q.tables) > 0 {
				tableName = q.tables[0]
			}
			handler := &SQLProxy{TableName: tableName}
			proxy := value.Value{K: value.Proxy, V: handler}
			res := q.vm.ExecuteLambda(sFn, []value.Value{proxy})

			if res.K == value.Proxy {
				if filter, ok := res.V.(*SQLProxy); ok {
					return q.condition("OR", filter.Column, filter.Operator, filter.Value)
				}
			}
		}
	}

	if len(args) == 2 {
		q.condition("OR", args[0].Text(), "=", args[1])
	} else if len(args) == 3 {
		q.condition("OR", args[0].Text(), args[1].Text(), args[2])
	}

	return q
}

func (q *Query) Order(col string, dir string) *Query {
	q.orders = append(q.orders, OrderQuery{Column: col, Direction: dir})
	return q
}

func (q *Query) OrderBy(col string, dir ...string) *Query {
	direction := "ASC"
	if len(dir) > 0 {
		direction = dir[0]
	}
	return q.Order(col, direction)
}

func (q *Query) Sort(col string, dir ...string) *Query {
	return q.OrderBy(col, dir...)
}

func (q *Query) Group(cols ...string) *Query {
	q.groups = append(q.groups, cols...)
	return q
}

func (q *Query) GroupBy(cols ...string) *Query {
	return q.Group(cols...)
}

func (q *Query) Having(args ...value.Value) *Query {
	if len(args) == 0 {
		return q
	}

	// MAGIC HAVING: If first arg is a Lambda
	if args[0].K == value.Func && q.vm != nil {
		if sFn, ok := args[0].V.(*value.Lambda); ok {
			tableName := ""
			if len(q.tables) > 0 {
				tableName = q.tables[0]
			}
			handler := &SQLProxy{TableName: tableName}
			proxy := value.Value{K: value.Proxy, V: handler}
			res := q.vm.ExecuteLambda(sFn, []value.Value{proxy})

			if res.K == value.Proxy {
				if filter, ok := res.V.(*SQLProxy); ok {
					q.havings = append(q.havings, Condition{Column: filter.Column, Operator: filter.Operator, Value: filter.Value, Logic: "AND"})
					return q
				}
			}
		}
	}

	if len(args) == 2 {
		q.havings = append(q.havings, Condition{Column: args[0].Text(), Operator: "=", Value: args[1], Logic: "AND"})
	} else if len(args) == 3 {
		q.havings = append(q.havings, Condition{Column: args[0].Text(), Operator: args[1].Text(), Value: args[2], Logic: "AND"})
	}

	return q
}

func (q *Query) In(column string, vals any) *Query {
	return q.in(column, vals)
}

func (q *Query) IsNull(column string) *Query {
	return q.isNull(column)
}

func (q *Query) NotNull(column string) *Query {
	return q.notNull(column)
}

func (q *Query) Join(args ...value.Value) *Query {
	// Case 1: .Join("orders", "users.id = orders.user_id")
	if len(args) == 2 {
		q.joins = append(q.joins, JoinQuery{Table: args[0].String(), On: args[1].String(), Type: "JOIN"})
		return q
	}

	// Case 2: Magic Join Lambda .Join((order, user) => order.user_id == user.id)
	if len(args) == 1 && q.vm != nil {
		val := args[0]
		if val.K == value.Func {
			if sFn, ok := val.V.(*value.Lambda); ok {
				// Chúng ta cần ít nhất một tham số (bảng join), tham số thứ hai là bảng chính
				joinTableAlias := sFn.Params[0]
				primaryTableAlias := ""
				if len(sFn.Params) > 1 {
					primaryTableAlias = sFn.Params[1]
				}

				// TỰ SUY LUẬN BẢNG CHÍNH: Nếu bảng hiện tại rỗng, lấy tham số thứ hai của Lambda
				if len(q.tables) == 0 && primaryTableAlias != "" {
					q.tables = []string{primaryTableAlias}
				}

				hJoin := &SQLProxy{TableName: joinTableAlias}
				pJoin := value.Value{K: value.Proxy, V: hJoin}

				hPrimary := &SQLProxy{TableName: primaryTableAlias}
				pPrimary := value.Value{K: value.Proxy, V: hPrimary}

				res := q.vm.ExecuteLambda(sFn, []value.Value{pJoin, pPrimary})

				if res.K == value.Proxy {
					if filter, ok := res.V.(*SQLProxy); ok {
						if filter.Value.K == value.Proxy {
							if otherFilter, ok := filter.Value.V.(*SQLProxy); ok {
								on := fmt.Sprintf("\"%s\".\"%s\" = \"%s\".\"%s\"",
									filter.TableName, filter.Column,
									otherFilter.TableName, otherFilter.Column)
								q.joins = append(q.joins, JoinQuery{Table: joinTableAlias, On: on, Type: "JOIN"})
								return q
							}
						}
					}
				}
			}
		}
	}

	return q
}

func (q *Query) LeftJoin(table string, on string) *Query {
	q.joins = append(q.joins, JoinQuery{Table: table, On: on, Type: "LEFT JOIN"})
	return q
}

func (q *Query) RightJoin(table string, on string) *Query {
	q.joins = append(q.joins, JoinQuery{Table: table, On: on, Type: "RIGHT JOIN"})
	return q
}

func (q *Query) First(val ...value.Value) value.Value {
	if len(val) > 0 {
		q.Where(val...)
	}
	return q.first()
}

func (q *Query) list(limit int) value.Value {
	q.limit = limit
	return q.get()
}

func (q *Query) first() value.Value {
	return q.list(1).First()
}

func (q *Query) findBy(column string, operator string, value any) *Query {
	return q.and(column, operator, value)
}

func (q *Query) find(column string, value any) *Query {
	return q.findBy(column, "==", value)
}

func (q *Query) Find(vals ...value.Value) value.Value {
	length := len(vals)

	switch length {
	case 0:
		return value.NULL
	case 1:
		val := vals[0]
		if !val.IsCallable() {
			return q.find("id", val.Interface()).First()
		}
		return q.Where(vals...).First()
	case 2:
		key := vals[0]
		val := vals[1]
		if key.IsString() {
			return q.find(key.String(), val.Interface()).First()
		}
	case 3:
		key := vals[0]
		op := vals[1]
		val := vals[2]
		if key.IsString() && op.IsString() {
			return q.findBy(key.String(), op.String(), val.Interface()).First()
		}
	}

	return value.NULL
}

func (q *Query) List(args ...value.Value) value.Value {
	if len(args) == 0 {
		return q.get()
	}

	val := args[0]
	if val.IsNumber() {
		return q.list(val.Int())
	}

	// Apply as where condition if not a number
	q.Where(args...)
	return q.get()
}

func (q *Query) Exists(args ...value.Value) value.Value {
	if len(args) > 0 {
		q.Where(args...)
	}
	// We only need to know if at least one record exists
	res := q.list(1)
	arr := res.Array()
	return value.New(len(arr) > 0)
}

func (q *Query) val() value.Value {
	return q.first().One()
}

func (q *Query) condition(logic string, column, operator string, value any) *Query {
	q.conditions = append(q.conditions, Condition{Column: column, Operator: operator, Value: value, Logic: logic})
	return q
}

func (q *Query) and(column string, operator string, value any) *Query {
	return q.condition("AND", column, operator, value)
}

func (q *Query) or(column string, operator string, value any) *Query {
	return q.condition("OR", column, operator, value)
}

func (q *Query) in(column string, vals any) *Query {
	return q.and(column, "IN", vals)
}

func (q *Query) isNull(column string) *Query {
	return q.and(column, "==", nil)
}

func (q *Query) notNull(column string) *Query {
	return q.and(column, "!=", nil)
}

func (q *Query) fieldAppend(add string) *Query {
	q.fields = append(q.fields, add)
	return q
}

func (q *Query) count(col string) *Query {
	if col == "*" {
		return q.Field("COUNT(*)")
	}
	return q.Field(fmt.Sprintf("COUNT(\"%s\")", col))
}

func (q *Query) sum(col string) *Query {
	return q.Field(fmt.Sprintf("SUM(\"%s\")", col))
}

func (q *Query) avg(col string) *Query {
	return q.Field(fmt.Sprintf("AVG(\"%s\")", col))
}

func (q *Query) max(col string) *Query {
	return q.Field(fmt.Sprintf("MAX(\"%s\")", col))
}

func (q *Query) min(col string) *Query {
	return q.Field(fmt.Sprintf("MIN(\"%s\")", col))
}

func (q *Query) Count(args ...value.Value) value.Value {
	length := len(args)

	switch length {
	case 0:
		return q.count("*").val()
	case 1:
		val := args[0]
		if val.IsString() {
			// Matches: .count("email")
			return q.count(val.String()).val()
		}
		if val.IsCallable() {
			// Matches: .count(u => u.status == "active")
			return q.Where(val).count("*").val()
		}
	case 2:
		// Matches: .count("status", "active")
		return q.Where(args...).count("*").val()
	case 3:
		// Matches: .count("age", ">", 18)
		return q.Where(args...).count("*").val()
	}

	return q.count("*").val()
}

func (q *Query) Sum(col string) value.Value {
	return q.sum(col).val()
}

func (q *Query) Avg(col string) value.Value {
	return q.avg(col).val()
}

func (q *Query) Max(col string) value.Value {
	return q.max(col).val()
}

func (q *Query) Min(col string) value.Value {
	return q.min(col).val()
}

// ==========================================
// INTERNAL EXECUTORS
// ==========================================

func (q *Query) getSQL() (string, []any) {
	selectedFields := "*"
	if len(q.fields) > 0 {
		var quoted []string
		for _, f := range q.fields {
			if strings.Contains(f, "(") || strings.Contains(f, "\"") || strings.Contains(f, "*") {
				quoted = append(quoted, f)
			} else {
				quoted = append(quoted, fmt.Sprintf("\"%s\"", f))
			}
		}
		selectedFields = strings.Join(quoted, ", ")
	}

	fromClause := ""
	if len(q.tables) > 0 {
		var quoted []string
		for _, rawTable := range q.tables {
			tableNames := strings.Split(rawTable, ",")
			for _, name := range tableNames {
				name = strings.TrimSpace(name)
				if name != "" {
					quoted = append(quoted, fmt.Sprintf("\"%s\"", name))
				}
			}
		}
		if len(quoted) > 0 {
			fromClause = strings.Join(quoted, ", ")
		} else {
			fromClause = "\"\""
		}
	} else {
		fromClause = "\"\""
	}

	query := fmt.Sprintf("SELECT %s FROM %s", selectedFields, fromClause)

	for _, join := range q.joins {
		query += fmt.Sprintf(" %s \"%s\" ON %s", join.Type, join.Table, join.On)
	}

	whereSQL, whereArgs := q.buildConditions(q.conditions, 1)
	if whereSQL != "" {
		query += " WHERE " + whereSQL
	}

	if len(q.groups) > 0 {
		var quoted []string
		for _, g := range q.groups {
			quoted = append(quoted, fmt.Sprintf("\"%s\"", g))
		}
		query += " GROUP BY " + strings.Join(quoted, ", ")
	}

	if len(q.havings) > 0 {
		havingSQL, havingArgs := q.buildConditions(q.havings, len(whereArgs)+1)
		if havingSQL != "" {
			query += " HAVING " + havingSQL
			whereArgs = append(whereArgs, havingArgs...)
		}
	}

	if len(q.orders) > 0 {
		var parts []string
		for _, o := range q.orders {
			parts = append(parts, fmt.Sprintf("\"%s\" %s", o.Column, o.Direction))
		}
		query += " ORDER BY " + strings.Join(parts, ", ")
	}

	limit := q.limit
	if limit <= 0 {
		limit = DefaultDBLimit
	}
	max := q.maxLimit
	if max <= 0 {
		max = DefaultDBMaxLimit
	}
	if limit > max {
		limit = max
	}
	query += fmt.Sprintf(" LIMIT %d", limit)

	if q.offset > 0 {
		query += fmt.Sprintf(" OFFSET %d", q.offset)
	}

	return query, whereArgs
}

func quoteCol(col string) string {
	if strings.Contains(col, ".") {
		parts := strings.SplitN(col, ".", 2)
		return fmt.Sprintf("\"%s\".\"%s\"", parts[0], parts[1])
	}
	return fmt.Sprintf("\"%s\"", col)
}

func (q *Query) buildConditions(conditions []Condition, argOffset int) (string, []any) {
	if len(conditions) == 0 {
		return "", nil
	}

	var parts []string
	var args []any
	for i, cond := range conditions {
		logic := cond.Logic
		if i == 0 {
			logic = ""
		} else {
			logic += " "
		}

		op := cond.Operator
		switch strings.ToLower(op) {
		case "==", "===", "":
			op = "="
		case "!=", "!==":
			op = "<>"
		}

		if cond.Value == nil && !cond.IsColumn {
			if op == "=" {
				parts = append(parts, fmt.Sprintf("%s%s IS NULL", logic, quoteCol(cond.Column)))
			} else {
				parts = append(parts, fmt.Sprintf("%s%s IS NOT NULL", logic, quoteCol(cond.Column)))
			}
			continue
		}

		if cond.IsColumn {
			parts = append(parts, fmt.Sprintf("%s%s %s %s", logic, quoteCol(cond.Column), op, quoteCol(fmt.Sprintf("%v", cond.Value))))
		} else {
			if strings.ToUpper(op) == "IN" {
				var list []any
				v := cond.Value
				if val, ok := v.(value.Value); ok {
					v = val.Interface()
				}
				rv := reflect.ValueOf(v)
				if rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array {
					for j := 0; j < rv.Len(); j++ {
						list = append(list, rv.Index(j).Interface())
					}
				} else {
					list = []any{v}
				}

				if len(list) == 0 {
					parts = append(parts, fmt.Sprintf("%s1=0", logic))
				} else {
					var placeholders []string
					for _, item := range list {
						args = append(args, item)
						placeholders = append(placeholders, fmt.Sprintf("$%d", len(args)+argOffset-1))
					}
					parts = append(parts, fmt.Sprintf("%s%s IN (%s)", logic, quoteCol(cond.Column), strings.Join(placeholders, ", ")))
				}
			} else {
				val := cond.Value
				if v, ok := val.(value.Value); ok {
					val = v.Interface()
				}
				args = append(args, val)
				parts = append(parts, fmt.Sprintf("%s%s %s $%d", logic, quoteCol(cond.Column), op, len(args)+argOffset-1))
			}
		}
	}

	return strings.Join(parts, " "), args
}

func (q *Query) get() value.Value {
	if q.db == nil {
		return value.Value{K: value.Nil}
	}

	sqlStr, args := q.getSQL()
	if q.debug {
		fmt.Printf("[DB] Executing SQL: %s | Args: %v\n", sqlStr, args)
	}

	ctx := context.Background()
	if q.ctx != nil && *q.ctx != nil {
		ctx = *q.ctx
	}

	rows, err := q.db.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		fmt.Printf("[DB] Query Error: %v\n", err)
		return value.Value{K: value.Nil}
	}
	defer rows.Close()

	columns, _ := rows.Columns()
	var res []value.Value
	for rows.Next() {
		values := make([]any, len(columns))
		ptr := make([]any, len(columns))
		for i := range values {
			ptr[i] = &values[i]
		}
		if err := rows.Scan(ptr...); err != nil {
			continue
		}
		row := make(map[string]value.Value)
		for i, col := range columns {
			row[col] = value.New(values[i])
		}
		res = append(res, value.New(row))
	}
	if len(res) == 0 {
		// fmt.Printf("[DB] GET Query returned no rows\n")
	}
	return value.New(res)
}

func (q *Query) Create(args ...value.Value) value.Value {
	if len(args) == 0 {
		return value.Value{K: value.Nil}
	}
	val := args[0]
	if val.IsMap() {
		return q.insert(val.Map())
	}
	return value.Value{K: value.Nil}
}

func (q *Query) Save(args ...value.Value) value.Value {
	if len(args) == 0 {
		return value.Value{K: value.Nil}
	}
	val := args[0]
	if val.IsMap() {
		m := val.Map()
		// If ID exists, try update
		if id, ok := m["id"]; ok && !id.IsBlank() {
			return q.Where(value.New("id"), id).update(m)
		}
		return q.insert(m)
	}
	return value.Value{K: value.Nil}
}

func (q *Query) Update(args ...value.Value) value.Value {
	if len(args) == 0 {
		return value.Value{K: value.Nil}
	}

	// Case 1: .Update({status: "active"})
	if len(args) == 1 && args[0].IsMap() {
		return q.update(args[0].Map())
	}

	// Case 2: .Update("status", "active")
	if len(args) == 2 && args[0].IsString() {
		m := make(map[string]value.Value)
		m[args[0].String()] = args[1]
		return q.update(m)
	}

	return value.Value{K: value.Nil}
}

func (q *Query) Delete() value.Value {
	return q.delete()
}

func (q *Query) Remove() value.Value {
	return q.remove()
}

func (q *Query) insert(val map[string]value.Value) value.Value {
	if len(q.tables) == 0 || q.db == nil {
		return value.Value{K: value.Nil}
	}

	var cols []string
	var placeholders []string
	var args []any
	i := 1
	for k, v := range val {
		cols = append(cols, fmt.Sprintf("\"%s\"", k))
		placeholders = append(placeholders, fmt.Sprintf("$%d", i))
		args = append(args, v.Interface())
		i++
	}

	returningClause := "RETURNING *"
	if len(q.returning) > 0 {
		var quoted []string
		for _, f := range q.returning {
			quoted = append(quoted, fmt.Sprintf("\"%s\"", f))
		}
		returningClause = "RETURNING " + strings.Join(quoted, ", ")
	}

	query := fmt.Sprintf("INSERT INTO \"%s\" (%s) VALUES (%s) %s",
		q.tables[0], strings.Join(cols, ", "), strings.Join(placeholders, ", "), returningClause)

	if q.debug {
		fmt.Printf("[DB] Executing SQL: %s | Args: %v\n", query, args)
	}

	ctx := context.Background()
	if q.ctx != nil && *q.ctx != nil {
		ctx = *q.ctx
	}

	rows, err := q.db.QueryContext(ctx, query, args...)
	if err != nil {
		fmt.Printf("[DB] Insert Error: %v\n", err)
		return value.Value{K: value.Nil}
	}
	defer rows.Close()

	if rows.Next() {
		columns, _ := rows.Columns()
		values := make([]any, len(columns))
		ptr := make([]any, len(columns))
		for i := range values {
			ptr[i] = &values[i]
		}
		if err := rows.Scan(ptr...); err == nil {
			row := make(map[string]value.Value)
			for i, col := range columns {
				row[col] = value.New(values[i])
			}
			return value.New(row)
		}
	}
	return value.Value{K: value.Nil}
}

func (q *Query) update(val map[string]value.Value) value.Value {
	if len(q.tables) == 0 || q.db == nil {
		return value.Value{K: value.Nil}
	}

	if len(q.conditions) == 0 {
		fmt.Printf("[DB] Update Error: Missing WHERE clause. Bulk updates are blocked for safety.\n")
		return value.Value{K: value.Nil}
	}

	var sets []string
	var args []any
	i := 1
	for k, v := range val {
		sets = append(sets, fmt.Sprintf("\"%s\" = $%d", k, i))
		args = append(args, v.Interface())
		i++
	}

	whereSQL, whereArgs := q.buildConditions(q.conditions, i)
	args = append(args, whereArgs...)

	returningClause := "RETURNING *"
	if len(q.returning) > 0 {
		var quoted []string
		for _, f := range q.returning {
			quoted = append(quoted, fmt.Sprintf("\"%s\"", f))
		}
		returningClause = "RETURNING " + strings.Join(quoted, ", ")
	}

	query := fmt.Sprintf("UPDATE \"%s\" SET %s WHERE %s %s", q.tables[0], strings.Join(sets, ", "), whereSQL, returningClause)

	if q.debug {
		fmt.Printf("[DB] Executing SQL: %s | Args: %v\n", query, args)
	}

	ctx := context.Background()
	if q.ctx != nil && *q.ctx != nil {
		ctx = *q.ctx
	}

	rows, err := q.db.QueryContext(ctx, query, args...)
	if err != nil {
		fmt.Printf("[DB] Update Error: %v\n", err)
		return value.Value{K: value.Nil}
	}
	defer rows.Close()

	if rows.Next() {
		columns, _ := rows.Columns()
		values := make([]any, len(columns))
		ptr := make([]any, len(columns))
		for i := range values {
			ptr[i] = &values[i]
		}
		if err := rows.Scan(ptr...); err == nil {
			row := make(map[string]value.Value)
			for i, col := range columns {
				row[col] = value.New(values[i])
			}
			return value.New(row)
		}
	}
	return value.Value{K: value.Nil}
}

func (q *Query) delete() value.Value {
	return q.update(map[string]value.Value{
		"deleted_at": value.New(time.Now()),
	})
}

func (q *Query) remove() value.Value {
	if len(q.tables) == 0 || q.db == nil {
		return value.Value{K: value.Nil}
	}

	if len(q.conditions) == 0 {
		fmt.Printf("[DB] WARNING: Attempting hard REMOVE without WHERE on table %s. Blocked for safety.\n", q.tables[0])
		return value.Value{K: value.Nil}
	}

	whereSQL, whereArgs := q.buildConditions(q.conditions, 1)
	query := fmt.Sprintf("DELETE FROM \"%s\" WHERE %s", q.tables[0], whereSQL)

	if q.debug {
		fmt.Printf("[DB] Executing SQL: %s | Args: %v\n", query, whereArgs)
	}

	ctx := context.Background()
	if q.ctx != nil && *q.ctx != nil {
		ctx = *q.ctx
	}

	res, err := q.db.ExecContext(ctx, query, whereArgs...)
	if err != nil {
		fmt.Printf("[DB] Remove Error: %v\n", err)
		return value.Value{K: value.Nil}
	}
	affected, _ := res.RowsAffected()
	return value.New(affected)
}
