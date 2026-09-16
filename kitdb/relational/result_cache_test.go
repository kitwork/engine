package relational

import (
	"context"
	"testing"
	"time"

	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

func TestQueryResultCacheHitsAndInvalidatesOnCommit(t *testing.T) {
	engine, err := OpenWithOptions(t.TempDir()+"/cache.kitdb", Options{
		QueryCache: QueryCacheOptions{
			Select: time.Hour, Analytics: time.Hour,
			MaximumBytes: 1 << 20, MaximumEntries: 16, MaximumResult: 64 << 10,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	ctx := context.Background()
	for _, source := range []string{
		`CREATE TABLE products (id INTEGER PRIMARY KEY, name TEXT NOT NULL, price INTEGER NOT NULL)`,
		`INSERT INTO products VALUES (1, 'keyboard', 100)`,
	} {
		if _, err := engine.Execute(ctx, source); err != nil {
			t.Fatal(err)
		}
	}
	query := `SELECT name, price FROM products WHERE id = 1`
	first, err := engine.Execute(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	second, err := engine.Execute(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	if first.Rows[0][0] != "keyboard" || second.Rows[0][1] != int64(100) {
		t.Fatalf("cached rows = %#v / %#v", first.Rows, second.Rows)
	}
	stats := engine.QueryCacheStats()
	if stats.Hits != 1 || stats.Misses != 1 || stats.Entries != 1 {
		t.Fatalf("cache after hit = %+v", stats)
	}
	if _, err := engine.Execute(ctx, `UPDATE products SET price = 120 WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	updated, err := engine.Execute(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Rows[0][1] != int64(120) {
		t.Fatalf("stale cached row after commit: %#v", updated.Rows)
	}
	stats = engine.QueryCacheStats()
	if stats.Hits != 1 || stats.Misses != 2 || stats.Entries != 1 {
		t.Fatalf("cache after invalidation = %+v", stats)
	}
}

func TestQueryResultCacheSeparatesReadClassesAndBypassesVolatileSelect(t *testing.T) {
	cache, err := normalizeQueryCacheOptions(QueryCacheOptions{
		Select: time.Minute, Search: 2 * time.Minute, Analytics: 3 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	owner := newQueryResultCache(cache)
	for source, want := range map[string]time.Duration{
		`SELECT id FROM products`:                                  time.Minute,
		`SELECT count(*) FROM products`:                            3 * time.Minute,
		`SELECT * FROM products WHERE * SEARCH 'keyboard' LIMIT 5`: 2 * time.Minute,
		`SELECT current_timestamp`:                                 0,
	} {
		statement, err := kitdbsql.ParseStatement(source)
		if err != nil {
			t.Fatalf("parse %q: %v", source, err)
		}
		if got := owner.duration(&statement); got != want {
			t.Fatalf("duration %q = %s, want %s", source, got, want)
		}
	}
}

func TestQueryCacheStatusPragmaReportsActivity(t *testing.T) {
	engine, err := OpenWithOptions(t.TempDir()+"/cache-status.kitdb", Options{
		QueryCache: QueryCacheOptions{Select: time.Minute},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	ctx := context.Background()
	if _, err := engine.Execute(ctx, `CREATE TABLE products (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := engine.Execute(ctx, `SELECT id FROM products`); err != nil {
			t.Fatal(err)
		}
	}
	result, err := engine.Execute(ctx, `PRAGMA cache_status`)
	if err != nil {
		t.Fatal(err)
	}
	want := []any{true, int64(1), result.Rows[0][2], int64(1), int64(1), int64(0), int64(0)}
	if len(result.Rows) != 1 || len(result.Rows[0]) != len(want) {
		t.Fatalf("cache status shape = %#v", result)
	}
	for index := range want {
		if result.Rows[0][index] != want[index] {
			t.Fatalf("cache status column %d = %#v, want %#v", index, result.Rows[0][index], want[index])
		}
	}
	if bytes, ok := result.Rows[0][2].(int64); !ok || bytes <= 0 {
		t.Fatalf("cache status bytes = %#v", result.Rows[0][2])
	}
}
