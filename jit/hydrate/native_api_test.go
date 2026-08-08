package hydrate

import "testing"

func TestNativeAPICanonicalPromisesAndDynamicBridge(t *testing.T) {
	setup := `
var modules = Object.create(null);
var platformServices = Object.create(null);
var page = Object.create(null);
var cleanups = [];
var kit = {
  bridge: null,
  runtime: { info: function () { return { engine: kit.platform || "web" }; } },
  module: function (name, value) {
    if (arguments.length > 1) {
      modules[name] = value;
      return value;
    }
    return modules[name];
  },
  has: function (name) {
    return Object.prototype.hasOwnProperty.call(modules, name);
  },
  service: function (name, value) {
    if (arguments.length === 1) return platformServices[name];
    platformServices[name] = value;
    this[name] = value;
    return value;
  },
  cleanup: function (callback) { cleanups.push(callback); },
  set: function (key, value) { page[key] = value; },
  KitworkError: function (message, code, moduleName, actionName) {
    var error = new Error(message);
    error.code = code;
    error.module = moduleName;
    error.action = actionName;
    return error;
  }
};
global.window = { kit: kit, kitwork: kit };
var dark = false;
global.document = {
  body: { appendChild: function () {} },
  documentElement: {
    classList: {
      contains: function (name) { return name === "dark" && dark; },
      toggle: function (name, value) { if (name === "dark") dark = !!value; }
    }
  },
  createElement: function () {
    return {
      style: {},
      setAttribute: function () {},
      addEventListener: function () {},
      select: function () {},
      click: function () {},
      remove: function () {}
    };
  },
  execCommand: function () { return true; }
};
Object.defineProperty(global, "navigator", {
  configurable: true,
  writable: true,
  value: {
    clipboard: {
      writes: [],
      writeText: function (text) {
        this.writes.push(text);
        return Promise.resolve();
      },
      readText: function () { return Promise.resolve("web clipboard"); }
    }
  }
});
window.navigator = global.navigator;
var stored = Object.create(null);
global.localStorage = {
  getItem: function (key) {
    return Object.prototype.hasOwnProperty.call(stored, key) ? stored[key] : null;
  },
  setItem: function (key, value) { stored[key] = String(value); },
  removeItem: function (key) { delete stored[key]; }
};
window.localStorage = global.localStorage;
global.history = {
  backs: 0,
  forwards: 0,
  back: function () { this.backs++; },
  forward: function () { this.forwards++; }
};
global.location = {
  reloads: 0,
  reload: function () { this.reloads++; }
};
window.history = global.history;
window.location = global.location;
`

	assertions := `
(async function () {
  var native = kit.module("native");
  if (native.available !== false) {
    throw new Error("native module should start unavailable");
  }
  if (native.bridge !== undefined) throw new Error("native module leaked the raw bridge");
  if (typeof kit.host !== "object") throw new Error("kit.host lifecycle API missing");
  if (kit.app !== kit.host) throw new Error("deprecated kit.app compatibility alias changed");
  if (typeof kit.clipboard !== "object" || typeof kit.camera !== "object") {
    throw new Error("clipboard/camera must be service objects, not callable shims");
  }
  if (typeof kit.theme.toggle !== "function" || typeof kit.navigation.back !== "function") {
    throw new Error("theme/navigation namespaces missing");
  }
  if (typeof kit.window.restore !== "function" || typeof kit.capabilities.supports !== "function") {
    throw new Error("window/capabilities namespaces missing");
  }
  if (typeof kit.toggleTheme !== "function" || typeof kit.minimize !== "function") {
    throw new Error("trusted-JavaScript compatibility aliases missing");
  }

  var unavailable = kit.host.info();
  if (!unavailable || typeof unavailable.then !== "function") {
    throw new Error("unsupported host call must still return a Promise");
  }
  try {
    await unavailable;
    throw new Error("unsupported host call resolved");
  } catch (error) {
      if (error.code !== "UNSUPPORTED") throw error;
  }

  var webMinimize = kit.window.minimize();
  if (!webMinimize || typeof webMinimize.then !== "function" || await webMinimize !== false) {
    throw new Error("web window command must resolve false through a Promise");
  }

  var webWrite = kit.clipboard.writeText(42);
  if (!webWrite || typeof webWrite.then !== "function") {
    throw new Error("clipboard.writeText web fallback is not Promise-based");
  }
  await webWrite;
  if (await kit.clipboard.writeText("void result") !== undefined) {
    throw new Error("clipboard.writeText must resolve without a value");
  }
  if (navigator.clipboard.writes[0] !== "42") throw new Error("web clipboard write mismatch");
  if (await kit.clipboard.readText() !== "web clipboard") throw new Error("web clipboard read mismatch");
  if (await kit.clipboard.copy("copy") !== undefined) {
    throw new Error("clipboard.copy must share Promise<void> write semantics");
  }
  if (navigator.clipboard.writes[2] !== "copy") throw new Error("clipboard.copy write mismatch");
  if (await kit.capabilities.supports("clipboard.writeText") !== true) {
    throw new Error("web capabilities.supports did not accept the public method path");
  }
  if (await kit.capabilities.supports("clipboard.write") !== false) {
    throw new Error("legacy capability id should not be canonical");
  }
  if (await kit.capabilities.supports("window.minimize") !== false) {
    throw new Error("web should not report native window controls as supported");
  }

  if (kit.theme.mode !== "light") throw new Error("initial theme mode should follow the document");
  if (kit.theme.set("dark") !== "dark" || kit.theme.mode !== "dark" || kit.theme.resolved !== "dark") {
    throw new Error("theme.set did not update mode/resolved state");
  }
  if (kit.theme.toggle() !== "light" || kit.theme.mode !== "light") {
    throw new Error("theme.toggle did not switch the resolved mode");
  }
  kit.navigation.back();
  kit.navigation.forward();
  kit.navigation.reload();
  if (history.backs !== 1 || history.forwards !== 1 || location.reloads !== 1) {
    throw new Error("navigation namespace did not delegate to browser history/location");
  }

  var calls = [];
  var lateBridge = {
    call: function (action, params) {
      calls.push({ action: action, params: params });
      if (action === "clipboard.readText") return "native clipboard";
      if (action === "camera.capture") return Promise.resolve("native://photo");
      if (action === "capabilities.supports") {
        return Promise.resolve(params.id === "camera.capture" || params.id === "window.minimize");
      }
      return Promise.resolve(true);
    }
  };
  kit.bridge = lateBridge;
  if (!native.available) {
    throw new Error("late bridge attachment was not observed dynamically");
  }
  if (!kit.isNative || kit.platform !== "native" || kit.runtime.info().engine !== "native") {
    throw new Error("late bridge attachment left runtime metadata stale");
  }

  if (await kit.clipboard.writeText("native write") !== undefined) {
    throw new Error("native clipboard.writeText must resolve without a value");
  }
  if (calls[0].action !== "clipboard.writeText" || calls[0].params.text !== "native write") {
    throw new Error("native clipboard write contract mismatch");
  }
  if (await kit.clipboard.readText() !== "native clipboard") {
    throw new Error("native clipboard read contract mismatch");
  }
  await kit.host.info();
  if (!calls.some(function (entry) { return entry.action === "host.info"; })) {
    throw new Error("host lifecycle transport changed");
  }

  var capture = kit.camera.capture({ facingMode: "user" });
  if (!capture || typeof capture.then !== "function") {
    throw new Error("camera.capture is not Promise-based");
  }
  if (await capture !== "native://photo") throw new Error("camera result mismatch");
  if (!calls.some(function (entry) {
    return entry.action === "camera.capture" && entry.params.facingMode === "user";
  })) {
    throw new Error("camera options were not forwarded");
  }
  if (await kit.capabilities.supports("camera.capture") !== true) {
    throw new Error("native capability probe mismatch");
  }
  if (await kit.capabilities.supports("window.minimize") !== true) {
    throw new Error("native window capability probe mismatch");
  }
  var callsBeforeRuntimeOwnedProbe = calls.length;
  if (await kit.capabilities.supports("theme.toggle") !== true ||
      await kit.capabilities.supports("navigation.back") !== true) {
    throw new Error("runtime-owned capabilities became dependent on the native shell");
  }
  if (calls.length !== callsBeforeRuntimeOwnedProbe) {
    throw new Error("runtime-owned capability probe was incorrectly sent to the native shell");
  }
  var restored = kit.window.restore();
  if (!restored || typeof restored.then !== "function" || await restored !== true) {
    throw new Error("native window.restore must return the bridge Promise");
  }
  if (!calls.some(function (entry) { return entry.action === "window.restore"; })) {
    throw new Error("window.restore action mismatch");
  }

  kit.bridge = {
    call: function () { throw new Error("sync adapter failure"); }
  };
  var rejected = kit.clipboard.writeText("reject");
  if (!rejected || typeof rejected.then !== "function") {
    throw new Error("sync adapter failure escaped instead of becoming a rejected Promise");
  }
  try {
    await rejected;
    throw new Error("sync adapter failure unexpectedly resolved");
  } catch (error) {
    if (error.message !== "sync adapter failure") throw error;
  }

  kit.bridge = null;
  if (native.available !== false) {
    throw new Error("bridge removal was not observed dynamically");
  }
  if (kit.isNative || kit.platform !== "web" || kit.runtime.info().engine !== "web") {
    throw new Error("bridge removal left runtime metadata stale");
  }
  await kit.clipboard.writeText("web again");
  if (navigator.clipboard.writes[3] !== "web again") {
    throw new Error("clipboard did not return to its web fallback");
  }

  var destroyed = false;
  kit.bridge = {
    call: function () { return Promise.resolve(true); },
    destroy: function () { destroyed = true; }
  };
  cleanups.forEach(function (cleanup) { cleanup(); });
  if (!destroyed) throw new Error("runtime cleanup did not tear down the active bridge");
})().catch(function (error) {
  console.error(error);
  process.exitCode = 1;
});
`

	runNodeSourceTest(t, bridgeJS+"\n"+nativeJS+"\n"+webJS, setup, assertions)
}
