package relational

import kitdbsql "github.com/kitwork/engine/kitdb/sql"

// exactUUIDFieldsCompatible preserves the legacy identifier-text contract
// while preventing exact UUID keys from being compared through that contract.
// Callers can still make the conversion explicit with CAST(... AS TEXT).
func exactUUIDFieldsCompatible(left, right kitdbsql.Field) bool {
	leftExact := left.Kind == "uuid" && left.ExactUUID
	rightExact := right.Kind == "uuid" && right.ExactUUID
	return leftExact == rightExact
}

func postgresCatalogTypeForField(field kitdbsql.Field) (kitdbsql.PostgreSQLType, bool) {
	typeInfo, found := kitdbsql.LookupKind(field.Kind)
	if !found {
		return kitdbsql.PostgreSQLType{}, false
	}
	if field.Kind == "uuid" && !field.ExactUUID {
		legacy, _ := kitdbsql.LookupKind("text")
		return legacy.Catalog, true
	}
	return typeInfo.Catalog, true
}

func postgresResultTypeForColumn(column Column) (kitdbsql.PostgreSQLType, bool) {
	typeInfo, found := kitdbsql.LookupKind(column.Kind)
	if !found {
		return kitdbsql.PostgreSQLType{}, false
	}
	if column.Kind == "uuid" && !column.ExactUUID {
		legacy, _ := kitdbsql.LookupKind("text")
		return legacy.Result, true
	}
	return typeInfo.Result, true
}
