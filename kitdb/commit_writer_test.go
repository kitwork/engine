package kitdb

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestConcurrentCommitsShareOneWALSync(t *testing.T) {
	const transactions = 16
	path := filepath.Join(t.TempDir(), "tenant.kitdb")
	db, err := OpenWithOptions(path, OpenOptions{
		CommitQueueSize:  transactions,
		MaxCommitBatch:   transactions,
		GroupCommitDelay: maximumGroupCommitWait,
	})
	if err != nil {
		t.Fatal(err)
	}

	counting := &countingSyncWAL{commitWAL: db.wal}
	db.mu.Lock()
	db.wal = counting
	db.mu.Unlock()

	prepared := make([]*Tx, transactions)
	for index := range prepared {
		prepared[index] = mustBegin(t, db)
		if err := prepared[index].Put(
			[]byte(fmt.Sprintf("product/%02d", index)),
			[]byte(fmt.Sprintf("value-%02d", index)),
		); err != nil {
			t.Fatal(err)
		}
	}

	type result struct {
		transaction uint64
		err         error
	}
	results := make(chan result, transactions)
	release := make(chan struct{})
	var ready sync.WaitGroup
	ready.Add(transactions)
	for _, transaction := range prepared {
		transaction := transaction
		go func() {
			ready.Done()
			<-release
			id, err := transaction.Commit()
			results <- result{transaction: id, err: err}
		}()
	}
	ready.Wait()
	close(release)

	ids := make([]int, 0, transactions)
	for range transactions {
		result := <-results
		if result.err != nil {
			t.Fatalf("concurrent commit %d: %v", result.transaction, result.err)
		}
		ids = append(ids, int(result.transaction))
	}
	sort.Ints(ids)
	for index, id := range ids {
		if id != index+1 {
			t.Fatalf("transaction IDs = %v", ids)
		}
	}
	if syncs := counting.syncs.Load(); syncs != 1 {
		t.Fatalf("WAL syncs = %d, want 1", syncs)
	}

	stats, err := db.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.CommitBatches != 1 || stats.CommittedTransactions != transactions || stats.CommitSyncs != 1 || stats.LargestCommitBatch != transactions {
		t.Fatalf("group commit stats = %+v", stats)
	}
	if stats.ActiveTransactions != 0 || stats.ActiveTransactionBytes != 0 || stats.PendingCommits != 0 {
		t.Fatalf("post-commit resources = %+v", stats)
	}
	for index := range prepared {
		key := fmt.Sprintf("product/%02d", index)
		want := fmt.Sprintf("value-%02d", index)
		if got := string(requireValue(t, db, key)); got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}

	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db = mustOpen(t, path)
	defer db.Close()
	if last, err := db.LastTransaction(); err != nil || last != transactions {
		t.Fatalf("recovered transaction = %d, %v; want %d", last, err, transactions)
	}
}

