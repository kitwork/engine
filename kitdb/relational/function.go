package relational

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	kitdbengine "github.com/kitwork/engine/kitdb"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

type storedFunction struct {
	Version int    `json:"version"`
	ID      string `json:"id"`
	Name    string `json:"name"`
	Hash    string `json:"hash"`
	kitdbsql.FunctionDefinition
}

type boundFunction struct {
	definition *kitdbsql.FunctionDefinition
	body       *boundPredicate
}

const maximumFunctionTextBytes = 1 << 20

func functionSchema(definition *kitdbsql.FunctionDefinition) kitdbsql.Schema {
	schema := kitdbsql.Schema{Name: "function arguments"}
	for _, parameter := range definition.Parameters {
		schema.Fields = append(schema.Fields, kitdbsql.Field{
			Name: parameter.Name, Kind: parameter.Kind, ExactUUID: parameter.Kind == "uuid",
		})
	}
	return schema
}

func functionHash(definition storedFunction) string {
	definition.Hash = ""
	encoded, _ := json.Marshal(definition)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func decodeStoredFunction(entry kitdbengine.CatalogFunction) (*storedFunction, error) {
	var definition storedFunction
	decoder := json.NewDecoder(bytes.NewReader(entry.Definition))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&definition); err != nil {
		return nil, fmt.Errorf("kitdb: invalid function catalog: %w", err)
	}
	if definition.Version != 1 || definition.ID != entry.ID || definition.Name != entry.Name ||
		definition.Hash != entry.Hash || definition.Hash != functionHash(definition) {
		return nil, fmt.Errorf("kitdb: invalid function catalog identity/hash")
	}
	if err := validateFunction(definition.Name, &definition.FunctionDefinition); err != nil {
		return nil, err
	}
	return &definition, nil
}

func builtinScalarFunction(name string) bool {
	switch name {
	case "coalesce", "nullif", "lower", "upper", "trim", "ltrim", "rtrim", "length", "abs", "round",
		"date_trunc", "date_part",
		"in", "not in", "between", "not between":
		return true
	}
	return false
}

func validateFunction(name string, definition *kitdbsql.FunctionDefinition) error {
	if name == "" || len(name) > 128 || name != strings.ToLower(name) || builtinScalarFunction(name) {
		return fmt.Errorf("kitdb SQL: invalid or reserved function name %q", name)
	}
	switch name {
	case "count", "sum", "avg", "min", "max", "now", "current_timestamp", "current_date", "current_time", "localtimestamp", "random",
		"nextval", "currval", "setval", "lastval",
		"version", "current_database", "current_schema", "current_user", "session_user", "null", "true", "false":
		return fmt.Errorf("kitdb SQL: reserved function name %q", name)
	}
	if !kitdbsql.SupportedFunctionKind(definition.ReturnKind) || len(definition.Parameters) > 16 {
		return fmt.Errorf("kitdb SQL: unsupported function signature")
	}
	parameters := make(map[string]string, len(definition.Parameters))
	for _, parameter := range definition.Parameters {
		if parameter.Name == "" || len(parameter.Name) > 128 || strings.Contains(parameter.Name, ".") ||
			!kitdbsql.SupportedFunctionKind(parameter.Kind) || parameters[parameter.Name] != "" {
			return fmt.Errorf("kitdb SQL: invalid function parameter %q", parameter.Name)
		}
		parameters[parameter.Name] = parameter.Kind
	}
	if err := kitdbsql.ValidateExpression(definition.Body); err != nil {
		return err
	}
	kind, err := pureFunctionExpressionKind(&definition.Body, parameters)
	if err != nil {
		return err
	}
	if !functionKindAssignable(kind, definition.ReturnKind) {
		return fmt.Errorf("kitdb SQL: function returns %s, declared %s", kind, definition.ReturnKind)
	}
	return nil
}

func functionKindAssignable(from, to string) bool {
	return from == "" || from == to ||
		isIntegerExpressionKind(from) && (isIntegerExpressionKind(to) || to == "float" || to == "decimal")
}

