package hydrate

import "testing"

func TestNativeAPICanonicalPromisesAndDynamicBridge(t *testing.T) {
	setup := `
var modules = Object.create(null);
var platformServices = Object.create(null);
var page = Object.create(null);
var cleanups = [];
var kitwork = {
  bridge: null,
  runtime: { info: function () { return { engine: kitwork.platform || "web" }; } },
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
global.window = { kit: kitwork, kitwork: kitwork };
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
  var native = kitwork.module("native");
  if (native.available !== false) {
    throw new Error("native module should start unavailable");
  }
  if (native.bridge !== undefined) throw new Error("native module leaked the raw bridge");
  if (typeof kitwork.host !== "object") throw new Error("kitwork.host lifecycle API missing");
  if (kitwork.app !== kitwork.host) throw new Error("deprecated kitwork.app compatibility alias changed");
  if (typeof kitwork.clipboard !== "object" || typeof kitwork.camera !== "object") {
    throw new Error("clipboard/camera must be service objects, not callable shims");
  }
  if (typeof kitwork.appearance.toggle !== "function" || typeof kitwork.navigation.back !== "function") {
    throw new Error("appearance/navigation namespaces missing");
  }
  if (typeof kitwork.window.restore !== "function" || typeof kitwork.capabilities.supports !== "function") {
    throw new Error("window/capabilities namespaces missing");
  }
  if (typeof kitwork.network.status !== "function") {
    throw new Error("network namespace missing");
  }
  if (typeof kitwork.back !== "function" || typeof kitwork.minimize !== "function") {
    throw new Error("trusted-JavaScript compatibility aliases missing");
  }
  // kitwork.theme / kitwork.toggleTheme (the light/dark service's old names) and kitwork.dialog went on 26/10/2026.
  if (kitwork.theme !== undefined || kitwork.toggleTheme !== undefined || kitwork.dialog !== undefined) {
    throw new Error("a service removed on 26/10/2026 is still installed");
  }

  var unavailable = kitwork.host.info();
  if (!unavailable || typeof unavailable.then !== "function") {
    throw new Error("unsupported host call must still return a Promise");
  }
  try {
    await unavailable;
    throw new Error("unsupported host call resolved");
  } catch (error) {
      if (error.code !== "UNSUPPORTED") throw error;
  }

  var webMinimize = kitwork.window.minimize();
  if (!webMinimize || typeof webMinimize.then !== "function" || await webMinimize !== false) {
    throw new Error("web window command must resolve false through a Promise");
  }

  var webWrite = kitwork.clipboard.writeText(42);
  if (!webWrite || typeof webWrite.then !== "function") {
    throw new Error("clipboard.writeText web fallback is not Promise-based");
  }
  await webWrite;
  if (await kitwork.clipboard.writeText("void result") !== undefined) {
    throw new Error("clipboard.writeText must resolve without a value");
  }
  if (navigator.clipboard.writes[0] !== "42") throw new Error("web clipboard write mismatch");
  if (await kitwork.clipboard.readText() !== "web clipboard") throw new Error("web clipboard read mismatch");
  if (await kitwork.clipboard.copy("copy") !== undefined) {
    throw new Error("clipboard.copy must share Promise<void> write semantics");
  }
  if (navigator.clipboard.writes[2] !== "copy") throw new Error("clipboard.copy write mismatch");
  if (await kitwork.capabilities.supports("clipboard.writeText") !== true) {
    throw new Error("web capabilities.supports did not accept the public method path");
  }
  if (await kitwork.capabilities.supports("clipboard.write") !== false) {
    throw new Error("legacy capability id should not be canonical");
  }
  if (await kitwork.capabilities.supports("window.minimize") !== false) {
    throw new Error("web should not report native window controls as supported");
  }

  // mode is the CHOICE, resolved is what is painted. With nothing stored, the choice is "system"
  // and the paint follows prefers-color-scheme — the same three modes the server pre-paint reads.
  if (kitwork.appearance.mode !== "system") throw new Error("with nothing stored the choice is system, got " + kitwork.appearance.mode);
  if (kitwork.appearance.resolved !== "light" && kitwork.appearance.resolved !== "dark") {
    throw new Error("resolved must be a painted mode, got " + kitwork.appearance.resolved);
  }
  if (kitwork.appearance.system() !== "system" || kitwork.appearance.mode !== "system") {
    throw new Error("appearance.system() should hand the choice back to the OS");
  }
  if (kitwork.appearance.set("dark") !== "dark" || kitwork.appearance.mode !== "dark" || kitwork.appearance.resolved !== "dark") {
    throw new Error("appearance.set did not update mode/resolved state");
  }
  if (kitwork.appearance.toggle() !== "light" || kitwork.appearance.mode !== "light") {
    throw new Error("appearance.toggle did not switch the resolved mode");
  }
  kitwork.navigation.back();
  kitwork.navigation.forward();
  kitwork.navigation.reload();
  if (history.backs !== 1 || history.forwards !== 1 || location.reloads !== 1) {
    throw new Error("navigation namespace did not delegate to browser history/location");
  }

  var calls = [];
  var lateBridge = {
    call: function (action, params) {
      calls.push({ action: action, params: params });
      if (action === "clipboard.readText") return "native clipboard";
      if (action === "camera.capture") return Promise.resolve("native://photo");
      if (action === "secureStorage.get") return Promise.resolve("stored value");
      if (action === "device.info") {
        return Promise.resolve({ platform: "android", osVersion: "16", model: "Pixel" });
      }
      if (action === "network.status") return Promise.resolve({ online: true });
      if (action === "capabilities.supports") {
        return Promise.resolve(
          params.id === "camera.capture" || params.id === "window.minimize" || params.id === "network.status"
        );
      }
      return Promise.resolve(true);
    }
  };
  kitwork.bridge = lateBridge;
  if (!native.available) {
    throw new Error("late bridge attachment was not observed dynamically");
  }
  // kitwork.isNative / kitwork.platform / runtime.info().engine are the KERNEL's view of the bridge, and this
  // test deliberately runs without the kernel — see TestPlatformFactsFollowTheBridge.
  if (!kitwork.bridge || typeof kitwork.bridge.call !== "function") {
    throw new Error("late bridge attachment left the adapter unusable");
  }

  if (await kitwork.clipboard.writeText("native write") !== undefined) {
    throw new Error("native clipboard.writeText must resolve without a value");
  }
  if (calls[0].action !== "clipboard.writeText" || calls[0].params.text !== "native write") {
    throw new Error("native clipboard write contract mismatch");
  }
  if (await kitwork.clipboard.readText() !== "native clipboard") {
    throw new Error("native clipboard read contract mismatch");
  }
  await kitwork.host.info();
  if (!calls.some(function (entry) { return entry.action === "host.info"; })) {
    throw new Error("host lifecycle transport changed");
  }

  await kitwork.secureStorage.set("note", "private");
  if (await kitwork.secureStorage.get("note") !== "stored value") {
    throw new Error("secureStorage.get result mismatch");
  }
  await kitwork.secureStorage.remove("note");
  var deviceInfo = await kitwork.device.info();
  if (!deviceInfo || deviceInfo.platform !== "android") {
    throw new Error("device.info result mismatch");
  }
  await kitwork.device.vibrate([10, 20]);
  await kitwork.shell.open("https://example.test/");
  if (!calls.some(function (entry) {
    return entry.action === "secureStorage.set" && entry.params.key === "note" && entry.params.value === "private";
  }) || !calls.some(function (entry) {
    return entry.action === "secureStorage.remove" && entry.params.key === "note";
  }) || !calls.some(function (entry) {
    return entry.action === "device.vibrate" && entry.params.pattern[1] === 20;
  }) || !calls.some(function (entry) {
    return entry.action === "shell.open" && entry.params.url === "https://example.test/";
  })) {
    throw new Error("native service parameter contract mismatch");
  }

  var capture = kitwork.camera.capture({ facingMode: "user" });
  if (!capture || typeof capture.then !== "function") {
    throw new Error("camera.capture is not Promise-based");
  }
  if (await capture !== "native://photo") throw new Error("camera result mismatch");
  if (!calls.some(function (entry) {
    return entry.action === "camera.capture" && entry.params.facingMode === "user";
  })) {
    throw new Error("camera options were not forwarded");
  }
  if (await kitwork.capabilities.supports("camera.capture") !== true) {
    throw new Error("native capability probe mismatch");
  }
  if (await kitwork.capabilities.supports("window.minimize") !== true) {
    throw new Error("native window capability probe mismatch");
  }
  if (await kitwork.capabilities.supports("network.status") !== true) {
    throw new Error("native network capability probe mismatch");
  }
  var networkStatus = await kitwork.network.status();
  if (!networkStatus || networkStatus.online !== true || !calls.some(function (entry) {
    return entry.action === "network.status";
  })) {
    throw new Error("native network.status contract mismatch");
  }
  var callsBeforeRuntimeOwnedProbe = calls.length;
  if (await kitwork.capabilities.supports("appearance.toggle") !== true ||
      await kitwork.capabilities.supports("navigation.back") !== true) {
    throw new Error("runtime-owned capabilities became dependent on the native shell");
  }
  if (calls.length !== callsBeforeRuntimeOwnedProbe) {
    throw new Error("runtime-owned capability probe was incorrectly sent to the native shell");
  }
  var restored = kitwork.window.restore();
  if (!restored || typeof restored.then !== "function" || await restored !== true) {
    throw new Error("native window.restore must return the bridge Promise");
  }
  if (!calls.some(function (entry) { return entry.action === "window.restore"; })) {
    throw new Error("window.restore action mismatch");
  }

  kitwork.bridge = {
    call: function () { throw new Error("sync adapter failure"); }
  };
  var rejected = kitwork.clipboard.writeText("reject");
  if (!rejected || typeof rejected.then !== "function") {
    throw new Error("sync adapter failure escaped instead of becoming a rejected Promise");
  }
  try {
    await rejected;
    throw new Error("sync adapter failure unexpectedly resolved");
  } catch (error) {
    if (error.message !== "sync adapter failure") throw error;
  }

  kitwork.bridge = null;
  if (native.available !== false) {
    throw new Error("bridge removal was not observed dynamically");
  }
  if (kitwork.bridge) {
    throw new Error("bridge removal left the adapter in place");
  }
  await kitwork.clipboard.writeText("web again");
  if (navigator.clipboard.writes[3] !== "web again") {
    throw new Error("clipboard did not return to its web fallback");
  }

  var destroyed = false;
  kitwork.bridge = {
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
