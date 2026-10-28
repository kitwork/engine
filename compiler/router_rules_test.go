package compiler

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

// The rules of router.work and of the router's config.work (KITWORK_ROUTER.md mục 3, 4, 11; 24/10/2026), each
// a compile error at file:line:column naming what to write. The CONTROL of each test is in its comment: the
// code under test disabled through go test -overlay (a copy of the file, the tree untouched), the test red
// for the reason the comment quotes, then green again with the tree as it is.

const routerImport = "import * as router from \"kitwork/router\";\n"

// compileRouterApp writes files into the app acme and compiles the one at relative as the engine does: a
// config.work of the app's folder, a site or a branch as a config file, a job or a schedule as a module,
// any other as an entry.
func compileRouterApp(t *testing.T, files map[string]string, relative string) (*Bytecode, error) {
	t.Helper()
	tree := map[string]string{}
	for path, content := range files {
		tree["apps/acme/"+path] = content
	}
	root := writeApps(t, tree)
	app := filepath.Join(root, "apps", "acme")
	file := filepath.Join(app, filepath.FromSlash(relative))
	switch {
	case filepath.Base(relative) == RouterConfigFile:
		return CompileConfigFileInApp(app, file)
	case strings.HasPrefix(relative, ".shared/queue/") || strings.HasPrefix(relative, ".schedule/"):
		return CompileModuleFileInApp(app, file)
	}
	return CompileFileInApp(app, file)
}

// placeIn is where marker first stands in source, offset characters into it, as file:line:column.
func placeIn(file, source, marker string, offset int) string {
	index := strings.Index(source, marker)
	if index < 0 {
		return file + ":?"
	}
	index += len(string([]rune(marker)[:offset]))
	line := strings.Count(source[:index], "\n") + 1
	start := strings.LastIndex(source[:index], "\n") + 1
	return file + ":" + strconv.Itoa(line) + ":" + strconv.Itoa(utf8.RuneCountInString(source[start:index])+1)
}

// routerRow is a router.work (or config.work) that does not compile: the error is want, at the place of
// marker (offset characters into it).
type routerRow struct {
	name, source, marker string
	offset               int
	want                 string
}

func expectRouterErrors(t *testing.T, relative string, rows []routerRow, more ...map[string]string) {
	t.Helper()
	file := filepath.Base(relative)
	for _, row := range rows {
		files := map[string]string{relative: row.source}
		for _, extra := range more {
			for path, content := range extra {
				files[path] = content
			}
		}
		_, err := compileRouterApp(t, files, relative)
		want := placeIn(file, row.source, row.marker, row.offset) + ": " + row.want
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s:\n  got  %v\n  want %s", row.name, err, want)
		}
	}
}

func expectRouterCompiles(t *testing.T, relative string, sources []string, more ...map[string]string) {
	t.Helper()
	for _, source := range sources {
		files := map[string]string{relative: source}
		for _, extra := range more {
			for path, content := range extra {
				files[path] = content
			}
		}
		if _, err := compileRouterApp(t, files, relative); err != nil {
			t.Errorf("%q: %v, want it to compile", source, err)
		}
	}
}

// One way to the router (mục 3): exactly import * as router from "kitwork/router", named router, once per
// file, in a router.work and a site's config.work. Every other way is refused at its line, or where the router
// is read: a named or default import of kitwork/router, another name, a second line, router of "kitwork"
// (named, aliased, written out as kitwork().router, read off import * as k from "kitwork"), and router with no
// import at all. Other words of "kitwork" stay; a template's router.kitwork.js, and a source compiled with no
// file's name, keep every old way.
//
// CONTROL (24/10/2026, -overlay with readRouterFile not calling reader.imports, routerMember not refusing
// router read off "kitwork", routerDeclaration not refusing const { router } = k, and readLine not refusing
// router with no import): every row went red, compiled or refused elsewhere: "import { get }: got <nil>";
// "import router from: got router.work:2:8: router.get(…) chỉ viết ở cấp file …" (no line read as the
// router's); "a second line: got router.work:2:13: router đã được khai báo trong cùng khối …";
// "kitwork().router: got <nil>"; "const { router } = k: got <nil>"; "no import: got <nil>". (24/10/2026, fix
// of the review) With routerIndex refusing nothing, the four rows of kitwork()["router"] and k["router"] here
// and the two in TestRouterOnlyAtTheHeadOfALine went red ("kitwork()[\"router\"]: got <nil>", "k[\"router\"] in
// a handler: got <nil>"): a door declared through it in an if served at run time.
func TestRouterOneImportForm(t *testing.T) {
	const handler = "router.get((context) => context.text(\"x\"));\n"
	only := func(written, uses string) string { return RouterImportForm(written, uses) }
	expectRouterErrors(t, "shop.com/router.work", []routerRow{
		{"import { get }", "import { get } from \"kitwork/router\";\nget((context) => context.text(\"x\"));\n", "import", 0,
			only(`import { get } from "kitwork/router"`, "router.get(…)")},
		{"import { get, post }", "import { get, post } from \"kitwork/router\";\n", "import", 0,
			only(`import { get, post } from "kitwork/router"`, "router.get(…), router.post(…)")},
		{"import { get as g }", "import { get as g } from \"kitwork/router\";\n", "import", 0,
			only(`import { get as g } from "kitwork/router"`, "router.get(…)")},
		{"import router from", "import router from \"kitwork/router\";\n" + handler, "import", 0,
			only(`import router from "kitwork/router"`, "")},
		{"import * as r", "import * as r from \"kitwork/router\";\nr.get((context) => context.text(\"x\"));\n", "import", 0,
			only(`import * as r from "kitwork/router"`, "")},
		{"a second line", routerImport + routerImport + handler, "\nimport", 1,
			RouterImportTwice(1, 2)},
		{"import { router } from \"kitwork\"", "import { router } from \"kitwork\";\n" + handler, "import", 0,
			only(`import { router } from "kitwork"`, "")},
		{"import { router, safe } from \"kitwork\"", "import { router, safe } from \"kitwork\";\n" + handler, "import", 0,
			only(`import { router, safe } from "kitwork"`, "")},
		{"import { router as r } from \"kitwork\"", "import { router as r } from \"kitwork\";\nr.get((context) => context.text(\"x\"));\n", "import", 0,
			only(`import { router as r } from "kitwork"`, "")},
		{"kitwork().router", routerImport + "const r = kitwork().router;\n", "router;", 0, only("kitwork().router", "")},
		{"kitwork().router.get(…)", routerImport + "kitwork().router.get((context) => context.text(\"x\"));\n", "router.get", 0,
			only("kitwork().router", "")},
		{"k.router", routerImport + "import * as k from \"kitwork\";\nk.router.get((context) => context.text(\"x\"));\n", "router.get", 0,
			only("k.router", "")},
		{"const { router } = k", routerImport + "import * as k from \"kitwork\";\nconst { router: r } = k;\n", "router: r", 0,
			only("const { router } = k", "")},
		{"const { router } = kitwork()", "const { router } = kitwork();\n", "router", 0, only("const { router } = kitwork()", "")},
		{"no import", handler, "router", 0, only("router.get(…) không có dòng import", "")},
		{"import { view }", "import { view } from \"kitwork/router\";\n", "import", 0, RouterViewRemoved},
		{"kitwork()[\"router\"]", routerImport + "kitwork()[\"router\"].get((context) => context.text(\"x\"));\n", "\"router\"]", 0,
			only(`kitwork()["router"]`, "")},
		{"k[\"router\"]", routerImport + "import * as k from \"kitwork\";\nconst r = k[\"router\"];\n", "\"router\"]", 0,
			only(`k["router"]`, "")},
		{"kitwork()[\"router\"] with no import line", "kitwork()[\"router\"].get((context) => context.text(\"x\"));\n", "\"router\"]", 0,
			only(`kitwork()["router"]`, "")},
	})
	// a site's config.work takes the same one line
	expectRouterErrors(t, "shop.com/config.work", []routerRow{
		{"config.work: import { router }", "import { router } from \"kitwork\";\nrouter.colors({ brand: \"#2563eb\" });\n", "import", 0,
			only(`import { router } from "kitwork"`, "")},
		{"config.work: import * as r", "import * as r from \"kitwork/router\";\nr.colors({ brand: \"#2563eb\" });\n", "import", 0,
			only(`import * as r from "kitwork/router"`, "")},
		{"config.work: kitwork()[\"router\"]", "kitwork()[\"router\"].get((context) => context.text(\"x\"));\n", "\"router\"]", 0,
			only(`kitwork()["router"]`, "")},
	})

	expectRouterCompiles(t, "shop.com/router.work", []string{
		routerImport + "import { environment, richtext, safe } from \"kitwork\";\nrouter.get((context) => context.json({ ok: safe(() => 1).ok }));\n",
		routerImport + "import * as k from \"kitwork\";\nrouter.get((context) => context.json({ port: k.environment.PUBLIC_PORT }));\n",
		routerImport + handler,
	})
	// a template site's router.kitwork.js keeps import { router } from "kitwork", and a source with no file's name
	// keeps import { get } from "kitwork/router" (compiler/http_send_test.go)
	expectRouterCompiles(t, "shop.com/router.kitwork.js", []string{
		"import { router } from \"kitwork\";\nrouter.get((ctx) => ctx.text(\"x\"));\n",
		"import { router as r } from \"kitwork\";\nconst k = kitwork().router;\nr.get((ctx) => ctx.text(\"x\"));\n",
	})
	if _, err := CompileSource("import { get } from \"kitwork/router\";\nget((ctx) => ctx.text(\"x\"));\n"); err != nil {
		t.Errorf("a source with no file's name: %v, want it to compile", err)
	}
}

