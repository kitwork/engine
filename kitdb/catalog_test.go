package kitdb

import (
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

const (
	productsCatalogID = "00112233445566778899aabbccddeeff"
	linksCatalogID    = "102132435465768798a9bacbdcedfe0f"
)

func TestCatalogCommitsAtomicallyRollsBackAndReopens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenant.kitdb")
	database := mustOpen(t, path)

	initial, err := database.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	if len(initial.Structs) != 0 {
		t.Fatalf("initial catalog contains %d structs", len(initial.Structs))
	}

	rolledBack := mustBegin(t, database)
	if err := rolledBack.DefineStruct(testCatalogDefinition(linksCatalogID, "links", "legacy-hash")); err != nil {
		t.Fatal(err)
	}
	if err := rolledBack.Rollback(); err != nil {
		t.Fatal(err)
	}
	if snapshot, err := database.Catalog(); err != nil || len(snapshot.Structs) != 0 {
		t.Fatalf("catalog after rollback = %#v, %v", snapshot, err)
	}

	transaction := mustBegin(t, database)
	definition := testCatalogDefinition(productsCatalogID, "products", "legacy-hash")
	if err := transaction.DefineStruct(definition); err != nil {
		t.Fatal(err)
	}
	if err := transaction.Put([]byte("product/1"), []byte("Kitwork")); err != nil {
		t.Fatal(err)
	}
	committed, err := transaction.Commit()
	if err != nil || committed != 1 {
		t.Fatalf("commit = (%d, %v), want transaction 1", committed, err)
	}

	snapshot, err := database.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Transaction != committed || len(snapshot.Structs) != 1 {
		t.Fatalf("catalog snapshot = %#v", snapshot)
	}
	if snapshot.Structs[0].Name != "products" || snapshot.Structs[0].Hash != "legacy-hash" {
		t.Fatalf("catalog struct = %#v", snapshot.Structs[0])
	}
	if got := requireValue(t, database, "product/1"); string(got) != "Kitwork" {
		t.Fatalf("row committed with catalog = %q", got)
	}

	// Public snapshots own their byte slices.
	snapshot.Structs[0].Definition[0] = '!'
	stored, found, err := database.CatalogStructByID(productsCatalogID)
	if err != nil || !found {
		t.Fatalf("catalog lookup found=%v err=%v", found, err)
	}
	if stored.Definition[0] != '{' {
		t.Fatalf("caller changed stored definition: %q", stored.Definition)
	}
	hash, found, err := database.CatalogStructHashByID(productsCatalogID)
	if err != nil || !found || hash != "legacy-hash" {
		t.Fatalf("catalog hash lookup = %q found=%v err=%v", hash, found, err)
	}
	byName, found, err := database.CatalogStructByName("products")
	if err != nil || !found || byName.ID != productsCatalogID {
		t.Fatalf("catalog name lookup = %#v found=%v err=%v", byName, found, err)
	}

	if _, err := database.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	database = mustOpen(t, path)
	defer database.Close()
	reopened, err := database.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Transaction != committed || len(reopened.Structs) != 1 || reopened.Structs[0].ID != productsCatalogID {
		t.Fatalf("reopened catalog = %#v", reopened)
	}
}

func TestCatalogRenamePreservesIdentityAndReleasesOldName(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenant.kitdb")
	database := mustOpen(t, path)

	created := mustBegin(t, database)
	if err := created.DefineStruct(testCatalogDefinition(productsCatalogID, "products", "products-v1")); err != nil {
		t.Fatal(err)
	}
	if _, err := created.Commit(); err != nil {
		t.Fatal(err)
	}

	rolledBack := mustBegin(t, database)
	if err := rolledBack.DefineStruct(testCatalogDefinition(productsCatalogID, "catalog", "catalog-rollback")); err != nil {
		t.Fatal(err)
	}
	if err := rolledBack.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, found, err := database.CatalogStructByName("products"); err != nil || !found {
		t.Fatalf("original name after rollback found=%v err=%v", found, err)
	}
	if _, found, err := database.CatalogStructByName("catalog"); err != nil || found {
		t.Fatalf("rolled-back name found=%v err=%v", found, err)
	}

	renamed := mustBegin(t, database)
	if err := renamed.DefineStruct(testCatalogDefinition(productsCatalogID, "catalog", "catalog-v1")); err != nil {
		t.Fatal(err)
	}
	if _, err := renamed.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, found, err := database.CatalogStructByName("products"); err != nil || found {
		t.Fatalf("old name after rename found=%v err=%v", found, err)
	}
	byName, found, err := database.CatalogStructByName("catalog")
	if err != nil || !found || byName.ID != productsCatalogID {
		t.Fatalf("renamed catalog lookup = %#v found=%v err=%v", byName, found, err)
	}

	reused := mustBegin(t, database)
	if err := reused.DefineStruct(testCatalogDefinition(linksCatalogID, "products", "products-v2")); err != nil {
		t.Fatal(err)
	}
	if _, err := reused.Commit(); err != nil {
		t.Fatal(err)
	}

	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	database = mustOpen(t, path)
	defer database.Close()
	snapshot, err := database.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Structs) != 2 {
		t.Fatalf("reopened renamed catalog = %#v", snapshot.Structs)
	}
	if entry, found, err := database.CatalogStructByName("catalog"); err != nil || !found || entry.ID != productsCatalogID {
		t.Fatalf("reopened new name = %#v found=%v err=%v", entry, found, err)
	}
	if entry, found, err := database.CatalogStructByName("products"); err != nil || !found || entry.ID != linksCatalogID {
		t.Fatalf("reopened reused name = %#v found=%v err=%v", entry, found, err)
	}
}

