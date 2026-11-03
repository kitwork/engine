package work

import (
	"database/sql"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/kitwork/engine/database"
	queuestore "github.com/kitwork/engine/utilities/queue"
)

const flakyJob = `import * as queue from "kitwork/queue";
export const flaky = queue.job((context) => {
  fail("lần " + context.job.attempt + " hỏng");
});
`

const readRoute = `import * as router from "kitwork/router";
import * as queue from "kitwork/queue";
import { camOn } from "app/.shared/queue/camOn.work";
import { flaky } from "app/.shared/queue/flaky.work";
const said = (result) => (result.ok ? result.value : result.error.code + ": " + result.error.message);
router.get((context) => context.json({
  list: queue.list(),
  first: queue.list(1),
  second: queue.list({ page: 2, limit: 1 }),
  runs: queue.runs(),
  camOn: camOn.runs(2),
  failed: queue.status("failed").runs(),
  camOnFailed: camOn.status("failed").runs(10),
  page: queue.runs({ page: 2, limit: 2 }),
  zero: said(safe(() => queue.runs(0))),
  status: said(safe(() => queue.status("done"))),
  where: said(safe(() => queue.where((run) => run.attempt >= 3).runs()))
}));
router.post((context) => {
  const request = context.request;
  if (request.form("job") === "flaky") {
    return context.json(flaky.send({ n: 1 }));
  }
  return context.json(camOn.send({ email: request.form("email"), orderId: 1 }));
});
`

// The read side (KITWORK_QUEUE.md mục 8): list() reads the definitions by name whether anything was sent
// or not; runs() the runs newest first, of the app or of one job, by status, a page at a time; where(fn)
// is UNSUPPORTED rather than a filter in memory; a wrong argument is an error.
func TestQueueListAndRuns(t *testing.T) {
	watch := watchQueue(t)
	q := newQueueApp(t, map[string]string{
		".shared/email.work":         emailLibrary,
		".shared/queue/camOn.work":   camOnJob,
		".shared/queue/flaky.work":   flakyJob,
		"localhost/read/router.work": readRoute,
	})
	empty := q.serve(t, http.MethodGet, "/read", nil)
	list, _ := empty["list"].([]any)
	if len(list) != 2 {
		t.Fatalf("list() before any send = %v, want camOn and flaky", empty["list"])
	}
	camOn, flaky := list[0].(map[string]any), list[1].(map[string]any)
	if camOn["name"] != "camOn" || camOn["retry"] != float64(3) || camOn["timeout"] != "30s" ||
		flaky["name"] != "flaky" || flaky["retry"] != float64(0) || flaky["timeout"] != nil {
		t.Fatalf("list() = %v", list)
	}
	if first := empty["first"].([]any); len(first) != 1 || first[0].(map[string]any)["name"] != "camOn" {
		t.Fatalf("list(1) = %v", first)
	}
	if second := empty["second"].([]any); len(second) != 1 || second[0].(map[string]any)["name"] != "flaky" {
		t.Fatalf("list({ page: 2, limit: 1 }) = %v", second)
	}
	if runs := empty["runs"].([]any); len(runs) != 0 {
		t.Fatalf("runs() before any send = %v", runs)
	}

	var ids []string
	for _, email := range []string{"a@b.c", "d@e.f", "g@h.i"} {
		id := q.serve(t, http.MethodPost, "/read", url.Values{"email": {email}})["id"].(string)
		watch.until(t, id, "completed")
		ids = append(ids, id)
	}
	failedID := q.serve(t, http.MethodPost, "/read", url.Values{"job": {"flaky"}})["id"].(string)
	watch.until(t, failedID, "failed")

	read := q.serve(t, http.MethodGet, "/read", nil)
	idsOf := func(key string) string {
		var out []string
		for _, run := range read[key].([]any) {
			out = append(out, run.(map[string]any)["id"].(string))
		}
		return strings.Join(out, ",")
	}
	if got, want := idsOf("runs"), strings.Join([]string{failedID, ids[2], ids[1], ids[0]}, ","); got != want {
		t.Errorf("runs() = %s, want newest first %s", got, want)
	}
	if got, want := idsOf("camOn"), ids[2]+","+ids[1]; got != want {
		t.Errorf("camOn.runs(2) = %s, want %s", got, want)
	}
	if got := idsOf("failed"); got != failedID {
		t.Errorf(`queue.status("failed").runs() = %s, want %s`, got, failedID)
	}
	if got := idsOf("camOnFailed"); got != "" {
		t.Errorf(`camOn.status("failed").runs(10) = %s, want none: flaky's run is not camOn's`, got)
	}
	if got, want := idsOf("page"), ids[1]+","+ids[0]; got != want {
		t.Errorf("runs({ page: 2, limit: 2 }) = %s, want %s", got, want)
	}
	failed := read["failed"].([]any)[0].(map[string]any)
	failure, _ := failed["error"].(map[string]any)
	if failed["name"] != "flaky" || failed["status"] != "failed" || failed["attempt"] != float64(1) || failed["maxAttempts"] != float64(1) ||
		failure["code"] != "FAIL" || !strings.Contains(failure["message"].(string), "lần 1 hỏng") || failed["finishedAt"] == nil ||
		failed["data"].(map[string]any)["n"] != float64(1) || failed["replayOf"] != nil || failed["nextRunAt"] != nil {
		t.Errorf("a failed run reads %v", failed)
	}
	for key, want := range map[string]string{
		"zero":   "INVALID_ARGUMENT: runs(n): n là số nguyên từ 1 tới 1000",
		"status": "INVALID_ARGUMENT: queue.status nhận một trạng thái của lần chạy: pending, running, completed, failed, cancelling, cancelled",
		"where":  `UNSUPPORTED: queue.where(fn) chưa có trong engine hôm nay: điều kiện của where chạy ở tầng lưu trữ, không lọc trong bộ nhớ (KITWORK_QUEUE.md mục 8); lọc theo trạng thái là .status("failed")`,
	} {
		if read[key] != want {
			t.Errorf("%s = %v\n want %s", key, read[key], want)
		}
	}
}

