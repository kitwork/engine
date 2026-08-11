package javascript

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestBrowserServicesFixtureCoversDefaultCatalog(t *testing.T) {
	t.Parallel()
	services, err := os.ReadFile("test/services.html")
	if err != nil {
		t.Fatal(err)
	}
	components, err := os.ReadFile("test/components.html")
	if err != nil {
		t.Fatal(err)
	}
	registry, err := DefaultRegistry()
	if err != nil {
		t.Fatal(err)
	}

	covered := 0
	for _, module := range registry.modules {
		if module.ID.Kind != ServiceModule && module.ID.Kind != ComponentModule {
			continue
		}
		covered++
		marker := `src="../` + module.Path + `"`
		source := services
		if module.ID.Kind == ComponentModule {
			source = components
		}
		if !bytes.Contains(source, []byte(marker)) {
			t.Errorf("browser fixture for %s does not load it with %q", module.ID.Kind, marker)
		}
	}
	if covered != 25 {
		t.Fatalf("browser fixture catalog check covered %d service/component modules, want 25", covered)
	}
}

func TestBrowserServicesFixture(t *testing.T) {
	runBrowserContractFixture(t, "/test/services.html")
}

func TestBrowserComponentsFixture(t *testing.T) {
	runBrowserContractFixture(t, "/test/components.html")
}

func runBrowserContractFixture(t *testing.T, fixture string) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping headless browser contract in short mode")
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
	profile := t.TempDir()
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
		"--user-data-dir=" + profile,
		"--virtual-time-budget=4000",
		"--dump-dom",
		server.URL + fixture,
	}
	output, runErr := exec.CommandContext(ctx, browser, args...).CombinedOutput()
	if bytes.Contains(output, []byte(`data-test-status="passed"`)) {
		return
	}
	if ctx.Err() != nil {
		t.Fatalf("headless browser contract timed out: %v\n%s", ctx.Err(), boundedBrowserOutput(output))
	}
	if runErr != nil {
		t.Fatalf("headless browser contract failed to run: %v\n%s", runErr, boundedBrowserOutput(output))
	}
	t.Fatalf("headless browser contract did not pass\n%s", boundedBrowserOutput(output))
}

func findContractBrowser() string {
	if configured := strings.TrimSpace(os.Getenv("KITJS_BROWSER")); configured != "" {
		if info, err := os.Stat(configured); err == nil && !info.IsDir() {
			return configured
		}
		if path, err := exec.LookPath(configured); err == nil {
			return path
		}
	}
	for _, name := range []string{"google-chrome", "chromium", "chromium-browser", "chrome", "msedge"} {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	if runtime.GOOS == "windows" {
		for _, path := range []string{
			`C:\Program Files\Google\Chrome\Application\chrome.exe`,
			`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
		} {
			if info, err := os.Stat(path); err == nil && !info.IsDir() {
				return path
			}
		}
	}
	return ""
}

func boundedBrowserOutput(output []byte) string {
	const limit = 12000
	if len(output) > limit {
		output = output[len(output)-limit:]
	}
	return fmt.Sprintf("browser output (last %d bytes):\n%s", len(output), output)
}
