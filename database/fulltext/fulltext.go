// Package fulltext keeps the full-text indexes of SQLite tables (KITWORK_DATABASE.md mục 2, matches;
// KITWORK_KITDB.md câu 13.5, MATCH on the database port). An index lives in the container beside its file,
// <file>.search (kitwork.db.search, billing.db.search), as engine/kitdb/sqlite keeps one: one index per table, named
// by the table, a document per row by rowid, the state {revision, columns} with it. The process has one Manager and
// one container per file, whoever reads it: an app's matches and the database port's MATCH read the same index.
//
// A table is tracked or not. A table of the app's (one application.work does not declare) in a file whose history
// the engine keeps is tracked (03/11/2026, KITWORK_KITDB.md mục 4–5): every write goes through the file's one writer
// (engine/kitdb/sqlite), which records each row it changed in kitwork_history in the same transaction. A tracked
// table's index is brought up to date before each read by folding in the rows the commits after its revision changed
// (an update that leaves the searchable columns as they were is not one), so a row the app just wrote is found; a
// commit that changed the schema builds the index again. Before 03/11/2026 triggers counted the changes; an index
// folded from them is built again. A table nobody tracks (a host database application.work declares, a file without
// history) is built again when its signature moves (schema version, rows, last rowid, revision of kitwork_history),
// looked at every Watch.
//
// The first build of a table runs in the background, one table at a time; a read waits for it up to Wait, then is
// told how far it is (BuildingError). An index whose table has not moved since it was built is used as it is.
package fulltext

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	kitdbsqlite "github.com/kitwork/engine/kitdb/sqlite"
	"github.com/kitwork/engine/search"
	_ "modernc.org/sqlite" // the "sqlite" database/sql driver
)

// Watch is how often a file of an untracked index is looked at again; Wait how long a read waits for a build in
// progress before it is told how far the build is (variables for the tests).
var (
	Watch = 30 * time.Second
	Wait  = 2 * time.Second
)

// buildOptions bound the segments of a build, as engine/kitdb/sqlite builds: large ones, so a big table starts with
// few segments.
var buildOptions = search.BuildOptions{SkipLongTerms: true, FlushThresholdBytes: 192 << 20}

// foldShare is the share of a table's rows past which changes are applied by building the index again.
const foldShare = 4

const queueSize = 64

// Column is a column of an index and its weight. Column and state are written as engine/kitdb/sqlite writes its
// SearchColumn and searchState, so either reads the other's index.
type Column struct {
	Name  string
	Boost float64
}

type state struct {
	Revision  uint64     `json:"revision"`
	Columns   []Column   `json:"columns"`
	Signature *Signature `json:"signature,omitempty"`
	Tracked   bool       `json:"tracked,omitempty"`
	// History marks a tracked index whose revision is the file's kitwork_history: one without it counted the
	// changes by triggers (before 03/11/2026) and is built again.
	History bool `json:"history,omitempty"`
}

// Signature tells whether an untracked table is still what its index was built from.
type Signature struct {
	SchemaVersion int64 `json:"schemaVersion"`
	Rows          int64 `json:"rows"`
	LastRowID     int64 `json:"lastRowid"`
	Revision      int64 `json:"revision"`
}

// BuildingError is a read of an index whose first build has not ended.
type BuildingError struct {
	Name         string
	Built, Total int64
}

func (err *BuildingError) Error() string {
	progress := "it has not started yet"
	if err.Total > 0 {
		progress = fmt.Sprintf("%d of %d rows (%d%%)", err.Built, err.Total, err.Built*100/err.Total)
	}
	return fmt.Sprintf("the search index of %s is being built: %s; try again later", err.Name, progress)
}

// Manager owns every container of the process.
type Manager struct {
	mutex sync.Mutex
	files map[string]*file
	queue chan *Table
	start sync.Once
}

