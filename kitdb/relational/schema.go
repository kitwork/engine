package relational

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	kitdbengine "github.com/kitwork/engine/kitdb"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

func schemaFromCreate(
	plan *kitdbsql.CreateTableStatement,
	catalog kitdbengine.CatalogSnapshot,
) (kitdbsql.Schema, []byte, error) {
	if plan == nil || plan.Name == "" || len(plan.Columns) == 0 {
		return kitdbsql.Schema{}, nil, fmt.Errorf("kitdb: invalid CREATE TABLE plan")
	}
	resolved, domains, err := resolveCreateDomains(plan, catalog)
	if err != nil {
		return kitdbsql.Schema{}, nil, err
	}
	plan = &resolved
	schema := kitdbsql.Schema{
		Version: kitdbsql.SchemaVersion2,
		ID:      kitdbsql.StableSchemaID("struct", plan.Name),
		Name:    plan.Name,
	}
	primaryOrder := make(map[string]int, len(plan.PrimaryKey))
	for index, requested := range plan.PrimaryKey {
		column, found := createColumn(plan.Columns, requested)
		if !found {
			return kitdbsql.Schema{}, nil, fmt.Errorf("kitdb: PRIMARY KEY field %q disappeared", requested)
		}
		primaryOrder[strings.ToLower(column.Name)] = index + 1
	}
	for position, column := range plan.Columns {
		field := kitdbsql.Field{
			Domain:        domains[column.Name],
			ID:            kitdbsql.StableSchemaID("field", schema.ID+":"+column.Name),
			Tag:           uint32(position + 1),
			Name:          column.Name,
			Position:      position,
			Kind:          column.Type.Kind,
			Precision:     column.Precision,
			Scale:         column.Scale,
			TimePrecision: column.TimePrecision,
			TextLength:    column.TextLength,
			ExactUUID:     column.Type.ID == kitdbsql.TypeUUID,
			NotNull:       column.NotNull,
			Unique:        column.Unique,
			Searchable:    column.Searchable,
			SearchWeight:  column.SearchWeight,
			Analytics:     column.Analytics,
			Enum:          append([]string(nil), column.Choices...),
			Default:       json.RawMessage("null"),
		}
		if field.Domain != nil {
			schema.Version = kitdbsql.SchemaVersion9
		}
		if field.Precision != 0 && schema.Version < kitdbsql.SchemaVersion4 {
			schema.Version = kitdbsql.SchemaVersion4
		}
		if exactTemporalTypeID(column.Type.ID) && schema.Version < kitdbsql.SchemaVersion5 {
			schema.Version = kitdbsql.SchemaVersion5
		}
		if field.TextLength != nil && schema.Version < kitdbsql.SchemaVersion6 {
			schema.Version = kitdbsql.SchemaVersion6
		}
		if field.ExactUUID && schema.Version < kitdbsql.SchemaVersion7 {
			schema.Version = kitdbsql.SchemaVersion7
		}
		if order := primaryOrder[strings.ToLower(column.Name)]; order != 0 {
			field.Primary = true
			field.PrimaryOrder = order
			field.NotNull = true
			if len(plan.PrimaryKey) == 1 {
				field.Unique = true
			}
		}
		if column.SequenceMode != "" {
			var found bool
			for _, sequence := range catalog.Sequences {
				if sequence.Name == column.SequenceName {
					expected, valid := kitdbsql.SequenceDataTypeForKind(column.Type.Kind)
					if !valid || sequence.DataTypeName() != expected {
						return kitdbsql.Schema{}, nil, fmt.Errorf(
							"kitdb: field %q type %s does not match sequence %q type %s",
							column.Name, column.Type.Catalog.DataType, sequence.Name, sequence.DataTypeName(),
						)
					}
					field.Sequence = &kitdbsql.SequenceDefault{ID: sequence.ID, Name: sequence.Name, Mode: column.SequenceMode}
					found = true
					break
				}
			}
			if !found {
				return kitdbsql.Schema{}, nil, fmt.Errorf("kitdb: sequence %q is not declared", column.SequenceName)
			}
			if schema.Version < kitdbsql.SchemaVersion3 {
				schema.Version = kitdbsql.SchemaVersion3
			}
		}
		if column.HasDefault {
			if literalUsesClock(column.Default.Kind) {
				valid := false
				switch column.Default.Kind {
				case kitdbsql.LiteralCurrentDate:
					valid = column.Type.ID == kitdbsql.TypeDate
				case kitdbsql.LiteralCurrentTime:
					valid = column.Type.ID == kitdbsql.TypeTime
				case kitdbsql.LiteralLocalTimestamp:
					valid = column.Type.ID == kitdbsql.TypeTimestamp
				case kitdbsql.LiteralCurrentTimestamp:
					valid = column.Type.ID == kitdbsql.TypeDatetime || column.Type.ID == kitdbsql.TypeTimestamp ||
						column.Type.ID == kitdbsql.TypeTimestampTZ
				}
				if !valid {
					return kitdbsql.Schema{}, nil, fmt.Errorf(
						"kitdb: clock default is incompatible with %s", strings.ToUpper(column.Type.Kind),
					)
				}
				field.DefaultNow = true
			} else {
				item, err := resolveLiteral(column.Default, nil)
				if err != nil {
					return kitdbsql.Schema{}, nil, err
				}
				item, err = coerceField(field, item)
				if err != nil {
					return kitdbsql.Schema{}, nil, fmt.Errorf("kitdb: default for field %q: %w", field.Name, err)
				}
				encoded, err := json.Marshal(item)
				if err != nil {
					return kitdbsql.Schema{}, nil, fmt.Errorf("kitdb: encode default for field %q: %w", field.Name, err)
				}
				field.HasDefault = true
				field.Default = encoded
			}
		}
		schema.Fields = append(schema.Fields, field)
	}
	schema.NextFieldTag = uint32(len(schema.Fields) + 1)
	if plan.Partition != nil {
		_, field, found := schema.FieldByName(plan.Partition.Field)
		if !found {
			return kitdbsql.Schema{}, nil, fmt.Errorf("kitdb: partition field %q disappeared", plan.Partition.Field)
		}
		if schema.Version < kitdbsql.SchemaVersion8 {
			schema.Version = kitdbsql.SchemaVersion8
		}
		schema.Partition = &kitdbsql.Partition{
			Version: kitdbsql.PartitionVersion1, Field: field.Tag, Strategy: plan.Partition.Strategy,
		}
		if plan.Partition.Strategy == "hash" {
			schema.Partition.Buckets = kitdbsql.PartitionHashBuckets
		}
	}
	for _, requested := range plan.Unique {
		fields := make([]uint32, len(requested))
		names := make([]string, len(requested))
		for index, name := range requested {
			canonical, field, found := schema.FieldByName(name)
			if !found {
				return kitdbsql.Schema{}, nil, fmt.Errorf("kitdb: UNIQUE field %q disappeared", name)
			}
			fields[index], names[index] = field.Tag, canonical
		}
		if len(fields) == 1 {
			for index := range schema.Fields {
				if schema.Fields[index].Tag == fields[0] {
					schema.Fields[index].Unique = true
				}
			}
			continue
		}
		name := "unique_" + schema.Name + "_" + strings.Join(names, "_")
		schema.UniqueConstraints = append(schema.UniqueConstraints, kitdbsql.UniqueConstraint{
			Version: 1,
			ID:      kitdbsql.StableSchemaID("unique", schema.ID+":"+strings.ToLower(name)),
			Name:    name,
			Fields:  fields,
		})
	}
	sort.Slice(schema.UniqueConstraints, func(left, right int) bool {
		return strings.ToLower(schema.UniqueConstraints[left].Name) < strings.ToLower(schema.UniqueConstraints[right].Name)
	})
	if err := appendCreateChecks(&schema, plan.Checks); err != nil {
		return kitdbsql.Schema{}, nil, err
	}
	if err := appendCreateForeignKeys(&schema, plan.ForeignKeys, catalog); err != nil {
		return kitdbsql.Schema{}, nil, err
	}
	encoded, err := kitdbsql.EncodeSchema(schema)
	if err != nil {
		return kitdbsql.Schema{}, nil, err
	}
	decoded, err := kitdbsql.DecodeSchema(encoded)
	if err != nil {
		return kitdbsql.Schema{}, nil, err
	}
	return decoded, encoded, nil
}

