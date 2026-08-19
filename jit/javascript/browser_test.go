package javascript

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const browserAssertionSeam = "</body>"

func TestBrowserExamples(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping standalone KitJS browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	kitJS := readVanillaFile(t, "kit.js")
	examples := map[string][]byte{
		"counter":  injectBrowserAssertions(t, readVanillaFile(t, "examples", "counter.html"), counterAssertions),
		"dropdown": injectBrowserAssertions(t, readVanillaFile(t, "examples", "dropdown.html"), dropdownAssertions),
	}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/kit.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(kitJS)
		case "/examples/counter.html":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write(examples["counter"])
		case "/examples/dropdown.html":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write(examples["dropdown"])
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	for _, name := range []string{"counter", "dropdown"} {
		name := name
		t.Run(name, func(t *testing.T) {
			runVanillaBrowser(t, browser, server.URL+"/examples/"+name+".html")
		})
	}
}

func TestBrowserRuntimeOwnership(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping standalone KitJS browser ownership contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	kitJS := readVanillaFile(t, "kit.js")
	fixture := []byte(runtimeOwnershipDocument)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/kit.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(kitJS)
		case "/contracts/runtime-ownership.html":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write(fixture)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	runVanillaBrowser(t, browser, server.URL+"/contracts/runtime-ownership.html")
}

func injectBrowserAssertions(t *testing.T, source []byte, assertions string) []byte {
	t.Helper()
	if got := bytes.Count(bytes.ToLower(source), []byte(browserAssertionSeam)); got != 1 {
		t.Fatalf("example </body> count = %d, want one", got)
	}
	script := []byte("<script>\n" + browserHarness + "\n" + assertions + "\n</script>\n</body>")
	return bytes.Replace(source, []byte(browserAssertionSeam), script, 1)
}

const browserHarness = `(function () {
  "use strict";
  var root = document.documentElement;

  function finish(status, error) {
    root.setAttribute("data-kit-test", status);
    if (error) root.setAttribute("data-kit-test-error", String(error && error.message || error));
  }

  function assert(condition, message) {
    if (!condition) throw new Error(message);
  }

  function waitFor(predicate, message) {
    return new Promise(function (resolve, reject) {
      var deadline = performance.now() + 2000;
      function poll() {
        try {
          if (predicate()) { resolve(); return; }
          if (performance.now() >= deadline) { reject(new Error(message)); return; }
          setTimeout(poll, 8);
        } catch (error) { reject(error); }
      }
      poll();
    });
  }

  function nextTurn() {
    return new Promise(function (resolve) { setTimeout(resolve, 0); });
  }

  function assertPublicContract() {
    assert(globalThis.kit && typeof globalThis.kit === "object", "global kit object missing");
    assert(Object.keys(globalThis.kit).join(",") === "version,component", "public kit keys were " + Object.keys(globalThis.kit).join(","));
    assert(typeof globalThis.kit.version === "string" && globalThis.kit.version.length > 0, "kit.version must be a non-empty string");
    assert(typeof globalThis.kit.component === "function", "kit.component must be a function");
    ["start", "destroy", "use", "mount", "unmount"].forEach(function (name) {
      assert(globalThis.kit[name] === undefined, "private control leaked as kit." + name);
    });
    assert(Object.isFrozen(globalThis.kit), "base public kit object is not frozen");
    assert(globalThis.kit.service === undefined && !Object.prototype.hasOwnProperty.call(globalThis.kit, "service"),
      "private service registrar leaked into the base public contract");
    var external = document.querySelectorAll("script[src]");
    assert(external.length === 1, "example loaded " + external.length + " external scripts instead of one");
    assert(new URL(external[0].src, location.href).pathname === "/kit.js", "example did not load the standalone kit.js");
    assert(!document.querySelector("[data-kit-app],[data-kit-hydrate],[data-kit-plan],[data-kitwork-plan]"), "server activation marker leaked into standalone HTML");
    var getterRejected = false;
    try { globalThis.kit.component("__browser_contract_missing__"); }
    catch (error) { getterRejected = error instanceof TypeError; }
    assert(getterRejected, "kit.component retained a one-argument registry getter");
    assert(globalThis.kit.component("__browser_contract_probe__", {}) === undefined, "kit.component registration must return undefined");
  }

  window.addEventListener("error", function (event) { finish("failed", event.error || event.message); });
  window.addEventListener("unhandledrejection", function (event) { finish("failed", event.reason); });

  function run(test) {
    var start = function () {
      setTimeout(function () {
        Promise.resolve().then(test).then(
          function () { finish("passed"); },
          function (error) { finish("failed", error); }
        );
      }, 0);
    };
    if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", start, { once: true });
    else start();
  }

  globalThis.__runStandaloneKitTest = run;
  globalThis.__kitTestAssert = assert;
  globalThis.__kitTestWaitFor = waitFor;
  globalThis.__kitTestNextTurn = nextTurn;
  globalThis.__kitTestPublicContract = assertPublicContract;
})();`