func TestActiveTransactionsAreBoundedAndReleased(t *testing.T) {
	const limit = 4
	db, err := OpenWithOptions(filepath.Join(t.TempDir(), "tenant.kitdb"), OpenOptions{
		CommitQueueSize: limit,
		MaxCommitBatch:  limit,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	transactions := make([]*Tx, limit)
	for index := range transactions {
		transactions[index] = mustBegin(t, db)
	}
	if _, err := db.Begin(); !errors.Is(err, ErrTooManyTransactions) {
		t.Fatalf("Begin above limit = %v, want ErrTooManyTransactions", err)
	}
	for _, transaction := range transactions {
		if err := transaction.Rollback(); err != nil {
			t.Fatal(err)
		}
	}
	stats, err := db.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.ActiveTransactions != 0 || stats.ActiveTransactionBytes != 0 {
		t.Fatalf("released transaction resources = %+v", stats)
	}
}

func TestGroupCommitSyncFailureIsResolvedByRecovery(t *testing.T) {
	const transactions = 4
	path := filepath.Join(t.TempDir(), "tenant.kitdb")
	db, err := OpenWithOptions(path, OpenOptions{
		CommitQueueSize:  transactions,
		MaxCommitBatch:   transactions,
		GroupCommitDelay: maximumGroupCommitWait,
	})
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("injected group sync failure")
	db.mu.Lock()
	db.wal = &syncFailWAL{commitWAL: db.wal, failure: failure}
	db.mu.Unlock()

	prepared := make([]*Tx, transactions)
	for index := range prepared {
		prepared[index] = mustBegin(t, db)
		if err := prepared[index].Put([]byte(fmt.Sprintf("key/%d", index)), []byte("value")); err != nil {
			t.Fatal(err)
		}
	}

	results := make(chan commitResult, transactions)
	release := make(chan struct{})
	var ready sync.WaitGroup
	ready.Add(transactions)
	for _, transaction := range prepared {
		transaction := transaction
		go func() {
			ready.Done()
			<-release
			id, err := transaction.Commit()
			results <- commitResult{transaction: id, err: err}
		}()
	}
	ready.Wait()
	close(release)

	ids := make([]int, 0, transactions)
	for range transactions {
		result := <-results
		if !errors.Is(result.err, ErrDurabilityUncertain) {
			t.Fatalf("group commit %d error = %v, want ErrDurabilityUncertain", result.transaction, result.err)
		}
		ids = append(ids, int(result.transaction))
	}
	sort.Ints(ids)
	for index, id := range ids {
		if id != index+1 {
			t.Fatalf("attempted transaction IDs = %v", ids)
		}
	}
	if _, _, err := db.Get([]byte("key/0")); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Get after group sync failure = %v, want ErrUnavailable", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db = mustOpen(t, path)
	defer db.Close()
	if last, err := db.LastTransaction(); err != nil || last != transactions {
		t.Fatalf("recovered transaction = %d, %v; want %d", last, err, transactions)
	}
	for index := range prepared {
		if got := string(requireValue(t, db, fmt.Sprintf("key/%d", index))); got != "value" {
			t.Fatalf("recovered key/%d = %q", index, got)
		}
	}
}

func TestGroupCommitOptionsAreBounded(t *testing.T) {
	tests := []OpenOptions{
		{CommitQueueSize: -1},
		{CommitQueueSize: maximumCommitQueueSize + 1},
		{CommitQueueSize: 2, MaxCommitBatch: 3},
		{MaxCommitBatch: -1},
		{GroupCommitDelay: -time.Nanosecond},
		{GroupCommitDelay: maximumGroupCommitWait + time.Nanosecond},
	}
	for index, options := range tests {
		if _, err := OpenWithOptions(filepath.Join(t.TempDir(), fmt.Sprintf("invalid-%d.kitdb", index)), options); err == nil {
			t.Fatalf("OpenWithOptions(%+v) succeeded", options)
		}
	}
}

func TestCloseDrainsAnAdmittedCommit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenant.kitdb")
	db := mustOpen(t, path)
	blocking := &blockingSyncWAL{
		commitWAL: db.wal,
		entered:   make(chan struct{}),
		release:   make(chan struct{}),
	}
	db.mu.Lock()
	db.wal = blocking
	db.mu.Unlock()

	transaction := mustBegin(t, db)
	if err := transaction.Put([]byte("accepted"), []byte("durable")); err != nil {
		t.Fatal(err)
	}
	committed := make(chan commitResult, 1)
	go func() {
		id, err := transaction.Commit()
		committed <- commitResult{transaction: id, err: err}
	}()
	<-blocking.entered

	closeResult := make(chan error, 1)
	go func() { closeResult <- db.Close() }()
	close(blocking.release)
	if result := <-committed; result.err != nil || result.transaction != 1 {
		t.Fatalf("admitted commit = (%d, %v), want transaction 1", result.transaction, result.err)
	}
	if err := <-closeResult; err != nil {
		t.Fatalf("Close: %v", err)
	}

	db = mustOpen(t, path)
	defer db.Close()
	if got := string(requireValue(t, db, "accepted")); got != "durable" {
		t.Fatalf("recovered admitted commit = %q", got)
	}
}

type countingSyncWAL struct {
	commitWAL
	syncs atomic.Uint64
}

func (wal *countingSyncWAL) Sync() error {
	wal.syncs.Add(1)
	return wal.commitWAL.Sync()
}

type blockingSyncWAL struct {
	commitWAL
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (wal *blockingSyncWAL) Sync() error {
	wal.once.Do(func() { close(wal.entered) })
	<-wal.release
	return wal.commitWAL.Sync()
}
