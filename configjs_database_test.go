package engine

import (
	"strings"
	"testing"
)

// The manifest declares a database by KIND — app.postgresql(), app.sqlite(), app.mysql() — the way
// it declares a surface by kind (app.web(), app.desktop()). Each is the typed door into the same
// databases entry app.database({ type, … }) builds, so parseDatabases and every consumer stay as
// they are; the kind fixes `type`, the alias comes first because it is the connection's name.
func TestAppDatabaseByKind(t *testing.T) {
	file := writeServerJS(t, `import { app, env } from "kitwork";
app
  .postgresql("system", { host: "db.internal", port: 5432, user: "kit", password: "pw", name: "kitwork", sslmode: "disable" })
  .sqlite("app", "./.data/app.db")
  .sqlite("cache", { name: ":memory:", max_open: 1 })
  .mysql("legacy", { host: "127.0.0.1", port: 3306, user: "root", password: "", name: "shop" })
  .web({ port: 8080 });`)

	raw, err := evalConfigJS(file)
	if err != nil {
		t.Fatalf("evalConfigJS: %v", err)
	}
	dbs, _ := raw["databases"].([]interface{})
	if len(dbs) != 4 {
		t.Fatalf("databases len = %d, want 4: %v", len(dbs), raw["databases"])
	}
	want := []map[string]interface{}{
		{"alias": "system", "type": "postgres", "host": "db.internal", "port": 5432.0, "user": "kit", "password": "pw", "name": "kitwork", "sslmode": "disable"},
		{"alias": "app", "type": "sqlite", "name": "./.data/app.db"},
		{"alias": "cache", "type": "sqlite", "name": ":memory:", "max_open": 1.0},
		{"alias": "legacy", "type": "mysql", "host": "127.0.0.1", "port": 3306.0, "user": "root", "password": "", "name": "shop"},
	}
	for i, w := range want {
		got := dbs[i].(map[string]interface{})
		for k, v := range w {
			if got[k] != v {
				t.Errorf("databases[%d].%s = %#v, want %#v (entry %v)", i, k, got[k], v, got)
			}
		}
	}
	// The whole list must still parse into database.Config through the ordinary path.
	cfg, err := ParseConfig(raw)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if len(cfg.Databases) != 4 || cfg.Databases[0].Alias != "system" || cfg.Databases[0].Type != "postgres" ||
		cfg.Databases[1].Type != "sqlite" || cfg.Databases[1].Name != "./.data/app.db" || cfg.Databases[3].Port != 3306 {
		t.Fatalf("ParseConfig lost a typed database: %+v", cfg.Databases)
	}
}

// Alias may be omitted — the flat form named "default" — and the typed kind wins over a `type` key
// smuggled into the options, so app.sqlite() can never open Postgres.
func TestAppDatabaseByKindDefaults(t *testing.T) {
	file := writeServerJS(t, `import { app } from "kitwork";
app.sqlite({ name: "data.db", type: "postgres" }).postgresql({ host: "h", name: "n" }).web({ port: 8080 });`)
	raw, err := evalConfigJS(file)
	if err != nil {
		t.Fatalf("evalConfigJS: %v", err)
	}
	dbs, _ := raw["databases"].([]interface{})
	if len(dbs) != 2 {
		t.Fatalf("databases len = %d, want 2", len(dbs))
	}
	d0, d1 := dbs[0].(map[string]interface{}), dbs[1].(map[string]interface{})
	if d0["type"] != "sqlite" || d0["alias"] != "default" {
		t.Errorf("sqlite entry = %v", d0)
	}
	if d1["type"] != "postgres" || d1["alias"] != "default" {
		t.Errorf("postgresql entry = %v", d1)
	}
}

// CONTROL: app.database({ … }) — the untyped form every existing manifest uses — is unchanged and
// composes with the typed ones in one list.
func TestAppDatabaseUntypedStillWorks(t *testing.T) {
	file := writeServerJS(t, `import { app } from "kitwork";
app.database({ alias: "system", type: "postgres", host: "h", port: 5432, user: "u", password: "p", name: "n" })
   .sqlite("local", "x.db")
   .web({ port: 8080 });`)
	raw, err := evalConfigJS(file)
	if err != nil {
		t.Fatalf("evalConfigJS: %v", err)
	}
	dbs, _ := raw["databases"].([]interface{})
	if len(dbs) != 2 || dbs[0].(map[string]interface{})["alias"] != "system" || dbs[1].(map[string]interface{})["alias"] != "local" {
		t.Fatalf("databases = %v", raw["databases"])
	}
}

// A manifest method this engine does not have must not end the chain silently. Before this, the
// unknown member read as nil, the call on nil yielded nil, and app.web() after it was lost — the
// boot then said "declares no surface", pointing at the wrong line entirely.
func TestAppUnknownMethodKeepsTheChainAlive(t *testing.T) {
	file := writeServerJS(t, `import { app } from "kitwork";
app.title("Before").nosuchmethod("x", { y: 1 }).web({ port: 8080 });`)
	raw, err := evalConfigJS(file)
	if err != nil {
		t.Fatalf("the chain must survive an unknown method: %v", err)
	}
	if raw["title"] != "Before" {
		t.Errorf("title = %v", raw["title"])
	}
	builder, err := evalServerBuilder(file)
	if err != nil {
		t.Fatal(err)
	}
	if warning := builder.unknownMethodWarning(); !strings.Contains(warning, "app.nosuchmethod()") {
		t.Fatalf("the boot warning must name the method: %q", warning)
	}
}
