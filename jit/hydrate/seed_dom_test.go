package hydrate

import "testing"

// data-kit-seed in the kernel — DOM → state, once: the server's text becomes state (DOM wins over
// the scope literal), a property/attribute by the bind groups in reverse, a dotted path builds its
// objects, list[] collects in document order, a JSON island hands over structure, and a later state
// change is not undone by re-rendering (no second read of the DOM).
func TestKernelSeedHandsTheDOMToState(t *testing.T) {
	runNodeDOMScript(t, "seed.js", `
var kit = window.kit;
var page = el("section", { "data-kit-scope": "title: 'from the literal', count: 0, expanded: false, busy: 'unset', tags: [], user: null, items: []" });
var title = el("h1", { "data-kit-seed": "title", "data-kit-text": "title" }); title.textContent = "  Rendered by the server\n";
var email = el("input", { "value": "ada@example.com", "data-kit-seed:value": "user.email" }); email.value = "ada@example.com";
var age = el("input", { "type": "number", "value": "36", "data-kit-seed:value": "count" }); age.value = "36";
var details = el("details", { "open": "", "data-kit-seed:open": "expanded" });
var busy = el("button", { "aria-busy": "true", "data-kit-seed:aria-busy": "busy", "data-kit-seed:data-missing": "user.missing" });
var ul = el("ul");
["go", "sqlite", "html"].forEach(function (t) { var li = el("li", { "data-kit-seed": "tags[]" }); li.textContent = t; ul.appendChild(li); });
var island = el("script", { "type": "application/json", "data-kit-seed": "items" }); island.textContent = '[{"id": 1, "name": "Ada"}, {"id": 2, "name": "Bob"}]';
var out = el("output", { "data-kit-text": "title + '|' + user.email + '|' + count + '|' + expanded + '|' + busy + '|' + user.missing + '|' + tags.join(',') + '|' + items.length" });
[title, email, age, details, busy, ul, island, out].forEach(function (n) { page.appendChild(n); });
document.body.appendChild(page);
kit.render();

var scope = kit.scopeFor(page);
if (out.textContent !== "Rendered by the server|ada@example.com|36|true|true|null|go,sqlite,html|2") throw new Error("seeded state, got " + JSON.stringify(out.textContent));
if (title.textContent !== "Rendered by the server") throw new Error("the text binding re-renders the trimmed value, got " + JSON.stringify(title.textContent));
if (scope.items[1].name !== "Bob") throw new Error("the JSON island should be parsed into state");

// Once: a later change survives the next render even though the DOM still says 36.
scope.count = 1; scope.title = "changed";
kit.render();
if (scope.count !== 1 || scope.title !== "changed") throw new Error("seeding must not run again on the next render: count=" + scope.count + " title=" + scope.title);
console.log("kernel seed ok");
`)
}
