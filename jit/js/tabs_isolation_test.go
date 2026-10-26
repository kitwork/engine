package js

import "testing"

// Two components that each define a helper of the same name must not overwrite each other. tree and
// tabs both had a top-level nameOf — tree's read data-id, tabs' read data-tab — and concatenated
// bare into one script the later one won: on a page with a tree and a tablist, the arrow keys moved
// focus along the tabs but selected nothing. The catalogue's tree page was exactly that page.
//
// The tree went on 26/10/2026. tabs and terminal both have a top-level follow — tabs' moves a tablist
// to a choice another one made, terminal's scrolls a line after its typing caret — and the terminal's
// file comes after the tabs': joined bare, a choice in one tablist would no longer reach the other
// that keeps its choice under the same name.
func TestBrowserComponentsKeepTheirHelpersToThemselves(t *testing.T) {
	runBrowserProof(t, "component isolation", []string{"tabs", "terminal"}, `
<div id="first" role="tablist" aria-label="Files" data-kitwork-component="tabs" data-kitwork-scope="selected: 'a'; persist: 'files'">`+tabButton("a")+tabButton("b")+`</div>
<div id="second" role="tablist" aria-label="Files again" data-kitwork-component="tabs" data-kitwork-scope="selected: 'a'; persist: 'files'">
  <button id="a2" type="button" role="tab" value="a" data-kitwork-bind="aria-selected: selected == 'a'" data-kitwork-event="click: select('a')">A</button>
  <button id="b2" type="button" role="tab" value="b" data-kitwork-bind="aria-selected: selected == 'b'" data-kitwork-event="click: select('b')">B</button>
</div>
<div id="term" data-kitwork-component="terminal"><pre><code>go test ./...</code></pre></div>
<script>
(async function () {
  await wait(80);
  var el = function (id) { return document.getElementById(id); };
  focusIn(el("a"));
  key(el("a"), "ArrowRight"); await wait(40);
  assert(active() === "b" && el("b").getAttribute("aria-selected") === "true", "ArrowRight selects the next tab, not only focuses it; selected=" + el("b").getAttribute("aria-selected"));
  assert(el("b2").getAttribute("aria-selected") === "true" && el("a2").getAttribute("aria-selected") === "false",
    "and the tablist that keeps its choice under the same name follows it with the tabs' own follow, not the terminal's; selected=" + el("b2").getAttribute("aria-selected"));
  assert(typeof window.follow === "undefined" && typeof window.step === "undefined" && typeof window.nameOf === "undefined", "no component helper leaks into the page's globals");
  root.setAttribute("data-kit-test", "passed");
})().catch(function (error) { var root = document.documentElement; if (!root.hasAttribute("data-kit-test")) { root.setAttribute("data-kit-test", "failed"); root.setAttribute("data-kit-test-error", String(error && error.message || error)); } });
</script>`)
}
