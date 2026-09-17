package javascript

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
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

  // Under --virtual-time-budget a timer costs no real time: the clock jumps to the next timer
  // as soon as the page is idle, and only a pending network request holds it still. So the
  // timeout below is a count of idle polls, not a duration, and it says nothing about events
  // that arrive from outside the renderer — a streamed body chunk, a cookie-change
  // notification, a browser-process reply. When the virtual budget runs out, the wait spends
  // a bounded number of same-origin probe requests, each a real round trip through the network
  // service, before it calls the condition missing. A wait that succeeds never gets here.
  // Calibration: a first body chunk held back 400ms after its headers (the request-form
  // fixture's shape, which lost a ubuntu run) needs about 1000 probes locally; 200 was short.
  // A probe is not free of virtual time either — measured, about 5ms each while a timer is
  // pending — so the grace is also capped in virtual time, or a wait that will never pass
  // spends the whole budget and the dump carries no message at all.
  var probeGrace = 1000;
  var probeGraceVirtualMS = 1500;

  // The probe is an image load, not a fetch: fixtures replace globalThis.fetch to record or
  // fail Drive's requests — one installs its spy before this harness even runs — and the probe
  // must be invisible to them. An Image object loads without joining the document, every
  // fixture server answers an unknown path with 404, and the error event fires only after the
  // round trip. The serial defeats the memory cache.
  var probeSerial = 0;
  function networkTick() {
    return new Promise(function (resolve) {
      var image = new Image();
      image.onload = image.onerror = function () { resolve(); };
      image.src = "/__kit-test-probe?" + (probeSerial++);
    });
  }

  function waitFor(predicate, message, timeout, interval) {
    return new Promise(function (resolve, reject) {
      var duration = typeof timeout === "number" && timeout > 0 ? timeout : 2000;
      var delay = typeof interval === "number" && interval >= 0 ? interval : 8;
      var deadline = performance.now() + duration;
      var grace = probeGrace;
      function poll() {
        try {
          if (predicate()) { resolve(); return; }
          if (performance.now() >= deadline) {
            if (grace-- <= 0 || performance.now() - deadline > probeGraceVirtualMS) { reject(new Error(message)); return; }
            networkTick().then(poll);
            return;
          }
          setTimeout(poll, delay);
        } catch (error) { reject(error); }
      }
      poll();
    });
  }

  function nextTurn() {
    return new Promise(function (resolve) { setTimeout(resolve, 0); });
  }

  // A native navigation proves itself with a cookie its response sets. document.cookie is not
  // where to wait for it: Chromium answers that getter from a renderer-side cache keyed on a
  // shared-memory version that the network service bumps from a posted task, after the response
  // that stored the cookie was delivered. Polling it costs no real time, so a 2000ms virtual
  // budget burned before that task landed — the "did not hard-navigate" failures the ubuntu
  // runner produced in four of seven runs, on a different fixture each time, while every run
  // elsewhere passed. The Cookie Store API reads the store itself; loopback origins are secure
  // contexts, so it is present in every fixture.
  function hasCookie(name) {
    if (globalThis.cookieStore && typeof globalThis.cookieStore.get === "function") {
      return globalThis.cookieStore.get(name).then(function (cookie) { return cookie !== null && cookie !== undefined; });
    }
    return Promise.resolve(document.cookie.split("; ").some(function (pair) { return pair.indexOf(name + "=") === 0; }));
  }

  // Resolves like the pending promise, but keeps a probe request in flight until it settles.
  // For anything answered from outside the renderer — a cookie-store read, the popstate that
  // history.back() produces — a bare await leaves nothing pending, and virtual time leaps to
  // the next timer, or to the end of the budget, the moment the page goes idle. Bounded by the
  // same probe count as waitFor's grace, so a promise that never settles still names itself.
  function anchored(pending, message) {
    var settled = false;
    var remaining = probeGrace;
    pending.then(function () { settled = true; }, function () { settled = true; });
    function spin() {
      if (settled) return Promise.resolve();
      if (remaining-- <= 0) throw new Error(message || "anchored promise did not settle");
      return networkTick().then(spin);
    }
    return spin().then(function () { return pending; });
  }

  function waitForCookie(name, message, timeout, interval) {
    var duration = typeof timeout === "number" && timeout > 0 ? timeout : 2000;
    var delay = typeof interval === "number" && interval >= 0 ? interval : 8;
    var deadline = performance.now() + duration;
    function attempt() {
      return anchored(hasCookie(name), "cookie store read for " + name + " did not answer").then(function (present) {
        if (present) return;
        if (performance.now() >= deadline) throw new Error(message + " (cookie " + name + " never reached the store)");
        return new Promise(function (resolve) { setTimeout(resolve, delay); }).then(attempt);
      });
    }
    return attempt();
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

  // A dump with no data-kit-test attribute means the virtual budget ran out mid-test. This
  // stamps how far the virtual clock had got, so that dump says whether the budget was spent
  // step by step or leapt over — an await with nothing pending jumps straight to the end.
  setInterval(function () { root.setAttribute("data-kit-test-clock", String(Math.round(performance.now()))); }, 500);

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
  globalThis.__kitTestWaitForCookie = waitForCookie;
  globalThis.__kitTestAnchored = anchored;
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
	runVanillaBrowserWithBudget(t, browser, target, 5000)
}

