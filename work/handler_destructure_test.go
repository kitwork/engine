package work

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kitwork/engine/value"
)

// A route handler takes apart only the objects of its context, request and response; taking a
// function out (({ json }), ({ view }), ({ data }), ({ request: { body } })) loses its `this` in
// JavaScript and stops the boot, at its place, saying how to write it and what may be taken (19/10/2026).
func TestRouteHandlerDestructuringAFunctionIsAnError(t *testing.T) {
	const list = "tên tách được: request, response"
	tests := []struct {
		name, route string
		want        []string
	}{
		{"json", "router.get(({ json }) => json({ a: 1 }));",
			[]string{"router.kitwork.js:2:", "`json` là hàm của `context`; tách ra thì trong JavaScript nó mất `this`: viết `context.json(…)`, hoặc tách `response` rồi `response.json(…)`", list}},
		{"view", "router.get(({ response, view }) => view());",
			[]string{"`view` là hàm của `context`", "hoặc tách `response` rồi `response.view(…)`", list}},
		{"data", "router.get(({ data }) => data());",
			[]string{"`data` là hàm của `context`; tách ra thì trong JavaScript nó mất `this`: viết `context.data(…)`", list}},
		{"request side", "router.post(({ body }) => body());",
			[]string{"`body` là hàm của `context`", "hoặc tách `request` rồi `request.body(…)`", list}},
		{"nested", "router.post(({ request: { body } }) => body());",
			[]string{"`body` là hàm của `request`; tách ra thì trong JavaScript nó mất `this`: tách `request` rồi viết `request.body(…)`", list}},
		{"guard", "router.get((context) => context.text(\"ok\")).guard(({ status }) => status(403));",
			[]string{"`status` là hàm của `context`", "hoặc tách `response` rồi `response.status(…)`", list}},
		{"type", "router.get(({ type }) => type(\"text/csv\"));",
			[]string{"`type` là hàm của `context`", "hoặc tách `response` rồi `response.type(…)`", list}},
		{"not on context", "router.get(({ shop }) => 1);",
			[]string{"`shop` không có trong `context` của một route", list}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tmp := writeSite(t, map[string]string{
				"filesystem.kitwork": "",
				"router.kitwork.js":  "import * as router from \"kitwork/router\";\n" + test.route + "\n",
			})
			err := NewTenant(tmp, "localhost").Run()
			if err == nil {
				t.Fatal("boot passed")
			}
			for _, want := range test.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("run error = %v\nwant substring %q", err, want)
				}
			}
		})
	}
}

// Destructuring anywhere else stays as JavaScript has it: a function of the file, a callback inside a
// handler, a handler taking request and response apart.
func TestDestructuringOutsideTheHandlerParameterStays(t *testing.T) {
	tmp := writeSite(t, map[string]string{
		"filesystem.kitwork": "",
		"router.kitwork.js": "import * as router from \"kitwork/router\";\n" +
			"const label = ({ json, data }) => json + \"/\" + data;\n" +
			"router.get(({ request, response }) => {\n" +
			"  const ids = [{ id: 1 }, { id: 2 }].map(({ id }) => id * 10);\n" +
			"  const { json } = { json: \"j\" };\n" +
			"  return response.text(label({ json: json, data: ids.join(\",\") }) + \" \" + request.query(\"q\"));\n" +
			"});\n",
	})
	tn := NewTenant(tmp, "localhost")
	if err := tn.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	if r := serveGet(tn, "/?q=x", nil); r.Code != http.StatusOK || r.Body.String() != "j/10,20 x" {
		t.Fatalf("GET: %d %q", r.Code, r.Body.String())
	}
}

// A job or a schedule takes apart job, and nothing else of its context.
func TestJobHandlerDestructuring(t *testing.T) {
	refused := &value.Lambda{Params: []string{"{ log }"}, SourceFile: "_cron/greet.kitwork.js", SourceLine: 2, SourceColumn: 14}
	err := destructureError(refused, jobDestructurable, jobSide)
	if err == nil {
		t.Fatal("({ log }) passed")
	}
	for _, want := range []string{"greet.kitwork.js:2:14", "`log` là của `context`; tách ra thì trong JavaScript nó mất `this`", "tên tách được: job"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %v\nwant substring %q", err, want)
		}
	}
	if err := destructureError(&value.Lambda{Params: []string{"{ job }"}}, jobDestructurable, jobSide); err != nil {
		t.Errorf("({ job }) refused: %v", err)
	}
	if err := destructureError(&value.Lambda{Params: []string{"job"}}, jobDestructurable, jobSide); err != nil {
		t.Errorf("(job) refused: %v", err)
	}
	// a queue job's context.data() is a function (KITWORK_QUEUE.md mục 5)
	data := &value.Lambda{Params: []string{"{ data }"}, SourceFile: ".shared/queue/greet.work", SourceLine: 3, SourceColumn: 30}
	err = destructureError(data, queueDestructurable, queueSide)
	if err == nil || !strings.Contains(err.Error(), "greet.work:3:30: `data` là hàm của `context`; tách ra thì trong JavaScript nó mất `this`: viết `context.data()`; tên tách được: job") {
		t.Errorf("({ data }) of a job: %v", err)
	}
}