func TestCatalogVersionChangesOnlyWithCatalogBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenant.kitdb")
	database := mustOpen(t, path)

	initial, err := database.CatalogVersion()
	if err != nil {
		t.Fatal(err)
	}
	if initial.Transaction != 0 || initial.Revision == "" {
		t.Fatalf("initial catalog version = %#v", initial)
	}

	defined := mustBegin(t, database)
	if err := defined.DefineStruct(testCatalogDefinition(productsCatalogID, "products", "products-v1")); err != nil {
		t.Fatal(err)
	}
	if _, err := defined.Commit(); err != nil {
		t.Fatal(err)
	}
	afterSchema, err := database.CatalogVersion()
	if err != nil {
		t.Fatal(err)
	}
	if afterSchema.Transaction != 1 || afterSchema.Revision == initial.Revision {
		t.Fatalf("catalog version after schema commit = %#v, initial = %#v", afterSchema, initial)
	}

	recordOnly := mustBegin(t, database)
	if err := recordOnly.Put([]byte("product/1"), []byte("Kitwork")); err != nil {
		t.Fatal(err)
	}
	if _, err := recordOnly.Commit(); err != nil {
		t.Fatal(err)
	}
	afterRecord, err := database.CatalogVersion()
	if err != nil {
		t.Fatal(err)
	}
	if afterRecord.Transaction != 2 || afterRecord.Revision != afterSchema.Revision {
		t.Fatalf("catalog version after record commit = %#v, schema = %#v", afterRecord, afterSchema)
	}

	rolledBack := mustBegin(t, database)
	if err := rolledBack.DefineStruct(testCatalogDefinition(productsCatalogID, "catalog", "catalog-v2")); err != nil {
		t.Fatal(err)
	}
	if err := rolledBack.Rollback(); err != nil {
		t.Fatal(err)
	}
	afterRollback, err := database.CatalogVersion()
	if err != nil {
		t.Fatal(err)
	}
	if afterRollback != afterRecord {
		t.Fatalf("catalog version changed after rollback: %#v -> %#v", afterRecord, afterRollback)
	}

	allocations := testing.AllocsPerRun(1_000, func() {
		version, versionErr := database.CatalogVersion()
		if versionErr != nil || version.Revision == "" {
			panic("catalog version lookup failed")
		}
	})
	if allocations != 0 {
		t.Fatalf("CatalogVersion allocations = %.2f, want 0", allocations)
	}

	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	database = mustOpen(t, path)
	defer database.Close()
	reopened, err := database.CatalogVersion()
	if err != nil {
		t.Fatal(err)
	}
	if reopened != afterRecord {
		t.Fatalf("reopened catalog version = %#v, want %#v", reopened, afterRecord)
	}
}

