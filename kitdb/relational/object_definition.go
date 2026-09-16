package relational

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	kitdbengine "github.com/kitwork/engine/kitdb"
	"github.com/kitwork/engine/kitdb/pgwire"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

// Definitions describe schema, not a logical backup: sequence counters and
// ownership are restored through the normal verified backup path.
func catalogObjectDefinition(helper string, oid uint32, catalog postgresCatalogSnapshot) (any, bool, error) {
	var definition string
	found := false
	accept := func(value string) error {
		if found {
			return pgwire.NewError("42725", "ambiguous KitDB catalog OID")
		}
		found, definition = true, value
		return nil
	}
	switch helper {
	case "pg_get_triggerdef":
		for _, trigger := range catalog.triggers {
			if postgresCatalogOID("trigger", trigger.ID) != oid {
				continue
			}
			if err := accept(triggerDefinitionSQL(trigger, catalog)); err != nil {
				return nil, true, err
			}
		}
	case "kitdb_get_domaindef":
		for _, domain := range catalog.domains {
			if postgresCatalogOID("domain", domain.ID) != oid {
				continue
			}
			if err := accept(domainDefinitionSQL(domain)); err != nil {
				return nil, true, err
			}
		}
	case "kitdb_get_sequencedef":
		for _, sequence := range catalog.sequences {
			if postgresCatalogOID("sequence", sequence.ID) != oid {
				continue
			}
			if sequence.OwnerStruct != "" {
				return nil, true, pgwire.NewError("0A000", "owned sequence is recreated by its column's SERIAL/IDENTITY declaration, not standalone CREATE SEQUENCE")
			}
			if err := accept(sequenceDefinitionSQL(sequence)); err != nil {
				return nil, true, err
			}
		}
	case "pg_get_constraintdef":
		for _, relation := range catalog.relations {
			for _, constraint := range catalogConstraintsFor(catalog, relation) {
				if constraint.constraint != oid {
					continue
				}
				value, err := tableConstraintDefinitionSQL(constraint, relation.schema, catalog)
				if err != nil {
					return nil, true, err
				}
				if err := accept(value); err != nil {
					return nil, true, err
				}
			}
		}
		for _, domain := range catalog.domains {
			for i, check := range domain.Column.Checks {
				if domainCheckOID(domain, i) == oid {
					if err := accept("CHECK (" + domainExpressionSQL(check.Expression) + ")"); err != nil {
						return nil, true, err
					}
				}
			}
		}
	default:
		return nil, false, nil
	}
	if !found {
		return nil, true, nil
	}
	return definition, true, nil
}

func triggerDefinitionSQL(trigger *storedTrigger, catalog postgresCatalogSnapshot) string {
	source := triggerCatalogSchema(catalog, trigger.SourceStruct)
	definition := "CREATE TRIGGER " + quotePostgresIdentifier(trigger.Name) + " AFTER " + strings.ToUpper(trigger.Event) +
		" ON " + quotePostgresIdentifier(source.Name) + " FOR EACH ROW"
	if trigger.When != nil {
		definition += " WHEN (" + triggerExpressionSQL(*trigger.When, source) + ")"
	}
	return definition + " " + triggerActionSQL(trigger, catalog) + ";"
}

func domainDefinitionSQL(domain *storedDomain) string {
	column := domain.Column
	kind, _ := kitdbsql.LookupKind(column.Kind)
	dataType := kind.Catalog.DataType
	if column.Precision != 0 {
		dataType += fmt.Sprintf("(%d,%d)", column.Precision, column.Scale)
	}
	if column.TextLength != nil {
		dataType += fmt.Sprintf("(%d)", *column.TextLength)
	}
	definition := "CREATE DOMAIN " + quotePostgresIdentifier(domain.Name) + " AS " + dataType
	if column.HasDefault {
		value, _ := resolveLiteral(column.Default, nil)
		definition += " DEFAULT " + postgresCatalogLiteral(value)
	}
	if column.NotNull {
		definition += " NOT NULL"
	}
	for _, check := range column.Checks {
		if check.Name != "" {
			definition += " CONSTRAINT " + quotePostgresIdentifier(check.Name)
		}
		definition += " CHECK (" + domainExpressionSQL(check.Expression) + ")"
	}
	return definition + ";"
}

func domainExpressionSQL(plan kitdbsql.ExpressionPlan) string {
	return scalarExpressionSQL(plan, func(string) string { return "VALUE" })
}

