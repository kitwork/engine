package relational

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	kitdbengine "github.com/kitwork/engine/kitdb"
	"github.com/kitwork/engine/kitdb/pgwire"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

const postgresPublicNamespaceOID uint32 = 2200

type postgresCatalogRelation struct {
	schema kitdbsql.Schema
	table  Table
}

type postgresCatalogSnapshot struct {
	database  string
	databases []string
	relations []postgresCatalogRelation
	functions []*storedFunction
	sequences []kitdbengine.Sequence
}

type postgresCatalogColumn struct {
	name string
	oid  uint32
	size int16
}

type postgresCatalogDataset struct {
	columns  map[string]postgresCatalogColumn
	defaults []string
	rows     []map[string]any
}

type postgresCatalogProjection struct {
	name     string
	source   string
	constant *string
	cast     string
	column   pgwire.Column
}

type postgresCatalogIndex struct {
	name       string
	fields     []kitdbsql.Field
	unique     bool
	primary    bool
	filter     []kitdbsql.IndexCondition
	tableOID   uint32
	indexOID   uint32
	definition string
}

type postgresCatalogConstraint struct {
	name       string
	kind       string
	fields     []kitdbsql.Field
	targetOID  uint32
	tableOID   uint32
	constraint uint32
}

func (engine *Engine) postgresCatalogSnapshot(database string) (postgresCatalogSnapshot, error) {
	engine.mu.RLock()
	defer engine.mu.RUnlock()
	if err := engine.readyLocked(); err != nil {
		return postgresCatalogSnapshot{}, err
	}
	catalog, err := engine.database.Catalog()
	if err != nil {
		return postgresCatalogSnapshot{}, err
	}
	result := postgresCatalogSnapshot{
		database: database, databases: []string{database},
		sequences: catalog.Sequences,
		relations: make([]postgresCatalogRelation, 0, len(catalog.Structs)),
	}
	for _, entry := range catalog.Functions {
		definition, err := decodeStoredFunction(entry)
		if err != nil {
			return postgresCatalogSnapshot{}, err
		}
		result.functions = append(result.functions, definition)
	}
	for _, entry := range catalog.Structs {
		schema, err := decodeCatalogSchema(entry.Definition)
		if err != nil {
			return postgresCatalogSnapshot{}, err
		}
		count, analyzed, err := readTableCount(engine.database, schema)
		if err != nil {
			return postgresCatalogSnapshot{}, err
		}
		result.relations = append(result.relations, postgresCatalogRelation{
			schema: schema,
			table: Table{
				Name: schema.Name, ID: schema.ID, Hash: schema.Hash, Fields: len(schema.Fields),
				Rows: count, Analyzed: analyzed,
			},
		})
	}
	sort.Slice(result.relations, func(left, right int) bool {
		return strings.ToLower(result.relations[left].schema.Name) < strings.ToLower(result.relations[right].schema.Name)
	})
	return result, nil
}

func (transaction *Transaction) postgresCatalogSnapshot(database string) (postgresCatalogSnapshot, error) {
	if err := transaction.ready(); err != nil {
		return postgresCatalogSnapshot{}, err
	}
	result := postgresCatalogSnapshot{
		database: database, databases: []string{database},
		sequences: transaction.catalog.Sequences,
		relations: make([]postgresCatalogRelation, 0, len(transaction.catalog.Structs)),
	}
	for _, entry := range transaction.catalog.Functions {
		definition, err := decodeStoredFunction(entry)
		if err != nil {
			return postgresCatalogSnapshot{}, err
		}
		result.functions = append(result.functions, definition)
	}
	for _, entry := range transaction.catalog.Structs {
		schema, err := decodeCatalogSchema(entry.Definition)
		if err != nil {
			return postgresCatalogSnapshot{}, err
		}
		count, analyzed, err := readTableCount(transaction, schema)
		if err != nil {
			return postgresCatalogSnapshot{}, err
		}
		result.relations = append(result.relations, postgresCatalogRelation{
			schema: schema,
			table: Table{
				Name: schema.Name, ID: schema.ID, Hash: schema.Hash, Fields: len(schema.Fields),
				Rows: count, Analyzed: analyzed,
			},
		})
	}
	sort.Slice(result.relations, func(left, right int) bool {
		return strings.ToLower(result.relations[left].schema.Name) < strings.ToLower(result.relations[right].schema.Name)
	})
	return result, nil
}

func executePostgresCatalogQuery(source string, catalog postgresCatalogSnapshot) (pgwire.Result, bool, error) {
	dataset, handled, err := postgresCatalogDatasetFor(source, catalog)
	if err != nil || !handled {
		return pgwire.Result{}, handled, err
	}
	filterPostgresCatalogRows(source, &dataset)
	projections, count, err := projectPostgresCatalog(source, dataset)
	if err != nil {
		return pgwire.Result{}, true, pgwire.NewError("42601", err.Error())
	}
	if count {
		return pgwire.Result{
			Columns:    []pgwire.Column{{Name: projections[0].name, DataTypeOID: pgwire.OIDInt8, DataTypeSize: 8}},
			Rows:       [][]pgwire.Field{{{Data: []byte(strconv.Itoa(len(dataset.rows)))}}},
			CommandTag: "SELECT 1",
		}, true, nil
	}
	rows := make([][]pgwire.Field, len(dataset.rows))
	for rowIndex, row := range dataset.rows {
		rows[rowIndex] = make([]pgwire.Field, len(projections))
		for columnIndex, projection := range projections {
			if projection.constant != nil {
				field, err := postgresCatalogProjectionField(*projection.constant, projection.cast)
				if err != nil {
					return pgwire.Result{}, true, pgwire.NewError("22P02", err.Error())
				}
				rows[rowIndex][columnIndex] = field
				continue
			}
			value, found := row[projection.source]
			if !found || value == nil {
				rows[rowIndex][columnIndex] = pgwire.Field{Null: true}
				continue
			}
			field, err := postgresCatalogProjectionField(value, projection.cast)
			if err != nil {
				return pgwire.Result{}, true, pgwire.NewError("22P02", err.Error())
			}
			rows[rowIndex][columnIndex] = field
		}
	}
	if limit := postgresCatalogLimit(source); limit >= 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	columns := make([]pgwire.Column, len(projections))
	for index, projection := range projections {
		columns[index] = projection.column
	}
	return pgwire.Result{Columns: columns, Rows: rows, CommandTag: fmt.Sprintf("SELECT %d", len(rows))}, true, nil
}

