package relational

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"maps"
	"math/rand"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

// Compare planner claims with the actual durable ordered components, rather
// than using the planner's field-list construction as the expected ordering.
func TestIndexPrimarySuffixOrderSoundness(t *testing.T) {
	fields := []kitdbsql.Field{
		{ID: "bucket", Name: "bucket", Kind: "bigint"},
		{ID: "score", Name: "score", Kind: "bigint"},
		{ID: "tenant", Name: "tenant", Kind: "bigint"},
		{ID: "id", Name: "id", Kind: "bigint"},
	}
	primary := fields[2:]
	random := rand.New(rand.NewSource(19301))
	coveredCases := 0
	for range 500 {
		permutation := random.Perm(len(fields))
		index := secondaryIndex{}
		for _, position := range permutation[:1+random.Intn(3)] {
			index.fields = append(index.fields, fields[position])
		}
		fixed := make(map[string]any)
		for _, field := range fields {
			if random.Intn(3) == 0 {
				fixed[field.Name] = int64(1)
			}
		}
		prefix := 0
		for _, field := range index.fields {
			if _, ok := fixed[field.Name]; !ok {
				break
			}
			prefix++
		}
		var orders []boundOrder
		for _, position := range random.Perm(len(fields))[:1+random.Intn(4)] {
			orders = append(orders, boundOrder{field: fields[position], descending: random.Intn(2) == 0})
		}
		covered, reverse := indexCoversOrder(index, primary, prefix, fixed, orders)
		if !covered {
			continue
		}
		coveredCases++
		type keyed struct {
			key    []byte
			values map[string]any
		}
		var rows []keyed
		for n := 0; n < 81; n++ {
			values := make(map[string]any)
			value := n
			match := true
			for _, field := range fields {
				item := int64(value%3 - 1)
				value /= 3
				values[field.Name] = item
				if expected, ok := fixed[field.Name]; ok && expected != item {
					match = false
				}
			}
			if !match {
				continue
			}
			var key []byte
			for _, group := range [][]kitdbsql.Field{index.fields, primary} {
				for _, field := range group {
					component, err := orderedFieldScalarComponent(field, values[field.Name])
					if err != nil {
						t.Fatal(err)
					}
					key = append(key, component...)
				}
			}
			rows = append(rows, keyed{key, values})
		}
		sort.Slice(rows, func(i, j int) bool {
			comparison := bytes.Compare(rows[i].key, rows[j].key)
			if reverse {
				return comparison > 0
			}
			return comparison < 0
		})
		for i := 1; i < len(rows); i++ {
			for _, order := range orders {
				left, right := rows[i-1].values[order.field.Name].(int64), rows[i].values[order.field.Name].(int64)
				if left == right {
					continue
				}
				if (!order.descending && left > right) || (order.descending && left < right) {
					t.Fatalf("unsound order: index=%v prefix=%d fixed=%v orders=%v reverse=%t", index.fields, prefix, fixed, orders, reverse)
				}
				break
			}
		}
	}
	if coveredCases < 30 {
		t.Fatalf("insufficient covered cases: %d", coveredCases)
	}
}

