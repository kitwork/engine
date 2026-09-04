package work

import (
	"bytes"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"

	kitdbengine "github.com/kitwork/engine/kitdb"
	"github.com/kitwork/engine/utilities/query"
	"github.com/kitwork/engine/value"
	_ "modernc.org/sqlite"
)

const kitDBSQLiteComparisonRows = 100_000

// BenchmarkKitDBSQLiteStorage compares equivalent warm, embedded storage
// operations. It intentionally excludes SQL parsing and Kitwork's ORM so the
// result describes the engines rather than two different API layers.
func BenchmarkKitDBSQLiteStorage(b *testing.B) {
	keys := make([][]byte, kitDBSQLiteComparisonRows)
	for index := range keys {
		keys[index] = []byte(fmt.Sprintf("product/%08d", index))
	}
	row := bytes.Repeat([]byte{'v'}, 128)

	kitDatabase := prepareKitDBComparisonDatabase(b, keys, row)
	defer kitDatabase.Close()
	sqliteDatabase := prepareSQLiteComparisonDatabase(b, keys, row)
	defer sqliteDatabase.Close()

	b.Run("point_lookup/kitdb", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(row)))
		b.ResetTimer()
		for index := 0; index < b.N; index++ {
			key := keys[(index*7919)%len(keys)]
			got, found, err := kitDatabase.Get(key)
			if err != nil || !found || len(got) != len(row) {
				b.Fatalf("KitDB Get = bytes:%d found:%t err:%v", len(got), found, err)
			}
		}
	})

	b.Run("point_lookup/sqlite", func(b *testing.B) {
		statement, err := sqliteDatabase.Prepare(`SELECT value FROM records WHERE key = ?`)
		if err != nil {
			b.Fatal(err)
		}
		defer statement.Close()
		b.ReportAllocs()
		b.SetBytes(int64(len(row)))
		b.ResetTimer()
		for index := 0; index < b.N; index++ {
			key := keys[(index*7919)%len(keys)]
			var got []byte
			if err := statement.QueryRow(key).Scan(&got); err != nil || len(got) != len(row) {
				b.Fatalf("SQLite Get = bytes:%d err:%v", len(got), err)
			}
		}
	})

	b.Run("range_100/kitdb", func(b *testing.B) {
		snapshot, err := kitDatabase.Snapshot()
		if err != nil {
			b.Fatal(err)
		}
		defer snapshot.Close()
		b.ReportAllocs()
		b.ResetTimer()
		for index := 0; index < b.N; index++ {
			start := keys[(index*7919)%(len(keys)-100)]
			cursor, err := snapshot.Cursor(kitdbengine.RangeOptions{Start: start, Limit: 100})
			if err != nil {
				b.Fatal(err)
			}
			count := 0
			for cursor.Next() {
				count++
			}
			cursorErr := cursor.Err()
			_ = cursor.Close()
			if cursorErr != nil || count != 100 {
				b.Fatalf("KitDB range count:%d err:%v", count, cursorErr)
			}
		}
	})

	b.Run("range_100/sqlite", func(b *testing.B) {
		statement, err := sqliteDatabase.Prepare(`SELECT key, value FROM records WHERE key >= ? ORDER BY key LIMIT 100`)
		if err != nil {
			b.Fatal(err)
		}
		defer statement.Close()
		b.ReportAllocs()
		b.ResetTimer()
		for index := 0; index < b.N; index++ {
			start := keys[(index*7919)%(len(keys)-100)]
			rows, err := statement.Query(start)
			if err != nil {
				b.Fatal(err)
			}
			count := 0
			for rows.Next() {
				var key, got []byte
				if err := rows.Scan(&key, &got); err != nil {
					_ = rows.Close()
					b.Fatal(err)
				}
				count++
			}
			rowsErr := rows.Err()
			_ = rows.Close()
			if rowsErr != nil || count != 100 {
				b.Fatalf("SQLite range count:%d err:%v", count, rowsErr)
			}
		}
	})

}

