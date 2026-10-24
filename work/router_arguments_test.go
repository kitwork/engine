package work

import (
	"net/http"
	"strings"
	"testing"
)

// What a word of a Kitwork UI router is handed is checked as the router runs (KITWORK_ROUTER.md mục 3, 4,
// 11): a guard that is no function, a door without its handler, a branch's value of another kind stop the
// boot where they are written, naming the shape the word takes. A guard of null above all: a name written
// wrong (rules.close for rules.closed) left the branch with no guard and nothing said. A template's router
// passes such a value over, as before.
//
// CONTROL (go test -overlay, 24/10/2026): with routerArgumentError and guardArgumentError answering "" for
// everything, every row booted (run = <nil>).
func TestUIRouterRefusesAValueItCannotRun(t *testing.T) {
	const handler = `(context) => context.text("x")`
	for _, c := range []struct{ file, source, want string }{
		{"a/config.work", "const rules = { closed: (context) => false };\nrouter.guard(rules.close);",
			"config.work:3:7: router.guard(null): guard là một hàm hay một mảng hàm: router.guard((context) => …); null thường là một tên không có giá trị: viết sai, hay file của nó không export"},
		{"a/config.work", "router.guard();", "config.work:2:7: router.guard() thiếu guard: router.guard nhận một hàm hay một mảng hàm"},
		{"a/config.work", "router.guard(() => true, () => true);", "config.work:2:7: router.guard nhận một hàm hay một mảng hàm, không phải 2 đối số"},
		{"a/config.work", `router.guard([() => true, "x"]);`, `config.work:2:7: router.guard([…]): phần tử [1] là "x", không phải hàm`},
		{"a/router.work", "router.get(" + handler + ").guard(42);", "router.work:2:43: router.get(…).guard(42): guard là một hàm hay một mảng hàm"},
		{"a/router.work", "router.get(" + handler + ").guard();", "router.work:2:43: router.get(…).guard() thiếu guard"},
		{"a/router.work", "router.get(5);", "router.work:2:7: router.get(5): handler là một hàm: router.get((context) => …)"},
		{"a/router.work", "router.post();", "router.work:2:7: router.post() thiếu handler"},
		{"a/router.work", "const handlers = {};\nrouter.get(handlers.page);", "router.work:3:7: router.get(null): handler là một hàm"},
		{"a/router.work", "router.proxy(5);", "router.work:2:7: router.proxy(5): router.proxy nhận một URL hay một hàm trả về URL"},
		{"router.work", "router.sitemap(5);", "router.work:2:7: router.sitemap(5): router.sitemap nhận true (mặc định), đường dẫn một file, một object hay một hàm"},
		{"router.work", `router.meta("Quán");`, `router.work:2:7: router.meta("Quán"): router.meta nhận một object`},
		{"router.work", "router.jsonld(5);", "router.work:2:7: router.jsonld(5): router.jsonld nhận một object hay một mảng object"},
		{"router.work", `router.jsonld([{ "@type": "Organization" }, 5]);`, "router.work:2:7: router.jsonld([…]): phần tử [1] là 5, không phải object"},
		{"router.work", "router.language(5);", "router.work:2:7: router.language(5): router.language nhận một mã ngôn ngữ"},
		{"router.work", "router.favicon(5);", "router.work:2:7: router.favicon(5): router.favicon nhận đường dẫn của icon"},
		{"router.work", "router.assets(5);", "router.work:2:7: router.assets(5): router.assets nhận một thư mục, hay đường dẫn công khai rồi thư mục"},
	} {
		files := map[string]string{
			"acme/localhost/router.work":   routerImport + "router.get(" + handler + ");\n",
			"acme/localhost/a/router.work": routerImport + "router.get((context) => context.text(\"a\"));\n",
		}
		files["acme/localhost/"+c.file] = routerImport + c.source + "\n"
		if c.file == "router.work" {
			files["acme/localhost/"+c.file] += "router.get(" + handler + ");\n"
		}
		_, err := bootAppSite(t, t.TempDir(), files, "localhost")
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s %s: run = %v\nwant %s", c.file, c.source, err, c.want)
		}
	}

	// CONTROL: the shapes each word takes boot, and the guard a right name hands runs
	tenant, err := bootAppSite(t, t.TempDir(), map[string]string{
		"acme/localhost/router.work": routerImport + "router.meta({ title: \"Quán\" });\n" +
			"router.jsonld([{ \"@type\": \"Organization\" }, { \"@type\": \"WebSite\" }]);\nrouter.language(\"vi\");\n" +
			"router.favicon(\"/assets/favicon.svg\");\nrouter.assets(true);\nrouter.sitemap(true);\nrouter.robots(false);\n" +
			"router.get(" + handler + ");\n",
		"acme/localhost/a/config.work": routerImport + "const rules = { closed: (context) => false };\nrouter.guard(rules.closed);\nrouter.guard([() => true]);\n",
		"acme/localhost/a/router.work": routerImport + "router.get((context) => context.text(\"a\")).guard([() => true, () => true]);\n",
		"acme/localhost/p/router.work": routerImport + "router.proxy(\"https://cdn.example.com/logo.png\");\n",
	}, "localhost")
	if err != nil {
		t.Fatalf("CONTROL: the shapes each word takes, run = %v", err)
	}
	if rec := serveAt(tenant, http.MethodGet, "http://localhost/a"); rec.Code != http.StatusForbidden {
		t.Errorf("CONTROL: the guard rules.closed hands: GET /a = %d %q, want 403", rec.Code, rec.Body.String())
	}
	// CONTROL: a template's router passes such a value over, as it always has
	if _, err := bootAppSite(t, t.TempDir(), map[string]string{
		"acme/localhost/router.kitwork.js":   "import { router } from \"kitwork\";\nrouter.get((ctx) => ctx.text(\"x\"));\n",
		"acme/localhost/a/router.kitwork.js": "import { router } from \"kitwork\";\nconst rules = {};\nrouter.guard(rules.close);\nrouter.meta(\"Quán\");\nrouter.get((ctx) => ctx.text(\"a\"));\n",
	}, "localhost"); err != nil {
		t.Errorf("CONTROL: a template's router keeps its lenient reading, run = %v", err)
	}
}

