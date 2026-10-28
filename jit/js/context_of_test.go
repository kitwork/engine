package js

import "testing"

// kitwork.context(this): a component method reaches the context its init received — the same object —
// without stashing it in a Symbol slot of its own. In a real browser (the page hears each changed in
// one write, the hosts it came from in order: a pair runs one expression):
//
//	· from a click inside the host, from a nested data-kitwork-scope inside it (the call falls through
//	  to the component's method), and through the alias from outside the host — all reach the host;
//	· it is the very context init got, so emit/owned/listen behave the same from anywhere (element went
//	  with data-kit-element on 26/10/2026);
//	· two instances never share one: each call answers for its own host;
//	· anything that is not a component's this — the page's scope, a plain scope, a stray object —
//	  is refused by name.
func TestBrowserKitContextFromAMethod(t *testing.T) {
	runBrowserProof(t, "kitwork.context", nil, `
<div id="page" data-kitwork-scope="heard: ''">
  <div id="one" data-kitwork-component="counter: $one" data-kitwork-event="changed: heard = heard + $event.target.id + ','">
    <button id="inside" data-kitwork-event="click: increment()">+</button>
    <div data-kitwork-scope="label: 'nested'">
      <button id="nested" data-kitwork-event="click: increment()">+</button>
    </div>
    <span class="value" data-kitwork-bind="text: count">0</span>
  </div>
  <div id="two" data-kitwork-component="counter"></div>
  <button id="outside" data-kitwork-event="click: $one.increment()">+ from outside</button>
  <div id="plain" data-kitwork-scope="x: 1"></div>
</div>
<script>
kitwork.component("counter", {
  count: 0,
  same: 0,
  init: function (context) { this.initContext = context; },
  increment: function () {
    var context = kitwork.context(this);
    if (context === this.initContext) this.same++;
    this.count++;
    context.emit("changed", { count: this.count });
  },
  valueText: function () { return kitwork.context(this).owned(".value")[0].textContent; }
});
(async function () {
  await wait(150);
  var page = function () { return window.kitwork.scopeFor(document.getElementById("page")); };
  var one = function () { return window.kitwork.scopeFor(document.getElementById("one")); };

  document.getElementById("inside").click(); await wait(40);
  assert(one().count === 1 && page().heard === "one,", "a click inside the host: " + one().count + " " + page().heard);

  document.getElementById("nested").click(); await wait(40);
  assert(one().count === 2 && page().heard === "one,one,", "from a nested scope inside the host: " + one().count + " " + page().heard);

  document.getElementById("outside").click(); await wait(40);
  assert(one().count === 3 && page().heard === "one,one,one,", "through the alias from outside: " + one().count + " " + page().heard);

  assert(one().same === 3, "every call answered with the context init received, got " + one().same + " of 3");
  assert(one().valueText() === "3", "context.owned from a method finds the host's own part, got " + one().valueText());

  var two = window.kitwork.scopeFor(document.getElementById("two"));
  two.increment(); await wait(40);
  assert(two.count === 1 && one().count === 3 && page().heard === "one,one,one,", "the second instance answers for itself (its event has no listener): " + two.count + " " + one().count + " " + page().heard);
  assert(kitwork.context(two) !== kitwork.context(one()), "two instances, two contexts");
  assert(kitwork.context(two).host === document.getElementById("two"), "the context's host is the instance's own host");

  var refusals = 0;
  [page(), window.kitwork.scopeFor(document.getElementById("plain")), {}, null].forEach(function (self) {
    try { kitwork.context(self); } catch (error) { if (String(error.message).indexOf("kitwork.context(this)") >= 0) refusals++; }
  });
  assert(refusals === 4, "the page scope, a plain scope, an object and null are each refused by name, got " + refusals + " of 4");

  root.setAttribute("data-kit-test", "passed");
})().catch(function (error) { var root = document.documentElement; if (!root.hasAttribute("data-kit-test")) { root.setAttribute("data-kit-test", "failed"); root.setAttribute("data-kit-test-error", String(error && error.message || error)); } });
</script>`)
}
