package work

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/id"
	kitdbengine "github.com/kitwork/engine/kitdb"
	"github.com/kitwork/engine/runtime"
	queuestore "github.com/kitwork/engine/utilities/queue"
	"github.com/kitwork/engine/value"
)

// The copy of an app's KitDB files into its SQLite files (KITWORK_KITDB.md mục 11 bước 5): `kitwork migrate
// kitdb` reads application.kitdb and storage.kitdb, the files the app's database and datastore() were until
// 29–30/10/2026, and writes what they hold into kitwork.db beside each, checked by a digest of every row on
// both sides. The KitDB files are never written, moved or deleted.
//
//   - application.kitdb: each table goes to the file its .shared/schema/ declares it in (kitwork.db for
//     connection(), billing.db for connection().schema("billing")), in the columns the engine makes for it
//     today (SchemaTable.ensureTable); the engine's tables of the runs (kitwork_runs, kitwork_definitions,
//     kitwork_schedules) to kitwork.db, as queuestore.InitSchema makes them. A table no file declares, a value
//     that would change (a decimal to round, one past 15 digits, a value of another type), stop the copy
//     before anything is written, naming the table, the key and the column, never the value.
//   - storage.kitdb: every value of datastore() to kitwork_storage of .database/storage/kitwork.db, its expiry
//     with it, expired or not.
//
// The KitDB file is read from a private copy of it and of every file beside it of its name (its WAL, its
// history), opened read-only: the file itself is never opened, its WAL never recovered, its lock never
// taken; the files are hashed before and after the copy is made, and a change stops it (a server was
// writing). Each SQLite file is written in one transaction, through the app's own pool of it (which takes
// the file's process lock: a server holding the file makes the copy fail): its tables made, the rows
// inserted, every copied table read back and its digest held against KitDB's, then a row of kitwork_copies,
// the mark, and the commit; any difference rolls everything back. A fresh read-only connection checks the
// file again after the commit. A target table that already holds rows is refused (kitwork_definitions and
// kitwork_schedules excepted: the rows the target holds win, and only the names it lacks are copied); a file
// the mark says was copied is checked against the KitDB file again, and nothing is written.

// kitDBCopySchema is the table a SQLite file keeps the copies made into it in: one row for each KitDB file
// copied, named by its path in the app (source), with the digest of what was copied, how many rows, and when
// (milliseconds since the epoch).
const kitDBCopySchema = `CREATE TABLE IF NOT EXISTS kitwork_copies (source TEXT NOT NULL PRIMARY KEY, digest TEXT NOT NULL, rows INTEGER NOT NULL, at INTEGER NOT NULL)`

