package hydrate

import "testing"

// Markup has no kit (03/10). Trusted JavaScript owns the whole window.kitwork root; an expression gets
// nothing under that name — it reads like any name nobody declared — so no service, no runtime
// control and no raw bridge is reachable from markup. A page reaches a service through the component
// that is its view (appearance, progressbar, titlebar). `$app` is not installed by the kernel.
func TestMarkupHasNoKit(t *testing.T) {
	const assertions = `
var kitwork = window.kitwork;
var invented = kitwork.run(kitwork.compile("nameNobodyDeclared"));
if (kitwork.run(kitwork.compile("kitwork")) !== invented) throw new Error("markup still resolves kitwork to something");

// The control: JavaScript still has it all, so the refusals below are about markup.
if (typeof kitwork.appearance.toggle !== "function" || typeof kitwork.destroy !== "function") throw new Error("precondition: the trusted root is intact");

var mode = kitwork.appearance.mode;
if (kitwork.run(kitwork.compile("kitwork.appearance.toggle()")) !== undefined || kitwork.appearance.mode !== mode) {
  throw new Error("markup reached kitwork.appearance");
}
["kitwork.window.minimize()", "kitwork.clipboard.copy('x')", "kitwork.destroy()", "kitwork.bridge", "kitwork.internal", "kitwork.service('x', 1)"].forEach(function (source) {
  if (kitwork.run(kitwork.compile(source)) !== undefined) throw new Error("markup reached " + source);
});
var destroy = kitwork.destroy;
kitwork.run(kitwork.compile("kitwork.__defineGetter__('destroy', 1)"));
if (kitwork.destroy !== destroy) throw new Error("an expression mutated the trusted runtime");

kitwork.bridge = {
  call: function () { return Promise.reject(new Error("native window command failed")); }
};
var dragRegion = el("div", { "data-kitwork-drag": "true" });
document.body.appendChild(dragRegion);
document.dispatchEvent({ type: "mousedown", button: 0, target: dragRegion });
document.dispatchEvent({ type: "dblclick", target: dragRegion });
kitwork.bridge = null;
try {
  kitwork.service("constructor", {});
  throw new Error("blocked service name was accepted");
} catch (error) {
  if (error.message === "blocked service name was accepted") throw error;
}

// A page cannot hand markup a kitwork either: an alias is a $-name, so "kitwork" is not one.
kitwork.component("collision", { label: "must-not-become-kit" });
var collision = el("div", { "data-kitwork-component": "collision: kitwork" });
document.body.appendChild(collision);
kitwork.render();
if (kitwork.run(kitwork.compile("kitwork.label")) !== undefined) throw new Error("a component alias became kitwork");

if (kitwork.scope.$app !== 0) throw new Error("$app must not exist before an app component registers it");
console.log("markup has no kitwork; JavaScript keeps it all; no implicit $app");
`
	runNodeDOMScript(t, "markup_has_no_kit.test.js", dragDirective(t)+"\n"+assertions)
}

// `$app` is an ordinary component alias. The application opts into it explicitly, owns its state
// and methods, and can use kit services from trusted component JavaScript.
func TestAppIsAnOrdinaryComponentAlias(t *testing.T) {
	const assertions = `
var kitwork = window.kitwork;
kitwork.component("app", {
  name: "demo",
  copied: false,
  copy: function () {
    this.copied = true;
    return kitwork.clipboard.writeText(this.name);
  }
});

var app = el("main", { "data-kitwork-component": "app: $app" });
var label = el("b", { "data-kitwork-bind": "text: $app.name" });
app.appendChild(label);
document.body.appendChild(app);
kitwork.render();

if (typeof kitwork.scope.$app !== "object") throw new Error("$app did not resolve to the app component instance");
if (kitwork.scope.$app.name !== "demo") throw new Error("$app state mismatch: " + kitwork.scope.$app.name);
if (label.textContent !== "demo") throw new Error("markup could not read the app component alias");
// A handle resolves to the boundary's scope proxy, and that proxy reads an undeclared name as 0 —
// the same answer any undeclared name gives anywhere in the grammar. What matters is that nothing
// of the runtime leaks through it.
if (kitwork.scope.$app.bridge !== 0) throw new Error("$app must not inherit the native transport, got " + typeof kitwork.scope.$app.bridge);
if (kitwork.scope.$app.destroy !== 0 || kitwork.scope.$app.component !== 0) throw new Error("$app must not inherit runtime controls");

// The handle is the SAME object a directive is given, which is what makes a write through it
// repaint: the proxy's set trap is the only thing that tells the kernel anything changed.
if (kitwork.scope.$app !== kitwork.scopeFor(label)) throw new Error("the handle and the directive scope must be one object");

console.log("$app: explicit application component instance");
`
	runNodeDOMScript(t, "app_component_alias.test.js", assertions)
}

// "name: $alias" in data-kitwork-component registers any component's scope as a global handle.
func TestComponentAliasRegistersHandle(t *testing.T) {
	const assertions = `
var kitwork = window.kitwork;
kitwork.component("aliasdemo", { label: "hi" });
var box = el("div", { "data-kitwork-component": "aliasdemo: $box" });
box.appendChild(el("b", { "data-kitwork-bind": "text: label" }));
document.body.appendChild(box);
kitwork.render();

if (typeof kitwork.scope.$box !== "object") throw new Error("aliasdemo: $box did not register a handle (got " + typeof kitwork.scope.$box + ")");
if (kitwork.scope.$box.label !== "hi") throw new Error("$box handle should reach the component scope, got " + kitwork.scope.$box.label);

var invalid = el("div", { "data-kitwork-component": "aliasdemo: $bad-name" });
document.body.appendChild(invalid);
kitwork.render();
if (kitwork.scope["$bad-name"] !== 0) throw new Error("alias accepted characters the lexer cannot resolve");

kitwork.internal.cleanupTree(box);
if (kitwork.scope.$box !== 0) throw new Error("component cleanup left a stale global alias");

// One spelling (ideaship-final §6): the alias follows the name after ": " in data-kitwork-component,
// never a "=$alias" tail on the component name, and never an attribute of its own (data-kitwork-alias,
// data-alias, or data-kit-alias, the hand-written one that went on 26/10/2026).
var tail = el("div", { "data-kitwork-component": "aliasdemo=$tail" });
var legacy = el("div", { "data-kitwork-component": "aliasdemo", "data-kitwork-alias": "$legacy", "data-alias": "$plain", "data-kit-alias": "$old" });
document.body.appendChild(tail); document.body.appendChild(legacy);
kitwork.render();
if (kitwork.scope.$tail !== 0) throw new Error("a =$alias tail on the component name should not register a handle");
if (kitwork.scope.$legacy !== 0 || kitwork.scope.$plain !== 0 || kitwork.scope.$old !== 0) throw new Error("data-kitwork-alias / data-alias / data-kit-alias should not register a handle");

console.log("name: $alias: registered globally and released with its component");
`
	runNodeDOMScript(t, "data_kit_alias.test.js", assertions)
}
