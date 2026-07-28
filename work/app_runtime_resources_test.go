package work

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kitwork/engine/app"
	"github.com/kitwork/engine/database"
	"github.com/kitwork/engine/value"
)

func TestSiblingSitesShareAppDatabaseUntilAppShutdown(t *testing.T) {
	runtime := app.NewRuntime("identity-a")
	firstSite, _ := runtime.Site(t.TempDir(), "first.example")
	secondSite, _ := runtime.Site(t.TempDir(), "second.example")
	firstGeneration, _ := firstSite.PrepareGeneration()
	secondGeneration, _ := secondSite.PrepareGeneration()
	first := NewTenantWithRuntime("", "first.example", runtime, firstSite, firstGeneration)
	second := NewTenantWithRuntime("", "second.example", runtime, secondSite, secondGeneration)

	alias := "shared-app-test"
	config := database.Config{
		Alias: alias,
		Type:  "sqlite",
		Name:  filepath.Join(t.TempDir(), "shared.db"),
	}
	previous, hadPrevious := database.Configs[alias]
	database.Configs[alias] = config
	t.Cleanup(func() {
		if hadPrevious {
			database.Configs[alias] = previous
		} else {
			delete(database.Configs, alias)
		}
	})

	firstDB := (&KitWork{tenant: first}).Database().Connect(value.New(alias)).sqlDB
	secondDB := (&KitWork{tenant: second}).Database().Connect(value.New(alias)).sqlDB
	if firstDB == nil || firstDB != secondDB {
		t.Fatal("sibling sites did not share the app-owned configured database")
	}

	first.Close()
	runtime.RemoveSite("first.example")
	if err := secondDB.Ping(); err != nil {
		t.Fatalf("site eviction closed an app database: %v", err)
	}

	second.Close()
	runtime.Close()
	if err := secondDB.Ping(); err == nil {
		t.Fatal("app shutdown left its database connection open")
	}
}

func TestSchedulerResourceSurvivesFacadeCloseAndStopsWithApp(t *testing.T) {
	root := t.TempDir()
	cronDir := filepath.Join(root, "identity-a", "_cron")
	if err := os.MkdirAll(cronDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(cronDir, "heartbeat.kitwork.js"),
		[]byte(`import { cron } from "kitwork";
cron.every("1h").handle(() => {});`),
		0o644,
	); err != nil {
		t.Fatal(err)
	}

	runtime := app.NewRuntime("identity-a")
	tenant := NewAppTenantWithRuntime(root, "identity-a", runtime)
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	scheduler := tenant.scheduler()
	if scheduler == nil || scheduler.closed {
		t.Fatal("app scheduler was not installed")
	}

	tenant.Close()
	if scheduler.closed {
		t.Fatal("closing a compatibility facade stopped the app scheduler")
	}
	runtime.Close()
	if !scheduler.closed {
		t.Fatal("app shutdown did not stop its scheduler")
	}
}
