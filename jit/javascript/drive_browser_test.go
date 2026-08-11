package javascript

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestBrowserDriveContract(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping headless Drive contract in short mode")
	}
	browser := findContractBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.FileServer(http.Dir(root)))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	args := []string{
		"--headless=new",
		"--disable-gpu",
		"--no-sandbox",
		"--disable-dev-shm-usage",
		"--disable-background-networking",
		"--disable-default-apps",
		"--disable-extensions",
		"--disable-sync",
		"--metrics-recording-only",
		"--no-first-run",
		"--run-all-compositor-stages-before-draw",
		"--user-data-dir=" + t.TempDir(),
		"--virtual-time-budget=5000",
		"--dump-dom",
		server.URL + "/test/drive/contract.html",
	}
	output, runErr := exec.CommandContext(ctx, browser, args...).CombinedOutput()
	if bytes.Contains(output, []byte(`data-test-status="passed"`)) {
		return
	}
	if ctx.Err() != nil {
		t.Fatalf("headless Drive contract timed out: %v\n%s", ctx.Err(), boundedBrowserOutput(output))
	}
	if runErr != nil {
		t.Fatalf("headless Drive contract failed to run: %v\n%s", runErr, boundedBrowserOutput(output))
	}
	t.Fatalf("headless Drive contract did not pass\n%s", boundedBrowserOutput(output))
}
