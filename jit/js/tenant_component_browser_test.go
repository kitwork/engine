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

// dialog@v2.0.0 drives a NATIVE <dialog>: the host is the element, the platform owns focus, Escape
// and the backdrop, and the component adds only what the platform lacks — backdrop click closes,
// the page behind cannot scroll — while keeping `open`/`result` in step however it closed.
func TestBrowserDialogComponentDrivesTheNativeElement(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping dialog browser proof in short mode")
	}
	browser := findHeadlessBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	page := `<!doctype html>
<html lang="en" data-kit-app="dialog"><head><meta charset="utf-8"><title>dialog</title></head><body>
  <button id="open" type="button" data-kit-click="$confirm.show()">Delete</button>
  <dialog id="confirm" data-kit-component="dialog" data-kit-alias="$confirm">
    <p id="state" data-kit-text="open ? 'open' : 'closed:' + result"></p>
    <button id="cancel" type="button" data-kit-click="close('cancel')">Cancel</button>
  </dialog>
  <script src="/kit.js"></script>
  <script>
  (async function () {
    var root = document.documentElement;
    function fail(m) { root.setAttribute("data-kit-test", "failed"); root.setAttribute("data-kit-test-error", m); throw new Error(m); }
    function assert(c, m) { if (!c) fail(m); }
    function tick() { return new Promise(function (r) { setTimeout(r, 30); }); }
    var dialog = document.getElementById("confirm");
    var overflow = function () { return document.documentElement.style.overflow; };
    await tick();

    document.getElementById("open").click(); await tick();
    assert(dialog.open === true, "show() must call the platform's showModal()");
    assert(document.getElementById("state").textContent === "open", "state must follow the element");
    assert(overflow() === "hidden", "the page behind a modal must not scroll");

    document.getElementById("cancel").click(); await tick();
    assert(dialog.open === false, "close(result) must close the element");
    assert(dialog.returnValue === "cancel", "the result rides the platform's returnValue, got " + dialog.returnValue);
    assert(document.getElementById("state").textContent === "closed:cancel", "result must reach the binding");
    assert(overflow() !== "hidden", "the scroll lock must be released");

    // Escape is the platform's: the component only has to keep up
    document.getElementById("open").click(); await tick();
    assert(dialog.open === true, "reopen");
    dialog.dispatchEvent(new Event("close"));
    dialog.close("");
    await tick();
    assert(document.getElementById("state").textContent.indexOf("closed") === 0, "a platform close must update state");
    assert(overflow() !== "hidden", "a platform close must release the scroll lock");

    // a click on the dialog element itself is the backdrop; a click on its content is not
    document.getElementById("open").click(); await tick();
    document.getElementById("state").click(); await tick();
    assert(dialog.open === true, "a click on the CONTENT must not close the dialog");
    dialog.click(); await tick();
    assert(dialog.open === false, "a click on the backdrop must close the dialog");

    root.setAttribute("data-kit-test", "passed");
  })().catch(function (error) { if (!root.hasAttribute("data-kit-test")) { root.setAttribute("data-kit-test", "failed"); root.setAttribute("data-kit-test-error", String(error && error.message || error)); } });
  </script>
</body></html>`
	runKernelPage(t, browser, page, scanModules(page, nil))
}

