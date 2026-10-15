package hydrate

import "testing"

// appearanceShim gives the node DOM shim a localStorage that remembers, a classList that remembers
// and an operating system whose preference the test sets. The shim's own are no-ops (storage forgets,
// classList.contains is always false), which would let every assertion below pass for the wrong
// reason.
const appearanceShim = `
var kit = window.kit;
var store = {};
var classes = {};
var systemDark = false;
document.documentElement.classList = {
  add: function (c) { classes[c] = true; },
  remove: function (c) { delete classes[c]; },
  contains: function (c) { return !!classes[c]; },
  toggle: function (c, on) { if (on) classes[c] = true; else delete classes[c]; return !!classes[c]; }
};
window.localStorage = global.localStorage = {
  getItem: function (k) { return Object.prototype.hasOwnProperty.call(store, k) ? store[k] : null; },
  setItem: function (k, v) { store[k] = String(v); },
  removeItem: function (k) { delete store[k]; },
  clear: function () { store = {}; }
};
window.matchMedia = global.matchMedia = function (query) {
  return { matches: systemDark && query === "(prefers-color-scheme: dark)", media: query };
};
`

// router.theme() became router.appearance() (11/10/2026), and the light/dark service with it:
// kit.appearance. kit.theme is not a second service that could drift from it — it is the same object,
// so a page written for kit.theme, and the theme component, read and change the one choice.
func TestAppearanceServiceIsKitThemeItself(t *testing.T) {
	const assertions = appearanceShim + `
if (!kit.appearance || typeof kit.appearance.toggle !== "function") throw new Error("kit.appearance is missing");
if (kit.theme !== kit.appearance) throw new Error("kit.theme must be the same object as kit.appearance, not a copy");
if (kit.service("theme") !== kit.service("appearance")) throw new Error("the service registry must hold one object under both names");

// One choice, seen through either name.
kit.theme.set("dark");
if (kit.appearance.mode !== "dark" || kit.appearance.dark !== true) {
  throw new Error("a choice made through kit.theme must be kit.appearance's: " + kit.appearance.mode + "/" + kit.appearance.dark);
}
kit.appearance.set("light");
if (kit.theme.mode !== "light" || kit.theme.resolved !== "light") {
  throw new Error("a choice made through kit.appearance must be kit.theme's: " + kit.theme.mode + "/" + kit.theme.resolved);
}
// toggleTheme(), the oldest spelling, still flips the same choice.
kit.toggleTheme();
if (kit.appearance.mode !== "dark") throw new Error("toggleTheme() must flip kit.appearance, got " + kit.appearance.mode);

// The service answers for its own name in capabilities, without asking a native shell.
Promise.all([
  kit.capabilities.supports("appearance.toggle"),
  kit.capabilities.supports("appearance.dark"),
  kit.capabilities.supports("theme.toggle"),
  kit.capabilities.supports("appearance.nope")
]).then(function (answers) {
  if (answers[0] !== true || answers[1] !== true || answers[2] !== true) {
    throw new Error("capabilities must know the service under both names: " + answers.join(","));
  }
  // control: an id nobody declared is still refused, so the yeses above are not a blanket yes
  if (answers[3] !== false) throw new Error("an undeclared capability id must answer false");
  console.log("kit.theme is kit.appearance");
}).catch(function (error) { console.error(error && error.message || error); process.exit(1); });
`
	runNodeDOMScript(t, "appearance_is_theme.test.js", assertions)
}

