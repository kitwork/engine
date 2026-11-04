package queue

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fileDatabase is a SQLite file of the runs, each write one transaction begun IMMEDIATE under the file's gate,
// as work writes the app's database and a database of a name of the host's catalogue (Immediate). The tests ran on a
// KitDB file too until KROW went (03/11/2026).
type fileDatabase struct {
	db   *sql.DB
	path string
}

// sqlite is the file as work writes a SQLite file of the runs.
func (f *fileDatabase) sqlite() Immediate {
	return Immediate{Pool: f.db, Gate: FileGate(f.path)}
}

func (f *fileDatabase) Read(fn func(db SQL) error) error  { return f.sqlite().Read(fn) }
func (f *fileDatabase) Write(fn func(db SQL) error) error { return f.sqlite().Write(fn) }

func (f *fileDatabase) close() { f.db.Close() }

// openFile opens path as the engine opens a SQLite file of the runs, the tables made in it.
func openFile(t *testing.T, path string) *fileDatabase {
	t.Helper()
	return openSQLiteFile(t, path)
}

func newStore(t *testing.T) (*Store, *fileDatabase) {
	t.Helper()
	file := openFile(t, filepath.Join(t.TempDir(), "kitwork.db"))
	t.Cleanup(file.close)
	return New(file, "queue"), file
}

var epoch = time.Date(2026, 10, 22, 8, 0, 0, 0, time.UTC)

func send(t *testing.T, store *Store, run NewRun) (Run, bool) {
	t.Helper()
	if run.MaxAttempts == 0 {
		run.MaxAttempts = 1
	}
	stored, duplicate, err := store.Enqueue(run, epoch)
	if err != nil {
		t.Fatal(err)
	}
	return stored, duplicate
}

