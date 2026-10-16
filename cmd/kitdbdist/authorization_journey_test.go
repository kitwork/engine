package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lib/pq"
)

type authorizationServer struct {
	command *exec.Cmd
	stderr  bytes.Buffer
	address string
	stopped bool
}

func (s *authorizationServer) stop(t *testing.T) {
	t.Helper()
	if s.stopped {
		return
	}
	s.stopped = true
	killErr := s.command.Process.Kill()
	waitErr := s.command.Wait()
	var exit *exec.ExitError
	if killErr != nil || !errors.As(waitErr, &exit) {
		t.Errorf("authorization server hard stop: kill=%v wait=%v", killErr, waitErr)
	}
	if s.stderr.Len() != 0 {
		t.Errorf("authorization server stderr: %s", s.stderr.String())
	}
}

func startAuthorizationServer(t *testing.T, ctx context.Context, binary, root string) *authorizationServer {
	t.Helper()
	s := &authorizationServer{command: exec.CommandContext(ctx, binary, "-root", root,
		"-listen", "127.0.0.1:0", "-verify-on-open", "-max-connections", "8")}
	s.command.Dir = root
	s.command.Env = buildEnvironment(os.Environ(), map[string]string{"KITDB_TOKEN": "", "KITDB_AUTH_PASSWORD": ""})
	s.command.Stderr = &s.stderr
	stdout, err := s.command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.stop(t) })
	ready := make(chan string, 1)
	go func() {
		reader := bufio.NewReader(stdout)
		line, _ := reader.ReadString('\n')
		ready <- line
		_, _ = io.Copy(io.Discard, reader)
	}()
	select {
	case line := <-ready:
		const prefix = "KitDB standalone PostgreSQL node listening on "
		if strings.HasPrefix(line, prefix) {
			s.address = strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	case <-time.After(10 * time.Second):
		t.Fatal("authorization server readiness timeout")
	}
	host, _, err := net.SplitHostPort(s.address)
	if err != nil || host != "127.0.0.1" {
		t.Fatalf("authorization server readiness address %q: %v", s.address, err)
	}
	return s
}

func authorizationClient(t *testing.T, ctx context.Context, server *authorizationServer, user, password string) *sql.DB {
	t.Helper()
	u := url.URL{Scheme: "postgres", Host: server.address, Path: "/shop",
		User: url.UserPassword(user, password), RawQuery: "sslmode=disable&connect_timeout=5"}
	client, err := sql.Open("postgres", u.String())
	if err != nil {
		t.Fatal(err)
	}
	client.SetMaxOpenConns(1)
	client.SetMaxIdleConns(1)
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Error(err)
		}
	})
	return client
}

func requireAuthorizationState(t *testing.T, err error, want string) {
	t.Helper()
	var wire *pq.Error
	if !errors.As(err, &wire) || string(wire.Code) != want {
		t.Fatalf("expected SQLSTATE %s, got %v", want, err)
	}
}

func authorizationRows(t *testing.T, ctx context.Context, client *sql.DB, want []string) {
	t.Helper()
	rows, err := client.QueryContext(ctx, "SELECT id,title FROM articles ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var id int
		var title string
		if err := rows.Scan(&id, &title); err != nil {
			t.Fatal(err)
		}
		if id != len(got)+1 {
			t.Fatalf("unexpected article ID %d", id)
		}
		got = append(got, title)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("article rows %v, want %v", got, want)
	}
}

