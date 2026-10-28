package hydrate

import "testing"

// Morph keeps a keyed child's identity across a swap by data-kitwork-key, the marker a live
// data-kitwork-for writes on each row it makes (26/10/2026; data-kit-key until then). data-key was an
// alias with no reason to exist, and data-kit-key keys nothing any more: two rows that agree only on
// either are ordinary same-tag siblings, matched by position.
func TestMorphKeysOnAuthoredAttributesOnly(t *testing.T) {
	const assertions = `
var kitwork = window.kitwork;
function list(keyAttr, keys) {
  var ul = el("ul", {});
  keys.forEach(function (k) { var li = el("li", {}); li.setAttribute(keyAttr, k); li.setAttribute("data-id", k); ul.appendChild(li); });
  return ul;
}
function ids(ul) { return ul.childNodes.map(function (n) { return n.getAttribute("data-id"); }).join(","); }

["data-kitwork-key"].forEach(function (attr) {
  var from = list(attr, ["a", "b", "c"]);
  var a = from.childNodes[0], b = from.childNodes[1], c = from.childNodes[2];
  kitwork.morph(from, list(attr, ["c", "b", "a"]));
  if (ids(from) !== "c,b,a") throw new Error(attr + ": order not reconciled: " + ids(from));
  if (from.childNodes[0] !== c || from.childNodes[1] !== b || from.childNodes[2] !== a) {
    throw new Error(attr + ": keyed rows must MOVE (same nodes, new order), not be rewritten in place");
  }
});

// The node shim keeps attributes as a plain object, so morph's attribute sync is a no-op here;
// what it CAN show is identity: with no key morph reads, the three <li> match by position and stay
// where they are, instead of moving.
["data-kit-key", "data-key"].forEach(function (attr) {
  var inert = list(attr, ["a", "b", "c"]);
  var i0 = inert.childNodes[0], i1 = inert.childNodes[1], i2 = inert.childNodes[2];
  kitwork.morph(inert, list(attr, ["c", "b", "a"]));
  if (inert.childNodes.length !== 3) throw new Error(attr + ": positional match should keep three rows, got " + inert.childNodes.length);
  if (inert.childNodes[0] !== i0 || inert.childNodes[1] !== i1 || inert.childNodes[2] !== i2) {
    throw new Error(attr + " must not key rows — expected positional matching (nodes stay put), got keyed moves");
  }
});

console.log("morph keys: data-kitwork-key moves nodes; data-key and data-kit-key are inert");
`
	runNodeDOMScript(t, "morph_key.test.js", assertions)
}
