package javascript

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestBrowserHydrateDriveNavigation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Hydrate Drive browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	hydrateJS, err := SourceForProfile(ProfileHydrate)
	if err != nil {
		t.Fatal(err)
	}
	contractIntegrity := driveScriptIntegrity([]byte(hydrateDriveContractSource))
	hydrateIntegrity := driveScriptIntegrity(hydrateJS)

	var activeContentLoads atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/hydrate.kit.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(hydrateJS)
		case "/hydrate-drive-contract.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write([]byte(hydrateDriveContractSource))
		case "/drive.html":
			writeHydrateHTML(response, hydrateDriveInitialDocument(contractIntegrity, hydrateIntegrity))
		case "/slow":
			time.Sleep(300 * time.Millisecond)
			writeHydrateHTML(response, hydrateDriveRouteDocument("Slow", "Slow", "/assets/slow.css", contractIntegrity, hydrateIntegrity))
		case "/fast":
			writeHydrateHTML(response, hydrateDriveRouteDocument("Fast", "Fast", "/assets/fast.css", contractIntegrity, hydrateIntegrity))
		case "/search":
			writeHydrateHTML(response, hydrateDriveRouteDocument(
				"Search",
				"Search:"+request.URL.Query().Get("q"),
				"/assets/search.css",
				contractIntegrity,
				hydrateIntegrity,
			))
		case "/mismatch":
			if request.Header.Get("X-KitJS-Drive") == "1" {
				writeHydrateHTML(response, hydrateDriveMismatchDocument)
			} else {
				response.Header().Set("Set-Cookie", "kit_drive_hard_fallback=1; Path=/; SameSite=Lax")
				response.WriteHeader(http.StatusNoContent)
			}
		case "/unknown":
			if request.Header.Get("X-KitJS-Drive") == "1" {
				writeHydrateHTML(response, hydrateDriveUnknownComponentDocument)
			} else {
				response.Header().Set("Set-Cookie", "kit_drive_unknown_fallback=1; Path=/; SameSite=Lax")
				response.WriteHeader(http.StatusNoContent)
			}
		case "/base-mismatch":
			if request.Header.Get("X-KitJS-Drive") == "1" {
				writeHydrateHTML(response, hydrateDriveBaseMismatchDocument)
			} else {
				response.Header().Set("Set-Cookie", "kit_drive_base_fallback=1; Path=/; SameSite=Lax")
				response.WriteHeader(http.StatusNoContent)
			}
		case "/hazard/iframe", "/hazard/frame", "/hazard/object", "/hazard/embed", "/hazard/refresh":
			kind := strings.TrimPrefix(request.URL.Path, "/hazard/")
			if request.Header.Get("X-KitJS-Drive") == "1" {
				writeHydrateHTML(response, hydrateDriveHazardDocument(kind))
			} else {
				response.Header().Set("Set-Cookie", "kit_drive_"+kind+"_fallback=1; Path=/; SameSite=Lax")
				response.WriteHeader(http.StatusNoContent)
			}
		case "/active-payload":
			activeContentLoads.Add(1)
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(`<script>parent.__activeContentExecuted = true</script>`))
		case "/native":
			http.Error(response, "native navigation should have been prevented by the test", http.StatusTeapot)
		default:
			if strings.HasPrefix(request.URL.Path, "/assets/") {
				response.Header().Set("Content-Type", "text/css; charset=utf-8")
				_, _ = response.Write([]byte("/* hydrate drive browser fixture */\n"))
				return
			}
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	runVanillaBrowser(t, browser, server.URL+"/drive.html")
	if got := activeContentLoads.Load(); got != 0 {
		t.Fatalf("incoming active content loaded %d external payloads before hard fallback", got)
	}
}

func writeHydrateHTML(response http.ResponseWriter, source string) {
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = response.Write([]byte(source))
}

