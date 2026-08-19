package javascript

import (
	"html"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBrowserComponentLifecycleContext(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping component lifecycle context browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/component-context.html" {
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(componentContextFixture()))
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

	runVanillaBrowser(t, browser, server.URL+"/component-context.html")
}

func componentContextFixture() string {
	var page strings.Builder
	page.WriteString(`<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Component context</title></head>
<body>
  <section id="owner" data-kit-component="context-owner">
    <button id="owned-button" class="owned" type="button">Owned</button>
    <output id="owner-value" data-kit-text="value">server</output>
    <div data-kit-ignore><button id="ignored-button" class="owned" type="button">Ignored</button></div>
    <section data-kit-scope="count: 1"><button id="nested-scope-button" class="owned" type="button">Nested scope</button></section>
    <section id="child" data-kit-component="context-child">
      <button id="nested-button" class="owned" type="button">Nested</button>
    </section>
  </section>
  <section id="retained" data-kit-retain="context-retained" data-kit-component="context-retained">
    <span class="retained-owned">old</span>
  </section>
  <section id="remove-before-render" data-kit-component="context-remove-before-render"></section>
`)
	for _, name := range FragmentNames() {
		if name == "src/boot.js" || name == "src/morph.js" || name == "src/drive.js" {
			continue
		}
		page.WriteString(`<script src="/` + html.EscapeString(name) + `"></script>` + "\n")
	}
	page.WriteString(`<script src="/src/morph.js"></script>
<script>
  globalThis.__componentContext = {
    contexts: {}, ownerAfter: 0, ownerClicks: 0, ownerWindows: 0, captureCalls: 0, abortCalls: 0,
    cleanupOrder: [], cleanupThis: 0, legacyCleanup: 0,
    retainedAfter: 0, retainedInit: 0, removedAfter: 0, childInit: 0
  };
  var core = document[Symbol.for("kitjs:assembly")];
  var morph = core.morph;
  core.phase = "events";
  core.component("context-child", { init: function () { globalThis.__componentContext.childInit++; } });
  core.component("context-owner", {
    value: "ready",
    init: function (context) {
      var scope = this;
      var state = globalThis.__componentContext;
      state.contexts.owner = context;
      state.contextFrozen = Object.isFrozen(context) && Object.isFrozen(context.owned) &&
        Object.isFrozen(context.listen) && Object.isFrozen(context.cleanup) && Object.isFrozen(context.afterRender);
      state.contextKeys = Object.keys(context).join(",");
      state.scopeLeak = this.host !== undefined || this.owned !== undefined || this.afterRender !== undefined;
      state.initialOwned = context.owned(".owned").map(function (element) { return element.id; }).join(",");
      state.ownedFrozen = Object.isFrozen(context.owned(".owned"));
      context.listen(context.owned("#owned-button")[0], "click", function () { state.ownerClicks++; });
      context.listen(context.host.ownerDocument.defaultView, "context-window", function () { state.ownerWindows++; });
      var mutableOptions = { capture: true };
      context.listen(context.host.ownerDocument.defaultView, "context-capture", function () {
        state.captureCalls++;
      }, mutableOptions);
      mutableOptions.capture = false;
      var controller = new AbortController();
      context.listen(context.host.ownerDocument.defaultView, "context-abort", function () {
        state.abortCalls++;
      }, { signal: controller.signal });
      state.abortListener = function () { controller.abort(); };
      context.cleanup(function () { state.cleanupOrder.push("first"); if (this === scope) state.cleanupThis++; });
      context.cleanup(function () { state.cleanupOrder.push("second"); if (this === scope) state.cleanupThis++; });
      var cancelled = context.cleanup(function () { state.cleanupOrder.push("cancelled"); });
      cancelled();
      context.afterRender(function () {
        state.ownerAfter++;
        state.afterSawRender = document.getElementById("owner-value").textContent === "ready";
      });
      var cancelledAfter = context.afterRender(function () { state.ownerAfter += 100; });
      cancelledAfter();
      return function () { state.legacyCleanup++; };
    }
  });
  core.component("context-retained", {
    value: 1,
    init: function (context) {
      var state = globalThis.__componentContext;
      state.contexts.retained = context;
      state.retainedInit++;
      context.afterRender(function () { state.retainedAfter++; });
    }
  });
  core.component("context-remove-before-render", {
    init: function (context) {
      globalThis.__componentContext.contexts.removed = context;
      context.afterRender(function () { globalThis.__componentContext.removedAfter++; });
      context.host.remove();
    }
  });
  globalThis.__componentContextCore = core;
  globalThis.__componentContextMorph = morph;
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
  var state = globalThis.__componentContext;
  var core = globalThis.__componentContextCore;
  var morph = globalThis.__componentContextMorph;

  await waitFor(function () {
    return state.ownerAfter === 1 && state.retainedAfter === 1 && state.childInit === 1 &&
      !document.getElementById("remove-before-render") && state.contexts.removed.host === null;
  }, "lifecycle contexts did not finish their first render");
  assert(state.contextFrozen, "context or one of its methods was mutable");
  assert(state.contextKeys === "host,owned,listen,cleanup,afterRender", "context keys were " + state.contextKeys);
  assert(state.scopeLeak === false, "lifecycle context entered reactive component scope");
  assert(state.initialOwned === "owned-button", "owned() crossed an ignore or nested boundary: " + state.initialOwned);
  assert(state.ownedFrozen, "owned() did not return a frozen snapshot");
  assert(state.afterSawRender, "afterRender ran before the boundary's completed binding render");
  await nextTurn();
  assert(state.removedAfter === 0, "afterRender survived disposal before its render completed");

  document.getElementById("owned-button").click();
  window.dispatchEvent(new Event("context-window"));
  window.dispatchEvent(new Event("context-capture"));
  state.abortListener();
  window.dispatchEvent(new Event("context-abort"));
  assert(state.ownerClicks === 1 && state.ownerWindows === 1, "context.listen did not attach exact targets");
  assert(state.captureCalls === 1 && state.abortCalls === 0,
    "capture snapshot or AbortSignal listener semantics were incorrect");

  var retained = document.getElementById("retained");
  var retainedContext = state.contexts.retained;
  var incomingRetained = new DOMParser().parseFromString(
    '<section id="retained" data-kit-retain="context-retained" data-kit-component="context-retained">' +
      '<span class="retained-owned">new</span><button class="retained-owned" id="retained-new">New</button>' +
    '</section>', "text/html").body.firstElementChild;
  assert(morph(retained, incomingRetained) === retained, "retained Morph replaced the host");
  await waitFor(function () { return retainedContext.owned(".retained-owned").length === 2; },
    "owned() did not reflect the retained host's morphed DOM");
  assert(state.retainedInit === 1 && state.retainedAfter === 1,
    "retained Morph initialized context again or replayed one-shot afterRender");

  var owner = document.getElementById("owner");
  var ownerContext = state.contexts.owner;
  var ownerScope = core.scopes.get(owner).scope;
  owner.remove();
  await waitFor(function () { return ownerContext.host === null && state.legacyCleanup === 1; },
    "direct removal did not dispose lifecycle context");
  assert(state.cleanupOrder.join(",") === "cancelled,second,first",
    "cleanup was not immediate-on-cancel then LIFO-on-dispose: " + state.cleanupOrder.join(","));
  assert(state.cleanupThis === 2, "registered cleanup did not receive component scope as this");
  document.getElementById("owned-button") && document.getElementById("owned-button").click();
  window.dispatchEvent(new Event("context-window"));
  window.dispatchEvent(new Event("context-capture"));
  assert(state.ownerClicks === 1 && state.ownerWindows === 1 && state.captureCalls === 1,
    "disposed listeners remained attached after options mutation");
  assert(ownerContext.owned("*").length === 0, "disposed owned() did not fail closed");
  assert(ownerContext.cleanup(function () { state.cleanupOrder.push("late"); })() === undefined,
    "disposed cleanup cancel was not safe");
  assert(ownerContext.afterRender(function () { state.ownerAfter += 1000; })() === undefined,
    "disposed afterRender cancel was not safe");
  assert(ownerContext.listen(window, "context-window", function () { state.ownerWindows += 1000; })() === undefined,
    "disposed listen cancel was not safe");
  await nextTurn();
  assert(state.ownerAfter === 1 && state.cleanupOrder.indexOf("late") < 0,
    "disposed lifecycle method scheduled new work");
  assert(ownerScope.host === undefined && ownerScope.owned === undefined,
    "context fields leaked into retained authored scope");
});
</script></body></html>`)
	return page.String()
}
