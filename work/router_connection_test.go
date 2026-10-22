package work

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kitwork/engine/value"
)

// connectionTenant boots one site, identity "test", domain "localhost", from router files (paths
// relative to the site folder) and hands back the boot error for a site that must not boot.
func connectionTenant(t *testing.T, files map[string]string) (*Tenant, string, error) {
	t.Helper()
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "test", "localhost")
	for rel, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tenant := NewTenant(tmp, "localhost")
	t.Cleanup(tenant.Close)
	return tenant, dir, tenant.Run()
}

func mustBoot(t *testing.T, files map[string]string) (*Tenant, string) {
	t.Helper()
	tenant, dir, err := connectionTenant(t, files)
	if err != nil {
		t.Fatal(err)
	}
	return tenant, dir
}

func hitJSON(t *testing.T, tenant *Tenant, path string) (int, map[string]any, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "http://localhost"+path, nil)
	rec := httptest.NewRecorder()
	tenant.Serve(rec, req)
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec.Code, body, rec.Body.String()
}

// router.connection(…) and router.connect(…) were removed on 22/10/2026 (KITWORK_DATABASE.md): a folder
// that calls one does not boot, and the error says what the spec writes instead — a named database is
// connection("tên") in .shared/schema/ with the host's app.databases, a database outside the app is
// connect({ … }) there. Every form of the old calls is refused the same way.
func TestRouterConnectionAndConnectAreErrors(t *testing.T) {
	for name, scenario := range map[string]struct{ router, want string }{
		"connection by a name":     {`router.connection("system");`, removedRouterConnection},
		"connection application":   {`router.connection("application");`, removedRouterConnection},
		"connection with a config": {`router.connection({ sqlite: "./.data/x.db" });`, removedRouterConnection},
		"connect a config":         {`router.connect({ sqlite: "./.data/x.db" });`, removedRouterConnect},
		"connect by a name":        {`router.connect("crm", { postgresql: "postgres://x" });`, removedRouterConnect},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := connectionTenant(t, map[string]string{
				"filesystem.kitwork": "",
				"router.kitwork.js":  `import { router } from "kitwork";` + "\n" + scenario.router + "\n" + `router.get((ctx) => ctx.json({ ok: true }));`,
			})
			if err == nil || !strings.Contains(err.Error(), scenario.want) {
				t.Fatalf("the site must not boot, saying %q: %v", scenario.want, err)
			}
		})
	}
	if !strings.Contains(removedRouterConnection, `connection("warehouse")`) || !strings.Contains(removedRouterConnection, "app.databases") ||
		!strings.Contains(removedRouterConnect, `connect({ postgresql: env.CRM_URL })`) || !strings.Contains(removedRouterConnect, "crm.query(sql, [giá trị])") {
		t.Fatalf("the errors name the spec's spelling: %q, %q", removedRouterConnection, removedRouterConnect)
	}
}

// context.database (and ctx.database("tên")) was removed on 22/10/2026: reading it fails INVALID_ARGUMENT
// saying where an app's database is declared and how a file imports it.
func TestContextDatabaseIsAnError(t *testing.T) {
	tenant, _ := mustBoot(t, map[string]string{
		"filesystem.kitwork": "",
		"router.kitwork.js": `import { router, safe } from "kitwork";
router.get((ctx) => {
  const read = safe(() => ctx.database.table("items").count());
  const named = safe(() => ctx.database("system"));
  return ctx.json({ read: read.error.code + " " + read.error.message, named: named.error.message });
});`,
	})
	code, body, raw := hitJSON(t, tenant, "/")
	read, _ := body["read"].(string)
	named, _ := body["named"].(string)
	if code != 200 || read != value.InvalidArgumentCode+" "+removedContextDatabase || named != removedContextDatabase {
		t.Fatalf("ctx.database: code=%d body=%s", code, raw)
	}
	if !strings.Contains(removedContextDatabase, `import { db } from "app/.shared/schema/<tên>.work"`) {
		t.Fatalf("the error names the spec's spelling: %q", removedContextDatabase)
	}
}

// In a Kitwork UI site the same read fails in router.view: the view does not render data it cannot have.
func TestUIViewContextDatabaseIsAnError(t *testing.T) {
	files := withFiles(map[string]string{
		"localhost/router.work": `import { router, safe } from "kitwork";
router.view((ctx) => {
  const { error } = safe(() => ctx.database.table("items").count());
  const message = error.message;
  ctx.title("Kho");
});`,
		"localhost/content.ui": `<content><text>{{ message }}</text></content>`,
	})
	tenant := buildUISite(t, files)
	page := serveUI(t, tenant, "http://localhost/")
	if page.Code != 200 || !strings.Contains(page.Body.String(), "context.database đã bỏ") {
		t.Fatalf("a .work view reads ctx.database: code=%d body=%s", page.Code, page.Body.String())
	}
}
