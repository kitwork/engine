package relational

import (
	"fmt"
	"strings"

	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

func (transaction *Transaction) executeJoinExplain(plan *kitdbsql.SelectStatement, parameters []any) (Result, error) {
	if _, err := describeJoinSelect(transaction.catalog, plan); err != nil {
		return Result{}, err
	}
	sources, err := bindJoinSources(transaction.catalog, plan)
	if err != nil {
		return Result{}, err
	}
	joins, err := bindJoins(sources, plan.Joins)
	if err != nil {
		return Result{}, err
	}
	conditions, err := bindJoinConditions(sources, plan.Conditions, parameters)
	if err != nil {
		return Result{}, err
	}
	if _, err := bindJoinPredicatePlan(sources, plan.Predicate, parameters); err != nil {
		return Result{}, err
	}
	var base []boundCondition
	for _, condition := range conditions {
		if condition.column.source == 0 {
			base = append(base, boundCondition{field: condition.column.field, operator: condition.operator, value: condition.value})
		}
	}
	generation, err := activeRowGeneration(transaction, sources[0].schema)
	if err != nil {
		return Result{}, err
	}
	access, err := transaction.planRowAccess(sources[0].schema, generation, base, nil)
	if err != nil {
		return Result{}, err
	}
	rows := [][]any{{int64(0), "nested loop join", fmt.Sprintf(
		"table=%s index=%s sources=%d input_limit=%d candidate_limit=%d snapshot=fixed",
		sources[0].schema.Name, access.name, len(sources), maximumJoinInputRows, maximumJoinCandidatePairs)}}
	for _, join := range joins {
		fields := make([]string, len(join.equalities))
		for i, equality := range join.equalities {
			fields[i] = equality.targetField.Name
		}
		rows = append(rows, []any{int64(len(rows)), "indexed " + join.kind + " join", fmt.Sprintf(
			"table=%s alias=%s equality_fields=%s lookup=runtime-indexed residual=all-equalities",
			sources[join.target].schema.Name, sources[join.target].alias, strings.Join(fields, ","))})
	}
	if selectHasAggregates(plan) {
		rows = append(rows, []any{int64(len(rows)), "join aggregate", fmt.Sprintf(
			"traversal=all-matches limit=after-aggregation group_limit=%d memory_limit=%d spill=false",
			transaction.engine.maximumResultRows, maximumMaterializedBytes)})
	}
	return Result{Columns: explainColumns(), Rows: rows, CommandTag: "EXPLAIN"}, nil
}
