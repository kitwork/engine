package work

// // SQLProxy represents a connected database, allowing access to tables
// type SQLProxy struct {
// 	db     *sql.DB
// 	vm     *runtime.Runtime
// 	tenant *Tenant
// }

// func (s *SQLProxy) OnGet(key string) value.Value {
// 	// e.g. db.users
// 	return value.Value{K: value.Proxy, V: &QueryBuilder{
// 		db:     s.db,
// 		table:  key,,
// 	}}
// }

// func (s *SQLProxy) OnInvoke(method string, args ...value.Value) value.Value { return value.Value{} }
// func (s *SQLProxy) OnCompare(op string, other value.Value) value.Value      { return value.Value{} }

// // QueryBuilder represents the industrial fluent query builder for a specific table
// type QueryBuilder struct {
// 	db     *sql.DB
// 	table  string
// 	wheres []string
// 	args   []any
// 	orders []string
// 	limit  int
// 	offset int
// }

// func (q *QueryBuilder) OnGet(key string) value.Value {
// 	// Columns reference: p.name
// 	return value.Value{K: value.Proxy, V: &ColumnProxy{name: key}}
// }

// func (q *QueryBuilder) OnInvoke(method string, args ...value.Value) value.Value {
// 	switch method {
// 	case "where":
// 		if len(args) > 0 && args[0].K == value.Func {
// 			lambda := args[0].V.(*value.Lambda)
// 			row := value.Value{K: value.Proxy, V: &RowProxy{table: q.table}}
// 			res := q.vm.ExecuteLambda(lambda, []value.Value{row})
// 			if res.K == value.Proxy {
// 				if cond, ok := res.V.(*ConditionProxy); ok {
// 					q.wheres = append(q.wheres, cond.SQL)
// 					q.args = append(q.args, cond.Args...)
// 				}
// 			}
// 		}
// 	case "sort", "orderBy":
// 		if len(args) > 0 {
// 			direction := "ASC"
// 			if len(args) > 1 {
// 				direction = strings.ToUpper(args[1].String())
// 			}
// 			col := ""
// 			// Magic lambda for sort: sort(u => u.id)
// 			if args[0].K == value.Func {
// 				lambda := args[0].V.(*value.Lambda)
// 				row := value.Value{K: value.Proxy, V: &RowProxy{table: q.table}}
// 				res := q.vm.ExecuteLambda(lambda, []value.Value{row})
// 				if cp, ok := res.V.(*ColumnProxy); ok {
// 					col = cp.name
// 				}
// 			} else {
// 				col = args[0].String()
// 			}
// 			if col != "" {
// 				q.orders = append(q.orders, fmt.Sprintf("%s %s", col, direction))
// 			}
// 		}
// 	case "limit", "take":
// 		if len(args) > 0 {
// 			q.limit = int(args[0].N)
// 		}
// 	case "skip", "offset":
// 		if len(args) > 0 {
// 			q.offset = int(args[0].N)
// 		}

// 	// Execution Methods
// 	case "list":
// 		return q.execute("list")
// 	case "first":
// 		return q.execute("first")
// 	case "count":
// 		return q.execute("count")
// 	case "exists":
// 		return q.execute("exists")

// 	// Mutators
// 	case "create", "insert":
// 		return q.executeMutator("create", args...)
// 	case "update":
// 		return q.executeMutator("update", args...)
// 	case "delete":
// 		return q.executeMutator("delete", args...)
// 	case "destroy":
// 		return q.executeMutator("destroy", args...)
// 	}

// 	return value.Value{K: value.Proxy, V: q}
// }

// func (q *QueryBuilder) OnCompare(op string, other value.Value) value.Value { return value.Value{} }

// func (q *QueryBuilder) buildSQL(method string) (string, []any) {
// 	var sb strings.Builder
// 	switch method {
// 	case "count":
// 		sb.WriteString("SELECT COUNT(*) FROM ")
// 	case "exists":
// 		sb.WriteString("SELECT EXISTS(SELECT 1 FROM ")
// 	default:
// 		sb.WriteString("SELECT * FROM ")
// 	}
// 	sb.WriteString(q.table)

// 	if len(q.wheres) > 0 {
// 		sb.WriteString(" WHERE ")
// 		sb.WriteString(strings.Join(q.wheres, " AND "))
// 	}

