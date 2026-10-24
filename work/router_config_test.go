package work

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kitwork/engine/compiler"
)

// The config.work of a Kitwork UI site (KITWORK_ROUTER.md mục 4, 12; 24/10/2026): the app's at the app's
// folder is the parent of every site's root, a site's or a branch's runs before the router.work of its
// folder, into the same folder. The tests below build an app acme under a temporary root, each site a
// domain folder of it (apps/<identity>/<domain>/, the app's folder apart from its sites), unless one says
// it is the app of one site in its own folder.

const routerImport = "import * as router from \"kitwork/router\";\n"

// bootAppSite writes files (paths from the temporary root, "acme/…" for the app acme) and boots the site
// domain of the app, in development.
func bootAppSite(t *testing.T, root string, files map[string]string, domain string) (*Tenant, error) {
	t.Helper()
	saved := AllowLocal
	AllowLocal = true
	t.Cleanup(func() { AllowLocal = saved })
	writeAppTree(t, root, files)
	tenant := NewTenant(root, domain)
	t.Cleanup(tenant.Close)
	return tenant, tenant.Run()
}

// serveAt answers method target on tenant.
func serveAt(tenant *Tenant, method, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	tenant.Serve(rec, httptest.NewRequest(method, target, nil))
	return rec
}

// A branch's config.work runs before the router.work of its folder, into the same folder: its guard
// comes first, then router.work's, each in written order (KITWORK_ROUTER.md mục 4). A guard config.work
// hands the router runs at request time with config.work's own bytecode, a library's function it imports
// too, and a folder that has a config.work and no router.work guards everything below it.
//
// CONTROL (go test -overlay, 24/10/2026): with compileUIFolder not running config.work, /a?stop=yes answered
// 403 "router" and /khoa/trong 200; with code() always giving the router file's bytecode, /a and /a?stop=yes
// answered 500 "guard error: lambda belongs to a different program", and /khoa/trong 200 (a folder with no
// router.work had no bytecode to run its guard with).
func TestConfigWorkRunsBeforeRouterWork(t *testing.T) {
	root := t.TempDir()
	tenant, err := bootAppSite(t, root, map[string]string{
		"acme/.shared/rules.work":    "export const closed = (context) => context.status(403).text(\"đóng\");\n",
		"acme/localhost/router.work": routerImport + "router.get((context) => context.text(\"root\"));\n",
		"acme/localhost/a/config.work": routerImport +
			"router.guard((context) => {\n  if (context.query(\"stop\") === \"yes\") {\n    return context.status(403).text(\"config\");\n  }\n});\n",
		"acme/localhost/a/router.work": routerImport +
			"router.guard((context) => {\n  if (context.query(\"stop\") === \"yes\") {\n    return context.status(403).text(\"router\");\n  }\n});\n" +
			"router.get((context) => context.text(\"a\"));\n",
		"acme/localhost/khoa/config.work":       routerImport + "import { closed } from \"app/.shared/rules.work\";\nrouter.guard(closed);\n",
		"acme/localhost/khoa/trong/router.work": routerImport + "router.get((context) => context.text(\"trong\"));\n",
	}, "localhost")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, c := range []struct {
		target, body string
		code         int
	}{
		{"http://localhost/a", "a", http.StatusOK},
		{"http://localhost/a?stop=yes", "config", http.StatusForbidden}, // config.work's guard first
		{"http://localhost/khoa/trong", "đóng", http.StatusForbidden},   // a folder with only a config.work
		{"http://localhost/", "root", http.StatusOK},
	} {
		rec := serveAt(tenant, http.MethodGet, c.target)
		if rec.Code != c.code || rec.Body.String() != c.body {
			t.Errorf("GET %s = %d %q, want %d %q", c.target, rec.Code, rec.Body.String(), c.code, c.body)
		}
	}
}

