package js

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	hydrate "github.com/kitwork/engine/jit/hydrate"
)

// Two of apptop.vn's tenant components, VERBATIM (apps/…/apptop.vn/components, 22/09/2026), run on
// the kernel with no change to their bodies: init(context) hands them the same host/cleanup the
// component runtime did (B9, one kernel — step 1). favicon-fallback mounts on a host with no
// directive inside and owns an <img>; stats-live fetches, paints its own DOM, and stops its timers
// through context.cleanup when morph removes the host.
const apptopFaviconFallback = `kit.component("apptop-favicon", {
  init: function (context) {
    var image = context.host.querySelector("[data-favicon-primary]");
    if (!image) return;

    var storedProfile = context.host.querySelector("[data-profile-avatar]");
    if (storedProfile) {
      image.hidden = true;
      return;
    }

    var showFallback = function () {
      image.hidden = true;
    };
    var showPrimary = function () {
      image.hidden = false;
    };

    image.addEventListener("error", showFallback);
    image.addEventListener("load", showPrimary);
    if (image.complete && image.naturalWidth < 1) showFallback();

    context.cleanup(function () {
      image.removeEventListener("error", showFallback);
      image.removeEventListener("load", showPrimary);
    });
  }
});`

const apptopStatsLive = `kit.component("apptop-stats", {
  init: function (context) {
    var root = context.host;
    var endpoint = root.getAttribute("data-stats-endpoint") || "/stats/data";
    var status = root.querySelector("[data-stats-status]");
    var refreshTimer = 0;
    var firstRefresh = 0;
    var stopped = false;

    function paint(stats) {
      root.querySelectorAll("[data-live-stat]").forEach(function (element) {
        var key = element.getAttribute("data-live-stat");
        if (stats[key] != null) element.textContent = String(stats[key]);
      });
    }

    function paintActivities(activities) {
      var rows = root.querySelectorAll("[data-activity-slot]");
      rows.forEach(function (row, index) {
        var activity = activities[index];
        if (!activity) {
          row.hidden = true;
          return;
        }
        row.hidden = false;
        row.querySelector("[data-activity-title]").textContent = activity.title;
        row.querySelector("[data-activity-meta]").textContent = activity.kind + " · " + activity.age;
        row.querySelector("[data-activity-amount]").textContent = activity.amountLabel;
        row.querySelector("[data-activity-bid]").textContent = "hiện " + activity.bidLabel;
      });
    }

    function refresh() {
      fetch(endpoint, {
        credentials: "same-origin",
        cache: "no-store",
        headers: { "Accept": "application/json" }
      })
        .then(function (response) {
          if (!response.ok) throw new Error("HTTP " + response.status);
          return response.json();
        })
        .then(function (payload) {
          if (stopped || !payload || !payload.ok || !payload.stats) return;
          paint(payload.stats);
          if (payload.activities) paintActivities(payload.activities);
          if (status) status.textContent = "Vừa cập nhật";
        })
        .catch(function () {
          if (status) status.textContent = "Tạm dừng tự động cập nhật";
        });
    }

    firstRefresh = window.setTimeout(refresh, 1000);
    refreshTimer = window.setInterval(refresh, 15000);
    context.cleanup(function () {
      stopped = true;
      window.clearTimeout(firstRefresh);
      window.clearInterval(refreshTimer);
    });
  }
});`

