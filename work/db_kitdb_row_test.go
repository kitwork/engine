package work

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	kitdbengine "github.com/kitwork/engine/kitdb"
	"github.com/kitwork/engine/value"
)

func TestKitDBRowValidationEnforcesSchemaTypes(t *testing.T) {
	tests := []struct {
		name    string
		kind    string
		input   value.Value
		wantErr string
	}{
		{name: "integer", kind: "integer", input: value.New(42)},
		{name: "integer rejects fraction", kind: "integer", input: value.New(4.2), wantErr: "expects an integer"},
		{name: "integer rejects text", kind: "integer", input: value.New("42"), wantErr: "expects an integer"},
		{name: "float", kind: "float", input: value.New(4.2)},
		{name: "boolean", kind: "bool", input: value.New(true)},
		{name: "boolean rejects text", kind: "bool", input: value.New("true"), wantErr: "expects a boolean"},
		{name: "decimal", kind: "decimal", input: value.New("12345678901234567890.125")},
		{name: "decimal exponent", kind: "decimal", input: value.New("1.25e-3")},
		{name: "decimal rejects text", kind: "decimal", input: value.New("12 coins"), wantErr: "expects decimal text"},
		{name: "json", kind: "json", input: value.New(map[string]value.Value{"ok": value.New(true)})},
		{name: "json rejects malformed", kind: "json", input: value.New("{"), wantErr: "expects valid JSON for json"},
		{name: "array", kind: "array", input: value.New([]value.Value{value.New(1), value.New(2)})},
		{name: "array rejects object", kind: "array", input: value.New(map[string]value.Value{"x": value.New(1)}), wantErr: "expects a JSON array"},
		{name: "vector", kind: "vector", input: value.New([]value.Value{value.New(0.25), value.New(-1)})},
		{name: "vector rejects text component", kind: "vector", input: value.New([]value.Value{value.New("x")}), wantErr: "vector component 0"},
		{name: "blob", kind: "blob", input: value.New([]byte{1, 2, 3})},
		{name: "blob rejects text", kind: "blob", input: value.New("bytes"), wantErr: "expects bytes"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			columns := map[string]*ColumnSpec{
				"id":      {kind: "text", primary: true, seq: 1},
				"payload": {kind: test.kind, notNull: true, seq: 2},
			}
			definition := bindStructDef("typed_"+strings.ReplaceAll(test.name, " ", "_"), nil, columns)
			row, message := coerceWriteRow(columns, map[string]value.Value{
				"id": value.New("row-1"), "payload": test.input,
			})
			if message != "" {
				t.Fatalf("coerce write: %s", message)
			}
			err := validateKitDBRow(definition, row)
			if test.wantErr == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("error = %v, want %q", err, test.wantErr)
			}
		})
	}
}

