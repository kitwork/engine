package relational

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/kitwork/engine/kitdb/pgwire"
)

func TestStandaloneArithmeticParameters(t *testing.T) {
	engine, err := Open(filepath.Join(t.TempDir(), "parameters.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	ctx := context.Background()
	for _, source := range []string{
		`CREATE TABLE counters (id BIGINT PRIMARY KEY, total BIGINT, fraction DOUBLE PRECISION, label TEXT)`,
		`INSERT INTO counters (id,total,fraction,label) VALUES (1,10,1.5,'name')`,
	} {
		if _, err := engine.Execute(ctx, source); err != nil {
			t.Fatal(err)
		}
	}
	parameters, err := postgresParameters([]pgwire.Parameter{{Data: []byte("7")}, {Data: []byte("1")}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, `UPDATE counters SET total = total + $1 WHERE id = $2`, parameters...); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		source string
		args   []any
		want   any
	}{
		{`SELECT total + $1 FROM counters`, []any{"3"}, int64(20)},
		{`SELECT $1 - total FROM counters`, []any{"30"}, int64(13)},
		{`SELECT (total + $1) * $2 FROM counters`, []any{"3", "2"}, int64(40)},
		{`SELECT CAST($1 AS BIGINT) + $2 FROM counters`, []any{"3", "2"}, int64(5)},
		{`SELECT fraction + $1 FROM counters`, []any{"0.25"}, float64(1.75)},
		{`SELECT CAST(total AS DOUBLE PRECISION) + $1 FROM counters`, []any{float64(0.5)}, float64(17.5)},
		{`SELECT total + $1 FROM counters`, []any{nil}, nil},
	} {
		result, err := engine.Execute(ctx, test.source, test.args...)
		if err != nil || len(result.Rows) != 1 || len(result.Rows[0]) != 1 || result.Rows[0][0] != test.want {
			t.Fatalf("%s: rows=%v err=%v want=%v", test.source, result.Rows, err, test.want)
		}
	}
	for _, invalid := range []string{"not a number", "1.5", "9223372036854775807", "9223372036854775808"} {
		if _, err := engine.Execute(ctx, `UPDATE counters SET total = total + $1`, invalid); err == nil {
			t.Fatalf("accepted invalid bigint parameter %q", invalid)
		}
	}
	if _, err := engine.Execute(ctx, `UPDATE counters SET total = total + $1`, float64(0.5)); err == nil {
		t.Fatal("silently truncated a typed fractional parameter in an integer expression")
	}
	for _, source := range []string{`SELECT total + '3' FROM counters`, `SELECT label + $1 FROM counters`, `SELECT $1 + $2 FROM counters`} {
		if _, err := engine.Execute(ctx, source, "3", "4"); err == nil {
			t.Fatalf("guessed numeric type without context: %s", source)
		}
	}
	transaction, err := engine.BeginTransaction(ctx, TransactionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer transaction.Rollback()
	if _, err := transaction.Execute(ctx, `UPDATE counters SET total = $1 + total`, "3"); err != nil {
		t.Fatal(err)
	}
	result, err := transaction.Execute(ctx, `SELECT total FROM counters`)
	if err != nil || result.Rows[0][0] != int64(20) {
		t.Fatalf("transaction arithmetic: %v %v", result.Rows, err)
	}
	if err := transaction.Rollback(); err != nil {
		t.Fatal(err)
	}
	result, err = engine.Execute(ctx, `SELECT total FROM counters`)
	if err != nil || result.Rows[0][0] != int64(17) {
		t.Fatalf("invalid parameters or rollback changed counter: %v %v", result.Rows, err)
	}
}