type file struct {
	manager   *Manager
	path      string
	users     int
	container *search.Container
	tables    map[string]*Table // by folded name

	// reader is a connection kept open on the file: PRAGMA data_version for the watch, the revision of a tracked
	// table before each read.
	readerMutex sync.Mutex
	database    *sql.DB
	reader      *sql.Conn
	version     int64
}

var shared = &Manager{files: map[string]*file{}, queue: make(chan *Table, queueSize)}

// Shared is the process's Manager.
func Shared() *Manager { return shared }

// Table is the index of one table of one file.
type Table struct {
	manager    *Manager
	file       *file
	name       string
	label      string // what messages call it: the database's name and the table
	columns    []Column
	definition search.Schema
	declared   bool
	tracked    bool

	retired atomic.Bool
	queued  atomic.Bool
	built   atomic.Int64
	total   atomic.Int64

	folding sync.Mutex // one fold at a time

	mutex     sync.RWMutex
	index     *search.Index
	revision  uint64    // tracked: the revision the index holds
	signature Signature // untracked: what the index was built from
	building  bool
	failure   error
	done      chan struct{} // closed when the build in progress ends
}

// Acquire marks path as used by one more owner (an app, the database port) until release: the last release closes
// its container and indexes, so its files can be moved or removed.
func (manager *Manager) Acquire(path string) (release func(), err error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	manager.start.Do(func() {
		go manager.work()
		go manager.watch()
	})
	manager.mutex.Lock()
	entry := manager.files[absolute]
	if entry == nil {
		entry = &file{manager: manager, path: absolute, tables: map[string]*Table{}, version: -1}
		manager.files[absolute] = entry
	}
	entry.users++
	manager.mutex.Unlock()
	var once sync.Once
	return func() { once.Do(func() { manager.release(entry) }) }, nil
}

func (manager *Manager) release(entry *file) {
	manager.mutex.Lock()
	entry.users--
	if entry.users > 0 {
		manager.mutex.Unlock()
		return
	}
	delete(manager.files, entry.path)
	tables := entry.tables
	entry.tables = map[string]*Table{}
	container := entry.container
	entry.container = nil
	manager.mutex.Unlock()
	for _, table := range tables {
		table.retired.Store(true)
		table.swap(nil, nil)
	}
	if container != nil {
		_ = container.Close()
	}
	entry.readerMutex.Lock()
	if entry.reader != nil {
		_ = entry.reader.Close()
		_ = entry.database.Close()
		entry.reader, entry.database = nil, nil
	}
	entry.readerMutex.Unlock()
}

// Open gives the index of table in path over columns, opening it the first time: used as it is when its table has
// not moved, else built. A table already open over the same columns is that index; over other columns it is replaced,
// unless declared (application.work) holds it. path must be acquired.
func (manager *Manager) Open(path, table, label string, columns []Column, declared bool) (*Table, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	manager.mutex.Lock()
	entry := manager.files[absolute]
	if entry == nil {
		manager.mutex.Unlock()
		return nil, fmt.Errorf("fulltext: %s is not acquired", filepath.Base(path))
	}
	key := strings.ToLower(table)
	current := entry.tables[key]
	if current != nil && (current.declared && !declared || reflect.DeepEqual(current.columns, columns)) {
		manager.mutex.Unlock()
		if !current.tracked && !current.declared {
			// the engine may have begun to keep the file's history since: its index then folds changes in
			if tracked, err := entry.tracks(context.Background()); err == nil && tracked {
				return manager.replace(entry, current, table, label, columns, declared)
			}
		}
		return current, nil
	}
	manager.mutex.Unlock()
	return manager.replace(entry, current, table, label, columns, declared)
}

