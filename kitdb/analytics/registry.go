package analytics

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"maps"
	"path/filepath"
	"slices"
	"sync"
	"time"
)

// Registry keeps the copies a process reads, the File of each database file and one copy per table in it. Which table
// has a copy, of which columns, the database declares (CREATE INDEX … USING columnar, kitwork_columnar; KITWORK_KITDB.md
// câu 13.11 (c), 07/11/2026, which replaced the engine deciding): a query asks for the table's declared columns, and a
// table without a copy of exactly those gets one built in the background when it holds at least MinimumRows rows; the
// query reads the rows meanwhile. A copy whose changed
// rows reach FoldRows is folded in the background, unless they are spread over many blocks while fewer than a 256th of
// the table's rows: then the fold waits for that many (Fold). One that went stale is built again. After either, a
// File holding more bytes no generation reads than ones it does is compacted.
type Registry struct {
	// MinimumRows is the fewest rows a table has a copy for (100,000 when zero).
	MinimumRows int64
	// FoldRows is how many changed rows a view reads before the copy is folded (4,096 when zero).
	FoldRows int
	// Place is the copies' file of the database file at path (Path, beside it, when nil).
	Place func(path string) string

	mutex  sync.Mutex
	files  map[string]*registryFile
	ctx    context.Context
	cancel context.CancelFunc
	builds sync.WaitGroup
}

// registryFile is one database file's copies.
type registryFile struct {
	file   *File // nil while it does not open
	failed time.Time
	tables map[string]*registryEntry
}

type registryEntry struct {
	table    *Table
	users    map[*Table]int // the queries reading each table, this one or one a build, fold or compaction replaced
	wanted   map[string]bool
	building bool
	folding  bool
	foldAt   int       // the changed rows a fold waits for when they were found spread; FoldRows when 0
	small    time.Time // when the table was found too small; asked again an hour later
	failed   time.Time // when a build failed; tried again ten minutes later
}

// NewRegistry is a registry of no copy yet.
func NewRegistry() *Registry {
	ctx, cancel := context.WithCancel(context.Background())
	return &Registry{files: map[string]*registryFile{}, ctx: ctx, cancel: cancel}
}

func (r *Registry) place(path string) string {
	if r.Place != nil {
		return r.Place(path)
	}
	return Path(path)
}

func (r *Registry) minimumRows() int64 {
	if r.MinimumRows > 0 {
		return r.MinimumRows
	}
	return 100_000
}

func (r *Registry) foldRows() int {
	if r.FoldRows > 0 {
		return r.FoldRows
	}
	return 4096
}

// fileOf is the copies of the database file at path, opened the first time; the caller holds the mutex.
func (r *Registry) fileOf(path string) *registryFile {
	entry := r.files[path]
	if entry == nil {
		entry = &registryFile{tables: map[string]*registryEntry{}}
		r.files[path] = entry
	}
	if entry.file == nil && (entry.failed.IsZero() || time.Since(entry.failed) > 10*time.Minute) && r.ctx.Err() == nil {
		file, err := OpenFile(r.place(path))
		if err != nil {
			entry.failed = time.Now()
			slog.Warn("analytics: the copies' file does not open", "file", r.place(path), "error", err)
			return entry
		}
		file.Moved = func() { r.moveTables(path) }
		entry.file = file
	}
	return entry
}

// Acquire gives the copy of table of the file at path when it holds exactly columns, the columns the database declares
// for it; release gives it back. Without one it returns nil, and a build of the table with those columns starts in
// the background unless one runs or the table was found too small: a declaration that changed builds the copy again.
func (r *Registry) Acquire(path, table string, columns []string) (*Table, func()) {
	path = filepath.Clean(path)
	r.mutex.Lock()
	defer r.mutex.Unlock()
	file := r.fileOf(path)
	if file.file == nil {
		return nil, func() {}
	}
	entry := file.tables[table]
	if entry == nil {
		entry = &registryEntry{wanted: map[string]bool{}, users: map[*Table]int{}}
		file.tables[table] = entry
		if opened, err := file.file.Table(table); err == nil && opened != nil {
			entry.table = opened
		}
	}
	declared := make(map[string]bool, len(columns))
	for _, column := range columns {
		declared[column] = true
	}
	if !maps.Equal(entry.wanted, declared) {
		entry.wanted = declared
		entry.failed = time.Time{} // another declaration: its build is tried at once
	}
	if entry.table != nil && holdsExactly(entry.table.Manifest(), entry.wanted) {
		table := entry.table
		entry.users[table]++
		return table, func() { r.release(path, table) }
	}
	r.startBuild(path, table, file, entry)
	return nil, func() {}
}

