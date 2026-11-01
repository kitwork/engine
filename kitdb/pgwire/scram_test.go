package pgwire

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lib/pq"
)

// scramTestIterations keeps the tests fast; one test uses the real 600000 once.
const scramTestIterations = 4096

// scramTestAuthenticator speaks SCRAM-SHA-256 only: its cleartext Authenticate always refuses, so a client that
// gets in went through SCRAM. It records which users it was asked about and which sessions it opened.
type scramTestAuthenticator struct {
	verifiers map[string]SCRAMVerifier

	mutex  sync.Mutex
	asked  []string
	opened []string
}

func (authenticator *scramTestAuthenticator) Authenticate(context.Context, Startup, string) (Session, error) {
	return nil, NewError("28000", "this authenticator speaks SCRAM-SHA-256 only")
}

func (authenticator *scramTestAuthenticator) SCRAMVerifier(_ context.Context, startup Startup) (SCRAMVerifier, bool, error) {
	authenticator.mutex.Lock()
	defer authenticator.mutex.Unlock()
	authenticator.asked = append(authenticator.asked, startup.User())
	verifier, found := authenticator.verifiers[startup.User()]
	return verifier, found, nil
}

func (authenticator *scramTestAuthenticator) OpenSession(_ context.Context, startup Startup) (Session, error) {
	authenticator.mutex.Lock()
	defer authenticator.mutex.Unlock()
	authenticator.opened = append(authenticator.opened, startup.User())
	return answerSession{}, nil
}

func (authenticator *scramTestAuthenticator) record() (asked, opened []string) {
	authenticator.mutex.Lock()
	defer authenticator.mutex.Unlock()
	return append([]string(nil), authenticator.asked...), append([]string(nil), authenticator.opened...)
}

// cleartextTestAuthenticator is an ordinary Authenticator, not a SCRAMAuthenticator.
type cleartextTestAuthenticator struct{ password string }

func (authenticator cleartextTestAuthenticator) Authenticate(_ context.Context, startup Startup, password string) (Session, error) {
	if password != authenticator.password {
		return nil, NewError("28P01", "password authentication failed for user \""+startup.User()+"\"")
	}
	return answerSession{}, nil
}

// answerSession answers every statement with one text column "answer" holding 42.
type answerSession struct{}

func (answerSession) Execute(context.Context, string, []Parameter) (Result, error) {
	return Result{
		Columns:    []Column{{Name: "answer"}},
		Rows:       [][]Field{{{Data: []byte("42")}}},
		CommandTag: "SELECT 1",
	}, nil
}

func (answerSession) Close() error { return nil }

func scramTestVerifier(t *testing.T, password string, iterations int) SCRAMVerifier {
	t.Helper()
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		t.Fatal(err)
	}
	saltedPassword, err := pbkdf2.Key(sha256.New, password, salt, iterations, sha256.Size)
	if err != nil {
		t.Fatal(err)
	}
	return SCRAMVerifierFromSaltedPassword(salt, iterations, saltedPassword)
}

func newSCRAMTestAuthenticator(t *testing.T) *scramTestAuthenticator {
	t.Helper()
	return &scramTestAuthenticator{verifiers: map[string]SCRAMVerifier{
		"alice": scramTestVerifier(t, "pencil", scramTestIterations),
	}}
}

// serveForTest runs server on 127.0.0.1:0 until the test ends and returns its address.
func serveForTest(t *testing.T, server Server) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
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

// queryWithPQ connects with lib/pq as user and password and reads the answer of one statement.
func queryWithPQ(t *testing.T, address, user, password string) (string, error) {
	t.Helper()
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatal(err)
	}
	connector, err := pq.NewConnector(fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=shop sslmode=disable connect_timeout=30", host, port, user, password))
	if err != nil {
		t.Fatal(err)
	}
	database := sql.OpenDB(connector)
	defer database.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	var answer string
	err = database.QueryRowContext(ctx, "SELECT answer").Scan(&answer)
	return answer, err
}

