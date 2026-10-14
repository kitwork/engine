package kitsql_test

import (
	"context"
	"database/sql"
	"net"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kitwork/engine/kitdb/kitsql"
	"github.com/kitwork/engine/kitdb/managed"
	"github.com/kitwork/engine/kitdb/relational"
)

func localRoleWriteFixture(t *testing.T) (context.Context, *sql.DB, func(string, string) *sql.DB) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	path := t.TempDir()
	if _, err := managed.Init(path); err != nil {
		t.Fatal(err)
	}
	e, err := relational.Open(filepath.Join(path, "shop", "data.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"CREATE TABLE articles(id BIGINT PRIMARY KEY,title TEXT)", "CREATE TABLE secret(id BIGINT PRIMARY KEY)"} {
		if _, err = e.Execute(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	if err = e.Close(); err != nil {
		t.Fatal(err)
	}
	root, err := managed.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = root.Attach("shop"); err != nil {
		t.Fatal(err)
	}
	const password = "role-write-test-password"
	if err = root.EnableAuth("admin", password); err != nil {
		t.Fatal(err)
	}
	if err = root.SetUser(managed.UserInfo{Name: "writer", Grants: []managed.DatabaseGrant{{Database: "shop", Permissions: []string{"connect"}}}}, password); err != nil {
		t.Fatal(err)
	}
	if err = root.Close(); err != nil {
		t.Fatal(err)
	}
	node, err := relational.OpenPostgresNode(relational.PostgresNodeOptions{Root: path})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { node.Close() })
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serverCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- node.ServePostgres(serverCtx, ln, relational.PostgresServerOptions{}) }()
	t.Cleanup(func() {
		stop()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("server drain timeout")
		}
	})
	handler, err := kitsql.NewHandler(kitsql.HandlerOptions{Open: kitsql.OpenNodeDatabase(node), AllowInsecureLocal: true})
	if err != nil {
		t.Fatal(err)
	}
	http := httptest.NewServer(handler)
	t.Cleanup(http.Close)
	open := func(adapter, user string) *sql.DB {
		var db *sql.DB
		var err error
		switch adapter {
		case "native":
			db, err = node.OpenAuthenticatedDatabase(ctx, user, password, "shop")
		case "pg":
			u := url.URL{Scheme: "postgres", Host: ln.Addr().String(), Path: "/shop", User: url.UserPassword(user, password), RawQuery: "sslmode=disable"}
			db, err = sql.Open("postgres", u.String())
		case "http":
			u := url.URL{Scheme: "kitsql", Host: strings.TrimPrefix(http.URL, "http://"), Path: "/shop", User: url.UserPassword(user, password), RawQuery: "sslmode=disable"}
			db, err = kitsql.OpenWithClient(u.String(), http.Client())
		}
		if err != nil {
			t.Fatal(err)
		}
		db.SetMaxOpenConns(1)
		return db
	}
	admin := open("native", "admin")
	t.Cleanup(func() { admin.Close() })
	return ctx, admin, open
}

