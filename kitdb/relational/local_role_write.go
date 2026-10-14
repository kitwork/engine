package relational

import (
	"context"
	"slices"
	"strings"

	"github.com/kitwork/engine/kitdb/pgwire"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

func localAllows(c localRoleCatalog, policy *localRolePolicy, action, table string) bool {
	if policy == nil {
		return false
	}
	for _, role := range c.Roles {
		if slices.Contains(policy.Roles, role.ID) && slices.Contains(*role.grants(action), table) {
			return true
		}
	}
	return false
}

func localExpressionReads(expr *kitdbsql.ExpressionPlan) bool {
	return localExpressionReadsStored(expr, false)
}

func localExpressionReadsStored(expr *kitdbsql.ExpressionPlan, excludedInput bool) bool {
	if expr == nil {
		return false
	}
	if expr.Kind == "field" || expr.Field != "" {
		return !excludedInput || !strings.HasPrefix(strings.ToLower(expr.Field), "excluded.")
	}
	for i := range expr.Arguments {
		if localExpressionReadsStored(&expr.Arguments[i], excludedInput) {
			return true
		}
	}
	return false
}

func (engine *Engine) executeLocalStatement(ctx context.Context, policy *localRolePolicy, active *Transaction, source string, args []any, describe, readOnly bool) (Result, bool, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, true, err
	}
	env, err := kitdbsql.ParseEnvelope(source)
	if err != nil {
		return Result{}, false, nil
	}
	if env.Kind != kitdbsql.StatementInsert && env.Kind != kitdbsql.StatementUpdate && env.Kind != kitdbsql.StatementDelete {
		return engine.executeLocalRead(ctx, policy, active, source, args, describe)
	}
	deny := pgwire.NewError("42501", "table mutation permission denied or query outside local-role profile")
	st, err := kitdbsql.ParseStatement(source)
	if err != nil {
		return Result{}, true, err
	}
	action, table := "", ""
	reads := false
	var returning []kitdbsql.Projection
	var selectSource *kitdbsql.SelectStatement
	conflictUpdate := false
	switch st.Kind {
	case kitdbsql.StatementInsert:
		p := st.Insert
		action, table, returning = "insert", p.Table, p.Returning
		if p.Select != nil && !safeLocalSelect(p.Select) {
			return Result{}, true, deny
		}
		selectSource = p.Select
		if p.Conflict != nil {
			conflictUpdate = !p.Conflict.Nothing
			if !safeLocalExpression(p.Conflict.Predicate) {
				return Result{}, true, deny
			}
			reads = p.Conflict.Predicate != nil
			for _, assignment := range p.Conflict.Assignments {
				if !safeLocalExpression(assignment.Expression) {
					return Result{}, true, deny
				}
				reads = reads || localExpressionReadsStored(assignment.Expression, true)
			}
		}
		for _, row := range p.Values {
			for i := range row {
				if !safeLocalExpression(&row[i]) || localExpressionReads(&row[i]) {
					return Result{}, true, deny
				}
			}
		}
	case kitdbsql.StatementUpdate:
		p := st.Update
		action, table, returning = "update", p.Table, p.Returning
		if !safeLocalExpression(p.Predicate) {
			return Result{}, true, deny
		}
		reads = len(p.Conditions) > 0 || localExpressionReads(p.Predicate)
		for _, a := range p.Assignments {
			if !safeLocalExpression(a.Expression) {
				return Result{}, true, deny
			}
			reads = reads || localExpressionReads(a.Expression)
		}
	case kitdbsql.StatementDelete:
		p := st.Delete
		action, table, returning = "delete", p.Table, p.Returning
		if !safeLocalExpression(p.Predicate) {
			return Result{}, true, deny
		}
		reads = len(p.Conditions) > 0 || localExpressionReads(p.Predicate)
	}
	for _, p := range returning {
		if p.Count || p.Aggregate != "" || !safeLocalExpression(p.Expression) {
			return Result{}, true, deny
		}
	}
	reads = reads || len(returning) > 0
	if readOnly || active != nil && active.readOnly {
		return Result{}, true, pgwire.NewError("25006", "database or transaction is read-only")
	}
	tx := active
	if tx == nil {
		tx, err = engine.BeginTransaction(ctx, TransactionOptions{ReadOnly: describe})
		if err != nil {
			return Result{}, true, err
		}
		defer tx.Rollback()
	}
	schema, err := schemaFromCatalog(tx.catalog, table)
	if err != nil {
		return Result{}, true, deny
	}
	roles, err := readLocalRoles(tx.snapshot)
	if err != nil {
		return Result{}, true, err
	}
	if !localAllows(roles, policy, action, schema.ID) || reads && !localAllows(roles, policy, "select", schema.ID) {
		return Result{}, true, deny
	}
	if conflictUpdate && !localAllows(roles, policy, "update", schema.ID) {
		return Result{}, true, deny
	}
	if selectSource != nil {
		sourceSchema, e := schemaFromCatalog(tx.catalog, selectSource.Table)
		if e != nil || !localAllows(roles, policy, "select", sourceSchema.ID) {
			return Result{}, true, deny
		}
	}
	// Indirect effects need their own explicit authorization model. Refuse them
	// in this direct-table profile, even when another role grants the destination.
	if schema.Partition != nil || len(schema.ForeignConstraints) > 0 {
		return Result{}, true, deny
	}
	for _, f := range schema.Fields {
		if f.Reference != nil || f.Sequence != nil {
			return Result{}, true, deny
		}
	}
	for _, trigger := range tx.catalog.Triggers {
		if trigger.SourceStruct == schema.ID {
			return Result{}, true, deny
		}
	}
	for _, entry := range tx.catalog.Structs {
		if err := ctx.Err(); err != nil {
			return Result{}, true, err
		}
		candidate, e := kitdbsql.DecodeSchema(entry.Definition)
		if e != nil {
			return Result{}, true, e
		}
		for _, fk := range candidate.ForeignConstraints {
			if fk.TargetStructID == schema.ID || strings.EqualFold(fk.TargetStruct, schema.Name) {
				return Result{}, true, deny
			}
		}
		for _, f := range candidate.Fields {
			if f.Reference != nil && strings.EqualFold(f.Reference.Struct, schema.Name) {
				return Result{}, true, deny
			}
		}
	}
	if describe {
		cols, e := tx.Describe(ctx, source)
		return Result{Columns: cols}, true, e
	}
	result, err := tx.Execute(ctx, source, args...)
	if err == nil && active == nil {
		_, err = tx.Commit(ctx)
	}
	return result, true, err
}
