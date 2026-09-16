package kitdb

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
)

func testTriggerTable(id, name string) []byte {
	return []byte(fmt.Sprintf(`{"version":3,"id":%q,"name":%q,"hash":"hash","fields":[{"id":"id","tag":1,"name":"id"},{"id":"value","tag":2,"name":"value"}]}`, id, name))
}

func testTriggerDefinition(id, name, source, target, hash string) []byte {
	return []byte(fmt.Sprintf(`{"version":1,"id":%q,"name":%q,"hash":%q,"sourceStruct":%q,"targetStruct":%q,"sourceFields":[1],"targetFields":[2],"event":"insert"}`, id, name, hash, source, target))
}

func testTriggerTables(t *testing.T, db *DB) {
	t.Helper()
	tx := mustBegin(t, db)
	if err := tx.DefineStruct(testTriggerTable(productsCatalogID, "source")); err != nil {
		t.Fatal(err)
	}
	if err := tx.DefineStruct(testTriggerTable(linksCatalogID, "target")); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestCatalogTriggerAtomicityDependenciesAndIsolation(t *testing.T) {
	db := mustOpen(t, filepath.Join(t.TempDir(), "triggers.kitdb"))
	defer db.Close()
	testTriggerTables(t, db)
	id := "a0112233445566778899aabbccddeeff"
	definition := testTriggerDefinition(id, "record", productsCatalogID, linksCatalogID, "hash")
	before, _ := db.CatalogVersion()
	tx := mustBegin(t, db)
	if err := tx.DefineTrigger(definition); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	rolled, _ := db.CatalogVersion()
	if rolled.Revision != before.Revision {
		t.Fatal("rollback published trigger")
	}
	tx = mustBegin(t, db)
	if err := tx.DefineTrigger(definition); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	view, err := db.Catalog()
	if err != nil || len(view.Triggers) != 1 || view.Revision == before.Revision {
		t.Fatalf("catalog: %#v %v", view, err)
	}
	view.Triggers[0].Definition[0] = 'x'
	view.Triggers[0].SourceFields[0] = 999
	view.Triggers[0].TargetFields[0] = 999
	fresh, _ := db.Catalog()
	if !bytes.Equal(fresh.Triggers[0].Definition, definition) || fresh.Triggers[0].SourceFields[0] != 1 || fresh.Triggers[0].TargetFields[0] != 2 {
		t.Fatal("snapshot aliases trigger data")
	}
	commitPut(t, db, "marker", "value")
	version, _ := db.CatalogVersion()
	if version.Revision != view.Revision {
		t.Fatal("data write changed catalog revision")
	}
	tx = mustBegin(t, db)
	if err := tx.DefineTrigger(testTriggerDefinition(id, "record", productsCatalogID, linksCatalogID, "changed")); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Commit(); !errors.Is(err, ErrInvalidCatalog) {
		t.Fatalf("immutable definition: %v", err)
	}
	for _, table := range []string{productsCatalogID, linksCatalogID} {
		tx = mustBegin(t, db)
		if err := tx.DeleteStruct(table); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Commit(); !errors.Is(err, ErrInvalidCatalog) {
			t.Fatalf("deleted referenced table: %v", err)
		}
	}
	tx = mustBegin(t, db)
	if err := tx.DefineStruct([]byte(fmt.Sprintf(`{"version":3,"id":%q,"name":"source","hash":"changed","fields":[{"id":"value","tag":2,"name":"value"}]}`, productsCatalogID))); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Commit(); !errors.Is(err, ErrInvalidCatalog) {
		t.Fatalf("deleted referenced field: %v", err)
	}
	tx = mustBegin(t, db)
	if err := tx.DefineTrigger(testTriggerDefinition("b0112233445566778899aabbccddeeff", "cycle", linksCatalogID, productsCatalogID, "hash")); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Commit(); !errors.Is(err, ErrInvalidCatalog) {
		t.Fatalf("cyclic graph: %v", err)
	}
	tx = mustBegin(t, db)
	if err := tx.DeleteTrigger(id); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	version, _ = db.CatalogVersion()
	if version.Revision != before.Revision {
		t.Fatal("dropped trigger retained revision contribution")
	}
}

func TestCatalogTriggerBounds(t *testing.T) {
	state := newCatalogState()
	for i := 0; i < maximumCatalogTriggers; i++ {
		id := fmt.Sprintf("%032x", i)
		key, _ := catalogTriggerKey(id)
		source := fmt.Sprintf("%032x", i/maximumTriggersPerStruct)
		if err := state.put(key, testTriggerDefinition(id, fmt.Sprintf("t%d", i), source, linksCatalogID, "hash")); err != nil {
			t.Fatal(err)
		}
	}
	id := fmt.Sprintf("%032x", maximumCatalogTriggers)
	key, _ := catalogTriggerKey(id)
	if err := state.put(key, testTriggerDefinition(id, "over", productsCatalogID, linksCatalogID, "hash")); !errors.Is(err, ErrInvalidCatalog) {
		t.Fatalf("global limit: %v", err)
	}
	state = newCatalogState()
	for i := 0; i < maximumTriggersPerStruct; i++ {
		id := fmt.Sprintf("%032x", i)
		key, _ := catalogTriggerKey(id)
		if err := state.put(key, testTriggerDefinition(id, fmt.Sprintf("t%d", i), productsCatalogID, linksCatalogID, "hash")); err != nil {
			t.Fatal(err)
		}
	}
	if err := state.put(key, testTriggerDefinition(id, "over", productsCatalogID, linksCatalogID, "hash")); !errors.Is(err, ErrInvalidCatalog) {
		t.Fatalf("table limit: %v", err)
	}
	if _, err := decodeCatalogTrigger(bytes.Repeat([]byte("x"), maximumTriggerDefinitionBytes+1)); !errors.Is(err, ErrInvalidCatalog) {
		t.Fatalf("size limit: %v", err)
	}
	definition := testTriggerDefinition(id, "invalid", productsCatalogID, linksCatalogID, "hash")
	for _, invalid := range [][]byte{
		bytes.Replace(definition, []byte(`"targetFields":[2]`), []byte(`"targetFields":[0]`), 1),
		bytes.Replace(definition, []byte(`"targetFields":[2]`), []byte(`"targetFields":[2,2]`), 1),
		bytes.Replace(definition, []byte(`"event":"insert"`), []byte(`"event":"truncate"`), 1),
		bytes.Replace(definition, []byte(`"version":1`), []byte(`"version":0`), 1),
	} {
		if _, err := decodeCatalogTrigger(invalid); !errors.Is(err, ErrInvalidCatalog) {
			t.Fatalf("invalid header accepted: %s %v", invalid, err)
		}
	}
}

func TestCatalogTriggerSyncFailureRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "uncertain.kitdb")
	db := mustOpen(t, path)
	testTriggerTables(t, db)
	db.mu.Lock()
	db.wal = &syncFailWAL{commitWAL: db.wal, failure: errors.New("injected sync failure")}
	db.mu.Unlock()
	tx := mustBegin(t, db)
	if err := tx.DefineTrigger(testTriggerDefinition("a0112233445566778899aabbccddeeff", "record", productsCatalogID, linksCatalogID, "hash")); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Commit(); !errors.Is(err, ErrDurabilityUncertain) {
		t.Fatalf("commit: %v", err)
	}
	db.mu.RLock()
	published := len(db.catalog.triggers)
	db.mu.RUnlock()
	if published != 0 {
		t.Fatal("published trigger before successful sync")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db = mustOpen(t, path)
	defer db.Close()
	catalog, err := db.Catalog()
	if err != nil || len(catalog.Triggers) != 1 {
		t.Fatalf("recovery: %#v %v", catalog.Triggers, err)
	}
}
