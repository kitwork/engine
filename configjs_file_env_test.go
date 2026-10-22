package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The manifest reads its environment from a file it names: const env = file.env("./.env"). The path
// is the file's, relative to the manifest's own folder and never outside it; the real environment
// lies on top (the OS wins), so production without a .env still boots (docs/KITWORK_ENVIRONMENT.md,
// 22/10/2026).

func writeManifest(t *testing.T, files map[string]string) string {
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
	return filepath.Join(dir, "app", "app.kitwork.js")
}

const fileEnvManifest = `import { server, file } from "kitwork";
const env = file.env("./.env");
server.run({ port: env.PORT || 3000, name: env.NAME || "none" });
`

func TestFileEnvReadsTheFileBesideTheManifest(t *testing.T) {
	file := writeManifest(t, map[string]string{
		"app/app.kitwork.js": fileEnvManifest,
		"app/.env":           "PORT=9090\nNAME=kit\n",
	})
	raw, err := evalConfigJS(file)
	if err != nil {
		t.Fatalf("evalConfigJS: %v", err)
	}
	if got, _ := raw["port"].(float64); got != 9090 {
		t.Errorf("port = %v, want 9090 from app/.env", raw["port"])
	}
	if raw["name"] != "kit" {
		t.Errorf("name = %v, want kit", raw["name"])
	}
}

func TestFileEnvTheOperatingSystemWins(t *testing.T) {
	file := writeManifest(t, map[string]string{
		"app/app.kitwork.js": fileEnvManifest,
		"app/.env":           "PORT=9090\n",
	})
	t.Setenv("PORT", "8080")
	raw, err := evalConfigJS(file)
	if err != nil {
		t.Fatalf("evalConfigJS: %v", err)
	}
	if got, _ := raw["port"].(float64); got != 8080 {
		t.Errorf("port = %v, want 8080: the real environment wins over the file", raw["port"])
	}
}

// Production often has no .env: the real environment alone must boot the manifest.
func TestFileEnvWithoutTheFileUsesTheOperatingSystem(t *testing.T) {
	file := writeManifest(t, map[string]string{"app/app.kitwork.js": fileEnvManifest})
	t.Setenv("PORT", "7070")
	raw, err := evalConfigJS(file)
	if err != nil {
		t.Fatalf("evalConfigJS: %v", err)
	}
	if got, _ := raw["port"].(float64); got != 7070 {
		t.Errorf("port = %v, want 7070 from the real environment", raw["port"])
	}
}

func TestFileEnvReadsAnyFileTheManifestNames(t *testing.T) {
	file := writeManifest(t, map[string]string{
		"app/app.kitwork.js": `import { server, file } from "kitwork";
const env = file.env("./config/production.env");
server.run({ port: env.PORT || 3000 });
`,
		"app/config/production.env": "PORT=6060\n",
		"app/.env":                  "PORT=9090\n",
	})
	raw, err := evalConfigJS(file)
	if err != nil {
		t.Fatalf("evalConfigJS: %v", err)
	}
	if got, _ := raw["port"].(float64); got != 6060 {
		t.Errorf("port = %v, want 6060 from config/production.env", raw["port"])
	}
}

func TestFileEnvRequireMissingFailsTheBoot(t *testing.T) {
	file := writeManifest(t, map[string]string{
		"app/app.kitwork.js": `import { server, file } from "kitwork";
const env = file.env("./.env");
server.run({ secret: env.require("KITWORK_FILE_ENV_MISSING") });
`,
	})
	_, err := evalConfigJS(file)
	if err == nil || !strings.Contains(err.Error(), "KITWORK_FILE_ENV_MISSING") {
		t.Fatalf("err = %v, want a boot error naming KITWORK_FILE_ENV_MISSING", err)
	}
}

// A path that leaves the manifest's folder is refused at boot, and the file it pointed at is never
// read: its value must not reach the config even as a fallback.
func TestFileEnvRefusesAPathOutsideTheManifestFolder(t *testing.T) {
	file := writeManifest(t, map[string]string{
		"app/app.kitwork.js": `import { server, file } from "kitwork";
const env = file.env("../.env");
server.run({ secret: env.OUTSIDE_SECRET || "none" });
`,
		".env": "OUTSIDE_SECRET=leaked\n",
	})
	raw, err := evalConfigJS(file)
	if err == nil {
		t.Fatalf("expected a boot error for ../.env, got config %v", raw)
	}
	if !strings.Contains(err.Error(), "../.env") || strings.Contains(err.Error(), "leaked") {
		t.Errorf("err = %q, want it to name ../.env and never show the value", err)
	}
}

func TestFileEnvNeedsAPathToAFile(t *testing.T) {
	cases := map[string]string{
		"no path":   `file.env()`,
		"a number":  `file.env(42)`,
		"directory": `file.env("./config")`,
	}
	for name, call := range cases {
		t.Run(name, func(t *testing.T) {
			file := writeManifest(t, map[string]string{
				"app/app.kitwork.js":     "import { server, file } from \"kitwork\";\nconst env = " + call + ";\nserver.run({ port: 3000 });\n",
				"app/config/placeholder": "",
			})
			if _, err := evalConfigJS(file); err == nil || !strings.Contains(err.Error(), "file.env") {
				t.Fatalf("err = %v, want a boot error about file.env", err)
			}
		})
	}
}
