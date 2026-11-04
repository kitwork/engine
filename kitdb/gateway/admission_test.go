package gateway

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/kitwork/engine/kitdb/pgwire"
)

// userAuthenticator opens a gateway Session for the cleartext password "secret" as the user the client named.
type userAuthenticator struct {
	fixture *fixture
}

func (authenticator userAuthenticator) Authenticate(ctx context.Context, startup pgwire.Startup, password string) (pgwire.Session, error) {
	if password != "secret" {
		return nil, pgwire.NewError("28P01", "password authentication failed for user "+startup.User())
	}
	return Open(ctx, authenticator.fixture.target(), defaultAccess(), fakeColumns{}, Options{User: startup.User()})
}

// One account cannot hold the port: the statements running at once are shared out by user, so while one user's
// statements fill that user's share, another user's statement runs at once. A statement that reads no table (a
// recursive CTE counting a billion) is authorized for anyone, and runs until the port's QueryTimeout interrupts it.
//
// CONTROL (04/11/2026): with QueryAdmission taken out of Session, every session waits in one share and the other
// user's "SELECT 1" waits for the heavy statement's timeout: "light's SELECT 1 took 2.7s while heavy held its share".
func TestOneAccountCannotHoldThePort(t *testing.T) {
	fixture := newFixture(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	server := pgwire.Server{
		Authenticator:              userAuthenticator{fixture: fixture},
		MaxConcurrentQueries:       2,
		MaxConcurrentQueriesPerKey: 1,
		QueryTimeout:               3 * time.Second,
	}
	go func() { served <- server.Serve(ctx, listener) }()
	t.Cleanup(func() {
		cancel()
		if err := <-served; err != nil {
			t.Errorf("Serve: %v", err)
		}
	})
	port := listener.Addr().(*net.TCPAddr).Port
	open := func(user string) *sql.DB {
		t.Helper()
		client, err := sql.Open("postgres", fmt.Sprintf(
			"host=127.0.0.1 port=%d user=%s password=secret dbname=demo.kitwork.io/application sslmode=disable", port, user))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = client.Close() })
		if err := client.Ping(); err != nil {
			t.Fatalf("%s: %v", user, err)
		}
		return client
	}
	heavy, light := open("heavy"), open("light")

	running := make(chan error, 1)
	go func() {
		var sum int64
		running <- heavy.QueryRow(`WITH RECURSIVE c(x) AS (VALUES(1) UNION ALL SELECT x + 1 FROM c WHERE x < 1000000000)
			SELECT sum(x) FROM c`).Scan(&sum)
	}()
	time.Sleep(300 * time.Millisecond) // the heavy statement holds its user's share by now
	start := time.Now()
	var one int
	if err := light.QueryRow(`SELECT 1`).Scan(&one); err != nil || one != 1 {
		t.Fatalf("light's SELECT 1 = %d (%v)", one, err)
	}
	if took := time.Since(start); took > 1500*time.Millisecond {
		t.Fatalf("light's SELECT 1 took %.1fs while heavy held its share", took.Seconds())
	}
	select {
	case err := <-running:
		if err == nil {
			t.Fatal("the recursive CTE of a billion rows finished inside the port's QueryTimeout")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the heavy statement still runs 10s after the port's QueryTimeout")
	}
}