func (manager *Manager) replace(entry *file, current *Table, table, label string, columns []Column, declared bool) (*Table, error) {
	fields := make([]search.Field, 0, len(columns))
	for _, column := range columns {
		fields = append(fields, search.Text(column.Name, search.VietnameseAnalyzer(), search.Boost(column.Boost)))
	}
	definition, err := search.NewSchema(fields...)
	if err != nil {
		return nil, fmt.Errorf("the search index of %s: %w", label, err)
	}
	// a file that cannot be read yet (not there) is not tracked: its build says why; a table application.work declares
	// is the host's, written by whoever writes it
	tracked, _ := entry.tracks(context.Background())
	created := &Table{manager: manager, file: entry, name: table, label: label, columns: columns, definition: definition,
		declared: declared, tracked: tracked && !declared}
	manager.mutex.Lock()
	if entry.users == 0 {
		manager.mutex.Unlock()
		return nil, fmt.Errorf("fulltext: %s is not acquired", filepath.Base(entry.path))
	}
	entry.tables[strings.ToLower(table)] = created
	manager.mutex.Unlock()
	if current != nil {
		current.retired.Store(true)
		current.swap(nil, nil)
	}
	if err := manager.reuse(created); err != nil {
		manager.enqueue(created)
	}
	return created, nil
}

// Lookup is the index of table in path, nil when none is open.
func (manager *Manager) Lookup(path, table string) *Table {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil
	}
	manager.mutex.Lock()
	defer manager.mutex.Unlock()
	if entry := manager.files[absolute]; entry != nil {
		return entry.tables[strings.ToLower(table)]
	}
	return nil
}

// Name is the table's name; Label what messages call the index; Columns its columns in order.
func (table *Table) Name() string      { return table.name }
func (table *Table) Label() string     { return table.label }
func (table *Table) Columns() []Column { return table.columns }
func (table *Table) Tracked() bool     { return table.tracked }
func (table *Table) Path() string      { return table.file.path }
func (table *Table) Built() int64      { return table.built.Load() }
func (table *Table) ColumnNames() []string {
	names := make([]string, len(table.columns))
	for position, column := range table.columns {
		names[position] = column.Name
	}
	return names
}

// Ready reports an index that answers.
func (table *Table) Ready() bool {
	table.mutex.RLock()
	defer table.mutex.RUnlock()
	return table.index != nil
}

// Reading runs read with an up-to-date index held: a tracked table's changes are folded in first; a build in
// progress is waited for up to Wait.
func (table *Table) Reading(ctx context.Context, read func(*search.Index) error) error {
	if table.tracked {
		if err := table.catchUp(ctx); err != nil {
			return err
		}
	}
	deadline := time.NewTimer(Wait)
	defer deadline.Stop()
	for {
		table.mutex.RLock()
		if table.index != nil {
			defer table.mutex.RUnlock()
			return read(table.index)
		}
		building, failure, done := table.building, table.failure, table.done
		table.mutex.RUnlock()
		switch {
		case building && done != nil:
			select {
			case <-done:
				continue
			case <-deadline.C:
			case <-ctx.Done():
				return ctx.Err()
			}
		case failure != nil:
			return fmt.Errorf("the search index of %s could not be built: %w", table.label, failure)
		}
		return &BuildingError{Name: table.label, Built: table.built.Load(), Total: table.total.Load()}
	}
}

// Search ranks the rows that match query, at most limit (up to search.MaximumSearchLimit).
func (table *Table) Search(ctx context.Context, query search.MatchQuery, limit int) ([]search.Hit, error) {
	var hits []search.Hit
	err := table.Reading(ctx, func(index *search.Index) error {
		found, err := index.Search(ctx, query, search.SearchOptions{Limit: min(max(limit, 1), search.MaximumSearchLimit)})
		hits = found
		return err
	})
	return hits, err
}

// RowID is a hit's rowid.
func RowID(hit search.Hit) (int64, error) {
	return strconv.ParseInt(hit.ID, 10, 64)
}