// A built-in door written with nothing is its default, as with true (KITWORK_ROUTER.md mục 3, "Cửa sinh sẵn":
// true là mặc định): router.robots() is allow-all with the Sitemap line, router.sitemap(), router.llms(),
// router.llmsFull(), router.manifest() generate their defaults, router.rss() is router.rss(true). It booted and
// served before the values of the words were checked (24/10/2026), and the engine's own docs write it.
//
// CONTROL (go test -overlay, 24/10/2026, final fix): with routerArgumentError refusing a built-in door with no
// argument, as the review found it, the boot stopped at "router.work:2:7: router.robots(): router.robots nhận
// true (mặc định), …".
func TestUIBuiltInDoorWithNothingIsItsDefault(t *testing.T) {
	tenant, err := bootAppSite(t, t.TempDir(), map[string]string{
		"acme/localhost/router.work": routerImport + "router.robots();\nrouter.sitemap();\nrouter.llms();\nrouter.llmsFull();\nrouter.manifest();\nrouter.rss();\n" +
			"router.get((context) => context.text(\"root\"));\n",
	}, "localhost")
	if err != nil {
		t.Fatalf("run = %v, want the six built-in doors with nothing to boot", err)
	}
	for target, want := range map[string]string{
		"/robots.txt":    "User-agent: *\nAllow: /\n\nSitemap: http://localhost/sitemap.xml\n",
		"/sitemap.xml":   "<urlset xmlns=\"http://www.sitemaps.org/schemas/sitemap/0.9\"></urlset>",
		"/llms.txt":      "# Site\n",
		"/llms-full.txt": "# Site\n",
		"/manifest.json": "\"start_url\": \"/\"",
	} {
		rec := serveAt(tenant, http.MethodGet, "http://localhost"+target)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), want) {
			t.Errorf("GET %s = %d %q, want 200 with %q", target, rec.Code, rec.Body.String(), want)
		}
	}
}

