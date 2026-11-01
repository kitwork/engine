package engine

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/kitwork/engine/database/fulltext"
	"github.com/kitwork/engine/kitdb/gateway"
	"github.com/kitwork/engine/kitdb/pgwire"
	"github.com/kitwork/engine/search"
)

// app.database's search (KITWORK_KITDB.md question 13.5): the full-text indexes MATCH reads on the port. The indexes
// are engine/database/fulltext's, one container per file for the whole process, so an app's matches and the port's
// MATCH read the same index. A declared table of a database of the host's .database/ has its index in its schema
// file's container, <file>.search (kitwork.db.search, billing.db.search): used at start when it was built from the
// table as it is, otherwise built in the background, one table at a time, and a MATCH before then answers how far the
// build is; a file another connection changed is looked at again every fulltext.Watch, and an index whose table
// changed is built anew while the old one answers. One process owns a container: the kitdbsqlite tool does not run on
// the same files while the host does.
//
// A table of an app's database needs no declaration in application.work: its .searchable() columns (kitwork/schema,
// recorded with their weight in kitwork_columns, db.sqlite_columns.go in work) make its index, found when a session
// opens the database, in the app's .database/<name>/kitwork.db.search. The engine tracks such a table, so its index
// folds in the rows changed since it last read before each MATCH. When the columns change the index is built again
// from them; what application.work declares for a table wins over its file.

// appDatabaseSearch is what the port holds of the indexes: the files it acquired, released when it closes.
type appDatabaseSearch struct {
	mutex    sync.Mutex
	releases map[string]func() // by file
	closed   bool
}

// startAppDatabaseSearch opens or builds the declared indexes of the host's .database/ folder at root.
func startAppDatabaseSearch(_ context.Context, root string, declared []AppDatabaseSearch) (*appDatabaseSearch, error) {
	manager := &appDatabaseSearch{releases: map[string]func(){}}
	for _, declaration := range declared {
		file := filepath.Join(root, declaration.Database, "kitwork.db")
		if declaration.Schema != "public" {
			file = filepath.Join(root, declaration.Database, declaration.Schema+".db")
		}
		columns := make([]fulltext.Column, 0, len(declaration.Columns))
		for _, column := range declaration.Columns {
			columns = append(columns, fulltext.Column{Name: column.Name, Boost: column.Boost})
		}
		label := appDatabaseSearchLabel(declaration.Database, declaration.Schema, declaration.Table)
		if err := manager.acquire(file); err != nil {
			_ = manager.Close()
			return nil, fmt.Errorf("app.database search %s: %w", label, err)
		}
		if _, err := fulltext.Shared().Open(file, declaration.Table, label, columns, true); err != nil {
			_ = manager.Close()
			return nil, fmt.Errorf("app.database search %s: %w", label, err)
		}
	}
	return manager, nil
}

func appDatabaseSearchLabel(database, schema, table string) string {
	if schema == "public" {
		return database + "." + table
	}
	return database + "." + schema + "." + table
}

// acquire holds file for the port until Close.
func (manager *appDatabaseSearch) acquire(file string) error {
	manager.mutex.Lock()
	defer manager.mutex.Unlock()
	if manager.closed {
		return fmt.Errorf("the database port is closing")
	}
	if manager.releases[file] != nil {
		return nil
	}
	release, err := fulltext.Shared().Acquire(file)
	if err != nil {
		return err
	}
	manager.releases[file] = release
	return nil
}

// discover opens the indexes the files of a database ask for: each table whose kitwork_columns gives a column a search
// weight, over those columns.
func (manager *appDatabaseSearch) discover(database string, schemas map[string]string, columns appDatabaseColumns) {
	if manager == nil {
		return
	}
	type place struct{ schema, table string }
	wanted := map[place][]fulltext.Column{}
	for _, column := range columns {
		if column.weight <= 0 || schemas[column.schema] == "" {
			continue
		}
		at := place{column.schema, column.table}
		wanted[at] = append(wanted[at], fulltext.Column{Name: column.column, Boost: float64(column.weight)})
	}
	for at, found := range wanted {
		sort.Slice(found, func(left, right int) bool { return found[left].Name < found[right].Name })
		label := appDatabaseSearchLabel(database, at.schema, at.table)
		file := schemas[at.schema]
		if err := manager.acquire(file); err != nil {
			continue
		}
		if _, err := fulltext.Shared().Open(file, at.table, label, found, false); err != nil {
			fmt.Printf("[database] search %s: the index cannot be made: %v\n", label, err)
		}
	}
}

