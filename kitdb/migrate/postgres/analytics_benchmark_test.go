package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	_ "github.com/lib/pq"
)

const shoppingTextAnalyticsQuery = `
	SELECT merchant, COUNT(*) AS products, SUM(price) AS value
	FROM shopping
	GROUP BY merchant
	ORDER BY merchant`

type shoppingTextAggregate struct {
	Merchant string
	Products int64
	Value    string
}

// BenchmarkShoppingProductionPostgresTextAnalytics is opt-in and read-only.
// It keeps the PostgreSQL baseline beside the KitDB 13M evidence without ever
// accepting or printing a source credential on the command line.
func BenchmarkShoppingProductionPostgresTextAnalytics(b *testing.B) {
	dsn, err := shoppingPostgresBenchmarkURL()
	if err != nil {
		b.Fatal(err)
	}
	if dsn == "" {
		b.Skip("set KITDB_SHOPPING_POSTGRES_BENCHMARK to a read-only PostgreSQL URL")
	}
	database, err := sql.Open("postgres", dsn)
	if err != nil {
		b.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)
	defer database.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	if err := database.PingContext(ctx); err != nil {
		b.Fatal(err)
	}
	if rows, err := queryShoppingTextAnalytics(ctx, database); err != nil || len(rows) != 3 {
		b.Fatalf("warmup groups=%d error=%v", len(rows), err)
	}

	b.ReportAllocs()
	b.ReportMetric(3, "groups")
	b.ResetTimer()
	for b.Loop() {
		if rows, err := queryShoppingTextAnalytics(ctx, database); err != nil || len(rows) != 3 {
			b.Fatalf("groups=%d error=%v", len(rows), err)
		}
	}
}

// TestShoppingProductionPostgresTextAnalyticsResult is an opt-in snapshot
// comparison aid. It logs only aggregate values, never the source URL.
func TestShoppingProductionPostgresTextAnalyticsResult(t *testing.T) {
	dsn, err := shoppingPostgresBenchmarkURL()
	if err != nil {
		t.Fatal(err)
	}
	if dsn == "" {
		t.Skip("set KITDB_SHOPPING_POSTGRES_BENCHMARK to a read-only PostgreSQL URL")
	}
	database, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	defer database.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	rows, err := queryShoppingTextAnalytics(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("groups=%d, want 3", len(rows))
	}
	t.Logf("aggregates=%+v", rows)
}

func shoppingPostgresBenchmarkURL() (string, error) {
	dsn := os.Getenv("KITDB_SHOPPING_POSTGRES_BENCHMARK")
	if dsn == "" {
		return "", nil
	}
	if name := os.Getenv("KITDB_SHOPPING_POSTGRES_DATABASE"); name != "" {
		return URLWithDatabase(dsn, name)
	}
	return dsn, nil
}

func queryShoppingTextAnalytics(ctx context.Context, database *sql.DB) ([]shoppingTextAggregate, error) {
	rows, err := database.QueryContext(ctx, shoppingTextAnalyticsQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]shoppingTextAggregate, 0, 3)
	for rows.Next() {
		var aggregate shoppingTextAggregate
		if err := rows.Scan(&aggregate.Merchant, &aggregate.Products, &aggregate.Value); err != nil {
			return nil, err
		}
		if aggregate.Merchant == "" || aggregate.Products < 1 || aggregate.Value == "" {
			return nil, fmt.Errorf("invalid PostgreSQL shopping aggregate")
		}
		result = append(result, aggregate)
	}
	return result, rows.Err()
}