func sequenceDefinitionSQL(sequence kitdbengine.Sequence) string {
	cycle := "NO CYCLE"
	if sequence.Cycle {
		cycle = "CYCLE"
	}
	return fmt.Sprintf("CREATE SEQUENCE %s AS %s INCREMENT BY %d MINVALUE %d MAXVALUE %d START WITH %d CACHE %d %s;",
		quotePostgresIdentifier(sequence.Name), sequence.DataTypeName(), sequence.Increment, sequence.Minimum,
		sequence.Maximum, sequence.Start, sequence.CacheSize(), cycle)
}

func domainCheckName(domain *storedDomain, index int) string {
	if name := domain.Column.Checks[index].Name; name != "" {
		return name
	}
	return fmt.Sprintf("check_domain_%s_%d", domain.Name, index+1)
}

func domainCheckOID(domain *storedDomain, index int) uint32 {
	return postgresCatalogOID("domain-check", fmt.Sprintf("%s:%d", domain.ID, index))
}

func catalogFieldList(fields []kitdbsql.Field) string {
	names := make([]string, len(fields))
	for i, field := range fields {
		names[i] = quotePostgresIdentifier(field.Name)
	}
	return "(" + strings.Join(names, ", ") + ")"
}

func tableConstraintDefinitionSQL(constraint postgresCatalogConstraint, schema kitdbsql.Schema, catalog postgresCatalogSnapshot) (string, error) {
	switch constraint.kind {
	case "p":
		return "PRIMARY KEY " + catalogFieldList(constraint.fields), nil
	case "u":
		return "UNIQUE " + catalogFieldList(constraint.fields), nil
	case "c":
		for _, check := range schema.CheckConstraints {
			if check.Name == constraint.name {
				expression, err := catalogCheckExpressionSQL(check.Expression, schema)
				return "CHECK (" + expression + ")", err
			}
		}
	case "f":
		for _, foreign := range schema.ForeignConstraints {
			if foreign.Name != constraint.name {
				continue
			}
			for _, relation := range catalog.relations {
				if postgresCatalogOID("table", relation.schema.ID) != constraint.targetOID {
					continue
				}
				targetFields := make([]kitdbsql.Field, len(foreign.TargetFields))
				for i, name := range foreign.TargetFields {
					_, field, found := relation.schema.FieldByName(name)
					if !found {
						return "", fmt.Errorf("catalog foreign key references missing field")
					}
					targetFields[i] = field
				}
				definition := "FOREIGN KEY " + catalogFieldList(constraint.fields) + " REFERENCES " + quotePostgresIdentifier(relation.schema.Name) +
					" " + catalogFieldList(targetFields)
				if foreign.OnDelete != "" {
					definition += " ON DELETE " + strings.ToUpper(foreign.OnDelete)
				}
				if foreign.OnUpdate != "" {
					definition += " ON UPDATE " + strings.ToUpper(foreign.OnUpdate)
				}
				return definition, nil
			}
		}
	}
	return "", pgwire.NewError("0A000", "constraint definition cannot be reconstructed from this catalog")
}

func catalogCheckExpressionSQL(expression kitdbsql.CheckExpression, schema kitdbsql.Schema) (string, error) {
	switch expression.Kind {
	case "field":
		field, found := fieldByTag(schema, expression.Field)
		if !found {
			return "", fmt.Errorf("catalog CHECK references missing field")
		}
		return quotePostgresIdentifier(field.Name), nil
	case "literal":
		decoder := json.NewDecoder(bytes.NewReader(expression.Literal))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			return "", err
		}
		return postgresCatalogLiteral(value), nil
	}
	args := make([]string, len(expression.Arguments))
	for i, child := range expression.Arguments {
		var err error
		args[i], err = catalogCheckExpressionSQL(child, schema)
		if err != nil {
			return "", err
		}
	}
	if expression.Kind == "binary" && len(args) == 2 {
		return "(" + args[0] + " " + strings.ToUpper(expression.Operator) + " " + args[1] + ")", nil
	}
	if expression.Kind == "unary" && len(args) == 1 {
		if expression.Operator == "is null" || expression.Operator == "is not null" {
			return "(" + args[0] + " " + strings.ToUpper(expression.Operator) + ")", nil
		}
		return "(" + strings.ToUpper(expression.Operator) + " " + args[0] + ")", nil
	}
	return "", pgwire.NewError("0A000", "unsupported catalog CHECK expression")
}
