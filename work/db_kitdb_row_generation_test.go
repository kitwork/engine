package work

import (
	"bytes"
	"testing"

	"github.com/kitwork/engine/value"
)

func TestKitDBRowGenerationMetadataRoundTripsAndRejectsCorruption(t *testing.T) {
	metadata := kitDBRowGenerationMetadata{
		Epoch: 9, Active: 27,
		Retired: [][]byte{{0x11, 0x02}, {0x10, 0x01}},
	}
	encoded, err := encodeKitDBRowGenerationMetadata(metadata)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeKitDBRowGenerationMetadata(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Epoch != metadata.Epoch || decoded.Active != metadata.Active ||
		len(decoded.Retired) != 2 || bytes.Compare(decoded.Retired[0], decoded.Retired[1]) >= 0 {
		t.Fatalf("decoded row-generation metadata = %#v", decoded)
	}

	corrupt := bytes.Clone(encoded)
	corrupt[len(corrupt)/2] ^= 0x40
	if _, err := decodeKitDBRowGenerationMetadata(corrupt); err == nil {
		t.Fatal("corrupt row-generation metadata was accepted")
	}
	if _, err := encodeKitDBRowGenerationMetadata(kitDBRowGenerationMetadata{
		Retired: [][]byte{{0x10}, {0x10}},
	}); err == nil {
		t.Fatal("duplicate retired row prefix was accepted")
	}
}

func TestKitDBPhysicalRowGenerationPreservesLogicalIdentity(t *testing.T) {
	definition := bindStructDef("products", nil, map[string]*ColumnSpec{
		"id": {kind: "text", primary: true, seq: 1},
	})
	logical, err := kitDBRowKey(definition, value.New("product-1"))
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := kitDBPhysicalRowKey(definition, logical, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(legacy, logical) {
		t.Fatalf("generation-zero key = %x, want %x", legacy, logical)
	}

	physical, err := kitDBPhysicalRowKey(definition, logical, 42)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(physical, logical) || len(physical) <= len(logical) || physical[0] != kitDBShadowRowNamespace {
		t.Fatalf("shadow physical key = %x", physical)
	}
	restored, err := kitDBLogicalRowKey(definition, physical, 42)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(restored, logical) {
		t.Fatalf("restored logical key = %x, want %x", restored, logical)
	}
	if _, err := kitDBLogicalRowKey(definition, logical, 42); err == nil {
		t.Fatal("logical key was accepted as a generation-42 physical key")
	}
}
