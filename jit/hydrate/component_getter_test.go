package hydrate

import "testing"

// A component writes a computed value as a getter — `get visibleTodos() { return
// this.todos.filter(...) }` — which is the JavaScript counterpart of a lambda in a scope literal.
// Seeding used to READ each key, so a getter was called once and its answer frozen: the list looked
// right on the first paint and never changed again. kitjs.org's todo app is where this surfaced.
func TestComponentGettersStayComputed(t *testing.T) {
	const assertions = `
var kit = window.kit;
kit.component("getterdemo", {
  items: [{ done: false }, { done: true }],
  get remaining() { return this.items.filter(function (i) { return !i.done; }).length; },
  get total() { return this.items.length; },
  add: function () { this.items = this.items.concat([{ done: false }]); }
});

var host = el("div", { "data-kit-component": "getterdemo", "data-kit-alias": "$demo" });
var remaining = el("b", { "data-kit-text": "remaining" });
var total = el("i", { "data-kit-text": "total" });
var go = el("button", { "data-kit-click": "add()" });
host.appendChild(remaining); host.appendChild(total); host.appendChild(go);
document.body.appendChild(host);
kit.render();

if (remaining.textContent !== "1") throw new Error("initial remaining should be 1, got " + remaining.textContent);
if (total.textContent !== "2") throw new Error("initial total should be 2, got " + total.textContent);

document.dispatchEvent({ type: "click", target: go, defaultPrevented: false, preventDefault: function () {}, stopPropagation: function () {} });
kit.render();

// The proof: a getter is still a getter after seeding, so it follows the state it reads.
if (remaining.textContent !== "2") throw new Error("remaining did not follow items, got " + remaining.textContent);
if (total.textContent !== "3") throw new Error("total did not follow items, got " + total.textContent);

// And two instances never share one array.
var second = el("div", { "data-kit-component": "getterdemo" });
var count = el("u", { "data-kit-text": "total" });
second.appendChild(count);
document.body.appendChild(second);
kit.render();
if (count.textContent !== "2") throw new Error("a second instance should start fresh, got " + count.textContent);

console.log("component getters: computed at read time, per instance");
`
	runNodeDOMScript(t, "component_getter.test.js", assertions)
}
