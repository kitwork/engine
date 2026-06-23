package domain

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/kitwork/engine/database"
	"golang.org/x/crypto/acme/autocert"
)

// AllowedDomains holds the list of domains allowed by configuration
var Allows []string
var SkipLabels []string

// SitesDir, when set (e.g. "tenants/sites"), enables the single-tenant convention: a host is
// allowed for AutoSSL if <SitesDir>/<host>/ exists on disk. Dropping a domain folder is enough to
// get a certificate — no YAML whitelist entry and no DB registration. Empty = disabled.
var SitesDir string

// siteFolderExists reports whether <SitesDir>/<host> is an existing directory.
func siteFolderExists(host string) bool {
	if SitesDir == "" {
		return false
	}
	fi, err := os.Stat(filepath.Join(SitesDir, host))
	return err == nil && fi.IsDir()
}

// HostPolicy implements Let's Encrypt hostname whitelist dynamic validation
func HostPolicy(ctx context.Context, host string) error {
	// Allow local connection
	if host == "localhost" || host == "127.0.0.1" {
		return nil
	}

	cleanHost := strings.TrimPrefix(host, "www.")

	// Case 1: Check via YAML config domains whitelist
	for _, d := range Allows {
		if d == host || d == cleanHost {
			return nil
		}
	}

	// Case 1.5: single-tenant sites/ folder present on disk (drop-a-folder → cert, no config/DB).
	// Evaluated live, so a site added while running is served without a restart.
	if siteFolderExists(host) || siteFolderExists(cleanHost) {
		return nil
	}

	// Case 2: Check via Database registration

	exists, err := database.DomainSystemExists(host)

	if err != nil {
		fmt.Printf("error checking domain %s: %v\n", host, err)
	}

	if exists {
		return nil
	}

	return fmt.Errorf("domain %s is not registered on this platform", host)
}

// AutoSSL initializes the Let's Encrypt autocert manager and returns TLS config
func AutoSSL(Allows []string) *tls.Config {
	certDir := filepath.Join(".", "certs")
	_ = os.MkdirAll(certDir, 0700)

	m := &autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		HostPolicy: HostPolicy,
		Cache:      autocert.DirCache(certDir),
	}

	// Spin up ACME challenge response handler on port 80 in background
	go func() {
		fmt.Println("Starting ACME challenge handler on port :80...")
		// Non-ACME http traffic → forced to https (+ canonical/domain redirects).
		if err := http.ListenAndServe(":80", m.HTTPHandler(RedirectFallback())); err != nil {
			fmt.Printf("[ACME-HTTP] Error: %v\n", err)
		}
	}()

	return &tls.Config{
		GetCertificate: m.GetCertificate,
		NextProtos:     []string{"h2", "http/1.1", "acme-tls/1"},
		MinVersion:     tls.VersionTLS12,
	}
}
