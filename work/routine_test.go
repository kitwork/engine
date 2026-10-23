package work

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kitwork/engine/app"
	"github.com/kitwork/engine/builtins"
	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
)

// The routines of kitwork/routine (KITWORK_ROUTINE.md), driven through a real site: router.work compiled,
// served, each function in a VM of its own. A function that must be held is held in testNotify, whose hook
// the test sets: no test sleeps to make an order happen.

// routineHeader leads every route of these sites: the first line of a route's own source is line 3.
const routineHeader = "import * as router from \"kitwork/router\";\nimport * as routine from \"kitwork/routine\";\n"

// routineSite serves files (each a folder's router.work, under routineHeader) on a tenant with energy for
// the spins, closed when the test ends.
func routineSite(t *testing.T, files map[string]string) *Tenant {
	t.Helper()
	all := map[string]string{"router.work": `import * as router from "kitwork/router";`}
	for path, source := range files {
		all[path] = routineHeader + source
	}
	tn := NewTenant(writeSite(t, all), "localhost")
	tn.MaxEnergy = 200_000_000
	if err := tn.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	t.Cleanup(tn.Close)
	return tn
}

// notifyHook makes hook what testNotify(name, …) calls, for this test only.
func notifyHook(t *testing.T, hook func(name string, args ...value.Value)) {
	t.Helper()
	saved, savedBuiltin := TestNotifyHook, builtins.TestNotifyHook
	TestNotifyHook, builtins.TestNotifyHook = hook, hook
	t.Cleanup(func() { TestNotifyHook, builtins.TestNotifyHook = saved, savedBuiltin })
}

// gate is a point a function is held at until the test opens it, or two seconds pass (then the test
// fails: never a hang).
type gate struct {
	once sync.Once
	ch   chan struct{}
}

func newGate() *gate { return &gate{ch: make(chan struct{})} }

func (g *gate) open() { g.once.Do(func() { close(g.ch) }) }

func (g *gate) wait(t *testing.T, what string) bool {
	select {
	case <-g.ch:
		return true
	case <-time.After(2 * time.Second):
		t.Errorf("%s: not reached within 2s", what)
		return false
	}
}

// routineJSON serves path and reads its JSON answer.
func routineJSON(t *testing.T, tn *Tenant, path string) (int, map[string]any, string) {
	t.Helper()
	rec := serveGet(tn, path, nil)
	var answer map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &answer)
	return rec.Code, answer, rec.Body.String()
}

// wait(fn…) starts every function before it waits for any, and gives their results in input order, not
// in the order they end: here b ends first, and a is not even let go until both have started.
//
// CONTROL: with waitFunctions running each function to its end before starting the next, a was held
// for b's start, which never came: "both functions started: not reached within 2s", 500. With the
// results collected in the order the functions ended, the answer was ["b","a"].
func TestRoutineWaitRunsAllBeforeWaiting(t *testing.T) {
	var mu sync.Mutex
	arrived := map[string]bool{}
	both, bDone := newGate(), newGate()
	notifyHook(t, func(name string, args ...value.Value) {
		label := ""
		if len(args) > 0 {
			label = args[0].Text()
		}
		switch name {
		case "arrive":
			mu.Lock()
			arrived[label] = true
			if arrived["a"] && arrived["b"] {
				both.open()
			}
			mu.Unlock()
			if !both.wait(t, "both functions started") {
				return
			}
			if label == "a" {
				bDone.wait(t, "b ended")
			}
		case "done":
			bDone.open()
		}
	})
	tn := routineSite(t, map[string]string{
		"r/router.work": `router.get((context) => context.json(routine.wait(
  () => { testNotify("arrive", "a"); return "a"; },
  () => { testNotify("arrive", "b"); testNotify("done", "b"); return "b"; }
)));`,
	})
	if code, _, body := routineJSON(t, tn, "/r"); code != http.StatusOK || body != `["a","b"]` {
		t.Fatalf("/r = %d %s, want 200 [\"a\",\"b\"]", code, body)
	}
}

// go(fn) gives a handle at once; wait(handle) gives the routine's result, and a second wait of the same
// handle the same result: the function ran once.
//
// CONTROL: with start running each routine twice (two runRoutine calls behind one handle), ran counted 2.
func TestRoutineGoReturnsHandleAndWaitIsIdempotent(t *testing.T) {
	var ran atomic.Int32
	notifyHook(t, func(name string, _ ...value.Value) {
		if name == "ran" {
			ran.Add(1)
		}
	})
	tn := routineSite(t, map[string]string{
		"r/router.work": `router.get((context) => {
  const handle = routine.go(() => { testNotify("ran"); return { n: 42 }; });
  const first = routine.wait(handle);
  first.n = 7;
  return context.json([first, routine.wait(handle)]);
});`,
	})
	if code, _, body := routineJSON(t, tn, "/r"); code != http.StatusOK || body != `[{"n":7},{"n":42}]` {
		t.Fatalf("/r = %d %s, want [{\"n\":7},{\"n\":42}]: each wait its own copy of one result", code, body)
	}
	if got := ran.Load(); got != 1 {
		t.Fatalf("the routine ran %d times for two waits of one handle, want 1", got)
	}
}

// What a function of go reads from outside is copied when go() is called: a write after it does not
// reach the routine, and nothing is shared, so -race sees no race.
//
// CONTROL: with copyRoutineLambda copying the lambda alone (its scope and the objects in it shared, as the
// go of before did), the routine read 2, and go test -race reported the race on box.
func TestRoutineGoCopiesValues(t *testing.T) {
	held := newGate()
	notifyHook(t, func(name string, _ ...value.Value) {
		switch name {
		case "hold":
			held.wait(t, "release")
		case "release":
			held.open()
		}
	})
	tn := routineSite(t, map[string]string{
		"r/router.work": `router.get((context) => {
  const box = { n: 1 };
  let count = 1;
  const handle = routine.go(() => { testNotify("hold"); return [box.n, count]; });
  box.n = 2;
  count = 2;
  testNotify("release");
  return context.json(routine.wait(handle));
});`,
	})
	if code, _, body := routineJSON(t, tn, "/r"); code != http.StatusOK || body != `[1,1]` {
		t.Fatalf("/r = %d %s, want [1,1]: the routine sees what go() saw", code, body)
	}
}

// Two functions of one wait that write one outer variable and one outer object each write their own copy:
// no Go map is written from two goroutines (a fault that ends the process), and the caller sees none of
// their writes; what they give back comes by return.
//
// CONTROL: with waitFunctions handing each function the lambda as written (no copy), the process died,
// with -race and without: "fatal error: concurrent map read and map write".
func TestRoutineWaitChildrenDoNotShareMaps(t *testing.T) {
	tn := routineSite(t, map[string]string{
		"r/router.work": `router.get((context) => {
  let total = 0;
  const shared = { n: 0, items: [] };
  const count = () => {
    for (let i = 0; i < 3000; i++) { total = total + 1; shared.n = shared.n + 1; shared.items.push(i); }
    return [total, shared.n, shared.items.length];
  };
  const [a, b] = routine.wait(count, count);
  return context.json({ a, b, total, n: shared.n, items: shared.items.length });
});`,
	})
	code, _, body := routineJSON(t, tn, "/r")
	if code != http.StatusOK || body != `{"a":[3000,3000,3000],"b":[3000,3000,3000],"items":0,"n":0,"total":0}` {
		t.Fatalf("/r = %d %s", code, body)
	}
}