// reuse uses the index the container holds when it was built from the table as it is: the same columns, and a
// tracked table's revision not past, an untracked table's signature unchanged.
func (manager *Manager) reuse(table *Table) error {
	ctx := context.Background()
	if !exists(table.file.path) {
		return fmt.Errorf("no file")
	}
	container, err := table.file.open(ctx)
	if err != nil {
		return err
	}
	index, encoded, err := container.Index(table.name, table.definition)
	if err != nil {
		return err
	}
	var held state
	if err := json.Unmarshal(encoded, &held); err != nil || !reflect.DeepEqual(held.Columns, table.columns) || held.Tracked != table.tracked ||
		held.Tracked && !held.History {
		_ = index.Close()
		return fmt.Errorf("the index was built from other columns")
	}
	if table.tracked {
		revision, err := table.file.revision(ctx)
		if err != nil || held.Revision > revision {
			_ = index.Close()
			return fmt.Errorf("the table's revision went back")
		}
		table.mutex.Lock()
		table.revision = held.Revision
		table.mutex.Unlock()
		table.swap(index, nil)
		return nil
	}
	if held.Signature == nil {
		_ = index.Close()
		return fmt.Errorf("the index has no signature")
	}
	signature, err := readSignature(ctx, table.file.path, table.name)
	if err != nil || signature != *held.Signature {
		_ = index.Close()
		return fmt.Errorf("the table changed since its index was built")
	}
	table.mutex.Lock()
	table.signature = signature
	table.mutex.Unlock()
	table.swap(index, nil)
	fmt.Printf("[database] search %s: index of %d rows, unchanged since it was built\n", table.label, signature.Rows)
	return nil
}

func (manager *Manager) enqueue(table *Table) {
	if table.retired.Load() || !table.queued.CompareAndSwap(false, true) {
		return
	}
	table.mutex.Lock()
	table.building = true
	if table.done == nil {
		table.done = make(chan struct{})
	}
	table.mutex.Unlock()
	select {
	case manager.queue <- table:
	default:
		table.queued.Store(false) // the queue is full: the next read or watch queues it again
	}
}

func (manager *Manager) work() {
	for table := range manager.queue {
		table.queued.Store(false)
		if table.retired.Load() {
			table.finish(nil)
			continue
		}
		started := time.Now()
		if !table.tracked {
			fmt.Printf("[database] search %s: building the index of %s\n", table.label, strings.Join(table.ColumnNames(), ", "))
		}
		rows, err := table.build(context.Background())
		if err != nil {
			if !table.retired.Load() {
				fmt.Printf("[database] search %s: the index could not be built: %v\n", table.label, err)
			}
			table.finish(err)
			continue
		}
		if !table.tracked {
			fmt.Printf("[database] search %s: index of %d rows built in %s\n", table.label, rows, time.Since(started).Round(time.Second))
		}
		table.finish(nil)
	}
}

// finish ends a build: its failure, if any, stays until the next build.
func (table *Table) finish(failure error) {
	table.mutex.Lock()
	table.building, table.failure = false, failure
	done := table.done
	table.done = nil
	table.mutex.Unlock()
	if done != nil {
		close(done)
	}
}

// build replaces the table's index with the table as one read transaction sees it, and gives how many rows it holds.
func (table *Table) build(ctx context.Context) (int64, error) {
	table.folding.Lock()
	defer table.folding.Unlock()
	if !exists(table.file.path) {
		return 0, fmt.Errorf("the database has no file %s", filepath.Base(table.file.path))
	}
	container, err := table.file.open(ctx)
	if err != nil {
		return 0, err
	}
	database, connection, err := openFile(ctx, table.file.path)
	if err != nil {
		return 0, err
	}
	defer database.Close()
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, "BEGIN"); err != nil {
		return 0, err
	}
	defer func() { _, _ = connection.ExecContext(context.WithoutCancel(ctx), "ROLLBACK") }()
	held := state{Columns: table.columns, Tracked: table.tracked, History: table.tracked}
	var rows int64
	if table.tracked {
		revision, err := revisionOf(ctx, connection)
		if err != nil {
			return 0, err
		}
		held.Revision = revision
		if err := connection.QueryRowContext(ctx, "SELECT count(*) FROM "+quote(table.name)).Scan(&rows); err != nil {
			return 0, err
		}
	} else {
		signature, err := signatureOf(ctx, connection, table.name)
		if err != nil {
			return 0, err
		}
		held.Revision, held.Signature, rows = uint64(max(signature.Revision, 0)), &signature, signature.Rows
	}
	table.total.Store(rows)
	table.built.Store(0)
	err = container.Replace(ctx, table.name, table.definition, held, buildOptions, func(add func(search.Document) error) error {
		return table.documents(ctx, connection, "", nil, func(id int64, fields map[string]string) error {
			if err := add(search.Document{ID: strconv.FormatInt(id, 10), Fields: fields}); err != nil {
				return err
			}
			table.built.Add(1)
			return nil
		})
	})
	if err != nil {
		return 0, err
	}
	index, _, err := container.Index(table.name, table.definition)
	if err != nil {
		return 0, err
	}
	table.mutex.Lock()
	table.revision = held.Revision
	if held.Signature != nil {
		table.signature = *held.Signature
	}
	table.mutex.Unlock()
	table.swap(index, nil)
	return rows, nil
}