func TestKitDBBinaryRowRoundTripIsTypedAndDeterministic(t *testing.T) {
	definition := testKitDBRowDefinition(
		"id", "enabled", "amount", "negative", "large", "name", "blob", "at", "elapsed", "items", "metadata", "empty",
	)
	row := map[string]value.Value{
		"id":       value.New("product-1"),
		"enabled":  value.New(true),
		"amount":   value.New(42.5),
		"negative": value.New(-42),
		"large":    value.New(9_007_199_254_740_991),
		"name":     value.New("Ao thun Viet Nam"),
		"blob":     value.New([]byte{0x00, 0x7f, 0xff}),
		"at":       {K: value.Time, N: 1_700_000_000_123_456_789},
		"elapsed":  {K: value.Duration, N: 12_345_678},
		"items": value.New([]value.Value{
			value.New("first"), value.New(9), value.NewNil(),
		}),
		"metadata": value.New(map[string]value.Value{
			"nested": value.New(map[string]value.Value{"ok": value.New(true)}),
			"tags":   value.New([]value.Value{value.New("go"), value.New("db")}),
		}),
		"empty": value.NewNil(),
	}

	encoded, err := encodeKitDBRow(definition, row, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(encoded, kitDBRowMagic[:]) || encoded[4] != kitDBBinaryRowVersion {
		t.Fatalf("row header = %x, want KROW version %d", encoded[:min(len(encoded), 8)], kitDBBinaryRowVersion)
	}
	second, err := encodeKitDBRow(definition, row, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, second) {
		t.Fatal("the same logical row did not produce deterministic bytes")
	}

	decoded, err := decodeKitDBRow(definition, encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.legacy || len(decoded.unknown) != 0 {
		t.Fatalf("decoded format legacy=%t unknown=%d", decoded.legacy, len(decoded.unknown))
	}
	assertKitDBRowValuesEqual(t, decoded.values, row)
}

func TestKitDBProjectedRowDecodeMaterializesOnlySelectedFields(t *testing.T) {
	definition := testKitDBRowDefinition("id", "name", "description", "attributes")
	row := map[string]value.Value{
		"id":          value.New("product-1"),
		"name":        value.New("Highlands Coffee"),
		"description": value.New(strings.Repeat("Vietnamese coffee ", 32)),
		"attributes": value.New(map[string]value.Value{
			"options": value.New([]value.Value{value.New("large"), value.New("iced")}),
		}),
	}
	encoded, err := encodeKitDBRow(definition, row, nil)
	if err != nil {
		t.Fatal(err)
	}
	projected, err := decodeProjectedKitDBRow(definition, encoded, map[uint32]struct{}{
		definition.Fields[0].Tag: {},
		definition.Fields[1].Tag: {},
	})
	if err != nil {
		t.Fatal(err)
	}
	if projected.legacy || len(projected.unknown) != 0 || len(projected.values) != 2 {
		t.Fatalf("projected row = %#v", projected)
	}
	if projected.values["id"].String() != "product-1" || projected.values["name"].String() != "Highlands Coffee" {
		t.Fatalf("projected values = %#v", projected.values)
	}
	if _, exists := projected.values["description"]; exists {
		t.Fatal("unselected description was materialized")
	}
	if _, exists := projected.values["attributes"]; exists {
		t.Fatal("unselected attributes were materialized")
	}

	corrupt := bytes.Clone(encoded)
	corrupt[len(corrupt)-1] ^= 0xff
	if _, err := decodeProjectedKitDBRow(definition, corrupt, map[uint32]struct{}{}); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("projected checksum corruption error = %v", err)
	}
}

func TestKitDBBinaryRowPreservesUnknownTagsAcrossUpdate(t *testing.T) {
	older := testKitDBRowDefinition("id", "name")
	future := testKitDBRowDefinition("id", "name")
	future.Fields = append(future.Fields, StructFieldDef{
		ID: stableSchemaID("field", future.ID+":future"), Tag: 9, Name: "future", Kind: "blob", Position: 2,
	})
	future.NextFieldTag = 10
	refreshStructHash(future)

	original, err := encodeKitDBRow(future, map[string]value.Value{
		"id": value.New("p1"), "name": value.New("before"),
		"future": value.New([]byte{0xde, 0xad, 0xbe, 0xef}),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	readByOlder, err := decodeKitDBRow(older, original)
	if err != nil {
		t.Fatal(err)
	}
	if len(readByOlder.unknown) != 1 || readByOlder.unknown[0].tag != 9 {
		t.Fatalf("unknown fields = %#v, want tag 9", readByOlder.unknown)
	}
	readByOlder.values["name"] = value.New("after")
	rewritten, err := encodeKitDBRow(older, readByOlder.values, readByOlder.unknown)
	if err != nil {
		t.Fatal(err)
	}
	readByFuture, err := decodeKitDBRow(future, rewritten)
	if err != nil {
		t.Fatal(err)
	}
	if got := readByFuture.values["name"].String(); got != "after" {
		t.Fatalf("updated name = %q", got)
	}
	if got := readByFuture.values["future"].Bytes(); !bytes.Equal(got, []byte{0xde, 0xad, 0xbe, 0xef}) {
		t.Fatalf("future field was not preserved: %x", got)
	}
}

func TestKitDBLegacyJSONUsesPersistedRenameAliases(t *testing.T) {
	definition := testKitDBRowDefinition("id", "name")
	definition.Fields[1].Aliases = []string{"title"}
	refreshStructHash(definition)
	legacy, err := json.Marshal(value.New(map[string]value.Value{
		"id": value.New("p1"), "title": value.New("kept"),
	}))
	if err != nil {
		t.Fatal(err)
	}

	decoded, err := decodeKitDBRow(definition, legacy)
	if err != nil {
		t.Fatal(err)
	}
	if !decoded.legacy || decoded.values["name"].String() != "kept" {
		t.Fatalf("legacy decode = %#v", decoded)
	}
	if _, exists := decoded.values["title"]; exists {
		t.Fatal("legacy alias leaked as a second logical field")
	}
	upgraded, err := encodeKitDBRow(definition, decoded.values, decoded.unknown)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(upgraded, kitDBRowMagic[:]) {
		t.Fatal("legacy row was not upgraded to binary on write")
	}
}

func TestKitDBBinaryRowRejectsCorruptionAndNonCanonicalEncoding(t *testing.T) {
	definition := testKitDBRowDefinition("id")
	encoded, err := encodeKitDBRow(definition, map[string]value.Value{"id": value.New("p1")}, nil)
	if err != nil {
		t.Fatal(err)
	}

	corrupt := bytes.Clone(encoded)
	corrupt[len(corrupt)-1] ^= 0xff
	if _, err := decodeKitDBRow(definition, corrupt); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("checksum corruption error = %v", err)
	}

	futureVersion := bytes.Clone(encoded)
	futureVersion[4]++
	if _, err := decodeKitDBRow(definition, futureVersion); err == nil || !strings.Contains(err.Error(), "unsupported binary version") {
		t.Fatalf("future version error = %v", err)
	}

	body := encoded[kitDBRowHeaderSize:]
	nonCanonicalBody := append([]byte{0x81, 0x00}, body[1:]...)
	nonCanonical := bytes.Clone(encoded[:kitDBRowHeaderSize])
	binary.LittleEndian.PutUint32(nonCanonical[12:16], crc32.Checksum(nonCanonicalBody, kitDBRowCRCTable))
	nonCanonical = append(nonCanonical, nonCanonicalBody...)
	if _, err := decodeKitDBRow(definition, nonCanonical); err == nil || !strings.Contains(err.Error(), "non-canonical") {
		t.Fatalf("non-canonical field tag error = %v", err)
	}
}

func TestKitDBSchemaIRV1GetsStableTagsWithoutChangingRows(t *testing.T) {
	legacy := struct {
		Version int              `json:"version"`
		ID      string           `json:"id"`
		Name    string           `json:"name"`
		Hash    string           `json:"hash"`
		Fields  []StructFieldDef `json:"fields"`
	}{
		Version: 1, ID: stableSchemaID("struct", "products"), Name: "products", Hash: "legacy-hash",
		Fields: []StructFieldDef{
			{ID: stableSchemaID("field", "products:id"), Name: "id", Position: 0, Kind: "kitid", Primary: true, NotNull: true, Unique: true},
			{ID: stableSchemaID("field", "products:title"), Name: "title", Position: 1, Kind: "text"},
		},
	}
	encoded, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	upgraded, err := decodeKitDBCatalog(encoded, "products")
	if err != nil {
		t.Fatal(err)
	}
	if upgraded.Version != structIRVersion || !upgraded.catalogNeedsUpgrade || upgraded.catalogHash != "legacy-hash" {
		t.Fatalf("catalog upgrade metadata = %#v", upgraded)
	}
	if upgraded.Fields[0].Tag != 1 || upgraded.Fields[1].Tag != 2 || upgraded.NextFieldTag != 3 {
		t.Fatalf("assigned tags = %d, %d next=%d", upgraded.Fields[0].Tag, upgraded.Fields[1].Tag, upgraded.NextFieldTag)
	}
}

func TestKitDBCatalogV1AutoUpgradeKeepsLegacyJSONRows(t *testing.T) {
	current := testKitDBRowDefinition("id", "title")
	current.Name = "products"
	current.ID = stableSchemaID("struct", current.Name)
	current.Fields[0].ID = stableSchemaID("field", current.ID+":id")
	current.Fields[0].Kind = "kitid"
	current.Fields[0].Primary = true
	current.Fields[0].NotNull = true
	current.Fields[0].Unique = true
	current.Fields[1].ID = stableSchemaID("field", current.ID+":title")
	refreshStructHash(current)

	legacy := *current
	legacy.Version = 1
	legacy.Hash = "v1-catalog-hash"
	legacy.NextFieldTag = 0
	legacy.Fields = append([]StructFieldDef(nil), current.Fields...)
	for index := range legacy.Fields {
		legacy.Fields[index].Tag = 0
	}
	legacyCatalog, err := json.Marshal(&legacy)
	if err != nil {
		t.Fatal(err)
	}
	legacyRow, err := json.Marshal(value.New(map[string]value.Value{
		"id": value.New("p1"), "title": value.New("kept"),
	}))
	if err != nil {
		t.Fatal(err)
	}

	database, err := kitdbengine.Open(filepath.Join(t.TempDir(), "upgrade.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	rowKey, err := kitDBRowKey(current, value.New("p1"))
	if err != nil {
		t.Fatal(err)
	}
	transaction, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := transaction.DefineStruct(legacyCatalog); err != nil {
		t.Fatal(err)
	}
	if err := transaction.Put(rowKey, legacyRow); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Commit(); err != nil {
		t.Fatal(err)
	}

	if err := ensureKitDBCatalog(database, current, map[string]*StructDef{"products": current}, false, nil); err != nil {
		t.Fatalf("automatic catalog upgrade: %v", err)
	}
	persistedEntry, found, err := database.CatalogStructByID(current.ID)
	if err != nil || !found {
		t.Fatalf("read upgraded catalog: found=%t err=%v", found, err)
	}
	persisted, err := decodeKitDBCatalog(persistedEntry.Definition, "products")
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Version != structIRVersion || persisted.catalogNeedsUpgrade {
		t.Fatalf("persisted catalog version=%d needsUpgrade=%t", persisted.Version, persisted.catalogNeedsUpgrade)
	}
	storedRow, found, err := database.Get(rowKey)
	if err != nil || !found {
		t.Fatalf("read legacy row: found=%t err=%v", found, err)
	}
	decoded, err := decodeKitDBRow(current, storedRow)
	if err != nil {
		t.Fatal(err)
	}
	if !decoded.legacy || decoded.values["title"].String() != "kept" {
		t.Fatalf("legacy row after catalog upgrade = %#v", decoded)
	}
}

func TestKitDBFieldTagsSurviveReorderAndAllocateMonotonically(t *testing.T) {
	stored := testKitDBRowDefinition("id", "sku", "name")
	current := testKitDBRowDefinition("name", "added", "id", "sku")
	if err := reconcileKitDBDefinition(stored, current); err != nil {
		t.Fatal(err)
	}
	wantTags := map[string]uint32{"id": 1, "sku": 2, "name": 3, "added": 4}
	for _, field := range current.Fields {
		if got := field.Tag; got != wantTags[field.Name] {
			t.Errorf("field %q tag = %d, want %d", field.Name, got, wantTags[field.Name])
		}
	}
	if current.NextFieldTag != 5 {
		t.Fatalf("next field tag = %d, want 5", current.NextFieldTag)
	}
}

func TestKitDBPureRenameDoesNotRequireRowRewrite(t *testing.T) {
	stored := testKitDBRowDefinition("id", "title")
	current := testKitDBRowDefinition("id", "name")
	current.Fields[1].ID = stored.Fields[1].ID
	current.Fields[1].Tag = stored.Fields[1].Tag
	current.Fields[1].Aliases = []string{"title"}
	refreshStructHash(current)
	if kitDBMigrationNeedsRewrite(stored, current) {
		t.Fatal("a tag-stable rename without an index should be catalog-only")
	}
	if kitDBMigrationNeedsValidation(stored, current) {
		t.Fatal("a tag-stable rename should not scan row data")
	}

	stored.Fields[1].Indexes = []StructIndexMember{{}}
	current.Fields[1].Indexes = []StructIndexMember{{}}
	if !kitDBMigrationNeedsRewrite(stored, current) {
		t.Fatal("an indexed rename must rebuild its physical index")
	}
}

func TestKitDBMigrationScansOnlyTightenedDataConstraints(t *testing.T) {
	stored := testKitDBRowDefinition("id", "status")
	current := testKitDBRowDefinition("id", "status")
	stored.Fields[1].Enum = []string{"active", "disabled"}
	current.Fields[1].Enum = []string{"active", "disabled", "archived"}
	if kitDBMigrationNeedsValidation(stored, current) {
		t.Fatal("expanding an already-enforced enum should not scan rows")
	}
	current.Fields[1].Enum = []string{"active"}
	if !kitDBMigrationNeedsValidation(stored, current) {
		t.Fatal("tightening an enum must validate existing rows")
	}
	current.Fields[1].Enum = append([]string(nil), stored.Fields[1].Enum...)
	current.Fields[1].NotNull = true
	if !kitDBMigrationNeedsValidation(stored, current) {
		t.Fatal("adding not-null must validate existing rows")
	}
}

func FuzzKitDBBinaryRowDecoderDoesNotPanic(f *testing.F) {
	definition := testKitDBRowDefinition("id", "name")
	valid, err := encodeKitDBRow(definition, map[string]value.Value{
		"id": value.New("p1"), "name": value.New("KitDB"),
	}, nil)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(valid)
	f.Add([]byte(`{"id":"legacy"}`))
	f.Add([]byte("KROW"))
	f.Fuzz(func(t *testing.T, encoded []byte) {
		_, _ = decodeKitDBRow(definition, encoded)
	})
}

func BenchmarkKitDBRowCodec(b *testing.B) {
	definition := testKitDBRowDefinition("id", "sku", "name", "description", "price", "active", "metadata")
	row := map[string]value.Value{
		"id": value.New("01J9Z4YJ4N8W7TQ6PG3D5X2K1M"), "sku": value.New("KIT-001"),
		"name":        value.New("Ao thun cotton nam cao cap"),
		"description": value.New(strings.Repeat("Mo ta san pham KitDB. ", 20)),
		"price":       value.New(249_000), "active": value.New(true),
		"metadata": value.New(map[string]value.Value{"brand": value.New("Kitwork"), "stock": value.New(120)}),
	}
	binaryRow, err := encodeKitDBRow(definition, row, nil)
	if err != nil {
		b.Fatal(err)
	}
	jsonRow, err := json.Marshal(value.New(row))
	if err != nil {
		b.Fatal(err)
	}

	b.Run("binary_encode_validated", func(b *testing.B) {
		b.ReportAllocs()
		b.ReportMetric(float64(len(binaryRow)), "bytes/row")
		for range b.N {
			if _, err := encodeKitDBValidatedRow(definition, row, nil); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("binary_validate_encode", func(b *testing.B) {
		b.ReportAllocs()
		b.ReportMetric(float64(len(binaryRow)), "bytes/row")
		for range b.N {
			if _, err := encodeKitDBRow(definition, row, nil); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("binary_decode", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(binaryRow)))
		for range b.N {
			if _, err := decodeKitDBRow(definition, binaryRow); err != nil {
				b.Fatal(err)
			}
		}
	})
	projectedTags := map[uint32]struct{}{
		definition.Fields[0].Tag: {},
		definition.Fields[2].Tag: {},
		definition.Fields[3].Tag: {},
	}
	b.Run("binary_projected_decode", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(binaryRow)))
		for range b.N {
			if _, err := decodeProjectedKitDBRow(definition, binaryRow, projectedTags); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("legacy_json_decode", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(jsonRow)))
		for range b.N {
			if _, err := decodeKitDBRow(definition, jsonRow); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func testKitDBRowDefinition(names ...string) *StructDef {
	definition := &StructDef{
		Version: structIRVersion, ID: stableSchemaID("struct", "row-test"), Name: "row_test",
		NextFieldTag: uint32(len(names) + 1),
	}
	for index, name := range names {
		definition.Fields = append(definition.Fields, StructFieldDef{
			ID: stableSchemaID("field", definition.ID+":"+name), Tag: uint32(index + 1),
			Name: name, Position: index, Kind: "text",
		})
	}
	refreshStructHash(definition)
	return definition
}

func assertKitDBRowValuesEqual(t *testing.T, got, want map[string]value.Value) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("row field count = %d, want %d", len(got), len(want))
	}
	for name, expected := range want {
		actual, found := got[name]
		if !found {
			t.Fatalf("row is missing field %q", name)
		}
		if actual.K != expected.K || !reflect.DeepEqual(actual.Interface(), expected.Interface()) {
			t.Errorf("field %q = (%s) %#v, want (%s) %#v", name, actual.K, actual.Interface(), expected.K, expected.Interface())
		}
	}
}
