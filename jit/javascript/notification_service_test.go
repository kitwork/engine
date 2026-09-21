package javascript

import (
	"bytes"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func notificationServicePackage(t *testing.T) Service {
	t.Helper()
	return Service{
		Name:    "notifications",
		Version: "1.0.0",
		Source:  readVanillaFile(t, "service", "notifications", "1.0.0.js"),
	}
}

func TestNotificationServiceSourceIsClosedAndBounded(t *testing.T) {
	source := notificationServicePackage(t).Source
	if !bytes.HasPrefix(bytes.TrimSpace(source), []byte(";")) || bytes.Contains(source, []byte{'\r'}) {
		t.Fatal("notifications@1.0.0 is not a sealable LF-only classic script")
	}
	if got := bytes.Count(source, []byte(`kit.service("notifications"`)); got != 1 {
		t.Fatalf("notification registration count = %d, want one", got)
	}
	for _, marker := range [][]byte{
		[]byte(`nativeHost.call("notifications." + action, params)`),
		[]byte(`MAX_TITLE_UNITS = 256`),
		[]byte(`MAX_BODY_UNITS = 4096`),
		[]byte(`MAX_TAG_BYTES = 128`),
		[]byte(`permission: permission`),
		[]byte(`requestPermission: requestPermission`),
		[]byte(`show: show`),
	} {
		if !bytes.Contains(source, marker) {
			t.Fatalf("notifications@1.0.0 lost contract %q", marker)
		}
	}
	for _, forbidden := range [][]byte{
		[]byte("serviceWorker"), []byte("PushManager"), []byte("pushManager"),
		[]byte("showNotification"), []byte("setTimeout"), []byte("schedule"),
		[]byte("fetch("), []byte("XMLHttpRequest"), []byte("kit.component("),
	} {
		if bytes.Contains(source, forbidden) {
			t.Fatalf("notifications@1.0.0 widened into forbidden contract %q", forbidden)
		}
	}
}

func TestNotificationServiceBuildsAsOneExactPackage(t *testing.T) {
	service := notificationServicePackage(t)
	artifact, err := Build(BuildOptions{Profile: ProfileKit, Services: []Service{service}})
	if err != nil {
		t.Fatal(err)
	}
	if got := bytes.Count(artifact.Bytes(), service.Source); got != 1 {
		t.Fatalf("standalone notification source count = %d, want one", got)
	}
	if graph := string(artifact.Bytes()); !strings.Contains(graph, `services["notifications"] = "1.0.0";`) {
		t.Fatal("standalone notification graph metadata was missing")
	}
	staged, err := BuildStaged(StagedBuildOptions{Profile: ProfileHydrate, Services: []Service{service}})
	if err != nil {
		t.Fatal(err)
	}
	if len(staged.Services) != 1 || staged.Services[0].Package() != "notifications" ||
		staged.Services[0].Version() != "1.0.0" {
		t.Fatalf("staged notification artifact = %#v", staged.Services)
	}
	if appGrantsAuthoredService("1.7.0", "notifications") {
		t.Fatal("app@1.7.0 exposed notifications to authored HTML")
	}
	for _, member := range []string{"permission", "requestPermission", "show"} {
		if validAuthoredServiceAction("notifications", member) {
			t.Fatalf("notification member %q escaped into authored actions", member)
		}
	}
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	for _, expression := range []string{
		`$app.notifications.requestPermission()`,
		`$app['notifications'].requestPermission()`,
		`$app.notifications?.requestPermission()`,
		`$app.notifications.requestPermission?.()`,
	} {
		source := `<main data-kit-component="app@1.7.0" data-kit-alias="$app"><button data-kit-click="` + expression + `"></button></main>`
		if _, err := composer.ComposeHTML([]byte(source)); err == nil {
			t.Fatalf("authored HTML gained private notification access through %s", expression)
		}
	}
}

func TestBrowserNotificationFallbackContract(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping notification browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	artifact, err := Build(BuildOptions{Profile: ProfileKit, Services: []Service{notificationServicePackage(t)}})
	if err != nil {
		t.Fatal(err)
	}
	assetPath := "/assets/" + artifact.Name()
	var packageRequests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case assetPath:
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(artifact.Bytes())
		case "/notification.html":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = fmt.Fprintf(response, notificationFallbackDocument, assetPath)
		case "/service/notifications/1.0.0.js", "/notifications.js":
			packageRequests.Add(1)
			http.Error(response, "notification package must already be sealed", http.StatusGone)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	runVanillaBrowser(t, browser, server.URL+"/notification.html")
	if got := packageRequests.Load(); got != 0 {
		t.Fatalf("browser fetched notification package %d times", got)
	}
}

func TestBrowserNativeNotificationContract(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping native notification browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	assembly, err := BuildStaged(StagedBuildOptions{
		Profile:  ProfileHydrate,
		Services: []Service{notificationServicePackage(t)},
	})
	if err != nil {
		t.Fatal(err)
	}
	assets := make(map[string][]byte, len(assembly.Artifacts()))
	for _, artifact := range assembly.Artifacts() {
		assets["/jit/"+artifact.Name()] = artifact.Bytes()
	}
	page := nativeNotificationDocument(assembly)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if source, exists := assets[request.URL.Path]; exists {
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(source)
			return
		}
		if request.URL.Path == "/" {
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(page))
			return
		}
		http.NotFound(response, request)
	}))
	defer server.Close()
	runVanillaBrowser(t, browser, server.URL+"/")
}