func exerciseAuthorizationBinaries(t *testing.T, binary func(string) string, checks *[]string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	dir := t.TempDir()
	cli := func(t *testing.T, password string, args ...string) {
		t.Helper()
		data, err := run(ctx, dir, map[string]string{"KITDB_TOKEN": "", "KITDB_AUTH_PASSWORD": password}, binary("kitdb"), args...)
		if err != nil {
			t.Fatal(err)
		}
		var result struct{ Format string }
		if err := json.Unmarshal(data, &result); err != nil || result.Format != commandFormatForTest {
			t.Fatalf("operator response: %s: %v", data, err)
		}
	}
	execSQL := func(t *testing.T, client *sql.DB, query string, args ...any) {
		t.Helper()
		if _, err := client.ExecContext(ctx, query, args...); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	deny := func(t *testing.T, client *sql.DB, query string) {
		t.Helper()
		_, err := client.ExecContext(ctx, query)
		requireAuthorizationState(t, err, "42501")
	}
	// Each stage is required; a filtered stage cannot silently qualify the journey.
	stage := func(name string, exercise func(*testing.T)) {
		t.Helper()
		completed := false
		if !t.Run(name, func(child *testing.T) {
			exercise(child)
			completed = !child.Failed() && !child.Skipped()
		}) || !completed {
			t.Fatalf("mandatory authorization stage %s did not pass", name)
		}
		*checks = append(*checks, name)
	}
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		t.Fatal(err)
	}
	password := hex.EncodeToString(secret[:])
	root := filepath.Join(dir, "source")
	source := filepath.Join(root, "shop", "data.kitdb")
	stage("offline-root-bootstrap", func(t *testing.T) {
		if err := os.MkdirAll(filepath.Dir(source), 0700); err != nil {
			t.Fatal(err)
		}
		cli(t, "", "init-root", root)
		cli(t, "", "query", "--create", source, "CREATE TABLE articles(id BIGINT PRIMARY KEY,title TEXT)")
		cli(t, "", "query", source, "INSERT INTO articles VALUES(1,'public')")
		cli(t, "", "register-database", root, "shop", "shop")
		cli(t, password, "auth-init", root, "admin")
	})
	server := startAuthorizationServer(t, ctx, binary("kitdbpg"), root)
	admin := authorizationClient(t, ctx, server, "admin", password)
	var roleID string
	stage("sql-users-roles-mappings", func(t *testing.T) {
		for _, user := range []string{"reader", "writer"} {
			execSQL(t, admin, "CREATE USER "+user+" WITH PASSWORD $1", password)
			execSQL(t, admin, "GRANT CONNECT ON DATABASE shop TO "+user)
		}
		for _, query := range []string{"CREATE ROLE viewer", "CREATE ROLE editor",
			"GRANT SELECT ON TABLE articles TO ROLE viewer", "GRANT SELECT ON TABLE articles TO ROLE editor",
			"GRANT INSERT ON TABLE articles TO ROLE editor", "GRANT ROLE viewer ON DATABASE shop TO reader",
			"GRANT ROLE editor ON DATABASE shop TO writer"} {
			execSQL(t, admin, query)
		}
		if err := admin.QueryRowContext(ctx, "SELECT role_id FROM kitdb_catalog.roles WHERE role_name='viewer'").Scan(&roleID); err != nil || roleID == "" {
			t.Fatalf("source local role identity: %q: %v", roleID, err)
		}
	})
	stage("wrong-password-28P01", func(t *testing.T) {
		bad := authorizationClient(t, ctx, server, "reader", "wrong-fixture-password")
		requireAuthorizationState(t, bad.PingContext(ctx), "28P01")
	})
	reader := authorizationClient(t, ctx, server, "reader", password)
	writer := authorizationClient(t, ctx, server, "writer", password)
	stage("read-write-schema-permissions-42501", func(t *testing.T) {
		authorizationRows(t, ctx, reader, []string{"public"})
		deny(t, reader, "INSERT INTO articles VALUES(2,'forbidden')")
		deny(t, reader, "CREATE TABLE forbidden(id BIGINT PRIMARY KEY)")
		deny(t, writer, "CREATE TABLE forbidden(id BIGINT PRIMARY KEY)")
		execSQL(t, writer, "INSERT INTO articles VALUES(2,'writer-ack')")
		authorizationRows(t, ctx, reader, []string{"public", "writer-ack"})
	})
	_ = reader.Close()
	_ = writer.Close()
	_ = admin.Close()
	server.stop(t)
	cli(t, "", "auth-list", root)
	server2 := startAuthorizationServer(t, ctx, binary("kitdbpg"), root)
	stage("restart-retains-authorization", func(t *testing.T) {
		r := authorizationClient(t, ctx, server2, "reader", password)
		w := authorizationClient(t, ctx, server2, "writer", password)
		authorizationRows(t, ctx, r, []string{"public", "writer-ack"})
		deny(t, r, "INSERT INTO articles VALUES(3,'forbidden')")
		execSQL(t, w, "INSERT INTO articles VALUES(3,'restart-ack')")
		authorizationRows(t, ctx, r, []string{"public", "writer-ack", "restart-ack"})
	})
	server2.stop(t)
	newRoot := filepath.Join(dir, "restored")
	restored := filepath.Join(newRoot, "shop", "data.kitdb")
	stage("independent-verified-restore", func(t *testing.T) {
		anchor := filepath.Join(dir, "backup.kitdb")
		if err := os.MkdirAll(filepath.Dir(restored), 0700); err != nil {
			t.Fatal(err)
		}
		for _, args := range [][]string{{"verify", source}, {"backup", source, anchor}, {"init-root", newRoot},
			{"restore", anchor, restored}, {"verify", restored}, {"register-database", newRoot, "shop", "shop"}} {
			cli(t, "", args...)
		}
		cli(t, password, "auth-init", newRoot, "admin")
	})
	server3 := startAuthorizationServer(t, ctx, binary("kitdbpg"), newRoot)
	stage("root-login-does-not-transfer", func(t *testing.T) {
		absent := authorizationClient(t, ctx, server3, "reader", password)
		requireAuthorizationState(t, absent.PingContext(ctx), "28P01")
	})
	operator := authorizationClient(t, ctx, server3, "admin", password)
	stage("portable-role-without-mapping-denied", func(t *testing.T) {
		authorizationRows(t, ctx, operator, []string{"public", "writer-ack", "restart-ack"})
		var restoredID string
		if err := operator.QueryRowContext(ctx, "SELECT role_id FROM kitdb_catalog.roles WHERE role_name='viewer'").Scan(&restoredID); err != nil || restoredID != roleID {
			t.Fatalf("restored role identity %q, want %q: %v", restoredID, roleID, err)
		}
		execSQL(t, operator, "CREATE USER reader WITH PASSWORD $1", password)
		execSQL(t, operator, "GRANT CONNECT ON DATABASE shop TO reader")
		connectOnly := authorizationClient(t, ctx, server3, "reader", password)
		deny(t, connectOnly, "SELECT COUNT(*) FROM articles")
	})
	stage("explicit-remapping-and-writable-restore", func(t *testing.T) {
		execSQL(t, operator, "GRANT ROLE viewer ON DATABASE shop TO reader")
		r := authorizationClient(t, ctx, server3, "reader", password)
		authorizationRows(t, ctx, r, []string{"public", "writer-ack", "restart-ack"})
		deny(t, r, "INSERT INTO articles VALUES(4,'forbidden')")
		execSQL(t, operator, "INSERT INTO articles VALUES(4,'restored-writable')")
		authorizationRows(t, ctx, r, []string{"public", "writer-ack", "restart-ack", "restored-writable"})
	})
	_ = operator.Close()
	server3.stop(t)
	stage("processes-reaped-and-offline-ownership-reacquired", func(t *testing.T) {
		for _, args := range [][]string{{"root-catalog", root}, {"root-catalog", newRoot}, {"verify", source}, {"verify", restored}} {
			cli(t, "", args...)
		}
	})
}

const commandFormatForTest = "kitdb-cli/v1"
