package javascript

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestBrowserDriveUnicodeFragmentsAndBoundedScrollHistory(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Drive Unicode-fragment and scroll-history browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	hydrateJS, err := SourceForProfile(ProfileHydrate)
	if err != nil {
		t.Fatal(err)
	}
	hydrateIntegrity := driveScriptIntegrity(hydrateJS)

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/drive-fragment-contract.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write([]byte(driveFragmentContractSource))
		case "/hydrate.kit.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(hydrateJS)
		case "/drive-fragments":
			if request.Header.Get("X-KitJS-Drive") == "1" {
				writeDriveFragmentHTML(response, driveFragmentRouteDocument("Fragments", "/drive-fragments", hydrateIntegrity))
				return
			}
			writeDriveFragmentHTML(response, driveFragmentInitialDocument(hydrateIntegrity))
		case "/drive-fragments-next":
			writeDriveFragmentHTML(response, driveFragmentRouteDocument("Next", "/drive-fragments-next", hydrateIntegrity))
		case "/drive-fragments-redirect":
			http.Redirect(response, request, "/drive-fragments-next", http.StatusFound)
		case "/drive-fragments-matrix-encoded":
			writeDriveFragmentHTML(response, driveFragmentRouteDocumentWithLiteral("Encoded", request.URL.Path, hydrateIntegrity))
		case "/drive-fragments-matrix-named",
			"/drive-fragments-matrix-malformed",
			"/drive-fragments-matrix-invalid-utf8-raw",
			"/drive-fragments-matrix-nfd",
			"/drive-fragments-matrix-selector":
			writeDriveFragmentHTML(response, driveFragmentRouteDocument("Matrix", request.URL.Path, hydrateIntegrity))
		case "/drive-fragments-matrix-invalid-utf8-replacement":
			writeDriveFragmentHTML(response, driveFragmentRouteDocumentWithReplacement("Replacement", request.URL.Path, hydrateIntegrity))
		case "/drive-fragments-matrix-empty":
			writeDriveFragmentHTML(response, driveFragmentRouteDocument("Empty", request.URL.Path, hydrateIntegrity))
		case "/drive-fragments-pop-slow":
			if request.Header.Get("X-KitJS-Drive") == "1" {
				time.Sleep(400 * time.Millisecond)
			}
			writeDriveFragmentHTML(response, driveFragmentRouteDocument("Slow", request.URL.Path, hydrateIntegrity))
		case "/drive-fragments-pop-fast":
			writeDriveFragmentHTML(response, driveFragmentRouteDocument("Fast", request.URL.Path, hydrateIntegrity))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	runDriveFragmentBrowser(t, browser, server.URL+"/drive-fragments")
}

func runDriveFragmentBrowser(t *testing.T, browser, target string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	args := []string{
		"--headless=new",
		"--disable-gpu",
		"--no-sandbox",
		"--disable-dev-shm-usage",
		"--disable-background-networking",
		"--disable-default-apps",
		"--disable-extensions",
		"--disable-sync",
		"--metrics-recording-only",
		"--no-first-run",
		"--run-all-compositor-stages-before-draw",
		"--user-data-dir=" + t.TempDir(),
		"--virtual-time-budget=12000",
		"--dump-dom",
		target,
	}
	output, runErr := exec.CommandContext(ctx, browser, args...).CombinedOutput()
	if bytes.Contains(output, []byte(`data-kit-test="passed"`)) {
		return
	}
	if ctx.Err() != nil {
		t.Fatalf("Drive fragment browser proof timed out: %v\n%s", ctx.Err(), boundedVanillaOutput(output))
	}
	if runErr != nil {
		t.Fatalf("Drive fragment browser proof failed to run: %v\n%s", runErr, boundedVanillaOutput(output))
	}
	t.Fatalf("Drive fragment browser proof did not pass\n%s", boundedVanillaOutput(output))
}

func writeDriveFragmentHTML(response http.ResponseWriter, source string) {
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = response.Write([]byte(source))
}

