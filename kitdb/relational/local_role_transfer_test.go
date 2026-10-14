package relational

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	kitdb "github.com/kitwork/engine/kitdb"
	"github.com/kitwork/engine/kitdb/managed"
	"github.com/lib/pq"
)

func portableRoleListener(t *testing.T, node *PostgresNode) (string, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- node.ServePostgres(ctx, ln, PostgresServerOptions{}) }()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Error(err)
				}
			case <-time.After(5 * time.Second):
				t.Error("portable listener drain timeout")
			}
		})
	}
	t.Cleanup(stop)
	return ln.Addr().String(), stop
}

func TestLocalRoleTransferRenamedRootPGWire(t *testing.T) {
	for _, name := range []string{"shop", "archive"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			source := sessionTestNode(t, 16)
			admin := setupLocalRoleAdmin(t, source)
			defer admin.Close()
			for _, q := range []string{"CREATE TABLE payments(id BIGINT PRIMARY KEY)", "CREATE ROLE editor", "GRANT SELECT ON TABLE articles TO ROLE editor", "GRANT INSERT ON TABLE articles TO ROLE editor", "GRANT UPDATE ON TABLE articles TO ROLE editor", "GRANT DELETE ON TABLE articles TO ROLE editor", "GRANT ROLE editor ON DATABASE shop TO reader"} {
				if _, err := admin.ExecContext(ctx, q); err != nil {
					t.Fatalf("%s: %v", q, err)
				}
			}
			var roleID, tableID string
			if err := admin.QueryRowContext(ctx, "SELECT role_id,table_id FROM kitdb_catalog.table_grants WHERE role_name='editor'").Scan(&roleID, &tableID); err != nil {
				t.Fatal(err)
			}
			source.mu.Lock()
			var owner *Engine
			for _, entry := range source.engines {
				if entry.database.name == "shop" {
					owner = entry.engine
				}
			}
			source.mu.Unlock()
			if owner == nil {
				t.Fatal("source owner missing")
			}
			path := t.TempDir()
			if _, err := managed.Init(path); err != nil {
				t.Fatal(err)
			}
			for _, folder := range []string{name, "duplicate", "corrupt"} {
				if err := os.Mkdir(filepath.Join(path, folder), 0700); err != nil {
					t.Fatal(err)
				}
			}
			anchor, err := owner.database.CreateBackupAnchor(ctx, filepath.Join(t.TempDir(), "shop.kitdb"))
			if err != nil {
				t.Fatal(err)
			}
			for _, folder := range []string{name, "duplicate"} {
				if restored, err := kitdb.RestoreToTransaction(ctx, anchor.Path, "", filepath.Join(path, folder, "data.kitdb"), anchor.Transaction); err != nil || restored.DatabaseID != anchor.DatabaseID {
					t.Fatal(err)
				}
			}
			if _, err = kitdb.RestoreToTransaction(ctx, anchor.Path, "", filepath.Join(path, name, "data.kitdb"), anchor.Transaction); err == nil {
				t.Fatal("restore overwrote existing database")
			}
			// Corrupt only an isolated fixture, never source data or an attached database.
			if err := os.WriteFile(filepath.Join(path, "corrupt", "data.kitdb"), make([]byte, 4096), 0600); err != nil {
				t.Fatal(err)
			}
			root, err := managed.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			attached, err := root.Attach(name)
			if err != nil {
				t.Fatal(err)
			}
			if attached.Name != name || attached.Directory != name || attached.DatabaseID != owner.database.ID() {
				t.Fatalf("transfer changed identity: %+v", attached)
			}
			again, err := root.Attach(name)
			if err != nil || again != attached {
				t.Fatalf("attach retry: %+v %v", again, err)
			}
			const destinationPassword = "portable-destination-password"
			if err = root.EnableAuth("admin", destinationPassword); err != nil {
				t.Fatal(err)
			}
			if err = root.SetUser(managed.UserInfo{Name: "reader", Grants: []managed.DatabaseGrant{{Database: name, Permissions: []string{"connect"}}}}, destinationPassword); err != nil {
				t.Fatal(err)
			}
			beforeCatalog, beforeUsers := root.Catalog(), root.Users()
			unchanged := func() {
				t.Helper()
				if !reflect.DeepEqual(beforeCatalog, root.Catalog()) || !reflect.DeepEqual(beforeUsers, root.Users()) {
					t.Fatal("failed attach changed catalog or authority")
				}
			}
			for _, bad := range []string{"missing", "../escape", ".catalog", "duplicate", "corrupt"} {
				if _, err = root.Attach(bad); err == nil {
					t.Fatalf("attached %s", bad)
				}
				unchanged()
			}
			busy, err := Open(filepath.Join(path, name, "data.kitdb"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err = root.Attach(name); err == nil {
				busy.Close()
				t.Fatal("attached busy database")
			}
			unchanged()
			if err = busy.Close(); err != nil {
				t.Fatal(err)
			}
			if err = root.Close(); err != nil {
				t.Fatal(err)
			}
			root, err = managed.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(beforeCatalog, root.Catalog()) || !reflect.DeepEqual(beforeUsers, root.Users()) {
				t.Fatal("failed attach affected recovered catalog")
			}
			if err = root.Close(); err != nil {
				t.Fatal(err)
			}
			dest, err := OpenPostgresNode(PostgresNodeOptions{Root: path})
			if err != nil {
				t.Fatal(err)
			}
			defer dest.Close()
			address, stop := portableRoleListener(t, dest)
			open := func(address, user, password, database string) *sql.DB {
				t.Helper()
				// Authentication is intentionally expensive, especially under -race.
				// Keep admission bounded without treating a 3s KDF budget as a DB fault.
				u := url.URL{Scheme: "postgres", Host: address, Path: "/" + database, User: url.UserPassword(user, password), RawQuery: "sslmode=disable&connect_timeout=15"}
				db, err := sql.Open("postgres", u.String())
				if err != nil {
					t.Fatal(err)
				}
				db.SetMaxOpenConns(1)
				t.Cleanup(func() { db.Close() })
				return db
			}
			expectCode := func(err error, code string) {
				t.Helper()
				var wire *pq.Error
				if !errors.As(err, &wire) || string(wire.Code) != code {
					t.Fatalf("expected %s, got %v", code, err)
				}
			}
			wrong := open(address, "reader", "session-test-password", name)
			expectCode(wrong.PingContext(ctx), "28P01")
			wrong.Close()
			reader := open(address, "reader", destinationPassword, name)
			var n int
			err = reader.QueryRowContext(ctx, "SELECT COUNT(*) FROM articles").Scan(&n)
			expectCode(err, "42501")
			_, err = reader.ExecContext(ctx, "INSERT INTO articles VALUES(2,'unauthorized')")
			expectCode(err, "42501")
			if err = reader.QueryRowContext(ctx, "SELECT COUNT(*) FROM kitdb_catalog.roles").Scan(&n); err != nil || n != 0 {
				t.Fatal("unmapped user saw role", n, err)
			}
			operator := open(address, "admin", destinationPassword, name)
			var copiedRole, copiedTable string
			var a, b, c, d bool
			if err = operator.QueryRowContext(ctx, "SELECT role_id,table_id,can_select,can_insert,can_update,can_delete FROM kitdb_catalog.table_grants WHERE role_name='editor'").Scan(&copiedRole, &copiedTable, &a, &b, &c, &d); err != nil || copiedRole != roleID || copiedTable != tableID || !a || !b || !c || !d {
				t.Fatalf("copied ACL: %s %s %v %v %v %v %v", copiedRole, copiedTable, a, b, c, d, err)
			}
			if name != "shop" {
				_, err = operator.ExecContext(ctx, "GRANT ROLE editor ON DATABASE shop TO reader")
				expectCode(err, "42501")
			}
			if _, err = operator.ExecContext(ctx, "GRANT ROLE editor ON DATABASE "+name+" TO reader"); err != nil {
				t.Fatal(err)
			}
			// Mapping changes do not silently upgrade an already authenticated connection.
			err = reader.QueryRowContext(ctx, "SELECT COUNT(*) FROM articles").Scan(&n)
			expectCode(err, "42501")
			reader.Close()
			reader = open(address, "reader", destinationPassword, name)
			var title string
			if err = reader.QueryRowContext(ctx, "SELECT title FROM articles WHERE id=1").Scan(&title); err != nil || title != "public" {
				t.Fatal("copied data", title, err)
			}
			for _, q := range []string{"INSERT INTO articles VALUES(2,'draft')", "UPDATE articles SET title='updated' WHERE id=2", "DELETE FROM articles WHERE id=2", "INSERT INTO articles VALUES(2,'persisted')"} {
				if _, err = reader.ExecContext(ctx, q); err != nil {
					t.Fatalf("%s: %v", q, err)
				}
			}
			err = reader.QueryRowContext(ctx, "SELECT COUNT(*) FROM payments").Scan(&n)
			expectCode(err, "42501")
			if err = admin.QueryRowContext(ctx, "SELECT COUNT(*) FROM articles").Scan(&n); err != nil || n != 1 {
				t.Fatal("destination writes changed source", n, err)
			}
			reader.Close()
			operator.Close()
			stop()
			if err = dest.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := OpenPostgresNode(PostgresNodeOptions{Root: path})
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			address, stop = portableRoleListener(t, reopened)
			reader = open(address, "reader", destinationPassword, name)
			if err = reader.QueryRowContext(ctx, "SELECT title FROM articles WHERE id=2").Scan(&title); err != nil || title != "persisted" {
				t.Fatal("restart data", title, err)
			}
			for _, q := range []string{"UPDATE articles SET title='reopened' WHERE id=2", "DELETE FROM articles WHERE id=2", "INSERT INTO articles VALUES(2,'reopened')"} {
				if _, err = reader.ExecContext(ctx, q); err != nil {
					t.Fatalf("restart %s: %v", q, err)
				}
			}
			if err = reader.QueryRowContext(ctx, "SELECT role_id FROM kitdb_catalog.roles WHERE role_name='editor'").Scan(&copiedRole); err != nil || copiedRole != roleID {
				t.Fatal("restart role ID", copiedRole, err)
			}
			reader.Close()
			stop()
		})
	}
}

