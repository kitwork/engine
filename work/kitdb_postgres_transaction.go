package work

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/kitwork/engine/kitdb/pgwire"
	requestscope "github.com/kitwork/engine/request"
	"github.com/kitwork/engine/value"
)

const defaultKitDBPostgresTransactionTimeout = time.Minute

type kitDBPostgresTransactionMode uint8

const (
	kitDBPostgresTransactionUndecided kitDBPostgresTransactionMode = iota
	kitDBPostgresTransactionData
	kitDBPostgresTransactionDDL
)

func kitDBPostgresTransactionTimeout(configured time.Duration) time.Duration {
	if configured <= 0 {
		return defaultKitDBPostgresTransactionTimeout
	}
	return configured
}

// executeKitDBPostgresTransactionQuery gives one PostgreSQL connection one
// bounded KitDB record transaction. The first data statement opens the fixed
// snapshot lazily so the existing single-DDL GUI envelope can keep staging DDL
// without pretending that schema changes participate in a record transaction.
func (session *kitDBPostgresSession) executeKitDBPostgresTransactionQuery(
	ctx context.Context,
	source string,
	parameters []pgwire.Parameter,
) (pgwire.Result, bool, error) {
	normalized := normalizeKitDBPostgresSQL(source)
	if handled, readOnly, err := kitDBPostgresBeginOptions(normalized); handled {
		if err != nil {
			return pgwire.Result{}, true, err
		}
		if len(parameters) != 0 {
			return pgwire.Result{}, true, pgwire.NewError("42601", "BEGIN does not accept parameters")
		}
		return session.beginKitDBPostgresTransaction(readOnly)
	}

	switch normalized {
	case "rollback", "rollback transaction", "rollback work":
		if len(parameters) != 0 {
			return pgwire.Result{}, true, pgwire.NewError("42601", "ROLLBACK does not accept parameters")
		}
		return session.rollbackKitDBPostgresTransaction()
	case "commit", "commit transaction", "commit work", "end", "end transaction", "end work":
		if len(parameters) != 0 {
			return pgwire.Result{}, true, pgwire.NewError("42601", "COMMIT does not accept parameters")
		}
		return session.commitKitDBPostgresTransaction(ctx)
	}

	session.transactionMu.Lock()
	defer session.transactionMu.Unlock()
	transaction := session.transaction
	if transaction == nil {
		return pgwire.Result{}, false, nil
	}
	if transaction.failed {
		if transaction.expired {
			return pgwire.Result{}, true, pgwire.NewError(
				"25P03",
				"KitDB transaction exceeded its lifetime; issue ROLLBACK",
			)
		}
		return pgwire.Result{}, true, pgwire.NewError(
			"25P02",
			"current KitDB transaction is aborted; issue ROLLBACK",
		)
	}
	if transaction.context == nil || transaction.context.Err() != nil {
		session.expireKitDBPostgresTransactionLocked(transaction)
		return pgwire.Result{}, true, pgwire.NewError(
			"25P03",
			"KitDB transaction exceeded its lifetime; issue ROLLBACK",
		)
	}
	transaction.statements++
	if transaction.statements > kitDBRemoteTransactionStatementLimit {
		transaction.failed = true
		return pgwire.Result{}, true, pgwire.NewError(
			"54000",
			fmt.Sprintf("KitDB transaction exceeds %d statements", kitDBRemoteTransactionStatementLimit),
		)
	}

	if normalized == "discard all" {
		transaction.failed = true
		return pgwire.Result{}, true, pgwire.NewError("25001", "DISCARD ALL cannot run inside a KitDB transaction")
	}
	if strings.HasPrefix(normalized, "create database ") {
		transaction.failed = true
		return pgwire.Result{}, true, pgwire.NewError(
			"25001",
			"CREATE DATABASE cannot run inside a KitDB transaction",
		)
	}
	if strings.HasPrefix(normalized, "alter database ") ||
		strings.HasPrefix(normalized, "drop database ") {
		transaction.failed = true
		return pgwire.Result{}, true, pgwire.NewError(
			"25001",
			"ALTER/DROP DATABASE cannot run inside a KitDB transaction",
		)
	}
	if normalized == "show transaction_read_only" {
		readOnly := session.readonly || transaction.readOnly
		if readOnly {
			return kitDBPostgresTextResult("transaction_read_only", "on"), true, nil
		}
		return kitDBPostgresTextResult("transaction_read_only", "off"), true, nil
	}
	if result, handled, err := session.compatibilityQuery(source); handled {
		if err != nil {
			transaction.failed = true
		}
		return result, true, err
	}
	catalogDatabase := session.database
	if transaction.mode == kitDBPostgresTransactionData {
		catalogDatabase = transaction.database
	}
	if result, handled, err := session.catalogQueryWithDatabase(catalogDatabase, source, parameters); handled {
		if err != nil {
			transaction.failed = true
		}
		return result, true, err
	}
	if session.maintenance || session.database == nil {
		transaction.failed = true
		return pgwire.Result{}, true, pgwire.NewError("3D000", "KitDB maintenance database has no user structs")
	}

	bindings, err := kitDBPostgresBindings(parameters)
	if err != nil {
		transaction.failed = true
		return pgwire.Result{}, true, err
	}
	statement, err := parseKitSQL(source, bindings)
	if err != nil {
		transaction.failed = true
		return pgwire.Result{}, true, kitDBPostgresError(err)
	}
	if kitDBPostgresTransactionDDLKind(statement.kind) {
		return session.stageKitDBPostgresDDLLocked(transaction, statement.kind, source, parameters)
	}
	if !kitDBPostgresTransactionDataKind(statement.kind) {
		transaction.failed = true
		return pgwire.Result{}, true, pgwire.NewError(
			"0A000",
			"KitDB transactions accept SELECT, EXPLAIN SELECT, INSERT, UPDATE, DELETE, or one supported schema DDL statement",
		)
	}
	if transaction.mode == kitDBPostgresTransactionDDL {
		transaction.failed = true
		return pgwire.Result{}, true, pgwire.NewError(
			"0A000",
			"a staged KitDB schema transaction cannot mix DDL and data statements",
		)
	}
	if transaction.mode == kitDBPostgresTransactionUndecided {
		transaction.database, transaction.record, err = session.database.beginKitDBRecordTransaction(transaction.scope)
		if err != nil {
			transaction.failed = true
			if transaction.context.Err() != nil {
				session.expireKitDBPostgresTransactionLocked(transaction)
				return pgwire.Result{}, true, pgwire.NewError("25P03", "KitDB transaction exceeded its lifetime; issue ROLLBACK")
			}
			return pgwire.Result{}, true, kitDBPostgresError(err)
		}
		transaction.mode = kitDBPostgresTransactionData
	}

	statementContext, releaseContext := kitDBPostgresStatementContext(ctx, transaction.context)
	result, executeErr := executeKitDBRemoteSQL(
		statementContext,
		transaction.scope,
		transaction.database,
		source,
		bindings,
		session.readonly || transaction.readOnly,
	)
	releaseContext()
	if transaction.context.Err() != nil {
		session.expireKitDBPostgresTransactionLocked(transaction)
		return pgwire.Result{}, true, pgwire.NewError("25P03", "KitDB transaction exceeded its lifetime; issue ROLLBACK")
	}
	if executeErr != nil {
		transaction.failed = true
		return pgwire.Result{}, true, kitDBPostgresError(executeErr)
	}
	return kitDBPostgresResult(source, result), true, nil
}

