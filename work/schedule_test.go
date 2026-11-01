package work

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kitwork/engine/app"
	"github.com/kitwork/engine/compiler"
	queuestore "github.com/kitwork/engine/utilities/queue"
)

// watchSchedule makes the worker of the app's schedules quick (a poll every 20 ms, a retry after 10 ms) and
// hears its settles (ScheduleSettled).
func watchSchedule(t *testing.T) *queueWatch {
	t.Helper()
	savedPoll, savedBackoff, savedHook := queuePollInterval, queueBackoff, ScheduleSettled
	queuePollInterval = 20 * time.Millisecond
	queueBackoff = func(int) time.Duration { return 10 * time.Millisecond }
	watch := &queueWatch{events: make(chan [2]string, 256), heard: map[string][]string{}}
	ScheduleSettled = func(_, id, status string) { watch.events <- [2]string{id, status} }
	t.Cleanup(func() { queuePollInterval, queueBackoff, ScheduleSettled = savedPoll, savedBackoff, savedHook })
	return watch
}

// testClock is the schedules' clock of a test: it reads what the test set, never the system's.
type testClock struct {
	now atomic.Int64
}

// setClock makes the schedules read at as the time until the test sets another.
func setClock(t *testing.T, at time.Time) *testClock {
	t.Helper()
	clock := &testClock{}
	clock.now.Store(at.UnixNano())
	t.Cleanup(SetScheduleClock(func() time.Time { return time.Unix(0, clock.now.Load()) }))
	return clock
}

func (c *testClock) set(at time.Time) { c.now.Store(at.UnixNano()) }

func instant(t *testing.T, text string) time.Time {
	t.Helper()
	at, err := time.Parse(time.RFC3339, text)
	if err != nil {
		t.Fatal(err)
	}
	return at
}

// runOf waits for the run of the schedule name due at at (context.job.at as the author reads it) to be
// stored, and gives it.
func runOf(t *testing.T, q *queueApp, name, at string) queuestore.Run {
	t.Helper()
	var found queuestore.Run
	if !waitFor(10*time.Second, func() bool {
		runs, err := q.app.scheduleStore().Runs(queuestore.Filter{Name: name}, 100, 0)
		if err != nil {
			return false
		}
		for _, run := range runs {
			if due, _ := scheduleRunOf(run.Data); due == at {
				found = run
				return true
			}
		}
		return false
	}) {
		runs, _ := q.app.scheduleStore().Runs(queuestore.Filter{}, 100, 0)
		t.Fatalf("no run of %s due at %s; the runs: %+v", name, at, runs)
	}
	return found
}

// scheduleRuns is every run of the schedule name, newest first.
func scheduleRuns(t *testing.T, q *queueApp, name string) []queuestore.Run {
	t.Helper()
	runs, err := q.app.scheduleStore().Runs(queuestore.Filter{Name: name}, 1000, 0)
	if err != nil {
		t.Fatal(err)
	}
	return runs
}

// listed is what list() gave, by name.
func listed(t *testing.T, answer map[string]any) map[string]map[string]any {
	t.Helper()
	out := map[string]map[string]any{}
	items, _ := answer["list"].([]any)
	for _, item := range items {
		schedule := item.(map[string]any)
		out[schedule["name"].(string)] = schedule
	}
	return out
}

const reportLibrary = `export const guiBaoCao = (at) => "báo cáo của " + at;
`

const camOnSchedulesJob = `import * as queue from "kitwork/queue";
export const camOn = queue.job((context) => { context.job.log("nhận " + context.data().kind); });
`

// The first block of KITWORK_SCHEDULE.md, word for word but its zone (router.timezone of the app is not
// read yet, so each schedule writes it) and what its handlers do: three schedules exported from one file,
// one of them handing off to a job of the queue with a key of its due time.
const baoCaoFile = `import * as schedule from "kitwork/schedule";
import { camOn } from "app/.shared/queue/camOn.work";
import { guiBaoCao } from "app/.shared/report.work";

export const baoCaoSang = schedule.daily("09:00").retry(3).timeout("10m").timezone("Asia/Ho_Chi_Minh").run((context) => {
  context.job.log(guiBaoCao(context.job.at));
});

export const nhacCuoiThang = schedule.lastday("09:00").before(3).timezone("Asia/Ho_Chi_Minh").run((context) => {
  camOn.send({ kind: "nhac-thanh-toan" }, { key: "nhac:" + context.job.at });
});

export const raMat = schedule.on("2026-12-01T09:00:00+07:00").once((context) => {
  context.job.log("mở bán");
});
`

// The read block of the spec, from a route of a site of the app: the schedules, the runs, the failed runs,
// one schedule's runs; a post runs baoCaoSang now.
const scheduleReadRoute = `import * as router from "kitwork/router";
import * as schedule from "kitwork/schedule";
import { baoCaoSang } from "app/.schedule/baoCao.work";
router.get((context) => context.json({
  list: schedule.list(),
  first: schedule.list(1),
  runs: schedule.runs(),
  failed: schedule.status("failed").runs(10),
  mine: baoCaoSang.runs()
}));
router.post((context) => context.json(baoCaoSang.now()));
`

