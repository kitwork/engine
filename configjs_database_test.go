package engine

import (
	"strings"
	"testing"
)

// The manifest OFFERS surfaces and CONSUMES connections, and names both by kind:
//
//	surfaces     app.web()  app.desktop()  app.mobile()  app.database(path, options)   ← KitDB
//	connections  app.connect()  app.postgresql()  app.mysql()  app.sqlite()  app.redis()
//
// This is the RC branch's manifest layer, carried over verbatim so the two builds agree on every
// shape and every refusal: alias first and required, then a URL or an options object; the kind
// fixes `type`, and options that contradict it are an error, not a silent override.
func TestAppConnectionsByKind(t *testing.T) {
	file := writeServerJS(t, `import { app } from "kitwork";
app
  .postgresql("system", { host: "db.internal", port: 5432, user: "kit", password: "pw", name: "kitwork", sslmode: "disable" })
  .postgresql("replica", "postgres://kit:pw@replica.internal:5432/kitwork?sslmode=disable")
  .sqlite("app", "./.data/app.db")
  .sqlite("cache", { name: ":memory:", max_open: 1 })
  .mysql("legacy", { host: "127.0.0.1", port: 3306, user: "root", password: "", name: "shop" })
  .connect("events", { type: "postgres", host: "h", name: "n" })
  .connect("analytics", "postgresql://a:b@c/d")
  .web({ port: 8080 });`)

	raw, err := evalConfigJS(file)
	if err != nil {
		t.Fatalf("evalConfigJS: %v", err)
	}
	dbs, _ := raw["databases"].([]interface{})
	if len(dbs) != 7 {
		t.Fatalf("databases len = %d, want 7: %v", len(dbs), raw["databases"])
	}
	want := []map[string]interface{}{
		{"alias": "system", "type": "postgres", "host": "db.internal", "port": 5432.0, "name": "kitwork"},
		{"alias": "replica", "type": "postgres", "url": "postgres://kit:pw@replica.internal:5432/kitwork?sslmode=disable"},
		{"alias": "app", "type": "sqlite", "name": "./.data/app.db"},
		{"alias": "cache", "type": "sqlite", "name": ":memory:", "max_open": 1.0},
		{"alias": "legacy", "type": "mysql", "host": "127.0.0.1", "port": 3306.0},
		{"alias": "events", "type": "postgres", "host": "h"},
		{"alias": "analytics", "type": "postgres", "url": "postgresql://a:b@c/d"},
	}
	for i, w := range want {
		got := dbs[i].(map[string]interface{})
		for k, v := range w {
			if got[k] != v {
				t.Errorf("databases[%d].%s = %#v, want %#v (entry %v)", i, k, got[k], v, got)
			}
		}
	}
	cfg, err := ParseConfig(raw)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if len(cfg.Databases) != 7 || cfg.Databases[1].URL == "" {
		t.Fatalf("ParseConfig lost a typed connection: %+v", cfg.Databases)
	}
	if dsn := cfg.Databases[1].DSN(); dsn != "postgres://kit:pw@replica.internal:5432/kitwork?sslmode=disable" {
		t.Fatalf("a URL connection must use the URL as its DSN, got %q", dsn)
	}
}

// Refusals, each one a validation error naming the method — never a silent nothing.
func TestAppConnectionRefusals(t *testing.T) {
	for name, manifest := range map[string]string{
		"missing alias":          `app.postgresql({ host: "h" }).web({ port: 8080 });`,
		"contradicting type":     `app.sqlite("x", { name: "a.db", type: "postgres" }).web({ port: 8080 });`,
		"url scheme vs kind":     `app.mysql("x", "postgres://a@b/c").web({ port: 8080 });`,
		"connect without a kind": `app.connect("x", { host: "h" }).web({ port: 8080 });`,
		"empty path":             `app.database("").web({ port: 8080 });`,
	} {
		file := writeServerJS(t, "import { app } from \"kitwork\";\n"+manifest)
		_, err := evalConfigJS(file)
		if err == nil || !strings.Contains(err.Error(), "app.") {
			t.Errorf("%s: want a validation error naming the method, got %v", name, err)
		}
	}
	// Two connections under one alias would shadow each other at database.connect("alias").
	file := writeServerJS(t, `import { app } from "kitwork";
app.sqlite("main", "a.db").sqlite("main", "b.db").web({ port: 8080 });`)
	raw, err := evalConfigJS(file)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseConfig(raw); err == nil || !strings.Contains(err.Error(), `"main"`) {
		t.Fatalf("duplicate alias must be refused by name, got %v", err)
	}
}

// app.database(path, options) is the DATABASE surface — the app OFFERS KitDB — so a manifest with
// only it is complete, like a desktop-only one. This build carries the declaration as data
// (owned_databases); the RC runtime serves it. The untyped app.database({ type, … }) connection
// object every existing manifest uses keeps working, in the same list as the typed ones.
func TestAppDatabaseSurfaceAndUntypedConnection(t *testing.T) {
	file := writeServerJS(t, `import { app } from "kitwork";
app
  .database("./.database/", { memory: "64mb", concurrency: 2, warm: ["demo"] })
  .database({ alias: "system", type: "postgres", host: "h", port: 5432, user: "u", password: "p", name: "n" })
  .sqlite("local", "x.db");`)
	raw, err := evalConfigJS(file)
	if err != nil {
		t.Fatalf("a database-only manifest declares a surface: %v", err)
	}
	owned, _ := raw["owned_databases"].([]interface{})
	if len(owned) != 1 || owned[0].(map[string]interface{})["path"] != "./.database/" || owned[0].(map[string]interface{})["memory"] != "64mb" {
		t.Fatalf("owned_databases = %v", raw["owned_databases"])
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
