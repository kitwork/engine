package sql

import "strings"

// TypeID is the stable in-process identity of a KitDB logical type. Durable
// catalogs continue to store Kind; TypeID keeps runtime switches compact while
// the file format remains readable and evolvable.
type TypeID uint16

const (
	TypeInvalid TypeID = iota
	TypeKitID
	TypeUUID
	TypeText
	TypeVarchar
	TypeChar
	TypeInteger
	TypeSerial
	TypeFloat
	TypeDecimal
	TypeBool
	TypeChoice
	TypeDatetime
	TypeDate
	TypeTime
	TypeYear
	TypeMonth
	TypeDay
	TypeJSON
	TypeJSONB
	TypeArray
	TypeBlob
	TypeIP
	TypeMAC
	TypeVector
	TypeOID
	TypeBigInt
	// Append new IDs: existing values are stable in process and may be used by
	// prepared plans. Durable catalogs store Kind rather than TypeID.
	TypeSmallInt
	TypeInt32
	TypeTimestamp
	TypeTimestampTZ
	TypeInterval
)

// Family groups logical types that share comparison and coercion semantics.
// It is intentionally coarser than a SQL dialect's type catalog.
type Family uint8

const (
	FamilyInvalid Family = iota
	FamilyIdentifier
	FamilyText
	FamilyInteger
	FamilyFloat
	FamilyDecimal
	FamilyBoolean
	FamilyChoice
	FamilyTemporal
	FamilyJSON
	FamilyArray
	FamilyBinary
	FamilyNetwork
	FamilyVector
	FamilySystem
)

// StorageClass describes the current scalar representation expected by the
// record adapters. It is not a promise that KitDB will copy SQLite's page or
// affinity model.
type StorageClass uint8

const (
	StorageInvalid StorageClass = iota
	StorageText
	StorageInteger
	StorageReal
	StorageBlob
)

func (storage StorageClass) String() string {
	switch storage {
	case StorageText:
		return "TEXT"
	case StorageInteger:
		return "INTEGER"
	case StorageReal:
		return "REAL"
	case StorageBlob:
		return "BLOB"
	default:
		return ""
	}
}

// PostgreSQLType is the compatibility metadata exposed to PostgreSQL clients.
// Catalog and Result may differ while KitDB still transports a logical value
// through text format, for example date/time values in the current profile.
type PostgreSQLType struct {
	DataType string
	UDTName  string
	OID      uint32
	Size     int16
}

// PostgreSQL type OIDs are centralized here so the catalog and wire encoder
// cannot silently disagree about the type contract they advertise.
const (
	PostgreSQLOIDBool        uint32 = 16
	PostgreSQLOIDBytea       uint32 = 17
	PostgreSQLOIDInt8        uint32 = 20
	PostgreSQLOIDInt2        uint32 = 21
	PostgreSQLOIDInt4        uint32 = 23
	PostgreSQLOIDText        uint32 = 25
	PostgreSQLOIDOID         uint32 = 26
	PostgreSQLOIDJSON        uint32 = 114
	PostgreSQLOIDFloat4      uint32 = 700
	PostgreSQLOIDFloat8      uint32 = 701
	PostgreSQLOIDDate        uint32 = 1082
	PostgreSQLOIDTime        uint32 = 1083
	PostgreSQLOIDTimestamp   uint32 = 1114
	PostgreSQLOIDTimestampTZ uint32 = 1184
	PostgreSQLOIDInterval    uint32 = 1186
	PostgreSQLOIDBPChar      uint32 = 1042
	PostgreSQLOIDVarchar     uint32 = 1043
	PostgreSQLOIDNumeric     uint32 = 1700
	PostgreSQLOIDUUID        uint32 = 2950
	PostgreSQLOIDJSONB       uint32 = 3802
)

// Type is one immutable registry entry. Kind is the canonical spelling stored
// in Schema IR; SQL aliases are accepted only by ResolveName.
type Type struct {
	ID      TypeID
	Kind    string
	Family  Family
	Storage StorageClass
	Catalog PostgreSQLType
	Result  PostgreSQLType
}

type typeRegistration struct {
	typeInfo Type
	sqlNames []string
}