// The spec's example boots and fires: each schedule is named by its export (baoCaoSang, not the file's
// baoCao), list() gives its rule and its next due time in its zone, the due time stores one run that the
// worker runs with context.job.at the time it was due, the hand-off to camOn sends one job keyed by that
// time, baoCaoSang.runs() reads only baoCaoSang's runs, and now() adds a run due when it is called. The
// clock is the test's: 23/10/2026 10:00 in Ho Chi Minh, a Friday, then 28/10 09:00, three days before the
// last of October. The run a due time stores carries the rule's .timeout, as now()'s does. CONTROL (validated
// by disabling the code): with the names the bundler gives not bound (Schedule.exports), the loader says each
// schedule is not exported and the app runs none; with the timeout left off the run a due time stores, the
// run has none (TimeoutMS 0) and no attempt of it is bounded.
func TestScheduleSpecExampleBootsAndFires(t *testing.T) {
	schedules := watchSchedule(t)
	jobs := watchQueue(t)
	clock := setClock(t, instant(t, "2026-10-23T10:00:00+07:00"))
	q := newQueueApp(t, map[string]string{
		".shared/report.work":        reportLibrary,
		".shared/queue/camOn.work":   camOnSchedulesJob,
		".schedule/baoCao.work":      baoCaoFile,
		"localhost/read/router.work": scheduleReadRoute,
	})
	if strings.Contains(q.output, "[Schedule] baoCao.work") {
		t.Fatalf("boot said a problem:\n%s", q.output)
	}
	if !strings.Contains(q.output, "[Schedule] đồng hồ chạy (kitwork_runs của .database/application/kitwork.db) — 3 lịch cho \"acme\": baoCaoSang, nhacCuoiThang, raMat") {
		t.Errorf("boot did not start the clock with the three schedules by their export:\n%s", q.output)
	}

	read := q.serve(t, http.MethodGet, "/read", nil)
	list := listed(t, read)
	for name, want := range map[string][5]string{
		"baoCaoSang":    {`daily("09:00")`, "recurring", "10m", "2026-10-24T09:00:00+07:00", "3"},
		"nhacCuoiThang": {`lastday("09:00").before(3)`, "recurring", "", "2026-10-28T09:00:00+07:00", "0"},
		"raMat":         {`on("2026-12-01T09:00:00+07:00")`, "once", "", "2026-12-01T09:00:00+07:00", "0"},
	} {
		got := list[name]
		timeout, _ := got["timeout"].(string)
		if got == nil || got["rule"] != want[0] || got["mode"] != want[1] || timeout != want[2] || got["next"] != want[3] ||
			got["retry"] != map[string]float64{"3": 3, "0": 0}[want[4]] || got["source"] != ".schedule/baoCao.work" {
			t.Errorf("list() %s = %v, want %v", name, got, want)
		}
	}
	if zone := list["baoCaoSang"]["timezone"]; zone != "Asia/Ho_Chi_Minh" || list["raMat"]["timezone"] != "+07:00" {
		t.Errorf("list() zones = %v, %v", zone, list["raMat"]["timezone"])
	}
	if first := read["first"].([]any); len(first) != 1 || first[0].(map[string]any)["name"] != "baoCaoSang" {
		t.Errorf("list(1) = %v", first)
	}
	if runs := read["runs"].([]any); len(runs) != 0 {
		t.Fatalf("runs() before any due time = %v", runs)
	}

	// 28/10 09:00 +07:00: both baoCaoSang (the last of the days missed) and nhacCuoiThang are due
	clock.set(instant(t, "2026-10-28T09:00:00+07:00"))
	report := runOf(t, q, "baoCaoSang", "2026-10-28T09:00:00+07:00")
	schedules.until(t, report.ID, "completed")
	reminder := runOf(t, q, "nhacCuoiThang", "2026-10-28T09:00:00+07:00")
	schedules.until(t, reminder.ID, "completed")
	if got := scheduleRuns(t, q, "baoCaoSang"); len(got) != 1 {
		t.Fatalf("baoCaoSang has %d runs after five days, want one: the due times missed run once, the last", len(got))
	}
	if done, _ := q.app.scheduleStore().Get(report.ID); done.Log != "báo cáo của 2026-10-28T09:00:00+07:00\n" || done.Attempt != 1 || done.MaxAttempts != 4 ||
		done.TimeoutMS != 600000 {
		t.Errorf("baoCaoSang's run = %+v, want its log of its due time, attempt 1 of 4, .timeout(\"10m\") on each attempt", done)
	}
	handed, err := q.app.queueStore().Runs(queuestore.Filter{Name: "camOn"}, 10, 0)
	if err != nil || len(handed) != 1 || handed[0].Key != "nhac:2026-10-28T09:00:00+07:00" {
		t.Fatalf("camOn's runs after the hand-off = %+v %v, want one keyed nhac:2026-10-28T09:00:00+07:00", handed, err)
	}
	jobs.until(t, handed[0].ID, "completed")

	read = q.serve(t, http.MethodGet, "/read", nil)
	mine := read["mine"].([]any)
	if len(mine) != 1 || mine[0].(map[string]any)["id"] != report.ID || mine[0].(map[string]any)["at"] != "2026-10-28T09:00:00+07:00" ||
		mine[0].(map[string]any)["trigger"] != "schedule" || mine[0].(map[string]any)["status"] != "completed" {
		t.Errorf("baoCaoSang.runs() = %v, want only its own run", mine)
	}
	if runs := read["runs"].([]any); len(runs) != 2 {
		t.Errorf("schedule.runs() = %d runs, want baoCaoSang's and nhacCuoiThang's", len(runs))
	}
	if next := listed(t, read)["nhacCuoiThang"]["next"]; next != "2026-11-27T09:00:00+07:00" {
		t.Errorf("nhacCuoiThang's next after it ran = %v, want 27/11, three days before the last of November", next)
	}

	// now(): a run outside the rule, due when it is called
	clock.set(instant(t, "2026-10-28T10:15:30+07:00"))
	receipt := q.serve(t, http.MethodPost, "/read", url.Values{})
	if receipt["name"] != "baoCaoSang" || receipt["status"] != "pending" || receipt["at"] != "2026-10-28T10:15:30+07:00" {
		t.Fatalf("baoCaoSang.now() = %v", receipt)
	}
	id := receipt["id"].(string)
	schedules.until(t, id, "completed")
	if run, _ := q.app.scheduleStore().Get(id); run.Log != "báo cáo của 2026-10-28T10:15:30+07:00\n" {
		t.Errorf("now()'s run logged %q, want its context.job.at the time now() was called", run.Log)
	}
	if read := q.serve(t, http.MethodGet, "/read", nil); read["mine"].([]any)[0].(map[string]any)["trigger"] != "now" {
		t.Errorf("the run of now() reads %v", read["mine"].([]any)[0])
	}
}

