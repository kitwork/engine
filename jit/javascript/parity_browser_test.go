package javascript_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"
	"time"

	kitjavascript "github.com/kitwork/engine/jit/javascript"
	"github.com/kitwork/engine/work"
)

const (
	counterStartMarker = "<!-- COUNTER_PROOF_START -->"
	counterEndMarker   = "<!-- COUNTER_PROOF_END -->"
	assertionMarker    = `<template id="kitjs-parity-assertions"></template>`
)

var (
	externalScriptPattern = regexp.MustCompile(`(?i)<script\b[^>]*\bsrc\s*=`)
	externalSourcePattern = regexp.MustCompile(`(?i)<script\b[^>]*\bsrc="([^"]+)"[^>]*>`)
	runtimeSourcePattern  = regexp.MustCompile(`(?i)<script\b[^>]*\bdata-kitwork-runtime\b[^>]*\bsrc="([^"]+)"[^>]*>`)
)

const counterParityAssertions = `(function () {
  "use strict";
  var root = document.documentElement;

  function fail(error) {
    root.setAttribute("data-proof-status", "failed");
    root.setAttribute("data-proof-error", String(error && error.message || error));
  }

  function assert(condition, message) {
    if (!condition) throw new Error(message);
  }

  window.addEventListener("error", function (event) { fail(event.error || event.message); });
  window.addEventListener("unhandledrejection", function (event) { fail(event.reason); });

  document.addEventListener("DOMContentLoaded", function () {
    setTimeout(function () {
      try {
        assert(document.querySelectorAll("script[src]").length === 1, "expected exactly one external script");
        assert(!document.querySelector("[data-kit-app],[data-kit-hydrate]"), "root app/hydrate marker leaked");

        var output = document.getElementById("count");
        var increment = document.getElementById("increment");
        var decrement = document.getElementById("decrement");
        assert(output && increment && decrement, "counter proof markup missing");
        assert(output.textContent === "0", "initial count was not zero");

        increment.click();
        setTimeout(function () {
          try {
            assert(output.textContent === "1", "increment did not produce one");
            decrement.click();
            setTimeout(function () {
              try {
                assert(output.textContent === "0", "decrement did not return to zero");
                root.setAttribute("data-proof-status", "passed");
              } catch (error) { fail(error); }
            }, 0);
          } catch (error) { fail(error); }
        }, 0);
      } catch (error) { fail(error); }
    }, 0);
  }, { once: true });
})();`

type counterProof struct {
	page      []byte
	assetPath string
	asset     []byte
}

func TestCounterParityFixturesShareExactMarkupAndDeliveryOwnership(t *testing.T) {
	standalone := readParityFixture(t, "standalone", "index.html")
	kitworkIndex := readParityFixture(t, "kitwork", "index.kitwork.html")
	kitworkPage := readParityFixture(t, "kitwork", "page.kitwork.html")
	kitworkRouter := readParityFixture(t, "kitwork", "router.kitwork.js")

	standaloneCounter := counterMarkup(t, standalone)
	kitworkCounter := counterMarkup(t, kitworkPage)
	if !bytes.Equal(standaloneCounter, kitworkCounter) {
		t.Fatalf("standalone and Kitwork counter markup drifted:\nstandalone:\n%s\nKitwork:\n%s", standaloneCounter, kitworkCounter)
	}

	if got := len(externalScriptPattern.FindAll(standalone, -1)); got != 1 {
		t.Fatalf("standalone fixture has %d external scripts, want one", got)
	}
	if bytes.Contains(standalone, []byte("data-kitwork-runtime")) || bytes.Contains(standalone, []byte("data-kitwork-plan")) {
		t.Fatal("standalone fixture without Drive must use an ordinary script without Kitwork plan markers")
	}
	if got := len(externalScriptPattern.FindAll(kitworkIndex, -1)); got != 0 {
		t.Fatalf("Kitwork fixture authored %d external scripts; Go must inject the only runtime", got)
	}
	if !bytes.Contains(kitworkRouter, []byte("router.kitjs(true)")) {
		t.Fatal("Kitwork fixture did not enable the Go KitJS adapter")
	}

	for name, source := range map[string][]byte{
		"standalone": standalone,
		"kitwork":    append(append([]byte(nil), kitworkIndex...), kitworkPage...),
	} {
		if bytes.Contains(source, []byte("data-kit-app=")) || bytes.Contains(source, []byte("data-kit-hydrate=")) {
			t.Fatalf("%s fixture contains an authored root app/hydrate marker", name)
		}
		if bytes.Count(source, []byte(assertionMarker)) != 1 {
			t.Fatalf("%s fixture must expose exactly one assertion seam", name)
		}
	}
}

