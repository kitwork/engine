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
	"github.com/kitwork/engine/kitdb/gateway"
	kitdbsqlite "github.com/kitwork/engine/kitdb/sqlite"
	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
)

// kitwork migrate (KITWORK_DATABASE.md "Database là gốc", slice 4, 05/11/2026): the one way the engine changes a
// database's structure because of the code. It runs the .shared/schema/ of every app, holds each database the app
// declares tables on against its file (database_follows.go), says what it would add and what differs, and, asked to,
// adds it through the file's writer in one transaction (Maintain, by "kitwork migrate"): the history keeps it as a
// Barrier, the structure file beside the file is written again. It changes no column a table has: what differs is
// the code's to follow. A file another process holds (a server running: one writer per database) is planned reading
// only and marked Held; it applies through that server's database port when the engine gives the plan a way there
// (ThroughPort, kitwork.go): the plan's PostgreSQL statements, sent as an admin.

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
	// Statements are what it runs, as PostgreSQL writes them (what one sends through the port), or SQLite's after
	// "-- SQLite:" where PostgreSQL's words cannot say it (database_postgres.go)
	Statements []string
	// Held is a file another process writes, a server running: the plan was read without its writer
	Held bool
	// Through says the port a plan applies through (ThroughPort); Left is what applying through it could not write,
	// which kitwork migrate writes with the server stopped (a .searchable() weight, a kind of a column already there)
	Through string
	Left    []string
	schema  string // the file's schema as the port names it ("" public)
	// postgres are the statements as sent through the port; native tells that one of them has SQLite's words only
	postgres []string
	native   bool
	send     func(ctx context.Context, statements []string) error
	tables   map[string]map[string]*ColumnSpec
	tenant   *Tenant
}

// ThroughPort makes the plan apply through a database port: send runs the plan's PostgreSQL statements there, as an
// admin, through, in words, the port and the database it names.
func (m *DatabaseMigration) ThroughPort(through string, send func(ctx context.Context, statements []string) error) {
	m.Through, m.send = through, send
}

// applyThroughPort sends the plan's statements to the port and holds the file against the tables again, reading only;
// what the port could not write is Left.
func (m *DatabaseMigration) applyThroughPort(ctx context.Context) error {
	if m.native {
		return fmt.Errorf("%s: kế hoạch có câu chỉ SQLite nói được (sau -- SQLite:), không đi qua cổng: dừng server rồi chạy lại kitwork migrate", m.Database)
	}
	if err := m.send(ctx, m.postgres); err != nil {
		return fmt.Errorf("%s, qua %s: %w", m.Database, m.Through, err)
	}
	gap, err := readGap(m.Path, m.tables)
	if err != nil {
		return err
	}
	add, differ, notes := gap.lines()
	if len(add) > 0 || len(differ) > 0 {
		return fmt.Errorf("%s: sau các câu qua %s, database vẫn chưa có: %s", m.Database, m.Through, strings.Join(append(add, differ...), "; "))
	}
	m.Left = notes
	return nil
}

// fill says gap in the plan: its lines and the statements that close it.
func (m *DatabaseMigration) fill(gap databaseGap) {
	m.Add, m.Differ, m.Write = gap.lines()
	m.Statements, m.postgres, m.native = nil, nil, false
	for _, statement := range gapStatements(m.tables, gap, m.schema) {
		m.Statements = append(m.Statements, statement.shown())
		if statement.postgres == "" {
			m.native = true
			continue
		}
		m.postgres = append(m.postgres, statement.postgres)
	}
}

// Pending reports a migration that changes the file.
func (m *DatabaseMigration) Pending() bool { return len(m.Add) > 0 || len(m.Write) > 0 }

// Apply makes the file what the plan says, held against the file again inside the transaction that changes it; a
// plan given a port applies through it.
func (m *DatabaseMigration) Apply(ctx context.Context) error {
	switch {
	case m.send != nil:
		return m.applyThroughPort(ctx)
	case m.Held:
		return fmt.Errorf("%s: %s; không có cổng database nào của server đó phục vụ file (app.database trong application.work): "+
			"dừng server rồi chạy lại kitwork migrate", m.Database, heldError{path: m.Path})
	case m.tenant == nil:
		return fmt.Errorf("%s: a plan of CheckAppDatabases reads only", m.Database)
	}
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
	tenant, plans, closeTenant, err := declaredFiles(root, identity, layout, shared)
	if err != nil {
		return nil, nil, err
	}
	for _, plan := range plans {
		gap, held, err := tenant.planGap(plan.Path, plan.tables)
		if err != nil {
			closeTenant()
			return nil, nil, fmt.Errorf("%s: %w", plan.Database, err)
		}
		plan.Held = held
		plan.fill(gap)
	}
	return plans, closeTenant, nil
}

// CheckAppDatabases is PlanAppMigrations reading only: no writer opens, no file is made or changed, so a server's
// boot and kitwork check say what each database lacks before anything runs. A file that is not there is held as the
// file its structure file would make (made in a temporary folder), or as a file of no table. Its plans do not apply.
func CheckAppDatabases(root, identity string, layout RootLayout, shared *SharedEnvironment) ([]*DatabaseMigration, error) {
	_, plans, closeTenant, err := declaredFiles(root, identity, layout, shared)
	if err != nil {
		return nil, err
	}
	defer closeTenant()
	for _, plan := range plans {
		gap, err := readGap(plan.Path, plan.tables)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", plan.Database, err)
		}
		plan.fill(gap)
		plan.tenant = nil
	}
	return plans, nil
}

