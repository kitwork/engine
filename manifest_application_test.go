package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// application.work is the manifest of the new .work world (22/10/2026): the surfaces come from
// import * as app from "kitwork/application" and the host env from import * as file from "kitwork/file",
// one call per line like router.work. The engine looks for it first; a folder holding it beside an old
// manifest (app.kitwork.js, server.kitwork.js) stops the boot instead of picking one.

const applicationWork = `import * as app from "kitwork/application";
import * as file from "kitwork/file";

const env = file.env("./.env");

app.root(env.ROOT || "apps");
app.website({ hostname: env.SITE_HOSTNAME || "kitwork.localhost", allowLocal: true });
app.run(env.PORT || 3000);
`

func writeFolder(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		full := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestApplicationWorkEvaluates(t *testing.T) {
	dir := writeFolder(t, map[string]string{
		"application.work": applicationWork,
		".env":             "PORT=9191\nSITE_HOSTNAME=shop.localhost\n",
	})
	raw, err := evalConfigJS(filepath.Join(dir, "application.work"))
	if err != nil {
		t.Fatalf("evalConfigJS(application.work): %v", err)
	}
	if got, _ := raw["port"].(float64); got != 9191 {
		t.Errorf("port = %v, want 9191 from .env through file.env", raw["port"])
	}
	if raw["root"] != "apps" {
		t.Errorf("root = %v, want apps", raw["root"])
	}
}

func TestFindManifestPrefersApplicationWork(t *testing.T) {
	dir := writeFolder(t, map[string]string{"application.work": applicationWork})
	got, err := findManifest(dir)
	if err != nil {
		t.Fatalf("findManifest: %v", err)
	}
	if filepath.Base(got) != "application.work" {
		t.Errorf("findManifest = %s, want application.work", got)
	}
}

// Two manifests in one folder is a question only the author can answer: no silent precedence.
func TestFindManifestRefusesTwoManifests(t *testing.T) {
	for _, old := range []string{"app.kitwork.js", "server.kitwork.js"} {
		dir := writeFolder(t, map[string]string{"application.work": applicationWork, old: "// old\n"})
		_, err := findManifest(dir)
		if err == nil || !strings.Contains(err.Error(), "application.work") || !strings.Contains(err.Error(), old) {
			t.Errorf("findManifest with application.work and %s: err = %v, want an error naming both", old, err)
		}
	}
}

func TestFindManifestStillReadsAnOldManifestAlone(t *testing.T) {
	dir := writeFolder(t, map[string]string{"app.kitwork.js": "// old\n"})
	got, err := findManifest(dir)
	if err != nil || filepath.Base(got) != "app.kitwork.js" {
		t.Fatalf("findManifest = %q, %v; want app.kitwork.js", got, err)
	}
}

func TestFindManifestNamesApplicationWorkWhenNothingIsThere(t *testing.T) {
	_, err := findManifest(t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "application.work") {
		t.Fatalf("err = %v, want it to ask for application.work", err)
	}
}

func TestCommandConfigAcceptsApplicationWork(t *testing.T) {
	dir := writeFolder(t, map[string]string{"application.work": applicationWork, "apps/.keep": ""})
	t.Setenv("ROOT", filepath.Join(dir, "apps"))
	cfg, err := commandConfig(filepath.Join(dir, "application.work"))
	if err != nil {
		t.Fatalf("commandConfig(application.work): %v", err)
	}
	if cfg == nil {
		t.Fatal("commandConfig returned no config")
	}
}
