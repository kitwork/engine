package engine

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/kitwork/engine/core"
	"github.com/kitwork/engine/work"
)

// app.shareEnvironment (docs/KITWORK_ENVIRONMENT.md mục 2, 22/10/2026): the manifest names keys, the host
// gives their values, for every app or by app; the values come from the .env beside the manifest, a file
// file.env names, the operating system on top. A key the host has not, or has empty, stops the boot naming
// the key and never a value; a shape the method does not take stops it naming the shape.
//
// CONTROL (22/10/2026): with resolveSharedEnvironment taking the keys it finds and saying nothing of the
// others, the manifest sharing keys the host has not booted: "missing: error = \"\""; with ShareEnvironment
// recording no list for every app, "shared = &{All:map[] …}".
func TestShareEnvironmentTakesTheHostsValuesByName(t *testing.T) {
	t.Setenv("KITWORK_TEST_SHARED_FROM_THE_SYSTEM", "from-the-system")
	file := writeManifestFiles(t, map[string]string{
		"app/application.work": `import * as app from "kitwork/application";
import * as file from "kitwork/file";
const env = file.env("./config/host.env");
app.shareEnvironment(["SMTP_URL", "KITWORK_TEST_SHARED_FROM_THE_SYSTEM"]);
app.shareEnvironment({ shop: ["SENTRY_DSN"], blog: ["ZIP"] });
app.website("localhost").run(8080);
`,
		"app/.env":            "SMTP_URL=smtp://beside\nDB_PASSWORD=never-shared\n",
		"app/config/host.env": "SENTRY_DSN=https://sentry\nZIP=\"0123\"\n",
	})
	builder, err := evalServerBuilder(file)
	if err != nil {
		t.Fatalf("evalServerBuilder: %v", err)
	}
	want := &work.SharedEnvironment{
		All: map[string]work.EnvironmentEntry{
			"SMTP_URL":                            {Text: "smtp://beside"},
			"KITWORK_TEST_SHARED_FROM_THE_SYSTEM": {Text: "from-the-system"},
		},
		Apps: map[string]map[string]work.EnvironmentEntry{
			"shop": {"SENTRY_DSN": {Text: "https://sentry"}},
			"blog": {"ZIP": {Text: "0123", Quoted: true}},
		},
	}
	if !reflect.DeepEqual(builder.sharedEnvironment, want) {
		t.Fatalf("shared = %+v\nwant %+v", builder.sharedEnvironment, want)
	}
	if got := builder.sharedEnvironment.For("shop"); got["DB_PASSWORD"].Text != "" || got["SENTRY_DSN"].Text != "https://sentry" || got["SMTP_URL"].Text != "smtp://beside" {
		t.Fatalf("shop receives %v", got)
	}

	for name, row := range map[string]struct{ call, want string }{
		"missing": {`app.shareEnvironment(["SMTP_URL", "MISSING_KEY", "EMPTY_KEY"]);`, "app.shareEnvironment: host không có EMPTY_KEY, MISSING_KEY; khai trong .env cạnh application.work, file file.env nêu, hay biến môi trường của hệ điều hành (KITWORK_ENVIRONMENT.md mục 2)"},
		"a word":  {`app.shareEnvironment("SMTP_URL");`, shareEnvironmentShape},
		"a value": {`app.shareEnvironment([42]);`, `app.shareEnvironment: "42" không phải tên khoá; tên khoá viết là chuỗi, như "SMTP_URL" (KITWORK_ENVIRONMENT.md mục 2)`},
	} {
		t.Run(name, func(t *testing.T) {
			file := writeManifestFiles(t, map[string]string{
				"app/application.work": "import * as app from \"kitwork/application\";\n" + row.call + "\napp.website(\"localhost\").run(8080);\n",
				"app/.env":             "SMTP_URL=smtp://secret-host\nEMPTY_KEY=\n",
			})
			builder, err := evalServerBuilder(file)
			got := ""
			if err != nil {
				got = err.Error()
			} else if builder.err != "" {
				got = builder.err
			}
			if got != row.want {
				t.Fatalf("error = %q\nwant %q", got, row.want)
			}
		})
	}
}

// writeManifestFiles writes files under a fresh folder and gives the path of its app/application.work.
func writeManifestFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	return filepath.Join(filepath.Dir(writeManifest(t, files)), "application.work")
}

