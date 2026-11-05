package analytics

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/kitwork/engine/kitdb/columnar"
	kitdb "github.com/kitwork/engine/kitdb/sqlite"
)

// ErrStale means the copy cannot be brought to the snapshot a query reads: the schema moved since it was built, the
// file has no history to say what changed, the history no longer reaches back to the copy's revision, or rows of the
// table changed outside the history. The query reads the rows instead, and the copy is built again.
var ErrStale = errors.New("analytics: the copy is stale")

// ErrBehind means the snapshot a query reads is older than the copy (a read transaction begun before a fold or a
// build): the query reads the rows instead; the copy is right for the snapshots after.
var ErrBehind = errors.New("analytics: the snapshot is older than the copy")

// keepVersions is how many revisions of the changed rows a Table keeps for the readers of older snapshots.
const keepVersions = 8

// Table is one table's copy as queries read it: the main part, and the rows changed since by revision.
type Table struct {
	copy     *Copy
	mutex    sync.Mutex
	versions []*changed // ascending revision, the first the main part's own
}

// changed is the rows of the table changed after the main part's revision, as they are at revision: each one's
// values of every column of the copy, nil for a row deleted since (or inserted and deleted).
type changed struct {
	revision uint64
	images   map[int64][]any
	rowIDs   []int64 // the keys of images, ascending
}

// newTable is the table read from copy, no row changed yet.
func newTable(copy *Copy) *Table {
	return &Table{copy: copy, versions: []*changed{{revision: copy.Manifest.Revision, images: map[int64][]any{}}}}
}

// carry keeps the rows changed that previous, the same main part opened from an earlier generation, read already.
func (t *Table) carry(previous *Table) {
	if previous == nil || previous.copy.Manifest.Section != t.copy.Manifest.Section || previous.copy.Manifest.Revision != t.copy.Manifest.Revision {
		return
	}
	previous.mutex.Lock()
	versions := slices.Clone(previous.versions)
	previous.mutex.Unlock()
	t.mutex.Lock()
	t.versions = versions
	t.mutex.Unlock()
}

// Manifest is the main part's manifest.
func (t *Table) Manifest() *Manifest { return t.copy.Manifest }

// Copy is the main part, as it was built or last folded.
func (t *Table) Copy() *Copy { return t.copy }

// Close lets the main part go.
func (t *Table) Close() error { return t.copy.Close() }

// View is the table as one read transaction sees it.
type View struct {
	table    *Table
	changed  *changed
	Revision uint64
}

// View brings the copy to the snapshot source reads of the file it attaches as schema ("main": its own), inside that
// read transaction: the commits after the newest
// revision the table holds rows for at or before the snapshot's are read from the history, and the rows they touched
// are read as the snapshot holds them. A snapshot the copy cannot reach is ErrStale.
func (t *Table) View(ctx context.Context, source Querier, schema string) (*View, error) {
	manifest := t.copy.Manifest
	revision, version, hasHistory, err := snapshotState(ctx, source, schema)
	if err != nil {
		return nil, err
	}
	switch {
	case version != manifest.Schema:
		return nil, fmt.Errorf("%w: the schema moved (version %d, the copy's %d)", ErrStale, version, manifest.Schema)
	case !hasHistory:
		return nil, fmt.Errorf("%w: the file has no history", ErrStale)
	case revision < manifest.Revision:
		return nil, fmt.Errorf("%w: the snapshot is at revision %d, the copy at %d", ErrBehind, revision, manifest.Revision)
	}
	t.mutex.Lock()
	base := t.versions[0]
	for _, version := range t.versions {
		if version.revision <= revision {
			base = version
		}
	}
	t.mutex.Unlock()
	if base.revision == revision {
		return &View{table: t, changed: base, Revision: revision}, nil
	}
	next, err := t.advance(ctx, source, schema, base, revision)
	if err != nil {
		return nil, err
	}
	t.mutex.Lock()
	position, found := slices.BinarySearchFunc(t.versions, revision, func(version *changed, revision uint64) int {
		return compare(version.revision, revision)
	})
	if found {
		next = t.versions[position] // another reader brought it to the same revision first: the same rows
	} else {
		t.versions = slices.Insert(t.versions, position, next)
		if len(t.versions) > keepVersions {
			t.versions = append(t.versions[:1], t.versions[2:]...) // the main part's own stays
		}
	}
	t.mutex.Unlock()
	return &View{table: t, changed: next, Revision: revision}, nil
}

