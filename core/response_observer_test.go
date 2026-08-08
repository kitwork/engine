package core

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestObservedResponseWriterTracksStatusAndPreservesStreaming(t *testing.T) {
	recorder := httptest.NewRecorder()
	observed := &observedResponseWriter{ResponseWriter: recorder}

	if _, err := observed.ReadFrom(strings.NewReader("streamed")); err != nil {
		t.Fatal(err)
	}
	observed.Flush()

	if observed.Status() != http.StatusOK {
		t.Fatalf("status = %d", observed.Status())
	}
	if recorder.Body.String() != "streamed" {
		t.Fatalf("body = %q", recorder.Body.String())
	}
	if !recorder.Flushed {
		t.Fatal("Flush was not forwarded")
	}
}

func TestObservedResponseWriterKeepsFirstFinalStatus(t *testing.T) {
	recorder := httptest.NewRecorder()
	observed := &observedResponseWriter{ResponseWriter: recorder}
	observed.WriteHeader(http.StatusCreated)
	observed.WriteHeader(http.StatusInternalServerError)

	if observed.Status() != http.StatusCreated {
		t.Fatalf("status = %d, want 201", observed.Status())
	}
}
