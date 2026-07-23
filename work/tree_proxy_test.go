package work

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

	// The upstream is loopback; the SSRF transport blocks private space unless local is allowed.
	AllowLocal = true
	defer func() { AllowLocal = false }()

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
