package core

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/kitwork/engine/database"
	"github.com/kitwork/engine/work"
)

func TestEngineSingleRootServesTheAppDirectory(t *testing.T) {
	root := t.TempDir()
	writeRootLayoutRouter(t, root, "single")

	engine := New(root, 100_000, false, "example.com")
	defer engine.Close()
	if err := engine.SetRootLayout(work.RootLayoutSingle); err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "single" {
		t.Fatalf("single response = %d %q", response.Code, response.Body.String())
	}
}

func TestEngineMultiDomainRootUsesOnlyFlatDomainFolders(t *testing.T) {
	root := t.TempDir()
	writeRootLayoutRouter(t, filepath.Join(root, "example.com"), "flat")
	writeRootLayoutRouter(t, filepath.Join(root, "second.example"), "second")
	writeRootLayoutRouter(t, filepath.Join(root, "identity", "nested.example"), "nested")

	engine := New(root, 100_000, false, "")
	defer engine.Close()
	if err := engine.SetRootLayout(work.RootLayoutMultiDomain); err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "flat" {
		t.Fatalf("flat response = %d %q", response.Code, response.Body.String())
	}

	second := httptest.NewRecorder()
	secondRequest := httptest.NewRequest(http.MethodGet, "http://second.example/", nil)
	engine.ServeHTTP(second, secondRequest)
	if second.Code != http.StatusOK || second.Body.String() != "second" {
		t.Fatalf("second response = %d %q", second.Code, second.Body.String())
	}
	firstTenant := engine.cache["example.com"].current()
	secondTenant := engine.cache["second.example"].current()
	if firstTenant.AppRuntime() != secondTenant.AppRuntime() {
		t.Fatal("multi-domain sites did not share one app runtime")
	}
	if firstTenant.AppRuntime().ID() != work.RootAppIdentity {
		t.Fatalf("app runtime ID = %q, want %q", firstTenant.AppRuntime().ID(), work.RootAppIdentity)
	}
	if firstTenant.AppID() != "app" || secondTenant.AppID() != "app" {
		t.Fatalf("logical app IDs = %q and %q, want app", firstTenant.AppID(), secondTenant.AppID())
	}

	nested := httptest.NewRecorder()
	nestedRequest := httptest.NewRequest(http.MethodGet, "http://nested.example/", nil)
	engine.ServeHTTP(nested, nestedRequest)
	if nested.Code != http.StatusNotFound {
		t.Fatalf("nested response = %d %q, want 404", nested.Code, nested.Body.String())
	}
}

func TestEngineMultiTenantRootResolvesFilesystemIdentityWithoutSystemDatabase(t *testing.T) {
	root := t.TempDir()
	writeRootLayoutRouter(t, filepath.Join(root, "identity", "example.com"), "tenant")
	previousSystem := database.System
	database.System = nil
	t.Cleanup(func() { database.System = previousSystem })

	engine := New(root, 100_000, false, "")
	defer engine.Close()
	if err := engine.SetRootLayout(work.RootLayoutMultiTenant); err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "tenant" {
		t.Fatalf("response = %d %q, want tenant", response.Code, response.Body.String())
	}
	tenant := engine.cache["example.com"].current()
	if tenant.AppID() != "identity" {
		t.Fatalf("app ID = %q, want identity", tenant.AppID())
	}
}

func TestEngineMultiTenantRootSeparatesAppRuntimesByIdentity(t *testing.T) {
	root := t.TempDir()
	writeRootLayoutRouter(t, filepath.Join(root, "first", "one.example"), "one")
	writeRootLayoutRouter(t, filepath.Join(root, "second", "two.example"), "two")
	previousSystem := database.System
	database.System = nil
	t.Cleanup(func() { database.System = previousSystem })

	engine := New(root, 100_000, false, "")
	t.Cleanup(engine.Close)
	if err := engine.SetRootLayout(work.RootLayoutMultiTenant); err != nil {
		t.Fatal(err)
	}
	one, err := engine.run("one.example")
	if err != nil {
		t.Fatal(err)
	}
	two, err := engine.run("two.example")
	if err != nil {
		t.Fatal(err)
	}
	if one.AppRuntime() == two.AppRuntime() {
		t.Fatal("different app identities shared one app runtime")
	}
	if one.AppID() != "first" || two.AppID() != "second" {
		t.Fatalf("app IDs = %q and %q, want first and second", one.AppID(), two.AppID())
	}
}

func TestEngineMultiDomainSchedulerSharesTheRootAppRuntime(t *testing.T) {
	root := t.TempDir()
	writeRootLayoutRouter(t, filepath.Join(root, "example.com"), "site")
	cronDir := filepath.Join(root, "_cron")
	if err := os.MkdirAll(cronDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cron := `import { cron } from "kitwork";
cron.every("1h").handle(() => {});`
	if err := os.WriteFile(filepath.Join(cronDir, "heartbeat.kitwork.js"), []byte(cron), 0o644); err != nil {
		t.Fatal(err)
	}

	engine := New(root, 100_000, false, "")
	t.Cleanup(engine.Close)
	if err := engine.SetRootLayout(work.RootLayoutMultiDomain); err != nil {
		t.Fatal(err)
	}
	if started := engine.StartAppSchedulers(); started != 1 {
		t.Fatalf("started schedulers = %d, want 1", started)
	}

	tenant, err := engine.run("example.com")
	if err != nil {
		t.Fatal(err)
	}
	appTenant := engine.appTenants[work.RootAppIdentity]
	if appTenant == nil {
		t.Fatal("root app scheduler tenant was not registered")
	}
	if tenant.AppRuntime() != appTenant.AppRuntime() {
		t.Fatal("root app scheduler and domain site do not share one app runtime")
	}
	if tenant.AppID() != "app" || appTenant.AppID() != "app" {
		t.Fatalf("logical app IDs = %q and %q, want app", tenant.AppID(), appTenant.AppID())
	}
}

func writeRootLayoutRouter(t testing.TB, directory, body string) {
	t.Helper()
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	source := `import { router } from "kitwork";
router.get(() => "` + body + `");`
	if err := os.WriteFile(filepath.Join(directory, work.RouterFileName), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
}
