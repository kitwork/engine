package js

import "testing"

// A data-kitwork-event pair under a component's event name and context.emit, in a real browser
// (data-kit-event:<name> until 26/10/2026):
//
//	· a component's event reaches the markup on its host — and only there: a component's event is its
//	  host's, so an ancestor that writes the same name does not answer, though the event bubbles;
//	· a component that emits from init (inside the first paint) is heard, once the paint is over;
//	· a DOM event outside the short list (paste) is heard by name, with its modifiers;
//	· a component may not emit under a DOM event's name.
//
// The control is the second bell, never rung: its handler must not run. (The probe's event is started:
// ready, in a data-kitwork-event, is the kernel's — once after the element's first paint. Each pair runs
// one expression — a ';' ends the pair — so the probe's handler records its target and its detail in
// one write.)
func TestBrowserComponentEventsReachTheMarkup(t *testing.T) {
	runBrowserProof(t, "events", nil, `
<div id="page" data-kitwork-scope="hostHeard: 0; outerHeard: 0; quietHeard: 0; started: ''; pasted: 0">
  <section id="outer" data-kitwork-event="ring: outerHeard++">
    <article id="near" data-kitwork-component="bell" data-kitwork-event="ring: hostHeard++"></article>
  </section>
  <article id="quiet" data-kitwork-component="bell" data-kitwork-event="ring: quietHeard++"></article>
  <div id="probe" data-kitwork-component="probe" data-kitwork-event="started: started = $event.target.id + ':' + $event.detail.n"></div>
  <input id="field" data-kitwork-event="paste.prevent: pasted++">
  <div id="refuse" data-kitwork-component="refuse"></div>
</div>
<script>
// Registered while the page parses, before the kernel boots: bell emits when rung, probe emits from
// init, which runs inside the first paint; refuse tries a DOM event's name.
kit.component("bell", { init: function (context) { this.ring = function () { context.emit("ring"); }; } });
kit.component("probe", { init: function (context) { context.emit("started", { n: 7 }); } });
kit.component("refuse", {
  refused: "",
  init: function (context) { try { context.emit("click"); } catch (error) { this.refused = error.message; } }
});
(async function () {
  await wait(150);
  var page = function () { return window.kit.scopeFor(document.getElementById("page")); };

  assert(page().started === "probe:7", "an event emitted from init is heard after the paint, with its detail and target: " + page().started);

  var near = window.kit.scopeFor(document.getElementById("near"));
  assert(page().hostHeard === 0, "not rung yet: no event");
  near.ring(); await wait(40);
  assert(page().hostHeard === 1 && page().outerHeard === 0, "rung: heard on the host, and not on its ancestor, got " + page().hostHeard + "/" + page().outerHeard);
  near.ring(); await wait(40);
  assert(page().hostHeard === 2, "each emit is heard, got " + page().hostHeard);
  assert(page().quietHeard === 0, "the control was never rung and heard nothing, got " + page().quietHeard);

  var field = document.getElementById("field");
  var paste = new Event("paste", { bubbles: true, cancelable: true });
  field.dispatchEvent(paste); await wait(20);
  assert(page().pasted === 1 && paste.defaultPrevented, "a DOM event by name, with .prevent: " + page().pasted + " " + paste.defaultPrevented);

  var refused = window.kit.scopeFor(document.getElementById("refuse")).refused;
  assert(refused.indexOf("DOM event") >= 0, "emit(\"click\") must be refused, got " + JSON.stringify(refused));

  root.setAttribute("data-kit-test", "passed");
})().catch(function (error) { var root = document.documentElement; if (!root.hasAttribute("data-kit-test")) { root.setAttribute("data-kit-test", "failed"); root.setAttribute("data-kit-test-error", String(error && error.message || error)); } });
</script>`)
}
