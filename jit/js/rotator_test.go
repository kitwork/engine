package js

import (
	"strings"
	"testing"
)

func TestRotatorIsShippedWhenUsed(t *testing.T) {
	out := deliver(`<div data-kit-component="rotator"><span data-kit-element="item">a</span></div>`)
	if !strings.Contains(out, `/_kitwork/components/rotator.`) {
		t.Fatalf("rotator was not injected for markup that uses it:\n%s", out)
	}
	if src := ModulesJS([]string{"rotator"}); !strings.Contains(src, `component("rotator", `) || strings.Contains(src, `component("rotator@`) {
		t.Error("the file must register the bare name, and only it")
	}
}

// A clock only a browser can show: it advances on its own, holds still while a pointer is over it,
// resumes when the pointer leaves, and stops for good on pause — with `running` telling the truth
// the whole time, because that is what the play/pause control reads.
func TestBrowserRotatorKeepsTimeAndHoldsStill(t *testing.T) {
	runBrowserProof(t, "rotator", []string{"rotator"}, `
<div id="host" data-kit-component="rotator" data-kit-alias="$words" data-interval="200">
  <span id="word" data-kit-text="['sites', 'apps', 'jobs'][active]"></span>
  <span data-kit-element="item"></span><span data-kit-element="item"></span><span data-kit-element="item"></span>
  <button id="toggle" type="button" data-kit-click="toggle()" data-kit-text="running ? 'Pause' : 'Play'"></button>
</div>
<div id="single" data-kit-component="rotator" data-interval="200"><span data-kit-element="item"></span><output id="lone" data-kit-text="running ? 'running' : 'still'"></output></div>
<script>
(async function () {
  var host = document.getElementById("host"), word = function () { return document.getElementById("word").textContent; };
  await wait(80);
  assert(word() === "sites" && document.getElementById("toggle").textContent === "Pause", "it starts on the first item, running");
  await wait(260);
  assert(word() === "apps", "it advances on its own after the interval, shows " + word());

  host.dispatchEvent(new PointerEvent("pointerenter")); await wait(20);
  assert(document.getElementById("toggle").textContent === "Play", "a pointer over it holds it, and running says so");
  var held = word(); await wait(450);
  assert(word() === held, "held still while the pointer is over it, moved to " + word());
  host.dispatchEvent(new PointerEvent("pointerleave")); await wait(260);
  assert(word() !== held, "it resumes when the pointer leaves");

  // go() wraps, and a choice restarts the dwell.
  window.kit.run(window.kit.compile("$words.go(5)")); await wait(20);
  assert(word() === "jobs", "go(5) of three lands on the third, shows " + word());

  document.getElementById("toggle").click(); await wait(20);
  held = word(); await wait(450);
  assert(word() === held && document.getElementById("toggle").textContent === "Play", "pause stops it for good");

  // CONTROL: one item has nothing to rotate — it never runs.
  assert(document.getElementById("lone").textContent === "still", "a single item never runs");

  root.setAttribute("data-kit-test", "passed");
})().catch(function (error) { var root = document.documentElement; if (!root.hasAttribute("data-kit-test")) { root.setAttribute("data-kit-test", "failed"); root.setAttribute("data-kit-test-error", String(error && error.message || error)); } });
</script>`)
}