func TestBrowserApptopTenantComponentsRunVerbatimOnTheKernel(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping tenant component browser proof in short mode")
	}
	browser := findHeadlessBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	page := `<!doctype html>
<html lang="vi" data-kit-app="apptop"><head><meta charset="utf-8"><title>apptop on the kernel</title></head><body>
  <div id="stats-parent">
    <section id="stats" data-kit-component="apptop-stats" data-stats-endpoint="/stats/data">
      <b data-live-stat="bids">0</b>
      <i data-stats-status>…</i>
      <div data-activity-slot hidden><span data-activity-title></span><span data-activity-meta></span><span data-activity-amount></span><span data-activity-bid></span></div>
    </section>
  </div>
  <figure id="avatar" data-kit-component="apptop-favicon"><img data-favicon-primary src="/missing.png" alt=""></figure>
  <script src="/kit.js"></script>
  <script>` + apptopFaviconFallback + `</script>
  <script>` + apptopStatsLive + `</script>
  <script>
  (async function () {
    var root = document.documentElement;
    function fail(message) { root.setAttribute("data-kit-test", "failed"); root.setAttribute("data-kit-test-error", message); throw new Error(message); }
    function assert(condition, message) { if (!condition) fail(message); }
    function wait(ms) { return new Promise(function (resolve) { setTimeout(resolve, ms); }); }
    var cleared = 0, originalClearInterval = window.clearInterval;
    window.clearInterval = function (id) { cleared++; return originalClearInterval.call(window, id); };

    await wait(50);
    assert(window.kit && window.kit.blueprints && window.kit.blueprints["apptop-stats"] && window.kit.blueprints["apptop-favicon"], "both tenant blueprints registered through kit.component");
    assert(window.kit.components["apptop-favicon"], "a host with NO directive inside must still mount (init-only component)");

    await wait(1300);
    assert(document.querySelector("[data-live-stat=bids]").textContent === "42", "stats-live: init ran with context.host, fetched, painted its own DOM (got " + document.querySelector("[data-live-stat=bids]").textContent + ")");
    assert(document.querySelector("[data-stats-status]").textContent === "Vừa cập nhật", "stats-live: status painted");
    var slot = document.querySelector("[data-activity-slot]");
    assert(slot.hidden === false && slot.querySelector("[data-activity-title]").textContent === "Ship", "stats-live: activity row painted");

    var image = document.querySelector("[data-favicon-primary]");
    assert(image.hidden === true, "favicon-fallback: a broken primary image is hidden by the component's own listener");

    // morph drops the stats host → context.cleanup runs: stopped + timers cleared
    var parent = document.getElementById("stats-parent");
    var replacement = parent.cloneNode(false);
    window.kit.morph(parent, replacement);
    assert(!document.getElementById("stats"), "morph removed the stats host");
    assert(cleared === 1, "stats-live: context.cleanup ran on removal and cleared its interval (cleared=" + cleared + ")");

    root.setAttribute("data-kit-test", "passed");
  })().catch(function (error) { if (!root.hasAttribute("data-kit-test")) { root.setAttribute("data-kit-test", "failed"); root.setAttribute("data-kit-test-error", String(error && error.message || error)); } });
  </script>
</body></html>`
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case hydrate.RuntimePath:
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write([]byte(hydrate.Runtime()))
		case "/stats/data":
			response.Header().Set("Content-Type", "application/json")
			_, _ = response.Write([]byte(`{"ok":true,"stats":{"bids":42},"activities":[{"title":"Ship","kind":"bid","age":"1m","amountLabel":"1.000đ","bidLabel":"2.000đ"}]}`))
		case "/":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(page))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	profile := t.TempDir()
	args := []string{"--headless=new", "--disable-gpu", "--no-sandbox", "--disable-dev-shm-usage", "--disable-background-networking",
		"--disable-default-apps", "--disable-extensions", "--disable-sync", "--metrics-recording-only", "--no-first-run",
		"--run-all-compositor-stages-before-draw", "--user-data-dir=" + filepath.Join(profile, "p"), "--virtual-time-budget=8000", "--dump-dom", server.URL + "/"}
	if runtime.GOOS == "windows" {
		args = append([]string{"--disable-background-mode", "--disable-breakpad", "--disable-crashpad-for-testing"}, args...)
	}
	output, err := exec.CommandContext(ctx, browser, args...).CombinedOutput()
	if bytes.Contains(output, []byte(`data-kit-test="passed"`)) {
		return
	}
	if i := bytes.Index(output, []byte("data-kit-test-error=")); i >= 0 {
		end := i + 300
		if end > len(output) {
			end = len(output)
		}
		t.Fatalf("tenant component proof failed: %s", output[i:end])
	}
	t.Fatalf("tenant component proof did not pass (err=%v):\n%.2000s", err, output)
}

