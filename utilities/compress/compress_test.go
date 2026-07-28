package compress

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func serve(t *testing.T, acceptEncoding string, h http.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if acceptEncoding != "" {
		req.Header.Set("Accept-Encoding", acceptEncoding)
	}
	rec := httptest.NewRecorder()
	Middleware(h).ServeHTTP(rec, req)
	return rec
}

func html(size int) string {
	return "<!doctype html><html><body>" + strings.Repeat("<p>kitwork</p>", size) + "</body></html>"
}

// The reason this package exists: a real page measured 174 KB on the wire uncompressed.
func TestCompressesHTML(t *testing.T) {
	body := html(500)
	rec := serve(t, "gzip", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, body)
	})

	if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}
	if rec.Body.Len() >= len(body) {
		t.Fatalf("compressed body (%d) is not smaller than the original (%d)", rec.Body.Len(), len(body))
	}
	// It has to be READABLE, not merely smaller.
	zr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatalf("body is not valid gzip: %v", err)
	}
	got, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != body {
		t.Fatal("decompressed body does not match what the handler wrote")
	}
	// A shared cache must not hand this to a client that did not ask for gzip.
	if !strings.Contains(rec.Header().Get("Vary"), "Accept-Encoding") {
		t.Error("Vary: Accept-Encoding missing")
	}
	// Content-Length would describe the identity body and truncate the compressed one.
	if rec.Header().Get("Content-Length") != "" {
		t.Error("Content-Length must be dropped when the body is re-encoded")
	}
}

// SSE is a live connection. Compressing it holds events until the window fills, which looks exactly
// like the stream having died — the failure this package most needs to avoid.
func TestNeverCompressesEventStream(t *testing.T) {
	rec := serve(t, "gzip", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for i := 0; i < 200; i++ {
			_, _ = io.WriteString(w, "data: "+strings.Repeat("x", 40)+"\n\n")
		}
	})

	if rec.Header().Get("Content-Encoding") != "" {
		t.Fatal("an event stream was compressed")
	}
	if !strings.HasPrefix(rec.Body.String(), "data: ") {
		t.Fatalf("stream body was altered: %.40q", rec.Body.String())
	}
}

// A handler that flushes is streaming, whatever its content type says. Buffering it would stall
// whatever the flush was meant to deliver.
func TestFlushDisablesCompressionAndReachesTheClient(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()

	Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, "first")
		w.(http.Flusher).Flush()
		_, _ = io.WriteString(w, strings.Repeat("y", 4096))
	})).ServeHTTP(rec, req)

	if rec.Header().Get("Content-Encoding") != "" {
		t.Fatal("a flushing handler was compressed")
	}
	if !strings.HasPrefix(rec.Body.String(), "first") {
		t.Fatalf("flushed prefix did not survive: %.20q", rec.Body.String())
	}
	if !rec.Flushed {
		t.Error("Flush did not reach the underlying writer")
	}
}

// Already-compressed formats: gzip costs CPU and makes them very slightly bigger.
func TestSkipsAlreadyCompressedTypes(t *testing.T) {
	for _, ct := range []string{"image/png", "image/jpeg", "font/woff2", "video/mp4", "application/zip"} {
		rec := serve(t, "gzip", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", ct)
			_, _ = w.Write([]byte(strings.Repeat("\x00\xff", 4096)))
		})
		if rec.Header().Get("Content-Encoding") != "" {
			t.Errorf("%s was compressed", ct)
		}
	}
}

// SVG is markup wearing an image/ prefix, so the prefix cannot be the rule.
func TestCompressesSVG(t *testing.T) {
	rec := serve(t, "gzip", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/svg+xml")
		_, _ = io.WriteString(w, "<svg>"+strings.Repeat("<path d='M0 0'/>", 300)+"</svg>")
	})
	if rec.Header().Get("Content-Encoding") != "gzip" {
		t.Error("SVG should be compressed — it is markup, not a raster image")
	}
}

// Under a kilobyte the gzip framing costs more than it saves.
func TestSkipsTinyBodies(t *testing.T) {
	rec := serve(t, "gzip", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, "<p>ok</p>")
	})
	if rec.Header().Get("Content-Encoding") != "" {
		t.Fatal("a tiny body was compressed")
	}
	if rec.Body.String() != "<p>ok</p>" {
		t.Fatalf("small body was altered: %q", rec.Body.String())
	}
}

// CONTROL: a client that cannot decode gzip must receive the bytes unchanged. Without this the
// suite would pass on a middleware that compresses unconditionally.
func TestClientWithoutGzipGetsIdentity(t *testing.T) {
	body := html(500)
	rec := serve(t, "", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, body)
	})
	if rec.Header().Get("Content-Encoding") != "" {
		t.Fatal("compressed for a client that never asked")
	}
	if rec.Body.String() != body {
		t.Fatal("identity body was altered")
	}
}

// "gzip" must not be matched inside another token, and q-values must parse.
func TestAcceptEncodingParsing(t *testing.T) {
	cases := map[string]bool{
		"gzip":                     true,
		"gzip;q=1.0, deflate":      true,
		" br , gzip ":              true,
		"deflate, br":              false,
		"":                         false,
		"x-gzip-not-really":        false,
		"identity;q=1, gzip;q=0.8": true,
	}
	for header, want := range cases {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		if header != "" {
			req.Header.Set("Accept-Encoding", header)
		}
		if got := acceptsGzip(req); got != want {
			t.Errorf("Accept-Encoding %q → %v, want %v", header, got, want)
		}
	}
}

// A status the handler set must survive the deferred header write.
func TestPreservesStatusCode(t *testing.T) {
	rec := serve(t, "gzip", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, html(300))
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if rec.Header().Get("Content-Encoding") != "gzip" {
		t.Error("a 404 page is still worth compressing")
	}
}

// A handler that writes nothing must still complete its response rather than hang or lose status.
func TestEmptyResponse(t *testing.T) {
	rec := serve(t, "gzip", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if rec.Header().Get("Content-Encoding") != "" {
		t.Error("nothing to compress")
	}
}

func TestConcurrentUseSharesPoolSafely(t *testing.T) {
	done := make(chan struct{})
	for i := 0; i < 32; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			rec := serve(t, "gzip", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/html")
				_, _ = io.WriteString(w, html(200))
			})
			if rec.Header().Get("Content-Encoding") != "gzip" {
				t.Error("expected compression")
			}
		}()
	}
	for i := 0; i < 32; i++ {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("timeout")
		}
	}
}
