package javascript

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestBrowserIgnoreSubtreeMatchesScannerOpacity locks the shared meaning of
// data-kit-ignore: the scanner and browser must both treat the host and every
// descendant as inert, opaque authored DOM. This test intentionally remains a
// browser-level parity gate because scanner-only coverage cannot detect event
// activation or diagnostics produced after boot.
func TestBrowserIgnoreSubtreeMatchesScannerOpacity(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping standalone KitJS ignore opacity contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	use, err := ScanHTML([]byte(ignoreOpacityMarkup))
	if err != nil {
		t.Fatalf("scanner rejected opaque data-kit-ignore markup: %v", err)
	}
	if !use.NeedsRuntime || len(use.Components) != 0 {
		t.Fatalf("scanner leaked ignored metadata into selection: %#v", use)
	}

	kitJS := readVanillaFile(t, "kit.js")
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/kit.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(kitJS)
		case "/contracts/ignore.html":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(ignoreOpacityDocument))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	runVanillaBrowser(t, browser, server.URL+"/contracts/ignore.html")
}

const ignoreOpacityMarkup = `<main data-kit-scope="ready: true;">
  <section data-kit-ignore data-kit-unknown="opaque">
    <a id="ignored-action" href="#changed" data-kit-click:prevent="ready">ignored action</a>
  </section>
</main>`

const ignoreOpacityDocument = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>KitJS ignore opacity contract</title>
  <script>
    globalThis.__kitIgnoreErrors = [];
    var originalError = console.error;
    console.error = function () {
      var message = [];
      for (var index = 0; index < arguments.length; index++) {
        var value = arguments[index];
        message.push(String(value && value.message || value));
      }
      globalThis.__kitIgnoreErrors.push(message.join(" "));
      return originalError.apply(this, arguments);
    };
  </script>
</head>
<body>
` + ignoreOpacityMarkup + `
<script src="/kit.js"></script>
<script>
` + browserHarness + `
__runStandaloneKitTest(async function () {
  var assert = __kitTestAssert;
  var nextTurn = __kitTestNextTurn;
  var link = document.getElementById("ignored-action");
  var event = new MouseEvent("click", { bubbles: true, cancelable: true });
  link.dispatchEvent(event);
  await nextTurn();

  assert(!event.defaultPrevented, "event directive inside data-kit-ignore was activated");
  assert(globalThis.__kitIgnoreErrors.length === 0,
    "opaque subtree emitted diagnostics: " + globalThis.__kitIgnoreErrors.join(" | "));
});
</script>
</body>
</html>`
