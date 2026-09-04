package work

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	kitdbengine "github.com/kitwork/engine/kitdb"
	kitdbnode "github.com/kitwork/engine/kitdb/node"
	"github.com/kitwork/engine/value"
)

func TestKitDBCatalogHydrationResumesSecondaryIndexAndCleanup(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	dataDirectory := filepath.Join(directory, ".data")
	if err := os.MkdirAll(dataDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	router := `import { router, database } from "kitwork";
const db = database.kitdb("alter.kitdb", {}, { token: "index-secret", access: "readwrite" });
router.get(() => db.products.count());`
	if err := os.WriteFile(filepath.Join(directory, RouterFileName), []byte(router), 0o644); err != nil {
		t.Fatal(err)
	}

	stored, target := generationReplacementDefinitions()
	path := filepath.Join(dataDirectory, "alter.kitdb")
	database, err := kitdbengine.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := commitKitDBCatalog(database, stored, nil, ""); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if ready, err := ensureKitDBSecondaryIndexCodec(database, stored); err != nil || !ready {
		_ = database.Close()
		t.Fatalf("initial codec ready=%t err=%v", ready, err)
	}

	const total = kitDBIndexBuildRowLimit*2 + 17
	tx, err := database.Begin()
	if err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	for number := 0; number < total; number++ {
		row := lifecycleProduct(number)
		rowKey, err := kitDBRowKey(stored, row["id"])
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := encodeKitDBValidatedRow(stored, row, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := tx.Put(rowKey, encoded); err != nil {
			t.Fatal(err)
		}
		entries, err := kitDBSecondaryIndexEntries(stored, row, rowKey)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if err := tx.Put(entry.key, entry.value); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := tx.Commit(); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	definitions := map[string]*StructDef{target.Name: target}
	if err := ensureKitDBCatalog(
		database, target, definitions, true, func() error { return nil },
	); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	state, found, err := loadKitDBIndexBuildState(database, target, kitDBIndexBuildSchema)
	if err != nil || !found || state.Phase != kitDBIndexBuildPhaseRows ||
		state.Rows != 0 || len(state.Progress) != 0 {
		_ = database.Close()
		t.Fatalf("prepared KIBS state=%#v found=%t err=%v", state, found, err)
	}
	oldIndex := collectIndexes(stored.Name, stored.columns)[0]
	oldPrefix, err := kitDBIndexBasePrefix(stored, oldIndex)
	if err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	tenant, proxy := openKitDBAlterDatabase(t, root)
	defer tenant.Close()
	if err := proxy.ensureKitDBCatalogLoaded(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		managed, err := kitDBForRequest(tenant, "alter.kitdb", nil).database()
		if err != nil {
			t.Fatal(err)
		}
		managed.writeMu.RLock()
		_, pending, stateErr := loadKitDBIndexBuildState(
			managed.database, target, kitDBIndexBuildSchema,
		)
		managed.writeMu.RUnlock()
		managed.Release()
		if stateErr != nil {
			t.Fatal(stateErr)
		}
		if !pending {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("catalog hydration did not finish secondary-index maintenance")
		}
		time.Sleep(time.Millisecond)
	}
	if err := proxy.refreshKitDBCatalogDefinitions(); err != nil {
		t.Fatal(err)
	}
	_, hydrated := proxy.schemaSnapshot()
	if hydrated[target.Name] == nil || hydrated[target.Name].Hash != target.Hash {
		t.Fatalf("same-session catalog did not publish target: %#v", hydrated[target.Name])
	}

	managed, err := kitDBForRequest(tenant, "alter.kitdb", nil).database()
	if err != nil {
		t.Fatal(err)
	}
	defer managed.Release()
	entry, found, err := managed.database.CatalogStructByID(target.ID)
	if err != nil || !found || entry.Hash != target.Hash {
		t.Fatalf("background target catalog found=%t entry=%#v err=%v", found, entry, err)
	}
	metadata, found, err := loadKitDBIndexGenerationMetadata(managed.database, target)
	if err != nil || !found || metadata.Epoch != 1 || len(metadata.Retired) != 0 {
		t.Fatalf("background index metadata=%#v found=%t err=%v", metadata, found, err)
	}
	if count := countKitDBKeysWithPrefix(t, managed.database, oldPrefix); count != 0 {
		t.Fatalf("background cleanup retained %d old index keys", count)
	}
	newIndex := collectIndexes(target.Name, target.columns)[0]
	assertKitDBIndexMatchesRows(t, managed.database, target, newIndex)
	rowKey, err := kitDBRowKey(target, value.New(fmt.Sprintf("p%05d", total-1)))
	if err != nil {
		t.Fatal(err)
	}
	if _, found, err := managed.database.Get(rowKey); err != nil || !found {
		t.Fatalf("last source row found=%t err=%v", found, err)
	}

	runtime := tenant.AppRuntime()
	resource := runtime.Resource(kitDBResourceName)
	manager, ok := resource.(*kitDBManager)
	if !ok || manager == nil {
		t.Fatalf("KitDB app manager = %T", resource)
	}
	stats := manager.fleet.Stats()
	if stats.SecondaryIndexCompletions != 1 || stats.SecondaryIndexChunks < 4 ||
		stats.SecondaryIndexAdvancedChunks != stats.SecondaryIndexChunks ||
		stats.MaintenanceFailures != 0 {
		t.Fatalf("automatic secondary-index stats = %#v", stats)
	}

	// A second wake observes no KIBS and completes as a bounded no-op rather
	// than recreating an accepted transition from process memory.
	ticket, err := manager.fleet.ScheduleSecondaryIndex(
		context.Background(), managed.path, kitdbengine.OpenOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := ticket.Wait(context.Background())
	if err != nil || result.SecondaryIndex == nil || result.SecondaryIndex.Pending ||
		result.SecondaryIndex.Chunks != 1 || result.SecondaryIndex.AdvancedChunks != 0 {
		t.Fatalf("idempotent secondary-index wake result=%#v err=%v", result, err)
	}
}

func TestKitDBBackgroundSecondaryIndexResumesCodecUpgrade(t *testing.T) {
	definition := bindStructDef("items", nil, map[string]*ColumnSpec{
		"id":   {kind: "text", primary: true, seq: 1},
		"code": {kind: "text", seq: 2, indexes: []colIndexRef{{}}},
	})
	path := filepath.Join(t.TempDir(), "codec-background.kitdb")
	database, err := kitdbengine.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := commitKitDBCatalog(database, definition, nil, ""); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	index := collectIndexes(definition.Name, definition.columns)[0]
	const total = kitDBIndexBuildRowLimit + 17
	tx, err := database.Begin()
	if err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	for number := 0; number < total; number++ {
		row := map[string]value.Value{
			"id":   value.New(fmt.Sprintf("p%05d", number)),
			"code": value.New(fmt.Sprintf("c%03d", number%127)),
		}
		rowKey, err := kitDBRowKey(definition, row["id"])
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := encodeKitDBValidatedRow(definition, row, nil)
		if err != nil {
			t.Fatal(err)
		}
		legacyKey, err := legacyKitDBSecondaryIndexKey(definition, index, row)
		if err != nil {
			t.Fatal(err)
		}
		if err := tx.Put(rowKey, encoded); err != nil {
			t.Fatal(err)
		}
		if err := tx.Put(legacyKey, rowKey); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.Commit(); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if ready, err := ensureKitDBSecondaryIndexCodec(database, definition); err != nil || ready {
		_ = database.Close()
		t.Fatalf("first codec chunk ready=%t err=%v", ready, err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	fleet, err := kitdbnode.NewManager(kitdbnode.Limits{
		MaxOpenDatabases: 2, MaxPageCacheBytes: 2 << 20,
		DefaultPageCacheBytes: 1 << 20, MaxConcurrentMaintenance: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	manager := newKitDBManager(fleet, false)
	t.Cleanup(func() {
		manager.Close()
		if err := fleet.Close(); err != nil {
			t.Errorf("close fleet: %v", err)
		}
	})
	managed, err := manager.open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := scheduleKitDBSecondaryIndex(managed)
	managed.Release()
	if err != nil {
		t.Fatal(err)
	}
	wait, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	result, err := ticket.Wait(wait)
	if err != nil {
		t.Fatal(err)
	}
	if result.SecondaryIndex == nil || result.SecondaryIndex.Pending ||
		result.SecondaryIndex.Chunks < 2 ||
		result.SecondaryIndex.AdvancedChunks != result.SecondaryIndex.Chunks {
		t.Fatalf("background codec result = %#v", result)
	}

	managed, err = manager.open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer managed.Release()
	if _, found, err := loadKitDBIndexBuildState(
		managed.database, definition, kitDBIndexBuildCodec,
	); err != nil || found {
		t.Fatalf("completed codec state found=%t err=%v", found, err)
	}
	markerKey, err := kitDBSecondaryIndexCodecMarkerKey(definition)
	if err != nil {
		t.Fatal(err)
	}
	marker, found, err := managed.database.Get(markerKey)
	if err != nil || !found || string(marker) != string(kitDBSecondaryIndexCodecMarker) {
		t.Fatalf("background codec marker=%x found=%t err=%v", marker, found, err)
	}
	if legacy := countKitDBLegacyIndexKeys(t, managed.database, definition); legacy != 0 {
		t.Fatalf("background codec retained %d legacy keys", legacy)
	}
}
