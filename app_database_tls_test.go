package engine

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kitwork/engine/work"
	"github.com/lib/pq"
)

// The TLS of the host's database port (app_database_runtime.go appDatabaseTLS; KITWORK_KITDB.md câu 13.11, 08/11/2026).

// portAuthority signs the port's certificates in these tests; file is its certificate, which a client trusts.
type portAuthority struct {
	certificate *x509.Certificate
	key         *ecdsa.PrivateKey
	file        string
}

func newPortAuthority(t *testing.T) *portAuthority {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "port authority"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certificate, _ := x509.ParseCertificate(der)
	file := filepath.Join(t.TempDir(), "authority.pem")
	if err := os.WriteFile(file, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		t.Fatal(err)
	}
	return &portAuthority{certificate: certificate, key: key, file: file}
}

// issue is a certificate of name and its key, in PEM.
func (authority *portAuthority) issue(t *testing.T, name string) (certificate, key []byte) {
	t.Helper()
	private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
	template := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: name}, DNSNames: []string{name},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, authority.certificate, &private.PublicKey, authority.key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, _ := x509.MarshalECPrivateKey(private)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
}

// portDialer dials address whatever lib/pq asks for: the client names a host its TLS checks the certificate against.
type portDialer struct{ address string }

func (dialer portDialer) Dial(network, _ string) (net.Conn, error) {
	return net.Dial(network, dialer.address)
}

func (dialer portDialer) DialTimeout(network, _ string, timeout time.Duration) (net.Conn, error) {
	return net.DialTimeout(network, dialer.address, timeout)
}

// connectOverTLS opens a client of the port at address by host, verify-full against the authority.
func connectOverTLS(t *testing.T, address, host, database, authority string) *sql.DB {
	t.Helper()
	connector, err := pq.NewConnector(fmt.Sprintf("host=%s port=5432 user=kitdb password=%s dbname=%s sslmode=verify-full sslrootcert=%s connect_timeout=30",
		host, appDatabaseTestPassword, database, filepath.ToSlash(authority)))
	if err != nil {
		t.Fatal(err)
	}
	connector.Dialer(portDialer{address})
	client := sql.OpenDB(connector)
	client.SetMaxOpenConns(1)
	t.Cleanup(func() { client.Close() })
	return client
}

