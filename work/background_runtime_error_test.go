package work

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A _cron or _queue file whose top level fails at run time says so on the server's output, one line
// per file, as a file that does not compile already does: "[Cron] run broken.kitwork.js: …". The file
// registers nothing, as before; until this line existed it left the schedule or the queue without a
// word. A call of a value that is not a function (14/10/2026) and fail("boom"), the control, fail the
// same way; a file that runs is the second control and prints no such line.
func TestBackgroundFileThatFailsAtRunTimeSaysSo(t *testing.T) {
	tmp := t.TempDir()
	files := map[string]string{
		"localhost/router.kitwork.js": `import { router } from "kitwork"; router.get((ctx) => ctx.text("ok"));`,
		"_cron/broken.kitwork.js":     "import { cron } from \"kitwork\";\nconst o = { x: 3 };\no.x();\ncron.every(\"1h\").handle(() => 1);\n",
		"_cron/control.kitwork.js":    "import { cron } from \"kitwork\";\nfail(\"boom\");\ncron.every(\"1h\").handle(() => 1);\n",
		"_cron/good.kitwork.js":       "import { cron } from \"kitwork\";\ncron.every(\"1h\").handle(() => 1);\n",
		"_queue/qbroken.kitwork.js":   "import { queue } from \"kitwork\";\nconst o = { x: 3 };\no.x();\nqueue.handle((job) => 1);\n",
		"_queue/qcontrol.kitwork.js":  "import { queue } from \"kitwork\";\nfail(\"boom\");\nqueue.handle((job) => 1);\n",
		"_queue/qgood.kitwork.js":     "import { queue } from \"kitwork\";\nqueue.handle((job) => 1);\n",
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
		"[Cron] run control.kitwork.js: control.kitwork.js:2:1: boom",
		"[Queue] run qcontrol.kitwork.js: qcontrol.kitwork.js:2:1: boom",
		"[Cron] run broken.kitwork.js: broken.kitwork.js:3:1: o.x không phải hàm (là số): không gọi được",
		"[Queue] run qbroken.kitwork.js: qbroken.kitwork.js:3:1: o.x không phải hàm (là số): không gọi được",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("the server's output lacks %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, "run good.kitwork.js") || strings.Contains(output, "run qgood.kitwork.js") {
		t.Errorf("a file that runs printed a failure:\n%s", output)
	}

	scheduler := tenant.scheduler()
	scheduler.mu.Lock()
	var crons []string
	for _, job := range scheduler.jobs {
		crons = append(crons, job.Name)
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
		t.Fatalf("registered crons %v and queues %v, want [good] and [qgood]", crons, queues)
	}
}
