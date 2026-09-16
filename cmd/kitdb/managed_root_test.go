package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/kitwork/engine/kitdb/managed"
)

func TestOperatorManagedRootJourney(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".kitdb")
	runAndDecode(t, "init-root", root)
	if _, err := os.Stat(filepath.Join(root, managed.CatalogDirectory, "data.kitdb")); err != nil {
		t.Fatalf("new root did not create .catalog: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, managed.LegacySystemDirectory)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("new root created legacy .system: %v", err)
	}
	runAndDecode(t, "query", "--create", filepath.Join(root, "shop", "data.kitdb"),
		"CREATE TABLE products (id BIGINT PRIMARY KEY, name TEXT)")
	runAndDecode(t, "register-database", root, "shop", "shop")
	runAndDecode(t, "register-database", root, "shop", "shop")
	response := runAndDecode(t, "root-catalog", root)
	var catalog managed.Catalog
	if err := json.Unmarshal(response.Result, &catalog); err != nil {
		t.Fatal(err)
	}
	if catalog.Version != 1 || len(catalog.Databases) != 1 || catalog.Databases[0].Name != "shop" {
		t.Fatalf("catalog = %+v", catalog)
	}
	for _, args := range [][]string{{"init-root"}, {"register-database", root, "shop"}, {"root-catalog"}, {"init-root", root}} {
		var output bytes.Buffer
		if err := run(context.Background(), args, &output); err == nil {
			t.Errorf("accepted invalid/repeated command %v", args)
		}
	}
}