// The catalogue's own components, driven in a real browser — the standard says one browser test
// each, because a component's contract is what a visitor experiences, not what the source implies.
//
// toast: a timer that outlives the event, released with the host.
// clipboard: the platform call, the two-second confirmation, and the same release.
// sidebar: two independent axes (a desktop rail and a mobile drawer) plus its one persisted key.
func TestBrowserCatalogueComponentsHoldTheirContract(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping catalogue browser proof in short mode")
	}
	browser := findHeadlessBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	page := `<!doctype html>
<html lang="en" data-kit-app="catalogue"><head><meta charset="utf-8"><title>catalogue</title></head><body>
  <script>
  window.__copied = [];
  Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText: function (text) { window.__copied.push(text); return Promise.resolve(); } } });
  try { localStorage.removeItem("kitwork:sidebar"); } catch (e) {}
  </script>

  <div id="toast-parent">
    <div id="toast-host" data-kit-component="toast" data-kit-alias="$toast">
      <p id="toast-box" data-kit-show="visible" role="status" aria-live="polite" data-kit-text="message" hidden></p>
    </div>
  </div>
  <button id="say" type="button" data-kit-click="$toast.show('Saved')">say</button>

  <article id="copy-parent">
    <div id="copy-host" data-kit-component="clipboard">
      <pre data-kit-element="snippet">npm i kitwork</pre>
      <button id="copy" type="button" data-kit-click="copy($element.snippet.textContent)" data-kit-class="copied ? 'is-copied' : ''">copy</button>
    </div>
  </article>

  <section id="rail" data-kit-component="sidebar" data-kit-alias="$sidebar" data-kit-bind:data-state="status" data-kit-bind:data-open="drawer">
    <button id="cycle" type="button" data-kit-click="cycle()">cycle</button>
    <button id="open-drawer" type="button" data-kit-click="openDrawer()">open</button>
    <div id="scrim" hidden data-kit-show="drawer" data-kit-click="closeDrawer()"></div>
  </section>

  <script src="/kit.js"></script>
  <script>
  (async function () {
    var root = document.documentElement;
    function fail(m) { root.setAttribute("data-kit-test", "failed"); root.setAttribute("data-kit-test-error", m); throw new Error(m); }
    function assert(c, m) { if (!c) fail(m); }
    function tick(ms) { return new Promise(function (r) { setTimeout(r, ms || 40); }); }
    var byId = function (id) { return document.getElementById(id); };
    // A release is only observable if we watch for it: a timer that outlives its host writes into a
    // scope nobody can see, so count the clearTimeout calls instead of guessing.
    var cleared = 0, nativeClearTimeout = window.clearTimeout;
    window.clearTimeout = function (id) { if (id !== undefined) cleared++; return nativeClearTimeout.call(window, id); };
    await tick(60);

    // ---- toast ----
    byId("say").click(); await tick();
    assert(byId("toast-box").hidden === false && byId("toast-box").textContent === "Saved", "toast: show() paints the message");
    assert(byId("toast-box").getAttribute("aria-live") === "polite", "toast: the page keeps its own aria-live");
    window.kit.components.toast.hide(); await tick();
    assert(byId("toast-box").hidden === true, "toast: hide() closes it before the timer");
    // a pending toast whose host morph removes must not write afterwards
    byId("say").click(); await tick();
    var clearedBeforeToastRemoval = cleared;
    window.kit.morph(byId("toast-parent"), byId("toast-parent").cloneNode(false));
    assert(!byId("toast-host"), "toast: the host was removed");
    assert(cleared > clearedBeforeToastRemoval, "toast: removing the host must release the pending timer");
    await tick(60);

    // ---- clipboard ----
    byId("copy").click(); await tick(); await tick();
    assert(window.__copied[0] === "npm i kitwork", "clipboard: copy() sends what $element.snippet holds, got " + JSON.stringify(window.__copied[0]));
    assert(byId("copy").classList.contains("is-copied"), "clipboard: copied paints is-copied");
    var clearedBeforeCopyRemoval = cleared;
    window.kit.morph(byId("copy-parent"), byId("copy-parent").cloneNode(false));
    assert(!byId("copy-host"), "clipboard: the host was removed");
    assert(cleared > clearedBeforeCopyRemoval, "clipboard: removing the host must release the reset timer");

    // ---- sidebar ----
    var rail = byId("rail");
    assert(rail.getAttribute("data-state") === "expanded", "sidebar: a first visit starts expanded, got " + rail.getAttribute("data-state"));
    byId("cycle").click(); await tick();
    assert(rail.getAttribute("data-state") === "collapsed", "sidebar: cycle() collapses the rail");
    assert(localStorage.getItem("kitwork:sidebar") === "collapsed", "sidebar: the rail is persisted");
    assert(byId("scrim").hidden === true, "sidebar: the rail axis must not open the drawer");
    byId("open-drawer").click(); await tick();
    assert(byId("scrim").hidden === false, "sidebar: openDrawer() opens the mobile axis");
    // a true on a plain attribute writes it empty (the hidden rule), false removes it
    assert(rail.hasAttribute("data-open") && rail.getAttribute("data-open") === "", "sidebar: data-open must be present and empty while the drawer is open, got " + JSON.stringify(rail.getAttribute("data-open")));
    assert(rail.getAttribute("data-state") === "collapsed", "sidebar: the drawer axis must not move the rail");
    assert(localStorage.getItem("kitwork:sidebar") === "collapsed", "sidebar: the drawer is deliberately not persisted");
    byId("scrim").click(); await tick();
    assert(byId("scrim").hidden === true, "sidebar: a click on the scrim closes the drawer");
    assert(!rail.hasAttribute("data-open"), "sidebar: a false result removes the attribute");
    // ask the BLUEPRINT: a scope proxy answers 0 for a name it does not have, by design
    assert(typeof window.kit.blueprints.sidebar.open === "undefined" && typeof window.kit.blueprints.sidebar.close === "undefined",
      "sidebar: open()/close() are gone — one name per thing");
    assert(typeof window.kit.blueprints.sidebar.openDrawer === "function", "sidebar: the explicit drawer spelling stays");

    root.setAttribute("data-kit-test", "passed");
  })().catch(function (error) { if (!root.hasAttribute("data-kit-test")) { root.setAttribute("data-kit-test", "failed"); root.setAttribute("data-kit-test-error", String(error && error.message || error)); } });
  </script>
</body></html>`
	rendered := Render(page)
	for _, want := range []string{"component%3Aclipboard", "component%3Asidebar", "component%3Atoast"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("Render should ask for %s:\n%s", want, rendered)
		}
	}
	runKernelPage(t, browser, page, scanModules(page, nil))
}

