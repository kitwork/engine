package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kitwork/engine/kitdb/pgwire"
)

// clientAuthority of app.database (config_app_database.go, kitwork.go resolveAppDatabasePaths, app_database_runtime.go;
// KITWORK_KITDB.md câu 13.11, 08/11/2026): the authorities whose certificate a client of another machine presents
// besides its password (kitdb/pgwire TestClientCertificates tries it on the wire).

// clientAuthority is the path of a file, read from the manifest's folder; the port's server is given its authorities.
//
// CONTROL (08/11/2026): with resolveAppDatabasePaths leaving it as written, "the path from the manifest's folder:
// \"ca/clients.pem\""; with appDatabaseServer not passing the authorities on, "the server has no client authorities".
func TestAppDatabaseClientAuthorityOption(t *testing.T) {
	base := func(value interface{}) map[string]interface{} {
		return map[string]interface{}{"path": "./.database/", "port": 5455, "password": "p", "clientAuthority": value}
	}
	config, err := parseAppDatabaseConfig(base("ca/clients.pem"))
	if err != nil || config.ClientAuthority != "ca/clients.pem" {
		t.Fatalf("clientAuthority: %q, %v", config.ClientAuthority, err)
	}
	for _, value := range []interface{}{"", " ", true, map[string]interface{}{"file": "a.pem"}} {
		if _, err := parseAppDatabaseConfig(base(value)); err == nil || !strings.Contains(err.Error(), "clientAuthority") {
			t.Errorf("%v: %v, want an error naming clientAuthority", value, err)
		}
	}

	folder := t.TempDir()
	cfg := &Config{AppDatabases: []AppDatabaseConfig{config}}
	if err := resolveAppDatabasePaths(cfg, filepath.Join(folder, "application.work")); err != nil {
		t.Fatal(err)
	}
	if got, want := cfg.AppDatabases[0].ClientAuthority, filepath.Join(folder, "ca", "clients.pem"); got != want {
		t.Errorf("the path from the manifest's folder: %q, want %q", got, want)
	}

	authority := newPortAuthority(t)
	clients, err := pgwire.ClientAuthorityFile(authority.file)
	if err != nil {
		t.Fatal(err)
	}
	if server := appDatabaseServer(config, &appDatabaseGateway{}, nil, clients); server.ClientAuthorities != clients {
		t.Error("the server has no client authorities")
	}
}

// A clientAuthority file that does not exist, or holds no certificate, stops the port before it listens.
//
// CONTROL (08/11/2026): with the file not read at start, "a missing clientAuthority: <nil>, want the error naming it".
func TestAppDatabaseClientAuthorityFile(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".database"), 0o755); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(root, "empty.pem")
	if err := os.WriteFile(empty, []byte("no certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, file := range map[string]string{"a missing clientAuthority": filepath.Join(root, "missing.pem"), "a file of no certificate": empty} {
		config := AppDatabaseConfig{Path: filepath.Join(root, ".database"), Host: "127.0.0.1", Port: freePort(t), User: "kitdb",
			Password: appDatabaseTestPassword, ClientAuthority: file}
		runtimes, _, err := startAppDatabaseRuntimes(context.Background(), []AppDatabaseConfig{config}, appDatabaseApps{})
		for _, runtime := range runtimes {
			_ = runtime.Close()
		}
		if err == nil || !strings.Contains(err.Error(), "clientAuthority") {
			t.Errorf("%s: %v, want the error naming it", name, err)
		}
	}
}
