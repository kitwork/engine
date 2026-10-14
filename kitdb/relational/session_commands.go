package relational

import (
	"context"

	"github.com/kitwork/engine/kitdb/managed"
	"github.com/kitwork/engine/kitdb/pgwire"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

func (node *PostgresNode) sessionCommand(ctx context.Context, account managed.UserInfo, self *nodeSessionRecord, source string, args []any, inTransaction, describe bool) (Result, bool, error) {
	id, handled, err := kitdbsql.ParseKillSession(source)
	if !handled {
		return Result{}, false, nil
	}
	if err != nil || len(args) != 0 {
		return Result{}, true, pgwire.NewError("42601", "invalid KILL SESSION statement")
	}
	if err := ctx.Err(); err != nil {
		return Result{}, true, postgresError(err)
	}
	if node.catalog == nil || !node.catalog.AuthEnabled() {
		return Result{}, true, pgwire.NewError("0A000", "session control requires catalog auth")
	}
	if inTransaction {
		return Result{}, true, pgwire.NewError("25001", "session control requires autocommit")
	}
	var live *managed.UserInfo
	for _, user := range node.catalog.SessionAccounts() {
		if account.SameAccount(user) && !user.Disabled {
			copy := user
			live = &copy
			break
		}
	}
	if live == nil {
		return Result{}, true, pgwire.NewError("42501", "session control permission denied")
	}
	node.mu.Lock()
	defer node.mu.Unlock()
	if node.closed {
		return Result{}, true, pgwire.NewError("08003", "node is closed")
	}
	target := node.sessionRegistry[id]
	if target == nil || (!(account.Admin && live.Admin) && !account.SameAccount(target.account)) {
		return Result{}, true, pgwire.NewError("42501", "session unavailable or permission denied")
	}
	if target == self {
		return Result{}, true, pgwire.NewError("55000", "cannot terminate the issuing session")
	}
	if !describe && target.terminating.CompareAndSwap(false, true) {
		target.cancel()
		// One worker per registered target, bounded by MaximumSessions. Never
		// close inline under the caller's connection lock or the node lock.
		go func() { _ = target.close() }()
	}
	return Result{CommandTag: "KILL SESSION"}, true, nil
}

func (session *postgresSession) sessionCommand(ctx context.Context, source string, parameters []pgwire.Parameter, describe bool) (pgwire.Result, bool, error) {
	_, handled, _ := kitdbsql.ParseKillSession(source)
	if !handled {
		return pgwire.Result{}, false, nil
	}
	if session.node == nil {
		return pgwire.Result{}, true, pgwire.NewError("0A000", "session control requires a node")
	}
	session.mu.Lock()
	inTransaction := session.transaction != nil || session.maintenanceTransaction
	failed := session.failed
	session.mu.Unlock()
	if failed {
		return pgwire.Result{}, true, pgwire.NewError("25P02", "current transaction is aborted")
	}
	var args []any
	var err error
	if !describe {
		args, err = postgresParameters(parameters)
	}
	if err != nil {
		return pgwire.Result{}, true, err
	}
	result, handled, err := session.node.sessionCommand(ctx, session.account, session.record, source, args, inTransaction, describe)
	if err != nil && inTransaction {
		session.mu.Lock()
		session.failed = true
		session.mu.Unlock()
	}
	return postgresResult(result), handled, err
}
