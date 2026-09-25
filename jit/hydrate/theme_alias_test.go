package hydrate

import (
	"strings"
	"testing"
)

// $theme was the last splinter of the deleted theme component: a hand-installed alias carrying one
// method, toggle(). It made a second name for kit.theme, and an incomplete one — studio calls
// $theme.set('light') and $theme.system(), neither of which the splinter had, so the site looked
// wired and did nothing. One name for one thing: the service.
//
// Removing it also gives $theme back to authors. It was reserved, so no site could even name a
// component handle that; now it is an ordinary alias like any other.
func TestThemeAliasIsGoneAndTheNameIsFreeAgain(t *testing.T) {
	const assertions = `
var kit = window.kit;

// Nothing is installed under the name. Note the kernel reads an UNDECLARED name as 0, not
// undefined, so the honest assertion is that $theme now reads like any name nobody declared —
// compared against a name invented here, so the test cannot pass by both sides being broken.
var invented = kit.run(kit.compile("$nobodyDeclaredThis"));
if (kit.run(kit.compile("$theme")) !== invented) {
  throw new Error("$theme does not read like an undeclared name — the kernel still installs it");
}
if (typeof kit.run(kit.compile("$theme.toggle")) === "function") throw new Error("$theme.toggle is still callable");

// The control: the service it used to shadow is reachable, so the two assertions above are about
// $theme being gone, not about expressions being broken.
if (typeof kit.run(kit.compile("kit.theme.toggle")) !== "function") {
  throw new Error("precondition: kit.theme.toggle is reachable from markup");
}

// And the name is an ordinary one now — a page may claim it for its own component. (The shim
// builds nodes; it does not parse HTML, so the host is assembled rather than written as markup.)
var host = el("div", { "data-kit-component": "appearance", "data-kit-alias": "$theme" });
host.appendChild(el("span", { "data-kit-text": "label" }));
document.body.appendChild(host);
kit.component("appearance", { label: "claimed" });
kit.render();
if (host.childNodes[0].textContent !== "claimed") {
  throw new Error("a component could not mount under the freed name: " + host.childNodes[0].textContent);
}
if (kit.run(kit.compile("$theme.label")) !== "claimed") {
  throw new Error("the freed alias did not register as an ordinary handle");
}

console.log("$theme is gone, and the name belongs to authors");
`
	runNodeDOMScript(t, "theme_alias.test.js", assertions)
}

// The undeclared-name scanner skips every name starting with "$" before it consults its reserved
// list, so the seven $-names that list carried could never be reached. Keeping them said the list
// was load-bearing where it was not — and one of them, $theme, is now a name the kernel does not
// install at all.
func TestReservedReadNamesCarriesOnlyNamesThatCanBeReached(t *testing.T) {
	for name := range reservedReadNames {
		if strings.HasPrefix(name, "$") {
			t.Errorf("reservedReadNames lists %q, but names starting with $ are skipped before the list is read", name)
		}
	}
	// The control: the list is not simply empty — the names that cannot start with $ are still there.
	for _, name := range []string{"kit", "count", "first", "last", "even", "odd"} {
		if !reservedReadNames[name] {
			t.Errorf("reservedReadNames lost %q, which the scanner does need", name)
		}
	}
}
