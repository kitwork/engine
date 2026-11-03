package work

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/kitwork/engine/value"
)

type colCheckRef struct {
	name     string
	operator string
	value    value.Value
}

// StructCheckConstraint is the durable, backend-neutral CHECK contract. Field
// references live as immutable tags, not SQL text or mutable public names.
type StructCheckConstraint struct {
	Version    int                   `json:"version"`
	ID         string                `json:"id"`
	Name       string                `json:"name"`
	Expression StructCheckExpression `json:"expression"`
}

type StructCheckExpression struct {
	Kind      string                  `json:"kind"`
	Literal   value.Value             `json:"literal,omitempty"`
	Field     uint32                  `json:"field,omitempty"`
	Operator  string                  `json:"operator,omitempty"`
	Arguments []StructCheckExpression `json:"arguments,omitempty"`
	CaseBase  *StructCheckExpression  `json:"caseBase,omitempty"`
	Branches  []StructCheckCaseBranch `json:"branches,omitempty"`
	Fallback  *StructCheckExpression  `json:"fallback,omitempty"`
}

type StructCheckCaseBranch struct {
	When StructCheckExpression `json:"when"`
	Then StructCheckExpression `json:"then"`
}

func normalizeKitDBCheckOperator(operator string) string {
	operator = strings.TrimSpace(strings.ToLower(operator))
	if operator == "==" {
		return "="
	}
	return operator
}

func validKitDBCheckOperator(operator string) bool {
	switch normalizeKitDBCheckOperator(operator) {
	case "=", "!=", "<>", ">", ">=", "<", "<=":
		return true
	default:
		return false
	}
}

func validKitDBCheckLiteral(item value.Value) bool {
	if item.IsNil() {
		return true
	}
	switch item.K {
	case value.Number, value.Time, value.Duration:
		return !math.IsNaN(item.N) && !math.IsInf(item.N, 0)
	case value.Bool, value.String, value.Bytes:
		return true
	default:
		return false
	}
}

func appendColumnCheckConstraints(definition *StructDef) error {
	if definition == nil {
		return fmt.Errorf("check constraints are unavailable")
	}
	seen := make(map[string]string, len(definition.CheckConstraints))
	for _, constraint := range definition.CheckConstraints {
		seen[strings.ToLower(constraint.Name)] = constraint.Name
	}
	for _, field := range definition.Fields {
		spec := definition.columns[field.Name]
		if spec == nil {
			continue
		}
		for index, check := range spec.checks {
			name := check.name
			if name == "" {
				name = fmt.Sprintf("check_%s_%s_%d", definition.Name, field.Name, index+1)
			}
			key := strings.ToLower(name)
			if previous := seen[key]; previous != "" {
				return fmt.Errorf("check constraints %q and %q share a name", previous, name)
			}
			seen[key] = name
			definition.CheckConstraints = append(definition.CheckConstraints, StructCheckConstraint{
				Version: 1,
				ID:      stableSchemaID("check", definition.ID+":"+key),
				Name:    name,
				Expression: StructCheckExpression{
					Kind: "binary", Operator: normalizeKitDBCheckOperator(check.operator),
					Arguments: []StructCheckExpression{
						{Kind: "field", Field: field.Tag},
						{Kind: "literal", Literal: coerceRead(spec.kind, coerceWrite(spec.kind, check.value))},
					},
				},
			})
		}
	}
	sort.Slice(definition.CheckConstraints, func(left, right int) bool {
		return strings.ToLower(definition.CheckConstraints[left].Name) < strings.ToLower(definition.CheckConstraints[right].Name)
	})
	return nil
}

func cloneStructCheckConstraints(source []StructCheckConstraint) []StructCheckConstraint {
	cloned := make([]StructCheckConstraint, len(source))
	for index, constraint := range source {
		cloned[index] = constraint
		cloned[index].Expression = cloneStructCheckExpression(constraint.Expression)
	}
	return cloned
}

func cloneStructCheckExpression(source StructCheckExpression) StructCheckExpression {
	cloned := source
	cloned.Arguments = make([]StructCheckExpression, len(source.Arguments))
	for index := range source.Arguments {
		cloned.Arguments[index] = cloneStructCheckExpression(source.Arguments[index])
	}
	if source.CaseBase != nil {
		base := cloneStructCheckExpression(*source.CaseBase)
		cloned.CaseBase = &base
	}
	cloned.Branches = make([]StructCheckCaseBranch, len(source.Branches))
	for index, branch := range source.Branches {
		cloned.Branches[index] = StructCheckCaseBranch{
			When: cloneStructCheckExpression(branch.When),
			Then: cloneStructCheckExpression(branch.Then),
		}
	}
	if source.Fallback != nil {
		fallback := cloneStructCheckExpression(*source.Fallback)
		cloned.Fallback = &fallback
	}
	return cloned
}

func remapStructCheckFieldTags(
	expression *StructCheckExpression,
	declaredNames map[uint32]string,
	currentTags map[string]uint32,
) error {
	if expression == nil {
		return nil
	}
	if expression.Kind == "field" {
		name := declaredNames[expression.Field]
		tag, found := currentTags[name]
		if name == "" || !found {
			return fmt.Errorf("check expression lost field tag %d during reconciliation", expression.Field)
		}
		expression.Field = tag
	}
	for index := range expression.Arguments {
		if err := remapStructCheckFieldTags(&expression.Arguments[index], declaredNames, currentTags); err != nil {
			return err
		}
	}
	if err := remapStructCheckFieldTags(expression.CaseBase, declaredNames, currentTags); err != nil {
		return err
	}
	for index := range expression.Branches {
		if err := remapStructCheckFieldTags(&expression.Branches[index].When, declaredNames, currentTags); err != nil {
			return err
		}
		if err := remapStructCheckFieldTags(&expression.Branches[index].Then, declaredNames, currentTags); err != nil {
			return err
		}
	}
	return remapStructCheckFieldTags(expression.Fallback, declaredNames, currentTags)
}

func structCheckExpressionReferencesField(expression StructCheckExpression, tag uint32) bool {
	if expression.Kind == "field" && expression.Field == tag {
		return true
	}
	for _, argument := range expression.Arguments {
		if structCheckExpressionReferencesField(argument, tag) {
			return true
		}
	}
	if expression.CaseBase != nil && structCheckExpressionReferencesField(*expression.CaseBase, tag) {
		return true
	}
	for _, branch := range expression.Branches {
		if structCheckExpressionReferencesField(branch.When, tag) ||
			structCheckExpressionReferencesField(branch.Then, tag) {
			return true
		}
	}
	return expression.Fallback != nil && structCheckExpressionReferencesField(*expression.Fallback, tag)
}
