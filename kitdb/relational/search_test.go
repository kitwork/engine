package relational

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	kitdbengine "github.com/kitwork/engine/kitdb"
)

func TestStandaloneSearchBuildFilterCursorCatchUpAndReopen(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "products.kitdb")
	searchRoot := filepath.Join(root, "search")
	options := Options{
		Kernel:                  kitdbengine.OpenOptions{RetainHistory: true},
		SearchRoot:              searchRoot,
		SearchForegroundWait:    10 * time.Second,
		MaximumSearchResults:    2_000,
		MaximumSearchCandidates: 10_000,
	}
	engine, err := OpenWithOptions(path, options)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := engine.Execute(ctx, `
		CREATE TABLE products (
			merchant TEXT NOT NULL,
			id INTEGER NOT NULL,
			name TEXT NOT NULL SEARCHABLE WEIGHT 5,
			description TEXT NOT NULL SEARCHABLE WEIGHT 2,
			price INTEGER NOT NULL,
			PRIMARY KEY (merchant, id)
		)
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, `
		INSERT INTO products (merchant, id, name, description, price) VALUES
		('shopee', 1, 'Bàn phím Logitech MX Keys', 'Bàn phím không dây cao cấp', 2100000),
		('shopee', 2, 'Bàn phím Logitech K380', 'Bàn phím Bluetooth nhỏ gọn', 650000),
		('shopee', 3, 'Chuột Logitech MX Master', 'Chuột không dây văn phòng', 1800000),
		('lazada', 1, 'Bàn phím Logitech G Pro', 'Bàn phím cơ chơi game', 2400000),
		('lazada', 2, 'Bàn phím cơ Keychron', 'Bàn phím Bluetooth', 1900000),
		('tiki', 1, 'Logitech Combo Touch', 'Bàn phím dành cho máy tính bảng', 3200000)
	`); err != nil {
		t.Fatal(err)
	}

	first, err := engine.Execute(ctx, `
		SELECT merchant, id, name, _score, _snippet, _cursor
		FROM products
		WHERE * SEARCH $1
		ORDER BY _score DESC
		LIMIT 2
	`, "ban phim logitech")
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Rows) != 2 || first.Rows[0][3].(float64) <= 0 {
		t.Fatalf("first SEARCH page = %#v", first.Rows)
	}
	if snippet, _ := first.Rows[0][4].(string); !strings.Contains(snippet, "<b>") {
		t.Fatalf("SEARCH snippet = %q", snippet)
	}
	cursor, ok := first.Rows[len(first.Rows)-1][5].(string)
	if !ok || cursor == "" {
		t.Fatalf("SEARCH cursor = %#v", first.Rows[len(first.Rows)-1][5])
	}
	second, err := engine.Execute(ctx, `
		SELECT merchant, id, name, _score, _cursor
		FROM products
		WHERE * SEARCH $1
		ORDER BY _score DESC
		LIMIT 2 AFTER $2
	`, "ban phim logitech", cursor)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Rows) == 0 {
		t.Fatal("second SEARCH page is empty")
	}
	seen := make(map[string]struct{}, len(first.Rows))
	for _, row := range first.Rows {
		seen[fmt.Sprintf("%s:%d", row[0], row[1])] = struct{}{}
	}
	for _, row := range second.Rows {
		key := fmt.Sprintf("%s:%d", row[0], row[1])
		if _, duplicate := seen[key]; duplicate {
			t.Fatalf("SEARCH AFTER repeated row %q", key)
		}
	}

	filtered, err := engine.Execute(ctx, `
		SELECT merchant, id, name, _score
		FROM products
		WHERE * SEARCH 'ban phim' AND merchant = 'shopee'
		ORDER BY _score DESC
		LIMIT 1000
	`)
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered.Rows) != 2 {
		t.Fatalf("merchant-filtered SEARCH = %#v", filtered.Rows)
	}
	for _, row := range filtered.Rows {
		if row[0] != "shopee" {
			t.Fatalf("merchant-filtered SEARCH leaked %#v", row)
		}
	}

	transaction, err := engine.BeginTransaction(ctx, TransactionOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Execute(ctx, `SELECT id FROM products WHERE * SEARCH 'logitech'`); err == nil {
		t.Fatal("SEARCH unexpectedly ran inside an explicit transaction")
	}
	_ = transaction.Rollback()

	if _, err := engine.Execute(ctx, `
		UPDATE products SET name = 'Màn hình văn phòng' WHERE merchant = 'shopee' AND id = 1
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, `
		SELECT merchant, id FROM products
		WHERE * SEARCH $1 ORDER BY _score DESC LIMIT 2 AFTER $2
	`, "ban phim logitech", cursor); err == nil || !strings.Contains(err.Error(), "cursor is stale") {
		t.Fatalf("stale cursor error = %v", err)
	}
	afterUpdate, err := engine.Execute(ctx, `
		SELECT merchant, id, name FROM products
		WHERE * SEARCH 'man hinh van phong' ORDER BY _score DESC LIMIT 10
	`)
	if err != nil || len(afterUpdate.Rows) != 1 || afterUpdate.Rows[0][2] != "Màn hình văn phòng" {
		t.Fatalf("SEARCH catch-up = %#v, %v", afterUpdate.Rows, err)
	}
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenWithOptions(path, options)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	reused, err := reopened.Execute(ctx, `
		SELECT merchant, id, name FROM products
		WHERE * SEARCH 'man hinh' ORDER BY _score DESC LIMIT 500
	`)
	if err != nil || len(reused.Rows) != 1 || reused.Rows[0][2] != "Màn hình văn phòng" {
		t.Fatalf("reopened SEARCH = %#v, %v", reused.Rows, err)
	}
}

