package hydrate

import "testing"

// A data-kit-for inside the rows of another: weeks of days, groups of items. A row created in this
// pass carries its own inner data-kit-for — and renderFor collected the lists only once, at the
// start of the pass, so the inner list was not seen until the NEXT pass. The new row appeared
// empty until something else happened to repaint. Found on 29/09 by the calendar: PageUp back to
// September drew September's title over a grid with no days in it.
func TestNestedForFillsANewRowInTheSamePass(t *testing.T) {
	runNodeDOMScript(t, "nested_for.test.js", `
var kit = window.kit;
var box = el("div", { "data-kit-scope": "{ weeks: [[1, 2], [3, 4]] }" });
var row = el("p", { "data-kit-for": "week of weeks", "data-kit-key": "week[0]" });
row.appendChild(el("b", { "data-kit-for": "day of week", "data-kit-key": "day", "data-kit-text": "day" }));
box.appendChild(row);
document.body.appendChild(box);
kit.render(); kit.render(); // the first page may take two passes; that is not the case under test

function days() {
  var out = [];
  walk(document.body, function (n) { if (n.tagName === "B" && n.getAttribute("data-kit-item")) out.push(n.textContent); });
  return out.join(",");
}
if (days() !== "1,2,3,4") throw new Error("precondition: the first weeks render, got " + days());

// New weeks — rows that did not exist before — and ONE pass.
kit.scopeFor(box).weeks = [[3, 4], [5, 6], [7, 8]];
kit.render();
if (days() !== "3,4,5,6,7,8") throw new Error("a new row's inner list must fill in the same pass, got " + days());
console.log("a nested list fills a new row in the same pass");
`)
}
