package hydrate

import "testing"

// data-kit-if is the sibling of data-kit-for on the same anchor+template+cleanupTree machinery, but
// its contract is the one that separates it from data-kit-show, and that contract is what this test
// pins:
//
//  1. false  → the branch is NOT in the DOM at all (show would keep it, merely hidden);
//  2. true   → the branch MOUNTS and its bindings run (text bound from the component scope);
//  3. live   → while mounted it re-binds on state change WITHOUT being rebuilt (node identity holds);
//  4. false  → it UNMOUNTS: gone from the DOM, detached, and cleanupTree DISPOSES the subtree's
//     effects (the registered cleanup fires — an SSE stream / listener a hidden node would
//     have kept open is released);
//  5. true again → a FRESH node mounts (proving 4 was a real unmount, not a toggle), and it shows the
//     LATEST state, because component state lives on the boundary and outlives the view.
//
// If renderIf skipped cleanupTree, step 4's dispose count stays 0. If it toggled `hidden` like show
// instead of removing, steps 1/4's element count never drops to 0. Either regression turns this red.
func TestKitIfMountsUnmountsAndDisposes(t *testing.T) {
	const assertions = `
var kit = window.kit;

// <section component=panel> <div if="open"> <span text="title"> </div> </section>
var section = el("section", { "data-kit-component": "panel" });
var div = el("div", { "data-kit-if": "open" });
div.appendChild(el("span", { "data-kit-text": "title" }));
section.appendChild(div);
document.body.appendChild(section);

// REAL-JS component: state on the boundary, a plain-function method. No IR.
kit.component("panel", {
  open: false,
  title: "Editor",
  toggle: function () { this.open = !this.open; }
});
kit.render();

function texts() { return document.querySelectorAll("[data-kit-text]"); }

// 1. open=false → the whole branch is captured as a template and replaced by an anchor: absent.
if (texts().length !== 0) throw new Error("closed: branch should be absent, found " + texts().length);

// 2. open=true → mount; the ordinary text pass binds the span in the same render.
kit.scopeFor(section).toggle();
kit.render();
if (texts().length !== 1) throw new Error("opened: expected 1 mounted node, got " + texts().length);
var span1 = texts()[0];
var mount1 = span1.parentNode; // the mounted branch root — data-kit-if removed, so it never re-collects
if (span1.textContent !== "Editor") throw new Error("opened: span text = " + span1.textContent);

// The dispose probe: a cleanup registered on the mounted subtree. cleanupTree must fire it on unmount.
var disposed = 0;
kit.onCleanup(span1, function () { disposed++; });

// 3. mutate state while mounted → re-binds, same node (no rebuild, no churn).
kit.scopeFor(section).title = "Renamed";
kit.render();
if (texts().length !== 1) throw new Error("relabel: expected 1 node, got " + texts().length);
if (texts()[0] !== span1) throw new Error("relabel: mounted node was rebuilt — identity lost");
if (span1.textContent !== "Renamed") throw new Error("relabel: span text = " + span1.textContent);

// 4. open=false → unmount: absent, detached, and the subtree's effects disposed.
kit.scopeFor(section).toggle();
kit.render();
if (texts().length !== 0) throw new Error("reclose: branch should be gone, found " + texts().length);
if (mount1.parentNode !== null) throw new Error("reclose: unmounted branch root is still attached");
if (disposed !== 1) throw new Error("reclose: cleanupTree did not dispose the subtree (disposed=" + disposed + ")");

// 5. open=true again → a FRESH node, carrying the latest boundary state.
kit.scopeFor(section).toggle();
kit.render();
if (texts().length !== 1) throw new Error("reopen: expected 1 node, got " + texts().length);
if (texts()[0] === span1) throw new Error("reopen: same node returned — it was never truly unmounted");
if (texts()[0].textContent !== "Renamed") throw new Error("reopen: state did not survive unmount, got " + texts()[0].textContent);

console.log("data-kit-if: mount/unmount + dispose OK (absent -> mount -> relabel -> unmount+dispose -> fresh remount)");
`
	runNodeDOMScript(t, "if.test.js", assertions)
}

// data-kitwork-if is one of the eight grouped attributes a Kitwork UI page carries (KITWORK_UI.md
// §11): its value is expression SOURCE, exactly like data-kit-if (the control beside it), and a false
// condition takes the element out. What stays inert is every other data-kitwork-* name — a retired
// per-directive spelling (data-kitwork-text, now `text:` in data-kitwork-bind) and the transport
// markers the engine hangs on a page (generated, layout, current, delay, transition): their elements
// and contents stay exactly as written.
func TestKitIfReadsTheGroupedForm(t *testing.T) {
	const assertions = `
var grouped = el("div", { "data-kitwork-if": "count > 1" });
grouped.appendChild(el("span", { "data-if-probe": "grouped" }));
document.body.appendChild(grouped);
var legacy = el("div", { "data-kit-if": "count > 1" });
legacy.appendChild(el("span", { "data-if-probe": "legacy" }));
document.body.appendChild(legacy);
var text = el("b", { "data-kitwork-text": "'ir'" });
text.textContent = "authored";
document.body.appendChild(text);
var markers = el("div", { "data-kitwork-generated": "css", "data-kitwork-layout": "page.ui", "data-kitwork-current": "page",
  "data-kitwork-delay": "0", "data-kitwork-transition": "fade" });
markers.appendChild(el("span", { "data-marker-probe": "yes" }));
document.body.appendChild(markers);
function probes(kind) { return document.querySelectorAll('[data-if-probe="' + kind + '"]').length; }

window.kit.set("count", 0);
if (probes("legacy") !== 0) throw new Error("control: data-kit-if with a false condition must take its element out");
if (probes("grouped") !== 0) throw new Error("data-kitwork-if with a false condition must take its element out, as data-kit-if does");

window.kit.set("count", 2);
if (probes("legacy") !== 1) throw new Error("control: data-kit-if must mount when its condition turns true");
if (probes("grouped") !== 1) throw new Error("data-kitwork-if must mount when its condition turns true");

if (text.textContent !== "authored") throw new Error("data-kitwork-text is not a directive and must stay inert, got " + JSON.stringify(text.textContent));
if (!markers.parentNode || document.querySelectorAll("[data-marker-probe]").length !== 1) throw new Error("an element carrying transport markers must stay, with its content");
var kept = ["generated", "layout", "current", "delay", "transition"].filter(function (n) { return markers.hasAttribute("data-kitwork-" + n); });
if (kept.length !== 5) throw new Error("the kernel must not touch transport markers, kept " + kept.join(","));

console.log("data-kitwork-if: read as source like data-kit-if; data-kitwork-text and transport markers inert");
`
	runNodeDOMScript(t, "if-grouped.test.js", assertions)
}
