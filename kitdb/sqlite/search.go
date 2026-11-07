package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"

	"github.com/kitwork/engine/search"
)

// SearchColumn is one text column of a searchable table.
type SearchColumn struct {
	Name string
	// Boost weighs a match in this column; 0 means 1.
	Boost float64
}

// SearchTable makes a table searchable through Snapshot.Search.
type SearchTable struct {
	Table   string
	Columns []SearchColumn
}

// SearchHit is one ranked row of a search.
type SearchHit struct {
	RowID int64
	Score float64
}

// searchState is what the container keeps with each table's index: the
// revision it reflects and the columns it was built from. A crash between the
// index's publication and nothing else cannot separate them; replaying
// changes from an older revision is safe anyway, a fold upserts and deletes
// by row.
type searchState struct {
	Revision uint64         `json:"revision"`
	Columns  []SearchColumn `json:"columns"`
}

// searchBuild bounds the segments of a first build: large ones, so a big
// table starts with few segments.
var searchBuild = search.BuildOptions{SkipLongTerms: true, FlushThresholdBytes: 192 << 20}

// searchable keeps one table's index exact at every revision: the table's
// index in the database's search container, plus search.Live for the commits
// after it. Readers pull: a search at revision R first applies the history
// rows the index has not seen up to R. A background fold moves applied
// changes into the container, never past the oldest open snapshot, so no
// reader loses its revision.
type searchable struct {
	database  *Database
	config    SearchTable
	schema    search.Schema
	threshold int

	mu   sync.Mutex
	live *search.Live

	folding sync.Mutex
	wake    chan struct{}
	stop    chan struct{}
	done    chan struct{}
}

func newSearchable(database *Database, config SearchTable, threshold int) (*searchable, error) {
	if config.Table == "" || len(config.Columns) == 0 {
		return nil, fmt.Errorf("kitdb: a searchable table needs a name and columns")
	}
	fields := make([]search.Field, 0, len(config.Columns))
	for _, column := range config.Columns {
		boost := column.Boost
		if boost == 0 {
			boost = 1
		}
		fields = append(fields, search.Text(column.Name, search.VietnameseAnalyzer(), search.Boost(boost)))
	}
	schema, err := search.NewSchema(fields...)
	if err != nil {
		return nil, fmt.Errorf("kitdb: search table %q: %w", config.Table, err)
	}
	s := &searchable{
		database: database, config: config, schema: schema, threshold: threshold,
		wake: make(chan struct{}, 1), stop: make(chan struct{}), done: make(chan struct{}),
	}
	go s.run()
	return s, nil
}

func (s *searchable) run() {
	defer close(s.done)
	for {
		select {
		case <-s.stop:
			return
		case <-s.wake:
			_ = s.fold(context.Background())
		}
	}
}

func (s *searchable) close() error {
	close(s.stop)
	<-s.done
	s.folding.Lock()
	defer s.folding.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.live == nil {
		return nil
	}
	err := s.live.Close()
	s.live = nil
	return err
}

// ensureLocked opens the table's index from the container, or builds it from
// snapshot, once. Every query runs inside the caller's snapshot: taking
// another reader connection here could wait forever on a pool full of searches.
func (s *searchable) ensureLocked(snapshot *Snapshot) error {
	if s.live != nil {
		return nil
	}
	index, encoded, err := s.database.container.Index(s.config.Table, s.schema)
	if err == nil {
		var state searchState
		if json.Unmarshal(encoded, &state) == nil && reflect.DeepEqual(state.Columns, s.config.Columns) &&
			state.Revision <= s.database.Revision() {
			if s.live, err = search.NewLive(index, state.Revision, search.LiveOptions{}); err == nil {
				return nil
			}
		}
		_ = index.Close()
	}
	return s.rebuildLocked(snapshot)
}

