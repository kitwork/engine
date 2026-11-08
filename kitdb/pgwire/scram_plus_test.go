package pgwire

import (
	"bufio"
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/binary"
	"io"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"
)

// SCRAM-SHA-256-PLUS over the port's TLS (scram.go, tls.go servedCertificate and serverEndPoint; KITWORK_KITDB.md
// câu 13.11, 08/11/2026).

const plusHeader = "p=tls-server-end-point,,"

// offered is a mechanism list as AuthenticationSASL carries it: each name ended by a zero byte, the list by one more.
func offered(names ...string) string {
	var list []byte
	for _, name := range names {
		list = append(append(list, name...), 0)
	}
	return string(append(list, 0))
}

// bindingOf is the c= of a client-final-message: base64 of the gs2 header and the certificate's hash.
func bindingOf(header string, hash []byte) string {
	return base64.StdEncoding.EncodeToString(append([]byte(header), hash...))
}

func ecdsaKey(t *testing.T, curve elliptic.Curve) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(curve, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

// selfSigned is a certificate of name signed by its own key, with algorithm, or when it is 0 the signature Go makes
// with that key (ECDSA P-256: SHA-256, P-384: SHA-384, P-521: SHA-512, Ed25519: no hash).
func selfSigned(t *testing.T, name string, key crypto.Signer, algorithm x509.SignatureAlgorithm) tls.Certificate {
	t.Helper()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(7), Subject: pkix.Name{CommonName: name}, DNSNames: []string{name},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		SignatureAlgorithm: algorithm,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, key.Public(), key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// startSCRAMOverTLS asks for TLS with an SSLRequest, runs the handshake with config (nil: checking nothing of the
// certificate, as sslmode=require), sends the startup for user, and returns the client, the mechanisms offered, and
// the certificate the client was served.
func startSCRAMOverTLS(t *testing.T, address, user string, config *tls.Config) (*scramClient, string, *x509.Certificate) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", address, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	if _, err := conn.Write([]byte{0, 0, 0, 8, 0x04, 0xd2, 0x16, 0x2f}); err != nil {
		t.Fatal(err)
	}
	var answer [1]byte
	if _, err := io.ReadFull(conn, answer[:]); err != nil || answer[0] != 'S' {
		t.Fatalf("answer to the SSLRequest %q, %v, want S", answer[0], err)
	}
	if config == nil {
		config = &tls.Config{InsecureSkipVerify: true}
	}
	encrypted := tls.Client(conn, config)
	if err := encrypted.Handshake(); err != nil {
		t.Fatal(err)
	}
	client := &scramClient{wireClient: &wireClient{conn: encrypted, reader: bufio.NewReader(encrypted)}}
	mechanisms := client.startup(t, user)
	return client, mechanisms, encrypted.ConnectionState().PeerCertificates[0]
}

// requireAuthenticated requires the server-final-message signed for withoutProof and password, AuthenticationOk,
// ReadyForQuery, and an answer to a query.
func (client *scramClient) requireAuthenticated(t *testing.T, kind byte, body []byte, withoutProof, password string) {
	t.Helper()
	if kind != 'R' || len(body) < 4 || binary.BigEndian.Uint32(body) != authenticationSASLFinal {
		t.Fatalf("reply to client-final = %q %q, want AuthenticationSASLFinal", kind, body)
	}
	serverKey := scramHMAC(client.saltedPassword(t, password), "Server Key")
	want := "v=" + base64.StdEncoding.EncodeToString(scramHMAC(serverKey, client.authMessage(withoutProof)))
	if got := string(body[4:]); got != want {
		t.Fatalf("server-final = %q, want %q", got, want)
	}
	kind, body = client.read(t)
	if kind != 'R' || len(body) != 4 || binary.BigEndian.Uint32(body) != 0 {
		t.Fatalf("after server-final = %q %q, want AuthenticationOk", kind, body)
	}
	for kind != 'Z' {
		kind, body = client.read(t)
		if kind == 'E' {
			t.Fatalf("startup rejected: %q", body)
		}
	}
	if kind, body = client.query(t, "SELECT answer"); kind != 'T' {
		t.Fatalf("reply to query = %q %q, want RowDescription", kind, body)
	}
}

// plus runs SCRAM-SHA-256-PLUS with the binding of hash and password, and returns the server's reply and what the
// proof was computed over.
func (client *scramClient) plus(t *testing.T, hash []byte, password string) (byte, []byte, string) {
	t.Helper()
	client.beginWith(t, scramPlusMechanism, plusHeader)
	binding := bindingOf(plusHeader, hash)
	kind, body := client.finish(t, binding, client.nonce, password)
	return kind, body, "c=" + binding + ",r=" + client.nonce
}

