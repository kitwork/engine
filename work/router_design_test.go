package work

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/value"
)

// design.work (KITWORK_DESIGN.md, 28/10/2026): a folder of a Kitwork UI site declares its look in a file of its
// own, the seven words of kitwork/design, each the router's word of that name run into the folder before its
// config.work and its router.work. The tests below build an app acme under a temporary root (bootAppSite), each
// site a domain folder of it. The CONTROL of each test is in its comment: the code under test disabled through go
// test -overlay (a copy of the file, the tree untouched), the test red for the reason quoted, then green again.

const designImport = "import * as design from \"kitwork/design\";\n"

// designLook is a folder's look in the seven words, written with the namespace given ("router" or "design").
func designLook(namespace string) string {
	return strings.ReplaceAll(`ns.colors({ brand: "#f5225f", ink: { light: "#242442", dark: "#c0caf5" }, canvas: { light: "#f6f9fc", dark: "#16161e" } });
ns.gradients({ hero: { type: "linear", direction: "to-right", stops: [ { color: "brand", at: 0 }, { color: "paper", at: 100 } ] } });
ns.fonts({ body: "Outfit", code: "Google Sans Code" });
ns.highlight({ keyword: { light: "#7847bd", dark: "#bb9af7" }, "function": { light: "#2e5cb8", dark: "#7aa2f7" } });
ns.appearance("dark");
ns.contrast("warning");
ns.transition("fade");
`, "ns.", namespace+".")
}

// designSite is a site whose root and branch quan-ly declare their look with the namespace given: in router.work
// and config.work ("router"), or in design.work ("design").
func designSite(namespace string) map[string]string {
	page := "<content><box background=\"hero\" padding=\"4\"><text heading=\"1\">{{ name }}</text></box>" +
		"<code language=\"go\">package main\nfunc main() { println(\"x\") } // chạy</code></content>"
	files := map[string]string{
		"acme/localhost/content.ui":         page,
		"acme/localhost/quan-ly/content.ui": page,
		"acme/localhost/view.ui":            "<view><content /></view>",
	}
	root := routerImport + "router.language(\"vi\");\nrouter.reload(false);\n"
	branch := routerImport
	switch namespace {
	case "router":
		root += designLook("router")
		files["acme/localhost/quan-ly/config.work"] = routerImport + "router.colors({ brand: \"#2563eb\" });\nrouter.transition(\"slide\");\n"
	case "design":
		files["acme/localhost/design.work"] = designImport + designLook("design")
		files["acme/localhost/quan-ly/design.work"] = designImport + "design.colors({ brand: \"#2563eb\" });\ndesign.transition(\"slide\");\n"
	default:
		// the page's gradient alone, which the page cannot be drawn without
		root += strings.SplitAfter(designLook("router"), "\n")[1]
	}
	files["acme/localhost/router.work"] = root + "router.get((context) => context.title(\"Quán\").view({ name: \"Quán\" }));\n"
	files["acme/localhost/quan-ly/router.work"] = branch + "router.get((context) => context.title(\"Quản lý\").view({ name: \"Quản lý\" }));\n"
	return files
}

