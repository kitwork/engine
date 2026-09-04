package relational

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"time"

	kitdbengine "github.com/kitwork/engine/kitdb"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

func fieldIdentityMode(field kitdbsql.Field) string {
	if field.Sequence != nil {
		if field.Sequence.Mode == "always" {
			return "a"
		}
		if field.Sequence.Mode == "by_default" {
			return "d"
		}
	}
	return ""
}

func prepareCreateSequences(tx *kitdbengine.Tx, original *kitdbsql.CreateTableStatement, catalog kitdbengine.CatalogSnapshot) (*kitdbsql.CreateTableStatement, kitdbengine.CatalogSnapshot, error) {
	owned := false
	for _, column := range original.Columns {
		owned = owned || column.SequenceMode != "" && column.SequenceMode != "default"
	}
	if !owned {
		return original, catalog, nil
	}
	plan := *original
	plan.Columns = append([]kitdbsql.ColumnDefinition(nil), original.Columns...)
	catalog.Sequences = append([]kitdbengine.Sequence(nil), catalog.Sequences...)
	names := make(map[string]bool)
	for _, sequence := range catalog.Sequences {
		names[sequence.Name] = true
	}
	for _, table := range catalog.Structs {
		names[table.Name] = true
		schema, err := decodeCatalogSchema(table.Definition)
		if err != nil {
			return nil, catalog, err
		}
		for _, index := range catalogIndexesFor(schema) {
			names[index.name] = true
		}
	}
	names[plan.Name] = true
	for i := range plan.Columns {
		column := &plan.Columns[i]
		if column.SequenceMode == "" || column.SequenceMode == "default" {
			continue
		}
		base := plan.Name + "_" + column.Name + "_seq"
		if len(base) > 96 {
			base = "sequence_" + kitdbsql.StableSchemaID("sequence-name", base)
		}
		name := base
		for suffix := 2; names[name]; suffix++ {
			if suffix > 2048 {
				return nil, catalog, fmt.Errorf("kitdb: automatic sequence name budget exceeded")
			}
			name = base + "_" + strconv.Itoa(suffix)
		}
		dataType, valid := kitdbsql.SequenceDataTypeForKind(column.Type.Kind)
		if !valid {
			return nil, catalog, fmt.Errorf("kitdb: field %q cannot own a sequence", column.Name)
		}
		maximum := int64(math.MaxInt64)
		switch dataType {
		case "smallint":
			maximum = math.MaxInt16
		case "integer":
			maximum = math.MaxInt32
		}
		sequence, err := tx.CreateSequence(kitdbengine.Sequence{
			Name: name, DataType: dataType, Start: 1, Increment: 1, Minimum: 1, Maximum: maximum,
			Cache: column.SequenceCache, OwnerStruct: kitdbsql.StableSchemaID("struct", plan.Name), OwnerTag: uint32(i + 1),
		})
		if err != nil {
			return nil, catalog, err
		}
		column.SequenceName = name
		names[name] = true
		catalog.Sequences = append(catalog.Sequences, sequence)
	}
	return &plan, catalog, nil
}

func (transaction *Transaction) sequenceState() *sequenceSession {
	transaction.mu.Lock()
	defer transaction.mu.Unlock()
	if transaction.sequences == nil {
		transaction.sequences = &sequenceSession{}
	}
	return transaction.sequences
}

func (session *sequenceSession) nextByID(ctx context.Context, database *kitdbengine.DB, id string) (int64, error) {
	session.mu.Lock()
	defer session.mu.Unlock()
	if _, found := session.values[id]; !found && len(session.values) >= 1024 {
		return 0, fmt.Errorf("kitdb SQL: session sequence-state budget exceeded")
	}
	sequence, value, err := database.NextSequenceByID(ctx, id)
	if err != nil {
		return 0, err
	}
	if session.values == nil {
		session.values = make(map[string]int64)
	}
	session.values[id] = value
	session.lastID, session.lastName = id, sequence.Name
	return value, nil
}

func (transaction *Transaction) fieldDefault(ctx context.Context, field kitdbsql.Field, now time.Time) (any, error) {
	if field.Sequence != nil {
		return transaction.sequenceState().nextByID(ctx, transaction.engine.database, field.Sequence.ID)
	}
	row, err := fillRow(kitdbsql.Schema{Name: "default", Fields: []kitdbsql.Field{field}}, nil, now)
	return row[field.Name], err
}

// Drop only sequences whose owner field was removed. The kernel validates
// external references against the final graph before publishing any deletion.
func stageRemovedOwnedSequences(tx *kitdbengine.Tx, catalog kitdbengine.CatalogSnapshot, changed map[string]*alterSchemaState) error {
	for _, sequence := range catalog.Sequences {
		state, changedOwner := changed[sequence.OwnerStruct]
		if !changedOwner {
			continue
		}
		retained := false
		if state != nil {
			for _, field := range state.schema.Fields {
				retained = retained || field.Tag == sequence.OwnerTag && field.Sequence != nil && field.Sequence.ID == sequence.ID
			}
		}
		if !retained {
			if err := tx.DropSequence(sequence.ID); err != nil {
				return err
			}
		}
	}
	return nil
}
