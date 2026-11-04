package queue

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// openSQLitePool opens a pool of a SQLite file as the engine opens a file of the host's catalogue
// (database.Config: WAL, a busy timeout, foreign keys), the busy timeout in milliseconds.
func openSQLitePool(t *testing.T, path string, busy int) *sql.DB {
	t.Helper()
	pool, err := sql.Open("sqlite", fmt.Sprintf("file:%s?_pragma=busy_timeout(%d)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)", filepath.ToSlash(path), busy))
	if err != nil {
		t.Fatal(err)
	}
	return pool
}

// openSQLiteFile opens a SQLite file as the engine opens a file of the host's catalogue, with the tables made
// in it.
func openSQLiteFile(t *testing.T, path string) *fileDatabase {
	t.Helper()
	file := &fileDatabase{db: openSQLitePool(t, path, 5000), path: path}
	if err := file.Write(InitSchema); err != nil {
		file.close()
		t.Fatal(err)
	}
	return file
}

// The store's statements were KitDB's SQL; the same statements keep the runs on SQLite, the app's database
// (.database/application/kitwork.db) or a database of the host's catalogue when queue.database(db) or
// schedule.database(db) chooses one (KITWORK_QUEUE.md mục 2). Every test of the store runs on a SQLite file in
// WAL (openFile); until KROW went (03/11/2026) they ran on a KitDB file too.
//
// CONTROL (03/11/2026): with openSQLitePool leaving out journal_mode(WAL), "openFile gave journal_mode
// "delete" (<nil>), want a SQLite file in WAL".
func TestOpenFileIsSQLiteInWAL(t *testing.T) {
	file := openFile(t, filepath.Join(t.TempDir(), "jobs.db"))
	defer file.close()
	var mode string
	if err := file.db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("openFile gave journal_mode %q (%v), want a SQLite file in WAL", mode, err)
	}
}