func requirePasswordFailure(t *testing.T, err error, user string) {
	t.Helper()
	var protocolErr *pq.Error
	if !errors.As(err, &protocolErr) {
		t.Fatalf("error = %v (%T), want a PostgreSQL error 28P01", err, err)
	}
	want := "password authentication failed for user \"" + user + "\""
	if protocolErr.Code != "28P01" || protocolErr.Message != want {
		t.Fatalf("error = %s %q, want 28P01 %q", protocolErr.Code, protocolErr.Message, want)
	}
}

// TestSCRAMVerifierFromSaltedPasswordMatchesRFC7677 derives the verifier of RFC 7677's example (user "user",
// password "pencil") and checks it against the RFC's messages: the RFC's proof verifies with StoredKey and
// ServerKey signs the RFC's server signature.
//
// CONTROL: SCRAMVerifierFromSaltedPassword keying the client key with "Client key" instead of "Client Key" fails
// with "the RFC's proof does not verify against StoredKey"; with "Server key" for "Server Key", "server signature
// = …, want 6rriTRBi23WpRR/wtup+mMhUZUn/dB5nLTJRsjl95G4=".
func TestSCRAMVerifierFromSaltedPasswordMatchesRFC7677(t *testing.T) {
	salt, err := base64.StdEncoding.DecodeString("W22ZaJ0SNY7soEsUEjb6gQ==")
	if err != nil {
		t.Fatal(err)
	}
	saltedPassword, err := pbkdf2.Key(sha256.New, "pencil", salt, 4096, sha256.Size)
	if err != nil {
		t.Fatal(err)
	}
	verifier := SCRAMVerifierFromSaltedPassword(salt, 4096, saltedPassword)
	if verifier.Iterations != 4096 || string(verifier.Salt) != string(salt) {
		t.Fatalf("verifier salt and iterations = %x %d", verifier.Salt, verifier.Iterations)
	}

	const nonce = "rOprNGfwEbeRWgbNEkqO%hvYDpWUa2RaTCAfuxFIlj)hNlF$k0"
	authMessage := "n=user,r=rOprNGfwEbeRWgbNEkqO," +
		"r=" + nonce + ",s=W22ZaJ0SNY7soEsUEjb6gQ==,i=4096," +
		"c=biws,r=" + nonce
	proof, err := base64.StdEncoding.DecodeString("dHzbZapWIk4jUhN+Ute9ytag9zjfMHgsqmmiz7AndVQ=")
	if err != nil {
		t.Fatal(err)
	}
	clientSignature := scramHMAC(verifier.StoredKey[:], authMessage)
	clientKey := make([]byte, len(proof))
	for index := range clientKey {
		clientKey[index] = proof[index] ^ clientSignature[index]
	}
	if storedKey := sha256.Sum256(clientKey); !hmac.Equal(storedKey[:], verifier.StoredKey[:]) {
		t.Fatal("the RFC's proof does not verify against StoredKey")
	}
	serverSignature := base64.StdEncoding.EncodeToString(scramHMAC(verifier.ServerKey[:], authMessage))
	if serverSignature != "6rriTRBi23WpRR/wtup+mMhUZUn/dB5nLTJRsjl95G4=" {
		t.Fatalf("server signature = %s, want 6rriTRBi23WpRR/wtup+mMhUZUn/dB5nLTJRsjl95G4=", serverSignature)
	}
}