const counterAssertions = `__runStandaloneKitTest(async function () {
  var assert = __kitTestAssert;
  var waitFor = __kitTestWaitFor;
  __kitTestPublicContract();

  var output = document.getElementById("counter-output");
  var increment = document.getElementById("counter-increment");
  var reset = document.getElementById("counter-reset");
  var decrement = document.getElementById("counter-decrement");
  assert(output && increment && reset && decrement, "counter demo controls missing");
  await waitFor(function () { return output.textContent.trim() === "0"; }, "counter did not initialize at zero");

  // A compiled binding owns its original program. Changing authored source after
  // boot must not make a later dirty-check parse or adopt a second expression.
  output.setAttribute("data-kit-text", "count + 1000");
  increment.click();
  await waitFor(function () { return output.textContent.trim() === "1"; }, "increment did not produce one or expression was recompiled");
  reset.click();
  await waitFor(function () { return output.textContent.trim() === "0"; }, "reset did not return to zero");
  decrement.click();
  await waitFor(function () { return output.textContent.trim() === "-1"; }, "decrement did not produce minus one");
});`

const dropdownAssertions = `__runStandaloneKitTest(async function () {
  var assert = __kitTestAssert;
  var waitFor = __kitTestWaitFor;
  var nextTurn = __kitTestNextTurn;
  __kitTestPublicContract();

  var root = document.getElementById("dropdown-demo");
  var trigger = document.getElementById("dropdown-trigger");
  var menu = document.getElementById("dropdown-menu");
  var inside = document.getElementById("dropdown-settings");
  assert(root && trigger && menu && inside, "dropdown demo controls missing");
  await waitFor(function () { return trigger.getAttribute("aria-expanded") === "false" && menu.hidden; }, "dropdown did not initialize closed");

  trigger.click();
  await waitFor(function () { return trigger.getAttribute("aria-expanded") === "true" && !menu.hidden; }, "dropdown did not open");
  inside.click();
  await nextTurn();
  assert(trigger.getAttribute("aria-expanded") === "true" && !menu.hidden, "inside click closed dropdown");

  document.body.click();
  await waitFor(function () { return trigger.getAttribute("aria-expanded") === "false" && menu.hidden; }, "outside click did not close dropdown");

  trigger.click();
  await waitFor(function () { return trigger.getAttribute("aria-expanded") === "true" && !menu.hidden; }, "dropdown did not reopen");
  trigger.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true }));
  await waitFor(function () { return trigger.getAttribute("aria-expanded") === "false" && menu.hidden; }, "Escape did not close dropdown");
});`

const runtimeOwnershipDocument = `<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><title>KitJS runtime ownership contract</title></head>
<body>
  <section data-kit-component="runtime-broken">
    <output id="runtime-broken-output" data-kit-text="value">server</output>
  </section>
  <main data-kit-component="runtime-ownership" id="runtime-root">
    <section id="dynamic-outside" data-kit-click:outside="outside()">
      <button type="button" id="runtime-click" data-kit-click="click()">Click</button>
    </section>
    <output id="runtime-click-output" data-kit-text="clickCount"></output>
    <output id="runtime-outside-output" data-kit-text="outsideCount"></output>
    <output id="runtime-escape-output" data-kit-text="escapeCount"></output>
  </main>
  <script>
  (function () {
    var original = EventTarget.prototype.addEventListener;
    var counts = { click: 0, keydown: 0 };
    EventTarget.prototype.addEventListener = function (type, listener, options) {
      if (this === document && (type === "click" || type === "keydown")) counts[type]++;
      return original.call(this, type, listener, options);
    };
    globalThis.__kitListenerCounts = counts;
    globalThis.__restoreKitListenerSpy = function () {
      EventTarget.prototype.addEventListener = original;
      delete globalThis.__restoreKitListenerSpy;
    };
  })();
  </script>
  <script src="/kit.js"></script>
  <script>
    globalThis.__firstKit = globalThis.kit;
    globalThis.__kitEventCalls = { click: 0, outside: 0, escape: 0 };
    globalThis.__runtimeDefinition = {
      clickCount: 0,
      outsideCount: 0,
      escapeCount: 0,
      click: function () {
        globalThis.__kitEventCalls.click++;
        this.clickCount++;
      },
      outside: function () {
        globalThis.__kitEventCalls.outside++;
        this.outsideCount++;
      },
      escape: function () {
        globalThis.__kitEventCalls.escape++;
        this.escapeCount++;
      }
    };
    globalThis.kit.component("runtime-broken", { value: 0, init: true });
    globalThis.kit.component("runtime-ownership", globalThis.__runtimeDefinition);
  </script>
  <script src="/kit.js"></script>
  <script>
    if (globalThis.kit !== globalThis.__firstKit) {
      globalThis.kit.component("runtime-ownership", globalThis.__runtimeDefinition);
    }
    globalThis.__restoreKitListenerSpy();
  </script>
  <script>
` + browserHarness + `
` + runtimeOwnershipAssertions + `
  </script>
</body>
</html>`