// dark is what the page SHOWS, not what was chosen: under "system" it is the operating system's
// answer, and it agrees with the dark class on <html> — the class the pre-paint and the stylesheet
// both go by.
func TestAppearanceDarkFollowsWhatIsShown(t *testing.T) {
	const assertions = appearanceShim + `
function check(label, mode, dark) {
  if (kit.appearance.mode !== mode) throw new Error(label + ": mode = " + kit.appearance.mode + ", want " + mode);
  if (kit.appearance.dark !== dark) throw new Error(label + ": dark = " + kit.appearance.dark + ", want " + dark);
  if (document.documentElement.classList.contains("dark") !== dark) throw new Error(label + ": the dark class disagrees with dark");
  if (kit.appearance.resolved !== (dark ? "dark" : "light")) throw new Error(label + ": resolved = " + kit.appearance.resolved);
  // data-theme carries what is SHOWN, as the pre-paint writes it — never "system", which a site that
  // scopes its dark under [data-theme="dark"] would paint light on a dark device.
  var attribute = document.documentElement.getAttribute("data-theme");
  if (attribute !== (dark ? "dark" : "light")) throw new Error(label + ": data-theme = " + attribute + ", want what is shown");
}

systemDark = true;
kit.appearance.system();
check("system, OS dark", "system", true);

systemDark = false;
kit.appearance.system();
check("system, OS light", "system", false);

// An explicit choice is shown whatever the OS says.
systemDark = true;
kit.appearance.set("light");
check("light on a dark OS", "light", false);
systemDark = false;
kit.appearance.set("dark");
check("dark on a light OS", "dark", true);

// toggle() flips what is shown, and dark follows it.
kit.appearance.toggle();
check("toggled from dark", "light", false);
kit.appearance.toggle();
check("toggled from light", "dark", true);

// dark reads the page, not the store: the pre-paint put the class there before the kernel ran.
store = {};
document.documentElement.classList.add("dark");
if (kit.appearance.mode !== "system" || kit.appearance.dark !== true) {
  throw new Error("with nothing stored and a dark page, dark must be true: " + kit.appearance.mode + "/" + kit.appearance.dark);
}
document.documentElement.classList.remove("dark");
if (kit.appearance.dark !== false) throw new Error("a light page must read dark = false");

console.log("dark follows what is shown");
`
	runNodeDOMScript(t, "appearance_dark.test.js", assertions)
}

// The choice moved from localStorage "theme" to "appearance". A visitor who chose before the move
// keeps the choice: "appearance" is read first, "theme" only when "appearance" holds none of the
// three words — the rule the pre-paint uses too. A new choice is written under "appearance".
func TestAppearanceReadsTheNewKeyThenTheOldOne(t *testing.T) {
	const assertions = appearanceShim + `
function modeWith(saved) { store = saved; return kit.appearance.mode; }

if (modeWith({}) !== "system") throw new Error("nothing stored: system");
if (modeWith({ theme: "dark" }) !== "dark") throw new Error("the old key alone must still be honoured, got " + kit.appearance.mode);
if (modeWith({ theme: "Light" }) !== "light") throw new Error("the old key is read case-insensitively, got " + kit.appearance.mode);
if (modeWith({ appearance: "dark" }) !== "dark") throw new Error("the new key alone, got " + kit.appearance.mode);
if (modeWith({ appearance: "light", theme: "dark" }) !== "light") throw new Error("the new key must win over the old, got " + kit.appearance.mode);
if (modeWith({ appearance: "system", theme: "dark" }) !== "system") throw new Error("system under the new key must win over an old explicit choice, got " + kit.appearance.mode);
if (modeWith({ appearance: "sepia", theme: "dark" }) !== "dark") throw new Error("a word that is not a choice under the new key must fall back to the old, got " + kit.appearance.mode);
if (modeWith({ appearance: "sepia" }) !== "system") throw new Error("no choice under either key: system, got " + kit.appearance.mode);

// Writing goes to "appearance" alone; the old key is left as it was (it no longer decides anything).
store = { theme: "dark" };
kit.appearance.set("light");
if (store.appearance !== "light") throw new Error("set() must store under \"appearance\", got " + JSON.stringify(store));
if (store.theme !== "dark") throw new Error("set() must not rewrite the old key, got " + JSON.stringify(store));
if (kit.appearance.mode !== "light") throw new Error("after set('light') the new key decides, got " + kit.appearance.mode);
kit.appearance.system();
if (store.appearance !== "system" || kit.appearance.mode !== "system") {
  throw new Error("system() must store \"system\" under the new key and win over the old dark: " + JSON.stringify(store));
}

console.log("appearance key first, theme key as the fallback");
`
	runNodeDOMScript(t, "appearance_keys.test.js", assertions)
}

