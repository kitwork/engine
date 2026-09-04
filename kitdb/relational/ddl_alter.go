package relational

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	kitdbengine "github.com/kitwork/engine/kitdb"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

type alterSchemaState struct {
	schema kitdbsql.Schema
	ready  map[string]struct{}
}

func (engine *Engine) executeAlterTable(ctx context.Context, plan *kitdbsql.AlterTableStatement) (Result, error) {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if err := engine.readyLocked(); err != nil {
		return Result{}, err
	}
	if plan == nil || plan.Table == "" || plan.Action == kitdbsql.AlterTableInvalid {
		return Result{}, fmt.Errorf("kitdb SQL: invalid ALTER TABLE plan")
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	catalog, err := engine.database.Catalog()
	if err != nil {
		return Result{}, err
	}
	states := make(map[string]*alterSchemaState, len(catalog.Structs))
	var target *alterSchemaState
	for _, entry := range catalog.Structs {
		schema, err := decodeCatalogSchema(entry.Definition)
		if err != nil {
			return Result{}, err
		}
		ready, err := readyIndexIdentities(engine.database, schema)
		if err != nil {
			return Result{}, err
		}
		state := &alterSchemaState{schema: schema, ready: ready}
		states[schema.ID] = state
		if schema.Name == plan.Table || strings.EqualFold(schema.Name, plan.Table) {
			if target != nil {
				return Result{}, fmt.Errorf("kitdb SQL: ambiguous table %q", plan.Table)
			}
			target = state
		}
	}
	if target == nil {
		return Result{}, fmt.Errorf("kitdb SQL: no such table: %s", plan.Table)
	}
	changed := map[string]*alterSchemaState{target.schema.ID: target}
	switch plan.Action {
	case kitdbsql.AlterTableAddColumn:
		err = alterAddColumn(&target.schema, plan.Column)
	case kitdbsql.AlterTableDropColumn:
		err = alterDropColumn(states, &target.schema, plan.OldName, plan.IfExists)
	case kitdbsql.AlterTableRenameColumn:
		err = alterRenameColumn(states, &target.schema, plan.OldName, plan.NewName, changed)
	case kitdbsql.AlterTableRenameTable:
		err = alterRenameTable(states, &target.schema, plan.NewName, changed)
	case kitdbsql.AlterTableSetSearchable:
		err = alterSearchable(&target.schema, plan.OldName, true, plan.SearchWeight)
	case kitdbsql.AlterTableDropSearchable:
		err = alterSearchable(&target.schema, plan.OldName, false, 0)
	case kitdbsql.AlterTableSetAnalytics:
		err = alterAnalytics(&target.schema, plan.OldName, true)
	case kitdbsql.AlterTableDropAnalytics:
		err = alterAnalytics(&target.schema, plan.OldName, false)
	case kitdbsql.AlterTableSetPartitioning:
		err = alterSetPartitioning(&target.schema, plan.Partition)
	case kitdbsql.AlterTableDropPartitioning:
		err = alterDropPartitioning(&target.schema)
	default:
		err = fmt.Errorf("kitdb SQL: unsupported ALTER TABLE action")
	}
	if err != nil {
		return Result{}, err
	}
	if err := publishAlterSchemas(ctx, engine.database, changed); err != nil {
		return Result{}, err
	}
	return Result{CommandTag: "ALTER TABLE"}, nil
}

func alterSetPartitioning(schema *kitdbsql.Schema, plan *kitdbsql.PartitionDefinition) error {
	if schema == nil || plan == nil || plan.Field == "" {
		return fmt.Errorf("kitdb SQL: invalid SET PARTITION BY")
	}
	canonical, field, found := schema.FieldByName(plan.Field)
	if !found {
		return fmt.Errorf("kitdb SQL: table %q has no field %q", schema.Name, plan.Field)
	}
	typeInfo, found := kitdbsql.LookupKind(field.Kind)
	if !found || typeInfo.Family != kitdbsql.FamilyInteger {
		return fmt.Errorf(
			"kitdb SQL: PARTITION BY %s requires an integer field",
			strings.ToUpper(plan.Strategy),
		)
	}
	partition := &kitdbsql.Partition{
		Version:  kitdbsql.PartitionVersion1,
		Field:    field.Tag,
		Strategy: strings.ToLower(plan.Strategy),
	}
	switch partition.Strategy {
	case "hash":
		partition.Buckets = kitdbsql.PartitionHashBuckets
	case "range":
	default:
		return fmt.Errorf("kitdb SQL: PARTITION BY supports HASH or RANGE")
	}
	if current := schema.Partition; current != nil && *current == *partition {
		return fmt.Errorf(
			"kitdb SQL: table %q is already partitioned by %s (%s)",
			schema.Name, strings.ToUpper(partition.Strategy), canonical,
		)
	}
	schema.Partition = partition
	if schema.Version < kitdbsql.SchemaVersion8 {
		schema.Version = kitdbsql.SchemaVersion8
	}
	return nil
}

func alterDropPartitioning(schema *kitdbsql.Schema) error {
	if schema == nil {
		return fmt.Errorf("kitdb SQL: invalid DROP PARTITIONING")
	}
	if schema.Partition == nil {
		return fmt.Errorf("kitdb SQL: table %q is not partitioned", schema.Name)
	}
	schema.Partition = nil
	return nil
}

func alterAddColumn(schema *kitdbsql.Schema, column *kitdbsql.ColumnDefinition) error {
	if schema == nil || column == nil || column.Name == "" {
		return fmt.Errorf("kitdb SQL: invalid ADD COLUMN")
	}
	if _, _, found := schema.FieldByName(column.Name); found {
		return fmt.Errorf("kitdb SQL: table %q already has field or alias %q", schema.Name, column.Name)
	}
	if column.Primary || column.NotNull || column.Unique || column.HasDefault || column.SequenceMode != "" ||
		column.Reference != nil || len(column.Checks) != 0 {
		return fmt.Errorf(
			"kitdb SQL: ADD COLUMN currently accepts a nullable column without default or constraints; backfilled ALTER needs a migration phase",
		)
	}
	field := kitdbsql.Field{
		ID:            kitdbsql.StableSchemaID("field", schema.ID+":"+column.Name),
		Tag:           schema.NextFieldTag,
		Name:          column.Name,
		Position:      len(schema.Fields),
		Kind:          column.Type.Kind,
		Precision:     column.Precision,
		Scale:         column.Scale,
		TimePrecision: column.TimePrecision,
		TextLength:    column.TextLength,
		ExactUUID:     column.Type.ID == kitdbsql.TypeUUID,
		Enum:          append([]string(nil), column.Choices...),
		Default:       json.RawMessage("null"),
		Searchable:    column.Searchable,
		SearchWeight:  column.SearchWeight,
		Analytics:     column.Analytics,
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
	if field.Tag == 0 {
		return fmt.Errorf("kitdb: table %q exhausted field tags", schema.Name)
	}
	schema.Fields = append(schema.Fields, field)
	schema.NextFieldTag++
	if schema.NextFieldTag == 0 {
		return fmt.Errorf("kitdb: table %q exhausted field tags", schema.Name)
	}
	return nil
}

func alterSearchable(
	schema *kitdbsql.Schema,
	requested string,
	searchable bool,
	weight int,
) error {
	canonical, field, found := schema.FieldByName(requested)
	if !found {
		return fmt.Errorf("kitdb SQL: table %q has no field %q", schema.Name, requested)
	}
	typeInfo, found := kitdbsql.LookupKind(field.Kind)
	if !found || (typeInfo.Family != kitdbsql.FamilyText &&
		typeInfo.Family != kitdbsql.FamilyIdentifier && typeInfo.Family != kitdbsql.FamilyChoice) {
		return fmt.Errorf("kitdb SQL: field %q is not text-compatible and cannot be SEARCHABLE", canonical)
	}
	if searchable && (weight < 1 || weight > 16) {
		return fmt.Errorf("kitdb SQL: SEARCHABLE WEIGHT must be between 1 and 16")
	}
	if field.Searchable == searchable && (!searchable || field.SearchWeight == weight) {
		state := "not searchable"
		if searchable {
			state = fmt.Sprintf("already SEARCHABLE WEIGHT %d", weight)
		}
		return fmt.Errorf("kitdb SQL: field %q is %s", canonical, state)
	}
	for index := range schema.Fields {
		if schema.Fields[index].ID != field.ID {
			continue
		}
		schema.Fields[index].Searchable = searchable
		schema.Fields[index].SearchWeight = weight
		return nil
	}
	return fmt.Errorf("kitdb SQL: field %q disappeared during ALTER", canonical)
}

func alterAnalytics(schema *kitdbsql.Schema, requested string, analytics bool) error {
	canonical, field, found := schema.FieldByName(requested)
	if !found {
		return fmt.Errorf("kitdb SQL: table %q has no field %q", schema.Name, requested)
	}
	typeInfo, found := kitdbsql.LookupKind(field.Kind)
	if !found {
		return fmt.Errorf("kitdb SQL: field %q has unsupported kind %q", canonical, field.Kind)
	}
	switch typeInfo.Family {
	case kitdbsql.FamilyInteger, kitdbsql.FamilySystem, kitdbsql.FamilyFloat, kitdbsql.FamilyBoolean,
		kitdbsql.FamilyText, kitdbsql.FamilyIdentifier, kitdbsql.FamilyChoice:
	default:
		return fmt.Errorf("kitdb SQL: field %q cannot use ANALYTICS", canonical)
	}
	if field.Analytics == analytics {
		state := "does not use ANALYTICS"
		if analytics {
			state = "already uses ANALYTICS"
		}
		return fmt.Errorf("kitdb SQL: field %q %s", canonical, state)
	}
	for index := range schema.Fields {
		if schema.Fields[index].ID == field.ID {
			schema.Fields[index].Analytics = analytics
			return nil
		}
	}
	return fmt.Errorf("kitdb SQL: field %q disappeared during ALTER", canonical)
}

func alterDropColumn(
	states map[string]*alterSchemaState,
	schema *kitdbsql.Schema,
	requested string,
	ifExists bool,
) error {
	canonical, field, found := schema.FieldByName(requested)
	if !found {
		if ifExists {
			return nil
		}
		return fmt.Errorf("kitdb SQL: table %q has no field %q", schema.Name, requested)
	}
	if len(schema.Fields) == 1 {
		return fmt.Errorf("kitdb SQL: cannot drop the last field of table %q", schema.Name)
	}
	if field.Primary || field.Unique || field.Reference != nil || len(field.Indexes) != 0 {
		return fmt.Errorf("kitdb SQL: cannot drop field %q while a key, reference, or index depends on it", canonical)
	}
	if schema.Partition != nil && schema.Partition.Field == field.Tag {
		return fmt.Errorf("kitdb SQL: cannot drop field %q while table partitioning depends on it", canonical)
	}
	for _, candidate := range schema.Fields {
		for _, index := range candidate.Indexes {
			for _, filter := range index.Filter {
				if strings.EqualFold(filter.Field, canonical) {
					return fmt.Errorf("kitdb SQL: cannot drop field %q while an index filter depends on it", canonical)
				}
			}
		}
	}
	for _, constraint := range schema.UniqueConstraints {
		if containsFieldTag(constraint.Fields, field.Tag) {
			return fmt.Errorf("kitdb SQL: cannot drop field %q while unique constraint %q depends on it", canonical, constraint.Name)
		}
	}
	for _, constraint := range relationalForeignConstraints(*schema) {
		if containsFieldTag(constraint.Fields, field.Tag) {
			return fmt.Errorf("kitdb SQL: cannot drop field %q while foreign key %q depends on it", canonical, constraint.Name)
		}
	}
	for _, constraint := range schema.CheckConstraints {
		if checkExpressionUsesTag(constraint.Expression, field.Tag) {
			return fmt.Errorf("kitdb SQL: cannot drop field %q while check %q depends on it", canonical, constraint.Name)
		}
	}
	for _, state := range states {
		for _, constraint := range relationalForeignConstraints(state.schema) {
			if !foreignTargetsSchema(constraint, *schema) {
				continue
			}
			for _, target := range constraint.TargetFields {
				if strings.EqualFold(target, canonical) {
					return fmt.Errorf(
						"kitdb SQL: cannot drop field %q because foreign key %q on %q targets it",
						canonical, constraint.Name, state.schema.Name,
					)
				}
			}
		}
	}
	fields := make([]kitdbsql.Field, 0, len(schema.Fields)-1)
	for _, candidate := range schema.Fields {
		if candidate.ID == field.ID {
			continue
		}
		candidate.Position = len(fields)
		fields = append(fields, candidate)
	}
	schema.Fields = fields
	return nil
}

func alterRenameColumn(
	states map[string]*alterSchemaState,
	schema *kitdbsql.Schema,
	requested, replacement string,
	changed map[string]*alterSchemaState,
) error {
	canonical, field, found := schema.FieldByName(requested)
	if !found {
		return fmt.Errorf("kitdb SQL: table %q has no field %q", schema.Name, requested)
	}
	if replacement == "" {
		return fmt.Errorf("kitdb SQL: replacement field name is empty")
	}
	if existing, other, found := schema.FieldByName(replacement); found && other.ID != field.ID {
		return fmt.Errorf("kitdb SQL: field name %q already resolves to %q", replacement, existing)
	}
	if strings.EqualFold(canonical, replacement) {
		return fmt.Errorf("kitdb SQL: field %q already resolves as %q", canonical, replacement)
	}
	for index := range schema.Fields {
		if schema.Fields[index].ID != field.ID {
			continue
		}
		schema.Fields[index].Name = replacement
		aliases := schema.Fields[index].Aliases[:0]
		for _, alias := range schema.Fields[index].Aliases {
			if alias != replacement && alias != canonical {
				aliases = append(aliases, alias)
			}
		}
		schema.Fields[index].Aliases = append(aliases, canonical)
		sort.Strings(schema.Fields[index].Aliases)
	}
	for fieldIndex := range schema.Fields {
		for memberIndex := range schema.Fields[fieldIndex].Indexes {
			for filterIndex := range schema.Fields[fieldIndex].Indexes[memberIndex].Filter {
				filter := &schema.Fields[fieldIndex].Indexes[memberIndex].Filter[filterIndex]
				if strings.EqualFold(filter.Field, canonical) {
					filter.Field = replacement
				}
			}
		}
	}
	for _, state := range states {
		stateChanged := state.schema.ID == schema.ID
		for index := range state.schema.Fields {
			reference := state.schema.Fields[index].Reference
			if reference != nil && referenceTargetsSchema(reference.Struct, *schema) &&
				strings.EqualFold(reference.Field, canonical) {
				reference.Field = replacement
				stateChanged = true
			}
		}
		for index := range state.schema.ForeignConstraints {
			constraint := &state.schema.ForeignConstraints[index]
			if !foreignTargetsSchema(*constraint, *schema) {
				continue
			}
			for targetIndex := range constraint.TargetFields {
				if strings.EqualFold(constraint.TargetFields[targetIndex], canonical) {
					constraint.TargetFields[targetIndex] = replacement
					stateChanged = true
				}
			}
		}
		if stateChanged {
			changed[state.schema.ID] = state
		}
	}
	return nil
}

func alterRenameTable(
	states map[string]*alterSchemaState,
	schema *kitdbsql.Schema,
	replacement string,
	changed map[string]*alterSchemaState,
) error {
	if replacement == "" {
		return fmt.Errorf("kitdb SQL: replacement table name is empty")
	}
	for _, state := range states {
		if state.schema.ID != schema.ID && strings.EqualFold(state.schema.Name, replacement) {
			return fmt.Errorf("kitdb SQL: table %q already exists", state.schema.Name)
		}
	}
	if strings.EqualFold(schema.Name, replacement) {
		return fmt.Errorf("kitdb SQL: table %q already resolves as %q", schema.Name, replacement)
	}
	previous := schema.Name
	stabilizeUnnamedIndexes(schema)
	schema.Name = replacement
	for _, state := range states {
		stateChanged := state.schema.ID == schema.ID
		for index := range state.schema.Fields {
			reference := state.schema.Fields[index].Reference
			if reference != nil && referenceTargetsSchema(reference.Struct, kitdbsql.Schema{ID: schema.ID, Name: previous}) {
				reference.Struct = replacement
				stateChanged = true
			}
		}
		for index := range state.schema.ForeignConstraints {
			constraint := &state.schema.ForeignConstraints[index]
			if foreignTargetsSchema(*constraint, kitdbsql.Schema{ID: schema.ID, Name: previous}) {
				constraint.TargetStruct = replacement
				stateChanged = true
			}
		}
		if stateChanged {
			changed[state.schema.ID] = state
		}
	}
	return nil
}

func stabilizeUnnamedIndexes(schema *kitdbsql.Schema) {
	if schema == nil {
		return
	}
	for fieldIndex := range schema.Fields {
		for memberIndex := range schema.Fields[fieldIndex].Indexes {
			member := &schema.Fields[fieldIndex].Indexes[memberIndex]
			if member.Name != "" {
				continue
			}
			member.Name = "idx_" + schema.Name + "_" + schema.Fields[fieldIndex].Name
			if member.ID == "" {
				member.ID = kitdbsql.StableSchemaID(
					"index", schema.ID+":"+member.Name+":"+schema.Fields[fieldIndex].Name,
				)
			}
		}
	}
}

func publishAlterSchemas(
	ctx context.Context,
	database *kitdbengine.DB,
	changed map[string]*alterSchemaState,
) error {
	identities := make([]string, 0, len(changed))
	for identity := range changed {
		identities = append(identities, identity)
	}
	sort.Strings(identities)
	transaction, err := database.Begin()
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	catalog, err := database.Catalog()
	if err != nil {
		return err
	}
	if err := stageRemovedOwnedSequences(transaction, catalog, changed); err != nil {
		return err
	}
	for _, identity := range identities {
		state := changed[identity]
		encoded, err := kitdbsql.EncodeSchema(state.schema)
		if err != nil {
			_ = transaction.Rollback()
			return err
		}
		state.schema, err = kitdbsql.DecodeSchema(encoded)
		if err != nil {
			_ = transaction.Rollback()
			return err
		}
		if err := transaction.DefineStruct(encoded); err != nil {
			_ = transaction.Rollback()
			return err
		}
		indexes, err := collectSecondaryIndexes(state.schema)
		if err != nil {
			_ = transaction.Rollback()
			return err
		}
		for _, index := range indexes {
			if _, ready := state.ready[index.id]; !ready {
				continue
			}
			key, err := indexReadyKey(state.schema, index)
			if err == nil {
				err = transaction.Put(key, indexReadyValue(state.schema))
			}
			if err != nil {
				_ = transaction.Rollback()
				return err
			}
		}
	}
	if err := ctx.Err(); err != nil {
		_ = transaction.Rollback()
		return err
	}
	_, err = transaction.CommitSequenceChanges()
	return err
}

func containsFieldTag(fields []uint32, tag uint32) bool {
	for _, candidate := range fields {
		if candidate == tag {
			return true
		}
	}
	return false
}

func checkExpressionUsesTag(expression kitdbsql.CheckExpression, tag uint32) bool {
	if expression.Kind == "field" && expression.Field == tag {
		return true
	}
	for _, argument := range expression.Arguments {
		if checkExpressionUsesTag(argument, tag) {
			return true
		}
	}
	if expression.CaseBase != nil && checkExpressionUsesTag(*expression.CaseBase, tag) {
		return true
	}
	for _, branch := range expression.Branches {
		if checkExpressionUsesTag(branch.When, tag) || checkExpressionUsesTag(branch.Then, tag) {
			return true
		}
	}
	return expression.Fallback != nil && checkExpressionUsesTag(*expression.Fallback, tag)
}

func foreignTargetsSchema(constraint kitdbsql.ForeignConstraint, target kitdbsql.Schema) bool {
	return constraint.TargetStructID == target.ID || strings.EqualFold(constraint.TargetStruct, target.Name)
}

func referenceTargetsSchema(requested string, target kitdbsql.Schema) bool {
	return strings.EqualFold(requested, target.Name)
}