func isPostgresCatalogQuery(source string) bool {
	normalized := normalizePostgresCatalogSQL(source)
	for _, relation := range []string{
		"information_schema.tables", "information_schema.columns",
		"information_schema.table_constraints", "information_schema.key_column_usage",
		"information_schema.routines", "information_schema.schemata",
		"pg_catalog.pg_tables", "pg_tables", "pg_catalog.pg_indexes", "pg_indexes",
		"pg_catalog.pg_class", "pg_class", "pg_catalog.pg_namespace", "pg_namespace",
		"pg_catalog.pg_attribute", "pg_attribute", "pg_catalog.pg_index", "pg_index",
		"pg_catalog.pg_constraint", "pg_constraint", "pg_catalog.pg_database", "pg_database",
		"pg_catalog.pg_proc", "pg_proc", "pg_catalog.pg_enum", "pg_enum",
		"information_schema.sequences", "pg_catalog.pg_sequence", "pg_sequence",
		"pg_catalog.pg_type", "pg_type",
	} {
		if containsPostgresRelation(normalized, relation) {
			return true
		}
	}
	return false
}

func postgresCatalogDatasetFor(source string, catalog postgresCatalogSnapshot) (postgresCatalogDataset, bool, error) {
	normalized := normalizePostgresCatalogSQL(source)
	switch {
	case containsPostgresRelation(normalized, "information_schema.sequences"):
		return informationSchemaSequences(catalog), true, nil
	case containsPostgresRelation(normalized, "pg_catalog.pg_sequence") || containsPostgresRelation(normalized, "pg_sequence"):
		return postgresSequences(catalog), true, nil
	case containsPostgresRelation(normalized, "information_schema.tables"):
		return informationSchemaTables(catalog), true, nil
	case containsPostgresRelation(normalized, "information_schema.columns"):
		return informationSchemaColumns(catalog), true, nil
	case containsPostgresRelation(normalized, "information_schema.table_constraints") &&
		containsPostgresRelation(normalized, "information_schema.key_column_usage"):
		return informationSchemaConstraints(catalog, true), true, nil
	case containsPostgresRelation(normalized, "information_schema.table_constraints"):
		return informationSchemaConstraints(catalog, false), true, nil
	case containsPostgresRelation(normalized, "information_schema.key_column_usage"):
		return informationSchemaConstraints(catalog, true), true, nil
	case containsPostgresRelation(normalized, "information_schema.routines"):
		return informationSchemaFunctions(catalog), true, nil
	case containsPostgresRelation(normalized, "information_schema.schemata"):
		return postgresCatalogSchemata(catalog), true, nil
	case containsPostgresRelation(normalized, "pg_catalog.pg_tables") || containsPostgresRelation(normalized, "pg_tables"):
		return postgresTables(catalog), true, nil
	case containsPostgresRelation(normalized, "pg_catalog.pg_indexes") || containsPostgresRelation(normalized, "pg_indexes"):
		return postgresIndexes(catalog), true, nil
	case containsPostgresRelation(normalized, "pg_catalog.pg_class") || containsPostgresRelation(normalized, "pg_class"):
		return postgresClasses(catalog), true, nil
	case containsPostgresRelation(normalized, "pg_catalog.pg_namespace") || containsPostgresRelation(normalized, "pg_namespace"):
		return postgresNamespaces(), true, nil
	case containsPostgresRelation(normalized, "pg_catalog.pg_attribute") || containsPostgresRelation(normalized, "pg_attribute"):
		return postgresAttributes(catalog), true, nil
	case containsPostgresRelation(normalized, "pg_catalog.pg_index") || containsPostgresRelation(normalized, "pg_index"):
		return postgresIndexCatalog(catalog), true, nil
	case containsPostgresRelation(normalized, "pg_catalog.pg_constraint") || containsPostgresRelation(normalized, "pg_constraint"):
		return postgresConstraints(catalog), true, nil
	case containsPostgresRelation(normalized, "pg_catalog.pg_database") || containsPostgresRelation(normalized, "pg_database"):
		return postgresDatabases(catalog), true, nil
	case containsPostgresRelation(normalized, "pg_catalog.pg_proc") || containsPostgresRelation(normalized, "pg_proc"):
		return postgresFunctions(catalog), true, nil
	case containsPostgresRelation(normalized, "pg_catalog.pg_enum") || containsPostgresRelation(normalized, "pg_enum"):
		return emptyPostgresCatalogDataset([]postgresCatalogColumn{
			oidCatalogColumn("oid"), textCatalogColumn("typname"), oidCatalogColumn("enumtypid"),
			textCatalogColumn("enumlabel"), float4CatalogColumn("enumsortorder"),
		}), true, nil
	case containsPostgresRelation(normalized, "pg_catalog.pg_type") || containsPostgresRelation(normalized, "pg_type"):
		return postgresTypes(), true, nil
	default:
		return postgresCatalogDataset{}, false, nil
	}
}

func informationSchemaTables(catalog postgresCatalogSnapshot) postgresCatalogDataset {
	dataset := newPostgresCatalogDataset(
		[]string{"table_catalog", "table_schema", "table_name", "table_type"},
		textCatalogColumn("table_catalog"), textCatalogColumn("table_schema"),
		textCatalogColumn("table_name"), textCatalogColumn("table_type"),
		textCatalogColumn("is_insertable_into"),
	)
	for _, relation := range catalog.relations {
		dataset.rows = append(dataset.rows, map[string]any{
			"table_catalog": catalog.database, "table_schema": "public",
			"table_name": relation.schema.Name, "table_type": "BASE TABLE", "is_insertable_into": "YES",
		})
	}
	return dataset
}

