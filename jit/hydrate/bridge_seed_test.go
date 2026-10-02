package hydrate

import "testing"

// The native shells seed their adapter under a Symbol on document, and answer through it — never
// with a message event. The kernel must adopt that adapter, even when a WebView handle is also there
// (a Bridge on the handle would send requests and never hear back). A slot with the wrong contract
// version is not adopted: the control, falling back to the handle.
func TestBridgeAdoptsTheShellsSeededHost(t *testing.T) {
	runNodeSourceTest(t, bridgeJS,
		`
var host = {
  version: "1.0.0",
  call: function (action, params) { return Promise.resolve({ via: "seed", action: action, params: params }); }
};
var posted = [];
var webview = { postMessage: function (message) { posted.push(message); }, addEventListener: function () {} };
global.window = { chrome: { webview: webview }, document: {} };
window.document[Symbol.for("kitwork:native:host:v1")] = host;
`,
		`
(async function () {
  if (window.kit.bridge !== host) throw new Error("the shell's seeded host was not adopted");
  var answer = await window.kit.bridge.call("window.isMaximized", {});
  if (answer.via !== "seed" || answer.action !== "window.isMaximized") throw new Error("the call did not go through the seed");
  if (posted.length !== 0) throw new Error("a request went to the raw WebView handle");
  if (window.document[Symbol.for("kitwork:native:host:v1")] !== host) throw new Error("the slot must stay for the shell's own scripts");
})().catch(function (error) { console.error(error); process.exitCode = 1; });
`)
	runNodeSourceTest(t, bridgeJS,
		`
var webview = { postMessage: function () {}, addEventListener: function () {} };
global.window = { chrome: { webview: webview }, document: {} };
window.document[Symbol.for("kitwork:native:host:v1")] = { version: "9.9.9", call: function () {} };
`,
		`
if (!window.kit.bridge || window.kit.bridge.handle !== webview) throw new Error("CONTROL: an unknown contract version must not be adopted");
`)
}
