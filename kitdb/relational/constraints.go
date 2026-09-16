package relational

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	kitdbengine "github.com/kitwork/engine/kitdb"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

type checkTruth uint8

const (
	checkUnknown checkTruth = iota
	checkFalse
	checkTrue
)

func validateRowChecks(schema kitdbsql.Schema, row map[string]any) error {
	for _, constraint := range schema.CheckConstraints {
		value, err := evaluateCheckExpression(schema, row, constraint.Expression)
		if err != nil {
			return fmt.Errorf("kitdb: table %q check %q: %w", schema.Name, constraint.Name, err)
		}
		truth, err := checkTruthOf(value)
		if err != nil {
			return fmt.Errorf("kitdb: table %q check %q: %w", schema.Name, constraint.Name, err)
		}
		// SQL CHECK rejects FALSE; TRUE and UNKNOWN both satisfy the row.
		if truth == checkFalse {
			return fmt.Errorf("kitdb: table %q check constraint %q failed", schema.Name, constraint.Name)
		}
	}
	return nil
}

func evaluateCheckExpression(
	schema kitdbsql.Schema,
	row map[string]any,
	expression kitdbsql.CheckExpression,
) (any, error) {
	switch expression.Kind {
	case "literal":
		decoder := json.NewDecoder(bytes.NewReader(expression.Literal))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			return nil, fmt.Errorf("invalid literal: %w", err)
		}
		return normalizeJSONNumber(value), nil
	case "field":
		field, found := fieldByTag(schema, expression.Field)
		if !found {
			return nil, fmt.Errorf("references missing field tag %d", expression.Field)
		}
		return readField(field, row[field.Name]), nil
	case "unary":
		if len(expression.Arguments) != 1 {
			return nil, fmt.Errorf("unary expression %q needs one argument", expression.Operator)
		}
		child, err := evaluateCheckExpression(schema, row, expression.Arguments[0])
		if err != nil {
			return nil, err
		}
		switch strings.ToLower(expression.Operator) {
		case "is null":
			return child == nil, nil
		case "is not null":
			return child != nil, nil
		case "not":
			truth, err := checkTruthOf(child)
			if err != nil || truth == checkUnknown {
				return nil, err
			}
			return truth == checkFalse, nil
		default:
			return nil, fmt.Errorf("unsupported unary operator %q", expression.Operator)
		}
	case "binary":
		if len(expression.Arguments) != 2 {
			return nil, fmt.Errorf("binary expression %q needs two arguments", expression.Operator)
		}
		left, err := evaluateCheckExpression(schema, row, expression.Arguments[0])
		if err != nil {
			return nil, err
		}
		right, err := evaluateCheckExpression(schema, row, expression.Arguments[1])
		if err != nil {
			return nil, err
		}
		operator := strings.ToLower(expression.Operator)
		if operator == "and" || operator == "or" {
			return evaluateCheckBoolean(operator, left, right)
		}
		if left == nil || right == nil {
			return nil, nil
		}
		comparison := compareCheckExpressionValues(schema, expression.Arguments[0], expression.Arguments[1], left, right)
		switch operator {
		case "=":
			return comparison == 0, nil
		case "!=", "<>":
			return comparison != 0, nil
		case "<":
			return comparison < 0, nil
		case "<=":
			return comparison <= 0, nil
		case ">":
			return comparison > 0, nil
		case ">=":
			return comparison >= 0, nil
		default:
			return nil, fmt.Errorf("unsupported binary operator %q", expression.Operator)
		}
	default:
		return nil, fmt.Errorf("unsupported expression kind %q", expression.Kind)
	}
}

func compareCheckExpressionValues(
	schema kitdbsql.Schema,
	leftPlan, rightPlan kitdbsql.CheckExpression,
	left, right any,
) int {
	for _, plan := range []kitdbsql.CheckExpression{leftPlan, rightPlan} {
		if plan.Kind != "field" {
			continue
		}
		if field, found := fieldByTag(schema, plan.Field); found {
			return compareFieldValues(field, left, right)
		}
	}
	return compareValues(left, right)
}

func evaluateCheckBoolean(operator string, left, right any) (any, error) {
	leftTruth, err := checkTruthOf(left)
	if err != nil {
		return nil, err
	}
	rightTruth, err := checkTruthOf(right)
	if err != nil {
		return nil, err
	}
	if operator == "and" {
		if leftTruth == checkFalse || rightTruth == checkFalse {
			return false, nil
		}
		if leftTruth == checkUnknown || rightTruth == checkUnknown {
			return nil, nil
		}
		return true, nil
	}
	if leftTruth == checkTrue || rightTruth == checkTrue {
		return true, nil
	}
	if leftTruth == checkUnknown || rightTruth == checkUnknown {
		return nil, nil
	}
	return false, nil
}

func checkTruthOf(value any) (checkTruth, error) {
	if value == nil {
		return checkUnknown, nil
	}
	truth, err := booleanValue(value)
	if err != nil {
		return checkUnknown, fmt.Errorf("expression result %T is not boolean", value)
	}
	if truth {
		return checkTrue, nil
	}
	return checkFalse, nil
}

func (transaction *Transaction) validateRowForeignKeys(schema kitdbsql.Schema, row map[string]any) error {
	for _, constraint := range relationalForeignConstraints(schema) {
		localFields, target, targetFields, err := transaction.bindForeignConstraint(schema, constraint)
		if err != nil {
			return err
		}
		values := make([]any, len(localFields))
		null := false
		for index, field := range localFields {
			values[index] = row[field.Name]
			if values[index] == nil {
				null = true
			}
		}
		if null {
			continue
		}
		for index := range values {
			values[index], err = foreignComparableValue(localFields[index], targetFields[index], values[index])
			if err != nil {
				return fmt.Errorf("kitdb: foreign key %q: %w", constraint.Name, err)
			}
		}
		exists, err := transaction.foreignTargetExists(target, targetFields, values)
		if err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("%w: table %q foreign key %q references a missing %s key", ErrForeignKeyViolation, schema.Name, constraint.Name, target.Name)
		}
	}
	return nil
}

