package kitdb

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
)

func TestCatalogDomainRevisionIsolationAndBounds(t *testing.T) {
	db := mustOpen(t, filepath.Join(t.TempDir(), "domains.kitdb"))
	defer db.Close()
	before, err := db.CatalogVersion()
	if err != nil {
		t.Fatal(err)
	}
	definition := catalogFunctionTestDefinition(productsCatalogID, "positive", "hash")
	tx := mustBegin(t, db)
	if err := tx.DefineDomain(definition); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	rolled, _ := db.CatalogVersion()
	if rolled.Revision != before.Revision {
		t.Fatal("rollback changed catalog")
	}
	tx = mustBegin(t, db)
	if err := tx.DefineDomain(definition); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	view, err := db.Catalog()
	if err != nil || len(view.Domains) != 1 || len(view.Structs) != 0 || view.Revision == before.Revision {
		t.Fatalf("catalog: %#v %v", view, err)
	}
	view.Domains[0].Definition[0] = 'x'
	fresh, _ := db.Catalog()
	if !bytes.Equal(fresh.Domains[0].Definition, definition) {
		t.Fatal("snapshot aliases engine memory")
	}
	commitPut(t, db, "marker", "value")
	version, _ := db.CatalogVersion()
	if version.Revision != view.Revision {
		t.Fatal("record commit changed catalog revision")
	}
	tx = mustBegin(t, db)
	if err := tx.DefineDomain(catalogFunctionTestDefinition(productsCatalogID, "positive", "changed")); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Commit(); !errors.Is(err, ErrInvalidCatalog) {
		t.Fatalf("mutated immutable domain: %v", err)
	}
	tx = mustBegin(t, db)
	if err := tx.DeleteDomain(productsCatalogID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	deleted, _ := db.CatalogVersion()
	if deleted.Revision != before.Revision {
		t.Fatal("dropped domain still changes revision")
	}

	state := newCatalogState()
	for i := 0; i < maximumCatalogDomains; i++ {
		id := fmt.Sprintf("%032x", i)
		key, _ := catalogDomainKey(id)
		if err := state.put(key, catalogFunctionTestDefinition(id, fmt.Sprintf("d%d", i), "hash")); err != nil {
			t.Fatal(err)
		}
	}
	id := fmt.Sprintf("%032x", maximumCatalogDomains)
	key, _ := catalogDomainKey(id)
	if err := state.put(key, catalogFunctionTestDefinition(id, "over", "hash")); !errors.Is(err, ErrInvalidCatalog) {
		t.Fatalf("domain limit: %v", err)
	}
	if _, err := decodeCatalogDomain(bytes.Repeat([]byte("x"), maximumDomainDefinitionBytes+1)); !errors.Is(err, ErrInvalidCatalog) {
		t.Fatalf("byte limit: %v", err)
	}
	if err := state.put(key, definition); !errors.Is(err, ErrInvalidCatalog) {
		t.Fatalf("key mismatch: %v", err)
	}
}

func TestCatalogDomainSyncFailureRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "uncertain.kitdb")
	db := mustOpen(t, path)
	if _, err := db.Catalog(); err != nil {
		t.Fatal(err)
	}
	db.mu.Lock()
	db.wal = &syncFailWAL{commitWAL: db.wal, failure: errors.New("injected sync failure")}
	db.mu.Unlock()
	tx := mustBegin(t, db)
	if err := tx.DefineDomain(catalogFunctionTestDefinition(productsCatalogID, "positive", "hash")); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Commit(); !errors.Is(err, ErrDurabilityUncertain) {
		t.Fatalf("commit: %v", err)
	}
	db.mu.RLock()
	published := len(db.catalog.domains)
	db.mu.RUnlock()
	if published != 0 {
		t.Fatal("published domain before successful sync")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db = mustOpen(t, path)
	defer db.Close()
	catalog, err := db.Catalog()
	if err != nil || len(catalog.Domains) != 1 {
		t.Fatalf("recovery: %#v %v", catalog, err)
	}
}