var (
	postgresText = PostgreSQLType{
		DataType: "text", UDTName: "text", OID: PostgreSQLOIDText, Size: -1,
	}
	postgresVarchar = PostgreSQLType{
		DataType: "character varying", UDTName: "varchar", OID: PostgreSQLOIDVarchar, Size: -1,
	}
	postgresBPChar = PostgreSQLType{
		DataType: "character", UDTName: "bpchar", OID: PostgreSQLOIDBPChar, Size: -1,
	}
	postgresInt8 = PostgreSQLType{
		DataType: "bigint", UDTName: "int8", OID: PostgreSQLOIDInt8, Size: 8,
	}
	postgresInt2 = PostgreSQLType{
		DataType: "smallint", UDTName: "int2", OID: PostgreSQLOIDInt2, Size: 2,
	}
	postgresInt4 = PostgreSQLType{
		DataType: "integer", UDTName: "int4", OID: PostgreSQLOIDInt4, Size: 4,
	}
	postgresBool = PostgreSQLType{
		DataType: "boolean", UDTName: "bool", OID: PostgreSQLOIDBool, Size: 1,
	}
	postgresFloat8 = PostgreSQLType{
		DataType: "double precision", UDTName: "float8", OID: PostgreSQLOIDFloat8, Size: 8,
	}
	postgresNumeric = PostgreSQLType{
		DataType: "numeric", UDTName: "numeric", OID: PostgreSQLOIDNumeric, Size: -1,
	}
	postgresBytea = PostgreSQLType{
		DataType: "bytea", UDTName: "bytea", OID: PostgreSQLOIDBytea, Size: -1,
	}
	postgresJSON = PostgreSQLType{
		DataType: "json", UDTName: "json", OID: PostgreSQLOIDJSON, Size: -1,
	}
	postgresJSONB = PostgreSQLType{
		DataType: "jsonb", UDTName: "jsonb", OID: PostgreSQLOIDJSONB, Size: -1,
	}
	postgresTimestampTZ = PostgreSQLType{
		DataType: "timestamp with time zone", UDTName: "timestamptz", OID: PostgreSQLOIDTimestampTZ, Size: 8,
	}
	postgresTimestamp = PostgreSQLType{
		DataType: "timestamp without time zone", UDTName: "timestamp", OID: PostgreSQLOIDTimestamp, Size: 8,
	}
	postgresDate = PostgreSQLType{
		DataType: "date", UDTName: "date", OID: PostgreSQLOIDDate, Size: 4,
	}
	postgresTime = PostgreSQLType{
		DataType: "time without time zone", UDTName: "time", OID: PostgreSQLOIDTime, Size: 8,
	}
	postgresInterval = PostgreSQLType{
		DataType: "interval", UDTName: "interval", OID: PostgreSQLOIDInterval, Size: 16,
	}
	postgresUUID = PostgreSQLType{
		DataType: "uuid", UDTName: "uuid", OID: PostgreSQLOIDUUID, Size: 16,
	}
	postgresOID = PostgreSQLType{
		DataType: "oid", UDTName: "oid", OID: PostgreSQLOIDOID, Size: 4,
	}
)

var registrations = []typeRegistration{
	{Type{TypeKitID, "kitid", FamilyIdentifier, StorageText, postgresText, postgresText}, []string{"kitid"}},
	{Type{TypeUUID, "uuid", FamilyIdentifier, StorageText, postgresUUID, postgresUUID}, []string{"uuid"}},
	{Type{TypeText, "text", FamilyText, StorageText, postgresText, postgresText}, []string{"text", "clob"}},
	{Type{TypeVarchar, "varchar", FamilyText, StorageText, postgresVarchar, postgresVarchar}, []string{"varchar", "character varying"}},
	{Type{TypeChar, "char", FamilyText, StorageText, postgresBPChar, postgresBPChar}, []string{"char", "character"}},
	// "integer" is the original KitDB/Kitwork kind. Keep its exact-range and
	// int8 metadata for old catalogs, but do not author new PostgreSQL INTEGER
	// columns into it. TINYINT remains the explicit legacy spelling.
	{Type{TypeInteger, "integer", FamilyInteger, StorageInteger, postgresInt8, postgresInt8}, []string{"tinyint"}},
	{Type{TypeSerial, "serial", FamilyInteger, StorageInteger, postgresInt8, postgresInt8}, nil},
	{Type{TypeFloat, "float", FamilyFloat, StorageReal, postgresFloat8, postgresFloat8}, []string{"real", "float", "double", "double precision"}},
	{Type{TypeDecimal, "decimal", FamilyDecimal, StorageText, postgresNumeric, postgresNumeric}, []string{"decimal", "numeric"}},
	{Type{TypeBool, "bool", FamilyBoolean, StorageInteger, postgresBool, postgresBool}, []string{"boolean", "bool"}},
	{Type{TypeChoice, "enum", FamilyChoice, StorageText, postgresText, postgresText}, []string{"choice", "enum"}},
	// datetime is the relaxed legacy Kitwork kind. New SQL should use one of the
	// exact temporal kinds below so timezone and precision are never ambiguous.
	{Type{TypeDatetime, "datetime", FamilyTemporal, StorageText, postgresTimestampTZ, postgresText}, []string{"datetime"}},
	{Type{TypeDate, "date", FamilyTemporal, StorageText, postgresDate, postgresDate}, []string{"date"}},
	{Type{TypeTime, "time", FamilyTemporal, StorageText, postgresTime, postgresTime}, []string{"time", "time without time zone"}},
	{Type{TypeYear, "year", FamilyInteger, StorageInteger, postgresInt8, postgresInt8}, nil},
	{Type{TypeMonth, "month", FamilyInteger, StorageInteger, postgresInt8, postgresInt8}, nil},
	{Type{TypeDay, "day", FamilyInteger, StorageInteger, postgresInt8, postgresInt8}, nil},
	{Type{TypeJSON, "json", FamilyJSON, StorageText, postgresJSON, postgresJSON}, []string{"json"}},
	{Type{TypeJSONB, "jsonb", FamilyJSON, StorageText, postgresJSONB, postgresJSONB}, []string{"jsonb"}},
	{Type{TypeArray, "array", FamilyArray, StorageText, postgresText, postgresText}, []string{"array"}},
	{Type{TypeBlob, "blob", FamilyBinary, StorageBlob, postgresBytea, postgresBytea}, []string{"blob", "binary", "varbinary", "bytea"}},
	{Type{TypeIP, "ip", FamilyNetwork, StorageText, postgresText, postgresText}, nil},
	{Type{TypeMAC, "mac", FamilyNetwork, StorageText, postgresText, postgresText}, nil},
	{Type{TypeVector, "vector", FamilyVector, StorageText, postgresText, postgresText}, []string{"vector"}},
	{Type{TypeOID, "oid", FamilySystem, StorageInteger, postgresOID, postgresOID}, nil},
	// A distinct durable kind keeps legacy integer keys and admission unchanged.
	{Type{TypeBigInt, "bigint", FamilyInteger, StorageInteger, postgresInt8, postgresInt8}, []string{"bigint", "int8"}},
	{Type{TypeSmallInt, "smallint", FamilyInteger, StorageInteger, postgresInt2, postgresInt2}, []string{"smallint", "int2"}},
	// The durable name differs from SQL INTEGER so old Kind="integer" catalogs
	// retain their original 53-bit contract without a schema rewrite.
	{Type{TypeInt32, "int32", FamilyInteger, StorageInteger, postgresInt4, postgresInt4}, []string{"integer", "int", "int4"}},
	{Type{TypeTimestamp, "timestamp", FamilyTemporal, StorageText, postgresTimestamp, postgresTimestamp}, []string{"timestamp", "timestamp without time zone"}},
	{Type{TypeTimestampTZ, "timestamptz", FamilyTemporal, StorageText, postgresTimestampTZ, postgresTimestampTZ}, []string{"timestamptz", "timestamp with time zone"}},
	{Type{TypeInterval, "interval", FamilyTemporal, StorageText, postgresInterval, postgresInterval}, []string{"interval"}},
}