func claimOne(t *testing.T, store *Store, at time.Time) Claim {
	t.Helper()
	claims, _, err := store.Claim(at, 30*time.Second, 10, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 1 {
		t.Fatalf("claimed %d runs, want 1", len(claims))
	}
	return claims[0]
}

// A key held by a run of the job stores nothing more: the second send gives the first run's id with
// duplicate true (mục 4). CONTROL: the same sends without a key are three runs, so the one row above is
// the key's doing, not a send that stores nothing.
func TestEnqueueKeyGivesTheRunHoldingIt(t *testing.T) {
	store, _ := newStore(t)
	first, duplicate := send(t, store, NewRun{Name: "camOn", Data: `{"orderId":1}`, Key: "order:1"})
	if duplicate || first.Status != Pending || len(first.ID) != 18 || first.ID[:2] != "r_" {
		t.Fatalf("first send = %+v duplicate %v", first, duplicate)
	}
	again, duplicate := send(t, store, NewRun{Name: "camOn", Data: `{"orderId":1}`, Key: "order:1"})
	if !duplicate || again.ID != first.ID {
		t.Fatalf("second send with the key = %s duplicate %v, want %s duplicate", again.ID, duplicate, first.ID)
	}
	// another job may hold the same key
	other, duplicate := send(t, store, NewRun{Name: "flaky", Key: "order:1"})
	if duplicate || other.ID == first.ID {
		t.Fatalf("another job's key was taken as camOn's: %+v", other)
	}

	seen := map[string]bool{}
	for i := 0; i < 3; i++ {
		run, duplicate := send(t, store, NewRun{Name: "camOn", Data: `{"orderId":1}`})
		if duplicate || seen[run.ID] {
			t.Fatalf("unkeyed send %d = %s duplicate %v: NULL keys are deduplicated", i, run.ID, duplicate)
		}
		seen[run.ID] = true
	}
	runs, err := store.Runs(Filter{Name: "camOn"}, 50, 0)
	if err != nil || len(runs) != 4 {
		t.Fatalf("camOn has %d runs (%v), want 4", len(runs), err)
	}
}

// Only the holder of the lease settles a run: an attempt that lost it (failed by its timeout, then claimed
// again) cannot report success. CONTROL: the current holder completes it.
func TestSettleIsFencedByTheLeaseToken(t *testing.T) {
	store, _ := newStore(t)
	run, _ := send(t, store, NewRun{Name: "camOn", MaxAttempts: 2})
	first := claimOne(t, store, epoch)
	if first.Attempt != 1 || first.Token == "" {
		t.Fatalf("first claim = %+v", first)
	}
	status, err := store.Fail(run.ID, first.Token, "TIMEOUT", "hết giờ", "", epoch, epoch)
	if err != nil || status != Pending {
		t.Fatalf("fail with attempts left = %q %v, want pending", status, err)
	}
	second := claimOne(t, store, epoch)
	if second.ID != run.ID || second.Attempt != 2 || second.Token == first.Token {
		t.Fatalf("second claim = %+v", second)
	}

	won, err := store.Complete(run.ID, first.Token, "lần 1 xong muộn", epoch)
	if err != nil || won {
		t.Fatalf("completing with the first lease = %v %v, want refused", won, err)
	}
	if got, _ := store.Get(run.ID); got.Status != Running || got.Log != "" {
		t.Fatalf("after the stale completion the run is %s with log %q, want running and untouched", got.Status, got.Log)
	}
	if status, _ := store.Fail(run.ID, first.Token, "FAIL", "x", "", epoch, epoch); status != "" {
		t.Fatalf("failing with the first lease = %q, want refused", status)
	}

	won, err = store.Complete(run.ID, second.Token, "xong", epoch)
	if err != nil || !won {
		t.Fatalf("completing with the current lease = %v %v", won, err)
	}
	if got, _ := store.Get(run.ID); got.Status != Completed || got.Log != "xong" || got.FinishedAt == "" {
		t.Fatalf("completed run = %+v", got)
	}
}

// A failed attempt with attempts left keeps the run's id and waits until retryAt; the last one ends it
// failed, the row kept with its data and its error (the dead letter).
func TestFailRetriesThenDeadLetters(t *testing.T) {
	store, _ := newStore(t)
	run, _ := send(t, store, NewRun{Name: "flaky", Data: `{"n":1}`, MaxAttempts: 2})
	first := claimOne(t, store, epoch)
	later := epoch.Add(time.Minute)
	if status, _ := store.Fail(run.ID, first.Token, "FAIL", "lần 1 hỏng", "lần 1\n", later, epoch); status != Pending {
		t.Fatalf("first failure = %q, want pending", status)
	}
	if claims, _, _ := store.Claim(epoch.Add(30*time.Second), 30*time.Second, 10, nil); len(claims) != 0 {
		t.Fatalf("claimed a run before its retry time: %+v", claims)
	}
	second := claimOne(t, store, later)
	if second.ID != run.ID || second.Attempt != 2 {
		t.Fatalf("the retry is %+v, want the same run at attempt 2", second)
	}
	if status, _ := store.Fail(run.ID, second.Token, "FAIL", "lần 2 hỏng", "lần 1\nlần 2\n", later, later); status != Failed {
		t.Fatalf("last failure = %q, want failed", status)
	}
	got, _ := store.Get(run.ID)
	if got.Status != Failed || got.Attempt != 2 || got.ErrorCode != "FAIL" || got.ErrorMessage != "lần 2 hỏng" ||
		got.Data != `{"n":1}` || got.Log != "lần 1\nlần 2\n" {
		t.Fatalf("dead letter = %+v", got)
	}
}

// cancel by the run's status (mục 9): pending ends cancelled at once, running becomes cancelling and its
// failure ends it cancelled (never retried), an ended run is refused, an unknown id and another job's id
// are not found.
func TestCancelFollowsTheStatus(t *testing.T) {
	store, _ := newStore(t)
	waiting, _ := send(t, store, NewRun{Name: "camOn", Available: epoch.Add(time.Hour)})
	cancelled, err := store.Cancel(waiting.ID, "camOn", epoch)
	if err != nil || cancelled.Status != Cancelled {
		t.Fatalf("cancel of a pending run = %+v %v", cancelled, err)
	}
	again, err := store.Cancel(waiting.ID, "", epoch)
	if err != nil || again.Status != Cancelled {
		t.Fatalf("cancel of a cancelled run = %+v %v, want it given back as it is", again, err)
	}

	running, _ := send(t, store, NewRun{Name: "camOn", MaxAttempts: 3})
	claim := claimOne(t, store, epoch)
	if got, err := store.Cancel(running.ID, "", epoch); err != nil || got.Status != Cancelling {
		t.Fatalf("cancel of a running run = %+v %v, want cancelling", got, err)
	}
	if status, holds, _ := store.Extend(running.ID, claim.Token, epoch.Add(time.Minute)); !holds || status != Cancelling {
		t.Fatalf("the worker reads %q (holds %v), want cancelling", status, holds)
	}
	if status, _ := store.Fail(running.ID, claim.Token, "EXECUTION_CANCELLED", "huỷ", "", epoch, epoch); status != Cancelled {
		t.Fatalf("an attempt stopped by cancel = %q, want cancelled with attempts left", status)
	}

	finished, _ := send(t, store, NewRun{Name: "camOn"})
	token := claimOne(t, store, epoch).Token
	if won, _ := store.Complete(finished.ID, token, "", epoch); !won {
		t.Fatal("control: the run did not complete")
	}
	var state *StateError
	if _, err := store.Cancel(finished.ID, "", epoch); !errors.As(err, &state) || state.Run.Status != Completed {
		t.Fatalf("cancel of a completed run = %v, want a state error naming completed", err)
	}
	if _, err := store.Cancel("r_khongco", "", epoch); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cancel of an unknown id = %v", err)
	}
	if _, err := store.Cancel(finished.ID, "flaky", epoch); !errors.Is(err, ErrNotFound) {
		t.Fatalf("flaky.cancel of camOn's run = %v, want not found", err)
	}
}

