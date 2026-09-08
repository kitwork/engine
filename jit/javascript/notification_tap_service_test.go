package javascript

import (
	"bytes"
	"strings"
	"testing"
)

const notificationsService100SHA256 = "1df5b7e92f06d8162e9f0fc187d64129612adbf9a6994498512ef23bdd80f498"
const notificationsService110SHA256 = "0994495431dc311b71cc01cc2e86681626dfe8125d7c61c1dfabdcabc44ef122"

func notificationService110Package(t *testing.T) Service {
	t.Helper()
	return Service{
		Name:    "notifications",
		Version: "1.1.0",
		Source:  readVanillaFile(t, "service", "notifications", "1.1.0.js"),
	}
}

func TestNotificationService110AddsOnlyCanonicalTapPath(t *testing.T) {
	legacy := notificationServicePackage(t).Source
	if got := ContentHash(legacy); got != notificationsService100SHA256 {
		t.Fatalf("notifications@1.0.0 bytes changed: %s", got)
	}
	if bytes.Contains(legacy, []byte(`path: true`)) || bytes.Contains(legacy, []byte(`params.path`)) {
		t.Fatal("notifications@1.0.0 gained notification-tap routing")
	}

	service := notificationService110Package(t)
	if got := ContentHash(service.Source); got != notificationsService110SHA256 {
		t.Fatalf("notifications@1.1.0 bytes changed: %s", got)
	}
	if len(service.Source) == 0 || service.Source[0] != ';' ||
		service.Source[len(service.Source)-1] != '\n' || bytes.Contains(service.Source, []byte{'\r'}) {
		t.Fatal("notifications@1.1.0 is not a sealable LF-only classic script")
	}
	for _, marker := range [][]byte{
		[]byte(`notifications@1.1.0`),
		[]byte(`path: true`),
		[]byte(`MAX_PATH_BYTES = 2048`),
		[]byte(`params.path = data.path`),
		[]byte(`notificationError("UNAVAILABLE", "show")`),
		[]byte(`kit.service("notifications", {`),
	} {
		if !bytes.Contains(service.Source, marker) {
			t.Fatalf("notifications@1.1.0 lost contract %q", marker)
		}
	}
	for _, forbidden := range [][]byte{
		[]byte(`addEventListener(`), []byte(`onclick`), []byte(`location.assign`),
		[]byte(`location.href`), []byte(`kit:navigation`), []byte(`kitwork:deep-link`),
		[]byte(`serviceWorker`), []byte(`PushManager`), []byte(`showNotification`),
	} {
		if bytes.Contains(service.Source, forbidden) {
			t.Fatalf("notifications@1.1.0 gained forbidden tap/navigation behavior %q", forbidden)
		}
	}

	catalog, err := loadDeliveryCatalog()
	if err != nil {
		t.Fatal(err)
	}
	legacyCatalog, err := catalog.service(ServiceVersion{Name: "notifications", Version: "1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	current, err := catalog.service(ServiceVersion{Name: "notifications", Version: "1.1.0"})
	if err != nil {
		t.Fatal(err)
	}
	if legacyCatalog.identity.Version != "1.0.0" || current.identity.Version != "1.1.0" ||
		bytes.Equal(legacyCatalog.source, current.source) || !bytes.Equal(current.source, service.Source) {
		t.Fatal("notification service versions are not distinct exact catalog packages")
	}
	artifact, err := Build(BuildOptions{Profile: ProfileKit, Services: []Service{service}})
	if err != nil {
		t.Fatal(err)
	}
	if graph := string(artifact.Bytes()); !strings.Contains(graph, `services["notifications"] = "1.1.0";`) ||
		strings.Contains(graph, `services["notifications"] = "1.0.0";`) {
		t.Fatalf("notifications@1.1.0 graph metadata is not exact")
	}
}

func TestNotificationService110PathProvidersNodeContract(t *testing.T) {
	source := notificationService110Package(t).Source
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
  var nativeCalls = [];
  var native = install({
    call: function (action, params) {
      nativeCalls.push({ action: action, params: params });
      return Promise.resolve(true);
    }
  });
  assert(Object.keys(native).join(",") === "permission,requestPermission,show",
    "notifications@1.1.0 added a tap event or API");

  var valid = [
    "/",
    "/orders/42?tab=history#receipt",
    "/search?q=%E2%82%AC",
    "/query?next=%252Forders"
  ];
  for (var index = 0; index < valid.length; index++) {
    var route = valid[index];
    assert(await native.show({ title: "Kitwork", body: "Tap", path: route }) === true,
      "valid route was rejected: " + route);
    var call = nativeCalls[nativeCalls.length - 1];
    assert(call.action === "notifications.show" && call.params.path === route,
      "canonical route was changed before native validation");
  }

  var nested = "%E2%82%AC";
  for (var pass = 1; pass < 8; pass++) nested = nested.replace(/%/g, "%25");
  assert(await native.show({ title: "Kitwork", body: "Tap", path: "/query?q=" + nested }) === true,
    "exact eight-pass route was rejected");
  nested = nested.replace(/%/g, "%25");

  var invalid = [
    "", "relative", "//authority", "/space here", "/unicode/€", "/bad\\path",
    "/empty?", "/empty#", "/bad?#fragment", "/two#fragments#here", "/raw/{value}",
    "/lower/%e2%82%ac", "/encoded/%41", "/dot/../route", "/dot/%252E%252E/route",
    "/slash%2Froute", "/slash%252Froute", "/invalid/%FF", "/query?q=" + nested,
    "/" + "x".repeat(2048)
  ];
  var beforeInvalid = nativeCalls.length;
  for (var invalidIndex = 0; invalidIndex < invalid.length; invalidIndex++) {
    var threw = false;
    try { native.show({ title: "Kitwork", body: "Tap", path: invalid[invalidIndex] }); }
    catch (error) { threw = error instanceof TypeError; }
    assert(threw, "invalid route was accepted: " + invalid[invalidIndex]);
  }
  assert(nativeCalls.length === beforeInvalid, "invalid route reached the native host");

  var reads = 0;
  var accessor = { title: "Kitwork", body: "Tap" };
  Object.defineProperty(accessor, "path", {
    enumerable: true,
    get: function () { reads++; return "/private"; }
  });
  var accessorRejected = false;
  try { native.show(accessor); }
  catch (error) { accessorRejected = error instanceof TypeError; }
  assert(accessorRejected && reads === 0 && nativeCalls.length === beforeInvalid,
    "notification path accessor was evaluated or reached native");

  var created = [];
  function BrowserNotification(title, options) { created.push({ title: title, options: options }); }
  BrowserNotification.permission = "granted";
  BrowserNotification.requestPermission = function () { return Promise.resolve("granted"); };
  Object.defineProperty(globalThis, "Notification", {
    configurable: true,
    writable: true,
    value: BrowserNotification
  });
  var browser = install(null);
  var unavailable = await rejected(browser.show({ title: "Kitwork", body: "Tap", path: "/orders/42" }));
  assert(unavailable.name === "KitNotificationError" && unavailable.code === "UNAVAILABLE" &&
    unavailable.operation === "show" && Object.isFrozen(unavailable) && created.length === 0,
    "browser silently discarded a native tap route");
  assert(await browser.show({ title: "Kitwork", body: "Plain" }) === true && created.length === 1,
    "notifications@1.1.0 changed the path-free browser fallback");
})().catch(function (error) {
  console.error(error && error.stack || error);
  process.exitCode = 1;
});
`
	runNativeHostNode(t, script)
}
