package work

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kitwork/engine/app"
	"github.com/kitwork/engine/compiler"
)

// writeAppTree writes files under root (paths with "/").
func writeAppTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for relative, content := range files {
		full := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// A router.work of any folder of a site imports from its app's root with `app/…` (22/10/2026,
// KITWORK_UI.md mục 4): apps/<identity>/.shared/ for a host of many apps, through the generation's
// bytecode cache, which every app of the host shares. Neither the site's own .shared/ nor another app's
// is reached.
func TestRouterImportsFromTheAppRoot(t *testing.T) {
	root := t.TempDir()
	writeAppTree(t, root, map[string]string{
		"acme/.shared/demo.work": `export const listDemos = () => [{ name: "http", ready: true }, { name: "queue", ready: false }];`,
		// the site's own .shared/ and another app's: same names, never reached
		"acme/shop.example/.shared/demo.work": `export const listDemos = () => [{ name: "site" }];`,
		"other/.shared/demo.work":             `export const listDemos = () => [{ name: "other" }];`,
		"other/other.example/router.work":     `import * as router from "kitwork/router";`,

		"acme/shop.example/router.work": `import * as router from "kitwork/router";
import { listDemos } from "app/.shared/demo.work";
router.get((context) => context.json({ demos: listDemos() }));`,
		"acme/shop.example/view.ui": `<view><content /></view>`,
		"acme/shop.example/demos/router.work": `import * as router from "kitwork/router";
import { listDemos } from "app/.shared/demo.work";
router.get((context) => context.title("Demos").view({ demos: listDemos() }));`,
		"acme/shop.example/demos/content.ui": `<content><list><item for="demo of demos; key: demo.name"><text>DEMO-{{ demo.name }}</text></item></list></content>`,
	})

	appRuntime := app.NewRuntime("acme")
	t.Cleanup(appRuntime.Close)
	siteRuntime, err := appRuntime.Site(root, "shop.example")
	if err != nil {
		t.Fatal(err)
	}
	generation, err := siteRuntime.PrepareGeneration()
	if err != nil {
		t.Fatal(err)
	}
	if err := generation.SetBytecodeCache(compiler.NewFileCache(filepath.Join(t.TempDir(), "bytecode"))); err != nil {
		t.Fatal(err)
	}
	tn := NewTenantWithRuntimeLayout(root, "shop.example", RootLayoutMultiTenant, appRuntime, siteRuntime, generation)
	t.Cleanup(tn.Close)
	if err := tn.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}

	api := serveGet(tn, "/", nil)
	if api.Code != http.StatusOK || api.Body.String() != `{"demos":[{"name":"http","ready":true},{"name":"queue","ready":false}]}` {
		t.Fatalf("/: %d %q", api.Code, api.Body.String())
	}
	page := serveGet(tn, "/demos", nil)
	body := page.Body.String()
	if page.Code != http.StatusOK || !strings.Contains(body, "DEMO-http") || !strings.Contains(body, "DEMO-queue") ||
		strings.Contains(body, "DEMO-site") || strings.Contains(body, "DEMO-other") {
		t.Fatalf("/demos: %d\n%.800s", page.Code, body)
	}
}

// A router.work whose app/ import names a file the app does not have does not boot, and the error names
// the router, the place and the path from the app's root. The site's own .shared/ has the file: app/ is
// the app's folder, so the site's copy does not count.
func TestRouterAppImportOfAMissingFileFailsTheBoot(t *testing.T) {
	root := t.TempDir()
	writeAppTree(t, root, map[string]string{
		"acme/shop.example/router.work":       "import * as router from \"kitwork/router\";\nimport { listDemos } from \"app/.shared/nope.work\";",
		"acme/shop.example/.shared/nope.work": `export const listDemos = () => [];`,
	})
	appRuntime := app.NewRuntime("acme")
	t.Cleanup(appRuntime.Close)
	siteRuntime, err := appRuntime.Site(root, "shop.example")
	if err != nil {
		t.Fatal(err)
	}
	tn := NewTenantWithRuntimeLayout(root, "shop.example", RootLayoutMultiTenant, appRuntime, siteRuntime)
	t.Cleanup(tn.Close)
	err = tn.Run()
	want := `router.work:2:27: import "app/.shared/nope.work": app không có file .shared/nope.work`
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("run: %v, want %q", err, want)
	}
}

