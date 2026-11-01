package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kitwork/engine/kitdb/gateway"
	"github.com/kitwork/engine/kitdb/pgwire"
	"github.com/kitwork/engine/search"
)

// app.database's search (KITWORK_KITDB.md question 13.5): the full-text indexes MATCH reads on the port. A declared
// table of a database of the host's .database/ has its index in its schema file's container, <file>.search
// (kitwork.db.search, billing.db.search), as engine/kitdb/sqlite keeps one: one index per table, named by the table,
// a document per row by rowid, the state {revision, columns} with it (plus the signature the port checks). At start
// an index is used when it was built from the table as it is (same columns, same signature: schema version, rows,
// last rowid, history revision); otherwise it is built in the background, one table at a time, and a MATCH before
// then answers how far the build is. Every 30 seconds a file another connection changed is looked at again, and an
// index whose table changed is built anew while the old one answers. One process owns a container: the kitdbsqlite
// tool does not run on the same files while the host does.

// appDatabaseSearchWatch is how often the files are looked at (a variable for the tests).
var appDatabaseSearchWatch = 30 * time.Second

const appDatabaseSearchQueue = 64

// appDatabaseSearchBuild bounds the segments of a build, as engine/kitdb/sqlite builds: large ones, so a big table
// starts with few segments.
var appDatabaseSearchBuild = search.BuildOptions{SkipLongTerms: true, FlushThresholdBytes: 192 << 20}

// appDatabaseSearchColumn and appDatabaseSearchState are written as engine/kitdb/sqlite writes its SearchColumn and
// searchState, so either reads the other's index.
type appDatabaseSearchColumn struct {
	Name  string
	Boost float64
}

type appDatabaseSearchState struct {
	Revision  uint64                      `json:"revision"`
	Columns   []appDatabaseSearchColumn   `json:"columns"`
	Signature *appDatabaseSearchSignature `json:"signature,omitempty"`
}

// appDatabaseSearchSignature tells whether a table is still what its index was built from.
type appDatabaseSearchSignature struct {
	SchemaVersion int64 `json:"schemaVersion"`
	Rows          int64 `json:"rows"`
	LastRowID     int64 `json:"lastRowid"`
	Revision      int64 `json:"revision"`
}

type appDatabaseSearch struct {
	cancel context.CancelFunc
	done   sync.WaitGroup
	queue  chan *appDatabaseSearchIndex

	mutex      sync.Mutex
	indexes    map[string]*appDatabaseSearchIndex // by database, schema and table, folded
	containers map[string]*search.Container       // by file
	watchers   map[string]*appDatabaseSearchWatcher
}

type appDatabaseSearchIndex struct {
	database, schema, table, file string
	columns                       []appDatabaseSearchColumn
	definition                    search.Schema
	queued                        atomic.Bool
	built, total                  atomic.Int64

	mutex     sync.RWMutex
	index     *search.Index
	signature appDatabaseSearchSignature
	building  bool
	failure   error
}

type appDatabaseSearchWatcher struct {
	database   *sql.DB
	connection *sql.Conn
	version    int64
}

func appDatabaseSearchKey(database, schema, table string) string {
	return strings.ToLower(database + "\x00" + schema + "\x00" + table)
}

// startAppDatabaseSearch opens or builds the declared indexes of the host's .database/ folder at root.
func startAppDatabaseSearch(parent context.Context, root string, declared []AppDatabaseSearch) (*appDatabaseSearch, error) {
	ctx, cancel := context.WithCancel(parent)
	manager := &appDatabaseSearch{
		cancel: cancel, queue: make(chan *appDatabaseSearchIndex, appDatabaseSearchQueue),
		indexes: map[string]*appDatabaseSearchIndex{}, containers: map[string]*search.Container{},
		watchers: map[string]*appDatabaseSearchWatcher{},
	}
	for _, declaration := range declared {
		file := filepath.Join(root, declaration.Database, "kitwork.db")
		if declaration.Schema != "public" {
			file = filepath.Join(root, declaration.Database, declaration.Schema+".db")
		}
		entry := &appDatabaseSearchIndex{database: declaration.Database, schema: declaration.Schema, table: declaration.Table, file: file}
		fields := make([]search.Field, 0, len(declaration.Columns))
		for _, column := range declaration.Columns {
			entry.columns = append(entry.columns, appDatabaseSearchColumn{Name: column.Name, Boost: column.Boost})
			fields = append(fields, search.Text(column.Name, search.VietnameseAnalyzer(), search.Boost(column.Boost)))
		}
		definition, err := search.NewSchema(fields...)
		if err != nil {
			cancel()
			return nil, fmt.Errorf("app.database search %s: %w", entry.name(), err)
		}
		entry.definition = definition
		manager.indexes[appDatabaseSearchKey(entry.database, entry.schema, entry.table)] = entry
	}
	manager.done.Add(2)
	go manager.work(ctx)
	go manager.watch(ctx)
	for _, entry := range manager.indexes {
		manager.open(ctx, entry)
	}
	return manager, nil
}

