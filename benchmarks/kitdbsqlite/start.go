package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	kitdb "github.com/kitwork/engine/kitdb/sqlite"
)

// cmdStart measures the anchor written as a table's history starts (kitdb/sqlite, KITWORK_KITDB.md câu 13.11 of the
// Kitwork repository, 08/11/2026): on a copy of shopping's file, a table is made and its history set on, and the anchor
// of the commit it starts in is written in the background while the writer goes on. It prints how long SetHistory held
// its caller, how long the anchor took and what it holds, and the time of one-row commits before it and while it is
// written: what the writer pays for the pages read from the same disk.
//
// path is a copy, written to: a table start_<n> is made in it, with its history on, and an anchor is written beside
// it, a .pages when the copy has an anchor already, a .base of every page when it has none. The anchor's read
// transaction holds the WAL back from a checkpoint while it lasts, so the largest the -wal file grows is printed too,
// and its size once the writer has cut it back after the anchor.
func cmdStart(path string) {
	ctx := context.Background()
	history := path + ".history"
	fmt.Printf("anchors before: %v\n", anchorNamesIn(history))
	begin := time.Now()
	database, err := kitdb.Open(ctx, path, kitdb.Options{})
	must(err)
	defer database.Close()
	fmt.Printf("Open: %s\n", duration(time.Since(begin)))
	readers, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro")
	must(err)
	defer readers.Close()
	table := fmt.Sprintf("start_%d", time.Now().UnixNano())
	_, err = database.Migrate(ctx, "bench", `CREATE TABLE `+table+` (id INTEGER PRIMARY KEY, note TEXT)`)
	must(err)
	commit := func() time.Duration {
		begin := time.Now()
		_, err := database.Write(ctx, "bench", func(transaction *kitdb.Transaction) error {
			_, err := transaction.Exec(`INSERT INTO ` + table + ` (note) VALUES ('x')`)
			return err
		})
		must(err)
		return time.Since(begin)
	}
	var before []time.Duration
	for range 300 {
		before = append(before, commit())
	}

	begin = time.Now()
	_, err = database.SetHistory(ctx, "bench", table, true)
	must(err)
	held := time.Since(begin)
	_, from, err := kitdb.TableHistory(ctx, readers, table)
	must(err)
	prefix := fmt.Sprintf("%020d-", from)
	var during []time.Duration
	var anchor string
	var wal int64
	for anchor == "" {
		during = append(during, commit())
		wal = max(wal, fileSize(path+"-wal"))
		for _, name := range anchorNamesIn(history) {
			if strings.HasPrefix(name, prefix) {
				anchor = name
			}
		}
	}
	written := time.Since(begin)
	fmt.Printf("SetHistory held its caller %s; the anchor of the start, revision %d, written in %s: %s, %s\n",
		duration(held), from, duration(written), anchor, gigabytes(fileSize(filepath.Join(history, anchor))))
	fmt.Printf("one-row commits before:          %s\n", commitTimes(before))
	fmt.Printf("one-row commits while it is written: %s\n", commitTimes(during))
	fmt.Printf("the -wal file: %s at most while it is written, %s once it is\n", gigabytes(wal), gigabytes(fileSize(path+"-wal")))
	// the anchor's snapshot closes after its file is named; a commit then checkpoints the WAL, the next restarts it,
	// cut to the writer's journal_size_limit (64 MiB, 08/11/2026)
	after := 0
	for ; after < 1000 && fileSize(path+"-wal") > 64<<20; after++ {
		commit()
	}
	fmt.Printf("the -wal file %d commits after: %s\n", after, gigabytes(fileSize(path+"-wal")))
	fmt.Printf("anchors after: %v\n", anchorNamesIn(history))
}

func anchorNamesIn(history string) []string {
	entries, err := os.ReadDir(history)
	if err != nil {
		return nil
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

// commitTimes is the count, median, 99th percentile and longest of times.
func commitTimes(times []time.Duration) string {
	sorted := slices.Clone(times)
	slices.Sort(sorted)
	at := func(share float64) time.Duration { return sorted[int(share*float64(len(sorted)-1))] }
	return fmt.Sprintf("%d, median %s, p99 %s, longest %s", len(sorted), duration(at(0.5)), duration(at(0.99)), duration(sorted[len(sorted)-1]))
}
