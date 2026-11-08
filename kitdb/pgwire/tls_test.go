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
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lib/pq"
)

// The port's TLS (tls.go, server.go startTLS; KITWORK_KITDB.md câu 13.11, 08/11/2026).

// testAuthority is a certificate authority of the test, and the PEM file of its certificate a client trusts.
type testAuthority struct {
	certificate *x509.Certificate
	key         *ecdsa.PrivateKey
	file        string
}

func newTestAuthority(t *testing.T) *testAuthority {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "KitDB test authority"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "authority.pem")
	if err := os.WriteFile(file, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		t.Fatal(err)
	}
	return &testAuthority{certificate: certificate, key: key, file: file}
}

// issue is a certificate of name the authority signs, valid from notBefore to notAfter, and its key, in PEM.
func (authority *testAuthority) issue(t *testing.T, name string, notBefore, notAfter time.Time) (certificate, key []byte) {
	t.Helper()
	private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
	template := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: name}, DNSNames: []string{name},
		NotBefore: notBefore, NotAfter: notAfter,
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, authority.certificate, &private.PublicKey, authority.key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
}

// remoteListener is a loopback listener that says it listens everywhere and whose connections say they come from
// another machine (203.0.113.7, a documentation address): the server's rules for a client of another machine, tried
// on this one.
type remoteListener struct{ net.Listener }

func (listener remoteListener) Addr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4zero, Port: listener.Listener.Addr().(*net.TCPAddr).Port}
}

func (listener remoteListener) Accept() (net.Conn, error) {
	connection, err := listener.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return remoteConnection{connection}, nil
}

type remoteConnection struct{ net.Conn }

func (remoteConnection) RemoteAddr() net.Addr {
	return &net.TCPAddr{IP: net.ParseIP("203.0.113.7"), Port: 40000}
}

// serveRemote runs server on a remoteListener until the test ends and gives its real address; done receives what
// Serve returned.
func serveRemote(t *testing.T, server Server) (string, <-chan error) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx, remoteListener{listener}) }()
	t.Cleanup(func() {
		cancel()
		listener.Close()
	})
	return listener.Addr().String(), done
}

// redirectDialer dials address whatever address lib/pq asks for: the client connects by a name, its TLS checks the
// certificate against that name, and the connection reaches the test's server.
type redirectDialer struct{ address string }

func (dialer redirectDialer) Dial(network, _ string) (net.Conn, error) {
	return net.Dial(network, dialer.address)
}

func (dialer redirectDialer) DialTimeout(network, _ string, timeout time.Duration) (net.Conn, error) {
	return net.DialTimeout(network, dialer.address, timeout)
}