// Over TLS the server offers SCRAM-SHA-256-PLUS first, and a client binding its proof to the certificate it was served
// comes in; a client without channel binding still comes in with SCRAM-SHA-256 (lib/pq), and a client that did not ask
// for TLS is offered no PLUS. Every message that says the client and the server disagree about channel binding is
// refused, as PostgreSQL refuses it.
//
// CONTROL (08/11/2026): with startTLS not keeping the certificate's hash, or PLUS left out of the offer, "PLUS" fails
// "mechanisms = …SCRAM-SHA-256…, want …SCRAM-SHA-256-PLUS…"; with receiveClientFinal not comparing the hash, "another
// certificate" and "PLUS without the hash" fail "reply = 'R' …v=…, want ErrorResponse 28000"; with "y" taken whether
// PLUS was offered or not, "y while PLUS is offered" fails "reply = 'R' …r=…, want ErrorResponse 08P01"; so do "PLUS
// with n" with n or y taken under PLUS, "binding data without PLUS" with p= taken under SCRAM-SHA-256 (TestSCRAMByHand
// too), and "tls-unique" with any binding type taken.
func TestSCRAMPlusOverTLS(t *testing.T) {
	authority := newTestAuthority(t)
	certificate, key := authority.issue(t, "db.example", time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	pair, err := tls.X509KeyPair(certificate, key)
	if err != nil {
		t.Fatal(err)
	}
	other := selfSigned(t, "db.example", ecdsaKey(t, elliptic.P256()), 0)
	otherHash := sha256.Sum256(other.Certificate[0])
	authenticator := newSCRAMTestAuthenticator(t)
	address := serveForTest(t, Server{Authenticator: authenticator, SCRAMMockIterations: scramTestIterations,
		TLS: TLSConfig(func(*tls.ClientHelloInfo) (*tls.Certificate, error) { return &pair, nil })})
	const bare = "n=,r=fyko+d2lbbFgONRv9qkxdawL"

	t.Run("PLUS", func(t *testing.T) {
		client, mechanisms, served := startSCRAMOverTLS(t, address, "alice", nil)
		if want := offered(scramPlusMechanism, scramMechanism); mechanisms != want {
			t.Fatalf("mechanisms = %q, want %q", mechanisms, want)
		}
		if served.SignatureAlgorithm != x509.ECDSAWithSHA256 {
			t.Fatalf("the certificate is signed with %v; this test hashes it with SHA-256", served.SignatureAlgorithm)
		}
		hash := sha256.Sum256(served.Raw)
		kind, body, withoutProof := client.plus(t, hash[:], "pencil")
		client.requireAuthenticated(t, kind, body, withoutProof, "pencil")
	})
	t.Run("without channel binding", func(t *testing.T) {
		client, _, _ := startSCRAMOverTLS(t, address, "alice", nil)
		client.begin(t, "n,,")
		kind, body := client.finish(t, "biws", client.nonce, "pencil")
		client.requireAuthenticated(t, kind, body, "c=biws,r="+client.nonce, "pencil")
	})
	t.Run("no TLS", func(t *testing.T) {
		client := startSCRAM(t, address, "alice") // requires SCRAM-SHA-256 alone
		client.begin(t, "y,,")
		kind, body := client.finish(t, "eSws", client.nonce, "pencil")
		client.requireAuthenticated(t, kind, body, "c=eSws,r="+client.nonce, "pencil")
	})

	t.Run("another certificate", func(t *testing.T) {
		client, _, _ := startSCRAMOverTLS(t, address, "alice", nil)
		kind, body, _ := client.plus(t, otherHash[:], "pencil")
		client.requireRefused(t, kind, body, "28000", "SCRAM channel binding check failed")
	})
	t.Run("PLUS without the hash", func(t *testing.T) {
		client, _, _ := startSCRAMOverTLS(t, address, "alice", nil)
		kind, body, _ := client.plus(t, nil, "pencil")
		client.requireRefused(t, kind, body, "28000", "SCRAM channel binding check failed")
	})
	for _, refused := range []struct{ name, mechanism, first, message string }{
		{"y while PLUS is offered", scramMechanism, "y,," + bare, "SCRAM channel binding negotiation error: the client supports " +
			"SCRAM channel binding but thinks the server does not; this server does support channel binding"},
		{"PLUS with n", scramPlusMechanism, "n,," + bare, "malformed SCRAM message: the client selected SCRAM-SHA-256-PLUS, " +
			"but the SCRAM message does not include channel binding data"},
		{"PLUS with y", scramPlusMechanism, "y,," + bare, "malformed SCRAM message: the client selected SCRAM-SHA-256-PLUS, " +
			"but the SCRAM message does not include channel binding data"},
		{"binding data without PLUS", scramMechanism, plusHeader + bare, "malformed SCRAM message: the client selected " +
			"SCRAM-SHA-256 without channel binding, but the SCRAM message includes channel binding data"},
		{"tls-unique", scramPlusMechanism, "p=tls-unique,," + bare, "unsupported SCRAM channel-binding type \"tls-unique\""},
	} {
		t.Run(refused.name, func(t *testing.T) {
			client, _, _ := startSCRAMOverTLS(t, address, "alice", nil)
			kind, body := client.sendClientFirst(t, refused.mechanism, refused.first)
			client.requireRefused(t, kind, body, "08P01", refused.message)
		})
	}

	if _, opened := authenticator.record(); strings.Join(opened, " ") != "alice alice alice" {
		t.Fatalf("sessions opened for %q, want the three that came in", opened)
	}
}

// startRelay is someone between a client and the server at address: it ends the client's TLS with a certificate of its
// own, as a client that checks no certificate (sslmode=require) lets it, opens its own TLS to the server, and carries
// the bytes both ways; with strip it also takes SCRAM-SHA-256-PLUS out of the server's offer on the way.
func startRelay(t *testing.T, address string, own tls.Certificate, strip bool) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			client, err := listener.Accept()
			if err != nil {
				return
			}
			go relayOne(client, address, own, strip)
		}
	}()
	return listener.Addr().String()
}

