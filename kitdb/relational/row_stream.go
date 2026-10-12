package relational

import (
	"context"
	"fmt"
	"io"
	"sync"

	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

// selectRowStream owns one read-only transaction until the consumer reaches
// EOF or closes the stream. The single-row channel deliberately applies
// backpressure instead of turning a slow client into an unbounded row buffer.
type selectRowStream struct {
	columns []Column
	rows    <-chan []any
	cancel  context.CancelFunc
	done    <-chan struct{}

	closeOnce sync.Once
	errMu     sync.Mutex
	err       error
}

func (stream *selectRowStream) Columns() []Column {
	if stream == nil {
		return nil
	}
	return append([]Column(nil), stream.columns...)
}

func (stream *selectRowStream) Next() ([]any, error) {
	if stream == nil || stream.rows == nil {
		return nil, io.EOF
	}
	row, available := <-stream.rows
	if available {
		return row, nil
	}
	stream.errMu.Lock()
	err := stream.err
	stream.errMu.Unlock()
	if err != nil {
		return nil, err
	}
	return nil, io.EOF
}

func (stream *selectRowStream) Close() error {
	if stream == nil {
		return nil
	}
	stream.closeOnce.Do(func() {
		stream.cancel()
		<-stream.done
	})
	return nil
}

func (stream *selectRowStream) setError(err error) {
	if err == nil {
		return
	}
	stream.errMu.Lock()
	stream.err = err
	stream.errMu.Unlock()
}

// openSelectRowStream returns handled=false when the statement needs an
// executor that materializes rows (for example JOIN, DISTINCT or an uncovered
// ORDER BY). A handled error is final and must not be reparsed through the
// fallback executor.
func (engine *Engine) openSelectRowStream(
	ctx context.Context,
	source string,
	parameters []any,
) (*selectRowStream, bool, error) {
	if ctx == nil {
		return nil, true, fmt.Errorf("kitdb: query context is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, true, err
	}
	statement, err := kitdbsql.ParseStatement(source)
	if err != nil {
		return nil, true, err
	}
	if statement.Kind != kitdbsql.StatementSelect || !streamableSelect(statement.Select) {
		return nil, false, nil
	}
	// A cacheable SELECT must keep the existing cache accounting and result
	// ownership. Non-cacheable SELECTs may still stream when a cache exists.
	if engine.queryCache.duration(&statement) > 0 {
		return nil, false, nil
	}

	transaction, err := engine.BeginTransaction(ctx, TransactionOptions{ReadOnly: true})
	if err != nil {
		return nil, true, err
	}
	rollback := true
	defer func() {
		if rollback {
			_ = transaction.Rollback()
		}
	}()

	if err := resolveStatementFunctions(&statement, transaction.catalog); err != nil {
		return nil, true, err
	}
	plan := statement.Select
	schema, err := transaction.schema(plan.Table)
	if err != nil {
		return nil, true, err
	}
	conditions, err := bindConditions(schema, plan.Conditions, parameters)
	if err != nil {
		return nil, true, err
	}
	predicate, err := bindPredicate(schema, plan.Predicate, parameters)
	if err != nil {
		return nil, true, err
	}
	orders, err := bindOrders(schema, plan.Order)
	if err != nil {
		return nil, true, err
	}
	columns, names, err := bindProjection(schema, plan.Projection)
	if err != nil {
		return nil, true, err
	}
	limit := engine.maximumResultRows
	if plan.HasLimit {
		if plan.Limit > engine.maximumResultRows {
			return nil, true, fmt.Errorf(
				"kitdb SQL: LIMIT %d exceeds this server's result limit of %d",
				plan.Limit, engine.maximumResultRows,
			)
		}
		limit = plan.Limit
	}
	generation, err := activeRowGeneration(transaction, schema)
	if err != nil {
		return nil, true, err
	}
	access, err := transaction.planRowAccess(schema, generation, conditions, orders)
	if err != nil {
		return nil, true, err
	}
	if len(orders) != 0 && !access.orderCovered {
		return nil, false, nil
	}

	streamContext, cancel := context.WithCancel(ctx)
	rowChannel := make(chan []any, 1)
	done := make(chan struct{})
	stream := &selectRowStream{
		columns: append([]Column(nil), columns...),
		rows:    rowChannel, cancel: cancel, done: done,
	}
	decoder := newProjectedRowDecoder(
		schema, selectDecodeTags(schema, conditions, predicate, names),
	)
	if engine.queryCache != nil {
		engine.queryCache.bypasses.Add(1)
	}
	rollback = false
	go func() {
		defer close(done)
		defer close(rowChannel)
		defer transaction.Rollback()
		if plan.HasLimit && limit == 0 {
			return
		}

		matched := 0
		emitted := 0
		visited := 0
		walkErr := transaction.walkAccessRows(access, func(_, encoded []byte) (bool, error) {
			visited++
			if visited&255 == 0 {
				if err := streamContext.Err(); err != nil {
					return false, err
				}
			}
			decoded, err := decoder.decode(encoded)
			if err != nil {
				return false, err
			}
			if !matchesAll(decoded.values, conditions) {
				return false, nil
			}
			matches, err := predicateMatches(decoded.values, predicate)
			if err != nil {
				return false, err
			}
			if !matches {
				return false, nil
			}
			matched++
			if matched <= plan.Offset {
				return false, nil
			}
			if plan.HasLimit && emitted >= limit {
				return true, nil
			}
			if !plan.HasLimit && emitted >= engine.maximumResultRows {
				return false, fmt.Errorf(
					"kitdb SQL: result exceeds %d rows; add a narrower WHERE or explicit LIMIT",
					engine.maximumResultRows,
				)
			}
			row := projectRow(schema, decoded.values, names)
			select {
			case rowChannel <- row:
				emitted++
				return plan.HasLimit && emitted == limit, nil
			case <-streamContext.Done():
				return false, streamContext.Err()
			}
		})
		stream.setError(walkErr)
	}()
	return stream, true, nil
}

func streamableSelect(plan *kitdbsql.SelectStatement) bool {
	return plan != nil && plan.Table != "" && plan.Source == nil &&
		len(plan.CommonTables) == 0 && len(plan.Joins) == 0 && len(plan.SetOperations) == 0 &&
		plan.Search == nil && !plan.Distinct && !plan.HasAfter &&
		!selectHasAggregates(plan) && !selectHasScalarExpressions(plan)
}
