package js

import "testing"

// A component host that MOVES — taken out of one parent and put into another, as Drive's morph
// does when it reorders a page — is still on the page. The kernel's observer used to treat every
// removed node as gone and clean it up, a tick after the move: the component's own listeners went
// with it while its state stayed, so the tree on kitwork.io's tree page stopped answering the arrow
// keys after a Drive swap, with nothing reported anywhere (01/10).
func TestBrowserAMovedHostKeepsItsListeners(t *testing.T) {
	runBrowserProof(t, "moved host", []string{"tree"}, `
<section id="first">
  <ul id="tree" role="tree" aria-label="Files" data-kit-component="tree">
    <li id="one" role="treeitem" data-kit-element="item" data-id="one" data-kit-bind:tabindex="focused === 'one' ? 0 : -1">one</li>
    <li id="two" role="treeitem" data-kit-element="item" data-id="two" data-kit-bind:tabindex="focused === 'two' ? 0 : -1">two</li>
  </ul>
</section>
<section id="second"></section>
<div id="gone" data-kit-component="tree"><span data-kit-element="item" data-id="x">x</span></div>
<script>
(async function () {
  await wait(80);
  var el = function (id) { return document.getElementById(id); };
  el("second").appendChild(el("tree")); // moved, not removed
  el("gone").remove();                   // CONTROL: really removed
  await wait(60);
  focusIn(el("one"));
  key(el("one"), "ArrowDown"); await wait(40);
  assert(active() === "two", "a moved tree still walks with the arrows, focus is on " + active());
  root.setAttribute("data-kit-test", "passed");
})().catch(function (error) { if (!root.hasAttribute("data-kit-test")) { root.setAttribute("data-kit-test", "failed"); root.setAttribute("data-kit-test-error", String(error && error.message || error)); } });
</script>`)
}
