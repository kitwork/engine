package compiler

import (
	"encoding/hex"
	"path/filepath"
	"strings"
	"testing"
)

// x.safe() is gone (14/10/2026): it is a compile error, with file:line:column at the name safe, that
// quotes the receiver in the new spelling, so the fix is a copy. The error stays for good: the old
// spelling lives on in documents, reports and the memory of every AI that read them.
func TestRemovedSafeMethodSaysHowToWriteIt(t *testing.T) {
	for _, c := range []struct{ source, want string }{
		{"const r = db.users.first().safe();",
			"<source>:1:28: .safe() đã bỏ (14/10/2026): viết safe(() => db.users.first()), nhận { value, error }"},
		// the receiver of a chain written one call a line is one line, without the breaks
		{"const rows = db.table(\"article\")\n    .where(\"visible\", \"public\")\n    .list(200)\n    .safe();",
			"<source>:4:6: .safe() đã bỏ (14/10/2026): viết safe(() => db.table(\"article\").where(\"visible\", \"public\").list(200)), nhận { value, error }"},
		// an object receiver is parenthesized, as an arrow returning an object must be
		{"const r = { answer: 42 }.safe();",
			"<source>:1:26: .safe() đã bỏ (14/10/2026): viết safe(() => ({ answer: 42 })), nhận { value, error }"},
		// a parenthesized receiver keeps its parentheses
		{"const r = (a + b).safe();",
			"<source>:1:19: .safe() đã bỏ (14/10/2026): viết safe(() => (a + b)), nhận { value, error }"},
	} {
		_, err := CompileSource(c.source)
		if err == nil {
			t.Errorf("%q compiled", c.source)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q:\n got %v\nwant %s", c.source, err, c.want)
		}
	}
	// columns count characters: the Vietnamese text before it does not move the place
	_, err := CompileSource("const r = f(\"Phường\").safe();")
	if err == nil || !strings.Contains(err.Error(), "<source>:1:23: .safe() đã bỏ") {
		t.Fatalf("a place after Vietnamese text = %v, want column 23 (characters; bytes would be 25)", err)
	}
}

// A receiver longer than 100 characters keeps its head (to a space) and its tail (from a dot): the
// error of vietnamese.kitwork.js's .list(200) chain, 212 characters, as KITWORK_LANG.md quotes it.
func TestRemovedSafeMethodShortensALongReceiver(t *testing.T) {
	source := `const rows = db.table("article")
    .select("slug", "name", "description", "created_at", "updated_at", "images", "language", "deleted_at")
    .where("visible", "public")
    .where("language", "vi")
    .orderBy("created_at", "desc")
    .list(200)
    .safe();`
	_, err := CompileSource(source)
	want := `viết safe(() => db.table("article").select("slug", "name", "description", …orderBy("created_at", "desc").list(200)), nhận { value, error }`
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("long receiver = %v\nwant %s", err, want)
	}
}

// With an argument it is an ordinary call of a method named safe: an object's own function answers it,
// and through a bracket o["safe"]() calls it too. Neither is the global.
func TestSafeWithAnArgumentIsAMethodCall(t *testing.T) {
	if got, want := runOwnKey(t, "const o = { safe: (x) => \"own:\" + x }\nconst result = [o.safe(1), o[\"safe\"](2)]", nil), `["own:1","own:2"]`; got != want {
		t.Fatalf("own safe = %s, want %s", got, want)
	}
}

// safe(<what is certainly not a function>) is a compile error that quotes what to write: at run time
// the argument runs before safe is called, so if it fails the request ends with its error and nothing
// says the arrow was missing. A call of safe without exactly one argument is the same error.
func TestSafeOfWhatIsNotAFunctionIsACompileError(t *testing.T) {
	for _, c := range []struct{ source, want string }{
		{"const r = safe(findUser(id));", "<source>:1:11: safe nhận một hàm: viết safe(() => findUser(id))"},
		{"const r = safe(db.users\n  .where(\"id\", 1)\n  .first());", "<source>:1:11: safe nhận một hàm: viết safe(() => db.users.where(\"id\", 1).first())"},
		{"const r = safe({ a: 1 });", "<source>:1:11: safe nhận một hàm: viết safe(() => ({ a: 1 }))"},
		{"const r = safe([1, 2]);", "safe nhận một hàm: viết safe(() => [1, 2])"},
		{"const r = safe(\"x\");", "safe nhận một hàm: viết safe(() => \"x\")"},
		{"const r = safe(1 + 2);", "safe nhận một hàm: viết safe(() => 1 + 2)"},
		{"const r = safe(!ready);", "safe nhận một hàm: viết safe(() => !ready)"},
		{"const r = safe(o?.load());", "safe nhận một hàm: viết safe(() => o?.load())"},
		{"const r = safe();", "<source>:1:11: safe nhận một hàm: viết safe(() => …)"},
		{"const r = safe(() => 1, 2);", "<source>:1:11: safe nhận đúng một hàm, không nhận 2 đối số: viết safe(() => …)"},
		{"const f = () => {\n  return safe(load());\n};", "<source>:2:10: safe nhận một hàm: viết safe(() => load())"},
	} {
		_, err := CompileSource(c.source)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q:\n got %v\nwant %s", c.source, err, c.want)
		}
	}
	// what may hold a function is checked when it runs: a name, a member, a ternary, a logical
	for _, allowed := range []string{
		"const r = safe(() => 1);",
		"const load = () => 1; const r = safe(load);",
		"const o = { load: () => 1 }; const r = safe(o.load);",
		"const o = { load: () => 1 }; const r = safe(o[\"load\"]);",
		"const c = true; const f = () => 1; const r = safe(c ? f : f);",
		"const f = null; const g = () => 1; const r = safe(f || g);",
		"const r = safe(function () { return 1; });",
	} {
		if _, err := CompileSource(allowed); err != nil {
			t.Errorf("%q: %v", allowed, err)
		}
	}
}

