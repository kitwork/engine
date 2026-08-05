package hydrate

import "testing"

// Event modifiers, the shape the user chose: dedicated directives (data-kit-away / data-kit-escape,
// which change the event SOURCE) plus companion attributes (data-kit-guard / data-kit-debounce, which
// tune the actor's own event). This drives the REAL delegated listeners through the shim's event
// dispatch, and pins the discriminating behaviour of each — the thing a regression would break:
//
//   guard    → prevent calls preventDefault synchronously, AND the handler still runs;
//   away     → a click OUTSIDE fires; a click INSIDE does NOT (the whole point of click-away);
//   escape   → the Escape key fires; any other key does NOT;
//   debounce → the model write is DEFERRED (not applied on the keystroke) and a burst COALESCES to
//              the final value.
//
// Each check is disable-code-shaped: remove applyGuard → guard red; drop the inside-guard in away →
// inside click fires red; drop the key check in escape → 'a' fires red; skip debounced() → the model
// updates immediately and the deferral assert goes red.
func TestKitEventModifiers(t *testing.T) {
	const assertions = `
var kit = window.kit;

// ---- guard: data-kit-guard="prevent" on a click actor ----
var form = el("form", { "data-kit-scope": "n = 0" });
var btn = el("button", { "data-kit-click": "n = n + 1", "data-kit-guard": "prevent" });
form.appendChild(btn);
document.body.appendChild(form);
kit.render();

var clickEvt = { type: "click", target: btn, defaultPrevented: false,
  preventDefault: function () { this.defaultPrevented = true; },
  stopPropagation: function () { this._stopped = true; } };
document.dispatchEvent(clickEvt);
if (!clickEvt.defaultPrevented) throw new Error("guard: prevent did not call preventDefault");
if (kit.scopeFor(form).n !== 1) throw new Error("guard: click handler did not run, n = " + kit.scopeFor(form).n);

// ---- away: click OUTSIDE fires, click INSIDE does not ----
var menu = el("div", { "data-kit-away": "open = false", "data-kit-scope": "open = true" });
var link = el("a");
menu.appendChild(link);
document.body.appendChild(menu);
if (kit.scopeFor(menu).open !== true) throw new Error("away: initial open should be true");

document.dispatchEvent({ type: "click", target: link });   // inside the menu
if (kit.scopeFor(menu).open !== true) throw new Error("away: an inside click must NOT fire away");

document.dispatchEvent({ type: "click", target: document.body }); // outside the menu
if (kit.scopeFor(menu).open !== false) throw new Error("away: an outside click must fire away");

// ---- escape: Escape fires, another key does not ----
var modal = el("div", { "data-kit-escape": "open = false", "data-kit-scope": "open = true" });
document.body.appendChild(modal);
if (kit.scopeFor(modal).open !== true) throw new Error("escape: initial open should be true");

document.dispatchEvent({ type: "keydown", key: "a" });
if (kit.scopeFor(modal).open !== true) throw new Error("escape: a non-Escape key must NOT fire");

document.dispatchEvent({ type: "keydown", key: "Escape" });
if (kit.scopeFor(modal).open !== false) throw new Error("escape: the Escape key must fire");

// ---- debounce: model write deferred + coalesced to the final value ----
var box = el("input", { "data-kit-model": "q", "data-kit-debounce": "40", "data-kit-scope": "q = ''" });
box.value = "";
document.body.appendChild(box);
if (kit.scopeFor(box).q !== "") throw new Error("debounce: initial q should be empty");

box.value = "a";  document.dispatchEvent({ type: "input", target: box });
box.value = "ab"; document.dispatchEvent({ type: "input", target: box });
// Right now — before the debounce window elapses — the scope must be untouched.
if (kit.scopeFor(box).q !== "") throw new Error("debounce: model updated immediately, not deferred (q=" + kit.scopeFor(box).q + ")");

// After the window: exactly the final value, once (the burst coalesced).
setTimeout(function () {
  if (kit.scopeFor(box).q !== "ab") throw new Error("debounce: after settle expected 'ab', got '" + kit.scopeFor(box).q + "'");
  console.log("event modifiers: guard(prevent) + away(inside/outside) + escape(key) + debounce(defer/coalesce) OK");
}, 90);
`
	runNodeDOMScript(t, "events.test.js", assertions)
}
