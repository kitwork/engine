package work

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	hostdatabase "github.com/kitwork/engine/database"
	queuestore "github.com/kitwork/engine/utilities/queue"
	"github.com/kitwork/engine/value"
)

// queue.database(db) and schedule.database(db) (KITWORK_QUEUE.md mục 2, KITWORK_SCHEDULE.md mục 2,
// 24/10/2026): the runs of each kind live in the database the config.work of its folder hands its .database
// call, a database the app declares in .shared/schema/; the app's own by default.

// jobsSchema declares the host's database jobs in the app: a table, since schema({ … }) takes one at least.
const jobsSchema = `import { table, id, text } from "kitwork/schema";
import { connection } from "kitwork/database";
const letters = table({ id: id(), email: text() });
export const jobs = connection("jobs").tables({ letters });
`

const beatSchedule = `import * as schedule from "kitwork/schedule";
export const beat = schedule.every("1h").timezone("Asia/Ho_Chi_Minh").run((context) => context.job.log("nhịp"));
`

const beatRoute = `import * as router from "kitwork/router";
import { beat } from "app/.schedule/beat.work";
router.post((context) => context.json(beat.now()));
`

// queueInJobs and scheduleInJobs are the config.work of each folder that hands its .database call jobs.
const (
	queueInJobs    = "import * as queue from \"kitwork/queue\";\nimport { jobs } from \"app/.shared/schema/jobs.work\";\nqueue.database(jobs);\n"
	scheduleInJobs = "import * as schedule from \"kitwork/schedule\";\nimport { jobs } from \"app/.shared/schema/jobs.work\";\nschedule.database(jobs);\n"
)

// jobsCatalogue declares the host's database jobs, a SQLite file of the test, and gives its path.
func jobsCatalogue(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "host", "jobs.db")
	withCatalogue(t, map[string]hostdatabase.Config{"jobs": {Type: "sqlite", Name: path}})
	return path
}

// runsApp is an app that sends camOn from / and runs beat now from /beat, each folder's config.work what it is
// given ("" for none).
func runsApp(queueConfig, scheduleConfig string) map[string]string {
	files := map[string]string{
		".shared/schema/jobs.work":   jobsSchema,
		".shared/email.work":         emailLibrary,
		".shared/queue/camOn.work":   camOnJob,
		".schedule/beat.work":        beatSchedule,
		"localhost/router.work":      sendRoute,
		"localhost/beat/router.work": beatRoute,
	}
	if queueConfig != "" {
		files[".shared/queue/config.work"] = queueConfig
	}
	if scheduleConfig != "" {
		files[".schedule/config.work"] = scheduleConfig
	}
	return files
}

// textRows reads rows, each row's columns joined by a space.
func textRows(rows *sql.Rows) ([]string, error) {
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var out []string
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			return nil, err
		}
		parts := make([]string, len(values))
		for i, item := range values {
			if bytes, isBytes := item.([]byte); isBytes {
				item = string(bytes)
			}
			parts[i] = fmt.Sprint(item)
		}
		out = append(out, strings.Join(parts, " "))
	}
	return out, rows.Err()
}

// fileRows reads a SQLite file of the test as anyone would, beside the app that holds it (waiting for the
// file's lock as the engine's own connections do): none for a file never made.
func fileRows(t *testing.T, path, query string) []string {
	t.Helper()
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}
	file, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	rows, err := file.Query(query)
	if err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	out, err := textRows(rows)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// appRows reads the app's own database, its SQLite file .database/application/kitwork.db.
func appRows(t *testing.T, q *queueApp, query string) []string {
	t.Helper()
	var out []string
	err := q.app.runsDatabase(queueKind, nil, nil).Read(func(db queuestore.SQL) error {
		rows, err := db.Query(query)
		if err != nil {
			return err
		}
		out, err = textRows(rows)
		return err
	})
	if err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return out
}

