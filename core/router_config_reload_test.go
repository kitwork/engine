package core

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A config.work is a source of its site's generation like router.work (KITWORK_ROUTER.md mục 4, 15;
// 24/10/2026): one appearing, changing or going in a folder builds the generation again with it, and so does
// the app's config.work at the app's folder for every site of the app. One that does not run leaves the
// generation that serves as it is: the old one keeps serving.
//
// CONTROL (go test -overlay, 24/10/2026): with compileUIFolder not running config.work, /a answered 200 "a" once
// the guard appeared; with the app's config.work not watched, its appearing built no new generation.
func TestEngineReloadsConfigWork(t *testing.T) {
	root := t.TempDir()
	write := func(relative, content string) {
		t.Helper()
		filename := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const routerImport = "import * as router from \"kitwork/router\";\n"
	write("acme/localhost/router.work", routerImport+"router.get((context) => context.text(\"root\"));\n")
	write("acme/localhost/a/router.work", routerImport+"router.get((context) => context.text(\"a\"));\n")

	engine := New(root, 0, true, "")
	t.Cleanup(engine.Close)
	request := func(want string) {
		t.Helper()
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "http://localhost/a", nil))
		if got := recorder.Body.String(); got != want {
			t.Fatalf("GET /a = %d %q, want %q", recorder.Code, got, want)
		}
	}
	wait := func() { time.Sleep(1100 * time.Millisecond) } // the engine reads the sources at most once a second
	request("a")

	write("acme/localhost/a/config.work", routerImport+"router.guard((context) => context.status(403).text(\"đóng\"));\n")
	wait()
	request("đóng")

	write("acme/localhost/a/config.work", routerImport+"router.guard((context) => context.status(403).text(\"sửa\"));\n")
	wait()
	request("sửa")
	serving := engine.cache["localhost"].current()

	write("acme/localhost/a/config.work", routerImport+"router.transition(\"zoom\");\n")
	wait()
	request("sửa") // the new generation did not boot: the old keeps serving
	if engine.cache["localhost"].current() != serving {
		t.Fatal("a config.work that does not run replaced the generation that serves")
	}

	if err := os.Remove(filepath.Join(root, "acme", "localhost", "a", "config.work")); err != nil {
		t.Fatal(err)
	}
	wait()
	request("a")
	before := engine.cache["localhost"].current()

	write("acme/config.work", routerImport+"router.language(\"vi\");\n")
	wait()
	request("a")
	if engine.cache["localhost"].current() == before {
		t.Fatal("the app's config.work appearing did not build the site's generation again")
	}
}
