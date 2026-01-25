package work

import (
	"context"
	"fmt"
	"log"
	"reflect"
	"strings"

	"github.com/kitwork/engine/value"
)

// SQLProxyHandler implements value.ProxyHandler to capture SQL conditions
type SQLProxyHandler struct {
	Column   string
	Operator string
	Value    value.Value
}

func (h *SQLProxyHandler) OnGet(key string) value.Value {
	return value.Value{K: value.Proxy, V: &value.ProxyData{Handler: &SQLProxyHandler{Column: key}}}
}

func (h *SQLProxyHandler) OnCompare(op string, other value.Value) value.Value {
	return value.Value{K: value.Proxy, V: &value.ProxyData{
		Handler: &SQLProxyHandler{Column: h.Column, Operator: op, Value: other},
	}}
}

func (h *SQLProxyHandler) OnInvoke(method string, args ...value.Value) value.Value {
	if len(args) > 0 {
		return value.Value{K: value.Proxy, V: &value.ProxyData{
			Handler: &SQLProxyHandler{Column: h.Column, Operator: method, Value: args[0]},
		}}
	}
	return value.Value{K: value.Nil}
}

type LambdaExecutor interface {
	ExecuteLambda(fn *value.ScriptFunction, args []value.Value) value.Value
}

type DBQuery struct {
	table      string
	fields     []string
	limit      int
	offset     int
	order      string
	method     string
	conditions []string
	whereArgs  []any
	executor   LambdaExecutor
}

func NewDBQuery() *DBQuery {
	return &DBQuery{method: "select"}
}

func (q *DBQuery) SetExecutor(e LambdaExecutor) {
	q.executor = e
}

func (q *DBQuery) Table(table string) *DBQuery {
	q.table = table
	return q
}

func (q *DBQuery) From(table string) *DBQuery {
	return q.Table(table)
}

func (q *DBQuery) Where(args ...value.Value) *DBQuery {
	if len(args) == 0 {
		return q
	}

	// MAGIC WHERE: If first arg is a Lambda
	if args[0].K == value.Func && q.executor != nil {
		if sFn, ok := args[0].V.(*value.ScriptFunction); ok {
			// Create a Proxy with SQL handler
			handler := &SQLProxyHandler{}
			proxy := value.Value{K: value.Proxy, V: &value.ProxyData{Handler: handler}}
			res := q.executor.ExecuteLambda(sFn, []value.Value{proxy})

			if res.K == value.Proxy {
				if d, ok := res.V.(*value.ProxyData); ok {
					if filter, ok := d.Handler.(*SQLProxyHandler); ok {
						op := filter.Operator
						val := filter.Value

						// 1. Mapping JS operators to SQL Base
						switch strings.ToLower(op) {
						case "==", "===", "":
							op = "="
						case "like":
							op = "LIKE"
						case "in":
							op = "IN"
						case "!=", "!==":
							op = "<>"
						case ">", "<", ">=", "<=":
							op = strings.ToLower(op)
						}

						// --- SMART DETECTION BLOCK ---
						// 1. Auto-LIKE: Nếu chuỗi có dấu %
						if op == "=" && val.K == value.String {
							if strings.Contains(val.Text(), "%") {
								op = "LIKE"
							}
						}

						// 2. Auto-IN: Nếu giá trị là một Array
						if op == "=" && val.K == value.Array {
							op = "IN"
						}
						// ------------------------------

						// Handle IN pattern logic
						if op == "IN" {
							var list []any
							// TRÍ TUỆ NHÂN TẠO: Tự động trích xuất phần tử từ mọi loại mảng
							vRaw := val.Interface()
							rv := reflect.ValueOf(vRaw)
							if rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array {
								for i := 0; i < rv.Len(); i++ {
									item := rv.Index(i).Interface()
									if v, ok := item.(value.Value); ok {
										list = append(list, v.Interface())
									} else {
										list = append(list, item)
									}
								}
							} else {
								list = []any{vRaw}
							}

							placeholders := []string{}
							for _, v := range list {
								q.whereArgs = append(q.whereArgs, v)
								placeholders = append(placeholders, fmt.Sprintf("$%d", len(q.whereArgs)))
							}

							if len(placeholders) == 0 {
								q.conditions = append(q.conditions, "1=0")
							} else {
								q.conditions = append(q.conditions, fmt.Sprintf("\"%s\" IN (%s)", filter.Column, strings.Join(placeholders, ", ")))
							}
							return q
						}

						// Standard Operator Execution
						argCount := len(q.whereArgs) + 1
						q.conditions = append(q.conditions, fmt.Sprintf("\"%s\" %s $%d", filter.Column, op, argCount))
						q.whereArgs = append(q.whereArgs, val.Interface())
						return q
					}
				}
			}
		}
	}

	argCount := len(q.whereArgs) + 1

	if len(args) == 1 && args[0].IsString() {
		q.conditions = append(q.conditions, args[0].Text())
	} else if len(args) == 2 {
		// Dùng $n cho Postgres
		q.conditions = append(q.conditions, fmt.Sprintf("\"%s\" = $%d", args[0].Text(), argCount))
		q.whereArgs = append(q.whereArgs, args[1].Interface())
	} else if len(args) == 3 {
		q.conditions = append(q.conditions, fmt.Sprintf("\"%s\" %s $%d", args[0].Text(), args[1].Text(), argCount))
		q.whereArgs = append(q.whereArgs, args[2].Interface())
	}
	return q
}

