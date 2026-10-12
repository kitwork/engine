package kitdb

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func activateConflictJournal(t *testing.T, db *DB) {
	t.Helper()
	base, err := db.LastTransaction()
	if err != nil {
		t.Fatal(err)
	}
	tx := mustBegin(t, db)
	if err := tx.Put([]byte("activation"), []byte("1")); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.CommitWithDependencies(base, CommitDependencies{}); err != nil {
		t.Fatal(err)
	}
}

func TestCommitDependenciesPhysicalConflictMatrix(t *testing.T) {
	for _, scenario := range []struct {
		name         string
		dependencies CommitDependencies
		mutations    []string
		conflict     bool
	}{
		{"disjoint", CommitDependencies{Keys: [][]byte{[]byte("read")}}, []string{"other"}, false},
		{"read-miss", CommitDependencies{Keys: [][]byte{[]byte("missing")}}, []string{"missing"}, true},
		{"blind-write", CommitDependencies{}, []string{"output"}, true},
		{"aba", CommitDependencies{Keys: [][]byte{[]byte("read")}}, []string{"read", "read"}, true},
		{"empty-range", CommitDependencies{Ranges: []RangeOptions{{Prefix: []byte("range/")}}}, []string{"range/new"}, true},
		{"start-inclusive", CommitDependencies{Ranges: []RangeOptions{{Start: []byte("b"), End: []byte("d")}}}, []string{"b"}, true},
		{"end-exclusive", CommitDependencies{Ranges: []RangeOptions{{Start: []byte("b"), End: []byte("d")}}}, []string{"d"}, false},
		{"prefix-intersection", CommitDependencies{Ranges: []RangeOptions{{Start: []byte("a"), End: []byte("d"), Prefix: []byte("b")}}}, []string{"a1"}, false},
		{"full-range", CommitDependencies{Ranges: []RangeOptions{{}}}, []string{"any"}, true},
		{"coarse", CommitDependencies{Coarse: true}, []string{"other"}, true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			db := mustOpen(t, filepath.Join(t.TempDir(), "conflicts.kitdb"))
			defer db.Close()
			activateConflictJournal(t, db)
			base, _ := db.LastTransaction()
			pending := mustBegin(t, db)
			if err := pending.Put([]byte("output"), []byte("pending")); err != nil {
				t.Fatal(err)
			}
			for _, key := range scenario.mutations {
				commitPut(t, db, key, "committed")
			}
			_, err := pending.CommitWithDependencies(base, scenario.dependencies)
			if scenario.conflict {
				if !errors.Is(err, ErrTransactionConflict) {
					t.Fatalf("conflict=%v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			db.mu.RLock()
			retained := db.activeTxBytes
			db.mu.RUnlock()
			if retained != 0 {
				t.Fatalf("admission bytes retained=%d", retained)
			}
		})
	}
}

func TestCommitDependenciesValidatesEarlierBatchRequests(t *testing.T) {
	for _, dependent := range []bool{false, true} {
		t.Run(fmt.Sprint(dependent), func(t *testing.T) {
			db := mustOpen(t, filepath.Join(t.TempDir(), "batch.kitdb"))
			defer db.Close()
			base, _ := db.LastTransaction()
			firstOps := []operation{{kind: operationPut, key: []byte("first"), value: []byte("1")}}
			secondOps := []operation{{kind: operationPut, key: []byte("second"), value: []byte("2")}}
			dependencies := CommitDependencies{}
			if dependent {
				dependencies.Keys = [][]byte{[]byte("first")}
			}
			first := &commitRequest{snapshot: &base, operations: firstOps, validation: prepareCommitValidation(CommitDependencies{}, firstOps), result: make(chan commitResult, 1)}
			second := &commitRequest{snapshot: &base, operations: secondOps, validation: prepareCommitValidation(dependencies, secondOps), result: make(chan commitResult, 1)}
			db.commitBatch([]*commitRequest{first, second})
			if result := <-first.result; result.err != nil {
				t.Fatal(result.err)
			}
			result := <-second.result
			if dependent && !errors.Is(result.err, ErrTransactionConflict) {
				t.Fatalf("batch dependency missed: %v", result.err)
			}
			if !dependent && (result.err != nil || result.transaction != base+2) {
				t.Fatalf("disjoint batch: %+v", result)
			}
			value, found, err := db.Get([]byte("second"))
			if err != nil || found == dependent || (!dependent && string(value) != "2") {
				t.Fatalf("partial publication=%q/%v/%v", value, found, err)
			}
		})
	}
}

func TestCommitDependenciesCoverageExpiryAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "retention.kitdb")
	db := mustOpen(t, path)
	defer func() { _ = db.Close() }()
	activateConflictJournal(t, db)
	base, _ := db.LastTransaction()
	for i := 0; i <= maximumConflictJournalFrames; i++ {
		commitPut(t, db, "noise", "x")
	}
	pending := mustBegin(t, db)
	if err := pending.Put([]byte("output"), []byte("x")); err != nil {
		t.Fatal(err)
	}
	if _, err := pending.CommitWithDependencies(base, CommitDependencies{}); !errors.Is(err, ErrTransactionConflict) {
		t.Fatalf("expired coverage accepted: %v", err)
	}
	if db.conflicts.count > maximumConflictJournalFrames || db.conflicts.keys > maximumConflictJournalKeys || db.conflicts.bytes > maximumConflictJournalBytes {
		t.Fatal("journal exceeded bounds")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if db.conflicts.entries != nil {
		t.Fatal("close retained journal")
	}
	db = mustOpen(t, path)
	if db.conflicts.entries != nil {
		t.Fatal("journal persisted across reopen")
	}
	base, _ = db.LastTransaction()
	commitPut(t, db, "later", "x")
	pending = mustBegin(t, db)
	if err := pending.Put([]byte("output"), []byte("x")); err != nil {
		t.Fatal(err)
	}
	if _, err := pending.CommitWithDependencies(base, CommitDependencies{}); !errors.Is(err, ErrTransactionConflict) {
		t.Fatalf("untracked mutation after reopen accepted: %v", err)
	}
}

func TestCommitDependenciesCatalogAndDeleteFence(t *testing.T) {
	db := mustOpen(t, filepath.Join(t.TempDir(), "catalog.kitdb"))
	defer db.Close()
	activateConflictJournal(t, db)
	base, _ := db.LastTransaction()
	defined := mustBegin(t, db)
	if err := defined.DefineStruct(testCatalogDefinition(productsCatalogID, "products", "v1")); err != nil {
		t.Fatal(err)
	}
	if _, err := defined.Commit(); err != nil {
		t.Fatal(err)
	}
	pending := mustBegin(t, db)
	if err := pending.Put([]byte("output"), []byte("x")); err != nil {
		t.Fatal(err)
	}
	if _, err := pending.CommitWithDependencies(base, CommitDependencies{}); !errors.Is(err, ErrTransactionConflict) {
		t.Fatalf("catalog fence=%v", err)
	}
	base, _ = db.LastTransaction()
	deleted := mustBegin(t, db)
	if err := deleted.Delete([]byte("missing")); err != nil {
		t.Fatal(err)
	}
	if _, err := deleted.Commit(); err != nil {
		t.Fatal(err)
	}
	pending = mustBegin(t, db)
	if err := pending.Put([]byte("output"), []byte("x")); err != nil {
		t.Fatal(err)
	}
	if _, err := pending.CommitWithDependencies(base, CommitDependencies{Keys: [][]byte{[]byte("missing")}}); !errors.Is(err, ErrTransactionConflict) {
		t.Fatalf("tombstone dependency=%v", err)
	}
}

func TestCommitDependenciesCopiesBoundsAndFallsBack(t *testing.T) {
	key, start := []byte("key"), []byte("a")
	validation := prepareCommitValidation(CommitDependencies{Keys: [][]byte{key}, Ranges: []RangeOptions{{Start: start, End: []byte("z"), Limit: 1, Reverse: true}}}, nil)
	key[0], start[0] = 'X', 'X'
	if _, ok := validation.keys["key"]; !ok || string(validation.ranges[0].Start) != "a" || validation.ranges[0].Limit != 0 || validation.ranges[0].Reverse {
		t.Fatal("dependency evidence aliases caller or narrows range")
	}
	if prepareCommitValidation(CommitDependencies{Keys: [][]byte{bytes.Repeat([]byte("x"), maximumCommitDependencyBytes+1)}}, nil) != nil {
		t.Fatal("dependency byte cap ignored")
	}
	journal := conflictJournal{}
	journal.activate(10)
	oversized := &commitRequest{operations: []operation{{key: bytes.Repeat([]byte("x"), maximumConflictJournalBytes+1)}}}
	journal.record(11, oversized)
	if journal.floor != 11 || journal.count != 0 || journal.bytes != 0 {
		t.Fatal("oversized mutation did not drop coverage")
	}
	if !journal.conflicts(10, validation) {
		t.Fatal("missing coverage accepted")
	}
}

func TestCommitDependenciesDefaultCommitsDoNotActivateJournal(t *testing.T) {
	db := mustOpen(t, filepath.Join(t.TempDir(), "default.kitdb"))
	defer db.Close()
	commitPut(t, db, "key", "value")
	if db.conflicts.entries != nil {
		t.Fatal("ordinary commit allocated dependency history")
	}
}

func TestCommitDependenciesConcurrentWriters(t *testing.T) {
	for _, sharedReads := range []bool{false, true} {
		t.Run(fmt.Sprint(sharedReads), func(t *testing.T) {
			db := mustOpen(t, filepath.Join(t.TempDir(), "concurrent.kitdb"))
			defer db.Close()
			activateConflictJournal(t, db)
			base, _ := db.LastTransaction()
			start := make(chan struct{})
			results := make(chan error, 2)
			for _, key := range []string{"left", "right"} {
				tx := mustBegin(t, db)
				if err := tx.Put([]byte(key), []byte("value")); err != nil {
					t.Fatal(err)
				}
				dependencies := CommitDependencies{}
				if sharedReads {
					dependencies.Keys = [][]byte{[]byte("left"), []byte("right")}
				}
				go func() { <-start; _, err := tx.CommitWithDependencies(base, dependencies); results <- err }()
			}
			close(start)
			succeeded, conflicts := 0, 0
			for range 2 {
				select {
				case err := <-results:
					if err == nil {
						succeeded++
					} else if errors.Is(err, ErrTransactionConflict) {
						conflicts++
					} else {
						t.Fatal(err)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("commit blocked")
				}
			}
			want := 2
			if sharedReads {
				want = 1
			}
			if succeeded != want || succeeded+conflicts != 2 {
				t.Fatalf("success=%d conflict=%d", succeeded, conflicts)
			}
			last, _ := db.LastTransaction()
			if last != base+uint64(want) {
				t.Fatal("rejected transaction consumed durable identity")
			}
		})
	}
}

func TestCommitDependenciesAdmissionAndFutureBoundary(t *testing.T) {
	db := mustOpen(t, filepath.Join(t.TempDir(), "admission.kitdb"))
	defer db.Close()
	tx := mustBegin(t, db)
	if err := tx.Put([]byte("output"), []byte("value")); err != nil {
		t.Fatal(err)
	}
	db.mu.Lock()
	before := db.activeTxBytes
	db.activeTxBytes = maxPayloadSize
	db.mu.Unlock()
	if _, err := tx.CommitWithDependencies(0, CommitDependencies{}); !errors.Is(err, ErrActiveTransactionMemory) {
		t.Fatalf("admission=%v", err)
	}
	if tx.done {
		t.Fatal("admission failure consumed transaction")
	}
	db.mu.Lock()
	db.activeTxBytes = before
	db.mu.Unlock()
	if _, err := tx.CommitWithDependencies(100, CommitDependencies{}); !errors.Is(err, ErrTransactionConflict) {
		t.Fatalf("future snapshot=%v", err)
	}
	if db.activeTxBytes != 0 {
		t.Fatal("rejected validation retained admission bytes")
	}
}

func TestCommitDependenciesSyncFailureDoesNotPublishJournal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "uncertain.kitdb")
	db := mustOpen(t, path)
	defer func() { _ = db.Close() }()
	activateConflictJournal(t, db)
	base, _ := db.LastTransaction()
	count := db.conflicts.count
	db.mu.Lock()
	db.wal = &syncFailWAL{commitWAL: db.wal, failure: errors.New("injected sync failure")}
	db.mu.Unlock()
	tx := mustBegin(t, db)
	if err := tx.Put([]byte("uncertain"), []byte("value")); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.CommitWithDependencies(base, CommitDependencies{}); !errors.Is(err, ErrDurabilityUncertain) {
		t.Fatalf("sync failure=%v", err)
	}
	if db.conflicts.count != count {
		t.Fatal("uncertain commit published dependency history")
	}
	if _, _, err := db.Get([]byte("uncertain")); !errors.Is(err, ErrUnavailable) {
		t.Fatal("uncertain handle remained readable")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db = mustOpen(t, path)
	if string(requireValue(t, db, "uncertain")) != "value" {
		t.Fatal("recovery lost WAL frame")
	}
	if db.conflicts.entries != nil {
		t.Fatal("recovery treated journal as durable state")
	}
}

func TestCommitDependenciesJournalKeyAndByteBounds(t *testing.T) {
	for _, mode := range []string{"keys", "bytes"} {
		t.Run(mode, func(t *testing.T) {
			var journal conflictJournal
			journal.activate(0)
			keyCount, keySize := maximumConflictJournalKeys, 1
			if mode == "bytes" {
				keyCount, keySize = maximumConflictJournalBytes/maxKeySize, maxKeySize
			}
			operations := make([]operation, keyCount)
			for i := range operations {
				operations[i] = operation{key: bytes.Repeat([]byte("k"), keySize)}
			}
			journal.record(1, &commitRequest{operations: operations})
			if journal.floor != 0 || journal.count != 1 {
				t.Fatal("journal rejected exact cap")
			}
			journal.record(2, &commitRequest{operations: []operation{{key: []byte("next")}}})
			if journal.floor != 1 || journal.count != 1 || journal.keys != 1 || journal.bytes != 4 {
				t.Fatalf("eviction=%+v", journal)
			}
		})
	}
}

func TestCommitDependenciesNormalizationEntryBounds(t *testing.T) {
	keys := make([][]byte, maximumCommitDependencyKeys)
	for i := range keys {
		keys[i] = []byte(fmt.Sprint(i))
	}
	if validation := prepareCommitValidation(CommitDependencies{Keys: keys}, nil); validation == nil || len(validation.keys) != len(keys) {
		t.Fatal("key limit rejected early")
	}
	if prepareCommitValidation(CommitDependencies{Keys: keys}, []operation{{key: []byte("new-write")}}) != nil {
		t.Fatal("automatic write key bypassed limit")
	}
	ranges := make([]RangeOptions, maximumCommitDependencyRanges)
	for i := range ranges {
		ranges[i] = RangeOptions{Prefix: []byte(fmt.Sprint(i))}
	}
	if prepareCommitValidation(CommitDependencies{Ranges: ranges}, nil) == nil {
		t.Fatal("range limit rejected early")
	}
	ranges = append(ranges, RangeOptions{})
	if prepareCommitValidation(CommitDependencies{Ranges: ranges}, nil) != nil {
		t.Fatal("range limit ignored")
	}
}
