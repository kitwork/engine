package main

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStandaloneComparison(t *testing.T) {
	dir := t.TempDir()
	w, err := createWorkload(dir, 257, 128, 256, 1, 4)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"kitdb", "sqlite-go"} {
		t.Run(name, func(t *testing.T) {
			dbdir := filepath.Join(dir, name)
			if err := os.Mkdir(dbdir, 0700); err != nil {
				t.Fatal(err)
			}
			if name == "kitdb" {
				reports, err := runKit(context.Background(), dbdir, w)
				if err != nil {
					t.Fatal(err)
				}
				if len(reports) != 3 || len(reports[2].Queries) != 9 {
					t.Fatal("missing profiles/queries")
				}
			} else {
				if _, err := runSQLite(context.Background(), dbdir, w); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestGroupedSQLDifferentialWithSQLite(t *testing.T) {
	dir := t.TempDir()
	w, err := createWorkload(dir, 2051, 128, 2048, 1, 4)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	sqlite, err := openSQLite(filepath.Join(dir, "reference.sqlite"), 4)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlite.close()
	kit, err := openKit(filepath.Join(dir, "data.kitdb"), "kitdb-analytics", 4, false)
	if err != nil {
		t.Fatal(err)
	}
	defer kit.close()
	if _, err := ingest(ctx, sqlite, w); err != nil {
		t.Fatal(err)
	}
	if _, err := ingest(ctx, kit, w); err != nil {
		t.Fatal(err)
	}
	if _, err := kit.RefreshAnalytics(ctx); err != nil {
		t.Fatal(err)
	}
	if err := kit.close(); err != nil {
		t.Fatal(err)
	}
	queries := []string{
		`SELECT merchant,enabled,COUNT(*) AS n,COUNT(price),SUM(price),AVG(rating),MIN(price),MAX(rating) FROM products GROUP BY merchant,enabled ORDER BY merchant,enabled`,
		`SELECT price,COUNT(*) AS n,SUM(rating) AS total FROM products WHERE rating >= 1 AND enabled = true GROUP BY price HAVING n >= 1 AND total > 2 ORDER BY total DESC,price LIMIT 17 OFFSET 3`,
		`SELECT price,COUNT(*) FROM products WHERE price IS NULL GROUP BY price`,
		`SELECT merchant FROM products GROUP BY merchant ORDER BY merchant DESC LIMIT 7 OFFSET 2`,
		`SELECT enabled,COUNT(*) FROM products WHERE price < 0 GROUP BY enabled`,
	}
	for _, mode := range []string{"kitdb-row", "kitdb-batch", "kitdb-analytics"} {
		kit, err := openKit(filepath.Join(dir, "data.kitdb"), mode, 4, false)
		if err != nil {
			t.Fatal(err)
		}
		defer kit.close()
		for _, query := range queries {
			want, err := sqlite.execute(ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			got, err := kit.execute(ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			if err := checkRows(got.rows, want.rows); err != nil {
				t.Fatalf("%s %s: %v", mode, query, err)
			}
			if mode != "kitdb-row" {
				covered := uint64(0)
				if got.stats != nil {
					covered = got.stats.RowsScanned + got.stats.RowsSkipped + got.stats.RowsFromMetadata
					if got.stats.Path == "index-only-group" {
						covered = got.stats.IndexEntriesScanned
					}
				}
				if covered != uint64(w.Rows) {
					t.Fatalf("query did not use a bounded batch/index path: %+v", got.stats)
				}
			}
		}
		if err := kit.close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReferenceAndComparator(t *testing.T) {
	p := fixtureProduct(0)
	if p.price != nil || p.rating != nil || !p.enabled {
		t.Fatal(p)
	}
	q := cases(128, false)
	if q[2].Want[0][0] != int64(128) || q[5].Want[0][0] != int64(8) || q[5].Want[0][1] != nil {
		t.Fatal(q)
	}
	if err := checkRows([][]any{{int64(2), nil, true}}, [][]any{{float64(2), nil, int64(1)}}); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][][]any{{{3, nil, true}}, {{2, 0, true}}, {{2, nil}}, nil} {
		if checkRows(bad, [][]any{{2, nil, true}}) == nil {
			t.Fatalf("accepted mismatch: %v", bad)
		}
	}
}

func TestFixtureRefusesOverwrite(t *testing.T) {
	dir := t.TempDir()
	if _, err := createWorkload(dir, 128, 128, 128, 1, 4); err != nil {
		t.Fatal(err)
	}
	if _, err := createWorkload(dir, 128, 128, 128, 1, 4); err == nil {
		t.Fatal("overwrote fixture")
	}
}

func TestBenchmarkClock(t *testing.T) {
	start := benchmarkNow()
	time.Sleep(5 * time.Millisecond)
	if benchmarkSince(start) <= time.Millisecond {
		t.Fatal("clock did not advance")
	}
}

func TestNativeReportCompleteness(t *testing.T) {
	w := workload{Rows: 128, Repetitions: 2, Queries: cases(128, false), AfterUpdate: cases(128, true)[3]}
	r := report{Engine: "duckdb", Rows: 128, Version: "test"}
	for _, q := range w.Queries {
		r.Queries = append(r.Queries, timing{Name: q.Name, SQL: q.SQL, Rows: q.Want, SamplesMS: []float64{1, 2}})
	}
	after := w.AfterUpdate
	r.Queries = append(r.Queries, timing{Name: "aggregate_after_update", SQL: after.SQL, Rows: after.Want, SamplesMS: []float64{1}})
	if err := validateNative(r, w, "duckdb"); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"missing", "duplicate", "sample", "identity"} {
		bad := r
		bad.Queries = append([]timing(nil), r.Queries...)
		switch kind {
		case "missing":
			bad.Queries = bad.Queries[1:]
		case "duplicate":
			bad.Queries[0] = bad.Queries[1]
		case "sample":
			bad.Queries[0].SamplesMS = []float64{math.NaN(), 1}
		case "identity":
			bad.Rows++
		}
		if validateNative(bad, w, "duckdb") == nil {
			t.Fatal("accepted", kind)
		}
	}
}

func TestNativeComparison(t *testing.T) {
	python := os.Getenv("DBCOMPARE_PYTHON")
	if python == "" {
		t.Skip("set DBCOMPARE_PYTHON and optional DBCOMPARE_PYTHON_SITE for native adapters")
	}
	dir := t.TempDir()
	w, err := createWorkload(dir, 257, 128, 256, 2, 4)
	if err != nil {
		t.Fatal(err)
	}
	for _, engine := range []string{"sqlite-native", "duckdb"} {
		t.Run(engine, func(t *testing.T) {
			dbdir := filepath.Join(dir, engine)
			if err := os.Mkdir(dbdir, 0700); err != nil {
				t.Fatal(err)
			}
			r, err := runNative(context.Background(), python, "native.py", os.Getenv("DBCOMPARE_PYTHON_SITE"), engine, dbdir, filepath.Join(dir, "workload.json"))
			if err != nil {
				t.Fatal(err)
			}
			if err := validateNative(r, w, engine); err != nil {
				t.Fatal(err)
			}
		})
	}
}