func TestNotificationProvidersNodeContract(t *testing.T) {
	source := notificationServicePackage(t).Source
	script := `
"use strict";
var ASSEMBLY = Symbol.for("kitjs:assembly");
function assert(value, message) { if (!value) throw new Error(message); }
async function rejected(promise) {
  try { await promise; } catch (error) { return error; }
  throw new Error("promise unexpectedly resolved");
}
function install(nativeHost) {
  var document = {};
  Object.defineProperty(document, ASSEMBLY, {
    configurable: true,
    value: { nativeHost: nativeHost }
  });
  globalThis.document = document;
  var namespace = null;
  var kit = {
    service: function (name, value) {
      assert(name === "notifications" && namespace === null, "unexpected service registration");
      namespace = Object.freeze(value);
    }
  };
` + string(source) + `
  return namespace;
}

(async function () {
  var browserCreated = [];
  var browserRequests = 0;
  function BrowserNotification(title, options) {
    browserCreated.push({ title: title, options: options });
  }
  BrowserNotification.permission = "default";
  BrowserNotification.requestPermission = function () {
    browserRequests++;
    return Promise.resolve("granted");
  };
  Object.defineProperty(globalThis, "Notification", {
    configurable: true,
    writable: true,
    value: BrowserNotification
  });

  var browser = install(null);
  var failure = await rejected(browser.show({ title: "Kitwork", body: "Explicit permission" }));
  assert(failure.name === "KitNotificationError" && failure.code === "DENIED" &&
    browserRequests === 0 && browserCreated.length === 0,
    "browser show implicitly requested permission or constructed a notification");
  assert(await browser.permission() === "prompt", "browser default permission did not map to prompt");

  var getterReads = 0;
  Object.defineProperty(BrowserNotification, "requestPermission", {
    configurable: true,
    get: function () {
      getterReads++;
      return function () { browserRequests++; return Promise.resolve("granted"); };
    }
  });
  assert(await browser.requestPermission() === "granted" && getterReads === 1 && browserRequests === 1,
    "browser requestPermission did not capture and call one exact method");
  Object.defineProperty(BrowserNotification, "requestPermission", {
    configurable: true,
    get: function () { throw new Error("raw browser secret"); }
  });
  var escapedSynchronously = false;
  var permissionPromise;
  try { permissionPromise = browser.requestPermission(); }
  catch (_) { escapedSynchronously = true; }
  failure = await rejected(permissionPromise);
  assert(!escapedSynchronously && failure.name === "KitNotificationError" &&
    failure.code === "FAILED" && failure.operation === "requestPermission" &&
    Object.isFrozen(failure) && failure.message.indexOf("secret") < 0,
    "browser requestPermission getter escaped or leaked its raw failure");

  BrowserNotification.permission = "granted";
  assert(await browser.show({ title: "Kitwork", body: "Ready", tag: "lab.ready" }) === true &&
    browserCreated.length === 1 && browserCreated[0].options.tag === "lab.ready",
    "browser show did not post one exact notification");

  var calls = [];
  var state = "prompt";
  var invalidPermission = false;
  var invalidShow = false;
  var rawFailure = false;
  var nativeHost = {
    call: function (action, params) {
      calls.push({ action: action, params: params });
      if (rawFailure) return Promise.reject({ code: "DENIED", message: "raw native secret" });
      if (action === "notifications.permission") {
        if (invalidPermission) return Promise.resolve("default");
        return Promise.resolve(state);
      }
      if (action === "notifications.requestPermission") {
        state = "granted";
        return Promise.resolve("granted");
      }
      if (action === "notifications.show") {
        if (state !== "granted") return Promise.reject({ code: "NATIVE_ERROR", message: "raw native secret" });
        return Promise.resolve(invalidShow ? false : true);
      }
      throw new Error("unexpected native notification action");
    }
  };
  var native = install(nativeHost);
  var beforeBrowserCreated = browserCreated.length;
  failure = await rejected(native.show({ title: "Kitwork", body: "Explicit permission" }));
  assert(failure.code === "FAILED" && calls.length === 1 &&
    calls[0].action === "notifications.show" && browserCreated.length === beforeBrowserCreated,
    "native show queried permission, prompted, or fell back to the browser");
  assert(await native.requestPermission() === "granted" && calls.length === 2 &&
    calls[1].action === "notifications.requestPermission",
    "explicit native permission request changed action shape");
  assert(await native.show({ title: "Kitwork", body: "Ready" }) === true && calls.length === 3,
    "native show did not accept exact true");

  var maximumTitle = "😀".repeat(128);
  var maximumBody = "😀".repeat(2048);
  var maximumTag = "x".repeat(128);
  var beforeBoundary = calls.length;
  assert(await native.show({ title: maximumTitle, body: maximumBody, tag: maximumTag }) === true &&
    calls.length === beforeBoundary + 1 && calls[calls.length - 1].params.title.length === 256 &&
    calls[calls.length - 1].params.body.length === 4096 &&
    calls[calls.length - 1].params.tag.length === 128,
    "exact UTF-16 and ASCII notification boundaries were rejected or changed");
  var beforeInvalidBoundaries = calls.length;
  var invalidBoundaries = [
    { title: maximumTitle + "A", body: "x" },
    { title: "x", body: maximumBody + "A" },
    { title: "bad\u007f", body: "x" },
    { title: "x", body: "bad\u0085" }
  ];
  var boundaryFailures = 0;
  invalidBoundaries.forEach(function (input) {
    try { native.show(input); }
    catch (error) { if (error instanceof TypeError) boundaryFailures++; }
  });
  assert(boundaryFailures === invalidBoundaries.length && calls.length === beforeInvalidBoundaries,
    "oversized UTF-16 or C1 notification text reached the native host");

  invalidPermission = true;
  failure = await rejected(native.permission());
  assert(failure.code === "FAILED", "native default escaped the closed permission enum");
  invalidPermission = false;
  invalidShow = true;
  failure = await rejected(native.show({ title: "Kitwork", body: "Ready" }));
  assert(failure.code === "FAILED", "native false escaped the exact show acknowledgment");
  invalidShow = false;
  rawFailure = true;
  failure = await rejected(native.permission());
  assert(failure.code === "DENIED" && failure.operation === "permission" &&
    Object.isFrozen(failure) && failure.message.indexOf("secret") < 0,
    "native notification failure was not frozen and redacted");
  rawFailure = false;

  var beforeInvalid = calls.length;
  var typeError = false;
  try { native.show({ title: "Kitwork", body: "Ready", unknown: true }); }
  catch (error) { typeError = error instanceof TypeError; }
  assert(typeError && calls.length === beforeInvalid,
    "invalid notification input reached the native host");
})().catch(function (error) {
  console.error(error && error.stack || error);
  process.exitCode = 1;
});
`
	runNativeHostNode(t, script)
}