func driveFragmentPage(route string) string {
	return fmt.Sprintf(`<nav>
  <a id="raw-unicode-link" href="#á">Raw Unicode</a>
  <a id="encoded-unicode-link" href="#%%C3%%A1">Encoded Unicode</a>
  <a id="nfd-unicode-link" href="#a&#x0301;">NFD Unicode</a>
  <a id="named-unicode-link" href="#tên">Named Unicode</a>
  <a id="malformed-link" href="#%%ZZ">Malformed percent</a>
  <a id="invalid-utf8-link" href="#%%C3%%28">Invalid UTF-8</a>
  <a id="selector-link" href="#x%%22%%5D%%2Cbody%%5Bdata-poison%%3D%%22">Selector text</a>
  <a id="top-link" href="#top">Top</a>
  <a id="empty-link" href="#">Empty</a>
  <a id="next-link" href="/drive-fragments-redirect#á">Next</a>
  <a id="matrix-encoded-link" href="/drive-fragments-matrix-encoded#%%C3%%A1">Cross encoded</a>
  <a id="matrix-named-link" href="/drive-fragments-matrix-named#tên">Cross named</a>
  <a id="matrix-malformed-link" href="/drive-fragments-matrix-malformed#%%ZZ">Cross malformed</a>
  <a id="matrix-invalid-utf8-raw-link" href="/drive-fragments-matrix-invalid-utf8-raw#%%C3%%28">Cross invalid UTF-8 raw</a>
  <a id="matrix-invalid-utf8-replacement-link" href="/drive-fragments-matrix-invalid-utf8-replacement#%%C3%%28">Cross invalid UTF-8 replacement</a>
  <a id="matrix-nfd-link" href="/drive-fragments-matrix-nfd#a&#x0301;">Cross NFD</a>
  <a id="matrix-selector-link" href="/drive-fragments-matrix-selector#x%%22%%5D%%2Cbody%%5Bdata-poison%%3D%%22">Cross selector</a>
  <a id="matrix-empty-link" href="/drive-fragments-matrix-empty#">Cross empty</a>
  <a id="pop-slow-link" href="/drive-fragments-pop-slow">Pop slow</a>
  <a id="pop-fast-link" href="/drive-fragments-pop-fast">Pop fast</a>
</nav>
<main id="route-main" data-route="%s">
  <div style="height: 900px">Top space</div>
  <h2 id="á">Unicode id</h2>
  <div style="height: 900px">Unicode space</div>
  <h2 id="a&#x0301;">Decomposed Unicode id</h2>
  <div style="height: 900px">Decomposed Unicode space</div>
  <input name="tên" value="not-an-anchor">
  <div style="height: 80px">Non-anchor name separation</div>
  <a name="tên">Unicode name</a>
  <div style="height: 900px">Named space</div>
  <h2 id="%%ZZ">Malformed percent id</h2>
  <div style="height: 900px">Malformed space</div>
  <h2 id="%%C3%%28">Invalid UTF-8 percent id</h2>
  <div style="height: 900px">Invalid UTF-8 space</div>
  <h2 id="x&quot;],body[data-poison=&quot;">Selector-looking id</h2>
  <div style="height: 2400px">Scroll space</div>
</main>`, route)
}

func driveFragmentPageWithLiteral(route string) string {
	return strings.Replace(driveFragmentPage(route),
		`<h2 id="á">Unicode id</h2>`,
		`<h2 id="á">Unicode id</h2><h2 id="%C3%A1">Literal encoded id</h2>`, 1)
}

func driveFragmentPageWithReplacement(route string) string {
	return strings.Replace(driveFragmentPage(route),
		`<h2 id="%C3%28">Invalid UTF-8 percent id</h2>`,
		`<h2 id="�(">Invalid UTF-8 replacement id</h2>`, 1)
}

func driveFragmentRouteDocument(title, route, hydrateIntegrity string) string {
	return fmt.Sprintf(`<!doctype html>
<html><head><meta charset="utf-8"><title>%s</title><script defer src="/drive-fragment-contract.js" integrity="%s" crossorigin="anonymous"></script><script defer src="/hydrate.kit.js?v=fragment-contract" integrity="%s" crossorigin="anonymous"></script></head>
<body>%s</body></html>`, title, driveFragmentContractIntegrity, hydrateIntegrity, driveFragmentPage(route))
}

func driveFragmentRouteDocumentWithLiteral(title, route, hydrateIntegrity string) string {
	return fmt.Sprintf(`<!doctype html>
<html><head><meta charset="utf-8"><title>%s</title><script defer src="/drive-fragment-contract.js" integrity="%s" crossorigin="anonymous"></script><script defer src="/hydrate.kit.js?v=fragment-contract" integrity="%s" crossorigin="anonymous"></script></head>
<body>%s</body></html>`, title, driveFragmentContractIntegrity, hydrateIntegrity, driveFragmentPageWithLiteral(route))
}

