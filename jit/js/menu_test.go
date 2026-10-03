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

// menu is the last of the three kitwork.io needs, and the only one of them that earns being a
// component: opening is grammar, closing on an outside click is grammar, and moving focus is not.

func TestMenuIsShippedWhenUsed(t *testing.T) {
	html := `<div data-kit-component="menu"><button data-kit-click="toggle()">m</button></div>`
	out := deliver(html)

	if !strings.Contains(out, `/kit/components/menu@`) {
		t.Fatalf("menu was not injected for markup that uses it:\n%s", out)
	}
	if !strings.Contains(ModulesJS([]string{"menu"}), `component("menu@v1.0.0"`) {
		t.Error("versioned registration missing")
	}
}

// CONTROL: only-what-is-used.
func TestMenuIsAbsentWhenUnused(t *testing.T) {
	out := deliver(`<dialog data-kit-component="dialog"><button data-kit-click="close()">x</button></dialog>`)

	if strings.Contains(out, `/kit/components/menu@`) {
		t.Fatal("menu shipped to a page that never mentions it")
	}
}

// Driven in a real browser because focus IS the behaviour. document.activeElement is the only
// honest way to ask where focus went, and a shim would answer whatever the test wanted.
func TestBrowserMenuOwnsFocusAndLeavesOpeningToTheGrammar(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping menu browser proof in short mode")
	}
	browser := findHeadlessBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	page := `<!doctype html><html><body>
  <div id="menu" data-kit-component="menu" data-kit-alias="$developers" data-kit-click:outside="hide()">
    <button id="trigger" type="button" data-kit-element="trigger" aria-haspopup="true"
            data-kit-click="toggle()" data-kit-bind:aria-expanded="open ? 'true' : 'false'">Developers</button>
    <div role="menu" data-kit-show="open" hidden aria-label="Developers">
      <a id="one" role="menuitem" data-kit-element="item" href="#one">Docs</a>
      <a id="two" role="menuitem" data-kit-element="item" href="#two" aria-disabled="true">Soon</a>
      <a id="three" role="menuitem" data-kit-element="item" href="#three">Blog</a>
    </div>
  </div>
  <button id="elsewhere" type="button">elsewhere</button>
  <script src="` + hydrate.RuntimePath + `"></script>
  <script>
  (async function () {
    var root = document.documentElement;
    function fail(message) { root.setAttribute("data-kit-test", "failed"); root.setAttribute("data-kit-test-error", message); throw new Error(message); }
    function assert(condition, message) { if (!condition) fail(message); }
    function wait(ms) { return new Promise(function (resolve) { setTimeout(resolve, ms); }); }
    function key(target, name) { target.dispatchEvent(new KeyboardEvent("keydown", { key: name, bubbles: true, cancelable: true })); }
    function active() { return document.activeElement ? document.activeElement.id : "(none)"; }

    await wait(50);
    var trigger = document.getElementById("trigger");
    var panel = document.querySelector('[role="menu"]');

    // Opening is the grammar: the click handler is an assignment, and aria-expanded is a binding.
    assert(panel.hidden === true, "the menu starts closed");
    assert(trigger.getAttribute("aria-expanded") === "false", "aria-expanded starts false, got " + trigger.getAttribute("aria-expanded"));
    trigger.click();
    await wait(50);
    assert(panel.hidden === false, "clicking the trigger opens the menu");
    assert(trigger.getAttribute("aria-expanded") === "true", "aria-expanded follows the state");

    // …and a click on the page closes it, because the markup said so with :outside — no code.
    document.getElementById("elsewhere").click();
    await wait(50);
    assert(panel.hidden === true, "an outside click closes the menu through the grammar");

    // From here on it is the component's half. Down from the trigger opens AND lands on the first
    // entry the person can actually choose.
    trigger.focus();
    key(trigger, "ArrowDown");
    await wait(60);
    assert(panel.hidden === false, "ArrowDown on the trigger opens the menu");
    assert(active() === "one", "ArrowDown should land on the first entry, focus is on " + active());

    // The disabled entry is stepped over, not focused: a dead end the keyboard cannot see.
    key(document.activeElement, "ArrowDown");
    await wait(30);
    assert(active() === "three", "the arrows must skip aria-disabled, focus is on " + active());

    // …and they wrap.
    key(document.activeElement, "ArrowDown");
    await wait(30);
    assert(active() === "one", "the arrows wrap to the first entry, focus is on " + active());

    key(document.activeElement, "End");
    await wait(60);
    assert(active() === "three", "End jumps to the last entry, focus is on " + active());

    // Escape closes and hands focus back, so a person who opened this from the keyboard is not
    // left focused on something that is no longer on screen.
    key(document.activeElement, "Escape");
    await wait(50);
    assert(panel.hidden === true, "Escape closes the menu");
    assert(active() === "trigger", "Escape returns focus to the trigger, focus is on " + active());

    // Up from the trigger opens at the other end.
    key(trigger, "ArrowUp");
    await wait(60);
    assert(active() === "three", "ArrowUp should land on the last entry, focus is on " + active());

    // A page can drive it by name, and hide() from outside the menu must not steal focus back.
    document.getElementById("elsewhere").focus();
    window.kit.run(window.kit.compile("$developers.hide()"));
    await wait(50);
    assert(panel.hidden === true, "a write through the handle must repaint: state is " + window.kit.components["menu"].open + " but the DOM still shows the old answer");
    assert(active() === "elsewhere", "closing while focus is elsewhere must leave focus alone, focus is on " + active());

    root.setAttribute("data-kit-test", "passed");
  })().catch(function (error) { if (!document.documentElement.hasAttribute("data-kit-test")) { document.documentElement.setAttribute("data-kit-test", "failed"); document.documentElement.setAttribute("data-kit-test-error", String(error && error.message || error)); } });
  </script>
</body></html>`

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case hydrate.RuntimePath:
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write([]byte(RuntimeJS([]string{"menu"})))
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
		t.Fatalf("menu proof failed: %s", output[i:end])
	}
	t.Fatalf("menu proof did not pass (err=%v):\n%.2000s", err, output)
}
