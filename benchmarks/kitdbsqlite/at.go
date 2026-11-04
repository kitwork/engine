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
// the restore it runs today. Backward, a read at a past revision r reads, in one read transaction of the live file, the
// relation kitdb.PastRelation builds, as the engine's at() does: the live rows the history after r did not touch, the
// rows deleted since whole from the history, and the rows still there joined to the old values of the columns changed
// since. Today at() restores the file into a copy as it was at r (the anchor, then every commit up to r) and reads the
// copy; it still does for a moment it cannot reach going backward.
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
	writes := atWorkload(columns, rowCount)
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
	var historyBytes int64
	must(database.Read(ctx, func(snapshot *kitdb.Snapshot) error {
		rows, err := snapshot.Query(`SELECT coalesce(sum(length(changes)), 0) FROM kitwork_history WHERE rev > ?`, int64(base))
		if err != nil {
			return err
		}
		defer rows.Close()
		rows.Next()
		return rows.Scan(&historyBytes)
	}))
	fmt.Printf("the history after the anchor: %d commits, %d row changes, %d bytes (%.0f a write), revision %d to %d\n\n",
		len(commits), total, historyBytes, float64(historyBytes)/float64(len(writes)), base, last)

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
	fmt.Printf("%-12s %8s %10s %9s %9s %11s %9s %11s  %s\n", "changes after", "rows", "setup", "point", "page", "count",
		"today", "restored", "same")
	for _, wanted := range []int{0, 1000, 10000, 100000, total} {
		revision, after := last, 0
		for position := len(commits) - 1; position >= 0 && after < wanted; position-- {
			after += commits[position].changes
			revision = commits[position].revision - 1
		}
		back := atBackward(ctx, reader, revision)
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
			plain := atQueries(copyReader, "shopping", back.keys)
			restoredTime = duration(time.Since(start))
			must(copyReader.Close())
			removeDatabase(target)
			same = "yes"
			for name, digest := range plain {
				if back.results[name] != digest {
					same = "NO: " + name
				}
			}
		}
		fmt.Printf("%-12d %8d %10s %9s %9s %11s %9s %11s  %s\n", after, back.rows, duration(back.setup), duration(back.point),
			duration(back.page), duration(back.count), restoreTime, restoredTime, same)
	}
	fmt.Println()
	fmt.Println("setup: kitdb.PastRelation, the history after the moment read and the temporary tables and view built;")
	fmt.Println("point: one read by key through the view, the mean of the sampled keys; page: 50 shopee rows by id;")
	fmt.Println("count: count and sums of lazada; today: the restore of the file; restored: the same reads on the copy.")
}

// atWorkload is the writes of at and changeset, the same every run: 150,000 updates of random rows, 100,000 on 10,000
// hot rows, 30,000 inserts copying a random row under a new id, 20,000 deletes of random rows, in random order.
func atWorkload(columns []string, rowCount int64) []queuedWrite {
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
	return writes
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
	rows                      int
	setup, point, page, count time.Duration
	results                   map[string]string
	keys                      [][2]any
}

// atBackward reads shopping as it was at revision through the relation the engine's at() reads, kitdb.PastRelation,
// in one read transaction.
func atBackward(ctx context.Context, reader *sql.DB, revision uint64) backwardRead {
	var read backwardRead
	tx, err := reader.BeginTx(ctx, nil)
	must(err)
	defer tx.Rollback() // the temporary tables and view go with it
	start := time.Now()
	relation, err := kitdb.PastRelation(ctx, tx, "shopping", revision, 100_000_000)
	must(err)
	read.setup = time.Since(start)

	// the keys read by point: rows changed after the moment (updated, deleted, inserted) and rows nobody touched
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
	changed := map[int64]bool{}
	var updated []int64
	var deleted, inserted [][2]any
	must(kitdb.ReadChanges(ctx, tx, revision, func(commit kitdb.Commit) error {
		for _, change := range commit.Changes {
			if change.Table != "shopping" || changed[change.RowID] {
				continue
			}
			changed[change.RowID] = true
			switch change.Operation {
			case kitdb.Insert:
				if len(inserted) < 20 {
					inserted = append(inserted, key(change.Columns, change.After))
				}
			case kitdb.Delete:
				if len(deleted) < 20 {
					deleted = append(deleted, key(change.Columns, change.Before))
				}
			case kitdb.Update:
				if len(updated) < 20 {
					updated = append(updated, change.RowID)
				}
			}
		}
		return nil
	}))
	read.rows = len(changed)
	// an update keeps only the columns it changed; the workload never changes a key, so the table holds it
	for _, rowID := range updated {
		var merchant string
		var id int64
		if tx.QueryRowContext(ctx, `SELECT "merchant", "id" FROM main.shopping WHERE rowid = ?`, rowID).Scan(&merchant, &id) == nil {
			read.keys = append(read.keys, [2]any{merchant, id})
		}
	}
	read.keys = append(append(read.keys, deleted...), inserted...)
	random := rand.New(rand.NewPCG(uint64(revision), 7))
	var rowCount int64
	must(tx.QueryRowContext(ctx, `SELECT max(rowid) FROM main.shopping`).Scan(&rowCount))
	for untouched := 0; untouched < 20; {
		rowID := random.Int64N(rowCount) + 1
		if changed[rowID] {
			continue
		}
		var merchant string
		var id int64
		if tx.QueryRowContext(ctx, `SELECT "merchant", "id" FROM main.shopping WHERE rowid = ?`, rowID).Scan(&merchant, &id) != nil {
			continue
		}
		read.keys = append(read.keys, [2]any{merchant, id})
		untouched++
	}
	read.results = atQueries(tx, relation, read.keys)
	read.point, read.page, read.count = atTimings(tx, relation, read.keys)
	return read
}

type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// atQueries runs the reads on table and returns the digest of each one's rows.
func atQueries(db querier, table string, keys [][2]any) map[string]string {
	results := map[string]string{}
	point := sha256.New()
	for _, key := range keys {
		fmt.Fprint(point, queryRows(db, `SELECT * FROM `+table+` WHERE "merchant" = ? AND "id" = ?`, key[0], key[1]))
	}
	results["point"] = hex.EncodeToString(point.Sum(nil))
	results["page"] = digestOf(queryRows(db, `SELECT "merchant", "id", "name", "price", "stock" FROM `+table+
		` WHERE "merchant" = 'shopee' ORDER BY "id" LIMIT 50`))
	results["count"] = digestOf(queryRows(db, `SELECT count(*), sum("stock"), sum("sold") FROM `+table+` WHERE "merchant" = 'lazada'`))
	return results
}

// atTimings times each read on table: point as the mean over keys, page over twenty runs and count over five, since
// Windows' clock moves in steps of about half a millisecond.
func atTimings(db querier, table string, keys [][2]any) (point, page, count time.Duration) {
	start := time.Now()
	for _, key := range keys {
		queryRows(db, `SELECT * FROM `+table+` WHERE "merchant" = ? AND "id" = ?`, key[0], key[1])
	}
	point = time.Since(start) / time.Duration(max(len(keys), 1))
	start = time.Now()
	for range 20 {
		queryRows(db, `SELECT "merchant", "id", "name", "price", "stock" FROM `+table+` WHERE "merchant" = 'shopee' ORDER BY "id" LIMIT 50`)
	}
	page = time.Since(start) / 20
	start = time.Now()
	for range 5 {
		queryRows(db, `SELECT count(*), sum("stock"), sum("sold") FROM `+table+` WHERE "merchant" = 'lazada'`)
	}
	count = time.Since(start) / 5
	return point, page, count
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
