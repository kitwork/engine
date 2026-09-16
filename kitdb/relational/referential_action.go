package relational

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

const maximumReferentialRounds = 64

var ErrReferentialActionUnsupported = errors.New("kitdb: referential action is outside the supported profile")

type referentialIntent struct {
	deleting bool
	fields   map[uint32]boundAssignment
}

type referentialRowAction struct {
	schema   kitdbsql.Schema
	record   mutationRecord
	intent   referentialIntent
	defaults map[uint32]any
}

// Capture a complete wave before writing its children. Otherwise a key swap
// (A -> B, B -> A) would find children already changed by the previous probe.
// Only the existing transaction publishes; this queue has no durable state.
func (transaction *Transaction) applyReferentialActions(ctx context.Context, effects *deferredRowEffects) error {
	checks := ctx.Value(foreignKeyCheckKey{}).(*foreignKeyChecks)
	for start, round := 0, 0; start < len(effects.rows); round++ {
		if round >= maximumReferentialRounds {
			return fmt.Errorf("%w: referential actions exceed %d rounds", ErrForeignKeyCheckLimit, maximumReferentialRounds)
		}
		end := len(effects.rows)
		actions := make(map[string]*referentialRowAction)
		for _, effect := range effects.rows[start:end] {
			if err := ctx.Err(); err != nil {
				return err
			}
			if effect.old == nil {
				continue
			}
			bindings, err := transaction.reverseForeignKeys(ctx, checks, effect.schema)
			if err != nil {
				return err
			}
			for _, binding := range bindings {
				action := normalizeReferentialAction(binding.constraint.OnUpdate)
				if effect.event == "delete" {
					action = normalizeReferentialAction(binding.constraint.OnDelete)
				}
				if action != "cascade" && action != "set null" && action != "set default" {
					continue
				}
				if err := checks.probe(ctx); err != nil {
					return err
				}
				values, changed := binding.changedTuple(referenceChange{effect.old, effect.next})
				if !changed {
					continue
				}
				conditions, possible := binding.conditions(values)
				if !possible {
					continue
				}
				if err := transaction.collectReferentialRows(ctx, checks, actions, binding, conditions, action, effect.next); err != nil {
					return err
				}
			}
		}
		if err := transaction.stageReferentialRows(ctx, checks, actions); err != nil {
			return err
		}
		start = end
	}
	return nil
}

func (transaction *Transaction) collectReferentialRows(ctx context.Context, checks *foreignKeyChecks, actions map[string]*referentialRowAction, binding reverseForeignKey, conditions []boundCondition, action string, next map[string]any) error {
	generation, err := activeRowGeneration(transaction, binding.child)
	if err != nil {
		return err
	}
	access, err := transaction.planRowAccess(binding.child, generation, conditions, nil)
	if err != nil {
		return err
	}
	return transaction.walkAccessRowsAdmitted(access, func(key, value []byte) error { return checks.admit(ctx, key, value) }, func(key, encoded []byte) (bool, error) {
		decoded, err := decodeRow(binding.child, encoded)
		if err != nil {
			return false, err
		}
		if !matchesAll(decoded.values, conditions) {
			return false, nil
		}
		if err := standaloneWriteSupported(binding.child); err != nil {
			return false, err
		}
		if generation != 0 {
			return false, fmt.Errorf("%w: migrated child row generation %d", ErrReferentialActionUnsupported, generation)
		}
		identity := string(key)
		pending := actions[identity]
		var defaults map[uint32]any
		if pending != nil {
			defaults = pending.defaults
		}
		if action == "set default" {
			// Reject an over-budget row before reserving any sequence values.
			if pending == nil && checks.actionRows >= transaction.engine.maximumMutationRows {
				return false, fmt.Errorf("%w: referential actions exceed %d child mutations", ErrForeignKeyCheckLimit, transaction.engine.maximumMutationRows)
			}
			for _, field := range binding.local {
				if field.Primary {
					return false, fmt.Errorf("%w: SET DEFAULT on primary field %q", ErrReferentialActionUnsupported, field.Name)
				}
			}
			if defaults == nil {
				defaults = make(map[uint32]any)
			}
			if checks.defaultTime.IsZero() {
				checks.defaultTime = time.Now().UTC()
			}
		}
		intent := referentialIntent{deleting: action == "cascade" && next == nil}
		if !intent.deleting {
			intent.fields = make(map[uint32]boundAssignment)
			for i, local := range binding.local {
				var value any
				if action == "cascade" {
					value, err = foreignComparableValue(binding.target[i], local, next[binding.target[i].Name])
					if err != nil {
						return false, err
					}
				}
				if action == "set default" {
					var found bool
					value, found = defaults[local.Tag]
					if !found {
						value, err = transaction.referentialDefault(ctx, checks, local)
						if err != nil {
							return false, fmt.Errorf("kitdb: SET DEFAULT on %q.%q: %w", binding.child.Name, local.Name, err)
						}
						defaults[local.Tag] = value
					}
				}
				value, err = coerceField(local, value)
				if err != nil {
					return false, err
				}
				if action == "cascade" && value != nil {
					roundtrip, conversionErr := foreignComparableValue(local, binding.target[i], value)
					if conversionErr != nil || compareFieldValues(binding.target[i], next[binding.target[i].Name], roundtrip) != 0 {
						return false, fmt.Errorf("%w: cascade key cannot be represented exactly by child field %q", ErrForeignKeyViolation, local.Name)
					}
				}
				// Even an unchanged DEFAULT must be staged/revalidated: its parent
				// may just have disappeared. Only non-default no-ops can be skipped.
				if action != "set default" && compareFieldValues(local, value, decoded.values[local.Name]) == 0 {
					continue
				}
				if local.Primary || (local.Sequence != nil && local.Sequence.Mode == "always" && action != "set default") {
					return false, fmt.Errorf("%w: cannot change primary/generated-always field %q on %q", ErrReferentialActionUnsupported, local.Name, binding.child.Name)
				}
				intent.fields[local.Tag] = boundAssignment{field: local, expression: &boundPredicate{kind: "literal", literal: value}}
			}
			if len(intent.fields) == 0 {
				return false, nil
			}
		}
		if prior := checks.acted[identity]; prior != nil {
			if err := compatibleReferentialIntents(prior, &intent); err != nil {
				return false, err
			}
		}
		if pending == nil {
			if checks.actionRows >= transaction.engine.maximumMutationRows {
				return false, fmt.Errorf("%w: referential actions exceed %d child mutations", ErrForeignKeyCheckLimit, transaction.engine.maximumMutationRows)
			}
			checks.actionRows++
			actions[identity] = &referentialRowAction{schema: binding.child, record: mutationRecord{bytes.Clone(key), decoded}, intent: intent, defaults: defaults}
		} else {
			if err := compatibleReferentialIntents(&pending.intent, &intent); err != nil {
				return false, err
			}
			for tag, assignment := range intent.fields {
				pending.intent.fields[tag] = assignment
			}
			pending.defaults = defaults
		}
		return false, nil
	})
}

