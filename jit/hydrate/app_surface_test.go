package hydrate

import "testing"

// Trusted JavaScript owns the complete window.kit root. Markup resolves `kit` through a curated,
// read-only service view: public platform methods are reachable, while runtime control internals and
// the raw bridge transport are not. `$app` is not installed by the kernel.
func TestKitExpressionSurfaceGrantsServicesDeniesInternals(t *testing.T) {
	const assertions = `
var kit = window.kit;

var services = kit.scope.kit;
if (services === kit) throw new Error("markup received the trusted runtime root");
if (typeof services.theme.toggle !== "function") throw new Error("kit.theme.toggle must be reachable in markup");
if (services.theme.mode !== "light" || services.theme.resolved !== "light") throw new Error("kit.theme state missing");
if (typeof services.clipboard.writeText !== "function" ||
    typeof services.clipboard.readText !== "function" ||
    typeof services.clipboard.copy !== "function") throw new Error("kit.clipboard service incomplete");
if (typeof services.camera.capture !== "function") throw new Error("kit.camera.capture missing");
if (typeof services.navigation.back !== "function") throw new Error("kit.navigation.back missing");
if (typeof services.window.minimize !== "function" ||
    typeof services.window.restore !== "function" ||
    typeof services.window.close !== "function") throw new Error("kit.window service incomplete");
if (typeof services.capabilities.supports !== "function") throw new Error("kit.capabilities.supports missing");
if (typeof services.dialog.confirm !== "function" ||
    typeof services.share.open !== "function" ||
    typeof services.storage.get !== "function") throw new Error("web service grants incomplete");
if (services.window.drag !== undefined) throw new Error("private window.drag leaked to markup");
if (services.permissions !== undefined) throw new Error("ungranted permissions service leaked to markup");
if (services.toggleTheme !== undefined || services.back !== undefined || services.minimize !== undefined) {
  throw new Error("deprecated flat aliases leaked to markup");
}

if (typeof kit.destroy !== "function") throw new Error("precondition: kit.destroy exists on the runtime root");
["bridge", "destroy", "internal", "render", "run", "compile", "component", "module", "service", "scope", "has"].forEach(function (name) {
  if (services[name] !== undefined) {
    throw new Error("expression kit." + name + " leaked a private/runtime control API");
  }
});
["toString", "__defineGetter__", "__defineSetter__", "__lookupGetter__", "__lookupSetter__"].forEach(function (name) {
  if (services[name] !== undefined) {
    throw new Error("expression kit inherited an ungranted prototype method: " + name);
  }
});

var originalDestroy = kit.destroy;
var originalToggle = kit.theme.toggle;
kit.run(kit.compile("kit.__defineGetter__('destroy', kit.theme.toggle)"));
kit.run(kit.compile("kit.theme.__defineGetter__('toggle', kit.theme.toggle)"));
if (kit.destroy !== originalDestroy || kit.theme.toggle !== originalToggle) {
  throw new Error("expression mutated the trusted runtime through a prototype meta-method");
}

var privateService = {
  value: 7,
  visible: function () { return this === privateService ? this.value : -1; },
  secret: function () { return 99; }
};
kit.service("probe", privateService, { expression: ["value", "visible"] });
if (services.probe.value !== 7 || services.probe.visible() !== 7) {
  throw new Error("registered service members or this-binding are broken");
}
if (services.probe.secret !== undefined) throw new Error("ungranted service member leaked");
if (kit.run(kit.compile("kit.probe.visible()")) !== 7) {
  throw new Error("expression walker could not invoke a dynamically registered service");
}
if (kit.run(kit.compile("kit.probe.secret()")) !== undefined) {
  throw new Error("expression walker invoked an ungranted service member");
}
try { services.probe.value = 9; } catch (_) { }
if (privateService.value !== 7 || services.probe.value !== 7) {
  throw new Error("expression service facade was writable");
}
privateService.value = 8;
if (services.probe.value !== 8) throw new Error("expression service property was snapshotted");
var windowResult = kit.run(kit.compile("kit.window.minimize()"));
if (!windowResult || typeof windowResult.then !== "function") {
  throw new Error("curated kit.window service did not pass the global-key blocklist safely");
}
kit.bridge = {
  call: function () { return Promise.reject(new Error("native window command failed")); }
};
var dragRegion = el("div", { "data-kit-drag": "" });
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

kit.component("collision", { label: "must-not-replace-kit" });
var collision = el("div", { "data-kit-component": "collision", "data-kit-alias": "kit" });
document.body.appendChild(collision);
kit.render();
if (kit.scope.kit !== services || kit.scope.kit.label !== undefined) {
  throw new Error("a component alias replaced the reserved kit service identifier");
}

if (kit.scope.$app !== 0) throw new Error("$app must not exist before an app component registers it");
console.log("kit expression services: exact grants, default-deny, prototype-safe, no implicit $app");
`
	runNodeDOMScript(t, "kit_expression_surface.test.js", assertions)
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
if (kit.scope.$app.bridge !== undefined) throw new Error("$app must not inherit the native transport");

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

console.log("data-kit-alias: registered globally and released with its component");
`
	runNodeDOMScript(t, "data_kit_alias.test.js", assertions)
}