func appendCreateChecks(schema *kitdbsql.Schema, checks []kitdbsql.CheckDefinition) error {
	seen := make(map[string]string, len(checks))
	for index, check := range checks {
		expression, err := createCheckExpression(*schema, check.Expression)
		if err != nil {
			return fmt.Errorf("kitdb SQL: CHECK: %w", err)
		}
		name := check.Name
		if name == "" {
			if check.Column != "" {
				canonical, _, found := schema.FieldByName(check.Column)
				if !found {
					return fmt.Errorf("kitdb SQL: CHECK references missing field %q", check.Column)
				}
				name = fmt.Sprintf("check_%s_%s_%d", schema.Name, canonical, index+1)
			} else {
				name = fmt.Sprintf("check_%s_%d", schema.Name, index+1)
			}
		}
		key := strings.ToLower(name)
		if previous := seen[key]; previous != "" {
			return fmt.Errorf("kitdb SQL: CHECK constraints %q and %q share a name", previous, name)
		}
		seen[key] = name
		schema.CheckConstraints = append(schema.CheckConstraints, kitdbsql.CheckConstraint{
			Version:    1,
			ID:         kitdbsql.StableSchemaID("check", schema.ID+":"+key),
			Name:       name,
			Expression: expression,
		})
	}
	sort.Slice(schema.CheckConstraints, func(left, right int) bool {
		return strings.ToLower(schema.CheckConstraints[left].Name) < strings.ToLower(schema.CheckConstraints[right].Name)
	})
	return nil
}

