package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kitwork/engine/kitdb"
)

type decodedResponse struct {
	Format  string          `json:"format"`
	Command string          `json:"command"`
	Result  json.RawMessage `json:"result"`
}

func TestOperatorVersionReportsKitDBV1Contract(t *testing.T) {
	response := runAndDecode(t, "version")
	if response.Format != commandFormat || response.Command != "version" {
		t.Fatalf("version envelope = %#v", response)
	}
	var profile kitdb.CompatibilityProfile
	if err := json.Unmarshal(response.Result, &profile); err != nil {
		t.Fatal(err)
	}
	if profile != kitdb.CurrentCompatibility() ||
		profile.ReleaseTarget != "1.0.0" || profile.Stability != "release-candidate" {
		t.Fatalf("version profile = %#v", profile)
	}

	var output bytes.Buffer
	if err := run(context.Background(), []string{"version", "unexpected"}, &output); err == nil {
		t.Fatal("version accepted an argument")
	}
}

func TestOperatorCommandsInspectVerifyBackupAndRestore(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.kitdb")
	database, err := kitdb.OpenWithOptions(source, kitdb.OpenOptions{RetainHistory: true})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Put([]byte("product/1"), []byte("first")); err != nil {
		t.Fatal(err)
	}
	committed, err := tx.Commit()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	inspect := runAndDecode(t, "inspect", source)
	if inspect.Format != commandFormat || inspect.Command != "inspect" {
		t.Fatalf("inspect envelope = %#v", inspect)
	}
	var inspected inspectResult
	if err := json.Unmarshal(inspect.Result, &inspected); err != nil {
		t.Fatal(err)
	}
	if inspected.DatabaseID == "" || inspected.Stats.LastTransaction != committed ||
		inspected.CatalogVersion.Transaction != committed ||
		inspected.CatalogVersion.Revision == "" || !inspected.Stats.HistoryEnabled {
		t.Fatalf("inspect result = %#v", inspected)
	}

	catalogResponse := runAndDecode(t, "catalog", source)
	var catalog kitdb.CatalogSnapshot
	if err := json.Unmarshal(catalogResponse.Result, &catalog); err != nil {
		t.Fatal(err)
	}
	if catalog.Transaction != committed || catalog.Revision != inspected.CatalogVersion.Revision {
		t.Fatalf("catalog result = %#v", catalog)
	}

	verified := runAndDecode(t, "verify", source)
	var verification verifyResult
	if err := json.Unmarshal(verified.Result, &verification); err != nil {
		t.Fatal(err)
	}
	if !verification.Verified || verification.DatabaseID != inspected.DatabaseID {
		t.Fatalf("verify result = %#v", verification)
	}

	anchorPath := filepath.Join(root, "anchor.kitdb")
	backedUp := runAndDecode(t, "backup", "--pin", "backup/test", source, anchorPath)
	var backup backupResult
	if err := json.Unmarshal(backedUp.Result, &backup); err != nil {
		t.Fatal(err)
	}
	if backup.Anchor.Transaction != committed || backup.Pin.Cursor.Transaction != committed ||
		backup.Anchor.DatabaseID != inspected.DatabaseID {
		t.Fatalf("backup result = %#v", backup)
	}

	restoredPath := filepath.Join(root, "restored.kitdb")
	restored := runAndDecode(t, "restore", anchorPath, restoredPath)
	var restoration kitdb.RestoreResult
	if err := json.Unmarshal(restored.Result, &restoration); err != nil {
		t.Fatal(err)
	}
	if restoration.Transaction != committed || restoration.DatabaseID != inspected.DatabaseID {
		t.Fatalf("restore result = %#v", restoration)
	}
	reopened, err := kitdb.Open(restoredPath)
	if err != nil {
		t.Fatal(err)
	}
	value, found, getErr := reopened.Get([]byte("product/1"))
	if getErr != nil || !found || string(value) != "first" {
		_ = reopened.Close()
		t.Fatalf("restored value = (%q, %t, %v)", value, found, getErr)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestOperatorCommandsNeverCreateMissingSource(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.kitdb")
	var output bytes.Buffer
	err := run(context.Background(), []string{"inspect", missing}, &output)
	if err == nil {
		t.Fatal("inspect accepted a missing database")
	}
	if _, statErr := os.Stat(missing); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("missing source stat = %v", statErr)
	}
	if output.Len() != 0 {
		t.Fatalf("failed inspect output = %q", output.String())
	}
}

func TestOperatorQueryCreatesAndUsesStandaloneRelationalDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "query.kitdb")
	created := runAndDecode(t,
		"query", "--create", path,
		`CREATE TABLE products (id INTEGER PRIMARY KEY, title TEXT NOT NULL)`,
	)
	var createResult queryResult
	if err := json.Unmarshal(created.Result, &createResult); err != nil {
		t.Fatal(err)
	}
	if createResult.CommandTag != "CREATE TABLE" {
		t.Fatalf("CREATE result = %#v", createResult)
	}
	inserted := runAndDecode(t,
		"query", "--params", `[1,"Keyboard"]`, path,
		`INSERT INTO products (id, title) VALUES ($1, $2) RETURNING id, title`,
	)
	var insertResult queryResult
	if err := json.Unmarshal(inserted.Result, &insertResult); err != nil {
		t.Fatal(err)
	}
	if insertResult.Affected != 1 || len(insertResult.Rows) != 1 || insertResult.Rows[0][1] != "Keyboard" {
		t.Fatalf("INSERT result = %#v", insertResult)
	}
	selected := runAndDecode(t, "query", path, `SELECT title FROM products WHERE id = 1`)
	var selectResult queryResult
	if err := json.Unmarshal(selected.Result, &selectResult); err != nil {
		t.Fatal(err)
	}
	if len(selectResult.Rows) != 1 || selectResult.Rows[0][0] != "Keyboard" {
		t.Fatalf("SELECT result = %#v", selectResult)
	}
	expression := runAndDecode(t, "query", path, `SELECT id + 1 AS next_id, UPPER(title) AS title FROM products`)
	var expressionResult queryResult
	if err := json.Unmarshal(expression.Result, &expressionResult); err != nil {
		t.Fatal(err)
	}
	if len(expressionResult.Rows) != 1 || expressionResult.Rows[0][0] != float64(2) ||
		expressionResult.Rows[0][1] != "KEYBOARD" {
		t.Fatalf("expression result = %#v", expressionResult)
	}

	var output bytes.Buffer
	if err := run(context.Background(), []string{
		"query", "--readonly", path, `DELETE FROM products WHERE id = 1`,
	}, &output); err == nil {
		t.Fatal("read-only query accepted DELETE")
	}
}

