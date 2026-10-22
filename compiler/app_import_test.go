package compiler

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
)

// writeApps writes files under a fresh root (paths with "/") and gives the root.
func writeApps(t *testing.T, files map[string]string) string {
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

// runAppProgram runs a compiled program with globals and gives its `result` variable.
func runAppProgram(t *testing.T, bytecode *Bytecode, globals ...map[string]value.Value) string {
	t.Helper()
	vm := runtime.New(bytecode.Program)
	vm.MaxEnergy = 10_000_000
	for _, set := range globals {
		for name, item := range set {
			vm.Globals[name] = item
		}
	}
	if returned := vm.Run(); returned.IsInvalid() {
		t.Fatalf("run: %s", returned.Text())
	}
	return vm.Vars["result"].Text()
}

// An `app/…` import resolves from the folder of the app the file belongs to (22/10/2026, KITWORK_UI.md
// mục 4): written the same in every folder, in every import form, from a module too, and never shadowed by
// a site's own folder of the same name (the `_core` walk-up would have taken the site's copy).
func TestAppImportResolvesFromTheAppRoot(t *testing.T) {
	root := writeApps(t, map[string]string{
		"apps/acme/.shared/demo.work":    `import { label } from "app/.shared/label.work";` + "\n" + `export const listDemos = () => label + ":http";`,
		"apps/acme/.shared/label.work":   `export const label = "acme";`,
		"apps/acme/.shared/default.work": `export default "mặc định";`,
		"apps/acme/.shared/effect.work":  `mark("chạy");`,
		// the site's own .shared must not shadow the app's
		"apps/acme/shop.com/.shared/demo.work": `export const listDemos = () => "site";`,
		"apps/acme/shop.com/blog/router.work": `import { listDemos } from "app/.shared/demo.work";
import fallback from "app/.shared/default.work";
import "app/.shared/effect.work";
var result = [listDemos(), fallback].join("|");`,
		// another app with the same names: its files are never reached from acme
		"apps/other/.shared/demo.work":  `export const listDemos = () => "other";`,
		"apps/other/.shared/label.work": `export const label = "other";`,
	})
	entry := filepath.Join(root, "apps", "acme", "shop.com", "blog", "router.work")
	bytecode, err := CompileFileInApp(filepath.Join(root, "apps", "acme"), entry)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	marks := []string{}
	mark := value.NewFunc(func(args ...value.Value) value.Value {
		marks = append(marks, args[0].Text())
		return value.Value{K: value.Nil}
	})
	if got, want := runAppProgram(t, bytecode, map[string]value.Value{"mark": mark}), "acme:http|mặc định"; got != want {
		t.Fatalf("result = %q, want %q", got, want)
	}
	// the side-effect import ran its module, once
	if strings.Join(marks, ",") != "chạy" {
		t.Fatalf("marks = %v, want the side-effect module run once", marks)
	}
	// hot reload watches the app's files the import reached
	want := filepath.Join(root, "apps", "acme", ".shared", "demo.work")
	found := false
	for _, file := range bytecode.Files {
		found = found || file == want
	}
	if !found {
		t.Fatalf("Files = %v, want %s among them", bytecode.Files, want)
	}
}

// What is wrong with an app/… path as written is a compile error at the specifier, in every form of
// import, before anything is read from the disk.
func TestAppImportRefusesAPathOutOfTheApp(t *testing.T) {
	cases := []struct{ spec, want string }{
		{`app/../other/.shared/demo.work`, `ra ngoài lối của app/`},
		{`app/.shared/../../other/x.work`, `ra ngoài lối của app/`},
		{`app/./x.work`, `ra ngoài lối của app/`},
		{`app//etc/passwd`, `không phải đường dẫn tuyệt đối`},
		{`app/C:/Windows/x.work`, `không phải đường dẫn tuyệt đối`},
		{`app\.shared\x.work`, `đường dẫn viết bằng "/"`},
		{`app/.database/files/notes.work`, `.database/ là dữ liệu của app, không import được`},
		{`app/.Database/application/x.work`, `.database/ là dữ liệu của app, không import được`},
		{`app/.database./files/notes.work`, `.database/ là dữ liệu của app, không import được`},
		{`app/.shared/.../x.work`, `ra ngoài lối của app/`},
		{`app/`, `sau app/ là đường dẫn của một file từ gốc app`},
		{`app`, `sau app/ là đường dẫn của một file từ gốc app`},
		{`app/.shared//x.work`, `mỗi đoạn giữa hai "/"`},
	}
	for _, c := range cases {
		quoted := strings.ReplaceAll(c.spec, `\`, `\\`)
		for _, source := range []string{
			`import { x } from "` + quoted + `";`,
			`import x from "` + quoted + `";`,
			`import "` + quoted + `";`,
		} {
			_, p := parseSrc(source)
			joined := strings.Join(p.Errors(), "\n")
			if !strings.Contains(joined, c.want) {
				t.Errorf("%s\n  errors %q, want %q", source, joined, c.want)
			}
			// at the specifier's string, line 1
			at := strings.Index(source, `"`) + 1
			if prefix := "1:" + strconv.Itoa(at) + ": import "; !strings.HasPrefix(joined, prefix) {
				t.Errorf("%s\n  errors %q, want them at %q", source, joined, prefix)
			}
		}
	}
	// the file's name leads, as every compile error's place does
	root := writeApps(t, map[string]string{
		"apps/acme/shop.com/router.work": "const a = 1;\nimport { db } from \"app/../other/.shared/schema/shop.work\";",
	})
	_, err := CompileFileInApp(filepath.Join(root, "apps", "acme"), filepath.Join(root, "apps", "acme", "shop.com", "router.work"))
	if err == nil || !strings.Contains(err.Error(), `router.work:2:20: import "app/../other/.shared/schema/shop.work" ra ngoài lối của app/`) {
		t.Fatalf("compile error = %v", err)
	}
}

// A file of no app (the host's application.work, CompileFile) has no app/: the import is a compile error
// that says why, where it stands; a file the app does not have is one too, named from the app's folder,
// never the machine's path.
func TestAppImportNeedsAnAppAndAFile(t *testing.T) {
	root := writeApps(t, map[string]string{
		"application.work":                   "import * as app from \"kitwork/application\";\nimport { port } from \"app/.shared/port.work\";",
		"apps/acme/.shared/port.work":        `export const port = 8080;`,
		"apps/acme/shop.com/router.work":     `import { listDemos } from "app/.shared/nope.work";`,
		"apps/acme/.shared/indirect.work":    "\n" + `import { gone } from "app/.shared/gone.work";` + "\nexport const x = 1;",
		"apps/acme/shop.com/api/router.work": `import { x } from "app/.shared/indirect.work";`,
	})
	_, err := CompileFile(filepath.Join(root, "application.work"))
	want := `application.work:2:22: import "app/.shared/port.work": file này không thuộc app nào nên không có app/`
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("no app: error = %v, want %q", err, want)
	}

	app := filepath.Join(root, "apps", "acme")
	_, err = CompileFileInApp(app, filepath.Join(app, "shop.com", "router.work"))
	want = `router.work:1:27: import "app/.shared/nope.work": app không có file .shared/nope.work`
	if err == nil || !strings.Contains(err.Error(), want) || strings.Contains(err.Error(), root) {
		t.Fatalf("missing file: error = %v, want %q and no machine path", err, want)
	}
	// an import of a module is placed in that module, by its name from the entry's folder
	_, err = CompileFileInApp(app, filepath.Join(app, "shop.com", "api", "router.work"))
	want = `../../.shared/indirect.work:2:22: import "app/.shared/gone.work": app không có file .shared/gone.work`
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("missing file in a module: error = %v, want %q", err, want)
	}
}