// design.work is the router's seven words in a file of their own: a site whose root and branch write them in
// design.work answers every page with the same bytes, the HTML and each stylesheet it links, as the same site
// writing router.colors(…)… in router.work and config.work. The look is there (not two sites with none): the
// branch's brand over the root's, the root's ink, its dark appearance, the branch's transition, the gradient, the
// highlight.
//
// CONTROL: the same site with no look but the page's gradient differs on every page. CONTROL (06/11/2026,
// -overlay with compileUIFolder not running the folder's design.work): the design.work site's page could not be
// drawn, "a .ui page has a stylesheet of its own …: content.ui:1:15: background=\"hero\": \"hero\" không có trong
// bảng màu của trang" (the gradient its design.work declares never reached it).
func TestDesignWorkIsTheRouterWordsInAFileOfTheirOwn(t *testing.T) {
	routerSite, err := bootAppSite(t, t.TempDir(), designSite("router"), "localhost")
	if err != nil {
		t.Fatalf("router.work: %v", err)
	}
	designed, err := bootAppSite(t, t.TempDir(), designSite("design"), "localhost")
	if err != nil {
		t.Fatalf("design.work: %v", err)
	}
	bare, err := bootAppSite(t, t.TempDir(), designSite(""), "localhost")
	if err != nil {
		t.Fatalf("no look: %v", err)
	}
	for target, wants := range map[string][]string{
		"http://localhost/":        {`data-kitwork-appearance="dark"`, `data-kitwork-transition="fade"`, "--brand:#f5225f", "--kitwork-highlight-keyword:#7847bd", "linear-gradient("},
		"http://localhost/quan-ly": {`data-kitwork-appearance="dark"`, `data-kitwork-transition="slide"`, "--brand:#2563eb", "--kitwork-highlight-keyword:#bb9af7", "linear-gradient("},
	} {
		want := pageWithStylesheet(t, routerSite, target)
		got := pageWithStylesheet(t, designed, target)
		if got != want {
			t.Errorf("%s: design.work answers other bytes than router.work\n--- router.work\n%s\n--- design.work\n%s", target, linesWith(want, "kitwork"), linesWith(got, "kitwork"))
		}
		for _, look := range wants {
			if !strings.Contains(got, look) {
				t.Errorf("%s is missing %s", target, look)
			}
		}
		if plain := pageWithStylesheet(t, bare, target); plain == got { // CONTROL
			t.Errorf("CONTROL: %s with no look answers the same bytes", target)
		}
	}
}

// design.work runs first in its folder, before config.work and router.work, into the same folder: a token
// design.work and a later file both write is the later file's; a token only design.work writes stays. A word whose nearest folder wins is once
// per folder across the three files, the error naming both calls; an error of a word's value says design.<word>,
// at its place in design.work.
//
// CONTROL (06/11/2026, three -overlay runs, each red): with compileUIFolder running design.work after router.work,
// "--brand:#222222 missing", "--ink:#444444 missing" and "… theirs wins over --brand:#111111" (design.work won),
// the transition error raised at design.work:2 instead of router.work:2; with once() not naming a design.work's
// call, "router.transition gọi hai lần, design.work:2 và router.work:2 …"; with designWord handing the router's
// message on, "design.work:2:7: call fonts: router.fonts({ heading: … }) …".
func TestDesignWorkRunsFirstInItsFolder(t *testing.T) {
	handler := "router.get((context) => context.title(\"Quán\").view({ name: \"Quán\" }));\n"
	tenant, err := bootAppSite(t, t.TempDir(), map[string]string{
		"acme/localhost/design.work": designImport + "design.colors({ brand: \"#111111\", ink: \"#333333\", line: \"#555555\" });\n",
		"acme/localhost/config.work": routerImport + "router.colors({ ink: \"#444444\", panel: \"#eeeeee\" });\n",
		"acme/localhost/router.work": routerImport + "router.colors({ brand: \"#222222\" });\n" + handler,
		"acme/localhost/content.ui":  "<content><text>{{ name }}</text></content>",
	}, "localhost")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	page := pageWithStylesheet(t, tenant, "http://localhost/")
	for _, want := range []string{"--brand:#222222", "--ink:#444444", "--line:#555555", "--panel:#eeeeee"} {
		if !strings.Contains(page, want) {
			t.Errorf("%s missing\n%s", want, linesWith(page, "--brand"))
		}
	}
	for _, earlier := range []string{"--brand:#111111", "--ink:#333333"} {
		if strings.Contains(page, earlier) {
			t.Errorf("config.work and router.work run after design.work: theirs wins over %s\n%s", earlier, linesWith(page, "--brand"))
		}
	}

	for _, c := range []struct{ name, design, router, want string }{
		{"transition in design.work and router.work",
			designImport + "design.transition(\"fade\");\n", routerImport + "router.transition(\"slide\");\n" + handler,
			"router.work:2:7: " + compiler.DesignCallTwice("transition", "design.work:2", "router.transition ở router.work:2")},
		{"a value design.fonts cannot read",
			designImport + "design.fonts({ heading: \"Inter\" });\n", routerImport + handler,
			"initialize design design.work: design.work:2:7: call fonts: " + `design.fonts({ heading: … }): không có vai trò "heading"`},
		{"kitwork().design in router.work",
			designImport + "design.colors({ brand: \"#111111\" });\n", routerImport + "const look = kitwork().design;\n" + handler,
			compiler.DesignImportOutside},
	} {
		_, err := bootAppSite(t, t.TempDir(), map[string]string{
			"acme/localhost/design.work": c.design,
			"acme/localhost/router.work": c.router,
			"acme/localhost/content.ui":  "<content><text>{{ name }}</text></content>",
		}, "localhost")
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: run = %v\nwant %s", c.name, err, c.want)
		}
	}
}

