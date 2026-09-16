package relational

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

const (
	maximumForeignKeyProbes  = 100_000
	maximumForeignKeyEntries = 100_000
	maximumForeignKeyBytes   = 32 << 20
)

var ErrForeignKeyViolation = errors.New("kitdb: foreign key violation")
var ErrForeignKeyCheckLimit = errors.New("kitdb: foreign key check budget exceeded; add an index on the referencing columns or use a smaller batch")

type foreignKeyCheckKey struct{}

// Statement-owned: one ledger/cache is shared by upsert rows and trigger work.
// No contents outlive the pinned catalog/snapshot or survive a failed statement.
type foreignKeyChecks struct {
	bindings               map[string][]reverseForeignKey
	probes, entries, bytes int
	actionRows             int
	acted                  map[string]*referentialIntent
	defaultTime            time.Time
}

type reverseForeignKey struct {
	child         kitdbsql.Schema
	constraint    kitdbsql.ForeignConstraint
	local, target []kitdbsql.Field
}

type referenceChange struct{ old, next map[string]any }

func (checks *foreignKeyChecks) probe(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if checks.probes >= maximumForeignKeyProbes {
		return ErrForeignKeyCheckLimit
	}
	checks.probes++
	return nil
}

func (checks *foreignKeyChecks) admit(ctx context.Context, key, value []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if key != nil {
		if checks.entries >= maximumForeignKeyEntries {
			return ErrForeignKeyCheckLimit
		}
		checks.entries++
	}
	if len(key) > maximumForeignKeyBytes-checks.bytes {
		return ErrForeignKeyCheckLimit
	}
	checks.bytes += len(key)
	if len(value) > maximumForeignKeyBytes-checks.bytes {
		return ErrForeignKeyCheckLimit
	}
	checks.bytes += len(value)
	return nil
}

func (transaction *Transaction) reverseForeignKeys(ctx context.Context, checks *foreignKeyChecks, target kitdbsql.Schema) ([]reverseForeignKey, error) {
	if cached, ok := checks.bindings[target.ID]; ok {
		return cached, nil
	}
	var bindings []reverseForeignKey
	for _, entry := range transaction.catalog.Structs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		child, err := decodeCatalogSchema(entry.Definition)
		if err != nil {
			return nil, err
		}
		for _, constraint := range relationalForeignConstraints(child) {
			// Immutable identity takes precedence over a stale/historical name.
			if constraint.TargetStructID != "" {
				if constraint.TargetStructID != target.ID {
					continue
				}
			} else if !strings.EqualFold(constraint.TargetStruct, target.Name) {
				continue
			}
			if err := checks.probe(ctx); err != nil {
				return nil, err
			}
			local, boundTarget, fields, err := transaction.bindForeignConstraint(child, constraint)
			if err != nil {
				return nil, err
			}
			if boundTarget.ID != target.ID {
				return nil, fmt.Errorf("kitdb: foreign key target identity mismatch")
			}
			bindings = append(bindings, reverseForeignKey{child, constraint, local, fields})
		}
	}
	if checks.bindings == nil {
		checks.bindings = make(map[string][]reverseForeignKey)
	}
	checks.bindings[target.ID] = bindings
	return bindings, nil
}

// Probes run after source row/index staging and before AFTER triggers. Reading
// through the transaction sees self-deletes and the complete batch's new keys.
func (transaction *Transaction) validateReverseReferences(ctx context.Context, target kitdbsql.Schema, changes []referenceChange, event string) error {
	if len(changes) == 0 {
		return nil
	}
	checks, _ := ctx.Value(foreignKeyCheckKey{}).(*foreignKeyChecks)
	if checks == nil {
		checks = &foreignKeyChecks{}
	}
	bindings, err := transaction.reverseForeignKeys(ctx, checks, target)
	if err != nil {
		return err
	}
	for _, binding := range bindings {
		action := normalizeReferentialAction(binding.constraint.OnUpdate)
		if event == "delete" {
			action = normalizeReferentialAction(binding.constraint.OnDelete)
		}
		if action == "cascade" || action == "set null" || action == "set default" {
			continue // Applied through the ordinary mutation paths before checks.
		}
		if action != "restrict" && action != "no action" {
			return fmt.Errorf("kitdb: reverse foreign key action %q is not enabled yet", action)
		}
		for _, change := range changes {
			if err := checks.probe(ctx); err != nil {
				return err
			}
			values, changed := binding.changedTuple(change)
			if !changed {
				continue
			}
			if action == "no action" {
				exists, err := transaction.foreignTargetExists(target, binding.target, values)
				if err != nil {
					return err
				}
				if exists {
					continue
				}
			}
			conditions, possible := binding.conditions(values)
			if !possible {
				continue
			}
			found, err := transaction.foreignReferenceExists(ctx, checks, binding.child, conditions)
			if err != nil {
				return err
			}
			if found {
				return fmt.Errorf("%w: %s on %q is referenced by constraint %q on table %q", ErrForeignKeyViolation, strings.ToUpper(event), target.Name, binding.constraint.Name, binding.child.Name)
			}
		}
	}
	return nil
}

func (binding reverseForeignKey) changedTuple(change referenceChange) ([]any, bool) {
	values := make([]any, len(binding.target))
	unchanged := change.next != nil
	for i, field := range binding.target {
		values[i] = change.old[field.Name]
		if values[i] == nil {
			return nil, false // MATCH SIMPLE cannot reference a NULL tuple.
		}
		if change.next != nil {
			unchanged = unchanged && compareFieldValues(field, values[i], change.next[field.Name]) == 0
		}
	}
	return values, !unchanged
}

func (binding reverseForeignKey) conditions(values []any) ([]boundCondition, bool) {
	conditions := make([]boundCondition, len(binding.local))
	for i, local := range binding.local {
		// Narrowing must not turn an impossible child key into a false match.
		value, err := foreignComparableValue(binding.target[i], local, values[i])
		if err == nil {
			value, err = coerceField(local, value)
		}
		if err != nil {
			return nil, false
		}
		roundtrip, err := foreignComparableValue(local, binding.target[i], value)
		if err != nil || compareFieldValues(binding.target[i], values[i], roundtrip) != 0 {
			return nil, false
		}
		conditions[i] = boundCondition{field: local, operator: "=", value: value}
	}
	return conditions, true
}

func (transaction *Transaction) foreignReferenceExists(ctx context.Context, checks *foreignKeyChecks, child kitdbsql.Schema, conditions []boundCondition) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	generation, err := activeRowGeneration(transaction, child)
	if err != nil {
		return false, err
	}
	access, err := transaction.planRowAccess(child, generation, conditions, nil)
	if err != nil {
		return false, err
	}
	found := false
	err = transaction.walkAccessRowsAdmitted(access, func(key, value []byte) error { return checks.admit(ctx, key, value) }, func(_, encoded []byte) (bool, error) {
		decoded, err := decodeRow(child, encoded)
		if err != nil {
			return false, err
		}
		found = matchesAll(decoded.values, conditions)
		return found, nil
	})
	return found, err
}