func (session *kitDBPostgresSession) beginKitDBPostgresTransaction(
	readOnly bool,
) (pgwire.Result, bool, error) {
	session.transactionMu.Lock()
	defer session.transactionMu.Unlock()
	if session.transaction != nil {
		return pgwire.Result{}, true, pgwire.NewError("25001", "a KitDB transaction is already active")
	}
	if session.tenant == nil {
		return pgwire.Result{}, true, pgwire.NewError("57P01", "KitDB tenant is unavailable")
	}
	if !session.tenant.beginRequest() {
		return pgwire.Result{}, true, pgwire.NewError("57P01", "KitDB tenant is shutting down")
	}

	timeout := kitDBPostgresTransactionTimeout(session.transactionTimeout)
	transactionContext, cancel := context.WithTimeout(context.Background(), timeout)
	request := (&http.Request{}).WithContext(transactionContext)
	scope := requestscope.New(session.tenant, nil, request)
	lease, err := session.tenant.generationLease()
	if err != nil {
		cancel()
		scope.Close()
		session.tenant.endRequest()
		return pgwire.Result{}, true, pgwire.NewError("57P01", err.Error())
	}
	if lease != nil && !scope.AddCleanup(lease.Release) {
		lease.Release()
		cancel()
		scope.Close()
		session.tenant.endRequest()
		return pgwire.Result{}, true, pgwire.NewError("57P01", "KitDB transaction scope is unavailable")
	}

	transaction := &kitDBPostgresTransaction{
		scope: scope, context: transactionContext, cancel: cancel,
		requestOpen: true, readOnly: readOnly,
	}
	session.transaction = transaction
	transaction.timer = time.AfterFunc(timeout, func() {
		// Cancel before waiting for the session lock so an executing statement
		// observes the lifetime bound through its merged context.
		transaction.cancel()
		session.transactionMu.Lock()
		defer session.transactionMu.Unlock()
		if session.transaction == transaction {
			session.expireKitDBPostgresTransactionLocked(transaction)
		}
	})
	return pgwire.Result{CommandTag: "BEGIN"}, true, nil
}

