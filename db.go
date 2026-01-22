package engine

import (
	"fmt"

	"github.com/kitwork/engine/value"
)

// DBQuery là builder cho database query
type DBQuery struct {
	table  string
	limit  int
	method string // "select", "insert", etc.
}

func NewDBQuery() *DBQuery {
	return &DBQuery{method: "select"}
}

func (q *DBQuery) From(table string) *DBQuery {
	q.table = table
	return q
}

func (q *DBQuery) Take(n float64) *DBQuery {
	q.limit = int(n)
	return q
}

func (q *DBQuery) Limit(n float64) *DBQuery {
	return q.Take(n)
}

func (q *DBQuery) Get() value.Value {
	// Giả lập trả về dữ liệu mẫu dựa trên table
	res := make([]value.Value, 0)
	for i := 1; i <= q.limit; i++ {
		row := make(map[string]value.Value)
		row["id"] = value.New(i)
		row["name"] = value.New(fmt.Sprintf("%s_item_%d", q.table, i))
		res = append(res, value.New(row))
	}
	return value.New(res)
}

// ToSQL trả về chuỗi SQL (để debug)
func (q *DBQuery) ToSQL() string {
	limitStr := ""
	if q.limit > 0 {
		limitStr = fmt.Sprintf(" LIMIT %d", q.limit)
	}
	return fmt.Sprintf("SELECT * FROM %s%s", q.table, limitStr)
}

// String implementation cho Value.Text()
func (q *DBQuery) String() string {
	return q.ToSQL()
}
