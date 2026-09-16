package relational

import (
	"context"
	"errors"
	"fmt"
	"strings"

	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

var errUpsertCardinality = errors.New("kitdb SQL: ON CONFLICT DO UPDATE cannot affect the same row twice")

type conflictArbiter struct {
	primary bool
	id      string
	fields  []kitdbsql.Field
}

type rowEffectsKey struct{}
type deferredRowEffect struct {
	schema    kitdbsql.Schema
	event     string
	old, next map[string]any
}
type deferredRowEffects struct{ rows []deferredRowEffect }

func ownRowEffects(ctx context.Context) (context.Context, *deferredRowEffects, bool) {
	if effects, _ := ctx.Value(rowEffectsKey{}).(*deferredRowEffects); effects != nil {
		return ctx, effects, false
	}
	effects := &deferredRowEffects{}
	return context.WithValue(ctx, rowEffectsKey{}, effects), effects, true
}

// All input rows must exist before FK checks and AFTER triggers run, just
// as in a plain multi-row INSERT. This also permits forward self-references.
func deferRowEffects(ctx context.Context, schema kitdbsql.Schema, event string, count int, row func(int) (map[string]any, map[string]any)) bool {
	effects, _ := ctx.Value(rowEffectsKey{}).(*deferredRowEffects)
	if effects == nil {
		return false
	}
	for i := 0; i < count; i++ {
		old, next := row(i)
		effects.rows = append(effects.rows, deferredRowEffect{schema, event, old, next})
	}
	return true
}

func (effects *deferredRowEffects) apply(ctx context.Context, transaction *Transaction) error {
	if checks, _ := ctx.Value(foreignKeyCheckKey{}).(*foreignKeyChecks); checks == nil {
		ctx = context.WithValue(ctx, foreignKeyCheckKey{}, &foreignKeyChecks{})
	}
	if err := transaction.applyReferentialActions(ctx, effects); err != nil {
		return err
	}
	ctx = context.WithValue(ctx, rowEffectsKey{}, (*deferredRowEffects)(nil))
	for _, effect := range effects.rows {
		if effect.old != nil {
			if err := transaction.validateReverseReferences(ctx, effect.schema, []referenceChange{{effect.old, effect.next}}, effect.event); err != nil {
				return err
			}
		}
	}
	checked := make(map[string]bool)
	for i := len(effects.rows) - 1; i >= 0; i-- {
		effect := effects.rows[i]
		if err := ctx.Err(); err != nil {
			return err
		}
		row := effect.next
		if row == nil {
			row = effect.old
		}
		key, err := rowKey(effect.schema, row, 0)
		if err != nil {
			return err
		}
		if checked[string(key)] {
			continue
		}
		checked[string(key)] = true
		// The last staged image is authoritative before AFTER triggers run.
		// Walking backwards avoids decoding KROW again and skips deleted rows.
		if effect.next == nil {
			continue
		}
		if err := transaction.validateRowForeignKeys(effect.schema, row); err != nil {
			return err
		}
	}
	for _, effect := range effects.rows {
		if err := transaction.fireAfterTriggers(ctx, effect.schema, effect.event, 1, func(int) (map[string]any, map[string]any) { return effect.old, effect.next }); err != nil {
			return err
		}
	}
	return nil
}

func conflictArbiters(schema kitdbsql.Schema, columns []string) ([]conflictArbiter, error) {
	wanted := make(map[uint32]bool, len(columns))
	for _, column := range columns {
		_, field, ok := schema.FieldByName(column)
		if !ok || wanted[field.Tag] {
			return nil, fmt.Errorf("kitdb SQL: invalid ON CONFLICT field %q", column)
		}
		wanted[field.Tag] = true
	}
	all := []conflictArbiter{{primary: true, fields: schema.PrimaryFields()}}
	for _, field := range schema.Fields {
		if field.Unique && !field.Primary {
			all = append(all, conflictArbiter{id: field.ID, fields: []kitdbsql.Field{field}})
		}
	}
	for _, unique := range schema.UniqueConstraints {
		arbiter := conflictArbiter{id: unique.ID}
		for _, tag := range unique.Fields {
			field, ok := fieldByTag(schema, tag)
			if !ok {
				return nil, fmt.Errorf("kitdb SQL: invalid unique constraint field")
			}
			arbiter.fields = append(arbiter.fields, field)
		}
		all = append(all, arbiter)
	}
	var matches []conflictArbiter
	for _, arbiter := range all {
		if len(arbiter.fields) == 0 {
			continue
		}
		match := len(columns) == 0 || len(arbiter.fields) == len(wanted)
		for _, field := range arbiter.fields {
			match = match && (len(columns) == 0 || wanted[field.Tag])
		}
		if match {
			matches = append(matches, arbiter)
		}
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("kitdb SQL: ON CONFLICT requires a matching primary or unique column tuple")
	}
	return matches, nil
}

func validateConflict(schema kitdbsql.Schema, plan *kitdbsql.ConflictClause, parameters []any) error {
	if plan == nil {
		return nil
	}
	if plan.Nothing {
		if len(plan.Assignments) != 0 || plan.Predicate != nil {
			return fmt.Errorf("kitdb SQL: invalid DO NOTHING plan")
		}
	} else if len(plan.Columns) == 0 || len(plan.Assignments) == 0 {
		return fmt.Errorf("kitdb SQL: ON CONFLICT DO UPDATE requires columns and assignments")
	}
	if _, err := conflictArbiters(schema, plan.Columns); err != nil {
		return err
	}
	seen := make(map[string]bool)
	for _, assignment := range plan.Assignments {
		name, _, ok := schema.FieldByName(assignment.Column)
		if strings.Contains(assignment.Column, ".") || !ok || seen[name] {
			return fmt.Errorf("kitdb SQL: invalid ON CONFLICT assignment %q", assignment.Column)
		}
		seen[name] = true
		if err := validateConflictReferences(schema, assignment.Expression); err != nil {
			return err
		}
	}
	if err := validateConflictReferences(schema, plan.Predicate); err != nil {
		return err
	}
	if _, err := bindAssignments(schema, plan.Assignments, parameters); err != nil {
		return err
	}
	_, err := bindPredicate(schema, plan.Predicate, parameters)
	return err
}

func validateConflictReferences(schema kitdbsql.Schema, expression *kitdbsql.ExpressionPlan) error {
	if expression == nil {
		return nil
	}
	if err := expression.Validate(); err != nil {
		return err
	}
	var walk func(*kitdbsql.ExpressionPlan) error
	walk = func(node *kitdbsql.ExpressionPlan) error {
		if node.Kind == "field" {
			if at := strings.LastIndexByte(node.Field, '.'); at >= 0 {
				prefix := node.Field[:at]
				if !strings.EqualFold(prefix, "excluded") && !strings.EqualFold(prefix, schema.Name) && !strings.EqualFold(prefix, "public."+schema.Name) {
					return fmt.Errorf("kitdb SQL: unknown ON CONFLICT qualifier %q", prefix)
				}
			}
		}
		for i := range node.Arguments {
			if err := walk(&node.Arguments[i]); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(expression)
}

// Bind through the ordinary field/type binder, then replace only excluded
// leaves with the proposed row's typed values. Never reinterpret old-row refs.
func bindExcluded(plan *kitdbsql.ExpressionPlan, bound *boundPredicate, row map[string]any) error {
	if plan == nil || bound == nil {
		return nil
	}
	if plan.Kind == "field" && strings.HasPrefix(strings.ToLower(plan.Field), "excluded.") {
		value, err := evaluateBoundPredicate(row, bound)
		if err != nil {
			return err
		}
		bound.kind, bound.field, bound.literal = "literal", nil, value
	}
	for i := range plan.Arguments {
		if err := bindExcluded(&plan.Arguments[i], bound.arguments[i], row); err != nil {
			return err
		}
	}
	return nil
}

func (transaction *Transaction) findInsertConflict(schema kitdbsql.Schema, row map[string]any, arbiters []conflictArbiter) ([]byte, error) {
	for _, arbiter := range arbiters {
		var key []byte
		var err error
		if arbiter.primary {
			key, err = rowKey(schema, row, 0)
		} else {
			values := make([]any, len(arbiter.fields))
			for i, field := range arbiter.fields {
				values[i] = row[field.Name]
			}
			var applicable bool
			key, applicable, err = uniqueKey(schema, arbiter.id, values)
			if err == nil && !applicable {
				continue
			}
		}
		if err != nil {
			return nil, err
		}
		value, found, err := transaction.Get(key)
		if err != nil {
			return nil, err
		}
		if found {
			if arbiter.primary {
				return key, nil
			}
			return value, nil
		}
	}
	return nil, nil
}

func (transaction *Transaction) executeUpsert(ctx context.Context, schema kitdbsql.Schema, rows []map[string]any, plan *kitdbsql.ConflictClause, parameters []any, returning []boundScalarProjection) (Result, error) {
	effects := &deferredRowEffects{}
	ctx = context.WithValue(ctx, rowEffectsKey{}, effects)
	arbiters, err := conflictArbiters(schema, plan.Columns)
	if err != nil {
		return Result{}, err
	}
	result, err := mutationResult(ctx, "INSERT 0", nil, returning)
	if err != nil {
		return Result{}, err
	}
	touched := make(map[string]bool, len(rows))
	affected := 0
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		// CHECK/NOT NULL are not uniqueness conflicts and cannot be suppressed.
		if err := validateRowChecks(schema, row); err != nil {
			return Result{}, err
		}
		key, err := transaction.findInsertConflict(schema, row, arbiters)
		if err != nil {
			return Result{}, err
		}
		var changed Result
		if key == nil {
			changed, err = transaction.insertBoundRows(ctx, schema, []map[string]any{row}, returning)
			if err != nil {
				return Result{}, err
			}
			key, err = rowKey(schema, row, 0)
			if err != nil {
				return Result{}, err
			}
		} else {
			if plan.Nothing {
				continue
			}
			if touched[string(key)] {
				return Result{}, errUpsertCardinality
			}
			encoded, found, err := transaction.Get(key)
			if err != nil {
				return Result{}, err
			}
			if !found {
				return Result{}, fmt.Errorf("kitdb: unique index references a missing row")
			}
			decoded, err := decodeRow(schema, encoded)
			if err != nil {
				return Result{}, err
			}
			predicate, err := bindPredicate(schema, plan.Predicate, parameters)
			if err != nil {
				return Result{}, err
			}
			if err := bindExcluded(plan.Predicate, predicate, row); err != nil {
				return Result{}, err
			}
			if predicate != nil {
				value, err := evaluateBoundPredicate(decoded.values, predicate)
				if err != nil {
					return Result{}, err
				}
				truth, err := checkTruthOf(value)
				if err != nil {
					return Result{}, err
				}
				if truth != checkTrue {
					continue
				}
			}
			assignments, err := bindAssignments(schema, plan.Assignments, parameters)
			if err != nil {
				return Result{}, err
			}
			for i := range assignments {
				if err := bindExcluded(plan.Assignments[i].Expression, assignments[i].expression, row); err != nil {
					return Result{}, err
				}
			}
			changed, err = transaction.updateBoundRecords(ctx, schema, []mutationRecord{{key: key, decoded: decoded}}, assignments, returning)
			if err != nil {
				return Result{}, err
			}
		}
		touched[string(key)] = true
		affected++
		result.Rows = append(result.Rows, changed.Rows...)
	}
	if err := effects.apply(ctx, transaction); err != nil {
		return Result{}, err
	}
	result.CommandTag = fmt.Sprintf("INSERT 0 %d", affected)
	result.Affected = int64(affected)
	return result, nil
}