// documents reads the rows of the table (those of rowids when where is set) and gives each one's searchable text.
func (table *Table) documents(ctx context.Context, connection *sql.Conn, where string, arguments []any, add func(int64, map[string]string) error) error {
	statement := "SELECT _rowid_"
	for _, column := range table.columns {
		statement += ", " + quote(column.Name)
	}
	statement += " FROM " + quote(table.name) + where
	rows, err := connection.QueryContext(ctx, statement, arguments...)
	if err != nil {
		if strings.Contains(err.Error(), "_rowid_") {
			return fmt.Errorf("table %s has no rowid (WITHOUT ROWID): the search index holds rows by rowid", table.name)
		}
		return err
	}
	defer rows.Close()
	values := make([]any, len(table.columns)+1)
	pointers := make([]any, len(values))
	for position := range values {
		pointers[position] = &values[position]
	}
	for rows.Next() {
		if err := rows.Scan(pointers...); err != nil {
			return err
		}
		rowID, ok := values[0].(int64)
		if !ok {
			return fmt.Errorf("table %s returned a rowid of type %T", table.name, values[0])
		}
		fields := make(map[string]string, len(table.columns))
		for position, column := range table.columns {
			fields[column.Name] = Text(values[position+1])
		}
		if err := add(rowID, fields); err != nil {
			return err
		}
	}
	return rows.Err()
}

// catchUp folds into a tracked table's index the rows changed since its revision, in one read transaction: a row
// that is still there is indexed again, one that is gone is removed. Changes to more than a quarter of the table, or
// a revision that went back, build the index again.
func (table *Table) catchUp(ctx context.Context) error {
	table.mutex.RLock()
	ready, held := table.index != nil, table.revision
	table.mutex.RUnlock()
	if !ready {
		table.manager.enqueue(table)
		return nil
	}
	current, err := table.file.revision(ctx)
	if err != nil || current == held {
		return err
	}
	rebuild, err := table.fold(ctx)
	if err != nil || !rebuild {
		return err
	}
	_, err = table.build(ctx)
	return err
}

