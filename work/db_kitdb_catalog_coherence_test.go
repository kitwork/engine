package work

import (
	"errors"
	"strings"
	"sync"
	"testing"
)

func TestKitDBCatalogCoherenceAcrossIndependentProxies(t *testing.T) {
	root := newKitDBRenameTestRoot(t)
	tenant, primary := openKitDBDropDatabase(t, root)
	defer func() { tenant.Close() }()
	peer := newIndependentKitDBCatalogProxy(t, primary)
	if err := peer.refreshKitDBCatalogDefinitions(); err != nil {
		t.Fatal(err)
	}

	executeKitDBDropSQL(t, primary, `CREATE TABLE inventory (
  id TEXT PRIMARY KEY,
  sku TEXT NOT NULL UNIQUE,
  status TEXT NOT NULL,
  price INTEGER NOT NULL
)`)
	executeKitDBDropSQL(t, primary, `INSERT INTO inventory (id, sku, status, price)
VALUES ('item_1', 'KIT-1', 'active', 100)`)
	rows := executeKitDBDropSQL(t, peer, `SELECT sku FROM inventory WHERE id = 'item_1'`)
	if len(rows.rows) != 1 || rows.rows[0][0].String() != "KIT-1" {
		t.Fatalf("peer CREATE visibility rows = %#v", rows.rows)
	}

	_, definitions, _, revision := peer.schemaSnapshotWithCatalog()
	before := definitions["inventory"]
	if before == nil || revision == "" {
		t.Fatalf("peer did not publish inventory at a catalog revision: %#v, %q", before, revision)
	}
	executeKitDBDropSQL(t, primary, `INSERT INTO inventory (id, sku, status, price)
VALUES ('item_2', 'KIT-2', 'disabled', 200)`)
	if err := peer.refreshKitDBCatalogDefinitions(); err != nil {
		t.Fatal(err)
	}
	_, definitions, _, afterRecordRevision := peer.schemaSnapshotWithCatalog()
	if afterRecordRevision != revision {
		t.Fatalf("record commit changed catalog revision: %q -> %q", revision, afterRecordRevision)
	}
	if definitions["inventory"] != before {
		t.Fatal("record-only commit decoded and republished an unchanged catalog")
	}

	executeKitDBDropSQL(t, primary, `ALTER TABLE inventory RENAME TO products`)
	rows = executeKitDBDropSQL(t, peer, `SELECT sku FROM products ORDER BY sku`)
	if len(rows.rows) != 2 || rows.rows[0][0].String() != "KIT-1" || rows.rows[1][0].String() != "KIT-2" {
		t.Fatalf("peer table rename rows = %#v", rows.rows)
	}
	if _, err := runKitDBDropSQL(peer, `SELECT * FROM inventory`); err == nil ||
		!strings.Contains(err.Error(), "no such table") {
		t.Fatalf("peer retained old table name: %v", err)
	}

	executeKitDBDropSQL(t, primary, `CREATE INDEX products_status_price_idx ON products (status, price)`)
	waitForKitDBSecondaryIndexIdle(t, primary)
	indexes := executeKitDBDropSQL(t, peer, `PRAGMA index_list(products)`)
	if !kitDBRenameResultHasString(indexes, "products_status_price_idx") {
		t.Fatalf("peer CREATE INDEX visibility = %#v", indexes.rows)
	}
	executeKitDBDropSQL(t, primary,
		`ALTER INDEX products_status_price_idx RENAME TO products_state_price_idx`)
	indexes = executeKitDBDropSQL(t, peer, `PRAGMA index_list(products)`)
	if kitDBRenameResultHasString(indexes, "products_status_price_idx") ||
		!kitDBRenameResultHasString(indexes, "products_state_price_idx") {
		t.Fatalf("peer index rename visibility = %#v", indexes.rows)
	}

	executeKitDBDropSQL(t, primary, `DROP TABLE products`)
	if _, err := runKitDBDropSQL(peer, `SELECT * FROM products`); err == nil ||
		!strings.Contains(err.Error(), "no such table") {
		t.Fatalf("peer retained dropped table: %v", err)
	}
	_, definitions = peer.schemaSnapshot()
	if definitions["products"] != nil {
		t.Fatal("peer retained dropped catalog-owned definition")
	}
	if definitions["protected_records"] == nil {
		t.Fatal("catalog replacement removed a source-declared struct")
	}
	executeKitDBDropSQL(t, peer, `SELECT COUNT(*) FROM protected_records`)

	tenant.Close()
	tenant, primary = openKitDBDropDatabase(t, root)
	if _, err := runKitDBDropSQL(primary, `SELECT * FROM products`); err == nil ||
		!strings.Contains(err.Error(), "no such table") {
		t.Fatalf("dropped table reappeared after restart: %v", err)
	}
	executeKitDBDropSQL(t, primary, `SELECT COUNT(*) FROM protected_records`)
}

