package relational

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	kitdbengine "github.com/kitwork/engine/kitdb"
)

func TestTransactionDependenciesPointReadsAndWrites(t *testing.T) {
	engine := concurrencyOpen(t, filepath.Join(t.TempDir(), "dependencies.kitdb"))
	transaction := concurrencyBegin(t, engine)
	key := []byte("missing")
	if _, found, err := transaction.Get(key); err != nil || found {
		t.Fatalf("missing read: found=%v err=%v", found, err)
	}
	key[0] = 'X'
	if transaction.dependencies.keys["missing"] != dependencyRead {
		t.Fatal("missing read was not retained independently of caller memory")
	}
	var stats kitdbengine.CursorStats
	if _, _, err := transaction.getWithStats([]byte("missing"), &stats); err != nil {
		t.Fatal(err)
	}
	if _, _, err := transaction.getWithStats([]byte("another"), &stats); err != nil {
		t.Fatal(err)
	}
	if err := transaction.Put([]byte("missing"), []byte("own")); err != nil {
		t.Fatal(err)
	}
	value, found, err := transaction.Get([]byte("missing"))
	if err != nil || !found || string(value) != "own" {
		t.Fatalf("overlay read=%q/%v/%v", value, found, err)
	}
	if err := transaction.Delete([]byte("write-only")); err != nil {
		t.Fatal(err)
	}
	if got := transaction.dependencies.keys["missing"]; got != dependencyRead|dependencyWrite {
		t.Fatalf("access=%d", got)
	}
	if got := transaction.dependencies.keys["another"]; got != dependencyRead {
		t.Fatalf("stats read access=%d", got)
	}
	if got := transaction.dependencies.keys["write-only"]; got != dependencyWrite {
		t.Fatalf("blind write access=%d", got)
	}
	if len(transaction.dependencies.keys) != 3 || transaction.dependencies.bytes != len("missinganotherwrite-only") {
		t.Fatalf("unexpected deduplication: %+v", transaction.dependencies)
	}
	if err := transaction.Rollback(); err != nil {
		t.Fatal(err)
	}
	assertDependenciesReleased(t, transaction)
}

func TestTransactionDependenciesRangesProtectEmptyAndLimitedScans(t *testing.T) {
	engine := concurrencyOpen(t, filepath.Join(t.TempDir(), "ranges.kitdb"))
	transaction := concurrencyBegin(t, engine)
	empty := kitdbengine.RangeOptions{Start: []byte("empty/"), End: []byte("empty0"), Prefix: []byte("empty/")}
	if err := transaction.Scan(empty, func(_, _ []byte) (bool, error) { t.Fatal("unexpected row"); return true, nil }); err != nil {
		t.Fatal(err)
	}
	if err := transaction.scanKeys(empty, nil, func(_ []byte) (bool, error) { t.Fatal("unexpected key"); return true, nil }); err != nil {
		t.Fatal(err)
	}
	if len(transaction.dependencies.ranges) != 1 {
		t.Fatal("empty range not recorded/deduplicated")
	}
	empty.Start[0] = 'X'
	if string(transaction.dependencies.ranges[0].Start) != "empty/" {
		t.Fatal("range bound aliases caller memory")
	}
	for _, key := range []string{"row/a", "row/b"} {
		if err := transaction.Put([]byte(key), []byte("value")); err != nil {
			t.Fatal(err)
		}
	}
	limited := kitdbengine.RangeOptions{Start: []byte("row/"), End: []byte("row0"), Prefix: []byte("row/"), Limit: 1, Reverse: true}
	visited := 0
	if err := transaction.Scan(limited, func(_, _ []byte) (bool, error) { visited++; return false, nil }); err != nil {
		t.Fatal(err)
	}
	if visited != 1 {
		t.Fatalf("limited visits=%d", visited)
	}
	var stats kitdbengine.CursorStats
	if err := transaction.scanKeys(limited, &stats, func(_ []byte) (bool, error) { return true, nil }); err != nil {
		t.Fatal(err)
	}
	limited.Limit, limited.Reverse = 0, false
	if err := transaction.Scan(limited, func(_, _ []byte) (bool, error) { return true, nil }); err != nil {
		t.Fatal(err)
	}
	if len(transaction.dependencies.ranges) != 2 {
		t.Fatalf("range count=%d", len(transaction.dependencies.ranges))
	}
	protected := transaction.dependencies.ranges[1]
	if protected.Limit != 0 || protected.Reverse || string(protected.End) != "row0" {
		t.Fatalf("range narrowed by consumption: %+v", protected)
	}
}

