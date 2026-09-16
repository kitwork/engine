package relational

import kitdbsql "github.com/kitwork/engine/kitdb/sql"

func informationSchemaDomains(catalog postgresCatalogSnapshot) postgresCatalogDataset {
	dataset := newPostgresCatalogDataset(
		[]string{"domain_catalog", "domain_schema", "domain_name", "data_type", "domain_default"},
		textCatalogColumn("domain_catalog"), textCatalogColumn("domain_schema"), textCatalogColumn("domain_name"),
		textCatalogColumn("data_type"), textCatalogColumn("domain_default"),
		textCatalogColumn("udt_catalog"), textCatalogColumn("udt_schema"), textCatalogColumn("udt_name"),
		int8CatalogColumn("numeric_precision"), int8CatalogColumn("numeric_scale"), int8CatalogColumn("character_maximum_length"),
	)
	for _, domain := range catalog.domains {
		column := domain.Column
		info, _ := kitdbsql.LookupKind(column.Kind)
		row := map[string]any{
			"domain_catalog": catalog.database, "domain_schema": "public", "domain_name": domain.Name,
			"data_type": info.Catalog.DataType, "udt_catalog": catalog.database,
			"udt_schema": "pg_catalog", "udt_name": info.Catalog.UDTName,
		}
		if column.HasDefault {
			value, _ := resolveLiteral(column.Default, nil)
			row["domain_default"] = postgresCatalogLiteral(value)
		}
		if column.Precision != 0 {
			row["numeric_precision"], row["numeric_scale"] = int64(column.Precision), int64(column.Scale)
		}
		if column.TextLength != nil {
			row["character_maximum_length"] = int64(*column.TextLength)
		}
		dataset.rows = append(dataset.rows, row)
	}
	return dataset
}

func postgresTypesWithDomains(catalog postgresCatalogSnapshot) postgresCatalogDataset {
	dataset := postgresTypes()
	dataset.functionCatalog = &catalog
	for _, column := range []postgresCatalogColumn{
		oidCatalogColumn("typbasetype"), boolCatalogColumn("typnotnull"), textCatalogColumn("typdefault"), textCatalogColumn("nspname"),
		int8CatalogColumn("typtypmod"),
	} {
		dataset.columns[column.name] = column
	}
	for _, row := range dataset.rows {
		row["typbasetype"], row["typnotnull"], row["nspname"], row["typtypmod"] = uint32(0), false, "pg_catalog", int64(-1)
	}
	for _, domain := range catalog.domains {
		info, _ := kitdbsql.LookupKind(domain.Column.Kind)
		row := map[string]any{
			"oid": postgresCatalogOID("domain", domain.ID), "typname": domain.Name, "typnamespace": postgresPublicNamespaceOID,
			"typlen": int64(info.Catalog.Size), "typbyval": postgresTypeByValue(info.Catalog.OID),
			"typtype": "d", "typcategory": postgresTypeCategory(info.Family), "typispreferred": false, "typisdefined": true,
			"typdelim": ",", "typrelid": uint32(0), "typelem": uint32(0), "typarray": uint32(0),
			"typbasetype": info.Catalog.OID, "typnotnull": domain.Column.NotNull, "nspname": "public",
			"typtypmod": int64(postgresFieldTypeModifier(kitdbsql.Field{Kind: info.Kind, Precision: domain.Column.Precision, Scale: domain.Column.Scale, TextLength: domain.Column.TextLength})),
		}
		if domain.Column.HasDefault {
			value, _ := resolveLiteral(domain.Column.Default, nil)
			row["typdefault"] = postgresCatalogLiteral(value)
		}
		dataset.rows = append(dataset.rows, row)
	}
	return dataset
}

func informationSchemaDomainConstraints(catalog postgresCatalogSnapshot) postgresCatalogDataset {
	dataset := newPostgresCatalogDataset(
		[]string{"constraint_catalog", "constraint_schema", "constraint_name", "domain_catalog", "domain_schema", "domain_name", "is_deferrable", "initially_deferred"},
		textCatalogColumn("constraint_catalog"), textCatalogColumn("constraint_schema"), textCatalogColumn("constraint_name"),
		textCatalogColumn("domain_catalog"), textCatalogColumn("domain_schema"), textCatalogColumn("domain_name"),
		textCatalogColumn("is_deferrable"), textCatalogColumn("initially_deferred"),
	)
	for _, domain := range catalog.domains {
		for i := range domain.Column.Checks {
			dataset.rows = append(dataset.rows, map[string]any{
				"constraint_catalog": catalog.database, "constraint_schema": "public", "constraint_name": domainCheckName(domain, i),
				"domain_catalog": catalog.database, "domain_schema": "public", "domain_name": domain.Name,
				"is_deferrable": "NO", "initially_deferred": "NO",
			})
		}
	}
	return dataset
}

func informationSchemaChecks(catalog postgresCatalogSnapshot, domainsOnly bool) (postgresCatalogDataset, error) {
	dataset := newPostgresCatalogDataset(
		[]string{"constraint_catalog", "constraint_schema", "constraint_name", "check_clause"},
		textCatalogColumn("constraint_catalog"), textCatalogColumn("constraint_schema"), textCatalogColumn("constraint_name"), textCatalogColumn("check_clause"),
	)
	if domainsOnly {
		for _, name := range []string{"domain_catalog", "domain_schema", "domain_name", "is_deferrable", "initially_deferred"} {
			dataset.columns[name] = textCatalogColumn(name)
		}
	}
	appendCheck := func(name, expression string) {
		dataset.rows = append(dataset.rows, map[string]any{
			"constraint_catalog": catalog.database, "constraint_schema": "public", "constraint_name": name, "check_clause": expression,
		})
	}
	for _, relation := range catalog.relations {
		if domainsOnly {
			break
		}
		for _, check := range relation.schema.CheckConstraints {
			expression, err := catalogCheckExpressionSQL(check.Expression, relation.schema)
			if err != nil {
				return dataset, err
			}
			appendCheck(check.Name, expression)
		}
	}
	for _, domain := range catalog.domains {
		for i, check := range domain.Column.Checks {
			appendCheck(domainCheckName(domain, i), domainExpressionSQL(check.Expression))
			if domainsOnly {
				row := dataset.rows[len(dataset.rows)-1]
				row["domain_catalog"], row["domain_schema"], row["domain_name"] = catalog.database, "public", domain.Name
				row["is_deferrable"], row["initially_deferred"] = "NO", "NO"
			}
		}
	}
	return dataset, nil
}
