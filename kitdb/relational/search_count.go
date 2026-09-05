package relational

import (
	"context"
	"fmt"
	"math"
	"strings"

	kitdbengine "github.com/kitwork/engine/kitdb"
	"github.com/kitwork/engine/kitdb/searchprojection"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
	"github.com/kitwork/engine/search"
)

func selectSearchCountStar(plan *kitdbsql.SelectStatement) bool {
	if plan == nil || len(plan.Projection) != 1 || len(plan.GroupBy) != 0 || plan.Having != nil {
		return false
	}
	p := plan.Projection[0]
	return p.Name == "" && p.Expression == nil && (p.Count || strings.EqualFold(p.Aggregate, "count"))
}

func relationalSearchCountRows(plan boundRelationalSearchPlan, count uint64) [][]any {
	// LIMIT/OFFSET apply to the single aggregate result, never to its input.
	if plan.limit == 0 || plan.offset > 0 {
		return nil
	}
	return [][]any{{int64(count)}}
}

func (engine *Engine) collectRelationalSearchCount(
	ctx context.Context,
	counter func(context.Context, search.MatchQuery, func(string) (bool, error)) (uint64, error),
	snapshot *kitdbengine.Snapshot,
	plan boundRelationalSearchPlan,
	watermark searchprojection.Watermark,
) ([][]any, error) {
	var accept func(string) (bool, error)
	if plan.predicate != nil && !plan.prefixCoversFilter {
		decoder := newProjectedRowDecoder(plan.schema, selectDecodeTags(plan.schema, nil, plan.predicate, nil))
		accept = func(id string) (bool, error) {
			if err := ctx.Err(); err != nil {
				return false, err
			}
			row, err := decodeRelationalSearchMatch(snapshot, plan, watermark, decoder, id)
			if err != nil {
				return false, err
			}
			return predicateMatches(row, plan.predicate)
		}
	}
	count, err := counter(ctx, search.MatchQuery{
		Fields: plan.fields, Text: plan.query, IdentifierPrefix: plan.identifierPrefix,
	}, accept)
	if err != nil {
		return nil, err
	}
	if count > math.MaxInt64 {
		return nil, fmt.Errorf("kitdb SQL: SEARCH count exceeds BIGINT")
	}
	return relationalSearchCountRows(plan, count), nil
}
