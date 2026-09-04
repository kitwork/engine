package work

import (
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	kitdbengine "github.com/kitwork/engine/kitdb"
	"github.com/kitwork/engine/value"
	"github.com/lib/pq"
)

type kitDBNodeCatalogTestServer struct {
	tenant *Tenant
	addr   string
	cancel context.CancelFunc
	done   chan error
	once   sync.Once
}

func startKitDBNodeCatalogTestServer(t *testing.T, root string) *kitDBNodeCatalogTestServer {
	t.Helper()
	tenant := NewTenant(root, "localhost")
	if err := tenant.Run(); err != nil {
		tenant.Close()
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		tenant.Close()
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	server := &kitDBNodeCatalogTestServer{
		tenant: tenant,
		addr:   listener.Addr().String(),
		cancel: cancel,
		done:   make(chan error, 1),
	}
	go func() {
		server.done <- tenant.ServeKitDBPostgres(ctx, listener, KitDBPostgresOptions{
			MaintenanceDatabase: "kitdb",
			User:                "kitdb",
			MaxConnections:      8,
			IdleTimeout:         5 * time.Second,
			QueryTimeout:        15 * time.Second,
		})
	}()
	t.Cleanup(func() { server.close(t) })
	return server
}

func (server *kitDBNodeCatalogTestServer) close(t *testing.T) {
	t.Helper()
	server.once.Do(func() {
		server.cancel()
		select {
		case err := <-server.done:
			if err != nil {
				t.Errorf("ServeKitDBPostgres: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("ServeKitDBPostgres did not stop")
		}
		server.tenant.Close()
	})
}

func TestKitDBNodeCatalogEntryCodec(t *testing.T) {
	states := []kitDBNodeCatalogState{
		kitDBNodeCatalogCreating,
		kitDBNodeCatalogActive,
		kitDBNodeCatalogDropping,
	}
	for _, state := range states {
		state := state
		t.Run(state.String(), func(t *testing.T) {
			entry := newKitDBNodeCatalogEntry("products_recovered", "products.kitdb")
			entry.State = state
			if state != kitDBNodeCatalogCreating {
				entry.DatabaseID = "00112233445566778899aabbccddeeff"
			}
			encoded, err := encodeKitDBNodeCatalogEntry(entry)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := decodeKitDBNodeCatalogEntry(entry.Name, encoded)
			if err != nil {
				t.Fatal(err)
			}
			if decoded != entry {
				t.Fatalf("decoded entry = %#v, want %#v", decoded, entry)
			}
			encoded[len(encoded)-1] ^= 0xff
			if _, err := decodeKitDBNodeCatalogEntry(entry.Name, encoded); err == nil ||
				!strings.Contains(err.Error(), "checksum") {
				t.Fatalf("corrupt metadata error = %v", err)
			}
		})
	}
}

func TestKitDBNodeCatalogEntryCodecReadsVersionOne(t *testing.T) {
	entry := newKitDBNodeCatalogEntry("legacy", "products.kitdb")
	entry.StorageName = "legacy.kitdb"
	entry.State = kitDBNodeCatalogActive
	entry.DatabaseID = "00112233445566778899aabbccddeeff"
	encoded, err := encodeKitDBNodeCatalogEntry(entry)
	if err != nil {
		t.Fatal(err)
	}
	binary.LittleEndian.PutUint16(encoded[8:10], kitDBNodeCatalogVersionV1)
	checksumAt := len(encoded) - 4
	binary.LittleEndian.PutUint32(
		encoded[checksumAt:],
		crc32.Checksum(encoded[:checksumAt], kitDBNodeCatalogCRC),
	)
	decoded, err := decodeKitDBNodeCatalogEntry(entry.Name, encoded)
	if err != nil || decoded != entry {
		t.Fatalf("decode version-one entry = (%#v, %v)", decoded, err)
	}
}

func TestKitDBNodeCatalogStorageNameIsReserved(t *testing.T) {
	result := newDbProxy(nil, nil, "kitdb", value.New(kitDBNodeCatalogFile))
	if result.K != value.Invalid || !strings.Contains(fmt.Sprint(result.V), "reserved") {
		t.Fatalf("reserved node catalog declaration = %#v", result)
	}
}

func TestKitDBPostgresManagedDatabaseFollowsCapabilityRotation(t *testing.T) {
	tenant := NewTenant(t.TempDir(), "localhost")
	defer tenant.Close()
	_ = tenant.resolve()

	source := &dbProxy{
		tenant: tenant, engine: "kitdb", dbName: "source.kitdb", sourceDeclared: true,
	}
	target := &dbProxy{
		tenant: tenant, engine: "kitdb", dbName: "recovered.kitdb",
	}
	registerServe(source, "token-one", "readonly")
	registerServeFromCapability(target, "stale-token", "readwrite", source.dbName)
	t.Cleanup(func() {
		serveRegMu.Lock()
		delete(serveReg, serveDBKey(tenant, source.dbName))
		delete(serveReg, serveDBKey(tenant, target.dbName))
		serveRegMu.Unlock()
	})

	authenticator := &kitDBPostgresAuthenticator{tenant: tenant}
	assertTarget := func(token, access string) {
		t.Helper()
		databases, err := authenticator.databases()
		if err != nil {
			t.Fatal(err)
		}
		database, found := findKitDBPostgresDatabase(databases, "recovered")
		if !found {
			t.Fatalf("managed database is absent from %#v", databases)
		}
		if database.config.token != token || database.config.access != access {
			t.Fatalf(
				"managed capability = token:%q access:%q, want token:%q access:%q",
				database.config.token, database.config.access, token, access,
			)
		}
		_, remote, served := resolveServe(tenant, target.dbName)
		if !served || remote.token != token || remote.access != access {
			t.Fatalf(
				"remote managed capability = served:%v token:%q access:%q",
				served, remote.token, remote.access,
			)
		}
	}

	assertTarget("token-one", "readonly")
	registerServe(source, "token-two", "readwrite")
	assertTarget("token-two", "readwrite")

	databases, err := authenticator.databases()
	if err != nil {
		t.Fatal(err)
	}
	if len(authenticator.authorizedDatabases("token-one", databases)) != 0 {
		t.Fatal("rotated-out token still authorizes a KitDB database")
	}
	authorized := authenticator.authorizedDatabases("token-two", databases)
	if recovered, found := findKitDBPostgresDatabase(authorized, "recovered"); !found || recovered.config.access != "readwrite" {
		t.Fatalf("rotated token authorization = %#v", authorized)
	}

	serveRegMu.Lock()
	delete(serveReg, serveDBKey(tenant, source.dbName))
	serveRegMu.Unlock()
	if _, _, served := resolveServe(tenant, target.dbName); served {
		t.Fatal("managed database remained remotely exposed without its source capability")
	}
	databases, err = authenticator.databases()
	if err != nil {
		t.Fatal(err)
	}
	if _, found := findKitDBPostgresDatabase(databases, "recovered"); found {
		t.Fatal("managed database remained in PostgreSQL catalog without its source capability")
	}
}

func TestKitDBNodeCatalogCapabilityDependents(t *testing.T) {
	entries := map[string]kitDBNodeCatalogEntry{
		"second": {Name: "second", CapabilityStorage: "source.kitdb"},
		"other":  {Name: "other", CapabilityStorage: "other.kitdb"},
		"first":  {Name: "first", CapabilityStorage: "source.kitdb"},
	}
	dependents := kitDBNodeCatalogCapabilityDependents(entries, "source.kitdb")
	if strings.Join(dependents, ",") != "first,second" {
		t.Fatalf("capability dependents = %v", dependents)
	}
}

func TestKitDBPostgresNodeCatalogProtectsCapabilitySource(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	dataDirectory := filepath.Join(directory, ".data")
	if err := os.MkdirAll(dataDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	router := `import { router, database } from "kitwork";
const { kitdb } = database;
kitdb("authority.kitdb", {}, { token: "authority-secret", access: "readwrite" });
router.get(() => "ok");`
	if err := os.WriteFile(filepath.Join(directory, RouterFileName), []byte(router), 0o644); err != nil {
		t.Fatal(err)
	}

	authorityPath := filepath.Join(dataDirectory, "authority.kitdb")
	authority, err := kitdbengine.Open(authorityPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := authority.Close(); err != nil {
		t.Fatal(err)
	}
	target, err := kitdbengine.Open(filepath.Join(dataDirectory, "dependent.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	entry := newKitDBNodeCatalogEntry("dependent", "authority.kitdb")
	entry.State = kitDBNodeCatalogActive
	entry.DatabaseID = target.ID()
	if err := target.Close(); err != nil {
		t.Fatal(err)
	}
	catalog, err := kitdbengine.Open(filepath.Join(dataDirectory, kitDBNodeCatalogFile))
	if err != nil {
		t.Fatal(err)
	}
	if err := putKitDBNodeCatalogEntry(context.Background(), catalog, entry); err != nil {
		_ = catalog.Close()
		t.Fatal(err)
	}
	if err := catalog.Close(); err != nil {
		t.Fatal(err)
	}

	server := startKitDBNodeCatalogTestServer(t, root)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	maintenance := openKitDBPostgresDatabaseTestClient(
		t, server.addr, "kitdb", "authority-secret",
	)
	defer maintenance.Close()
	_, err = maintenance.ExecContext(ctx, `DROP DATABASE authority`)
	var postgresErr *pq.Error
	if !errors.As(err, &postgresErr) || postgresErr.Code != "2BP01" {
		t.Fatalf("dependent capability DROP error = %T %v", err, err)
	}
	if _, err := os.Stat(authorityPath); err != nil {
		t.Fatalf("capability source was removed despite dependent databases: %v", err)
	}
}

func TestKitDBNodeCatalogPersistsTransitions(t *testing.T) {
	path := t.TempDir() + "/node.kitdb"
	database, err := kitdbengine.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	entry := newKitDBNodeCatalogEntry("products_recovered", "products.kitdb")
	if err := putKitDBNodeCatalogEntry(ctx, database, entry); err != nil {
		t.Fatal(err)
	}
	entry.State = kitDBNodeCatalogActive
	entry.DatabaseID = database.ID()
	if err := putKitDBNodeCatalogEntry(ctx, database, entry); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	database, err = kitdbengine.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := loadKitDBNodeCatalog(database)
	if err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if stored := entries[entry.Name]; stored != entry {
		_ = database.Close()
		t.Fatalf("stored entry = %#v, want %#v", stored, entry)
	}
	if err := deleteKitDBNodeCatalogEntry(ctx, database, entry.Name); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	entries, err = loadKitDBNodeCatalog(database)
	if err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if len(entries) != 0 {
		_ = database.Close()
		t.Fatalf("entries after delete = %#v", entries)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestKitDBNodeCatalogRejectsUnknownKeys(t *testing.T) {
	database, err := kitdbengine.Open(t.TempDir() + "/node.kitdb")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	transaction, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := transaction.Put([]byte("unknown"), []byte("value")); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := loadKitDBNodeCatalog(database); err == nil ||
		!strings.Contains(err.Error(), "unknown key") {
		t.Fatalf("unknown key error = %v", err)
	}
}

func TestKitDBNodeCatalogRejectsSwappedDatabaseIdentity(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	dataDirectory := filepath.Join(directory, ".data")
	if err := os.MkdirAll(dataDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	router := `import { router, database } from "kitwork";
const { kitdb, struct, id } = database;
const products = struct({ id: id() });
const db = kitdb("products.kitdb", { products }, { token: "identity-secret", access: "readwrite" });
router.get(() => db.products.count());`
	if err := os.WriteFile(filepath.Join(directory, RouterFileName), []byte(router), 0o644); err != nil {
		t.Fatal(err)
	}
	targetPath := filepath.Join(dataDirectory, "swapped.kitdb")
	target, err := kitdbengine.Open(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	actualID := target.ID()
	if err := target.Close(); err != nil {
		t.Fatal(err)
	}
	entry := newKitDBNodeCatalogEntry("swapped", "products.kitdb")
	entry.State = kitDBNodeCatalogActive
	entry.DatabaseID = "00112233445566778899aabbccddeeff"
	catalog, err := kitdbengine.Open(filepath.Join(dataDirectory, kitDBNodeCatalogFile))
	if err != nil {
		t.Fatal(err)
	}
	if err := putKitDBNodeCatalogEntry(context.Background(), catalog, entry); err != nil {
		_ = catalog.Close()
		t.Fatal(err)
	}
	if err := catalog.Close(); err != nil {
		t.Fatal(err)
	}

	tenant := NewTenant(root, "localhost")
	if err := tenant.Run(); err != nil {
		tenant.Close()
		t.Fatal(err)
	}
	defer tenant.Close()
	authenticator := &kitDBPostgresAuthenticator{tenant: tenant}
	if err := authenticator.restoreKitDBNodeCatalog(context.Background()); err != nil {
		t.Fatal(err)
	}
	managed, err := kitDBForRequest(tenant, entry.StorageName, nil).database()
	if err == nil {
		managed.Release()
		t.Fatal("swapped database identity was accepted")
	}
	if !strings.Contains(err.Error(), "identity mismatch") || !strings.Contains(err.Error(), actualID) {
		t.Fatalf("identity mismatch error = %v", err)
	}
}

func TestKitDBNodeCatalogConflictNeverDropsSourceDatabase(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	dataDirectory := filepath.Join(directory, ".data")
	if err := os.MkdirAll(dataDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	router := `import { router, database } from "kitwork";
const { kitdb, struct, id } = database;
const products = struct({ id: id() });
const db = kitdb("products.kitdb", { products }, { token: "conflict-secret", access: "readwrite" });
kitdb("conflict.kitdb", {}, { token: "conflict-secret", access: "readwrite" });
router.get(() => db.products.count());`
	if err := os.WriteFile(filepath.Join(directory, RouterFileName), []byte(router), 0o644); err != nil {
		t.Fatal(err)
	}
	conflictPath := filepath.Join(dataDirectory, "conflict.kitdb")
	conflict, err := kitdbengine.Open(conflictPath)
	if err != nil {
		t.Fatal(err)
	}
	conflictID := conflict.ID()
	if err := conflict.Close(); err != nil {
		t.Fatal(err)
	}
	entry := newKitDBNodeCatalogEntry("conflict", "products.kitdb")
	entry.State = kitDBNodeCatalogDropping
	entry.DatabaseID = conflictID
	catalog, err := kitdbengine.Open(filepath.Join(dataDirectory, kitDBNodeCatalogFile))
	if err != nil {
		t.Fatal(err)
	}
	if err := putKitDBNodeCatalogEntry(context.Background(), catalog, entry); err != nil {
		_ = catalog.Close()
		t.Fatal(err)
	}
	if err := catalog.Close(); err != nil {
		t.Fatal(err)
	}

	tenant := NewTenant(root, "localhost")
	if err := tenant.Run(); err != nil {
		tenant.Close()
		t.Fatal(err)
	}
	defer tenant.Close()
	authenticator := &kitDBPostgresAuthenticator{tenant: tenant}
	err = authenticator.restoreKitDBNodeCatalog(context.Background())
	if err == nil || !strings.Contains(err.Error(), "conflicts with a source declaration") {
		t.Fatalf("source conflict error = %v", err)
	}
	if _, err := os.Stat(conflictPath); err != nil {
		t.Fatalf("source database was touched before conflict rejection: %v", err)
	}
}

func TestKitDBNodeCatalogReconcilesInterruptedLifecycle(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	dataDirectory := filepath.Join(directory, ".data")
	if err := os.MkdirAll(dataDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	router := `import { router, database } from "kitwork";
const { kitdb, struct, id, text } = database;
const products = struct({ id: id(), title: text().notNull() });
const db = kitdb("products.kitdb", { products }, { token: "catalog-secret", access: "readwrite", recovery: true });
router.get(() => db.products.count());`
	if err := os.WriteFile(filepath.Join(directory, RouterFileName), []byte(router), 0o644); err != nil {
		t.Fatal(err)
	}

	finishPath := filepath.Join(dataDirectory, "finish_create.kitdb")
	finishDatabase, err := kitdbengine.Open(finishPath)
	if err != nil {
		t.Fatal(err)
	}
	finishID := finishDatabase.ID()
	if err := finishDatabase.Close(); err != nil {
		t.Fatal(err)
	}
	dropPath := filepath.Join(dataDirectory, "finish_drop.kitdb")
	dropDatabase, err := kitdbengine.Open(dropPath)
	if err != nil {
		t.Fatal(err)
	}
	dropID := dropDatabase.ID()
	if err := dropDatabase.Close(); err != nil {
		t.Fatal(err)
	}
	catalog, err := kitdbengine.Open(filepath.Join(dataDirectory, kitDBNodeCatalogFile))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, entry := range []kitDBNodeCatalogEntry{
		newKitDBNodeCatalogEntry("missing_create", "products.kitdb"),
		newKitDBNodeCatalogEntry("finish_create", "products.kitdb"),
		{
			Name: "finish_drop", StorageName: "finish_drop.kitdb",
			CapabilityStorage: "products.kitdb", DatabaseID: dropID,
			State: kitDBNodeCatalogDropping, CreatedAt: time.Now().UTC().UnixNano(),
		},
	} {
		if err := putKitDBNodeCatalogEntry(ctx, catalog, entry); err != nil {
			_ = catalog.Close()
			t.Fatal(err)
		}
	}
	if err := catalog.Close(); err != nil {
		t.Fatal(err)
	}

	tenant := NewTenant(root, "localhost")
	if err := tenant.Run(); err != nil {
		tenant.Close()
		t.Fatal(err)
	}
	defer tenant.Close()
	authenticator := &kitDBPostgresAuthenticator{tenant: tenant}
	if err := authenticator.restoreKitDBNodeCatalog(ctx); err != nil {
		t.Fatal(err)
	}
	manager, err := kitDBManagerForTenant(tenant)
	if err != nil {
		t.Fatal(err)
	}
	manager.catalogMu.Lock()
	entries, exists, err := readKitDBNodeCatalog(ctx, tenant, manager)
	manager.catalogMu.Unlock()
	if err != nil || !exists {
		t.Fatalf("read reconciled catalog: exists=%v err=%v", exists, err)
	}
	if len(entries) != 1 {
		t.Fatalf("reconciled entries = %#v", entries)
	}
	finished := entries["finish_create"]
	if finished.State != kitDBNodeCatalogActive || finished.DatabaseID != finishID {
		t.Fatalf("finished create entry = %#v", finished)
	}
	if _, config, found := resolveServe(tenant, "finish_create.kitdb"); !found ||
		config.database == nil || config.database.databaseID != finishID {
		t.Fatalf("finished create serve = found:%v config:%#v", found, config)
	}
	if _, _, found := resolveServe(tenant, "missing_create.kitdb"); found {
		t.Fatal("missing creating database remained exposed")
	}
	for _, removed := range []string{dropPath, dropPath + ".wal", dropPath + ".lock", dropPath + ".history"} {
		if _, err := os.Lstat(removed); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("interrupted drop path %q error = %v", removed, err)
		}
	}
}

func TestKitDBPostgresCreateEmptyDatabaseLifecycle(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	router := `import { router, database } from "kitwork";
const { kitdb, struct, id, text } = database;
const products = struct({ id: id(), title: text().notNull() });
const source = kitdb("products.kitdb", { products }, { token: "create-secret", access: "readwrite" });
router.get(() => source.products.count());`
	if err := os.WriteFile(filepath.Join(directory, RouterFileName), []byte(router), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	first := startKitDBNodeCatalogTestServer(t, root)
	maintenance := openKitDBPostgresDatabaseTestClient(t, first.addr, "kitdb", "create-secret")
	if err := maintenance.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := maintenance.ExecContext(ctx, `CREATE DATABASE shop`); err != nil {
		t.Fatalf("CREATE DATABASE: %v", err)
	}
	if names := queryKitDBPostgresStrings(
		t,
		ctx,
		maintenance,
		`SELECT datname FROM pg_catalog.pg_database ORDER BY datname`,
	); !containsString(names, "shop") {
		t.Fatalf("created database is absent from pg_database: %v", names)
	}

	manager, err := kitDBManagerForTenant(first.tenant)
	if err != nil {
		t.Fatal(err)
	}
	manager.catalogMu.Lock()
	entries, exists, err := readKitDBNodeCatalog(ctx, first.tenant, manager)
	manager.catalogMu.Unlock()
	if err != nil || !exists {
		t.Fatalf("read node catalog: exists=%v err=%v", exists, err)
	}
	entry, found := entries["shop"]
	if !found || entry.State != kitDBNodeCatalogActive ||
		entry.CapabilityStorage != "products.kitdb" || entry.DatabaseID == "" {
		t.Fatalf("created node catalog entry = %#v", entry)
	}
	source, err := kitDBForRequest(first.tenant, "products.kitdb", nil).database()
	if err != nil {
		t.Fatal(err)
	}
	target, err := kitDBForRequest(first.tenant, "shop.kitdb", nil).database()
	if err != nil {
		source.Release()
		t.Fatal(err)
	}
	if source.database.ID() == target.database.ID() || target.database.ID() != entry.DatabaseID {
		t.Fatalf(
			"database identities source=%q target=%q catalog=%q",
			source.database.ID(),
			target.database.ID(),
			entry.DatabaseID,
		)
	}
	target.Release()
	source.Release()

	shop := openKitDBPostgresDatabaseTestClient(t, first.addr, "shop", "create-secret")
	if err := shop.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := shop.ExecContext(ctx, `
CREATE TABLE inventory (
  id TEXT PRIMARY KEY,
  sku TEXT NOT NULL UNIQUE,
  title TEXT NOT NULL,
  price INTEGER NOT NULL DEFAULT 0,
  status TEXT NOT NULL DEFAULT 'active'
)`); err != nil {
		t.Fatalf("CREATE TABLE in empty database: %v", err)
	}
	if _, err := shop.ExecContext(ctx, `
INSERT INTO inventory (id, sku, title, price)
VALUES ('item-1', 'SKU-1', 'First item', 125)`); err != nil {
		t.Fatalf("INSERT in empty database: %v", err)
	}
	var title string
	var price int64
	var status string
	if err := shop.QueryRowContext(ctx, `
SELECT title, price, status FROM inventory WHERE id = 'item-1'`).Scan(
		&title,
		&price,
		&status,
	); err != nil {
		t.Fatalf("SELECT in empty database: %v", err)
	}
	if title != "First item" || price != 125 || status != "active" {
		t.Fatalf("created database row = (%q, %d, %q)", title, price, status)
	}
	if err := shop.Close(); err != nil {
		t.Fatal(err)
	}
	if err := maintenance.Close(); err != nil {
		t.Fatal(err)
	}
	first.close(t)

	second := startKitDBNodeCatalogTestServer(t, root)
	shop = openKitDBPostgresDatabaseTestClient(t, second.addr, "shop", "create-secret")
	if err := shop.QueryRowContext(ctx, `
SELECT title, price, status FROM inventory WHERE id = 'item-1'`).Scan(
		&title,
		&price,
		&status,
	); err != nil {
		t.Fatalf("query created database after restart: %v", err)
	}
	if title != "First item" || price != 125 || status != "active" {
		t.Fatalf("restarted database row = (%q, %d, %q)", title, price, status)
	}
	if err := shop.Close(); err != nil {
		t.Fatal(err)
	}
	maintenance = openKitDBPostgresDatabaseTestClient(t, second.addr, "kitdb", "create-secret")
	if _, err := maintenance.ExecContext(ctx, `DROP DATABASE shop`); err != nil {
		t.Fatalf("DROP DATABASE: %v", err)
	}
	if err := maintenance.Close(); err != nil {
		t.Fatal(err)
	}
	second.close(t)

	third := startKitDBNodeCatalogTestServer(t, root)
	maintenance = openKitDBPostgresDatabaseTestClient(t, third.addr, "kitdb", "create-secret")
	names := queryKitDBPostgresStrings(
		t,
		ctx,
		maintenance,
		`SELECT datname FROM pg_catalog.pg_database ORDER BY datname`,
	)
	if containsString(names, "shop") {
		t.Fatalf("dropped database returned after restart: %v", names)
	}
	if err := maintenance.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestKitDBPostgresRenameDatabasePreservesStorageIdentityAndRestart(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	router := `import { router, database } from "kitwork";
const { kitdb } = database;
const source = kitdb("products.kitdb", {}, { token: "rename-secret", access: "readwrite" });
router.get(() => 1);`
	if err := os.WriteFile(filepath.Join(directory, RouterFileName), []byte(router), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	first := startKitDBNodeCatalogTestServer(t, root)
	maintenance := openKitDBPostgresDatabaseTestClient(t, first.addr, "kitdb", "rename-secret")
	if err := maintenance.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := maintenance.ExecContext(ctx, `CREATE DATABASE shop`); err != nil {
		t.Fatal(err)
	}
	observer := openKitDBPostgresDatabaseTestClient(t, first.addr, "kitdb", "rename-secret")
	if err := observer.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	shop := openKitDBPostgresDatabaseTestClient(t, first.addr, "shop", "rename-secret")
	if err := shop.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := shop.ExecContext(ctx, `CREATE TABLE inventory (id TEXT PRIMARY KEY, title TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := shop.ExecContext(ctx, `INSERT INTO inventory (id, title) VALUES ('one', 'Preserved')`); err != nil {
		t.Fatal(err)
	}
	if _, err := maintenance.ExecContext(ctx, `ALTER DATABASE shop RENAME TO warehouse`); err == nil {
		t.Fatal("rename succeeded while the source database had an active session")
	} else {
		var postgresErr *pq.Error
		if !errors.As(err, &postgresErr) || postgresErr.Code != "55006" {
			t.Fatalf("busy rename error = %T %v", err, err)
		}
	}
	if err := shop.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := maintenance.ExecContext(ctx, `ALTER DATABASE products RENAME TO source_renamed`); err == nil {
		t.Fatal("source-declared database rename was accepted")
	} else {
		var postgresErr *pq.Error
		if !errors.As(err, &postgresErr) || postgresErr.Code != "0A000" {
			t.Fatalf("source rename error = %T %v", err, err)
		}
	}
	if _, err := maintenance.ExecContext(ctx, `ALTER DATABASE shop RENAME TO warehouse`); err != nil {
		t.Fatalf("ALTER DATABASE: %v", err)
	}
	for _, client := range []*sql.DB{maintenance, observer} {
		names := queryKitDBPostgresStrings(
			t, ctx, client, `SELECT datname FROM pg_catalog.pg_database ORDER BY datname`,
		)
		if containsString(names, "shop") || !containsString(names, "warehouse") {
			t.Fatalf("renamed pg_database snapshot = %v", names)
		}
	}
	oldName := openKitDBPostgresDatabaseTestClient(t, first.addr, "shop", "rename-secret")
	if err := oldName.PingContext(ctx); err == nil {
		oldName.Close()
		t.Fatal("old logical database name remained connectable")
	}
	oldName.Close()
	warehouse := openKitDBPostgresDatabaseTestClient(t, first.addr, "warehouse", "rename-secret")
	var title string
	if err := warehouse.QueryRowContext(ctx, `SELECT title FROM inventory WHERE id = 'one'`).Scan(&title); err != nil {
		t.Fatal(err)
	}
	if title != "Preserved" {
		t.Fatalf("renamed database title = %q", title)
	}
	if err := warehouse.Close(); err != nil {
		t.Fatal(err)
	}

	manager, err := kitDBManagerForTenant(first.tenant)
	if err != nil {
		t.Fatal(err)
	}
	manager.catalogMu.Lock()
	entries, exists, err := readKitDBNodeCatalog(ctx, first.tenant, manager)
	manager.catalogMu.Unlock()
	if err != nil || !exists || len(entries) != 1 {
		t.Fatalf("renamed catalog = exists:%t entries:%#v err:%v", exists, entries, err)
	}
	entry, found := entries["warehouse"]
	if !found || entry.StorageName != "shop.kitdb" || entry.State != kitDBNodeCatalogActive {
		t.Fatalf("renamed catalog entry = %#v", entry)
	}
	physical := filepath.Join(directory, ".data", "shop.kitdb")
	if _, err := os.Stat(physical); err != nil {
		t.Fatalf("stable physical storage: %v", err)
	}
	if _, err := os.Stat(filepath.Join(directory, ".data", "warehouse.kitdb")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unexpected renamed physical file: %v", err)
	}
	physicalDatabase, err := kitDBForRequest(first.tenant, "shop.kitdb", nil).database()
	if err != nil {
		t.Fatal(err)
	}
	if physicalDatabase.database.ID() != entry.DatabaseID {
		physicalDatabase.Release()
		t.Fatalf(
			"physical identity = %q, want %q",
			physicalDatabase.database.ID(),
			entry.DatabaseID,
		)
	}
	physicalDatabase.Release()
	if err := maintenance.Close(); err != nil {
		t.Fatal(err)
	}
	if err := observer.Close(); err != nil {
		t.Fatal(err)
	}
	first.close(t)

	second := startKitDBNodeCatalogTestServer(t, root)
	warehouse = openKitDBPostgresDatabaseTestClient(t, second.addr, "warehouse", "rename-secret")
	if err := warehouse.QueryRowContext(ctx, `SELECT title FROM inventory WHERE id = 'one'`).Scan(&title); err != nil {
		t.Fatalf("query renamed database after restart: %v", err)
	}
	if title != "Preserved" {
		t.Fatalf("restarted renamed title = %q", title)
	}
	if err := warehouse.Close(); err != nil {
		t.Fatal(err)
	}
	maintenance = openKitDBPostgresDatabaseTestClient(t, second.addr, "kitdb", "rename-secret")
	if _, err := maintenance.ExecContext(ctx, `DROP DATABASE warehouse`); err != nil {
		t.Fatalf("drop renamed database: %v", err)
	}
	if err := maintenance.Close(); err != nil {
		t.Fatal(err)
	}
	second.close(t)
	if _, err := os.Stat(physical); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("renamed database physical storage after drop = %v", err)
	}
}

func TestKitDBPostgresCreateEmptyDatabaseCapabilitySelection(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	router := `import { router, database } from "kitwork";
const { kitdb } = database;
const products = kitdb("products.kitdb", {}, { token: "shared-secret", access: "readwrite" });
const analytics = kitdb("analytics.kitdb", {}, { token: "shared-secret", access: "readwrite" });
router.get(() => 1);`
	if err := os.WriteFile(filepath.Join(directory, RouterFileName), []byte(router), 0o644); err != nil {
		t.Fatal(err)
	}

	server := startKitDBNodeCatalogTestServer(t, root)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	maintenance := openKitDBPostgresDatabaseTestClient(t, server.addr, "kitdb", "shared-secret")
	defer maintenance.Close()
	if _, err := maintenance.ExecContext(ctx, `CREATE DATABASE ambiguous`); err == nil {
		t.Fatal("ambiguous implicit capability was accepted")
	} else {
		var postgresErr *pq.Error
		if !errors.As(err, &postgresErr) || postgresErr.Code != "42725" ||
			!strings.Contains(postgresErr.Message, "WITH CAPABILITY") {
			t.Fatalf("ambiguous capability error = %T %v", err, err)
		}
	}
	if _, err := maintenance.ExecContext(
		ctx,
		`CREATE DATABASE selected WITH CAPABILITY products`,
	); err != nil {
		t.Fatalf("explicit CREATE DATABASE capability: %v", err)
	}
	if _, err := maintenance.ExecContext(
		ctx,
		`CREATE DATABASE child WITH CAPABILITY selected`,
	); err == nil {
		t.Fatal("SQL-managed database was accepted as a capability authority")
	} else {
		var postgresErr *pq.Error
		if !errors.As(err, &postgresErr) || postgresErr.Code != "42501" {
			t.Fatalf("managed capability error = %T %v", err, err)
		}
	}
	manager, err := kitDBManagerForTenant(server.tenant)
	if err != nil {
		t.Fatal(err)
	}
	manager.catalogMu.Lock()
	entries, _, err := readKitDBNodeCatalog(ctx, server.tenant, manager)
	manager.catalogMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries["selected"].CapabilityStorage != "products.kitdb" {
		t.Fatalf("explicit capability catalog = %#v", entries)
	}
}

func TestKitDBPostgresCreateEmptyDatabaseRejectsReadOnlyAuthority(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	router := `import { router, database } from "kitwork";
const { kitdb } = database;
const source = kitdb("source.kitdb", {}, { token: "readonly-secret", access: "readonly" });
router.get(() => 1);`
	if err := os.WriteFile(filepath.Join(directory, RouterFileName), []byte(router), 0o644); err != nil {
		t.Fatal(err)
	}

	server := startKitDBNodeCatalogTestServer(t, root)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	maintenance := openKitDBPostgresDatabaseTestClient(t, server.addr, "kitdb", "readonly-secret")
	defer maintenance.Close()
	if _, err := maintenance.ExecContext(ctx, `CREATE DATABASE denied`); err == nil {
		t.Fatal("read-only authority created a database")
	} else {
		var postgresErr *pq.Error
		if !errors.As(err, &postgresErr) || postgresErr.Code != "42501" {
			t.Fatalf("read-only authority error = %T %v", err, err)
		}
	}
}

func TestKitDBPostgresNodeCatalogSurvivesRestart(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	router := `import { router, database } from "kitwork";
const { kitdb, struct, id, text } = database;
const products = struct({ id: id(), title: text().notNull() });
const db = kitdb("products.kitdb", { products }, { token: "restart-secret", access: "readwrite", recovery: true });
router.get(() => db.products.count());`
	if err := os.WriteFile(filepath.Join(directory, RouterFileName), []byte(router), 0o644); err != nil {
		t.Fatal(err)
	}

	first := startKitDBNodeCatalogTestServer(t, root)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	source := openKitDBPostgresDatabaseTestClient(t, first.addr, "products", "restart-secret")
	if err := source.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := source.ExecContext(ctx, `INSERT INTO products (id, title) VALUES ('product-1', 'Before restart')`); err != nil {
		t.Fatal(err)
	}
	maintenance := openKitDBPostgresDatabaseTestClient(t, first.addr, "kitdb", "restart-secret")
	if err := maintenance.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	command := fmt.Sprintf(
		"CREATE DATABASE restart_recovered FROM products AS OF TIMESTAMP '%s'",
		time.Now().UTC().Add(time.Second).Format(time.RFC3339Nano),
	)
	if _, err := maintenance.ExecContext(ctx, command); err != nil {
		t.Fatal(err)
	}
	recovered := openKitDBPostgresDatabaseTestClient(t, first.addr, "restart_recovered", "restart-secret")
	var title string
	if err := recovered.QueryRowContext(ctx, `SELECT title FROM products WHERE id = 'product-1'`).Scan(&title); err != nil {
		t.Fatal(err)
	}
	if title != "Before restart" {
		t.Fatalf("recovered title = %q", title)
	}
	_ = recovered.Close()
	_ = maintenance.Close()
	_ = source.Close()
	first.close(t)

	second := startKitDBNodeCatalogTestServer(t, root)
	maintenance = openKitDBPostgresDatabaseTestClient(t, second.addr, "kitdb", "restart-secret")
	if err := maintenance.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	names := queryKitDBPostgresStrings(t, ctx, maintenance,
		`SELECT datname FROM pg_catalog.pg_database ORDER BY datname`)
	if !containsString(names, "restart_recovered") {
		t.Fatalf("restart catalog databases = %v", names)
	}
	recovered = openKitDBPostgresDatabaseTestClient(t, second.addr, "restart_recovered", "restart-secret")
	if err := recovered.QueryRowContext(ctx, `SELECT title FROM products WHERE id = 'product-1'`).Scan(&title); err != nil {
		t.Fatal(err)
	}
	if err := recovered.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := maintenance.ExecContext(ctx, `DROP DATABASE restart_recovered`); err != nil {
		t.Fatal(err)
	}
	if err := maintenance.Close(); err != nil {
		t.Fatal(err)
	}
	second.close(t)

	targetPath := filepath.Join(directory, ".data", "restart_recovered.kitdb")
	for _, removed := range []string{targetPath, targetPath + ".wal", targetPath + ".lock", targetPath + ".history"} {
		if _, err := os.Lstat(removed); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("dropped restart database path %q error = %v", removed, err)
		}
	}
	third := startKitDBNodeCatalogTestServer(t, root)
	maintenance = openKitDBPostgresDatabaseTestClient(t, third.addr, "kitdb", "restart-secret")
	if err := maintenance.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	names = queryKitDBPostgresStrings(t, ctx, maintenance,
		`SELECT datname FROM pg_catalog.pg_database ORDER BY datname`)
	if containsString(names, "restart_recovered") {
		t.Fatalf("dropped database returned after restart: %v", names)
	}
	missing := openKitDBPostgresDatabaseTestClient(t, third.addr, "restart_recovered", "restart-secret")
	if err := missing.PingContext(ctx); err == nil {
		_ = missing.Close()
		t.Fatal("dropped database accepted a connection after restart")
	}
	_ = missing.Close()
	_ = maintenance.Close()
}
