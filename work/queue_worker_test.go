package work

import (
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	queuestore "github.com/kitwork/engine/utilities/queue"
)

const laterJob = `import * as queue from "kitwork/queue";
export const later = queue.job((context) => context.job.log("xong")).retry(1);
`

const okJob = `import * as queue from "kitwork/queue";
export const ok = queue.job((context) => context.job.log("ok"));
`

// enqueue stores a run as send does, without a route.
func (q *queueApp) enqueue(t *testing.T, name string) string {
	t.Helper()
	run, _, err := q.app.queueStore().Enqueue(queuestore.NewRun{Name: name, Data: "{}", MaxAttempts: 2}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	q.app.wakeQueueWorker()
	return run.ID
}

// A job whose file does not load at boot (it does not compile, it fails as it runs, or the job's
// declaration has a problem) is not a job removed: its runs stay pending, no attempt spent, until the file
// loads again, and its definition stays in list(). Before, the worker claimed them and failed each at once
// as a run of no job (NOT_FOUND "đã đổi tên hay đã xoá"), the whole backlog of one bad deploy. CONTROL: the
// run of the job that loads is claimed in the same claim and completes, and the run waiting completes once
// the file is fixed.
func TestQueueJobWhoseFileFailsWaitsForTheFix(t *testing.T) {
	for _, broken := range []struct{ name, source, said string }{
		{"compile", "import * as queue from \"kitwork/queue\";\nexport const later = queue.job((context) => {;\n", "[Queue] compile .shared/queue/later.work"},
		{"run", "import * as queue from \"kitwork/queue\";\nfail(\"boom\");\nexport const later = queue.job((context) => 1);\n", "[Queue] run .shared/queue/later.work"},
		{"problem", strings.Replace(laterJob, ".retry(1)", ".retry(1).retry(2)", 1), ".retry nối hai lần"},
	} {
		t.Run(broken.name, func(t *testing.T) {
			watch := watchQueue(t)
			q := newQueueApp(t, map[string]string{".shared/queue/later.work": laterJob, ".shared/queue/ok.work": okJob})
			q.app.StopQueueWorker()
			waiting := q.enqueue(t, "later")
			q.close()

			writeQueueApp(t, q.root, map[string]string{".shared/queue/later.work": broken.source})
			again := bootQueueApp(t, q.root)
			for _, line := range []string{broken.said, "[Queue] job later chưa nạp được (file lỗi): các lần chạy của nó chờ"} {
				if !strings.Contains(again.output, line) {
					t.Errorf("boot did not say %q:\n%s", line, again.output)
				}
			}
			control := again.enqueue(t, "ok")
			watch.until(t, control, "completed")
			if run := again.run(t, waiting); run.Status != "pending" || run.Attempt != 0 || run.ErrorCode != "" {
				t.Fatalf("the run of the job that did not load = %+v, want pending, no attempt spent", run)
			}
			if definitions, _ := again.app.queueStore().Definitions(10, 0); len(definitions) != 2 || definitions[0].Name != "later" {
				t.Fatalf("definitions = %+v, want later kept beside ok", definitions)
			}
			again.close()

			writeQueueApp(t, q.root, map[string]string{".shared/queue/later.work": laterJob})
			fixed := bootQueueApp(t, q.root)
			watch.until(t, waiting, "completed")
			if run := fixed.run(t, waiting); run.Attempt != 1 || run.Log != "xong\n" {
				t.Fatalf("after the fix the run = %+v, want completed at its first attempt", run)
			}
		})
	}
}

const listReplayRoute = `import * as router from "kitwork/router";
import * as queue from "kitwork/queue";
const said = (result) => (result.ok ? result.value.status : result.error.code + ": " + result.error.message);
router.post((context) => context.json({
  list: queue.list().map((job) => job.name),
  replay: said(safe(() => queue.replay(context.request.form("id"))))
}));
`

// A job no file declares any more is not declared: a file removed leaves list() without it, and replay of
// its run is NOT_FOUND; the whole .shared/queue/ removed leaves list() empty and refuses replay of any run,
// though kitwork_definitions still holds what the last boot with the folder synced (no worker reads the
// folder to sync it). Before, list() still showed the jobs and replay stored a run no worker would claim.
// CONTROL: before the removal both replays are runs.
func TestQueueRemovedJobIsNotDeclared(t *testing.T) {
	watch := watchQueue(t)
	q := newQueueApp(t, map[string]string{
		".shared/email.work":         emailLibrary,
		".shared/queue/camOn.work":   camOnJob,
		".shared/queue/flaky.work":   flakyJob,
		"localhost/act/router.work":  listReplayRoute,
		"localhost/send/router.work": sendRoute,
	})
	act := func(q *queueApp, id string) map[string]any {
		return q.serve(t, http.MethodPost, "/act", url.Values{"id": {id}})
	}
	thanks := q.serve(t, http.MethodPost, "/send", order("an@example.com", "1", "", ""))["id"].(string)
	watch.until(t, thanks, "completed")
	failed := q.enqueue(t, "flaky")
	watch.until(t, failed, "pending")
	watch.until(t, failed, "failed")
	if got := act(q, thanks); got["replay"] != "pending" || strings.Join(names(got["list"]), ",") != "camOn,flaky" {
		t.Fatalf("control: %v", got)
	}
	q.close()

	os.Remove(filepath.Join(q.root, "acme", ".shared", "queue", "camOn.work"))
	os.RemoveAll(filepath.Join(q.root, "acme", "localhost", "send"))
	second := bootQueueApp(t, q.root)
	got := act(second, thanks)
	if strings.Join(names(got["list"]), ",") != "flaky" || got["replay"] != `NOT_FOUND: queue.replay("`+thanks+`"): job camOn không còn trong .shared/queue/, không chạy lại được` {
		t.Fatalf("camOn.work removed: %v", got)
	}
	second.close()

	os.RemoveAll(filepath.Join(q.root, "acme", ".shared", "queue"))
	third := bootQueueApp(t, q.root)
	got = act(third, failed)
	if list := names(got["list"]); len(list) != 0 || got["replay"] != `NOT_FOUND: queue.replay("`+failed+`"): job flaky không còn trong .shared/queue/, không chạy lại được` {
		t.Fatalf(".shared/queue/ removed: %v", got)
	}
}

func names(list any) []string {
	var out []string
	items, _ := list.([]any)
	for _, item := range items {
		out = append(out, item.(string))
	}
	return out
}

// An idle worker reads nothing and writes nothing: it knows when the next pending run is due (none, or a
// delayed one an hour away) and looks at its clock only, where it used to claim under the app database's
// writer gate every tick, a scan of every kept run. CONTROL: a run due now is claimed at once.
func TestQueueIdleWorkerWritesNothing(t *testing.T) {
	watch := watchQueue(t)
	q := newQueueApp(t, map[string]string{
		".shared/email.work":         emailLibrary,
		".shared/queue/camOn.work":   camOnJob,
		"localhost/send/router.work": sendRoute,
	})
	worker := q.app.queueWorker()
	resource, err := q.app.runsResource()
	if err != nil {
		t.Fatal(err)
	}
	idle := func(when string) {
		t.Helper()
		writes, polls := resource.writes.Load(), worker.polls.Load()
		if !waitFor(10*time.Second, func() bool { return worker.polls.Load() >= polls+10 }) {
			t.Fatalf("%s: the poller stopped", when)
		}
		if more := resource.writes.Load() - writes; more != 0 {
			t.Fatalf("%s: ten polls wrote %d times, want none", when, more)
		}
	}
	first := q.serve(t, http.MethodPost, "/send", order("an@example.com", "1", "", ""))["id"].(string)
	watch.until(t, first, "completed")
	idle("nothing pending")
	q.serve(t, http.MethodPost, "/send", order("an@example.com", "2", "", "1h"))
	idle("a run due in an hour")
	now := q.serve(t, http.MethodPost, "/send", order("an@example.com", "3", "", ""))["id"].(string)
	watch.until(t, now, "completed")
}

// A run sent after a restart is the newest: the number runs are ordered by goes on from the file's highest,
// read once when the file is opened, never from 1 again.
func TestQueueRunSentAfterARestartIsNewest(t *testing.T) {
	q := newQueueApp(t, map[string]string{
		".shared/email.work":         emailLibrary,
		".shared/queue/camOn.work":   camOnJob,
		"localhost/send/router.work": sendRoute,
	})
	q.app.StopQueueWorker()
	before := q.serve(t, http.MethodPost, "/send", order("an@example.com", "1", "", "1h"))["id"].(string)
	q.close()
	again := bootQueueApp(t, q.root)
	after := again.serve(t, http.MethodPost, "/send", order("an@example.com", "2", "", "1h"))["id"].(string)
	runs, err := again.app.queueStore().Runs(queuestore.Filter{}, 10, 0)
	if err != nil || len(runs) != 2 || runs[0].ID != after || runs[1].ID != before {
		t.Fatalf("runs = %v (%v), want %s then %s", runs, err, after, before)
	}
}

// A log line or an error that is not valid UTF-8 (the body of a page in another encoding) is kept with
// U+FFFD in its place: KitDB stores only UTF-8, and before, the settle failed, the run stayed running until
// its lease ran out and ran again. CONTROL: the text around the bad byte is kept.
func TestQueueTextThatIsNotUTF8IsKept(t *testing.T) {
	reachLoopback(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=latin1")
		w.Write([]byte("caf\xe9"))
	}))
	defer upstream.Close()
	watch := watchQueue(t)
	q := newQueueApp(t, map[string]string{
		".shared/queue/latin.work": `import * as queue from "kitwork/queue";
import * as http from "kitwork/http";
export const logs = queue.job((context) => context.job.log("trang: " + http.get("` + upstream.URL + `").send().text()));
export const fails = queue.job((context) => fail("trang: " + http.get("` + upstream.URL + `").send().text()));
`,
	})
	logs, fails := q.enqueue(t, "logs"), q.enqueue(t, "fails")
	watch.until(t, logs, "completed")
	if run := q.run(t, logs); run.Log != "trang: caf\uFFFD\n" {
		t.Fatalf("the log = %q", run.Log)
	}
	watch.until(t, fails, "pending")
	watch.until(t, fails, "failed")
	if run := q.run(t, fails); !strings.HasSuffix(run.ErrorMessage, "trang: caf\uFFFD") || !strings.Contains(run.Log, "trang: caf\uFFFD") {
		t.Fatalf("the failure = %q, log %q", run.ErrorMessage, run.Log)
	}
}