func nativeNotificationDocument(assembly StagedAssembly) string {
	var tags strings.Builder
	for _, artifact := range assembly.Artifacts() {
		tags.WriteString(`<script data-kitwork-jit="` + html.EscapeString(string(artifact.Role())) +
			`" data-kitwork-hash="` + artifact.SHA256() + `" src="/jit/` + html.EscapeString(artifact.Name()) +
			`" integrity="` + html.EscapeString(artifact.Integrity()) + `" crossorigin="anonymous" defer></script>` + "\n")
	}
	return `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Native notifications</title>
<script>
(function () {
  "use strict";
  var HOST = Symbol.for("kitwork:native:host:v1");
  var calls = [];
  var state = "prompt";
  var invalidPermission = false;
  var adapter = {
    version: "1.0.0",
    call: function (action, params) {
      calls.push({ action: action, params: params, frozen: Object.isFrozen(params), receiver: this === adapter });
      if (action === "notifications.permission") {
        if (invalidPermission) { invalidPermission = false; return Promise.resolve("default"); }
        return Promise.resolve(state);
      }
      if (action === "notifications.requestPermission") {
        state = "granted";
        return Promise.resolve("granted");
      }
      if (action === "notifications.show") {
        if (state !== "granted") return Promise.reject({ code: "NATIVE_ERROR", message: "private detail" });
        return Promise.resolve(true);
      }
      return Promise.reject({ code: "UNAVAILABLE", message: "private detail" });
    }
  };
  Object.defineProperty(document, HOST, { configurable: true, value: adapter });
  globalThis.__nativeNotification = {
    calls: calls,
    invalidatePermission: function () { invalidPermission = true; }
  };
})();
</script>
` + tags.String() + `</head><body><script>
` + browserHarness + `
__runStandaloneKitTest(async function () {
  "use strict";
  var assert = __kitTestAssert;
  var notifications = globalThis.kit.notifications;
  var probe = globalThis.__nativeNotification;
  assert(probe.calls.length === 0, "package evaluation invoked native notification code");

  var promptShowRejected = false;
  try { await notifications.show({ title: "Kitwork", body: "Explicit permission only" }); }
  catch (error) {
    promptShowRejected = error.name === "KitNotificationError" && error.code === "FAILED" &&
      error.operation === "show";
  }
  assert(promptShowRejected && probe.calls.length === 1 &&
    probe.calls[0].action === "notifications.show",
    "show while prompt queried or requested permission implicitly");

  assert(await notifications.permission() === "prompt", "native permission result changed");
  assert(probe.calls.length === 2 && probe.calls[1].action === "notifications.permission" &&
    Object.keys(probe.calls[1].params).length === 0 && probe.calls[1].frozen && probe.calls[1].receiver,
    "permission did not use the sealed native call");
  assert(await notifications.requestPermission() === "granted",
    "native requestPermission result changed");
  assert(probe.calls.length === 3 && probe.calls[2].action === "notifications.requestPermission" &&
    Object.keys(probe.calls[2].params).length === 0,
    "requestPermission did not use its exact native action");
  assert(await notifications.show({ title: "Kitwork", body: "Ready", tag: "lab.ready" }) === true,
    "native show did not resolve exact true");
  assert(probe.calls.length === 4 && probe.calls[3].action === "notifications.show" &&
    probe.calls[3].params.title === "Kitwork" && probe.calls[3].params.body === "Ready" &&
    probe.calls[3].params.tag === "lab.ready",
    "native show did not send its one exact payload");
  probe.invalidatePermission();
  var invalid = false;
  try { await notifications.permission(); }
  catch (error) { invalid = error.name === "KitNotificationError" && error.code === "FAILED"; }
  assert(invalid, "native default permission escaped as a public enum");
});
</script></body></html>`
}