func relayOne(client net.Conn, address string, own tls.Certificate, strip bool) {
	defer client.Close()
	var request [8]byte
	if _, err := io.ReadFull(client, request[:]); err != nil {
		return
	}
	upstream, err := net.Dial("tcp", address)
	if err != nil {
		return
	}
	defer upstream.Close()
	var answer [1]byte
	if _, err := upstream.Write(request[:]); err != nil {
		return
	}
	if _, err := io.ReadFull(upstream, answer[:]); err != nil || answer[0] != 'S' {
		return
	}
	if _, err := client.Write(answer[:]); err != nil {
		return
	}
	front := tls.Server(client, &tls.Config{Certificates: []tls.Certificate{own}})
	back := tls.Client(upstream, &tls.Config{InsecureSkipVerify: true})
	if front.Handshake() != nil || back.Handshake() != nil {
		return
	}
	go func() {
		_, _ = io.Copy(back, front)
		_ = back.Close()
	}()
	if strip {
		var head [5]byte
		if _, err := io.ReadFull(back, head[:]); err != nil {
			return
		}
		body := make([]byte, binary.BigEndian.Uint32(head[1:])-4)
		if _, err := io.ReadFull(back, body); err != nil {
			return
		}
		if head[0] == 'R' && binary.BigEndian.Uint32(body) == authenticationSASL {
			kept := binary.BigEndian.AppendUint32(nil, authenticationSASL)
			for _, name := range bytes.Split(body[4:], []byte{0}) {
				if len(name) > 0 && string(name) != scramPlusMechanism {
					kept = append(append(kept, name...), 0)
				}
			}
			body = append(kept, 0)
			binary.BigEndian.PutUint32(head[1:], uint32(4+len(body)))
		}
		if _, err := front.Write(append(head[:], body...)); err != nil {
			return
		}
	}
	_, _ = io.Copy(front, back)
	_ = front.Close()
}