// A running run that finishes before its cancel takes effect is completed (mục 7).
func TestCancellingRunThatFinishesIsCompleted(t *testing.T) {
	store, _ := newStore(t)
	run, _ := send(t, store, NewRun{Name: "camOn"})
	claim := claimOne(t, store, epoch)
	store.Cancel(run.ID, "", epoch)
	if won, err := store.Complete(run.ID, claim.Token, "xong", epoch); !won || err != nil {
		t.Fatalf("completion while cancelling = %v %v", won, err)
	}
	if got, _ := store.Get(run.ID); got.Status != Completed {
		t.Fatalf("status = %s, want completed", got.Status)
	}
}

// replay makes a new run from an ended one's data with the current definition, and keeps the old one; a
// run not ended is refused, and a job no file declares cannot be replayed.
func TestReplayMakesANewRun(t *testing.T) {
	store, _ := newStore(t)
	if _, err := store.Sync([]Definition{{Name: "camOn", Source: ".shared/queue/camOn.work", Retry: 3, Timeout: "30s", TimeoutMS: 30000, ContentHash: "a"}}, Withheld{}, epoch); err != nil {
		t.Fatal(err)
	}
	old, _ := send(t, store, NewRun{Name: "camOn", Data: `{"orderId":7}`, Key: "order:7"})
	claim := claimOne(t, store, epoch)
	store.Fail(old.ID, claim.Token, "FAIL", "hỏng", "", epoch, epoch)

	replayed, definition, err := store.Replay(old.ID, "camOn", epoch, nil)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.ID == old.ID || replayed.ReplayOf != old.ID || replayed.Status != Pending || replayed.Data != `{"orderId":7}` ||
		replayed.MaxAttempts != 4 || replayed.TimeoutMS != 30000 || replayed.Key != "" || definition.Retry != 3 {
		t.Fatalf("replay = %+v", replayed)
	}
	if got, _ := store.Get(old.ID); got.Status != Failed {
		t.Fatalf("the old run became %s, want it kept failed", got.Status)
	}
	var state *StateError
	if _, _, err := store.Replay(replayed.ID, "", epoch, nil); !errors.As(err, &state) || state.Run.Status != Pending {
		t.Fatalf("replay of a pending run = %v, want refused", err)
	}
	if _, _, err := store.Replay(old.ID, "", epoch, func(string) bool { return false }); !errors.Is(err, ErrNoDefinition) {
		t.Fatalf("replay of a job no file declares now = %v", err)
	}
	if _, err := store.Sync(nil, Withheld{}, epoch); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Replay(old.ID, "", epoch, nil); !errors.Is(err, ErrNoDefinition) {
		t.Fatalf("replay of a job no longer declared = %v", err)
	}
}