// kitwork/router is only in a router.work and a site's config.work (mục 3): any other .work file that takes
// it, in any form, is refused where it does: a library alone or bundled into a router.work, a job, a
// schedule, a schema, the config.work of .shared/queue/ and .schedule/, the manifest. A .kitwork.js library
// keeps it.
//
// CONTROL (24/10/2026, -overlay with refuseRouterImport returning nil at its start): every row went red,
// "a library alone: got <nil>", "a library, kitwork().router: got <nil>", "a library bundled into a
// router.work: <nil>", "the manifest: <nil>"…; a schema file compiled alone failed on a rule of its own
// instead ("shop.work:2:1: import từ \"kitwork/schema\" chỉ có trong .shared/schema/ …"). (24/10/2026, fix of the
// review) With a const of the module not read as the module and [ ] not read: the five rows of const k =
// kitwork(), const k2 = k and ["router"] went red ("a library, const k = kitwork(); k.router: got <nil>").
func TestRouterImportOutsideRouterFiles(t *testing.T) {
	library := "import * as router from \"kitwork/router\";\nexport const helper = () => 1;\n"
	expectRouterErrors(t, ".shared/lib.work", []routerRow{
		{"a library alone", library, "import", 0, RouterImportOutside},
		{"a library, import { router }", "import { router, safe } from \"kitwork\";\nexport const helper = () => 1;\n", "import", 0, RouterImportOutside},
		{"a library, import { router as r }", "import { router as r } from \"kitwork\";\nexport const helper = () => 1;\n", "import", 0, RouterImportOutside},
		{"a library, kitwork().router", "export const helper = () => kitwork().router;\n", "router", 0, RouterImportOutside},
		{"a library, k.router", "import * as k from \"kitwork\";\nexport const helper = () => k.router;\n", "router", 0, RouterImportOutside},
		{"a library, const { router } = k", "import * as k from \"kitwork\";\nconst { router } = k;\nexport const helper = () => router;\n", "router }", 0, RouterImportOutside},
		{"a library, const k = kitwork(); k.router", "const k = kitwork();\nexport const helper = () => k.router;\n", "router;", 0, RouterImportOutside},
		{"a library, const k2 = k; k2.router", "import * as k from \"kitwork\";\nconst k2 = k;\nexport const helper = () => k2.router;\n", "router;", 0, RouterImportOutside},
		{"a library, kitwork()[\"router\"]", "export const helper = () => kitwork()[\"router\"];\n", "\"router\"", 0, RouterImportOutside},
		{"a library, k[\"router\"]", "const k = kitwork();\nexport const helper = () => k[\"router\"];\n", "\"router\"", 0, RouterImportOutside},
		{"a library, const { router } = kitwork() through k", "const k = kitwork();\nconst { router: r } = k;\nexport const helper = () => r;\n", "router: r", 0, RouterImportOutside},
	})
	// bundled into a router.work: the library's own line, named from the router's folder
	_, err := compileRouterApp(t, map[string]string{
		".shared/lib.work":     library,
		"shop.com/router.work": routerImport + "import { helper } from \"app/.shared/lib.work\";\nrouter.get((context) => context.json({ n: helper() }));\n",
	}, "shop.com/router.work")
	if want := "../.shared/lib.work:1:1: " + RouterImportOutside; err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("a library bundled into a router.work: %v, want %s", err, want)
	}
	for _, place := range []struct{ relative, source string }{
		{".shared/queue/mail.work", "import * as router from \"kitwork/router\";\nimport { queue } from \"kitwork\";\nexport const mail = queue.job((context) => null);\n"},
		{".schedule/daily.work", "import { router } from \"kitwork\";\nimport * as schedule from \"kitwork/schedule\";\nexport const daily = schedule.daily(\"09:00\").run((context) => null);\n"},
		{".shared/queue/config.work", "import * as router from \"kitwork/router\";\nimport * as queue from \"kitwork/queue\";\nqueue.parallel(4);\n"},
		{".schedule/config.work", "import * as router from \"kitwork/router\";\nimport * as schedule from \"kitwork/schedule\";\nschedule.keep(\"7d\");\n"},
		{".shared/schema/shop.work", "import * as router from \"kitwork/router\";\nimport { struct, id } from \"kitwork/schema\";\nimport { connection } from \"kitwork/database\";\nconst items = struct({ id: id() });\nexport const db = connection().schema({ items });\n"},
	} {
		_, err := compileRouterApp(t, map[string]string{place.relative: place.source}, place.relative)
		if want := filepath.Base(place.relative) + ":1:1: " + RouterImportOutside; err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %s", place.relative, err, want)
		}
	}
	root := writeApps(t, map[string]string{"application.work": "import * as router from \"kitwork/router\";\nimport * as app from \"kitwork/application\";\n"})
	if _, err := CompileFile(filepath.Join(root, "application.work")); err == nil || !strings.Contains(err.Error(), "application.work:1:1: "+RouterImportOutside) {
		t.Errorf("the manifest: %v, want application.work:1:1: %s", err, RouterImportOutside)
	}
	expectRouterCompiles(t, ".shared/lib.kitwork.js", []string{"import { router } from \"kitwork\";\nexport const helper = () => 1;\n"})
	expectRouterCompiles(t, ".shared/lib.work", []string{"import { safe, environment } from \"kitwork\";\nexport const helper = () => safe(() => 1);\n"})
}