// fold folds the changes in, or reports that the index is to be built again.
func (table *Table) fold(ctx context.Context) (bool, error) {
	table.folding.Lock()
	defer table.folding.Unlock()
	table.mutex.RLock()
	held := table.revision
	table.mutex.RUnlock()
	database, connection, err := openFile(ctx, table.file.path)
	if err != nil {
		return false, err
	}
	defer database.Close()
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, "BEGIN"); err != nil {
		return false, err
	}
	defer func() { _, _ = connection.ExecContext(context.WithoutCancel(ctx), "ROLLBACK") }()
	current, err := revisionOf(ctx, connection)
	if err != nil || current == held {
		return false, err
	}
	if current < held {
		return true, nil
	}
	var rows int64
	if err := connection.QueryRowContext(ctx, "SELECT count(*) FROM "+quote(table.name)).Scan(&rows); err != nil {
		return false, err
	}
	ids, rebuild, err := table.changedRows(ctx, connection, held, max(rows, 64)/foldShare)
	if err != nil || rebuild {
		return rebuild, err
	}
	if len(ids) == 0 {
		// the commits changed no row of the index (another table, a stock count): nothing to write
		table.mutex.Lock()
		table.revision = current
		table.mutex.Unlock()
		return false, nil
	}
	present := map[int64]map[string]string{}
	for start := 0; start < len(ids); start += 500 {
		batch := ids[start:min(start+500, len(ids))]
		arguments := make([]any, len(batch))
		marks := make([]string, len(batch))
		for position, id := range batch {
			arguments[position], marks[position] = id, "?"
		}
		err := table.documents(ctx, connection, " WHERE _rowid_ IN ("+strings.Join(marks, ", ")+")", arguments,
			func(id int64, fields map[string]string) error {
				present[id] = fields
				return nil
			})
		if err != nil {
			return false, err
		}
	}
	changes := make([]search.Change, 0, len(ids))
	for _, id := range ids {
		if fields, ok := present[id]; ok {
			changes = append(changes, search.Change{ID: strconv.FormatInt(id, 10), Fields: fields})
		} else {
			changes = append(changes, search.Change{ID: strconv.FormatInt(id, 10), Delete: true})
		}
	}
	container, err := table.file.open(ctx)
	if err != nil {
		return false, err
	}
	if err := container.Fold(ctx, table.name, table.definition, state{Revision: current, Columns: table.columns, Tracked: true, History: true},
		search.BuildOptions{SkipLongTerms: true}, changes); err != nil {
		return false, err
	}
	index, _, err := container.Index(table.name, table.definition)
	if err != nil {
		return false, err
	}
	table.mutex.Lock()
	table.revision = current
	table.mutex.Unlock()
	table.swap(index, nil)
	if container.NeedsCompaction() {
		return false, table.manager.compact(ctx, table.file)
	}
	return false, nil
}

// errEnough stops reading the history once the index is to be built again.
var errEnough = errors.New("enough")

// changedRows reads, inside the connection's transaction, the commits after revision and gives the rowids of the
// table they changed, in order, an update counted only when it changed a searchable column; rebuild when a commit
// changed the schema (a Barrier, a statement of Migrate) or more than most rows changed.
func (table *Table) changedRows(ctx context.Context, connection *sql.Conn, revision uint64, most int64) ([]int64, bool, error) {
	seen := map[int64]bool{}
	var ids []int64
	rebuild := false
	err := kitdbsqlite.ReadChanges(ctx, connection, revision, func(commit kitdbsqlite.Commit) error {
		for _, change := range commit.Changes {
			switch change.Operation {
			case kitdbsqlite.Barrier, kitdbsqlite.Statement:
				rebuild = true
				return errEnough
			}
			if !strings.EqualFold(change.Table, table.name) || seen[change.RowID] ||
				change.Operation == kitdbsqlite.Update && !table.textChanged(change) {
				continue
			}
			seen[change.RowID] = true
			ids = append(ids, change.RowID)
			if int64(len(ids)) > most {
				rebuild = true
				return errEnough
			}
		}
		return nil
	})
	if errors.Is(err, errEnough) {
		err = nil
	}
	sort.Slice(ids, func(left, right int) bool { return ids[left] < ids[right] })
	return ids, rebuild, err
}

// textChanged reports an update of a row that changed one of the index's columns: its value before and after differ.
func (table *Table) textChanged(change kitdbsqlite.Change) bool {
	if len(change.Before) != len(change.After) || len(change.Columns) != len(change.After) {
		return true
	}
	for _, column := range table.columns {
		for position, name := range change.Columns {
			if strings.EqualFold(name, column.Name) && Text(change.Before[position]) != Text(change.After[position]) {
				return true
			}
		}
	}
	return false
}

