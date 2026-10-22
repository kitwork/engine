package query

import (
	"context"
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"github.com/kitwork/engine/value"
)

// recordingExecutor keeps what a query sent and answers nothing.
type recordingExecutor struct {
	sql  []string
	args [][]any
}

func (r *recordingExecutor) QueryContext(_ context.Context, text string, args ...any) (*sql.Rows, error) {
	r.sql, r.args = append(r.sql, text), append(r.args, args)
	return nil, sql.ErrNoRows
}

func (r *recordingExecutor) ExecContext(_ context.Context, text string, args ...any) (sql.Result, error) {
	r.sql, r.args = append(r.sql, text), append(r.args, args)
	return nil, sql.ErrNoRows
}

// A grouped predicate (WherePredicate) reaches the SQL of every statement the query renders: a read, an
// update, a delete. Parentheses keep its grouping, NOT and IS NULL are written as such, an empty IN is
// 1=0 and an empty NOT IN 1=1, and every value is an argument, never text of the statement. A delete
// whose only condition is a predicate is not refused as one without WHERE.
func TestWhereClauseRendersPredicate(t *testing.T) {
	leaf := func(column, operator string, item any) Predicate {
		return Predicate{Kind: PredicateCondition, Condition: Condition{Column: column, Operator: operator, Value: item}}
	}
	injection := "' OR 1=1 --"
	predicate := Predicate{Kind: PredicateAnd, Children: []Predicate{
		leaf("day", "=", value.New(injection)),
		{Kind: PredicateOr, Children: []Predicate{
			leaf("n", "<", value.New(5)),
			{Kind: PredicateNot, Children: []Predicate{leaf("note", "is null", nil)}},
		}},
		leaf("id", "in", value.New([]value.Value{})),
		leaf("id", "not in", value.New([]value.Value{})),
	}}
	query := NewQuery(nil, nil).Table("orders").WherePredicate(predicate)
	text, args := query.getSQL()
	want := `SELECT * FROM "orders" WHERE ("day" = $1 AND ("n" < $2 OR NOT ("note" IS NULL)) AND 1=0 AND 1=1) LIMIT 60`
	if text != want {
		t.Fatalf("SQL:\n%s\nwant\n%s", text, want)
	}
	if !reflect.DeepEqual(args, []any{injection, float64(5)}) {
		t.Fatalf("arguments %#v", args)
	}
	if strings.Contains(text, "OR 1=1 --") {
		t.Fatalf("a value is in the statement's text: %s", text)
	}

	// flat conditions and a predicate together, numbered on
	both := NewQuery(nil, nil).Table("orders").WherePredicate(leaf("id", "in", value.New([]value.Value{value.New("a"), value.New("b")})))
	both.and("day", "=", "2026-10-22")
	text, args = both.getSQL()
	if want := `SELECT * FROM "orders" WHERE ("day" = $1) AND "id" IN ($2, $3) LIMIT 60`; text != want ||
		!reflect.DeepEqual(args, []any{"2026-10-22", "a", "b"}) {
		t.Fatalf("SQL %s %#v", text, args)
	}

	// an update and a delete with a predicate alone
	recorder := &recordingExecutor{}
	update := New(recorder, nil).Table("orders").WherePredicate(leaf("id", "=", value.New("a")))
	update.update(map[string]value.Value{"note": value.New("x")})
	remove := New(recorder, nil).Table("orders").WherePredicate(leaf("id", "=", value.New("a")))
	refused := remove.remove()
	if len(recorder.sql) != 2 || recorder.sql[0] != `UPDATE "orders" SET "note" = $1 WHERE "id" = $2 RETURNING *` ||
		recorder.sql[1] != `DELETE FROM "orders" WHERE "id" = $1` {
		t.Fatalf("statements sent: %q (delete gave %v)", recorder.sql, refused)
	}
	if !reflect.DeepEqual(recorder.args[1], []any{"a"}) {
		t.Fatalf("delete arguments %#v", recorder.args[1])
	}
}