// An error of a config.work stops the site's generation where config.work writes it, its file from the
// site's root, as an error of router.work does; a config.work of a template's folder is never read.
//
// CONTROL: a template site with the same config.work boots. CONTROL (go test -overlay, 24/10/2026): with
// compileUIFolder not running config.work, the Kitwork UI site booted too.
func TestConfigWorkErrorNamesItsFile(t *testing.T) {
	root := t.TempDir()
	_, err := bootAppSite(t, root, map[string]string{
		"acme/localhost/router.work":   routerImport + "router.get((context) => context.text(\"root\"));\n",
		"acme/localhost/a/config.work": routerImport + "router.transition(\"zoom\");\n",
	}, "localhost")
	want := "initialize router " + filepath.FromSlash("a/config.work") + ": config.work:2:"
	if err == nil || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), `router.transition("zoom")`) {
		t.Fatalf("run = %v, want %q naming router.transition", err, want)
	}
	// a guard's parameter read the old way names its folder's config.work too, not only config.work:line:col.
	// CONTROL (go test -overlay, 24/10/2026): with the error wrapped as "router: %w" as before, it read
	// "router: config.work:4:34: …", naming none of the site's config.work.
	_, err = bootAppSite(t, t.TempDir(), map[string]string{
		"acme/localhost/router.work":   routerImport + "router.get((context) => context.text(\"root\"));\n",
		"acme/localhost/a/config.work": routerImport + "router.colors({ brand: \"#111111\" });\n\nrouter.guard((context, response) => true);\n",
	}, "localhost")
	want = "router " + filepath.FromSlash("a/config.work") + ": config.work:4:"
	if err == nil || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "tham số `response`") {
		t.Fatalf("a guard's parameter: run = %v, want %q naming response", err, want)
	}
	template := t.TempDir()
	if _, err := bootAppSite(t, template, map[string]string{
		"acme/localhost/router.kitwork.js": "import { router } from \"kitwork\";\nrouter.get((ctx) => ctx.text(\"root\"));\n",
		"acme/localhost/a/config.work":     routerImport + "router.transition(\"zoom\");\n",
	}, "localhost"); err != nil {
		t.Fatalf("CONTROL: a template site never reads config.work, run = %v", err)
	}
}

// The config.work at the app's folder is the parent of every site's root (KITWORK_ROUTER.md mục 4,
// 24/10/2026): two sites of one app start from it, each adding its own over it, token by token, or winning
// over it, as a branch does over its site. Its error stops each site with its place, app/config.work.
//
// CONTROL (go test -overlay, 24/10/2026): with compileUIFolder not seeding the site's root, neither site had
// --brand:#123456 or data-kitwork-appearance="dark", one.example's lang was the engine's "vi", and the broken
// app config.work booted; with the seed counted as a call of the root, two.example stopped, "router.language
// gọi hai lần".
func TestAppConfigWorkIsTheParentOfEverySite(t *testing.T) {
	root := t.TempDir()
	page := routerImport + "router.get((context) => context.title(\"Trang\").view({ name: \"x\" }));\n"
	files := map[string]string{
		"acme/config.work": routerImport + "router.colors({ brand: \"#123456\", accent: \"#654321\" });\n" +
			"router.language(\"fr\");\nrouter.appearance(\"dark\");\n",
		"acme/one.example/router.work": page,
		"acme/one.example/content.ui":  "<content><text>{{ name }}</text></content>",
		"acme/two.example/config.work": routerImport + "router.colors({ accent: \"#0a0a0a\" });\nrouter.language(\"en\");\n",
		"acme/two.example/router.work": page,
		"acme/two.example/content.ui":  "<content><text>{{ name }}</text></content>",
	}
	one, err := bootAppSite(t, root, files, "one.example")
	if err != nil {
		t.Fatalf("one.example: %v", err)
	}
	two, err := bootAppSite(t, root, nil, "two.example")
	if err != nil {
		t.Fatalf("two.example: %v", err)
	}
	for _, c := range []struct {
		tenant     *Tenant
		name, html string
		css        []string
	}{
		{one, "one.example", `<html lang="fr" data-kitwork-appearance="dark">`, []string{"--brand:#123456", "--accent:#654321"}},
		// the site's accent over the app's, the app's brand kept; the site's language wins
		{two, "two.example", `<html lang="en" data-kitwork-appearance="dark">`, []string{"--brand:#123456", "--accent:#0a0a0a"}},
	} {
		body := pageWithStylesheet(t, c.tenant, "http://"+c.name+"/")
		if !strings.Contains(body, c.html) {
			t.Errorf("%s is missing %s\n%.600s", c.name, c.html, body)
		}
		for _, want := range c.css {
			if !strings.Contains(body, want) {
				t.Errorf("%s CSS is missing %s", c.name, want)
			}
		}
	}

	writeAppTree(t, root, map[string]string{"acme/config.work": routerImport + "router.colors({ brand: \"#123456\" });\nrouter.transition(\"zoom\");\n"})
	_, err = bootAppSite(t, root, nil, "one.example")
	if err == nil || !strings.Contains(err.Error(), "initialize router app/config.work: config.work:3:") || !strings.Contains(err.Error(), `router.transition("zoom")`) {
		t.Fatalf("a broken app config.work: run = %v, want its place app/config.work", err)
	}
}

