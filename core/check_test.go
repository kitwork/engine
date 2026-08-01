package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckAggregatesSiteAndCronFailuresWithoutStartingRuntime(t *testing.T) {
	root := t.TempDir()
	valid := filepath.Join(root, "identity-a", "valid.example")
	brokenImport := filepath.Join(root, "identity-a", "import.example")
	brokenTemplate := filepath.Join(root, "identity-b", "template.example")
	cronDir := filepath.Join(root, "identity-a", "_cron")
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
	router := `import { router } from "kitwork";
router.get((ctx) => ctx.text("ok"));`
	write(filepath.Join(valid, "router.kitwork.js"), router)
	write(filepath.Join(valid, "index.kitwork.html"), `<html>{{ @page }}</html>`)
	write(filepath.Join(valid, "page.kitwork.html"), `<main>ok</main>`)
	write(
		filepath.Join(brokenImport, "router.kitwork.js"),
		`import { missing } from "../../_core/missing.kitwork.js";
import { router } from "kitwork";
router.get((ctx) => ctx.json(missing));`,
	)
	write(filepath.Join(brokenTemplate, "router.kitwork.js"), router)
	write(filepath.Join(brokenTemplate, "index.kitwork.html"), `<html>{{ @page }}</html>`)
	write(filepath.Join(brokenTemplate, "page.kitwork.html"), `{{ if }}`)
	write(
		filepath.Join(cronDir, "broken.kitwork.js"),
		`import { cron } from "kitwork"; cron.every("1m").handle(() => {`,
	)

	report := Check(root, 100_000)
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
	for _, want := range []string{"import.example", "template.example", "broken.kitwork.js"} {
		if !strings.Contains(joined, want) {
			t.Errorf("report does not identify %q:\n%s", want, joined)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "identity-a", ".data", "scheduler.db")); !os.IsNotExist(err) {
		t.Fatal("preflight started or persisted the cron scheduler")
	}
}