// answerOverTLS connects with lib/pq to address by host, with sslmode (and the authority's certificate trusted), and
// reads the answer of one statement.
func answerOverTLS(t *testing.T, address, host, sslmode, authority string) (string, error) {
	t.Helper()
	parameters := fmt.Sprintf("host=%s port=5432 user=alice password=pencil dbname=shop sslmode=%s connect_timeout=30", host, sslmode)
	if authority != "" {
		parameters += " sslrootcert=" + filepath.ToSlash(authority)
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

// A server with TLS listens everywhere. A client of another machine that asks for TLS gets the certificate of the
// port's files and checks it (lib/pq, sslmode=verify-full, the test's authority trusted); one that does not is refused
// at its startup with 28000, its password never read. A server without TLS refuses that listener, as before.
//
// CONTROL (08/11/2026): with an SSLRequest answered N whatever the server has, "verify-full: \"\", pq: SSL is not
// enabled on the server"; with no rule for a cleartext client of another machine, "a cleartext client of another
// machine got in: 42".
func TestTLSForAClientOfAnotherMachine(t *testing.T) {
	authority := newTestAuthority(t)
	certificate, key := authority.issue(t, "db.example", time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	folder := t.TempDir()
	certificateFile, keyFile := filepath.Join(folder, "db.pem"), filepath.Join(folder, "db.key")
	if err := os.WriteFile(certificateFile, certificate, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, key, 0o600); err != nil {
		t.Fatal(err)
	}
	files, err := CertificateFiles(certificateFile, keyFile)
	if err != nil {
		t.Fatal(err)
	}
	address, _ := serveRemote(t, Server{Authenticator: cleartextTestAuthenticator{password: "pencil"}, TLS: TLSConfig(files)})

	if answer, err := answerOverTLS(t, address, "db.example", "verify-full", authority.file); err != nil || answer != "42" {
		t.Fatalf("verify-full: %q, %v", answer, err)
	}
	if _, err := answerOverTLS(t, address, "other.example", "verify-full", authority.file); err == nil || !strings.Contains(err.Error(), "other.example") {
		t.Errorf("verify-full by a name the certificate is not of: %v, want the name refused", err)
	}
	answer, err := answerOverTLS(t, address, "db.example", "disable", "")
	if err == nil {
		t.Fatalf("a cleartext client of another machine got in: %s", answer)
	}
	var protocolErr *pq.Error
	if !errors.As(err, &protocolErr) || protocolErr.Code != "28000" || !strings.Contains(protocolErr.Message, "must use TLS") {
		t.Errorf("a cleartext client of another machine: %v, want 28000 saying it must use TLS", err)
	}

	_, done := serveRemote(t, Server{Authenticator: cleartextTestAuthenticator{password: "pencil"}})
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "loopback") {
			t.Errorf("a server without TLS on a listener of every address: %v, want refused", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("a server without TLS served a listener of every address")
	}
}

// Bytes the client sends after its SSLRequest, before the server answers it, are refused (08P01), not read later as if
// they came through TLS (PostgreSQL's CVE-2021-23214).
//
// CONTROL (08/11/2026): with startTLS not looking at what is buffered, "the answer to an SSLRequest followed by bytes:
// 'S', want E".
func TestTLSRefusesBytesBeforeTheHandshake(t *testing.T) {
	authority := newTestAuthority(t)
	certificate, key := authority.issue(t, "db.example", time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	pair, err := tls.X509KeyPair(certificate, key)
	if err != nil {
		t.Fatal(err)
	}
	address := serveForTest(t, Server{
		Authenticator: cleartextTestAuthenticator{password: "pencil"},
		TLS:           TLSConfig(func(*tls.ClientHelloInfo) (*tls.Certificate, error) { return &pair, nil }),
	})
	connection, err := net.Dial("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	request := make([]byte, 8, 16)
	binary.BigEndian.PutUint32(request[0:4], 8)
	binary.BigEndian.PutUint32(request[4:8], sslRequestCode)
	request = append(request, 'Q', 0, 0, 0, 4, 'x', 'y', 'z') // what must wait for the handshake, sent with it
	if _, err := connection.Write(request); err != nil {
		t.Fatal(err)
	}
	_ = connection.SetReadDeadline(time.Now().Add(10 * time.Second))
	first, err := bufio.NewReader(connection).ReadByte()
	if err != nil {
		t.Fatal(err)
	}
	if first != 'E' {
		t.Errorf("the answer to an SSLRequest followed by bytes: %q, want E", first)
	}
}

// The port's files are read again once they change (a renewal written over them); a pair that does not load leaves
// the one before answering, and a pair that never loaded fails at once.
//
// CONTROL (08/11/2026): with get answering what was loaded first, "after the renewal: the certificate of first.example,
// want second.example" (and "after a broken write: … first.example, want second.example kept").
func TestTLSCertificateFilesAreReadAgain(t *testing.T) {
	authority := newTestAuthority(t)
	folder := t.TempDir()
	certificateFile, keyFile := filepath.Join(folder, "db.pem"), filepath.Join(folder, "db.key")
	write := func(certificate, key []byte, at time.Time) {
		t.Helper()
		for path, data := range map[string][]byte{certificateFile: certificate, keyFile: key} {
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(path, at, at); err != nil {
				t.Fatal(err)
			}
		}
	}
	name := func(certificate *tls.Certificate) string {
		leaf, err := x509.ParseCertificate(certificate.Certificate[0])
		if err != nil {
			t.Fatal(err)
		}
		return leaf.Subject.CommonName
	}
	now := time.Now()
	first, firstKey := authority.issue(t, "first.example", now.Add(-time.Hour), now.Add(time.Hour))
	write(first, firstKey, now.Add(-time.Minute))
	get, err := CertificateFiles(certificateFile, keyFile)
	if err != nil {
		t.Fatal(err)
	}
	if certificate, _ := get(&tls.ClientHelloInfo{}); name(certificate) != "first.example" {
		t.Fatalf("the certificate of %s, want first.example", name(certificate))
	}
	second, secondKey := authority.issue(t, "second.example", now.Add(-time.Hour), now.Add(time.Hour))
	write(second, secondKey, now)
	if certificate, _ := get(&tls.ClientHelloInfo{}); name(certificate) != "second.example" {
		t.Errorf("after the renewal: the certificate of %s, want second.example", name(certificate))
	}
	write([]byte("not a certificate"), secondKey, now.Add(time.Minute))
	if certificate, _ := get(&tls.ClientHelloInfo{}); name(certificate) != "second.example" {
		t.Errorf("after a broken write: the certificate of %s, want second.example kept", name(certificate))
	}
	if _, err := CertificateFiles(certificateFile, keyFile); err == nil {
		t.Error("a pair that does not load was taken")
	}
}

// writeStored writes a certificate as autocert.DirCache keeps it: the private key, then the certificate, in PEM, in a
// file named by the host.
func writeStored(t *testing.T, folder, file string, certificate, key []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(folder, file), append(append([]byte(nil), key...), certificate...), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The website's certificates answer by the name the client connects with: a site's, valid now, out of the AutoSSL
// store, checked by the client (lib/pq, verify-full); no name, a name that is no site, a name that is no hostname (a
// path, a file of the store), a certificate expired, each answers no certificate and says why. A client of another
// machine connects by TLS to the website's certificate with nothing declared for the port.
//
// CONTROL (08/11/2026): with the hostname pattern not checked, the file outside the store is read (its certificate is
// not of the name, so it is still refused, by another message): "../outside: pgwire: no certificate of ../outside
// valid now in …\certs: …, want refused saying \"is no site\""; with the time of the certificate not checked,
// "old.example: <nil> (true), want refused saying \"valid from\"".
func TestTLSWebsiteCertificates(t *testing.T) {
	authority := newTestAuthority(t)
	root := t.TempDir()
	folder := filepath.Join(root, "certs")
	if err := os.MkdirAll(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	shop, shopKey := authority.issue(t, "shop.example", now.Add(-time.Hour), now.Add(time.Hour))
	writeStored(t, folder, "shop.example", shop, shopKey)
	old, oldKey := authority.issue(t, "old.example", now.Add(-48*time.Hour), now.Add(-24*time.Hour))
	writeStored(t, folder, "old.example", old, oldKey)
	// a file outside the store a name could reach if it were a path
	writeStored(t, root, "outside", shop, shopKey)
	sites := map[string]bool{"shop.example": true, "old.example": true, "../outside": true}
	get := CachedCertificates(folder, func(name string) bool { return sites[name] })

	if certificate, err := get(&tls.ClientHelloInfo{ServerName: "Shop.Example."}); err != nil || certificate == nil {
		t.Errorf("shop.example: %v", err)
	}
	for name, want := range map[string]string{
		"":              "named no host",
		"other.example": "is no site",
		"../outside":    "is no site",
		"old.example":   "valid from",
	} {
		if certificate, err := get(&tls.ClientHelloInfo{ServerName: name}); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v (%v), want refused saying %q", name, err, certificate != nil, want)
		}
	}

	address, _ := serveRemote(t, Server{Authenticator: cleartextTestAuthenticator{password: "pencil"}, TLS: TLSConfig(get)})
	if answer, err := answerOverTLS(t, address, "shop.example", "verify-full", authority.file); err != nil || answer != "42" {
		t.Errorf("verify-full by the site's name: %q, %v", answer, err)
	}
	if _, err := answerOverTLS(t, address, "other.example", "require", ""); err == nil {
		t.Error("a name that is no site got a certificate")
	}
}
