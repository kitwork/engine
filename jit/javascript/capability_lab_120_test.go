package javascript

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

const capabilityLab120SHA256 = "aad75d667488b0989d4c0ab6cd7c5186142468d3592632c5c90508ae170fcebd"

func TestCapabilityLab120PinsStrictDeepLinksAndFreezesSource(t *testing.T) {
	source := readVanillaFile(t, "component", "capability-lab", "1.2.0.js")
	if got := ContentHash(source); got != capabilityLab120SHA256 {
		t.Fatalf("capability-lab@1.2.0 bytes changed: %s", got)
	}
	for _, marker := range [][]byte{
		[]byte(`capability-lab@1.2.0`),
		[]byte(`notificationTapArms = new WeakMap()`),
		[]byte(`snapshot.id !== arm.baselineID`),
		[]byte(`matching-route-received`),
		[]byte(`awaiting-matching-route`),
		[]byte(`không xác nhận nguồn hay thao tác chạm vật lý`),
	} {
		if !bytes.Contains(source, marker) {
			t.Fatalf("capability-lab@1.2.0 lost %q", marker)
		}
	}

	catalog, err := loadDeliveryCatalog()
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := catalog.component("capability-lab", "1.1.0")
	if err != nil {
		t.Fatal(err)
	}
	current, err := catalog.component("capability-lab", "1.2.0")
	if err != nil {
		t.Fatal(err)
	}
	legacyVersions := serviceVersionMap(legacy.requires)
	currentVersions := serviceVersionMap(current.requires)
	if legacyVersions["deepLinks"] != "1.0.0" || currentVersions["deepLinks"] != "1.1.0" ||
		legacyVersions["notifications"] != "1.1.0" || currentVersions["notifications"] != "1.1.0" {
		t.Fatalf("capability-lab service pins: 1.1=%#v 1.2=%#v", legacyVersions, currentVersions)
	}
	delete(legacyVersions, "deepLinks")
	delete(currentVersions, "deepLinks")
	if !reflect.DeepEqual(currentVersions, legacyVersions) {
		t.Fatalf("capability-lab@1.2.0 changed unrelated service pins: 1.1=%#v 1.2=%#v",
			legacyVersions, currentVersions)
	}
}

func TestCapabilityLab120RequiresCurrentSuccessfulArmBeforeMatchingRoutePasses(t *testing.T) {
	source := readVanillaFile(t, "component", "capability-lab", "1.2.0.js")
	script := `
"use strict";
function assert(value, message) { if (!value) throw new Error(message); }
var definition = null;
var deepLinkListener = null;
var tapPath = "/capability-lab?source=notification#tap";
var snapshotValue = Object.freeze({ id: "11111111111111111111111111111111", path: tapPath });
var shownPayload = null;
var kit = {
  component: function (name, value) {
    assert(name === "capability-lab" && definition === null, "unexpected component registration");
    definition = value;
  },
  capabilities: {
    supports: function (name) { return Promise.resolve(name === "notifications.tap"); }
  },
  deepLinks: {
    snapshot: function () { return Promise.resolve(snapshotValue); },
    subscribe: function (listener) {
      deepLinkListener = listener;
      return function () { deepLinkListener = null; };
    }
  },
  notifications: {
    permission: function () { return Promise.resolve("granted"); },
    show: function (payload) { shownPayload = payload; return Promise.resolve(true); }
  }
};
globalThis.kit = kit;
globalThis.document = { visibilityState: "visible" };
` + string(source) + `

function row(instance, id) {
  return instance.items.filter(function (item) { return item.id === id; })[0];
}

(async function () {
  var instance = {};
  Object.keys(definition).forEach(function (key) { instance[key] = definition[key]; });
  var cleanup = null;
  await instance.init({
    listen: function () {},
    cleanup: function (value) { cleanup = value; }
  });
  assert(typeof cleanup === "function" && typeof deepLinkListener === "function",
    "deep-link subscription was not lifecycle owned");

  deepLinkListener(snapshotValue);
  assert(row(instance, "notification-tap").result !== "pass",
    "a matching route passed before this component armed a notification");

  assert(await instance.showNotification() === true, "routed notification was not shown");
  assert(shownPayload && shownPayload.path === tapPath,
    "notification did not carry the exact probe route");
  assert(row(instance, "notification-tap").result === "running" &&
    row(instance, "notification-tap").detail === "awaiting-matching-route",
    "successful show did not arm a matching-route observation");
  assert(instance.statusText.indexOf("không xác nhận nguồn hay thao tác chạm vật lý") >= 0,
    "armed state overclaimed proof of a physical tap");

  assert(await instance.checkDeepLink() === true, "baseline snapshot was not readable");
  assert(row(instance, "notification-tap").result === "running",
    "the pre-arm baseline snapshot proved the route");
  deepLinkListener(Object.freeze({ id: "22222222222222222222222222222222", path: "/orders/42" }));
  assert(row(instance, "notification-tap").result === "running",
    "an unrelated route proved the notification route");
  deepLinkListener(Object.freeze({ id: "33333333333333333333333333333333", path: tapPath }));
  assert(row(instance, "notification-tap").result === "pass" &&
    row(instance, "notification-tap").detail === "matching-route-received",
    "a new matching route did not satisfy the armed observation");
  assert(instance.statusText.indexOf("chỉ xác nhận route trong inbox") >= 0,
    "matching route overclaimed its source");

  deepLinkListener(Object.freeze({ id: "44444444444444444444444444444444", path: tapPath }));
  assert(row(instance, "notification-tap").detail === "matching-route-received",
    "completed observation was not one-shot");
  cleanup();
  assert(deepLinkListener === null, "cleanup retained the deep-link subscription");
})().catch(function (error) {
  console.error(error && error.stack || error);
  process.exitCode = 1;
});
`
	runNativeHostNode(t, script)
}

func TestCapabilityLab120WordingDoesNotClaimTapProvenance(t *testing.T) {
	source := string(readVanillaFile(t, "component", "capability-lab", "1.2.0.js"))
	for _, forbidden := range []string{"tap-proven", "tap-confirmed", "source=notification proves"} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("capability-lab@1.2.0 overclaims notification provenance with %q", forbidden)
		}
	}
}
