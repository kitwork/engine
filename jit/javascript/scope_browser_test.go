package javascript

import (
	"html"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestScopeFragmentIsAStaticBoundedDataParser(t *testing.T) {
	source, err := sources.ReadFile("src/scope.js")
	if err != nil {
		t.Fatal(err)
	}
	code := javascriptWithoutComments(string(source))
	tokens := javascriptIdentifiers(string(source))
	for _, forbidden := range []string{"eval", "Function"} {
		if indexOfToken(tokens, forbidden) >= 0 {
			t.Fatalf("scope.js contains dynamic-code token %q", forbidden)
		}
	}
	for _, forbidden := range []string{"core.compile(", "core.parse(", "JSON.parse("} {
		if strings.Contains(code, forbidden) {
			t.Fatalf("scope.js delegates authored state to %q", forbidden)
		}
	}
	for _, required := range []string{
		"var SOURCE_LIMIT = 16384;",
		"var DEPTH_LIMIT = 32;",
		"var NODE_LIMIT = 1024;",
		"Object.create(null)",
		"new WeakMap()",
	} {
		if !strings.Contains(code, required) {
			t.Fatalf("scope.js lost static parser contract %q", required)
		}
	}
}

func TestBrowserScopeBoundaryAndMorphContract(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping data-kit-scope browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	fixture := scopeBrowserFixture(t)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/scope.html" {
			response.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self' 'unsafe-inline'; base-uri 'none'; object-src 'none'")
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(fixture))
			return
		}
		name := strings.TrimPrefix(request.URL.Path, "/")
		if !strings.HasPrefix(name, "src/") {
			http.NotFound(response, request)
			return
		}
		source, err := sources.ReadFile(name)
		if err != nil {
			http.NotFound(response, request)
			return
		}
		response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		_, _ = response.Write(source)
	}))
	defer server.Close()

	runVanillaBrowser(t, browser, server.URL+"/scope.html")
}