// TestSCRAMExchangeFollowsRFC7677 plays RFC 7677's example through the server's side of the exchange, with the
// RFC's server nonce: the server-first and server-final messages must be the RFC's, byte for byte. The same
// exchange on a mock verifier (an unknown user) must fail even with a proof that matches it.
//
// CONTROL: building the server-first message with ",i=" before ",s=" fails with "server-first = …, want
// r=rOprNGfwEbeRWgbNEkqO%hvYDpWUa2RaTCAfuxFIlj)hNlF$k0,s=W22ZaJ0SNY7soEsUEjb6gQ==,i=4096"; dropping
// "|| !exchange.found" from the proof check fails with "a mock verifier authenticated a proof".
func TestSCRAMExchangeFollowsRFC7677(t *testing.T) {
	salt, err := base64.StdEncoding.DecodeString("W22ZaJ0SNY7soEsUEjb6gQ==")
	if err != nil {
		t.Fatal(err)
	}
	saltedPassword, err := pbkdf2.Key(sha256.New, "pencil", salt, 4096, sha256.Size)
	if err != nil {
		t.Fatal(err)
	}
	verifier := SCRAMVerifierFromSaltedPassword(salt, 4096, saltedPassword)
	const (
		clientFirst = "n,,n=user,r=rOprNGfwEbeRWgbNEkqO"
		serverFirst = "r=rOprNGfwEbeRWgbNEkqO%hvYDpWUa2RaTCAfuxFIlj)hNlF$k0,s=W22ZaJ0SNY7soEsUEjb6gQ==,i=4096"
		clientFinal = "c=biws,r=rOprNGfwEbeRWgbNEkqO%hvYDpWUa2RaTCAfuxFIlj)hNlF$k0," +
			"p=dHzbZapWIk4jUhN+Ute9ytag9zjfMHgsqmmiz7AndVQ="
		serverFinal = "v=6rriTRBi23WpRR/wtup+mMhUZUn/dB5nLTJRsjl95G4="
	)

	for _, found := range []bool{true, false} {
		exchange := scramExchange{verifier: verifier, found: found, serverNonce: "%hvYDpWUa2RaTCAfuxFIlj)hNlF$k0"}
		first, err := exchange.receiveClientFirst([]byte(clientFirst))
		if err != nil {
			t.Fatal(err)
		}
		if string(first) != serverFirst {
			t.Fatalf("server-first = %s, want %s", first, serverFirst)
		}
		final, authenticated, err := exchange.receiveClientFinal([]byte(clientFinal))
		if err != nil {
			t.Fatal(err)
		}
		if !found {
			if authenticated || final != nil {
				t.Fatalf("a mock verifier authenticated a proof (server-final %q)", final)
			}
			continue
		}
		if !authenticated || string(final) != serverFinal {
			t.Fatalf("server-final = %q authenticated %v, want %s", final, authenticated, serverFinal)
		}
	}
}

// TestSCRAMMockIterationsDefault: an unknown user is announced the iteration count accounts are made with.
//
// CONTROL: removing the SCRAMMockIterations default from withDefaults fails with "SCRAMMockIterations = 0, want
// 600000".
func TestSCRAMMockIterationsDefault(t *testing.T) {
	if iterations := (Server{}).withDefaults().SCRAMMockIterations; iterations != 600000 {
		t.Fatalf("SCRAMMockIterations = %d, want 600000", iterations)
	}
	if iterations := (Server{SCRAMMockIterations: 4096}).withDefaults().SCRAMMockIterations; iterations != 4096 {
		t.Fatalf("SCRAMMockIterations = %d, want 4096", iterations)
	}
}