// kitDBCopyMarked reports whether the SQLite file at target holds the mark of a copy from source. A file that
// does not exist is not made, and one without kitwork_copies holds no mark.
func kitDBCopyMarked(t *Tenant, name, target, source string) bool {
	if !t.insideAppRoot(target) {
		return false
	}
	if _, err := os.Stat(target); err != nil {
		return false
	}
	config := hostSQLiteConfig(name, target)
	pool := t.lookupDatabase(config)
	if pool == nil {
		opened, err := t.openDatabase(config)
		if err != nil {
			return false
		}
		pool = opened
	}
	var held int
	if err := pool.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'kitwork_copies'`).Scan(&held); err != nil || held == 0 {
		return false
	}
	var marked int
	if err := pool.QueryRow(`SELECT COUNT(*) FROM kitwork_copies WHERE source = ?`, source).Scan(&marked); err != nil {
		return false
	}
	return marked > 0
}

// kitDBApplicationSource is the KitDB file of the app's database as the mark of a copy names it.
const kitDBApplicationSource = ".database/application/" + kitDBApplicationFile

// applicationCopied reports whether the app's kitwork.db holds the mark of a copy from application.kitdb: what
// that file held, the runs among it, is in kitwork.db, and boot no longer opens it (appRunsLeftBehind).
func applicationCopied(t *Tenant) bool {
	return kitDBCopyMarked(t, appDatabaseName(""), appDatabasePath(t, ""), kitDBApplicationSource)
}

// Hooks of the tests, nil in a server: kitDBCopyCopied runs once the files of a KitDB file were copied out,
// before they are hashed again; kitDBCopyOpened is given the KitDB the copy reads; kitDBCopyInserted runs in a
// copy's transaction once the rows of a table are inserted, and fails the copy with what it returns.
var (
	kitDBCopyCopied   func(source string)
	kitDBCopyOpened   func(db *kitdbengine.DB)
	kitDBCopyInserted func(db queuestore.SQL, table string) error
)

// KitDBCopyOptions is how `kitwork migrate kitdb` runs on one app.
type KitDBCopyOptions struct {
	// DryRun reads the KitDB files and what the SQLite files hold, says what would be copied, and writes
	// nothing: no SQLite file is made.
	DryRun bool
}

// KitDBCopyReport is what the copy of one app's KitDB files did, or would do with DryRun.
type KitDBCopyReport struct {
	DryRun bool
	Files  []KitDBCopyFile
	// Notes are what the copy leaves aside and why: a database of the host's catalogue, one outside the app.
	Notes []string
}

// KitDBCopyFile is the copy of one KitDB file into one SQLite file of the app.
type KitDBCopyFile struct {
	// Source is the KitDB file and Target the SQLite file, each from the app's folder, with /.
	Source string
	Target string
	Tables []KitDBCopyTable
	Rows   int
	// Digest is the digest of what the KitDB file holds for the target (hex), the mark's.
	Digest string
	// Copied: this run wrote the file. Already: a run before did (At), and the KitDB file still holds what
	// it copied; nothing was written.
	Copied  bool
	Already bool
	At      time.Time
}

// KitDBCopyTable is one table of a copy: the rows copied (or to copy), and the rows of the KitDB file the
// target already held a row of the same key for, and kept (kitwork_definitions, kitwork_schedules).
type KitDBCopyTable struct {
	Name  string
	Rows  int
	Kept  int
	Notes []string
}

// Lines is the report as `kitwork migrate kitdb` prints it.
func (report KitDBCopyReport) Lines() []string {
	var lines []string
	for _, file := range report.Files {
		digest := file.Digest
		if len(digest) > 16 {
			digest = digest[:16]
		}
		switch {
		case file.Already:
			lines = append(lines, fmt.Sprintf("%s → %s: đã chép lúc %s; %s vẫn giữ đúng những gì đã chép (digest %s), không ghi gì",
				file.Source, file.Target, file.At.UTC().Format(time.RFC3339), file.Source, digest))
			continue
		case file.Copied:
			lines = append(lines, fmt.Sprintf("%s → %s: đã chép %d dòng, đọc lại khớp từng dòng (digest %s)", file.Source, file.Target, file.Rows, digest))
		default:
			lines = append(lines, fmt.Sprintf("%s → %s: sẽ chép %d dòng (digest %s)", file.Source, file.Target, file.Rows, digest))
		}
		for _, table := range file.Tables {
			line := fmt.Sprintf("  %s: %d dòng", table.Name, table.Rows)
			if table.Kept > 0 {
				line += fmt.Sprintf(", %d dòng %s đã có cùng khoá nên giữ của %s", table.Kept, file.Target, file.Target)
			}
			for _, note := range table.Notes {
				line += "; " + note
			}
			lines = append(lines, line)
		}
	}
	return append(lines, report.Notes...)
}

// CopyKitDB copies the app's KitDB files into its SQLite files (`kitwork migrate kitdb`), or with DryRun
// says what it would copy. t is the app's tenant, never Run: the app's .shared/schema/ is run on its own,
// and no worker starts. Each file is copied on its own: an error of one does not stop the other, and the
// errors are joined.
func (t *Tenant) CopyKitDB(options KitDBCopyOptions) (KitDBCopyReport, error) {
	report := KitDBCopyReport{DryRun: options.DryRun}
	application := t.resolveApp(".database", "application", kitDBApplicationFile)
	datastore := t.resolveApp(".database", "storage", datastoreLegacyFile)
	held := func(path string) bool {
		info, err := os.Stat(path)
		return err == nil && info.Mode().IsRegular()
	}
	if !held(application) && !held(datastore) {
		return report, nil
	}
	if err := t.prepareProgram(); err != nil {
		return report, err
	}
	var failures []error
	if held(application) {
		files, notes, err := t.copyApplicationKitDB(application, options.DryRun)
		report.Files = append(report.Files, files...)
		report.Notes = append(report.Notes, notes...)
		if err != nil {
			failures = append(failures, err)
		}
	}
	if held(datastore) {
		file, err := t.copyDatastoreKitDB(datastore, options.DryRun)
		if file.Source != "" {
			report.Files = append(report.Files, file)
		}
		if err != nil {
			failures = append(failures, err)
		}
	}
	return report, errors.Join(failures...)
}

// ── what the app declares ─────────────────────────────────────────────────────────────────────────────

// kitDBCopyDeclared is a table the app's .shared/schema/ declares on its own database: the schema it is in
// ("" for public), the file that declares it, and its columns.
type kitDBCopyDeclared struct {
	name    string
	schema  string
	file    string
	columns map[string]*ColumnSpec
}

// kitDBCopyDeclarations runs every file of the app's .shared/schema/ as the app's own files import it, and
// gives the tables they declare on the app's database by name, where a table is declared on another database
// instead (connection("tên") of the host's catalogue), and what the copy leaves aside. A file that does not
// run, and a table declared twice, stop the copy: where its tables go is not known.
func (t *Tenant) kitDBCopyDeclarations() (declared map[string]*kitDBCopyDeclared, elsewhere map[string]string, notes []string, err error) {
	declared, elsewhere = map[string]*kitDBCopyDeclared{}, map[string]string{}
	folder := t.resolveApp(".shared", "schema")
	entries, readErr := os.ReadDir(folder)
	if readErr != nil {
		if os.IsNotExist(readErr) {
			return declared, elsewhere, nil, nil
		}
		return nil, nil, nil, readErr
	}
	sort.Slice(entries, func(left, right int) bool { return entries[left].Name() < entries[right].Name() })
	seen := map[*dbProxy]bool{}
	var problems []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".work") {
			continue
		}
		relative := ".shared/schema/" + entry.Name()
		exports, runErr := t.runSchemaFile(filepath.Join(folder, entry.Name()))
		if runErr != nil {
			problems = append(problems, fmt.Sprintf("%s không chạy được: %v", relative, runErr))
			continue
		}
		names := make([]string, 0, len(exports))
		for name := range exports {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			item := exports[name]
			if item.Failed() {
				problems = append(problems, fmt.Sprintf("%s, export %s: %s", relative, name, item.Text()))
				continue
			}
			switch handle := item.V.(type) {
			case *ManagedDatabase:
				proxy := handle.proxy
				if proxy == nil || seen[proxy] {
					continue
				}
				seen[proxy] = true
				tables, _ := proxy.schemaSnapshot()
				schema, own := t.appSchemaOf(proxy)
				if !own {
					where := fmt.Sprintf("connection(%q) của %s", strings.TrimPrefix(proxy.dbName, catalogDatabasePrefix), relative)
					notes = append(notes, where+": database của danh mục host, không phải database của app; không chép")
					for table := range tables {
						elsewhere[table] = where
					}
					continue
				}
				for _, table := range sortedTableNames(tables) {
					columns := tables[table]
					if previous := declared[table]; previous != nil {
						if previous.schema != schema || schemaHash(previous.columns) != schemaHash(columns) {
							problems = append(problems, fmt.Sprintf("bảng %s khai hai lần, ở %s và ở %s, khác nhau: không biết chép vào bảng nào", table, previous.file, relative))
						}
						continue
					}
					declared[table] = &kitDBCopyDeclared{name: table, schema: schema, file: relative, columns: columns}
				}
			case *ExternalDatabase:
				notes = append(notes, fmt.Sprintf("connect(…) %s của %s: database ngoài app; không chép", name, relative))
			}
		}
	}
	if len(problems) > 0 {
		return nil, nil, notes, kitDBCopyRefused(kitDBApplicationSource, problems)
	}
	return declared, elsewhere, notes, nil
}

// appSchemaOf is the schema of the app's database a handle of .shared/schema/ declares its tables in, and
// whether it is the app's database at all (not a name of the host's catalogue).
func (t *Tenant) appSchemaOf(proxy *dbProxy) (string, bool) {
	if proxy == nil || proxy.engine != "sqlite" {
		return "", false
	}
	schema := ""
	if proxy.dbName != kitDBApplicationName {
		rest, own := strings.CutPrefix(proxy.dbName, kitDBApplicationName+"/")
		if !own || rest == "" {
			return "", false
		}
		schema = rest
	}
	if filepath.Clean(proxy.sqlitePath) != filepath.Clean(appDatabasePath(t, schema)) {
		return "", false
	}
	return schema, true
}

// runSchemaFile runs one file of .shared/schema/ as a module, in a VM of its own seeded from the tenant's
// globals (as runQueueFile runs a file of .shared/queue/), and gives its exports.
func (t *Tenant) runSchemaFile(file string) (map[string]value.Value, error) {
	bytecode, err := compiler.CompileModuleFileInApp(t.resolveApp(), file)
	if err != nil {
		return nil, err
	}
	if missing := t.missingEnvironment(bytecode); missing != nil {
		return nil, missing
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
	finished := loadingFile(vm)
	result := vm.Run()
	finished()
	if result.K == value.Invalid {
		return nil, errors.New(result.Text())
	}
	if result.K != value.Map {
		return nil, fmt.Errorf("file không trả về các export của nó")
	}
	return result.Map(), nil
}

// ── reading a KitDB file without writing it ───────────────────────────────────────────────────────────

// kitDBCopySource is a KitDB file opened for the copy: a private copy of it and of every file beside it of its
// name, opened as a replica (Begin is refused).
type kitDBCopySource struct {
	folder string
	db     *kitdbengine.DB
}

func (source *kitDBCopySource) close() {
	if source == nil {
		return
	}
	if source.db != nil {
		_ = source.db.Close()
	}
	_ = os.RemoveAll(source.folder)
}

// openKitDBCopySource copies the KitDB file at path, and every file beside it of its name (its WAL, its
// history, its search), into a private folder, hashing them before and after: a change stops it, a server is
// writing the file. Its lock file (.lock) is left: it holds no data, and a server holds it. The private copy
// is opened read-only, so recovering its WAL changes nothing of the app's.
func openKitDBCopySource(path string) (*kitDBCopySource, error) {
	relative := filepath.Base(path)
	before, err := kitDBCopyFingerprint(path)
	if err != nil {
		return nil, fmt.Errorf("không đọc được %s: %w", relative, err)
	}
	folder, err := os.MkdirTemp("", "kitwork-kitdb-copy-")
	if err != nil {
		return nil, err
	}
	source := &kitDBCopySource{folder: folder}
	if err := copyKitDBFiles(path, folder); err != nil {
		source.close()
		return nil, fmt.Errorf("không chép được %s ra để đọc: %w", relative, err)
	}
	if kitDBCopyCopied != nil {
		kitDBCopyCopied(path)
	}
	after, err := kitDBCopyFingerprint(path)
	if err != nil || after != before {
		source.close()
		return nil, fmt.Errorf("%s đổi trong lúc được chép ra để đọc: một server đang ghi nó; dừng server rồi chạy lại", relative)
	}
	// the file itself, not only what is beside it: KitDB would open a missing one as a new, empty database, and
	// the copy would mark an empty copy as done
	if info, err := os.Stat(filepath.Join(folder, relative)); err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		source.close()
		return nil, fmt.Errorf("%s không phải một file KitDB có dữ liệu (không có, là thư mục, hay trống): không chép", relative)
	}
	db, err := kitdbengine.OpenReplica(filepath.Join(folder, relative), kitdbengine.OpenOptions{})
	if err != nil {
		source.close()
		return nil, fmt.Errorf("không mở được %s: %w", relative, err)
	}
	source.db = db
	if kitDBCopyOpened != nil {
		kitDBCopyOpened(db)
	}
	return source, nil
}

// kitDBCopyFiles are the files of the KitDB file at path: itself and every file or folder beside it whose name
// is its name and a suffix (.wal, .history, …), the lock file left out, sorted.
func kitDBCopyFiles(path string) ([]string, error) {
	folder, base := filepath.Dir(path), filepath.Base(path)
	entries, err := os.ReadDir(folder)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, entry := range entries {
		name := entry.Name()
		if name == base || (strings.HasPrefix(name, base+".") && name != base+".lock") {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names, nil
}

// kitDBCopyFingerprint is the SHA-256 of the KitDB file at path and the files beside it: each one's path
// from the folder and its bytes.
func kitDBCopyFingerprint(path string) (string, error) {
	names, err := kitDBCopyFiles(path)
	if err != nil {
		return "", err
	}
	folder := filepath.Dir(path)
	sum := sha256.New()
	for _, name := range names {
		err := filepath.WalkDir(filepath.Join(folder, name), func(file string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			relative, _ := filepath.Rel(folder, file)
			fmt.Fprintf(sum, "%s\x00", filepath.ToSlash(relative))
			if entry.IsDir() {
				return nil
			}
			opened, err := os.Open(file)
			if err != nil {
				return err
			}
			defer opened.Close()
			size, err := io.Copy(sum, opened)
			fmt.Fprintf(sum, "\x00%d\x00", size)
			return err
		})
		if err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}

// copyKitDBFiles copies the KitDB file at path and the files beside it into folder, keeping their names.
func copyKitDBFiles(path, folder string) error {
	names, err := kitDBCopyFiles(path)
	if err != nil {
		return err
	}
	from := filepath.Dir(path)
	for _, name := range names {
		err := filepath.WalkDir(filepath.Join(from, name), func(file string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			relative, err := filepath.Rel(from, file)
			if err != nil {
				return err
			}
			target := filepath.Join(folder, relative)
			if entry.IsDir() {
				return os.MkdirAll(target, 0o700)
			}
			return copyKitDBFile(file, target)
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func copyKitDBFile(from, to string) error {
	source, err := os.Open(from)
	if err != nil {
		return err
	}
	defer source.Close()
	target, err := os.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(target, source); err != nil {
		target.Close()
		return err
	}
	return target.Close()
}

// kitDBCopyStruct is one struct of a KitDB file: its definition, and its rows as the ORM decodes them.
type kitDBCopyStruct struct {
	definition *StructDef
	rows       []map[string]value.Value
}

// readKitDBCopyStructs reads every struct of the catalog and all of its rows, at one snapshot, as the ORM
// reads them (the active row generation). A struct whose rows are being migrated, or a row holding a field
// its definition does not know, stops the copy: what it holds is not one shape.
func readKitDBCopyStructs(db *kitdbengine.DB) ([]*kitDBCopyStruct, error) {
	catalog, err := db.Catalog()
	if err != nil {
		return nil, err
	}
	snapshot, err := db.Snapshot()
	if err != nil {
		return nil, err
	}
	defer snapshot.Close()
	var structs []*kitDBCopyStruct
	for _, entry := range catalog.Structs {
		definition, err := decodeKitDBCatalog(entry.Definition, entry.Name)
		if err != nil {
			return nil, err
		}
		state, migrating, err := loadKitDBRowMigrationState(snapshot, definition)
		if err != nil {
			return nil, fmt.Errorf("bảng %s: %w", definition.Name, err)
		}
		if migrating && state.TargetGeneration != 0 && state.Phase != kitDBRowMigrationPhaseCleanup {
			return nil, fmt.Errorf("bảng %s đang đổi dòng sang hình mới (migration của KitDB chưa xong): mở app một lần bằng engine cũ cho xong, rồi chép", definition.Name)
		}
		generation, _, err := loadKitDBActiveRowLayout(snapshot, definition)
		if err != nil {
			return nil, fmt.Errorf("bảng %s: %w", definition.Name, err)
		}
		prefix, err := kitDBPhysicalRowPrefix(definition, generation)
		if err != nil {
			return nil, fmt.Errorf("bảng %s: %w", definition.Name, err)
		}
		read := &kitDBCopyStruct{definition: definition}
		_, err = snapshot.Scan(kitdbengine.RangeOptions{Prefix: prefix}, func(_, encoded []byte) (bool, error) {
			decoded, err := decodeKitDBRow(definition, encoded)
			if err != nil {
				return true, err
			}
			if len(decoded.unknown) > 0 {
				return true, fmt.Errorf("một dòng giữ trường KitDB không có trong định nghĩa của bảng")
			}
			for name := range decoded.values {
				if _, known := definition.byName[name]; !known {
					return true, fmt.Errorf("một dòng giữ trường %s không có trong định nghĩa của bảng", name)
				}
			}
			read.rows = append(read.rows, decoded.values)
			return false, nil
		})
		if err != nil {
			return nil, fmt.Errorf("bảng %s: %w", definition.Name, err)
		}
		structs = append(structs, read)
	}
	return structs, nil
}

// ── the plan ──────────────────────────────────────────────────────────────────────────────────────────

// kitDBCopyTarget is one SQLite file a copy writes, and what goes in it.
type kitDBCopyTarget struct {
	// source is the KitDB file as the mark names it: its path from the app's folder, with /
	source string
	// path is the SQLite file; relative its path from the app's folder, with /; name its pool's name
	path     string
	relative string
	name     string
	tables   []*kitDBCopyTable
	// engine: the engine's tables of the runs are made here; datastore: kitwork_storage is
	engine    bool
	datastore bool
}

// kitDBCopyEngineTables are the engine's tables the copy writes (the runs, datastore()), described by SQLite
// itself: their statements run on a database in memory, and PRAGMA table_info gives the columns in order,
// their types, NOT NULL and the primary key.
func kitDBCopyEngineTables(statements []string) (map[string]*kitDBCopyTable, error) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return nil, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			return nil, err
		}
	}
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type = 'table' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return nil, err
		}
		names = append(names, name)
	}
	rows.Close()
	tables := map[string]*kitDBCopyTable{}
	for _, name := range names {
		info, err := db.Query(fmt.Sprintf("PRAGMA table_info(%q)", name))
		if err != nil {
			return nil, err
		}
		table := &kitDBCopyTable{name: name}
		keys := map[int]int{}
		for info.Next() {
			var position, notNull, primary int
			var column, kind string
			var fallback sql.NullString
			if err := info.Scan(&position, &column, &kind, &notNull, &fallback, &primary); err != nil {
				info.Close()
				return nil, err
			}
			class := kitDBCopyText
			switch strings.ToUpper(kind) {
			case "INTEGER":
				class = kitDBCopyInteger
			case "REAL":
				class = kitDBCopyFloat
			case "BLOB":
				class = kitDBCopyBytes
			}
			if primary > 0 {
				keys[primary] = len(table.columns)
			}
			table.columns = append(table.columns, kitDBCopyColumn{name: column, class: class, notNull: notNull != 0 || primary > 0})
		}
		info.Close()
		for order := 1; order <= len(keys); order++ {
			table.primary = append(table.primary, keys[order])
		}
		table.merge = name == "kitwork_definitions" || name == "kitwork_schedules"
		tables[name] = table
	}
	return tables, nil
}

// kitDBCopyDeclaredTable is a table .shared/schema/ declares, as the copy writes it: its columns in the order
// the engine lays them out (orderedColumns), each stored as sqliteStorage says.
func kitDBCopyDeclaredTable(declared *kitDBCopyDeclared) *kitDBCopyTable {
	table := &kitDBCopyTable{name: declared.name, declared: declared}
	for _, name := range orderedColumns(declared.columns) {
		spec := declared.columns[name]
		column := kitDBCopyColumn{name: name, notNull: spec.notNull || spec.primary, spec: spec}
		switch {
		case spec.kind == "decimal":
			column.class, column.scale = kitDBCopyDecimal, spec.scale
		case spec.kind == "bool":
			column.class = kitDBCopyBool
		case spec.kind == "json" || spec.kind == "jsonb" || spec.kind == "array" || spec.kind == "vector":
			column.class = kitDBCopyJSON
		default:
			switch sqliteStorage(spec) {
			case "INTEGER":
				column.class = kitDBCopyInteger
			case "REAL":
				column.class = kitDBCopyFloat
			case "BLOB":
				column.class = kitDBCopyBytes
			}
		}
		if spec.primary {
			table.primary = append(table.primary, len(table.columns))
		}
		table.columns = append(table.columns, column)
	}
	return table
}

// fill converts the rows of a KitDB struct into the table; problems say each value that has no stored form,
// by key and column, and each field of KitDB the table has no column for that holds a value.
func (table *kitDBCopyTable) fill(read *kitDBCopyStruct) []string {
	var problems []string
	columns := map[string]bool{}
	for _, column := range table.columns {
		columns[column.name] = true
	}
	left := map[string]bool{}
	for _, row := range read.rows {
		label := table.label(row)
		fields := make([]kitDBCopyField, len(table.columns))
		values := make([]any, len(table.columns))
		failed := false
		for index, column := range table.columns {
			item, present := row[column.name]
			if !present {
				item = value.NewNil()
			}
			stored, field, problem := kitDBCopyValue(column, item)
			if problem != "" {
				problems = append(problems, fmt.Sprintf("bảng %s, khoá %s, cột %s: %s", table.name, label, column.name, problem))
				failed = true
				continue
			}
			fields[index], values[index] = field, stored
		}
		for name, item := range row {
			if !columns[name] && item.K != value.Nil {
				left[name] = true
			}
		}
		if !failed {
			table.rows = append(table.rows, table.newRow(fields, values))
		}
	}
	for _, name := range sortedKeys(left) {
		problems = append(problems, fmt.Sprintf("bảng %s: cột %s có dữ liệu trong KitDB mà bảng không có cột đó (.shared/schema/ không khai): dữ liệu của nó sẽ bị bỏ lại", table.name, name))
	}
	return problems
}

// label is the key of a KitDB row as a message names it: its primary key's values, as text.
func (table *kitDBCopyTable) label(row map[string]value.Value) string {
	parts := make([]string, 0, len(table.primary))
	for _, position := range table.primary {
		parts = append(parts, row[table.columns[position].name].Text())
	}
	return strings.Join(parts, "/")
}

func sortedKeys(set map[string]bool) []string {
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// kitDBCopyRefused is the error of a copy that writes nothing, with each problem on its own line (the first
// thirty).
func kitDBCopyRefused(source string, problems []string) error {
	const shown = 30
	lines := problems
	if len(lines) > shown {
		lines = append(append([]string(nil), lines[:shown]...), fmt.Sprintf("… và %d chỗ khác", len(problems)-shown))
	}
	return fmt.Errorf("không chép %s, không ghi gì:\n  %s", source, strings.Join(lines, "\n  "))
}

// copyApplicationKitDB copies application.kitdb: each table to the file .shared/schema/ declares it in, the
// engine's tables to kitwork.db. The files of other schemas are written first and kitwork.db last, so the mark
// in kitwork.db (what boot reads) says every file was copied.
func (t *Tenant) copyApplicationKitDB(path string, dryRun bool) ([]KitDBCopyFile, []string, error) {
	declared, elsewhere, notes, err := t.kitDBCopyDeclarations()
	if err != nil {
		return nil, notes, err
	}
	source, err := openKitDBCopySource(path)
	if err != nil {
		return nil, notes, err
	}
	defer source.close()
	structs, err := readKitDBCopyStructs(source.db)
	if err != nil {
		return nil, notes, fmt.Errorf("không chép %s: %w", kitDBApplicationSource, err)
	}
	engine, err := kitDBCopyEngineTables(queuestore.Schema)
	if err != nil {
		return nil, notes, err
	}

	public := &kitDBCopyTarget{
		source: kitDBApplicationSource, path: appDatabasePath(t, ""), relative: ".database/application/" + filepath.Base(appDatabasePath(t, "")),
		name: appDatabaseName(""), engine: true,
	}
	targets := map[string]*kitDBCopyTarget{"": public}
	var problems []string
	for _, read := range structs {
		name := read.definition.Name
		var table *kitDBCopyTable
		target := public
		switch {
		case engineTable(name):
			table = engine[name]
			if table == nil {
				if len(read.rows) > 0 {
					problems = append(problems, fmt.Sprintf("bảng %s (%d dòng) là bảng engine mà engine hôm nay không có: dữ liệu của nó sẽ bị bỏ lại", name, len(read.rows)))
				}
				continue
			}
		case declared[name] != nil:
			table = kitDBCopyDeclaredTable(declared[name])
			schema := declared[name].schema
			if targets[schema] == nil {
				targets[schema] = &kitDBCopyTarget{
					source: kitDBApplicationSource, path: appDatabasePath(t, schema), relative: ".database/application/" + filepath.Base(appDatabasePath(t, schema)),
					name: appDatabaseName(schema),
				}
			}
			target = targets[schema]
		default:
			if len(read.rows) == 0 {
				notes = append(notes, fmt.Sprintf("bảng %s của %s trống và không file nào của .shared/schema/ khai nó: bỏ qua", name, kitDBApplicationSource))
				continue
			}
			where := "không file nào của .shared/schema/ khai nó trên connection()"
			if elsewhere[name] != "" {
				where = "nó được khai ở " + elsewhere[name] + ", không phải trên connection() của app"
			}
			problems = append(problems, fmt.Sprintf("bảng %s (%d dòng): %s; dữ liệu của nó sẽ bị bỏ lại", name, len(read.rows), where))
			continue
		}
		problems = append(problems, table.fill(read)...)
		target.tables = append(target.tables, table)
	}
	if len(problems) > 0 {
		return nil, notes, kitDBCopyRefused(kitDBApplicationSource, problems)
	}

	schemas := make([]string, 0, len(targets))
	for schema := range targets {
		if schema != "" {
			schemas = append(schemas, schema)
		}
	}
	sort.Strings(schemas)
	// every file is looked at before any is written: a refusal of kitwork.db never comes after billing.db was
	// committed
	if !dryRun {
		for _, schema := range append(schemas, "") {
			if _, err := t.writeKitDBCopy(targets[schema], true); err != nil {
				return nil, notes, err
			}
		}
	}
	var files []KitDBCopyFile
	for _, schema := range append(schemas, "") {
		file, err := t.writeKitDBCopy(targets[schema], dryRun)
		files = append(files, file)
		if err != nil {
			return files, notes, err
		}
	}
	return files, notes, nil
}

// copyDatastoreKitDB copies storage.kitdb: each value datastore() kept there (its key "storage:" and the
// key, its record the expiry in nanoseconds, 8 bytes big-endian, 0 for never, then the encoded value) to a
// row of kitwork_storage of .database/storage/kitwork.db: the key and the value as they are, the expiry in
// milliseconds since the epoch (the nanoseconds divided down, floor), NULL for never.
func (t *Tenant) copyDatastoreKitDB(path string, dryRun bool) (KitDBCopyFile, error) {
	engine, err := kitDBCopyEngineTables(datastoreSchema)
	if err != nil {
		return KitDBCopyFile{}, err
	}
	table := engine["kitwork_storage"]
	if table == nil {
		return KitDBCopyFile{}, fmt.Errorf("kitwork_storage: datastoreSchema không làm bảng đó")
	}
	positions := map[string]int{}
	for index, column := range table.columns {
		positions[column.name] = index
	}
	source, err := openKitDBCopySource(path)
	if err != nil {
		return KitDBCopyFile{}, err
	}
	defer source.close()
	snapshot, err := source.db.Snapshot()
	if err != nil {
		return KitDBCopyFile{}, err
	}
	prefix := []byte("storage:")
	var problems []string
	_, err = snapshot.Scan(kitdbengine.RangeOptions{Prefix: prefix}, func(key, record []byte) (bool, error) {
		stored := bytes.Clone(key[len(prefix):])
		if len(record) < 8 {
			problems = append(problems, fmt.Sprintf("khoá %s: bản ghi ngắn hơn 8 byte của thời hạn", stored))
			return false, nil
		}
		fields := make([]kitDBCopyField, len(table.columns))
		values := make([]any, len(table.columns))
		data := bytes.Clone(record[8:])
		fields[positions["key"]], values[positions["key"]] = kitDBCopyField{data: stored}, stored
		fields[positions["value"]], values[positions["value"]] = kitDBCopyField{data: data}, data
		// the expiry KitDB kept in nanoseconds, kitwork_storage keeps in milliseconds (floor), NULL for never
		expire := kitDBCopyField{null: true}
		var expireValue any
		if nanoseconds := int64(binary.BigEndian.Uint64(record[:8])); nanoseconds != 0 {
			milliseconds := time.Unix(0, nanoseconds).UnixMilli()
			expire, expireValue = kitDBCopyField{data: []byte(strconv.FormatInt(milliseconds, 10))}, milliseconds
		}
		fields[positions["expire"]], values[positions["expire"]] = expire, expireValue
		table.rows = append(table.rows, table.newRow(fields, values))
		return false, nil
	})
	snapshot.Close()
	if err != nil {
		return KitDBCopyFile{}, fmt.Errorf("không đọc được %s: %w", datastoreLegacySource, err)
	}
	if len(problems) > 0 {
		return KitDBCopyFile{}, kitDBCopyRefused(datastoreLegacySource, problems)
	}
	target := &kitDBCopyTarget{
		source: datastoreLegacySource, path: datastorePath(t), relative: ".database/storage/" + filepath.Base(datastorePath(t)),
		name: datastoreDatabaseName, tables: []*kitDBCopyTable{table}, datastore: true,
	}
	return t.writeKitDBCopy(target, dryRun)
}

// ── what the target holds, and the write ──────────────────────────────────────────────────────────────

// kitDBCopyHeld is what a target file holds before the copy: the mark of an earlier copy from the source,
// the tables there are, the rows of each table the copy writes, and the keys of each merged table.
type kitDBCopyHeld struct {
	marked bool
	digest string
	at     int64
	tables map[string]bool
	counts map[string]int
	keys   map[string]map[string]bool
}

// readKitDBCopyHeld reads what db (a target file, or the copy's transaction of it) holds for target.
func readKitDBCopyHeld(db queuestore.SQL, target *kitDBCopyTarget) (kitDBCopyHeld, error) {
	held := kitDBCopyHeld{tables: map[string]bool{}, counts: map[string]int{}, keys: map[string]map[string]bool{}}
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type = 'table'`)
	if err != nil {
		return held, err
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return held, err
		}
		held.tables[name] = true
	}
	rows.Close()
	if held.tables["kitwork_copies"] {
		err := db.QueryRow(`SELECT digest, at FROM kitwork_copies WHERE source = ?`, target.source).Scan(&held.digest, &held.at)
		switch {
		case err == nil:
			held.marked = true
		case !errors.Is(err, sql.ErrNoRows):
			return held, err
		}
	}
	for _, table := range target.tables {
		if !held.tables[table.name] {
			continue
		}
		count := 0
		if err := db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM %q", table.name)).Scan(&count); err != nil {
			return held, err
		}
		held.counts[table.name] = count
		if table.merge {
			read, err := readKitDBCopyRows(db, table, true)
			if err != nil {
				return held, err
			}
			keys := map[string]bool{}
			for _, row := range read {
				keys[string(row.key)] = true
			}
			held.keys[table.name] = keys
		}
	}
	return held, nil
}

