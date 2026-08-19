package javascript

import (
	"html"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/kitwork/engine/jit/theme"
)

type driveThemeRouteCounter struct {
	drive atomic.Int64
	full  atomic.Int64
}

// TestBrowserStagedDriveAcceptsOnlyCanonicalThemePrepaint proves that Kitwork's
// synchronous engine-owned theme prepaint is not mistaken for an authored
// inline script. The proof remains black-box: it observes only the public DOM,
// native-navigation cookies, and HTTP request counters.
func TestBrowserStagedDriveAcceptsOnlyCanonicalThemePrepaint(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping staged Drive theme-prepaint browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	assembly, err := BuildStaged(StagedBuildOptions{Profile: ProfileHydrate})
	if err != nil {
		t.Fatal(err)
	}
	contractSource := []byte(browserHarness + "\n" + driveThemePrepaintAssertions)
	contractIntegrity := driveScriptIntegrity(contractSource)

	assets := make(map[string][]byte)
	for _, artifact := range assembly.Artifacts() {
		assets["/jit/"+artifact.Name()] = artifact.Bytes()
	}

	canonical := func(route, title string) string {
		return driveThemePrepaintDocument(assembly, route, title, contractIntegrity)
	}
	startDocument := canonical("start", "Theme prepaint start")
	sameDocument := canonical("same", "Theme prepaint same")
	changedDocument := mutateFirstInlineScript(sameDocument, func(script string) string {
		return strings.Replace(script, "</script>", ";void 0;</script>", 1)
	})
	missingDocument := mutateFirstInlineScript(sameDocument, func(string) string { return "" })
	extraDocument := mutateFirstInlineScript(sameDocument, func(script string) string { return script + script })
	nonScriptDocument := mutateFirstInlineScript(sameDocument, func(script string) string {
		return script + `<meta data-kitwork-jit="theme" content="not-a-script">`
	})
	forgedDocument := mutateFirstInlineScript(sameDocument, func(string) string {
		return `<script data-kitwork-jit="theme">document.documentElement.setAttribute("data-forged-prepaint", "executed");</script>`
	})
	ordinaryInlineDocument := strings.Replace(sameDocument, "</body>",
		`<script>document.documentElement.setAttribute("data-ordinary-inline", "executed");</script></body>`, 1)

	routes := map[string]struct {
		document string
		cookie   string
		counter  *driveThemeRouteCounter
	}{
		"/prepaint-drive/changed": {
			document: changedDocument,
			cookie:   "prepaint_drive_changed_full",
			counter:  &driveThemeRouteCounter{},
		},
		"/prepaint-drive/missing": {
			document: missingDocument,
			cookie:   "prepaint_drive_missing_full",
			counter:  &driveThemeRouteCounter{},
		},
		"/prepaint-drive/extra": {
			document: extraDocument,
			cookie:   "prepaint_drive_extra_full",
			counter:  &driveThemeRouteCounter{},
		},
		"/prepaint-drive/non-script": {
			document: nonScriptDocument,
			cookie:   "prepaint_drive_non_script_full",
			counter:  &driveThemeRouteCounter{},
		},
		"/prepaint-drive/forged": {
			document: forgedDocument,
			cookie:   "prepaint_drive_forged_full",
			counter:  &driveThemeRouteCounter{},
		},
		"/prepaint-drive/ordinary-inline": {
			document: ordinaryInlineDocument,
			cookie:   "prepaint_drive_ordinary_inline_full",
			counter:  &driveThemeRouteCounter{},
		},
		"/prepaint-drive/replaced-live": {
			document: sameDocument,
			cookie:   "prepaint_drive_replaced_live_full",
			counter:  &driveThemeRouteCounter{},
		},
	}
	var startFull atomic.Int64
	var startDrive atomic.Int64
	var sameDrive atomic.Int64
	var sameFull atomic.Int64

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/prepaint-drive-contract.js" {
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(contractSource)
			return
		}
		if source, ok := assets[request.URL.Path]; ok {
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			response.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			response.Header().Set("X-Content-Type-Options", "nosniff")
			_, _ = response.Write(source)
			return
		}

		drive := request.Header.Get("X-KitJS-Drive") == "1"
		switch request.URL.Path {
		case "/prepaint-drive/start":
			if drive {
				startDrive.Add(1)
				writeStagedDriveHTML(response, startDocument)
				return
			}
			startFull.Add(1)
			writeStagedDriveHTML(response, startDocument)
		case "/prepaint-drive/same":
			if drive {
				sameDrive.Add(1)
				writeStagedDriveHTML(response, sameDocument)
				return
			}
			sameFull.Add(1)
			response.Header().Set("Set-Cookie", "prepaint_drive_unexpected_same_full=1; Path=/; SameSite=Lax")
			response.WriteHeader(http.StatusNoContent)
		default:
			fixture, ok := routes[request.URL.Path]
			if !ok {
				http.NotFound(response, request)
				return
			}
			if drive {
				fixture.counter.drive.Add(1)
				writeStagedDriveHTML(response, fixture.document)
				return
			}
			fixture.counter.full.Add(1)
			response.Header().Set("Set-Cookie", fixture.cookie+"=1; Path=/; SameSite=Lax")
			response.WriteHeader(http.StatusNoContent)
		}
	}))
	defer server.Close()

	runVanillaBrowser(t, browser, server.URL+"/prepaint-drive/start")

	if got := startFull.Load(); got != 1 {
		t.Errorf("initial theme-prepaint full requests = %d, want 1", got)
	}
	if got := startDrive.Load(); got != 1 {
		t.Errorf("return theme-prepaint Drive requests = %d, want 1", got)
	}
	if got := sameDrive.Load(); got != 2 {
		t.Errorf("identical theme-prepaint Drive requests = %d, want 2", got)
	}
	if got := sameFull.Load(); got != 0 {
		t.Errorf("identical theme-prepaint full requests = %d, want 0", got)
	}
	for path, fixture := range routes {
		if got := fixture.counter.drive.Load(); got != 1 {
			t.Errorf("%s Drive requests = %d, want 1", path, got)
		}
		if got := fixture.counter.full.Load(); got != 1 {
			t.Errorf("%s full requests = %d, want 1", path, got)
		}
	}
}

