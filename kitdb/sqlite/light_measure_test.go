package sqlite

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// TestMeasureLightChanges measures what a commit of a table costs the writer when the history keeps its images
// (history on), records it light (a columns copy declared, KITWORK_KITDB.md câu 13.11, lát 2b), or does not follow it:
// one-row commits, an insert then an update of another column, in blocks of 200 taken in turn in one process (this
// machine moves between two speeds, so runs apart do not compare). Set KITDB_MEASURE=1 to run it.
func TestMeasureLightChanges(t *testing.T) {
	if os.Getenv("KITDB_MEASURE") == "" {
		t.Skip("KITDB_MEASURE=1 runs it")
	}
	ctx := context.Background()
	modes := []string{"images", "light", "none"}
	databases := map[string]*Database{}
	for _, mode := range modes {
		database, err := Open(ctx, filepath.Join(t.TempDir(), mode+".db"), Options{})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = database.Close() })
		if _, err := database.Migrate(ctx, "boot",
			`CREATE TABLE posts (id INTEGER PRIMARY KEY, title TEXT, body TEXT, stock INTEGER)`,
			`CREATE TABLE IF NOT EXISTS kitwork_columnar (table_name TEXT NOT NULL, column_name TEXT NOT NULL, PRIMARY KEY (table_name, column_name)) STRICT`,
		); err != nil {
			t.Fatal(err)
		}
		switch mode {
		case "images":
			if _, err := database.SetHistory(ctx, "t", "posts", true); err != nil {
				t.Fatal(err)
			}
		case "light":
			mustWrite(t, database, `INSERT INTO kitwork_columnar VALUES ('posts', 'title')`)
		}
		database.background.Wait()
		databases[mode] = database
	}
	times := map[string][]time.Duration{}
	next := map[string]int{}
	body := fmt.Sprintf("%0400d", 7)
	for block := 0; block < 12; block++ {
		for _, mode := range modes {
			database := databases[mode]
			for range 100 {
				next[mode]++
				id := next[mode]
				for _, statement := range []string{
					fmt.Sprintf(`INSERT INTO posts (id, title, body, stock) VALUES (%d, 'bài số %d', '%s', %d)`, id, id, body, id),
					fmt.Sprintf(`UPDATE posts SET stock = stock + 1 WHERE id = %d`, (id*7919)%id+1),
				} {
					begin := time.Now()
					mustWrite(t, database, statement)
					times[mode] = append(times[mode], time.Since(begin))
				}
			}
		}
	}
	for _, mode := range modes {
		sorted := slices.Clone(times[mode])
		slices.Sort(sorted)
		var total time.Duration
		for _, took := range sorted {
			total += took
		}
		var bytes int64
		if err := databases[mode].readers.QueryRow(`SELECT coalesce(sum(length(changes)), 0) FROM kitwork_history`).Scan(&bytes); err != nil {
			t.Fatal(err)
		}
		t.Logf("%-6s %d commits: median %v, p90 %v, mean %v; kitwork_history %d bytes", mode, len(sorted),
			sorted[len(sorted)/2].Round(time.Microsecond), sorted[len(sorted)*9/10].Round(time.Microsecond),
			(total / time.Duration(len(sorted))).Round(time.Microsecond), bytes)
	}
}