func TestIndexPrimarySuffixOrder(t *testing.T) {
	field := func(name string) kitdbsql.Field { return kitdbsql.Field{ID: name, Name: name} }
	bucket, tenant, id, score := field("bucket"), field("tenant"), field("id"), field("score")
	for _, tc := range []struct {
		name    string
		fields  []kitdbsql.Field
		fixed   []string
		prefix  int
		orders  []boundOrder
		covered bool
		reverse bool
	}{
		{"ascending", []kitdbsql.Field{bucket}, []string{"bucket"}, 1, []boundOrder{{field: tenant}, {field: id}}, true, false},
		{"descending", []kitdbsql.Field{bucket}, []string{"bucket"}, 1, []boundOrder{{field: tenant, descending: true}, {field: id, descending: true}}, true, true},
		{"mixed", []kitdbsql.Field{bucket}, []string{"bucket"}, 1, []boundOrder{{field: tenant}, {field: id, descending: true}}, false, false},
		{"missing primary leader", []kitdbsql.Field{bucket}, []string{"bucket"}, 1, []boundOrder{{field: id}}, false, false},
		{"fixed primary leader", []kitdbsql.Field{bucket}, []string{"bucket", "tenant"}, 1, []boundOrder{{field: id}}, true, false},
		{"unfixed index leader", []kitdbsql.Field{bucket}, nil, 0, []boundOrder{{field: tenant}, {field: id}}, false, false},
		{"full physical order", []kitdbsql.Field{bucket}, nil, 0, []boundOrder{{field: bucket}, {field: tenant}, {field: id}}, true, false},
		{"varying middle", []kitdbsql.Field{bucket, score}, []string{"bucket"}, 1, []boundOrder{{field: tenant}, {field: id}}, false, false},
		{"fixed middle", []kitdbsql.Field{bucket, score}, []string{"bucket", "score"}, 2, []boundOrder{{field: tenant}, {field: id}}, true, false},
		{"duplicate primary member", []kitdbsql.Field{bucket, tenant}, []string{"bucket"}, 1, []boundOrder{{field: tenant}, {field: id}}, true, false},
		{"reordered primary member", []kitdbsql.Field{bucket, id}, []string{"bucket"}, 1, []boundOrder{{field: id}, {field: tenant}}, true, false},
		{"reordered suffix not primary order", []kitdbsql.Field{bucket, id}, []string{"bucket"}, 1, []boundOrder{{field: tenant}, {field: id}}, false, false},
		{"uncovered field", []kitdbsql.Field{bucket}, []string{"bucket"}, 1, []boundOrder{{field: score}}, false, false},
		{"no order", []kitdbsql.Field{bucket}, []string{"bucket"}, 1, nil, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixed := make(map[string]any)
			for _, name := range tc.fixed {
				fixed[name] = int64(1)
			}
			index := secondaryIndex{fields: tc.fields}
			covered, reverse := indexCoversOrder(index, []kitdbsql.Field{tenant, id}, tc.prefix, fixed, tc.orders)
			if covered != tc.covered || reverse != tc.reverse {
				t.Fatalf("covered/reverse=%t/%t, want %t/%t", covered, reverse, tc.covered, tc.reverse)
			}
		})
	}
}

