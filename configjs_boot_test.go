package engine

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kitwork/engine/database"
)

// captureBootStdout returns what fn printed to os.Stdout. The boot's notices are plain lines on the
// console, and a test that cannot read them cannot go red when one disappears. The configured
// connections Check records are put back afterwards, so the next test sees the map it expects.
func captureBootStdout(t *testing.T, fn func()) string {
	t.Helper()
	savedConfigs := map[string]database.Config{}
	for alias, config := range database.Configs {
		savedConfigs[alias] = config
	}
	defer func() {
		for alias := range database.Configs {
			delete(database.Configs, alias)
		}
		for alias, config := range savedConfigs {
			database.Configs[alias] = config
		}
	}()

	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	var printed bytes.Buffer
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(&printed, read)
		close(done)
	}()
	saved := os.Stdout
	os.Stdout = write
	restored := false
	restore := func() {
		if restored {
			return
		}
		restored = true
		os.Stdout = saved
		_ = write.Close()
		<-done
		_ = read.Close()
	}
	defer restore() // also when fn stops the test
	fn()
	restore()
	return printed.String()
}

// The main boot path — Run, and the check and profile commands — names a manifest method this engine
// does not have, once, where the person is looking. Until 15/10/2026 only a legacy
// server.run("file.js") printed the line MissingMember's comment promised, and a typo in
// app.kitwork.js was ignored in silence.
func TestBootNamesAnUnknownMethodOnTheMainPath(t *testing.T) {
	root := t.TempDir()
	manifest := writeServerJS(t, fmt.Sprintf(`import { app } from "kitwork";
app
  .root(%q)
  .website({ hostname: "acme.localhost", allowLocal: true })
  .nosuchmethod("x")
  .nosuchmethod("y")
  .run(8080);`, filepath.ToSlash(root)))

	printed := captureBootStdout(t, func() {
		if _, err := Check(manifest); err != nil {
			t.Errorf("check: %v", err)
		}
	})
	const want = "[app] server.kitwork.js: app.nosuchmethod() is not a method of this engine's manifest — ignored"
	if !strings.Contains(printed, want) {
		t.Fatalf("check did not name the unknown method:\n%s", printed)
	}
	if n := strings.Count(printed, "app.nosuchmethod()"); n != 1 {
		t.Fatalf("the method is named %d times, want once however often the chain calls it:\n%s", n, printed)
	}

	// Run: a desktop-only manifest returns before any listener opens, and the line is already out.
	desktop := writeServerJS(t, `import { app } from "kitwork";
app.desktop(true).nosuchmethod();`)
	printed = captureBootStdout(t, func() {
		if err := Run(desktop); err != nil {
			t.Errorf("run: %v", err)
		}
	})
	if !strings.Contains(printed, want) {
		t.Fatalf("run did not name the unknown method:\n%s", printed)
	}

	// CONTROL: a chain of known methods earns no such line.
	clean := writeServerJS(t, `import { app } from "kitwork";
app.desktop(true);`)
	printed = captureBootStdout(t, func() { _ = Run(clean) })
	if strings.Contains(printed, "is not a method") {
		t.Fatalf("CONTROL: a clean chain earned a notice:\n%s", printed)
	}
}

// A connection whose driver this build does not link is named at boot, by the method and alias as
// written, with the reason. Until 15/10/2026 app.redis() and app.mysql() were accepted and then
// failed on the first query — or, as "system", stopped the boot — with words that named no line.
func TestBootNamesConnectionsWithoutADriver(t *testing.T) {
	root := t.TempDir()
	manifest := writeServerJS(t, fmt.Sprintf(`import { app } from "kitwork";
app
  .root(%q)
  .postgresql("replica", "postgres://kit:pw@replica.internal:5432/kitwork")
  .sqlite("app", "./.data/app.db")
  .mysql("legacy", { host: "127.0.0.1", port: 3306, user: "root", password: "", name: "shop" })
  .redis("cache", "redis://127.0.0.1:6379")
  .connect("events", "mysql://root@127.0.0.1:3306/events")
  .website({ hostname: "acme.localhost", allowLocal: true })
  .run(8080);`, filepath.ToSlash(root)))

	printed := captureBootStdout(t, func() {
		if _, err := Check(manifest); err != nil {
			t.Errorf("check: %v", err)
		}
	})
	for _, want := range []string{
		`[app] server.kitwork.js: app.mysql("legacy"): no driver linked in this build`,
		`[app] server.kitwork.js: app.redis("cache"): no driver linked in this build`,
		`[app] server.kitwork.js: app.connect("events") (mysql): no driver linked in this build`,
	} {
		if !strings.Contains(printed, want) {
			t.Errorf("boot did not print %q:\n%s", want, printed)
		}
	}
	// CONTROL: the drivers this build links — PostgreSQL and SQLite — earn no line.
	if n := strings.Count(printed, "no driver linked"); n != 3 {
		t.Fatalf("%d connections reported, want the 3 without a driver:\n%s", n, printed)
	}
	for _, linked := range []string{`"replica"`, `app.sqlite("app")`} {
		if strings.Contains(printed, linked) {
			t.Errorf("CONTROL: a linked connection was reported:\n%s", printed)
		}
	}
	if !database.DriverLinked("postgres") || !database.DriverLinked("sqlite3") || database.DriverLinked("redis") {
		t.Fatal("database.DriverLinked disagrees with the boot")
	}
}