// 	if method != "count" && method != "exists" {
// 		if len(q.orders) > 0 {
// 			sb.WriteString(" ORDER BY ")
// 			sb.WriteString(strings.Join(q.orders, ", "))
// 		}
// 		if q.limit > 0 {
// 			sb.WriteString(fmt.Sprintf(" LIMIT %d", q.limit))
// 		}
// 		if q.offset > 0 {
// 			sb.WriteString(fmt.Sprintf(" OFFSET %d", q.offset))
// 		}
// 	}

// 	if method == "exists" {
// 		sb.WriteString(")")
// 	}

// 	return sb.String(), q.args
// }

// func (q *QueryBuilder) execute(method string) value.Value {
// 	sqlStr, args := q.buildSQL(method)

// 	if q.db == nil {
// 		return value.New(map[string]any{"error": "no database connection"})
// 	}

// 	switch method {
// 	case "count":
// 		var count int64
// 		if err := q.db.QueryRow(sqlStr, args...).Scan(&count); err != nil {
// 			return value.New(0)
// 		}
// 		return value.New(count)
// 	case "exists":
// 		var exists bool
// 		if err := q.db.QueryRow(sqlStr, args...).Scan(&exists); err != nil {
// 			return value.New(false)
// 		}
// 		return value.New(exists)
// 	case "first":
// 		rows, err := q.db.Query(sqlStr, args...)
// 		if err != nil {
// 			return value.Value{K: value.Nil}
// 		}
// 		defer rows.Close()
// 		if rows.Next() {
// 			return q.scanRow(rows)
// 		}
// 		return value.Value{K: value.Nil}
// 	default: // list
// 		rows, err := q.db.Query(sqlStr, args...)
// 		if err != nil {
// 			return value.New([]any{})
// 		}
// 		defer rows.Close()
// 		var res []value.Value
// 		for rows.Next() {
// 			res = append(res, q.scanRow(rows))
// 		}
// 		return value.New(res)
// 	}
// }

// func (q *QueryBuilder) executeMutator(method string, args ...value.Value) value.Value {
// 	if q.db == nil {
// 		return value.New(map[string]any{"error": "no database connection"})
// 	}

// 	switch method {
// 	case "create":
// 		if len(args) == 0 || !args[0].IsMap() {
// 			return value.New(map[string]any{"error": "create requires a map of values"})
// 		}
// 		data := args[0].Map()
// 		cols := make([]string, 0, len(data))
// 		placeholders := make([]string, 0, len(data))
// 		vals := make([]any, 0, len(data))
// 		for k, v := range data {
// 			cols = append(cols, k)
// 			placeholders = append(placeholders, "?")
// 			vals = append(vals, v.Interface())
// 		}
// 		sqlStr := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s) RETURNING *", q.table, strings.Join(cols, ", "), strings.Join(placeholders, ", "))
// 		rows, err := q.db.Query(sqlStr, vals...)
// 		if err != nil {
// 			return value.New(map[string]any{"error": err.Error()})
// 		}
// 		defer rows.Close()
// 		if rows.Next() {
// 			return q.scanRow(rows)
// 		}
// 		return value.Value{K: value.Nil}

// 	case "update":
// 		if len(args) == 0 || !args[0].IsMap() {
// 			return value.New(map[string]any{"error": "update requires a map of values"})
// 		}
// 		if len(q.wheres) == 0 {
// 			return value.New(map[string]any{"error": "update requires a where clause for safety"})
// 		}
// 		data := args[0].Map()
// 		sets := make([]string, 0, len(data))
// 		vals := make([]any, 0, len(data))
// 		for k, v := range data {
// 			sets = append(sets, fmt.Sprintf("%s = ?", k))
// 			vals = append(vals, v.Interface())
// 		}
// 		vals = append(vals, q.args...) // Add where clause args
// 		sqlStr := fmt.Sprintf("UPDATE %s SET %s WHERE %s RETURNING *", q.table, strings.Join(sets, ", "), strings.Join(q.wheres, " AND "))
// 		rows, err := q.db.Query(sqlStr, vals...)
// 		if err != nil {
// 			return value.New(map[string]any{"error": err.Error()})
// 		}
// 		defer rows.Close()
// 		if rows.Next() {
// 			return q.scanRow(rows)
// 		}
// 		return value.Value{K: value.Nil}