// Nothing imports from an app's data, whatever the spelling (KITWORK_UI.md mục 4): a relative path into this
// app's .database/, another app's, or the folder as a side-effect import is refused like app/.database/. A
// relative path into another app's code still compiles (an engine gap the spec records).
func TestRelativeImportIntoTheDataIsRefused(t *testing.T) {
	root := writeApps(t, map[string]string{
		"apps/acme/.database/files/notes.work":  `export const notes = "data";`,
		"apps/other/.database/files/notes.work": `export const notes = "other data";`,
		"apps/other/.shared/x.work":             `export const a = 1;`,
		"apps/acme/shop.com/own/router.work":    "const x = 1;\nimport { notes } from \"../../.database/files/notes.work\";",
		"apps/acme/shop.com/beside/router.work": `import { notes } from "../../../other/.database/files/notes.work";`,
		"apps/acme/shop.com/folder/router.work": `import "../../.database/files";`,
		"apps/acme/shop.com/dotted/router.work": `import { notes } from "../../.database./files/notes.work";`,
		"apps/acme/shop.com/code/router.work":   `import { a } from "../../../other/.shared/x.work";`,
	})
	app := filepath.Join(root, "apps", "acme")
	refused := `: .database/ là dữ liệu của app, không import được; code dùng chung đặt trong .shared/`
	cases := []struct{ folder, want string }{
		{"own", `router.work:2:23: import "../../.database/files/notes.work"` + refused},
		{"beside", `router.work:1:23: import "../../../other/.database/files/notes.work"` + refused},
		{"folder", `router.work:1:8: import "../../.database/files"` + refused},
	}
	if goruntime.GOOS == "windows" {
		// Windows opens .database. as .database; elsewhere it is another folder, which is not there
		cases = append(cases, struct{ folder, want string }{"dotted", `router.work:1:23: import "../../.database./files/notes.work"` + refused})
	}
	for _, c := range cases {
		_, err := CompileFileInApp(app, filepath.Join(app, "shop.com", c.folder, "router.work"))
		if err == nil || err.Error() != c.want {
			t.Errorf("%s:\n  error %v\n  want  %s", c.folder, err, c.want)
		}
	}
	if _, err := CompileFileInApp(app, filepath.Join(app, "shop.com", "code", "router.work")); err != nil {
		t.Fatalf("relative import of another app's code: %v", err)
	}
}

