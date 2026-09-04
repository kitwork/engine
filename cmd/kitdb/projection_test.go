package main

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestOperatorExperimentalProjectionFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.kitdb")
	runAndDecode(t, "query", "--create", path, `CREATE TABLE products (id INTEGER PRIMARY KEY, price INTEGER, name TEXT SEARCHABLE)`)
	runAndDecode(t, "query", path, `INSERT INTO products VALUES (1,10,'blue widget'),(2,20,'red widget')`)
	runAndDecode(t, "refresh-projections", path)
	response := runAndDecode(t, "query", "--experimental-projections", path, `SELECT SUM(price) FROM products`)
	var result queryResult
	if err := json.Unmarshal(response.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.Execution == nil || result.Execution.Path != "kcol-batch" || result.Rows[0][0] != float64(30) {
		t.Fatalf("analytics CLI: %+v", result)
	}
	response = runAndDecode(t, "query", "--readonly", "--experimental-projections", path,
		`EXPLAIN ANALYZE SELECT SUM(price) FROM products`)
	if err := json.Unmarshal(response.Result, &result); err != nil {
		t.Fatal(err)
	}
	actual, kcolIO := "", ""
	for _, row := range result.Rows {
		if len(row) == 3 && row[1] == "actual" {
			actual, _ = row[2].(string)
		}
		if len(row) == 3 && row[1] == "kcol_io" {
			kcolIO, _ = row[2].(string)
		}
	}
	if result.Execution == nil || result.Execution.Path != "kcol-batch" ||
		result.Execution.ColumnarBlockHeadersRead == 0 || actual == "" || kcolIO == "" {
		t.Fatalf("EXPLAIN ANALYZE CLI: %+v", result)
	}
	response = runAndDecode(t, "query", "--readonly", path,
		`EXPLAIN ANALYZE SELECT name FROM products WHERE id = 1 LIMIT 1`)
	if err := json.Unmarshal(response.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.Execution == nil ||
		result.Execution.Path != "primary-lookup" ||
		result.Execution.PointLookups != 1 ||
		result.Execution.RowsScanned != 1 ||
		result.Execution.RowsMatched != 1 ||
		result.Execution.PageAccesses != 0 ||
		result.Execution.OverlayEntriesVisited != 1 {
		t.Fatalf("KROW EXPLAIN ANALYZE CLI: %+v", result)
	}
	response = runAndDecode(t, "query", "--experimental-projections", path, `SELECT name FROM products WHERE * SEARCH 'blue' LIMIT 2`)
	if err := json.Unmarshal(response.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.Execution.Path != "search-snapshot" || len(result.Rows) != 1 {
		t.Fatalf("search CLI: %+v", result)
	}
	response = runAndDecode(t, "query", "--batch-aggregates", path, `SELECT SUM(price) FROM products`)
	if err := json.Unmarshal(response.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.Execution.Path != "krow-batch" {
		t.Fatalf("batch CLI: %+v", result)
	}
}
