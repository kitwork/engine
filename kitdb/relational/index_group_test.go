package relational

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestIndexOnlyGroupCountUsesOrderedSecondaryIndex(t *testing.T) {
	engine := openIndexOnlyGroupTestEngine(t)
	defer engine.Close()
	ctx := context.Background()
	query := `SELECT merchant, COUNT(*) AS products FROM shopping GROUP BY merchant ORDER BY merchant`

	planned, err := engine.Execute(ctx, "EXPLAIN "+query)
	if err != nil {
		t.Fatal(err)
	}
	if len(planned.Rows) != 1 || planned.Rows[0][1] != "index-only group" ||
		!strings.Contains(planned.Rows[0][2].(string), "index=shopping_merchant_id_idx") {
		t.Fatalf("index-only plan = %#v", planned.Rows)
	}

	got, err := engine.Execute(ctx, "EXPLAIN ANALYZE "+query)
	if err != nil {
		t.Fatal(err)
	}
	want := [][]any{{"lazada", int64(2)}, {"shopee", int64(3)}, {"tiki", int64(1)}}
	result, err := engine.Execute(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.Rows, want) {
		t.Fatalf("index-only rows = %#v, want %#v", result.Rows, want)
	}
	for name, observed := range map[string]Result{"query": result, "analyze": got} {
		if observed.Execution == nil || observed.Execution.Path != "index-only-group" ||
			observed.Execution.IndexEntriesScanned != 6 || observed.Execution.RowsMatched != 6 ||
			observed.Execution.RowsScanned != 0 || observed.Execution.PointLookups != 0 ||
			observed.Execution.Groups != 3 {
			t.Fatalf("%s stats = %+v", name, observed.Execution)
		}
	}
	if got.Execution.GenerationEntriesVisited != 6 || got.Execution.PageRecordsDecoded == 0 {
		t.Fatalf("EXPLAIN ANALYZE physical stats = %+v", got.Execution)
	}
	if !explainDetailContains(got, "rows", "scanned=0 matched=6 groups=3") {
		t.Fatalf("EXPLAIN ANALYZE rows = %#v", got.Rows)
	}
	keysOnly, err := engine.Execute(ctx, `SELECT merchant FROM shopping GROUP BY merchant ORDER BY merchant`)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(keysOnly.Rows, [][]any{{"lazada"}, {"shopee"}, {"tiki"}}) ||
		keysOnly.Execution == nil || keysOnly.Execution.Path != "index-only-group" {
		t.Fatalf("grouped keys = %+v", keysOnly)
	}
}

