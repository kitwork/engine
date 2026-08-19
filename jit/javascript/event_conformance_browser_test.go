package javascript

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestBrowserGenericEventDirectiveGrammar is the browser-level contract for
// data-kit-<event>:<modifier>. It deliberately exercises the public HTML
// surface only: no private runtime hook or registry is used by the fixture.
func TestBrowserGenericEventDirectiveGrammar(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping standalone KitJS event grammar contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	kitJS := readVanillaFile(t, "kit.js")
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/kit.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(kitJS)
		case "/contracts/events.html":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(genericEventDirectiveDocument))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	runVanillaBrowser(t, browser, server.URL+"/contracts/events.html")
}

const genericEventDirectiveDocument = `<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><title>KitJS generic event directive contract</title></head>
<body>
  <main id="event-root" data-kit-component="event-conformance">
    <section id="basic-events">
      <button type="button" id="event-click" data-kit-click="mark('click')">click</button>
      <button type="button" id="event-dblclick" data-kit-dblclick="mark('dblclick')">dblclick</button>
      <form id="event-submit" data-kit-submit:prevent="mark('submit')"></form>
      <input id="event-input" data-kit-input="mark('input')">
      <input id="event-change" data-kit-change="mark('change')">
      <input id="event-keydown" data-kit-keydown="mark('keydown')">
      <input id="event-keyup" data-kit-keyup="mark('keyup')">
      <button type="button" id="event-pointerdown" data-kit-pointerdown="mark('pointerdown')">pointerdown</button>
      <button type="button" id="event-pointerup" data-kit-pointerup="mark('pointerup')">pointerup</button>
      <input id="event-focusin" data-kit-focusin="mark('focusin')">
      <input id="event-focusout" data-kit-focusout="mark('focusout')">
      <div id="event-safe-value" data-kit-click="captureEventValue($event.value)">safe value</div>
    </section>

    <section id="modifier-events">
      <div id="modifier-self" data-kit-click:self="mark('self')">
        <button type="button" id="modifier-self-child">self child</button>
      </div>
      <a id="modifier-prevent" href="#prevented" data-kit-click:prevent="mark('prevent')">prevent</a>
      <div id="modifier-stop-outer" data-kit-click="mark('stop-outer')">
        <button type="button" id="modifier-stop-inner" data-kit-click:stop="mark('stop-inner')">stop</button>
      </div>
      <div data-kit-click="mark('stop-same-outer')">
        <button
          type="button"
          id="modifier-stop-same"
          data-kit-click:stop="mark('stop-same-a')"
          data-kit-click:once="mark('stop-same-b')">same target</button>
      </div>
      <button type="button" id="modifier-once" data-kit-click:once="mark('once')">once</button>
      <section id="modifier-outside" data-kit-click:outside="mark('outside')">
        <button type="button" id="modifier-outside-child">inside outside-region</button>
      </section>
      <section id="modifier-outside-dblclick" data-kit-dblclick:outside="mark('outside-dblclick')">
        <button type="button" id="modifier-outside-dblclick-child">inside dblclick outside-region</button>
      </section>
      <section id="modifier-outside-pointerdown" data-kit-pointerdown:outside="mark('outside-pointerdown')">
        <button type="button" id="modifier-outside-pointerdown-child">inside pointerdown outside-region</button>
      </section>
      <section id="modifier-outside-pointerup" data-kit-pointerup:outside="mark('outside-pointerup')">
        <button type="button" id="modifier-outside-pointerup-child">inside pointerup outside-region</button>
      </section>
      <section id="modifier-outside-focusin" data-kit-focusin:outside="mark('outside-focusin')">
        <input id="modifier-outside-focusin-child">
      </section>
      <input id="modifier-enter" data-kit-keydown:enter="mark('enter')">
      <input id="modifier-escape" data-kit-keydown:escape="mark('escape')">
      <input id="modifier-debounce" data-kit-input:debounce(30)="mark('debounce')">
      <form id="modifier-debounce-prevent" data-kit-submit:prevent:debounce(30)="mark('debounce-prevent')"></form>
    </section>

    <section id="invalid-events">
      <button type="button" id="invalid-unknown" data-kit-click:mystery="mark('invalid')">unknown</button>
      <button type="button" id="invalid-click-enter" data-kit-click:enter="mark('invalid')">wrong enter</button>
      <input id="invalid-input-escape" data-kit-input:escape="mark('invalid')">
      <input id="invalid-key-conflict" data-kit-keydown:enter:escape="mark('invalid')">
      <button type="button" id="invalid-self-outside" data-kit-click:self:outside="mark('invalid')">self outside</button>
      <input id="invalid-input-outside" data-kit-input:outside="mark('invalid')">
      <button type="button" id="invalid-duplicate-once" data-kit-click:once:once="mark('invalid')">duplicate once</button>
      <input id="invalid-debounce-zero" data-kit-input:debounce(0)="mark('invalid')">
      <input id="invalid-debounce-negative" data-kit-input:debounce(-1)="mark('invalid')">
      <input id="invalid-debounce-text" data-kit-input:debounce(nope)="mark('invalid')">
      <input id="invalid-debounce-too-large" data-kit-input:debounce(60001)="mark('invalid')">
      <input id="invalid-debounce-duplicate" data-kit-input:debounce(10):debounce(20)="mark('invalid')">
    </section>
  </main>

  <script>
  (function () {
    "use strict";
    var eventTypes = "click dblclick submit input change keydown keyup pointerdown pointerup focusin focusout".split(" ");
    var watched = Object.create(null);
    var counts = Object.create(null);
    eventTypes.forEach(function (type) { watched[type] = true; counts[type] = 0; });
    var originalListener = EventTarget.prototype.addEventListener;
    EventTarget.prototype.addEventListener = function (type, listener, options) {
      if (this === document && watched[type]) counts[type]++;
      return originalListener.call(this, type, listener, options);
    };
    globalThis.__kitEventTypes = eventTypes;
    globalThis.__kitEventListenerCounts = counts;
    globalThis.__restoreEventListenerSpy = function () {
      EventTarget.prototype.addEventListener = originalListener;
      delete globalThis.__restoreEventListenerSpy;
    };

    globalThis.__kitEventCalls = Object.create(null);
    globalThis.__kitEventValue = "unset";
    globalThis.__kitEventErrors = [];
    var originalError = console.error;
    console.error = function () {
      var message = [];
      for (var index = 0; index < arguments.length; index++) {
        var value = arguments[index];
        message.push(String(value && value.message || value));
      }
      globalThis.__kitEventErrors.push(message.join(" "));
      return originalError.apply(this, arguments);
    };
  })();
  </script>
  <script src="/kit.js"></script>
  <script>
    globalThis.__firstEventKit = globalThis.kit;
    kit.component("event-conformance", {
      mark: function (name) {
        var calls = globalThis.__kitEventCalls;
        calls[name] = (calls[name] || 0) + 1;
      },
      captureEventValue: function (value) {
        globalThis.__kitEventValue = value;
      }
    });
  </script>
  <script src="/kit.js"></script>
  <script>
` + browserHarness + `

__runStandaloneKitTest(async function () {
  var assert = __kitTestAssert;
  var waitFor = __kitTestWaitFor;
  var nextTurn = __kitTestNextTurn;
  var calls = globalThis.__kitEventCalls;

  function count(name) { return calls[name] || 0; }
  function fire(id, type, init) {
    var target = typeof id === "string" ? document.getElementById(id) : id;
    assert(target, "missing event target " + id);
    var options = { bubbles: true, cancelable: true };
    if (init) Object.keys(init).forEach(function (key) { options[key] = init[key]; });
    var event;
    if (type === "keydown" || type === "keyup") event = new KeyboardEvent(type, options);
    else if (type === "click" || type === "dblclick") event = new MouseEvent(type, options);
    else if (type === "pointerdown" || type === "pointerup") {
      event = typeof PointerEvent === "function" ? new PointerEvent(type, options) : new Event(type, options);
    } else if (type === "focusin" || type === "focusout") {
      event = typeof FocusEvent === "function" ? new FocusEvent(type, options) : new Event(type, options);
    } else event = new Event(type, options);
    target.dispatchEvent(event);
    return event;
  }

  assert(globalThis.kit === globalThis.__firstEventKit, "loading kit.js twice replaced the canonical kit object");
  globalThis.__kitEventTypes.forEach(function (type) {
    assert(globalThis.__kitEventListenerCounts[type] === 1,
      type + " has " + globalThis.__kitEventListenerCounts[type] + " delegated document listeners, want one");
  });
  globalThis.__restoreEventListenerSpy();

  Object.defineProperty(document.getElementById("event-safe-value"), "value", {
    value: { label: "must-not-cross-the-expression-boundary" }
  });
  fire("event-safe-value", "click");
  assert(globalThis.__kitEventValue === null, "$event.value exposed a non-primitive element value");

  fire("event-click", "click");
  fire("event-dblclick", "dblclick");
  var submitEvent = fire("event-submit", "submit");
  fire("event-input", "input");
  fire("event-change", "change");
  fire("event-keydown", "keydown", { key: "a" });
  fire("event-keyup", "keyup", { key: "a" });
  fire("event-pointerdown", "pointerdown", { button: 0 });
  fire("event-pointerup", "pointerup", { button: 0 });
  fire("event-focusin", "focusin");
  fire("event-focusout", "focusout");
  assert(submitEvent.defaultPrevented, "submit:prevent did not synchronously prevent its event");
  globalThis.__kitEventTypes.forEach(function (type) {
    assert(count(type) === 1, type + " action ran " + count(type) + " times, want one");
  });

  fire("modifier-self-child", "click");
  assert(count("self") === 0, "self accepted a descendant target");
  fire("modifier-self", "click");
  assert(count("self") === 1, "self rejected its own target");

  var preventEvent = fire("modifier-prevent", "click");
  assert(preventEvent.defaultPrevented, "prevent did not synchronously cancel click");
  assert(count("prevent") === 1, "prevent action did not execute once");

  fire("modifier-stop-inner", "click");
  assert(count("stop-inner") === 1, "stop action did not execute");
  assert(count("stop-outer") === 0, "stop allowed an outer KitJS action to execute");

  fire("modifier-stop-same", "click");
  fire("modifier-stop-same", "click");
  assert(count("stop-same-a") === 2, "stop action did not run for both events");
  assert(count("stop-same-b") === 1, "stop skipped or repeated a same-target once action");
  assert(count("stop-same-outer") === 0, "stop allowed a same-target event to reach its ancestor");

  fire("modifier-once", "click");
  fire("modifier-once", "click");
  assert(count("once") === 1, "once action ran " + count("once") + " times");

  var outsideBefore = count("outside");
  fire("modifier-outside-child", "click");
  assert(count("outside") === outsideBefore, "outside fired for a descendant click");
  fire(document.body, "click");
  assert(count("outside") === outsideBefore + 1, "outside did not fire for an external click");

  [
    ["dblclick", "modifier-outside-dblclick-child", "outside-dblclick"],
    ["pointerdown", "modifier-outside-pointerdown-child", "outside-pointerdown"],
    ["pointerup", "modifier-outside-pointerup-child", "outside-pointerup"],
    ["focusin", "modifier-outside-focusin-child", "outside-focusin"]
  ].forEach(function (testCase) {
    var type = testCase[0];
    var before = count(testCase[2]);
    fire(testCase[1], type);
    assert(count(testCase[2]) === before, type + ":outside fired for a descendant event");
    fire(document.body, type);
    assert(count(testCase[2]) === before + 1, type + ":outside did not fire for an external event");
  });

  fire("modifier-enter", "keydown", { key: "Enter", isComposing: true });
  fire("modifier-enter", "keydown", { key: "a" });
  fire("modifier-enter", "keydown", { key: "Enter" });
  assert(count("enter") === 1, "enter filter did not reject IME or accept exactly Enter");
  fire("modifier-escape", "keydown", { key: "Escape", isComposing: true });
  fire("modifier-escape", "keydown", { key: "Enter" });
  fire("modifier-escape", "keydown", { key: "Escape" });
  assert(count("escape") === 1, "escape filter did not reject IME or accept exactly Escape");

  fire("modifier-debounce", "input");
  fire("modifier-debounce", "input");
  fire("modifier-debounce", "input");
  await nextTurn();
  assert(count("debounce") === 0, "debounce ran before its quiet period");
  await waitFor(function () { return count("debounce") === 1; }, "debounce did not coalesce a burst into one action");

  var debouncedSubmit = fire("modifier-debounce-prevent", "submit");
  assert(debouncedSubmit.defaultPrevented, "prevent was delayed behind debounce");
  assert(count("debounce-prevent") === 0, "debounced submit action ran synchronously");
  await waitFor(function () { return count("debounce-prevent") === 1; }, "debounced submit action did not run once");

  fire("invalid-unknown", "click");
  fire("invalid-click-enter", "click");
  fire("invalid-input-escape", "input");
  fire("invalid-key-conflict", "keydown", { key: "Enter" });
  fire("invalid-key-conflict", "keydown", { key: "Escape" });
  fire("invalid-self-outside", "click");
  fire(document.body, "click");
  fire("invalid-input-outside", "input");
  fire(document.body, "input");
  fire("invalid-duplicate-once", "click");
  fire("invalid-duplicate-once", "click");
  fire("invalid-debounce-zero", "input");
  fire("invalid-debounce-negative", "input");
  fire("invalid-debounce-text", "input");
  fire("invalid-debounce-too-large", "input");
  fire("invalid-debounce-duplicate", "input");
  await new Promise(function (resolve) { setTimeout(resolve, 60); });
  assert(count("invalid") === 0, "an invalid event attribute degraded into an executable handler");
  assert(globalThis.__kitEventErrors.some(function (message) {
    return /event|modifier|unsupported|invalid/i.test(message);
  }), "invalid event attributes failed silently instead of reporting a diagnostic");

  var root = document.getElementById("event-root");
  var liveOutside = document.createElement("section");
  liveOutside.setAttribute("data-kit-click:outside", "mark('detached-outside')");
  root.appendChild(liveOutside);
  fire(document.body, "click");
  assert(count("detached-outside") === 1, "a connected dynamic outside action was not live");
  liveOutside.remove();
  fire(document.body, "click");
  assert(count("detached-outside") === 1, "a detached outside action still executed");

  var liveDirect = document.createElement("button");
  liveDirect.type = "button";
  liveDirect.setAttribute("data-kit-click", "mark('detached-direct')");
  root.appendChild(liveDirect);
  fire(liveDirect, "click");
  assert(count("detached-direct") === 1, "a connected dynamic direct action was not live");
  liveDirect.remove();
  fire(liveDirect, "click");
  assert(count("detached-direct") === 1, "a detached direct action still executed");

  var pending = document.createElement("input");
  pending.setAttribute("data-kit-input:debounce(30)", "mark('detached-debounce')");
  root.appendChild(pending);
  fire(pending, "input");
  pending.remove();
  await new Promise(function (resolve) { setTimeout(resolve, 60); });
  assert(count("detached-debounce") === 0, "a detached node's pending debounce action executed");
});
  </script>
</body>
</html>`
