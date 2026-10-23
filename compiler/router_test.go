package compiler

import (
	"path/filepath"
	"strings"
	"testing"
)

// router.view is gone (KITWORK_ROUTER.md, 23/10/2026): a page is router.get((context) => context.view({ … })).
// In a router.work, router.view read off the router, however the router is named or view taken apart, is a
// compile error at the word, naming the spelling to write. router.view of a template site's
// router.kitwork.js, view of something not the router, and context.view compile: the router refuses the
// first when it runs (work/router_ui.go), the others are not router.view.
//
// CONTROL (23/10/2026): with routerMember and routerDeclaration refusing nothing, every refused row compiled
// ("import * as router from \"kitwork/router\";\nrouter.view((context) => { });: <nil>, want
// router.work:2:8: router.view đã bỏ: …"). With routerMember not taking kitwork().router for the router, the
// call kitwork().router.view(…) compiled ("<nil>, want router.work:2:18: …"); with routerDeclaration's
// kitwork().router.view branch off as well, const page = kitwork().router.view compiled too (either refuses
// it, at the same word).
func TestRouterViewIsACompileErrorInRouterWork(t *testing.T) {
	const namespace = "import * as router from \"kitwork/router\";\n"
	for source, at := range map[string]string{
		namespace + "router.view((context) => { });":                                        "router.work:2:8",
		namespace + "router.view(() => {\n  const title = \"x\";\n});":                      "router.work:2:8",
		"import { router } from \"kitwork\";\nrouter.view((ctx) => { ctx.title(\"x\"); });": "router.work:2:8",
		"import { router as r } from \"kitwork\";\nr.view((context) => { });":               "router.work:2:3",
		namespace + "const { get, view } = router;":                                         "router.work:2:14",
		"import { view } from \"kitwork/router\";":                                          "router.work:1:1",
		namespace + "const page = router.view;":                                             "router.work:2:21",
		"import { kitwork } from \"kitwork\";\nkitwork().router.view((context) => { });":    "router.work:2:18",
		"import { kitwork } from \"kitwork\";\nconst page = kitwork().router.view;":         "router.work:2:31",
	} {
		_, err := compileAppRoute(t, source)
		want := at + ": " + RouterViewRemoved
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v, want %q", source, err, want)
		}
	}
	if !strings.Contains(RouterViewRemoved, `router.get((context) => context.title("…").view({ … }))`) {
		t.Errorf("the error does not name the spelling to write: %s", RouterViewRemoved)
	}

	for _, source := range []string{
		namespace + "router.get((context) => context.title(\"x\").view({ title: \"x\" }));",
		namespace + "const shelf = { view: () => 1 };\nrouter.get((context) => context.json({ n: shelf.view() }));",
		namespace + "router.get(({ response }) => response.view({ n: 1 }));",
	} {
		if _, err := compileAppRoute(t, source); err != nil {
			t.Errorf("%q: %v, want it to compile", source, err)
		}
	}
	root := writeApps(t, map[string]string{
		"apps/acme/shop.com/router.kitwork.js": "import { router } from \"kitwork\";\nrouter.view((ctx) => { });\n",
	})
	appRoot := filepath.Join(root, "apps", "acme")
	if _, err := CompileFileInApp(appRoot, filepath.Join(appRoot, "shop.com", "router.kitwork.js")); err != nil {
		t.Errorf("router.view in a router.kitwork.js: %v, want it to compile (the router refuses it when it runs)", err)
	}
}
