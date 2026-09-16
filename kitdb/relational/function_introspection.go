package relational

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/kitwork/engine/kitdb/pgwire"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

func functionCatalogHelper(name string) bool {
	switch strings.ToLower(name) {
	case "pg_get_functiondef", "pg_get_function_arguments", "pg_get_function_identity_arguments", "pg_get_function_result", "pg_get_userbyid",
		"pg_get_triggerdef", "pg_get_constraintdef", "kitdb_get_domaindef", "kitdb_get_sequencedef":
		return true
	}
	return false
}

// Recognize SELECT helpers without FROM by tokens, never by matching strings
// inside user literals or CREATE FUNCTION bodies.
func scalarFunctionCatalogSelect(source string) (string, bool) {
	tokens, err := kitdbsql.Lex(source)
	if err != nil || len(tokens) < 4 || !strings.EqualFold(tokens[0].Text, "select") {
		return "", false
	}
	found := false
	end := tokens[len(tokens)-1].Start
	for i := 1; i < len(tokens)-1; i++ {
		if tokens[i].Kind == kitdbsql.TokenIdentifier {
			switch strings.ToLower(tokens[i].Text) {
			case "from", "where", "union", "limit", "order":
				return "", false
			}
			if functionCatalogHelper(tokens[i].Text) && tokens[i+1].Text == "(" {
				found = true
			}
		}
		if tokens[i].Text == ";" {
			if i != len(tokens)-2 {
				return "", false
			}
			end = tokens[i].Start
		}
	}
	return strings.TrimSpace(source[tokens[0].End:end]), found
}

func compileFunctionCatalogProjection(expression string, dataset postgresCatalogDataset) (postgresCatalogProjection, bool, error) {
	var projection postgresCatalogProjection
	tokens, err := kitdbsql.Lex(expression)
	if err != nil {
		return projection, false, nil
	}
	tokens = tokens[:len(tokens)-1]
	if len(tokens) == 0 {
		return projection, false, nil
	}
	if dataset.functionCatalog != nil && tokens[0].Kind == kitdbsql.TokenIdentifier && strings.EqualFold(tokens[0].Text, "case") {
		return compileFunctionCatalogCase(tokens, dataset)
	}
	if len(tokens) >= 3 && tokens[1].Text == "." {
		if !strings.EqualFold(tokens[0].Text, "pg_catalog") {
			if functionCatalogHelper(tokens[2].Text) {
				return projection, true, fmt.Errorf("KitDB catalog helpers require pg_catalog or no qualifier")
			}
			return projection, false, nil
		}
		tokens = tokens[2:]
	}
	name := strings.ToLower(tokens[0].Text)
	if !functionCatalogHelper(name) {
		return projection, false, nil
	}
	invalid := func() (postgresCatalogProjection, bool, error) {
		return projection, true, fmt.Errorf("KitDB catalog helper %s requires one OID literal, parameter or catalog column", name)
	}
	if dataset.functionCatalog == nil || len(tokens) < 4 || tokens[1].Text != "(" {
		return invalid()
	}
	// A helper returns text. An OID cast INSIDE its argument must never change
	// the result's wire type to OID (the old lexical fallback did exactly that).
	cast := ""
	if len(tokens) >= 3 && tokens[len(tokens)-2].Text == "::" {
		cast = strings.ToLower(tokens[len(tokens)-1].Text)
		if cast != "text" && cast != "varchar" {
			return invalid()
		}
		tokens = tokens[:len(tokens)-2]
	}
	if tokens[len(tokens)-1].Text != ")" {
		return invalid()
	}
	args := tokens[2 : len(tokens)-1]
	prettyNull := false
	// PostgreSQL managers optionally request pretty printing. Both styles use
	// the same deterministic, replayable KitDB definition; other arities fail.
	if len(args) >= 3 && args[len(args)-2].Text == "," &&
		(name == "pg_get_triggerdef" || name == "pg_get_constraintdef") {
		pretty := args[len(args)-1]
		prettyNull = pretty.Kind == kitdbsql.TokenIdentifier && strings.EqualFold(pretty.Text, "null")
		if !prettyNull && ((pretty.Kind != kitdbsql.TokenIdentifier && pretty.Kind != kitdbsql.TokenString) || (!strings.EqualFold(pretty.Text, "true") && !strings.EqualFold(pretty.Text, "false"))) {
			return invalid()
		}
		args = args[:len(args)-2]
	}
	if len(args) >= 3 && args[len(args)-2].Text == "::" {
		switch strings.ToLower(args[len(args)-1].Text) {
		case "oid", "int8", "bigint", "int4", "integer":
		default:
			return invalid()
		}
		args = args[:len(args)-2]
	}
	var column string
	var constant any
	if len(args) == 1 {
		token := args[0]
		switch token.Kind {
		case kitdbsql.TokenString, kitdbsql.TokenNumber:
			constant = token.Text
		case kitdbsql.TokenIdentifier:
			if !strings.EqualFold(token.Text, "null") {
				column = strings.ToLower(token.Text)
			}
		default:
			return invalid()
		}
	} else if len(args) == 3 && args[0].Kind == kitdbsql.TokenIdentifier && args[1].Text == "." && args[2].Kind == kitdbsql.TokenIdentifier {
		column = strings.ToLower(args[2].Text)
	} else {
		return invalid()
	}
	if column != "" {
		if _, found := dataset.columns[column]; !found {
			return invalid()
		}
	}
	catalog := dataset.functionCatalog
	projection = postgresCatalogProjection{name: name, cast: cast, column: pgwire.Column{Name: name, DataTypeOID: pgwire.OIDText, DataTypeSize: -1}}
	projection.evaluate = func(row map[string]any) (any, error) {
		if prettyNull {
			return nil, nil
		}
		value := constant
		if column != "" {
			value = row[column]
		}
		if value == nil {
			return nil, nil
		}
		oid, err := strconv.ParseUint(fmt.Sprint(value), 10, 32)
		if err != nil {
			return nil, pgwire.NewError("22P02", "invalid catalog OID")
		}
		if name == "pg_get_userbyid" {
			if uint32(oid) == postgresCatalogOID("role", catalog.user) {
				return catalog.user, nil
			}
			return nil, nil
		}
		if value, handled, err := catalogObjectDefinition(name, uint32(oid), *catalog); handled {
			return value, err
		}
		var function *storedFunction
		for _, candidate := range catalog.functions {
			if postgresCatalogOID("function", candidate.ID) == uint32(oid) {
				if function != nil {
					return nil, pgwire.NewError("42725", "ambiguous KitDB function OID")
				}
				function = candidate
			}
		}
		if function == nil {
			return nil, nil
		}
		switch name {
		case "pg_get_functiondef":
			return functionDefinitionSQL(function), nil
		case "pg_get_function_arguments", "pg_get_function_identity_arguments":
			return functionArgumentsSQL(function), nil
		case "pg_get_function_result":
			kind, _ := kitdbsql.LookupKind(function.ReturnKind)
			return kind.Catalog.DataType, nil
		}
		return nil, nil
	}
	return projection, true, nil
}

