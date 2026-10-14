package relational

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/kitwork/engine/kitdb/managed"
	"github.com/kitwork/engine/kitdb/pgwire"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

func isAccountCatalogQuery(source string) bool {
	if isLocalRoleCatalogQuery(source) {
		return true
	}
	const namespace = "kitdb_catalog"
	candidate := false
	for i := 0; i+len(namespace) <= len(source); i++ {
		if (source[i] == 'k' || source[i] == 'K') && strings.EqualFold(source[i:i+len(namespace)], namespace) {
			candidate = true
			break
		}
	}
	if !candidate {
		return false
	}
	tokens, err := kitdbsql.Lex(source)
	if err != nil {
		return false
	}
	for i := 0; i+2 < len(tokens); i++ {
		if tokens[i].Kind == kitdbsql.TokenIdentifier && strings.EqualFold(tokens[i].Text, "kitdb_catalog") &&
			tokens[i+1].Text == "." && tokens[i+2].Kind == kitdbsql.TokenIdentifier &&
			(strings.EqualFold(tokens[i+2].Text, "users") || strings.EqualFold(tokens[i+2].Text, "grants") || strings.EqualFold(tokens[i+2].Text, "sessions")) {
			return true
		}
	}
	return false
}

func accountCatalogReadOnly(source string) error {
	envelope, err := kitdbsql.ParseEnvelope(source)
	if err != nil {
		return err
	}
	if envelope.Kind != kitdbsql.StatementSelect {
		return pgwire.NewError("42501", "account catalog views are read-only")
	}
	return nil
}