func TestKitDBRecordTransactionPinsCatalogRevision(t *testing.T) {
	root := newKitDBRenameTestRoot(t)
	tenant, primary := openKitDBDropDatabase(t, root)
	defer tenant.Close()
	peer := newIndependentKitDBCatalogProxy(t, primary)

	executeKitDBDropSQL(t, primary, `CREATE TABLE sessions (
  id TEXT PRIMARY KEY,
  label TEXT NOT NULL
)`)
	executeKitDBDropSQL(t, primary,
		`INSERT INTO sessions (id, label) VALUES ('session_1', 'before rename')`)
	if err := peer.refreshKitDBCatalogDefinitions(); err != nil {
		t.Fatal(err)
	}
	transactionProxy, transaction, err := peer.beginKitDBRecordTransaction(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer transaction.Rollback()

	executeKitDBDropSQL(t, primary, `ALTER TABLE sessions RENAME TO active_sessions`)
	rows := executeKitDBDropSQL(t, transactionProxy,
		`SELECT label FROM sessions WHERE id = 'session_1'`)
	if len(rows.rows) != 1 || rows.rows[0][0].String() != "before rename" {
		t.Fatalf("transaction did not retain its schema snapshot: %#v", rows.rows)
	}
	if _, err := runKitDBDropSQL(transactionProxy, `SELECT * FROM active_sessions`); err == nil ||
		!strings.Contains(err.Error(), "no such table") {
		t.Fatalf("transaction observed a later catalog: %v", err)
	}
	if _, err := transaction.Commit(); !errors.Is(err, errKitDBTransactionConflict) {
		t.Fatalf("schema-advanced transaction commit error = %v", err)
	}

	rows = executeKitDBDropSQL(t, peer,
		`SELECT label FROM active_sessions WHERE id = 'session_1'`)
	if len(rows.rows) != 1 || rows.rows[0][0].String() != "before rename" {
		t.Fatalf("peer did not advance after transaction conflict: %#v", rows.rows)
	}
	if _, err := runKitDBDropSQL(peer, `SELECT * FROM sessions`); err == nil ||
		!strings.Contains(err.Error(), "no such table") {
		t.Fatalf("peer retained old name after advancing: %v", err)
	}
}

func TestKitDBCatalogRefreshPublishesAtomicSnapshots(t *testing.T) {
	root := newKitDBRenameTestRoot(t)
	tenant, primary := openKitDBDropDatabase(t, root)
	defer tenant.Close()
	peer := newIndependentKitDBCatalogProxy(t, primary)
	executeKitDBDropSQL(t, primary, `CREATE TABLE inventory (id TEXT PRIMARY KEY, sku TEXT NOT NULL)`)
	if err := peer.refreshKitDBCatalogDefinitions(); err != nil {
		t.Fatal(err)
	}

	stop := make(chan struct{})
	failures := make(chan string, 1)
	report := func(message string) {
		select {
		case failures <- message:
		default:
		}
	}
	var readers sync.WaitGroup
	for range 4 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if err := peer.refreshKitDBCatalogDefinitions(); err != nil {
					report(err.Error())
					return
				}
				_, definitions := peer.schemaSnapshot()
				hasInventory := definitions["inventory"] != nil
				hasProducts := definitions["products"] != nil
				if hasInventory == hasProducts {
					report("catalog snapshot exposed both rename names or neither rename name")
					return
				}
				if definitions["protected_records"] == nil {
					report("catalog snapshot lost a source-declared struct")
					return
				}
			}
		}()
	}
	for range 12 {
		executeKitDBDropSQL(t, primary, `ALTER TABLE inventory RENAME TO products`)
		executeKitDBDropSQL(t, primary, `ALTER TABLE products RENAME TO inventory`)
	}
	close(stop)
	readers.Wait()
	select {
	case failure := <-failures:
		t.Fatal(failure)
	default:
	}
	if err := peer.refreshKitDBCatalogDefinitions(); err != nil {
		t.Fatal(err)
	}
	_, definitions := peer.schemaSnapshot()
	if definitions["inventory"] == nil || definitions["products"] != nil {
		t.Fatalf("final catalog snapshot = %#v", definitions)
	}
}

func newIndependentKitDBCatalogProxy(t *testing.T, source *dbProxy) *dbProxy {
	t.Helper()
	if err := source.refreshKitDBCatalogDefinitions(); err != nil {
		t.Fatal(err)
	}
	source.schemaMu.RLock()
	declarations := append([]string(nil), source.declaredTables...)
	tables := make(map[string]map[string]*ColumnSpec, len(declarations))
	definitions := make(map[string]*StructDef, len(declarations))
	for _, name := range declarations {
		definition := source.structs[name]
		if definition == nil {
			source.schemaMu.RUnlock()
			t.Fatalf("source declaration %q is unavailable", name)
		}
		definitions[name] = definition
		tables[name] = definition.columns
	}
	source.schemaMu.RUnlock()
	return &dbProxy{
		tenant: source.tenant, scope: source.scope, engine: "kitdb", dbName: source.dbName,
		allowDrop: source.allowDrop, migrate: source.migrate,
		tables: tables, structs: definitions, declaredTables: declarations,
	}
}
