package js

import (
	"strings"
	"testing"
)

func TestTreeIsShippedWhenUsed(t *testing.T) {
	out := deliver(`<ul role="tree" data-kit-component="tree"><li data-kit-element="item" data-id="a">a</li></ul>`)
	if !strings.Contains(out, `/kit/components/tree@`) {
		t.Fatalf("tree was not injected for markup that uses it:\n%s", out)
	}
	if !strings.Contains(ModulesJS([]string{"tree"}), `component("tree@v1.0.0"`) {
		t.Error("versioned registration missing")
	}
}

// item renders one treeitem with every state it shows written as a binding.
func treeItem(name, onClick, inner string) string {
	return `<li role="treeitem" id="` + name + `" data-kit-element="item" data-id="` + name + `"` +
		` data-kit-bind:aria-expanded="expanded.includes('` + name + `') ? 'true' : 'false'"` +
		` data-kit-bind:aria-selected="selected === '` + name + `' ? 'true' : 'false'"` +
		` data-kit-bind:tabindex="focused === '` + name + `' ? 0 : -1"` +
		` data-kit-click:self="` + onClick + `">` + name + inner + `</li>`
}

// The tree view pattern is focus walking the tree's SHAPE — skipping what a closed branch hides,
// opening and entering with →, closing and climbing with ←. Only a browser can show where focus is.
func TestBrowserTreeWalksFocusAlongTheShape(t *testing.T) {
	group := func(parent, items string) string {
		return `<ul role="group" data-kit-show="expanded.includes('` + parent + `')" hidden>` + items + `</ul>`
	}
	markup := `<ul role="tree" aria-label="Files" data-kit-component="tree" data-kit-alias="$files">` +
		treeItem("docs", "toggle('docs')", group("docs", treeItem("intro", "select('intro')", "")+treeItem("api", "select('api')", ""))) +
		treeItem("src", "toggle('src')", group("src", treeItem("main", "select('main')", ""))) +
		treeItem("readme", "select('readme')", "") +
		`</ul>`

	runBrowserProof(t, "tree", []string{"tree"}, markup+`
<script>
(async function () {
  await wait(80);
  var el = function (id) { return document.getElementById(id); };
  var tabStops = function () { return [].slice.call(document.querySelectorAll('[role=treeitem]')).filter(function (i) { return i.getAttribute("tabindex") === "0"; }).map(function (i) { return i.id; }).join(","); };

  // One tab stop, on the first item, because the markup's tabindex binding reads 'focused'.
  assert(tabStops() === "docs", "exactly one tab stop, on the first item, got " + tabStops());
  focusIn(el("docs"));

  // ↓ skips what a closed branch hides: docs → src, not docs → intro.
  key(el("docs"), "ArrowDown"); await wait(40);
  assert(active() === "src", "ArrowDown from a closed branch skips its children, focus is on " + active());
  assert(tabStops() === "src", "the tab stop follows focus, got " + tabStops());

  // → opens a closed branch (focus stays), → again enters its first child.
  key(el("src"), "ArrowRight"); await wait(60);
  assert(el("src").getAttribute("aria-expanded") === "true" && active() === "src", "ArrowRight opens src and stays on it");
  key(el("src"), "ArrowRight"); await wait(60);
  assert(active() === "main", "ArrowRight on an open branch enters its first child, focus is on " + active());

  // ← on a child climbs to the parent; ← on an open parent closes it.
  key(el("main"), "ArrowLeft"); await wait(40);
  assert(active() === "src", "ArrowLeft on a child climbs to its parent, focus is on " + active());
  key(el("src"), "ArrowLeft"); await wait(60);
  assert(el("src").getAttribute("aria-expanded") === "false" && active() === "src", "ArrowLeft on an open branch closes it");

  // Home / End reach the ends of what is on screen.
  key(el("src"), "End"); await wait(40);
  assert(active() === "readme", "End reaches the last item on screen, focus is on " + active());
  key(el("readme"), "Home"); await wait(40);
  assert(active() === "docs", "Home reaches the first, focus is on " + active());

  // Open docs, walk into it, and Enter selects — aria-selected is the markup's binding.
  key(el("docs"), "ArrowRight"); await wait(60);
  key(el("docs"), "ArrowDown"); await wait(60);
  assert(active() === "intro", "once docs is open, ArrowDown enters intro, focus is on " + active());
  key(el("intro"), "ArrowDown"); await wait(40);
  key(el("api"), "Enter"); await wait(40);
  assert(el("api").getAttribute("aria-selected") === "true" && el("intro").getAttribute("aria-selected") === "false", "Enter selects the focused item");

  // CONTROL: the grammar's half works without a key — a click on an item runs its own expression.
  el("readme").click(); await wait(40);
  assert(el("readme").getAttribute("aria-selected") === "true", "a click selects through data-kit-click");

  root.setAttribute("data-kit-test", "passed");
})().catch(function (error) { var root = document.documentElement; if (!root.hasAttribute("data-kit-test")) { root.setAttribute("data-kit-test", "failed"); root.setAttribute("data-kit-test-error", String(error && error.message || error)); } });
</script>`)
}

