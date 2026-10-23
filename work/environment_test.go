package work

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// The environment of an app (docs/KITWORK_ENVIRONMENT.md, 22/10/2026), driven through real tenants of the
// layout apps/<identity>/<domain>/: the app's .env at apps/<identity>/, the site's at <domain>/.

// environmentApp writes files under a fresh apps/ folder (paths with "/") and gives the folder.
func environmentApp(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for relative, content := range files {
		full := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// environmentRoute is a router.work of the site that answers what it reads as JSON.
func environmentRoute(reads string) string {
	return "import * as router from \"kitwork/router\";\nimport { environment } from \"kitwork\";\n" +
		"router.get((context) => context.json(" + reads + "));\n"
}

// environmentJSON boots the site localhost of root, serves path and gives the JSON it answered.
func environmentJSON(t *testing.T, root, path string, shared ...*SharedEnvironment) map[string]any {
	t.Helper()
	tenant := NewTenant(root, "localhost")
	if len(shared) > 0 {
		tenant.SetSharedEnvironment(shared[0])
	}
	if err := tenant.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	defer tenant.Close()
	recorder := serveIsolation(t, tenant, "http://localhost"+path)
	var got map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
		t.Fatalf("%s = %d %q: %v", path, recorder.Code, recorder.Body.String(), err)
	}
	return got
}

// The app's .env at its folder is the app's tier; the site's .env lies over it key by key (mục 1): a key of
// the site wins, a key only the app has is the app's. Each value comes coerced (mục 4): a canonical number
// is a number, a zero-padded code and a quoted version stay strings, false is a boolean ?? keeps, an empty
// value is no value.
//
// CONTROL (22/10/2026): with the app's .env not read (environmentFiles giving the site's alone), every key only
// the app holds was null: "got map[a:<nil> aKind:undefined b:site empty:absent flag:true version:<nil> …]".
func TestEnvironmentAppTierWithSiteOverlayByKey(t *testing.T) {
	root := environmentApp(t, map[string]string{
		"acme/.env":           "A=1\nB=app\nZIP=0123\nVERSION=\"1.10\"\nFLAG=false\nEMPTY=\n",
		"acme/localhost/.env": "B=site\n",
		"acme/localhost/router.work": environmentRoute(`{ a: environment.A, aKind: typeof environment.A, b: environment.B,
  zip: environment.ZIP, zipKind: typeof environment.ZIP, version: environment.VERSION, flag: environment.FLAG ?? true,
  empty: environment.EMPTY ?? "absent" }`),
	})
	got := environmentJSON(t, root, "/")
	want := map[string]any{
		"a": float64(1), "aKind": "number", "b": "site", "zip": "0123", "zipKind": "string", "version": "1.10",
		"flag": false, "empty": "absent",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %v\nwant %v", got, want)
	}
}

// env, the name until 22/10/2026, stops the boot of an app in the words of the new name, where it is
// written; a read the compiler could not follow (the module handed to a function that reads it by a computed
// key; kitwork()[word] written as such is a compile error since 22/10/2026) is the same error when it runs.
//
// CONTROL (22/10/2026): with environmentDeclaration refusing nothing, the boot still failed in the same words,
// now from the runtime (KitWork.MissingMember, as the import ran); with both refusing nothing the site booted:
// "run = <nil>, want the error naming environment". With MissingMember alone not knowing env, the computed read
// gave null: "kitwork()[\"env\"] = map[code:<nil> message:<nil>]" (the read was then k[word] in the handler;
// it moved into a function when the compiler began to refuse k[word] with k the module; CONTROL 23/10/2026 on
// the read through the function, MissingMember not knowing env: the same "map[code:<nil> message:<nil>]").
func TestEnvImportIsABootErrorNamingEnvironment(t *testing.T) {
	root := environmentApp(t, map[string]string{
		"acme/.env":                  "PORT=8080\n",
		"acme/localhost/router.work": "import { router, env } from \"kitwork\";\nrouter.get((context) => context.json({ port: env.PORT }));\n",
	})
	tenant := NewTenant(root, "localhost")
	err := tenant.Run()
	tenant.Close()
	const want = "router.work:1:1: `env` đã đổi tên: import { environment } from \"kitwork\", rồi environment.KEY, environment.require(\"KEY\") (KITWORK_ENVIRONMENT.md mục 11)"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("run = %v, want the error naming environment: %q", err, want)
	}

	runtimeRoot := environmentApp(t, map[string]string{
		"acme/localhost/router.work": "import * as router from \"kitwork/router\";\n" +
			"const read = (module, word) => module[word];\n" +
			"router.get((context) => { const r = safe(() => read(kitwork(), \"env\")); return context.json({ code: r.error.code, message: r.error.message }); });\n",
	})
	got := environmentJSON(t, runtimeRoot, "/")
	if got["code"] != "INVALID_ARGUMENT" || !strings.Contains(got["message"].(string), "`env` đã đổi tên: import { environment } from \"kitwork\"") {
		t.Fatalf("kitwork()[\"env\"] = %v, want INVALID_ARGUMENT naming environment", got)
	}
}