// Each kind follows its own call: with queue.database(jobs) the runs of the jobs and their definitions are in
// the host's SQLite file jobs, and the schedules stay in the app's KitDB; with schedule.database(jobs) the
// other way round, the schedules' state (kitwork_schedules) with their runs. The reads of the app read the
// database its kind is in, and boot says where each lives.
//
// CONTROL (24/10/2026): with runsStoreAt giving the app's database whatever the place, both kinds ran in the
// app's KitDB in both cases: "runs in jobs = [], want [queue camOn completed]", "runs in the app's database =
// [queue camOn completed schedule beat completed]", the definitions likewise, and no kitwork_schedules row in
// jobs; with runsStore reading the queue's place for every kind, now() stored beat where the schedules' worker
// does not look and it never ran ("never settled as completed; settled as []"), in both cases. The
// schedules' clock is pinned: on the real clock, a test that ran across HH:00 also stored beat's due time and
// saw two runs of it (forced with SetScheduleClock one tick before the hour).
func TestRunsLiveInTheDatabaseEachFolderChose(t *testing.T) {
	queueWatch, scheduleWatch := watchQueue(t), watchSchedule(t)
	// the schedules' clock stands mid-hour: beat (every hour) never falls due while the test runs, so the
	// runs counted are the test's own
	setClock(t, instant(t, "2026-10-23T03:30:00Z"))
	for _, each := range []struct {
		name                     string
		queueConfig, scheduleCfg string
		inJobs, inApp            string
		booted                   []string
	}{
		{"the queue in jobs, the schedules in the app's", queueInJobs, "", "queue camOn completed", "schedule beat completed",
			[]string{`[Queue] worker up (kitwork_runs của database jobs) — 1 job cho "acme": camOn`,
				`[Schedule] đồng hồ chạy (kitwork_runs của .database/application/kitwork.db) — 1 lịch cho "acme": beat`}},
		{"the schedules in jobs, the queue in the app's", "", scheduleInJobs, "schedule beat completed", "queue camOn completed",
			[]string{`[Queue] worker up (kitwork_runs của .database/application/kitwork.db) — 1 job cho "acme": camOn`,
				`[Schedule] đồng hồ chạy (kitwork_runs của database jobs) — 1 lịch cho "acme": beat`}},
	} {
		t.Run(each.name, func(t *testing.T) {
			path := jobsCatalogue(t)
			q := newQueueApp(t, runsApp(each.queueConfig, each.scheduleCfg))
			for _, line := range each.booted {
				if !strings.Contains(q.output, line) {
					t.Errorf("boot did not say %q:\n%s", line, q.output)
				}
			}
			sent := q.serve(t, http.MethodPost, "/", order("an@example.com", "1", "", ""))
			beat := q.serve(t, http.MethodPost, "/beat", url.Values{})
			queueWatch.until(t, sent["id"].(string), "completed")
			scheduleWatch.until(t, beat["id"].(string), "completed")

			const runs = `SELECT kind, name, status FROM kitwork_runs ORDER BY seq`
			if got := fileRows(t, path, runs); strings.Join(got, ",") != each.inJobs {
				t.Errorf("runs in jobs = %v, want [%s]", got, each.inJobs)
			}
			if got := appRows(t, q, runs); strings.Join(got, ",") != each.inApp {
				t.Errorf("runs in the app's database = %v, want [%s]", got, each.inApp)
			}
			const definitions = `SELECT kind, name FROM kitwork_definitions ORDER BY kind`
			kindOf := func(line string) string { return strings.Join(strings.Fields(line)[:2], " ") }
			if got := fileRows(t, path, definitions); strings.Join(got, ",") != kindOf(each.inJobs) {
				t.Errorf("definitions in jobs = %v, want [%s]", got, kindOf(each.inJobs))
			}
			if got := appRows(t, q, definitions); strings.Join(got, ",") != kindOf(each.inApp) {
				t.Errorf("definitions in the app's database = %v, want [%s]", got, kindOf(each.inApp))
			}
			// the state of the schedules is where their runs are
			states := fileRows(t, path, `SELECT name FROM kitwork_schedules`)
			if inJobs := strings.HasPrefix(each.inJobs, "schedule"); (len(states) == 1) != inJobs {
				t.Errorf("kitwork_schedules of jobs = %v, the schedules in jobs: %v", states, inJobs)
			}

			// what the app reads is where its kind lives
			queued, err := q.app.queueStore().Runs(queuestore.Filter{}, 10, 0)
			if err != nil || len(queued) != 1 || queued[0].ID != sent["id"] {
				t.Errorf("queue.runs() = %v (%v), want %s", queued, err, sent["id"])
			}
			scheduled, err := q.app.scheduleStore().Runs(queuestore.Filter{}, 10, 0)
			if err != nil || len(scheduled) != 1 || scheduled[0].ID != beat["id"] {
				t.Errorf("schedule.runs() = %v (%v), want %s", scheduled, err, beat["id"])
			}
		})
	}
}