// Writers on one SQLite file never give two runs one number, nor take one run twice. Two pools on the file
// stand for two stores of this process writing it, eight writers on them; each write holds the file's gate
// and is one transaction begun IMMEDIATE (Immediate), so a send's read of the highest seq and its insert are
// one step. A claim's updates also check, each, that the run is still pending (Store.Claim), as on KitDB: the
// claim half holds by that check whatever the write is, and is asserted here, not proven of the write. What
// each of the two layers keeps on its own is proven apart: the gate by TestSQLiteWritesOfAFileWaitOnItsGate,
// the transaction by TestSQLiteAppWritesWaitForAWriteOfTheRuns and TestSQLiteWriteThatFailsLeavesNothing.
//
// CONTROL (24/10/2026): with Immediate.Write holding no gate and running fn on the pool, no transaction, three
// runs gave "200 runs hold 190 numbers" (190, 192): two sends read the same highest seq. With either layer
// alone it stays green. Before the gate (the transaction alone, writers of two pools waiting on SQLite's
// lock), one run of a review under -race gave "200 runs hold 123 numbers", not seen again in some 400 runs:
// the gate is why this process no longer counts on the VFS's locking of its own connections.
func TestSQLiteWritersTakeEachRunOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.db")
	first, second := openSQLiteFile(t, path), openSQLiteFile(t, path)
	defer first.close()
	defer second.close()
	stores := []*Store{New(first, "queue"), New(second, "queue")}
	const writers, each = 8, 25

	failures := make(chan error, writers*each)
	var wg sync.WaitGroup
	for writer := 0; writer < writers; writer++ {
		wg.Add(1)
		go func(store *Store) {
			defer wg.Done()
			for i := 0; i < each; i++ {
				if _, _, err := store.Enqueue(NewRun{Name: "camOn", MaxAttempts: 1}, epoch); err != nil {
					failures <- fmt.Errorf("send: %w", err)
					return
				}
			}
		}(stores[writer%2])
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	var runs, numbers int
	if err := first.db.QueryRow(`SELECT COUNT(*), COUNT(DISTINCT seq) FROM kitwork_runs`).Scan(&runs, &numbers); err != nil {
		t.Fatal(err)
	}
	if runs != writers*each || numbers != runs {
		t.Fatalf("%d runs hold %d numbers, want %d runs, each its own seq", runs, numbers, writers*each)
	}

	var mu sync.Mutex
	taken := map[string]int{}
	failures = make(chan error, writers)
	for writer := 0; writer < writers; writer++ {
		wg.Add(1)
		go func(store *Store) {
			defer wg.Done()
			for {
				claims, _, err := store.Claim(epoch, time.Minute, 3, nil)
				if err != nil {
					failures <- fmt.Errorf("claim: %w", err)
					return
				}
				if len(claims) == 0 {
					return
				}
				mu.Lock()
				for _, claim := range claims {
					taken[claim.ID]++
				}
				mu.Unlock()
			}
		}(stores[writer%2])
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	if len(taken) != writers*each {
		t.Fatalf("%d runs claimed, want all %d", len(taken), writers*each)
	}
	for id, times := range taken {
		if times != 1 {
			t.Fatalf("run %s claimed %d times", id, times)
		}
	}
}

// The store's writes of one file wait for each other on the file's gate (FileGate), not on SQLite's lock: a
// write that holds the file longer than the busy timeout does not make the next one fail, through another
// pool of the file too. The gate is the file's however its path is written.
//
// CONTROL (24/10/2026): with Immediate.Write holding no gate, the send waited out the busy timeout (50ms)
// on SQLite's lock and failed: "a send while another write of the file held it: database is locked (5)
// (SQLITE_BUSY)".
func TestSQLiteWritesOfAFileWaitOnItsGate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.db")
	if FileGate(path) != FileGate(filepath.Dir(path)+"/./jobs.db") {
		t.Fatal("one file written two ways gave two gates")
	}
	holder := Immediate{Pool: openSQLitePool(t, path, 50), Gate: FileGate(path)}
	defer holder.Pool.Close()
	other := Immediate{Pool: openSQLitePool(t, path, 50), Gate: FileGate(path)}
	defer other.Pool.Close()
	if err := holder.Write(InitSchema); err != nil {
		t.Fatal(err)
	}

	holding, held := make(chan struct{}), make(chan error, 1)
	go func() {
		held <- holder.Write(func(db SQL) error {
			close(holding)
			time.Sleep(300 * time.Millisecond)
			_, _, err := New(holder, "queue").insert(db, NewRun{Name: "camOn", MaxAttempts: 1}, epoch)
			return err
		})
	}()
	<-holding
	sent, _, err := New(other, "queue").Enqueue(NewRun{Name: "camOn", MaxAttempts: 1}, epoch)
	if err != nil {
		t.Fatalf("a send while another write of the file held it: %v", err)
	}
	if err := <-held; err != nil {
		t.Fatal(err)
	}
	var seq int64
	if err := other.Pool.QueryRow(`SELECT seq FROM kitwork_runs WHERE id = $1`, sent.ID).Scan(&seq); err != nil || seq != 2 {
		t.Fatalf("the send waiting for the write was numbered %d (%v), want 2, after the write's run", seq, err)
	}
}

// holdFile holds path's write lock from another pool, as an app statement or another process would, until
// the returned function lets it go.
func holdFile(t *testing.T, path string) (letGo func()) {
	t.Helper()
	other := openSQLitePool(t, path, 50)
	ctx := context.Background()
	writer, err := other.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	letGo = func() {
		once.Do(func() {
			_, _ = writer.ExecContext(ctx, "COMMIT")
			writer.Close()
			other.Close()
		})
	}
	t.Cleanup(letGo)
	return letGo
}

// A write with Done (the worker's: a claim, a settle, a heartbeat) waits for the file's write lock for as
// long as another writer holds it, past the busy timeout: an app statement that writes for long, or another
// process, never makes a settle fail and its attempt run again (KITWORK_QUEUE.md mục 16). Waiting is told each
// time the busy timeout runs out, with how long the write has waited so far.
//
// CONTROL (24/10/2026): with Write trying once whatever Done, the send failed when the busy timeout (50ms)
// ran out: "a send while another writer held the file: database is locked (5) (SQLITE_BUSY)"; with Waiting
// told the time of the last try instead of the whole wait: "Waiting was told [50ms 50ms …]".
func TestSQLiteWriteWaitsOutAnotherWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.db")
	var mu sync.Mutex
	var waited []time.Duration
	runs := Immediate{Pool: openSQLitePool(t, path, 50), Gate: FileGate(path), Done: make(chan struct{}),
		Waiting: func(d time.Duration) { mu.Lock(); waited = append(waited, d); mu.Unlock() }}
	defer runs.Pool.Close()
	if err := runs.Write(InitSchema); err != nil {
		t.Fatal(err)
	}
	letGo := holdFile(t, path)
	const held = 400 * time.Millisecond
	start := time.Now()
	time.AfterFunc(held, letGo)
	if _, _, err := New(runs, "queue").Enqueue(NewRun{Name: "camOn", MaxAttempts: 1}, epoch); err != nil {
		t.Fatalf("a send while another writer held the file: %v", err)
	}
	if took := time.Since(start); took < held-50*time.Millisecond {
		t.Fatalf("the send was stored after %v, while the other writer held the file %v", took, held)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(waited) < 2 || waited[len(waited)-1] < held/2 {
		t.Fatalf("Waiting was told %v, want the time waited so far, growing to about %v", waited, held)
	}
	for k := 1; k < len(waited); k++ {
		if waited[k] <= waited[k-1] {
			t.Fatalf("Waiting was told %v, want the time waited so far, growing", waited)
		}
	}
}