// Every environment.require("KEY") of the site's code is checked at boot, in every folder, before any of
// it runs: a key the app's environment has not (or has empty) stops the boot with one report listing every
// missing key and where it is required, never a value. A require inside a handler that has never run is
// checked as one at the file's level. The site boots once the keys are there.
//
// CONTROL (22/10/2026): with compileFolder not checking EnvironmentRequires, the site booted and the handler
// read null: "run = <nil>, want one error listing NEED_A and NEED_B".
func TestEnvironmentRequireMissingFailsBootOnce(t *testing.T) {
	files := map[string]string{
		"acme/.env": "HAVE=secret-value-xyz\nEMPTY=\n",
		"acme/localhost/router.work": "import * as router from \"kitwork/router\";\nimport { environment } from \"kitwork\";\n" +
			"const have = environment.require(\"HAVE\");\nconst a = environment.require(\"NEED_A\");\n" +
			"router.get((context) => context.json({ have: have.length }));\n",
		"acme/localhost/orders/router.work": "import * as router from \"kitwork/router\";\nimport { environment } from \"kitwork\";\n" +
			"router.get((context) => context.json({ b: environment.require(\"NEED_B\"), empty: environment.require(\"EMPTY\") }));\n",
	}
	root := environmentApp(t, files)
	tenant := NewTenant(root, "localhost")
	err := tenant.Run()
	tenant.Close()
	const want = "environment.require: thiếu EMPTY (orders/router.work:3:101), NEED_A (router.work:4:31), NEED_B (orders/router.work:3:63); khai trong .env của app hay của site, hay chia từ host bằng app.shareEnvironment trong application.work (KITWORK_ENVIRONMENT.md mục 3)"
	if err == nil || err.Error() != want {
		t.Fatalf("run = %v\nwant %s", err, want)
	}
	var missing *environmentMissing
	if !errors.As(err, &missing) || strings.Contains(err.Error(), "secret-value-xyz") || strings.Contains(err.Error(), "HAVE") {
		t.Fatalf("the report names a key that is there, or a value: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "acme", ".env"), []byte("HAVE=secret-value-xyz\nEMPTY=x\nNEED_A=a\nNEED_B=b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := environmentJSON(t, root, "/orders"); got["b"] != "b" || got["empty"] != "x" {
		t.Fatalf("with every key there: %v", got)
	}
}

// A .env in a branch of a site (shop.com/quan-ly/.env) is never read: the boot stops naming the folder and
// config.work, the place of configuration by branch (mục 1). The site's own .env, at its root, is read.
//
// CONTROL (22/10/2026): with RouteTree.Prepare not looking for a .env in a branch, the site booted and the
// branch's file was ignored without a word: "run = <nil>, want the error naming /quan-ly".
func TestBranchEnvIsABootError(t *testing.T) {
	root := environmentApp(t, map[string]string{
		"acme/localhost/.env":                "SITE=1\n",
		"acme/localhost/router.work":         environmentRoute(`{ site: environment.SITE }`),
		"acme/localhost/quan-ly/.env":        "ADMIN_ONLY=1\n",
		"acme/localhost/quan-ly/router.work": environmentRoute(`{ admin: environment.ADMIN_ONLY }`),
	})
	tenant := NewTenant(root, "localhost")
	err := tenant.Run()
	tenant.Close()
	const want = "thư mục /quan-ly: .env ở nhánh không được đọc; biến môi trường đặt ở .env của site hay của app, cấu hình theo nhánh đặt trong config.work (KITWORK_ENVIRONMENT.md mục 1)"
	if err == nil || err.Error() != want {
		t.Fatalf("run = %v\nwant %s", err, want)
	}
	if err := os.Remove(filepath.Join(root, "acme", "localhost", "quan-ly", ".env")); err != nil {
		t.Fatal(err)
	}
	if got := environmentJSON(t, root, "/"); got["site"] != float64(1) {
		t.Fatalf("the site's own .env: %v", got)
	}
}

// The keys application.work shares reach the app (mục 2): those for every app and those named for this one,
// never those named for another app, never a key of the host it did not share. The app's own .env wins over
// a key the host shares, and the boot says so by name, without a value.
//
// CONTROL (22/10/2026): with buildEnvironment not reading what the host shares, "got map[host:<nil> mine:<nil>
// override:app smtp:<nil> theirs:<nil>]"; with the shared keys laid over the app's .env, "override:host-secret-value".
func TestSharedEnvironmentReachesTheAppAndTheAppWins(t *testing.T) {
	var said []string
	var mu sync.Mutex
	saved := environmentSay
	environmentSay = func(line string) { mu.Lock(); said = append(said, line); mu.Unlock() }
	t.Cleanup(func() { environmentSay = saved })
	environmentOverrides.Delete("acme\x00OVERRIDE")

	root := environmentApp(t, map[string]string{
		"acme/.env": "OVERRIDE=app\n",
		"acme/localhost/router.work": environmentRoute(`{ smtp: environment.SMTP_URL, mine: environment.ONLY_ACME,
  theirs: environment.ONLY_OTHER, override: environment.OVERRIDE, host: environment.DB_PASSWORD }`),
	})
	shared := &SharedEnvironment{
		All: map[string]EnvironmentEntry{"SMTP_URL": {Text: "smtp://host"}, "OVERRIDE": {Text: "host-secret-value"}},
		Apps: map[string]map[string]EnvironmentEntry{
			"acme":  {"ONLY_ACME": {Text: "42"}},
			"other": {"ONLY_OTHER": {Text: "x"}},
		},
	}
	got := environmentJSON(t, root, "/", shared)
	want := map[string]any{"smtp": "smtp://host", "mine": float64(42), "theirs": nil, "override": "app", "host": nil}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %v\nwant %v", got, want)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(said) != 1 || said[0] != "[environment] app `acme` đè `OVERRIDE` host chia: .env của app thắng (KITWORK_ENVIRONMENT.md mục 2)" {
		t.Fatalf("the boot said %q", said)
	}
}

// A .env reached through a link is refused, never read: an app cannot point its .env at the host's or another
// app's. The app's .env as a link to a file (skipped where the system does not let a test make one, as Windows
// without the privilege), and the site's folder as a link to another app's site (a junction on such a Windows,
// which filepath.EvalSymlinks does not follow): both are read through an os.Root of the app's folder, which
// follows no absolute link, a junction always being one. So a site's folder linked to a folder of the same
// app is refused too when a .env is there behind it (said as such, not as a way out of the app), and boots as
// before when none is (22/10/2026).
//
// CONTROL (22/10/2026): with readEnvironmentFile opening the file by its machine path (os.Open, the link
// followed), the site linked to the other app's booted and read its secret: "run = <nil>, want …".
// CONTROL (23/10/2026): with a link on the way an error whether a .env is there or not (the 22/10 rule), the
// site linked inside the app with no .env stopped the boot: "run: environment: localhost/.env nằm sau một
// liên kết …".
func TestEnvironmentFileLinkedOutOfTheAppIsRefused(t *testing.T) {
	behindLink := func(relative string) string {
		return "environment: " + relative + " nằm sau một liên kết (symlink, junction); Kitwork không đọc .env qua liên kết, kể cả liên kết trỏ vào trong app: đặt file thật ở thư mục của app hay của site (KITWORK_ENVIRONMENT.md mục 1)"
	}
	t.Run("the site's folder", func(t *testing.T) {
		root := environmentApp(t, map[string]string{
			"victim-corp/victim.com/.env":        "NEIGHBOUR_SECRET=" + victimMarker + "\n",
			"victim-corp/victim.com/router.work": environmentRoute(`{ secret: environment.NEIGHBOUR_SECRET }`),
			"acme/.keep":                         "",
		})
		linkFolder(t, filepath.Join(root, "victim-corp", "victim.com"), filepath.Join(root, "acme", "localhost"))
		tenant := NewTenant(root, "localhost")
		err := tenant.Run()
		tenant.Close()
		want := behindLink("localhost/.env")
		if err == nil || err.Error() != want || strings.Contains(err.Error(), victimMarker) {
			t.Fatalf("run = %v, want %q", err, want)
		}
	})
	t.Run("the site's folder linked inside the app", func(t *testing.T) {
		root := environmentApp(t, map[string]string{
			"acme/.env":                   "FROM_APP=app\n",
			"acme/real-site/router.work":  environmentRoute(`{ app: environment.FROM_APP, site: environment.FROM_SITE }`),
			"acme/other-site/router.work": environmentRoute(`{ site: environment.FROM_SITE }`),
			"acme/other-site/.env":        "FROM_SITE=site\n",
		})
		linkFolder(t, filepath.Join(root, "acme", "real-site"), filepath.Join(root, "acme", "localhost"))
		if got := environmentJSON(t, root, "/"); got["app"] != "app" || got["site"] != nil {
			t.Fatalf("a site linked inside the app with no .env: %v, want the app's key and no site's", got)
		}
		if err := os.Remove(filepath.Join(root, "acme", "localhost")); err != nil {
			t.Fatal(err)
		}
		linkFolder(t, filepath.Join(root, "acme", "other-site"), filepath.Join(root, "acme", "localhost"))
		tenant := NewTenant(root, "localhost")
		err := tenant.Run()
		tenant.Close()
		if want := behindLink("localhost/.env"); err == nil || err.Error() != want {
			t.Fatalf("run = %v, want %q", err, want)
		}
	})
	t.Run("the app's .env", func(t *testing.T) {
		root := environmentApp(t, map[string]string{
			".env":                       "HOST_SECRET=leaked\n",
			"acme/localhost/router.work": environmentRoute(`{ secret: environment.HOST_SECRET }`),
		})
		if err := os.Symlink(filepath.Join(root, ".env"), filepath.Join(root, "acme", ".env")); err != nil {
			t.Skipf("symlink to a file unavailable: %v", err)
		}
		tenant := NewTenant(root, "localhost")
		err := tenant.Run()
		tenant.Close()
		if want := behindLink(".env"); err == nil || err.Error() != want || strings.Contains(err.Error(), "leaked") {
			t.Fatalf("run = %v, want %q", err, want)
		}
	})
}

// A const of a router.work's top level that holds a key not named PUBLIC_… is the server's: the page of a
// router.view in the file never reads it, while a PUBLIC_ key, and a yes/no made of the secret, are the
// page's (mục 6). A handler of the file still uses the secret.
//
// CONTROL (22/10/2026): with convertViewScope not leaving out EnvironmentServerOnly, the page printed the
// secret: "the page shows the secret: …sk-live-123…".
func TestEnvironmentSecretOfTheFileIsNotPageData(t *testing.T) {
	root := environmentApp(t, map[string]string{
		"acme/.env":                 "STRIPE_SECRET=sk-live-123\nPUBLIC_GREETING=Xin chào\n",
		"acme/localhost/view.ui":    "<view><content /></view>",
		"acme/localhost/content.ui": "<content><text id=\"page\">[{{ greeting }}] [{{ stripe }}] [{{ apiKeyUnknown }}] [{{ secretSet }}]</text></content>",
		"acme/localhost/router.work": "import * as router from \"kitwork/router\";\nimport { environment } from \"kitwork\";\n" +
			"const stripe = environment.require(\"STRIPE_SECRET\");\nconst greeting = environment.PUBLIC_GREETING || \"\";\n" +
			"router.view((context) => {\n  const secretSet = stripe.length > 0;\n});\n",
		"acme/localhost/charge/router.work": "import * as router from \"kitwork/router\";\nimport { environment } from \"kitwork\";\n" +
			"const stripe = environment.require(\"STRIPE_SECRET\");\nrouter.get((context) => context.json({ prefix: stripe.slice(0, 7) }));\n",
	})
	tenant := NewTenant(root, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	defer tenant.Close()
	page := serveIsolation(t, tenant, "http://localhost/").Body.String()
	if strings.Contains(page, "sk-live-123") {
		t.Fatalf("the page shows the secret: %.1500s", page)
	}
	// stripe reads as a name the page was never given does
	start := strings.Index(page, `<p id="page">`)
	end := strings.Index(page[max(start, 0):], "</p>")
	if start < 0 || end < 0 {
		t.Fatalf("no page text: %.1500s", page)
	}
	text := page[start+len(`<p id="page">`) : start+end]
	parts := strings.Split(text, "] [")
	if len(parts) != 4 || parts[0] != "[Xin chào" || parts[1] != parts[2] || parts[3] != "true]" {
		t.Fatalf("the page = %q, want the public key, stripe read as a name never given, and the yes/no", text)
	}
	if charge := serveIsolation(t, tenant, "http://localhost/charge").Body.String(); charge != `{"prefix":"sk-live"}` {
		t.Fatalf("/charge = %s, want the handler to use the secret", charge)
	}
}

// A secret reaches a router.view's page by no other way of writing it (mục 6, 22/10/2026): a const imported
// from a module of the app that holds one, and a let of the file's top level, are the server's as a const of
// the file is, while the PUBLIC_ key the module exports beside it is the page's; a view's function passed by
// its name with a variable that holds a secret inside an object stops the boot, naming the key.
//
// CONTROL (23/10/2026): with holdEnvironment reading nothing once a declaration is resolved and a let held as
// nothing, the page was handed them: "the page shows a secret: <!doctype html>…"; with viewFunction not
// following a name, the site booted: "run = <nil>, want \"router.work:4:9: khoá `TOKEN` sẽ đi xuống trình duyệt\"".
func TestEnvironmentSecretOfAModuleOrALetIsNotPageData(t *testing.T) {
	root := environmentApp(t, map[string]string{
		"acme/.env":                  "STRIPE_SECRET=sk-live-123\nTOKEN=tok-456\nPUBLIC_GREETING=Xin chào\n",
		"acme/.shared/keys.work":     "import { environment } from \"kitwork\";\nexport const stripe = environment.STRIPE_SECRET;\nexport const greeting = environment.PUBLIC_GREETING || \"\";\n",
		"acme/localhost/view.ui":     "<view><content /></view>",
		"acme/localhost/content.ui":  "<content><text id=\"page\">[{{ greeting }}] [{{ stripe }}] [{{ token }}] [{{ apiKeyUnknown }}]</text></content>",
		"acme/localhost/router.work": "import * as router from \"kitwork/router\";\nimport { environment } from \"kitwork\";\nimport { stripe, greeting } from \"app/.shared/keys.work\";\nlet token = environment.TOKEN;\nrouter.view((context) => { });\n",
	})
	tenant := NewTenant(root, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	page := serveIsolation(t, tenant, "http://localhost/").Body.String()
	tenant.Close()
	if strings.Contains(page, "sk-live-123") || strings.Contains(page, "tok-456") {
		t.Fatalf("the page shows a secret: %.1500s", page)
	}
	start := strings.Index(page, `<p id="page">`)
	end := strings.Index(page[max(start, 0):], "</p>")
	if start < 0 || end < 0 {
		t.Fatalf("no page text: %.1500s", page)
	}
	text := page[start+len(`<p id="page">`) : start+end]
	parts := strings.Split(text, "] [")
	// stripe and token read as apiKeyUnknown, a name the page was never given, does
	if len(parts) != 4 || parts[0] != "[Xin chào" || parts[1] != parts[2] || parts[2]+"]" != parts[3] {
		t.Fatalf("the page = %q, want the public key, then stripe and token read as names never given", text)
	}

	byName := environmentApp(t, map[string]string{
		"acme/.env":                 "TOKEN=tok-456\n",
		"acme/localhost/view.ui":    "<view><content /></view>",
		"acme/localhost/content.ui": "<content><text>{{ data }}</text></content>",
		"acme/localhost/router.work": "import * as router from \"kitwork/router\";\nimport { environment } from \"kitwork\";\n" +
			"const page = (context) => {\n  const data = { token: environment.TOKEN };\n};\nrouter.view(page);\n",
	})
	tenant = NewTenant(byName, "localhost")
	err := tenant.Run()
	tenant.Close()
	if want := "router.work:4:9: khoá `TOKEN` sẽ đi xuống trình duyệt"; err == nil || !strings.Contains(err.Error(), want) || strings.Contains(err.Error(), "tok-456") {
		t.Fatalf("run = %v, want %q", err, want)
	}
}
