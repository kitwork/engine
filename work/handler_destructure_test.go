package work

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

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
		"router.work": "import * as router from \"kitwork/router\";\n" +
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
	refused := &value.Lambda{Params: []string{"{ log }"}, SourceFile: ".schedule/greet.work", SourceLine: 2, SourceColumn: 14}
	err := destructureError(refused, scheduleDestructurable, scheduleSide)
	if err == nil {
		t.Fatal("({ log }) passed")
	}
	for _, want := range []string{"greet.work:2:14", "`log` không có trong `context` của một lịch: context có job", "tên tách được: job"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %v\nwant substring %q", err, want)
		}
	}
	if err := destructureError(&value.Lambda{Params: []string{"{ job }"}}, scheduleDestructurable, scheduleSide); err != nil {
		t.Errorf("({ job }) refused: %v", err)
	}
	if err := destructureError(&value.Lambda{Params: []string{"job"}}, scheduleDestructurable, scheduleSide); err != nil {
		t.Errorf("(job) refused: %v", err)
	}
	// a schedule carries no data (KITWORK_SCHEDULE.md mục 7)
	noData := &value.Lambda{Params: []string{"{ data }"}, SourceFile: ".schedule/greet.work", SourceLine: 3, SourceColumn: 30}
	if err := destructureError(noData, scheduleDestructurable, scheduleSide); err == nil || !strings.Contains(err.Error(), "greet.work:3:30: lịch không có context.data(): lịch không mang dữ liệu") {
		t.Errorf("({ data }) of a schedule: %v", err)
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