// A function's failure goes up from wait as any failure does: safe catches it with the place where it
// failed. Of several, wait reports the first in input order, wrapped (error.cause), and only once every
// function has ended: here the second fails at once, the first only after it.
//
// CONTROL: with routineOutcome taking the failure of the function that ended first, the error was the
// second's ("hàm thứ 2", cause "second").
func TestRoutineWaitErrorIsCatchableWithItsPlace(t *testing.T) {
	secondFailed := newGate()
	notifyHook(t, func(name string, _ ...value.Value) {
		switch name {
		case "second":
			secondFailed.open()
		case "first":
			secondFailed.wait(t, "the second function failed")
		}
	})
	tn := routineSite(t, map[string]string{
		"one/router.work": `router.get((context) => context.json(safe(() => routine.wait(() => fail("boom"))).error));`,
		"two/router.work": `router.get((context) => context.json(safe(() => routine.wait(
  () => { testNotify("first"); fail("first"); },
  () => { testNotify("second"); fail("second"); }
)).error));`,
	})
	code, one, body := routineJSON(t, tn, "/one")
	if code != http.StatusOK || one["code"] != "FAIL" || one["message"] != "boom" || one["at"] != "router.work:3:68" || one["cause"] != nil {
		t.Fatalf("/one = %d %s, want FAIL boom at router.work:3:68, no cause", code, body)
	}
	code, two, body := routineJSON(t, tn, "/two")
	cause, _ := two["cause"].(map[string]any)
	if code != http.StatusOK || two["code"] != "FAIL" || two["at"] != "router.work:3:56" ||
		two["message"] != "routine.wait: hàm thứ 1 trong 2 hỏng: first" || cause == nil ||
		cause["message"] != "first" || cause["code"] != "FAIL" || cause["at"] != "router.work:4:32" {
		t.Fatalf("/two = %d %s, want the first function's failure, wrapped, at the wait", code, body)
	}
}

// retry(n) is n attempts more; timeout bounds each attempt and is TIMEOUT, which safe catches.
//
// CONTROL: with runRoutine stopping once attempt reached rules.retry (retry as the total), there were 2
// tries; with the per-attempt deadline left as EXECUTION_CANCELLED, safe did not catch it and /timeout
// answered 500.
func TestRoutineRetryAndTimeout(t *testing.T) {
	var tries atomic.Int32
	notifyHook(t, func(name string, _ ...value.Value) {
		if name == "try" {
			tries.Add(1)
		}
	})
	tn := routineSite(t, map[string]string{
		"retry/router.work": `router.get((context) => context.json(safe(() => routine.retry(2).wait(() => { testNotify("try"); fail("x"); })).error));`,
		"timeout/router.work": `const spin = (n) => { let x = 0; for (let i = 0; i < n; i++) { x = x + i; } return x; };
router.get((context) => context.json(safe(() => routine.retry(1).timeout("30ms").wait(() => spin(1000000000))).error));`,
		"recovers/router.work": `router.get((context) => context.json(routine.retry(3).wait(() => { testNotify("try"); return "ok"; })));`,
	})
	code, failed, body := routineJSON(t, tn, "/retry")
	if code != http.StatusOK || failed["code"] != "FAIL" || tries.Load() != 3 {
		t.Fatalf("/retry = %d %s after %d tries, want FAIL after 3", code, body, tries.Load())
	}
	started := time.Now()
	code, timed, body := routineJSON(t, tn, "/timeout")
	if code != http.StatusOK || timed["code"] != "TIMEOUT" || timed["message"] != `hết giờ: lần thử 2 chạy quá 30ms (.timeout("30ms"))` {
		t.Fatalf("/timeout = %d %s, want TIMEOUT of the second attempt", code, body)
	}
	if took := time.Since(started); took < 60*time.Millisecond || took > 2*time.Second {
		t.Fatalf("/timeout took %s, want two attempts of 30ms", took)
	}
	tries.Store(0)
	if code, _, body := routineJSON(t, tn, "/recovers"); code != http.StatusOK || body != `"ok"` || tries.Load() != 1 {
		t.Fatalf("/recovers = %d %s after %d tries, want ok after 1", code, body, tries.Load())
	}
}

// cancel(handle) stops a routine; a wait of it fails CANCELLED, which safe catches. cancel of one that
// has ended keeps its result.
//
// CONTROL: with routineStopped giving back the stop as it is, wait(handle) failed EXECUTION_CANCELLED,
// which safe does not catch: /cancel answered 500.
func TestRoutineCancel(t *testing.T) {
	tn := routineSite(t, map[string]string{
		"cancel/router.work": `const spin = (n) => { let x = 0; for (let i = 0; i < n; i++) { x = x + i; } return x; };
router.get((context) => {
  const forever = routine.go(() => spin(1000000000));
  routine.cancel(forever);
  const done = routine.go(() => 5);
  const five = routine.wait(done);
  routine.cancel(done);
  return context.json({ cancelled: safe(() => routine.wait(forever)).error, kept: routine.wait(done), five });
});`,
	})
	code, answer, body := routineJSON(t, tn, "/cancel")
	cancelled, _ := answer["cancelled"].(map[string]any)
	if code != http.StatusOK || cancelled == nil || cancelled["code"] != "CANCELLED" ||
		cancelled["message"] != "routine đã bị huỷ bằng routine.cancel(handle)" || answer["kept"] != float64(5) {
		t.Fatalf("/cancel = %d %s, want CANCELLED and the ended routine's 5 kept", code, body)
	}
}

// slogRecords collects what the process logs while a test runs.
type slogRecords struct {
	mu      sync.Mutex
	records []slog.Record
}

func (s *slogRecords) Enabled(context.Context, slog.Level) bool { return true }
func (s *slogRecords) Handle(_ context.Context, record slog.Record) error {
	s.mu.Lock()
	s.records = append(s.records, record.Clone())
	s.mu.Unlock()
	return nil
}
func (s *slogRecords) WithAttrs([]slog.Attr) slog.Handler { return s }
func (s *slogRecords) WithGroup(string) slog.Handler      { return s }

// named gives the attributes of every record with message, as text.
func (s *slogRecords) named(message string) []map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var found []map[string]string
	for _, record := range s.records {
		if record.Message != message {
			continue
		}
		attributes := map[string]string{"level": record.Level.String()}
		record.Attrs(func(attribute slog.Attr) bool {
			attributes[attribute.Key] = attribute.Value.String()
			return true
		})
		found = append(found, attributes)
	}
	return found
}