func TestBrowserStandaloneAndKitworkCounterParity(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping standalone/Kitwork browser parity in short mode")
	}
	browser := parityBrowserExecutable()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	composer, err := kitjavascript.NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	standaloneBundle, err := composer.ComposeStandalone([]kitjavascript.ComponentRef{{Name: "counter", Version: "1.0.0"}}, false)
	if err != nil {
		t.Fatal(err)
	}
	standalone := standaloneCounterProof(t, standaloneBundle)
	kitwork := kitworkCounterProof(t)

	if !bytes.Equal(standalone.asset, kitwork.asset) {
		t.Fatal("standalone and Kitwork counter fixtures received different generated artifacts")
	}
	if len(externalScriptPattern.FindAll(standalone.page, -1)) != 1 || len(externalScriptPattern.FindAll(kitwork.page, -1)) != 1 {
		t.Fatal("a final proof document does not contain exactly one external script")
	}

	for name, proof := range map[string]counterProof{"standalone": standalone, "kitwork": kitwork} {
		t.Run(name, func(t *testing.T) {
			server := serveCounterProof(proof)
			defer server.Close()
			runCounterProofBrowser(t, browser, server.URL)
		})
	}
}

func standaloneCounterProof(t *testing.T, bundle kitjavascript.Bundle) counterProof {
	t.Helper()
	if bundle.Empty() || bundle.ContentHash == "" {
		t.Fatal("standalone composer returned an empty counter artifact")
	}
	source := readParityFixture(t, "standalone", "index.html")
	source = bytes.ReplaceAll(source, []byte("__KITJS_PLAN__"), []byte(bundle.ContentHash))
	page := injectParityAssertions(t, source)
	assetPath := "/kitjs." + bundle.ContentHash + ".js"
	match := externalSourcePattern.FindSubmatch(page)
	if len(match) != 2 || string(match[1]) != assetPath {
		t.Fatalf("standalone fixture runtime source = %q, want %q", match, assetPath)
	}
	return counterProof{page: page, assetPath: assetPath, asset: append([]byte(nil), bundle.JavaScript...)}
}

func kitworkCounterProof(t *testing.T) counterProof {
	t.Helper()
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(parityFixturePath("kitwork"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		source := readParityFixture(t, "kitwork", entry.Name())
		if err := os.WriteFile(filepath.Join(directory, entry.Name()), source, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	tenant := work.NewTenant(root, "localhost")
	t.Cleanup(tenant.Close)
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}

	pageRecorder := httptest.NewRecorder()
	tenant.Serve(pageRecorder, httptest.NewRequest(http.MethodGet, "http://localhost/", nil))
	if pageRecorder.Code != http.StatusOK {
		t.Fatalf("Kitwork fixture status=%d body=%s", pageRecorder.Code, pageRecorder.Body.String())
	}
	rawPage := pageRecorder.Body.Bytes()
	if bytes.Contains(rawPage, []byte("data-kit-app=")) || bytes.Contains(rawPage, []byte("data-kit-hydrate=")) {
		t.Fatal("Go-injected Kitwork document invented an app/hydrate root marker")
	}
	if got := len(externalScriptPattern.FindAll(rawPage, -1)); got != 1 {
		t.Fatalf("Go-injected Kitwork document has %d external scripts, want one", got)
	}
	match := runtimeSourcePattern.FindSubmatch(rawPage)
	if len(match) != 2 {
		t.Fatalf("Go-injected runtime source missing from:\n%s", rawPage)
	}
	assetPath := string(match[1])

	assetRecorder := httptest.NewRecorder()
	tenant.Serve(assetRecorder, httptest.NewRequest(http.MethodGet, "http://localhost"+assetPath, nil))
	if assetRecorder.Code != http.StatusOK {
		t.Fatalf("Kitwork runtime %s status=%d", assetPath, assetRecorder.Code)
	}
	return counterProof{
		page:      injectParityAssertions(t, rawPage),
		assetPath: assetPath,
		asset:     append([]byte(nil), assetRecorder.Body.Bytes()...),
	}
}

func serveCounterProof(proof counterProof) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write(proof.page)
		case proof.assetPath:
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(proof.asset)
		default:
			http.NotFound(response, request)
		}
	}))
}

