package hydrate

import "testing"

// The event family with the fixed modifier pipeline of ideaship-final §4 — target → filter →
// prevent → stop → timing → once → run — driven through the REAL delegated listeners by the shim's
// event dispatch. Each check pins the discriminating behaviour a regression would break:
//
//	:prevent          → preventDefault is called synchronously AND the handler still runs;
//	:outside          → a click OUTSIDE fires; a click INSIDE does NOT;
//	:escape :window   → the Escape key fires wherever focus is; another key does NOT — and a
//	                    failed filter does NOT swallow the event (no preventDefault);
//	:enter            → the Enter key fires on the element's own keydown;
//	:stop             → the walk up the ancestors ends at the element that stopped;
//	:once             → the second event does not run;
//	:throttle(n)      → a burst inside the window runs once, the leading edge;
//	unknown modifier  → the handler is disabled, not misfired;
//	data-kit-debounce → still the model input's own coalescing, deferred to the final value.
func TestKitEventModifiers(t *testing.T) {
	const assertions = `
var kit = window.kit;
function evt(type, target, extra) {
  var e = { type: type, target: target, defaultPrevented: false, _stopped: false,
    preventDefault: function () { this.defaultPrevented = true; },
    stopPropagation: function () { this._stopped = true; } };
  if (extra) for (var k in extra) e[k] = extra[k];
  return e;
}

// ---- :prevent — preventDefault, then the handler still runs ----
var form = el("form", { "data-kit-scope": "n: 0" });
var btn = el("button", { "data-kit-click:prevent": "n = n + 1" });
form.appendChild(btn);
document.body.appendChild(form);
kit.render();
var clickEvt = evt("click", btn);
document.dispatchEvent(clickEvt);
if (!clickEvt.defaultPrevented) throw new Error(":prevent did not call preventDefault");
if (kit.scopeFor(form).n !== 1) throw new Error(":prevent: handler did not run, n = " + kit.scopeFor(form).n);

// ---- :outside — a click outside fires, a click inside does not ----
var menu = el("div", { "data-kit-click:outside": "open = false", "data-kit-scope": "open: true" });
var link = el("a");
menu.appendChild(link);
document.body.appendChild(menu);
document.dispatchEvent(evt("click", link));
if (kit.scopeFor(menu).open !== true) throw new Error(":outside: an inside click must NOT fire");
document.dispatchEvent(evt("click", document.body));
if (kit.scopeFor(menu).open !== false) throw new Error(":outside: an outside click must fire");

// ---- :escape:window — filter first: another key neither fires nor is swallowed ----
var modal = el("div", { "data-kit-keydown:escape:window:prevent": "open = false", "data-kit-scope": "open: true" });
document.body.appendChild(modal);
var other = evt("keydown", document.body, { key: "a" });
document.dispatchEvent(other);
if (kit.scopeFor(modal).open !== true) throw new Error(":escape: a non-Escape key must NOT fire");
if (other.defaultPrevented) throw new Error("a failed filter must not swallow the event (preventDefault ran before the filter)");
var esc = evt("keydown", document.body, { key: "Escape" });
document.dispatchEvent(esc);
if (kit.scopeFor(modal).open !== false) throw new Error(":escape:window: the Escape key must fire wherever focus is");
if (!esc.defaultPrevented) throw new Error(":prevent should run once the filter passed");

// ---- :enter on the element's own keydown ----
var field = el("input", { "data-kit-keydown:enter": "sent = sent + 1", "data-kit-scope": "sent: 0" });
document.body.appendChild(field);
document.dispatchEvent(evt("keydown", field, { key: "Enter" }));
document.dispatchEvent(evt("keydown", field, { key: "x" }));
if (kit.scopeFor(field).sent !== 1) throw new Error(":enter should fire for Enter only, sent = " + kit.scopeFor(field).sent);

// ---- :stop ends the walk; without it the ancestor also runs ----
var outer = el("div", { "data-kit-click": "hits = hits + 'outer,'", "data-kit-scope": "hits: ''" });
var inner = el("button", { "data-kit-click:stop": "hits = hits + 'inner,'" });
var plain = el("button", { "data-kit-click": "hits = hits + 'plain,'" });
outer.appendChild(inner); outer.appendChild(plain);
document.body.appendChild(outer);
document.dispatchEvent(evt("click", inner));
if (kit.scopeFor(outer).hits !== "inner,") throw new Error(":stop should end the walk at the inner button, hits = " + kit.scopeFor(outer).hits);
document.dispatchEvent(evt("click", plain));
if (kit.scopeFor(outer).hits !== "inner,plain,outer,") throw new Error("without :stop the ancestor runs after the target, hits = " + kit.scopeFor(outer).hits);

// ---- :self — only when the event landed on the element itself, not on a child ----
var selfBox = el("div", { "data-kit-click:self": "own = own + 1", "data-kit-scope": "own: 0" });
var selfChild = el("span");
selfBox.appendChild(selfChild);
document.body.appendChild(selfBox);
document.dispatchEvent(evt("click", selfChild));
if (kit.scopeFor(selfBox).own !== 0) throw new Error(":self must not fire for a click on a child");
document.dispatchEvent(evt("click", selfBox));
if (kit.scopeFor(selfBox).own !== 1) throw new Error(":self should fire for a click on the element itself, own = " + kit.scopeFor(selfBox).own);

// ---- :once ----
var once = el("button", { "data-kit-click:once": "count = count + 1", "data-kit-scope": "count: 0" });
document.body.appendChild(once);
document.dispatchEvent(evt("click", once));
document.dispatchEvent(evt("click", once));
if (kit.scopeFor(once).count !== 1) throw new Error(":once should run a single time, count = " + kit.scopeFor(once).count);

// ---- :throttle(n) — leading edge, then quiet for n ms ----
var burst = el("button", { "data-kit-click:throttle(500)": "ticks = ticks + 1", "data-kit-scope": "ticks: 0" });
document.body.appendChild(burst);
document.dispatchEvent(evt("click", burst));
document.dispatchEvent(evt("click", burst));
document.dispatchEvent(evt("click", burst));
if (kit.scopeFor(burst).ticks !== 1) throw new Error(":throttle should run the leading edge once inside the window, ticks = " + kit.scopeFor(burst).ticks);

// ---- an unknown modifier disables the handler ----
var odd = el("button", { "data-kit-click:mystery": "n = n + 1" });
form.appendChild(odd);
document.dispatchEvent(evt("click", odd));
if (kit.scopeFor(form).n !== 1) throw new Error("an unknown modifier must disable the handler, n = " + kit.scopeFor(form).n);

// ---- data-kit-debounce on a model input: deferred, coalesced ----
var box = el("input", { "data-kit-model": "q", "data-kit-debounce": "40", "data-kit-scope": "q: ''" });
box.value = "";
document.body.appendChild(box);
box.value = "a";  document.dispatchEvent(evt("input", box));
box.value = "ab"; document.dispatchEvent(evt("input", box));
if (kit.scopeFor(box).q !== "") throw new Error("model debounce: updated immediately, not deferred (q=" + kit.scopeFor(box).q + ")");
setTimeout(function () {
  if (kit.scopeFor(box).q !== "ab") throw new Error("model debounce: after settle expected 'ab', got '" + kit.scopeFor(box).q + "'");
  console.log("event pipeline: prevent, outside, escape:window (filter first), enter, stop, once, throttle, unknown-disabled, model debounce OK");
}, 90);
`
	runNodeDOMScript(t, "events.test.js", assertions)
}
