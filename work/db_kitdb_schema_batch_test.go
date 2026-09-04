package work

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	kitdbengine "github.com/kitwork/engine/kitdb"
	"github.com/kitwork/engine/value"
)

func TestKitDBMultiStructMigrationIsAtomicAndSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "schema-batch.kitdb")
	database, err := kitdbengine.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	catalogTransactions := make(map[uint64]int)
	removeListener, err := database.AddCommitListener(func(event kitdbengine.CommitEvent) {
		for _, operation := range event.Operations {
			if len(operation.Key) != 0 && operation.Key[0] == 0x01 {
				catalogTransactions[event.Transaction]++
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}

	versionOne := kitDBSchemaBatchDefinitions(false)
	if err := ensureKitDBSchema(database, versionOne, false, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	removeListener()
	if len(catalogTransactions) != 1 {
		t.Fatalf("initial schema used %d catalog transactions, want one: %#v", len(catalogTransactions), catalogTransactions)
	}
	for transaction, operations := range catalogTransactions {
		if operations != 2 {
			t.Fatalf("initial schema transaction %d contains %d catalog definitions, want two", transaction, operations)
		}
	}
	seedKitDBSchemaBatchRows(t, database, versionOne)

	versionTwo := kitDBSchemaBatchDefinitions(true)
	beforeRejected, err := database.LastTransaction()
	if err != nil {
		t.Fatal(err)
	}
	err = ensureKitDBSchema(database, versionTwo, true, func() error { return nil })
	if err == nil || !strings.Contains(err.Error(), `constraint "orders_account"`) {
		t.Fatalf("dirty multi-struct migration error = %v", err)
	}
	afterRejected, err := database.LastTransaction()
	if err != nil {
		t.Fatal(err)
	}
	if afterRejected != beforeRejected {
		t.Fatalf("rejected schema batch committed transaction %d -> %d", beforeRejected, afterRejected)
	}
	assertKitDBCatalogHashes(t, database, versionOne)
	assertKitDBCompositeUniqueMissing(t, database, versionTwo["accounts"], "north", "A")

	invalidKey, err := kitDBRowKey(versionOne["orders"], value.New("invalid"))
	if err != nil {
		t.Fatal(err)
	}
	remove, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := remove.Delete(invalidKey); err != nil {
		t.Fatal(err)
	}
	if _, err := remove.Commit(); err != nil {
		t.Fatal(err)
	}

	beforeApplied, err := database.LastTransaction()
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureKitDBSchema(database, versionTwo, true, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	afterApplied, err := database.LastTransaction()
	if err != nil {
		t.Fatal(err)
	}
	if afterApplied != beforeApplied+1 {
		t.Fatalf("schema batch used %d transactions, want exactly one", afterApplied-beforeApplied)
	}
	assertKitDBCatalogHashes(t, database, versionTwo)
	assertKitDBCompositeUniquePresent(t, database, versionTwo["accounts"], "north", "A")

	accountAudit := readKitDBMigrationAudit(t, database, versionTwo["accounts"])
	orderAudit := readKitDBMigrationAudit(t, database, versionTwo["orders"])
	if accountAudit.Version != 2 || accountAudit.Batch == "" || accountAudit.Batch != orderAudit.Batch {
		t.Fatalf("migration audits do not share one batch: accounts=%#v orders=%#v", accountAudit, orderAudit)
	}
	if accountAudit.AppliedAt != orderAudit.AppliedAt {
		t.Fatalf("migration audit timestamps differ: %q != %q", accountAudit.AppliedAt, orderAudit.AppliedAt)
	}

	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	database, err = kitdbengine.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	assertKitDBCatalogHashes(t, database, versionTwo)
	assertKitDBCompositeUniquePresent(t, database, versionTwo["accounts"], "north", "A")
}

func TestKitDBMultiStructMigrationRefusesMixedOnlineIndexBuild(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mixed-index-batch.kitdb")
	database, err := kitdbengine.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	versionOne := kitDBMixedIndexBatchDefinitions(false)
	if err := ensureKitDBSchema(database, versionOne, false, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	seedKitDBRows(t, database, versionOne["products"], []map[string]value.Value{{
		"id": value.New("p1"), "title": value.New("Kitwork"),
	}})
	seedKitDBRows(t, database, versionOne["settings"], []map[string]value.Value{{
		"id": value.New("s1"), "name": value.New("theme"),
	}})

	versionTwo := kitDBMixedIndexBatchDefinitions(true)
	before, err := database.LastTransaction()
	if err != nil {
		t.Fatal(err)
	}
	err = ensureKitDBSchema(database, versionTwo, true, func() error { return nil })
	if err == nil || !strings.Contains(err.Error(), "deploy that index change separately") {
		t.Fatalf("mixed online index batch error = %v", err)
	}
	after, err := database.LastTransaction()
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("refused mixed index batch committed transaction %d -> %d", before, after)
	}
	assertKitDBCatalogHashes(t, database, versionOne)
}

func kitDBSchemaBatchDefinitions(constrained bool) map[string]*StructDef {
	accountColumns := map[string]*ColumnSpec{
		"id":     {kind: "text", primary: true, seq: 1},
		"tenant": {kind: "text", notNull: true, seq: 2},
		"code":   {kind: "text", notNull: true, seq: 3},
	}
	orderColumns := map[string]*ColumnSpec{
		"id":             {kind: "text", primary: true, seq: 1},
		"account_tenant": {kind: "text", seq: 2},
		"account_code":   {kind: "text", seq: 3},
	}
	if constrained {
		accountColumns["tenant"].uniques = []colUniqueRef{{name: "accounts_tenant_code", pos: 1}}
		accountColumns["code"].uniques = []colUniqueRef{{name: "accounts_tenant_code", pos: 2}}
		orderColumns["account_tenant"].fk = &fkRef{
			target: accountColumns["tenant"], name: "orders_account", pos: 1,
		}
		orderColumns["account_code"].fk = &fkRef{
			target: accountColumns["code"], name: "orders_account", pos: 2,
		}
		resolver := &dbProxy{tables: map[string]map[string]*ColumnSpec{
			"accounts": accountColumns,
			"orders":   orderColumns,
		}}
		resolver.resolveForeignKeys()
	}
	definitions := map[string]*StructDef{
		"accounts": bindStructDef("accounts", nil, accountColumns),
		"orders":   bindStructDef("orders", nil, orderColumns),
	}
	return definitions
}

func kitDBMixedIndexBatchDefinitions(changed bool) map[string]*StructDef {
	productColumns := map[string]*ColumnSpec{
		"id":    {kind: "text", primary: true, seq: 1},
		"title": {kind: "text", notNull: true, seq: 2},
	}
	settingColumns := map[string]*ColumnSpec{
		"id":   {kind: "text", primary: true, seq: 1},
		"name": {kind: "text", notNull: true, seq: 2},
	}
	if changed {
		productColumns["title"].indexes = []colIndexRef{{name: "products_title_idx"}}
		settingColumns["enabled"] = &ColumnSpec{
			kind: "bool", hasDefault: true, def: value.New(true), seq: 3,
		}
	}
	return map[string]*StructDef{
		"products": bindStructDef("products", nil, productColumns),
		"settings": bindStructDef("settings", nil, settingColumns),
	}
}

func seedKitDBSchemaBatchRows(
	t *testing.T,
	database *kitdbengine.DB,
	definitions map[string]*StructDef,
) {
	t.Helper()
	seedKitDBRows(t, database, definitions["accounts"], []map[string]value.Value{
		{"id": value.New("a1"), "tenant": value.New("north"), "code": value.New("A")},
		{"id": value.New("a2"), "tenant": value.New("south"), "code": value.New("B")},
	})
	seedKitDBRows(t, database, definitions["orders"], []map[string]value.Value{
		{"id": value.New("valid"), "account_tenant": value.New("north"), "account_code": value.New("A")},
		{"id": value.New("invalid"), "account_tenant": value.New("north"), "account_code": value.New("B")},
	})
}

func seedKitDBRows(
	t *testing.T,
	database *kitdbengine.DB,
	definition *StructDef,
	rows []map[string]value.Value,
) {
	t.Helper()
	tx, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if err := validateKitDBRow(definition, row); err != nil {
			t.Fatal(err)
		}
		primary, _ := definition.primaryField()
		rowKey, err := kitDBRowKey(definition, row[primary.Name])
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
		indexes, err := kitDBIndexEntries(definition, row, rowKey)
		if err != nil {
			t.Fatal(err)
		}
		for _, index := range indexes {
			if err := tx.Put(index.key, index.value); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func assertKitDBCatalogHashes(
	t *testing.T,
	database *kitdbengine.DB,
	definitions map[string]*StructDef,
) {
	t.Helper()
	for name, definition := range definitions {
		entry, found, err := database.CatalogStructByID(definition.ID)
		if err != nil || !found || entry.Hash != definition.Hash {
			t.Fatalf("catalog %q = %#v found=%v err=%v, want hash %s", name, entry, found, err, definition.Hash)
		}
	}
}

func assertKitDBCompositeUniqueMissing(
	t *testing.T,
	database *kitdbengine.DB,
	definition *StructDef,
	tenant, code string,
) {
	t.Helper()
	key := kitDBTestCompositeUniqueKey(t, definition, tenant, code)
	if _, found, err := database.Get(key); err != nil || found {
		t.Fatalf("composite unique key exists after rejected migration: found=%v err=%v", found, err)
	}
}

func assertKitDBCompositeUniquePresent(
	t *testing.T,
	database *kitdbengine.DB,
	definition *StructDef,
	tenant, code string,
) {
	t.Helper()
	key := kitDBTestCompositeUniqueKey(t, definition, tenant, code)
	if _, found, err := database.Get(key); err != nil || !found {
		t.Fatalf("composite unique key is missing: found=%v err=%v", found, err)
	}
}

func kitDBTestCompositeUniqueKey(
	t *testing.T,
	definition *StructDef,
	tenant, code string,
) []byte {
	t.Helper()
	if len(definition.UniqueConstraints) != 1 {
		t.Fatalf("struct %q has %d composite unique constraints", definition.Name, len(definition.UniqueConstraints))
	}
	key, applicable, err := kitDBCompositeUniqueKey(
		definition,
		definition.UniqueConstraints[0],
		map[string]value.Value{"tenant": value.New(tenant), "code": value.New(code)},
	)
	if err != nil || !applicable {
		t.Fatalf("composite unique key: applicable=%v err=%v", applicable, err)
	}
	return key
}

func readKitDBMigrationAudit(
	t *testing.T,
	database *kitdbengine.DB,
	definition *StructDef,
) kitDBMigrationAudit {
	t.Helper()
	auditID := stableSchemaID("migration", definition.ID+":"+definition.Hash)
	key, err := kitDBFixedKey(kitDBMigrationNamespace, definition.ID, auditID)
	if err != nil {
		t.Fatal(err)
	}
	encoded, found, err := database.Get(key)
	if err != nil || !found {
		t.Fatalf("migration audit for %q: found=%v err=%v", definition.Name, found, err)
	}
	var audit kitDBMigrationAudit
	if err := json.Unmarshal(encoded, &audit); err != nil {
		t.Fatal(err)
	}
	return audit
}