// Key combinations in the event grammar: data-kit-keydown:mod+k="…". `mod` is Control on Windows
// and Linux, Command on macOS — exactly one of them — and every modifier the author did NOT name
// must be off, so mod+k and mod+shift+k can mean two different things on one page.
func TestBrowserKeyCombinationModifier(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping key combination browser proof in short mode")
	}
	browser := findHeadlessBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	page := `<!doctype html>
<html lang="en" data-kit-app="keys"><head><meta charset="utf-8"><title>keys</title></head><body>
  <section data-kit-scope="palette: 0, wide: 0, saved: 0, typed: 0">
    <b id="palette" data-kit-text="palette"></b>
    <b id="wide" data-kit-text="wide"></b>
    <b id="saved" data-kit-text="saved"></b>
    <b id="typed" data-kit-text="typed"></b>
    <input id="field"
      data-kit-keydown:mod+k:window:prevent="palette = palette + 1"
      data-kit-keydown:mod+shift+k:window="wide = wide + 1"
      data-kit-keydown:mod+enter:window="saved = saved + 1"
      data-kit-keydown:window="typed = typed + 1">
  </section>
  <script src="/kit.js"></script>
  <script>
  (async function () {
    var root = document.documentElement;
    function fail(m) { root.setAttribute("data-kit-test", "failed"); root.setAttribute("data-kit-test-error", m); throw new Error(m); }
    function assert(c, m) { if (!c) fail(m); }
    function tick() { return new Promise(function (r) { setTimeout(r, 25); }); }
    // A real key press lands on the focused element and bubbles up through document, which is
    // where the kernel listens; dispatching straight at window would reach nobody.
    function press(init) {
      var e = new KeyboardEvent("keydown", Object.assign({ key: "k", bubbles: true, cancelable: true }, init));
      document.getElementById("field").dispatchEvent(e);
      return e;
    }
    var read = function (id) { return document.getElementById(id).textContent; };
    await tick();

    var ctrl = press({ ctrlKey: true }); await tick();
    assert(read("palette") === "1", "mod+k must fire on Ctrl+K, got " + read("palette"));
    assert(ctrl.defaultPrevented === true, ":prevent must still run after the filter");
    press({ metaKey: true }); await tick();
    assert(read("palette") === "2", "mod+k must fire on Cmd+K too, got " + read("palette"));

    // every modifier not named must be off
    press({ ctrlKey: true, metaKey: true }); await tick();
    press({ ctrlKey: true, altKey: true }); await tick();
    press({}); await tick();
    press({ ctrlKey: true, repeat: true }); await tick();
    press({ ctrlKey: true, isComposing: true }); await tick();
    assert(read("palette") === "2", "Ctrl+Cmd, Alt, a bare k, a repeat and an IME composition must all be refused (" + read("palette") + ")");

    // mod+shift+k is its own shortcut, not a looser mod+k
    press({ ctrlKey: true, shiftKey: true }); await tick();
    assert(read("wide") === "1", "mod+shift+k must fire on Ctrl+Shift+K");
    assert(read("palette") === "2", "mod+k must NOT fire when shift is held — that is a different shortcut");

    // a named key other than a letter
    var enter = new KeyboardEvent("keydown", { key: "Enter", ctrlKey: true, bubbles: true, cancelable: true });
    document.getElementById("field").dispatchEvent(enter); await tick();
    assert(read("saved") === "1", "mod+enter must fire on Ctrl+Enter");

    // a handler with no key filter still hears everything, so the filter is what narrows it
    assert(Number(read("typed")) >= 8, "the unfiltered handler should have counted every press, got " + read("typed"));

    root.setAttribute("data-kit-test", "passed");
  })().catch(function (error) { if (!root.hasAttribute("data-kit-test")) { root.setAttribute("data-kit-test", "failed"); root.setAttribute("data-kit-test-error", String(error && error.message || error)); } });
  </script>
</body></html>`
	runKernelPage(t, browser, page, nil)
}