// The first profile admits only bounded, pure expressions over parameters and
// reviewed built-ins. No SQL reads, clock, UDF dependencies or recursion.
func pureFunctionExpressionKind(node *kitdbsql.ExpressionPlan, parameters map[string]string) (string, error) {
	if node.Function != nil {
		return "", fmt.Errorf("kitdb SQL: function dependencies are not supported yet")
	}
	invalid := func() (string, error) {
		return "", fmt.Errorf("kitdb SQL: invalid pure function expression %q", node.Operator)
	}
	switch node.Kind {
	case "field":
		kind, ok := parameters[strings.ToLower(node.Field)]
		if !ok {
			return "", fmt.Errorf("kitdb SQL: unknown function parameter %q", node.Field)
		}
		return kind, nil
	case "literal":
		switch node.Literal.Kind {
		case kitdbsql.LiteralNull:
			return "", nil
		case kitdbsql.LiteralString:
			return "text", nil
		case kitdbsql.LiteralBoolean:
			return "bool", nil
		case kitdbsql.LiteralNumber:
			value, err := resolveLiteral(node.Literal, nil)
			if err != nil {
				return "", err
			}
			if integer, ok := value.(int64); ok {
				return integerLiteralExpressionKind(integer), nil
			}
			return "decimal", nil
		default:
			return "", fmt.Errorf("kitdb SQL: pure functions cannot use bound SQL parameters or the clock")
		}
	}
	kinds := make([]string, len(node.Arguments))
	for i := range node.Arguments {
		kind, err := pureFunctionExpressionKind(&node.Arguments[i], parameters)
		if err != nil {
			return "", err
		}
		kinds[i] = kind
	}
	all := func(wanted string) bool {
		for _, kind := range kinds {
			if !functionKindAssignable(kind, wanted) {
				return false
			}
		}
		return true
	}
	numeric := func() bool {
		for _, kind := range kinds {
			if kind != "" && !isNumericExpressionKind(kind) {
				return false
			}
		}
		return true
	}
	switch node.Kind {
	case "unary":
		if len(kinds) != 1 {
			return invalid()
		}
		switch node.Operator {
		case "is null", "is not null":
			return "bool", nil
		case "not":
			if all("bool") {
				return "bool", nil
			}
		case "+", "-":
			if numeric() || kinds[0] == "interval" {
				return kinds[0], nil
			}
		}
	case "binary":
		if len(kinds) != 2 {
			return invalid()
		}
		switch node.Operator {
		case "and", "or":
			if all("bool") {
				return "bool", nil
			}
		case "||":
			if all("text") {
				return "text", nil
			}
		case "=", "!=", "<>", "<", "<=", ">", ">=":
			if kinds[0] == kinds[1] || kinds[0] == "" || kinds[1] == "" || numeric() {
				return "bool", nil
			}
		case "+", "-", "*", "/", "%":
			if result, temporal := temporalArithmeticResultKind(node.Operator, kinds[0], kinds[1]); temporal {
				if result != "" {
					return result, nil
				}
				return invalid()
			}
			if numeric() {
				if kinds[0] == "float" || kinds[1] == "float" {
					return "float", nil
				}
				if kinds[0] == "decimal" || kinds[1] == "decimal" {
					return "decimal", nil
				}
				if node.Operator == "/" {
					return "float", nil
				}
				return integerExpressionResultKind(kinds[0], kinds[1]), nil
			}
		}
	case "function":
		switch node.Operator {
		case "lower", "upper", "trim", "ltrim", "rtrim":
			if len(kinds) == 1 && all("text") {
				return "text", nil
			}
		case "length":
			if len(kinds) == 1 && all("text") {
				return "int32", nil
			}
		case "abs":
			if len(kinds) == 1 && numeric() {
				return kinds[0], nil
			}
		case "round":
			if (len(kinds) == 1 || len(kinds) == 2 && functionKindAssignable(kinds[1], "integer")) && numeric() {
				if kinds[0] == "decimal" {
					return "decimal", nil
				}
				return "float", nil
			}
		case "date_trunc":
			if len(kinds) == 2 && functionKindAssignable(kinds[0], "text") && exactTemporalFieldKind(kinds[1]) {
				return kinds[1], nil
			}
		case "date_part":
			if len(kinds) == 2 && functionKindAssignable(kinds[0], "text") && exactTemporalFieldKind(kinds[1]) {
				return "float", nil
			}
		case "coalesce", "nullif":
			if len(kinds) == 0 || node.Operator == "nullif" && len(kinds) != 2 {
				return invalid()
			}
			kind := ""
			for _, candidate := range kinds {
				if candidate == "" {
					continue
				}
				if kind != "" && kind != candidate {
					if isDecimalExpressionKind(kind) && isIntegerExpressionKind(candidate) ||
						isIntegerExpressionKind(kind) && isDecimalExpressionKind(candidate) {
						kind = "decimal"
						continue
					}
					if !isIntegerExpressionKind(kind) || !isIntegerExpressionKind(candidate) {
						return invalid()
					}
					kind = integerExpressionResultKind(kind, candidate)
					continue
				}
				kind = candidate
			}
			return kind, nil
		}
	}
	return invalid()
}

