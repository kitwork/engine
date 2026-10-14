package relational

import (
	"context"
	"errors"
	"testing"
)

func TestLocalRoleInsertSelectBoundsAndCancellation(t *testing.T) {
	node, admin, reader := roleCatalogFixture(t)
	for _, q := range []string{"CREATE TABLE drafts(id BIGINT PRIMARY KEY,title TEXT)", "INSERT INTO drafts VALUES(10,'a'),(11,'b')", "GRANT SELECT ON TABLE drafts TO ROLE article_reader", "GRANT INSERT ON TABLE articles TO ROLE article_reader"} {
		if _, err := admin.Exec(q); err != nil {
			t.Fatal(q, err)
		}
	}
	node.mu.Lock()
	var owner *Engine
	for _, entry := range node.engines {
		if entry.database.name == "shop" {
			owner = entry.engine
		}
	}
	node.mu.Unlock()
	if owner == nil {
		t.Fatal("owner missing")
	}
	owner.mu.Lock()
	oldLimit := owner.maximumResultRows
	owner.maximumResultRows = 1
	owner.mu.Unlock()
	defer func() { owner.mu.Lock(); owner.maximumResultRows = oldLimit; owner.mu.Unlock() }()
	if _, err := reader.Exec("INSERT INTO articles SELECT id,title FROM drafts"); err == nil {
		t.Fatal("source exceeded bounded materialization")
	}
	var n int
	if err := admin.QueryRow("SELECT COUNT(*) FROM articles WHERE id>=10").Scan(&n); err != nil || n != 0 {
		t.Fatal("limit failure published rows", n, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := reader.ExecContext(ctx, "INSERT INTO articles SELECT id,title FROM drafts LIMIT 1"); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation", err)
	}
	if err := admin.QueryRow("SELECT COUNT(*) FROM articles WHERE id>=10").Scan(&n); err != nil || n != 0 {
		t.Fatal("cancel published rows", n, err)
	}
	tx, err := reader.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.Exec("INSERT INTO articles SELECT id,title FROM drafts"); err == nil {
		t.Fatal("transaction bypassed materialization limit")
	}
	if _, err = tx.Exec("INSERT INTO articles SELECT id,title FROM drafts ORDER BY id LIMIT 1"); err != nil {
		t.Fatal("failed statement damaged transaction", err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = admin.QueryRow("SELECT COUNT(*) FROM articles WHERE id>=10").Scan(&n); err != nil || n != 1 {
		t.Fatal("bounded success", n, err)
	}
}