// tls: { certificate, key } of app.database, relative to the manifest: `kitwork database` serves the port with TLS,
// and a client checking the certificate (lib/pq, verify-full) reads the host's database through it, on the loopback as
// anywhere; a client connecting by a name the certificate is not of is refused by its own check.
//
// CONTROL (08/11/2026): with the tls files not made absolute from the manifest's folder, "kitwork database ended before
// it listened: app.database …: tls: pgwire: TLS certificate: GetFileAttributesEx ./tls/db.pem: The system cannot find
// the path specified."; with the server not given the TLS, "SELECT name FROM products: pq: SSL is not enabled on the
// server".
func TestTheDatabasePortServesTLSFromItsFiles(t *testing.T) {
	authority := newPortAuthority(t)
	root := t.TempDir()
	certificate, key := authority.issue(t, "db.example")
	if err := os.MkdirAll(filepath.Join(root, "tls"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "tls", "db.pem"), certificate, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "tls", "db.key"), key, 0o600); err != nil {
		t.Fatal(err)
	}
	writeAppDatabaseFile(t, filepath.Join(root, ".database", "shopping", "kitwork.db"),
		`CREATE TABLE products (id INTEGER PRIMARY KEY, name TEXT NOT NULL)`, `INSERT INTO products (name) VALUES ('cà phê')`)
	if err := os.MkdirAll(filepath.Join(root, "apps"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(root, "application.work")
	source := fmt.Sprintf(`import * as app from "kitwork/application";
app.root(%q);
app.database({ path: "./.database/", port: %d, user: "kitdb", password: %q, tls: { certificate: "./tls/db.pem", key: "./tls/db.key" } });
`, filepath.ToSlash(filepath.Join(root, "apps")), freePort(t), appDatabaseTestPassword)
	if err := os.WriteFile(manifest, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	listening := make(chan string, 1)
	done := make(chan error, 1)
	finished := make(chan struct{})
	go func() {
		done <- runDatabase(ctx, listening, manifest)
		close(finished)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-finished:
		case <-time.After(30 * time.Second):
			t.Error("kitwork database did not return")
		}
	})
	var address string
	select {
	case address = <-listening:
	case err := <-done:
		t.Fatalf("kitwork database ended before it listened: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("kitwork database did not listen in 30 s")
	}
	client := connectOverTLS(t, address, "db.example", "shopping", authority.file)
	if got := appDatabaseRows(t, client, `SELECT name FROM products`); got != "cà phê" {
		t.Errorf("shopping.products over TLS = %q", got)
	}
	other := connectOverTLS(t, address, "other.example", "shopping", authority.file)
	if err := other.Ping(); err == nil || !strings.Contains(err.Error(), "other.example") {
		t.Errorf("a name the certificate is not of: %v, want refused", err)
	}
}

// Which TLS the port answers with: the files when declared; on the loopback without them none, the port as before;
// beyond the loopback without them the website's certificates, a site's by the name the client connects with, and an
// error before anything listens when the manifest declares no website; tls files that do not load fail at once.
//
// CONTROL (08/11/2026): with appDatabaseTLS answering nil beyond the loopback too, "0.0.0.0 with the website's
// certificates: <nil> (no TLS)"; with every name taken for a site, "other.example, no site of the host: a certificate
// (true), want refused".
func TestTheDatabasePortChoosesItsCertificates(t *testing.T) {
	authority := newPortAuthority(t)
	root := t.TempDir()
	apps := filepath.Join(root, "apps")
	if err := os.MkdirAll(filepath.Join(apps, "acme", "shop.example"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(apps, "acme", "shop.example", "router.work"), []byte(`import * as router from "kitwork/router";`), 0o644); err != nil {
		t.Fatal(err)
	}
	store := filepath.Join(root, "certs")
	if err := os.MkdirAll(store, 0o700); err != nil {
		t.Fatal(err)
	}
	certificate, key := authority.issue(t, "shop.example")
	if err := os.WriteFile(filepath.Join(store, "shop.example"), append(key, certificate...), 0o600); err != nil {
		t.Fatal(err)
	}
	// a certificate in the store of a name no site of the host has
	other, otherKey := authority.issue(t, "other.example")
	if err := os.WriteFile(filepath.Join(store, "other.example"), append(otherKey, other...), 0o600); err != nil {
		t.Fatal(err)
	}
	port := &appDatabaseGateway{apps: appDatabaseApps{root: apps, layout: work.RootLayoutMultiTenant}}
	withSite := appDatabaseApps{root: apps, layout: work.RootLayoutMultiTenant, certificates: store}

	if secure, _, err := appDatabaseTLS(AppDatabaseConfig{Path: "x", Host: "127.0.0.1"}, withSite, port); err != nil || secure != nil {
		t.Errorf("the loopback without files: %v, %v, want no TLS", secure != nil, err)
	}
	secure, said, err := appDatabaseTLS(AppDatabaseConfig{Path: "x", Host: "0.0.0.0"}, withSite, port)
	if err != nil || secure == nil {
		t.Fatalf("0.0.0.0 with the website's certificates: %v (no TLS), %v", secure, err)
	}
	if !strings.Contains(said, store) {
		t.Errorf("it says %q, want the store", said)
	}
	if found, err := secure.GetCertificate(&tls.ClientHelloInfo{ServerName: "shop.example"}); err != nil || found == nil {
		t.Errorf("shop.example: %v", err)
	}
	if found, err := secure.GetCertificate(&tls.ClientHelloInfo{ServerName: "other.example"}); err == nil {
		t.Errorf("other.example, no site of the host: a certificate (%v), want refused", found != nil)
	}
	if _, _, err := appDatabaseTLS(AppDatabaseConfig{Path: "x", Host: "0.0.0.0"}, appDatabaseApps{root: apps}, port); err == nil ||
		!strings.Contains(err.Error(), "tls: { certificate, key }") {
		t.Errorf("0.0.0.0 with no website: %v, want the error naming tls", err)
	}
	broken := &AppDatabaseTLS{Certificate: filepath.Join(root, "missing.pem"), Key: filepath.Join(root, "missing.key")}
	if _, _, err := appDatabaseTLS(AppDatabaseConfig{Path: "x", Host: "127.0.0.1", TLS: broken}, withSite, port); err == nil {
		t.Error("tls files that do not exist were taken")
	}
}

// tls of app.database takes certificate and key, both, and nothing else.
func TestAppDatabaseTLSOption(t *testing.T) {
	base := func(tls interface{}) map[string]interface{} {
		return map[string]interface{}{"path": "./.database/", "port": 5455, "password": "p", "tls": tls}
	}
	config, err := parseAppDatabaseConfig(base(map[string]interface{}{"certificate": "a.pem", "key": "a.key"}))
	if err != nil || config.TLS == nil || config.TLS.Certificate != "a.pem" || config.TLS.Key != "a.key" {
		t.Errorf("tls: { certificate, key } = %+v, %v", config.TLS, err)
	}
	for name, value := range map[string]interface{}{
		"no key":        map[string]interface{}{"certificate": "a.pem"},
		"an option":     map[string]interface{}{"certificate": "a.pem", "key": "a.key", "ca": "c.pem"},
		"not an object": true,
		"empty path":    map[string]interface{}{"certificate": " ", "key": "a.key"},
	} {
		if _, err := parseAppDatabaseConfig(base(value)); err == nil || !strings.Contains(err.Error(), "tls") {
			t.Errorf("%s: %v, want an error naming tls", name, err)
		}
	}
}