// refusals are the tables the copy may not write because the target holds rows there already: a table of the
// app, kitwork_runs, kitwork_storage. A merged table may hold rows.
func (held kitDBCopyHeld) refusals(target *kitDBCopyTarget) []string {
	var problems []string
	for _, table := range target.tables {
		if count := held.counts[table.name]; count > 0 && !table.merge {
			problems = append(problems, fmt.Sprintf("bảng %s của %s đã có %d dòng: chép vào sẽ trộn dữ liệu cũ với dữ liệu mới", table.name, target.relative, count))
		}
	}
	return problems
}

// copied are the rows of table the copy writes: every row, or for a merged table those whose key the target
// does not hold.
func (held kitDBCopyHeld) copied(table *kitDBCopyTable) []kitDBCopyRow {
	if !table.merge {
		return table.rows
	}
	var rows []kitDBCopyRow
	for _, row := range table.rows {
		if !held.keys[table.name][string(row.key)] {
			rows = append(rows, row)
		}
	}
	return rows
}

// readKitDBCopyRows reads every row of table back from db in the canonical form; keysOnly reads the primary
// key alone. A value SQLite stored as another type than its column's is an error.
func readKitDBCopyRows(db queuestore.SQL, table *kitDBCopyTable, keysOnly bool) ([]kitDBCopyRow, error) {
	columns := table.columns
	positions := make([]int, len(columns))
	for index := range columns {
		positions[index] = index
	}
	if keysOnly {
		positions = table.primary
	}
	names := make([]string, len(positions))
	for index, position := range positions {
		names[index] = fmt.Sprintf("%q", columns[position].name)
	}
	rows, err := db.Query(fmt.Sprintf("SELECT %s FROM %q", strings.Join(names, ", "), table.name))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var read []kitDBCopyRow
	for rows.Next() {
		scanned := make([]any, len(positions))
		pointers := make([]any, len(positions))
		for index := range scanned {
			pointers[index] = &scanned[index]
		}
		if err := rows.Scan(pointers...); err != nil {
			return nil, err
		}
		fields := make([]kitDBCopyField, len(columns))
		for index, position := range positions {
			field, problem := kitDBCopyRead(columns[position], scanned[index])
			if problem != "" {
				return nil, fmt.Errorf("bảng %s, cột %s: %s", table.name, columns[position].name, problem)
			}
			fields[position] = field
		}
		read = append(read, table.newRow(fields, nil))
	}
	return read, rows.Err()
}