// The app's design.work is the parent of every site's root, as the app's config.work is (KITWORK_DESIGN.md mục 2:
// Kit Design, then the app's folder, the site's root, the branches): a site with none takes it whole, a site's own
// design.work writes only what it wants other. The app's design.work and config.work are one folder: a word whose
// nearest folder wins in both stops each site naming both; an error of the app's design.work names app/design.work.
//
// CONTROL (06/11/2026, -overlay with compileUIFolder hearing only the app's config.work): one.example had neither
// --brand:#123456 nor --accent:#654321 nor data-kitwork-appearance="dark", two.example no --brand:#123456, and
// both rows below booted ("run = <nil>").
func TestDesignWorkOfTheAppIsTheParentOfEverySite(t *testing.T) {
	root := t.TempDir()
	page := routerImport + "router.get((context) => context.title(\"Trang\").view({ name: \"x\" }));\n"
	one, err := bootAppSite(t, root, map[string]string{
		"acme/design.work":             designImport + "design.colors({ brand: \"#123456\", accent: \"#654321\" });\ndesign.appearance(\"dark\");\n",
		"acme/one.example/router.work": page,
		"acme/one.example/content.ui":  "<content><text>{{ name }}</text></content>",
		"acme/two.example/design.work": designImport + "design.colors({ accent: \"#0a0a0a\" });\ndesign.appearance(\"light\");\n",
		"acme/two.example/router.work": page,
		"acme/two.example/content.ui":  "<content><text>{{ name }}</text></content>",
	}, "one.example")
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
		{one, "one.example", `data-kitwork-appearance="dark"`, []string{"--brand:#123456", "--accent:#654321"}},
		{two, "two.example", `data-kitwork-appearance="light"`, []string{"--brand:#123456", "--accent:#0a0a0a"}},
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

	for _, c := range []struct{ name, design, config, want string }{
		{"appearance in the app's design.work and config.work",
			designImport + "design.appearance(\"dark\");\n", routerImport + "router.appearance(\"light\");\n",
			"initialize router app/config.work: config.work:2:7: " + compiler.DesignCallTwice("appearance", "design.work:2", "router.appearance ở config.work:2")},
		{"a value of the app's design.work",
			designImport + "design.transition(\"zoom\");\n", "",
			"initialize design app/design.work: design.work:2:7: "},
	} {
		files := map[string]string{
			"acme/design.work":             c.design,
			"acme/one.example/router.work": page,
			"acme/one.example/content.ui":  "<content><text>{{ name }}</text></content>",
		}
		if c.config != "" {
			files["acme/config.work"] = c.config
		}
		_, err := bootAppSite(t, t.TempDir(), files, "one.example")
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: run = %v\nwant %s", c.name, err, c.want)
		}
		if err != nil && strings.Contains(err.Error(), "router.transition") {
			t.Errorf("%s: an error of design.work says design.transition: %v", c.name, err)
		}
	}
}

// design.work has a role where config.work has one: the app's folder, a site's root, a branch. In .shared/ or
// .schedule/ (any depth), at the top of .database/, in a folder's assets/ (any depth), it stops the site at boot:
// "design.work không có vai ở đây" (KITWORK_DESIGN.md mục 1), never a file that silently does nothing.
//
// CONTROL: a design.work deeper in .database/ is a file the app stored (.database/files/), not code: the site
// boots. CONTROL (06/11/2026, -overlay with misplacedDesign and designInAssets returning nil): every row booted.
func TestDesignWorkWhereNoFolderHasALook(t *testing.T) {
	look := designImport + "design.colors({ brand: \"#111111\" });\n"
	site := map[string]string{
		"acme/localhost/router.work":   routerImport + "router.get((context) => context.text(\"x\"));\n",
		"acme/localhost/a/router.work": routerImport + "router.get((context) => context.text(\"a\"));\n",
	}
	for _, place := range []string{
		"acme/.shared/design.work",
		"acme/.shared/queue/design.work",
		"acme/.schedule/design.work",
		"acme/.database/design.work",
		"acme/localhost/assets/design.work",
		"acme/localhost/a/assets/img/design.work",
	} {
		files := map[string]string{place: look}
		for name, source := range site {
			files[name] = source
		}
		_, err := bootAppSite(t, t.TempDir(), files, "localhost")
		want := strings.TrimPrefix(place, "acme/") + ": design.work không có vai ở đây"
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: run = %v\nwant %s", place, err, want)
		}
	}
	files := map[string]string{"acme/.database/files/design.work": look}
	for name, source := range site {
		files[name] = source
	}
	if _, err := bootAppSite(t, t.TempDir(), files, "localhost"); err != nil {
		t.Errorf("CONTROL: a file the app stored in .database/files/ boots, run = %v", err)
	}
}