// TestSCRAMWithLibPQ connects with lib/pq, which speaks SCRAM-SHA-256 and checks the server's signature, through a
// SCRAM-only authenticator: the right password gets in and a statement answers; a wrong password and an unknown
// user both fail with the same 28P01, and no session is opened for either. One user has the real 600000
// iterations, to show lib/pq copes with them.
//
// CONTROL: signing the server-final message with StoredKey instead of ServerKey fails "right password" and
// "600000 iterations" with "pq: SCRAM-SHA-256 error: cannot authenticate SCRAM-SHA-256 server signature: …";
// making receiveClientFinal accept every proof of a known user fails "wrong password" with "error = pq: SCRAM-SHA-256
// error: cannot authenticate SCRAM-SHA-256 server signature: … want a PostgreSQL error 28P01" (the server let it in,
// lib/pq then caught the signature of the real password) and the test with "sessions opened for [\"alice\" \"bob\"
// \"alice\"], want alice and bob only".
func TestSCRAMWithLibPQ(t *testing.T) {
	authenticator := newSCRAMTestAuthenticator(t)
	authenticator.verifiers["bob"] = scramTestVerifier(t, "correct-horse", 600000)
	address := serveForTest(t, Server{
		Authenticator: authenticator, SCRAMMockIterations: scramTestIterations,
	})

	t.Run("right password", func(t *testing.T) {
		answer, err := queryWithPQ(t, address, "alice", "pencil")
		if err != nil {
			t.Fatal(err)
		}
		if answer != "42" {
			t.Fatalf("answer = %q, want 42", answer)
		}
	})
	t.Run("600000 iterations", func(t *testing.T) {
		answer, err := queryWithPQ(t, address, "bob", "correct-horse")
		if err != nil {
			t.Fatal(err)
		}
		if answer != "42" {
			t.Fatalf("answer = %q, want 42", answer)
		}
	})
	t.Run("wrong password", func(t *testing.T) {
		answer, err := queryWithPQ(t, address, "alice", "pencils")
		if err == nil {
			t.Fatalf("wrong password connected and answered %q", answer)
		}
		requirePasswordFailure(t, err, "alice")
	})
	t.Run("unknown user", func(t *testing.T) {
		answer, err := queryWithPQ(t, address, "mallory", "pencil")
		if err == nil {
			t.Fatalf("unknown user connected and answered %q", answer)
		}
		requirePasswordFailure(t, err, "mallory")
	})

	asked, opened := authenticator.record()
	if strings.Join(opened, " ") != "alice bob" {
		t.Fatalf("sessions opened for %q, want alice and bob only", opened)
	}
	if !strings.Contains(" "+strings.Join(asked, " ")+" ", " mallory ") {
		t.Fatalf("the authenticator was asked about %q, never mallory", asked)
	}
}

// scramClient drives SCRAM-SHA-256 by hand over a raw connection.
type scramClient struct {
	*wireClient
	clientFirstBare string
	serverFirst     string
	nonce           string
	salt            []byte
	iterations      int
}