// openKitDBCopyTarget opens a target file read-only on a connection of its own, nil when the file does not
// exist: what it holds is read without writing it, and without making it.
func openKitDBCopyTarget(path string) (*sql.DB, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro")
}

// writeKitDBCopy copies what target holds into its file, or with dryRun says what it would copy.
func (t *Tenant) writeKitDBCopy(target *kitDBCopyTarget, dryRun bool) (KitDBCopyFile, error) {
	sort.Slice(target.tables, func(left, right int) bool { return target.tables[left].name < target.tables[right].name })
	full := map[string][32]byte{}
	for _, table := range target.tables {
		full[table.name] = table.digest(table.rows)
	}
	file := KitDBCopyFile{Source: target.source, Target: target.relative, Digest: kitDBCopyFileDigest(full)}

	reader, err := openKitDBCopyTarget(target.path)
	if err != nil {
		return file, err
	}
	held := kitDBCopyHeld{tables: map[string]bool{}, counts: map[string]int{}, keys: map[string]map[string]bool{}}
	if reader != nil {
		held, err = readKitDBCopyHeld(reader, target)
		reader.Close()
		if err != nil {
			return file, fmt.Errorf("không đọc được %s: %w", target.relative, err)
		}
	}
	if held.marked {
		file.Already, file.At = true, time.UnixMilli(held.at)
		if held.digest != file.Digest {
			return file, fmt.Errorf("%s đã chép sang %s lúc %s với digest %s, nay %s giữ digest %s: file KitDB đã đổi sau lần chép; không ghi gì",
				target.source, target.relative, file.At.UTC().Format(time.RFC3339), held.digest, target.source, file.Digest)
		}
		return file, nil
	}
	if problems := held.refusals(target); len(problems) > 0 {
		return file, kitDBCopyRefused(target.source, problems)
	}
	for _, table := range target.tables {
		rows := held.copied(table)
		entry := KitDBCopyTable{Name: table.name, Rows: len(rows), Kept: len(table.rows) - len(rows)}
		for _, column := range table.columns {
			if column.class == kitDBCopyDecimal {
				entry.Notes = append(entry.Notes, fmt.Sprintf("decimal %s: chữ thành số nguyên × 10^%d", column.name, column.scale))
			}
		}
		file.Tables = append(file.Tables, entry)
		file.Rows += len(rows)
	}
	if dryRun {
		return file, nil
	}

	if err := os.MkdirAll(filepath.Dir(target.path), 0o755); err != nil {
		return file, err
	}
	config := hostSQLiteConfig(target.name, target.path)
	pool := t.lookupDatabase(config)
	if pool == nil {
		opened, err := t.openDatabase(config)
		if err != nil {
			return file, fmt.Errorf("không mở được %s: %w", target.relative, err)
		}
		pool = opened
	}
	// a table of the app the target holds already, with no row: the engine's own migration brings it to what
	// .shared/schema/ declares, as the app's first statement would
	for _, table := range target.tables {
		if table.declared == nil || !held.tables[table.name] {
			continue
		}
		existing := &SchemaTable{tenant: t, engine: "sqlite", dbName: target.name, sqlitePath: target.path,
			table: table.name, columns: table.declared.columns}
		if err := existing.ensureTable(); err != nil {
			return file, fmt.Errorf("bảng %s của %s: %w", table.name, target.relative, err)
		}
	}

	wrote := map[string][]kitDBCopyRow{}
	err = kitDBCopyWrite(target.path, func(db queuestore.SQL) error {
		inside, err := readKitDBCopyHeld(db, target)
		if err != nil {
			return err
		}
		if inside.marked {
			return fmt.Errorf("%s vừa được chép sang %s bởi một lần chạy khác", target.source, target.relative)
		}
		if problems := inside.refusals(target); len(problems) > 0 {
			return kitDBCopyRefused(target.source, problems)
		}
		if _, err := db.Exec(`PRAGMA defer_foreign_keys = ON`); err != nil {
			return err
		}
		if target.engine {
			if err := queuestore.InitSchema(db); err != nil {
				return err
			}
		}
		if target.datastore {
			for _, statement := range datastoreSchema {
				if _, err := db.Exec(statement); err != nil {
					return err
				}
			}
		}
		for _, table := range target.tables {
			if table.declared != nil && !inside.tables[table.name] {
				if err := createKitDBCopyTable(db, table); err != nil {
					return err
				}
			}
		}
		if _, err := db.Exec(kitDBCopySchema); err != nil {
			return err
		}
		count := 0
		for _, table := range target.tables {
			rows := inside.copied(table)
			wrote[table.name] = rows
			count += len(rows)
			if err := insertKitDBCopyRows(db, table, rows); err != nil {
				return err
			}
			if kitDBCopyInserted != nil {
				if err := kitDBCopyInserted(db, table.name); err != nil {
					return err
				}
			}
		}
		if err := verifyKitDBCopy(db, target, wrote); err != nil {
			return err
		}
		_, err = db.Exec(`INSERT INTO kitwork_copies (source, digest, rows, at) VALUES (?, ?, ?, ?)`,
			target.source, file.Digest, count, time.Now().UnixMilli())
		return err
	})
	if err != nil {
		return file, fmt.Errorf("không chép %s sang %s, không ghi gì: %w", target.source, target.relative, err)
	}
	// what the transaction wrote, which another writer of the file may have changed since the look before it
	file.Copied, file.Rows = true, 0
	for index, table := range target.tables {
		file.Tables[index].Rows = len(wrote[table.name])
		file.Tables[index].Kept = len(table.rows) - len(wrote[table.name])
		file.Rows += len(wrote[table.name])
	}

	// read again, on a connection of its own, after the commit
	reader, err = openKitDBCopyTarget(target.path)
	if err == nil && reader != nil {
		defer reader.Close()
		var check string
		if err = reader.QueryRow(`PRAGMA quick_check`).Scan(&check); err == nil && check != "ok" {
			err = fmt.Errorf("PRAGMA quick_check: %s", check)
		}
		if err == nil {
			err = verifyKitDBCopy(reader, target, wrote)
		}
	}
	if err != nil {
		return file, fmt.Errorf("%s đã chép sang %s, nhưng đọc lại sau khi ghi không khớp: %w", target.source, target.relative, err)
	}
	return file, nil
}