func (transaction *Transaction) referentialDefault(ctx context.Context, checks *foreignKeyChecks, field kitdbsql.Field) (any, error) {
	// Charge the encoded literal before decoding and its retained value before
	// queueing. Small old rows must not fan out into unbounded large defaults.
	if err := checks.admit(ctx, nil, field.Default); err != nil {
		return nil, err
	}
	value, err := transaction.fieldDefault(ctx, field, checks.defaultTime)
	if err != nil {
		return nil, err
	}
	nodes := 0
	size := materializedValueBytesBounded(value, 0, &nodes)
	if size > maximumForeignKeyBytes-checks.bytes {
		return nil, ErrForeignKeyCheckLimit
	}
	checks.bytes += size
	return value, nil
}

func compatibleReferentialIntents(previous, next *referentialIntent) error {
	if previous.deleting != next.deleting {
		return fmt.Errorf("%w: multiple paths both delete and update the same child", ErrReferentialActionUnsupported)
	}
	for tag, assignment := range next.fields {
		if prior, ok := previous.fields[tag]; ok && compareFieldValues(assignment.field, prior.expression.literal, assignment.expression.literal) != 0 {
			return fmt.Errorf("%w: multiple paths or an update cycle assign different values to the same child field", ErrReferentialActionUnsupported)
		}
	}
	return nil
}

func (transaction *Transaction) stageReferentialRows(ctx context.Context, checks *foreignKeyChecks, actions map[string]*referentialRowAction) error {
	keys := make([]string, 0, len(actions))
	for key := range actions {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	// Row keys include the immutable table identity; group in explicit table
	// order rather than relying on that physical encoding to remain contiguous.
	groups := make(map[string][]*referentialRowAction)
	var tables []string
	for _, key := range keys {
		action := actions[key]
		if _, ok := groups[action.schema.ID]; !ok {
			tables = append(tables, action.schema.ID)
		}
		groups[action.schema.ID] = append(groups[action.schema.ID], action)
		if checks.acted == nil {
			checks.acted = make(map[string]*referentialIntent)
		}
		if prior := checks.acted[key]; prior != nil {
			for tag, assignment := range action.intent.fields {
				prior.fields[tag] = assignment
			}
		} else {
			checks.acted[key] = &action.intent
		}
	}
	sort.Strings(tables)
	for _, table := range tables {
		group := groups[table]
		var deletes, updates []mutationRecord
		var assignments [][]boundAssignment
		for _, action := range group {
			if action.intent.deleting {
				deletes = append(deletes, action.record)
				continue
			}
			updates = append(updates, action.record)
			var fields []boundAssignment
			for _, field := range action.schema.Fields {
				if assignment, ok := action.intent.fields[field.Tag]; ok {
					fields = append(fields, assignment)
				}
			}
			assignments = append(assignments, fields)
		}
		if len(deletes) != 0 {
			if _, err := transaction.deleteBoundRecords(ctx, group[0].schema, deletes, nil); err != nil {
				return err
			}
		}
		if len(updates) != 0 {
			if _, err := transaction.updateAssignedRecords(ctx, group[0].schema, updates, func(i int) []boundAssignment { return assignments[i] }, nil); err != nil {
				return err
			}
		}
	}
	return nil
}
