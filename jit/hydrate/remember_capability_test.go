package hydrate

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// remember was lifted out of the core into jit/js/capabilities/remember.js. The Go tests in jit/js
// prove the SERVING (it is emitted + injected for a data-kit-remember page). This proves the other
// half — that the extracted module, composed after the core exactly as the served asset composes it
// (hydrate.Runtime() + the capability module), still WORKS: it installs through the kit.internal seam
// and page-scope $ keys restore from and persist to localStorage.
//
// The module source is read from its jit/js home (go test runs with CWD = this package dir). If the
// seam the module depends on (kit.internal.pageScope / scheduleRender) ever regressed, this goes red.
func TestRememberCapabilityRestoresAndPersists(t *testing.T) {
	node := requireNode(t)
	moduleSrc, err := os.ReadFile(filepath.Join("..", "js", "capabilities", "remember.js"))
	if err != nil {
		t.Fatalf("reading the remember capability module: %v", err)
	}

	// Runs BEFORE the core + module: a real in-memory localStorage (the shim's is a stub), a preloaded
	// remembered value, and the declaration in the DOM so the module's loadRemembered scan finds it.
	const preamble = `
(function () {
  var store = {};
  global.localStorage = {
    getItem: function (k) { return Object.prototype.hasOwnProperty.call(store, k) ? store[k] : null; },
    setItem: function (k, v) { store[k] = String(v); },
    removeItem: function (k) { delete store[k]; }
  };
  window.localStorage = global.localStorage;
})();
localStorage.setItem("kitwork:remember:bip01", "true");
document.body.appendChild(el("div", { "data-kit-remember": "bip01" }));
`

	const assertions = `
var kit = window.kit;

// The appended module installed its public API through the seam — proof the extraction wired up.
if (typeof kit.remember !== "function") throw new Error("remember module did not install kit.remember");

// RESTORE: bip01 was in storage before boot; the declared key reads back through the page scope.
if (kit.scope.bip01 !== true) throw new Error("restore: expected $.bip01 === true from storage, got " + kit.scope.bip01);

// PERSIST: writing the page-scope key mirrors to storage under the remember prefix.
kit.scope.bip01 = false;
if (localStorage.getItem("kitwork:remember:bip01") !== "false") throw new Error("persist: storage not updated, got " + localStorage.getItem("kitwork:remember:bip01"));

// PROGRAMMATIC: kit.remember(key) registers a fresh key; then it too round-trips through storage.
kit.remember("bip02");
kit.scope.bip02 = true;
if (localStorage.getItem("kitwork:remember:bip02") !== "true") throw new Error("programmatic: bip02 not persisted, got " + localStorage.getItem("kitwork:remember:bip02"));

console.log("remember capability: restore + persist + programmatic OK (module out of core, installed via kit.internal seam)");
`

	script := blockDOMShim + "\n" + preamble + "\n" + Runtime() + "\n" + string(moduleSrc) + "\n" + assertions
	path := filepath.Join(t.TempDir(), "remember.test.js")
	if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(node, path).CombinedOutput()
	if err != nil {
		t.Fatalf("remember capability test failed:\n%s", out)
	}
	t.Logf("%s", out)
}