var typesByKind, typesBySQLName = buildTypeRegistry()

func buildTypeRegistry() (map[string]Type, map[string]Type) {
	byKind := make(map[string]Type, len(registrations))
	bySQLName := make(map[string]Type, len(registrations)*2)
	for _, registration := range registrations {
		item := registration.typeInfo
		if item.ID == TypeInvalid || item.Kind == "" || item.Storage == StorageInvalid {
			panic("kitdb/sql: invalid type registration")
		}
		if _, exists := byKind[item.Kind]; exists {
			panic("kitdb/sql: duplicate type kind " + item.Kind)
		}
		byKind[item.Kind] = item
		for _, name := range registration.sqlNames {
			name = normalizeTypeName(name)
			if name == "" {
				panic("kitdb/sql: empty SQL type name")
			}
			if _, exists := bySQLName[name]; exists {
				panic("kitdb/sql: duplicate SQL type name " + name)
			}
			bySQLName[name] = item
		}
	}
	return byKind, bySQLName
}

// LookupKind resolves the canonical kind stored in Schema IR.
func LookupKind(kind string) (Type, bool) {
	item, found := typesByKind[normalizeTypeName(kind)]
	return item, found
}

// ResolveName resolves one accepted SQL spelling into its canonical catalog
// type. Not every catalog type is SQL-authorable yet; serial is deliberately
// withheld until its sequence semantics are implemented.
func ResolveName(name string) (Type, bool) {
	item, found := typesBySQLName[normalizeTypeName(name)]
	return item, found
}

// SequenceDataTypeForKind maps one durable integer kind to PostgreSQL's
// sequence type spelling. Legacy integer/serial are intentionally excluded:
// their historical range is neither int4 nor int8.
func SequenceDataTypeForKind(kind string) (string, bool) {
	item, found := LookupKind(kind)
	if !found {
		return "", false
	}
	switch item.ID {
	case TypeSmallInt:
		return "smallint", true
	case TypeInt32:
		return "integer", true
	case TypeBigInt:
		return "bigint", true
	default:
		return "", false
	}
}

// SequenceKindForDataType returns the durable field kind associated with an
// AS SMALLINT/INTEGER/BIGINT sequence declaration.
func SequenceKindForDataType(dataType string) (string, bool) {
	item, found := ResolveName(dataType)
	if !found {
		return "", false
	}
	_, valid := SequenceDataTypeForKind(item.Kind)
	return item.Kind, valid
}

// AllTypes returns a caller-owned snapshot in stable TypeID order.
func AllTypes() []Type {
	result := make([]Type, len(registrations))
	for index, registration := range registrations {
		result[index] = registration.typeInfo
	}
	return result
}

func normalizeTypeName(name string) string {
	return strings.Join(strings.Fields(strings.ToLower(name)), " ")
}
