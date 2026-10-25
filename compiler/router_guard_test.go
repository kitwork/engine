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

// A guard answers only so (25/10/2026, KITWORK_ROUTER.md mục 4): it returns nothing, true or false, or answers with
// context.redirect(…) or context.status(…).json(…). Any other answer of the context in what it runs (html, text,
// svg, css, file, notFound, notfound, error, a template response's send…, and json with no status before it in its
// own chain), under any name of the context, in any case, and in a function it hands the context to, is a compile
// error at the answer. A status outside the json's chain (on a line of its own, bound to a name, written by a
// function the guard calls, in an argument) is no compile-time status: whether it runs before the json is what only
// the run tells, and the engine takes it then (work). A value the guard function itself returns as written (a
// string, a number, a template, an object, an array, a function, arithmetic, typeof, also behind ?:, ||, &&, ??) is
// an error at the value. A function written in the guard returns to it, not to the router; a name or a call it
// returns is read as it runs.
//
// CONTROL (25/10/2026, through a copy of router_guard.go or router_rules.go): with guardRefusedAnswers empty, the 17
// rows of html, text, svg, css, file, notFound, notfound, error, send, in router.work, both config.work and imported,
// compiled ("text: got <nil>"); with a json taken whatever its chain, the 15 rows of json compiled ("json with no
// status: got <nil>"); with returns reading nothing, the 26 rows of a value compiled ("a string: got <nil>"); with
// the returns read after the parameter and the seen check, the five rows of a guard with no parameter and of one first
// read as a helper compiled; with returnedKind reading only a bare literal, the eight rows of a ternary, ||, &&, ??, a
// sign, a product, typeof and + compiled; with a helper's returns read as a guard's, the role helper's "admin" went
// red; with the method's name not folded, the four rows in capitals (notFound among them) compiled; with res not the
// response, "text of context.res: got <nil>".
func TestGuardAnswersOnlySo(t *testing.T) {
	const handler = "router.get((context) => context.text(\"x\"));\n"
	answer := func(written string) string { return ContextAnswerInGuard(written) }
	expectRouterErrors(t, "shop.com/router.work", []routerRow{
		{"html", routerImport + "router.guard((context) => context.html(\"<h1>Đăng nhập</h1>\"));\n" + handler, "html(", 0, answer("context.html(…)")},
		{"text", routerImport + "router.guard((context) => context.text(\"đóng\"));\n" + handler, "text(\"đóng", 0, answer("context.text(…)")},
		{"text after a status", routerImport + "router.guard((context) => context.status(403).text(\"đóng\"));\n" + handler, "text(\"đóng", 0, answer("….text(…)")},
		{"svg", routerImport + "router.guard((context) => context.svg(\"<svg></svg>\"));\n" + handler, "svg(", 0, answer("context.svg(…)")},
		{"file", routerImport + "router.guard((context) => context.file(\"app/shop.com/assets/a.pdf\"));\n" + handler, "file(", 0, answer("context.file(…)")},
		{"notFound", routerImport + "router.guard((context) => context.notFound());\n" + handler, "notFound(", 0, answer("context.notFound(…)")},
		{"error", routerImport + "router.guard((context) => context.error(\"cấm\"));\n" + handler, "error(", 0, answer("context.error(…)")},
		{"json with no status", routerImport + "router.guard((context) => context.json({ error: \"login\" }));\n" + handler, "json(", 0, answer("context.json(…) không có status trong chuỗi")},
		{"json after a header, no status", routerImport + "router.guard((context) => context.header(\"x-a\", \"b\").json({ error: \"login\" }));\n" + handler, "json(", 0, answer("….json(…) không có status trong chuỗi")},
		{"json of the response taken apart", routerImport + "router.guard(({ response }) => response.json({ error: \"login\" }));\n" + handler, "json(", 0, answer("response.json(…) không có status trong chuỗi")},
		{"text of a route's guard, as a statement", routerImport + "router.get((context) => context.text(\"x\")).guard((context) => {\n  context.status(403).text(\"đóng\");\n});\n", "text(\"đóng", 0, answer("….text(…)")},
		{"text in a function the guard hands the context to", routerImport + "const deny = (context) => context.status(403).text(\"đóng\");\nrouter.guard((context) => deny(context));\n" + handler, "text(\"đóng", 0, answer("….text(…)")},
		// a value the guard returns as written
		{"a string", routerImport + "router.guard((context) => \"đóng\");\n" + handler, "\"đóng\"", 0, GuardReturnsValue("một chuỗi")},
		{"a number", routerImport + "router.guard((context) => 403);\n" + handler, "403)", 0, GuardReturnsValue("một số")},
		{"a template", routerImport + "router.guard((context) => `đóng`);\n" + handler, "`đóng`", 0, GuardReturnsValue("một chuỗi")},
		{"an object", routerImport + "router.guard((context) => ({ error: \"login\" }));\n" + handler, "{ error", 0, GuardReturnsValue("một object")},
		{"an array", routerImport + "router.guard((context) => [1]);\n" + handler, "[1]", 0, GuardReturnsValue("một mảng")},
		{"a function", routerImport + "router.guard((context) => (c) => true);\n" + handler, "=> true", 0, GuardReturnsValue("một hàm")},
		{"a string in a branch", routerImport + "router.guard((context) => {\n  if (context.request.method === \"POST\") {\n    return \"đóng\";\n  }\n});\n" + handler, "\"đóng\"", 0, GuardReturnsValue("một chuỗi")},
		{"a string of a guard by name", routerImport + "const gate = (context) => \"đóng\";\nrouter.guard(gate);\n" + handler, "\"đóng\"", 0, GuardReturnsValue("một chuỗi")},
		// css, the lowercase notfound, a template's response word
		{"css", routerImport + "router.guard((context) => context.css(\"body{}\"));\n" + handler, "css(", 0, answer("context.css(…)")},
		{"notfound", routerImport + "router.guard((context) => context.notfound());\n" + handler, "notfound(", 0, answer("context.notfound(…)")},
		{"send of the response", routerImport + "router.guard((context) => context.response.send(\"x\"));\n" + handler, "send(", 0, answer("….send(…)")},
		// a json with no status before it, in a function the guard hands the context to, whichever guard comes first
		{"json of a helper with no status", routerImport + "const unauthorized = (context) => context.json({ error: \"login\" });\nrouter.guard((context) => {\n  context.status(401);\n  return unauthorized(context);\n});\nrouter.guard((context) => unauthorized(context));\n" + handler, "json(", 0,
			answer("context.json(…) không có status trong chuỗi")},
		// a status chained into one answer is that answer's, and one written in a block counts in that block only
		{"json after an answer's status", routerImport + "router.guard((context) => {\n  if (context.query(\"a\") === \"1\") {\n    return context.status(401).json({ error: \"login\" });\n  }\n  return context.json({ error: \"forbidden\" });\n});\n" + handler, "json({ error: \"forbidden\"", 0,
			answer("context.json(…) không có status trong chuỗi")},
		{"json beside an answer's status in a ternary", routerImport + "router.guard((context) => context.query(\"a\") === \"1\" ? context.status(401).json({ a: 1 }) : context.json({ b: 1 }));\n" + handler, "json({ b", 0,
			answer("context.json(…) không có status trong chuỗi")},
		{"json after a redirect's status", routerImport + "router.guard((context) => {\n  if (context.query(\"a\") === \"1\") {\n    return context.status(302).redirect(\"/login\");\n  }\n  return context.json({ b: 1 });\n});\n" + handler, "json({ b", 0,
			answer("context.json(…) không có status trong chuỗi")},
		{"a helper after an answer's status", routerImport + "const forbid = (c) => c.json({ b: 1 });\nrouter.guard((context) => {\n  if (context.query(\"a\") === \"1\") {\n    return context.status(401).json({ a: 1 });\n  }\n  return forbid(context);\n});\n" + handler, "json({ b", 0,
			answer("c.json(…) không có status trong chuỗi")},
		{"json after a status of another block", routerImport + "router.guard((context) => {\n  if (context.query(\"a\") === \"1\") {\n    context.status(401);\n  }\n  return context.json({ b: 1 });\n});\n" + handler, "json({ b", 0,
			answer("context.json(…) không có status trong chuỗi")},
		// the status outside the json's chain is no compile-time status, wherever it stands: on a line of its own, bound
		// to a name, written by a function the guard calls, in an argument (only the run tells whether it runs first)
		{"json after a status of its own line", routerImport + "router.guard((context) => {\n  context.status(401);\n  return context.json({ b: 1 });\n});\n" + handler, "json({ b", 0,
			answer("context.json(…) không có status trong chuỗi")},
		{"json of a name bound to a status", routerImport + "router.guard((context) => {\n  const answer = context.status(401);\n  return answer.json({ b: 1 });\n});\n" + handler, "json({ b", 0,
			answer("answer.json(…) không có status trong chuỗi")},
		{"json after a helper's status", routerImport + "const unauthorized = (c) => {\n  c.status(401);\n};\nrouter.guard((context) => {\n  unauthorized(context);\n  return context.json({ b: 1 });\n});\n" + handler, "json({ b", 0,
			answer("context.json(…) không có status trong chuỗi")},
		{"json of a helper handed a status", routerImport + "const deny = (c) => c.json({ b: 1 });\nrouter.guard((context) => deny(context.status(401)));\n" + handler, "json({ b", 0,
			answer("c.json(…) không có status trong chuỗi")},
		{"json of a closure before a status", routerImport + "router.guard((context) => {\n  const refuse = () => context.json({ b: 1 });\n  context.status(401);\n  return refuse();\n});\n" + handler, "json({ b", 0,
			answer("context.json(…) không có status trong chuỗi")},
		// a name of the context in any case (the VM reads it so), and res, the response's other name
		{"Text in capitals", routerImport + "router.guard((context) => context.Text(\"đóng\"));\n" + handler, "Text(", 0, answer("context.Text(…)")},
		{"JSON with no status, in capitals", routerImport + "router.guard((context) => context.JSON({ a: 1 }));\n" + handler, "JSON(", 0, answer("context.JSON(…) không có status trong chuỗi")},
		{"View in capitals", routerImport + "router.guard((context) => context.View({ a: 1 }));\n" + handler, "View(", 0, ContextViewInGuard("context.View(…)")},
		{"text of context.res", routerImport + "router.guard((context) => context.res.text(\"đóng\"));\n" + handler, "text(\"đóng", 0, answer("….text(…)")},
		// a number or a string built in the return
		{"a sum", routerImport + "router.guard((context) => 1 + 2);\n" + handler, "1 + 2", 0, GuardReturnsValue("một số")},
		{"a product", routerImport + "router.guard((context) => 2 * 3);\n" + handler, "2 * 3", 0, GuardReturnsValue("một số")},
		{"typeof", routerImport + "router.guard((context) => typeof context);\n" + handler, "typeof", 0, GuardReturnsValue("một chuỗi")},
		{"a + of two calls", routerImport + "router.guard((context) => context.query(\"a\") + context.query(\"b\"));\n" + handler, "context.query(\"a\") +", 0, GuardReturnsValue("một số hay một chuỗi")},
		// a guard that takes no context, and one first read as a helper
		{"a guard with no parameter", routerImport + "router.guard(() => \"đóng\");\n" + handler, "\"đóng\"", 0, GuardReturnsValue("một chuỗi")},
		{"a route's guard with no parameter", routerImport + "router.get((context) => context.text(\"x\")).guard(() => ({ a: 1 }));\n", "{ a", 0, GuardReturnsValue("một object")},
		{"one of an array with no parameter", routerImport + "router.guard([() => \"x\"]);\n" + handler, "\"x\"", 0, GuardReturnsValue("một chuỗi")},
		{"a const with no parameter", routerImport + "const gate = () => 403;\nrouter.guard(gate);\n" + handler, "403", 0, GuardReturnsValue("một số")},
		{"a guard first read as a helper", routerImport + "const check = (context) => \"đóng\";\nrouter.guard((context) => check(context) === \"x\");\nrouter.guard(check);\n" + handler, "\"đóng\"", 0, GuardReturnsValue("một chuỗi")},
		// a value a ternary, a logical operator, a sign or a + may give
		{"a ternary", routerImport + "router.guard((context) => context.request.method === \"POST\" ? \"đóng\" : true);\n" + handler, "\"đóng\"", 0, GuardReturnsValue("một chuỗi")},
		{"||", routerImport + "router.guard((context) => context.request.method === \"GET\" || \"đóng\");\n" + handler, "\"đóng\"", 0, GuardReturnsValue("một chuỗi")},
		{"&&", routerImport + "router.guard((context) => context.request.method === \"GET\" && ({ a: 1 }));\n" + handler, "{ a", 0, GuardReturnsValue("một object")},
		{"??", routerImport + "router.guard((context) => null ?? \"x\");\n" + handler, "\"x\"", 0, GuardReturnsValue("một chuỗi")},
		{"a signed number", routerImport + "router.guard((context) => -1);\n" + handler, "-1", 0, GuardReturnsValue("một số")},
		{"a string joined", routerImport + "router.guard((context) => \"a\" + context.request.method);\n" + handler, "\"a\" +", 0, GuardReturnsValue("một chuỗi")},
	})
	expectRouterCompiles(t, "shop.com/router.work", []string{
		routerImport + "router.guard((context) => true);\n" + handler,
		routerImport + "router.guard((context) => null);\n" + handler,
		routerImport + "router.guard((context) => {\n  return;\n});\n" + handler,
		routerImport + "router.guard((context) => context.redirect(\"/login\"));\n" + handler,
		routerImport + "router.guard((context) => context.status(302).redirect(\"/login\"));\n" + handler,
		routerImport + "router.guard((context) => context.status(401).header(\"www-authenticate\", \"Bearer\").json({ error: \"login\" }));\n" + handler,
		routerImport + "router.guard((context) => context.header(\"x-a\", \"b\").status(401).json({ error: \"login\" }));\n" + handler,
		routerImport + "router.guard((context) => {\n  context.status(401).json({ error: \"login\" });\n});\n" + handler,
		// what a function written in the guard returns is its own; a name or a call the guard returns is read as it runs
		routerImport + "router.guard((context) => [1].map((n) => \"x\").length > 0);\n" + handler,
		routerImport + "const label = () => \"x\";\nrouter.guard((context) => label() === \"x\");\n" + handler,
		routerImport + "const allowed = (context) => context.request.method === \"GET\";\nrouter.guard((context) => allowed(context));\n" + handler,
		// the status in the json's own chain, in any case, through the response's names, in a function the guard hands
		// the context to
		routerImport + "router.guard((context) => context.Status(401).Json({ error: \"login\" }));\n" + handler,
		routerImport + "router.guard((context) => context.res.status(401).json({ error: \"login\" }));\n" + handler,
		routerImport + "router.guard(({ response }) => response.status(401).json({ error: \"login\" }));\n" + handler,
		routerImport + "const unauthorized = (c) => c.status(401).json({ error: \"login\" });\nrouter.guard((context) => unauthorized(context));\n" + handler,
		// a function the guard hands the context to returns to the guard, a string included
		routerImport + "const role = (context) => \"admin\";\nrouter.guard((context) => role(context) === \"admin\");\n" + handler,
		routerImport + "router.guard(() => true);\n" + handler,
		routerImport + "router.guard((context) => context.request.method === \"GET\" ? true : false);\n" + handler,
		// the handler answers as it likes
		routerImport + "router.get((context) => context.status(200).html(\"<p>x</p>\")).guard((context) => true);\n",
	})
	for _, relative := range []string{"shop.com/config.work", "shop.com/a/config.work"} {
		expectRouterErrors(t, relative, []routerRow{
			{"text", routerImport + "router.guard((context) => context.status(403).text(\"đóng\"));\n", "text(", 0, answer("….text(…)")},
			{"a string", routerImport + "router.guard((context) => \"đóng\");\n", "\"đóng\"", 0, GuardReturnsValue("một chuỗi")},
		})
	}
	// a guard imported from another file: the error stands there, told from the guard
	for name, row := range map[string]struct{ library, marker, want string }{
		"text":     {"export const closed = (context) => context.status(403).text(\"đóng\");\n", "text(", answer("….text(…)")},
		"a string": {"export const closed = (context) => \"đóng\";\n", "\"đóng\"", GuardReturnsValue("một chuỗi")},
	} {
		source := routerImport + "import { closed } from \"app/.shared/rules.work\";\nrouter.guard(closed);\n" + handler
		_, err := compileRouterApp(t, map[string]string{".shared/rules.work": row.library, "shop.com/router.work": source}, "shop.com/router.work")
		want := placeIn("../.shared/rules.work", row.library, row.marker, 0) + ": " + row.want + " (guard ở " + placeIn("router.work", source, "closed);", 0) + ")"
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("an imported guard, %s:\n  got  %v\n  want %s", name, err, want)
		}
	}
}