// Each problem of a schedule is said at boot with its file:line:column and leaves that schedule out (mục 6,
// 11): a schedule not exported, two schedules of one name, a schedule exported under two names, a rule whose
// value the compiler could not see that breaks mục 11, a .run() chained twice, an on() whose instant has
// passed, a handler that takes apart what is not job, two ends the compiler could not see (the handle the
// first end gives says it, and the file stops there: said as two ends, not as what the first end lacks, as
// the compiler says it), a folder _cron/ left from before. CONTROL: the schedule written right beside them
// runs. CONTROL (validated by disabling the code): with the handler's destructuring not checked, bad runs and
// boot says nothing of it; with the handle not knowing .run, boot says only "sau .once(…) không nối gì".
func TestScheduleBootErrors(t *testing.T) {
	setClock(t, instant(t, "2026-10-23T10:00:00Z"))
	q := newQueueApp(t, map[string]string{
		".schedule/good.work":        "import * as schedule from \"kitwork/schedule\";\nexport const good = schedule.every(\"1h\").run((context) => 1);\n",
		".schedule/unnamed.work":     "import * as schedule from \"kitwork/schedule\";\nschedule.every(\"1h\").run((context) => 1);\n",
		".schedule/twice.work":       "import * as schedule from \"kitwork/schedule\";\nexport const good = schedule.every(\"2h\").run((context) => 1);\n",
		".schedule/two.work":         "import * as schedule from \"kitwork/schedule\";\nconst one = schedule.every(\"1h\").run((context) => 1);\nexport const a = one;\nexport const b = one;\n",
		".schedule/value.work":       "import * as schedule from \"kitwork/schedule\";\nconst at = \"25:00\";\nexport const late = schedule.daily(at).run((context) => 1);\n",
		".schedule/past.work":        "import * as schedule from \"kitwork/schedule\";\nexport const past = schedule.on(\"2026-01-01T09:00:00+07:00\").once((context) => 1);\n",
		".schedule/unended.work":     "import * as schedule from \"kitwork/schedule\";\nexport const open = schedule.every(\"1h\");\n",
		".schedule/destructure.work": "import * as schedule from \"kitwork/schedule\";\nexport const bad = schedule.every(\"1h\").run(({ log }) => 1);\n",
		// a namespace passed through a function: the compiler does not see the chain, the loader says it
		".schedule/ends.work":        "import * as schedule from \"kitwork/schedule\";\nconst pass = (namespace) => namespace;\nexport const ends = pass(schedule).daily(\"09:00\").once().run((context) => 1);\n",
		".schedule/config.work":      "import * as schedule from \"kitwork/schedule\";\nschedule.keep(\"7d\");\nschedule.session(\"system\");\n",
		"_cron/heartbeat.kitwork.js": "cron.every(\"1h\").handle(() => {});\n",
	})
	for _, want := range []string{
		// each where schedule.<rule> is written: the dot of the word
		"[Schedule] unnamed.work:2:9: lịch không export: export const baoCaoSang = schedule.daily(\"09:00\").run(fn), tên lịch là tên export",
		"[Schedule] twice.work:2:29: lịch good đã khai ở good.work:2:29; tên lịch duy nhất trong app",
		"[Schedule] two.work:2:21: lịch a export thêm dưới tên b: một lịch, một tên",
		"[Schedule] value.work:3:29: giờ \"25:00\" sai: viết \"HH:MM\", từ \"00:00\" tới \"23:59\"",
		"[Schedule] past.work:2:29: schedule.on(\"2026-01-01T09:00:00+07:00\") đã qua lúc khai: thời điểm của once() phải ở tương lai",
		"[Schedule] unended.work:2:29: lịch open chưa kết thúc: nối .run(fn), hay .once(fn) cho đúng một lần",
		`[Schedule] .schedule/config.work:3:9: schedule.session nhận một object, danh tính dịch vụ của lịch: schedule.session({ role: "system" })`,
		"[Schedule] destructure.work:2:55: `log` không có trong `context` của một lịch: context có job; tên tách được: job",
		"[Schedule] run .schedule/ends.work: ends.work:3:57: .run và .once loại trừ nhau và đứng cuối: lịch đã kết thúc bằng .once(…), sau đó không nối gì",
		scheduleLegacyLine,
	} {
		if !strings.Contains(q.output, want) {
			t.Errorf("boot did not say %q:\n%s", want, q.output)
		}
	}
	if strings.Contains(q.output, ".once(handler) nhận một hàm") {
		t.Errorf("boot said what the first end lacks, not that two ends are written:\n%s", q.output)
	}
	worker := q.app.scheduleWorker()
	worker.mu.Lock()
	var names []string
	for _, entry := range worker.book.entries {
		names = append(names, entry.name+" "+entry.rule.Text())
	}
	keep := worker.keep
	worker.mu.Unlock()
	if strings.Join(names, ",") != `good every("1h")` {
		t.Fatalf("the clock runs %v, want only good, the first one declared", names)
	}
	if keep != 7*24*time.Hour {
		t.Errorf("config.work keep = %s, want 7d", keep)
	}
}

// .schedule/config.work sets how long an ended run is kept with schedule.keep(…), one call per line
// (KITWORK_SCHEDULE.md mục 2, 24/10/2026); schedule.session({ … }) is accepted and said, the engine has it not
// yet; schedule.database(db) of the app's own database (connection()) is the default said again, and boot says
// nothing of it; a keep under an hour is said with where it is written, and the default kept. The const of
// 17/10 does not compile, and the default holds.
//
// CONTROL (24/10/2026): with Schedule.OnInvoke handing the words to no collector, the first app booted with
// "[Schedule] run .schedule/config.work: config.work:4:9: schedule.keep(…) chỉ viết trong
// .schedule/config.work …" and keep 720h0m0s; with readScheduleConfig saying nothing of session, the first
// app said nothing of it; with readRunsDatabase not reading connection() as the app's database (24/10/2026),
// the first app said a line of schedule.database(db) at config.work:6.
func TestScheduleConfigFile(t *testing.T) {
	const head = "import * as schedule from \"kitwork/schedule\";\n"
	const heartbeat = head + "export const heartbeat = schedule.every(\"1h\").run((context) => 1);\n"
	keep := func(q *queueApp) time.Duration {
		worker := q.app.scheduleWorker()
		worker.mu.Lock()
		defer worker.mu.Unlock()
		return worker.keep
	}
	q := newQueueApp(t, map[string]string{
		".schedule/config.work":    head + "import { db } from \"app/.shared/schema/shop.work\";\n\nschedule.keep(\"2d\");\nschedule.session({ role: \"system\" });\nschedule.database(db);\n",
		".shared/schema/shop.work": "import { table, id, text } from \"kitwork/schema\";\nimport { connection } from \"kitwork/database\";\nconst notes = table({ id: id(), body: text() });\nexport const db = connection().tables({ notes });\n",
		".schedule/heartbeat.work": heartbeat,
	})
	if got := keep(q); got != 48*time.Hour {
		t.Fatalf("keep = %s, want 2 days:\n%s", got, q.output)
	}
	if line := "[Schedule] .schedule/config.work:5:9: schedule.session(…) chưa có trong engine hôm nay: lịch chạy không phiên"; !strings.Contains(q.output, line) {
		t.Errorf("boot did not say %q:\n%s", line, q.output)
	}
	if strings.Contains(q.output, "config.work:6:") || q.app.scheduleWorker().runs.Load() != nil {
		t.Errorf("schedule.database(db) of connection() is the app's database, said of nothing:\n%s", q.output)
	}

	wrong := newQueueApp(t, map[string]string{
		".schedule/config.work":    head + "schedule.keep(\"5m\");\n",
		".schedule/heartbeat.work": heartbeat,
	})
	if line := `[Schedule] .schedule/config.work:2:9: schedule.keep nhận một khoảng từ "1h", thời gian giữ lần chạy đã xong: schedule.keep("7d")`; !strings.Contains(wrong.output, line) {
		t.Errorf("boot did not say %q:\n%s", line, wrong.output)
	}
	if got := keep(wrong); got != queueDefaultKeep {
		t.Errorf("a keep of 5m ran keep %s, want the default", got)
	}

	old := newQueueApp(t, map[string]string{
		".schedule/config.work":    "const keep = \"2d\";\n",
		".schedule/heartbeat.work": heartbeat,
	})
	if line := `[Schedule] compile .schedule/config.work: config.work:1:7: const keep đã bỏ: thời gian giữ lần chạy đã xong là lời gọi schedule.keep("7d")`; !strings.Contains(old.output, line) {
		t.Errorf("boot did not say %q:\n%s", line, old.output)
	}
	if got := keep(old); got != queueDefaultKeep {
		t.Errorf("the const of 17/10 ran keep %s, want the default", got)
	}
}

