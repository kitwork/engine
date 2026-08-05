package hydrate

import "testing"

// data-kit-for proves the three things the feature has to get right:
//
//  1. rows materialise from the array, each bound to its own item (content via the ordinary text pass);
//  2. add/remove reconcile the right rows;
//  3. KEYED IDENTITY — a surviving row is the SAME DOM node before and after a mutation, never rebuilt,
//     which is the whole reason for data-kit-key (focus/cursor/input on a row survive a re-render).
//
// The DOM shim + node runner it drives live in blocks_dom_test.go, shared with the data-kit-if test.
func TestKitForRendersAndReconcilesByKey(t *testing.T) {
	const assertions = `
var kit = window.kit;

// Build the cart list: <section component=cart> <ul> <li for=... key=item.id> <span text=item.name> <b text=item.price> </li> </ul> </section>
var section = el("section", { "data-kit-component": "cart" });
var ul = el("ul");
var li = el("li", { "data-kit-for": "item, i of items", "data-kit-key": "item.id" });
li.appendChild(el("span", { "data-kit-text": "item.name" }));
li.appendChild(el("b", { "data-kit-text": "item.price" }));
ul.appendChild(li);
section.appendChild(ul);
document.body.appendChild(section);

// A REAL-JS component — methods are plain functions (filter/push), no IR.
var nextId = 3;
kit.component("cart", {
  items: [ { id: 1, name: "Ban phim", price: 250 }, { id: 2, name: "Chuot", price: 120 } ],
  add: function () { this.items.push({ id: nextId++, name: "Moi", price: 0 }); },
  remove: function (id) { this.items = this.items.filter(function (x) { return x.id !== id; }); }
});
kit.render();

function rows() { return document.querySelectorAll("[data-kit-item]"); }
function rowByKey(k) { var rs = rows(); for (var i = 0; i < rs.length; i++) if (rs[i].getAttribute("data-kit-key") === k) return rs[i]; return null; }
function nameOf(row) { return row.querySelector("[data-kit-text]").textContent; }

var r = rows();
if (r.length !== 2) throw new Error("initial: expected 2 rows, got " + r.length);
if (nameOf(rowByKey("1")) !== "Ban phim") throw new Error("initial: row 1 name = " + nameOf(rowByKey("1")));
if (nameOf(rowByKey("2")) !== "Chuot") throw new Error("initial: row 2 name = " + nameOf(rowByKey("2")));

// Capture the row-2 node object to prove keyed identity across mutations.
var row2 = rowByKey("2");

// ADD → 3 rows; row 2 is the SAME node object (not rebuilt).
kit.scopeFor(section).add();
kit.render();
if (rows().length !== 3) throw new Error("after add: expected 3 rows, got " + rows().length);
if (rowByKey("2") !== row2) throw new Error("after add: row 2 was rebuilt — keyed identity lost");
if (nameOf(rowByKey("3")) !== "Moi") throw new Error("after add: new row name = " + nameOf(rowByKey("3")));

// REMOVE key 1 → 2 rows; key 1 gone; row 2 STILL the same node.
kit.scopeFor(section).remove(1);
kit.render();
var after = rows();
if (after.length !== 2) throw new Error("after remove: expected 2 rows, got " + after.length);
if (rowByKey("1") !== null) throw new Error("after remove: row 1 still present");
if (rowByKey("2") !== row2) throw new Error("after remove: row 2 was rebuilt — keyed identity lost");
if (nameOf(rowByKey("3")) !== "Moi") throw new Error("after remove: row 3 name = " + nameOf(rowByKey("3")));

console.log("data-kit-for: render + keyed reconcile OK (2 -> 3 -> 2, node identity preserved)");
`
	runNodeDOMScript(t, "for.test.js", assertions)
}
