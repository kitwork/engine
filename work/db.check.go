package work

import (
	"fmt"
	"math"
	"reflect"
	"sort"
	"strings"

	"github.com/kitwork/engine/value"
)

const kitDBMaximumCheckConstraints = 64

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

type compiledStructCheck struct {
	name       string
	expression *kitSQLExpression
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

func validateStructCheckConstraints(definition *StructDef) error {
	_, err := compileStructCheckConstraints(definition)
	return err
}

func prepareStructCheckConstraints(definition *StructDef) error {
	compiled, err := compileStructCheckConstraints(definition)
	if err != nil {
		return err
	}
	definition.compiledChecks = compiled
	return nil
}

func compileStructCheckConstraints(definition *StructDef) ([]compiledStructCheck, error) {
	if definition == nil {
		return nil, fmt.Errorf("check constraints are unavailable")
	}
	if definition.constraintErr != "" {
		return nil, fmt.Errorf("%s", definition.constraintErr)
	}
	if len(definition.CheckConstraints) > kitDBMaximumCheckConstraints {
		return nil, fmt.Errorf("struct %q exceeds %d check constraints", definition.Name, kitDBMaximumCheckConstraints)
	}
	compiled := make([]compiledStructCheck, 0, len(definition.CheckConstraints))
	seen := make(map[string]string, len(definition.CheckConstraints))
	previous := ""
	for _, constraint := range definition.CheckConstraints {
		name := strings.TrimSpace(constraint.Name)
		key := strings.ToLower(name)
		if constraint.Version != 1 {
			return nil, fmt.Errorf("check constraint %q uses unsupported version %d", constraint.Name, constraint.Version)
		}
		if name == "" || name != constraint.Name {
			return nil, fmt.Errorf("check constraint has an invalid name %q", constraint.Name)
		}
		if previous != "" && key <= previous {
			return nil, fmt.Errorf("check constraints are not strictly ordered by name")
		}
		previous = key
		if duplicate := seen[key]; duplicate != "" {
			return nil, fmt.Errorf("check constraints %q and %q share a name", duplicate, constraint.Name)
		}
		seen[key] = constraint.Name
		if constraint.ID != stableSchemaID("check", definition.ID+":"+key) {
			return nil, fmt.Errorf("check constraint %q has an invalid identity", constraint.Name)
		}
		expression, resultKind, err := compileStructCheckExpression(definition, constraint.Expression)
		if err != nil {
			return nil, fmt.Errorf("check constraint %q: %w", constraint.Name, err)
		}
		if resultKind != "bool" && resultKind != "null" && !kitSQLNumericKind(resultKind) {
			return nil, fmt.Errorf("check constraint %q must produce a boolean or numeric result, got %s", constraint.Name, resultKind)
		}
		compiled = append(compiled, compiledStructCheck{name: constraint.Name, expression: expression})
	}
	return compiled, nil
}

func compileStructCheckExpression(
	definition *StructDef,
	stored StructCheckExpression,
) (*kitSQLExpression, string, error) {
	expression, err := kitSQLExpressionFromStructCheck(definition, stored)
	if err != nil {
		return nil, "", err
	}
	if err := validateKitSQLExpression(expression); err != nil {
		return nil, "", err
	}
	if kitSQLExpressionContainsAggregate(expression) {
		return nil, "", fmt.Errorf("aggregate expressions are not valid in CHECK")
	}
	result, err := resolveKitSQLExpression(expression, func(requested string) (string, string, error) {
		field, found := kitDBField(definition, requested)
		if !found {
			return "", "", fmt.Errorf("references missing field %q", requested)
		}
		return field.Name, field.Kind, nil
	})
	return expression, result, err
}

func kitSQLExpressionFromStructCheck(
	definition *StructDef,
	stored StructCheckExpression,
) (*kitSQLExpression, error) {
	expression := &kitSQLExpression{operator: strings.ToLower(stored.Operator)}
	switch stored.Kind {
	case "literal":
		if !validKitDBCheckLiteral(stored.Literal) {
			return nil, fmt.Errorf("contains a non-scalar literal")
		}
		expression.kind, expression.literal = kitSQLExpressionLiteral, stored.Literal
	case "field":
		index, found := definition.byTag[stored.Field]
		if !found {
			return nil, fmt.Errorf("references missing field tag %d", stored.Field)
		}
		expression.kind, expression.reference = kitSQLExpressionReference, definition.Fields[index].Name
	case "unary":
		expression.kind = kitSQLExpressionUnary
	case "binary":
		expression.kind = kitSQLExpressionBinary
	case "function":
		expression.kind = kitSQLExpressionFunction
	case "case":
		expression.kind = kitSQLExpressionCase
	default:
		return nil, fmt.Errorf("contains unsupported expression kind %q", stored.Kind)
	}
	for _, argument := range stored.Arguments {
		converted, err := kitSQLExpressionFromStructCheck(definition, argument)
		if err != nil {
			return nil, err
		}
		expression.arguments = append(expression.arguments, converted)
	}
	if stored.CaseBase != nil {
		converted, err := kitSQLExpressionFromStructCheck(definition, *stored.CaseBase)
		if err != nil {
			return nil, err
		}
		expression.caseBase = converted
	}
	for _, branch := range stored.Branches {
		when, err := kitSQLExpressionFromStructCheck(definition, branch.When)
		if err != nil {
			return nil, err
		}
		then, err := kitSQLExpressionFromStructCheck(definition, branch.Then)
		if err != nil {
			return nil, err
		}
		expression.branches = append(expression.branches, kitSQLCaseBranch{when: when, then: then})
	}
	if stored.Fallback != nil {
		converted, err := kitSQLExpressionFromStructCheck(definition, *stored.Fallback)
		if err != nil {
			return nil, err
		}
		expression.fallback = converted
	}
	return expression, nil
}

func structCheckExpressionFromKitSQL(
	definition *StructDef,
	expression *kitSQLExpression,
) (StructCheckExpression, error) {
	if expression == nil {
		return StructCheckExpression{}, fmt.Errorf("expression is unavailable")
	}
	stored := StructCheckExpression{Operator: strings.ToLower(expression.operator)}
	switch expression.kind {
	case kitSQLExpressionLiteral:
		stored.Kind, stored.Literal = "literal", expression.literal
	case kitSQLExpressionReference:
		field, found := kitDBField(definition, expression.reference)
		if !found {
			return StructCheckExpression{}, fmt.Errorf("references missing field %q", expression.reference)
		}
		stored.Kind, stored.Field = "field", field.Tag
	case kitSQLExpressionUnary:
		stored.Kind = "unary"
	case kitSQLExpressionBinary:
		stored.Kind = "binary"
	case kitSQLExpressionFunction:
		stored.Kind = "function"
	case kitSQLExpressionCase:
		stored.Kind = "case"
	default:
		return StructCheckExpression{}, fmt.Errorf("contains unsupported expression node %d", expression.kind)
	}
	for _, argument := range expression.arguments {
		converted, err := structCheckExpressionFromKitSQL(definition, argument)
		if err != nil {
			return StructCheckExpression{}, err
		}
		stored.Arguments = append(stored.Arguments, converted)
	}
	if expression.caseBase != nil {
		converted, err := structCheckExpressionFromKitSQL(definition, expression.caseBase)
		if err != nil {
			return StructCheckExpression{}, err
		}
		stored.CaseBase = &converted
	}
	for _, branch := range expression.branches {
		when, err := structCheckExpressionFromKitSQL(definition, branch.when)
		if err != nil {
			return StructCheckExpression{}, err
		}
		then, err := structCheckExpressionFromKitSQL(definition, branch.then)
		if err != nil {
			return StructCheckExpression{}, err
		}
		stored.Branches = append(stored.Branches, StructCheckCaseBranch{When: when, Then: then})
	}
	if expression.fallback != nil {
		converted, err := structCheckExpressionFromKitSQL(definition, expression.fallback)
		if err != nil {
			return StructCheckExpression{}, err
		}
		stored.Fallback = &converted
	}
	return stored, nil
}

func appendKitSQLCheckConstraints(definition *StructDef, checks []kitSQLDDLCheck) error {
	if len(checks) == 0 {
		return nil
	}
	if len(definition.CheckConstraints)+len(checks) > kitDBMaximumCheckConstraints {
		return fmt.Errorf("kitdb SQL: CREATE/ALTER exceeds %d CHECK constraints", kitDBMaximumCheckConstraints)
	}
	seen := make(map[string]string, len(definition.CheckConstraints)+len(checks))
	for _, constraint := range definition.CheckConstraints {
		seen[strings.ToLower(constraint.Name)] = constraint.Name
	}
	for index, check := range checks {
		if check.expression == nil {
			return fmt.Errorf("kitdb SQL: CHECK expression is unavailable")
		}
		if kitSQLExpressionContainsAggregate(check.expression) {
			return fmt.Errorf("kitdb SQL: aggregate expressions are not valid in CHECK")
		}
		resultKind, err := resolveKitSQLExpression(check.expression, func(requested string) (string, string, error) {
			name, field, err := kitDBRemoteField(definition, requested)
			return name, field.Kind, err
		})
		if err != nil {
			return fmt.Errorf("kitdb SQL: CHECK: %w", err)
		}
		if resultKind != "bool" && resultKind != "null" && !kitSQLNumericKind(resultKind) {
			return fmt.Errorf("kitdb SQL: CHECK must produce a boolean or numeric result, got %s", resultKind)
		}
		stored, err := structCheckExpressionFromKitSQL(definition, check.expression)
		if err != nil {
			return fmt.Errorf("kitdb SQL: CHECK: %w", err)
		}
		name := check.name
		if name == "" {
			if check.column != "" {
				name = fmt.Sprintf("check_%s_%s_%d", definition.Name, check.column, index+1)
			} else {
				name = fmt.Sprintf("check_%s_%d", definition.Name, index+1)
			}
		}
		key := strings.ToLower(name)
		if previous := seen[key]; previous != "" {
			return fmt.Errorf("kitdb SQL: CHECK constraints %q and %q share a name", previous, name)
		}
		seen[key] = name
		definition.CheckConstraints = append(definition.CheckConstraints, StructCheckConstraint{
			Version: 1, ID: stableSchemaID("check", definition.ID+":"+key), Name: name, Expression: stored,
		})
	}
	sort.Slice(definition.CheckConstraints, func(left, right int) bool {
		return strings.ToLower(definition.CheckConstraints[left].Name) < strings.ToLower(definition.CheckConstraints[right].Name)
	})
	refreshStructHash(definition)
	return prepareStructCheckConstraints(definition)
}

func validateKitDBChecks(definition *StructDef, row map[string]value.Value) error {
	if definition == nil || len(definition.CheckConstraints) == 0 {
		return nil
	}
	if len(definition.compiledChecks) != len(definition.CheckConstraints) {
		return fmt.Errorf("kitdb: struct %q CHECK plan is not prepared", definition.Name)
	}
	for _, check := range definition.compiledChecks {
		result, err := evaluateKitSQLExpression(check.expression, func(requested string) (value.Value, error) {
			spec := definition.columns[requested]
			if spec == nil {
				return value.Value{}, fmt.Errorf("unresolved CHECK field %q", requested)
			}
			item, found := row[requested]
			if !found {
				return value.NewNil(), nil
			}
			return coerceRead(spec.kind, item), nil
		})
		if err != nil {
			return fmt.Errorf("kitdb: struct %q check %q: %w", definition.Name, check.name, err)
		}
		truth, err := kitSQLExpressionTruth(result)
		if err != nil {
			return fmt.Errorf("kitdb: struct %q check %q: %w", definition.Name, check.name, err)
		}
		if truth == kitDBFalse {
			return fmt.Errorf("kitdb: struct %q check constraint %q failed", definition.Name, check.name)
		}
	}
	return nil
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

func renderStructCheckExpression(definition *StructDef, expression StructCheckExpression) (string, error) {
	render := func(child StructCheckExpression) (string, error) {
		return renderStructCheckExpression(definition, child)
	}
	switch expression.Kind {
	case "literal":
		return sqlLiteral(expression.Literal), nil
	case "field":
		index, found := definition.byTag[expression.Field]
		if !found {
			return "", fmt.Errorf("missing field tag %d", expression.Field)
		}
		return fmt.Sprintf("%q", definition.Fields[index].Name), nil
	case "unary":
		if len(expression.Arguments) != 1 {
			return "", fmt.Errorf("unary CHECK expression needs one argument")
		}
		child, err := render(expression.Arguments[0])
		if err != nil {
			return "", err
		}
		switch expression.Operator {
		case "is null", "is not null":
			return "(" + child + " " + strings.ToUpper(expression.Operator) + ")", nil
		case "not":
			return "(NOT " + child + ")", nil
		default:
			return "(" + expression.Operator + child + ")", nil
		}
	case "binary":
		if len(expression.Arguments) != 2 {
			return "", fmt.Errorf("binary CHECK expression needs two arguments")
		}
		left, err := render(expression.Arguments[0])
		if err != nil {
			return "", err
		}
		right, err := render(expression.Arguments[1])
		if err != nil {
			return "", err
		}
		return "(" + left + " " + strings.ToUpper(expression.Operator) + " " + right + ")", nil
	case "function":
		arguments := make([]string, len(expression.Arguments))
		for index, argument := range expression.Arguments {
			item, err := render(argument)
			if err != nil {
				return "", err
			}
			arguments[index] = item
		}
		switch expression.Operator {
		case "in", "not in":
			return "(" + arguments[0] + " " + strings.ToUpper(expression.Operator) + " (" + strings.Join(arguments[1:], ", ") + "))", nil
		case "between", "not between":
			return "(" + arguments[0] + " " + strings.ToUpper(expression.Operator) + " " + arguments[1] + " AND " + arguments[2] + ")", nil
		default:
			return strings.ToUpper(expression.Operator) + "(" + strings.Join(arguments, ", ") + ")", nil
		}
	case "case":
		parts := []string{"CASE"}
		if expression.CaseBase != nil {
			base, err := render(*expression.CaseBase)
			if err != nil {
				return "", err
			}
			parts = append(parts, base)
		}
		for _, branch := range expression.Branches {
			when, err := render(branch.When)
			if err != nil {
				return "", err
			}
			then, err := render(branch.Then)
			if err != nil {
				return "", err
			}
			parts = append(parts, "WHEN", when, "THEN", then)
		}
		if expression.Fallback != nil {
			fallback, err := render(*expression.Fallback)
			if err != nil {
				return "", err
			}
			parts = append(parts, "ELSE", fallback)
		}
		parts = append(parts, "END")
		return "(" + strings.Join(parts, " ") + ")", nil
	default:
		return "", fmt.Errorf("unsupported CHECK expression kind %q", expression.Kind)
	}
}

func formatStructCheckConstraints(definition *StructDef) string {
	if definition == nil || len(definition.CheckConstraints) == 0 {
		return ""
	}
	parts := make([]string, 0, len(definition.CheckConstraints))
	for _, constraint := range definition.CheckConstraints {
		rendered, err := renderStructCheckExpression(definition, constraint.Expression)
		if err != nil {
			rendered = "invalid"
		}
		parts = append(parts, constraint.Name+":"+rendered)
	}
	return strings.Join(parts, ",")
}

func structCheckConstraintsNeedValidation(stored, current *StructDef) bool {
	previous := make(map[string]StructCheckConstraint, len(stored.CheckConstraints))
	for _, constraint := range stored.CheckConstraints {
		previous[constraint.ID] = constraint
	}
	for _, constraint := range current.CheckConstraints {
		if old, found := previous[constraint.ID]; !found || !structCheckConstraintEqual(old, constraint) {
			return true
		}
	}
	return false
}

func structCheckConstraintEqual(left, right StructCheckConstraint) bool {
	return reflect.DeepEqual(left, right)
}
