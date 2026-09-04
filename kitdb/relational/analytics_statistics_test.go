package relational

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kitwork/engine/kitdb/columnar"
)

func TestAnalyticsBlockStatisticsSkipAnswerAndScan(t *testing.T) {
	engine, err := OpenWithOptions(filepath.Join(t.TempDir(), "data.kitdb"), Options{ExperimentalProjections: true})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	ctx := context.Background()
	if _, err := engine.Execute(ctx, `CREATE TABLE metrics (id INTEGER PRIMARY KEY, value INTEGER NOT NULL, enabled BOOLEAN NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	const rows = 3 * 1024
	for start := 0; start < rows; start += 128 {
		var query strings.Builder
		query.WriteString(`INSERT INTO metrics (id,value,enabled) VALUES `)
		for row := start; row < min(start+128, rows); row++ {
			if row != start {
				query.WriteByte(',')
			}
			value, enabled := 10, false
			switch {
			case row < 1024:
			case row < 2048:
				value, enabled = 100, true
			case row < 2560:
				value, enabled = 90, true
			default:
				value, enabled = 110, true
			}
			fmt.Fprintf(&query, "(%d,%d,%t)", row, value, enabled)
		}
		if _, err := engine.Execute(ctx, query.String()); err != nil {
			t.Fatal(err)
		}
	}
	query := `SELECT COUNT(*), SUM(value), AVG(value), MIN(value), MAX(value) FROM metrics WHERE value >= 100 AND enabled = true`
	engine.experimentalProjections, engine.batchAggregates = false, false
	want := projectionExecute(t, engine, query)
	engine.experimentalProjections, engine.batchAggregates = true, true
	if _, err := engine.RefreshAnalytics(ctx); err != nil {
		t.Fatal(err)
	}
	got := projectionExecute(t, engine, query)
	if !reflect.DeepEqual(got.Rows, want.Rows) || got.Execution == nil || got.Execution.Path != "kcol-batch" {
		t.Fatalf("analytics result: got=%+v stats=%+v want=%+v", got.Rows, got.Execution, want.Rows)
	}
	if got.Execution.RowsSkipped != 1024 || got.Execution.BatchesSkipped != 1 ||
		got.Execution.RowsFromMetadata != 1024 || got.Execution.BatchesFromMetadata != 1 ||
		got.Execution.RowsScanned != 1024 || got.Execution.Batches != 1 {
		t.Fatalf("analytics block ladder: %+v", got.Execution)
	}

	all := `SELECT COUNT(value), SUM(value), AVG(value), MIN(value), MAX(value) FROM metrics`
	engine.experimentalProjections, engine.batchAggregates = false, false
	want = projectionExecute(t, engine, all)
	engine.experimentalProjections, engine.batchAggregates = true, true
	got = projectionExecute(t, engine, all)
	if !reflect.DeepEqual(got.Rows, want.Rows) || got.Execution.RowsScanned != 0 || got.Execution.RowsFromMetadata != rows || got.Execution.BatchesFromMetadata != 3 {
		t.Fatalf("full metadata aggregate: got=%+v stats=%+v want=%+v", got.Rows, got.Execution, want.Rows)
	}
}

func TestAnalyticsBlockCoveragePreservesSQLNullAndRangeSemantics(t *testing.T) {
	partial := columnar.ColumnStatistics{Field: columnar.Field{Tag: 1, Kind: columnar.Integer}, Nulls: 2, HasValue: true, IntegerMin: 10, IntegerMax: 20}
	full := partial
	full.Nulls = 0
	constant := full
	constant.IntegerMax = 10
	allNull := partial
	allNull.Nulls, allNull.HasValue = 4, false
	for _, test := range []struct {
		name       string
		statistics columnar.ColumnStatistics
		filter     batchFilter
		want       batchCoverage
	}{
		{"is-null-partial", partial, batchFilter{operator: "is null"}, batchCoveragePartial},
		{"is-not-null-partial", partial, batchFilter{operator: "is not null"}, batchCoveragePartial},
		{"all-null", allNull, batchFilter{operator: "is null"}, batchCoverageAll},
		{"all-null-comparison", allNull, batchFilter{operator: "=", integer: 10}, batchCoverageNone},
		{"null-literal", full, batchFilter{operator: "=", null: true}, batchCoverageNone},
		{"below-equality", full, batchFilter{operator: "=", integer: 5}, batchCoverageNone},
		{"range-equality", full, batchFilter{operator: "=", integer: 15}, batchCoveragePartial},
		{"constant-equality", constant, batchFilter{operator: "=", integer: 10}, batchCoverageAll},
		{"constant-inequality", constant, batchFilter{operator: "!=", integer: 10}, batchCoverageNone},
		{"outside-inequality", full, batchFilter{operator: "!=", integer: 5}, batchCoverageAll},
		{"nullable-outside-inequality", partial, batchFilter{operator: "!=", integer: 5}, batchCoveragePartial},
		{"less-none", full, batchFilter{operator: "<", integer: 10}, batchCoverageNone},
		{"less-all", full, batchFilter{operator: "<", integer: 21}, batchCoverageAll},
		{"less-equal-partial", full, batchFilter{operator: "<=", integer: 10}, batchCoveragePartial},
		{"greater-none", full, batchFilter{operator: ">", integer: 20}, batchCoverageNone},
		{"greater-all", full, batchFilter{operator: ">", integer: 9}, batchCoverageAll},
		{"greater-equal-partial", full, batchFilter{operator: ">=", integer: 20}, batchCoveragePartial},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := batchFilterCoverage(test.statistics, 4, test.filter); got != test.want {
				t.Fatalf("coverage=%d, want %d", got, test.want)
			}
		})
	}
}
