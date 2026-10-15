package hydrate

import (
	"encoding/json"
	"html"
	"strings"
	"testing"
)

// The web kernel (KernelFile(false)) carries no native module. Its platform services must take the
// browser path there instead of reading .available off an undefined bridge: kit.clipboard.writeText
// reaches navigator.clipboard (stubbed here, a headless browser has no clipboard to read back).
// CONTROL: a bridge registered after the services were installed is used from then on, so the
// lookup is made when a service runs, not once when the module loads.
func TestBrowserWebServicesRunWithoutTheNativeBridge(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the web services browser proof in short mode")
	}
	browser := findHeadlessBrowserForDrive()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	page := `<!doctype html><html><head><meta charset="utf-8"><title>web services</title></head><body>
<pre id="result"></pre>
<script>
var copied = [];
Object.defineProperty(navigator, "clipboard", { configurable: true, value: {
  writeText: function (text) { copied.push(text); return Promise.resolve(); },
  readText: function () { return Promise.resolve("read"); }
} });
var facts = {};
function report() { document.getElementById("result").textContent = JSON.stringify(facts); }
window.addEventListener("load", function () { setTimeout(function () {
  facts.hasNative = !!window.kit.module("native");
  try { window.kit.clipboard.writeText("web").then(function () { facts.webResolved = true; }); facts.webThrew = ""; }
  catch (error) { facts.webThrew = String(error && error.message || error); }
  try { window.kit.storage.get("missing").then(function (value) { facts.storageValue = value === undefined || value === null ? "none" : String(value); }); facts.storageThrew = ""; }
  catch (error) { facts.storageThrew = String(error && error.message || error); }
  setTimeout(function () {
    facts.copied = copied.join(",");
    var calls = [];
    window.kit.module("native", { available: true, call: function (name, args) { calls.push(name + ":" + (args && args.text)); return Promise.resolve(); } });
    try { window.kit.clipboard.writeText("bridge"); facts.bridgeThrew = ""; }
    catch (error) { facts.bridgeThrew = String(error && error.message || error); }
    setTimeout(function () {
      facts.calls = calls.join(",");
      facts.copiedAfter = copied.join(",");
      report();
    }, 50);
  }, 50);
}, 50); });
</script>
<script data-kitwork-generated="runtime" src="/kit/kernel.js" defer></script>
</body></html>`
	output := runGroupedBrowserPage(t, browser, map[string]string{
		"/":              page,
		"/kit/kernel.js": KernelFile(false),
	}, "3000")
	match := groupedResultRe.FindSubmatch(output)
	if match == nil || len(strings.TrimSpace(string(match[1]))) == 0 {
		t.Fatalf("the page reported nothing:\n%.1500s", output)
	}
	var facts map[string]any
	if err := json.Unmarshal([]byte(html.UnescapeString(string(match[1]))), &facts); err != nil {
		t.Fatalf("the page's report is not JSON: %v\n%s", err, match[1])
	}
	want := map[string]any{
		"hasNative":    false, // the web kernel really has no bridge, or this proves nothing
		"webThrew":     "",
		"webResolved":  true,
		"copied":       "web",
		"storageThrew": "",
		"storageValue": "none",
		// control: a bridge that arrives later takes the call, and the browser clipboard is not used
		"bridgeThrew": "",
		"calls":       "clipboard.writeText:bridge",
		"copiedAfter": "web",
	}
	for key, value := range want {
		if facts[key] != value {
			t.Errorf("%s = %#v, want %#v", key, facts[key], value)
		}
	}
}
