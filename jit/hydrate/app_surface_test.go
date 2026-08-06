package hydrate

import "testing"

// $app is a curated, DEFAULT-DENY capability surface, not the whole window.kit. Markup (increasingly
// agent-authored) must reach the granted capabilities and NOTHING else — the runtime's own control API
// (destroy/internal/render/run/compile/component/module/scope) is invisible through $app. This is the
// encapsulation the "$app as a component" change buys; before it, $app === kit exposed everything.
func TestAppSurfaceGrantsCapabilitiesDeniesInternals(t *testing.T) {
	const assertions = `
var kit = window.kit;

// a granted capability delegates to kit's own method, with this bound to kit
kit.toggleTheme = function () { return this === kit ? "this=kit" : "wrong-this"; };
var app = kit.scope.$app;
if (typeof app.toggleTheme !== "function") throw new Error("granted capability $app.toggleTheme must be reachable");
if (app.toggleTheme() !== "this=kit") throw new Error("capability this-binding wrong: " + app.toggleTheme());

// runtime internals exist on kit but must be DENIED through $app
if (typeof kit.destroy !== "function") throw new Error("precondition: kit.destroy exists on the runtime root");
["destroy", "internal", "render", "run", "compile", "component", "module", "scope", "has"].forEach(function (k) {
  if (app[k] !== undefined) throw new Error("$app." + k + " leaked a runtime internal (got " + typeof app[k] + ")");
});

console.log("$app surface: granted capability reachable (this=kit), runtime internals denied");
`
	runNodeDOMScript(t, "app_surface.test.js", assertions)
}

// data-kit-alias registers a component's scope as a global handle (the clean attribute form of the
// name=$alias syntax) — the mechanism $app itself now rides.
func TestDataKitAliasRegistersHandle(t *testing.T) {
	const assertions = `
var kit = window.kit;
kit.component("aliasdemo", { label: "hi" });
var box = el("div", { "data-kit-component": "aliasdemo", "data-kit-alias": "$box" });
box.appendChild(el("b", { "data-kit-text": "label" })); // a child directive forces the boundary to seed
document.body.appendChild(box);
kit.render();

if (typeof kit.scope.$box !== "object") throw new Error("data-kit-alias did not register a handle (got " + typeof kit.scope.$box + ")");
if (kit.scope.$box.label !== "hi") throw new Error("$box handle should reach the component scope, got " + kit.scope.$box.label);

console.log("data-kit-alias: $box → the component scope, reachable globally");
`
	runNodeDOMScript(t, "data_kit_alias.test.js", assertions)
}
