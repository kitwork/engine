package kitsql_test

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kitwork/engine/kitdb/kitsql"
	"github.com/kitwork/engine/kitdb/managed"
	"github.com/kitwork/engine/kitdb/relational"
	"github.com/lib/pq"
)

func TestNodeCatalogAuthAcrossAdapters(t *testing.T) {
	rootPath := t.TempDir()
	if _, err := managed.Init(rootPath); err != nil {
		t.Fatal(err)
	}
	root, err := managed.Open(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"shop", "blog"} {
		folder := filepath.Join(rootPath, name)
		if err := os.MkdirAll(folder, 0700); err != nil {
			t.Fatal(err)
		}
		engine, err := relational.Open(filepath.Join(folder, "data.kitdb"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := engine.Execute(context.Background(), "CREATE TABLE items(id BIGINT PRIMARY KEY, value BIGINT NOT NULL)"); err != nil {
			t.Fatal(err)
		}
		if _, err := engine.Execute(context.Background(), "INSERT INTO items VALUES(1,0)"); err != nil {
			t.Fatal(err)
		}
		if _, err := engine.Execute(context.Background(), "CREATE SEQUENCE sequence_test"); err != nil {
			t.Fatal(err)
		}
		if err := engine.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := root.Register(name, name); err != nil {
			t.Fatal(err)
		}
	}
	const password = "catalog-test-password"
	if err := root.EnableAuth("admin", password); err != nil {
		t.Fatal(err)
	}
	for name, role := range map[string]string{"reader": "reader", "writer": "writer"} {
		if err := root.SetUser(managed.UserInfo{Name: name, Grants: []managed.DatabaseGrant{{Database: "shop", Role: role}}}, password); err != nil {
			t.Fatal(err)
		}
	}
	for name, grant := range map[string]managed.DatabaseGrant{
		"developer":    {Database: "shop", Role: "developer"},
		"connectonly":  {Database: "shop", Permissions: []string{"connect"}},
		"schemareader": {Database: "shop", Permissions: []string{"connect", "read", "schema"}},
		"schemaonly":   {Database: "shop", Permissions: []string{"connect", "schema"}},
		"noconnect":    {Database: "shop", Permissions: []string{"read"}},
		"writeonly":    {Database: "shop", Permissions: []string{"connect", "write"}},
	} {
		if err := root.SetUser(managed.UserInfo{Name: name, Grants: []managed.DatabaseGrant{grant}}, password); err != nil {
			t.Fatal(err)
		}
	}
	if err := root.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	node, err := relational.OpenPostgresNode(relational.PostgresNodeOptions{Root: rootPath, User: "legacy", Password: "legacy-password"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := node.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := managed.Open(rootPath); err == nil {
		t.Fatal("offline mutation allowed while serving")
	}
	if _, err := node.OpenNativeDatabase("shop"); err == nil {
		t.Fatal("unauthenticated native bypass")
	}
	if _, err := node.OpenAuthenticatedDatabase(ctx, "legacy", "legacy-password", "shop"); err == nil {
		t.Fatal("config credential fallback")
	}
	if _, err := node.OpenAuthenticatedDatabase(ctx, "writer", password, "blog"); err == nil {
		t.Fatal("cross-database grant bypass")
	}
	if _, err := node.OpenAuthenticatedDatabase(ctx, "noconnect", password, "shop"); err == nil {
		t.Fatal("read implied connect")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	pgCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- node.ServePostgres(pgCtx, listener, relational.PostgresServerOptions{MaxConnections: 16, QueryTimeout: 5 * time.Second})
	}()
	t.Cleanup(func() {
		stop()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("pgwire did not drain")
		}
	})
	pgPool := func(user, database string) *sql.DB {
		u := &url.URL{Scheme: "postgres", Host: listener.Addr().String(), Path: "/" + database, User: url.UserPassword(user, password), RawQuery: "sslmode=disable"}
		db, err := sql.Open("postgres", u.String())
		if err != nil {
			t.Fatal(err)
		}
		db.SetMaxOpenConns(1)
		t.Cleanup(func() { _ = db.Close() })
		return db
	}
	denied := pgPool("writer", "blog")
	var permission *pq.Error
	if err := denied.PingContext(ctx); !errors.As(err, &permission) || permission.Code != "42501" {
		t.Fatalf("wrong denial: %v", err)
	}
	missing := pgPool("writer", "nonexistent")
	if err := missing.PingContext(ctx); !errors.As(err, &permission) || permission.Code != "42501" {
		t.Fatalf("database existence oracle: %v", err)
	}
	readerPG := pgPool("reader", "shop")
	names, err := readerPG.QueryContext(ctx, "SELECT datname FROM pg_catalog.pg_database ORDER BY datname")
	if err != nil {
		t.Fatal(err)
	}
	var visible []string
	for names.Next() {
		var name string
		if err := names.Scan(&name); err != nil {
			t.Fatal(err)
		}
		visible = append(visible, name)
	}
	if err := names.Err(); err != nil {
		t.Fatal(err)
	}
	names.Close()
	if strings.Join(visible, ",") != "kitdb,shop" {
		t.Fatalf("metadata leaked: %v", visible)
	}
	maintenance := pgPool("reader", "kitdb")
	var count int
	if err := maintenance.QueryRowContext(ctx, "SELECT COUNT(*) FROM pg_catalog.pg_database").Scan(&count); err != nil || count != 2 {
		t.Fatalf("maintenance metadata leaked: %d / %v", count, err)
	}
	handler, err := kitsql.NewHandler(kitsql.HandlerOptions{AllowInsecureLocal: true, MaximumConcurrent: 4,
		Open: kitsql.OpenNodeDatabase(node)})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	for _, attempt := range []struct{ user, password, database, code string }{
		{"reader", password, "blog", "KSQL_FORBIDDEN"},
		{"reader", password, "nonexistent", "KSQL_FORBIDDEN"},
		{"reader", "wrong-password", "shop", "KSQL_AUTH"},
	} {
		db, err := kitsql.OpenWithClient("kitsql://"+attempt.user+":"+attempt.password+"@"+strings.TrimPrefix(server.URL, "http://")+"/"+attempt.database+"?sslmode=disable", server.Client())
		if err != nil {
			t.Fatal(err)
		}
		err = db.PingContext(ctx)
		_ = db.Close()
		var protocol *kitsql.ProtocolError
		if !errors.As(err, &protocol) || protocol.Code != attempt.code {
			t.Fatalf("wrong HTTP auth denial: %v", err)
		}
	}
	for _, user := range []string{"reader", "writer"} {
		native, err := node.OpenAuthenticatedDatabase(ctx, user, password, "shop")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = native.Close() })
		pg := pgPool(user, "shop")
		remote, err := kitsql.OpenWithClient("kitsql://"+user+":"+password+"@"+strings.TrimPrefix(server.URL, "http://")+"/shop?sslmode=disable", server.Client())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = remote.Close() })
		for adapter, pool := range map[string]*sql.DB{"native": native, "pgwire": pg, "kitsql": remote} {
			if user == "reader" {
				checkOwnAccountCatalog(t, ctx, pool, user)
			}
			var value int64
			if err := pool.QueryRowContext(ctx, "SELECT value FROM items WHERE id=1").Scan(&value); err != nil {
				t.Fatalf("%s %s read: %v", user, adapter, err)
			}
			_, err := pool.ExecContext(ctx, "UPDATE items SET value=value+1 WHERE id=1")
			if user == "reader" && err == nil {
				t.Fatalf("%s reader wrote", adapter)
			}
			if user == "reader" {
				if _, err := pool.ExecContext(ctx, "CREATE TABLE forbidden(id BIGINT PRIMARY KEY)"); err == nil {
					t.Fatalf("%s reader performed DDL", adapter)
				}
				if adapter != "kitsql" {
					tx, err := pool.BeginTx(ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					_, err = tx.ExecContext(ctx, "UPDATE items SET value=value+1 WHERE id=1")
					_ = tx.Rollback()
					if err == nil {
						t.Fatalf("%s reader bypassed permission in transaction", adapter)
					}
				}
			}
			if user == "writer" && err != nil {
				t.Fatalf("%s writer failed: %v", adapter, err)
			}
			if user == "writer" {
				for _, query := range []string{"CREATE TABLE forbidden(id BIGINT PRIMARY KEY)", "DROP TABLE items", "ANALYZE items", "REINDEX TABLE items"} {
					if _, err := pool.ExecContext(ctx, query); err == nil {
						t.Fatalf("%s writer performed schema operation: %s", adapter, query)
					}
				}
				if adapter != "kitsql" {
					tx, err := pool.BeginTx(ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					_, err = tx.ExecContext(ctx, "CREATE TABLE forbidden(id BIGINT PRIMARY KEY)")
					_ = tx.Rollback()
					if err == nil {
						t.Fatalf("%s writer performed transactional DDL", adapter)
					}
				}
			}
		}
	}
	for _, user := range []string{"connectonly", "developer", "schemareader", "schemaonly", "writeonly"} {
		native, err := node.OpenAuthenticatedDatabase(ctx, user, password, "shop")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = native.Close() })
		remote, err := kitsql.OpenWithClient("kitsql://"+user+":"+password+"@"+strings.TrimPrefix(server.URL, "http://")+"/shop?sslmode=disable", server.Client())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = remote.Close() })
		for adapter, pool := range map[string]*sql.DB{"native": native, "pgwire": pgPool(user, "shop"), "kitsql": remote} {
			if user == "connectonly" {
				checkOwnAccountCatalog(t, ctx, pool, user)
			}
			if err := pool.PingContext(ctx); err != nil {
				t.Fatalf("%s %s connect: %v", user, adapter, err)
			}
			var value int64
			err := pool.QueryRowContext(ctx, "SELECT value FROM items WHERE id=1").Scan(&value)
			canRead := user == "developer" || user == "schemareader"
			if (err == nil) != canRead {
				t.Fatalf("%s %s read: %v", user, adapter, err)
			}
			_, err = pool.ExecContext(ctx, "CREATE TABLE permission_"+user+"_"+adapter+"(id BIGINT PRIMARY KEY)")
			if (err == nil) != (user == "developer" || user == "schemareader" || user == "schemaonly") {
				t.Fatalf("%s %s schema: %v", user, adapter, err)
			}
			if user == "writeonly" {
				if _, err := pool.ExecContext(ctx, "UPDATE items SET value=value WHERE id=1"); err != nil {
					t.Fatalf("%s write-only mutation: %v", adapter, err)
				}
				if _, err := pool.ExecContext(ctx, "UPDATE items SET value=value+100 WHERE id=1 RETURNING value"); err == nil {
					t.Fatalf("%s write-only RETURNING leaked data", adapter)
				}
			} else if user != "developer" {
				if _, err := pool.ExecContext(ctx, "UPDATE items SET value=value+100 WHERE id=1"); err == nil {
					t.Fatalf("%s %s wrote without write", user, adapter)
				}
				if err := pool.QueryRowContext(ctx, "SELECT nextval('sequence_test')").Scan(&value); err == nil {
					t.Fatalf("%s %s mutated sequence without write", user, adapter)
				}
			}
		}
	}
	admin, err := node.OpenAuthenticatedDatabase(ctx, "admin", password, "shop")
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	var users int
	if err := admin.QueryRowContext(ctx, "SELECT COUNT(*) FROM kitdb_catalog.users").Scan(&users); err != nil || users != 9 {
		t.Fatalf("admin users: %d / %v", users, err)
	}
	for _, pool := range []*sql.DB{admin, pgPool("admin", "kitdb")} {
		if _, err := pool.ExecContext(ctx, "DELETE FROM kitdb_catalog.users"); err == nil {
			t.Fatal("admin mutated account view")
		}
		var count int
		if err := pool.QueryRowContext(ctx, "SELECT COUNT(*) FROM kitdb_catalog.grants WHERE username=$1 AND can_write=true", "writer").Scan(&count); err != nil || count != 1 {
			t.Fatalf("admin filtered grants: %d / %v", count, err)
		}
	}
	var value int64
	if err := admin.QueryRowContext(ctx, "SELECT value FROM items WHERE id=1").Scan(&value); err != nil || value != 3 {
		t.Fatalf("denied write changed data or writer lost ACK: %d / %v", value, err)
	}
	if _, err := node.OpenAuthenticatedDatabase(ctx, "reader", password, ".catalog"); err == nil {
		t.Fatal("internal auth store exposed")
	}
}

