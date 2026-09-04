package kitdb

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
)

func catalogFunctionTestDefinition(id, name, hash string) []byte {
	return []byte(fmt.Sprintf(`{"version":1,"id":%q,"name":%q,"hash":%q}`, id, name, hash))
}

func TestCatalogFunctionAtomicRevisionAndBounds(t *testing.T) {
	db := mustOpen(t, filepath.Join(t.TempDir(), "functions.kitdb"))
	defer db.Close()
	before, err := db.CatalogVersion()
	if err != nil {
		t.Fatal(err)
	}
	definition := catalogFunctionTestDefinition(productsCatalogID, "normalize", "one")
	tx := mustBegin(t, db)
	if err := tx.DefineFunction(definition); err != nil {
		t.Fatal(err)
	}
	if err := tx.Put([]byte("marker"), []byte("one")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	rolled, _ := db.CatalogVersion()
	if rolled.Revision != before.Revision {
		t.Fatal("rolled-back function changed revision")
	}
	tx = mustBegin(t, db)
	if err := tx.DefineFunction(definition); err != nil {
		t.Fatal(err)
	}
	if err := tx.Put([]byte("marker"), []byte("one")); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	snapshot, err := db.Catalog()
	if err != nil || len(snapshot.Functions) != 1 || len(snapshot.Structs) != 0 || snapshot.Revision == before.Revision {
		t.Fatalf("snapshot = %#v, %v", snapshot, err)
	}
	snapshot.Functions[0].Definition[0] = 'x'
	fresh, _ := db.Catalog()
	if !bytes.Equal(fresh.Functions[0].Definition, definition) {
		t.Fatal("catalog function not caller-owned")
	}
	commitPut(t, db, "marker", "two")
	recordOnly, _ := db.CatalogVersion()
	if recordOnly.Revision != snapshot.Revision {
		t.Fatal("record commit invalidated functions")
	}
	tx = mustBegin(t, db)
	if err := tx.DeleteFunction(productsCatalogID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	deleted, _ := db.CatalogVersion()
	if deleted.Revision != before.Revision {
		t.Fatal("deleted function remains in revision")
	}

	state := newCatalogState()
	for i := 0; i < maximumCatalogFunctions; i++ {
		id := fmt.Sprintf("%032x", i)
		key, _ := catalogFunctionKey(id)
		if err := state.put(key, catalogFunctionTestDefinition(id, fmt.Sprintf("f%d", i), "hash")); err != nil {
			t.Fatal(err)
		}
	}
	id := fmt.Sprintf("%032x", maximumCatalogFunctions)
	key, _ := catalogFunctionKey(id)
	if err := state.put(key, catalogFunctionTestDefinition(id, "over", "hash")); !errors.Is(err, ErrInvalidCatalog) {
		t.Fatalf("function limit: %v", err)
	}
	if err := state.put(key, definition); !errors.Is(err, ErrInvalidCatalog) {
		t.Fatalf("key mismatch: %v", err)
	}
	if _, err := decodeCatalogFunction(bytes.Repeat([]byte("x"), maximumFunctionDefinitionBytes+1)); !errors.Is(err, ErrInvalidCatalog) {
		t.Fatalf("byte limit: %v", err)
	}
}

func TestCatalogFunctionSyncFailureRecoversWithoutEarlyPublication(t *testing.T) {
	path := filepath.Join(t.TempDir(), "uncertain.kitdb")
	db := mustOpen(t, path)
	if _, err := db.Catalog(); err != nil {
		t.Fatal(err)
	}
	db.mu.Lock()
	db.wal = &syncFailWAL{commitWAL: db.wal, failure: errors.New("injected sync failure")}
	db.mu.Unlock()
	tx := mustBegin(t, db)
	if err := tx.DefineFunction(catalogFunctionTestDefinition(productsCatalogID, "normalize", "hash")); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Commit(); !errors.Is(err, ErrDurabilityUncertain) {
		t.Fatalf("commit: %v", err)
	}
	db.mu.RLock()
	published := len(db.catalog.functions)
	db.mu.RUnlock()
	if published != 0 {
		t.Fatal("published function before successful sync")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db = mustOpen(t, path)
	defer db.Close()
	snapshot, err := db.Catalog()
	if err != nil || len(snapshot.Functions) != 1 {
		t.Fatalf("recovery: %#v %v", snapshot, err)
	}
}