// Say tells what a database lacks, or has otherwise, as the boot says it: the statements on it fail until it changes
// (kitwork migrate, the port) or the code does; what they run without is said once, as a statement says it.
func (m *DatabaseMigration) Say() {
	where := m.Database
	if m.App != "" {
		where += " (app " + m.App + ")"
	}
	if len(m.Add) > 0 {
		fmt.Printf("[database] %s chưa có: %s; câu trên database này lỗi tới khi thêm: dừng server rồi chạy go run . migrate, "+
			"hay câu đổi schema qua cổng\n", where, strings.Join(m.Add, "; "))
	}
	if len(m.Differ) > 0 {
		fmt.Printf("[database] %s khác code: %s; câu trên database này lỗi tới khi sửa table() theo database\n", where, strings.Join(m.Differ, "; "))
	}
	sayNotes(m.Database, m.Write)
}

// declaredFiles runs the .shared/schema/ of the app identity and gives, file by file, the tables its databases declare
// on it (two schema files may each declare connection()), the plans' gaps not read yet.
func declaredFiles(root, identity string, layout RootLayout, shared *SharedEnvironment) (*Tenant, []*DatabaseMigration, func(), error) {
	tenant := NewAppTenantWithRuntimeLayout(root, identity, layout, app.NewRuntime(identity))
	tenant.ownsApp = true
	tenant.SetSharedEnvironment(shared)
	closeTenant := func() {
		forgetSchemas(tenant)
		tenant.Close()
	}
	if err := tenant.prepareProgram(); err != nil {
		closeTenant()
		return nil, nil, nil, err
	}
	declared, err := tenant.runSchemaFiles()
	if err != nil {
		closeTenant()
		return nil, nil, nil, err
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
				Path: proxy.sqlitePath, schema: schemaOfDatabase(proxy.dbName), tables: map[string]map[string]*ColumnSpec{}, tenant: tenant}
			byPath[proxy.sqlitePath] = plan
			plans = append(plans, plan)
		}
		for name, columns := range tables {
			plan.tables[name] = columns
		}
	}
	sort.Slice(plans, func(i, j int) bool { return plans[i].Path < plans[j].Path })
	return tenant, plans, closeTenant, nil
}

// readGap holds tables against the file at path reading only: the file itself on a connection that cannot write; the
// file its structure file would make, made in a temporary folder; or a file of no table when neither is there.
func readGap(path string, tables map[string]map[string]*ColumnSpec) (databaseGap, error) {
	if _, err := os.Stat(path); err == nil {
		file, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?_pragma=busy_timeout(5000)&_pragma=query_only(1)")
		if err != nil {
			return databaseGap{}, err
		}
		defer file.Close()
		return checkDatabase(file, tables)
	}
	structure, err := os.ReadFile(path + ".sql")
	if errors.Is(err, os.ErrNotExist) {
		return checkDatabase(nil, tables)
	}
	if err != nil {
		return databaseGap{}, err
	}
	folder, err := os.MkdirTemp("", "kitwork-structure-")
	if err != nil {
		return databaseGap{}, err
	}
	defer os.RemoveAll(folder)
	ctx := context.Background()
	made, err := kitdbsqlite.Open(ctx, filepath.Join(folder, filepath.Base(path)), kitdbsqlite.Options{})
	if err != nil {
		return databaseGap{}, err
	}
	defer made.Close()
	if err := gateway.LoadStructure(ctx, made, migrateBy, string(structure)); err != nil {
		return databaseGap{}, fmt.Errorf("%s.sql: %w", filepath.Base(path), err)
	}
	var gap databaseGap
	err = made.Read(ctx, func(snapshot *kitdbsqlite.Snapshot) error {
		var err error
		gap, err = checkDatabase(snapshot, tables)
		return err
	})
	return gap, err
}

// planGap holds tables against the file at path: through its writer when the file, or its structure file, is
// there; as a file of no table when neither is (nothing is made until the migration applies). A file another process
// writes is read without the writer, and held.
func (t *Tenant) planGap(path string, tables map[string]map[string]*ColumnSpec) (databaseGap, bool, error) {
	_, fileErr := os.Stat(path)
	_, structureErr := os.Stat(path + ".sql")
	if errors.Is(fileErr, os.ErrNotExist) && errors.Is(structureErr, os.ErrNotExist) {
		gap, err := checkDatabase(nil, tables)
		return gap, false, err
	}
	database, err := t.historyDatabase(path)
	if errors.Is(err, kitdbsqlite.ErrLocked) {
		gap, err := readGap(path, tables)
		return gap, true, err
	}
	if err != nil {
		return databaseGap{}, false, err
	}
	var gap databaseGap
	err = database.Read(context.Background(), func(snapshot *kitdbsqlite.Snapshot) error {
		var err error
		gap, err = checkDatabase(snapshot, tables)
		return err
	})
	return gap, false, err
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