// An app app.shareEnvironment names must be one the host's root holds: a folder of apps/, or "app" for the
// one app of app/.
//
// CONTROL (22/10/2026): with validateSharedApps accepting every name, "shareEnvironment({ blgo: … }) = <nil>,
// want …".
func TestShareEnvironmentNamesAnAppTheHostHas(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "shop"), 0o755); err != nil {
		t.Fatal(err)
	}
	shared := func(app string) *work.SharedEnvironment {
		return &work.SharedEnvironment{Apps: map[string]map[string]work.EnvironmentEntry{app: {}}}
	}
	if err := validateSharedApps(shared("shop"), root, work.RootLayoutMultiTenant); err != nil {
		t.Fatalf("shop: %v", err)
	}
	want := "app.shareEnvironment: không có app `blgo` (thư mục " + filepath.Base(root) + "/blgo); tên app là tên thư mục của nó (KITWORK_ENVIRONMENT.md mục 2)"
	if err := validateSharedApps(shared("blgo"), root, work.RootLayoutMultiTenant); err == nil || err.Error() != want {
		t.Fatalf("shareEnvironment({ blgo: … }) = %v, want %q", err, want)
	}
	if err := validateSharedApps(shared("app"), root, work.RootLayoutSingle); err != nil {
		t.Fatalf("the one app of app/: %v", err)
	}
	if err := validateSharedApps(shared("shop"), root, work.RootLayoutSingle); err == nil || !strings.Contains(err.Error(), "không có app `shop`; host này chạy một app, tên nó là \"app\"") {
		t.Fatalf("a named app on a host of one app: %v", err)
	}
}

// A key the host's .env declares that the operating system has too is said at boot, once, by name: the
// system's value wins, so the file's never counts (mục 1). A value is never in the line.
//
// CONTROL (22/10/2026): with hostEnvironment not calling warnHostCollision, nothing was said: "said [], want
// one line naming KITWORK_TEST_COLLISION".
func TestHostEnvironmentSaysAKeyTheSystemHasToo(t *testing.T) {
	t.Setenv("KITWORK_TEST_COLLISION", "from-the-system")
	var said []string
	saved := hostSay
	hostSay = func(line string) { said = append(said, line) }
	t.Cleanup(func() { hostSay = saved })
	hostCollisions.Delete("KITWORK_TEST_COLLISION")

	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("KITWORK_TEST_COLLISION=from-the-file\nKITWORK_TEST_ONLY_FILE=x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		vars := hostEnvironment(path)
		if vars["KITWORK_TEST_COLLISION"].Text != "from-the-system" || vars["KITWORK_TEST_ONLY_FILE"].Text != "x" {
			t.Fatalf("host environment = %v", vars)
		}
	}
	const want = "[environment] .env của host khai `KITWORK_TEST_COLLISION` mà hệ điều hành cũng có: giá trị của hệ điều hành thắng; đặt tên khác cho khoá của host (không dùng HOSTNAME, HOST, USER, PATH, HOME; KITWORK_ENVIRONMENT.md mục 1)"
	if len(said) != 1 || said[0] != want {
		t.Fatalf("said %q, want one line naming KITWORK_TEST_COLLISION", said)
	}

	// the same of a file file.env names in application.work (CONTROL 23/10/2026: with manifestFileEnv not
	// calling warnHostCollision, "file.env said [], want one line naming KITWORK_TEST_COLLISION_FILE")
	t.Setenv("KITWORK_TEST_COLLISION_FILE", "from-the-system")
	hostCollisions.Delete("KITWORK_TEST_COLLISION_FILE")
	said = nil
	manifest := writeManifestFiles(t, map[string]string{
		"app/application.work": "import * as app from \"kitwork/application\";\nimport * as file from \"kitwork/file\";\n" +
			"const env = file.env(\"./config/host.env\");\napp.desktop({ name: env.KITWORK_TEST_COLLISION_FILE });\n",
		"app/config/host.env": "KITWORK_TEST_COLLISION_FILE=from-the-file\n",
	})
	if _, err := evalServerBuilder(manifest); err != nil {
		t.Fatalf("evalServerBuilder: %v", err)
	}
	wantFile := strings.ReplaceAll(want, "KITWORK_TEST_COLLISION`", "KITWORK_TEST_COLLISION_FILE`")
	if len(said) != 1 || said[0] != wantFile || strings.Contains(said[0], "from-the-") {
		t.Fatalf("file.env said %q, want one line naming KITWORK_TEST_COLLISION_FILE", said)
	}
}