// A folder's design.work is watched, the app's too, each even when absent: one appearing, changing or going builds
// the site again, as config.work does.
//
// CONTROL (06/11/2026, -overlay with compileUIFolder not adding the folder's design.work and hearing only the app's
// config.work): "localhost/design.work is not watched", "design.work of the app is not watched".
func TestDesignWorkIsWatched(t *testing.T) {
	root := t.TempDir()
	tenant, err := bootAppSite(t, root, map[string]string{
		"acme/localhost/router.work": routerImport + "router.get((context) => context.text(\"x\"));\n",
	}, "localhost")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	watched := map[string]bool{}
	for _, file := range tenant.routeTree().root.sourceFiles() {
		absolute, _ := filepath.Abs(file)
		watched[filepath.Clean(absolute)] = true
	}
	for name, file := range map[string]string{
		"localhost/design.work":  filepath.Join(root, "acme", "localhost", compiler.DesignFile),
		"design.work of the app": filepath.Join(root, "acme", compiler.DesignFile),
		"localhost/config.work":  filepath.Join(root, "acme", "localhost", compiler.RouterConfigFile),
		"config.work of the app": filepath.Join(root, "acme", compiler.RouterConfigFile),
	} {
		absolute, _ := filepath.Abs(file)
		if !watched[filepath.Clean(absolute)] {
			t.Errorf("%s is not watched: %v", name, tenant.routeTree().root.sourceFiles())
		}
	}
}

// What the compiler refuses first, the engine refuses again where it runs: kitwork().router while a design.work
// runs, a call of design.work chained, design.<word> not of the seven.
//
// CONTROL (06/11/2026, -overlay with Router() giving the router whatever runs, routerCallResult ignoring its
// module, designWord taking every word and KitWork.MissingMember not knowing design): every check went red,
// "kitwork().router in design.work: got , want the error" (a router); "a design call chained: got
// \"router.colors(…).fonts(…) nối tiếp …\""; "design.guard: got , want …" and "design.colours: got , want …" (a
// function); "kitwork().design outside a folder: got null".
func TestDesignRefusedAtRunTime(t *testing.T) {
	folder := &FolderRouter{ui: true, running: compiler.DesignFile}
	node := &RouteNode{folder: folder}
	folder.node = node
	tree := &TreeKitWork{node: node}
	if got := tree.Router(); got.K != value.Invalid || !strings.Contains(got.Text(), "trong design.work") {
		t.Errorf("kitwork().router in design.work: got %v, want the error", got)
	}
	if got := tree.Design(); got.K == value.Invalid {
		t.Errorf("kitwork().design in design.work: %v", got.Text())
	}
	folder.running = compiler.RouterFile
	if got := tree.Design(); got.K != value.Invalid || got.Text() != compiler.DesignImportOutside {
		t.Errorf("kitwork().design in router.work: got %v, want %s", got, compiler.DesignImportOutside)
	}
	chained := &routerCallResult{word: "colors", module: "design"}
	if got := chained.OnInvoke("fonts"); got.Text() != compiler.DesignCallChained("colors", ".fonts(…)") {
		t.Errorf("a design call chained: got %q", got.Text())
	}
	for word, want := range map[string]string{
		"guard":   compiler.DesignRouterWord("guard"),
		"colours": compiler.DesignUnknownWord("colours"),
	} {
		if got := folder.designWord(word); got.K != value.Invalid || got.Text() != want {
			t.Errorf("design.%s: got %v, want %s", word, got, want)
		}
	}
	if got := (&KitWork{}).MissingMember("design"); got.K != value.Invalid || got.Text() != compiler.DesignImportOutside {
		t.Errorf("kitwork().design outside a folder: got %v", got)
	}
}
