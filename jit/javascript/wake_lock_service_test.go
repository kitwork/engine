package javascript

import (
	"bytes"
	"strings"
	"testing"
)

func wakeLockServicePackage(t *testing.T) Service {
	t.Helper()
	return Service{
		Name:    "wakeLock",
		Version: "1.0.0",
		Source:  readVanillaFile(t, "service", "wakeLock", "1.0.0.js"),
	}
}

func TestWakeLockServiceStaticContract(t *testing.T) {
	source := wakeLockServicePackage(t).Source
	if len(source) == 0 || source[0] != ';' || source[len(source)-1] != '\n' || bytes.Contains(source, []byte{'\r'}) {
		t.Fatal("wakeLock@1.0.0 is not a sealable LF-only classic script")
	}
	if got := bytes.Count(source, []byte(`kit.service("wakeLock"`)); got != 1 {
		t.Fatalf("wakeLock registration count = %d, want one", got)
	}
	for _, contract := range [][]byte{
		[]byte(`request: request`),
		[]byte(`nativeHost.call("screen.keepAwake", { enabled: true })`),
		[]byte(`nativeHost.call("screen.keepAwake", { enabled: false })`),
		[]byte(`requestMethod.call(wakeLock, "screen")`),
		[]byte(`name: { value: "KitWakeLockError" }`),
		[]byte(`get: function () { return record.status === "released"; }`),
		[]byte(`listen(document, "visibilitychange", visibilityChange)`),
		[]byte(`listen(global, "pagehide", pageHide)`),
	} {
		if !bytes.Contains(source, contract) {
			t.Errorf("wakeLock@1.0.0 lost contract %q", contract)
		}
	}
	for _, forbidden := range [][]byte{
		[]byte(`kit.component(`), []byte(`globalThis.kit`), []byte(`global.kit`),
		[]byte(`window.kit`), []byte(`localStorage`), []byte(`sessionStorage`),
		[]byte(`fetch(`), []byte(`XMLHttpRequest`),
	} {
		if bytes.Contains(source, forbidden) {
			t.Errorf("wakeLock@1.0.0 contains forbidden coupling %q", forbidden)
		}
	}
}

func TestWakeLockServiceSealsWithoutDependencies(t *testing.T) {
	service := wakeLockServicePackage(t)
	artifact, err := Build(BuildOptions{Profile: ProfileKit, Services: []Service{service}})
	if err != nil {
		t.Fatal(err)
	}
	if got := bytes.Count(artifact.Bytes(), service.Source); got != 1 {
		t.Fatalf("standalone wakeLock source count = %d, want one", got)
	}
	if graph := string(artifact.Bytes()); !strings.Contains(graph, `services["wakeLock"] = "1.0.0";`) {
		t.Fatal("standalone wakeLock graph metadata was missing")
	}

	staged, err := BuildStaged(StagedBuildOptions{Profile: ProfileKit, Services: []Service{service}})
	if err != nil {
		t.Fatal(err)
	}
	if len(staged.Services) != 1 || staged.Services[0].Package() != "wakeLock" ||
		staged.Services[0].Version() != "1.0.0" ||
		!bytes.Contains(staged.Services[0].Bytes(), service.Source) {
		t.Fatalf("staged wakeLock artifact = %#v", staged.Services)
	}
	if len(service.Requires) != 0 {
		t.Fatalf("wakeLock registry dependencies = %#v, want none", service.Requires)
	}
}

