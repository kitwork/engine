package compiler

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// A guard draws no page (25/10/2026, KITWORK_ROUTER.md mục 4): context.view in what a guard of a router.work or a
// router config.work runs is a compile error at the view, whatever the guard is called and however it reaches it
// (a route's guard, a branch's, one of an array, a top-level const handed by name, a function the guard hands the
// context to, the context under another name, past a modifier, its response), and in a file the guard is
// imported from, said where it stands and told from the guard. A redirect, a status with json, false, and a view
// of the handler stay; so does a template site's router.kitwork.js, and a guard the compiler cannot follow (the
// engine refuses it as it runs, work/router_guard_view_test.go).
//
// CONTROL (25/10/2026, -overlay with readRouterFile not calling reader.guardViews): all 41 error rows of the first
// draft went red with "got <nil>" (13 of router.work, 2 of each config.work, 8 imported rows in each of the three
// files: "a route's guard: got <nil>", "shop.com/a/config.work, re-exported: got <nil>"), and every row that
// compiles stayed green. With function giving nil for an imported name, the 24 imported rows went red with "got
// <nil>" and the other 17 stayed green. After the review (each through a copy of router_guard.go): with no alias
// read (const gate = draw), the 13 rows of a name went red ("a name bound to a function", "a default export of a
// name" in each file); with outer not checked in hand, the 7 rows of a guard's own name went red at the page's view
// ("a guard's const named as an imported page: ../.shared/page.work:1:42: context.view(…) trong guard"); with no nil
// check of a parameter, the test panicked ("invalid memory address or nil pointer dereference"); with the seen set
// off, "fatal error: stack overflow"; with resolve's visited set off, the cycle row never returned ("test timed out
// after 1m0s"); with resolve bound to 16 names, "a chain of seventeen files: got <nil>". After the second review
// (through a copy of router_rules.go or router_guard.go): with a block's names not its own (block := declared),
// "a const of another block: got <nil>"; with a function's parameters added to the names around it,
// "a callback's parameter beside the call: got <nil>"; with reassigned names still followed, the five rows of a
// name assigned again went red at the first value's view ("a library's let it assigns again:
// ../.shared/session.work:1:34: context.view(…) trong guard").
func TestGuardDrawsNoPage(t *testing.T) {
	expectRouterErrors(t, "shop.com/router.work", []routerRow{
		{"a route's guard", routerImport + "router.get((context) => context.text(\"x\")).guard((context) => context.view({ login: true }));\n", "view({ login", 0,
			ContextViewInGuard("context.view(…)")},
		{"a route's second guard", routerImport + "router.get((context) => context.text(\"x\")).guard((context) => true).guard((context) => context.view());\n", "view()", 0,
			ContextViewInGuard("context.view(…)")},
		{"a branch's guard by name", routerImport + "const gate = (context) => context.view({ login: true });\nrouter.guard(gate);\nrouter.get((context) => context.text(\"x\"));\n", "view({", 0,
			ContextViewInGuard("context.view(…)")},
		{"one guard of an array", routerImport + "router.guard([(context) => true, (ctx) => ctx.view({ login: true })]);\nrouter.get((context) => context.text(\"x\"));\n", "view({", 0,
			ContextViewInGuard("ctx.view(…)")},
		{"past a modifier", routerImport + "router.get((context) => context.text(\"x\")).guard((context) => context.title(\"a\").view({ a: 1 }));\n", "view({", 0,
			ContextViewInGuard("….view(…)")},
		{"its response taken apart", routerImport + "router.get((context) => context.text(\"x\")).guard(({ response }) => response.view({ a: 1 }));\n", "view({", 0,
			ContextViewInGuard("response.view(…)")},
		{"context.response", routerImport + "router.get((context) => context.text(\"x\")).guard((context) => context.response.view({ a: 1 }));\n", "view({", 0,
			ContextViewInGuard("….view(…)")},
		{"a name bound to it", routerImport + "router.guard((context) => {\n  const c = context;\n  return c.view({ a: 1 });\n});\nrouter.get((context) => context.text(\"x\"));\n", "view({", 0,
			ContextViewInGuard("c.view(…)")},
		{"in a callback", routerImport + "router.guard((context) => [1].map(() => context.view({ a: 1 })).length > 0);\nrouter.get((context) => context.text(\"x\"));\n", "view({", 0,
			ContextViewInGuard("context.view(…)")},
		{"after a check", routerImport + "router.guard((context) => context.request.method === \"GET\" || context.view({ a: 1 }));\nrouter.get((context) => context.text(\"x\"));\n", "view({", 0,
			ContextViewInGuard("context.view(…)")},
		{"returning nothing", routerImport + "router.guard((context) => {\n  context.view({ a: 1 });\n});\nrouter.get((context) => context.text(\"x\"));\n", "view({", 0,
			ContextViewInGuard("context.view(…)")},
		{"a function the guard hands the context to", routerImport + "const draw = (page, context) => context.view({ page });\nrouter.guard((context) => draw(1, context));\nrouter.get((context) => context.text(\"x\"));\n", "view({", 0,
			ContextViewInGuard("context.view(…)")},
		{"a function a handler shares with a guard", routerImport + "const page = (context) => context.view({ a: 1 });\nrouter.get(page).guard(page);\n", "view({", 0,
			ContextViewInGuard("context.view(…)")},
		{"a name bound to a function", routerImport + "const draw = (context) => context.view({ a: 1 });\nconst gate = draw;\nrouter.guard(gate);\nrouter.get((context) => context.text(\"x\"));\n", "view({", 0,
			ContextViewInGuard("context.view(…)")},
		{"a function that calls itself", routerImport + "const check = (context, depth) => depth > 3 ? context.view({ a: 1 }) : check(context, depth + 1);\nrouter.guard((context) => check(context, 0));\nrouter.get((context) => context.text(\"x\"));\n", "view({", 0,
			ContextViewInGuard("context.view(…)")},
		// a name declared in a block that does not hold the call, or by a callback beside it, is not the call's
		{"a const of another block", routerImport + "const draw = (context) => context.view({ a: 1 });\nrouter.guard((context) => {\n  if (context.request.method === \"HEAD\") {\n    const draw = 1;\n  }\n  return draw(context);\n});\nrouter.get((context) => context.text(\"x\"));\n", "view({", 0,
			ContextViewInGuard("context.view(…)")},
		{"a callback's parameter beside the call", routerImport + "const draw = (context) => context.view({ a: 1 });\nrouter.guard((context) => {\n  const names = [\"a\"].map((draw) => draw);\n  return draw(context);\n});\nrouter.get((context) => context.text(\"x\"));\n", "view({", 0,
			ContextViewInGuard("context.view(…)")},
		{"a let the program never assigns", routerImport + "let gate = (context) => context.view({ a: 1 });\nrouter.guard(gate);\nrouter.get((context) => context.text(\"x\"));\n", "view({", 0,
			ContextViewInGuard("context.view(…)")},
	})
	expectRouterCompiles(t, "shop.com/router.work", []string{
		// a guard lets the request on, refuses it, or answers without a page; the handler draws it
		routerImport + "router.get((context) => context.view({ a: 1 })).guard((context) => context.request.method === \"GET\");\n",
		routerImport + "router.guard((context) => context.request.method === \"GET\" || context.redirect(\"/login\"));\nrouter.get((context) => context.view());\n",
		routerImport + "router.post((context) => context.json({ ok: true })).guard((context) => context.status(401).json({ error: \"login\" }));\n",
		routerImport + "router.guard((context) => false);\nrouter.get((context) => context.view());\n",
		// a function of the file that draws the page, handed to no guard
		routerImport + "const page = (context) => context.view({ a: 1 });\nconst gate = (context) => page !== null;\nrouter.get(page).guard(gate);\n",
		// view read off anything but the guard's context
		routerImport + "router.guard((context) => [{ view: () => 1 }].map((context) => context.view()).length > 0);\nrouter.get((context) => context.text(\"x\"));\n",
		routerImport + "const settings = { view: () => 1 };\nrouter.guard((context) => settings.view() === 1);\nrouter.get((context) => context.text(\"x\"));\n",
		// a function that calls itself, two that call each other: each is read once
		routerImport + "const check = (context, depth) => depth > 3 || check(context, depth + 1);\nrouter.guard((context) => check(context, 0));\nrouter.get((context) => context.text(\"x\"));\n",
		routerImport + "const a = (context) => b(context);\nconst b = (context) => a(context);\nrouter.guard(a);\nrouter.get((context) => context.text(\"x\"));\n",
		// a name the guard declares itself is its own, not the file's function of that name: a const, a callback's
		// parameter, a loop's name, a function's parameter, and in a function written in place
		routerImport + "const draw = (context) => context.view({ a: 1 });\nrouter.get(draw);\nrouter.guard((context) => {\n  const draw = (c) => c.request.method === \"GET\";\n  return draw(context);\n});\n",
		routerImport + "const draw = (context) => context.view({ a: 1 });\nrouter.get(draw);\nrouter.guard((context) => [(c) => true].map((draw) => draw(context)).length > 0);\n",
		routerImport + "const draw = (context) => context.view({ a: 1 });\nrouter.get(draw);\nrouter.guard((context) => {\n  let open = false;\n  for (const draw of [(c) => true]) {\n    open = draw(context);\n  }\n  return open;\n});\n",
		routerImport + "const draw = (context) => context.view({ a: 1 });\nrouter.get(draw);\nconst run = (context, draw) => draw(context);\nrouter.guard((context) => run(context, (c) => true));\n",
		routerImport + "const draw = (context) => context.view({ a: 1 });\nrouter.get(draw);\nrouter.guard((context) => {\n  const draw = (c) => true;\n  return ((c) => draw(c))(context);\n});\n",
		// a name the program assigns is not followed: its first value is not surely the one the router gets
		routerImport + "let gate = (context) => context.view({ a: 1 });\ngate = (context) => true;\nrouter.guard(gate);\nrouter.get((context) => context.text(\"x\"));\n",
		routerImport + "function gate(context) {\n  return context.view({ a: 1 });\n}\ngate = (context) => true;\nrouter.guard(gate);\nrouter.get((context) => context.text(\"x\"));\n",
		routerImport + "const draw = (context) => context.view({ a: 1 });\nconst open = (context) => true;\nlet gate = draw;\ngate = open;\nrouter.guard(gate);\nrouter.get((context) => context.text(\"x\"));\n",
		routerImport + "let draw = (context) => context.view({ a: 1 });\nrouter.get((context) => draw(context));\nrouter.guard((context) => {\n  draw = (c) => true;\n  return draw(context);\n});\n",
		// a guard a call makes: the compiler does not follow it, the engine refuses its view as it runs
		routerImport + "const make = () => (context) => context.view({ a: 1 });\nrouter.guard(make());\nrouter.get((context) => context.text(\"x\"));\n",
	})
	expectRouterCompiles(t, "shop.com/router.kitwork.js", []string{
		"import { router } from \"kitwork\";\nrouter.get((ctx) => ctx.text(\"x\")).guard((ctx) => ctx.view());\n",
	})

	// config.work: its guards are the branch's
	for _, relative := range []string{"shop.com/config.work", "shop.com/a/config.work"} {
		expectRouterErrors(t, relative, []routerRow{
			{"a branch's guard", routerImport + "router.guard((context) => context.view({ login: true }));\n", "view({", 0,
				ContextViewInGuard("context.view(…)")},
			{"a branch's guard by name", routerImport + "const gate = (ctx) => ctx.view();\nrouter.guard(gate);\n", "view()", 0,
				ContextViewInGuard("ctx.view(…)")},
		})
		expectRouterCompiles(t, relative, []string{
			routerImport + "router.guard((context) => context.status(401).json({ error: \"login\" }));\n",
		})
	}

	// a guard imported from another file of the app: the error stands in that file, told from the guard
	session := "export const authenticated = (context) => context.view({ login: true });\n"
	type importedRow struct {
		name    string
		files   map[string]string
		source  string
		anchor  string
		path    string
		marker  string
		written string
	}
	imported := []importedRow{
		{"imported by name", map[string]string{".shared/session.work": session},
			routerImport + "import { authenticated } from \"app/.shared/session.work\";\nrouter.guard(authenticated);\nrouter.get((context) => context.text(\"x\"));\n",
			"authenticated);", ".shared/session.work", "view({", "context.view(…)"},
		{"imported under another name", map[string]string{".shared/session.work": session},
			routerImport + "import { authenticated as signedIn } from \"../.shared/session.work\";\nrouter.get((context) => context.text(\"x\")).guard(signedIn);\n",
			"signedIn);", ".shared/session.work", "view({", "context.view(…)"},
		{"exported under another name", map[string]string{".shared/session.work": "const check = (ctx) => ctx.view({ login: true });\nexport { check as authenticated };\n"},
			routerImport + "import { authenticated } from \"app/.shared/session.work\";\nrouter.guard(authenticated);\nrouter.get((context) => context.text(\"x\"));\n",
			"authenticated);", ".shared/session.work", "view({", "ctx.view(…)"},
		{"re-exported", map[string]string{".shared/session.work": session, ".shared/auth.work": "import { authenticated } from \"./session.work\";\nexport { authenticated };\n"},
			routerImport + "import { authenticated } from \"app/.shared/auth.work\";\nrouter.guard(authenticated);\nrouter.get((context) => context.text(\"x\"));\n",
			"authenticated);", ".shared/session.work", "view({", "context.view(…)"},
		{"a default export", map[string]string{".shared/gate.work": "export default (context) => context.view({ login: true });\n"},
			routerImport + "import gate from \"app/.shared/gate.work\";\nrouter.guard(gate);\nrouter.get((context) => context.text(\"x\"));\n",
			"gate);", ".shared/gate.work", "view({", "context.view(…)"},
		{"a function of the file the guard hands the context to", map[string]string{".shared/session.work": "const draw = (context) => context.view({ login: true });\nexport const authenticated = (context) => draw(context);\n"},
			routerImport + "import { authenticated } from \"app/.shared/session.work\";\nrouter.guard(authenticated);\nrouter.get((context) => context.text(\"x\"));\n",
			"authenticated);", ".shared/session.work", "view({", "context.view(…)"},
		{"a function of a third file", map[string]string{
			".shared/session.work": "import { draw } from \"./page.work\";\nexport const authenticated = (context) => draw(context);\n",
			".shared/page.work":    "export const draw = (context) => context.view({ login: true });\n"},
			routerImport + "import { authenticated } from \"app/.shared/session.work\";\nrouter.guard(authenticated);\nrouter.get((context) => context.text(\"x\"));\n",
			"authenticated);", ".shared/page.work", "view({", "context.view(…)"},
		{"a default export of a name", map[string]string{".shared/gate.work": "const gate = (context) => context.view({ login: true });\nexport default gate;\n"},
			routerImport + "import gate from \"app/.shared/gate.work\";\nrouter.guard(gate);\nrouter.get((context) => context.text(\"x\"));\n",
			"gate);", ".shared/gate.work", "view({", "context.view(…)"},
		{"a default export of an imported name", map[string]string{
			".shared/gate.work": "import { draw } from \"./page.work\";\nexport default draw;\n",
			".shared/page.work": "export const draw = (context) => context.view({ login: true });\n"},
			routerImport + "import gate from \"app/.shared/gate.work\";\nrouter.guard(gate);\nrouter.get((context) => context.text(\"x\"));\n",
			"gate);", ".shared/page.work", "view({", "context.view(…)"},
		{"an export of another name", map[string]string{".shared/session.work": "const check = (context) => context.view({ login: true });\nexport const authenticated = check;\n"},
			routerImport + "import { authenticated } from \"app/.shared/session.work\";\nrouter.guard(authenticated);\nrouter.get((context) => context.text(\"x\"));\n",
			"authenticated);", ".shared/session.work", "view({", "context.view(…)"},
		{"a name bound to an imported guard", map[string]string{".shared/session.work": session},
			routerImport + "import { authenticated } from \"app/.shared/session.work\";\nconst gate = authenticated;\nrouter.guard(gate);\nrouter.get((context) => context.text(\"x\"));\n",
			"gate);", ".shared/session.work", "view({", "context.view(…)"},
		{"a guard of the file handing the context to an imported function", map[string]string{".shared/page.work": "export const draw = (context) => context.view({ login: true });\n"},
			routerImport + "import { draw } from \"app/.shared/page.work\";\nrouter.guard((context) => draw(context));\nrouter.get((context) => context.text(\"x\"));\n",
			"guard((context) => draw", ".shared/page.work", "view({", "context.view(…)"},
	}
	for _, relative := range []string{"shop.com/router.work", "shop.com/config.work", "shop.com/a/config.work"} {
		up := strings.Repeat("../", strings.Count(relative, "/"))
		for _, row := range imported {
			source := strings.ReplaceAll(row.source, "\"../.shared/", "\""+up+".shared/")
			if filepath.Base(relative) == RouterConfigFile {
				// config.work has no door
				source = strings.Replace(source, "router.get((context) => context.text(\"x\")).guard(", "router.guard(", 1)
				source = strings.Replace(source, "router.get((context) => context.text(\"x\"));\n", "", 1)
			}
			files := map[string]string{relative: source}
			for path, content := range row.files {
				files[path] = content
			}
			_, err := compileRouterApp(t, files, relative)
			want := placeIn(up+row.path, row.files[row.path], row.marker, 0) + ": " + ContextViewInGuard(row.written) +
				" (guard ở " + placeIn(filepath.Base(relative), source, row.anchor, 0) + ")"
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("%s, %s:\n  got  %v\n  want %s", relative, row.name, err, want)
			}
		}
	}
	// a file the guard comes from may draw a page in a function no guard runs, and a guard's own name for a
	// function is its own, in the router file and in the library
	for name, files := range map[string]map[string]string{
		"a guard and a page of one file": {
			".shared/session.work": "export const authenticated = (context) => context.request.method === \"GET\" || context.redirect(\"/login\");\nexport const page = (context) => context.view({ a: 1 });\n",
			"shop.com/router.work": routerImport + "import { authenticated, page } from \"app/.shared/session.work\";\nrouter.guard(authenticated);\nrouter.get(page);\n",
		},
		"a guard's const named as an imported page": {
			".shared/page.work":    "export const draw = (context) => context.view({ a: 1 });\n",
			"shop.com/router.work": routerImport + "import { draw } from \"app/.shared/page.work\";\nrouter.get(draw);\nrouter.guard((context) => {\n  const draw = (c) => true;\n  return draw(context);\n});\n",
		},
		"a library's let it assigns again": {
			".shared/session.work": "let check = (context) => context.view({ login: true });\ncheck = (context) => true;\nexport { check };\n",
			"shop.com/router.work": routerImport + "import { check } from \"app/.shared/session.work\";\nrouter.guard(check);\nrouter.get((context) => context.text(\"x\"));\n",
		},
		"a library guard's const named as a page of its file": {
			".shared/session.work": "const draw = (context) => context.view({ a: 1 });\nexport const page = draw;\nexport const gate = (context) => {\n  const draw = (c) => true;\n  return draw(context);\n};\n",
			"shop.com/router.work": routerImport + "import { gate, page } from \"app/.shared/session.work\";\nrouter.guard(gate);\nrouter.get(page);\n",
		},
	} {
		if _, err := compileRouterApp(t, files, "shop.com/router.work"); err != nil {
			t.Errorf("%s: %v, want it to compile", name, err)
		}
	}

	// a function whose parameter the parser cannot read is the compiler's error, never a crash of the reader
	for name, files := range map[string]map[string]string{
		"in the router file": {"shop.com/router.work": routerImport + "function gate(a.b) {\n  return true;\n}\nrouter.guard(gate);\nrouter.get((context) => context.text(\"x\"));\n"},
		"in a library": {
			".shared/gate.work":    "export function gate(context.request) {\n  return true;\n}\n",
			"shop.com/router.work": routerImport + "import { gate } from \"app/.shared/gate.work\";\nrouter.guard(gate);\nrouter.get((context) => context.text(\"x\"));\n",
		},
	} {
		if _, err := compileRouterApp(t, files, "shop.com/router.work"); err == nil || !strings.Contains(err.Error(), "parameter 1 is invalid") {
			t.Errorf("a parameter the parser cannot read, %s: %v, want the compiler's \"parameter 1 is invalid\"", name, err)
		}
	}

	// files that export a guard on to each other end the search: the bundler says import cycle
	if _, err := compileRouterApp(t, map[string]string{
		".shared/a.work":       "import { gate } from \"./b.work\";\nexport { gate };\n",
		".shared/b.work":       "import { gate } from \"./a.work\";\nexport { gate };\n",
		"shop.com/router.work": routerImport + "import { gate } from \"app/.shared/a.work\";\nrouter.guard(gate);\nrouter.get((context) => context.text(\"x\"));\n",
	}, "shop.com/router.work"); err == nil || !strings.Contains(err.Error(), "import cycle") {
		t.Errorf("a cycle of re-exports: %v, want the bundler's import cycle", err)
	}
	// and a chain of files that export it on is followed however long it is
	chain := map[string]string{".shared/f0.work": session}
	for index := 1; index <= 17; index++ {
		chain[".shared/f"+strconv.Itoa(index)+".work"] = "import { authenticated } from \"./f" + strconv.Itoa(index-1) + ".work\";\nexport { authenticated };\n"
	}
	chained := routerImport + "import { authenticated } from \"app/.shared/f17.work\";\nrouter.guard(authenticated);\nrouter.get((context) => context.text(\"x\"));\n"
	chain["shop.com/router.work"] = chained
	want := placeIn("../.shared/f0.work", session, "view({", 0) + ": " + ContextViewInGuard("context.view(…)") +
		" (guard ở " + placeIn("router.work", chained, "authenticated);", 0) + ")"
	if _, err := compileRouterApp(t, chain, "shop.com/router.work"); err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("a chain of seventeen files:\n  got  %v\n  want %s", err, want)
	}
}