// BenchmarkKitDBPlannerV2Range isolates the physical consequence of the v2
// plan: a bounded secondary-index walk fetches only the requested rows, while
// the control path decodes the complete row namespace before filtering.
func BenchmarkKitDBPlannerV2Range(b *testing.B) {
	columns := map[string]*ColumnSpec{
		"id":     {kind: "text", primary: true, seq: 1},
		"status": {kind: "text", seq: 2, indexes: []colIndexRef{{name: "status_price"}}},
		"price":  {kind: "integer", seq: 3, indexes: []colIndexRef{{name: "status_price"}}},
	}
	definition := bindStructDef("products", nil, columns)
	database, err := kitdbengine.Open(filepath.Join(b.TempDir(), "planner-v2.kitdb"))
	if err != nil {
		b.Fatal(err)
	}
	defer database.Close()
	tx, err := database.Begin()
	if err != nil {
		b.Fatal(err)
	}
	for index := 0; index < kitDBSQLiteComparisonRows; index++ {
		row := map[string]value.Value{
			"id":     value.New(fmt.Sprintf("product/%08d", index)),
			"status": value.New("active"),
			"price":  value.New(index),
		}
		rowKey, err := kitDBRowKey(definition, row["id"])
		if err != nil {
			b.Fatal(err)
		}
		encoded, err := encodeKitDBValidatedRow(definition, row, nil)
		if err != nil {
			b.Fatal(err)
		}
		if err := tx.Put(rowKey, encoded); err != nil {
			b.Fatal(err)
		}
		entries, err := kitDBSecondaryIndexEntries(definition, row, rowKey)
		if err != nil {
			b.Fatal(err)
		}
		for _, entry := range entries {
			if err := tx.Put(entry.key, entry.value); err != nil {
				b.Fatal(err)
			}
		}
	}
	if _, err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
	if _, err := database.Checkpoint(); err != nil {
		b.Fatal(err)
	}
	snapshot, err := database.Snapshot()
	if err != nil {
		b.Fatal(err)
	}
	defer snapshot.Close()

	table := &SchemaTable{table: definition.Name, columns: definition.columns, definition: definition}
	plan := query.ExecutionPlan{
		Conditions: []query.Condition{
			{Column: "status", Operator: "=", Value: "active", Logic: "AND"},
			{Column: "price", Operator: ">=", Value: kitDBSQLiteComparisonRows - 100, Logic: "AND"},
		},
		Orders: []query.OrderQuery{{Column: "price", Direction: "asc"}},
		Limit:  20,
	}
	access, err := table.planKitDBAccess(plan)
	if err != nil || access.kind != kitDBAccessIndexRange || !access.orderCovered {
		b.Fatalf("range plan = %#v, err=%v", access, err)
	}
	rowPrefix, err := kitDBRowPrefix(definition)
	if err != nil {
		b.Fatal(err)
	}

	b.Run("secondary_index_limit_20", func(b *testing.B) {
		b.ReportAllocs()
		for iteration := 0; iteration < b.N; iteration++ {
			cursor, err := snapshot.Cursor(access.options)
			if err != nil {
				b.Fatal(err)
			}
			count := 0
			for cursor.Next() {
				encoded, found, err := snapshot.Get(cursor.Value())
				if err != nil || !found {
					_ = cursor.Close()
					b.Fatalf("index owner missing: found=%t err=%v", found, err)
				}
				if _, err := decodeKitDBRow(definition, encoded); err != nil {
					_ = cursor.Close()
					b.Fatal(err)
				}
				count++
				if count == 20 {
					break
				}
			}
			cursorErr := cursor.Err()
			_ = cursor.Close()
			if cursorErr != nil || count != 20 {
				b.Fatalf("index range count=%d err=%v", count, cursorErr)
			}
		}
	})

	b.Run("row_scan_filter_all", func(b *testing.B) {
		b.ReportAllocs()
		for iteration := 0; iteration < b.N; iteration++ {
			cursor, err := snapshot.Cursor(kitdbengine.RangeOptions{Prefix: rowPrefix})
			if err != nil {
				b.Fatal(err)
			}
			matches := 0
			for cursor.Next() {
				row, err := decodeKitDBRow(definition, cursor.Value())
				if err != nil {
					_ = cursor.Close()
					b.Fatal(err)
				}
				if row.values["price"].N >= kitDBSQLiteComparisonRows-100 {
					matches++
				}
			}
			cursorErr := cursor.Err()
			_ = cursor.Close()
			if cursorErr != nil || matches != 100 {
				b.Fatalf("row scan matches=%d err=%v", matches, cursorErr)
			}
		}
	})
}

func prepareKitDBComparisonDatabase(b *testing.B, keys [][]byte, row []byte) *kitdbengine.DB {
	b.Helper()
	database, err := kitdbengine.Open(filepath.Join(b.TempDir(), "comparison.kitdb"))
	if err != nil {
		b.Fatal(err)
	}
	transaction, err := database.Begin()
	if err != nil {
		b.Fatal(err)
	}
	for _, key := range keys {
		if err := transaction.Put(key, row); err != nil {
			b.Fatal(err)
		}
	}
	if _, err := transaction.Commit(); err != nil {
		b.Fatal(err)
	}
	if _, err := database.Checkpoint(); err != nil {
		b.Fatal(err)
	}
	return database
}

func prepareSQLiteComparisonDatabase(b *testing.B, keys [][]byte, row []byte) *sql.DB {
	b.Helper()
	database, err := sql.Open("sqlite", filepath.Join(b.TempDir(), "comparison.sqlite"))
	if err != nil {
		b.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	for _, statement := range []string{
		`PRAGMA journal_mode = WAL`,
		`PRAGMA synchronous = FULL`,
		`CREATE TABLE records (key BLOB PRIMARY KEY, value BLOB NOT NULL) WITHOUT ROWID`,
	} {
		if _, err := database.Exec(statement); err != nil {
			b.Fatal(err)
		}
	}
	transaction, err := database.Begin()
	if err != nil {
		b.Fatal(err)
	}
	statement, err := transaction.Prepare(`INSERT INTO records (key, value) VALUES (?, ?)`)
	if err != nil {
		b.Fatal(err)
	}
	for _, key := range keys {
		if _, err := statement.Exec(key, row); err != nil {
			b.Fatal(err)
		}
	}
	if err := statement.Close(); err != nil {
		b.Fatal(err)
	}
	if err := transaction.Commit(); err != nil {
		b.Fatal(err)
	}
	return database
}
