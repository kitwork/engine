package hydrate

import "testing"

// Three modes, and the client must know the same three the server's pre-paint reads from
// localStorage (under "appearance" since 11/10/2026, "theme" before): light, dark, and system — the
// default, meaning "follow the OS". The service is kitwork.appearance (kitwork.theme until 11/10/2026; that
// name went on 26/10/2026).
//
// The kernel used to understand only two. A page could ask the server for system (by storing
// nothing) and then have no way to ask for it back, which is how kitjs.org ended up with an
// appearance page whose third button had nothing to call.
func TestAppearanceKnowsTheSameThreeModesAsThePrePaint(t *testing.T) {
	const assertions = `
var kitwork = window.kitwork;

// The block shim ships a localStorage that forgets everything, which would make every assertion
// below pass for the wrong reason. Give it a real one.
var store = {};
// …and a classList that remembers, for the same reason: the shim's is a set of no-ops, so
// "resolved" would always read light and every branch below would look correct.
var classes = {};
document.documentElement.classList = {
  add: function (c) { classes[c] = true; },
  remove: function (c) { delete classes[c]; },
  contains: function (c) { return !!classes[c]; },
  toggle: function (c, on) { if (on) classes[c] = true; else delete classes[c]; return !!classes[c]; }
};
window.localStorage = global.localStorage = {
  getItem: function (k) { return Object.prototype.hasOwnProperty.call(store, k) ? store[k] : null; },
  setItem: function (k, v) { store[k] = String(v); },
  removeItem: function (k) { delete store[k]; }
};

// Nothing stored: the CHOICE is system, and resolved is whatever is painted.
if (kitwork.appearance.mode !== "system") throw new Error("default choice should be system, got " + kitwork.appearance.mode);
if (kitwork.appearance.resolved !== "light" && kitwork.appearance.resolved !== "dark") {
  throw new Error("resolved must be a painted mode, got " + kitwork.appearance.resolved);
}

// An explicit choice is remembered as itself.
if (kitwork.appearance.set("dark") !== "dark" || kitwork.appearance.mode !== "dark" || kitwork.appearance.resolved !== "dark") {
  throw new Error("set('dark') did not stick: " + kitwork.appearance.mode + "/" + kitwork.appearance.resolved);
}
if (localStorage.getItem("appearance") !== "dark") throw new Error("the choice should be stored as written, under \"appearance\"");
if (document.documentElement.getAttribute("data-theme") !== "dark") {
  throw new Error("data-theme should carry what is shown, like the pre-paint writes it");
}

// Handing it back: the stored value becomes the word the pre-paint reads on the next load.
if (kitwork.appearance.system() !== "system" || kitwork.appearance.mode !== "system") {
  throw new Error("system() did not hand the choice back: " + kitwork.appearance.mode);
}
if (localStorage.getItem("appearance") !== "system") throw new Error("stored value should be 'system', got " + localStorage.getItem("appearance"));
if (kitwork.appearance.resolved !== "light" && kitwork.appearance.resolved !== "dark") {
  throw new Error("resolved must still be a painted mode under system");
}

// A word that is not one of the three is refused rather than silently stored.
var refused = false;
try { kitwork.appearance.set("sepia"); } catch (e) { refused = true; }
if (!refused) throw new Error("an unknown mode must be refused");
if (localStorage.getItem("appearance") !== "system") throw new Error("a refused set must not change what is stored");

console.log("appearance: light / dark / system, the same three the pre-paint reads");
`
	runNodeDOMScript(t, "theme_system.test.js", assertions)
}

// The service knowing three modes is only half of it: markup once reached the service through the
// expression grant, a separate list, and a member missing from that list read as undefined. So the
// third mode existed on the runtime while `data-kit-click="kitwork.theme.system()"` threw — which is
// the same page kitjs.org had, one layer down. Markup has no kit now (03/10), so what has to know all
// three is the service's own surface, which trusted JavaScript and the appearance component call.
func TestAppearanceServiceKnowsAllThreeModes(t *testing.T) {
	const assertions = `
var kitwork = window.kitwork;
var store = {};
var classes = {};
document.documentElement.classList = {
  add: function (c) { classes[c] = true; },
  remove: function (c) { delete classes[c]; },
  contains: function (c) { return !!classes[c]; },
  toggle: function (c, on) { if (on) classes[c] = true; else delete classes[c]; return !!classes[c]; }
};
window.localStorage = global.localStorage = {
  getItem: function (k) { return Object.prototype.hasOwnProperty.call(store, k) ? store[k] : null; },
  setItem: function (k, v) { store[k] = String(v); },
  removeItem: function (k) { delete store[k]; }
};

// JavaScript calls the service; markup reaches it through the appearance component (jit/js).
if (kitwork.appearance.set('dark') !== "dark") throw new Error("the service cannot set dark");
if (kitwork.appearance.set('light') !== "light") throw new Error("the service cannot set light");
if (kitwork.appearance.system() !== "system") throw new Error("the service cannot hand the choice back to the system");
if (kitwork.appearance.mode !== "system") throw new Error("the service cannot read the choice back");
if (kitwork.appearance.toggle() !== "dark" && kitwork.appearance.mode !== "light") throw new Error("the service cannot toggle");

console.log("the appearance service knows light, dark and system");
`
	runNodeDOMScript(t, "theme_service_modes.test.js", assertions)
}
