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
)

// data-kit-intersect is delivered like a component: a page that writes it gets the kernel and one
// directive file; a page that only prints it in an example gets neither the file nor, for that
// reason alone, the kernel.
func TestIntersectIsDeliveredWhereWritten(t *testing.T) {
	roles, paths := deliveredFiles(t, Deliver(`<html><head></head><body><section data-kit-intersect:once="n++"></section></body></html>`, nil, Options{}))
	if strings.Join(roles, ",") != "runtime,directive" || !strings.HasPrefix(paths[1], "/_kitwork/directives/intersect.") {
		t.Fatalf("a page with data-kit-intersect: roles %v paths %v", roles, paths)
	}
	if asset, exact := AssetAt(paths[1], nil); asset == nil || !exact {
		t.Errorf("%s does not round-trip through AssetAt", paths[1])
	}
	if asset, _ := AssetAt("/_kitwork/directives/nope."+strings.Repeat("0", hashLength)+".js", nil); asset != nil {
		t.Error("an unknown directive must not resolve")
	}
	if out := Deliver(`<html><head></head><body><pre><code>data-kit-intersect="n++"</code></pre></body></html>`, nil, Options{}); strings.Contains(out, "<script") {
		t.Errorf("CONTROL: a page that only prints the directive gets no script:\n%s", out)
	}
}

// The directive in a real browser, with an IntersectionObserver the test drives: :once runs once;
// a plain one runs each time the element enters and :leave each time it is off screen, the first
// report included; :half waits for half, :margin reaches the observer; a parent never answers its
// child's report; the safe reveal hides only what is known unseen; an element a later paint adds is
// watched too. #quiet is never reported: the control.
func TestBrowserIntersectRunsOnTheScreenEdge(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping intersect browser proof in short mode")
	}
	browser := findHeadlessBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	page := Deliver(`<!doctype html><html><head><script>
// Installed before the deferred files run, so the directive's observers are these.
window.__observers = [];
window.IntersectionObserver = function (callback, options) {
  var record = { callback: callback, options: options || {}, targets: [], live: true };
  window.__observers.push(record);
  this.observe = function (el) { record.targets.push(el); };
  this.disconnect = function () { record.live = false; };
};
</script></head><body>
<div id="page" data-kit-scope="onceRuns: 0, enters: 0, leaves: 0, halfRuns: 0, outerRuns: 0, innerRuns: 0, added: 0, quietRuns: 0">
  <section id="once" data-kit-intersect:once="onceRuns++"></section>
  <section id="each" data-kit-intersect="enters++" data-kit-intersect:leave="leaves++"></section>
  <section id="half" data-kit-intersect:half="halfRuns++"></section>
  <section id="margin" data-kit-intersect:margin(-20%)="quietRuns = quietRuns"></section>
  <section id="outer" data-kit-intersect="outerRuns++"><div id="inner" data-kit-intersect="innerRuns++"></div></section>
  <section id="quiet" data-kit-intersect="quietRuns++"></section>
  <div id="slot"></div>
</div>
<section id="reveal" data-kit-scope="seen: null" data-kit-intersect:once="seen = true" data-kit-intersect:leave="seen = seen || false" data-kit-bind:data-unseen="seen === false"></section>
<script>
document.addEventListener("DOMContentLoaded", function () { (async function () {
`+proofScriptHead+`
  function report(el, inside, ratio) {
    window.__observers.filter(function (o) { return o.live && o.targets.indexOf(el) >= 0; }).forEach(function (o) {
      o.callback([{ target: el, isIntersecting: inside, intersectionRatio: ratio === undefined ? (inside ? 1 : 0) : ratio }]);
    });
  }
  function byId(id) { return document.getElementById(id); }
  var page = function () { return window.kit.scopeFor(byId("page")); };
  await wait(150);

  var reveal = byId("reveal");
  assert(!reveal.hasAttribute("data-unseen"), "before the first report nothing is hidden");
  report(reveal, false); await wait(30);
  assert(reveal.hasAttribute("data-unseen"), "a real no hides the reveal");
  report(reveal, true); await wait(30);
  assert(!reveal.hasAttribute("data-unseen"), "seen shows it");
  report(reveal, false); await wait(30);
  assert(!reveal.hasAttribute("data-unseen"), "once seen it stays shown");

  report(byId("once"), true); await wait(30);
  report(byId("once"), false); await wait(30);
  report(byId("once"), true); await wait(30);
  assert(page().onceRuns === 1, ":once runs once, got " + page().onceRuns);

  var each = byId("each");
  report(each, false); await wait(30);
  assert(page().leaves === 1 && page().enters === 0, "the first report, off screen, is a leave: " + page().enters + "/" + page().leaves);
  report(each, true); await wait(30);
  report(each, false); await wait(30);
  report(each, true); await wait(30);
  assert(page().enters === 2 && page().leaves === 2, "each enter and each leave runs: " + page().enters + "/" + page().leaves);

  var half = byId("half");
  var halfOptions = window.__observers.filter(function (o) { return o.targets.indexOf(half) >= 0; })[0].options;
  assert(halfOptions.threshold === 0.5, ":half asks the observer for 0.5, got " + JSON.stringify(halfOptions));
  report(half, true, 0.3); await wait(30);
  assert(page().halfRuns === 0, "under half is not enough");
  report(half, true, 0.6); await wait(30);
  assert(page().halfRuns === 1, "past half runs it");

  var marginOptions = window.__observers.filter(function (o) { return o.targets.indexOf(byId("margin")) >= 0; })[0].options;
  assert(marginOptions.rootMargin === "-20%", ":margin(-20%) reaches the observer, got " + JSON.stringify(marginOptions));

  report(byId("inner"), true); await wait(30);
  assert(page().innerRuns === 1 && page().outerRuns === 0, "a parent never answers its child's report: " + page().innerRuns + "/" + page().outerRuns);

  var late = document.createElement("section");
  late.id = "late";
  late.setAttribute("data-kit-intersect", "added++");
  byId("slot").appendChild(late);
  window.kit.render(); await wait(80);
  report(late, true); await wait(30);
  assert(page().added === 1, "an element added after load is watched, got " + page().added);

  assert(page().quietRuns === 0, "the control was never reported, got " + page().quietRuns);
  root.setAttribute("data-kit-test", "passed");
})().catch(function (error) { var root = document.documentElement; if (!root.hasAttribute("data-kit-test")) { root.setAttribute("data-kit-test", "failed"); root.setAttribute("data-kit-test-error", String(error && error.message || error)); } }); });
</script></body></html>`, nil, Options{})

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if asset, _ := AssetAt(request.URL.Path, nil); asset != nil {
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(asset.Body)
			return
		}
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = response.Write([]byte(page))
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	args := []string{"--headless=new", "--disable-gpu", "--no-sandbox", "--disable-dev-shm-usage", "--disable-background-networking",
		"--disable-default-apps", "--disable-extensions", "--disable-sync", "--metrics-recording-only", "--no-first-run",
		"--run-all-compositor-stages-before-draw", "--user-data-dir=" + filepath.Join(t.TempDir(), "p"), "--virtual-time-budget=8000", "--dump-dom", server.URL + "/"}
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
		t.Fatalf("intersect proof failed: %s", output[i:end])
	}
	t.Fatalf("intersect proof did not pass (err=%v):\n%.1500s", err, output)
}
