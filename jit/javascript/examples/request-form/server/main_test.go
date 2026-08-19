package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestProfileAPISuccess(t *testing.T) {
	handler := newTestDemoHandler(t)
	request := newProfileRequest(t, context.Background(), "/api/profile", `{
		"name": "Ada Lovelace",
		"email": "ada@example.test"
	}`)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	result := response.Result()
	defer result.Body.Close()
	if result.StatusCode != http.StatusOK {
		t.Fatalf("POST /api/profile status = %d, want %d; body=%s",
			result.StatusCode, http.StatusOK, response.Body.String())
	}
	if got := result.Header.Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Fatalf("POST /api/profile Content-Type = %q", got)
	}
	if got := result.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("POST /api/profile Cache-Control = %q, want no-store", got)
	}

	var payload struct {
		Saved   bool `json:"saved"`
		Profile struct {
			Name  string `json:"name"`
			Email string `json:"email"`
		} `json:"profile"`
	}
	decoder := json.NewDecoder(result.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		t.Fatalf("decode successful profile response: %v", err)
	}
	if payload.Saved != true || payload.Profile.Name != "Ada Lovelace" || payload.Profile.Email != "ada@example.test" {
		t.Fatalf("successful profile response = %#v", payload)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		t.Fatalf("successful profile response has trailing JSON: %v", err)
	}
}

func TestDemoHandlerServesRequestFormPage(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "request-form")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatalf("create request-form fixture directory: %v", err)
	}
	const page = "<!doctype html><title>Request form fixture</title>"
	if err := os.WriteFile(filepath.Join(directory, "index.html"), []byte(page), 0o600); err != nil {
		t.Fatalf("write request-form fixture: %v", err)
	}
	handler, err := newDemoHandler(root)
	if err != nil {
		t.Fatalf("new demo handler: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	result, err := server.Client().Get(server.URL + "/request-form/index.html")
	if err != nil {
		t.Fatalf("GET request-form page: %v", err)
	}
	defer result.Body.Close()
	if result.StatusCode != http.StatusOK {
		t.Fatalf("GET request-form page status = %d, want %d", result.StatusCode, http.StatusOK)
	}
	body, err := io.ReadAll(result.Body)
	if err != nil {
		t.Fatalf("read request-form page: %v", err)
	}
	if string(body) != page {
		t.Fatalf("GET request-form page body = %q, want %q", body, page)
	}
}

func TestDemoHandlerServesCheckedRequestFormGraph(t *testing.T) {
	handler, err := newDemoHandler(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("new demo handler with checked examples root: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	result, err := server.Client().Get(server.URL + "/request-form/index.html")
	if err != nil {
		t.Fatalf("GET checked request-form page: %v", err)
	}
	page, err := io.ReadAll(result.Body)
	result.Body.Close()
	if err != nil {
		t.Fatalf("read checked request-form page: %v", err)
	}
	if result.StatusCode != http.StatusOK {
		t.Fatalf("GET checked request-form page status = %d, want %d", result.StatusCode, http.StatusOK)
	}

	match := regexp.MustCompile(`<script[^>]+src="(\./hydrate\.kit\.[^"]+\.js)"`).FindSubmatch(page)
	if len(match) != 2 {
		t.Fatalf("checked request-form page does not reference one sealed Hydrate artifact")
	}
	artifactURL := server.URL + "/request-form/" + strings.TrimPrefix(string(match[1]), "./")
	artifactResult, err := server.Client().Get(artifactURL)
	if err != nil {
		t.Fatalf("GET checked request-form artifact: %v", err)
	}
	defer artifactResult.Body.Close()
	if artifactResult.StatusCode != http.StatusOK {
		t.Fatalf("GET checked request-form artifact status = %d, want %d", artifactResult.StatusCode, http.StatusOK)
	}
	artifact, err := io.ReadAll(artifactResult.Body)
	if err != nil {
		t.Fatalf("read checked request-form artifact: %v", err)
	}
	if len(artifact) < 1024 {
		t.Fatalf("checked request-form artifact size = %d, want a non-trivial sealed graph", len(artifact))
	}
}

func TestProfileAPIFailsClosed(t *testing.T) {
	handler := newTestDemoHandler(t)
	tests := []struct {
		name        string
		method      string
		body        string
		contentType string
		csrf        string
		wantStatus  int
		wantAllow   string
	}{
		{
			name:        "method",
			method:      http.MethodGet,
			body:        `{}`,
			contentType: "application/json",
			csrf:        demoCSRFToken,
			wantStatus:  http.StatusMethodNotAllowed,
			wantAllow:   http.MethodPost,
		},
		{
			name:        "csrf",
			method:      http.MethodPost,
			body:        `{}`,
			contentType: "application/json",
			wantStatus:  http.StatusForbidden,
		},
		{
			name:        "content type",
			method:      http.MethodPost,
			body:        `{}`,
			contentType: "text/plain",
			csrf:        demoCSRFToken,
			wantStatus:  http.StatusUnsupportedMediaType,
		},
		{
			name:        "malformed JSON",
			method:      http.MethodPost,
			body:        `{"name":`,
			contentType: "application/json",
			csrf:        demoCSRFToken,
			wantStatus:  http.StatusBadRequest,
		},
		{
			name:        "null JSON",
			method:      http.MethodPost,
			body:        `null`,
			contentType: "application/json",
			csrf:        demoCSRFToken,
			wantStatus:  http.StatusBadRequest,
		},
		{
			name:        "unknown JSON field",
			method:      http.MethodPost,
			body:        `{"name":"Ada","email":"ada@example.test","admin":true}`,
			contentType: "application/json",
			csrf:        demoCSRFToken,
			wantStatus:  http.StatusBadRequest,
		},
		{
			name:        "oversized body",
			method:      http.MethodPost,
			body:        `{"name":"` + strings.Repeat("a", 70<<10) + `","email":"ada@example.test"}`,
			contentType: "application/json",
			csrf:        demoCSRFToken,
			wantStatus:  http.StatusRequestEntityTooLarge,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, "/api/profile", strings.NewReader(test.body))
			request.Header.Set("Content-Type", test.contentType)
			if test.csrf != "" {
				request.Header.Set("X-CSRF-Token", test.csrf)
			}
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, test.wantStatus, response.Body.String())
			}
			if got := response.Header().Get("Cache-Control"); got != "no-store" {
				t.Fatalf("Cache-Control = %q, want no-store", got)
			}
			if got := response.Header().Get("Allow"); got != test.wantAllow {
				t.Fatalf("Allow = %q, want %q", got, test.wantAllow)
			}
		})
	}
}

func TestSlowProfileRequestHonorsCancellation(t *testing.T) {
	handler := newTestDemoHandler(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request := newProfileRequest(t, ctx, "/api/profile?demo=slow", `{
		"name": "Ada Lovelace",
		"email": "ada@example.test"
	}`)
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(response, request)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(300 * time.Millisecond):
		t.Fatal("cancelled demo=slow request did not return promptly")
	}
}

func newTestDemoHandler(t *testing.T) http.Handler {
	t.Helper()
	handler, err := newDemoHandler(t.TempDir())
	if err != nil {
		t.Fatalf("new demo handler: %v", err)
	}
	return handler
}

func newProfileRequest(t *testing.T, ctx context.Context, target, body string) *http.Request {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body)).WithContext(ctx)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-CSRF-Token", demoCSRFToken)
	return request
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("unexpected second JSON value")
	}
	return err
}
