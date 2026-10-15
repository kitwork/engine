package work

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kitwork/engine/value"
)

// developing runs the rest of a test as a site runs while it is being developed (AllowLocal): an error
// nothing caught answers its whole diagnostic, which a test that reads what an author is told needs.
// In production the answer carries only the status and the request id (writeFailure).
func developing(t *testing.T) {
	t.Helper()
	saved := AllowLocal
	AllowLocal = true
	t.Cleanup(func() { AllowLocal = saved })
}

// failureSite writes a filesystem-routed site (each folder's router.kitwork.js) and boots it while
// developing (AllowLocal) or in production. The site's log is captured; ask serves one request with an
// Accept header and the X-Request-ID it carries.
func failureSite(t *testing.T, developing bool, routers map[string]string) (ask func(path, accept, id string) *httptest.ResponseRecorder, logged *bytes.Buffer) {
	t.Helper()
	saved := AllowLocal
	AllowLocal = developing
	t.Cleanup(func() { AllowLocal = saved })
	root := t.TempDir()
	for folder, source := range routers {
		dir := filepath.Join(root, "acme", "localhost", filepath.FromSlash(folder))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "router.kitwork.js"), []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tenant := NewTenant(root, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(tenant.Close)
	logged = &bytes.Buffer{}
	savedLog := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logged, nil)))
	t.Cleanup(func() { slog.SetDefault(savedLog) })
	return func(path, accept, id string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, "http://localhost"+path, nil)
		if accept != "" {
			request.Header.Set("Accept", accept)
		}
		request.Header.Set("X-Request-ID", id)
		recorder := httptest.NewRecorder()
		tenant.Serve(recorder, request)
		return recorder
	}, logged
}

// failingRouters are routes whose error nothing catches, each with what its diagnostic says: a JSON
// handler, a plain handler, a guard before the handler, a handler that set a status before it failed,
// a JSON value JSON cannot carry, a generated output, and the .error() lifecycle: a handler that
// answers nothing and one that fails itself.
var failingRouters = map[string]struct{ path, said string }{
	"json":        {"/json", "order.total không phải hàm"},
	"plain":       {"/plain", "boom-plain"},
	"guard":       {"/guard", "boom-guard"},
	"afterstatus": {"/afterstatus", "boom-status"},
	"encode":      {"/encode", "không viết được câu trả lời thành JSON: JSON không có số +Inf"},
	"output":      {"/broken.xml", "requires title, description, and link"},
	"silent":      {"/silent", "boom-silent"},
	"errorfails":  {"/errorfails", "boom-handler"},
}

var failureRouters = map[string]string{
	"":            "import { router } from \"kitwork\";\nrouter.rss({ path: \"/broken.xml\", title: \"Broken\", items: [] });\nrouter.get((ctx) => ctx.json({ home: true }));\n",
	"json":        "import { router } from \"kitwork\";\nrouter.get((ctx) => {\n  const order = { total: 3 };\n  order.total(\"vnd\");\n  return ctx.json({ reached: true });\n});\n",
	"plain":       "import { router } from \"kitwork\";\nrouter.get(() => {\n  fail(\"boom-plain\");\n  return \"reached\";\n});\n",
	"guard":       "import { router } from \"kitwork\";\nrouter.guard(() => {\n  fail(\"boom-guard\");\n});\nrouter.get((ctx) => ctx.text(\"reached\"));\n",
	"afterstatus": "import { router } from \"kitwork\";\nrouter.get((ctx) => {\n  ctx.status(201);\n  fail(\"boom-status\");\n});\n",
	"encode":      "import { router } from \"kitwork\";\nrouter.get((ctx) => {\n  let ratio = 1;\n  for (let i = 0; i < 400; i++) {\n    ratio = ratio * 10;\n  }\n  return ctx.json({ ratio: ratio });\n});\n",
	"silent":      "import { router } from \"kitwork\";\nrouter.get(() => {\n  fail(\"boom-silent\");\n}).error((ctx, err) => {\n  const seen = err;\n});\n",
	"errorfails":  "import { router } from \"kitwork\";\nrouter.get(() => {\n  fail(\"boom-handler\");\n}).error(() => {\n  fail(\"boom-error-handler\");\n});\n",
	// CONTROLS: an .error() handler that answers, and an error the handler chose to show
	"handled": "import { router } from \"kitwork\";\nrouter.get(() => {\n  fail(\"boom-handled\");\n}).error((ctx, err) => ctx.status(503).json({ handled: true }));\n",
	"said":    "import { router } from \"kitwork\";\nrouter.get((ctx) => {\n  ctx.error(\"Thanh toán không thành công\");\n});\n",
}

