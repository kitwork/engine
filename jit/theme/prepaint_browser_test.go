package theme

import (
	"bytes"
	"context"
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

// The pre-paint as a browser runs it. Each page pins the slot with today's marker, so Render fills it
// in place, after one script that sets up what the visitor has stored. Two first paints are proven on
// a real load:
//
//   - a visitor who chose dark before the light/dark service became kit.appearance (only the OLD
//     "theme" key holds it) on a site with no default: the first paint is dark;
//   - a site whose default is dark (router.appearance("dark") → <html data-kitwork-appearance="dark">)
//     and a visitor who stored nothing: the first paint is dark, whatever the OS prefers.
//
// Then the same shipped script (read back from the page, run again as a fresh inline script) is run
// once per state — what is stored, the site's default, the OS (matchMedia stands in for it) — and
// each run is checked on all three things it writes: the dark class, data-theme and color-scheme.
func TestBrowserPrepaintReadsAppearanceThenTheOldThemeKey(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the pre-paint browser proof in short mode")
	}
	browser := findHeadlessBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	for _, first := range []struct {
		name, html, store string
	}{
		{"old key, no site default", `<html data-want-first="dark/dark/dark">`, `localStorage.setItem("theme", "dark");`},
		{"nothing stored, site default dark", `<html data-kitwork-appearance="dark" data-want-first="dark/dark/dark">`, ``},
	} {
		page := Render(`<!doctype html>` + first.html + `<head><meta charset="utf-8">` +
			`<script>window.matchMedia = function (query) { return { matches: false, media: query }; };` + // a light device
			`try { localStorage.clear(); ` + first.store + ` } catch (e) {}</script>` +
			`<script data-kitwork-generated="appearance"></script>` +
			`<title>pre-paint</title></head><body>` + prepaintProof + `</body></html>`)
		if !injected(page) {
			t.Fatalf("%s: Render did not fill today's marker with the pre-paint:\n%s", first.name, page)
		}
		output, err := runPrepaintPage(t, browser, page, "")
		if bytes.Contains(output, []byte(`data-kit-test="passed"`)) {
			continue
		}
		if i := bytes.Index(output, []byte("data-kit-test-error=")); i >= 0 {
			end := i + 400
			if end > len(output) {
				end = len(output)
			}
			t.Errorf("%s: pre-paint proof failed: %s", first.name, output[i:end])
			continue
		}
		t.Errorf("%s: pre-paint proof did not pass (err=%v):\n%.2000s", first.name, err, output)
	}
}

// The hash the engine adds to a site's policy, proven where it counts: in a browser that enforces the
// policy. The page is a dark-default site's (<html data-kitwork-appearance="dark">) with the engine's
// pre-paint; nothing is stored, so only the pre-paint can make the first frame dark. Served under
// `script-src 'self'` plus what AllowInCSP adds, the first frame is dark — the browser accepted the
// hash as the hash of the script it was given. Control: the same page under `script-src 'self'` alone
// is refused its pre-paint, and the first frame is light, so the dark above is the hash's doing.
// What the page painted is read by a same-origin script (an inline one would be refused too).
func TestBrowserPrepaintRunsUnderTheHashItsPolicyCarries(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the pre-paint CSP browser proof in short mode")
	}
	browser := findHeadlessBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	// /light-device.js makes the device a light one before the pre-paint runs (a same-origin file, so
	// the policy allows it; the marker keeps the pre-paint after it): only the site's default can paint
	// the first frame dark, not a dark desktop the browser happens to run on.
	page := Force(`<!doctype html><html data-kitwork-appearance="dark"><head><meta charset="utf-8">` +
		`<script src="/light-device.js"></script><script data-kitwork-generated="appearance"></script><title>csp</title></head>` +
		`<body><p>pre-paint under a policy</p><script src="/painted.js"></script></body></html>`)
	// (Only the marker is checked here, not the exact body: whether the script the page carries is the
	// one the hash describes is what the browser below decides.)
	if at := strings.Index(page, `<script data-kitwork-generated="appearance">(function(){`); at < 0 || strings.Index(page, "/light-device.js") > at {
		t.Fatalf("the pre-paint must be on the page, after the light-device stub:\n%s", page)
	}
	const strict = "default-src 'self'; script-src 'self'"
	allowed := AllowInCSP(strict, page)
	if allowed == strict || !strings.Contains(allowed, PrepaintHash()) {
		t.Fatalf("AllowInCSP must add the hash for a page carrying the pre-paint, got %q", allowed)
	}
	for _, run := range []struct {
		policy, want, why string
	}{
		{allowed, `data-painted="dark/dark"`, "the policy carrying the engine's hash must let the pre-paint run"},
		{strict, `data-painted="light/null"`, "control: without the hash the browser refuses the pre-paint"},
	} {
		output, err := runPrepaintPage(t, browser, page, run.policy)
		if !bytes.Contains(output, []byte(run.want)) {
			got := "(no data-painted)"
			if i := bytes.Index(output, []byte(`data-painted="`)); i >= 0 {
				end := i + 40
				if end > len(output) {
					end = len(output)
				}
				got = string(output[i:end])
			}
			t.Errorf("%s: under %q want %s, got %s (err=%v)", run.why, run.policy, run.want, got, err)
		}
	}
}