func driveFragmentRouteDocumentWithReplacement(title, route, hydrateIntegrity string) string {
	return fmt.Sprintf(`<!doctype html>
<html><head><meta charset="utf-8"><title>%s</title><script defer src="/drive-fragment-contract.js" integrity="%s" crossorigin="anonymous"></script><script defer src="/hydrate.kit.js?v=fragment-contract" integrity="%s" crossorigin="anonymous"></script></head>
<body>%s</body></html>`, title, driveFragmentContractIntegrity, hydrateIntegrity, driveFragmentPageWithReplacement(route))
}

var driveFragmentContractIntegrity = driveScriptIntegrity([]byte(driveFragmentContractSource))

func driveFragmentInitialDocument(hydrateIntegrity string) string {
	return fmt.Sprintf(`<!doctype html>
<html><head><meta charset="utf-8"><title>Fragments</title>
<script defer src="/drive-fragment-contract.js" integrity="%s" crossorigin="anonymous"></script>
<script defer src="/hydrate.kit.js?v=fragment-contract" integrity="%s" crossorigin="anonymous"></script>
</head><body>%s</body></html>`, driveFragmentContractIntegrity, hydrateIntegrity, driveFragmentPage("/drive-fragments"))
}

const driveFragmentContractSource = `
  globalThis.__driveReplaceWrites = [];
  (function () {
    var replace = history.replaceState.bind(history);
    history.replaceState = function (state, title, url) {
      var value = state && state.__kitjs_drive__ && state.__kitjs_drive__.scroll;
      globalThis.__driveReplaceWrites.push({ href: String(url || location.href), x: value && value.x, y: value && value.y });
      return replace(state, title, url);
    };
  })();
` + browserHarness + `
` + driveFragmentAssertions

