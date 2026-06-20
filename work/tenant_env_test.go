package work

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// A tenant reads its OWN .env via kitwork().env, and CANNOT see the host process
// env (path isolation) — proven over a real HTTP request.
func TestTenantEnvScopedByPath(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "test", "localhost") // identity defaults to "test" w/o DB
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"),
		[]byte("GREETING=hello-from-tenant\nPORTX=7777\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	app := `import { router, env } from "kitwork";
router.get("/whoami").handle((res) => { return res.text(env.GREETING); });
router.get("/portx").handle((res) => { return res.text(env.PORTX || "none"); });
router.get("/secret").handle((res) => { return res.text(env.HOST_SECRET || "INVISIBLE"); });`
	if err := os.WriteFile(filepath.Join(dir, "app.kitwork.js"), []byte(app), 0o644); err != nil {
		t.Fatal(err)
	}

	// A host secret in the PROCESS env must NOT leak into the tenant.
	t.Setenv("HOST_SECRET", "topsecret")

	tenant := NewTenant(root, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}

	get := func(path string) string {
		rr := httptest.NewRecorder()
		tenant.Serve(rr, httptest.NewRequest("GET", path, nil))
		return rr.Body.String()
	}

	if got := get("/whoami"); got != "hello-from-tenant" {
		t.Errorf("/whoami = %q, want tenant's GREETING", got)
	}
	if got := get("/portx"); got != "7777" {
		t.Errorf("/portx = %q, want 7777 from tenant .env", got)
	}
	if got := get("/secret"); got != "INVISIBLE" {
		t.Errorf("/secret = %q — host env leaked into tenant! must be INVISIBLE", got)
	}
}
