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
)

// A request handler that calls a value that is not a function fails the way every runtime error of the
// VM fails a handler (14/10/2026): the request is a 500 whose body is the error, and the site's log has
// one "Kitwork runtime execution failed" record with the code, the cause, the source and the place. No
// channel of its own: the control route fails with fail("boom"), an error of the kind handlers already
// had, and its response and record have the same shape. Inside safe(() => …) the handler gets the
// error and goes on.
func TestHandlerCallingANonFunctionFailsLikeEveryRuntimeError(t *testing.T) {
	developing(t) // a 500 carries its diagnostic only while developing
	root := t.TempDir()
	routes := map[string]string{
		"":        "import { router } from \"kitwork\";\nrouter.get((ctx) => ctx.json({ home: true }));\n",
		"broken":  "import { router } from \"kitwork\";\nrouter.get((ctx) => {\n  const o = { x: 3 };\n  o.x(\"k\");\n  return ctx.json({ reached: true });\n});\n",
		"control": "import { router } from \"kitwork\";\nrouter.get((ctx) => {\n  const o = { x: 3 };\n  fail(\"boom\");\n  return ctx.json({ reached: true });\n});\n",
		"safe":    "import { router } from \"kitwork\";\nrouter.get((ctx) => {\n  const o = { x: 3 };\n  const r = safe(() => o.x(\"k\"));\n  return ctx.json({ value: r.value, error: r.error.message, code: r.error.code, at: r.error.at });\n});\n",
	}
	for folder, source := range routes {
		dir := filepath.Join(root, "test", "localhost", folder)
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

	var logged bytes.Buffer
	saved := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, nil)))
	defer slog.SetDefault(saved)
	serve := func(path string) (int, string, string) {
		logged.Reset()
		recorder := httptest.NewRecorder()
		tenant.Serve(recorder, httptest.NewRequest(http.MethodGet, "http://localhost"+path, nil))
		return recorder.Code, recorder.Body.String(), logged.String()
	}

	code, body, record := serve("/control")
	if code != http.StatusInternalServerError || !strings.Contains(body, "boom") || strings.Contains(body, "reached") ||
		!strings.Contains(record, "Kitwork runtime execution failed") || !strings.Contains(record, "code=RUNTIME_ERROR") {
		t.Fatalf("CONTROL: a handler's runtime error = %d %q, record %q", code, body, record)
	}

	code, body, record = serve("/broken")
	const message = "router.kitwork.js:4:3: o.x không phải hàm (là số): không gọi được"
	if code != http.StatusInternalServerError || !strings.HasPrefix(body, message) || strings.Contains(body, "reached") {
		t.Fatalf("a handler calling a non-function = %d %q, want 500 with %q", code, body, message)
	}
	for _, field := range []string{"Kitwork runtime execution failed", "code=RUNTIME_ERROR", "cause_code=NOT_A_FUNCTION", "source=router.kitwork.js", "line=4", "column=3", "stage=handler"} {
		if !strings.Contains(record, field) {
			t.Fatalf("the site's log record lacks %q: %q", field, record)
		}
	}

	code, body, _ = serve("/safe")
	var safe struct {
		Value *bool  `json:"value"`
		Error string `json:"error"`
		Code  string `json:"code"`
		At    string `json:"at"`
	}
	if err := json.Unmarshal([]byte(body), &safe); err != nil || code != http.StatusOK {
		t.Fatalf("the safe() handler = %d %q (%v)", code, body, err)
	}
	// the message says what failed, at says where (the place no longer leads the message)
	const safeMessage = "o.x không phải hàm (là số): không gọi được"
	if safe.Value != nil || safe.Error != safeMessage || safe.Code != "NOT_A_FUNCTION" || safe.At != "router.kitwork.js:4:24" {
		t.Fatalf("the safe() handler got %+v, want %q, NOT_A_FUNCTION at router.kitwork.js:4:24", safe, safeMessage)
	}
}

// notAFunctionSite writes files (paths under the site's folder) and serves one request, with the
// site's log captured.
func notAFunctionSite(t *testing.T, files map[string]string) func(string) (int, string, string) {
	t.Helper()
	root := t.TempDir()
	for path, content := range files {
		full := filepath.Join(root, "acme", "localhost", filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tenant := NewTenant(root, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	var logged bytes.Buffer
	saved := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, nil)))
	t.Cleanup(func() { slog.SetDefault(saved) })
	return func(path string) (int, string, string) {
		logged.Reset()
		recorder := httptest.NewRecorder()
		tenant.Serve(recorder, httptest.NewRequest(http.MethodGet, "http://localhost"+path, nil))
		return recorder.Code, recorder.Body.String(), logged.String()
	}
}

