package postgres

import (
	"encoding/hex"
	"testing"
)

func TestSyntheticShoppingKeyIsCollisionFreeForDelimiterContent(t *testing.T) {
	first := syntheticShoppingKey("a:b", 12)
	second := syntheticShoppingKey("a", 12)
	third := syntheticShoppingKey("a:b", 13)
	if first == second || first == third || second == third {
		t.Fatalf("synthetic keys collided: %q %q %q", first, second, third)
	}
	merchantHex, err := hex.DecodeString(first[:len(first)-17])
	if err != nil || string(merchantHex) != "a:b" {
		t.Fatalf("synthetic merchant is not reversible: %q err=%v", merchantHex, err)
	}
}

func TestAdvanceMigrationChecksumDistinguishesNullAndEmpty(t *testing.T) {
	seed := migrationSeed("id", "source", "fingerprint", []string{"field"})
	nullChecksum := advanceMigrationChecksum(seed, [][]byte{nil})
	emptyChecksum := advanceMigrationChecksum(seed, [][]byte{{}})
	if nullChecksum == emptyChecksum {
		t.Fatal("canonical checksum conflates null and empty")
	}
}

func TestNormalizeMigrationConfigBounds(t *testing.T) {
	plan := TargetPlan{
		Format: TargetPlanFormat, TargetTable: "shopping", PrimaryStrategy: "native-composite",
		SourcePrimary: []string{"merchant", "id"},
	}
	config, err := normalizeMigrationConfig(MigrationConfig{
		SourceURL: "postgres://source", TargetURL: "postgres://target", TargetDB: "shadow", ID: "test", Plan: plan,
	})
	if err != nil {
		t.Fatalf("normalize migration config: %v", err)
	}
	if config.ChunkRows != defaultMigrationChunkRows || config.ChunkBytes != defaultMigrationChunkBytes {
		t.Fatalf("unexpected migration defaults: %+v", config)
	}
}

func TestCanonicalJSONIgnoresObjectKeyOrder(t *testing.T) {
	first, err := canonicalJSON([]byte(`{"b":2,"a":1}`))
	if err != nil {
		t.Fatalf("canonicalize first JSON: %v", err)
	}
	second, err := canonicalJSON([]byte(`{"a":1,"b":2}`))
	if err != nil {
		t.Fatalf("canonicalize second JSON: %v", err)
	}
	if string(first) != string(second) {
		t.Fatalf("canonical JSON differs: %s != %s", first, second)
	}
}
