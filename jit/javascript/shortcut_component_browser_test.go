package javascript

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestBrowserShortcutExactChordDriveFocusAndCleanup(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping shortcut browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := composer.ComposeStandalone([]ComponentRef{{Name: "shortcut", Version: "1.0.0"}}, true)
	if err != nil {
		t.Fatal(err)
	}
	bundleIntegrity := driveScriptIntegrity(bundle.JavaScript)
	contractSource := shortcutExternalContract(t)
	contractIntegrity := driveScriptIntegrity(contractSource)

	var searchDrive atomic.Int64
	var searchFull atomic.Int64
	var nextDrive atomic.Int64
	var nextFull atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/shortcut.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(bundle.JavaScript)
			return
		case "/shortcut-contract.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(contractSource)
			return
		case "/":
			writeShortcutHTML(response, shortcutDriveDocument(
				"home", "/search?q=kit#docs-search-input", false, bundleIntegrity, contractIntegrity))
			return
		case "/search":
			if request.Header.Get("X-KitJS-Drive") == "1" {
				searchDrive.Add(1)
			} else {
				searchFull.Add(1)
			}
			writeShortcutHTML(response, shortcutDriveDocument(
				"search", "/search?q=kit#docs-search-input", true, bundleIntegrity, contractIntegrity))
			return
		case "/next":
			if request.Header.Get("X-KitJS-Drive") == "1" {
				nextDrive.Add(1)
			} else {
				nextFull.Add(1)
			}
			writeShortcutHTML(response, shortcutDriveDocument(
				"next", "/next", false, bundleIntegrity, contractIntegrity))
			return
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	runVanillaBrowser(t, browser, server.URL+"/")
	if got := searchDrive.Load(); got != 1 {
		t.Fatalf("shortcut search Drive requests = %d, want 1", got)
	}
	if got := nextDrive.Load(); got != 1 {
		t.Fatalf("shortcut next Drive requests = %d, want 1", got)
	}
	if searchFull.Load() != 0 || nextFull.Load() != 0 {
		t.Fatalf("shortcut escaped Drive: search full=%d next full=%d", searchFull.Load(), nextFull.Load())
	}
}

func writeShortcutHTML(response http.ResponseWriter, source string) {
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = response.Write([]byte(source))
}

func shortcutDriveDocument(route, href string, searchInput bool, bundleIntegrity, contractIntegrity string) string {
	var output strings.Builder
	output.WriteString(`<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Shortcut ` + route + `</title>` +
		`<script defer src="/shortcut.js" integrity="` + bundleIntegrity + `" crossorigin="anonymous"></script>` +
		`<script defer src="/shortcut-contract.js" integrity="` + contractIntegrity + `" crossorigin="anonymous"></script>` +
		`</head><body>
<header><a id="shortcut" href="` + href + `" data-kit-component="shortcut" data-kit-version="1.0.0" data-shortcut="mod+k" aria-keyshortcuts="Control+K Meta+K">Search</a></header>
<main id="main" tabindex="-1"><h1 id="route">` + route + `</h1>`)
	if searchInput {
		output.WriteString(`<label for="docs-search-input">Search docs</label><input id="docs-search-input" type="search" autofocus>`)
	}
	output.WriteString(`</main>`)
	output.WriteString(`</body></html>`)
	return output.String()
}

func shortcutExternalContract(t *testing.T) []byte {
	t.Helper()
	legacy := shortcutLegacyDriveDocument("home", "/search?q=kit#docs-search-input", false)
	start := strings.Index(legacy, "<script>")
	if start < 0 {
		t.Fatal("shortcut legacy fixture omitted its inline browser contract")
	}
	contract := legacy[start+len("<script>"):]
	end := strings.Index(contract, "</script>")
	if end < 0 {
		t.Fatal("shortcut legacy fixture has an unterminated inline browser contract")
	}
	if strings.Contains(contract[end+len("</script>"):], "<script>") {
		t.Fatal("shortcut legacy fixture has more than one inline browser contract")
	}
	return []byte(contract[:end])
}

