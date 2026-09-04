package relational

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	kitdbengine "github.com/kitwork/engine/kitdb"
	kitdbrecord "github.com/kitwork/engine/kitdb/record"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

const (
	indexBuildBatchOperations = 4_096
	indexBuildBatchBytes      = 8 << 20
)

func (engine *Engine) executeDropTable(ctx context.Context, plan *kitdbsql.DropTableStatement) (Result, error) {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if err := engine.readyLocked(); err != nil {
		return Result{}, err
	}
	if plan == nil || plan.Name == "" {
		return Result{}, fmt.Errorf("kitdb SQL: invalid DROP TABLE plan")
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	catalog, err := engine.database.Catalog()
	if err != nil {
		return Result{}, err
	}
	var schema kitdbsql.Schema
	found := false
	for _, entry := range catalog.Structs {
		if entry.Name == plan.Name || strings.EqualFold(entry.Name, plan.Name) {
			schema, err = decodeCatalogSchema(entry.Definition)
			if err != nil {
				return Result{}, err
			}
			found = true
			break
		}
	}
	if !found {
		if plan.IfExists {
			return Result{CommandTag: "DROP TABLE"}, nil
		}
		return Result{}, fmt.Errorf("kitdb SQL: no such table: %s", plan.Name)
	}
	for _, entry := range catalog.Structs {
		candidate, err := decodeCatalogSchema(entry.Definition)
		if err != nil {
			return Result{}, err
		}
		if candidate.ID == schema.ID {
			continue
		}
		for _, constraint := range relationalForeignConstraints(candidate) {
			if constraint.TargetStructID == schema.ID || strings.EqualFold(constraint.TargetStruct, schema.Name) {
				return Result{}, fmt.Errorf(
					"kitdb SQL: cannot drop table %q because foreign key %q on %q references it",
					schema.Name, constraint.Name, candidate.Name,
				)
			}
		}
	}
	transaction, err := engine.database.Begin()
	if err != nil {
		return Result{}, err
	}
	if err := transaction.DeleteStruct(schema.ID); err != nil {
		_ = transaction.Rollback()
		return Result{}, err
	}
	if err := stageRemovedOwnedSequences(transaction, catalog, map[string]*alterSchemaState{schema.ID: nil}); err != nil {
		_ = transaction.Rollback()
		return Result{}, err
	}
	if err := ctx.Err(); err != nil {
		_ = transaction.Rollback()
		return Result{}, err
	}
	if _, err := transaction.CommitSequenceChanges(); err != nil {
		return Result{}, err
	}
	for _, namespace := range []byte{
		kitdbrecord.RowNamespace,
		kitdbrecord.ShadowRowNamespace,
		kitdbrecord.UniqueNamespace,
		kitdbrecord.IndexNamespace,
		kitdbrecord.PhysicalNamespace,
	} {
		prefix, err := fixedKey(namespace, schema.ID, "")
		if err != nil {
			return Result{}, err
		}
		if err := engine.deletePrefixBatched(ctx, prefix); err != nil {
			return Result{}, fmt.Errorf(
				"kitdb: table %q was dropped but orphan cleanup is incomplete: %w", schema.Name, err,
			)
		}
	}
	return Result{CommandTag: "DROP TABLE"}, nil
}

func (engine *Engine) executeCreateIndex(ctx context.Context, plan *kitdbsql.CreateIndexStatement) (Result, error) {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if err := engine.readyLocked(); err != nil {
		return Result{}, err
	}
	if plan == nil || plan.Name == "" || plan.Table == "" || len(plan.Columns) == 0 {
		return Result{}, fmt.Errorf("kitdb SQL: invalid CREATE INDEX plan")
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	catalog, err := engine.database.Catalog()
	if err != nil {
		return Result{}, err
	}
	if owner := catalogIndexOwner(catalog, plan.Name); owner != "" {
		if plan.IfNotExists {
			return Result{CommandTag: "CREATE INDEX"}, nil
		}
		return Result{}, fmt.Errorf("kitdb SQL: index %q already exists on table %q", plan.Name, owner)
	}
	for _, sequence := range catalog.Sequences {
		if sequence.Name == plan.Name {
			return Result{}, fmt.Errorf("kitdb SQL: index name conflicts with sequence %q", plan.Name)
		}
	}
	schema, err := schemaFromCatalog(catalog, plan.Table)
	if err != nil {
		return Result{}, err
	}
	if generation, err := activeRowGeneration(engine.database, schema); err != nil {
		return Result{}, err
	} else if generation != 0 {
		return Result{}, fmt.Errorf("kitdb: CREATE INDEX on migrated row generations is not enabled yet")
	}
	ready, err := readyIndexIdentities(engine.database, schema)
	if err != nil {
		return Result{}, err
	}
	next, target, unique, err := schemaWithIndex(schema, plan)
	if err != nil {
		return Result{}, err
	}
	encoded, err := kitdbsql.EncodeSchema(next)
	if err != nil {
		return Result{}, err
	}
	next, err = kitdbsql.DecodeSchema(encoded)
	if err != nil {
		return Result{}, err
	}

	var prefix []byte
	if unique != nil {
		prefix, err = fixedKey(kitdbrecord.UniqueNamespace, schema.ID, unique.ID)
	} else {
		prefix, err = secondaryIndexBasePrefix(next, target)
	}
	if err != nil {
		return Result{}, err
	}
	if err := engine.deletePrefixBatched(ctx, prefix); err != nil {
		return Result{}, err
	}
	buildErr := error(nil)
	if unique != nil {
		buildErr = engine.buildUniqueConstraint(ctx, schema, *unique)
	} else {
		buildErr = engine.buildSecondaryIndex(ctx, schema, target)
	}
	if buildErr != nil {
		_ = engine.deletePrefixBatched(context.Background(), prefix)
		return Result{}, buildErr
	}
	if unique == nil {
		ready[target.id] = struct{}{}
	}
	if err := engine.publishSchema(ctx, next, encoded, ready, nil); err != nil {
		_ = engine.deletePrefixBatched(context.Background(), prefix)
		return Result{}, err
	}
	return Result{CommandTag: "CREATE INDEX"}, nil
}

func (engine *Engine) executeDropIndex(ctx context.Context, plan *kitdbsql.DropIndexStatement) (Result, error) {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if err := engine.readyLocked(); err != nil {
		return Result{}, err
	}
	if plan == nil || plan.Name == "" {
		return Result{}, fmt.Errorf("kitdb SQL: invalid DROP INDEX plan")
	}
	catalog, err := engine.database.Catalog()
	if err != nil {
		return Result{}, err
	}
	var schema kitdbsql.Schema
	found := false
	for _, entry := range catalog.Structs {
		candidate, err := decodeCatalogSchema(entry.Definition)
		if err != nil {
			return Result{}, err
		}
		if schemaHasDeclaredIndex(candidate, plan.Name) {
			if found {
				return Result{}, fmt.Errorf("kitdb SQL: index %q is ambiguous", plan.Name)
			}
			schema, found = candidate, true
		}
	}
	if !found {
		if plan.IfExists {
			return Result{CommandTag: "DROP INDEX"}, nil
		}
		return Result{}, fmt.Errorf("kitdb SQL: no such index: %s", plan.Name)
	}
	ready, err := readyIndexIdentities(engine.database, schema)
	if err != nil {
		return Result{}, err
	}
	next, removedIndex, removedUnique, err := schemaWithoutIndex(schema, plan.Name)
	if err != nil {
		return Result{}, err
	}
	encoded, err := kitdbsql.EncodeSchema(next)
	if err != nil {
		return Result{}, err
	}
	next, err = kitdbsql.DecodeSchema(encoded)
	if err != nil {
		return Result{}, err
	}
	var prefix, marker []byte
	if removedUnique != nil {
		prefix, err = fixedKey(kitdbrecord.UniqueNamespace, schema.ID, removedUnique.ID)
	} else {
		prefix, err = secondaryIndexBasePrefix(schema, *removedIndex)
		delete(ready, removedIndex.id)
		marker, _ = indexReadyKey(schema, *removedIndex)
	}
	if err != nil {
		return Result{}, err
	}
	if err := engine.publishSchema(ctx, next, encoded, ready, marker); err != nil {
		return Result{}, err
	}
	if err := engine.deletePrefixBatched(ctx, prefix); err != nil {
		return Result{}, fmt.Errorf("kitdb: index was dropped but orphan cleanup is incomplete: %w", err)
	}
	return Result{CommandTag: "DROP INDEX"}, nil
}

func catalogIndexOwner(catalog kitdbengine.CatalogSnapshot, requested string) string {
	for _, entry := range catalog.Structs {
		schema, err := decodeCatalogSchema(entry.Definition)
		if err == nil && schemaHasDeclaredIndex(schema, requested) {
			return schema.Name
		}
	}
	return ""
}

func schemaHasDeclaredIndex(schema kitdbsql.Schema, requested string) bool {
	for _, constraint := range schema.UniqueConstraints {
		if strings.EqualFold(constraint.Name, requested) {
			return true
		}
	}
	for _, field := range schema.Fields {
		for _, member := range field.Indexes {
			name := member.Name
			if name == "" {
				name = "idx_" + schema.Name + "_" + field.Name
			}
			if strings.EqualFold(name, requested) {
				return true
			}
		}
	}
	return false
}

func schemaWithIndex(
	schema kitdbsql.Schema,
	plan *kitdbsql.CreateIndexStatement,
) (kitdbsql.Schema, secondaryIndex, *kitdbsql.UniqueConstraint, error) {
	next := schema
	next.Fields = append([]kitdbsql.Field(nil), schema.Fields...)
	fields := make([]kitdbsql.Field, len(plan.Columns))
	seen := make(map[string]struct{}, len(plan.Columns))
	for position, requested := range plan.Columns {
		canonical, field, found := schema.FieldByName(requested)
		if !found {
			return kitdbsql.Schema{}, secondaryIndex{}, nil, fmt.Errorf(
				"kitdb SQL: table %q has no index field %q", schema.Name, requested,
			)
		}
		if _, duplicate := seen[field.ID]; duplicate {
			return kitdbsql.Schema{}, secondaryIndex{}, nil, fmt.Errorf("kitdb SQL: index repeats field %q", canonical)
		}
		if field.Kind == "interval" {
			return kitdbsql.Schema{}, secondaryIndex{}, nil, fmt.Errorf(
				"kitdb SQL: INTERVAL field %q cannot be indexed in the bounded temporal profile", canonical,
			)
		}
		seen[field.ID] = struct{}{}
		fields[position] = field
	}
	if plan.Unique {
		if len(plan.Conditions) != 0 {
			return kitdbsql.Schema{}, secondaryIndex{}, nil, fmt.Errorf("kitdb SQL: partial UNIQUE INDEX is not enabled yet")
		}
		tags := make([]uint32, len(fields))
		for position, field := range fields {
			tags[position] = field.Tag
		}
		constraint := kitdbsql.UniqueConstraint{
			Version: 1,
			ID:      kitdbsql.StableSchemaID("unique", schema.ID+":"+strings.ToLower(plan.Name)),
			Name:    plan.Name,
			Fields:  tags,
		}
		next.UniqueConstraints = append(append([]kitdbsql.UniqueConstraint(nil), schema.UniqueConstraints...), constraint)
		sort.Slice(next.UniqueConstraints, func(left, right int) bool {
			return strings.ToLower(next.UniqueConstraints[left].Name) < strings.ToLower(next.UniqueConstraints[right].Name)
		})
		return next, secondaryIndex{}, &constraint, nil
	}
	filters, err := indexConditions(schema, plan.Conditions)
	if err != nil {
		return kitdbsql.Schema{}, secondaryIndex{}, nil, err
	}
	columnNames := make([]string, len(fields))
	for position, field := range fields {
		columnNames[position] = field.Name
	}
	identity := kitdbsql.StableSchemaID("index", schema.ID+":"+plan.Name+":"+strings.Join(columnNames, ","))
	for order, field := range fields {
		for position := range next.Fields {
			if next.Fields[position].ID == field.ID {
				next.Fields[position].Indexes = append(next.Fields[position].Indexes, kitdbsql.IndexMember{
					Name: plan.Name, ID: identity, Order: order + 1,
					Filter: append([]kitdbsql.IndexCondition(nil), filters...),
				})
				break
			}
		}
	}
	return next, secondaryIndex{name: plan.Name, id: identity, fields: fields, filter: filters}, nil, nil
}

func indexConditions(schema kitdbsql.Schema, conditions []kitdbsql.Condition) ([]kitdbsql.IndexCondition, error) {
	filters := make([]kitdbsql.IndexCondition, 0, len(conditions))
	for _, condition := range conditions {
		if condition.Operator != "=" && condition.Operator != "is" {
			return nil, fmt.Errorf("kitdb SQL: partial indexes currently accept equality and IS NULL only")
		}
		canonical, field, found := schema.FieldByName(condition.Column)
		if !found {
			return nil, fmt.Errorf("kitdb SQL: table %q has no filter field %q", schema.Name, condition.Column)
		}
		item, err := resolveLiteral(condition.Value, nil)
		if err != nil {
			return nil, fmt.Errorf("kitdb SQL: index filters cannot use parameters: %w", err)
		}
		if item != nil {
			item, err = coerceField(field, item)
			if err != nil {
				return nil, err
			}
		}
		encoded, err := json.Marshal(item)
		if err != nil {
			return nil, err
		}
		filters = append(filters, kitdbsql.IndexCondition{Field: canonical, Value: encoded})
	}
	sort.Slice(filters, func(left, right int) bool { return filters[left].Field < filters[right].Field })
	return filters, nil
}

func schemaWithoutIndex(
	schema kitdbsql.Schema,
	requested string,
) (kitdbsql.Schema, *secondaryIndex, *kitdbsql.UniqueConstraint, error) {
	next := schema
	next.UniqueConstraints = make([]kitdbsql.UniqueConstraint, 0, len(schema.UniqueConstraints))
	var removedUnique *kitdbsql.UniqueConstraint
	for _, constraint := range schema.UniqueConstraints {
		if strings.EqualFold(constraint.Name, requested) {
			removed := constraint
			removedUnique = &removed
			continue
		}
		next.UniqueConstraints = append(next.UniqueConstraints, constraint)
	}
	if removedUnique != nil {
		return next, nil, removedUnique, nil
	}
	indexes, err := collectSecondaryIndexes(schema)
	if err != nil {
		return kitdbsql.Schema{}, nil, nil, err
	}
	var removed *secondaryIndex
	for _, index := range indexes {
		if !index.implicit && strings.EqualFold(index.name, requested) {
			item := index
			removed = &item
			break
		}
	}
	if removed == nil {
		return kitdbsql.Schema{}, nil, nil, fmt.Errorf("kitdb SQL: no such index: %s", requested)
	}
	next.Fields = append([]kitdbsql.Field(nil), schema.Fields...)
	for position := range next.Fields {
		members := next.Fields[position].Indexes[:0]
		for _, member := range next.Fields[position].Indexes {
			name := member.Name
			if name == "" {
				name = "idx_" + schema.Name + "_" + next.Fields[position].Name
			}
			if !strings.EqualFold(name, requested) {
				members = append(members, member)
			}
		}
		next.Fields[position].Indexes = members
	}
	return next, removed, nil, nil
}

func readyIndexIdentities(reader recordReader, schema kitdbsql.Schema) (map[string]struct{}, error) {
	ready := make(map[string]struct{})
	indexes, err := collectSecondaryIndexes(schema)
	if err != nil {
		return nil, err
	}
	for _, index := range indexes {
		ok, err := indexIsReady(reader, schema, index)
		if err != nil {
			return nil, err
		}
		if ok {
			ready[index.id] = struct{}{}
		}
	}
	return ready, nil
}

func (engine *Engine) publishSchema(
	ctx context.Context,
	schema kitdbsql.Schema,
	encoded []byte,
	ready map[string]struct{},
	deleteMarker []byte,
) error {
	transaction, err := engine.database.Begin()
	if err != nil {
		return err
	}
	if err := transaction.DefineStruct(encoded); err != nil {
		_ = transaction.Rollback()
		return err
	}
	indexes, err := collectSecondaryIndexes(schema)
	if err != nil {
		_ = transaction.Rollback()
		return err
	}
	for _, index := range indexes {
		if _, ok := ready[index.id]; !ok {
			continue
		}
		key, err := indexReadyKey(schema, index)
		if err != nil {
			_ = transaction.Rollback()
			return err
		}
		if err := transaction.Put(key, indexReadyValue(schema)); err != nil {
			_ = transaction.Rollback()
			return err
		}
	}
	if len(deleteMarker) != 0 {
		if err := transaction.Delete(deleteMarker); err != nil {
			_ = transaction.Rollback()
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		_ = transaction.Rollback()
		return err
	}
	_, err = transaction.Commit()
	return err
}

func (engine *Engine) buildSecondaryIndex(
	ctx context.Context,
	schema kitdbsql.Schema,
	index secondaryIndex,
) error {
	return engine.walkRowsForIndex(ctx, schema, func(
		transaction *kitdbengine.Tx,
		rowKey []byte,
		row map[string]any,
	) (int, error) {
		entry, applicable, err := secondaryIndexEntry(schema, index, row, rowKey)
		if err != nil || !applicable {
			return 0, err
		}
		if err := transaction.Put(entry.key, entry.value); err != nil {
			return 0, err
		}
		return len(entry.key) + len(entry.value), nil
	}, nil)
}

func (engine *Engine) buildUniqueConstraint(
	ctx context.Context,
	schema kitdbsql.Schema,
	constraint kitdbsql.UniqueConstraint,
) error {
	// The index is built before catalog publication. Include its pending
	// definition for field-aware key encoding, without mutating the caller.
	schema.UniqueConstraints = append(append([]kitdbsql.UniqueConstraint(nil), schema.UniqueConstraints...), constraint)
	batch := make(map[string]struct{})
	return engine.walkRowsForIndex(ctx, schema, func(
		transaction *kitdbengine.Tx,
		rowKey []byte,
		row map[string]any,
	) (int, error) {
		values := make([]any, len(constraint.Fields))
		for position, tag := range constraint.Fields {
			field, found := fieldByTag(schema, tag)
			if !found {
				return 0, fmt.Errorf("kitdb: unique constraint %q references missing field", constraint.Name)
			}
			values[position] = row[field.Name]
		}
		key, applicable, err := uniqueKey(schema, constraint.ID, values)
		if err != nil || !applicable {
			return 0, err
		}
		if _, duplicate := batch[string(key)]; duplicate {
			return 0, fmt.Errorf("kitdb SQL: CREATE UNIQUE INDEX %q found duplicate values", constraint.Name)
		}
		if _, found, err := engine.database.Get(key); err != nil {
			return 0, err
		} else if found {
			return 0, fmt.Errorf("kitdb SQL: CREATE UNIQUE INDEX %q found duplicate values", constraint.Name)
		}
		batch[string(key)] = struct{}{}
		if err := transaction.Put(key, rowKey); err != nil {
			return 0, err
		}
		return len(key) + len(rowKey), nil
	}, func() { clear(batch) })
}

func (engine *Engine) walkRowsForIndex(
	ctx context.Context,
	schema kitdbsql.Schema,
	add func(transaction *kitdbengine.Tx, rowKey []byte, row map[string]any) (int, error),
	afterFlush func(),
) error {
	prefix, err := rowPrefix(schema, 0)
	if err != nil {
		return err
	}
	snapshot, err := engine.database.Snapshot()
	if err != nil {
		return err
	}
	defer snapshot.Close()
	cursor, err := snapshot.Cursor(kitdbengine.RangeOptions{Prefix: prefix})
	if err != nil {
		return err
	}
	defer cursor.Close()
	var transaction *kitdbengine.Tx
	operations, bytesUsed := 0, 0
	flush := func() error {
		if transaction == nil {
			return nil
		}
		var err error
		if operations == 0 {
			err = transaction.Rollback()
		} else {
			_, err = transaction.Commit()
		}
		transaction = nil
		operations, bytesUsed = 0, 0
		if err == nil && afterFlush != nil {
			afterFlush()
		}
		return err
	}
	rollback := func() {
		if transaction != nil {
			_ = transaction.Rollback()
			transaction = nil
		}
	}
	for cursor.Next() {
		if operations&255 == 0 {
			if err := ctx.Err(); err != nil {
				rollback()
				return err
			}
		}
		if transaction == nil {
			transaction, err = engine.database.Begin()
			if err != nil {
				return err
			}
		}
		decoded, err := decodeRow(schema, cursor.Value())
		if err != nil {
			rollback()
			return err
		}
		added, err := add(transaction, cursor.Key(), decoded.values)
		if err != nil {
			rollback()
			return err
		}
		if added != 0 {
			operations++
			bytesUsed += added
		}
		if operations >= indexBuildBatchOperations || bytesUsed >= indexBuildBatchBytes {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	if err := cursor.Err(); err != nil {
		rollback()
		return err
	}
	return flush()
}

func (engine *Engine) deletePrefixBatched(ctx context.Context, prefix []byte) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		snapshot, err := engine.database.Snapshot()
		if err != nil {
			return err
		}
		cursor, err := snapshot.Cursor(kitdbengine.RangeOptions{Prefix: bytes.Clone(prefix), Limit: indexBuildBatchOperations})
		if err != nil {
			_ = snapshot.Close()
			return err
		}
		keys := make([][]byte, 0, indexBuildBatchOperations)
		for cursor.Next() {
			keys = append(keys, cursor.Key())
		}
		cursorErr := cursor.Err()
		_ = cursor.Close()
		_ = snapshot.Close()
		if cursorErr != nil {
			return cursorErr
		}
		if len(keys) == 0 {
			return nil
		}
		transaction, err := engine.database.Begin()
		if err != nil {
			return err
		}
		for _, key := range keys {
			if err := transaction.Delete(key); err != nil {
				_ = transaction.Rollback()
				return err
			}
		}
		if _, err := transaction.Commit(); err != nil {
			return err
		}
		if err := engine.checkpointIndexBuildIfNeeded(); err != nil {
			return err
		}
	}
}