func (entry *appDatabaseSearchIndex) name() string {
	if entry.schema == "public" {
		return entry.database + "." + entry.table
	}
	return entry.database + "." + entry.schema + "." + entry.table
}

func (entry *appDatabaseSearchIndex) columnNames() []string {
	names := make([]string, len(entry.columns))
	for index, column := range entry.columns {
		names[index] = column.Name
	}
	return names
}

// container opens the container of a schema file once; the manager owns it until Close.
func (manager *appDatabaseSearch) container(ctx context.Context, file string) (*search.Container, error) {
	manager.mutex.Lock()
	defer manager.mutex.Unlock()
	if container, open := manager.containers[file]; open {
		return container, nil
	}
	container, err := search.OpenContainer(ctx, file+".search")
	if err != nil {
		return nil, err
	}
	manager.containers[file] = container
	return container, nil
}

// open uses the index the container has when the table is what it was built from, else queues a build.
func (manager *appDatabaseSearch) open(ctx context.Context, entry *appDatabaseSearchIndex) {
	if err := manager.reuse(ctx, entry); err != nil {
		manager.enqueue(entry)
	}
}

func (manager *appDatabaseSearch) reuse(ctx context.Context, entry *appDatabaseSearchIndex) error {
	if !fileExists(entry.file) {
		return fmt.Errorf("no file")
	}
	container, err := manager.container(ctx, entry.file)
	if err != nil {
		return err
	}
	index, encoded, err := container.Index(entry.table, entry.definition)
	if err != nil {
		return err
	}
	var state appDatabaseSearchState
	if err := json.Unmarshal(encoded, &state); err != nil || state.Signature == nil || !reflect.DeepEqual(state.Columns, entry.columns) {
		_ = index.Close()
		return fmt.Errorf("the index was built from other columns")
	}
	signature, err := readAppDatabaseSearchSignature(ctx, entry.file, entry.table)
	if err != nil || signature != *state.Signature {
		_ = index.Close()
		return fmt.Errorf("the table changed since its index was built")
	}
	entry.swap(index, signature, nil)
	fmt.Printf("[database] search %s: index of %d rows, unchanged since it was built\n", entry.name(), signature.Rows)
	return nil
}

func (manager *appDatabaseSearch) enqueue(entry *appDatabaseSearchIndex) {
	if !entry.queued.CompareAndSwap(false, true) {
		return
	}
	entry.mutex.Lock()
	entry.building = true
	entry.mutex.Unlock()
	select {
	case manager.queue <- entry:
	default:
		entry.queued.Store(false) // the queue is full: the next watch queues it again
	}
}

func (manager *appDatabaseSearch) work(ctx context.Context) {
	defer manager.done.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case entry := <-manager.queue:
			started := time.Now()
			fmt.Printf("[database] search %s: building the index of %s\n", entry.name(), strings.Join(entry.columnNames(), ", "))
			signature, err := manager.build(ctx, entry)
			entry.queued.Store(false)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				entry.mutex.Lock()
				entry.building, entry.failure = false, err
				entry.mutex.Unlock()
				fmt.Printf("[database] search %s: the index could not be built: %v\n", entry.name(), err)
				continue
			}
			fmt.Printf("[database] search %s: index of %d rows built in %s\n", entry.name(), signature.Rows,
				time.Since(started).Round(time.Second))
		}
	}
}

