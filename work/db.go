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
	TableName string
	Column    string
	Operator  string
	Value     value.Value
}

func (h *SQLProxyHandler) OnGet(key string) value.Value {
	return value.Value{K: value.Proxy, V: &value.ProxyData{
		Handler: &SQLProxyHandler{TableName: h.TableName, Column: key},
	}}
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
	joins      []string
	groups     []string
	havings    []string
	executor   LambdaExecutor
	connection string
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

func (q *DBQuery) Limit(n float64) *DBQuery {
	q.limit = int(n)
	return q
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

func (q *DBQuery) Find(idOrFn any) value.Value {
	// SMART FIND: If it's a Lambda, treat it as a Where condition
	switch v := idOrFn.(type) {
	case value.Value:
		if v.K == value.Func {
			return q.Where(v).One()
		}
	case *value.ScriptFunction:
		return q.Where(value.Value{K: value.Func, V: v}).One()
	}

	// TRADITIONAL FIND: Primary Key lookup
	q.conditions = []string{"\"id\" = $1"}
	q.whereArgs = []any{idOrFn}
	return q.One()
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
	q.fields = fields
	return q
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

func (q *DBQuery) Join(tableOrFn any, args ...value.Value) *DBQuery {
	return q.joinInternal("JOIN", tableOrFn, args...)
}

func (q *DBQuery) LeftJoin(tableOrFn any, args ...value.Value) *DBQuery {
	return q.joinInternal("LEFT JOIN", tableOrFn, args...)
}

func (q *DBQuery) joinInternal(typ string, tableOrFn any, args ...value.Value) *DBQuery {
	var tableName string
	var sFn *value.ScriptFunction

	// 1. Phân tích Lambda để lấy tên bảng từ Parameter Names
	switch v := tableOrFn.(type) {
	case string:
		tableName = v
		if len(args) > 0 && args[0].K == value.Func {
			sFn, _ = args[0].V.(*value.ScriptFunction)
		}
	case value.Value:
		if v.K == value.Func {
			sFn, _ = v.V.(*value.ScriptFunction)
		}
	case *value.ScriptFunction:
		sFn = v
	}

	// Nếu là Lambda, tự động lấy tên bảng từ biến đầu tiên người dùng đặt
	if sFn != nil && tableName == "" && len(sFn.ParamNames) > 0 {
		tableName = sFn.ParamNames[0]
	}

	if tableName == "" {
		return q
	}

	sqlJoin := fmt.Sprintf("%s \"%s\"", typ, tableName)

	// 2. Xử lý logic ON (Inject đúng tên bảng vào Proxy)
	if sFn != nil {
		// Elite Logic: Lấy tên bảng trực tiếp từ cách người dùng đặt tên biến trong JS
		joinTableAlias := sFn.ParamNames[0]
		primaryTableAlias := q.table // Mặc định
		if len(sFn.ParamNames) > 1 {
			primaryTableAlias = sFn.ParamNames[1]
		}

		hJoin := &SQLProxyHandler{TableName: joinTableAlias}
		pJoin := value.Value{K: value.Proxy, V: &value.ProxyData{Handler: hJoin}}

		hPrimary := &SQLProxyHandler{TableName: primaryTableAlias}
		pPrimary := value.Value{K: value.Proxy, V: &value.ProxyData{Handler: hPrimary}}

		// Thực thi Lambda: (orders, users) => orders.user_id == users.id
		res := q.executor.ExecuteLambda(sFn, []value.Value{pJoin, pPrimary})

		if res.K == value.Proxy {
			if d, ok := res.V.(*value.ProxyData); ok {
				if filter, ok := d.Handler.(*SQLProxyHandler); ok {
					if filter.Value.K == value.Proxy {
						if otherData, ok := filter.Value.V.(*value.ProxyData); ok {
							if otherFilter, ok := otherData.Handler.(*SQLProxyHandler); ok {
								// Sinh ra SQL chuẩn xác dựa trên tên bảng/biến
								sqlJoin += fmt.Sprintf(" ON \"%s\".\"%s\" = \"%s\".\"%s\"",
									filter.TableName, filter.Column,
									otherFilter.TableName, otherFilter.Column)
							}
						}
					}
				}
			}
		}
	}

	q.joins = append(q.joins, sqlJoin)
	return q
}

func (q *DBQuery) On(args ...value.Value) *DBQuery {
	if len(args) > 0 && args[0].K == value.Func && q.executor != nil {
		if sFn, ok := args[0].V.(*value.ScriptFunction); ok {
			handler := &SQLProxyHandler{}
			proxy := value.Value{K: value.Proxy, V: &value.ProxyData{Handler: handler}}
			res := q.executor.ExecuteLambda(sFn, []value.Value{proxy})
			if res.K == value.Proxy {
				if d, ok := res.V.(*value.ProxyData); ok {
					if filter, ok := d.Handler.(*SQLProxyHandler); ok {
						// Custom On condition: users.id = orders.user_id
						// We don't use $n placeholders for JOIN ON usually, but raw columns
						last := len(q.joins) - 1
						if last >= 0 {
							q.joins[last] += fmt.Sprintf(" ON \"%s\" = \"%s\"", filter.Column, filter.Value.Text())
						}
					}
				}
			}
		}
	}
	return q
}

func (q *DBQuery) Group(columns ...string) *DBQuery {
	q.groups = append(q.groups, columns...)
	return q
}

func (q *DBQuery) Having(args ...value.Value) *DBQuery {
	// Re-use logic from Where for Having
	if len(args) > 0 && args[0].K == value.Func && q.executor != nil {
		if sFn, ok := args[0].V.(*value.ScriptFunction); ok {
			handler := &SQLProxyHandler{}
			proxy := value.Value{K: value.Proxy, V: &value.ProxyData{Handler: handler}}
			res := q.executor.ExecuteLambda(sFn, []value.Value{proxy})
			if res.K == value.Proxy {
				if d, ok := res.V.(*value.ProxyData); ok {
					if filter, ok := d.Handler.(*SQLProxyHandler); ok {
						op := filter.Operator
						if op == "==" || op == "" {
							op = "="
						}
						argCount := len(q.whereArgs) + 1
						q.havings = append(q.havings, fmt.Sprintf("%s %s $%d", filter.Column, op, argCount))
						q.whereArgs = append(q.whereArgs, filter.Value.Interface())
					}
				}
			}
		}
	}
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
	return q.aggregate()
}

func (q *DBQuery) Avg(column string) value.Value {
	q.method = fmt.Sprintf("AVG(\"%s\")", column)
	return q.aggregate()
}

func (q *DBQuery) Min(column string) value.Value {
	q.method = fmt.Sprintf("MIN(\"%s\")", column)
	return q.aggregate()
}

func (q *DBQuery) Max(column string) value.Value {
	q.method = fmt.Sprintf("MAX(\"%s\")", column)
	return q.aggregate()
}

func (q *DBQuery) Get() value.Value {
	return q.executeGet()
}

func (q *DBQuery) All() value.Value {
	return q.Get()
}

func (q *DBQuery) Take(args ...value.Value) value.Value {
	if len(args) > 0 {
		q.limit = int(args[0].Float())
	}
	return q.Get()
}

func (q *DBQuery) ToList() value.Value {
	return q.Get()
}

func (q *DBQuery) executeGet() value.Value {
	db := GetDB(q.connection)
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

	// 1. JOINS
	for _, join := range q.joins {
		query += " " + join
	}

	// 2. WHERE
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

	// 3. GROUP BY
	if len(q.groups) > 0 {
		query += " GROUP BY "
		for i, g := range q.groups {
			if i > 0 {
				query += ", "
			}
			query += fmt.Sprintf("\"%s\"", g)
		}
	}

	// 4. HAVING
	if len(q.havings) > 0 {
		query += " HAVING "
		for i, h := range q.havings {
			if i > 0 {
				query += " AND "
			}
			query += h
		}
	}

	// 5. ORDER BY
	if q.order != "" {
		query += fmt.Sprintf(" ORDER BY %s", q.order)
	}

	// 6. LIMIT & OFFSET
	if q.limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", q.limit)
	} else {
		query += " LIMIT 60"
	}

	if q.offset > 0 {
		query += fmt.Sprintf(" OFFSET %d", q.offset)
	}

	// DEBUG LOG
	fmt.Printf("[DB] Executing SQL: %s | Args: %v\n", query, q.whereArgs)
	rows, err := db.QueryContext(context.Background(), query, q.whereArgs...)
	fmt.Printf("[DB] Query completed. Err: %v\n", err)

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
	fmt.Printf("[DB] Rows scanned: %d\n", len(res))

	return value.New(res)
}

func (q *DBQuery) First() value.Value {
	// First implies Limit 1 and take result
	q.limit = 1
	res := q.Get()
	if res.K == value.Array {
		if ptr, ok := res.V.(*[]value.Value); ok {
			arr := *ptr
			if len(arr) > 0 {
				return arr[0]
			}
		}
	}
	return value.NewNull()
}

func (q *DBQuery) One() value.Value {
	return q.First()
}

func (q *DBQuery) FirstOrDefault() value.Value {
	return q.First()
}

func (q *DBQuery) SingleOrDefault() value.Value {
	return q.One()
}

func (q *DBQuery) Any() value.Value {
	q.limit = 1
	res := q.Get()
	if ptr, ok := res.V.(*[]value.Value); ok {
		return value.New(len(*ptr) > 0)
	}
	return value.New(false)
}

func (q *DBQuery) Last() value.Value {
	// Simple logic: If no order, order by id DESC. If exists, flip it.
	if q.order == "" {
		q.OrderBy("id", "DESC")
	} else {
		if strings.Contains(strings.ToUpper(q.order), "ASC") {
			q.order = strings.Replace(strings.ToUpper(q.order), "ASC", "DESC", 1)
		} else {
			q.order = strings.Replace(strings.ToUpper(q.order), "DESC", "ASC", 1)
		}
	}
	return q.First()
}

func (q *DBQuery) aggregate() value.Value {
	res := q.First()
	if m, ok := res.V.(map[string]value.Value); ok {
		// Return the first value found in the map (the aggregate result)
		for _, v := range m {
			return v
		}
	}
	return res
}

func (q *DBQuery) Count(field ...string) value.Value {
	target := "*"
	if len(field) > 0 {
		target = fmt.Sprintf("\"%s\"", field[0])
	}
	q.method = fmt.Sprintf("COUNT(%s)", target)
	return q.aggregate()
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