func TestSQLIndexPrimarySuffixOrder(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "suffix.kitdb")
	engine, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = engine.Close() }()
	exec := func(query string, args ...any) Result {
		t.Helper()
		result, err := engine.Execute(ctx, query, args...)
		if err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		return result
	}
	exec("CREATE TABLE items(tenant TEXT NOT NULL,id BIGINT NOT NULL,bucket BIGINT NOT NULL,score BIGINT NOT NULL,payload TEXT NOT NULL,PRIMARY KEY(tenant,id))")
	exec("CREATE INDEX items_bucket ON items(bucket)")
	type row struct {
		tenant            string
		id, bucket, score int64
		payload           string
	}
	model := make(map[string]row)
	for _, tenant := range []string{"z", "a", "m"} {
		var query strings.Builder
		query.WriteString("INSERT INTO items VALUES ")
		for i := int64(29); i >= -30; i-- {
			if i != 29 {
				query.WriteByte(',')
			}
			item := row{tenant, i, (i + 30) % 3, (i + 30) % 5, fmt.Sprintf("%s:%d", tenant, i)}
			fmt.Fprintf(&query, "('%s',%d,%d,%d,'%s')", tenant, i, item.bucket, item.score, item.payload)
			model[item.payload] = item
		}
		exec(query.String())
	}
	check := func(t *testing.T, runner func(string) (Result, error)) {
		t.Helper()
		for _, tc := range []struct {
			name, where, order                    string
			reverse, residual, fixedTenant, mixed bool
		}{
			{"ascending", "bucket=1", "tenant,id", false, false, false, false},
			{"descending", "bucket=1", "tenant DESC,id DESC", true, false, false, false},
			{"residual", "bucket=1 AND score=4", "tenant,id", false, true, false, false},
			{"fixed tenant", "bucket=1 AND tenant='m'", "id DESC", true, false, true, false},
			{"mixed fallback", "bucket=1", "tenant,id DESC", false, false, false, true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				var want []row
				for _, item := range model {
					if item.bucket == 1 && (!tc.residual || item.score == 4) && (!tc.fixedTenant || item.tenant == "m") {
						want = append(want, item)
					}
				}
				sort.Slice(want, func(i, j int) bool {
					if want[i].tenant != want[j].tenant {
						if tc.reverse {
							return want[i].tenant > want[j].tenant
						}
						return want[i].tenant < want[j].tenant
					}
					if tc.reverse || tc.mixed {
						return want[i].id > want[j].id
					}
					return want[i].id < want[j].id
				})
				query := "SELECT tenant,id,bucket,score,payload FROM items WHERE " + tc.where + " ORDER BY " + tc.order + " LIMIT 5 OFFSET 2"
				result, err := runner(query)
				if err != nil {
					t.Fatal(err)
				}
				var expected [][]any
				for _, item := range want[min(2, len(want)):min(7, len(want))] {
					expected = append(expected, []any{item.tenant, item.id, item.bucket, item.score, item.payload})
				}
				if !reflect.DeepEqual(result.Rows, expected) {
					t.Fatalf("rows=%v, want %v", result.Rows, expected)
				}
				observed, err := runner("EXPLAIN ANALYZE " + query)
				if err != nil || observed.Execution == nil {
					t.Fatalf("observation: %v", err)
				}
				if !tc.mixed && observed.Execution.RowsMatched != 7 {
					t.Fatalf("did not stop after seven matching rows: %+v", observed.Execution)
				}
				if !tc.mixed && !tc.residual && !tc.fixedTenant && observed.Execution.RowsScanned != 7 {
					t.Fatalf("did not stop after offset+limit: %+v", observed.Execution)
				}
				if (tc.residual || tc.fixedTenant) && observed.Execution.RowsScanned <= 7 {
					t.Fatalf("stopped before residual filtering: %+v", observed.Execution)
				}
			})
		}
	}
	run := func(query string) (Result, error) { return engine.Execute(ctx, query) }
	t.Run("WAL", func(t *testing.T) { check(t, run) })
	if _, err := engine.database.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	snapshot, err := engine.BeginTransaction(ctx, TransactionOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Rollback()
	// The old transaction must retain its rows while a newer one updates indexes.
	exec("DELETE FROM items WHERE tenant='a' AND id=-29")
	exec("UPDATE items SET bucket=2 WHERE tenant='m' AND id=-26")
	t.Run("retained snapshot", func(t *testing.T) { check(t, func(q string) (Result, error) { return snapshot.Execute(ctx, q) }) })
	if err := snapshot.Rollback(); err != nil {
		t.Fatal(err)
	}
	delete(model, "a:-29")
	changed := model["m:-26"]
	changed.bucket = 2
	model["m:-26"] = changed
	t.Run("index mutations", func(t *testing.T) { check(t, run) })
	if _, err := engine.database.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}
	engine, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Run("reopen", func(t *testing.T) { check(t, run) })
	t.Run("stream", func(t *testing.T) {
		for _, tail := range []string{
			"WHERE bucket=1 ORDER BY tenant,id LIMIT 5 OFFSET 2",
			"WHERE bucket=1 AND score=4 ORDER BY tenant DESC,id DESC LIMIT 5 OFFSET 2",
			"WHERE bucket=1 ORDER BY tenant,id",
			"WHERE bucket=1 ORDER BY tenant,id LIMIT 0",
		} {
			query := "SELECT tenant,id,payload FROM items " + tail
			want := exec(query)
			stream, handled, err := engine.openSelectRowStream(ctx, query, nil)
			if err != nil || !handled || stream == nil {
				t.Fatalf("stream handled=%t: %v", handled, err)
			}
			var rows [][]any
			for {
				row, err := stream.Next()
				if err == io.EOF {
					break
				}
				if err != nil {
					_ = stream.Close()
					t.Fatal(err)
				}
				rows = append(rows, row)
			}
			if err := stream.Close(); err != nil {
				t.Fatal(err)
			}
			if len(rows) != len(want.Rows) || (len(rows) > 0 && !reflect.DeepEqual(rows, want.Rows)) {
				t.Fatalf("stream %s: %v want %v", tail, rows, want.Rows)
			}
		}
		query := "SELECT tenant,id FROM items WHERE bucket=1 ORDER BY tenant,id DESC LIMIT 5"
		stream, handled, err := engine.openSelectRowStream(ctx, query, nil)
		if stream != nil {
			_ = stream.Close()
		}
		if err != nil || handled {
			t.Fatalf("mixed direction must retain materialized fallback: handled=%t err=%v", handled, err)
		}
	})
	write, err := engine.BeginTransaction(ctx, TransactionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer write.Rollback()
	if _, err := write.Execute(ctx, "UPDATE items SET bucket=2 WHERE tenant='a' AND id=-26"); err != nil {
		t.Fatal(err)
	}
	original := maps.Clone(model)
	changed = model["a:-26"]
	changed.bucket = 2
	model["a:-26"] = changed
	t.Run("own writes", func(t *testing.T) { check(t, func(q string) (Result, error) { return write.Execute(ctx, q) }) })
	if err := write.Rollback(); err != nil {
		t.Fatal(err)
	}
	model = original
	t.Run("rollback", func(t *testing.T) { check(t, run) })
}