// A file of .schedule/ in a folder of a site stops the site at boot (mục 2): the app's clock reads only the
// one at the app's root. CONTROL: the same site without it boots.
func TestScheduleFolderInASiteIsABootError(t *testing.T) {
	root := t.TempDir()
	writeAppTree(t, root, map[string]string{
		"acme/localhost/router.work":                `import * as router from "kitwork/router";` + "\n" + `router.get((context) => context.text("ok"));`,
		"acme/localhost/shop/router.work":           `import * as router from "kitwork/router";` + "\n" + `router.get((context) => context.text("shop"));`,
		"acme/localhost/shop/.schedule/report.work": "export const x = 1;\n",
	})
	boot := func() error {
		runtime := app.NewRuntime("acme")
		defer runtime.Close()
		siteRuntime, err := runtime.Site(root, "localhost")
		if err != nil {
			t.Fatal(err)
		}
		site := NewTenantWithRuntimeLayout(root, "localhost", RootLayoutMultiTenant, runtime, siteRuntime)
		defer site.Close()
		return site.Run()
	}
	err := boot()
	if err == nil || !strings.Contains(err.Error(), "/shop có .schedule/: site là web, .schedule/ là đồng hồ, .shared/queue/ là người làm") {
		t.Fatalf("a site with .schedule/: %v", err)
	}
	if err := os.RemoveAll(filepath.Join(root, "acme", "localhost", "shop", ".schedule")); err != nil {
		t.Fatal(err)
	}
	if err := boot(); err != nil {
		t.Fatalf("CONTROL: the site without .schedule/: %v", err)
	}
}

// context.job of a schedule's run is { id, name, attempt, maxAttempts, isFinalAttempt, at, log } (mục 7):
// at is the due time (every("30s") falls on :00 and :30), not when the run began. .retry(n) is n attempts
// more (mục 5: .retry(1) is two in all), each a run's attempt under the same id; failed when they are spent.
// CONTROL (validated by disabling the code): with the old cron's flat context (context.scheduledFor, no
// job), the handler fails and its run is failed; with .retry(n) as the total, the run fails at attempt 1.
func TestScheduleContextAndRetry(t *testing.T) {
	schedules := watchSchedule(t)
	clock := setClock(t, instant(t, "2026-10-23T03:00:05Z"))
	q := newQueueApp(t, map[string]string{
		".schedule/beat.work": `import * as schedule from "kitwork/schedule";
export const beat = schedule.every("30s").run(({ job }) => {
  job.log(job.name + " " + job.at + " " + job.attempt + "/" + job.maxAttempts + " " + job.isFinalAttempt + " " + (job.id === "" ? "no id" : "id"));
});
export const flaky = schedule.every("30s").retry(1).run((context) => {
  fail("lần " + context.job.attempt + "/" + context.job.maxAttempts);
});
`,
	})
	clock.set(instant(t, "2026-10-23T03:00:30Z"))
	beat := runOf(t, q, "beat", "2026-10-23T03:00:30Z")
	schedules.until(t, beat.ID, "completed")
	if run, _ := q.app.scheduleStore().Get(beat.ID); run.Log != "beat 2026-10-23T03:00:30Z 1/1 true id\n" {
		t.Errorf("beat logged %q, want its name, its due time, attempt 1/1", run.Log)
	}
	flaky := runOf(t, q, "flaky", "2026-10-23T03:00:30Z")
	if seen := schedules.until(t, flaky.ID, "failed"); strings.Join(seen, ",") != "pending,failed" {
		t.Errorf("flaky settled as %v, want one retry then failed", seen)
	}
	run, _ := q.app.scheduleStore().Get(flaky.ID)
	if run.Attempt != 2 || run.MaxAttempts != 2 || run.ErrorCode != "FAIL" || !strings.Contains(run.ErrorMessage, "lần 2/2") ||
		!strings.Contains(run.Log, "lần thử 1: FAIL") {
		t.Errorf("flaky's run = %+v, want two attempts, failed at the second", run)
	}
	if runs := scheduleRuns(t, q, "flaky"); len(runs) != 1 {
		t.Errorf("flaky has %d runs: a retry is the same run, not a due time of its own", len(runs))
	}
}

