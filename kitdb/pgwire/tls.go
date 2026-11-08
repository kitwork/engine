package pgwire

import (
	"bytes"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"hash"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// The certificates of the port's TLS (KITWORK_KITDB.md câu 13.11, 08/11/2026): a pair of files the host names, as
// PostgreSQL's ssl_cert_file and ssl_key_file, or the website's own, out of the AutoSSL store, by the name the client
// connects with.

// TLSConfig is the TLS a server answers an SSLRequest with: TLS 1.2 at least, the ALPN name PostgreSQL 17 clients
// offer, and the certificate certificate gives a handshake.
func TLSConfig(certificate func(*tls.ClientHelloInfo) (*tls.Certificate, error)) *tls.Config {
	return &tls.Config{
		MinVersion:     tls.VersionTLS12,
		NextProtos:     []string{"postgresql"},
		GetCertificate: certificate,
	}
}

// servedCertificate is config for one connection, and the certificate its handshake served once it ran: Go's
// ConnectionState names the client's certificates, never the server's own, and SCRAM-SHA-256-PLUS binds to that one
// (serverEndPoint). The copy issues no session tickets, so every handshake is a full one that chooses a certificate,
// as PostgreSQL turns tickets off too. A config that picks another config per client (GetConfigForClient) serves
// without telling, and its connections are offered no channel binding.
func servedCertificate(config *tls.Config) (own *tls.Config, served func() *tls.Certificate) {
	own = config.Clone()
	own.SessionTicketsDisabled = true
	var chosen *tls.Certificate
	get, certificates := config.GetCertificate, config.Certificates
	// crypto/tls asks GetCertificate only when Certificates is empty or the client named a host, so the copy holds
	// none and chooseCertificate chooses among them
	own.Certificates = nil
	own.GetCertificate = func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
		certificate, err := chooseCertificate(get, certificates, hello)
		if err != nil {
			return nil, err
		}
		chosen = certificate
		return certificate, nil
	}
	return own, func() *tls.Certificate { return chosen }
}

// chooseCertificate chooses as crypto/tls does: what get gives (asked when there are no certificates or the client
// named a host), else the one certificate, else the first the client supports, else the first (NameToCertificate,
// deprecated since Go 1.14, is not read).
func chooseCertificate(get func(*tls.ClientHelloInfo) (*tls.Certificate, error), certificates []tls.Certificate,
	hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	if get != nil && (len(certificates) == 0 || hello.ServerName != "") {
		if certificate, err := get(hello); certificate != nil || err != nil {
			return certificate, err
		}
	}
	switch len(certificates) {
	case 0:
		return nil, errors.New("tls: no certificates configured")
	case 1:
		return &certificates[0], nil
	}
	for index := range certificates {
		if hello.SupportsCertificate(&certificates[index]) == nil {
			return &certificates[index], nil
		}
	}
	return &certificates[0], nil
}

// ClientAuthorityFile reads the authorities whose certificates a client of another machine presents
// (Server.ClientAuthorities): a PEM file of one certificate or several, as PostgreSQL's ssl_ca_file. It is read once;
// a changed file takes a restart, as PostgreSQL's takes a reload.
func ClientAuthorityFile(path string) (*x509.CertPool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("pgwire: client authority: %w", err)
	}
	authorities := x509.NewCertPool()
	if !authorities.AppendCertsFromPEM(data) {
		return nil, fmt.Errorf("pgwire: client authority %s: no PEM certificate in it", path)
	}
	return authorities, nil
}

// BindsChannel reports whether SCRAM-SHA-256-PLUS can bind to certificate: whether its signature is made with a hash,
// which an Ed25519 certificate's is not (serverEndPoint). A host requiring channel binding checks its certificate with
// it before it listens.
func BindsChannel(certificate *tls.Certificate) bool {
	return serverEndPoint(certificate) != nil
}

// serverEndPoint is the channel binding data of tls-server-end-point (RFC 5929 section 4.1), the one type PostgreSQL
// binds SCRAM-SHA-256-PLUS to: the hash of the server's certificate, by the hash its signature is made with, SHA-256
// for MD5 and SHA-1. Nil for a certificate whose signature names no hash (Ed25519): its connections are offered no
// channel binding, where PostgreSQL fails the exchange.
func serverEndPoint(certificate *tls.Certificate) []byte {
	if certificate == nil || len(certificate.Certificate) == 0 {
		return nil
	}
	raw := certificate.Certificate[0]
	leaf := certificate.Leaf
	if leaf == nil || !bytes.Equal(leaf.Raw, raw) {
		parsed, err := x509.ParseCertificate(raw)
		if err != nil {
			return nil
		}
		leaf = parsed
	}
	var sum hash.Hash
	switch leaf.SignatureAlgorithm {
	case x509.MD5WithRSA, x509.SHA1WithRSA, x509.DSAWithSHA1, x509.ECDSAWithSHA1,
		x509.SHA256WithRSA, x509.SHA256WithRSAPSS, x509.DSAWithSHA256, x509.ECDSAWithSHA256:
		sum = sha256.New()
	case x509.SHA384WithRSA, x509.SHA384WithRSAPSS, x509.ECDSAWithSHA384:
		sum = sha512.New384()
	case x509.SHA512WithRSA, x509.SHA512WithRSAPSS, x509.ECDSAWithSHA512:
		sum = sha512.New()
	default:
		return nil
	}
	sum.Write(raw)
	return sum.Sum(nil)
}

// CertificateFiles is the certificate of a PEM file and the key of another, every name the client connects with
// answered with it. They are read now, so a pair that does not load fails before anything listens, and again at a
// handshake once either file changed (a renewal written over them); while the new pair does not load, the one before
// answers.
func CertificateFiles(certificate, key string) (func(*tls.ClientHelloInfo) (*tls.Certificate, error), error) {
	files := &certificateFiles{certificate: certificate, key: key}
	if err := files.load(); err != nil {
		return nil, err
	}
	return files.get, nil
}