// A handler that has already started a view (ctx.title, meta, bind, view) when the call fails keeps
// its page: the request answers 200 with what was bound before the failure, what was to be bound after
// it is missing, and only the site's log has the error. That is the channel every runtime error of a
// handler already had, fail("boom") in the same place being the control; a 500 with the message comes
// only from a handler that has not started a view.
func TestHandlerThatStartedAViewKeepsItsPageOnANonFunctionCall(t *testing.T) {
	developing(t) // a 500 carries its diagnostic only while developing
	const page = "<p>page[{{ early }}|{{ shown }}]</p>"
	handler := func(failure string) string {
		return "import { router } from \"kitwork\";\nrouter.get((ctx) => {\n  ctx.title(\"T\");\n  ctx.bind({ early: \"EARLY\" });\n  const o = { x: 3 };\n  " + failure + ";\n  return ctx.bind({ shown: \"BOUND\" });\n});\n"
	}
	serve := notAFunctionSite(t, map[string]string{
		"index.kitwork.html":           "<main>{{ @page }}</main>",
		"control/router.kitwork.js":    handler("fail(\"boom\")"),
		"control/page.kitwork.html":    page,
		"view/router.kitwork.js":       handler("o.x()"),
		"view/page.kitwork.html":       page,
		"notstarted/router.kitwork.js": "import { router } from \"kitwork\";\nrouter.get((ctx) => {\n  const o = { x: 3 };\n  o.x();\n  return ctx.bind({ shown: \"BOUND\" });\n});\n",
		"notstarted/page.kitwork.html": page,
	})

	code, body, record := serve("/control")
	if code != http.StatusOK || !strings.Contains(body, "page[EARLY|null]") || !strings.Contains(record, "msg=\"Kitwork runtime execution failed\"") || !strings.Contains(record, "boom") {
		t.Fatalf("CONTROL: a view handler's runtime error = %d %q, record %q", code, body, record)
	}

	code, body, record = serve("/view")
	if code != http.StatusOK || !strings.Contains(body, "page[EARLY|null]") || strings.Contains(body, "BOUND") || strings.Contains(body, "không phải hàm") {
		t.Fatalf("a view handler calling a non-function = %d %q, want 200 with the page bound before the call", code, body)
	}
	for _, field := range []string{"Kitwork runtime execution failed", "cause_code=NOT_A_FUNCTION", "router.kitwork.js:6:3: o.x không phải hàm (là số): không gọi được"} {
		if !strings.Contains(record, field) {
			t.Fatalf("the site's log record lacks %q: %q", field, record)
		}
	}

	code, body, _ = serve("/notstarted")
	if code != http.StatusInternalServerError || !strings.HasPrefix(body, "router.kitwork.js:4:3: o.x không phải hàm (là số): không gọi được") {
		t.Fatalf("a handler with no view started = %d %q, want 500 with the message", code, body)
	}
}

// env has one method, require: env.API_KEY() calls a value, so it is the not-a-function error like
// any other (14/10/2026), named as written and without the string's text, which is the secret. Until
// then the env proxy answered every other name with null, without a word. env.require is the control.
func TestCallingAnEnvValueIsNotAFunction(t *testing.T) {
	developing(t) // a 500 carries its diagnostic only while developing
	serve := notAFunctionSite(t, map[string]string{
		".env":                     "API_KEY=sk-live-secret\nPORT=8080\n",
		"broken/router.kitwork.js": "import { router, env } from \"kitwork\";\nrouter.get((ctx) => {\n  env.API_KEY();\n  return ctx.json({ reached: true });\n});\n",
		"safe/router.kitwork.js":   "import { router, env } from \"kitwork\";\nrouter.get((ctx) => {\n  const r = safe(() => env.PORT());\n  return ctx.json({ error: r.error.message, at: r.error.at, code: r.error.code, port: env.PORT, required: env.require(\"PORT\") });\n});\n",
	})

	code, body, record := serve("/broken")
	const message = "router.kitwork.js:3:3: env.API_KEY không phải hàm (là chuỗi): không gọi được"
	if code != http.StatusInternalServerError || !strings.HasPrefix(body, message) || strings.Contains(body, "reached") {
		t.Fatalf("env.API_KEY() = %d %q, want 500 with %q", code, body, message)
	}
	if strings.Contains(body, "sk-live-secret") || strings.Contains(record, "sk-live-secret") {
		t.Fatalf("the error shows the env value: body %q, record %q", body, record)
	}

	code, body, _ = serve("/safe")
	var safe struct {
		Error    string  `json:"error"`
		At       string  `json:"at"`
		Code     string  `json:"code"`
		Port     float64 `json:"port"`
		Required float64 `json:"required"`
	}
	if err := json.Unmarshal([]byte(body), &safe); err != nil || code != http.StatusOK {
		t.Fatalf("the safe() handler = %d %q (%v)", code, body, err)
	}
	// a number is not shown either (14/10/2026): env.PIN() would print the PIN
	if safe.Code != "NOT_A_FUNCTION" || safe.Error != "env.PORT không phải hàm (là số): không gọi được" ||
		safe.At != "router.kitwork.js:3:24" || safe.Port != 8080 || safe.Required != 8080 {
		t.Fatalf("safe(() => env.PORT()) and the control env.require(\"PORT\") = %+v", safe)
	}
}