// .timeout(hạn) bounds each attempt of a schedule's run as it bounds a job's (mục 5): an attempt past it is
// failed TIMEOUT when its time is up and its VM stops. The loop runs some seconds if nothing stops it.
// CONTROL (validated by disabling the code): with the rule's timeout not stored on the run, the attempts run
// their whole loop and the test's deadline fails.
func TestScheduleTimeoutPerAttempt(t *testing.T) {
	schedules := watchSchedule(t)
	setClock(t, instant(t, "2026-10-23T03:00:05Z"))
	q := newQueueApp(t, map[string]string{
		".schedule/slow.work": `import * as schedule from "kitwork/schedule";
export const slow = schedule.every("1h").retry(1).timeout("200ms").run((context) => {
  let total = 0;
  for (let i = 0; i < 10000000; i++) {
    total = total + i;
  }
  context.job.log("xong " + total);
});
`,
		"localhost/now/router.work": `import * as router from "kitwork/router";
import { slow } from "app/.schedule/slow.work";
router.post((context) => context.json(slow.now()));
`,
	})
	q.app.MaxEnergy = 1 << 40 // the loop stops by its time, never by energy
	started := time.Now()
	id := q.serve(t, http.MethodPost, "/now", url.Values{})["id"].(string)
	if seen := schedules.until(t, id, "failed"); strings.Join(seen, ",") != "pending,failed" {
		t.Fatalf("settled as %v, want one retry then failed", seen)
	}
	if took := time.Since(started); took > 10*time.Second {
		t.Fatalf("two attempts of 200 ms took %s", took)
	}
	if run, _ := q.app.scheduleStore().Get(id); run.Attempt != 2 || run.ErrorCode != "TIMEOUT" ||
		!strings.Contains(run.ErrorMessage, `hết giờ: lần thử 2 chạy quá 200ms (.timeout("200ms"))`) {
		t.Fatalf("run = %+v", run)
	}
}

// .once() chooses its due time when it is first synced and keeps it (mục 5: "khởi động lại không chọn sang
// một lần tới khác"): booting the app again keeps it and stores nothing; at its time it runs once; after,
// list() has no next for it, and a boot after its time neither runs it again nor refuses its on(). A due time
// missed while the app was down runs at the next boot, once. CONTROL (validated by disabling the code): with
// the kept state ignored at sync, the once() of every("2h") chooses again at each boot, 08:00 at the boot
// after 04:00, and the due time it chose first never runs.
func TestScheduleOnceKeepsItsDueTimeAcrossRestarts(t *testing.T) {
	schedules := watchSchedule(t)
	clock := setClock(t, instant(t, "2026-10-23T03:10:00Z"))
	files := map[string]string{
		".schedule/once.work": `import * as schedule from "kitwork/schedule";
export const sau = schedule.every("2h").once((context) => context.job.log("một lần " + context.job.at));
export const ngay = schedule.on("2026-10-23T05:00:00Z").once((context) => context.job.log("đúng giờ"));
`,
		"localhost/read/router.work": `import * as router from "kitwork/router";
import * as schedule from "kitwork/schedule";
router.get((context) => context.json({ list: schedule.list() }));
`,
	}
	q := newQueueApp(t, files)
	nextOf := func(q *queueApp) map[string]any {
		list := listed(t, q.serve(t, http.MethodGet, "/read", nil))
		return map[string]any{"sau": list["sau"]["next"], "ngay": list["ngay"]["next"]}
	}
	if got := nextOf(q); got["sau"] != "2026-10-23T04:00:00Z" || got["ngay"] != "2026-10-23T05:00:00Z" {
		t.Fatalf("next at the first boot = %v", got)
	}
	q.close()

	clock.set(instant(t, "2026-10-23T03:50:00Z"))
	again := bootQueueApp(t, q.root)
	if got := nextOf(again); got["sau"] != "2026-10-23T04:00:00Z" {
		t.Fatalf("sau's due time after a restart = %v, want the one chosen at the first boot", got["sau"])
	}
	if runs, _ := again.app.scheduleStore().Runs(queuestore.Filter{}, 10, 0); len(runs) != 0 {
		t.Fatalf("a restart before the due time stored %+v", runs)
	}
	again.close()

	// down from 03:50 to 06:00: both due times passed while the app was down
	clock.set(instant(t, "2026-10-23T06:00:00Z"))
	third := bootQueueApp(t, q.root)
	if strings.Contains(third.output, "đã qua lúc khai") {
		t.Errorf("an on() synced before its time is refused after it:\n%s", third.output)
	}
	sau := runOf(t, third, "sau", "2026-10-23T04:00:00Z")
	schedules.until(t, sau.ID, "completed")
	ngay := runOf(t, third, "ngay", "2026-10-23T05:00:00Z")
	schedules.until(t, ngay.ID, "completed")
	if got := nextOf(third); got["sau"] != nil || got["ngay"] != nil {
		t.Errorf("next after the once() ran = %v, want none", got)
	}
	third.close()

	clock.set(instant(t, "2026-10-24T06:00:00Z"))
	fourth := bootQueueApp(t, q.root)
	time.Sleep(10 * queuePollInterval)
	if runs, _ := fourth.app.scheduleStore().Runs(queuestore.Filter{}, 10, 0); len(runs) != 2 {
		t.Errorf("a boot after the once() ran has %d runs, want the two it had", len(runs))
	}
}

// A schedule's handler that hands off to a job sends with a key of its due time (mục 10): an attempt retried
// after it sent sends nothing more. CONTROL: the same hand-off without a key sends twice.
func TestScheduleHandOffKeyHoldsAcrossRetries(t *testing.T) {
	schedules := watchSchedule(t)
	watchQueue(t)
	clock := setClock(t, instant(t, "2026-10-23T03:00:05Z"))
	q := newQueueApp(t, map[string]string{
		".shared/queue/keyed.work": "import * as queue from \"kitwork/queue\";\nexport const keyed = queue.job((context) => 1);\n",
		".shared/queue/loose.work": "import * as queue from \"kitwork/queue\";\nexport const loose = queue.job((context) => 1);\n",
		".schedule/handoff.work": `import * as schedule from "kitwork/schedule";
import { keyed } from "app/.shared/queue/keyed.work";
import { loose } from "app/.shared/queue/loose.work";
export const withKey = schedule.every("30s").retry(1).run((context) => {
  keyed.send({ n: 1 }, { key: "k:" + context.job.at });
  if (context.job.attempt === 1) {
    fail("sau khi gửi");
  }
});
export const withoutKey = schedule.every("30s").retry(1).run((context) => {
  loose.send({ n: 1 });
  if (context.job.attempt === 1) {
    fail("sau khi gửi");
  }
});
`,
	})
	clock.set(instant(t, "2026-10-23T03:00:30Z"))
	for _, name := range []string{"withKey", "withoutKey"} {
		run := runOf(t, q, name, "2026-10-23T03:00:30Z")
		if seen := schedules.until(t, run.ID, "completed"); strings.Join(seen, ",") != "pending,completed" {
			t.Fatalf("%s settled as %v, want a failed attempt then a completed one", name, seen)
		}
	}
	count := func(job string) int {
		runs, err := q.app.queueStore().Runs(queuestore.Filter{Name: job}, 10, 0)
		if err != nil {
			t.Fatal(err)
		}
		return len(runs)
	}
	if got := count("keyed"); got != 1 {
		t.Errorf("the job sent with the due time's key has %d runs, want 1", got)
	}
	if got := count("loose"); got != 2 {
		t.Errorf("CONTROL: the job sent without a key has %d runs, want 2", got)
	}
}