// rebuildLocked replaces the table's index with the table as snapshot holds it.
func (s *searchable) rebuildLocked(snapshot *Snapshot) error {
	ctx, revision := snapshot.ctx, snapshot.Revision
	state := searchState{Revision: revision, Columns: s.config.Columns}
	err := s.database.container.Replace(ctx, s.config.Table, s.schema, state, searchBuild, func(add func(search.Document) error) error {
		var exists int
		if err := snapshot.tx.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_schema WHERE type = 'table' AND name = ?`, s.config.Table).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			return nil
		}
		query := `SELECT _rowid_`
		for _, column := range s.config.Columns {
			query += `, ` + quoteIdentifier(column.Name)
		}
		rows, err := snapshot.tx.QueryContext(ctx, query+` FROM `+quoteIdentifier(s.config.Table))
		if err != nil {
			return err
		}
		defer rows.Close()
		values := make([]any, len(s.config.Columns)+1)
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
				return fmt.Errorf("kitdb: table %q returned a rowid of type %T", s.config.Table, values[0])
			}
			fields := make(map[string]string, len(s.config.Columns))
			for position, column := range s.config.Columns {
				fields[column.Name] = searchText(values[position+1])
			}
			if err := add(search.Document{ID: strconv.FormatInt(rowID, 10), Fields: fields}); err != nil {
				return err
			}
		}
		return rows.Err()
	})
	if err != nil {
		return fmt.Errorf("kitdb: build search index of %q: %w", s.config.Table, err)
	}
	index, _, err := s.database.container.Index(s.config.Table, s.schema)
	if err != nil {
		return err
	}
	live, err := search.NewLive(index, revision, search.LiveOptions{})
	if err != nil {
		_ = index.Close()
		return err
	}
	if s.live != nil {
		_ = s.live.Close()
	}
	s.live = live
	return nil
}

func searchText(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case []byte:
		return string(typed)
	default:
		return fmt.Sprint(typed)
	}
}

// searchChanges turns the commit's changes of this table into index changes.
// An update that leaves every searchable column as it was changes nothing.
// It reports false when a schema statement names the table: the index is then
// rebuilt rather than patched.
func (s *searchable) searchChanges(changes []Change) ([]search.Change, bool) {
	var result []search.Change
	for _, change := range changes {
		if change.Operation == Statement {
			if shape, err := classifyStatement(change.Statement); err == nil && namesTable(shape.words, s.config.Table) {
				return nil, false
			}
			continue
		}
		if change.Operation == Tracked {
			if strings.EqualFold(change.Table, s.config.Table) {
				return nil, false // its rows changed with no trace before: rebuilt from the rows
			}
			continue
		}
		if change.Table != s.config.Table {
			continue
		}
		identifier := strconv.FormatInt(change.RowID, 10)
		if change.Operation == Delete {
			result = append(result, search.Change{ID: identifier, Delete: true})
			continue
		}
		fields := make(map[string]string, len(s.config.Columns))
		if change.Operation == Insert {
			for _, column := range s.config.Columns {
				fields[column.Name] = ""
				if position := columnPosition(change.Columns, column.Name); position >= 0 {
					fields[column.Name] = searchText(change.After[position])
				}
			}
			result = append(result, search.Change{ID: identifier, Fields: fields})
			continue
		}
		// an update is its Updates, whatever its format: format 3 holds the columns it changed and, when a searchable
		// one changed, every searchable column kept whole (the writer keeps Options.Search's columns whole)
		updates := make(map[int]ColumnUpdate, len(change.Updates))
		for _, update := range change.Updates {
			updates[update.Position] = update
		}
		changed, whole := false, true
		for _, column := range s.config.Columns {
			position := columnPosition(change.Columns, column.Name)
			if position < 0 {
				fields[column.Name] = ""
				changed = true
				continue
			}
			update, held := updates[position]
			if !held {
				whole = false
				continue
			}
			fields[column.Name] = searchText(update.New)
			changed = changed || !sameValue(update.Old, update.New)
		}
		if !changed {
			continue // an update that leaves every searchable column as it was changes nothing
		}
		if !whole {
			return nil, false // a searchable column changed and another is not in the history: rebuilt from the rows
		}
		result = append(result, search.Change{ID: identifier, Fields: fields})
	}
	return result, true
}

func namesTable(words []string, table string) bool {
	for _, word := range words {
		if word == upperASCII(table) {
			return true
		}
	}
	return false
}

func upperASCII(value string) string {
	bytes := []byte(value)
	for position, character := range bytes {
		if character >= 'a' && character <= 'z' {
			bytes[position] = character - 'a' + 'A'
		}
	}
	return string(bytes)
}

// catchUpLocked applies every commit after the index's latest revision up to
// the snapshot's, reading them inside the snapshot.
func (s *searchable) catchUpLocked(snapshot *Snapshot) error {
	if err := s.ensureLocked(snapshot); err != nil {
		return err
	}
	ctx, revision := snapshot.ctx, snapshot.Revision
	latest := s.live.Revision()
	if latest >= revision {
		return nil
	}
	rebuild := false
	next := latest + 1
	err := readHistory(ctx, snapshot.tx, latest, 0, func(row historyRow) error {
		if row.revision > revision {
			return errStopHistory
		}
		// a revision missing after the index's is one Retain took out: what it changed is no longer known
		if row.revision != next {
			rebuild = true
			return errStopHistory
		}
		next++
		changes, err := decodeChanges(row.changes)
		if err != nil {
			return fmt.Errorf("revision %d: %w", row.revision, err)
		}
		converted, patchable := s.searchChanges(changes)
		if !patchable {
			rebuild = true
			return errStopHistory
		}
		if len(converted) == 0 {
			return nil
		}
		return s.live.Apply(ctx, row.revision, converted)
	})
	if err != nil && !errors.Is(err, errStopHistory) {
		return err
	}
	if rebuild {
		return s.rebuildLocked(snapshot)
	}
	if s.live.Revision() < revision {
		if err := s.live.Apply(ctx, revision, nil); err != nil {
			return err
		}
	}
	if s.live.Pending() >= s.threshold {
		select {
		case s.wake <- struct{}{}:
		default:
		}
	}
	return nil
}

var errStopHistory = errors.New("stop reading history")

// fold moves the changes up to the oldest open snapshot into the container,
// then rebases the live index onto the new generation. When the container
// holds more obsolete bytes than live ones, it is compacted.
func (s *searchable) fold(ctx context.Context) error {
	s.folding.Lock()
	defer s.folding.Unlock()
	s.mu.Lock()
	if s.live == nil {
		s.mu.Unlock()
		return nil
	}
	target := s.live.Revision()
	if oldest, open := s.database.oldestSnapshot(); open && oldest < target {
		target = oldest
	}
	if target <= s.live.BaseRevision() {
		s.mu.Unlock()
		return nil
	}
	changes, err := s.live.Changes(target)
	s.mu.Unlock()
	if err != nil {
		return err
	}
	container := s.database.container
	state := searchState{Revision: target, Columns: s.config.Columns}
	if err := container.Fold(ctx, s.config.Table, s.schema, state, search.BuildOptions{SkipLongTerms: true}, changes); err != nil {
		return err
	}
	index, _, err := container.Index(s.config.Table, s.schema)
	if err != nil {
		return err
	}
	s.mu.Lock()
	err = s.live.Rebase(index, target)
	s.mu.Unlock()
	if err != nil {
		_ = index.Close()
		return err
	}
	if container.NeedsCompaction() {
		return s.database.compactSearch(ctx)
	}
	return nil
}

// compactSearch rewrites the search container without its obsolete bytes. Every
// table's live index moves onto the compacted file first; the old file is
// replaced once its last view has closed.
func (database *Database) compactSearch(ctx context.Context) error {
	compaction, err := database.container.PrepareCompaction(ctx)
	if err != nil {
		return err
	}
	for _, s := range database.searchables {
		s.mu.Lock()
		if s.live != nil {
			index, _, err := compaction.Index(s.config.Table, s.schema)
			if err == nil {
				err = s.live.Rebase(index, s.live.BaseRevision())
				if err != nil {
					_ = index.Close()
				}
			}
			if err != nil {
				// This table's index stays on the old file; the old file then
				// cannot be replaced, so the compaction is discarded.
				s.mu.Unlock()
				compaction.Abort()
				return err
			}
		}
		s.mu.Unlock()
	}
	return compaction.Commit(ctx)
}

// Search ranks the rows of a searchable table as this snapshot sees them:
// every commit up to Revision and none after it.
func (snapshot *Snapshot) Search(table string, query search.MatchQuery, options search.SearchOptions) ([]SearchHit, error) {
	s := snapshot.database.searchables[table]
	if s == nil {
		return nil, fmt.Errorf("kitdb: table %q is not searchable", table)
	}
	s.mu.Lock()
	if err := s.catchUpLocked(snapshot); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	live := s.live
	s.mu.Unlock()
	// Outside the lock, so searches assemble their views in parallel: a fold
	// never rebases past an open snapshot, this one included.
	view, err := live.View(snapshot.ctx, snapshot.Revision)
	if err != nil {
		return nil, err
	}
	defer view.Close()
	hits, err := view.Search(snapshot.ctx, query, options)
	if err != nil {
		return nil, err
	}
	result := make([]SearchHit, len(hits))
	for position, hit := range hits {
		rowID, err := strconv.ParseInt(hit.ID, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("kitdb: search index of %q holds identifier %q", table, hit.ID)
		}
		result[position] = SearchHit{RowID: rowID, Score: hit.Score}
	}
	return result, nil
}

// columnPosition is the position of name among columns, -1 when it is not one.
func columnPosition(columns []string, name string) int {
	for position, column := range columns {
		if column == name {
			return position
		}
	}
	return -1
}