// Managers classify pg_proc with searched CASE expressions. Compile each WHEN
// once, then evaluate in order; never guess a column from unsupported syntax.
func compileFunctionCatalogCase(tokens []kitdbsql.Token, dataset postgresCatalogDataset) (postgresCatalogProjection, bool, error) {
	var projection postgresCatalogProjection
	invalid := func() (postgresCatalogProjection, bool, error) {
		return projection, true, fmt.Errorf("unsupported function-catalog CASE expression")
	}
	keyword := func(i int, word string) bool {
		return i < len(tokens) && tokens[i].Kind == kitdbsql.TokenIdentifier && strings.EqualFold(tokens[i].Text, word)
	}
	textResult := func(i int) (any, bool) {
		if i >= len(tokens) {
			return nil, false
		}
		if tokens[i].Kind == kitdbsql.TokenString {
			return tokens[i].Text, true
		}
		return nil, keyword(i, "null")
	}
	type branch struct {
		matches func(map[string]any) bool
		result  any
	}
	var branches []branch
	i := 1
	for keyword(i, "when") {
		if len(branches) == 16 {
			return invalid()
		}
		i++
		start := i
		for i < len(tokens) && !keyword(i, "then") {
			i++
		}
		matches, ok := compileFunctionCatalogCondition(tokens[start:i], dataset)
		if !ok || !keyword(i, "then") {
			return invalid()
		}
		result, ok := textResult(i + 1)
		if !ok {
			return invalid()
		}
		branches = append(branches, branch{matches: matches, result: result})
		i += 2
	}
	var fallback any
	if keyword(i, "else") {
		var ok bool
		fallback, ok = textResult(i + 1)
		if !ok {
			return invalid()
		}
		i += 2
	}
	if len(branches) == 0 || !keyword(i, "end") || i+1 != len(tokens) {
		return invalid()
	}
	projection = postgresCatalogProjection{name: "case", column: pgwire.Column{Name: "case", DataTypeOID: pgwire.OIDText, DataTypeSize: -1}}
	projection.evaluate = func(row map[string]any) (any, error) {
		for _, branch := range branches {
			if branch.matches(row) {
				return branch.result, nil
			}
		}
		return fallback, nil
	}
	return projection, true, nil
}

