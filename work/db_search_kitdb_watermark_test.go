package work

import (
	"bytes"
	"errors"
	"testing"

	kitdbengine "github.com/kitwork/engine/kitdb"
	"github.com/kitwork/engine/value"
)

func TestKitDBSearchWatermarkRoundTripsAndRejectsCorruption(t *testing.T) {
	want := kitDBSearchWatermark{
		Cursor: kitdbengine.HistoryCursor{
			DatabaseID:  "00112233445566778899aabbccddeeff",
			Transaction: 42,
			Checksum:    0x1234abcd,
		},
		StructID:         "ffeeddccbbaa99887766554433221100",
		IdentifierLayout: kitDBSearchIdentifierLayoutLogicalRowKey,
		RowGeneration:    7,
		RowEpoch:         9,
	}
	encoded, err := encodeKitDBSearchWatermark(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeKitDBSearchWatermark(encoded)
	if err != nil || got != want {
		t.Fatalf("watermark round trip = %#v, %v; want %#v", got, err, want)
	}
	encoded[55] ^= 0xff
	if _, err := decodeKitDBSearchWatermark(encoded); !errors.Is(err, errKitDBSearchWatermark) {
		t.Fatalf("corrupt watermark error = %v", err)
	}
}

func TestKitDBTextIntegerSearchIdentifierMatchesMigratedCompositeLayout(t *testing.T) {
	definition := &StructDef{
		ID: stableSchemaID("struct", "shopping"), Name: "shopping",
		Fields: []StructFieldDef{
			{ID: stableSchemaID("field", "shopping.merchant"), Tag: 1, Name: "merchant", Kind: "text", Primary: true, PrimaryOrder: 1},
			{ID: stableSchemaID("field", "shopping.id"), Tag: 2, Name: "id", Kind: "integer", Primary: true, PrimaryOrder: 2},
		},
	}
	refreshStructLookups(definition)
	table := &SchemaTable{table: "shopping", definition: definition}
	rowKey, err := kitDBRowKeyForRow(definition, map[string]value.Value{
		"merchant": value.New("shopee"), "id": value.New(int64(241_495_921)),
	})
	if err != nil {
		t.Fatal(err)
	}
	identifier, err := table.kitDBSearchDocumentID(kitDBStoredRow{key: rowKey})
	if err != nil {
		t.Fatal(err)
	}
	if want := "73686f706565:800000000e64ef71"; identifier != want {
		t.Fatalf("composite search identifier = %q, want %q", identifier, want)
	}
	decoded, err := table.kitDBSearchRowKey(identifier)
	if err != nil || !bytes.Equal(decoded, rowKey) {
		t.Fatalf("decoded row key = %x, %v; want %x", decoded, err, rowKey)
	}

	cursor := kitdbengine.HistoryCursor{DatabaseID: "00112233445566778899aabbccddeeff"}
	watermark := table.kitDBSearchWatermark(cursor)
	if watermark.IdentifierLayout != kitDBSearchIdentifierLayoutTextInteger ||
		!table.kitDBSearchWatermarkMatches(watermark, cursor.DatabaseID) {
		t.Fatalf("composite watermark layout = %#v", watermark)
	}
	watermark.IdentifierLayout = kitDBSearchIdentifierLayoutLogicalRowKey
	if table.kitDBSearchWatermarkMatches(watermark, cursor.DatabaseID) {
		t.Fatal("watermark accepted an incompatible identifier layout")
	}
}

func TestKitDBSearchWatermarkRejectsUnversionedIdentifierLayout(t *testing.T) {
	want := kitDBSearchWatermark{
		Cursor: kitdbengine.HistoryCursor{
			DatabaseID: "00112233445566778899aabbccddeeff",
		},
		StructID: "ffeeddccbbaa99887766554433221100",
	}
	if _, err := encodeKitDBSearchWatermark(want); !errors.Is(err, errKitDBSearchWatermark) {
		t.Fatalf("unversioned watermark encode error = %v", err)
	}
}

func TestKitDBSearchSignatureProofBindsIdentifierLayout(t *testing.T) {
	composite := &StructDef{
		ID: stableSchemaID("struct", "shopping"), Name: "shopping",
		Fields: []StructFieldDef{
			{Name: "merchant", Kind: "text", Primary: true, PrimaryOrder: 1},
			{Name: "id", Kind: "integer", Primary: true, PrimaryOrder: 2},
		},
	}
	logical := &StructDef{
		ID: stableSchemaID("struct", "products"), Name: "products",
		Fields: []StructFieldDef{{Name: "id", Kind: "text", Primary: true}},
	}
	compositeTable := &SchemaTable{engine: "kitdb", definition: composite}
	logicalTable := &SchemaTable{engine: "kitdb", definition: logical}
	proof := compositeTable.proveKitDBSearchSignature("t:8114")
	if boundary, ok := compositeTable.verifyKitDBSearchSignatureProof(proof); !ok || boundary != "t:8114" {
		t.Fatalf("composite proof = (%q, %t), encoded %q", boundary, ok, proof)
	}
	if _, ok := logicalTable.verifyKitDBSearchSignatureProof(proof); ok {
		t.Fatal("logical row-key layout accepted a text/integer proof")
	}
	if _, ok := compositeTable.verifyKitDBSearchSignatureProof("t:8114"); ok {
		t.Fatal("unversioned content signature was accepted as an identifier proof")
	}
}

func TestStoredSearchSignatureAcceptsOneConventionalLineEnding(t *testing.T) {
	for _, encoded := range []string{"t:8114", "t:8114\n", "t:8114\r\n"} {
		if got, err := decodeStoredSearchSignature([]byte(encoded)); err != nil || got != "t:8114" {
			t.Fatalf("decode %q = (%q, %v)", encoded, got, err)
		}
	}
	for _, encoded := range []string{"", " t:8114", "t:8114 ", "t:8114\n\n"} {
		if _, err := decodeStoredSearchSignature([]byte(encoded)); err == nil {
			t.Fatalf("invalid signature %q was accepted", encoded)
		}
	}
}
