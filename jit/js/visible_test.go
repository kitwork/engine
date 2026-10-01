package js

import (
	"strings"
	"testing"
)

func TestVisibleIsShippedWhenUsed(t *testing.T) {
	out := Render(`<section data-kit-component="visible"></section>`)
	if !strings.Contains(out, `components=component%3Avisible`) {
		t.Fatalf("visible was not injected for markup that uses it:\n%s", out)
	}
	if !strings.Contains(ModulesJS([]string{"visible"}), `component("visible@v1.0.0"`) {
		t.Error("versioned registration missing")
	}
}

// These proofs drive the observer themselves. The headless browser they run in computes
// intersections unreliably — an observer made before load reported, one made after did not — so a
// proof that waited on the real one would pass or fail by timing. The real observer is checked by
// hand in a visible browser (kitwork.io /components/visible, 01/10).
// What scrolling does, with an observer the test drives: in view then out of view, `seen` staying
// true; a threshold read as a share of the element; the answers reaching an expression and, through
// the markup's own bindings, the host's attributes; the observer released with the host.
func TestBrowserVisibleFollowsTheObserver(t *testing.T) {
	runBrowserProof(t, "visible", []string{"visible"}, `
<section id="far" data-kit-component="visible" data-kit-bind:data-visible="visible ? 'true' : 'false'" data-kit-bind:data-seen="seen ? 'true' : 'false'">
  <output id="say" data-kit-text="(visible ? 'visible' : 'hidden') + ',' + (seen ? 'seen' : 'unseen')"></output>
</section>
<section id="half" data-kit-component="visible" data-threshold="0.5" data-margin="0px 0px -20% 0px" data-kit-bind:data-visible="visible ? 'true' : 'false'"></section>
<section id="reveal" data-kit-component="visible" data-kit-bind:data-unseen="seen === false"></section>
<section id="bare" data-kit-component="visible"><b id="bare-said" data-kit-text="seen ? 'seen' : 'unseen'"></b></section>
<script>
// Installed while the page parses, before the kernel starts on DOMContentLoaded and the components
// build their observers.
var observers = [];
window.IntersectionObserver = function (callback, options) {
  var record = { callback: callback, options: options, targets: [], disconnected: false };
  observers.push(record);
  this.observe = function (el) { record.targets.push(el); };
  this.disconnect = function () { record.disconnected = true; };
};
function observerOf(el) { return observers.filter(function (o) { return o.targets.indexOf(el) >= 0; })[0]; }
function report(el, intersecting, ratio) { observerOf(el).callback([{ target: el, isIntersecting: intersecting, intersectionRatio: ratio }]); }
(async function () {
  await wait(100);
  var far = document.getElementById("far"), half = document.getElementById("half");
  var say = function () { return document.getElementById("say").textContent; };
  assert(say() === "hidden,unseen", "before any report it is neither, got " + say());

  // The reveal binds seen === false: nothing is hidden until the observer has said "not seen".
  var reveal = document.getElementById("reveal");
  assert(!reveal.hasAttribute("data-unseen"), "before the first answer nothing is hidden — not known yet is not unseen");
  report(reveal, false, 0); await wait(40);
  assert(reveal.hasAttribute("data-unseen"), "a real no marks it unseen");
  report(reveal, true, 1); await wait(40);
  report(reveal, false, 0); await wait(40);
  assert(!reveal.hasAttribute("data-unseen"), "once seen it is never unseen again");

  report(far, true, 1); await wait(40);
  assert(say() === "visible,seen" && far.getAttribute("data-visible") === "true" && far.getAttribute("data-seen") === "true", "in view: visible and seen, in the text and on the host, got " + say());
  report(far, false, 0); await wait(40);
  assert(say() === "hidden,seen" && far.getAttribute("data-seen") === "true", "out of view: not visible, but still seen, got " + say());

  // The rules are the host's attributes, handed to the observer as written.
  var options = observerOf(half).options;
  assert(options.threshold === 0.5 && options.rootMargin === "0px 0px -20% 0px", "threshold and margin reach the observer, got " + JSON.stringify(options));
  report(half, true, 0.3); await wait(40);
  assert(half.getAttribute("data-visible") === "false", "under the threshold it is not visible");
  report(half, true, 0.6); await wait(40);
  assert(half.getAttribute("data-visible") === "true", "past the threshold it is");

  // The component writes nothing on its own: with no binding there is no attribute, only state.
  var bare = document.getElementById("bare");
  report(bare, true, 1); await wait(40);
  assert(!bare.hasAttribute("data-seen") && !bare.hasAttribute("data-visible") && document.getElementById("bare-said").textContent === "seen", "no binding, no attribute — but the state answers");

  far.remove(); await wait(60);
  assert(observerOf(far).disconnected, "the observer is released with its host");
  root.setAttribute("data-kit-test", "passed");
})().catch(function (error) { if (!root.hasAttribute("data-kit-test")) { root.setAttribute("data-kit-test", "failed"); root.setAttribute("data-kit-test-error", String(error && error.message || error)); } });
</script>`)
}

// A browser without IntersectionObserver cannot say what is on screen; content waiting to be seen
// must not stay hidden because of that.
func TestBrowserVisibleWithoutAnObserverShowsEverything(t *testing.T) {
	runBrowserProof(t, "visible fallback", []string{"visible"}, `
<section id="far" data-kit-component="visible" data-kit-bind:data-visible="visible ? 'true' : 'false'" data-kit-bind:data-seen="seen ? 'true' : 'false'"></section>
<script>
delete window.IntersectionObserver;
window.IntersectionObserver = undefined;
(async function () {
  await wait(100);
  var far = document.getElementById("far");
  assert(far.getAttribute("data-visible") === "true" && far.getAttribute("data-seen") === "true", "with no observer, everything counts as seen");
  root.setAttribute("data-kit-test", "passed");
})().catch(function (error) { if (!root.hasAttribute("data-kit-test")) { root.setAttribute("data-kit-test", "failed"); root.setAttribute("data-kit-test-error", String(error && error.message || error)); } });
</script>`)
}
