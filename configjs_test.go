package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeServerJS(t *testing.T, src string) string {
	t.Helper()
	dir := t.TempDir()
	file := filepath.Join(dir, "server.kitwork.js")
	if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return file
}

// server.run({...}) config must come back as a plain map ready for ParseConfig.
func TestEvalConfigJS(t *testing.T) {
	file := writeServerJS(t, `import { server, env } from "kitwork";
server.run({
  port: env.PORT || 3000,
  root: "./tenants",
  databases: [{ alias: "main", type: "sqlite", name: "data.db" }],
  rateLimit: { rate: 2000 },
});`)

	raw, err := evalConfigJS(file)
	if err != nil {
		t.Fatalf("evalConfigJS: %v", err)
	}

	if got, _ := raw["port"].(float64); got != 3000 {
		t.Errorf("port = %v, want 3000", raw["port"])
	}
	if raw["root"] != "./tenants" {
		t.Errorf("root = %v, want ./tenants", raw["root"])
	}
	rl, _ := raw["rateLimit"].(map[string]interface{})
	if rl == nil || rl["rate"].(float64) != 2000 {
		t.Errorf("rateLimit.rate = %v, want 2000", raw["rateLimit"])
	}
	dbs, _ := raw["databases"].([]interface{})
	if len(dbs) != 1 {
		t.Fatalf("databases len = %d, want 1", len(dbs))
	}
	db0 := dbs[0].(map[string]interface{})
	if db0["alias"] != "main" || db0["type"] != "sqlite" || db0["name"] != "data.db" {
		t.Errorf("db0 = %v", db0)
	}
}

// env.int must read the live env var (overriding the default).
func TestEvalConfigJS_EnvOverride(t *testing.T) {
	file := writeServerJS(t, `import { server, env } from "kitwork"; server.run({ port: env.PORT || 3000 });`)
	t.Setenv("PORT", "8080")

	raw, err := evalConfigJS(file)
	if err != nil {
		t.Fatalf("evalConfigJS: %v", err)
	}
	if got, _ := raw["port"].(float64); got != 8080 {
		t.Errorf("port = %v, want 8080 (env override)", raw["port"])
	}
}

// env.require on a missing var must fail the boot loudly.
func TestEvalConfigJS_RequireMissing(t *testing.T) {
	file := writeServerJS(t, `import { server, env } from "kitwork"; server.run({ secret: env.require("KITWORK_MISSING_XYZ") });`)

	if _, err := evalConfigJS(file); err == nil {
		t.Fatal("expected error for missing required env var")
	}
}

// The shipped example (docs/example-config/server.kitwork.js) must actually eval.
func TestEvalConfigJS_ExampleFile(t *testing.T) {
	t.Setenv("SYS_DB_PASSWORD", "secret123") // example marks this env.require
	raw, err := evalConfigJS(filepath.Join("docs", "example-config", "server.kitwork.js"))
	if err != nil {
		t.Fatalf("example server.kitwork.js failed to eval: %v", err)
	}
	if p, _ := raw["port"].(float64); p != 3000 {
		t.Errorf("port = %v, want 3000", raw["port"])
	}
	dbs, ok := raw["databases"].([]interface{})
	if !ok || len(dbs) != 2 {
		t.Fatalf("expected 2 databases, got %T len=%d", raw["databases"], len(dbs))
	}
}

