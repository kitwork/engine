package work

import (
	"net/http"
	"strings"
	"testing"
	"time"

	queuestore "github.com/kitwork/engine/utilities/queue"
)

// Every spelling the spec replaced is an error naming the new one (KITWORK_QUEUE.md mục 14, 16): the
// namespace's builders and reads, the handle's .success/.error, cancel and replay by a job's name, and
// the handler argument of old (job.payload); queue.parallel and queue.keep, calls of .shared/queue/config.work
// since 24/10/2026, are refused anywhere else, saying where they are written. The compiler refuses each where it is written on the
// namespace or a handle (compiler TestQueueOldSpellingsAreCompileErrors); here the namespace and the handle
// reach the line through a function, where the compiler cannot follow them, and the module refuses them as
// they run, with the same words; a queue.job made there has no name (only a file of .shared/queue/ names
// one), and its send is refused. The _queue/ folder is said once at boot and not run. CONTROL: camOn.send
// in the same route is a receipt.
func TestQueueOldSpellingsFail(t *testing.T) {
	watch := watchQueue(t)
	q := newQueueApp(t, map[string]string{
		"_queue/mail.kitwork.js":   "import { queue } from \"kitwork\";\nqueue.handle((job) => 1);\n",
		".shared/email.work":       emailLibrary,
		".shared/queue/camOn.work": camOnJob,
		".shared/queue/old.work":   "import * as queue from \"kitwork/queue\";\nexport const old = queue.job((job) => job.payload.name);\n",
		"localhost/old/router.work": `import * as router from "kitwork/router";
import * as queue from "kitwork/queue";
import { camOn } from "app/.shared/queue/camOn.work";
import { old } from "app/.shared/queue/old.work";
const said = (result) => (result.ok ? "ok" : result.error.code + ": " + result.error.message);
const reach = (value) => value;
const q = reach(queue);
const handle = reach(camOn);
router.get((context) => context.json({
  dispatch: said(safe(() => q.dispatch("camOn", {}))),
  handle: said(safe(() => q.handle((job) => 1))),
  retry: said(safe(() => q.retry(3))),
  parallel: said(safe(() => q.parallel(2))),
  keep: said(safe(() => q.keep("30d"))),
  jobs: said(safe(() => q.jobs("camOn"))),
  failed: said(safe(() => q.failed("camOn"))),
  replayName: said(safe(() => queue.replay("camOn"))),
  replayNameAndId: said(safe(() => queue.replay("camOn", 42))),
  cancelName: said(safe(() => queue.cancel("camOn"))),
  resend: said(safe(() => q.resend("r_x"))),
  send: said(safe(() => q.send("camOn", {}))),
  success: said(safe(() => handle.success(() => 1))),
  error: said(safe(() => handle.error(() => 1))),
  jobList: said(safe(() => handle.list())),
  runsWhere: said(safe(() => q.runs({ where: (run) => run.attempt > 1 }))),
  statusList: said(safe(() => q.status("failed").list())),
  stray: said(safe(() => q.job((context) => 1).send({}))),
  control: said(safe(() => camOn.send({ email: "a@b.c", orderId: 1 }))),
  old: old.send({ name: "an" }).id
}));
`,
	})
	if !strings.Contains(q.output, queueLegacyLine) {
		t.Errorf("boot did not say _queue/ is not read:\n%s", q.output)
	}
	if handler := q.app.queueWorker().handlerFor("mail"); handler != nil {
		t.Errorf("_queue/mail.kitwork.js registered %+v", handler)
	}
	got := q.serve(t, http.MethodGet, "/old", nil)
	for key, want := range map[string]string{
		"dispatch":        `INVALID_ARGUMENT: queue.dispatch đã bỏ: import { camOn } from "app/.shared/queue/camOn.work" rồi camOn.send(data, { delay, key }); biên nhận là { id, name, status }`,
		"handle":          "INVALID_ARGUMENT: queue.handle đã bỏ: job khai trong .shared/queue/<tên>.work bằng export const camOn = queue.job((context) => { … })",
		"retry":           "INVALID_ARGUMENT: queue.retry đã bỏ: lần thử thêm là luật của job, queue.job(fn).retry(3); chạy lại một lần chạy là queue.replay(id)",
		"parallel":        "INVALID_ARGUMENT: queue.parallel(…) chỉ viết trong .shared/queue/config.work, một dòng ở cấp file: queue.parallel(4); ở file khác nó không có nghĩa",
		"keep":            `INVALID_ARGUMENT: queue.keep(…) chỉ viết trong .shared/queue/config.work, một dòng ở cấp file: queue.keep("30d"); ở file khác nó không có nghĩa`,
		"jobs":            `INVALID_ARGUMENT: queue.jobs đã bỏ: các lần chạy là queue.runs() hay queue.status("failed").runs(), định nghĩa là queue.list()`,
		"failed":          `INVALID_ARGUMENT: queue.failed đã bỏ: queue.status("failed").runs()`,
		"replayName":      `INVALID_ARGUMENT: queue.replay("camOn"): replay nhận id của một lần chạy và tạo một lần chạy mới; chạy lại mọi lần thất bại của một job là đọc camOn.status("failed").runs() rồi replay(id) từng lần`,
		"replayNameAndId": `INVALID_ARGUMENT: queue.replay("camOn"): replay nhận id của một lần chạy và tạo một lần chạy mới; chạy lại mọi lần thất bại của một job là đọc camOn.status("failed").runs() rồi replay(id) từng lần`,
		"cancelName":      `INVALID_ARGUMENT: queue.cancel nhận id của một lần chạy ("r_…", từ biên nhận của send hay từ runs()), không phải tên job`,
		"resend":          "INVALID_ARGUMENT: queue.resend không có: send(data) gửi việc từ dữ liệu người gọi đưa, replay(id) tạo việc từ lịch sử",
		"send":            "INVALID_ARGUMENT: queue.send không có: gửi qua handle đã import, camOn.send(data), không gửi bằng tên chuỗi",
		"success":         "INVALID_ARGUMENT: .success đã bỏ: việc sau khi chạy xong viết ở cuối handler",
		"error":           `INVALID_ARGUMENT: .error đã bỏ: handler biết lần thử cuối qua context.job.isFinalAttempt; các lần chạy thất bại đọc bằng queue.status("failed").runs()`,
		"jobList":         "INVALID_ARGUMENT: .list() của một job không có: định nghĩa đọc bằng queue.list(), các lần chạy của job bằng .runs()",
		"runsWhere":       "INVALID_ARGUMENT: runs({ where }) không có: điều kiện viết trước hành động, queue.where(fn).runs()",
		"statusList":      `INVALID_ARGUMENT: queue.status("failed").list() không có: trạng thái chỉ có ở lần chạy, viết queue.status("failed").runs()`,
		"stray":           `INVALID_ARGUMENT: job chưa có tên: queue.job khai trong .shared/queue/<tên>.work và export, export const camOn = queue.job(fn); file khác import { camOn } from "app/.shared/queue/camOn.work" rồi camOn.send(data)`,
		"control":         "ok",
	} {
		if got[key] != want {
			t.Errorf("%s = %v\n want %s", key, got[key], want)
		}
	}
	// the handler argument of old: job.payload is context.payload, which says context.data()
	id, _ := got["old"].(string)
	watch.until(t, id, "failed")
	if run := q.run(t, id); run.ErrorCode != "INVALID_ARGUMENT" || !strings.HasSuffix(run.ErrorMessage, "context.payload đã bỏ: dữ liệu đã gửi là context.data()") {
		t.Errorf("job.payload in a handler: %s %q", run.ErrorCode, run.ErrorMessage)
	}
}

