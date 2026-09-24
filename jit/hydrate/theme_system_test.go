package hydrate

import "testing"

// Three modes, and the client must know the same three the server's pre-paint has always read from
// localStorage["theme"]: light, dark, and system — the default, meaning "follow the OS".
//
// The kernel used to understand only two. A page could ask the server for system (by storing
// nothing) and then have no way to ask for it back, which is how kitjs.org ended up with an
// appearance page whose third button had nothing to call.
func TestThemeKnowsTheSameThreeModesAsThePrePaint(t *testing.T) {
	const assertions = `
var kit = window.kit;

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
if (kit.theme.mode !== "system") throw new Error("default choice should be system, got " + kit.theme.mode);
if (kit.theme.resolved !== "light" && kit.theme.resolved !== "dark") {
  throw new Error("resolved must be a painted mode, got " + kit.theme.resolved);
}

// An explicit choice is remembered as itself.
if (kit.theme.set("dark") !== "dark" || kit.theme.mode !== "dark" || kit.theme.resolved !== "dark") {
  throw new Error("set('dark') did not stick: " + kit.theme.mode + "/" + kit.theme.resolved);
}
if (localStorage.getItem("theme") !== "dark") throw new Error("the choice should be stored as written");
if (document.documentElement.getAttribute("data-theme") !== "dark") {
  throw new Error("data-theme should carry the choice, like the pre-paint writes it");
}

// Handing it back: the stored value becomes the word the pre-paint reads on the next load.
if (kit.theme.system() !== "system" || kit.theme.mode !== "system") {
  throw new Error("system() did not hand the choice back: " + kit.theme.mode);
}
if (localStorage.getItem("theme") !== "system") throw new Error("stored value should be 'system', got " + localStorage.getItem("theme"));
if (kit.theme.resolved !== "light" && kit.theme.resolved !== "dark") {
  throw new Error("resolved must still be a painted mode under system");
}

// A word that is not one of the three is refused rather than silently stored.
var refused = false;
try { kit.theme.set("sepia"); } catch (e) { refused = true; }
if (!refused) throw new Error("an unknown mode must be refused");
if (localStorage.getItem("theme") !== "system") throw new Error("a refused set must not change what is stored");

console.log("theme: light / dark / system, the same three the pre-paint reads");
`
	runNodeDOMScript(t, "theme_system.test.js", assertions)
}
