package javascript

import (
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBrowserPrivateMorphRetainContract(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping data-kit-retain Morph browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/retain-morph.html" {
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(retainMorphFixture()))
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

	runVanillaBrowser(t, browser, server.URL+"/retain-morph.html")
}

func retainMorphFixture() string {
	var page strings.Builder
	page.WriteString(`<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Retain Morph contract</title></head>
<body data-stage="initial">
  <div id="retain-parent-a">
    <section data-test-host="primary" data-kit-retain="primary" data-kit-component="retain-one@1.0.0" data-kit-as="$one" data-server="old-primary">
      <button id="retain-add" type="button" data-kit-click="increment()">Increment old</button>
      <output id="retain-count" data-kit-text="count">server-old</output>
    </section>
    <section data-test-host="changed" data-kit-retain="change-old" data-kit-component="retain-one@1.0.0" data-kit-as="$changed"><span>changed old</span></section>
  </div>
  <div id="retain-parent-b">
    <section data-test-host="secondary" data-kit-retain="secondary" data-kit-component="retain-two@1.0.0" data-kit-as="$two"><span>secondary old</span></section>
	<section data-test-host="versioned" data-kit-retain="versioned" data-kit-component="retain-one@1.0.0" data-kit-as="$versioned"><span>version old</span></section>
    <section data-test-host="removed" data-kit-retain="removed" data-kit-component="retain-one@1.0.0" data-kit-as="$removed"><span>remove old</span></section>
  </div>
`)
	for _, name := range FragmentNames() {
		if name == "src/boot.js" || name == "src/morph.js" || name == "src/drive.js" {
			continue
		}
		page.WriteString(`<script src="/` + html.EscapeString(name) + `"></script>` + "\n")
	}
	page.WriteString(`<script>
  globalThis.__retainMorphState = { init: 0, cleanup: 0 };
  globalThis.__retainMorphCore = document[Symbol.for("kitjs:assembly")];
  function retainDefinition() {
    return {
      count: 0,
      increment: function () { this.count++; },
      init: function () {
        globalThis.__retainMorphState.init++;
        return function () { globalThis.__retainMorphState.cleanup++; };
      }
    };
  }
  __retainMorphCore.component("retain-one", retainDefinition());
  __retainMorphCore.component("retain-two", retainDefinition());
</script>
<script src="/src/morph.js"></script>
<script>
  globalThis.__privateRetainMorph = __retainMorphCore.morph;
  __retainMorphCore.phase = "events";
  __retainMorphCore.installComponentGraph({
    id: "0000000000000000000000000000000000000000000000000000000000000000",
    profile: "kit",
    services: {},
    components: { "retain-one": "1.0.0", "retain-two": "1.0.0" },
    actions: {},
    grants: { "retain-one": {}, "retain-two": {} }
  });
</script>
<script src="/src/boot.js"></script>
<script>
`)
	page.WriteString(browserHarness)
	page.WriteString(`
__runStandaloneKitTest(async function () {
  var assert = __kitTestAssert;
  var waitFor = __kitTestWaitFor;
  var nextTurn = __kitTestNextTurn;
  var core = globalThis.__retainMorphCore;
  var morph = globalThis.__privateRetainMorph;
  var state = globalThis.__retainMorphState;

  function parsedBody(markup) {
    return new DOMParser().parseFromString("<!doctype html><html><body>" + markup + "</body></html>", "text/html").body;
  }
  function host(key) { return document.querySelector('[data-kit-retain="' + key + '"]'); }
  function scope(element) {
    var record = core.scopes.get(element);
    return record && record.scope;
  }

  assert(typeof morph === "function" && kit.morph === undefined && kit.retain === undefined,
    "retain exposed Morph or a new public API");
  assert(Object.keys(kit).join(",") === "version,component", "retain expanded public KitJS keys");
  await waitFor(function () { return state.init === 5 && document.getElementById("retain-count").textContent === "0"; },
    "initial retained components did not mount exactly once");

  var primary = host("primary");
  var secondary = host("secondary");
  var changed = host("change-old");
  var versioned = host("versioned");
  var removed = host("removed");
  var primaryScope = scope(primary);
  var secondaryScope = scope(secondary);
  var changedScope = scope(changed);
  var versionedScope = scope(versioned);
  var removedScope = scope(removed);

  document.getElementById("retain-add").click();
  await waitFor(function () { return document.getElementById("retain-count").textContent === "1"; },
    "retained component state did not become dirty before Morph");

  var moved = parsedBody(
    '<div id="retain-parent-a">' +
      '<section data-test-host="removed" data-kit-retain="removed" data-kit-component="retain-one@1.0.0" data-kit-as="$removed"><span>remove moved</span></section>' +
      '<section data-test-host="versioned" data-kit-retain="versioned" data-kit-component="retain-one" data-kit-version="1.0.0" data-kit-as="$versioned"><span>version moved</span></section>' +
    '</div>' +
    '<div id="retain-parent-b" data-layout="new">' +
      '<section data-test-host="secondary" data-kit-retain="secondary" data-kit-component="retain-two@1.0.0" data-kit-as="$two" data-server="new-secondary"><span>secondary moved</span></section>' +
      '<section data-test-host="changed" data-kit-retain="change-old" data-kit-component="retain-one@1.0.0" data-kit-as="$changed"><span>changed moved</span></section>' +
      '<section data-test-host="primary" data-kit-retain="primary" data-kit-component="retain-one@1.0.0" data-kit-as="$one" data-server="new-primary">' +
        '<button id="retain-add" type="button" data-kit-click="increment()">Increment moved</button>' +
        '<output id="retain-count" data-kit-text="count">server-next</output><em id="retain-new-child">new child</em>' +
      '</section>' +
    '</div>');
  moved.setAttribute("data-stage", "moved");
  assert(morph(document.body, moved) === document.body, "retain Morph replaced the body root");
  await waitFor(function () { return document.getElementById("retain-count").textContent === "1"; },
    "retained state was not rendered after a parent move");

  assert(host("primary") === primary && host("secondary") === secondary && host("change-old") === changed &&
    host("versioned") === versioned && host("removed") === removed,
    "sibling reorder or parent move replaced a retained DOM host");
  assert(scope(primary) === primaryScope && scope(secondary) === secondaryScope && scope(changed) === changedScope &&
    scope(versioned) === versionedScope && scope(removed) === removedScope,
    "sibling reorder or parent move replaced a retained component scope");
  assert(primary.parentElement.id === "retain-parent-b" && removed.parentElement.id === "retain-parent-a",
    "retain did not move hosts across parents");
  var order = Array.from(document.getElementById("retain-parent-b").children).map(function (element) {
    return element.getAttribute("data-kit-retain");
  }).join(",");
  assert(order === "secondary,change-old,primary", "retain did not apply incoming sibling order: " + order);
  assert(primary.getAttribute("data-server") === "new-primary" &&
    document.getElementById("retain-add").textContent === "Increment moved" &&
    document.getElementById("retain-new-child").textContent === "new child" &&
    secondary.textContent.trim() === "secondary moved",
    "retain froze attributes or children instead of morphing them");
  assert(state.init === 5 && state.cleanup === 0, "compatible retain initialized or cleaned a component");

  var recordedPrimaryScope = primaryScope;
  primary.setAttribute("data-kit-component", "retain-two@1.0.0");
  primary.removeAttribute("data-kit-version");
  primary.setAttribute("data-kit-as", "$mutatedOne");
  var matchingMutation = parsedBody(
    '<section data-test-host="primary-mutated" data-kit-retain="primary" data-kit-component="retain-two@1.0.0" data-kit-as="$mutatedOne">' +
      '<output data-kit-text="count">mutated fresh</output>' +
    '</section>').firstElementChild;
  var mutatedDOMHost = primary;
  primary = morph(primary, matchingMutation);
  await waitFor(function () { return state.init === 6 && state.cleanup === 1; },
    "mounted identity mutation did not replace and dispose its stale scope");
  assert(primary !== mutatedDOMHost && !mutatedDOMHost.isConnected && scope(mutatedDOMHost) === undefined,
    "Morph trusted mutated DOM metadata over the mounted component identity");
  primaryScope = scope(primary);
  assert(primaryScope && primaryScope !== recordedPrimaryScope,
    "mounted component/version/as mutation reused its recorded scope");

  var incompatible = parsedBody(
    '<div id="retain-parent-a">' +
      '<section data-test-host="versioned-next" data-kit-retain="versioned" data-kit-component="retain-one@1.0.0" data-kit-as="$versionedNext"><span>version next</span></section>' +
    '</div>' +
    '<div id="retain-parent-b">' +
      '<section data-test-host="changed-next" data-kit-retain="change-next" data-kit-component="retain-one@1.0.0" data-kit-as="$changed"><span>changed next</span></section>' +
      '<section data-test-host="primary-next" data-kit-retain="primary" data-kit-component="retain-one@1.0.0" data-kit-as="$one"><output data-kit-text="count">primary fresh</output></section>' +
      '<section data-test-host="secondary-next" data-kit-retain="secondary" data-kit-component="retain-two@1.0.0" data-kit-as="$twoNext"><output data-kit-text="count">secondary fresh</output></section>' +
    '</div>');
  assert(morph(document.body, incompatible) === document.body, "incompatible retain Morph replaced the body root");
  await waitFor(function () { return state.init === 10 && state.cleanup === 6; },
    "incompatible, changed, or missing retain hosts did not complete lifecycle exactly once");

  assert(host("primary") !== primary && !primary.isConnected && scope(primary) === undefined,
    "component identity mismatch retained stale DOM or scope");
  assert(host("secondary") !== secondary && !secondary.isConnected && scope(secondary) === undefined,
    "alias mismatch retained stale DOM or scope");
  assert(host("versioned") !== versioned && !versioned.isConnected && scope(versioned) === undefined,
    "component metadata mismatch retained stale DOM or scope");
  assert(host("change-old") === null && host("change-next") !== changed && !changed.isConnected,
    "changed retain key reused its prior instance");
  assert(host("removed") === null && !removed.isConnected && scope(removed) === undefined,
    "missing incoming retain key did not remove and clean its component");

  var resetCalls = 0;
  var originalReset = core.resetStructures;
  core.resetStructures = function () {
    resetCalls++;
    return originalReset.apply(this, arguments);
  };
  var valid = '<section data-kit-retain="safe" data-kit-component="retain-one" data-kit-version="1.0.0"></section>';
  var invalidFixtures = [
    { label: "empty", current: '<div>' + valid + '</div>', incoming: '<div><section data-kit-retain="" data-kit-component="retain-one" data-kit-version="1.0.0"></section></div>' },
    { label: "whitespace", current: '<div>' + valid + '</div>', incoming: '<div><section data-kit-retain=" safe" data-kit-component="retain-one" data-kit-version="1.0.0"></section></div>' },
    { label: "orphan", current: '<div>' + valid + '</div>', incoming: '<div><aside data-kit-retain="orphan"></aside></div>' },
    { label: "duplicate incoming", current: '<div>' + valid + '</div>', incoming: '<div><section data-kit-retain="same" data-kit-component="retain-one" data-kit-version="1.0.0"></section><section data-kit-retain="same" data-kit-component="retain-two" data-kit-version="1.0.0"></section></div>' },
    { label: "duplicate current", current: '<div><section data-kit-retain="same" data-kit-component="retain-one" data-kit-version="1.0.0"></section><section data-kit-retain="same" data-kit-component="retain-two" data-kit-version="1.0.0"></section></div>', incoming: '<div>' + valid + '</div>' },
    { label: "template", current: '<div>' + valid + '</div>', incoming: '<div><template><section data-kit-retain="inside" data-kit-component="retain-one" data-kit-version="1.0.0"></section></template></div>' },
    { label: "structural", current: '<div>' + valid + '</div>', incoming: '<div><template data-kit-if="true"><section data-kit-retain="inside" data-kit-component="retain-one" data-kit-version="1.0.0"></section></template></div>' },
    { label: "nested", current: '<div>' + valid + '</div>', incoming: '<div><section data-kit-retain="outer" data-kit-component="retain-one" data-kit-version="1.0.0"><section data-kit-retain="inner" data-kit-component="retain-two" data-kit-version="1.0.0"></section></section></div>' }
  ];
  invalidFixtures.forEach(function (fixture) {
    var current = parsedBody(fixture.current).firstElementChild;
    var incoming = parsedBody(fixture.incoming).firstElementChild;
    var before = current.outerHTML;
    var resetsBefore = resetCalls;
    var rejected = false;
    try { morph(current, incoming); }
    catch (error) { rejected = error instanceof TypeError && String(error.message).indexOf("data-kit-retain") >= 0; }
    assert(rejected, fixture.label + " retain metadata was accepted");
    assert(current.outerHTML === before, fixture.label + " retain failure partially mutated current DOM");
    assert(resetCalls === resetsBefore, fixture.label + " retain failure reset structures before validation");
  });
  core.resetStructures = originalReset;
  await nextTurn();
  assert(Object.keys(kit).join(",") === "version,component" && kit.retain === undefined,
    "retain validation expanded the public API");
});
</script></body></html>`)
	return page.String()
}