// TestBrowserStagedDriveRejectsInitialAuthoredThemeMarker proves that an
// author cannot opt an arbitrary inline script into the engine-owned prepaint
// exception merely by copying its reserved marker onto the initial document.
func TestBrowserStagedDriveRejectsInitialAuthoredThemeMarker(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping staged Drive initial-forged-theme browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	assembly, err := BuildStaged(StagedBuildOptions{Profile: ProfileHydrate})
	if err != nil {
		t.Fatal(err)
	}
	contractSource := []byte(browserHarness + "\n" + driveInitialForgedThemeAssertions)
	contractIntegrity := driveScriptIntegrity(contractSource)
	startDocument := driveInitialForgedThemeDocument(assembly, "start", contractIntegrity)
	targetDocument := driveInitialForgedThemeDocument(assembly, "target", contractIntegrity)

	assets := make(map[string][]byte)
	for _, artifact := range assembly.Artifacts() {
		assets["/jit/"+artifact.Name()] = artifact.Bytes()
	}
	var startFull atomic.Int64
	var targetDrive atomic.Int64
	var targetFull atomic.Int64

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/initial-forged-theme-contract.js" {
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(contractSource)
			return
		}
		if source, ok := assets[request.URL.Path]; ok {
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			response.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			response.Header().Set("X-Content-Type-Options", "nosniff")
			_, _ = response.Write(source)
			return
		}

		drive := request.Header.Get("X-KitJS-Drive") == "1"
		switch request.URL.Path {
		case "/initial-forged-theme/start":
			if drive {
				http.Error(response, "unexpected Drive request for initial forged page", http.StatusInternalServerError)
				return
			}
			startFull.Add(1)
			writeStagedDriveHTML(response, startDocument)
		case "/initial-forged-theme/target":
			if drive {
				targetDrive.Add(1)
				writeStagedDriveHTML(response, targetDocument)
				return
			}
			targetFull.Add(1)
			response.Header().Set("Set-Cookie", "initial_forged_theme_full=1; Path=/; SameSite=Lax")
			response.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	runVanillaBrowser(t, browser, server.URL+"/initial-forged-theme/start")

	if got := startFull.Load(); got != 1 {
		t.Errorf("initial forged-theme full requests = %d, want 1", got)
	}
	if got := targetDrive.Load(); got > 1 {
		t.Errorf("initial forged-theme Drive probes = %d, want at most 1", got)
	}
	if got := targetFull.Load(); got != 1 {
		t.Errorf("initial forged-theme native requests = %d, want 1", got)
	}
}