func TestLocalRoleWritesAcrossAdapters(t *testing.T) {
	ctx, admin, open := localRoleWriteFixture(t)
	run := func(q string) {
		t.Helper()
		if _, err := admin.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	run("CREATE ROLE editor")
	run("GRANT ROLE editor ON DATABASE shop TO writer")
	for _, adapter := range []string{"native", "pg", "http"} {
		t.Run(adapter, func(t *testing.T) {
			guest := open(adapter, "writer")
			defer guest.Close()
			denied := func(q string, args ...any) {
				t.Helper()
				rows, err := guest.QueryContext(ctx, q, args...)
				if rows != nil {
					for rows.Next() {
					}
					if rows.Err() != nil {
						err = rows.Err()
					}
					rows.Close()
				}
				if err == nil {
					t.Fatalf("allowed %s", q)
				}
			}
			exec := func(q string, args ...any) {
				t.Helper()
				if _, err := guest.ExecContext(ctx, q, args...); err != nil {
					t.Fatalf("%s: %v", q, err)
				}
			}
			denied("INSERT INTO articles VALUES(1,'no')")
			run("GRANT INSERT ON TABLE articles TO ROLE editor")
			exec("INSERT INTO articles VALUES($1,$2)", 1, "insert-only")
			denied("SELECT * FROM articles")
			denied("INSERT INTO articles VALUES(2,'no') RETURNING title")
			denied("INSERT INTO secret VALUES(1)")
			denied("UPDATE articles SET title='no'")
			run("GRANT UPDATE ON TABLE articles TO ROLE editor")
			exec("UPDATE articles SET title='literal'")
			denied("UPDATE articles SET title='no' WHERE id=1")
			run("GRANT DELETE ON TABLE articles TO ROLE editor")
			denied("DELETE FROM articles WHERE id=1")
			run("GRANT SELECT ON TABLE articles TO ROLE editor")
			exec("UPDATE articles SET title=$1 WHERE id=$2", "updated", 1)
			var title string
			if err := guest.QueryRowContext(ctx, "INSERT INTO articles VALUES(2,'returned') RETURNING title").Scan(&title); err != nil || title != "returned" {
				t.Fatalf("returning %s %v", title, err)
			}
			denied("INSERT INTO articles VALUES(4,'partial'),(1,'duplicate')")
			var partial int
			if err := guest.QueryRowContext(ctx, "SELECT COUNT(*) FROM articles WHERE id=4").Scan(&partial); err != nil || partial != 0 {
				t.Fatal("failed insert published partial data", partial, err)
			}
			for _, q := range []string{"INSERT INTO articles SELECT id,'hidden' FROM secret", "INSERT INTO articles VALUES(1,'conflict') ON CONFLICT(id) DO UPDATE SET title=upper(excluded.title)", "UPDATE articles SET title=upper(title)", "DELETE FROM secret WHERE id=1", "DROP TABLE articles"} {
				denied(q)
			}
			for _, v := range []string{"table_grants", "effective_privileges"} {
				q := "SELECT can_insert,can_update,can_delete FROM kitdb_catalog." + v + " WHERE table_name='articles'"
				if v == "effective_privileges" {
					q += " AND policy_scope='session'"
				}
				var a, b, c bool
				if err := guest.QueryRowContext(ctx, q).Scan(&a, &b, &c); err != nil || !a || !b || !c {
					t.Fatalf("%s flags %v %v %v %v", v, a, b, c, err)
				}
			}
			if adapter != "http" {
				tx, err := guest.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
				if err != nil {
					t.Fatal(err)
				}
				var allowed bool
				if err = tx.QueryRowContext(ctx, "SELECT can_insert FROM kitdb_catalog.effective_privileges WHERE table_name='articles' AND policy_scope='session'").Scan(&allowed); err != nil || allowed {
					tx.Rollback()
					t.Fatal("readonly role view", allowed, err)
				}
				if _, err = tx.ExecContext(ctx, "INSERT INTO articles VALUES(3,'readonly')"); err == nil {
					t.Fatal("readonly wrote")
				}
				tx.Rollback()
				tx, err = guest.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = tx.ExecContext(ctx, "INSERT INTO articles VALUES(3,'rollback')"); err != nil {
					t.Fatal(err)
				}
				tx.Rollback()
				var n int
				if err = guest.QueryRowContext(ctx, "SELECT COUNT(*) FROM articles WHERE id=3").Scan(&n); err != nil || n != 0 {
					t.Fatalf("rollback %d %v", n, err)
				}
			}
			if adapter == "pg" {
				stmt, err := guest.PrepareContext(ctx, "UPDATE articles SET title=$1 WHERE id=$2")
				if err != nil {
					t.Fatal(err)
				}
				run("REVOKE UPDATE ON TABLE articles FROM ROLE editor")
				if _, err = stmt.ExecContext(ctx, "denied", 1); err == nil {
					t.Fatal("prepared bypassed revoke")
				}
				run("GRANT UPDATE ON TABLE articles TO ROLE editor")
				if _, err = stmt.ExecContext(ctx, "restored", 1); err != nil {
					t.Fatal(err)
				}
				stmt.Close()
			}
			exec("DELETE FROM articles WHERE id=$1", 2)
			run("REVOKE DELETE ON TABLE articles FROM ROLE editor")
			denied("DELETE FROM articles WHERE id=1")
			run("GRANT DELETE ON TABLE articles TO ROLE editor")
			exec("DELETE FROM articles WHERE id=1")
			for _, p := range []string{"SELECT", "INSERT", "UPDATE", "DELETE"} {
				run("REVOKE " + p + " ON TABLE articles FROM ROLE editor")
			}
		})
	}
	// Hidden write effects are not authorized merely by a grant on the root table.
	for _, q := range []string{"CREATE TABLE logs(id BIGINT PRIMARY KEY)", "CREATE TABLE triggered(id BIGINT PRIMARY KEY)", "CREATE TRIGGER log_insert AFTER INSERT ON triggered FOR EACH ROW INSERT INTO logs(id) VALUES(NEW.id)", "CREATE TABLE children(id BIGINT PRIMARY KEY,parent_id BIGINT REFERENCES secret(id) ON DELETE CASCADE)", "CREATE TABLE serials(id BIGSERIAL PRIMARY KEY)"} {
		run(q)
	}
	guest := open("native", "writer")
	defer guest.Close()
	for _, table := range []string{"triggered", "children", "serials", "secret"} {
		run("GRANT INSERT ON TABLE " + table + " TO ROLE editor")
		run("GRANT DELETE ON TABLE " + table + " TO ROLE editor")
		if _, err := guest.ExecContext(ctx, "INSERT INTO "+table+"(id) VALUES(7)"); err == nil {
			t.Fatalf("indirect insert %s allowed", table)
		}
		if _, err := guest.ExecContext(ctx, "INSERT INTO "+table+"(id) VALUES(7) ON CONFLICT(id) DO NOTHING"); err == nil {
			t.Fatalf("indirect upsert %s allowed", table)
		}
	}
	run("GRANT INSERT ON TABLE articles TO ROLE editor")
	run("DROP TABLE articles")
	run("CREATE TABLE articles(id BIGINT PRIMARY KEY,title TEXT)")
	if _, err := guest.ExecContext(ctx, "INSERT INTO articles VALUES(1,'inherited')"); err == nil {
		t.Fatal("recreated table inherited grant")
	}
}