// A write with no Done (a send, a cancel, a replay of a request) tries once: when another writer holds the
// file for the whole busy timeout it fails (SQLITE_BUSY, DATABASE to the app), as before the wait. And while a
// write with Done waits, the gate is free between its tries: the request's write gets its own try, it does
// not wait behind the worker's for as long as the file is held.
//
// CONTROL (24/10/2026): with Write beginning again with no Done too, and with the gate held across the tries of
// a write with Done, the request's send waited for as long as the file was held: "the send of a request still
// waited 2s while another writer held the file, want one try".
func TestSQLiteWriteWithNoDoneTriesOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.db")
	request := Immediate{Pool: openSQLitePool(t, path, 50), Gate: FileGate(path)}
	defer request.Pool.Close()
	if err := request.Write(InitSchema); err != nil {
		t.Fatal(err)
	}
	letGo := holdFile(t, path)
	worker := Immediate{Pool: openSQLitePool(t, path, 50), Gate: FileGate(path), Done: make(chan struct{})}
	defer worker.Pool.Close()
	waiting := make(chan error, 1)
	go func() {
		_, _, err := New(worker, "queue").Enqueue(NewRun{Name: "flaky", MaxAttempts: 1}, epoch)
		waiting <- err
	}()
	time.Sleep(100 * time.Millisecond)
	start := time.Now()
	sent := make(chan error, 1)
	go func() {
		_, _, err := New(request, "queue").Enqueue(NewRun{Name: "camOn", MaxAttempts: 1}, epoch)
		sent <- err
	}()
	var err error
	select {
	case err = <-sent:
	case <-time.After(2 * time.Second):
		letGo()
		t.Fatal("the send of a request still waited 2s while another writer held the file, want one try")
	}
	took := time.Since(start)
	if !busy(err) {
		t.Fatalf("a send with no Done while another writer held the file: %v, want SQLITE_BUSY", err)
	}
	if took > 500*time.Millisecond {
		t.Fatalf("the send of a request took %v while the worker's write waited, want one try of it", took)
	}
	letGo()
	if err := <-waiting; err != nil {
		t.Fatalf("the worker's send after the file was let go: %v", err)
	}
}

