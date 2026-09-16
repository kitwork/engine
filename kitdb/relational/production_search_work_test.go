//go:build searchwork

package relational

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/kitwork/engine/search"
)

// Work counts are deliberately separate from latency benchmarks: diagnostic
// atomics change timing. "Cold" below means a fresh reader/DF cache, not disk.
func TestShoppingProductionPackedSearchWork(t *testing.T) {
	path := os.Getenv("KITDB_SHOPPING_PACKED_SEARCH_BENCHMARK")
	if path == "" {
		t.Skip("set KITDB_SHOPPING_PACKED_SEARCH_BENCHMARK to a verified copy")
	}
	for _, workload := range shoppingSearchWorkloads() {
		t.Run(workload.name, func(t *testing.T) {
			options := Options{ExperimentalProjections: true, SearchReaderCacheBytes: 256 << 20,
				MaximumResultRows: 1000, MaximumSearchResults: 1000, MaximumSearchCandidates: 1_000_000}
			options.Kernel.Replica = true
			engine, err := OpenWithOptions(path, options)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := engine.Close(); err != nil {
					t.Error(err)
				}
			})
			transaction, err := engine.database.LastTransaction()
			if err != nil {
				t.Fatal(err)
			}
			var reference search.WorkSnapshot
			var digest [32]byte
			for pass := range 4 {
				ctx, observer := search.ObserveWork(context.Background())
				result, err := engine.Execute(ctx, workload.query)
				if err != nil {
					t.Fatal(err)
				}
				if len(result.Rows) != workload.rows || result.Execution == nil || result.Execution.Path != "search-snapshot" {
					t.Fatalf("rows=%d execution=%+v", len(result.Rows), result.Execution)
				}
				got := shoppingSearchDigest(t, result)
				if pass == 0 {
					digest = got
				} else if digest != got {
					t.Fatal("ranked results changed")
				}
				counts := observer.Snapshot()
				checkShoppingWorkTotals(t, counts.Ranking)
				checkShoppingWorkTotals(t, counts.Frequency)
				if counts.Ranking.DecodedPostings == 0 {
					t.Fatal("posting instrumentation was not reached")
				}
				if pass == 1 {
					reference = counts
				}
				if pass > 1 && counts != reference {
					t.Fatalf("warm work changed: got=%+v want=%+v", counts, reference)
				}
				if pass < 2 {
					encoded, err := json.Marshal(counts)
					if err != nil {
						t.Fatal(err)
					}
					t.Logf("pass=%d transaction=%d rows=%d result_sha256=%x work=%s", pass, transaction, len(result.Rows), got, encoded)
				}
			}
			plain, err := engine.Execute(context.Background(), workload.query)
			if err != nil {
				t.Fatal(err)
			}
			if shoppingSearchDigest(t, plain) != digest {
				t.Fatal("observer changed ranked results")
			}
			if after, err := engine.database.LastTransaction(); err != nil || after != transaction {
				t.Fatalf("source transaction changed: %d err=%v", after, err)
			}
		})
	}
}

func checkShoppingWorkTotals(t *testing.T, counts search.WorkCounts) {
	t.Helper()
	var integers, bytes uint64
	for width, count := range counts.VarintWidths {
		integers += count
		bytes += uint64(width+1) * count
	}
	if integers != counts.DecodedPostings*2+counts.PositionVarints || bytes != counts.DecodedPayloadBytes {
		t.Fatalf("varint/posting/payload work does not reconcile: %+v", counts)
	}
	if counts.Headers != counts.DecodedBlocks+counts.SkippedTargetBlocks+counts.SkippedScoreBlocks ||
		counts.ReadRequests != counts.ReadAheadHits+counts.ReaderCalls {
		t.Fatalf("block/read work does not reconcile: %+v", counts)
	}
}
