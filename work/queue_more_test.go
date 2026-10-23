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
// the handler argument of old (job.payload). The compiler refuses each where it is written on the
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
		"parallel":        "INVALID_ARGUMENT: queue.parallel đã bỏ: số lần chạy cùng lúc khai trong .shared/queue/config.work bằng const parallel = 2",
		"keep":            `INVALID_ARGUMENT: queue.keep đã bỏ: thời gian giữ lịch sử khai trong .shared/queue/config.work bằng const keep = "30d"`,
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

// .shared/queue/config.work sets the queue's parallel and keep with const; a wrong value or a name the
// queue has not is said at boot and its default kept.
func TestQueueConfigFile(t *testing.T) {
	q := newQueueApp(t, map[string]string{
		".shared/queue/config.work": "const parallel = 1;\nconst keep = \"7d\";\n",
		".shared/queue/mail.work":   "import * as queue from \"kitwork/queue\";\nexport const mail = queue.job((context) => 1);\n",
	})
	worker := q.app.queueWorker()
	worker.mu.Lock()
	parallel, keep := worker.parallel, worker.keep
	worker.mu.Unlock()
	if parallel != 1 || keep != 7*24*time.Hour {
		t.Fatalf("parallel %d keep %s, want 1 and 7 days", parallel, keep)
	}
	if handler := worker.handlerFor("mail"); handler == nil {
		t.Fatal("config.work was taken for a job file, or mail was not loaded")
	}

	wrong := newQueueApp(t, map[string]string{
		".shared/queue/config.work": "const parallel = 0;\nconst paralel = 2;\nconst keep = \"5m\";\nconst database = 1;\n",
		".shared/queue/mail.work":   "import * as queue from \"kitwork/queue\";\nexport const mail = queue.job((context) => 1);\n",
	})
	for _, line := range []string{
		"[Queue] .shared/queue/config.work: parallel là số nguyên từ 1 tới 100",
		"[Queue] .shared/queue/config.work: paralel không phải luật của hàng đợi: config.work khai const parallel, keep, database",
		`[Queue] .shared/queue/config.work: keep là một khoảng từ 1h, thời gian giữ lần chạy đã xong: const keep = "30d"`,
		"[Queue] .shared/queue/config.work: const database chưa có trong engine hôm nay",
	} {
		if !strings.Contains(wrong.output, line) {
			t.Errorf("boot did not say %q:\n%s", line, wrong.output)
		}
	}
	worker = wrong.app.queueWorker()
	worker.mu.Lock()
	defer worker.mu.Unlock()
	if worker.parallel != queueDefaultParallel || worker.keep != queueDefaultKeep {
		t.Errorf("a wrong config.work kept parallel %d keep %s, want the defaults", worker.parallel, worker.keep)
	}
}