// holdsExactly reports a copy whose columns are those of wanted, no more, no less.
func holdsExactly(manifest *Manifest, wanted map[string]bool) bool {
	if len(manifest.Columns) != len(wanted) {
		return false
	}
	for _, column := range manifest.Columns {
		if !wanted[column.Name] {
			return false
		}
	}
	return true
}

// Stale says table's copy no longer reaches the snapshots: it is built again.
func (r *Registry) Stale(path, table string) {
	path = filepath.Clean(path)
	r.mutex.Lock()
	defer r.mutex.Unlock()
	if file := r.files[path]; file != nil && file.file != nil && file.tables[table] != nil {
		entry := file.tables[table]
		entry.failed = time.Time{}
		r.startBuild(path, table, file, entry)
	}
}

// Changed is told the view a query read table's copy through: when it reads FoldRows changed rows or more, the copy
// is folded at its revision in the background.
func (r *Registry) Changed(path, table string, view *View) {
	if view.Changed() < r.foldRows() {
		return
	}
	path = filepath.Clean(path)
	r.mutex.Lock()
	defer r.mutex.Unlock()
	file := r.files[path]
	if file == nil || file.file == nil || r.ctx.Err() != nil {
		return
	}
	entry := file.tables[table]
	if entry == nil || entry.table != view.table || entry.building || entry.folding || view.Changed() < entry.foldAt {
		return
	}
	entry.folding = true
	section, changes, revision := view.table.copy.Manifest.Section, view.changed, view.Revision
	r.builds.Add(1)
	go func() {
		defer r.builds.Done()
		start := time.Now()
		manifest, err := file.file.fold(r.ctx, table, section, changes, revision, false)
		r.mutex.Lock()
		entry.folding = false
		switch {
		case errors.Is(err, errFoldSpread):
			if entry.table != nil && entry.table.copy.Manifest.Section == section {
				entry.foldAt = deferredRows(entry.table.copy.Manifest.Rows) // until a fold or a build moves it
			}
		case err == nil:
			slog.Info("analytics: copy folded", "file", path, "table", table, "rows", manifest.Rows, "changed", len(changes.rowIDs),
				"revision", revision, "took", time.Since(start).Round(time.Millisecond))
		case errors.Is(err, errFoldMoved), errors.Is(err, context.Canceled):
		default:
			slog.Warn("analytics: the copy was not folded; it is built again", "file", path, "table", table, "error", err)
			r.startBuild(path, table, file, entry)
		}
		r.mutex.Unlock()
		r.compact(path, file.file)
	}()
}

func holdsAll(manifest *Manifest, columns []string) bool {
	for _, column := range columns {
		if manifest.position(column) < 0 {
			return false
		}
	}
	return true
}

func (r *Registry) release(path string, table *Table) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	var entry *registryEntry
	if file := r.files[path]; file != nil {
		entry = file.tables[table.copy.Manifest.Table]
	}
	if entry == nil || entry.users[table] == 0 {
		_ = table.Close() // the registry was closed while it was read
		return
	}
	entry.users[table]--
	if entry.users[table] <= 0 {
		delete(entry.users, table)
		if table != entry.table {
			_ = table.Close() // another replaced it and its last reader is done
		}
	}
}

// moveTables opens each table of path's file again from its current generation, the rows a table already read
// changed kept when its main part is the same: the generations before close once their readers are done.
func (r *Registry) moveTables(path string) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	file := r.files[path]
	if file == nil || file.file == nil {
		return
	}
	for name, entry := range file.tables {
		previous := entry.table
		next, err := file.file.Table(name)
		if err != nil {
			slog.Warn("analytics: the copy does not open", "file", path, "table", name, "error", err)
			next = nil
		}
		if next != nil {
			next.carry(previous)
		}
		if next == nil || previous == nil || next.copy.Manifest.Section != previous.copy.Manifest.Section {
			entry.foldAt = 0 // a new main part: changed rows counted from it
		}
		entry.table = next
		if previous != nil && entry.users[previous] == 0 {
			_ = previous.Close() // else its last reader closes it
		}
	}
}