// What app.shareEnvironment shares reaches the engine on the boot paths, a link no test of a part covers
// (22/10/2026): kitwork check (Check) boots a site whose environment.require names a key only the host has
// and shares with its app; an app named that the host's root has not stops Check and Run, before anything
// is served.
//
// CONTROL (23/10/2026): with commandConfig not handing the engine builder.sharedEnvironment, the check
// reported the key missing: "check: issues [acme/localhost: prepare …: environment.require: thiếu SMTP_URL
// …]"; with commandConfig and Run not calling validateSharedApps, a misspelt app passed: "check = <nil>, want
// …" and "run = …failed to connect to system database…, want …".
func TestShareEnvironmentReachesCheckAndRun(t *testing.T) {
	write := func(t *testing.T, app string) string {
		t.Helper()
		dir := t.TempDir()
		apps := filepath.Join(dir, "apps")
		files := map[string]string{
			".env": "SMTP_URL=smtp://host-only\n",
			"application.work": "import * as app from \"kitwork/application\";\nimport * as file from \"kitwork/file\";\n" +
				"const env = file.env(\"./.env\");\napp.root(\"" + filepath.ToSlash(apps) + "\");\n" +
				"app.shareEnvironment({ " + app + ": [\"SMTP_URL\"] });\n" +
				"app.mysql({ alias: \"system\", host: \"127.0.0.1\", port: 3306, user: \"root\", password: \"\", name: \"kitwork\" });\n" +
				"app.website(\"localhost\");\napp.run(8080);\n",
			"apps/acme/localhost/router.work": "import * as router from \"kitwork/router\";\nimport { environment } from \"kitwork\";\n" +
				"const smtp = environment.require(\"SMTP_URL\");\nrouter.get((context) => context.json({ set: smtp.length > 0 }));\n",
		}
		for relative, content := range files {
			full := filepath.Join(dir, filepath.FromSlash(relative))
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return filepath.Join(dir, "application.work")
	}

	manifest := write(t, "acme")
	var report core.CheckReport
	var err error
	captureBootStdout(t, func() { report, err = Check(manifest) })
	if err != nil || !report.OK() || report.Valid != 1 {
		t.Fatalf("check: %v, issues %v", err, report.Issues)
	}

	misspelt := write(t, "acmee")
	const want = "app.shareEnvironment: không có app `acmee` (thư mục apps/acmee); tên app là tên thư mục của nó (KITWORK_ENVIRONMENT.md mục 2)"
	captureBootStdout(t, func() { _, err = Check(misspelt) })
	if err == nil || err.Error() != want {
		t.Fatalf("check = %v, want %q", err, want)
	}
	captureBootStdout(t, func() { err = Run(misspelt) })
	if err == nil || err.Error() != want {
		t.Fatalf("run = %v, want %q", err, want)
	}
}

// file.env reads its file through an os.Root of the manifest's folder: a folder of it that is a link out
// (on Windows a junction, which safepath's filepath.EvalSymlinks does not follow) is refused as the path
// outside the folder, and the file there is never read (mục 7).
//
// CONTROL (22/10/2026): with manifestFileEnv reading the file by its machine path (the check of safepath
// alone), the junction let the manifest read the file outside: "port = 9999 from outside, want the error".
func TestFileEnvRefusesAFolderLinkedOut(t *testing.T) {
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "host.env"), []byte("PORT=9999\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	file := writeManifestFiles(t, map[string]string{
		"app/application.work": `import * as app from "kitwork/application";
import * as file from "kitwork/file";
const env = file.env("./config/host.env");
app.website("localhost").run(env.PORT || 8080);
`,
	})
	link := filepath.Join(filepath.Dir(file), "config")
	if err := os.Symlink(outside, link); err != nil {
		if runtime.GOOS != "windows" {
			t.Skipf("symlink unavailable: %v", err)
		}
		if out, junctionErr := exec.Command("cmd", "/c", "mklink", "/J", link, outside).CombinedOutput(); junctionErr != nil {
			t.Skipf("neither a symlink (%v) nor a junction (%v: %s)", err, junctionErr, out)
		}
	}
	builder, err := evalServerBuilder(file)
	const want = `file.env("./config/host.env"): đường dẫn nằm ngoài thư mục của manifest`
	if err == nil || err.Error() != want {
		port := ""
		if builder != nil {
			port = builder.config["port"].Text()
		}
		t.Fatalf("err = %v, port = %s from outside, want the error %q", err, port, want)
	}
}
