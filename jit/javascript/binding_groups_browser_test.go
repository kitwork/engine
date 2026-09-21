package javascript

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// data-kit-bind:<name> — one form, three groups (ideaship-final §5): a reflected boolean lands on
// the property AND the attribute; a live property lands on the property only, so the authored
// attribute still serves a form reset; anything hyphenated or unknown is an attribute, with aria-*
// spelled "true"/"false" and other booleans present-or-absent.
func TestBrowserBindingGroupsWriteWhereTheSpecSays(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping binding groups browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := composer.ComposeStandalone([]ComponentRef{{Name: "switch", Version: "1.0.0"}}, false)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/kit.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(bundle.JavaScript)
		case "/bind.html":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(bindingGroupsDocument))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	runVanillaBrowser(t, browser, server.URL+"/bind.html")
}

var bindingGroupsDocument = fmt.Sprintf(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>KitJS binding groups</title></head><body>
  <form id="form" data-kit-scope="on: true, busy: false, label: 'Save', tone: 'warm', text: 'seed', level: 3">
    <button id="button" type="button" data-kit-bind:disabled="busy" data-kit-bind:title="label" data-kit-bind:aria-busy="busy" data-kit-bind:aria-expanded="on" data-kit-bind:data-tone="tone" data-kit-bind:data-lit="on">go</button>
    <input id="check" type="checkbox" data-kit-bind:checked="on">
    <input id="text" type="text" value="authored" data-kit-bind:value="text">
    <details id="details" data-kit-bind:open="on"><summary>more</summary></details>
    <progress id="progress" max="10" data-kit-bind:value="level"></progress>
    <button id="flip" type="button" data-kit-click="on = !on; busy = !busy; text = 'typed'; tone = null">flip</button>
  </form>
  <script src="/kit.js"></script><script>
%s
__runStandaloneKitTest(async function () {
  var waitFor = __kitTestWaitFor;
  var assert = __kitTestAssert;
  var button = document.getElementById("button");
  var check = document.getElementById("check");
  var text = document.getElementById("text");
  var details = document.getElementById("details");

  await waitFor(function () { return button.title === "Save" && check.checked === true; }, "bindings did not render");
  // Reflected boolean: property and attribute agree.
  assert(button.disabled === false && !button.hasAttribute("disabled"), "disabled=false should clear both property and attribute");
  assert(details.open === true && details.hasAttribute("open"), "open=true should set both property and attribute");
  // Live property: the property moves, the authored attribute stays for a form reset.
  assert(check.checked === true && !check.hasAttribute("checked"), "checked should be a property, not an attribute");
  assert(text.value === "seed" && text.getAttribute("value") === "authored", "value should be the property; the attribute keeps the authored reset value");
  assert(document.getElementById("progress").value === 3, "a plain property the element has (progress.value) is written as a property");
  // Attribute-only: aria in words, data-* present-or-absent, plain strings as strings.
  assert(button.getAttribute("aria-busy") === "false" && button.getAttribute("aria-expanded") === "true", "aria booleans should be spelled out");
  assert(button.getAttribute("data-tone") === "warm" && button.hasAttribute("data-lit") && button.getAttribute("data-lit") === "", "data-* strings and booleans");

  document.getElementById("flip").click();
  await waitFor(function () { return button.disabled === true; }, "flip did not re-render");
  assert(button.hasAttribute("disabled"), "disabled=true should set the attribute too");
  assert(check.checked === false && !check.hasAttribute("checked"), "checked=false should clear the property only");
  assert(text.value === "typed" && text.getAttribute("value") === "authored", "value=typed should not touch the attribute");
  assert(details.open === false && !details.hasAttribute("open"), "open=false should clear both");
  assert(button.getAttribute("aria-busy") === "true" && button.getAttribute("aria-expanded") === "false", "aria flipped");
  assert(!button.hasAttribute("data-tone") && !button.hasAttribute("data-lit"), "null and false remove a data-* attribute");
});
  </script>
</body></html>`, browserHarness)