func TestTransactionDependenciesSurviveSavepointAndFailedStatement(t *testing.T) {
	engine := concurrencyOpen(t, filepath.Join(t.TempDir(), "savepoint.kitdb"),
		"CREATE TABLE items(id INTEGER PRIMARY KEY, email TEXT UNIQUE)", "INSERT INTO items VALUES(1, 'existing')")
	transaction := concurrencyBegin(t, engine)
	point, err := transaction.Savepoint()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := transaction.Get([]byte("decision")); err != nil {
		t.Fatal(err)
	}
	if err := transaction.Put([]byte("temporary"), []byte("value")); err != nil {
		t.Fatal(err)
	}
	if err := transaction.RollbackTo(point); err != nil {
		t.Fatal(err)
	}
	if len(transaction.operations) != 0 || len(transaction.dependencies.keys) != 2 {
		t.Fatal("savepoint discarded dependency evidence")
	}
	ctx := context.Background()
	if _, err := transaction.Execute(ctx, "INSERT INTO items VALUES(2, 'new'), (3, 'existing')"); err == nil {
		t.Fatal("unique violation accepted")
	}
	if len(transaction.operations) != 0 {
		t.Fatal("failed statement retained mutations")
	}
	if len(transaction.dependencies.keys) <= 2 {
		t.Fatal("failed statement discarded constraint reads")
	}
	if _, err := transaction.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	assertDependenciesReleased(t, transaction)
	concurrencyCheckRows(t, engine, "SELECT id FROM items", [][]any{{int64(1)}})
}

func TestTransactionDependenciesReadOnlyDoesNotAllocate(t *testing.T) {
	engine := concurrencyOpen(t, filepath.Join(t.TempDir(), "read-only.kitdb"))
	transaction, err := engine.BeginTransaction(context.Background(), TransactionOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer transaction.Rollback()
	if _, _, err := transaction.Get([]byte("missing")); err != nil {
		t.Fatal(err)
	}
	var stats kitdbengine.CursorStats
	if _, _, err := transaction.getWithStats([]byte("missing"), &stats); err != nil {
		t.Fatal(err)
	}
	if err := transaction.Scan(kitdbengine.RangeOptions{}, func(_, _ []byte) (bool, error) { return true, nil }); err != nil {
		t.Fatal(err)
	}
	if err := transaction.scanKeys(kitdbengine.RangeOptions{}, nil, func(_ []byte) (bool, error) { return true, nil }); err != nil {
		t.Fatal(err)
	}
	assertDependenciesReleased(t, transaction)
}

func TestTransactionDependenciesConcurrentReads(t *testing.T) {
	engine := concurrencyOpen(t, filepath.Join(t.TempDir(), "concurrent-reads.kitdb"))
	transaction := concurrencyBegin(t, engine)
	var workers sync.WaitGroup
	errorsCh := make(chan error, 4)
	for worker := range 4 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for i := range 16 {
				if _, _, err := transaction.Get([]byte(fmt.Sprintf("missing/%d/%d", worker, i))); err != nil {
					errorsCh <- err
					return
				}
			}
		}()
	}
	workers.Wait()
	close(errorsCh)
	for err := range errorsCh {
		t.Fatal(err)
	}
	if len(transaction.dependencies.keys) != 64 {
		t.Fatalf("concurrent read count=%d", len(transaction.dependencies.keys))
	}
}

