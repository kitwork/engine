package sql

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDecodeSchemaValidatesIndependentCatalogContract(t *testing.T) {
	definition := Schema{
		Version: CurrentSchemaVersion,
		ID:      "struct-products",
		Name:    "products",
		Hash:    "schema-hash",
		Fields: []Field{
			{ID: "field-id", Tag: 1, Name: "id", Position: 0, Kind: "kitid", Primary: true, NotNull: true, Unique: true},
			{ID: "field-status", Tag: 2, Name: "status", Position: 1, Kind: "enum", Enum: []string{"active", "disabled"}},
		},
		NextFieldTag: 3,
	}
	encoded, err := json.Marshal(definition)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeSchema(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Name != "products" || decoded.Fields[1].Kind != "enum" {
		t.Fatalf("decoded schema = %#v", decoded)
	}
}

func TestDecodeSchemaFailsClosedOnUnknownType(t *testing.T) {
	definition := []byte(`{
		"version":2,
		"id":"struct-products",
		"name":"products",
		"hash":"schema-hash",
		"nextFieldTag":2,
		"fields":[{"id":"field-id","tag":1,"name":"id","position":0,"kind":"mystery"}]
	}`)
	_, err := DecodeSchema(definition)
	if err == nil || !strings.Contains(err.Error(), `unsupported kind "mystery"`) {
		t.Fatalf("DecodeSchema error = %v", err)
	}
}

func TestDecodeSchemaRejectsFutureVersion(t *testing.T) {
	definition := []byte(`{
		"version":9,
		"id":"struct-products",
		"name":"products",
		"hash":"schema-hash",
		"nextFieldTag":2,
		"fields":[{"id":"field-id","tag":1,"name":"id","position":0,"kind":"kitid"}]
	}`)
	_, err := DecodeSchema(definition)
	if err == nil || !strings.Contains(err.Error(), "schema version 9 is unsupported") {
		t.Fatalf("DecodeSchema error = %v", err)
	}
}

func TestDecodeSchemaGatesExactUUIDOnVersionSeven(t *testing.T) {
	base := Schema{
		Version: SchemaVersion7, ID: "objects", Name: "objects", Hash: "hash", NextFieldTag: 2,
		Fields: []Field{{ID: "external", Tag: 1, Name: "external", Position: 0, Kind: "uuid", ExactUUID: true}},
	}
	for _, test := range []struct {
		name   string
		change func(*Schema)
		valid  bool
	}{
		{"exact", func(*Schema) {}, true},
		{"legacy-version", func(s *Schema) { s.Version = SchemaVersion6 }, false},
		{"wrong-kind", func(s *Schema) { s.Fields[0].Kind = "kitid" }, false},
		{"legacy-uuid", func(s *Schema) { s.Version = SchemaVersion6; s.Fields[0].ExactUUID = false }, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			schema := base
			schema.Fields = append([]Field(nil), base.Fields...)
			test.change(&schema)
			encoded, err := json.Marshal(schema)
			if err != nil {
				t.Fatal(err)
			}
			_, err = DecodeSchema(encoded)
			if (err == nil) != test.valid {
				t.Fatalf("valid=%v error=%v", test.valid, err)
			}
		})
	}
}