func TestBrowserScopeDriveLifecycleContract(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping data-kit-scope Drive contract in short mode")
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
	contractSource := scopeDriveExternalContract(t)
	contractIntegrity := driveScriptIntegrity(contractSource)
	var invalidTemplateRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Cache-Control", "no-store")
		switch request.URL.Path {
		case "/hydrate.kit.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(hydrateJS)
		case "/scope-drive-contract.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(contractSource)
		case "/scope-drive":
			writeScopeDriveHTML(response, scopeDriveRouteDocument(
				"Initial", "scope-drive-body-a", "route: 'initial'", scopeDriveInitialShell,
				hydrateIntegrity, contractIntegrity))
		case "/scope-drive/invalid-template":
			if invalidTemplateRequests.Add(1) > 1 {
				response.WriteHeader(http.StatusNoContent)
				return
			}
			writeScopeDriveHTML(response, scopeDriveRouteDocument(
				"Invalid", "scope-drive-body-a", "route: 'initial'", scopeDriveInvalidTemplateShell,
				hydrateIntegrity, contractIntegrity))
		case "/scope-drive/same":
			writeScopeDriveHTML(response, scopeDriveRouteDocument(
				"Same", "scope-drive-body-a", "route: 'initial'", scopeDriveSameShell,
				hydrateIntegrity, contractIntegrity))
		case "/scope-drive/changed":
			writeScopeDriveHTML(response, scopeDriveRouteDocument(
				"Changed", "scope-drive-body-a", "route: 'changed'", scopeDriveChangedShell,
				hydrateIntegrity, contractIntegrity))
		case "/scope-drive/component":
			writeScopeDriveHTML(response, scopeDriveRouteDocument(
				"Component", "scope-drive-body-b", "route: 'changed'", scopeDriveComponentShell,
				hydrateIntegrity, contractIntegrity))
		case "/scope-drive/removed":
			writeScopeDriveHTML(response, scopeDriveRouteDocument(
				"Removed", "scope-drive-body-b", "route: 'changed'", scopeDriveRemovedShell,
				hydrateIntegrity, contractIntegrity))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	runVanillaBrowser(t, browser, server.URL+"/scope-drive")
	if requests := invalidTemplateRequests.Load(); requests != 2 {
		t.Fatalf("invalid template Drive requests = %d, want fetch plus 204 hard fallback", requests)
	}
}

func writeScopeDriveHTML(response http.ResponseWriter, source string) {
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = response.Write([]byte(source))
}

func scopeDriveRouteDocument(
	title, bodyComponent, bodyScope, shell, hydrateIntegrity, contractIntegrity string,
) string {
	return `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>` + title +
		`</title><script defer src="/hydrate.kit.js?v=scope-contract" integrity="` +
		html.EscapeString(hydrateIntegrity) + `" crossorigin="anonymous"></script>` +
		`<script defer src="/scope-drive-contract.js" integrity="` +
		html.EscapeString(contractIntegrity) + `" crossorigin="anonymous"></script></head>` +
		`<body data-kit-component="` +
		html.EscapeString(bodyComponent) + `" data-kit-scope="` + html.EscapeString(bodyScope) + `">` +
		shell + `</body></html>`
}

func scopeDriveExternalContract(t *testing.T) []byte {
	t.Helper()
	remaining := scopeDriveInitialDocument
	var scripts []string
	for {
		start := strings.Index(remaining, "<script>")
		if start < 0 {
			break
		}
		remaining = remaining[start+len("<script>"):]
		end := strings.Index(remaining, "</script>")
		if end < 0 {
			t.Fatal("scope Drive legacy fixture has an unterminated inline script")
		}
		scripts = append(scripts, remaining[:end])
		remaining = remaining[end+len("</script>"):]
	}
	if len(scripts) != 3 {
		t.Fatalf("scope Drive legacy fixture has %d inline scripts, want 3", len(scripts))
	}
	return []byte(strings.Join(scripts, "\n"))
}

const scopeDriveInitialShell = `<main id="scope-drive-shell" data-route="initial">
  <section id="drive-normal" data-kit-component="scope-drive-normal" data-kit-scope="count: 3">
    <button id="drive-normal-add" type="button" data-kit-click="count = count + 1">Normal</button>
    <output id="drive-normal-count" data-kit-text="count">server</output>
  </section>
  <section id="drive-anonymous" data-kit-scope="count: 10">
    <button id="drive-anonymous-add" type="button" data-kit-click="count = count + 1">Anonymous</button>
    <output id="drive-anonymous-count" data-kit-text="count">server</output>
  </section>
  <div id="drive-retain-parent-a">
    <section id="drive-retained" data-kit-retain="scope-drive-retained" data-kit-component="scope-drive-retained" data-kit-scope="count: 20">
      <button id="drive-retained-add" type="button" data-kit-click="count = count + 1">Retained</button>
      <output id="drive-retained-count" data-kit-text="count">server</output>
    </section>
  </div>
  <a id="drive-invalid-template" href="/scope-drive/invalid-template">Invalid template</a>
  <a id="drive-next" href="/scope-drive/same">Same</a>
</main>`

const scopeDriveInvalidTemplateShell = `<main id="scope-drive-shell" data-route="invalid-template">
  <div id="drive-retain-parent-invalid">
    <section id="drive-retained" data-kit-retain="scope-drive-retained" data-kit-component="scope-drive-retained" data-kit-scope="count: 999">
      <output id="drive-retained-count" data-kit-text="count">invalid</output>
    </section>
  </div>
  <template data-kit-scope="count: 1"><p id="drive-invalid-template-child">invalid</p></template>
</main>`

const scopeDriveSameShell = `<main id="scope-drive-shell" data-route="same">
  <section id="drive-normal" data-kit-component="scope-drive-normal" data-kit-scope="count: 3">
    <output id="drive-normal-count" data-kit-text="count">same</output>
  </section>
  <section id="drive-anonymous" data-kit-scope="count: 10">
    <output id="drive-anonymous-count" data-kit-text="count">same</output>
  </section>
  <div id="drive-retain-parent-b">
    <section id="drive-retained" data-kit-retain="scope-drive-retained" data-kit-component="scope-drive-retained" data-kit-scope="count: 999">
      <output id="drive-retained-count" data-kit-text="count">same</output>
    </section>
  </div>
  <a id="drive-changed" href="/scope-drive/changed">Changed</a>
</main>`

const scopeDriveChangedShell = `<main id="scope-drive-shell" data-route="changed">
  <section id="drive-normal" data-kit-component="scope-drive-normal" data-kit-scope="count: 100">
    <output id="drive-normal-count" data-kit-text="count">changed</output>
  </section>
  <section id="drive-anonymous" data-kit-scope="count: 200">
    <output id="drive-anonymous-count" data-kit-text="count">changed</output>
  </section>
  <div id="drive-retain-parent-c">
    <section id="drive-retained" data-kit-retain="scope-drive-retained" data-kit-component="scope-drive-retained" data-kit-scope="count: 888">
      <output id="drive-retained-count" data-kit-text="count">changed</output>
    </section>
  </div>
  <a id="drive-component" href="/scope-drive/component">Component</a>
</main>`

const scopeDriveComponentShell = `<main id="scope-drive-shell" data-route="component">
  <section id="drive-normal" data-kit-component="scope-drive-normal" data-kit-scope="count: 100">
    <output id="drive-normal-count" data-kit-text="count">component</output>
  </section>
  <section id="drive-anonymous" data-kit-scope="count: 200">
    <output id="drive-anonymous-count" data-kit-text="count">component</output>
  </section>
  <div id="drive-retain-parent-d">
    <section id="drive-retained" data-kit-retain="scope-drive-retained" data-kit-component="scope-drive-retained" data-kit-scope="count: 777">
      <output id="drive-retained-count" data-kit-text="count">component</output>
    </section>
  </div>
  <a id="drive-removed" href="/scope-drive/removed">Removed</a>
</main>`

const scopeDriveRemovedShell = `<main id="scope-drive-shell" data-route="removed">
  <p id="drive-empty">All boundaries removed</p>
</main>`

var scopeDriveInitialDocument = `<!doctype html><html lang="en"><head>
  <meta charset="utf-8"><title>Initial</title>
  <script>
    globalThis.__scopeDriveErrors = [];
    globalThis.__scopeDriveConsoleError = console.error;
    console.error = function (error) {
      globalThis.__scopeDriveErrors.push(String(error && error.message || error));
      return globalThis.__scopeDriveConsoleError.apply(console, arguments);
    };
  </script>
  <script src="/hydrate.kit.js?v=scope-contract"></script>
</head><body data-kit-component="scope-drive-body-a" data-kit-scope="route: 'initial'">
` + scopeDriveInitialShell + `
<script>
  globalThis.__scopeDriveIdentity = {};
  globalThis.__scopeDriveLifecycle = {
    bodyAInit: 0, bodyACleanup: 0, bodyBInit: 0, bodyBCleanup: 0,
    normalInit: 0, normalCleanup: 0, retainedInit: 0, retainedCleanup: 0
  };
  kit.component("scope-drive-body-a", {
    route: "",
    init: function () {
      __scopeDriveLifecycle.bodyAInit++;
      return function () { __scopeDriveLifecycle.bodyACleanup++; };
    }
  });
  kit.component("scope-drive-body-b", {
    route: "",
    init: function () {
      __scopeDriveLifecycle.bodyBInit++;
      return function () { __scopeDriveLifecycle.bodyBCleanup++; };
    }
  });
  kit.component("scope-drive-normal", {
    count: 0,
    init: function () {
      __scopeDriveLifecycle.normalInit++;
      return function () { __scopeDriveLifecycle.normalCleanup++; };
    }
  });
  kit.component("scope-drive-retained", {
    count: 0,
    init: function () {
      __scopeDriveLifecycle.retainedInit++;
      return function () { __scopeDriveLifecycle.retainedCleanup++; };
    }
  });
</script>
<script src="/hydrate.kit.js?v=scope-contract"></script>
<script>
` + browserHarness + `
__runStandaloneKitTest(async function () {
  var assert = __kitTestAssert;
  var waitFor = __kitTestWaitFor;
  var identity = globalThis.__scopeDriveIdentity;
  var lifecycle = globalThis.__scopeDriveLifecycle;
  var initialBody = document.body;
  var retainedElement = document.getElementById("drive-retained");

  await waitFor(function () {
    return document.getElementById("drive-normal-count").textContent.trim() === "3" &&
      document.getElementById("drive-anonymous-count").textContent.trim() === "10" &&
      document.getElementById("drive-retained-count").textContent.trim() === "20";
  }, "Drive scope fixtures did not boot");
  assert(lifecycle.bodyAInit === 1 && lifecycle.bodyACleanup === 0 &&
    lifecycle.normalInit === 1 && lifecycle.retainedInit === 1,
    "Drive scope components did not initialize exactly once");

  document.getElementById("drive-normal-add").click();
  document.getElementById("drive-anonymous-add").click();
  document.getElementById("drive-retained-add").click();
  await waitFor(function () {
    return document.getElementById("drive-normal-count").textContent.trim() === "4" &&
      document.getElementById("drive-anonymous-count").textContent.trim() === "11" &&
      document.getElementById("drive-retained-count").textContent.trim() === "21";
  }, "Drive scope state did not become live");

  document.getElementById("drive-invalid-template").click();
  await waitFor(function () {
    return globalThis.__scopeDriveErrors.some(function (message) {
      return message === "KitJS: data-kit-scope cannot be used on a template; place the boundary inside template.content";
    });
  }, "Drive did not report the invalid incoming template boundary");
  await new Promise(function (resolve) { setTimeout(resolve, 80); });
  assert(location.pathname === "/scope-drive" && document.title === "Initial" &&
    document.body === initialBody && document.getElementById("scope-drive-shell").getAttribute("data-route") === "initial" &&
    document.getElementById("drive-retained") === retainedElement && retainedElement.parentElement.id === "drive-retain-parent-a" &&
    document.getElementById("drive-normal-count").textContent.trim() === "4" &&
    document.getElementById("drive-anonymous-count").textContent.trim() === "11" &&
    document.getElementById("drive-retained-count").textContent.trim() === "21" &&
    lifecycle.bodyAInit === 1 && lifecycle.bodyACleanup === 0 &&
    lifecycle.normalInit === 1 && lifecycle.normalCleanup === 0 &&
    lifecycle.retainedInit === 1 && lifecycle.retainedCleanup === 0,
    "Drive invalid-template preflight or 204 fallback mutated URL, title, body, state, lifecycle, or retain identity");

  document.getElementById("drive-next").click();
  await waitFor(function () {
    return location.pathname === "/scope-drive/same" &&
      document.getElementById("scope-drive-shell").getAttribute("data-route") === "same" &&
      document.getElementById("drive-normal-count").textContent.trim() === "4" &&
      document.getElementById("drive-anonymous-count").textContent.trim() === "11" &&
      document.getElementById("drive-retained-count").textContent.trim() === "21";
  }, "same-source Drive navigation reset scope state");
  assert(globalThis.__scopeDriveIdentity === identity, "Drive performed a hard reload");
  assert(document.body === initialBody, "same-source Drive replaced the body scope root");
  assert(document.getElementById("drive-retained").parentElement.id === "drive-retain-parent-b" &&
    document.getElementById("drive-retained").getAttribute("data-kit-scope") === "count: 999",
    "Drive did not move retained markup while preserving live state");
  assert(lifecycle.normalInit === 1 && lifecycle.normalCleanup === 0 &&
    lifecycle.retainedInit === 1 && lifecycle.retainedCleanup === 0 &&
    lifecycle.bodyAInit === 1 && lifecycle.bodyACleanup === 0,
    "same-source Drive navigation ran lifecycle or reseeded retain");

  document.getElementById("drive-changed").click();
  await waitFor(function () {
    return location.pathname === "/scope-drive/changed" &&
      document.getElementById("scope-drive-shell").getAttribute("data-route") === "changed" &&
      document.getElementById("drive-normal-count").textContent.trim() === "100" &&
      document.getElementById("drive-anonymous-count").textContent.trim() === "200" &&
      document.getElementById("drive-retained-count").textContent.trim() === "21";
  }, "changed-source Drive navigation did not remount only non-retained state");
  assert(globalThis.__scopeDriveIdentity === identity, "changed-source Drive performed a hard reload");
  assert(document.body !== initialBody, "changed body scope source did not replace its root boundary");
  assert(document.getElementById("drive-retained") === retainedElement,
    "root-incompatible Drive replaced a globally paired retained descendant");
  assert(lifecycle.normalInit === 2 && lifecycle.normalCleanup === 1 &&
    lifecycle.retainedInit === 1 && lifecycle.retainedCleanup === 0 &&
    lifecycle.bodyAInit === 2 && lifecycle.bodyACleanup === 1,
    "changed-source Drive lifecycle was not exactly once");

  var changedBody = document.body;
  document.getElementById("drive-component").click();
  await waitFor(function () {
    return location.pathname === "/scope-drive/component" &&
      document.getElementById("scope-drive-shell").getAttribute("data-route") === "component" &&
      document.getElementById("drive-normal-count").textContent.trim() === "100" &&
      document.getElementById("drive-anonymous-count").textContent.trim() === "200" &&
      document.getElementById("drive-retained-count").textContent.trim() === "21";
  }, "changed body component identity did not reconcile through Drive");
  assert(document.body !== changedBody, "changed body component identity did not replace its root boundary");
  assert(document.getElementById("drive-retained") === retainedElement,
    "component-incompatible Drive replaced a globally paired retained descendant");
  assert(lifecycle.normalInit === 3 && lifecycle.normalCleanup === 2 &&
    lifecycle.retainedInit === 1 && lifecycle.retainedCleanup === 0 &&
    lifecycle.bodyAInit === 2 && lifecycle.bodyACleanup === 2 &&
    lifecycle.bodyBInit === 1 && lifecycle.bodyBCleanup === 0,
    "component-incompatible Drive lifecycle was not exactly once");

  document.getElementById("drive-removed").click();
  await waitFor(function () {
    return location.pathname === "/scope-drive/removed" && document.getElementById("drive-empty");
  }, "Drive did not commit boundary removal");
  await waitFor(function () {
    return lifecycle.normalCleanup === 3 && lifecycle.retainedCleanup === 1;
  }, "Drive did not clean removed scope components exactly once");
  assert(globalThis.__scopeDriveIdentity === identity, "boundary removal performed a hard reload");
  assert(!document.body.querySelector("[data-kit-scope],[data-kit-retain]"),
    "removed Drive descendant boundaries survived in the live document");
});
</script></body></html>`

func scopeBrowserFixture(t *testing.T) string {
	t.Helper()
	fragments, err := FragmentNamesForProfile(ProfileHydrate)
	if err != nil {
		t.Fatal(err)
	}

	tooLong := `value: "` + strings.Repeat("x", 16384) + `"`
	tooLongWhitespace := strings.Repeat(" ", 16384) + `count: 1`
	tooDeep := `value: ` + strings.Repeat("[", 33) + `0` + strings.Repeat("]", 33)
	tooMany := `items: [` + strings.Repeat("0,", 1024) + `0]`
	lengthPrefix := `value: "`
	lengthSuffix := `"`
	exactLength := lengthPrefix + strings.Repeat("x", 16384-len(lengthPrefix)-len(lengthSuffix)) + lengthSuffix
	exactDepth := `value: ` + strings.Repeat("[", 31) + `0` + strings.Repeat("]", 31)
	exactNodes := `items: [` + strings.Repeat("0,", 1021) + `0]`

	var page strings.Builder
	page.WriteString(`<!doctype html><html lang="en"><head><meta charset="utf-8"><title>KitJS scope contract</title></head>
<body>
  <main id="outer" data-kit-scope="count: 3; open: true">
    <button id="outer-add" type="button" data-kit-click="count = count + 1">Outer</button>
    <output id="outer-count" data-kit-text="count">server</output>
    <section id="inner" data-kit-scope="count: 7">
      <button id="inner-add" type="button" data-kit-click="count = count + 1">Inner</button>
      <output id="inner-count" data-kit-text="count">server</output>
    </section>
  </main>
  <section id="quoted-shorthand" data-kit-scope='"count": 1'>
    <output id="quoted-shorthand-count" data-kit-text="count">server</output>
  </section>
  <section id="prototype-word-shorthand" data-kit-scope="toString: 1; valueOf: 2; hasOwnProperty: 3">
    <output id="prototype-word-shorthand-value" data-kit-text="toString + valueOf + hasOwnProperty">server-prototype-shorthand</output>
  </section>
  <section id="prototype-word-object" data-kit-scope="{ toString: 4, valueOf: 5, hasOwnProperty: 6, meta: { toString: 7, valueOf: 8, hasOwnProperty: 9 } }">
    <output id="prototype-word-object-value" data-kit-text="toString + valueOf + hasOwnProperty">server-prototype-object</output>
    <output id="prototype-word-nested-value" data-kit-text="meta.toString + meta.valueOf + meta.hasOwnProperty">server-prototype-nested</output>
  </section>
  <section id="valid-template-outer" data-kit-scope="show: true">
    <template id="valid-template-owner" data-kit-if="show">
      <section id="valid-template-scope" data-kit-scope="count: 5">
        <output id="valid-template-scope-count" data-kit-text="count">server-template-scope</output>
      </section>
    </template>
  </section>
  <section id="object-scope" data-kit-scope='{ "rows": [1, 2], "meta": { "ok": true, "location": 1, "eval": 2, "true": 3, "window": 7 } }'>
    <output id="object-rows" data-kit-text="rows.join('-')">server</output>
    <output id="object-ok" data-kit-text="meta.ok">server</output>
  </section>
  <section id="serialized" data-kit-scope='message: &quot;A &amp; B &lt;ok&gt;&quot;; emoji: &quot;😀&quot;; json: &quot;\u004b\u0069\u0074\u004a\u0053&quot;; pair: &quot;\uD83D\uDE00&quot;; controls: &quot;\b\f\n\r\t\&quot;\\\/&quot;; amount: -12.5e2'>
    <output id="serialized-message" data-kit-text="message">server</output>
    <output id="serialized-emoji" data-kit-text="emoji">server</output>
    <output id="serialized-json" data-kit-text="json">server</output>
    <output id="serialized-pair" data-kit-text="pair">server</output>
    <output id="serialized-controls" data-kit-text="controls">server</output>
    <output id="serialized-amount" data-kit-text="amount">server</output>
  </section>
  <section id="component-seed" data-kit-component="scope-seeded" data-kit-scope="count: 3; open: true">
    <button id="component-add" type="button" data-kit-click="bump()">Component</button>
    <output id="component-count" data-kit-text="count">server</output>
    <output id="component-open" data-kit-text="open">server</output>
  </section>
  <section id="alias-target" data-kit-component="scope-alias-target" data-kit-as="$target">
    <output id="alias-touches" data-kit-text="touches">server</output>
  </section>

  <section id="invalid-empty" data-kit-scope=""><output>server-empty</output></section>
  <section id="invalid-alias-scope" data-kit-scope="">
    <button id="invalid-alias-trigger" type="button" data-kit-click="$target.touch()">Invalid alias trigger</button>
    <output>server-alias</output>
  </section>
  <section id="invalid-scope-alias" data-kit-scope="count: 1" data-kit-as="$target">
    <button id="invalid-scope-alias-trigger" type="button" data-kit-click="$target.touch()">Invalid scope alias</button>
    <output id="invalid-scope-alias-count" data-kit-text="count">server-scope-alias</output>
  </section>
  <section id="invalid-scope-version" data-kit-scope="count: 1" data-kit-version="1.0.0">
    <output data-kit-text="count">server-scope-version</output>
  </section>
  <section id="invalid-unknown-value" data-kit-scope="count: other"><output>server-unknown-value</output></section>
  <section id="invalid-method-value" data-kit-scope="run: () => true"><output>server-method-value</output></section>
  <section id="invalid-blocked" data-kit-scope="safe: { __proto__: 1 }"><output>server-blocked</output></section>
  <section id="invalid-blocked-constructor" data-kit-scope='safe: { "constructor": 1 }'><output>server-blocked-constructor</output></section>
  <section id="invalid-blocked-prototype" data-kit-scope='safe: { "prototype": 1 }'><output>server-blocked-prototype</output></section>
  <section id="invalid-blocked-proto" data-kit-scope='safe: { "__proto__": 1 }'><output>server-blocked-proto</output></section>
  <section id="invalid-blocked-define-getter" data-kit-scope='safe: { "__defineGetter__": 1 }'><output>server-blocked-define-getter</output></section>
  <section id="invalid-blocked-define-setter" data-kit-scope='safe: { "__defineSetter__": 1 }'><output>server-blocked-define-setter</output></section>
  <section id="invalid-blocked-lookup-getter" data-kit-scope='safe: { "__lookupGetter__": 1 }'><output>server-blocked-lookup-getter</output></section>
  <section id="invalid-blocked-lookup-setter" data-kit-scope='safe: { "__lookupSetter__": 1 }'><output>server-blocked-lookup-setter</output></section>
  <section id="invalid-top-true" data-kit-scope="true: 1"><output>server-top-true</output></section>
  <section id="invalid-top-false" data-kit-scope="false: 1"><output>server-top-false</output></section>
  <section id="invalid-top-null" data-kit-scope="null: 1"><output>server-top-null</output></section>
  <section id="invalid-top-location" data-kit-scope="location: 1"><output>server-top-location</output></section>
  <section id="invalid-top-eval" data-kit-scope="eval: 1"><output>server-top-eval</output></section>
  <section id="invalid-top-window" data-kit-scope="window: 1"><output>server-top-window</output></section>
  <section id="invalid-top-quoted-true" data-kit-scope='{ "true": 1 }'><output>server-top-quoted-true</output></section>
  <section id="invalid-top-quoted-false" data-kit-scope='{ "false": 1 }'><output>server-top-quoted-false</output></section>
  <section id="invalid-top-quoted-null" data-kit-scope='{ "null": 1 }'><output>server-top-quoted-null</output></section>
  <section id="invalid-top-quoted-location" data-kit-scope='{ "location": 1 }'><output>server-top-quoted-location</output></section>
  <section id="invalid-top-quoted-eval" data-kit-scope='{ "eval": 1 }'><output>server-top-quoted-eval</output></section>
  <section id="invalid-top-quoted-window" data-kit-scope='{ "window": 1 }'><output>server-top-quoted-window</output></section>
  <section id="invalid-nested-true" data-kit-scope="safe: { true: 1 }"><output>server-nested-true</output></section>
  <section id="invalid-nested-false" data-kit-scope="safe: { false: 1 }"><output>server-nested-false</output></section>
  <section id="invalid-nested-null" data-kit-scope="safe: { null: 1 }"><output>server-nested-null</output></section>
  <section id="invalid-leading-nbsp" data-kit-scope="&#160;count: 1"><output>server-leading-nbsp</output></section>
  <section id="invalid-trailing-nbsp" data-kit-scope="count: 1&#160;"><output>server-trailing-nbsp</output></section>
  <section id="invalid-duplicate" data-kit-scope="count: 1; count: 2"><output>server-duplicate</output></section>
  <section id="invalid-escape" data-kit-scope='text: "\u12G4"'><output>server-escape</output></section>
  <section id="invalid-lone-high" data-kit-scope='text: "\uD800"'><output>server-lone-high</output></section>
  <section id="invalid-lone-low" data-kit-scope='text: "\uDC00"'><output>server-lone-low</output></section>
  <section id="invalid-unknown-field" data-kit-component="scope-invalid-unknown" data-kit-scope="missing: 2"><output>server-unknown-field</output></section>
  <section id="invalid-method-field" data-kit-component="scope-invalid-method" data-kit-scope="run: 2"><output>server-method-field</output></section>
  <section id="invalid-accessor-field" data-kit-component="scope-invalid-accessor" data-kit-scope="value: 2"><output>server-accessor-field</output></section>
  <section id="invalid-init-field" data-kit-component="scope-invalid-init" data-kit-scope="init: 2"><output>server-init-field</output></section>
  <section id="invalid-retain" data-kit-retain="anonymous" data-kit-scope="count: 1"><output>server-retain</output></section>
  <section id="invalid-props" data-kit-props="count: 1"><output>server-props</output></section>
  <template id="invalid-template-scope" data-kit-scope="count: 1"><button data-invalid-template-materialized data-kit-click="$target.touch()">scope action</button><output data-kit-text="count">server-template-scope</output></template>
  <template id="invalid-template-component" data-kit-component="scope-invalid-template"><button data-invalid-template-materialized data-kit-click="$target.touch()">component action</button><output>server-template-component</output></template>
  <template id="invalid-template-if" data-kit-if="true" data-kit-scope="count: 1"><button data-invalid-template-materialized data-kit-click="$target.touch()">if action</button><output data-kit-text="count">server-template-if</output></template>
  <template id="invalid-template-for" data-kit-for="item of items" data-kit-scope="items: [1]"><button data-invalid-template-materialized data-kit-click="$target.touch()">for action</button><output data-kit-text="item">server-template-for</output></template>
  <button id="body-alias-trigger" type="button" data-kit-click="$target.touch()">Body alias trigger</button>
`)
	page.WriteString(`  <section id="invalid-length" data-kit-scope='` + html.EscapeString(tooLong) + `'><output>server-length</output></section>
`)
	page.WriteString(`  <section id="invalid-length-whitespace" data-kit-scope='` + html.EscapeString(tooLongWhitespace) + `'><output>server-length-whitespace</output></section>
`)
	page.WriteString(`  <section id="invalid-depth" data-kit-scope='` + html.EscapeString(tooDeep) + `'><output>server-depth</output></section>
`)
	page.WriteString(`  <section id="invalid-nodes" data-kit-scope='` + html.EscapeString(tooMany) + `'><output>server-nodes</output></section>
`)
	page.WriteString(`  <section id="valid-limit-length" data-kit-scope='` + html.EscapeString(exactLength) + `'></section>
`)
	page.WriteString(`  <section id="valid-limit-depth" data-kit-scope='` + html.EscapeString(exactDepth) + `'></section>
`)
	page.WriteString(`  <section id="valid-limit-nodes" data-kit-scope='` + html.EscapeString(exactNodes) + `'></section>
`)
	page.WriteString(`
  <div id="morph-root">
    <section id="morph-normal" data-kit-component="scope-morph-normal" data-kit-scope="count: 3">
      <button id="morph-normal-add" type="button" data-kit-click="count = count + 1">Normal</button>
      <output id="morph-normal-count" data-kit-text="count">server</output>
    </section>
    <section id="morph-anonymous" data-kit-scope="count: 10">
      <button id="morph-anonymous-add" type="button" data-kit-click="count = count + 1">Anonymous</button>
      <output id="morph-anonymous-count" data-kit-text="count">server</output>
    </section>
    <div id="retain-parent">
      <section id="morph-retained" data-kit-retain="scope-retained" data-kit-component="scope-morph-retained" data-kit-scope="count: 20">
        <button id="morph-retained-add" type="button" data-kit-click="count = count + 1">Retained</button>
        <output id="morph-retained-count" data-kit-text="count">server</output>
      </section>
    </div>
  </div>
  <script>
    globalThis.__scopeErrors = [];
    globalThis.__invalidTemplateHTML = {};
    ["invalid-template-scope", "invalid-template-component", "invalid-template-if", "invalid-template-for"].forEach(function (id) {
      globalThis.__invalidTemplateHTML[id] = document.getElementById(id).innerHTML;
    });
    globalThis.__dynamicCodeCalls = 0;
    globalThis.__originalConsoleError = console.error;
    console.error = function (error) {
      globalThis.__scopeErrors.push(String(error && error.message || error));
      return globalThis.__originalConsoleError.apply(console, arguments);
    };
    globalThis.eval = function () { globalThis.__dynamicCodeCalls++; throw new Error("eval called"); };
    globalThis.Function = function () { globalThis.__dynamicCodeCalls++; throw new Error("Function called"); };
  </script>
`)
	for _, fragment := range fragments {
		if fragment == "src/boot.js" {
			page.WriteString(`<script>
  globalThis.__scopeCore = document[Symbol.for("kitjs:assembly")];
  globalThis.__scopeLifecycle = {
    seededInit: 0,
    invalidInit: 0,
    invalidTemplateInit: 0,
    normalInit: 0,
    normalCleanup: 0,
    retainedInit: 0,
    retainedCleanup: 0
  };
  __scopeCore.component("scope-seeded", {
    count: 0,
    open: false,
    bump: function () { globalThis.__seedEventOwner = this; this.count++; },
    init: function () {
      __scopeLifecycle.seededInit++;
      globalThis.__seedInit = { owner: this, count: this.count, open: this.open };
    }
  });
  __scopeCore.component("scope-invalid-unknown", {
    known: 1,
    init: function () { __scopeLifecycle.invalidInit++; }
  });
  __scopeCore.component("scope-invalid-method", {
    value: 1,
    run: function () { return true; },
    init: function () { __scopeLifecycle.invalidInit++; }
  });
  var accessorDefinition = { init: function () { __scopeLifecycle.invalidInit++; } };
  Object.defineProperty(accessorDefinition, "value", {
    enumerable: true,
    configurable: true,
    get: function () { return 1; }
  });
  __scopeCore.component("scope-invalid-accessor", accessorDefinition);
  __scopeCore.component("scope-invalid-init", {
    value: 1,
    init: function () { __scopeLifecycle.invalidInit++; }
  });
  __scopeCore.component("scope-invalid-template", {
    init: function () { __scopeLifecycle.invalidTemplateInit++; }
  });
  __scopeCore.component("scope-alias-target", {
    touches: 0,
    touch: function () { this.touches++; }
  });
  __scopeCore.component("scope-morph-normal", {
    count: 0,
    init: function () {
      __scopeLifecycle.normalInit++;
      return function () { __scopeLifecycle.normalCleanup++; };
    }
  });
  __scopeCore.component("scope-morph-retained", {
    count: 0,
    init: function () {
      __scopeLifecycle.retainedInit++;
      return function () { __scopeLifecycle.retainedCleanup++; };
    }
  });
  globalThis.__scopeMorph = __scopeCore.morph;
</script>
`)
		}
		page.WriteString(`<script src="/` + html.EscapeString(fragment) + `"></script>
`)
	}
	page.WriteString(`<script>
`)
	page.WriteString(browserHarness)
	page.WriteString(`
__runStandaloneKitTest(async function () {
  var assert = __kitTestAssert;
  var waitFor = __kitTestWaitFor;
  var nextTurn = __kitTestNextTurn;
  var core = globalThis.__scopeCore;
  var morph = globalThis.__scopeMorph;
  var lifecycle = globalThis.__scopeLifecycle;

  function record(id) { return core.scopes.get(document.getElementById(id)); }
  function scope(id) {
    var current = record(id);
    return current && !current.failed && !current.disposed ? current.scope : null;
  }
  function parsedRoot(markup) {
    return new DOMParser().parseFromString("<!doctype html><html><body>" + markup + "</body></html>", "text/html").body.firstElementChild;
  }

  assert(typeof morph === "function" && kit.morph === undefined && kit.scope === undefined,
    "scope or Morph leaked a public API");
  assert(Object.keys(kit).join(",") === "version,component", "scope expanded the public kit facade");
  await waitFor(function () {
    return document.getElementById("outer-count").textContent.trim() === "3" &&
      document.getElementById("inner-count").textContent.trim() === "7" &&
      document.getElementById("component-count").textContent.trim() === "3" &&
      document.getElementById("prototype-word-shorthand-value").textContent.trim() === "6" &&
      document.getElementById("prototype-word-object-value").textContent.trim() === "15" &&
      document.getElementById("prototype-word-nested-value").textContent.trim() === "24" &&
      document.getElementById("valid-template-scope-count").textContent.trim() === "5" &&
      document.getElementById("morph-retained-count").textContent.trim() === "20";
  }, "valid scope boundaries did not boot");

  assert(document.getElementById("object-rows").textContent.trim() === "1-2" &&
    document.getElementById("object-ok").textContent.trim() === "true",
    "object-form scope did not render");
  assert(document.getElementById("quoted-shorthand-count").textContent.trim() === "1",
    "quoted shorthand field did not render");
  var objectScope = scope("object-scope");
  assert(objectScope && objectScope.meta.location === 1 && objectScope.meta.eval === 2 &&
    objectScope.meta.true === 3 && objectScope.meta.window === 7,
    "safe nested quoted location/eval/true/window keys did not survive parsing and cloning");
  var prototypeWordShorthand = scope("prototype-word-shorthand");
  var prototypeWordObject = scope("prototype-word-object");
  assert(prototypeWordShorthand && prototypeWordShorthand.toString === 1 &&
    prototypeWordShorthand.valueOf === 2 && prototypeWordShorthand.hasOwnProperty === 3 &&
    prototypeWordObject && prototypeWordObject.toString === 4 && prototypeWordObject.valueOf === 5 &&
    prototypeWordObject.hasOwnProperty === 6 && prototypeWordObject.meta.toString === 7 &&
    prototypeWordObject.meta.valueOf === 8 && prototypeWordObject.meta.hasOwnProperty === 9,
    "Object.prototype-looking safe fields did not survive shorthand, object, or nested parsing");
  ["invalid-template-scope", "invalid-template-component", "invalid-template-if", "invalid-template-for"].forEach(function (id) {
    var template = document.getElementById(id);
    var current = record(id);
    assert(template && template.innerHTML === globalThis.__invalidTemplateHTML[id],
      id + " changed template.content while failing closed");
    assert(!current || current.failed || current.disposed, id + " mounted a template boundary");
  });
  assert(!document.querySelector("[data-invalid-template-materialized]"),
    "an invalid template boundary materialized authored content or actions");
  assert(lifecycle.invalidTemplateInit === 0, "a template component reached init");
  assert(globalThis.__scopeErrors.some(function (message) {
    return message === "KitJS: data-kit-scope cannot be used on a template; place the boundary inside template.content";
  }), "template scope did not report its deterministic ownership error");
  assert(globalThis.__scopeErrors.some(function (message) {
    return message === "KitJS: data-kit-component cannot be used on a template; place the boundary inside template.content";
  }), "template component did not report its deterministic ownership error");
  assert(document.getElementById("serialized-message").textContent === "A & B <ok>" &&
    document.getElementById("serialized-emoji").textContent === "😀" &&
    document.getElementById("serialized-json").textContent === "KitJS" &&
    document.getElementById("serialized-pair").textContent === "😀" &&
    document.getElementById("serialized-controls").textContent === "\b\f\n\r\t\"\\/" &&
    document.getElementById("serialized-amount").textContent.trim() === "-1250",
    "server-serialized scope values did not round-trip");
  var serializedScope = scope("serialized");
  assert(serializedScope, "serialized scope record is missing");
  assert(Object.getPrototypeOf(serializedScope) === null, "anonymous state did not use a null prototype");

  assert(lifecycle.seededInit === 1 && globalThis.__seedInit &&
    globalThis.__seedInit.count === 3 && globalThis.__seedInit.open === true,
    "component seed was not applied before init");
  document.getElementById("outer-add").click();
  document.getElementById("inner-add").click();
  document.getElementById("component-add").click();
  await waitFor(function () {
    return document.getElementById("outer-count").textContent.trim() === "4" &&
      document.getElementById("inner-count").textContent.trim() === "8" &&
      document.getElementById("component-count").textContent.trim() === "4";
  }, "nested or component scope action did not commit");
  assert(globalThis.__seedEventOwner && globalThis.__seedEventOwner.count === 4 &&
    globalThis.__seedInit.owner.count === 4,
    "component seed, init, and event did not share one state store");

  var invalid = [
    "invalid-empty", "invalid-alias-scope", "invalid-scope-alias", "invalid-scope-version",
    "invalid-unknown-value", "invalid-method-value", "invalid-blocked",
    "invalid-blocked-constructor", "invalid-blocked-prototype", "invalid-blocked-proto",
    "invalid-blocked-define-getter", "invalid-blocked-define-setter",
    "invalid-blocked-lookup-getter", "invalid-blocked-lookup-setter",
    "invalid-top-true", "invalid-top-false", "invalid-top-null",
    "invalid-top-location", "invalid-top-eval", "invalid-top-window",
    "invalid-top-quoted-true", "invalid-top-quoted-false", "invalid-top-quoted-null",
    "invalid-top-quoted-location", "invalid-top-quoted-eval", "invalid-top-quoted-window",
    "invalid-nested-true", "invalid-nested-false", "invalid-nested-null",
    "invalid-leading-nbsp", "invalid-trailing-nbsp",
    "invalid-duplicate", "invalid-escape", "invalid-lone-high", "invalid-lone-low",
    "invalid-unknown-field", "invalid-method-field",
    "invalid-accessor-field", "invalid-init-field", "invalid-retain", "invalid-length",
    "invalid-length-whitespace",
    "invalid-depth", "invalid-nodes"
  ];
  invalid.forEach(function (id) {
    var current = record(id);
    assert(!current || current.failed || current.disposed, id + " mounted an invalid scope boundary");
    assert(document.getElementById(id).textContent.indexOf("server-") >= 0,
      id + " corrupted server HTML while failing closed");
  });
  var removedFailedElement = document.getElementById("invalid-empty");
  var removedFailedRecord = core.scopes.get(removedFailedElement);
  removedFailedElement.remove();
  await nextTurn();
  assert(removedFailedRecord && removedFailedRecord.failed && removedFailedRecord.disposed &&
    removedFailedRecord.host === null && !core.scopes.get(removedFailedElement),
    "direct removal retained a failed boundary record or DOM host");
  assert(lifecycle.invalidInit === 0, "an invalid component seed reached init");
  assert(globalThis.__scopeErrors.some(function (message) { return message.indexOf("data-kit-props") >= 0; }),
    "data-kit-props was not rejected as unsupported");
  assert(!record("invalid-props"), "data-kit-props created a state boundary");
  ["valid-limit-length", "valid-limit-depth", "valid-limit-nodes"].forEach(function (id) {
    assert(scope(id), id + " rejected an exact parser budget limit");
  });
  await waitFor(function () { return document.getElementById("alias-touches").textContent.trim() === "0"; },
    "alias target did not boot");
  document.getElementById("invalid-alias-trigger").click();
  document.getElementById("invalid-scope-alias-trigger").click();
  await nextTurn();
  assert(document.getElementById("alias-touches").textContent.trim() === "0",
    "an action escaped from a failed scope boundary");
  assert(document.getElementById("invalid-scope-alias-count").textContent.trim() === "server-scope-alias",
    "a scope-only alias mounted state or executed a descendant action");
  document.getElementById("body-alias-trigger").click();
  await waitFor(function () { return document.getElementById("alias-touches").textContent.trim() === "1"; },
    "an identical alias action outside any boundary did not execute");

  var failedAliasElement = document.getElementById("invalid-scope-alias");
  var failedVersionElement = document.getElementById("invalid-scope-version");
  var failedAliasRecord = record("invalid-scope-alias");
  var failedVersionRecord = record("invalid-scope-version");
  var recoveredAliasElement = morph(failedAliasElement, parsedRoot(
    '<section id="invalid-scope-alias" data-kit-scope="count: 1">' +
      '<button id="recovered-alias-add" type="button" data-kit-click="count = count + 1">Add</button>' +
      '<output id="invalid-scope-alias-count" data-kit-text="count">recovering</output>' +
    '</section>'));
  var recoveredVersionElement = morph(failedVersionElement, parsedRoot(
    '<section id="invalid-scope-version" data-kit-scope="count: 2">' +
      '<button id="recovered-version-add" type="button" data-kit-click="count = count + 1">Add</button>' +
      '<output id="invalid-scope-version-count" data-kit-text="count">recovering</output>' +
    '</section>'));
  assert(recoveredAliasElement !== failedAliasElement && recoveredVersionElement !== failedVersionElement,
    "Morph reused a failed anonymous boundary after alias/version metadata was removed");
  await waitFor(function () {
    return document.getElementById("invalid-scope-alias-count").textContent.trim() === "1" &&
      document.getElementById("invalid-scope-version-count").textContent.trim() === "2";
  }, "corrected alias/version scope boundaries did not recover");
  document.getElementById("recovered-alias-add").click();
  document.getElementById("recovered-version-add").click();
  await waitFor(function () {
    return document.getElementById("invalid-scope-alias-count").textContent.trim() === "2" &&
      document.getElementById("invalid-scope-version-count").textContent.trim() === "3";
  }, "recovered anonymous scope actions did not execute");
  assert(failedAliasRecord && failedVersionRecord &&
    failedAliasRecord.disposed && failedAliasRecord.host === null &&
    failedVersionRecord.disposed && failedVersionRecord.host === null &&
    !core.scopes.get(failedAliasElement) && !core.scopes.get(failedVersionElement) &&
    record("invalid-scope-alias") !== failedAliasRecord && record("invalid-scope-version") !== failedVersionRecord,
    "corrected anonymous scopes retained their failed elements or records");

  document.getElementById("morph-normal-add").click();
  document.getElementById("morph-anonymous-add").click();
  document.getElementById("morph-retained-add").click();
  await waitFor(function () {
    return document.getElementById("morph-normal-count").textContent.trim() === "4" &&
      document.getElementById("morph-anonymous-count").textContent.trim() === "11" &&
      document.getElementById("morph-retained-count").textContent.trim() === "21";
  }, "scope fixtures did not become live before Morph");
  assert(lifecycle.normalInit === 1 && lifecycle.retainedInit === 1,
    "initial Morph component lifecycle count is wrong");

  var root = document.getElementById("morph-root");
  var normal = document.getElementById("morph-normal");
  var anonymous = document.getElementById("morph-anonymous");
  var retained = document.getElementById("morph-retained");
  var normalRecord = record("morph-normal");
  var anonymousRecord = record("morph-anonymous");
  var retainedRecord = record("morph-retained");
  var normalScope = scope("morph-normal");
  var anonymousScope = scope("morph-anonymous");
  var retainedScope = scope("morph-retained");
  var preflightHTML = root.innerHTML;
  var invalidTemplateIncoming = parsedRoot(
    '<div id="morph-root" data-preflight-mutated="true">' +
      '<div id="preflight-retain-target"><section id="morph-retained" data-kit-retain="scope-retained" ' +
        'data-kit-component="scope-morph-retained" data-kit-scope="count: 999"></section></div>' +
      '<template data-kit-scope="count: 1"><p id="preflight-child">invalid</p></template>' +
    '</div>');
  var preflightError = null;
  try { morph(root, invalidTemplateIncoming); }
  catch (error) { preflightError = error; }
  assert(preflightError && preflightError.message ===
    "KitJS: data-kit-scope cannot be used on a template; place the boundary inside template.content",
    "Morph did not synchronously reject an incoming template boundary");
  assert(document.getElementById("morph-root") === root && root.innerHTML === preflightHTML &&
    !root.hasAttribute("data-preflight-mutated") && !document.getElementById("preflight-child") &&
    document.getElementById("morph-normal") === normal && document.getElementById("morph-anonymous") === anonymous &&
    document.getElementById("morph-retained") === retained && retained.parentElement.id === "retain-parent" &&
    scope("morph-normal") === normalScope && scope("morph-anonymous") === anonymousScope &&
    scope("morph-retained") === retainedScope && lifecycle.normalCleanup === 0 && lifecycle.retainedCleanup === 0,
    "Morph mutated attributes, children, scopes, lifecycle, or retain placement before template preflight failed");
  var same = parsedRoot(
    '<div id="morph-root">' +
      '<section id="morph-normal" data-kit-component="scope-morph-normal" data-kit-scope="count: 3"><output id="morph-normal-count" data-kit-text="count">same</output></section>' +
      '<section id="morph-anonymous" data-kit-scope="count: 10"><output id="morph-anonymous-count" data-kit-text="count">same</output></section>' +
      '<div id="retain-parent"><section id="morph-retained" data-kit-retain="scope-retained" data-kit-component="scope-morph-retained" data-kit-scope="count: 20"><output id="morph-retained-count" data-kit-text="count">same</output></section></div>' +
    '</div>');
  assert(morph(root, same) === root, "same-source Morph replaced its root");
  await waitFor(function () {
    return document.getElementById("morph-normal-count").textContent.trim() === "4" &&
      document.getElementById("morph-anonymous-count").textContent.trim() === "11" &&
      document.getElementById("morph-retained-count").textContent.trim() === "21";
  }, "same-source Morph reset live state");
  assert(document.getElementById("morph-normal") === normal && scope("morph-normal") === normalScope &&
    document.getElementById("morph-anonymous") === anonymous && scope("morph-anonymous") === anonymousScope &&
    document.getElementById("morph-retained") === retained && scope("morph-retained") === retainedScope,
    "same-source Morph replaced a boundary identity");
  assert(lifecycle.normalInit === 1 && lifecycle.normalCleanup === 0 &&
    lifecycle.retainedInit === 1 && lifecycle.retainedCleanup === 0,
    "same-source Morph ran component lifecycle");

  var changed = parsedRoot(
    '<div id="morph-root">' +
      '<section id="morph-normal" data-kit-component="scope-morph-normal" data-kit-scope="count: 100"><output id="morph-normal-count" data-kit-text="count">changed</output></section>' +
      '<section id="morph-anonymous" data-kit-scope="count: 200"><output id="morph-anonymous-count" data-kit-text="count">changed</output></section>' +
      '<div id="retain-parent-next"><section id="morph-retained" data-kit-retain="scope-retained" data-kit-component="scope-morph-retained" data-kit-scope="count: 999"><output id="morph-retained-count" data-kit-text="count">changed</output></section></div>' +
    '</div>');
  assert(morph(root, changed) === root, "changed-source Morph replaced its root");
  await waitFor(function () {
    return document.getElementById("morph-normal-count").textContent.trim() === "100" &&
      document.getElementById("morph-anonymous-count").textContent.trim() === "200" &&
      document.getElementById("morph-retained-count").textContent.trim() === "21";
  }, "changed-source or retained Morph semantics did not settle");
  assert(document.getElementById("morph-normal") !== normal && scope("morph-normal") !== normalScope &&
    document.getElementById("morph-anonymous") !== anonymous && scope("morph-anonymous") !== anonymousScope,
    "changed non-retained scope source reused stale state");
  assert(document.getElementById("morph-retained") === retained && scope("morph-retained") === retainedScope &&
    retained.getAttribute("data-kit-scope") === "count: 999",
    "retained component did not keep live state while accepting incoming markup");
  assert(normalRecord.disposed && normalRecord.host === null && normalRecord.scope === null &&
    anonymousRecord.disposed && anonymousRecord.host === null && anonymousRecord.scope === null,
    "replaced scope records retained DOM or state references");
  assert(!core.scopeRecords.has(normalScope) && !core.scopeRecords.has(anonymousScope) &&
    !core.dirtyRecords.has(normalRecord) && !core.dirtyRecords.has(anonymousRecord),
    "replaced scope records remained reachable from runtime ownership indexes");
  assert(!retainedRecord.disposed && retainedRecord.host === retained && retainedRecord.scope === retainedScope,
    "retained component was disposed while its state was preserved");
  assert(lifecycle.normalInit === 2 && lifecycle.normalCleanup === 1 &&
    lifecycle.retainedInit === 1 && lifecycle.retainedCleanup === 0,
    "changed-source Morph lifecycle was not exactly once");

  var finalNormalRecord = record("morph-normal");
  var finalAnonymousRecord = record("morph-anonymous");
  var finalRetainedRecord = record("morph-retained");
  var finalNormalScope = finalNormalRecord.scope;
  var finalAnonymousScope = finalAnonymousRecord.scope;
  var finalRetainedScope = finalRetainedRecord.scope;
  var empty = parsedRoot('<div id="morph-root"><p id="after-remove">empty</p></div>');
  assert(morph(root, empty) === root, "boundary removal replaced its root");
  await nextTurn();
  assert(lifecycle.normalCleanup === 2 && lifecycle.retainedCleanup === 1,
    "removed component scopes did not clean up exactly once");
  assert(!core.scopes.get(normal) && !core.scopes.get(anonymous) && !core.scopes.get(retained),
    "removed scope records survived disposal");
  [finalNormalRecord, finalAnonymousRecord, finalRetainedRecord].forEach(function (current) {
    assert(current.disposed && current.host === null && current.scope === null &&
      !core.dirtyRecords.has(current), "removed scope record retained GC roots");
  });
  assert(!core.scopeRecords.has(finalNormalScope) && !core.scopeRecords.has(finalAnonymousScope) &&
    !core.scopeRecords.has(finalRetainedScope), "removed state proxy remained in the ownership index");
  assert(globalThis.__dynamicCodeCalls === 0, "scope parsing used eval or Function");
  assert(kit.props === undefined && kit.scope === undefined, "scope introduced props or a public imperative API");
});
</script></body></html>`)
	return page.String()
}
