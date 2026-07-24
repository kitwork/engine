package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWebContext(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://localhost/api?foo=bar", nil)
	rec := httptest.NewRecorder()

	ctx := NewContext(rec, req)
	if ctx.Query("foo") != "bar" {
		t.Fatalf("expected query foo=bar, got %q", ctx.Query("foo"))
	}

	ctx.Params["id"] = "123"
	if ctx.Param("id") != "123" {
		t.Fatalf("expected param id=123, got %q", ctx.Param("id"))
	}

	v := ctx.JSON(http.StatusOK, map[string]string{"status": "ok"})
	if !v.Truthy() {
		t.Fatal("expected non-nil json value")
	}
}
