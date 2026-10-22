package relational

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	kitdbengine "github.com/kitwork/engine/kitdb"
)

func TestTransactionScanOwnedBuffers(t *testing.T) {
	tenant := fixedReadFixture(t)
	tx, err := tenant.engine.BeginTransaction(context.Background(), TransactionOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	rows, prefix := fixedReadRows(t, tenant, tx)
	var retained []fixedReadRow
	var observed kitdbengine.CursorStats
	err = tx.scan(kitdbengine.RangeOptions{Prefix: prefix}, &observed, func(key, value []byte) (bool, error) {
		retained = append(retained, fixedReadRow{key: key, value: value})
		return false, nil
	})
	if err != nil || len(retained) != len(rows) {
		t.Fatalf("rows=%d err=%v", len(retained), err)
	}
	for i, row := range retained {
		if !bytes.Equal(row.key, rows[i].key) || !bytes.Equal(row.value, rows[i].value) {
			t.Fatalf("retained row %d overwritten", i)
		}
	}
	wantStats, err := tx.snapshot.Scan(kitdbengine.RangeOptions{Prefix: prefix}, func(_, _ []byte) (bool, error) { return false, nil })
	if err != nil || observed.PagesRead != wantStats.PagesRead || observed.PageBytesRead != wantStats.PageBytesRead || observed.PageRecordsDecoded != wantStats.PageRecordsDecoded {
		t.Fatalf("page evidence changed: %+v want %+v: %v", observed, wantStats, err)
	}
	// Mutating a public visitor's bytes must not corrupt a reusable source page.
	seen := 0
	err = tx.Scan(kitdbengine.RangeOptions{Prefix: prefix}, func(key, value []byte) (bool, error) {
		at := seen
		seen++
		if !bytes.Equal(key, rows[at].key) || !bytes.Equal(value, rows[at].value) {
			return false, fmt.Errorf("mutation scan row %d", at)
		}
		key[0] ^= 0xff
		value[0] ^= 0xff
		got, found, err := tx.Get(rows[at].key)
		if err != nil || !found || !bytes.Equal(got, rows[at].value) {
			return false, fmt.Errorf("public mutation reached snapshot: %v", err)
		}
		return false, nil
	})
	if err != nil || seen != len(rows) {
		t.Fatalf("mutation rows=%d err=%v", seen, err)
	}
	for _, limit := range []int{1, 127, 128, 129} {
		seen = 0
		err = tx.Scan(kitdbengine.RangeOptions{Prefix: prefix, Limit: limit}, func(key, value []byte) (bool, error) { seen++; return false, nil })
		if err != nil || seen != limit {
			t.Fatalf("limit %d rows=%d: %v", limit, seen, err)
		}
	}
	sentinel := errors.New("visitor failure")
	err = tx.Scan(kitdbengine.RangeOptions{Prefix: prefix}, func(_, _ []byte) (bool, error) { return false, sentinel })
	if !errors.Is(err, sentinel) {
		t.Fatalf("visitor error lost: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	for i, row := range retained {
		if !bytes.Equal(row.key, rows[i].key) || !bytes.Equal(row.value, rows[i].value) {
			t.Fatalf("closed reader changed retained row %d", i)
		}
	}
	t.Logf("full scan: pages=%d bytes=%d decoded=%d retained=%d", observed.PagesRead, observed.PageBytesRead, observed.PageRecordsDecoded, len(retained))
}

func TestTransactionScanCallbackCheckpoint(t *testing.T) {
	tenant := fixedReadFixture(t)
	ctx := context.Background()
	tx, err := tenant.engine.BeginTransaction(ctx, TransactionOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	rows, prefix := fixedReadRows(t, tenant, tx)
	seen := 0
	err = tx.Scan(kitdbengine.RangeOptions{Prefix: prefix}, func(key, value []byte) (bool, error) {
		at := seen
		seen++
		if !bytes.Equal(key, rows[at].key) || !bytes.Equal(value, rows[at].value) {
			return false, fmt.Errorf("snapshot row %d changed", at)
		}
		if at == 0 {
			if _, err := tenant.engine.Execute(ctx, "UPDATE items SET version=999,bucket=39 WHERE id=900"); err != nil {
				return false, err
			}
			if _, err := tenant.lease.DB().Checkpoint(); err != nil {
				return false, err
			}
		}
		return false, nil
	})
	if err != nil || seen != len(rows) {
		t.Fatalf("checkpoint rows=%d: %v", seen, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	result, err := tenant.engine.Execute(ctx, "SELECT version FROM items WHERE id=900")
	if err != nil || len(result.Rows) != 1 || result.Rows[0][0] != int64(999) {
		t.Fatalf("new view=%v: %v", result.Rows, err)
	}
}

func TestTransactionScanOwnWritesBuffers(t *testing.T) {
	tenant := fixedReadFixture(t)
	ctx := context.Background()
	tx, err := tenant.engine.BeginTransaction(ctx, TransactionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Execute(ctx, "UPDATE items SET version=123,bucket=59 WHERE id=0"); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Execute(ctx, "DELETE FROM items WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	tenant.expected[0] = 123
	rows, prefix := fixedReadRows(t, tenant, tx)
	filtered := rows[:0]
	for _, row := range rows {
		if row.id != 1 {
			filtered = append(filtered, row)
		}
	}
	rows = filtered
	for _, reverse := range []bool{false, true} {
		seen := 0
		err = tx.Scan(kitdbengine.RangeOptions{Prefix: prefix, Reverse: reverse}, func(key, value []byte) (bool, error) {
			at := seen
			if reverse {
				at = len(rows) - 1 - seen
			}
			seen++
			if !bytes.Equal(key, rows[at].key) || !bytes.Equal(value, rows[at].value) {
				return false, fmt.Errorf("own write row %d", at)
			}
			key[0] ^= 0xff
			value[0] ^= 0xff
			return false, nil
		})
		if err != nil || seen != len(rows) {
			t.Fatalf("own writes reverse=%t rows=%d: %v", reverse, seen, err)
		}
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	stats, err := tenant.lease.DB().Stats()
	if err != nil || mixedDrainOf(stats) != (mixedDrain{}) {
		t.Fatalf("rollback did not drain: %v %+v", err, mixedDrainOf(stats))
	}
}

func TestTransactionScanConcurrentPrivateBuffers(t *testing.T) {
	tenant := fixedReadFixture(t)
	tx, err := tenant.engine.BeginTransaction(context.Background(), TransactionOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	rows, prefix := fixedReadRows(t, tenant, tx)
	var workers sync.WaitGroup
	failures := make(chan error, 4)
	for range 4 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			seen := 0
			err := tx.Scan(kitdbengine.RangeOptions{Prefix: prefix}, func(key, value []byte) (bool, error) {
				at := seen
				seen++
				if !bytes.Equal(key, rows[at].key) || !bytes.Equal(value, rows[at].value) {
					return false, fmt.Errorf("shared buffer at row %d", at)
				}
				key[0] ^= 0xff
				value[0] ^= 0xff
				return false, nil
			})
			if err == nil && seen != len(rows) {
				err = fmt.Errorf("rows=%d", seen)
			}
			failures <- err
		}()
	}
	workers.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestTransactionScanCallbackClose(t *testing.T) {
	tenant := fixedReadFixture(t)
	for _, stop := range []bool{false, true} {
		tx, err := tenant.engine.BeginTransaction(context.Background(), TransactionOptions{ReadOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		_, prefix := fixedReadRows(t, tenant, tx)
		err = tx.Scan(kitdbengine.RangeOptions{Prefix: prefix}, func(_, _ []byte) (bool, error) { return stop, tx.Rollback() })
		_ = tx.Rollback()
		if stop && err != nil || !stop && !errors.Is(err, kitdbengine.ErrSnapshotClosed) {
			t.Fatalf("close stop=%t: %v", stop, err)
		}
	}
}

func TestTransactionScanLargePageBuffers(t *testing.T) {
	tenant := fixedReadFixture(t)
	ctx := context.Background()
	large := strings.Repeat("large-row:", 32<<10)
	if _, err := tenant.engine.Execute(ctx, "UPDATE items SET payload=$1 WHERE id=0", large); err != nil {
		t.Fatal(err)
	}
	if _, err := tenant.lease.DB().Checkpoint(); err != nil {
		t.Fatal(err)
	}
	tx, err := tenant.engine.BeginTransaction(ctx, TransactionOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	rows, prefix := fixedReadRows(t, tenant, tx)
	schema, err := tx.schema("items")
	if err != nil {
		t.Fatal(err)
	}
	for i := range rows {
		if rows[i].id == 0 {
			version := tenant.expected[0]
			rows[i].value, err = encodeRow(schema, map[string]any{"id": int64(0), "version": version, "bucket": version % 64, "payload": large}, nil)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	var retained [][]byte
	err = tx.Scan(kitdbengine.RangeOptions{Prefix: prefix}, func(key, value []byte) (bool, error) {
		at := len(retained)
		if !bytes.Equal(key, rows[at].key) || !bytes.Equal(value, rows[at].value) {
			return false, fmt.Errorf("large-page row %d", at)
		}
		retained = append(retained, value)
		return false, nil
	})
	if err != nil || len(retained) != len(rows) {
		t.Fatalf("large-page rows=%d: %v", len(retained), err)
	}
	for i, value := range retained {
		if !bytes.Equal(value, rows[i].value) {
			t.Fatalf("large-page retained row %d overwritten", i)
		}
	}
}

func TestTransactionScanReadDependency(t *testing.T) {
	tenant := fixedReadFixture(t)
	ctx := context.Background()
	tx, err := tenant.engine.BeginTransaction(ctx, TransactionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	_, prefix := fixedReadRows(t, tenant, tx)
	if err := tx.Scan(kitdbengine.RangeOptions{Prefix: prefix}, func(_, _ []byte) (bool, error) { return false, nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := tenant.engine.Execute(ctx, "UPDATE items SET version=999,bucket=39 WHERE id=900"); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Execute(ctx, "UPDATE items SET version=123,bucket=59 WHERE id=0"); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Commit(ctx); !errors.Is(err, ErrTransactionConflict) {
		t.Fatalf("range read dependency lost: %v", err)
	}
}

func TestTransactionScanAllocationEnvelope(t *testing.T) {
	tenant := fixedReadFixture(t)
	tx, err := tenant.engine.BeginTransaction(context.Background(), TransactionOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	rows, prefix := fixedReadRows(t, tenant, tx)
	allocations := testing.AllocsPerRun(3, func() {
		seen := 0
		err := tx.Scan(kitdbengine.RangeOptions{Prefix: prefix}, func(key, value []byte) (bool, error) {
			at := seen
			seen++
			if !bytes.Equal(key, rows[at].key) || !bytes.Equal(value, rows[at].value) {
				return false, fmt.Errorf("row %d", at)
			}
			return false, nil
		})
		if err != nil || seen != len(rows) {
			t.Fatalf("rows=%d err=%v", seen, err)
		}
	})
	// Two caller-owned byte slices per row plus bounded iterator/page scratch.
	// The former copying cursor uses over 4,200 objects on this exact fixture.
	if allocations > 2300 {
		t.Fatalf("forward scan allocations=%.0f exceed 2300", allocations)
	}
	t.Logf("allocations=%.0f rows=%d", allocations, len(rows))
}

func TestFixedReadFixtureStaysImmutable(t *testing.T) {
	tenant := fixedReadFixture(t)
	before, err := tenant.lease.DB().Stats()
	if err != nil {
		t.Fatal(err)
	}
	tx, err := tenant.engine.BeginTransaction(context.Background(), TransactionOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	rows, prefix := fixedReadRows(t, tenant, tx)
	for range 3 {
		seen := 0
		if err := tx.Scan(kitdbengine.RangeOptions{Prefix: prefix}, func(_, _ []byte) (bool, error) { seen++; return false, nil }); err != nil || seen != len(rows) {
			t.Fatalf("rows=%d: %v", seen, err)
		}
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	after, err := tenant.lease.DB().Stats()
	if err != nil {
		t.Fatal(err)
	}
	if before.OverlayMutations != 0 || after.OverlayMutations != 0 || before.LastTransaction != after.LastTransaction || before.CheckpointTransaction != after.CheckpointTransaction || before.MainSegments != after.MainSegments || before.MainBlocks != after.MainBlocks || before.MainMutations != after.MainMutations || before.MainRecords != after.MainRecords {
		t.Fatalf("fixed reads changed source: before=%+v after=%+v", before, after)
	}
	t.Logf("segments=%d blocks=%d transaction=%d overlay=%d logical_rows=%d", after.MainSegments, after.MainBlocks, after.LastTransaction, after.OverlayMutations, len(rows))
}