// compact writes the container's live sections into a new file and moves every index of the file onto it.
func (manager *Manager) compact(ctx context.Context, entry *file) error {
	manager.mutex.Lock()
	container := entry.container
	tables := make([]*Table, 0, len(entry.tables))
	for _, table := range entry.tables {
		tables = append(tables, table)
	}
	manager.mutex.Unlock()
	if container == nil {
		return nil
	}
	compaction, err := container.PrepareCompaction(ctx)
	if err != nil {
		return err
	}
	opened := map[*Table]*search.Index{}
	for _, table := range tables {
		if !table.Ready() {
			continue
		}
		index, _, err := compaction.Index(table.name, table.definition)
		if err != nil {
			for _, other := range opened {
				_ = other.Close()
			}
			compaction.Abort()
			return err
		}
		opened[table] = index
	}
	for table, index := range opened {
		table.swap(index, nil)
	}
	return compaction.Commit(ctx)
}

// swap makes index the one reads hold; the old one closes once no read holds it.
func (table *Table) swap(index *search.Index, failure error) {
	table.mutex.Lock()
	previous := table.index
	table.index = index
	if failure != nil {
		table.failure = failure
	}
	table.mutex.Unlock()
	if previous != nil {
		_ = previous.Close()
	}
}

// watch looks at each file of an untracked index every Watch: when another connection changed it, every untracked
// index of it whose table changed is built again.
func (manager *Manager) watch() {
	last := time.Now()
	for {
		// a short tick, so a Watch the tests shorten takes effect at once
		time.Sleep(min(Watch, 250*time.Millisecond))
		if time.Since(last) < Watch {
			continue
		}
		last = time.Now()
		manager.mutex.Lock()
		var tables []*Table
		for _, entry := range manager.files {
			for _, table := range entry.tables {
				if !table.tracked {
					tables = append(tables, table)
				}
			}
		}
		manager.mutex.Unlock()
		changes := map[*file]bool{}
		for _, table := range tables {
			changed, looked := changes[table.file]
			if !looked {
				found, err := table.file.changed(context.Background())
				changed = err == nil && found
				changes[table.file] = changed
			}
			if !changed || table.retired.Load() {
				continue
			}
			signature, err := readSignature(context.Background(), table.file.path, table.name)
			table.mutex.RLock()
			stale := table.index == nil && !table.building || err == nil && signature != table.signature
			table.mutex.RUnlock()
			if stale {
				manager.enqueue(table)
			}
		}
	}
}

// open is the file's container, opened once while the file is acquired.
func (entry *file) open(ctx context.Context) (*search.Container, error) {
	entry.manager.mutex.Lock()
	defer entry.manager.mutex.Unlock()
	if entry.users == 0 {
		return nil, search.ErrClosed
	}
	if entry.container != nil {
		return entry.container, nil
	}
	container, err := search.OpenContainer(ctx, entry.path+".search")
	if err != nil {
		return nil, err
	}
	entry.container = container
	return container, nil
}

// connection runs use on the connection kept open on the file.
func (entry *file) connection(ctx context.Context, use func(*sql.Conn) error) error {
	entry.readerMutex.Lock()
	defer entry.readerMutex.Unlock()
	if entry.reader == nil {
		database, connection, err := openFile(ctx, entry.path)
		if err != nil {
			return err
		}
		entry.database, entry.reader = database, connection
	}
	return use(entry.reader)
}

// changed reports whether another connection committed to the file since the last look; the first look counts as a
// change.
func (entry *file) changed(ctx context.Context) (bool, error) {
	var changed bool
	err := entry.connection(ctx, func(connection *sql.Conn) error {
		var version int64
		if err := connection.QueryRowContext(ctx, "PRAGMA data_version").Scan(&version); err != nil {
			return err
		}
		changed = version != entry.version
		entry.version = version
		return nil
	})
	return changed, err
}

// tracks reports a file whose history the engine keeps: it has kitwork_history.
func (entry *file) tracks(ctx context.Context) (bool, error) {
	tracked := false
	err := entry.connection(ctx, func(connection *sql.Conn) error {
		_, err := revisionOf(ctx, connection)
		switch {
		case err == nil:
			tracked = true
			return nil
		case errors.Is(err, errUntracked):
			return nil
		}
		return err
	})
	return tracked, err
}

