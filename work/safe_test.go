package work

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// safeSite writes one router.kitwork.js per folder and serves one request of a path.
func safeSite(t *testing.T, routers map[string]string) func(path string) *httptest.ResponseRecorder {
	t.Helper()
	saved := AllowLocal
	AllowLocal = true // developing: an uncaught failure answers its text, which a test reads
	t.Cleanup(func() { AllowLocal = saved })
	root := t.TempDir()
	for folder, source := range routers {
		dir := filepath.Join(root, "acme", "localhost", filepath.FromSlash(folder))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, RouterFileName), []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tenant := NewTenant(root, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(tenant.Close)
	return func(path string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		tenant.Serve(recorder, httptest.NewRequest(http.MethodGet, "http://localhost"+path, nil))
		return recorder
	}
}

// The "kitwork" module exports safe as well (14/10/2026): import { safe } from "kitwork", the habit of
// import { log }, declares safe as the same global, and a call through it catches. Without the export
// the import gave null and every call was the NOT_A_FUNCTION error. CONTROL: the global without import.
func TestKitworkExportsSafe(t *testing.T) {
	serve := safeSite(t, map[string]string{
		"imported": "import { router, safe } from \"kitwork\";\nrouter.get((ctx) => {\n  const r = safe(() => fail(\"x\"));\n  return ctx.json({ code: r.error.code, value: r.value });\n});\n",
		"renamed":  "import { router, safe as guard } from \"kitwork\";\nrouter.get((ctx) => ctx.json({ value: guard(() => 7).value }));\n",
		"global":   "import { router } from \"kitwork\";\nrouter.get((ctx) => ctx.json({ code: safe(() => fail(\"x\")).error.code }));\n",
		"member":   "import { router } from \"kitwork\";\nrouter.get((ctx) => ctx.json({ value: kitwork().safe(() => 8).value }));\n",
	})
	for path, want := range map[string]string{
		"/imported": `{"code":"FAIL","value":null}`,
		"/renamed":  `{"value":7}`,
		"/global":   `{"code":"FAIL"}`,
		"/member":   `{"value":8}`,
	} {
		if got := serve(path); got.Code != http.StatusOK || strings.TrimSpace(got.Body.String()) != want {
			t.Errorf("%s = %d %s, want %s", path, got.Code, got.Body.String(), want)
		}
	}
}

// The parameter err, error or e of a .error() handler is the error that ended the request, the shape
// safe gives (14/10/2026): { message, code, at }. Until then it was the string of the whole
// diagnostic, stack included. CONTROL: an error the site chose to show (ctx.error) is FAIL, with no
// place.
func TestErrorHandlerReceivesTheErrorObject(t *testing.T) {
	serve := safeSite(t, map[string]string{
		"failed": "import { router } from \"kitwork\";\nrouter.get((ctx) => {\n  const o = { x: 3 };\n  o.x(1);\n}).error((ctx, error) => ctx.status(503).json(error));\n",
		"said":   "import { router } from \"kitwork\";\nrouter.get((ctx) => {\n  ctx.error(\"Thanh toán không thành công\");\n}).error((ctx, err) => ctx.status(402).json(err));\n",
	})
	got := serve("/failed")
	var failure struct{ Message, Code, At string }
	if err := json.Unmarshal(got.Body.Bytes(), &failure); err != nil || got.Code != http.StatusServiceUnavailable {
		t.Fatalf("/failed = %d %s (%v)", got.Code, got.Body.String(), err)
	}
	if failure.Message != "o.x không phải hàm (là số): không gọi được" || failure.Code != "NOT_A_FUNCTION" || failure.At != "router.kitwork.js:4:3" {
		t.Fatalf("the .error() handler got %+v", failure)
	}
	got = serve("/said")
	if body := strings.TrimSpace(got.Body.String()); got.Code != http.StatusPaymentRequired ||
		body != `{"at":null,"code":"FAIL","message":"Thanh toán không thành công"}` {
		t.Fatalf("/said = %d %s", got.Code, body)
	}
}

// A predicate of the query builder that fails is the query's failure (14/10/2026): safe(() => …) reads
// it, as it reads a db.transaction callback's. Until then the builder dropped the condition without a
// word and ran the query without it. CONTROL: the same query with a predicate that works.
func TestQueryLambdaFailureIsTheQuerysFailure(t *testing.T) {
	routes := `
router.get((ctx) => {
    const failed = safe(() => sqlite.table("items").where(row => {
        fail("điều kiện hỏng");
        return row.id == 1;
    }).list());
    const kept = safe(() => sqlite.table("items").where(row => row.id == 1).toSQL());
    return ctx.json({ code: failed.error.code, message: failed.error.message, at: failed.error.at, kept: kept.error });
});
`
	body := serveNesting(t, newNestingTenant(t, routes, 10_000_000), context.Background())
	for _, want := range []string{`"code":"FAIL"`, `"message":"điều kiện hỏng"`, `"at":"router.kitwork.js:14:9"`, `"kept":null`} {
		if !strings.Contains(body, want) {
			t.Errorf("%s lacks %s", body, want)
		}
	}
}

// The same on a KitDB table, whose terminals read the builder's plan rather than run it: the chain keeps
// the predicate's failure (SchemaTable.failWith). CONTROL: a predicate that works lists the row.
func TestKitDBQueryLambdaFailureIsTheQuerysFailure(t *testing.T) {
	serve := safeSite(t, map[string]string{
		"": `import { router, database } from "kitwork";
const { kitdb, struct, text } = database;
const items = struct({ id: text().key(), label: text() });
const db = kitdb("lambda-failure.kitdb", { items });
router.get((ctx) => {
  if (db.items.count() == 0) db.items.create({ id: "a", label: "một" });
  const failed = safe(() => db.items.where((row) => {
    fail("điều kiện hỏng");
    return row.id == "a";
  }).list());
  const kept = safe(() => db.items.where((row) => row.id == "a").list());
  return ctx.json({ code: failed.error.code, message: failed.error.message, kept: kept.error, rows: kept.value.length });
});
`,
	})
	got := serve("/")
	body := strings.TrimSpace(got.Body.String())
	if got.Code != http.StatusOK || body != `{"code":"FAIL","kept":null,"message":"điều kiện hỏng","rows":1}` {
		t.Fatalf("GET / = %d %s", got.Code, body)
	}
}

// router.view keeps the status it set with ctx.status() (14/10/2026; the page always answered 200), so
// a view that read a failure with safe(() => …) can say so, and the page shows the error as data.
// A function of the view the page calls that hits what nothing catches ends the request as a failed
// view does. CONTROL: a view that sets no status answers 200.
func TestViewKeepsItsStatusAndAFatalHelperEndsIt(t *testing.T) {
	tenant := buildUISite(t, map[string]string{
		"localhost/router.work": `
import { router } from "kitwork";
router.view((ctx) => {
  const { value: product, error: productError } = safe(() => fail("chưa đọc được sản phẩm"));
  if (productError) ctx.status(503);
});
`,
		"localhost/page.ui":    `<page><content /></page>`,
		"localhost/content.ui": `<content><error>{{ productError.message }}</error><text>{{ productError.code }}</text></content>`,
		"localhost/ok/router.work": `
import { router } from "kitwork";
router.view((ctx) => {
  const greeting = "chào";
});
`,
		"localhost/ok/content.ui": `<content><text>{{ greeting }}</text></content>`,
		"localhost/spin/router.work": `
import { router } from "kitwork";
router.view((ctx) => {
  const spin = () => { let n = 0; for (let i = 0; i < 100000000; i++) { n = n + 1; } return n; };
});
`,
		"localhost/spin/content.ui": `<content><text>{{ spin() }}</text></content>`,
	})
	tenant.MaxEnergy = 2_000_000 // a test tenant has no ceiling of its own; a site has one
	rec := serveUI(t, tenant, "http://localhost/")
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "chưa đọc được sản phẩm") || !strings.Contains(rec.Body.String(), "FAIL") {
		t.Fatalf("GET / = %d, want 503 showing the error\n%s", rec.Code, rec.Body.String())
	}
	if rec := serveUI(t, tenant, "http://localhost/ok"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "chào") {
		t.Fatalf("CONTROL: GET /ok = %d\n%s", rec.Code, rec.Body.String())
	}
	rec = serveUI(t, tenant, "http://localhost/spin")
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "Energy Limit Exceeded") {
		t.Fatalf("GET /spin = %d, want 500 on the energy limit\n%s", rec.Code, rec.Body.String())
	}
}