// 	case "delete":
// 		// Soft delete usually means UPDATE deleted_at = NOW()
// 		if len(q.wheres) == 0 {
// 			return value.New(map[string]any{"error": "delete requires a where clause"})
// 		}
// 		sqlStr := fmt.Sprintf("UPDATE %s SET deleted_at = NOW() WHERE %s", q.table, strings.Join(q.wheres, " AND "))
// 		res, err := q.db.Exec(sqlStr, q.args...)
// 		if err != nil {
// 			return value.New(map[string]any{"error": err.Error()})
// 		}
// 		affected, _ := res.RowsAffected()
// 		return value.New(map[string]any{"rows_affected": affected})

// 	case "destroy":
// 		if len(q.wheres) == 0 {
// 			return value.New(map[string]any{"error": "destroy requires a where clause"})
// 		}
// 		sqlStr := fmt.Sprintf("DELETE FROM %s WHERE %s", q.table, strings.Join(q.wheres, " AND "))
// 		res, err := q.db.Exec(sqlStr, q.args...)
// 		if err != nil {
// 			return value.New(map[string]any{"error": err.Error()})
// 		}
// 		affected, _ := res.RowsAffected()
// 		return value.New(map[string]any{"rows_affected": affected})
// 	}

// 	return value.Value{K: value.Nil}
// }

// func (q *QueryBuilder) scanRow(rows *sql.Rows) value.Value {
// 	cols, _ := rows.Columns()
// 	values := make([]any, len(cols))
// 	ptrs := make([]any, len(cols))
// 	for i := range values {
// 		ptrs[i] = &values[i]
// 	}
// 	if err := rows.Scan(ptrs...); err != nil {
// 		return value.Value{K: value.Nil}
// 	}
// 	m := make(map[string]value.Value)
// 	for i, name := range cols {
// 		// handle basic types from SQL
// 		m[name] = value.New(values[i])
// 	}
// 	return value.New(m)
// }

// // Proxies for Magic Lambdas

// type RowProxy struct {
// 	table string
// }

// func (r *RowProxy) OnGet(key string) value.Value {
// 	return value.Value{K: value.Proxy, V: &ColumnProxy{name: key}}
// }
// func (r *RowProxy) OnInvoke(m string, a ...value.Value) value.Value    { return value.Value{} }
// func (r *RowProxy) OnCompare(op string, other value.Value) value.Value { return value.Value{} }

// type ColumnProxy struct {
// 	name string
// }

// func (c *ColumnProxy) OnGet(key string) value.Value { return value.Value{} }
// func (c *ColumnProxy) OnInvoke(m string, a ...value.Value) value.Value {
// 	return value.Value{}
// }
// func (c *ColumnProxy) OnCompare(op string, other value.Value) value.Value {
// 	sqlOp := op
// 	if op == "==" {
// 		sqlOp = "="
// 	}

// 	// Logic IN if other is array
// 	if other.K == value.Array {
// 		arr := other.Array()
// 		placeholders := make([]string, len(arr))
// 		args := make([]any, len(arr))
// 		for i, v := range arr {
// 			placeholders[i] = "?"
// 			args[i] = v.Interface()
// 		}
// 		return value.Value{K: value.Proxy, V: &ConditionProxy{
// 			SQL:  fmt.Sprintf("%s IN (%s)", c.name, strings.Join(placeholders, ", ")),
// 			Args: args,
// 		}}
// 	}

// 	// Logic LIKE if other is string with %
// 	if other.K == value.String {
// 		str := other.String()
// 		if strings.Contains(str, "%") {
// 			return value.Value{K: value.Proxy, V: &ConditionProxy{
// 				SQL:  fmt.Sprintf("%s LIKE ?", c.name),
// 				Args: []any{str},
// 			}}
// 		}
// 	}

// 	return value.Value{K: value.Proxy, V: &ConditionProxy{
// 		SQL:  fmt.Sprintf("%s %s ?", c.name, sqlOp),
// 		Args: []any{other.Interface()},
// 	}}
// }

// type ConditionProxy struct {
// 	SQL  string
// 	Args []any
// }

// func (c *ConditionProxy) OnGet(key string) value.Value                    { return value.Value{} }
// func (c *ConditionProxy) OnInvoke(m string, a ...value.Value) value.Value { return value.Value{} }
// func (c *ConditionProxy) OnCompare(op string, other value.Value) value.Value {
// 	return value.Value{}
// }
