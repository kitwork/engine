package relational

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"os"
	"strings"
	"testing"
	"time"
)

// Opt-in wire check: no source writes, schema changes, or second kernel owner.
// Run against the newly built standalone server, not an older listener.
func TestShoppingProductionJoinAggregate(t *testing.T) {
	dsn := os.Getenv("KITDB_JOIN_BENCHMARK_DSN")
	if dsn == "" {
		t.Skip("set KITDB_JOIN_BENCHMARK_DSN to the local read-only shopping server")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	client, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	client.SetMaxOpenConns(1)
	var totalRows int64
	if err := client.QueryRowContext(ctx, `SELECT COUNT(*) FROM shopping`).Scan(&totalRows); err != nil {
		t.Fatal(err)
	}
	reference, err := client.QueryContext(ctx, `SELECT id, price FROM shopping WHERE merchant = 'shopee' ORDER BY id LIMIT 100`)
	if err != nil {
		t.Fatal(err)
	}
	var first, last, count, priced int64
	var sum, low, high float64
	for reference.Next() {
		var id int64
		var price sql.NullFloat64
		if err := reference.Scan(&id, &price); err != nil {
			reference.Close()
			t.Fatal(err)
		}
		if count == 0 {
			first = id
		}
		count++
		last = id
		if price.Valid {
			if priced == 0 || price.Float64 < low {
				low = price.Float64
			}
			if priced == 0 || price.Float64 > high {
				high = price.Float64
			}
			priced++
			sum += price.Float64
		}
	}
	err = reference.Err()
	reference.Close()
	if err != nil || count != 100 || priced == 0 {
		t.Fatalf("reference count=%d priced=%d: %v", count, priced, err)
	}
	query := `SELECT COUNT(*) AS matched, COUNT(b.price) AS priced, SUM(b.price) AS total_price,
	 AVG(b.price) AS mean_price, MIN(b.price) AS low, MAX(b.price) AS high
	 FROM shopping a JOIN shopping b ON a.merchant = b.merchant AND a.id = b.id
	 WHERE a.merchant = 'shopee' AND a.id >= $1 AND a.id <= $2 LIMIT 1`
	var timings []float64
	for run := 0; run < 3; run++ {
		start := time.Now()
		var gotCount, gotPriced int64
		var gotSum, gotMean, gotLow, gotHigh float64
		err := client.QueryRowContext(ctx, query, first, last).Scan(&gotCount, &gotPriced, &gotSum, &gotMean, &gotLow, &gotHigh)
		if err != nil {
			t.Fatal(err)
		}
		timings = append(timings, float64(time.Since(start).Microseconds())/1000)
		if gotCount != count || gotPriced != priced {
			t.Fatalf("JOIN counts=%d/%d reference=%d/%d", gotCount, gotPriced, count, priced)
		}
		for i, pair := range [][2]float64{{gotSum, sum}, {gotMean, sum / float64(priced)}, {gotLow, low}, {gotHigh, high}} {
			if math.Abs(pair[0]-pair[1]) > 1e-10*math.Max(1, math.Abs(pair[1])) {
				t.Fatalf("aggregate %d differs: %v", i, pair)
			}
		}
	}
	report := map[string]any{"source_rows": totalRows, "matched_rows": count, "reference_agrees": true,
		"query": query, "elapsed_ms": timings, "scope": "selective composite self-join, not a full-table JOIN or cold-cache benchmark"}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	t.Log(string(encoded))
	explained, err := client.QueryContext(ctx, "EXPLAIN ANALYZE "+query, first, last)
	if err != nil {
		t.Fatal(err)
	}
	defer explained.Close()
	for explained.Next() {
		var id int64
		var operation, detail string
		if err := explained.Scan(&id, &operation, &detail); err != nil {
			t.Fatal(err)
		}
		t.Logf("%s: %s", operation, detail)
	}
	if err := explained.Err(); err != nil {
		t.Fatal(err)
	}
	explained.Close()
	started := time.Now()
	var partial int64
	err = client.QueryRowContext(ctx, `SELECT COUNT(*) FROM shopping a
	 JOIN shopping b ON a.merchant = b.merchant AND a.id = b.id
	 WHERE a.merchant = 'shopee' LIMIT 1`).Scan(&partial)
	if err == nil || !strings.Contains(err.Error(), "10000-row input") {
		t.Fatalf("broad JOIN did not fail at its input budget: %v", err)
	}
	t.Logf("broad JOIN rejected without partial totals at 10000-row input budget in %.3f ms", float64(time.Since(started).Microseconds())/1000)
	if err := client.QueryRowContext(ctx, "SELECT 1").Scan(&partial); err != nil || partial != 1 {
		t.Fatalf("connection after budget error: %d, %v", partial, err)
	}
}
