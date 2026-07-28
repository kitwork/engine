package hydrate

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func runNodeSourceTest(t *testing.T, source, setup, assertions string) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for bridge behavior tests")
	}

	script := setup + "\n" + source + "\n" + assertions
	path := filepath.Join(t.TempDir(), "runtime.test.js")
	if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(node, path).CombinedOutput(); err != nil {
		t.Fatalf("bridge behavior test failed: %v\n%s", err, out)
	}
}

func TestBridgePlainWebStaysWeb(t *testing.T) {
	runNodeSourceTest(t, bridgeJS,
		`global.window = {};`,
		`
if (window.kitwork.isNative !== false) throw new Error("plain web reported native");
if (window.kitwork.platform !== "web") throw new Error("plain web platform mismatch");
if (window.kitwork.bridge !== null) throw new Error("plain web created a bridge");
if (typeof window.kitwork.Bridge !== "function") throw new Error("Bridge constructor missing");
`)
}

func TestBridgeRequestResponseEventsAndTimeout(t *testing.T) {
	runNodeSourceTest(t, bridgeJS,
		`
var sent = [];
var messageListener = null;
var removed = false;
global.window = {
  chrome: {
    webview: {
      platform: "webview2",
      postMessage: function (payload) { sent.push(payload); },
      addEventListener: function (name, listener) {
        if (name === "message") messageListener = listener;
      },
      removeEventListener: function (name, listener) {
        if (name === "message" && listener === messageListener) removed = true;
      }
    }
  }
};
`,
		`
(async function () {
  var bridge = window.kitwork.bridge;
  if (!window.kitwork.isNative) throw new Error("native handle was not detected");
  if (window.kitwork.platform !== "webview2") throw new Error("native platform mismatch");

  var resultPromise = bridge.call("storage.get", { key: "theme" });
  var request = sent.shift();
  if (!request || request.module !== "storage" || request.action !== "get") {
    throw new Error("request envelope mismatch");
  }
  messageListener({ data: { id: request.id, result: "dark" } });
  if (await resultPromise !== "dark") throw new Error("response did not resolve");
  if (bridge.pending.size !== 0) throw new Error("resolved request leaked");

  var eventValue = null;
  var off = bridge.on("network.change", function (value) { eventValue = value; });
  messageListener({ data: { event: "network.change", data: "online" } });
  if (eventValue !== "online") throw new Error("native event was not dispatched");
  off();

  bridge.timeout = 5;
  var timeoutCode = "";
  try {
    await bridge.call("device.silent", {});
  } catch (error) {
    timeoutCode = error.code;
  }
  if (timeoutCode !== "BRIDGE_TIMEOUT") throw new Error("timeout was not enforced");
  if (bridge.pending.size !== 0) throw new Error("timed-out request leaked");

  bridge.destroy();
  if (!removed) throw new Error("native message listener was not removed");
})().catch(function (error) {
  console.error(error);
  process.exitCode = 1;
});
`)
}

func TestClientExpressionRuntimeAndCleanup(t *testing.T) {
	runNodeSourceTest(t, Runtime()+"\nwindow.__firstAdded = added;\n"+Runtime(),
		`
var added = 0;
var removed = 0;
var observerDisconnected = false;
var storage = Object.create(null);
global.window = {
  addEventListener: function () { added++; },
  removeEventListener: function () { removed++; }
};
global.document = {
  readyState: "complete",
  addEventListener: function () { added++; },
  removeEventListener: function () { removed++; },
  querySelector: function () { return null; },
  querySelectorAll: function () { return []; },
  dispatchEvent: function () {},
  documentElement: {
    nodeType: 1,
    querySelectorAll: function () { return []; },
    classList: { contains: function () { return false; }, toggle: function () {} },
    style: { setProperty: function () {} }
  },
  body: {}
};
global.navigator = {};
global.localStorage = {
  get length() { return Object.keys(storage).length; },
  key: function (index) { return Object.keys(storage)[index] || null; },
  getItem: function (key) { return Object.prototype.hasOwnProperty.call(storage, key) ? storage[key] : null; },
  setItem: function (key, value) { storage[key] = String(value); },
  removeItem: function (key) { delete storage[key]; }
};
global.history = {
  back: function () {},
  forward: function () {}
};
global.location = { reload: function () {} };
global.MutationObserver = function () {
  this.observe = function () {};
  this.disconnect = function () { observerDisconnected = true; };
};
global.CustomEvent = function () {};
window.document = document;
window.history = history;
		`,
		`
(async function () {
var kitwork = window.kitwork;
if (!kitwork.runtime || !kitwork.runtime.booted) throw new Error("runtime metadata missing");
if (kitwork.runtime.engine !== "web") throw new Error("web runtime engine mismatch");
if (kitwork.isNative) throw new Error("plain web runtime reported native");
if (kitwork.run(7, {}) !== 7) throw new Error("literal IR parity mismatch");
["kernel", "native", "storage", "web", "componentLoader", "morph", "compat", "drive"].forEach(function (name) {
  if (!kitwork.has(name)) throw new Error("module missing: " + name);
});
if (added !== window.__firstAdded) throw new Error("double inclusion added listeners");

await kitwork.storage.set("theme", "dark");
if (await kitwork.storage.get("theme") !== "dark") throw new Error("web storage fallback failed");
localStorage.setItem("foreign", "keep");
await kitwork.storage.clear();
if (localStorage.getItem("foreign") !== "keep") throw new Error("storage.clear erased foreign data");

var state = { n: 0 };
var expression = kitwork.compile("inc = () => n = n + 1; false && inc(); true || inc(); n");
if (kitwork.run(expression, state) !== 0 || state.n !== 0) {
  throw new Error("logical operators did not short-circuit");
}

var invalidRejected = false;
try {
  kitwork.compile("n@");
} catch (_) {
  invalidRejected = true;
}
if (!invalidRejected) throw new Error("invalid source was silently accepted");

var oversized = [";"];
for (var i = 0; i < 10001; i++) oversized.push(["#", i]);
var budgetRejected = false;
try {
  kitwork.run(oversized, {});
} catch (error) {
  budgetRejected = error.message.indexOf("budget") >= 0;
}
if (!budgetRejected) throw new Error("client evaluation budget was not enforced");

kitwork.destroy();
if (kitwork.runtime.booted) throw new Error("destroy did not reset boot state");
if (kitwork.runtime.loaded) throw new Error("destroy did not reset load state");
if (!observerDisconnected) throw new Error("destroy did not disconnect the DOM observer");
if (removed === 0 || removed > added) throw new Error("global listener cleanup mismatch");
})().catch(function (error) {
  console.error(error);
  process.exitCode = 1;
});
`)
}
