package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	kitdb "github.com/kitwork/engine/kitdb/sqlite"
)

// cmdAt measures at() going backward (docs/KITWORK_KITDB.md mục 5, "hướng lùi" of the Kitwork repository) against
// the restore it runs today. Backward, a read at a past revision r reads the live file in one read transaction and,
// for every row a commit after r changed, the row as it was at r: the Before of the first change after r, or no row
// when that change inserted it. The statement runs on the live rows the history after r did not touch, together
// with those rows (two temporary tables of the read connection). Today at() restores the file into a copy as it was
// at r (the anchor, then every commit up to r) and reads the copy.
//
// directory holds work.db, a copy of .database/shopping/kitwork.db, written to; the anchor Open writes and one
// restored copy at a time are written beside it. Evidence for KITWORK_KITDB.md mục 5 (04/11/2026).
func cmdAt(directory string) {
	ctx := context.Background()
	work := filepath.Join(directory, "work.db")
	if _, err := os.Stat(work + ".history"); err == nil {
		panic(work + ".history exists: at runs once on a fresh copy")
	}
	checkpointFile(work)
	_, _, rowCount := fileGeometry(work)
	start := time.Now()
	database, err := kitdb.Open(ctx, work, kitdb.Options{}) // a file without an anchor gets one as it opens
	must(err)
	defer database.Close()
	base := database.Revision()
	fmt.Printf("work.db: %d rows of shopping; Open wrote its anchor in %s\n", rowCount, duration(time.Since(start)))

	columns := make([]string, len(shoppingColumns))
	for position, column := range shoppingColumns {
		columns[position] = quoted(column.name)
	}
	list := strings.Join(columns, ", ")
	random := rand.New(rand.NewPCG(4, 12))
	hot := make([]int64, 10000)
	for position := range hot {
		hot[position] = random.Int64N(rowCount) + 1
	}
	update := `UPDATE shopping SET "stock" = "stock" - 1, "sold" = "sold" + 1 WHERE rowid = ?`
	insert := `INSERT INTO shopping (` + list + `) SELECT ` + strings.Replace(list, `"id"`, `?`, 1) + ` FROM shopping WHERE rowid = ?`
	var writes []queuedWrite
	for range 150000 {
		writes = append(writes, queuedWrite{update, []any{random.Int64N(rowCount) + 1}})
	}
	for range 100000 {
		writes = append(writes, queuedWrite{update, []any{hot[random.IntN(len(hot))]}})
	}
	inserted := int64(2_000_000_000_000)
	for range 30000 {
		inserted++
		writes = append(writes, queuedWrite{insert, []any{inserted, random.Int64N(rowCount) + 1}})
	}
	for range 20000 {
		writes = append(writes, queuedWrite{`DELETE FROM shopping WHERE rowid = ?`, []any{random.Int64N(rowCount) + 1}})
	}
	random.Shuffle(len(writes), func(i, j int) { writes[i], writes[j] = writes[j], writes[i] })
	start = time.Now()
	runEach(ctx, database, writes)
	fmt.Printf("%d writes in random order (150,000 random updates, 100,000 on 10,000 hot rows, 30,000 inserts, 20,000 deletes): %.0f writes/s\n",
		len(writes), float64(len(writes))/time.Since(start).Seconds())

	type commitCount struct {
		revision uint64
		changes  int
	}
	var commits []commitCount
	total := 0
	must(database.Changes(ctx, base, func(commit kitdb.Commit) error {
		commits = append(commits, commitCount{commit.Revision, len(commit.Changes)})
		total += len(commit.Changes)
		return nil
	}))
	last := database.Revision()
	fmt.Printf("the history after the anchor: %d commits, %d row changes, revision %d to %d\n\n", len(commits), total, base, last)

	reader := openSQLite(work, "&mode=ro&_pragma=temp_store(2)&_pragma=cache_size(-262144)")
	defer reader.Close()
	moment := func(revision uint64) time.Time {
		var at int64
		if err := reader.QueryRow(`SELECT at FROM kitwork_history WHERE rev = ?`, int64(revision)).Scan(&at); err == nil {
			return time.Unix(0, at)
		}
		// a revision with no commit of its own (0 on a new file): just before the commit after it
		must(reader.QueryRow(`SELECT at FROM kitwork_history WHERE rev = ?`, int64(revision)+1).Scan(&at))
		return time.Unix(0, at-1)
	}
	fmt.Printf("%-12s %8s %9s %9s %9s %9s %11s %11s %9s %11s  %s\n", "changes after", "rows", "history", "overlay",
		"point", "page", "page/arm", "count", "today", "restored", "same")
	for _, wanted := range []int{0, 1000, 10000, 100000, total} {
		revision, after := last, 0
		for position := len(commits) - 1; position >= 0 && after < wanted; position-- {
			after += commits[position].changes
			revision = commits[position].revision - 1
		}
		back := atBackward(ctx, reader, revision, columns, wanted == 0)
		results := back.results
		restoreTime, restoredTime, same := "", "", "—"
		if wanted != 0 {
			target := filepath.Join(directory, "at.db")
			removeDatabase(target)
			start := time.Now()
			restored, err := database.Restore(ctx, moment(revision), target)
			must(err)
			if restored != revision {
				panic(fmt.Sprintf("restore gave revision %d, want %d", restored, revision))
			}
			restoreTime = duration(time.Since(start))
			copyReader := openSQLite(target, "&mode=ro&_pragma=cache_size(-262144)")
			start = time.Now()
			plain := atQueries(copyReader, "shopping", "", back.keys)
			restoredTime = duration(time.Since(start))
			must(copyReader.Close())
			removeDatabase(target)
			same = "yes"
			for name, digest := range plain {
				if results[name] != digest {
					same = "NO: " + name
				}
			}
		}
		page := "—"
		if back.page != 0 {
			page = duration(back.page)
		}
		fmt.Printf("%-12d %8d %9s %9s %9s %9s %11s %11s %9s %11s  %s\n", after, back.rows, duration(back.history),
			duration(back.overlay), duration(back.point), page, duration(back.pageByArm), duration(back.count),
			restoreTime, restoredTime, same)
	}
	fmt.Println()
	fmt.Println("history: reading and decoding the commits after the moment; overlay: the two temporary tables;")
	fmt.Println("point: one read by key, the mean of the sampled keys; page: 50 shopee rows by id through the overlay as written (once);")
	fmt.Println("page/arm: the same with ORDER BY and LIMIT on each side; count: count and sums of lazada;")
	fmt.Println("today: at() as the engine runs it, the restore of the file; restored: the same reads on the restored copy.")
}

