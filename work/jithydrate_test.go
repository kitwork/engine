package work

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	hydrate "github.com/kitwork/engine/jit/hydrate"
)

func TestServeHydrateIf(t *testing.T) {
	// A non-matching path is a no-op: returns false, writes nothing.
	r := httptest.NewRequest("GET", "/something-else", nil)
	w := httptest.NewRecorder()
	if serveHydrateIf(w, r) {
		t.Fatal("serveHydrateIf should not handle a non-runtime path")
	}

	// The runtime path serves the interpreter as JavaScript, with an ETag.
	r = httptest.NewRequest("GET", hydrate.RuntimePath, nil)
	w = httptest.NewRecorder()
	if !serveHydrateIf(w, r) {
		t.Fatal("serveHydrateIf should handle the runtime path")
	}
	res := w.Result()
	if ct := res.Header.Get("Content-Type"); !strings.Contains(ct, "javascript") {
		t.Errorf("want a javascript content-type, got %q", ct)
	}
	etag := res.Header.Get("ETag")
	if etag == "" {
		t.Error("want an ETag header")
	}
	if !strings.Contains(w.Body.String(), "window.hydrate") {
		t.Error("body should be the embedded interpreter")
	}

	// A conditional request with the current ETag revalidates to 304 with no body.
	r = httptest.NewRequest("GET", hydrate.RuntimePath, nil)
	r.Header.Set("If-None-Match", etag)
	w = httptest.NewRecorder()
	serveHydrateIf(w, r)
	if w.Result().StatusCode != http.StatusNotModified {
		t.Errorf("want 304 Not Modified, got %d", w.Result().StatusCode)
	}
	if w.Body.Len() != 0 {
		t.Error("304 response should have an empty body")
	}
}