func TestCatalogDeleteStructCommitsAndRollsBackWithOwnedData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenant.kitdb")
	database := mustOpen(t, path)

	transaction := mustBegin(t, database)
	if err := transaction.DefineStruct(testCatalogDefinition(productsCatalogID, "products", "hash")); err != nil {
		t.Fatal(err)
	}
	if err := transaction.Put([]byte("product/1"), []byte("Kitwork")); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Commit(); err != nil {
		t.Fatal(err)
	}

	rolledBack := mustBegin(t, database)
	if err := rolledBack.DeleteStruct(productsCatalogID); err != nil {
		t.Fatal(err)
	}
	if err := rolledBack.Delete([]byte("product/1")); err != nil {
		t.Fatal(err)
	}
	if err := rolledBack.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, found, err := database.CatalogStructByID(productsCatalogID); err != nil || !found {
		t.Fatalf("catalog after delete rollback found=%v err=%v", found, err)
	}
	if got := requireValue(t, database, "product/1"); string(got) != "Kitwork" {
		t.Fatalf("row after delete rollback = %q", got)
	}

	dropped := mustBegin(t, database)
	if err := dropped.DeleteStruct(productsCatalogID); err != nil {
		t.Fatal(err)
	}
	if err := dropped.Delete([]byte("product/1")); err != nil {
		t.Fatal(err)
	}
	if committed, err := dropped.Commit(); err != nil || committed != 2 {
		t.Fatalf("drop commit = (%d, %v), want transaction 2", committed, err)
	}
	if _, found, err := database.CatalogStructByID(productsCatalogID); err != nil || found {
		t.Fatalf("catalog after delete found=%v err=%v", found, err)
	}
	if _, found, err := database.Get([]byte("product/1")); err != nil || found {
		t.Fatalf("row after delete found=%v err=%v", found, err)
	}

	if _, err := database.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	database = mustOpen(t, path)
	defer database.Close()
	if snapshot, err := database.Catalog(); err != nil || len(snapshot.Structs) != 0 {
		t.Fatalf("reopened catalog after delete = %#v, %v", snapshot, err)
	}
	if _, found, err := database.Get([]byte("product/1")); err != nil || found {
		t.Fatalf("reopened row after delete found=%v err=%v", found, err)
	}
}

func TestCatalogRejectsMismatchedKeysAndDuplicateJSONFields(t *testing.T) {
	database := mustOpen(t, filepath.Join(t.TempDir(), "tenant.kitdb"))
	defer database.Close()

	duplicateJSON := []byte(`{"version":2,"version":2,"id":"00112233445566778899aabbccddeeff","name":"products","hash":"hash","fields":[{}]}`)
	transaction := mustBegin(t, database)
	if err := transaction.DefineStruct(duplicateJSON); !errors.Is(err, ErrInvalidCatalog) {
		t.Fatalf("duplicate JSON field error = %v", err)
	}
	if err := transaction.Rollback(); err != nil {
		t.Fatal(err)
	}

	key, err := catalogKey(linksCatalogID)
	if err != nil {
		t.Fatal(err)
	}
	transaction = mustBegin(t, database)
	if err := transaction.Put(key, testCatalogDefinition(productsCatalogID, "products", "hash")); err != nil {
		t.Fatal(err)
	}
	if committed, err := transaction.Commit(); !errors.Is(err, ErrInvalidCatalog) || committed != 0 {
		t.Fatalf("mismatched catalog commit = (%d, %v)", committed, err)
	}
	if last, err := database.LastTransaction(); err != nil || last != 0 {
		t.Fatalf("last transaction after rejected catalog = (%d, %v)", last, err)
	}
	if committed := commitPut(t, database, "healthy", "row"); committed != 1 {
		t.Fatalf("healthy transaction = %d, want 1", committed)
	}
}