type queuedWrite struct {
	query     string
	arguments []any
}

// runEach sends each write as its own request from sixteen goroutines.
func runEach(ctx context.Context, database *kitdb.Database, writes []queuedWrite) {
	var next atomic.Int64
	var failure error
	var once sync.Once
	var wait sync.WaitGroup
	for range 16 {
		wait.Go(func() {
			for {
				position := int(next.Add(1)) - 1
				if position >= len(writes) {
					return
				}
				write := writes[position]
				_, err := database.Write(ctx, "measure", func(tx *kitdb.Transaction) error {
					_, err := tx.Exec(write.query, write.arguments...)
					return err
				})
				if err != nil {
					once.Do(func() { failure = err })
					return
				}
			}
		})
	}
	wait.Wait()
	must(failure)
}

type backwardRead struct {
	rows                                            int
	history, overlay, point, page, pageByArm, count time.Duration
	results                                         map[string]string
	keys                                            [][2]any
}

// atBackward reads shopping as it was at revision through the history after it, in one read transaction.
func atBackward(ctx context.Context, reader *sql.DB, revision uint64, columns []string, naive bool) backwardRead {
	var read backwardRead
	tx, err := reader.BeginTx(ctx, nil)
	must(err)
	defer tx.Rollback() // the temporary tables go with it

	start := time.Now()
	changed := map[int64]bool{}
	before := map[int64][]any{}
	var updated, deleted, inserted [][2]any
	key := func(names []string, values []any) [2]any {
		var merchant, id any
		for position, name := range names {
			switch name {
			case "merchant":
				merchant = values[position]
			case "id":
				id = values[position]
			}
		}
		return [2]any{merchant, id}
	}
	must(kitdb.ReadChanges(ctx, tx, revision, func(commit kitdb.Commit) error {
		for _, change := range commit.Changes {
			if change.Operation == kitdb.Statement || change.Operation == kitdb.Barrier {
				return fmt.Errorf("revision %d changed the schema: at() restores across it", commit.Revision)
			}
			if change.Table != "shopping" || changed[change.RowID] {
				continue // only the first change after the moment says what the row was at it
			}
			changed[change.RowID] = true
			switch change.Operation {
			case kitdb.Insert:
				if len(inserted) < 20 {
					inserted = append(inserted, key(change.Columns, change.After))
				}
			case kitdb.Update:
				before[change.RowID] = change.Before
				if len(updated) < 20 {
					updated = append(updated, key(change.Columns, change.Before))
				}
			case kitdb.Delete:
				before[change.RowID] = change.Before
				if len(deleted) < 20 {
					deleted = append(deleted, key(change.Columns, change.Before))
				}
			}
		}
		return nil
	}))
	read.history = time.Since(start)
	read.rows = len(changed)

	start = time.Now()
	_, err = tx.ExecContext(ctx, `CREATE TEMP TABLE at_changed (rid INTEGER PRIMARY KEY)`)
	must(err)
	_, err = tx.ExecContext(ctx, `CREATE TEMP TABLE at_before (rid INTEGER PRIMARY KEY, `+strings.Join(columns, ", ")+`)`)
	must(err)
	changedInsert, err := tx.PrepareContext(ctx, `INSERT INTO temp.at_changed (rid) VALUES (?)`)
	must(err)
	for rowID := range changed {
		_, err := changedInsert.ExecContext(ctx, rowID)
		must(err)
	}
	must(changedInsert.Close())
	marks := strings.TrimSuffix(strings.Repeat("?, ", len(columns)+1), ", ")
	beforeInsert, err := tx.PrepareContext(ctx, `INSERT INTO temp.at_before VALUES (`+marks+`)`)
	must(err)
	for rowID, values := range before {
		_, err := beforeInsert.ExecContext(ctx, append([]any{rowID}, values...)...)
		must(err)
	}
	must(beforeInsert.Close())
	_, err = tx.ExecContext(ctx, `CREATE INDEX temp.at_before_key ON at_before ("merchant", "id")`)
	must(err)
	read.overlay = time.Since(start)

	// The keys read by point: rows changed after the moment, deleted, inserted, and rows nobody touched.
	read.keys = append(append(append(read.keys, updated...), deleted...), inserted...)
	random := rand.New(rand.NewPCG(uint64(revision), 7))
	var rowCount int64
	must(tx.QueryRowContext(ctx, `SELECT max(rowid) FROM shopping`).Scan(&rowCount))
	for untouched := 0; untouched < 20; {
		rowID := random.Int64N(rowCount) + 1
		if changed[rowID] {
			continue
		}
		var merchant string
		var id int64
		if err := tx.QueryRowContext(ctx, `SELECT "merchant", "id" FROM shopping WHERE rowid = ?`, rowID).Scan(&merchant, &id); err != nil {
			continue
		}
		read.keys = append(read.keys, [2]any{merchant, id})
		untouched++
	}

	overlay := `WITH shopping_at AS (SELECT ` + strings.Join(columns, ", ") + ` FROM main.shopping WHERE _rowid_ NOT IN (SELECT rid FROM temp.at_changed)
		UNION ALL SELECT ` + strings.Join(columns, ", ") + ` FROM temp.at_before) `
	read.results = atQueries(tx, "shopping_at", overlay, read.keys)
	read.point, read.page, read.pageByArm, read.count = atTimings(tx, overlay, read.keys, naive)
	return read
}

