package compiler

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// compileAppRoute compiles source as apps/acme/shop.com/router.work, a file of the app apps/acme/.
func compileAppRoute(t *testing.T, source string) (*Bytecode, error) {
	t.Helper()
	root := writeApps(t, map[string]string{"apps/acme/shop.com/router.work": source})
	return CompileFileInApp(filepath.Join(root, "apps", "acme"), filepath.Join(root, "apps", "acme", "shop.com", "router.work"))
}

const importEnvironment = "import { environment } from \"kitwork\";\n"

// environment is read by name only (docs/KITWORK_ENVIRONMENT.md mục 3, 22/10/2026): environment.NAME and
// environment.require("NAME"). A write, another name for the whole, handing it to a function, an index,
// going through it, taking it apart, and a require of a name not written as a string are compile errors
// where they are written, so no program holds the whole set and the boot sees every key required.
//
// CONTROL (22/10/2026): with the resolver's rule taken out (reference letting environment through, and
// environmentTarget, environmentCall's literal check refusing nothing), thirteen rows compiled ("environment.X
// = 1;: <nil>, want …", the require of a name held in a variable with no key to check), and environment = {}
// failed in the words of a const ("environment là hằng (const)"), not those of environment.
func TestEnvironmentReadOnlyByName(t *testing.T) {
	for source, want := range map[string]string{
		"environment.X = 1;":                                       "router.work:2:13: environment chỉ đọc: không gán environment.X;",
		"environment.X++;":                                         "router.work:2:13: environment chỉ đọc: không gán environment.X;",
		"environment[\"X\"] = 1;":                                  "router.work:2:12: environment chỉ đọc: không gán vào environment;",
		"environment = {};":                                        "router.work:2:1: environment chỉ đọc: không gán vào environment;",
		"const keys = Object.keys(environment);":                   "router.work:2:26: environment chỉ đọc theo tên: viết environment.TÊN",
		"const e = environment;":                                   "router.work:2:11: environment chỉ đọc theo tên",
		"const name = \"X\"; const v = environment[name];":         "router.work:2:29: environment chỉ đọc theo tên",
		"for (const k of environment) { }":                         "router.work:2:17: environment chỉ đọc theo tên",
		"const { PORT } = environment;":                            "router.work:2:18: environment chỉ đọc theo tên",
		"const all = { ...environment };":                          "router.work:2:18: environment chỉ đọc theo tên",
		"const name = \"X\"; const v = environment.require(name);": "router.work:2:49: environment.require nhận đúng một tên khoá viết thẳng là chuỗi: environment.require(\"STRIPE_SECRET\")",
		"const v = environment.require();":                         "router.work:2:23: environment.require nhận đúng một tên khoá viết thẳng là chuỗi",
		"const v = environment.require(\"A\", \"B\");":             "router.work:2:31: environment.require nhận đúng một tên khoá viết thẳng là chuỗi",
		"const v = environment.require(`A`);":                      "router.work:2:31: environment.require nhận đúng một tên khoá viết thẳng là chuỗi",
	} {
		_, err := compileAppRoute(t, importEnvironment+source)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", source, err, want)
		}
	}
}

// What reads by name compiles and runs: environment.NAME with a default, ?? for a boolean, a require of a
// literal, a name read inside a function and in a template, import { environment as e }, import * as
// kitwork from "kitwork" with kitwork.environment.NAME, and kitwork.environment.require. The whole under
// another name, from either form of import, is refused as environment is.
//
// CONTROL (22/10/2026): with reference letting environment through and environmentMember letting
// kitwork.environment be read whole, the three refusals compiled: "const all = settings;: <nil>, want …".
func TestEnvironmentReadByNameCompiles(t *testing.T) {
	for _, source := range []string{
		importEnvironment + "const port = environment.PORT || 8080;",
		importEnvironment + "const signup = environment.SIGNUP_OPEN ?? true;",
		importEnvironment + "const stripe = environment.require(\"STRIPE_SECRET\");",
		importEnvironment + "const read = () => `${environment.HOST}:${environment.PORT}`;",
		importEnvironment + "const has = environment.SECRET != null; const called = () => environment.API_KEY();",
		"import { environment as settings } from \"kitwork\";\nconst port = settings.PORT || 8080;",
		"import * as kitwork from \"kitwork\";\nconst port = kitwork.environment.PORT || 8080; const key = kitwork.environment.require(\"KEY\");",
	} {
		if _, err := compileAppRoute(t, source); err != nil {
			t.Errorf("%q: %v", source, err)
		}
	}
	for source, want := range map[string]string{
		"import { environment as settings } from \"kitwork\";\nconst all = settings;":                      "router.work:2:13: environment chỉ đọc theo tên",
		"import * as kitwork from \"kitwork\";\nconst all = kitwork.environment;":                          "router.work:2:21: environment chỉ đọc theo tên",
		"import * as kitwork from \"kitwork\";\nconst { environment } = kitwork; const all = environment;": "router.work:2:46: environment chỉ đọc theo tên",
	} {
		if _, err := compileAppRoute(t, source); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v, want %q", source, err, want)
		}
	}
}