// startSCRAM sends the startup for user and requires AuthenticationSASL offering SCRAM-SHA-256 alone.
func startSCRAM(t *testing.T, address, user string) *scramClient {
	t.Helper()
	conn, err := net.DialTimeout("tcp", address, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	client := &scramClient{wireClient: &wireClient{conn: conn, reader: bufio.NewReader(conn)}}
	var startup []byte
	startup = binary.BigEndian.AppendUint32(startup, ProtocolVersion30)
	for _, pair := range [][2]string{{"user", user}, {"database", "shop"}} {
		startup = append(startup, pair[0]...)
		startup = append(startup, 0)
		startup = append(startup, pair[1]...)
		startup = append(startup, 0)
	}
	startup = append(startup, 0)
	client.write(t, 0, startup)
	kind, body := client.read(t)
	if kind != 'R' || len(body) < 4 || binary.BigEndian.Uint32(body) != authenticationSASL {
		t.Fatalf("reply to startup = %q %q, want AuthenticationSASL", kind, body)
	}
	if mechanisms := string(body[4:]); mechanisms != "SCRAM-SHA-256\x00\x00" {
		t.Fatalf("mechanisms = %q, want SCRAM-SHA-256 alone", mechanisms)
	}
	return client
}

// sendClientFirst sends the SASLInitialResponse and returns the server's reply.
func (client *scramClient) sendClientFirst(t *testing.T, mechanism, message string) (byte, []byte) {
	t.Helper()
	var body []byte
	body = append(body, mechanism...)
	body = append(body, 0)
	body = binary.BigEndian.AppendUint32(body, uint32(len(message)))
	body = append(body, message...)
	client.write(t, 'p', body)
	return client.read(t)
}

// begin sends a client-first-message with gs2Header and requires the server-first-message.
func (client *scramClient) begin(t *testing.T, gs2Header string) {
	t.Helper()
	client.clientFirstBare = "n=,r=fyko+d2lbbFgONRv9qkxdawL"
	kind, body := client.sendClientFirst(t, "SCRAM-SHA-256", gs2Header+client.clientFirstBare)
	if kind != 'R' || len(body) < 4 || binary.BigEndian.Uint32(body) != authenticationSASLContinue {
		t.Fatalf("reply to client-first = %q %q, want AuthenticationSASLContinue", kind, body)
	}
	client.serverFirst = string(body[4:])
	fields := strings.Split(client.serverFirst, ",")
	if len(fields) != 3 || !strings.HasPrefix(fields[0], "r=fyko+d2lbbFgONRv9qkxdawL") ||
		len(fields[0]) < len("r=fyko+d2lbbFgONRv9qkxdawL")+24 ||
		!strings.HasPrefix(fields[1], "s=") || !strings.HasPrefix(fields[2], "i=") {
		t.Fatalf("server-first = %q", client.serverFirst)
	}
	client.nonce = fields[0][2:]
	salt, err := base64.StdEncoding.DecodeString(fields[1][2:])
	if err != nil {
		t.Fatal(err)
	}
	client.salt = salt
	if client.iterations, err = strconv.Atoi(fields[2][2:]); err != nil {
		t.Fatal(err)
	}
}

// finish sends "c=<binding>,r=<nonce>,p=<proof>", the proof computed for password over that very message, and
// returns the server's reply.
func (client *scramClient) finish(t *testing.T, binding, nonce, password string) (byte, []byte) {
	t.Helper()
	withoutProof := "c=" + binding + ",r=" + nonce
	client.write(t, 'p', []byte(withoutProof+",p="+client.proof(t, password, withoutProof)))
	return client.read(t)
}

func (client *scramClient) authMessage(withoutProof string) string {
	return client.clientFirstBare + "," + client.serverFirst + "," + withoutProof
}

func (client *scramClient) saltedPassword(t *testing.T, password string) []byte {
	t.Helper()
	saltedPassword, err := pbkdf2.Key(sha256.New, password, client.salt, client.iterations, sha256.Size)
	if err != nil {
		t.Fatal(err)
	}
	return saltedPassword
}

func (client *scramClient) proof(t *testing.T, password, withoutProof string) string {
	t.Helper()
	saltedPassword := client.saltedPassword(t, password)
	clientKey := scramHMAC(saltedPassword, "Client Key")
	storedKey := sha256.Sum256(clientKey)
	clientSignature := scramHMAC(storedKey[:], client.authMessage(withoutProof))
	for index := range clientKey {
		clientKey[index] ^= clientSignature[index]
	}
	return base64.StdEncoding.EncodeToString(clientKey)
}

// requireRefused requires an ErrorResponse with code and message, then the server closing the connection.
func (client *scramClient) requireRefused(t *testing.T, kind byte, body []byte, code, message string) {
	t.Helper()
	if kind != 'E' {
		t.Fatalf("reply = %q %q, want ErrorResponse %s", kind, body, code)
	}
	if got := wireErrorCode(body); got != code {
		t.Fatalf("error code = %q, want %s (%q)", got, code, body)
	}
	if got := wireErrorMessage(body); message != "" && got != message {
		t.Fatalf("error message = %q, want %q", got, message)
	}
	if _, err := client.reader.ReadByte(); !errors.Is(err, io.EOF) {
		t.Fatalf("after the refusal the connection read %v, want EOF", err)
	}
}

// TestSCRAMByHand drives the exchange over a raw connection: the right proof is answered with the server's
// signature and the session; each tampered client-final-message is refused even though its proof matches what was
// sent; channel binding is refused.
//
// CONTROL: in receiveClientFinal, removing the nonce check fails "wrong nonce" with "reply = 'R' \"…v=…\", want
// ErrorResponse 08P01"; removing the channel-binding check fails "wrong channel binding" the same way; accepting
// every proof of a known user fails "wrong proof" with "reply = 'R' \"…v=…\", want ErrorResponse 28P01"; treating
// "p=…" like "n" in receiveClientFirst fails "channel binding requested" with "reply = 'R' \"…r=…,s=…,i=4096\",
// want ErrorResponse 08P01"; refusing the flag "y" fails "y header" with "reply to client-first = 'E' … want
// AuthenticationSASLContinue"; removing the mechanism check in authenticateSCRAM fails "PLUS mechanism" with "error
// message = \"malformed SCRAM message: the client requires channel binding, …\", want \"client selected an invalid
// SASL authentication mechanism\"". Each also fails the closing count of opened sessions.
func TestSCRAMByHand(t *testing.T) {
	authenticator := newSCRAMTestAuthenticator(t)
	address := serveForTest(t, Server{Authenticator: authenticator, SCRAMMockIterations: scramTestIterations})

	for _, scenario := range []struct {
		name      string
		gs2Header string
		binding   string
	}{
		{name: "n header", gs2Header: "n,,", binding: "biws"},
		{name: "y header", gs2Header: "y,,", binding: "eSws"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			client := startSCRAM(t, address, "alice")
			client.begin(t, scenario.gs2Header)
			if client.iterations != scramTestIterations || len(client.salt) != 16 {
				t.Fatalf("salt %x iterations %d, want alice's", client.salt, client.iterations)
			}
			withoutProof := "c=" + scenario.binding + ",r=" + client.nonce
			kind, body := client.finish(t, scenario.binding, client.nonce, "pencil")
			if kind != 'R' || len(body) < 4 || binary.BigEndian.Uint32(body) != authenticationSASLFinal {
				t.Fatalf("reply to client-final = %q %q, want AuthenticationSASLFinal", kind, body)
			}
			serverKey := scramHMAC(client.saltedPassword(t, "pencil"), "Server Key")
			want := "v=" + base64.StdEncoding.EncodeToString(scramHMAC(serverKey, client.authMessage(withoutProof)))
			if got := string(body[4:]); got != want {
				t.Fatalf("server-final = %q, want %q", got, want)
			}
			kind, body = client.read(t)
			if kind != 'R' || string(body) != "\x00\x00\x00\x00" {
				t.Fatalf("after server-final = %q %q, want AuthenticationOk", kind, body)
			}
			for kind != 'Z' {
				kind, body = client.read(t)
				if kind == 'E' {
					t.Fatalf("startup rejected: %q", body)
				}
			}
			kind, body = client.query(t, "SELECT answer")
			if kind != 'T' {
				t.Fatalf("reply to query = %q %q, want RowDescription", kind, body)
			}
		})
	}

	t.Run("wrong nonce", func(t *testing.T) {
		client := startSCRAM(t, address, "alice")
		client.begin(t, "n,,")
		tampered := client.nonce[:len(client.nonce)-1] + "!"
		kind, body := client.finish(t, "biws", tampered, "pencil")
		client.requireRefused(t, kind, body, "08P01", "invalid SCRAM response: the nonce does not match")
	})
	t.Run("wrong channel binding", func(t *testing.T) {
		client := startSCRAM(t, address, "alice")
		client.begin(t, "n,,")
		kind, body := client.finish(t, "eSws", client.nonce, "pencil")
		client.requireRefused(t, kind, body, "08P01", "unexpected SCRAM channel-binding attribute in client-final-message")
	})
	t.Run("wrong proof", func(t *testing.T) {
		client := startSCRAM(t, address, "alice")
		client.begin(t, "n,,")
		kind, body := client.finish(t, "biws", client.nonce, "pencils")
		client.requireRefused(t, kind, body, "28P01", "password authentication failed for user \"alice\"")
	})
	t.Run("channel binding requested", func(t *testing.T) {
		client := startSCRAM(t, address, "alice")
		kind, body := client.sendClientFirst(t, "SCRAM-SHA-256", "p=tls-server-end-point,,n=,r=fyko+d2lbbFgONRv9qkxdawL")
		client.requireRefused(t, kind, body, "08P01", "")
	})
	t.Run("PLUS mechanism", func(t *testing.T) {
		client := startSCRAM(t, address, "alice")
		kind, body := client.sendClientFirst(t, "SCRAM-SHA-256-PLUS", "p=tls-server-end-point,,n=,r=fyko+d2lbbFgONRv9qkxdawL")
		client.requireRefused(t, kind, body, "08P01", "client selected an invalid SASL authentication mechanism")
	})

	if _, opened := authenticator.record(); strings.Join(opened, " ") != "alice alice" {
		t.Fatalf("sessions opened for %q, want the two successful exchanges only", opened)
	}
}

