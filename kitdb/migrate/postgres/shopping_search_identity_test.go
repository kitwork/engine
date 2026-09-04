package postgres

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveShoppingSearchProjectionIdentityIsTenantAndStorageScoped(t *testing.T) {
	tenant := filepath.Join(t.TempDir(), "shopping.local")
	first, err := ResolveShoppingSearchProjectionIdentity(tenant, "first.kitdb", "shopping")
	if err != nil {
		t.Fatal(err)
	}
	second, err := ResolveShoppingSearchProjectionIdentity(tenant, "second.kitdb", "shopping")
	if err != nil {
		t.Fatal(err)
	}
	if first.IndexKey == second.IndexKey || first.SchemaHash == "" {
		t.Fatalf("identity is not storage scoped: first=%#v second=%#v", first, second)
	}
	if !strings.HasPrefix(first.SignaturePath, first.TenantRoot) ||
		!strings.HasPrefix(first.IndexDirectory, first.ManagerRoot) {
		t.Fatalf("identity escaped tenant root: %#v", first)
	}
	directoryHash := sha256.Sum256([]byte(first.IndexKey))
	wantDirectory := hex.EncodeToString(directoryHash[:])
	if filepath.Base(first.IndexDirectory) != wantDirectory {
		t.Fatalf("index directory = %q, want %q", filepath.Base(first.IndexDirectory), wantDirectory)
	}
	if first.IndexKey == "db-7a59bb224ec71b14de9f26051dc2594f04e22246c329241de1e933efc513be70" {
		t.Fatal("shopping search identity still points at the pre-composite-key layout")
	}
}