func (engine *Engine) executeFunctionDDL(ctx context.Context, create *kitdbsql.CreateFunctionStatement, drop *kitdbsql.DropFunctionStatement) (Result, error) {
	engine.mu.RLock()
	defer engine.mu.RUnlock()
	if err := engine.readyLocked(); err != nil {
		return Result{}, err
	}
	engine.writeMu.Lock()
	defer engine.writeMu.Unlock()
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	catalog, err := engine.database.Catalog()
	if err != nil {
		return Result{}, err
	}
	name := ""
	if create != nil {
		name = create.Name
	} else {
		name = drop.Name
	}
	var existing *storedFunction
	for _, entry := range catalog.Functions {
		if entry.Name != name {
			continue
		}
		existing, err = decodeStoredFunction(entry)
		if err != nil {
			return Result{}, err
		}
		break
	}
	var definition []byte
	command := "CREATE FUNCTION"
	if create != nil {
		if err := validateFunction(name, &create.FunctionDefinition); err != nil {
			return Result{}, err
		}
		if existing != nil {
			if !create.Replace {
				return Result{}, fmt.Errorf("kitdb SQL: function %q already exists", name)
			}
			oldSignature, _ := json.Marshal(existing.Parameters)
			newSignature, _ := json.Marshal(create.Parameters)
			if !bytes.Equal(oldSignature, newSignature) || existing.ReturnKind != create.ReturnKind {
				return Result{}, fmt.Errorf("kitdb SQL: OR REPLACE cannot change a function signature")
			}
		}
		stored := storedFunction{Version: 1, ID: kitdbsql.StableSchemaID("function", name), Name: name, FunctionDefinition: create.FunctionDefinition}
		stored.Hash = functionHash(stored)
		definition, err = json.Marshal(stored)
		if err != nil {
			return Result{}, err
		}
	} else {
		command = "DROP FUNCTION"
		if existing != nil && drop.HasSignature {
			if len(drop.ArgumentKinds) != len(existing.Parameters) {
				existing = nil
			} else {
				for i, kind := range drop.ArgumentKinds {
					if kind != existing.Parameters[i].Kind {
						existing = nil
						break
					}
				}
			}
		}
		if existing == nil {
			if drop.IfExists {
				return Result{CommandTag: command}, nil
			}
			return Result{}, fmt.Errorf("kitdb SQL: function %q with this signature does not exist", name)
		}
	}
	tx, err := engine.database.Begin()
	if err != nil {
		return Result{}, err
	}
	defer tx.Rollback()
	if create != nil {
		err = tx.DefineFunction(definition)
	} else {
		err = tx.DeleteFunction(existing.ID)
	}
	if err != nil {
		return Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if _, err := tx.Commit(); err != nil {
		return Result{}, err
	}
	return Result{CommandTag: command}, nil
}

// Bind call definitions once per statement using only the captured catalog.
// Mutating a function later cannot change an already-running transaction.
func resolveStatementFunctions(statement *kitdbsql.ParsedStatement, catalog kitdbengine.CatalogSnapshot) error {
	loaded := make(map[string]*storedFunction)
	var visit func(*kitdbsql.ExpressionPlan) error
	count := 0
	visit = func(node *kitdbsql.ExpressionPlan) error {
		if node == nil {
			return nil
		}
		for i := range node.Arguments {
			if err := visit(&node.Arguments[i]); err != nil {
				return err
			}
		}
		if node.Kind != "function" || builtinScalarFunction(node.Operator) {
			return nil
		}
		count++
		if count > 64 {
			return fmt.Errorf("kitdb SQL: statement exceeds 64 user function calls")
		}
		definition := loaded[node.Operator]
		if definition == nil {
			for _, entry := range catalog.Functions {
				if entry.Name != node.Operator {
					continue
				}
				var err error
				definition, err = decodeStoredFunction(entry)
				if err != nil {
					return err
				}
				loaded[node.Operator] = definition
				break
			}
		}
		if definition == nil {
			return fmt.Errorf("kitdb SQL: unknown function %q", node.Operator)
		}
		if len(node.Arguments) != len(definition.Parameters) {
			return fmt.Errorf("kitdb SQL: %s expects %d arguments", node.Operator, len(definition.Parameters))
		}
		node.Function = &definition.FunctionDefinition
		return nil
	}
	selectPlan := statement.Select
	if statement.Explain != nil {
		selectPlan = statement.Explain
	}
	var visitSelect func(*kitdbsql.SelectStatement) error
	visitSelect = func(plan *kitdbsql.SelectStatement) error {
		if plan == nil {
			return nil
		}
		before := count
		for i := range plan.Projection {
			if err := visit(plan.Projection[i].Expression); err != nil {
				return err
			}
		}
		for i := range plan.Order {
			if err := visit(plan.Order[i].Expression); err != nil {
				return err
			}
		}
		if err := visit(plan.Predicate); err != nil {
			return err
		}
		if err := visit(plan.Having); err != nil {
			return err
		}
		if count > before && (len(plan.Joins) != 0 || selectHasAggregates(plan) || plan.Search != nil) {
			return fmt.Errorf("kitdb SQL: user functions with JOIN, aggregates or SEARCH are not supported yet")
		}
		if err := visitSelect(plan.Source); err != nil {
			return err
		}
		for _, commonTable := range plan.CommonTables {
			if err := visitSelect(commonTable.Select); err != nil {
				return err
			}
		}
		for _, operation := range plan.SetOperations {
			if err := visitSelect(operation.Select); err != nil {
				return err
			}
		}
		return nil
	}
	if err := visitSelect(selectPlan); err != nil {
		return err
	}
	if plan := statement.Update; plan != nil {
		for i := range plan.Assignments {
			if err := visit(plan.Assignments[i].Expression); err != nil {
				return err
			}
		}
		if err := visit(plan.Predicate); err != nil {
			return err
		}
	}
	if plan := statement.Delete; plan != nil {
		if err := visit(plan.Predicate); err != nil {
			return err
		}
	}
	return nil
}

func bindUserFunction(plan *kitdbsql.ExpressionPlan) (*boundFunction, error) {
	definition := plan.Function
	if definition == nil {
		return nil, nil
	}
	if len(plan.Arguments) != len(definition.Parameters) {
		return nil, fmt.Errorf("kitdb SQL: invalid function arguments")
	}
	body, err := bindPredicate(functionSchema(definition), &definition.Body, nil)
	if err != nil {
		return nil, err
	}
	var boundText func(*boundPredicate)
	boundText = func(node *boundPredicate) {
		node.maximumTextBytes = maximumFunctionTextBytes
		for _, child := range node.arguments {
			boundText(child)
		}
	}
	boundText(body)
	return &boundFunction{definition: definition, body: body}, nil
}

func evaluateUserFunction(function *boundFunction, arguments []any) (any, error) {
	row := make(map[string]any, len(arguments))
	for i, value := range arguments {
		parameter := function.definition.Parameters[i]
		coerced, err := coerceFunctionValue(parameter.Kind, value)
		if err != nil {
			return nil, fmt.Errorf("kitdb SQL: function argument %s: %w", parameter.Name, err)
		}
		row[parameter.Name] = coerced
	}
	value, err := evaluateBoundPredicate(row, function.body)
	if err != nil {
		return nil, err
	}
	return coerceFunctionValue(function.definition.ReturnKind, value)
}

func coerceFunctionValue(kind string, value any) (any, error) {
	if value == nil {
		return nil, nil
	}
	field := kitdbsql.Field{Name: "function value", Kind: kind, ExactUUID: kind == "uuid"}
	if kind == "text" {
		text, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("expected text")
		}
		if len(text) > maximumFunctionTextBytes {
			return nil, fmt.Errorf("kitdb SQL: function text exceeds %d bytes", maximumFunctionTextBytes)
		}
	}
	coerced, err := coerceField(field, value)
	if err != nil {
		return nil, err
	}
	return readField(field, coerced), nil
}

