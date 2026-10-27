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
		"":        "import * as router from \"kitwork/router\";\nrouter.get((ctx) => ctx.json({ home: true }));\n",
		"broken":  "import * as router from \"kitwork/router\";\nrouter.get((ctx) => {\n  const o = { x: 3 };\n  o.x(\"k\");\n  return ctx.json({ reached: true });\n});\n",
		"control": "import * as router from \"kitwork/router\";\nrouter.get((ctx) => {\n  const o = { x: 3 };\n  fail(\"boom\");\n  return ctx.json({ reached: true });\n});\n",
		"safe":    "import * as router from \"kitwork/router\";\nrouter.get((ctx) => {\n  const o = { x: 3 };\n  const r = safe(() => o.x(\"k\"));\n  return ctx.json({ value: r.value, error: r.error.message, code: r.error.code, at: r.error.at });\n});\n",
	}
	for folder, source := range routes {
		dir := filepath.Join(root, "test", "localhost", folder)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, UIRouterFileName), []byte(source), 0o644); err != nil {
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
	const message = "router.work:4:3: o.x không phải hàm (là số): không gọi được"
	if code != http.StatusInternalServerError || !strings.HasPrefix(body, message) || strings.Contains(body, "reached") {
		t.Fatalf("a handler calling a non-function = %d %q, want 500 with %q", code, body, message)
	}
	for _, field := range []string{"Kitwork runtime execution failed", "code=RUNTIME_ERROR", "cause_code=NOT_A_FUNCTION", "source=router.work", "line=4", "column=3", "stage=handler"} {
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
	if safe.Value != nil || safe.Error != safeMessage || safe.Code != "NOT_A_FUNCTION" || safe.At != "router.work:4:24" {
		t.Fatalf("the safe() handler got %+v, want %q, NOT_A_FUNCTION at router.work:4:24", safe, safeMessage)
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

// environment has one method, require: environment.API_KEY() calls a value, so it is the not-a-function
// error like any other (14/10/2026), named as written and without the string's text, which is the secret.
// Until then the proxy answered every other name with null, without a word. environment.require is the
// control. (env until 22/10/2026, KITWORK_ENVIRONMENT.md.)
func TestCallingAnEnvValueIsNotAFunction(t *testing.T) {
	developing(t) // a 500 carries its diagnostic only while developing
	serve := notAFunctionSite(t, map[string]string{
		".env":               "API_KEY=sk-live-secret\nPORT=8080\n",
		"broken/router.work": "import * as router from \"kitwork/router\";\nimport { environment } from \"kitwork\";\nrouter.get((ctx) => {\n  environment.API_KEY();\n  return ctx.json({ reached: true });\n});\n",
		"safe/router.work":   "import * as router from \"kitwork/router\";\nimport { environment } from \"kitwork\";\nrouter.get((ctx) => {\n  const r = safe(() => environment.PORT());\n  return ctx.json({ error: r.error.message, at: r.error.at, code: r.error.code, port: environment.PORT, required: environment.require(\"PORT\") });\n});\n",
	})

	code, body, record := serve("/broken")
	const message = "router.work:4:3: environment.API_KEY không phải hàm (là chuỗi): không gọi được"
	if code != http.StatusInternalServerError || !strings.HasPrefix(body, message) || strings.Contains(body, "reached") {
		t.Fatalf("environment.API_KEY() = %d %q, want 500 with %q", code, body, message)
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
	if safe.Code != "NOT_A_FUNCTION" || safe.Error != "environment.PORT không phải hàm (là số): không gọi được" ||
		safe.At != "router.work:4:24" || safe.Port != 8080 || safe.Required != 8080 {
		t.Fatalf("safe(() => environment.PORT()) and the control environment.require(\"PORT\") = %+v", safe)
	}
}