// One app whose site is its own folder (KITWORK_ROUTER.md mục 4): the config.work there is the app's and the
// site's at once, runs once, and takes the words of both: router.timezone with router.guard and
// router.reload. A branch of that site is a branch: router.reload and a built-in door there stop the boot,
// where the compiler cannot tell a branch from another site's root.
//
// CONTROL: the same branch words at the site's root boot (the first site below). CONTROL (go test -overlay,
// 24/10/2026): with call not checking the level, both branch rows booted.
func TestOneSiteInTheAppsFolder(t *testing.T) {
	base := map[string]string{
		"config.work": routerImport + "router.timezone(\"Asia/Kolkata\");\nrouter.reload(false);\n" +
			"router.guard((context) => {\n  if (context.query(\"stop\") === \"yes\") {\n    return false;\n  }\n});\n",
		"router.work": routerImport + "router.sitemap(true);\nrouter.get((context) => context.text(\"root\"));\n",
	}
	tenant := NewTenant(writeSite(t, base), "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := serveAt(tenant, http.MethodGet, "http://localhost/?stop=yes").Code; got != http.StatusForbidden {
		t.Errorf("the guard of the app's and site's config.work: GET /?stop=yes = %d, want 403", got)
	}
	if zone := tenant.routeTree().root.folder.timezone; zone == nil || zone.String() != "Asia/Kolkata" {
		t.Errorf("the app's zone = %v, want Asia/Kolkata", zone)
	}
	for _, c := range []struct{ file, source, want string }{
		{"a/config.work", routerImport + "router.reload(true);\n",
			"config.work:2:7: " + compiler.RouterWrongLevel("reload", "config.work của một nhánh")},
		{"a/router.work", routerImport + "router.sitemap(true);\n",
			"router.work:2:7: " + compiler.RouterWrongLevel("sitemap", "router.work của một nhánh")},
	} {
		files := map[string]string{c.file: c.source}
		for name, source := range base {
			files[name] = source
		}
		err := NewTenant(writeSite(t, files), "localhost").Run()
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: run = %v\nwant %s", c.file, err, c.want)
		}
	}
}

// Each word at its level (KITWORK_ROUTER.md mục 4): the config.work at the app's folder takes only the app's
// words, so router.guard, router.meta, router.limit, router.jsonld, router.cors, router.assets and
// router.reload there stop each site's boot at file:line:column, naming where the word belongs. The compiler
// cannot tell them: the app's folder may be a site's root too.
//
// CONTROL (go test -overlay, 24/10/2026): with call not checking the level, every row went red: six booted, and
// router.cors stopped as "chưa có" rather than naming where it belongs.
func TestAppConfigWorkTakesOnlyTheAppsWords(t *testing.T) {
	for _, c := range []struct{ word, line string }{
		{"guard", "router.guard(() => true);"},
		{"meta", `router.meta({ title: "Quán" });`},
		{"limit", `router.limit({ rate: 5, per: "1m" });`},
		{"jsonld", `router.jsonld({ "@type": "Organization" });`},
		{"cors", `router.cors({ origin: "https://a.example" });`},
		{"assets", `router.assets("_public");`},
		{"reload", "router.reload(false);"},
	} {
		_, err := bootAppSite(t, t.TempDir(), map[string]string{
			"acme/config.work":           routerImport + c.line + "\n",
			"acme/localhost/router.work": routerImport + "router.get((context) => context.text(\"x\"));\n",
		}, "localhost")
		want := "initialize router app/config.work: config.work:2:7: " + compiler.RouterWrongLevel(c.word, "config.work của gốc app")
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: run = %v\nwant %s", c.word, err, want)
		}
	}
}

// A call whose nearest folder wins is once per folder, config.work and router.work together
// (KITWORK_ROUTER.md mục 4): the second stops the boot naming both, config.work:line and router.work:line.
// The app's call a site's root starts from is the parent's, not the root's: the site writes it again freely.
//
// CONTROL (go test -overlay, 24/10/2026): with once not keeping the folder's calls across its files, the first
// site booted, its router.work's language over config.work's.
func TestNearestWinsCallOncePerFolderAcrossItsFiles(t *testing.T) {
	_, err := bootAppSite(t, t.TempDir(), map[string]string{
		"acme/localhost/router.work":   routerImport + "router.get((context) => context.text(\"x\"));\n",
		"acme/localhost/a/config.work": routerImport + "router.colors({ brand: \"#111111\" });\nrouter.language(\"vi\");\n",
		"acme/localhost/a/router.work": routerImport + "router.language(\"en\");\nrouter.get((context) => context.text(\"a\"));\n",
	}, "localhost")
	want := "router.work:2:7: " + compiler.RouterCallTwice("language", "config.work:3", "router.work:2")
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("run = %v\nwant %s", err, want)
	}
	// accumulating calls in both files add up
	if _, err := bootAppSite(t, t.TempDir(), map[string]string{
		"acme/config.work":             routerImport + "router.language(\"vi\");\nrouter.transition(\"fade\");\n",
		"acme/localhost/config.work":   routerImport + "router.language(\"en\");\nrouter.colors({ brand: \"#111111\" });\n",
		"acme/localhost/router.work":   routerImport + "router.transition(\"slide\");\nrouter.colors({ ink: \"#222222\" });\nrouter.get((context) => context.text(\"x\"));\n",
		"acme/localhost/a/router.work": routerImport + "router.get((context) => context.text(\"a\"));\n",
	}, "localhost"); err != nil {
		t.Fatalf("CONTROL: the app's calls are no calls of the site's root, run = %v", err)
	}
}

