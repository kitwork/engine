package hydrate

import "testing"

// A scope literal may hold a lambda, which gives a page the one thing a state value cannot: a name
// for an expression it repeats. `screen === 'workspace'` written twelve times across a page is
// twelve places to edit when the screen is renamed; `isWorkspace()` is one.
//
// It works because a lambda is called against the scope chain of whoever calls it, so the names in
// its body resolve exactly as they would where the call is written — and reading them during a
// render pass is what makes the result repaint when they change.
func TestComputedValuesInAScopeLiteral(t *testing.T) {
	const assertions = `
var kit = window.kit;
function click(target) {
  document.dispatchEvent({ type: "click", target: target, defaultPrevented: false, preventDefault: function () {}, stopPropagation: function () {} });
  kit.render();
}

var host = el("div", { "data-kit-scope": "{ screen: 'workspace', rows: [1, 2, 3], " +
  "isWorkspace: () => screen === 'workspace', " +
  "title: () => isWorkspace() ? 'Workspace' : 'Library', " +
  "plural: (n) => n + (n === 1 ? ' row' : ' rows') }" });
var flag = el("b", { "data-kit-text": "isWorkspace() ? 'yes' : 'no'" });
var name = el("h1", { "data-kit-text": "title()" });
var count = el("small", { "data-kit-text": "plural(rows.length)" });
var one = el("em", { "data-kit-text": "plural(1)" });
var go = el("button", { "data-kit-click": "screen = 'library'" });
host.appendChild(flag); host.appendChild(name); host.appendChild(count); host.appendChild(one); host.appendChild(go);
document.body.appendChild(host);
kit.render();

if (flag.textContent !== "yes") throw new Error("a computed did not read the scope: " + flag.textContent);
if (name.textContent !== "Workspace") throw new Error("a computed could not call another: " + name.textContent);
if (count.textContent !== "3 rows") throw new Error("a computed ignored its argument: " + count.textContent);
if (one.textContent !== "1 row") throw new Error("a computed branch on its argument failed: " + one.textContent);

// The proof that matters: the answer is computed at read time, not frozen when the scope was made.
click(go);
if (flag.textContent !== "no") throw new Error("the computed did not follow the state it reads: " + flag.textContent);
if (name.textContent !== "Library") throw new Error("the computed calling a computed did not follow: " + name.textContent);
if (count.textContent !== "3 rows") throw new Error("an unrelated computed changed: " + count.textContent);

console.log("computed values in a scope literal: read live, take arguments, compose");
`
	runNodeDOMScript(t, "computed_scope.test.js", assertions)
}

// The name a computed reads resolves where it is CALLED, not where it is written. That is worth a
// test because it is the one way this differs from a closure in ordinary JavaScript: a nested
// boundary that declares the same name shadows it for a computed defined further out.
func TestComputedValueResolvesInTheCallingScope(t *testing.T) {
	const assertions = `
var kit = window.kit;
var outer = el("div", { "data-kit-scope": "{ screen: 'workspace', isWorkspace: () => screen === 'workspace' }" });
var here = el("b", { "data-kit-text": "isWorkspace() ? 'yes' : 'no'" });
var inner = el("div", { "data-kit-scope": "{ screen: 'library' }" });
var there = el("i", { "data-kit-text": "isWorkspace() ? 'yes' : 'no'" });
inner.appendChild(there);
outer.appendChild(here); outer.appendChild(inner);
document.body.appendChild(outer);
kit.render();

if (here.textContent !== "yes") throw new Error("the computed failed in its own boundary: " + here.textContent);
if (there.textContent !== "no") throw new Error("the computed should read the nearer screen, got: " + there.textContent);

console.log("a computed reads the scope chain of the expression that calls it");
`
	runNodeDOMScript(t, "computed_scope_shadow.test.js", assertions)
}