// A named import of a folder, app/ or relative, is a compile error at the specifier in the author's words,
// with the two forms that work: a file of the folder, or the folder imported only to run.
func TestImportOfAFolderByNameSaysWhatWorks(t *testing.T) {
	root := writeApps(t, map[string]string{
		"apps/acme/.shared/a.work":             `export const a = 1;`,
		"apps/acme/shop.com/router.work":       "const x = 1;\nimport { a } from \"app/.shared\";",
		"apps/acme/shop.com/lib/a.work":        `export const a = 1;`,
		"apps/acme/shop.com/local/router.work": `import { a } from "../lib";`,
	})
	app := filepath.Join(root, "apps", "acme")
	for _, c := range []struct{ entry, want string }{
		{filepath.Join(app, "shop.com", "router.work"),
			`router.work:2:19: import "app/.shared" là một thư mục, không có gì để lấy theo tên: lấy từ một file trong đó ("app/.shared/x.work"), hay viết import "app/.shared" không tên để chạy cả thư mục`},
		{filepath.Join(app, "shop.com", "local", "router.work"),
			`router.work:1:19: import "../lib" là một thư mục, không có gì để lấy theo tên: lấy từ một file trong đó ("../lib/x.work"), hay viết import "../lib" không tên để chạy cả thư mục`},
	} {
		_, err := CompileFileInApp(app, c.entry)
		if err == nil || err.Error() != c.want {
			t.Errorf("%s:\n  error %v\n  want  %s", c.entry, err, c.want)
		}
	}
}

// The disk has the last word: a link inside the app that leads out of it, or into its data, is refused
// like the path written that way. A link Go cannot follow (a junction on Windows, made where links need a
// right this machine lacks) is refused too, saying so.
func TestAppImportRefusesALinkOutOfTheApp(t *testing.T) {
	root := writeApps(t, map[string]string{
		"apps/other/.shared/secret.work":       `export const secret = "other";`,
		"apps/acme/.database/files/notes.work": `export const notes = "data";`,
		"apps/acme/shop.com/router.work":       `import { secret } from "app/.shared/out/secret.work";`,
		"apps/acme/shop.com/notes/router.work": `import { notes } from "app/.shared/data/notes.work";`,
	})
	app := filepath.Join(root, "apps", "acme")
	if err := os.MkdirAll(filepath.Join(app, ".shared"), 0o755); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(app, ".shared", "out")
	data := filepath.Join(app, ".shared", "data")
	linkFolder(t, filepath.Join(root, "apps", "other", ".shared"), out)
	linkFolder(t, filepath.Join(app, ".database", "files"), data)

	unfollowable := `không lần được đường tới file qua liên kết`
	cases := []struct{ entry, through, want string }{
		{filepath.Join(app, "shop.com", "router.work"), filepath.Join(out, "secret.work"), `import "app/.shared/out/secret.work" ra ngoài app`},
		{filepath.Join(app, "shop.com", "notes", "router.work"), filepath.Join(data, "notes.work"), `import "app/.shared/data/notes.work": .database/ là dữ liệu của app`},
	}
	for _, c := range cases {
		want := c.want
		if _, err := filepath.EvalSymlinks(c.through); err != nil {
			want = unfollowable
		}
		_, err := CompileFileInApp(app, c.entry)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: error = %v, want %q", c.entry, err, want)
		}
	}
}

