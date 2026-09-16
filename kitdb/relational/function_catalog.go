package relational

import (
	"strings"

	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

func functionExpressionSQL(plan kitdbsql.ExpressionPlan) string {
	return scalarExpressionSQL(plan, quotePostgresIdentifier)
}

func scalarExpressionSQL(plan kitdbsql.ExpressionPlan, fieldSQL func(string) string) string {
	switch plan.Kind {
	case "field":
		return fieldSQL(plan.Field)
	case "literal":
		value, _ := resolveLiteral(plan.Literal, nil)
		return postgresCatalogLiteral(value)
	}
	arguments := make([]string, len(plan.Arguments))
	for i, child := range plan.Arguments {
		arguments[i] = scalarExpressionSQL(child, fieldSQL)
	}
	switch plan.Kind {
	case "binary":
		return "(" + arguments[0] + " " + strings.ToUpper(plan.Operator) + " " + arguments[1] + ")"
	case "unary":
		if plan.Operator == "is null" || plan.Operator == "is not null" {
			return "(" + arguments[0] + " " + strings.ToUpper(plan.Operator) + ")"
		}
		return "(" + strings.ToUpper(plan.Operator) + " " + arguments[0] + ")"
	case "function":
		return plan.Operator + "(" + strings.Join(arguments, ", ") + ")"
	}
	return ""
}

func functionArgumentsSQL(function *storedFunction) string {
	arguments := make([]string, len(function.Parameters))
	for i, parameter := range function.Parameters {
		typeInfo, _ := kitdbsql.LookupKind(parameter.Kind)
		arguments[i] = quotePostgresIdentifier(parameter.Name) + " " + typeInfo.Catalog.DataType
	}
	return strings.Join(arguments, ", ")
}

func functionDefinitionSQL(function *storedFunction) string {
	typeInfo, _ := kitdbsql.LookupKind(function.ReturnKind)
	return "CREATE OR REPLACE FUNCTION " + quotePostgresIdentifier(function.Name) + "(" + functionArgumentsSQL(function) + ")\nRETURNS " + typeInfo.Catalog.DataType + "\nLANGUAGE SQL\nRETURN " + functionExpressionSQL(function.Body) + ";"
}

func informationSchemaFunctions(catalog postgresCatalogSnapshot) postgresCatalogDataset {
	dataset := newPostgresCatalogDataset(
		[]string{"routine_schema", "routine_name", "routine_type", "data_type"},
		textCatalogColumn("specific_catalog"), textCatalogColumn("specific_schema"), textCatalogColumn("specific_name"),
		textCatalogColumn("routine_catalog"), textCatalogColumn("routine_schema"), textCatalogColumn("routine_name"),
		textCatalogColumn("routine_type"), textCatalogColumn("data_type"), textCatalogColumn("external_language"),
		textCatalogColumn("sql_data_access"), textCatalogColumn("is_deterministic"),
		textCatalogColumn("routine_definition"), textCatalogColumn("routine_body"),
	)
	for _, function := range catalog.functions {
		typeInfo, _ := kitdbsql.LookupKind(function.ReturnKind)
		dataset.rows = append(dataset.rows, map[string]any{
			"specific_catalog": catalog.database, "specific_schema": "public", "specific_name": function.Name + "_" + function.ID,
			"routine_catalog": catalog.database, "routine_schema": "public", "routine_name": function.Name,
			"routine_type": "FUNCTION", "data_type": typeInfo.Catalog.DataType,
			"external_language": "SQL", "sql_data_access": "NO SQL", "is_deterministic": "YES",
			"routine_definition": "RETURN " + functionExpressionSQL(function.Body), "routine_body": "SQL",
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
		textCatalogColumn("prosrc"), textCatalogColumn("proargnames"), textCatalogColumn("proargtypes"),
		boolCatalogColumn("proisagg"), boolCatalogColumn("proiswindow"),
		oidCatalogColumn("proowner"), textCatalogColumn("rolname"), textCatalogColumn("lanname"),
	)
	dataset.functionCatalog = &catalog
	for _, function := range catalog.functions {
		typeInfo, _ := kitdbsql.LookupKind(function.ReturnKind)
		names, types := make([]string, len(function.Parameters)), make([]string, len(function.Parameters))
		for i, parameter := range function.Parameters {
			names[i] = `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(parameter.Name) + `"`
			kind, _ := kitdbsql.LookupKind(parameter.Kind)
			types[i] = postgresCatalogLiteral(kind.Catalog.OID)
		}
		dataset.rows = append(dataset.rows, map[string]any{
			"oid": postgresCatalogOID("function", function.ID), "proname": function.Name,
			"pronamespace": postgresPublicNamespaceOID, "prorettype": typeInfo.Catalog.OID,
			"pronargs": int64(len(function.Parameters)), "prokind": "f", "provolatile": "i",
			"proretset": false, "prosecdef": false, "proisstrict": false, "nspname": "public",
			"prosrc": "RETURN " + functionExpressionSQL(function.Body), "proargnames": "{" + strings.Join(names, ",") + "}", "proargtypes": strings.Join(types, " "),
			"proisagg": false, "proiswindow": false, "proowner": postgresCatalogOID("role", catalog.user), "rolname": catalog.user, "lanname": "sql",
		})
	}
	return dataset
}
