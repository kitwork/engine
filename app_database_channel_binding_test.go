package engine

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// channelBinding of app.database (config_app_database.go, app_database_runtime.go appDatabaseServer; KITWORK_KITDB.md
// câu 13.11, 08/11/2026): "require" takes a client of another machine with SCRAM-SHA-256-PLUS only (kitdb/pgwire
// TestSCRAMPlusRequired tries it on the wire).

// channelBinding is "prefer" or "require", nothing else; the port's server requires binding exactly when it is
// "require".
//
// CONTROL (08/11/2026): with appDatabaseServer not passing the requirement on, "require: the server requires binding
// false"; with any value taken, "true: <nil>, want an error naming channelBinding".
func TestAppDatabaseChannelBindingOption(t *testing.T) {
	base := func(value interface{}) map[string]interface{} {
		source := map[string]interface{}{"path": "./.database/", "port": 5455, "password": "p"}
		if value != nil {
			source["channelBinding"] = value
		}
		return source
	}
	for value, want := range map[string]bool{"": false, "prefer": false, "require": true} {
		var given interface{}
		if value != "" {
			given = value
		}
		config, err := parseAppDatabaseConfig(base(given))
		if err != nil || config.RequireChannelBinding != want {
			t.Errorf("channelBinding %q: required %v, %v, want %v", value, config.RequireChannelBinding, err, want)
			continue
		}
		if server := appDatabaseServer(config, &appDatabaseGateway{}, nil); server.RequireChannelBinding != want {
			t.Errorf("%s: the server requires binding %v", value, server.RequireChannelBinding)
		}
	}
	for _, value := range []interface{}{"disable", "Require", true, 1} {
		if _, err := parseAppDatabaseConfig(base(value)); err == nil || !strings.Contains(err.Error(), "channelBinding") {
			t.Errorf("%v: %v, want an error naming channelBinding", value, err)
		}
	}
}

// With channelBinding: "require", a certificate file signed without a hash (Ed25519) is refused before the port listens:
// no client of another machine could come in. An ECDSA certificate is taken, and Ed25519 without the requirement too.
//
// CONTROL (08/11/2026): with appDatabaseTLS not checking the certificate, "Ed25519 with binding required: <nil>, want
// the error naming the certificate".
func TestAppDatabaseChannelBindingCertificate(t *testing.T) {
	folder := t.TempDir()
	authority := newPortAuthority(t)
	certificate, key := authority.issue(t, "db.example")
	write := func(name string, data []byte) string {
		path := filepath.Join(folder, name)
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	ecdsa := &AppDatabaseTLS{Certificate: write("ecdsa.pem", certificate), Key: write("ecdsa.key", key)}
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(3), Subject: pkix.Name{CommonName: "db.example"}, DNSNames: []string{"db.example"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, private.Public(), private)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	ed := &AppDatabaseTLS{Certificate: write("ed.pem", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		Key: write("ed.key", pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))}
	port := &appDatabaseGateway{}

	if _, _, err := appDatabaseTLS(AppDatabaseConfig{Path: "x", Host: "0.0.0.0", TLS: ed, RequireChannelBinding: true}, appDatabaseApps{}, port); err == nil ||
		!strings.Contains(err.Error(), "ed.pem is signed without a hash") {
		t.Errorf("Ed25519 with binding required: %v, want the error naming the certificate", err)
	}
	if secure, _, err := appDatabaseTLS(AppDatabaseConfig{Path: "x", Host: "0.0.0.0", TLS: ecdsa, RequireChannelBinding: true}, appDatabaseApps{}, port); err != nil || secure == nil {
		t.Errorf("ECDSA with binding required: %v, %v", secure != nil, err)
	}
	if secure, _, err := appDatabaseTLS(AppDatabaseConfig{Path: "x", Host: "0.0.0.0", TLS: ed}, appDatabaseApps{}, port); err != nil || secure == nil {
		t.Errorf("Ed25519 without the requirement: %v, %v", secure != nil, err)
	}
}