// Done closing ends the wait of a write: the worker stopping, or the app closing, does not wait for another
// writer to let the file go. The write fails (SQLITE_BUSY) and nothing of it is in the file.
//
// CONTROL (24/10/2026): with Write not reading Done, the write waited until the file was let go: "the write
// still waited 2s after Done closed".
func TestSQLiteWriteEndsWhenDoneCloses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.db")
	done := make(chan struct{})
	runs := Immediate{Pool: openSQLitePool(t, path, 50), Gate: FileGate(path), Done: done}
	defer runs.Pool.Close()
	if err := runs.Write(InitSchema); err != nil {
		t.Fatal(err)
	}
	letGo := holdFile(t, path)
	ended := make(chan error, 1)
	go func() {
		_, _, err := New(runs, "queue").Enqueue(NewRun{Name: "camOn", MaxAttempts: 1}, epoch)
		ended <- err
	}()
	time.Sleep(150 * time.Millisecond)
	close(done)
	select {
	case err := <-ended:
		if !busy(err) {
			t.Fatalf("the write after Done closed gave %v, want SQLITE_BUSY", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the write still waited 2s after Done closed")
	}
	letGo()
	var runsStored int
	if err := runs.Pool.QueryRow(`SELECT COUNT(*) FROM kitwork_runs`).Scan(&runsStored); err != nil || runsStored != 0 {
		t.Fatalf("the file holds %d runs (%v) of a write that ended unbegun", runsStored, err)
	}
}

// workersWaiting starts k writes with done and a short Try on path, held by another writer: each a send of
// its own run, on a pool whose busy timeout (400ms) is far longer than Try. ended gets each one's error.
func workersWaiting(t *testing.T, path string, k int, done chan struct{}, try time.Duration) (ended chan error) {
	t.Helper()
	ended = make(chan error, k)
	for w := 0; w < k; w++ {
		worker := Immediate{Pool: openSQLitePool(t, path, 400), Gate: FileGate(path), Done: done, Try: try}
		t.Cleanup(func() { worker.Pool.Close() })
		go func(w int) {
			_, _, err := New(worker, "queue").Enqueue(NewRun{Name: fmt.Sprintf("worker%d", w), MaxAttempts: 1}, epoch)
			ended <- err
		}(w)
	}
	return ended
}

// A try of a worker's write waits Try for another writer's lock, not the pool's busy timeout, so it holds the
// gate that long at most: a request's write behind three waiting worker writes waits for one short try of
// each, then its own busy timeout, and fails (SQLITE_BUSY) in well under a second, not after three of the
// workers' busy timeouts.
//
// CONTROL (24/10/2026): with Try not read (each try of a worker's write waiting the pool's 400ms): "the send
// of a request behind 3 waiting worker writes took 1.12s, want under 600ms".
func TestSQLiteWorkerTriesAreShort(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.db")
	request := Immediate{Pool: openSQLitePool(t, path, 50), Gate: FileGate(path)}
	defer request.Pool.Close()
	if err := request.Write(InitSchema); err != nil {
		t.Fatal(err)
	}
	letGo := holdFile(t, path)
	done := make(chan struct{})
	ended := workersWaiting(t, path, 3, done, 20*time.Millisecond)
	time.Sleep(150 * time.Millisecond)
	start := time.Now()
	_, _, err := New(request, "queue").Enqueue(NewRun{Name: "camOn", MaxAttempts: 1}, epoch)
	took := time.Since(start)
	if !busy(err) {
		t.Fatalf("a send with no Done while another writer held the file: %v, want SQLITE_BUSY", err)
	}
	if took > 600*time.Millisecond {
		t.Fatalf("the send of a request behind 3 waiting worker writes took %v, want under 600ms", took)
	}
	letGo()
	for w := 0; w < 3; w++ {
		if err := <-ended; err != nil {
			t.Fatalf("a worker's send after the file was let go: %v", err)
		}
	}
	close(done)
}

// Done closing ends every worker write waiting on the file at once: a write waiting for the gate looks at Done
// when it gets it and does not try again, so closing the app with four writes waiting costs at most the try
// that holds the gate, not a try of each. (The first try of a write always runs, Done or not: a settle at the
// app's close is written when the file is free.)
//
// CONTROL (24/10/2026): with a try after the first not looking at Done once it has the gate, the four writes
// took a try each: "4 waiting worker writes ended 761ms after Done closed, want under 450ms".
func TestSQLiteWaitingWritesEndTogether(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.db")
	file := Immediate{Pool: openSQLitePool(t, path, 50), Gate: FileGate(path)}
	defer file.Pool.Close()
	if err := file.Write(InitSchema); err != nil {
		t.Fatal(err)
	}
	letGo := holdFile(t, path)
	done := make(chan struct{})
	ended := workersWaiting(t, path, 4, done, 200*time.Millisecond)
	// every write past its first try
	time.Sleep(1500 * time.Millisecond)
	close(done)
	start := time.Now()
	for w := 0; w < 4; w++ {
		if err := <-ended; !busy(err) {
			t.Fatalf("a worker's write after Done closed gave %v, want SQLITE_BUSY", err)
		}
	}
	if took := time.Since(start); took > 450*time.Millisecond {
		t.Fatalf("4 waiting worker writes ended %v after Done closed, want under 450ms", took)
	}
	letGo()
}

// A short try sets the busy timeout of its connection for the BEGIN alone and sets the pool's back: the
// connection goes back to the pool waiting the pool's busy timeout, so the next write on it (a request's send,
// an app statement) does not give up after the worker's short try.
//
// CONTROL (24/10/2026): with begin not setting the pool's busy timeout back, the request's send on the same
// connection gave up after the short try: "the send after a worker's short tries gave up after 21ms, want
// the pool's busy timeout (300ms)".
func TestSQLiteShortTryGivesTheConnectionBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.db")
	pool := openSQLitePool(t, path, 300)
	defer pool.Close()
	pool.SetMaxOpenConns(1)
	done := make(chan struct{})
	worker := Immediate{Pool: pool, Gate: FileGate(path), Done: done, Try: 20 * time.Millisecond}
	if err := worker.Write(InitSchema); err != nil {
		t.Fatal(err)
	}
	letGo := holdFile(t, path)
	ended := make(chan error, 1)
	go func() {
		_, _, err := New(worker, "queue").Enqueue(NewRun{Name: "flaky", MaxAttempts: 1}, epoch)
		ended <- err
	}()
	time.Sleep(150 * time.Millisecond)
	close(done)
	if err := <-ended; !busy(err) {
		t.Fatalf("the worker's write after Done closed gave %v, want SQLITE_BUSY", err)
	}
	request := Immediate{Pool: pool, Gate: FileGate(path)}
	start := time.Now()
	_, _, err := New(request, "queue").Enqueue(NewRun{Name: "camOn", MaxAttempts: 1}, epoch)
	if took := time.Since(start); !busy(err) || took < 250*time.Millisecond {
		t.Fatalf("the send after a worker's short tries gave up after %v (%v), want the pool's busy timeout (300ms)", took, err)
	}
	letGo()
}