// kitDBCopyWrite runs write in one transaction on the writer of the file at path (database_history.go), holding the
// file's gate as every write of the runs does: what the copy writes is a Barrier of the file's history with an anchor
// at it (Maintain), so a restore after the copy starts from what it wrote and never replays across it.
func kitDBCopyWrite(path string, write func(db queuestore.SQL) error) error {
	writer, release, err := acquireHistory(path)
	if err != nil {
		return err
	}
	defer release()
	ctx := context.Background()
	_, err = writer.Maintain(ctx, "kitwork migrate kitdb", func(conn *sql.Conn) error {
		gate := queuestore.FileGate(path)
		gate.Lock()
		defer gate.Unlock()
		if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
			return err
		}
		err := write(connRunner{ctx: ctx, conn: conn})
		if err == nil {
			_, err = conn.ExecContext(ctx, "COMMIT")
		}
		if err != nil {
			_, _ = conn.ExecContext(context.WithoutCancel(ctx), "ROLLBACK")
		}
		return err
	})
	return err
}

// createKitDBCopyTable makes a table .shared/schema/ declares in the copy's transaction, as migrate() makes a
// new one: the statement schemaDDL gives, its indexes (collectSQLIndexes), and the row of _kitwork_migrations
// that records it.
func createKitDBCopyTable(db queuestore.SQL, table *kitDBCopyTable) error {
	columns := table.declared.columns
	if err := validateSchema(columns); err != nil {
		return fmt.Errorf("schema %q: %w", table.name, err)
	}
	statement := schemaDDL(table.name, columns)
	if _, err := db.Exec(statement); err != nil {
		return fmt.Errorf("create %q: %w", table.name, err)
	}
	for _, index := range collectSQLIndexes(table.name, columns) {
		if _, err := db.Exec(indexSQL(table.name, index)); err != nil {
			return fmt.Errorf("create index %q: %w", index.name, err)
		}
	}
	// the declared kinds, which the PostgreSQL wire port reads (db.sqlite_columns.go)
	if err := writeColumnKinds(db, table.name, columns); err != nil {
		return fmt.Errorf("kitwork_columns of %q: %w", table.name, err)
	}
	// the table ensureHistory keeps migrate()'s history in
	if _, err := db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %q (
		id TEXT PRIMARY KEY, table_name TEXT, action TEXT, statement TEXT, applied_at TEXT)`, migrationsTable)); err != nil {
		return err
	}
	_, err := db.Exec(fmt.Sprintf("INSERT INTO %q (id, table_name, action, statement, applied_at) VALUES (?, ?, ?, ?, ?)", migrationsTable),
		id.Entity(), table.name, "create-table", statement, time.Now().UTC().Format(time.RFC3339))
	return err
}

// insertKitDBCopyRows inserts rows as they are: never through the table's create, which would give a row a
// new id and a new createdAt.
func insertKitDBCopyRows(db queuestore.SQL, table *kitDBCopyTable, rows []kitDBCopyRow) error {
	names := make([]string, len(table.columns))
	holes := make([]string, len(table.columns))
	for index, column := range table.columns {
		names[index] = fmt.Sprintf("%q", column.name)
		holes[index] = "?"
	}
	statement := fmt.Sprintf("INSERT INTO %q (%s) VALUES (%s)", table.name, strings.Join(names, ", "), strings.Join(holes, ", "))
	for _, row := range rows {
		if _, err := db.Exec(statement, row.values...); err != nil {
			return fmt.Errorf("bảng %s, khoá %s: %w", table.name, row.label, err)
		}
	}
	return nil
}

// verifyKitDBCopy reads every table of target back from db and holds each one's digest against KitDB's: for a
// table the copy wrote whole, every row it holds; for a merged one, the rows of the keys it copied.
func verifyKitDBCopy(db queuestore.SQL, target *kitDBCopyTarget, wrote map[string][]kitDBCopyRow) error {
	for _, table := range target.tables {
		read, err := readKitDBCopyRows(db, table, false)
		if err != nil {
			return err
		}
		want := wrote[table.name]
		if table.merge {
			keys := make(map[string]bool, len(want))
			for _, row := range want {
				keys[string(row.key)] = true
			}
			kept := read[:0]
			for _, row := range read {
				if keys[string(row.key)] {
					kept = append(kept, row)
				}
			}
			read = kept
		}
		if len(read) != len(want) || table.digest(read) != table.digest(want) {
			return fmt.Errorf("bảng %s đọc lại %d dòng, KitDB có %d, digest khác; các khoá khác nhau: %s",
				table.name, len(read), len(want), strings.Join(kitDBCopyDifferent(want, read, 10), ", "))
		}
	}
	return nil
}