const notificationFallbackDocument = `<!doctype html><html lang="en"><head><meta charset="utf-8">
<title>Notification service contract</title><script>
(function () {
  "use strict";
  var probe = globalThis.__notificationProbe = { created: [], requests: 0 };
  function FakeNotification(title, options) {
    probe.created.push({ title: title, options: options });
  }
  FakeNotification.permission = "default";
  FakeNotification.requestPermission = function () {
    probe.requests++;
    FakeNotification.permission = "granted";
    return Promise.resolve("granted");
  };
  Object.defineProperty(globalThis, "Notification", {
    configurable: true, writable: true, value: FakeNotification
  });
  probe.Notification = FakeNotification;
})();
</script><script src=%q></script></head><body><script>
` + browserHarness + `
__runStandaloneKitTest(async function () {
  "use strict";
  var assert = __kitTestAssert;
  var notifications = globalThis.kit.notifications;
  var probe = globalThis.__notificationProbe;
  assert(Object.keys(globalThis.kit).join(",") === "version,component,notifications",
    "notification service keys were " + Object.keys(globalThis.kit).join(","));
  assert(Object.isFrozen(notifications) && notifications.version === "1.0.0",
    "notification namespace was not exact and frozen");
  assert(Object.keys(notifications).join(",") === "permission,requestPermission,show",
    "notification members were " + Object.keys(notifications).join(","));

  var promptShowDenied = false;
  try { await notifications.show({ title: "Kitwork", body: "Explicit permission only" }); }
  catch (error) {
    promptShowDenied = error.name === "KitNotificationError" && error.code === "DENIED" &&
      error.operation === "show";
  }
  assert(promptShowDenied && probe.requests === 0 && probe.created.length === 0,
    "browser show while prompt requested permission or constructed a notification");

  assert(await notifications.permission() === "prompt", "default permission did not map to prompt");
  assert(await notifications.requestPermission() === "granted" && probe.requests === 1,
    "requestPermission did not use the platform prompt once");
  assert(await notifications.permission() === "granted", "granted permission was not observable");

  var requestReads = 0;
  Object.defineProperty(probe.Notification, "requestPermission", {
    configurable: true,
    get: function () {
      requestReads++;
      return function () { return Promise.resolve("granted"); };
    }
  });
  assert(await notifications.requestPermission() === "granted" && requestReads === 1,
    "requestPermission read a platform method more than once");
  Object.defineProperty(probe.Notification, "requestPermission", {
    configurable: true,
    get: function () { throw new Error("private platform detail"); }
  });
  var rawSynchronous = false;
  var accessorFailure;
  try { accessorFailure = notifications.requestPermission(); }
  catch (_) { rawSynchronous = true; }
  var accessorRejected = false;
  try { await accessorFailure; }
  catch (error) {
    accessorRejected = error.name === "KitNotificationError" && error.code === "FAILED" &&
      error.operation === "requestPermission" && Object.isFrozen(error) &&
      String(error.message).indexOf("private") < 0;
  }
  assert(!rawSynchronous && accessorRejected,
    "requestPermission accessor escaped synchronously or leaked its raw failure");
  assert(await notifications.show({ title: "Kitwork", body: "Đã sẵn sàng", tag: "lab.ready" }) === true,
    "show did not resolve true");
  assert(probe.created.length === 1 && probe.created[0].title === "Kitwork" &&
    probe.created[0].options.body === "Đã sẵn sàng" && probe.created[0].options.tag === "lab.ready",
    "show did not pass the bounded payload to Notification");

  probe.Notification.permission = "denied";
  var denied = false;
  try { await notifications.show({ title: "Kitwork", body: "Denied" }); }
  catch (error) { denied = error.name === "KitNotificationError" && error.code === "DENIED" && error.operation === "show"; }
  assert(denied && probe.created.length === 1, "denied show reached the platform constructor");

  function rejects(operation, label) {
    var rejected = false;
    try { operation(); } catch (error) { rejected = error instanceof TypeError; }
    assert(rejected, label + " was accepted");
  }
  rejects(function () { notifications.permission(1); }, "permission argument");
  rejects(function () { notifications.requestPermission(1); }, "requestPermission argument");
  rejects(function () { notifications.show(); }, "missing payload");
  rejects(function () { notifications.show(null); }, "null payload");
  rejects(function () { notifications.show({ title: "", body: "x" }); }, "empty title");
  rejects(function () { notifications.show({ title: "bad\n", body: "x" }); }, "control title");
  rejects(function () { notifications.show({ title: "x".repeat(257), body: "x" }); }, "oversized title");
  rejects(function () { notifications.show({ title: "x", body: "\uD800" }); }, "unpaired surrogate body");
  rejects(function () { notifications.show({ title: "x", body: "x".repeat(4097) }); }, "oversized body");
  rejects(function () { notifications.show({ title: "x", body: "x", tag: "-unsafe" }); }, "unsafe tag");
  rejects(function () { notifications.show({ title: "x", body: "x", tag: "x".repeat(129) }); }, "oversized tag");
  rejects(function () { notifications.show({ title: "x", body: "x", url: "/private" }); }, "unknown field");
  var reads = 0;
  var accessor = { body: "x" };
  Object.defineProperty(accessor, "title", { enumerable: true, get: function () { reads++; return "x"; } });
  rejects(function () { notifications.show(accessor); }, "payload accessor");
  var hidden = { body: "x" };
  Object.defineProperty(hidden, "title", { enumerable: false, value: "x" });
  rejects(function () { notifications.show(hidden); }, "non-enumerable payload field");
  assert(reads === 0, "notification payload accessor was evaluated");
});
</script></body></html>`
