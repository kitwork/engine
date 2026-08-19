// Command server runs the request-form example against a real, local HTTP API.
// It is intentionally separate from the KitJS runtime and uses only the Go
// standard library.
package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"net/mail"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

const (
	defaultAddress = "127.0.0.1:4174"
	defaultRoot    = "jit/javascript/examples"
	demoCSRFToken  = "request-form-demo-token"
	maxBodyBytes   = 64 << 10
	slowDemoDelay  = 2 * time.Second
)

type profile struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

type profileResponse struct {
	Saved   bool    `json:"saved"`
	Profile profile `json:"profile"`
}

type errorResponse struct {
	Saved bool        `json:"saved"`
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func main() {
	address := flag.String("addr", defaultAddress, "HTTP listen address")
	root := flag.String("root", defaultRoot, "directory containing the KitJS examples")
	flag.Parse()

	handler, err := newDemoHandler(*root)
	if err != nil {
		log.Fatal(err)
	}

	server := &http.Server{
		Addr:              *address,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	serverErrors := make(chan error, 1)
	go func() {
		serverErrors <- server.ListenAndServe()
	}()

	log.Printf("request-form demo: http://%s/request-form/index.html", *address)
	select {
	case err := <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
		return
	case <-ctx.Done():
	}

	shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownContext); err != nil {
		log.Printf("shutdown: %v", err)
		_ = server.Close()
	}
	if err := <-serverErrors; err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Printf("server: %v", err)
	}
}

// newDemoHandler serves all KitJS examples from root and mounts the profile
// API at /api/profile. Keeping the handler constructor independent from the
// listener makes the complete demo contract testable with httptest.
func newDemoHandler(root string) (http.Handler, error) {
	absoluteRoot, err := resolveExamplesRoot(root)
	if err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/profile", handleProfile)
	mux.Handle("/", http.FileServer(http.Dir(absoluteRoot)))
	return mux, nil
}

func resolveExamplesRoot(root string) (string, error) {
	abs, err := filepath.Abs(root)
	if err == nil {
		if info, statErr := os.Stat(abs); statErr == nil && info.IsDir() {
			return abs, nil
		}
	}

	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolve examples root %q: %w", root, err)
	}

	dir := wd
	for {
		candidate := filepath.Join(dir, root)
		if info, statErr := os.Stat(candidate); statErr == nil && info.IsDir() {
			return candidate, nil
		}

		standard := filepath.Join(dir, "engine", "jit", "javascript", "examples")
		if info, statErr := os.Stat(standard); statErr == nil && info.IsDir() {
			return standard, nil
		}

		if filepath.Base(dir) == "examples" {
			if info, statErr := os.Stat(filepath.Join(dir, "request-form")); statErr == nil && info.IsDir() {
				return dir, nil
			}
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	return "", fmt.Errorf("open examples root %q: directory not found from %q", root, wd)
}

func handleProfile(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", http.MethodPost)
		writeError(response, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "use POST for this endpoint")
		return
	}
	if !validCSRFToken(request.Header.Get("X-CSRF-Token")) {
		writeError(response, http.StatusForbidden, "CSRF", "the CSRF token is missing or invalid")
		return
	}

	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || !strings.EqualFold(mediaType, "application/json") {
		writeError(response, http.StatusUnsupportedMediaType, "CONTENT_TYPE", "Content-Type must be application/json")
		return
	}
	if request.ContentLength > maxBodyBytes {
		writeError(response, http.StatusRequestEntityTooLarge, "BODY_TOO_LARGE", "request body exceeds 64 KiB")
		return
	}

	payload, status, code, message := decodeProfile(response, request)
	if status != 0 {
		writeError(response, status, code, message)
		return
	}

	switch request.URL.Query().Get("demo") {
	case "":
	case "fast":
	case "error":
		writeError(response, http.StatusUnprocessableEntity, "PROFILE_REJECTED", "the demo server rejected this profile")
		return
	case "slow":
		timer := time.NewTimer(slowDemoDelay)
		defer timer.Stop()
		select {
		case <-request.Context().Done():
			return
		case <-timer.C:
		}
	default:
		writeError(response, http.StatusBadRequest, "INVALID_DEMO", "demo must be fast, slow, or error")
		return
	}

	writeJSON(response, http.StatusOK, profileResponse{Saved: true, Profile: payload})
}

func decodeProfile(response http.ResponseWriter, request *http.Request) (profile, int, string, string) {
	request.Body = http.MaxBytesReader(response, request.Body, maxBodyBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()

	var decoded *profile
	if err := decoder.Decode(&decoded); err != nil {
		if isBodyTooLarge(err) {
			return profile{}, http.StatusRequestEntityTooLarge, "BODY_TOO_LARGE", "request body exceeds 64 KiB"
		}
		return profile{}, http.StatusBadRequest, "INVALID_JSON", "body must be one JSON profile object"
	}
	if decoded == nil {
		return profile{}, http.StatusBadRequest, "INVALID_JSON", "body must be one JSON profile object"
	}

	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if isBodyTooLarge(err) {
			return profile{}, http.StatusRequestEntityTooLarge, "BODY_TOO_LARGE", "request body exceeds 64 KiB"
		}
		return profile{}, http.StatusBadRequest, "INVALID_JSON", "body must contain exactly one JSON value"
	}

	payload := *decoded
	payload.Name = strings.TrimSpace(payload.Name)
	payload.Email = strings.TrimSpace(payload.Email)
	if payload.Name == "" || utf8.RuneCountInString(payload.Name) > 100 {
		return profile{}, http.StatusUnprocessableEntity, "INVALID_NAME", "name must contain between 1 and 100 characters"
	}
	if payload.Email == "" || len(payload.Email) > 254 {
		return profile{}, http.StatusUnprocessableEntity, "INVALID_EMAIL", "email must be a valid address"
	}
	parsed, err := mail.ParseAddress(payload.Email)
	if err != nil || parsed.Address != payload.Email {
		return profile{}, http.StatusUnprocessableEntity, "INVALID_EMAIL", "email must be a valid address"
	}
	return payload, 0, "", ""
}

func validCSRFToken(value string) bool {
	return subtle.ConstantTimeCompare([]byte(value), []byte(demoCSRFToken)) == 1
}

func isBodyTooLarge(err error) bool {
	var maximumBytesError *http.MaxBytesError
	return errors.As(err, &maximumBytesError)
}

func writeError(response http.ResponseWriter, status int, code, message string) {
	writeJSON(response, status, errorResponse{
		Saved: false,
		Error: errorDetail{Code: code, Message: message},
	})
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	body, err := json.Marshal(value)
	if err != nil {
		http.Error(response, "encode response", http.StatusInternalServerError)
		return
	}
	body = append(body, '\n')
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	response.Header().Set("Content-Length", fmt.Sprintf("%d", len(body)))
	response.WriteHeader(status)
	_, _ = response.Write(body)
}
