package relational

import (
	"context"
	"errors"
	"strings"

	"github.com/kitwork/engine/kitdb/managed"
	"github.com/kitwork/engine/kitdb/pgwire"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

func (node *PostgresNode) accountCommand(ctx context.Context, account managed.UserInfo, source string, args []any, inTransaction, describe bool) (Result, bool, error) {
	statement, handled, err := kitdbsql.ParseAccountStatement(source)
	if !handled {
		return Result{}, false, nil
	}
	if err != nil {
		return Result{}, true, pgwire.NewError("42601", "invalid account management statement")
	}
	if node.catalog == nil || !node.catalog.AuthEnabled() {
		return Result{}, true, pgwire.NewError("0A000", "SQL account management requires catalog auth")
	}
	if !account.Admin {
		return Result{}, true, pgwire.NewError("42501", "root administrator required")
	}
	node.mu.Lock()
	closed := node.closed
	node.mu.Unlock()
	if closed {
		return Result{}, true, pgwire.NewError("08003", "node is closed")
	}
	if node.readOnly {
		return Result{}, true, pgwire.NewError("25006", "node is read-only")
	}
	if inTransaction {
		return Result{}, true, pgwire.NewError("25001", "account management requires autocommit outside data transactions")
	}
	liveAdmin := false
	for _, user := range node.catalog.SessionAccounts() {
		if account.SameAccount(user) && user.Admin && !user.Disabled {
			liveAdmin = true
			break
		}
	}
	if !liveAdmin {
		return Result{}, true, pgwire.NewError("42501", "root administrator required")
	}
	ctx = managed.WithSessionAccount(ctx, account)
	if describe {
		return Result{}, true, nil
	}
	switch statement.Action {
	case "create", "alter":
		if statement.PasswordParameter == 0 && len(args) != 0 {
			return Result{}, true, pgwire.NewError("42601", "statement does not accept value parameters")
		}
		password := statement.Password
		if statement.PasswordParameter > 0 {
			index := statement.PasswordParameter - 1
			if len(args) != statement.PasswordParameter {
				return Result{}, true, pgwire.NewError("42P02", "password parameter count does not match")
			}
			var ok bool
			password, ok = args[index].(string)
			if !ok {
				return Result{}, true, pgwire.NewError("22023", "password parameter must be text")
			}
		}
		if statement.Action == "create" {
			err = node.catalog.CreateAccount(ctx, account.Name, statement.User, password)
		} else {
			var replacement *string
			if statement.PasswordSpecified {
				replacement = &password
			}
			err = node.catalog.AlterAccount(ctx, account.Name, statement.User, replacement, statement.Disabled)
		}
	case "drop":
		if len(args) != 0 {
			return Result{}, true, pgwire.NewError("42601", "DROP USER does not accept value parameters")
		}
		err = node.catalog.DropAccount(ctx, account.Name, statement.User, statement.IfExists)
	case "grant", "revoke":
		if len(args) != 0 {
			return Result{}, true, pgwire.NewError("42601", "GRANT/REVOKE do not accept value parameters")
		}
		err = node.catalog.ChangePermissions(ctx, account.Name, statement.User, statement.Database, statement.Permissions, statement.Action == "revoke")
	}
	if err != nil {
		switch {
		case errors.Is(err, managed.ErrPermission), errors.Is(err, managed.ErrAuthentication):
			return Result{}, true, pgwire.NewError("42501", "root administrator required")
		case errors.Is(err, managed.ErrAccountExists):
			return Result{}, true, pgwire.NewError("42710", "account already exists")
		case errors.Is(err, managed.ErrAccountMissing):
			return Result{}, true, pgwire.NewError("42704", "account does not exist")
		case errors.Is(err, managed.ErrLastAdministrator):
			return Result{}, true, pgwire.NewError("55000", "cannot remove or disable the last enabled administrator")
		case errors.Is(err, managed.ErrAccountChanged):
			return Result{}, true, pgwire.NewError("40001", "account changed during update; retry")
		}
		return Result{}, true, postgresError(err)
	}
	tag := "CREATE USER"
	if statement.Action == "alter" {
		tag = "ALTER USER"
	} else if statement.Action == "drop" {
		tag = "DROP USER"
	} else if statement.Action != "create" {
		tag = strings.ToUpper(statement.Action)
	}
	return Result{CommandTag: tag}, true, nil
}

func (session *postgresSession) accountCommand(ctx context.Context, source string, parameters []pgwire.Parameter, describe bool) (pgwire.Result, bool, error) {
	if !kitdbsql.AccountCandidate(source) {
		return pgwire.Result{}, false, nil
	}
	_, handled, parseErr := kitdbsql.ParseAccountStatement(source)
	if !handled {
		return pgwire.Result{}, false, nil
	}
	if parseErr != nil {
		session.mu.Lock()
		if session.transaction != nil || session.maintenanceTransaction {
			session.failed = true
		}
		session.mu.Unlock()
		return pgwire.Result{}, true, pgwire.NewError("42601", "invalid account management statement")
	}
	if session.node == nil {
		return pgwire.Result{}, true, pgwire.NewError("0A000", "SQL account management requires a managed root")
	}
	session.mu.Lock()
	inTransaction := session.transaction != nil || session.maintenanceTransaction
	failed := session.failed
	session.mu.Unlock()
	if failed {
		return pgwire.Result{}, true, pgwire.NewError("25P02", "current transaction is aborted; issue ROLLBACK")
	}
	var args []any
	var err error
	if !describe {
		args, err = postgresParameters(parameters)
	}
	if err != nil {
		return pgwire.Result{}, true, err
	}
	result, handled, err := session.node.accountCommand(ctx, session.account, source, args, inTransaction, describe)
	if err != nil && inTransaction {
		session.mu.Lock()
		session.failed = true
		session.mu.Unlock()
	}
	return postgresResult(result), handled, err
}