func captureSlog(t *testing.T) *slogRecords {
	t.Helper()
	records := &slogRecords{}
	saved := slog.Default()
	slog.SetDefault(slog.New(records))
	t.Cleanup(func() { slog.SetDefault(saved) })
	return records
}

// A routine of go that fails and that no wait reads is one line of the log, with the app, the site, the
// code, the message and where it failed: never lost without a word. The line waits for the call that
// started the routine to be over, so a wait later in that call still reads the failure as its own: here
// both routines have ended, with no wait there, before the handler waits for one of them; the one it
// reads is never logged, the one it leaves is logged once the request is over, and only then.
//
// CONTROL: with routineHandle.end not reporting, no record came: "want one record of the routine that
// failed, got 0". With end logging a failure no wait was there for at once (as it did), the failure the
// handler then read was logged during its request: "logged while the request ran: [mine unread]".
func TestRoutineUnwaitedFailureIsLogged(t *testing.T) {
	records := captureSlog(t)
	var tn *Tenant
	var during []string
	notifyHook(t, func(name string, _ ...value.Value) {
		if name != "ended" {
			return
		}
		// done() of an app task comes after the routine's end: none counted, both have ended
		for deadline := time.Now().Add(2 * time.Second); tn.appRuntime.Tasks().Running() > 0 && time.Now().Before(deadline); {
			time.Sleep(time.Millisecond)
		}
		for _, record := range records.named("Kitwork routine failed") {
			during = append(during, record["message"])
		}
	})
	tn = routineSite(t, map[string]string{
		"lost/router.work": `router.get((context) => {
  routine.go(() => fail("boom"));
  return context.status(202).json({ queued: true });
});`,
		"waited/router.work": `router.get((context) => {
  const unread = routine.go(() => fail("unread"));
  const task = routine.go(() => fail("mine"));
  testNotify("ended");
  return context.json(safe(() => routine.wait(task)).error);
});`,
	})
	if code, _, body := routineJSON(t, tn, "/lost"); code != http.StatusAccepted {
		t.Fatalf("/lost = %d %s, want 202", code, body)
	}
	logged := func(message string) []map[string]string {
		var found []map[string]string
		for _, record := range records.named("Kitwork routine failed") {
			if record["message"] == message {
				found = append(found, record)
			}
		}
		return found
	}
	for deadline := time.Now().Add(2 * time.Second); len(logged("boom")) == 0 && time.Now().Before(deadline); {
		time.Sleep(5 * time.Millisecond)
	}
	failed := records.named("Kitwork routine failed")
	if len(failed) != 1 {
		t.Fatalf("want one record of the routine that failed, got %d: %v", len(failed), failed)
	}
	record := failed[0]
	if record["level"] != "ERROR" || record["code"] != "FAIL" || record["message"] != "boom" ||
		record["at"] != "router.work:4:20" || record["routine"] != "router.work:4:10" || record["app"] != "localhost" || record["site"] != "localhost" {
		t.Fatalf("record = %v", record)
	}
	if code, answer, body := routineJSON(t, tn, "/waited"); code != http.StatusOK || answer["message"] != "mine" {
		t.Fatalf("/waited = %d %s", code, body)
	}
	if len(during) != 1 || during[0] != "boom" {
		t.Fatalf("logged while the request ran: %v, want only the boom of /lost", during)
	}
	for deadline := time.Now().Add(2 * time.Second); len(logged("unread")) == 0 && time.Now().Before(deadline); {
		time.Sleep(5 * time.Millisecond)
	}
	if unread := logged("unread"); len(unread) != 1 || unread[0]["at"] != "router.work:4:35" {
		t.Fatalf("the failure the handler left: %v, want one record once the request was over", unread)
	}
	if mine := logged("mine"); len(mine) != 0 {
		t.Fatalf("a failure its wait read was logged as well: %v", mine)
	}
}

// An app runs at most routineLimit routines at once, those of go and the functions of wait together: past
// it go() and wait() fail DENIED at once, nothing waits in line; once one ends there is room again.
//
// CONTROL: with StartWithin given no limit (0), the third go started: /third answered "started". With
// waitFunctions' StartWithin given no limit, the wait ran its function: /wait answered "started".
func TestRoutineCap(t *testing.T) {
	saved := routineLimit
	routineLimit = 2
	t.Cleanup(func() { routineLimit = saved })
	release := newGate()
	var held atomic.Int32
	notifyHook(t, func(name string, _ ...value.Value) {
		if name == "hold" {
			held.Add(1)
			release.wait(t, "release")
		}
	})
	tn := routineSite(t, map[string]string{
		"hold/router.work":  `router.get((context) => { routine.go(() => testNotify("hold"), () => testNotify("hold")); return context.json("held"); });`,
		"third/router.work": `router.get((context) => { const tried = safe(() => routine.go(() => 1)); return context.json(tried.ok ? "started" : tried.error); });`,
		"wait/router.work":  `router.get((context) => { const tried = safe(() => routine.wait(() => 1)); return context.json(tried.ok ? "started" : tried.error); });`,
	})
	if code, _, body := routineJSON(t, tn, "/hold"); code != http.StatusOK {
		t.Fatalf("/hold = %d %s", code, body)
	}
	for deadline := time.Now().Add(2 * time.Second); held.Load() < 2 && time.Now().Before(deadline); {
		time.Sleep(time.Millisecond)
	}
	code, refused, body := routineJSON(t, tn, "/third")
	waitCode, waitRefused, waitBody := routineJSON(t, tn, "/wait")
	release.open()
	if code != http.StatusOK || refused["code"] != "DENIED" ||
		refused["message"] != "routine.go: app đã chạy tới trần 2 routine cùng lúc; không bắt đầu thêm, không xếp hàng (KITWORK_ROUTINE.md mục 9)" {
		t.Fatalf("/third = %d %s, want DENIED past the limit", code, body)
	}
	if waitCode != http.StatusOK || waitRefused["code"] != "DENIED" ||
		waitRefused["message"] != "routine.wait: app đã chạy tới trần 2 routine cùng lúc; không bắt đầu thêm, không xếp hàng (KITWORK_ROUTINE.md mục 9)" {
		t.Fatalf("/wait = %d %s, want DENIED past the limit", waitCode, waitBody)
	}
	for deadline := time.Now().Add(2 * time.Second); tn.appRuntime.Tasks().Running() > 0 && time.Now().Before(deadline); {
		time.Sleep(time.Millisecond)
	}
	if code, _, body := routineJSON(t, tn, "/third"); code != http.StatusOK || body != `"started"` {
		t.Fatalf("/third after the two ended = %d %s, want started", code, body)
	}
}