// The shortcut component, ported from the KitJS catalogue (22/09): a keyboard shortcut that clicks
// its host. Driven in a real browser because the discipline IS the behaviour — exactly one of
// Ctrl/Cmd, no Alt or Shift, no repeat, and an event something else handled is left alone.
func TestBrowserShortcutComponentClicksItsHost(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping shortcut browser proof in short mode")
	}
	browser := findHeadlessBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	page := `<!doctype html>
<html lang="en" data-kit-app="shortcut"><head><meta charset="utf-8"><title>shortcut</title></head><body>
  <!-- the undeclared host comes FIRST: its listener runs first, so if it ever answered it would
       take the key (preventDefault) and the declared host below would never see it -->
  <a id="plain" href="#plain" data-kit-component="shortcut">No shortcut declared</a>
  <a id="search" href="#search" data-kit-component="shortcut" data-shortcut="mod+k">Search</a>
  <script src="/kit.js"></script>
  <script>
  (async function () {
    var root = document.documentElement;
    function fail(m) { root.setAttribute("data-kit-test", "failed"); root.setAttribute("data-kit-test-error", m); throw new Error(m); }
    function assert(c, m) { if (!c) fail(m); }
    function tick() { return new Promise(function (r) { setTimeout(r, 20); }); }
    var clicks = 0, plainClicks = 0, lastDefaultPrevented = null;
    document.getElementById("search").addEventListener("click", function (e) { e.preventDefault(); clicks++; });
    document.getElementById("plain").addEventListener("click", function (e) { e.preventDefault(); plainClicks++; });
    function press(init) {
      var e = new KeyboardEvent("keydown", Object.assign({ key: "k", bubbles: true, cancelable: true }, init));
      document.dispatchEvent(e);
      lastDefaultPrevented = e.defaultPrevented;
      return e;
    }
    await tick();
    assert(window.kit.blueprints.shortcut, "the shortcut blueprint must be registered");

    press({ ctrlKey: true }); await tick();
    assert(clicks === 1, "Ctrl+K must click the host, got " + clicks);
    assert(lastDefaultPrevented === true, "the component must take the key from the browser");

    press({ metaKey: true }); await tick();
    assert(clicks === 2, "Cmd+K must click the host too, got " + clicks);

    // discipline: each of these must be ignored
    press({ ctrlKey: true, metaKey: true });
    press({ ctrlKey: true, altKey: true });
    press({ ctrlKey: true, shiftKey: true });
    press({ ctrlKey: true, repeat: true });
    press({});
    var other = new KeyboardEvent("keydown", { key: "j", ctrlKey: true, bubbles: true, cancelable: true });
    document.dispatchEvent(other);
    await tick();
    assert(clicks === 2, "Ctrl+Cmd, Alt, Shift, a repeat, a bare k and another key must all be ignored (clicks=" + clicks + ")");

    // a host that declares no data-shortcut is never triggered
    // A host that declares no data-shortcut is inert. Checked LAST and against a fresh press, so a
    // regression here cannot hide behind the earlier counts.
    press({ ctrlKey: true }); await tick();
    assert(plainClicks === 0, "a host without data-shortcut must never be clicked (" + plainClicks + ")");
    assert(clicks === 3, "the declared host still answers (" + clicks + ")");

    root.setAttribute("data-kit-test", "passed");
  })().catch(function (error) { if (!root.hasAttribute("data-kit-test")) { root.setAttribute("data-kit-test", "failed"); root.setAttribute("data-kit-test-error", String(error && error.message || error)); } });
  </script>
</body></html>`
	rendered := Render(page)
	if !strings.Contains(rendered, "components=component%3Ashortcut") {
		t.Fatalf("Render should inject the shortcut component:\n%s", rendered)
	}
	runKernelPage(t, browser, page, scanModules(page, nil))
}

// runKernelPage serves one page plus /kit.js (kernel + the named modules) and drives it in a real
// headless browser, failing with whatever the page wrote into data-kit-test-error.
func runKernelPage(t *testing.T, browser, page string, moduleNames []string) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case hydrate.RuntimePath:
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write([]byte(hydrate.Runtime() + "\n" + ModulesJS(ModuleKeys(moduleNames))))
		case "/":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(page))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	profile := t.TempDir()
	args := []string{"--headless=new", "--disable-gpu", "--no-sandbox", "--disable-dev-shm-usage", "--disable-background-networking",
		"--disable-default-apps", "--disable-extensions", "--disable-sync", "--metrics-recording-only", "--no-first-run",
		"--run-all-compositor-stages-before-draw", "--user-data-dir=" + filepath.Join(profile, "p"), "--virtual-time-budget=6000", "--dump-dom", server.URL + "/"}
	if runtime.GOOS == "windows" {
		args = append([]string{"--disable-background-mode", "--disable-breakpad", "--disable-crashpad-for-testing"}, args...)
	}
	output, err := exec.CommandContext(ctx, browser, args...).CombinedOutput()
	if bytes.Contains(output, []byte(`data-kit-test="passed"`)) {
		return
	}
	if i := bytes.Index(output, []byte("data-kit-test-error=")); i >= 0 {
		end := i + 300
		if end > len(output) {
			end = len(output)
		}
		t.Fatalf("kernel page proof failed: %s", output[i:end])
	}
	t.Fatalf("kernel page proof did not pass (err=%v):\n%.1500s", err, output)
}