func informationSchemaColumns(catalog postgresCatalogSnapshot) postgresCatalogDataset {
	dataset := newPostgresCatalogDataset(
		[]string{"table_catalog", "table_schema", "table_name", "column_name", "ordinal_position", "column_default", "is_nullable", "data_type"},
		textCatalogColumn("table_catalog"), textCatalogColumn("table_schema"),
		textCatalogColumn("table_name"), textCatalogColumn("column_name"),
		int8CatalogColumn("ordinal_position"), textCatalogColumn("column_default"),
		textCatalogColumn("is_nullable"), textCatalogColumn("data_type"),
		textCatalogColumn("udt_schema"), textCatalogColumn("udt_name"),
		int8CatalogColumn("character_maximum_length"), int8CatalogColumn("numeric_precision"),
		int8CatalogColumn("numeric_scale"), int8CatalogColumn("datetime_precision"),
		textCatalogColumn("is_identity"), textCatalogColumn("identity_generation"),
		textCatalogColumn("identity_start"), textCatalogColumn("identity_increment"),
		textCatalogColumn("identity_minimum"), textCatalogColumn("identity_maximum"), textCatalogColumn("identity_cycle"),
	)
	for _, relation := range catalog.relations {
		fields := append([]kitdbsql.Field(nil), relation.schema.Fields...)
		sort.Slice(fields, func(left, right int) bool { return fields[left].Position < fields[right].Position })
		for _, field := range fields {
			typeInfo, found := kitdbsql.LookupKind(field.Kind)
			if !found {
				continue
			}
			postgres, _ := postgresCatalogTypeForField(field)
			row := map[string]any{
				"table_catalog": catalog.database, "table_schema": "public", "table_name": relation.schema.Name,
				"column_name": field.Name, "ordinal_position": int64(field.Position + 1),
				"column_default": postgresColumnDefault(field), "is_nullable": postgresYesNo(!field.NotNull),
				"data_type": postgres.DataType, "udt_schema": "pg_catalog", "udt_name": postgres.UDTName,
				"is_identity": "NO", "identity_generation": nil,
			}
			switch typeInfo.ID {
			case kitdbsql.TypeSmallInt:
				row["numeric_precision"] = int64(16)
				row["numeric_scale"] = int64(0)
			case kitdbsql.TypeInt32:
				row["numeric_precision"] = int64(32)
				row["numeric_scale"] = int64(0)
			case kitdbsql.TypeInteger, kitdbsql.TypeSerial, kitdbsql.TypeBigInt,
				kitdbsql.TypeYear, kitdbsql.TypeMonth, kitdbsql.TypeDay, kitdbsql.TypeOID:
				row["numeric_precision"] = int64(64)
				row["numeric_scale"] = int64(0)
			case kitdbsql.TypeDecimal:
				if field.Precision != 0 {
					row["numeric_precision"] = int64(field.Precision)
					row["numeric_scale"] = int64(field.Scale)
				}
			case kitdbsql.TypeTime, kitdbsql.TypeTimestamp, kitdbsql.TypeTimestampTZ:
				precision := kitdbsql.MaximumTemporalPrecision
				if field.TimePrecision != nil {
					precision = *field.TimePrecision
				}
				row["datetime_precision"] = int64(precision)
			case kitdbsql.TypeVarchar, kitdbsql.TypeChar:
				if field.TextLength != nil {
					row["character_maximum_length"] = int64(*field.TextLength)
				}
			}
			if mode := fieldIdentityMode(field); mode != "" {
				row["is_identity"], row["identity_generation"] = "YES", "ALWAYS"
				if mode == "d" {
					row["identity_generation"] = "BY DEFAULT"
				}
				for _, sequence := range catalog.sequences {
					if sequence.ID != field.Sequence.ID {
						continue
					}
					row["identity_start"], row["identity_increment"] = strconv.FormatInt(sequence.Start, 10), strconv.FormatInt(sequence.Increment, 10)
					row["identity_minimum"], row["identity_maximum"] = strconv.FormatInt(sequence.Minimum, 10), strconv.FormatInt(sequence.Maximum, 10)
					row["identity_cycle"] = postgresYesNo(sequence.Cycle)
				}
			}
			dataset.rows = append(dataset.rows, row)
		}
	}
	return dataset
}

func informationSchemaConstraints(catalog postgresCatalogSnapshot, keyUsage bool) postgresCatalogDataset {
	dataset := newPostgresCatalogDataset(
		[]string{"constraint_catalog", "constraint_schema", "constraint_name", "table_catalog", "table_schema", "table_name", "constraint_type"},
		textCatalogColumn("constraint_catalog"), textCatalogColumn("constraint_schema"),
		textCatalogColumn("constraint_name"), textCatalogColumn("table_catalog"),
		textCatalogColumn("table_schema"), textCatalogColumn("table_name"),
		textCatalogColumn("constraint_type"), textCatalogColumn("column_name"),
		int8CatalogColumn("ordinal_position"), int8CatalogColumn("position_in_unique_constraint"),
	)
	if keyUsage {
		dataset.defaults = []string{
			"constraint_catalog", "constraint_schema", "constraint_name", "table_catalog",
			"table_schema", "table_name", "column_name", "ordinal_position", "position_in_unique_constraint",
		}
	}
	for _, relation := range catalog.relations {
		for _, constraint := range catalogConstraintsFor(catalog, relation) {
			constraintType := postgresConstraintType(constraint.kind)
			if keyUsage && len(constraint.fields) == 0 {
				continue
			}
			if !keyUsage {
				dataset.rows = append(dataset.rows, map[string]any{
					"constraint_catalog": catalog.database, "constraint_schema": "public", "constraint_name": constraint.name,
					"table_catalog": catalog.database, "table_schema": "public", "table_name": relation.schema.Name,
					"constraint_type": constraintType,
				})
				continue
			}
			for position, field := range constraint.fields {
				dataset.rows = append(dataset.rows, map[string]any{
					"constraint_catalog": catalog.database, "constraint_schema": "public", "constraint_name": constraint.name,
					"table_catalog": catalog.database, "table_schema": "public", "table_name": relation.schema.Name,
					"column_name": field.Name, "ordinal_position": int64(position + 1),
					"position_in_unique_constraint": nil, "constraint_type": constraintType,
				})
			}
		}
	}
	return dataset
}

func postgresTables(catalog postgresCatalogSnapshot) postgresCatalogDataset {
	dataset := newPostgresCatalogDataset(
		[]string{"schemaname", "tablename", "tableowner", "tablespace"},
		textCatalogColumn("schemaname"), textCatalogColumn("tablename"),
		textCatalogColumn("tableowner"), textCatalogColumn("tablespace"),
		oidCatalogColumn("tableoid"),
	)
	for _, relation := range catalog.relations {
		dataset.rows = append(dataset.rows, map[string]any{
			"schemaname": "public", "tablename": relation.schema.Name, "tableowner": "kitdb",
			"tablespace": nil, "tableoid": postgresCatalogOID("table", relation.schema.ID),
		})
	}
	return dataset
}

func postgresIndexes(catalog postgresCatalogSnapshot) postgresCatalogDataset {
	dataset := newPostgresCatalogDataset(
		[]string{"schemaname", "tablename", "indexname", "tablespace", "indexdef"},
		textCatalogColumn("schemaname"), textCatalogColumn("tablename"),
		textCatalogColumn("indexname"), textCatalogColumn("tablespace"), textCatalogColumn("indexdef"),
	)
	for _, relation := range catalog.relations {
		for _, index := range catalogIndexesFor(relation.schema) {
			dataset.rows = append(dataset.rows, map[string]any{
				"schemaname": "public", "tablename": relation.schema.Name, "indexname": index.name,
				"tablespace": nil, "indexdef": index.definition,
			})
		}
	}
	return dataset
}

