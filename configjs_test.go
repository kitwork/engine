package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kitwork/engine/database"
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

func TestAppWebBytecodeCacheIsExplicitAndDirectoryScoped(t *testing.T) {
	file := writeServerJS(t, `import { app } from "kitwork";
app.web({
  port: 8080,
  bytecodeCache: true,
  bytecodeCacheDir: ".cache/vm",
});`)
	raw, err := evalConfigJS(file)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := ParseConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.BytecodeCache || cfg.BytecodeCacheDir != ".cache/vm" {
		t.Fatalf("bytecode cache config = %+v", cfg)
	}
	if got := bytecodeCacheDirectory(cfg); got != filepath.Join(cfg.Root, ".cache", "vm") {
		t.Fatalf("bytecode cache directory = %q", got)
	}

	disabled, err := ParseConfig(map[string]interface{}{})
	if err != nil {
		t.Fatal(err)
	}
	if disabled.BytecodeCache || bytecodeCacheDirectory(disabled) != "" {
		t.Fatal("bytecode cache was not opt-in")
	}
}

func TestAppSearchCollectionCanaryIsExplicit(t *testing.T) {
	file := writeServerJS(t, `import { app } from "kitwork";
app.search({ collectionCanary: true }).web({ port: 8080 });`)
	raw, err := evalConfigJS(file)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := ParseConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Search.CollectionCanary {
		t.Fatalf("collection search canary config = %+v", cfg.Search)
	}

	disabled, err := ParseConfig(map[string]interface{}{})
	if err != nil {
		t.Fatal(err)
	}
	if disabled.Search.CollectionCanary {
		t.Fatal("collection search canary was not opt-in")
	}
	snake, err := ParseConfig(map[string]interface{}{
		"search": map[string]interface{}{"collection_canary": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !snake.Search.CollectionCanary {
		t.Fatal("snake-case collection search canary was not parsed")
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

func TestAppDatabaseOwnsKitDBWithFlatOptions(t *testing.T) {
	file := writeServerJS(t, `
import { app, env } from "kitwork";
app.database("./data.kitdb", {
  alias: "shop",
  host: "127.0.0.1",
  port: 55432,
  user: "kitdb",
  password: env.require("APP_DATABASE_PASSWORD"),
  kitsql: true,
  memory: "256mb",
  concurrency: 6,
  warm: true,
  cache: { select: "2m", search: "30s", analytics: "5m", memory: "32mb" },
}).web(8080);
`)
	t.Setenv("APP_DATABASE_PASSWORD", "manifest-secret")
	raw, err := evalConfigJS(file)
	if err != nil {
		t.Fatal(err)
	}
	if _, found := raw["databases"]; found {
		t.Fatal("owned KitDB was confused with an external database connection")
	}
	cfg, err := ParseConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.AppDatabases) != 1 {
		t.Fatalf("owned databases = %d, want 1", len(cfg.AppDatabases))
	}
	database := cfg.AppDatabases[0]
	if database.Alias != "shop" || database.Path != "./data.kitdb" || database.Host != "127.0.0.1" ||
		database.Port != 55432 || database.User != "kitdb" || database.Password != "manifest-secret" || !database.KitSQL {
		t.Fatalf("database identity = %+v", database)
	}
	if database.MemoryBytes != 256<<20 || database.Concurrency != 6 || !database.Warm {
		t.Fatalf("database resources = %+v", database)
	}
	if database.Cache.Select != 2*time.Minute || database.Cache.Search != 30*time.Second ||
		database.Cache.Analytics != 5*time.Minute || database.Cache.MaximumBytes != 32<<20 {
		t.Fatalf("database cache = %+v", database.Cache)
	}
}

func TestAppDatabaseKitSQLRequiresPassword(t *testing.T) {
	file := writeServerJS(t, `import { app } from "kitwork";
app.database("./data.kitdb", { kitsql: true }).web(8080);`)
	raw, err := evalConfigJS(file)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseConfig(raw); err == nil || !strings.Contains(err.Error(), "password is required") {
		t.Fatalf("ParseConfig error = %v", err)
	}
}

func TestAppDatabaseCacheDurationAppliesToEverySafeReadClass(t *testing.T) {
	file := writeServerJS(t, `import { app } from "kitwork";
app.database("./data.kitdb", { cache: "1m" }).web(8080);`)
	raw, err := evalConfigJS(file)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := ParseConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	cache := cfg.AppDatabases[0].Cache
	if cache.Select != time.Minute || cache.Search != time.Minute || cache.Analytics != time.Minute {
		t.Fatalf("database cache = %+v", cache)
	}
}

func TestAppDatabaseKeepsExternalConnectionDeclarationCompatible(t *testing.T) {
	file := writeServerJS(t, `import { app } from "kitwork";
app.database({ alias: "system", type: "postgres", host: "db.internal" }).web(8080);`)
	raw, err := evalConfigJS(file)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := ParseConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Databases) != 1 || cfg.Databases[0].Alias != "system" || len(cfg.AppDatabases) != 0 {
		t.Fatalf("external=%+v owned=%+v", cfg.Databases, cfg.AppDatabases)
	}
}

func TestAppExternalConnectorMethodsNormalizeToOneRegistry(t *testing.T) {
	file := writeServerJS(t, `import { app } from "kitwork";
app
  .database("./native.kitdb", { alias: "native" })
  .postgresql("system", "postgresql://kitwork:secret@db.internal/app")
  .mysql("legacy", { host: "mysql.internal", port: 3306, user: "reader", name: "archive" })
  .sqlite("local", "./local.sqlite")
  .redis("cache", "rediss://cache.internal:6380")
  .connect("remote-kitdb", "kitsql://kitdb:secret@kitdb.internal/shop")
  .connect("warehouse", { driver: "postgresql", host: "warehouse.internal", name: "events" })
  .web(8080);`)
	raw, err := evalConfigJS(file)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := ParseConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.AppDatabases) != 1 || cfg.AppDatabases[0].Alias != "native" {
		t.Fatalf("owned databases = %+v", cfg.AppDatabases)
	}
	if len(cfg.Databases) != 6 {
		t.Fatalf("external databases = %+v", cfg.Databases)
	}
	byAlias := make(map[string]database.Config, len(cfg.Databases))
	for _, connection := range cfg.Databases {
		byAlias[connection.Alias] = connection
	}
	if got := byAlias["system"]; got.Type != "postgres" || got.URL != "postgresql://kitwork:secret@db.internal/app" {
		t.Fatalf("postgresql = %+v", got)
	}
	if got := byAlias["legacy"]; got.Type != "mysql" || got.Host != "mysql.internal" || got.Port != 3306 {
		t.Fatalf("mysql = %+v", got)
	}
	if got := byAlias["local"]; got.Type != "sqlite" || got.Name != "./local.sqlite" {
		t.Fatalf("sqlite = %+v", got)
	}
	if got := byAlias["cache"]; got.Type != "redis" || got.URL != "rediss://cache.internal:6380" {
		t.Fatalf("redis = %+v", got)
	}
	if got := byAlias["remote-kitdb"]; got.Type != "kitsql" || got.URL != "kitsql://kitdb:secret@kitdb.internal/shop" {
		t.Fatalf("kitsql = %+v", got)
	}
	if got := byAlias["warehouse"]; got.Type != "postgres" || got.Host != "warehouse.internal" {
		t.Fatalf("generic connection = %+v", got)
	}
}

func TestAppConnectRejectsAmbiguousOrInvalidDescriptors(t *testing.T) {
	tests := []string{
		`app.connect("system", "db.internal").web(8080);`,
		`app.connect("system", { host: "db.internal" }).web(8080);`,
		`app.postgresql("system", { type: "mysql" }).web(8080);`,
		`app.postgresql("system", "mysql://db.internal/app").web(8080);`,
		`app.sqlite("", "./local.sqlite").web(8080);`,
	}
	for _, source := range tests {
		file := writeServerJS(t, `import { app } from "kitwork"; `+source)
		if _, err := evalConfigJS(file); err == nil {
			t.Fatalf("accepted invalid connector: %s", source)
		}
	}
}

func TestAppExternalConnectorAliasesMustBeUnique(t *testing.T) {
	file := writeServerJS(t, `import { app } from "kitwork";
app
  .postgresql("system", "postgresql://db.internal/app")
  .sqlite("SYSTEM", "./local.sqlite")
  .web(8080);`)
	raw, err := evalConfigJS(file)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseConfig(raw); err == nil || !strings.Contains(err.Error(), "declared more than once") {
		t.Fatalf("ParseConfig() error = %v, want duplicate alias rejection", err)
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

func TestReadMobileManifestV1(t *testing.T) {
	file := writeServerJS(t, `import { app } from "kitwork";
app.title("Pocket Notes").icon("assets/app-icon.svg").mobile({
  version: 1,
  id: "org.kitwork.notes",
  versionName: "1.2.3",
  versionCode: 12,
  start: { domain: "notes.kitwork.localhost", path: "/notes" },
  orientation: "portrait",
  theme: { mode: "dark", color: "#112233" },
  permissions: ["clipboard.writeText", "device.info"]
});`)

	raw, err := ReadManifest(file)
	if err != nil {
		t.Fatalf("ReadManifest: %v", err)
	}
	if _, ok := raw["mobile"].(map[string]interface{}); !ok {
		t.Fatalf("raw mobile block = %#v", raw["mobile"])
	}

	mobile, err := ReadMobileManifest(file)
	if err != nil {
		t.Fatalf("ReadMobileManifest: %v", err)
	}
	if !mobile.Declared || mobile.Legacy || mobile.ID != "org.kitwork.notes" || mobile.Title != "Pocket Notes" || mobile.Icon != "assets/app-icon.svg" {
		t.Fatalf("mobile identity = %#v", mobile)
	}
	if mobile.Start.Domain != "notes.kitwork.localhost" || mobile.Start.Path != "/notes" || mobile.VersionName != "1.2.3" || mobile.VersionCode != 12 {
		t.Fatalf("mobile start/version = %#v", mobile)
	}
	if mobile.Orientation != "portrait" || mobile.Theme.Mode != "dark" || mobile.Theme.Color != "#112233" || len(mobile.Permissions) != 2 {
		t.Fatalf("mobile presentation/permissions = %#v", mobile)
	}
}

func TestReadMobileManifestKeepsLegacyDeclaration(t *testing.T) {
	file := writeServerJS(t, `import { app } from "kitwork"; app.title("Legacy").mobile(true);`)

	raw, err := ReadManifest(file)
	if err != nil {
		t.Fatalf("ReadManifest legacy: %v", err)
	}
	if raw["mobile"] != true {
		t.Fatalf("raw legacy mobile = %#v, want true", raw["mobile"])
	}
	mobile, err := ReadMobileManifest(file)
	if err != nil {
		t.Fatalf("ReadMobileManifest legacy: %v", err)
	}
	if !mobile.Declared || !mobile.Legacy || mobile.Version != 0 || mobile.Title != "Legacy" {
		t.Fatalf("legacy mobile = %#v", mobile)
	}

	_, err = evalConfigJS(file)
	if err == nil || !strings.Contains(err.Error(), "no web surface") || !strings.Contains(err.Error(), "app.mobile()") {
		t.Fatalf("mobile-only cloud error = %v", err)
	}
}

func TestManifestStyle_DatabaseOnlyIsRunnable(t *testing.T) {
	raw, err := evalConfigJS(writeServerJS(t, `
import { app } from "kitwork";
app.database("./data.kitdb", { port: 5445, user: "kitdb", password: "secret" });`))
	if err != nil {
		t.Fatalf("database-only manifest should satisfy the host: %v", err)
	}
	databases, ok := raw["owned_databases"].([]interface{})
	if !ok || len(databases) != 1 {
		t.Fatalf("owned databases = %#v", raw["owned_databases"])
	}
}
