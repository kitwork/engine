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
// expression boundary, and accepts modifiers on either side of the verb. Any answer is a response, a
// 503 included; a request that got no answer is a failure (14/10/2026), as fetch rejects in
// JavaScript, read with safe(() => …). .then() and .catch() are gone with it: calling one is the
// NOT_A_FUNCTION error of any missing method.
func TestTreeHttpCommitsOnceAndFailsWithoutAnAnswer(t *testing.T) {
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
	gone := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	goneURL := gone.URL
	gone.Close() // nothing answers there any more

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
		// a 503 is an answer: read as a response, retried as a transient one
		`  const failed = http.get("` + u + `/fail").retry(1);` + "\n" +
		// a configured request commits even when assigned and never observed
		`  const lz = http.get("` + u + `/lazy");` + "\n" +
		`  const pre = http.retry(0).timeout(5000).get("` + u + `/pre");` + "\n" +
		`  const post = http.get("` + u + `/post").retry(0).timeout(5000);` + "\n" +
		// no answer: a failure, caught where it is
		`  const down = safe(() => http.get("` + goneURL + `/x"));` + "\n" +
		`  const json = safe(() => http.get("` + goneURL + `/x").json());` + "\n" +
		// .then is no method of a request
		`  const then = safe(() => http.get("` + u + `/ok").then((res) => 1));` + "\n" +
		`  return ctx.json({ failed: failed.status, ok: failed.ok, pre: pre.status, post: post.status, ` +
		`down: down.error.code, json: json.error.code, downValue: down.value, then: then.error.message });` + "\n" +
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

	for _, want := range []string{
		`"failed":503`, `"ok":false`, `"pre":200`, `"post":200`,
		`"down":"NETWORK"`, `"json":"NETWORK"`, `"downValue":null`,
		`"then":"http.get(…).then không phải hàm (là null): không gọi được"`,
	} {
		if rec.Code != http.StatusOK || !strings.Contains(body, want) {
			t.Errorf("%d %s lacks %s", rec.Code, body, want)
		}
	}
	if n := atomic.LoadInt32(&committedHits); n != 1 {
		t.Errorf("assigned request committed %d times, want exactly 1", n)
	}
}