func compileFunctionCatalogCondition(tokens []kitdbsql.Token, dataset postgresCatalogDataset) (func(map[string]any) bool, bool) {
	if len(tokens) == 0 || tokens[0].Kind != kitdbsql.TokenIdentifier {
		return nil, false
	}
	if len(tokens) >= 3 && tokens[1].Text == "." && tokens[2].Kind == kitdbsql.TokenIdentifier {
		tokens = tokens[2:]
	}
	column := strings.ToLower(tokens[0].Text)
	spec, found := dataset.columns[column]
	if !found {
		return nil, false
	}
	if len(tokens) == 1 {
		if spec.oid != pgwire.OIDBool {
			return nil, false
		}
		return func(row map[string]any) bool { return row[column] == true }, true
	}
	if len(tokens) < 3 || tokens[1].Text != "=" {
		return nil, false
	}
	value := tokens[2]
	if len(tokens) == 3 && value.Kind == kitdbsql.TokenIdentifier && strings.EqualFold(value.Text, "null") {
		return func(map[string]any) bool { return false }, true
	}
	if value.Kind != kitdbsql.TokenString {
		return nil, false
	}
	if len(tokens) == 3 && spec.oid == pgwire.OIDText {
		return func(row map[string]any) bool { return row[column] == value.Text }, true
	}
	// Older clients test for PostgreSQL's trigger pseudo-type even though KitDB
	// exposes only scalar SQL functions. Resolve the type identity, not its body.
	if len(tokens) < 5 || tokens[3].Text != "::" || spec.oid != pgwire.OIDOID {
		return nil, false
	}
	cast := tokens[4:]
	if len(cast) == 3 && cast[0].Kind == kitdbsql.TokenIdentifier && strings.EqualFold(cast[0].Text, "pg_catalog") && cast[1].Text == "." {
		cast = cast[2:]
	}
	if len(cast) != 1 || cast[0].Kind != kitdbsql.TokenIdentifier || !strings.EqualFold(cast[0].Text, "regtype") {
		return nil, false
	}
	name := strings.TrimPrefix(strings.ToLower(value.Text), "pg_catalog.")
	var oid uint32
	if name == "trigger" {
		oid = 2279
	} else if kind, ok := kitdbsql.ResolveName(name); ok {
		oid = kind.Catalog.OID
	}
	if oid == 0 {
		return nil, false
	}
	return func(row map[string]any) bool { return row[column] == oid }, true
}

// Bind only lexical placeholders, preserving quoted data as data. Catalog
// execution must not silently ignore $1 in a manager's OID/name lookup.
func bindFunctionCatalogParameters(source string, parameters []pgwire.Parameter, describe bool) (string, error) {
	tokens, err := kitdbsql.Lex(source)
	if err != nil {
		return "", pgwire.NewError("42601", err.Error())
	}
	var result strings.Builder
	start := 0
	for _, token := range tokens {
		if token.Kind != kitdbsql.TokenPlaceholder {
			continue
		}
		index, err := strconv.Atoi(strings.TrimPrefix(token.Text, "$"))
		if err != nil || index < 1 || !strings.HasPrefix(token.Text, "$") {
			return "", pgwire.NewError("42P02", "catalog requires numbered SQL parameters")
		}
		var value any
		if !describe {
			if index > len(parameters) {
				return "", pgwire.NewError("42P02", "catalog parameter is missing")
			}
			value, err = decodePostgresParameter(parameters[index-1])
			if err != nil {
				return "", err
			}
		}
		result.WriteString(source[start:token.Start])
		result.WriteString(postgresCatalogLiteral(value))
		start = token.End
		if result.Len() > kitdbsql.MaximumStatementBytes {
			return "", pgwire.NewError("54000", "catalog statement exceeds byte limit")
		}
	}
	if start == 0 {
		return source, nil
	}
	result.WriteString(source[start:])
	if result.Len() > kitdbsql.MaximumStatementBytes {
		return "", pgwire.NewError("54000", "catalog statement exceeds byte limit")
	}
	return result.String(), nil
}

func isFunctionCatalogQuery(source string) bool {
	if _, ok := scalarFunctionCatalogSelect(source); ok {
		return true
	}
	normalized := normalizePostgresCatalogSQL(source)
	return containsPostgresRelation(normalized, "pg_catalog.pg_proc") || containsPostgresRelation(normalized, "pg_proc") || containsPostgresRelation(normalized, "information_schema.routines")
}