// build replaces the table's index with the table as one read transaction sees it.
func (manager *appDatabaseSearch) build(ctx context.Context, entry *appDatabaseSearchIndex) (appDatabaseSearchSignature, error) {
	if !fileExists(entry.file) {
		return appDatabaseSearchSignature{}, fmt.Errorf("the database has no file %s", filepath.Base(entry.file))
	}
	container, err := manager.container(ctx, entry.file)
	if err != nil {
		return appDatabaseSearchSignature{}, err
	}
	database, connection, err := openAppDatabaseSearchFile(ctx, entry.file)
	if err != nil {
		return appDatabaseSearchSignature{}, err
	}
	defer database.Close()
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, "BEGIN"); err != nil {
		return appDatabaseSearchSignature{}, err
	}
	defer func() { _, _ = connection.ExecContext(context.WithoutCancel(ctx), "ROLLBACK") }()
	signature, err := appDatabaseSearchSignatureOf(ctx, connection, entry.table)
	if err != nil {
		return appDatabaseSearchSignature{}, err
	}
	entry.total.Store(signature.Rows)
	entry.built.Store(0)
	state := appDatabaseSearchState{Revision: uint64(max(signature.Revision, 0)), Columns: entry.columns, Signature: &signature}
	err = container.Replace(ctx, entry.table, entry.definition, state, appDatabaseSearchBuild, func(add func(search.Document) error) error {
		statement := "SELECT _rowid_"
		for _, column := range entry.columns {
			statement += ", " + quoteAppDatabaseIdentifier(column.Name)
		}
		rows, err := connection.QueryContext(ctx, statement+" FROM "+quoteAppDatabaseIdentifier(entry.table))
		if err != nil {
			return err
		}
		defer rows.Close()
		values := make([]any, len(entry.columns)+1)
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
				return fmt.Errorf("table %s returned a rowid of type %T", entry.table, values[0])
			}
			fields := make(map[string]string, len(entry.columns))
			for position, column := range entry.columns {
				fields[column.Name] = appDatabaseSearchText(values[position+1])
			}
			if err := add(search.Document{ID: strconv.FormatInt(rowID, 10), Fields: fields}); err != nil {
				return err
			}
			entry.built.Add(1)
		}
		return rows.Err()
	})
	if err != nil {
		return appDatabaseSearchSignature{}, err
	}
	index, _, err := container.Index(entry.table, entry.definition)
	if err != nil {
		return appDatabaseSearchSignature{}, err
	}
	entry.swap(index, signature, nil)
	return signature, nil
}

// swap makes index the one searches read; the old one closes once no search holds it.
func (entry *appDatabaseSearchIndex) swap(index *search.Index, signature appDatabaseSearchSignature, failure error) {
	entry.mutex.Lock()
	previous := entry.index
	entry.index, entry.signature, entry.failure, entry.building = index, signature, failure, false
	entry.mutex.Unlock()
	if previous != nil {
		_ = previous.Close()
	}
}

// watch looks at each file every appDatabaseSearchWatch: when another connection changed it, every index of it whose
// table changed is built again.
func (manager *appDatabaseSearch) watch(ctx context.Context) {
	defer manager.done.Done()
	ticker := time.NewTicker(appDatabaseSearchWatch)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		for _, entry := range manager.snapshot() {
			if ctx.Err() != nil {
				return
			}
			changed, err := manager.fileChanged(ctx, entry.file)
			if err != nil || !changed {
				continue
			}
			signature, err := readAppDatabaseSearchSignature(ctx, entry.file, entry.table)
			entry.mutex.RLock()
			stale := entry.index == nil && !entry.building || err == nil && signature != entry.signature
			entry.mutex.RUnlock()
			if stale {
				manager.enqueue(entry)
			}
		}
	}
}

func (manager *appDatabaseSearch) snapshot() []*appDatabaseSearchIndex {
	manager.mutex.Lock()
	defer manager.mutex.Unlock()
	entries := make([]*appDatabaseSearchIndex, 0, len(manager.indexes))
	for _, entry := range manager.indexes {
		entries = append(entries, entry)
	}
	return entries
}

// fileChanged reports whether another connection committed to file since the last look (PRAGMA data_version of a
// connection kept open on it).
func (manager *appDatabaseSearch) fileChanged(ctx context.Context, file string) (bool, error) {
	manager.mutex.Lock()
	watcher := manager.watchers[file]
	manager.mutex.Unlock()
	if watcher == nil {
		if !fileExists(file) {
			return false, fmt.Errorf("no file")
		}
		database, connection, err := openAppDatabaseSearchFile(ctx, file)
		if err != nil {
			return false, err
		}
		// The first look counts as a change: a file that appeared, or an index that failed before it did, is looked at.
		watcher = &appDatabaseSearchWatcher{database: database, connection: connection, version: -1}
		manager.mutex.Lock()
		manager.watchers[file] = watcher
		manager.mutex.Unlock()
	}
	var version int64
	if err := watcher.connection.QueryRowContext(ctx, "PRAGMA data_version").Scan(&version); err != nil {
		return false, err
	}
	changed := version != watcher.version
	watcher.version = version
	return changed, nil
}

