package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	kitdb "github.com/kitwork/engine/kitdb/sqlite"
)

// cmdReadTail looks for the root of the rare slow read right after a commit (docs/KITWORK_KITDB.md mục 10, "Còn mở từ
// bản thử" of the Kitwork repository: 15-80 ms where the median is under a microsecond). Writers commit small
// updates through the engine's writer without pause while readers read one row at a time, each read as the engine's
// Database.Read runs it: a read transaction on a pool of read-only connections, the revision it sees, then the row.
// Each read's time is split into its steps, so a slow one shows where it waited. It runs twice, with Windows' timer
// at its default resolution and at 1 ms (timeBeginPeriod), since SQLite's WAL reader sleeps between its retries and
// a sleep on Windows lasts at least one tick of that timer.
//
// directory receives tail.db, a new file of 100,000 rows.
func cmdReadTail(directory string) {
	ctx := context.Background()
	path := filepath.Join(directory, "tail.db")
	_, statErr := os.Stat(path)
	fresh := statErr != nil
	database, err := kitdb.Open(ctx, path, kitdb.Options{})
	must(err)
	defer database.Close()
	const rows = 100_000
	if fresh {
		_, err = database.Migrate(ctx, "boot", `CREATE TABLE notes (id INTEGER PRIMARY KEY, title TEXT NOT NULL, n INTEGER NOT NULL)`)
		must(err)
	}
	for first := 0; fresh && first < rows; first += 10_000 {
		_, err := database.Write(ctx, "load", func(tx *kitdb.Transaction) error {
			_, err := tx.Exec(`WITH RECURSIVE k(i) AS (SELECT ? UNION ALL SELECT i + 1 FROM k WHERE i < ?)
				INSERT INTO notes (id, title, n) SELECT i, printf('note %08d', i), 0 FROM k`, first+1, first+10_000)
			return err
		})
		must(err)
	}
	readers, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?_pragma=busy_timeout(10000)&mode=ro&_pragma=query_only(1)")
	must(err)
	defer readers.Close()
	readers.SetMaxOpenConns(8)
	readers.SetMaxIdleConns(8)

	fmt.Printf("%d rows (a file made now: %v); 4 writers committing one-row updates, 4 readers reading one row each, 20 s a run\n\n", rows, fresh)
	fmt.Printf("%-22s %9s %8s %8s %8s %9s %9s  %s\n", "timer", "reads", "p50", "p99", "p99.9", "max", "writes/s", "reads over 2 ms: where, how long")
	for _, run := range []struct {
		label         string
		fine, writing bool
	}{
		{"Windows default", false, true}, {"Windows default", false, true}, {"1 ms (timeBeginPeriod)", true, true},
		{"no writer", false, false},
	} {
		if run.label == "no writer" {
			// Go alone, no SQLite: the same four goroutines hashing 4 KiB a step while the writers commit, so a stall
			// of the process or the machine shows here too
			goAlone(ctx, database, rows)
			locksAlone(directory)
		}
		restore := func() {}
		if run.fine {
			if restore, err = fineTimer(); err != nil {
				fmt.Printf("%-22s %v\n", run.label, err)
				continue
			}
		}
		runReadTail(ctx, database, readers, rows, run.label, run.writing)
		restore()
	}
	fmt.Println()
	fmt.Println("begin: BeginTx; revision: SELECT max(rev) FROM kitwork_history, where SQLite starts the read transaction;")
	fmt.Println("row: SELECT of one row by id; end: Rollback. A slow read is counted under its slowest step.")
}