func hydrateDriveRouteDocument(title, route, stylesheet, contractIntegrity, hydrateIntegrity string) string {
	return fmt.Sprintf(`<!doctype html>
<html lang="vi" dir="ltr">
<head>
  <meta charset="utf-8">
  <meta name="description" content="%s description">
  <meta property="og:title" content="%s">
  <link rel="canonical" href="/%s-canonical">
  <link rel="stylesheet" href="%s">
  <style data-kit-head>#route-main { outline: none; }</style>
  <title>%s</title>
  <script defer src="/hydrate.kit.js?v=contract" integrity="%s" crossorigin="anonymous"></script>
  <script defer src="/hydrate-drive-contract.js" integrity="%s" crossorigin="anonymous"></script>
</head>
<body>
  %s
</body>
</html>`, title, title, strings.ToLower(title), stylesheet, title, hydrateIntegrity, contractIntegrity, hydrateDriveShell(route))
}

func hydrateDriveShell(route string) string {
	return fmt.Sprintf(`<header id="counter-shell" data-kit-component="drive-counter">
    <button id="counter-add" type="button" data-kit-click="count = count + 1">Increment</button>
    <output id="counter-output" data-kit-text="count">server</output>
  </header>
  <nav aria-label="Drive contract navigation">
    <a id="slow-link" href="/slow">Slow</a>
    <a id="fast-link" href="/fast">Fast</a>
    <a id="native-link" href="/native" data-kit-drive="false">Native</a>
    <a id="unknown-link" href="/unknown">Unknown component</a>
    <a id="mismatch-link" href="/mismatch">Mismatch</a>
    <a id="base-mismatch-link" href="/base-mismatch">Base mismatch</a>
    <a id="iframe-link" href="/hazard/iframe">Iframe hazard</a>
    <a id="frame-link" href="/hazard/frame">Frame hazard</a>
    <a id="object-link" href="/hazard/object">Object hazard</a>
    <a id="embed-link" href="/hazard/embed">Embed hazard</a>
    <a id="refresh-link" href="/hazard/refresh">Refresh hazard</a>
  </nav>
  <form id="search-form" action="/search" method="get">
    <input id="search-query" name="q" value="">
    <button type="submit">Search</button>
  </form>
  <main id="route-main">%s</main>`, route)
}