func TestWakeLockNativeNodeContract(t *testing.T) {
	source := wakeLockServicePackage(t).Source
	script := `
"use strict";
function assert(value, message) { if (!value) throw new Error(message); }
async function rejected(value) {
  try { await value; } catch (error) { return error; }
  throw new Error("promise unexpectedly resolved");
}
async function turns(count) { while (count-- > 0) await Promise.resolve(); }
function deferred() {
  var result = {};
  result.promise = new Promise(function (resolve, reject) {
    result.resolve = resolve;
    result.reject = reject;
  });
  return result;
}
function Events() { this.listeners = Object.create(null); }
Events.prototype.addEventListener = function (type, listener) {
  (this.listeners[type] || (this.listeners[type] = new Set())).add(listener);
};
Events.prototype.removeEventListener = function (type, listener) {
  var listeners = this.listeners[type];
  if (listeners) listeners.delete(listener);
};
Events.prototype.dispatch = function (type) {
  var listeners = this.listeners[type];
  if (listeners) Array.from(listeners).forEach(function (listener) { listener({ type: type }); });
};

var pageEvents = new Events();
globalThis.addEventListener = pageEvents.addEventListener.bind(pageEvents);
globalThis.removeEventListener = pageEvents.removeEventListener.bind(pageEvents);
var document = new Events();
document.visibilityState = "visible";
var ASSEMBLY = Symbol.for("kitjs:assembly");
var browserRequests = 0;
Object.defineProperty(globalThis, "navigator", {
  configurable: true,
  value: { wakeLock: { request: function () { browserRequests++; throw new Error("browser fallback used"); } } }
});

var calls = [];
var enable = function () { return true; };
var disable = function () { return true; };
var host = {
  call: function (action, params) {
    assert(action === "screen.keepAwake", "native action changed to " + action);
    assert(params && Object.keys(params).join(",") === "enabled" && typeof params.enabled === "boolean",
      "native params were not the exact enabled boolean");
    calls.push(params.enabled);
    return params.enabled ? enable() : disable();
  }
};
Object.defineProperty(document, ASSEMBLY, { value: { nativeHost: host }, configurable: true });
var namespace = null;
var kit = {
  service: function (name, value) {
    assert(name === "wakeLock" && namespace === null, "unexpected service registration");
    namespace = Object.freeze(value);
  }
};
` + string(source) + `

(async function () {
  var typeFailures = [];
  try { namespace.request(); } catch (error) { typeFailures.push(error instanceof TypeError); }
  try { namespace.request("display"); } catch (error) { typeFailures.push(error instanceof TypeError); }
  try { namespace.request("screen", true); } catch (error) { typeFailures.push(error instanceof TypeError); }
  assert(typeFailures.length === 3 && typeFailures.every(Boolean) && calls.length === 0,
    "invalid wake-lock types reached the host");

  var firstEnable = deferred();
  enable = function () { return firstEnable.promise; };
  var firstPromise = namespace.request("screen");
  var secondPromise = namespace.request("screen");
  assert(calls.join(",") === "true", "concurrent requests did not share one native enable");
  firstEnable.resolve(true);
  var first = await firstPromise;
  var second = await secondPromise;
  assert(first !== second && Object.isFrozen(first) && Object.isFrozen(second) &&
    Object.keys(first).sort().join(",") === "release,released" &&
    first.released === false && second.released === false,
    "public requests did not return distinct frozen sentinels");
  var releaseTypeError = false;
  try { first.release(true); } catch (error) { releaseTypeError = error instanceof TypeError; }
  assert(releaseTypeError, "wake-lock sentinel release accepted parameters");
  await first.release();
  assert(first.released === true && second.released === false && calls.join(",") === "true",
    "first sentinel release disabled a shared native lock");
  await second.release();
  assert(second.released === true && calls.join(",") === "true,false",
    "last sentinel release did not disable the native lock exactly once");
  await second.release();
  assert(calls.join(",") === "true,false", "sentinel release was not idempotent");

  enable = function () { return Promise.reject({ code: "DENIED", message: "raw native secret" }); };
  var failure = await rejected(namespace.request("screen"));
  assert(failure.name === "KitWakeLockError" && failure.code === "DENIED" &&
    failure.operation === "request" && Object.isFrozen(failure) &&
    failure.message.indexOf("secret") < 0 && browserRequests === 0,
    "native denial was not normalized or attempted browser fallback");
  enable = function () { return Promise.reject({ code: "UNAVAILABLE", message: "raw unavailable secret" }); };
  failure = await rejected(namespace.request("screen"));
  assert(failure.code === "UNAVAILABLE" && browserRequests === 0,
    "native unavailability attempted browser fallback");
  enable = function () { return false; };
  failure = await rejected(namespace.request("screen"));
  assert(failure.code === "FAILED" && failure.operation === "request",
    "invalid native enable acknowledgement was accepted");

  enable = function () { return true; };
  disable = function () { return Promise.reject({ code: "BRIDGE_TIMEOUT", message: "raw release secret" }); };
  var releaseFailureSentinel = await namespace.request("screen");
  failure = await rejected(releaseFailureSentinel.release());
  assert(releaseFailureSentinel.released === true && failure.name === "KitWakeLockError" &&
    failure.code === "TIMEOUT" && failure.operation === "release" &&
    failure.message.indexOf("secret") < 0,
    "native release failure was not normalized after releasing the public sentinel");

  disable = function () { return true; };
  var visibilitySentinel = await namespace.request("screen");
  var beforeVisibility = calls.length;
  document.visibilityState = "hidden";
  document.dispatch("visibilitychange");
  await turns(4);
  assert(visibilitySentinel.released === true && calls.length === beforeVisibility + 1 &&
    calls[calls.length - 1] === false, "visibility hiding did not release the native lock");
  var beforeHiddenRequest = calls.length;
  failure = await rejected(namespace.request("screen"));
  assert(failure.code === "DENIED" && calls.length === beforeHiddenRequest,
    "a hidden document reached the native host");

  document.visibilityState = "visible";
  var pageSentinel = await namespace.request("screen");
  var beforePageHide = calls.length;
  pageEvents.dispatch("pagehide");
  await turns(4);
  assert(pageSentinel.released === true && calls.length === beforePageHide + 1 &&
    calls[calls.length - 1] === false, "pagehide did not release the native lock");
  failure = await rejected(namespace.request("screen"));
  assert(failure.code === "CANCELLED", "pagehide did not close new wake-lock admission");
  pageEvents.dispatch("pageshow");

  var staleEnable = deferred();
  var staleDisable = deferred();
  var freshEnable = deferred();
  var enableSequence = [staleEnable.promise, freshEnable.promise];
  var disableSequence = [staleDisable.promise];
  enable = function () { return enableSequence.shift(); };
  disable = function () { return disableSequence.length ? disableSequence.shift() : true; };
  var beforeRace = calls.length;
  var staleRequest = namespace.request("screen");
  document.visibilityState = "hidden";
  document.dispatch("visibilitychange");
  failure = await rejected(staleRequest);
  assert(failure.code === "CANCELLED" && calls.length === beforeRace + 1,
    "hidden pending acquisition was not cancelled");
  document.visibilityState = "visible";
  var freshRequest = namespace.request("screen");
  assert(calls.length === beforeRace + 1,
    "a fresh enable raced an unresolved stale native enable");
  staleEnable.resolve(true);
  await turns(6);
  assert(calls.length === beforeRace + 2 && calls[calls.length - 1] === false,
    "late native enable was not disabled before reacquisition");
  staleDisable.resolve(true);
  await turns(6);
  assert(calls.length === beforeRace + 3 && calls[calls.length - 1] === true,
    "fresh native enable did not wait for stale cleanup");
  freshEnable.resolve(true);
  var fresh = await freshRequest;
  assert(fresh.released === false, "fresh sentinel inherited stale acquisition state");
  await fresh.release();
  assert(fresh.released === true && browserRequests === 0,
    "fresh native sentinel did not release cleanly");
})().catch(function (error) {
  console.error(error && error.stack || error);
  process.exitCode = 1;
});
`
	runNativeHostNode(t, script)
}