func driveThemePrepaintDocument(assembly StagedAssembly, route, title, contractIntegrity string) string {
	var output strings.Builder
	output.WriteString(`<!doctype html><html lang="en"><head><meta charset="utf-8"><title>`)
	output.WriteString(html.EscapeString(title))
	output.WriteString(`</title><meta name="description" content="theme-prepaint-`)
	output.WriteString(html.EscapeString(route))
	output.WriteString(`"><link rel="canonical" href="/prepaint-drive/`)
	output.WriteString(html.EscapeString(route))
	output.WriteString(`"><style data-kit-head="prepaint-route">:root{--prepaint-drive-route:"`)
	output.WriteString(html.EscapeString(route))
	output.WriteString(`"}</style><script defer src="/prepaint-drive-contract.js" integrity="`)
	output.WriteString(html.EscapeString(contractIntegrity))
	output.WriteString(`" crossorigin="anonymous"></script>`)
	for _, artifact := range assembly.Artifacts() {
		output.WriteString(`<script data-kitwork-jit="`)
		output.WriteString(html.EscapeString(string(artifact.Role())))
		output.WriteString(`" data-kitwork-hash="`)
		output.WriteString(artifact.SHA256())
		output.WriteString(`" src="/jit/`)
		output.WriteString(html.EscapeString(artifact.Name()))
		output.WriteString(`" integrity="`)
		output.WriteString(html.EscapeString(artifact.Integrity()))
		output.WriteString(`" crossorigin="anonymous" defer></script>`)
	}
	output.WriteString(`</head><body><nav aria-label="Theme prepaint routes">
<a id="prepaint-start" href="/prepaint-drive/start">Start</a>
<a id="prepaint-same" href="/prepaint-drive/same">Same</a>
<a id="prepaint-changed" href="/prepaint-drive/changed">Changed</a>
<a id="prepaint-missing" href="/prepaint-drive/missing">Missing</a>
<a id="prepaint-extra" href="/prepaint-drive/extra">Extra</a>
<a id="prepaint-non-script" href="/prepaint-drive/non-script">Non-script</a>
<a id="prepaint-forged" href="/prepaint-drive/forged">Forged</a>
<a id="prepaint-ordinary-inline" href="/prepaint-drive/ordinary-inline">Ordinary inline</a>
<a id="prepaint-replaced-live" href="/prepaint-drive/replaced-live">Replaced live</a>
</nav><main id="prepaint-route">`)
	output.WriteString(html.EscapeString(route))
	output.WriteString(`</main></body></html>`)
	return theme.Force(output.String())
}

func driveInitialForgedThemeDocument(assembly StagedAssembly, route, contractIntegrity string) string {
	var output strings.Builder
	output.WriteString(`<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Initial forged theme `)
	output.WriteString(html.EscapeString(route))
	output.WriteString(`</title><script data-kitwork-jit="theme">document.documentElement.setAttribute("data-authored-theme-marker-runs",String(Number(document.documentElement.getAttribute("data-authored-theme-marker-runs")||0)+1));</script><script defer src="/initial-forged-theme-contract.js" integrity="`)
	output.WriteString(html.EscapeString(contractIntegrity))
	output.WriteString(`" crossorigin="anonymous"></script>`)
	for _, artifact := range assembly.Artifacts() {
		output.WriteString(`<script data-kitwork-jit="`)
		output.WriteString(html.EscapeString(string(artifact.Role())))
		output.WriteString(`" data-kitwork-hash="`)
		output.WriteString(artifact.SHA256())
		output.WriteString(`" src="/jit/`)
		output.WriteString(html.EscapeString(artifact.Name()))
		output.WriteString(`" integrity="`)
		output.WriteString(html.EscapeString(artifact.Integrity()))
		output.WriteString(`" crossorigin="anonymous" defer></script>`)
	}
	output.WriteString(`</head><body><a id="initial-forged-theme-target" href="/initial-forged-theme/target">Target</a><main id="initial-forged-theme-route">`)
	output.WriteString(html.EscapeString(route))
	output.WriteString(`</main></body></html>`)
	return output.String()
}

