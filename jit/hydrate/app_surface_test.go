package hydrate

import "testing"

// Markup has no kit (03/10). Trusted JavaScript owns the whole window.kit root; an expression gets
// nothing under that name — it reads like any name nobody declared — so no service, no runtime
// control and no raw bridge is reachable from markup. A page reaches a service through the component
// that is its view (theme, progressbar, titlebar, clipboard). `$app` is not installed by the kernel.
func TestMarkupHasNoKit(t *testing.T) {
	const assertions = `
var kit = window.kit;
var invented = kit.run(kit.compile("nameNobodyDeclared"));
if (kit.run(kit.compile("kit")) !== invented) throw new Error("markup still resolves kit to something");

// The control: JavaScript still has it all, so the refusals below are about markup.
if (typeof kit.theme.toggle !== "function" || typeof kit.destroy !== "function") throw new Error("precondition: the trusted root is intact");

var mode = kit.theme.mode;
if (kit.run(kit.compile("kit.theme.toggle()")) !== undefined || kit.theme.mode !== mode) {
  throw new Error("markup reached kit.theme");
}
["kit.window.minimize()", "kit.clipboard.copy('x')", "kit.destroy()", "kit.bridge", "kit.internal", "kit.service('x', 1)"].forEach(function (source) {
  if (kit.run(kit.compile(source)) !== undefined) throw new Error("markup reached " + source);
});
var destroy = kit.destroy;
kit.run(kit.compile("kit.__defineGetter__('destroy', 1)"));
if (kit.destroy !== destroy) throw new Error("an expression mutated the trusted runtime");

kit.bridge = {
  call: function () { return Promise.reject(new Error("native window command failed")); }
};
var dragRegion = el("div", { "data-kit-drag": "true" });
document.body.appendChild(dragRegion);
document.dispatchEvent({ type: "mousedown", button: 0, target: dragRegion });
document.dispatchEvent({ type: "dblclick", target: dragRegion });
kit.bridge = null;
try {
  kit.service("constructor", {});
  throw new Error("blocked service name was accepted");
} catch (error) {
  if (error.message === "blocked service name was accepted") throw error;
}

// A page cannot hand markup a kit either: an alias is a $-name, so "kit" is not one.
kit.component("collision", { label: "must-not-become-kit" });
var collision = el("div", { "data-kit-component": "collision", "data-kit-alias": "kit" });
document.body.appendChild(collision);
kit.render();
if (kit.run(kit.compile("kit.label")) !== undefined) throw new Error("a component alias became kit");

if (kit.scope.$app !== 0) throw new Error("$app must not exist before an app component registers it");
console.log("markup has no kit; JavaScript keeps it all; no implicit $app");
`
	runNodeDOMScript(t, "markup_has_no_kit.test.js", dragDirective(t)+"\n"+assertions)
}

// `$app` is an ordinary component alias. The application opts into it explicitly, owns its state
// and methods, and can use kit services from trusted component JavaScript.
func TestAppIsAnOrdinaryComponentAlias(t *testing.T) {
	const assertions = `
var kit = window.kit;
kit.component("app", {
  name: "demo",
  copied: false,
  copy: function () {
    this.copied = true;
    return kit.clipboard.writeText(this.name);
  }
});

var app = el("main", { "data-kit-component": "app", "data-kit-alias": "$app" });
var label = el("b", { "data-kit-text": "$app.name" });
app.appendChild(label);
document.body.appendChild(app);
kit.render();

if (typeof kit.scope.$app !== "object") throw new Error("$app did not resolve to the app component instance");
if (kit.scope.$app.name !== "demo") throw new Error("$app state mismatch: " + kit.scope.$app.name);
if (label.textContent !== "demo") throw new Error("markup could not read the app component alias");
// A handle resolves to the boundary's scope proxy, and that proxy reads an undeclared name as 0 —
// the same answer any undeclared name gives anywhere in the grammar. What matters is that nothing
// of the runtime leaks through it.
if (kit.scope.$app.bridge !== 0) throw new Error("$app must not inherit the native transport, got " + typeof kit.scope.$app.bridge);
if (kit.scope.$app.destroy !== 0 || kit.scope.$app.component !== 0) throw new Error("$app must not inherit runtime controls");

// The handle is the SAME object a directive is given, which is what makes a write through it
// repaint: the proxy's set trap is the only thing that tells the kernel anything changed.
if (kit.scope.$app !== kit.scopeFor(label)) throw new Error("the handle and the directive scope must be one object");

console.log("$app: explicit application component instance");
`
	runNodeDOMScript(t, "app_component_alias.test.js", assertions)
}

// data-kit-alias registers any component's scope as a global handle.
func TestDataKitAliasRegistersHandle(t *testing.T) {
	const assertions = `
var kit = window.kit;
kit.component("aliasdemo", { label: "hi" });
var box = el("div", { "data-kit-component": "aliasdemo", "data-kit-alias": "$box" });
box.appendChild(el("b", { "data-kit-text": "label" }));
document.body.appendChild(box);
kit.render();

if (typeof kit.scope.$box !== "object") throw new Error("data-kit-alias did not register a handle (got " + typeof kit.scope.$box + ")");
if (kit.scope.$box.label !== "hi") throw new Error("$box handle should reach the component scope, got " + kit.scope.$box.label);

var invalid = el("div", { "data-kit-component": "aliasdemo", "data-kit-alias": "$bad-name" });
document.body.appendChild(invalid);
kit.render();
if (kit.scope["$bad-name"] !== 0) throw new Error("alias accepted characters the lexer cannot resolve");

kit.internal.cleanupTree(box);
if (kit.scope.$box !== 0) throw new Error("component cleanup left a stale global alias");

// One spelling (ideaship-final §6): the alias is data-kit-alias, never a "=$alias" tail on the
// component name, and never data-kitwork-alias / data-alias.
var tail = el("div", { "data-kit-component": "aliasdemo=$tail" });
var legacy = el("div", { "data-kit-component": "aliasdemo", "data-kitwork-alias": "$legacy", "data-alias": "$plain" });
document.body.appendChild(tail); document.body.appendChild(legacy);
kit.render();
if (kit.scope.$tail !== 0) throw new Error("a =$alias tail on the component name should not register a handle");
if (kit.scope.$legacy !== 0 || kit.scope.$plain !== 0) throw new Error("data-kitwork-alias / data-alias should not register a handle");

console.log("data-kit-alias: registered globally and released with its component");
`
	runNodeDOMScript(t, "data_kit_alias.test.js", assertions)
}