// Every environment.require("KEY") of a program is on its bytecode with where it is written, those of a
// module it imports too, and they come back the same from a cached artifact: the boot checks them
// (work/environment.go) whether the program compiled now or before.
//
// CONTROL (22/10/2026): with FileCache's hit giving the artifact without reading the program again, the
// second compile carried no require: "from the cache: [], want …".
func TestEnvironmentRequiresAreOnTheBytecode(t *testing.T) {
	root := writeApps(t, map[string]string{
		"apps/acme/.shared/mail.work": importEnvironment + "export const send = () => environment.require(\"SMTP_URL\");\n",
		"apps/acme/shop.com/router.work": importEnvironment + "import { send } from \"app/.shared/mail.work\";\n" +
			"const stripe = environment.require(\"STRIPE_SECRET\");\nconst port = environment.PORT || 8080;\n",
	})
	appRoot := filepath.Join(root, "apps", "acme")
	entry := filepath.Join(appRoot, "shop.com", "router.work")
	want := []NameUse{
		{Name: "SMTP_URL", File: "../.shared/mail.work", Line: 2, Column: 47},
		{Name: "STRIPE_SECRET", File: "router.work", Line: 3, Column: 36},
	}
	sorted := func(uses []NameUse) []NameUse {
		out := append([]NameUse(nil), uses...)
		for i := range out {
			for j := i + 1; j < len(out); j++ {
				if out[j].Name < out[i].Name {
					out[i], out[j] = out[j], out[i]
				}
			}
		}
		return out
	}
	compiled, err := CompileFileInApp(appRoot, entry)
	if err != nil {
		t.Fatal(err)
	}
	if got := sorted(compiled.EnvironmentRequires); !reflect.DeepEqual(got, want) {
		t.Fatalf("compiled: %+v, want %+v", got, want)
	}
	cache := NewFileCache(t.TempDir())
	for _, pass := range []string{"compiled into the cache", "from the cache"} {
		cached, err := cache.CompileFileInApp(appRoot, entry)
		if err != nil {
			t.Fatal(err)
		}
		if got := sorted(cached.EnvironmentRequires); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: %+v, want %+v", pass, got, want)
		}
	}
}

// env, the name of environment until 22/10/2026, is refused in a file of an app in every form an import
// lowers to, with the name to write; a manifest, compiled with no app, keeps its import { env } (mục 9.8).
//
// CONTROL (22/10/2026): with environmentDeclaration and environmentMember refusing nothing, every row of an
// app compiled: "import { router, env } from \"kitwork\";: <nil>, want …". (24/10/2026: the first row imports
// safe beside env, since import { router } from "kitwork" is itself refused in a router.work now.)
func TestEnvImportIsRefusedInAnApp(t *testing.T) {
	const renamed = "`env` đã đổi tên: import { environment } from \"kitwork\", rồi environment.KEY"
	for source, at := range map[string]string{
		"import { safe, env } from \"kitwork\";\nconst port = env.PORT;":        "router.work:1:1: ",
		"import { env as e } from \"kitwork\";\nconst port = e.PORT;":           "router.work:1:1: ",
		"import * as e from \"kitwork/env\";\nconst port = e.PORT;":             "router.work:1:13: ",
		"import * as kitwork from \"kitwork\";\nconst port = kitwork.env.PORT;": "router.work:2:22: ",
		"import * as kitwork from \"kitwork\";\nconst { env } = kitwork;":       "router.work:2:9: ",
	} {
		_, err := compileAppRoute(t, source)
		if err == nil || !strings.Contains(err.Error(), at+renamed) {
			t.Errorf("%q: %v, want %q", source, err, at+renamed)
		}
	}
	root := writeApps(t, map[string]string{"application.work": "import { app, env } from \"kitwork\";\nconst port = env.PORT || 8080;\n"})
	if _, err := CompileFile(filepath.Join(root, "application.work")); err != nil {
		t.Errorf("a manifest with import { env }: %v", err)
	}
}