func TestBrowserInitialRetainMetadataFailsClosedPerBoundary(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping initial data-kit-retain metadata browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	names := []string{
		"retain-boot-valid",
		"retain-boot-owner",
		"retain-boot-empty",
		"retain-boot-token",
		"retain-boot-duplicate-a",
		"retain-boot-duplicate-b",
		"retain-boot-nested-a",
		"retain-boot-nested-b",
		"retain-boot-template",
		"retain-boot-structural",
	}
	components := make([]ComponentVersion, 0, len(names))
	for _, name := range names {
		components = append(components, ComponentVersion{Name: name, Version: "1.0.0"})
	}
	artifact, err := Build(BuildOptions{
		Profile:    ProfileKit,
		Components: components,
		Scripts: []Script{{Name: "retain-boot", Source: []byte(`;(function (kit) {
  "use strict";
  globalThis.__retainBootInit = Object.create(null);
  [
    "retain-boot-valid", "retain-boot-owner", "retain-boot-empty", "retain-boot-token",
    "retain-boot-duplicate-a", "retain-boot-duplicate-b", "retain-boot-nested-a",
    "retain-boot-nested-b", "retain-boot-template", "retain-boot-structural"
  ].forEach(function (name) {
    globalThis.__retainBootInit[name] = 0;
    kit.component(name, {
      label: "client-" + name,
      visible: true,
      init: function () { globalThis.__retainBootInit[name]++; }
    });
  });
})(kit);
`)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	assetPath := "/assets/" + artifact.Name()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case assetPath:
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(artifact.Bytes())
		case "/retain-boot.html":
			writeRetainHTML(response, fmt.Sprintf(retainBootDocument, assetPath, browserHarness, retainBootAssertions))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	runVanillaBrowser(t, browser, server.URL+"/retain-boot.html")
}

const retainBootDocument = `<!doctype html>
<html lang="en"><head>
  <meta charset="utf-8"><title>Retain initial metadata</title>
  <script>
    globalThis.__retainBootErrors = [];
    (function () {
      var original = console.error;
      console.error = function (error) {
        var message = String(error && error.message || error || "");
        if (message.indexOf("data-kit-retain") >= 0) globalThis.__retainBootErrors.push(message);
        return original.apply(this, arguments);
      };
    })();
  </script>
  <script src=%q></script>
</head><body>
  <section data-test-boot="valid" data-kit-retain="valid" data-kit-component="retain-boot-valid" data-kit-version="1.0.0">
    <output id="retain-boot-valid-output" data-kit-text="label">server-valid</output>
  </section>
  <section data-test-boot="empty" data-kit-retain="" data-kit-component="retain-boot-empty" data-kit-version="1.0.0">
    <output id="retain-boot-empty-output" data-kit-text="label">server-empty</output>
  </section>
  <section data-test-boot="token" data-kit-retain="0invalid" data-kit-component="retain-boot-token" data-kit-version="1.0.0">
    <output id="retain-boot-token-output" data-kit-text="label">server-token</output>
  </section>
  <section data-test-boot="duplicate-a" data-kit-retain="duplicate" data-kit-component="retain-boot-duplicate-a" data-kit-version="1.0.0">
    <output id="retain-boot-duplicate-a-output" data-kit-text="label">server-duplicate-a</output>
  </section>
  <section data-test-boot="duplicate-b" data-kit-retain="duplicate" data-kit-component="retain-boot-duplicate-b" data-kit-version="1.0.0">
    <output id="retain-boot-duplicate-b-output" data-kit-text="label">server-duplicate-b</output>
  </section>
  <section data-test-boot="nested-a" data-kit-retain="nested-a" data-kit-component="retain-boot-nested-a" data-kit-version="1.0.0">
    <output id="retain-boot-nested-a-output" data-kit-text="label">server-nested-a</output>
    <section data-test-boot="nested-b" data-kit-retain="nested-b" data-kit-component="retain-boot-nested-b" data-kit-version="1.0.0">
      <output id="retain-boot-nested-b-output" data-kit-text="label">server-nested-b</output>
    </section>
  </section>
  <aside data-test-boot="orphan" data-kit-retain="orphan">orphan remains plain</aside>
  <template id="retain-boot-template">
    <section data-kit-retain="template" data-kit-component="retain-boot-template" data-kit-version="1.0.0">
      <output data-kit-text="label">server-template</output>
    </section>
  </template>
  <section data-test-boot="owner" data-kit-component="retain-boot-owner" data-kit-version="1.0.0">
    <output id="retain-boot-owner-output" data-kit-text="label">server-owner</output>
    <template id="retain-boot-structural-template" data-kit-if="visible">
      <section data-test-boot="structural" data-kit-retain="structural" data-kit-component="retain-boot-structural" data-kit-version="1.0.0">
        <output data-kit-text="label">server-structural</output>
      </section>
    </template>
  </section>
  <script>
%s
%s
  </script>
</body></html>`

const retainBootAssertions = `__runStandaloneKitTest(async function () {
  var assert = __kitTestAssert;
  var waitFor = __kitTestWaitFor;
  var nextTurn = __kitTestNextTurn;
  var initialized = globalThis.__retainBootInit;
  await waitFor(function () {
    return initialized["retain-boot-valid"] === 1 && initialized["retain-boot-owner"] === 1 &&
      document.getElementById("retain-boot-valid-output").textContent === "client-retain-boot-valid" &&
      document.getElementById("retain-boot-owner-output").textContent === "client-retain-boot-owner";
  }, "valid components beside invalid retain metadata did not mount");
  await nextTurn();

  [
    "retain-boot-empty", "retain-boot-token", "retain-boot-duplicate-a", "retain-boot-duplicate-b",
    "retain-boot-nested-a", "retain-boot-nested-b", "retain-boot-template", "retain-boot-structural"
  ].forEach(function (name) {
    assert(initialized[name] === 0, name + " initialized despite invalid retain metadata");
  });
  [
    ["retain-boot-empty-output", "server-empty"],
    ["retain-boot-token-output", "server-token"],
    ["retain-boot-duplicate-a-output", "server-duplicate-a"],
    ["retain-boot-duplicate-b-output", "server-duplicate-b"],
    ["retain-boot-nested-a-output", "server-nested-a"],
    ["retain-boot-nested-b-output", "server-nested-b"]
  ].forEach(function (entry) {
    assert(document.getElementById(entry[0]).textContent.trim() === entry[1],
      entry[0] + " activated directives on an invalid retained host");
  });
  assert(!document.querySelector('[data-test-boot="structural"]'),
    "invalid retained component materialized from a structural template");
  assert(globalThis.__retainBootErrors.length >= 6,
    "initial retain validation did not report invalid/orphan/duplicate/template/nested metadata");
  assert(!document.querySelector("[data-kit-app],[data-kit-hydrate]"),
    "initial retain validation required an activation marker");
  assert(Object.keys(kit).join(",") === "version,component" && kit.retain === undefined,
    "initial retain validation expanded the public API");
});`

func TestBrowserDriveRetainPreflightBeforeDocumentMutation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping data-kit-retain Drive preflight browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	artifact := buildRetainDriveArtifact(t)
	assetPath := "/assets/" + artifact.Name()
	assetIntegrity := driveScriptIntegrity(artifact.Bytes())
	contractSource := []byte(browserHarness + "\n" + retainDriveAssertions)
	contractIntegrity := driveScriptIntegrity(contractSource)
	invalidBodies := map[string]string{
		"/retain-empty":      `<section data-kit-retain="" data-kit-component="retain-one" data-kit-version="1.0.0"></section>`,
		"/retain-whitespace": `<section data-kit-retain=" one" data-kit-component="retain-one" data-kit-version="1.0.0"></section>`,
		"/retain-invalid":    `<section data-kit-retain="0one" data-kit-component="retain-one" data-kit-version="1.0.0"></section>`,
		"/retain-orphan":     `<aside data-kit-retain="orphan">orphan poison</aside>`,
		"/retain-duplicate": `<section data-kit-retain="same" data-kit-component="retain-one" data-kit-version="1.0.0"></section>
<section data-kit-retain="same" data-kit-component="retain-two" data-kit-version="1.0.0"></section>`,
		"/retain-template":   `<template><section data-kit-retain="inside" data-kit-component="retain-one" data-kit-version="1.0.0"></section></template>`,
		"/retain-structural": `<template data-kit-if="true"><section data-kit-retain="inside" data-kit-component="retain-one" data-kit-version="1.0.0"></section></template>`,
		"/retain-nested": `<section data-kit-retain="outer" data-kit-component="retain-one" data-kit-version="1.0.0">
  <section data-kit-retain="inner" data-kit-component="retain-two" data-kit-version="1.0.0"></section>
</section>`,
		"/retain-version": `<section data-kit-retain="one" data-kit-component="retain-one" data-kit-version="2.0.0"></section>`,
	}
	validCurrentBody := retainDriveHosts("Valid current poison", false)
	identityBody := `<main id="retain-route">Identity committed</main>
<section data-test-host="one-next" data-kit-retain="one" data-kit-component="retain-two" data-kit-version="1.0.0" data-kit-as="$one">
  <output id="retain-one-next-count" data-kit-text="count">fresh one</output>
</section>
<section data-test-host="two-next" data-kit-retain="two" data-kit-component="retain-two" data-kit-version="1.0.0" data-kit-as="$twoNext">
  <output id="retain-two-next-count" data-kit-text="count">fresh two</output>
</section>`

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case assetPath:
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(artifact.Bytes())
		case "/retain-drive-contract.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(contractSource)
		case "/retain-drive.html":
			writeRetainHTML(response, retainDriveInitialDocument(
				assetPath, assetIntegrity, contractIntegrity))
		case "/retain-valid-current":
			if request.Header.Get("X-KitJS-Drive") == "1" {
				writeRetainHTML(response, retainDriveIncomingDocument(
					assetPath, assetIntegrity, contractIntegrity, "Valid current poison", validCurrentBody))
				return
			}
			writeRetainFallback(response, "current")
		case "/retain-identity":
			writeRetainHTML(response, retainDriveIncomingDocument(
				assetPath, assetIntegrity, contractIntegrity, "Identity committed", identityBody))
		default:
			body, exists := invalidBodies[request.URL.Path]
			if !exists {
				http.NotFound(response, request)
				return
			}
			if request.Header.Get("X-KitJS-Drive") == "1" {
				writeRetainHTML(response, retainDriveIncomingDocument(
					assetPath, assetIntegrity, contractIntegrity, "Retain poison", body))
				return
			}
			writeRetainFallback(response, strings.TrimPrefix(request.URL.Path, "/retain-"))
		}
	}))
	defer server.Close()

	runVanillaBrowser(t, browser, server.URL+"/retain-drive.html")
}

