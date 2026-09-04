package relational

import (
	"context"
	"errors"
	"fmt"
	"sync"

	kitdbengine "github.com/kitwork/engine/kitdb"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

// Session retains currval/lastval across embedded calls. Engine.Execute itself
// is stateless; PostgreSQL connections each own an equivalent session state.
type Session struct {
	engine    *Engine
	sequences sequenceSession
}

func (engine *Engine) NewSession() *Session { return &Session{engine: engine} }

func (session *Session) Execute(ctx context.Context, source string, parameters ...any) (Result, error) {
	return session.engine.executeWithSequences(ctx, source, parameters, &session.sequences, false)
}

// ExecutePlan is the direct typed counterpart of Execute and retains the same
// session-local sequence state without routing through SQL text.
func (session *Session) ExecutePlan(
	ctx context.Context,
	plan *kitdbsql.ParsedStatement,
	parameters ...any,
) (Result, error) {
	if session == nil || session.engine == nil {
		return Result{}, fmt.Errorf("kitdb: relational session is unavailable")
	}
	return session.engine.executePlanWithSequences(ctx, plan, parameters, &session.sequences, false)
}

func (session *Session) BeginTransaction(ctx context.Context, options TransactionOptions) (*Transaction, error) {
	transaction, err := session.engine.BeginTransaction(ctx, options)
	if err == nil {
		transaction.sequences = &session.sequences
	}
	return transaction, err
}

type sequenceSession struct {
	mu               sync.Mutex
	values           map[string]int64
	lastID, lastName string
}

func (session *sequenceSession) reset() {
	session.mu.Lock()
	defer session.mu.Unlock()
	session.values, session.lastID, session.lastName = nil, "", ""
}

func sequenceFunction(name string) bool {
	return name == "nextval" || name == "currval" || name == "setval" || name == "lastval"
}

func containsSequenceFunction(node *kitdbsql.ExpressionPlan) bool {
	if node == nil {
		return false
	}
	if node.Kind == "function" && sequenceFunction(node.Operator) {
		return true
	}
	for i := range node.Arguments {
		if containsSequenceFunction(&node.Arguments[i]) {
			return true
		}
	}
	return false
}

func sequenceArgument(node kitdbsql.ExpressionPlan) (kitdbsql.Literal, error) {
	if node.Kind == "literal" && !literalUsesClock(node.Literal.Kind) {
		return node.Literal, nil
	}
	if node.Kind == "unary" && (node.Operator == "-" || node.Operator == "+") && len(node.Arguments) == 1 {
		child := node.Arguments[0]
		if child.Kind == "literal" && child.Literal.Kind == kitdbsql.LiteralNumber {
			literal := child.Literal
			literal.Text = node.Operator + literal.Text
			return literal, nil
		}
	}
	return kitdbsql.Literal{}, fmt.Errorf("kitdb SQL: sequence arguments must be literals or parameters")
}

func describeSequenceSelect(plan *kitdbsql.SelectStatement) ([]Column, bool, error) {
	if plan == nil {
		return nil, false, nil
	}
	found := selectContainsSequenceFunction(plan)
	if !found {
		return nil, false, nil
	}
	invalid := func() ([]Column, bool, error) {
		return nil, true, fmt.Errorf("kitdb SQL: sequence functions currently require direct scalar SELECT projections without FROM, nesting or other clauses")
	}
	if plan.Table != "" || plan.Source != nil || len(plan.CommonTables) != 0 || plan.Distinct || plan.Predicate != nil || plan.HasLimit || plan.Offset != 0 || len(plan.Order) != 0 || len(plan.GroupBy) != 0 || len(plan.SetOperations) != 0 {
		return invalid()
	}
	columns := make([]Column, len(plan.Projection))
	for i, projection := range plan.Projection {
		node := projection.Expression
		if node == nil || node.Kind != "function" || !sequenceFunction(node.Operator) {
			return invalid()
		}
		count := len(node.Arguments)
		if node.Operator == "lastval" && count != 0 || (node.Operator == "nextval" || node.Operator == "currval") && count != 1 || node.Operator == "setval" && count != 2 && count != 3 {
			return nil, true, fmt.Errorf("kitdb SQL: invalid %s argument count", node.Operator)
		}
		for _, argument := range node.Arguments {
			if _, err := sequenceArgument(argument); err != nil {
				return nil, true, err
			}
		}
		columns[i] = Column{Name: projectionLabel(projection, node.Operator), Kind: "bigint"}
	}
	return columns, true, nil
}

func selectContainsSequenceFunction(plan *kitdbsql.SelectStatement) bool {
	if plan == nil {
		return false
	}
	for _, projection := range plan.Projection {
		if containsSequenceFunction(projection.Expression) {
			return true
		}
	}
	if selectContainsSequenceFunction(plan.Source) {
		return true
	}
	for _, commonTable := range plan.CommonTables {
		if selectContainsSequenceFunction(commonTable.Select) {
			return true
		}
	}
	for _, operation := range plan.SetOperations {
		if selectContainsSequenceFunction(operation.Select) {
			return true
		}
	}
	return false
}

func (session *sequenceSession) execute(ctx context.Context, database *kitdbengine.DB, plan *kitdbsql.SelectStatement, parameters []any, readOnly bool) (Result, error) {
	columns, _, err := describeSequenceSelect(plan)
	if err != nil {
		return Result{}, err
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	// Bind the entire call list before reserving any values. Runtime failures
	// can still leave gaps, as required for non-transactional sequences.
	type call struct {
		function, name string
		value          int64
		called, null   bool
	}
	calls := make([]call, len(plan.Projection))
	for i, projection := range plan.Projection {
		node := projection.Expression
		item := call{function: node.Operator, called: true}
		if readOnly && (item.function == "nextval" || item.function == "setval") {
			return Result{}, fmt.Errorf("kitdb: sequence mutation is not allowed in a read-only transaction")
		}
		arguments := make([]any, len(node.Arguments))
		for j, argument := range node.Arguments {
			literal, err := sequenceArgument(argument)
			if err != nil {
				return Result{}, err
			}
			arguments[j], err = resolveLiteral(literal, parameters)
			if err != nil {
				return Result{}, err
			}
			item.null = item.null || arguments[j] == nil
		}
		if !item.null && len(arguments) != 0 {
			name, ok := arguments[0].(string)
			if !ok {
				return Result{}, fmt.Errorf("kitdb SQL: sequence name must be text")
			}
			item.name, err = kitdbsql.ParseSequenceName(name)
			if err != nil {
				return Result{}, err
			}
			if item.function == "setval" {
				item.value, err = integerValue(arguments[1])
				if err != nil {
					return Result{}, err
				}
				if len(arguments) == 3 {
					item.called, ok = arguments[2].(bool)
					if !ok {
						return Result{}, fmt.Errorf("kitdb SQL: setval is_called must be boolean")
					}
				}
			}
		}
		calls[i] = item
	}
	row := make([]any, len(calls))
	for i, item := range calls {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		if item.null {
			continue
		}
		if item.function == "lastval" {
			if session.lastID == "" {
				return Result{}, fmt.Errorf("kitdb SQL: lastval is not defined in this session")
			}
			item.name = session.lastName
		}
		sequence, err := database.SequenceByName(item.name)
		if err != nil {
			return Result{}, err
		}
		if item.function == "currval" || item.function == "lastval" {
			value, exists := session.values[sequence.ID]
			if !exists || item.function == "lastval" && sequence.ID != session.lastID {
				return Result{}, fmt.Errorf("kitdb SQL: %s is not defined for this sequence in this session", item.function)
			}
			row[i] = value
			continue
		}
		if _, found := session.values[sequence.ID]; !found && len(session.values) >= 1024 {
			return Result{}, fmt.Errorf("kitdb SQL: session sequence-state budget exceeded; open a new session")
		}
		var value int64
		if item.function == "nextval" {
			sequence, value, err = database.NextSequence(ctx, item.name)
		} else {
			sequence, value, err = database.SetSequence(ctx, item.name, item.value, item.called)
		}
		if err != nil {
			return Result{}, err
		}
		if item.called {
			if session.values == nil {
				session.values = make(map[string]int64)
			}
			session.values[sequence.ID] = value
		}
		if item.function == "nextval" {
			session.lastID, session.lastName = sequence.ID, sequence.Name
		}
		row[i] = value
	}
	return Result{Columns: columns, Rows: [][]any{row}, CommandTag: "SELECT 1", Affected: 1}, nil
}

func (engine *Engine) executeSequenceDDL(ctx context.Context, statement kitdbsql.ParsedStatement) (Result, error) {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if err := engine.readyLocked(); err != nil {
		return Result{}, err
	}
	command := ""
	var err error
	switch {
	case statement.CreateSequence != nil:
		plan := statement.CreateSequence
		// Index names share PostgreSQL's relation namespace too.
		catalog, catalogErr := engine.sequenceRelationSchemasLocked()
		if catalogErr != nil {
			return Result{}, catalogErr
		}
		for _, schema := range catalog {
			for _, index := range catalogIndexesFor(schema) {
				if index.name == plan.Name {
					return Result{}, fmt.Errorf("kitdb SQL: sequence name conflicts with index %q", plan.Name)
				}
			}
		}
		_, err = engine.database.CreateSequence(ctx, kitdbengine.Sequence{Name: plan.Name, DataType: plan.DataType, Start: plan.Start, Increment: plan.Increment, Minimum: plan.Minimum, Maximum: plan.Maximum, Cycle: plan.Cycle, Cache: plan.Cache})
		if plan.IfNotExists && errors.Is(err, kitdbengine.ErrSequenceExists) {
			err = nil
		}
		command = "CREATE SEQUENCE"
	case statement.DropSequence != nil:
		plan := statement.DropSequence
		err = engine.database.DropSequence(ctx, plan.Name)
		if plan.IfExists && errors.Is(err, kitdbengine.ErrSequenceNotFound) {
			err = nil
		}
		command = "DROP SEQUENCE"
	case statement.AlterSequence != nil:
		plan := statement.AlterSequence
		err = engine.database.RestartSequence(ctx, plan.Name, plan.Restart)
		if plan.IfExists && errors.Is(err, kitdbengine.ErrSequenceNotFound) {
			err = nil
		}
		command = "ALTER SEQUENCE"
	}
	return Result{CommandTag: command}, err
}

func (engine *Engine) sequenceRelationSchemasLocked() ([]kitdbsql.Schema, error) {
	catalog, err := engine.database.Catalog()
	if err != nil {
		return nil, err
	}
	schemas := make([]kitdbsql.Schema, 0, len(catalog.Structs))
	for _, entry := range catalog.Structs {
		schema, err := decodeCatalogSchema(entry.Definition)
		if err != nil {
			return nil, err
		}
		schemas = append(schemas, schema)
	}
	return schemas, nil
}