// paintedProbe records what the first frame was painted as, from a file the policy allows ('self').
const paintedProbe = `var r = document.documentElement;
r.setAttribute("data-painted", (r.classList.contains("dark") ? "dark" : "light") + "/" + r.getAttribute("data-theme"));`

// runPrepaintPage serves page at / (under policy as its Content-Security-Policy when not empty),
// paintedProbe at /painted.js and a light device at /light-device.js, and returns the DOM the
// browser ends with.
func runPrepaintPage(t *testing.T, browser, page, policy string) ([]byte, error) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/":
			if policy != "" {
				response.Header().Set("Content-Security-Policy", policy)
			}
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(page))
		case "/painted.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write([]byte(paintedProbe))
		case "/light-device.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write([]byte(`window.matchMedia = function (query) { return { matches: false, media: query }; };`))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	args := []string{"--headless=new", "--disable-gpu", "--no-sandbox", "--disable-dev-shm-usage", "--disable-background-networking",
		"--disable-default-apps", "--disable-extensions", "--disable-sync", "--metrics-recording-only", "--no-first-run",
		"--user-data-dir=" + filepath.Join(t.TempDir(), "p"), "--virtual-time-budget=4000", "--dump-dom", server.URL + "/"}
	if runtime.GOOS == "windows" {
		args = append([]string{"--disable-background-mode", "--disable-breakpad", "--disable-crashpad-for-testing"}, args...)
	}
	return exec.CommandContext(ctx, browser, args...).CombinedOutput()
}