// A Kitwork UI site never publishes a dot file or folder (.env, .database/, .persist/) or a source of the site
// (.work, .ui, *.kitwork.*), whatever public path names it (KITWORK_UI.md mục 5): router.assets(…), a built-in
// door's file and router.favicon(…) naming one stop the boot where they are written; and a mount the boot did
// not read that way (a template's folder inside the site mounts with its own router) is refused where the file
// is read, the folder it maps to as the URL is.
//
// CONTROL (go test -overlay, 24/10/2026): with assetDiskError answering "" every boot row booted (run = <nil>);
// with serveTreeStatic not reading the folder a mount maps to, GET /old/k answered 200 "SECRET", /old/d/secret.txt
// 200 "SECRET", /old/n 200 with the source of notes.work.
func TestUIAssetsNeverPublishADotFileOrASource(t *testing.T) {
	for _, c := range []struct{ source, want string }{
		{`router.assets("/data/*", ".private");`, `router.work:2:7: router.assets(…): ".private" là file hay thư mục dấu chấm (.env, .database/, .persist/…), không bao giờ phục vụ`},
		{`router.assets("/e", ".env");`, `router.work:2:7: router.assets(…): ".env" là file hay thư mục dấu chấm`},
		{`router.assets(".database");`, `router.work:2:7: router.assets(".database"): ".database" là file hay thư mục dấu chấm`},
		{`router.assets("ma.txt", "router.work");`, `router.work:2:7: router.assets(…): "router.work" là mã nguồn của site (.work, .ui, .kitwork.*), không bao giờ phục vụ`},
		{`router.assets("/v/*", "pages/content.ui");`, `router.work:2:7: router.assets(…): "pages/content.ui" là mã nguồn của site`},
		{`router.assets("/old/*", "old/router.kitwork.js");`, `router.work:2:7: router.assets(…): "old/router.kitwork.js" là mã nguồn của site`},
		{`router.robots(".env");`, `router.work:2:7: router.robots(".env"): ".env" là file hay thư mục dấu chấm`},
		{`router.favicon("/.env");`, `router.work:2:7: router.favicon("/.env"): "/.env" là file hay thư mục dấu chấm`},
		{`router.favicon("./router.work");`, `router.work:2:7: router.favicon("./router.work"): "./router.work" là mã nguồn của site`},
	} {
		_, err := bootAppSite(t, t.TempDir(), map[string]string{
			"acme/localhost/router.work": routerImport + c.source + "\nrouter.get((context) => context.text(\"x\"));\n",
			"acme/localhost/.env":        "TOKEN=abc\n",
		}, "localhost")
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: run = %v\nwant %s", c.source, err, c.want)
		}
	}

	tenant, err := bootAppSite(t, t.TempDir(), map[string]string{
		"acme/localhost/router.work": routerImport + "router.get((context) => context.text(\"root\"));\n",
		"acme/localhost/old/router.kitwork.js": "import { router } from \"kitwork\";\n" +
			"router.assets(\"/k\", \".secret\");\nrouter.assets(\"/d/*\", \".private\");\nrouter.assets(\"/n\", \"notes.work\");\n" +
			"router.assets(\"/ok\", \"public.txt\");\nrouter.get((ctx) => ctx.text(\"old\"));\n",
		"acme/localhost/old/.secret":             "SECRET",
		"acme/localhost/old/.private/secret.txt": "SECRET",
		"acme/localhost/old/notes.work":          "export const note = \"riêng\";\n",
		"acme/localhost/old/public.txt":          "công khai",
	}, "localhost")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	for target, want := range map[string]int{
		"/old/k":                 http.StatusNotFound,
		"/old/d/secret.txt":      http.StatusNotFound,
		"/old/n":                 http.StatusNotFound,
		"/old/ok":                http.StatusOK, // CONTROL: the template's own mount of a plain file is served
		"/old/.secret":           http.StatusNotFound,
		"/old/notes.work":        http.StatusNotFound,
		"/old/router.kitwork.js": http.StatusNotFound,
	} {
		rec := serveAt(tenant, http.MethodGet, "http://localhost"+target)
		if rec.Code != want || strings.Contains(rec.Body.String(), "SECRET") || strings.Contains(rec.Body.String(), "riêng") {
			t.Errorf("GET %s = %d %q, want %d", target, rec.Code, rec.Body.String(), want)
		}
	}
}

// A site's root is marked by its router file (the host finds a site by it): a config.work at a root with no
// router file would run nowhere, its guards with it, and neither would the app's config.work. It stops the
// boot naming the file and what to add, rather than a guard written there guarding nothing (KITWORK_ROUTER.md
// mục 4).
//
// CONTROL: the same root with a router.work boots, and its config.work's guard and the app's language apply.
// CONTROL (go test -overlay, 24/10/2026): with compileFolder not checking the root, the site booted (run =
// <nil>), GET / answered 200 with lang="vi" and GET /a 200 "a".
func TestSiteRootConfigWorkNeedsRouterWork(t *testing.T) {
	files := map[string]string{
		"acme/config.work":             routerImport + "router.language(\"fr\");\n",
		"acme/localhost/config.work":   routerImport + "router.guard((context) => false);\nrouter.colors({ brand: \"#123456\" });\n",
		"acme/localhost/content.ui":    "<content><text>trang</text></content>",
		"acme/localhost/a/router.work": routerImport + "router.get((context) => context.text(\"a\"));\n",
	}
	_, err := bootAppSite(t, t.TempDir(), files, "localhost")
	if err == nil || !strings.Contains(err.Error(), "config.work: "+routerConfigWithoutRouter) {
		t.Fatalf("run = %v\nwant config.work: %s", err, routerConfigWithoutRouter)
	}
	files["acme/localhost/router.work"] = routerImport
	tenant, err := bootAppSite(t, t.TempDir(), files, "localhost")
	if err != nil {
		t.Fatalf("CONTROL: with a router.work at the root, run = %v", err)
	}
	if code := serveAt(tenant, http.MethodGet, "http://localhost/a").Code; code != http.StatusForbidden {
		t.Errorf("CONTROL: the root's config.work guard: GET /a = %d, want 403", code)
	}
	if language := tenant.routeTree().root.folder.meta["language"]; language.Text() != "fr" {
		t.Errorf("CONTROL: the app's language = %q, want fr", language.Text())
	}
}