func shortcutLegacyDriveDocument(route, href string, searchInput bool) string {
	var output strings.Builder
	output.WriteString(`<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Shortcut ` + route + `</title><script src="/shortcut.js"></script></head><body>
<header><a id="shortcut" href="` + href + `" data-kit-component="shortcut" data-kit-version="1.0.0" data-shortcut="mod+k" aria-keyshortcuts="Control+K Meta+K">Search</a></header>
<main id="main" tabindex="-1"><h1 id="route">` + route + `</h1>`)
	if searchInput {
		output.WriteString(`<label for="docs-search-input">Search docs</label><input id="docs-search-input" type="search" autofocus>`)
	}
	output.WriteString(`</main><script>` + browserHarness + `
__runStandaloneKitTest(async function () {
  var assert = __kitTestAssert;
  var waitFor = __kitTestWaitFor;
  var nextTurn = __kitTestNextTurn;
  var clicks = 0;
  document.addEventListener("click", function (event) {
    if (event.target && event.target.closest && event.target.closest("#shortcut")) clicks++;
  });
  await waitFor(function () { return globalThis.kit && document.getElementById("shortcut"); },
    "shortcut did not boot");

  function key(options) {
    var event = new KeyboardEvent("keydown", Object.assign({
      key: "k", bubbles: true, cancelable: true
    }, options || {}));
    return document.dispatchEvent(event);
  }
  assert(key({}) === true, "plain k was cancelled");
  assert(key({ ctrlKey: true, altKey: true }) === true, "Alt+Ctrl+K was cancelled");
  assert(key({ ctrlKey: true, shiftKey: true }) === true, "Shift+Ctrl+K was cancelled");
  assert(key({ ctrlKey: true, metaKey: true }) === true, "Ctrl+Meta+K was cancelled");
  assert(key({ ctrlKey: true, repeat: true }) === true, "repeated Ctrl+K was cancelled");
  assert(key({ ctrlKey: true, isComposing: true }) === true, "composing Ctrl+K was cancelled");
  assert(clicks === 0 && location.pathname === "/", "an invalid chord activated the host");

  assert(key({ ctrlKey: true }) === false, "Ctrl+K was not cancelled");
  await waitFor(function () {
    return location.pathname === "/search" && location.search === "?q=kit" &&
      location.hash === "#docs-search-input" &&
      document.getElementById("route").textContent === "search";
  }, "Ctrl+K did not use Drive to reach search");
  assert(clicks === 1, "Ctrl+K activated the shortcut more than once");
  assert(document.activeElement && document.activeElement.id === "docs-search-input",
    "Drive did not focus the autofocus search field");

  document.getElementById("main").focus();
  assert(document.activeElement.id === "main", "fixture did not move focus away from search");
  assert(key({ metaKey: true }) === false, "Meta+K was not cancelled on the search route");
  await waitFor(function () { return document.activeElement.id === "docs-search-input"; },
    "same-document shortcut did not focus its fragment target");
  assert(clicks === 2 && location.pathname === "/search", "same-document shortcut navigated twice");

  document.getElementById("shortcut").setAttribute("href", "/next");
  assert(key({ ctrlKey: true }) === false, "Ctrl+K was not cancelled after Drive morph");
  await waitFor(function () {
    return location.pathname === "/next" && document.getElementById("route").textContent === "next";
  }, "Ctrl+K did not use the reconciled shortcut host");
  assert(clicks === 3, "Drive lifecycle left duplicate shortcut listeners");

  document.getElementById("shortcut").remove();
  await nextTurn();
  assert(key({ ctrlKey: true }) === true, "disposed shortcut still cancelled Ctrl+K");
  assert(clicks === 3 && location.pathname === "/next", "disposed shortcut still activated");
});
</script>`)
	output.WriteString(`</body></html>`)
	return output.String()
}
