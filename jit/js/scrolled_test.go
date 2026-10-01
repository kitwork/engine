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

// scrolled is the first of the three components kitwork.io needs before it can leave KitJS. It
// answers one question the grammar cannot ask: how far has the page moved? Scroll position is not
// state a page declares — it is a fact the browser owns and reports only by event.

func TestScrolledIsShippedWhenUsed(t *testing.T) {
	html := `<nav data-kit-component="scrolled" data-scrolled="false" class="data-[scrolled=true]:bg-paper"></nav>`
	out := Render(html)

	if !strings.Contains(out, `components=component%3Ascrolled`) {
		t.Fatalf("scrolled was not injected for markup that uses it:\n%s", out)
	}
	// Without the versioned alias, data-kit-component="scrolled@v1.0.0" resolves to nothing.
	if !strings.Contains(ModulesJS([]string{"scrolled"}), `component("scrolled@v1.0.0"`) {
		t.Error("versioned registration missing")
	}
}

// CONTROL: only-what-is-used is the point. Without this, the test above would also pass on a build
// that inlines every component on every page.
func TestScrolledIsAbsentWhenUnused(t *testing.T) {
	out := Render(`<dialog data-kit-component="dialog"><button data-kit-click="close()">x</button></dialog>`)

	if strings.Contains(out, `components=component%3Ascrolled`) {
		t.Fatal("scrolled shipped to a page that never mentions it")
	}
	if !strings.Contains(out, `components=component%3Adialog`) {
		t.Fatal("the component that IS used should still ship")
	}
}

// Driven in a real browser because the behaviour IS the scroll: a shim that reports whatever the
// test wants would prove only that the code reads a variable. Here the page is really 3000px tall,
// the viewport really moves, and the component reads what the browser reports.
func TestBrowserScrolledAnswersTheQuestionMarkupCannotAsk(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping scrolled browser proof in short mode")
	}
	browser := findHeadlessBrowser()
	if browser == "" {
		t.Skip("no headless browser available")
	}

	page := `<!doctype html><html><body style="margin:0">
  <nav id="bar" data-kit-component="scrolled" data-scrolled="false" data-kit-bind:data-scrolled="scrolled ? 'true' : 'false'" style="position:fixed;top:0;height:40px"></nav>
  <nav id="late" data-kit-component="scrolled" data-offset="500" data-scrolled="false" data-kit-bind:data-scrolled="scrolled ? 'true' : 'false'" style="position:fixed;top:40px;height:40px"></nav>
  <nav id="plain" data-kit-component="scrolled" style="position:fixed;top:80px;height:40px"><b id="said" data-kit-text="scrolled ? 'yes' : 'no'"></b></nav>
  <div style="height:3000px"></div>
  <script src="` + hydrate.RuntimePath + `"></script>
  <script>
  (async function () {
    var root = document.documentElement;
    function fail(message) { root.setAttribute("data-kit-test", "failed"); root.setAttribute("data-kit-test-error", message); throw new Error(message); }
    function assert(condition, message) { if (!condition) fail(message); }
    // setTimeout, not requestAnimationFrame: under --virtual-time-budget the clock runs ahead of
    // the compositor, so a frame callback may never arrive while a timer always does.
    function wait(ms) { return new Promise(function (resolve) { setTimeout(resolve, ms); }); }
    // The page really scrolls here — window.scrollY below is the browser's own number, and the
    // component reads it. What headless does NOT deliver under --virtual-time-budget is the scroll
    // EVENT, which the compositor raises; the clock runs ahead of it. So the notification is raised
    // by hand and the measurement stays real: scrollTo moves the viewport, dispatch only tells the
    // listener to look.
    async function scrollTo(y) { window.scrollTo(0, y); window.dispatchEvent(new Event("scroll")); await wait(30); }

    await wait(50);
    assert(window.kit && window.kit.blueprints && window.kit.blueprints["scrolled"], "the scrolled blueprint registered through kit.component");

    var bar = document.getElementById("bar");
    var late = document.getElementById("late");

    // At rest the answer is false, on the host through the markup's own binding.
    assert(bar.getAttribute("data-scrolled") === "false", "at rest the host should read false, got " + bar.getAttribute("data-scrolled"));

    // Past the default 24px threshold.
    await scrollTo(400);
    assert(bar.getAttribute("data-scrolled") === "true", "after scrolling 400px the host should read true, got " + bar.getAttribute("data-scrolled") + " (scrollY=" + window.scrollY + " scrollTop=" + document.documentElement.scrollTop + " height=" + document.documentElement.scrollHeight + " inner=" + window.innerHeight + ")");

    // The SAME scroll, a different threshold: data-offset="500" has not been passed yet. This is
    // the control — without it, a component that simply wrote "true" on any scroll would pass.
    assert(late.getAttribute("data-scrolled") === "false", "a host with data-offset=500 must still read false at 400px, got " + late.getAttribute("data-scrolled"));

    await scrollTo(800);
    assert(late.getAttribute("data-scrolled") === "true", "past its own threshold the second host should read true, got " + late.getAttribute("data-scrolled"));

    // And back: the answer is a fact, not a latch.
    await scrollTo(0);
    assert(bar.getAttribute("data-scrolled") === "false", "returning to the top should read false again, got " + bar.getAttribute("data-scrolled"));

    // The component writes nothing on its own: a host that binds nothing never gets the attribute,
    // while its state still answers.
    await scrollTo(400);
    var plain = document.getElementById("plain");
    assert(!plain.hasAttribute("data-scrolled") && document.getElementById("said").textContent === "yes", "no binding, no attribute — but the state answers; got attr=" + plain.getAttribute("data-scrolled") + " said=" + document.getElementById("said").textContent);

    root.setAttribute("data-kit-test", "passed");
  })().catch(function (error) { if (!document.documentElement.hasAttribute("data-kit-test")) { document.documentElement.setAttribute("data-kit-test", "failed"); document.documentElement.setAttribute("data-kit-test-error", String(error && error.message || error)); } });
  </script>
</body></html>`

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case hydrate.RuntimePath:
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write([]byte(RuntimeJS([]string{"scrolled"})))
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
		"--window-size=800,600",
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
		t.Fatalf("scrolled proof failed: %s", output[i:end])
	}
	t.Fatalf("scrolled proof did not pass (err=%v):\n%.2000s", err, output)
}
