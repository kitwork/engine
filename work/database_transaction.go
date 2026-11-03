package work

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"

	kitdbsqlite "github.com/kitwork/engine/kitdb/sqlite"
	"github.com/kitwork/engine/value"
)

// db.transaction((database) => { … }) (KITWORK_DATABASE.md mục 3, lát 4, 03/11/2026). The compiler lowers it to
// db.#transaction(function), the function's parameter a handle whose statements are translated as db's. The engine
// migrates the database's tables, then runs the function as one request of the file's writer
// (database_history.go): inside the writer's transaction, in a savepoint of its own, its writes and reads through the
// handle on the writer's connection, so it reads what it wrote. When the function returns, the request commits
// with the writer's next commit, one revision of the history, and the transaction gives what the function
// returned; when it fails, the savepoint is rolled back and the failure goes on. return false and return null are
// results. The handle is no use once the transaction is over.
//
// The function runs on the writer's goroutine, as a VM nested in the request's (runtime/nesting.go): the request
// waits for it, and every other write of the file waits for the transaction, as SQLite has one writer. A write
// through another handle of the same file from inside it would wait for the transaction itself; the writer
// refuses it (kitdbsqlite.ErrReentrant) and the statement says what to write instead. A write to another file is
// its own commit, not part of the transaction (the compiler refuses one written in the function).

// errTransactionFailed rolls the transaction's savepoint back when its function failed.
var errTransactionFailed = errors.New("the transaction's function failed")

// transaction runs db.#transaction(function).
func (m *ManagedDatabase) transaction(args []value.Value) value.Value {
	if m.tx != nil {
		return value.InvalidFailure(value.InvalidArgumentCode, "database.transaction(…) lồng trong giao dịch là lỗi, cho tới khi có savepoint thật")
	}
	var lambda *value.Lambda
	if len(args) == 1 && args[0].K == value.Func {
		lambda, _ = args[0].V.(*value.Lambda)
	}
	if lambda == nil {
		return value.InvalidFailure(value.InvalidArgumentCode, "transaction nhận một hàm: db.transaction((database) => { … })")
	}
	if m.proxy.engine == "kitdb" || m.proxy.sqlitePath == "" {
		return value.InvalidFailure(value.UnsupportedCode, "db.transaction chạy trên database có lịch sử (SQLite của app hay của host)")
	}
	// every table migrated first: a migration from inside the transaction would wait for it
	tables, _ := m.proxy.schemaSnapshot()
	names := make([]string, 0, len(tables))
	for name := range tables {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) > 0 {
		table, failure := m.proxy.table(names[0])
		if table == nil {
			return failure
		}
		if failure, ok := table.ready(); !ok {
			return failure
		}
	}
	database, err := m.proxy.tenant.historyDatabase(m.proxy.sqlitePath)
	if err != nil {
		return value.InvalidFailure(value.DatabaseCode, err.Error())
	}
	ctx := context.Background()
	if m.proxy.scope != nil {
		ctx = m.proxy.scope.Context()
	}
	var result value.Value
	_, err = database.Write(ctx, scopeBy(m.proxy), func(tx *kitdbsqlite.Transaction) error {
		handle := &ManagedDatabase{proxy: m.proxy, catalog: m.catalog, tx: tx}
		defer handle.over.Store(true)
		result = (tenantLambdaExecutor{tenant: m.proxy.tenant, requestScope: m.proxy.scope}).ExecuteLambda(
			lambda, []value.Value{{K: value.Proxy, V: handle}})
		if result.Failed() {
			return errTransactionFailed
		}
		return nil
	})
	switch {
	case result.Failed():
		return result
	case errors.Is(err, kitdbsqlite.ErrReentrant):
		return reentrantFailure()
	case err != nil:
		return value.InvalidFailure(value.DatabaseCode, fmt.Sprintf("db.transaction: %v", err))
	}
	return result
}

// reentrantFailure is a write that would wait for the transaction it runs in.
func reentrantFailure() value.Value {
	return value.InvalidFailure(value.InvalidArgumentCode,
		"ghi qua db bên trong db.transaction(…) của cùng database thì phải chờ chính giao dịch đó: viết trên handle của giao dịch, (database) => database.insert(…)")
}

// scopeBy is who a commit names: the request's authenticated subject, or nobody.
func scopeBy(proxy *dbProxy) string {
	return historyBy(&SchemaTable{scope: proxy.scope})
}

// table is the table name of the statement's database, bound to the transaction when the handle is one's.
func (m *ManagedDatabase) table(name string) (*SchemaTable, value.Value) {
	table, failure := m.proxy.table(name)
	if table != nil {
		table.historyTx = m.tx
	}
	return table, failure
}

// transactionExecutor reads and writes through the writer's transaction, for the query builder.
type transactionExecutor struct {
	tx *kitdbsqlite.Transaction
}

func (e transactionExecutor) QueryContext(_ context.Context, query string, args ...any) (*sql.Rows, error) {
	return e.tx.Query(query, args...)
}

func (e transactionExecutor) ExecContext(_ context.Context, query string, args ...any) (sql.Result, error) {
	return e.tx.Exec(query, args...)
}