func TestDecodeSchemaGatesTextLengthOnVersionSix(t *testing.T) {
	length := 64
	base := Schema{
		Version: SchemaVersion6, ID: "labels", Name: "labels", Hash: "hash", NextFieldTag: 2,
		Fields: []Field{{ID: "slug", Tag: 1, Name: "slug", Position: 0, Kind: "varchar", TextLength: &length}},
	}
	for _, test := range []struct {
		name   string
		change func(*Schema)
		valid  bool
	}{
		{"varchar", func(*Schema) {}, true},
		{"char", func(s *Schema) { s.Fields[0].Kind = "char" }, true},
		{"legacy-version", func(s *Schema) { s.Version = SchemaVersion5 }, false},
		{"wrong-kind", func(s *Schema) { s.Fields[0].Kind = "text" }, false},
		{"zero", func(s *Schema) { value := 0; s.Fields[0].TextLength = &value }, false},
		{"too-large", func(s *Schema) { value := MaximumTextLength + 1; s.Fields[0].TextLength = &value }, false},
		{"legacy-unconstrained", func(s *Schema) { s.Version = SchemaVersion5; s.Fields[0].TextLength = nil }, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			schema := base
			schema.Fields = append([]Field(nil), base.Fields...)
			test.change(&schema)
			encoded, err := json.Marshal(schema)
			if err != nil {
				t.Fatal(err)
			}
			_, err = DecodeSchema(encoded)
			if (err == nil) != test.valid {
				t.Fatalf("valid=%v error=%v", test.valid, err)
			}
		})
	}
}

func TestDecodeSchemaGatesTemporalPrecisionOnVersionFive(t *testing.T) {
	precision := 3
	base := Schema{
		Version: SchemaVersion5, ID: "events", Name: "events", Hash: "hash", NextFieldTag: 2,
		Fields: []Field{{
			ID: "occurred", Tag: 1, Name: "occurred", Position: 0, Kind: "timestamptz",
			TimePrecision: &precision,
		}},
	}
	for _, test := range []struct {
		name   string
		change func(*Schema)
		valid  bool
	}{
		{"timestamptz", func(*Schema) {}, true},
		{"legacy-version", func(s *Schema) { s.Version = SchemaVersion4 }, false},
		{"wrong-kind", func(s *Schema) { s.Fields[0].Kind = "date" }, false},
		{"negative", func(s *Schema) { value := -1; s.Fields[0].TimePrecision = &value }, false},
		{"too-large", func(s *Schema) { value := 7; s.Fields[0].TimePrecision = &value }, false},
		{"zero", func(s *Schema) { value := 0; s.Fields[0].TimePrecision = &value }, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			schema := base
			schema.Fields = append([]Field(nil), base.Fields...)
			test.change(&schema)
			encoded, err := json.Marshal(schema)
			if err != nil {
				t.Fatal(err)
			}
			_, err = DecodeSchema(encoded)
			if (err == nil) != test.valid {
				t.Fatalf("valid=%v error=%v", test.valid, err)
			}
		})
	}
}

func TestDecodeSchemaGatesNewTemporalKindsOnVersionFive(t *testing.T) {
	for _, kind := range []string{"timestamp", "timestamptz", "interval"} {
		t.Run(kind, func(t *testing.T) {
			schema := Schema{
				Version: SchemaVersion4, ID: "events", Name: "events", Hash: "hash", NextFieldTag: 2,
				Fields: []Field{{ID: "value", Tag: 1, Name: "value", Position: 0, Kind: kind}},
			}
			encoded, err := json.Marshal(schema)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := DecodeSchema(encoded); err == nil || !strings.Contains(err.Error(), "requires schema version 5") {
				t.Fatalf("version-four %s error = %v", kind, err)
			}
		})
	}
}

func TestDecodeSchemaRejectsIntervalKeysAndIndexes(t *testing.T) {
	base := func() Schema {
		return Schema{
			Version: SchemaVersion5, ID: "events", Name: "events", Hash: "hash", NextFieldTag: 2,
			Fields: []Field{{ID: "elapsed", Tag: 1, Name: "elapsed", Position: 0, Kind: "interval"}},
		}
	}
	for _, test := range []struct {
		name   string
		change func(*Schema)
	}{
		{"primary", func(s *Schema) { s.Fields[0].Primary = true }},
		{"field-unique", func(s *Schema) { s.Fields[0].Unique = true }},
		{"secondary", func(s *Schema) { s.Fields[0].Indexes = []IndexMember{{Name: "events_elapsed_idx"}} }},
		{"table-unique", func(s *Schema) {
			s.UniqueConstraints = []UniqueConstraint{{Version: 1, ID: "unique-elapsed", Name: "events_elapsed_key", Fields: []uint32{1}}}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			schema := base()
			test.change(&schema)
			encoded, err := json.Marshal(schema)
			if err != nil {
				t.Fatal(err)
			}
			_, err = DecodeSchema(encoded)
			if err == nil || !strings.Contains(err.Error(), "cannot participate") {
				t.Fatalf("DecodeSchema error = %v", err)
			}
		})
	}
}