func postgresClasses(catalog postgresCatalogSnapshot) postgresCatalogDataset {
	dataset := newPostgresCatalogDataset(
		[]string{"oid", "relname", "relnamespace", "relkind", "reltuples", "relpages"},
		oidCatalogColumn("oid"), textCatalogColumn("relname"), oidCatalogColumn("relnamespace"),
		textCatalogColumn("relkind"), float4CatalogColumn("reltuples"), int8CatalogColumn("relpages"),
		boolCatalogColumn("relhasindex"), boolCatalogColumn("relrowsecurity"), textCatalogColumn("nspname"),
	)
	for _, sequence := range catalog.sequences {
		dataset.rows = append(dataset.rows, map[string]any{
			"oid": postgresCatalogOID("sequence", sequence.ID), "relname": sequence.Name,
			"relnamespace": postgresPublicNamespaceOID, "relkind": "S", "reltuples": float64(1),
			"relpages": int64(0), "relhasindex": false, "relrowsecurity": false, "nspname": "public",
		})
	}
	for _, relation := range catalog.relations {
		indexes := catalogIndexesFor(relation.schema)
		reltuples := any(float64(-1))
		if relation.table.Analyzed {
			reltuples = float64(relation.table.Rows)
		}
		dataset.rows = append(dataset.rows, map[string]any{
			"oid": postgresCatalogOID("table", relation.schema.ID), "relname": relation.schema.Name,
			"relnamespace": postgresPublicNamespaceOID, "relkind": "r", "reltuples": reltuples,
			"relpages": int64(0), "relhasindex": len(indexes) != 0, "relrowsecurity": false,
			"nspname": "public",
		})
		for _, index := range indexes {
			dataset.rows = append(dataset.rows, map[string]any{
				"oid": index.indexOID, "relname": index.name, "relnamespace": postgresPublicNamespaceOID,
				"relkind": "i", "reltuples": float64(0), "relpages": int64(0),
				"relhasindex": false, "relrowsecurity": false, "nspname": "public",
			})
		}
	}
	return dataset
}

func postgresNamespaces() postgresCatalogDataset {
	dataset := newPostgresCatalogDataset(
		[]string{"oid", "nspname", "nspowner"},
		oidCatalogColumn("oid"), textCatalogColumn("nspname"), oidCatalogColumn("nspowner"),
	)
	dataset.rows = append(dataset.rows,
		map[string]any{"oid": postgresPublicNamespaceOID, "nspname": "public", "nspowner": uint32(10)},
		map[string]any{"oid": uint32(11), "nspname": "pg_catalog", "nspowner": uint32(10)},
		map[string]any{"oid": uint32(13207), "nspname": "information_schema", "nspowner": uint32(10)},
	)
	return dataset
}

func postgresTypes() postgresCatalogDataset {
	dataset := newPostgresCatalogDataset(
		[]string{"oid", "typname", "typnamespace", "typlen", "typbyval", "typtype", "typcategory"},
		oidCatalogColumn("oid"), textCatalogColumn("typname"), oidCatalogColumn("typnamespace"),
		int8CatalogColumn("typlen"), boolCatalogColumn("typbyval"), textCatalogColumn("typtype"),
		textCatalogColumn("typcategory"), boolCatalogColumn("typispreferred"),
		boolCatalogColumn("typisdefined"), textCatalogColumn("typdelim"),
		oidCatalogColumn("typrelid"), oidCatalogColumn("typelem"), oidCatalogColumn("typarray"),
	)
	seen := make(map[uint32]struct{})
	for _, typeInfo := range kitdbsql.AllTypes() {
		postgres := typeInfo.Catalog
		if postgres.OID == 0 {
			continue
		}
		if _, duplicate := seen[postgres.OID]; duplicate {
			continue
		}
		seen[postgres.OID] = struct{}{}
		category := postgresTypeCategory(typeInfo.Family)
		if typeInfo.ID == kitdbsql.TypeUUID {
			category = "U"
		}
		dataset.rows = append(dataset.rows, map[string]any{
			"oid": postgres.OID, "typname": postgres.UDTName, "typnamespace": uint32(11),
			"typlen": int64(postgres.Size), "typbyval": postgresTypeByValue(postgres.OID),
			"typtype": "b", "typcategory": category,
			"typispreferred": false, "typisdefined": true, "typdelim": ",",
			"typrelid": uint32(0), "typelem": uint32(0), "typarray": uint32(0),
		})
	}
	sort.Slice(dataset.rows, func(left, right int) bool {
		return dataset.rows[left]["oid"].(uint32) < dataset.rows[right]["oid"].(uint32)
	})
	return dataset
}

func postgresTypeByValue(oid uint32) bool {
	switch oid {
	case kitdbsql.PostgreSQLOIDBool, kitdbsql.PostgreSQLOIDInt2, kitdbsql.PostgreSQLOIDInt4,
		kitdbsql.PostgreSQLOIDInt8, kitdbsql.PostgreSQLOIDOID,
		kitdbsql.PostgreSQLOIDFloat8, kitdbsql.PostgreSQLOIDDate, kitdbsql.PostgreSQLOIDTime,
		kitdbsql.PostgreSQLOIDTimestamp, kitdbsql.PostgreSQLOIDTimestampTZ:
		return true
	default:
		return false
	}
}

func postgresTypeCategory(family kitdbsql.Family) string {
	switch family {
	case kitdbsql.FamilyBoolean:
		return "B"
	case kitdbsql.FamilyInteger, kitdbsql.FamilyFloat, kitdbsql.FamilyDecimal, kitdbsql.FamilySystem:
		return "N"
	case kitdbsql.FamilyIdentifier, kitdbsql.FamilyText, kitdbsql.FamilyChoice, kitdbsql.FamilyNetwork:
		return "S"
	case kitdbsql.FamilyTemporal:
		return "D"
	default:
		return "U"
	}
}

func postgresAttributes(catalog postgresCatalogSnapshot) postgresCatalogDataset {
	dataset := newPostgresCatalogDataset(
		[]string{"attrelid", "attname", "atttypid", "attnum", "attnotnull", "attisdropped"},
		oidCatalogColumn("attrelid"), textCatalogColumn("attname"), oidCatalogColumn("atttypid"),
		int8CatalogColumn("attnum"), boolCatalogColumn("attnotnull"), boolCatalogColumn("attisdropped"),
		int8CatalogColumn("attlen"), int8CatalogColumn("atttypmod"), textCatalogColumn("attidentity"), boolCatalogColumn("atthasdef"),
	)
	for _, relation := range catalog.relations {
		fields := append([]kitdbsql.Field(nil), relation.schema.Fields...)
		sort.Slice(fields, func(left, right int) bool { return fields[left].Position < fields[right].Position })
		for _, field := range fields {
			_, found := kitdbsql.LookupKind(field.Kind)
			if !found {
				continue
			}
			postgres, _ := postgresCatalogTypeForField(field)
			dataset.rows = append(dataset.rows, map[string]any{
				"attrelid": postgresCatalogOID("table", relation.schema.ID), "attname": field.Name,
				"atttypid": postgres.OID, "attnum": int64(field.Position + 1),
				"attnotnull": field.NotNull, "attisdropped": false, "attlen": int64(postgres.Size),
				"atttypmod": int64(postgresFieldTypeModifier(field)), "attidentity": fieldIdentityMode(field), "atthasdef": postgresColumnDefault(field) != nil,
			})
		}
	}
	return dataset
}

func postgresFieldTypeModifier(field kitdbsql.Field) int32 {
	if field.Kind == "decimal" && field.Precision != 0 {
		return postgresNumericTypeModifier(field.Precision, field.Scale)
	}
	if field.TimePrecision != nil && (field.Kind == "time" || field.Kind == "timestamp" || field.Kind == "timestamptz") {
		return int32(*field.TimePrecision)
	}
	if field.TextLength != nil && (field.Kind == "varchar" || field.Kind == "char") {
		return postgresTextTypeModifier(*field.TextLength)
	}
	return -1
}

