package relational

import (
	"context"
	"slices"
	"strings"

	"github.com/kitwork/engine/kitdb/managed"
	"github.com/kitwork/engine/kitdb/pgwire"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

func isLocalRoleCatalogQuery(source string) bool {
	if !kitdbsql.ContainsFold(source, "kitdb_catalog") {
		return false
	}
	tokens, err := kitdbsql.Lex(source)
	if err != nil {
		return false
	}
	for i := 0; i+2 < len(tokens); i++ {
		if tokens[i].Kind == kitdbsql.TokenIdentifier && strings.EqualFold(tokens[i].Text, "kitdb_catalog") && tokens[i+1].Text == "." && tokens[i+2].Kind == kitdbsql.TokenIdentifier {
			switch strings.ToLower(tokens[i+2].Text) {
			case "roles", "table_grants", "role_members", "effective_privileges":
				return true
			}
		}
	}
	return false
}

type databaseCatalogScope struct {
	engine      *Engine
	database    string
	active      *Transaction
	permissions *managed.DatabasePermissions
	local       *localRolePolicy
	readOnly    bool
}

func (node *PostgresNode) localRoleCatalogQuery(ctx context.Context, account managed.UserInfo, scope databaseCatalogScope, source string, args []any) (Result, bool, error) {
	if !isLocalRoleCatalogQuery(source) {
		return Result{}, false, nil
	}
	if err := accountCatalogReadOnly(source); err != nil {
		return Result{}, true, err
	}
	if scope.engine == nil || node.catalog == nil || !node.catalog.AuthEnabled() {
		return Result{}, true, pgwire.NewError("0A000", "database role views require a selected authenticated managed database")
	}
	node.mu.Lock()
	closed := node.closed
	node.mu.Unlock()
	if closed {
		return Result{}, true, pgwire.NewError("08003", "node is closed")
	}
	st, err := kitdbsql.ParseStatement(source)
	if err != nil {
		return Result{}, true, err
	}
	if st.Select == nil {
		return Result{}, true, pgwire.NewError("0A000", "single-view SELECT required")
	}
	view := strings.ToLower(st.Select.Table)
	schema := kitdbsql.Schema{Name: view}
	add := func(name, kind string) { schema.Fields = append(schema.Fields, kitdbsql.Field{Name: name, Kind: kind}) }
	add("database_name", "text")
	switch view {
	case "roles":
		add("role_id", "text")
		add("role_name", "text")
	case "table_grants":
		for _, name := range []string{"role_id", "role_name", "table_id", "table_name"} {
			add(name, "text")
		}
		add("table_exists", "bool")
		add("can_select", "bool")
		add("can_insert", "bool")
		add("can_update", "bool")
		add("can_delete", "bool")
	case "role_members":
		for _, name := range []string{"username", "role_id", "role_name"} {
			add(name, "text")
		}
		add("disabled", "bool")
		add("role_available", "bool")
	case "effective_privileges":
		for _, name := range []string{"username", "table_id", "table_name", "policy_scope", "select_source"} {
			add(name, "text")
		}
		for _, name := range []string{"can_connect", "can_select", "can_insert", "can_update", "can_delete", "can_schema"} {
			add(name, "bool")
		}
	default:
		return Result{}, true, pgwire.NewError("0A000", "database role view cannot be combined with another relation")
	}
	tx := scope.active
	if tx == nil {
		tx, err = scope.engine.BeginTransaction(ctx, TransactionOptions{ReadOnly: true})
		if err != nil {
			return Result{}, true, err
		}
		defer tx.Rollback()
	}
	roles, err := readLocalRoles(tx.snapshot)
	if err != nil {
		return Result{}, true, err
	}
	users := node.catalog.SessionAccounts()
	admin := false
	visible := map[string]bool{}
	for _, user := range users {
		if account.SameAccount(user) {
			admin = account.Admin && user.Admin && !user.Disabled
			for _, g := range user.Grants {
				if strings.EqualFold(g.Database, scope.database) {
					for _, id := range g.LocalRoles {
						visible[id] = true
					}
				}
			}
		}
	}
	if scope.local != nil {
		for _, id := range scope.local.Roles {
			visible[id] = true
		}
	}
	byRole := map[string]localRole{}
	roleTables := map[string]map[string]uint8{}
	for _, role := range roles.Roles {
		byRole[role.ID] = role
		roleTables[role.ID] = map[string]uint8{}
		for bit, action := range []string{"select", "insert", "update", "delete"} {
			for _, id := range *role.grants(action) {
				roleTables[role.ID][id] |= 1 << bit
			}
		}
	}
	localPermission := func(ids []string, tableID string, mask uint8) bool {
		for _, id := range ids {
			if roleTables[id][tableID]&mask != 0 {
				return true
			}
		}
		return false
	}
	sessionPermissions := account.Permissions(scope.database)
	if scope.permissions != nil {
		sessionPermissions = *scope.permissions
	}
	var sessionRoles []string
	if scope.local != nil {
		sessionRoles = scope.local.Roles
	}
	visibleTables := map[string]bool{}
	if !admin {
		mark := func(p managed.DatabasePermissions, ids []string, readOnly bool) {
			if !p.Connect {
				return
			}
			for _, table := range tx.catalog.Structs {
				if p.Read || localPermission(ids, table.ID, 1) || !readOnly && (p.Write || p.Schema || localPermission(ids, table.ID, 14)) {
					visibleTables[table.ID] = true
				}
			}
		}
		mark(sessionPermissions, sessionRoles, scope.readOnly)
		for _, user := range users {
			if !account.SameAccount(user) {
				continue
			}
			var ids []string
			if !user.Disabled {
				for _, g := range user.Grants {
					if strings.EqualFold(g.Database, scope.database) {
						ids = g.LocalRoles
					}
				}
			}
			mark(user.Permissions(scope.database), ids, node.readOnly)
		}
	}
	tables := map[string]string{}
	for _, table := range tx.catalog.Structs {
		tables[table.ID] = table.Name
	}
	result, err := queryCatalogView(ctx, source, args, schema, node.maximumResults, func(emit func(map[string]any) error) error {
		switch view {
		case "roles", "table_grants":
			for _, role := range roles.Roles {
				if !admin && !visible[role.ID] {
					continue
				}
				if view == "roles" {
					if err := emit(map[string]any{"database_name": scope.database, "role_id": role.ID, "role_name": role.Name}); err != nil {
						return err
					}
					continue
				}
				var grantTables []string
				for id := range roleTables[role.ID] {
					grantTables = append(grantTables, id)
				}
				slices.Sort(grantTables)
				for _, id := range grantTables {
					name, exists := tables[id]
					flags := roleTables[role.ID][id]
					if err := emit(map[string]any{"database_name": scope.database, "role_id": role.ID, "role_name": role.Name, "table_id": id, "table_name": name, "table_exists": exists, "can_select": flags&1 != 0, "can_insert": flags&2 != 0, "can_update": flags&4 != 0, "can_delete": flags&8 != 0}); err != nil {
						return err
					}
				}
			}
		case "role_members":
			for _, user := range users {
				if !admin && !account.SameAccount(user) {
					continue
				}
				for _, g := range user.Grants {
					if !strings.EqualFold(g.Database, scope.database) {
						continue
					}
					for _, id := range g.LocalRoles {
						role, exists := byRole[id]
						if err := emit(map[string]any{"database_name": scope.database, "username": user.Name, "role_id": id, "role_name": role.Name, "disabled": user.Disabled, "role_available": exists}); err != nil {
							return err
						}
					}
				}
			}
		case "effective_privileges":
			emitUser := func(user managed.UserInfo, p managed.DatabasePermissions, ids []string, label string) error {
				readOnly := node.readOnly
				schemaAllowed := true
				if label == "session" {
					readOnly = scope.readOnly
					schemaAllowed = scope.active == nil
					if scope.active != nil && scope.active.readOnly {
						readOnly = true
					}
				}
				for _, table := range tx.catalog.Structs {
					localRead := localPermission(ids, table.ID, 1)
					selectable := p.Connect && (p.Read || localRead)
					write := p.Connect && p.Write && !readOnly
					insert := write || p.Connect && !readOnly && localPermission(ids, table.ID, 2)
					update := write || p.Connect && !readOnly && localPermission(ids, table.ID, 4)
					deleteAllowed := write || p.Connect && !readOnly && localPermission(ids, table.ID, 8)
					ddl := p.Connect && p.Schema && !readOnly && schemaAllowed
					// Ordinary clients must not enumerate tables on which they have no rights.
					if !admin && !visibleTables[table.ID] {
						continue
					}
					source := "none"
					if selectable {
						if p.Read {
							source = "database"
						} else {
							source = "local_role"
						}
					}
					if err := emit(map[string]any{"database_name": scope.database, "username": user.Name, "table_id": table.ID, "table_name": table.Name, "policy_scope": label, "select_source": source, "can_connect": p.Connect, "can_select": selectable, "can_insert": insert, "can_update": update, "can_delete": deleteAllowed, "can_schema": ddl}); err != nil {
						return err
					}
				}
				return nil
			}
			if err := emitUser(account, sessionPermissions, sessionRoles, "session"); err != nil {
				return err
			}
			for _, user := range users {
				if !admin && !account.SameAccount(user) {
					continue
				}
				var ids []string
				for _, g := range user.Grants {
					if strings.EqualFold(g.Database, scope.database) {
						ids = g.LocalRoles
					}
				}
				if user.Disabled {
					ids = nil
				}
				if err := emitUser(user, user.Permissions(scope.database), ids, "configured"); err != nil {
					return err
				}
			}
		}
		return nil
	})
	return result, true, err
}
