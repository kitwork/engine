package database

import (
	"strings"
	"testing"
)

func TestConfigBuildDSNUsesExplicitConnectorURL(t *testing.T) {
	config := Config{
		Type: "postgres", URL: "postgresql://kitwork:secret@db.internal/app?sslmode=require",
		Host: "ignored", Port: 5432, Name: "ignored",
	}
	dsn, err := config.BuildDSN()
	if err != nil {
		t.Fatal(err)
	}
	if dsn != config.URL {
		t.Fatalf("DSN = %q, want explicit URL", dsn)
	}
}

func TestConfigConnectRejectsUnlinkedConnectorClearly(t *testing.T) {
	_, err := (&Config{Type: "connector-not-linked", URL: "custom://example"}).Connect()
	if err == nil || !strings.Contains(err.Error(), "not linked") {
		t.Fatalf("unlinked connector error = %v", err)
	}
}

func TestConfigBuildDSNAcceptsKitSQLURL(t *testing.T) {
	config := Config{Type: "kitsql", URL: "kitsql://kitdb:secret@db.example/shop"}
	dsn, err := config.BuildDSN()
	if err != nil {
		t.Fatal(err)
	}
	if dsn != config.URL {
		t.Fatalf("DSN = %q", dsn)
	}
}
