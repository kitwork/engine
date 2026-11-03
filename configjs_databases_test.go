package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kitwork/engine/database"
	"github.com/kitwork/engine/work"
)

// app.databases({ … }) of application.work is the host's catalogue of named databases
// (KITWORK_DATABASE.md mục 1, "Host và tên database"): an app picks one with connection("tên") in its
// .shared/schema/. Each entry is one connector; a SQLite path is read from the manifest's folder. The host's
// own connections (app.postgresql, app.connect) stay outside it, so no app reaches them.

func writeApplication(t *testing.T, body string, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, text := range files {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	file := filepath.Join(dir, "application.work")
	source := "import * as app from \"kitwork/application\";\nimport * as file from \"kitwork/file\";\nconst env = file.env(\"./.env\");\n" + body + "\napp.run(8080);\n"
	if err := os.WriteFile(file, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	return file
}

func TestAppDatabasesCatalogue(t *testing.T) {
	file := writeApplication(t, `app.postgresql({ alias: "system", url: "postgres://kit:pw@db.internal:5432/kitwork" });
app.databases({ warehouse: { sqlite: "./data/warehouse.db" }, crm: { postgresql: env.CRM_URL } });
app.databases({ archive: { sqlite: "/srv/archive.db" } });`, map[string]string{".env": "CRM_URL=postgres://crm:secret@crm.example:5432/crm\n"})
	raw, err := evalConfigJS(file)
	if err != nil {
		t.Fatalf("evalConfigJS: %v", err)
	}
	cfg, err := ParseConfig(raw)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if len(cfg.DatabaseCatalog) != 3 {
		t.Fatalf("catalogue = %+v, want warehouse, crm, archive", cfg.DatabaseCatalog)
	}
	warehouse := cfg.DatabaseCatalog["warehouse"]
	wantPath, _ := filepath.Abs(filepath.Join(filepath.Dir(file), "data", "warehouse.db"))
	if warehouse.Type != "sqlite" || warehouse.Name != wantPath || warehouse.Alias != "warehouse" {
		t.Errorf("warehouse = %+v, want sqlite at %s (the manifest's folder)", warehouse, wantPath)
	}
	if crm := cfg.DatabaseCatalog["crm"]; crm.Type != "postgres" || crm.URL != "postgres://crm:secret@crm.example:5432/crm" {
		t.Errorf("crm = %+v, want postgres with the URL of the host's env", crm)
	}
	if archive := cfg.DatabaseCatalog["archive"]; archive.Type != "sqlite" || !filepath.IsAbs(archive.Name) {
		t.Errorf("archive = %+v, want sqlite at an absolute path", archive)
	}
	// the host's own connection is not in the catalogue, and the catalogue is not the host's aliases
	if _, inCatalogue := cfg.DatabaseCatalog["system"]; inCatalogue || len(cfg.Databases) != 1 || cfg.Databases[0].Alias != "system" {
		t.Errorf("host connections %+v, catalogue %+v: system stays the host's alone", cfg.Databases, cfg.DatabaseCatalog)
	}
	if !cfg.TenantConnections {
		t.Error("connect() is allowed unless the host says app.policy({ tenantConnections: false })")
	}
}

func TestAppDatabasesRefusals(t *testing.T) {
	for _, test := range []struct{ body, want string }{
		{`app.databases({ application: { postgresql: "postgres://h/db" } });`, "application is the app's own database"},
		{`app.databases({ storage: { postgresql: "postgres://h/db" } });`, "storage is the name of datastore()'s store"},
		{`app.databases({ files: { sqlite: "./files.db" } });`, "files is the name of the shared file store"},
		{`app.databases({ w: { sqlite: "./a.db" } });` + "\n" + `app.databases({ w: { sqlite: "./b.db" } });`, "w is declared twice"},
		{`app.databases({ w: { sqlite: "./a.db", postgresql: "postgres://h/db" } });`, "w is one connector"},
		{`app.databases({ w: { redis: "redis://h" } });`, "w.redis is not a connector"},
		{`app.databases({ w: { postgresql: env.MISSING } });`, "w.postgresql needs a non-empty string"},
		{`app.databases({ w: { postgresql: "mysql://h/db" } });`, "w.postgresql has a URL of another connector"},
		{`app.databases([{ alias: "w", type: "sqlite", name: "./a.db" }]);`, "app.databases expects one object of named databases"},
		{`app.policy({ requirePolicy: true });`, "requirePolicy needs policy() in .shared/schema/"},
		{`app.policy({ tenantConnections: "no" });`, "tenantConnections is true or false"},
		{`app.policy({ sharing: true });`, "sharing is not a policy of this engine"},
	} {
		file := writeApplication(t, test.body, nil)
		_, err := evalConfigJS(file)
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Errorf("%s\n  got  %v\n  want …%s…", test.body, err, test.want)
		}
		if err != nil && strings.Contains(err.Error(), "postgres://h/db") {
			t.Errorf("%s: the error repeats the URL: %v", test.body, err)
		}
	}
}

func TestAppPolicyTurnsTenantConnectionsOff(t *testing.T) {
	file := writeApplication(t, `app.policy({ tenantConnections: false });`, nil)
	raw, err := evalConfigJS(file)
	if err != nil {
		t.Fatalf("evalConfigJS: %v", err)
	}
	cfg, err := ParseConfig(raw)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if cfg.TenantConnections {
		t.Fatal("app.policy({ tenantConnections: false }) must turn connect() off")
	}
}

// A catalogue entry whose connector this build does not link is said once at boot, by its name.
func TestAppDatabasesUnlinkedConnectorNotice(t *testing.T) {
	file := writeApplication(t, `app.databases({ legacy: { mysql: "mysql://u:p@h/shop" }, warehouse: { sqlite: "./w.db" } });`, nil)
	builder, err := evalServerBuilder(file)
	if err != nil {
		t.Fatal(err)
	}
	notices := strings.Join(builder.bootNotices(), "\n")
	if !strings.Contains(notices, `app.databases: legacy (mysql): no driver linked in this build — connection("legacy") cannot open it`) {
		t.Fatalf("notices = %q", notices)
	}
	if strings.Contains(notices, "warehouse") || strings.Contains(notices, "u:p@") {
		t.Fatalf("a linked connector or the URL is in the notices: %q", notices)
	}
}

// What application.work says an app's .shared/schema/ may reach reaches the engine on both boot paths:
// Check (kitwork check), and Run, whose boot is stopped right after it at the host's system database, a
// connector this build does not link. Each half (the parse, the engine's use) has its tests; this is the
// link between them, applyDatabaseManifest.
func TestDatabaseManifestReachesTheEngine(t *testing.T) {
	t.Cleanup(func() {
		database.SetCatalog(nil)
		work.SetTenantConnections(true)
		work.SetHistoryDays(0)
	})
	for _, path := range []string{"check", "run"} {
		database.SetCatalog(nil)
		work.SetTenantConnections(true)
		work.SetHistoryDays(0)
		root := t.TempDir()
		body := fmt.Sprintf(`app.root(%q);
app.databases({ warehouse: { sqlite: "./data/warehouse.db" } });
app.policy({ tenantConnections: false });
app.history({ days: 90 });`, filepath.ToSlash(root))
		if path == "run" {
			body += "\n" + `app.mysql({ alias: "system", host: "127.0.0.1", port: 3306, user: "root", password: "", name: "kitwork" });`
		}
		manifest := writeApplication(t, body, nil)
		var bootErr error
		captureBootStdout(t, func() {
			if path == "check" {
				_, bootErr = Check(manifest)
			} else {
				bootErr = Run(manifest)
			}
		})
		switch {
		case path == "check" && bootErr != nil:
			t.Fatalf("check: %v", bootErr)
		case path == "run" && (bootErr == nil || !strings.Contains(bootErr.Error(), "failed to connect to system database")):
			t.Fatalf("run must stop at the system database, after the manifest's databases: %v", bootErr)
		}
		entry, declared := database.CatalogEntry("warehouse")
		want, _ := filepath.Abs(filepath.Join(filepath.Dir(manifest), "data", "warehouse.db"))
		if !declared || entry.Type != "sqlite" || entry.Name != want {
			t.Errorf("%s: the engine's catalogue has warehouse = %+v (%v), want sqlite at %s", path, entry, declared, want)
		}
		if work.TenantConnections() {
			t.Errorf("%s: app.policy({ tenantConnections: false }) did not reach the engine: connect() is still allowed", path)
		}
		if days := work.HistoryDays(); days != 90 {
			t.Errorf("%s: app.history({ days: 90 }) did not reach the engine: the history keeps %d days", path, days)
		}
	}
}
