package pgwire

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// The authority of clientAuthority and the certificates it signs (client_authority.go; KITWORK_KITDB.md câu 13.11,
// 08/11/2026).

// CreateClientAuthority writes an authority once; IssueClientCertificate signs a user a certificate of its name, for
// clients, valid the days asked, whose key goes with it; neither writes over a file, and each refuses what it cannot
// sign.
//
// CONTROL (08/11/2026): with a second authority written over the first, "a second authority: <nil>, want refused";
// with the client certificate not marked for clients, "alice's certificate does not verify as a client's: x509:
// certificate specifies an incompatible key usage"; with the user's name not checked, "user \"../outside\": <nil>".
func TestClientAuthorityAndItsCertificates(t *testing.T) {
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	folder := filepath.Join(t.TempDir(), "clients")
	authorityFile, authorityKey, err := CreateClientAuthority(folder, now)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(authorityFile)
	if _, _, err := CreateClientAuthority(folder, now); err == nil || !strings.Contains(err.Error(), "is there already") {
		t.Fatalf("a second authority: %v, want refused", err)
	}
	if after, _ := os.ReadFile(authorityFile); !bytes.Equal(before, after) {
		t.Fatal("the authority was written over")
	}
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(authorityKey); err != nil || info.Mode().Perm() != 0o600 {
			t.Errorf("the authority's key: %v, %v, want 0600", info.Mode().Perm(), err)
		}
	}

	certificateFile, keyFile, notAfter, err := IssueClientCertificate(folder, "alice", 30, now)
	if err != nil {
		t.Fatal(err)
	}
	if want := now.AddDate(0, 0, 30); !notAfter.Equal(want) {
		t.Errorf("valid until %v, want %v", notAfter, want)
	}
	pair, err := tls.LoadX509KeyPair(certificateFile, keyFile)
	if err != nil {
		t.Fatalf("alice's certificate and key do not go together: %v", err)
	}
	certificate, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	authorities, err := ClientAuthorityFile(authorityFile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := certificate.Verify(x509.VerifyOptions{Roots: authorities, CurrentTime: now,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		t.Errorf("alice's certificate does not verify as a client's: %v", err)
	}
	if certificate.Subject.CommonName != "alice" || !certificate.NotAfter.Equal(notAfter) {
		t.Errorf("alice's certificate: %q until %v", certificate.Subject.CommonName, certificate.NotAfter)
	}
	if _, err := certificate.Verify(x509.VerifyOptions{Roots: authorities, CurrentTime: now,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err == nil {
		t.Error("alice's certificate verifies as a server's")
	}
	if _, _, _, err := IssueClientCertificate(folder, "alice", 30, now); err == nil || !strings.Contains(err.Error(), "is there already") {
		t.Errorf("alice again: %v, want refused", err)
	}

	for _, user := range []string{"", "../outside", "a/b", `a\b`, ".hidden", "-x", "a b", strings.Repeat("a", 64)} {
		if _, _, _, err := IssueClientCertificate(folder, user, 30, now); err == nil || !strings.Contains(err.Error(), "names the files") {
			t.Errorf("user %q: %v", user, err)
		}
	}
	for _, days := range []int{0, -1, MaximumCertificateDays + 1} {
		if _, _, _, err := IssueClientCertificate(folder, "bob", days, now); err == nil || !strings.Contains(err.Error(), "days") {
			t.Errorf("days %d: %v", days, err)
		}
	}
	entries, _ := os.ReadDir(folder)
	if len(entries) != 4 {
		names := []string{}
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Errorf("the folder holds %v, want the authority's two files and alice's two", names)
	}

	if _, _, _, err := IssueClientCertificate(filepath.Join(t.TempDir(), "none"), "alice", 30, now); err == nil ||
		!strings.Contains(err.Error(), "kitwork database authority") {
		t.Errorf("no authority: %v, want the command that makes one named", err)
	}
	other := filepath.Join(t.TempDir(), "other")
	if _, _, err := CreateClientAuthority(other, now); err != nil {
		t.Fatal(err)
	}
	mixed := filepath.Join(t.TempDir(), "mixed")
	if err := os.MkdirAll(mixed, 0o700); err != nil {
		t.Fatal(err)
	}
	for file, from := range map[string]string{AuthorityCertificate: folder, AuthorityKey: other} {
		data, _ := os.ReadFile(filepath.Join(from, file))
		if err := os.WriteFile(filepath.Join(mixed, file), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, _, err := IssueClientCertificate(mixed, "alice", 30, now); err == nil || !strings.Contains(err.Error(), "is not the key of") {
		t.Errorf("another authority's key: %v", err)
	}
}

// A certificate never outlives its authority.
func TestClientCertificateEndsWithItsAuthority(t *testing.T) {
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	folder := t.TempDir()
	if _, _, err := CreateClientAuthority(folder, now.AddDate(-9, -11, 0)); err != nil {
		t.Fatal(err)
	}
	_, _, notAfter, err := IssueClientCertificate(folder, "alice", 365, now)
	if err != nil {
		t.Fatal(err)
	}
	authority, _, err := readAuthority(folder)
	if err != nil {
		t.Fatal(err)
	}
	if !notAfter.Equal(authority.NotAfter) {
		t.Errorf("valid until %v, past its authority's %v", notAfter, authority.NotAfter)
	}
}

// What the two write is what a server and a client take: the server reads the authority as clientAuthority, lib/pq
// gives alice's files and comes in from another machine; bob's files do not log in as alice.
//
// CONTROL (08/11/2026): with the client certificate's common name not the user's, "lib/pq with the files written for
// alice: …certificate authentication failed…".
func TestWrittenCertificatesComeIn(t *testing.T) {
	now := time.Now()
	folder := t.TempDir()
	authorityFile, _, err := CreateClientAuthority(folder, now)
	if err != nil {
		t.Fatal(err)
	}
	aliceCertificate, aliceKey, _, err := IssueClientCertificate(folder, "alice", 1, now)
	if err != nil {
		t.Fatal(err)
	}
	bobCertificate, bobKey, _, err := IssueClientCertificate(folder, "bob", 1, now)
	if err != nil {
		t.Fatal(err)
	}
	authorities, err := ClientAuthorityFile(authorityFile)
	if err != nil {
		t.Fatal(err)
	}
	serverAuthority := newTestAuthority(t)
	certificate, key := serverAuthority.issue(t, "db.example", now.Add(-time.Hour), now.Add(time.Hour))
	pair, err := tls.X509KeyPair(certificate, key)
	if err != nil {
		t.Fatal(err)
	}
	remote, _ := serveRemote(t, Server{Authenticator: newSCRAMTestAuthenticator(t), SCRAMMockIterations: scramTestIterations,
		ClientAuthorities: authorities, TLS: TLSConfig(func(*tls.ClientHelloInfo) (*tls.Certificate, error) { return &pair, nil })})
	if answer, err := answerWithCertificate(t, remote, aliceCertificate, aliceKey); err != nil || answer != "42" {
		t.Fatalf("lib/pq with the files written for alice: %q, %v", answer, err)
	}
	if _, err := answerWithCertificate(t, remote, bobCertificate, bobKey); err == nil || !strings.Contains(err.Error(), "certificate authentication failed") {
		t.Fatalf("lib/pq with bob's files as alice: %v, want refused", err)
	}
	keyPEM, _ := os.ReadFile(aliceKey)
	if block, _ := pem.Decode(keyPEM); block == nil || block.Type != "PRIVATE KEY" {
		t.Errorf("alice's key is not a PKCS #8 PEM key, which every libpq reads")
	}
}
