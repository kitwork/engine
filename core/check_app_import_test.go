package core

import (
	"os"
	"path/filepath"
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