// pageByArmQuery is the page read through the overlay as an engine would write it: ORDER BY and LIMIT on the live
// rows and on the rows as they were, then on both.
const pageByArmQuery = `SELECT * FROM (SELECT * FROM (SELECT "merchant", "id", "name", "price", "stock" FROM main.shopping
	WHERE "merchant" = 'shopee' AND _rowid_ NOT IN (SELECT rid FROM temp.at_changed) ORDER BY "id" LIMIT 50)
	UNION ALL SELECT * FROM (SELECT "merchant", "id", "name", "price", "stock" FROM temp.at_before
	WHERE "merchant" = 'shopee' ORDER BY "id" LIMIT 50)) ORDER BY "id" LIMIT 50`

type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// atQueries runs the reads on table (prefixed by with) and returns the digest of each one's rows.
func atQueries(db querier, table, with string, keys [][2]any) map[string]string {
	results := map[string]string{}
	point := sha256.New()
	for _, key := range keys {
		fmt.Fprint(point, queryRows(db, with+`SELECT * FROM `+table+` WHERE "merchant" = ? AND "id" = ?`, key[0], key[1]))
	}
	results["point"] = hex.EncodeToString(point.Sum(nil))
	if with != "" {
		results["page"] = digestOf(queryRows(db, pageByArmQuery))
	} else {
		results["page"] = digestOf(queryRows(db, `SELECT "merchant", "id", "name", "price", "stock" FROM `+table+
			` WHERE "merchant" = 'shopee' ORDER BY "id" LIMIT 50`))
	}
	results["count"] = digestOf(queryRows(db, with+`SELECT count(*), sum("stock"), sum("sold") FROM `+table+` WHERE "merchant" = 'lazada'`))
	return results
}

