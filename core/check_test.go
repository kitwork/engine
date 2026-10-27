package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kitwork/engine/work"
)

func TestCheckAggregatesSiteAndScheduleFailuresWithoutStartingRuntime(t *testing.T) {
	root := t.TempDir()
	valid := filepath.Join(root, "identity-a", "valid.example")
	brokenImport := filepath.Join(root, "identity-a", "import.example")
	brokenTemplate := filepath.Join(root, "identity-b", "template.example")
	cronDir := filepath.Join(root, "identity-a", ".schedule")
	for _, dir := range []string{valid, brokenImport, brokenTemplate, cronDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	write := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	router := `import * as router from "kitwork/router";
router.get((context) => context.text("ok"));`
	write(filepath.Join(valid, work.UIRouterFileName), router)
	write(
		filepath.Join(brokenImport, work.UIRouterFileName),
		`import * as router from "kitwork/router";
import { missing } from "../.shared/missing.work";
router.get((context) => context.json(missing));`,
	)
	// a template page left in a site: a boot error since the template pages went (27/10/2026)
	write(filepath.Join(brokenTemplate, work.UIRouterFileName), router)
	write(filepath.Join(brokenTemplate, "page.kitwork.html"), `<main>ok</main>`)
	write(
		filepath.Join(cronDir, "broken.work"),
		`import * as schedule from "kitwork/schedule"; export const broken = schedule.every("1m").run((context) => {`,
	)

	report := Check(root, 100_000)
	if report.Root != root || report.Layout != "auto" {
		t.Fatalf("check source = %q (%s), want %q (auto)", report.Root, report.Layout, root)
	}
	if report.Sites != 3 || report.Valid != 1 {
		t.Fatalf("report = %+v, want 3 sites and 1 valid", report)
	}
	if report.Programs != 2 || report.Compatible != 2 {
		t.Fatalf(
			"compatibility = %d/%d, want 2/2",
			report.Compatible,
			report.Programs,
		)
	}
	if len(report.Issues) != 3 {
		for _, issue := range report.Issues {
			t.Log(issue.Error())
		}
		t.Fatalf("issues = %d, want 3", len(report.Issues))
	}

	joined := ""
	for _, issue := range report.Issues {
		joined += issue.Error() + "\n"
	}
	for _, want := range []string{"import.example", "template.example", "page.kitwork.html: trang template (.kitwork.html) không còn được đọc", "broken.work", "schedule compile"} {
		if !strings.Contains(joined, want) {
			t.Errorf("report does not identify %q:\n%s", want, joined)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "identity-a", ".database")); !os.IsNotExist(err) {
		t.Fatal("preflight started the app's schedules or stored their runs")
	}
}

func TestCheckWithLayoutUsesMultiDomainDiscoveryContract(t *testing.T) {
	root := t.TempDir()
	site := filepath.Join(root, "one.example")
	private := filepath.Join(root, "_private")
	for _, directory := range []string{site, private} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	valid := `import * as router from "kitwork/router";
router.get((context) => context.text("ok"));`
	if err := os.WriteFile(filepath.Join(site, work.UIRouterFileName), []byte(valid), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(private, work.UIRouterFileName),
		[]byte(`const broken = ;`),
		0o644,
	); err != nil {
		t.Fatal(err)
	}

	report := CheckWithLayout(root, 100_000, work.RootLayoutMultiDomain)
	if report.Root != root || report.Layout != "multi-domain" {
		t.Fatalf("check root metadata = %q (%s), want %q (multi-domain)", report.Root, report.Layout, root)
	}
	if !report.OK() {
		for _, issue := range report.Issues {
			t.Log(issue.Error())
		}
		t.Fatalf("check issues = %d", len(report.Issues))
	}
	if report.Apps != 1 || report.Sites != 1 || report.Valid != 1 {
		t.Fatalf("check report = %+v, want one app and one valid site", report)
	}
	if report.Programs != 1 || report.Compatible != 1 {
		t.Fatalf(
			"compatibility = %d/%d, want 1/1",
			report.Compatible,
			report.Programs,
		)
	}
}
