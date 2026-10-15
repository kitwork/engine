package work

import (
	"net/http"
	"strings"
	"testing"
)

// A folder's router.error((ctx, err) => …) answers a failure of its routes and of its subfolders'
// routes that have no .error() of their own, the nearest folder first (14/10/2026), as the docs say:
// it was kept and never run, so the request answered 500. A route's own .error() still comes first.
// CONTROL: a site with no handler anywhere answers 500 with the request id.
func TestFolderErrorHandlerAnswersItsRoutes(t *testing.T) {
	ask, _ := failureSite(t, false, map[string]string{
		"folder":       "import { router } from \"kitwork\";\nrouter.error((ctx, err) => ctx.status(503).json({ seen: err }));\nrouter.get(() => {\n  fail(\"hỏng\");\n});\n",
		"parent":       "import { router } from \"kitwork\";\nrouter.error((ctx, err) => ctx.status(502).json({ parent: err.code }));\n",
		"parent/child": "import { router } from \"kitwork\";\nrouter.get(() => {\n  fail(\"con\");\n});\n",
		"parent/own":   "import { router } from \"kitwork\";\nrouter.error((ctx, err) => ctx.status(504).json({ own: err.message }));\nrouter.get(() => {\n  fail(\"riêng\");\n});\n",
		"parent/route": "import { router } from \"kitwork\";\nrouter.get(() => {\n  fail(\"tuyến\");\n}).error((ctx, err) => ctx.status(409).json({ route: err.message }));\n",
		"bare":         "import { router } from \"kitwork\";\nrouter.get(() => {\n  fail(\"trơn\");\n});\n",
	})
	for path, want := range map[string]struct {
		status int
		body   string
	}{
		"/folder":       {http.StatusServiceUnavailable, `{"seen":{"at":"router.kitwork.js:4:3","code":"FAIL","message":"hỏng"}}`},
		"/parent/child": {http.StatusBadGateway, `{"parent":"FAIL"}`},
		"/parent/own":   {http.StatusGatewayTimeout, `{"own":"riêng"}`},
		"/parent/route": {http.StatusConflict, `{"route":"tuyến"}`},
	} {
		answer := ask(path, "application/json", "req"+strings.ReplaceAll(path, "/", "-"))
		if body := strings.TrimSpace(answer.Body.String()); answer.Code != want.status || body != want.body {
			t.Errorf("%s = %d %s, want %d %s", path, answer.Code, body, want.status, want.body)
		}
	}
	if answer := ask("/bare", "", "req-bare"); answer.Code != http.StatusInternalServerError || answer.Body.String() != "Internal Server Error\nRequest ID: req-bare\n" {
		t.Errorf("CONTROL: /bare = %d %q", answer.Code, answer.Body.String())
	}
}
