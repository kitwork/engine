package main

import (
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestReadEnvValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	content := "# source\nOTHER=value\nKITDB_MIGRATE_PG_URL='postgresql://reader:p%23w@example/app'\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write env: %v", err)
	}
	value, found, err := readEnvValue(path, defaultSourceEnv)
	if err != nil {
		t.Fatalf("read env: %v", err)
	}
	if !found || value != "postgresql://reader:p%23w@example/app" {
		t.Fatalf("unexpected env value: found=%t value=%q", found, value)
	}
}

func TestReadEnvValueRejectsDuplicateSecret(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	content := "KITDB_MIGRATE_PG_URL=first\nKITDB_MIGRATE_PG_URL=second\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write env: %v", err)
	}
	if _, _, err := readEnvValue(path, defaultSourceEnv); err == nil {
		t.Fatal("expected duplicate declaration failure")
	}
}

func TestDecodeEnvValueRejectsUnterminatedQuote(t *testing.T) {
	if _, err := decodeEnvValue("'unterminated"); err == nil {
		t.Fatal("expected unterminated quote failure")
	}
}

func TestOverrideURLDatabasePreservesCredentialsWithoutExposingThem(t *testing.T) {
	overridden, err := overrideURLDatabase(
		"postgresql://reader:p%40ss@example.test:5432/wrong?sslmode=require",
		"app db",
	)
	if err != nil {
		t.Fatalf("override database: %v", err)
	}
	parsed, err := url.Parse(overridden)
	if err != nil {
		t.Fatalf("parse overridden URL: %v", err)
	}
	password, _ := parsed.User.Password()
	if parsed.Path != "/app db" || password != "p@ss" || parsed.Query().Get("sslmode") != "require" {
		t.Fatalf("override changed unrelated URL components")
	}
}