// A file only declares as it loads: a go or a wait at the top of a router is refused, and the site does
// not boot, saying where.
//
// CONTROL: with runFolderRouter not marking its VM as loading, the routine started as the router loaded and
// the site booted.
func TestRoutineRefusedAtFileLevel(t *testing.T) {
	var ran atomic.Int32
	notifyHook(t, func(name string, _ ...value.Value) {
		if name == "ran" {
			ran.Add(1)
		}
	})
	tn := NewTenant(writeSite(t, map[string]string{
		"router.work": `import * as router from "kitwork/router";`,
		"top/router.work": routineHeader + `routine.go(() => testNotify("ran"));
router.get((context) => context.json("loaded"));`,
	}), "localhost")
	t.Cleanup(tn.Close)
	err := tn.Run()
	if err == nil || !strings.Contains(err.Error(), "router.work:3:8: routine.go ở cấp file: lúc nạp, file chỉ khai; gọi routine.go trong một handler, một job, một lịch hay một hàm (KITWORK_ROUTINE.md mục 9)") {
		t.Fatalf("run = %v, want the load refused at router.work:3:8", err)
	}
	if ran.Load() != 0 {
		t.Fatalf("the routine of the file's top level ran")
	}
}

// The functions of wait belong to the caller: when its request is cancelled they stop at once, wait
// returns once all have ended, and none is left counted. The request ends for that reason, the stop
// (EXECUTION_CANCELLED), not for its energy: the tenant has energy for seconds of spinning.
//
// CONTROL: with waitFunctions starting its functions under context.Background(), they spun on past the
// cancel: "the request had not ended 500ms after it was cancelled".
func TestRoutineWaitStopsWithRequest(t *testing.T) {
	records := captureSlog(t)
	started := make(chan struct{}, 2)
	notifyHook(t, func(name string, _ ...value.Value) {
		if name == "spinning" {
			started <- struct{}{}
		}
	})
	tn := routineSite(t, map[string]string{
		"r/router.work": `const spin = (n) => { testNotify("spinning"); let x = 0; for (let i = 0; i < n; i++) { x = x + i; } return x; };
router.get((context) => context.json(routine.wait(() => spin(1000000000), () => spin(1000000000))));`,
	})
	tn.MaxEnergy = 2_000_000_000
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	served := make(chan int, 1)
	go func() {
		req := httptest.NewRequest(http.MethodGet, "http://localhost/r", nil).WithContext(ctx)
		rec := httptest.NewRecorder()
		tn.Serve(rec, req)
		served <- rec.Code
	}()
	for range 2 {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("the functions did not start")
		}
	}
	cancel()
	select {
	case <-served:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("the request had not ended 500ms after it was cancelled: its functions ran on")
	}
	if running := tn.appRuntime.Tasks().Running(); running != 0 {
		t.Fatalf("%d functions of the wait still counted after it returned", running)
	}
	failed := records.named("Kitwork runtime execution failed")
	if len(failed) != 1 || failed[0]["code"] != "EXECUTION_CANCELLED" || failed[0]["function"] != "spin" {
		t.Fatalf("the request ended with %v, want one EXECUTION_CANCELLED in spin", failed)
	}
}

// generationTenant serves files as one site generation of an app it does not own, as the engine serves a
// site: closing it is the generation replaced (or the site removed), while the app goes on.
func generationTenant(t *testing.T, files map[string]string, energy uint64) (*Tenant, *app.Runtime) {
	t.Helper()
	all := map[string]string{"router.work": `import * as router from "kitwork/router";`}
	for path, source := range files {
		all[path] = routineHeader + source
	}
	root := writeSite(t, all)
	appRuntime := app.NewRuntime(ResolveIdentity(root, "localhost"))
	t.Cleanup(appRuntime.Close)
	siteRuntime, err := appRuntime.Site(root, "localhost")
	if err != nil {
		t.Fatal(err)
	}
	generation, err := siteRuntime.PrepareGeneration()
	if err != nil {
		t.Fatal(err)
	}
	tn := NewTenantWithRuntime(root, "localhost", appRuntime, siteRuntime, generation)
	tn.MaxEnergy = energy
	if err := tn.Run(); err != nil {
		t.Fatal(err)
	}
	return tn, appRuntime
}

// A routine of go outlives its request and belongs to the generation that started it: Close (the
// generation replaced, the site removed) stops it and waits for it while the app goes on, writes one line
// for it, and starts none after. (The app's own close stops it too: TestRoutineGoStopsWhenTheAppCloses. An
// idle site is not evicted while it runs: core's TestIdleSiteWithARunningRoutineIsNotEvicted.)
//
// CONTROL: with Tenant.Close not stopping its routines (generation replacement leaving app work alone, as
// STABILITY.md §4 said before), Close returned with the routine spinning on: "app closed false, 1
// routines counted".
func TestRoutineGoStopsWhenTheTenantCloses(t *testing.T) {
	records := captureSlog(t)
	spinning := make(chan struct{}, 1)
	notifyHook(t, func(name string, _ ...value.Value) {
		if name == "spinning" {
			spinning <- struct{}{}
		}
	})
	tn, appRuntime := generationTenant(t, map[string]string{
		"r/router.work": `const spin = (n) => { testNotify("spinning"); let x = 0; for (let i = 0; i < n; i++) { x = x + i; } return x; };
router.get((context) => { routine.go(() => spin(1000000000)); return context.status(202).json("started"); });`,
	}, 2_000_000_000)
	if code, _, body := routineJSON(t, tn, "/r"); code != http.StatusAccepted {
		t.Fatalf("/r = %d %s", code, body)
	}
	select {
	case <-spinning:
	case <-time.After(2 * time.Second):
		t.Fatal("the routine did not start")
	}
	closed := make(chan struct{})
	go func() {
		tn.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("Close had not returned after 3s: the routine of go was not stopped")
	}
	if appRuntime.Closed() || appRuntime.Tasks().Running() != 0 {
		t.Fatalf("after the generation closed: app closed %v, %d routines counted; want the app open, none", appRuntime.Closed(), appRuntime.Tasks().Running())
	}
	if stopped := records.named("Kitwork routine stopped"); len(stopped) != 1 || stopped[0]["code"] != "EXECUTION_CANCELLED" || stopped[0]["level"] != "WARN" {
		t.Fatalf("want one line for the routine stopped, got %v", stopped)
	}
	refused := (&Routine{tenant: tn}).start(nil, []value.Value{routineLambda(t, `() => 1`)})
	if failure, _ := value.FailureFrom(refused); failure.Code != value.UnavailableCode {
		t.Fatalf("go after Close = %v, want UNAVAILABLE", refused)
	}
}

// Close waits for a routine held in a host call (a stop is cooperative: a VM stops at its next checkpoint,
// a host call when it returns) before the generation retires, within routineStopGrace (past it:
// TestRoutineCloseWaitsNoLongerThanTheGrace): nothing of it runs against what Close releases.
//
// CONTROL: with stopRoutines not waiting for the routines it stopped, Close returned while the routine was
// still held: "Close returned while a routine was running".
func TestRoutineCloseWaitsForARoutineInAHostCall(t *testing.T) {
	inside, release := newGate(), newGate()
	var ended atomic.Bool
	notifyHook(t, func(name string, _ ...value.Value) {
		if name == "inside" {
			inside.open()
			release.wait(t, "release")
			ended.Store(true)
		}
	})
	tn, _ := generationTenant(t, map[string]string{
		"r/router.work": `router.get((context) => { routine.go(() => testNotify("inside")); return context.json("started"); });`,
	}, 1_000_000)
	if code, _, body := routineJSON(t, tn, "/r"); code != http.StatusOK {
		t.Fatalf("/r = %d %s", code, body)
	}
	inside.wait(t, "the routine in its host call")
	closed := make(chan struct{})
	go func() {
		tn.Close()
		close(closed)
	}()
	select {
	case <-closed:
		t.Fatal("Close returned while a routine was running")
	case <-time.After(150 * time.Millisecond):
	}
	release.open()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not return once the routine stopped")
	}
	if !ended.Load() {
		t.Fatal("Close returned before the routine's host call did")
	}
}

