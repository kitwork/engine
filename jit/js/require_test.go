package js

import "testing"

// context.require: a component says what it cannot work without, and a page that leaves it out is
// told — once, through the error channel, naming the component and what is missing. Each case sits
// in its own error boundary so a message can only have come from the component inside it. The
// complete tree is the control: a check that reported everything would fail it.
func TestBrowserRequireNamesWhatIsMissing(t *testing.T) {
	runBrowserProof(t, "require", []string{"tree", "tabs"}, `
<section id="no-id" data-kit-scope="errors: []" data-kit-error="errors = errors.concat([$error.message])">
  <ul role="tree" data-kit-component="tree">
    <li role="treeitem" data-kit-element="item" data-id="docs">docs</li>
    <li role="treeitem" data-kit-element="item">intro</li>
  </ul>
</section>
<section id="bound" data-kit-scope="errors: [], names: ['a', 'b']" data-kit-error="errors = errors.concat([$error.message])">
  <ul role="tree" data-kit-component="tree">
    <li role="treeitem" data-kit-for="name of names" data-kit-element="item" data-kit-bind:data-id="name" data-kit-text="name"></li>
  </ul>
</section>
<section id="no-tabs" data-kit-scope="errors: []" data-kit-error="errors = errors.concat([$error.message])">
  <div data-kit-component="tabs"><div role="tablist"></div></div>
</section>
<script>
(async function () {
  await wait(300);
  function errors(id) { return window.kit.scopeFor(document.getElementById(id)).errors; }

  var noId = errors("no-id");
  assert(noId.length === 1, "a tree item without data-id: want one report, got " + JSON.stringify(noId));
  assert(noId[0].indexOf("tree needs data-id") >= 0 && noId[0].indexOf("1 of 2") >= 0,
    "the report must name the component, the attribute and how many: " + noId[0]);

  assert(errors("bound").length === 0, "a bound data-id is on the element when checked: " + JSON.stringify(errors("bound")));

  var noTabs = errors("no-tabs");
  assert(noTabs.length === 1 && noTabs[0].indexOf('tabs needs data-kit-element="tab"') >= 0,
    "tabs with no tab must say so: " + JSON.stringify(noTabs));

  // Once: later paints do not repeat it.
  window.kit.render();
  await wait(100);
  window.kit.render();
  await wait(100);
  assert(errors("no-id").length === 1, "reported again on a later paint: " + JSON.stringify(errors("no-id")));

  root.setAttribute("data-kit-test", "passed");
})();
</script>`)
}
