package hydrate

import "testing"

// data-kitwork-scope="qty: 2; price: 250" — pairs, each value its own expression (KITWORK_UI.md §11),
// a trailing ";" fine. It seeds the boundary. What is not "name: value" pairs is reported and the
// boundary stays empty: the old NAME form ("cart") and INIT form ("qty = 4; price = 250"), gone since
// B8 (22/09), and the braced object of the hand-written data-kit-scope ("{ qty: 3, price: 250 }"),
// which the kernel stopped reading on 26/10/2026.
func TestKernelScopeLiteralIsPairs(t *testing.T) {
	runNodeDOMScript(t, "scope_literal.js", `
var kit = window.kit;
function boundary(scopeSource) {
  var section = el("section", { "data-kitwork-scope": scopeSource });
  var out = el("output", { "data-kitwork-bind": "text: qty * price" });
  section.appendChild(out);
  document.body.appendChild(section);
  return { section: section, out: out };
}
var logged = [];
var originalError = console.error;
console.error = function (e) { logged.push(String(e && e.message || e)); };
var bare = boundary("qty: 2; price: 250; label: 'two';");
var braced = boundary("{ qty: 3, price: 250 }");
var named = boundary("cart");
var program = boundary("qty = 4; price = 250");
kit.render();
console.error = originalError;

if (bare.out.textContent !== "500") throw new Error("the pairs did not seed the boundary, text = " + JSON.stringify(bare.out.textContent));
if (kit.scopeFor(bare.section).label !== "two") throw new Error("the pairs lost a string field (and the trailing ; should be fine)");
if (braced.out.textContent !== "0") throw new Error("the braced object must not seed any more, got " + braced.out.textContent);
if (program.out.textContent !== "0") throw new Error("the init form must not seed any more, got " + program.out.textContent);
if (named.out.textContent !== "0" || kit.scopeFor(bare.section).qty !== 2) throw new Error("the name form must not seed (empty boundary; unknown reads are 0): " + JSON.stringify(named.out.textContent));
if (logged.filter(function (m) { return /is not name: value/.test(m); }).length !== 3) throw new Error("the three rejected forms should each be reported as not name: value, logged: " + logged.join(" | "));
console.log("kernel scope literal ok");
`)
}
