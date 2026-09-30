package js

import "testing"

// Two components that each define a helper of the same name must not overwrite each other. tree and
// tabs both have a top-level nameOf — tree's reads data-id, tabs' reads data-tab — and concatenated
// bare into one script the later one won: on a page with a tree and a tablist, the arrow keys moved
// focus along the tabs but selected nothing. The catalogue's tree page is exactly that page.
func TestBrowserComponentsKeepTheirHelpersToThemselves(t *testing.T) {
	runBrowserProof(t, "component isolation", []string{"tabs", "tree"}, `
<div data-kit-component="tabs">
  <div role="tablist" data-kit-element="list" aria-label="Files">
    <button id="a" role="tab" data-kit-element="tab" data-tab="a" data-kit-click="select('a')" data-kit-bind:aria-selected="current === 'a' ? 'true' : 'false'" data-kit-bind:tabindex="current === 'a' ? 0 : -1">A</button>
    <button id="b" role="tab" data-kit-element="tab" data-tab="b" data-kit-click="select('b')" data-kit-bind:aria-selected="current === 'b' ? 'true' : 'false'" data-kit-bind:tabindex="current === 'b' ? 0 : -1">B</button>
  </div>
</div>
<ul role="tree" aria-label="Tree" data-kit-component="tree">
  <li id="one" role="treeitem" data-kit-element="item" data-id="one" data-kit-bind:tabindex="focused === 'one' ? 0 : -1">one</li>
  <li id="two" role="treeitem" data-kit-element="item" data-id="two" data-kit-bind:tabindex="focused === 'two' ? 0 : -1">two</li>
</ul>
<script>
(async function () {
  await wait(80);
  var el = function (id) { return document.getElementById(id); };
  focusIn(el("a"));
  key(el("a"), "ArrowRight"); await wait(40);
  assert(active() === "b" && el("b").getAttribute("aria-selected") === "true", "ArrowRight selects the next tab, not only focuses it; selected=" + el("b").getAttribute("aria-selected"));
  focusIn(el("one"));
  key(el("one"), "ArrowDown"); await wait(40);
  assert(active() === "two", "and the tree beside it still walks, focus is on " + active());
  assert(typeof window.nameOf === "undefined" && typeof window.walk === "undefined", "no component helper leaks into the page's globals");
  root.setAttribute("data-kit-test", "passed");
})().catch(function (error) { if (!root.hasAttribute("data-kit-test")) { root.setAttribute("data-kit-test", "failed"); root.setAttribute("data-kit-test-error", String(error && error.message || error)); } });
</script>`)
}