// startBuild builds the entry's copy in the background; the caller holds the mutex.
func (r *Registry) startBuild(path, table string, file *registryFile, entry *registryEntry) {
	switch {
	case entry.building, r.ctx.Err() != nil:
		return
	case len(entry.wanted) == 0:
		// no column declared: a copy of none holds nothing, its build fails, and the failure would hold back for ten
		// minutes the build a declaration asks for next
		return
	case !entry.small.IsZero() && time.Since(entry.small) < time.Hour:
		return
	case !entry.failed.IsZero() && time.Since(entry.failed) < 10*time.Minute:
		return
	}
	entry.building = true
	columns := slices.Sorted(maps.Keys(entry.wanted))
	r.builds.Add(1)
	go func() {
		defer r.builds.Done()
		small, err := r.build(path, table, file.file, columns)
		r.mutex.Lock()
		entry.building = false
		switch {
		case err != nil:
			entry.failed = time.Now()
			if !errors.Is(err, context.Canceled) {
				slog.Warn("analytics: the copy was not built", "file", path, "table", table, "error", err)
			}
		case small:
			entry.small = time.Now()
		case entry.table == nil || !holdsExactly(entry.table.Manifest(), entry.wanted):
			r.startBuild(path, table, file, entry) // the declaration changed while it was built
		}
		r.mutex.Unlock()
		if err == nil && !small {
			r.compact(path, file.file)
		}
	}()
}

// build builds the copy from a read transaction of a connection of its own, when the table is big enough; the File
// moves the registry's tables to it.
func (r *Registry) build(path, table string, file *File, columns []string) (bool, error) {
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro&_pragma=busy_timeout(10000)")
	if err != nil {
		return false, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	tx, err := db.BeginTx(r.ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var rows int64
	if err := tx.QueryRowContext(r.ctx, `SELECT count(*) FROM `+quoteIdentifier(table)).Scan(&rows); err != nil {
		return false, err
	}
	if rows < r.minimumRows() {
		return true, nil
	}
	start := time.Now()
	manifest, err := file.Build(r.ctx, tx, "main", table, columns)
	if err != nil {
		return false, err
	}
	slog.Info("analytics: copy built", "file", path, "table", table, "columns", len(manifest.Columns),
		"rows", manifest.Rows, "bytes", manifest.Bytes, "revision", manifest.Revision, "took", time.Since(start).Round(time.Millisecond))
	return false, nil
}

// compact compacts path's file when it needs it, waiting for the readers of the file before (a query at most).
func (r *Registry) compact(path string, file *File) {
	if !file.NeedsCompaction() {
		return
	}
	start := time.Now()
	before := file.Info()
	if err := file.Compact(r.ctx); err != nil {
		if !errors.Is(err, context.Canceled) {
			slog.Warn("analytics: the copies' file was not compacted", "file", path, "error", err)
		}
		return
	}
	slog.Info("analytics: the copies' file compacted", "file", path, "bytes before", before.FileBytes,
		"bytes", file.Info().FileBytes, "took", time.Since(start).Round(time.Millisecond))
}

// Wait waits for the builds and folds running.
func (r *Registry) Wait() { r.builds.Wait() }

// Close stops the builds and folds, and lets every copy go; a table still read keeps its generation until released.
func (r *Registry) Close() error {
	r.cancel()
	r.builds.Wait()
	r.mutex.Lock()
	defer r.mutex.Unlock()
	var errs []error
	for _, file := range r.files {
		for _, entry := range file.tables {
			if entry.table != nil && entry.users[entry.table] == 0 {
				errs = append(errs, entry.table.Close())
			}
		}
		if file.file != nil {
			errs = append(errs, file.file.Close())
		}
	}
	r.files = map[string]*registryFile{}
	return errors.Join(errs...)
}
