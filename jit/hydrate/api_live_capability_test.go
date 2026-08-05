package hydrate

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// api and live left the core through the reconcile/destroy lifecycle seam. The jit/js tests prove the
// SERVING; this proves the BEHAVIOUR against the real core, composed exactly as the served asset is
// (hydrate.Runtime() + both capability modules). It pins the seam the whole refactor rests on:
//
//   live  — installs, opens ONE EventSource per data-kit-live URL, and a pushed JSON patch lands in
//           the region's nearest scope and repaints;
//   reconcile — a live region added AFTER boot gets wired when kitwork:load fires (the Drive-swap path
//           that used to be a hardcoded syncLive/syncApi call in the core);
//   api   — installs, marks a data-kit-api boundary data-state="loading" synchronously, then seeds the
//           boundary scope from the JSON and flips to "ready";
//   destroy — kit.destroy runs the live module's onDestroy hook, closing the EventSource.
//
// If the seam regressed (a module can't reach pageScope/boundaryScope/render, or reconcile/onDestroy
// stopped firing), one of these goes red.
func TestApiLiveCapabilitiesBehaviour(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for the api/live capability DOM test")
	}
	apiSrc, err := os.ReadFile(filepath.Join("..", "js", "capabilities", "api.js"))
	if err != nil {
		t.Fatalf("reading api capability: %v", err)
	}
	liveSrc, err := os.ReadFile(filepath.Join("..", "js", "capabilities", "live.js"))
	if err != nil {
		t.Fatalf("reading live capability: %v", err)
	}

	// Runs BEFORE the core + modules: stub EventSource + fetch, and build the two regions so the
	// modules' self-run on load wires them (the modules are appended after boot, like the real asset).
	const preamble = `
global.EventSource = function (url) { this.url = url; this.closed = false; this.onmessage = null; EventSource.instances.push(this); };
global.EventSource.prototype.close = function () { this.closed = true; };
global.EventSource.instances = [];
window.EventSource = global.EventSource;
global.fetch = function () {
  return Promise.resolve({ ok: true, json: function () { return Promise.resolve({ label: "Hi", n: 3 }); } });
};
window.fetch = global.fetch;

var bell = el("div", { "data-kit-scope": "bell", "data-kit-live": "/notifs" });
bell.appendChild(el("b", { "data-kit-text": "count" }));
document.body.appendChild(bell);

var apiBox = el("div", { "data-kit-api": "/data" });
apiBox.appendChild(el("b", { "data-kit-text": "label" }));
document.body.appendChild(apiBox);
`

	const assertions = `
var kit = window.kit;

// live INSTALLED and wired the region: one EventSource at the declared URL.
if (typeof kit.sync !== "function") throw new Error("live module did not install kit.sync");
if (EventSource.instances.length !== 1) throw new Error("live: expected 1 EventSource, got " + EventSource.instances.length);
if (EventSource.instances[0].url !== "/notifs") throw new Error("live: wrong URL " + EventSource.instances[0].url);

// A pushed JSON patch lands in the region's nearest scope and repaints.
EventSource.instances[0].onmessage({ data: JSON.stringify({ count: 7 }) });
if (kit.scopeFor(bell).count !== 7) throw new Error("live: patch not applied, count = " + kit.scopeFor(bell).count);

// RECONCILE lifecycle: a live region added AFTER boot is wired when kitwork:load fires.
var bell2 = el("div", { "data-kit-scope": "bell2", "data-kit-live": "/notifs2" });
document.body.appendChild(bell2);
document.dispatchEvent({ type: "kitwork:load" });
if (EventSource.instances.length !== 2) throw new Error("reconcile: kitwork:load did not wire the new region, got " + EventSource.instances.length);

// api INSTALLED and marked the boundary loading synchronously (before the fetch resolves).
if (typeof kit.syncApi !== "function") throw new Error("api module did not install kit.syncApi");
if (apiBox.getAttribute("data-state") !== "loading") throw new Error("api: boundary not marked loading, got " + apiBox.getAttribute("data-state"));

// After the fetch resolves (microtasks), the boundary scope is seeded and the state flips to ready;
// then destroy tears the live stream down.
setTimeout(function () {
  if (apiBox.getAttribute("data-state") !== "ready") throw new Error("api: expected ready, got " + apiBox.getAttribute("data-state"));
  if (kit.scopeFor(apiBox).label !== "Hi") throw new Error("api: scope not seeded, label = " + kit.scopeFor(apiBox).label);

  kit.destroy();
  if (!EventSource.instances[0].closed) throw new Error("destroy: live EventSource was not closed by the onDestroy hook");

  console.log("api + live capabilities: install + wire + patch + reconcile + seed + destroy OK");
}, 0);
`

	script := blockDOMShim + "\n" + preamble + "\n" + Runtime() + "\n" + string(apiSrc) + "\n" + string(liveSrc) + "\n" + assertions
	path := filepath.Join(t.TempDir(), "api_live.test.js")
	if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(node, path).CombinedOutput()
	if err != nil {
		t.Fatalf("api/live capability test failed:\n%s", out)
	}
	t.Logf("%s", out)
}