// router stands only at the head of a call that is a line of its own at the file's top level (mục 3): read
// as a value (const r = router, helper(router), router["get"], router.get with no call, as an argument), or
// called in a function, an if, a loop or as part of another expression, it is refused where it is read. A
// parameter of a handler named router is the handler's own.
//
// CONTROL (24/10/2026, -overlay with routerRead returning false, routerCall letting every call through as a
// head, routerMember not refusing a word read off the router, and readRouterFile's lines not refusing
// const r = router): every row compiled ("helper(router): got <nil>", "in a handler: got <nil>"…).
func TestRouterOnlyAtTheHeadOfALine(t *testing.T) {
	const handler = "(context) => context.text(\"x\")"
	expectRouterErrors(t, "shop.com/router.work", []routerRow{
		{"const r = router", routerImport + "const r = router;\n", "router;", 0, RouterAsValue("router")},
		{"const { get } = router", routerImport + "const { get: g } = router;\n", "router;", 0, RouterAsValue("router")},
		{"helper(router)", routerImport + "const helper = (r) => r;\nhelper(router);\n", "(router)", 1, RouterAsValue("router")},
		{"router[\"get\"]", routerImport + "router[\"get\"](" + handler + ");\n", "router[", 0, RouterAsValue("router")},
		{"router.get read", routerImport + "const g = router.get;\n", "get;", 0, RouterAsValue("router.get")},
		{"router.get as an argument", routerImport + "const helper = (r) => r;\nhelper(router.get);\n", "get)", 0, RouterAsValue("router.get")},
		{"in an if", routerImport + "if (true) {\n  router.get(" + handler + ");\n}\n", "get(", 0, RouterNotAtTop("get")},
		{"in a loop", routerImport + "for (const color of [\"#000000\"]) {\n  router.colors({ brand: color });\n}\n", "colors(", 0, RouterNotAtTop("colors")},
		{"in a function", routerImport + "const declare = () => router.guard(() => true);\n", "guard(", 0, RouterNotAtTop("guard")},
		{"in a handler", routerImport + "router.get((context) => {\n  router.guard(() => true);\n  return context.text(\"x\");\n});\n", "guard(", 0, RouterNotAtTop("guard")},
		{"as a value of a const", routerImport + "const route = router.get(" + handler + ");\n", "get(", 0, RouterNotAtTop("get")},
		{"in a larger expression", routerImport + "router.get(" + handler + ") || router.post(" + handler + ");\n", "get(", 0, RouterNotAtTop("get")},
		{"config.work: in a function", routerImport + "const later = () => router.colors({ brand: \"#000000\" });\n", "colors(", 0, RouterNotAtTop("colors")},
		// kitwork()["router"] would declare a door the static list never sees (an if, a handler)
		{"kitwork()[\"router\"] in an if", routerImport + "if (true) {\n  kitwork()[\"router\"].get(" + handler + ");\n}\n", "\"router\"]", 0,
			RouterImportForm(`kitwork()["router"]`, "")},
		{"k[\"router\"] in a handler", routerImport + "import * as k from \"kitwork\";\nrouter.get((context) => {\n  k[\"router\"].guard(() => true);\n  return context.text(\"x\");\n});\n", "\"router\"]", 0,
			RouterImportForm(`k["router"]`, "")},
	})
	expectRouterErrors(t, "shop.com/config.work", []routerRow{
		{"config.work: const r = router", routerImport + "const r = router;\n", "router;", 0, RouterAsValue("router")},
		{"config.work: in an if", routerImport + "if (true) {\n  router.colors({ brand: \"#000000\" });\n}\n", "colors(", 0, RouterNotAtTop("colors")},
	})
	expectRouterCompiles(t, "shop.com/router.work", []string{
		routerImport + "router.get((router) => router.text(\"x\"));\n",
		routerImport + "const handle = " + handler + ";\nrouter.get(handle).guard(() => true).cache(\"5m\");\n",
		routerImport + "router.get(" + handler + ")\n  .guard(() => true)\n  .persist(\"1h\");\n",
	})
}

