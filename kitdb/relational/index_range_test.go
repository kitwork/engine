package relational

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	kitdbengine "github.com/kitwork/engine/kitdb"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

func indexRangeFixture(t *testing.T) *Engine {
	t.Helper()
	e, err := Open(filepath.Join(t.TempDir(), "ranges.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := e.Close(); err != nil {
			t.Error(err)
		}
	})
	for _, table := range []string{"items", "reference"} {
		projectionExecute(t, e, "CREATE TABLE "+table+" (id INTEGER PRIMARY KEY, bucket INTEGER, score INTEGER, label TEXT, weight FLOAT)")
		for start := 0; start < 360; start += 60 {
			var sql strings.Builder
			fmt.Fprintf(&sql, "INSERT INTO %s VALUES ", table)
			for i := start; i < start+60; i++ {
				if i > start {
					sql.WriteByte(',')
				}
				score := fmt.Sprint(i/9 - 20)
				if i%17 == 0 {
					score = "NULL"
				}
				fmt.Fprintf(&sql, "(%d,%d,%s,'label-%03d',%.2f)", i, i%3, score, i%31, float64(i%37-18)/4)
			}
			projectionExecute(t, e, sql.String())
		}
	}
	projectionExecute(t, e, "CREATE INDEX items_bucket_score ON items (bucket,score,id)")
	projectionExecute(t, e, "CREATE INDEX items_label ON items (label,id)")
	projectionExecute(t, e, "CREATE INDEX items_weight ON items (weight,id)")
	if _, err := e.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	return e
}