func postgresIndexCatalog(catalog postgresCatalogSnapshot) postgresCatalogDataset {
	dataset := newPostgresCatalogDataset(
		[]string{"indexrelid", "indrelid", "indisunique", "indisprimary", "indkey"},
		oidCatalogColumn("indexrelid"), oidCatalogColumn("indrelid"),
		boolCatalogColumn("indisunique"), boolCatalogColumn("indisprimary"),
		textCatalogColumn("indkey"), boolCatalogColumn("indisvalid"), boolCatalogColumn("indisready"),
		textCatalogColumn("indexdef"), textCatalogColumn("indexname"), textCatalogColumn("tablename"),
	)
	for _, relation := range catalog.relations {
		for _, index := range catalogIndexesFor(relation.schema) {
			positions := make([]string, len(index.fields))
			for position, field := range index.fields {
				positions[position] = strconv.Itoa(field.Position + 1)
			}
			dataset.rows = append(dataset.rows, map[string]any{
				"indexrelid": index.indexOID, "indrelid": index.tableOID,
				"indisunique": index.unique, "indisprimary": index.primary, "indkey": strings.Join(positions, " "),
				"indisvalid": true, "indisready": true, "indexdef": index.definition,
				"indexname": index.name, "tablename": relation.schema.Name,
			})
		}
	}
	return dataset
}

func postgresConstraints(catalog postgresCatalogSnapshot) postgresCatalogDataset {
	dataset := newPostgresCatalogDataset(
		[]string{"oid", "conname", "contype", "connamespace", "conrelid", "confrelid", "conkey"},
		oidCatalogColumn("oid"), textCatalogColumn("conname"), textCatalogColumn("contype"),
		oidCatalogColumn("connamespace"), oidCatalogColumn("conrelid"), oidCatalogColumn("confrelid"),
		textCatalogColumn("conkey"), boolCatalogColumn("convalidated"),
	)
	for _, relation := range catalog.relations {
		for _, constraint := range catalogConstraintsFor(catalog, relation) {
			positions := make([]string, len(constraint.fields))
			for position, field := range constraint.fields {
				positions[position] = strconv.Itoa(field.Position + 1)
			}
			dataset.rows = append(dataset.rows, map[string]any{
				"oid": constraint.constraint, "conname": constraint.name, "contype": constraint.kind,
				"connamespace": postgresPublicNamespaceOID, "conrelid": constraint.tableOID,
				"confrelid": constraint.targetOID, "conkey": strings.Join(positions, " "), "convalidated": true,
			})
		}
	}
	return dataset
}

func postgresDatabases(catalog postgresCatalogSnapshot) postgresCatalogDataset {
	dataset := newPostgresCatalogDataset(
		[]string{"oid", "datname", "datdba", "encoding", "datcollate", "datctype"},
		oidCatalogColumn("oid"), textCatalogColumn("datname"), oidCatalogColumn("datdba"),
		int8CatalogColumn("encoding"), textCatalogColumn("datcollate"), textCatalogColumn("datctype"),
		boolCatalogColumn("datistemplate"), boolCatalogColumn("datallowconn"),
	)
	names := append([]string(nil), catalog.databases...)
	if len(names) == 0 {
		names = []string{catalog.database}
	}
	sort.Slice(names, func(left, right int) bool {
		return strings.ToLower(names[left]) < strings.ToLower(names[right])
	})
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		key := strings.ToLower(strings.TrimSpace(name))
		if key == "" {
			continue
		}
		if _, found := seen[key]; found {
			continue
		}
		seen[key] = struct{}{}
		dataset.rows = append(dataset.rows, map[string]any{
			"oid": postgresCatalogOID("database", name), "datname": name,
			"datdba": uint32(10), "encoding": int64(6), "datcollate": "C", "datctype": "C",
			"datistemplate": false, "datallowconn": true,
		})
	}
	return dataset
}

func postgresCatalogSchemata(catalog postgresCatalogSnapshot) postgresCatalogDataset {
	dataset := newPostgresCatalogDataset(
		[]string{"catalog_name", "schema_name", "schema_owner"},
		textCatalogColumn("catalog_name"), textCatalogColumn("schema_name"), textCatalogColumn("schema_owner"),
	)
	dataset.rows = append(dataset.rows, map[string]any{
		"catalog_name": catalog.database, "schema_name": "public", "schema_owner": "kitdb",
	})
	return dataset
}

func catalogIndexesFor(schema kitdbsql.Schema) []postgresCatalogIndex {
	result := make([]postgresCatalogIndex, 0)
	tableOID := postgresCatalogOID("table", schema.ID)
	primary := schema.PrimaryFields()
	if len(primary) != 0 {
		name := schema.Name + "_pkey"
		result = append(result, postgresCatalogIndex{
			name: name, fields: primary, unique: true, primary: true, tableOID: tableOID,
			indexOID:   postgresCatalogOID("index", schema.ID+":"+name),
			definition: postgresIndexDefinition(schema.Name, name, primary, true, nil),
		})
	}
	for _, field := range schema.Fields {
		if !field.Unique || field.Primary {
			continue
		}
		name := "unique_" + schema.Name + "_" + field.Name
		result = append(result, postgresCatalogIndex{
			name: name, fields: []kitdbsql.Field{field}, unique: true, tableOID: tableOID,
			indexOID:   postgresCatalogOID("index", schema.ID+":"+name),
			definition: postgresIndexDefinition(schema.Name, name, []kitdbsql.Field{field}, true, nil),
		})
	}
	for _, constraint := range schema.UniqueConstraints {
		fields := fieldsByTags(schema, constraint.Fields)
		result = append(result, postgresCatalogIndex{
			name: constraint.Name, fields: fields, unique: true, tableOID: tableOID,
			indexOID:   postgresCatalogOID("index", schema.ID+":"+constraint.ID),
			definition: postgresIndexDefinition(schema.Name, constraint.Name, fields, true, nil),
		})
	}
	indexes, err := collectSecondaryIndexes(schema)
	if err == nil {
		for _, index := range indexes {
			if index.implicit {
				continue
			}
			result = append(result, postgresCatalogIndex{
				name: index.name, fields: index.fields, filter: index.filter, tableOID: tableOID,
				indexOID:   postgresCatalogOID("index", schema.ID+":"+index.id),
				definition: postgresIndexDefinition(schema.Name, index.name, index.fields, false, index.filter),
			})
		}
	}
	sort.Slice(result, func(left, right int) bool {
		return strings.ToLower(result[left].name) < strings.ToLower(result[right].name)
	})
	return result
}