// In production a request whose error nothing caught answers 500 with the status and the request id
// only (X-Request-ID): a page for a browser, JSON for a client that asked for JSON, text for any other.
// The diagnostic (message, stack, file:line:column) is in the site's log record, under the same request
// id. It used to be the body of the answer, in production too. CONTROLS: a route that works answers
// 200; an .error() handler's answer and an error the handler chose to show (ctx.error) are answered
// as written.
func TestUncaughtErrorShowsVisitorsOnlyTheRequestIDInProduction(t *testing.T) {
	ask, logged := failureSite(t, false, failureRouters)
	for name, route := range failingRouters {
		logged.Reset()
		id := "req-" + name
		answer := ask(route.path, "", id)
		body := answer.Body.String()
		if answer.Code != http.StatusInternalServerError || body != "Internal Server Error\nRequest ID: "+id+"\n" {
			t.Errorf("%s: production answered %d %q, want 500 with the status and the request id only", name, answer.Code, body)
		}
		if got := answer.Header().Get("X-Request-ID"); got != id {
			t.Errorf("%s: X-Request-ID = %q, want %q", name, got, id)
		}
		if record := logged.String(); !strings.Contains(record, "request_id="+id) || !strings.Contains(record, route.said) {
			t.Errorf("%s: the log record lacks the request id or %q:\n%s", name, route.said, record)
		}
	}

	// the record keeps the whole diagnostic, the stack included, that the answer no longer carries
	logged.Reset()
	ask("/plain", "", "req-stack")
	if record := logged.String(); !strings.Contains(record, "diagnostic=") || !strings.Contains(record, "at ") || !strings.Contains(record, "router.kitwork.js:") {
		t.Errorf("the record of a runtime error lacks its whole diagnostic:\n%s", record)
	}
	// the .error() handler that failed is in the record too
	logged.Reset()
	ask("/errorfails", "", "req-errorfails")
	if record := logged.String(); !strings.Contains(record, "boom-error-handler") || !strings.Contains(record, "stage=error") {
		t.Errorf("the record lacks the failure of the .error() handler:\n%s", record)
	}

	// a browser reads a page, a JSON client JSON; neither reads the diagnostic
	page := ask("/json", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8", "req-page")
	if body := page.Body.String(); page.Code != http.StatusInternalServerError || !strings.HasPrefix(page.Header().Get("Content-Type"), "text/html") ||
		!strings.Contains(body, "<h1>Internal Server Error</h1>") || !strings.Contains(body, "<code>req-page</code>") || strings.Contains(body, "không phải hàm") {
		t.Errorf("a browser read %d %q:\n%s", page.Code, page.Header().Get("Content-Type"), body)
	}
	data := ask("/json", "application/json", "req-data")
	var answer struct {
		Error     string `json:"error"`
		Status    int    `json:"status"`
		RequestID string `json:"requestId"`
	}
	if err := json.Unmarshal(data.Body.Bytes(), &answer); err != nil || data.Code != http.StatusInternalServerError ||
		answer.Error != "Internal Server Error" || answer.Status != 500 || answer.RequestID != "req-data" || strings.Contains(data.Body.String(), "không phải hàm") {
		t.Errorf("a JSON client read %d %q (%v)", data.Code, data.Body.String(), err)
	}
	// a request id the client sent is its text, never markup of the page
	hostile := ask("/json", "text/html", `<script>alert(1)</script>`)
	if body := hostile.Body.String(); strings.Contains(body, "<script>") || !strings.Contains(body, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Errorf("the request id reached the page as markup:\n%s", body)
	}
	if got := hostile.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}

	// CONTROLS
	if home := ask("/", "", "req-home"); home.Code != http.StatusOK || !strings.Contains(home.Body.String(), `"home":true`) {
		t.Errorf("CONTROL: a route that works = %d %q", home.Code, home.Body.String())
	}
	if handled := ask("/handled", "", "req-handled"); handled.Code != http.StatusServiceUnavailable || !strings.Contains(handled.Body.String(), `"handled":true`) {
		t.Errorf("CONTROL: an .error() handler's answer = %d %q", handled.Code, handled.Body.String())
	}
	if said := ask("/said", "", "req-said"); said.Code != http.StatusInternalServerError || said.Body.String() != "Thanh toán không thành công" {
		t.Errorf("CONTROL: ctx.error(\"…\") = %d %q, want its words", said.Code, said.Body.String())
	}
}