func createCheckExpression(schema kitdbsql.Schema, plan kitdbsql.CheckPlan) (kitdbsql.CheckExpression, error) {
	result := kitdbsql.CheckExpression{Kind: plan.Kind, Operator: strings.ToLower(plan.Operator)}
	switch plan.Kind {
	case "literal":
		if literalUsesClock(plan.Literal.Kind) || plan.Literal.Kind == kitdbsql.LiteralParameter {
			return kitdbsql.CheckExpression{}, fmt.Errorf("CHECK literals must be immutable")
		}
		value, err := resolveLiteral(plan.Literal, nil)
		if err != nil {
			return kitdbsql.CheckExpression{}, err
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return kitdbsql.CheckExpression{}, err
		}
		result.Literal = encoded
	case "field":
		_, field, found := schema.FieldByName(plan.Field)
		if !found {
			return kitdbsql.CheckExpression{}, fmt.Errorf("references missing field %q", plan.Field)
		}
		result.Field = field.Tag
	case "unary":
		if len(plan.Arguments) != 1 || (result.Operator != "not" && result.Operator != "is null" && result.Operator != "is not null") {
			return kitdbsql.CheckExpression{}, fmt.Errorf("unsupported unary expression %q", plan.Operator)
		}
	case "binary":
		if len(plan.Arguments) != 2 ||
			(result.Operator != "and" && result.Operator != "or" && !supportedCheckComparison(result.Operator)) {
			return kitdbsql.CheckExpression{}, fmt.Errorf("unsupported binary expression %q", plan.Operator)
		}
	default:
		return kitdbsql.CheckExpression{}, fmt.Errorf("unsupported expression kind %q", plan.Kind)
	}
	result.Arguments = make([]kitdbsql.CheckExpression, len(plan.Arguments))
	for index, argument := range plan.Arguments {
		converted, err := createCheckExpression(schema, argument)
		if err != nil {
			return kitdbsql.CheckExpression{}, err
		}
		result.Arguments[index] = converted
	}
	return result, nil
}

func supportedCheckComparison(operator string) bool {
	switch operator {
	case "=", "!=", "<>", "<", "<=", ">", ">=":
		return true
	default:
		return false
	}
}

