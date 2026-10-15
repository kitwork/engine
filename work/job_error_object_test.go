package work

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/kitwork/engine/value"
)

// jobErrorReceiver is a server a job's .error() handler posts its err to; it keeps the first body.
func jobErrorReceiver(t *testing.T) (url string, received func() string) {
	t.Helper()
	var mu sync.Mutex
	var first string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		if first == "" {
			first = string(body)
		}
		mu.Unlock()
		w.WriteHeader(200)
	}))
	t.Cleanup(server.Close)
	return server.URL, func() string {
		mu.Lock()
		defer mu.Unlock()
		return first
	}
}

// postErr is the .error() handler of a job: it posts what it received, its keys included.
func postErr(url string) string {
	return `.error((job, err) => { http.post("` + url + `/err", { err: err, keys: Object.keys(err) }); })`
}

// checkJobErr reads what the handler posted: { message, code, at }, the shape a router's .error()
// handler and safe give, the message alone (no place first, no stack after), at the place of the fail.
func checkJobErr(t *testing.T, posted, file string) {
	t.Helper()
	var got struct {
		Err  map[string]any `json:"err"`
		Keys []string       `json:"keys"`
	}
	if err := json.Unmarshal([]byte(posted), &got); err != nil {
		t.Fatalf("the .error() handler posted %q (%v)", posted, err)
	}
	if len(got.Keys) != 3 || got.Err["message"] != "hỏng" || got.Err["code"] != value.FailCode ||
		got.Err["at"] != file+":3:19" {
		t.Fatalf("the .error() handler got %s, want { message: \"hỏng\", code: FAIL, at: %s:3:19 }", posted, file)
	}
}

// A cron's and a queue's .error((run, err) => …) handler receives the error object a router's .error()
// handler receives (14/10/2026): { message, code, at }. It was { message } alone, the message led by
// the place and followed by the stack, with no code and no at.
func TestCronErrorHandlerReceivesTheErrorObject(t *testing.T) {
	savedLocal := AllowLocal
	AllowLocal = true
	defer func() { AllowLocal = savedLocal }()
	url, received := jobErrorReceiver(t)

	tmp := t.TempDir()
	site := filepath.Join(tmp, "acme", "localhost")
	cronDir := filepath.Join(tmp, "acme", "_cron")
	for _, dir := range []string{site, cronDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(site, "router.kitwork.js"), []byte(`import { router } from "kitwork"; router.get((ctx) => ctx.text("ok"));`), 0o644); err != nil {
		t.Fatal(err)
	}
	job := "import { cron, http } from \"kitwork\";\ncron.every(\"1s\")\n  .handle(() => { fail(\"hỏng\"); })\n  " + postErr(url) + ";\n"
	if err := os.WriteFile(filepath.Join(cronDir, "boom.kitwork.js"), []byte(job), 0o644); err != nil {
		t.Fatal(err)
	}
	tenant := NewAppTenant(tmp, "acme")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	defer tenant.Close()
	posted := ""
	waitFor(10*time.Second, func() bool { posted = received(); return posted != "" })
	tenant.StopCronJobs()
	time.Sleep(200 * time.Millisecond)
	checkJobErr(t, posted, "boom.kitwork.js")
}

func TestQueueErrorHandlerReceivesTheErrorObject(t *testing.T) {
	savedLocal := AllowLocal
	AllowLocal = true
	defer func() { AllowLocal = savedLocal }()
	url, received := jobErrorReceiver(t)

	tenant := newQueueTenant(t, "boom", "import { queue, http } from \"kitwork\";\nqueue\n  .handle(() => { fail(\"hỏng\"); })\n  "+postErr(url)+";\n")
	defer tenant.Close()
	if result := dispatch(t, tenant, value.New("boom"), value.New(map[string]any{"n": 1})); !result.Get("queued").IsTrue() {
		t.Fatalf("dispatch refused: %v", result.Get("error").Text())
	}
	posted := ""
	waitFor(10*time.Second, func() bool { posted = received(); return posted != "" })
	tenant.StopQueueWorker()
	time.Sleep(200 * time.Millisecond)
	checkJobErr(t, posted, "boom.kitwork.js")
}