// A lease that ran out gives the run back, unless this process still runs the attempt (skip).
func TestReclaimGivesBackLostLeases(t *testing.T) {
	store, _ := newStore(t)
	lost, _ := send(t, store, NewRun{Name: "camOn", MaxAttempts: 2})
	kept, _ := send(t, store, NewRun{Name: "camOn", MaxAttempts: 2})
	if claims, _, _ := store.Claim(epoch, time.Second, 10, nil); len(claims) != 2 {
		t.Fatalf("claimed %d", len(claims))
	}
	count, _, err := store.Reclaim(epoch.Add(time.Minute), map[string]bool{kept.ID: true})
	if err != nil || count != 1 {
		t.Fatalf("reclaimed %d (%v), want 1", count, err)
	}
	if got, _ := store.Get(lost.ID); got.Status != Pending || got.ErrorCode != "TIMEOUT" || got.Attempt != 1 {
		t.Fatalf("the lost run = %+v", got)
	}
	if got, _ := store.Get(kept.ID); got.Status != Running {
		t.Fatalf("the run still held = %s, want running", got.Status)
	}
}

// Runs come newest first, by job and status, a page at a time; prune frees the ended ones before keep.
func TestRunsNewestFirstAndPrune(t *testing.T) {
	store, _ := newStore(t)
	var ids []string
	for i := 0; i < 5; i++ {
		run, _ := send(t, store, NewRun{Name: "camOn"})
		ids = append(ids, run.ID)
	}
	page, _ := store.Runs(Filter{}, 2, 1)
	if len(page) != 2 || page[0].ID != ids[3] || page[1].ID != ids[2] {
		t.Fatalf("page 2 of 2 = %v, want %s, %s", page, ids[3], ids[2])
	}
	store.Cancel(ids[0], "", epoch)
	cancelled, _ := store.Runs(Filter{Status: Cancelled}, 10, 0)
	if len(cancelled) != 1 || cancelled[0].ID != ids[0] {
		t.Fatalf("cancelled runs = %v", cancelled)
	}
	if count, _ := store.Prune(epoch.Add(-time.Second)); count != 0 {
		t.Fatalf("pruned %d runs that ended after the bound", count)
	}
	if count, _ := store.Prune(epoch.Add(time.Second)); count != 1 {
		t.Fatalf("pruned %d, want the cancelled run", count)
	}
	if _, err := store.Get(ids[0]); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a pruned run reads %v", err)
	}
}

// A definition whose content hash is unchanged is not written again. CONTROL: a new hash is.
func TestSyncSkipsUnchangedDefinitions(t *testing.T) {
	store, file := newStore(t)
	definition := Definition{Name: "camOn", Source: ".shared/queue/camOn.work", Retry: 3, ContentHash: "a"}
	store.Sync([]Definition{definition}, Withheld{}, epoch)
	file.db.Exec(`UPDATE kitwork_definitions SET retry = 9 WHERE name = 'camOn'`)
	store.Sync([]Definition{definition}, Withheld{}, epoch)
	read, _ := store.Definitions(10, 0)
	if len(read) != 1 || read[0].Retry != 9 {
		t.Fatalf("an unchanged definition was written again: %+v", read)
	}
	definition.ContentHash = "b"
	store.Sync([]Definition{definition}, Withheld{}, epoch)
	read, _ = store.Definitions(10, 0)
	if len(read) != 1 || read[0].Retry != 3 || read[0].Timeout != "" {
		t.Fatalf("a changed definition = %+v, want retry 3", read)
	}
}

