package relational

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"

	kitdb "github.com/kitwork/engine/kitdb"
	"github.com/kitwork/engine/kitdb/managed"
	"github.com/kitwork/engine/kitdb/pgwire"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

// A bounded security catalog record travels with backup/restore of this DB.
// It contains role names and stable table IDs, never login accounts or mappings.
const localRolesKey = "kitdb/catalog/local-roles/v1"

type localRole struct {
	ID     string   `json:"id"`
	Name   string   `json:"name"`
	Select []string `json:"select"`
	Insert []string `json:"insert,omitempty"`
	Update []string `json:"update,omitempty"`
	Delete []string `json:"delete,omitempty"`
}

func (role *localRole) grants(action string) *[]string {
	switch action {
	case "select":
		return &role.Select
	case "insert":
		return &role.Insert
	case "update":
		return &role.Update
	case "delete":
		return &role.Delete
	}
	panic("invalid local role privilege")
}

type localRoleCatalog struct {
	Version int         `json:"version"`
	Roles   []localRole `json:"roles"`
}
type localRolePolicy struct{ Roles []string }

func validLocalRole(name string) bool {
	if len(name) < 1 || len(name) > 63 {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_') {
			return false
		}
	}
	return true
}

func readLocalRoles(db interface {
	Get([]byte) ([]byte, bool, error)
}) (localRoleCatalog, error) {
	data, found, err := db.Get([]byte(localRolesKey))
	if err != nil {
		return localRoleCatalog{}, err
	}
	if !found {
		return localRoleCatalog{Version: 1}, nil
	}
	invalid := fmt.Errorf("kitdb: invalid local role catalog")
	if len(data) > 1<<20 {
		return localRoleCatalog{}, invalid
	}
	var catalog localRoleCatalog
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if d.Decode(&catalog) != nil || d.Decode(new(any)) != io.EOF || (catalog.Version != 1 && catalog.Version != 2) || len(catalog.Roles) > 128 {
		return localRoleCatalog{}, invalid
	}
	seen := map[string]bool{}
	roleIDs := map[string]bool{}
	for _, role := range catalog.Roles {
		id, e := hex.DecodeString(role.ID)
		if e != nil || len(id) != 16 || roleIDs[role.ID] || role.ID != strings.ToLower(role.ID) {
			return localRoleCatalog{}, invalid
		}
		roleIDs[role.ID] = true
		if !validLocalRole(role.Name) || seen[role.Name] || catalog.Version == 1 && (len(role.Insert)+len(role.Update)+len(role.Delete) != 0) {
			return localRoleCatalog{}, invalid
		}
		seen[role.Name] = true
		for _, action := range []string{"select", "insert", "update", "delete"} {
			ids := map[string]bool{}
			grants := *role.grants(action)
			if len(grants) > 4096 {
				return localRoleCatalog{}, invalid
			}
			for _, id := range grants {
				b, e := hex.DecodeString(id)
				if e != nil || len(b) != 16 || ids[id] || id != strings.ToLower(id) {
					return localRoleCatalog{}, invalid
				}
				ids[id] = true
			}
		}
	}
	return catalog, nil
}

func (engine *Engine) localRolePolicy(account managed.UserInfo, database string) (*localRolePolicy, error) {
	var names []string
	for _, g := range account.Grants {
		if strings.EqualFold(g.Database, database) {
			names = g.LocalRoles
		}
	}
	if len(names) == 0 {
		return nil, nil
	}
	engine.mu.RLock()
	defer engine.mu.RUnlock()
	if err := engine.readyLocked(); err != nil {
		return nil, err
	}
	c, err := readLocalRoles(engine.database)
	if err != nil {
		return nil, err
	}
	policy := &localRolePolicy{Roles: slices.Clone(names)}
	for _, id := range names {
		i := slices.IndexFunc(c.Roles, func(r localRole) bool { return r.ID == id })
		if i < 0 {
			return nil, pgwire.NewError("42501", "mapped database role is unavailable")
		}
	}
	return policy, nil
}

