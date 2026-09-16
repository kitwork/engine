package relational

import (
	"strings"

	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

func triggerCatalogSchema(catalog postgresCatalogSnapshot, id string) kitdbsql.Schema {
	for _, relation := range catalog.relations {
		if relation.schema.ID == id {
			return relation.schema
		}
	}
	return kitdbsql.Schema{}
}

func triggerExpressionSQL(plan kitdbsql.ExpressionPlan, source kitdbsql.Schema) string {
	switch plan.Kind {
	case "field":
		old, tag, err := triggerVariable(plan.Field)
		if err != nil {
			return "<invalid>"
		}
		field, found := fieldByTag(source, tag)
		if !found {
			return "<missing field>"
		}
		prefix := "NEW."
		if old {
			prefix = "OLD."
		}
		return prefix + quotePostgresIdentifier(field.Name)
	case "literal":
		if plan.Literal.Kind == kitdbsql.LiteralDefault {
			return "DEFAULT"
		}
		value, err := resolveLiteral(plan.Literal, nil)
		if err != nil {
			return "<invalid literal>"
		}
		return postgresCatalogLiteral(value)
	}
	arguments := make([]string, len(plan.Arguments))
	for i, child := range plan.Arguments {
		arguments[i] = triggerExpressionSQL(child, source)
	}
	switch plan.Kind {
	case "binary":
		if len(arguments) == 2 {
			return "(" + arguments[0] + " " + strings.ToUpper(plan.Operator) + " " + arguments[1] + ")"
		}
	case "unary":
		if len(arguments) == 1 {
			if plan.Operator == "is null" || plan.Operator == "is not null" {
				return "(" + arguments[0] + " " + strings.ToUpper(plan.Operator) + ")"
			}
			return "(" + strings.ToUpper(plan.Operator) + " " + arguments[0] + ")"
		}
	case "function":
		return plan.Operator + "(" + strings.Join(arguments, ", ") + ")"
	}
	return "<unsupported expression>"
}

func triggerActionSQL(trigger *storedTrigger, catalog postgresCatalogSnapshot) string {
	source := triggerCatalogSchema(catalog, trigger.SourceStruct)
	target := triggerCatalogSchema(catalog, trigger.TargetStruct)
	columns, values := make([]string, len(trigger.TargetFields)), make([]string, len(trigger.Values))
	for i, tag := range trigger.TargetFields {
		field, _ := fieldByTag(target, tag)
		columns[i] = quotePostgresIdentifier(field.Name)
	}
	for i, value := range trigger.Values {
		values[i] = triggerExpressionSQL(value, source)
	}
	return "INSERT INTO " + quotePostgresIdentifier(target.Name) + " (" + strings.Join(columns, ", ") + ") VALUES (" + strings.Join(values, ", ") + ")"
}

func informationSchemaTriggers(catalog postgresCatalogSnapshot) postgresCatalogDataset {
	dataset := newPostgresCatalogDataset(
		[]string{"trigger_schema", "trigger_name", "event_manipulation", "event_object_table", "action_timing", "action_statement"},
		textCatalogColumn("trigger_catalog"), textCatalogColumn("trigger_schema"), textCatalogColumn("trigger_name"),
		textCatalogColumn("event_manipulation"), textCatalogColumn("event_object_catalog"), textCatalogColumn("event_object_schema"), textCatalogColumn("event_object_table"),
		textCatalogColumn("action_timing"), textCatalogColumn("action_orientation"), textCatalogColumn("action_condition"), textCatalogColumn("action_statement"), int8CatalogColumn("action_order"),
	)
	orders := make(map[string]int64)
	for _, trigger := range catalog.triggers {
		source := triggerCatalogSchema(catalog, trigger.SourceStruct)
		key := source.ID + ":" + trigger.Event
		orders[key]++
		row := map[string]any{
			"trigger_catalog": catalog.database, "trigger_schema": "public", "trigger_name": trigger.Name,
			"event_manipulation": strings.ToUpper(trigger.Event), "event_object_catalog": catalog.database, "event_object_schema": "public", "event_object_table": source.Name,
			"action_timing": "AFTER", "action_orientation": "ROW", "action_statement": triggerActionSQL(trigger, catalog), "action_order": orders[key],
		}
		if trigger.When != nil {
			row["action_condition"] = triggerExpressionSQL(*trigger.When, source)
		}
		dataset.rows = append(dataset.rows, row)
	}
	return dataset
}

func postgresTriggers(catalog postgresCatalogSnapshot) postgresCatalogDataset {
	dataset := newPostgresCatalogDataset(
		[]string{"oid", "tgname", "tgrelid", "tgtype", "tgenabled", "tgisinternal"},
		oidCatalogColumn("oid"), textCatalogColumn("tgname"), oidCatalogColumn("tgrelid"), int8CatalogColumn("tgtype"),
		textCatalogColumn("tgenabled"), boolCatalogColumn("tgisinternal"), oidCatalogColumn("tgfoid"), textCatalogColumn("relname"), textCatalogColumn("nspname"),
	)
	dataset.functionCatalog = &catalog
	for _, trigger := range catalog.triggers {
		source := triggerCatalogSchema(catalog, trigger.SourceStruct)
		event := int64(4)
		if trigger.Event == "update" {
			event = 16
		} else if trigger.Event == "delete" {
			event = 8
		}
		dataset.rows = append(dataset.rows, map[string]any{
			"oid": postgresCatalogOID("trigger", trigger.ID), "tgname": trigger.Name, "tgrelid": postgresCatalogOID("table", source.ID),
			"tgtype": event | 1, "tgenabled": "O", "tgisinternal": false, "tgfoid": uint32(0), "relname": source.Name, "nspname": "public",
		})
	}
	return dataset
}