func (transaction *Transaction) bindForeignConstraint(
	local kitdbsql.Schema,
	constraint kitdbsql.ForeignConstraint,
) ([]kitdbsql.Field, kitdbsql.Schema, []kitdbsql.Field, error) {
	localFields := fieldsByTags(local, constraint.Fields)
	if len(localFields) != len(constraint.Fields) {
		return nil, kitdbsql.Schema{}, nil, fmt.Errorf(
			"kitdb: table %q foreign key %q has missing local fields", local.Name, constraint.Name,
		)
	}
	target, err := schemaFromCatalogIdentity(transaction.catalog, constraint.TargetStructID, constraint.TargetStruct)
	if err != nil {
		return nil, kitdbsql.Schema{}, nil, fmt.Errorf(
			"kitdb: table %q foreign key %q: %w", local.Name, constraint.Name, err,
		)
	}
	targetFields := make([]kitdbsql.Field, len(constraint.TargetFields))
	for index, requested := range constraint.TargetFields {
		_, field, found := target.FieldByName(requested)
		if !found {
			return nil, kitdbsql.Schema{}, nil, fmt.Errorf(
				"kitdb: table %q foreign key %q target field %q is missing",
				local.Name, constraint.Name, requested,
			)
		}
		targetFields[index] = field
	}
	if len(targetFields) != len(localFields) || !schemaFieldsAreUnique(target, targetFields) {
		return nil, kitdbsql.Schema{}, nil, fmt.Errorf(
			"kitdb: table %q foreign key %q target is not uniquely addressable",
			local.Name, constraint.Name,
		)
	}
	for index := range localFields {
		if !foreignFieldsCompatible(localFields[index], targetFields[index]) {
			return nil, kitdbsql.Schema{}, nil, fmt.Errorf(
				"kitdb: table %q foreign key %q fields %q and %q have incompatible types",
				local.Name, constraint.Name, localFields[index].Name, targetFields[index].Name,
			)
		}
	}
	return localFields, target, targetFields, nil
}

func foreignFieldsCompatible(local, target kitdbsql.Field) bool {
	localType, localFound := kitdbsql.LookupKind(local.Kind)
	targetType, targetFound := kitdbsql.LookupKind(target.Kind)
	if !localFound || !targetFound || localType.Family != targetType.Family ||
		!exactUUIDFieldsCompatible(local, target) {
		return false
	}
	localExactChar := exactCharacterField(local)
	targetExactChar := exactCharacterField(target)
	return localExactChar == targetExactChar
}

func (transaction *Transaction) foreignTargetExists(
	target kitdbsql.Schema,
	fields []kitdbsql.Field,
	values []any,
) (bool, error) {
	if sameFieldTuple(target.PrimaryFields(), fields) {
		row := make(map[string]any, len(fields))
		for index, field := range fields {
			row[field.Name] = values[index]
		}
		generation, err := activeRowGeneration(transaction, target)
		if err != nil {
			return false, err
		}
		key, err := rowKey(target, row, generation)
		if err != nil {
			return false, err
		}
		_, found, err := transaction.Get(key)
		return found, err
	}
	identity := ""
	if len(fields) == 1 && fields[0].Unique {
		identity = fields[0].ID
	} else {
		for _, constraint := range target.UniqueConstraints {
			if sameFieldTuple(fieldsByTags(target, constraint.Fields), fields) {
				identity = constraint.ID
				break
			}
		}
	}
	if identity == "" {
		return false, fmt.Errorf("kitdb: foreign key target on %q is not unique", target.Name)
	}
	key, applicable, err := uniqueKey(target, identity, values)
	if err != nil || !applicable {
		return false, err
	}
	_, found, err := transaction.Get(key)
	return found, err
}

func relationalForeignConstraints(schema kitdbsql.Schema) []kitdbsql.ForeignConstraint {
	result := append([]kitdbsql.ForeignConstraint(nil), schema.ForeignConstraints...)
	for _, field := range schema.Fields {
		if field.Reference == nil {
			continue
		}
		result = append(result, kitdbsql.ForeignConstraint{
			Version:      1,
			ID:           kitdbsql.StableSchemaID("foreign", schema.ID+":"+field.ID),
			Name:         "fk_" + schema.Name + "_" + field.Name,
			Fields:       []uint32{field.Tag},
			TargetStruct: field.Reference.Struct,
			TargetFields: []string{field.Reference.Field},
			OnDelete:     normalizeReferentialAction(field.Reference.OnDelete),
			OnUpdate:     normalizeReferentialAction(field.Reference.OnUpdate),
		})
	}
	return result
}

func normalizeReferentialAction(action string) string {
	action = strings.ToLower(strings.TrimSpace(action))
	if action == "" {
		return "no action"
	}
	return action
}

func schemaFromCatalogIdentity(
	catalog kitdbengine.CatalogSnapshot,
	identity, requested string,
) (kitdbsql.Schema, error) {
	if identity != "" {
		for _, entry := range catalog.Structs {
			if entry.ID == identity {
				return decodeCatalogSchema(entry.Definition)
			}
		}
	}
	return schemaFromCatalog(catalog, requested)
}
