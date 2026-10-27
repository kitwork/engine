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
// kitwork/file (each read, each change of a handle, context.file), collection, sqlite, plus two
// URL-driven static paths. A session that found
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
	if err := os.WriteFile(filepath.Join(appDir, UIRouterFileName), []byte(routerScript), 0644); err != nil {
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
// capability is one more branch instead of one more fixture. The paths of kitwork/file start in the app's
// data (.database/files/) or, written app/…, in its code: the escapes are tried from both.
const readSinkRouter = `
import * as router from "kitwork/router";
import { collection } from "kitwork";
import * as file from "kitwork/file";

router.get((ctx) => {
    const api = ctx.query("api");
    const p = ctx.query("p");
    if (api === "file.read") {
        return ctx.text(file.read(p));
    }
    if (api === "file.text") {
        return ctx.text(file.text(p));
    }
    if (api === "file.bytes") {
        return ctx.type("text/plain").file(file.bytes(p));
    }
    if (api === "file.json") {
        return ctx.json(file.json(p));
    }
    if (api === "file.list") {
        return ctx.json(file.list(p));
    }
    if (api === "file.info") {
        return ctx.json(file.info(p));
    }
    if (api === "file.exists") {
        return ctx.json(file.exists(p));
    }
    if (api === "open.copy") {
        file.open(p).copy("stolen.txt");
        return ctx.text(file.text("stolen.txt"));
    }
    if (api === "ctx.file") {
        return ctx.file(file.open(p));
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
		"app/../victim-corp/victim.com/secret.txt",
		"app/../../victim-corp/victim.com/secret.txt",
		"app/localhost/../../victim-corp/victim.com/secret.txt",
	}
	apis := []string{"file.read", "file.text", "file.bytes", "file.json", "file.list", "file.info", "file.exists",
		"open.copy", "ctx.file", "collection.open"}

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

	for _, api := range []string{"file.read", "file.text", "file.bytes", "open.copy", "ctx.file"} {
		rec := serveIsolation(t, tenant, "http://localhost/?api="+api+"&p=app/localhost/allowed.txt")
		if !strings.Contains(rec.Body.String(), "ALLOWED-OWN-FILE") {
			t.Fatalf("%s must read the tenant's own file; got %d %q", api, rec.Code, rec.Body.String())
		}
	}
}

// writeSinkRouter is every change of a handle of kitwork/file, at a path the request gives: none may land
// in, or take from, another app.
const writeSinkRouter = `
import * as router from "kitwork/router";
import * as file from "kitwork/file";

router.get((ctx) => {
    const api = ctx.query("api");
    const p = ctx.query("p");
    file.open("own.txt").write("OWN");
    if (api === "write") { return ctx.json(file.open(p).write("PLANTED")); }
    if (api === "create") { return ctx.json(file.open(p).create()); }
    if (api === "copy") { return ctx.json(file.open("own.txt").copy(p)); }
    if (api === "move") { return ctx.json(file.open("own.txt").move(p)); }
    if (api === "delete") { return ctx.json(file.open(p).delete()); }
    if (api === "clear") { return ctx.json(file.open(p).clear()); }
    return ctx.text("no-api");
});
`

// The changes of kitwork/file never reach another app: a path that climbs is refused, so nothing is
// planted there and its file stays.
func TestAppIsolationWriteSinksDenyEscape(t *testing.T) {
	root, tenant := buildIsolationFixture(t, writeSinkRouter)
	victim := filepath.Join(root, "victim-corp", "victim.com")
	for _, api := range []string{"write", "create", "copy", "move", "delete", "clear"} {
		for _, p := range []string{
			"../../victim-corp/victim.com/planted.txt", "app/../victim-corp/victim.com/planted.txt",
			"../../victim-corp/victim.com/secret.txt", "../../victim-corp/victim.com/",
			"app/../victim-corp/victim.com/",
		} {
			serveIsolation(t, tenant, "http://localhost/?api="+api+"&p="+p)
			if _, err := os.Stat(filepath.Join(victim, "planted.txt")); err == nil {
				t.Fatalf("%s %q planted a file in another app", api, p)
			}
			if content, err := os.ReadFile(filepath.Join(victim, "secret.txt")); err != nil || string(content) != victimMarker {
				t.Fatalf("%s %q changed another app's file: %q %v", api, p, content, err)
			}
		}
	}
	// CONTROL: the same route writes inside the app
	if rec := serveIsolation(t, tenant, "http://localhost/?api=write&p=inside.txt"); rec.Code != http.StatusOK {
		t.Fatalf("write inside the app = %d %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, "acme", ".database", "files", "inside.txt")); err != nil {
		t.Fatalf("the control write is not in the app's data: %v", err)
	}
}

// ── Write sink: a SQLite file named from a request, and a database landing outside the app is persistent ──
//
// App code can no longer name a SQLite file (import { sqlite } and kitdb(…) were removed on 22/10/2026; a
// named database is the host's, connection("tên") with app.databases). The engine still builds a site's
// SQLite file by name (newDbProxy, sqliteRel), so the confinement is checked through the test-only module.

const sqliteSinkRouter = `
import * as router from "kitwork/router";
import { internals } from "kitwork";
const { sqlite, struct, int } = internals;

router.get((ctx) => {
    const handle = sqlite(ctx.query("p"), { probe: struct({ id: int().primaryKey() }) });
    handle.probe.count();
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
import * as router from "kitwork/router";

router.get((ctx) => {
    return ctx.text("home");
});
`

// NOTE — why there is no ".." test for static serving.
//
// It was written, and it could not be made to fail. URL-driven traversal is neutralised
// STRUCTURALLY, before any guard runs, by one idiom used consistently on this path:
//
//	clean := path.Clean("/" + urlPath)     // serveTreeStatic
//	s = path.Clean("/" + s)                // cleanAssetPrefix, for .assets() disk aliases
//
// Cleaning an ABSOLUTE path collapses leading ".." to root — "/../../victim" becomes "/victim" —
// so no traversal survives to be caught. Measured both ways: through Tenant.Serve (the folder
// router rejects it even earlier) and by calling serveTreeStatic directly with an un-normalised
// path, in each case with serveTreeStatic's ".." check AND its filepath.Rel boundary disabled.
// Nothing leaked in any combination.
//
// So those two guards are real defense in depth, not the thing doing the work, and a test
// asserting them would pass on a build with them deleted. Shipping one would be worse than
// shipping none: it would claim coverage that does not exist. If this path is ever refactored to
// resolve a RELATIVE path, the idiom above is what must be preserved.
//
// What IS tested below is the part that has no structural backstop: the allowlist that keeps
// sources and dot-files unreadable.

// Sources and dot-files must never be served even from inside the tenant: .env holds secrets and
// router.work is the app's own source code.
func TestAppIsolationStaticRefusesSourcesAndDotFiles(t *testing.T) {
	root, tenant := buildIsolationFixture(t, staticOnlyRouter)

	appDir := filepath.Join(root, "acme", "localhost")
	if err := os.WriteFile(filepath.Join(appDir, ".env"), []byte("SECRET_KEY="+victimMarker), 0644); err != nil {
		t.Fatal(err)
	}
	// A router.work site serves only its allowlist, which already keeps /.env out: the same file in
	// assets/, which it serves, is what reaches the dot-file rule.
	if err := os.MkdirAll(filepath.Join(appDir, "assets"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "assets", ".env"), []byte("SECRET_KEY="+victimMarker), 0644); err != nil {
		t.Fatal(err)
	}

	for _, target := range []string{
		"http://localhost/.env",
		"http://localhost/assets/.env",
		"http://localhost/router.work",
	} {
		rec := serveIsolation(t, tenant, target)
		body := rec.Body.String()
		if strings.Contains(body, victimMarker) || strings.Contains(body, "import * as router") {
			t.Fatalf("static serving exposed %q: %s", target, body)
		}
	}
}

// Kitwork UI sources (*.ui interface, *.work logic) carry no ".kitwork." in their names, so the
// *.kitwork.* rule alone would serve them. They must be refused like any other source, in any
// folder and in any letter case, while an ordinary file in the same folder still serves. A Kitwork UI
// site serves files only from its allowlist, so they stand in its assets/, which it serves.
func TestAppIsolationStaticRefusesUISources(t *testing.T) {
	root, tenant := buildIsolationFixture(t, staticOnlyRouter)

	appDir := filepath.Join(root, "acme", "localhost", "assets")
	sources := map[string]string{
		"view.ui":              "<view>" + victimMarker + "</view>",
		"router.work":          "// " + victimMarker,
		"shop/content.ui":      "<content>" + victimMarker + "</content>",
		"shop/cart.work":       "// " + victimMarker,
		"shop/Product-Card.UI": "<card>" + victimMarker + "</card>",
		"shop/notes.txt":       "PLAIN-FILE-SERVES",
	}
	for name, body := range sources {
		file := filepath.Join(appDir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}

	// Control: the static path is live, so a refusal below is the guard and not a broken route.
	if body := serveIsolation(t, tenant, "http://localhost/assets/shop/notes.txt").Body.String(); !strings.Contains(body, "PLAIN-FILE-SERVES") {
		t.Fatalf("control file was not served, the test cannot tell a refusal from a broken path: %q", body)
	}

	for _, target := range []string{
		"http://localhost/assets/view.ui",
		"http://localhost/assets/router.work",
		"http://localhost/assets/shop/content.ui",
		"http://localhost/assets/shop/cart.work",
		"http://localhost/assets/shop/Product-Card.UI",
	} {
		if body := serveIsolation(t, tenant, target).Body.String(); strings.Contains(body, victimMarker) {
			t.Fatalf("static serving exposed UI source %q: %s", target, body)
		}
	}
}
