package main

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/maphash"
	"io"
	"io/fs"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	kitdb "github.com/kitwork/engine/kitdb/sqlite"
)

// cmdAnchor measures what an anchor of the history costs on a file the size of shopping's: the anchor written today,
// a whole copy (kitdb/sqlite Anchor: VACUUM INTO, the history cut to its last commit, VACUUM), against an anchor that
// keeps only the pages changed since the one before it, over a base that is a byte copy of the file. Between anchors
// it writes through the engine's writer, so the history grows as an app's would: scattered updates, updates on a hot
// set of rows, inserts. Then it restores the last commit both ways. Evidence for docs/KITWORK_KITDB.md mục 5 of the
// Kitwork repository (04/11/2026).
//
// directory holds work.db, a copy of .database/shopping/kitwork.db, which is written to; base.db, the page deltas
// and the restored files are written beside it, at most about four times the file's size with SQLite's temporary
// copy of a VACUUM.
func cmdAnchor(directory string) {
	ctx := context.Background()
	work := filepath.Join(directory, "work.db")
	base := filepath.Join(directory, "base.db")
	for _, path := range []string{base, work + ".history"} {
		if _, err := os.Stat(path); err == nil {
			panic(fmt.Sprintf("%s exists: anchor runs once on a fresh copy", path))
		}
	}
	checkpointFile(work)
	pageSize, pageCount, rowCount := fileGeometry(work)
	fmt.Printf("work.db: %d pages of %d bytes (%s), %d rows of shopping\n\n", pageCount, pageSize,
		gigabytes(int64(pageCount)*int64(pageSize)), rowCount)

	seed := maphash.MakeSeed()
	start := time.Now()
	hashes := hashPages(work, pageSize, seed)
	read := time.Since(start)
	fmt.Printf("reading every page from the file:                 %s (%s/s)\n", duration(read), gigabytes(int64(float64(len(hashes)*pageSize)/read.Seconds())))
	start = time.Now()
	through := hashDBPages(work, len(hashes), seed)
	fmt.Printf("reading every page through sqlite_dbpage:         %s, one read transaction\n", duration(time.Since(start)))
	if len(changedPages(hashes, through)) != 0 {
		panic("sqlite_dbpage and the file disagree")
	}
	start = time.Now()
	copyBytes(work, base)
	fmt.Printf("a byte copy of the file (the pages' base):        %s\n", duration(time.Since(start)))
	start = time.Now()
	database, err := kitdb.Open(ctx, work, kitdb.Options{}) // a file without an anchor gets one as it opens
	must(err)
	whole := time.Since(start)
	must(database.Close())
	wholeSize := directorySize(work + ".history")
	fmt.Printf("today's anchor (VACUUM INTO, history cut, VACUUM): %s, %s\n\n", duration(whole), gigabytes(wholeSize))

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

	fmt.Printf("%-36s %9s %10s %10s %8s %8s  %s\n", "between two anchors", "writes/s", "pages", "delta", "scan", "write", "pages by owner")
	var deltas []string
	var deltaTotal int64
	for index, step := range steps {
		database, err := kitdb.Open(ctx, work, kitdb.Options{})
		must(err)
		start := time.Now()
		runWrites(ctx, database, step.query, step.arguments)
		rate := float64(len(step.arguments)) / time.Since(start).Seconds()
		must(database.Close())
		checkpointFile(work)
		start = time.Now()
		next := hashPages(work, pageSize, seed)
		scan := time.Since(start)
		changed := changedPages(hashes, next)
		delta := filepath.Join(directory, fmt.Sprintf("delta-%d.pages", index+1))
		start = time.Now()
		size := writeDelta(work, delta, pageSize, changed, len(next))
		written := time.Since(start)
		deltas = append(deltas, delta)
		deltaTotal += size
		fmt.Printf("%-36s %9.0f %10d %10s %8s %8s  %s\n", step.name, rate, len(changed), gigabytes(size), duration(scan),
			duration(written), pageOwners(work, changed))
		hashes = next
	}
	historyRows, historyBytes := historySize(work)
	fmt.Printf("\nthe history now: %d commits, %s of changes\n\n", historyRows, gigabytes(historyBytes))

	database, err = kitdb.Open(ctx, work, kitdb.Options{})
	must(err)
	restored := filepath.Join(directory, "restored-whole.db")
	start = time.Now()
	revision, err := database.Restore(ctx, time.Now(), restored)
	must(err)
	fmt.Printf("restore today (copy the anchor, replay every commit since): %s, revision %d\n", duration(time.Since(start)), revision)
	must(database.Close())
	want := rowsDigest(work)
	if got := rowsDigest(restored); got != want {
		panic(fmt.Sprintf("the restored file reads %s, the file %s", got, want))
	}
	removeDatabase(restored)

	restored = filepath.Join(directory, "restored-pages.db")
	start = time.Now()
	copyBytes(base, restored)
	copying := time.Since(start)
	start = time.Now()
	for _, delta := range deltas {
		applyDelta(restored, delta)
	}
	applying := time.Since(start)
	fmt.Printf("restore by pages (copy the base, write the deltas):         %s + %s\n", duration(copying), duration(applying))
	if len(changedPages(hashes, hashPages(restored, pageSize, seed))) != 0 {
		panic("the pages restored differ from the file")
	}
	if got := rowsDigest(restored); got != want {
		panic(fmt.Sprintf("the pages restored read %s, the file %s", got, want))
	}
	removeDatabase(restored)
	fmt.Printf("both restores read %s; the pages restored are the file byte for byte\n\n", want)
	fmt.Printf("kept as anchors: today one whole copy a day of commits, %s each; by pages the base %s and %s for the %d deltas\n",
		gigabytes(wholeSize), gigabytes(int64(pageCount)*int64(pageSize)), gigabytes(deltaTotal), len(deltas))
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

// hashPages reads the file page by page: page n is hashes[n-1].
func hashPages(path string, pageSize int, seed maphash.Seed) []uint64 {
	file, err := os.Open(path)
	must(err)
	defer file.Close()
	info, err := file.Stat()
	must(err)
	hashes := make([]uint64, int(info.Size()/int64(pageSize)))
	buffer := make([]byte, 64*pageSize)
	for first := 0; first < len(hashes); first += 64 {
		count := min(64, len(hashes)-first)
		_, err := io.ReadFull(file, buffer[:count*pageSize])
		must(err)
		for position := range count {
			hashes[first+position] = maphash.Bytes(seed, buffer[position*pageSize:(position+1)*pageSize])
		}
	}
	return hashes
}

// hashDBPages reads every page through sqlite_dbpage in one read transaction: what a live file's pages are at one
// revision without stopping its writer.
func hashDBPages(path string, count int, seed maphash.Seed) []uint64 {
	db := openSQLite(path, "&mode=ro")
	defer db.Close()
	tx, err := db.Begin()
	must(err)
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT pgno, data FROM sqlite_dbpage`)
	must(err)
	defer rows.Close()
	hashes := make([]uint64, count)
	for rows.Next() {
		var number int
		var data sql.RawBytes
		must(rows.Scan(&number, &data))
		hashes[number-1] = maphash.Bytes(seed, data)
	}
	must(rows.Err())
	return hashes
}

// changedPages lists the pages whose bytes differ, and every page after the end of before.
func changedPages(before, after []uint64) []uint32 {
	var changed []uint32
	for position := range after {
		if position >= len(before) || before[position] != after[position] {
			changed = append(changed, uint32(position+1))
		}
	}
	return changed
}

func copyBytes(source, target string) {
	input, err := os.Open(source)
	must(err)
	defer input.Close()
	output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	must(err)
	_, err = io.Copy(output, input)
	must(err)
	must(output.Sync())
	must(output.Close())
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

// writeDelta keeps the changed pages of source: the page size and the page count, then each page's number and bytes.
func writeDelta(source, target string, pageSize int, changed []uint32, pageCount int) int64 {
	input, err := os.Open(source)
	must(err)
	defer input.Close()
	output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	must(err)
	writer := bufio.NewWriterSize(output, 1<<20)
	header := binary.BigEndian.AppendUint32(binary.BigEndian.AppendUint32(nil, uint32(pageSize)), uint32(pageCount))
	_, err = writer.Write(header)
	must(err)
	page := make([]byte, pageSize)
	for _, number := range changed {
		_, err := input.ReadAt(page, int64(number-1)*int64(pageSize))
		must(err)
		_, err = writer.Write(binary.BigEndian.AppendUint32(nil, number))
		must(err)
		_, err = writer.Write(page)
		must(err)
	}
	must(writer.Flush())
	must(output.Sync())
	info, err := output.Stat()
	must(err)
	must(output.Close())
	return info.Size()
}

func applyDelta(target, delta string) {
	input, err := os.Open(delta)
	must(err)
	defer input.Close()
	reader := bufio.NewReaderSize(input, 1<<20)
	header := make([]byte, 8)
	_, err = io.ReadFull(reader, header)
	must(err)
	pageSize, pageCount := int64(binary.BigEndian.Uint32(header)), int64(binary.BigEndian.Uint32(header[4:]))
	output, err := os.OpenFile(target, os.O_RDWR, 0)
	must(err)
	page := make([]byte, pageSize)
	number := make([]byte, 4)
	for {
		if _, err := io.ReadFull(reader, number); errors.Is(err, io.EOF) {
			break
		} else {
			must(err)
		}
		_, err := io.ReadFull(reader, page)
		must(err)
		_, err = output.WriteAt(page, int64(binary.BigEndian.Uint32(number)-1)*pageSize)
		must(err)
	}
	must(output.Truncate(pageCount * pageSize))
	must(output.Sync())
	must(output.Close())
}

// pageOwners names the b-tree each changed page belongs to (dbstat); a page in none is on the free list.
func pageOwners(path string, changed []uint32) string {
	wanted := make(map[uint32]bool, len(changed))
	for _, number := range changed {
		wanted[number] = true
	}
	db := openSQLite(path, "&mode=ro")
	defer db.Close()
	rows, err := db.Query(`SELECT pageno, name FROM dbstat`)
	must(err)
	defer rows.Close()
	owners := map[string]int{}
	found := 0
	for rows.Next() {
		var number uint32
		var name string
		must(rows.Scan(&number, &name))
		if wanted[number] {
			owners[name]++
			found++
		}
	}
	must(rows.Err())
	if found < len(changed) {
		owners["free list"] = len(changed) - found
	}
	names := make([]string, 0, len(owners))
	for name := range owners {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool { return owners[names[i]] > owners[names[j]] })
	parts := make([]string, len(names))
	for position, name := range names {
		parts[position] = fmt.Sprintf("%s %d", name, owners[name])
	}
	return strings.Join(parts, ", ")
}

func historySize(path string) (rows, size int64) {
	db := openSQLite(path, "&mode=ro")
	defer db.Close()
	must(db.QueryRow(`SELECT count(*), coalesce(sum(length(changes)), 0) FROM kitwork_history`).Scan(&rows, &size))
	return rows, size
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