// TestSCRAMUnknownUserRunsTheExchange: an unknown user gets a server-first message like a real user's (a 16-byte
// salt, the same for the same name, the real iteration count) and fails only at the proof, with a wrong password's
// error; the authenticator was asked about the user and opened no session.
//
// CONTROL: refusing an unknown user with 28P01 before AuthenticationSASL (in authenticateSCRAM, right after the
// verifier lookup) fails with "reply to startup = 'E' \"…C28P01…\", want AuthenticationSASL". TestSCRAMWithLibPQ
// stays green under that control (lib/pq sees the same 28P01), which is why this test drives the protocol by hand.
func TestSCRAMUnknownUserRunsTheExchange(t *testing.T) {
	authenticator := newSCRAMTestAuthenticator(t)
	address := serveForTest(t, Server{Authenticator: authenticator, SCRAMMockIterations: scramTestIterations})

	attempt := func(user string) []byte {
		client := startSCRAM(t, address, user)
		client.begin(t, "n,,")
		if client.iterations != scramTestIterations || len(client.salt) != 16 {
			t.Fatalf("%s: salt %x iterations %d, want a 16-byte salt and %d", user, client.salt, client.iterations, scramTestIterations)
		}
		kind, body := client.finish(t, "biws", client.nonce, "pencil")
		client.requireRefused(t, kind, body, "28P01", "password authentication failed for user \""+user+"\"")
		return client.salt
	}
	first := attempt("mallory")
	again := attempt("mallory")
	other := attempt("trudy")
	if string(first) != string(again) {
		t.Fatalf("mallory's salt changed between attempts: %x then %x", first, again)
	}
	if string(first) == string(other) {
		t.Fatalf("mallory and trudy share the salt %x", first)
	}

	asked, opened := authenticator.record()
	if strings.Join(asked, " ") != "mallory mallory trudy" || len(opened) != 0 {
		t.Fatalf("asked about %q, opened %q; want asked about each user and no session", asked, opened)
	}
}