const actRoute = `import * as router from "kitwork/router";
import * as queue from "kitwork/queue";
import { camOn } from "app/.shared/queue/camOn.work";
import { slow } from "app/.shared/queue/slow.work";
const said = (result) => (result.ok ? result.value : { failed: result.error.code + ": " + result.error.message });
router.post((context) => {
  const request = context.request;
  const op = request.form("op");
  const id = request.form("id");
  if (op === "send") {
    return context.json(camOn.send({ email: "a@b.c", orderId: 1 }, { delay: request.form("delay") }));
  }
  if (op === "slow") {
    return context.json(slow.send({}));
  }
  if (op === "cancel") {
    return context.json(said(safe(() => queue.cancel(id))));
  }
  if (op === "slowCancel") {
    return context.json(said(safe(() => slow.cancel(id))));
  }
  if (op === "camOnCancel") {
    return context.json(said(safe(() => camOn.cancel(id))));
  }
  return context.json(said(safe(() => queue.replay(id))));
});
`

// cancel(id) and replay(id) by the run's id (mục 9): a pending run is cancelled at once and asking again
// gives it as it is; a running one becomes cancelling, its attempt is stopped by the cancel itself, and it
// ends cancelled; replay makes a new run from an ended one's data, which runs; an ended run cannot be
// cancelled, a run not ended cannot be replayed; an unknown id, and another job's id through a handle, are
// not found. The lease is an hour here, so the heartbeat (a third of it) cannot be what stops the attempt.
func TestQueueCancelAndReplay(t *testing.T) {
	savedLease := queueLeaseTTL
	queueLeaseTTL = time.Hour
	t.Cleanup(func() { queueLeaseTTL = savedLease })
	watch := watchQueue(t)
	q := newQueueApp(t, map[string]string{
		".shared/email.work":       emailLibrary,
		".shared/queue/camOn.work": camOnJob,
		".shared/queue/slow.work": `import * as queue from "kitwork/queue";
export const slow = queue.job((context) => {
  let total = 0;
  for (let i = 0; i < 100000000; i++) {
    total = total + i;
  }
}).retry(2);
`,
		"localhost/act/router.work": actRoute,
	})
	q.app.MaxEnergy = 1 << 40
	act := func(op, id, delay string) map[string]any {
		return q.serve(t, http.MethodPost, "/act", url.Values{"op": {op}, "id": {id}, "delay": {delay}})
	}

	waiting := act("send", "", "1h")["id"].(string)
	if got := act("cancel", waiting, ""); got["id"] != waiting || got["status"] != "cancelled" || got["name"] != "camOn" {
		t.Fatalf("cancel of a pending run = %v", got)
	}
	if got := act("camOnCancel", waiting, ""); got["status"] != "cancelled" {
		t.Fatalf("cancel of a cancelled run = %v, want it as it is", got)
	}
	replayed := act("replay", waiting, "")
	newID, _ := replayed["id"].(string)
	if !strings.HasPrefix(newID, "r_") || newID == waiting || replayed["replayOf"] != waiting || replayed["status"] != "pending" || replayed["name"] != "camOn" {
		t.Fatalf("replay = %v", replayed)
	}
	watch.until(t, newID, "completed")
	if run := q.run(t, waiting); run.Status != "cancelled" {
		t.Fatalf("the replayed run became %s, want it kept cancelled", run.Status)
	}
	if run := q.run(t, newID); run.ReplayOf != waiting || !strings.Contains(run.Log, "gửi a@b.c đơn 1") {
		t.Fatalf("the replay = %+v", run)
	}

	if got := act("cancel", newID, ""); got["failed"] != `CONFLICT: queue.cancel("`+newID+`"): lần chạy đã completed, không còn gì để huỷ` {
		t.Errorf("cancel of a completed run = %v", got)
	}
	pending := act("send", "", "1h")["id"].(string)
	if got := act("replay", pending, ""); got["failed"] != `CONFLICT: queue.replay("`+pending+`"): lần chạy đang pending; chỉ chạy lại lần chạy đã xong (failed, cancelled, completed)` {
		t.Errorf("replay of a pending run = %v", got)
	}
	if got := act("cancel", "r_khongco", ""); got["failed"] != `NOT_FOUND: queue.cancel("r_khongco"): app không có lần chạy này` {
		t.Errorf("cancel of an unknown id = %v", got)
	}
	if got := act("replay", "r_khongco", ""); got["failed"] != `NOT_FOUND: queue.replay("r_khongco"): app không có lần chạy này (chưa từng gửi, hay đã dọn theo keep)` {
		t.Errorf("replay of an unknown id = %v", got)
	}
	if got := act("slowCancel", pending, ""); got["failed"] != `NOT_FOUND: slow.cancel("`+pending+`"): slow không có lần chạy này` {
		t.Errorf("slow.cancel of camOn's run = %v", got)
	}

	// running → cancelling → cancelled: the attempt stops at its VM's next checkpoint and is not retried
	running := act("slow", "", "")["id"].(string)
	if !waitFor(10*time.Second, func() bool { return q.run(t, running).Status == "running" }) {
		t.Fatalf("the slow run never started: %+v", q.run(t, running))
	}
	cancelled := time.Now()
	if got := act("slowCancel", running, ""); got["status"] != "cancelling" {
		t.Fatalf("cancel of a running run = %v, want cancelling", got)
	}
	if seen := watch.until(t, running, "cancelled"); len(seen) != 1 {
		t.Fatalf("the cancelled run settled as %v", seen)
	}
	if took := time.Since(cancelled); took > 5*time.Second {
		t.Fatalf("the attempt stopped %s after the cancel, want at once", took)
	}
	if run := q.run(t, running); run.Attempt != 1 || run.ErrorCode != "" || !strings.Contains(run.Log, "lần thử 1: dừng vì lần chạy bị huỷ") {
		t.Fatalf("the cancelled run = %+v", run)
	}
}