// The words of the router are closed (mục 3, 4, 11): a line begins with a door, a built-in door or a folder
// call; a door chains guard and limit as often as written, cache and persist once; a folder call chains
// nothing (two lines), and one whose nearest folder wins is called once in a file. Any other word is an error
// with the nearest word (router.GET too, compared as written); an old word names the new spelling. A door is
// not declared in a config.work, and router.timezone is only the app's config.work's.
//
// CONTROL (24/10/2026, -overlay with call returning right after it records the head, before any word is
// read): 54 of the 55 refused rows went red ("router.colours: got <nil>", "router.guard(a).get(b): got
// <nil>"…; .view(1) after a door met the context.view rule instead); router.view stayed refused, by the
// resolver (RouterViewRemoved). With RouterNearestWord comparing case (no strings.EqualFold, no lower case in
// the distance): "router.GET: got router.work:2:8: router.GET không có; router có get, post, …", and the
// table's "nearest of GET = \"\"".
func TestRouterClosedVocabulary(t *testing.T) {
	const handler = "(context) => context.text(\"x\")"
	rows := []routerRow{
		{"router.colours", routerImport + "router.colours({ brand: \"#2563eb\" });\n", "colours", 0, "router.colours không có; ý là router.colors?"},
		{"router.GET", routerImport + "router.GET(" + handler + ");\n", "GET", 0, "router.GET không có; ý là router.get?"},
		{"router.gaurd", routerImport + "router.gaurd(() => true);\n", "gaurd", 0, "router.gaurd không có; ý là router.guard?"},
		{"router.llmsfull", routerImport + "router.llmsfull(true);\n", "llmsfull", 0, "router.llmsfull không có; ý là router.llmsFull?"},
		{"router.zzz", routerImport + "router.zzz(1);\n", "zzz", 0, "router.zzz không có; router có get, view, post, put, patch, delete, proxy, sitemap"},
		{".cahce", routerImport + "router.get(" + handler + ").cahce(\"5m\");\n", "cahce", 0, ".cahce không nối sau router.get(…); ý là .cache?"},
		{".Guard", routerImport + "router.post(" + handler + ").Guard(() => true);\n", "Guard", 0, ".Guard không nối sau router.post(…); ý là .guard?"},
		{".type", routerImport + "router.get(" + handler + ").type(\"text/plain\");\n", "type(", 0, ".type không nối sau router.get(…): sau cửa chỉ nối .guard(…), .limit(…), .cache(…), .persist(…)"},
		{".cache read", routerImport + "router.get(" + handler + ").cache;\n", ".cache", 1, RouteRuleNotACall("get", ".cache")},
		{"a call of a door's", routerImport + "router.get(" + handler + ")(1);\n", "(1)", 0, RouteRuleNotACall("get", "(…)")},
		{".cache twice", routerImport + "router.get(" + handler + ").cache(\"5m\").persist(\"1h\").cache(\"1m\");\n", "cache(\"1m", 0,
			RouteRuleTwice("get", "cache", "router.work:2:44", "router.work:2:70")},
		{".persist twice", routerImport + "router.get(" + handler + ")\n  .persist(\"1h\")\n  .persist(\"2h\");\n", "persist(\"2h", 0,
			RouteRuleTwice("get", "persist", "router.work:3:4", "router.work:4:4")},
		{"router.guard(a).get(b)", routerImport + "router.guard(() => true).get(" + handler + ");\n", "get(", 0, RouterCallChained("guard", ".get(…)")},
		{"router.reload(false).transition(\"fade\")", routerImport + "router.reload(false).transition(\"fade\");\n", "transition", 0, RouterCallChained("reload", ".transition(…)")},
		{"router.colors(…).x", routerImport + "router.colors({ brand: \"#000000\" }).x;\n", "x;", 0, RouterCallChained("colors", ".x")},
		{"router.guard(a)(b)", routerImport + "router.guard(() => true)(1);\n", "(1)", 0, RouterCallChained("guard", "(…)")},
		{"router.language twice", routerImport + "router.language(\"vi\");\nrouter.get(" + handler + ");\nrouter.language(\"en\");\n", "language(\"en", 0,
			RouterCallTwice("language", "dòng 2", "dòng 4")},
		{"router.reload twice", routerImport + "router.reload(false);\nrouter.reload(true);\n", "reload(true", 0, RouterCallTwice("reload", "dòng 2", "dòng 3")},
		{"router.timezone in router.work", routerImport + "router.timezone(\"Asia/Ho_Chi_Minh\");\n", "timezone", 0,
			"router.timezone chỉ viết trong config.work của gốc app, không trong router.work (KITWORK_ROUTER.md mục 4)"},
	}
	// every old word a line began with names its new spelling
	for _, word := range []string{"ratelimit", "rateLimit", "title", "description", "image", "url", "type", "theme", "themes",
		"jittheme", "jitTheme", "css", "jitcss", "middleware", "notfound", "notFound", "error", "cache", "headers", "redirect",
		"drive", "javascript", "jitjs", "connection", "connect"} {
		message, ok := RouterRemovedWord(word)
		if !ok {
			t.Fatalf("router.%s is no removed word", word)
		}
		rows = append(rows, routerRow{"router." + word, routerImport + "router." + word + "({ a: 1 });\n", word + "(", 0, message})
	}
	for _, word := range []string{"ratelimit", "rateLimit", "error", "success", "finally", "then", "catch", "middleware", "view", "handle"} {
		message, _ := RouteRuleRemoved("post", word)
		rows = append(rows, routerRow{"." + word, routerImport + "router.post(" + handler + ")." + word + "(1);\n", word + "(1", 0, message})
	}
	expectRouterErrors(t, "shop.com/router.work", rows)

	for word, spelling := range map[string]string{
		"ratelimit": "router.limit({ rate, per, by })", "title": "router.meta({", "theme": "router.colors({ … }), phông chữ là router.fonts({ … })",
		"middleware": "router.guard(…)", "notfound": "notfound.ui", "error": "error.ui", "cache": `.cache("5m")`, "headers": "context.header(…)",
		"redirect": "context.status(301).redirect", "drive": "router.reload(false)", "jitjs": "router.reload(false)",
	} {
		if message, _ := RouterRemovedWord(word); !strings.Contains(message, spelling) {
			t.Errorf("router.%s: %q does not name %s", word, message, spelling)
		}
	}
	for word, spelling := range map[string]string{"ratelimit": ".limit({ rate, per, by })", "error": "error.ui", "view": "context.title(\"…\").view({ … })", "handle": "router.get((context) => …)"} {
		if message, _ := RouteRuleRemoved("get", word); !strings.Contains(message, spelling) {
			t.Errorf(".%s: %q does not name %s", word, message, spelling)
		}
	}

	expectRouterCompiles(t, "shop.com/router.work", []string{
		routerImport + "router.get(" + handler + ").guard(() => true).guard(() => true).limit({ rate: 5, per: \"1m\" }).limit({ rate: 50, per: \"1h\" }).cache(\"5m\").persist(\"1h\");\n",
		routerImport + "router.post(" + handler + ");\nrouter.put(" + handler + ");\nrouter.patch(" + handler + ");\nrouter.delete(" + handler + ");\n",
		routerImport + "router.proxy(\"https://cdn.example.com/logo.png\").persist(\"30d\");\n",
		routerImport + "router.sitemap(true).cache(\"1h\");\nrouter.rss({ title: \"a\", description: \"b\", link: \"/\" });\nrouter.robots(true);\nrouter.llms(true);\nrouter.llmsFull(true);\nrouter.manifest(true);\n",
		routerImport + "router.guard(() => true);\nrouter.guard(() => true);\nrouter.colors({ brand: \"#2563eb\" });\nrouter.colors({ ink: \"#111111\" });\nrouter.meta({ title: \"a\" });\nrouter.meta({ description: \"b\" });\n",
		routerImport + "router.language(\"vi\");\nrouter.appearance(\"system\");\nrouter.contrast(\"warning\");\nrouter.transition(\"fade\");\nrouter.bundle(\"auto\");\nrouter.favicon(\"/assets/icon.svg\");\nrouter.cors({ origin: \"https://a.example\" });\nrouter.reload(false);\nrouter.limit({ rate: 600, per: \"1m\" });\nrouter.jsonld({ name: \"a\" });\nrouter.assets(\"_public\");\nrouter.fonts({ body: \"Outfit\" });\nrouter.gradients({ dawn: [\"#000000\", \"#ffffff\"] });\nrouter.highlight({ keyword: \"brand\" });\n",
	})
}