func catalogConstraintsFor(catalog postgresCatalogSnapshot, relation postgresCatalogRelation) []postgresCatalogConstraint {
	schema := relation.schema
	tableOID := postgresCatalogOID("table", schema.ID)
	result := make([]postgresCatalogConstraint, 0)
	if primary := schema.PrimaryFields(); len(primary) != 0 {
		name := schema.Name + "_pkey"
		result = append(result, postgresCatalogConstraint{
			name: name, kind: "p", fields: primary, tableOID: tableOID,
			constraint: postgresCatalogOID("constraint", schema.ID+":"+name),
		})
	}
	for _, field := range schema.Fields {
		if !field.Unique || field.Primary {
			continue
		}
		name := "unique_" + schema.Name + "_" + field.Name
		result = append(result, postgresCatalogConstraint{
			name: name, kind: "u", fields: []kitdbsql.Field{field}, tableOID: tableOID,
			constraint: postgresCatalogOID("constraint", schema.ID+":"+name),
		})
	}
	for _, constraint := range schema.UniqueConstraints {
		result = append(result, postgresCatalogConstraint{
			name: constraint.Name, kind: "u", fields: fieldsByTags(schema, constraint.Fields), tableOID: tableOID,
			constraint: postgresCatalogOID("constraint", schema.ID+":"+constraint.ID),
		})
	}
	for _, constraint := range schema.ForeignConstraints {
		targetOID := uint32(0)
		for _, candidate := range catalog.relations {
			if candidate.schema.ID == constraint.TargetStructID || strings.EqualFold(candidate.schema.Name, constraint.TargetStruct) {
				targetOID = postgresCatalogOID("table", candidate.schema.ID)
				break
			}
		}
		result = append(result, postgresCatalogConstraint{
			name: constraint.Name, kind: "f", fields: fieldsByTags(schema, constraint.Fields),
			tableOID: tableOID, targetOID: targetOID,
			constraint: postgresCatalogOID("constraint", schema.ID+":"+constraint.ID),
		})
	}
	for _, constraint := range schema.CheckConstraints {
		result = append(result, postgresCatalogConstraint{
			name: constraint.Name, kind: "c", tableOID: tableOID,
			constraint: postgresCatalogOID("constraint", schema.ID+":"+constraint.ID),
		})
	}
	sort.Slice(result, func(left, right int) bool {
		return strings.ToLower(result[left].name) < strings.ToLower(result[right].name)
	})
	return result
}

func fieldsByTags(schema kitdbsql.Schema, tags []uint32) []kitdbsql.Field {
	result := make([]kitdbsql.Field, 0, len(tags))
	for _, tag := range tags {
		if field, found := fieldByTag(schema, tag); found {
			result = append(result, field)
		}
	}
	return result
}

func postgresIndexDefinition(table, name string, fields []kitdbsql.Field, unique bool, filter []kitdbsql.IndexCondition) string {
	columns := make([]string, len(fields))
	for position, field := range fields {
		columns[position] = quotePostgresIdentifier(field.Name)
	}
	definition := "CREATE "
	if unique {
		definition += "UNIQUE "
	}
	definition += "INDEX " + quotePostgresIdentifier(name) + " ON public." + quotePostgresIdentifier(table) +
		" (" + strings.Join(columns, ", ") + ")"
	if len(filter) != 0 {
		conditions := make([]string, 0, len(filter))
		for _, condition := range filter {
			var value any
			if err := json.Unmarshal(condition.Value, &value); err != nil {
				continue
			}
			if value == nil {
				conditions = append(conditions, quotePostgresIdentifier(condition.Field)+" IS NULL")
			} else {
				conditions = append(conditions, quotePostgresIdentifier(condition.Field)+" = "+postgresCatalogLiteral(value))
			}
		}
		if len(conditions) != 0 {
			definition += " WHERE " + strings.Join(conditions, " AND ")
		}
	}
	return definition
}

func postgresColumnDefault(field kitdbsql.Field) any {
	if field.Sequence != nil {
		if fieldIdentityMode(field) != "" {
			return nil
		}
		return "nextval(" + postgresCatalogLiteral("public."+quotePostgresIdentifier(field.Sequence.Name)) + ")"
	}
	if field.DefaultNow {
		switch field.Kind {
		case "date":
			return "CURRENT_DATE"
		case "time":
			return "CURRENT_TIME"
		case "timestamp":
			return "LOCALTIMESTAMP"
		default:
			return "CURRENT_TIMESTAMP"
		}
	}
	if !field.HasDefault {
		return nil
	}
	var value any
	if err := json.Unmarshal(field.Default, &value); err != nil {
		return string(field.Default)
	}
	return postgresCatalogLiteral(value)
}

func postgresCatalogLiteral(value any) string {
	switch item := value.(type) {
	case nil:
		return "NULL"
	case string:
		return "'" + strings.ReplaceAll(item, "'", "''") + "'"
	case bool:
		if item {
			return "TRUE"
		}
		return "FALSE"
	default:
		return fmt.Sprint(item)
	}
}

func quotePostgresIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func postgresConstraintType(kind string) string {
	switch kind {
	case "p":
		return "PRIMARY KEY"
	case "u":
		return "UNIQUE"
	case "f":
		return "FOREIGN KEY"
	case "c":
		return "CHECK"
	default:
		return ""
	}
}

func postgresCatalogOID(kind, identity string) uint32 {
	digest := sha256.Sum256([]byte("kitdb:postgres-catalog:" + kind + ":" + identity))
	value := binary.BigEndian.Uint32(digest[:4]) & 0x7fffffff
	if value < 16_384 {
		value += 16_384
	}
	return value
}

func projectPostgresCatalog(source string, dataset postgresCatalogDataset) ([]postgresCatalogProjection, bool, error) {
	selectList, found := postgresCatalogSelectList(source)
	if !found {
		return nil, false, fmt.Errorf("KitDB PostgreSQL catalog query needs SELECT ... FROM")
	}
	selectList = strings.TrimSpace(selectList)
	if strings.HasPrefix(strings.ToLower(selectList), "distinct ") {
		selectList = strings.TrimSpace(selectList[len("distinct "):])
	}
	parts := splitPostgresCatalogList(selectList)
	if len(parts) == 1 && isPostgresCatalogCount(parts[0]) {
		name := postgresCatalogAlias(parts[0])
		if name == "" {
			name = "count"
		}
		return []postgresCatalogProjection{{name: name}}, true, nil
	}
	result := make([]postgresCatalogProjection, 0, len(parts))
	for _, part := range parts {
		expression, alias := splitPostgresCatalogAlias(part)
		if expression == "*" || strings.HasSuffix(strings.TrimSpace(expression), ".*") {
			for _, name := range dataset.defaults {
				column := dataset.columns[name]
				result = append(result, postgresCatalogProjection{
					name: name, source: name,
					column: pgwire.Column{Name: name, DataTypeOID: column.oid, DataTypeSize: column.size},
				})
			}
			continue
		}
		sourceName, constant := resolvePostgresCatalogExpression(expression, dataset)
		name := alias
		if name == "" {
			name = sourceName
		}
		if name == "" {
			name = "?column?"
		}
		column := dataset.columns[sourceName]
		if column.oid == 0 {
			column = textCatalogColumn(sourceName)
		}
		cast := postgresCatalogCast(expression)
		if cast != "" {
			column = postgresCatalogColumnForCast(sourceName, cast, column)
		}
		result = append(result, postgresCatalogProjection{
			name: name, source: sourceName, constant: constant,
			cast:   cast,
			column: pgwire.Column{Name: name, DataTypeOID: column.oid, DataTypeSize: column.size},
		})
	}
	if len(result) == 0 {
		return nil, false, fmt.Errorf("KitDB PostgreSQL catalog query has no projection")
	}
	return result, false, nil
}

