package work

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kitwork/engine/kitdb/pgimport"
	"github.com/kitwork/engine/kitdb/pgwire"
	"github.com/lib/pq"
)

func TestParseKitDBPostgresCopy(t *testing.T) {
	tests := []struct {
		name      string
		source    string
		handled   bool
		wantTable string
		wantCols  string
		wantCSV   bool
		wantDelim byte
		wantNull  string
		wantHead  bool
		wantBulk  bool
		wantError bool
	}{
		{
			name: "text defaults", source: `COPY public.products (id, sku, title, price) FROM STDIN`,
			handled: true, wantTable: "products", wantCols: "[id sku title price]",
			wantDelim: '\t', wantNull: `\N`,
		},
		{
			name: "csv options", source: `COPY "products" (id, title) FROM STDIN WITH
(FORMAT csv, HEADER true, DELIMITER ';', NULL 'NULL');`,
			handled: true, wantTable: "products", wantCols: "[id title]", wantCSV: true,
			wantDelim: ';', wantNull: "NULL", wantHead: true,
		},
		{
			name: "leading comments", source: "-- import\n/* trusted client */ COPY products FROM STDIN",
			handled: true, wantTable: "products", wantCols: "[]", wantDelim: '\t', wantNull: `\N`,
		},
		{
			name: "bounded bulk", source: `COPY products FROM STDIN WITH (KITDB_BULK true)`,
			handled: true, wantTable: "products", wantCols: "[]", wantDelim: '\t', wantNull: `\N`,
			wantBulk: true,
		},
		{name: "ordinary query", source: `SELECT * FROM products`, handled: false},
		{name: "file source", source: `COPY products FROM '/tmp/products.csv'`, handled: true, wantError: true},
		{name: "text header", source: `COPY products FROM STDIN WITH (HEADER true)`, handled: true, wantError: true},
		{name: "binary", source: `COPY products FROM STDIN WITH (FORMAT binary)`, handled: true, wantError: true},
		{name: "text backslash delimiter", source: `COPY products FROM STDIN WITH (DELIMITER '\')`, handled: true, wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spec, handled, err := parseKitDBPostgresCopy(test.source)
			if handled != test.handled {
				t.Fatalf("handled = %v, want %v", handled, test.handled)
			}
			if test.wantError {
				if err == nil {
					t.Fatalf("parse unexpectedly succeeded: %#v", spec)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !handled {
				return
			}
			if spec.table != test.wantTable || fmt.Sprint(spec.columns) != test.wantCols ||
				(spec.format == kitDBPostgresCopyCSV) != test.wantCSV ||
				spec.delimiter != test.wantDelim || spec.null != test.wantNull || spec.header != test.wantHead ||
				spec.bulk != test.wantBulk {
				t.Fatalf("COPY spec = %#v", spec)
			}
		})
	}
}

func TestParseKitDBPostgresResumableCopy(t *testing.T) {
	previous := strings.Repeat("1", 64)
	checksum := strings.Repeat("2", 64)
	source := fmt.Sprintf(`COPY products (id, sku, title, price) FROM STDIN WITH (
KITDB_IMPORT 'catalog-v1', KITDB_SOURCE 'feed-v1', KITDB_SOURCE_FORMAT 'csv',
KITDB_CHUNK 3, KITDB_START 128, KITDB_END 512, KITDB_ROWS 2,
KITDB_PREVIOUS '%s', KITDB_CHECKSUM '%s', KITDB_COMPLETE false)`, previous, checksum)
	spec, handled, err := parseKitDBPostgresCopy(source)
	if err != nil || !handled || spec.progress == nil {
		t.Fatalf("parse resumable COPY: handled=%v spec=%#v err=%v", handled, spec, err)
	}
	if spec.progress.id != "catalog-v1" || spec.progress.source != "feed-v1" ||
		spec.progress.sourceFormat != "csv" || spec.progress.chunk != 3 ||
		spec.progress.start != 128 || spec.progress.end != 512 || spec.progress.rows != 2 ||
		spec.progress.previous != previous || spec.progress.checksum != checksum || spec.progress.complete {
		t.Fatalf("resumable COPY progress = %#v", spec.progress)
	}
	_, _, err = parseKitDBPostgresCopy(strings.Replace(source, ", KITDB_COMPLETE false", "", 1))
	if err == nil || !strings.Contains(err.Error(), "requires every") {
		t.Fatalf("incomplete progress options error = %v", err)
	}
}

func TestKitDBPostgresCopyCSVDecoderStreamsQuotedRecords(t *testing.T) {
	spec := kitDBPostgresCopySpec{
		format: kitDBPostgresCopyCSV, delimiter: ',', null: "", header: true,
	}
	var rows [][]kitDBPostgresCopyField
	decoder := newKitDBPostgresCopyDecoder(
		spec,
		make([]StructFieldDef, 3),
		func(fields []kitDBPostgresCopyField, _ int64) error {
			cloned := make([]kitDBPostgresCopyField, len(fields))
			for index, field := range fields {
				cloned[index] = kitDBPostgresCopyField{
					data: append([]byte(nil), field.data...), quoted: field.quoted,
				}
			}
			rows = append(rows, cloned)
			return nil
		},
	)
	for _, chunk := range []string{
		"id,title,note\r\n1,\"Cotton, ",
		"shirt\",\"line one\nline two\"\r",
		"\n2,\"\",\r\n",
	} {
		if err := decoder.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	if err := decoder.Complete(); err != nil {
		t.Fatal(err)
	}
	if decoder.Rows() != 2 || len(rows) != 2 {
		t.Fatalf("decoded rows = %d, captured = %d", decoder.Rows(), len(rows))
	}
	if got := string(rows[0][1].data); got != "Cotton, shirt" || !rows[0][1].quoted {
		t.Fatalf("quoted title = %q, quoted=%v", got, rows[0][1].quoted)
	}
	if got := string(rows[0][2].data); got != "line one\nline two" {
		t.Fatalf("multiline note = %q", got)
	}
	if !rows[1][1].quoted || len(rows[1][1].data) != 0 || rows[1][2].quoted {
		t.Fatalf("empty CSV fields lost quote identity: %#v", rows[1])
	}
}

func TestKitDBPostgresCopyAdmissionPartitionsByAppAndDatabase(t *testing.T) {
	appA := &Tenant{AppScope: AppScope{entity: &Entity{Identity: "app-a", Domain: "a.example"}}}
	appB := &Tenant{AppScope: AppScope{entity: &Entity{Identity: "app-b", Domain: "b.example"}}}
	productsA := (&kitDBPostgresSession{tenant: appA, databaseName: "products"}).CopyAdmission()
	ordersA := (&kitDBPostgresSession{tenant: appA, databaseName: "orders"}).CopyAdmission()
	productsB := (&kitDBPostgresSession{tenant: appB, databaseName: "products"}).CopyAdmission()
	if productsA.Key == "" || productsA.Weight != 1 || productsA.Key == ordersA.Key || productsA.Key == productsB.Key {
		t.Fatalf("COPY admission partitions = productsA:%#v ordersA:%#v productsB:%#v", productsA, ordersA, productsB)
	}
}

func TestKitDBPostgresCopyInWithLibPQIsAtomic(t *testing.T) {
	_, address, _ := startKitDBPostgresTransactionTest(t, 10*time.Second)
	writer := openKitDBPostgresDatabaseTestClient(t, address, "transactions", "transaction-secret")
	observer := openKitDBPostgresDatabaseTestClient(t, address, "transactions", "transaction-secret")
	defer writer.Close()
	defer observer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	transaction, err := writer.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	statement, err := transaction.PrepareContext(ctx, pq.CopyIn("products", "id", "sku", "title", "price"))
	if err != nil {
		_ = transaction.Rollback()
		t.Fatalf("prepare COPY: %v", err)
	}
	for _, row := range [][]any{
		{"copy-1", "COPY-1", "Cotton\tshirt", int64(12)},
		{"copy-2", "COPY-2", "Line one\nline two", int64(24)},
	} {
		if _, err := statement.ExecContext(ctx, row...); err != nil {
			_ = statement.Close()
			_ = transaction.Rollback()
			t.Fatalf("stream COPY row: %v", err)
		}
	}
	result, err := statement.ExecContext(ctx)
	if err != nil {
		_ = statement.Close()
		_ = transaction.Rollback()
		t.Fatalf("finish COPY: %v", err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 2 {
		_ = statement.Close()
		_ = transaction.Rollback()
		t.Fatalf("COPY rows affected = %d, err=%v", affected, err)
	}
	if err := statement.Close(); err != nil {
		_ = transaction.Rollback()
		t.Fatal(err)
	}
	var count int64
	if err := observer.QueryRowContext(ctx, `SELECT COUNT(*) FROM products`).Scan(&count); err != nil {
		_ = transaction.Rollback()
		t.Fatal(err)
	}
	if count != 0 {
		_ = transaction.Rollback()
		t.Fatalf("COPY leaked %d rows before COMMIT", count)
	}
	if err := transaction.Commit(); err != nil {
		t.Fatal(err)
	}
	var title string
	var price int64
	if err := observer.QueryRowContext(
		ctx, `SELECT title, price FROM products WHERE id = 'copy-2'`,
	).Scan(&title, &price); err != nil {
		t.Fatal(err)
	}
	if title != "Line one\nline two" || price != 24 {
		t.Fatalf("copied row = (%q, %d)", title, price)
	}

	failed, err := writer.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := failed.PrepareContext(ctx, pq.CopyIn("products", "id", "sku", "title", "price"))
	if err != nil {
		_ = failed.Rollback()
		t.Fatal(err)
	}
	_, _ = duplicate.ExecContext(ctx, "must-rollback", "ROLLBACK-ME", "First row", int64(1))
	_, _ = duplicate.ExecContext(ctx, "duplicate", "COPY-1", "Duplicate unique", int64(2))
	_, finishErr := duplicate.ExecContext(ctx)
	_ = duplicate.Close()
	if finishErr == nil {
		_ = failed.Rollback()
		t.Fatal("duplicate COPY unexpectedly succeeded")
	}
	var postgresErr *pq.Error
	if !errors.As(finishErr, &postgresErr) || postgresErr.Code != "23505" {
		_ = failed.Rollback()
		t.Fatalf("duplicate COPY SQLSTATE = %T %v", finishErr, finishErr)
	}
	if err := failed.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		t.Fatal(err)
	}
	if err := observer.QueryRowContext(
		ctx, `SELECT COUNT(*) FROM products WHERE id = 'must-rollback'`,
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("failed COPY retained %d earlier rows", count)
	}
}

func TestKitDBPostgresResumableImporterCSVAndJSONL(t *testing.T) {
	_, address, _ := startKitDBPostgresTransactionTest(t, 20*time.Second)
	database := openKitDBPostgresDatabaseTestClient(t, address, "transactions", "transaction-secret")
	defer database.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	directory := t.TempDir()
	csvPath := filepath.Join(directory, "products.csv")
	originalCSV := "id,sku,title,price\n" +
		"resume-1,RESUME-1,First,10\n" +
		"resume-2,RESUME-2,Second,20\n" +
		"resume-3,RESUME-3,Third,30\n" +
		"resume-4,RESUME-4,Fourth,40\n" +
		"resume-5,RESUME-5,Fifth,50\n"
	if err := os.WriteFile(csvPath, []byte(originalCSV), 0o644); err != nil {
		t.Fatal(err)
	}
	config := pgimport.Config{
		Database: database, File: csvPath, Format: "csv", Header: true,
		Table: "products", Columns: []string{"id", "sku", "title", "price"},
		ImportID: "products-csv-resume", SourceID: "products-feed-v1",
		ChunkRows: 2, ChunkBytes: 1024, MaxChunks: 1,
	}
	first, err := pgimport.Run(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	if first.Complete || first.Chunk != 1 || first.Rows != 2 || first.CommittedChunks != 1 {
		t.Fatalf("first import report = %#v", first)
	}
	var count int64
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM products`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("first import count = %d err=%v", count, err)
	}

	changed := strings.Replace(originalCSV, "resume-1,RESUME-1,First,10", "resume-1,RESUME-1,Changed,10", 1)
	if err := os.WriteFile(csvPath, []byte(changed), 0o644); err != nil {
		t.Fatal(err)
	}
	config.MaxChunks = 0
	if _, err := pgimport.Run(ctx, config); err == nil || !strings.Contains(err.Error(), "source prefix changed") {
		t.Fatalf("changed prefix resume error = %v", err)
	}
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM products`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("changed prefix mutated rows = %d err=%v", count, err)
	}

	if err := os.WriteFile(csvPath, []byte(originalCSV), 0o644); err != nil {
		t.Fatal(err)
	}
	completed, err := pgimport.Run(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	if !completed.Resumed || !completed.Complete || completed.Chunk != 3 ||
		completed.Rows != 5 || completed.CommittedChunks != 2 {
		t.Fatalf("completed CSV import = %#v", completed)
	}
	again, err := pgimport.Run(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	if !again.Resumed || !again.Complete || again.Rows != 5 || again.CommittedChunks != 0 {
		t.Fatalf("completed replay = %#v", again)
	}

	jsonlPath := filepath.Join(directory, "products.jsonl")
	jsonl := "{\"id\":\"jsonl-1\",\"sku\":\"JSONL-1\",\"title\":\"One\",\"price\":61}\n" +
		"{\"id\":\"jsonl-2\",\"sku\":\"JSONL-2\",\"title\":\"Two\",\"price\":62}\n"
	if err := os.WriteFile(jsonlPath, []byte(jsonl), 0o644); err != nil {
		t.Fatal(err)
	}
	jsonReport, err := pgimport.Run(ctx, pgimport.Config{
		Database: database, File: jsonlPath, Format: "jsonl", Table: "products",
		Columns: []string{"id", "sku", "title", "price"}, ImportID: "products-jsonl",
		SourceID: "products-jsonl-v1", ChunkRows: 1, ChunkBytes: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !jsonReport.Complete || jsonReport.Rows != 2 || jsonReport.Chunk != 2 {
		t.Fatalf("JSONL import report = %#v", jsonReport)
	}
	var price int64
	if err := database.QueryRowContext(ctx, `SELECT price FROM products WHERE id = 'jsonl-2'`).Scan(&price); err != nil || price != 62 {
		t.Fatalf("JSONL imported price = %d err=%v", price, err)
	}
	if _, err := pgimport.Cancel(ctx, database, "products-jsonl"); err == nil ||
		!strings.Contains(err.Error(), "already complete") {
		t.Fatalf("cancel completed import error = %v", err)
	}
	if forgotten, err := pgimport.Forget(ctx, database, "products-jsonl"); err != nil || !forgotten {
		t.Fatalf("forget completed import = %t err=%v", forgotten, err)
	}
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM products WHERE id = 'jsonl-2'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("forget completed import removed rows = %d err=%v", count, err)
	}
}

func TestKitDBPostgresImportLifecycle(t *testing.T) {
	_, address, _ := startKitDBPostgresTransactionTest(t, 20*time.Second)
	database := openKitDBPostgresDatabaseTestClient(t, address, "transactions", "transaction-secret")
	defer database.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	path := filepath.Join(t.TempDir(), "lifecycle.csv")
	original := "id,sku,title,price\n" +
		"lifecycle-1,LIFECYCLE-1,First,10\n" +
		"lifecycle-2,LIFECYCLE-2,Second,20\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	config := pgimport.Config{
		Database: database, File: path, Format: "csv", Header: true,
		Table: "products", Columns: []string{"id", "sku", "title", "price"},
		ImportID: "products-lifecycle", SourceID: "products-lifecycle-v1",
		ChunkRows: 1, ChunkBytes: 1024, MaxChunks: 1,
	}
	first, err := pgimport.Run(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	if first.Complete || first.Cancelled || first.Chunk != 1 || first.Rows != 1 {
		t.Fatalf("initial lifecycle import = %#v", first)
	}
	if _, err := pgimport.Forget(ctx, database, config.ImportID); err == nil ||
		!strings.Contains(err.Error(), "is active") {
		t.Fatalf("forget active import error = %v", err)
	}
	verified, err := pgimport.Verify(ctx, config)
	if err != nil || !verified.Verified || verified.Transaction != first.Transaction {
		t.Fatalf("verified lifecycle import = %#v err=%v", verified, err)
	}

	changed := strings.Replace(original, "First", "Changed", 1)
	if err := os.WriteFile(path, []byte(changed), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := pgimport.Verify(ctx, config); err == nil || !strings.Contains(err.Error(), "source prefix changed") {
		t.Fatalf("verify changed source error = %v", err)
	}
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	cancelled, err := pgimport.Cancel(ctx, database, config.ImportID)
	if err != nil {
		t.Fatal(err)
	}
	if !cancelled.Cancelled || cancelled.Complete || cancelled.Transaction <= first.Transaction {
		t.Fatalf("cancelled lifecycle import = %#v", cancelled)
	}
	again, err := pgimport.Cancel(ctx, database, config.ImportID)
	if err != nil || !again.Cancelled || again.Transaction != cancelled.Transaction {
		t.Fatalf("idempotent cancel = %#v err=%v", again, err)
	}
	if _, err := pgimport.Run(ctx, config); err == nil || !strings.Contains(err.Error(), "is cancelled") {
		t.Fatalf("resume cancelled import error = %v", err)
	}
	verified, err = pgimport.Verify(ctx, config)
	if err != nil || !verified.Verified || !verified.Cancelled {
		t.Fatalf("verify cancelled import = %#v err=%v", verified, err)
	}

	forgotten, err := pgimport.Forget(ctx, database, config.ImportID)
	if err != nil || !forgotten {
		t.Fatalf("forget cancelled import = %t err=%v", forgotten, err)
	}
	if _, found, err := pgimport.QueryStatus(ctx, database, config.ImportID); err != nil || found {
		t.Fatalf("forgotten import status found=%t err=%v", found, err)
	}
	if forgotten, err := pgimport.Forget(ctx, database, config.ImportID); err != nil || forgotten {
		t.Fatalf("idempotent forget = %t err=%v", forgotten, err)
	}
	var rows int64
	if err := database.QueryRowContext(
		ctx, `SELECT COUNT(*) FROM products WHERE id = 'lifecycle-1'`,
	).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("forget removed committed rows = %d err=%v", rows, err)
	}
}

func TestKitDBPostgresResumableCopyWatermarkIsAtomic(t *testing.T) {
	_, address, _ := startKitDBPostgresTransactionTest(t, 20*time.Second)
	writer := openKitDBPostgresDatabaseTestClient(t, address, "transactions", "transaction-secret")
	observer := openKitDBPostgresDatabaseTestClient(t, address, "transactions", "transaction-secret")
	defer writer.Close()
	defer observer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	previous := strings.Repeat("1", 64)
	checksum := strings.Repeat("2", 64)
	query := fmt.Sprintf(`COPY products (id, sku, title, price) FROM STDIN WITH (
KITDB_IMPORT 'atomic-copy', KITDB_SOURCE 'atomic-source', KITDB_SOURCE_FORMAT 'csv',
KITDB_CHUNK 1, KITDB_START 0, KITDB_END 32, KITDB_ROWS 1,
KITDB_PREVIOUS '%s', KITDB_CHECKSUM '%s', KITDB_COMPLETE true)`, previous, checksum)

	copyChunk := func(commit bool) error {
		transaction, err := writer.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		statement, err := transaction.PrepareContext(ctx, query)
		if err != nil {
			_ = transaction.Rollback()
			return err
		}
		if _, err := statement.ExecContext(ctx, "atomic-1", "ATOMIC-1", "Atomic", int64(1)); err != nil {
			_ = statement.Close()
			_ = transaction.Rollback()
			return err
		}
		if _, err := statement.ExecContext(ctx); err != nil {
			_ = statement.Close()
			_ = transaction.Rollback()
			return err
		}
		if err := statement.Close(); err != nil {
			_ = transaction.Rollback()
			return err
		}
		if commit {
			return transaction.Commit()
		}
		return transaction.Rollback()
	}

	if err := copyChunk(false); err != nil {
		t.Fatal(err)
	}
	if _, found, err := pgimport.QueryStatus(ctx, observer, "atomic-copy"); err != nil || found {
		t.Fatalf("rolled-back KIMP status: found=%v err=%v", found, err)
	}
	var count int64
	if err := observer.QueryRowContext(ctx, `SELECT COUNT(*) FROM products`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rolled-back resumable COPY rows = %d err=%v", count, err)
	}

	if err := copyChunk(true); err != nil {
		t.Fatal(err)
	}
	status, found, err := pgimport.QueryStatus(ctx, observer, "atomic-copy")
	if err != nil || !found || !status.Complete || status.Chunk != 1 || status.Rows != 1 || status.Checksum != checksum {
		t.Fatalf("committed KIMP status = %#v found=%v err=%v", status, found, err)
	}
	if err := copyChunk(true); err == nil {
		t.Fatal("replayed completed chunk unexpectedly committed")
	}
	if err := observer.QueryRowContext(ctx, `SELECT COUNT(*) FROM products`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("replayed resumable COPY rows = %d err=%v", count, err)
	}
}

func TestKitDBPostgresConcurrentResumableImportersConverge(t *testing.T) {
	_, address, _ := startKitDBPostgresTransactionTest(t, 20*time.Second)
	firstDB := openKitDBPostgresDatabaseTestClient(t, address, "transactions", "transaction-secret")
	secondDB := openKitDBPostgresDatabaseTestClient(t, address, "transactions", "transaction-secret")
	observer := openKitDBPostgresDatabaseTestClient(t, address, "transactions", "transaction-secret")
	defer firstDB.Close()
	defer secondDB.Close()
	defer observer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	path := filepath.Join(t.TempDir(), "concurrent.csv")
	var source strings.Builder
	source.WriteString("id,sku,title,price\n")
	for index := 0; index < 20; index++ {
		fmt.Fprintf(&source, "concurrent-%d,CONCURRENT-%d,Item %d,%d\n", index, index, index, index)
	}
	if err := os.WriteFile(path, []byte(source.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	base := pgimport.Config{
		File: path, Format: "csv", Header: true, Table: "products",
		Columns: []string{"id", "sku", "title", "price"}, ImportID: "concurrent-import",
		SourceID: "concurrent-source", ChunkRows: 2, ChunkBytes: 64 << 10,
	}
	type outcome struct {
		report pgimport.Report
		err    error
	}
	start := make(chan struct{})
	results := make(chan outcome, 2)
	for _, database := range []*sql.DB{firstDB, secondDB} {
		config := base
		config.Database = database
		go func() {
			<-start
			report, err := pgimport.Run(ctx, config)
			results <- outcome{report: report, err: err}
		}()
	}
	close(start)
	for range 2 {
		result := <-results
		if result.err != nil {
			t.Fatalf("concurrent importer: %v", result.err)
		}
		if !result.report.Complete || result.report.Rows != 20 || result.report.Chunk != 10 {
			t.Fatalf("concurrent importer report = %#v", result.report)
		}
	}
	var count int64
	if err := observer.QueryRowContext(ctx, `SELECT COUNT(*) FROM products`).Scan(&count); err != nil || count != 20 {
		t.Fatalf("concurrent import rows = %d err=%v", count, err)
	}
}

func TestKitDBImportStateEnvelopeDetectsCorruption(t *testing.T) {
	state := kitDBImportState{
		Version: kitDBImportStateVersion, ID: "state-test", Source: "source", SourceFormat: "csv",
		TableID: strings.Repeat("1", 32), Table: "products",
		FieldIDs: []string{strings.Repeat("2", 32)}, Columns: []string{"id"},
		Seed: strings.Repeat("3", 64), Checksum: strings.Repeat("4", 64),
		Chunk: 1, Rows: 1, Offset: 10, Complete: false, Transaction: 2,
	}
	encoded, err := encodeKitDBImportState(state)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeKitDBImportState(encoded)
	if err != nil || decoded.ID != state.ID || decoded.Checksum != state.Checksum {
		t.Fatalf("decoded KIMP = %#v err=%v", decoded, err)
	}
	corrupt := append([]byte(nil), encoded...)
	corrupt[len(corrupt)-5] ^= 0x80
	if _, err := decodeKitDBImportState(corrupt); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("corrupt KIMP error = %v", err)
	}
}

func TestKitDBPostgresBulkCopyCommitsInExplicitTransaction(t *testing.T) {
	_, address, _ := startKitDBPostgresTransactionTest(t, 10*time.Second)
	database := openKitDBPostgresDatabaseTestClient(t, address, "transactions", "transaction-secret")
	defer database.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	transaction, err := database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	statement, err := transaction.PrepareContext(
		ctx,
		pq.CopyIn("products", "id", "sku", "title", "price")+" WITH (KITDB_BULK TRUE)",
	)
	if err != nil {
		_ = transaction.Rollback()
		t.Fatal(err)
	}
	if _, err := statement.ExecContext(ctx, "bulk-1", "BULK-1", "Bulk row", int64(42)); err != nil {
		_ = statement.Close()
		_ = transaction.Rollback()
		t.Fatal(err)
	}
	if _, err := statement.ExecContext(ctx); err != nil {
		_ = statement.Close()
		_ = transaction.Rollback()
		t.Fatal(err)
	}
	if err := statement.Close(); err != nil {
		_ = transaction.Rollback()
		t.Fatal(err)
	}
	if err := transaction.Commit(); err != nil {
		t.Fatal(err)
	}

	var title string
	if err := database.QueryRowContext(
		ctx, `SELECT title FROM products WHERE id = 'bulk-1'`,
	).Scan(&title); err != nil {
		t.Fatal(err)
	}
	if title != "Bulk row" {
		t.Fatalf("bulk row title = %q", title)
	}
}

func TestKitDBPostgresAutocommitCopyAbortRollsBack(t *testing.T) {
	tenant, _, database := startKitDBPostgresTransactionTest(t, 10*time.Second)
	session := &kitDBPostgresSession{
		tenant: tenant, database: database, databaseName: "transactions",
		storageName: "transactions.kitdb", user: "kitdb",
	}
	defer session.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, handled, err := session.BeginCopyIn(
		ctx,
		`COPY products (id, sku, title, price) FROM STDIN WITH (KITDB_BULK true)`,
		nil,
	)
	var protocolErr *pgwire.Error
	if !handled || !errors.As(err, &protocolErr) || protocolErr.Code != "25001" {
		t.Fatalf("autocommit bulk COPY = handled %v, error %T %v", handled, err, err)
	}
	request, handled, err := session.BeginCopyIn(
		ctx,
		`COPY products (id, sku, title, price) FROM STDIN WITH (FORMAT csv)`,
		nil,
	)
	if err != nil || !handled {
		t.Fatalf("begin autocommit COPY: handled=%v err=%v", handled, err)
	}
	if err := request.Stream.Write(ctx, []byte("aborted,ABORTED,Discard me,9\n")); err != nil {
		t.Fatal(err)
	}
	if err := request.Stream.Abort(context.Canceled); err == nil {
		t.Fatal("aborted COPY did not return its cancellation")
	}
	result, err := session.Execute(ctx, `SELECT COUNT(*) FROM products WHERE id = 'aborted'`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Rows) != 1 || string(result.Rows[0][0].Data) != "0" {
		t.Fatalf("aborted autocommit COPY rows = %#v", result.Rows)
	}

	request, handled, err = session.BeginCopyIn(
		ctx,
		`COPY products (id, sku, title, price) FROM STDIN WITH (FORMAT csv, HEADER true)`,
		nil,
	)
	if err != nil || !handled {
		t.Fatalf("begin CSV COPY: handled=%v err=%v", handled, err)
	}
	for _, chunk := range []string{
		"id,sku,title,price\r\ncsv-1,CSV-1,\"Cotton, ",
		"shirt\",42\r\ncsv-2,CSV-2,\"line one\nline two\",7\n",
	} {
		if err := request.Stream.Write(ctx, []byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	completed, err := request.Stream.Complete(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if completed.CommandTag != "COPY 2" {
		t.Fatalf("CSV COPY command tag = %q", completed.CommandTag)
	}
	result, err = session.Execute(ctx, `SELECT title, price FROM products WHERE id = 'csv-2'`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Rows) != 1 || string(result.Rows[0][0].Data) != "line one\nline two" ||
		string(result.Rows[0][1].Data) != "7" {
		t.Fatalf("CSV COPY typed row = %#v", result.Rows)
	}
}

func TestKitDBPostgresCopyObservesTransactionLifetimeWhileIdle(t *testing.T) {
	const transactionLifetime = 2 * time.Second
	_, address, _ := startKitDBPostgresTransactionTest(t, transactionLifetime)
	database := openKitDBPostgresDatabaseTestClient(t, address, "transactions", "transaction-secret")
	defer database.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	transaction, err := database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	statement, err := transaction.PrepareContext(ctx, pq.CopyIn("products", "id", "sku", "title"))
	if err != nil {
		_ = transaction.Rollback()
		t.Fatal(err)
	}
	time.Sleep(transactionLifetime + 250*time.Millisecond)
	_, finishErr := statement.ExecContext(ctx, "late", "LATE", "Too late")
	if finishErr == nil {
		_, finishErr = statement.ExecContext(ctx)
	}
	if closeErr := statement.Close(); finishErr == nil {
		finishErr = closeErr
	}
	if finishErr == nil {
		_ = transaction.Rollback()
		t.Fatal("COPY outlived its transaction timeout")
	}
	var postgresErr *pq.Error
	if !errors.As(finishErr, &postgresErr) || postgresErr.Code != "57014" {
		_ = transaction.Rollback()
		t.Fatalf("timed-out COPY SQLSTATE = %T %v", finishErr, finishErr)
	}
	if err := transaction.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		t.Fatal(err)
	}
}

func TestKitDBPostgresCopyBoundsTotalInputAndRollsBack(t *testing.T) {
	_, address, _ := startKitDBPostgresTransactionTestWithCopyLimit(t, 5*time.Second, 48)
	database := openKitDBPostgresDatabaseTestClient(t, address, "transactions", "transaction-secret")
	observer := openKitDBPostgresDatabaseTestClient(t, address, "transactions", "transaction-secret")
	defer database.Close()
	defer observer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	transaction, err := database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	statement, err := transaction.PrepareContext(ctx, pq.CopyIn("products", "id", "sku", "title"))
	if err != nil {
		_ = transaction.Rollback()
		t.Fatal(err)
	}
	_, _ = statement.ExecContext(ctx, "bounded", "BOUNDED", "a title that pushes this COPY over its byte budget")
	_, finishErr := statement.ExecContext(ctx)
	_ = statement.Close()
	if finishErr == nil {
		_ = transaction.Rollback()
		t.Fatal("COPY exceeded MaxCopyBytes without failing")
	}
	var postgresErr *pq.Error
	if !errors.As(finishErr, &postgresErr) || postgresErr.Code != "54000" {
		_ = transaction.Rollback()
		t.Fatalf("bounded COPY SQLSTATE = %T %v", finishErr, finishErr)
	}
	if err := transaction.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		t.Fatal(err)
	}
	var count int64
	if err := observer.QueryRowContext(
		ctx, `SELECT COUNT(*) FROM products WHERE id = 'bounded'`,
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("bounded COPY retained %d rows", count)
	}
}

func TestKitDBPostgresCopyDecoderBoundsOneRecord(t *testing.T) {
	decoder := newKitDBPostgresCopyDecoder(
		kitDBPostgresCopySpec{format: kitDBPostgresCopyText, delimiter: '\t', null: `\N`},
		make([]StructFieldDef, 1),
		func([]kitDBPostgresCopyField, int64) error { return nil },
	)
	err := decoder.Write(make([]byte, kitDBPostgresCopyRecordBytes+1))
	if err == nil {
		t.Fatal("COPY accepted an oversized record")
	}
	var protocolErr *pgwire.Error
	if !errors.As(err, &protocolErr) || protocolErr.Code != "54000" {
		t.Fatalf("oversized record SQLSTATE = %T %v", err, err)
	}
}
