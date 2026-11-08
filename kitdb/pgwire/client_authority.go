package pgwire

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// The authority of clientAuthority and the certificates it signs (KITWORK_KITDB.md câu 13.11, 08/11/2026): what
// `kitwork database authority` and `kitwork database certificate <user>` write, so a host needs no openssl to give its
// clients certificates.

const (
	// AuthorityCertificate and AuthorityKey are the files of an authority in its folder: the certificate the
	// manifest's clientAuthority names, and the private key that signs the clients' certificates.
	AuthorityCertificate = "authority.pem"
	AuthorityKey         = "authority.key"
	authorityYears       = 10
	// MaximumCertificateDays bounds how long a client's certificate is valid.
	MaximumCertificateDays = 3650
)

// clientName is a user a certificate is signed for: it names the files too, so it is a file's name everywhere.
var clientName = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,62}$`)

// CreateClientAuthority makes an authority in folder: authority.pem, its certificate, which the manifest's
// clientAuthority names and a server may hold, and authority.key, its private key, which signs the clients'
// certificates and stays secret. It writes neither when either is there: a new authority leaves every certificate the
// old one signed worthless.
func CreateClientAuthority(folder string, now time.Time) (certificateFile, keyFile string, err error) {
	certificateFile, keyFile = filepath.Join(folder, AuthorityCertificate), filepath.Join(folder, AuthorityKey)
	if err := absent(certificateFile, keyFile); err != nil {
		return "", "", fmt.Errorf("%w; a new authority leaves every certificate the old one signed worthless, so remove "+
			"both files by hand to make one", err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", err
	}
	serial, err := serialNumber()
	if err != nil {
		return "", "", err
	}
	template := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: "KitDB client authority"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.AddDate(authorityYears, 0, 0),
		IsCA: true, BasicConstraintsValid: true, MaxPathLenZero: true,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return "", "", err
	}
	if err := writePair(folder, certificateFile, der, keyFile, key); err != nil {
		return "", "", err
	}
	return certificateFile, keyFile, nil
}

// IssueClientCertificate signs, with the authority of folder, a certificate for user valid for days days: <user>.crt
// and <user>.key in folder, what a client gives as sslcert and sslkey. Its common name is user, the name the client
// must log in with. It writes neither file when either is there.
func IssueClientCertificate(folder, user string, days int, now time.Time) (certificateFile, keyFile string, notAfter time.Time, err error) {
	if !clientName.MatchString(user) {
		return "", "", time.Time{}, fmt.Errorf("user %q: a user of letters, digits, _, . and -, at most 63 bytes, not beginning with . or -, "+
			"since it names the files (sign another name with openssl)", user)
	}
	if days < 1 || days > MaximumCertificateDays {
		return "", "", time.Time{}, fmt.Errorf("days %d: a certificate is valid from 1 to %d days", days, MaximumCertificateDays)
	}
	authority, signer, err := readAuthority(folder)
	if err != nil {
		return "", "", time.Time{}, err
	}
	certificateFile, keyFile = filepath.Join(folder, user+".crt"), filepath.Join(folder, user+".key")
	if err := absent(certificateFile, keyFile); err != nil {
		return "", "", time.Time{}, fmt.Errorf("%w; remove both to sign %s a new one", err, user)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", time.Time{}, err
	}
	serial, err := serialNumber()
	if err != nil {
		return "", "", time.Time{}, err
	}
	notAfter = now.AddDate(0, 0, days)
	if notAfter.After(authority.NotAfter) {
		notAfter = authority.NotAfter
	}
	template := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: user},
		NotBefore: now.Add(-time.Hour), NotAfter: notAfter,
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, authority, &key.PublicKey, signer)
	if err != nil {
		return "", "", time.Time{}, err
	}
	if err := writePair(folder, certificateFile, der, keyFile, key); err != nil {
		return "", "", time.Time{}, err
	}
	return certificateFile, keyFile, notAfter, nil
}

// readAuthority reads the authority of folder: its certificate and the key that signs with it.
func readAuthority(folder string) (*x509.Certificate, crypto.Signer, error) {
	certificateFile, keyFile := filepath.Join(folder, AuthorityCertificate), filepath.Join(folder, AuthorityKey)
	certificatePEM, err := os.ReadFile(certificateFile)
	if err != nil {
		return nil, nil, fmt.Errorf("the authority: %w (make one with kitwork database authority)", err)
	}
	keyPEM, err := os.ReadFile(keyFile)
	if err != nil {
		return nil, nil, fmt.Errorf("the authority's key: %w (it signs; the server needs only %s)", err, AuthorityCertificate)
	}
	block, _ := pem.Decode(certificatePEM)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, nil, fmt.Errorf("%s is no PEM certificate", certificateFile)
	}
	authority, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", certificateFile, err)
	}
	if !authority.IsCA {
		return nil, nil, fmt.Errorf("%s is no authority's certificate", certificateFile)
	}
	block, _ = pem.Decode(keyPEM)
	if block == nil {
		return nil, nil, fmt.Errorf("%s is no PEM key", keyFile)
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", keyFile, err)
	}
	signer, ok := parsed.(crypto.Signer)
	if !ok {
		return nil, nil, fmt.Errorf("%s holds no key that signs", keyFile)
	}
	if public, ok := signer.Public().(interface{ Equal(crypto.PublicKey) bool }); !ok || !public.Equal(authority.PublicKey) {
		return nil, nil, fmt.Errorf("%s is not the key of %s", keyFile, certificateFile)
	}
	return authority, signer, nil
}

// absent refuses files that are there.
func absent(files ...string) error {
	for _, file := range files {
		if _, err := os.Stat(file); err == nil {
			return fmt.Errorf("%s is there already", file)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

// writePair writes a certificate (readable by anyone, 0644) and its key (its owner's only, 0600, as libpq requires),
// neither over a file that is there; a pair half written is removed.
func writePair(folder, certificateFile string, der []byte, keyFile string, key *ecdsa.PrivateKey) error {
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(folder, 0o700); err != nil {
		return err
	}
	if err := writeNew(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		return err
	}
	if err := writeNew(certificateFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		_ = os.Remove(keyFile)
		return err
	}
	return nil
}

// writeNew writes data to a file that is not there yet.
func writeNew(path string, data []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}

func serialNumber() (*big.Int, error) {
	return rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
}