// The writes of the runs in a database of the catalogue hold the file's gate (queuestore.FileGate), the one
// gate of the file in this process, so they go one at a time whatever SQLite's lock does; what the gate keeps
// is proven in utilities/queue (TestSQLiteWritesOfAFileWaitOnItsGate). A write that waits for another writer
// of the file past the busy timeout (TestSQLiteWriteWaitsOutAnotherWriter) says so on the server's output,
// with the kind and the database; only the running worker's writes wait (runsDatabase with its stop), a
// request's try once. The file's pool keeps connections between statements (hostSQLiteConfig), so a write of
// the runs does not open the file again.
//
// CONTROL (24/10/2026): with catalogRuns.open giving queuestore.Immediate{Pool: pool}, no gate: "the runs of
// jobs are written under gate 0x0, not the file's"; with it giving no Waiting: "a write of the runs of jobs
// waits for the file and the server's output is not told"; with runsDatabase handing catalogRuns no kind:
// "waiting said [Queue] …, want [Schedule] …"; with it handing no stop: "a write of the running worker
// on jobs does not end when the worker stops"; with hostSQLiteConfig keeping no idle connection (MaxIdle 0):
// "after a read the pool of jobs keeps 0 idle connections".
func TestRunsOfACatalogueFileAreWrittenUnderItsGate(t *testing.T) {
	path := jobsCatalogue(t)
	q := newQueueApp(t, runsApp(queueInJobs, ""))
	place := q.app.queueWorker().runs.Load()
	if place == nil {
		t.Fatalf("the queue's runs are not in jobs:\n%s", q.output)
	}
	database, err := q.app.runsDatabase(queueKind, place, nil).(catalogRuns).open()
	if err != nil {
		t.Fatal(err)
	}
	if database.Done != nil {
		t.Fatal("a write of the runs of jobs with no worker's stop (a send of a request) waits for the file")
	}
	stop := make(chan struct{})
	running, err := q.app.runsDatabase(scheduleKind, place, stop).(catalogRuns).open()
	if err != nil {
		t.Fatal(err)
	}
	if running.Done != (<-chan struct{})(stop) {
		t.Fatal("a write of the running worker on jobs does not end when the worker stops")
	}
	if database.Gate == nil || database.Gate != queuestore.FileGate(path) {
		t.Fatalf("the runs of jobs are written under gate %p, not the file's", database.Gate)
	}
	if database.Waiting == nil {
		t.Fatal("a write of the runs of jobs waits for the file and the server's output is not told")
	}
	if err := database.Read(func(db queuestore.SQL) error { return db.QueryRow(`SELECT COUNT(*) FROM kitwork_runs`).Scan(new(int)) }); err != nil {
		t.Fatal(err)
	}
	if idle := database.Pool.Stats().Idle; idle == 0 {
		t.Fatalf("after a read the pool of jobs keeps %d idle connections", idle)
	}
	said := captureOutput(t, func() { running.Waiting(5 * time.Second) })
	if want := "[Schedule] kitwork_runs của database jobs: một lần ghi chờ khoá ghi của file đã 5s, một câu khác đang ghi file"; !strings.Contains(said, want) {
		t.Fatalf("waiting said %q, want %q", said, want)
	}
}

