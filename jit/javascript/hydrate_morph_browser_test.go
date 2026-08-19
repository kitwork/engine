package javascript

import (
	"html"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBrowserPrivateMorphContract(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping KitJS Morph browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/morph.html" {
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(morphFixture()))
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

	runVanillaBrowser(t, browser, server.URL+"/morph.html")
}

func morphFixture() string {
	var page strings.Builder
	page.WriteString(`<!doctype html><html lang="en"><head><meta charset="utf-8"><title>KitJS Morph contract</title></head>
<body data-route="old">
  <main id="keeper" data-kit-component="keeper" data-kit-as="$keeper">
    <button id="increment" type="button" data-kit-click:once="increment()">Increment once</button>
    <output id="count" data-kit-text="count">server-old</output>
    <div id="morph-style" style="width: 7px; height: 9px; opacity: 0.1 !important"
      data-kit-style="width: count + 'px'; opacity: count > 0 ? 1 : null;"></div>
    <label for="editor">Draft</label>
    <input id="editor" value="server-old">
    <label for="stable-secret">Stable draft</label>
    <input id="stable-secret" name="stable-secret" value="stable-server-old">
    <div id="unkeyed-form-zone">
      <input name="old-secret" value="unkeyed-server-old">
    </div>
    <label for="preference">Preference</label>
    <select id="preference">
      <option value="a">A old</option>
      <option value="b">B old</option>
      <option value="c">C old</option>
    </select>
    <ul id="rows">
      <li id="row-a">A old</li>
      <li id="row-b">B old</li>
    </ul>
  </main>
  <section id="replacement" data-kit-component="replaceable" data-kit-as="$old" data-kit-show="ready">
    <output data-kit-text="count">old replacement</output>
  </section>
  <section id="remove-parent" data-kit-component="remove-parent">
    <section id="remove-child" data-kit-component="remove-child"><span>remove me</span></section>
  </section>
`)
	for _, name := range FragmentNames() {
		if name == "src/boot.js" || name == "src/morph.js" || name == "src/drive.js" {
			continue
		}
		page.WriteString(`<script src="/` + html.EscapeString(name) + `"></script>` + "\n")
	}
	page.WriteString(`<script src="/src/morph.js"></script>
<script>
  globalThis.__morphCore = document[Symbol.for("kitjs:assembly")];
  globalThis.__privateMorph = globalThis.__morphCore.morph;
  // This fixture tests Morph without Drive. Production Hydrate advances Morph
  // to Drive before boot; the standalone boot path remains an events profile.
  globalThis.__morphCore.phase = "events";
</script>
<script src="/src/boot.js"></script>
<script>
  kit.component("keeper", {
    count: 0,
    increment: function () { this.count++; }
  });
  kit.component("replaceable", { ready: true, count: 40 });
  kit.component("remove-parent", { value: "parent" });
  kit.component("remove-child", { value: "child" });
  kit.component("added", { value: "new component" });
</script>
<script>
`)
	page.WriteString(browserHarness)
	page.WriteString(`
__runStandaloneKitTest(async function () {
  var assert = __kitTestAssert;
  var waitFor = __kitTestWaitFor;
  var nextTurn = __kitTestNextTurn;
  var core = globalThis.__morphCore;
  var morph = globalThis.__privateMorph;

  assert(typeof morph === "function", "private Morph hook is missing");
  assert(kit.morph === undefined, "Morph leaked onto the public kit object");
  assert(Object.keys(kit).join(",") === "version,component", "Morph changed the public KitJS contract");
  await waitFor(function () {
    var styled = document.getElementById("morph-style");
    return document.getElementById("count").textContent === "0" &&
      styled.style.width === "0px" && styled.style.opacity === "0.1";
  }, "initial keeper did not render");

  var keeper = document.getElementById("keeper");
  var keeperScope = core.scopes.get(keeper).scope;
  var increment = document.getElementById("increment");
  var editor = document.getElementById("editor");
  var stableSecret = document.getElementById("stable-secret");
  var unkeyedSecret = document.querySelector("#unkeyed-form-zone input");
  var preference = document.getElementById("preference");
  var rowA = document.getElementById("row-a");
  var rowB = document.getElementById("row-b");
  var replacement = document.getElementById("replacement");
  var morphStyle = document.getElementById("morph-style");

  increment.click();
  await waitFor(function () {
    return document.getElementById("count").textContent === "1" &&
      morphStyle.style.width === "1px" && morphStyle.style.opacity === "1";
  }, "pre-morph action did not update state");
  editor.value = "draft in progress";
  editor.focus();
  editor.setSelectionRange(2, 7, "forward");
  stableSecret.value = "stable private draft";
  unkeyedSecret.value = "must never leak across an unkeyed match";
  preference.value = "b";

  var disposal = [];
  var originalDispose = core.disposeComponent;
  core.disposeComponent = function (element) {
    if (element.id) disposal.push(element.id);
    return originalDispose(element);
  };
  var invalidations = 0;
  var globalInvalidations = 0;
  var invalidatedOwners = [];
  var originalInvalidate = core.invalidate;
  core.invalidate = function (owner) {
    invalidations++;
    if (!owner) globalInvalidations++;
    else invalidatedOwners.push(owner);
    return originalInvalidate(owner);
  };
  var renderPasses = 0;
  var originalRender = core.render;
  core.render = function (owners) {
    renderPasses++;
    return originalRender(owners);
  };

  var incoming = new DOMParser().parseFromString(` + "`" + `<!doctype html><html><body data-route="new">
    <main id="keeper" data-kit-component="keeper" data-kit-as="$keeper">
      <button id="increment" type="button" data-kit-click:once="increment()">Increment after morph</button>
      <output id="count" data-kit-text="count">server-next</output>
      <div id="morph-style" style="width: 12px; height: 10px; opacity: 0.25 !important"
        data-kit-style="width: count + 'px'; opacity: count > 1 ? 1 : null;"></div>
      <label for="editor">Draft</label>
      <input id="editor" value="server-next">
      <label for="stable-secret">Stable draft</label>
      <input id="stable-secret" name="stable-secret" value="stable-server-next">
      <div id="unkeyed-form-zone">
        <input name="new-public-field" value="fresh public value">
      </div>
      <label for="preference">Preference</label>
      <select id="preference">
        <option value="b">B next</option>
        <option value="c">C next</option>
        <option value="a">A next</option>
      </select>
      <ul id="rows">
        <li id="row-b">B next</li>
        <li id="row-a">A next</li>
      </ul>
    </main>
    <section id="replacement" data-kit-component="replaceable" data-kit-as="$new" data-kit-show="ready">
      <output data-kit-text="count">new replacement</output>
    </section>
    <section id="added" data-kit-component="added"><output id="added-output" data-kit-text="value">server</output></section>
    <a id="unsafe-link" href="javascript:globalThis.__morphURLRan=true" onclick="globalThis.__morphEventRan=true">unsafe</a>
    <iframe id="unsafe-frame" srcdoc="<script>globalThis.__morphFrameRan=true<\/script>"></iframe>
    <template id="safe-template"><script>globalThis.__morphScriptRan=true<\/script><button onclick="globalThis.__templateEventRan=true">safe</button></template>
    <script>globalThis.__morphScriptRan=true<\/script>
  </body></html>` + "`" + `, "text/html");

  var result = morph(document.body, incoming.body);
  assert(result === document.body, "body morph replaced a compatible body root");
  assert(globalInvalidations === 0, "Morph escaped dirty boundaries with a global invalidation");
  assert(invalidations === 3 && new Set(invalidatedOwners).size === 3,
    "Morph did not target each live boundary exactly once");
  core.invalidate = originalInvalidate;
  core.disposeComponent = originalDispose;

  assert(document.body.getAttribute("data-route") === "new", "body attributes were not patched");
  assert(document.getElementById("keeper") === keeper, "compatible component host identity was not retained");
  assert(core.scopes.get(keeper).scope === keeperScope, "compatible component state was replaced");
  assert(document.getElementById("morph-style") === morphStyle, "style-bound element identity was not retained");
  assert(document.getElementById("replacement") !== replacement, "incompatible component alias retained stale state");
  assert(disposal.indexOf("remove-child") >= 0 && disposal.indexOf("remove-parent") >= 0,
    "removed component subtree was not disposed");
  assert(disposal.indexOf("remove-child") < disposal.indexOf("remove-parent"),
    "component subtree was not disposed deepest-first");
  assert(disposal.indexOf("keeper") < 0, "retained component was disposed");

  assert(document.getElementById("row-a") === rowA && document.getElementById("row-b") === rowB,
    "id-keyed child identity was not retained while reordering");
  assert(rowB.previousElementSibling === null && rowB.textContent === "B next" && rowA.textContent === "A next",
    "id-keyed children were not reordered and patched");
  assert(document.getElementById("editor") === editor, "focused form control identity was not retained");
  assert(editor.value === "draft in progress", "dirty form value was overwritten");
  assert(document.activeElement === editor, "focus was not preserved");
  assert(editor.selectionStart === 2 && editor.selectionEnd === 7, "input selection was not preserved");
  assert(document.getElementById("stable-secret") === stableSecret && stableSecret.value === "stable private draft",
    "dirty form state was not retained for a stable id identity");
  var nextUnkeyed = document.querySelector("#unkeyed-form-zone input");
  assert(nextUnkeyed !== unkeyedSecret && nextUnkeyed.value === "fresh public value" &&
    document.body.textContent.indexOf("must never leak across an unkeyed match") < 0,
    "unkeyed form control identity or dirty state leaked into a different field");
  assert(document.getElementById("preference") === preference && preference.value === "b",
    "select reorder did not preserve the dirty selected value for a stable identity");

  assert(!document.querySelector("script"), "incoming script element survived sanitization");
  assert(!document.getElementById("unsafe-link").hasAttribute("onclick"), "inline event attribute survived sanitization");
  assert(!document.getElementById("unsafe-link").hasAttribute("href"), "unsafe URL survived sanitization");
  var sanitizedFrame = document.getElementById("unsafe-frame");
  assert(!sanitizedFrame || !sanitizedFrame.hasAttribute("srcdoc"), "srcdoc survived sanitization");
  assert(!document.getElementById("safe-template").content.querySelector("script"), "template script survived sanitization");
  assert(!document.getElementById("safe-template").content.querySelector("button").hasAttribute("onclick"),
    "template inline event survived sanitization");
  assert(globalThis.__morphScriptRan === undefined && globalThis.__morphEventRan === undefined &&
    globalThis.__morphFrameRan === undefined && globalThis.__morphURLRan === undefined,
    "sanitized incoming code executed");

  await waitFor(function () {
    return document.getElementById("count").textContent === "1" &&
      document.getElementById("added-output").textContent === "new component" &&
      morphStyle.style.width === "1px" && morphStyle.style.height === "10px" &&
      morphStyle.style.opacity === "0.25" && morphStyle.style.getPropertyPriority("opacity") === "important";
  }, "post-morph boundaries were not re-prepared and rendered");
  assert(renderPasses === 1, "targeted Morph invalidations produced " + renderPasses + " render passes");
  core.render = originalRender;
  increment.click();
  await waitFor(function () {
    return document.getElementById("count").textContent === "2" &&
      morphStyle.style.width === "2px" && morphStyle.style.opacity === "1";
  },
    "retained once-event cache was not re-prepared for the new document");
  increment.click();
  await nextTurn();
  assert(document.getElementById("count").textContent === "2", "once event ran more than once after Morph");

  var partialOwners = [];
  var partialRenders = 0;
  core.invalidate = function (owner) {
    partialOwners.push(owner);
    return originalInvalidate(owner);
  };
  core.render = function (owners) {
    partialRenders++;
    return originalRender(owners);
  };
  var incomingRows = document.createElement("ul");
  incomingRows.id = "rows";
  incomingRows.innerHTML = '<li id="row-a">A partial</li><li id="row-b">B partial</li>';
  var rows = document.getElementById("rows");
  assert(morph(rows, incomingRows) === rows, "given-root Morph replaced a compatible root");
  assert(partialOwners.length === 1 && partialOwners[0] === core.scopes.get(keeper),
    "given-root Morph did not target its enclosing component boundary");
  await waitFor(function () { return partialRenders === 1; }, "given-root Morph did not coalesce one render pass");
  core.invalidate = originalInvalidate;
  core.render = originalRender;
  assert(document.getElementById("row-a") === rowA && document.getElementById("row-b") === rowB &&
    rowA.textContent === "A partial" && rowB.textContent === "B partial",
    "given-root Morph lost id identity or content");

  function performanceFixture(size, currentTag, incomingTag) {
    var currentRoot = document.createElement("div");
    var incomingRoot = document.createElement("div");
    var currentFragment = document.createDocumentFragment();
    var incomingFragment = document.createDocumentFragment();
    for (var fixtureIndex = 0; fixtureIndex < size; fixtureIndex++) {
      currentFragment.appendChild(document.createElement(currentTag(fixtureIndex, size)));
      incomingFragment.appendChild(document.createElement(incomingTag(fixtureIndex, size)));
    }
    currentRoot.appendChild(currentFragment);
    incomingRoot.appendChild(incomingFragment);
    return { current: currentRoot, incoming: incomingRoot, size: size };
  }

  function boundedMorph(label, fixture) {
    var descriptor = Object.getOwnPropertyDescriptor(Node.prototype, "nextSibling");
    var reads = 0;
    var instrumented = !!(descriptor && descriptor.configurable && typeof descriptor.get === "function");
    if (instrumented) {
      Object.defineProperty(Node.prototype, "nextSibling", {
        configurable: true,
        enumerable: descriptor.enumerable,
        get: function () {
          reads++;
          return descriptor.get.call(this);
        }
      });
    }
    var startedAt = performance.now();
    try { morph(fixture.current, fixture.incoming); }
    finally {
      if (instrumented) Object.defineProperty(Node.prototype, "nextSibling", descriptor);
    }
    var elapsed = performance.now() - startedAt;
    assert(elapsed < 4000, label + " exceeded the generous practical Morph budget: " + elapsed + "ms");
    if (instrumented) {
      assert(reads < fixture.size * 80,
        label + " performed quadratic sibling scanning (" + reads + " nextSibling reads)");
    }
    assert(fixture.current.childNodes.length === fixture.size, label + " produced the wrong child count");
  }

  boundedMorph("all-mismatch unkeyed fixture", performanceFixture(
    1200,
    function () { return "x-current-node"; },
    function () { return "x-incoming-node"; }
  ));
  boundedMorph("reverse-order unkeyed fixture", performanceFixture(
    1200,
    function (index) { return "x-order-" + index; },
    function (index, size) { return "x-order-" + (size - index - 1); }
  ));
});
</script></body></html>`)
	return page.String()
}