func TestWakeLockBrowserFallbackNodeContract(t *testing.T) {
	source := wakeLockServicePackage(t).Source
	script := `
"use strict";
function assert(value, message) { if (!value) throw new Error(message); }
async function rejected(value) {
  try { await value; } catch (error) { return error; }
  throw new Error("promise unexpectedly resolved");
}
async function turns(count) { while (count-- > 0) await Promise.resolve(); }
function deferred() {
  var result = {};
  result.promise = new Promise(function (resolve, reject) {
    result.resolve = resolve;
    result.reject = reject;
  });
  return result;
}
function Events() { this.listeners = Object.create(null); }
Events.prototype.addEventListener = function (type, listener) {
  (this.listeners[type] || (this.listeners[type] = new Set())).add(listener);
};
Events.prototype.removeEventListener = function (type, listener) {
  var listeners = this.listeners[type];
  if (listeners) listeners.delete(listener);
};
Events.prototype.dispatch = function (type) {
  var listeners = this.listeners[type];
  if (listeners) Array.from(listeners).forEach(function (listener) { listener({ type: type }); });
};
function BrowserSentinel() {
  Events.call(this);
  this.released = false;
  this.releaseCalls = 0;
  this.releaseGate = null;
}
BrowserSentinel.prototype = Object.create(Events.prototype);
BrowserSentinel.prototype.constructor = BrowserSentinel;
BrowserSentinel.prototype.release = function () {
  var self = this;
  self.releaseCalls++;
  var result = self.releaseGate ? self.releaseGate.promise : Promise.resolve();
  return result.then(function () {
    self.released = true;
    self.dispatch("release");
  });
};
BrowserSentinel.prototype.externalRelease = function () {
  this.released = true;
  this.dispatch("release");
};

var pageEvents = new Events();
globalThis.addEventListener = pageEvents.addEventListener.bind(pageEvents);
globalThis.removeEventListener = pageEvents.removeEventListener.bind(pageEvents);
var document = new Events();
document.visibilityState = "visible";
Object.defineProperty(document, Symbol.for("kitjs:assembly"), {
  value: { nativeHost: null }, configurable: true
});
var requestCalls = 0;
var nextRequest = function () { throw new Error("browser request was not configured"); };
var manager = {
  request: function (type) {
    assert(type === "screen", "browser fallback requested " + type);
    requestCalls++;
    return nextRequest();
  }
};
Object.defineProperty(globalThis, "navigator", {
  configurable: true,
  value: { wakeLock: manager }
});
var namespace = null;
var kit = {
  service: function (name, value) {
    assert(name === "wakeLock" && namespace === null, "unexpected service registration");
    namespace = Object.freeze(value);
  }
};
` + string(source) + `

(async function () {
  var firstAcquire = deferred();
  nextRequest = function () { return firstAcquire.promise; };
  var firstPromise = namespace.request("screen");
  var secondPromise = namespace.request("screen");
  assert(requestCalls === 1, "concurrent browser requests did not share one acquisition");
  var browserFirst = new BrowserSentinel();
  firstAcquire.resolve(browserFirst);
  var first = await firstPromise;
  var second = await secondPromise;
  assert(first !== second && !first.released && !second.released,
    "browser fallback did not create independent public sentinels");
  await first.release();
  assert(first.released && !second.released && browserFirst.releaseCalls === 0,
    "one public release released a shared browser sentinel");
  await second.release();
  assert(second.released && browserFirst.releaseCalls === 1,
    "last public release did not release the browser sentinel once");

  var browserExternal = new BrowserSentinel();
  nextRequest = function () { return browserExternal; };
  var externallyReleased = await namespace.request("screen");
  browserExternal.externalRelease();
  await turns(2);
  assert(externallyReleased.released, "browser release event did not update the public sentinel");

  var browserCurrent = new BrowserSentinel();
  nextRequest = function () { return browserCurrent; };
  var current = await namespace.request("screen");
  browserExternal.dispatch("release");
  await turns(2);
  assert(!current.released, "stale browser release callback released a newer lock");

  var releaseGate = deferred();
  browserCurrent.releaseGate = releaseGate;
  var releasingPromise = current.release();
  var acquireAfterRelease = deferred();
  var browserAfterRelease = new BrowserSentinel();
  nextRequest = function () { return acquireAfterRelease.promise; };
  var callsBeforeQueued = requestCalls;
  var queuedRequest = namespace.request("screen");
  assert(requestCalls === callsBeforeQueued,
    "browser reacquisition raced a pending underlying release");
  releaseGate.resolve();
  await releasingPromise;
  await turns(4);
  assert(requestCalls === callsBeforeQueued + 1,
    "queued browser acquisition did not begin after release settled");
  acquireAfterRelease.resolve(browserAfterRelease);
  var queued = await queuedRequest;
  assert(!queued.released, "queued browser request resolved released");
  await queued.release();

  var staleAcquire = deferred();
  var staleCleanup = deferred();
  var browserStale = new BrowserSentinel();
  browserStale.releaseGate = staleCleanup;
  nextRequest = function () { return staleAcquire.promise; };
  var beforeStale = requestCalls;
  var staleRequest = namespace.request("screen");
  document.visibilityState = "hidden";
  document.dispatch("visibilitychange");
  var failure = await rejected(staleRequest);
  assert(failure.name === "KitWakeLockError" && failure.code === "CANCELLED" &&
    failure.operation === "request", "pending browser acquisition was not cancelled on hide");
  document.visibilityState = "visible";
  var freshAcquire = deferred();
  var browserFresh = new BrowserSentinel();
  nextRequest = function () { return freshAcquire.promise; };
  var freshRequest = namespace.request("screen");
  assert(requestCalls === beforeStale + 1,
    "fresh browser request raced an unresolved stale acquisition");
  staleAcquire.resolve(browserStale);
  await turns(5);
  assert(browserStale.releaseCalls === 1 && requestCalls === beforeStale + 1,
    "resolved stale browser acquisition was not held for cleanup");
  staleCleanup.resolve();
  await turns(6);
  assert(requestCalls === beforeStale + 2,
    "fresh browser acquisition did not wait for stale release");
  freshAcquire.resolve(browserFresh);
  var fresh = await freshRequest;
  browserStale.dispatch("release");
  await turns(2);
  assert(!fresh.released, "stale acquired sentinel callback affected the fresh browser lock");
  await fresh.release();

  nextRequest = function () {
    return Promise.reject({ name: "NotAllowedError", message: "raw browser secret" });
  };
  failure = await rejected(namespace.request("screen"));
  assert(failure.name === "KitWakeLockError" && failure.code === "DENIED" &&
    failure.operation === "request" && Object.isFrozen(failure) &&
    failure.message.indexOf("secret") < 0,
    "browser denial was not normalized");

  Object.defineProperty(globalThis.navigator, "wakeLock", { configurable: true, value: null });
  failure = await rejected(namespace.request("screen"));
  assert(failure.code === "UNAVAILABLE" && failure.operation === "request",
    "missing browser wake-lock capability did not fail unavailable");
})().catch(function (error) {
  console.error(error && error.stack || error);
  process.exitCode = 1;
});
`
	runNativeHostNode(t, script)
}