// While the worker runs, its writes on a database of the catalogue wait for another writer of the file (here a
// claim; a sqlite shell after BEGIN IMMEDIATE, or another process, would hold it) in short tries, the wait
// said on the server's output once each 5s, and the claim is not given up. The app closing ends the wait at
// once: the close does not wait for the file to be let go (KITWORK_QUEUE.md mục 16).
//
// CONTROL (24/10/2026): with startQueueRuntime handing the poller the store of runsStoreAt (no stop), the
// claim gave up at the busy timeout and the wait was never said ("[Queue] claim: database is locked (5)");
// with Immediate.Write not reading Done, "the app's close still waited 2s while another writer held the
// file"; with WaitingEvery not read, the wait was said at each try: "the wait was said 47 times in 5.5s".
func TestRunsWorkerWaitsForAHeldFileUntilTheAppCloses(t *testing.T) {
	path := jobsCatalogue(t)
	q := newQueueApp(t, runsApp(queueInJobs, ""))
	holder, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?_pragma=busy_timeout(50)")
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	held, err := holder.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer held.Rollback()
	if _, err := held.Exec(`UPDATE kitwork_runs SET status = status WHERE 0`); err != nil {
		t.Fatal(err)
	}
	var closeTook time.Duration
	// the app closes before the output is read: no goroutine of the worker prints after it
	said := captureOutput(t, func() {
		q.app.queueWorker().noteDue(time.Now())
		time.Sleep(5500 * time.Millisecond)
		start, closed := time.Now(), make(chan struct{})
		go func() {
			q.close()
			close(closed)
		}()
		select {
		case <-closed:
		case <-time.After(2 * time.Second):
			held.Rollback()
			<-closed
		}
		closeTook = time.Since(start)
	})
	const line = "[Queue] kitwork_runs của database jobs: một lần ghi chờ khoá ghi của file đã "
	if !strings.Contains(said, line+"5s") {
		t.Fatalf("the worker's claim on a held file did not wait, want %q:\n%s", line+"5s", said)
	}
	if times := strings.Count(said, line); times != 1 {
		t.Fatalf("the wait was said %d times in 5.5s, want once each 5s:\n%s", times, said)
	}
	if closeTook >= 2*time.Second {
		t.Fatal("the app's close still waited 2s while another writer held the file")
	}
}

