package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand/v2"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestFormatThreeMatchesTheRowsAtEveryRevision runs a random workload through the writer, which writes format 3, and
// keeps the table as it is after every commit. Then, at every revision, what each reader rebuilds from the history is
// the table as it was: history()'s images before and after every change (WholeImages), the rows going backward from
// the live table (the first change after the revision), and the restore of the file (every thirtieth revision). The
// searchable columns of Options.Search fold into the index, which every fiftieth request is searched against an index
// rebuilt from the rows. The workload inserts, updates some columns (to NULL, to the value they had, two statements on
// one row in one request), deletes, swaps a UNIQUE code between two rows in one request, and moves a row to a new
// rowid.
//
// CONTROL (04/11/2026): with WholeImages leaving an update's old values out, "revision 6, change of row 3: before
// [3 c82 …] after [3 c82 …], want [3 c3 …]"; with replay reading each updated row only when its change runs, the swap
// of codes fails, "restore of revision 31: replay revision 17: … update of row 4 of \"products\", which the restored
// copy does not hold as the history says".
func TestFormatThreeMatchesTheRowsAtEveryRevision(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "kitwork.db")
	database := openSearchDatabase(t, path, 8)
	t.Cleanup(func() { _ = database.Close() })
	if _, err := database.Migrate(ctx, "boot", `CREATE TABLE products (id INTEGER PRIMARY KEY, code TEXT UNIQUE,
		name TEXT, description TEXT, price INTEGER, note TEXT)`); err != nil {
		t.Fatal(err)
	}
	snapshots := map[uint64]map[int64][]any{}
	take := func() map[int64][]any {
		t.Helper()
		rows := map[int64][]any{}
		if err := database.Read(ctx, func(snapshot *Snapshot) error {
			result, err := snapshot.Query(`SELECT _rowid_, * FROM products`)
			if err != nil {
				return err
			}
			defer result.Close()
			names, _ := result.Columns()
			for result.Next() {
				values := make([]any, len(names))
				pointers := make([]any, len(names))
				for position := range values {
					pointers[position] = &values[position]
				}
				if err := result.Scan(pointers...); err != nil {
					return err
				}
				rows[values[0].(int64)] = values[1:]
			}
			return result.Err()
		}); err != nil {
			t.Fatal(err)
		}
		return rows
	}
	snapshots[database.Revision()] = take()

	random := rand.New(rand.NewPCG(3, 4))
	nextID := int64(1)
	type statement struct {
		query     string
		arguments []any
	}
	for request := range 400 {
		current := snapshots[database.Revision()]
		ids := make([]int64, 0, len(current))
		for id := range current {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		pick := func() int64 { return ids[random.IntN(len(ids))] }
		var statements []statement
		insert := func() {
			statements = append(statements, statement{`INSERT INTO products (id, code, name, description, price, note) VALUES (?, ?, ?, ?, ?, ?)`,
				[]any{nextID, fmt.Sprintf("c%d", nextID), searchSentence(random, 1, 4), searchSentence(random, 3, 12),
					random.Int64N(100000), nil}})
			nextID++
		}
		switch kind := random.IntN(10); {
		case len(ids) < 3 || kind < 3:
			insert()
		case kind < 6: // some columns, to NULL, to the value they had, or new
			id := pick()
			var sets []string
			var arguments []any
			for _, column := range []string{"name", "description", "price", "note"} {
				switch random.IntN(4) {
				case 0:
					sets = append(sets, column+" = NULL")
				case 1:
					sets = append(sets, column+" = "+column)
				case 2:
					sets = append(sets, column+" = ?")
					switch column {
					case "price":
						arguments = append(arguments, random.Int64N(100000))
					default:
						arguments = append(arguments, searchSentence(random, 1, 6))
					}
				}
			}
			if len(sets) == 0 {
				sets = append(sets, "price = price + 1")
			}
			statements = append(statements, statement{`UPDATE products SET ` + strings.Join(sets, ", ") + ` WHERE id = ?`, append(arguments, id)})
		case kind == 6: // two statements on one row, one request
			id := pick()
			statements = append(statements,
				statement{`UPDATE products SET name = ? WHERE id = ?`, []any{searchSentence(random, 1, 3), id}},
				statement{`UPDATE products SET price = price * 2, note = ? WHERE id = ?`, []any{"hai lần", id}})
		case kind == 7:
			statements = append(statements, statement{`DELETE FROM products WHERE id = ?`, []any{pick()}})
		case kind == 8: // swap the UNIQUE codes of two rows, one request
			first, second := pick(), pick()
			if first == second {
				insert()
				break
			}
			firstCode, secondCode := current[first][1], current[second][1]
			statements = append(statements,
				statement{`UPDATE products SET code = 'swapping' WHERE id = ?`, []any{first}},
				statement{`UPDATE products SET code = ? WHERE id = ?`, []any{firstCode, second}},
				statement{`UPDATE products SET code = ? WHERE id = ?`, []any{secondCode, first}})
		default: // a new rowid
			statements = append(statements, statement{`UPDATE products SET id = ? WHERE id = ?`, []any{nextID, pick()}})
			nextID++
		}
		if _, err := database.Write(ctx, "t", func(tx *Transaction) error {
			for _, statement := range statements {
				if _, err := tx.Exec(statement.query, statement.arguments...); err != nil {
					return fmt.Errorf("%s: %w", statement.query, err)
				}
			}
			return nil
		}); err != nil {
			t.Fatalf("request %d: %v", request, err)
		}
		snapshots[database.Revision()] = take()
		if request%50 == 49 {
			if err := database.Read(ctx, func(snapshot *Snapshot) error {
				compareSearch(t, fmt.Sprintf("request %d", request), snapshot)
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		}
	}

	// the history, rebuilt as its readers rebuild it
	var changes []Change
	var revisions []uint64
	moments := map[uint64]time.Time{}
	if err := database.Changes(ctx, 0, func(commit Commit) error {
		moments[commit.Revision] = commit.At
		for _, change := range commit.Changes {
			if change.Table == "products" && change.Operation != Statement {
				changes = append(changes, change)
				revisions = append(revisions, commit.Revision)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := WholeImages(changes, "products", liveFrom(database.readers)); err != nil {
		t.Fatal(err)
	}
	for index, change := range changes {
		revision := revisions[index]
		before, after := snapshots[revision-1][change.RowID], snapshots[revision][change.RowID]
		if change.Operation == Insert {
			before = nil
		}
		if change.Operation == Delete {
			after = nil
		}
		if !reflect.DeepEqual(nilIfEmpty(change.Before), before) || !reflect.DeepEqual(nilIfEmpty(change.After), after) {
			t.Fatalf("revision %d, change of row %d: before %v after %v, want %v and %v", revision, change.RowID,
				change.Before, change.After, before, after)
		}
	}
	// going backward: at every revision, the live rows the history after it did not touch, and the first change's before
	last := database.Revision()
	for revision := range last {
		if _, taken := snapshots[revision]; !taken {
			continue // before the table was made
		}
		rows := map[int64][]any{}
		for id, row := range snapshots[last] {
			rows[id] = row
		}
		seen := map[int64]bool{}
		for index, change := range changes {
			if revisions[index] <= revision || seen[change.RowID] {
				continue
			}
			seen[change.RowID] = true
			delete(rows, change.RowID)
			if change.Operation != Insert {
				rows[change.RowID] = change.Before
			}
		}
		if !reflect.DeepEqual(rows, snapshots[revision]) {
			t.Fatalf("going backward to revision %d: %d rows, want %d as they were", revision, len(rows), len(snapshots[revision]))
		}
	}
	// the restore of the file
	for revision := uint64(1); revision <= last; revision += 30 {
		target := filepath.Join(t.TempDir(), fmt.Sprintf("restored-%d.db", revision))
		if _, err := database.Restore(ctx, moments[revision], target); err != nil {
			t.Fatalf("restore of revision %d: %v", revision, err)
		}
		restored, err := sql.Open("sqlite", "file:"+filepath.ToSlash(target)+"?mode=ro")
		if err != nil {
			t.Fatal(err)
		}
		rows := map[int64][]any{}
		result, err := restored.Query(`SELECT _rowid_, * FROM products`)
		if err != nil {
			t.Fatal(err)
		}
		names, _ := result.Columns()
		for result.Next() {
			values := make([]any, len(names))
			pointers := make([]any, len(names))
			for position := range values {
				pointers[position] = &values[position]
			}
			if err := result.Scan(pointers...); err != nil {
				t.Fatal(err)
			}
			rows[values[0].(int64)] = values[1:]
		}
		result.Close()
		restored.Close()
		if !reflect.DeepEqual(rows, snapshots[revision]) {
			t.Fatalf("restore of revision %d: %d rows, want %d as they were", revision, len(rows), len(snapshots[revision]))
		}
	}
	if err := database.Read(ctx, func(snapshot *Snapshot) error {
		compareSearch(t, "the end", snapshot)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	t.Logf("%d requests, %d revisions, %d changes of products", 400, last, len(changes))
}

func nilIfEmpty(values []any) []any {
	if len(values) == 0 {
		return nil
	}
	return values
}

// liveFrom reads rows as they are now from db, by rowid.
func liveFrom(db *sql.DB) LiveRows {
	return func(table string, rowIDs []int64) ([]string, map[int64][]any, error) {
		found := map[int64][]any{}
		var columns []string
		for _, rowID := range rowIDs {
			rows, err := db.Query(`SELECT _rowid_, * FROM `+quoteIdentifier(table)+` WHERE _rowid_ = ?`, rowID)
			if err != nil {
				return nil, nil, err
			}
			names, _ := rows.Columns()
			columns = names[1:]
			for rows.Next() {
				values := make([]any, len(names))
				pointers := make([]any, len(names))
				for position := range values {
					pointers[position] = &values[position]
				}
				if err := rows.Scan(pointers...); err != nil {
					rows.Close()
					return nil, nil, err
				}
				found[rowID] = values[1:]
			}
			rows.Close()
		}
		return columns, found, nil
	}
}