func (session *kitDBPostgresSession) rollbackKitDBPostgresTransaction() (pgwire.Result, bool, error) {
	session.transactionMu.Lock()
	defer session.transactionMu.Unlock()
	transaction := session.transaction
	session.transaction = nil
	if err := session.releaseKitDBPostgresTransactionLocked(transaction, false, nil); err != nil {
		return pgwire.Result{}, true, kitDBPostgresError(err)
	}
	return pgwire.Result{CommandTag: "ROLLBACK"}, true, nil
}

func (session *kitDBPostgresSession) commitKitDBPostgresTransaction(
	ctx context.Context,
) (pgwire.Result, bool, error) {
	session.transactionMu.Lock()
	transaction := session.transaction
	if transaction == nil {
		session.transactionMu.Unlock()
		return pgwire.Result{CommandTag: "COMMIT"}, true, nil
	}
	if transaction.failed || transaction.context == nil || transaction.context.Err() != nil {
		session.transaction = nil
		_ = session.releaseKitDBPostgresTransactionLocked(transaction, false, nil)
		session.transactionMu.Unlock()
		return pgwire.Result{CommandTag: "ROLLBACK"}, true, nil
	}
	if transaction.mode == kitDBPostgresTransactionDDL {
		source := transaction.ddlSource
		parameters := transaction.ddlParameters
		session.transaction = nil
		_ = session.releaseKitDBPostgresTransactionLocked(transaction, false, nil)
		session.transactionMu.Unlock()
		if _, err := session.executeAutocommit(ctx, source, parameters); err != nil {
			return pgwire.Result{}, true, err
		}
		return pgwire.Result{CommandTag: "COMMIT"}, true, nil
	}

	session.transaction = nil
	err := session.releaseKitDBPostgresTransactionLocked(transaction, true, ctx)
	session.transactionMu.Unlock()
	if err != nil {
		return pgwire.Result{}, true, kitDBPostgresError(err)
	}
	return pgwire.Result{CommandTag: "COMMIT"}, true, nil
}

func (session *kitDBPostgresSession) stageKitDBPostgresDDLLocked(
	transaction *kitDBPostgresTransaction,
	kind string,
	source string,
	parameters []pgwire.Parameter,
) (pgwire.Result, bool, error) {
	if transaction.mode == kitDBPostgresTransactionData {
		transaction.failed = true
		return pgwire.Result{}, true, pgwire.NewError(
			"0A000",
			"a KitDB data transaction cannot mix data statements and schema DDL",
		)
	}
	if session.readonly || transaction.readOnly {
		transaction.failed = true
		return pgwire.Result{}, true, pgwire.NewError("25006", "database is read-only")
	}
	if transaction.ddlSource != "" {
		transaction.failed = true
		return pgwire.Result{}, true, pgwire.NewError(
			"0A000",
			"the KitDB PostgreSQL transaction envelope accepts only one supported schema DDL statement",
		)
	}
	transaction.mode = kitDBPostgresTransactionDDL
	transaction.ddlSource = source
	transaction.ddlParameters = cloneKitDBPostgresParameters(parameters)
	return pgwire.Result{CommandTag: kitDBPostgresDDLCommandTag(kind)}, true, nil
}

