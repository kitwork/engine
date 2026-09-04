package work

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	kitdbengine "github.com/kitwork/engine/kitdb"
	"github.com/kitwork/engine/kitdb/relational"
	"github.com/kitwork/engine/value"
)

func TestStandaloneBigIntFailsClosedInLegacyVMBridge(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bigint.kitdb")
	standalone, err := relational.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer standalone.Close()
	if _, err := standalone.Execute(context.Background(), `CREATE TABLE numbers (id BIGINT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	// Decode through the real adapter, before it can turn int64 into VM Number.
	if err := standalone.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := kitdbengine.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	entry, found, err := db.CatalogStructByName("numbers")
	if err != nil || !found {
		t.Fatalf("catalog: %v %v", found, err)
	}
	if _, err := decodeKitDBCatalog(entry.Definition, entry.Name); err == nil || !strings.Contains(err.Error(), "standalone BIGINT") {
		t.Fatalf("unsafe adapter admission: %v", err)
	}
}

func TestStandaloneConstrainedNumericFailsClosedInLegacyVMBridge(t *testing.T) {
	path := filepath.Join(t.TempDir(), "numeric.kitdb")
	standalone, err := relational.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := standalone.Execute(context.Background(), `CREATE TABLE ledger (id INTEGER PRIMARY KEY, amount NUMERIC(18,2))`); err != nil {
		t.Fatal(err)
	}
	if err := standalone.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := kitdbengine.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	entry, found, err := db.CatalogStructByName("ledger")
	if err != nil || !found {
		t.Fatalf("catalog: %v %v", found, err)
	}
	if _, err := decodeKitDBCatalog(entry.Definition, entry.Name); err == nil || !strings.Contains(err.Error(), "schema IR version 4") {
		t.Fatalf("unsafe constrained NUMERIC adapter admission: %v", err)
	}
}

func TestStandaloneAndKitworkShareCatalogRowAndKeyFormats(t *testing.T) {
	t.Run("standalone_to_kitwork", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "standalone.kitdb")
		standalone, err := relational.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := standalone.Execute(context.Background(), `
			CREATE TABLE products (
				merchant TEXT NOT NULL,
				id INTEGER NOT NULL,
				sku TEXT UNIQUE,
				title TEXT NOT NULL,
				PRIMARY KEY (merchant, id)
			)
		`); err != nil {
			t.Fatal(err)
		}
		if _, err := standalone.Execute(context.Background(), `
			INSERT INTO products (merchant, id, sku, title)
			VALUES ('shopee', 42, 'SKU-42', 'Standalone')
		`); err != nil {
			t.Fatal(err)
		}
		if err := standalone.Close(); err != nil {
			t.Fatal(err)
		}

		database, err := kitdbengine.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer database.Close()
		entry, found, err := database.CatalogStructByName("products")
		if err != nil || !found {
			t.Fatalf("catalog lookup found=%v err=%v", found, err)
		}
		definition, err := decodeKitDBCatalog(entry.Definition, entry.Name)
		if err != nil {
			t.Fatal(err)
		}
		key, err := kitDBRowKeyForRow(definition, map[string]value.Value{
			"merchant": value.New("shopee"), "id": value.New(int64(42)),
		})
		if err != nil {
			t.Fatal(err)
		}
		encoded, found, err := database.Get(key)
		if err != nil || !found {
			t.Fatalf("row lookup found=%v err=%v", found, err)
		}
		decoded, err := decodeKitDBRow(definition, encoded)
		if err != nil {
			t.Fatal(err)
		}
		if decoded.values["title"].String() != "Standalone" {
			t.Fatalf("Kitwork decoded row = %#v", decoded.values)
		}
	})

	t.Run("kitwork_to_standalone", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "kitwork.kitdb")
		definition := bindStructDef("links", nil, map[string]*ColumnSpec{
			"id":    {kind: "kitid", primary: true, notNull: true, seq: 1},
			"title": {kind: "text", notNull: true, seq: 2},
		})
		catalog, err := json.Marshal(definition)
		if err != nil {
			t.Fatal(err)
		}
		row := map[string]value.Value{
			"id": value.New("link-1"), "title": value.New("Kitwork"),
		}
		key, err := kitDBRowKeyForRow(definition, row)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := encodeKitDBRow(definition, row, nil)
		if err != nil {
			t.Fatal(err)
		}
		database, err := kitdbengine.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		transaction, err := database.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if err := transaction.DefineStruct(catalog); err != nil {
			t.Fatal(err)
		}
		if err := transaction.Put(key, encoded); err != nil {
			t.Fatal(err)
		}
		if _, err := transaction.Commit(); err != nil {
			t.Fatal(err)
		}
		if err := database.Close(); err != nil {
			t.Fatal(err)
		}

		standalone, err := relational.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer standalone.Close()
		result, err := standalone.Execute(context.Background(), `
			SELECT title FROM links WHERE id = 'link-1'
		`)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Rows) != 1 || result.Rows[0][0] != "Kitwork" {
			t.Fatalf("standalone decoded row = %#v", result.Rows)
		}
	})
}