func (node *PostgresNode) localRoleCommand(ctx context.Context, engine *Engine, database string, account managed.UserInfo, source string, inTx, describe bool) (Result, bool, error) {
	st, handled, err := kitdbsql.ParseLocalRoleStatement(source)
	if !handled {
		return Result{}, false, nil
	}
	if err != nil {
		return Result{}, true, pgwire.NewError("42601", "invalid local role statement")
	}
	deny := pgwire.NewError("42501", "enabled root administrator required")
	if engine == nil || node.catalog == nil || !node.catalog.AuthEnabled() {
		return Result{}, true, pgwire.NewError("0A000", "local role management requires a selected managed database")
	}
	node.mu.Lock()
	closed := node.closed
	node.mu.Unlock()
	if closed {
		return Result{}, true, pgwire.NewError("08003", "node is closed")
	}
	if !account.Admin {
		return Result{}, true, deny
	}
	live := false
	for _, u := range node.catalog.SessionAccounts() {
		if account.SameAccount(u) && u.Admin && !u.Disabled {
			live = true
		}
	}
	if !live {
		return Result{}, true, deny
	}
	ctx = managed.WithSessionAccount(ctx, account)
	if node.readOnly {
		return Result{}, true, pgwire.NewError("25006", "node is read-only")
	}
	if inTx {
		return Result{}, true, pgwire.NewError("25001", "role management requires autocommit")
	}
	if err := ctx.Err(); err != nil {
		return Result{}, true, err
	}
	if !validLocalRole(st.Role) {
		return Result{}, true, pgwire.NewError("22023", "invalid role name")
	}
	engine.mu.RLock()
	defer engine.mu.RUnlock()
	if err := engine.readyLocked(); err != nil {
		return Result{}, true, err
	}
	engine.writeMu.Lock()
	defer engine.writeMu.Unlock()
	c, err := readLocalRoles(engine.database)
	if err != nil {
		return Result{}, true, err
	}
	i := slices.IndexFunc(c.Roles, func(r localRole) bool { return r.Name == st.Role })
	if st.Action == "create" {
		if i >= 0 {
			return Result{}, true, pgwire.NewError("42710", "local role already exists")
		}
		if len(c.Roles) >= 128 {
			return Result{}, true, pgwire.NewError("54000", "local role limit")
		}
		var id [16]byte
		if _, err := rand.Read(id[:]); err != nil {
			return Result{}, true, err
		}
		c.Roles = append(c.Roles, localRole{ID: hex.EncodeToString(id[:]), Name: st.Role})
	} else {
		if i < 0 {
			return Result{}, true, pgwire.NewError("42704", "local role does not exist")
		}
		switch st.Action {
		case "drop":
			for _, u := range node.catalog.Users() {
				for _, g := range u.Grants {
					if strings.EqualFold(g.Database, database) && slices.Contains(g.LocalRoles, c.Roles[i].ID) {
						return Result{}, true, pgwire.NewError("55006", "revoke account mappings before dropping this role")
					}
				}
			}
			c.Roles = slices.Delete(c.Roles, i, i+1)
		case "select", "insert", "update", "delete":
			schema, e := engine.schemaLocked(st.Table)
			if e != nil {
				return Result{}, true, e
			}
			grants := c.Roles[i].grants(st.Action)
			if st.Revoke {
				*grants = slices.DeleteFunc(*grants, func(id string) bool { return id == schema.ID })
			} else if !slices.Contains(*grants, schema.ID) {
				if len(*grants) >= 4096 {
					return Result{}, true, pgwire.NewError("54000", "role table limit")
				}
				*grants = append(*grants, schema.ID)
			}
			if st.Action != "select" {
				c.Version = 2
			}
		case "map":
			if !strings.EqualFold(database, st.Database) {
				return Result{}, true, pgwire.NewError("42501", "select the mapping's database first")
			}
			if describe {
				return Result{}, true, nil
			}
			if err := node.catalog.ChangeLocalRole(ctx, account.Name, st.User, database, c.Roles[i].ID, st.Revoke); err != nil {
				return Result{}, true, pgwire.NewError("42501", "role mapping requires a connect-only account grant")
			}
			return Result{CommandTag: "GRANT ROLE"}, true, nil
		}
	}
	if describe {
		return Result{}, true, nil
	}
	if err := ctx.Err(); err != nil {
		return Result{}, true, err
	}
	data, err := json.Marshal(c)
	if err != nil {
		return Result{}, true, err
	}
	if len(data) > 1<<20 {
		return Result{}, true, pgwire.NewError("54000", "local role catalog size limit")
	}
	if err := node.catalog.RequireLocalRoleSupport(ctx, account.Name); err != nil {
		return Result{}, true, err
	}
	tx, err := engine.database.Begin()
	if err != nil {
		return Result{}, true, err
	}
	defer tx.Rollback()
	if err = tx.Put([]byte(localRolesKey), data); err == nil {
		_, err = tx.Commit()
	}
	return Result{CommandTag: "LOCAL ROLE"}, true, err
}

func safeLocalExpression(expr *kitdbsql.ExpressionPlan) bool {
	if expr == nil {
		return true
	}
	if expr.Kind == "function" || expr.Function != nil {
		return false
	}
	for i := range expr.Arguments {
		if !safeLocalExpression(&expr.Arguments[i]) {
			return false
		}
	}
	return true
}