func TestTransactionDependenciesBoundsAndStickyFallback(t *testing.T) {
	for _, mode := range []string{"keys", "ranges", "bytes"} {
		t.Run(mode, func(t *testing.T) {
			var dependencies transactionDependencies
			switch mode {
			case "keys":
				for i := 0; i < maximumTransactionDependencyKeys; i++ {
					dependencies.recordKey([]byte(fmt.Sprint(i)), dependencyRead)
				}
				if dependencies.coarse || len(dependencies.keys) != maximumTransactionDependencyKeys {
					t.Fatal("key ceiling rejected early")
				}
				dependencies.recordKey([]byte("0"), dependencyWrite)
				if dependencies.coarse {
					t.Fatal("duplicate exceeded key ceiling")
				}
				dependencies.recordKey([]byte("overflow"), dependencyRead)
			case "ranges":
				for i := 0; i < maximumTransactionDependencyRanges; i++ {
					dependencies.recordRange(kitdbengine.RangeOptions{Prefix: []byte(fmt.Sprint(i))})
				}
				if dependencies.coarse || len(dependencies.ranges) != maximumTransactionDependencyRanges {
					t.Fatal("range ceiling rejected early")
				}
				dependencies.recordRange(kitdbengine.RangeOptions{Prefix: []byte("0"), Limit: 1, Reverse: true})
				if dependencies.coarse {
					t.Fatal("duplicate exceeded range ceiling")
				}
				dependencies.recordRange(kitdbengine.RangeOptions{Prefix: []byte("overflow")})
			case "bytes":
				dependencies.recordRange(kitdbengine.RangeOptions{Start: bytes.Repeat([]byte("x"), maximumTransactionDependencyBytes)})
				if dependencies.coarse || dependencies.bytes != maximumTransactionDependencyBytes {
					t.Fatal("byte ceiling rejected early")
				}
				dependencies.recordKey([]byte("x"), dependencyRead)
			}
			if !dependencies.coarse || dependencies.keys != nil || dependencies.ranges != nil || dependencies.bytes != 0 {
				t.Fatalf("fallback retained storage: %+v", dependencies)
			}
			dependencies.recordKey([]byte("later"), dependencyWrite)
			dependencies.recordRange(kitdbengine.RangeOptions{})
			if dependencies.keys != nil || dependencies.ranges != nil || !dependencies.coarse {
				t.Fatal("fallback was not sticky")
			}
		})
	}
}

func TestTransactionDependenciesFallbackKeepsConflictFence(t *testing.T) {
	engine := concurrencyOpen(t, filepath.Join(t.TempDir(), "fallback.kitdb"),
		"CREATE TABLE items(id INTEGER PRIMARY KEY, value INTEGER)", "INSERT INTO items VALUES(1, 10)")
	transaction := concurrencyBegin(t, engine)
	transaction.mu.Lock()
	transaction.dependencies.fallback()
	transaction.mu.Unlock()
	ctx := context.Background()
	if _, err := transaction.Execute(ctx, "UPDATE items SET value=20 WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, "INSERT INTO items VALUES(2, 30)"); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Commit(ctx); !errors.Is(err, ErrTransactionConflict) {
		t.Fatalf("fallback conflict=%v", err)
	}
	assertDependenciesReleased(t, transaction)
}

