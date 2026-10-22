package work

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// router.proxy(target).persist(): a folder answered from an UPSTREAM.
//   - the upstream's bytes AND Content-Type survive (an image must not come back as octet-stream),
//   - binary is byte-identical through the value/response/cache path,
//   - a .persist() hit replays from disk with NO refetch (the "cached mount"),
//   - a handler target is resolved per request.
func TestTreeProxyPersist(t *testing.T) {
	// PNG magic + a NUL and a high byte: proves the path is binary-safe, not UTF-8 mangled.
	png := []byte("\x89PNG\r\n\x1a\n\x00\x01\xff\xfeBINARY")

	var hits int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if strings.HasSuffix(r.URL.Path, "/dynamic") {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = w.Write([]byte("DYNAMIC-OK"))
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(png)
	}))
	defer upstream.Close()

	// The upstream is loopback; the SSRF transport blocks private space unless a test opens loopback.
	reachLoopback(t)

	tmp := t.TempDir()
	dir := filepath.Join(tmp, "test", "localhost") // <root>/<identity>/<domain>
	write := func(rel, content string) {
		t.Helper()
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("router.kitwork.js", `import { router } from "kitwork";`)
	write("index.kitwork.html", `<html><body>{{ @page }}</body></html>`)
	write("page.kitwork.html", `<main>home</main>`)
	write("notfound.kitwork.html", `<main>nothing here</main>`)
	// Fixed upstream + disk cache.
	write("logo/router.kitwork.js", `import { router } from "kitwork";`+"\n"+
		`router.proxy("`+upstream.URL+`/logo.png").persist("1h");`)
	// Handler-computed upstream (no cache, so every hit re-resolves).
	write("shot/router.kitwork.js", `import { router } from "kitwork";`+"\n"+
		`router.proxy(() => "`+upstream.URL+`/dynamic");`)

	tenant := NewTenant(tmp, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatalf("tenant failed to run: %v", err)
	}
	get := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "http://localhost"+path, nil)
		req.RemoteAddr = "8.8.4.4:1234"
		rec := httptest.NewRecorder()
		tenant.Serve(rec, req)
		return rec
	}

	// 1. MISS: fetched from the upstream, replayed with the upstream's type + exact bytes.
	rec := get("/logo")
	if rec.Code != 200 {
		t.Fatalf("proxy: code=%d body=%q", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("content-type = %q, want image/png", ct)
	}
	if !bytes.Equal(rec.Body.Bytes(), png) {
		t.Errorf("body not byte-identical: got %q want %q", rec.Body.Bytes(), png)
	}
	if hits != 1 {
		t.Fatalf("upstream hits = %d, want 1", hits)
	}

	// 2. HIT: .persist() replays from disk — same bytes/type, and the upstream is NOT touched again.
	rec = get("/logo")
	if rec.Code != 200 || !bytes.Equal(rec.Body.Bytes(), png) {
		t.Errorf("cached proxy: code=%d bytes-equal=%v", rec.Code, bytes.Equal(rec.Body.Bytes(), png))
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("cached content-type = %q, want image/png", ct)
	}
	if hits != 1 {
		t.Errorf("upstream hits = %d after a cached hit, want still 1 (no refetch)", hits)
	}

	// 3. Handler target: resolved per request.
	rec = get("/shot")
	if rec.Code != 200 || rec.Body.String() != "DYNAMIC-OK" {
		t.Errorf("handler-target proxy: code=%d body=%q", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("handler-target content-type = %q, want text/plain*", ct)
	}
}

// router.proxy reads the upstream through kitwork/http's Go request (22/10/2026): an upstream that sends
// no Content-Type is sniffed (PNG bytes come back as image/png, not octet-stream), and a body past the
// proxy's cap (the host's 64 MiB, lowered here) is TOO_LARGE, which .error() reads, and nothing of it
// reaches the visitor.
func TestProxySniffsTheTypeAndCapsTheBody(t *testing.T) {
	reachLoopback(t)
	png := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/large" {
			_, _ = w.Write(bytes.Repeat([]byte("x"), 2048))
			return
		}
		w.Header()["Content-Type"] = nil // no Content-Type at all: Go's server would sniff one otherwise
		_, _ = w.Write(png)
	}))
	defer upstream.Close()
	saved := proxyMaxBytes
	proxyMaxBytes = 1024
	defer func() { proxyMaxBytes = saved }()

	tn := NewTenant(writeSite(t, map[string]string{
		"router.work": `import * as router from "kitwork/router";`,
		"logo/router.work": `import * as router from "kitwork/router";
router.proxy("` + upstream.URL + `/logo");
`,
		"large/router.work": `import * as router from "kitwork/router";
router.proxy("` + upstream.URL + `/large")
  .error((context, error) => context.status(502).json({ code: error.code }));
`,
	}), "localhost")
	if err := tn.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	rec := sendRequest(tn, http.MethodGet, "/logo", "", "", nil)
	if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), png) {
		t.Fatalf("GET /logo = %d %q", rec.Code, rec.Body.Bytes())
	}
	if got := rec.Header().Get("Content-Type"); got != "image/png" {
		t.Errorf("an upstream with no Content-Type answered as %q, want the sniffed image/png", got)
	}

	rec = sendRequest(tn, http.MethodGet, "/large", "", "", nil)
	var failed struct{ Code string }
	if err := json.Unmarshal(rec.Body.Bytes(), &failed); rec.Code != http.StatusBadGateway || err != nil || failed.Code != "TOO_LARGE" {
		t.Errorf("GET /large (2048 bytes, a cap of 1024) = %d %s, want 502 with code TOO_LARGE", rec.Code, rec.Body.String())
	}
}

// router.proxy's request ends with the visitor's (22/10/2026): when the visitor's request is cancelled
// (the connection closed), the upstream sees its own request cancelled rather than the proxy holding it
// until the upstream answers.
func TestProxyEndsWithTheVisitor(t *testing.T) {
	reachLoopback(t)
	arrived := make(chan struct{})
	var once sync.Once
	saw := make(chan bool, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { close(arrived) })
		select {
		case <-r.Context().Done():
			saw <- true
		case <-time.After(3 * time.Second):
			saw <- false
		}
	}))
	defer upstream.Close()
	tn := NewTenant(writeSite(t, map[string]string{
		"router.work": `import * as router from "kitwork/router";`,
		"mirror/router.work": `import * as router from "kitwork/router";
router.proxy("` + upstream.URL + `/slow");
`,
	}), "localhost")
	if err := tn.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}

	visitor, leave := context.WithCancel(context.Background())
	defer leave()
	served := make(chan int, 1)
	go func() {
		rec := httptest.NewRecorder()
		tn.Serve(rec, httptest.NewRequest(http.MethodGet, "http://localhost/mirror", nil).WithContext(visitor))
		served <- rec.Code
	}()
	select {
	case <-arrived:
	case <-time.After(5 * time.Second):
		t.Fatal("the proxy never reached its upstream")
	}
	leave()
	if !<-saw {
		t.Fatal("the upstream never saw its request cancelled: the proxy kept it after the visitor's request ended")
	}
	select {
	case code := <-served:
		if code == http.StatusOK {
			t.Errorf("a proxy whose visitor left answered 200, want the failure of a cancelled request")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the proxy did not finish after its visitor left")
	}
}
