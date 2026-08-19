package javascript

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestNetworkCookieServiceSourcesAreClosed(t *testing.T) {
	tests := []struct {
		name string
	}{
		{name: "network"},
		{name: "cookie"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			source := string(readVanillaFile(t, "service", test.name, "1.0.0.js"))
			registrar := `kit.service("` + test.name + `"`
			if got := strings.Count(source, registrar); got != 1 {
				t.Fatalf("%s registrar count = %d, want one", test.name, got)
			}
			if !strings.HasPrefix(strings.TrimSpace(source), ";") {
				t.Fatalf("%s is not an ordinary classic script", test.name)
			}
			for _, forbidden := range []string{
				"globalThis.kit", "fetch(", "XMLHttpRequest", "localStorage", "sessionStorage",
				"data-kit", "<html", "<script", "kit.component(", "kit.use(",
			} {
				if strings.Contains(source, forbidden) {
					t.Fatalf("%s source contains forbidden runtime/HTML bridge %q", test.name, forbidden)
				}
			}
		})
	}
}

func TestBuildNetworkCookieServiceGraphIsSealedAndDeterministic(t *testing.T) {
	network := networkServicePackage(t)
	cookie := cookieServicePackage(t)
	first, err := Build(BuildOptions{
		Profile:  ProfileKit,
		Services: []Service{network, cookie},
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := Build(BuildOptions{
		Profile:  ProfileKit,
		Services: []Service{cookie, network},
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.Name() != second.Name() || !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Fatal("network/cookie service graph depends on input order")
	}
	source := first.Bytes()
	cookieAt := bytes.Index(source, cookie.Source)
	networkAt := bytes.Index(source, network.Source)
	if cookieAt < 0 || networkAt < 0 || cookieAt >= networkAt {
		t.Fatalf("sealed service order = cookie %d network %d, want deterministic name order", cookieAt, networkAt)
	}
	if bytes.Count(source, cookie.Source) != 1 || bytes.Count(source, network.Source) != 1 {
		t.Fatal("sealed artifact did not include each service source exactly once")
	}
	base, err := Build(BuildOptions{Profile: ProfileKit})
	if err != nil {
		t.Fatal(err)
	}
	if first.Name() == base.Name() || bytes.Equal(first.Bytes(), base.Bytes()) {
		t.Fatal("network/cookie graph did not affect immutable artifact identity")
	}
}

func TestBrowserNetworkCookieServiceContract(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping network/cookie browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	artifact := buildNetworkCookieArtifact(t)
	assetPath := "/assets/" + artifact.Name()
	var artifactRequests atomic.Int64
	var packageRequests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case assetPath:
			artifactRequests.Add(1)
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(artifact.Bytes())
		case "/contract/cookie.html":
			response.Header().Add("Set-Cookie", "serverVisible=plain; Path=/; SameSite=Lax")
			response.Header().Add("Set-Cookie", "serverSecret=opaque; Path=/; HttpOnly; SameSite=Lax")
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = fmt.Fprintf(response, networkCookieContractDocument, assetPath)
		case "/service/network/1.0.0.js", "/service/cookie/1.0.0.js", "/network.js", "/cookie.js":
			packageRequests.Add(1)
			http.Error(response, "services must already be sealed into the artifact", http.StatusGone)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	runVanillaBrowser(t, browser, server.URL+"/contract/cookie.html")
	if got := artifactRequests.Load(); got != 1 {
		t.Fatalf("network/cookie artifact requests = %d, want 1", got)
	}
	if got := packageRequests.Load(); got != 0 {
		t.Fatalf("browser fetched a sealed network/cookie package %d times", got)
	}
}

func TestBrowserNetworkUnsubscribeReleasesSubscriber(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping network forced-GC contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	artifact := buildNetworkCookieArtifact(t)
	assetPath := "/assets/" + artifact.Name()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case assetPath:
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(artifact.Bytes())
		case "/network-retention.html":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = fmt.Fprintf(response, networkRetentionDocument, assetPath)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	status, output := runRetentionBrowser(t, browser, server.URL+"/network-retention.html")
	if status == "unsupported" {
		t.Skipf("browser did not make forced network collection observable\n%s", boundedVanillaOutput(output))
	}
	if status != "passed" {
		t.Fatalf("network unsubscribe retention contract did not pass\n%s", boundedVanillaOutput(output))
	}
}

func networkServicePackage(t *testing.T) Service {
	t.Helper()
	return Service{
		Name:    "network",
		Version: "1.0.0",
		Source:  readVanillaFile(t, "service", "network", "1.0.0.js"),
	}
}

func cookieServicePackage(t *testing.T) Service {
	t.Helper()
	return Service{
		Name:    "cookie",
		Version: "1.0.0",
		Source:  readVanillaFile(t, "service", "cookie", "1.0.0.js"),
	}
}

func buildNetworkCookieArtifact(t *testing.T) Artifact {
	t.Helper()
	artifact, err := Build(BuildOptions{
		Profile:  ProfileKit,
		Services: []Service{networkServicePackage(t), cookieServicePackage(t)},
		Components: []ComponentVersion{{
			Name: "network-cookie-contract", Version: "1.0.0",
		}},
		Scripts: []Script{{
			Name: "network-cookie-contract",
			Source: []byte(`;kit.component("network-cookie-contract", {
  safe: "component-only"
});
`),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return artifact
}

const networkCookieContractDocument = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>Network and cookie service contract</title>
<script>
(function () {
  "use strict";
  var probe = globalThis.__networkProbe = {
    online: true,
    navigatorMode: "normal",
    adds: { online: 0, offline: 0 },
    removes: { online: 0, offline: 0 },
    reports: []
  };
  var simulatedNavigator = {};
  Object.defineProperty(simulatedNavigator, "onLine", {
    configurable: true,
    get: function () {
      if (probe.navigatorMode === "throw") throw new Error("navigator unavailable");
      if (probe.navigatorMode === "undefined") return undefined;
      return probe.online;
    }
  });
  Object.defineProperty(globalThis, "navigator", {
    configurable: true,
    get: function () {
      return probe.navigatorMode === "absent" ? null : simulatedNavigator;
    }
  });
  var add = window.addEventListener;
  var remove = window.removeEventListener;
  window.addEventListener = function (type, listener, options) {
    if (type === "online" || type === "offline") probe.adds[type]++;
    return add.call(this, type, listener, options);
  };
  window.removeEventListener = function (type, listener, options) {
    if (type === "online" || type === "offline") probe.removes[type]++;
    return remove.call(this, type, listener, options);
  };
  globalThis.reportError = function (error) {
    probe.reports.push(String(error && error.message || error));
  };

  var cookie = Object.getOwnPropertyDescriptor(Document.prototype, "cookie");
  globalThis.__cookieWrites = [];
  Object.defineProperty(document, "cookie", {
    configurable: true,
    get: function () { return cookie.get.call(document); },
    set: function (value) {
      globalThis.__cookieWrites.push(String(value));
      return cookie.set.call(document, value);
    }
  });
})();
</script>
<script src=%q></script></head><body>
<main data-kit-component="network-cookie-contract" data-kit-version="1.0.0">
  <output id="html-network" data-kit-text="kit.network.online">server-only</output>
  <button id="html-cookie" type="button" data-kit-click="kit.cookie.set('html-poison', 'bad')">Try HTML cookie</button>
</main>
<script>
` + browserHarness + `
__runStandaloneKitTest(async function () {
  var assert = __kitTestAssert;
  var nextTurn = __kitTestNextTurn;
  var probe = globalThis.__networkProbe;
  var network = globalThis.kit.network;
  var cookie = globalThis.kit.cookie;

  assert(Object.keys(globalThis.kit).join(",") === "version,component,cookie,network",
    "sealed public keys were " + Object.keys(globalThis.kit).join(","));
  assert(Object.isFrozen(globalThis.kit) && Object.isFrozen(network) && Object.isFrozen(cookie),
    "sealed service facade was mutable");
  assert(globalThis.kit.service === undefined && globalThis.kit.bridge === undefined,
    "private registrar or bridge escaped package assembly");

  var networkVersion = Object.getOwnPropertyDescriptor(network, "version");
  var cookieVersion = Object.getOwnPropertyDescriptor(cookie, "version");
  assert(network.version === "1.0.0" && networkVersion && networkVersion.enumerable === false &&
    networkVersion.writable === false && networkVersion.configurable === false,
    "network exact version was not hidden and immutable");
  assert(cookie.version === "1.0.0" && cookieVersion && cookieVersion.enumerable === false &&
    cookieVersion.writable === false && cookieVersion.configurable === false,
    "cookie exact version was not hidden and immutable");
  assert(Object.keys(network).join(",") === "online,snapshot,subscribe",
    "network members were " + Object.keys(network).join(","));
  assert(Object.keys(cookie).join(",") === "get,set,remove,has",
    "cookie members were " + Object.keys(cookie).join(","));
  var onlineDescriptor = Object.getOwnPropertyDescriptor(network, "online");
  assert(onlineDescriptor && typeof onlineDescriptor.get === "function" && onlineDescriptor.set === undefined,
    "network.online was not a readonly getter");
  assert(network.connection === undefined && network.effectiveType === undefined &&
    network.downlink === undefined && network.rtt === undefined && network.saveData === undefined,
    "network exposed unstable connection metrics");
  assert(network.bridge === undefined && network.addEventListener === undefined &&
    cookie.bridge === undefined && cookie.raw === undefined && cookie.all === undefined &&
    cookie.document === undefined && cookie.httpOnly === undefined,
    "network/cookie exposed a raw browser bridge");

  assert(probe.adds.online === 0 && probe.adds.offline === 0,
    "network attached listeners before its first subscriber");
  var initial = network.snapshot();
  assert(initial === network.snapshot() && Object.isFrozen(initial) &&
    Object.keys(initial).join(",") === "online" && initial.online === true && network.online === true,
    "network initial snapshot was not stable, frozen, or online");

  var first = [];
  var second = [];
  var stopFirst = network.subscribe(function (value) { first.push(value); });
  assert(first.length === 1 && first[0] === initial, "first subscriber did not synchronously receive current state");
  assert(probe.adds.online === 1 && probe.adds.offline === 1,
    "first subscriber did not attach exactly one event pair");
  var reports = probe.reports.length;
  var stopThrowing = network.subscribe(function () { throw new Error("network subscriber failed"); });
  assert(probe.reports.length === reports + 1, "initial throwing subscriber escaped isolation");
  var stopSecond = network.subscribe(function (value) { second.push(value); });
  assert(second.length === 1 && second[0] === initial && probe.adds.online === 1 && probe.adds.offline === 1,
    "additional subscriber was not synchronous or duplicated event attachment");

  probe.online = false;
  window.dispatchEvent(new Event("offline"));
  assert(first.length === 2 && second.length === 2 && first[1] === second[1] &&
    first[1].online === false && Object.isFrozen(first[1]) && network.online === false,
    "offline transition did not publish one shared frozen snapshot");
  assert(probe.reports.length === reports + 2, "throwing transition subscriber blocked isolated delivery");
  var firstCount = first.length;
  var secondCount = second.length;
  window.dispatchEvent(new Event("offline"));
  assert(first.length === firstCount && second.length === secondCount,
    "duplicate offline state was republished");

  stopThrowing();
  stopThrowing();
  stopFirst();
  stopFirst();
  assert(probe.removes.online === 0 && probe.removes.offline === 0,
    "network detached while one subscriber remained");
  probe.online = true;
  window.dispatchEvent(new Event("online"));
  assert(first.length === firstCount && second.length === secondCount + 1 && second[second.length - 1].online === true,
    "unsubscribe or surviving subscriber delivery failed");
  stopSecond();
  stopSecond();
  assert(probe.removes.online === 1 && probe.removes.offline === 1,
    "last idempotent unsubscribe did not detach exactly one event pair");

  probe.navigatorMode = "undefined";
  assert(network.snapshot().online === true && network.online === true,
    "undefined navigator.onLine was treated as authoritative offline");
  probe.navigatorMode = "throw";
  assert(network.snapshot().online === true && network.online === true,
    "throwing navigator.onLine was treated as authoritative offline");
  probe.navigatorMode = "absent";
  assert(network.snapshot().online === true && network.online === true,
    "absent navigator was treated as authoritative offline");
  probe.navigatorMode = "normal";
  probe.online = false;
  window.dispatchEvent(new Event("offline"));
  assert(second.length === secondCount + 1, "detached network subscriber received another event");
  var detached = network.snapshot();
  assert(detached.online === false && network.online === false,
    "snapshot/getter did not resample while detached");
  var resumed = [];
  var stopResumed = network.subscribe(function (value) { resumed.push(value); });
  assert(resumed.length === 1 && resumed[0] === detached && probe.adds.online === 2 && probe.adds.offline === 2,
    "resubscribe did not synchronously reuse current state and reattach once");
  stopResumed();
  assert(probe.removes.online === 2 && probe.removes.offline === 2,
    "resumed last unsubscribe did not detach once");
  var invalidSubscriber = false;
  try { network.subscribe(null); } catch (error) { invalidSubscriber = error instanceof TypeError; }
  assert(invalidSubscriber && probe.adds.online === 2,
    "network accepted an invalid subscriber or attached for it");

  assert(cookie.get("serverVisible") === "plain" && cookie.has("serverVisible"),
    "cookie could not read a visible same-origin server cookie");
  assert(cookie.get("serverSecret") === null && cookie.has("serverSecret") === false &&
    document.cookie.indexOf("serverSecret") < 0,
    "cookie service observed an HttpOnly cookie");
  var encodedValue = "hello; =/%% ☃";
  assert(cookie.set("encoded", encodedValue) === true && cookie.get("encoded") === encodedValue && cookie.has("encoded"),
    "cookie encoding round trip failed");
  var defaultWrite = globalThis.__cookieWrites[globalThis.__cookieWrites.length - 1];
  assert(defaultWrite.indexOf("encoded=hello%%3B%%20%%3D%%2F%%25%%20%%E2%%98%%83") === 0 &&
    defaultWrite.indexOf("; Path=/; SameSite=Lax") > 0 && defaultWrite.indexOf("; Secure") < 0,
    "cookie safe HTTP defaults or encoding were " + defaultWrite);

  assert(cookie.set("scoped", "value", { path: "/contract", sameSite: "strict", maxAge: 60 }) === true &&
    cookie.get("scoped") === "value", "scoped cookie was not observable");
  var scopedWrite = globalThis.__cookieWrites[globalThis.__cookieWrites.length - 1];
  assert(scopedWrite === "scoped=value; Path=/contract; SameSite=Strict; Max-Age=60",
    "bounded cookie options produced " + scopedWrite);
  assert(cookie.remove("scoped", { path: "/elsewhere", sameSite: "Strict" }) === true && cookie.has("scoped"),
    "unobservable-path removal reported failure or removed the wrong path");
  assert(cookie.remove("scoped", { path: "/contract", sameSite: "Strict" }) === true && !cookie.has("scoped"),
    "matching-path cookie removal failed");
  var removalWrite = globalThis.__cookieWrites[globalThis.__cookieWrites.length - 1];
  assert(removalWrite.indexOf("scoped=; Path=/contract; SameSite=Strict; Max-Age=0; Expires=") === 0,
    "cookie removal lacked deterministic expiry: " + removalWrite);
  assert(cookie.set("expiring", "gone", { maxAge: 0 }) === true && !cookie.has("expiring"),
    "maxAge=0 did not reflect observable deletion");
  assert(cookie.remove("encoded") === true && !cookie.has("encoded"), "default cookie removal failed");

  function rejects(operation, label) {
    var rejected = false;
    try { operation(); } catch (error) { rejected = error instanceof TypeError; }
    assert(rejected, label + " was accepted");
  }
  rejects(function () { cookie.get(1); }, "non-string cookie name");
  rejects(function () { cookie.get(""); }, "empty cookie name");
  rejects(function () { cookie.get("bad=name"); }, "cookie delimiter name");
  rejects(function () { cookie.get("n".repeat(129)); }, "oversized cookie name");
  rejects(function () { cookie.set("value", 1); }, "non-string cookie value");
  rejects(function () { cookie.set("value", "\uD800"); }, "invalid-Unicode cookie value");
  rejects(function () { cookie.set("value", "x".repeat(3801)); }, "oversized encoded cookie value");
  rejects(function () { cookie.set("value", "x", null); }, "null cookie options");
  rejects(function () { cookie.set("value", "x", { domain: "example.test" }); }, "cookie domain bridge");
  rejects(function () { cookie.set("value", "x", { expires: new Date() }); }, "cookie expires bridge");
  rejects(function () { cookie.set("value", "x", { httpOnly: true }); }, "cookie HttpOnly bridge");
  rejects(function () { cookie.set("value", "x", { raw: true }); }, "raw cookie option");
  rejects(function () { cookie.set("value", "x", { path: "relative" }); }, "relative cookie path");
  rejects(function () { cookie.set("value", "x", { path: "/bad;path" }); }, "cookie path delimiter");
  rejects(function () { cookie.set("value", "x", { path: "/snowman-☃" }); }, "non-ASCII cookie path");
  rejects(function () { cookie.set("value", "x", { path: "/" + "p".repeat(1024) }); }, "oversized cookie path");
  rejects(function () { cookie.set("value", "x", { sameSite: "Legacy" }); }, "unknown SameSite mode");
  rejects(function () { cookie.set("value", "x", { sameSite: "None" }); }, "insecure SameSite=None");
  rejects(function () { cookie.set("value", "x", { secure: "yes" }); }, "non-boolean secure option");
  rejects(function () { cookie.set("value", "x", { maxAge: 1.5 }); }, "fractional cookie maxAge");
  rejects(function () { cookie.set("value", "x", { maxAge: 31536001 }); }, "oversized cookie maxAge");
  rejects(function () { cookie.remove("value", { maxAge: 1 }); }, "remove maxAge override");
  rejects(function () { cookie.set("__Secure-value", "x"); }, "insecure __Secure- cookie");
  rejects(function () { cookie.set("__Host-value", "x", { secure: true, path: "/contract" }); },
    "non-root __Host- cookie");
  rejects(function () { cookie.set("value", "x".repeat(3600), { path: "/" + "p".repeat(500) }); },
    "oversized full cookie assignment");
  var reads = 0;
  var accessor = {};
  Object.defineProperty(accessor, "path", { get: function () { reads++; return "/"; } });
  rejects(function () { cookie.set("value", "x", accessor); }, "cookie option accessor");
  assert(reads === 0, "cookie read an option accessor before rejecting it");
  var symbols = {};
  symbols[Symbol("cookie")] = true;
  rejects(function () { cookie.set("value", "x", symbols); }, "symbol cookie option");

  assert(cookie.set("none", "safe", { path: "/unseen", sameSite: "none", secure: true }) === true,
    "secure SameSite=None cookie was rejected");
  var secureWrite = globalThis.__cookieWrites[globalThis.__cookieWrites.length - 1];
  assert(secureWrite === "none=safe; Path=/unseen; SameSite=None; Secure",
    "secure cookie assignment was " + secureWrite);

  document.getElementById("html-cookie").click();
  await nextTurn();
  assert(document.getElementById("html-network").textContent.trim() === "server-only" &&
    cookie.has("html-poison") === false,
    "authored HTML obtained direct access to a sealed service");
});
</script></body></html>`

const networkRetentionDocument = `<!doctype html><html lang="en"><head><meta charset="utf-8">
<title>Network subscriber retention</title><script>
(function () {
  "use strict";
  var add = window.addEventListener;
  var remove = window.removeEventListener;
  globalThis.__networkRetention = {
    adds: { online: 0, offline: 0 },
    removes: { online: 0, offline: 0 }
  };
  window.addEventListener = function (type, listener, options) {
    if (type === "online" || type === "offline") globalThis.__networkRetention.adds[type]++;
    return add.call(this, type, listener, options);
  };
  window.removeEventListener = function (type, listener, options) {
    if (type === "online" || type === "offline") globalThis.__networkRetention.removes[type]++;
    return remove.call(this, type, listener, options);
  };
})();
</script><script src=%q></script></head><body><script>
(function () {
  "use strict";
  var root = document.documentElement;
  function finish(status, error) {
    root.setAttribute("data-kit-retention-test", status);
    if (error) root.setAttribute("data-kit-retention-error", String(error && error.message || error));
  }
  function fail(message) { throw new Error(message); }
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
  function releaseSubscribers() {
    var refs = [];
    for (var index = 0; index < 32; index++) {
      var owner = { index: index };
      var listener = (function (captured) {
        return function () { return captured.index; };
      })(owner);
      refs.push(new WeakRef(owner), new WeakRef(listener));
      var stop = globalThis.kit.network.subscribe(listener);
      stop();
      stop();
      stop = null;
      listener = null;
      owner = null;
    }
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
    if (alive(controlRefs) !== 0) {
      finish("unsupported", "forced GC retained control objects");
      return;
    }
    if (alive(refs) !== 0) fail("network retained an unsubscribed callback or its captured owner");
    var lifecycle = globalThis.__networkRetention;
    if (lifecycle.adds.online !== 32 || lifecycle.adds.offline !== 32 ||
      lifecycle.removes.online !== 32 || lifecycle.removes.offline !== 32) {
      fail("network first/last event lifecycle was not balanced: " + JSON.stringify(lifecycle));
    }
    finish("passed");
  }
  function run() {
    try {
      if (typeof WeakRef !== "function" || typeof globalThis.gc !== "function") {
        finish("unsupported", "WeakRef or forced gc() is unavailable");
        return;
      }
      var refs = releaseSubscribers();
      setTimeout(function () { collect(refs, controls(), 0); }, 0);
    } catch (error) { finish("failed", error); }
  }
  window.addEventListener("error", function (event) { finish("failed", event.error || event.message); });
  window.addEventListener("unhandledrejection", function (event) { finish("failed", event.reason); });
  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", function () { setTimeout(run, 0); }, { once: true });
  } else setTimeout(run, 0);
})();
</script></body></html>`