func resolvePostgresCatalogExpression(expression string, dataset postgresCatalogDataset) (string, *string) {
	trimmed := strings.TrimSpace(expression)
	withoutCast := trimmed
	if marker := strings.Index(withoutCast, "::"); marker >= 0 {
		withoutCast = strings.TrimSpace(withoutCast[:marker])
	}
	if len(withoutCast) >= 2 && withoutCast[0] == '\'' && withoutCast[len(withoutCast)-1] == '\'' {
		value := strings.ReplaceAll(withoutCast[1:len(withoutCast)-1], "''", "'")
		return "", &value
	}
	identifier := normalizePostgresCatalogIdentifier(withoutCast)
	if _, found := dataset.columns[identifier]; found {
		return identifier, nil
	}
	lower := strings.ToLower(withoutCast)
	if strings.Contains(lower, "pg_get_indexdef") {
		if _, found := dataset.columns["indexdef"]; found {
			return "indexdef", nil
		}
	}
	words := postgresCatalogWords(withoutCast)
	for index := len(words) - 1; index >= 0; index-- {
		if _, found := dataset.columns[words[index]]; found {
			return words[index], nil
		}
	}
	return identifier, nil
}

func filterPostgresCatalogRows(source string, dataset *postgresCatalogDataset) {
	if dataset == nil || len(dataset.rows) == 0 {
		return
	}
	filterNames := make([]string, 0, len(dataset.columns))
	for name := range dataset.columns {
		filterNames = append(filterNames, name)
	}
	sort.Strings(filterNames)
	filters := make(map[string]string)
	for _, name := range filterNames {
		if value, found := postgresCatalogEquality(source, name); found {
			filters[name] = value
		}
	}
	if len(filters) == 0 {
		return
	}
	rows := dataset.rows[:0]
	for _, row := range dataset.rows {
		matches := true
		for name, expected := range filters {
			value, exists := row[name]
			if !exists || value == nil || fmt.Sprint(value) != expected {
				matches = false
				break
			}
		}
		if matches {
			rows = append(rows, row)
		}
	}
	dataset.rows = rows
}

func postgresCatalogEquality(source, name string) (string, bool) {
	prefix := `(?i)(?:[a-z_][a-z0-9_]*\s*\.\s*)?"?` + regexp.QuoteMeta(name) + `"?\s*=\s*`
	quoted := regexp.MustCompile(prefix + `'((?:''|[^'])*)'`).FindStringSubmatch(source)
	if len(quoted) == 2 {
		return strings.ReplaceAll(quoted[1], "''", "'"), true
	}
	literal := regexp.MustCompile(prefix + `([-+]?(?:[0-9]+(?:\.[0-9]+)?|true|false))\b`).FindStringSubmatch(source)
	if len(literal) == 2 {
		return strings.ToLower(literal[1]), true
	}
	return "", false
}

func postgresCatalogLimit(source string) int {
	matches := regexp.MustCompile(`(?i)\blimit\s+([0-9]+)`).FindStringSubmatch(source)
	if len(matches) != 2 {
		return -1
	}
	limit, err := strconv.Atoi(matches[1])
	if err != nil {
		return -1
	}
	return limit
}

func postgresCatalogSelectList(source string) (string, bool) {
	lower := strings.ToLower(source)
	selectAt := strings.Index(lower, "select")
	if selectAt < 0 {
		return "", false
	}
	depth := 0
	single, double := false, false
	for index := selectAt + len("select"); index < len(source); index++ {
		character := source[index]
		switch character {
		case '\'':
			if !double {
				if single && index+1 < len(source) && source[index+1] == '\'' {
					index++
					continue
				}
				single = !single
			}
		case '"':
			if !single {
				double = !double
			}
		case '(':
			if !single && !double {
				depth++
			}
		case ')':
			if !single && !double && depth > 0 {
				depth--
			}
		default:
			if !single && !double && depth == 0 && hasPostgresCatalogWordAt(lower, index, "from") {
				return source[selectAt+len("select") : index], true
			}
		}
	}
	return "", false
}

func splitPostgresCatalogList(source string) []string {
	result := make([]string, 0, 4)
	start, depth := 0, 0
	single, double := false, false
	for index := 0; index < len(source); index++ {
		switch source[index] {
		case '\'':
			if !double {
				if single && index+1 < len(source) && source[index+1] == '\'' {
					index++
					continue
				}
				single = !single
			}
		case '"':
			if !single {
				double = !double
			}
		case '(':
			if !single && !double {
				depth++
			}
		case ')':
			if !single && !double && depth > 0 {
				depth--
			}
		case ',':
			if !single && !double && depth == 0 {
				result = append(result, strings.TrimSpace(source[start:index]))
				start = index + 1
			}
		}
	}
	if tail := strings.TrimSpace(source[start:]); tail != "" {
		result = append(result, tail)
	}
	return result
}

func splitPostgresCatalogAlias(source string) (string, string) {
	lower := strings.ToLower(source)
	depth := 0
	single, double := false, false
	last := -1
	for index := 0; index < len(source); index++ {
		switch source[index] {
		case '\'':
			if !double {
				single = !single
			}
		case '"':
			if !single {
				double = !double
			}
		case '(':
			if !single && !double {
				depth++
			}
		case ')':
			if !single && !double && depth > 0 {
				depth--
			}
		default:
			if !single && !double && depth == 0 && hasPostgresCatalogWordAt(lower, index, "as") {
				last = index
			}
		}
	}
	if last < 0 {
		return strings.TrimSpace(source), ""
	}
	return strings.TrimSpace(source[:last]), normalizePostgresCatalogIdentifier(source[last+2:])
}

func postgresCatalogAlias(source string) string {
	_, alias := splitPostgresCatalogAlias(source)
	return alias
}

func postgresCatalogCast(source string) string {
	marker := strings.LastIndex(source, "::")
	if marker < 0 {
		return ""
	}
	rest := strings.TrimSpace(source[marker+2:])
	if as := strings.Index(strings.ToLower(rest), " as "); as >= 0 {
		rest = strings.TrimSpace(rest[:as])
	}
	return normalizePostgresCatalogIdentifier(rest)
}

