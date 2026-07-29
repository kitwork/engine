package work

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// End to end through a real tenant VM: COMMIT fires each configured request exactly once at the
// expression boundary, runs .then()/.catch(), and accepts modifiers on either side of the verb.
func TestTreeHttpThenCatch(t *testing.T) {
	savedLocal := AllowLocal
	AllowLocal = true // upstream is loopback; SSRF guard would otherwise block it
	defer func() { AllowLocal = savedLocal }()

	var committedHits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/fail"):
			w.WriteHeader(503)
		case strings.HasPrefix(r.URL.Path, "/lazy"):
			atomic.AddInt32(&committedHits, 1)
			w.WriteHeader(200)
		default:
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{"v":1}`))
		}
	}))
	defer srv.Close()

	tmp, err := os.MkdirTemp("", "kitwork-then-catch-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	dir := filepath.Join(tmp, "test", "localhost")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}

	u := srv.URL
	router := `import { router, http } from "kitwork";` + "\n" +
		`router.get((ctx) => {` + "\n" +
		`  let a = "none"; let b = "none"; let d = "none"; let e = "none";` + "\n" +
		// bare statement, handlers BEFORE config (dạng 2) + success
		`  http.get("` + u + `/ok").then(res => { a = "ok:" + res.status }).catch(x => { a = "err" }).retry(3);` + "\n" +
		// bare statement, config BEFORE handlers (dạng 1) + all-fail
		`  http.get("` + u + `/fail").retry(1).then(res => { b = "ok" }).catch(x => { b = "caught:" + x.status });` + "\n" +
		// Assigned to a var WITH a handler: commit once and run the continuation.
		`  const asg = http.get("` + u + `/ok").then(res => { d = "assigned:" + res.status });` + "\n" +
		// Arrow-body return WITH a handler: commit before crossing the return boundary.
		`  const f = () => http.get("` + u + `/ok").then(res => { e = "returned:" + res.status });` + "\n" +
		`  f();` + "\n" +
		// A configured request commits even when assigned and never observed.
		`  const lz = http.get("` + u + `/lazy");` + "\n" +
		`  const pre = http.retry(0).timeout(5000).get("` + u + `/pre");` + "\n" +
		`  const post = http.get("` + u + `/post").retry(0).timeout(5000);` + "\n" +
		`  return ctx.json({ a: a, b: b, d: d, e: e, pre: pre.status, post: post.status });` + "\n" +
		`});`
	if err := os.WriteFile(filepath.Join(dir, "router.kitwork.js"), []byte(router), 0644); err != nil {
		t.Fatal(err)
	}

	tenant := NewTenant(tmp, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "http://localhost/", nil)
	rec := httptest.NewRecorder()
	tenant.Serve(rec, req)
	body := rec.Body.String()

	if !strings.Contains(body, `"a":"ok:200"`) {
		t.Errorf("bare dạng 2 (handlers before config) success failed — body: %s", body)
	}
	if !strings.Contains(body, `"b":"caught:503"`) {
		t.Errorf("bare dạng 1 (config before handlers) failure path failed — body: %s", body)
	}
	if !strings.Contains(body, `"d":"assigned:200"`) {
		t.Errorf("SHARP EDGE 1 — assigned request .then() did not run: %s", body)
	}
	if !strings.Contains(body, `"e":"returned:200"`) {
		t.Errorf("SHARP EDGE 2 — returned (arrow-body) request .then() did not run: %s", body)
	}
	if !strings.Contains(body, `"pre":200`) || !strings.Contains(body, `"post":200`) {
		t.Errorf("modifiers before/after the verb are not equivalent: %s", body)
	}
	if n := atomic.LoadInt32(&committedHits); n != 1 {
		t.Errorf("assigned request committed %d times, want exactly 1", n)
	}
}