// While developing (AllowLocal) the same requests answer their whole diagnostic, the way router.view
// and a .ui page answer theirs. CONTROL: the route that works.
func TestUncaughtErrorShowsItsDiagnosticWhileDeveloping(t *testing.T) {
	ask, _ := failureSite(t, true, failureRouters)
	for name, route := range failingRouters {
		answer := ask(route.path, "", "req-"+name)
		if body := answer.Body.String(); answer.Code != http.StatusInternalServerError || !strings.Contains(body, route.said) || strings.Contains(body, "Request ID") {
			t.Errorf("%s: developing answered %d %q, want 500 with %q", name, answer.Code, body, route.said)
		}
	}
	plain := ask("/plain", "text/html", "req-plain-html").Body.String()
	if !strings.Contains(plain, "router.kitwork.js:") {
		t.Errorf("developing: the diagnostic lacks its file:line:column:\n%s", plain)
	}
	// CONTROL
	if home := ask("/", "", "req-home"); home.Code != http.StatusOK {
		t.Errorf("CONTROL: a route that works = %d", home.Code)
	}
}

// A Kitwork UI site answers the same way: a router.view that fails and a .ui page that does not
// compile answer 500 with the status and the request id in production, and their whole text while
// developing; the log record keeps it in both. Until now production answered "Internal Server Error"
// with no request id, and the page that did not compile left no record at all. CONTROL: a page that
// compiles, with a view that runs, answers 200.
func TestUIPageFailureShowsVisitorsOnlyTheRequestID(t *testing.T) {
	files := withFiles(map[string]string{
		"localhost/tinh/content.ui": `<content><text>{{ name </text></content>`,
		"localhost/mon/{slug}/router.work": `
import { router } from "kitwork";
router.view((ctx) => {
  const name = "Cà phê sữa";
  const order = { total: 3 };
  order.total("vnd");
});
`,
	})
	failures := map[string]string{
		"/tinh":       "tinh/content.ui:1:16: biểu thức {{ chưa được đóng bằng }}",
		"/mon/ca-phe": "order.total không phải hàm",
	}
	for _, developing := range []bool{false, true} {
		tenant := buildUISiteMode(t, files, developing)
		var logged bytes.Buffer
		savedLog := slog.Default()
		slog.SetDefault(slog.New(slog.NewTextHandler(&logged, nil)))
		for path, said := range failures {
			logged.Reset()
			request := httptest.NewRequest(http.MethodGet, "http://localhost"+path, nil)
			request.Header.Set("X-Request-ID", "req-ui")
			answer := httptest.NewRecorder()
			tenant.Serve(answer, request)
			body := answer.Body.String()
			if developing {
				if answer.Code != http.StatusInternalServerError || !strings.Contains(body, said) {
					t.Errorf("developing: %s = %d %q, want 500 with %q", path, answer.Code, body, said)
				}
			} else if answer.Code != http.StatusInternalServerError || body != "Internal Server Error\nRequest ID: req-ui\n" {
				t.Errorf("production: %s = %d %q, want 500 with the status and the request id only", path, answer.Code, body)
			}
			if record := logged.String(); !strings.Contains(record, "request_id=req-ui") || !strings.Contains(record, said) {
				t.Errorf("developing=%v: the log record of %s lacks the request id or %q:\n%s", developing, path, said, record)
			}
		}
		slog.SetDefault(savedLog)
		// CONTROL
		if rec := serveUI(t, tenant, "http://localhost/"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "<p>2 món, gấp đôi 4</p>") {
			t.Errorf("CONTROL developing=%v: the page that compiles = %d", developing, rec.Code)
		}
	}
}