// The runs are in the file: closed and opened again, the run sent before is there, pending, with its key.
func TestRunsSurviveReopening(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kitwork.db")
	file := openFile(t, path)
	run, _, err := New(file, "queue").Enqueue(NewRun{Name: "camOn", Key: "order:9", MaxAttempts: 4, Available: epoch.Add(5 * time.Minute)}, epoch)
	if err != nil {
		t.Fatal(err)
	}
	file.close()

	reopened := openFile(t, path)
	defer reopened.close()
	store := New(reopened, "queue")
	got, err := store.Get(run.ID)
	if err != nil || got.Status != Pending || got.Key != "order:9" || got.MaxAttempts != 4 || got.AvailableAt != Stamp(epoch.Add(5*time.Minute)) {
		t.Fatalf("after reopening: %+v %v", got, err)
	}
	if again, duplicate, _ := store.Enqueue(NewRun{Name: "camOn", Key: "order:9", MaxAttempts: 1}, epoch); !duplicate || again.ID != run.ID {
		t.Fatalf("the key is not held after reopening: %+v", again)
	}
}

// Claim leaves the runs of a withheld job pending (a job whose file did not load: its runs wait for the
// fix, attempts untouched), and says when the first run it left is due, the zero time when it left none:
// the worker does not look before then. CONTROL: without withheld the same run is claimed.
func TestClaimLeavesWithheldJobsAndSaysWhenNextIsDue(t *testing.T) {
	store, _ := newStore(t)
	now, _ := send(t, store, NewRun{Name: "camOn"})
	broken, _ := send(t, store, NewRun{Name: "broken"})
	later, _ := send(t, store, NewRun{Name: "camOn", Available: epoch.Add(time.Hour)})
	withheld := map[string]bool{"broken": true}

	claims, next, err := store.Claim(epoch, 30*time.Second, 10, withheld)
	if err != nil || len(claims) != 1 || claims[0].ID != now.ID {
		t.Fatalf("claimed %+v (%v), want %s alone: broken is withheld, later not due", claims, err, now.ID)
	}
	if !next.Equal(epoch.Add(time.Hour)) {
		t.Fatalf("next due = %s, want %s (later's, the withheld run counts for nothing)", next, epoch.Add(time.Hour))
	}
	if got, _ := store.Get(broken.ID); got.Status != Pending || got.Attempt != 0 {
		t.Fatalf("the withheld run = %+v, want pending, no attempt spent", got)
	}
	claims, next, _ = store.Claim(epoch, 30*time.Second, 10, nil)
	if len(claims) != 1 || claims[0].ID != broken.ID || !next.Equal(epoch.Add(time.Hour)) {
		t.Fatalf("without withheld claimed %+v, next %s", claims, next)
	}
	claims, next, _ = store.Claim(epoch.Add(time.Hour), 30*time.Second, 10, nil)
	if len(claims) != 1 || claims[0].ID != later.ID || !next.IsZero() {
		t.Fatalf("an hour on claimed %+v, next %s, want later and nothing left", claims, next)
	}
}

// Reclaim says when the first lease no attempt here holds runs out: a process that stopped while it ran
// an attempt left one that has not run out yet when the next one boots. CONTROL: once it runs out, the run
// is given back.
func TestReclaimSaysWhenTheNextLeaseRunsOut(t *testing.T) {
	store, _ := newStore(t)
	first, _ := send(t, store, NewRun{Name: "camOn", MaxAttempts: 2})
	claimOne(t, store, epoch) // leased until epoch+30s
	second, _ := send(t, store, NewRun{Name: "camOn", MaxAttempts: 2})
	claimOne(t, store, epoch.Add(10*time.Second)) // until epoch+40s

	count, next, err := store.Reclaim(epoch.Add(time.Second), nil)
	if err != nil || count != 0 || !next.Equal(epoch.Add(30*time.Second)) {
		t.Fatalf("reclaim before any lease ran out = %d, next %s (%v), want 0 and %s", count, next, err, epoch.Add(30*time.Second))
	}
	count, next, _ = store.Reclaim(epoch.Add(31*time.Second), nil)
	if count != 1 || !next.Equal(epoch.Add(40*time.Second)) {
		t.Fatalf("reclaim after the first lease = %d, next %s, want 1 and %s", count, next, epoch.Add(40*time.Second))
	}
	if got, _ := store.Get(first.ID); got.Status != Pending {
		t.Fatalf("the first run = %s, want given back", got.Status)
	}
	if _, next, _ = store.Reclaim(epoch.Add(31*time.Second), map[string]bool{second.ID: true}); !next.IsZero() {
		t.Fatalf("with the second held here, next = %s, want none", next)
	}
}

