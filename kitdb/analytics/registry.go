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

// Registry keeps the copies a process reads, one per table of a file, and decides them (KITWORK_KITDB.md mục 6 and
// 7: the engine decides, nobody declares one). A query that would scan a table asks for the columns it reads; a table
// without a copy holding them gets one built in the background, of every column asked for so far, when it holds at
// least MinimumRows rows; the query reads the rows meanwhile. A copy that went stale, or whose changed rows grew past
// a twentieth of it, is built again the same way.
type Registry struct {
	// MinimumRows is the fewest rows a table has a copy for (100,000 when zero).
	MinimumRows int64
	// Place is the directory of the copies of the file at path (Directory, beside it, when nil).
	Place func(path string) string

	mutex   sync.Mutex
	entries map[registryKey]*registryEntry
	ctx     context.Context
	cancel  context.CancelFunc
	builds  sync.WaitGroup
}

type registryKey struct{ path, table string }

type registryEntry struct {
	table    *Table
	users    map[*Table]int // the queries reading each table, this one or one a build replaced
	wanted   map[string]bool
	building bool
	small    time.Time // when the table was found too small; asked again an hour later
	failed   time.Time // when a build failed; tried again ten minutes later
}

// NewRegistry is a registry of no copy yet.
func NewRegistry() *Registry {
	ctx, cancel := context.WithCancel(context.Background())
	return &Registry{entries: map[registryKey]*registryEntry{}, ctx: ctx, cancel: cancel}
}

func (r *Registry) place(path string) string {
	if r.Place != nil {
		return r.Place(path)
	}
	return Directory(path)
}

func (r *Registry) minimumRows() int64 {
	if r.MinimumRows > 0 {
		return r.MinimumRows
	}
	return 100_000
}

// Acquire gives the copy of table of the file at path when it holds every column of columns; release gives it back.
// Without one it returns nil, and a build of the table with those columns (and every one asked for before) starts in
// the background unless one runs or the table was found too small.
func (r *Registry) Acquire(path, table string, columns []string) (*Table, func()) {
	path = filepath.Clean(path)
	key := registryKey{path, table}
	r.mutex.Lock()
	defer r.mutex.Unlock()
	entry := r.entries[key]
	if entry == nil {
		entry = &registryEntry{wanted: map[string]bool{}, users: map[*Table]int{}}
		r.entries[key] = entry
		if opened, err := OpenTable(r.place(path), table); err == nil && opened != nil {
			entry.table = opened
			for _, column := range opened.Manifest().Columns {
				entry.wanted[column.Name] = true
			}
		}
	}
	missing := false
	for _, column := range columns {
		if !entry.wanted[column] {
			entry.wanted[column] = true
			missing = true
		}
	}
	if entry.table != nil && !missing && holdsAll(entry.table.Manifest(), columns) {
		table := entry.table
		entry.users[table]++
		return table, func() { r.release(key, table) }
	}
	r.startBuild(key, entry)
	return nil, func() {}
}

// Stale says table's copy no longer reaches the snapshots, or holds too many changed rows: it is built again.
func (r *Registry) Stale(path, table string) {
	key := registryKey{filepath.Clean(path), table}
	r.mutex.Lock()
	defer r.mutex.Unlock()
	if entry := r.entries[key]; entry != nil {
		entry.failed = time.Time{}
		r.startBuild(key, entry)
	}
}

func holdsAll(manifest *Manifest, columns []string) bool {
	for _, column := range columns {
		if manifest.position(column) < 0 {
			return false
		}
	}
	return true
}

func (r *Registry) release(key registryKey, table *Table) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	entry := r.entries[key]
	if entry == nil {
		_ = table.Close() // the registry was closed while it was read
		return
	}
	entry.users[table]--
	if entry.users[table] <= 0 {
		delete(entry.users, table)
		if table != entry.table {
			_ = table.Close() // a build replaced it and its last reader is done
		}
	}
}

// startBuild builds the entry's copy in the background; the caller holds the mutex.
func (r *Registry) startBuild(key registryKey, entry *registryEntry) {
	switch {
	case entry.building, r.ctx.Err() != nil:
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
		table, small, err := r.build(key, columns)
		r.mutex.Lock()
		defer r.mutex.Unlock()
		entry.building = false
		switch {
		case err != nil:
			entry.failed = time.Now()
			if !errors.Is(err, context.Canceled) {
				slog.Warn("analytics: the copy was not built", "file", key.path, "table", key.table, "error", err)
			}
		case small:
			entry.small = time.Now()
		default:
			if previous := entry.table; previous != nil && entry.users[previous] == 0 {
				_ = previous.Close() // else its last reader closes it
			}
			entry.table = table
			if !holdsAll(table.Manifest(), slices.Collect(maps.Keys(entry.wanted))) {
				r.startBuild(key, entry) // columns asked for while it was built
			}
		}
	}()
}

// build builds the copy from a read transaction of a connection of its own, when the table is big enough.
func (r *Registry) build(key registryKey, columns []string) (*Table, bool, error) {
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(key.path)+"?mode=ro&_pragma=busy_timeout(10000)")
	if err != nil {
		return nil, false, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	tx, err := db.BeginTx(r.ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	var rows int64
	if err := tx.QueryRowContext(r.ctx, `SELECT count(*) FROM `+quoteIdentifier(key.table)).Scan(&rows); err != nil {
		return nil, false, err
	}
	if rows < r.minimumRows() {
		return nil, true, nil
	}
	start := time.Now()
	manifest, err := Build(r.ctx, tx, "main", r.place(key.path), key.table, columns)
	if err != nil {
		return nil, false, err
	}
	slog.Info("analytics: copy built", "file", key.path, "table", key.table, "columns", len(manifest.Columns),
		"rows", manifest.Rows, "bytes", manifest.Bytes, "revision", manifest.Revision, "took", time.Since(start).Round(time.Millisecond))
	table, err := OpenTable(r.place(key.path), key.table)
	return table, false, err
}

// Wait waits for the builds running.
func (r *Registry) Wait() { r.builds.Wait() }

// Close stops the builds and closes every copy.
func (r *Registry) Close() error {
	r.cancel()
	r.builds.Wait()
	r.mutex.Lock()
	defer r.mutex.Unlock()
	var errs []error
	for _, entry := range r.entries {
		if entry.table != nil && entry.users[entry.table] == 0 {
			errs = append(errs, entry.table.Close()) // a table still read is closed by its reader's release
		}
	}
	r.entries = map[registryKey]*registryEntry{}
	return errors.Join(errs...)
}