func injectParityAssertions(t *testing.T, source []byte) []byte {
	t.Helper()
	if bytes.Count(source, []byte(assertionMarker)) != 1 {
		t.Fatal("proof document must contain exactly one assertion seam")
	}
	script := []byte("<script>\n" + counterParityAssertions + "\n</script>")
	return bytes.Replace(source, []byte(assertionMarker), script, 1)
}

func counterMarkup(t *testing.T, source []byte) []byte {
	t.Helper()
	start := bytes.Index(source, []byte(counterStartMarker))
	end := bytes.Index(source, []byte(counterEndMarker))
	if start < 0 || end < 0 || end <= start {
		t.Fatal("counter proof markers are missing or out of order")
	}
	start += len(counterStartMarker)
	return bytes.TrimSpace(source[start:end])
}

func readParityFixture(t *testing.T, path ...string) []byte {
	t.Helper()
	source, err := os.ReadFile(parityFixturePath(path...))
	if err != nil {
		t.Fatal(err)
	}
	return source
}

func parityFixturePath(path ...string) string {
	_, filename, _, _ := runtime.Caller(0)
	parts := append([]string{filepath.Dir(filename), "test", "parity"}, path...)
	return filepath.Join(parts...)
}

func runCounterProofBrowser(t *testing.T, browser, target string) {
	t.Helper()
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
		"--virtual-time-budget=4000",
		"--dump-dom",
		target,
	}
	output, runErr := exec.CommandContext(ctx, browser, args...).CombinedOutput()
	if bytes.Contains(output, []byte(`data-proof-status="passed"`)) {
		return
	}
	if ctx.Err() != nil {
		t.Fatalf("headless parity proof timed out: %v\n%s", ctx.Err(), boundedParityOutput(output))
	}
	if runErr != nil {
		t.Fatalf("headless parity proof failed to run: %v\n%s", runErr, boundedParityOutput(output))
	}
	t.Fatalf("headless parity proof did not pass\n%s", boundedParityOutput(output))
}

func parityBrowserExecutable() string {
	for _, name := range []string{"google-chrome", "chromium", "chromium-browser", "chrome", "msedge"} {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	if runtime.GOOS != "windows" {
		return ""
	}
	for _, path := range []string{
		filepath.Join(os.Getenv("ProgramFiles"), "Google", "Chrome", "Application", "chrome.exe"),
		filepath.Join(os.Getenv("ProgramFiles(x86)"), "Microsoft", "Edge", "Application", "msedge.exe"),
		filepath.Join(os.Getenv("LOCALAPPDATA"), "Google", "Chrome", "Application", "chrome.exe"),
	} {
		if path == "" {
			continue
		}
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path
		}
	}
	return ""
}

func boundedParityOutput(output []byte) string {
	const limit = 16 << 10
	if len(output) > limit {
		output = output[len(output)-limit:]
	}
	return fmt.Sprintf("browser output (last %d bytes):\n%s", len(output), output)
}