func TestTransactionDependenciesSharedByteBudget(t *testing.T) {
	var dependencies transactionDependencies
	key := bytes.Repeat([]byte("k"), 256)
	dependencies.recordKey(key, dependencyRead)
	dependencies.recordRange(kitdbengine.RangeOptions{Prefix: bytes.Repeat([]byte("p"), maximumTransactionDependencyBytes-len(key))})
	if dependencies.coarse || dependencies.bytes != maximumTransactionDependencyBytes {
		t.Fatal("shared byte budget rejected at its exact ceiling")
	}
	dependencies.recordKey(key, dependencyWrite)
	if dependencies.coarse {
		t.Fatal("access upgrade charged bytes twice")
	}
	dependencies.recordRange(kitdbengine.RangeOptions{Start: []byte("extra")})
	if !dependencies.coarse || dependencies.bytes != 0 {
		t.Fatal("shared byte budget did not fall back")
	}
	var oversized transactionDependencies
	oversized.recordKey(bytes.Repeat([]byte("x"), maximumTransactionDependencyBytes+1), dependencyRead)
	if !oversized.coarse || oversized.keys != nil {
		t.Fatal("oversized point was retained")
	}
}

func assertDependenciesReleased(t *testing.T, transaction *Transaction) {
	t.Helper()
	dependencies := transaction.dependencies
	if dependencies.keys != nil || dependencies.ranges != nil || dependencies.bytes != 0 || dependencies.coarse {
		t.Fatalf("retained dependency state: %+v", dependencies)
	}
}

func TestTransactionDependenciesReleaseOnCommitOutcomes(t *testing.T) {
	for _, mode := range []string{"success", "cancel", "conflict"} {
		t.Run(mode, func(t *testing.T) {
			engine := concurrencyOpen(t, filepath.Join(t.TempDir(), "release.kitdb"), "CREATE TABLE items(id INTEGER PRIMARY KEY)")
			transaction := concurrencyBegin(t, engine)
			if _, _, err := transaction.Get([]byte("row/new")); err != nil {
				t.Fatal(err)
			}
			if err := transaction.Put([]byte("row/new"), []byte("value")); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var want error
			if mode == "cancel" {
				cancel()
				want = context.Canceled
			}
			if mode == "conflict" {
				other := concurrencyBegin(t, engine)
				if err := other.Put([]byte("row/new"), []byte("other")); err != nil {
					t.Fatal(err)
				}
				if _, err := other.Commit(ctx); err != nil {
					t.Fatal(err)
				}
				want = ErrTransactionConflict
			}
			if _, err := transaction.Commit(ctx); !errors.Is(err, want) {
				t.Fatalf("commit error=%v want=%v", err, want)
			}
			assertDependenciesReleased(t, transaction)
			concurrencyNoSnapshots(t, engine)
		})
	}
}

func TestTransactionDependenciesRepeatedAccessAllocationEnvelope(t *testing.T) {
	var dependencies transactionDependencies
	key := []byte("row/repeated")
	options := kitdbengine.RangeOptions{Start: []byte("row/"), End: []byte("row0")}
	dependencies.recordKey(key, dependencyRead)
	dependencies.recordRange(options)
	allocations := testing.AllocsPerRun(100, func() {
		dependencies.recordKey(key, dependencyRead)
		dependencies.recordRange(options)
	})
	if allocations != 0 {
		t.Fatalf("repeated access allocations=%v", allocations)
	}
}

func BenchmarkTransactionDependencies(b *testing.B) {
	b.Run("point-repeat", func(b *testing.B) {
		var dependencies transactionDependencies
		key := []byte("row/example")
		dependencies.recordKey(key, dependencyRead)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			dependencies.recordKey(key, dependencyRead)
		}
	})
	b.Run("range-repeat", func(b *testing.B) {
		var dependencies transactionDependencies
		options := kitdbengine.RangeOptions{Start: []byte("a"), End: []byte("z")}
		dependencies.recordRange(options)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			dependencies.recordRange(options)
		}
	})
	b.Run("point-set-256", func(b *testing.B) {
		keys := make([][]byte, 256)
		for i := range keys {
			keys[i] = []byte(fmt.Sprint(i))
		}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			var dependencies transactionDependencies
			for _, key := range keys {
				dependencies.recordKey(key, dependencyRead)
			}
		}
	})
}