// A run of a job no file declares (renamed or removed since it was sent) ends failed, saying so, at once:
// it is not claimed and released for ever. CONTROL: the declared job of the same app completes.
func TestQueueUnknownJobDeadLetters(t *testing.T) {
	watch := watchQueue(t)
	q := newQueueApp(t, map[string]string{
		".shared/queue/known.work": "import * as queue from \"kitwork/queue\";\nexport const known = queue.job((context) => context.job.log(\"ok\")).retry(3);\n",
	})
	store := q.app.queueStore()
	ghost, _, err := store.Enqueue(queuestore.NewRun{Name: "ghost", Data: "{}", MaxAttempts: 4}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	q.app.wakeQueueWorker()
	if seen := watch.until(t, ghost.ID, "failed"); len(seen) != 1 {
		t.Fatalf("the run of no job settled as %v, want failed at once", seen)
	}
	if run := q.run(t, ghost.ID); run.Attempt != 1 || run.ErrorCode != "NOT_FOUND" ||
		run.ErrorMessage != "không có job ghost trong .shared/queue/ của app: job đã đổi tên hay đã xoá sau khi gửi" {
		t.Fatalf("run = %+v", run)
	}
	known, _, _ := store.Enqueue(queuestore.NewRun{Name: "known", Data: "{}", MaxAttempts: 1}, time.Now())
	q.app.wakeQueueWorker()
	watch.until(t, known.ID, "completed")
}

// A delayed run is not claimed before its delay is over. CONTROL: the same send without a delay is.
func TestQueueDelayHoldsRunBack(t *testing.T) {
	q := newQueueApp(t, map[string]string{
		".shared/email.work":         emailLibrary,
		".shared/queue/camOn.work":   camOnJob,
		"localhost/send/router.work": sendRoute,
	})
	q.app.StopQueueWorker()
	later := q.serve(t, http.MethodPost, "/send", order("an@example.com", "1", "", "1h"))
	run := q.run(t, later["id"].(string))
	if available, _ := time.Parse(time.RFC3339, run.AvailableAt); time.Until(available) < 59*time.Minute {
		t.Fatalf("a run delayed an hour is due at %s", run.AvailableAt)
	}
	store := q.app.queueStore()
	if claims, _, _ := store.Claim(time.Now(), time.Minute, 10, nil); len(claims) != 0 {
		t.Fatalf("claimed %d delayed runs, want 0", len(claims))
	}
	q.serve(t, http.MethodPost, "/send", order("an@example.com", "1", "", ""))
	if claims, _, _ := store.Claim(time.Now(), time.Minute, 10, nil); len(claims) != 1 {
		t.Fatalf("claimed %d runs without a delay, want 1", len(claims))
	}
}

// A run sent is kept in the app's file: the app stopped and booted again, the run sent before is run
// then, and its key is still held.
func TestQueueRunsSurviveARestart(t *testing.T) {
	watch := watchQueue(t)
	q := newQueueApp(t, map[string]string{
		".shared/email.work":         emailLibrary,
		".shared/queue/camOn.work":   camOnJob,
		"localhost/send/router.work": sendRoute,
	})
	q.app.StopQueueWorker()
	id := q.serve(t, http.MethodPost, "/send", order("an@example.com", "7", "order:7", ""))["id"].(string)
	q.close()

	again := bootQueueApp(t, q.root)
	watch.until(t, id, "completed")
	if run := again.run(t, id); !strings.Contains(run.Log, "gửi an@example.com đơn 7") {
		t.Fatalf("after the restart the run logged %q", run.Log)
	}
	if receipt := again.serve(t, http.MethodPost, "/send", order("an@example.com", "7", "order:7", "")); receipt["id"] != id || receipt["duplicate"] != true {
		t.Fatalf("the key after the restart: %v", receipt)
	}
}

// .shared/queue/config.work sets the queue's parallel and keep with calls, one per line (KITWORK_QUEUE.md mục
// 2, 24/10/2026); a wrong value is said at boot with where it is written, and its default kept;
// queue.database(…) is said, the engine has it not yet. A call the compiler could not follow (the namespace
// under another name) is the same error when it runs: a word called twice is said and the first kept, a chain
// stops the file, and the defaults hold. The const of 17/10 does not compile, and the defaults hold.
//
// CONTROL (24/10/2026): with Queue.OnInvoke handing parallel and keep to no collector, the first app booted
// with "[Queue] run .shared/queue/config.work: config.work:3:6: queue.parallel(…) chỉ viết trong
// .shared/queue/config.work …" and parallel 4 keep 720h0m0s; with readQueueConfig reading no keep, the first
// app ran keep 720h0m0s; with folderSettings.call keeping no word seen, the alias app said nothing of the
// second q.parallel and ran parallel 3; with folderCallResult taking a chain (giving nil), the chained app
// said nothing and ran parallel 1.
func TestQueueConfigFile(t *testing.T) {
	const head = "import * as queue from \"kitwork/queue\";\n"
	const mail = head + "export const mail = queue.job((context) => 1);\n"
	settings := func(q *queueApp) (int, time.Duration) {
		worker := q.app.queueWorker()
		worker.mu.Lock()
		defer worker.mu.Unlock()
		return worker.parallel, worker.keep
	}
	q := newQueueApp(t, map[string]string{
		".shared/queue/config.work": head + "\nqueue.parallel(1);\nqueue.keep(\"7d\");\n",
		".shared/queue/mail.work":   mail,
	})
	if parallel, keep := settings(q); parallel != 1 || keep != 7*24*time.Hour {
		t.Fatalf("parallel %d keep %s, want 1 and 7 days:\n%s", parallel, keep, q.output)
	}
	if handler := q.app.queueWorker().handlerFor("mail"); handler == nil {
		t.Fatal("config.work was taken for a job file, or mail was not loaded")
	}
	if strings.Contains(q.output, "config.work") {
		t.Errorf("boot said something of a config.work written right:\n%s", q.output)
	}

	wrong := newQueueApp(t, map[string]string{
		".shared/queue/config.work": head + "queue.parallel(0);\nqueue.keep(\"5m\");\nqueue.database(1);\n",
		".shared/queue/mail.work":   mail,
	})
	for _, line := range []string{
		"[Queue] .shared/queue/config.work:2:6: queue.parallel nhận một số nguyên từ 1 tới 100, số lần chạy cùng lúc trên một máy: queue.parallel(4)",
		`[Queue] .shared/queue/config.work:3:6: queue.keep nhận một khoảng từ "1h", thời gian giữ lần chạy đã xong: queue.keep("30d")`,
		"[Queue] .shared/queue/config.work:4:6: queue.database(…) chưa có trong engine hôm nay: các lần chạy nằm trong database của app (.database/application/)",
	} {
		if !strings.Contains(wrong.output, line) {
			t.Errorf("boot did not say %q:\n%s", line, wrong.output)
		}
	}
	if parallel, keep := settings(wrong); parallel != queueDefaultParallel || keep != queueDefaultKeep {
		t.Errorf("a wrong config.work kept parallel %d keep %s, want the defaults", parallel, keep)
	}

	// the namespace under another name: the compiler does not follow it, the module refuses as it runs
	alias := newQueueApp(t, map[string]string{
		".shared/queue/config.work": head + "const q = queue;\nq.parallel(2);\nq.parallel(3);\n",
		".shared/queue/mail.work":   mail,
	})
	if line := "[Queue] .shared/queue/config.work:4:2: queue.parallel gọi hai lần, dòng 3 và dòng 4: mỗi chữ một lần trong config.work, giữ một dòng"; !strings.Contains(alias.output, line) {
		t.Errorf("boot did not say %q:\n%s", line, alias.output)
	}
	if parallel, _ := settings(alias); parallel != 2 {
		t.Errorf("a word called twice ran parallel %d, want the first call's 2", parallel)
	}
	chained := newQueueApp(t, map[string]string{
		".shared/queue/config.work": head + "const q = queue;\nq.parallel(1).keep(\"7d\");\n",
		".shared/queue/mail.work":   mail,
	})
	if line := "[Queue] run .shared/queue/config.work: "; !strings.Contains(chained.output, line) ||
		!strings.Contains(chained.output, `queue.parallel(…).keep(…) nối tiếp: mỗi lời gọi của config.work một dòng, tách thành hai dòng: queue.parallel(…); queue.keep(…);`) {
		t.Errorf("boot did not refuse the chain:\n%s", chained.output)
	}
	if parallel, keep := settings(chained); parallel != queueDefaultParallel || keep != queueDefaultKeep {
		t.Errorf("a chained config.work kept parallel %d keep %s, want the defaults", parallel, keep)
	}

	old := newQueueApp(t, map[string]string{
		".shared/queue/config.work": "const parallel = 1;\n",
		".shared/queue/mail.work":   mail,
	})
	if line := "[Queue] compile .shared/queue/config.work: config.work:1:7: const parallel đã bỏ: số lần chạy cùng lúc trên một máy là lời gọi queue.parallel(4)"; !strings.Contains(old.output, line) {
		t.Errorf("boot did not say %q:\n%s", line, old.output)
	}
	if parallel, _ := settings(old); parallel != queueDefaultParallel {
		t.Errorf("the const of 17/10 ran parallel %d, want the default", parallel)
	}
}

// queue.parallel takes a whole number from 1 to 100, one argument (KITWORK_QUEUE.md mục 11): anything else is
// said at boot with where it is written, and the default kept; 100 itself is taken.
//
// CONTROL (24/10/2026): with readQueueConfig taking any number from 1 up, 101 ran parallel 101 and 2.5 ran
// parallel 2, neither said; with its ceiling at 99, queue.parallel(100) ran the default 4; with
// folderSetting.only taking the first of two arguments, queue.parallel(2, 3) ran parallel 2, not said.
func TestQueueParallelIsAWholeNumberFrom1To100(t *testing.T) {
	const head = "import * as queue from \"kitwork/queue\";\n"
	const said = "[Queue] .shared/queue/config.work:2:6: queue.parallel nhận một số nguyên từ 1 tới 100, số lần chạy cùng lúc trên một máy: queue.parallel(4)"
	parallel := func(q *queueApp) int {
		worker := q.app.queueWorker()
		worker.mu.Lock()
		defer worker.mu.Unlock()
		return worker.parallel
	}
	for _, call := range []string{"queue.parallel(101);", "queue.parallel(2.5);", `queue.parallel("4");`, "queue.parallel(2, 3);", "queue.parallel();"} {
		t.Run(call, func(t *testing.T) {
			q := newQueueApp(t, map[string]string{
				".shared/queue/config.work": head + call + "\n",
				".shared/queue/mail.work":   head + "export const mail = queue.job((context) => 1);\n",
			})
			if !strings.Contains(q.output, said) {
				t.Errorf("boot did not say %q:\n%s", said, q.output)
			}
			if got := parallel(q); got != queueDefaultParallel {
				t.Errorf("%s ran parallel %d, want the default %d", call, got, queueDefaultParallel)
			}
		})
	}
	q := newQueueApp(t, map[string]string{
		".shared/queue/config.work": head + "queue.parallel(100);\n",
		".shared/queue/mail.work":   head + "export const mail = queue.job((context) => 1);\n",
	})
	if got := parallel(q); got != 100 || strings.Contains(q.output, "config.work") {
		t.Errorf("queue.parallel(100) ran parallel %d, want 100, and boot said:\n%s", got, q.output)
	}
}

// A call of a folder's config.work the compiler could not follow, the namespace under another name, is refused
// as it runs where the compiler refuses it written plainly (KITWORK_QUEUE.md mục 2, KITWORK_SCHEDULE.md mục 2):
// the other folder's word, a call inside a function, a call a library makes. The file stops, and the defaults
// hold; the line names the file the call is written in.
//
// CONTROL (24/10/2026): with folderSettings.call refusing only a nil s (not another module's word), the
// queue's config.work ran the schedule's keep as the queue's (queue keep 48h0m0s) and the schedule's ran the
// queue's (schedule keep 48h0m0s), neither said; with no frame check, both calls inside a function were taken
// (parallel 2, schedule keep 48h0m0s), not said; with no file check, both library calls were refused as "là
// một dòng ở cấp file của config.work" instead of "chỉ viết trong .shared/queue/config.work". Before the two
// checks were written, the function and library calls were all taken (parallel 2, schedule keep 48h0m0s).
func TestFolderConfigRefusesWhatTheCompilerCannotFollow(t *testing.T) {
	const queueHead = "import * as queue from \"kitwork/queue\";\n"
	const scheduleHead = "import * as schedule from \"kitwork/schedule\";\n"
	const outsideQueue = "queue.parallel(…) chỉ viết trong .shared/queue/config.work, một dòng ở cấp file: queue.parallel(4); ở file khác nó không có nghĩa"
	settings := func(q *queueApp) (int, time.Duration, time.Duration) {
		queueWorker, scheduleWorker := q.app.queueWorker(), q.app.scheduleWorker()
		queueWorker.mu.Lock()
		parallel, queueKeep := queueWorker.parallel, queueWorker.keep
		queueWorker.mu.Unlock()
		scheduleWorker.mu.Lock()
		defer scheduleWorker.mu.Unlock()
		return parallel, queueKeep, scheduleWorker.keep
	}
	for _, each := range []struct {
		name  string
		files map[string]string
		said  string
	}{
		{"the schedule's keep in the queue's config.work", map[string]string{
			".shared/queue/config.work": scheduleHead + "const s = schedule;\ns.keep(\"2d\");\n",
		}, `[Queue] run .shared/queue/config.work: config.work:3:2: schedule.keep(…) chỉ viết trong .schedule/config.work, một dòng ở cấp file: schedule.keep("7d"); ở file khác nó không có nghĩa`},
		{"the queue's keep in the schedule's config.work", map[string]string{
			".schedule/config.work": queueHead + "const q = queue;\nq.keep(\"2d\");\n",
		}, `[Schedule] run .schedule/config.work: config.work:3:2: queue.keep(…) chỉ viết trong .shared/queue/config.work, một dòng ở cấp file: queue.keep("30d"); ở file khác nó không có nghĩa`},
		{"a queue call inside a function", map[string]string{
			".shared/queue/config.work": queueHead + "const q = queue;\nconst set = () => q.parallel(2);\nset();\n",
		}, "[Queue] run .shared/queue/config.work: config.work:3:20: queue.parallel(…) là một dòng ở cấp file của config.work: queue.parallel(4); không viết trong hàm, trong khối hay làm giá trị"},
		{"a schedule call inside a function", map[string]string{
			".schedule/config.work": scheduleHead + "const s = schedule;\nconst set = () => s.keep(\"2d\");\nset();\n",
		}, `[Schedule] run .schedule/config.work: config.work:3:20: schedule.keep(…) là một dòng ở cấp file của config.work: schedule.keep("7d"); không viết trong hàm, trong khối hay làm giá trị`},
		{"a library's function", map[string]string{
			".shared/settings.work":     "export const set = (q) => q.parallel(2);\n",
			".shared/queue/config.work": queueHead + "import { set } from \"app/.shared/settings.work\";\nset(queue);\n",
		}, "[Queue] run .shared/queue/config.work: ../settings.work:1:28: " + outsideQueue},
		{"a library's top level", map[string]string{
			".shared/settings.work":     queueHead + "const q = queue;\nq.parallel(2);\nexport const ready = true;\n",
			".shared/queue/config.work": "import { ready } from \"app/.shared/settings.work\";\n",
		}, "[Queue] run .shared/queue/config.work: ../settings.work:3:2: " + outsideQueue},
	} {
		t.Run(each.name, func(t *testing.T) {
			files := map[string]string{
				".shared/queue/mail.work":  queueHead + "export const mail = queue.job((context) => 1);\n",
				".schedule/heartbeat.work": scheduleHead + "export const heartbeat = schedule.every(\"1h\").run((context) => 1);\n",
			}
			for name, text := range each.files {
				files[name] = text
			}
			q := newQueueApp(t, files)
			if !strings.Contains(q.output, each.said) {
				t.Errorf("boot did not say %q:\n%s", each.said, q.output)
			}
			if parallel, queueKeep, scheduleKeep := settings(q); parallel != queueDefaultParallel || queueKeep != queueDefaultKeep || scheduleKeep != queueDefaultKeep {
				t.Errorf("ran parallel %d, queue keep %s, schedule keep %s; want the defaults", parallel, queueKeep, scheduleKeep)
			}
		})
	}
}
