package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	kitdb "github.com/kitwork/engine/kitdb/sqlite"
)

// cmdAnchor measures the engine's anchors on a file the size of shopping's (kitdb/sqlite, KITWORK_KITDB.md mục 5, câu
// 13.9 of the Kitwork repository): the first one a base, every page of the file read through sqlite_dbpage in one read
// transaction and copied; every later one the pages changed since, found by the hashes of the anchor before. Between
// anchors it writes through the engine's writer, so the history grows as an app's would: scattered updates, updates
// on a hot set of rows, inserts. Then moments are restored from the base and the pages after it and compared with
// the file as it was, and Retain folds the oldest anchors into the base. Evidence for KITWORK_KITDB.md mục 5
// (05/11/2026); the anchor measured on 04/11/2026, a whole copy, is in RESULTS_2026-11-04.md.
//
// directory holds work.db, a copy of .database/shopping/kitwork.db, which is written to; the anchors and the restored
// files are written beside it, at most about three times the file's size.
func cmdAnchor(directory string) {
	ctx := context.Background()
	work := filepath.Join(directory, "work.db")
	history := work + ".history"
	if _, err := os.Stat(history); err == nil {
		panic(fmt.Sprintf("%s exists: anchor runs once on a fresh copy", history))
	}
	checkpointFile(work)
	pageSize, pageCount, rowCount := fileGeometry(work)
	fmt.Printf("work.db: %d pages of %d bytes (%s), %d rows of shopping\n\n", pageCount, pageSize,
		gigabytes(int64(pageCount)*int64(pageSize)), rowCount)

	start := time.Now()
	database, err := kitdb.Open(ctx, work, kitdb.Options{}) // a file without an anchor gets one as it opens: a base
	must(err)
	fmt.Printf("the first anchor, a base (Open): %s, %s; pages.hashes %s\n\n", duration(time.Since(start)),
		gigabytes(newestSize(history, ".base")), gigabytes(fileSize(filepath.Join(history, "pages.hashes"))))

	columns := make([]string, len(shoppingColumns))
	for position, column := range shoppingColumns {
		columns[position] = quoted(column.name)
	}
	selected := strings.Replace(strings.Join(columns, ", "), `"id"`, `?`, 1) // a new id, unique for every merchant
	update := `UPDATE shopping SET "stock" = "stock" - 1, "sold" = "sold" + 1 WHERE rowid = ?`
	insert := `INSERT INTO shopping (` + strings.Join(columns, ", ") + `) SELECT ` + selected + ` FROM shopping WHERE rowid = ?`
	random := rand.New(rand.NewPCG(4, 11))
	hot := make([]int64, 10000)
	for position := range hot {
		hot[position] = random.Int64N(rowCount) + 1
	}
	inserted := int64(1_000_000_000_000)
	arguments := func(count int, next func() []any) [][]any {
		list := make([][]any, count)
		for position := range list {
			list[position] = next()
		}
		return list
	}
	scattered := func() []any { return []any{random.Int64N(rowCount) + 1} }
	onHot := func() []any { return []any{hot[random.IntN(len(hot))]} }
	copied := func() []any { inserted++; return []any{inserted, random.Int64N(rowCount) + 1} }
	steps := []struct {
		name, query string
		arguments   [][]any
	}{
		{"1,000 updates of random rows", update, arguments(1000, scattered)},
		{"10,000 updates of random rows", update, arguments(10000, scattered)},
		{"100,000 updates of random rows", update, arguments(100000, scattered)},
		{"100,000 updates on 10,000 hot rows", update, arguments(100000, onHot)},
		{"100,000 inserts", insert, arguments(100000, copied)},
	}

	fmt.Printf("%-36s %9s %10s %10s\n", "then", "writes/s", "anchor", ".pages")
	type anchored struct {
		revision uint64
		digest   string
	}
	var anchors []anchored
	for _, step := range steps {
		start := time.Now()
		runWrites(ctx, database, step.query, step.arguments)
		rate := float64(len(step.arguments)) / time.Since(start).Seconds()
		start = time.Now()
		revision, err := database.Anchor(ctx)
		must(err)
		took := time.Since(start)
		anchors = append(anchors, anchored{revision, rowsDigest(work)})
		fmt.Printf("%-36s %9.0f %10s %10s\n", step.name, rate, duration(took), gigabytes(newestSize(history, ".pages")))
	}
	fmt.Printf("\nthe anchors now: %s: %s\n\n", gigabytes(directorySize(history)), anchorNames(history))

	// The base and the .pages it chains: each moment restores, the newest through every .pages.
	restore := func(label string, want anchored) {
		target := filepath.Join(directory, "restored.db")
		moment := revisionMoment(ctx, database, want.revision)
		start := time.Now()
		revision, err := database.Restore(ctx, moment, target)
		must(err)
		took := time.Since(start)
		got := rowsDigest(target)
		same := "the same rows"
		if revision != want.revision || got != want.digest {
			same = fmt.Sprintf("DIFFERENT: revision %d, %s against %s", revision, got, want.digest)
		}
		fmt.Printf("restore %-40s %10s, %s\n", label, duration(took), same)
		removeDatabase(target)
	}
	restore("at the second anchor (base + 2 .pages)", anchors[1])
	restore("at the last anchor (base + 5 .pages)", anchors[len(anchors)-1])

	// Retain as a day after the third anchor with a day kept: the base takes in the first three .pages.
	moment := revisionMoment(ctx, database, anchors[2].revision)
	start = time.Now()
	kept, err := database.Retain(ctx, moment.Add(24*time.Hour), kitdb.Retention{Keep: 24 * time.Hour, Every: 1000 * time.Hour})
	must(err)
	fmt.Printf("\nRetain, the base taking in three .pages and the history before them let go: %s, kept from revision %d\n",
		duration(time.Since(start)), kept)
	fmt.Printf("the anchors now: %s: %s\n\n", gigabytes(directorySize(history)), anchorNames(history))
	restore("at the third anchor (the folded base)", anchors[2])
	restore("at the last anchor (base + 2 .pages)", anchors[len(anchors)-1])
	must(database.Close())
}

