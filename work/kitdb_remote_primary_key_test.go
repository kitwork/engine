package work

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	kitdbengine "github.com/kitwork/engine/kitdb"
	"github.com/kitwork/engine/value"
)

func TestKitDBSQLParsesAlterPrimaryKey(t *testing.T) {
	statement, err := parseKitSQL(
		`ALTER TABLE shopping ALTER PRIMARY KEY (merchant, id)`,
		kitSQLBindings{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if statement.kind != "alter_primary_key" || statement.table != "shopping" ||
		statement.alterPrimaryKey == nil || len(statement.alterPrimaryKey.columns) != 2 ||
		statement.alterPrimaryKey.columns[0] != "merchant" ||
		statement.alterPrimaryKey.columns[1] != "id" {
		t.Fatalf("ALTER PRIMARY KEY statement = %#v", statement)
	}
}

func TestKitDBRemoteAlterPrimaryKeyRewritesRowsAndSurvivesRestart(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	router := `import { router, database } from "kitwork";
const db = database.kitdb("alter.kitdb", {}, { token: "rekey-secret", access: "readwrite" });
router.get(() => db.products.count());`
	if err := os.WriteFile(filepath.Join(directory, RouterFileName), []byte(router), 0o644); err != nil {
		t.Fatal(err)
	}

	tenant, database := openKitDBAlterDatabase(t, root)
	executeKitDBAlterSQL(t, database, `CREATE TABLE products (
  _key TEXT PRIMARY KEY,
  merchant TEXT NOT NULL,
  id INTEGER NOT NULL,
  title TEXT
)`)
	executeKitDBAlterSQL(t, database, `INSERT INTO products (_key, merchant, id, title) VALUES
  ('legacy-1', 'shop-a', 1, 'Keyboard'),
  ('legacy-2', 'shop-a', 2, 'Mouse'),
  ('legacy-3', 'shop-b', 1, 'Monitor')`)
	if _, err := runKitDBAlterSQL(
		database, `ALTER TABLE products ALTER PRIMARY KEY (merchant, id)`,
	); err != nil && !errors.Is(err, errKitDBRowMigrationPending) {
		t.Fatal(err)
	}
	waitForKitDBPrimaryMigration(t, database, "products")

	if err := database.refreshKitDBCatalogDefinitions(); err != nil {
		t.Fatal(err)
	}
	_, definitions := database.schemaSnapshot()
	primary := definitions["products"].primaryFields()
	if len(primary) != 2 || primary[0].Name != "merchant" || primary[0].PrimaryOrder != 1 ||
		primary[1].Name != "id" || primary[1].PrimaryOrder != 2 {
		t.Fatalf("published primary key = %#v", primary)
	}
	row := executeKitDBAlterSQL(
		t, database,
		`SELECT _key, title FROM products WHERE merchant = 'shop-a' AND id = 2`,
	)
	if len(row.rows) != 1 || row.rows[0][0].String() != "legacy-2" ||
		row.rows[0][1].String() != "Mouse" {
		t.Fatalf("composite primary lookup = %#v", row.rows)
	}
	plan := executeKitDBAlterSQL(
		t, database,
		`EXPLAIN SELECT title FROM products WHERE merchant = 'shop-a' AND id = 2`,
	)
	primaryPlan := false
	if len(plan.rows) == 1 {
		for _, item := range plan.rows[0] {
			primaryPlan = primaryPlan || strings.Contains(item.String(), "PRIMARY KEY")
		}
	}
	if !primaryPlan {
		t.Fatalf("composite primary plan = %#v", plan.rows)
	}
	if _, err := runKitDBAlterSQL(
		database,
		`INSERT INTO products (_key, merchant, id, title) VALUES ('legacy-4', 'shop-a', 2, 'Duplicate')`,
	); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("duplicate composite primary error = %v", err)
	}

	tenant.Close()
	tenant, database = openKitDBAlterDatabase(t, root)
	defer tenant.Close()
	reopened := executeKitDBAlterSQL(
		t, database,
		`SELECT _key, title FROM products WHERE merchant = 'shop-b' AND id = 1`,
	)
	if len(reopened.rows) != 1 || reopened.rows[0][0].String() != "legacy-3" ||
		reopened.rows[0][1].String() != "Monitor" {
		t.Fatalf("reopened composite primary lookup = %#v", reopened.rows)
	}
}

func TestKitDBPrimaryKeyMigrationResumesRowsAndIndexesAfterRestart(t *testing.T) {
	stored, target, steps := primaryMigrationDefinitions(t)
	path := filepath.Join(t.TempDir(), "primary-restart.kitdb")
	database, err := kitdbengine.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := commitKitDBCatalog(database, stored, nil, ""); err != nil {
		t.Fatal(err)
	}
	if ready, err := ensureKitDBSecondaryIndexCodec(database, stored); err != nil || !ready {
		t.Fatalf("initial index codec ready=%t err=%v", ready, err)
	}
	const total = 5_123
	insertPrimaryMigrationRows(t, database, stored, total, -1)
	state, err := beginKitDBSegmentedMigration(
		database, stored, target,
		time.Date(2026, 8, 29, 8, 0, 0, 0, time.UTC),
		kitDBAtomicMigrationRows+1,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !state.PrimaryKeyChange || len(state.RetiredIndexes) != 2 || len(state.TargetIndexes) != 1 {
		t.Fatalf("primary migration physical contract = %#v", state)
	}
	complete, err := advanceKitDBSegmentedMigration(database, state, steps)
	if err != nil || complete {
		t.Fatalf("first primary migration chunk complete=%t err=%v", complete, err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	database, err = kitdbengine.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	for attempt := 0; attempt < 64; attempt++ {
		state, found, err := loadKitDBRowMigrationState(database, stored)
		if err != nil {
			t.Fatal(err)
		}
		if !found {
			break
		}
		switch state.Phase {
		case kitDBRowMigrationPhaseRows, kitDBRowMigrationPhaseVerify:
			if _, err := advanceKitDBSegmentedMigration(database, state, steps); err != nil {
				t.Fatal(err)
			}
		case kitDBRowMigrationPhaseCleanup:
			if _, err := advanceKitDBRetiredRowCleanup(database, state); err != nil {
				t.Fatal(err)
			}
		default:
			t.Fatalf("unexpected primary migration phase %q", state.Phase)
		}
	}
	if _, found, err := loadKitDBRowMigrationState(database, target); err != nil || found {
		t.Fatalf("completed primary migration state found=%t err=%v", found, err)
	}
	entry, found, err := database.CatalogStructByID(target.ID)
	if err != nil || !found || entry.Hash != target.Hash {
		t.Fatalf("target catalog found=%t entry=%#v err=%v", found, entry, err)
	}
	rowKey, err := kitDBRowKeyForRow(target, map[string]value.Value{
		"merchant": value.New("shop-005"), "id": value.New(5_122),
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, found, err := getKitDBLogicalRow(database, target, rowKey)
	if err != nil || !found {
		t.Fatalf("rekeyed row found=%t err=%v", found, err)
	}
	decoded, err := decodeKitDBRow(target, encoded)
	if err != nil || decoded.values["_key"].String() != "legacy-05122" {
		t.Fatalf("rekeyed row=%#v err=%v", decoded.values, err)
	}
	rowMetadata, found, err := loadKitDBRowGenerationMetadata(database, target)
	if err != nil || !found || rowMetadata.Active != state.TargetGeneration || len(rowMetadata.Retired) != 0 {
		t.Fatalf("row generations found=%t metadata=%#v err=%v", found, rowMetadata, err)
	}
	indexMetadata, _, err := loadKitDBIndexGenerationMetadata(database, target)
	if err != nil || len(indexMetadata.Active) != 1 || len(indexMetadata.Retired) != 0 {
		t.Fatalf("index generations metadata=%#v err=%v", indexMetadata, err)
	}
	for _, prefix := range state.RetiredIndexes {
		assertNoKitDBKeysWithPrefix(t, database, prefix)
	}
}

func TestKitDBPrimaryKeyMigrationRejectsDuplicatesAndCancelsCleanly(t *testing.T) {
	stored, target, steps := primaryMigrationDefinitions(t)
	database, err := kitdbengine.Open(filepath.Join(t.TempDir(), "primary-duplicate.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := commitKitDBCatalog(database, stored, nil, ""); err != nil {
		t.Fatal(err)
	}
	if ready, err := ensureKitDBSecondaryIndexCodec(database, stored); err != nil || !ready {
		t.Fatalf("initial index codec ready=%t err=%v", ready, err)
	}
	insertPrimaryMigrationRows(t, database, stored, 2, 1)
	state, err := beginKitDBSegmentedMigration(
		database, stored, target, time.Now().UTC(), kitDBAtomicMigrationRows+1,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateKitDBWriteDefinition(database, stored); err == nil ||
		!strings.Contains(err.Error(), "writes") || !strings.Contains(err.Error(), "paused") {
		t.Fatalf("primary migration write fence error = %v", err)
	}
	if complete, err := advanceKitDBSegmentedMigration(database, state, steps); err == nil || complete ||
		!strings.Contains(err.Error(), "duplicate primary key") {
		t.Fatalf("duplicate primary migration complete=%t err=%v", complete, err)
	}
	state, found, err := loadKitDBRowMigrationState(database, stored)
	if err != nil || !found || state.Rows != 0 {
		t.Fatalf("duplicate primary state found=%t state=%#v err=%v", found, state, err)
	}
	state, err = beginKitDBRowMigrationCancellation(database, state)
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 32; attempt++ {
		complete, _, err := advanceKitDBCancelledRowCleanup(database, state)
		if err != nil {
			t.Fatal(err)
		}
		if complete {
			break
		}
		state, found, err = loadKitDBRowMigrationState(database, stored)
		if err != nil || !found {
			t.Fatalf("cancel state found=%t err=%v", found, err)
		}
	}
	if _, found, err := loadKitDBRowMigrationState(database, stored); err != nil || found {
		t.Fatalf("cancelled primary migration state found=%t err=%v", found, err)
	}
	if err := validateKitDBWriteDefinition(database, stored); err != nil {
		t.Fatalf("source writes remained fenced after cancellation: %v", err)
	}
	entry, found, err := database.CatalogStructByID(stored.ID)
	if err != nil || !found || entry.Hash != stored.Hash {
		t.Fatalf("source catalog after cancellation found=%t entry=%#v err=%v", found, entry, err)
	}
}

func TestKitDBPrimaryKeyMigrationDetectsCrossChunkDuplicatesBeforeCutover(t *testing.T) {
	stored, target, steps := primaryMigrationDefinitions(t)
	database, err := kitdbengine.Open(filepath.Join(t.TempDir(), "primary-cross-chunk-duplicate.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := commitKitDBCatalog(database, stored, nil, ""); err != nil {
		t.Fatal(err)
	}
	if ready, err := ensureKitDBSecondaryIndexCodec(database, stored); err != nil || !ready {
		t.Fatalf("initial index codec ready=%t err=%v", ready, err)
	}
	const total = kitDBPrimaryKeyBuildRowLimit + 1
	insertPrimaryMigrationRows(t, database, stored, total, total-1)
	state, err := beginKitDBSegmentedMigration(
		database, stored, target, time.Now().UTC(), kitDBAtomicMigrationRows+1,
	)
	if err != nil {
		t.Fatal(err)
	}
	detected := false
	for attempt := 0; attempt < 8; attempt++ {
		complete, advanceErr := advanceKitDBSegmentedMigration(database, state, steps)
		if advanceErr != nil {
			if !strings.Contains(advanceErr.Error(), "duplicate primary key") {
				t.Fatal(advanceErr)
			}
			detected = true
			break
		}
		if complete {
			t.Fatal("cross-chunk duplicate primary key was published")
		}
		state, _, err = loadKitDBRowMigrationState(database, stored)
		if err != nil {
			t.Fatal(err)
		}
	}
	if !detected {
		t.Fatal("cross-chunk duplicate primary key was not detected")
	}
	if state.Phase != kitDBRowMigrationPhaseVerify {
		t.Fatalf("duplicate detection phase = %q, want verify", state.Phase)
	}
	entry, found, err := database.CatalogStructByID(stored.ID)
	if err != nil || !found || entry.Hash != stored.Hash {
		t.Fatalf("source catalog before cancellation found=%t entry=%#v err=%v", found, entry, err)
	}
	state, err = beginKitDBRowMigrationCancellation(database, state)
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 32; attempt++ {
		complete, _, err := advanceKitDBCancelledRowCleanup(database, state)
		if err != nil {
			t.Fatal(err)
		}
		if complete {
			break
		}
		state, found, err = loadKitDBRowMigrationState(database, stored)
		if err != nil || !found {
			t.Fatalf("cancel state found=%t err=%v", found, err)
		}
	}
	if _, found, err := loadKitDBRowMigrationState(database, stored); err != nil || found {
		t.Fatalf("cancelled cross-chunk state found=%t err=%v", found, err)
	}
}

func primaryMigrationDefinitions(t *testing.T) (*StructDef, *StructDef, []planStep) {
	t.Helper()
	indexID := stableSchemaID("index", "shopping:merchant-id")
	stored := bindStructDef("shopping", nil, map[string]*ColumnSpec{
		"_key":     {kind: "text", primary: true, notNull: true, seq: 1},
		"merchant": {kind: "text", notNull: true, seq: 2, indexes: []colIndexRef{{name: "shopping_merchant_id_idx", id: indexID, pos: 1}}},
		"id":       {kind: "integer", notNull: true, seq: 3, indexes: []colIndexRef{{name: "shopping_merchant_id_idx", id: indexID, pos: 2}}},
		"title":    {kind: "text", seq: 4},
	})
	columns := cloneKitDBDDLColumns(stored.columns)
	columns["_key"].primary = false
	columns["_key"].primaryOrder = 0
	columns["_key"].unique = false
	columns["merchant"].primary = true
	columns["merchant"].primaryOrder = 1
	columns["id"].primary = true
	columns["id"].primaryOrder = 2
	target := bindStructDef(stored.Name, stored, columns)
	if err := reconcileKitDBDefinition(stored, target); err != nil {
		t.Fatal(err)
	}
	intents := make(map[string]kitDBMigrationIntent)
	for _, field := range append(stored.primaryFields(), target.primaryFields()...) {
		addKitDBMigrationIntent(intents, stored.ID, field.ID, "primary")
	}
	steps := planKitDBMigrationWithIntent(stored, target, true, intents[stored.ID])
	entry := &kitDBSchemaPlanEntry{stored: stored, current: target, steps: steps}
	if !kitDBPrimaryKeyMigrationCandidate(entry, map[string]*StructDef{target.Name: target}) {
		t.Fatalf("primary migration was not admitted: %#v", steps)
	}
	return stored, target, steps
}

func insertPrimaryMigrationRows(
	t *testing.T,
	database *kitdbengine.DB,
	definition *StructDef,
	total int,
	duplicateAt int,
) {
	t.Helper()
	rowGeneration, _, err := loadKitDBActiveRowLayout(database, definition)
	if err != nil {
		t.Fatal(err)
	}
	indexMetadata, _, err := loadKitDBIndexGenerationMetadata(database, definition)
	if err != nil {
		t.Fatal(err)
	}
	physicalIndexes := kitDBPhysicalIndexes(
		collectKitDBIndexes(definition),
		kitDBIndexGenerationMap(collectKitDBIndexes(definition), indexMetadata),
	)
	for start := 0; start < total; start += 256 {
		end := start + 256
		if end > total {
			end = total
		}
		tx, err := database.Begin()
		if err != nil {
			t.Fatal(err)
		}
		for number := start; number < end; number++ {
			merchant := fmt.Sprintf("shop-%03d", number%17)
			id := number
			if number == duplicateAt {
				merchant = "shop-000"
				id = 0
			}
			row := map[string]value.Value{
				"_key":     value.New(fmt.Sprintf("legacy-%05d", number)),
				"merchant": value.New(merchant), "id": value.New(id),
				"title": value.New(fmt.Sprintf("Product %d", number)),
			}
			logicalKey, err := kitDBRowKeyForRow(definition, row)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := encodeKitDBValidatedRow(definition, row, nil)
			if err != nil {
				t.Fatal(err)
			}
			physicalKey, err := kitDBPhysicalRowKey(definition, logicalKey, rowGeneration)
			if err != nil {
				t.Fatal(err)
			}
			if err := tx.Put(physicalKey, encoded); err != nil {
				t.Fatal(err)
			}
			entries, err := kitDBIndexEntriesForPhysical(definition, row, logicalKey, physicalIndexes)
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
}

func assertNoKitDBKeysWithPrefix(t *testing.T, database *kitdbengine.DB, prefix []byte) {
	t.Helper()
	snapshot, err := database.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	cursor, err := snapshot.Cursor(kitdbengine.RangeOptions{Prefix: bytes.Clone(prefix), Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer cursor.Close()
	if cursor.Next() {
		t.Fatalf("retired prefix %x still contains key %x", prefix, cursor.Key())
	}
	if err := cursor.Err(); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
}

func waitForKitDBPrimaryMigration(t *testing.T, database *dbProxy, table string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		status := executeKitDBAlterSQL(t, database, `PRAGMA migration_status(`+table+`)`)
		if len(status.rows) == 0 {
			return
		}
		values := kitDBMigrationStatusValues(t, status)
		if values["mode"].String() != "rekey" {
			t.Fatalf("primary-key migration status = %#v", values)
		}
		if time.Now().After(deadline) {
			t.Fatalf("primary-key migration did not finish: %#v", values)
		}
		time.Sleep(time.Millisecond)
	}
}
