package js

import "testing"

// context.require: a component says what it cannot work without, and a page that leaves it out is
// told — once, on the console, naming the component and what is missing. Each case sits in its own
// section and a report is filed by the section of the element it names, so a message can only have
// come from the component inside it. The complete tree is the control: a check that reported
// everything would fail it.
func TestBrowserRequireNamesWhatIsMissing(t *testing.T) {
	runBrowserProof(t, "require", []string{"tree", "tabs"}, `
<section id="no-id">
  <ul role="tree" data-kit-component="tree">
    <li role="treeitem" data-kit-element="item" data-id="docs">docs</li>
    <li role="treeitem" data-kit-element="item">intro</li>
  </ul>
</section>
<section id="bound" data-kit-scope="names: ['a', 'b']">
  <ul role="tree" data-kit-component="tree">
    <li role="treeitem" data-kit-for="name of names" data-kit-element="item" data-kit-bind:data-id="name" data-kit-text="name"></li>
  </ul>
</section>
<section id="no-tabs">
  <div data-kit-component="tabs"><div role="tablist"></div></div>
</section>
<script>
// Reports reach the console (data-kit-error was removed 02/10); each is filed under the section the
// reporting component sits in, so a message can only have come from that component.
var reports = { "no-id": [], "bound": [], "no-tabs": [] };
var originalError = console.error;
console.error = function (error, directive, element) {
  var section = element && element.closest ? element.closest("section") : null;
  if (section && reports[section.id]) reports[section.id].push(String(error && error.message || error));
  return originalError.apply(console, arguments);
};
(async function () {
  await wait(300);
  function errors(id) { return reports[id]; }

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
