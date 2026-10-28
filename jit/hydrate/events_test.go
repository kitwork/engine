package hydrate

import "testing"

// The event family with the fixed modifier pipeline of ideaship-final §4 — target → filter →
// prevent → stop → timing → once → run — driven through the REAL delegated listeners by the shim's
// event dispatch, in data-kitwork-event's spelling (event.modifier: expr). Each check pins the
// discriminating behaviour a regression would break:
//
//	.prevent          → preventDefault is called synchronously AND the handler still runs;
//	click-outside     → a click OUTSIDE fires; a click INSIDE does NOT;
//	.escape .window   → the Escape key fires wherever focus is; another key does NOT — and a
//	                    failed filter does NOT swallow the event (no preventDefault);
//	.enter            → the Enter key fires on the element's own keydown;
//	.stop             → the walk up the ancestors ends at the element that stopped;
//	.self             → only a click on the element itself, not on a child;
//	.once             → the second event does not run;
//	.throttle(n)      → a burst inside the window runs once, the leading edge;
//	unknown modifier  → the handler is disabled, not misfired.
//
// (data-kit-debounce, a two-way field's own coalescing, went with the hand-written form on
// 26/10/2026; an event's .debounce(n) is in grouped_dom_test.go.)
func TestKitEventModifiers(t *testing.T) {
	const assertions = `
var kitwork = window.kitwork;
function evt(type, target, extra) {
  var e = { type: type, target: target, defaultPrevented: false, _stopped: false,
    preventDefault: function () { this.defaultPrevented = true; },
    stopPropagation: function () { this._stopped = true; } };
  if (extra) for (var k in extra) e[k] = extra[k];
  return e;
}

// ---- .prevent — preventDefault, then the handler still runs ----
var form = el("form", { "data-kitwork-scope": "n: 0" });
var btn = el("button", { "data-kitwork-event": "click.prevent: n = n + 1" });
form.appendChild(btn);
document.body.appendChild(form);
kitwork.render();
var clickEvt = evt("click", btn);
document.dispatchEvent(clickEvt);
if (!clickEvt.defaultPrevented) throw new Error(".prevent did not call preventDefault");
if (kitwork.scopeFor(form).n !== 1) throw new Error(".prevent: handler did not run, n = " + kitwork.scopeFor(form).n);

// ---- click-outside — a click outside fires, a click inside does not ----
var menu = el("div", { "data-kitwork-event": "click-outside: open = false", "data-kitwork-scope": "open: true" });
var link = el("a");
menu.appendChild(link);
document.body.appendChild(menu);
document.dispatchEvent(evt("click", link));
if (kitwork.scopeFor(menu).open !== true) throw new Error("click-outside: an inside click must NOT fire");
document.dispatchEvent(evt("click", document.body));
if (kitwork.scopeFor(menu).open !== false) throw new Error("click-outside: an outside click must fire");

// ---- .escape.window — filter first: another key neither fires nor is swallowed ----
var modal = el("div", { "data-kitwork-event": "keydown.escape.window.prevent: open = false", "data-kitwork-scope": "open: true" });
document.body.appendChild(modal);
var other = evt("keydown", document.body, { key: "a" });
document.dispatchEvent(other);
if (kitwork.scopeFor(modal).open !== true) throw new Error(".escape: a non-Escape key must NOT fire");
if (other.defaultPrevented) throw new Error("a failed filter must not swallow the event (preventDefault ran before the filter)");
var esc = evt("keydown", document.body, { key: "Escape" });
document.dispatchEvent(esc);
if (kitwork.scopeFor(modal).open !== false) throw new Error(".escape.window: the Escape key must fire wherever focus is");
if (!esc.defaultPrevented) throw new Error(".prevent should run once the filter passed");

// ---- .enter on the element's own keydown ----
var field = el("input", { "data-kitwork-event": "keydown.enter: sent = sent + 1", "data-kitwork-scope": "sent: 0" });
document.body.appendChild(field);
document.dispatchEvent(evt("keydown", field, { key: "Enter" }));
document.dispatchEvent(evt("keydown", field, { key: "x" }));
if (kitwork.scopeFor(field).sent !== 1) throw new Error(".enter should fire for Enter only, sent = " + kitwork.scopeFor(field).sent);

// ---- .stop ends the walk; without it the ancestor also runs ----
var outer = el("div", { "data-kitwork-event": "click: hits = hits + 'outer,'", "data-kitwork-scope": "hits: ''" });
var inner = el("button", { "data-kitwork-event": "click.stop: hits = hits + 'inner,'" });
var plain = el("button", { "data-kitwork-event": "click: hits = hits + 'plain,'" });
outer.appendChild(inner); outer.appendChild(plain);
document.body.appendChild(outer);
document.dispatchEvent(evt("click", inner));
if (kitwork.scopeFor(outer).hits !== "inner,") throw new Error(".stop should end the walk at the inner button, hits = " + kitwork.scopeFor(outer).hits);
document.dispatchEvent(evt("click", plain));
if (kitwork.scopeFor(outer).hits !== "inner,plain,outer,") throw new Error("without .stop the ancestor runs after the target, hits = " + kitwork.scopeFor(outer).hits);

// ---- .self — only when the event landed on the element itself, not on a child ----
var selfBox = el("div", { "data-kitwork-event": "click.self: own = own + 1", "data-kitwork-scope": "own: 0" });
var selfChild = el("span");
selfBox.appendChild(selfChild);
document.body.appendChild(selfBox);
document.dispatchEvent(evt("click", selfChild));
if (kitwork.scopeFor(selfBox).own !== 0) throw new Error(".self must not fire for a click on a child");
document.dispatchEvent(evt("click", selfBox));
if (kitwork.scopeFor(selfBox).own !== 1) throw new Error(".self should fire for a click on the element itself, own = " + kitwork.scopeFor(selfBox).own);

// ---- .once ----
var once = el("button", { "data-kitwork-event": "click.once: count = count + 1", "data-kitwork-scope": "count: 0" });
document.body.appendChild(once);
document.dispatchEvent(evt("click", once));
document.dispatchEvent(evt("click", once));
if (kitwork.scopeFor(once).count !== 1) throw new Error(".once should run a single time, count = " + kitwork.scopeFor(once).count);

// ---- .throttle(n) — leading edge, then quiet for n ms ----
var burst = el("button", { "data-kitwork-event": "click.throttle(500): ticks = ticks + 1", "data-kitwork-scope": "ticks: 0" });
document.body.appendChild(burst);
document.dispatchEvent(evt("click", burst));
document.dispatchEvent(evt("click", burst));
document.dispatchEvent(evt("click", burst));
if (kitwork.scopeFor(burst).ticks !== 1) throw new Error(".throttle should run the leading edge once inside the window, ticks = " + kitwork.scopeFor(burst).ticks);

// ---- an unknown modifier disables the handler ----
var odd = el("button", { "data-kitwork-event": "click.mystery: n = n + 1" });
form.appendChild(odd);
document.dispatchEvent(evt("click", odd));
if (kitwork.scopeFor(form).n !== 1) throw new Error("an unknown modifier must disable the handler, n = " + kitwork.scopeFor(form).n);

console.log("event pipeline: prevent, click-outside, escape.window (filter first), enter, stop, self, once, throttle, unknown-disabled OK");
`
	runNodeDOMScript(t, "events.test.js", assertions)
}
