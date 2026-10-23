package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kitwork/engine/work"
)

// check and profile compile every router the way the host does, so an `app/…` import (22/10/2026)
// resolves from the folder of the router's app in every layout: apps/<identity>/ when the root holds
// many apps, the root itself when it is the one app. Until then the compatibility pass of check and
// every program of profile compiled with no app, where `app/…` is an error.
func TestCheckAndProfileResolveAppImports(t *testing.T) {
	root := t.TempDir()
	write := func(relative, body string) {
		t.Helper()
		file := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, identity := range []string{"acme", "other"} {
		write(identity+"/.shared/demo.work", `export const listDemos = () => ["http"];`)
		write(identity+"/shop-"+identity+".example/router.work", `import * as router from "kitwork/router";
import { listDemos } from "app/.shared/demo.work";
router.get((context) => context.json({ demos: listDemos() }));`)
	}

	report := CheckWithLayout(root, 100_000, work.RootLayoutMultiTenant, filepath.Join(t.TempDir(), "bytecode"))
	if !report.OK() {
		for _, issue := range report.Issues {
			t.Log(issue.Error())
		}
		t.Fatalf("multi-tenant check: %d issues", len(report.Issues))
	}
	if report.Valid != 2 || report.Programs != 2 || report.Compatible != 2 {
		t.Fatalf("multi-tenant check = %+v, want two sites prepared and two programs compatible", report)
	}
	assertProfileFiles(t, ProfileWithLayout(root, work.RootLayoutMultiTenant),
		[]string{"acme/shop-acme.example/router.work", "other/shop-other.example/router.work"})
	assertProfileFiles(t, Profile(root),
		[]string{"acme/shop-acme.example/router.work", "other/shop-other.example/router.work"})

	// one app at the root: its folder is the app
	app := filepath.Join(root, "acme")
	single := CheckWithLayout(app, 100_000, work.RootLayoutMultiDomain)
	if !single.OK() || single.Programs != 1 || single.Compatible != 1 {
		for _, issue := range single.Issues {
			t.Log(issue.Error())
		}
		t.Fatalf("one app: report = %+v", single)
	}
	assertProfileFiles(t, ProfileWithLayout(app, work.RootLayoutMultiDomain), []string{"shop-acme.example/router.work"})
}

// kitwork check finds the queue's old spellings in a route, at their line (KITWORK_QUEUE.md mục 14): before,
// a route that still wrote queue.dispatch, queue.jobs or a stray queue.job compiled clean and failed only
// when a visitor reached the line. CONTROL: the route written in the spec's spelling checks clean, and the
// job file is one of its programs, compiled as the module the loader runs (as an entry its queue.job would
// stand outside .shared/queue/, and the compatibility pass skipped it without a word).
func TestCheckFindsTheQueueOldSpellings(t *testing.T) {
	root := t.TempDir()
	write := func(relative, body string) {
		t.Helper()
		file := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("acme/.shared/queue/camOn.work", "import * as queue from \"kitwork/queue\";\nexport const camOn = queue.job((context) => 1);\n")
	route := "import * as router from \"kitwork/router\";\nimport * as queue from \"kitwork/queue\";\nimport { camOn } from \"app/.shared/queue/camOn.work\";\n"
	write("acme/shop.example/router.work", route+"router.get((context) => context.json(camOn.send({})));\n")
	if report := CheckWithLayout(root, 100_000, work.RootLayoutMultiTenant); !report.OK() || report.Programs != 2 || report.Compatible != 2 {
		for _, issue := range report.Issues {
			t.Log(issue.Error())
		}
		t.Fatalf("control: the spec's spelling has %d issues, %d programs (%d compatible), want none and the route and the job", len(report.Issues), report.Programs, report.Compatible)
	}

	write("acme/shop.example/router.work", route+"router.get((context) => context.json({ sent: queue.dispatch(\"camOn\", {}) }));\n")
	report := CheckWithLayout(root, 100_000, work.RootLayoutMultiTenant)
	found := false
	for _, issue := range report.Issues {
		found = found || strings.Contains(issue.Error(), "router.work:4:52: queue.dispatch đã bỏ")
	}
	if found {
		return
	}
	for _, issue := range report.Issues {
		t.Log(issue.Error())
	}
	t.Fatalf("check did not say queue.dispatch is gone, at its line (%d issues)", len(report.Issues))
}