// revisionMoment is the moment of revision's commit.
func revisionMoment(ctx context.Context, database *kitdb.Database, revision uint64) time.Time {
	var moment time.Time
	found := false
	must(database.Changes(ctx, revision-1, func(commit kitdb.Commit) error {
		if commit.Revision == revision {
			moment, found = commit.At, true
		}
		return nil
	}))
	if !found {
		panic(fmt.Sprintf("revision %d is not in the history", revision))
	}
	return moment
}

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	must(err)
	return info.Size()
}

// newestSize is the size of the newest anchor file of the extension: the names sort by revision.
func newestSize(history, extension string) int64 {
	entries, err := os.ReadDir(history)
	must(err)
	var newest string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), extension) {
			newest = entry.Name()
		}
	}
	return fileSize(filepath.Join(history, newest))
}

// anchorNames lists the anchor files by revision and kind, with each one's size.
func anchorNames(history string) string {
	entries, err := os.ReadDir(history)
	must(err)
	var names []string
	for _, entry := range entries {
		name := entry.Name()
		info, err := entry.Info()
		must(err)
		if strings.IndexByte(name, '-') == 20 {
			var revision uint64
			fmt.Sscanf(name[:20], "%d", &revision)
			name = fmt.Sprintf("%d%s", revision, filepath.Ext(name))
		}
		names = append(names, fmt.Sprintf("%s %s", name, gigabytes(info.Size())))
	}
	return strings.Join(names, ", ")
}

func gigabytes(size int64) string {
	if size >= 1<<30 {
		return fmt.Sprintf("%.2f GB", float64(size)/(1<<30))
	}
	return fmt.Sprintf("%.1f MB", float64(size)/(1<<20))
}

func openSQLite(path string, parameters string) *sql.DB {
	db, err := sql.Open("sqlite", "file:"+toSlash(path)+"?_pragma=busy_timeout(10000)"+parameters)
	must(err)
	db.SetMaxOpenConns(1)
	return db
}

// checkpointFile leaves every commit in the main file and the WAL empty, so the file's bytes are the database.
func checkpointFile(path string) {
	db := openSQLite(path, "")
	defer db.Close()
	var busy, log, done int
	must(db.QueryRow(`PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &log, &done))
	if busy != 0 {
		panic("the checkpoint was blocked")
	}
}

func fileGeometry(path string) (pageSize, pageCount int, rows int64) {
	db := openSQLite(path, "&mode=ro")
	defer db.Close()
	must(db.QueryRow(`PRAGMA page_size`).Scan(&pageSize))
	must(db.QueryRow(`PRAGMA page_count`).Scan(&pageCount))
	must(db.QueryRow(`SELECT max(rowid) FROM shopping`).Scan(&rows))
	return pageSize, pageCount, rows
}

func directorySize(path string) int64 {
	var size int64
	must(filepath.WalkDir(path, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		info, err := entry.Info()
		size += info.Size()
		return err
	}))
	return size
}

// runWrites sends each write as its own request from sixteen goroutines, so the writer gathers them as it would an
// app's requests.
func runWrites(ctx context.Context, database *kitdb.Database, query string, arguments [][]any) {
	var next atomic.Int64
	var failure error
	var once sync.Once
	var wait sync.WaitGroup
	for range 16 {
		wait.Go(func() {
			for {
				position := int(next.Add(1)) - 1
				if position >= len(arguments) {
					return
				}
				_, err := database.Write(ctx, "measure", func(tx *kitdb.Transaction) error {
					_, err := tx.Exec(query, arguments[position]...)
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

// rowsDigest is what a restore must give back: the rows' count, the sums the writes moved, the last row and the
// last revision.
func rowsDigest(path string) string {
	db := openSQLite(path, "&mode=ro")
	defer db.Close()
	var count, stock, sold, identifiers, last, revision int64
	must(db.QueryRow(`SELECT count(*), sum("stock"), sum("sold"), sum("id"), max(rowid) FROM shopping`).
		Scan(&count, &stock, &sold, &identifiers, &last))
	must(db.QueryRow(`SELECT max(rev) FROM kitwork_history`).Scan(&revision))
	return fmt.Sprintf("{rows %d, stock %d, sold %d, ids %d, last row %d, revision %d}", count, stock, sold, identifiers, last, revision)
}

func removeDatabase(path string) {
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := os.Remove(path + suffix); err != nil && !errors.Is(err, fs.ErrNotExist) {
			panic(err)
		}
	}
}