func appendCreateForeignKeys(
	schema *kitdbsql.Schema,
	foreignKeys []kitdbsql.ForeignKeyDefinition,
	catalog kitdbengine.CatalogSnapshot,
) error {
	seen := make(map[string]string, len(foreignKeys))
	for _, definition := range foreignKeys {
		localFields := make([]kitdbsql.Field, len(definition.Columns))
		localNames := make([]string, len(definition.Columns))
		localTags := make([]uint32, len(definition.Columns))
		for index, requested := range definition.Columns {
			canonical, field, found := schema.FieldByName(requested)
			if !found {
				return fmt.Errorf("kitdb SQL: FOREIGN KEY references missing local field %q", requested)
			}
			localFields[index], localNames[index], localTags[index] = field, canonical, field.Tag
		}
		target, err := createForeignTarget(*schema, catalog, definition.TargetTable)
		if err != nil {
			return err
		}
		targetFields := make([]kitdbsql.Field, len(definition.TargetColumns))
		targetNames := make([]string, len(definition.TargetColumns))
		for index, requested := range definition.TargetColumns {
			canonical, field, found := target.FieldByName(requested)
			if !found {
				return fmt.Errorf(
					"kitdb SQL: FOREIGN KEY target %q has no field %q", target.Name, requested,
				)
			}
			if !foreignFieldsCompatible(localFields[index], field) {
				return fmt.Errorf(
					"kitdb SQL: FOREIGN KEY fields %s.%s and %s.%s have incompatible types",
					schema.Name, localFields[index].Name, target.Name, field.Name,
				)
			}
			targetFields[index], targetNames[index] = field, canonical
		}
		if !schemaFieldsAreUnique(target, targetFields) {
			return fmt.Errorf(
				"kitdb SQL: FOREIGN KEY target (%s) is not a primary or unique key on %q",
				strings.Join(targetNames, ", "), target.Name,
			)
		}
		name := definition.Name
		if name == "" {
			name = "fk_" + schema.Name + "_" + strings.Join(localNames, "_")
		}
		key := strings.ToLower(name)
		if previous := seen[key]; previous != "" {
			return fmt.Errorf("kitdb SQL: FOREIGN KEY constraints %q and %q share a name", previous, name)
		}
		seen[key] = name
		schema.ForeignConstraints = append(schema.ForeignConstraints, kitdbsql.ForeignConstraint{
			Version:        1,
			ID:             kitdbsql.StableSchemaID("foreign", schema.ID+":"+key),
			Name:           name,
			Fields:         localTags,
			TargetStruct:   target.Name,
			TargetStructID: target.ID,
			TargetFields:   targetNames,
			OnDelete:       definition.OnDelete,
			OnUpdate:       definition.OnUpdate,
		})
	}
	sort.Slice(schema.ForeignConstraints, func(left, right int) bool {
		return strings.ToLower(schema.ForeignConstraints[left].Name) < strings.ToLower(schema.ForeignConstraints[right].Name)
	})
	return nil
}

func createForeignTarget(
	self kitdbsql.Schema,
	catalog kitdbengine.CatalogSnapshot,
	requested string,
) (kitdbsql.Schema, error) {
	if strings.EqualFold(self.Name, requested) {
		return self, nil
	}
	target, err := schemaFromCatalog(catalog, requested)
	if err != nil {
		return kitdbsql.Schema{}, fmt.Errorf("kitdb SQL: FOREIGN KEY: %w", err)
	}
	return target, nil
}

func schemaFieldsAreUnique(schema kitdbsql.Schema, fields []kitdbsql.Field) bool {
	primary := schema.PrimaryFields()
	if sameFieldTuple(primary, fields) {
		return true
	}
	if len(fields) == 1 && fields[0].Unique {
		return true
	}
	for _, constraint := range schema.UniqueConstraints {
		if sameFieldTuple(fieldsByTags(schema, constraint.Fields), fields) {
			return true
		}
	}
	return false
}

func sameFieldTuple(left, right []kitdbsql.Field) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].ID != right[index].ID {
			return false
		}
	}
	return true
}

func createColumn(columns []kitdbsql.ColumnDefinition, requested string) (kitdbsql.ColumnDefinition, bool) {
	for _, column := range columns {
		if column.Name == requested {
			return column, true
		}
	}
	for _, column := range columns {
		if strings.EqualFold(column.Name, requested) {
			return column, true
		}
	}
	return kitdbsql.ColumnDefinition{}, false
}

func decodeCatalogSchema(encoded []byte) (kitdbsql.Schema, error) {
	schema, err := kitdbsql.DecodeSchema(encoded)
	if err != nil {
		return kitdbsql.Schema{}, fmt.Errorf("kitdb: catalog schema: %w", err)
	}
	if err := schema.VerifyHash(); err != nil {
		return kitdbsql.Schema{}, fmt.Errorf("kitdb: catalog schema: %w", err)
	}
	return schema, nil
}

func fieldByTag(schema kitdbsql.Schema, tag uint32) (kitdbsql.Field, bool) {
	for _, field := range schema.Fields {
		if field.Tag == tag {
			return field, true
		}
	}
	return kitdbsql.Field{}, false
}