func TestDecodeSchemaGatesNumericModifiersOnVersionFour(t *testing.T) {
	base := Schema{
		Version: SchemaVersion4, ID: "ledger", Name: "ledger", Hash: "hash", NextFieldTag: 2,
		Fields: []Field{{
			ID: "amount", Tag: 1, Name: "amount", Position: 0, Kind: "decimal",
			Precision: 12, Scale: 4,
		}},
	}
	for _, test := range []struct {
		name   string
		change func(*Schema)
		valid  bool
	}{
		{"numeric", func(*Schema) {}, true},
		{"legacy-version", func(s *Schema) { s.Version = SchemaVersion3 }, false},
		{"wrong-kind", func(s *Schema) { s.Fields[0].Kind = "text" }, false},
		{"zero-precision", func(s *Schema) { s.Fields[0].Precision = 0 }, false},
		{"large-precision", func(s *Schema) { s.Fields[0].Precision = MaximumDecimalPrecision + 1 }, false},
		{"large-scale", func(s *Schema) { s.Fields[0].Scale = 13 }, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			schema := base
			schema.Fields = append([]Field(nil), base.Fields...)
			test.change(&schema)
			encoded, err := json.Marshal(schema)
			if err != nil {
				t.Fatal(err)
			}
			_, err = DecodeSchema(encoded)
			if (err == nil) != test.valid {
				t.Fatalf("valid=%v error=%v", test.valid, err)
			}
		})
	}
}

func TestDecodeSchemaSequenceVersionAndValidation(t *testing.T) {
	base := func() Schema {
		return Schema{
			Version: SchemaVersion3, ID: "orders", Name: "orders", Hash: "hash", NextFieldTag: 2,
			Fields: []Field{{ID: "id", Name: "id", Tag: 1, Kind: "bigint", NotNull: true,
				Sequence: &SequenceDefault{ID: "abcdef0123456789abcdef0123456789", Name: "orders_id_seq", Mode: "always"}}},
		}
	}
	for _, test := range []struct {
		name   string
		change func(*Schema)
		valid  bool
	}{
		{"identity", func(*Schema) {}, true},
		{"legacy-version", func(s *Schema) { s.Version = SchemaVersion2 }, false},
		{"wrong-kind", func(s *Schema) { s.Fields[0].Kind = "integer" }, false},
		{"literal-default", func(s *Schema) { s.Fields[0].HasDefault = true }, false},
		{"clock-default", func(s *Schema) { s.Fields[0].DefaultNow = true }, false},
		{"on-update", func(s *Schema) { s.Fields[0].Updated = true }, false},
		{"unknown-mode", func(s *Schema) { s.Fields[0].Sequence.Mode = "automatic" }, false},
		{"missing-id", func(s *Schema) { s.Fields[0].Sequence.ID = "" }, false},
		{"uppercase-id", func(s *Schema) { s.Fields[0].Sequence.ID = strings.ToUpper(s.Fields[0].Sequence.ID) }, false},
		{"missing-name", func(s *Schema) { s.Fields[0].Sequence.Name = "" }, false},
		{"nullable-identity", func(s *Schema) { s.Fields[0].NotNull = false }, false},
		{"nullable-default", func(s *Schema) { s.Fields[0].NotNull = false; s.Fields[0].Sequence.Mode = "default" }, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			schema := base()
			test.change(&schema)
			encoded, err := json.Marshal(schema)
			if err != nil {
				t.Fatal(err)
			}
			_, err = DecodeSchema(encoded)
			if (err == nil) != test.valid {
				t.Fatalf("valid=%v error=%v", test.valid, err)
			}
		})
	}
}