// Sync keeps the definitions a load withheld, by name (a job whose declaration has a problem) or by the
// file they were stored from (a file that did not compile), and says which: a mistake in a file is not the
// removal of its jobs. CONTROL: once nothing is withheld, they are deleted.
func TestSyncKeepsWithheldDefinitions(t *testing.T) {
	store, _ := newStore(t)
	a := Definition{Name: "a", Source: ".shared/queue/a.work", ContentHash: "1"}
	b := Definition{Name: "b", Source: ".shared/queue/b.work", ContentHash: "1"}
	c := Definition{Name: "c", Source: ".shared/queue/b.work", ContentHash: "1"}
	store.Sync([]Definition{a, b, c}, Withheld{}, epoch)
	names := func() string {
		read, _ := store.Definitions(10, 0)
		var out []string
		for _, definition := range read {
			out = append(out, definition.Name)
		}
		return strings.Join(out, ",")
	}

	kept, err := store.Sync([]Definition{a}, Withheld{Sources: map[string]bool{".shared/queue/b.work": true}}, epoch)
	if err != nil || strings.Join(kept, ",") != "b,c" || names() != "a,b,c" {
		t.Fatalf("b.work withheld: kept %v (%v), definitions %s, want b and c kept", kept, err, names())
	}
	kept, _ = store.Sync([]Definition{a}, Withheld{Names: map[string]bool{"b": true}}, epoch)
	if strings.Join(kept, ",") != "b" || names() != "a,b" {
		t.Fatalf("b withheld: kept %v, definitions %s, want b kept and c gone", kept, names())
	}
	if kept, _ = store.Sync([]Definition{a}, Withheld{}, epoch); len(kept) != 0 || names() != "a" {
		t.Fatalf("nothing withheld: kept %v, definitions %s, want a alone", kept, names())
	}
}

// numbered is a database that numbers runs itself (Sequencer), from 100.
type numbered struct {
	*fileDatabase
	next int64
}

func (n *numbered) NextSeq(SQL) (int64, error) {
	n.next++
	return 99 + n.next, nil
}

// A database that numbers runs (work's runs database counts on from the highest seq it read once) is
// asked for the number, and the table is not read for it. CONTROL: the order of runs() is that number's.
func TestSequencerNumbersRuns(t *testing.T) {
	_, file := newStore(t)
	store := New(&numbered{fileDatabase: file}, "queue")
	first, _ := send(t, store, NewRun{Name: "camOn"})
	second, _ := send(t, store, NewRun{Name: "camOn"})
	var seq int64
	if err := file.db.QueryRow(`SELECT seq FROM kitwork_runs WHERE id = $1`, second.ID).Scan(&seq); err != nil || seq != 101 {
		t.Fatalf("the second run's seq = %d (%v), want 101 from the sequencer", seq, err)
	}
	if runs, _ := store.Runs(Filter{}, 10, 0); len(runs) != 2 || runs[0].ID != second.ID || runs[1].ID != first.ID {
		t.Fatalf("runs = %v, want newest first", runs)
	}
	if highest, err := HighestSeq(file.db); err != nil || highest != 101 {
		t.Fatalf("highest seq = %d (%v), want 101", highest, err)
	}
}