func TestStandaloneSearchCursorRejectsTamperAndDifferentQuery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cursor.kitdb")
	engine, err := OpenWithOptions(path, Options{
		Kernel:               kitdbengine.OpenOptions{RetainHistory: true},
		SearchForegroundWait: 10 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	ctx := context.Background()
	if _, err := engine.Execute(ctx, `CREATE TABLE notes (
		id INTEGER PRIMARY KEY, body TEXT SEARCHABLE
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, `INSERT INTO notes (id, body) VALUES
		(1, 'alpha common'), (2, 'beta common'), (3, 'gamma common')`); err != nil {
		t.Fatal(err)
	}
	page, err := engine.Execute(ctx, `SELECT id, _cursor FROM notes
		WHERE * SEARCH 'common' ORDER BY _score DESC LIMIT 1`)
	if err != nil {
		t.Fatal(err)
	}
	cursor := page.Rows[0][1].(string)
	replacement := byte('A')
	if cursor[10] == replacement {
		replacement = 'B'
	}
	tampered := cursor[:10] + string(replacement) + cursor[11:]
	if _, err := engine.Execute(ctx, `SELECT id FROM notes
		WHERE * SEARCH 'common' ORDER BY _score DESC LIMIT 1 AFTER $1`, tampered); err == nil {
		t.Fatal("tampered SEARCH cursor unexpectedly succeeded")
	}
	if _, err := engine.Execute(ctx, `SELECT id FROM notes
		WHERE * SEARCH 'alpha' ORDER BY _score DESC LIMIT 1 AFTER $1`, cursor); err == nil ||
		!strings.Contains(err.Error(), "different query") {
		t.Fatalf("different-query cursor error = %v", err)
	}
}

func TestStandaloneAlterColumnSearchablePublishesNewProjectionSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "alter-search.kitdb")
	engine, err := OpenWithOptions(path, Options{
		Kernel:               kitdbengine.OpenOptions{RetainHistory: true},
		SearchForegroundWait: 10 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	ctx := context.Background()
	if _, err := engine.Execute(ctx, `CREATE TABLE documents (
		id INTEGER PRIMARY KEY, title TEXT NOT NULL, body TEXT NOT NULL
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, `INSERT INTO documents (id, title, body)
		VALUES (1, 'KitDB search', 'pure Go database')`); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, `SELECT id FROM documents WHERE * SEARCH 'kitdb'`); err == nil {
		t.Fatal("SEARCH unexpectedly accepted a table without searchable fields")
	}
	if _, err := engine.Execute(ctx, `ALTER TABLE documents ALTER COLUMN title SET SEARCHABLE WEIGHT 6`); err != nil {
		t.Fatal(err)
	}
	result, err := engine.Execute(ctx, `SELECT id, title FROM documents
		WHERE * SEARCH 'kitdb' ORDER BY _score DESC LIMIT 10`)
	if err != nil || len(result.Rows) != 1 {
		t.Fatalf("SEARCH after SET SEARCHABLE = %#v, %v", result.Rows, err)
	}
	if _, err := engine.Execute(ctx, `ALTER TABLE documents ALTER title DROP SEARCHABLE`); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, `SELECT id FROM documents WHERE * SEARCH 'kitdb'`); err == nil {
		t.Fatal("SEARCH unexpectedly survived DROP SEARCHABLE")
	}
}

func TestStandaloneFilteredSearchPagesBeyondFormerTwoHundredCandidateWindow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "candidate-pages.kitdb")
	engine, err := OpenWithOptions(path, Options{
		Kernel:                  kitdbengine.OpenOptions{RetainHistory: true},
		SearchForegroundWait:    10 * time.Second,
		MaximumSearchResults:    500,
		MaximumSearchCandidates: 1_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	ctx := context.Background()
	if _, err := engine.Execute(ctx, `CREATE TABLE events (
		id INTEGER PRIMARY KEY, body TEXT SEARCHABLE, selected BOOLEAN NOT NULL
	)`); err != nil {
		t.Fatal(err)
	}
	var source strings.Builder
	source.WriteString("INSERT INTO events (id, body, selected) VALUES ")
	for id := 1; id <= 300; id++ {
		if id != 1 {
			source.WriteByte(',')
		}
		fmt.Fprintf(&source, "(%d, 'common event', %t)", id, id > 250)
	}
	if _, err := engine.Execute(ctx, source.String()); err != nil {
		t.Fatal(err)
	}
	result, err := engine.Execute(ctx, `
		SELECT id, _score FROM events
		WHERE * SEARCH 'common' AND selected = true
		ORDER BY _score DESC
		LIMIT 20
	`)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Rows) != 20 || result.Rows[0][0] != int64(251) || result.Rows[19][0] != int64(270) {
		t.Fatalf("filtered SEARCH beyond 200 candidates = %#v", result.Rows)
	}
}