func checkOwnAccountCatalog(t *testing.T, ctx context.Context, pool *sql.DB, user string) {
	t.Helper()
	var name string
	var admin, disabled bool
	if err := pool.QueryRowContext(ctx, "SELECT username,is_admin,disabled FROM kitdb_catalog.users").Scan(&name, &admin, &disabled); err != nil || name != user || admin || disabled {
		t.Fatalf("%s account view: %s / %v", user, name, err)
	}
	var count int
	if err := pool.QueryRowContext(ctx, "SELECT COUNT(*) FROM kitdb_catalog.users WHERE username=$1 OR username=$2", user, "admin").Scan(&count); err != nil || count != 1 {
		t.Fatalf("%s cross-user WHERE leaked: %d / %v", user, count, err)
	}
	if err := pool.QueryRowContext(ctx, "SELECT COUNT(*) FROM kitdb_catalog.grants WHERE username='admin'").Scan(&count); err != nil || count != 0 {
		t.Fatalf("%s admin grants leaked: %d / %v", user, count, err)
	}
	rows, err := pool.QueryContext(ctx, "SELECT username,database_name,can_connect,can_read,can_write,can_schema FROM kitdb_catalog.grants ORDER BY database_name")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	found := 0
	for rows.Next() {
		var username, database string
		var connect, read, write, schema bool
		if err := rows.Scan(&username, &database, &connect, &read, &write, &schema); err != nil {
			t.Fatal(err)
		}
		if username != user || database != "shop" || !connect {
			t.Fatalf("wrong visible grant: %s/%s", username, database)
		}
		if write != (user == "writer" || user == "developer" || user == "writeonly") ||
			read != (user == "reader" || user == "writer" || user == "developer" || user == "schemareader") ||
			schema != (user == "developer" || user == "schemareader" || user == "schemaonly") {
			t.Fatalf("%s wrong effective grant", user)
		}
		found++
	}
	if err := rows.Err(); err != nil || found != 1 {
		t.Fatalf("%s grants: %d / %v", user, found, err)
	}
	for _, query := range []string{"SELECT verifier FROM kitdb_catalog.users", "SELECT salt FROM kitdb_catalog.users", "UPDATE kitdb_catalog.grants SET can_schema=true"} {
		if _, err := pool.ExecContext(ctx, query); err == nil {
			t.Fatalf("%s unsafe catalog query: %s", user, query)
		}
	}
}