func (q *DBQuery) Take(n float64) *DBQuery {
	q.limit = int(n)
	return q
}

func (q *DBQuery) Limit(n float64) *DBQuery {
	return q.Take(n)
}

func (q *DBQuery) Offset(n float64) *DBQuery {
	q.offset = int(n)
	return q
}

func (q *DBQuery) Skip(n float64) *DBQuery {
	return q.Offset(n)
}

func (q *DBQuery) OrderBy(column string, direction ...string) *DBQuery {
	dir := "ASC"
	if len(direction) > 0 {
		dir = direction[0]
	}
	q.order = fmt.Sprintf("\"%s\" %s", column, dir)
	return q
}

func (q *DBQuery) Find(id any) value.Value {
	q.conditions = []string{"\"id\" = $1"}
	q.whereArgs = []any{id}
	return q.First()
}

func (q *DBQuery) Insert(data value.Value) *DBQuery {
	fmt.Printf("[DB] (Mock) INSERT INTO %s | Data: %s\n", q.table, data.Text())
	return q
}

func (q *DBQuery) selectField(fields ...string) *DBQuery {
	q.fields = append(q.fields, fields...)
	return q
}

func (q *DBQuery) Select(fields ...string) *DBQuery {
	return q.selectField(fields...)
}

func (q *DBQuery) Or(args ...value.Value) *DBQuery {
	q.conditions = append(q.conditions, "OR")
	return q.Where(args...)
}

func (q *DBQuery) In(columnOrFn any, vals ...any) *DBQuery {
	// Support Lambda approach: .in(u => u.id == [1,2,3])
	switch v := columnOrFn.(type) {
	case value.Value:
		if v.K == value.Func {
			return q.Where(v)
		}
	case *value.ScriptFunction:
		return q.Where(value.Value{K: value.Func, V: v})
	}

	column, ok := columnOrFn.(string)
	if !ok {
		return q
	}
	// Chuyển tập hợp thành mảng để Where() tự xử lý thông minh
	return q.Where(value.New(column), value.New(vals))
}

func (q *DBQuery) Null(column string) *DBQuery {
	q.conditions = append(q.conditions, fmt.Sprintf("\"%s\" IS NULL", column))
	return q
}

func (q *DBQuery) NotNull(column string) *DBQuery {
	q.conditions = append(q.conditions, fmt.Sprintf("\"%s\" IS NOT NULL", column))
	return q
}

