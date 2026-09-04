package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/kitwork/engine/kitdb/relational"
)

func TestReplayQueriesChecksResultsWithoutCommitting(t *testing.T) {
	dir := t.TempDir()
	w, err := createWorkload(dir, 257, 128, 256, 2, 4)
	if err != nil {
		t.Fatal(err)
	}
	dbdir := filepath.Join(dir, "kitdb")
	if err := os.Mkdir(dbdir, 0700); err != nil {
		t.Fatal(err)
	}
	dbpath := filepath.Join(dbdir, "data.kitdb")
	db, err := openKit(dbpath, "kitdb-row", 4, false)
	if err != nil {
		t.Fatal(err)
	}
	_, ingestErr := ingest(context.Background(), db, w)
	closeErr := db.close()
	if ingestErr != nil || closeErr != nil {
		t.Fatalf("ingest: %v, close: %v", ingestErr, closeErr)
	}
	state := func() uint64 {
		t.Helper()
		db, err := openKit(dbpath, "kitdb-row", 4, false)
		if err != nil {
			t.Fatal(err)
		}
		defer db.close()
		tx, err := db.BeginTransaction(context.Background(), relational.TransactionOptions{ReadOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		q := cases(w.Rows, false)[3]
		result, err := tx.Execute(context.Background(), q.SQL)
		if err != nil {
			t.Fatal(err)
		}
		if err := checkRows(result.Rows, q.Want); err != nil {
			t.Fatal(err)
		}
		return tx.BaseTransaction()
	}
	before := state()
	procs := runtime.GOMAXPROCS(0)
	output := filepath.Join(dir, "replay")
	if err := replayQueries(dir, output, "lookup,indexed_page,count_all", false, true, 2, 4, time.Minute); err != nil {
		t.Fatal(err)
	}
	if runtime.GOMAXPROCS(0) != procs {
		t.Fatal("replay leaked process concurrency setting")
	}
	encoded, err := os.ReadFile(filepath.Join(output, "results.json"))
	if err != nil {
		t.Fatal(err)
	}
	var r report
	if err := json.Unmarshal(encoded, &r); err != nil {
		t.Fatal(err)
	}
	if r.Error != "" || r.Rows != w.Rows || len(r.Queries) != 3 || len(r.Plans) != 3 || len(r.Phases) != 0 {
		t.Fatalf("incomplete replay report: %+v", r)
	}
	for i, q := range r.Queries {
		if len(q.SamplesMS) != 2 || q.GoAllocatedBytes == nil {
			t.Fatalf("missing measurements: %+v", q)
		}
		if err := checkRows(q.Rows, w.Queries[i].Want); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"cpu.pprof", "allocs.pprof"} {
		info, err := os.Stat(filepath.Join(output, name))
		if err != nil || info.Size() == 0 {
			t.Fatalf("missing profile %s: %v", name, err)
		}
	}
	if err := replayQueries(dir, output, "lookup", false, false, 1, 4, time.Minute); !os.IsExist(err) {
		t.Fatalf("did not reject existing output: %v", err)
	}
	if err := replayQueries(dir, filepath.Join(dir, "unknown"), "DELETE FROM products", false, false, 1, 4, time.Minute); err == nil {
		t.Fatal("accepted a query outside the fixed fixture")
	}
	wrongState := filepath.Join(dir, "wrong-state")
	if err := replayQueries(dir, wrongState, "lookup", true, false, 1, 4, time.Minute); err == nil || !strings.Contains(err.Error(), "want") {
		t.Fatalf("wrong fixture state was accepted: %v", err)
	}
	encoded, err = os.ReadFile(filepath.Join(wrongState, "results.json"))
	if err != nil {
		t.Fatal(err)
	}
	var failed report
	if err := json.Unmarshal(encoded, &failed); err != nil || failed.Error == "" {
		t.Fatalf("failure missing from report: %v", err)
	}
	if after := state(); after != before {
		t.Fatalf("replay committed source mutations: %d -> %d", before, after)
	}
}

func TestReplayGroupedModesRequireActualPath(t *testing.T) {
	dir := t.TempDir()
	w, err := createWorkload(dir, 257, 128, 256, 1, 4)
	if err != nil {
		t.Fatal(err)
	}
	dbdir := filepath.Join(dir, "kitdb")
	if err := os.Mkdir(dbdir, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := runKit(context.Background(), dbdir, w); err != nil {
		t.Fatal(err)
	}
	for mode, path := range map[string]string{"kitdb-row": "scalar-or-index", "kitdb-batch": "krow-batch", "kitdb-analytics": "kcol-batch"} {
		output := filepath.Join(dir, mode)
		if err := replayQueriesMode(dir, output, "group_merchant", mode, true, false, 1, 4, time.Minute); err != nil {
			t.Fatal(err)
		}
		encoded, err := os.ReadFile(filepath.Join(output, "results.json"))
		if err != nil {
			t.Fatal(err)
		}
		var r report
		if err := json.Unmarshal(encoded, &r); err != nil {
			t.Fatal(err)
		}
		if r.Engine != mode || len(r.Queries) != 1 || r.Queries[0].Path != path {
			t.Fatalf("wrong mode or path: %+v", r)
		}
		stats := r.Queries[0].Execution
		if mode != "kitdb-row" && (stats == nil || stats.Groups != 64 || stats.RowsScanned != 257) {
			t.Fatalf("missing group evidence: %+v", stats)
		}
	}
	if err := replayQueriesMode(dir, filepath.Join(dir, "bad-mode"), "group_merchant", "unknown", true, false, 1, 4, time.Minute); err == nil {
		t.Fatal("unknown replay mode accepted")
	}
	projection := filepath.Join(dbdir, "data.kitdb.analytics")
	if err := os.Rename(projection, projection+".parked"); err != nil {
		t.Fatal(err)
	}
	if err := replayQueriesMode(dir, filepath.Join(dir, "missing-analytics"), "group_merchant", "kitdb-analytics", true, false, 1, 4, time.Minute); err == nil || !strings.Contains(err.Error(), "expected KCOL") {
		t.Fatalf("scalar fallback was measured as columnar: %v", err)
	}
}