// router.appearance(mode) is the mode a page shows BEFORE the visitor chooses. The server writes it on
// <html data-kitwork-appearance> (nothing for "system", the default), and kit.appearance reads it
// only when nothing is stored: a stored choice — under either key — beats it, and system() stores
// "system", so a visitor follows the device even on a site whose default is dark.
func TestAppearanceSiteDefaultAppliesOnlyWhenNothingIsStored(t *testing.T) {
	const assertions = appearanceShim + `
var root = document.documentElement;
function modeWith(siteDefault, saved) {
  if (siteDefault == null) root.removeAttribute("data-kitwork-appearance");
  else root.setAttribute("data-kitwork-appearance", siteDefault);
  store = saved;
  return kit.appearance.mode;
}
function expect(got, want, why) { if (got !== want) throw new Error(why + ": got " + got + ", want " + want); }

// control: no default on the page, nothing stored — system, as before the default existed
expect(modeWith(null, {}), "system", "no site default, nothing stored");

// the default decides only when nothing is stored
expect(modeWith("dark", {}), "dark", "nothing stored: the site's dark default");
expect(modeWith("light", {}), "light", "nothing stored: the site's light default");
expect(modeWith("Dark", {}), "dark", "the default is read case-insensitively, like a stored word");
expect(modeWith("dark", { appearance: "sepia" }), "dark", "a word that is not a choice is no choice: the default");
expect(modeWith("sepia", {}), "system", "a default that is not a mode is ignored");
expect(modeWith("system", {}), "system", "a default of system is system");

// a stored choice beats it
expect(modeWith("dark", { appearance: "light" }), "light", "a stored light beats a dark default");
expect(modeWith("light", { appearance: "dark" }), "dark", "a stored dark beats a light default");
expect(modeWith("dark", { theme: "light" }), "light", "a choice under the old key beats the default too");
expect(modeWith("dark", { appearance: "system" }), "system", "a stored system beats a dark default");

// system() on a dark-default site: "system" is stored (not the store emptied), so the device decides
systemDark = false;
modeWith("dark", {});
root.classList.add("dark"); // the pre-paint painted the site's dark default
if (kit.appearance.dark !== true) throw new Error("the page painted dark: dark must be true");
kit.appearance.system();
expect(store.appearance, "system", "system() must store the word, not empty the store");
expect(kit.appearance.mode, "system", "after system() on a dark-default site");
if (kit.appearance.dark !== false || root.classList.contains("dark")) {
  throw new Error("system() on a dark-default site with a light device must show light");
}

// toggle() from the site's default flips what is shown and stores the visitor's choice
modeWith("dark", {});
root.classList.add("dark");
kit.appearance.toggle();
expect(store.appearance, "light", "toggle() from a dark default stores light");
expect(kit.appearance.mode, "light", "after toggle() from a dark default");

// What Drive calls after a move whose page says another default: the rule is applied again.
var web = kit.module("web");
if (!web || typeof web.showAppearance !== "function") throw new Error("the web module must offer showAppearance to Drive");
systemDark = false;
modeWith("dark", {});
root.classList.remove("dark");
web.showAppearance();
if (!root.classList.contains("dark") || root.getAttribute("data-theme") !== "dark") throw new Error("showAppearance with a dark default and nothing stored must show dark");
modeWith(null, {});
web.showAppearance();
if (root.classList.contains("dark") || root.getAttribute("data-theme") !== "light") throw new Error("showAppearance with no default on a light device must show light");
// control: a stored choice is shown as it was, whatever the page's default
modeWith("light", { appearance: "dark" });
web.showAppearance();
if (!root.classList.contains("dark")) throw new Error("showAppearance must keep a stored dark over a light default");

console.log("the site's default applies only when nothing is stored");
`
	runNodeDOMScript(t, "appearance_site_default.test.js", assertions)
}