// The app's own statements on the file (its ORM on connection("tên")) hold no gate of the runs: what keeps
// them from writing between a write of the runs' read and its change is SQLite's write lock, which the
// write's transaction takes at its start (BEGIN IMMEDIATE). The app's statement waits for the write to
// commit (the busy timeout), and neither fails.
//
// CONTROL (24/10/2026): with BEGIN (DEFERRED) for BEGIN IMMEDIATE, the transaction held no write lock after
// its read: "the app's statement wrote in the middle of a write of the runs (<nil>)"; the same with no
// transaction at all (fn run on the pool, the gate held).
func TestSQLiteAppWritesWaitForAWriteOfTheRuns(t *testing.T) {
	file := openSQLiteFile(t, filepath.Join(t.TempDir(), "jobs.db"))
	defer file.close()
	if _, err := file.db.Exec(`CREATE TABLE letters (id INTEGER PRIMARY KEY, email TEXT)`); err != nil {
		t.Fatal(err)
	}
	store := New(file, "queue")
	app := make(chan error, 1)
	err := file.Write(func(db SQL) error {
		if _, err := HighestSeq(db); err != nil {
			return err
		}
		go func() {
			_, err := file.db.Exec(`INSERT INTO letters (email) VALUES ('an@example.com')`)
			app <- err
		}()
		time.Sleep(200 * time.Millisecond)
		select {
		case err := <-app:
			return fmt.Errorf("the app's statement wrote in the middle of a write of the runs (%v)", err)
		default:
		}
		_, _, err := store.insert(db, NewRun{Name: "camOn", MaxAttempts: 1}, epoch)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := <-app; err != nil {
		t.Fatalf("the app's statement after the write of the runs: %v", err)
	}
	var letters, runs int
	if err := file.db.QueryRow(`SELECT (SELECT COUNT(*) FROM letters), (SELECT COUNT(*) FROM kitwork_runs)`).Scan(&letters, &runs); err != nil || letters != 1 || runs != 1 {
		t.Fatalf("the file holds %d letters and %d runs (%v), want one of each", letters, runs, err)
	}
}

// A write of the runs on SQLite is one transaction: one that fails half way leaves nothing of itself, and its
// connection goes back to the pool out of the transaction.
//
// CONTROL (24/10/2026): with Immediate.Write running fn on the pool, no transaction, the run stored before
// the failure stayed: "a write that failed half way left [camOn pending]".
func TestSQLiteWriteThatFailsLeavesNothing(t *testing.T) {
	file := openSQLiteFile(t, filepath.Join(t.TempDir(), "jobs.db"))
	defer file.close()
	store := New(file, "queue")
	halfWay := errors.New("half way")
	err := file.Write(func(db SQL) error {
		if _, _, err := store.insert(db, NewRun{Name: "camOn", MaxAttempts: 1}, epoch); err != nil {
			return err
		}
		return halfWay
	})
	if !errors.Is(err, halfWay) {
		t.Fatalf("the write gave %v, want its own error", err)
	}
	list := func() []string {
		runs, err := store.Runs(Filter{}, 10, 0)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, run := range runs {
			out = append(out, run.Name+" "+run.Status)
		}
		return out
	}
	if left := list(); len(left) != 0 {
		t.Fatalf("a write that failed half way left %v", left)
	}
	send(t, store, NewRun{Name: "camOn"})
	if stored := list(); len(stored) != 1 {
		t.Fatalf("the send after the failed write stored %v, want one run", stored)
	}
}