const runtimeOwnershipAssertions = `__runStandaloneKitTest(async function () {
  var assert = __kitTestAssert;
  var waitFor = __kitTestWaitFor;
  var nextTurn = __kitTestNextTurn;
  var root = document.getElementById("runtime-root");
  var clickOutput = document.getElementById("runtime-click-output");
  var outsideOutput = document.getElementById("runtime-outside-output");
  var escapeOutput = document.getElementById("runtime-escape-output");

  assert(globalThis.kit === globalThis.__firstKit, "evaluating kit.js twice replaced the canonical kit object");
  assert(globalThis.__kitListenerCounts.click === 1, "kit.js installed " + globalThis.__kitListenerCounts.click + " document click runtimes");
  assert(globalThis.__kitListenerCounts.keydown === 1, "kit.js installed " + globalThis.__kitListenerCounts.keydown + " document keydown runtimes");
  assert(document.getElementById("runtime-broken-output").textContent.trim() === "server", "invalid component was partially activated");
  await waitFor(function () {
    return clickOutput.textContent.trim() === "0" &&
      outsideOutput.textContent.trim() === "0" &&
      escapeOutput.textContent.trim() === "0";
  }, "runtime ownership component did not initialize");

  document.getElementById("runtime-click").click();
  await waitFor(function () {
    return globalThis.__kitEventCalls.click === 1 && clickOutput.textContent.trim() === "1";
  }, "one delegated click did not execute exactly once");

  var outside = document.getElementById("dynamic-outside");
  document.body.click();
  await waitFor(function () {
    return globalThis.__kitEventCalls.outside === 1 && outsideOutput.textContent.trim() === "1";
  }, "prepared outside directive was not live");

  outside.removeAttribute("data-kit-click:outside");
  document.body.click();
  await nextTurn();
  assert(globalThis.__kitEventCalls.outside === 1, "removed outside directive still executed");

  outside.setAttribute("data-kit-click:outside", "outside()");
  document.body.click();
  await waitFor(function () { return globalThis.__kitEventCalls.outside === 2; }, "restored outside directive was not live");
  outside.remove();
  document.body.click();
  await nextTurn();
  assert(globalThis.__kitEventCalls.outside === 2, "detached outside candidate still executed");

  var escape = document.createElement("section");
  var escapeTarget = document.createElement("button");
  escape.id = "dynamic-escape";
  escape.setAttribute("data-kit-keydown:escape", "escape()");
  escape.appendChild(escapeTarget);
  root.appendChild(escape);
  escapeTarget.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true }));
  await waitFor(function () {
    return globalThis.__kitEventCalls.escape === 1 && escapeOutput.textContent.trim() === "1";
  }, "Escape directive added after boot was not live");

  escape.removeAttribute("data-kit-keydown:escape");
  escapeTarget.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true }));
  await nextTurn();
  assert(globalThis.__kitEventCalls.escape === 1, "removed Escape directive still executed");

  escape.setAttribute("data-kit-keydown:escape", "escape()");
  escapeTarget.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true }));
  await waitFor(function () { return globalThis.__kitEventCalls.escape === 2; }, "restored Escape directive was not live");
});`

func runVanillaBrowser(t *testing.T, browser, target string) {
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
		"--virtual-time-budget=5000",
		"--dump-dom",
		target,
	}
	output, runErr := exec.CommandContext(ctx, browser, args...).CombinedOutput()
	if bytes.Contains(output, []byte(`data-kit-test="passed"`)) {
		return
	}
	if ctx.Err() != nil {
		t.Fatalf("headless browser proof timed out: %v\n%s", ctx.Err(), boundedVanillaOutput(output))
	}
	if runErr != nil {
		t.Fatalf("headless browser proof failed to run: %v\n%s", runErr, boundedVanillaOutput(output))
	}
	t.Fatalf("headless browser proof did not pass\n%s", boundedVanillaOutput(output))
}

func findVanillaBrowser() string {
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
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path
		}
	}
	return ""
}

func boundedVanillaOutput(output []byte) string {
	const limit = 16 << 10
	header := ""
	if start := bytes.Index(output, []byte("<html")); start >= 0 {
		if end := bytes.IndexByte(output[start:], '>'); end >= 0 {
			header = string(output[start:start+end+1]) + "\n"
		}
	}
	if len(output) > limit {
		output = output[len(output)-limit:]
	}
	return fmt.Sprintf("browser root: %s browser output (last %d bytes):\n%s", header, len(output), strings.TrimSpace(string(output)))
}