// Close stops the builds and the watch and closes every index and container.
func (manager *appDatabaseSearch) Close() error {
	if manager == nil {
		return nil
	}
	manager.cancel()
	manager.done.Wait()
	var err error
	for _, entry := range manager.snapshot() {
		entry.swap(nil, appDatabaseSearchSignature{}, nil)
	}
	manager.mutex.Lock()
	defer manager.mutex.Unlock()
	for _, watcher := range manager.watchers {
		err = errors.Join(err, watcher.connection.Close(), watcher.database.Close())
	}
	for _, container := range manager.containers {
		err = errors.Join(err, container.Close())
	}
	return err
}

// lookup finds a declared index and the fields a MATCH names (every declared column when none).
func (manager *appDatabaseSearch) lookup(database, schema, table string, fields []string) (*appDatabaseSearchIndex, []string, error) {
	var entry *appDatabaseSearchIndex
	if manager != nil {
		manager.mutex.Lock()
		entry = manager.indexes[appDatabaseSearchKey(database, schema, table)]
		manager.mutex.Unlock()
	}
	if entry == nil {
		return nil, nil, pgwire.NewError("42704", fmt.Sprintf(
			"table %s.%s has no search index: declare its columns in the search of app.database (application.work)", schema, table))
	}
	if len(fields) == 0 {
		return entry, entry.columnNames(), nil
	}
	named := make([]string, len(fields))
	for position, field := range fields {
		for _, column := range entry.columns {
			if strings.EqualFold(column.Name, field) {
				named[position] = column.Name
			}
		}
		if named[position] == "" {
			return nil, nil, pgwire.NewError("42703", fmt.Sprintf("column %s of %s.%s is not in its search index (searchable: %s)",
				field, schema, table, strings.Join(entry.columnNames(), ", ")))
		}
	}
	return entry, named, nil
}

// reading runs read with the entry's index held, or says why there is none yet.
func (entry *appDatabaseSearchIndex) reading(read func(*search.Index) error) error {
	entry.mutex.RLock()
	defer entry.mutex.RUnlock()
	if entry.index != nil {
		return read(entry.index)
	}
	if entry.building {
		total, built := entry.total.Load(), entry.built.Load()
		progress := "it has not started yet"
		if total > 0 {
			progress = fmt.Sprintf("%d of %d rows (%d%%)", built, total, built*100/total)
		}
		return pgwire.NewError("55000", fmt.Sprintf("the search index of %s is being built: %s; try again later", entry.name(), progress))
	}
	if entry.failure != nil {
		return pgwire.NewError("58000", fmt.Sprintf("the search index of %s could not be built: %v", entry.name(), entry.failure))
	}
	return pgwire.NewError("55000", fmt.Sprintf("the search index of %s is not built yet", entry.name()))
}

// appDatabaseSearchProvider answers MATCH on one database of the port (gateway.SearchProvider).
type appDatabaseSearchProvider struct {
	manager  *appDatabaseSearch
	database string
}

func (provider appDatabaseSearchProvider) Search(ctx context.Context, schema, table string, fields []string, query gateway.SearchQuery, limit int) ([]gateway.SearchHit, error) {
	entry, named, err := provider.manager.lookup(provider.database, schema, table, fields)
	if err != nil {
		return nil, err
	}
	var hits []gateway.SearchHit
	err = entry.reading(func(index *search.Index) error {
		found, err := index.Search(ctx, appDatabaseMatchQuery(named, query),
			search.SearchOptions{Limit: min(max(limit, 1), search.MaximumSearchLimit)})
		if err != nil {
			return pgwire.NewError("42601", "MATCH: "+err.Error())
		}
		hits = make([]gateway.SearchHit, 0, len(found))
		for _, hit := range found {
			rowID, err := strconv.ParseInt(hit.ID, 10, 64)
			if err != nil {
				return fmt.Errorf("search index of %s holds a document %q that is not a rowid", entry.name(), hit.ID)
			}
			hits = append(hits, gateway.SearchHit{RowID: rowID, Score: hit.Score})
		}
		return nil
	})
	return hits, err
}