// routineLambda is a function of a program compiled and run on its own, as a host would hand it to the
// module.
func routineLambda(t *testing.T, source string) value.Value {
	t.Helper()
	bytecode, err := compiler.CompileSource("var made = " + source + ";")
	if err != nil {
		t.Fatalf("compile %s: %v", source, err)
	}
	vm := runtime.New(bytecode.Program)
	vm.Globals = map[string]value.Value{}
	if result := vm.Run(); result.K == value.Invalid {
		t.Fatalf("run %s: %v", source, result.V)
	}
	made := vm.Vars["made"]
	if !made.IsCallable() {
		t.Fatalf("%s did not make a function", source)
	}
	return made
}

// Every routine of go runs its own VM on its own copy: many at once, each to its end, each seeing the
// outer object as go() saw it, and the caller seeing none of their writes.
//
// CONTROL: with start handing each routine the function as written (no copy), the ten routines wrote
// hits from ten goroutines: the process died, "fatal error: concurrent map read and map write".
func TestRoutineGoConcurrentIsolation(t *testing.T) {
	var ran atomic.Int32
	notifyHook(t, func(name string, args ...value.Value) {
		if name == "ran" {
			ran.Add(int32(args[0].N))
		}
	})
	tn := routineSite(t, map[string]string{
		"r/router.work": `router.get((context) => {
  const hits = { n: 0 };
  const handles = [];
  for (let i = 1; i <= 10; i++) {
    const box = { i: i };
    handles.push(routine.go(() => {
      for (let k = 0; k < 500; k++) { hits.n = hits.n + 1; }
      box.i = box.i * 10 + hits.n - 500;
      testNotify("ran", box.i);
      return box.i;
    }));
  }
  const results = [];
  for (const handle of handles) { results.push(routine.wait(handle)); }
  return context.json({ results, hits: hits.n });
});`,
	})
	if code, _, body := routineJSON(t, tn, "/r"); code != http.StatusOK || body != `{"hits":0,"results":[10,20,30,40,50,60,70,80,90,100]}` {
		t.Fatalf("/r = %d %s", code, body)
	}
	if ran.Load() != 550 {
		t.Fatalf("the routines reported %d, want 550", ran.Load())
	}
}

// In a function of wait the request reads (context.query, context.request.json), one function at a time;
// answering it is the caller's: context.json in a function of wait is an error. A function of go reaches
// no part of the request, by whatever name it holds it.
//
// CONTROL: with the copy leaving the request's objects as they are (no routineRequestRules), the
// function of wait answered the request itself (/answer gave 1, the error never came) and the function of
// go read the query of a request that was over.
func TestRoutineRequestInsideARoutine(t *testing.T) {
	tn := routineSite(t, map[string]string{
		"read/router.work":   `router.get((context) => context.json(routine.wait(() => context.query("q"), () => context.request.query("q"))));`,
		"answer/router.work": `router.get((context) => context.json(safe(() => routine.wait(() => context.json(1))).error));`,
		"later/router.work": `router.get((context) => {
  const held = context;
  return context.json(safe(() => routine.wait(routine.go(() => held.query("q")))).error);
});`,
	})
	if code, _, body := routineJSON(t, tn, "/read?q=x"); code != http.StatusOK || body != `["x","x"]` {
		t.Fatalf("/read = %d %s", code, body)
	}
	code, answer, body := routineJSON(t, tn, "/answer")
	if code != http.StatusOK || answer["code"] != "INVALID_ARGUMENT" ||
		answer["message"] != "context.json trong hàm của routine.wait: trả lời ở hàm gọi routine.wait, sau khi nó trả kết quả" {
		t.Fatalf("/answer = %d %s", code, body)
	}
	code, later, body := routineJSON(t, tn, "/later?q=x")
	if code != http.StatusOK || later["message"] != "context.query trong hàm của routine.go: `go` chạy sau khi request xong: lấy giá trị cần trước rồi đưa vào" {
		t.Fatalf("/later = %d %s", code, body)
	}
}

// kitwork().go is gone: reading it is the error that names routine.go of kitwork/routine.
//
// CONTROL: with MissingMember not answering go, the call was "k.go is not a function" (NOT_A_FUNCTION).
func TestKitworkGoIsGone(t *testing.T) {
	tn := routineSite(t, map[string]string{
		"r/router.work": `router.get((context) => { const k = kitwork(); return context.json(safe(() => k.go(() => 1)).error); });`,
	})
	code, answer, body := routineJSON(t, tn, "/r")
	if code != http.StatusOK || answer["code"] != "INVALID_ARGUMENT" ||
		answer["message"] != "go của \"kitwork\" đã bỏ: `go` nằm trong `kitwork/routine`: `import * as routine from \"kitwork/routine\"` rồi `routine.go(fn)`" {
		t.Fatalf("/r = %d %s", code, body)
	}
}

// import { go, wait } from "kitwork/routine" runs (mục 2): a word read by name is still handed its caller.
//
// CONTROL: with callHost not knowing value.CallerFunction, each word called alone gave null:
// [null,null,null].
func TestRoutineWordsImportedByName(t *testing.T) {
	tn := routineSite(t, map[string]string{
		"r/router.work": `import { go, wait, retry } from "kitwork/routine";
router.get((context) => context.json([wait(go(() => 1)), wait(() => 2, () => 3), retry(1).wait(() => 4)]));`,
	})
	if code, _, body := routineJSON(t, tn, "/r"); code != http.StatusOK || body != `[1,[2,3],4]` {
		t.Fatalf("/r = %d %s", code, body)
	}
}

