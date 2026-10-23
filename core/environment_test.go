package core

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kitwork/engine/work"
)

// Every environment.require("KEY") is checked at boot against the app's .env even when the code comes from
// the bytecode cache (docs/KITWORK_ENVIRONMENT.md mục 3, 22/10/2026): an artifact holds code alone, so the
// keys are read off the program again on a hit. Changing only .env, which the cache key does not cover, must
// still stop the boot. A key application.work shares counts as the app's (CheckWithEnvironment).
//
// CONTROL (22/10/2026): with FileCache's hit giving the artifact without reading the program again, the
// second check found nothing: "after .env lost the key: 0 issues, want the missing STRIPE_SECRET".
func TestEnvironmentRequireSurvivesBytecodeCache(t *testing.T) {
	root := t.TempDir()
	site := filepath.Join(root, "acme", "localhost")
	if err := os.MkdirAll(site, 0o755); err != nil {
		t.Fatal(err)
	}
	router := "import * as router from \"kitwork/router\";\nimport { environment } from \"kitwork\";\n" +
		"const stripe = environment.require(\"STRIPE_SECRET\");\nrouter.get((context) => context.json({ set: stripe.length > 0 }));\n"
	if err := os.WriteFile(filepath.Join(site, "router.work"), []byte(router), 0o644); err != nil {
		t.Fatal(err)
	}
	appEnv := filepath.Join(root, "acme", ".env")
	if err := os.WriteFile(appEnv, []byte("STRIPE_SECRET=sk-test-1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cache := t.TempDir()

	first := CheckWithEnvironment(root, 0, work.RootLayoutMultiTenant, nil, cache)
	if !first.OK() || first.Valid != 1 {
		t.Fatalf("with the key: %+v", first.Issues)
	}
	artifacts, _ := filepath.Glob(filepath.Join(cache, "*.kwbc"))
	if len(artifacts) == 0 {
		t.Fatal("the first check wrote no artifact: the second would not read the cache")
	}

	if err := os.WriteFile(appEnv, []byte("OTHER=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	second := CheckWithEnvironment(root, 0, work.RootLayoutMultiTenant, nil, cache)
	const want = "environment.require: thiếu STRIPE_SECRET (router.work:3:36)"
	if len(second.Issues) != 1 || !strings.Contains(second.Issues[0].Err.Error(), want) {
		t.Fatalf("after .env lost the key: %d issues %v, want the missing STRIPE_SECRET", len(second.Issues), second.Issues)
	}

	shared := &work.SharedEnvironment{Apps: map[string]map[string]work.EnvironmentEntry{"acme": {"STRIPE_SECRET": {Text: "sk-from-host"}}}}
	if third := CheckWithEnvironment(root, 0, work.RootLayoutMultiTenant, shared, cache); !third.OK() {
		t.Fatalf("with the key shared by the host: %v", third.Issues)
	}
}

// What application.work shares reaches a site the engine serves (SetSharedEnvironment, then every tenant
// it builds), for the app named and not for another.
//
// CONTROL (22/10/2026): with prepareTenantCandidate not handing the tenant what the engine holds, "localhost/ =
// 200 {\"all\":null,\"mine\":null}, want {\"all\":\"https://sentry\",\"mine\":\"smtp://host\"}".
func TestEngineHandsTheSharedEnvironmentToItsSites(t *testing.T) {
	root := t.TempDir()
	for identity, domain := range map[string]string{"acme": "localhost", "other": "other.localhost"} {
		site := filepath.Join(root, identity, domain)
		if err := os.MkdirAll(site, 0o755); err != nil {
			t.Fatal(err)
		}
		router := "import * as router from \"kitwork/router\";\nimport { environment } from \"kitwork\";\n" +
			"router.get((context) => context.json({ mine: environment.SMTP_URL, all: environment.SENTRY_DSN }));\n"
		if err := os.WriteFile(filepath.Join(site, "router.work"), []byte(router), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	engine := New(root, 0, false, "")
	t.Cleanup(engine.Close)
	if err := engine.SetRootLayout(work.RootLayoutMultiTenant); err != nil {
		t.Fatal(err)
	}
	engine.SetSharedEnvironment(&work.SharedEnvironment{
		All:  map[string]work.EnvironmentEntry{"SENTRY_DSN": {Text: "https://sentry"}},
		Apps: map[string]map[string]work.EnvironmentEntry{"acme": {"SMTP_URL": {Text: "smtp://host"}}},
	})
	for host, want := range map[string]string{
		"localhost":       `{"all":"https://sentry","mine":"smtp://host"}`,
		"other.localhost": `{"all":"https://sentry","mine":null}`,
	} {
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "http://"+host+"/", nil))
		if recorder.Body.String() != want {
			t.Errorf("%s/ = %d %s, want %s", host, recorder.Code, recorder.Body.String(), want)
		}
	}
}

// kitwork check holds what the app's jobs and schedules require against the app's tier of its environment
// (KITWORK_ENVIRONMENT.md mục 3, 22/10/2026), as the app's tenant does at boot, where such a file is held back
// with only a line in the log: a key the app has not is an issue of the file that requires it, by name and
// place, never a value; a key application.work shares with the app is there.
//
// CONTROL (23/10/2026): with checkFolder holding nothing against the environment, the check of an app whose
// job requires a key nobody declares was clean: "issues [], want the queue file naming SMTP_URL".
func TestCheckHoldsBackgroundRequiresAgainstTheApp(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		filepath.Join("acme", ".env"):                          "HAVE=secret-xyz\n",
		filepath.Join("acme", "localhost", "router.work"):      "import * as router from \"kitwork/router\";\nrouter.get((context) => context.text(\"ok\"));\n",
		filepath.Join("acme", ".shared", "queue", "mail.work"): "import * as queue from \"kitwork/queue\";\nimport { environment } from \"kitwork\";\nexport const mail = queue.job((context) => environment.require(\"SMTP_URL\").length + environment.require(\"HAVE\").length);\n",
		filepath.Join("acme", ".schedule", "heartbeat.work"):   "import * as schedule from \"kitwork/schedule\";\nimport { environment } from \"kitwork\";\nexport const heartbeat = schedule.every(\"1h\").run((context) => environment.require(\"HAVE\").length);\n",
	}
	for relative, content := range files {
		full := filepath.Join(root, relative)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	report := CheckWithEnvironment(root, 0, work.RootLayoutMultiTenant, nil)
	const want = "environment.require: thiếu SMTP_URL (mail.work:3:64)"
	if len(report.Issues) != 1 || report.Issues[0].Stage != "queue compile" || !strings.Contains(report.Issues[0].Err.Error(), want) ||
		filepath.Base(report.Issues[0].File) != "mail.work" || strings.Contains(report.Issues[0].Err.Error(), "secret-xyz") {
		t.Fatalf("issues %v, want the queue file naming SMTP_URL", report.Issues)
	}
	shared := &work.SharedEnvironment{Apps: map[string]map[string]work.EnvironmentEntry{"acme": {"SMTP_URL": {Text: "smtp://host"}}}}
	if report := CheckWithEnvironment(root, 0, work.RootLayoutMultiTenant, shared); !report.OK() {
		t.Fatalf("with SMTP_URL shared by the host: %v", report.Issues)
	}
}

// The tenant that runs an app's jobs and schedules (StartAppSchedulers) receives what application.work shares
// with the app, as the tenants of its sites do: a job that requires a key only the host gives loads.
//
// CONTROL (23/10/2026): with StartAppSchedulers not handing the app's tenant the shared environment, the job
// was held back: "the boot said [Queue] compile .shared/queue/mail.work: environment.require: thiếu SMTP_URL …".
func TestEngineHandsTheSharedEnvironmentToTheAppsBackground(t *testing.T) {
	root := t.TempDir()
	queueDir := filepath.Join(root, "acme", ".shared", "queue")
	if err := os.MkdirAll(queueDir, 0o755); err != nil {
		t.Fatal(err)
	}
	job := "import * as queue from \"kitwork/queue\";\nimport { environment } from \"kitwork\";\nexport const mail = queue.job((context) => environment.require(\"SMTP_URL\").length);\n"
	if err := os.WriteFile(filepath.Join(queueDir, "mail.work"), []byte(job), 0o644); err != nil {
		t.Fatal(err)
	}
	engine := New(root, 0, false, "")
	t.Cleanup(engine.Close)
	if err := engine.SetRootLayout(work.RootLayoutMultiTenant); err != nil {
		t.Fatal(err)
	}
	engine.SetSharedEnvironment(&work.SharedEnvironment{Apps: map[string]map[string]work.EnvironmentEntry{"acme": {"SMTP_URL": {Text: "smtp://host"}}}})
	var started int
	said := captureEnvironmentOutput(t, func() { started = engine.StartAppSchedulers() })
	if started != 1 {
		t.Fatalf("started %d app tenants, want 1", started)
	}
	if strings.Contains(said, "environment.require") || !strings.Contains(said, `1 job cho "acme": mail`) {
		t.Fatalf("the boot said %s, want the job mail loaded", said)
	}
}

// Editing the app's .env (apps/<identity>/.env, the tier every site of the app reads) while developing builds
// the site anew, as editing the site's own .env does (mục 5).
//
// CONTROL (23/10/2026): with the tenant watching only the site's .env, the edit of the app's was never seen:
// "reloaded environment: 200 \"app-v1\", want app-v2".
func TestEngineReloadsTheAppsEnvironment(t *testing.T) {
	root := t.TempDir()
	site := filepath.Join(root, "acme", "localhost")
	if err := os.MkdirAll(site, 0o755); err != nil {
		t.Fatal(err)
	}
	router := "import * as router from \"kitwork/router\";\nimport { environment } from \"kitwork\";\nrouter.get((context) => context.text(environment.MESSAGE));\n"
	if err := os.WriteFile(filepath.Join(site, "router.work"), []byte(router), 0o644); err != nil {
		t.Fatal(err)
	}
	appEnv := filepath.Join(root, "acme", ".env")
	if err := os.WriteFile(appEnv, []byte("MESSAGE=app-v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	engine := New(root, 0, true, "")
	t.Cleanup(engine.Close)
	if err := engine.SetRootLayout(work.RootLayoutMultiTenant); err != nil {
		t.Fatal(err)
	}
	request := func() *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "http://localhost/", nil))
		return recorder
	}
	if response := request(); response.Code != http.StatusOK || response.Body.String() != "app-v1" {
		t.Fatalf("baseline: %d %q", response.Code, response.Body.String())
	}
	if err := os.WriteFile(appEnv, []byte("MESSAGE=app-v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)
	if response := request(); response.Code != http.StatusOK || response.Body.String() != "app-v2" {
		t.Fatalf("reloaded environment: %d %q, want app-v2", response.Code, response.Body.String())
	}
}

// captureEnvironmentOutput runs fn and gives what it printed.
func captureEnvironmentOutput(t *testing.T, fn func()) string {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stdout
	os.Stdout = writer
	printed := make(chan string)
	go func() {
		data, _ := io.ReadAll(reader)
		printed <- string(data)
	}()
	func() {
		defer func() { os.Stdout = saved; writer.Close() }()
		fn()
	}()
	return <-printed
}
