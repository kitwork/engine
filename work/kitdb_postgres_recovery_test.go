package work

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	kitdbengine "github.com/kitwork/engine/kitdb"
	"github.com/kitwork/engine/value"
	"github.com/lib/pq"
)

func TestKitDBPostgresCreateDatabaseAsOfTimestamp(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	router := `import { router, database } from "kitwork";
const { kitdb, struct, id, text, int } = database;
const products = struct({
  id: id(),
  title: text().notNull(),
  price: int().default(0)
});
const source = kitdb("products.kitdb", { products }, { token: "recovery-secret", access: "readwrite", recovery: true });
const noHistory = kitdb("plain.kitdb", {}, { token: "recovery-secret", access: "readwrite" });
router.get(() => source.products.count());`
	if err := os.WriteFile(filepath.Join(directory, RouterFileName), []byte(router), 0o644); err != nil {
		t.Fatal(err)
	}
	tenant := NewTenant(root, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	defer tenant.Close()

	sourceManaged, err := kitDBForRequest(tenant, "products.kitdb", nil).database()
	if err != nil {
		t.Fatal(err)
	}
	defer sourceManaged.Release()
	var eventMu sync.Mutex
	var events []time.Time
	removeListener, err := sourceManaged.database.AddCommitListener(func(event kitdbengine.CommitEvent) {
		eventMu.Lock()
		events = append(events, event.CommittedAt)
		eventMu.Unlock()
	})
	if err != nil {
		t.Fatal(err)
	}
	defer removeListener()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serverContext, stopServer := context.WithCancel(context.Background())
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- tenant.ServeKitDBPostgres(serverContext, listener, KitDBPostgresOptions{
			MaintenanceDatabase: "kitdb", User: "kitdb", MaxConnections: 8,
			IdleTimeout: 5 * time.Second, QueryTimeout: 15 * time.Second,
		})
	}()
	defer func() {
		stopServer()
		select {
		case serveErr := <-serverDone:
			if serveErr != nil {
				t.Errorf("ServeKitDBPostgres: %v", serveErr)
			}
		case <-time.After(5 * time.Second):
			t.Error("ServeKitDBPostgres did not stop")
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	source := openKitDBPostgresDatabaseTestClient(t, listener.Addr().String(), "products", "recovery-secret")
	defer source.Close()
	if _, err := source.ExecContext(ctx, `INSERT INTO products (id, title, price) VALUES ('product-1', 'Before', 100)`); err != nil {
		t.Fatalf("insert source product: %v", err)
	}
	eventMu.Lock()
	if len(events) == 0 {
		eventMu.Unlock()
		t.Fatal("source insert emitted no commit timestamp")
	}
	target := events[len(events)-1]
	eventMu.Unlock()
	if _, err := source.ExecContext(ctx, `UPDATE products SET title = 'After', price = 200 WHERE id = 'product-1'`); err != nil {
		t.Fatalf("update source product: %v", err)
	}

	maintenance := openKitDBPostgresDatabaseTestClient(t, listener.Addr().String(), "kitdb", "recovery-secret")
	defer maintenance.Close()
	if _, err := source.ExecContext(ctx, `CREATE DATABASE wrong_connection`); err == nil {
		t.Fatal("CREATE DATABASE was accepted outside the maintenance database")
	} else {
		var postgresErr *pq.Error
		if !errors.As(err, &postgresErr) || postgresErr.Code != "0A000" {
			t.Fatalf("non-maintenance CREATE DATABASE error = %T %v", err, err)
		}
	}
	command := fmt.Sprintf(
		"CREATE DATABASE products_recovered FROM products AS OF TIMESTAMP '%s'",
		target.Format(time.RFC3339Nano),
	)
	if _, err := maintenance.ExecContext(ctx, command); err != nil {
		t.Fatalf("CREATE DATABASE AS OF TIMESTAMP: %v", err)
	}
	if names := queryKitDBPostgresStrings(t, ctx, maintenance, `
SELECT datname FROM pg_catalog.pg_database ORDER BY datname`); !containsString(names, "products_recovered") {
		t.Fatalf("recovered database is absent from pg_database: %v", names)
	}

	recovered := openKitDBPostgresDatabaseTestClient(t, listener.Addr().String(), "products_recovered", "recovery-secret")
	defer recovered.Close()
	var recoveredTitle string
	var recoveredPrice int64
	if err := recovered.QueryRowContext(ctx, `SELECT title, price FROM products WHERE id = 'product-1'`).Scan(&recoveredTitle, &recoveredPrice); err != nil {
		t.Fatalf("query recovered product: %v", err)
	}
	if recoveredTitle != "Before" || recoveredPrice != 100 {
		t.Fatalf("recovered product = (%q, %d), want (Before, 100)", recoveredTitle, recoveredPrice)
	}
	var sourceTitle string
	if err := source.QueryRowContext(ctx, `SELECT title FROM products WHERE id = 'product-1'`).Scan(&sourceTitle); err != nil {
		t.Fatal(err)
	}
	if sourceTitle != "After" {
		t.Fatalf("source product = %q, want After", sourceTitle)
	}
	if _, err := recovered.ExecContext(ctx, `INSERT INTO products (id, title, price) VALUES ('fork-only', 'Independent', 300)`); err != nil {
		t.Fatalf("write recovered database: %v", err)
	}
	var sourceCount int64
	if err := source.QueryRowContext(ctx, `SELECT COUNT(*) FROM products WHERE id = 'fork-only'`).Scan(&sourceCount); err != nil {
		t.Fatal(err)
	}
	if sourceCount != 0 {
		t.Fatalf("recovered write leaked into source: count=%d", sourceCount)
	}
	targetManaged, err := kitDBForRequest(tenant, "products_recovered.kitdb", nil).database()
	if err != nil {
		t.Fatal(err)
	}
	if targetManaged.database.ID() == sourceManaged.database.ID() {
		t.Errorf("recovered database reused source identity %q", targetManaged.database.ID())
	}
	targetManaged.Release()

	if _, err := maintenance.ExecContext(ctx, command); err == nil {
		t.Fatal("duplicate recovery destination was accepted")
	} else {
		var postgresErr *pq.Error
		if !errors.As(err, &postgresErr) || postgresErr.Code != "42P04" {
			t.Fatalf("duplicate recovery error = %T %v", err, err)
		}
	}
	if _, err := maintenance.ExecContext(ctx, `
CREATE DATABASE products_too_old FROM products
AS OF TIMESTAMP '2000-01-01T00:00:00Z'`); err == nil {
		t.Fatal("recovery before retained history was accepted")
	} else {
		var postgresErr *pq.Error
		if !errors.As(err, &postgresErr) || postgresErr.Code != "22008" {
			t.Fatalf("too-old recovery error = %T %v", err, err)
		}
	}
	if _, err := maintenance.ExecContext(ctx, `
CREATE DATABASE plain_recovered FROM plain
AS OF TIMESTAMP '2026-08-27T14:30:00+07:00'`); err == nil {
		t.Fatal("recovery without retained history was accepted")
	} else {
		var postgresErr *pq.Error
		if !errors.As(err, &postgresErr) || postgresErr.Code != "55000" {
			t.Fatalf("history-disabled recovery error = %T %v", err, err)
		}
	}

	transaction, err := maintenance.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.ExecContext(ctx, `
CREATE DATABASE transaction_recovered FROM products
AS OF TIMESTAMP '2026-08-27T14:30:00+07:00'`); err == nil {
		_ = transaction.Rollback()
		t.Fatal("CREATE DATABASE was accepted inside a transaction")
	} else {
		var postgresErr *pq.Error
		if !errors.As(err, &postgresErr) || postgresErr.Code != "25001" {
			_ = transaction.Rollback()
			t.Fatalf("transaction recovery error = %T %v", err, err)
		}
	}
	_ = transaction.Rollback()

	if _, err := recovered.ExecContext(ctx, `DROP DATABASE "products_recovered"`); err == nil {
		t.Fatal("current recovered database dropped itself")
	} else {
		var postgresErr *pq.Error
		if !errors.As(err, &postgresErr) || postgresErr.Code != "55006" {
			t.Fatalf("current database DROP error = %T %v", err, err)
		}
	}
	if _, err := maintenance.ExecContext(ctx, `DROP DATABASE products_recovered`); err == nil {
		t.Fatal("maintenance dropped a database with an active PostgreSQL session")
	} else {
		var postgresErr *pq.Error
		if !errors.As(err, &postgresErr) || postgresErr.Code != "55006" {
			t.Fatalf("active database DROP error = %T %v", err, err)
		}
	}
	if err := recovered.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := maintenance.ExecContext(ctx, `DROP DATABASE products`); err == nil {
		t.Fatal("source-declared database was dropped")
	} else {
		var postgresErr *pq.Error
		if !errors.As(err, &postgresErr) || postgresErr.Code != "0A000" {
			t.Fatalf("source-declared database DROP error = %T %v", err, err)
		}
	}
	if _, err := maintenance.ExecContext(ctx, `DROP DATABASE plain`); err != nil {
		t.Fatalf("DROP DATABASE catalog-only source attachment: %v", err)
	}
	if names := queryKitDBPostgresStrings(t, ctx, maintenance,
		`SELECT datname FROM pg_catalog.pg_database ORDER BY datname`); containsString(names, "plain") {
		t.Fatalf("dropped catalog-only attachment remains in pg_database: %v", names)
	}
	dropKitDBPostgresDatabase(t, ctx, maintenance, "products_recovered")
	if names := queryKitDBPostgresStrings(t, ctx, maintenance,
		`SELECT datname FROM pg_catalog.pg_database ORDER BY datname`); containsString(names, "products_recovered") {
		t.Fatalf("dropped database remains in pg_database: %v", names)
	}
	for _, removed := range []string{
		filepath.Join(directory, ".data", "products_recovered.kitdb"),
		filepath.Join(directory, ".data", "products_recovered.kitdb.wal"),
		filepath.Join(directory, ".data", "products_recovered.kitdb.lock"),
		filepath.Join(directory, ".data", "products_recovered.kitdb.history"),
	} {
		if _, err := os.Lstat(removed); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("dropped path %q error = %v", removed, err)
		}
	}
	missing := openKitDBPostgresDatabaseTestClient(
		t, listener.Addr().String(), "products_recovered", "recovery-secret",
	)
	if err := missing.PingContext(ctx); err == nil {
		missing.Close()
		t.Fatal("dropped database accepted a new PostgreSQL connection")
	}
	_ = missing.Close()
	if _, err := maintenance.ExecContext(ctx, `DROP DATABASE IF EXISTS products_recovered`); err != nil {
		t.Fatalf("DROP DATABASE IF EXISTS: %v", err)
	}
}

func TestParseKitSQLCreateDatabaseAsOfTimestamp(t *testing.T) {
	statement, err := parseKitSQL(
		`CREATE DATABASE products_recovered FROM products AS OF TIMESTAMP '2026-08-27T14:30:00+07:00'`,
		kitSQLBindings{named: map[string]value.Value{}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if statement.kind != "create_database" || statement.createDatabase == nil {
		t.Fatalf("CREATE DATABASE plan = %#v", statement)
	}
	plan := statement.createDatabase
	if plan.name != "products_recovered" || plan.source != "products" {
		t.Fatalf("CREATE DATABASE names = %q from %q", plan.name, plan.source)
	}
	if plan.capability != "" {
		t.Fatalf("CREATE DATABASE recovery capability = %q", plan.capability)
	}
	want := time.Date(2026, 8, 27, 7, 30, 0, 0, time.UTC)
	if !plan.timestamp.Equal(want) {
		t.Fatalf("CREATE DATABASE timestamp = %s, want %s", plan.timestamp, want)
	}
	if _, err := parseKitSQL(
		`CREATE DATABASE bad FROM products AS OF TIMESTAMP 'not-a-time'`,
		kitSQLBindings{named: map[string]value.Value{}},
	); err == nil {
		t.Fatal("invalid recovery timestamp was accepted")
	}
	if _, err := parseKitSQL(
		`CREATE DATABASE bad.kitdb FROM products AS OF TIMESTAMP '2026-08-27T14:30:00+07:00'`,
		kitSQLBindings{named: map[string]value.Value{}},
	); err == nil {
		t.Fatal("physical .kitdb suffix was accepted as a logical database name")
	}
}

func TestParseKitSQLCreateEmptyDatabase(t *testing.T) {
	tests := []struct {
		name       string
		source     string
		capability string
	}{
		{name: "shop", source: `CREATE DATABASE shop`},
		{
			name:       "shop_explicit",
			source:     `CREATE DATABASE shop_explicit WITH CAPABILITY products`,
			capability: "products",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			statement, err := parseKitSQL(
				test.source,
				kitSQLBindings{named: map[string]value.Value{}},
			)
			if err != nil {
				t.Fatal(err)
			}
			if statement.kind != "create_database" || statement.createDatabase == nil {
				t.Fatalf("CREATE DATABASE plan = %#v", statement)
			}
			plan := statement.createDatabase
			if plan.name != test.name || plan.capability != test.capability ||
				plan.source != "" || !plan.timestamp.IsZero() {
				t.Fatalf("CREATE DATABASE plan = %#v", plan)
			}
		})
	}
	for _, source := range []string{
		`CREATE DATABASE bad.kitdb`,
		`CREATE DATABASE bad WITH OWNER products`,
		`CREATE DATABASE bad WITH CAPABILITY products.kitdb`,
	} {
		if _, err := parseKitSQL(
			source,
			kitSQLBindings{named: map[string]value.Value{}},
		); err == nil {
			t.Fatalf("unsupported CREATE DATABASE was accepted: %s", source)
		}
	}
}

func TestParseKitSQLDropDatabase(t *testing.T) {
	statement, err := parseKitSQL(
		`DROP DATABASE IF EXISTS "products_recovered"`,
		kitSQLBindings{named: map[string]value.Value{}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if statement.kind != "drop_database" || statement.dropDatabase == nil {
		t.Fatalf("DROP DATABASE plan = %#v", statement)
	}
	if !statement.dropDatabase.ifExists || statement.dropDatabase.name != "products_recovered" {
		t.Fatalf("DROP DATABASE plan = %#v", statement.dropDatabase)
	}
	if _, err := parseKitSQL(
		`DROP DATABASE products_recovered.kitdb`,
		kitSQLBindings{named: map[string]value.Value{}},
	); err == nil {
		t.Fatal("DROP DATABASE accepted a physical suffix")
	}
}

func TestParseKitSQLRenameDatabase(t *testing.T) {
	statement, err := parseKitSQL(
		`ALTER DATABASE shop RENAME TO warehouse`,
		kitSQLBindings{named: map[string]value.Value{}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if statement.kind != "alter_rename_database" || statement.renameDatabase == nil ||
		statement.renameDatabase.name != "shop" || statement.renameDatabase.newName != "warehouse" {
		t.Fatalf("ALTER DATABASE plan = %#v", statement)
	}
	for _, source := range []string{
		`ALTER DATABASE shop.kitdb RENAME TO warehouse`,
		`ALTER DATABASE shop RENAME warehouse`,
		`ALTER DATABASE shop RENAME TO warehouse.kitdb`,
	} {
		if _, err := parseKitSQL(
			source,
			kitSQLBindings{named: map[string]value.Value{}},
		); err == nil {
			t.Fatalf("invalid ALTER DATABASE was accepted: %s", source)
		}
	}
}