func TestOperatorRestoreTimeUsesDurableCommitBoundary(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.kitdb")
	database, err := kitdb.OpenWithOptions(source, kitdb.OpenOptions{RetainHistory: true})
	if err != nil {
		t.Fatal(err)
	}
	putCommandValue(t, database, "product/1", "before")
	if _, err := database.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	var first kitdb.CommitEvent
	if _, err := database.WalkHistory(context.Background(), 0, func(event kitdb.CommitEvent) error {
		first = event
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	putCommandValue(t, database, "product/2", "after")
	if _, err := database.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	destination := filepath.Join(root, "as-of.kitdb")
	response := runAndDecode(
		t,
		"restore-time",
		"--at",
		first.CommittedAt.Format(time.RFC3339Nano),
		source,
		destination,
	)
	var restored kitdb.TimeRestoreResult
	if err := json.Unmarshal(response.Result, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.Transaction != first.Transaction {
		t.Fatalf("time restore transaction = %d, want %d", restored.Transaction, first.Transaction)
	}
	reopened, err := kitdb.Open(destination)
	if err != nil {
		t.Fatal(err)
	}
	if value, found, err := reopened.Get([]byte("product/1")); err != nil ||
		!found || string(value) != "before" {
		_ = reopened.Close()
		t.Fatalf("restored first value = (%q, %t, %v)", value, found, err)
	}
	if value, found, err := reopened.Get([]byte("product/2")); err != nil || found {
		_ = reopened.Close()
		t.Fatalf("future value = (%q, %t, %v), want absent", value, found, err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
}

func putCommandValue(t *testing.T, database *kitdb.DB, key, stored string) uint64 {
	t.Helper()
	transaction, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := transaction.Put([]byte(key), []byte(stored)); err != nil {
		t.Fatal(err)
	}
	committed, err := transaction.Commit()
	if err != nil {
		t.Fatal(err)
	}
	return committed
}

func runAndDecode(t *testing.T, args ...string) decodedResponse {
	t.Helper()
	var output bytes.Buffer
	if err := run(context.Background(), args, &output); err != nil {
		t.Fatalf("kitdb %v: %v", args, err)
	}
	var response decodedResponse
	if err := json.Unmarshal(output.Bytes(), &response); err != nil {
		t.Fatalf("decode %q: %v", output.String(), err)
	}
	return response
}