// prepaintProof runs in <body>, after the pre-paint has already run once in <head>.
const prepaintProof = `<script>
(function () {
  var root = document.documentElement;
  function fail(message) { root.setAttribute("data-kit-test", "failed"); root.setAttribute("data-kit-test-error", message); throw new Error(message); }
  function shown() {
    return (root.classList.contains("dark") ? "dark" : "light") + "/" + root.getAttribute("data-theme") + "/" + root.style.colorScheme;
  }

  // The first paint, as the page set it up (see the Go side for the two setups).
  var wantFirst = root.getAttribute("data-want-first");
  if (shown() !== wantFirst) fail("first paint (stored " + JSON.stringify(localStorage) + ", site default " + root.getAttribute("data-kitwork-appearance") + "): got " + shown() + ", want " + wantFirst);

  var shipped = document.querySelector('script[data-kitwork-generated="appearance"]');
  if (!shipped) fail("the pre-paint is not on the page under today's marker");
  var realMatchMedia = window.matchMedia;
  function run(store, siteDefault, systemDark, want) {
    // Start each run from the opposite of what it must show, so a run that did nothing cannot pass.
    root.classList.toggle("dark", want.indexOf("dark") !== 0);
    root.setAttribute("data-theme", "none");
    root.style.colorScheme = "";
    if (siteDefault == null) root.removeAttribute("data-kitwork-appearance");
    else root.setAttribute("data-kitwork-appearance", siteDefault);
    localStorage.clear();
    for (var key in store) localStorage.setItem(key, store[key]);
    window.matchMedia = function (query) {
      return { matches: !!systemDark && query === "(prefers-color-scheme: dark)", media: query };
    };
    var again = document.createElement("script");
    again.textContent = shipped.textContent; // the bytes the server sent, run as the browser runs them
    document.head.appendChild(again);
    again.remove();
    window.matchMedia = realMatchMedia;
    return shown();
  }
  var cases = [
    // [what is stored, the site's default (null: none), the OS is dark, what must be shown, why]
    [{ theme: "dark" }, null, false, "dark/dark/dark", "the old key alone is still honoured"],
    [{ theme: "DARK" }, null, false, "dark/dark/dark", "the old key is read case-insensitively, as before"],
    [{ appearance: "dark" }, null, false, "dark/dark/dark", "the new key alone"],
    [{ appearance: "light", theme: "dark" }, null, true, "light/light/light", "the new key wins over the old"],
    [{ appearance: "dark", theme: "light" }, null, false, "dark/dark/dark", "the new key wins over the old (the other way)"],
    [{ appearance: "system", theme: "dark" }, null, false, "light/light/light", "system under the new key wins, and follows a light OS"],
    [{ appearance: "system", theme: "light" }, null, true, "dark/dark/dark", "system under the new key follows a dark OS"],
    [{ appearance: "sepia", theme: "dark" }, null, false, "dark/dark/dark", "a word that is not a choice under the new key falls back to the old"],
    [{}, null, true, "dark/dark/dark", "nothing stored, no site default: a dark OS decides"],
    [{}, null, false, "light/light/light", "nothing stored, no site default: a light OS decides"],

    // The site's default (router.appearance) applies only when nothing is stored.
    [{}, "dark", false, "dark/dark/dark", "nothing stored: the site's dark default beats a light OS"],
    [{}, "light", true, "light/light/light", "nothing stored: the site's light default beats a dark OS"],
    [{}, "system", true, "dark/dark/dark", "a site default of system follows the OS"],
    [{ appearance: "sepia" }, "dark", false, "dark/dark/dark", "a word that is not a choice is no choice: the site's default applies"],
    [{}, "sepia", true, "dark/dark/dark", "a default that is not a mode is ignored: the OS decides"],
    [{ appearance: "light" }, "dark", false, "light/light/light", "a stored choice beats the site's default"],
    [{ theme: "light" }, "dark", false, "light/light/light", "a choice under the old key beats the site's default too"],
    [{ appearance: "system" }, "dark", false, "light/light/light", "a stored system beats a dark default, and follows a light OS"],
    [{ appearance: "system" }, "light", true, "dark/dark/dark", "a stored system beats a light default, and follows a dark OS"]
  ];
  for (var i = 0; i < cases.length; i++) {
    var c = cases[i];
    var got = run(c[0], c[1], c[2], c[3]);
    if (got !== c[3]) fail(c[4] + ": stored " + JSON.stringify(c[0]) + ", site default " + c[1] + ", OS dark " + c[2] + " → " + got + ", want " + c[3]);
  }

  // A browser that refuses storage (a sandboxed frame, a blocked cookie setting) still gets the site's
  // default: the failure is caught before the default is read.
  var realStorage = Object.getOwnPropertyDescriptor(window, "localStorage");
  Object.defineProperty(window, "localStorage", { configurable: true, get: function () { throw new Error("denied"); } });
  root.classList.remove("dark");
  root.setAttribute("data-kitwork-appearance", "dark");
  window.matchMedia = function (query) { return { matches: false, media: query }; }; // a light device
  var blocked = document.createElement("script");
  blocked.textContent = shipped.textContent;
  document.head.appendChild(blocked);
  blocked.remove();
  window.matchMedia = realMatchMedia;
  if (realStorage) Object.defineProperty(window, "localStorage", realStorage); else delete window.localStorage;
  if (shown() !== "dark/dark/dark") fail("storage refused: the site's dark default must still apply, got " + shown());

  root.setAttribute("data-kit-test", "passed");
})();
</script>`

func findHeadlessBrowser() string {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("KITWORK_BROWSER_TESTS"))) {
	case "0", "false", "off", "no":
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