// A site's config.work (and the app's, and a branch's) holds only the rules of its branch (mục 4): a door is
// refused there, the calls of a branch and code of its own are not. router.timezone is the app's config.work's
// only, router.reload a site's root's: a config.work deeper than the app's folder is no app's, and one inside
// a site's folder is a branch's. What the path cannot tell (a config.work of the app's folder may be a site's
// too) is the engine's to check as it runs the folders.
//
// CONTROL (24/10/2026, -overlay with call not refusing a door in a config.work, and wrongLevel returning ""):
// "router.get in config.work: got <nil>", "router.timezone in a site's config.work: got <nil>", "router.reload
// in a branch's config.work: got <nil>".
func TestRouterConfigFileRules(t *testing.T) {
	const handler = "(context) => context.text(\"x\")"
	expectRouterErrors(t, "shop.com/config.work", []routerRow{
		{"router.get in config.work", routerImport + "router.get(" + handler + ");\n", "get", 0, RouterDoorInConfig("get")},
		{"router.sitemap in config.work", routerImport + "router.sitemap(true);\n", "sitemap", 0, RouterDoorInConfig("sitemap")},
		{"router.proxy in config.work", routerImport + "router.proxy(\"https://a.example/x\");\n", "proxy", 0, RouterDoorInConfig("proxy")},
		{"router.timezone in a site's config.work", routerImport + "router.timezone(\"Asia/Ho_Chi_Minh\");\n", "timezone", 0,
			"router.timezone chỉ viết trong config.work của gốc app, không trong config.work của một site hay một nhánh (KITWORK_ROUTER.md mục 4)"},
		{"a word of no set", routerImport + "router.colours({ brand: \"#000000\" });\n", "colours", 0, "router.colours không có; ý là router.colors?"},
		{"a door's typo in config.work", routerImport + "router.gett(" + handler + ");\n", "gett", 0, "router.gett không có; router có timezone, language"},
	})
	expectRouterErrors(t, "shop.com/dashboard/config.work", []routerRow{
		{"router.reload in a branch's config.work", routerImport + "router.reload(false);\n", "reload", 0,
			"router.reload chỉ viết ở gốc site, không trong config.work của một nhánh (KITWORK_ROUTER.md mục 4)"},
	})
	expectRouterErrors(t, "shop.com/dashboard/router.work", []routerRow{
		{"router.reload in a branch's router.work", routerImport + "router.reload(false);\n", "reload", 0,
			"router.reload chỉ viết ở gốc site, không trong router.work của một nhánh (KITWORK_ROUTER.md mục 4)"},
	})
	expectRouterCompiles(t, "config.work", []string{
		routerImport + "router.timezone(\"Asia/Ho_Chi_Minh\");\nrouter.colors({ brand: \"#2563eb\" });\nrouter.language(\"vi\");\n",
		// the app's folder may be a site's too: a call of a site is the engine's to check
		routerImport + "router.reload(false);\nrouter.meta({ title: \"a\" });\n",
	})
	expectRouterCompiles(t, "shop.com/config.work", []string{
		routerImport + "import { environment } from \"kitwork\";\nconst brand = environment.PUBLIC_BRAND || \"#2563eb\";\nconst gate = (context) => context.query(\"key\") === \"open\";\nrouter.guard(gate);\nrouter.colors({ brand });\nrouter.reload(false);\n",
	})
	expectRouterCompiles(t, "shop.com/dashboard/config.work", []string{
		routerImport + "router.guard(() => true);\nrouter.colors({ brand: \"#16a34a\" });\nrouter.transition(\"slide\");\n",
	})
	// the config.work of .shared/queue/ keeps its own words (folder_config.go)
	if _, err := compileRouterApp(t, map[string]string{".shared/queue/config.work": "import * as queue from \"kitwork/queue\";\nqueue.parallel(4);\n"}, ".shared/queue/config.work"); err != nil {
		t.Errorf("the queue's config.work: %v", err)
	}
}