func TestStandaloneIndexRangeSeeksAndMatchesScan(t *testing.T) {
	e := indexRangeFixture(t)
	cases := []struct {
		where, order string
		args         []any
	}{
		{"bucket = 1 AND score >= -7 AND score < 11", "score,id", nil},
		{"bucket = 1 AND score > -7 AND score <= 11", "score DESC,id DESC", nil},
		{"bucket = 1 AND score >= 0 AND score >= 5 AND score > 5 AND score < 10 AND score <= 7", "score,id", nil},
		{"bucket = 1 AND score >= 7 AND score <= 7", "score,id", nil},
		{"bucket = 1 AND score > 7 AND score <= 7", "score DESC,id DESC", nil},
		{"bucket = 1 AND score > 9 AND score < -9", "score,id", nil},
		{"bucket = 1 AND score < -12", "score DESC,id DESC", nil},
		{"bucket = 1 AND score >= $1 AND score < $2 AND label = 'label-007'", "score,id", []any{int64(-15), int64(17)}},
		{"label > 'label-010' AND label <= 'label-015'", "label,id", nil},
		{"weight >= -0.5 AND weight < 0.5", "weight DESC,id DESC", nil},
		{"bucket = 1 AND score < NULL", "score,id", nil},
		{"score >= 7 AND score < 9", "score,id", nil},
		{"bucket = 1 AND id > 200", "score,id", nil},
		{"bucket = 1 AND (score < -18 OR score > 17)", "score,id", nil},
		{"(bucket = 1 AND score >= 7) OR bucket = 2", "score,id", nil},
		{"bucket = 1 AND NOT (score >= 7)", "score,id", nil},
	}
	for _, c := range cases {
		t.Run(c.where+"/"+c.order, func(t *testing.T) {
			for _, page := range []string{"", " LIMIT 7 OFFSET 2"} {
				suffix := " WHERE " + c.where + " ORDER BY " + c.order + page
				got, err := e.Execute(context.Background(), "SELECT id,bucket,score FROM items"+suffix, c.args...)
				if err != nil {
					t.Fatal(err)
				}
				want, err := e.Execute(context.Background(), "SELECT id,bucket,score FROM reference"+suffix, c.args...)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got.Rows, want.Rows) {
					t.Fatalf("indexed=%v scan=%v", got.Rows, want.Rows)
				}
			}
		})
	}
	access := testSelectAccess(t, e, "SELECT id FROM items WHERE bucket = 1 AND score >= 7 AND score < 9 ORDER BY score,id LIMIT 7")
	if access.kind != rowAccessSecondary || access.name != "items_bucket_score" || len(access.options.Start) == 0 || len(access.options.End) == 0 {
		t.Fatalf("bounded index was not selected: %+v", access)
	}
	tx, err := e.BeginTransaction(context.Background(), TransactionOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	visited := 0
	err = tx.walkAccessRows(access, func(_, encoded []byte) (bool, error) {
		row, err := decodeRow(access.schema, encoded)
		if err != nil {
			return false, err
		}
		score := row.values["score"]
		if score == nil || score.(int64) < 7 || score.(int64) >= 9 {
			t.Errorf("hydrated row outside range: %v", row.values)
		}
		visited++
		return false, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if visited == 0 || visited > 6 {
		t.Fatalf("range hydrated %d rows, want 1..6", visited)
	}
	explained := projectionExecute(t, e, "EXPLAIN SELECT id FROM items WHERE bucket=1 AND score>=7 AND score<9 ORDER BY score DESC,id DESC LIMIT 7")
	for _, part := range []string{"equality_prefix=1", "range=score", "direction=reverse", "order=index"} {
		if !strings.Contains(explained.Rows[0][2].(string), part) {
			t.Fatalf("missing %s in %v", part, explained.Rows)
		}
	}
}

func TestStandaloneIndexRangeOverlayAndSnapshot(t *testing.T) {
	e := indexRangeFixture(t)
	ctx := context.Background()
	old, err := e.BeginTransaction(ctx, TransactionOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer old.Rollback()
	query := "SELECT id,score FROM items WHERE bucket = 1 AND score >= 3 AND score <= 7 ORDER BY score,id"
	before, err := old.Execute(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"items", "reference"} {
		projectionExecute(t, e, "UPDATE "+table+" SET score = 5 WHERE id = 1")
		projectionExecute(t, e, "UPDATE "+table+" SET score = -40 WHERE id = 217")
		projectionExecute(t, e, "DELETE FROM "+table+" WHERE id = 226")
		projectionExecute(t, e, "INSERT INTO "+table+" VALUES (1001,1,7,'overlay',1.0)")
	}
	after, err := old.Execute(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before.Rows, after.Rows) {
		t.Fatal("old snapshot changed")
	}
	writer, err := e.BeginTransaction(ctx, TransactionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"items", "reference"} {
		if _, err := writer.Execute(ctx, "UPDATE "+table+" SET score = 6 WHERE id = 4"); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Execute(ctx, "DELETE FROM "+table+" WHERE id = 235"); err != nil {
			t.Fatal(err)
		}
	}
	for _, order := range []string{"score,id", "score DESC,id DESC"} {
		q := "SELECT id,score FROM items WHERE bucket = 1 AND score >= 3 AND score <= 7 ORDER BY " + order + " LIMIT 9 OFFSET 1"
		got, err := writer.Execute(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		want, err := writer.Execute(ctx, strings.Replace(q, "FROM items", "FROM reference", 1))
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got.Rows, want.Rows) {
			t.Fatalf("overlay: %v != %v", got.Rows, want.Rows)
		}
	}
	if err := writer.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := old.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	got := projectionExecute(t, e, query)
	want := projectionExecute(t, e, strings.Replace(query, "FROM items", "FROM reference", 1))
	if !reflect.DeepEqual(got.Rows, want.Rows) {
		t.Fatal("checkpoint changed range results")
	}
}

func TestStandaloneOrderedLimitStopsBeforeNextRow(t *testing.T) {
	e, err := Open(filepath.Join(t.TempDir(), "stop.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	projectionExecute(t, e, "CREATE TABLE items (id INTEGER PRIMARY KEY, score INTEGER)")
	projectionExecute(t, e, "INSERT INTO items VALUES (1,1),(2,2),(3,3)")
	tx, err := e.BeginTransaction(context.Background(), TransactionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	schema, err := tx.schema("items")
	if err != nil {
		t.Fatal(err)
	}
	poison := func(id int64) {
		key, err := rowKey(schema, map[string]any{"id": id}, 0)
		if err != nil {
			t.Fatal(err)
		}
		if err := tx.Put(key, []byte("deliberately invalid row encoding")); err != nil {
			t.Fatal(err)
		}
	}
	poison(2)
	for _, order := range []string{"id", "id DESC"} {
		r, err := tx.Execute(context.Background(), "SELECT id FROM items ORDER BY "+order+" LIMIT 1")
		if err != nil || len(r.Rows) != 1 {
			t.Fatalf("read past LIMIT: %+v %v", r, err)
		}
	}
	if _, err := tx.Execute(context.Background(), "SELECT id FROM items ORDER BY id LIMIT 2"); err == nil {
		t.Fatal("invalid visited row was not checked")
	}
	poison(1)
	if r, err := tx.Execute(context.Background(), "SELECT id FROM items ORDER BY id LIMIT 0"); err != nil || len(r.Rows) != 0 {
		t.Fatalf("LIMIT 0 read rows: %+v %v", r, err)
	}
	if _, err := tx.Execute(context.Background(), "SELECT missing FROM items LIMIT 0"); err == nil {
		t.Fatal("LIMIT 0 skipped field validation")
	}
}

func TestStandaloneRangeDoesNotUseDecimalByteOrder(t *testing.T) {
	e, err := Open(filepath.Join(t.TempDir(), "decimal.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	projectionExecute(t, e, "CREATE TABLE amounts (id INTEGER PRIMARY KEY, bucket INTEGER, amount DECIMAL)")
	projectionExecute(t, e, "INSERT INTO amounts VALUES (1,1,2),(2,1,11),(3,1,100)")
	projectionExecute(t, e, "CREATE INDEX amount_idx ON amounts (bucket,amount)")
	access := testSelectAccess(t, e, "SELECT id FROM amounts WHERE bucket=1 AND amount>9")
	if len(access.options.Start) != 0 || len(access.options.End) != 0 {
		t.Fatal("decimal byte range is not numeric order")
	}
	r := projectionExecute(t, e, "SELECT id FROM amounts WHERE bucket=1 AND amount>9 ORDER BY id")
	if !reflect.DeepEqual(r.Rows, [][]any{{int64(2)}, {int64(3)}}) {
		t.Fatal(r.Rows)
	}
}

func TestStandaloneIndexRangeMutations(t *testing.T) {
	e := indexRangeFixture(t)
	for _, statement := range []string{
		"UPDATE %s SET score=score+100 WHERE bucket=1 AND score>=3 AND score<9",
		"DELETE FROM %s WHERE bucket=1 AND score>=104 AND score<108",
	} {
		got := projectionExecute(t, e, fmt.Sprintf(statement, "items"))
		want := projectionExecute(t, e, fmt.Sprintf(statement, "reference"))
		if got.Affected != want.Affected || got.Affected == 0 {
			t.Fatalf("affected: %d != %d", got.Affected, want.Affected)
		}
		actual := projectionExecute(t, e, "SELECT id,score FROM items ORDER BY id")
		expected := projectionExecute(t, e, "SELECT id,score FROM reference ORDER BY id")
		if !reflect.DeepEqual(actual.Rows, expected.Rows) {
			t.Fatal("range mutation differs from reference scan")
		}
	}
}

func TestStandaloneIndexRangeEscapedText(t *testing.T) {
	e, err := Open(filepath.Join(t.TempDir(), "text.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	values := []any{nil, "", "a", "a\x00", "a\x00b", "aa", "b", "\u00e9", "\u4e16\u754c"}
	for _, table := range []string{"items", "reference"} {
		projectionExecute(t, e, "CREATE TABLE "+table+" (id INTEGER PRIMARY KEY, label TEXT)")
		for i, value := range values {
			if _, err := e.Execute(context.Background(), "INSERT INTO "+table+" VALUES ($1,$2)", int64(i), value); err != nil {
				t.Fatal(err)
			}
		}
	}
	projectionExecute(t, e, "CREATE INDEX label_idx ON items (label,id)")
	for _, value := range values[1:] {
		for _, op := range []string{"<", "<=", ">", ">="} {
			suffix := " WHERE label " + op + " $1 ORDER BY label DESC,id DESC"
			got, err := e.Execute(context.Background(), "SELECT id,label FROM items"+suffix, value)
			if err != nil {
				t.Fatal(err)
			}
			want, err := e.Execute(context.Background(), "SELECT id,label FROM reference"+suffix, value)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.Rows, want.Rows) {
				t.Fatalf("%s %q: %v != %v", op, value, got.Rows, want.Rows)
			}
		}
	}
}

func verifyScalarRange(t *testing.T, kind string, lower, upper, probe any, lowerInclusive, upperInclusive bool) {
	t.Helper()
	field := kitdbsql.Field{ID: "value", Name: "value", Kind: kind}
	lo, hi := ">", "<"
	if lowerInclusive {
		lo = ">="
	}
	if upperInclusive {
		hi = "<="
	}
	conditions := []boundCondition{{field: field, operator: lo, value: lower}, {field: field, operator: hi, value: upper}}
	options := kitdbengine.RangeOptions{Prefix: []byte{0x30, 0, 0xff}}
	bounded, err := boundSecondaryIndexRange(&options, field, conditions)
	if err != nil || !bounded {
		t.Fatalf("bound: %t %v", bounded, err)
	}
	component, err := orderedScalarComponent(probe)
	if err != nil {
		t.Fatal(err)
	}
	key := append(append([]byte(nil), options.Prefix...), component...)
	// Include a suffix: strict endpoints must exclude every row sharing a value.
	key = append(key, 0, 0xff, 1)
	want := matchesAll(map[string]any{"value": probe}, conditions)
	if got := recordKeyInRange(key, options); got != want {
		t.Fatalf("range %v %s x %s %v, x=%v: bytes=%t SQL=%t", lower, lo, hi, upper, probe, got, want)
	}
}

func TestStandaloneIndexRangeIntegerAndBooleanBoundaries(t *testing.T) {
	const maximum = int64(1<<53 - 1)
	for _, c := range []struct {
		kind   string
		values []any
	}{
		{"integer", []any{-maximum, -maximum + 1, int64(-1), int64(0), int64(1), maximum - 1, maximum}},
		{"bool", []any{false, true}},
	} {
		for _, lo := range c.values {
			for _, hi := range c.values {
				for _, probe := range append([]any{nil}, c.values...) {
					for _, includeLo := range []bool{false, true} {
						for _, includeHi := range []bool{false, true} {
							verifyScalarRange(t, c.kind, lo, hi, probe, includeLo, includeHi)
						}
					}
				}
			}
		}
	}
}

func FuzzStandaloneNumericIndexRange(f *testing.F) {
	f.Add(-1.0, 1.0, 0.0, true, false)
	f.Add(0.0, 0.0, math.Copysign(0, -1), true, true)
	f.Add(-math.MaxFloat64, math.MaxFloat64, math.SmallestNonzeroFloat64, false, true)
	f.Add(9.0, -9.0, 1.0, false, false)
	f.Fuzz(func(t *testing.T, lo, hi, probe float64, includeLo, includeHi bool) {
		for _, v := range []float64{lo, hi, probe} {
			if math.IsNaN(v) || math.IsInf(v, 0) {
				t.Skip()
			}
		}
		verifyScalarRange(t, "float", lo, hi, probe, includeLo, includeHi)
		verifyScalarRange(t, "float", lo, hi, nil, includeLo, includeHi)
	})
}

func FuzzStandaloneTextIndexRange(f *testing.F) {
	f.Add("a", "a\x00b", "a\x00", true, false)
	f.Add("", "\xff", "", false, true)
	f.Add("z", "a", "m", true, true)
	f.Fuzz(func(t *testing.T, lo, hi, probe string, includeLo, includeHi bool) {
		if len(lo)+len(hi)+len(probe) > 2048 {
			t.Skip()
		}
		verifyScalarRange(t, "text", lo, hi, probe, includeLo, includeHi)
		verifyScalarRange(t, "text", lo, hi, nil, includeLo, includeHi)
	})
}
