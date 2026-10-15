package work

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// The error a .error() handler receives for a proxy or a generated output is the one safe(() => …)
// would give (14/10/2026): { message, code, at }. A target or provider function that failed gives its
// run's own message (no stack), code and place, as a handler's does; an upstream that gave no answer
// gives the request's code (DENIED here: the SSRF guard) and a message with no user information and no
// query string. Until now both were ERROR, with no place, and the message carried the whole diagnostic,
// stack included, or the upstream URL with its query string, which a site that sends err.message to a
// browser showed. The log record keeps the whole of it. CONTROL: a handler's own failure, the same shape.
func TestStageFailureGivesTheErrorHandlerItsObject(t *testing.T) {
	const echo = ".error((ctx, err) => ctx.json({ message: err.message, code: err.code, at: err.at }));\n"
	ask, logged := failureSite(t, false, map[string]string{
		"target":   "import { router } from \"kitwork\";\nrouter.proxy(() => {\n  const order = { total: 3 };\n  order.total(\"vnd\");\n})" + echo,
		"upstream": "import { router } from \"kitwork\";\nrouter.proxy(\"http://user:hunter2@127.0.0.1:1/private?api_key=SECRET123\")" + echo,
		"":         "import { router } from \"kitwork\";\nrouter.sitemap(() => {\n  fail(\"boom-sitemap\");\n})" + echo,
		"handler":  "import { router } from \"kitwork\";\nrouter.get(() => {\n  const order = { total: 3 };\n  order.total(\"vnd\");\n})" + echo,
	})
	type object struct {
		Message string  `json:"message"`
		Code    string  `json:"code"`
		At      *string `json:"at"`
	}
	read := func(path string) (object, string) {
		t.Helper()
		logged.Reset()
		answer := ask(path, "application/json", "req"+strings.ReplaceAll(path, "/", "-"))
		var got object
		if err := json.Unmarshal(answer.Body.Bytes(), &got); err != nil {
			t.Fatalf("%s: answered %d %q, want the handler's JSON", path, answer.Code, answer.Body.String())
		}
		return got, logged.String()
	}
	place := func(at *string) string {
		if at == nil {
			return "null"
		}
		return *at
	}

	for _, path := range []string{"/target", "/handler"} {
		got, record := read(path)
		if got.Code != "NOT_A_FUNCTION" || place(got.At) != "router.kitwork.js:4:3" ||
			!strings.HasPrefix(got.Message, "order.total không phải hàm") || strings.Contains(got.Message, "\n") ||
			strings.Contains(got.Message, "router.kitwork.js") {
			t.Errorf("%s: err = %+v at %s, want NOT_A_FUNCTION at router.kitwork.js:4:3, its message alone", path, got, place(got.At))
		}
		if !strings.Contains(record, "diagnostic=") || !strings.Contains(record, "order.total") {
			t.Errorf("%s: the log record lacks the whole diagnostic:\n%s", path, record)
		}
	}

	got, record := read("/sitemap.xml")
	if got.Code != "FAIL" || place(got.At) != "router.kitwork.js:3:3" || got.Message != "boom-sitemap" {
		t.Errorf("/sitemap.xml: err = %+v at %s, want FAIL boom-sitemap at router.kitwork.js:3:3", got, place(got.At))
	}
	if !strings.Contains(record, "boom-sitemap") || !strings.Contains(record, "stage=output") {
		t.Errorf("/sitemap.xml: the log record lacks the failure:\n%s", record)
	}

	got, record = read("/upstream")
	if got.Code != "DENIED" || got.At != nil || !strings.HasPrefix(got.Message, "proxy: ") ||
		!strings.Contains(got.Message, "127.0.0.1:1/private") ||
		strings.Contains(got.Message, "SECRET123") || strings.Contains(got.Message, "hunter2") {
		t.Errorf("/upstream: err = %+v at %s, want DENIED with no place, no query string, no user", got, place(got.At))
	}
	if !strings.Contains(record, "SECRET123") || !strings.Contains(record, "stage=proxy") {
		t.Errorf("/upstream: the log record lacks the upstream URL whole:\n%s", record)
	}
}

// CONTROL for the status: a proxy that failed answers 502 when no .error() handler answers.
func TestStageFailureStillAnswersBadGateway(t *testing.T) {
	ask, _ := failureSite(t, false, map[string]string{
		"upstream": "import { router } from \"kitwork\";\nrouter.proxy(\"http://127.0.0.1:1/private?api_key=SECRET123\");\n",
	})
	answer := ask("/upstream", "", "req-upstream")
	if answer.Code != http.StatusBadGateway || strings.Contains(answer.Body.String(), "SECRET123") {
		t.Fatalf("answered %d %q, want 502 with the request id only", answer.Code, answer.Body.String())
	}
}
