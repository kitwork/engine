package work

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A file of .schedule/ or a job file of .shared/queue/ whose top level fails at run time says so on the
// server's output, one line per file, as a file that does not compile already does: "[Schedule] run
// .schedule/broken.work: …". The file registers nothing, as before; until this line existed it left the
// schedule or the queue without a word. A call of a value that is not a function (14/10/2026) and
// fail("boom"), the control, fail the same way; a file that runs is the second control and prints no such
// line.
func TestBackgroundFileThatFailsAtRunTimeSaysSo(t *testing.T) {
	tmp := t.TempDir()
	files := map[string]string{
		"localhost/router.kitwork.js": `import { router } from "kitwork"; router.get((ctx) => ctx.text("ok"));`,
		".schedule/broken.work":       "import * as schedule from \"kitwork/schedule\";\nconst o = { x: 3 };\no.x();\nexport const broken = schedule.every(\"1h\").run((context) => 1);\n",
		".schedule/control.work":      "import * as schedule from \"kitwork/schedule\";\nfail(\"boom\");\nexport const control = schedule.every(\"1h\").run((context) => 1);\n",
		".schedule/good.work":         "import * as schedule from \"kitwork/schedule\";\nexport const good = schedule.every(\"1h\").run((context) => 1);\n",
		".shared/queue/qbroken.work":  "import * as queue from \"kitwork/queue\";\nconst o = { x: 3 };\no.x();\nexport const qbroken = queue.job((context) => 1);\n",
		".shared/queue/qcontrol.work": "import * as queue from \"kitwork/queue\";\nfail(\"boom\");\nexport const qcontrol = queue.job((context) => 1);\n",
		".shared/queue/qgood.work":    "import * as queue from \"kitwork/queue\";\nexport const qgood = queue.job((context) => 1);\n",
	}
	for path, content := range files {
		full := filepath.Join(tmp, "acme", filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stdout
	os.Stdout = writer
	var printed bytes.Buffer
	copied := make(chan struct{})
	go func() {
		io.Copy(&printed, reader)
		close(copied)
	}()
	tenant := NewAppTenant(tmp, "acme")
	runErr := tenant.Run()
	os.Stdout = saved
	writer.Close()
	<-copied
	if runErr != nil {
		t.Fatal(runErr)
	}
	defer tenant.Close()
	output := printed.String()

	for _, want := range []string{
		// a printed failure leads with its place (14/10/2026), the not-a-function one and fail's alike
		"[Schedule] run .schedule/control.work: control.work:2:1: boom",
		"[Queue] run .shared/queue/qcontrol.work: qcontrol.work:2:1: boom",
		"[Schedule] run .schedule/broken.work: broken.work:3:1: o.x không phải hàm (là số): không gọi được",
		"[Queue] run .shared/queue/qbroken.work: qbroken.work:3:1: o.x không phải hàm (là số): không gọi được",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("the server's output lacks %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, "run .schedule/good.work") || strings.Contains(output, "run .shared/queue/qgood.work") {
		t.Errorf("a file that runs printed a failure:\n%s", output)
	}

	scheduler := tenant.scheduleWorker()
	scheduler.mu.Lock()
	var crons []string
	for _, handler := range scheduler.handlers {
		crons = append(crons, handler.Name)
	}
	scheduler.mu.Unlock()
	worker := tenant.queueWorker()
	worker.mu.Lock()
	var queues []string
	for _, handler := range worker.handlers {
		queues = append(queues, handler.Name)
	}
	worker.mu.Unlock()
	if strings.Join(crons, ",") != "good" || strings.Join(queues, ",") != "qgood" {
		t.Fatalf("registered schedules %v and queues %v, want [good] and [qgood]", crons, queues)
	}
}
