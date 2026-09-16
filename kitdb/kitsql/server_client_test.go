package kitsql_test

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kitwork/engine/kitdb/kitsql"
	"github.com/kitwork/engine/kitdb/relational"
)

func TestKitSQLEndToEndOverTLS(t *testing.T) {
	native, closeNative := openKitSQLFixture(t)
	defer closeNative()
	handler, err := kitsql.NewHandler(kitsql.HandlerOptions{
		Open: fixtureOpener(native), MaximumConcurrent: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(handler)
	defer server.Close()
	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	database, err := kitsql.OpenWithClient("kitsql://kitdb:secret@"+parsed.Host+"/shop", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	database.SetMaxOpenConns(2)
	ctx := context.Background()
	if err := database.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	result, err := database.ExecContext(ctx, `INSERT INTO products VALUES ($1, $2, $3)`, int64(2), "mouse", int64(75))
	if err != nil {
		t.Fatal(err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		t.Fatalf("affected = %d, %v", affected, err)
	}
	var name string
	var price int64
	if err := database.QueryRowContext(ctx, `SELECT name, price FROM products WHERE id = $1`, int64(2)).Scan(&name, &price); err != nil {
		t.Fatal(err)
	}
	if name != "mouse" || price != 75 {
		t.Fatalf("row = %q/%d", name, price)
	}
	if _, err := database.BeginTx(ctx, nil); err == nil || !strings.Contains(err.Error(), "does not support transactions") {
		t.Fatalf("BeginTx error = %v", err)
	}
}

func TestKitSQLDatabaseSQLDriverAndAuthentication(t *testing.T) {
	native, closeNative := openKitSQLFixture(t)
	defer closeNative()
	handler, err := kitsql.NewHandler(kitsql.HandlerOptions{
		Open: fixtureOpener(native), AllowInsecureLocal: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	parsed, _ := url.Parse(server.URL)
	good, err := sql.Open("kitsql", "kitsql://kitdb:secret@"+parsed.Host+"/shop?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	defer good.Close()
	var count int64
	if err := good.QueryRow(`SELECT count(*) FROM products`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("count = %d", count)
	}
	bad, err := kitsql.Open("kitsql://kitdb:wrong@" + parsed.Host + "/shop?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	defer bad.Close()
	if err := bad.Ping(); err == nil || !strings.Contains(err.Error(), "KSQL_AUTH") {
		t.Fatalf("authentication error = %v", err)
	}
}

func TestKitSQLRefusesCleartextOutsideLoopback(t *testing.T) {
	native, closeNative := openKitSQLFixture(t)
	defer closeNative()
	handler, err := kitsql.NewHandler(kitsql.HandlerOptions{
		Open: fixtureOpener(native), AllowInsecureLocal: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, kitsql.Path, strings.NewReader(`{}`))
	request.RemoteAddr = "203.0.113.10:41000"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUpgradeRequired || !strings.Contains(response.Body.String(), "KSQL_TLS_REQUIRED") {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
	if _, err := kitsql.Open("kitsql://kitdb:secret@db.example/shop?sslmode=disable"); err == nil {
		t.Fatal("remote cleartext connection URL was accepted")
	}
}

func openKitSQLFixture(t *testing.T) (*sql.DB, func()) {
	t.Helper()
	engine, err := relational.Open(filepath.Join(t.TempDir(), "shop.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	native, err := relational.OpenNativeSQL(engine)
	if err != nil {
		_ = engine.Close()
		t.Fatal(err)
	}
	for _, source := range []string{
		`CREATE TABLE products (id BIGINT PRIMARY KEY, name TEXT NOT NULL, price BIGINT NOT NULL)`,
		`INSERT INTO products VALUES (1, 'keyboard', 100)`,
	} {
		if _, err := native.Exec(source); err != nil {
			_ = native.Close()
			_ = engine.Close()
			t.Fatal(err)
		}
	}
	return native, func() {
		_ = native.Close()
		_ = engine.Close()
	}
}

func fixtureOpener(native *sql.DB) kitsql.OpenDatabase {
	return func(_ context.Context, user, password, database string) (*sql.DB, func() error, error) {
		if user != "kitdb" || password != "secret" {
			return nil, nil, kitsql.ErrUnauthorized
		}
		if database != "shop" {
			return nil, nil, kitsql.ErrDatabaseNotFound
		}
		return native, func() error { return nil }, nil
	}
}
