package work

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	kitdbengine "github.com/kitwork/engine/kitdb"
	kitdbnode "github.com/kitwork/engine/kitdb/node"
	"github.com/kitwork/engine/value"
)

func TestKitDBBackgroundRowMigrationResumesDurableState(t *testing.T) {
	stored, target, _ := segmentedTypeMigrationDefinitions(t)
	path := filepath.Join(t.TempDir(), "background-resume.kitdb")
	database, err := kitdbengine.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := commitKitDBCatalog(database, stored, nil, ""); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	const total = kitDBAtomicMigrationRows + 17
	insertSegmentedMigrationRows(t, database, stored, total, -1, "")
	state, err := beginKitDBSegmentedMigration(
		database, stored, target, time.Date(2026, 8, 26, 18, 0, 0, 0, time.UTC),
		kitDBAtomicMigrationRows+1,
	)
	if err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	sourceGeneration := state.SourceGeneration
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
	ticket, err := scheduleKitDBRowMigration(managed)
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
	if result.RowMigration == nil || result.RowMigration.Pending ||
		result.RowMigration.Chunks < 2 || result.RowMigration.AdvancedChunks != result.RowMigration.Chunks {
		t.Fatalf("background row-migration result = %#v", result)
	}

	managed, err = manager.open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer managed.Release()
	if _, found, err := loadKitDBRowMigrationState(managed.database, target); err != nil || found {
		t.Fatalf("completed background state found=%t err=%v", found, err)
	}
	entry, found, err := managed.database.CatalogStructByID(target.ID)
	if err != nil || !found || entry.Hash != target.Hash {
		t.Fatalf("background target catalog found=%t entry=%#v err=%v", found, entry, err)
	}
	assertSegmentedMigrationValue(t, managed.database, target, total-1, value.Number, fmt.Sprint(total-1))
	assertNoKitDBRowsAtGeneration(t, managed.database, target, sourceGeneration)
}