func TestCatalogSerializesConcurrentDuplicateNames(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenant.kitdb")
	database, err := OpenWithOptions(path, OpenOptions{
		CommitQueueSize:  8,
		MaxCommitBatch:   8,
		GroupCommitDelay: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	first := mustBegin(t, database)
	second := mustBegin(t, database)
	if err := first.DefineStruct(testCatalogDefinition(productsCatalogID, "products", "first")); err != nil {
		t.Fatal(err)
	}
	if err := second.DefineStruct(testCatalogDefinition(linksCatalogID, "products", "second")); err != nil {
		t.Fatal(err)
	}

	type result struct {
		transaction uint64
		err         error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	var wait sync.WaitGroup
	for _, transaction := range []*Tx{first, second} {
		wait.Add(1)
		go func(transaction *Tx) {
			defer wait.Done()
			<-start
			committed, err := transaction.Commit()
			results <- result{transaction: committed, err: err}
		}(transaction)
	}
	close(start)
	wait.Wait()
	close(results)

	succeeded := 0
	rejected := 0
	for result := range results {
		switch {
		case result.err == nil && result.transaction == 1:
			succeeded++
		case errors.Is(result.err, ErrInvalidCatalog) && result.transaction == 0:
			rejected++
		default:
			t.Fatalf("unexpected concurrent commit = (%d, %v)", result.transaction, result.err)
		}
	}
	if succeeded != 1 || rejected != 1 {
		t.Fatalf("concurrent results: succeeded=%d rejected=%d", succeeded, rejected)
	}
	snapshot, err := database.Catalog()
	if err != nil || len(snapshot.Structs) != 1 || snapshot.Structs[0].Name != "products" {
		t.Fatalf("catalog after concurrent commits = %#v, %v", snapshot, err)
	}
}

func TestCatalogSemanticCorruptionLoadsLazily(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenant.kitdb")
	database := mustOpen(t, path)
	transaction := mustBegin(t, database)
	if err := transaction.DefineStruct(testCatalogDefinition(productsCatalogID, "products", "hash")); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Commit(); err != nil {
		t.Fatal(err)
	}

	key, err := catalogKey(productsCatalogID)
	if err != nil {
		t.Fatal(err)
	}
	database.mu.Lock()
	database.overlay[string(key)] = rowMutation{value: []byte(`{"version":2}`)}
	database.mu.Unlock()
	if _, err := database.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	database, err = Open(path)
	if err != nil {
		t.Fatalf("fast Open should defer semantic catalog validation: %v", err)
	}
	if _, err := database.Catalog(); !errors.Is(err, ErrCorrupt) || !errors.Is(err, ErrInvalidCatalog) {
		t.Fatalf("Catalog semantic corruption error = %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenWithOptions(path, OpenOptions{VerifyOnOpen: true}); !errors.Is(err, ErrCorrupt) || !errors.Is(err, ErrInvalidCatalog) {
		t.Fatalf("verified Open semantic corruption error = %v", err)
	}
}

func TestCatalogSyncFailurePublishesOnlyThroughRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenant.kitdb")
	database := mustOpen(t, path)
	if _, err := database.Catalog(); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("injected catalog sync failure")
	database.mu.Lock()
	database.wal = &syncFailWAL{commitWAL: database.wal, failure: failure}
	database.mu.Unlock()

	transaction := mustBegin(t, database)
	if err := transaction.DefineStruct(testCatalogDefinition(productsCatalogID, "products", "hash")); err != nil {
		t.Fatal(err)
	}
	committed, err := transaction.Commit()
	if !errors.Is(err, ErrDurabilityUncertain) || committed != 1 {
		t.Fatalf("catalog commit with failed sync = (%d, %v)", committed, err)
	}
	database.mu.RLock()
	visibleBeforeRecovery := len(database.catalog.byID)
	database.mu.RUnlock()
	if visibleBeforeRecovery != 0 {
		t.Fatalf("catalog published before successful sync: %d structs", visibleBeforeRecovery)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	database = mustOpen(t, path)
	defer database.Close()
	snapshot, err := database.Catalog()
	if err != nil || snapshot.Transaction != 1 || len(snapshot.Structs) != 1 {
		t.Fatalf("recovered catalog = %#v, %v", snapshot, err)
	}
}

func TestCloseDrainsCatalogCommitWaitingForInitialLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenant.kitdb")
	database := mustOpen(t, path)
	definition := testCatalogDefinition(productsCatalogID, "products", "hash")
	key, err := catalogKey(productsCatalogID)
	if err != nil {
		t.Fatal(err)
	}
	request := &commitRequest{
		operations: []operation{{kind: operationPut, key: key, value: definition}},
		result:     make(chan commitResult, 1),
	}

	// Hold the lazy loader so Close can enter its draining state after this
	// request is admitted but before the writer validates the first catalog.
	database.catalogLoadMu.Lock()
	database.commitQueue <- request
	closed := make(chan error, 1)
	go func() { closed <- database.Close() }()
	deadline := time.Now().Add(time.Second)
	for {
		database.mu.RLock()
		closing := database.closing
		database.mu.RUnlock()
		if closing {
			break
		}
		if time.Now().After(deadline) {
			database.catalogLoadMu.Unlock()
			t.Fatal("Close did not enter draining state")
		}
		time.Sleep(time.Millisecond)
	}
	database.catalogLoadMu.Unlock()

	if result := <-request.result; result.err != nil || result.transaction != 1 {
		t.Fatalf("drained catalog commit = (%d, %v)", result.transaction, result.err)
	}
	if err := <-closed; err != nil {
		t.Fatal(err)
	}

	database = mustOpen(t, path)
	defer database.Close()
	snapshot, err := database.Catalog()
	if err != nil || len(snapshot.Structs) != 1 || snapshot.Structs[0].Name != "products" {
		t.Fatalf("recovered drained catalog = %#v, %v", snapshot, err)
	}
}

func testCatalogDefinition(id, name, hash string) []byte {
	return []byte(fmt.Sprintf(
		`{"version":2,"id":%q,"name":%q,"hash":%q,"fields":[{"id":"field-id","name":"id"}]}`,
		id,
		name,
		hash,
	))
}