// The runs are the app's data, in its own database (the SQLite file .database/application/kitwork.db since
// 29/10/2026), never in a database of the host: with a system database connected (a host's
// app.postgresql({ alias: "system" }) is often a server other apps share), a send and its run leave nothing
// there. The engine's tables share the file with the app's own tables: the app still inserts and reads its
// orders, after the app booted again too, and its handle never takes the engine's tables in. CONTROL: the
// order and the run are both in the app's file.
func TestQueueRunsLiveInTheAppDatabase(t *testing.T) {
	system, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer system.Close()
	savedSystem, savedDriver := database.System, database.SystemDriver
	database.System, database.SystemDriver = system, "sqlite"
	t.Cleanup(func() { database.System, database.SystemDriver = savedSystem, savedDriver })

	watch := watchQueue(t)
	files := map[string]string{
		".shared/email.work":       emailLibrary,
		".shared/queue/camOn.work": camOnJob,
		".shared/schema/shop.work": `import { table, id, text, integer } from "kitwork/schema";
import { connection } from "kitwork/database";
const orders = table({ id: id(), email: text(), amount: integer() });
export const db = connection().tables({ orders });
`,
		"localhost/order/router.work": `import * as router from "kitwork/router";
import { db } from "app/.shared/schema/shop.work";
import { camOn } from "app/.shared/queue/camOn.work";
router.post((context) => {
  const email = context.request.form("email");
  const added = db.insert((orders) => ({ email, amount: 5 }));
  const receipt = camOn.send({ email: email, orderId: added.id });
  return context.json({ order: added.id, run: receipt.id });
});
router.get((context) => context.json({ orders: db.list((orders) => orders.amount > 0) }));
`,
	}
	q := newQueueApp(t, files)
	placed := q.serve(t, http.MethodPost, "/order", url.Values{"email": {"an@example.com"}})
	runID, _ := placed["run"].(string)
	watch.until(t, runID, "completed")
	if listed := q.serve(t, http.MethodGet, "/order", nil)["orders"].([]any); len(listed) != 1 {
		t.Fatalf("orders = %v", listed)
	}
	var tables int
	if err := system.QueryRow(`SELECT COUNT(*) FROM sqlite_master`).Scan(&tables); err != nil || tables != 0 {
		t.Fatalf("the system database has %d tables (%v), want none", tables, err)
	}

	var names []string
	err = q.app.runsDatabase(queueKind, nil, nil).Read(func(db queuestore.SQL) error {
		rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type = 'table' ORDER BY name`)
		if err != nil {
			return err
		}
		names, err = textRows(rows)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if joined := strings.Join(names, ","); !strings.Contains(joined, "orders") || !strings.Contains(joined, "kitwork_runs") || !strings.Contains(joined, "kitwork_definitions") {
		t.Fatalf("the app's file holds %v, want orders beside kitwork_runs and kitwork_definitions", names)
	}

	q.close()
	again := bootQueueApp(t, q.root)
	second := again.serve(t, http.MethodPost, "/order", url.Values{"email": {"binh@example.com"}})
	watch.until(t, second["run"].(string), "completed")
	if listed := again.serve(t, http.MethodGet, "/order", nil)["orders"].([]any); len(listed) != 2 {
		t.Fatalf("orders after the restart = %v", listed)
	}
	if runs, _ := again.app.queueStore().Runs(queuestore.Filter{}, 10, 0); len(runs) != 2 {
		t.Fatalf("runs after the restart = %d, want 2", len(runs))
	}

	// the app's handle never takes the engine's tables in as its own
	var proxy *dbProxy
	schemaRegMu.Lock()
	for key, registered := range schemaReg {
		if strings.HasPrefix(key, tenantScopeKey(again.site)+"|sqlite|"+applicationDatabaseName) {
			proxy = registered
		}
	}
	schemaRegMu.Unlock()
	if proxy == nil {
		t.Fatal("the site declared no app database")
	}
	for name := range proxy.tables {
		if strings.HasPrefix(name, "kitwork_") {
			t.Errorf("the app's handle holds the engine's table %s", name)
		}
	}
	if proxy.tables["orders"] == nil {
		t.Error("the app's handle lost orders")
	}
}