// The check is off when the bundle declares or assigns a name safe anywhere: that safe is the
// author's own, as any global can be shadowed (const fail = 3 runs). So is a safe imported from
// another module, or another export of "kitwork" imported as safe. import { safe } from "kitwork" is
// not: TestImportingTheGlobalSafeKeepsTheCheck.
func TestTheBundlesOwnSafeTurnsTheCheckOff(t *testing.T) {
	for _, source := range []string{
		"const safe = (x) => x; const r = safe(load());",
		"const f = (safe) => safe(load()); const r = f((x) => x);",
		"safe = (x) => x; const r = safe(load());",
		"import { fail as safe } from \"kitwork\"; const r = safe(load());",
		"import { safe } from \"kitwork/http\"; const r = safe(load());",
		"import { safe, router } from \"kitwork\"; const g = () => { const safe = (x) => x; return safe(1); }; const r = safe(load());",
		"const g = () => { let safe = 1; return safe; }; const r = safe(load());",
	} {
		if _, err := CompileSource(source); err != nil {
			t.Errorf("%q: %v, want the author's own safe to compile", source, err)
		}
	}
	// a module of the bundle that declares safe turns it off for the entry as well
	entry := writeTenant(t, map[string]string{
		"app.kitwork.js":  "import { wrap } from \"./lib.kitwork.js\";\nconst r = safe(load());\n",
		"lib.kitwork.js":  "const safe = (x) => x;\nexport const wrap = (x) => safe(x);\n",
		"good.kitwork.js": "const r = safe(load());\n",
	})
	if _, err := CompileFile(entry); err != nil {
		t.Fatalf("a module declaring safe: %v", err)
	}
	if _, err := CompileFile(filepath.Join(filepath.Dir(entry), "good.kitwork.js")); err == nil ||
		!strings.Contains(err.Error(), "good.kitwork.js:1:11: safe nhận một hàm") {
		t.Fatalf("control, a bundle with no safe of its own: %v", err)
	}
}

// import { safe } from "kitwork" binds the global safe itself (kitwork().safe), not a safe of the
// bundle's own, so safe(<not a function>) stays the compile error after it (14/10/2026; the import turned
// the check off for the whole bundle). Every form of that import: alone, beside others, beside an
// aliased one, and in a module the entry imports. CONTROL: a function given to it compiles.
func TestImportingTheGlobalSafeKeepsTheCheck(t *testing.T) {
	for _, c := range []struct{ source, at string }{
		{"import { safe } from \"kitwork\";\nconst r = safe(load());", "<source>:2:11: safe nhận một hàm: viết safe(() => load())"},
		{"import { router, safe } from \"kitwork\";\nconst r = safe(1);", "<source>:2:11: safe nhận một hàm: viết safe(() => 1)"},
		{"import { safe, fail as stop } from \"kitwork\";\nconst r = safe({ a: 1 });", "<source>:2:11: safe nhận một hàm: viết safe(() => ({ a: 1 }))"},
	} {
		if _, err := CompileSource(c.source); err == nil || !strings.Contains(err.Error(), c.at) {
			t.Errorf("%q: %v, want %s", c.source, err, c.at)
		}
	}
	entry := writeTenant(t, map[string]string{
		"app.kitwork.js": "import { wrap } from \"./lib.kitwork.js\";\nconst r = safe(load());\n",
		"lib.kitwork.js": "import { safe } from \"kitwork\";\nexport const wrap = (f) => safe(f);\n",
	})
	if _, err := CompileFile(entry); err == nil || !strings.Contains(err.Error(), "app.kitwork.js:2:11: safe nhận một hàm") {
		t.Fatalf("a module importing the global safe: %v, want the check on in the entry", err)
	}
	if _, err := CompileSource("import { safe } from \"kitwork\";\nconst r = safe(() => load());"); err != nil {
		t.Fatalf("CONTROL: safe(() => …) after the import: %v", err)
	}
}

// A file a bundle cannot name relative to its entry's folder is named by its file name alone in the
// debug table, so an error's at never shows the machine's path.
func TestASourceNameIsNeverAnAbsolutePath(t *testing.T) {
	b := &bundler{debugRoot: t.TempDir()}
	if got := b.sourceName(filepath.Join("relative", "dir", "store.kitwork.js")); got != "store.kitwork.js" {
		t.Fatalf("sourceName = %q, want the file's name", got)
	}
	inside := filepath.Join(b.debugRoot, "_core", "users.kitwork.js")
	if got := b.sourceName(inside); got != "_core/users.kitwork.js" {
		t.Fatalf("control: a file under the entry's folder = %q", got)
	}
}

// Schema 7 retires every artifact cached by schema 6: those still hold x.safe()'s evaluator lambda,
// which nothing runs any more.
func TestSchema7RetiresSchema6Artifacts(t *testing.T) {
	const schema6 = "d8993ae852e0dbcd87c55b8c8fd73f5bb272d4b58728581875a21521a14e7081"
	stamped := bytecode2FingerprintFor(6)
	if got := hex.EncodeToString(stamped[:]); got != schema6 {
		t.Fatalf("schema 6 on bytecode 2 stamps %s, want %s", got, schema6)
	}
	if Fingerprint() == schema6 {
		t.Fatal("the compiler still stamps the schema-6 fingerprint")
	}
}
