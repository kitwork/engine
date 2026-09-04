package postgres

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestValidateShoppingSearchCatalogRequiresExactProjectionSchema(t *testing.T) {
	definition := map[string]any{"fields": []map[string]any{
		{"name": "_key"},
		{"name": "name", "searchable": true, "searchWeight": 5},
		{"name": "description", "searchable": true, "searchWeight": 2},
		{"name": "content", "searchable": true, "searchWeight": 1},
		{"name": "brand", "searchable": true, "searchWeight": 2},
	}}
	encoded, err := json.Marshal(definition)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateShoppingSearchCatalog(encoded); err != nil {
		t.Fatal(err)
	}
	definition["fields"].([]map[string]any)[1]["searchWeight"] = 4
	encoded, _ = json.Marshal(definition)
	if err := validateShoppingSearchCatalog(encoded); err == nil {
		t.Fatal("mismatched search weight was accepted")
	}
}

func TestWriteShoppingSearchSignaturePublishesExactBoundary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "projection.signature")
	if err := writeShoppingSearchSignature(path, "42:catalog"); err != nil {
		t.Fatal(err)
	}
	encoded, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != "42:catalog" {
		t.Fatalf("signature = %q", encoded)
	}
}

func TestReadShoppingSearchSourceSignaturePreservesContentBoundary(t *testing.T) {
	data := t.TempDir()
	root := filepath.Join(data, "search")
	state := filepath.Join(data, "search-state")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "source.signature"), []byte("t:8114"), 0o600); err != nil {
		t.Fatal(err)
	}
	signature, found, err := readShoppingSearchSourceSignature(root, "source")
	if err != nil || !found || signature != "t:8114" {
		t.Fatalf("source signature = (%q, %t, %v)", signature, found, err)
	}
}

func TestShoppingSearchAdoptionSignatureDeclaresIdentifierLayout(t *testing.T) {
	if got, want := shoppingSearchAdoptionSignaturePrefix+"t:8114", "id:text-integer-v1|t:8114"; got != want {
		t.Fatalf("adoption signature = %q, want %q", got, want)
	}
}
