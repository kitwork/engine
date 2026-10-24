package work

import (
	"sort"
	"strings"
	"testing"

	"github.com/kitwork/engine/app"
)

// The background files of an app read its tier of the environment (docs/KITWORK_ENVIRONMENT.md mục 1, 3):
// the app's .env, and the keys application.work shares with it through the app's tenant
// (SetSharedEnvironment). A file of .shared/queue/ or .schedule/, or their config.work, whose
// environment.require names a key the app has not is held back, as one that does not compile: not loaded,
// said at boot with the key and where it is required, never a value (22/10/2026; a route folder stops the
// boot instead). env, the old name, is refused in each of them as in a route, since they compile as files of
// the app (Program.App).
//
// CONTROL (23/10/2026): with Tenant.missingEnvironment finding nothing missing, mail.work loaded and no line
// named a key: "boot did not say \"[Queue] compile .shared/queue/mail.work: environment.require: thiếu
// MAIL_KEY …\"", the same for config.work and report.work; with the app's tenant not reading what the host
// shares (buildEnvironment), notify.work was held back too: "queue jobs = [], want [notify]"; with
// Program.App false for CompileModuleFileInApp and CompileConfigFileInApp, the three env files compiled.
func TestBackgroundFilesHoldTheirEnvironment(t *testing.T) {
	const header = "import * as queue from \"kitwork/queue\";\nimport { environment } from \"kitwork\";\n"
	const scheduleHeader = "import * as schedule from \"kitwork/schedule\";\nimport { environment } from \"kitwork\";\n"
	root := t.TempDir()
	writeQueueApp(t, root, map[string]string{
		".env":                      "HAVE=secret-xyz\n",
		".shared/queue/mail.work":   header + "const key = environment.require(\"MAIL_KEY\");\nexport const mail = queue.job((context) => key.length);\n",
		".shared/queue/notify.work": header + "export const notify = queue.job((context) => environment.require(\"SMTP_URL\").length + environment.require(\"HAVE\").length);\n",
		".shared/queue/config.work": "import { environment } from \"kitwork\";\nimport * as queue from \"kitwork/queue\";\nqueue.parallel(environment.require(\"QUEUE_PARALLEL\"));\n",
		".schedule/report.work":     scheduleHeader + "export const report = schedule.every(\"1h\").run((context) => environment.require(\"REPORT_TO\").length);\n",
		".schedule/heartbeat.work":  scheduleHeader + "export const heartbeat = schedule.every(\"1h\").run((context) => environment.require(\"HAVE\").length);\n",
		"localhost/router.work":     "import * as router from \"kitwork/router\";\nrouter.get((context) => context.text(\"ok\"));\n",
	})
	runtime := app.NewRuntime("acme")
	tenant := NewAppTenantWithRuntimeLayout(root, "acme", RootLayoutMultiTenant, runtime)
	tenant.SetSharedEnvironment(&SharedEnvironment{Apps: map[string]map[string]EnvironmentEntry{
		"acme": {"SMTP_URL": {Text: "smtp://host-only"}},
	}})
	output := captureOutput(t, func() {
		if err := tenant.Run(); err != nil {
			t.Fatal(err)
		}
	})
	t.Cleanup(func() { tenant.Close(); runtime.Close() })

	for _, line := range []string{
		"[Queue] compile .shared/queue/mail.work: environment.require: thiếu MAIL_KEY (mail.work:3:33);",
		"[Queue] compile .shared/queue/config.work: environment.require: thiếu QUEUE_PARALLEL (config.work:3:36);",
		"[Schedule] compile .schedule/report.work: environment.require: thiếu REPORT_TO (report.work:3:81);",
	} {
		if !strings.Contains(output, line) {
			t.Errorf("boot did not say %q:\n%s", line, output)
		}
	}
	if strings.Contains(output, "secret-xyz") || strings.Contains(output, "smtp://host-only") || strings.Contains(output, "compile .shared/queue/notify.work") || strings.Contains(output, "compile .schedule/heartbeat.work") {
		t.Errorf("the boot said a value, or a file whose keys are there:\n%s", output)
	}
	names := func(worker *queueRuntime) []string {
		worker.mu.Lock()
		defer worker.mu.Unlock()
		var loaded []string
		for _, handler := range worker.handlers {
			loaded = append(loaded, handler.Name)
		}
		sort.Strings(loaded)
		return loaded
	}
	if got := names(tenant.queueWorker()); strings.Join(got, ",") != "notify" {
		t.Errorf("queue jobs = %v, want [notify]", got)
	}
	if got := names(tenant.scheduleWorker()); strings.Join(got, ",") != "heartbeat" {
		t.Errorf("schedules = %v, want [heartbeat]", got)
	}
	queueWorker := tenant.queueWorker()
	queueWorker.mu.Lock()
	held := queueWorker.unloaded.Sources[".shared/queue/mail.work"]
	queueWorker.mu.Unlock()
	if !held {
		t.Errorf("mail.work is not among the files held back")
	}

	old := t.TempDir()
	writeQueueApp(t, old, map[string]string{
		".shared/queue/old.work":    "import * as queue from \"kitwork/queue\";\nimport { env } from \"kitwork\";\nexport const old = queue.job((context) => env.KEY);\n",
		".shared/queue/config.work": "import { env } from \"kitwork\";\nimport * as queue from \"kitwork/queue\";\nqueue.parallel(env.PARALLEL || 2);\n",
		".schedule/config.work":     "import { env } from \"kitwork\";\nimport * as schedule from \"kitwork/schedule\";\nschedule.keep(env.KEEP || \"7d\");\n",
		".schedule/heartbeat.work":  "import * as schedule from \"kitwork/schedule\";\nexport const heartbeat = schedule.every(\"1h\").run((context) => 1);\n",
		"localhost/router.work":     "import * as router from \"kitwork/router\";\nrouter.get((context) => context.text(\"ok\"));\n",
	})
	oldRuntime := app.NewRuntime("acme")
	oldTenant := NewAppTenantWithRuntimeLayout(old, "acme", RootLayoutMultiTenant, oldRuntime)
	output = captureOutput(t, func() {
		if err := oldTenant.Run(); err != nil {
			t.Fatal(err)
		}
	})
	t.Cleanup(func() { oldTenant.Close(); oldRuntime.Close() })
	const renamed = "`env` đã đổi tên: import { environment } from \"kitwork\""
	for _, line := range []string{
		"[Queue] compile .shared/queue/old.work: old.work:2:1: " + renamed,
		"[Queue] compile .shared/queue/config.work: config.work:1:1: " + renamed,
		"[Schedule] compile .schedule/config.work: config.work:1:1: " + renamed,
	} {
		if !strings.Contains(output, line) {
			t.Errorf("boot did not say %q:\n%s", line, output)
		}
	}
}