func hydrateDriveInitialDocument(contractIntegrity, hydrateIntegrity string) string {
	return fmt.Sprintf(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="description" content="Initial description">
  <meta property="og:title" content="Initial">
  <link rel="canonical" href="/initial-canonical">
  <link rel="stylesheet" href="/assets/initial.css">
  <style data-kit-head>#route-main { outline: none; }</style>
  <title>Initial</title>
  <script defer src="/hydrate.kit.js?v=contract" integrity="%s" crossorigin="anonymous"></script>
  <script defer src="/hydrate-drive-contract.js" integrity="%s" crossorigin="anonymous"></script>
</head>
<body>
  %s
</body>
</html>`, hydrateIntegrity, contractIntegrity, hydrateDriveShell("Initial"))
}

const hydrateDriveContractSource = `globalThis.__initialScriptRuns = (globalThis.__initialScriptRuns || 0) + 1;
globalThis.__incomingScriptRuns = 0;
kit.component("drive-counter", { count: 0 });
` + browserHarness + `
` + hydrateDriveAssertions

const hydrateDriveAssertions = `__runStandaloneKitTest(async function () {
  var assert = __kitTestAssert;
  var waitFor = __kitTestWaitFor;
  var nextTurn = __kitTestNextTurn;

  assert(Object.keys(kit).join(",") === "version,component", "Hydrate leaked a public API: " + Object.keys(kit).join(","));
  assert(!document.querySelector("[data-kit-app],[data-kit-hydrate]"), "Hydrate required an activation marker");
  assert(globalThis.__initialScriptRuns === 1, "initial authored script did not execute exactly once");
  await waitFor(function () { return document.getElementById("counter-output").textContent === "0"; }, "counter did not boot");

  document.getElementById("counter-add").click();
  await waitFor(function () { return document.getElementById("counter-output").textContent === "1"; }, "counter did not increment");

  var realFetch = globalThis.fetch.bind(globalThis);
  var fetches = [];
  globalThis.fetch = function (source, options) {
    fetches.push(String(source));
    return realFetch(source, options);
  };

  var nativeAllowed = null;
  function observeNative(event) {
    var link = event.target.closest && event.target.closest("#native-link");
    if (!link) return;
    nativeAllowed = !event.defaultPrevented;
    event.preventDefault();
  }
  document.addEventListener("click", observeNative);
  document.getElementById("native-link").click();
  await nextTurn();
  document.removeEventListener("click", observeNative);
  assert(nativeAllowed === true, "data-kit-drive=false was intercepted");
  assert(fetches.every(function (url) { return new URL(url).pathname !== "/native"; }), "opted-out link was fetched");

  document.getElementById("slow-link").click();
  document.getElementById("fast-link").click();
  await waitFor(function () {
    return location.pathname === "/fast" && document.getElementById("route-main").textContent.trim() === "Fast";
  }, "latest navigation did not win");

  assert(fetches.some(function (url) { return new URL(url).pathname === "/slow"; }), "slow visit was not started");
  assert(fetches.some(function (url) { return new URL(url).pathname === "/fast"; }), "fast visit was not started");
  assert(fetches.filter(function (url) { return new URL(url).pathname === "/fast"; }).length === 1,
    "loading the Hydrate artifact twice installed duplicate Drive listeners");
  assert(document.title === "Fast", "title was not reconciled");
  assert(document.documentElement.lang === "vi" && document.documentElement.dir === "ltr", "safe html attributes were not reconciled");
  assert(document.querySelector('meta[name="description"]').content === "Fast description", "description metadata was not reconciled");
  assert(document.querySelector('meta[property="og:title"]').content === "Fast", "property metadata was not reconciled");
  assert(!document.querySelector('meta[http-equiv="refresh"]'), "unsafe refresh metadata entered the live head");
  assert(new URL(document.querySelector('link[rel="canonical"]').href).pathname === "/fast-canonical", "canonical link was not reconciled");
  assert(new URL(document.querySelector('link[rel="stylesheet"]').href).pathname === "/assets/fast.css", "stylesheet was not reconciled");
  assert(globalThis.__incomingScriptRuns === 0, "an incoming script executed");
  assert(!document.querySelector("script[data-incoming-script]"), "an incoming body script entered the live DOM");
  assert(globalThis.__initialScriptRuns === 1, "current document script ownership changed");
  assert(document.getElementById("counter-output").textContent === "1", "compatible component state was not preserved");
  assert(document.activeElement && document.activeElement.id === "route-main", "new route was not focused");

  var query = document.getElementById("search-query");
  query.value = "kit js";
  document.getElementById("search-form").requestSubmit();
  await waitFor(function () {
    return location.pathname === "/search" && location.search === "?q=kit+js" &&
      document.getElementById("route-main").textContent.trim() === "Search:kit js";
  }, "GET form navigation did not commit");
  assert(document.getElementById("counter-output").textContent === "1", "form navigation reset component state");

  history.back();
  await waitFor(function () {
    return location.pathname === "/fast" && document.getElementById("route-main").textContent.trim() === "Fast";
  }, "popstate navigation did not restore the prior route");

  document.getElementById("unknown-link").click();
  await waitFor(function () { return document.cookie.indexOf("kit_drive_unknown_fallback=1") >= 0; },
    "unknown component did not hard-navigate");
  assert(location.pathname === "/fast", "unknown component committed a Drive URL");
  assert(document.title === "Fast" && document.getElementById("route-main").textContent.trim() === "Fast",
    "unknown component mutated the document before hard fallback");
  assert(!document.querySelector('[data-kit-component="missing-drive-component"]'),
    "unknown component boundary entered the live body");

  document.getElementById("mismatch-link").click();
  await waitFor(function () { return document.cookie.indexOf("kit_drive_hard_fallback=1") >= 0; },
    "artifact mismatch did not hard-navigate");
  assert(location.pathname === "/fast", "204 hard fallback unexpectedly committed a Drive URL");
  assert(document.title === "Fast", "artifact mismatch changed title before hard fallback");
  assert(document.getElementById("route-main").textContent.trim() === "Fast", "artifact mismatch changed body before hard fallback");
  assert(document.getElementById("counter-output").textContent.trim() === "1", "artifact mismatch changed component state before hard fallback");
  assert(globalThis.__incomingScriptRuns === 0, "artifact mismatch executed an incoming script");

  function assertFastDocument(label) {
    assert(location.pathname === "/fast", label + " committed a Drive URL");
    assert(document.title === "Fast", label + " changed title before hard fallback");
    assert(document.documentElement.lang === "vi" && document.documentElement.dir === "ltr",
      label + " changed document attributes before hard fallback");
    assert(document.querySelector('meta[name="description"]').content === "Fast description",
      label + " changed head metadata before hard fallback");
    assert(document.getElementById("route-main").textContent.trim() === "Fast",
      label + " changed body before hard fallback");
    assert(document.getElementById("counter-output").textContent.trim() === "1",
      label + " changed component state before hard fallback");
    assert(globalThis.__incomingScriptRuns === 0 && globalThis.__activeContentExecuted !== true,
      label + " executed incoming active content");
  }

  document.getElementById("base-mismatch-link").click();
  await waitFor(function () { return document.cookie.indexOf("kit_drive_base_fallback=1") >= 0; },
    "base semantic mismatch did not hard-navigate");
  assertFastDocument("base semantic mismatch");

  var hazards = ["iframe", "frame", "object", "embed", "refresh"];
  for (var hazardIndex = 0; hazardIndex < hazards.length; hazardIndex++) {
    var hazard = hazards[hazardIndex];
    document.getElementById(hazard + "-link").click();
    await waitFor(function () {
      return document.cookie.indexOf("kit_drive_" + hazard + "_fallback=1") >= 0;
    }, hazard + " active content did not hard-navigate");
    assertFastDocument(hazard + " active content");
    assert(!document.querySelector("iframe,frame,object,embed,meta[http-equiv]"),
      hazard + " active content entered the live document");
  }
});`

const hydrateDriveMismatchDocument = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="description" content="Mutated before fallback">
  <title>Mutated before fallback</title>
  <script src="/hydrate.kit.js?v=different-artifact"></script>
</head>
<body>
  <main id="route-main">Mutated before fallback</main>
  <script>globalThis.__incomingScriptRuns = 999;</script>
</body>
</html>`

const hydrateDriveUnknownComponentDocument = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="description" content="Unknown component">
  <title>Unknown component</title>
  <script src="/hydrate.kit.js?v=contract"></script>
</head>
<body>
  <template><section data-kit-component="missing-drive-component">Must not commit</section></template>
</body>
</html>`

const hydrateDriveBaseMismatchDocument = `<!doctype html>
<html lang="poisoned">
<head>
  <meta charset="utf-8">
  <base href="/different-document-base/">
  <meta name="description" content="Base mismatch mutated the head">
  <title>Base mismatch mutated title</title>
  <script src="/hydrate.kit.js?v=contract"></script>
</head>
<body>
  <main id="route-main">Base mismatch mutated body</main>
  <script>globalThis.__incomingScriptRuns = 777;</script>
</body>
</html>`

func hydrateDriveHazardDocument(kind string) string {
	if kind == "frame" {
		return `<!doctype html>
<html lang="poisoned">
<head>
  <meta charset="utf-8">
  <meta name="description" content="frame hazard mutated the head">
  <title>frame hazard mutated title</title>
  <script src="/hydrate.kit.js?v=contract"></script>
</head>
<frameset cols="100%">
  <frame src="/active-payload?kind=frame">
  <noframes>frame hazard mutated body</noframes>
</frameset>
</html>`
	}
	hazard := map[string]string{
		"iframe":  `<iframe src="/active-payload?kind=iframe" srcdoc="<script>parent.__activeContentExecuted=true<\/script>"></iframe>`,
		"object":  `<object data="/active-payload?kind=object" type="text/html"></object>`,
		"embed":   `<embed src="/active-payload?kind=embed" type="text/html">`,
		"refresh": `<meta http-equiv="refresh" content="0;url=/active-payload?kind=refresh">`,
	}[kind]
	headHazard := ""
	bodyHazard := hazard
	if kind == "refresh" {
		headHazard = hazard
		bodyHazard = ""
	}
	return fmt.Sprintf(`<!doctype html>
<html lang="poisoned">
<head>
  <meta charset="utf-8">
  <meta name="description" content="%s hazard mutated the head">
  <title>%s hazard mutated title</title>
  %s
  <script src="/hydrate.kit.js?v=contract"></script>
</head>
<body>
  <main id="route-main">%s hazard mutated body</main>
  %s
  <script>globalThis.__incomingScriptRuns = 888;</script>
</body>
</html>`, kind, kind, headHazard, kind, bodyHazard)
}
