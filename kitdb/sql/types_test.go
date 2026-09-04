package sql

import "testing"

func TestTypeRegistryCoversDurableCatalogKinds(t *testing.T) {
	expected := []string{
		"kitid", "uuid", "text", "varchar", "char", "integer", "serial",
		"float", "decimal", "bool", "enum", "datetime", "date", "time",
		"year", "month", "day", "json", "jsonb", "array", "blob", "ip",
		"mac", "vector", "oid", "bigint",
		"smallint", "int32", "timestamp", "timestamptz", "interval",
	}
	for _, kind := range expected {
		typeInfo, found := LookupKind(kind)
		if !found {
			t.Fatalf("LookupKind(%q) did not resolve", kind)
		}
		if typeInfo.Kind != kind || typeInfo.ID == TypeInvalid || typeInfo.Storage == StorageInvalid {
			t.Fatalf("LookupKind(%q) = %#v", kind, typeInfo)
		}
		if typeInfo.Catalog.OID == 0 || typeInfo.Result.OID == 0 {
			t.Fatalf("LookupKind(%q) has incomplete PostgreSQL metadata: %#v", kind, typeInfo)
		}
	}
	if got := len(AllTypes()); got != len(expected) {
		t.Fatalf("AllTypes() length = %d, want %d", got, len(expected))
	}
}

func TestResolveNameNormalizesPostgreSQLAndLegacyAliases(t *testing.T) {
	tests := map[string]string{
		"CHARACTER   VARYING":      "varchar",
		"double precision":         "float",
		"BIGINT":                   "bigint",
		"int8":                     "bigint",
		"SMALLINT":                 "smallint",
		"int2":                     "smallint",
		"INTEGER":                  "int32",
		"int":                      "int32",
		"int4":                     "int32",
		"numeric":                  "decimal",
		"boolean":                  "bool",
		"timestamp":                "timestamp",
		"timestamp with time zone": "timestamptz",
		"timestamptz":              "timestamptz",
		"interval":                 "interval",
		"bytea":                    "blob",
		"choice":                   "enum",
	}
	for name, expected := range tests {
		typeInfo, found := ResolveName(name)
		if !found || typeInfo.Kind != expected {
			t.Fatalf("ResolveName(%q) = %#v, %v; want %q", name, typeInfo, found, expected)
		}
	}
	if _, found := ResolveName("serial"); found {
		t.Fatal("serial must remain unavailable in SQL until sequence semantics exist")
	}
	if _, found := ResolveName("definitely_not_a_type"); found {
		t.Fatal("unknown SQL type unexpectedly resolved")
	}
}

func TestCharacterTypesExposePostgreSQLOIDs(t *testing.T) {
	varchar, _ := LookupKind("varchar")
	char, _ := LookupKind("char")
	if varchar.Catalog.OID != PostgreSQLOIDVarchar || varchar.Result.OID != PostgreSQLOIDVarchar ||
		varchar.Catalog.UDTName != "varchar" {
		t.Fatalf("VARCHAR metadata = %#v", varchar)
	}
	if char.Catalog.OID != PostgreSQLOIDBPChar || char.Result.OID != PostgreSQLOIDBPChar ||
		char.Catalog.UDTName != "bpchar" {
		t.Fatalf("CHAR metadata = %#v", char)
	}
}

func TestUUIDExposesPostgreSQLNativeMetadata(t *testing.T) {
	uuid, _ := LookupKind("uuid")
	if uuid.Catalog.OID != PostgreSQLOIDUUID || uuid.Result.OID != PostgreSQLOIDUUID ||
		uuid.Catalog.UDTName != "uuid" || uuid.Catalog.Size != 16 {
		t.Fatalf("UUID metadata = %#v", uuid)
	}
}

func TestAllTypesReturnsCallerOwnedSlice(t *testing.T) {
	types := AllTypes()
	types[0].Kind = "changed"
	typeInfo, found := LookupKind("kitid")
	if !found || typeInfo.Kind != "kitid" {
		t.Fatalf("caller mutation escaped into registry: %#v, %v", typeInfo, found)
	}
}
