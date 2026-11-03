package work

import (
	"context"
	"database/sql"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/kitwork/engine/kitdb/gateway"
	"github.com/kitwork/engine/kitdb/pgwire"
)

// startPostgresTestServer serves a SQLite file over the PostgreSQL wire protocol on loopback, the database
// connect({ postgresql: … }) reaches in these tests: user kitdb, database transactions, the password given, a
// cleartext login (pgwire's profile on loopback), read-only through kitdb/gateway. seed fills its products
// table. It replaces the KitDB file the old KROW port served (03/11/2026).
func startPostgresTestServer(t *testing.T, password, seed string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "transactions.db")
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`CREATE TABLE products (id TEXT PRIMARY KEY, sku TEXT NOT NULL UNIQUE, title TEXT NOT NULL, price INTEGER NOT NULL DEFAULT 0)`,
		seed,
	} {
		if _, err := database.Exec(statement); err != nil {
			database.Close()
			t.Fatalf("%s: %v", statement, err)
		}
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := pgwire.Server{Authenticator: postgresTestLogin{path: path, password: password}, MaxConnections: 8}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx, listener) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("the PostgreSQL test server did not stop")
		}
	})
	return listener.Addr().String()
}

// postgresTestLogin lets in user kitdb to database transactions with its password, as a server refuses any
// other login: 28P01, what PostgreSQL answers a wrong password with.
type postgresTestLogin struct {
	path, password string
}

func (login postgresTestLogin) Authenticate(ctx context.Context, startup pgwire.Startup, password string) (pgwire.Session, error) {
	if startup.User() != "kitdb" || startup.Database() != "transactions" || password != login.password {
		return nil, pgwire.NewError("28P01", "password authentication failed")
	}
	return gateway.Open(ctx, gateway.Target{Name: "transactions", Schemas: map[string]string{"public": login.path}},
		postgresTestAccess{}, nil, gateway.Options{User: "kitdb"})
}

// postgresTestAccess lets the session read every table.
type postgresTestAccess struct{}

func (postgresTestAccess) CanSelect(context.Context, string, string) (bool, error) { return true, nil }
func (postgresTestAccess) Command(context.Context, string) (string, bool, error) {
	return "", false, nil
}
func (postgresTestAccess) Current(context.Context) (bool, error) { return true, nil }
