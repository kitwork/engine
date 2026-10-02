package js

import "testing"

// data-kit-event:<name> and context.emit, in a real browser:
//
//	· visible emits `seen` once, the first time it reaches the screen, and the markup hears it —
//	  on the host, and on an ancestor, since the event bubbles;
//	· a component that emits from init (inside the first paint) is heard, once the paint is over;
//	· a DOM event outside the short list (paste) is heard by name, with its modifiers;
//	· a component may not emit under a DOM event's name.
//
// The control is the second visible host, never reported in view: its handler must not run.
func TestBrowserComponentEventsReachTheMarkup(t *testing.T) {
	runBrowserProof(t, "events", []string{"visible"}, `
<div id="page" data-kit-scope="hostHeard: 0, outerHeard: 0, quietHeard: 0, ready: 0, readyAt: '', pasted: 0">
  <section id="outer" data-kit-event:seen="outerHeard++">
    <article id="near" data-kit-component="visible" data-kit-event:seen="hostHeard++"></article>
  </section>
  <article id="quiet" data-kit-component="visible" data-kit-event:seen="quietHeard++"></article>
  <div id="probe" data-kit-component="probe" data-kit-event:ready="ready = $event.detail.n; readyAt = $event.target.id"></div>
  <input id="field" data-kit-event:paste:prevent="pasted++">
  <div id="refuse" data-kit-component="refuse"></div>
</div>
<script>
var observers = [];
window.IntersectionObserver = function (callback, options) {
  var record = { callback: callback, targets: [] };
  observers.push(record);
  this.observe = function (el) { record.targets.push(el); };
  this.disconnect = function () { };
};
function report(el, intersecting) {
  observers.filter(function (o) { return o.targets.indexOf(el) >= 0; })[0].callback([{ target: el, isIntersecting: intersecting, intersectionRatio: intersecting ? 1 : 0 }]);
}
// Registered while the page parses, before the kernel boots: probe emits from init, which runs
// inside the first paint; refuse tries a DOM event's name.
kit.component("probe", { init: function (context) { context.emit("ready", { n: 7 }); } });
kit.component("refuse", {
  refused: "",
  init: function (context) { try { context.emit("click"); } catch (error) { this.refused = error.message; } }
});
(async function () {
  await wait(150);
  var page = function () { return window.kit.scopeFor(document.getElementById("page")); };

  assert(page().ready === 7 && page().readyAt === "probe", "an event emitted from init is heard after the paint, with its detail and target: " + page().ready + " " + page().readyAt);

  var near = document.getElementById("near");
  report(near, false); await wait(40);
  assert(page().hostHeard === 0, "not seen yet: no event");
  report(near, true); await wait(40);
  assert(page().hostHeard === 1 && page().outerHeard === 1, "first time on screen: heard on the host and on its ancestor, got " + page().hostHeard + "/" + page().outerHeard);
  report(near, false); await wait(40);
  report(near, true); await wait(40);
  assert(page().hostHeard === 1, "seen is announced once, got " + page().hostHeard);
  assert(page().quietHeard === 0, "the control was never on screen and heard nothing, got " + page().quietHeard);

  var field = document.getElementById("field");
  var paste = new Event("paste", { bubbles: true, cancelable: true });
  field.dispatchEvent(paste); await wait(20);
  assert(page().pasted === 1 && paste.defaultPrevented, "a DOM event by name, with :prevent: " + page().pasted + " " + paste.defaultPrevented);

  var refused = window.kit.scopeFor(document.getElementById("refuse")).refused;
  assert(refused.indexOf("DOM event") >= 0, "emit(\"click\") must be refused, got " + JSON.stringify(refused));

  root.setAttribute("data-kit-test", "passed");
})().catch(function (error) { if (!root.hasAttribute("data-kit-test")) { root.setAttribute("data-kit-test", "failed"); root.setAttribute("data-kit-test-error", String(error && error.message || error)); } });
</script>`)
}