// A key not named PUBLIC_… never goes straight to a page (mục 6): an entry of the object .view({ … }) is
// given is a compile error naming the key. PUBLIC_ keys, a yes/no made of a secret, and a secret used in a
// handler pass; a const of the file that holds one never reaches a page by itself, since the page reads only
// the names of the object (KITWORK_ROUTER.md, 23/10/2026).
//
// CONTROL (22/10/2026): with environmentView refusing nothing, "context.view({ stripe: environment.STRIPE_SECRET })
// compiled, want khoá `STRIPE_SECRET` sẽ đi xuống trình duyệt".
func TestEnvironmentSecretNeverGoesToThePage(t *testing.T) {
	const header = "import * as router from \"kitwork/router\";\n" + importEnvironment
	for source, want := range map[string]string{
		"router.get((context) => context.view({ stripe: environment.STRIPE_SECRET }));":                 "router.work:3:48: khoá `STRIPE_SECRET` sẽ đi xuống trình duyệt; khoá công khai đặt tên `PUBLIC_…`",
		"router.get((context) => context.view({ key: environment.require(\"API_KEY\") || \"\" }));":     "router.work:3:45: khoá `API_KEY` sẽ đi xuống trình duyệt",
		"const stripe = environment.STRIPE_SECRET;\nrouter.get((context) => context.view({ stripe }));": "router.work:4:40: khoá `STRIPE_SECRET` sẽ đi xuống trình duyệt",
	} {
		_, err := compileAppRoute(t, header+source)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v, want %q", source, err, want)
		}
	}
	if _, err := compileAppRoute(t, header+`const stripe = environment.require("STRIPE_SECRET");
const greeting = environment.PUBLIC_GREETING || "Xin chào";
router.get((context) => context.view({ greeting, name: environment.PUBLIC_NAME || "", secretSet: environment.DEMO_SECRET != null }));
router.post((context) => context.json({ charged: stripe.length > 0 }));
`); err != nil {
		t.Fatal(err)
	}
}

// What a .view(…) hands its page is checked however the key is written into it (mục 6, 22/10/2026): a member
// of a member of the object, an item of an array, a spread, an object passed by its name, text made with the
// key (a template, +). A member of an object read by its name holds only what that member holds: config.host
// of an object whose key member is a secret goes to the page.
//
// CONTROL (23/10/2026, on 22/10's rule): with environmentHolds reading no object, array, template, + or
// let (the 22/10 environmentSecret), the rows compiled: "router.get((context) => context.view({ config: {
// stripe: environment.STRIPE_SECRET } }));: <nil>, want …"; the precise member went with it.
func TestEnvironmentSecretNeverGoesToThePageHoweverDeep(t *testing.T) {
	const header = "import * as router from \"kitwork/router\";\n" + importEnvironment
	for source, want := range map[string]string{
		"router.get((context) => context.view({ config: { stripe: environment.STRIPE_SECRET } }));":                                "router.work:3:48: khoá `STRIPE_SECRET` sẽ đi xuống trình duyệt",
		"router.get((context) => context.view({ keys: [environment.STRIPE_SECRET] }));":                                            "router.work:3:46: khoá `STRIPE_SECRET` sẽ đi xuống trình duyệt",
		"router.get((context) => context.view({ header: `Bearer ${environment.TOKEN}` }));":                                        "router.work:3:48: khoá `TOKEN` sẽ đi xuống trình duyệt",
		"router.get((context) => context.view({ header: \"Bearer \" + environment.TOKEN }));":                                      "router.work:3:48: khoá `TOKEN` sẽ đi xuống trình duyệt",
		"const config = { db: { password: environment.DB_PASSWORD } };\nrouter.get((context) => context.view({ db: config.db }));": "router.work:4:44: khoá `DB_PASSWORD` sẽ đi xuống trình duyệt",
	} {
		_, err := compileAppRoute(t, header+source)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v, want %q", source, err, want)
		}
	}
	// a spread and an object passed by its name are refused in router.work itself (context.view takes an object
	// written in place, 24/10/2026); a page of .shared/ may still hand them, and is checked the same way
	for page, want := range map[string]string{
		"const secrets = { stripe: environment.STRIPE_SECRET };\nexport const page = (context) => context.view({ ...secrets });": "../.shared/page.work:3:52: khoá `STRIPE_SECRET` sẽ đi xuống trình duyệt",
		"const data = { stripe: environment.STRIPE_SECRET };\nexport const page = (context) => context.view(data);":              "../.shared/page.work:3:47: khoá `STRIPE_SECRET` sẽ đi xuống trình duyệt",
	} {
		root := writeApps(t, map[string]string{
			"apps/acme/.shared/page.work":    importEnvironment + page,
			"apps/acme/shop.com/router.work": "import * as router from \"kitwork/router\";\nimport { page } from \"app/.shared/page.work\";\nrouter.get((context) => page(context));\n",
		})
		_, err := CompileFileInApp(filepath.Join(root, "apps", "acme"), filepath.Join(root, "apps", "acme", "shop.com", "router.work"))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v, want %q", page, err, want)
		}
	}
	if _, err := compileAppRoute(t, header+`const config = { host: environment.PUBLIC_HOST, key: environment.STRIPE_SECRET };
let token = environment.TOKEN;
router.get((context) => context.view({ host: config.host, set: token != null, port: environment.PUBLIC_PORT || 8080 }));
`); err != nil {
		t.Fatal(err)
	}
}

