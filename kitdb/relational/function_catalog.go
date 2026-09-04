package relational

import kitdbsql "github.com/kitwork/engine/kitdb/sql"

func informationSchemaFunctions(catalog postgresCatalogSnapshot) postgresCatalogDataset {
	dataset := newPostgresCatalogDataset(
		[]string{"routine_schema", "routine_name", "routine_type", "data_type"},
		textCatalogColumn("specific_catalog"), textCatalogColumn("specific_schema"), textCatalogColumn("specific_name"),
		textCatalogColumn("routine_catalog"), textCatalogColumn("routine_schema"), textCatalogColumn("routine_name"),
		textCatalogColumn("routine_type"), textCatalogColumn("data_type"), textCatalogColumn("external_language"),
		textCatalogColumn("sql_data_access"), textCatalogColumn("is_deterministic"),
	)
	for _, function := range catalog.functions {
		typeInfo, _ := kitdbsql.LookupKind(function.ReturnKind)
		dataset.rows = append(dataset.rows, map[string]any{
			"specific_catalog": catalog.database, "specific_schema": "public", "specific_name": function.Name + "_" + function.ID,
			"routine_catalog": catalog.database, "routine_schema": "public", "routine_name": function.Name,
			"routine_type": "FUNCTION", "data_type": typeInfo.Catalog.DataType,
			"external_language": "SQL", "sql_data_access": "NO SQL", "is_deterministic": "YES",
		})
	}
	return dataset
}

func postgresFunctions(catalog postgresCatalogSnapshot) postgresCatalogDataset {
	dataset := newPostgresCatalogDataset(
		[]string{"oid", "proname", "pronamespace", "prorettype", "pronargs"},
		oidCatalogColumn("oid"), textCatalogColumn("proname"), oidCatalogColumn("pronamespace"),
		oidCatalogColumn("prorettype"), int8CatalogColumn("pronargs"),
		textCatalogColumn("prokind"), textCatalogColumn("provolatile"), boolCatalogColumn("proretset"),
		boolCatalogColumn("prosecdef"), boolCatalogColumn("proisstrict"), textCatalogColumn("nspname"),
	)
	for _, function := range catalog.functions {
		typeInfo, _ := kitdbsql.LookupKind(function.ReturnKind)
		dataset.rows = append(dataset.rows, map[string]any{
			"oid": postgresCatalogOID("function", function.ID), "proname": function.Name,
			"pronamespace": postgresPublicNamespaceOID, "prorettype": typeInfo.Catalog.OID,
			"pronargs": int64(len(function.Parameters)), "prokind": "f", "provolatile": "i",
			"proretset": false, "prosecdef": false, "proisstrict": false, "nspname": "public",
		})
	}
	return dataset
}