// The reads of mục 8 from a route: status("failed").runs(), handle.runs() of that schedule only, and the
// errors a namespace passed through a function reaches at run time: status(…).list(), jobs(), where(fn),
// a status the runs have not, now() with an argument. CONTROL: baoCaoSang's runs are not nhip's.
func TestScheduleReads(t *testing.T) {
	schedules := watchSchedule(t)
	clock := setClock(t, instant(t, "2026-10-23T03:00:05Z"))
	q := newQueueApp(t, map[string]string{
		".schedule/nhip.work": `import * as schedule from "kitwork/schedule";
export const nhip = schedule.every("30s").run((context) => context.job.log("nhịp " + context.job.at));
export const hong = schedule.every("1m").run((context) => { fail("hỏng"); });
`,
		"localhost/read/router.work": `import * as router from "kitwork/router";
import * as schedule from "kitwork/schedule";
import { nhip, hong } from "app/.schedule/nhip.work";
const pass = (namespace) => namespace;
const said = (result) => (result.ok ? result.value : result.error.code + ": " + result.error.message);
router.get((context) => context.json({
  failed: schedule.status("failed").runs(),
  nhip: nhip.runs(),
  hongFailed: hong.status("failed").runs(5),
  page: schedule.runs({ page: 2, limit: 1 }),
  statusList: said(safe(() => pass(schedule).status("failed").list())),
  jobs: said(safe(() => pass(schedule).jobs())),
  where: said(safe(() => schedule.where((run) => run.attempt > 1).runs())),
  status: said(safe(() => schedule.status("cancelled"))),
  now: said(safe(() => pass(nhip).now(1))),
  cron: said(safe(() => kitwork().cron)),
  handleList: said(safe(() => pass(nhip).list()))
}));
`,
	})
	clock.set(instant(t, "2026-10-23T03:01:00Z"))
	beat := runOf(t, q, "nhip", "2026-10-23T03:01:00Z")
	broken := runOf(t, q, "hong", "2026-10-23T03:01:00Z")
	schedules.until(t, beat.ID, "completed")
	schedules.until(t, broken.ID, "failed")

	read := q.serve(t, http.MethodGet, "/read", nil)
	ids := func(key string) string {
		var out []string
		for _, run := range read[key].([]any) {
			out = append(out, run.(map[string]any)["name"].(string)+":"+run.(map[string]any)["status"].(string))
		}
		sort.Strings(out)
		return strings.Join(out, ",")
	}
	if got := ids("failed"); got != "hong:failed" {
		t.Errorf(`schedule.status("failed").runs() = %s`, got)
	}
	if got := ids("nhip"); got != "nhip:completed" {
		t.Errorf("nhip.runs() = %s, want nhip's run alone", got)
	}
	if got := ids("hongFailed"); got != "hong:failed" {
		t.Errorf(`hong.status("failed").runs(5) = %s`, got)
	}
	if got := len(read["page"].([]any)); got != 1 {
		t.Errorf("runs({ page: 2, limit: 1 }) = %d runs", got)
	}
	failure := read["failed"].([]any)[0].(map[string]any)["error"].(map[string]any)
	if failure["code"] != "FAIL" || !strings.Contains(failure["message"].(string), "nhip.work:3:61: hỏng") {
		t.Errorf("the failed run's error = %v", failure)
	}
	for key, want := range map[string]string{
		"statusList": `INVALID_ARGUMENT: schedule.status("failed").list() không có: trạng thái chỉ có ở lần chạy, viết schedule.status("failed").runs()`,
		"jobs":       "INVALID_ARGUMENT: schedule.jobs() không có: các lịch là schedule.list(), các lần chạy là schedule.runs()",
		"where":      `UNSUPPORTED: schedule.where(fn) chưa có trong engine hôm nay: điều kiện của where chạy ở tầng lưu trữ, không lọc trong bộ nhớ (KITWORK_SCHEDULE.md mục 8); lọc theo trạng thái là .status("failed")`,
		"status":     "INVALID_ARGUMENT: schedule.status nhận một trạng thái của lần chạy: pending, running, completed, failed",
		"now":        "INVALID_ARGUMENT: nhip.now() không nhận tham số: chạy lịch ngay một lần, context.job.at là lúc gọi",
		"handleList": "INVALID_ARGUMENT: .list() không có trên lịch: các lịch đọc bằng schedule.list(), các lần chạy của lịch bằng .runs()",
		"cron":       "INVALID_ARGUMENT: " + compiler.CronRemoved,
	} {
		if read[key] != want {
			t.Errorf("%s = %v\n want %s", key, read[key], want)
		}
	}
}

// A schedule without .timezone(…) in an app that declares no router.timezone(…) in its config.work counts in
// UTC (mục 5): boot says so for each, with its file:line:column, naming the app's call, so the spec's first
// block copied as written does not fire at 09:00 UTC unseen. CONTROL: a schedule with its zone and an on(…),
// whose instant carries its offset, are not said; an app that declares its zone is
// TestScheduleCountsInTheAppsZone. CONTROL (validated by disabling the code): with the line not said, boot
// says nothing of sang.
func TestScheduleWithoutTimezoneIsSaidAtBoot(t *testing.T) {
	setClock(t, instant(t, "2026-10-23T10:00:00Z"))
	q := newQueueApp(t, map[string]string{
		".schedule/baoCao.work": `import * as schedule from "kitwork/schedule";
export const sang = schedule.daily("09:00").run((context) => 1);
export const zoned = schedule.daily("09:00").timezone("Asia/Ho_Chi_Minh").run((context) => 1);
export const launch = schedule.on("2026-12-01T09:00:00+07:00").once((context) => 1);
`,
	})
	want := `[Schedule] baoCao.work:2:29: lịch sang không viết .timezone(…), và app không khai router.timezone(…): đếm theo UTC; múi giờ của app viết router.timezone("Asia/Ho_Chi_Minh") trong config.work ở gốc app, của riêng lịch thì .timezone(…)`
	if !strings.Contains(q.output, want) {
		t.Errorf("boot did not say %q:\n%s", want, q.output)
	}
	if strings.Contains(q.output, "lịch zoned không viết") || strings.Contains(q.output, "lịch launch không viết") {
		t.Errorf("CONTROL: boot said a schedule with its zone counts in UTC:\n%s", q.output)
	}
}