func buildRetainDriveArtifact(t *testing.T) Artifact {
	t.Helper()
	artifact, err := Build(BuildOptions{
		Profile: ProfileHydrate,
		Components: []ComponentVersion{
			{Name: "retain-one", Version: "1.0.0"},
			{Name: "retain-two", Version: "1.0.0"},
		},
		Scripts: []Script{{Name: "retain-drive-contract", Source: []byte(`;(function (kit) {
  "use strict";
  globalThis.__retainDriveState = { init: 0, cleanup: 0 };
  function definition() {
    return {
      count: 0,
      increment: function () { this.count++; },
      init: function () {
        globalThis.__retainDriveState.init++;
        return function () { globalThis.__retainDriveState.cleanup++; };
      }
    };
  }
  kit.component("retain-one", definition());
  kit.component("retain-two", definition());
})(kit);
`)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return artifact
}

func retainDriveInitialDocument(assetPath, assetIntegrity, contractIntegrity string) string {
	return fmt.Sprintf(`<!doctype html>
<html lang="en"><head>
  <meta charset="utf-8">
  <meta name="description" content="Retain initial">
  <title>Retain initial</title>
  <script defer src=%q integrity=%q crossorigin="anonymous"></script>
  <script defer src="/retain-drive-contract.js" integrity=%q crossorigin="anonymous"></script>
</head><body>
  %s
  <nav aria-label="Retain rejection routes">
    <a id="retain-empty-link" href="/retain-empty">empty</a>
    <a id="retain-whitespace-link" href="/retain-whitespace">whitespace</a>
    <a id="retain-invalid-link" href="/retain-invalid">invalid</a>
    <a id="retain-orphan-link" href="/retain-orphan">orphan</a>
    <a id="retain-duplicate-link" href="/retain-duplicate">duplicate</a>
    <a id="retain-template-link" href="/retain-template">template</a>
    <a id="retain-structural-link" href="/retain-structural">structural</a>
    <a id="retain-nested-link" href="/retain-nested">nested</a>
    <a id="retain-version-link" href="/retain-version">version</a>
    <a id="retain-current-link" href="/retain-valid-current">current duplicate</a>
    <a id="retain-identity-link" href="/retain-identity">identity</a>
  </nav>
</body></html>`, assetPath, assetIntegrity, contractIntegrity, retainDriveHosts("Initial", true))
}

func retainDriveHosts(route string, controls bool) string {
	button := ""
	if controls {
		button = `<button id="retain-drive-add" type="button" data-kit-click="increment()">increment</button>`
	}
	return fmt.Sprintf(`<main id="retain-route">%s</main>
<section data-test-host="one" data-kit-retain="one" data-kit-component="retain-one" data-kit-version="1.0.0" data-kit-as="$one">
  %s
  <output id="retain-drive-count" data-kit-text="count">server one</output>
</section>
<section data-test-host="two" data-kit-retain="two" data-kit-component="retain-two" data-kit-version="1.0.0" data-kit-as="$two">
  <output data-kit-text="count">server two</output>
</section>`, route, button)
}

func retainDriveIncomingDocument(assetPath, assetIntegrity, contractIntegrity, title, body string) string {
	return fmt.Sprintf(`<!doctype html>
<html lang="poisoned"><head>
  <meta charset="utf-8">
  <meta name="description" content="Poisoned retain metadata">
  <title>%s</title>
  <script defer src=%q integrity=%q crossorigin="anonymous"></script>
  <script defer src="/retain-drive-contract.js" integrity=%q crossorigin="anonymous"></script>
</head><body>
  %s
</body></html>`, html.EscapeString(title), assetPath, assetIntegrity, contractIntegrity, body)
}

func writeRetainHTML(response http.ResponseWriter, source string) {
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = response.Write([]byte(source))
}

func writeRetainFallback(response http.ResponseWriter, name string) {
	response.Header().Set("Set-Cookie", "kit_retain_"+name+"=1; Path=/; SameSite=Lax")
	response.WriteHeader(http.StatusNoContent)
}

const retainDriveAssertions = `__runStandaloneKitTest(async function () {
  var assert = __kitTestAssert;
  var waitFor = __kitTestWaitFor;
  var state = globalThis.__retainDriveState;

  assert(!document.querySelector("[data-kit-app],[data-kit-hydrate]"),
    "retain Drive required an authored activation marker");
  assert(Object.keys(kit).join(",") === "version,component" && kit.retain === undefined,
    "retain expanded the public API");
  await waitFor(function () { return state.init === 2 && document.getElementById("retain-drive-count").textContent === "0"; },
    "retain Drive fixture did not initialize");
  document.getElementById("retain-drive-add").click();
  await waitFor(function () { return document.getElementById("retain-drive-count").textContent === "1"; },
    "retain Drive fixture did not preserve mutable state");

  var root = document.documentElement;
  var body = document.body;
  var description = document.querySelector('meta[name="description"]');
  var one = document.querySelector('[data-kit-retain="one"]');
  var two = document.querySelector('[data-kit-retain="two"]');
  globalThis.__retainIncomingScript = 0;

  async function reject(label, cookie) {
    var before = {
      path: location.pathname,
      title: document.title,
      lang: root.lang,
      description: description.content,
      head: document.head.innerHTML,
      body: body.innerHTML,
      historyLength: history.length,
      historyState: JSON.stringify(history.state)
    };
    document.getElementById("retain-" + label + "-link").click();
    await waitFor(function () { return document.cookie.indexOf("kit_retain_" + cookie + "=1") >= 0; },
      label + " retain metadata did not hard-fallback");
    assert(document.documentElement === root && document.body === body,
      label + " retain failure replaced a document root");
    assert(location.pathname === before.path && document.title === before.title && root.lang === before.lang,
      label + " retain failure committed URL, title, or html attributes");
    assert(document.querySelector('meta[name="description"]') === description &&
      description.content === before.description && document.head.innerHTML === before.head,
      label + " retain failure mutated head metadata");
    assert(body.innerHTML === before.body, label + " retain failure partially mutated body");
    assert(history.length === before.historyLength && JSON.stringify(history.state) === before.historyState,
      label + " retain failure mutated history");
    assert(document.querySelector('[data-test-host="one"]') === one &&
      document.querySelector('[data-test-host="two"]') === two &&
      document.getElementById("retain-drive-count").textContent === "1",
      label + " retain failure replaced or reset component state");
    assert(state.init === 2 && state.cleanup === 0 && globalThis.__retainIncomingScript === 0,
      label + " retain failure activated incoming content or lifecycle");
  }

  var invalid = ["empty", "whitespace", "invalid", "orphan", "duplicate", "template", "structural", "nested", "version"];
  for (var index = 0; index < invalid.length; index++) await reject(invalid[index], invalid[index]);

  async function rejectCurrent(label, apply, restore) {
    document.cookie = "kit_retain_current=; Max-Age=0; Path=/; SameSite=Lax";
    apply();
    await reject("current", "current");
    restore();
    assert(state.init === 2 && state.cleanup === 0, label + " current failure changed lifecycle");
  }
  await rejectCurrent("empty", function () { two.setAttribute("data-kit-retain", ""); },
    function () { two.setAttribute("data-kit-retain", "two"); });
  await rejectCurrent("whitespace", function () { two.setAttribute("data-kit-retain", " two"); },
    function () { two.setAttribute("data-kit-retain", "two"); });
  await rejectCurrent("invalid", function () { two.setAttribute("data-kit-retain", "0two"); },
    function () { two.setAttribute("data-kit-retain", "two"); });
  await rejectCurrent("duplicate", function () { two.setAttribute("data-kit-retain", "one"); },
    function () { two.setAttribute("data-kit-retain", "two"); });

  var currentNav = document.querySelector('nav[aria-label="Retain rejection routes"]');
  await rejectCurrent("orphan", function () { currentNav.setAttribute("data-kit-retain", "orphan"); },
    function () { currentNav.removeAttribute("data-kit-retain"); });

  async function rejectInsertedCurrent(label, markup) {
    var container = document.createElement("div");
    container.setAttribute("data-current-retain-fixture", label);
    container.innerHTML = markup;
    await rejectCurrent(label, function () { body.appendChild(container); }, function () { container.remove(); });
  }
  await rejectInsertedCurrent("template",
    '<template><section data-kit-retain="inside" data-kit-component="retain-one" data-kit-version="1.0.0"></section></template>');
  await rejectInsertedCurrent("structural",
    '<template data-kit-if="true"><section data-kit-retain="inside" data-kit-component="retain-one" data-kit-version="1.0.0"></section></template>');
  var nested = document.createElement("section");
  nested.setAttribute("data-kit-retain", "nested");
  nested.setAttribute("data-kit-component", "retain-two");
  nested.setAttribute("data-kit-version", "1.0.0");
  await rejectCurrent("nested", function () { one.appendChild(nested); }, function () { nested.remove(); });

  document.getElementById("retain-identity-link").click();
  await waitFor(function () {
    return location.pathname === "/retain-identity" && state.init === 4 && state.cleanup === 2;
  }, "compatible Drive document with incompatible component identities did not commit fresh instances");
  assert(document.title === "Identity committed" &&
    document.getElementById("retain-route").textContent.trim() === "Identity committed",
    "identity replacement did not commit the incoming document");
  assert(!one.isConnected && !two.isConnected &&
    document.querySelector('[data-kit-retain="one"]') !== one &&
    document.querySelector('[data-kit-retain="two"]') !== two,
    "component or alias mismatch retained stale hosts");
  assert(document.getElementById("retain-one-next-count").textContent === "0" &&
    document.getElementById("retain-two-next-count").textContent === "0",
    "fresh incompatible instances inherited stale state");
  assert(globalThis.__retainIncomingScript === 0, "Drive executed an incoming script");
  assert(Object.keys(kit).join(",") === "version,component" && kit.retain === undefined,
    "retain added a public API after Drive");
});`

func TestBrowserRemovedRetainHostAndScopeAreReleased(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping data-kit-retain forced-GC contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	artifact, err := Build(BuildOptions{
		Profile:    ProfileHydrate,
		Components: []ComponentVersion{{Name: "retain-gc", Version: "1.0.0"}},
		Scripts: []Script{{Name: "retain-gc", Source: []byte(`;globalThis.__retainGC = {
  init: 0,
  cleanup: 0,
  scope: null
};
kit.component("retain-gc", {
  value: "ready",
  init: function () {
    globalThis.__retainGC.init++;
    globalThis.__retainGC.scope = new WeakRef(this);
    return function () { globalThis.__retainGC.cleanup++; };
  }
});
`)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	assetPath := "/assets/" + artifact.Name()
	assetIntegrity := driveScriptIntegrity(artifact.Bytes())
	contractSource := retainGCExternalContract(t, assetPath)
	contractIntegrity := driveScriptIntegrity(contractSource)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case assetPath:
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(artifact.Bytes())
		case "/retain-gc-contract.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(contractSource)
		case "/retain-gc.html":
			writeRetainHTML(response, retainGCDocument(
				assetPath, assetIntegrity, contractIntegrity, true))
		case "/retain-gc-next":
			writeRetainHTML(response, retainGCDocument(
				assetPath, assetIntegrity, contractIntegrity, false))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	status, output := runRetentionBrowser(t, browser, server.URL+"/retain-gc.html")
	if status == "unsupported" {
		t.Skipf("browser did not make forced collection observable\n%s", boundedVanillaOutput(output))
	}
	if status != "passed" {
		t.Fatalf("removed retain host/scope retention contract did not pass\n%s", boundedVanillaOutput(output))
	}
}

func retainGCDocument(assetPath, assetIntegrity, contractIntegrity string, initial bool) string {
	title := "Retain GC next"
	body := `<main id="retain-gc-route">Retained host removed</main>`
	if initial {
		title = "Retain GC"
		body = `<section id="retain-gc-host" data-kit-retain="gc-host" data-kit-component="retain-gc" data-kit-version="1.0.0">
    <output data-kit-text="value">server</output>
  </section>
  <a id="retain-gc-next" href="/retain-gc-next">remove retained host</a>`
	}
	return fmt.Sprintf(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>%s</title>
<script defer src=%q integrity=%q crossorigin="anonymous"></script>
<script defer src="/retain-gc-contract.js" integrity=%q crossorigin="anonymous"></script></head>
<body>%s</body></html>`, title, assetPath, assetIntegrity, contractIntegrity, body)
}

func retainGCExternalContract(t *testing.T, assetPath string) []byte {
	t.Helper()
	legacy := fmt.Sprintf(retainGCInitialDocument, assetPath)
	start := strings.Index(legacy, "<script>")
	if start < 0 {
		t.Fatal("retain GC legacy fixture omitted its inline browser contract")
	}
	contract := legacy[start+len("<script>"):]
	end := strings.Index(contract, "</script>")
	if end < 0 {
		t.Fatal("retain GC legacy fixture has an unterminated inline browser contract")
	}
	if strings.Contains(contract[end+len("</script>"):], "<script>") {
		t.Fatal("retain GC legacy fixture has more than one inline browser contract")
	}
	return []byte(contract[:end])
}

const retainGCInitialDocument = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>Retain GC</title><script src=%q></script></head>
<body>
  <section id="retain-gc-host" data-kit-retain="gc-host" data-kit-component="retain-gc" data-kit-version="1.0.0">
    <output data-kit-text="value">server</output>
  </section>
  <a id="retain-gc-next" href="/retain-gc-next">remove retained host</a>
  <script>
  (function () {
    "use strict";
    var root = document.documentElement;
    function finish(status, error) {
      root.setAttribute("data-kit-retention-test", status);
      if (error) root.setAttribute("data-kit-retention-error", String(error && error.message || error));
    }
    function fail(message) { throw new Error(message); }
    function waitFor(predicate, message, deadline, done) {
      if (predicate()) { done(); return; }
      if (performance.now() >= deadline) { finish("failed", message); return; }
      setTimeout(function () { waitFor(predicate, message, deadline, done); }, 8);
    }
    function alive(refs) {
      var count = 0;
      refs.forEach(function (ref) { if (ref.deref() !== undefined) count++; });
      return count;
    }
    function controls() {
      var refs = [];
      for (var index = 0; index < 128; index++) refs.push(new WeakRef({ index: index }));
      return refs;
    }
    function collect(refs, controlRefs, pass) {
      var pressure = [];
      for (var index = 0; index < 8; index++) pressure.push(new Array(65536).fill(pass));
      pressure = null;
      globalThis.gc();
      globalThis.gc();
      if (pass < 7) {
        setTimeout(function () { collect(refs, controlRefs, pass + 1); }, 0);
        return;
      }
      var controlAlive = alive(controlRefs);
      if (controlAlive !== 0) {
        finish("unsupported", "forced GC retained " + controlAlive + " control objects");
        return;
      }
      var retained = alive(refs);
      if (retained !== 0) fail("retain runtime kept " + retained + " removed host/scope objects alive");
      finish("passed");
    }
    function run() {
      try {
        if (typeof WeakRef !== "function" || typeof globalThis.gc !== "function") {
          finish("unsupported", "WeakRef or forced gc() is unavailable");
          return;
        }
        if (Object.keys(kit).join(",") !== "version,component" || kit.retain !== undefined) {
          fail("retain expanded the public API");
        }
        waitFor(function () { return globalThis.__retainGC.init === 1; },
          "retained GC component did not initialize", performance.now() + 2000, function () {
          try {
            var host = document.getElementById("retain-gc-host");
            var refs = [new WeakRef(host), globalThis.__retainGC.scope];
            document.getElementById("retain-gc-next").click();
            waitFor(function () {
              return location.pathname === "/retain-gc-next" && globalThis.__retainGC.cleanup === 1;
            }, "removed retained host did not clean exactly once", performance.now() + 2000, function () {
              try {
                if (host.isConnected) fail("removed retained host stayed connected");
                host = null;
                globalThis.__retainGC.scope = null;
                setTimeout(function () { collect(refs, controls(), 0); }, 0);
              } catch (error) { finish("failed", error); }
            });
          } catch (error) { finish("failed", error); }
        });
      } catch (error) { finish("failed", error); }
    }
    window.addEventListener("error", function (event) { finish("failed", event.error || event.message); });
    window.addEventListener("unhandledrejection", function (event) { finish("failed", event.reason); });
    if (document.readyState === "loading") {
      document.addEventListener("DOMContentLoaded", function () { setTimeout(run, 0); }, { once: true });
    } else setTimeout(run, 0);
  })();
  </script>
</body></html>`

const retainGCNextDocument = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>Retain GC next</title><script src=%q></script></head>
<body><main id="retain-gc-route">Retained host removed</main></body></html>`
