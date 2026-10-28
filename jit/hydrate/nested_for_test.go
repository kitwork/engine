package hydrate

import "testing"

// A data-kitwork-for inside the rows of another: weeks of days, groups of items. A row created in this
// pass carries its own inner data-kitwork-for — and renderFor collected the lists only once, at the
// start of the pass, so the inner list was not seen until the NEXT pass. The new row appeared
// empty until something else happened to repaint. Found on 29/09 by the calendar: PageUp back to
// September drew September's title over a grid with no days in it.
func TestNestedForFillsANewRowInTheSamePass(t *testing.T) {
	runNodeDOMScript(t, "nested_for.test.js", `
var kitwork = window.kitwork;
var box = el("div", { "data-kitwork-scope": "weeks: [[1, 2], [3, 4]]" });
var row = el("p", { "data-kitwork-for": "week of weeks; key: week[0]" });
row.appendChild(el("b", { "data-kitwork-for": "day of week; key: day", "data-kitwork-bind": "text: day" }));
box.appendChild(row);
document.body.appendChild(box);
kitwork.render(); kitwork.render(); // the first page may take two passes; that is not the case under test

function days() {
  var out = [];
  walk(document.body, function (n) { if (n.tagName === "B" && n.getAttribute("data-kitwork-item")) out.push(n.textContent); });
  return out.join(",");
}
if (days() !== "1,2,3,4") throw new Error("precondition: the first weeks render, got " + days());

// New weeks — rows that did not exist before — and ONE pass.
kitwork.scopeFor(box).weeks = [[3, 4], [5, 6], [7, 8]];
kitwork.render();
if (days() !== "3,4,5,6,7,8") throw new Error("a new row's inner list must fill in the same pass, got " + days());
console.log("a nested list fills a new row in the same pass");
`)
}
