package relational

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	kitdbengine "github.com/kitwork/engine/kitdb"
)

const secondaryReadSQL = "SELECT id,bucket,payload FROM items WHERE bucket=$1 ORDER BY id LIMIT 16"

var secondaryReadPayload = strings.Repeat("x", 512)

func secondaryReadOracle(result Result, bucket int64) error {
	if len(result.Rows) != 16 {
		return fmt.Errorf("secondary rows=%d, want 16", len(result.Rows))
	}
	for i, row := range result.Rows {
		if len(row) != 3 || row[0] != bucket+int64(i*8) || row[1] != bucket || row[2] != secondaryReadPayload {
			return fmt.Errorf("secondary row %d differs: %v", i, row)
		}
	}
	return nil
}

func BenchmarkSecondaryRead(b *testing.B) {
	for _, policy := range []struct {
		name  string
		cache int64
	}{{"warm", 256 << 10}, {"disabled", -1}} {
		b.Run(policy.name, func(b *testing.B) {
			e := pointSQLFixtureWithCache(b, 1024, policy.cache)
			ctx := context.Background()
			for bucket := range 8 {
				if _, err := e.Execute(ctx, secondaryReadSQL, bucket); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := range b.N {
				bucket := int64(i % 8)
				result, err := e.Execute(ctx, secondaryReadSQL, bucket)
				if err != nil {
					b.Fatal(err)
				}
				if err := secondaryReadOracle(result, bucket); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestSecondaryScanOwnedBuffers(t *testing.T) {
	e := pointSQLFixture(t, 2048)
	ctx := context.Background()
	tx, err := e.BeginTransaction(ctx, TransactionOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	schema, err := tx.schema("items")
	if err != nil {
		t.Fatal(err)
	}
	conditions, err := bindConditions(schema, primaryStatement(t, secondaryReadSQL).Select.Conditions, []any{int64(0)})
	if err != nil {
		t.Fatal(err)
	}
	access, err := tx.planRowAccess(schema, 0, conditions, nil)
	if err != nil || access.kind != rowAccessSecondary {
		t.Fatalf("access=%+v: %v", access, err)
	}
	options := access.options
	cursor, err := tx.snapshot.Cursor(options)
	if err != nil {
		t.Fatal(err)
	}
	var want [][2][]byte
	for cursor.Next() {
		want = append(want, [2][]byte{cursor.Key(), cursor.Value()})
	}
	if err := cursor.Err(); err != nil {
		t.Fatal(err)
	}
	cursor.Close()
	if len(want) != 256 {
		t.Fatalf("index entries=%d", len(want))
	}
	var retained [][2][]byte
	var stats kitdbengine.CursorStats
	err = tx.scan(options, &stats, func(key, value []byte) (bool, error) {
		retained = append(retained, [2][]byte{key, value})
		return false, nil
	})
	if err != nil || !reflect.DeepEqual(retained, want) {
		t.Fatalf("retained scan: %v", err)
	}
	if stats.PagesRead < 2 {
		t.Fatalf("fixture did not cross pages: %+v", stats)
	}
	seen := 0
	err = tx.Scan(options, func(key, value []byte) (bool, error) {
		if !bytes.Equal(key, want[seen][0]) || !bytes.Equal(value, want[seen][1]) {
			return false, fmt.Errorf("row %d changed", seen)
		}
		seen++
		key[0] ^= 0xff
		value[0] ^= 0xff
		return false, nil
	})
	if err != nil || seen != len(want) || !reflect.DeepEqual(retained, want) {
		t.Fatalf("mutation rows=%d: %v", seen, err)
	}
	for _, limit := range []int{0, 1, 127, 128, 129} {
		rangeOptions := options
		rangeOptions.Limit = limit
		seen = 0
		count := len(want)
		if limit > 0 {
			count = limit
		}
		if err := tx.Scan(rangeOptions, func(_, _ []byte) (bool, error) { seen++; return false, nil }); err != nil || seen != count {
			t.Fatalf("limit=%d seen=%d: %v", limit, seen, err)
		}
	}
	sentinel := errors.New("secondary visitor")
	if err := tx.Scan(options, func(_, _ []byte) (bool, error) { return false, sentinel }); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(retained, want) {
		t.Fatal("closing scan owner changed retained data")
	}
	result, err := e.Execute(ctx, secondaryReadSQL, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := secondaryReadOracle(result, 0); err != nil {
		t.Fatal(err)
	}
	for _, stop := range []bool{false, true} {
		reader, err := e.BeginTransaction(ctx, TransactionOptions{ReadOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		err = reader.Scan(options, func(_, _ []byte) (bool, error) { return stop, reader.Rollback() })
		_ = reader.Rollback()
		if stop && err != nil || !stop && !errors.Is(err, kitdbengine.ErrSnapshotClosed) {
			t.Fatalf("close stop=%t: %v", stop, err)
		}
	}
	requirePrimaryHandoffReleased(t, e)
}

func TestSecondaryReadPolicies(t *testing.T) {
	for _, policy := range []struct {
		name  string
		cache int64
	}{{"warm", 256 << 10}, {"disabled", -1}} {
		t.Run(policy.name, func(t *testing.T) {
			e := pointSQLFixtureWithCache(t, 1024, policy.cache)
			ctx := context.Background()
			for range 2 {
				if _, err := e.Execute(ctx, secondaryReadSQL, 0); err != nil {
					t.Fatal(err)
				}
			}
			observed, err := e.Execute(ctx, "EXPLAIN ANALYZE "+secondaryReadSQL, 0)
			if err != nil || observed.Execution == nil {
				t.Fatal(err)
			}
			stats := observed.Execution
			if stats.PointLookups != 16 || stats.IndexEntriesScanned != 16 || stats.RowsScanned != 16 {
				t.Fatalf("wrong index work: %+v", stats)
			}
			if policy.cache > 0 && (stats.PageCacheHits < 16 || stats.PageCacheMisses != 0) {
				t.Fatalf("cache not warm: %+v", stats)
			}
			if policy.cache < 0 && (stats.PageCacheHits != 0 || stats.PagesRead == 0) {
				t.Fatalf("cache not disabled: %+v", stats)
			}
			for _, tc := range []struct {
				query string
				ids   []int64
			}{
				{"SELECT id FROM items WHERE bucket=0 ORDER BY id LIMIT 4 OFFSET 3", []int64{24, 32, 40, 48}},
				{"SELECT id FROM items WHERE bucket=0 ORDER BY id DESC LIMIT 4", []int64{1016, 1008, 1000, 992}},
				{"SELECT id FROM items WHERE bucket=0 AND id>=32 ORDER BY id LIMIT 4", []int64{32, 40, 48, 56}},
				{"SELECT id FROM items WHERE bucket=0 ORDER BY id LIMIT 0", []int64{}},
			} {
				result, err := e.Execute(ctx, tc.query)
				if err != nil || len(result.Rows) != len(tc.ids) {
					t.Fatalf("%s: rows=%v err=%v", tc.query, result.Rows, err)
				}
				for i, id := range tc.ids {
					if result.Rows[i][0] != id {
						t.Fatalf("%s: rows=%v", tc.query, result.Rows)
					}
				}
			}
			result, err := e.Execute(ctx, secondaryReadSQL, 0)
			if err != nil {
				t.Fatal(err)
			}
			result.Rows[0][2] = "caller mutation"
			again, err := e.Execute(ctx, secondaryReadSQL, 0)
			if err != nil {
				t.Fatal(err)
			}
			if err := secondaryReadOracle(again, 0); err != nil {
				t.Fatal(err)
			}
			cancelled, cancel := context.WithCancel(ctx)
			cancel()
			if _, err := e.Execute(cancelled, secondaryReadSQL, 0); !errors.Is(err, context.Canceled) {
				t.Fatalf("cancel: %v", err)
			}
			allocs := testing.AllocsPerRun(20, func() {
				result, err := e.Execute(ctx, secondaryReadSQL, 0)
				if err != nil {
					t.Fatal(err)
				}
				if err := secondaryReadOracle(result, 0); err != nil {
					t.Fatal(err)
				}
			})
			// Baseline cursor envelopes include race/background accounting.
			// The stricter copy-elimination experiment was withdrawn pending
			// Windows WAL publication and mixed-tail qualification.
			maximum := 460.0
			if policy.cache < 0 {
				maximum = 480
			}
			if allocs > maximum {
				t.Fatalf("secondary allocations=%.0f exceed %.0f", allocs, maximum)
			}
			t.Logf("secondary allocations=%.0f cache_hits=%d pages=%d", allocs, stats.PageCacheHits, stats.PagesRead)
			requirePrimaryHandoffReleased(t, e)
		})
	}
}

func TestSecondaryReadConcurrentCheckpoint(t *testing.T) {
	e := pointSQLFixture(t, 1024)
	ctx := context.Background()
	tx, err := e.BeginTransaction(ctx, TransactionOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var workers sync.WaitGroup
	failures := make(chan error, 5)
	start := make(chan struct{})
	for worker := range 4 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			for i := range 16 {
				bucket := int64((worker + i) % 8)
				result, err := tx.Execute(ctx, secondaryReadSQL, bucket)
				if err == nil {
					err = secondaryReadOracle(result, bucket)
				}
				if err != nil {
					failures <- err
					return
				}
			}
		}()
	}
	workers.Add(1)
	go func() {
		defer workers.Done()
		<-start
		for i := range 8 {
			if _, err := e.Execute(ctx, "UPDATE items SET payload='new' WHERE id=$1", i); err != nil {
				failures <- err
				return
			}
			// The kernel checkpoint is snapshot-safe; the facade intentionally
			// waits for lifetime-held transactions before checkpointing.
			if _, err := e.database.Checkpoint(); err != nil {
				failures <- err
				return
			}
		}
	}()
	close(start)
	workers.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	requirePrimaryHandoffReleased(t, e)
}

func TestSecondaryReadRangeDependency(t *testing.T) {
	e := pointSQLFixture(t, 64)
	ctx := context.Background()
	tx, err := e.BeginTransaction(ctx, TransactionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	result, err := tx.Execute(ctx, secondaryReadSQL, 0)
	if err != nil || len(result.Rows) != 8 {
		t.Fatalf("rows=%v: %v", result.Rows, err)
	}
	if _, err := e.Execute(ctx, "INSERT INTO items VALUES(1024,0,'phantom')"); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Execute(ctx, "UPDATE items SET payload='local' WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Commit(ctx); !errors.Is(err, ErrTransactionConflict) {
		t.Fatalf("index range dependency lost: %v", err)
	}
	requirePrimaryHandoffReleased(t, e)
}
