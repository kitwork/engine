package work

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kitwork/engine/app"
	queuestore "github.com/kitwork/engine/utilities/queue"
)

// queueApp is an app acme on disk, booted as a host boots it: its app tenant (the loader of .shared/queue/
// and the worker) and its site localhost (the routes), sharing one app runtime.
type queueApp struct {
	root    string
	runtime *app.Runtime
	app     *Tenant
	site    *Tenant
	// output is what the boot printed.
	output string
}

// writeQueueApp writes files under the app's folder (paths from it, with "/").
func writeQueueApp(t *testing.T, root string, files map[string]string) {
	t.Helper()
	tree := make(map[string]string, len(files))
	for relative, content := range files {
		tree["acme/"+relative] = content
	}
	writeAppTree(t, root, tree)
}

// newQueueApp writes files into a new app and boots it.
func newQueueApp(t *testing.T, files map[string]string) *queueApp {
	t.Helper()
	root := t.TempDir()
	if _, site := files["localhost/router.work"]; !site {
		files["localhost/router.work"] = `import * as router from "kitwork/router";` + "\n" + `router.get((context) => context.text("ok"));`
	}
	writeQueueApp(t, root, files)
	return bootQueueApp(t, root)
}

// bootQueueApp boots the app acme of root, printing to a buffer the test reads as output.
func bootQueueApp(t *testing.T, root string) *queueApp {
	t.Helper()
	q := &queueApp{root: root, runtime: app.NewRuntime("acme")}
	q.output = captureOutput(t, func() {
		q.app = NewAppTenantWithRuntimeLayout(root, "acme", RootLayoutMultiTenant, q.runtime)
		if err := q.app.Run(); err != nil {
			t.Fatal(err)
		}
	})
	siteRuntime, err := q.runtime.Site(root, "localhost")
	if err != nil {
		t.Fatal(err)
	}
	q.site = NewTenantWithRuntimeLayout(root, "localhost", RootLayoutMultiTenant, q.runtime, siteRuntime)
	if err := q.site.Run(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(q.close)
	return q
}

func (q *queueApp) close() {
	q.site.Close()
	q.app.Close()
	q.runtime.Close()
}

// captureOutput runs fn and gives what it printed.
func captureOutput(t *testing.T, fn func()) (output string) {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stdout
	os.Stdout = writer
	var printed bytes.Buffer
	copied := make(chan struct{})
	go func() {
		io.Copy(&printed, reader)
		close(copied)
	}()
	defer func() {
		os.Stdout = saved
		writer.Close()
		<-copied
		output = printed.String()
	}()
	fn()
	return ""
}

// serve answers one request of the site; form, when not nil, is posted as a form.
func (q *queueApp) serve(t *testing.T, method, target string, form url.Values) map[string]any {
	t.Helper()
	var request *http.Request
	if form == nil {
		request = httptest.NewRequest(method, "http://localhost"+target, nil)
	} else {
		request = httptest.NewRequest(method, "http://localhost"+target, strings.NewReader(form.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	recorder := httptest.NewRecorder()
	q.site.Serve(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("%s %s = %d\n%s", method, target, recorder.Code, recorder.Body.String())
	}
	var answer map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &answer); err != nil {
		t.Fatalf("%s %s: %v\n%s", method, target, err, recorder.Body.String())
	}
	return answer
}

// run reads one run of the app's kitwork_runs.
func (q *queueApp) run(t *testing.T, id string) queuestore.Run {
	t.Helper()
	run, err := q.app.queueStore().Get(id)
	if err != nil {
		t.Fatalf("run %s: %v", id, err)
	}
	return run
}

// queueWatch hears the worker's settles (QueueSettled) for one test, and keeps them by run.
type queueWatch struct {
	events chan [2]string
	heard  map[string][]string
}

// watchQueue makes the worker quick (a poll every 20 ms, a retry after 10 ms) and hears its settles.
func watchQueue(t *testing.T) *queueWatch {
	t.Helper()
	savedPoll, savedBackoff, savedHook := queuePollInterval, queueBackoff, QueueSettled
	queuePollInterval = 20 * time.Millisecond
	queueBackoff = func(int) time.Duration { return 10 * time.Millisecond }
	watch := &queueWatch{events: make(chan [2]string, 256), heard: map[string][]string{}}
	QueueSettled = func(_, id, status string) { watch.events <- [2]string{id, status} }
	t.Cleanup(func() { queuePollInterval, queueBackoff, QueueSettled = savedPoll, savedBackoff, savedHook })
	return watch
}

// until waits for the run id to settle as status, and gives every status it settled as on the way; the
// settles of other runs heard meanwhile are kept for them.
func (w *queueWatch) until(t *testing.T, id, status string) []string {
	t.Helper()
	deadline := time.After(15 * time.Second)
	for {
		if seen := w.heard[id]; len(seen) > 0 && seen[len(seen)-1] == status {
			return seen
		}
		select {
		case event := <-w.events:
			w.heard[event[0]] = append(w.heard[event[0]], event[1])
		case <-deadline:
			t.Fatalf("run %s never settled as %s; settled as %v", id, status, w.heard[id])
		}
	}
}

const camOnJob = `import * as queue from "kitwork/queue";
import { sendOrderEmail } from "app/.shared/email.work";

export const camOn = queue.job((context) => {
  const { email, orderId } = context.data();
  context.job.log("lần " + context.job.attempt + "/" + context.job.maxAttempts);
  sendOrderEmail(context, email, orderId);
}).retry(3).timeout("30s");
`

const emailLibrary = `export const sendOrderEmail = (context, email, orderId) => context.job.log("gửi " + email + " đơn " + orderId);`

const sendRoute = `import * as router from "kitwork/router";
import { camOn } from "app/.shared/queue/camOn.work";

router.post((context) => {
  const request = context.request;
  const options = {};
  if (request.form("key") !== "") {
    options.key = request.form("key");
  }
  if (request.form("delay") !== "") {
    options.delay = request.form("delay");
  }
  return context.json(camOn.send({ email: request.form("email"), orderId: Number(request.form("orderId")) }, options));
});
`

func order(email, orderID, key, delay string) url.Values {
	return url.Values{"email": {email}, "orderId": {orderID}, "key": {key}, "delay": {delay}}
}

// The spec's example (KITWORK_QUEUE.md, the start): a job declared in .shared/queue/ and exported, a route
// that imports it and sends; the receipt is { id, name, status: "pending" } and the worker runs the handler
// with the data sent. The assertion is on the logged values: a run that received no data completes too,
// so "it completed" proves nothing of the data. CONTROL of the name: the file is thanks.work, the export
// camOn, and the job is camOn.
func TestQueueSendFromRouteRunsHandlerWithContextData(t *testing.T) {
	watch := watchQueue(t)
	q := newQueueApp(t, map[string]string{
		".shared/email.work":         emailLibrary,
		".shared/queue/thanks.work":  strings.Replace(camOnJob, "camOn.work", "thanks.work", 1),
		"localhost/send/router.work": strings.Replace(sendRoute, "camOn.work", "thanks.work", 1),
	})

	receipt := q.serve(t, http.MethodPost, "/send", order("an@example.com", "123", "", ""))
	id, _ := receipt["id"].(string)
	if !strings.HasPrefix(id, "r_") || receipt["name"] != "camOn" || receipt["status"] != "pending" || receipt["duplicate"] != nil {
		t.Fatalf("receipt = %v, want { id: r_…, name: camOn, status: pending }", receipt)
	}
	watch.until(t, id, "completed")
	run := q.run(t, id)
	if run.Status != "completed" || run.Attempt != 1 || run.MaxAttempts != 4 || run.TimeoutMS != 30000 ||
		!strings.Contains(run.Log, "lần 1/4\ngửi an@example.com đơn 123\n") || run.Data != `{"email":"an@example.com","orderId":123}` {
		t.Fatalf("run = %+v", run)
	}
	definitions, _ := q.app.queueStore().Definitions(10, 0)
	if len(definitions) != 1 || definitions[0].Name != "camOn" || definitions[0].Source != ".shared/queue/thanks.work" ||
		definitions[0].Retry != 3 || definitions[0].Timeout != "30s" {
		t.Fatalf("definitions = %+v", definitions)
	}
	// the runs are the app's, in its own database
	if _, err := os.Stat(filepath.Join(q.root, "acme", ".database", "application", "application.kitdb")); err != nil {
		t.Fatalf("the app's database: %v", err)
	}
}

// .retry(n) is n attempts MORE (mục 3): .retry(2) is three attempts, the run keeps its id through them, and
// it ends failed with every attempt's error in its log (the dead letter).
func TestQueueRetryIsExtraAttempts(t *testing.T) {
	watch := watchQueue(t)
	q := newQueueApp(t, map[string]string{
		".shared/queue/flaky.work": `import * as queue from "kitwork/queue";
export const flaky = queue.job((context) => {
  context.job.log("thử " + context.job.attempt + (context.job.isFinalAttempt ? " (cuối)" : ""));
  fail("hỏng ở lần " + context.job.attempt);
}).retry(2);
`,
		"localhost/send/router.work": `import * as router from "kitwork/router";
import { flaky } from "app/.shared/queue/flaky.work";
router.post((context) => context.json(flaky.send({ n: 1 })));
`,
	})
	id := q.serve(t, http.MethodPost, "/send", url.Values{})["id"].(string)
	if seen := watch.until(t, id, "failed"); strings.Join(seen, ",") != "pending,pending,failed" {
		t.Fatalf("settled as %v, want two retries then failed", seen)
	}
	run := q.run(t, id)
	if run.Attempt != 3 || run.MaxAttempts != 3 || run.ErrorCode != "FAIL" || !strings.Contains(run.ErrorMessage, "hỏng ở lần 3") {
		t.Fatalf("run = %+v, want failed at attempt 3 of 3 with its error", run)
	}
	for _, line := range []string{"thử 1\n", "lần thử 1: FAIL", "thử 2\n", "lần thử 2: FAIL", "thử 3 (cuối)\n", "lần thử 3: FAIL"} {
		if !strings.Contains(run.Log, line) {
			t.Errorf("the log lacks %q:\n%s", line, run.Log)
		}
	}
}

// .timeout() bounds each attempt: an attempt past it is failed TIMEOUT when its time is up, and the run is
// retried, then failed; the runtime stops the attempt's VM too (mục 6, "runtime báo huỷ cho lần thử"), so
// both attempts have ended soon after, not when their loop would. The loop runs some seconds if nothing
// stops it, so a deadline the VM ignores fails here at once. The store's side of fencing, a settle by a
// lease taken since refused, is TestSettleIsFencedByTheLeaseToken; the worker's, a late success refused,
// is TestQueueLateSuccessOfALostLeaseIsRefused.
func TestQueueTimeoutPerAttemptIsFenced(t *testing.T) {
	watch := watchQueue(t)
	q := newQueueApp(t, map[string]string{
		".shared/queue/slow.work": `import * as queue from "kitwork/queue";
export const slow = queue.job((context) => {
  let total = 0;
  for (let i = 0; i < 10000000; i++) {
    total = total + i;
  }
  context.job.log("xong " + total);
}).retry(1).timeout("200ms");
`,
		"localhost/send/router.work": `import * as router from "kitwork/router";
import { slow } from "app/.shared/queue/slow.work";
router.post((context) => context.json(slow.send({})));
`,
	})
	q.app.MaxEnergy = 1 << 40 // the loop stops by its time, never by energy
	worker := q.app.queueWorker()
	started := time.Now()
	id := q.serve(t, http.MethodPost, "/send", url.Values{})["id"].(string)
	if seen := watch.until(t, id, "failed"); strings.Join(seen, ",") != "pending,failed" {
		t.Fatalf("settled as %v, want one retry then failed", seen)
	}
	if !waitFor(time.Second, func() bool { return len(worker.held()) == 0 }) {
		t.Fatalf("a second after the last timeout %d attempts still run: the VM did not stop at its deadline", len(worker.held()))
	}
	if took := time.Since(started); took > 10*time.Second {
		t.Fatalf("two attempts of 200 ms took %s", took)
	}
	run := q.run(t, id)
	if run.Attempt != 2 || run.ErrorCode != "TIMEOUT" || !strings.Contains(run.ErrorMessage, `hết giờ: lần thử 2 chạy quá 200ms (.timeout("200ms"))`) ||
		!strings.Contains(run.Log, "lần thử 1: TIMEOUT") {
		t.Fatalf("run = %+v", run)
	}
}

// A key held by a run of the job sends nothing more: the receipt says duplicate with the holding run's id.
// CONTROL: without a key every send is a run of its own.
func TestQueueDuplicateKeyReturnsHoldingRun(t *testing.T) {
	q := newQueueApp(t, map[string]string{
		".shared/email.work":         emailLibrary,
		".shared/queue/camOn.work":   camOnJob,
		"localhost/send/router.work": sendRoute,
	})
	q.app.StopQueueWorker() // what send stores, not what the worker does with it
	first := q.serve(t, http.MethodPost, "/send", order("an@example.com", "1", "order:1", ""))
	again := q.serve(t, http.MethodPost, "/send", order("an@example.com", "1", "order:1", ""))
	if first["duplicate"] != nil || again["duplicate"] != true || again["id"] != first["id"] || again["status"] != "pending" {
		t.Fatalf("first %v, again %v: want the same id, duplicate", first, again)
	}
	one := q.serve(t, http.MethodPost, "/send", order("an@example.com", "1", "", ""))
	two := q.serve(t, http.MethodPost, "/send", order("an@example.com", "1", "", ""))
	if one["id"] == two["id"] || one["duplicate"] != nil || two["duplicate"] != nil {
		t.Fatalf("unkeyed sends %v and %v: want two runs", one, two)
	}
}

// A send that cannot be stored or is written wrong is an error of Work's model, which safe() reads:
// a function in the data, data over the ceiling, an option send has not, a delay it cannot read, and
// data that holds itself or nests past value.MaxDepth (TYPE and INVALID_ARGUMENT, as JSON.stringify
// says them; before, the walk went round in Go until the host died). CONTROL: a send written right is ok.
func TestQueueSendErrorsAreWorkErrors(t *testing.T) {
	q := newQueueApp(t, map[string]string{
		".shared/email.work":       emailLibrary,
		".shared/queue/camOn.work": camOnJob,
		"localhost/send/router.work": `import * as router from "kitwork/router";
import { camOn } from "app/.shared/queue/camOn.work";
const said = (result) => (result.ok ? "ok" : result.error.code + ": " + result.error.message);
const selfish = () => {
  const order = { n: 1, items: [] };
  order.items.push({ owner: order });
  return order;
};
const deep = () => {
  let nested = { n: 0 };
  for (let i = 0; i < 10001; i++) {
    nested = { inner: nested };
  }
  return nested;
};
router.get((context) => context.json({
  right: said(safe(() => camOn.send({ email: "a@b.c", orderId: 1 }, { delay: "5m", key: "k" }))),
  cycle: said(safe(() => camOn.send(selfish()))),
  deep: said(safe(() => camOn.send(deep()))),
  function: said(safe(() => camOn.send({ email: () => 1 }))),
  large: said(safe(() => camOn.send({ text: "x".repeat(70000) }))),
  option: said(safe(() => camOn.send({}, { when: "5m" }))),
  delay: said(safe(() => camOn.send({}, { delay: "sớm" }))),
  number: said(safe(() => camOn.send({}, { delay: 5 })))
}));
`,
	})
	q.app.StopQueueWorker()
	got := q.serve(t, http.MethodGet, "/send", nil)
	want := map[string]string{
		"right":    "ok",
		"cycle":    "TYPE: camOn.send: object tự tham chiếu (vòng ở items[0].owner, quay về gốc)",
		"deep":     "INVALID_ARGUMENT: camOn.send: giá trị lồng quá sâu (hơn 10000 tầng object và mảng",
		"function": "INVALID_ARGUMENT: camOn.send: dữ liệu của job phải viết được thành JSON: data.email là func, không phải dữ liệu",
		"large":    "TOO_LARGE: camOn.send: dữ liệu 70011 byte, quá trần 64 KiB của một lần chạy",
		"option":   "INVALID_ARGUMENT: send không có lựa chọn when: chỉ delay và key",
		"delay":    `INVALID_ARGUMENT: delay "sớm" không đọc được`,
		"number":   "INVALID_ARGUMENT: delay là một khoảng viết bằng chuỗi",
	}
	for key, prefix := range want {
		if said, _ := got[key].(string); !strings.HasPrefix(said, prefix) {
			t.Errorf("%s = %q, want it to start %q", key, said, prefix)
		}
	}
}

// A store that cannot be written is a DATABASE error, never a receipt. CONTROL: the same send before the
// app's database manager closed is ok.
func TestQueueSendThatCannotBeStoredIsADatabaseError(t *testing.T) {
	q := newQueueApp(t, map[string]string{
		".shared/email.work":       emailLibrary,
		".shared/queue/camOn.work": camOnJob,
		"localhost/send/router.work": `import * as router from "kitwork/router";
import { camOn } from "app/.shared/queue/camOn.work";
router.get((context) => {
  const result = safe(() => camOn.send({ email: "a@b.c", orderId: 1 }));
  return context.json({ ok: result.ok, code: result.ok ? "" : result.error.code });
});
`,
	})
	q.app.StopQueueWorker()
	if got := q.serve(t, http.MethodGet, "/send", nil); got["ok"] != true {
		t.Fatalf("control: %v", got)
	}
	// the app's database manager is closed under the site: the file cannot be opened
	manager, ok := q.runtime.Resource(kitDBResourceName).(*kitDBManager)
	if !ok {
		t.Fatal("the app has no database manager")
	}
	manager.Close()
	if got := q.serve(t, http.MethodGet, "/send", nil); got["ok"] != false || got["code"] != "DATABASE" {
		t.Fatalf("a send with no database = %v, want DATABASE", got)
	}
}

// Every way a declaration goes wrong is said at boot with its place, and the job is not registered: a job
// not exported, one job under two names, a job name two files declare, a rule chained twice, a rule of
// the wrong kind, a handler that is not a function, a handler that takes data() apart; an aliased export
// does not compile. The place is where queue.job is written (its dot), or the handler's arrow for what the
// handler does. CONTROL: the right job of the same app registers.
func TestQueueDeclarationErrors(t *testing.T) {
	q := newQueueApp(t, map[string]string{
		".shared/queue/a.work": `import * as queue from "kitwork/queue";
export const good = queue.job((context) => 1);
const hidden = queue.job((context) => 2);
export const twice = queue.job((context) => 3);
export const again = twice;
export const doubled = queue.job((context) => 4).retry(1).retry(2);
export const wrong = queue.job((context) => 5).retry(-1).timeout(30);
export const notAFunction = queue.job(5);
export const takesData = queue.job(({ data }) => 6);
export const same = queue.job((context) => 7);
`,
		".shared/queue/b.work": `import * as queue from "kitwork/queue";
export const same = queue.job((context) => 8);
`,
		".shared/queue/c.work": `import * as queue from "kitwork/queue";
const aliased = queue.job((context) => 9);
export { aliased as renamed };
`,
	})
	for _, line := range []string{
		"[Queue] a.work:3:21: queue.job không export: export const camOn = queue.job(…), tên job là tên export",
		"[Queue] a.work:4:27: job twice export thêm dưới tên again: một job, một tên",
		"[Queue] a.work:6:29: .retry nối hai lần: một job một luật",
		"[Queue] a.work:7:27: .retry(n) nhận số lần thử thêm, số nguyên từ 0 tới 100",
		`[Queue] a.work:7:27: .timeout(hạn) nhận một khoảng viết bằng chuỗi: .timeout("30s")`,
		"[Queue] a.work:8:34: queue.job nhận một hàm: queue.job((context) => { … })",
		"[Queue] a.work:9:47: `data` là hàm của `context`",
		"[Queue] b.work:2:26: job same đã khai ở a.work:10:26; tên job duy nhất trong app",
		`[Queue] compile .shared/queue/c.work: c.work:3:21: export { aliased as renamed } trong .shared/queue/: job mang tên export của nó, một job một tên`,
	} {
		if !strings.Contains(q.output, line) {
			t.Errorf("boot did not say %q:\n%s", line, q.output)
		}
	}
	worker := q.app.queueWorker()
	worker.mu.Lock()
	var names []string
	for _, handler := range worker.handlers {
		names = append(names, handler.Name)
	}
	worker.mu.Unlock()
	if strings.Join(names, ",") != "good,same" {
		t.Fatalf("registered %v, want good and the first same", names)
	}
}

// waitFor polls until cond holds or the deadline passes. Returns whether it held.
func waitFor(deadline time.Duration, cond func() bool) bool {
	stop := time.Now().Add(deadline)
	for time.Now().Before(stop) {
		if cond() {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return cond()
}
