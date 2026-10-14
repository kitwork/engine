package relational

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/kitwork/engine/kitdb/managed"
	"github.com/lib/pq"
)

func authorizationState(err error) string {
	var wire *pq.Error
	if errors.As(err, &wire) {
		return string(wire.Code)
	}
	return postgresNodeWireSQLState(err)
}

func TestAuthorizationLifecycleWriteCommitAndKill(t *testing.T) {
	for _, adapter := range []string{"native", "pg"} {
		for _, change := range []string{"table-revoke", "mapping-revoke", "disable", "drop", "kill"} {
			t.Run(adapter+"/"+change, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
				defer cancel()
				node, admin, _ := roleCatalogFixture(t)
				if _, err := admin.ExecContext(ctx, "GRANT INSERT ON TABLE articles TO ROLE article_reader"); err != nil {
					t.Fatal(err)
				}
				var victim *sql.DB
				var err error
				if adapter == "native" {
					victim, err = node.OpenAuthenticatedDatabase(ctx, "reader", "session-test-password", "shop")
				} else {
					address, stop := portableRoleListener(t, node)
					defer stop()
					u := url.URL{Scheme: "postgres", Host: address, Path: "/shop", User: url.UserPassword("reader", "session-test-password"), RawQuery: "sslmode=disable&connect_timeout=15"}
					victim, err = sql.Open("postgres", u.String())
				}
				if err != nil {
					t.Fatal(err)
				}
				defer victim.Close()
				victim.SetMaxOpenConns(1)
				conn, err := victim.Conn(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				tx, err := conn.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				if _, err = tx.ExecContext(ctx, "INSERT INTO articles VALUES(2,'uncommitted')"); err != nil {
					t.Fatal(err)
				}
				var id int64
				if err = tx.QueryRowContext(ctx, "SELECT session_id FROM kitdb_catalog.sessions WHERE transaction_state='transaction'").Scan(&id); err != nil {
					t.Fatal(err)
				}
				commands := map[string]string{"table-revoke": "REVOKE INSERT ON TABLE articles FROM ROLE article_reader", "mapping-revoke": "REVOKE ROLE article_reader ON DATABASE shop FROM reader", "disable": "ALTER USER reader NOLOGIN", "drop": "DROP USER reader", "kill": fmt.Sprintf("KILL SESSION %d", id)}
				if _, err = admin.ExecContext(ctx, commands[change]); err != nil {
					t.Fatal(err)
				}
				if change == "kill" {
					waitSessionGone(t, node, id)
					if err = tx.Commit(); err == nil {
						t.Fatal("terminated transaction committed")
					}
				} else {
					if err = tx.Commit(); err != nil {
						t.Fatal("captured policy commit", err)
					}
					if change == "table-revoke" {
						if _, err = conn.ExecContext(ctx, "INSERT INTO articles VALUES(3,'fresh')"); authorizationState(err) != "42501" {
							t.Fatal("fresh ACL did not revoke", err)
						}
					} else {
						// Existing data policy deliberately survives root changes; KILL is explicit.
						if _, err = conn.ExecContext(ctx, "INSERT INTO articles VALUES(3,'captured')"); err != nil {
							t.Fatal("captured data policy", err)
						}
					}
				}
				var n int
				want := 1
				if change == "kill" {
					want = 0
				}
				if err = admin.QueryRowContext(ctx, "SELECT COUNT(*) FROM articles WHERE id=2").Scan(&n); err != nil || n != want {
					t.Fatal("commit/rollback outcome", n, err)
				}
				if change == "disable" || change == "drop" {
					if fresh, err := node.OpenAuthenticatedDatabase(ctx, "reader", "session-test-password", "shop"); err == nil {
						fresh.Close()
						t.Fatal("revoked login admitted")
					}
				}
			})
		}
	}
}

func TestAuthorizationLifecycleRoleRecreation(t *testing.T) {
	for _, adapter := range []string{"native", "pg"} {
		t.Run(adapter, func(t *testing.T) { testAuthorizationRoleRecreation(t, adapter) })
	}
}

func authorizationClientFactory(t *testing.T, node *PostgresNode, adapter string) func(string) *sql.DB {
	t.Helper()
	address := ""
	if adapter == "pg" {
		address, _ = portableRoleListener(t, node)
	}
	return func(user string) *sql.DB {
		t.Helper()
		var db *sql.DB
		var err error
		if adapter == "native" {
			db, err = node.OpenAuthenticatedDatabase(context.Background(), user, "session-test-password", "shop")
		} else {
			u := url.URL{Scheme: "postgres", Host: address, Path: "/shop", User: url.UserPassword(user, "session-test-password"), RawQuery: "sslmode=disable&connect_timeout=15"}
			db, err = sql.Open("postgres", u.String())
		}
		if err != nil {
			t.Fatal(err)
		}
		db.SetMaxOpenConns(1)
		t.Cleanup(func() { db.Close() })
		return db
	}
}