// A page that seeds its state from the markup — the branch that is open, the file that is chosen —
// gets the tab stop on the chosen file, and the arrows start from wherever focus really is, even
// when it got there without the tree's help.
func TestBrowserTreeTabStopFollowsSeedsAndRealFocus(t *testing.T) {
	markup := `<ul role="tree" aria-label="Files" data-kit-component="tree">` +
		`<li role="treeitem" id="docs" data-kit-element="item" data-id="docs" data-kit-seed:data-id="expanded[]"` +
		` data-kit-bind:aria-expanded="expanded.includes('docs') ? 'true' : 'false'"` +
		` data-kit-bind:tabindex="focused === 'docs' ? 0 : -1">docs` +
		`<ul role="group" data-kit-show="expanded.includes('docs')">` +
		treeItem("intro", "select('intro')", "") +
		`<li role="treeitem" id="api" data-kit-element="item" data-id="api" data-kit-seed:data-id="selected"` +
		` data-kit-bind:aria-selected="selected === 'api' ? 'true' : 'false'"` +
		` data-kit-bind:tabindex="focused === 'api' ? 0 : -1" data-kit-click:self="select('api')">api</li>` +
		`</ul></li>` +
		treeItem("readme", "select('readme')", "") +
		`</ul>`

	runBrowserProof(t, "tree seeds", []string{"tree"}, markup+`
<script>
(async function () {
  await wait(80);
  var el = function (id) { return document.getElementById(id); };
  var tabStops = function () { return [].slice.call(document.querySelectorAll('[role=treeitem]')).filter(function (i) { return i.getAttribute("tabindex") === "0"; }).map(function (i) { return i.id; }).join(","); };

  assert(el("docs").getAttribute("aria-expanded") === "true" && el("api").getAttribute("aria-selected") === "true", "the seeds reached the tree's state");
  assert(tabStops() === "api", "the one tab stop is the seeded selection, got " + tabStops());

  // Focus put on intro by something other than the tree: the tab stop follows it, and ↓ starts there.
  focusIn(el("intro")); await wait(40);
  assert(tabStops() === "intro", "focus arriving from outside moves the tab stop, got " + tabStops());
  key(el("intro"), "ArrowDown"); await wait(40);
  assert(active() === "api", "ArrowDown starts from the focused item, focus is on " + active());

  root.setAttribute("data-kit-test", "passed");
})().catch(function (error) { var root = document.documentElement; if (!root.hasAttribute("data-kit-test")) { root.setAttribute("data-kit-test", "failed"); root.setAttribute("data-kit-test-error", String(error && error.message || error)); } });
</script>`)
}