// A config.work is the router's by one rule, whichever entry compiles it (routerFileOf): CompileConfigFileInApp,
// as the engine will run it, and CompileFileInApp (prepareFile), as a check of the app reads any file. In the
// app's folder, a site or a branch the router's rules hold (a door, router.timezone below the app's folder);
// in a folder that is no route (.shared/, .shared/schema/, _drafts/, a site's _x/) it is a file like any
// other, and kitwork/router is refused there.
//
// CONTROL (24/10/2026, -overlay with CompileConfigFileInApp deciding by folderOf alone, as before the fix): the
// four rows of a folder that is no route went red through CompileConfigFileInApp, and only through it
// ("CompileConfigFileInApp .shared/config.work: <nil>, want config.work:1:1: kitwork/router chỉ có …"). With
// routerFileOf answering notRouterFile for every config.work, every row of the app's folder, a site and a branch
// went red through both entries ("CompileFileInApp shop.com/config.work: config.work:1:1: kitwork/router chỉ có
// …, want config.work:2:8: router.get(…) trong config.work …").
func TestRouterConfigFileIsReadAlikeByBothEntries(t *testing.T) {
	const handler = "(context) => context.text(\"x\")"
	entries := map[string]func(app, file string) (*Bytecode, error){
		"CompileConfigFileInApp": CompileConfigFileInApp,
		"CompileFileInApp":       func(app, file string) (*Bytecode, error) { return CompileFileInApp(app, file) },
	}
	compile := func(entry, relative, source string) error {
		root := writeApps(t, map[string]string{"apps/acme/" + relative: source})
		app := filepath.Join(root, "apps", "acme")
		_, err := entries[entry](app, filepath.Join(app, filepath.FromSlash(relative)))
		return err
	}
	for _, entry := range []string{"CompileConfigFileInApp", "CompileFileInApp"} {
		for _, row := range []struct{ relative, source, marker, want string }{
			{"shop.com/config.work", routerImport + "router.get(" + handler + ");\n", "get(", RouterDoorInConfig("get")},
			{"shop.com/config.work", routerImport + "router.timezone(\"Asia/Ho_Chi_Minh\");\n", "timezone",
				"router.timezone chỉ viết trong config.work của gốc app, không trong config.work của một site hay một nhánh (KITWORK_ROUTER.md mục 4)"},
			{"shop.com/dashboard/config.work", routerImport + "router.reload(false);\n", "reload",
				"router.reload chỉ viết ở gốc site, không trong config.work của một nhánh (KITWORK_ROUTER.md mục 4)"},
			{".shared/config.work", routerImport + "router.colors({ brand: \"#000000\" });\n", "import", RouterImportOutside},
			{".shared/schema/config.work", routerImport + "router.colors({ brand: \"#000000\" });\n", "import", RouterImportOutside},
			{"_drafts/config.work", routerImport + "router.colors({ brand: \"#000000\" });\n", "import", RouterImportOutside},
			{"shop.com/_x/config.work", routerImport + "router.colors({ brand: \"#000000\" });\n", "import", RouterImportOutside},
		} {
			err := compile(entry, row.relative, row.source)
			if want := placeIn(RouterConfigFile, row.source, row.marker, 0) + ": " + row.want; err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("%s %s: %v, want %s", entry, row.relative, err, want)
			}
		}
		for _, relative := range []string{"config.work", "shop.com/config.work", "shop.com/dashboard/config.work"} {
			if err := compile(entry, relative, routerImport+"router.colors({ brand: \"#000000\" });\nrouter.guard(() => true);\n"); err != nil {
				t.Errorf("%s %s: %v, want it to compile", entry, relative, err)
			}
		}
		// a config.work of a folder that is no route compiles as any file does, without the router
		if err := compile(entry, ".shared/config.work", "export const brand = \"#000000\";\n"); err != nil {
			t.Errorf("%s .shared/config.work with no router: %v, want it to compile", entry, err)
		}
	}
}

// In a router.work a const or let of the top level named get, post, put or patch (destructured names among
// them) is refused at the name: it looks like a door and opens none. A const of a handler is its own.
//
// CONTROL (24/10/2026, -overlay with lines not reading routerConstDoors): every row compiled ("const get:
// got <nil>").
func TestRouterWorkConstOfADoor(t *testing.T) {
	expectRouterErrors(t, "shop.com/router.work", []routerRow{
		{"const get", routerImport + "const get = (context) => context.text(\"x\");\nrouter.get(get);\n", "get =", 0, RouterConstDoor("const", "get")},
		{"let post", routerImport + "let post = 1;\n", "post", 0, RouterConstDoor("let", "post")},
		{"const { put }", routerImport + "const handlers = { put: 1 };\nconst { put } = handlers;\n", "put }", 0, RouterConstDoor("const", "put")},
		{"const [patch]", routerImport + "const [patch] = [1];\n", "patch", 0, RouterConstDoor("const", "patch")},
	})
	expectRouterCompiles(t, "shop.com/router.work", []string{
		routerImport + "const getProducts = (context) => context.json([]);\nrouter.get(getProducts);\n",
		routerImport + "router.post((context) => {\n  const post = context.request.form(\"post\");\n  return context.text(post);\n});\n",
		routerImport + "const remove = 1;\nconst del = 2;\n",
	})
	// a config.work declares no door, and its const get is its own (it may not call router.get anyway)
	expectRouterCompiles(t, "shop.com/config.work", []string{routerImport + "const get = 1;\nrouter.language(\"vi\");\n"})
}

