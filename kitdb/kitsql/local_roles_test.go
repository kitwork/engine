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

func TestDatabaseLocalRolesAcrossAdapters(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	path := t.TempDir()
	if _, err := managed.Init(path); err != nil {
		t.Fatal(err)
	}
	e, err := relational.Open(filepath.Join(path, "shop", "data.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{"CREATE TABLE articles(id BIGINT PRIMARY KEY, title TEXT)", "CREATE TABLE payments(id BIGINT PRIMARY KEY)", "INSERT INTO articles VALUES(1,'public')"} {
		if _, err := e.Execute(ctx, source); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	root, err := managed.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := root.Attach("shop"); err != nil {
		t.Fatal(err)
	}
	const password = "local-role-password"
	if err := root.EnableAuth("admin", password); err != nil {
		t.Fatal(err)
	}
	if err := root.SetUser(managed.UserInfo{Name: "guest", Grants: []managed.DatabaseGrant{{Database: "shop", Permissions: []string{"connect"}}}}, password); err != nil {
		t.Fatal(err)
	}
	if err := root.Close(); err != nil {
		t.Fatal(err)
	}
	node, err := relational.OpenPostgresNode(relational.PostgresNodeOptions{Root: path})
	if err != nil {
		t.Fatal(err)
	}
	defer node.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serveCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- node.ServePostgres(serveCtx, listener, relational.PostgresServerOptions{}) }()
	defer func() {
		stop()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("pgwire did not stop")
		}
	}()
	handler, err := kitsql.NewHandler(kitsql.HandlerOptions{Open: kitsql.OpenNodeDatabase(node), AllowInsecureLocal: true})
	if err != nil {
		t.Fatal(err)
	}
	http := httptest.NewServer(handler)
	defer http.Close()
	open := func(adapter, user string) *sql.DB {
		var db *sql.DB
		var err error
		switch adapter {
		case "native":
			db, err = node.OpenAuthenticatedDatabase(ctx, user, password, "shop")
		case "pg":
			u := url.URL{Scheme: "postgres", Host: listener.Addr().String(), Path: "/shop", User: url.UserPassword(user, password), RawQuery: "sslmode=disable"}
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
	defer admin.Close()
	for _, source := range []string{"CREATE ROLE article_reader", "GRANT SELECT ON TABLE articles TO ROLE article_reader", "GRANT ROLE article_reader ON DATABASE shop TO guest"} {
		if _, err := admin.ExecContext(ctx, source); err != nil {
			t.Fatalf("%s: %v", source, err)
		}
	}
	for _, adapter := range []string{"native", "pg", "http"} {
		t.Run(adapter, func(t *testing.T) {
			guest := open(adapter, "guest")
			defer guest.Close()
			var title string
			if err := guest.QueryRowContext(ctx, "SELECT title FROM articles WHERE id=$1", 1).Scan(&title); err != nil || title != "public" {
				t.Fatalf("allowed SELECT: %q %v", title, err)
			}
			var role string
			if err := guest.QueryRowContext(ctx, "SELECT role_name FROM kitdb_catalog.roles WHERE role_name=$1", "article_reader").Scan(&role); err != nil || role != "article_reader" {
				t.Fatalf("%s role view: %q %v", adapter, role, err)
			}
			var canSelect bool
			var selectSource string
			if err := guest.QueryRowContext(ctx, "SELECT can_select,select_source FROM kitdb_catalog.effective_privileges WHERE table_name='articles' AND policy_scope='session'").Scan(&canSelect, &selectSource); err != nil || !canSelect || selectSource != "local_role" {
				t.Fatalf("%s effective: %v %q %v", adapter, canSelect, selectSource, err)
			}
			var count int
			for _, view := range []string{"table_grants", "role_members"} {
				if err := guest.QueryRowContext(ctx, "SELECT COUNT(*) FROM kitdb_catalog."+view).Scan(&count); err != nil || count != 1 {
					t.Fatalf("%s %s: %d %v", adapter, view, count, err)
				}
			}
			if err := guest.QueryRowContext(ctx, "SELECT COUNT(*) FROM kitdb_catalog.effective_privileges WHERE table_name='payments'").Scan(&count); err != nil || count != 0 {
				t.Fatalf("%s private metadata: %d %v", adapter, count, err)
			}
			if adapter == "pg" {
				stmt, err := guest.PrepareContext(ctx, "SELECT title FROM articles WHERE id=$1")
				if err != nil {
					t.Fatal(err)
				}
				if err := stmt.QueryRowContext(ctx, 1).Scan(&title); err != nil {
					t.Fatal(err)
				}
				stmt.Close()
				view, err := guest.PrepareContext(ctx, "SELECT role_name FROM kitdb_catalog.roles WHERE role_name=$1")
				if err != nil {
					t.Fatal(err)
				}
				if err := view.QueryRowContext(ctx, "article_reader").Scan(&role); err != nil {
					t.Fatal(err)
				}
				view.Close()
				prepared, err := guest.PrepareContext(ctx, "SELECT title FROM articles WHERE id=$1")
				if err != nil {
					t.Fatal(err)
				}
				if _, err := admin.ExecContext(ctx, "REVOKE SELECT ON TABLE articles FROM ROLE article_reader"); err != nil {
					t.Fatal(err)
				}
				if err := prepared.QueryRowContext(ctx, 1).Scan(&title); err == nil {
					t.Fatal("prepared read bypassed revoke")
				}
				if _, err := admin.ExecContext(ctx, "GRANT SELECT ON TABLE articles TO ROLE article_reader"); err != nil {
					t.Fatal(err)
				}
				if err := prepared.QueryRowContext(ctx, 1).Scan(&title); err != nil {
					t.Fatal("prepared read not restored", err)
				}
				prepared.Close()
			}
			for _, source := range []string{"SELECT * FROM payments", "SELECT a.title FROM articles a JOIN payments p ON a.id=p.id", "SELECT title FROM articles UNION ALL SELECT title FROM articles", "WITH hidden AS (SELECT * FROM payments) SELECT * FROM hidden", "SELECT nextval('secret') FROM articles", "INSERT INTO articles VALUES(2,'denied')", "DELETE FROM articles WHERE id=1", "DROP TABLE articles", "CREATE ROLE forbidden", "GRANT ROLE article_reader ON DATABASE shop TO guest"} {
				rows, err := guest.QueryContext(ctx, source)
				if rows != nil {
					for rows.Next() {
					}
					if rows.Err() != nil {
						err = rows.Err()
					}
					rows.Close()
				}
				if err == nil {
					t.Fatalf("accepted %s", source)
				}
			}
			if adapter != "http" {
				failed, err := guest.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
				if err != nil {
					t.Fatal(err)
				}
				if err := failed.QueryRowContext(ctx, "SELECT COUNT(*) FROM payments").Scan(new(int)); err == nil {
					failed.Rollback()
					t.Fatal("private table in transaction")
				}
				if err := failed.Rollback(); err != nil {
					t.Fatal("denied query prevented ROLLBACK", err)
				}
				tx, err := guest.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				if err := tx.QueryRowContext(ctx, "SELECT title FROM articles").Scan(&title); err != nil {
					t.Fatal(err)
				}
				if _, err := admin.ExecContext(ctx, "REVOKE SELECT ON TABLE articles FROM ROLE article_reader"); err != nil {
					t.Fatal(err)
				}
				if err := tx.QueryRowContext(ctx, "SELECT title FROM articles").Scan(&title); err != nil {
					t.Fatal("snapshot lost ACL", err)
				}
				tx.Rollback()
				if err := guest.QueryRowContext(ctx, "SELECT title FROM articles").Scan(&title); err == nil {
					t.Fatal("revoke ignored by fresh snapshot")
				}
				if _, err := admin.ExecContext(ctx, "GRANT SELECT ON TABLE articles TO ROLE article_reader"); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
	// Name-derived table IDs must not transfer old ACLs to a recreated table.
	old := open("native", "guest")
	defer old.Close()
	if err := old.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{"DROP TABLE articles", "CREATE TABLE articles(id BIGINT PRIMARY KEY, title TEXT)", "INSERT INTO articles VALUES(1,'private replacement')"} {
		if _, err := admin.ExecContext(ctx, source); err != nil {
			t.Fatal(err)
		}
	}
	var title string
	if err := old.QueryRowContext(ctx, "SELECT title FROM articles").Scan(&title); err == nil {
		t.Fatal("drop/recreate inherited ACL")
	}
	if _, err := admin.ExecContext(ctx, "DROP ROLE article_reader"); err == nil {
		t.Fatal("mapped role dropped")
	}
	if _, err := admin.ExecContext(ctx, "REVOKE ROLE article_reader ON DATABASE shop FROM guest"); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.ExecContext(ctx, "DROP ROLE article_reader"); err != nil {
		t.Fatal(err)
	}
}
