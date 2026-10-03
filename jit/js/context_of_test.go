package js

import "testing"

// kit.context(this): a component method reaches the context its init received — the same object —
// without stashing it in a Symbol slot of its own. In a real browser:
//
//	· from a click inside the host, from a nested data-kit-scope inside it (the call falls through
//	  to the component's method), and through the alias from outside the host — all reach the host;
//	· it is the very context init got, so emit/element/listen behave the same from anywhere;
//	· two instances never share one: each call answers for its own host;
//	· anything that is not a component's this — the page's scope, a plain scope, a stray object —
//	  is refused by name.
func TestBrowserKitContextFromAMethod(t *testing.T) {
	runBrowserProof(t, "kit.context", nil, `
<div id="page" data-kit-scope="heard: 0, heardFrom: ''">
  <div id="one" data-kit-component="counter" data-kit-alias="$one" data-kit-event:changed="heard++; heardFrom = $event.target.id">
    <button id="inside" data-kit-click="increment()">+</button>
    <div data-kit-scope="label: 'nested'">
      <button id="nested" data-kit-click="increment()">+</button>
    </div>
    <span data-kit-element="value" data-kit-text="count">0</span>
  </div>
  <div id="two" data-kit-component="counter"></div>
  <button id="outside" data-kit-click="$one.increment()">+ from outside</button>
  <div id="plain" data-kit-scope="x: 1"></div>
</div>
<script>
kit.component("counter", {
  count: 0,
  same: 0,
  init: function (context) { this.initContext = context; },
  increment: function () {
    var context = kit.context(this);
    if (context === this.initContext) this.same++;
    this.count++;
    context.emit("changed", { count: this.count });
  },
  valueText: function () { return kit.context(this).element("value").textContent; }
});
(async function () {
  await wait(150);
  var page = function () { return window.kit.scopeFor(document.getElementById("page")); };
  var one = function () { return window.kit.scopeFor(document.getElementById("one")); };

  document.getElementById("inside").click(); await wait(40);
  assert(one().count === 1 && page().heard === 1 && page().heardFrom === "one", "a click inside the host: " + one().count + " " + page().heard + " " + page().heardFrom);

  document.getElementById("nested").click(); await wait(40);
  assert(one().count === 2 && page().heard === 2 && page().heardFrom === "one", "from a nested scope inside the host: " + one().count + " " + page().heard + " " + page().heardFrom);

  document.getElementById("outside").click(); await wait(40);
  assert(one().count === 3 && page().heard === 3 && page().heardFrom === "one", "through the alias from outside: " + one().count + " " + page().heard + " " + page().heardFrom);

  assert(one().same === 3, "every call answered with the context init received, got " + one().same + " of 3");
  assert(one().valueText() === "3", "context.element from a method finds the host's own part, got " + one().valueText());

  var two = window.kit.scopeFor(document.getElementById("two"));
  two.increment(); await wait(40);
  assert(two.count === 1 && one().count === 3 && page().heardFrom === "one", "the second instance answers for itself (its event has no listener): " + two.count + " " + one().count + " " + page().heardFrom);
  assert(kit.context(two) !== kit.context(one()), "two instances, two contexts");
  assert(kit.context(two).host === document.getElementById("two"), "the context's host is the instance's own host");

  var refusals = 0;
  [page(), window.kit.scopeFor(document.getElementById("plain")), {}, null].forEach(function (self) {
    try { kit.context(self); } catch (error) { if (String(error.message).indexOf("kit.context(this)") >= 0) refusals++; }
  });
  assert(refusals === 4, "the page scope, a plain scope, an object and null are each refused by name, got " + refusals + " of 4");

  root.setAttribute("data-kit-test", "passed");
})().catch(function (error) { var root = document.documentElement; if (!root.hasAttribute("data-kit-test")) { root.setAttribute("data-kit-test", "failed"); root.setAttribute("data-kit-test-error", String(error && error.message || error)); } });
</script>`)
}