// goAlone times a step of plain Go work (a SHA-256 of 4 KiB) on four goroutines for 20 s while the writers commit.
func goAlone(ctx context.Context, database *kitdb.Database, rows int) {
	var stop atomic.Bool
	var group sync.WaitGroup
	for writer := range 4 {
		group.Go(func() {
			random := rand.New(rand.NewPCG(uint64(writer), 9))
			for !stop.Load() {
				id := random.IntN(rows) + 1
				_, err := database.Write(ctx, "tail", func(tx *kitdb.Transaction) error {
					_, err := tx.Exec(`UPDATE notes SET n = n + 1 WHERE id = ?`, id)
					return err
				})
				must(err)
			}
		})
	}
	var mutex sync.Mutex
	var steps []time.Duration
	for range 4 {
		group.Go(func() {
			block := make([]byte, 4096)
			var mine []time.Duration
			for !stop.Load() {
				start := time.Now()
				sum := sha256.Sum256(block)
				block[0] = sum[0]
				mine = append(mine, time.Since(start))
			}
			mutex.Lock()
			steps = append(steps, mine...)
			mutex.Unlock()
		})
	}
	time.Sleep(20 * time.Second)
	stop.Store(true)
	group.Wait()
	slices.Sort(steps)
	over := func(limit time.Duration) int {
		count := 0
		for _, step := range steps {
			if step >= limit {
				count++
			}
		}
		return count
	}
	fmt.Printf("%-22s %9d %8s %8s %8s %9s %9s  steps of 2 ms or more %d, 10 ms or more %d\n", "Go alone (SHA-256)", len(steps),
		duration(steps[len(steps)/2]), duration(steps[len(steps)*99/100]), duration(steps[len(steps)*999/1000]),
		duration(steps[len(steps)-1]), "writing", over(2*time.Millisecond), over(10*time.Millisecond))
}

// locksAlone times, on four goroutines for 20 s with no writer, a shared lock taken and given back on one byte of a
// file of its own each: the Windows calls SQLite makes around every read transaction, without SQLite.
func locksAlone(directory string) {
	stop := make(chan struct{})
	var mutex sync.Mutex
	var steps []time.Duration
	var failure error
	var group sync.WaitGroup
	for locker := range 4 {
		group.Go(func() {
			mine, err := lockSteps(filepath.Join(directory, fmt.Sprintf("lock-%d", locker)), 120, stop)
			mutex.Lock()
			steps, failure = append(steps, mine...), errors.Join(failure, err)
			mutex.Unlock()
		})
	}
	time.Sleep(20 * time.Second)
	close(stop)
	group.Wait()
	if failure != nil || len(steps) == 0 {
		fmt.Printf("%-22s %v\n", "file locks alone", failure)
		return
	}
	slices.Sort(steps)
	over := func(limit time.Duration) int {
		count := 0
		for _, step := range steps {
			if step >= limit {
				count++
			}
		}
		return count
	}
	fmt.Printf("%-22s %9d %8s %8s %8s %9s %9s  steps of 2 ms or more %d, 10 ms or more %d\n", "file locks alone", len(steps),
		duration(steps[len(steps)/2]), duration(steps[len(steps)*99/100]), duration(steps[len(steps)*999/1000]),
		duration(steps[len(steps)-1]), "no writer", over(2*time.Millisecond), over(10*time.Millisecond))
}

type readSteps struct {
	begin, revision, row, end time.Duration
}

func (steps readSteps) total() time.Duration {
	return steps.begin + steps.revision + steps.row + steps.end
}