// ({ job }) => … of a queue job gets context.job: its name, its attempt, its log.
func TestQueueHandlerTakesJobApart(t *testing.T) {
	watch := watchQueue(t)
	q := newQueueApp(t, map[string]string{
		".shared/queue/greet.work": `import * as queue from "kitwork/queue";
export const greet = queue.job(({ job }) => { job.log("hello " + job.name + " x" + job.attempt + "/" + job.maxAttempts); });
`,
		"localhost/send/router.work": `import * as router from "kitwork/router";
import { greet } from "app/.shared/queue/greet.work";
router.post((context) => context.json(greet.send({})));
`,
	})
	id := q.serve(t, http.MethodPost, "/send", url.Values{})["id"].(string)
	watch.until(t, id, "completed")
	if run := q.run(t, id); run.Log != "hello greet x1/1\n" {
		t.Fatalf("({ job }) handler logged %q", run.Log)
	}
}

// A job whose handler takes a function apart is not kept, as a file that does not compile.
func TestQueueHandlerDestructuringAFunctionIsRefused(t *testing.T) {
	q := newQueueApp(t, map[string]string{
		".shared/queue/greet.work": "import * as queue from \"kitwork/queue\";\nexport const greet = queue.job(({ log }) => { log(\"x\"); });\n",
	})
	if !strings.Contains(q.output, "[Queue] greet.work:2:42: `log` không có trong `context` của một job: context có data() và job; tên tách được: job") {
		t.Errorf("boot did not say why:\n%s", q.output)
	}
	worker := q.app.queueWorker()
	worker.mu.Lock()
	defer worker.mu.Unlock()
	if len(worker.handlers) != 0 {
		t.Fatalf("handler kept: %d", len(worker.handlers))
	}
}

// newCronTenant boots an app with one _cron file.
func newCronTenant(t *testing.T, name, source string) *Tenant {
	t.Helper()
	tmp := t.TempDir()
	site := filepath.Join(tmp, "acme", "localhost")
	cronDir := filepath.Join(tmp, "acme", "_cron")
	for _, dir := range []string{site, cronDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(site, "router.kitwork.js"), []byte(`import { router } from "kitwork"; router.get((ctx) => ctx.text("ok"));`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cronDir, name+".kitwork.js"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	tenant := NewAppTenant(tmp, "acme")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	return tenant
}

// A schedule takes job apart the same way, and one that takes a function apart is not kept.
func TestCronHandlerDestructuring(t *testing.T) {
	tenant := newCronTenant(t, "beat", `import { cron } from "kitwork";
cron.every("1s").handle(({ job }) => { job.log("tick " + job.attempt); }).keep("7d");`)
	ok := waitFor(8*time.Second, func() bool {
		var n int
		_ = tenant.scheduler().db.QueryRow(`SELECT COUNT(*) FROM cron_runs WHERE status='completed' AND output LIKE '%tick %'`).Scan(&n)
		return n >= 1
	})
	tenant.StopCronJobs()
	time.Sleep(250 * time.Millisecond)
	defer tenant.Close()
	if !ok {
		rows, _ := tenant.scheduler().db.Query(`SELECT status, COALESCE(output,''), COALESCE(error_message,'') FROM cron_runs`)
		for rows != nil && rows.Next() {
			var status, output, failure string
			_ = rows.Scan(&status, &output, &failure)
			t.Logf("run: %s %q %q", status, output, failure)
		}
		t.Fatal("({ job }) schedule never logged its attempt")
	}

	refused := newCronTenant(t, "beat", `import { cron } from "kitwork";
cron.every("1s").handle(({ log }) => { log("tick"); });`)
	defer refused.Close()
	if scheduler := refused.scheduler(); scheduler != nil {
		scheduler.mu.Lock()
		kept := len(scheduler.jobs)
		scheduler.mu.Unlock()
		if kept != 0 {
			t.Fatalf("schedule kept: %d", kept)
		}
	}
}
