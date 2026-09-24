package hydrate

import "testing"

// Morph keeps a keyed child's identity across a swap when the key is authored — data-kit-key (the
// for directive's spelling). data-key was an alias with no reason to exist; the engine-prefixed
// data-kitwork-key had no emitter and no longer keys anything (§9 subtraction, 22/09): two rows
// that agree only on it are ordinary same-tag siblings, matched by position.
func TestMorphKeysOnAuthoredAttributesOnly(t *testing.T) {
	const assertions = `
var kit = window.kit;
function list(keyAttr, keys) {
  var ul = el("ul", {});
  keys.forEach(function (k) { var li = el("li", {}); li.setAttribute(keyAttr, k); li.setAttribute("data-id", k); ul.appendChild(li); });
  return ul;
}
function ids(ul) { return ul.childNodes.map(function (n) { return n.getAttribute("data-id"); }).join(","); }

["data-kit-key"].forEach(function (attr) {
  var from = list(attr, ["a", "b", "c"]);
  var a = from.childNodes[0], b = from.childNodes[1], c = from.childNodes[2];
  kit.morph(from, list(attr, ["c", "b", "a"]));
  if (ids(from) !== "c,b,a") throw new Error(attr + ": order not reconciled: " + ids(from));
  if (from.childNodes[0] !== c || from.childNodes[1] !== b || from.childNodes[2] !== a) {
    throw new Error(attr + ": keyed rows must MOVE (same nodes, new order), not be rewritten in place");
  }
});

// The node shim keeps attributes as a plain object, so morph's attribute sync is a no-op here;
// what it CAN show is identity: with no authored key the three <li> match by position and stay
// where they are, instead of moving by data-kitwork-key.
["data-kitwork-key", "data-key"].forEach(function (attr) {
  var inert = list(attr, ["a", "b", "c"]);
  var i0 = inert.childNodes[0], i1 = inert.childNodes[1], i2 = inert.childNodes[2];
  kit.morph(inert, list(attr, ["c", "b", "a"]));
  if (inert.childNodes.length !== 3) throw new Error(attr + ": positional match should keep three rows, got " + inert.childNodes.length);
  if (inert.childNodes[0] !== i0 || inert.childNodes[1] !== i1 || inert.childNodes[2] !== i2) {
    throw new Error(attr + " must not key rows — expected positional matching (nodes stay put), got keyed moves");
  }
});

var legacy = list("data-kitwork-key", ["a", "b", "c"]);
var l0 = legacy.childNodes[0], l1 = legacy.childNodes[1], l2 = legacy.childNodes[2];
kit.morph(legacy, list("data-kitwork-key", ["c", "b", "a"]));
if (legacy.childNodes.length !== 3) throw new Error("legacy: positional match should keep three rows, got " + legacy.childNodes.length);
if (legacy.childNodes[0] !== l0 || legacy.childNodes[1] !== l1 || legacy.childNodes[2] !== l2) {
  throw new Error("legacy: data-kitwork-key must not key rows — expected positional matching (nodes stay in place), got keyed moves");
}
console.log("morph keys: data-kit-key moves nodes; data-key and data-kitwork-key are inert");
`
	runNodeDOMScript(t, "morph_key.test.js", assertions)
}
