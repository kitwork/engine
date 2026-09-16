package relational

import (
	"strconv"

	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

func sequencePostgresType(sequenceType string) (kitdbsql.PostgreSQLType, int64) {
	typeInfo, _ := kitdbsql.ResolveName(sequenceType)
	precision := int64(64)
	switch typeInfo.ID {
	case kitdbsql.TypeSmallInt:
		precision = 16
	case kitdbsql.TypeInt32:
		precision = 32
	}
	return typeInfo.Catalog, precision
}

func informationSchemaSequences(catalog postgresCatalogSnapshot) postgresCatalogDataset {
	dataset := newPostgresCatalogDataset(
		[]string{"sequence_catalog", "sequence_schema", "sequence_name", "data_type", "start_value", "increment", "minimum_value", "maximum_value", "cycle_option"},
		textCatalogColumn("sequence_catalog"), textCatalogColumn("sequence_schema"), textCatalogColumn("sequence_name"),
		textCatalogColumn("data_type"), int8CatalogColumn("numeric_precision"), int8CatalogColumn("numeric_precision_radix"), int8CatalogColumn("numeric_scale"),
		textCatalogColumn("start_value"), textCatalogColumn("increment"), textCatalogColumn("minimum_value"), textCatalogColumn("maximum_value"), textCatalogColumn("cycle_option"),
	)
	for _, sequence := range catalog.sequences {
		postgres, precision := sequencePostgresType(sequence.DataTypeName())
		cycle := "NO"
		if sequence.Cycle {
			cycle = "YES"
		}
		dataset.rows = append(dataset.rows, map[string]any{
			"sequence_catalog": catalog.database, "sequence_schema": "public", "sequence_name": sequence.Name,
			"data_type": postgres.DataType, "numeric_precision": precision, "numeric_precision_radix": int64(2), "numeric_scale": int64(0),
			"start_value": strconv.FormatInt(sequence.Start, 10), "increment": strconv.FormatInt(sequence.Increment, 10),
			"minimum_value": strconv.FormatInt(sequence.Minimum, 10), "maximum_value": strconv.FormatInt(sequence.Maximum, 10), "cycle_option": cycle,
		})
	}
	return dataset
}

func postgresSequences(catalog postgresCatalogSnapshot) postgresCatalogDataset {
	dataset := newPostgresCatalogDataset(
		[]string{"seqrelid", "seqtypid", "seqstart", "seqincrement", "seqmax", "seqmin", "seqcache", "seqcycle"},
		oidCatalogColumn("seqrelid"), oidCatalogColumn("seqtypid"), int8CatalogColumn("seqstart"), int8CatalogColumn("seqincrement"),
		int8CatalogColumn("seqmax"), int8CatalogColumn("seqmin"), int8CatalogColumn("seqcache"), boolCatalogColumn("seqcycle"),
		textCatalogColumn("relname"), textCatalogColumn("nspname"), oidCatalogColumn("relnamespace"), oidCatalogColumn("relowner"),
	)
	dataset.functionCatalog = &catalog
	for _, sequence := range catalog.sequences {
		postgres, _ := sequencePostgresType(sequence.DataTypeName())
		dataset.rows = append(dataset.rows, map[string]any{
			"seqrelid": postgresCatalogOID("sequence", sequence.ID), "seqtypid": postgres.OID,
			"seqstart": sequence.Start, "seqincrement": sequence.Increment, "seqmax": sequence.Maximum,
			"seqmin": sequence.Minimum, "seqcache": sequence.CacheSize(), "seqcycle": sequence.Cycle,
			"relname": sequence.Name, "nspname": "public", "relnamespace": postgresPublicNamespaceOID, "relowner": postgresCatalogOID("role", catalog.user),
		})
	}
	return dataset
}
