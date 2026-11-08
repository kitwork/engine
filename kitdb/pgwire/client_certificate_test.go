package pgwire

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/binary"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lib/pq"
)

// Client certificates on the port's TLS (server.go startTLS and checkClientCertificate, tls.go ClientAuthorityFile;
// KITWORK_KITDB.md câu 13.11, 08/11/2026): a certificate added to the password, as PostgreSQL's clientcert=verify-full.

// clientCertificate is a certificate for a client named name, signed by authority, with its key.
func clientCertificate(t *testing.T, authority *testAuthority, name string) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
	template := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: name},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, authority.certificate, &key.PublicKey, authority.key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// writeClientFiles writes certificate and its key as the PEM files a libpq client is given (sslcert, sslkey).
func writeClientFiles(t *testing.T, certificate tls.Certificate) (certificateFile, keyFile string) {
	t.Helper()
	folder := t.TempDir()
	keyDER, err := x509.MarshalECPrivateKey(certificate.PrivateKey.(*ecdsa.PrivateKey))
	if err != nil {
		t.Fatal(err)
	}
	certificateFile, keyFile = filepath.Join(folder, "client.crt"), filepath.Join(folder, "client.key")
	if err := os.WriteFile(certificateFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Certificate[0]}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certificateFile, keyFile
}

// firstReply connects over TLS with config, sends the startup for user and returns the server's first message, or the
// error that ended the connection first (the handshake, or the read after it: TLS 1.3 reports a refused client
// certificate there).
func firstReply(t *testing.T, address, user string, config *tls.Config) (byte, []byte, error) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", address, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	if _, err := conn.Write([]byte{0, 0, 0, 8, 0x04, 0xd2, 0x16, 0x2f}); err != nil {
		t.Fatal(err)
	}
	var answer [1]byte
	if _, err := io.ReadFull(conn, answer[:]); err != nil || answer[0] != 'S' {
		t.Fatalf("answer to the SSLRequest %q, %v, want S", answer[0], err)
	}
	encrypted := tls.Client(conn, config)
	if err := encrypted.Handshake(); err != nil {
		return 0, nil, err
	}
	var startup []byte
	startup = binary.BigEndian.AppendUint32(startup, 0)
	startup = binary.BigEndian.AppendUint32(startup, ProtocolVersion30)
	for _, pair := range [][2]string{{"user", user}, {"database", "shop"}} {
		startup = append(append(append(append(startup, pair[0]...), 0), pair[1]...), 0)
	}
	startup = append(startup, 0)
	binary.BigEndian.PutUint32(startup, uint32(len(startup)))
	if _, err := encrypted.Write(startup); err != nil {
		return 0, nil, err
	}
	reader := bufio.NewReader(encrypted)
	var head [5]byte
	if _, err := io.ReadFull(reader, head[:]); err != nil {
		return 0, nil, err
	}
	body := make([]byte, binary.BigEndian.Uint32(head[1:])-4)
	if _, err := io.ReadFull(reader, body); err != nil {
		return 0, nil, err
	}
	return head[0], body, nil
}

// answerWithCertificate connects with lib/pq (sslmode=require) giving the client certificate files when they are not
// empty, and reads the answer of one statement.
func answerWithCertificate(t *testing.T, address, certificateFile, keyFile string) (string, error) {
	t.Helper()
	parameters := "host=db.example port=5432 user=alice password=pencil dbname=shop sslmode=require connect_timeout=30"
	if certificateFile != "" {
		parameters += fmt.Sprintf(" sslcert=%s sslkey=%s", filepath.ToSlash(certificateFile), filepath.ToSlash(keyFile))
	}
	connector, err := pq.NewConnector(parameters)
	if err != nil {
		t.Fatal(err)
	}
	connector.Dialer(redirectDialer{address})
	database := sql.OpenDB(connector)
	defer database.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var answer string
	err = database.QueryRowContext(ctx, "SELECT answer").Scan(&answer)
	return answer, err
}

