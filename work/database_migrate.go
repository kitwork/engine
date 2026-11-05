package work

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kitwork/engine/app"
	"github.com/kitwork/engine/compiler"
	kitdbsqlite "github.com/kitwork/engine/kitdb/sqlite"
	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
)

// kitwork migrate (KITWORK_DATABASE.md "Database là gốc", slice 4, 05/11/2026): the one way the engine changes a
// database's structure because of the code. It runs the .shared/schema/ of every app, holds each database the app
// declares tables on against its file (database_follows.go), says what it would add and what differs, and, asked to,
// adds it through the file's writer in one transaction (Maintain, by "kitwork migrate"): the history keeps it as a
// Barrier, the structure file beside the file is written again. It changes no column a table has: what differs is
// the code's to follow. A server holding the file (one writer per database) keeps kitwork migrate from it.

// migrateBy is who a migration's commit names in the history.
const migrateBy = "kitwork migrate"

// DatabaseMigration is what kitwork migrate does to one database file an app declares tables on.
type DatabaseMigration struct {
	App      string   // the app's folder under the root ("" for an app that is the root)
	Database string   // the file as the app names it: .database/application/kitwork.db, connection("tên")
	Path     string   // the file
	Add      []string // what it adds: a table, a column, a unique constraint
	Write    []string // what it writes besides, which statements run without: an index, a column's kind
	Differ   []string // what differs from the file, which it leaves: the code follows the database there
	tables   map[string]map[string]*ColumnSpec
	tenant   *Tenant
}

// Pending reports a migration that changes the file.
func (m *DatabaseMigration) Pending() bool { return len(m.Add) > 0 || len(m.Write) > 0 }

// Apply makes the file what the plan says, held against the file again inside the transaction that changes it.
func (m *DatabaseMigration) Apply(ctx context.Context) error {
	database, err := m.tenant.historyDatabase(m.Path)
	if err != nil {
		return err
	}
	_, err = database.Maintain(ctx, migrateBy, func(conn *sql.Conn) error {
		transaction, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		gap, err := checkDatabase(transaction, m.tables)
		if err == nil {
			err = applyGap(transaction, m.tables, gap)
		}
		if err != nil {
			_ = transaction.Rollback()
			return err
		}
		return transaction.Commit()
	})
	return err
}

