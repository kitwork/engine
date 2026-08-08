package core

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

type engineDiscardWriter struct {
	header http.Header
	status int
}

func newEngineDiscardWriter() *engineDiscardWriter {
	return &engineDiscardWriter{header: make(http.Header)}
}

func (w *engineDiscardWriter) Header() http.Header {
	return w.header
}

func (w *engineDiscardWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}

func (w *engineDiscardWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return len(body), nil
}

func (w *engineDiscardWriter) Reset() {
	clear(w.header)
	w.status = 0
}

func BenchmarkEngineObservedRequest(b *testing.B) {
	root := b.TempDir()
	writeTreeTenant(b, root, "ok")
	engine := New(root, 0, false, "")
	defer engine.Close()

	request := httptest.NewRequest(http.MethodGet, "http://localhost/", nil)
	response := newEngineDiscardWriter()
	engine.ServeHTTP(response, request)
	if response.status != http.StatusOK {
		b.Fatalf("warm response = %d", response.status)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		response.Reset()
		engine.ServeHTTP(response, request)
		if response.status != http.StatusOK {
			b.Fatalf("response = %d", response.status)
		}
	}
}
