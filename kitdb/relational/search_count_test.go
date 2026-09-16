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

func TestStandaloneSearchCountExactAcrossBackends(t *testing.T) {
	for _, packed := range []bool{false, true} {
		t.Run(fmt.Sprintf("packed=%t", packed), func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "counts.kitdb")
			options := Options{Kernel: kitdbengine.OpenOptions{RetainHistory: true}, ExperimentalProjections: packed,
				SearchForegroundWait: 10 * time.Second, MaximumSearchResults: 2, MaximumSearchCandidates: 4}
			engine, err := OpenWithOptions(path, options)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { engine.Close() }()
			projectionExecute(t, engine, `CREATE TABLE shopping (
				merchant TEXT NOT NULL, id INTEGER NOT NULL,
				name TEXT SEARCHABLE, description TEXT SEARCHABLE, price INTEGER,
				PRIMARY KEY (merchant, id))`)
			var values []string
			for i := 0; i < 300; i++ {
				merchant := "shopee"
				if i%2 != 0 {
					merchant = "lazada"
				}
				values = append(values, fmt.Sprintf("('%s', %d, 'highlands', 'coffee highlands', %d)", merchant, i, i))
			}
			projectionExecute(t, engine, `INSERT INTO shopping VALUES `+strings.Join(values, ","))
			projectionExecute(t, engine, `INSERT INTO shopping VALUES ('other', 0, 'tea', NULL, NULL)`)
			refresh := func() {
				if packed {
					if _, err := engine.RefreshProjections(ctx); err != nil {
						t.Fatal(err)
					}
				}
			}
			refresh()
			assertCount := func(query string, want int64, args ...any) {
				t.Helper()
				result, err := engine.Execute(ctx, query, args...)
				if err != nil {
					t.Fatalf("%s: %v", query, err)
				}
				if len(result.Rows) != 1 || len(result.Rows[0]) != 1 || result.Rows[0][0] != want || result.CommandTag != "SELECT 1" {
					t.Fatalf("%s result=%+v want=%d", query, result, want)
				}
				if len(result.Columns) != 1 || result.Columns[0].Kind != "integer" {
					t.Fatalf("count columns=%+v", result.Columns)
				}
			}
			base := `SELECT COUNT(*) FROM shopping WHERE * SEARCH 'highlands coffee'`
			assertCount(base, 300)
			assertCount(base+` LIMIT 1`, 300)
			assertCount(base+` LIMIT 100000`, 300)
			assertCount(base+` AND merchant = 'shopee'`, 150)
			assertCount(base+` AND merchant = 'shopee' AND price >= 100`, 100)
			assertCount(base+` AND (price < 5 OR price >= 295)`, 10)
			assertCount(base+` AND price IS NULL`, 0)
			assertCount(`SELECT COUNT(price) FROM shopping WHERE * SEARCH 'coffee'`, 300)
			assertCount(`SELECT COUNT(*) AS total FROM shopping WHERE name SEARCH $1 ORDER BY total`, 300, "highlands")
			assertCount(`SELECT COUNT(*) FROM shopping WHERE name SEARCH 'highlands coffee'`, 0)
			for _, query := range []any{"missing", "", "   ", "!!!", nil} {
				assertCount(`SELECT COUNT(*) FROM shopping WHERE * SEARCH $1`, 0, query)
			}
			for _, suffix := range []string{` LIMIT 0`, ` LIMIT 1 OFFSET 1`} {
				result := projectionExecute(t, engine, base+suffix)
				if len(result.Rows) != 0 || len(result.Columns) != 1 {
					t.Fatalf("aggregate pagination=%+v", result)
				}
			}
			for _, query := range []string{
				`SELECT COUNT(*), merchant FROM shopping WHERE * SEARCH 'coffee'`,
				base + ` ORDER BY _score DESC`, base + ` AFTER 'invalid'`,
			} {
				if _, err := engine.Execute(ctx, query); err == nil {
					t.Fatalf("unsupported count shape accepted: %s", query)
				}
			}
			explained := projectionExecute(t, engine, `EXPLAIN `+base)
			if !strings.Contains(fmt.Sprint(explained.Rows), "top_k=false") {
				t.Fatalf("count explain=%+v", explained)
			}
			tx, err := engine.BeginTransaction(ctx, TransactionOptions{ReadOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Execute(ctx, base); err == nil {
				t.Fatal("count accepted explicit transaction")
			}
			_ = tx.Rollback()
			projectionExecute(t, engine, `DELETE FROM shopping WHERE merchant = 'shopee' AND id = 0`)
			projectionExecute(t, engine, `UPDATE shopping SET description = 'tea' WHERE merchant = 'lazada' AND id = 1`)
			if packed {
				if _, err := engine.Execute(ctx, base); err == nil {
					t.Fatal("count served a stale packed projection")
				}
			}
			refresh()
			assertCount(base, 298)
			if err := engine.Close(); err != nil {
				t.Fatal(err)
			}
			engine, err = OpenWithOptions(path, options)
			if err != nil {
				t.Fatal(err)
			}
			assertCount(base, 298)
		})
	}
}