func TestLocalRoleOfflineFolderRename(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	node := sessionTestNode(t, 16)
	admin := setupLocalRoleAdmin(t, node)
	for _, q := range []string{"CREATE ROLE editor", "GRANT SELECT ON TABLE articles TO ROLE editor", "GRANT INSERT ON TABLE articles TO ROLE editor", "GRANT UPDATE ON TABLE articles TO ROLE editor", "GRANT DELETE ON TABLE articles TO ROLE editor", "GRANT ROLE editor ON DATABASE shop TO reader"} {
		if _, err := admin.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	var originalID string
	if err := admin.QueryRowContext(ctx, "SELECT role_id FROM kitdb_catalog.roles WHERE role_name='editor'").Scan(&originalID); err != nil {
		t.Fatal(err)
	}
	admin.Close()
	if err := node.Close(); err != nil {
		t.Fatal(err)
	}
	root, err := managed.Open(node.root)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	detached, err := root.Detach("shop")
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range root.Users() {
		if u.Name == "reader" {
			if len(u.Grants) != 0 {
				t.Fatal("detach retained old grants/mappings")
			}
		}
	}
	oldPath, newPath := filepath.Join(node.root, "shop"), filepath.Join(node.root, "archive")
	// Both names are fixed children of this test's isolated root; no external path
	// or user database can be selected by this offline rename fixture.
	if err = os.Rename(oldPath, newPath); err != nil {
		t.Fatal(err)
	}
	attached, err := root.Attach("archive")
	if err != nil || attached.DatabaseID != detached.DatabaseID {
		t.Fatal("rename identity", attached, err)
	}
	if err = root.Close(); err != nil {
		t.Fatal(err)
	}
	fresh, err := OpenPostgresNode(PostgresNodeOptions{Root: node.root})
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	if db, err := fresh.OpenAuthenticatedDatabase(ctx, "reader", "session-test-password", "archive"); !errors.Is(err, managed.ErrPermission) {
		if db != nil {
			db.Close()
		}
		t.Fatal("renamed database inherited old authority", err)
	}
	operator, err := fresh.OpenAuthenticatedDatabase(ctx, "admin", "session-test-password", "archive")
	if err != nil {
		t.Fatal(err)
	}
	defer operator.Close()
	for _, q := range []string{"GRANT CONNECT ON DATABASE archive TO reader", "GRANT ROLE editor ON DATABASE archive TO reader"} {
		if _, err = operator.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	reader, err := fresh.OpenAuthenticatedDatabase(ctx, "reader", "session-test-password", "archive")
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	var recoveredID string
	if err = reader.QueryRowContext(ctx, "SELECT role_id FROM kitdb_catalog.roles WHERE role_name='editor'").Scan(&recoveredID); err != nil || recoveredID != originalID {
		t.Fatal("rename role ID", recoveredID, err)
	}
	for _, q := range []string{"INSERT INTO articles VALUES(2,'renamed')", "UPDATE articles SET title='updated' WHERE id=2", "DELETE FROM articles WHERE id=2"} {
		if _, err = reader.ExecContext(ctx, q); err != nil {
			t.Fatal(q, err)
		}
	}
	var title string
	if err = reader.QueryRowContext(ctx, "SELECT title FROM articles WHERE id=1").Scan(&title); err != nil || title != "public" {
		t.Fatal("rename data", title, err)
	}
}
