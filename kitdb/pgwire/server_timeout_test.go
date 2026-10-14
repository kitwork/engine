package pgwire

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

// slowSession waits for the query context or a fixed delay before answering,
// so the reply is written long after the last write deadline was armed.
type slowSession struct {
	delay time.Duration
	err   error
}

func (session *slowSession) Execute(ctx context.Context, query string, _ []Parameter) (Result, error) {
	if session.delay > 0 {
		select {
		case <-time.After(session.delay):
		case <-ctx.Done():
			return Result{}, ctx.Err()
		}
	}
	if session.err != nil {
		return Result{}, session.err
	}
	return Result{CommandTag: "SELECT 1"}, nil
}

func (session *slowSession) Close() error { return nil }

type slowAuthenticator struct{ session *slowSession }

func (authenticator slowAuthenticator) Authenticate(context.Context, Startup, string) (Session, error) {
	return authenticator.session, nil
}

// TestLateErrorReachesClientAfterWriteTimeout reproduces a query whose failure
// arrives after Server.WriteTimeout has elapsed since the previous write. The
// error response must still reach the client and the connection must stay
// usable; an expired write deadline must not turn a slow failure into EOF.
func TestLateErrorReachesClientAfterWriteTimeout(t *testing.T) {
	for _, scenario := range []struct {
		name    string
		session *slowSession
		code    string
	}{
		{
			name:    "query timeout",
			session: &slowSession{delay: time.Minute},
			code:    "57014",
		},
		{
			name:    "late execution error",
			session: &slowSession{delay: 120 * time.Millisecond, err: NewError("42P01", "no such table")},
			code:    "42P01",
		},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			address := startSlowServer(t, scenario.session, Server{
				MaxConnections: 2,
				WriteTimeout:   40 * time.Millisecond,
				QueryTimeout:   200 * time.Millisecond,
				IdleTimeout:    5 * time.Second,
			})
			client := dialWire(t, address)
			kind, body := client.query(t, "SELECT slow")
			if kind != 'E' {
				t.Fatalf("first reply = %q %q, want an ErrorResponse", kind, body)
			}
			if code := wireErrorCode(body); code != scenario.code {
				t.Fatalf("error code = %q, want %q (%q)", code, scenario.code, body)
			}
			client.expect(t, 'Z')

			// The connection survives: a fast statement on the same socket works.
			scenario.session.delay = 0
			scenario.session.err = nil
			kind, body = client.query(t, "SELECT 1")
			if kind != 'C' || string(body) != "SELECT 1\x00" {
				t.Fatalf("follow-up reply = %q %q, want CommandComplete", kind, body)
			}
			client.expect(t, 'Z')
		})
	}
}

func startSlowServer(t *testing.T, session *slowSession, server Server) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server.Authenticator = slowAuthenticator{session: session}
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
			t.Error("pgwire did not stop")
		}
	})
	return listener.Addr().String()
}

type wireClient struct {
	conn   net.Conn
	reader *bufio.Reader
}

func dialWire(t *testing.T, address string) *wireClient {
	t.Helper()
	conn, err := net.DialTimeout("tcp", address, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	client := &wireClient{conn: conn, reader: bufio.NewReader(conn)}
	var startup []byte
	startup = binary.BigEndian.AppendUint32(startup, ProtocolVersion30)
	for _, pair := range [][2]string{{"user", "kitdb"}, {"database", "slow"}} {
		startup = append(startup, pair[0]...)
		startup = append(startup, 0)
		startup = append(startup, pair[1]...)
		startup = append(startup, 0)
	}
	startup = append(startup, 0)
	client.write(t, 0, startup)
	for {
		kind, body := client.read(t)
		if kind == 'R' && len(body) == 4 && binary.BigEndian.Uint32(body) == 3 {
			client.write(t, 'p', append([]byte("secret"), 0))
			continue
		}
		if kind == 'Z' {
			return client
		}
		if kind == 'E' {
			t.Fatalf("startup rejected: %q", body)
		}
	}
}

func (client *wireClient) write(t *testing.T, kind byte, body []byte) {
	t.Helper()
	var message []byte
	if kind != 0 {
		message = append(message, kind)
	}
	message = binary.BigEndian.AppendUint32(message, uint32(len(body)+4))
	message = append(message, body...)
	if _, err := client.conn.Write(message); err != nil {
		t.Fatal(err)
	}
}

func (client *wireClient) read(t *testing.T) (byte, []byte) {
	t.Helper()
	kind, err := client.reader.ReadByte()
	if err != nil {
		if errors.Is(err, io.EOF) {
			t.Fatal("server closed the connection instead of replying")
		}
		t.Fatal(err)
	}
	var header [4]byte
	if _, err := io.ReadFull(client.reader, header[:]); err != nil {
		t.Fatal(err)
	}
	body := make([]byte, binary.BigEndian.Uint32(header[:])-4)
	if _, err := io.ReadFull(client.reader, body); err != nil {
		t.Fatal(err)
	}
	return kind, body
}

func (client *wireClient) query(t *testing.T, sql string) (byte, []byte) {
	t.Helper()
	client.write(t, 'Q', append([]byte(sql), 0))
	return client.read(t)
}

func (client *wireClient) expect(t *testing.T, want byte) {
	t.Helper()
	if kind, body := client.read(t); kind != want {
		t.Fatalf("reply = %q %q, want %q", kind, body, want)
	}
}

// wireErrorCode extracts the SQLSTATE field from an ErrorResponse body.
func wireErrorCode(body []byte) string {
	for len(body) > 0 && body[0] != 0 {
		field := body[0]
		end := 1
		for end < len(body) && body[end] != 0 {
			end++
		}
		if field == 'C' {
			return string(body[1:end])
		}
		if end+1 > len(body) {
			break
		}
		body = body[end+1:]
	}
	return ""
}