// schedule.<rule>(…) outside .schedule/ is an error where the compiler cannot see it too (mục 2, 11): a
// namespace a route renames is the compiler's error when it runs, at the top of the route a boot error of the
// site, so the site does not boot quietly with a schedule nobody runs. CONTROL: the same renaming in a file of
// .schedule/ declares its schedule, and a route that imports that file evaluates it again without error.
// CONTROL (validated by disabling the code): with the place of the call not checked, the route's rule is a
// builder nobody runs, said nowhere, and the site boots; with every place refused, viaName is not declared.
func TestScheduleRuleOutsideTheFolderThroughAName(t *testing.T) {
	setClock(t, instant(t, "2026-10-23T10:00:00Z"))
	q := newQueueApp(t, map[string]string{
		".schedule/alias.work": `import * as schedule from "kitwork/schedule";
const s = schedule;
export const viaName = s.every("1h").timezone("UTC").run((context) => 1);
`,
		"localhost/read/router.work": `import * as router from "kitwork/router";
import * as schedule from "kitwork/schedule";
import { viaName } from "app/.schedule/alias.work";
const s = schedule;
const said = (result) => (result.ok ? "ok" : result.error.code + ": " + result.error.message);
router.get((context) => context.json({
  stray: said(safe(() => s.every("1m").run((context) => 1))),
  runs: said(safe(() => viaName.runs())),
  list: schedule.list().length
}));
`,
	})
	read := q.serve(t, http.MethodGet, "/read", nil)
	if want := "INVALID_ARGUMENT: " + fmt.Sprintf(compiler.ScheduleRuleOutside, "every"); read["stray"] != want {
		t.Errorf("a rule through a name in a route = %v\n want %s", read["stray"], want)
	}
	if read["runs"] != "ok" || read["list"] != float64(1) {
		t.Errorf("CONTROL: the schedule declared through a name in .schedule/: runs %v, list() has %v", read["runs"], read["list"])
	}

	// at the top of a route: the site does not boot
	root := t.TempDir()
	writeAppTree(t, root, map[string]string{
		"acme/localhost/router.work": `import * as router from "kitwork/router";
import * as schedule from "kitwork/schedule";
const s = schedule;
const stray = s.every("1m").run((context) => 1);
router.get((context) => context.text("ok"));
`,
	})
	runtime := app.NewRuntime("acme")
	defer runtime.Close()
	siteRuntime, err := runtime.Site(root, "localhost")
	if err != nil {
		t.Fatal(err)
	}
	site := NewTenantWithRuntimeLayout(root, "localhost", RootLayoutMultiTenant, runtime, siteRuntime)
	defer site.Close()
	if err := site.Run(); err == nil || !strings.Contains(err.Error(), "router.work:4:16: "+fmt.Sprintf(compiler.ScheduleRuleOutside, "every")) {
		t.Errorf("a site whose route declares a schedule through a name: %v", err)
	}
}

// A due time that comes while a run of the schedule has not ended is passed over, its last due time moved
// all the same (§13, 12.4: one run of a schedule at a time); a once() is never passed over. Each failed
// attempt waits an hour for its retry here, so a run stays pending while the test's clock moves. CONTROL: the
// once() due while its now() run waits is stored. CONTROL (validated by disabling the code): with no run
// passed over (skipBusy false), cham stores a second run at 03:01:00; with the once() passed over instead of
// the recurring schedule, motLan's due time is never stored.
func TestScheduleDueTimeWhileARunHasNotEndedIsPassedOver(t *testing.T) {
	schedules := watchSchedule(t)
	queueBackoff = func(int) time.Duration { return time.Hour } // restored by watchSchedule's cleanup
	clock := setClock(t, instant(t, "2026-10-23T03:00:05Z"))
	q := newQueueApp(t, map[string]string{
		".schedule/busy.work": `import * as schedule from "kitwork/schedule";
export const cham = schedule.every("30s").retry(1).timezone("UTC").run((context) => { fail("chậm"); });
export const motLan = schedule.every("30s").retry(1).timezone("UTC").once((context) => { fail("một lần"); });
`,
		"localhost/now/router.work": `import * as router from "kitwork/router";
import { motLan } from "app/.schedule/busy.work";
router.post((context) => context.json(motLan.now()));
`,
	})
	// motLan's now() run fails its first attempt and waits for its retry: a run of motLan has not ended
	waiting := q.serve(t, http.MethodPost, "/now", url.Values{})["id"].(string)
	schedules.until(t, waiting, "pending")

	clock.set(instant(t, "2026-10-23T03:00:30Z"))
	first := runOf(t, q, "cham", "2026-10-23T03:00:30Z")
	schedules.until(t, first.ID, "pending")
	once := runOf(t, q, "motLan", "2026-10-23T03:00:30Z")
	if once.ID == waiting {
		t.Fatalf("motLan's due time is its now() run")
	}

	// 03:01:00: cham's run of 03:00:30 waits for its retry, so this due time is passed over
	clock.set(instant(t, "2026-10-23T03:01:00Z"))
	if !waitFor(10*time.Second, func() bool {
		states, err := q.app.scheduleStore().ScheduleStates()
		return err == nil && states["cham"].LastAt.Equal(instant(t, "2026-10-23T03:01:00Z"))
	}) {
		states, _ := q.app.scheduleStore().ScheduleStates()
		t.Fatalf("cham's last due time = %v, want 03:01:00 passed over", states["cham"].LastAt)
	}
	if runs := scheduleRuns(t, q, "cham"); len(runs) != 1 || runs[0].ID != first.ID {
		t.Errorf("cham's runs after a due time while one waits = %+v, want the one of 03:00:30 alone", runs)
	}
}