// A method of the request read without its call (const header = context.header) is still the request's:
// the copy sees it with its object. Two functions of one wait that answer through it are refused, as
// context.header(…) written there is, and a read through one (const query = context.query) runs; a
// function of go that calls it is refused as any use of the request is. No map of the response is written
// from two goroutines, nor after the answer has gone.
//
// CONTROL: with the copy leaving a value.BoundMethod as it is, both functions of the wait set their header
// (refused was null, X-A and X-B on the answer) and -race reported the two writes of the response's
// headers; the function of go set X-Late on a request that was over.
func TestRoutineRequestMethodReadAsAValue(t *testing.T) {
	tn := routineSite(t, map[string]string{
		"wait/router.work": `router.get((context) => {
  const header = context.header;
  const query = context.query;
  const read = routine.wait(() => query("q"), () => query("q"));
  const refused = safe(() => routine.wait(() => { header("X-A", "1"); return "a"; }, () => { header("X-B", "2"); return "b"; })).error;
  return context.json({ read, refused });
});`,
		"go/router.work": `router.get((context) => {
  const header = context.header;
  const task = routine.go(() => { header("X-Late", "1"); return "late"; });
  return context.json(safe(() => routine.wait(task)).error);
});`,
	})
	rec := serveGet(tn, "/wait?q=x", nil)
	var answer struct {
		Read    []string       `json:"read"`
		Refused map[string]any `json:"refused"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &answer)
	cause, _ := answer.Refused["cause"].(map[string]any)
	if rec.Code != http.StatusOK || len(answer.Read) != 2 || answer.Read[0] != "x" || answer.Read[1] != "x" ||
		answer.Refused["code"] != "INVALID_ARGUMENT" || cause == nil ||
		cause["message"] != "context.header trong hàm của routine.wait: trả lời ở hàm gọi routine.wait, sau khi nó trả kết quả" {
		t.Fatalf("/wait = %d %s, want the reads and the header refused", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("X-A") != "" || rec.Header().Get("X-B") != "" {
		t.Fatalf("a function of wait answered the request: X-A %q, X-B %q", rec.Header().Get("X-A"), rec.Header().Get("X-B"))
	}
	rec = serveGet(tn, "/go", nil)
	var refused map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &refused)
	if rec.Code != http.StatusOK || refused["code"] != "INVALID_ARGUMENT" ||
		refused["message"] != "context.header trong hàm của routine.go: `go` chạy sau khi request xong: lấy giá trị cần trước rồi đưa vào" {
		t.Fatalf("/go = %d %s, want the header refused", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("X-Late") != "" {
		t.Fatalf("a function of go answered the request: X-Late %q", rec.Header().Get("X-Late"))
	}
}

// go(fn) written bare is the compile error of mục 2 wherever it is written, even when another file of
// the bundle imports go by name: that go is the other file's, and this call's go is the global no one
// answers any more. The file that imports it uses it.
//
// CONTROL: with checkBareGo off whenever the bundle declares a go somewhere (as it was), the router
// booted and the call failed at run time, "go không phải hàm (là null)", which does not name routine.go.
func TestRoutineBareGoBesideAFileThatImportsGo(t *testing.T) {
	files := map[string]string{
		"router.work":    `import * as router from "kitwork/router";`,
		"lib/later.work": "import { go } from \"kitwork/routine\";\nexport const later = (f) => go(f);\n",
		"b/router.work":  "import * as router from \"kitwork/router\";\nimport { later } from \"../lib/later.work\";\nimport * as routine from \"kitwork/routine\";\nrouter.get((context) => { const task = later(() => 7); return context.json(routine.wait(task)); });\n",
	}
	tn := NewTenant(writeSite(t, files), "localhost")
	t.Cleanup(tn.Close)
	if err := tn.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	if code, _, body := routineJSON(t, tn, "/b"); code != http.StatusOK || body != "7" {
		t.Fatalf("/b = %d %s, want 7: the go the library imports runs", code, body)
	}
	files["a/router.work"] = "import * as router from \"kitwork/router\";\nimport { later } from \"../lib/later.work\";\nrouter.get((context) => { go(() => 1); return context.json(1); });\n"
	bare := NewTenant(writeSite(t, files), "localhost")
	t.Cleanup(bare.Close)
	err := bare.Run()
	if err == nil || !strings.Contains(err.Error(), "router.work:3:27: "+compiler.RoutineGoMoved) {
		t.Fatalf("run = %v, want the bare go of a/router.work refused at 3:27", err)
	}
}

// A file only declares as it loads, however the module's word is reached: routine.wait at a router's top
// level, and a word kept in an object and called after a dot, are refused where they are written, and
// the site does not boot.
//
// CONTROL: with routineLoading not refusing wait, the first site booted ("run = <nil>"); with INVOKE of
// an object's key leaving a value.CallerFunction to its own Call (no VM), the word kept in an object ran
// at load and the second site booted.
func TestRoutineWaitRefusedAtFileLevel(t *testing.T) {
	var ran atomic.Int32
	notifyHook(t, func(name string, _ ...value.Value) {
		if name == "ran" {
			ran.Add(1)
		}
	})
	for _, c := range []struct{ name, source, want string }{
		{"wait", "routine.wait(() => testNotify(\"ran\"));\nrouter.get((context) => context.json(\"loaded\"));",
			"router.work:3:8: routine.wait ở cấp file: lúc nạp, file chỉ khai; gọi routine.wait trong một handler, một job, một lịch hay một hàm (KITWORK_ROUTINE.md mục 9)"},
		{"a word kept in an object", "const words = { all: routine.wait };\nconst started = words.all(() => testNotify(\"ran\"));\nrouter.get((context) => context.json(started));",
			"router.work:4:22: routine.wait ở cấp file: lúc nạp, file chỉ khai; gọi routine.wait trong một handler, một job, một lịch hay một hàm (KITWORK_ROUTINE.md mục 9)"},
	} {
		tn := NewTenant(writeSite(t, map[string]string{
			"router.work":     `import * as router from "kitwork/router";`,
			"top/router.work": routineHeader + c.source,
		}), "localhost")
		err := tn.Run()
		tn.Close()
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: run = %v, want %s", c.name, err, c.want)
		}
	}
	if ran.Load() != 0 {
		t.Fatalf("a function of a file's top level ran %d times", ran.Load())
	}
}

// A file of .shared/queue/ and one of .schedule/ only declare as they load too: routine.go at their top
// level is refused, said on the server's output with its place, and the file registers nothing, as a
// file whose top level fails does (background_runtime_error_test.go).
//
// CONTROL: with runQueueFile and runScheduleFile not marking their VMs as loading, the two routines ran as
// the files loaded and both files registered their job and schedule.
func TestRoutineRefusedAtTheTopOfAJobOrAScheduleFile(t *testing.T) {
	var ran atomic.Int32
	notifyHook(t, func(name string, _ ...value.Value) {
		if name == "ran" {
			ran.Add(1)
		}
	})
	tmp := t.TempDir()
	files := map[string]string{
		"localhost/router.kitwork.js": `import { router } from "kitwork"; router.get((ctx) => ctx.text("ok"));`,
		".shared/queue/later.work":    "import * as queue from \"kitwork/queue\";\nimport * as routine from \"kitwork/routine\";\nroutine.go(() => testNotify(\"ran\"));\nexport const later = queue.job((context) => 1);\n",
		".schedule/hourly.work":       "import * as schedule from \"kitwork/schedule\";\nimport * as routine from \"kitwork/routine\";\nroutine.go(() => testNotify(\"ran\"));\nexport const hourly = schedule.every(\"1h\").run((context) => 1);\n",
	}
	for path, content := range files {
		full := filepath.Join(tmp, "acme", filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
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
	tenant := NewAppTenant(tmp, "acme")
	runErr := tenant.Run()
	os.Stdout = saved
	writer.Close()
	<-copied
	if runErr != nil {
		t.Fatal(runErr)
	}
	defer tenant.Close()
	output := printed.String()
	for _, want := range []string{
		"[Queue] run .shared/queue/later.work: later.work:3:8: routine.go ở cấp file: lúc nạp, file chỉ khai; gọi routine.go trong một handler, một job, một lịch hay một hàm (KITWORK_ROUTINE.md mục 9)",
		"[Schedule] run .schedule/hourly.work: hourly.work:3:8: routine.go ở cấp file: lúc nạp, file chỉ khai; gọi routine.go trong một handler, một job, một lịch hay một hàm (KITWORK_ROUTINE.md mục 9)",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("the server's output lacks %q:\n%s", want, output)
		}
	}
	scheduler, worker := tenant.scheduleWorker(), tenant.queueWorker()
	scheduler.mu.Lock()
	schedules := len(scheduler.handlers)
	scheduler.mu.Unlock()
	worker.mu.Lock()
	jobs := len(worker.handlers)
	worker.mu.Unlock()
	if schedules != 0 || jobs != 0 || ran.Load() != 0 {
		t.Fatalf("%d schedules and %d jobs registered, %d routines ran; want none", schedules, jobs, ran.Load())
	}
}

// wait(fn…) and wait(h…) wait for every function before they answer, a failure among them included
// (mục 6: "không bao giờ bỏ dở các hàm còn đang chạy"): here the first fails at once and the second is
// held. The wait has not answered while the second runs; it answers the first's failure once the second
// has ended, and nothing of the wait is left counted.
//
// CONTROL: with waitFunctions returning as soon as the first function in input order had failed, the
// route answered while its second function was held: "/functions answered while its second function ran".
// With waitHandles returning at the first failure, "/handles answered while its second function ran".
func TestRoutineWaitWaitsForAllBeforeItFails(t *testing.T) {
	var held, release *gate
	var finished atomic.Int32
	notifyHook(t, func(name string, _ ...value.Value) {
		if name == "hold" {
			held.open()
			release.wait(t, "release")
			finished.Add(1)
		}
	})
	tn := routineSite(t, map[string]string{
		"functions/router.work": `router.get((context) => context.json(safe(() => routine.wait(() => fail("first"), () => { testNotify("hold"); return 2; })).error));`,
		"handles/router.work": `router.get((context) => {
  const a = routine.go(() => fail("first"));
  const b = routine.go(() => { testNotify("hold"); return 2; });
  return context.json(safe(() => routine.wait(a, b)).error);
});`,
	})
	for _, c := range []struct{ path, noun string }{{"/functions", "hàm"}, {"/handles", "handle"}} {
		held, release = newGate(), newGate()
		served := make(chan *httptest.ResponseRecorder, 1)
		go func() { served <- serveGet(tn, c.path, nil) }()
		if !held.wait(t, c.path+": the second function held") {
			return
		}
		select {
		case rec := <-served:
			release.open()
			t.Fatalf("%s answered while its second function ran: %d %s", c.path, rec.Code, rec.Body.String())
		case <-time.After(100 * time.Millisecond):
		}
		release.open()
		var rec *httptest.ResponseRecorder
		select {
		case rec = <-served:
		case <-time.After(2 * time.Second):
			t.Fatalf("%s did not answer once its second function ended", c.path)
		}
		var failure map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &failure)
		if rec.Code != http.StatusOK || failure["code"] != "FAIL" || failure["message"] != "routine.wait: "+c.noun+" thứ 1 trong 2 hỏng: first" {
			t.Fatalf("%s = %d %s, want the first's failure", c.path, rec.Code, rec.Body.String())
		}
		for deadline := time.Now().Add(2 * time.Second); tn.appRuntime.Tasks().Running() > 0 && time.Now().Before(deadline); {
			time.Sleep(time.Millisecond)
		}
		if running := tn.appRuntime.Tasks().Running(); running != 0 {
			t.Fatalf("%s: %d routines still counted", c.path, running)
		}
	}
	if finished.Load() != 2 {
		t.Fatalf("%d held functions ended, want 2", finished.Load())
	}
}

// The functions of one wait read the request one at a time: the body, which the request reads once and
// keeps, is read by four of them at once without a race (go test -race), and each gets it.
//
// CONTROL (under -race): with routineRequestView.locked not taking the wait's lock, the race detector
// reported the four functions reading the body: "DATA RACE … (*Request).readBody".
func TestRoutineWaitFunctionsReadTheRequestOneAtATime(t *testing.T) {
	tn := routineSite(t, map[string]string{
		"body/router.work": `router.post((context) => context.json(routine.wait(
  () => context.request.json().a,
  () => context.request.json().a,
  () => context.request.json().a,
  () => context.request.json().a
)));`,
	})
	req := httptest.NewRequest(http.MethodPost, "http://localhost/body", strings.NewReader(`{"a":5}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	tn.Serve(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != `[5,5,5,5]` {
		t.Fatalf("/body = %d %s, want [5,5,5,5]", rec.Code, rec.Body.String())
	}
}

// cancel(handle) of a routine that is running stops it at its next checkpoint, and a wait of it fails
// CANCELLED, which safe catches, at once: the spin it was in had energy for seconds.
//
// CONTROL: with routineAttempt running the VM under context.Background(), the cancel never reached it and
// the wait answered when the spin's energy ran out: "/running took 25.6s". With runRoutine giving back a
// stopped VM's failure as it is, the wait failed EXECUTION_CANCELLED, which safe does not catch: "/running
// = 500".
func TestRoutineCancelStopsARunningRoutine(t *testing.T) {
	spinning := newGate()
	notifyHook(t, func(name string, _ ...value.Value) {
		switch name {
		case "spinning":
			spinning.open()
		case "when-spinning":
			spinning.wait(t, "the routine spinning")
		}
	})
	tn := routineSite(t, map[string]string{
		"running/router.work": `const spin = (n) => { testNotify("spinning"); let x = 0; for (let i = 0; i < n; i++) { x = x + i; } return x; };
router.get((context) => {
  const task = routine.go(() => spin(1000000000));
  testNotify("when-spinning");
  routine.cancel(task);
  return context.json(safe(() => routine.wait(task)).error);
});`,
	})
	tn.MaxEnergy = 2_000_000_000
	started := time.Now()
	code, cancelled, body := routineJSON(t, tn, "/running")
	if took := time.Since(started); took > time.Second {
		t.Fatalf("/running took %s, want the spin stopped at once", took)
	}
	at, _ := cancelled["at"].(string)
	if code != http.StatusOK || cancelled["code"] != "CANCELLED" || cancelled["message"] != "routine đã bị huỷ bằng routine.cancel(handle)" ||
		!strings.HasPrefix(at, "router.work:3:") {
		t.Fatalf("/running = %d %s, want CANCELLED where the spin stopped", code, body)
	}
}

// Each attempt of retry starts from what the call saw, never from what the attempt before it left: an
// outer variable the function writes is its copy's, and the copy is made again for each attempt.
//
// CONTROL: with runRoutine running every attempt on the one copy wait made, the second attempt read the
// first's write and returned 2: "/fresh = 200 null".
func TestRoutineRetryStartsEachAttemptFromTheCopy(t *testing.T) {
	tn := routineSite(t, map[string]string{
		"fresh/router.work": `router.get((context) => {
  let n = 0;
  return context.json(safe(() => routine.retry(1).wait(() => { n = n + 1; if (n < 2) { fail("try " + n); } return n; })).error);
});`,
	})
	if code, failed, body := routineJSON(t, tn, "/fresh"); code != http.StatusOK || failed["code"] != "FAIL" || failed["message"] != "try 1" {
		t.Fatalf("/fresh = %d %s, want FAIL try 1: both attempts start from n = 0", code, body)
	}
}

// The app's close stops its routines of go wherever the generation that started them is: Close returns
// at once, none is left counted, and the routine stopped is one warning of the log.
//
// CONTROL: with the routine not linked to the app's tasks (no context.AfterFunc), the app's close waited
// for the spin: "the app's Close had not returned after 2s".
func TestRoutineGoStopsWhenTheAppCloses(t *testing.T) {
	records := captureSlog(t)
	spinning := make(chan struct{}, 1)
	notifyHook(t, func(name string, _ ...value.Value) {
		if name == "spinning" {
			spinning <- struct{}{}
		}
	})
	tn, appRuntime := generationTenant(t, map[string]string{
		"r/router.work": `const spin = (n) => { testNotify("spinning"); let x = 0; for (let i = 0; i < n; i++) { x = x + i; } return x; };
router.get((context) => { routine.go(() => spin(1000000000)); return context.status(202).json("started"); });`,
	}, 2_000_000_000)
	t.Cleanup(tn.Close)
	if code, _, body := routineJSON(t, tn, "/r"); code != http.StatusAccepted {
		t.Fatalf("/r = %d %s", code, body)
	}
	select {
	case <-spinning:
	case <-time.After(2 * time.Second):
		t.Fatal("the routine did not start")
	}
	closed := make(chan struct{})
	go func() {
		appRuntime.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("the app's Close had not returned after 2s: its routine of go was not stopped")
	}
	if running := appRuntime.Tasks().Running(); running != 0 {
		t.Fatalf("%d routines counted after the app closed", running)
	}
	if stopped := records.named("Kitwork routine stopped"); len(stopped) != 1 || stopped[0]["code"] != "EXECUTION_CANCELLED" || stopped[0]["level"] != "WARN" {
		t.Fatalf("want one line for the routine stopped, got %v", stopped)
	}
}

// Close waits routineStopGrace at most for a routine held in a host call that does not hear the stop:
// past it the generation goes on closing, says so in one line, and the routine ends on its own when its
// host call returns. A hot reload is held no longer than that.
//
// CONTROL: with stopRoutines waiting for its routines without a bound, Close returned only when the
// routine's host call did: "Close took 2.0s".
func TestRoutineCloseWaitsNoLongerThanTheGrace(t *testing.T) {
	saved := routineStopGrace
	routineStopGrace = 100 * time.Millisecond
	t.Cleanup(func() { routineStopGrace = saved })
	records := captureSlog(t)
	inside, release := newGate(), newGate()
	notifyHook(t, func(name string, _ ...value.Value) {
		if name == "inside" {
			inside.open()
			select {
			case <-release.ch:
			case <-time.After(2 * time.Second):
			}
		}
	})
	tn, _ := generationTenant(t, map[string]string{
		"r/router.work": `router.get((context) => { routine.go(() => testNotify("inside")); return context.json("started"); });`,
	}, 1_000_000)
	if code, _, body := routineJSON(t, tn, "/r"); code != http.StatusOK {
		t.Fatalf("/r = %d %s", code, body)
	}
	inside.wait(t, "the routine in its host call")
	started := time.Now()
	tn.Close()
	took := time.Since(started)
	release.open()
	if took < routineStopGrace || took > time.Second {
		t.Fatalf("Close took %s, want the grace of %s", took, routineStopGrace)
	}
	still := records.named("Kitwork routines still running after their generation closed")
	if len(still) != 1 || still[0]["running"] != "1" || still[0]["waited"] != "100ms" || still[0]["level"] != "WARN" || still[0]["site"] != "localhost" {
		t.Fatalf("want one line for the routine left running, got %v", still)
	}
	for deadline := time.Now().Add(2 * time.Second); tn.RunningRoutines() > 0 && time.Now().Before(deadline); {
		time.Sleep(time.Millisecond)
	}
	if running := tn.RunningRoutines(); running != 0 {
		t.Fatalf("%d routines running once the host call returned", running)
	}
}

// In a function of wait, every member that answers the request or edits its answer is refused, with its
// name, not only context.json; and the mixes the compiler cannot follow (handles out of an array) are
// refused when the wait runs, in the words of the compile error.
//
// CONTROL: with "status" out of routineContextAnswers and header and cookie with a value let through, the
// functions answered: /status = 201 null, /header and /cookie = 200 null. With routine.wait not checking a
// mix of functions and handles, /mixed waited for both: "/mixed = 200 null"; with it not checking rules on
// handles, "/rules = 200 null".
func TestRoutineRefusalsWhenTheWaitRuns(t *testing.T) {
	answer := func(member, call string) (string, string) {
		return member + "/router.work", `router.get((context) => context.json(safe(() => routine.wait(() => context.` + call + `)).error));`
	}
	files := map[string]string{
		"mixed/router.work": `router.get((context) => { const handles = [routine.go(() => 1)]; return context.json(safe(() => routine.wait(() => 1, handles[0])).error); });`,
		"rules/router.work": `router.get((context) => { const handles = [routine.go(() => 1)]; return context.json(safe(() => routine.retry(1).wait(handles[0])).error); });`,
	}
	members := []struct{ member, call string }{
		{"status", `status(201)`}, {"header", `header("X-A", "1")`}, {"cookie", `cookie("a", "b")`}, {"title", `title("t")`},
	}
	for _, m := range members {
		path, source := answer(m.member, m.call)
		files[path] = source
	}
	tn := routineSite(t, files)
	for _, m := range members {
		code, refused, body := routineJSON(t, tn, "/"+m.member)
		want := "context." + m.member + " trong hàm của routine.wait: trả lời ở hàm gọi routine.wait, sau khi nó trả kết quả"
		if code != http.StatusOK || refused["code"] != "INVALID_ARGUMENT" || refused["message"] != want {
			t.Errorf("/%s = %d %s, want %s", m.member, code, body, want)
		}
	}
	for path, want := range map[string]string{"/mixed": compiler.RoutineWaitMixed, "/rules": compiler.RoutineRulesOnHandle} {
		code, refused, body := routineJSON(t, tn, path)
		if code != http.StatusOK || refused["code"] != "INVALID_ARGUMENT" || refused["message"] != want {
			t.Errorf("%s = %d %s, want %s", path, code, body, want)
		}
	}
}