// TestCleartextAuthenticatorKeepsCleartext: an Authenticator that is not a SCRAMAuthenticator still gets the
// cleartext exchange (AuthenticationCleartextPassword), and lib/pq still connects through it.
//
// CONTROL: sending code 10 instead of 3 in authenticateCleartext fails with "first authentication request = 10, want
// 3 (AuthenticationCleartextPassword)".
func TestCleartextAuthenticatorKeepsCleartext(t *testing.T) {
	address := serveForTest(t, Server{Authenticator: cleartextTestAuthenticator{password: "secret"}})

	conn, err := net.DialTimeout("tcp", address, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	client := &wireClient{conn: conn, reader: bufio.NewReader(conn)}
	var startup []byte
	startup = binary.BigEndian.AppendUint32(startup, ProtocolVersion30)
	startup = append(startup, "user\x00carol\x00database\x00shop\x00\x00"...)
	client.write(t, 0, startup)
	kind, body := client.read(t)
	if kind != 'R' || len(body) != 4 || binary.BigEndian.Uint32(body) != 3 {
		code := uint32(0)
		if len(body) >= 4 {
			code = binary.BigEndian.Uint32(body)
		}
		t.Fatalf("first authentication request = %d, want 3 (AuthenticationCleartextPassword) (%q %q)", code, kind, body)
	}

	answer, err := queryWithPQ(t, address, "carol", "secret")
	if err != nil {
		t.Fatal(err)
	}
	if answer != "42" {
		t.Fatalf("answer = %q, want 42", answer)
	}
	_, err = queryWithPQ(t, address, "carol", "wrong")
	requirePasswordFailure(t, err, "carol")
}

// wireErrorMessage extracts the message field from an ErrorResponse body.
func wireErrorMessage(body []byte) string {
	for len(body) > 0 && body[0] != 0 {
		field := body[0]
		end := 1
		for end < len(body) && body[end] != 0 {
			end++
		}
		if field == 'M' {
			return string(body[1:end])
		}
		if end+1 > len(body) {
			break
		}
		body = body[end+1:]
	}
	return ""
}