// Reads are authorized against the exact catalog snapshot executing the query.
// v1 refuses indirect reads rather than guessing access behind functions/CTEs.
func (engine *Engine) executeLocalRead(ctx context.Context, policy *localRolePolicy, active *Transaction, source string, args []any, describe bool) (Result, bool, error) {
	env, err := kitdbsql.ParseEnvelope(source)
	if err != nil {
		return Result{}, false, nil
	}
	if env.Kind != kitdbsql.StatementSelect && env.Kind != kitdbsql.StatementExplain {
		return Result{}, false, nil
	}
	if normalizePostgresSQL(source) == "select 1" {
		return Result{}, false, nil
	}
	st, err := kitdbsql.ParseStatement(source)
	if err != nil {
		return Result{}, true, err
	}
	plan := st.Select
	if plan == nil {
		plan = st.Explain
	}
	deny := pgwire.NewError("42501", "table SELECT permission denied or query outside local-role profile")
	if !safeLocalSelect(plan) {
		return Result{}, true, deny
	}
	tx := active
	if tx == nil {
		tx, err = engine.BeginTransaction(ctx, TransactionOptions{ReadOnly: true})
		if err != nil {
			return Result{}, true, err
		}
		defer tx.Rollback()
	}
	schema, err := schemaFromCatalog(tx.catalog, plan.Table)
	if err != nil {
		return Result{}, true, deny
	}
	roles, err := readLocalRoles(tx.snapshot)
	if err != nil {
		return Result{}, true, err
	}
	allowed := false
	for _, role := range roles.Roles {
		if slices.Contains(policy.Roles, role.ID) && slices.Contains(role.Select, schema.ID) {
			allowed = true
		}
	}
	if !allowed {
		return Result{}, true, deny
	}
	if describe {
		cols, e := tx.Describe(ctx, source)
		return Result{Columns: cols}, true, e
	}
	result, err := tx.Execute(ctx, source, args...)
	return result, true, err
}

func safeLocalSelect(plan *kitdbsql.SelectStatement) bool {
	if plan == nil || plan.Table == "" || plan.Source != nil || len(plan.CommonTables) > 0 || len(plan.Joins) > 0 || len(plan.SetOperations) > 0 || plan.Search != nil || !safeLocalExpression(plan.Predicate) || !safeLocalExpression(plan.Having) {
		return false
	}
	for _, p := range plan.Projection {
		if !safeLocalExpression(p.Expression) {
			return false
		}
	}
	for _, o := range plan.Order {
		if !safeLocalExpression(o.Expression) {
			return false
		}
	}
	return true
}

// Drop removes grants in the same WAL transaction as the table definition.
// StableSchemaID is name-derived; this prevents same-name recreation inheritance.
func clearDroppedTableRoles(db *kitdb.DB, tx *kitdb.Tx, tableID string) error {
	c, err := readLocalRoles(db)
	if err != nil {
		return err
	}
	changed := false
	for i := range c.Roles {
		for _, action := range []string{"select", "insert", "update", "delete"} {
			grants := c.Roles[i].grants(action)
			before := len(*grants)
			*grants = slices.DeleteFunc(*grants, func(id string) bool { return id == tableID })
			changed = changed || before != len(*grants)
		}
	}
	if !changed {
		return nil
	}
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return tx.Put([]byte(localRolesKey), data)
}

func (session *postgresSession) localRoleCommand(ctx context.Context, source string, describe bool, parameterCount int) (pgwire.Result, bool, error) {
	_, handled, _ := kitdbsql.ParseLocalRoleStatement(source)
	if !handled {
		return pgwire.Result{}, false, nil
	}
	if parameterCount != 0 {
		return pgwire.Result{}, true, pgwire.NewError("42601", "local role statements do not accept value parameters")
	}
	if session.node == nil {
		return pgwire.Result{}, true, pgwire.NewError("0A000", "local roles require a managed node")
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.failed {
		return pgwire.Result{}, true, pgwire.NewError("25P02", "current transaction is aborted")
	}
	result, handled, err := session.node.localRoleCommand(ctx, session.authenticator.engine, session.authenticator.database, session.account, source, session.transaction != nil || session.maintenanceTransaction, describe)
	if err != nil && (session.transaction != nil || session.maintenanceTransaction) {
		session.failed = true
	}
	return postgresResult(result), handled, postgresError(err)
}

func (session *postgresSession) localStatement(ctx context.Context, source string, parameters []pgwire.Parameter, describe bool) (pgwire.Result, bool, error) {
	if session.authenticator.localPolicy == nil {
		return pgwire.Result{}, false, nil
	}
	envelope, err := kitdbsql.ParseEnvelope(source)
	if err != nil || (envelope.Kind != kitdbsql.StatementSelect && envelope.Kind != kitdbsql.StatementExplain && envelope.Kind != kitdbsql.StatementInsert && envelope.Kind != kitdbsql.StatementUpdate && envelope.Kind != kitdbsql.StatementDelete) {
		return pgwire.Result{}, false, nil
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.failed {
		return pgwire.Result{}, true, pgwire.NewError("25P02", "current transaction is aborted")
	}
	var args []any
	err = nil
	if !describe {
		args, err = postgresParameters(parameters)
		if err != nil {
			return pgwire.Result{}, true, err
		}
	}
	result, handled, err := session.authenticator.engine.executeLocalStatement(ctx, session.authenticator.localPolicy, session.transaction, source, args, describe, session.authenticator.readOnly)
	if err != nil && session.transaction != nil {
		session.failed = true
	}
	return postgresResult(result), handled, postgresError(err)
}