func TestKitDBBackgroundRowMigrationStopsAndResumesAfterRepair(t *testing.T) {
	stored, target, _ := segmentedTypeMigrationDefinitions(t)
	path := filepath.Join(t.TempDir(), "background-repair.kitdb")
	database, err := kitdbengine.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := commitKitDBCatalog(database, stored, nil, ""); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	const total = kitDBAtomicMigrationRows + 9
	insertSegmentedMigrationRows(t, database, stored, total, total-1, "bad")
	if _, err := beginKitDBSegmentedMigration(
		database, stored, target, time.Date(2026, 8, 26, 19, 0, 0, 0, time.UTC),
		kitDBAtomicMigrationRows+1,
	); err != nil {
		_ = database.Close()
		t.Fatal(err)
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
	ticket, err := scheduleKitDBRowMigration(managed)
	managed.Release()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ticket.Wait(context.Background()); err == nil ||
		!strings.Contains(err.Error(), `cannot cast text to integer`) {
		t.Fatalf("background invalid-row error = %v", err)
	}

	managed, err = manager.open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	managed.writeMu.Lock()
	putSegmentedMigrationRow(t, managed.database, stored, total-1, fmt.Sprint(total-1))
	managed.writeMu.Unlock()
	ticket, err = scheduleKitDBRowMigration(managed)
	managed.Release()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ticket.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	managed, err = manager.open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer managed.Release()
	if _, found, err := loadKitDBRowMigrationState(managed.database, target); err != nil || found {
		t.Fatalf("repaired background state found=%t err=%v", found, err)
	}
	assertSegmentedMigrationValue(t, managed.database, target, total-1, value.Number, fmt.Sprint(total-1))
	if stats := fleet.Stats(); stats.MaintenanceFailures != 1 ||
		stats.RowMigrationCompletions != 1 || stats.RowMigrationChunks < 2 {
		t.Fatalf("background repair stats = %#v", stats)
	}
}

func TestKitDBRepairWriteWakesStoppedBackgroundMigration(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	router := `import { router, database } from "kitwork";
const db = database.kitdb("alter.kitdb", {}, { token: "repair-secret", access: "readwrite" });
router.get(() => db.products.count());`
	if err := os.WriteFile(filepath.Join(directory, RouterFileName), []byte(router), 0o644); err != nil {
		t.Fatal(err)
	}
	tenant, proxy := openKitDBAlterDatabase(t, root)
	defer tenant.Close()
	executeKitDBAlterSQL(t, proxy, `CREATE TABLE products (id TEXT PRIMARY KEY, quantity TEXT NOT NULL)`)
	_, definitions := proxy.schemaSnapshot()
	stored := definitions["products"]
	columns := cloneKitDBDDLColumns(stored.columns)
	columns["quantity"].kind = "integer"
	target := bindStructDef(stored.Name, stored, columns)
	if err := reconcileKitDBDefinition(stored, target); err != nil {
		t.Fatal(err)
	}

	managed, err := kitDBForRequest(tenant, "alter.kitdb", nil).database()
	if err != nil {
		t.Fatal(err)
	}
	managed.writeMu.Lock()
	const total = kitDBAtomicMigrationRows + 13
	insertSegmentedMigrationRows(t, managed.database, stored, total, total-1, "bad")
	_, err = beginKitDBSegmentedMigration(
		managed.database, stored, target, time.Date(2026, 8, 26, 20, 0, 0, 0, time.UTC),
		kitDBAtomicMigrationRows+1,
	)
	managed.writeMu.Unlock()
	if err != nil {
		managed.Release()
		t.Fatal(err)
	}
	ticket, err := scheduleKitDBRowMigration(managed)
	managed.Release()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ticket.Wait(context.Background()); err == nil ||
		!strings.Contains(err.Error(), `cannot cast text to integer`) {
		t.Fatalf("background invalid-row error = %v", err)
	}

	executeKitDBAlterSQL(
		t, proxy,
		fmt.Sprintf(`UPDATE products SET quantity = '%d' WHERE id = 'p%05d'`, total-1, total-1),
	)
	deadline := time.Now().Add(15 * time.Second)
	for {
		managed, err = kitDBForRequest(tenant, "alter.kitdb", nil).database()
		if err != nil {
			t.Fatal(err)
		}
		managed.writeMu.RLock()
		_, found, stateErr := loadKitDBRowMigrationState(managed.database, target)
		managed.writeMu.RUnlock()
		if stateErr != nil {
			managed.Release()
			t.Fatal(stateErr)
		}
		if !found {
			assertSegmentedMigrationValue(
				t, managed.database, target, total-1, value.Number, fmt.Sprint(total-1),
			)
			managed.Release()
			break
		}
		managed.Release()
		if time.Now().After(deadline) {
			t.Fatal("repair write did not wake the stopped background migration")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestKitDBBackgroundMigrationKeepsTransactionalSourceCRUD(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	router := `import { router, database } from "kitwork";
const db = database.kitdb("alter.kitdb", {}, { token: "alter-secret", access: "readwrite" });
router.get(() => db.products.count());`
	if err := os.WriteFile(filepath.Join(directory, RouterFileName), []byte(router), 0o644); err != nil {
		t.Fatal(err)
	}

	tenant, proxy := openKitDBAlterDatabase(t, root)
	defer tenant.Close()
	executeKitDBAlterSQL(t, proxy, `CREATE TABLE products (id TEXT PRIMARY KEY, legacy TEXT)`)
	_, definitions := proxy.schemaSnapshot()
	stored := definitions["products"]
	columns := cloneKitDBDDLColumns(stored.columns)
	delete(columns, "legacy")
	target := bindStructDef(stored.Name, stored, columns)
	if err := reconcileKitDBDefinition(stored, target); err != nil {
		t.Fatal(err)
	}
	field, found := kitDBField(stored, "legacy")
	if !found {
		t.Fatal("legacy field is unavailable")
	}
	intents := make(map[string]kitDBMigrationIntent)
	addKitDBMigrationIntent(intents, stored.ID, field.ID, "drop")
	steps := planKitDBMigrationWithIntent(stored, target, true, intents[stored.ID])

	managed, err := kitDBForRequest(tenant, "alter.kitdb", nil).database()
	if err != nil {
		t.Fatal(err)
	}
	managed.writeMu.Lock()
	const total = kitDBAtomicMigrationRows + 7
	insertSegmentedMigrationRows(t, managed.database, stored, total, -1, "")
	state, err := beginKitDBSegmentedMigration(
		managed.database, stored, target, time.Date(2026, 8, 26, 11, 0, 0, 0, time.UTC), total,
	)
	if err == nil {
		_, err = advanceKitDBSegmentedMigration(managed.database, state, steps)
	}
	managed.writeMu.Unlock()
	managed.Release()
	if err != nil {
		t.Fatal(err)
	}
	if result, err := runKitDBAlterSQL(proxy, `SELECT legacy FROM products LIMIT 1`); err != nil || len(result.rows) != 1 {
		t.Fatalf("source read during shadow build rows=%#v err=%v", result.rows, err)
	}
	// a-new sorts before the durable backfill cursor, so it can reach the
	// target generation only through dual-write. p00001 is already backfilled,
	// so deleting it proves the shadow copy is deleted with the source.
	record, err := beginKitDBRecordTransaction(tenant, nil, "alter.kitdb", true)
	if err != nil {
		t.Fatal(err)
	}
	proxy.transaction = record
	for _, statement := range []string{
		`INSERT INTO products (id, legacy) VALUES ('a-new', 'fresh')`,
		`UPDATE products SET legacy = 'changed' WHERE id = 'p00000'`,
		`DELETE FROM products WHERE id = 'p00001'`,
	} {
		if _, err := runKitDBAlterSQL(proxy, statement); err != nil {
			proxy.transaction = nil
			_ = record.Rollback()
			t.Fatalf("execute transactional %q: %v", statement, err)
		}
	}
	proxy.transaction = nil
	if _, err := record.Commit(); err != nil {
		t.Fatal(err)
	}
	// This test creates KRMS directly rather than through ALTER, so explicitly
	// mark the catalog-only proxy for the same post-cutover refresh ALTER does.
	proxy.markKitDBCatalogPending()
	deadline := time.Now().Add(15 * time.Second)
	for {
		status := executeKitDBAlterSQL(t, proxy, `PRAGMA migration_status(products)`)
		if len(status.rows) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("dual-write migration did not finish: %#v", status.rows)
		}
		time.Sleep(time.Millisecond)
	}
	count := executeKitDBAlterSQL(t, proxy, `SELECT COUNT(*) FROM products`)
	if len(count.rows) != 1 || count.rows[0][0].N != total {
		t.Fatalf("resumed row count = %#v", count.rows)
	}
	if _, err := runKitDBAlterSQL(proxy, `SELECT legacy FROM products`); err == nil ||
		!strings.Contains(err.Error(), "no such column: legacy") {
		t.Fatalf("dropped column query error = %v", err)
	}
	inserted := executeKitDBAlterSQL(t, proxy, `SELECT id FROM products WHERE id = 'a-new'`)
	if len(inserted.rows) != 1 || inserted.rows[0][0].String() != "a-new" {
		t.Fatalf("dual-written insert rows = %#v", inserted.rows)
	}
	deleted := executeKitDBAlterSQL(t, proxy, `SELECT id FROM products WHERE id = 'p00001'`)
	if len(deleted.rows) != 0 {
		t.Fatalf("dual-written delete rows = %#v", deleted.rows)
	}
}

func TestKitDBRemoteAlterContinuesInBackgroundWithoutRetry(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	router := `import { router, database } from "kitwork";
const db = database.kitdb("alter.kitdb", {}, { token: "background-secret", access: "readwrite" });
router.get(() => db.products.count());`
	if err := os.WriteFile(filepath.Join(directory, RouterFileName), []byte(router), 0o644); err != nil {
		t.Fatal(err)
	}

	tenant, proxy := openKitDBAlterDatabase(t, root)
	defer tenant.Close()
	executeKitDBAlterSQL(t, proxy, `CREATE TABLE products (id TEXT PRIMARY KEY, quantity TEXT NOT NULL)`)
	_, definitions := proxy.schemaSnapshot()
	stored := definitions["products"]
	managed, err := kitDBForRequest(tenant, "alter.kitdb", nil).database()
	if err != nil {
		t.Fatal(err)
	}
	managed.writeMu.Lock()
	const total = kitDBAtomicMigrationRows + 23
	insertSegmentedMigrationRows(t, managed.database, stored, total, -1, "")
	managed.writeMu.Unlock()
	managed.Release()

	if _, err := runKitDBAlterSQL(
		proxy, `ALTER TABLE products ALTER COLUMN quantity TYPE INTEGER`,
	); err == nil || !errors.Is(err, errKitDBRowMigrationPending) {
		t.Fatalf("initial online ALTER error = %v", err)
	}

	deadline := time.Now().Add(15 * time.Second)
	for {
		managed, err = kitDBForRequest(tenant, "alter.kitdb", nil).database()
		if err != nil {
			t.Fatal(err)
		}
		managed.writeMu.RLock()
		_, found, stateErr := loadKitDBRowMigrationState(managed.database, stored)
		managed.writeMu.RUnlock()
		managed.Release()
		if stateErr != nil {
			t.Fatal(stateErr)
		}
		if !found {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("background ALTER did not finish without a retry")
		}
		time.Sleep(time.Millisecond)
	}

	result := executeKitDBAlterSQL(
		t, proxy, fmt.Sprintf(`SELECT quantity FROM products WHERE id = 'p%05d'`, total-1),
	)
	if len(result.rows) != 1 || result.rows[0][0].K != value.Number ||
		result.rows[0][0].N != float64(total-1) {
		t.Fatalf("same-session schema did not refresh after background cutover: %#v", result.rows)
	}
	runtime := tenant.AppRuntime()
	resource := runtime.Resource(kitDBResourceName)
	manager, ok := resource.(*kitDBManager)
	if !ok || manager == nil {
		t.Fatalf("KitDB app manager = %T", resource)
	}
	if stats := manager.fleet.Stats(); stats.RowMigrationCompletions != 1 ||
		stats.RowMigrationChunks < 2 || stats.MaintenanceFailures != 0 {
		t.Fatalf("automatic row-migration stats = %#v", stats)
	}
}

func TestKitDBSegmentedTypeMigrationBlocksRepairsAndCutsOver(t *testing.T) {
	stored, target, intents := segmentedTypeMigrationDefinitions(t)
	database, err := kitdbengine.Open(filepath.Join(t.TempDir(), "segmented-type.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := commitKitDBCatalog(database, stored, nil, ""); err != nil {
		t.Fatal(err)
	}
	if ready, err := ensureKitDBSecondaryIndexCodec(database, stored); err != nil || !ready {
		t.Fatalf("initial physical index contract ready=%t err=%v", ready, err)
	}

	const total = kitDBAtomicMigrationRows + 11
	insertSegmentedMigrationRows(t, database, stored, total, total-1, "bad")
	definitions := map[string]*StructDef{target.Name: target}
	checkpoint := func() error { return nil }
	migrationStart, err := database.LastTransaction()
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureKitDBSchemaWithIntents(database, definitions, true, checkpoint, intents); err == nil ||
		!errors.Is(err, errKitDBRowMigrationPending) {
		t.Fatalf("first online migration step error = %v", err)
	}
	state, found, err := loadKitDBRowMigrationState(database, stored)
	if err != nil || !found || state.Rows != kitDBRowMigrationRowLimit ||
		state.TotalRows != kitDBAtomicMigrationRows+1 {
		t.Fatalf("online migration state found=%t state=%#v err=%v", found, state, err)
	}
	blocked := false
	for attempt := 0; attempt < 16; attempt++ {
		err = ensureKitDBSchemaWithIntents(database, definitions, true, checkpoint, intents)
		if strings.Contains(fmt.Sprint(err), `field "quantity" cannot cast text to integer`) {
			blocked = true
			break
		}
		if !errors.Is(err, errKitDBRowMigrationPending) {
			t.Fatalf("online migration progress error = %v", err)
		}
	}
	if !blocked {
		t.Fatal("online migration did not stop at the incompatible row")
	}
	state, found, err = loadKitDBRowMigrationState(database, stored)
	if err != nil || !found || state.Phase != kitDBRowMigrationPhaseRows || len(state.Progress) == 0 {
		t.Fatalf("blocked migration state found=%t state=%#v err=%v", found, state, err)
	}
	assertSegmentedMigrationValue(t, database, stored, 0, value.String, "0")

	putSegmentedMigrationRow(t, database, stored, total-1, fmt.Sprint(total-1))
	finishKitDBSegmentedMigration(t, database, definitions, intents)
	after, err := database.LastTransaction()
	if err != nil {
		t.Fatal(err)
	}
	if after-migrationStart < 5 {
		t.Fatalf("online migration used only %d durable commits", after-migrationStart)
	}
	finishKitDBRowCleanup(t, database, definitions)
	entry, found, err := database.CatalogStructByID(target.ID)
	if err != nil || !found || entry.Hash != target.Hash {
		t.Fatalf("target catalog found=%t hash=%q err=%v", found, entry.Hash, err)
	}
	assertSegmentedMigrationValue(t, database, target, 0, value.Number, "0")
	assertSegmentedMigrationValue(t, database, target, total-1, value.Number, fmt.Sprint(total-1))
	firstLayout, found, err := loadKitDBRowGenerationMetadata(database, target)
	if err != nil || !found || firstLayout.Active == 0 {
		t.Fatalf("first row generation found=%t metadata=%#v err=%v", found, firstLayout, err)
	}
	if firstLayout.Epoch == 0 {
		t.Fatal("first row generation did not advance its layout epoch")
	}
	if err := validateKitDBRowLayoutEpoch(database, target, firstLayout.Epoch-1); err == nil ||
		!errors.Is(err, errKitDBLayoutAdvanced) {
		t.Fatalf("stale row-layout epoch error = %v", err)
	}
	assertNoKitDBRowsAtGeneration(t, database, target, 0)

	secondColumns := cloneKitDBDDLColumns(target.columns)
	delete(secondColumns, "quantity")
	secondTarget := bindStructDef(target.Name, target, secondColumns)
	if err := reconcileKitDBDefinition(target, secondTarget); err != nil {
		t.Fatal(err)
	}
	quantity, found := kitDBField(target, "quantity")
	if !found {
		t.Fatal("migrated quantity field is unavailable")
	}
	secondIntents := make(map[string]kitDBMigrationIntent)
	addKitDBMigrationIntent(secondIntents, target.ID, quantity.ID, "drop")
	secondDefinitions := map[string]*StructDef{secondTarget.Name: secondTarget}
	finishKitDBSegmentedMigration(t, database, secondDefinitions, secondIntents)
	finishKitDBRowCleanup(t, database, secondDefinitions)
	secondLayout, found, err := loadKitDBRowGenerationMetadata(database, secondTarget)
	if err != nil || !found || secondLayout.Active == 0 || secondLayout.Active == firstLayout.Active {
		t.Fatalf("second row generation found=%t metadata=%#v first=%#v err=%v", found, secondLayout, firstLayout, err)
	}
	assertNoKitDBRowsAtGeneration(t, database, secondTarget, firstLayout.Active)
	row := loadSegmentedMigrationRow(t, database, secondTarget, total-1)
	if _, found := row.values["quantity"]; found || len(row.unknown) != 0 {
		t.Fatalf("second-generation row retained quantity: values=%#v unknown=%#v", row.values, row.unknown)
	}
}

func TestKitDBShadowMigrationKeepsUniqueLookupsAndDualWrites(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	router := `import { router, database } from "kitwork";
	const db = database.kitdb("alter.kitdb", {}, { token: "shadow-secret", access: "readwrite" });
router.get(() => db.products.count());`
	if err := os.WriteFile(filepath.Join(directory, RouterFileName), []byte(router), 0o644); err != nil {
		t.Fatal(err)
	}

	tenant, proxy := openKitDBAlterDatabase(t, root)
	defer tenant.Close()
	executeKitDBAlterSQL(t, proxy, `CREATE TABLE products (id TEXT PRIMARY KEY, code TEXT UNIQUE, quantity TEXT NOT NULL)`)
	_, definitions := proxy.schemaSnapshot()
	stored := definitions["products"]
	columns := cloneKitDBDDLColumns(stored.columns)
	columns["quantity"].kind = "integer"
	target := bindStructDef(stored.Name, stored, columns)
	if err := reconcileKitDBDefinition(stored, target); err != nil {
		t.Fatal(err)
	}
	field, found := kitDBField(stored, "quantity")
	if !found {
		t.Fatal("quantity field is unavailable")
	}
	intents := make(map[string]kitDBMigrationIntent)
	addKitDBMigrationIntent(intents, stored.ID, field.ID, "type")
	steps := planKitDBMigrationWithIntent(stored, target, true, intents[stored.ID])

	managed, err := kitDBForRequest(tenant, "alter.kitdb", nil).database()
	if err != nil {
		t.Fatal(err)
	}
	managed.writeMu.Lock()
	const total = kitDBAtomicMigrationRows + 9
	insertShadowIndexedRows(t, managed.database, stored, total, total-1, "not-a-number")
	state, err := beginKitDBSegmentedMigration(
		managed.database, stored, target, time.Date(2026, 8, 26, 13, 0, 0, 0, time.UTC), total,
	)
	if err == nil {
		_, err = advanceKitDBSegmentedMigration(managed.database, state, steps)
	}
	managed.writeMu.Unlock()
	managed.Release()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runKitDBAlterSQL(
		proxy, `INSERT INTO products (id, code, quantity) VALUES ('bad-new', 'code-bad', 'not-a-number')`,
	); err == nil || !strings.Contains(err.Error(), "cannot cast text to integer") {
		t.Fatalf("incompatible source write error = %v", err)
	}
	if result := executeKitDBAlterSQL(t, proxy, `SELECT id FROM products WHERE id = 'bad-new'`); len(result.rows) != 0 {
		t.Fatalf("incompatible source write was partially committed: %#v", result.rows)
	}

	// Both keys sort before the backfill cursor. Their target rows can only be
	// correct if ordinary source-schema writes maintain the shadow generation.
	executeKitDBAlterSQL(t, proxy, `UPDATE products SET quantity = '700' WHERE code = 'code-00000'`)
	executeKitDBAlterSQL(t, proxy, `INSERT INTO products (id, code, quantity) VALUES ('a-new', 'code-new', '41')`)
	executeKitDBAlterSQL(t, proxy, `DELETE FROM products WHERE code = 'code-00001'`)
	executeKitDBAlterSQL(t, proxy, fmt.Sprintf(
		`UPDATE products SET quantity = '%d' WHERE code = 'code-%05d'`, total-1, total-1,
	))
	finishKitDBRemoteAlter(t, proxy, `ALTER TABLE products ALTER COLUMN quantity TYPE INTEGER`)

	updated := executeKitDBAlterSQL(t, proxy, `SELECT quantity FROM products WHERE code = 'code-00000'`)
	if len(updated.rows) != 1 || updated.rows[0][0].K != value.Number || updated.rows[0][0].N != 700 {
		t.Fatalf("updated unique lookup rows = %#v", updated.rows)
	}
	inserted := executeKitDBAlterSQL(t, proxy, `SELECT quantity FROM products WHERE code = 'code-new'`)
	if len(inserted.rows) != 1 || inserted.rows[0][0].K != value.Number || inserted.rows[0][0].N != 41 {
		t.Fatalf("inserted unique lookup rows = %#v", inserted.rows)
	}
	deleted := executeKitDBAlterSQL(t, proxy, `SELECT quantity FROM products WHERE code = 'code-00001'`)
	if len(deleted.rows) != 0 {
		t.Fatalf("deleted unique lookup rows = %#v", deleted.rows)
	}
}

func TestKitDBRemoteMigrationStatusAndCancellationResume(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	router := `import { router, database } from "kitwork";
const db = database.kitdb("alter.kitdb", {}, { token: "cancel-secret", access: "readwrite" });
router.get(() => db.products.count());`
	if err := os.WriteFile(filepath.Join(directory, RouterFileName), []byte(router), 0o644); err != nil {
		t.Fatal(err)
	}

	tenant, proxy := openKitDBAlterDatabase(t, root)
	executeKitDBAlterSQL(t, proxy, `CREATE TABLE products (id TEXT PRIMARY KEY, quantity TEXT NOT NULL)`)
	_, definitions := proxy.schemaSnapshot()
	stored := definitions["products"]
	columns := cloneKitDBDDLColumns(stored.columns)
	columns["quantity"].kind = "integer"
	target := bindStructDef(stored.Name, stored, columns)
	if err := reconcileKitDBDefinition(stored, target); err != nil {
		t.Fatal(err)
	}
	field, found := kitDBField(stored, "quantity")
	if !found {
		t.Fatal("quantity field is unavailable")
	}
	intents := make(map[string]kitDBMigrationIntent)
	addKitDBMigrationIntent(intents, stored.ID, field.ID, "type")
	steps := planKitDBMigrationWithIntent(stored, target, true, intents[stored.ID])

	managed, err := kitDBForRequest(tenant, "alter.kitdb", nil).database()
	if err != nil {
		t.Fatal(err)
	}
	managed.writeMu.Lock()
	const total = kitDBAtomicMigrationRows + 17
	insertSegmentedMigrationRows(t, managed.database, stored, total, -1, "")
	state, err := beginKitDBSegmentedMigration(
		managed.database, stored, target, time.Date(2026, 8, 26, 15, 0, 0, 0, time.UTC), total,
	)
	for chunk := 0; err == nil && chunk < 3; chunk++ {
		var complete bool
		complete, err = advanceKitDBSegmentedMigration(managed.database, state, steps)
		if complete {
			err = fmt.Errorf("shadow migration completed before cancellation test")
			break
		}
		state, found, err = loadKitDBRowMigrationState(managed.database, stored)
		if err == nil && !found {
			err = fmt.Errorf("shadow migration state disappeared before cancellation")
		}
	}
	managed.writeMu.Unlock()
	managed.Release()
	if err != nil {
		t.Fatal(err)
	}
	targetGeneration := state.TargetGeneration

	status := executeKitDBAlterSQL(t, proxy, `PRAGMA migration_status(products)`)
	assertKitDBMigrationStatus(t, status, "building", kitDBRowMigrationRowLimit*3, 0, 1, 0)
	executeKitDBAlterSQL(t, proxy, `ALTER TABLE products CANCEL MIGRATION`)
	status = executeKitDBAlterSQL(t, proxy, `PRAGMA migration_status(products)`)
	if len(status.rows) != 0 {
		values := kitDBMigrationStatusValues(t, status)
		if values["phase"].String() != "cancelling" || values["can_cancel"].N != 0 ||
			values["target_published"].N != 0 ||
			values["cleaned_rows"].N < kitDBRowMigrationRowLimit ||
			values["cleaned_rows"].N > values["processed_rows"].N {
			t.Fatalf("automatic cancellation status = %#v", values)
		}
	}

	// Once cancellation is durable, source writes no longer maintain the
	// abandoned target. This value is intentionally invalid for INTEGER.
	executeKitDBAlterSQL(
		t, proxy, `INSERT INTO products (id, quantity) VALUES ('after-cancel', 'not-a-number')`,
	)
	tenant.Close()

	tenant, proxy = openKitDBAlterDatabase(t, root)
	defer tenant.Close()
	deadline := time.Now().Add(15 * time.Second)
	for {
		status = executeKitDBAlterSQL(t, proxy, `PRAGMA migration_status(products)`)
		if len(status.rows) == 0 {
			break
		}
		values := kitDBMigrationStatusValues(t, status)
		if values["phase"].String() != "cancelling" || values["can_cancel"].N != 0 {
			t.Fatalf("reopened cancellation status = %#v", values)
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if len(status.rows) != 0 {
		t.Fatalf("cancellation did not finish: %#v", status.rows)
	}
	// Cancellation is idempotent once no durable state remains.
	executeKitDBAlterSQL(t, proxy, `ALTER TABLE products CANCEL MIGRATION`)
	row := executeKitDBAlterSQL(t, proxy, `SELECT quantity FROM products WHERE id = 'after-cancel'`)
	if len(row.rows) != 1 || row.rows[0][0].String() != "not-a-number" {
		t.Fatalf("source row after cancellation = %#v", row.rows)
	}
	count := executeKitDBAlterSQL(t, proxy, `SELECT COUNT(*) FROM products`)
	if len(count.rows) != 1 || count.rows[0][0].N != total+1 {
		t.Fatalf("source count after cancellation = %#v", count.rows)
	}

	managed, err = kitDBForRequest(tenant, "alter.kitdb", nil).database()
	if err != nil {
		t.Fatal(err)
	}
	defer managed.Release()
	if _, found, err := loadKitDBRowMigrationState(managed.database, stored); err != nil || found {
		t.Fatalf("cancelled state found=%t err=%v", found, err)
	}
	assertNoKitDBRowsAtGeneration(t, managed.database, target, targetGeneration)
	entry, found, err := managed.database.CatalogStructByID(stored.ID)
	if err != nil || !found || entry.Hash != stored.Hash {
		t.Fatalf("source catalog after cancellation found=%t hash=%q err=%v", found, entry.Hash, err)
	}
}

func TestKitDBSegmentedDropMigrationResumesAfterRestart(t *testing.T) {
	stored, target, intents := segmentedDropMigrationDefinitions(t)
	path := filepath.Join(t.TempDir(), "segmented-drop.kitdb")
	database, err := kitdbengine.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := commitKitDBCatalog(database, stored, nil, ""); err != nil {
		t.Fatal(err)
	}
	if ready, err := ensureKitDBSecondaryIndexCodec(database, stored); err != nil || !ready {
		t.Fatalf("codec ready=%t err=%v", ready, err)
	}

	const total = kitDBAtomicMigrationRows + 13
	insertSegmentedMigrationRows(t, database, stored, total, -1, "")
	steps := planKitDBMigrationWithIntent(stored, target, true, intents[stored.ID])
	entry := &kitDBSchemaPlanEntry{stored: stored, current: target, from: stored.Hash, steps: steps}
	if _, _, ok := kitDBSegmentedMigrationCandidate(
		entry, map[string]*StructDef{target.Name: target},
	); !ok {
		t.Fatal("drop migration was not admitted to the segmented executor")
	}
	state, err := beginKitDBSegmentedMigration(
		database, stored, target, time.Date(2026, 8, 26, 10, 0, 0, 0, time.UTC), total,
	)
	if err != nil {
		t.Fatal(err)
	}
	complete, err := advanceKitDBSegmentedMigration(database, state, steps)
	if err != nil || complete {
		t.Fatalf("first chunk complete=%t err=%v", complete, err)
	}
	state, found, err := loadKitDBRowMigrationState(database, stored)
	if err != nil || !found || state.Rows != kitDBRowMigrationRowLimit || len(state.Progress) == 0 {
		t.Fatalf("durable row cursor found=%t state=%#v err=%v", found, state, err)
	}
	if err := validateKitDBNoPendingRowMigration(database, stored); !errors.Is(err, errKitDBRowMigrationPending) {
		t.Fatalf("ordinary access fence error = %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	database, err = kitdbengine.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := ensureKitDBSchema(
		database, map[string]*StructDef{stored.Name: stored}, true, func() error { return nil },
	); err != nil {
		t.Fatalf("reopened source schema error = %v", err)
	}
	definitions := map[string]*StructDef{target.Name: target}
	finishKitDBSegmentedMigration(t, database, definitions, intents)
	state, found, err = loadKitDBRowMigrationState(database, target)
	if err != nil || !found || state.Phase != kitDBRowMigrationPhaseCleanup {
		t.Fatalf("published cleanup state found=%t state=%#v err=%v", found, state, err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	database, err = kitdbengine.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	finishKitDBRowCleanup(t, database, definitions)
	for _, number := range []int{0, kitDBRowMigrationRowLimit - 1, kitDBRowMigrationRowLimit, total - 1} {
		row := loadSegmentedMigrationRow(t, database, target, number)
		if _, found := row.values["legacy"]; found || len(row.unknown) != 0 {
			t.Fatalf("row %d retained dropped field: values=%#v unknown=%#v", number, row.values, row.unknown)
		}
	}
}

func TestKitDBSegmentedMigrationStateRejectsCorruption(t *testing.T) {
	stored, target, _ := segmentedTypeMigrationDefinitions(t)
	sourceCatalog, err := json.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	targetCatalog, err := json.Marshal(target)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := encodeKitDBRowMigrationState(kitDBRowMigrationState{
		StartedAt: 7, MigrationUnixNano: 8, TotalRows: kitDBAtomicMigrationRows + 1,
		SourceHash: stored.Hash, TargetHash: target.Hash,
		SourceDefinition: sourceCatalog, TargetDefinition: targetCatalog,
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded[len(encoded)/2] ^= 0x40
	if _, err := decodeKitDBRowMigrationState(encoded); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("corrupt row-migration state error = %v", err)
	}
}

func TestKitDBShadowMigrationStateAllowsOnlineCardinalityDrift(t *testing.T) {
	stored, target, _ := segmentedTypeMigrationDefinitions(t)
	sourceCatalog, err := json.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	targetCatalog, err := json.Marshal(target)
	if err != nil {
		t.Fatal(err)
	}
	retiredPrefix, err := kitDBPhysicalRowPrefix(stored, 0)
	if err != nil {
		t.Fatal(err)
	}
	state := kitDBRowMigrationState{
		StartedAt: 7, MigrationUnixNano: 8,
		Rows: kitDBAtomicMigrationRows + 2, TotalRows: kitDBAtomicMigrationRows + 1,
		Phase: kitDBRowMigrationPhaseRows, TargetGeneration: 8,
		RetiredPrefix: retiredPrefix,
		SourceHash:    stored.Hash, TargetHash: target.Hash,
		SourceDefinition: sourceCatalog, TargetDefinition: targetCatalog,
	}
	encoded, err := encodeKitDBRowMigrationState(state)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeKitDBRowMigrationState(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Rows <= decoded.TotalRows {
		t.Fatalf("online row progress=%d did not exceed admission lower bound=%d", decoded.Rows, decoded.TotalRows)
	}
}

func TestKitDBLegacySegmentedMigrationStateResumesInPlace(t *testing.T) {
	stored, target, intents := segmentedDropMigrationDefinitions(t)
	database, err := kitdbengine.Open(filepath.Join(t.TempDir(), "legacy-segmented.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := commitKitDBCatalog(database, stored, nil, ""); err != nil {
		t.Fatal(err)
	}
	const total = kitDBAtomicMigrationRows + 3
	insertSegmentedMigrationRows(t, database, stored, total, -1, "")
	sourceCatalog, err := json.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	targetCatalog, err := json.Marshal(target)
	if err != nil {
		t.Fatal(err)
	}
	startedAt, err := database.LastTransaction()
	if err != nil {
		t.Fatal(err)
	}
	state := kitDBRowMigrationState{
		StartedAt: startedAt, MigrationUnixNano: time.Date(2026, 8, 26, 9, 0, 0, 0, time.UTC).UnixNano(),
		TotalRows: total, SourceHash: stored.Hash, TargetHash: target.Hash,
		SourceDefinition: sourceCatalog, TargetDefinition: targetCatalog,
	}
	encoded, err := encodeKitDBRowMigrationState(state)
	if err != nil {
		t.Fatal(err)
	}
	stateKey, err := kitDBRowMigrationStateKey(stored)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Put(stateKey, encoded); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := beginKitDBRowMigrationCancellation(database, state); err == nil ||
		!strings.Contains(err.Error(), "cannot be cancelled safely") {
		t.Fatalf("legacy cancellation error = %v", err)
	}
	steps := planKitDBMigrationWithIntent(stored, target, true, intents[stored.ID])
	entry := &kitDBSchemaPlanEntry{stored: stored, current: target, from: stored.Hash, steps: steps}
	if err := applyKitDBSegmentedMigration(database, entry, time.Now().UTC(), 0); err != nil {
		t.Fatal(err)
	}
	if _, found, err := loadKitDBRowMigrationState(database, target); err != nil || found {
		t.Fatalf("legacy state found=%t err=%v", found, err)
	}
	if _, found, err := loadKitDBRowGenerationMetadata(database, target); err != nil || found {
		t.Fatalf("legacy migration unexpectedly published row generation found=%t err=%v", found, err)
	}
	row := loadSegmentedMigrationRow(t, database, target, total-1)
	if _, found := row.values["legacy"]; found {
		t.Fatalf("legacy resumed row retained dropped field: %#v", row.values)
	}
}

func TestKitDBSegmentedMigrationRejectsPhysicalFieldDependencies(t *testing.T) {
	stored := bindStructDef("products", nil, map[string]*ColumnSpec{
		"id":       {kind: "text", primary: true, seq: 1},
		"quantity": {kind: "text", unique: true, seq: 2},
	})
	target := bindStructDef("products", nil, map[string]*ColumnSpec{
		"id":       {kind: "text", primary: true, seq: 1},
		"quantity": {kind: "integer", unique: true, seq: 2},
	})
	if err := reconcileKitDBDefinition(stored, target); err != nil {
		t.Fatal(err)
	}
	field, found := kitDBField(stored, "quantity")
	if !found {
		t.Fatal("quantity field is unavailable")
	}
	intent := kitDBMigrationIntent{actions: map[string]string{field.ID: "type"}}
	entry := &kitDBSchemaPlanEntry{
		stored: stored, current: target,
		steps: planKitDBMigrationWithIntent(stored, target, true, intent),
	}
	if _, _, ok := kitDBSegmentedMigrationCandidate(
		entry, map[string]*StructDef{target.Name: target},
	); ok {
		t.Fatal("unique field was admitted to segmented in-place migration")
	}
}

func segmentedTypeMigrationDefinitions(t *testing.T) (*StructDef, *StructDef, map[string]kitDBMigrationIntent) {
	t.Helper()
	stored := bindStructDef("products", nil, map[string]*ColumnSpec{
		"id":       {kind: "text", primary: true, seq: 1},
		"quantity": {kind: "text", notNull: true, seq: 2},
	})
	target := bindStructDef("products", nil, map[string]*ColumnSpec{
		"id":       {kind: "text", primary: true, seq: 1},
		"quantity": {kind: "integer", notNull: true, seq: 2},
	})
	if err := reconcileKitDBDefinition(stored, target); err != nil {
		t.Fatal(err)
	}
	field, found := kitDBField(stored, "quantity")
	if !found {
		t.Fatal("quantity field is unavailable")
	}
	intents := make(map[string]kitDBMigrationIntent)
	addKitDBMigrationIntent(intents, stored.ID, field.ID, "type")
	return stored, target, intents
}

func segmentedDropMigrationDefinitions(t *testing.T) (*StructDef, *StructDef, map[string]kitDBMigrationIntent) {
	t.Helper()
	stored := bindStructDef("products", nil, map[string]*ColumnSpec{
		"id":     {kind: "text", primary: true, seq: 1},
		"legacy": {kind: "text", seq: 2},
	})
	target := bindStructDef("products", nil, map[string]*ColumnSpec{
		"id": {kind: "text", primary: true, seq: 1},
	})
	if err := reconcileKitDBDefinition(stored, target); err != nil {
		t.Fatal(err)
	}
	field, found := kitDBField(stored, "legacy")
	if !found {
		t.Fatal("legacy field is unavailable")
	}
	intents := make(map[string]kitDBMigrationIntent)
	addKitDBMigrationIntent(intents, stored.ID, field.ID, "drop")
	return stored, target, intents
}

func insertSegmentedMigrationRows(
	t *testing.T,
	database *kitdbengine.DB,
	definition *StructDef,
	total int,
	override int,
	overrideValue string,
) {
	t.Helper()
	tx, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for number := 0; number < total; number++ {
		quantity := fmt.Sprint(number)
		if number == override {
			quantity = overrideValue
		}
		row := map[string]value.Value{
			"id": value.New(fmt.Sprintf("p%05d", number)),
		}
		if _, found := definition.columns["quantity"]; found {
			row["quantity"] = value.New(quantity)
		}
		if _, found := definition.columns["legacy"]; found {
			row["legacy"] = value.New("remove-" + quantity)
		}
		rowKey, err := kitDBRowKey(definition, row["id"])
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := encodeKitDBValidatedRow(definition, row, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := tx.Put(rowKey, encoded); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func insertShadowIndexedRows(
	t *testing.T,
	database *kitdbengine.DB,
	definition *StructDef,
	total int,
	override int,
	overrideValue string,
) {
	t.Helper()
	tx, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for number := 0; number < total; number++ {
		quantity := fmt.Sprint(number)
		if number == override {
			quantity = overrideValue
		}
		row := map[string]value.Value{
			"id":       value.New(fmt.Sprintf("p%05d", number)),
			"code":     value.New(fmt.Sprintf("code-%05d", number)),
			"quantity": value.New(quantity),
		}
		rowKey, err := kitDBRowKey(definition, row["id"])
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := encodeKitDBValidatedRow(definition, row, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := tx.Put(rowKey, encoded); err != nil {
			t.Fatal(err)
		}
		entries, err := kitDBIndexEntries(definition, row, rowKey)
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
		t.Fatal(err)
	}
}

func putSegmentedMigrationRow(
	t *testing.T,
	database *kitdbengine.DB,
	definition *StructDef,
	number int,
	quantity string,
) {
	t.Helper()
	row := map[string]value.Value{
		"id": value.New(fmt.Sprintf("p%05d", number)), "quantity": value.New(quantity),
	}
	rowKey, err := kitDBRowKey(definition, row["id"])
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := encodeKitDBValidatedRow(definition, row, nil)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Put(rowKey, encoded); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if _, err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func assertSegmentedMigrationValue(
	t *testing.T,
	database *kitdbengine.DB,
	definition *StructDef,
	number int,
	kind value.Kind,
	want string,
) {
	t.Helper()
	row := loadSegmentedMigrationRow(t, database, definition, number)
	quantity := row.values["quantity"]
	if quantity.K != kind || quantity.Text() != want {
		t.Fatalf("row %d quantity=%#v text=%q, want kind=%s text=%q", number, quantity, quantity.Text(), kind, want)
	}
}

func loadSegmentedMigrationRow(
	t *testing.T,
	database *kitdbengine.DB,
	definition *StructDef,
	number int,
) kitDBDecodedRow {
	t.Helper()
	rowKey, err := kitDBRowKey(definition, value.New(fmt.Sprintf("p%05d", number)))
	if err != nil {
		t.Fatal(err)
	}
	encoded, found, err := getKitDBLogicalRow(database, definition, rowKey)
	if err != nil || !found {
		t.Fatalf("row %d found=%t err=%v", number, found, err)
	}
	row, err := decodeKitDBRow(definition, encoded)
	if err != nil {
		t.Fatal(err)
	}
	return row
}

func finishKitDBRemoteAlter(t *testing.T, proxy *dbProxy, statement string) {
	t.Helper()
	for attempt := 0; attempt < 64; attempt++ {
		_, err := runKitDBAlterSQL(proxy, statement)
		if err == nil {
			return
		}
		if !errors.Is(err, errKitDBRowMigrationPending) {
			t.Fatal(err)
		}
	}
	t.Fatal("remote ALTER did not finish its bounded shadow migration")
}

func finishKitDBSegmentedMigration(
	t *testing.T,
	database *kitdbengine.DB,
	definitions map[string]*StructDef,
	intents map[string]kitDBMigrationIntent,
) {
	t.Helper()
	for attempt := 0; attempt < 64; attempt++ {
		err := ensureKitDBSchemaWithIntents(
			database, definitions, true, func() error { return nil }, intents,
		)
		if err == nil {
			return
		}
		if !errors.Is(err, errKitDBRowMigrationPending) {
			t.Fatal(err)
		}
	}
	t.Fatal("segmented shadow migration did not cut over")
}

func finishKitDBRowCleanup(
	t *testing.T,
	database *kitdbengine.DB,
	definitions map[string]*StructDef,
) {
	t.Helper()
	definition := definitions[sortedKitDBDefinitionNames(definitions)[0]]
	for attempt := 0; attempt < 64; attempt++ {
		_, found, err := loadKitDBRowMigrationState(database, definition)
		if err != nil {
			t.Fatal(err)
		}
		if !found {
			return
		}
		if err := ensureKitDBSchema(database, definitions, true, func() error { return nil }); err != nil {
			t.Fatal(err)
		}
	}
	t.Fatal("retired row cleanup did not finish")
}

func assertNoKitDBRowsAtGeneration(
	t *testing.T,
	database *kitdbengine.DB,
	definition *StructDef,
	generation uint64,
) {
	t.Helper()
	prefix, err := kitDBPhysicalRowPrefix(definition, generation)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := database.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	cursor, err := snapshot.Cursor(kitdbengine.RangeOptions{Prefix: prefix, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer cursor.Close()
	if cursor.Next() {
		t.Fatalf("retired row generation %d still has key %x", generation, cursor.Key())
	}
	if err := cursor.Err(); err != nil {
		t.Fatal(err)
	}
}

func assertKitDBMigrationStatus(
	t *testing.T,
	result kitDBRemoteResult,
	phase string,
	processed int,
	cleaned int,
	canCancel int,
	targetPublished int,
) {
	t.Helper()
	values := kitDBMigrationStatusValues(t, result)
	if values["phase"].String() != phase || values["processed_rows"].N != float64(processed) ||
		values["cleaned_rows"].N != float64(cleaned) ||
		values["can_cancel"].N != float64(canCancel) ||
		values["target_published"].N != float64(targetPublished) {
		t.Fatalf("migration status = %#v", values)
	}
}

func kitDBMigrationStatusValues(
	t *testing.T,
	result kitDBRemoteResult,
) map[string]value.Value {
	t.Helper()
	if len(result.rows) != 1 || len(result.rows[0]) != len(result.columns) {
		t.Fatalf("migration status shape columns=%#v rows=%#v", result.columns, result.rows)
	}
	values := make(map[string]value.Value, len(result.columns))
	for index, column := range result.columns {
		values[column.name] = result.rows[0][index]
	}
	return values
}
