package relational

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

type mutationRecord struct {
	key     []byte
	decoded decodedRow
}

type preparedUpdate struct {
	record  mutationRecord
	row     map[string]any
	encoded []byte
	unique  []indexEntry
	indexes []indexEntry
}

type boundAssignment struct {
	defaultValue bool
	field        kitdbsql.Field
	expression   *boundPredicate
}

func (transaction *Transaction) executeUpdate(
	ctx context.Context,
	plan *kitdbsql.UpdateStatement,
	parameters []any,
) (Result, error) {
	if err := transaction.ready(); err != nil {
		return Result{}, err
	}
	if plan == nil || len(plan.Assignments) == 0 {
		return Result{}, fmt.Errorf("kitdb SQL: invalid UPDATE plan")
	}
	schema, err := transaction.schema(plan.Table)
	if err != nil {
		return Result{}, err
	}
	if err := standaloneWriteSupported(schema); err != nil {
		return Result{}, err
	}
	generation, err := activeRowGeneration(transaction, schema)
	if err != nil {
		return Result{}, err
	}
	if generation != 0 {
		return Result{}, fmt.Errorf(
			"kitdb: standalone writes to migrated row generation %d are not enabled yet", generation,
		)
	}
	conditions, err := bindConditions(schema, plan.Conditions, parameters)
	if err != nil {
		return Result{}, err
	}
	predicate, err := bindPredicate(schema, plan.Predicate, parameters)
	if err != nil {
		return Result{}, err
	}
	assignments, err := bindAssignments(schema, plan.Assignments, parameters)
	if err != nil {
		return Result{}, err
	}
	records, err := transaction.findMutationRecords(ctx, schema, generation, conditions, predicate)
	if err != nil {
		return Result{}, err
	}
	if len(plan.Returning) != 0 && len(records) > transaction.engine.maximumResultRows {
		return Result{}, fmt.Errorf(
			"kitdb SQL: RETURNING exceeds this server's result limit of %d", transaction.engine.maximumResultRows,
		)
	}
	if len(records) == 0 {
		return Result{CommandTag: "UPDATE 0"}, nil
	}

	now := time.Now().UTC()
	prepared := make([]preparedUpdate, 0, len(records))
	oldUnique := make(map[string][]byte)
	oldIndexes := make(map[string][]byte)
	newUnique := make(map[string]int)
	for index, record := range records {
		if index&255 == 0 {
			if err := ctx.Err(); err != nil {
				return Result{}, err
			}
		}
		oldEntries, err := uniqueEntries(schema, record.decoded.values, record.key)
		if err != nil {
			return Result{}, err
		}
		for _, entry := range oldEntries {
			oldUnique[string(entry.key)] = entry.key
		}
		oldSecondary, err := secondaryIndexEntries(schema, record.decoded.values, record.key)
		if err != nil {
			return Result{}, err
		}
		for _, entry := range oldSecondary {
			oldIndexes[string(entry.key)] = entry.key
		}
		row := cloneRow(record.decoded.values)
		for _, assignment := range assignments {
			var item any
			var err error
			if assignment.defaultValue {
				item, err = transaction.fieldDefault(ctx, assignment.field, now)
			} else {
				item, err = evaluateBoundPredicate(record.decoded.values, assignment.expression)
			}
			if err != nil {
				return Result{}, fmt.Errorf("kitdb SQL: UPDATE SET %s: %w", assignment.field.Name, err)
			}
			item, err = coerceField(assignment.field, item)
			if err != nil {
				return Result{}, err
			}
			if item == nil && assignment.field.NotNull {
				return Result{}, fmt.Errorf(
					"kitdb: struct %q field %q cannot be null", schema.Name, assignment.field.Name,
				)
			}
			row[assignment.field.Name] = item
		}
		for _, field := range schema.Fields {
			if !field.Updated {
				continue
			}
			typeInfo, _ := kitdbsql.LookupKind(field.Kind)
			if typeInfo.Family != kitdbsql.FamilyTemporal {
				return Result{}, fmt.Errorf(
					"kitdb: standalone UPDATE does not yet implement on-update values for field %q", field.Name,
				)
			}
			item, err := coerceField(field, now)
			if err != nil {
				return Result{}, err
			}
			row[field.Name] = item
		}
		if err := validateRequiredFields(schema, row); err != nil {
			return Result{}, err
		}
		if err := validateRowChecks(schema, row); err != nil {
			return Result{}, err
		}
		encoded, err := encodeRow(schema, row, record.decoded.unknown)
		if err != nil {
			return Result{}, err
		}
		unique, err := uniqueEntries(schema, row, record.key)
		if err != nil {
			return Result{}, err
		}
		for _, entry := range unique {
			key := string(entry.key)
			if owner, duplicate := newUnique[key]; duplicate {
				return Result{}, fmt.Errorf(
					"kitdb SQL: UPDATE rows %d and %d create the same unique value", owner+1, index+1,
				)
			}
			newUnique[key] = index
		}
		secondary, err := secondaryIndexEntries(schema, row, record.key)
		if err != nil {
			return Result{}, err
		}
		prepared = append(prepared, preparedUpdate{
			record: record, row: row, encoded: encoded, unique: unique, indexes: secondary,
		})
	}
	for key := range newUnique {
		if _, found, err := transaction.Get([]byte(key)); err != nil {
			return Result{}, err
		} else if found {
			if _, replaced := oldUnique[key]; !replaced {
				return Result{}, fmt.Errorf("kitdb SQL: UPDATE violates a unique constraint")
			}
		}
	}

	oldKeys := sortedMutationKeys(oldUnique)
	for _, key := range oldKeys {
		if err := transaction.Delete(key); err != nil {
			return Result{}, err
		}
	}
	for _, key := range sortedMutationKeys(oldIndexes) {
		if err := transaction.Delete(key); err != nil {
			return Result{}, err
		}
	}
	for _, update := range prepared {
		if err := transaction.Put(update.record.key, update.encoded); err != nil {
			return Result{}, err
		}
		for _, entry := range update.unique {
			if err := transaction.Put(entry.key, entry.value); err != nil {
				return Result{}, err
			}
		}
		for _, entry := range update.indexes {
			if err := transaction.Put(entry.key, entry.value); err != nil {
				return Result{}, err
			}
		}
	}
	for _, update := range prepared {
		if err := transaction.validateRowForeignKeys(schema, update.row); err != nil {
			return Result{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	rows := make([]map[string]any, len(prepared))
	for index, update := range prepared {
		rows[index] = update.row
	}
	return mutationResult("UPDATE", rows, plan.Returning, schema)
}

func (transaction *Transaction) executeDelete(
	ctx context.Context,
	plan *kitdbsql.DeleteStatement,
	parameters []any,
) (Result, error) {
	if err := transaction.ready(); err != nil {
		return Result{}, err
	}
	if plan == nil {
		return Result{}, fmt.Errorf("kitdb SQL: invalid DELETE plan")
	}
	schema, err := transaction.schema(plan.Table)
	if err != nil {
		return Result{}, err
	}
	if err := standaloneWriteSupported(schema); err != nil {
		return Result{}, err
	}
	generation, err := activeRowGeneration(transaction, schema)
	if err != nil {
		return Result{}, err
	}
	if generation != 0 {
		return Result{}, fmt.Errorf(
			"kitdb: standalone writes to migrated row generation %d are not enabled yet", generation,
		)
	}
	conditions, err := bindConditions(schema, plan.Conditions, parameters)
	if err != nil {
		return Result{}, err
	}
	predicate, err := bindPredicate(schema, plan.Predicate, parameters)
	if err != nil {
		return Result{}, err
	}
	records, err := transaction.findMutationRecords(ctx, schema, generation, conditions, predicate)
	if err != nil {
		return Result{}, err
	}
	if len(plan.Returning) != 0 && len(records) > transaction.engine.maximumResultRows {
		return Result{}, fmt.Errorf(
			"kitdb SQL: RETURNING exceeds this server's result limit of %d", transaction.engine.maximumResultRows,
		)
	}
	if len(records) == 0 {
		return Result{CommandTag: "DELETE 0"}, nil
	}
	if err := transaction.validateDeleteReferences(ctx, schema, records); err != nil {
		return Result{}, err
	}

	unique := make(map[string][]byte)
	indexes := make(map[string][]byte)
	for index, record := range records {
		if index&255 == 0 {
			if err := ctx.Err(); err != nil {
				return Result{}, err
			}
		}
		entries, err := uniqueEntries(schema, record.decoded.values, record.key)
		if err != nil {
			return Result{}, err
		}
		for _, entry := range entries {
			unique[string(entry.key)] = entry.key
		}
		secondary, err := secondaryIndexEntries(schema, record.decoded.values, record.key)
		if err != nil {
			return Result{}, err
		}
		for _, entry := range secondary {
			indexes[string(entry.key)] = entry.key
		}
	}
	for _, record := range records {
		if err := transaction.Delete(record.key); err != nil {
			return Result{}, err
		}
	}
	for _, key := range sortedMutationKeys(unique) {
		if err := transaction.Delete(key); err != nil {
			return Result{}, err
		}
	}
	for _, key := range sortedMutationKeys(indexes) {
		if err := transaction.Delete(key); err != nil {
			return Result{}, err
		}
	}
	if err := adjustTableCount(transaction, schema, -int64(len(records))); err != nil {
		return Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	rows := make([]map[string]any, len(records))
	for index, record := range records {
		rows[index] = record.decoded.values
	}
	return mutationResult("DELETE", rows, plan.Returning, schema)
}

func bindAssignments(
	schema kitdbsql.Schema,
	plans []kitdbsql.Assignment,
	parameters []any,
) ([]boundAssignment, error) {
	assignments := make([]boundAssignment, len(plans))
	for index, plan := range plans {
		_, field, found := schema.FieldByName(unqualifiedColumn(plan.Column))
		if !found {
			return nil, fmt.Errorf("kitdb SQL: table %q has no field %q", schema.Name, plan.Column)
		}
		if field.Primary {
			return nil, fmt.Errorf(
				"kitdb: standalone UPDATE does not yet change primary field %q", field.Name,
			)
		}
		if plan.Default {
			assignments[index] = boundAssignment{field: field, defaultValue: true}
			continue
		}
		if field.Sequence != nil && field.Sequence.Mode == "always" {
			return nil, fmt.Errorf("kitdb SQL: cannot assign a value to GENERATED ALWAYS field %q", field.Name)
		}
		expressionPlan := plan.Expression
		if expressionPlan == nil {
			expressionPlan = &kitdbsql.CheckPlan{Kind: "literal", Literal: plan.Value}
		}
		expression, err := bindPredicate(schema, expressionPlan, parameters)
		if err != nil {
			return nil, err
		}
		assignments[index] = boundAssignment{field: field, expression: expression}
	}
	return assignments, nil
}

func (transaction *Transaction) findMutationRecords(
	ctx context.Context,
	schema kitdbsql.Schema,
	generation uint64,
	conditions []boundCondition,
	predicate *boundPredicate,
) ([]mutationRecord, error) {
	access, err := transaction.planRowAccess(schema, generation, conditions, nil)
	if err != nil {
		return nil, err
	}
	records := make([]mutationRecord, 0, min(transaction.engine.maximumMutationRows, 256))
	visited := 0
	err = transaction.walkAccessRows(access, func(key, encoded []byte) (bool, error) {
		visited++
		if visited&255 == 0 {
			if err := ctx.Err(); err != nil {
				return false, err
			}
		}
		decoded, err := decodeRow(schema, encoded)
		if err != nil {
			return false, err
		}
		if !matchesAll(decoded.values, conditions) {
			return false, nil
		}
		matched, err := predicateMatches(decoded.values, predicate)
		if err != nil {
			return false, err
		}
		if !matched {
			return false, nil
		}
		if len(records) >= transaction.engine.maximumMutationRows {
			return false, fmt.Errorf(
				"kitdb SQL: mutation exceeds this server's %d-row limit; narrow the WHERE clause",
				transaction.engine.maximumMutationRows,
			)
		}
		records = append(records, mutationRecord{key: key, decoded: decoded})
		return false, nil
	})
	if err != nil {
		return nil, err
	}
	return records, nil
}

func (transaction *Transaction) standaloneDeleteSupported(target kitdbsql.Schema) error {
	for _, entry := range transaction.catalog.Structs {
		schema, err := decodeCatalogSchema(entry.Definition)
		if err != nil {
			return err
		}
		for _, field := range schema.Fields {
			if field.Reference != nil && strings.EqualFold(field.Reference.Struct, target.Name) {
				return fmt.Errorf(
					"kitdb: standalone DELETE from %q is disabled because %q references it",
					target.Name, schema.Name,
				)
			}
		}
		for _, constraint := range schema.ForeignConstraints {
			if constraint.TargetStructID == target.ID || strings.EqualFold(constraint.TargetStruct, target.Name) {
				return fmt.Errorf(
					"kitdb: standalone DELETE from %q is disabled because %q references it",
					target.Name, schema.Name,
				)
			}
		}
	}
	return nil
}

func validateRequiredFields(schema kitdbsql.Schema, row map[string]any) error {
	for _, field := range schema.Fields {
		if item, found := row[field.Name]; field.NotNull && (!found || item == nil) {
			return fmt.Errorf("kitdb: struct %q field %q cannot be null", schema.Name, field.Name)
		}
	}
	return nil
}

func cloneRow(source map[string]any) map[string]any {
	result := make(map[string]any, len(source))
	for name, item := range source {
		switch current := item.(type) {
		case []byte:
			result[name] = bytes.Clone(current)
		default:
			result[name] = item
		}
	}
	return result
}

func sortedMutationKeys(keys map[string][]byte) [][]byte {
	result := make([][]byte, 0, len(keys))
	for _, key := range keys {
		result = append(result, key)
	}
	sort.Slice(result, func(left, right int) bool { return bytes.Compare(result[left], result[right]) < 0 })
	return result
}

func mutationResult(
	command string,
	rows []map[string]any,
	returning []kitdbsql.Projection,
	schema kitdbsql.Schema,
) (Result, error) {
	result := Result{Affected: int64(len(rows)), CommandTag: fmt.Sprintf("%s %d", command, len(rows))}
	if len(returning) == 0 {
		return result, nil
	}
	columns, names, err := bindProjection(schema, returning)
	if err != nil {
		return Result{}, err
	}
	result.Columns = columns
	result.Rows = make([][]any, len(rows))
	for index, row := range rows {
		result.Rows[index] = projectRow(schema, row, names)
	}
	return result, nil
}
