package work

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Per-app isolation is what Kitwork sells: many sites in one process, none able to reach another's
// files. That promise is only as good as the weakest path sink, and the sinks are spread across
// file/collection/sqlite/ctx.file/saveFile plus two URL-driven static paths. A session that found
// two unguarded sinks by hand-grepping (fixed in bbe9ece) is the reason this is a TABLE: adding a
// capability that takes a path means adding a row here, not remembering to re-grep.
//
// Every case attacks from inside a working tenant and must fail to reach the victim app.

const victimMarker = "VICTIM-APP-SECRET"

// buildIsolationFixture creates a tenant plus a SEPARATE victim app under the same root:
//
//	<root>/acme/localhost/      ← tenant under test (the attacker's own app)
//	<root>/victim-corp/victim.com/secret.txt   ← must never be readable
//	<root>/victim-corp/victim.com/             ← must never be writable
func buildIsolationFixture(t *testing.T, routerScript string) (root string, tenant *Tenant) {
	t.Helper()
	root = t.TempDir()

	appDir := filepath.Join(root, "acme", "localhost")
	if err := os.MkdirAll(filepath.Join(appDir, "views"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "router.kitwork.js"), []byte(routerScript), 0644); err != nil {
		t.Fatal(err)
	}
	// A file the tenant IS allowed to read, so a case that returns nothing because the whole API
	// is broken can be told apart from one that was correctly denied.
	if err := os.WriteFile(filepath.Join(appDir, "allowed.txt"), []byte("ALLOWED-OWN-FILE"), 0644); err != nil {
		t.Fatal(err)
	}

	victimDir := filepath.Join(root, "victim-corp", "victim.com")
	if err := os.MkdirAll(victimDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(victimDir, "secret.txt"), []byte(victimMarker), 0644); err != nil {
		t.Fatal(err)
	}

	tenant = NewTenant(root, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	return root, tenant
}

func serveIsolation(t *testing.T, tenant *Tenant, target string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	rec := httptest.NewRecorder()
	tenant.Serve(rec, req)
	return rec
}

// ── JS-driven sinks: a handler passes request input to an API that takes a path ────────────────

// readSinkRouter routes each sink behind ?api=, so one fixture covers every JS path API and a new
// capability is one more branch instead of one more fixture.
const readSinkRouter = `
import { router, file, collection } from "kitwork";

router.get((ctx) => {
    const api = ctx.query("api");
    const p = ctx.query("p");
    if (api === "file.read") {
        return ctx.text(file.read(p));
    }
    if (api === "file.base64") {
        return ctx.text(file.base64(p));
    }
    if (api === "ctx.file") {
        return ctx.file(p);
    }
    if (api === "collection.open") {
        return ctx.json({ items: collection.open(p).all() });
    }
    return ctx.text("no-api");
});
`

func TestAppIsolationReadSinksDenyEscape(t *testing.T) {
	_, tenant := buildIsolationFixture(t, readSinkRouter)

	escapes := []string{
		"../../victim-corp/victim.com/secret.txt",
		"../../../victim-corp/victim.com/secret.txt",
		"./../../victim-corp/victim.com/secret.txt",
		"..\\..\\victim-corp\\victim.com\\secret.txt",
		"foo/../../../victim-corp/victim.com/secret.txt",
	}
	apis := []string{"file.read", "file.base64", "ctx.file", "collection.open"}

	for _, api := range apis {
		for _, p := range escapes {
			t.Run(api+" "+p, func(t *testing.T) {
				rec := serveIsolation(t, tenant, "http://localhost/?api="+api+"&p="+p)
				if strings.Contains(rec.Body.String(), victimMarker) {
					t.Fatalf("%s leaked another app's file via %q: %s", api, p, rec.Body.String())
				}
			})
		}
	}
}

// CONTROL for the table above: the same APIs must still work on the tenant's OWN file. Without
// this, every denial assertion would also hold on a build where the whole file API is broken.
func TestAppIsolationReadSinksAllowOwnFile(t *testing.T) {
	_, tenant := buildIsolationFixture(t, readSinkRouter)

	for _, api := range []string{"file.read", "ctx.file"} {
		rec := serveIsolation(t, tenant, "http://localhost/?api="+api+"&p=allowed.txt")
		if !strings.Contains(rec.Body.String(), "ALLOWED-OWN-FILE") {
			t.Fatalf("%s must read the tenant's own file; got %d %q", api, rec.Code, rec.Body.String())
		}
	}
}

// ── Write sink: sqlite names a file, and a database landing outside the app is persistent ──────

const sqliteSinkRouter = `
import { router, sqlite } from "kitwork";

router.get((ctx) => {
    const handle = sqlite.open(ctx.query("p"));
    handle.exec("CREATE TABLE IF NOT EXISTS probe (id INTEGER)");
    return ctx.text("ok");
});
`

func TestAppIsolationSqliteNameCannotEscape(t *testing.T) {
	root, tenant := buildIsolationFixture(t, sqliteSinkRouter)

	for _, p := range []string{
		"../../victim-corp/victim.com/planted.db",
		"../../../planted.db",
		"/etc/planted.db",
	} {
		serveIsolation(t, tenant, "http://localhost/?p="+p)
	}

	// sqliteRel flattens an escaping name to its base, so the file must land in the tenant's own
	// .data/ and nowhere else.
	for _, mustNotExist := range []string{
		filepath.Join(root, "victim-corp", "victim.com", "planted.db"),
		filepath.Join(root, "planted.db"),
	} {
		if _, err := os.Stat(mustNotExist); err == nil {
			t.Fatalf("sqlite created a database outside the app: %s", mustNotExist)
		}
	}
}

// ── URL-driven sinks: no tenant cooperation needed, so these are the most exposed ──────────────

const staticOnlyRouter = `
import { router } from "kitwork";

router.get((ctx) => {
    return ctx.text("home");
});
`

func TestAppIsolationStaticURLCannotEscape(t *testing.T) {
	_, tenant := buildIsolationFixture(t, staticOnlyRouter)

	// Raw targets: httptest.NewRequest keeps the path as written, which is the point — the check
	// has to survive un-normalised input, not just what a tidy client would send.
	for _, target := range []string{
		"http://localhost/../victim-corp/victim.com/secret.txt",
		"http://localhost/../../victim-corp/victim.com/secret.txt",
		"http://localhost/views/../../../victim-corp/victim.com/secret.txt",
		"http://localhost/%2e%2e/victim-corp/victim.com/secret.txt",
	} {
		rec := serveIsolation(t, tenant, target)
		if strings.Contains(rec.Body.String(), victimMarker) {
			t.Fatalf("static serving leaked another app's file via %q", target)
		}
	}
}

// Sources and dot-files must never be served even from inside the tenant: .env holds secrets and
// *.kitwork.* is the app's own source code.
func TestAppIsolationStaticRefusesSourcesAndDotFiles(t *testing.T) {
	root, tenant := buildIsolationFixture(t, staticOnlyRouter)

	appDir := filepath.Join(root, "acme", "localhost")
	if err := os.WriteFile(filepath.Join(appDir, ".env"), []byte("SECRET_KEY="+victimMarker), 0644); err != nil {
		t.Fatal(err)
	}

	for _, target := range []string{
		"http://localhost/.env",
		"http://localhost/router.kitwork.js",
	} {
		rec := serveIsolation(t, tenant, target)
		body := rec.Body.String()
		if strings.Contains(body, victimMarker) || strings.Contains(body, "import { router }") {
			t.Fatalf("static serving exposed %q: %s", target, body)
		}
	}
}