func testAuthorizationRoleRecreation(t *testing.T, adapter string) {
	ctx := context.Background()
	node, admin, _ := roleCatalogFixture(t)
	open := authorizationClientFactory(t, node, adapter)
	old := open("reader")
	if _, err := admin.Exec("GRANT INSERT ON TABLE articles TO ROLE article_reader"); err != nil {
		t.Fatal(err)
	}
	var before, after string
	if err := old.QueryRow("SELECT role_id FROM kitdb_catalog.roles WHERE role_name='article_reader'").Scan(&before); err != nil {
		t.Fatal(err)
	}
	tx, err := old.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, q := range []string{"REVOKE ROLE article_reader ON DATABASE shop FROM reader", "DROP ROLE article_reader", "CREATE ROLE article_reader", "GRANT SELECT ON TABLE payments TO ROLE article_reader", "GRANT INSERT ON TABLE payments TO ROLE article_reader", "GRANT ROLE article_reader ON DATABASE shop TO reader"} {
		if _, err = admin.Exec(q); err != nil {
			t.Fatal(q, err)
		}
	}
	if _, err = tx.Exec("INSERT INTO articles VALUES(2,'old-snapshot')"); err != nil {
		t.Fatal("pinned role identity", err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal("pinned role commit", err)
	}
	if _, err = old.Exec("INSERT INTO payments VALUES(2)"); authorizationState(err) != "42501" {
		t.Fatal("old role gained recreated writes", err)
	}
	fresh := open("reader")
	defer fresh.Close()
	if err = fresh.QueryRow("SELECT role_id FROM kitdb_catalog.roles WHERE role_name='article_reader'").Scan(&after); err != nil || after == before {
		t.Fatal("reused role identity", after, err)
	}
	if _, err = fresh.Exec("INSERT INTO payments VALUES(2)"); err != nil {
		t.Fatal("fresh role write", err)
	}
}

func TestAuthorizationLifecycleUserRecreation(t *testing.T) {
	for _, adapter := range []string{"native", "pg"} {
		t.Run(adapter, func(t *testing.T) { testAuthorizationUserRecreation(t, adapter) })
	}
}

func testAuthorizationUserRecreation(t *testing.T, adapter string) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	node, admin, _ := roleCatalogFixture(t)
	open := authorizationClientFactory(t, node, adapter)
	old := open("reader")
	var err error
	old.SetMaxOpenConns(1)
	if err := old.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.ExecContext(ctx, "DROP USER reader"); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"CREATE USER reader WITH PASSWORD 'session-test-password'", "GRANT CONNECT ON DATABASE shop TO reader", "GRANT ROLE private_reader ON DATABASE shop TO reader"} {
		if _, err := admin.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	fresh := open("reader")
	defer fresh.Close()
	if err = fresh.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	var n int
	if err = old.QueryRowContext(ctx, "SELECT COUNT(*) FROM payments").Scan(&n); authorizationState(err) != "42501" {
		t.Fatal("old login inherited new data rights", err)
	}
	for _, q := range []string{"SELECT COUNT(*) FROM kitdb_catalog.role_members", "SELECT COUNT(*) FROM kitdb_catalog.roles WHERE role_name='private_reader'", "SELECT COUNT(*) FROM kitdb_catalog.users"} {
		if err = old.QueryRowContext(ctx, q).Scan(&n); err != nil || n != 0 {
			t.Fatal("old login saw new incarnation metadata", q, n, err)
		}
	}
	var freshID int64
	if err = fresh.QueryRowContext(ctx, "SELECT session_id FROM kitdb_catalog.sessions").Scan(&freshID); err != nil {
		t.Fatal(err)
	}
	if _, err = old.ExecContext(ctx, fmt.Sprintf("KILL SESSION %d", freshID)); authorizationState(err) != "42501" {
		t.Fatal("old incarnation killed new login", err)
	}
	var oldID int64
	if err = old.QueryRowContext(ctx, "SELECT session_id FROM kitdb_catalog.sessions").Scan(&oldID); err != nil {
		t.Fatal(err)
	}
	if _, err = fresh.ExecContext(ctx, fmt.Sprintf("KILL SESSION %d", oldID)); authorizationState(err) != "42501" {
		t.Fatal("new incarnation killed old login", err)
	}
}

func TestAuthorizationLifecycleAdministratorRecreation(t *testing.T) {
	for _, adapter := range []string{"native", "pg"} {
		t.Run(adapter, func(t *testing.T) { testAuthorizationAdministratorRecreation(t, adapter) })
	}
}

func testAuthorizationAdministratorRecreation(t *testing.T, adapter string) {
	ctx := context.Background()
	node, _, _ := roleCatalogFixture(t)
	open := authorizationClientFactory(t, node, adapter)
	old := open("admin")
	if err := old.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	if err := node.catalog.SetUser(managed.UserInfo{Name: "backupadmin", Admin: true}, "session-test-password"); err != nil {
		t.Fatal(err)
	}
	backup, err := node.OpenAuthenticatedDatabase(ctx, "backupadmin", "session-test-password", "shop")
	if err != nil {
		t.Fatal(err)
	}
	defer backup.Close()
	if _, err = backup.ExecContext(ctx, "DROP USER admin"); err != nil {
		t.Fatal(err)
	}
	if err = node.catalog.SetUser(managed.UserInfo{Name: "admin", Admin: true}, "session-test-password"); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"CREATE USER forbidden WITH PASSWORD 'session-test-password'", "CREATE ROLE forbidden"} {
		if _, err = old.ExecContext(ctx, q); authorizationState(err) != "42501" {
			t.Fatal("old admin inherited recreated authority", q, err)
		}
	}
	var n int
	if err = old.QueryRowContext(ctx, "SELECT COUNT(*) FROM kitdb_catalog.users").Scan(&n); err != nil || n != 0 {
		t.Fatal("old admin enumerated current accounts", n, err)
	}
}