const driveFragmentAssertions = `__runStandaloneKitTest(async function () {
  var assert = __kitTestAssert;
  var waitFor = __kitTestWaitFor;
  var delay = function (milliseconds) { return new Promise(function (resolve) { setTimeout(resolve, milliseconds); }); };
  var realFetch = globalThis.fetch.bind(globalThis);
  var driveFetches = [];
  var navigationEvents = [];
  globalThis.addEventListener("kit:navigation", function (event) {
    navigationEvents.push(event.detail);
  });
  globalThis.fetch = function (source, options) {
    driveFetches.push(String(source));
    return realFetch(source, options);
  };

  function nearTop(element) {
    return Math.abs(element.getBoundingClientRect().top) < 2;
  }
  async function clickAtTop(link, target, message) {
    scrollTo(0, 0);
    var click = new MouseEvent("click", { bubbles: true, cancelable: true, button: 0 });
    link.dispatchEvent(click);
    await delay(100);
    assert(nearTop(target), message + ": top=" + target.getBoundingClientRect().top +
      " hash=" + location.hash + " y=" + scrollY + " fetches=" + driveFetches.length +
      " prevented=" + click.defaultPrevented + " writes=" + JSON.stringify(globalThis.__driveReplaceWrites) +
      " state=" + JSON.stringify(history.state));
    return click;
  }

  var unicode = document.getElementById("á");
  var nativeUnicodeClick = await clickAtTop(document.getElementById("raw-unicode-link"), unicode,
    "raw Unicode fragment #á did not reach its exact id");
  assert(!nativeUnicodeClick.defaultPrevented,
    "Drive intercepted a same-document fragment instead of preserving native navigation");
  assert(decodeURIComponent(location.hash.slice(1)) === "á", "raw Unicode fragment was corrupted");
  assert(unicode.matches(":target"), "native :target did not follow the Unicode fragment URL");

  await clickAtTop(document.getElementById("encoded-unicode-link"), unicode,
    "encoded Unicode fragment did not reach its decoded id");
  assert(decodeURIComponent(location.hash.slice(1)) === "á", "encoded Unicode fragment was corrupted");

  var literal = document.createElement("h2");
  literal.id = "%C3%A1";
  literal.textContent = "Literal percent id";
  var routeMain = document.getElementById("route-main");
  routeMain.insertBefore(literal, routeMain.lastElementChild);
  await clickAtTop(document.getElementById("encoded-unicode-link"), literal,
    "literal raw fragment id did not precede its percent-decoded form");
  assert(literal.matches(":target"), "raw fragment precedence did not update native :target");
  literal.remove();

  var decomposed = document.getElementById("a\u0301");
  await clickAtTop(document.getElementById("nfd-unicode-link"), decomposed,
    "decomposed Unicode fragment was normalized onto the NFC id");
  assert(decodeURIComponent(location.hash.slice(1)) === "a\u0301",
    "decomposed Unicode fragment was normalized or corrupted");
  assert(decomposed.matches(":target") && !unicode.matches(":target"),
    "NFC and NFD fragment identifiers were not kept distinct");

  var named = document.getElementsByName("tên")[0];
  named = Array.prototype.filter.call(document.getElementsByName("tên"), function (element) {
    return element.localName === "a";
  })[0];
  await clickAtTop(document.getElementById("named-unicode-link"), named,
    "Unicode named anchor did not retain legacy anchor semantics");

  var malformed = document.getElementById("%ZZ");
  await clickAtTop(document.getElementById("malformed-link"), malformed,
    "malformed percent fragment threw or failed closed incorrectly");

  var invalidUTF8 = document.getElementById("%C3%28");
  await clickAtTop(document.getElementById("invalid-utf8-link"), invalidUTF8,
    "invalid UTF-8 percent fragment did not retain exact raw-id semantics");
  var invalidUTF8DecodeRejected = false;
  try { decodeURIComponent(location.hash.slice(1)); }
  catch (_) { invalidUTF8DecodeRejected = true; }
  assert(invalidUTF8DecodeRejected,
    "invalid UTF-8 fixture unexpectedly became a valid decoded fragment");

  var selector = document.getElementById('x"],body[data-poison="');
  await clickAtTop(document.getElementById("selector-link"), selector,
    "selector-looking fragment was not treated as an exact id");
  assert(!document.body.hasAttribute("data-poison"), "fragment text entered a CSS selector");
  assert(driveFetches.length === 0, "same-document fragment navigation issued a Drive fetch");

  scrollTo(0, 800);
  document.getElementById("top-link").click();
  await waitFor(function () { return scrollY === 0; }, "#top did not restore the top-of-document semantic");
  scrollTo(0, 800);
  document.getElementById("empty-link").click();
  await waitFor(function () { return scrollY === 0; }, "empty fragment did not restore the document top");

  var delayedFetchStart = driveFetches.length;
  document.getElementById("pop-slow-link").click();
  await waitFor(function () { return driveFetches.length === delayedFetchStart + 1; },
    "delayed Drive visit did not start");
  var cancelClick = new MouseEvent("click", { bubbles: true, cancelable: true, button: 0 });
  document.getElementById("raw-unicode-link").dispatchEvent(cancelClick);
  assert(cancelClick.defaultPrevented,
    "same-document fragment click did not use the guarded native-assignment path during an active visit");
  await delay(500);
  assert(location.pathname === "/drive-fragments" &&
    decodeURIComponent(location.hash.slice(1)) === "á" &&
    document.getElementById("route-main").getAttribute("data-route") === "/drive-fragments" &&
    document.getElementById("á").matches(":target"),
    "delayed Drive response committed over a newer native fragment navigation");
  history.replaceState(history.state, "", "/drive-fragments");

  await delay(300);
  globalThis.__driveReplaceWrites.length = 0;
  var stormStart = performance.now();
  var stormStep = 0;
  while (performance.now() - stormStart < 900) {
    stormStep++;
    scrollTo(0, 300 + stormStep);
    globalThis.dispatchEvent(new Event("scroll"));
    await delay(5);
  }
  var finalStormY = scrollY;
  await delay(300);
  assert(globalThis.__driveReplaceWrites.length <= 4,
    "scroll storm exceeded four history writes per second: " + globalThis.__driveReplaceWrites.length);
  assert(history.state.__kitjs_drive__.scroll.y === finalStormY,
    "throttled scroll history did not retain the final position");

  globalThis.__driveReplaceWrites.length = 0;
  for (var unchanged = 0; unchanged < 8; unchanged++) {
    globalThis.dispatchEvent(new Event("scroll"));
  }
  await delay(300);
  assert(globalThis.__driveReplaceWrites.length === 0,
    "unchanged scroll coordinates produced redundant history writes");

  history.replaceState(history.state, "", "/drive-fragments");
  scrollTo(0, 1100);
  globalThis.dispatchEvent(new Event("scroll"));
  var backY = scrollY;
  document.getElementById("raw-unicode-link").click();
  await delay(300);
  var forwardY = scrollY;
  var popFetchStart = driveFetches.length;
  history.back();
  await delay(300);
  assert(!location.hash && Math.abs(scrollY - backY) < 2,
    "Back did not restore the exact saved scroll position: hash=" + location.hash + " y=" + scrollY +
      " expected=" + backY + " state=" + JSON.stringify(history.state));
  assert(driveFetches.length === popFetchStart,
    "same-document Back traversal issued a Drive fetch");
  history.forward();
  await delay(300);
  assert(decodeURIComponent(location.hash.slice(1)) === "á" && Math.abs(scrollY - forwardY) < 2,
    "Forward did not restore the exact saved fragment position: hash=" + location.hash + " y=" + scrollY +
      " expected=" + forwardY + " state=" + JSON.stringify(history.state));
  assert(driveFetches.length === popFetchStart,
    "same-document Forward traversal issued a Drive fetch");
  history.replaceState(history.state, "", "/drive-fragments");

  globalThis.__driveReplaceWrites.length = 0;
  scrollTo(0, 1000);
  globalThis.dispatchEvent(new Event("scroll"));
  globalThis.dispatchEvent(new Event("pagehide"));
  var pagehideWrites = globalThis.__driveReplaceWrites.length;
  assert(pagehideWrites === 1 && history.state.__kitjs_drive__.scroll.y === scrollY,
    "pagehide did not synchronously flush the exact scroll position");
  await delay(300);
  assert(globalThis.__driveReplaceWrites.length === pagehideWrites,
    "pagehide left a pending scroll timer behind");

  globalThis.__driveReplaceWrites.length = 0;
  scrollTo(0, 1200);
  globalThis.dispatchEvent(new Event("scroll"));
  var leavingY = scrollY;
  document.getElementById("next-link").click();
  await waitFor(function () { return location.pathname === "/drive-fragments-next"; },
    "Drive visit did not commit after synchronous scroll flush");
  await delay(100);
  var routeUnicode = document.getElementById("á");
  assert(decodeURIComponent(location.hash.slice(1)) === "á" && routeUnicode &&
    Math.abs(routeUnicode.getBoundingClientRect().top) < 2,
    "cross-route Unicode fragment was not preserved: hash=" + location.hash + " top=" +
      (routeUnicode && routeUnicode.getBoundingClientRect().top) + " y=" + scrollY);
  assert(document.getElementById("route-main").getAttribute("data-route") === "/drive-fragments-next",
    "redirect response did not commit the final route while retaining the requested fragment");
  assert(globalThis.__driveReplaceWrites.some(function (write) {
    return new URL(write.href, location.href).pathname === "/drive-fragments" && write.y === leavingY;
  }), "Drive visit did not flush the exact leaving position before navigation");
  assert(driveFetches.filter(function (source) {
    return new URL(source, location.href).pathname === "/drive-fragments-redirect";
  }).length === 1, "loading the artifact twice installed duplicate Drive click listeners");
  var writesAfterVisit = globalThis.__driveReplaceWrites.length;
  await delay(350);
  assert(globalThis.__driveReplaceWrites.length === writesAfterVisit,
    "Drive visit left a stale scroll-save timer behind");

  async function visitMatrix(linkID, path, target, message) {
    var fetchStart = driveFetches.length;
    var link = document.getElementById(linkID);
    assert(link, message + " link missing from current document");
    var expectedURL = new URL(link.href, location.href).href;
    link.click();
    await waitFor(function () {
      return location.pathname === path &&
        document.getElementById("route-main").getAttribute("data-route") === path;
    }, message + " did not commit; events=" + JSON.stringify(navigationEvents));
    await delay(100);
    assert(driveFetches.length === fetchStart + 1, message + " issued an unexpected fetch count; events=" +
      JSON.stringify(navigationEvents));
    target = target();
    assert(target && nearTop(target), message + ": target=" + target +
      " hash=" + location.hash + " y=" + scrollY);
    history.replaceState(history.state, "", expectedURL);
    return target;
  }

  var crossLiteral = await visitMatrix("matrix-encoded-link", "/drive-fragments-matrix-encoded",
    function () { return document.getElementById("%C3%A1"); },
    "cross-route raw encoded id precedence");
  assert(document.activeElement === crossLiteral,
    "cross-route raw encoded id was not the focus target");

  var crossNamed = await visitMatrix("matrix-named-link", "/drive-fragments-matrix-named",
    function () {
      return Array.prototype.filter.call(document.getElementsByName("tên"), function (element) {
        return element.localName === "a";
      })[0];
    }, "cross-route Unicode named anchor");
  var crossNamedInput = document.getElementsByName("tên")[0];
  assert(crossNamedInput.localName === "input",
    "cross-route named fixture did not place the non-anchor first");
  assert(nearTop(crossNamed),
    "cross-route named fragment did not scroll to the anchor");
  assert(crossNamedInput.getBoundingClientRect().top < -20,
    "cross-route named fragment scrolled to the non-anchor name; inputTop=" +
      crossNamedInput.getBoundingClientRect().top + " anchorTop=" + crossNamed.getBoundingClientRect().top);
  assert(document.activeElement === crossNamed,
    "cross-route named anchor was scrolled but did not become the focus target");

  await visitMatrix("matrix-malformed-link", "/drive-fragments-matrix-malformed",
    function () { return document.getElementById("%ZZ"); },
    "cross-route malformed-percent raw id");
  await visitMatrix("matrix-invalid-utf8-raw-link", "/drive-fragments-matrix-invalid-utf8-raw",
    function () { return document.getElementById("%C3%28"); },
    "cross-route invalid UTF-8 raw id");
  await visitMatrix("matrix-invalid-utf8-replacement-link",
    "/drive-fragments-matrix-invalid-utf8-replacement",
    function () { return document.getElementById("�("); },
    "cross-route invalid UTF-8 replacement id");

  var crossNFD = await visitMatrix("matrix-nfd-link", "/drive-fragments-matrix-nfd",
    function () { return document.getElementById("a\u0301"); },
    "cross-route decomposed Unicode id");
  assert(crossNFD !== document.getElementById("á") && document.activeElement === crossNFD,
    "cross-route fragment normalized distinct NFC and NFD identifiers");

  await visitMatrix("matrix-selector-link", "/drive-fragments-matrix-selector",
    function () { return document.getElementById('x"],body[data-poison="'); },
    "cross-route selector-looking exact id");
  assert(!document.body.hasAttribute("data-poison"),
    "cross-route fragment text entered a CSS selector");

  var crossEmpty = await visitMatrix("matrix-empty-link", "/drive-fragments-matrix-empty",
    function () { return document.documentElement; },
    "cross-route empty fragment");
  assert(location.href.endsWith("#") && document.activeElement === crossEmpty && scrollY === 0,
    "cross-route empty fragment did not target the document root");

  await visitMatrix("pop-slow-link", "/drive-fragments-pop-slow",
    function () { return document.documentElement; }, "slow history fixture");
  scrollTo(0, 1400);
  globalThis.dispatchEvent(new Event("scroll"));
  await delay(300);
  var slowY = scrollY;
  assert(history.state.__kitjs_drive__.scroll.y === slowY,
    "slow history entry did not save its own position");

  await visitMatrix("pop-fast-link", "/drive-fragments-pop-fast",
    function () { return document.documentElement; }, "fast history fixture");
  scrollTo(0, 700);
  globalThis.dispatchEvent(new Event("scroll"));
  await delay(300);
  var fastY = scrollY;
  assert(history.state.__kitjs_drive__.scroll.y === fastY,
    "fast history entry did not save its own position");

  var rapidFetchStart = driveFetches.length;
  history.back();
  await waitFor(function () { return location.pathname === "/drive-fragments-pop-slow"; },
    "rapid Back did not select the slow destination entry");
  history.forward();
  await waitFor(function () { return location.pathname === "/drive-fragments-pop-fast"; },
    "rapid Forward did not restore the fast destination entry");
  await delay(500);
  assert(document.getElementById("route-main").getAttribute("data-route") === "/drive-fragments-pop-fast" &&
    history.state.__kitjs_drive__.scroll.y === fastY && Math.abs(scrollY - fastY) < 2,
    "rapid cross-document Back/Forward clobbered the destination state");
  assert(driveFetches.length >= rapidFetchStart + 1 && driveFetches.length <= rapidFetchStart + 2,
    "rapid cross-document Back/Forward issued an unexpected Drive fetch count: " +
      (driveFetches.length - rapidFetchStart));

  var pagehideFetchStart = driveFetches.length;
  history.back();
  await waitFor(function () {
    return location.pathname === "/drive-fragments-pop-slow" && driveFetches.length === pagehideFetchStart + 1;
  }, "pagehide popstate fixture did not start its destination visit");
  assert(history.state.__kitjs_drive__.scroll.y === slowY,
    "cross-document popstate clobbered destination state before pagehide");
  globalThis.dispatchEvent(new Event("pagehide"));
  await delay(500);
  assert(history.state.__kitjs_drive__.scroll.y === slowY,
    "pagehide during cross-document popstate clobbered destination state");
});`