// What channel binding is for: someone between the client and the server who ends the client's TLS with their own
// certificate and carries the exchange to the server. A client of SCRAM-SHA-256-PLUS binds its proof to the relay's
// certificate, and the server refuses it; a client without channel binding is carried through and gets a session,
// which is what PLUS stops. A relay that takes PLUS out of the offer gets a client that has channel binding to say "y",
// and the server, which offered it, refuses that too. The same client straight to the server comes in. The server
// here holds its certificate in Certificates, not GetCertificate (chooseCertificate's other way).
//
// CONTROL (08/11/2026): with receiveClientFinal not comparing the hash, "PLUS through the relay" fails "reply = 'R'
// …v=…, want ErrorResponse 28000"; with "y" taken whether PLUS was offered or not, "PLUS taken out" fails "reply = 'R'
// …r=…, want ErrorResponse 08P01"; with chooseCertificate not falling back to Certificates, "straight to the server"
// fails "mechanisms = …SCRAM-SHA-256…, want …SCRAM-SHA-256-PLUS…".
func TestSCRAMPlusAgainstARelay(t *testing.T) {
	server := selfSigned(t, "db.example", ecdsaKey(t, elliptic.P256()), 0)
	relayed := selfSigned(t, "db.example", ecdsaKey(t, elliptic.P256()), 0)
	authenticator := newSCRAMTestAuthenticator(t)
	address := serveForTest(t, Server{Authenticator: authenticator, SCRAMMockIterations: scramTestIterations,
		TLS: &tls.Config{Certificates: []tls.Certificate{server}}})
	relay := startRelay(t, address, relayed, false)
	stripping := startRelay(t, address, relayed, true)

	t.Run("PLUS through the relay", func(t *testing.T) {
		client, mechanisms, served := startSCRAMOverTLS(t, relay, "alice", nil)
		if want := offered(scramPlusMechanism, scramMechanism); mechanisms != want {
			t.Fatalf("mechanisms = %q, want %q", mechanisms, want)
		}
		if !bytes.Equal(served.Raw, relayed.Certificate[0]) {
			t.Fatal("the client was not served the relay's certificate")
		}
		hash := sha256.Sum256(served.Raw)
		kind, body, _ := client.plus(t, hash[:], "pencil")
		client.requireRefused(t, kind, body, "28000", "SCRAM channel binding check failed")
	})
	t.Run("without channel binding through the relay", func(t *testing.T) {
		client, _, _ := startSCRAMOverTLS(t, relay, "alice", nil)
		client.begin(t, "n,,")
		kind, body := client.finish(t, "biws", client.nonce, "pencil")
		client.requireAuthenticated(t, kind, body, "c=biws,r="+client.nonce, "pencil")
	})
	t.Run("PLUS taken out", func(t *testing.T) {
		client, mechanisms, _ := startSCRAMOverTLS(t, stripping, "alice", nil)
		if want := offered(scramMechanism); mechanisms != want {
			t.Fatalf("mechanisms = %q, want %q", mechanisms, want)
		}
		client.clientFirstBare = "n=,r=fyko+d2lbbFgONRv9qkxdawL"
		kind, body := client.sendClientFirst(t, scramMechanism, "y,,"+client.clientFirstBare)
		client.requireRefused(t, kind, body, "08P01", "")
	})
	t.Run("straight to the server", func(t *testing.T) {
		client, mechanisms, served := startSCRAMOverTLS(t, address, "alice", nil)
		if want := offered(scramPlusMechanism, scramMechanism); mechanisms != want {
			t.Fatalf("mechanisms = %q, want %q", mechanisms, want)
		}
		hash := sha256.Sum256(served.Raw)
		kind, body, withoutProof := client.plus(t, hash[:], "pencil")
		client.requireAuthenticated(t, kind, body, withoutProof, "pencil")
	})

	if _, opened := authenticator.record(); strings.Join(opened, " ") != "alice alice" {
		t.Fatalf("sessions opened for %q, want the relayed one without channel binding and the straight one", opened)
	}
}

// Every connection is offered SCRAM-SHA-256-PLUS, a client that keeps TLS sessions to resume them too, even with a TLS
// config whose ticket key is fixed (servers sharing it): the connection's copy issues no session tickets, so each
// handshake serves the certificate the binding is of (servedCertificate).
//
// CONTROL (08/11/2026): with the connection's config issuing tickets, "connection 2: mechanisms = …SCRAM-SHA-256…, want
// …SCRAM-SHA-256-PLUS…" (it resumed, and a resumed handshake serves no certificate). Without the fixed key the control
// stays green: each copy makes its own ticket keys, and no ticket of one connection opens another.
func TestSCRAMPlusOnEveryHandshake(t *testing.T) {
	certificate := selfSigned(t, "db.example", ecdsaKey(t, elliptic.P256()), 0)
	secure := TLSConfig(func(*tls.ClientHelloInfo) (*tls.Certificate, error) { return &certificate, nil })
	secure.SessionTicketKey = [32]byte{7}
	address := serveForTest(t, Server{Authenticator: newSCRAMTestAuthenticator(t), SCRAMMockIterations: scramTestIterations,
		TLS: secure})
	config := &tls.Config{InsecureSkipVerify: true, ClientSessionCache: tls.NewLRUClientSessionCache(4)}
	for attempt := 1; attempt <= 3; attempt++ {
		client, mechanisms, served := startSCRAMOverTLS(t, address, "alice", config)
		if want := offered(scramPlusMechanism, scramMechanism); mechanisms != want {
			t.Fatalf("connection %d: mechanisms = %q, want %q", attempt, mechanisms, want)
		}
		hash := sha256.Sum256(served.Raw)
		kind, body, withoutProof := client.plus(t, hash[:], "pencil")
		client.requireAuthenticated(t, kind, body, withoutProof, "pencil")
		_ = client.conn.Close()
	}
}

