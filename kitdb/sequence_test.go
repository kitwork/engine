package kitdb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestSequenceConcurrentDurableReservations(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "sequence.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	_, err = db.CreateSequence(ctx, Sequence{Name: "orders", Start: 1, Increment: 1, Minimum: 1, Maximum: math.MaxInt64})
	if err != nil {
		t.Fatal(err)
	}
	catalog, _ := db.CatalogVersion()
	const workers, perWorker = 12, 20
	values := make(chan int64, workers*perWorker)
	errorsCh := make(chan error, workers)
	var wait sync.WaitGroup
	for range workers {
		wait.Go(func() {
			for range perWorker {
				_, value, err := db.NextSequence(ctx, "orders")
				if err != nil {
					errorsCh <- err
					return
				}
				values <- value
			}
		})
	}
	wait.Wait()
	close(values)
	close(errorsCh)
	for err := range errorsCh {
		t.Fatal(err)
	}
	seen := map[int64]bool{}
	for value := range values {
		if value < 1 || value > workers*perWorker || seen[value] {
			t.Fatalf("invalid/duplicate value %d", value)
		}
		seen[value] = true
	}
	if len(seen) != workers*perWorker {
		t.Fatal(len(seen))
	}
	after, _ := db.CatalogVersion()
	if after.Revision != catalog.Revision || after.Transaction <= catalog.Transaction {
		t.Fatalf("catalog reservation boundary: %+v %+v", catalog, after)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(db.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, value, err := db.NextSequence(ctx, "orders"); err != nil || value != workers*perWorker+1 {
		t.Fatalf("reopen: %d %v", value, err)
	}
}

func TestSequenceSnapshotCommitBoundary(t *testing.T) {
	db, err := OpenWithOptions(filepath.Join(t.TempDir(), "conditional.kitdb"), OpenOptions{GroupCommitDelay: 5 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	sequence, err := db.CreateSequence(ctx, Sequence{Name: "s", Start: 1, Increment: 1, Minimum: 1, Maximum: 100})
	if err != nil {
		t.Fatal(err)
	}
	base, _ := db.LastTransaction()
	if _, _, err := db.NextSequence(ctx, "s"); err != nil {
		t.Fatal(err)
	}
	tx := mustBegin(t, db)
	_ = tx.Put([]byte("row/1"), []byte("one"))
	if _, err := tx.CommitIfUnchanged(base); err != nil {
		t.Fatalf("reservation incorrectly conflicts: %v", err)
	}
	base, _ = db.LastTransaction()
	const count = 8
	errorsCh := make(chan error, count)
	for i := range count {
		tx := mustBegin(t, db)
		_ = tx.Put([]byte(fmt.Sprintf("row/%d", i)), []byte("x"))
		go func() { _, err := tx.CommitIfUnchanged(base); errorsCh <- err }()
	}
	succeeded := 0
	for range count {
		err := <-errorsCh
		if err == nil {
			succeeded++
		} else if !errors.Is(err, ErrTransactionConflict) {
			t.Fatal(err)
		}
	}
	if succeeded != 1 {
		t.Fatalf("same-snapshot commits succeeded: %d", succeeded)
	}
	// A raw caller cannot opt out of conflicts simply by writing a counter key.
	base, _ = db.LastTransaction()
	_, key := sequenceKeys(sequence.ID)
	raw := mustBegin(t, db)
	_ = raw.Put(key, encodeSequenceCounter(50, true))
	if _, err := raw.Commit(); err != nil {
		t.Fatal(err)
	}
	tx = mustBegin(t, db)
	_ = tx.Put([]byte("row/stale"), []byte("bad"))
	if _, err := tx.CommitIfUnchanged(base); !errors.Is(err, ErrTransactionConflict) {
		t.Fatal(err)
	}
}

func TestSequenceBoundsAndCounterCorruption(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "bounds.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	for _, sequence := range []Sequence{
		{Name: "up", Start: math.MaxInt64, Increment: 1, Minimum: 1, Maximum: math.MaxInt64},
		{Name: "down", Start: math.MinInt64, Increment: -1, Minimum: math.MinInt64, Maximum: -1},
		{Name: "huge", Start: -1, Increment: math.MinInt64, Minimum: math.MinInt64, Maximum: -1},
	} {
		if _, err := db.CreateSequence(ctx, sequence); err != nil {
			t.Fatal(err)
		}
		if _, value, err := db.NextSequence(ctx, sequence.Name); err != nil || value != sequence.Start {
			t.Fatalf("first: %d %v", value, err)
		}
		if _, _, err := db.NextSequence(ctx, sequence.Name); !errors.Is(err, ErrSequenceLimit) {
			t.Fatal(err)
		}
	}
	sequence, err := db.CreateSequence(ctx, Sequence{Name: "cycle", Start: 3, Increment: 2, Minimum: 1, Maximum: 3, Cycle: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []int64{3, 1, 3, 1} {
		if _, value, err := db.NextSequence(ctx, "cycle"); err != nil || value != want {
			t.Fatalf("cycle: %d %v", value, err)
		}
	}
	_, key := sequenceKeys(sequence.ID)
	tx := mustBegin(t, db)
	_ = tx.Put(key, []byte("broken"))
	if _, err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.NextSequence(ctx, "cycle"); !errors.Is(err, ErrCorrupt) {
		t.Fatal(err)
	}
}

func TestSequenceTypedBounds(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "typed-bounds.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	for _, sequence := range []Sequence{
		{Name: "small", DataType: "smallint", Start: math.MaxInt16, Increment: 1, Minimum: math.MinInt16, Maximum: math.MaxInt16},
		{Name: "regular", DataType: "integer", Start: math.MaxInt32, Increment: 1, Minimum: math.MinInt32, Maximum: math.MaxInt32},
	} {
		created, err := db.CreateSequence(ctx, sequence)
		if err != nil {
			t.Fatalf("create %s: %v", sequence.Name, err)
		}
		if created.DataTypeName() != sequence.DataType {
			t.Fatalf("%s type = %q", sequence.Name, created.DataTypeName())
		}
		if _, got, err := db.NextSequence(ctx, sequence.Name); err != nil || got != sequence.Start {
			t.Fatalf("%s first = %d, %v", sequence.Name, got, err)
		}
		if _, _, err := db.NextSequence(ctx, sequence.Name); !errors.Is(err, ErrSequenceLimit) {
			t.Fatalf("%s exceeded type maximum: %v", sequence.Name, err)
		}
	}
	for _, sequence := range []Sequence{
		{Name: "unknown", DataType: "int32", Start: 1, Increment: 1, Minimum: 1, Maximum: math.MaxInt32},
		{Name: "wide-small", DataType: "smallint", Start: 1, Increment: 1, Minimum: 1, Maximum: math.MaxInt32},
		{Name: "wide-regular", DataType: "integer", Start: 1, Increment: 1, Minimum: 1, Maximum: math.MaxInt64},
	} {
		if _, err := db.CreateSequence(ctx, sequence); !errors.Is(err, ErrInvalidCatalog) {
			t.Fatalf("accepted invalid typed sequence %+v: %v", sequence, err)
		}
	}
}

func TestSequenceSyncBeforeAcknowledgement(t *testing.T) {
	db := mustOpen(t, filepath.Join(t.TempDir(), "sync.kitdb"))
	defer db.Close()
	ctx := context.Background()
	_, err := db.CreateSequence(ctx, Sequence{Name: "s", Start: 1, Increment: 1, Minimum: 1, Maximum: 100})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := db.LastTransaction()
	blocked := &blockingSyncWAL{commitWAL: db.wal, entered: make(chan struct{}), release: make(chan struct{})}
	db.mu.Lock()
	db.wal = blocked
	db.mu.Unlock()
	defer func() {
		select {
		case <-blocked.release:
		default:
			close(blocked.release)
		}
	}()
	done := make(chan error, 1)
	go func() { _, _, err := db.NextSequence(ctx, "s"); done <- err }()
	select {
	case <-blocked.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("sync not entered")
	}
	select {
	case err := <-done:
		t.Fatalf("ack before sync: %v", err)
	default:
	}
	after, _ := db.LastTransaction()
	if after != before {
		t.Fatal("published before sync")
	}
	close(blocked.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	failure := errors.New("injected sequence fsync failure")
	db.mu.Lock()
	db.wal = &syncFailWAL{commitWAL: blocked.commitWAL, failure: failure}
	db.mu.Unlock()
	got, value, err := db.NextSequence(ctx, "s")
	if !errors.Is(err, ErrDurabilityUncertain) || got.ID != "" || value != 0 {
		t.Fatalf("uncertain reservation returned usable value: %+v %d %v", got, value, err)
	}
	if _, _, err := db.NextSequence(ctx, "s"); err == nil {
		t.Fatal("continued after uncertain sync")
	}
}

func TestSequencePointInTimeRestore(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "source.kitdb")
	db := mustOpenWithHistory(t, path)
	defer db.Close()
	ctx := context.Background()
	if _, err := db.CreateSequence(ctx, Sequence{Name: "s", Start: 1, Increment: 1, Minimum: 1, Maximum: 100}); err != nil {
		t.Fatal(err)
	}
	anchor := filepath.Join(root, "anchor.kitdb")
	if _, err := db.CreateBackupAnchor(ctx, anchor); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.NextSequence(ctx, "s"); err != nil {
		t.Fatal(err)
	}
	boundary, _ := db.LastTransaction()
	if _, _, err := db.SetSequence(ctx, "s", 50, true); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(root, "restored.kitdb")
	if _, err := RestoreToTransaction(ctx, anchor, databaseHistoryPath(path), destination, boundary); err != nil {
		t.Fatal(err)
	}
	restored := mustOpen(t, destination)
	defer restored.Close()
	if _, value, err := restored.NextSequence(ctx, "s"); err != nil || value != 2 {
		t.Fatalf("restore: %d %v", value, err)
	}
	if _, value, err := db.NextSequence(ctx, "s"); err != nil || value != 51 {
		t.Fatalf("source changed: %d %v", value, err)
	}
}

func TestSequenceCacheLeaseLifecycle(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "cached.kitdb")
	db := mustOpen(t, path)
	ctx := context.Background()
	sequence, err := db.CreateSequence(ctx, Sequence{
		Name: "cached", Start: 1, Increment: 1, Minimum: 1, Maximum: 1000, Cache: 4,
	})
	if err != nil || sequence.CacheSize() != 4 {
		t.Fatalf("create: %+v %v", sequence, err)
	}
	base, _ := db.LastTransaction()
	for want := int64(1); want <= 10; want++ {
		if _, got, err := db.NextSequence(ctx, "cached"); err != nil || got != want {
			t.Fatalf("next %d: %d %v", want, got, err)
		}
	}
	after, _ := db.LastTransaction()
	if after-base != 3 {
		t.Fatalf("ten calls used %d durable leases, want 3", after-base)
	}
	anchor := filepath.Join(root, "anchor.kitdb")
	if _, err := db.CreateBackupAnchor(ctx, anchor); err != nil {
		t.Fatal(err)
	}
	backup := mustOpen(t, anchor)
	if _, got, err := backup.NextSequence(ctx, "cached"); err != nil || got != 13 {
		t.Fatalf("backup high-watermark: %d %v", got, err)
	}
	if err := backup.Close(); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db = mustOpen(t, path)
	defer db.Close()
	if _, got, err := db.NextSequence(ctx, "cached"); err != nil || got != 13 {
		t.Fatalf("reopen high-watermark: %d %v", got, err)
	}
	if _, _, err := db.SetSequence(ctx, "cached", 50, false); err != nil {
		t.Fatal(err)
	}
	if _, got, err := db.NextSequence(ctx, "cached"); err != nil || got != 50 {
		t.Fatalf("setval did not discard lease: %d %v", got, err)
	}
	if err := db.RestartSequence(ctx, "cached", nil); err != nil {
		t.Fatal(err)
	}
	if _, got, err := db.NextSequence(ctx, "cached"); err != nil || got != 1 {
		t.Fatalf("restart did not discard lease: %d %v", got, err)
	}
	if err := db.DropSequence(ctx, "cached"); err != nil {
		t.Fatal(err)
	}
	if len(db.sequenceLeases) != 0 {
		t.Fatalf("drop retained leases: %+v", db.sequenceLeases)
	}
	if _, err := db.CreateSequence(ctx, Sequence{Name: "cached", Start: 7, Increment: 1, Minimum: 1, Maximum: 1000, Cache: 4}); err != nil {
		t.Fatal(err)
	}
	if _, got, err := db.NextSequence(ctx, "cached"); err != nil || got != 7 {
		t.Fatalf("recreated sequence inherited a lease: %d %v", got, err)
	}
}

func TestSequenceCacheReadsLegacyDefinition(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-cache.kitdb")
	db := mustOpen(t, path)
	legacy := Sequence{
		Version: 1, ID: "00112233445566778899aabbccddeeff", Name: "legacy",
		Start: 1, Increment: 1, Minimum: 1, Maximum: 100,
	}
	definition, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(definition, []byte(`"cache"`)) || bytes.Contains(definition, []byte(`"dataType"`)) {
		t.Fatalf("legacy encoding gained optional fields: %s", definition)
	}
	key, counter := sequenceKeys(legacy.ID)
	tx := mustBegin(t, db)
	if err := tx.Put(key, definition); err != nil {
		t.Fatal(err)
	}
	if err := tx.Put(counter, encodeSequenceCounter(legacy.Start, false)); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	loaded, err := db.SequenceByName("legacy")
	if err != nil || loaded.Cache != 0 || loaded.CacheSize() != 1 || loaded.DataTypeName() != "bigint" {
		t.Fatalf("legacy sequence: %+v %v", loaded, err)
	}
	if _, got, err := db.NextSequence(context.Background(), "legacy"); err != nil || got != 1 {
		t.Fatalf("legacy nextval: %d %v", got, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db = mustOpen(t, path)
	defer db.Close()
	if _, got, err := db.NextSequence(context.Background(), "legacy"); err != nil || got != 2 {
		t.Fatalf("legacy reopen: %d %v", got, err)
	}
}

func TestSequenceCacheBoundsCycleAndConcurrency(t *testing.T) {
	db := mustOpen(t, filepath.Join(t.TempDir(), "bounds-cache.kitdb"))
	defer db.Close()
	ctx := context.Background()
	for _, sequence := range []Sequence{
		{Name: "bounded", Start: 1, Increment: 1, Minimum: 1, Maximum: 3, Cache: 64},
		{Name: "descending", Start: 5, Increment: -2, Minimum: 1, Maximum: 5, Cache: 64},
		{Name: "cycling", Start: 1, Increment: 1, Minimum: 1, Maximum: 3, Cache: 4, Cycle: true},
	} {
		if _, err := db.CreateSequence(ctx, sequence); err != nil {
			t.Fatal(err)
		}
	}
	for _, want := range []int64{1, 2, 3} {
		if _, got, err := db.NextSequence(ctx, "bounded"); err != nil || got != want {
			t.Fatalf("bounded: %d %v", got, err)
		}
	}
	if _, _, err := db.NextSequence(ctx, "bounded"); !errors.Is(err, ErrSequenceLimit) {
		t.Fatalf("bounded limit: %v", err)
	}
	for _, want := range []int64{5, 3, 1} {
		if _, got, err := db.NextSequence(ctx, "descending"); err != nil || got != want {
			t.Fatalf("descending: %d %v", got, err)
		}
	}
	for _, want := range []int64{1, 2, 3, 1, 2, 3} {
		if _, got, err := db.NextSequence(ctx, "cycling"); err != nil || got != want {
			t.Fatalf("cycle: got %d want %d: %v", got, want, err)
		}
	}
	if _, err := db.CreateSequence(ctx, Sequence{Name: "concurrent", Start: 1, Increment: 1, Minimum: 1, Maximum: 1000, Cache: 64}); err != nil {
		t.Fatal(err)
	}
	base, _ := db.LastTransaction()
	const workers, each = 8, 30
	values := make(chan int64, workers*each)
	errorsCh := make(chan error, workers)
	var wait sync.WaitGroup
	for range workers {
		wait.Go(func() {
			for range each {
				_, value, err := db.NextSequence(ctx, "concurrent")
				if err != nil {
					errorsCh <- err
					return
				}
				values <- value
			}
		})
	}
	wait.Wait()
	close(values)
	close(errorsCh)
	for err := range errorsCh {
		t.Fatal(err)
	}
	seen := make(map[int64]bool, workers*each)
	for value := range values {
		if value < 1 || value > workers*each || seen[value] {
			t.Fatalf("duplicate or invalid concurrent value %d", value)
		}
		seen[value] = true
	}
	after, _ := db.LastTransaction()
	if after-base != 4 {
		t.Fatalf("240 calls used %d durable leases, want 4", after-base)
	}
}

func TestSequenceCacheCrashSkipsUnservedLease(t *testing.T) {
	if path := os.Getenv("KITDB_SEQUENCE_CACHE_CRASH_CHILD"); path != "" {
		db := mustOpen(t, path)
		ctx := context.Background()
		if _, err := db.CreateSequence(ctx, Sequence{Name: "cached", Start: 1, Increment: 1, Minimum: 1, Maximum: 100, Cache: 8}); err != nil {
			t.Fatal(err)
		}
		if _, got, err := db.NextSequence(ctx, "cached"); err != nil || got != 1 {
			t.Fatalf("first lease value: %d %v", got, err)
		}
		os.Exit(43) // Hard exit after acknowledgement, without DB.Close.
	}
	path := filepath.Join(t.TempDir(), "crash-cache.kitdb")
	child := exec.Command(os.Args[0], "-test.run=^TestSequenceCacheCrashSkipsUnservedLease$")
	child.Env = append(os.Environ(), "KITDB_SEQUENCE_CACHE_CRASH_CHILD="+path)
	output, err := child.CombinedOutput()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 43 {
		t.Fatalf("child: %v %s", err, output)
	}
	db := mustOpen(t, path)
	defer db.Close()
	if _, got, err := db.NextSequence(context.Background(), "cached"); err != nil || got != 9 {
		t.Fatalf("post-crash high-watermark: %d %v", got, err)
	}
}