// context.view in a router.work takes an object written in place whose keys are plain names (KITWORK_UI.md
// mục 4), so the page knows each name it is given: a variable, a call, a spread, a computed or number key is
// refused at the argument, and so is a second argument (the engine draws the page with the last object, so the
// first would give nothing; the earliest error in the file is told). view taken apart from context is refused
// too: (context.view)(…), context["view"],
// const { view } = context, ({ view }) => view(…). The context is what a router line hands a handler or a guard
// first, under any name, followed into a top-level function handed it, a name bound to it, its response, and
// past a modifier; view read off anything else (a body's field, a row's column, a setting) is data and
// compiles. context.view() with nothing compiles. A page of .shared/ and a template's router.kitwork.js are not
// router.work.
//
// CONTROL (24/10/2026, -overlay with readRouterFile not calling reader.views): every row compiled
// ("context.view(data): got <nil>", "(context.view)({ … }): got <nil>"…). (24/10/2026, fix of the review) With
// contextViews not called, its 15 rows compiled ("a handler by name: got <nil>"); with contextRooted taking
// any receiver for the context (the rule as it was), the data rows went red ("router.work:4:36: body.view: view
// tách khỏi context …", "router.work:6:25: row.view: …", "router.work:2:30: item.view: …"); with the context
// followed no further than the parameter itself, "render(context)", "const c = context", "context.response.view"
// and "context.title(…).view" compiled. (24/10/2026, fix of the ui review) With the second argument not looked
// at, "context.view({ a }, { b }): got <nil>"; with it told in place of the first argument's error,
// "context.view(data, { b })" said router.work:3:44 "context.view({ … }, …)", not 3:38 "context.view(data)".
// (24/10/2026, final fix) With readRouterFile reading views in a router.work only, as before, the four rows of
// config.work compiled, in a site's and in a branch's ("a guard's context.view(data): got <nil>", "a guard by
// name, ({ view }): got <nil>"). (25/10/2026, through a copy of router_rules.go) With a call through a name the
// handler declares followed (outer not checked), the three rows of the handler's own draw went red at the file's
// draw ("router.work:3:11: { view }: view tách khỏi context"); with a function written in place not seeing the
// names around it, its row did; with a name the file assigns again followed, the let draw row did; with a block's
// names not its own, "render(context) beside a const of another block: got <nil>"; with a function's parameters
// added to the names around it, "render(context) beside a callback's parameter: got <nil>".
func TestContextViewTakesAnObjectWrittenInPlace(t *testing.T) {
	expectRouterErrors(t, "shop.com/router.work", []routerRow{
		{"context.view(data)", routerImport + "router.get((context) => {\n  const data = { a: 1 };\n  return context.view(data);\n});\n", "data);", 0,
			ContextViewObject("context.view(data)")},
		{"context.view(load())", routerImport + "const load = () => ({ a: 1 });\nrouter.get((context) => context.view(load()));\n", "load())", 0,
			ContextViewObject("context.view(…)")},
		{"ctx.title(…).view(data)", routerImport + "const data = { a: 1 };\nrouter.get((ctx) => ctx.title(\"a\").view(data));\n", "data))", 0,
			ContextViewObject("….view(data)")},
		{"context.view({ ...data })", routerImport + "const data = { a: 1 };\nrouter.get((context) => context.view({ ...data }));\n", "data })", 0,
			ContextViewObject("context.view({ ...data })")},
		{"context.view({ [key]: 1 })", routerImport + "const key = \"a\";\nrouter.get((context) => context.view({ [key]: 1 }));\n", "[key]", 0,
			ContextViewObject("context.view({ [key]: … })")},
		{"context.view({ 1: x })", routerImport + "router.get((context) => context.view({ 1: \"x\" }));\n", "1: ", 0,
			ContextViewObject("context.view({ 1: … })")},
		{"context.view({ a }, { b })", routerImport + "router.get((context) => context.view({ a: 1 }, { b: 2 }));\n", "{ b", 0,
			ContextViewObject("context.view({ … }, …)")},
		{"context.view(data, { b })", routerImport + "const data = { a: 1 };\nrouter.get((context) => context.view(data, { b: 2 }));\n", "data,", 0,
			ContextViewObject("context.view(data)")},
		{"(context.view)({ … })", routerImport + "router.get((context) => (context.view)({ a: 1 }));\n", "view)", 0,
			ContextViewApart("context.view")},
		{"context[\"view\"]", routerImport + "router.get((context) => context[\"view\"]({ a: 1 }));\n", "\"view\"", 0,
			ContextViewApart("context[\"view\"]")},
		{"const { view } = context", routerImport + "router.get((context) => {\n  const { view } = context;\n  return view({ a: 1 });\n});\n", "view }", 0,
			ContextViewApart("{ view }")},
		{"({ view }) => view(…)", routerImport + "router.get(({ view }) => view({ a: 1 }));\n", "view }", 0,
			ContextViewApart("{ view }")},
		{"typeof context.view", routerImport + "router.get((context) => context.json({ kind: typeof context.view }));\n", "view }", 0,
			ContextViewApart("context.view")},
		// the context under any name, handed by name, followed into a function the file calls with it, bound to
		// another name, its response, past a modifier, in a guard
		{"a handler by name", routerImport + "const handle = (request) => (request.view)({ a: 1 });\nrouter.get(handle);\n", "view)", 0,
			ContextViewApart("request.view")},
		{"render(context)", routerImport + "const render = (page, context) => {\n  const { view } = context;\n  return view({ page });\n};\nrouter.get((context) => render(1, context));\n", "view }", 0,
			ContextViewApart("{ view }")},
		{"const c = context", routerImport + "router.get((context) => {\n  const c = context;\n  return c[\"view\"]({ a: 1 });\n});\n", "\"view\"", 0,
			ContextViewApart("c[\"view\"]")},
		{"({ response }) => response.view", routerImport + "router.get(({ request, response }) => (response.view)({ a: 1 }));\n", "view)", 0,
			ContextViewApart("response.view")},
		{"({ response: { view } })", routerImport + "router.get(({ response: { view } }) => view({ a: 1 }));\n", "view }", 0,
			ContextViewApart("{ view }")},
		{"context.response.view", routerImport + "router.get((context) => (context.response.view)({ a: 1 }));\n", "view)", 0,
			ContextViewApart("….view")},
		{"context.title(…).view", routerImport + "router.get((context) => (context.title(\"a\").view)({ a: 1 }));\n", "view)", 0,
			ContextViewApart("….view")},
		{"a route's guard", routerImport + "router.get((context) => context.text(\"x\")).guard((context) => typeof context.view === \"function\");\n", "view ===", 0,
			ContextViewApart("context.view")},
		{"a branch's guard", routerImport + "const gate = (context) => context[\"view\"] !== null;\nrouter.guard(gate);\n", "\"view\"", 0,
			ContextViewApart("context[\"view\"]")},
		{"in a callback of the handler", routerImport + "router.get((context) => context.json([1].map((n) => typeof context.view)));\n", "view)", 0,
			ContextViewApart("context.view")},
		// a name declared in a block that does not hold the call, or by a callback beside it, is not the call's:
		// render(context) is the file's render
		{"render(context) beside a const of another block", routerImport + "const render = (context) => {\n  const { view } = context;\n  return view({ a: \"given\" });\n};\nrouter.get((context) => {\n  if (context.request.method === \"HEAD\") {\n    const render = 1;\n  }\n  return render(context);\n});\n", "view }", 0,
			ContextViewApart("{ view }")},
		{"render(context) beside a callback's parameter", routerImport + "const render = (context) => (context.view)({ a: 1 });\nrouter.get((context) => {\n  const names = [\"a\"].map((render) => render);\n  return render(context);\n});\n", "view)", 0,
			ContextViewApart("context.view")},
	})
	expectRouterCompiles(t, "shop.com/router.work", []string{
		routerImport + "router.get((context) => context.view());\n",
		routerImport + "router.get((context) => {\n  const products = [];\n  return context.title(\"a\").view({ products, \"page-title\": \"a\", count: products.length, more: () => 1 });\n});\n",
		routerImport + "const shelf = { view: () => 1 };\nrouter.get((context) => context.json({ n: shelf.view() }));\n",
		// view read off anything but the context is data: a field of a body, a row's column, a setting
		routerImport + "router.post((context) => {\n  const body = context.request.json();\n  return context.json({ mode: body.view });\n});\n",
		routerImport + "const settings = { view: \"grid\" };\nconst { view } = settings;\nrouter.get((context) => context.json({ mode: settings.view, same: view === settings[\"view\"] }));\n",
		routerImport + "const rows = [{ view: 3, title: \"a\" }];\nrouter.get((context) => {\n  let total = 0;\n  for (const row of rows) {\n    total = total + row.view;\n  }\n  const { view, title } = rows[0];\n  return context.json({ total, view, title, views: rows.map(({ view }) => view) });\n});\n",
		// a parameter of the same name in a callback is the callback's own
		routerImport + "const items = [{ view: 1 }];\nrouter.get((context) => context.json(items.map((context) => context.view)));\n",
		// a function the file never hands the context to is not read for it
		routerImport + "const label = (item) => item.view;\nrouter.get((context) => context.json({ n: label({ view: 2 }) }));\n",
		// a call through a name the handler declares is not the file's function of that name: its own const, in a
		// function written in place, a parameter; nor through a name the file assigns again
		routerImport + "const draw = (c) => {\n  const { view } = c;\n  return view({ a: 1 });\n};\nrouter.get((context) => {\n  const draw = (c) => c.request.method;\n  return context.text(draw(context));\n});\n",
		routerImport + "const draw = (c) => {\n  const { view } = c;\n  return view({ a: 1 });\n};\nrouter.get((context) => {\n  const draw = (c) => c.request.method;\n  return context.text(((c) => draw(c))(context));\n});\n",
		routerImport + "const draw = (c) => {\n  const { view } = c;\n  return view({ a: 1 });\n};\nconst run = (context, draw) => draw(context);\nrouter.get((context) => context.text(run(context, (c) => \"x\")));\n",
		routerImport + "let draw = (c) => {\n  const { view } = c;\n  return view({ a: 1 });\n};\ndraw = (c) => \"x\";\nrouter.get((context) => context.text(draw(context)));\n",
	})
	if _, err := compileRouterApp(t, map[string]string{
		".shared/page.work":    "export const page = (context, data) => context.view(data);\n",
		"shop.com/router.work": routerImport + "import { page } from \"app/.shared/page.work\";\nrouter.get((context) => page(context, { a: 1 }));\n",
	}, "shop.com/router.work"); err != nil {
		t.Errorf("a page of .shared/ handed a variable: %v, want it to compile (the rule is router.work's)", err)
	}
	expectRouterCompiles(t, "shop.com/router.kitwork.js", []string{"import { router } from \"kitwork\";\nconst data = { a: 1 };\nrouter.get((ctx) => ctx.view(data));\n"})

	// config.work is the other half of the folder's scope (KITWORK_ROUTER.md mục 4): a .view(…) in it hands an
	// object written in place too, and view is never taken apart from the context (its guards draw no page at
	// all, TestGuardDrawsNoPage)
	for _, relative := range []string{"shop.com/config.work", "shop.com/a/config.work"} {
		expectRouterErrors(t, relative, []routerRow{
			{"a function's .view(data)", routerImport + "const data = { login: true };\nconst draw = (page) => page.view(data);\nrouter.guard((context) => draw !== null);\n", "data);", 0,
				ContextViewObject("page.view(data)")},
			{"a guard's (context.view)(…)", routerImport + "router.guard((context) => (context.view)({ login: true }));\n", "view)", 0,
				ContextViewApart("context.view")},
			{"a guard by name, ({ view })", routerImport + "const gate = ({ view }) => view({ login: true });\nrouter.guard(gate);\n", "view }", 0,
				ContextViewApart("{ view }")},
		})
		expectRouterCompiles(t, relative, []string{
			routerImport + "router.guard((context) => context.redirect(\"/login\"));\n",
			routerImport + "const settings = { view: \"grid\" };\nrouter.guard((context) => settings.view === \"grid\");\n",
		})
	}
}