// Due times a recurring schedule missed while the app was down run once, the last of them, as soon as it is
// back, its context.job.at the time it was due (§13, 12.3): its last due time handled is read back from
// kitwork_schedules, never counted from the boot. A boot again at the same time stores nothing more.
// CONTROL (validated by disabling the code): with a recurring schedule's last due time set to the boot's
// time, the boot after three days down stores nothing.
func TestScheduleMissedDueTimesRunOnceAfterARestart(t *testing.T) {
	schedules := watchSchedule(t)
	clock := setClock(t, instant(t, "2026-10-23T08:00:00Z"))
	q := newQueueApp(t, map[string]string{
		".schedule/sang.work": `import * as schedule from "kitwork/schedule";
export const sang = schedule.daily("09:00").timezone("UTC").run((context) => context.job.log("sáng " + context.job.at));
`,
	})
	if runs := scheduleRuns(t, q, "sang"); len(runs) != 0 {
		t.Fatalf("a boot before the first due time stored %+v", runs)
	}
	q.close()

	// down from 08:00 on 23/10 to 10:00 on 26/10: 09:00 of the 23rd, 24th, 25th and 26th missed
	clock.set(instant(t, "2026-10-26T10:00:00Z"))
	back := bootQueueApp(t, q.root)
	run := runOf(t, back, "sang", "2026-10-26T09:00:00Z")
	schedules.until(t, run.ID, "completed")
	if runs := scheduleRuns(t, back, "sang"); len(runs) != 1 {
		t.Errorf("sang has %d runs after four due times missed, want one, the last", len(runs))
	}
	if done, _ := back.app.scheduleStore().Get(run.ID); done.Log != "sáng 2026-10-26T09:00:00Z\n" {
		t.Errorf("the missed run logged %q, want its due time", done.Log)
	}
	back.close()

	again := bootQueueApp(t, q.root)
	time.Sleep(10 * queuePollInterval)
	if runs := scheduleRuns(t, again, "sang"); len(runs) != 1 {
		t.Errorf("a boot again at the same time has %d runs of sang, want the one", len(runs))
	}
}

// The words of the old cron's handler argument are errors naming context.job.… when a handler reads them,
// and context.data() says how a schedule sends a job (§13; mục 7). Read inside safe(), so the run completes
// and its log keeps what each said. CONTROL: context.job.at reads the due time. CONTROL (validated by
// disabling the code): with the old words not refused, scheduledFor and executionId read null and
// context.log(…) is "không phải hàm"; with data not refused, context.data() is "không phải hàm".
func TestScheduleOldContextWordsSayTheNewOnes(t *testing.T) {
	schedules := watchSchedule(t)
	clock := setClock(t, instant(t, "2026-10-23T03:00:05Z"))
	q := newQueueApp(t, map[string]string{
		".schedule/cu.work": `import * as schedule from "kitwork/schedule";
const said = (result) => (result.ok ? "ok " + result.value : result.error.code + ": " + result.error.message);
export const cu = schedule.every("30s").timezone("UTC").run((context) => {
  context.job.log(said(safe(() => context.scheduledFor)));
  context.job.log(said(safe(() => context.executionId)));
  context.job.log(said(safe(() => context.log("x"))));
  context.job.log(said(safe(() => context.data())));
  context.job.log(said(safe(() => context.job.at)));
});
`,
	})
	clock.set(instant(t, "2026-10-23T03:00:30Z"))
	run := runOf(t, q, "cu", "2026-10-23T03:00:30Z")
	schedules.until(t, run.ID, "completed")
	done, _ := q.app.scheduleStore().Get(run.ID)
	want := strings.Join([]string{
		"INVALID_ARGUMENT: context.scheduledFor không có ở lịch: context.job.at",
		"INVALID_ARGUMENT: context.executionId không có ở lịch: context.job.id",
		"INVALID_ARGUMENT: context.log không có ở lịch: context.job.log(…)",
		"INVALID_ARGUMENT: " + scheduleNoData,
		"ok 2026-10-23T03:00:30Z",
	}, "\n") + "\n"
	if done.Log != want {
		t.Errorf("the handler's log =\n%s\nwant\n%s", done.Log, want)
	}
}

// The state of a schedule whose file does not load is kept (§13): a once() keeps the due time it chose
// while its file does not compile, and runs it once the file is fixed. CONTROL (validated by disabling the
// code): with the state of a schedule no file declares removed even when its file did not load, the boot of
// the broken file deletes sau's row, the fixed file's once() chooses again, and the due time it chose first,
// 04:00, never runs.
func TestScheduleStateOfAFileThatDoesNotLoadIsKept(t *testing.T) {
	schedules := watchSchedule(t)
	clock := setClock(t, instant(t, "2026-10-23T03:10:00Z"))
	const good = `import * as schedule from "kitwork/schedule";
export const sau = schedule.every("2h").timezone("UTC").once((context) => context.job.log("một lần " + context.job.at));
`
	q := newQueueApp(t, map[string]string{".schedule/once.work": good})
	stateOf := func(q *queueApp) queuestore.ScheduleState {
		t.Helper()
		states, err := q.app.scheduleStore().ScheduleStates()
		if err != nil {
			t.Fatal(err)
		}
		return states["sau"]
	}
	if chosen := stateOf(q).OnceAt; !chosen.Equal(instant(t, "2026-10-23T04:00:00Z")) {
		t.Fatalf("sau chose %v, want 04:00", chosen)
	}
	q.close()

	file := filepath.Join(q.root, "acme", ".schedule", "once.work")
	if err := os.WriteFile(file, []byte(strings.Replace(good, "once((context) =>", "once((context) => {", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	clock.set(instant(t, "2026-10-23T03:30:00Z"))
	broken := bootQueueApp(t, q.root)
	if !strings.Contains(broken.output, "[Schedule] compile .schedule/once.work") {
		t.Fatalf("the broken file compiled:\n%s", broken.output)
	}
	if kept := stateOf(broken); kept.Name != "sau" || !kept.OnceAt.Equal(instant(t, "2026-10-23T04:00:00Z")) {
		t.Fatalf("sau's state while its file does not load = %+v, want the due time it chose kept", kept)
	}
	broken.close()

	if err := os.WriteFile(file, []byte(good), 0o644); err != nil {
		t.Fatal(err)
	}
	clock.set(instant(t, "2026-10-23T05:00:00Z"))
	fixed := bootQueueApp(t, q.root)
	run := runOf(t, fixed, "sau", "2026-10-23T04:00:00Z")
	schedules.until(t, run.ID, "completed")
}