// Close releases every file the port acquired.
func (manager *appDatabaseSearch) Close() error {
	if manager == nil {
		return nil
	}
	manager.mutex.Lock()
	manager.closed = true
	releases := manager.releases
	manager.releases = map[string]func(){}
	manager.mutex.Unlock()
	for _, release := range releases {
		release()
	}
	return nil
}

// appDatabaseSearchProvider answers MATCH on one database of the port (gateway.SearchProvider).
type appDatabaseSearchProvider struct {
	database string
	schemas  map[string]string // schema → file
}

// lookup finds the index of a table and the fields a MATCH names (every indexed column when none).
func (provider appDatabaseSearchProvider) lookup(schema, table string, fields []string) (*fulltext.Table, []string, error) {
	var entry *fulltext.Table
	if file := provider.schemas[schema]; file != "" {
		entry = fulltext.Shared().Lookup(file, table)
	}
	if entry == nil {
		if strings.Contains(provider.database, "/") {
			return nil, nil, pgwire.NewError("42704", fmt.Sprintf(
				"table %s.%s has no search index: mark its columns .searchable() in the app's .shared/schema/ (kitwork/schema)", schema, table))
		}
		return nil, nil, pgwire.NewError("42704", fmt.Sprintf(
			"table %s.%s has no search index: declare its columns in the search of app.database (application.work)", schema, table))
	}
	if len(fields) == 0 {
		return entry, entry.ColumnNames(), nil
	}
	named := make([]string, len(fields))
	for position, field := range fields {
		for _, column := range entry.Columns() {
			if strings.EqualFold(column.Name, field) {
				named[position] = column.Name
			}
		}
		if named[position] == "" {
			return nil, nil, pgwire.NewError("42703", fmt.Sprintf("column %s of %s.%s is not in its search index (searchable: %s)",
				field, schema, table, strings.Join(entry.ColumnNames(), ", ")))
		}
	}
	return entry, named, nil
}

// appDatabaseSearchReading runs read on the index, and says in PostgreSQL's words why there is none yet.
func appDatabaseSearchReading(ctx context.Context, entry *fulltext.Table, read func(*search.Index) error) error {
	err := entry.Reading(ctx, read)
	var building *fulltext.BuildingError
	var protocol *pgwire.Error
	switch {
	case err == nil, errors.As(err, &protocol):
		return err
	case errors.As(err, &building):
		return pgwire.NewError("55000", building.Error())
	}
	return pgwire.NewError("58000", err.Error())
}

func (provider appDatabaseSearchProvider) Search(ctx context.Context, schema, table string, fields []string, query gateway.SearchQuery, limit int) ([]gateway.SearchHit, error) {
	entry, named, err := provider.lookup(schema, table, fields)
	if err != nil {
		return nil, err
	}
	var hits []gateway.SearchHit
	err = appDatabaseSearchReading(ctx, entry, func(index *search.Index) error {
		found, err := index.Search(ctx, appDatabaseMatchQuery(named, query),
			search.SearchOptions{Limit: min(max(limit, 1), search.MaximumSearchLimit)})
		if err != nil {
			return pgwire.NewError("42601", "MATCH: "+err.Error())
		}
		hits = make([]gateway.SearchHit, 0, len(found))
		for _, hit := range found {
			rowID, err := fulltext.RowID(hit)
			if err != nil {
				return fmt.Errorf("search index of %s holds a document %q that is not a rowid", entry.Label(), hit.ID)
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
	entry, named, err := provider.lookup(schema, table, fields)
	if err != nil {
		return 0, err
	}
	var count uint64
	err = appDatabaseSearchReading(ctx, entry, func(index *search.Index) error {
		count, err = index.Count(ctx, appDatabaseMatchQuery(named, query), nil)
		if err != nil {
			return pgwire.NewError("42601", "MATCH: "+err.Error())
		}
		return nil
	})
	return count, err
}

// Fields names the indexed columns a MATCH reads (every one when fields is empty), with their weights.
func (provider appDatabaseSearchProvider) Fields(_ context.Context, schema, table string, fields []string) ([]gateway.SearchField, error) {
	entry, named, err := provider.lookup(schema, table, fields)
	if err != nil {
		return nil, err
	}
	result := make([]gateway.SearchField, 0, len(named))
	for _, name := range named {
		for _, column := range entry.Columns() {
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
