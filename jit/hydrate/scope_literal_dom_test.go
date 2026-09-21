package hydrate

import "testing"

// data-kit-scope="qty: 1, price: 250" — the object literal with its braces left off (ideaship-final
// §6). It seeds the boundary exactly as the braced form does, separated by "," like any object;
// a bare word is still a NAME (an empty local boundary), and an init program still runs.
func TestKernelScopeLiteralBracesOptional(t *testing.T) {
	runNodeDOMScript(t, "scope_literal.js", `
var kit = window.kit;
function boundary(scopeSource) {
  var section = el("section", { "data-kit-scope": scopeSource });
  var out = el("output", { "data-kit-text": "qty * price" });
  section.appendChild(out);
  document.body.appendChild(section);
  return { section: section, out: out };
}
var bare = boundary("qty: 2, price: 250, label: 'two',");
var braced = boundary("{ qty: 3, price: 250 }");
var named = boundary("cart");
var program = boundary("qty = 4; price = 250");
kit.render();

if (bare.out.textContent !== "500") throw new Error("bare literal did not seed the boundary, text = " + JSON.stringify(bare.out.textContent));
if (kit.scopeFor(bare.section).label !== "two") throw new Error("bare literal lost a string field (and its trailing comma should be fine)");
if (braced.out.textContent !== "750") throw new Error("braced literal broke: " + braced.out.textContent);
if (program.out.textContent !== "1000") throw new Error("init program broke: " + program.out.textContent);
if (named.out.textContent !== "0" || kit.scopeFor(bare.section).qty !== 2) throw new Error("a bare word is a name (empty boundary; unknown reads are 0), not a literal: " + JSON.stringify(named.out.textContent));
console.log("kernel scope literal ok");
`)
}