// The loaders that run the app's background files at boot (LoadScheduleFiles, LoadQueueFiles, from Run)
// compile them as the app's: an app/ import resolves from the app's folder, so the schedule and the job
// register under their export's name, with what the import gave them. A loader that compiled them as no
// app's would print "[Schedule] compile …: file này không thuộc app nào" and register nothing.
func TestBackgroundLoadersImportFromTheAppRoot(t *testing.T) {
	root := t.TempDir()
	writeAppTree(t, root, map[string]string{
		"acme/.shared/plan.work":           "export const every = \"1h\";\nexport const attempts = 4;",
		"acme/.schedule/daily.work":        "import * as schedule from \"kitwork/schedule\";\nimport { every } from \"app/.shared/plan.work\";\nexport const daily = schedule.every(every).run((context) => {});",
		"acme/.shared/queue/mail.work":     "import * as queue from \"kitwork/queue\";\nimport { attempts } from \"app/.shared/plan.work\";\nexport const mail = queue.job((context) => {}).retry(attempts);",
		"acme/localhost/router.kitwork.js": `import { router } from "kitwork"; router.get((ctx) => ctx.text("ok"));`,
		// another app with the same names: never reached from acme
		"other/.shared/plan.work": "export const every = \"2h\";\nexport const attempts = 9;",
	})
	tenant := NewAppTenant(root, "acme")
	if err := tenant.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	defer tenant.Close()

	if entry := tenant.scheduleWorker().scheduleEntry("daily"); entry == nil || entry.rule.Text() != `every("1h")` {
		t.Errorf("schedule daily: %+v, want every(\"1h\") from the app's .shared/plan.work (nil: .schedule/daily.work did not compile as the app's)", entry)
	}

	worker := tenant.queueWorker()
	if worker == nil {
		t.Fatal("the queue worker did not start")
	}
	handler := worker.handlerFor("mail")
	if handler == nil || handler.Retry != 4 {
		t.Errorf("queue mail: handler = %+v, want Retry 4 from the app's .shared/plan.work (nil: .shared/queue/mail.work did not compile as the app's)", handler)
	}
}

// The app's background files (.schedule/, .shared/queue/) are the app's too: their app/ imports resolve from
// its folder in the preflight (CheckScheduleFiles, CheckQueueFiles) as in the loaders above.
func TestBackgroundFilesImportFromTheAppRoot(t *testing.T) {
	root := t.TempDir()
	writeAppTree(t, root, map[string]string{
		"acme/.shared/report.work":        `export const report = () => "ok";`,
		"acme/.schedule/daily.work":       "import { report } from \"app/.shared/report.work\";\nreport();",
		"acme/.shared/queue/mail.work":    "import { report } from \"app/.shared/report.work\";\nreport();",
		"acme/.shared/queue/broken.work":  "import { gone } from \"app/.shared/gone.work\";",
		"acme/shop.example/router.work":   `import * as router from "kitwork/router";`,
		"other/.shared/report.work":       `export const report = () => "other";`,
		"other/other.example/router.work": `import * as router from "kitwork/router";`,
	})
	if issues := CheckScheduleFiles(root, "acme"); len(issues) != 0 {
		t.Fatalf("schedule issues = %v", issues)
	}
	issues := CheckQueueFiles(root, "acme")
	if len(issues) != 1 || filepath.Base(issues[0].File) != "broken.work" ||
		!strings.Contains(issues[0].Err.Error(), `import "app/.shared/gone.work": app không có file .shared/gone.work`) {
		t.Fatalf("queue issues = %v, want only broken.work, missing .shared/gone.work", issues)
	}
}