// revision is the file's last committed revision now.
func (entry *file) revision(ctx context.Context) (uint64, error) {
	var revision uint64
	err := entry.connection(ctx, func(connection *sql.Conn) error {
		found, err := revisionOf(ctx, connection)
		revision = found
		return err
	})
	return revision, err
}

var errUntracked = errors.New("the file keeps no history")

// revisionOf is the last revision of the file's kitwork_history, inside the connection's transaction when it has one.
func revisionOf(ctx context.Context, connection *sql.Conn) (uint64, error) {
	var exists int
	if err := connection.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_schema WHERE type = 'table' AND name = 'kitwork_history'").Scan(&exists); err != nil {
		return 0, err
	}
	if exists == 0 {
		return 0, errUntracked
	}
	var revision int64
	err := connection.QueryRowContext(ctx, "SELECT coalesce(max(rev), 0) FROM kitwork_history").Scan(&revision)
	return uint64(max(revision, 0)), err
}

// openFile opens one connection to path, read-only. It waits for a lock another connection holds: two read-only
// connections opening a WAL file at once meet SQLITE_BUSY_RECOVERY otherwise.
func openFile(ctx context.Context, path string) (*sql.DB, *sql.Conn, error) {
	uri := "file:" + strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23").Replace(filepath.ToSlash(path)) + "?mode=ro&_pragma=busy_timeout(5000)"
	database, err := sql.Open("sqlite", uri)
	if err != nil {
		return nil, nil, err
	}
	database.SetMaxOpenConns(1)
	connection, err := database.Conn(ctx)
	if err != nil {
		_ = database.Close()
		return nil, nil, err
	}
	return database, connection, nil
}

func readSignature(ctx context.Context, path, table string) (Signature, error) {
	database, connection, err := openFile(ctx, path)
	if err != nil {
		return Signature{}, err
	}
	defer database.Close()
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, "BEGIN"); err != nil {
		return Signature{}, err
	}
	defer func() { _, _ = connection.ExecContext(context.WithoutCancel(ctx), "ROLLBACK") }()
	return signatureOf(ctx, connection, table)
}

// signatureOf reads the signature of an untracked table inside the connection's transaction.
func signatureOf(ctx context.Context, connection *sql.Conn, table string) (Signature, error) {
	var signature Signature
	if err := connection.QueryRowContext(ctx, "PRAGMA schema_version").Scan(&signature.SchemaVersion); err != nil {
		return signature, err
	}
	var exists int
	if err := connection.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_schema WHERE type = 'table' AND name = ? COLLATE NOCASE", table).Scan(&exists); err != nil {
		return signature, err
	}
	if exists == 0 {
		return signature, fmt.Errorf("the database has no table %s", table)
	}
	if err := connection.QueryRowContext(ctx, "SELECT count(*), coalesce(max(_rowid_), 0) FROM "+quote(table)).
		Scan(&signature.Rows, &signature.LastRowID); err != nil {
		if strings.Contains(err.Error(), "_rowid_") {
			return signature, fmt.Errorf("table %s has no rowid (WITHOUT ROWID): the search index holds rows by rowid", table)
		}
		return signature, err
	}
	var history int
	if err := connection.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_schema WHERE type = 'table' AND name = 'kitwork_history'").Scan(&history); err != nil {
		return signature, err
	}
	if history > 0 {
		if err := connection.QueryRowContext(ctx, "SELECT coalesce(max(rev), 0) FROM kitwork_history").Scan(&signature.Revision); err != nil {
			return signature, err
		}
	}
	return signature, nil
}

func exists(path string) bool {
	information, err := os.Stat(path)
	return err == nil && information.Mode().IsRegular()
}

// Text is a stored value as the index reads it.
func Text(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case []byte:
		return string(typed)
	}
	return fmt.Sprint(value)
}

func quote(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}
