package work

import (
	"context"
	"fmt"
	"strings"

	"github.com/kitwork/engine/utilities/query"
	"github.com/kitwork/engine/value"
)

func lowerKitDBRemoteJoinDistinct(
	left *SchemaTable,
	right *SchemaTable,
	statement kitSQLStatement,
) (kitSQLStatement, error) {
	sources := [2]kitDBRemoteJoinSource{
		{table: left, alias: statement.tableAlias},
		{table: right, alias: statement.joins[0].alias},
	}
	resolver := &kitDBRemoteJoinPlan{sources: sources}
	return lowerKitDBRemoteDistinct(statement, func(requested string) ([]kitSQLProjection, error) {
		qualifier, field := kitSQLReferenceParts(requested)
		if field != "*" {
			return nil, fmt.Errorf("kitdb SQL: invalid star projection %q", requested)
		}
		selected := []int{0, 1}
		if qualifier != "" {
			source, err := resolver.sourceForQualifier(qualifier)
			if err != nil {
				return nil, err
			}
			selected = []int{source}
		}
		fields := make([]kitSQLProjection, 0)
		for _, source := range selected {
			for _, definition := range sources[source].table.definition.Fields {
				fields = append(fields, kitSQLProjection{
					field: sources[source].alias + "." + definition.Name,
				})
			}
		}
		return fields, nil
	})
}

func prepareKitDBRemoteJoinGroupPlan(
	left *SchemaTable,
	right *SchemaTable,
	statement kitSQLStatement,
) (*kitDBRemoteJoinPlan, *kitDBRemoteGroupPlan, error) {
	if len(statement.orders) > kitDBRemoteJoinOrderLimit {
		return nil, nil, fmt.Errorf("kitdb SQL: joined SELECT exceeds %d ORDER BY fields", kitDBRemoteJoinOrderLimit)
	}
	base, err := prepareKitDBRemoteJoinBasePlan(left, right, statement)
	if err != nil {
		return nil, nil, err
	}
	group, err := prepareKitDBRemoteGroupPlanWithResolver(statement, func(
		requested string,
	) (kitDBRemoteResolvedGroupField, error) {
		reference, err := base.resolve(requested)
		if err != nil {
			return kitDBRemoteResolvedGroupField{}, err
		}
		spec := base.sources[reference.source].table.columns[reference.field]
		if spec == nil {
			return kitDBRemoteResolvedGroupField{}, fmt.Errorf(
				"kitdb SQL: struct %q has no field %q",
				base.sources[reference.source].table.table, reference.field,
			)
		}
		return kitDBRemoteResolvedGroupField{
			name: reference.field, kind: reference.kind, source: reference.ref, spec: spec,
		}, nil
	})
	if err != nil {
		return nil, nil, err
	}
	return base, group, nil
}

func executeKitDBRemoteJoinGroups(
	ctx context.Context,
	left *SchemaTable,
	right *SchemaTable,
	statement kitSQLStatement,
) (kitDBRemoteResult, error) {
	joinPlan, groupPlan, err := prepareKitDBRemoteJoinGroupPlan(left, right, statement)
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	sourcePlan := joinPlan.sourcePlan()
	leftAccess, err := left.planKitDBAccess(sourcePlan)
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	return executeKitDBRemoteGroupRows(ctx, statement, groupPlan, func(
		visit func(map[string]value.Value) (bool, error),
	) error {
		return scanKitDBRemoteJoinRows(
			ctx, left, right, joinPlan, sourcePlan, leftAccess,
			func(leftRow kitDBStoredRow, rightRow *kitDBStoredRow) (bool, error) {
				row := make(map[string]value.Value, len(joinPlan.references))
				for encoded, reference := range joinPlan.references {
					row[encoded] = joinPlan.storedRowValue(reference, leftRow, rightRow)
				}
				return visit(row)
			},
		)
	})
}

func kitDBExplainJoinGroupDetail(
	left *SchemaTable,
	access kitDBAccessPlan,
	sourcePlan query.ExecutionPlan,
	statement kitSQLStatement,
	joinPlan *kitDBRemoteJoinPlan,
	groupPlan *kitDBRemoteGroupPlan,
) string {
	detail := kitDBExplainAccessDetail(left.table, access, sourcePlan)
	detail += "; INDEX NESTED LOOP " + strings.ToUpper(joinPlan.kind) + " JOIN "
	detail += joinPlan.sources[1].table.table + " USING " + joinPlan.lookupName
	if joinPlan.predicate != nil {
		detail += "; JOIN FILTER"
	}
	detail = kitDBExplainGroupStages(detail, statement, groupPlan)
	return fmt.Sprintf(
		"%s; INPUT CAP %d; PAIR CAP %d",
		detail, kitDBRemoteJoinInputLimit, kitDBRemoteJoinPairLimit,
	)
}

func executeKitDBRemoteJoinGroupExplain(
	ctx context.Context,
	left *SchemaTable,
	right *SchemaTable,
	statement kitSQLStatement,
) (kitDBRemoteResult, error) {
	joinPlan, groupPlan, err := prepareKitDBRemoteJoinGroupPlan(left, right, statement)
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	sourcePlan := joinPlan.sourcePlan()
	access, err := left.planKitDBAccess(sourcePlan)
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return kitDBRemoteResult{}, err
	}
	return kitDBRemoteResult{
		columns: []kitDBRemoteColumn{
			{name: "id", kind: "integer"},
			{name: "parent", kind: "integer"},
			{name: "notused", kind: "integer"},
			{name: "detail", kind: "text"},
		},
		rows: [][]value.Value{{
			value.New(0), value.New(0), value.New(0),
			value.New(kitDBExplainJoinGroupDetail(
				left, access, sourcePlan, statement, joinPlan, groupPlan,
			)),
		}},
	}, nil
}
