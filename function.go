package engine

import (
	"fmt"
)

// --- 1. MOCK DATA & STRUCTS ---

type User struct {
	ID   float64
	Name string
}

func (u *User) GetInfo() string {
	return fmt.Sprintf("User: %s (ID: %.0f)", u.Name, u.ID)
}

type QueryBuilder struct {
	table string
	limit int
}

func (q *QueryBuilder) From(t string) *QueryBuilder {
	q.table = t
	return q
}

func (q *QueryBuilder) Limit(n float64) *QueryBuilder {
	q.limit = int(n)
	return q
}

func (q *QueryBuilder) Get() string {
	return fmt.Sprintf("SELECT * FROM %s LIMIT %d", q.table, q.limit)
}