// A const a module of the app exports that holds a key not named PUBLIC_… is the server's in the file that
// imports it as in its own (mục 6): handing it to a .view(…) is the compile error. A PUBLIC_ key exported
// beside it goes to the page.
//
// CONTROL (23/10/2026): with holdEnvironment reading nothing once a declaration is resolved, the import held
// nothing, and context.view({ stripe }) compiled.
func TestEnvironmentSecretOfAModuleIsServerOnly(t *testing.T) {
	keys := importEnvironment + "export const stripe = environment.STRIPE_SECRET;\nexport const greeting = environment.PUBLIC_GREETING || \"Xin chào\";\n"
	root := writeApps(t, map[string]string{
		"apps/acme/.shared/keys.work": keys,
		"apps/acme/shop.com/router.work": "import * as router from \"kitwork/router\";\nimport { stripe, greeting } from \"app/.shared/keys.work\";\n" +
			"router.get((context) => context.view({ greeting, charged: stripe.length > 0 }));\n",
		"apps/acme/blog.com/router.work": "import * as router from \"kitwork/router\";\nimport { stripe } from \"app/.shared/keys.work\";\n" +
			"router.get((context) => context.view({ stripe }));\n",
	})
	appRoot := filepath.Join(root, "apps", "acme")
	if _, err := CompileFileInApp(appRoot, filepath.Join(appRoot, "shop.com", "router.work")); err != nil {
		t.Fatal(err)
	}
	_, err := CompileFileInApp(appRoot, filepath.Join(appRoot, "blog.com", "router.work"))
	if want := "router.work:3:40: khoá `STRIPE_SECRET` sẽ đi xuống trình duyệt"; err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("context.view({ stripe }) of an import: %v, want %q", err, want)
	}
}

// The module "kitwork" is read by name too (mục 3): a word of it read by [ ] with a name the compiler cannot
// read, or with "environment", would hand a program environment whole, so it is a compile error; so is
// environment read whole through another name for the module. A word written as a string other than those
// two reads, and another name for the module reads environment by name as the module does.
//
// CONTROL (23/10/2026): with environmentIndex refusing nothing and a const of the module not marked as the
// module, the six rows compiled ("const e = kitwork()[\"env\"];: <nil>, want …"): kitwork()[word] is how a
// review read STRIPE_SECRET through e[name] at run time.
func TestKitworkModuleIsReadByName(t *testing.T) {
	const namespace = "import * as kitwork from \"kitwork\";\n"
	for source, want := range map[string]string{
		namespace + "const word = \"environ\" + \"ment\"; const e = kitwork[word];":  "router.work:2:51: module \"kitwork\" chỉ đọc theo tên: viết kitwork.TÊN",
		"const word = \"environment\"; const e = kitwork()[word];":                   "router.work:1:48: module \"kitwork\" chỉ đọc theo tên",
		"const k = kitwork(); const k2 = k; const name = \"x\"; const e = k2[name];": "router.work:1:66: module \"kitwork\" chỉ đọc theo tên",
		"const k = kitwork(); const e = k[\"environment\"];":                         "router.work:1:33: environment chỉ đọc theo tên",
		"const e = kitwork()[\"env\"];":                                              "router.work:1:20: `env` đã đổi tên",
		namespace + "const k2 = kitwork; const all = k2.environment;":                "router.work:2:36: environment chỉ đọc theo tên",
	} {
		if _, err := compileAppRoute(t, source); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v, want %q", source, err, want)
		}
	}
	for _, source := range []string{
		namespace + "const k2 = kitwork; const port = k2.environment.PORT || 8080; const key = k2.environment.require(\"KEY\");",
		// (24/10/2026) kitwork()["router"] in a router.work is refused by the router's rule (router_rules_test.go)
		"const clean = kitwork()[\"richtext\"];",
		"const table = { a: 1 }; const name = \"a\"; const a = table[name];",
	} {
		if _, err := compileAppRoute(t, source); err != nil {
			t.Errorf("%q: %v", source, err)
		}
	}
}