func postgresCatalogColumnForCast(name, cast string, fallback postgresCatalogColumn) postgresCatalogColumn {
	switch cast {
	case "int8", "bigint", "int4", "integer", "int2", "smallint":
		return int8CatalogColumn(name)
	case "oid":
		return oidCatalogColumn(name)
	case "bool", "boolean":
		return boolCatalogColumn(name)
	case "float4", "real":
		return float4CatalogColumn(name)
	case "text", "varchar", "character varying":
		return textCatalogColumn(name)
	default:
		return fallback
	}
}

func isPostgresCatalogCount(source string) bool {
	expression, _ := splitPostgresCatalogAlias(source)
	normalized := strings.ToLower(strings.Join(strings.Fields(expression), ""))
	return normalized == "count(*)"
}

func normalizePostgresCatalogSQL(source string) string {
	source = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(source), ";"))
	source = strings.ReplaceAll(source, `"`, "")
	return strings.ToLower(strings.Join(strings.Fields(source), " "))
}

func normalizePostgresCatalogIdentifier(source string) string {
	value := strings.TrimSpace(source)
	if marker := strings.LastIndex(value, "."); marker >= 0 {
		value = value[marker+1:]
	}
	value = strings.TrimSpace(value)
	value = strings.Trim(value, `"`)
	return strings.ToLower(value)
}

func postgresCatalogWords(source string) []string {
	return strings.FieldsFunc(strings.ToLower(source), func(character rune) bool {
		return !(unicode.IsLetter(character) || unicode.IsDigit(character) || character == '_')
	})
}

func containsPostgresRelation(normalized, relation string) bool {
	for _, marker := range []string{
		"from " + relation,
		"join " + relation,
		"," + relation,
		", " + relation,
	} {
		remaining := normalized
		for {
			index := strings.Index(remaining, marker)
			if index < 0 {
				break
			}
			after := index + len(marker)
			if after == len(remaining) || !isPostgresCatalogIdentifierByte(remaining[after]) {
				return true
			}
			remaining = remaining[index+1:]
		}
	}
	return false
}

func isPostgresCatalogIdentifierByte(value byte) bool {
	return value == '_' || value == '.' ||
		value >= 'a' && value <= 'z' ||
		value >= '0' && value <= '9'
}

func hasPostgresCatalogWordAt(source string, index int, word string) bool {
	if index < 0 || index+len(word) > len(source) || source[index:index+len(word)] != word {
		return false
	}
	leftOK := index == 0 || !(source[index-1] == '_' || source[index-1] >= 'a' && source[index-1] <= 'z' || source[index-1] >= '0' && source[index-1] <= '9')
	right := index + len(word)
	rightOK := right == len(source) || !(source[right] == '_' || source[right] >= 'a' && source[right] <= 'z' || source[right] >= '0' && source[right] <= '9')
	return leftOK && rightOK
}

func newPostgresCatalogDataset(defaults []string, columns ...postgresCatalogColumn) postgresCatalogDataset {
	result := postgresCatalogDataset{columns: make(map[string]postgresCatalogColumn, len(columns)), defaults: defaults}
	for _, column := range columns {
		result.columns[column.name] = column
	}
	return result
}

func emptyPostgresCatalogDataset(columns []postgresCatalogColumn) postgresCatalogDataset {
	defaults := make([]string, len(columns))
	for index, column := range columns {
		defaults[index] = column.name
	}
	return newPostgresCatalogDataset(defaults, columns...)
}

func textCatalogColumn(name string) postgresCatalogColumn {
	return postgresCatalogColumn{name: name, oid: pgwire.OIDText, size: -1}
}

func int8CatalogColumn(name string) postgresCatalogColumn {
	return postgresCatalogColumn{name: name, oid: pgwire.OIDInt8, size: 8}
}

func oidCatalogColumn(name string) postgresCatalogColumn {
	return postgresCatalogColumn{name: name, oid: pgwire.OIDOID, size: 4}
}

func boolCatalogColumn(name string) postgresCatalogColumn {
	return postgresCatalogColumn{name: name, oid: pgwire.OIDBool, size: 1}
}

func float4CatalogColumn(name string) postgresCatalogColumn {
	return postgresCatalogColumn{name: name, oid: pgwire.OIDFloat4, size: 4}
}

func postgresCatalogField(value any) pgwire.Field {
	switch item := value.(type) {
	case bool:
		if item {
			return pgwire.Field{Data: []byte("t")}
		}
		return pgwire.Field{Data: []byte("f")}
	case []byte:
		return pgwire.Field{Data: append([]byte(nil), item...)}
	default:
		return pgwire.Field{Data: []byte(fmt.Sprint(item))}
	}
}

func postgresCatalogProjectionField(value any, cast string) (pgwire.Field, error) {
	switch cast {
	case "int8", "bigint", "int4", "integer", "int2", "smallint":
		integer, err := postgresCatalogInt64(value)
		if err != nil {
			return pgwire.Field{}, fmt.Errorf("KitDB PostgreSQL catalog cannot cast %v to %s", value, cast)
		}
		return pgwire.Field{Data: []byte(strconv.FormatInt(integer, 10))}, nil
	case "oid":
		integer, err := postgresCatalogInt64(value)
		if err != nil || integer < 0 || uint64(integer) > math.MaxUint32 {
			return pgwire.Field{}, fmt.Errorf("KitDB PostgreSQL catalog cannot cast %v to oid", value)
		}
		return pgwire.Field{Data: []byte(strconv.FormatInt(integer, 10))}, nil
	case "text", "varchar", "character varying":
		return pgwire.Field{Data: []byte(fmt.Sprint(value))}, nil
	default:
		return postgresCatalogField(value), nil
	}
}

func postgresCatalogInt64(value any) (int64, error) {
	switch item := value.(type) {
	case int:
		return int64(item), nil
	case int8:
		return int64(item), nil
	case int16:
		return int64(item), nil
	case int32:
		return int64(item), nil
	case int64:
		return item, nil
	case uint:
		if uint64(item) <= math.MaxInt64 {
			return int64(item), nil
		}
	case uint8:
		return int64(item), nil
	case uint16:
		return int64(item), nil
	case uint32:
		return int64(item), nil
	case uint64:
		if item <= math.MaxInt64 {
			return int64(item), nil
		}
	case float32:
		return postgresCatalogRoundedInt64(float64(item))
	case float64:
		return postgresCatalogRoundedInt64(item)
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(item), 64)
		if err == nil {
			return postgresCatalogRoundedInt64(parsed)
		}
	}
	return 0, fmt.Errorf("not an int64")
}

func postgresCatalogRoundedInt64(value float64) (int64, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, fmt.Errorf("not finite")
	}
	rounded := math.Round(value)
	if rounded < -float64(uint64(1)<<63) || rounded >= float64(uint64(1)<<63) {
		return 0, fmt.Errorf("outside int64")
	}
	return int64(rounded), nil
}

func postgresYesNo(value bool) string {
	if value {
		return "YES"
	}
	return "NO"
}