func (q *DBQuery) Like(columnOrFn any, pattern ...string) *DBQuery {
	// Support Lambda approach: .like(u => u.name == "Apple%")
	switch v := columnOrFn.(type) {
	case value.Value:
		if v.K == value.Func {
			return q.Where(v)
		}
	case *value.ScriptFunction:
		return q.Where(value.Value{K: value.Func, V: v})
	}

	column, ok := columnOrFn.(string)
	if !ok {
		return q
	}
	p := ""
	if len(pattern) > 0 {
		p = pattern[0]
	}
	return q.Where(value.New(column), value.New(p))
}

func (q *DBQuery) Sum(column string) value.Value {
	q.method = fmt.Sprintf("SUM(\"%s\")", column)
	return q.Get()
}

func (q *DBQuery) Avg(column string) value.Value {
	q.method = fmt.Sprintf("AVG(\"%s\")", column)
	return q.Get()
}

func (q *DBQuery) Min(column string) value.Value {
	q.method = fmt.Sprintf("MIN(\"%s\")", column)
	return q.Get()
}

func (q *DBQuery) Max(column string) value.Value {
	q.method = fmt.Sprintf("MAX(\"%s\")", column)
	return q.Get()
}

func (q *DBQuery) Get() value.Value {
	db := GetDB()
	if db == nil {
		return q.mockGet()
	}

	selectedFields := "*"
	if len(q.fields) > 0 {
		selectedFields = ""
		for i, f := range q.fields {
			if i > 0 {
				selectedFields += ", "
			}
			selectedFields += fmt.Sprintf("\"%s\"", f)
		}
	}

	if strings.Contains(q.method, "(") {
		selectedFields = q.method
	}

	query := fmt.Sprintf("SELECT %s FROM \"%s\"", selectedFields, q.table)

	if len(q.conditions) > 0 {
		query += " WHERE "
		for i, cond := range q.conditions {
			if i > 0 && cond != "OR" && q.conditions[i-1] != "OR" {
				query += " AND "
			} else if i > 0 && cond != "OR" {
				query += " "
			}
			query += cond
		}
	}

	if q.limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", q.limit)
	} else {
		query += " LIMIT 60"
	}

	if q.offset > 0 {
		query += fmt.Sprintf(" OFFSET %d", q.offset)
	}

	if q.order != "" {
		query += fmt.Sprintf(" ORDER BY %s", q.order)
	}

	// DEBUG LOG
	fmt.Printf("[DB] Executing SQL: %s | Args: %v\n", query, q.whereArgs)

	rows, err := db.QueryContext(context.Background(), query, q.whereArgs...)

	if err != nil {
		fmt.Printf("[DB] Query Error: %v | SQL: %s\n", err, query)
		return value.Value{K: value.Nil}
	}
	defer rows.Close()

	columns, _ := rows.Columns()
	res := make([]value.Value, 0)

	for rows.Next() {
		values := make([]any, len(columns))
		valuePtrs := make([]any, len(columns))
		for i := range values {
			valuePtrs[i] = &values[i]
		}

		if err := rows.Scan(valuePtrs...); err != nil {
			continue
		}

		rowMap := make(map[string]value.Value)
		for i, col := range columns {
			rowMap[col] = value.New(values[i])
		}
		res = append(res, value.New(rowMap))
	}

	return value.New(res)
}

func (q *DBQuery) First() value.Value {
	q.limit = 1
	res := q.Get()
	if res.K == value.Array {
		if arr, ok := res.V.([]value.Value); ok {
			if len(arr) > 0 {
				return arr[0]
			}
		}
	}
	return value.NewNull()
}

func (q *DBQuery) mockGet() value.Value {
	log.Println("[DB] WARNING: Using Mock DB!")
	limit := q.limit
	if limit == 0 {
		limit = 2
	}
	res := make([]value.Value, 0)
	for i := 1; i <= limit; i++ {
		row := make(map[string]value.Value)
		row["id"] = value.New(i)
		row["username"] = value.New(fmt.Sprintf("mock_user_%d", i))
		res = append(res, value.New(row))
	}
	return value.New(res)
}

func (q *DBQuery) String() string {
	return fmt.Sprintf("DBQuery on %s", q.table)
}