// With ClientAuthorities, a client of another machine comes in with a certificate those authorities signed whose
// common name is its user, and its password: the password is still asked. A certificate of another user, of another
// authority, or none is refused, the last two in the TLS handshake, before the startup; lib/pq given the certificate
// files comes in, without them it does not. A client of this machine is not asked for a certificate.
//
// CONTROL (08/11/2026): with startTLS not asking for a certificate, "no certificate" fails "got in to its startup:
// 'E' …28000…, want the handshake refused" (the startup's check still stops it: two walls) and every client with a
// certificate fails "the client's certificate is of \"\""; so does any certificate taken unverified
// (RequireAnyClientCert), and the verified certificate not kept; with the common name not compared, "bob's
// certificate as alice" fails "reply = 'R' …SCRAM-SHA-256…, want 28000 naming both"; with a client of this machine
// asked too, "this machine" fails "remote error: tls: certificate required".
func TestClientCertificates(t *testing.T) {
	serverAuthority := newTestAuthority(t)
	certificate, key := serverAuthority.issue(t, "db.example", time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	pair, err := tls.X509KeyPair(certificate, key)
	if err != nil {
		t.Fatal(err)
	}
	clients := newTestAuthority(t)
	authorities, err := ClientAuthorityFile(clients.file)
	if err != nil {
		t.Fatal(err)
	}
	authenticator := newSCRAMTestAuthenticator(t)
	server := Server{Authenticator: authenticator, SCRAMMockIterations: scramTestIterations, ClientAuthorities: authorities,
		TLS: TLSConfig(func(*tls.ClientHelloInfo) (*tls.Certificate, error) { return &pair, nil })}
	remote, _ := serveRemote(t, server)
	local := serveForTest(t, server)
	alice := clientCertificate(t, clients, "alice")
	bob := clientCertificate(t, clients, "bob")
	stranger := clientCertificate(t, newTestAuthority(t), "alice")
	with := func(certificate *tls.Certificate) *tls.Config {
		config := &tls.Config{InsecureSkipVerify: true}
		if certificate != nil {
			config.Certificates = []tls.Certificate{*certificate}
		}
		return config
	}

	t.Run("alice's certificate and password", func(t *testing.T) {
		client, _, _ := startSCRAMOverTLS(t, remote, "alice", with(&alice))
		client.begin(t, "n,,")
		kind, body := client.finish(t, "biws", client.nonce, "pencil")
		client.requireAuthenticated(t, kind, body, "c=biws,r="+client.nonce, "pencil")
	})
	t.Run("alice's certificate, a wrong password", func(t *testing.T) {
		client, _, _ := startSCRAMOverTLS(t, remote, "alice", with(&alice))
		client.begin(t, "n,,")
		kind, body := client.finish(t, "biws", client.nonce, "pencils")
		client.requireRefused(t, kind, body, "28P01", "password authentication failed for user \"alice\"")
	})
	t.Run("bob's certificate as alice", func(t *testing.T) {
		kind, body, err := firstReply(t, remote, "alice", with(&bob))
		if err != nil || kind != 'E' || wireErrorCode(body) != "28000" ||
			wireErrorMessage(body) != "certificate authentication failed for user \"alice\": the client's certificate is of \"bob\"" {
			t.Fatalf("reply = %q %q, %v, want 28000 naming both", kind, body, err)
		}
	})
	for name, certificate := range map[string]*tls.Certificate{"no certificate": nil, "another authority's certificate": &stranger} {
		t.Run(name, func(t *testing.T) {
			if kind, body, err := firstReply(t, remote, "alice", with(certificate)); err == nil {
				t.Fatalf("got in to its startup: %q %q, want the handshake refused", kind, body)
			}
		})
	}
	t.Run("lib/pq", func(t *testing.T) {
		certificateFile, keyFile := writeClientFiles(t, alice)
		if answer, err := answerWithCertificate(t, remote, certificateFile, keyFile); err != nil || answer != "42" {
			t.Fatalf("lib/pq with alice's certificate files: %q, %v", answer, err)
		}
		if answer, err := answerWithCertificate(t, remote, "", ""); err == nil {
			t.Fatalf("lib/pq without a certificate got in: %q", answer)
		}
	})
	t.Run("this machine", func(t *testing.T) {
		client, _, _ := startSCRAMOverTLS(t, local, "alice", nil)
		client.begin(t, "n,,")
		kind, body := client.finish(t, "biws", client.nonce, "pencil")
		client.requireAuthenticated(t, kind, body, "c=biws,r="+client.nonce, "pencil")
	})

	if _, opened := authenticator.record(); strings.Join(opened, " ") != "alice alice alice" {
		t.Fatalf("sessions opened for %q, want alice by hand, by lib/pq and on this machine", opened)
	}
}

// ClientAuthorityFile reads a PEM file of certificates, and refuses a file with none or no file.
func TestClientAuthorityFile(t *testing.T) {
	authority := newTestAuthority(t)
	if authorities, err := ClientAuthorityFile(authority.file); err != nil || authorities == nil {
		t.Fatalf("a PEM certificate: %v, %v", authorities, err)
	}
	empty := filepath.Join(t.TempDir(), "empty.pem")
	if err := os.WriteFile(empty, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ClientAuthorityFile(empty); err == nil || !strings.Contains(err.Error(), "no PEM certificate") {
		t.Errorf("a file of no certificate: %v", err)
	}
	if _, err := ClientAuthorityFile(filepath.Join(t.TempDir(), "missing.pem")); err == nil {
		t.Error("a missing file was read")
	}
}
