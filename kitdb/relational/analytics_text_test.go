package relational

import (
	"context"
	"reflect"
	"testing"

	"github.com/kitwork/engine/internal/snapshotfile"
	"github.com/kitwork/engine/kitdb/columnar"
)

func TestAnalyticsDictionaryTextMatchesScalarAcrossKROWAndKCOL(t *testing.T) {
	engine := projectionTestDatabase(t, 2507)
	ctx := context.Background()
	projectionExecute(t, engine, `UPDATE products SET name = NULL WHERE id = 0`)
	queries := []string{
		`SELECT name,COUNT(*) AS n,MIN(id) AS lo,MAX(id) AS hi FROM products WHERE name >= 'blue widget 3' AND name < 'blue widget 8' GROUP BY name ORDER BY name`,
		`SELECT COUNT(name),MIN(name),MAX(name) FROM products WHERE name != 'blue widget 4'`,
		`SELECT name,enabled,COUNT(*) AS n,SUM(price) AS total FROM products GROUP BY name,enabled ORDER BY name,enabled`,
		`SELECT name,COUNT(*) FROM products GROUP BY name ORDER BY name`,
	}
	engine.batchAggregates, engine.experimentalProjections = false, false
	wants := make([]Result, len(queries))
	for index, query := range queries {
		wants[index] = projectionExecute(t, engine, query)
	}

	engine.batchAggregates = true
	if got := projectionExecute(t, engine, queries[0]); got.Execution != nil {
		t.Fatalf("undeclared text unexpectedly entered the batch path: %+v", got.Execution)
	}
	projectionExecute(t, engine, `ALTER TABLE products ALTER COLUMN name SET ANALYTICS`)
	for index, query := range queries {
		got := projectionExecute(t, engine, query)
		if got.Execution == nil || got.Execution.Path != "krow-batch" || !reflect.DeepEqual(got.Rows, wants[index].Rows) {
			t.Fatalf("KROW text batch mismatch for %s: rows=%v stats=%+v want=%v", query, got.Rows, got.Execution, wants[index].Rows)
		}
	}

	engine.experimentalProjections = true
	if _, err := engine.RefreshAnalytics(ctx); err != nil {
		t.Fatal(err)
	}
	for index, query := range queries {
		got := projectionExecute(t, engine, query)
		if got.Execution == nil || got.Execution.Path != "kcol-batch" || !reflect.DeepEqual(got.Rows, wants[index].Rows) {
			t.Fatalf("KCOL text batch mismatch for %s: rows=%v stats=%+v want=%v", query, got.Rows, got.Execution, wants[index].Rows)
		}
	}

	file, err := snapshotfile.Open(engine.Path() + ".analytics")
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := engine.database.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	schema, err := schemaFromCatalog(catalog, "products")
	if err != nil {
		t.Fatal(err)
	}
	section, err := file.Section(schema.ID)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := columnar.Open(section)
	if err != nil {
		t.Fatal(err)
	}
	foundText := false
	for _, field := range reader.Fields {
		foundText = foundText || field.Kind == columnar.Text
	}
	if reader.FormatVersion() != 3 || !foundText {
		t.Fatalf("analytics format=%d fields=%v", reader.FormatVersion(), reader.Fields)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	reused, err := engine.RefreshAnalytics(ctx)
	if err != nil || reused.AnalyticsPublication != "unchanged" || reused.AnalyticsReusedRows != 2507 {
		t.Fatalf("verified text reuse = %+v, %v", reused, err)
	}
}

func TestStandaloneAlterAnalyticsInvalidatesAndCanRemoveTextProjection(t *testing.T) {
	engine := projectionTestDatabase(t, 64)
	ctx := context.Background()
	projectionExecute(t, engine, `ALTER TABLE products ALTER name SET ANALYTICS`)
	if _, err := engine.RefreshAnalytics(ctx); err != nil {
		t.Fatal(err)
	}
	query := `SELECT name,COUNT(*) FROM products GROUP BY name ORDER BY name`
	if got := projectionExecute(t, engine, query); got.Execution == nil || got.Execution.Path != "kcol-batch" {
		t.Fatalf("declared text did not use KCOL: %+v", got.Execution)
	}
	projectionExecute(t, engine, `ALTER TABLE products ALTER name DROP ANALYTICS`)
	if got := projectionExecute(t, engine, query); got.Execution != nil {
		t.Fatalf("removed text policy remained executable through KCOL: %+v", got.Execution)
	}
	if _, err := engine.Execute(ctx, `ALTER TABLE products ALTER name DROP ANALYTICS`); err == nil {
		t.Fatal("duplicate DROP ANALYTICS unexpectedly succeeded")
	}
}

func TestAnalyticsDictionaryTextKeepsRangePartitionBlockPruning(t *testing.T) {
	engine := projectionTestDatabase(t, 5000)
	ctx := context.Background()
	projectionExecute(t, engine, `ALTER TABLE products ALTER name SET ANALYTICS`)
	projectionExecute(t, engine, `ALTER TABLE products SET PARTITION BY RANGE (price)`)
	query := `SELECT name,COUNT(*) FROM products WHERE price >= 90 AND name >= 'blue widget 0' GROUP BY name ORDER BY name`
	engine.batchAggregates, engine.experimentalProjections = false, false
	want := projectionExecute(t, engine, query)
	engine.batchAggregates, engine.experimentalProjections = true, true
	if _, err := engine.RefreshAnalytics(ctx); err != nil {
		t.Fatal(err)
	}
	got := projectionExecute(t, engine, query)
	if got.Execution == nil || got.Execution.Path != "kcol-batch" || got.Execution.ColumnarBlocksPruned == 0 ||
		!reflect.DeepEqual(got.Rows, want.Rows) {
		t.Fatalf("partitioned text query rows=%v stats=%+v want=%v", got.Rows, got.Execution, want.Rows)
	}
}

func BenchmarkAnalyticsDictionaryTextGroup(b *testing.B) {
	engine := projectionTestDatabase(b, 100_000)
	ctx := context.Background()
	projectionExecute(b, engine, `ALTER TABLE products ALTER name SET ANALYTICS`)
	query := `SELECT name,COUNT(*) AS n,SUM(price) AS total FROM products WHERE name >= 'blue widget 2' AND name < 'blue widget 9' GROUP BY name ORDER BY name`

	for _, mode := range []struct {
		name         string
		batch        bool
		projection   bool
		expectedPath string
	}{
		{name: "scalar"},
		{name: "krow-batch", batch: true, expectedPath: "krow-batch"},
		{name: "kcol-dictionary", batch: true, projection: true, expectedPath: "kcol-batch"},
	} {
		b.Run(mode.name, func(b *testing.B) {
			engine.batchAggregates = mode.batch
			engine.experimentalProjections = mode.projection
			if mode.projection {
				if _, err := engine.RefreshAnalytics(ctx); err != nil {
					b.Fatal(err)
				}
			}
			observed, err := engine.Execute(ctx, query)
			if err != nil {
				b.Fatal(err)
			}
			if mode.expectedPath != "" && (observed.Execution == nil || observed.Execution.Path != mode.expectedPath) {
				b.Fatalf("execution path = %+v, want %s", observed.Execution, mode.expectedPath)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if _, err := engine.Execute(ctx, query); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