func TestIndexOnlyGroupCountReadsTransactionOverlay(t *testing.T) {
	engine := openIndexOnlyGroupTestEngine(t)
	defer engine.Close()
	ctx := context.Background()
	transaction, err := engine.BeginTransaction(ctx, TransactionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer transaction.Rollback()
	if _, err := transaction.Execute(ctx, `INSERT INTO shopping (merchant,id,payload) VALUES ('shopee',4,'new')`); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Execute(ctx, `DELETE FROM shopping WHERE merchant = 'lazada' AND id = 1`); err != nil {
		t.Fatal(err)
	}
	got, err := transaction.Execute(ctx, `
		SELECT merchant, COUNT(*) AS products
		FROM shopping GROUP BY merchant ORDER BY merchant
	`)
	if err != nil {
		t.Fatal(err)
	}
	want := [][]any{{"lazada", int64(1)}, {"shopee", int64(4)}, {"tiki", int64(1)}}
	if !reflect.DeepEqual(got.Rows, want) || got.Execution == nil ||
		got.Execution.Path != "index-only-group" || got.Execution.IndexEntriesScanned != 6 {
		t.Fatalf("overlay index-only group = %+v", got)
	}
}

func TestIndexOnlyGroupCompositeKeyPreservesHavingAndProjectionOrder(t *testing.T) {
	engine, err := Open(filepath.Join(t.TempDir(), "composite-index-group.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	ctx := context.Background()
	for _, statement := range []string{
		`CREATE TABLE events (
			tenant TEXT NOT NULL,
			kind TEXT NOT NULL,
			id BIGINT NOT NULL,
			PRIMARY KEY (tenant,id)
		)`,
		`CREATE INDEX events_tenant_kind_id_idx ON events (tenant,kind,id)`,
		`INSERT INTO events (tenant,kind,id) VALUES
			('a','click',1), ('a','click',2), ('a','view',3),
			('b','click',1), ('b','click',2), ('b','view',3)`,
	} {
		if _, err := engine.Execute(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	got, err := engine.Execute(ctx, `
		SELECT kind, tenant, COUNT(*) AS visits
		FROM events
		GROUP BY tenant,kind
		HAVING visits >= 2
		ORDER BY visits DESC,tenant,kind
	`)
	if err != nil {
		t.Fatal(err)
	}
	want := [][]any{{"click", "a", int64(2)}, {"click", "b", int64(2)}}
	if !reflect.DeepEqual(got.Rows, want) || got.Execution == nil ||
		got.Execution.Path != "index-only-group" || got.Execution.Groups != 4 ||
		got.Execution.IndexEntriesScanned != 6 {
		t.Fatalf("composite index-only group = %+v, want rows %#v", got, want)
	}
}

func TestCoveringAggregateHandlesShapesBeyondIndexOnlyGroup(t *testing.T) {
	engine := openIndexOnlyGroupTestEngine(t)
	defer engine.Close()
	ctx := context.Background()
	tests := []struct {
		name  string
		query string
	}{
		{name: "count-field", query: `SELECT merchant, COUNT(id) FROM shopping GROUP BY merchant ORDER BY merchant`},
		{name: "sum", query: `SELECT merchant, SUM(id) FROM shopping GROUP BY merchant ORDER BY merchant`},
		{name: "predicate", query: `SELECT merchant, COUNT(*) FROM shopping WHERE id > 1 GROUP BY merchant ORDER BY merchant`},
		{name: "non-prefix", query: `SELECT id, COUNT(*) FROM shopping GROUP BY id ORDER BY id`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := engine.Execute(ctx, test.query)
			if err != nil {
				t.Fatal(err)
			}
			if got.Execution == nil || got.Execution.Path != "index-only-aggregate" ||
				got.Execution.RowsScanned != 0 || got.Execution.PointLookups != 0 {
				t.Fatalf("covering query did not use the wider index-only path: %+v", got.Execution)
			}
		})
	}
}

func TestIndexOnlyGroupHonorsGroupLimit(t *testing.T) {
	engine := openIndexOnlyGroupTestEngine(t)
	defer engine.Close()
	engine.maximumResultRows = 2
	_, err := engine.Execute(context.Background(), `
		SELECT merchant, COUNT(*) FROM shopping GROUP BY merchant ORDER BY merchant
	`)
	if err == nil || !strings.Contains(err.Error(), "2-group limit") {
		t.Fatalf("group limit error = %v", err)
	}
}

func openIndexOnlyGroupTestEngine(t *testing.T) *Engine {
	t.Helper()
	engine, err := Open(filepath.Join(t.TempDir(), "index-group.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	statements := []string{
		`CREATE TABLE shopping (
			merchant TEXT NOT NULL,
			id BIGINT NOT NULL,
			payload TEXT NOT NULL,
			PRIMARY KEY (merchant,id)
		)`,
		`CREATE INDEX shopping_merchant_id_idx ON shopping (merchant,id)`,
		`INSERT INTO shopping (merchant,id,payload) VALUES
			('shopee',2,'two'), ('lazada',2,'two'), ('tiki',1,'one'),
			('shopee',1,'one'), ('lazada',1,'one'), ('shopee',3,'three')`,
	}
	for _, statement := range statements {
		if _, err := engine.Execute(ctx, statement); err != nil {
			engine.Close()
			t.Fatal(err)
		}
	}
	if _, err := engine.Checkpoint(); err != nil {
		engine.Close()
		t.Fatal(err)
	}
	return engine
}

func explainDetailContains(result Result, operation, fragment string) bool {
	for _, row := range result.Rows {
		if len(row) >= 3 && row[1] == operation {
			detail, _ := row[2].(string)
			return strings.Contains(detail, fragment)
		}
	}
	return false
}