// The vocabulary is one table the engine reads too (router_words.go): its kinds, how a folder call meets its
// parents', and where each is written, as KITWORK_ROUTER.md mục 3 and the table of mục 4 say.
//
// CONTROL (24/10/2026, fix of the review: -overlay with bundle at site and branch, rss at site and branch, proxy
// at the site only): "router.proxy takes levels 010, want 110", "router.rss takes levels 110, want 010",
// "router.bundle takes levels 110, want 111".
func TestRouterVocabularyTable(t *testing.T) {
	if got := strings.Join(RouterWords(RouterDoor), " "); got != "get view post put patch delete proxy" {
		t.Errorf("doors: %s", got)
	}
	if got := strings.Join(RouterWords(RouterBuiltIn), " "); got != "sitemap rss robots llms llmsFull manifest" {
		t.Errorf("built-in doors: %s", got)
	}
	if got := strings.Join(RouterWords(RouterRouteRule), " "); got != "guard limit cache persist" {
		t.Errorf("route rules: %s", got)
	}
	accumulate := "guard limit jsonld colors fonts gradients highlight meta assets"
	nearest := "timezone language appearance contrast transition bundle favicon cors reload"
	for _, word := range strings.Fields(accumulate) {
		if known, ok := RouterWordOf(word); !ok || known.Kind != RouterFolderCall || known.Inheritance != RouterAccumulates {
			t.Errorf("router.%s: %+v, want a folder call that accumulates", word, known)
		}
	}
	for _, word := range strings.Fields(nearest) {
		if known, ok := RouterWordOf(word); !ok || known.Kind != RouterFolderCall || known.Inheritance != RouterNearestWins {
			t.Errorf("router.%s: %+v, want a folder call whose nearest folder wins", word, known)
		}
	}
	if got := len(RouterWords(RouterFolderCall)); got != 18 {
		t.Errorf("%d folder calls, want the 18 of the table", got)
	}
	// the level of every word, as the brief of 24/10/2026 and the table of mục 4 say: the engine checks them
	// as it runs the folders, so none may drift
	const app, site, branch = RouterApp, RouterSite, RouterBranch
	levels := map[string]RouterLevel{
		"get": site | branch, "view": site | branch, "post": site | branch, "put": site | branch, "patch": site | branch, "delete": site | branch, "proxy": site | branch,
		"sitemap": site, "rss": site, "robots": site, "llms": site, "llmsFull": site, "manifest": site,
		"timezone": app, "reload": site,
		"meta": site | branch, "jsonld": site | branch, "guard": site | branch, "limit": site | branch, "cors": site | branch, "assets": site | branch,
		"language": app | site | branch, "colors": app | site | branch, "fonts": app | site | branch, "gradients": app | site | branch,
		"appearance": app | site | branch, "contrast": app | site | branch, "transition": app | site | branch, "highlight": app | site | branch,
		"bundle": app | site | branch, "favicon": app | site | branch,
	}
	words := append(append(RouterWords(RouterDoor), RouterWords(RouterBuiltIn)...), RouterWords(RouterFolderCall)...)
	if len(words) != len(levels) {
		t.Errorf("%d words of the router, want the %d of the table", len(words), len(levels))
	}
	for _, word := range words {
		want, ok := levels[word]
		if !ok {
			t.Errorf("router.%s is no word of the table", word)
			continue
		}
		if known, _ := RouterWordOf(word); known.Levels != want {
			t.Errorf("router.%s takes levels %03b, want %03b", word, known.Levels, want)
		}
	}
	for _, rule := range []string{"guard", "limit"} {
		if known, _ := RouteRuleOf(rule); !known.Repeats {
			t.Errorf(".%s repeats on a route", rule)
		}
	}
	for _, rule := range []string{"cache", "persist"} {
		if known, _ := RouteRuleOf(rule); known.Repeats {
			t.Errorf(".%s is once per route", rule)
		}
	}
	for written, want := range map[string]string{"colours": "colors", "GET": "get", "Reload": "reload", "jsonLd": "jsonld", "faviconn": "favicon", "zz": ""} {
		if got := RouterNearestWord(written, append(RouterWords(RouterDoor), RouterWords(RouterFolderCall)...)); got != want {
			t.Errorf("nearest of %s = %q, want %q", written, got, want)
		}
	}
}