// A _cron or _queue handler has no visitor: its error, nothing catching it, goes to its run's record
// only, and the record keeps the whole diagnostic (file:line:column of the call) in production too.
// What a visitor of a site is spared is not taken from the author's own record. CONTROL: the queue's
// dispatch was taken and the cron ran; each record is one of a failed run.
func TestBackgroundFailureKeepsItsWholeDiagnosticInProduction(t *testing.T) {
	saved := AllowLocal
	AllowLocal = false
	t.Cleanup(func() { AllowLocal = saved })

	worker := newQueueTenant(t, "broken", "import { queue } from \"kitwork\";\nqueue.handle((job) => {\n  const order = { total: 3 };\n  order.total(\"vnd\");\n});\n")
	defer worker.Close()
	if result := dispatch(t, worker, value.New("broken"), value.New(map[string]any{"n": 1})); !result.Get("queued").IsTrue() {
		t.Fatalf("CONTROL: the dispatch was refused: %v", result.Get("error").Text())
	}
	if !waitFor(10*time.Second, func() bool {
		return countQueueRows(t, worker, `SELECT COUNT(*) FROM queue_jobs WHERE status='failed'`) >= 1
	}) {
		t.Fatal("CONTROL: the queue job never failed")
	}
	worker.StopQueueWorker()
	var queued string
	worker.queueWorker().db.QueryRow(`SELECT error_message FROM queue_jobs WHERE status='failed' LIMIT 1`).Scan(&queued)
	if !strings.Contains(queued, "broken.kitwork.js:4:3: order.total không phải hàm") {
		t.Errorf("the queue job's record lacks its diagnostic: %q", queued)
	}

	root := t.TempDir()
	for path, content := range map[string]string{
		"acme/localhost/router.kitwork.js": "import { router } from \"kitwork\";\nrouter.get((ctx) => ctx.text(\"ok\"));\n",
		"acme/_cron/tick.kitwork.js":       "import { cron } from \"kitwork\";\ncron.every(\"1s\").handle(() => {\n  const order = { total: 3 };\n  order.total(\"vnd\");\n});\n",
	} {
		full := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	scheduled := NewAppTenant(root, "acme")
	if err := scheduled.Run(); err != nil {
		t.Fatal(err)
	}
	defer scheduled.Close()
	if scheduled.scheduler().db == nil {
		t.Fatal("CONTROL: the scheduler did not start")
	}
	var ran string
	waitFor(10*time.Second, func() bool {
		scheduled.scheduler().db.QueryRow(`SELECT error_message FROM cron_runs WHERE status='failed' LIMIT 1`).Scan(&ran)
		return ran != ""
	})
	scheduled.StopCronJobs()
	if !strings.Contains(ran, "tick.kitwork.js:4:3: order.total không phải hàm") {
		t.Errorf("the cron run's record lacks its diagnostic: %q", ran)
	}
}
