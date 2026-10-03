package js

import "testing"

// data-kit-event:<name> and context.emit, in a real browser:
//
//	· a component's event reaches the markup — on the host, and on an ancestor, since it bubbles;
//	· a component that emits from init (inside the first paint) is heard, once the paint is over;
//	· a DOM event outside the short list (paste) is heard by name, with its modifiers;
//	· a component may not emit under a DOM event's name.
//
// The control is the second bell, never rung: its handler must not run.
func TestBrowserComponentEventsReachTheMarkup(t *testing.T) {
	runBrowserProof(t, "events", nil, `
<div id="page" data-kit-scope="hostHeard: 0, outerHeard: 0, quietHeard: 0, ready: 0, readyAt: '', pasted: 0">
  <section id="outer" data-kit-event:ring="outerHeard++">
    <article id="near" data-kit-component="bell" data-kit-event:ring="hostHeard++"></article>
  </section>
  <article id="quiet" data-kit-component="bell" data-kit-event:ring="quietHeard++"></article>
  <div id="probe" data-kit-component="probe" data-kit-event:ready="ready = $event.detail.n; readyAt = $event.target.id"></div>
  <input id="field" data-kit-event:paste:prevent="pasted++">
  <div id="refuse" data-kit-component="refuse"></div>
</div>
<script>
// Registered while the page parses, before the kernel boots: bell emits when rung, probe emits from
// init, which runs inside the first paint; refuse tries a DOM event's name.
kit.component("bell", { init: function (context) { this.ring = function () { context.emit("ring"); }; } });
kit.component("probe", { init: function (context) { context.emit("ready", { n: 7 }); } });
kit.component("refuse", {
  refused: "",
  init: function (context) { try { context.emit("click"); } catch (error) { this.refused = error.message; } }
});
(async function () {
  await wait(150);
  var page = function () { return window.kit.scopeFor(document.getElementById("page")); };

  assert(page().ready === 7 && page().readyAt === "probe", "an event emitted from init is heard after the paint, with its detail and target: " + page().ready + " " + page().readyAt);

  var near = window.kit.scopeFor(document.getElementById("near"));
  assert(page().hostHeard === 0, "not rung yet: no event");
  near.ring(); await wait(40);
  assert(page().hostHeard === 1 && page().outerHeard === 1, "rung: heard on the host and on its ancestor, got " + page().hostHeard + "/" + page().outerHeard);
  near.ring(); await wait(40);
  assert(page().hostHeard === 2, "each emit is heard, got " + page().hostHeard);
  assert(page().quietHeard === 0, "the control was never rung and heard nothing, got " + page().quietHeard);

  var field = document.getElementById("field");
  var paste = new Event("paste", { bubbles: true, cancelable: true });
  field.dispatchEvent(paste); await wait(20);
  assert(page().pasted === 1 && paste.defaultPrevented, "a DOM event by name, with :prevent: " + page().pasted + " " + paste.defaultPrevented);

  var refused = window.kit.scopeFor(document.getElementById("refuse")).refused;
  assert(refused.indexOf("DOM event") >= 0, "emit(\"click\") must be refused, got " + JSON.stringify(refused));

  root.setAttribute("data-kit-test", "passed");
})().catch(function (error) { var root = document.documentElement; if (!root.hasAttribute("data-kit-test")) { root.setAttribute("data-kit-test", "failed"); root.setAttribute("data-kit-test-error", String(error && error.message || error)); } });
</script>`)
}
