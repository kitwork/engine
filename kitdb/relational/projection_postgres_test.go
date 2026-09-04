package relational

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

func TestProjectionPostgresProtocol(t *testing.T) {
	engine := projectionTestDatabase(t, 100)
	if _, err := engine.RefreshProjections(context.Background()); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- engine.ServePostgres(ctx, listener, PostgresServerOptions{PostgresOptions: PostgresOptions{Database: "test", User: "kitdb", Password: "projection-test", ReadOnly: true}})
	}()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("server did not drain")
		}
	}()
	db, err := sql.Open("postgres", fmt.Sprintf("postgres://kitdb:projection-test@%s/test?sslmode=disable", listener.Addr()))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	query := `SELECT SUM(price) FROM products WHERE price >= 25 AND enabled = true`
	expected := projectionExecute(t, engine, query).Rows[0][0]
	var sum int64
	if err := db.QueryRow(query).Scan(&sum); err != nil || sum != expected {
		t.Fatalf("wire aggregate: %v, %v, want %v", sum, err, expected)
	}
	explainRows, err := db.Query(`EXPLAIN ANALYZE ` + query)
	if err != nil {
		t.Fatal(err)
	}
	var actualDetail, kcolIODetail string
	for explainRows.Next() {
		var id int64
		var operation, detail string
		if err := explainRows.Scan(&id, &operation, &detail); err != nil {
			explainRows.Close()
			t.Fatal(err)
		}
		if operation == "actual" {
			actualDetail = detail
		}
		if operation == "kcol_io" {
			kcolIODetail = detail
		}
	}
	if err := explainRows.Close(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(actualDetail, "path=kcol-batch") || !strings.Contains(actualDetail, "loops=1") {
		t.Fatalf("wire EXPLAIN ANALYZE actual = %q", actualDetail)
	}
	if !strings.Contains(kcolIODetail, "header_reads=") ||
		!strings.Contains(kcolIODetail, "payload_reads=") ||
		!strings.Contains(kcolIODetail, "total_bytes=") {
		t.Fatalf("wire EXPLAIN ANALYZE KCOL I/O = %q", kcolIODetail)
	}
	var table, status, queryPath, layout string
	var partitionStrategy, partitionField, reason sql.NullString
	var fresh, enabled, supported bool
	var sourceTransaction, currentTransaction, projectedRows, chunks, chunkVersion int64
	var generation, fileBytes, liveBytes, obsoleteBytes int64
	if err := db.QueryRow(`PRAGMA analytics_status(products)`).Scan(
		&table, &status, &fresh, &enabled, &supported, &queryPath, &layout,
		&partitionStrategy, &partitionField, &sourceTransaction, &currentTransaction,
		&projectedRows, &chunks, &chunkVersion, &generation, &fileBytes, &liveBytes,
		&obsoleteBytes, &reason,
	); err != nil {
		t.Fatal(err)
	}
	if table != "products" || status != "ready" || !fresh || !enabled || !supported ||
		queryPath != "kcol-batch" || layout != "kcol-v3+chunk-v5" || partitionStrategy.Valid ||
		partitionField.Valid || sourceTransaction != currentTransaction || projectedRows != 100 ||
		chunks != 1 || chunkVersion != columnarChunkVersion || generation == 0 ||
		fileBytes == 0 || liveBytes == 0 || obsoleteBytes < 0 || reason.Valid {
		t.Fatalf("wire analytics status: table=%s status=%s fresh=%t path=%s layout=%s source=%d current=%d rows=%d chunks=%d version=%d generation=%d file=%d live=%d obsolete=%d reason=%v",
			table, status, fresh, queryPath, layout, sourceTransaction, currentTransaction,
			projectedRows, chunks, chunkVersion, generation, fileBytes, liveBytes, obsoleteBytes, reason)
	}
	rows, err := db.Query(`SELECT id, _score FROM products WHERE * SEARCH 'blue widget' LIMIT 5`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var id int64
		var score float64
		if err := rows.Scan(&id, &score); err != nil {
			t.Fatal(err)
		}
		count++
	}
	if err := rows.Err(); err != nil || count != 5 {
		t.Fatal(count, err)
	}
	searchExplain, err := db.Query(`
		EXPLAIN ANALYZE
		SELECT id, _score FROM products WHERE * SEARCH 'blue widget' LIMIT 5
	`)
	if err != nil {
		t.Fatal(err)
	}
	actualDetail = ""
	for searchExplain.Next() {
		var id int64
		var operation, detail string
		if err := searchExplain.Scan(&id, &operation, &detail); err != nil {
			searchExplain.Close()
			t.Fatal(err)
		}
		if operation == "actual" {
			actualDetail = detail
		}
	}
	if err := searchExplain.Close(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(actualDetail, "path=search-snapshot") || !strings.Contains(actualDetail, "result_rows=5") {
		t.Fatalf("wire SEARCH EXPLAIN ANALYZE actual = %q", actualDetail)
	}
}