// router.cors(…) is a word of the table whose rules are open (KITWORK_ROUTER.md mục 4, 14): in a Kitwork UI
// folder it stops the boot where it is written. A template site's is an unknown method, said and passed
// over, as before.
//
// CONTROL (go test -overlay, 24/10/2026): with router.cors run as the other words are, by the Go method of its
// name, the site stopped as "router.cors chưa có trong engine này", saying nothing of the open rules.
func TestRouterCorsIsNotYet(t *testing.T) {
	_, err := bootAppSite(t, t.TempDir(), map[string]string{
		"acme/localhost/router.work": routerImport + "router.cors({ origin: \"https://a.example\" });\nrouter.get((context) => context.text(\"x\"));\n",
	}, "localhost")
	if err == nil || !strings.Contains(err.Error(), "router.work:2:7: "+routerCorsMissing) {
		t.Fatalf("run = %v, want %s", err, routerCorsMissing)
	}
	var tenant *Tenant
	output := captureBootOutput(t, func() {
		tenant, err = bootAppSite(t, t.TempDir(), map[string]string{
			"acme/localhost/router.kitwork.js": "import { router } from \"kitwork\";\nrouter.cors({ origin: \"*\" });\nrouter.get((ctx) => ctx.text(\"x\"));\n",
		}, "localhost")
	})
	if err != nil || !strings.Contains(output, "router.cors() is not a method") || serveAt(tenant, http.MethodGet, "http://localhost/").Body.String() != "x" {
		t.Fatalf("CONTROL: a template site keeps its chain, run = %v\n%s", err, output)
	}
}

// router.assets(…) only adds to a Kitwork UI site's allowlist (KITWORK_ROUTER.md mục 4, KITWORK_UI.md mục 5):
// a folder it mounts is served at its public path, one call after another, and the site's assets/ is served
// at /assets/… with or without a declaration. What neither names stays unserved.
//
// CONTROL (go test -overlay, 24/10/2026): with serveTreeStatic as before (the allowlist alone, then the mounts
// alone), /public/ghi-chu.txt and /tai-lieu/huong-dan.txt answered 404, and so did /assets/logo.svg once the
// site declared a mount.
func TestUIAssetsAddToTheAllowlist(t *testing.T) {
	files := map[string]string{
		"acme/localhost/router.work":              routerImport + "router.get((context) => context.text(\"root\"));\n",
		"acme/localhost/assets/logo.svg":          `<svg xmlns="http://www.w3.org/2000/svg"></svg>`,
		"acme/localhost/_public/ghi-chu.txt":      "ghi chú",
		"acme/localhost/_docs/huong-dan.txt":      "hướng dẫn",
		"acme/localhost/bi-mat.txt":               "bí mật",
		"acme/localhost/assets/config.work.notes": "x",
	}
	plain, err := bootAppSite(t, t.TempDir(), files, "localhost")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if rec := serveAt(plain, http.MethodGet, "http://localhost/assets/logo.svg"); rec.Code != http.StatusOK {
		t.Errorf("assets/ with no declaration: %d", rec.Code)
	}
	files["acme/localhost/router.work"] = routerImport + "router.assets(\"_public\");\nrouter.assets(\"/tai-lieu/*\", \"_docs\");\n" +
		"router.get((context) => context.text(\"root\"));\n"
	mounted, err := bootAppSite(t, t.TempDir(), files, "localhost")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	for target, want := range map[string]int{
		"/assets/logo.svg":        http.StatusOK,
		"/public/ghi-chu.txt":     http.StatusOK,
		"/tai-lieu/huong-dan.txt": http.StatusOK,
		"/bi-mat.txt":             http.StatusNotFound,
		"/_public/ghi-chu.txt":    http.StatusNotFound,
		"/_docs/huong-dan.txt":    http.StatusNotFound,
	} {
		if rec := serveAt(mounted, http.MethodGet, "http://localhost"+target); rec.Code != want {
			t.Errorf("GET %s = %d, want %d", target, rec.Code, want)
		}
	}
	if body := serveAt(mounted, http.MethodGet, "http://localhost/tai-lieu/huong-dan.txt").Body.String(); body != "hướng dẫn" {
		t.Errorf("the mounted file: %q", body)
	}
}