// Count counts the matches: on the index alone for words (each required); for OR, a phrase or a prefix, by ranking
// them, at most search.MaximumSearchLimit.
func (provider appDatabaseSearchProvider) Count(ctx context.Context, schema, table string, fields []string, query gateway.SearchQuery) (uint64, error) {
	if query.Kind != gateway.SearchWords {
		hits, err := provider.Search(ctx, schema, table, fields, query, search.MaximumSearchLimit)
		if err == nil && len(hits) == search.MaximumSearchLimit {
			return 0, pgwire.NewError("54000", fmt.Sprintf(
				"MATCH: counting OR, a phrase or a prefix reads at most %d matches, and this query has more", search.MaximumSearchLimit))
		}
		return uint64(len(hits)), err
	}
	entry, named, err := provider.manager.lookup(provider.database, schema, table, fields)
	if err != nil {
		return 0, err
	}
	var count uint64
	err = entry.reading(func(index *search.Index) error {
		count, err = index.Count(ctx, appDatabaseMatchQuery(named, query), nil)
		if err != nil {
			return pgwire.NewError("42601", "MATCH: "+err.Error())
		}
		return nil
	})
	return count, err
}

// Fields names the declared columns a MATCH reads (every one when fields is empty), with their weights.
func (provider appDatabaseSearchProvider) Fields(_ context.Context, schema, table string, fields []string) ([]gateway.SearchField, error) {
	entry, named, err := provider.manager.lookup(provider.database, schema, table, fields)
	if err != nil {
		return nil, err
	}
	result := make([]gateway.SearchField, 0, len(named))
	for _, name := range named {
		for _, column := range entry.columns {
			if column.Name == name {
				result = append(result, gateway.SearchField{Name: column.Name, Boost: column.Boost})
			}
		}
	}
	return result, nil
}

// appDatabaseMatchQuery is the search engine's query for one kind of MATCH query.
func appDatabaseMatchQuery(fields []string, query gateway.SearchQuery) search.MatchQuery {
	switch query.Kind {
	case gateway.SearchAny:
		return search.MatchQuery{Fields: fields, Text: query.Text, Operator: search.QueryAny}
	case gateway.SearchPhrase:
		return search.MatchQuery{Fields: fields, Phrase: query.Text}
	case gateway.SearchPrefix:
		return search.MatchQuery{Fields: fields, Prefix: query.Text}
	}
	return search.MatchQuery{Fields: fields, Text: query.Text}
}

// openAppDatabaseSearchFile opens one connection to file, read-only.
func openAppDatabaseSearchFile(ctx context.Context, file string) (*sql.DB, *sql.Conn, error) {
	uri := "file:" + strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23").Replace(filepath.ToSlash(file)) + "?mode=ro"
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

func readAppDatabaseSearchSignature(ctx context.Context, file, table string) (appDatabaseSearchSignature, error) {
	database, connection, err := openAppDatabaseSearchFile(ctx, file)
	if err != nil {
		return appDatabaseSearchSignature{}, err
	}
	defer database.Close()
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, "BEGIN"); err != nil {
		return appDatabaseSearchSignature{}, err
	}
	defer func() { _, _ = connection.ExecContext(context.WithoutCancel(ctx), "ROLLBACK") }()
	return appDatabaseSearchSignatureOf(ctx, connection, table)
}

// appDatabaseSearchSignatureOf reads the signature of table inside the connection's transaction.
func appDatabaseSearchSignatureOf(ctx context.Context, connection *sql.Conn, table string) (appDatabaseSearchSignature, error) {
	var signature appDatabaseSearchSignature
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
	if err := connection.QueryRowContext(ctx, "SELECT count(*), coalesce(max(_rowid_), 0) FROM "+quoteAppDatabaseIdentifier(table)).
		Scan(&signature.Rows, &signature.LastRowID); err != nil {
		if strings.Contains(err.Error(), "_rowid_") {
			return signature, fmt.Errorf("table %s has no rowid (WITHOUT ROWID): MATCH indexes rows by rowid", table)
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

func appDatabaseSearchText(value any) string {
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

func quoteAppDatabaseIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}