type certificateFiles struct {
	certificate, key string

	mutex   sync.Mutex
	loaded  *tls.Certificate
	changed [2]time.Time // the files' times when loaded was read
}

func (files *certificateFiles) times() ([2]time.Time, error) {
	var times [2]time.Time
	for position, path := range []string{files.certificate, files.key} {
		info, err := os.Stat(path)
		if err != nil {
			return times, err
		}
		times[position] = info.ModTime()
	}
	return times, nil
}

func (files *certificateFiles) load() error {
	times, err := files.times()
	if err != nil {
		return fmt.Errorf("pgwire: TLS certificate: %w", err)
	}
	pair, err := tls.LoadX509KeyPair(files.certificate, files.key)
	if err != nil {
		return fmt.Errorf("pgwire: TLS certificate %s with key %s: %w", files.certificate, files.key, err)
	}
	files.loaded, files.changed = &pair, times
	return nil
}

func (files *certificateFiles) get(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	files.mutex.Lock()
	defer files.mutex.Unlock()
	if times, err := files.times(); err == nil && times != files.changed {
		_ = files.load() // the pair before answers until the new one loads
	}
	return files.loaded, nil
}

// hostnamePattern is a name the AutoSSL store may hold a certificate of: labels of letters, digits and inner hyphens,
// two at least. Nothing else reaches the folder: no path, no file of the store that is no certificate.
var hostnamePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$`)

// CachedCertificates is the certificate of the name a client connects with (SNI) out of the website's AutoSSL store,
// the folder autocert.DirCache writes: <name> holds the ECDSA certificate, <name>+rsa the RSA one, each the private key
// then the chain, in PEM. Only a hostname that allowed admits is read, and only a certificate valid now for the name
// answers. It never asks Let's Encrypt: the website gets the certificates and renews them. A file is read again when it
// changed.
func CachedCertificates(folder string, allowed func(name string) bool) func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	store := &certificateStore{folder: folder, allowed: allowed, read: map[string]storedCertificate{}}
	return store.get
}

type certificateStore struct {
	folder  string
	allowed func(string) bool

	mutex sync.Mutex
	read  map[string]storedCertificate // by file name
}

type storedCertificate struct {
	certificate *tls.Certificate
	changed     time.Time
}

func (store *certificateStore) get(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	name := strings.TrimSuffix(strings.ToLower(hello.ServerName), ".")
	if name == "" {
		return nil, errors.New("pgwire: the client named no host (SNI): connect by the name of a site of this host, or give the port a certificate of its own")
	}
	if !hostnamePattern.MatchString(name) || !store.allowed(name) {
		return nil, fmt.Errorf("pgwire: %q is no site of this host: connect by the name of a site, or give the port a certificate of its own", name)
	}
	var failures []error
	var valid *tls.Certificate // one the client may not take: the handshake then says why
	for _, file := range []string{name, name + "+rsa"} {
		certificate, err := store.certificate(file, name)
		if err == nil {
			if hello.SupportsCertificate(certificate) == nil {
				return certificate, nil
			}
			if valid == nil {
				valid = certificate
			}
			continue
		}
		if !errors.Is(err, os.ErrNotExist) {
			failures = append(failures, err)
		}
	}
	if valid != nil {
		return valid, nil
	}
	return nil, errors.Join(append([]error{fmt.Errorf("pgwire: no certificate of %s valid now in %s: the website gets it from Let's Encrypt (AutoSSL), or give the port a certificate of its own", name, store.folder)}, failures...)...)
}

// certificate is the certificate in the store's file, read again when the file changed, refused unless valid now for
// name.
func (store *certificateStore) certificate(file, name string) (*tls.Certificate, error) {
	path := filepath.Join(store.folder, file)
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	store.mutex.Lock()
	defer store.mutex.Unlock()
	kept, found := store.read[file]
	if !found || !kept.changed.Equal(info.ModTime()) {
		certificate, err := readStoredCertificate(path)
		if err != nil {
			return nil, err
		}
		kept = storedCertificate{certificate: certificate, changed: info.ModTime()}
		store.read[file] = kept
	}
	leaf := kept.certificate.Leaf
	now := time.Now()
	if now.Before(leaf.NotBefore) || now.After(leaf.NotAfter) {
		return nil, fmt.Errorf("pgwire: the certificate of %s in %s is valid from %s to %s", name, path, leaf.NotBefore.Format(time.DateOnly), leaf.NotAfter.Format(time.DateOnly))
	}
	if err := leaf.VerifyHostname(name); err != nil {
		return nil, fmt.Errorf("pgwire: the certificate in %s: %w", path, err)
	}
	return kept.certificate, nil
}

// readStoredCertificate reads a file of autocert's store: a private key and certificates, in PEM.
func readStoredCertificate(path string) (*tls.Certificate, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var keys, certificates []byte
	for rest := data; ; {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		switch {
		case block.Type == "CERTIFICATE":
			certificates = append(certificates, pem.EncodeToMemory(block)...)
		case strings.HasSuffix(block.Type, "PRIVATE KEY"):
			keys = append(keys, pem.EncodeToMemory(block)...)
		}
	}
	pair, err := tls.X509KeyPair(certificates, keys)
	if err != nil {
		return nil, fmt.Errorf("pgwire: %s: %w", path, err)
	}
	if pair.Leaf == nil {
		if pair.Leaf, err = x509.ParseCertificate(pair.Certificate[0]); err != nil {
			return nil, fmt.Errorf("pgwire: %s: %w", path, err)
		}
	}
	return &pair, nil
}