// The runs follow a load of the folder again with another database (LoadQueueFiles, LoadScheduleFiles on the
// same app runtime; the engine does not load them again on a change of a file yet): the worker stops, then
// runs on the new database, and a run stored before stays where it is. queue.runs() reads the database chosen
// now; back to the app's database, the first run is read again. The schedules' clock moves the same way.
//
// CONTROL (24/10/2026): with LoadQueueFiles not storing the place on the worker, the load started it on the
// app's database again ("the load did not start the worker on jobs": it said "worker up (kitwork_runs của
// .database/application/)"); with LoadScheduleFiles not storing it, "the load did not start the clock on jobs".
func TestRunsDatabaseMovesWithALoadOfTheFolder(t *testing.T) {
	watch := watchQueue(t)
	path := jobsCatalogue(t)
	q := newQueueApp(t, runsApp("", ""))
	first := q.serve(t, http.MethodPost, "/", order("an@example.com", "1", "", ""))["id"].(string)
	watch.until(t, first, "completed")

	reload := func(config string) string {
		t.Helper()
		configFile := filepath.Join(q.root, "acme", ".shared", "queue", "config.work")
		if config == "" {
			if err := os.Remove(configFile); err != nil {
				t.Fatal(err)
			}
		} else if err := os.WriteFile(configFile, []byte(config), 0o644); err != nil {
			t.Fatal(err)
		}
		return captureOutput(t, q.app.LoadQueueFiles)
	}
	if said := reload(queueInJobs); !strings.Contains(said, `[Queue] worker up (kitwork_runs của database jobs) — 1 job cho "acme": camOn`) {
		t.Fatalf("the load did not start the worker on jobs:\n%s", said)
	}
	second := q.serve(t, http.MethodPost, "/", order("binh@example.com", "2", "", ""))["id"].(string)
	watch.until(t, second, "completed")
	if got := fileRows(t, path, `SELECT id FROM kitwork_runs`); strings.Join(got, ",") != second {
		t.Fatalf("runs in jobs = %v, want [%s]", got, second)
	}
	if got := appRows(t, q, `SELECT id FROM kitwork_runs WHERE kind = 'queue'`); strings.Join(got, ",") != first {
		t.Fatalf("runs in the app's database = %v, want [%s]: changing the database moves no run", got, first)
	}
	if runs, _ := q.app.queueStore().Runs(queuestore.Filter{}, 10, 0); len(runs) != 1 || runs[0].ID != second {
		t.Fatalf("queue.runs() on jobs = %v, want %s", runs, second)
	}
	if _, err := q.app.queueStore().Get(first); err != queuestore.ErrNotFound {
		t.Fatalf("the first run read on jobs: %v, want not found", err)
	}

	if said := reload(""); !strings.Contains(said, `[Queue] worker up (kitwork_runs của .database/application/kitwork.db)`) {
		t.Fatalf("the load did not start the worker on the app's database:\n%s", said)
	}
	if runs, _ := q.app.queueStore().Runs(queuestore.Filter{}, 10, 0); len(runs) != 1 || runs[0].ID != first {
		t.Fatalf("queue.runs() back on the app's database = %v, want %s", runs, first)
	}

	// the schedules' clock moves the same way, its state with it
	writeQueueApp(t, q.root, map[string]string{".schedule/config.work": scheduleInJobs})
	if said := captureOutput(t, q.app.LoadScheduleFiles); !strings.Contains(said, `[Schedule] đồng hồ chạy (kitwork_runs của database jobs) — 1 lịch cho "acme": beat`) {
		t.Fatalf("the load did not start the clock on jobs:\n%s", said)
	}
	if states := fileRows(t, path, `SELECT name FROM kitwork_schedules`); strings.Join(states, ",") != "beat" {
		t.Fatalf("kitwork_schedules of jobs = %v, want beat", states)
	}
}