// A certificate whose signature names no hash (Ed25519) gives no tls-server-end-point: the server offers SCRAM-SHA-256
// alone over TLS, and a client that has channel binding says "y" and comes in, as libpq does.
func TestSCRAMWithoutPlusForAnEd25519Certificate(t *testing.T) {
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	certificate := selfSigned(t, "db.example", key, 0)
	address := serveForTest(t, Server{Authenticator: newSCRAMTestAuthenticator(t), SCRAMMockIterations: scramTestIterations,
		TLS: &tls.Config{Certificates: []tls.Certificate{certificate}}})
	client, mechanisms, _ := startSCRAMOverTLS(t, address, "alice", nil)
	if want := offered(scramMechanism); mechanisms != want {
		t.Fatalf("mechanisms = %q, want %q", mechanisms, want)
	}
	client.begin(t, "y,,")
	kind, body := client.finish(t, "eSws", client.nonce, "pencil")
	client.requireAuthenticated(t, kind, body, "c=eSws,r="+client.nonce, "pencil")
}

// serverEndPoint hashes the certificate by the hash of its signature, SHA-256 for SHA-1 (RFC 5929 section 4.1), and
// gives nothing for a signature without a hash.
//
// CONTROL (08/11/2026): with SHA-256 for every signature, "ECDSA P-384" fails "the hash of SHA-256, want SHA-384's".
func TestServerEndPoint(t *testing.T) {
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	_, edKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	p256 := selfSigned(t, "a.example", ecdsaKey(t, elliptic.P256()), 0)
	sha1Leaf, err := x509.ParseCertificate(p256.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	sha1Leaf.SignatureAlgorithm = x509.ECDSAWithSHA1 // as a certificate signed with SHA-1 says, which Go no longer makes
	sha256Of := func(raw []byte) []byte { sum := sha256.Sum256(raw); return sum[:] }
	sha384Of := func(raw []byte) []byte { sum := sha512.Sum384(raw); return sum[:] }
	sha512Of := func(raw []byte) []byte { sum := sha512.Sum512(raw); return sum[:] }
	for _, test := range []struct {
		name        string
		certificate tls.Certificate
		signature   x509.SignatureAlgorithm
		hash        func([]byte) []byte
		hashName    string
	}{
		{"ECDSA P-256", p256, x509.ECDSAWithSHA256, sha256Of, "SHA-256"},
		{"ECDSA P-384", selfSigned(t, "a.example", ecdsaKey(t, elliptic.P384()), 0), x509.ECDSAWithSHA384, sha384Of, "SHA-384"},
		{"ECDSA P-521", selfSigned(t, "a.example", ecdsaKey(t, elliptic.P521()), 0), x509.ECDSAWithSHA512, sha512Of, "SHA-512"},
		{"RSA", selfSigned(t, "a.example", rsaKey, 0), x509.SHA256WithRSA, sha256Of, "SHA-256"},
		{"RSA-PSS SHA-384", selfSigned(t, "a.example", rsaKey, x509.SHA384WithRSAPSS), x509.SHA384WithRSAPSS, sha384Of, "SHA-384"},
		{"SHA-1", tls.Certificate{Certificate: p256.Certificate, Leaf: sha1Leaf}, x509.ECDSAWithSHA1, sha256Of, "SHA-256"},
		{"Ed25519", selfSigned(t, "a.example", edKey, 0), x509.PureEd25519, nil, "none"},
	} {
		t.Run(test.name, func(t *testing.T) {
			leaf := test.certificate.Leaf
			if leaf == nil {
				if leaf, err = x509.ParseCertificate(test.certificate.Certificate[0]); err != nil {
					t.Fatal(err)
				}
			}
			if leaf.SignatureAlgorithm != test.signature {
				t.Fatalf("signed with %v, want %v", leaf.SignatureAlgorithm, test.signature)
			}
			got := serverEndPoint(&test.certificate)
			if test.hash == nil {
				if got != nil {
					t.Fatalf("a hash of %d bytes, want none", len(got))
				}
				return
			}
			if want := test.hash(test.certificate.Certificate[0]); !bytes.Equal(got, want) {
				name := map[int]string{32: "SHA-256", 48: "SHA-384", 64: "SHA-512"}[len(got)]
				t.Fatalf("the hash of %s, want %s's", name, test.hashName)
			}
		})
	}
	if serverEndPoint(nil) != nil || serverEndPoint(&tls.Certificate{}) != nil {
		t.Fatal("a hash of no certificate")
	}
}
