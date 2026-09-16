package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/kitwork/engine/kitdb/relational"
)

func TestOperatorProjectionPreflightAndAdmission(t *testing.T) {
	path := filepath.Join(t.TempDir(), "status.kitdb")
	runAndDecode(t, "query", "--create", path,
		`CREATE TABLE products (id INTEGER PRIMARY KEY, name TEXT SEARCHABLE, price INTEGER)`)
	runAndDecode(t, "query", path, `INSERT INTO products VALUES (1,'blue widget',10)`)

	response := runAndDecode(t, "projections", path)
	var report relational.ProjectionPreflightReport
	if err := json.Unmarshal(response.Result, &report); err != nil {
		t.Fatal(err)
	}
	if !report.Valid || report.Ready || report.Analytics[0].Status != "missing" || report.Search[0].Status != "missing" {
		t.Fatalf("missing CLI preflight = %+v", report)
	}
	runAndDecode(t, "refresh-projections", path)
	response = runAndDecode(t, "projections", path)
	if err := json.Unmarshal(response.Result, &report); err != nil {
		t.Fatal(err)
	}
	if !report.Valid || !report.Ready || report.Analytics[0].Status != "ready" || report.Search[0].Status != "ready" {
		t.Fatalf("ready CLI preflight = %+v", report)
	}

	runAndDecode(t, "query", path, `UPDATE products SET price = 11 WHERE id = 1`)
	if err := runCommand("query", "--experimental-projections", "--projection-open-policy", "require-ready",
		path, `SELECT SUM(price) FROM products`); err == nil {
		t.Fatal("require-ready query accepted stale projections")
	}
	runAndDecode(t, "query", "--experimental-projections", "--projection-open-policy", "validate",
		path, `SELECT SUM(price) FROM products`)

	if err := os.WriteFile(path+".analytics", []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := runCommand("query", "--experimental-projections", "--projection-open-policy", "validate",
		path, `SELECT SUM(price) FROM products`); err == nil {
		t.Fatal("validate query accepted invalid analytics")
	}
	response = runAndDecode(t, "projections", path)
	if err := json.Unmarshal(response.Result, &report); err != nil {
		t.Fatal(err)
	}
	if report.Valid || report.Ready || report.Analytics[0].Status != "invalid" {
		t.Fatalf("invalid CLI preflight = %+v", report)
	}
	if err := runCommand("query", "--projection-open-policy", "validate", path, `SELECT 1`); err == nil {
		t.Fatal("projection policy accepted without --experimental-projections")
	}
}

func runCommand(arguments ...string) error {
	var output bytes.Buffer
	return run(context.Background(), arguments, &output)
}