func compare(a, b uint64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// advance is base brought to revision: the rows the commits after base.revision touched, read as the snapshot holds
// them, over base's.
func (t *Table) advance(ctx context.Context, source Querier, schema string, base *changed, revision uint64) (*changed, error) {
	table := t.copy.Manifest.Table
	touched := map[int64]bool{}
	expected := base.revision + 1
	err := kitdb.ReadChangesIn(ctx, source, schema, base.revision, func(commit kitdb.Commit) error {
		if commit.Revision != expected {
			return fmt.Errorf("%w: the history goes from revision %d to %d", ErrStale, expected-1, commit.Revision)
		}
		expected++
		for _, change := range commit.Changes {
			switch {
			case change.Operation == kitdb.Statement:
				return fmt.Errorf("%w: revision %d changed the schema", ErrStale, commit.Revision)
			case change.Operation == kitdb.Barrier && strings.HasSuffix(change.Statement, " "+table):
				return fmt.Errorf("%w: revision %d changed rows of %s outside the history", ErrStale, commit.Revision, table)
			case change.Operation == kitdb.Barrier:
				// another table's rows, or the schema, which the schema_version says
			case change.Table == table:
				touched[change.RowID] = true
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if expected-1 < revision {
		return nil, fmt.Errorf("%w: the history ends at revision %d, the snapshot is at %d", ErrStale, expected-1, revision)
	}
	next := &changed{revision: revision, images: maps.Clone(base.images)}
	if len(touched) > 0 {
		rowIDs := slices.Sorted(maps.Keys(touched))
		images, err := t.readRows(ctx, source, schema, rowIDs)
		if err != nil {
			return nil, err
		}
		for _, rowID := range rowIDs {
			next.images[rowID] = images[rowID] // nil: not in the snapshot
		}
	}
	next.rowIDs = slices.Sorted(maps.Keys(next.images))
	return next, nil
}

// readRows reads the copy's columns of rowIDs as source holds them; a rowid not there is absent from the map.
func (t *Table) readRows(ctx context.Context, source Querier, schema string, rowIDs []int64) (map[int64][]any, error) {
	manifest := t.copy.Manifest
	names := make([]string, len(manifest.Columns))
	for position, column := range manifest.Columns {
		names[position] = quoteIdentifier(column.Name)
	}
	images := make(map[int64][]any, len(rowIDs))
	for start := 0; start < len(rowIDs); start += 500 {
		part := rowIDs[start:min(start+500, len(rowIDs))]
		arguments := make([]any, len(part))
		for position, rowID := range part {
			arguments[position] = rowID
		}
		marks := strings.TrimSuffix(strings.Repeat("?, ", len(part)), ", ")
		rows, err := source.QueryContext(ctx, `SELECT _rowid_, `+strings.Join(names, ", ")+` FROM `+quoteIdentifier(schema)+"."+quoteIdentifier(manifest.Table)+
			` WHERE _rowid_ IN (`+marks+`)`, arguments...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			values := make([]any, len(names)+1)
			pointers := make([]any, len(values))
			for position := range values {
				pointers[position] = &values[position]
			}
			if err := rows.Scan(pointers...); err != nil {
				rows.Close()
				return nil, err
			}
			rowID, _ := values[0].(int64)
			images[rowID] = values[1:]
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return images, nil
}

// Changed is how many rows the view reads from its changed rows rather than from the main part.
func (v *View) Changed() int { return len(v.changed.rowIDs) }

// Scan visits the table as the view's snapshot holds it, the rowid (column 0 of the batch) and the named columns
// after it: the blocks of the main part, a row changed since skipped (skip[row] true), then the changed rows that are
// there, in batches of their own (skip nil). decide is given each block of the main part first, and whether it holds
// a changed row (touched); such a block is scanned even when decide answers UseBlockStatistics, since its statistics
// count that row as it was. visit must not keep the batch or skip.
func (v *View) Scan(ctx context.Context, columns []string, decide func(statistics *columnar.BlockStatistics, touched bool) (columnar.BlockAction, error),
	visit func(batch *columnar.Batch, skip []bool) error) (columnar.ScanReport, error) {
	positions := make([]int, len(columns))
	for index, name := range columns {
		positions[index] = v.table.copy.Manifest.position(name)
	}
	rowIDs := v.changed.rowIDs
	touches := func(low, high int64) bool {
		at, _ := slices.BinarySearch(rowIDs, low)
		return at < len(rowIDs) && rowIDs[at] <= high
	}
	blockDecide := func(statistics *columnar.BlockStatistics) (columnar.BlockAction, error) {
		rowID := statistics.Columns[0]
		touched := len(rowIDs) > 0 && touches(rowID.IntegerMin, rowID.IntegerMax)
		action := columnar.ScanBlock
		if decide != nil {
			var err error
			if action, err = decide(statistics, touched); err != nil {
				return action, err
			}
		}
		if action == columnar.UseBlockStatistics && touched {
			action = columnar.ScanBlock
		}
		return action, nil
	}
	skip := make([]bool, columnar.BatchRows)
	report, err := v.table.copy.Scan(ctx, columns, blockDecide, func(batch *columnar.Batch) error {
		clear(skip)
		if len(rowIDs) > 0 {
			ids := batch.Columns[0].Integers[:batch.Rows]
			if touches(ids[0], ids[batch.Rows-1]) {
				at, _ := slices.BinarySearch(rowIDs, ids[0])
				for row, rowID := range ids {
					for at < len(rowIDs) && rowIDs[at] < rowID {
						at++
					}
					skip[row] = at < len(rowIDs) && rowIDs[at] == rowID
				}
			}
		}
		return visit(batch, skip[:batch.Rows])
	})
	if err != nil || len(rowIDs) == 0 {
		return report, err
	}
	fields, err := v.table.copy.fieldsOf(columns)
	if err != nil {
		return report, err
	}
	batch, err := columnar.NewBatch(fields)
	if err != nil {
		return report, err
	}
	flush := func() error {
		if batch.Rows == 0 {
			return nil
		}
		report.Rows += uint64(batch.Rows)
		err := visit(batch, nil)
		batch.Reset()
		return err
	}
	for _, rowID := range rowIDs {
		image := v.changed.images[rowID]
		if image == nil {
			continue
		}
		row := batch.Rows
		batch.Columns[0].Integers[row], batch.Columns[0].Valid[row] = rowID, 1
		for index, position := range positions {
			if err := setCell(&batch.Columns[index+1], row, image[position]); err != nil {
				return report, fmt.Errorf("%w: %s.%s, rowid %d: %v", ErrStale, v.table.copy.Manifest.Table, columns[index], rowID, err)
			}
		}
		batch.Rows++
		if batch.Rows == columnar.BatchRows {
			if err := flush(); err != nil {
				return report, err
			}
		}
	}
	return report, flush()
}
