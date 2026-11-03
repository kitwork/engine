package relational

import (
	"context"
	"strings"

	kitdbengine "github.com/kitwork/engine/kitdb"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

func simplePrimarySelect(plan *kitdbsql.SelectStatement) bool {
	if !streamableSelect(plan) || plan.TableAlias != "" || strings.Contains(plan.Table, ".") ||
		len(plan.Conditions) != 1 || plan.Conditions[0].Operator != "=" ||
		len(plan.Order) != 0 || len(plan.GroupBy) != 0 || plan.Having != nil ||
		plan.HasLimit || plan.Offset != 0 || len(plan.Projection) == 0 {
		return false
	}
	if predicate := plan.Predicate; predicate != nil {
		if predicate.Kind != "binary" || predicate.Operator != "=" || len(predicate.Arguments) != 2 {
			return false
		}
		for _, argument := range predicate.Arguments {
			if (argument.Kind != "field" && argument.Kind != "literal") || len(argument.Arguments) != 0 || argument.Function != nil {
				return false
			}
		}
	}
	for _, projection := range plan.Projection {
		if projection.Qualifier != "" || strings.Contains(projection.Name, ".") {
			return false
		}
	}
	return !strings.Contains(plan.Conditions[0].Column, ".")
}

// This entry owns preparation for both the primary read and its snapshot
// fallback. It must only receive a simplePrimarySelect plan.
func (engine *Engine) executeAutocommitSelect(ctx context.Context, statement kitdbsql.ParsedStatement, parameters []any) (Result, error) {
	engine.mu.RLock()
	if err := engine.readyLocked(); err != nil {
		engine.mu.RUnlock()
		return Result{}, err
	}
	reservation, err := engine.database.ReserveSnapshot(ctx)
	if err != nil {
		engine.mu.RUnlock()
		return Result{}, err
	}
	catalog, err := engine.database.Catalog()
	if err != nil {
		_ = reservation.Close()
		engine.mu.RUnlock()
		return Result{}, err
	}
	var prepared *rowSelectBinding
	if schema, err := schemaFromCatalog(catalog, statement.Select.Table); err == nil {
		if binding, err := bindRowSelect(schema, statement.Select, parameters); err == nil {
			prepared = &binding
		}
	}
	return engine.executePreparedAutocommitSelect(ctx, statement, parameters, reservation, catalog, prepared)
}

// Consumes the caller's lifetime read lock and reservation on every path.
func (engine *Engine) executePreparedAutocommitSelect(ctx context.Context, statement kitdbsql.ParsedStatement, parameters []any, reservation *kitdbengine.SnapshotReservation, catalog kitdbengine.CatalogSnapshot, prepared *rowSelectBinding) (Result, error) {
	unlock := true
	defer func() {
		_ = reservation.Close()
		if unlock {
			engine.mu.RUnlock()
		}
	}()
	if prepared != nil {
		if result, handled, _ := readBoundPrimarySelect(ctx, engine.database, catalog.Transaction, statement.Select, *prepared); handled {
			return result, nil
		}
	}
	transaction, err := engine.captureTransactionLocked(ctx, TransactionOptions{ReadOnly: true}, reservation, &catalog)
	if err != nil {
		return Result{}, err
	}
	unlock = false // The returned transaction now owns the lifetime lock.
	defer transaction.Rollback()
	if prepared == nil || transaction.catalog.Revision != catalog.Revision {
		return transaction.executeParsed(ctx, statement, parameters)
	}
	if err := resolveStatementFunctions(&statement, transaction.catalog); err != nil {
		return Result{}, err
	}
	return transaction.executeSelectWithBinding(ctx, statement.Select, parameters, false, nil, prepared)
}

type primarySelectReader interface {
	recordReader
	LastTransaction() (uint64, error)
}

func readBoundPrimarySelect(ctx context.Context, reader primarySelectReader, base uint64, plan *kitdbsql.SelectStatement, binding rowSelectBinding) (Result, bool, error) {
	schema, conditions, predicate := binding.schema, binding.conditions, binding.predicate
	var primary *kitdbsql.Field
	for i := range schema.Fields {
		if schema.Fields[i].Primary {
			if primary != nil {
				return Result{}, false, nil
			}
			primary = &schema.Fields[i]
		}
	}
	if primary == nil || conditions[0].field.ID != primary.ID || conditions[0].value == nil {
		return Result{}, false, nil
	}
	generation, err := activeRowGeneration(reader, schema)
	if err != nil {
		return Result{}, false, err
	}
	columns, projector, err := bindProjection(schema, plan.Projection)
	if err != nil {
		return Result{}, false, err
	}
	key, err := rowKey(schema, map[string]any{primary.Name: conditions[0].value}, generation)
	if err != nil {
		return Result{}, false, err
	}
	encoded, found, err := reader.Get(key)
	if err != nil {
		return Result{}, false, err
	}
	// Each Get owns its bytes and holds the kernel read lock during main I/O.
	// One monotonic fence covers catalog, physical generation and row reads.
	after, err := reader.LastTransaction()
	if err != nil || after != base {
		return Result{}, false, err
	}
	if err := ctx.Err(); err != nil {
		return Result{}, false, err
	}
	result := Result{Columns: columns, Rows: make([][]any, 0, 1), CommandTag: "SELECT 0"}
	if !found {
		return result, true, nil
	}
	decoder := newProjectedRowDecoder(schema, selectDecodeTags(schema, conditions, predicate, selectRetainedFields(projector, nil)))
	decoded, err := decoder.decode(encoded)
	if err != nil {
		return Result{}, false, err
	}
	matched, err := predicateMatches(decoded.values, predicate)
	if err != nil {
		return Result{}, false, err
	}
	if matchesAll(decoded.values, conditions) && matched {
		result.Rows = append(result.Rows, projectRow(decoded.values, projector))
		result.CommandTag = "SELECT 1"
	}
	return result, true, nil
}