func mutateFirstInlineScript(source string, mutate func(string) string) string {
	start := strings.Index(source, "<script")
	if start < 0 {
		return source
	}
	relativeEnd := strings.Index(source[start:], "</script>")
	if relativeEnd < 0 {
		return source
	}
	end := start + relativeEnd + len("</script>")
	return source[:start] + mutate(source[start:end]) + source[end:]
}

const driveThemePrepaintAssertions = `__runStandaloneKitTest(async function () {
  var assert = __kitTestAssert;
  var waitFor = __kitTestWaitFor;
  await waitFor(function () {
    return globalThis.kit && document.getElementById("prepaint-route").textContent === "start";
  }, "initial staged theme-prepaint page did not boot");

  var root = document.documentElement;
  var body = document.body;
  var themeNode = document.head.querySelector('script[data-kitwork-jit="theme"]');
  assert(themeNode && themeNode.parentNode === document.head,
    "engine theme prepaint was not a direct-head canonical script");

  function headRoute(route, previous, label) {
    var current = {
      meta: document.head.querySelector('meta[name="description"]'),
      link: document.head.querySelector('link[rel="canonical"]'),
      style: document.head.querySelector('style[data-kit-head="prepaint-route"]')
    };
    assert(document.head.querySelectorAll('meta[name="description"]').length === 1 &&
      document.head.querySelectorAll('link[rel="canonical"]').length === 1 &&
      document.head.querySelectorAll('style[data-kit-head="prepaint-route"]').length === 1,
      label + " left duplicate route-varying safe head nodes");
    assert(current.meta && current.meta.content === "theme-prepaint-" + route,
      label + " did not reconcile route metadata");
    assert(current.link && new URL(current.link.href, location.href).pathname === "/prepaint-drive/" + route,
      label + " did not reconcile the canonical link");
    assert(current.style && current.style.textContent.indexOf('--prepaint-drive-route:"' + route + '"') >= 0,
      label + " did not reconcile the safe route style");
    if (previous) {
      assert(current.meta !== previous.meta && current.link !== previous.link && current.style !== previous.style,
        label + " reused route-varying safe head nodes");
      assert(!previous.meta.isConnected && !previous.link.isConnected && !previous.style.isConnected,
        label + " left stale route-varying safe head nodes connected");
    }
    return current;
  }

  var headNodes = headRoute("start", null, "initial document");

  document.getElementById("prepaint-same").click();
  await waitFor(function () {
    return location.pathname === "/prepaint-drive/same" &&
      document.getElementById("prepaint-route").textContent === "same";
  }, "identical engine theme prepaint did not allow Drive Morph");
  assert(document.documentElement === root && document.body === body,
    "identical engine theme prepaint replaced a document root");
  assert(document.head.querySelector('script[data-kitwork-jit="theme"]') === themeNode,
    "identical engine theme prepaint replaced or reran its live node");
  headNodes = headRoute("same", headNodes, "first Morph");
  assert(document.cookie.indexOf("prepaint_drive_unexpected_same_full=1") < 0,
    "identical engine theme prepaint used full navigation");

  document.getElementById("prepaint-start").click();
  await waitFor(function () {
    return location.pathname === "/prepaint-drive/start" &&
      document.getElementById("prepaint-route").textContent === "start";
  }, "return navigation rejected identical engine theme prepaint");
  assert(document.documentElement === root && document.body === body &&
    document.head.querySelector('script[data-kitwork-jit="theme"]') === themeNode,
    "return navigation replaced a document root or the live theme prepaint");
  headNodes = headRoute("start", headNodes, "return Morph");

  document.getElementById("prepaint-same").click();
  await waitFor(function () {
    return location.pathname === "/prepaint-drive/same" &&
      document.getElementById("prepaint-route").textContent === "same";
  }, "theme prepaint became incompatible after head reconciliation");
  assert(document.documentElement === root && document.body === body &&
    document.head.querySelector('script[data-kitwork-jit="theme"]') === themeNode,
    "repeated theme-prepaint Morph replaced a document root or prepaint node");
  headNodes = headRoute("same", headNodes, "repeated Morph");

  function snapshot() {
    return {
      root: document.documentElement,
      body: document.body,
      head: document.head.innerHTML,
      bodyHTML: document.body.innerHTML,
      title: document.title,
      path: location.pathname,
      route: document.getElementById("prepaint-route"),
      historyLength: history.length,
      historyState: JSON.stringify(history.state)
    };
  }

  function unchanged(before, label) {
    assert(document.documentElement === before.root && document.body === before.body,
      label + " replaced a document root before fallback");
    assert(document.head.innerHTML === before.head && document.body.innerHTML === before.bodyHTML,
      label + " mutated head or body before fallback");
    assert(document.title === before.title && location.pathname === before.path,
      label + " committed title or URL before fallback");
    assert(document.getElementById("prepaint-route") === before.route,
      label + " replaced the live route before fallback");
    assert(history.length === before.historyLength && JSON.stringify(history.state) === before.historyState,
      label + " mutated history before fallback");
  }

  async function expectFallback(link, cookie, label) {
    var before = snapshot();
    document.getElementById(link).click();
    await waitFor(function () { return document.cookie.indexOf(cookie + "=1") >= 0; },
      label + " did not hard-navigate");
    unchanged(before, label);
  }

  await expectFallback("prepaint-changed", "prepaint_drive_changed_full", "changed theme prepaint");
  await expectFallback("prepaint-missing", "prepaint_drive_missing_full", "missing theme prepaint");
  await expectFallback("prepaint-extra", "prepaint_drive_extra_full", "extra theme prepaint");
  await expectFallback("prepaint-non-script", "prepaint_drive_non_script_full", "non-script theme marker");
  await expectFallback("prepaint-forged", "prepaint_drive_forged_full", "forged theme prepaint");
  assert(!document.documentElement.hasAttribute("data-forged-prepaint"),
    "fetched forged theme prepaint executed before fallback");
  await expectFallback("prepaint-ordinary-inline", "prepaint_drive_ordinary_inline_full",
    "ordinary unmarked inline script");
  assert(!document.documentElement.hasAttribute("data-ordinary-inline"),
    "fetched ordinary inline script executed before fallback");

  var replacement = themeNode.cloneNode(true);
  themeNode.parentNode.replaceChild(replacement, themeNode);
  await expectFallback("prepaint-replaced-live", "prepaint_drive_replaced_live_full",
    "replaced live theme prepaint node");
});`