func executeConstantSelect(plan *kitdbsql.SelectStatement, parameters []any) (Result, error) {
	if plan.Table != "" || len(plan.Projection) == 0 {
		return Result{}, fmt.Errorf("kitdb SQL: invalid scalar SELECT")
	}
	for _, projection := range plan.Projection {
		if projection.All || projection.Count || projection.Aggregate != "" {
			return Result{}, fmt.Errorf("kitdb SQL: scalar SELECT requires expressions")
		}
	}
	projections, err := bindScalarProjections(kitdbsql.Schema{}, plan.Projection, parameters)
	if err != nil {
		return Result{}, err
	}
	result := Result{Rows: [][]any{make([]any, len(projections))}}
	for i, projection := range projections {
		result.Columns = append(result.Columns, projection.column)
		value, err := evaluateBoundPredicate(nil, projection.expression)
		if err != nil {
			return Result{}, err
		}
		value, err = coerceScalarExpressionValue(projection.column.Kind, value)
		if err != nil {
			return Result{}, fmt.Errorf("kitdb SQL: projection %d: %w", i+1, err)
		}
		result.Rows[0][i] = value
	}
	if plan.Offset != 0 || plan.HasLimit && plan.Limit == 0 {
		result.Rows = result.Rows[:0]
	}
	result.CommandTag = fmt.Sprintf("SELECT %d", len(result.Rows))
	return result, nil
}
