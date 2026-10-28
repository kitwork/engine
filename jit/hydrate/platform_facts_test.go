package hydrate

import "testing"

// kitwork.platform and kitwork.isNative are the RUNTIME's view of whether a native shell is present. They
// used to live in bridge.js, which made them disappear on any page that did not ship the bridge —
// and web.js throws KitworkError from theme.set(), so the bridge could not simply be dropped either.
//
// They belong to the kernel: derived from kitwork.bridge, correct whether or not a bridge was ever
// loaded, and correct again the moment a shell attaches one late.
func TestPlatformFactsFollowTheBridge(t *testing.T) {
	const assertions = `
var kitwork = window.kitwork;

// No bridge: a plain web page.
if (kitwork.platform !== "web") throw new Error("platform without a bridge should be web, got " + kitwork.platform);
if (kitwork.isNative !== false) throw new Error("isNative without a bridge should be false");
if (kitwork.runtime.info().engine !== "web") throw new Error("runtime engine should be web, got " + kitwork.runtime.info().engine);
if (typeof kitwork.KitworkError !== "function") throw new Error("the kernel must carry the error shape");

var err = kitwork.KitworkError("nope", "UNSUPPORTED", "theme", "set", { mode: "sepia" });
if (err.name !== "KitworkError" || err.code !== "UNSUPPORTED" || err.module !== "theme") {
  throw new Error("KitworkError lost its shape: " + err.name + "/" + err.code + "/" + err.module);
}

// A shell attaches an adapter later: the facts must follow, not stay stale.
kitwork.bridge = { call: function () { return Promise.resolve(true); }, platform: "webview2" };
if (kitwork.platform !== "webview2") throw new Error("platform did not follow the bridge, got " + kitwork.platform);
if (kitwork.isNative !== true) throw new Error("isNative did not follow the bridge");
if (kitwork.runtime.info().engine !== "webview2") throw new Error("runtime engine did not follow, got " + kitwork.runtime.info().engine);

// An adapter with no call() is not a bridge.
kitwork.bridge = { platform: "pretend" };
if (kitwork.platform !== "web" || kitwork.isNative !== false) {
  throw new Error("an adapter without call() must not count as native");
}

kitwork.bridge = null;
console.log("platform facts: web by default, follow a late bridge, ignore a fake one");
`
	runNodeDOMScript(t, "platform_facts.test.js", assertions)
}
