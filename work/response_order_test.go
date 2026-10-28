package work

import (
	"bytes"
	"log/slog"
	"net/http"
	"strings"
	"testing"
)

// In router.work every edit stands before the one answer that ends the chain (KITWORK_ROUTER §6, §11;
// 20/10/2026). An edit after the answer, two answers, the page's text before an answer other than view,
// a .type() against json or view, a .file() of something not bytes, fail the request with an error that
// shows the order that works.
func TestUIResponseOrderErrors(t *testing.T) {
	routes := map[string]string{
		"view-title":  `context.view({ note: 1 }).title("x")`,
		"json-status": `context.json({ a: 1 }).status(201)`,
		"two":         `context.json({ a: 1 }).text("x")`,
		"text-title":  `context.title("x").json({ a: 1 })`,
		"type-view":   `context.type("text/plain").view({ note: 1 })`,
		"type-json":   `context.type("text/csv").json({ a: 1 })`,
		"file-number": `context.file(42)`,
		"after-file":  `context.type("image/png").file(qrcode.data("x").png()).header("X-A", "1")`,
	}
	want := map[string]string{
		"view-title":  "`.title()` nối sau `.view()`: mọi chỉnh sửa đứng trước hàm trả lời: `context.title(…).view(…)`",
		"json-status": "`.status()` nối sau `.json()`: mọi chỉnh sửa đứng trước hàm trả lời: `context.status(…).json(…)`",
		"two":         "`.text()` sau `.json()`: một chuỗi chỉ có một hàm trả lời, ở cuối",
		"text-title":  "`.title()` là chữ của trang, chỉ đi với hàm trả lời vẽ trang (`.view()`, `.notFound()`, `.error()`)",
		"type-view":   "`.type()` không hợp với `.view()`",
		"type-json":   "`.type()` không hợp với `.json()`",
		"file-number": "`.file()` nhận đường dẫn của kitwork/file, handle của nó, hay bytes: `context.file(\"exports/bao-cao.csv\")`",
		"after-file":  "`.header()` nối sau `.file()`",
	}
	files := map[string]string{"router.work": `import * as router from "kitwork/router";`}
	for name, chain := range routes {
		files[name+"/router.work"] = "import * as router from \"kitwork/router\";\nimport { qrcode } from \"kitwork\";\n" +
			"router.get((context) => " + chain + ");\n"
	}
	tn := NewTenant(writeSite(t, files), "localhost")
	if err := tn.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	defer slog.SetDefault(previous)
	for name := range routes {
		logs.Reset()
		r := serveGet(tn, "/"+name, nil)
		if r.Code != http.StatusInternalServerError {
			t.Errorf("/%s: %d %q, want the request to fail", name, r.Code, r.Body.String())
		}
		if logged := strings.ReplaceAll(logs.String(), `\"`, `"`); !strings.Contains(logged, want[name]) {
			t.Errorf("/%s: the log lacks %q:\n%s", name, want[name], logs.String())
		}
	}
}

// The edits in the right order answer as asked: status, headers (one, or an object), a cookie, a media
// type, then the answer; a redirect is 302 unless a status came first. Reading a request header is still
// context.header(name).
func TestUIResponseEditsBeforeTheAnswer(t *testing.T) {
	routes := map[string]string{
		"made":     `context.status(201).headers({ "X-A": "1", "X-B": "2" }).header("X-C", context.header("X-In")).cookie("seen", "1").json({ ok: true })`,
		"csv":      `context.header("Content-Disposition", "attachment").type("text/csv").text("a,b")`,
		"go":       `context.redirect("/there")`,
		"moved":    `context.status(301).redirect("/there")`,
		"response": `context.response.status(202).type("application/xml").text("<x/>")`,
	}
	files := map[string]string{"router.work": `import * as router from "kitwork/router";`}
	for name, chain := range routes {
		files[name+"/router.work"] = "import * as router from \"kitwork/router\";\nrouter.get((context) => " + chain + ");\n"
	}
	tn := NewTenant(writeSite(t, files), "localhost")
	if err := tn.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}

	made := serveGet(tn, "/made", map[string]string{"X-In": "vào"})
	if made.Code != http.StatusCreated || made.Header().Get("X-A") != "1" || made.Header().Get("X-B") != "2" || made.Header().Get("X-C") != "vào" ||
		len(made.Result().Cookies()) != 1 || made.Body.String() != `{"ok":true}` {
		t.Errorf("/made: %d headers=%v cookies=%v %q", made.Code, made.Header(), made.Result().Cookies(), made.Body.String())
	}
	if r := serveGet(tn, "/csv", nil); r.Header().Get("Content-Type") != "text/csv" || r.Header().Get("Content-Disposition") != "attachment" || r.Body.String() != "a,b" {
		t.Errorf("/csv: %d %v %q", r.Code, r.Header(), r.Body.String())
	}
	if r := serveGet(tn, "/go", nil); r.Code != http.StatusFound || r.Header().Get("Location") != "/there" {
		t.Errorf("/go: %d location=%q, want 302", r.Code, r.Header().Get("Location"))
	}
	if r := serveGet(tn, "/moved", nil); r.Code != http.StatusMovedPermanently {
		t.Errorf("/moved: %d, want 301", r.Code)
	}
	if r := serveGet(tn, "/response", nil); r.Code != http.StatusAccepted || r.Header().Get("Content-Type") != "application/xml" {
		t.Errorf("/response: %d %q", r.Code, r.Header().Get("Content-Type"))
	}
}