const driveInitialForgedThemeAssertions = `__runStandaloneKitTest(async function () {
  var assert = __kitTestAssert;
  var waitFor = __kitTestWaitFor;
  await waitFor(function () {
    return globalThis.kit && document.getElementById("initial-forged-theme-route").textContent === "start";
  }, "initial authored theme-marker page did not boot");
  assert(document.documentElement.getAttribute("data-authored-theme-marker-runs") === "1",
    "initial authored marker fixture did not execute exactly once through the document loader");

  var before = {
    root: document.documentElement,
    body: document.body,
    head: document.head.innerHTML,
    bodyHTML: document.body.innerHTML,
    title: document.title,
    path: location.pathname,
    route: document.getElementById("initial-forged-theme-route"),
    historyLength: history.length,
    historyState: JSON.stringify(history.state)
  };
  document.getElementById("initial-forged-theme-target").click();
  await waitFor(function () {
    return document.cookie.indexOf("initial_forged_theme_full=1") >= 0;
  }, "initial authored theme marker gained the inline-script exception");

  assert(document.documentElement === before.root && document.body === before.body,
    "initial authored theme marker replaced a document root before native fallback");
  assert(document.head.innerHTML === before.head && document.body.innerHTML === before.bodyHTML,
    "initial authored theme marker mutated head or body before native fallback");
  assert(document.title === before.title && location.pathname === before.path &&
    document.getElementById("initial-forged-theme-route") === before.route,
    "initial authored theme marker committed title, URL, or route before native fallback");
  assert(history.length === before.historyLength && JSON.stringify(history.state) === before.historyState,
    "initial authored theme marker mutated history before native fallback");
  assert(document.documentElement.getAttribute("data-authored-theme-marker-runs") === "1",
    "fetched authored theme marker executed before native fallback");
});`