func runVanillaBrowserWithBudget(t *testing.T, browser, target string, virtualTimeBudgetMS int) {
	t.Helper()
	if virtualTimeBudgetMS <= 0 {
		t.Fatalf("headless browser virtual-time budget must be positive; got %d", virtualTimeBudgetMS)
	}
	commandTimeout := 25 * time.Second
	if virtualTimeBudgetMS > 20000 {
		commandTimeout = 45 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
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
		"--user-data-dir=" + newHeadlessBrowserProfile(t),
		fmt.Sprintf("--virtual-time-budget=%d", virtualTimeBudgetMS),
		"--dump-dom",
		target,
	}
	output, runErr := runHeadlessBrowserCommand(ctx, browser, args...)
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

func runHeadlessBrowserCommand(ctx context.Context, browser string, args ...string) ([]byte, error) {
	if runtime.GOOS == "windows" {
		args = append([]string{
			"--disable-background-mode",
			"--disable-breakpad",
			"--disable-crashpad-for-testing",
		}, args...)
	}
	command := exec.CommandContext(ctx, browser, args...)
	if runtime.GOOS == "windows" {
		command.WaitDelay = 5 * time.Second
		command.Cancel = func() error {
			if command.Process == nil {
				return nil
			}
			if err := exec.Command("taskkill", "/PID", strconv.Itoa(command.Process.Pid), "/T", "/F").Run(); err == nil {
				return nil
			}
			if err := command.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
				return err
			}
			return nil
		}
	}
	return command.CombinedOutput()
}

func newHeadlessBrowserProfile(t *testing.T) string {
	t.Helper()
	if runtime.GOOS != "windows" {
		return t.TempDir()
	}
	profile, err := os.MkdirTemp("", "kitwork-browser-*")
	if err != nil {
		t.Fatalf("create headless browser profile: %v", err)
	}
	t.Cleanup(func() {
		if err := removeHeadlessBrowserProfile(profile); err != nil {
			t.Errorf("cleanup headless browser profile: %v", err)
		}
	})
	return profile
}

func removeHeadlessBrowserProfile(profile string) error {
	deadline := time.Now().Add(10 * time.Second)
	for {
		if err := os.RemoveAll(profile); err == nil {
			return nil
		} else if time.Now().After(deadline) {
			return fmt.Errorf("remove headless browser profile %q: %w", profile, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func findVanillaBrowser() string {
	if browserTestsDisabled() {
		return ""
	}
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

func browserTestsDisabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("KITWORK_BROWSER_TESTS"))) {
	case "0", "false", "off":
		return true
	default:
		return false
	}
}

func TestFindVanillaBrowserHonorsDisabledMode(t *testing.T) {
	t.Setenv("KITWORK_BROWSER_TESTS", "off")
	if browser := findVanillaBrowser(); browser != "" {
		t.Fatalf("disabled browser test mode resolved %q", browser)
	}
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