// linkFolder makes link lead to the folder target: a symbolic link, or on Windows without the right to
// make one a junction, which needs none.
func linkFolder(t *testing.T, target, link string) {
	t.Helper()
	err := os.Symlink(target, link)
	if err != nil && goruntime.GOOS == "windows" {
		output, junctionErr := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput()
		if junctionErr == nil {
			return
		}
		err = fmt.Errorf("%v; junction: %v %s", err, junctionErr, output)
	}
	if err != nil {
		t.Skipf("this machine does not make links: %v", err)
	}
}

// import * is for Kitwork's modules only (19/10/2026), app/ included: the error gives the named form. Open,
// waiting on the owner (KITWORK_UI.md mục 4): whether import * opens for an app's files. This pins today's
// rule so it does not change by accident; a decision to open it replaces this test.
func TestAppImportNamespaceGivesTheNamedForm(t *testing.T) {
	_, p := parseSrc(`import * as demo from "app/.shared/demo.work";`)
	joined := strings.Join(p.Errors(), "\n")
	if !strings.Contains(joined, `import { a, b } from "app/.shared/demo.work"`) {
		t.Fatalf("errors %q, want the named form", joined)
	}
}

// A place the native path does not know is said in the author's words, at the specifier, with the three
// that work.
func TestUnsupportedSpecifierSaysWhatWorks(t *testing.T) {
	for _, source := range []string{`import { x } from "lib";`, `import x from "lib";`, `import "lib";`} {
		_, p := parseSrc(source)
		joined := strings.Join(p.Errors(), "\n")
		at := strings.Index(source, `"`) + 1
		want := "1:" + strconv.Itoa(at) + `: import "lib": chỉ import được module của Kitwork ("kitwork", "kitwork/router"), file từ gốc app ("app/.shared/x.work") hay file tương đối ("./x.work")`
		if joined != want {
			t.Errorf("%s\n  errors %q\n  want   %q", source, joined, want)
		}
	}
}

// One cache directory serves every app of a host, and its artifact's name carries the app's folder: two
// apps with the same sources each get their own, and a hit hands back the files of the app that asked.
func TestFileCacheKeysOnTheAppRoot(t *testing.T) {
	same := map[string]string{
		".shared/demo.work":    `export const listDemos = () => "demo";`,
		"shop.com/router.work": `import { listDemos } from "app/.shared/demo.work";` + "\nvar result = listDemos();",
	}
	files := map[string]string{}
	for _, app := range []string{"acme", "other"} {
		for relative, content := range same {
			files["apps/"+app+"/"+relative] = content
		}
	}
	root := writeApps(t, files)
	cacheDirectory := filepath.Join(root, "cache")
	cache := NewFileCache(cacheDirectory)
	for _, app := range []string{"acme", "other", "acme"} {
		appRoot := filepath.Join(root, "apps", app)
		bytecode, err := cache.CompileFileInApp(appRoot, filepath.Join(appRoot, "shop.com", "router.work"))
		if err != nil {
			t.Fatalf("%s: %v", app, err)
		}
		// the artifact is where CacheKeyInApp says, the name a caller (the release gate's campaigns,
		// inspect) reads it under; CacheKey, the program's own, names no artifact of an app
		if _, err := os.Stat(filepath.Join(cacheDirectory, bytecode.CacheKeyInApp(appRoot)+".kwbc")); err != nil {
			t.Fatalf("%s: artifact under CacheKeyInApp: %v", app, err)
		}
		if bytecode.CacheKeyInApp(appRoot) == bytecode.CacheKey() || bytecode.CacheKeyInApp("") != bytecode.CacheKey() {
			t.Fatalf("%s: CacheKeyInApp(app) = %q, CacheKeyInApp(\"\") = %q, CacheKey = %q", app,
				bytecode.CacheKeyInApp(appRoot), bytecode.CacheKeyInApp(""), bytecode.CacheKey())
		}
		if got := runAppProgram(t, bytecode); got != "demo" {
			t.Fatalf("%s: result = %q", app, got)
		}
		module := filepath.Join(appRoot, ".shared", "demo.work")
		if len(bytecode.Files) != 2 || bytecode.Files[1] != module {
			t.Fatalf("%s: Files = %v, want the entry and %s", app, bytecode.Files, module)
		}
	}
	artifacts, err := filepath.Glob(filepath.Join(cacheDirectory, "*.kwbc"))
	if err != nil {
		t.Fatal(err)
	}
	if len(artifacts) != 2 {
		t.Fatalf("artifacts = %d, want one for each app's folder", len(artifacts))
	}
}
