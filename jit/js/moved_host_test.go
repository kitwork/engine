package js

import "testing"

// A component host that MOVES — taken out of one parent and put into another, as Drive's morph
// does when it reorders a page — is still on the page. The kernel's observer used to treat every
// removed node as gone and clean it up, a tick after the move: the component's own listeners went
// with it while its state stayed, so the tree on kitwork.io's tree page stopped answering the arrow
// keys after a Drive swap, with nothing reported anywhere (01/10). The tree went on 26/10/2026; the
// tabs answer the arrows from a listener on their host the same way.
func TestBrowserAMovedHostKeepsItsListeners(t *testing.T) {
	runBrowserProof(t, "moved host", []string{"tabs"}, `
<section id="first">
  <div id="tabs" role="tablist" aria-label="Files" data-kitwork-component="tabs" data-kitwork-scope="selected: 'one'">`+tabButton("one")+tabButton("two")+`</div>
</section>
<section id="second"></section>
<div id="gone" role="tablist" data-kitwork-component="tabs"><button type="button" role="tab" value="x" data-kitwork-event="click: select('x')">x</button></div>
<script>
(async function () {
  await wait(80);
  var el = function (id) { return document.getElementById(id); };
  el("second").appendChild(el("tabs")); // moved, not removed
  el("gone").remove();                   // CONTROL: really removed
  await wait(60);
  focusIn(el("one"));
  key(el("one"), "ArrowRight"); await wait(40);
  assert(active() === "two" && el("two").getAttribute("aria-selected") === "true", "moved tabs still walk with the arrows, focus is on " + active());
  root.setAttribute("data-kit-test", "passed");
})().catch(function (error) { var root = document.documentElement; if (!root.hasAttribute("data-kit-test")) { root.setAttribute("data-kit-test", "failed"); root.setAttribute("data-kit-test-error", String(error && error.message || error)); } });
</script>`)
}