// parallel of config.work bounds the runs one machine runs at once: with parallel 1, a run due while
// another runs waits pending, claimed by no one, until the first settles. CONTROL: it then runs.
func TestQueueParallelBoundsTheRunsAtOnce(t *testing.T) {
	upstream := holdUpstream(t, 2)
	watch := watchQueue(t)
	q := newQueueApp(t, map[string]string{
		".shared/queue/config.work":  "const parallel = 1;\n",
		".shared/queue/held.work":    heldJob(upstream.server.URL, ""),
		"localhost/send/router.work": heldRoute,
	})
	worker := q.app.queueWorker()
	first := q.serve(t, http.MethodPost, "/send", url.Values{})["id"].(string)
	upstream.wait(t, 1)
	second := q.serve(t, http.MethodPost, "/send", url.Values{})["id"].(string)
	polls := worker.polls.Load()
	if !waitFor(10*time.Second, func() bool { return worker.polls.Load() >= polls+10 }) {
		t.Fatal("the poller stopped")
	}
	if run := q.run(t, second); run.Status != "pending" || len(upstream.arrived) != 0 {
		t.Fatalf("while the first runs the second = %s with %d more requests, want pending, none", run.Status, len(upstream.arrived))
	}
	upstream.release(1)
	watch.until(t, first, "completed")
	upstream.wait(t, 2)
	upstream.release(2)
	watch.until(t, second, "completed")
}