// atTimings times each read through the overlay: point as the mean over keys, page as written and with ORDER BY and
// LIMIT on each side, as an engine writing the statement would.
func atTimings(db querier, overlay string, keys [][2]any, naive bool) (point, page, pageByArm, count time.Duration) {
	start := time.Now()
	for _, key := range keys {
		queryRows(db, overlay+`SELECT * FROM shopping_at WHERE "merchant" = ? AND "id" = ?`, key[0], key[1])
	}
	point = time.Since(start) / time.Duration(max(len(keys), 1))
	// Windows' clock moves in steps of about half a millisecond: a fast read is timed over many runs.
	start = time.Now()
	byArm := queryRows(db, pageByArmQuery)
	for range 19 {
		queryRows(db, pageByArmQuery)
	}
	pageByArm = time.Since(start) / 20
	if naive { // through the overlay as written: SQLite reads and sorts every shopee row
		start = time.Now()
		paged := queryRows(db, overlay+`SELECT "merchant", "id", "name", "price", "stock" FROM shopping_at WHERE "merchant" = 'shopee' ORDER BY "id" LIMIT 50`)
		page = time.Since(start)
		if digestOf(paged) != digestOf(byArm) {
			panic("the page read on each side differs from the page through the overlay")
		}
	}
	start = time.Now()
	for range 5 {
		queryRows(db, overlay+`SELECT count(*), sum("stock"), sum("sold") FROM shopping_at WHERE "merchant" = 'lazada'`)
	}
	count = time.Since(start) / 5
	return point, page, pageByArm, count
}

func queryRows(db querier, query string, arguments ...any) string {
	rows, err := db.QueryContext(context.Background(), query, arguments...)
	must(err)
	defer rows.Close()
	columns, err := rows.Columns()
	must(err)
	var text strings.Builder
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for position := range values {
			pointers[position] = &values[position]
		}
		must(rows.Scan(pointers...))
		for _, item := range values {
			if bytes, ok := item.([]byte); ok {
				item = string(bytes)
			}
			fmt.Fprintf(&text, "%T:%v|", item, item)
		}
		text.WriteString("\n")
	}
	must(rows.Err())
	return text.String()
}

func digestOf(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}