func (session *kitDBPostgresSession) expireKitDBPostgresTransactionLocked(
	transaction *kitDBPostgresTransaction,
) {
	if transaction == nil {
		return
	}
	transaction.failed = true
	transaction.expired = true
	_ = session.releaseKitDBPostgresTransactionLocked(transaction, false, nil)
}

// releaseKitDBPostgresTransactionLocked is the single resource teardown path
// for COMMIT, ROLLBACK, timeout, and disconnect. transactionMu must be held.
func (session *kitDBPostgresSession) releaseKitDBPostgresTransactionLocked(
	transaction *kitDBPostgresTransaction,
	commit bool,
	commitContext context.Context,
) error {
	if transaction == nil {
		return nil
	}
	if transaction.timer != nil {
		transaction.timer.Stop()
		transaction.timer = nil
	}
	var err error
	if transaction.record != nil {
		if commit {
			_, err = transaction.record.CommitContext(commitContext)
		} else {
			err = transaction.record.Rollback()
		}
		transaction.record = nil
	}
	transaction.database = nil
	if transaction.cancel != nil {
		transaction.cancel()
	}
	if transaction.scope != nil {
		transaction.scope.Close()
		transaction.scope = nil
	}
	if transaction.requestOpen {
		transaction.requestOpen = false
		if session.tenant != nil {
			session.tenant.endRequest()
		}
	}
	return err
}

func kitDBPostgresStatementContext(
	queryContext context.Context,
	transactionContext context.Context,
) (context.Context, func()) {
	ctx, cancel := context.WithCancel(queryContext)
	stop := context.AfterFunc(transactionContext, cancel)
	return ctx, func() {
		stop()
		cancel()
	}
}

func kitDBPostgresBindings(parameters []pgwire.Parameter) (kitSQLBindings, error) {
	bindings := kitSQLBindings{named: map[string]value.Value{}}
	for _, parameter := range parameters {
		item, err := kitDBPostgresParameter(parameter)
		if err != nil {
			return kitSQLBindings{}, err
		}
		bindings.positional = append(bindings.positional, item)
	}
	return bindings, nil
}

func kitDBPostgresBeginOptions(normalized string) (bool, bool, error) {
	for _, prefix := range []string{"begin", "start transaction"} {
		if normalized != prefix && !strings.HasPrefix(normalized, prefix+" ") {
			continue
		}
		options := strings.TrimSpace(strings.TrimPrefix(normalized, prefix))
		options = strings.TrimSpace(strings.TrimPrefix(options, "transaction"))
		readOnly := false
		if strings.HasSuffix(options, " read only") || options == "read only" {
			readOnly = true
			options = strings.TrimSpace(strings.TrimSuffix(options, "read only"))
		} else if strings.HasSuffix(options, " read write") || options == "read write" {
			options = strings.TrimSpace(strings.TrimSuffix(options, "read write"))
		}
		if options == "" {
			return true, readOnly, nil
		}
		for _, level := range []string{
			"isolation level read uncommitted",
			"isolation level read committed",
			"isolation level repeatable read",
			"isolation level serializable",
		} {
			if options == level {
				return true, readOnly, nil
			}
		}
		return true, false, pgwire.NewError("0A000", "unsupported KitDB transaction option")
	}
	return false, false, nil
}

func kitDBPostgresTransactionDataKind(kind string) bool {
	switch kind {
	case "select_scalar", "select", "explain", "insert", "update", "delete":
		return true
	default:
		return false
	}
}

func kitDBPostgresTransactionDDLKind(kind string) bool {
	switch kind {
	case "drop_table", "drop_index", "alter_add_constraint", "alter_drop_constraint",
		"alter_rename_table", "alter_rename_index", "alter_primary_key":
		return true
	default:
		return false
	}
}

func kitDBPostgresDDLCommandTag(kind string) string {
	switch kind {
	case "drop_index":
		return "DROP INDEX"
	case "alter_rename_index":
		return "ALTER INDEX"
	case "alter_add_constraint", "alter_drop_constraint", "alter_rename_table", "alter_primary_key":
		return "ALTER TABLE"
	default:
		return "DROP TABLE"
	}
}