func runReadTail(ctx context.Context, database *kitdb.Database, readers *sql.DB, rows int, label string, writing bool) {
	var stop atomic.Bool
	var writes atomic.Int64
	var group sync.WaitGroup
	for writer := range 4 {
		if !writing {
			break
		}
		group.Go(func() {
			random := rand.New(rand.NewPCG(uint64(writer), 1))
			for !stop.Load() {
				id := random.IntN(rows) + 1
				_, err := database.Write(ctx, "tail", func(tx *kitdb.Transaction) error {
					_, err := tx.Exec(`UPDATE notes SET n = n + 1 WHERE id = ?`, id)
					return err
				})
				must(err)
				writes.Add(1)
			}
		})
	}
	var mutex sync.Mutex
	var all []readSteps
	for reader := range 4 {
		group.Go(func() {
			random := rand.New(rand.NewPCG(uint64(reader), 2))
			var mine []readSteps
			for !stop.Load() {
				var steps readSteps
				start := time.Now()
				tx, err := readers.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
				must(err)
				steps.begin = time.Since(start)
				start = time.Now()
				var revision int64
				must(tx.QueryRowContext(ctx, `SELECT coalesce(max(rev), 0) FROM kitwork_history`).Scan(&revision))
				steps.revision = time.Since(start)
				start = time.Now()
				var title string
				must(tx.QueryRowContext(ctx, `SELECT title FROM notes WHERE id = ?`, random.IntN(rows)+1).Scan(&title))
				steps.row = time.Since(start)
				start = time.Now()
				must(tx.Rollback())
				steps.end = time.Since(start)
				mine = append(mine, steps)
			}
			mutex.Lock()
			all = append(all, mine...)
			mutex.Unlock()
		})
	}
	started := time.Now()
	time.Sleep(20 * time.Second)
	stop.Store(true)
	group.Wait()
	elapsed := time.Since(started)

	totals := make([]time.Duration, len(all))
	for position, steps := range all {
		totals[position] = steps.total()
	}
	slices.Sort(totals)
	at := func(fraction float64) time.Duration {
		return totals[min(len(totals)-1, int(fraction*float64(len(totals))))]
	}
	where := map[string]int{}
	buckets := []time.Duration{5 * time.Millisecond, 10 * time.Millisecond, 20 * time.Millisecond, 40 * time.Millisecond, 80 * time.Millisecond}
	counts := make([]int, len(buckets)+1)
	for _, steps := range all {
		total := steps.total()
		if total < 2*time.Millisecond {
			continue
		}
		slowest, name := steps.begin, "begin"
		for _, step := range []struct {
			name string
			took time.Duration
		}{{"revision", steps.revision}, {"row", steps.row}, {"end", steps.end}} {
			if step.took > slowest {
				slowest, name = step.took, step.name
			}
		}
		where[name]++
		bucket := len(buckets)
		for position, limit := range buckets {
			if total < limit {
				bucket = position
				break
			}
		}
		counts[bucket]++
	}
	histogram := fmt.Sprintf("2-5 ms %d, 5-10 %d, 10-20 %d, 20-40 %d, 40-80 %d, over 80 %d", counts[0], counts[1], counts[2], counts[3], counts[4], counts[5])
	near := map[string]int{} // the reads of about a millisecond, by the step that took it
	for _, steps := range all {
		if total := steps.total(); total >= 900*time.Microsecond && total < 2*time.Millisecond {
			switch {
			case steps.revision >= 800*time.Microsecond:
				near["revision"]++
			case steps.row >= 800*time.Microsecond:
				near["row"]++
			case steps.begin >= 800*time.Microsecond:
				near["begin"]++
			case steps.end >= 800*time.Microsecond:
				near["end"]++
			default:
				near["spread"]++
			}
		}
	}
	histogram += fmt.Sprintf("; 0.9-2 ms %v", near)
	var slow []string
	for _, steps := range all {
		if steps.total() >= 10*time.Millisecond && len(slow) < 6 {
			slow = append(slow, fmt.Sprintf("%s/%s/%s/%s", duration(steps.begin), duration(steps.revision), duration(steps.row), duration(steps.end)))
		}
	}
	if len(slow) > 0 {
		histogram += fmt.Sprintf("; over 10 ms (begin/revision/row/end): %v", slow)
	}
	fmt.Printf("%-22s %9d %8s %8s %8s %9s %9.0f  %v; %s\n", label, len(totals), duration(at(0.5)), duration(at(0.99)),
		duration(at(0.999)), duration(totals[len(totals)-1]), float64(writes.Load())/elapsed.Seconds(), where, histogram)
}