// The worker deletes the runs that ended longer ago than keep: the run is gone and its key free again.
// CONTROL: the run ended within keep stays.
func TestQueueWorkerDeletesRunsPastKeep(t *testing.T) {
	saved := queuePruneTicks
	queuePruneTicks = 1
	t.Cleanup(func() { queuePruneTicks = saved })
	watch := watchQueue(t)
	q := newQueueApp(t, map[string]string{
		".shared/email.work":         emailLibrary,
		".shared/queue/camOn.work":   camOnJob,
		"localhost/send/router.work": sendRoute,
	})
	old := q.serve(t, http.MethodPost, "/send", order("an@example.com", "1", "order:1", ""))["id"].(string)
	recent := q.serve(t, http.MethodPost, "/send", order("an@example.com", "2", "order:2", ""))["id"].(string)
	watch.until(t, old, "completed")
	watch.until(t, recent, "completed")
	if err := (appRuns{tenant: q.app}).Write(func(db *sql.DB) error {
		_, err := db.Exec(`UPDATE kitwork_runs SET finished_at = '2000-01-01T00:00:00.000Z' WHERE id = $1`, old)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if !waitFor(10*time.Second, func() bool {
		_, err := q.app.queueStore().Get(old)
		return errors.Is(err, queuestore.ErrNotFound)
	}) {
		t.Fatalf("the run that ended in 2000 is still kept: %+v", q.run(t, old))
	}
	if again := q.serve(t, http.MethodPost, "/send", order("an@example.com", "1", "order:1", "")); again["id"] == old || again["duplicate"] != nil {
		t.Fatalf("its key after the prune: %v, want a new run", again)
	}
	if run := q.run(t, recent); run.Status != "completed" {
		t.Fatalf("the run within keep = %+v", run)
	}
}

// The wait after a failed attempt doubles from one second, give or take a fifth, up to thirty minutes.
// CONTROL: the waits differ from one failure to the next (jitter).
func TestQueueBackoffDoublesWithJitter(t *testing.T) {
	for attempt := 1; attempt <= 14; attempt++ {
		base := time.Second << uint(attempt-1)
		if base > 30*time.Minute {
			base = 30 * time.Minute
		}
		lowest, highest := time.Duration(1<<62), time.Duration(0)
		for sample := 0; sample < 200; sample++ {
			wait := queueBackoff(attempt)
			if wait < base*8/10 || wait > base*12/10 {
				t.Fatalf("after attempt %d the wait is %s, want %s give or take a fifth", attempt, wait, base)
			}
			lowest, highest = min(lowest, wait), max(highest, wait)
		}
		if lowest == highest {
			t.Fatalf("after attempt %d every wait is %s: no jitter", attempt, lowest)
		}
	}
}

// context.job names the run and the attempt (mục 5): id, name, attempt, maxAttempts, isFinalAttempt,
// createdAt, startedAt, as the run records them.
func TestQueueContextJobNamesTheRun(t *testing.T) {
	watch := watchQueue(t)
	q := newQueueApp(t, map[string]string{
		".shared/queue/who.work": `import * as queue from "kitwork/queue";
export const who = queue.job((context) => {
  const job = context.job;
  context.job.log([job.id, job.name, job.attempt, job.maxAttempts, job.isFinalAttempt, job.createdAt, job.startedAt].join("|"));
}).retry(1);
`,
	})
	id := q.enqueue(t, "who")
	watch.until(t, id, "completed")
	run := q.run(t, id)
	if want := strings.Join([]string{run.ID, "who", "1", "2", "false", run.CreatedAt, run.StartedAt}, "|") + "\n"; run.Log != want || run.StartedAt == "" {
		t.Fatalf("context.job logged %q, want %q", run.Log, want)
	}
}

// The preflight says a _queue/ folder is not read any more, with the spelling that replaced it.
func TestCheckQueueFilesSaysQueueFolderIsNotRead(t *testing.T) {
	root := t.TempDir()
	writeAppTree(t, root, map[string]string{
		"acme/_queue/mail.kitwork.js":   "queue.handle((job) => 1);",
		"acme/shop.example/router.work": `import * as router from "kitwork/router";`,
	})
	issues := CheckQueueFiles(root, "acme", nil)
	if len(issues) != 1 || filepath.Base(issues[0].File) != "_queue" ||
		issues[0].Err.Error() != "_queue/ không còn đọc: khai job trong .shared/queue/<tên>.work bằng export const <tên> = queue.job(fn)" {
		t.Fatalf("issues = %v", issues)
	}
}