// server.run("path") loads a referenced config file instead of an inline object.
func TestEvalConfigJS_RunWithPath(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "real.json"),
		[]byte(`{"port": 7000, "root": "tenants"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "server.kitwork.js"),
		[]byte(`import { server } from "kitwork"; server.run("./real.json");`), 0o644); err != nil {
		t.Fatal(err)
	}

	raw, err := evalConfigJS(filepath.Join(dir, "server.kitwork.js"))
	if err != nil {
		t.Fatalf("evalConfigJS: %v", err)
	}
	if p, _ := raw["port"].(float64); p != 7000 {
		t.Errorf("port = %v, want 7000 from referenced file", raw["port"])
	}
	if raw["root"] != "tenants" {
		t.Errorf("root = %v, want tenants", raw["root"])
	}
}

// server.run("x.yaml") must load a YAML config file too.
func TestEvalConfigJS_RunWithYamlPath(t *testing.T) {
	dir := t.TempDir()
	yaml := "port: 6000\nroot: tenants\ndatabases:\n  - alias: system\n    type: postgres\n    sslmode: disable\n"
	if err := os.WriteFile(filepath.Join(dir, "real.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "server.kitwork.js"),
		[]byte(`import { server } from "kitwork"; server.run("./real.yaml");`), 0o644); err != nil {
		t.Fatal(err)
	}

	raw, err := evalConfigJS(filepath.Join(dir, "server.kitwork.js"))
	if err != nil {
		t.Fatalf("evalConfigJS: %v", err)
	}
	// End-to-end via ParseConfig (handles yaml int vs json float64).
	cfg, perr := ParseConfig(raw)
	if perr != nil {
		t.Fatalf("ParseConfig: %v", perr)
	}
	if cfg.Port != 6000 {
		t.Errorf("port = %d, want 6000 from referenced yaml", cfg.Port)
	}
	if len(cfg.Databases) != 1 {
		t.Errorf("expected 1 database from yaml, got %d", len(cfg.Databases))
	}
}

// engine.Run only accepts a .kitwork.js bootstrap. A YAML/JSON path passed directly
// is rejected with a hint to reference it via server.run() instead.
func TestRun_RejectsNonJSBootstrap(t *testing.T) {
	for _, f := range []string{"config.kitwork.yaml", "config.kitwork.json", "config.yml"} {
		err := Run(f)
		if err == nil {
			t.Fatalf("Run(%q): expected error, got nil", f)
		}
		if !strings.Contains(err.Error(), "server.run") {
			t.Errorf("Run(%q): error should hint at server.run, got: %v", f, err)
		}
	}
}

// A missing .js bootstrap is a clear error (and never starts a server).
func TestRun_MissingBootstrap(t *testing.T) {
	if err := Run(filepath.Join(t.TempDir(), "nope.kitwork.js")); err == nil {
		t.Fatal("expected error for missing bootstrap file")
	}
}

// A file that never calls server.run({...}) is a config error.
func TestEvalConfigJS_NoRun(t *testing.T) {
	file := writeServerJS(t, `import { server, env } from "kitwork"; const x = 1;`)

	if _, err := evalConfigJS(file); err == nil {
		t.Fatal("expected error when server.run is never called")
	}
}

// Validation errors should be returned to JS and abort host boot.
func TestEvalConfigJS_ValidationFailure(t *testing.T) {
	file := writeServerJS(t, `
import { server, env } from "kitwork";
const err = server.port(-5).run();
if (err) {
	// JS captured the error successfully!
}
`)
	_, err := evalConfigJS(file)
	if err == nil {
		t.Fatal("expected boot to fail on configuration validation error")
	}
	if !strings.Contains(err.Error(), "invalid port number") {
		t.Errorf("expected validation error message, got: %v", err)
	}
}

// Fluent builder pattern configuration must parse and run correctly.
func TestEvalConfigJS_BuilderPattern(t *testing.T) {
	file := writeServerJS(t, `
import { server, env } from "kitwork";
server.port(8080)
      .root("tenants")
      .database({ alias: "main", type: "sqlite", name: "data.db" })
      .run();
`)
	raw, err := evalConfigJS(file)
	if err != nil {
		t.Fatalf("evalConfigJS: %v", err)
	}
	if raw["port"] != float64(8080) {
		t.Errorf("port = %v, want 8080", raw["port"])
	}
	if raw["root"] != "tenants" {
		t.Errorf("root = %v, want tenants", raw["root"])
	}
	dbs, ok := raw["databases"].([]interface{})
	if !ok || len(dbs) != 1 {
		t.Fatalf("databases = %v, want 1 db", raw["databases"])
	}
	db0 := dbs[0].(map[string]interface{})
	if db0["alias"] != "main" || db0["type"] != "sqlite" || db0["name"] != "data.db" {
		t.Errorf("db0 = %v", db0)
	}
}

// Fluent builder must support string-to-numeric coercion and shorthand run arguments.
func TestEvalConfigJS_MultiStyle(t *testing.T) {
	// Style 1: String port coercion in .port()
	file1 := writeServerJS(t, `import { server } from "kitwork"; server.port("9090").run();`)
	raw1, err := evalConfigJS(file1)
	if err != nil {
		t.Fatalf("style 1 failed: %v", err)
	}
	if raw1["port"] != float64(9090) {
		t.Errorf("style 1 port = %v, want 9090", raw1["port"])
	}

	// Style 2: Shorthand numeric port in .run(8888)
	file2 := writeServerJS(t, `import { server } from "kitwork"; server.run(8888);`)
	raw2, err := evalConfigJS(file2)
	if err != nil {
		t.Fatalf("style 2 failed: %v", err)
	}
	if raw2["port"] != float64(8888) {
		t.Errorf("style 2 port = %v, want 8888", raw2["port"])
	}

	// Style 3: Shorthand string port in .run("7777")
	file3 := writeServerJS(t, `import { server } from "kitwork"; server.run("7777");`)
	raw3, err := evalConfigJS(file3)
	if err != nil {
		t.Fatalf("style 3 failed: %v", err)
	}
	if raw3["port"] != float64(7777) {
		t.Errorf("style 3 port = %v, want 7777", raw3["port"])
	}
}

// ---------------------------------------------------------------------------
// Manifest STYLES. A Kitwork manifest is a blueprint: the author picks the shape.
// Both must build the SAME config, and mixing them must compose — these tests pin
// that contract so neither style can quietly regress.
// ---------------------------------------------------------------------------

// Style A: flat chain — app.port().hostname().allowLocal()
func TestManifestStyle_FlatChain(t *testing.T) {
	file := writeServerJS(t, `import { app, env } from "kitwork";
app.port(3100).hostname("flat.example").allowLocal(true).rateLimit({ rate: 50, period: "1s" });`)

	raw, err := evalConfigJS(file)
	if err != nil {
		t.Fatalf("flat chain should be a valid manifest: %v", err)
	}
	if raw["port"] != float64(3100) {
		t.Errorf("port = %v, want 3100", raw["port"])
	}
	if raw["hostname"] != "flat.example" {
		t.Errorf("hostname = %v, want flat.example", raw["hostname"])
	}
	if raw["allow_local"] != true {
		t.Errorf("allow_local = %v, want true", raw["allow_local"])
	}
	if raw["rate_limit"] == nil {
		t.Error("rate_limit missing")
	}
}

// Style B: grouped surface — app.web({ ... }). Must flatten to the SAME keys as style A.
func TestManifestStyle_GroupedWeb(t *testing.T) {
	file := writeServerJS(t, `import { app } from "kitwork";
app.web({ port: 3100, hostname: "flat.example", allowLocal: true, rateLimit: { rate: 50, period: "1s" } });`)

	raw, err := evalConfigJS(file)
	if err != nil {
		t.Fatalf("grouped web should be a valid manifest: %v", err)
	}
	if raw["port"] != float64(3100) {
		t.Errorf("port = %v, want 3100", raw["port"])
	}
	if raw["hostname"] != "flat.example" {
		t.Errorf("hostname = %v, want flat.example (app.web must flatten to the same key)", raw["hostname"])
	}
	if raw["allow_local"] != true {
		t.Errorf("allow_local = %v, want true (allowLocal → allow_local)", raw["allow_local"])
	}
	if raw["rate_limit"] == nil {
		t.Error("rate_limit missing (rateLimit → rate_limit)")
	}
}

// app.web(8080) shorthand declares the surface with just a port.
func TestManifestStyle_WebShorthandPort(t *testing.T) {
	raw, err := evalConfigJS(writeServerJS(t, `import { app } from "kitwork"; app.web(8080);`))
	if err != nil {
		t.Fatalf("app.web(8080) should be valid: %v", err)
	}
	if raw["port"] != float64(8080) {
		t.Errorf("port = %v, want 8080", raw["port"])
	}
}

// Desktop in BOTH styles: flat sugar (app.chrome/app.window) and grouped (app.desktop({...})),
// mixed in one chain and in either order — they must merge into one desktop block.
func TestManifestStyle_DesktopFlatAndGroupedCompose(t *testing.T) {
	file := writeServerJS(t, `import { app } from "kitwork";
app.port(3000).chrome("native").desktop({ window: { width: 800, maximized: true } });`)

	raw, err := evalConfigJS(file)
	if err != nil {
		t.Fatalf("mixed desktop styles should be valid: %v", err)
	}
	d, ok := raw["desktop"].(map[string]interface{})
	if !ok {
		t.Fatalf("desktop block missing: %#v", raw["desktop"])
	}
	if d["chrome"] != "native" {
		t.Errorf("chrome = %v, want native (flat app.chrome must survive a later app.desktop)", d["chrome"])
	}
	w, ok := d["window"].(map[string]interface{})
	if !ok {
		t.Fatalf("window missing: %#v", d["window"])
	}
	if w["width"] != float64(800) || w["maximized"] != true {
		t.Errorf("window = %#v, want width 800 + maximized", w)
	}
}

// The legacy `server` object stays an alias of `app` — old manifests must not break.
func TestManifestStyle_ServerAliasStillWorks(t *testing.T) {
	raw, err := evalConfigJS(writeServerJS(t, `import { server } from "kitwork"; server.run(4321);`))
	if err != nil {
		t.Fatalf("legacy server.run must still work: %v", err)
	}
	if raw["port"] != float64(4321) {
		t.Errorf("port = %v, want 4321", raw["port"])
	}
}

// A desktop-only manifest is VALID data (the shell reads it) but has no web surface for the cloud
// host — the error must say that, not "you forgot to call run()".
func TestManifestStyle_DesktopOnlyHasNoWebSurface(t *testing.T) {
	_, err := evalConfigJS(writeServerJS(t, `import { app } from "kitwork"; app.title("X").desktop({ chrome: "native" });`))
	if err == nil {
		t.Fatal("desktop-only manifest must not satisfy the cloud host")
	}
	if !strings.Contains(err.Error(), "no web surface") {
		t.Errorf("error should explain the missing WEB surface, got: %v", err)
	}
}
