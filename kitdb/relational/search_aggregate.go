package relational

import (
	"context"
	"fmt"

	kitdbengine "github.com/kitwork/engine/kitdb"
	"github.com/kitwork/engine/kitdb/searchprojection"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
	"github.com/kitwork/engine/search"
)

func describeSearchAggregate(schema kitdbsql.Schema, plan *kitdbsql.SelectStatement) ([]Column, error) {
	if plan.HasAfter {
		return nil, fmt.Errorf("kitdb SQL: SEARCH aggregates do not accept AFTER")
	}
	for _, projection := range plan.Projection {
		if projection.Expression != nil || projection.All {
			return nil, fmt.Errorf("kitdb SQL: SEARCH aggregates require field-based aggregates and grouped fields")
		}
		if err := validateRelationalSearchQualifier(plan, searchQualifier(projection.Name)); err != nil {
			return nil, err
		}
	}
	for _, field := range plan.GroupBy {
		if err := validateRelationalSearchQualifier(plan, searchQualifier(field)); err != nil {
			return nil, err
		}
	}
	for _, order := range plan.Order {
		if order.Expression != nil {
			return nil, fmt.Errorf("kitdb SQL: SEARCH aggregate ORDER BY requires a projected field or alias")
		}
	}
	columns, err := describeAggregateSelect(schema, plan)
	if err != nil {
		return nil, err
	}
	if _, _, err := bindAggregateOrder(plan, columns); err != nil {
		return nil, err
	}
	return columns, nil
}

type relationalSearchCounter func(context.Context, search.MatchQuery, func(string) (bool, error)) (uint64, error)

func emptyRelationalSearchCount(ctx context.Context, _ search.MatchQuery, _ func(string) (bool, error)) (uint64, error) {
	return 0, ctx.Err()
}

func (engine *Engine) collectRelationalSearchAggregate(ctx context.Context, counter relationalSearchCounter, snapshot *kitdbengine.Snapshot, plan boundRelationalSearchPlan, watermark searchprojection.Watermark, stats *ExecutionStats) ([][]any, error) {
	if plan.limit == 0 {
		return nil, ctx.Err()
	}
	if plan.countStar {
		return engine.collectRelationalSearchCount(ctx, counter, snapshot, plan, watermark)
	}
	columns := make([]Column, len(plan.resultProjection))
	for i, projection := range plan.resultProjection {
		columns[i] = projection.column
	}
	fields, bindings, err := bindAggregateShape(plan.schema, plan.aggregate)
	if err != nil {
		return nil, err
	}
	budget := newMaterializationBudget(engine.maximumResultRows)
	working := newMaterializationWorkingSet(budget)
	defer working.close()
	if err := budget.consumeColumns(columns); err != nil {
		return nil, err
	}
	stream, err := newAggregateStream(fields, bindings, engine.maximumResultRows, working)
	if err != nil {
		return nil, err
	}
	retained := make([]string, 0, len(fields)+len(bindings))
	for _, field := range fields {
		retained = append(retained, field.Name)
	}
	for _, binding := range bindings {
		if binding.field != nil {
			retained = append(retained, binding.field.Name)
		}
	}
	decoder := newProjectedRowDecoder(plan.schema, selectDecodeTags(plan.schema, nil, plan.predicate, retained))
	matched, err := counter(ctx, search.MatchQuery{Fields: plan.fields, Text: plan.query, IdentifierPrefix: plan.identifierPrefix}, func(id string) (bool, error) {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		row, err := decodeRelationalSearchMatch(snapshot, plan, watermark, decoder, id)
		if err != nil {
			return false, err
		}
		if stats != nil {
			stats.RowsScanned++
			stats.PointLookups++
		}
		bytes, err := working.reserveMap(row, "SEARCH aggregate input row")
		if err != nil {
			return false, err
		}
		defer working.release(bytes)
		accepted, err := predicateMatches(row, plan.predicate)
		if err != nil || !accepted {
			return false, err
		}
		if err := stream.add(row); err != nil {
			return false, err
		}
		return true, nil
	})
	if err != nil {
		return nil, err
	}
	if stats != nil {
		stats.RowsMatched = matched
	}
	result, err := stream.finish(ctx, columns, plan.aggregate, plan.parameters, stats)
	if stats != nil {
		stats.MaterializationPeakBytes = uint64(budget.peakBytes)
	}
	return result.Rows, err
}

func decodeRelationalSearchMatch(snapshot *kitdbengine.Snapshot, plan boundRelationalSearchPlan, watermark searchprojection.Watermark, decoder *projectedRowDecoder, id string) (map[string]any, error) {
	logicalKey, err := relationalSearchRowKey(plan.schema, id)
	if err != nil {
		return nil, err
	}
	key, err := physicalRowKey(plan.schema, logicalKey, watermark.RowGeneration)
	if err != nil {
		return nil, err
	}
	encoded, found, err := snapshot.Get(key)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("kitdb: search projection references a missing source row")
	}
	decoded, err := decoder.decode(encoded)
	return decoded.values, err
}