// Visibility is applied to safe UserInfo before evaluating any user SQL.
// No verifier, salt or filesystem path is available to the query evaluator.
func (node *PostgresNode) accountCatalogQuery(account managed.UserInfo, source string, parameters []any) (Result, bool, error) {
	if !isAccountCatalogQuery(source) {
		return Result{}, false, nil
	}
	if err := accountCatalogReadOnly(source); err != nil {
		return Result{}, true, err
	}
	node.mu.Lock()
	closed := node.closed
	node.mu.Unlock()
	if closed {
		return Result{}, true, pgwire.NewError("08003", "node is closed")
	}
	if node.catalog == nil {
		return Result{}, true, pgwire.NewError("0A000", "account views require a managed root")
	}
	statement, err := kitdbsql.ParseStatement(source)
	if err != nil {
		return Result{}, true, err
	}
	plan := statement.Select
	if plan == nil || plan.Source != nil || len(plan.CommonTables) != 0 || len(plan.Joins) != 0 ||
		len(plan.SetOperations) != 0 || len(plan.GroupBy) != 0 || plan.Having != nil ||
		plan.Search != nil || plan.Distinct || plan.HasAfter {
		return Result{}, true, pgwire.NewError("0A000", "account views support single-view SELECT, WHERE, ORDER BY, LIMIT and OFFSET")
	}
	view := strings.ToLower(plan.Table)
	if view != "users" && view != "grants" && view != "sessions" {
		return Result{}, true, pgwire.NewError("0A000", "account view cannot be combined with another relation")
	}
	schema := kitdbsql.Schema{Name: view}
	addField := func(name, kind string) { schema.Fields = append(schema.Fields, kitdbsql.Field{Name: name, Kind: kind}) }
	addField("username", "text")
	if view == "sessions" {
		addField("session_id", "bigint")
		addField("terminating", "bool")
		for _, name := range []string{"database_name", "adapter", "started_at", "transaction_state"} {
			addField(name, "text")
		}
	} else if view == "users" {
		addField("is_admin", "bool")
		addField("disabled", "bool")
	} else {
		addField("database_name", "text")
		addField("role", "text")
		for _, name := range []string{"can_connect", "can_read", "can_write", "can_schema"} {
			addField(name, "bool")
		}
	}
	count := len(plan.Projection) == 1 && plan.Projection[0].Count
	var columns []Column
	var names []string
	if count {
		if plan.Projection[0].Name != "" && plan.Projection[0].Name != "*" {
			return Result{}, true, pgwire.NewError("0A000", "account view only supports COUNT(*)")
		}
		label := plan.Projection[0].Alias
		if label == "" {
			label = "count"
		}
		columns = []Column{{Name: label, Kind: "bigint"}}
	} else {
		columns, names, err = bindProjection(schema, plan.Projection)
		if err != nil {
			return Result{}, true, err
		}
	}
	predicate, err := bindPredicate(schema, plan.Predicate, parameters)
	if err != nil {
		return Result{}, true, err
	}
	for _, order := range plan.Order {
		if _, _, ok := schema.FieldByName(unqualifiedColumn(order.Column)); !ok {
			return Result{}, true, fmt.Errorf("account view has no order field %q", order.Column)
		}
	}
	users := node.catalog.SessionAccounts()
	liveAdmin := false
	for _, u := range users {
		if account.SameAccount(u) && u.Admin && !u.Disabled {
			liveAdmin = account.Admin
			break
		}
	}
	if !node.catalog.AuthEnabled() {
		users = []managed.UserInfo{account}
	}
	databases := node.catalog.Catalog().Databases
	var rows []map[string]any
	visited := 0
	appendRow := func(row map[string]any) error {
		visited++
		if visited > 65536 {
			return pgwire.NewError("54000", "account catalog candidate limit exceeded")
		}
		matches, err := predicateMatches(row, predicate)
		if err != nil || !matches {
			return err
		}
		if len(rows) >= node.maximumResults {
			return pgwire.NewError("54000", "account catalog result exceeds row limit; narrow WHERE")
		}
		rows = append(rows, row)
		return nil
	}
	for _, user := range users {
		if view == "sessions" {
			break
		}
		if !liveAdmin && !account.SameAccount(user) {
			continue
		}
		if view == "users" {
			if err := appendRow(map[string]any{"username": user.Name, "is_admin": user.Admin, "disabled": user.Disabled}); err != nil {
				return Result{}, true, err
			}
			continue
		}
		for _, db := range databases {
			role := ""
			if user.Admin {
				role = "admin"
			} else {
				for _, grant := range user.Grants {
					if strings.EqualFold(grant.Database, db.Name) {
						role = grant.Role
						if role == "" {
							role = "custom"
						}
						break
					}
				}
			}
			if role == "" {
				continue
			}
			p := user.Permissions(db.Name)
			if err := appendRow(map[string]any{"username": user.Name, "database_name": db.Name, "role": role,
				"can_connect": p.Connect, "can_read": p.Read, "can_write": p.Write, "can_schema": p.Schema}); err != nil {
				return Result{}, true, err
			}
		}
	}
	if view == "sessions" {
		for _, row := range node.sessionRows(account) {
			if err := appendRow(row); err != nil {
				return Result{}, true, err
			}
		}
	}
	if count {
		if plan.Offset > 0 || plan.HasLimit && plan.Limit == 0 {
			return Result{Columns: columns, CommandTag: "SELECT 0"}, true, nil
		}
		rowsCount := int64(len(rows))
		return Result{Columns: columns, Rows: [][]any{{rowsCount}}, CommandTag: "SELECT 1"}, true, nil
	}
	sort.SliceStable(rows, func(i, j int) bool {
		for _, order := range plan.Order {
			field, _, _ := schema.FieldByName(unqualifiedColumn(order.Column))
			compare := compareValues(rows[i][field], rows[j][field])
			if compare != 0 {
				if order.Descending {
					return compare > 0
				}
				return compare < 0
			}
		}
		return false
	})
	start := min(plan.Offset, len(rows))
	end := len(rows)
	if plan.HasLimit {
		end = start + min(plan.Limit, len(rows)-start)
	}
	result := Result{Columns: columns, CommandTag: fmt.Sprintf("SELECT %d", end-start)}
	for _, row := range rows[start:end] {
		result.Rows = append(result.Rows, projectRow(schema, row, names))
	}
	return result, true, nil
}

func (session *postgresSession) accountCatalogQuery(ctx context.Context, source string, parameters []pgwire.Parameter, describe bool) (pgwire.Result, bool, error) {
	if !isAccountCatalogQuery(source) {
		return pgwire.Result{}, false, nil
	}
	if session.node == nil {
		return pgwire.Result{}, true, pgwire.NewError("0A000", "account views require a managed root")
	}
	var values []any
	var err error
	if describe {
		source, err = bindFunctionCatalogParameters(source, nil, true)
	} else {
		values, err = postgresParameters(parameters)
	}
	if err != nil {
		return pgwire.Result{}, true, err
	}
	if isLocalRoleCatalogQuery(source) {
		session.mu.Lock()
		defer session.mu.Unlock()
		result, handled, err := session.node.localRoleCatalogQuery(ctx, session.account, databaseCatalogScope{engine: session.authenticator.engine, database: session.authenticator.database, active: session.transaction, permissions: session.authenticator.permissions, local: session.authenticator.localPolicy, readOnly: session.authenticator.readOnly}, source, values)
		return postgresResult(result), handled, postgresError(err)
	}
	result, handled, err := session.node.accountCatalogQuery(session.account, source, values)
	return postgresResult(result), handled, postgresError(err)
}
