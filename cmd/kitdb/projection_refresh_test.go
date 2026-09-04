package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/kitwork/engine/kitdb"
	"github.com/kitwork/engine/kitdb/relational"
)

func TestOperatorRefreshAnalyticsOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.kitdb")
	runAndDecode(t, "query", "--create", path, `CREATE TABLE products (id INTEGER PRIMARY KEY, name TEXT SEARCHABLE, price INTEGER)`)
	runAndDecode(t, "query", path, `INSERT INTO products VALUES (1, 'blue widget', 10), (2, 'green widget', 20)`)
	response := runAndDecode(t, "refresh-projections", "--analytics-only", path)
	var report relational.ProjectionReport
	if err := json.Unmarshal(response.Result, &report); err != nil {
		t.Fatal(err)
	}
	if report.AnalyticsFile != path+".analytics" || report.AnalyticsTables != 1 || report.SearchFile != "" || report.SearchTables != 0 {
		t.Fatalf("analytics report: %+v", report)
	}
	if _, err := os.Stat(path + ".search"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("analytics-only CLI created search: %v", err)
	}
	selected := runAndDecode(t, "query", "--experimental-projections", path, `SELECT SUM(price) FROM products`)
	var result queryResult
	if err := json.Unmarshal(selected.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.Execution == nil || result.Execution.Path != "kcol-batch" || len(result.Rows) != 1 || result.Rows[0][0] != float64(30) {
		t.Fatalf("columnar query: %+v", result)
	}
	response = runAndDecode(t, "refresh-projections", path)
	if err := json.Unmarshal(response.Result, &report); err != nil {
		t.Fatal(err)
	}
	if report.AnalyticsTables != 1 || report.SearchTables != 1 || report.SearchFile != path+".search" {
		t.Fatalf("default refresh no longer builds both files: %+v", report)
	}
}

func TestOperatorRefreshAnalyticsAllowsLegacySearchDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.kitdb")
	runAndDecode(t, "query", "--create", path, `CREATE TABLE products (id INTEGER PRIMARY KEY, price INTEGER)`)
	if err := os.Mkdir(path+".search", 0700); err != nil {
		t.Fatal(err)
	}
	runAndDecode(t, "refresh-projections", "--analytics-only", path)
	if stat, err := os.Stat(path + ".search"); err != nil || !stat.IsDir() {
		t.Fatalf("legacy search directory changed: %v", err)
	}
	var output bytes.Buffer
	if err := run(context.Background(), []string{"refresh-projections", path}, &output); err == nil {
		t.Fatal("default refresh accepted legacy search directory")
	}
}

func TestOperatorRefreshAnalyticsReusesRetainedChunks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.kitdb")
	runAndDecode(t, "query", "--create", path, `CREATE TABLE products (id INTEGER PRIMARY KEY, price INTEGER)`)
	runAndDecode(t, "query", path, `INSERT INTO products VALUES (1, 10), (2, 20)`)
	runAndDecode(t, "query", path, `CREATE TABLE clicks (id INTEGER PRIMARY KEY, visits INTEGER)`)
	runAndDecode(t, "query", path, `INSERT INTO clicks VALUES (1, 5)`)
	database, err := relational.OpenWithOptions(path, relational.Options{Kernel: kitdb.OpenOptions{RetainHistory: true}})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	runAndDecode(t, "refresh-projections", "--analytics-only", path)
	runAndDecode(t, "query", path, `UPDATE clicks SET visits = 6 WHERE id = 1`)
	response := runAndDecode(t, "refresh-projections", "--analytics-only", path)
	var report relational.ProjectionReport
	if err := json.Unmarshal(response.Result, &report); err != nil {
		t.Fatal(err)
	}
	if report.AnalyticsFallback != "" || report.AnalyticsSourceRows != 1 || report.AnalyticsReusedRows != 2 || report.AnalyticsBuiltChunks != 1 || report.AnalyticsReusedChunks != 1 || report.AnalyticsCopiedBytes != 0 || report.AnalyticsReferencedBytes == 0 || report.AnalyticsPublication != "append" {
		t.Fatalf("CLI did not reuse existing retained history: %+v", report)
	}
}

func TestOperatorRefreshAnalyticsRejectsInvalidArguments(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.kitdb")
	for _, args := range [][]string{
		{"refresh-projections", "--analytics-only"},
		{"refresh-projections", "--analytics-only", missing},
		{"refresh-projections", "--analytics-only", missing, "extra"},
		{"refresh-projections", "--unknown", missing},
	} {
		var output bytes.Buffer
		if err := run(context.Background(), args, &output); err == nil || output.Len() != 0 {
			t.Fatalf("invalid arguments accepted: %v, output=%s, error=%v", args, output.String(), err)
		}
	}
	if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("refresh created missing source: %v", err)
	}
}