// Changing the database never moves a run: when a kind's runs live in a database of the catalogue and the
// app's own database still holds runs of that kind that have not ended, boot says how many, that they stay
// there and nothing runs them. The runs stay as they were. Runs that ended (completed, cancelled, failed) are
// not counted. An app whose own database was never made has none, and none is made for the count.
//
// CONTROL (24/10/2026): with sayLeftBehind returning at once, boot said nothing of the three runs; with
// appSQLiteRunsLeftBehind (then appRunsLeftBehind) reading the app's file without looking whether it exists, the
// fresh app's .database/application/ was made; with it counting every status (WHERE kind = $1), boot said "còn 5
// lần chạy chưa xong của job" and "còn 3 … của lịch", the ended runs counted.
func TestRunsLeftInTheAppsDatabaseAreSaid(t *testing.T) {
	queueWatch, scheduleWatch := watchQueue(t), watchSchedule(t)
	// the schedules' clock stands mid-hour: beat (every hour) never falls due while the test runs, so the
	// runs counted are the test's own
	setClock(t, instant(t, "2026-10-23T03:30:00Z"))
	jobsCatalogue(t)
	q := newQueueApp(t, runsApp("", ""))
	// runs that ended, of each kind: completed (run by the app), cancelled, and a job's failed
	sent := q.serve(t, http.MethodPost, "/", order("an@example.com", "1", "", ""))["id"].(string)
	beat := q.serve(t, http.MethodPost, "/beat", url.Values{})["id"].(string)
	queueWatch.until(t, sent, "completed")
	scheduleWatch.until(t, beat, "completed")
	later := time.Now().Add(time.Hour)
	for _, run := range []struct {
		kind, name string
	}{{queueKind, "camOn"}, {scheduleKind, "beat"}} {
		store := q.app.runsStoreAt(run.kind, nil)
		stored, _, err := store.Enqueue(queuestore.NewRun{Name: run.name, MaxAttempts: 1, Available: later}, time.Now())
		if err == nil {
			_, err = store.Cancel(stored.ID, "", time.Now())
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	jobs := q.app.runsStoreAt(queueKind, nil)
	failing, _, err := jobs.Enqueue(queuestore.NewRun{Name: "camOn", MaxAttempts: 1, Available: later}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	claims, _, err := jobs.Claim(later, time.Hour, 1, nil)
	if err != nil || len(claims) != 1 || claims[0].ID != failing.ID {
		t.Fatalf("claim of the run to fail = %v (%v), want %s", claims, err, failing.ID)
	}
	if status, err := jobs.Fail(failing.ID, claims[0].Token, "TEST", "hỏng", "", time.Time{}, time.Now()); err != nil || status != queuestore.Failed {
		t.Fatalf("fail = %q (%v), want failed", status, err)
	}
	// runs that have not ended
	for _, run := range []struct {
		kind, name string
	}{{queueKind, "camOn"}, {queueKind, "camOn"}, {scheduleKind, "beat"}} {
		if _, _, err := q.app.runsStoreAt(run.kind, nil).Enqueue(queuestore.NewRun{Name: run.name, MaxAttempts: 1, Available: later}, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	q.close()
	writeQueueApp(t, q.root, map[string]string{".shared/queue/config.work": queueInJobs, ".schedule/config.work": scheduleInJobs})
	again := bootQueueApp(t, q.root)
	for _, line := range []string{
		"[Queue] .database/application/kitwork.db còn 2 lần chạy chưa xong của job (pending, running, cancelling): chúng ở lại đó và không chạy, vì queue.database(…) đặt các lần chạy ở database jobs; đổi database không dời lần chạy",
		"[Schedule] .database/application/kitwork.db còn 1 lần chạy chưa xong của lịch (pending, running, cancelling): chúng ở lại đó và không chạy, vì schedule.database(…) đặt các lần chạy ở database jobs; đổi database không dời lần chạy",
	} {
		if !strings.Contains(again.output, line) {
			t.Errorf("boot did not say %q:\n%s", line, again.output)
		}
	}
	if got := appRows(t, again, `SELECT kind, status FROM kitwork_runs WHERE status = 'pending' ORDER BY seq`); strings.Join(got, ",") != "queue pending,queue pending,schedule pending" {
		t.Errorf("the runs left = %v, want three pending, as they were", got)
	}

	again.close() // jobs keeps one app's runs: the fresh app takes it once this one is gone
	fresh := newQueueApp(t, map[string]string{
		".shared/schema/jobs.work":  jobsSchema,
		".shared/queue/config.work": queueInJobs,
		".shared/queue/camOn.work":  camOnJob,
		".shared/email.work":        emailLibrary,
	})
	if strings.Contains(fresh.output, "còn") || !strings.Contains(fresh.output, "[Queue] worker up (kitwork_runs của database jobs)") {
		t.Errorf("a fresh app said runs left, or its runs are not in jobs:\n%s", fresh.output)
	}
	if _, err := os.Stat(filepath.Join(fresh.root, "acme", ".database", "application")); !os.IsNotExist(err) {
		t.Errorf("the count made the app's database (%v)", err)
	}
}

// A database of the catalogue keeps the runs of one app: kitwork_runs carries no app, so a second app's worker
// would claim the first's runs. A second app that names the same database is said at boot, and its runs stay
// in its own database; once the first app closes, its pool of the file closes with it (the process holds the
// file no more), and the file is free for another.
//
// CONTROL (24/10/2026): with useRunsPlace not looking for another app on the file, the second app booted
// saying nothing and its worker ran on jobs ("want the second refused"); with catalogRunsRelease.Close
// giving nothing back, the third app was refused after the first had closed; with catalogRuns opening a pool
// of its own (config.Connect, the file's lock taken) instead of the app's, the file was "still held after the
// app that kept its runs there closed".
func TestRunsDatabaseKeepsOneApp(t *testing.T) {
	watch := watchQueue(t)
	path := jobsCatalogue(t)
	files := func() map[string]string { return runsApp(queueInJobs, "") }
	first := newQueueApp(t, files())
	sent := first.serve(t, http.MethodPost, "/", order("an@example.com", "1", "", ""))["id"].(string)
	watch.until(t, sent, "completed")
	second := newQueueApp(t, files())
	refused := `[Queue] .shared/queue/config.work:3:6: database jobs đã giữ các lần chạy của app acme: kitwork_runs không ghi app nào, nên mỗi database của danh mục giữ các lần chạy của một app; host khai một tên khác cho app này. Các lần chạy nằm trong database của app (.database/application/kitwork.db)`
	if strings.Contains(first.output, "đã giữ") || !strings.Contains(second.output, refused) {
		t.Fatalf("the first app said:\n%s\nthe second:\n%s\nwant the second refused: %s", first.output, second.output, refused)
	}
	if !strings.Contains(second.output, `[Queue] worker up (kitwork_runs của .database/application/kitwork.db)`) || second.app.queueWorker().runs.Load() != nil {
		t.Fatalf("the second app's runs are not in its own database:\n%s", second.output)
	}
	first.close()
	// the file's pool was the app's, and closed with it: this process holds the file no more
	dbLocksMu.Lock()
	_, held := dbLocks[filepath.Clean(path)]
	dbLocksMu.Unlock()
	if held {
		t.Fatalf("%s is still held after the app that kept its runs there closed", path)
	}
	third := newQueueApp(t, files())
	if strings.Contains(third.output, "đã giữ") || !strings.Contains(third.output, `[Queue] worker up (kitwork_runs của database jobs)`) {
		t.Fatalf("after the first app closed, the third said:\n%s", third.output)
	}
}

// What reaches queue.database(…) or schedule.database(…) through another name, which the compiler does not
// follow, is checked as config.work runs: a value that is not a database, or none, is said with where it is
// written, and the runs stay in the app's database. A database of connect(…) is refused the same way;
// connection() is the default said again, nothing said.
//
// CONTROL (24/10/2026): with readRunsDatabase taking any value for the app's database, nothing said, boot said
// nothing of q.database(1) nor of q.database(), and the database of connect(…) was taken as the app's, said of
// nothing; with it not reading connection() as the app's database, connection() "gave <nil>, refused true" and
// said "queue.database(…) nhận tên import của một database …".
func TestRunsDatabaseRefusedAsItRuns(t *testing.T) {
	const stays = "; các lần chạy nằm trong database của app (.database/application/kitwork.db)"
	for _, each := range []struct {
		config, said string
	}{
		{"import * as queue from \"kitwork/queue\";\nconst q = queue;\nq.database(1);\n",
			"[Queue] .shared/queue/config.work:3:2: queue.database(…) nhận tên import của một database khai trong .shared/schema/ (export const db = connection().tables({ … })): import { db } from \"app/.shared/schema/<tên>.work\"; queue.database(db); giá trị này không phải database đó" + stays},
		{"import * as queue from \"kitwork/queue\";\nconst q = queue;\nq.database();\n",
			"[Queue] .shared/queue/config.work:3:2: queue.database(…) nhận đúng một database: queue.database(db), db là tên import của một database khai trong .shared/schema/" + stays},
	} {
		q := newQueueApp(t, runsApp(each.config, ""))
		if !strings.Contains(q.output, each.said) || q.app.queueWorker().runs.Load() != nil {
			t.Errorf("boot did not say %q:\n%s", each.said, q.output)
		}
	}

	external := value.Value{K: value.Proxy, V: &ExternalDatabase{connector: "postgresql"}}
	said := captureOutput(t, func() {
		if place, refused := readRunsDatabase(folderSetting{word: "database", args: []value.Value{external}}, "schedule", "[Schedule]", ".schedule/config.work:3:10"); place != nil || !refused {
			t.Errorf("a database of connect(…) gave %v, refused %v", place, refused)
		}
	})
	if want := `[Schedule] .schedule/config.work:3:10: schedule.database(…): database khai bằng connect(…) là database ngoài, engine không tạo bảng ở đó`; !strings.Contains(said, want) {
		t.Errorf("a database of connect(…) said %q, want %q", said, want)
	}
	application := value.Value{K: value.Proxy, V: &ManagedDatabase{proxy: &dbProxy{engine: "kitdb", dbName: kitDBApplicationName}}}
	said = captureOutput(t, func() {
		if place, refused := readRunsDatabase(folderSetting{word: "database", args: []value.Value{application}}, "queue", "[Queue]", ".shared/queue/config.work:3:7"); place != nil || refused {
			t.Errorf("connection() gave %v, refused %v, want the app's database", place, refused)
		}
	})
	if said != "" {
		t.Errorf("connection() said %q", said)
	}
}

// The app's statements on the database its runs live in migrate the app's own tables there (a table made, a
// column added), never the engine's: kitwork_runs, kitwork_definitions and kitwork_schedules keep their
// columns, their indexes and their rows.
//
// CONTROL (24/10/2026): with SchemaTable.ensureTable migrating every table of the file, each one not declared
// read as a table with no column, the first insert into letters failed (500) with `bảng kitwork_definitions
// trên connection("jobs"): bỏ cột content_hash, bỏ cột kind, …`: the migration planned to drop the engine's
// columns.
func TestMigrationLeavesTheRunsTablesAlone(t *testing.T) {
	watch := watchQueue(t)
	path := jobsCatalogue(t)
	files := runsApp(queueInJobs, "")
	files["localhost/letter/router.work"] = `import * as router from "kitwork/router";
import { jobs } from "app/.shared/schema/jobs.work";
router.post((context) => {
  const email = context.request.form("email");
  return context.json(jobs.insert((letters) => ({ email })));
});
`
	q := newQueueApp(t, files)
	sent := q.serve(t, http.MethodPost, "/", order("an@example.com", "1", "", ""))["id"].(string)
	watch.until(t, sent, "completed")
	q.serve(t, http.MethodPost, "/letter", url.Values{"email": {"an@example.com"}})

	engine := func() []string {
		t.Helper()
		shape := fileRows(t, path, `SELECT type || ' ' || name || ' ' || COALESCE(sql, '') FROM sqlite_master WHERE name LIKE 'kitwork_%' ORDER BY name`)
		rows := fileRows(t, path, `SELECT id || ' ' || status || ' ' || seq FROM kitwork_runs`)
		definitions := fileRows(t, path, `SELECT kind || ' ' || name || ' ' || content_hash FROM kitwork_definitions`)
		return append(append(shape, rows...), definitions...)
	}
	before := engine()
	if len(before) < 6 {
		t.Fatalf("the engine's tables in jobs: %v", before)
	}

	q.close()
	writeQueueApp(t, q.root, map[string]string{".shared/schema/jobs.work": strings.Replace(jobsSchema, "email: text() }", "email: text(), note: text().nullable() }", 1)})
	again := bootQueueApp(t, q.root)
	again.serve(t, http.MethodPost, "/letter", url.Values{"email": {"binh@example.com"}})
	if columns := fileRows(t, path, `SELECT name FROM pragma_table_info('letters') ORDER BY cid`); strings.Join(columns, ",") != "id,email,note" {
		t.Fatalf("letters has %v, want note added by the migration", columns)
	}
	if after := engine(); strings.Join(after, "\n") != strings.Join(before, "\n") {
		t.Fatalf("the engine's tables changed:\nbefore %v\nafter  %v", before, after)
	}
}
