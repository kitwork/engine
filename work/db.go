package work

import (
	"context"
	"fmt"
	"log"

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
	return value.Value{K: value.Nil}
}

type LambdaExecutor interface {
	ExecuteLambda(fn *value.ScriptFunction, args []value.Value) value.Value
}

type DBQuery struct {
	table      string
	limit      int
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
						argCount := len(q.whereArgs) + 1
						q.conditions = append(q.conditions, fmt.Sprintf("\"%s\" %s $%d", filter.Column, filter.Operator, argCount))
						q.whereArgs = append(q.whereArgs, filter.Value.Interface())
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

func (q *DBQuery) Insert(data value.Value) *DBQuery {
	fmt.Printf("[DB] (Mock) INSERT INTO %s | Data: %s\n", q.table, data.Text())
	return q
}

func (q *DBQuery) Get() value.Value {
	db := GetDB()
	if db == nil {
		return q.mockGet()
	}

	query := fmt.Sprintf("SELECT * FROM \"%s\"", q.table)

	if len(q.conditions) > 0 {
		query += " WHERE "
		for i, cond := range q.conditions {
			if i > 0 {
				query += " AND "
			}
			query += cond
		}
	}

	if q.limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", q.limit)
	} else {
		query += " LIMIT 60"
	}

	// DEBUG LOG
	log.Printf("[DB] Executing SQL: %s | Args: %v\n", query, q.whereArgs)

	rows, err := db.QueryContext(context.Background(), query, q.whereArgs...)

	if err != nil {
		log.Printf("[DB] Query Error: %v | SQL: %s\n", err, query)
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