// MigrationApps are the apps under root that declare databases: those with a .shared/schema/.
func MigrationApps(root string, layout RootLayout) []string {
	if layout.IsSingleApp() {
		if hasSchemaFolder(root) {
			return []string{RootAppIdentity}
		}
		return nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var identities []string
	for _, entry := range entries {
		if entry.IsDir() && entry.Name() != SitesDirName && hasSchemaFolder(filepath.Join(root, entry.Name())) {
			identities = append(identities, entry.Name())
		}
	}
	sort.Strings(identities)
	return identities
}

func hasSchemaFolder(appRoot string) bool {
	info, err := os.Stat(filepath.Join(appRoot, ".shared", "schema"))
	return err == nil && info.IsDir()
}

// PlanAppMigrations runs the .shared/schema/ of the app identity under root and holds every database it declares
// tables on against its file. The plans hold the files' writers until close: a file another process writes (a server
// running) is said as such, a file made from its structure file is made here, as any open of it would.
func PlanAppMigrations(root, identity string, layout RootLayout, shared *SharedEnvironment) ([]*DatabaseMigration, func(), error) {
	tenant := NewAppTenantWithRuntimeLayout(root, identity, layout, app.NewRuntime(identity))
	tenant.ownsApp = true
	tenant.SetSharedEnvironment(shared)
	closeTenant := func() {
		forgetSchemas(tenant)
		tenant.Close()
	}
	if err := tenant.prepareProgram(); err != nil {
		closeTenant()
		return nil, nil, err
	}
	declared, err := tenant.runSchemaFiles()
	if err != nil {
		closeTenant()
		return nil, nil, err
	}
	appFolder := identity
	if layout.IsSingleApp() {
		appFolder = ""
	}
	// a file is one plan, whatever schema files declare tables on it (two files may each declare connection())
	var plans []*DatabaseMigration
	byPath := map[string]*DatabaseMigration{}
	for _, proxy := range declared {
		tables, _ := proxy.schemaSnapshot()
		plan := byPath[proxy.sqlitePath]
		if plan == nil {
			plan = &DatabaseMigration{App: appFolder, Database: databaseLabelOf(tenant, proxy.dbName, proxy.sqlitePath),
				Path: proxy.sqlitePath, tables: map[string]map[string]*ColumnSpec{}, tenant: tenant}
			byPath[proxy.sqlitePath] = plan
			plans = append(plans, plan)
		}
		for name, columns := range tables {
			plan.tables[name] = columns
		}
	}
	sort.Slice(plans, func(i, j int) bool { return plans[i].Path < plans[j].Path })
	for _, plan := range plans {
		gap, err := tenant.planGap(plan.Path, plan.tables)
		if err != nil {
			closeTenant()
			return nil, nil, fmt.Errorf("%s: %w", plan.Database, err)
		}
		plan.Add, plan.Differ, plan.Write = gap.lines()
	}
	return plans, closeTenant, nil
}

// planGap holds tables against the file at path: through its writer when the file, or its structure file, is
// there; as a file of no table when neither is (nothing is made until the migration applies).
func (t *Tenant) planGap(path string, tables map[string]map[string]*ColumnSpec) (databaseGap, error) {
	_, fileErr := os.Stat(path)
	_, structureErr := os.Stat(path + ".sql")
	if errors.Is(fileErr, os.ErrNotExist) && errors.Is(structureErr, os.ErrNotExist) {
		return checkDatabase(nil, tables)
	}
	database, err := t.historyDatabase(path)
	if err != nil {
		return databaseGap{}, err
	}
	var gap databaseGap
	err = database.Read(context.Background(), func(snapshot *kitdbsqlite.Snapshot) error {
		var err error
		gap, err = checkDatabase(snapshot, tables)
		return err
	})
	return gap, err
}

// runSchemaFiles runs every file of the app's .shared/schema/ and gives the databases they declare that the engine
// keeps the history of, each file's as it ran (the registry keeps one declaration of a name). A file that does not
// compile or run is the error.
func (t *Tenant) runSchemaFiles() ([]*dbProxy, error) {
	folder := t.resolveApp(".shared", "schema")
	entries, err := os.ReadDir(folder)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var declared []*dbProxy
	seen := map[*dbProxy]bool{}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".work") {
			continue
		}
		relative := ".shared/schema/" + entry.Name()
		bytecode, err := compiler.CompileModuleFileInApp(t.resolveApp(), filepath.Join(folder, entry.Name()))
		if err == nil {
			if missing := t.missingEnvironment(bytecode); missing != nil {
				err = missing
			}
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", relative, err)
		}
		vm := runtime.New(bytecode.Program)
		kitworkFunc := value.NewFunc(func(args ...value.Value) value.Value {
			return value.New(&KitWork{tenant: t, vm: vm})
		})
		globals := make(map[string]value.Value, len(t.vm.Globals)+1)
		for name, item := range t.vm.Globals {
			globals[name] = item
		}
		globals[kitwork] = kitworkFunc
		vm.Builtins = []value.Value{kitworkFunc}
		vm.Globals = globals
		vm.MaxEnergy = t.MaxEnergy
		done := loadingFile(vm)
		result := vm.Run()
		done()
		if result.K == value.Invalid {
			return nil, fmt.Errorf("%s: %s", relative, result.Text())
		}
		for _, proxy := range schemasOf(t) {
			if !seen[proxy] {
				seen[proxy] = true
				declared = append(declared, proxy)
			}
		}
	}
	return declared, nil
}

// schemasOf are the databases a tenant declared that the engine keeps the history of (the app's own, the host's of
// connection("tên")), by file.
func schemasOf(t *Tenant) []*dbProxy {
	schemaRegMu.Lock()
	var proxies []*dbProxy
	for _, proxy := range schemaReg {
		if proxy.tenant == t && proxy.sqlitePath != "" {
			proxies = append(proxies, proxy)
		}
	}
	schemaRegMu.Unlock()
	sort.Slice(proxies, func(i, j int) bool { return proxies[i].sqlitePath < proxies[j].sqlitePath })
	return proxies
}

// forgetSchemas takes a tenant's declarations out of the registry.
func forgetSchemas(t *Tenant) {
	schemaRegMu.Lock()
	defer schemaRegMu.Unlock()
	for key, proxy := range schemaReg {
		if proxy.tenant == t {
			delete(schemaReg, key)
		}
	}
}
